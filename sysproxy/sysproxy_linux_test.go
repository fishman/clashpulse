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

func TestApplyWritesChildSchemasAndRestoreResetsThem(t *testing.T) {
	listener := listenLoopback(t)
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	runner := newFakeRunner()
	manager := newManager(runner)

	if err := manager.Apply(context.Background(), listener.Addr().String()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	wantApply := []string{
		gnomeProxySchema + "|mode|'none'",
		gnomeProxyHTTP + "|host|'127.0.0.1'",
		gnomeProxyHTTP + "|port|" + port,
		gnomeProxyHTTPS + "|host|'127.0.0.1'",
		gnomeProxyHTTPS + "|port|" + port,
		gnomeProxySchema + "|use-same-proxy|false",
		gnomeProxySchema + "|mode|'manual'",
	}
	if got := runner.setArgs(); !reflect.DeepEqual(got, wantApply) {
		t.Fatalf("gsettings set argv = %v, want %v", got, wantApply)
	}

	if err := manager.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	wantReset := []string{
		gnomeProxySchema + "|mode",
		gnomeProxySchema + "|use-same-proxy",
		gnomeProxyHTTP + "|host",
		gnomeProxyHTTP + "|port",
		gnomeProxyHTTPS + "|host",
		gnomeProxyHTTPS + "|port",
	}
	if got := runner.resetArgs(); !reflect.DeepEqual(got, wantReset) {
		t.Fatalf("gsettings reset argv = %v, want %v", got, wantReset)
	}
	for _, written := range wantApply {
		parts := strings.Split(written, "|")
		if value, ok := runner.values[parts[0]+" "+parts[1]]; ok && value == parts[2] {
			t.Fatalf("Restore() left %s %s at the applied value %s", parts[0], parts[1], value)
		}
	}

	before := len(runner.calls)
	if err := manager.Restore(context.Background()); err != nil {
		t.Fatalf("second Restore() error = %v", err)
	}
	if len(runner.calls) != before {
		t.Fatalf("second Restore() issued commands: %v", runner.calls[before:])
	}
}

func TestRestoreWithoutApplyLeavesSettingsAlone(t *testing.T) {
	runner := newFakeRunner()
	manager := newManager(runner)
	if err := manager.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("Restore() without a prior Apply issued commands: %v", runner.calls)
	}
	if !reflect.DeepEqual(runner.values, initialProxySettings()) {
		t.Fatalf("settings = %v, want untouched %v", runner.values, initialProxySettings())
	}
}

func TestApplyFailureResetsPartialWrites(t *testing.T) {
	listener := listenLoopback(t)
	runner := newFakeRunner()
	runner.failAt = gnomeProxyHTTPS + " port"
	manager := newManager(runner)

	err := manager.Apply(context.Background(), listener.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "set port") {
		t.Fatalf("Apply() error = %v, want port failure", err)
	}
	if got := runner.resetArgs(); len(got) != len(resetKeys) {
		t.Fatalf("failed Apply() reset %v, want every owned key", got)
	}
	if manager.applied {
		t.Fatal("failed Apply() kept ownership of reset settings")
	}
}

func TestCanceledApplyResetsUsingUncanceledCleanupContext(t *testing.T) {
	listener := listenLoopback(t)
	runner := newFakeRunner()
	ctx, cancel := context.WithCancel(context.Background())
	runner.cancel = cancel
	runner.cancelAfterSetKey = "host"
	manager := newManager(runner)

	err := manager.Apply(ctx, listener.Addr().String())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply() error = %v, want context.Canceled", err)
	}
	if got := runner.resetArgs(); len(got) != len(resetKeys) {
		t.Fatalf("canceled Apply() reset %v, want every owned key", got)
	}
	if manager.applied {
		t.Fatal("canceled Apply() kept ownership of reset settings")
	}
}

func TestApplyRequiresReachableListener(t *testing.T) {
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
}

func TestApplyRejectsNonLoopbackOrInvalidAddressesBeforeCommands(t *testing.T) {
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

func TestApplyRequiresGNOMEProxySchemas(t *testing.T) {
	listener := listenLoopback(t)
	runner := newFakeRunner()
	runner.schemas = []string{"org.gnome.desktop.interface"}
	manager := newManager(runner)

	err := manager.Apply(context.Background(), listener.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "unsupported environment") || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("Apply() error = %v, want missing-schema error", err)
	}
	for _, call := range runner.calls {
		if len(call) > 1 && (call[1] == "set" || call[1] == "reset") {
			t.Fatalf("changed settings despite missing schema: %v", call)
		}
	}
}

