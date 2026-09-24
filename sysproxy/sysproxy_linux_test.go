//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestApplyConfiguresHTTPAndHTTPSAndRestoreRestoresPriorSettings(t *testing.T) {
	setGNOMESession(t)
	listener := listenLoopback(t)
	runner := newFakeRunner()
	manager := newManager(runner)

	if err := manager.Apply(context.Background(), listener.Addr().String()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := runner.values["mode"], "'manual'"; got != want {
		t.Errorf("proxy mode = %s, want %s", got, want)
	}
	for _, key := range []string{"http-host", "https-host"} {
		if got, want := runner.values[key], "'127.0.0.1'"; got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	for _, key := range []string{"http-port", "https-port"} {
		if got, want := runner.values[key], fmt.Sprint(listener.Addr().(*net.TCPAddr).Port); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	if got, want := runner.values["use-same-proxy"], "false"; got != want {
		t.Errorf("use-same-proxy = %s, want %s", got, want)
	}

	var applyKeys []string
	for _, call := range runner.calls {
		if len(call) == 5 && call[0] == "gsettings" && call[1] == "set" {
			applyKeys = append(applyKeys, call[3]+"="+call[4])
		}
	}
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	wantApplyKeys := []string{
		"mode='none'", "http-host='127.0.0.1'", "http-port=" + port,
		"https-host='127.0.0.1'", "https-port=" + port,
		"use-same-proxy=false", "mode='manual'",
	}
	if !reflect.DeepEqual(applyKeys, wantApplyKeys) {
		t.Fatalf("gsettings set argv = %v, want %v", applyKeys, wantApplyKeys)
	}

	if err := manager.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if !reflect.DeepEqual(runner.values, initialProxySettings()) {
		t.Errorf("settings after Restore() = %v, want %v", runner.values, initialProxySettings())
	}
	if err := manager.Restore(context.Background()); err != nil {
		t.Errorf("second Restore() error = %v", err)
	}
}

func TestApplyFailureRollsBackEveryProxySetting(t *testing.T) {
	setGNOMESession(t)
	listener := listenLoopback(t)
	runner := newFakeRunner()
	runner.failKey = "https-port"
	manager := newManager(runner)

	err := manager.Apply(context.Background(), listener.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "set https-port") {
		t.Fatalf("Apply() error = %v, want https-port failure", err)
	}
	if !reflect.DeepEqual(runner.values, initialProxySettings()) {
		t.Fatalf("settings after failed Apply() = %v, want restored %v", runner.values, initialProxySettings())
	}
	if manager.previous != nil {
		t.Fatal("failed Apply() retained a snapshot after successful rollback")
	}
}

func TestFailedReapplyRestoresOriginalSettings(t *testing.T) {
	setGNOMESession(t)
	listener := listenLoopback(t)
	runner := newFakeRunner()
	manager := newManager(runner)
	if err := manager.Apply(context.Background(), listener.Addr().String()); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	if err := manager.Apply(context.Background(), "127.0.0.1:0"); err == nil {
		t.Fatal("invalid reapply unexpectedly succeeded")
	}
	if !reflect.DeepEqual(runner.values, initialProxySettings()) {
		t.Fatalf("settings after failed reapply = %v, want restored %v", runner.values, initialProxySettings())
	}
	if manager.previous != nil {
		t.Fatal("failed reapply retained the original snapshot after rollback")
	}
}

func TestCanceledApplyRollsBackUsingUncanceledCleanupContext(t *testing.T) {
	setGNOMESession(t)
	listener := listenLoopback(t)
	runner := newFakeRunner()
	ctx, cancel := context.WithCancel(context.Background())
	runner.cancel = cancel
	runner.cancelAfterSetKey = "https-host"
	manager := newManager(runner)

	err := manager.Apply(ctx, listener.Addr().String())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(runner.values, initialProxySettings()) {
		t.Fatalf("settings after canceled Apply() = %v, want restored %v", runner.values, initialProxySettings())
	}
	if manager.previous != nil {
		t.Fatal("canceled Apply() retained a snapshot after successful rollback")
	}
}

func TestApplyRequiresReachableListenerAndGNOMESession(t *testing.T) {
	t.Run("unreachable listener", func(t *testing.T) {
		setGNOMESession(t)
		listener := listenLoopback(t)
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		runner := newFakeRunner()
		manager := newManager(runner)
		if err := manager.Apply(context.Background(), address); err == nil || !strings.Contains(err.Error(), "listener is not ready") {
			t.Fatalf("Apply() error = %v, want not-ready error", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("gsettings calls before listener readiness = %v", runner.calls)
		}
	})

	t.Run("unsupported session", func(t *testing.T) {
		clearGNOMESession(t)
		listener := listenLoopback(t)
		runner := newFakeRunner()
		manager := newManager(runner)
		if err := manager.Apply(context.Background(), listener.Addr().String()); err == nil || !strings.Contains(err.Error(), "unsupported session") {
			t.Fatalf("Apply() error = %v, want unsupported-session error", err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("gsettings calls in unsupported session = %v", runner.calls)
		}
	})
}

func TestApplyRejectsNonLoopbackOrInvalidAddressesBeforeCommands(t *testing.T) {
	setGNOMESession(t)
	for _, address := range []string{"0.0.0.0:1080", "192.0.2.1:1080", "[::]:1080", "127.0.0.1:0", "127.0.0.1:65536", "proxy.example:1080", "localhost:1080", "127.0.0.1:not-a-port"} {
		t.Run(address, func(t *testing.T) {
			runner := newFakeRunner()
			manager := newManager(runner)
			if err := manager.Apply(context.Background(), address); err == nil {
				t.Fatal("Apply() error = nil, want invalid listener address")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("gsettings calls for invalid address = %v", runner.calls)
			}
		})
	}
}

func TestApplyRequiresGNOMEProxySchema(t *testing.T) {
	setGNOMESession(t)
	listener := listenLoopback(t)
	runner := newFakeRunner()
	runner.hasSchema = false
	manager := newManager(runner)

	if err := manager.Apply(context.Background(), listener.Addr().String()); err == nil || !strings.Contains(err.Error(), "schema is unavailable") {
		t.Fatalf("Apply() error = %v, want missing-schema error", err)
	}
	for _, call := range runner.calls {
		if len(call) > 1 && call[1] == "set" {
			t.Fatalf("changed settings despite missing schema: %v", call)
		}
	}
}

func setGNOMESession(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
	t.Setenv("XDG_SESSION_DESKTOP", "ubuntu")
	t.Setenv("DESKTOP_SESSION", "ubuntu")
	t.Setenv("GNOME_DESKTOP_SESSION_ID", "")
}

func clearGNOMESession(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("XDG_SESSION_DESKTOP", "")
	t.Setenv("DESKTOP_SESSION", "")
	t.Setenv("GNOME_DESKTOP_SESSION_ID", "")
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func initialProxySettings() map[string]string {
	return map[string]string{
		"mode":           "'auto'",
		"http-host":      "'old-http.example'",
		"http-port":      "8080",
		"https-host":     "'old-https.example'",
		"https-port":     "8443",
		"use-same-proxy": "true",
	}
}

type fakeRunner struct {
	calls             [][]string
	values            map[string]string
	hasSchema         bool
	failKey           string
	failed            bool
	cancel            context.CancelFunc
	cancelAfterSetKey string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{values: initialProxySettings(), hasSchema: true}
}

func (r *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if name != "gsettings" || len(args) == 0 {
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
	if args[0] != "list-schemas" && (len(args) < 2 || args[1] != gnomeProxySchema) {
		return nil, fmt.Errorf("unexpected schema argv: %v", args)
	}
	switch args[0] {
	case "list-schemas":
		if r.hasSchema {
			return []byte(gnomeProxySchema + "\n"), nil
		}
		return []byte("org.gnome.desktop.interface\n"), nil
	case "list-keys":
		return []byte(strings.Join(proxyKeys, "\n") + "\n"), nil
	case "get":
		if len(args) != 3 {
			return nil, fmt.Errorf("unexpected get argv: %v", args)
		}
		value, ok := r.values[args[2]]
		if !ok {
			return nil, fmt.Errorf("unknown key %q", args[2])
		}
		return []byte(value + "\n"), nil
	case "set":
		if len(args) != 4 {
			return nil, fmt.Errorf("unexpected set argv: %v", args)
		}
		key, value := args[2], args[3]
		if r.failKey == key && !r.failed {
			r.failed = true
			return nil, errors.New("injected gsettings failure")
		}
		r.values[key] = value
		if key == r.cancelAfterSetKey {
			r.cancel()
			return nil, ctx.Err()
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected gsettings operation %q", args[0])
	}
}
