package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/resources"
	"github.com/fishman/clashpulse/subscriptions"
)

const serviceWorkQueueSize = intentQueueSize + 1

type serviceWorkKind uint8

const (
	workSubscriptionRefresh serviceWorkKind = iota
	workResourceRefresh
)

type serviceWorkRequest struct {
	id      uint64
	kind    serviceWorkKind
	ctx     context.Context
	done    chan struct{}
	command ipc.Command
	ids     []string
}

type serviceWorkResult struct {
	id       uint64
	err      error
	prepared *preparedResourceRefresh
	done     chan struct{}
}

type serviceWorkRecord struct {
	cancel    context.CancelFunc
	jobID     string
	command   ipc.Command
	scheduled bool
	canceled  bool
}

type preparedResourceRefresh struct {
	plan       *resources.Plan
	profile    []byte
	capability mihomo.Capability
}

func (s *runtimeService) beginServiceIntent(cmd ipc.Command) string {
	s.jobID++
	jobID := fmt.Sprintf("job-%d", s.jobID)
	s.snapshot.Jobs = append(s.snapshot.Jobs, core.JobSnapshot{ID: jobID, Kind: string(cmd.Kind), State: "running"})
	s.publish()
	return jobID
}

func (s *runtimeService) removeServiceIntent(jobID string) {
	for index, job := range s.snapshot.Jobs {
		if job.ID == jobID {
			s.snapshot.Jobs = append(s.snapshot.Jobs[:index], s.snapshot.Jobs[index+1:]...)
			return
		}
	}
}

func (s *runtimeService) finishServiceIntent(jobID string, cmd ipc.Command, err error) {
	s.removeServiceIntent(jobID)
	if err != nil {
		s.reportErrorScoped(string(cmd.Kind), serviceCommandSource(cmd), err)
	} else {
		s.resolveIssue(string(cmd.Kind), serviceCommandSource(cmd))
	}
	s.publish()
}

func serviceCommandSource(cmd ipc.Command) string {
	switch {
	case cmd.SubscriptionID != "":
		return cmd.SubscriptionID
	case cmd.ResourceID != "":
		return cmd.ResourceID
	default:
		return cmd.FilterID
	}
}

func (s *runtimeService) discardServiceIntent(jobID string) {
	s.removeServiceIntent(jobID)
	s.publish()
}

// Preparation can block on download and validation; commit and process mutation stay on the loop.
func (s *runtimeService) prepareResourceRefresh(ctx context.Context, ids []string) (*preparedResourceRefresh, error) {
	defer s.resourceDirty.Store(true)
	intent := s.store.Snapshot()
	profile, err := s.profileForRuntime()
	if s.localProfile == nil && errors.Is(err, subscriptions.ErrNoSnapshot) {
		return s.prepareResourceCache(ctx, intent, ids)
	}
	if err != nil {
		return nil, err
	}
	plan, err := s.registry.StageDue(ctx, intent, download.Direct, ids)
	if err != nil {
		return nil, err
	}
	prepared := &preparedResourceRefresh{plan: plan, profile: profile}
	prepared.capability, err = s.selectedCapability(ctx)
	if err != nil {
		_ = plan.Abort()
		return nil, err
	}
	if err := plan.Validate(func(home string, paths map[string]string) error {
		_, renderErr := s.validatedCandidate(ctx, profile, intent, home, paths, prepared.capability)
		return renderErr
	}); err != nil {
		_ = plan.Abort()
		return nil, err
	}
	return prepared, nil
}

func (s *runtimeService) prepareResourceCache(ctx context.Context, intent config.Snapshot, ids []string) (*preparedResourceRefresh, error) {
	home, err := s.registry.ActiveHome()
	if err != nil {
		return nil, err
	}
	var plan *resources.Plan
	if home == "" || len(ids) == 0 {
		plan, err = s.registry.Stage(ctx, intent, download.Direct)
	} else {
		statuses, statusErr := s.registry.Status(intent)
		if statusErr != nil {
			return nil, statusErr
		}
		due := append([]string(nil), ids...)
		for _, status := range statuses {
			if status.Enabled && !status.Validated {
				due = append(due, status.ID)
			}
		}
		plan, err = s.registry.StageDue(ctx, intent, download.Direct, due)
	}
	if err != nil {
		return nil, err
	}
	if err := plan.ValidateResources(); err != nil {
		_ = plan.Abort()
		return nil, err
	}
	return &preparedResourceRefresh{plan: plan}, nil
}

