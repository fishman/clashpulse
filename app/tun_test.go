package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/monitor"
	"github.com/fishman/clashpulse/tun"
)

type fakeTun struct {
	privilege                 atomic.Value
	prepares, revokes, checks atomic.Int32
	// outcome runs inside Prepare before the copy is granted; set it before
	// the service loop starts.
	outcome func(context.Context) error
}

func installFakeTun(t *testing.T, h *staticRuntime, initial tun.Privilege) *fakeTun {
	t.Helper()
	fake := &fakeTun{}
	fake.privilege.Store(initial)
	// TUN needs a profile resolver for dns-hijack to reach.
	h.profile.Store(new(twoNodeProfile + "dns:\n  enable: true\n  nameserver: [192.0.2.53]\n"))
	h.service.tun.check = func(tun.Source) tun.Privilege {
		fake.checks.Add(1)
		return fake.privilege.Load().(tun.Privilege)
	}
	h.service.tun.prepare = func(ctx context.Context, source tun.Source) error {
		fake.prepares.Add(1)
		if fake.outcome != nil {
			if err := fake.outcome(ctx); err != nil {
				return err
			}
		}
		if err := copyTunBinary(source.Path, h.service.tun.path); err != nil {
			return err
		}
		fake.privilege.Store(tun.PrivilegeReady)
		return nil
	}
	h.service.tun.revoke = func() error {
		fake.revokes.Add(1)
		fake.privilege.Store(tun.PrivilegeMissing)
		if err := os.Remove(h.service.tun.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return fake
}

func copyTunBinary(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o700)
}

// grantTun stands in for an earlier successful Prepare.
func grantTun(t *testing.T, h *staticRuntime) {
	t.Helper()
	if err := copyTunBinary(filepath.Join(h.root, "mihomo"), h.service.tun.path); err != nil {
		t.Fatal(err)
	}
}

func enableTunIntent(t *testing.T, h *staticRuntime) {
	t.Helper()
	path := filepath.Join(h.configDir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Write(path, append(data, "[tun]\nenabled = true\n"...)); err != nil {
		t.Fatal(err)
	}
	next, err := config.Load(h.configDir)
	if err != nil {
		t.Fatal(err)
	}
	h.service.store.Replace(next)
	h.service.lastAppliedSettings = next
}

func tunOff(t *testing.T, h *staticRuntime) {
	t.Helper()
	marker := filepath.Join(h.root, "tun-off")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLASHPULSE_FAKE_TUN_OFF", marker)
}

func lastStartBinary(t *testing.T, h *staticRuntime) string {
	t.Helper()
	starts := readStartRecords(t, h.startLog)
	if len(starts) == 0 {
		t.Fatal("Mihomo never started")
	}
	return starts[len(starts)-1]["binary"]
}

func setTun(enabled bool) ipc.Command {
	return ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{TunEnabled: &enabled}}
}

func runTunLoop(t *testing.T, h *staticRuntime) *ipc.Client {
	t.Helper()
	// The harness context carries a 30s deadline that would shorten Prepare's.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.service.run(ctx, nil, nil) }()
	client := dialAppClient(ctx, filepath.Join(h.root, "socket", "ipc.sock"))
	if client == nil {
		cancel()
		t.Fatalf("service did not listen: %v", <-done)
	}
	t.Cleanup(func() {
		client.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("service did not stop")
		}
	})
	return client
}

func sendTunIntent(t *testing.T, h *staticRuntime, cmd ipc.Command) {
	t.Helper()
	if err := h.service.enqueueIntent(cmd); err != nil {
		t.Fatal(err)
	}
}

func hasJob(state core.Snapshot, kind ipc.CommandKind) bool {
	return slices.ContainsFunc(state.Jobs, func(job core.JobSnapshot) bool { return job.Kind == string(kind) })
}

func TestTunEnableStartsPrivilegedCopy(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeMissing)
	h.activate(t)
	client := runTunLoop(t, h)
	sendTunIntent(t, h, setTun(true))
	state := waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool { return state.Tun.Active && len(state.Jobs) == 0 })
	if got := fake.prepares.Load(); got != 1 {
		t.Fatalf("Prepare ran %d times, want once", got)
	}
	if binary := lastStartBinary(t, h); binary != h.service.tun.path {
		t.Fatalf("Mihomo started from %q, want privileged copy %q", binary, h.service.tun.path)
	}
	want := core.TunSnapshot{Enabled: true, Active: true, Stack: core.TunStackGVisor, DNSMode: core.TunDNSProfile, Privilege: core.TunPrivilegeReady, PrivilegedPath: h.service.tun.path, Device: "clashpulse0"}
	if state.Tun != want {
		t.Fatalf("tun snapshot = %+v, want %+v", state.Tun, want)
	}
	if !state.SystemProxy.Active {
		t.Fatal("System Proxy was not re-applied alongside TUN")
	}
}

