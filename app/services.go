package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
	"github.com/fishman/clashpulse/services"
)

// serviceStatus is the latest check of one service on the node it checked;
// it is shown only while that node is still the group's selection.
type serviceStatus struct {
	proxy, outcome, region string
	lastCheck, nextCheck   int64
	lastSwitch             string
}

// serviceChecker runs checks through a probe slot pinned to the checked node.
type serviceChecker struct{ pool *mihomo.ProbePool }

func (c serviceChecker) CheckServices(ctx context.Context, proxy string, ids []string) []services.Result {
	defs := make([]services.Definition, 0, len(ids))
	for _, id := range ids {
		if def, ok := services.Lookup(id); ok {
			defs = append(defs, def)
		}
	}
	lease, err := c.pool.Acquire(ctx, proxy)
	if err != nil {
		out := make([]services.Result, len(defs))
		for i, def := range defs {
			out[i] = services.Result{Service: def.ID, Outcome: services.Failed}
		}
		return out
	}
	defer lease.Release()
	return services.Check(ctx, lease.Transport(), defs)
}

func configuredService(snapshot config.Snapshot, id string) (config.Service, bool) {
	index := slices.IndexFunc(snapshot.Services, func(service config.Service) bool { return service.ID == id })
	if index < 0 {
		return config.Service{}, false
	}
	return snapshot.Services[index], true
}

// latencyManaged reports whether latency automation may manage group; a
// service group follows its service checks instead.
func latencyManaged(group mihomo.Group) bool {
	_, service := mihomo.ServiceGroupFor(group.Name)
	return selectorType(group.Type) && !service
}

// serviceGroup returns the running group generated for a service.
func (s *runtimeService) serviceGroup(id string) (mihomo.Group, bool) {
	def, ok := services.Lookup(id)
	if !ok {
		return mihomo.Group{}, false
	}
	group, ok := s.groups[opaqueID(mihomo.ServiceGroupName(def.Name))]
	return group, ok
}

// serviceRenderKey is the part of service intent the generated config reads.
func serviceRenderKey(snapshot config.Snapshot) []string {
	var key []string
	for _, service := range snapshot.Services {
		if service.Enabled {
			key = append(key, service.ID+"\x00"+service.DomainsResource)
		}
	}
	slices.Sort(key)
	return key
}

// updateServicePlan hands the scheduler the current targets; an unchanged plan
// does not wake it.
func (s *runtimeService) updateServicePlan() {
	if s.serviceScheduler == nil {
		return
	}
	var plan monitor.ServicePlan
	if s.probePool != nil {
		settings := s.store.Snapshot()
		for _, service := range settings.Services {
			group, ok := s.serviceGroup(service.ID)
			if !service.Enabled || !ok {
				continue
			}
			plan.Targets = append(plan.Targets, monitor.ServiceTarget{
				ID: service.ID, Group: group.Name, Selected: group.Selected, Members: slices.Clone(group.Proxies),
				Interval: service.CheckInterval, Failures: service.Failures, Automatic: service.Automatic, Regions: slices.Clone(service.Regions),
			})
		}
		if len(plan.Targets) > 0 {
			plan.Latency, plan.Unhealthy = s.nodeHealth()
			plan.Cooldown, plan.Slots = settings.Monitor.Cooldown, s.probePool.Size()
		}
	}
	if reflect.DeepEqual(plan, s.servicePlan) {
		return
	}
	s.servicePlan = plan
	s.serviceScheduler.SetPlan(plan)
}

// nodeHealth reduces the latency rows to one value per node: its lowest median
// across groups, and whether its latest probe failed.
func (s *runtimeService) nodeHealth() (map[string]time.Duration, map[string]bool) {
	latency := make(map[string]time.Duration)
	unhealthy := make(map[string]bool)
	latest := make(map[string]int64)
	for _, row := range s.snapshot.Proxies {
		name, ok := s.proxies[row.ID]
		if !ok {
			continue
		}
		if row.MedianMillis > 0 {
			median := time.Duration(row.MedianMillis) * time.Millisecond
			if current, ok := latency[name]; !ok || median < current {
				latency[name] = median
			}
		}
		if row.Outcome != "" && row.FinishedAt >= latest[name] {
			latest[name] = row.FinishedAt
			unhealthy[name] = row.Outcome != "success"
		}
	}
	for name, bad := range unhealthy {
		if !bad {
			delete(unhealthy, name)
		}
	}
	return latency, unhealthy
}

// observeServiceGroup judges a service group's observed selection. A change
// not made by ClashPulse, since the last start or reload, from a node that is
// still a member is a manual pick and ends automatic switching for it.
func (s *runtimeService) observeServiceGroup(id string, group mihomo.Group) {
	previous, seen := s.serviceObserved[group.Name]
	s.serviceObserved[group.Name] = group.Selected
	if !seen || previous == group.Selected || !slices.Contains(group.Proxies, previous) {
		return
	}
	if err := s.persistServiceManual(id); err != nil {
		s.reportErrorScoped("service_switch", id, err)
	}
}

// persistServiceManual turns automatic off. The generated configuration does
// not read it, so the store is reloaded without an apply; the applied
// settings take the new service intent so a later re-enable is a change.
func (s *runtimeService) persistServiceManual(id string) error {
	current := s.store.Snapshot()
	service, ok := configuredService(current, id)
	if !ok || !service.Automatic {
		return nil
	}
	service.Automatic = false
	if err := config.PutService(filepath.Join(s.configDir, "config.toml"), current, service); err != nil {
		return err
	}
	if _, err := s.reloadConfig(); err != nil {
		return err
	}
	s.lastAppliedSettings.Services = s.store.Snapshot().Services
	s.updateServicePlan()
	return nil
}

