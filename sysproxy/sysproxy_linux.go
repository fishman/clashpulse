//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const gnomeProxySchema = "org.gnome.system.proxy"

var proxyKeys = []string{"mode", "http-host", "http-port", "https-host", "https-port", "use-same-proxy"}

var applySettings = []setting{
	{key: "mode", value: "'none'"},
	{key: "http-host"},
	{key: "http-port"},
	{key: "https-host"},
	{key: "https-port"},
	{key: "use-same-proxy", value: "false"},
	{key: "mode", value: "'manual'"},
}

type setting struct {
	key   string
	value string
}

// Apply points GNOME's HTTP and HTTPS proxy settings at a loopback listener.
// The listener must already be accepting connections before this is called.
func (m *Manager) Apply(ctx context.Context, listenerAddress string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return m.failApply(ctx, fmt.Errorf("sysproxy: apply: %w", err))
	}
	host, port, err := splitListenerAddress(listenerAddress)
	if err != nil {
		return m.failApply(ctx, fmt.Errorf("sysproxy: apply: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return m.failApply(ctx, fmt.Errorf("sysproxy: apply: %w", err))
	}
	if err := verifyListenerReady(ctx, host, port); err != nil {
		return m.failApply(ctx, fmt.Errorf("sysproxy: listener is not ready: %w", err))
	}
	if !gnomeSession() {
		return m.failApply(ctx, errors.New("sysproxy: unsupported session (GNOME desktop session required)"))
	}
	if m.runner == nil {
		return m.failApply(ctx, errors.New("sysproxy: no command runner configured"))
	}
	if err := verifyGNOMESchema(ctx, m.runner); err != nil {
		return m.failApply(ctx, err)
	}

	if m.previous == nil {
		previous, err := captureSettings(ctx, m.runner)
		if err != nil {
			return err
		}
		m.previous = previous
	}

	hostValue := variantString(host)
	portValue := strconv.Itoa(port)
	for _, item := range applySettings {
		if err := ctx.Err(); err != nil {
			return m.failApply(ctx, err)
		}
		value := item.value
		switch item.key {
		case "http-host", "https-host":
			value = hostValue
		case "http-port", "https-port":
			value = portValue
		}
		if _, err := m.runner.Run(ctx, "gsettings", "set", gnomeProxySchema, item.key, value); err != nil {
			return m.failApply(ctx, fmt.Errorf("sysproxy: set %s: %w", item.key, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return m.failApply(ctx, err)
	}
	return nil
}

// Restore returns GNOME's proxy keys to the values captured before the first
// successful snapshot. It is safe to call more than once.
func (m *Manager) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previous == nil {
		return nil
	}
	if m.runner == nil {
		return errors.New("sysproxy: no command runner configured")
	}
	if err := restoreSettings(ctx, m.runner, m.previous); err != nil {
		return err
	}
	m.previous = nil
	return nil
}

// Proxies returns GNOME's active HTTP and HTTPS proxy settings.
func (m *Manager) Proxies(ctx context.Context) (ProxySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ProxySettings{}, fmt.Errorf("sysproxy: query proxies: %w", err)
	}
	if !gnomeSession() {
		return ProxySettings{}, errors.New("sysproxy: unsupported session (GNOME desktop session required)")
	}
	if m.runner == nil {
		return ProxySettings{}, errors.New("sysproxy: no command runner configured")
	}
	mode, err := readGNOMESetting(ctx, m.runner, "mode")
	if err != nil {
		return ProxySettings{}, err
	}
	if mode != "manual" {
		return ProxySettings{}, errors.New("sysproxy: unsupported system proxy configuration")
	}
	same, err := m.runner.Run(ctx, "gsettings", "get", gnomeProxySchema, "use-same-proxy")
	if err != nil {
		return ProxySettings{}, fmt.Errorf("sysproxy: read use-same-proxy setting: %w", err)
	}
	useSame, err := strconv.ParseBool(strings.TrimSpace(string(same)))
	if err != nil {
		return ProxySettings{}, errors.New("sysproxy: invalid use-same-proxy setting")
	}
	httpProxy, err := gnomeProxy(ctx, m.runner, "http")
	if err != nil {
		return ProxySettings{}, err
	}
	settings := ProxySettings{HTTP: httpProxy}
	if useSame {
		settings.HTTPS = httpProxy
	} else if settings.HTTPS, err = gnomeProxy(ctx, m.runner, "https"); err != nil {
		return ProxySettings{}, err
	}
	if settings.HTTP == nil && settings.HTTPS == nil {
		return ProxySettings{}, errors.New("sysproxy: no active HTTP or HTTPS proxy is configured")
	}
	return settings, nil
}

func gnomeProxy(ctx context.Context, runner commandRunner, scheme string) (*url.URL, error) {
	host, err := readGNOMESetting(ctx, runner, scheme+"-host")
	if err != nil {
		return nil, err
	}
	portOutput, err := runner.Run(ctx, "gsettings", "get", gnomeProxySchema, scheme+"-port")
	if err != nil {
		return nil, fmt.Errorf("sysproxy: read %s proxy port: %w", scheme, err)
	}
	portText := strings.TrimSpace(string(portOutput))
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("sysproxy: invalid %s proxy port", scheme)
	}
	if host == "" && port == 0 {
		return nil, nil
	}
	proxy, err := makeProxyURL(host, port)
	if err != nil {
		return nil, fmt.Errorf("sysproxy: invalid %s proxy endpoint", scheme)
	}
	return proxy, nil
}

func readGNOMESetting(ctx context.Context, runner commandRunner, key string) (string, error) {
	output, err := runner.Run(ctx, "gsettings", "get", gnomeProxySchema, key)
	if err != nil {
		return "", fmt.Errorf("sysproxy: read %s setting: %w", key, err)
	}
	value := strings.TrimSpace(string(output))
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return "", fmt.Errorf("sysproxy: invalid %s setting", key)
	}
	return value[1 : len(value)-1], nil
}

