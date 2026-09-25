package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/monitor"
)

func opaqueID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func (s *runtimeService) runIntent(ctx context.Context, cmd ipc.Command) {
	s.jobID++
	jobID := fmt.Sprintf("job-%d", s.jobID)
	s.snapshot.Jobs = append(s.snapshot.Jobs, core.JobSnapshot{ID: jobID, Kind: string(cmd.Kind), State: "running"})
	s.publish()
	err := s.runOperation(ctx, cmd.Kind, func(operationCtx context.Context) error { return s.execute(operationCtx, cmd) })
	for index, job := range s.snapshot.Jobs {
		if job.ID == jobID {
			s.snapshot.Jobs = append(s.snapshot.Jobs[:index], s.snapshot.Jobs[index+1:]...)
			break
		}
	}
	if err != nil {
		s.reportErrorScoped(string(cmd.Kind), serviceCommandSource(cmd), err)
	} else {
		s.resolveIssue(string(cmd.Kind), serviceCommandSource(cmd))
	}
	s.publish()
}

func (s *runtimeService) execute(ctx context.Context, cmd ipc.Command) error {
	switch cmd.Kind {
	case ipc.CommandStart:
		if s.controller != nil {
			return nil
		}
		return s.start(ctx)
	case ipc.CommandStop:
		return s.stop(ctx)
	case ipc.CommandRestart:
		s.forceRestart = true
		defer func() { s.forceRestart = false }()
		return s.start(ctx)
	case ipc.CommandReloadConfiguration:
		next, err := s.reloadConfig()
		if err != nil {
			return err
		}
		change := config.NewStore(s.lastAppliedSettings).Replace(next)
		if len(change.Sections) != 0 {
			return s.applyChange(ctx, change)
		}
		if s.controller != nil {
			return s.start(ctx)
		}
		return nil
	case ipc.CommandUpdateConfiguration:
		return s.patchSettings(ctx, cmd.Config)
	case ipc.CommandRefreshSubscription:
		_, err := s.subs.Refresh(ctx, cmd.SubscriptionID)
		return err
	case ipc.CommandActivateSubscription:
		err := s.subs.Activate(ctx, cmd.SubscriptionID)
		s.activationBackup = nil
		if err != nil {
			return err
		}
		s.subScheduler.WakeResources()
		return nil
	case ipc.CommandPutSubscription:
		return s.putSubscription(ctx, cmd.SubscriptionID, cmd.Subscription)
	case ipc.CommandDeleteSubscription:
		return s.editUserFile(ctx, "subscriptions.toml", func(path string, current config.Snapshot) error {
			return config.DeleteSubscription(path, current, cmd.SubscriptionID)
		})
	case ipc.CommandPutResource:
		return s.putResource(ctx, cmd.ResourceID, cmd.Resource)
	case ipc.CommandPutFilter:
		return s.putFilter(ctx, cmd.FilterID, cmd.Filter)
	case ipc.CommandSetDNSRouting:
		return s.setDNSRouting(ctx, cmd.DNSRouting)
	case ipc.CommandRefreshResource:
		if !configuredResource(s.store.Snapshot(), cmd.ResourceID) {
			return fmt.Errorf("resource is not configured")
		}
		return s.refreshResourceIDs(ctx, []string{cmd.ResourceID})
	case ipc.CommandRefreshFilter:
		resourceID := resourceForFilter(s.store.Snapshot(), cmd.FilterID)
		if resourceID == "" {
			return fmt.Errorf("filter is not configured")
		}
		return s.refreshResourceIDs(ctx, []string{resourceID})
	case ipc.CommandManualProbe:
		if s.monitor == nil {
			return fmt.Errorf("monitor is unavailable")
		}
		group, ok := s.groups[cmd.GroupID]
		if !ok {
			return fmt.Errorf("group is unavailable")
		}
		return s.monitor.Request(group.Name)
	case ipc.CommandSelectGroup:
		group, ok := s.groups[cmd.GroupID]
		proxy, found := s.proxies[cmd.ChoiceID]
		if s.controller == nil || !ok || !found || (group.Type != "Selector" && group.Type != "select") {
			return fmt.Errorf("selection is unavailable")
		}
		eligible := false
		for _, member := range group.Proxies {
			if member == proxy {
				eligible = true
				break
			}
		}
		if !eligible {
			return fmt.Errorf("proxy is not a member of this group")
		}
		if err := s.persistGroupAutomation(cmd.GroupID, false); err != nil {
			return err
		}
		if err := s.controller.Select(ctx, group.Name, proxy); err != nil {
			return err
		}
		s.manualOverride[cmd.GroupID], s.automation[cmd.GroupID] = true, false
		return s.refreshGroups(ctx)
	case ipc.CommandSetAutomation:
		group, ok := s.groups[cmd.GroupID]
		if !ok || (group.Type != "Selector" && group.Type != "select") {
			return fmt.Errorf("group does not support automation")
		}
		if s.automation[cmd.GroupID] == cmd.Automation.Enabled && !s.manualOverride[cmd.GroupID] {
			return nil
		}
		if err := s.persistGroupAutomation(cmd.GroupID, cmd.Automation.Enabled); err != nil {
			return err
		}
		s.automation[cmd.GroupID] = cmd.Automation.Enabled
		if cmd.Automation.Enabled {
			s.manualOverride[cmd.GroupID] = false
		}
		return nil
	default:
		return fmt.Errorf("unsupported command")
	}
}