func TestProxiesReadsChildSchemas(t *testing.T) {
	runner := newFakeRunner()
	runner.values[gnomeProxySchema+" mode"] = "'manual'"
	runner.values[gnomeProxySchema+" use-same-proxy"] = "false"
	runner.values[gnomeProxyHTTP+" host"] = "'127.0.0.1'"
	runner.values[gnomeProxyHTTP+" port"] = "7897"
	runner.values[gnomeProxyHTTPS+" host"] = "'127.0.0.1'"
	runner.values[gnomeProxyHTTPS+" port"] = "7898"

	settings, err := newManager(runner).Proxies(context.Background())
	if err != nil {
		t.Fatalf("Proxies() error = %v", err)
	}
	if settings.HTTP == nil || settings.HTTP.String() != "http://127.0.0.1:7897" {
		t.Fatalf("HTTP proxy = %v", settings.HTTP)
	}
	if settings.HTTPS == nil || settings.HTTPS.String() != "http://127.0.0.1:7898" {
		t.Fatalf("HTTPS proxy = %v", settings.HTTPS)
	}
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
		gnomeProxySchema + " mode":           "'auto'",
		gnomeProxySchema + " use-same-proxy": "true",
		gnomeProxyHTTP + " host":             "'old-http.example'",
		gnomeProxyHTTP + " port":             "8080",
		gnomeProxyHTTPS + " host":            "'old-https.example'",
		gnomeProxyHTTPS + " port":            "8443",
	}
}

type fakeRunner struct {
	calls             [][]string
	values            map[string]string
	schemas           []string
	failAt            string
	failed            bool
	cancel            context.CancelFunc
	cancelAfterSetKey string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		values:  initialProxySettings(),
		schemas: []string{gnomeProxySchema, gnomeProxyHTTP, gnomeProxyHTTPS},
	}
}

func (r *fakeRunner) setArgs() []string   { return r.mutationArgs("set") }
func (r *fakeRunner) resetArgs() []string { return r.mutationArgs("reset") }

func (r *fakeRunner) mutationArgs(operation string) []string {
	var args []string
	for _, call := range r.calls {
		if len(call) < 3 || call[0] != "gsettings" || call[1] != operation {
			continue
		}
		if operation == "set" {
			args = append(args, call[2]+"|"+call[3]+"|"+call[4])
			continue
		}
		args = append(args, call[2]+"|"+call[3])
	}
	return args
}

func (r *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if name != "gsettings" || len(args) == 0 {
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
	switch args[0] {
	case "list-schemas":
		return []byte(strings.Join(r.schemas, "\n") + "\n"), nil
	case "get":
		if len(args) != 3 {
			return nil, fmt.Errorf("unexpected get argv: %v", args)
		}
		value, ok := r.values[args[1]+" "+args[2]]
		if !ok {
			return nil, fmt.Errorf("unknown key %v", args)
		}
		return []byte(value + "\n"), nil
	case "set":
		if len(args) != 4 {
			return nil, fmt.Errorf("unexpected set argv: %v", args)
		}
		if err := r.inject("set", args[1]+" "+args[2], args[2]); err != nil {
			return nil, err
		}
		r.values[args[1]+" "+args[2]] = args[3]
		return nil, nil
	case "reset":
		if len(args) != 3 {
			return nil, fmt.Errorf("unexpected reset argv: %v", args)
		}
		if err := r.inject("reset", args[1]+" "+args[2], args[2]); err != nil {
			return nil, err
		}
		delete(r.values, args[1]+" "+args[2])
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected gsettings operation %q", args[0])
	}
}

func (r *fakeRunner) inject(operation, qualified, key string) error {
	if r.failAt == qualified && !r.failed {
		r.failed = true
		return errors.New("injected gsettings failure")
	}
	if operation == "set" && key == r.cancelAfterSetKey && r.cancel != nil {
		r.cancel()
		return context.Canceled
	}
	return nil
}
