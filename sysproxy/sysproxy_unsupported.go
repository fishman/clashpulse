//go:build !linux && !darwin && !windows

package sysproxy

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("sysproxy: unsupported platform (system proxy adapter is not implemented)")

// Apply reports that system proxy control is unavailable on this platform.
func (m *Manager) Apply(context.Context, string) error { return errUnsupported }

// Restore reports that system proxy control is unavailable on this platform.
func (m *Manager) Restore(context.Context) error { return errUnsupported }

// Proxies reports that system proxy lookup is unavailable on this platform.
func (m *Manager) Proxies(context.Context) (ProxySettings, error) {
	return ProxySettings{}, errUnsupported
}
