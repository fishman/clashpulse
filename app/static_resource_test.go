package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/resources"
	"github.com/fishman/clashpulse/subscriptions"
)

type staticRuntime struct {
	root, configDir, stateDir string
	ctx                       context.Context
	cancel                    context.CancelFunc
	service                   *runtimeService
	subscription              config.Subscription
	source                    *httptest.Server
	resourceFiles             map[string]string
	initialBodies             map[string][]byte
	updatedBodies             map[string][]byte
	startLog, mutationLog     string
	failValidation, failStart string
	proxyState, fakeLog       string
}

func newStaticRuntime(t *testing.T) *staticRuntime {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("fake Mihomo and GNOME proxy commands require Linux")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable for fake controller")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	startLog := filepath.Join(root, "starts.jsonl")
	mutationLog := filepath.Join(root, "resource-mutated-while-running")
	fakeLog := filepath.Join(root, "mihomo.log")
	failValidation := filepath.Join(root, "reject-stable-validation")
	failStart := filepath.Join(root, "fail-next-start")
	proxyState := filepath.Join(root, "gsettings.json")
	initialProxy := map[string]string{
		"mode": "'manual'", "http-host": "'192.0.2.1'", "http-port": "8080",
		"https-host": "'192.0.2.2'", "https-port": "8443", "use-same-proxy": "false",
	}
	writeJSONFile(t, proxyState, initialProxy)
	t.Setenv("XDG_CURRENT_DESKTOP", "GNOME")
	t.Setenv("CLASHPULSE_FAKE_STARTS", startLog)
	t.Setenv("CLASHPULSE_FAKE_MUTATIONS", mutationLog)
	t.Setenv("CLASHPULSE_FAKE_PROXY_STATE", proxyState)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	gsettings := fmt.Sprintf("#!%s\nimport json, os, sys\npath=os.environ['CLASHPULSE_FAKE_PROXY_STATE']\nargs=sys.argv[1:]\nschema='org.gnome.system.proxy'\nkeys=['mode','http-host','http-port','https-host','https-port','use-same-proxy']\nif args[0]=='list-schemas': print(schema)\nelif args[0]=='list-keys': print(' '.join(keys))\nelif args[0]=='get': print(json.load(open(path))[args[2]])\nelif args[0]=='set':\n marker=os.getenv('CLASHPULSE_FAKE_GSETTINGS_FAIL_ONCE')\n if args[2]=='https-port' and marker and os.path.exists(marker):\n  os.remove(marker); sys.exit(1)\n d=json.load(open(path)); d[args[2]]=args[3]; json.dump(d,open(path,'w'))\n", python)
	if err := os.WriteFile(filepath.Join(binDir, "gsettings"), []byte(gsettings), 0o700); err != nil {
		t.Fatal(err)
	}
	binary := staticFakeMihomo(t, root, python, failValidation, failStart, fakeLog, startLog, mutationLog)
	resourceFiles := map[string]string{
		"list-a": filepath.Join(root, "list-a.txt"),
		"list-b": filepath.Join(root, "list-b.txt"),
	}
	initialBodies := map[string][]byte{"list-a": []byte("example.com\n"), "list-b": []byte("other.example\n")}
	updatedBodies := map[string][]byte{"list-a": []byte("updated.example\n"), "list-b": []byte("changed.example\n")}
	for id, path := range resourceFiles {
		if err := os.WriteFile(path, initialBodies[id], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "state")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = false\n[system_proxy]\nenabled = true\n", binary))); err != nil {
		t.Fatal(err)
	}
	resourceTOML := fmt.Sprintf("[[resource]]\nid = \"list-a\"\nkind = \"rule-set\"\nformat = \"text\"\nrule_type = \"domain\"\nurl = %q\nenabled = true\n\n[[resource]]\nid = \"list-b\"\nkind = \"rule-set\"\nformat = \"text\"\nrule_type = \"domain\"\nurl = %q\nenabled = true\n", resourceFiles["list-a"], resourceFiles["list-b"])
	if err := config.Write(filepath.Join(configDir, "resources.toml"), []byte(resourceTOML)); err != nil {
		t.Fatal(err)
	}
	filtersTOML := "[[filter]]\nid = \"filter-a\"\nresource = \"list-a\"\nformat = \"text\"\ntarget = \"select-main\"\nenabled = true\n\n[[filter]]\nid = \"filter-b\"\nresource = \"list-b\"\nformat = \"text\"\ntarget = \"select-main\"\nenabled = true\n"
	if err := config.Write(filepath.Join(configDir, "filters.toml"), []byte(filtersTOML)); err != nil {
		t.Fatal(err)
	}
	profile := []byte("proxies:\n  - name: node-a\n    type: direct\n  - name: node-b\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a, node-b]\n")
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(profile) }))
	t.Cleanup(source.Close)
	subscription := config.Subscription{ID: "daily", URL: source.URL, Enabled: true, AllowHTTP: true, Timeout: 2 * time.Second}
	if err := config.Write(filepath.Join(configDir, "subscriptions.toml"), []byte(fmt.Sprintf("[[subscription]]\nid = \"daily\"\nurl = %q\nenabled = true\nallow_http = true\ntimeout = \"2s\"\n", source.URL))); err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRuntimeService(configDir, stateDir, initial)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	server, err := ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(root, "socket", "ipc.sock"), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: service.snapshot})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	service.server, service.processCtx = server, ctx
	service.subScheduler = subscriptions.NewScheduler(service.subs, 1)
	h := &staticRuntime{
		root: root, configDir: configDir, stateDir: stateDir, ctx: ctx, cancel: cancel, service: service,
		subscription: subscription, source: source, resourceFiles: resourceFiles,
		initialBodies: initialBodies, updatedBodies: updatedBodies, startLog: startLog, mutationLog: mutationLog,
		failValidation: failValidation, failStart: failStart, proxyState: proxyState, fakeLog: fakeLog,
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 6*time.Second)
		defer done()
		if service.controller != nil || service.proxyActive {
			_ = service.stop(cleanup)
		}
		_ = server.Close()
		cancel()
	})
	return h
}