func (s *runtimeService) persistGroupAutomation(groupID string, enabled bool) error {
	path := filepath.Join(s.configDir, "config.toml")
	if err := config.PatchAutomation(path, s.store.Snapshot(), groupID, enabled); err != nil {
		return err
	}
	_, err := s.reloadConfig()
	return err
}

func (s *runtimeService) reloadConfig() (config.Snapshot, error) {
	next, err := config.Load(s.configDir)
	if err != nil {
		return config.Snapshot{}, err
	}
	s.store.Replace(next)
	return next, nil
}

func configuredResource(snapshot config.Snapshot, id string) bool {
	for _, item := range snapshot.Resources {
		if item.ID == id && item.Enabled {
			return true
		}
	}
	return false
}
func resourceForFilter(snapshot config.Snapshot, id string) string {
	for _, item := range snapshot.Filters {
		if item.ID == id && item.Enabled {
			return item.Resource
		}
	}
	return ""
}

func (s *runtimeService) patchSettings(ctx context.Context, patch *ipc.ConfigPatch) error {
	if patch == nil {
		return fmt.Errorf("empty configuration patch")
	}
	current := s.lastAppliedSettings
	path := filepath.Join(s.configDir, "config.toml")
	previous, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	restore := func() error {
		if readErr == nil {
			return config.Write(path, previous)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	change := config.SettingsPatch{Binary: patch.Binary, SystemProxyEnabled: patch.SystemProxyEnabled, DNSListen: patch.DNSListen, MonitorEnabled: patch.MonitorEnabled, MonitorTestURL: patch.MonitorTestURL}
	if patch.MonitorIntervalSeconds != nil {
		value := time.Duration(*patch.MonitorIntervalSeconds) * time.Second
		change.MonitorInterval = &value
	}
	if patch.MonitorTimeoutMillis != nil {
		value := time.Duration(*patch.MonitorTimeoutMillis) * time.Millisecond
		change.MonitorTimeout = &value
	}
	if patch.MonitorConcurrency != nil {
		value := int(*patch.MonitorConcurrency)
		change.MonitorConcurrency = &value
	}
	if patch.MonitorThresholdMillis != nil {
		value := time.Duration(*patch.MonitorThresholdMillis) * time.Millisecond
		change.MonitorThreshold = &value
	}
	if patch.MonitorConsecutiveBadSamples != nil {
		value := int(*patch.MonitorConsecutiveBadSamples)
		change.MonitorConsecutiveBadSamples = &value
	}
	if patch.MonitorMinImprovementMillis != nil {
		value := time.Duration(*patch.MonitorMinImprovementMillis) * time.Millisecond
		change.MonitorMinImprovement = &value
	}
	if patch.MonitorCooldownSeconds != nil {
		value := time.Duration(*patch.MonitorCooldownSeconds) * time.Second
		change.MonitorCooldown = &value
	}
	if patch.MonitorJitterMillis != nil {
		value := time.Duration(*patch.MonitorJitterMillis) * time.Millisecond
		change.MonitorJitter = &value
	}
	if patch.AlertThresholdMillis != nil {
		value := time.Duration(*patch.AlertThresholdMillis) * time.Millisecond
		change.AlertThreshold = &value
	}
	if err := config.PatchSettings(path, current, change); err != nil {
		return err
	}
	updated, err := s.reloadConfig()
	if err == nil {
		transition := config.NewStore(s.lastAppliedSettings).Replace(updated)
		err = s.applyChange(ctx, transition)
	}
	if err != nil {
		rollbackErr := restore()
		s.store.Replace(current)
		if rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("clashpulse: restore previous user config: %w", rollbackErr))
		}
		return err
	}
	return nil
}

