//go:build darwin

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	routeCommand        = "route"
	networksetupCommand = "networksetup"
	webProxyKind        = "web"
	secureProxyKind     = "secureweb"
)

// Apply points the active macOS network service's HTTP and HTTPS proxies at a
// loopback listener after verifying the listener is accepting TCP connections.
func (m *Manager) Apply(ctx context.Context, listenerAddress string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return m.failDarwinApply(ctx, fmt.Errorf("sysproxy: apply: %w", err))
	}
	host, port, err := splitListenerAddress(listenerAddress)
	if err != nil {
		return m.failDarwinApply(ctx, fmt.Errorf("sysproxy: apply: %w", err))
	}
	if err := verifyListenerReady(ctx, host, port); err != nil {
		return m.failDarwinApply(ctx, fmt.Errorf("sysproxy: listener is not ready: %w", err))
	}
	if m.runner == nil {
		return m.failDarwinApply(ctx, errors.New("sysproxy: no command runner configured"))
	}

	service, err := activeNetworkService(ctx, m.runner)
	if err != nil {
		return m.failDarwinApply(ctx, err)
	}
	if m.previous != nil && m.previous["service"] != service {
		if err := restoreDarwinSettings(ctx, m.runner, m.previous); err != nil {
			return m.failDarwinApply(ctx, fmt.Errorf("sysproxy: restore previously managed service: %w", err))
		}
		m.previous = nil
	}
	if err := verifyNoPACOrAutodiscovery(ctx, m.runner, service); err != nil {
		return m.failDarwinApply(ctx, err)
	}

	if m.previous == nil {
		previous, err := captureDarwinSettings(ctx, m.runner, service)
		if err != nil {
			return err
		}
		m.previous = previous
	}

	portValue := strconv.Itoa(port)
	for _, kind := range []string{webProxyKind, secureProxyKind} {
		if err := ctx.Err(); err != nil {
			return m.failDarwinApply(ctx, err)
		}
		if err := setDarwinProxy(ctx, m.runner, kind, service, host, portValue); err != nil {
			return m.failDarwinApply(ctx, err)
		}
		if _, err := m.runner.Run(ctx, networksetupCommand, "-set"+kind+"proxystate", service, "on"); err != nil {
			return m.failDarwinApply(ctx, fmt.Errorf("sysproxy: enable %s proxy: %w", kind, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return m.failDarwinApply(ctx, err)
	}
	return nil
}

// Restore returns the network service captured before the first successful
// Apply call to its saved HTTP and HTTPS settings.
func (m *Manager) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previous == nil {
		return nil
	}
	if m.runner == nil {
		return errors.New("sysproxy: no command runner configured")
	}
	if err := restoreDarwinSettings(ctx, m.runner, m.previous); err != nil {
		return err
	}
	m.previous = nil
	return nil
}

// Proxies returns the active macOS network service's HTTP and HTTPS proxies.
func (m *Manager) Proxies(ctx context.Context) (ProxySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ProxySettings{}, fmt.Errorf("sysproxy: query proxies: %w", err)
	}
	if m.runner == nil {
		return ProxySettings{}, errors.New("sysproxy: no command runner configured")
	}
	service, err := activeNetworkService(ctx, m.runner)
	if err != nil {
		return ProxySettings{}, err
	}
	if err := verifyNoPACOrAutodiscovery(ctx, m.runner, service); err != nil {
		return ProxySettings{}, err
	}
	web, err := getDarwinProxy(ctx, m.runner, webProxyKind, service)
	if err != nil {
		return ProxySettings{}, err
	}
	secure, err := getDarwinProxy(ctx, m.runner, secureProxyKind, service)
	if err != nil {
		return ProxySettings{}, err
	}
	settings := ProxySettings{}
	if web.enabled {
		if web.auth {
			return ProxySettings{}, errors.New("sysproxy: unsupported authenticated HTTP proxy")
		}
		settings.HTTP, err = makeProxyURL(web.host, web.port)
		if err != nil {
			return ProxySettings{}, fmt.Errorf("sysproxy: invalid HTTP proxy endpoint: %w", err)
		}
	}
	if secure.enabled {
		if secure.auth {
			return ProxySettings{}, errors.New("sysproxy: unsupported authenticated HTTPS proxy")
		}
		settings.HTTPS, err = makeProxyURL(secure.host, secure.port)
		if err != nil {
			return ProxySettings{}, fmt.Errorf("sysproxy: invalid HTTPS proxy endpoint: %w", err)
		}
	}
	if settings.HTTP == nil && settings.HTTPS == nil {
		return ProxySettings{}, errors.New("sysproxy: no active HTTP or HTTPS proxy is configured")
	}
	return settings, nil
}

