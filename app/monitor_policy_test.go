package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/monitor"
)

func TestLoadedMonitorPolicyControlsDecisionAndConcurrency(t *testing.T) {
	configuredDir := t.TempDir()
	configured := "[monitor]\ntest_url = \"https://example.com/204\"\ninterval = \"1s\"\ntimeout = \"200ms\"\nconcurrency = 2\nthreshold = \"500ms\"\nconsecutive_bad_samples = 2\nmin_improvement = \"250ms\"\ncooldown = \"0s\"\njitter = \"0s\"\n"
	if err := os.WriteFile(filepath.Join(configuredDir, "config.toml"), []byte(configured), 0o600); err != nil {
		t.Fatal(err)
	}
	configuredSnapshot, err := config.Load(configuredDir)
	if err != nil {
		t.Fatal(err)
	}
	policy := monitorPolicy(configuredSnapshot.Monitor)

	now := time.Now()
	group := monitor.GroupState{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true, AutomationEnabled: true, LastSwitchAt: now.Add(-time.Minute)}
	samplesFor := func(beta ...time.Duration) []monitor.Sample {
		samples := []monitor.Sample{
			{Group: "auto", Proxy: "alpha", URL: "https://example.com/204", FinishedAt: time.Unix(1, 0), Latency: 600 * time.Millisecond, Outcome: monitor.OutcomeSuccess},
			{Group: "auto", Proxy: "alpha", URL: "https://example.com/204", FinishedAt: time.Unix(2, 0), Latency: 610 * time.Millisecond, Outcome: monitor.OutcomeSuccess},
		}
		for i, latency := range beta {
			samples = append(samples, monitor.Sample{Group: "auto", Proxy: "beta", URL: "https://example.com/204", FinishedAt: time.Unix(int64(i+1), 0), Latency: latency, Outcome: monitor.OutcomeSuccess})
		}
		return samples
	}
	samples := samplesFor(300*time.Millisecond, 310*time.Millisecond, 320*time.Millisecond)
	if decision := monitor.Decide(policy, group, samples, now); !decision.Switch || decision.New != "beta" {
		t.Fatalf("configured policy decision = %+v", decision)
	}
	if decision := monitor.Decide(policy, group, samplesFor(450*time.Millisecond, 455*time.Millisecond, 460*time.Millisecond), now); decision.Switch {
		t.Fatalf("configured improvement requirement was ignored: %+v", decision)
	}
	defaultSnapshot, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defaultPolicy := monitorPolicy(defaultSnapshot.Monitor)
	if decision := monitor.Decide(defaultPolicy, group, samples, now); decision.Switch {
		t.Fatalf("default policy unexpectedly switched: %+v", decision)
	}

	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	prober := gatedMonitorProber{started: make(chan string, 16), timeouts: make(chan time.Duration, 16), release: release}
	batches := make(chan monitor.Batch, 1)
	scheduler, err := monitor.NewScheduler(policy, prober, func(batch monitor.Batch) { batches <- batch })
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.SetGroups([]monitor.GroupState{{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta", "gamma"}, ProbeEnabled: true}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	for range policy.Concurrency {
		select {
		case <-prober.started:
		case <-time.After(time.Second):
			t.Fatal("configured workers did not start")
		}
		select {
		case timeout := <-prober.timeouts:
			if timeout != 200*time.Millisecond {
				t.Fatalf("probe timeout = %v", timeout)
			}
		case <-time.After(time.Second):
			t.Fatal("configured timeout was not passed to the probe")
		}
	}
	select {
	case proxy := <-prober.started:
		t.Fatalf("probe %q exceeded configured concurrency", proxy)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case batch := <-batches:
			if len(batch.Samples) != 3 {
				t.Fatalf("completed probe count = %d, want 3", len(batch.Samples))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("scheduler did not complete the configured probe batch")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("scheduler error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}

type gatedMonitorProber struct {
	started  chan string
	timeouts chan time.Duration
	release  <-chan struct{}
}

func (p gatedMonitorProber) Delay(ctx context.Context, proxy, _ string, timeout time.Duration) (time.Duration, error) {
	select {
	case p.started <- proxy:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case p.timeouts <- timeout:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case <-p.release:
		return 10 * time.Millisecond, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