func (s *runtimeService) applyPreparedResourceRefresh(ctx context.Context, prepared *preparedResourceRefresh) error {
	defer s.resourceDirty.Store(true)
	defer prepared.plan.Abort()
	if err := ctx.Err(); err != nil {
		return err
	}
	if prepared.profile != nil && prepared.plan.Changed() {
		return s.applyResourcePlan(ctx, prepared.plan, prepared.profile, prepared.capability, false)
	}
	if _, err := prepared.plan.Commit(); err != nil {
		return err
	}
	return prepared.plan.Finalize()
}

func (s *runtimeService) runServiceWork(ctx context.Context, requests <-chan serviceWorkRequest, results chan<- serviceWorkResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			result := serviceWorkResult{id: request.id, done: request.done}
			switch request.kind {
			case workSubscriptionRefresh:
				_, result.err = s.subs.Refresh(request.ctx, request.command.SubscriptionID)
			case workResourceRefresh:
				result.prepared, result.err = s.prepareResourceRefresh(request.ctx, request.ids)
			}
			if result.err == nil {
				result.err = request.ctx.Err()
			}
			if result.err != nil && result.prepared != nil {
				_ = result.prepared.plan.Abort()
				result.prepared = nil
			}
			select {
			case results <- result:
				select {
				case <-request.done:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				if result.prepared != nil {
					_ = result.prepared.plan.Abort()
				}
				return
			}
		}
	}
}

