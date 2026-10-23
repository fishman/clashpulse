package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/dns"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
	"github.com/fishman/clashpulse/resources"
)

func (s *runtimeService) selectedCapability(ctx context.Context) (mihomo.Capability, error) {
	selection := s.store.Snapshot().Mihomo.Binary
	kind := "path"
	if selection == "system" || selection == "bundled" {
		kind = selection
	}
	if kind == "bundled" {
		return mihomo.Capability{}, fmt.Errorf("clashpulse: bundled Mihomo is not installed")
	}
	return mihomo.Inspect(ctx, mihomo.Selection{Kind: kind, Path: selection})
}

func (s *runtimeService) renderProfile(ctx context.Context, profile []byte) ([]byte, error) {
	capability, err := s.selectedCapability(ctx)
	if err != nil {
		return nil, err
	}
	intent := s.store.Snapshot()
	home, paths, err := s.registry.PathsWithHome(intent)
	if err == nil {
		return s.renderWithHome(ctx, profile, intent, home, paths, capability)
	}
	if s.controller != nil {
		return nil, fmt.Errorf("clashpulse: managed resources are unavailable while Mihomo is running")
	}
	plan, err := s.registry.Stage(ctx, intent, download.Auto)
	if err != nil {
		return nil, err
	}
	defer plan.Abort()
	if err := plan.Validate(func(home string, paths map[string]string) error {
		_, validationErr := s.validatedCandidate(ctx, profile, intent, home, paths, capability)
		return validationErr
	}); err != nil {
		return nil, err
	}
	if _, err := plan.Commit(); err != nil {
		return nil, err
	}
	rollback := func(cause error) ([]byte, error) {
		return nil, errors.Join(cause, plan.Rollback())
	}
	home, paths, err = s.registry.PathsWithHome(intent)
	if err != nil {
		return rollback(err)
	}
	candidate, err := s.validatedCandidate(ctx, profile, intent, home, paths, capability)
	if err != nil {
		return rollback(err)
	}
	if err := plan.Finalize(); err != nil {
		return rollback(err)
	}
	s.resourceDirty.Store(true)
	return candidate, nil
}

func (s *runtimeService) renderWithHome(ctx context.Context, profile []byte, intent config.Snapshot, home string, paths map[string]string, capability mihomo.Capability) ([]byte, error) {
	if !s.tunCapability(capability) {
		intent.Tun.Enabled = false
	}
	if err := dns.Validate(ctx, intent, paths); err != nil {
		return nil, core.WrapActivation(core.ActivationConfigValidation, err)
	}
	// The listener is reused across renders so an unchanged intent renders the
	// same bytes and a reload does not restart Mihomo.
	var listener mihomo.StreamListener
	if intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream {
		listener = s.streamProbe
		if listener.Port <= 0 {
			var err error
			if listener, err = newStreamListener(); err != nil {
				return nil, core.WrapActivation(core.ActivationConfigValidation, err)
			}
		}
	}
	candidate, err := mihomo.Render(profile, intent, mihomo.ManagedPaths(paths), mihomo.ControllerSettings{
		Address: s.controllerAddress, Socket: s.controllerSocket, Secret: s.secret, HomeDir: home, ProxyPort: intent.Mihomo.MixedPort, StreamProbe: listener,
	}, capability)
	if err != nil {
		var missing *mihomo.CapabilityError
		if errors.As(err, &missing) {
			return nil, core.WrapActivationResource(core.ActivationBinary, missing.ResourceID, err)
		}
		return nil, core.WrapActivation(core.ActivationConfigValidation, err)
	}
	s.streamProbe = listener
	return candidate, nil
}

