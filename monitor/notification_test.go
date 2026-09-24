package monitor

import (
	"context"
	"testing"
	"time"
)

type slowProber struct{}

func (slowProber) Delay(context.Context, string, string, time.Duration) (time.Duration, error) {
	return 300 * time.Millisecond, nil
}

func TestSchedulerAlertsWhenAutomationIsNotOptedIn(t *testing.T) {
	policy := DefaultPolicy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := make(chan Batch, 1)
	scheduler, err := NewScheduler(policy, slowProber{}, func(batch Batch) { batches <- batch; cancel() })
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.SetGroups([]GroupState{{Group: "select-main", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case batch := <-batches:
		if len(batch.Alerts) != 1 || batch.Alerts[0].Transition != AlertHighLatency || len(batch.Decisions) != 1 || batch.Decisions[0].Switch {
			t.Fatalf("alert/switch batch = %+v", batch)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("monitor did not measure opted-in notification group")
	}
	<-done
}