func (h *staticRuntime) activate(t *testing.T) {
	t.Helper()
	if configured := h.service.store.Snapshot().Resources; len(configured) != 2 || !configured[0].Enabled || !configured[1].Enabled {
		t.Fatalf("lifecycle resources = %+v", configured)
	}
	if _, err := h.service.subs.RefreshWith(h.ctx, h.subscription); err != nil {
		t.Fatal(err)
	}
	if err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: h.subscription.ID}); err != nil {
		detail, _ := os.ReadFile(h.fakeLog)
		t.Fatalf("activate subscription: %v: %s", err, detail)
	}
	if h.service.controller == nil || !h.service.proxyActive {
		t.Fatal("activation did not leave a ready runtime and active system proxy")
	}
	if _, err := os.Stat(filepath.Join(h.service.registry.Home(), ".resources.txn")); !os.IsNotExist(err) {
		t.Fatalf("activation left an unfinished resource transaction: %v", err)
	}
}

func (h *staticRuntime) setUpdatedSources(t *testing.T) {
	t.Helper()
	for id, path := range h.resourceFiles {
		if err := os.WriteFile(path, h.updatedBodies[id], 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStaticResourcesPromoteAfterQuiesce(t *testing.T) {
	h := newStaticRuntime(t)
	h.activate(t)
	if err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: opaqueID("select-main"), ChoiceID: opaqueID("node-b")}); err != nil {
		t.Fatal(err)
	}
	pathsBefore, err := h.service.registry.Paths(h.service.store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	generatedBefore, err := os.ReadFile(filepath.Join(h.stateDir, "generated.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	proxyBefore := readJSONFile(t, h.proxyState)
	h.setUpdatedSources(t)
	prepared, err := h.service.prepareResourceRefresh(h.ctx, []string{"list-a", "list-b"})
	if err != nil {
		t.Fatal(err)
	}
	for id, path := range pathsBefore {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(h.initialBodies[id]) {
			t.Fatalf("stable %s changed before apply: %q, %v", id, data, err)
		}
	}
	if err := h.service.applyPreparedResourceRefresh(h.ctx, prepared); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.mutationLog); !os.IsNotExist(err) {
		t.Fatalf("resource bytes changed while the prior Mihomo process was running: %v", err)
	}
	pathsAfter, err := h.service.registry.Paths(h.service.store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for id, path := range pathsBefore {
		if pathsAfter[id] != path || filepath.Dir(path) != h.service.registry.Home() {
			t.Fatalf("%s path changed: %q -> %q", id, path, pathsAfter[id])
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(h.updatedBodies[id]) {
			t.Fatalf("promoted %s = %q, err = %v", id, data, err)
		}
		if !strings.Contains(string(h.service.generated), path) {
			t.Fatalf("generated config does not use stable %s path %q: %s", id, path, h.service.generated)
		}
	}
	if string(generatedBefore) != string(h.service.generated) {
		t.Fatal("resource-only update changed stable generated paths")
	}
	starts := readStartRecords(t, h.startLog)
	if len(starts) != 2 {
		t.Fatalf("resource update did not restart Mihomo once: %d starts", len(starts))
	}
	if h.service.controller == nil || !h.service.proxyActive || h.service.groups[opaqueID("select-main")].Selected != "node-b" {
		t.Fatalf("runtime state after promotion = controller:%v proxy:%v groups:%+v", h.service.controller != nil, h.service.proxyActive, h.service.groups)
	}
	if !equalJSONMap(proxyBefore, readJSONFile(t, h.proxyState)) {
		t.Fatalf("system proxy changed across successful replacement: before=%v after=%v", proxyBefore, readJSONFile(t, h.proxyState))
	}
}

func TestStaticResourceFailureRestoresRuntime(t *testing.T) {
	for _, test := range []struct {
		name   string
		marker func(*staticRuntime) string
		stage  core.ActivationStage
	}{
		{name: "final path validation", marker: func(h *staticRuntime) string { return h.failValidation }, stage: core.ActivationConfigValidation},
		{name: "controller readiness", marker: func(h *staticRuntime) string { return h.failStart }, stage: core.ActivationControllerReadiness},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newStaticRuntime(t)
			h.activate(t)
			if err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: opaqueID("select-main"), ChoiceID: opaqueID("node-b")}); err != nil {
				t.Fatal(err)
			}
			paths, err := h.service.registry.Paths(h.service.store.Snapshot())
			if err != nil {
				t.Fatal(err)
			}
			beforeGenerated, err := os.ReadFile(filepath.Join(h.stateDir, "generated.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			beforeManifest, err := os.ReadFile(filepath.Join(h.service.registry.Home(), ".resources.json"))
			if err != nil {
				t.Fatal(err)
			}
			_, beforeProfile, err := h.service.subs.ActiveProfile()
			if err != nil {
				t.Fatal(err)
			}
			proxyBefore := readJSONFile(t, h.proxyState)
			h.setUpdatedSources(t)
			prepared, err := h.service.prepareResourceRefresh(h.ctx, []string{"list-a", "list-b"})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(test.marker(h), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			err = h.service.applyPreparedResourceRefresh(h.ctx, prepared)
			public, ok := core.PublicActivation(err)
			if err == nil || !ok || public.Stage != test.stage {
				t.Fatalf("resource failure stage = %v, want %s", err, test.stage)
			}
			for id, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != string(h.initialBodies[id]) {
					t.Fatalf("failed replacement changed %s: %q, %v", id, data, err)
				}
			}
			afterManifest, err := os.ReadFile(filepath.Join(h.service.registry.Home(), ".resources.json"))
			if err != nil || string(afterManifest) != string(beforeManifest) {
				t.Fatalf("failed replacement changed manifest: %v", err)
			}
			afterGenerated, err := os.ReadFile(filepath.Join(h.stateDir, "generated.yaml"))
			if err != nil || string(afterGenerated) != string(beforeGenerated) || string(h.service.generated) != string(beforeGenerated) {
				t.Fatalf("failed replacement changed generated config: %v", err)
			}
			_, afterProfile, err := h.service.subs.ActiveProfile()
			if err != nil || string(afterProfile) != string(beforeProfile) {
				t.Fatalf("failed replacement changed active profile: %v", err)
			}
			if h.service.controller == nil || !h.service.proxyActive || h.service.groups[opaqueID("select-main")].Selected != "node-b" {
				t.Fatalf("failed replacement did not restore runtime: controller:%v proxy:%v groups:%+v", h.service.controller != nil, h.service.proxyActive, h.service.groups)
			}
			if !equalJSONMap(proxyBefore, readJSONFile(t, h.proxyState)) {
				t.Fatalf("failed replacement changed System Proxy: before=%v after=%v", proxyBefore, readJSONFile(t, h.proxyState))
			}
			if _, err := os.Stat(h.mutationLog); !os.IsNotExist(err) {
				t.Fatalf("resource bytes changed while prior Mihomo was running: %v", err)
			}
		})
	}
}

func TestStaticSystemProxyApplyFailureReportsSafeStage(t *testing.T) {
	h := newStaticRuntime(t)
	before := readJSONFile(t, h.proxyState)
	marker := filepath.Join(h.root, "fail-proxy-once")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLASHPULSE_FAKE_GSETTINGS_FAIL_ONCE", marker)
	if _, err := h.service.subs.RefreshWith(h.ctx, h.subscription); err != nil {
		t.Fatal(err)
	}
	err := h.service.subs.Activate(h.ctx, h.subscription.ID)
	public, ok := core.PublicActivation(err)
	if !ok || public.Stage != core.ActivationSystemProxy || h.service.controller != nil || h.service.proxyActive {
		t.Fatalf("failed proxy activation state = %v, controller=%t proxy=%t", err, h.service.controller != nil, h.service.proxyActive)
	}

	if !equalJSONMap(before, readJSONFile(t, h.proxyState)) {
		t.Fatal("failed System Proxy Apply did not restore prior settings")
	}
}
func TestLocalProfileResourceFailureReportsStableID(t *testing.T) {
	h := newStaticRuntime(t)
	profile := filepath.Join(h.root, "source with password=private.yaml")
	if err := os.WriteFile(profile, []byte("proxies:\n  - name: node-a\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.resourceFiles["list-a"]); err != nil {
		t.Fatal(err)
	}
	err := RunFileAt(h.ctx, h.configDir, h.stateDir, filepath.Join(h.root, "socket", "local.sock"), profile, nil)
	public, ok := core.PublicActivation(err)
	if !ok || public.Stage != core.ActivationResources || public.ResourceID != "list-a" || strings.Contains(err.Error(), "private") {
		t.Fatalf("managed resource failure lost safe identity: %v", err)
	}
}
func TestLocalActiveSourceDoesNotMarkSavedSubscriptionRunning(t *testing.T) {
	h := newStaticRuntime(t)
	h.activate(t)
	h.service.localProfile = []byte("proxies:\n  - name: node-a\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a]\n")
	state := h.service.stateSnapshot()
	if state.ActiveSource != "local" || len(state.Subscriptions) != 1 || state.Subscriptions[0].Active {
		t.Fatalf("local source was shown as active subscription: %+v", state.Subscriptions)
	}
}

func TestLocalProfileSystemProxyRestoredOnCancel(t *testing.T) {
	h := newStaticRuntime(t)
	profile := filepath.Join(h.root, "local.yaml")
	if err := os.WriteFile(profile, []byte("proxies:\n  - name: node-a\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readJSONFile(t, h.proxyState)
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	ready, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		done <- RunFileAt(ctx, h.configDir, h.stateDir, filepath.Join(h.root, "socket", "local.sock"), profile, func() error { ready <- struct{}{}; return nil })
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("local activation failed: %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("local controller not ready")
	}
	if equalJSONMap(before, readJSONFile(t, h.proxyState)) {
		t.Fatal("requested System Proxy never applied")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("local shutdown: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("local shutdown blocked")
	}
	if !equalJSONMap(before, readJSONFile(t, h.proxyState)) {
		t.Fatal("local shutdown did not restore System Proxy")
	}
}

func TestLocalReadinessAndProxyRollbackFailureReportsRollback(t *testing.T) {
	h := newStaticRuntime(t)
	profile := filepath.Join(h.root, "local.yaml")
	if err := os.WriteFile(profile, []byte("proxies:\n  - name: node-a\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(h.root, "fail-restore-once")
	t.Setenv("CLASHPULSE_FAKE_GSETTINGS_FAIL_ONCE", marker)
	err := RunFileAt(h.ctx, h.configDir, h.stateDir, filepath.Join(h.root, "socket", "local.sock"), profile, func() error {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			return err
		}
		return errors.New("password=private")
	})
	public, ok := core.PublicActivation(err)
	if !ok || public.Stage != core.ActivationRollback || strings.Contains(err.Error(), "password=private") {
		t.Fatalf("failed cleanup was masked: %v (cause: %v)", err, errors.Unwrap(err))
	}
}

func localStaticRuntime(t *testing.T) *staticRuntime {
	t.Helper()
	h := newStaticRuntime(t)
	h.service.localProfile = []byte("proxies:\n  - name: local-only\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [local-only]\n")
	file, err := os.CreateTemp(h.stateDir, "generated-local-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	h.service.generatedPath = file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.service.start(h.ctx); err != nil {
		t.Fatalf("start local profile: %v", err)
	}
	return h
}

func TestLocalProfileResourceRefresh(t *testing.T) {
	h := localStaticRuntime(t)
	before, err := os.ReadFile(h.service.configPath())
	if err != nil {
		t.Fatal(err)
	}
	h.setUpdatedSources(t)
	prepared, err := h.service.prepareResourceRefresh(h.ctx, []string{"list-a", "list-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.service.applyPreparedResourceRefresh(h.ctx, prepared); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(h.service.configPath())
	if err != nil || !bytes.Equal(before, after) || h.service.controller == nil || h.service.localProfile == nil {
		t.Fatalf("local resource refresh lost runtime: %v", err)
	}
	for id, path := range h.resourceFiles {
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(body, h.updatedBodies[id]) {
			t.Fatalf("updated %s = %q, %v", id, body, err)
		}
	}
	if due, _ := h.service.resourceDeadlines(time.Now()); due.IsZero() {
		t.Fatal("local source disabled resource schedule")
	}
}

func TestLocalProfileRollbackAfterSubscriptionFailure(t *testing.T) {
	h := localStaticRuntime(t)
	localPath, localBytes := h.service.generatedPath, bytes.Clone(h.service.localProfile)
	generated, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.subs.RefreshWith(h.ctx, h.subscription); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.failStart, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err = h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: h.subscription.ID})
	if err == nil || h.service.controller == nil || h.service.generatedPath != localPath || !bytes.Equal(localBytes, h.service.localProfile) {
		t.Fatalf("failed subscription activation lost local source: %v", err)
	}
	if source := h.service.stateSnapshot().ActiveSource; source != "local" {
		t.Fatalf("failed activation source = %q", source)
	}
	if body, err := os.ReadFile(localPath); err != nil || !bytes.Equal(body, generated) {
		t.Fatalf("failed activation changed local config: %q, %v", body, err)
	}
	if err := h.service.execute(h.ctx, ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: h.subscription.ID}); err != nil {
		t.Fatalf("valid subscription activation: %v", err)
	}
	if h.service.localProfile != nil || h.service.generatedPath != "" {
		t.Fatal("subscription activation retained local source")
	}
	if source := h.service.stateSnapshot().ActiveSource; source != "subscription" {
		t.Fatalf("committed activation source = %q", source)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("local config survived subscription cutover: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "generated.yaml")); err != nil {
		t.Fatalf("durable config missing: %v", err)
	}
}

func TestLocalResourceActivationRollbackRestoresDurableConfig(t *testing.T) {
	h := localStaticRuntime(t)
	h.setUpdatedSources(t)
	intent := h.service.store.Snapshot()
	plan, err := h.service.registry.StageDue(h.ctx, intent, download.Direct, []string{"list-a", "list-b"})
	if err != nil {
		t.Fatal(err)
	}
	profile := []byte("proxies:\n  - name: node-a\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a]\n")
	if err := plan.Validate(func(home string, paths map[string]string) error {
		_, err := h.service.validatedCandidate(h.ctx, profile, intent, home, paths, h.service.cap)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.service.applyResourcePlan(h.ctx, plan, profile, h.service.cap, true); err != nil {
		t.Fatalf("resource activation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "generated.yaml")); err != nil {
		t.Fatalf("candidate durable config missing: %v", err)
	}
	backup := h.service.activationBackup
	h.service.activationBackup = nil
	rollbackErr := h.service.restoreResourceRuntime(h.ctx, backup, errors.New("proxy selection failed"), true)
	if rollbackErr == nil {
		t.Fatal("rollback lost original failure")
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "generated.yaml")); !os.IsNotExist(err) {
		t.Fatalf("rollback retained candidate durable config: %v", err)
	}
	if h.service.controller == nil || h.service.localProfile == nil || h.service.generatedPath == "" {
		t.Fatalf("rollback did not restore local runtime: %v", rollbackErr)
	}
}

func TestResourceRollbackFailureReportsRollbackStage(t *testing.T) {
	h := newStaticRuntime(t)
	h.activate(t)
	if err := h.service.stop(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.setUpdatedSources(t)
	plan, err := h.service.registry.StageDue(h.ctx, h.service.store.Snapshot(), download.Direct, []string{"list-a", "list-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.service.registry.Home(), ".resources.txn")); err != nil {
		t.Fatal(err)
	}
	cause := core.WrapActivation(core.ActivationConfigValidation, errors.New("candidate rejected"))
	err = h.service.restoreResourceRuntime(h.ctx, &runtimeBackup{resourcePlan: plan}, cause, true)
	public, ok := core.PublicActivation(err)
	if !ok || public.Stage != core.ActivationRollback {
		t.Fatalf("failed rollback concealed its stage: %v", err)
	}
}

func TestStaticJournalRecoversBeforeStart(t *testing.T) {
	h := newStaticRuntime(t)
	h.activate(t)
	if err := h.service.stop(h.ctx); err != nil {
		t.Fatal(err)
	}
	beforeManifest, err := os.ReadFile(filepath.Join(h.service.registry.Home(), ".resources.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.setUpdatedSources(t)
	plan, err := h.service.registry.StageDue(h.ctx, h.service.store.Snapshot(), download.Direct, []string{"list-a", "list-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	stablePaths, err := plan.Commit()
	if err != nil {
		t.Fatal(err)
	}
	for id, path := range stablePaths {
		if err := replaceStaticFile(path, h.initialBodies[id]); err != nil {
			t.Fatal(err)
		}
		break
	}
	if _, err := h.service.registry.Paths(h.service.store.Snapshot()); err == nil {
		t.Fatal("resolved a partially promoted resource set")
	}
	if err := os.WriteFile(h.startLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load(h.configDir)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := newRuntimeService(h.configDir, h.stateDir, initial)
	if err != nil {
		t.Fatal(err)
	}
	restarted.processCtx = h.ctx
	restarted.subScheduler = subscriptions.NewScheduler(restarted.subs, 1)
	restarted.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(h.root, "restart.sock"), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: restarted.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if restarted.controller != nil || restarted.proxyActive {
			cleanup, done := context.WithTimeout(context.Background(), 6*time.Second)
			defer done()
			_ = restarted.stop(cleanup)
		}
		_ = restarted.server.Close()
	})
	if err := restarted.start(h.ctx); err != nil {
		t.Fatal(err)
	}
	recoveredManifest, err := os.ReadFile(filepath.Join(h.service.registry.Home(), ".resources.json"))
	if err != nil || string(recoveredManifest) != string(beforeManifest) {
		t.Fatalf("startup did not restore prior manifest before Mihomo start: %v", err)
	}
	starts := readStartRecords(t, h.startLog)
	fileA := resources.ManagedFilename(config.Resource{ID: "list-a", Kind: config.ResourceRuleSet, Format: config.FormatText, RuleType: config.RuleDomain})
	fileB := resources.ManagedFilename(config.Resource{ID: "list-b", Kind: config.ResourceRuleSet, Format: config.FormatText, RuleType: config.RuleDomain})
	if len(starts) != 1 || starts[0][fileA] != hash(h.initialBodies["list-a"]) || starts[0][fileB] != hash(h.initialBodies["list-b"]) {
		t.Fatalf("Mihomo startup observed unrecovered resource bytes: %v", starts)
	}
}

func TestStaticActivationJournalRestoresPriorSelection(t *testing.T) {
	h := newStaticRuntime(t)
	h.activate(t)
	if err := h.service.stop(h.ctx); err != nil {
		t.Fatal(err)
	}
	next := h.subscription
	next.ID = "next"
	if err := config.Write(filepath.Join(h.configDir, "subscriptions.toml"), []byte(fmt.Sprintf("[[subscription]]\nid = \"daily\"\nurl = %q\nenabled = true\nallow_http = true\ntimeout = \"2s\"\n\n[[subscription]]\nid = \"next\"\nurl = %q\nenabled = true\nallow_http = true\ntimeout = \"2s\"\n", h.source.URL, h.source.URL))); err != nil {
		t.Fatal(err)
	}
	intent, err := config.Load(h.configDir)
	if err != nil {
		t.Fatal(err)
	}
	h.service.store.Replace(intent)
	if err := h.service.syncSubscriptions(intent); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.subs.RefreshWith(h.ctx, next); err != nil {
		t.Fatal(err)
	}
	subscriptionDir := filepath.Join(h.stateDir, "subscriptions")
	activePath := filepath.Join(subscriptionDir, "active.json")
	previous, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	var prior struct {
		ID            string `json:"id"`
		ProfileHash   string `json:"profile_hash"`
		CandidateHash string `json:"candidate_hash"`
	}
	if err := json.Unmarshal(previous, &prior); err != nil {
		t.Fatal(err)
	}
	h.setUpdatedSources(t)
	plan, err := h.service.registry.StageDue(h.ctx, h.service.store.Snapshot(), download.Direct, []string{"list-a", "list-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Commit(); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(h.service.registry.Home(), ".resources.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resourceVersion struct {
		CommitID string `json:"commit_id"`
	}
	if err := json.Unmarshal(manifest, &resourceVersion); err != nil {
		t.Fatal(err)
	}
	var pending map[string]string
	if err := json.Unmarshal(previous, &pending); err != nil {
		t.Fatal(err)
	}
	pending["resource_commit_id"] = resourceVersion.CommitID
	pendingBytes, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(subscriptionDir, ".activation-pending.json"), pendingBytes); err != nil {
		t.Fatal(err)
	}
	nextRecord, err := os.ReadFile(filepath.Join(subscriptionDir, next.ID, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Hash          string `json:"hash"`
		CandidateHash string `json:"candidate_hash"`
	}
	if err := json.Unmarshal(nextRecord, &record); err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(map[string]string{"id": next.ID, "profile_hash": record.Hash, "candidate_hash": record.CandidateHash})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Write(activePath, marker); err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load(h.configDir)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := newRuntimeService(h.configDir, h.stateDir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := restarted.subs.ActiveID(); err != nil || id != prior.ID {
		t.Fatalf("startup kept unaccepted subscription %q: %v", id, err)
	}
	paths, err := restarted.registry.Paths(initial)
	if err != nil {
		t.Fatal(err)
	}
	for id, path := range paths {
		if body, err := os.ReadFile(path); err != nil || string(body) != string(h.initialBodies[id]) {
			t.Fatalf("startup selected prior subscription with changed %s bytes: %q, %v", id, body, err)
		}
	}
	if _, err := os.Stat(filepath.Join(subscriptionDir, ".activation-pending.json")); !os.IsNotExist(err) {
		t.Fatalf("reconciled activation marker remains: %v", err)
	}
}

func staticFakeMihomo(t *testing.T, root, python, failValidation, failStart, fakeLog, startLog, mutationLog string) string {
	t.Helper()
	server := filepath.Join(root, "static-fake.py")
	source := `import hashlib, http.server, json, os, re, socketserver, subprocess, sys, threading, time
if sys.argv[1] == '--watch':
    pid, paths, initial, marker = int(sys.argv[2]), json.loads(sys.argv[3]), json.loads(sys.argv[4]), sys.argv[5]
    while True:
        try: os.kill(pid, 0)
        except OSError: break
        current = {p: hashlib.sha256(open(p,'rb').read()).hexdigest() for p in paths if os.path.isfile(p)}
        if current != initial:
            with open(marker, 'a', encoding='utf-8') as out: out.write(json.dumps({'pid':pid,'before':initial,'after':current})+'\n')
            break
        time.sleep(.001)
    raise SystemExit(0)
text = open(sys.argv[1], encoding='utf-8').read()
home = sys.argv[2]
address = re.search(r'^external-controller:\s*(\S+)', text, re.M).group(1)
secret = re.search(r'^secret:\s*(\S+)', text, re.M).group(1)
port = int(re.search(r'^mixed-port:\s*(\d+)', text, re.M).group(1))
paths = [v.strip().strip('"\'') for v in re.findall(r'^\s*path:\s*(.+?)\s*$', text, re.M)]
paths = [p for p in paths if os.path.isabs(p)]
initial = {p: hashlib.sha256(open(p,'rb').read()).hexdigest() for p in paths if os.path.isfile(p)}
with open(sys.argv[3], 'a', encoding='utf-8') as out: out.write(json.dumps({os.path.basename(p):v for p,v in initial.items()})+'\n')
subprocess.Popen([sys.executable, __file__, '--watch', str(os.getpid()), json.dumps(paths), json.dumps(initial), sys.argv[4]], start_new_session=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
class ProxyHandler(socketserver.BaseRequestHandler):
    def handle(self): self.request.close()
socketserver.ThreadingTCPServer.allow_reuse_address = True
proxy = socketserver.ThreadingTCPServer(('127.0.0.1', port), ProxyHandler)
threading.Thread(target=proxy.serve_forever, daemon=True).start()
selected = ['node-a']
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.headers.get('Authorization') != 'Bearer ' + secret:
            self.send_error(401); return
        if self.path.startswith('/proxies/node-a/delay') or self.path.startswith('/proxies/node-b/delay'):
            self.send_response(200); self.end_headers(); self.wfile.write(b'{"delay":180}'); return
        if self.path.startswith('/proxies'):
            payload = {'proxies': {'select-main': {'name':'select-main','type':'Selector','all':['node-a','node-b'],'now':selected[0]}, 'node-a':{'name':'node-a','type':'Direct'}, 'node-b':{'name':'node-b','type':'Direct'}}}
            self.send_response(200); self.end_headers(); self.wfile.write(json.dumps(payload).encode()); return
        self.send_error(404)
    def do_PUT(self):
        if self.headers.get('Authorization') != 'Bearer ' + secret:
            self.send_error(401); return
        if self.path == '/proxies/select-main':
            selected[0] = json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))))['name']
            self.send_response(204); self.end_headers(); return
        self.send_error(404)
    def log_message(self, *args): pass
http.server.ThreadingHTTPServer(('127.0.0.1', int(address.rsplit(':',1)[1])), Handler).serve_forever()
`
	if err := os.WriteFile(server, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "mihomo")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n -v) echo 'Mihomo Meta v1.19.31 linux amd64'; exit 0;;\n -t) if [ -f %q ] && [ \"$5\" = %q ]; then rm -f %q; exit 1; fi; exit 0;;\n -f) if [ -f %q ]; then rm -f %q; exit 0; fi; exec %q %q \"$2\" \"$4\" %q %q 2>>%q;;\nesac\nexit 1\n", failValidation, filepath.Join(root, "state", "resources"), failValidation, failStart, failStart, python, server, startLog, mutationLog, fakeLog)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func replaceStaticFile(path string, body []byte) error {
	temp := path + ".recovery"
	if err := os.WriteFile(temp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSONFile(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]string
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func equalJSONMap(a, b map[string]string) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func readStartRecords(t *testing.T, path string) []map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record map[string]string
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
