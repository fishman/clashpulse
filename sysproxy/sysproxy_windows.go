//go:build windows

package sysproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	windowsInternetSettingsKey    = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	windowsRegistryString         = uint32(registry.SZ)
	windowsRegistryDWORD          = uint32(registry.DWORD)
	windowsProxyEnable            = "ProxyEnable"
	windowsProxyServer            = "ProxyServer"
	windowsProxyOverride          = "ProxyOverride"
	windowsAutoConfigURL          = "AutoConfigURL"
	windowsAutoDetect             = "AutoDetect"
	internetOptionRefresh         = 37
	internetOptionSettingsChanged = 39
)

type windowsRegistryValue struct {
	exists bool
	typ    uint32
	data   []byte
}

type windowsProxyAPI interface {
	read(context.Context, string) (windowsRegistryValue, error)
	write(context.Context, string, windowsRegistryValue) error
	notify(context.Context) error
}

type windowsRegistryAPI struct{}

var internetSetOptionW = windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
var regSetValueExW = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW")

func (windowsRegistryAPI) read(ctx context.Context, name string) (windowsRegistryValue, error) {
	if err := ctx.Err(); err != nil {
		return windowsRegistryValue{}, err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsInternetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return windowsRegistryValue{}, err
	}
	defer key.Close()
	if err := ctx.Err(); err != nil {
		return windowsRegistryValue{}, err
	}

	size, typ, err := key.GetValue(name, nil)
	if errors.Is(err, registry.ErrNotExist) {
		return windowsRegistryValue{}, ctx.Err()
	}
	if err != nil {
		return windowsRegistryValue{}, err
	}
	if err := ctx.Err(); err != nil {
		return windowsRegistryValue{}, err
	}
	data := make([]byte, size)
	size, typ, err = key.GetValue(name, data)
	if errors.Is(err, registry.ErrNotExist) {
		return windowsRegistryValue{}, ctx.Err()
	}
	if err != nil {
		return windowsRegistryValue{}, err
	}
	if err := ctx.Err(); err != nil {
		return windowsRegistryValue{}, err
	}
	return windowsRegistryValue{exists: true, typ: typ, data: data[:size]}, nil
}

func (windowsRegistryAPI) write(ctx context.Context, name string, value windowsRegistryValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsInternetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.exists {
		pointer, pointerErr := syscall.UTF16PtrFromString(name)
		if pointerErr != nil {
			return pointerErr
		}
		var data *byte
		if len(value.data) != 0 {
			data = &value.data[0]
		}
		result, _, _ := regSetValueExW.Call(uintptr(key), uintptr(unsafe.Pointer(pointer)), 0, uintptr(value.typ), uintptr(unsafe.Pointer(data)), uintptr(len(value.data)))
		if result != 0 {
			err = syscall.Errno(result)
		}
	} else {
		err = key.DeleteValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			err = nil
		}
	}
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (windowsRegistryAPI) notify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, option := range []uintptr{internetOptionSettingsChanged, internetOptionRefresh} {
		result, _, callErr := internetSetOptionW.Call(0, option, 0, 0)
		if result == 0 {
			if callErr == nil || callErr == windows.ERROR_SUCCESS {
				callErr = errors.New("InternetSetOptionW failed without a Windows error")
			}
			return fmt.Errorf("InternetSetOptionW(%d): %w", option, callErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// Apply points WinINet's HTTP and HTTPS proxy settings at a ready loopback listener.
func (m *Manager) Apply(ctx context.Context, listenerAddress string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return applyWindows(ctx, m, windowsRegistryAPI{}, listenerAddress)
}

func applyWindows(ctx context.Context, manager *Manager, api windowsProxyAPI, listenerAddress string) error {
	if ctx == nil {
		return errors.New("sysproxy: apply requires a context")
	}
	if err := ctx.Err(); err != nil {
		return manager.failWindowsApply(ctx, api, fmt.Errorf("sysproxy: apply: %w", err))
	}
	host, port, err := splitListenerAddress(listenerAddress)
	if err != nil {
		return manager.failWindowsApply(ctx, api, fmt.Errorf("sysproxy: apply: %w", err))
	}
	if err := verifyListenerReady(ctx, host, port); err != nil {
		return manager.failWindowsApply(ctx, api, fmt.Errorf("sysproxy: listener is not ready: %w", err))
	}
	if err := verifyWindowsManualProxyMode(ctx, api); err != nil {
		return manager.failWindowsApply(ctx, api, err)
	}
	if manager.previous == nil {
		previous, err := captureWindowsSettings(ctx, api)
		if err != nil {
			return fmt.Errorf("sysproxy: capture Windows proxy settings: %w", err)
		}
		manager.previous = previous
	}

	proxyAddress := net.JoinHostPort(host, strconv.Itoa(port))
	proxyServer := windowsStringValue("http=" + proxyAddress + ";https=" + proxyAddress)
	if err := setWindowsSettings(ctx, api, proxyServer, windowsDWORDValue(1)); err != nil {
		return manager.failWindowsApply(ctx, api, err)
	}
	return nil
}

// Restore returns the WinINet HTTP and HTTPS settings to their captured values.
func (m *Manager) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return restoreWindows(ctx, m, windowsRegistryAPI{})
}

func restoreWindows(ctx context.Context, manager *Manager, api windowsProxyAPI) error {
	if manager.previous == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("sysproxy: restore requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := captureWindowsSettings(ctx, api)
	if err != nil {
		return fmt.Errorf("sysproxy: capture current Windows proxy settings: %w", err)
	}
	if err := writeWindowsSnapshot(ctx, api, manager.previous); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if rollbackErr := writeWindowsSnapshot(cleanupCtx, api, current); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("sysproxy: rollback restore: %w", rollbackErr))
		}
		return err
	}
	manager.previous = nil
	return nil
}

