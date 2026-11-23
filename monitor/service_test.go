package monitor

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fishman/clashpulse/services"
)

type fakeChecker struct {
	mu       sync.Mutex
	results  map[string]services.Outcome // proxy -> outcome for every service
	regions  map[string]string
	calls    [][]string // proxy followed by ids
	inFlight int
	peak     int
	gate     chan struct{}
}

func (f *fakeChecker) CheckServices(ctx context.Context, proxy string, ids []string) []services.Result {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{proxy}, ids...))
	f.inFlight++
	f.peak = max(f.peak, f.inFlight)
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
	out := make([]services.Result, len(ids))
	for i, id := range ids {
		outcome, ok := f.results[proxy]
		if !ok {
			outcome = services.Usable
		}
		out[i] = services.Result{Service: id, Outcome: outcome, Region: f.regions[proxy]}
	}
	return out
}

func (f *fakeChecker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func target(id, selected string, members ...string) ServiceTarget {
	return ServiceTarget{ID: id, Group: "ClashPulse - " + id, Selected: selected, Members: members, Interval: 30 * time.Minute, Failures: 2, Automatic: true}
}

func TestServiceFailing(t *testing.T) {
	tg := target("claude", "a", "a")
	cases := []struct {
		recent    []services.Outcome
		unhealthy bool
		want      bool
	}{
		{[]services.Outcome{services.Blocked, services.Blocked}, false, true},
		{[]services.Outcome{services.Usable, services.Blocked}, false, false},
		{[]services.Outcome{services.Failed, services.Failed}, false, false},
		{[]services.Outcome{services.Failed, services.Failed}, true, true},
		{[]services.Outcome{services.Blocked, services.Failed}, false, false},
		{[]services.Outcome{services.Blocked}, false, false},
	}
	for _, tc := range cases {
		if got := ServiceFailing(tg, tc.recent, tc.unhealthy); got != tc.want {
			t.Fatalf("ServiceFailing(%v, unhealthy=%v) = %v", tc.recent, tc.unhealthy, got)
		}
	}
}

func TestServiceCandidatesOrderAndFilter(t *testing.T) {
	members := []string{"cur", "jp-blocked", "us-slow", "us-fast", "unknown", "de", "n1", "n2", "n3", "n4", "n5", "n6"}
	tg := target("chatgpt", "cur", members...)
	tg.Regions = []string{"US", "JP"}
	last := map[string]services.Result{
		"jp-blocked": {Outcome: services.Blocked, Region: "JP"},
		"us-slow":    {Outcome: services.Usable, Region: "US"},
		"us-fast":    {Outcome: services.Usable, Region: "US"},
		"de":         {Outcome: services.Usable, Region: "DE"},
	}
	latency := map[string]time.Duration{"us-slow": 300 * time.Millisecond, "us-fast": 80 * time.Millisecond, "n1": 50 * time.Millisecond}
	got := ServiceCandidates(tg, last, latency)
	want := []string{"n1", "us-fast", "us-slow", "n2", "n3", "n4", "n5", "n6"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func runServiceScheduler(t *testing.T, checker *fakeChecker, plan ServicePlan) (*ServiceScheduler, chan ServiceBatch) {
	t.Helper()
	batches := make(chan ServiceBatch, 16)
	s := NewServiceScheduler(checker, func(b ServiceBatch) { batches <- b })
	s.SetPlan(plan)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && err != context.Canceled {
			t.Errorf("Run: %v", err)
		}
	})
	return s, batches
}

func nextServiceBatch(t *testing.T, batches chan ServiceBatch) ServiceBatch {
	t.Helper()
	select {
	case b := <-batches:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("no batch")
		return ServiceBatch{}
	}
}

func TestServiceSchedulerChecksOnlySelectedAndSharesNode(t *testing.T) {
	checker := &fakeChecker{}
	_, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Cooldown: time.Minute,
		Targets: []ServiceTarget{target("youtube", "a", "a", "b"), target("gemini", "a", "a", "b"), target("claude", "b", "a", "b")}})
	b := nextServiceBatch(t, batches)
	if len(b.Checks) != 3 || len(b.Next) != 3 {
		t.Fatalf("batch = %+v", b)
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if len(checker.calls) != 2 {
		t.Fatalf("calls = %v, want one per node", checker.calls)
	}
	for _, call := range checker.calls {
		if call[0] == "a" && !slices.Equal(call[1:], []string{"youtube", "gemini"}) {
			t.Fatalf("node a call = %v", call)
		}
	}
}

func TestServiceSchedulerSearchesAndSwitches(t *testing.T) {
	checker := &fakeChecker{results: map[string]services.Outcome{"cur": services.Blocked, "slow": services.Usable, "fast": services.Usable}, regions: map[string]string{"fast": "US"}}
	s, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Cooldown: time.Hour,
		Latency: map[string]time.Duration{"slow": 300 * time.Millisecond, "fast": 50 * time.Millisecond},
		Targets: []ServiceTarget{target("netflix", "cur", "cur", "slow", "fast")}})
	if b := nextServiceBatch(t, batches); len(b.Switches) != 0 {
		t.Fatalf("switched after one failure: %+v", b.Switches)
	}
	s.Request("netflix")
	sw := nextServiceBatch(t, batches).Switches
	if len(sw) != 1 || sw[0].Old != "cur" || sw[0].New != "fast" || sw[0].Reason != "blocked" || sw[0].Service != "netflix" || sw[0].Region != "US" {
		t.Fatalf("switch = %+v", sw)
	}
	s.Request("netflix")
	if b := nextServiceBatch(t, batches); len(b.Switches) != 0 {
		t.Fatalf("switched again inside the cooldown: %+v", b.Switches)
	}
}

