//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	gnomeProxySchema = "org.gnome.system.proxy"
	gnomeProxyHTTP   = "org.gnome.system.proxy.http"
	gnomeProxyHTTPS  = "org.gnome.system.proxy.https"
)

// The host and port keys belong to child schemas and are addressable only by
// their own schema id; `gsettings get org.gnome.system.proxy http-host` is
// rejected even though the parent declares the child.
type proxyKey struct{ schema, key string }

// requiredSchemas are probed before writing: they, not the desktop name, decide
// whether this environment can hold a GNOME proxy configuration. A session that
// merely is not GNOME still reads these keys through GLib/GIO.
var requiredSchemas = []string{gnomeProxySchema, gnomeProxyHTTP, gnomeProxyHTTPS}

// resetKeys are the keys Apply owns. Reset returns each to its schema default
// (no proxy), which is what turning the System Proxy off means.
var resetKeys = []proxyKey{
	{gnomeProxySchema, "mode"},
	{gnomeProxySchema, "use-same-proxy"},
	{gnomeProxyHTTP, "host"},
	{gnomeProxyHTTP, "port"},
	{gnomeProxyHTTPS, "host"},
	{gnomeProxyHTTPS, "port"},
}

type setting struct {
	schema, key, value string
}

// applySettings disables the proxy while the endpoints are written, so a partial
// write is never live, then enables manual mode.
func applySettings(host string, port int) []setting {
	hostValue, portValue := variantString(host), strconv.Itoa(port)
	return []setting{
		{gnomeProxySchema, "mode", "'none'"},
		{gnomeProxyHTTP, "host", hostValue},
		{gnomeProxyHTTP, "port", portValue},
		{gnomeProxyHTTPS, "host", hostValue},
		{gnomeProxyHTTPS, "port", portValue},
		{gnomeProxySchema, "use-same-proxy", "false"},
		{gnomeProxySchema, "mode", "'manual'"},
	}
}

// Apply points the GNOME proxy settings at a loopback listener. The listener
// must already be accepting connections before this is called. Prior values are
// deliberately not preserved: Restore resets these keys to their schema
// defaults, so a configuration this application never wrote is left alone.
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
	if m.runner == nil {
		return m.failApply(ctx, errors.New("sysproxy: no command runner configured"))
	}
	if err := verifyGNOMESchemas(ctx, m.runner); err != nil {
		return m.failApply(ctx, err)
	}

	m.applied = true
	for _, item := range applySettings(host, port) {
		if err := ctx.Err(); err != nil {
			return m.failApply(ctx, err)
		}
		if _, err := m.runner.Run(ctx, "gsettings", "set", item.schema, item.key, item.value); err != nil {
			return m.failApply(ctx, fmt.Errorf("sysproxy: set %s: %w", item.key, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return m.failApply(ctx, err)
	}
	return nil
}

// Restore resets the proxy keys to their schema defaults. It is safe to call
// more than once and does nothing when this manager applied nothing.
func (m *Manager) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.applied {
		return nil
	}
	if err := m.reset(ctx); err != nil {
		return err
	}
	m.applied = false
	return nil
}

// Proxies returns GNOME's active HTTP and HTTPS proxy settings.
func (m *Manager) Proxies(ctx context.Context) (ProxySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ProxySettings{}, fmt.Errorf("sysproxy: query proxies: %w", err)
	}
	if m.runner == nil {
		return ProxySettings{}, errors.New("sysproxy: no command runner configured")
	}
	mode, err := readGNOMESetting(ctx, m.runner, gnomeProxySchema, "mode")
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
	httpProxy, err := gnomeProxy(ctx, m.runner, gnomeProxyHTTP, "http")
	if err != nil {
		return ProxySettings{}, err
	}
	settings := ProxySettings{HTTP: httpProxy}
	if useSame {
		settings.HTTPS = httpProxy
	} else if settings.HTTPS, err = gnomeProxy(ctx, m.runner, gnomeProxyHTTPS, "https"); err != nil {
		return ProxySettings{}, err
	}
	if settings.HTTP == nil && settings.HTTPS == nil {
		return ProxySettings{}, errors.New("sysproxy: no active HTTP or HTTPS proxy is configured")
	}
	return settings, nil
}

func gnomeProxy(ctx context.Context, runner commandRunner, schema, scheme string) (*url.URL, error) {
	host, err := readGNOMESetting(ctx, runner, schema, "host")
	if err != nil {
		return nil, err
	}
	portOutput, err := runner.Run(ctx, "gsettings", "get", schema, "port")
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

func readGNOMESetting(ctx context.Context, runner commandRunner, schema, key string) (string, error) {
	output, err := runner.Run(ctx, "gsettings", "get", schema, key)
	if err != nil {
		return "", fmt.Errorf("sysproxy: read %s setting: %w", key, err)
	}
	value := strings.TrimSpace(string(output))
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return "", fmt.Errorf("sysproxy: invalid %s setting", key)
	}
	return value[1 : len(value)-1], nil
}

// failApply drops settings this manager already owns before reporting the cause,
// so a partial write is never left live. A failure before the first write leaves
// a configuration this application did not write untouched.
func (m *Manager) failApply(ctx context.Context, cause error) error {
	if !m.applied {
		return cause
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.reset(cleanup); err != nil {
		return errors.Join(cause, fmt.Errorf("sysproxy: reset after failed apply: %w", err))
	}
	m.applied = false
	return cause
}

func (m *Manager) reset(ctx context.Context) error {
	if m.runner == nil {
		return errors.New("sysproxy: no command runner configured")
	}
	var failures []error
	for _, item := range resetKeys {
		if _, err := m.runner.Run(ctx, "gsettings", "reset", item.schema, item.key); err != nil {
			failures = append(failures, fmt.Errorf("sysproxy: reset %s: %w", item.key, err))
		}
	}
	return errors.Join(failures...)
}

func verifyGNOMESchemas(ctx context.Context, runner commandRunner) error {
	schemas, err := runner.Run(ctx, "gsettings", "list-schemas")
	if err != nil {
		return fmt.Errorf("sysproxy: unsupported environment (cannot query GNOME proxy schema): %w", err)
	}
	available := make(map[string]bool)
	for _, schema := range strings.Fields(string(schemas)) {
		available[schema] = true
	}
	for _, schema := range requiredSchemas {
		if !available[schema] {
			return fmt.Errorf("sysproxy: unsupported environment (GNOME proxy schema %s is unavailable)", schema)
		}
	}
	return nil
}

func variantString(value string) string {
	return "'" + value + "'"
}
