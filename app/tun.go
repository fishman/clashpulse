package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/tun"
)

const (
	tunPrepareTimeout = 2 * time.Minute
	// Mihomo serves its controller before it brings the TUN inbound up, so a
	// start is confirmed by polling tun.enable for a bounded time.
	tunConfirmTimeout = 2 * time.Second
)

var errTunNotEnabled = errors.New("TUN is not enabled")

// tunGrant is the privileged-copy seam; tests replace its functions and the
// device probed for leftover state.
type tunGrant struct {
	path, device string
	check        func(tun.Source) tun.Privilege
	prepare      func(context.Context, tun.Source) error
	revoke       func() error
}

// The path is absolute: clients show it in a setcap command, and pkexec does
// not run in this process's working directory.
func newTunGrant(stateDir string) (tunGrant, error) {
	dir, err := filepath.Abs(filepath.Join(stateDir, "tun"))
	if err != nil {
		return tunGrant{}, fmt.Errorf("clashpulse: TUN state directory: %w", err)
	}
	manager := tun.NewManager(dir)
	return tunGrant{path: manager.Path(), device: mihomo.TunDevice, check: manager.Check, prepare: manager.Prepare, revoke: manager.Revoke}, nil
}

// runtimeCapability selects the binary for the next start: the privileged
// copy when TUN is requested, allowed, and its grant matches the selected
// binary; otherwise the selected binary, which renders without TUN. It never
// prompts.
func (s *runtimeService) runtimeCapability(ctx context.Context, allowTun bool) (mihomo.Capability, tun.Privilege, error) {
	capability, err := s.selectedCapability(ctx)
	if err != nil || !s.store.Snapshot().Tun.Enabled {
		return capability, "", err
	}
	privilege := s.tun.check(tunSource(capability))
	if allowTun && privilege == tun.PrivilegeReady {
		capability.Path = s.tun.path
	}
	return capability, privilege, nil
}

// tunSource is the binary identity a privileged copy is granted for.
func tunSource(capability mihomo.Capability) tun.Source {
	return tun.Source{Path: capability.Path, Version: capability.Version}
}

// tunEnviron is the privileged Mihomo's environment with an empty PATH. Mihomo
// runs no external command on Linux, but sing-tun runs resolvectl when it finds
// it, and systemd-resolved raises a polkit prompt for each such call from a
// non-root process. DNS still reaches Mihomo through auto-route and dns-hijack.
func tunEnviron() []string {
	env := slices.DeleteFunc(os.Environ(), func(variable string) bool { return strings.HasPrefix(variable, "PATH=") })
	return append(env, "PATH=")
}

func (s *runtimeService) tunCapability(capability mihomo.Capability) bool {
	return s.tun.path != "" && capability.Path == s.tun.path
}

// notePrivilege records a Check result. no-agent describes the last Prepare,
// so it survives the missing grant that Prepare left behind.
func (s *runtimeService) notePrivilege(privilege tun.Privilege) {
	if privilege == "" || privilege == tun.PrivilegeMissing && s.tunPrivilege == core.TunPrivilegeNoAgent {
		return
	}
	s.tunPrivilege = string(privilege)
}

// activateTun starts the privileged copy when its grant is ready. A failure
// leaves the prior runtime restored and the intent saved.
func (s *runtimeService) activateTun(ctx context.Context) error {
	capability, privilege, err := s.runtimeCapability(ctx, true)
	if err != nil {
		return core.WrapActivation(core.ActivationBinary, err)
	}
	s.notePrivilege(privilege)
	if s.controller == nil || !s.tunCapability(capability) {
		s.publish()
		return nil
	}
	profile, err := s.profileForRuntime()
	if err == nil {
		err = s.startWith(ctx, profile, capability)
	}
	if err != nil {
		s.tunFailure = core.ActivationTunStart.Message()
		if public, ok := core.PublicActivation(err); ok {
			s.tunFailure = public.Stage.Message()
		}
		s.publish()
	}
	return err
}

// prepareTun runs off the service loop. A ready grant, including one made by
// a manual setcap, is kept.
func (s *runtimeService) prepareTun(ctx context.Context) error {
	capability, err := s.selectedCapability(ctx)
	if err != nil {
		return core.WrapActivation(core.ActivationBinary, err)
	}
	source := tunSource(capability)
	if s.tun.check(source) == tun.PrivilegeReady {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, tunPrepareTimeout)
	defer cancel()
	return s.tun.prepare(ctx, source)
}

func (s *runtimeService) finishTunPrepare(ctx context.Context, prepareErr error) error {
	if !s.store.Snapshot().Tun.Enabled {
		// The intent was withdrawn while the dialog was open.
		return s.tun.revoke()
	}
	if prepareErr != nil {
		s.tunFailure = core.ActivationTunPrivilege.Message()
		switch {
		case errors.Is(prepareErr, tun.ErrNoAgent):
			s.tunPrivilege = core.TunPrivilegeNoAgent
		case errors.Is(prepareErr, tun.ErrUnsupported):
			s.tunPrivilege = core.TunPrivilegeUnsupported
		case errors.Is(prepareErr, tun.ErrDeclined):
			s.tunPrivilege, s.tunFailure = core.TunPrivilegeMissing, "privilege declined"
		}
		s.publish()
		if _, ok := core.PublicActivation(prepareErr); ok {
			return prepareErr
		}
		return core.WrapActivation(core.ActivationTunPrivilege, prepareErr)
	}
	s.tunPrivilege = ""
	if err := s.activateTun(ctx); err != nil {
		return err
	}
	if s.tunPrivilege != core.TunPrivilegeReady {
		s.tunFailure = core.ActivationTunPrivilege.Message()
		s.publish()
		return core.WrapActivation(core.ActivationTunPrivilege, errors.New("clashpulse: TUN grant is not ready after authorization"))
	}
	s.tunFailure = ""
	s.publish()
	return nil
}

func (s *runtimeService) tunSnapshot(intent config.Tun) core.TunSnapshot {
	state := core.TunSnapshot{
		Enabled: intent.Enabled, Active: s.tunActive, Stack: intent.Stack, DNSMode: intent.DNSMode,
		Privilege: s.tunPrivilege, LastFailure: s.tunFailure, Leftover: s.tunLeftover,
	}
	if runtime.GOOS == "linux" {
		state.PrivilegedPath = s.tun.path
	} else {
		state.Privilege = core.TunPrivilegeUnsupported
	}
	if s.tunActive {
		state.Device = mihomo.TunDevice
	}
	return state
}