func TestTunNotStartedRestoresPriorRuntime(t *testing.T) {
	h := newStaticRuntime(t)
	installFakeTun(t, h, tun.PrivilegeReady)
	grantTun(t, h)
	h.activate(t)
	before := bytes.Clone(h.service.generated)
	tunOff(t, h)
	if err := h.service.execute(h.ctx, setTun(true)); err != nil {
		t.Fatalf("saving TUN intent failed: %v", err)
	}
	if !bytes.Equal(h.service.generated, before) || h.service.controller == nil || !h.service.proxyActive {
		t.Fatalf("prior runtime not restored: controller=%t proxy=%t", h.service.controller != nil, h.service.proxyActive)
	}
	if onDisk, err := os.ReadFile(h.service.configPath()); err != nil || !bytes.Equal(onDisk, before) {
		t.Fatalf("prior generated configuration not restored: %v", err)
	}
	if binary := lastStartBinary(t, h); binary != filepath.Join(h.root, "mihomo") {
		t.Fatalf("restored runtime runs %q, want the selected binary", binary)
	}
	state := h.service.stateSnapshot()
	if !state.Tun.Enabled || state.Tun.Active || state.Tun.LastFailure != core.ActivationTunStart.Message() || state.Tun.Device != "" {
		t.Fatalf("tun snapshot = %+v", state.Tun)
	}
	if saved, err := config.Load(h.configDir); err != nil || !saved.Tun.Enabled {
		t.Fatalf("TUN intent was not kept: %+v, %v", saved.Tun, err)
	}
}

func TestTunPrepareRefusalLeavesRuntimeUntouched(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		privilege string
		failure   string
	}{
		{"declined", tun.ErrDeclined, core.TunPrivilegeMissing, "privilege declined"},
		{"no agent", tun.ErrNoAgent, core.TunPrivilegeNoAgent, core.ActivationTunPrivilege.Message()},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newStaticRuntime(t)
			fake := installFakeTun(t, h, tun.PrivilegeMissing)
			fake.outcome = func(context.Context) error { return test.err }
			h.activate(t)
			before, starts := bytes.Clone(h.service.generated), len(readStartRecords(t, h.startLog))
			client := runTunLoop(t, h)
			sendTunIntent(t, h, setTun(true))
			state := waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
				return state.Tun.LastFailure != "" && len(state.Jobs) == 0
			})
			if state.Tun.LastFailure != test.failure || state.Tun.Privilege != test.privilege || !state.Tun.Enabled || state.Tun.Active {
				t.Fatalf("tun snapshot = %+v", state.Tun)
			}
			if got := len(readStartRecords(t, h.startLog)); got != starts || !state.ServiceRunning || !state.SystemProxy.Active {
				t.Fatalf("refused privilege restarted or stopped the runtime: starts %d -> %d", starts, got)
			}
			if onDisk, err := os.ReadFile(filepath.Join(h.stateDir, "generated.yaml")); err != nil || !bytes.Equal(onDisk, before) {
				t.Fatalf("refused privilege changed the generated configuration: %v", err)
			}
		})
	}
}

func TestTunPrepareDoesNotBlockCommandsOrBatches(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeMissing)
	entered, release := make(chan time.Duration, 1), make(chan struct{})
	fake.outcome = func(ctx context.Context) error {
		deadline, _ := ctx.Deadline()
		entered <- time.Until(deadline)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return tun.ErrDeclined
	}
	defer close(release)
	h.activate(t)
	client := runTunLoop(t, h)
	sendTunIntent(t, h, setTun(true))
	select {
	case remaining := <-entered:
		if remaining < 110*time.Second || remaining > 2*time.Minute {
			t.Fatalf("Prepare deadline in %v, want 2m", remaining)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prepare did not start")
	}
	sendTunIntent(t, h, ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: opaqueID("select-main"), ChoiceID: opaqueID("node-b")})
	waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && hasJob(state, ipc.CommandAuthorizeTun)
	})
	h.service.batches <- monitor.Batch{Samples: []monitor.Sample{{Group: "select-main", Proxy: "node-a", Latency: 42 * time.Millisecond, Outcome: monitor.OutcomeSuccess, FinishedAt: time.Now()}}}
	waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
		return hasJob(state, ipc.CommandAuthorizeTun) && slices.ContainsFunc(state.Proxies, func(proxy core.ProxySnapshot) bool {
			return proxy.ID == opaqueID("node-a") && proxy.LatencyMillis == 42
		})
	})
	release <- struct{}{}
	waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
		return len(state.Jobs) == 0 && state.Tun.LastFailure == "privilege declined"
	})
}