// An unchecked node's region is unknown until its search check; a usable
// node outside the preferred regions must not win.
func TestServiceSearchHonorsRegions(t *testing.T) {
	checker := &fakeChecker{results: map[string]services.Outcome{"cur": services.Blocked}, regions: map[string]string{"fast": "DE", "slow": "US"}}
	tg := target("netflix", "cur", "cur", "slow", "fast")
	tg.Regions = []string{"US"}
	s, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Cooldown: time.Hour,
		Latency: map[string]time.Duration{"slow": 300 * time.Millisecond, "fast": 50 * time.Millisecond}, Targets: []ServiceTarget{tg}})
	nextServiceBatch(t, batches)
	s.Request("netflix")
	if sw := nextServiceBatch(t, batches).Switches; len(sw) != 1 || sw[0].New != "slow" {
		t.Fatalf("switch = %+v, want the US node", sw)
	}
}

func TestServiceSchedulerManualModeNeverSwitches(t *testing.T) {
	checker := &fakeChecker{results: map[string]services.Outcome{"cur": services.Blocked}}
	tg := target("netflix", "cur", "cur", "other")
	tg.Automatic = false
	s, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Targets: []ServiceTarget{tg}})
	for i := 0; i < 3; i++ {
		if b := nextServiceBatch(t, batches); len(b.Switches) != 0 || len(b.Exhausted) != 0 {
			t.Fatalf("searched with automatic=false: %+v", b)
		}
		s.Request("netflix")
	}
	if checker.callCount() < 3 {
		t.Fatalf("calls = %d", checker.callCount())
	}
	for _, call := range checker.calls {
		if call[0] != "cur" {
			t.Fatalf("checked a candidate with automatic=false: %v", call)
		}
	}
}

func TestServiceSchedulerExhaustedSearchCapsAtEight(t *testing.T) {
	members := []string{"cur"}
	results := map[string]services.Outcome{"cur": services.Blocked}
	for i := 0; i < 12; i++ {
		name := string(rune('a' + i))
		members = append(members, name)
		results[name] = services.Blocked
	}
	checker := &fakeChecker{results: results}
	s, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Cooldown: time.Hour, Targets: []ServiceTarget{target("claude", "cur", members...)}})
	nextServiceBatch(t, batches)
	s.Request("claude")
	b := nextServiceBatch(t, batches)
	if !slices.Equal(b.Exhausted, []string{"claude"}) || len(b.Switches) != 0 {
		t.Fatalf("batch = %+v", b)
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if checks := len(checker.calls); checks != 2+MaxServiceCandidates {
		t.Fatalf("calls = %d, want 2 checks of cur + %d candidates", checks, MaxServiceCandidates)
	}
	if checker.peak > 4 {
		t.Fatalf("peak concurrency %d", checker.peak)
	}
}

func TestServiceSchedulerNodeChangeIsDueNow(t *testing.T) {
	checker := &fakeChecker{}
	s, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "a", "a", "b")}})
	nextServiceBatch(t, batches)
	s.SetPlan(ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "b", "a", "b")}})
	if b := nextServiceBatch(t, batches); len(b.Checks) != 1 || b.Checks[0].Proxy != "b" {
		t.Fatalf("after node change: %+v", b.Checks)
	}
}

func TestServiceSchedulerUnchangedPlanDoesNoWork(t *testing.T) {
	checker := &fakeChecker{}
	s, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "a", "a", "b")}})
	nextServiceBatch(t, batches)
	s.SetPlan(ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "a", "a", "b")}, Latency: map[string]time.Duration{"b": time.Millisecond}})
	select {
	case b := <-batches:
		t.Fatalf("a latency-only plan change ran checks: %+v", b)
	case <-time.After(100 * time.Millisecond):
	}
	if checker.callCount() != 1 || !s.armed() {
		t.Fatalf("calls = %d, armed = %v", checker.callCount(), s.armed())
	}
}

func TestServiceSchedulerIdleWithoutServices(t *testing.T) {
	gate := make(chan struct{})
	checker := &fakeChecker{gate: gate}
	s, batches := runServiceScheduler(t, checker, ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "a", "a")}})
	for checker.callCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	s.SetPlan(ServicePlan{Slots: 4})
	select {
	case b := <-batches:
		if len(b.Checks) != 0 {
			t.Fatalf("a check for a removed service was published: %+v", b.Checks)
		}
	case <-time.After(200 * time.Millisecond):
	}
	close(gate)
	if s.armed() {
		t.Fatal("timer armed with no services")
	}
}

func TestServiceSchedulerKeepsResultsAcrossLatencyOnlyPlanChange(t *testing.T) {
	gate := make(chan struct{})
	checker := &fakeChecker{gate: gate}
	plan := ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "a", "a", "b")}}
	s, batches := runServiceScheduler(t, checker, plan)
	for checker.callCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	plan.Latency = map[string]time.Duration{"b": time.Millisecond}
	s.SetPlan(plan)
	close(gate)
	if b := nextServiceBatch(t, batches); len(b.Checks) != 1 {
		t.Fatalf("a latency update discarded the round: %+v", b)
	}
}