func (s *runtimeService) validatedCandidate(ctx context.Context, profile []byte, intent config.Snapshot, home string, paths map[string]string, capability mihomo.Capability) ([]byte, error) {
	candidate, err := s.renderWithHome(ctx, profile, intent, home, paths, capability)
	if err != nil {
		return nil, err
	}
	if err := s.validateWithHome(ctx, candidate, capability, home); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (s *runtimeService) validateGenerated(ctx context.Context, candidate []byte) error {
	capability, err := s.selectedCapability(ctx)
	if err != nil {
		return err
	}
	home, _, err := s.registry.PathsWithHome(s.store.Snapshot())
	if err != nil {
		return err
	}
	return s.validateWithHome(ctx, candidate, capability, home)
}

func (s *runtimeService) validateWithHome(ctx context.Context, candidate []byte, capability mihomo.Capability, home string) error {
	if err := privateDirectory(s.stateDir); err != nil {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	file, err := os.CreateTemp(s.stateDir, ".candidate-*.yaml")
	if err != nil {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	if _, err := file.Write(candidate); err != nil {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	if err := file.Sync(); err != nil {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	if err := file.Close(); err != nil {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	if err := mihomo.ValidateInHome(ctx, capability, file.Name(), home); err != nil {
		return core.WrapActivation(core.ActivationConfigValidation, err)
	}
	return nil
}

type runtimeBackup struct {
	generated        []byte
	cap              mihomo.Capability
	home             string
	running          bool
	proxyActive      bool
	selected         map[string]string
	resourcePlan     *resources.Plan
	overrides        []core.ConfigOverrideSnapshot
	pendingOverrides []core.ConfigOverrideSnapshot
	localProfile     []byte
	generatedPath    string
	durableConfig    []byte
	durableExisted   bool
}

func activationResourceError(err error) error {
	if _, ok := core.PublicActivation(err); ok || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var resource *resources.ResourceFailure
	if errors.As(err, &resource) {
		return core.WrapActivationResource(core.ActivationResources, resource.ResourceID, err)
	}
	return core.WrapActivation(core.ActivationResources, err)
}

// systemProxyActivation keeps an unconfigurable desktop environment out of the
// retryable apply failure, which would otherwise read as a transient error.
func systemProxyActivation(err error) error {
	if unsupportedSystemProxy(err) {
		return core.WrapActivation(core.ActivationSystemProxyUnsupported, err)
	}
	return core.WrapActivation(core.ActivationSystemProxy, err)
}

func overrideSnapshots(profile, candidate []byte) ([]core.ConfigOverrideSnapshot, error) {
	items, err := mihomo.ExplainOverrides(profile, candidate)
	if err != nil {
		return nil, core.WrapActivation(core.ActivationConfigValidation, err)
	}
	report := make([]core.ConfigOverrideSnapshot, 0, len(items))
	for _, item := range items {
		report = append(report, core.ConfigOverrideSnapshot{Key: item.Key, Change: item.Change})
	}
	return report, nil
}

func (s *runtimeService) commitOverrideReport(report []core.ConfigOverrideSnapshot) {
	s.configOverrides = append([]core.ConfigOverrideSnapshot(nil), report...)
	s.configReportPending = false
	s.publish()
}

func (s *runtimeService) applyGenerated(ctx context.Context, profile []byte) error {
	capability, privilege, err := s.runtimeCapability(ctx, true)
	if err != nil {
		return core.WrapActivation(core.ActivationBinary, err)
	}
	s.notePrivilege(privilege)
	intent := s.store.Snapshot()
	home, paths, err := s.registry.PathsWithHome(intent)
	if err == nil {
		candidate, err := s.renderWithHome(ctx, profile, intent, home, paths, capability)
		if err != nil {
			return err
		}
		return s.applyActivationCandidate(ctx, profile, candidate, capability, home)
	}
	plan, err := s.registry.Stage(ctx, intent, download.Auto)
	if err != nil {
		return activationResourceError(err)
	}
	if err := plan.Validate(func(home string, paths map[string]string) error {
		_, validationErr := s.validatedCandidate(ctx, profile, intent, home, paths, capability)
		return validationErr
	}); err != nil {
		_ = plan.Abort()
		return activationResourceError(err)
	}
	if err := s.applyResourcePlan(ctx, plan, profile, capability, true); err != nil {
		return activationResourceError(err)
	}
	return nil
}

func (s *runtimeService) captureLocalActivation(backup *runtimeBackup) error {
	if s.localProfile == nil {
		return nil
	}
	backup.localProfile = bytes.Clone(s.localProfile)
	backup.generatedPath = s.generatedPath
	data, err := os.ReadFile(filepath.Join(s.stateDir, "generated.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	backup.durableConfig, backup.durableExisted = data, err == nil
	return nil
}

func (s *runtimeService) restoreDurable(backup *runtimeBackup) error {
	if backup.generatedPath == "" {
		return nil
	}
	path := filepath.Join(s.stateDir, "generated.yaml")
	if backup.durableExisted {
		return config.Write(path, backup.durableConfig)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *runtimeService) activationCandidatePath() string {
	if s.localProfile != nil {
		return filepath.Join(s.stateDir, "generated.yaml")
	}
	return s.configPath()
}

func (s *runtimeService) applyActivationCandidate(ctx context.Context, profile, candidate []byte, capability mihomo.Capability, home string) error {
	report, err := overrideSnapshots(profile, candidate)
	if err != nil {
		return err
	}
	s.activationBackup = &runtimeBackup{generated: bytes.Clone(s.generated), cap: s.cap, home: s.resourceHome, running: s.controller != nil, proxyActive: s.proxyActive, selected: selectedGroups(s.groups), overrides: s.configOverrides, pendingOverrides: report}
	if err := s.captureLocalActivation(s.activationBackup); err != nil {
		s.activationBackup = nil
		return err
	}
	if err := s.applyCandidateAt(ctx, candidate, capability, home, s.activationCandidatePath(), s.activationBackup); err != nil {
		s.activationBackup = nil
		return err
	}
	return nil
}

func (s *runtimeService) applyResourcePlan(ctx context.Context, plan *resources.Plan, profile []byte, capability mihomo.Capability, activation bool) error {
	backup := &runtimeBackup{generated: bytes.Clone(s.generated), cap: s.cap, home: s.resourceHome, running: s.controller != nil, proxyActive: s.proxyActive, selected: selectedGroups(s.groups), resourcePlan: plan, overrides: s.configOverrides}
	if activation {
		if err := s.captureLocalActivation(backup); err != nil {
			_ = plan.Abort()
			return err
		}
	}
	defer s.resourceDirty.Store(true)
	if err := ctx.Err(); err != nil {
		_ = plan.Abort()
		return err
	}
	if !plan.Changed() {
		if _, err := plan.Commit(); err != nil {
			_ = plan.Abort()
			return err
		}
		if !activation && backup.running {
			return plan.Finalize()
		}
		intent := s.store.Snapshot()
		home, paths, err := s.registry.PathsWithHome(intent)
		if err != nil {
			return s.restoreResourceRuntime(ctx, backup, err, true)
		}
		candidate, err := s.renderWithHome(ctx, profile, intent, home, paths, capability)
		if err != nil {
			return s.restoreResourceRuntime(ctx, backup, err, true)
		}
		if activation {
			if err := s.applyActivationCandidate(ctx, profile, candidate, capability, home); err != nil {
				return s.restoreResourceRuntime(ctx, backup, err, true)
			}
			s.activationBackup.resourcePlan = plan
			return nil
		}
		report, err := overrideSnapshots(profile, candidate)
		if err != nil {
			return s.restoreResourceRuntime(ctx, backup, err, true)
		}
		if err := s.applyCandidate(ctx, candidate, capability, home); err != nil {
			return s.restoreResourceRuntime(ctx, backup, err, true)
		}
		if err := plan.Finalize(); err != nil {
			return s.restoreResourceRuntime(ctx, backup, err, true)
		}
		s.commitOverrideReport(report)
		return nil
	}
	if backup.running {
		if err := s.proxy.Restore(ctx); err != nil {
			_ = plan.Abort()
			return err
		}
		s.proxyActive = false
		if err := s.stopMonitorAndProcess(ctx); err != nil {
			return s.restoreResourceRuntime(ctx, backup, err, false)
		}
	}
	if _, err := plan.Commit(); err != nil {
		_ = plan.Abort()
		return s.restoreResourceRuntime(ctx, backup, err, false)
	}
	intent := s.store.Snapshot()
	home, paths, err := s.registry.PathsWithHome(intent)
	var report []core.ConfigOverrideSnapshot
	if err == nil {
		var candidate []byte
		candidate, err = s.renderWithHome(ctx, profile, intent, home, paths, capability)
		if err == nil {
			report, err = overrideSnapshots(profile, candidate)
		}
		if err == nil {
			if activation {
				err = s.applyCandidateAt(ctx, candidate, capability, home, s.activationCandidatePath(), backup)
			} else {
				err = s.applyCandidate(ctx, candidate, capability, home)
			}
		}
		if err == nil && backup.running {
			err = s.restoreSelections(ctx, backup.selected)
		}
	}
	if err != nil {
		return s.restoreResourceRuntime(ctx, backup, err, true)
	}
	if activation {
		backup.pendingOverrides = report
		s.activationBackup = backup
		return nil
	}
	if err := plan.Finalize(); err != nil {
		return s.restoreResourceRuntime(ctx, backup, err, true)
	}
	s.commitOverrideReport(report)
	return nil
}

func (s *runtimeService) restoreResourceRuntime(ctx context.Context, backup *runtimeBackup, cause error, committed bool) error {
	cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer done()
	var cleanupErr error
	rollbackFailed := func(err error) error {
		return core.WrapActivation(core.ActivationRollback, errors.Join(cause, cleanupErr, err))
	}
	if s.controller != nil {
		if err := s.proxy.Restore(cleanup); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("clashpulse: disable candidate system proxy: %w", err))
		} else {
			s.proxyActive = false
		}
		if err := s.stopMonitorAndProcess(cleanup); err != nil {
			return rollbackFailed(fmt.Errorf("clashpulse: stop candidate before resource rollback: %w", err))
		}
	}
	if err := s.process.Stop(cleanup); err != nil {
		return rollbackFailed(fmt.Errorf("clashpulse: wait for stopped Mihomo: %w", err))
	}
	if err := s.restoreDurable(backup); err != nil {
		return rollbackFailed(fmt.Errorf("clashpulse: restore prior generated configuration: %w", err))
	}
	if committed {
		if err := backup.resourcePlan.Rollback(); err != nil {
			return rollbackFailed(fmt.Errorf("clashpulse: restore resource files: %w", err))
		}
	}
	if err := backup.resourcePlan.Abort(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("clashpulse: remove staged resources: %w", err))
	}
	if backup.running {
		path := s.configPath()
		if err := config.Write(path, backup.generated); err != nil {
			return rollbackFailed(fmt.Errorf("clashpulse: restore generated configuration: %w", err))
		}
		if err := s.startProcess(cleanup, backup.cap, backup.home, path); err != nil {
			return rollbackFailed(fmt.Errorf("clashpulse: restart previous Mihomo: %w", err))
		}
		s.generated, s.cap, s.resourceHome = bytes.Clone(backup.generated), backup.cap, backup.home
		if err := s.restoreSelections(cleanup, backup.selected); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("clashpulse: restore proxy selection: %w", err))
		}
		if backup.proxyActive && !s.proxyActive {
			if err := s.applySystemProxy(cleanup); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("clashpulse: restore system proxy: %w", err))
			} else {
				s.proxyActive = true
			}
		} else if !backup.proxyActive && s.proxyActive {
			if err := s.proxy.Restore(cleanup); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("clashpulse: clear system proxy: %w", err))
			} else {
				s.proxyActive = false
			}
		}
		s.publish()
	} else if len(backup.generated) > 0 {
		if err := config.Write(s.configPath(), backup.generated); err != nil {
			return rollbackFailed(fmt.Errorf("clashpulse: restore generated configuration: %w", err))
		}
		s.generated, s.cap, s.resourceHome = backup.generated, backup.cap, backup.home
	} else {
		if err := os.Remove(s.configPath()); err != nil && !os.IsNotExist(err) {
			return rollbackFailed(err)
		}
		s.generated, s.cap, s.resourceHome = nil, mihomo.Capability{}, ""
	}
	if cleanupErr != nil {
		return rollbackFailed(nil)
	}
	s.commitOverrideReport(backup.overrides)
	return cause
}
func (s *runtimeService) restoreActivation(ctx context.Context, previousProfile []byte) (result error) {
	backup := s.activationBackup
	s.activationBackup = nil
	if backup == nil {
		return fmt.Errorf("clashpulse: no prior runtime was captured")
	}
	if backup.generatedPath != "" && backup.resourcePlan == nil {
		defer func() {
			if result != nil {
				return
			}
			if err := s.restoreDurable(backup); err != nil {
				result = core.WrapActivation(core.ActivationRollback, errors.Join(result, err))
			}
		}()
	}
	if backup.resourcePlan != nil {
		s.resourceDirty.Store(true)
		return s.restoreResourceRuntime(ctx, backup, nil, true)
	}
	if !backup.running {
		if err := s.stop(ctx); err != nil {
			return err
		}
		if len(previousProfile) == 0 {
			if err := os.Remove(s.configPath()); err != nil && !os.IsNotExist(err) {
				return err
			}
			s.generated, s.cap, s.resourceHome = nil, mihomo.Capability{}, ""
		} else {
			if err := config.Write(s.configPath(), backup.generated); err != nil {
				return err
			}
			s.generated, s.cap, s.resourceHome = backup.generated, backup.cap, backup.home
		}
		s.commitOverrideReport(backup.overrides)
		return nil
	}
	if err := s.applyCandidate(ctx, backup.generated, backup.cap, backup.home); err != nil {
		return err
	}
	if err := s.restoreSelections(ctx, backup.selected); err != nil {
		return err
	}
	if backup.proxyActive && !s.proxyActive {
		if err := s.applySystemProxy(ctx); err != nil {
			return err
		}
		s.proxyActive = true
	} else if !backup.proxyActive && s.proxyActive {
		if err := s.proxy.Restore(ctx); err != nil {
			return err
		}
		s.proxyActive = false
	}
	s.commitOverrideReport(backup.overrides)
	return nil
}