func (m *Manager) failDarwinApply(ctx context.Context, cause error) error {
	if m.previous == nil {
		return cause
	}
	if m.runner == nil {
		return errors.Join(cause, errors.New("sysproxy: cannot roll back without a command runner"))
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := restoreDarwinSettings(cleanupCtx, m.runner, m.previous); err != nil {
		return errors.Join(cause, fmt.Errorf("sysproxy: rollback: %w", err))
	}
	m.previous = nil
	return cause
}

func setDarwinProxy(ctx context.Context, runner commandRunner, kind, service, host, port string) error {
	if _, err := runner.Run(ctx, networksetupCommand, "-set"+kind+"proxy", service, host, port, "off"); err != nil {
		return fmt.Errorf("sysproxy: set %s proxy: %w", kind, err)
	}
	return nil
}

func activeNetworkService(ctx context.Context, runner commandRunner) (string, error) {
	route, err := runner.Run(ctx, routeCommand, "-n", "get", "default")
	if err != nil {
		return "", fmt.Errorf("sysproxy: find default route interface: %w", err)
	}
	device, err := parseDefaultInterface(string(route))
	if err != nil {
		return "", fmt.Errorf("sysproxy: find default route interface: %w", err)
	}
	services, err := runner.Run(ctx, networksetupCommand, "-listnetworkserviceorder")
	if err != nil {
		return "", fmt.Errorf("sysproxy: list network services: %w", err)
	}
	service, err := parseNetworkService(string(services), device)
	if err != nil {
		return "", fmt.Errorf("sysproxy: find service for default route: %w", err)
	}
	return service, nil
}

func verifyNoPACOrAutodiscovery(ctx context.Context, runner commandRunner, service string) error {
	pacOutput, err := runner.Run(ctx, networksetupCommand, "-getautoproxyurl", service)
	if err != nil {
		return fmt.Errorf("sysproxy: inspect PAC settings: %w", err)
	}
	pac, err := parsePACEnabled(string(pacOutput))
	if err != nil {
		return fmt.Errorf("sysproxy: inspect PAC settings: %w", err)
	}
	if pac {
		return errors.New("sysproxy: unsupported existing configuration (active PAC proxy cannot be safely restored)")
	}
	autoOutput, err := runner.Run(ctx, networksetupCommand, "-getproxyautodiscovery", service)
	if err != nil {
		return fmt.Errorf("sysproxy: inspect proxy autodiscovery: %w", err)
	}
	auto, err := parseAutoDiscoveryEnabled(string(autoOutput))
	if err != nil {
		return fmt.Errorf("sysproxy: inspect proxy autodiscovery: %w", err)
	}
	if auto {
		return errors.New("sysproxy: unsupported existing configuration (active proxy autodiscovery cannot be safely restored)")
	}
	return nil
}

func captureDarwinSettings(ctx context.Context, runner commandRunner, service string) (map[string]string, error) {
	web, err := getDarwinProxy(ctx, runner, "web", service)
	if err != nil {
		return nil, err
	}
	if web.auth {
		return nil, errors.New("sysproxy: unsupported existing configuration (authenticated HTTP proxy cannot be safely restored)")
	}
	secure, err := getDarwinProxy(ctx, runner, "secureweb", service)
	if err != nil {
		return nil, err
	}
	if secure.auth {
		return nil, errors.New("sysproxy: unsupported existing configuration (authenticated HTTPS proxy cannot be safely restored)")
	}
	return map[string]string{
		"service":           service,
		"web-enabled":       boolSetting(web.enabled),
		"web-host":          web.host,
		"web-port":          strconv.Itoa(web.port),
		"secureweb-enabled": boolSetting(secure.enabled),
		"secureweb-host":    secure.host,
		"secureweb-port":    strconv.Itoa(secure.port),
	}, nil
}

type darwinProxySettings struct {
	enabled bool
	host    string
	port    int
	auth    bool
}

func getDarwinProxy(ctx context.Context, runner commandRunner, kind, service string) (darwinProxySettings, error) {
	output, err := runner.Run(ctx, networksetupCommand, "-get"+kind+"proxy", service)
	if err != nil {
		return darwinProxySettings{}, fmt.Errorf("sysproxy: read %s proxy settings: %w", kind, err)
	}
	settings, err := parseNetworkProxy(string(output))
	if err != nil {
		return darwinProxySettings{}, fmt.Errorf("sysproxy: read %s proxy settings: %w", kind, err)
	}
	return darwinProxySettings{enabled: settings.enabled, host: settings.host, port: settings.port, auth: settings.auth}, nil
}

func restoreDarwinSettings(ctx context.Context, runner commandRunner, previous map[string]string) error {
	service, ok := previous["service"]
	if !ok || strings.TrimSpace(service) == "" {
		return errors.New("sysproxy: missing captured network service")
	}
	var restoreErrors []error
	for _, kind := range []string{webProxyKind, secureProxyKind} {
		host, hostOK := previous[kind+"-host"]
		port, portOK := previous[kind+"-port"]
		enabled, enabledOK := previous[kind+"-enabled"]
		if !hostOK || !portOK || !enabledOK {
			restoreErrors = append(restoreErrors, fmt.Errorf("sysproxy: missing captured %s proxy settings", kind))
			continue
		}
		if enabled == "off" && (host == "" || port == "0") {
			if _, err := runner.Run(ctx, networksetupCommand, "-set"+kind+"proxystate", service, "off"); err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("sysproxy: restore %s proxy state: %w", kind, err))
			}
			continue
		}
		if err := setDarwinProxy(ctx, runner, kind, service, host, port); err != nil {
			restoreErrors = append(restoreErrors, err)
			continue
		}
		if _, err := runner.Run(ctx, networksetupCommand, "-set"+kind+"proxystate", service, enabled); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("sysproxy: restore %s proxy state: %w", kind, err))
		}
	}
	return errors.Join(restoreErrors...)
}

func boolSetting(value bool) string {
	if value {
		return "on"
	}
	return "off"
}
