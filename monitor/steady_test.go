package monitor

import (
	"testing"
	"time"
)

func steadyPolicy() Policy {
	policy := testPolicy()
	policy.SwitchPolicy = SwitchSteadyStream
	policy.StreamURL = DefaultStreamURL
	policy.StreamInterval = 5 * time.Minute
	return policy
}

func pulls(proxy string, outcomes ...StreamOutcome) []StreamSample {
	samples := make([]StreamSample, len(outcomes))
	for i, outcome := range outcomes {
		samples[i] = StreamSample{Proxy: proxy, URL: DefaultStreamURL, FinishedAt: time.Unix(int64(100+i), 0),
			Bytes: 262144, Duration: time.Second, Outcome: outcome}
	}
	return samples
}

func withThroughput(samples []StreamSample, d time.Duration) []StreamSample {
	for i := range samples {
		samples[i].Duration = d
	}
	return samples
}

func TestDecideSteadyKeepsNodeWithOneInterruption(t *testing.T) {
	policy := steadyPolicy()
	streams := map[string][]StreamSample{
		"alpha": pulls("alpha", StreamSuccess, StreamReset, StreamSuccess, StreamSuccess, StreamSuccess),
		"beta":  pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess, StreamSuccess, StreamSuccess),
	}
	decision := DecideSteady(policy, testGroup(), nil, streams, time.Unix(200, 0))
	if decision.Switch || decision.Reason != ReasonSelectedStreamsHealthy {
		t.Fatalf("decision = %+v, want stay on alpha", decision)
	}
}

func TestDecideSteadySwitchesAwayFromInterruptedNode(t *testing.T) {
	policy := steadyPolicy()
	streams := map[string][]StreamSample{
		"alpha": pulls("alpha", StreamSuccess, StreamReset, StreamSuccess, StreamStall, StreamSuccess),
		"beta":  pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess),
	}
	decision := DecideSteady(policy, testGroup(), nil, streams, time.Unix(200, 0))
	if !decision.Switch || decision.New != "beta" || decision.Reason != ReasonSteadierStream || decision.Group != "auto" {
		t.Fatalf("decision = %+v, want switch to beta", decision)
	}
	if len(decision.Streams) != 8 {
		t.Fatalf("evidence = %d stream samples, want old and new windows (8)", len(decision.Streams))
	}
}

func TestDecideSteadyRequiresCompletedLatestAndEnoughPulls(t *testing.T) {
	policy := steadyPolicy()
	alpha := pulls("alpha", StreamReset, StreamReset, StreamSuccess)
	for name, beta := range map[string][]StreamSample{
		"too few":       pulls("beta", StreamSuccess, StreamSuccess),
		"latest broken": pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess, StreamTruncated),
		"not steadier":  pulls("beta", StreamSuccess, StreamReset, StreamSuccess, StreamTLS, StreamSuccess),
	} {
		decision := DecideSteady(policy, testGroup(), nil, map[string][]StreamSample{"alpha": alpha, "beta": beta}, time.Unix(200, 0))
		if decision.Switch {
			t.Fatalf("%s: switched to %s", name, decision.New)
		}
	}
}

func TestDecideSteadyBreaksTiesByThroughputThenLatency(t *testing.T) {
	policy := steadyPolicy()
	state := testGroup()
	state.Proxies = []string{"alpha", "beta", "gamma"}
	alpha := pulls("alpha", StreamReset, StreamReset, StreamSuccess)
	slow := withThroughput(pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess), 4*time.Second)
	fast := withThroughput(pulls("gamma", StreamSuccess, StreamSuccess, StreamSuccess), time.Second)
	decision := DecideSteady(policy, state, nil, map[string][]StreamSample{"alpha": alpha, "beta": slow, "gamma": fast}, time.Unix(200, 0))
	if decision.New != "gamma" {
		t.Fatalf("throughput tie-break chose %q, want gamma", decision.New)
	}
	equal := withThroughput(pulls("gamma", StreamSuccess, StreamSuccess, StreamSuccess), 4*time.Second)
	latency := []Sample{testSample(policy, "auto", "beta", 300*time.Millisecond, 100), testSample(policy, "auto", "gamma", 90*time.Millisecond, 100)}
	decision = DecideSteady(policy, state, latency, map[string][]StreamSample{"alpha": alpha, "beta": slow, "gamma": equal}, time.Unix(200, 0))
	if decision.New != "gamma" {
		t.Fatalf("latency tie-break chose %q, want gamma", decision.New)
	}
}

func TestDecideSteadyHonorsCommonGuards(t *testing.T) {
	policy := steadyPolicy()
	broken := pulls("alpha", StreamReset, StreamReset, StreamReset)
	good := pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess)
	state := testGroup()
	state.ManualOverride = true
	if d := DecideSteady(policy, state, nil, map[string][]StreamSample{"alpha": broken, "beta": good}, time.Unix(200, 0)); d.Switch || d.Reason != ReasonManualOverride {
		t.Fatalf("manual override: %+v", d)
	}
	state = testGroup()
	state.LastSwitchAt = time.Unix(190, 0)
	policy.Cooldown = time.Minute
	if d := DecideSteady(policy, state, nil, map[string][]StreamSample{"alpha": broken, "beta": good}, time.Unix(200, 0)); d.Switch || d.Reason != ReasonCooldown {
		t.Fatalf("cooldown: %+v", d)
	}
	policy.Cooldown = 0
	allBroken := pulls("beta", StreamSuccess, StreamSuccess, StreamReset)
	if d := DecideSteady(policy, testGroup(), nil, map[string][]StreamSample{"alpha": broken, "beta": allBroken}, time.Unix(200, 0)); d.Switch || d.Reason != ReasonAllCandidatesFailed {
		t.Fatalf("all failed: %+v", d)
	}
}

// The failover rule is the floor under steady_stream: a dead selected node
// must not wait for stream rounds.
func TestDecideUnderSteadyStreamLeavesDeadNodeOnLatency(t *testing.T) {
	policy := steadyPolicy()
	var samples []Sample
	for at := int64(1); at <= 3; at++ {
		samples = append(samples, failedSample(policy, "auto", "alpha", at), testSample(policy, "auto", "beta", 100*time.Millisecond, at))
	}
	decision := Decide(policy, testGroup(), samples, time.Unix(10, 0))
	if !decision.Switch || decision.New != "beta" {
		t.Fatalf("decision = %+v, want failover to beta", decision)
	}
}

func TestSteadyPolicyValidation(t *testing.T) {
	policy := steadyPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid steady policy: %v", err)
	}
	for name, mutate := range map[string]func(*Policy){
		"http url":       func(p *Policy) { p.StreamURL = "http://speed.example.com/file" },
		"userinfo":       func(p *Policy) { p.StreamURL = "https://user:pass@speed.example.com/file" },
		"short interval": func(p *Policy) { p.StreamInterval = 3 * time.Minute },
	} {
		p := steadyPolicy()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestMeasureStreamsUsesWindowAndMedianThroughput(t *testing.T) {
	samples := append(pulls("alpha", StreamReset), withThroughput(pulls("alpha", StreamSuccess, StreamSuccess, StreamSuccess), 2*time.Second)...)
	stats := MeasureStreams("alpha", samples, 3)
	if stats.Samples != 3 || stats.Completed != 3 || stats.MedianThroughput != 131072 {
		t.Fatalf("stats = %+v", stats)
	}
}
