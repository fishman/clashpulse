package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/dns"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
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
	plan, err := s.registry.Stage(ctx, intent, download.Direct)
	if err != nil {
		return nil, err
	}
	defer plan.Abort()
	var candidate []byte
	if err := plan.Validate(func(home string, paths map[string]string) error {
		candidate, err = s.validatedCandidate(ctx, profile, intent, home, paths, capability)
		return err
	}); err != nil {
		return nil, err
	}
	if _, err := plan.Commit(); err != nil {
		return nil, err
	}
	s.resourceDirty.Store(true)
	return candidate, nil
}

func (s *runtimeService) renderWithHome(ctx context.Context, profile []byte, intent config.Snapshot, home string, paths map[string]string, capability mihomo.Capability) ([]byte, error) {
	if err := dns.Validate(ctx, intent, paths); err != nil {
		return nil, err
	}
	return mihomo.Render(profile, intent, mihomo.ManagedPaths(paths), mihomo.ControllerSettings{
		Address: s.controllerAddress, Secret: s.secret, HomeDir: home, ProxyPort: s.proxyPort,
	}, capability)
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
		return err
	}
	file, err := os.CreateTemp(s.stateDir, ".candidate-*.yaml")
	if err != nil {
		return fmt.Errorf("clashpulse: stage generated configuration: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(candidate); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return mihomo.ValidateInHome(ctx, capability, file.Name(), home)
}

type runtimeBackup struct {
	generated   []byte
	cap         mihomo.Capability
	home        string
	running     bool
	proxyActive bool
	selected    map[string]string
}

func (s *runtimeService) applyGenerated(ctx context.Context, profile []byte) error {
	capability, err := s.selectedCapability(ctx)
	if err != nil {
		return err
	}
	intent := s.store.Snapshot()
	home, paths, err := s.registry.PathsWithHome(intent)
	if err != nil {
		return err
	}
	candidate, err := s.renderWithHome(ctx, profile, intent, home, paths, capability)
	if err != nil {
		return err
	}
	s.activationBackup = &runtimeBackup{generated: bytes.Clone(s.generated), cap: s.cap, home: s.resourceHome, running: s.controller != nil, proxyActive: s.proxyActive, selected: selectedGroups(s.groups)}
	if err := s.applyCandidate(ctx, candidate, capability, home); err != nil {
		s.activationBackup = nil
		return err
	}
	return nil
}

func (s *runtimeService) restoreActivation(ctx context.Context, previousProfile []byte) error {
	backup := s.activationBackup
	s.activationBackup = nil
	if backup == nil {
		return fmt.Errorf("clashpulse: no prior runtime was captured")
	}
	if !backup.running {
		if err := s.stop(ctx); err != nil {
			return err
		}
		if len(previousProfile) == 0 {
			if err := os.Remove(filepath.Join(s.stateDir, "generated.yaml")); err != nil && !os.IsNotExist(err) {
				return err
			}
			s.generated, s.cap, s.resourceHome = nil, mihomo.Capability{}, ""
		} else {
			if err := config.Write(filepath.Join(s.stateDir, "generated.yaml"), backup.generated); err != nil {
				return err
			}
			s.generated, s.cap, s.resourceHome = backup.generated, backup.cap, backup.home
		}
		return nil
	}
	if err := s.applyCandidate(ctx, backup.generated, backup.cap, backup.home); err != nil {
		return err
	}
	if err := s.restoreSelections(ctx, backup.selected); err != nil {
		return err
	}
	if backup.proxyActive && !s.proxyActive {
		if err := s.proxy.Apply(ctx, fmt.Sprintf("127.0.0.1:%d", s.proxyPort)); err != nil {
			return err
		}
		s.proxyActive = true
	} else if !backup.proxyActive && s.proxyActive {
		if err := s.proxy.Restore(ctx); err != nil {
			return err
		}
		s.proxyActive = false
	}
	return nil
}

func (s *runtimeService) applyCandidate(ctx context.Context, candidate []byte, capability mihomo.Capability, home string) error {
	if err := s.validateWithHome(ctx, candidate, capability, home); err != nil {
		return err
	}
	if !s.forceRestart && bytes.Equal(candidate, s.generated) && s.controller != nil && capability == s.cap && home == s.resourceHome {
		s.snapshot.Binary.LastCompatibilityFailure = ""
		return nil
	}
	oldConfig, oldCap, oldHome := bytes.Clone(s.generated), s.cap, s.resourceHome
	wasRunning := s.controller != nil
	wasProxy := s.proxyActive
	oldSelected := selectedGroups(s.groups)
	if wasRunning {
		if err := s.proxy.Restore(ctx); err != nil {
			return err
		}
		s.proxyActive = false
		if err := s.stopMonitorAndProcess(ctx); err != nil {
			return err
		}
	}
	activePath := filepath.Join(s.stateDir, "generated.yaml")
	if err := config.Write(activePath, candidate); err != nil {
		if wasRunning {
			s.restorePrevious(ctx, oldConfig, oldCap, oldHome, wasProxy, oldSelected)
		}
		return err
	}
	if err := s.startProcess(ctx, capability, home, activePath); err != nil {
		if wasRunning {
			s.restorePrevious(ctx, oldConfig, oldCap, oldHome, wasProxy, oldSelected)
		} else if len(oldConfig) > 0 {
			_ = config.Write(activePath, oldConfig)
		}
		return err
	}
	if wasRunning {
		if err := s.restoreSelections(ctx, oldSelected); err != nil {
			_ = s.stopMonitorAndProcess(ctx)
			s.restorePrevious(ctx, oldConfig, oldCap, oldHome, wasProxy, oldSelected)
			return err
		}
	}
	s.generated, s.cap, s.resourceHome = bytes.Clone(candidate), capability, home
	if s.store.Snapshot().App.SystemProxy.Enabled {
		if err := s.proxy.Apply(ctx, fmt.Sprintf("127.0.0.1:%d", s.proxyPort)); err != nil {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			restoreErr := s.proxy.Restore(cleanup)
			done()
			if restoreErr != nil {
				s.proxyActive = true
				s.publish()
				return errors.Join(err, fmt.Errorf("clashpulse: system proxy rollback failed: %w", restoreErr))
			}
			_ = s.stopMonitorAndProcess(ctx)
			if wasRunning {
				s.restorePrevious(ctx, oldConfig, oldCap, oldHome, wasProxy, oldSelected)
			} else if len(oldConfig) > 0 {
				_ = config.Write(activePath, oldConfig)
			}
			return err
		}
		s.proxyActive = true
	}
	s.snapshot.Binary.LastCompatibilityFailure = ""
	s.publish()
	return nil
}

func (s *runtimeService) restorePrevious(ctx context.Context, candidate []byte, capability mihomo.Capability, home string, enableProxy bool, selected map[string]string) {
	if len(candidate) == 0 || capability.Path == "" {
		return
	}
	path := filepath.Join(s.stateDir, "generated.yaml")
	if err := config.Write(path, candidate); err != nil {
		s.reportError("rollback", err)
		return
	}
	if err := s.startProcess(ctx, capability, home, path); err != nil {
		s.reportError("rollback", err)
		return
	}
	s.generated, s.cap, s.resourceHome = bytes.Clone(candidate), capability, home
	if err := s.restoreSelections(ctx, selected); err != nil {
		s.reportError("rollback", err)
	}
	if enableProxy {
		if err := s.proxy.Apply(ctx, fmt.Sprintf("127.0.0.1:%d", s.proxyPort)); err != nil {
			s.reportError("rollback", err)
		} else {
			s.proxyActive = true
		}
	}
}

func selectedGroups(groups map[string]mihomo.Group) map[string]string {
	selected := make(map[string]string)
	for _, group := range groups {
		if group.Type == "Selector" || group.Type == "select" {
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
		if !ok || group.Type != "Selector" && group.Type != "select" {
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
	release, err := s.registry.AcquireGeneration(home)
	if err != nil {
		return err
	}
	if err := s.process.Start(s.processCtx, mihomo.StartPlan{Capability: capability, ConfigPath: path, Args: []string{"-d", home}}); err != nil {
		release()
		return err
	}
	s.releaseResource = release
	controller, err := mihomo.NewController("http://"+s.controllerAddress, s.secret, &http.Client{Timeout: time.Second})
	if err != nil {
		return s.stopStartedProcess(ctx, err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if _, _, err := controller.Proxies(readyCtx); err == nil {
			break
		}
		select {
		case <-readyCtx.Done():
			return s.stopStartedProcess(ctx, fmt.Errorf("clashpulse: Mihomo controller did not become ready: %w", readyCtx.Err()))
		case <-time.After(50 * time.Millisecond):
		}
	}
	s.controller = controller
	s.running.Store(true)
	if err := s.refreshGroups(ctx); err != nil {
		s.running.Store(false)
		s.controller = nil
		return s.stopStartedProcess(ctx, err)
	}
	s.exited = s.process.Exited()
	return nil
}

func (s *runtimeService) stopStartedProcess(ctx context.Context, cause error) error {
	cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if err := s.process.Stop(cleanup); err != nil {
		return errors.Join(cause, fmt.Errorf("clashpulse: failed to stop Mihomo after startup error: %w", err))
	}
	if s.releaseResource != nil {
		s.releaseResource()
		s.releaseResource = nil
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
	s.exited = nil
	s.controller = nil
	if err := s.process.Stop(ctx); err != nil {
		return err
	}
	if s.releaseResource != nil {
		s.releaseResource()
		s.releaseResource = nil
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

func (s *runtimeService) start(ctx context.Context) error {
	_, profile, err := s.subs.ActiveProfile()
	if err != nil {
		return err
	}
	intent := s.store.Snapshot()
	capability, err := s.selectedCapability(ctx)
	if err != nil {
		return err
	}
	if home, paths, err := s.registry.PathsWithHome(intent); err == nil {
		candidate, err := s.renderWithHome(ctx, profile, intent, home, paths, capability)
		if err != nil {
			return err
		}
		return s.applyCandidate(ctx, candidate, capability, home)
	}
	plan, err := s.registry.Stage(ctx, intent, download.Direct)
	if err != nil {
		return err
	}
	defer plan.Abort()
	var candidate []byte
	if err := plan.Validate(func(home string, paths map[string]string) error {
		candidate, err = s.validatedCandidate(ctx, profile, intent, home, paths, capability)
		return err
	}); err != nil {
		return err
	}
	if _, err := plan.Commit(); err != nil {
		return err
	}
	s.resourceDirty.Store(true)
	if err := s.applyCandidate(ctx, candidate, capability, plan.Home()); err != nil {
		_ = plan.Rollback()
		s.resourceDirty.Store(true)
		return err
	}
	return nil
}

func monitorPolicy(settings config.Monitor) monitor.Policy {
	policy := monitor.DefaultPolicy()
	policy.TestURL = settings.TestURL
	policy.Interval = settings.Interval
	policy.Timeout = settings.Timeout
	policy.Concurrency = settings.Concurrency
	policy.Threshold = settings.Threshold
	policy.LatencyAlertThreshold = settings.AlertThreshold
	policy.ConsecutiveBadSamples = settings.ConsecutiveBadSamples
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
	s.snapshot.Groups = nil
	s.snapshot.Proxies = nil
	monitorGroups := make([]monitor.GroupState, 0, len(groups))
	settings := s.store.Snapshot().Monitor
	for _, proxy := range proxies {
		s.proxies[opaqueID(proxy.Name)] = proxy.Name
	}
	for groupIndex, group := range groups {
		if group.Type != "Selector" && group.Type != "select" && group.Type != "url-test" && group.Type != "URLTest" {
			continue
		}
		id := opaqueID(group.Name)
		managedSelector := group.Type == "Selector" || group.Type == "select"
		if s.automation[id] && !managedSelector {
			return fmt.Errorf("clashpulse: automation group %q is not a managed select group", id)
		}
		s.groups[id] = group
		automatic := automaticProbeEnabled(group.Type, settings.Enabled, s.automation[id])
		view := core.GroupSnapshot{ID: id, Label: proxyDisplayLabel(group.Name, "Group", groupIndex+1), Type: group.Type, Selected: opaqueID(group.Selected), AutomationEnabled: automatic, ManualOverride: s.manualOverride[id]}
		for proxyIndex, name := range group.Proxies {
			proxyID := opaqueID(name)
			s.proxies[proxyID] = name
			view.Proxies = append(view.Proxies, proxyID)
			s.snapshot.Proxies = append(s.snapshot.Proxies, core.ProxySnapshot{ID: proxyID, GroupID: id, Label: proxyDisplayLabel(name, "Proxy", proxyIndex+1)})
		}
		s.snapshot.Groups = append(s.snapshot.Groups, view)
		if managedSelector || group.Selected != "" {
			monitorGroups = append(monitorGroups, monitor.GroupState{Group: group.Name, Selected: group.Selected, Proxies: append([]string(nil), group.Proxies...), ProbeEnabled: automatic, AutomationEnabled: automatic, ManualOverride: s.manualOverride[id], LastSwitchAt: s.lastSwitch[id]})
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
		s.monitor, err = monitor.NewScheduler(policy, s.controller, func(batch monitor.Batch) {
			offerLatest(s.batches, batch)
		})
		if err != nil {
			return err
		}
		monitorCtx, cancel := context.WithCancel(s.processCtx)
		s.stopMonitor, s.monitorDone = cancel, make(chan error, 1)
		go func() { s.monitorDone <- s.monitor.Run(monitorCtx) }()
	}
	if err := s.monitor.SetGroups(monitorGroups); err != nil {
		return err
	}
	s.publish()
	return nil
}

func automaticProbeEnabled(groupType string, monitoring, optedIn bool) bool {
	return monitoring && optedIn && (groupType == "Selector" || groupType == "select")
}

func proxyDisplayLabel(name, fallback string, position int) string {
	name = strings.TrimSpace(name)
	if name != "" && utf8.RuneCountInString(name) <= 80 && strings.IndexFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" -_.()[]", r)
	}) == -1 {
		return name
	}
	return fmt.Sprintf("%s %d", fallback, position)
}