func (s *runtimeService) applyCandidate(ctx context.Context, candidate []byte, capability mihomo.Capability, home string) error {
	return s.applyCandidateAt(ctx, candidate, capability, home, s.configPath(), nil)
}

func (s *runtimeService) applyCandidateAt(ctx context.Context, candidate []byte, capability mihomo.Capability, home, activePath string, backup *runtimeBackup) (result error) {
	oldPath := s.configPath()
	if backup != nil && activePath != oldPath {
		defer func() {
			if result != nil {
				if err := s.restoreDurable(backup); err != nil {
					result = core.WrapActivation(core.ActivationRollback, errors.Join(result, err))
				}
			}
		}()
	}
	if err := s.validateWithHome(ctx, candidate, capability, home); err != nil {
		return err
	}
	if !s.forceRestart && activePath == oldPath && bytes.Equal(candidate, s.generated) && s.controller != nil && capability == s.cap && home == s.resourceHome {
		s.snapshot.Binary.LastCompatibilityFailure = ""
		return nil
	}
	// A mode change alone is switched live, so open connections survive it. A
	// failed switch falls through to the restart below.
	if from, to, ok := mihomo.ModeChange(s.generated, candidate); ok && !s.forceRestart && activePath == oldPath && s.controller != nil && capability == s.cap && home == s.resourceHome {
		if err := s.controller.SetMode(ctx, to); err == nil {
			if err := config.Write(activePath, candidate); err != nil {
				if from == "" {
					from = core.ProxyModeRule
				}
				_ = s.controller.SetMode(context.WithoutCancel(ctx), from)
				return core.WrapActivation(core.ActivationStateCommit, err)
			}
			s.generated = bytes.Clone(candidate)
			s.snapshot.Binary.LastCompatibilityFailure = ""
			return nil
		}
	}
	priorReportPending := s.configReportPending
	s.configReportPending = true
	s.publish()
	oldConfig, oldCap, oldHome := bytes.Clone(s.generated), s.cap, s.resourceHome
	wasRunning := s.controller != nil
	wasProxy := s.proxyActive
	oldSelected := selectedGroups(s.groups)
	restore := func() {
		if s.restorePrevious(ctx, oldConfig, oldCap, oldHome, oldPath, wasProxy, oldSelected) {
			s.configReportPending = priorReportPending
			s.publish()
		}
	}
	if wasRunning {
		if err := s.proxy.Restore(ctx); err != nil {
			s.configReportPending = priorReportPending
			s.publish()
			return systemProxyActivation(err)
		}
		s.proxyActive = false
		if err := s.stopMonitorAndProcess(ctx); err != nil {
			return core.WrapActivation(core.ActivationRollback, err)
		}
	}
	if !wasRunning {
		_, err := net.InterfaceByName(s.tun.device)
		s.tunLeftover = err == nil
	}
	if err := config.Write(activePath, candidate); err != nil {
		if wasRunning {
			restore()
		}
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	if err := s.startProcess(ctx, capability, home, activePath); err != nil {
		if wasRunning {
			restore()
		} else if len(oldConfig) > 0 {
			if activePath == oldPath {
				_ = config.Write(activePath, oldConfig)
			}
		}
		return err
	}
	if wasRunning {
		if err := s.restoreSelections(ctx, oldSelected); err != nil {
			_ = s.stopMonitorAndProcess(ctx)
			restore()
			return core.WrapActivation(core.ActivationControllerReadiness, err)
		}
	}
	s.generated, s.cap, s.resourceHome = bytes.Clone(candidate), capability, home
	abandon := func() {
		_ = s.stopMonitorAndProcess(ctx)
		if wasRunning {
			restore()
		} else if len(oldConfig) > 0 && activePath == oldPath {
			_ = config.Write(activePath, oldConfig)
		}
	}
	if s.store.Snapshot().App.SystemProxy.Enabled {
		if err := s.applySystemProxy(ctx); err != nil {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			restoreErr := s.proxy.Restore(cleanup)
			done()
			if restoreErr != nil {
				s.proxyActive = true
				s.publish()
				return core.WrapActivation(core.ActivationRollback, errors.Join(err, restoreErr))
			}
			abandon()
			return systemProxyActivation(err)
		}
		s.proxyActive = true
	}
	if s.tunCapability(capability) && !s.tunActive {
		cause := core.WrapActivation(core.ActivationTunStart, errors.New("clashpulse: Mihomo reported tun.enable false"))
		s.tunFailure = core.ActivationTunStart.Message()
		if s.proxyActive {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			restoreErr := s.proxy.Restore(cleanup)
			done()
			if restoreErr != nil {
				s.publish()
				return core.WrapActivation(core.ActivationRollback, errors.Join(cause, restoreErr))
			}
			s.proxyActive = false
		}
		abandon()
		return cause
	}
	s.snapshot.Binary.LastCompatibilityFailure = ""
	s.publish()
	return nil
}

func (s *runtimeService) restorePrevious(ctx context.Context, candidate []byte, capability mihomo.Capability, home, path string, enableProxy bool, selected map[string]string) bool {
	if len(candidate) == 0 || capability.Path == "" {
		return false
	}
	if err := config.Write(path, candidate); err != nil {
		s.reportError("rollback", err)
		return false
	}
	if err := s.startProcess(ctx, capability, home, path); err != nil {
		s.reportError("rollback", err)
		return false
	}
	s.generated, s.cap, s.resourceHome = bytes.Clone(candidate), capability, home
	restored := true
	if err := s.restoreSelections(ctx, selected); err != nil {
		s.reportError("rollback", err)
		restored = false
	}
	if enableProxy {
		if err := s.applySystemProxy(ctx); err != nil {
			s.reportError("rollback", err)
			restored = false
		} else {
			s.proxyActive = true
		}
	}
	return restored
}

func selectedGroups(groups map[string]mihomo.Group) map[string]string {
	selected := make(map[string]string)
	for _, group := range groups {
		if selectorType(group.Type) {
			selected[group.Name] = group.Selected
		}
	}
	return selected
}

func (s *runtimeService) restoreSelections(ctx context.Context, selected map[string]string) error {
	if len(selected) == 0 || s.controller == nil {
		return nil
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	changed := false
	for _, name := range names {
		group, ok := s.groups[opaqueID(name)]
		if !ok || !selectorType(group.Type) {
			continue
		}
		if group.Selected == selected[name] {
			continue
		}
		eligible := false
		for _, member := range group.Proxies {
			if member == selected[name] {
				eligible = true
				break
			}
		}
		if !eligible {
			continue
		}
		if err := s.controller.Select(ctx, name, selected[name]); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return s.refreshGroups(ctx)
	}
	return nil
}

func (s *runtimeService) startProcess(ctx context.Context, capability mihomo.Capability, home, path string) error {
	if home != s.registry.Home() {
		return core.WrapActivation(core.ActivationResources, fmt.Errorf("clashpulse: invalid managed data home"))
	}
	generated, err := os.ReadFile(path)
	if err != nil {
		return core.WrapActivation(core.ActivationProcessStart, err)
	}
	port, err := mihomo.MixedPort(generated)
	if err != nil {
		return core.WrapActivation(core.ActivationProcessStart, err)
	}
	// A rollback restarts an older file, so the running listener is the one in
	// the file started, not the one of the last render.
	listener, err := mihomo.StreamProbe(generated)
	if err != nil {
		return core.WrapActivation(core.ActivationProcessStart, err)
	}
	// Mihomo logs a failed listener bind and keeps running without its proxy,
	// so a taken port must be refused before start.
	probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return core.WrapActivation(core.ActivationMixedPort, err)
	}
	if err := probe.Close(); err != nil {
		return core.WrapActivation(core.ActivationMixedPort, err)
	}
	s.streamProbe = listener
	if listener.Port > 0 {
		probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", listener.Port))
		if err == nil {
			err = probe.Close()
		}
		if err != nil {
			// The next render picks a new port.
			s.streamProbe = mihomo.StreamListener{}
			return core.WrapActivation(core.ActivationStreamProbePort, err)
		}
	}
	if err := s.clearControllerEndpoint(); err != nil {
		return core.WrapActivation(core.ActivationProcessStart, err)
	}
	plan := mihomo.StartPlan{Capability: capability, ConfigPath: path, Args: []string{"-d", home}}
	if s.tunCapability(capability) {
		plan.Env = tunEnviron()
	}
	if err := s.process.Start(s.processCtx, plan); err != nil {
		return core.WrapActivation(core.ActivationProcessStart, err)
	}
	controller, err := s.newController()
	if err != nil {
		return s.stopStartedProcess(ctx, core.WrapActivation(core.ActivationControllerReadiness, err))
	}
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if _, _, err := controller.Proxies(readyCtx); err == nil {
			break
		}
		select {
		case <-readyCtx.Done():
			return s.stopStartedProcess(ctx, core.WrapActivation(core.ActivationControllerReadiness, readyCtx.Err()))
		case <-time.After(50 * time.Millisecond):
		}
	}
	s.controller = controller
	s.mixedPort.Store(int32(port))
	s.running.Store(true)
	if err := s.refreshGroups(ctx); err != nil {
		s.running.Store(false)
		s.controller = nil
		return s.stopStartedProcess(ctx, core.WrapActivation(core.ActivationControllerReadiness, err))
	}
	s.exited = s.process.Exited()
	if s.tunCapability(capability) {
		s.tunActive = s.confirmTun(ctx)
		if s.tunActive {
			s.tunFailure = ""
		}
	}
	return nil
}

