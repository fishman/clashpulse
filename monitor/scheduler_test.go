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
	scheduler, err := NewScheduler(policy, prober, nil, func(Batch) {})
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

type instantProber struct{}

func (instantProber) Delay(context.Context, string, string, time.Duration) (time.Duration, error) {
	return 100 * time.Millisecond, nil
}

func TestSchedulerDialFailuresForceEarlyProbeWithSpacing(t *testing.T) {
	policy := testPolicy()
	policy.Interval = time.Hour
	batches := make(chan Batch, 8)
	scheduler, err := NewScheduler(policy, instantProber{}, nil, func(b Batch) { batches <- b })
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1000, 0)
	scheduler.now = func() time.Time { return clock }
	if err := scheduler.SetGroups([]GroupState{{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	next := func(want bool, why string) {
		t.Helper()
		select {
		case <-batches:
			if !want {
				t.Fatalf("unexpected probe round: %s", why)
			}
		case <-time.After(200 * time.Millisecond):
			if want {
				t.Fatalf("missing probe round: %s", why)
			}
		}
	}
	first := <-batches
	if len(first.Stability) != 2 || first.Stability[0].Stability.Samples != 1 || first.Stability[0].Stability.Median != 100*time.Millisecond {
		t.Fatalf("batch stability = %+v", first.Stability)
	}

	scheduler.ReportDialFailure("unknown")
	scheduler.ReportDialFailure("auto")
	next(false, "one dial failure is below max_failures")
	scheduler.ReportDialFailure("auto")
	next(true, "max_failures dial failures")

	scheduler.ReportDialFailure("auto")
	scheduler.ReportDialFailure("auto")
	next(false, "early probe within the spacing")
	clock = clock.Add(DialFailureProbeSpacing)
	scheduler.ReportDialFailure("auto")
	next(true, "spacing elapsed with failures still counted")
}

func TestSchedulerProbedGroupsFollowSetGroups(t *testing.T) {
	scheduler, err := NewScheduler(testPolicy(), instantProber{}, nil, func(Batch) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := scheduler.ProbedGroups(); len(got) != 0 {
		t.Fatalf("probed groups before SetGroups = %q", got)
	}
	if err := scheduler.SetGroups([]GroupState{
		{Group: "auto", Selected: "alpha", Proxies: []string{"alpha"}, ProbeEnabled: true},
		{Group: "manual", Selected: "alpha", Proxies: []string{"alpha"}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := scheduler.ProbedGroups(); len(got) != 1 || got[0] != "auto" {
		t.Fatalf("probed groups = %q", got)
	}
}
