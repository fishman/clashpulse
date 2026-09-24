package monitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type gatedProber struct {
	started chan struct{}
	release chan struct{}
}

func (p gatedProber) Delay(ctx context.Context, proxy, _ string, _ time.Duration) (time.Duration, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		if proxy == "alpha" {
			return 900 * time.Millisecond, nil
		}
		return 100 * time.Millisecond, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func TestSchedulerSkipsDecisionWhenGroupChangesDuringBatch(t *testing.T) {
	policy := testPolicy()
	policy.Concurrency = 1
	policy.Interval = time.Hour
	policy.Jitter = 0
	prober := gatedProber{started: make(chan struct{}, 1), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := make(chan Batch, 1)
	scheduler, err := NewScheduler(policy, prober, func(batch Batch) {
		batches <- batch
		cancel()
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := GroupState{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true, AutomationEnabled: true}
	if err := scheduler.SetGroups([]GroupState{initial}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case <-prober.started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("probe did not begin")
	}
	changed := initial
	changed.Selected = "beta"
	if err := scheduler.SetGroups([]GroupState{changed}); err != nil {
		t.Fatal(err)
	}
	close(prober.release)
	select {
	case batch := <-batches:
		if len(batch.Decisions) != 0 || len(batch.Alerts) != 0 {
			t.Fatalf("stale batch evaluated against changed group: %+v", batch)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("batch did not complete")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}
