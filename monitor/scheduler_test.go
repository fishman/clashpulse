package monitor

import (
	"context"
	"testing"
	"time"
)

type waitingProber struct {
	started chan struct{}
}

func (p waitingProber) Delay(ctx context.Context, proxy, testURL string, timeout time.Duration) (time.Duration, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestSchedulerCancellationJoinsActiveProbe(t *testing.T) {
	policy := testPolicy()
	policy.Timeout = time.Minute
	policy.Interval = time.Hour
	policy.Jitter = 0
	prober := waitingProber{started: make(chan struct{}, 1)}
	scheduler, err := NewScheduler(policy, prober, func(Batch) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.SetGroups([]GroupState{{
		Group:             "auto",
		Selected:          "alpha",
		Proxies:           []string{"alpha", "beta"},
		ProbeEnabled:      true,
		AutomationEnabled: true,
	}}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case <-prober.started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("scheduler did not start a probe")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not cancel and join the active probe")
	}
}