func TestTunAutomaticStartWithStalePrivilegeSkipsTun(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeStale)
	grantTun(t, h)
	enableTunIntent(t, h)
	h.activate(t)
	if err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandRestart}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(h.service.generated), "clashpulse0") {
		t.Fatal("automatic start rendered TUN without a ready grant")
	}
	if binary := lastStartBinary(t, h); binary != filepath.Join(h.root, "mihomo") {
		t.Fatalf("automatic start ran %q, want the selected binary", binary)
	}
	if got := fake.prepares.Load(); got != 0 {
		t.Fatalf("automatic start prompted %d times", got)
	}
	state := h.service.stateSnapshot()
	if !state.Tun.Enabled || state.Tun.Active || state.Tun.Privilege != core.TunPrivilegeStale {
		t.Fatalf("tun snapshot = %+v, want requested, inactive, stale", state.Tun)
	}
}

func TestTunDisableRevokesEvenWhenRestartFails(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeReady)
	grantTun(t, h)
	enableTunIntent(t, h)
	h.activate(t)
	if !h.service.tunActive {
		t.Fatal("ready grant did not start TUN")
	}
	if err := os.WriteFile(h.failStart, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.service.execute(h.ctx, setTun(false)); err == nil {
		t.Fatal("failed restart was not reported")
	}
	if got := fake.revokes.Load(); got != 1 {
		t.Fatalf("revoked %d times after a failed restart, want once", got)
	}
	if err := h.service.execute(h.ctx, setTun(false)); err != nil {
		t.Fatal(err)
	}
	if binary := lastStartBinary(t, h); binary != filepath.Join(h.root, "mihomo") || h.service.tunActive {
		t.Fatalf("disabled TUN runs %q, active=%t", binary, h.service.tunActive)
	}
	if got := fake.revokes.Load(); got != 2 {
		t.Fatalf("revoked %d times, want twice", got)
	}
}

func TestTunAuthorizeRerunsPrepareForStaleGrant(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeStale)
	grantTun(t, h)
	enableTunIntent(t, h)
	h.activate(t)
	client := runTunLoop(t, h)
	sendTunIntent(t, h, ipc.Command{Kind: ipc.CommandAuthorizeTun})
	state := waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
		return state.Tun.Active && len(state.Jobs) == 0
	})
	if got := fake.prepares.Load(); got != 1 || !state.Tun.Enabled || len(state.Errors) != 0 {
		t.Fatalf("authorize prepared %d times: %+v, errors %+v", got, state.Tun, state.Errors)
	}
	if saved, err := config.Load(h.configDir); err != nil || !saved.Tun.Enabled {
		t.Fatalf("authorize changed intent: %+v, %v", saved.Tun, err)
	}
}

// A grant made by a manual sudo setcap must survive the Authorize button.
func TestTunAuthorizeKeepsReadyGrant(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeReady)
	grantTun(t, h)
	enableTunIntent(t, h)
	h.activate(t)
	before, err := os.Stat(h.service.tun.path)
	if err != nil {
		t.Fatal(err)
	}
	checked := fake.checks.Load()
	client := runTunLoop(t, h)
	sendTunIntent(t, h, ipc.Command{Kind: ipc.CommandAuthorizeTun})
	state := waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
		return fake.checks.Load() >= checked+2 && len(state.Jobs) == 0
	})
	if got := fake.prepares.Load(); got != 0 || fake.revokes.Load() != 0 {
		t.Fatalf("authorize replaced a ready grant: prepares %d revokes %d", got, fake.revokes.Load())
	}
	after, err := os.Stat(h.service.tun.path)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("privileged copy was rewritten: %v", err)
	}
	if !state.Tun.Active || len(state.Errors) != 0 {
		t.Fatalf("authorize changed TUN state: %+v, errors %+v", state.Tun, state.Errors)
	}
}

func TestTunAuthorizeWithoutIntentFails(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeMissing)
	h.activate(t)
	client := runTunLoop(t, h)
	sendTunIntent(t, h, ipc.Command{Kind: ipc.CommandAuthorizeTun})
	waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
		return len(state.Jobs) == 0 && slices.ContainsFunc(state.Errors, func(issue core.ErrorSnapshot) bool {
			return issue.Kind == string(ipc.CommandAuthorizeTun) && issue.Message == "TUN is not enabled"
		})
	})
	if got := fake.prepares.Load(); got != 0 {
		t.Fatalf("authorize without intent prompted %d times", got)
	}
}

func TestTunLeftoverDeviceReported(t *testing.T) {
	h := newStaticRuntime(t)
	installFakeTun(t, h, tun.PrivilegeMissing)
	h.service.tun.device = "lo"
	h.activate(t)
	if state := h.service.stateSnapshot(); !state.Tun.Leftover {
		t.Fatalf("existing device not reported: %+v", state.Tun)
	}
}

