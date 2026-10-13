//go:build !linux

package tun

import "context"

// Manager reports TUN as unsupported outside Linux.
type Manager struct{ dir string }

// NewManager creates a manager for the privileged copy under dir.
func NewManager(dir string) *Manager { return &Manager{dir: dir} }

// Check reports that TUN is unsupported on this platform.
func (m *Manager) Check(Source) Privilege { return PrivilegeUnsupported }

// Prepare reports that TUN is unsupported on this platform.
func (m *Manager) Prepare(context.Context, Source) error { return ErrUnsupported }
