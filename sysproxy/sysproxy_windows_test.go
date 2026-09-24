//go:build windows

package sysproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWindowsApplyAndRestorePreserveUserProxySettings(t *testing.T) {
	listener := windowsLoopbackListener(t)
	initial := map[string]windowsRegistryValue{
		"ProxyEnable":   testWindowsDWORD(1),
		"ProxyServer":   testWindowsString("proxy.example:8080"),
		"ProxyOverride": testWindowsString("<local>"),
		"AutoDetect":    testWindowsDWORD(0),
	}
	api := newFakeWindowsAPI(initial)
	manager := &Manager{}

	if err := applyWindows(context.Background(), manager, api, listener.Addr().String()); err != nil {
		t.Fatalf("applyWindows() error = %v", err)
	}
	wantProxy := fmt.Sprintf("http=127.0.0.1:%d;https=127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port, listener.Addr().(*net.TCPAddr).Port)
	if got := testWindowsStringContents(api.values["ProxyServer"]); got != wantProxy {
		t.Fatalf("ProxyServer = %q, want %q", got, wantProxy)
	}
	if got := api.values["ProxyOverride"]; !reflect.DeepEqual(got, initial["ProxyOverride"]) {
		t.Fatalf("ProxyOverride changed to %#v", got)
	}
	if api.notifications != 1 {
		t.Fatalf("settings notifications = %d, want 1", api.notifications)
	}

	if err := restoreWindows(context.Background(), manager, api); err != nil {
		t.Fatalf("restoreWindows() error = %v", err)
	}
	if !reflect.DeepEqual(api.values, initial) {
		t.Fatalf("settings after restore = %#v, want %#v", api.values, initial)
	}
	if api.notifications != 2 {
		t.Fatalf("settings notifications = %d, want 2", api.notifications)
	}
}

func TestWindowsApplyFailureRollsBackCapturedSettings(t *testing.T) {
	listener := windowsLoopbackListener(t)
	initial := windowsInitialSettings()
	api := newFakeWindowsAPI(initial)
	api.failWrite = "ProxyEnable"
	manager := &Manager{}

	err := applyWindows(context.Background(), manager, api, listener.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "write ProxyEnable") {
		t.Fatalf("applyWindows() error = %v, want ProxyEnable write failure", err)
	}
	if !reflect.DeepEqual(api.values, initial) {
		t.Fatalf("settings after failed apply = %#v, want %#v", api.values, initial)
	}
	if manager.previous != nil {
		t.Fatal("failed apply retained a snapshot after rollback")
	}
}

func TestWindowsRestoreFailureRollsBackToAppliedProxy(t *testing.T) {
	listener := windowsLoopbackListener(t)
	initial := windowsInitialSettings()
	api := newFakeWindowsAPI(initial)
	manager := &Manager{}
	if err := applyWindows(context.Background(), manager, api, listener.Addr().String()); err != nil {
		t.Fatalf("initial applyWindows() error = %v", err)
	}
	applying := newFakeWindowsAPI(api.values).values
	api.failWrite = "ProxyEnable"

	if err := restoreWindows(context.Background(), manager, api); err == nil || !strings.Contains(err.Error(), "write ProxyEnable") {
		t.Fatalf("restoreWindows() error = %v, want ProxyEnable write failure", err)
	}
	if !reflect.DeepEqual(api.values, applying) {
		t.Fatalf("settings after failed restore = %#v, want applied settings %#v", api.values, applying)
	}
	if manager.previous == nil {
		t.Fatal("failed restore discarded the original snapshot")
	}
	if err := restoreWindows(context.Background(), manager, api); err != nil {
		t.Fatalf("retry restoreWindows() error = %v", err)
	}
	if !reflect.DeepEqual(api.values, initial) {
		t.Fatalf("settings after retry = %#v, want original %#v", api.values, initial)
	}
}

func TestWindowsApplyCancellationRollsBackWithLiveCleanupContext(t *testing.T) {
	listener := windowsLoopbackListener(t)
	initial := windowsInitialSettings()
	api := newFakeWindowsAPI(initial)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.cancelAfterWrite = "ProxyServer"
	api.cancel = cancel
	manager := &Manager{}

	err := applyWindows(ctx, manager, api, listener.Addr().String())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("applyWindows() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(api.values, initial) {
		t.Fatalf("settings after canceled apply = %#v, want %#v", api.values, initial)
	}
	if manager.previous != nil {
		t.Fatal("canceled apply retained a snapshot after rollback")
	}
}

func TestWindowsApplyRejectsAutomaticProxyModesBeforeMutation(t *testing.T) {
	listener := windowsLoopbackListener(t)
	for _, tc := range []struct {
		name  string
		value windowsRegistryValue
		key   string
	}{
		{name: "PAC", key: "AutoConfigURL", value: testWindowsString("https://proxy.example/pac")},
		{name: "autodetect", key: "AutoDetect", value: testWindowsDWORD(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := windowsInitialSettings()
			initial[tc.key] = tc.value
			api := newFakeWindowsAPI(initial)
			manager := &Manager{}
			if err := applyWindows(context.Background(), manager, api, listener.Addr().String()); err == nil {
				t.Fatal("applyWindows() unexpectedly accepted automatic proxy mode")
			}
			if len(api.writes) != 0 || api.notifications != 0 {
				t.Fatalf("automatic mode caused changes: writes=%v notifications=%d", api.writes, api.notifications)
			}
			if manager.previous != nil {
				t.Fatal("rejected apply captured settings")
			}
		})
	}
}

func TestWindowsApplyRequiresAReadyLoopbackListener(t *testing.T) {
	listener := windowsLoopbackListener(t)
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	api := newFakeWindowsAPI(windowsInitialSettings())
	if err := applyWindows(context.Background(), &Manager{}, api, address); err == nil || !strings.Contains(err.Error(), "listener is not ready") {
		t.Fatalf("applyWindows() error = %v, want listener-not-ready error", err)
	}
	if len(api.writes) != 0 || api.notifications != 0 {
		t.Fatalf("unready listener caused changes: writes=%v notifications=%d", api.writes, api.notifications)
	}
}

func TestWindowsProxiesReadsBothEnabledProtocolEndpoints(t *testing.T) {
	api := newFakeWindowsAPI(map[string]windowsRegistryValue{
		"ProxyEnable": testWindowsDWORD(1),
		"ProxyServer": testWindowsString("http=proxy.example:8080;https=secure.example:8443"),
	})
	got, err := proxiesWindows(context.Background(), api)
	if err != nil {
		t.Fatalf("proxiesWindows() error = %v", err)
	}
	if got.HTTP == nil || got.HTTPS == nil {
		t.Fatalf("proxy settings = (%v, %v), want both protocol endpoints", got.HTTP, got.HTTPS)
	}
	if got.HTTP.String() != "http://proxy.example:8080" || got.HTTPS.String() != "http://secure.example:8443" {
		t.Fatalf("proxy settings = (%v, %v), want HTTP and HTTPS proxy endpoints", got.HTTP, got.HTTPS)
	}
}

func TestWindowsProxiesRejectsBypassRulesAndReportsDisabledSettings(t *testing.T) {
	api := newFakeWindowsAPI(map[string]windowsRegistryValue{
		"ProxyEnable":   testWindowsDWORD(1),
		"ProxyServer":   testWindowsString("proxy.example:8080"),
		"ProxyOverride": testWindowsString("<local>;*.internal"),
	})
	if _, err := proxiesWindows(context.Background(), api); err == nil || !strings.Contains(err.Error(), "ProxyOverride") {
		t.Fatalf("proxiesWindows() error = %v, want explicit bypass-rule error", err)
	}

	disabled := newFakeWindowsAPI(map[string]windowsRegistryValue{"ProxyEnable": testWindowsDWORD(0)})
	got, err := proxiesWindows(context.Background(), disabled)
	if err != nil {
		t.Fatalf("disabled proxiesWindows() error = %v", err)
	}
	if got.HTTP != nil || got.HTTPS != nil {
		t.Fatalf("disabled proxy settings = %#v, want both endpoints unavailable", got)
	}
}

func TestWindowsProxyParserKeepsOnlyConfiguredProtocols(t *testing.T) {
	got, err := parseWindowsProxyServer("http=proxy.example:8080")
	if err != nil {
		t.Fatalf("parseWindowsProxyServer() error = %v", err)
	}
	if got.HTTP == nil || got.HTTPS != nil {
		t.Fatalf("parsed proxy settings = %#v, want only HTTP endpoint", got)
	}
}

func windowsLoopbackListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func windowsInitialSettings() map[string]windowsRegistryValue {
	return map[string]windowsRegistryValue{
		"ProxyEnable":   testWindowsDWORD(0),
		"ProxyServer":   testWindowsString("original.proxy:3128"),
		"ProxyOverride": testWindowsString("<local>"),
	}
}

func testWindowsDWORD(value uint32) windowsRegistryValue {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, value)
	return windowsRegistryValue{exists: true, typ: windowsRegistryDWORD, data: data}
}