func TestTunAndSystemProxyResetOnStopAndFailedStart(t *testing.T) {
	h := newStaticRuntime(t)
	installFakeTun(t, h, tun.PrivilegeReady)
	grantTun(t, h)
	enableTunIntent(t, h)
	h.activate(t)
	if !h.service.tunActive {
		t.Fatal("TUN did not start with System Proxy")
	}
	if err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandStop}); err != nil {
		t.Fatal(err)
	}
	state := h.service.stateSnapshot()
	if len(readJSONFile(t, h.proxyState)) != 0 || state.SystemProxy.Active || state.Tun.Active || !state.Tun.Enabled {
		t.Fatalf("stop left proxy=%+v tun=%+v", state.SystemProxy, state.Tun)
	}
	proxyOn := true
	if err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{SystemProxyEnabled: &proxyOn}}); err != nil {
		t.Fatal(err)
	}
	tunOff(t, h)
	err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandStart})
	if public, ok := core.PublicActivation(err); !ok || public.Stage != core.ActivationTunStart {
		t.Fatalf("failed TUN start = %v", err)
	}
	if len(readJSONFile(t, h.proxyState)) != 0 || h.service.proxyActive || h.service.controller != nil || h.service.tunActive {
		t.Fatalf("failed start left proxy=%t controller=%t tun=%t", h.service.proxyActive, h.service.controller != nil, h.service.tunActive)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	t.Fatalf("%s did not appear", filepath.Base(path))
}

// A resource refresh validated against the selected binary before TUN came up
// must not restart Mihomo without TUN afterwards.
func TestTunActivationDiscardsResourceRefreshWithoutTun(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeMissing)
	release := make(chan struct{})
	fake.outcome = func(ctx context.Context) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.activate(t)
	block := filepath.Join(h.root, "block-validation")
	t.Setenv("CLASHPULSE_FAKE_VALIDATE_BLOCK", block)
	client := runTunLoop(t, h)
	sendTunIntent(t, h, setTun(true))
	waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool { return hasJob(state, ipc.CommandAuthorizeTun) })
	if err := os.WriteFile(block, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.setUpdatedSources(t)
	sendTunIntent(t, h, ipc.Command{Kind: ipc.CommandRefreshResource, ResourceID: "list-a"})
	waitForFile(t, block+".entered")
	close(release)
	waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool { return state.Tun.Active })
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	state := waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool { return len(state.Jobs) == 0 })
	if binary := lastStartBinary(t, h); !state.Tun.Active || binary != h.service.tun.path {
		t.Fatalf("stale resource refresh replaced TUN: active=%t binary=%q", state.Tun.Active, binary)
	}
}

func TestTunDisableCancelsPendingPrepare(t *testing.T) {
	h := newStaticRuntime(t)
	fake := installFakeTun(t, h, tun.PrivilegeMissing)
	canceled := make(chan error, 1)
	fake.outcome = func(ctx context.Context) error {
		<-ctx.Done()
		canceled <- ctx.Err()
		// Prepare may still finish writing its copy after the disable revoked.
		if err := copyTunBinary(filepath.Join(h.root, "mihomo"), h.service.tun.path); err != nil {
			return err
		}
		return tun.ErrDeclined
	}
	h.activate(t)
	client := runTunLoop(t, h)
	sendTunIntent(t, h, setTun(true))
	waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool { return hasJob(state, ipc.CommandAuthorizeTun) })
	sendTunIntent(t, h, setTun(false))
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Prepare ended by %v, want cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("disabling TUN did not cancel Prepare")
	}
	state := waitAppSnapshot(t, h.ctx, client, func(state core.Snapshot) bool {
		return !state.Tun.Enabled && len(state.Jobs) == 0
	})
	if state.Tun.Active || lastStartBinary(t, h) != filepath.Join(h.root, "mihomo") {
		t.Fatalf("canceled Prepare activated TUN: %+v", state.Tun)
	}
	if _, err := os.Stat(h.service.tun.path); !os.IsNotExist(err) {
		t.Fatalf("privileged copy left behind: %v", err)
	}
}

func TestTunPrivilegedPathAbsoluteForRelativeStateDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PrivilegedPath is reported on Linux only")
	}
	root := t.TempDir()
	t.Chdir(root)
	initial, err := config.Load(filepath.Join(root, "config"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRuntimeService(filepath.Join(root, "config"), "state", initial)
	if err != nil {
		t.Fatal(err)
	}
	if path := service.snapshot.Tun.PrivilegedPath; !filepath.IsAbs(path) {
		t.Fatalf("PrivilegedPath %q is relative", path)
	}
	server, err := ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(root, "socket", "ipc.sock"), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: service.snapshot})
	if err != nil {
		t.Fatalf("snapshot rejected at the IPC boundary: %v", err)
	}
	_ = server.Close()
}