func (m *Manager) failApply(ctx context.Context, cause error) error {
	if m.previous == nil {
		return cause
	}
	if m.runner == nil {
		return errors.Join(cause, errors.New("sysproxy: cannot roll back without a command runner"))
	}
	return m.rollbackApply(ctx, cause)
}
func (m *Manager) rollbackApply(ctx context.Context, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := restoreSettings(cleanupCtx, m.runner, m.previous); err != nil {
		return errors.Join(cause, fmt.Errorf("sysproxy: rollback: %w", err))
	}
	m.previous = nil
	return cause
}

func verifyGNOMESchema(ctx context.Context, runner commandRunner) error {
	schemas, err := runner.Run(ctx, "gsettings", "list-schemas")
	if err != nil {
		return fmt.Errorf("sysproxy: unsupported environment (cannot query GNOME proxy schema): %w", err)
	}
	found := false
	for _, schema := range strings.Fields(string(schemas)) {
		if schema == gnomeProxySchema {
			found = true
			break
		}
	}
	if !found {
		return errors.New("sysproxy: unsupported environment (GNOME proxy schema is unavailable)")
	}

	keysOutput, err := runner.Run(ctx, "gsettings", "list-keys", gnomeProxySchema)
	if err != nil {
		return fmt.Errorf("sysproxy: unsupported environment (cannot inspect GNOME proxy schema): %w", err)
	}
	keys := make(map[string]bool)
	for _, key := range strings.Fields(string(keysOutput)) {
		keys[key] = true
	}
	for _, key := range proxyKeys {
		if !keys[key] {
			return fmt.Errorf("sysproxy: unsupported environment (GNOME proxy schema lacks %q)", key)
		}
	}
	return nil
}

func captureSettings(ctx context.Context, runner commandRunner) (map[string]string, error) {
	previous := make(map[string]string, len(proxyKeys))
	for _, key := range proxyKeys {
		output, err := runner.Run(ctx, "gsettings", "get", gnomeProxySchema, key)
		if err != nil {
			return nil, fmt.Errorf("sysproxy: capture %s: %w", key, err)
		}
		value := strings.TrimSpace(string(output))
		if value == "" {
			return nil, fmt.Errorf("sysproxy: capture %s: gsettings returned an empty value", key)
		}
		previous[key] = value
	}
	return previous, nil
}

func restoreSettings(ctx context.Context, runner commandRunner, previous map[string]string) error {
	var restoreErrors []error
	set := func(key string) {
		value, ok := previous[key]
		if !ok {
			restoreErrors = append(restoreErrors, fmt.Errorf("sysproxy: missing captured value for %s", key))
			return
		}
		if _, err := runner.Run(ctx, "gsettings", "set", gnomeProxySchema, key, value); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("sysproxy: restore %s: %w", key, err))
		}
	}

	if _, ok := previous["mode"]; ok {
		if _, err := runner.Run(ctx, "gsettings", "set", gnomeProxySchema, "mode", "'none'"); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("sysproxy: disable proxy during restore: %w", err))
		}
	}
	for _, key := range proxyKeys {
		if key != "mode" {
			set(key)
		}
	}
	set("mode")
	return errors.Join(restoreErrors...)
}

func variantString(value string) string {
	return "'" + value + "'"
}

func gnomeSession() bool {
	for _, key := range []string{"XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "DESKTOP_SESSION"} {
		for _, desktop := range strings.FieldsFunc(os.Getenv(key), func(r rune) bool { return r == ':' || r == ';' }) {
			if strings.EqualFold(desktop, "gnome") || strings.EqualFold(desktop, "gnome-classic") {
				return true
			}
		}
	}
	return os.Getenv("GNOME_DESKTOP_SESSION_ID") != ""
}