// proxyAddress is the running Mihomo's mixed listener; it is read off the
// service loop by download workers, hence the atomic port.
func (s *runtimeService) proxyAddress() string {
	return fmt.Sprintf("127.0.0.1:%d", s.mixedPort.Load())
}

// applySystemProxy points the desktop proxy at the running mixed port.
func (s *runtimeService) applySystemProxy(ctx context.Context) error {
	return s.proxy.Apply(ctx, s.proxyAddress())
}

func (s *runtimeService) confirmTun(ctx context.Context) bool {
	confirmCtx, cancel := context.WithTimeout(ctx, tunConfirmTimeout)
	defer cancel()
	for {
		if enabled, err := s.controller.TunEnabled(confirmCtx); err == nil && enabled {
			return true
		}
		select {
		case <-confirmCtx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *runtimeService) stopStartedProcess(ctx context.Context, cause error) error {
	cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if err := s.process.Stop(cleanup); err != nil {
		return core.WrapActivation(core.ActivationRollback, errors.Join(cause, err))
	}
	return cause
}

func (s *runtimeService) stopMonitorAndProcess(ctx context.Context) error {
	if s.stopMonitor != nil {
		s.stopMonitor()
		<-s.monitorDone
		s.stopMonitor = nil
		s.monitor = nil
	}
	s.running.Store(false)
	s.tunActive = false
	s.exited = nil
	s.controller = nil
	if err := s.process.Stop(ctx); err != nil {
		return err
	}
	s.groups = make(map[string]mihomo.Group)
	s.proxies = make(map[string]string)
	s.snapshot.Groups, s.snapshot.Proxies = nil, nil
	s.publish()
	return nil
}

func (s *runtimeService) stop(ctx context.Context) error {
	if err := s.proxy.Restore(ctx); err != nil {
		return err
	}
	s.proxyActive = false
	return s.stopMonitorAndProcess(ctx)
}

func (s *runtimeService) configPath() string {
	if s.generatedPath != "" {
		return s.generatedPath
	}
	return filepath.Join(s.stateDir, "generated.yaml")
}

func (s *runtimeService) profileForRuntime() ([]byte, error) {
	if s.localProfile != nil {
		return s.localProfile, nil
	}
	_, profile, err := s.subs.ActiveProfile()
	return profile, err
}

func (s *runtimeService) start(ctx context.Context) error {
	profile, err := s.profileForRuntime()
	if err != nil {
		return err
	}
	capability, privilege, err := s.runtimeCapability(ctx, !s.tunHold)
	if err != nil {
		return core.WrapActivation(core.ActivationBinary, err)
	}
	s.notePrivilege(privilege)
	return s.startWith(ctx, profile, capability)
}

func (s *runtimeService) startWith(ctx context.Context, profile []byte, capability mihomo.Capability) error {
	intent := s.store.Snapshot()
	if home, paths, err := s.registry.PathsWithHome(intent); err == nil {
		candidate, err := s.renderWithHome(ctx, profile, intent, home, paths, capability)
		if err != nil {
			return err
		}
		report, err := overrideSnapshots(profile, candidate)
		if err != nil {
			return err
		}
		if err := s.applyCandidate(ctx, candidate, capability, home); err != nil {
			return err
		}
		s.commitOverrideReport(report)
		return nil
	}
	plan, err := s.registry.Stage(ctx, intent, download.Auto)
	if err != nil {
		return activationResourceError(err)
	}
	if err := plan.Validate(func(home string, paths map[string]string) error {
		_, validationErr := s.validatedCandidate(ctx, profile, intent, home, paths, capability)
		return validationErr
	}); err != nil {
		_ = plan.Abort()
		return activationResourceError(err)
	}
	if err := s.applyResourcePlan(ctx, plan, profile, capability, false); err != nil {
		return activationResourceError(err)
	}
	return nil
}

func monitorPolicy(settings config.Monitor) monitor.Policy {
	policy := monitor.DefaultPolicy()
	policy.TestURL = settings.TestURL
	policy.SwitchPolicy = settings.SwitchPolicy
	policy.StreamURL = settings.StreamURL
	policy.StreamInterval = settings.StreamInterval
	policy.Interval = settings.Interval
	policy.Timeout = settings.Timeout
	policy.Concurrency = settings.Concurrency
	policy.Threshold = settings.Threshold
	policy.LatencyAlertThreshold = settings.AlertThreshold
	policy.ConsecutiveBadSamples = settings.ConsecutiveBadSamples
	policy.WindowSize = settings.WindowSize
	policy.MinCandidateSamples = settings.MinCandidateSamples
	policy.MaxFailures = settings.MaxFailures
	policy.MinImprovement = settings.MinImprovement
	policy.Cooldown = settings.Cooldown
	policy.Jitter = settings.Jitter
	return policy
}

func (s *runtimeService) refreshGroups(ctx context.Context) error {
	proxies, groups, err := s.controller.Proxies(ctx)
	if err != nil {
		return err
	}
	s.groups = make(map[string]mihomo.Group, len(groups))
	s.proxies = make(map[string]string, len(proxies))
	// A rebuild must not discard probes already taken for these nodes, or every
	// automatic switch blanks the latency column until the next interval.
	carried := make(map[string]core.ProxySnapshot, len(s.snapshot.Proxies))
	for _, proxy := range s.snapshot.Proxies {
		carried[proxy.GroupID+"\x00"+proxy.ID] = proxy
	}
	s.snapshot.Groups = nil
	s.snapshot.Proxies = nil
	monitorGroups := make([]monitor.GroupState, 0, len(groups))
	settings := s.store.Snapshot().Monitor
	mihomoMillis := make(map[string]int64, len(proxies))
	for _, proxy := range proxies {
		s.proxies[opaqueID(proxy.Name)] = proxy.Name
		if proxy.DelayMillis > 0 {
			mihomoMillis[proxy.Name] = proxy.DelayMillis
		}
	}
	var streamMembers []string
	for groupIndex, group := range groups {
		if group.Name == mihomo.StreamProbeName {
			streamMembers = group.Proxies
			continue
		}
		if group.Type != "Selector" && group.Type != "select" && group.Type != "url-test" && group.Type != "URLTest" {
			continue
		}
		// Mihomo's GLOBAL lists every group, the probe group included.
		group.Proxies = slices.DeleteFunc(group.Proxies, func(name string) bool { return name == mihomo.StreamProbeName })
		id := opaqueID(group.Name)
		managedSelector := selectorType(group.Type)
		if s.automation[id] && !managedSelector {
			return fmt.Errorf("clashpulse: automation group %q is not a managed select group", id)
		}
		s.groups[id] = group
		measured := probeEnabled(group.Type, settings.Enabled)
		automatic := automationEnabled(group.Type, settings.Enabled, s.automation[id])
		view := core.GroupSnapshot{ID: id, Label: proxyDisplayLabel(group.Name, "Group", groupIndex+1), Type: group.Type, Selected: opaqueID(group.Selected), AutomationEnabled: automatic, ManualOverride: s.manualOverride[id]}
		labels := make(map[string]int, len(group.Proxies))
		for proxyIndex, name := range group.Proxies {
			proxyID := opaqueID(name)
			s.proxies[proxyID] = name
			view.Proxies = append(view.Proxies, proxyID)
			label := uniqueLabel(proxyDisplayLabel(name, "Proxy", proxyIndex+1), labels)
			view := core.ProxySnapshot{ID: proxyID, GroupID: id, Label: label, MihomoMillis: mihomoMillis[name]}
			if prior, ok := carried[id+"\x00"+proxyID]; ok {
				view.LatencyMillis, view.FinishedAt, view.Outcome = prior.LatencyMillis, prior.FinishedAt, prior.Outcome
				view.MedianMillis, view.JitterMillis, view.Samples, view.Failures = prior.MedianMillis, prior.JitterMillis, prior.Samples, prior.Failures
				view.StreamSamples, view.StreamCompleted, view.StreamKBps = prior.StreamSamples, prior.StreamCompleted, prior.StreamKBps
			}
			s.snapshot.Proxies = append(s.snapshot.Proxies, view)
		}
		s.snapshot.Groups = append(s.snapshot.Groups, view)
		// A selection of the hidden probe group cannot be monitored.
		if (managedSelector || group.Selected != "") && group.Selected != mihomo.StreamProbeName {
			monitorGroups = append(monitorGroups, monitor.GroupState{Group: group.Name, Selected: group.Selected, Proxies: append([]string(nil), group.Proxies...), ProbeEnabled: measured, AutomationEnabled: automatic, ManualOverride: s.manualOverride[id], LastSwitchAt: s.lastSwitch[id]})
		}
	}
	for id, enabled := range s.automation {
		if enabled {
			if _, ok := s.groups[id]; !ok {
				return fmt.Errorf("clashpulse: automation group %q is unavailable in the active profile", id)
			}
		}
	}
	sort.Slice(s.snapshot.Groups, func(i, j int) bool { return s.snapshot.Groups[i].ID < s.snapshot.Groups[j].ID })
	sort.Slice(s.snapshot.Proxies, func(i, j int) bool {
		if s.snapshot.Proxies[i].GroupID == s.snapshot.Proxies[j].GroupID {
			return s.snapshot.Proxies[i].ID < s.snapshot.Proxies[j].ID
		}
		return s.snapshot.Proxies[i].GroupID < s.snapshot.Proxies[j].GroupID
	})
	if s.monitor == nil {
		policy := monitorPolicy(settings)
		var streamer monitor.StreamProber
		if policy.SwitchPolicy == monitor.SwitchSteadyStream {
			streamer = streamProber{controller: s.controller, listener: s.streamProbe}
		}
		s.monitor, err = monitor.NewScheduler(policy, s.controller, streamer, s.offerBatch)
		if err != nil {
			return err
		}
		monitorCtx, cancel := context.WithCancel(s.processCtx)
		s.stopMonitor, s.monitorDone = cancel, make(chan error, 1)
		scheduler, controller := s.monitor, s.controller
		go func() {
			var watcher sync.WaitGroup
			watcher.Go(func() { watchDialFailures(monitorCtx, controller, scheduler.ProbedGroups, scheduler.ReportDialFailure) })
			err := scheduler.Run(monitorCtx)
			watcher.Wait()
			s.monitorDone <- err
		}()
	}
	// Members first: a roster change starts a stream round that needs them.
	s.monitor.SetStreamMembers(streamMembers)
	s.lastStreamMembers = streamMembers
	if err := s.monitor.SetGroups(monitorGroups); err != nil {
		return err
	}
	s.publish()
	return nil
}

// offerBatch keeps the latest pending batch of each kind for the service loop.
func (s *runtimeService) offerBatch(batch monitor.Batch) {
	if len(batch.Streams) > 0 {
		offerLatest(s.streamBatches, batch)
		return
	}
	offerLatest(s.batches, batch)
}

// watchDialFailures follows Mihomo's log for the monitor's lifetime. A dropped
// stream is not a service failure; it reconnects at the early-probe spacing.
func watchDialFailures(ctx context.Context, controller *mihomo.Controller, groups func() []string, report func(group string)) {
	for {
		_ = controller.WatchDialFailures(ctx, groups, report)
		select {
		case <-ctx.Done():
			return
		case <-time.After(monitor.DialFailureProbeSpacing):
		}
	}
}

// probeEnabled measures a managed select group whenever the monitor is enabled.
// Measurement is read-only, so it is not behind the per-group opt-in that grants
// this application authority to move traffic. Mihomo's own url-test groups are
// excluded: Mihomo keeps its native policy for them.
func probeEnabled(groupType string, monitoring bool) bool {
	return monitoring && selectorType(groupType)
}

func automationEnabled(groupType string, monitoring, optedIn bool) bool {
	return probeEnabled(groupType, monitoring) && optedIn
}

// selectorType reports a Mihomo select group, the only kind this application
// may move traffic in.
func selectorType(groupType string) bool {
	return groupType == "Selector" || groupType == "select"
}

// Published node names advertise their provider's domain, and a displayed one
// spreads with every screenshot or report, so a host in a name is masked.
var nodeNameHost = regexp.MustCompile(`[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)

// A node name is shown as the profile wrote it, including non-ASCII names and
// URLs. Userinfo ("@" marks a user:password@host URL, where masking cannot tell
// which part is the secret) and a non-printable rune are the only reasons to
// fall back to the positional label.
func proxyDisplayLabel(name, fallback string, position int) string {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 || strings.Contains(name, "@") ||
		strings.IndexFunc(name, func(r rune) bool { return !unicode.IsPrint(r) }) != -1 {
		return fmt.Sprintf("%s %d", fallback, position)
	}
	return nodeNameHost.ReplaceAllString(name, "[redacted]")
}

func uniqueLabel(label string, seen map[string]int) string {
	seen[label]++
	if count := seen[label]; count > 1 {
		return fmt.Sprintf("%s (%d)", label, count)
	}
	return label
}