func (s *runtimeService) applyChange(ctx context.Context, _ config.Change) error {
	change := config.NewStore(s.lastAppliedSettings).Replace(s.store.Snapshot())
	if len(change.Sections) == 0 {
		return nil
	}
	restoreBefore := func(cause error) error {
		s.store.Replace(change.Before)
		if err := s.syncSubscriptions(change.Before); err != nil {
			return errors.Join(cause, fmt.Errorf("clashpulse: restore prior subscriptions: %w", err))
		}
		return cause
	}
	needsRender := false
	monitorChanged, proxyChanged := false, false
	for _, section := range change.Sections {
		switch section {
		case "mihomo", "resources", "dns", "filters":
			needsRender = true
		case "monitor":
			monitorChanged = true
		case "app":
			proxyChanged = true
		}
	}
	if monitorChanged {
		if err := monitorPolicy(change.After.Monitor).Validate(); err != nil {
			return restoreBefore(fmt.Errorf("config.toml: monitor.interval: %w", err))
		}
	}
	if monitorChanged && s.controller != nil {
		for _, id := range change.After.Monitor.AutomatedGroups {
			group, ok := s.groups[id]
			if !ok || group.Type != "Selector" && group.Type != "select" {
				return restoreBefore(fmt.Errorf("config.toml: monitor.automated_groups: group %q is not a managed select group", id))
			}
		}
	}
	targetSubscriptions := make(map[string]bool, len(change.After.Subscriptions))
	for _, item := range change.After.Subscriptions {
		targetSubscriptions[item.ID] = true
	}
	for _, entry := range s.subs.List() {
		if entry.Active && !targetSubscriptions[entry.ID] {
			return restoreBefore(fmt.Errorf("clashpulse: active subscription %q cannot be removed before a validated replacement is activated", entry.ID))
		}
	}

	priorAutomation := maps.Clone(s.automation)
	priorManual := maps.Clone(s.manualOverride)
	runtimeBefore := runtimeBackup{
		generated: append([]byte(nil), s.generated...), cap: s.cap, home: s.resourceHome,
		proxyActive: s.proxyActive, selected: selectedGroups(s.groups),
	}
	runtimeAttempted, proxyAttempted := false, false
	revert := func(cause error) error {
		s.automation, s.manualOverride = priorAutomation, priorManual
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		rollbackErrors := []error{restoreBefore(cause)}
		if runtimeAttempted {
			if err := s.applyCandidate(rollbackCtx, runtimeBefore.generated, runtimeBefore.cap, runtimeBefore.home); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("clashpulse: restore prior runtime: %w", err))
			}
			if err := s.restoreSelections(rollbackCtx, runtimeBefore.selected); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("clashpulse: restore prior selections: %w", err))
			}
		}
		if runtimeAttempted || proxyAttempted {
			var err error
			if runtimeBefore.proxyActive {
				err = s.proxy.Apply(rollbackCtx, fmt.Sprintf("127.0.0.1:%d", s.proxyPort))
			} else {
				err = s.proxy.Restore(rollbackCtx)
			}
			if err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("clashpulse: restore prior system proxy: %w", err))
			} else {
				s.proxyActive = runtimeBefore.proxyActive
			}
		}
		if monitorChanged && !runtimeAttempted && s.controller != nil && s.monitor == nil {
			if err := s.refreshGroups(rollbackCtx); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("clashpulse: restore prior monitor: %w", err))
			}
		}
		return errors.Join(rollbackErrors...)
	}
	if monitorChanged {
		next := make(map[string]bool, len(change.After.Monitor.AutomatedGroups))
		for _, id := range change.After.Monitor.AutomatedGroups {
			next[id] = true
			if !s.automation[id] {
				s.manualOverride[id] = false
			}
		}
		s.automation = next
	}
	if s.controller != nil && needsRender {
		runtimeAttempted = true
		if err := s.start(ctx); err != nil {
			return revert(err)
		}
	}
	if s.controller != nil && monitorChanged {
		if s.stopMonitor != nil {
			s.stopMonitor()
			<-s.monitorDone
			s.stopMonitor = nil
			s.monitor = nil
		}
		if err := s.refreshGroups(ctx); err != nil {
			return revert(err)
		}
	}
	if s.controller != nil && proxyChanged && s.proxyActive != change.After.App.SystemProxy.Enabled {
		proxyAttempted = true
		var err error
		if change.After.App.SystemProxy.Enabled {
			err = s.proxy.Apply(ctx, fmt.Sprintf("127.0.0.1:%d", s.proxyPort))
		} else {
			err = s.proxy.Restore(ctx)
		}
		if err != nil {
			return revert(err)
		}
		s.proxyActive = change.After.App.SystemProxy.Enabled
	}
	var deletionErr error
	if err := s.syncSubscriptions(change.After); err != nil {
		if !errors.Is(err, errSubscriptionDeletionCommitted) {
			return revert(err)
		}
		deletionErr = err
	}
	for _, section := range change.Sections {
		if section == "resources" {
			s.resourceDirty.Store(true)
			s.subScheduler.WakeResources()
			break
		}
	}
	s.lastAppliedSettings = change.After
	if deletionErr != nil && s.configErrors != nil {
		offerLatest(s.configErrors, deletionErr)
	}
	s.publish()
	return nil
}