func testWindowsString(value string) windowsRegistryValue {
	units := utf16.Encode([]rune(value))
	units = append(units, 0)
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return windowsRegistryValue{exists: true, typ: windowsRegistryString, data: data}
}

func testWindowsStringContents(value windowsRegistryValue) string {
	units := make([]uint16, len(value.data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(value.data[i*2 : i*2+2])
	}
	if len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units))
}

type fakeWindowsAPI struct {
	values           map[string]windowsRegistryValue
	writes           []string
	notifications    int
	failWrite        string
	failedWrite      bool
	cancelAfterWrite string
	cancel           context.CancelFunc
}

func newFakeWindowsAPI(values map[string]windowsRegistryValue) *fakeWindowsAPI {
	api := &fakeWindowsAPI{values: make(map[string]windowsRegistryValue, len(values))}
	for name, value := range values {
		api.values[name] = cloneWindowsValue(value)
	}
	return api
}

func (f *fakeWindowsAPI) read(ctx context.Context, name string) (windowsRegistryValue, error) {
	if err := ctx.Err(); err != nil {
		return windowsRegistryValue{}, err
	}
	return cloneWindowsValue(f.values[name]), nil
}

func (f *fakeWindowsAPI) write(ctx context.Context, name string, value windowsRegistryValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == f.failWrite && !f.failedWrite {
		f.failedWrite = true
		return errors.New("injected write failure")
	}
	f.writes = append(f.writes, name)
	if value.exists {
		f.values[name] = cloneWindowsValue(value)
	} else {
		delete(f.values, name)
	}
	if name == f.cancelAfterWrite && f.cancel != nil {
		f.cancel()
	}
	return nil
}

func (f *fakeWindowsAPI) notify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.notifications++
	return nil
}

func cloneWindowsValue(value windowsRegistryValue) windowsRegistryValue {
	value.data = append([]byte(nil), value.data...)
	return value
}
