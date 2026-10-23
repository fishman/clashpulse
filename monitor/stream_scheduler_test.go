package monitor

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

type recordingStreamer struct {
	mu      sync.Mutex
	pulled  []string
	outcome map[string]StreamOutcome
	block   chan struct{}
}

func (r *recordingStreamer) StreamPull(ctx context.Context, proxy, url string) StreamSample {
	r.mu.Lock()
	r.pulled = append(r.pulled, proxy)
	outcome, ok := r.outcome[proxy]
	r.mu.Unlock()
	if !ok {
		outcome = StreamSuccess
	}
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Outcome: StreamError}
		}
	}
	return StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Bytes: 262144, Duration: time.Second, Outcome: outcome}
}

func latencyHistory(group string, medians map[string]time.Duration) map[string]map[string][]Sample {
	policy := DefaultPolicy()
	history := map[string][]Sample{}
	for proxy, latency := range medians {
		history[proxy] = []Sample{testSample(policy, group, proxy, latency, 1)}
	}
	return map[string]map[string][]Sample{group: history}
}

func TestStreamTargetsSelectedThenThreeFastest(t *testing.T) {
	groups := []GroupState{{Group: "auto", Selected: "e", Proxies: []string{"a", "b", "c", "d", "e"}, ProbeEnabled: true, AutomationEnabled: true}}
	history := latencyHistory("auto", map[string]time.Duration{"a": 300, "b": 100, "c": 200, "d": 50, "e": 400})
	members := map[string]bool{"a": true, "b": true, "c": true, "d": true, "e": true}
	got := streamTargets(groups, history, members, 5, 0)
	if !slices.Equal(got, []string{"e", "d", "b", "c"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestStreamTargetsSkipNonMembers(t *testing.T) {
	groups := []GroupState{{Group: "auto", Selected: "a", Proxies: []string{"a", "DIRECT", "nested"}, ProbeEnabled: true, AutomationEnabled: true}}
	history := latencyHistory("auto", map[string]time.Duration{"a": 100, "DIRECT": 1, "nested": 2})
	got := streamTargets(groups, history, map[string]bool{"a": true}, 5, 0)
	if !slices.Equal(got, []string{"a"}) {
		t.Fatalf("targets = %v, want only probe-group members", got)
	}
}

func TestStreamRoundDeduplicatesSharedNodes(t *testing.T) {
	groups := []GroupState{
		{Group: "one", Selected: "shared", Proxies: []string{"shared", "x"}, ProbeEnabled: true, AutomationEnabled: true},
		{Group: "two", Selected: "shared", Proxies: []string{"shared", "y"}, ProbeEnabled: true, AutomationEnabled: true},
	}
	history := map[string]map[string][]Sample{}
	for _, g := range []string{"one", "two"} {
		for group, h := range latencyHistory(g, map[string]time.Duration{"shared": 100, "x": 200, "y": 300}) {
			history[group] = h
		}
	}
	got := streamTargets(groups, history, map[string]bool{"shared": true, "x": true, "y": true}, 5, 0)
	if !slices.Equal(got, []string{"shared", "x", "y"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestStreamTargetsCapAtEight(t *testing.T) {
	var groups []GroupState
	members := map[string]bool{}
	history := map[string]map[string][]Sample{}
	for _, g := range []string{"g1", "g2", "g3"} {
		proxies := []string{g + "-sel", g + "-a", g + "-b", g + "-c"}
		groups = append(groups, GroupState{Group: g, Selected: g + "-sel", Proxies: proxies, ProbeEnabled: true, AutomationEnabled: true})
		medians := map[string]time.Duration{}
		for i, p := range proxies {
			members[p] = true
			medians[p] = time.Duration(i+1) * time.Millisecond
		}
		for group, h := range latencyHistory(g, medians) {
			history[group] = h
		}
	}
	got := streamTargets(groups, history, members, 5, 0)
	want := []string{"g1-sel", "g2-sel", "g3-sel", "g1-a", "g2-a", "g3-a", "g1-b", "g2-b"}
	if !slices.Equal(got, want) {
		t.Fatalf("round 0 targets = %v, want %v", got, want)
	}
	got = streamTargets(groups, history, members, 5, 1)
	want = []string{"g1-sel", "g2-sel", "g3-sel", "g2-a", "g3-a", "g1-a", "g2-b", "g3-b"}
	if !slices.Equal(got, want) {
		t.Fatalf("round 1 targets = %v, want rotation starting at g2: %v", got, want)
	}
}

func steadySchedulerPolicy() Policy {
	policy := DefaultPolicy()
	policy.SwitchPolicy = SwitchSteadyStream
	policy.Interval = time.Hour
	policy.Jitter = 0
	return policy
}

func TestStreamRoundEmitsSteadyDecision(t *testing.T) {
	streamer := &recordingStreamer{outcome: map[string]StreamOutcome{"alpha": StreamReset}}
	batches := make(chan Batch, 16)
	scheduler, err := NewScheduler(steadySchedulerPolicy(), instantProber{}, streamer, func(b Batch) { batches <- b })
	if err != nil {
		t.Fatal(err)
	}
	scheduler.SetStreamMembers([]string{"alpha", "beta"})
	if err := scheduler.SetGroups([]GroupState{{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true, AutomationEnabled: true}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.After(5 * time.Second)
	for {
		select {
		case batch := <-batches:
			if len(batch.Streams) > 0 {
				if len(batch.Streams) != 2 || len(batch.StreamStability) != 2 {
					t.Fatalf("stream batch = %+v", batch)
				}
				return
			}
		case <-deadline:
			t.Fatal("no stream batch")
		}
	}
}

func TestStreamRoundCancelsOnStop(t *testing.T) {
	streamer := &recordingStreamer{block: make(chan struct{})}
	batches := make(chan Batch, 16)
	scheduler, err := NewScheduler(steadySchedulerPolicy(), instantProber{}, streamer, func(b Batch) { batches <- b })
	if err != nil {
		t.Fatal(err)
	}
	scheduler.SetStreamMembers([]string{"alpha", "beta"})
	_ = scheduler.SetGroups([]GroupState{{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true, AutomationEnabled: true}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	for start := time.Now(); ; {
		streamer.mu.Lock()
		n := len(streamer.pulled)
		streamer.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("pull never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return while a pull was blocked")
	}
	for len(batches) > 0 {
		if batch := <-batches; len(batch.Streams) > 0 {
			t.Fatal("cancelled stream round emitted a batch")
		}
	}
}

func TestNewSchedulerRequiresStreamerForSteadyStream(t *testing.T) {
	if _, err := NewScheduler(steadySchedulerPolicy(), instantProber{}, nil, func(Batch) {}); err == nil {
		t.Fatal("steady_stream scheduler without a streamer accepted")
	}
}