func (s *runtimeService) run(ctx context.Context, startup func(context.Context) error) (result error) {
	serviceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.server.Close()

	processCtx, cancelProcess := context.WithCancel(context.WithoutCancel(serviceCtx))
	s.processCtx = processCtx
	defer cancelProcess()
	if err := s.subScheduler.Start(serviceCtx); err != nil {
		return err
	}
	defer s.subScheduler.Stop()

	unsubscribe := s.store.Subscribe("subscriptions", func(change config.Change) {
		s.subs.CancelChangedSources(change.Before.Subscriptions, change.After.Subscriptions)
		s.offerChange(change)
	})
	defer unsubscribe()
	for _, section := range []string{"app", "mihomo", "monitor", "dns", "resources", "filters"} {
		unsubscribeSection := s.store.Subscribe(section, s.offerChange)
		defer unsubscribeSection()
	}

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		s.backgroundErrors <- s.server.Serve(serviceCtx)
	}()
	go func() {
		defer workers.Done()
		s.backgroundErrors <- config.WatchWithResults(serviceCtx, s.configDir, s.store, func(err error) {
			offerLatest(s.configResults, err)
		})
	}()
	workRequests := make(chan serviceWorkRequest, serviceWorkQueueSize)
	workResults := make(chan serviceWorkResult, serviceWorkQueueSize)
	var workWorkers sync.WaitGroup
	workWorkers.Add(1)
	go func() {
		defer workWorkers.Done()
		s.runServiceWork(serviceCtx, workRequests, workResults)
	}()
	workRecords := make(map[uint64]*serviceWorkRecord)
	var workID uint64
	defer func() {
		if err := s.shutdown(); err != nil {
			result = errors.Join(result, core.WrapActivation(core.ActivationRollback, err))
		}
	}()
	defer func() {
		cancel()
		for _, record := range workRecords {
			record.cancel()
		}
		workWorkers.Wait()
		for {
			select {
			case result := <-workResults:
				if result.prepared != nil {
					_ = result.prepared.plan.Abort()
				}
			default:
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = s.server.Close()
		workers.Wait()
	}()
	go s.runNotifications(serviceCtx)
	defer func() {
		cancel()
		<-s.notificationDone
	}()

	queueWork := func(kind serviceWorkKind, cmd ipc.Command, jobID string, ids []string, scheduled bool) {
		workID++
		workCtx, stopWork := context.WithCancel(serviceCtx)
		request := serviceWorkRequest{id: workID, kind: kind, ctx: workCtx, done: make(chan struct{}), command: cmd, ids: ids}
		record := &serviceWorkRecord{cancel: stopWork, jobID: jobID, command: cmd, scheduled: scheduled}
		workRecords[workID] = record
		select {
		case workRequests <- request:
		default:
			delete(workRecords, workID)
			stopWork()
			err := fmt.Errorf("clashpulse: background work queue is full")
			if jobID != "" {
				s.finishServiceIntent(jobID, cmd, err)
			} else {
				s.reportError("resource", err)
			}
		}
	}
	cancelWork := func() {
		for _, record := range workRecords {
			if !record.canceled {
				record.canceled = true
				record.cancel()
			}
		}
	}
	finishWork := func(result serviceWorkResult) {
		record, ok := workRecords[result.id]
		if !ok {
			if result.prepared != nil {
				_ = result.prepared.plan.Abort()
			}
			close(result.done)
			return
		}
		delete(workRecords, result.id)
		record.cancel()
		if record.canceled {
			if result.prepared != nil {
				_ = result.prepared.plan.Abort()
			}
			if record.jobID != "" {
				s.discardServiceIntent(record.jobID)
			}
		} else {
			err := result.err
			if result.prepared != nil {
				err = s.applyPreparedResourceRefresh(serviceCtx, result.prepared)
			}
			if record.jobID != "" {
				s.finishServiceIntent(record.jobID, record.command, err)
			} else if record.scheduled {
				if err != nil {
					s.reportError("resource", err)
				} else {
					s.completeScheduledResourceRefresh()
					s.publish()
				}
				s.subScheduler.WakeResources()
			}
		}
		close(result.done)
	}
	startIntent := func(cmd ipc.Command) {
		var jobID string
		switch cmd.Kind {
		case ipc.CommandRefreshSubscription:
			jobID = s.beginServiceIntent(cmd)
			queueWork(workSubscriptionRefresh, cmd, jobID, nil, false)
		case ipc.CommandRefreshResource:
			jobID = s.beginServiceIntent(cmd)
			if !configuredResource(s.store.Snapshot(), cmd.ResourceID) {
				s.finishServiceIntent(jobID, cmd, fmt.Errorf("resource is not configured"))
				return
			}
			queueWork(workResourceRefresh, cmd, jobID, []string{cmd.ResourceID}, false)
		case ipc.CommandRefreshFilter:
			jobID = s.beginServiceIntent(cmd)
			resourceID := resourceForFilter(s.store.Snapshot(), cmd.FilterID)
			if resourceID == "" {
				s.finishServiceIntent(jobID, cmd, fmt.Errorf("filter is not configured"))
				return
			}
			queueWork(workResourceRefresh, cmd, jobID, []string{resourceID}, false)
		default:
			cancelWork()
			s.runIntent(serviceCtx, cmd)
		}
	}
	if startup != nil {
		if err := startup(serviceCtx); err != nil {
			return err
		}
	}
	for {
		select {
		case <-serviceCtx.Done():
			return nil
		case err := <-s.backgroundErrors:
			if serviceCtx.Err() != nil {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("clashpulse: service stopped unexpectedly")
		case err := <-s.configResults:
			if err != nil {
				s.reportError("config_reload", err)
			} else if s.resolveIssue("config_reload", "") {
				s.publish()
			}
		case err := <-s.configErrors:
			s.reportError("config", err)
		case <-s.notificationErrors:
			s.reportError("notification", fmt.Errorf("desktop notification unavailable"))
		case change := <-s.changes:
			cancelWork()
			if err := s.runOperation(serviceCtx, "", func(operationCtx context.Context) error { return s.applyChange(operationCtx, change) }); err != nil {
				s.reportError("config", err)
			}
		case <-s.stateChanged:
			s.reconcileSubscriptionFailures(s.subs.List())
			s.publish()
		case cmd := <-s.intents:
			startIntent(cmd)
		case ids := <-s.resourceRefresh:
			queueWork(workResourceRefresh, ipc.Command{}, "", ids, true)
		case result := <-workResults:
			finishWork(result)
		case <-s.exited:
			cancelWork()
			s.exited = nil
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.proxy.Restore(cleanup); err != nil {
				s.reportError("system_proxy", err)
			} else {
				s.proxyActive = false
			}
			_ = s.stopMonitorAndProcess(cleanup)
			stop()
			if s.localProfile != nil {
				return core.WrapActivation(core.ActivationProcessStart, fmt.Errorf("Mihomo child exited unexpectedly"))
			}
			s.reportError("mihomo", fmt.Errorf("mihomo child exited unexpectedly"))
		case batch := <-s.batches:
			s.acceptBatch(serviceCtx, batch)
		}
	}
}

func offerLatest[T any](ch chan T, value T) {
	select {
	case ch <- value:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- value:
		default:
		}
	}
}
