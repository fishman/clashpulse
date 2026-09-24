package app

import (
	"context"
	"fmt"
	"time"

	"github.com/fishman/clashpulse/download"
)

func (s *runtimeService) resourceDeadlines(now time.Time) (time.Time, []string) {
	if _, err := s.subs.ActiveID(); err != nil {
		return time.Time{}, nil
	}
	intent := s.store.Snapshot()
	if len(intent.Resources) == 0 {
		return time.Time{}, nil
	}
	statuses, err := s.registry.Status(intent)
	if err != nil {
		statuses = nil
	}
	checked := make(map[string]time.Time, len(statuses))
	for _, status := range statuses {
		if status.Validated {
			checked[status.ID] = status.LastCheck
		}
	}
	s.resourceScheduleMu.Lock()
	defer s.resourceScheduleMu.Unlock()
	var nearest time.Time
	var due []string
	for _, resource := range intent.Resources {
		if !resource.Enabled {
			continue
		}
		base := checked[resource.ID]
		if attempt := s.resourceAttempts[resource.ID]; attempt.After(base) {
			base = attempt
		}
		deadline := now
		if !base.IsZero() {
			deadline = base.Add(resource.Interval)
		}
		if !deadline.After(now) {
			due = append(due, resource.ID)
		}
		if nearest.IsZero() || deadline.Before(nearest) {
			nearest = deadline
		}
	}
	return nearest, due
}

func (s *runtimeService) nextResourceDue(now time.Time) time.Time {
	deadline, _ := s.resourceDeadlines(now)
	return deadline
}

func (s *runtimeService) enqueueResourceRefresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	_, due := s.resourceDeadlines(now)
	if len(due) == 0 {
		return nil
	}
	s.resourceScheduleMu.Lock()
	for _, id := range due {
		s.resourceAttempts[id] = now
	}
	s.resourceScheduleMu.Unlock()
	select {
	case s.resourceRefresh <- due:
		return nil
	default:
		merged := make(map[string]bool, len(due))
		for _, id := range due {
			merged[id] = true
		}
		select {
		case pending := <-s.resourceRefresh:
			for _, id := range pending {
				merged[id] = true
			}
		default:
		}
		all := make([]string, 0, len(merged))
		for id := range merged {
			all = append(all, id)
		}
		select {
		case s.resourceRefresh <- all:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *runtimeService) refreshResourceIDs(ctx context.Context, ids []string) error {
	defer s.resourceDirty.Store(true)
	_, profile, err := s.subs.ActiveProfile()
	if err != nil {
		return err
	}
	intent := s.store.Snapshot()
	plan, err := s.registry.StageDue(ctx, intent, download.Direct, ids)
	if err != nil {
		return err
	}
	defer plan.Abort()
	capability, err := s.selectedCapability(ctx)
	if err != nil {
		return err
	}
	var candidate []byte
	if err := plan.Validate(func(home string, paths map[string]string) error {
		candidate, err = s.validatedCandidate(ctx, profile, intent, home, paths, capability)
		return err
	}); err != nil {
		return err
	}
	changed := plan.Changed()
	if _, err := plan.Commit(); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err := s.applyCandidate(ctx, candidate, capability, plan.Home()); err != nil {
		if rollbackErr := plan.Rollback(); rollbackErr != nil {
			return fmt.Errorf("resource reload failed and rollback could not restore prior generation")
		}
		return err
	}
	return nil
}