func (s *runtimeService) acceptBatch(ctx context.Context, batch monitor.Batch) {
	for _, sample := range batch.Samples {
		id := opaqueID(sample.Proxy)
		for index := range s.snapshot.Proxies {
			if s.snapshot.Proxies[index].ID != id || s.snapshot.Proxies[index].GroupID != opaqueID(sample.Group) {
				continue
			}
			snapshot := &s.snapshot.Proxies[index]
			snapshot.LatencyMillis = sample.Latency.Milliseconds()
			snapshot.FinishedAt = sample.FinishedAt.Unix()
			switch sample.Outcome {
			case monitor.OutcomeSuccess:
				snapshot.Outcome = "success"
			case monitor.OutcomeTimeout:
				snapshot.Outcome = "timeout"
			default:
				snapshot.Outcome = "error"
			}
			break
		}
	}
	settings := s.store.Snapshot().Monitor
	threshold := settings.AlertThreshold
	high, fast := allMeasuredSlow(s.snapshot.Groups, s.snapshot.Proxies, threshold, time.Now(), settings.Interval)
	if !settings.Enabled {
		s.alertHigh, high, fast = false, false, false
	}
	if fast {
		s.alertHigh = false
	}
	if high && !s.alertHigh {
		s.alertHigh = true
		select {
		case s.notifications <- threshold:
		default:
		}
	}
	for _, decision := range batch.Decisions {
		if !decision.Switch || s.controller == nil || len(decision.Evidence) == 0 {
			continue
		}
		groupID := ""
		for key, group := range s.groups {
			if group.Name == decision.Evidence[0].Group {
				groupID = key
				break
			}
		}
		if groupID == "" || !s.automation[groupID] || s.manualOverride[groupID] {
			continue
		}
		group := s.groups[groupID]
		if group.Selected != decision.Old {
			continue
		}
		if err := s.controller.Select(ctx, group.Name, decision.New); err != nil {
			s.reportError("switch", err)
			continue
		}
		s.lastSwitch[groupID] = time.Now()
		event := core.SwitchSnapshot{GroupID: groupID, OldID: opaqueID(decision.Old), NewID: opaqueID(decision.New), Reason: decision.Reason, At: time.Now().Unix()}
		for _, sample := range decision.Evidence {
			if sample.Proxy != decision.Old && sample.Proxy != decision.New {
				continue
			}
			outcome := "error"
			if sample.Outcome == monitor.OutcomeSuccess {
				outcome = "success"
			} else if sample.Outcome == monitor.OutcomeTimeout {
				outcome = "timeout"
			}
			event.Evidence = append(event.Evidence, core.ProbeSnapshot{ProxyID: opaqueID(sample.Proxy), FinishedAt: sample.FinishedAt.Unix(), LatencyMillis: sample.Latency.Milliseconds(), Outcome: outcome})
		}
		sort.Slice(event.Evidence, func(i, j int) bool { return event.Evidence[i].FinishedAt < event.Evidence[j].FinishedAt })
		s.snapshot.Switches = append(s.snapshot.Switches, event)
		if len(s.snapshot.Switches) > 16 {
			s.snapshot.Switches = append([]core.SwitchSnapshot(nil), s.snapshot.Switches[len(s.snapshot.Switches)-16:]...)
		}
		if err := s.refreshGroups(ctx); err != nil {
			s.reportError("switch", err)
		}
	}
	s.publish()
}