func (s *runtimeService) setService(ctx context.Context, edit *ipc.ServiceEdit) error {
	service := config.Service{
		ID: edit.ID, Enabled: edit.Enabled, Automatic: edit.Automatic, Regions: slices.Clone(edit.Regions),
		CheckInterval: time.Duration(edit.IntervalSeconds) * time.Second, Failures: edit.Failures, DomainsResource: edit.DomainsResource,
	}
	return s.editUserFile(ctx, "config.toml", func(path string, current config.Snapshot) error {
		return config.PutService(path, current, service)
	})
}

func (s *runtimeService) checkService(id string) error {
	if service, ok := configuredService(s.store.Snapshot(), id); !ok || !service.Enabled {
		return errors.New("service check is not enabled")
	}
	if s.serviceScheduler == nil || s.probePool == nil {
		return errors.New("service checks are unavailable")
	}
	s.serviceScheduler.Request(id)
	return nil
}

// selectServiceGroup is a user's pick in a service group: it ends automatic
// switching for that service.
func (s *runtimeService) selectServiceGroup(ctx context.Context, id string, group mihomo.Group, proxy string) error {
	if err := s.controller.Select(ctx, group.Name, proxy); err != nil {
		return err
	}
	s.serviceObserved[group.Name] = proxy
	if err := s.persistServiceManual(id); err != nil {
		return err
	}
	return s.refreshGroups(ctx)
}

// applyServiceBatch runs on the service loop. A switch is re-checked against
// current intent and selection, since either may have changed since the check.
func (s *runtimeService) applyServiceBatch(ctx context.Context, batch monitor.ServiceBatch) {
	for _, check := range batch.Checks {
		group, ok := s.serviceGroup(check.Service)
		if !ok || check.Proxy != group.Selected {
			continue
		}
		status := s.serviceStatus[check.Service]
		status.proxy, status.outcome, status.region, status.lastCheck = check.Proxy, string(check.Result.Outcome), check.Result.Region, check.At.Unix()
		if check.Result.Outcome == services.Usable && status.lastSwitch == "exhausted" {
			status.lastSwitch = ""
		}
		s.serviceStatus[check.Service] = status
	}
	for id, next := range batch.Next {
		status := s.serviceStatus[id]
		status.nextCheck = unixSeconds(next)
		s.serviceStatus[id] = status
	}
	settings := s.store.Snapshot()
	switched := false
	for _, change := range batch.Switches {
		service, configured := configuredService(settings, change.Service)
		group, ok := s.serviceGroup(change.Service)
		if s.controller == nil || !configured || !service.Enabled || !service.Automatic || !ok || group.Name != change.Group || group.Selected != change.Old || !slices.Contains(group.Proxies, change.New) {
			continue
		}
		if err := s.controller.Select(ctx, group.Name, change.New); err != nil {
			s.reportErrorScoped("service_switch", change.Service, err)
			continue
		}
		s.serviceObserved[group.Name] = change.New
		status := s.serviceStatus[change.Service]
		status.lastSwitch = change.Reason
		s.serviceStatus[change.Service] = status
		s.appendDiagnostic(diagnosticEvent{Severity: "info", Kind: "service_switch", SourceID: change.Service, Message: "service.switch." + change.Reason, At: time.Now()})
		switched = true
	}
	for _, id := range batch.Exhausted {
		status := s.serviceStatus[id]
		if status.lastSwitch == "exhausted" {
			continue
		}
		status.lastSwitch = "exhausted"
		s.serviceStatus[id] = status
		s.appendDiagnostic(diagnosticEvent{Severity: "warning", Kind: "service_switch", SourceID: id, Message: "service.exhausted", At: time.Now()})
	}
	if switched {
		if err := s.refreshGroups(ctx); err != nil {
			s.reportError("service_switch", err)
		}
	}
	s.publish()
}

// serviceSnapshots lists every built-in service, configured or not, with its
// latest result on the group's current selection.
func (s *runtimeService) serviceSnapshots(settings config.Snapshot, proxies []core.ProxySnapshot) []core.ServiceSnapshot {
	out := make([]core.ServiceSnapshot, 0, len(services.Definitions()))
	for _, def := range services.Definitions() {
		service, ok := configuredService(settings, def.ID)
		if !ok {
			service = config.Service{CheckInterval: config.DefaultServiceInterval, Failures: config.DefaultServiceFailures}
		}
		view := core.ServiceSnapshot{
			ID: def.ID, Name: def.Name, Enabled: service.Enabled, Automatic: service.Automatic, Regions: slices.Clone(service.Regions),
			IntervalSeconds: uint32(service.CheckInterval / time.Second), Failures: service.Failures, DomainsResource: service.DomainsResource,
		}
		status := s.serviceStatus[def.ID]
		view.NextCheck, view.LastSwitch = status.nextCheck, status.lastSwitch
		if group, ok := s.serviceGroup(def.ID); ok && service.Enabled {
			view.GroupID = opaqueID(group.Name)
			selected := opaqueID(group.Selected)
			for _, proxy := range proxies {
				if proxy.GroupID == view.GroupID && proxy.ID == selected {
					view.Selected = proxy.Label
					break
				}
			}
			if status.proxy == group.Selected {
				view.Outcome, view.Region, view.LastCheck = status.outcome, status.region, status.lastCheck
			}
		}
		out = append(out, view)
	}
	return out
}

var errServiceGroupAutomation = fmt.Errorf("service groups use service automatic switching")
