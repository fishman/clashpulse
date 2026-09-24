package monitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type timeoutProber struct{}

func (timeoutProber) Delay(context.Context, string, string, time.Duration) (time.Duration, error) {
	return 0, context.DeadlineExceeded
}

func TestSchedulerReportsTimeoutDistinctFromError(t *testing.T) {
	policy := testPolicy()
	policy.Interval = time.Hour
	policy.Jitter = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := make(chan Batch, 1)
	scheduler, err := NewScheduler(policy, timeoutProber{}, func(batch Batch) {
		batches <- batch
		cancel()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.SetGroups([]GroupState{{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true, AutomationEnabled: true}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case batch := <-batches:
		if len(batch.Samples) != 2 {
			t.Fatalf("probe count = %d", len(batch.Samples))
		}
		for _, sample := range batch.Samples {
			if sample.Outcome != OutcomeTimeout || sample.Latency != 0 {
				t.Fatalf("timeout sample = %+v", sample)
			}
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("scheduler did not produce a batch")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}