func (m *Manager) Proxies(ctx context.Context) (ProxySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return proxiesWindows(ctx, windowsRegistryAPI{})
}

func proxiesWindows(ctx context.Context, api windowsProxyAPI) (ProxySettings, error) {
	if ctx == nil {
		return ProxySettings{}, errors.New("sysproxy: query proxies requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ProxySettings{}, err
	}
	if err := verifyWindowsManualProxyMode(ctx, api); err != nil {
		return ProxySettings{}, err
	}
	enabledValue, err := api.read(ctx, windowsProxyEnable)
	if err != nil {
		return ProxySettings{}, fmt.Errorf("sysproxy: read ProxyEnable: %w", err)
	}
	enabled, err := windowsDWORD(enabledValue, windowsProxyEnable)
	if err != nil {
		return ProxySettings{}, err
	}
	if enabled == 0 {
		return ProxySettings{}, nil
	}
	serverValue, err := api.read(ctx, windowsProxyServer)
	if err != nil {
		return ProxySettings{}, fmt.Errorf("sysproxy: read ProxyServer: %w", err)
	}
	server, err := windowsString(serverValue, windowsProxyServer)
	if err != nil {
		return ProxySettings{}, err
	}
	settings, err := parseWindowsProxyServer(server)
	if err != nil {
		return ProxySettings{}, err
	}
	if settings.HTTP == nil && settings.HTTPS == nil {
		return settings, nil
	}
	overrideValue, err := api.read(ctx, windowsProxyOverride)
	if err != nil {
		return ProxySettings{}, fmt.Errorf("sysproxy: read ProxyOverride: %w", err)
	}
	override, err := windowsString(overrideValue, windowsProxyOverride)
	if err != nil {
		return ProxySettings{}, err
	}
	if strings.TrimSpace(override) != "" {
		return ProxySettings{}, errors.New("sysproxy: unsupported Windows proxy configuration (ProxyOverride bypass rules cannot be preserved)")
	}
	return settings, nil
}

func verifyWindowsManualProxyMode(ctx context.Context, api windowsProxyAPI) error {
	pacValue, err := api.read(ctx, windowsAutoConfigURL)
	if err != nil {
		return fmt.Errorf("sysproxy: inspect Windows PAC settings: %w", err)
	}
	pac, err := windowsString(pacValue, windowsAutoConfigURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(pac) != "" {
		return errors.New("sysproxy: unsupported Windows proxy configuration (PAC cannot be safely managed)")
	}
	autoDetectValue, err := api.read(ctx, windowsAutoDetect)
	if err != nil {
		return fmt.Errorf("sysproxy: inspect Windows proxy autodiscovery: %w", err)
	}
	autoDetect, err := windowsDWORD(autoDetectValue, windowsAutoDetect)
	if err != nil {
		return err
	}
	if autoDetect != 0 {
		return errors.New("sysproxy: unsupported Windows proxy configuration (autodiscovery cannot be safely managed)")
	}
	return nil
}

func captureWindowsSettings(ctx context.Context, api windowsProxyAPI) (map[string]string, error) {
	previous := make(map[string]string, 2)
	for _, name := range []string{windowsProxyServer, windowsProxyEnable} {
		value, err := api.read(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if name == windowsProxyEnable {
			if _, err := windowsDWORD(value, name); err != nil {
				return nil, err
			}
		}
		previous["windows:"+name] = encodeWindowsValue(value)
	}
	return previous, nil
}

func encodeWindowsValue(value windowsRegistryValue) string {
	if !value.exists {
		return "absent"
	}
	return strconv.FormatUint(uint64(value.typ), 16) + ":" + string(value.data)
}

func decodeWindowsValue(encoded string) (windowsRegistryValue, error) {
	if encoded == "absent" {
		return windowsRegistryValue{}, nil
	}
	typText, data, ok := strings.Cut(encoded, ":")
	if !ok {
		return windowsRegistryValue{}, errors.New("invalid Windows proxy snapshot")
	}
	typ, err := strconv.ParseUint(typText, 16, 32)
	if err != nil {
		return windowsRegistryValue{}, fmt.Errorf("invalid Windows proxy snapshot type: %w", err)
	}
	return windowsRegistryValue{exists: true, typ: uint32(typ), data: []byte(data)}, nil
}

func writeWindowsSnapshot(ctx context.Context, api windowsProxyAPI, snapshot map[string]string) error {
	server, ok := snapshot["windows:"+windowsProxyServer]
	if !ok {
		return errors.New("sysproxy: missing ProxyServer snapshot")
	}
	enabled, ok := snapshot["windows:"+windowsProxyEnable]
	if !ok {
		return errors.New("sysproxy: missing ProxyEnable snapshot")
	}
	serverValue, err := decodeWindowsValue(server)
	if err != nil {
		return err
	}
	enabledValue, err := decodeWindowsValue(enabled)
	if err != nil {
		return err
	}
	return setWindowsSettings(ctx, api, serverValue, enabledValue)
}

func (m *Manager) failWindowsApply(ctx context.Context, api windowsProxyAPI, cause error) error {
	if m.previous == nil {
		return cause
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := writeWindowsSnapshot(cleanupCtx, api, m.previous); err != nil {
		return errors.Join(cause, fmt.Errorf("sysproxy: rollback: %w", err))
	}
	m.previous = nil
	return cause
}

func setWindowsSettings(ctx context.Context, api windowsProxyAPI, server, enabled windowsRegistryValue) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sysproxy: apply Windows proxy settings: %w", err)
	}
	if err := api.write(ctx, windowsProxyServer, server); err != nil {
		return fmt.Errorf("sysproxy: write ProxyServer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sysproxy: apply Windows proxy settings: %w", err)
	}
	if err := api.write(ctx, windowsProxyEnable, enabled); err != nil {
		return fmt.Errorf("sysproxy: write ProxyEnable: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sysproxy: apply Windows proxy settings: %w", err)
	}
	if err := api.notify(ctx); err != nil {
		return fmt.Errorf("sysproxy: notify Windows proxy settings: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sysproxy: apply Windows proxy settings: %w", err)
	}
	return nil
}

func windowsDWORD(value windowsRegistryValue, name string) (uint32, error) {
	if !value.exists {
		return 0, nil
	}
	if value.typ != windowsRegistryDWORD || len(value.data) != 4 {
		return 0, fmt.Errorf("sysproxy: unsupported Windows registry value %s (expected DWORD)", name)
	}
	return binary.LittleEndian.Uint32(value.data), nil
}

func windowsString(value windowsRegistryValue, name string) (string, error) {
	if !value.exists {
		return "", nil
	}
	if value.typ != windowsRegistryString || len(value.data)%2 != 0 {
		return "", fmt.Errorf("sysproxy: unsupported Windows registry value %s (expected REG_SZ)", name)
	}
	if len(value.data) == 0 {
		return "", nil
	}
	units := make([]uint16, len(value.data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(value.data[i*2 : i*2+2])
	}
	if units[len(units)-1] != 0 {
		return "", fmt.Errorf("sysproxy: malformed Windows registry string %s", name)
	}
	for _, unit := range units[:len(units)-1] {
		if unit == 0 {
			return "", fmt.Errorf("sysproxy: malformed Windows registry string %s", name)
		}
	}
	return string(utf16.Decode(units[:len(units)-1])), nil
}

func windowsStringValue(value string) windowsRegistryValue {
	units := utf16.Encode([]rune(value))
	units = append(units, 0)
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return windowsRegistryValue{exists: true, typ: windowsRegistryString, data: data}
}

func windowsDWORDValue(value uint32) windowsRegistryValue {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, value)
	return windowsRegistryValue{exists: true, typ: windowsRegistryDWORD, data: data}
}

func parseWindowsProxyServer(value string) (ProxySettings, error) {
	var settings ProxySettings
	value = strings.TrimSpace(value)
	if value == "" {
		return settings, nil
	}
	if !strings.Contains(value, "=") {
		proxy, err := parseWindowsProxyEndpoint(value)
		if err != nil {
			return settings, err
		}
		return ProxySettings{HTTP: proxy, HTTPS: proxy}, nil
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(value, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		scheme, endpoint, ok := strings.Cut(item, "=")
		if !ok {
			return ProxySettings{}, errors.New("sysproxy: malformed Windows ProxyServer value")
		}
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		if scheme != "http" && scheme != "https" {
			continue
		}
		if seen[scheme] {
			return ProxySettings{}, fmt.Errorf("sysproxy: duplicate %s Windows proxy endpoint", scheme)
		}
		seen[scheme] = true
		proxy, err := parseWindowsProxyEndpoint(strings.TrimSpace(endpoint))
		if err != nil {
			return ProxySettings{}, fmt.Errorf("sysproxy: invalid Windows %s proxy: %w", scheme, err)
		}
		if scheme == "http" {
			settings.HTTP = proxy
		} else {
			settings.HTTPS = proxy
		}
	}
	return settings, nil
}

func parseWindowsProxyEndpoint(endpoint string) (*url.URL, error) {
	if endpoint == "" {
		return nil, errors.New("empty proxy endpoint")
	}
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "http" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("unsupported proxy endpoint URL")
		}
		endpoint = parsed.Host
	}
	host, rawPort, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, fmt.Errorf("proxy endpoint must include host and port: %w", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("proxy endpoint port must be between 1 and 65535")
	}
	return makeProxyURL(host, port)
}