func allMeasuredSlow(groups []core.GroupSnapshot, proxies []core.ProxySnapshot, threshold time.Duration, now time.Time, interval time.Duration) (bool, bool) {
	allHigh, anyFast, observed := true, false, false
	maxAge := interval
	if interval <= 24*time.Hour {
		maxAge = 2*interval + 5*time.Second
	}
	for _, group := range groups {
		if group.Type != "Selector" && group.Type != "select" && group.Type != "URLTest" && group.Type != "url-test" {
			continue
		}
		for _, id := range group.Proxies {
			observed = true
			found := false
			for _, proxy := range proxies {
				if proxy.GroupID != group.ID || proxy.ID != id {
					continue
				}
				found = true
				finished := time.Unix(proxy.FinishedAt, 0)
				if proxy.Outcome != "success" || proxy.LatencyMillis <= 0 || proxy.FinishedAt <= 0 || finished.After(now) || now.Sub(finished) > maxAge {
					allHigh = false
				} else if time.Duration(proxy.LatencyMillis)*time.Millisecond <= threshold {
					allHigh, anyFast = false, true
				}
				break
			}
			if !found {
				allHigh = false
			}
		}
	}
	return observed && allHigh, anyFast
}

func (s *runtimeService) runNotifications(ctx context.Context) {
	defer close(s.notificationDone)
	for {
		select {
		case <-ctx.Done():
			return
		case threshold := <-s.notifications:
			if ctx.Err() != nil {
				return
			}
			if err := notifySlowConnections(threshold); err != nil {
				select {
				case s.notificationErrors <- err:
				default:
				}
			}
		}
	}
}
