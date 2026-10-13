// Package tun owns the private, TUN-scoped copy of the selected Mihomo binary
// that carries cap_net_admin. It is the only code that creates, inspects, or
// removes that copy.
package tun

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/fishman/clashpulse/core"
)

// Privilege is the state of the privileged copy for a selected binary.
type Privilege string

const (
	PrivilegeReady       Privilege = core.TunPrivilegeReady
	PrivilegeMissing     Privilege = core.TunPrivilegeMissing
	PrivilegeStale       Privilege = core.TunPrivilegeStale
	PrivilegeNoSUID      Privilege = core.TunPrivilegeNoSUID
	PrivilegeUnsupported Privilege = core.TunPrivilegeUnsupported
)

// Source is the inspected, selected Mihomo binary.
type Source struct{ Path, Version string }

var (
	ErrDeclined    = errors.New("tun: privilege declined")
	ErrNoAgent     = errors.New("tun: no polkit authentication agent")
	ErrUnsupported = errors.New("tun: unsupported platform")
)

// Path returns the privileged copy's path.
func (m *Manager) Path() string { return filepath.Join(m.dir, "mihomo") }

func (m *Manager) grantPath() string { return filepath.Join(m.dir, "grant.json") }

// Revoke deletes the privileged copy and its grant record. The directory is
// the user's, so no privilege is needed; missing files are not an error.
func (m *Manager) Revoke() error {
	var errs []error
	for _, path := range []string{m.Path(), m.grantPath()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
