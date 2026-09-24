package monitor

import (
	"testing"
	"time"
)

func testPolicy() Policy {
	policy := DefaultPolicy()
	policy.Jitter = 0
	return policy
}

func testGroup() GroupState {
	return GroupState{
		Group:             "auto",
		Selected:          "alpha",
		Proxies:           []string{"alpha", "beta"},
		ProbeEnabled:      true,
		AutomationEnabled: true,
	}
}

func testSample(policy Policy, group, proxy string, latency time.Duration, at int64) Sample {
	return Sample{
		Group:      group,
		Proxy:      proxy,
		URL:        policy.TestURL,
		FinishedAt: time.Unix(at, 0),
		Latency:    latency,
		Outcome:    OutcomeSuccess,
	}
}

func TestDecideNeedsConsecutiveUnhealthySamples(t *testing.T) {
	policy := testPolicy()
	samples := []Sample{
		testSample(policy, "auto", "alpha", 900*time.Millisecond, 1),
		testSample(policy, "auto", "beta", 400*time.Millisecond, 1),
		testSample(policy, "auto", "beta", 410*time.Millisecond, 2),
		testSample(policy, "auto", "beta", 390*time.Millisecond, 3),
	}
	decision := Decide(policy, testGroup(), samples, time.Unix(4, 0))
	if decision.Switch {
		t.Fatalf("one unhealthy sample switched proxy: %+v", decision)
	}
}

func TestDecideUsesMedianAndRequiresMaterialImprovement(t *testing.T) {
	policy := testPolicy()
	state := testGroup()
	var samples []Sample
	for i := int64(1); i <= 3; i++ {
		samples = append(samples,
			testSample(policy, "auto", "alpha", 900*time.Millisecond, i),
			testSample(policy, "auto", "beta", 800*time.Millisecond, i),
		)
	}
	decision := Decide(policy, state, samples, time.Unix(4, 0))
	if !decision.Switch || decision.New != "beta" {
		t.Fatalf("candidate exactly at the improvement boundary should be selected: %+v", decision)
	}

	for i := range samples {
		if samples[i].Proxy == "beta" {
			samples[i].Latency = 801 * time.Millisecond
		}
	}
	decision = Decide(policy, state, samples, time.Unix(4, 0))
	if decision.Switch {
		t.Fatalf("candidate worse than threshold switched proxy: %+v", decision)
	}
}

func TestDecideDoesNotTrustSingleFastCandidateSample(t *testing.T) {
	policy := testPolicy()
	var samples []Sample
	for i := int64(1); i <= 3; i++ {
		samples = append(samples,
			testSample(policy, "auto", "alpha", 900*time.Millisecond, i),
			testSample(policy, "auto", "beta", 900*time.Millisecond, i),
		)
	}
	samples[3].Latency = 100 * time.Millisecond
	decision := Decide(policy, testGroup(), samples, time.Unix(4, 0))
	if decision.Switch {
		t.Fatalf("single fast result outweighed candidate median: %+v", decision)
	}
}

func TestDecideAllFailedBreakerAndManualOverride(t *testing.T) {
	policy := testPolicy()
	now := time.Unix(10, 0)
	failed := []Sample{
		{Group: "auto", Proxy: "alpha", URL: policy.TestURL, FinishedAt: time.Unix(1, 0), Outcome: OutcomeError},
		{Group: "auto", Proxy: "beta", URL: policy.TestURL, FinishedAt: time.Unix(2, 0), Outcome: OutcomeTimeout},
	}
	decision := Decide(policy, testGroup(), failed, now)
	if decision.Switch || decision.Reason != ReasonAllCandidatesFailed {
		t.Fatalf("all-failed decision = %+v", decision)
	}

	state := testGroup()
	state.ManualOverride = true
	decision = Decide(policy, state, failed, now)
	if decision.Switch || decision.Reason != ReasonManualOverride {
		t.Fatalf("manual override decision = %+v", decision)
	}
}

func TestDecideCooldownBoundary(t *testing.T) {
	policy := testPolicy()
	now := time.Unix(100, 0)
	state := testGroup()
	state.LastSwitchAt = now.Add(-policy.Cooldown)
	var samples []Sample
	for i := int64(1); i <= 3; i++ {
		samples = append(samples,
			testSample(policy, "auto", "alpha", 900*time.Millisecond, i),
			testSample(policy, "auto", "beta", 500*time.Millisecond, i),
		)
	}
	decision := Decide(policy, state, samples, now)
	if !decision.Switch {
		t.Fatalf("cooldown should expire at its exact boundary: %+v", decision)
	}
}

func TestLatencyAlertRequiresEveryProxyAboveStrictThreshold(t *testing.T) {
	policy := testPolicy()
	state := testGroup()
	state.Proxies = []string{"alpha", "beta", "gamma"}
	samples := []Sample{
		testSample(policy, "auto", "alpha", 251*time.Millisecond, 1),
		testSample(policy, "auto", "beta", 250*time.Millisecond, 1),
		testSample(policy, "auto", "gamma", 300*time.Millisecond, 1),
	}
	_, event := UpdateLatencyAlert(policy, state, samples, AlertState{})
	if event.Transition != AlertNoChange {
		t.Fatalf("exactly-at-threshold proxy must suppress alert: %+v", event)
	}

	samples[1].Latency = 200 * time.Millisecond
	_, event = UpdateLatencyAlert(policy, state, samples, AlertState{})
	if event.Transition != AlertNoChange {
		t.Fatalf("mixed fast result must suppress alert: %+v", event)
	}
}

func TestLatencyAlertDeduplicatesAndRecoveryRearms(t *testing.T) {
	policy := testPolicy()
	state := testGroup()
	bad := []Sample{
		testSample(policy, "auto", "alpha", 251*time.Millisecond, 1),
		testSample(policy, "auto", "beta", 300*time.Millisecond, 1),
	}
	active, event := UpdateLatencyAlert(policy, state, bad, AlertState{})
	if !active.HighLatency || event.Transition != AlertHighLatency {
		t.Fatalf("initial high-latency transition = state %+v event %+v", active, event)
	}
	active, event = UpdateLatencyAlert(policy, state, bad, active)
	if !active.HighLatency || event.Transition != AlertNoChange {
		t.Fatalf("repeated high batch was not deduplicated: state %+v event %+v", active, event)
	}

	recovered := append([]Sample(nil), bad...)
	recovered[1].Latency = 250 * time.Millisecond
	recovered[1].FinishedAt = time.Unix(2, 0)
	active, event = UpdateLatencyAlert(policy, state, recovered, active)
	if active.HighLatency || event.Transition != AlertRecovered {
		t.Fatalf("recovery transition = state %+v event %+v", active, event)
	}
	bad[0].FinishedAt = time.Unix(3, 0)
	bad[1].FinishedAt = time.Unix(3, 0)
	active, event = UpdateLatencyAlert(policy, state, bad, active)
	if !active.HighLatency || event.Transition != AlertHighLatency {
		t.Fatalf("recovered state did not re-arm alert: state %+v event %+v", active, event)
	}
}

func TestLatencyAlertDoesNotTreatTimeoutAsHighLatencyOrRecovery(t *testing.T) {
	policy := testPolicy()
	state := testGroup()
	timedOut := []Sample{
		testSample(policy, "auto", "alpha", 300*time.Millisecond, 1),
		{Group: "auto", Proxy: "beta", URL: policy.TestURL, FinishedAt: time.Unix(1, 0), Outcome: OutcomeTimeout},
	}
	previous := AlertState{HighLatency: true}
	current, event := UpdateLatencyAlert(policy, state, timedOut, previous)
	if current != previous || event.Transition != AlertNoChange {
		t.Fatalf("timeout should leave alert state unknown/latched: state %+v event %+v", current, event)
	}
}

func TestDecideOfflineSelectedUsesConsecutiveFailureEvidence(t *testing.T) {
	policy := testPolicy()
	samples := []Sample{
		testSample(policy, "auto", "alpha", 50*time.Millisecond, 1),
		testSample(policy, "auto", "alpha", 50*time.Millisecond, 2),
	}
	for i := int64(3); i <= 5; i++ {
		samples = append(samples,
			Sample{Group: "auto", Proxy: "alpha", URL: policy.TestURL, FinishedAt: time.Unix(i, 0), Outcome: OutcomeTimeout},
			testSample(policy, "auto", "beta", 900*time.Millisecond, i),
		)
	}
	decision := Decide(policy, testGroup(), samples, time.Unix(6, 0))
	if !decision.Switch || decision.New != "beta" {
		t.Fatalf("offline selected proxy was retained despite working candidate: %+v", decision)
	}
}

func TestLatencyAlertRecoversWithOneFastProxyDespiteOtherTimeout(t *testing.T) {
	policy := testPolicy()
	state := testGroup()
	samples := []Sample{
		{Group: "auto", Proxy: "alpha", URL: policy.TestURL, FinishedAt: time.Unix(1, 0), Outcome: OutcomeTimeout},
		testSample(policy, "auto", "beta", 249*time.Millisecond, 1),
	}
	next, event := UpdateLatencyAlert(policy, state, samples, AlertState{HighLatency: true})
	if next.HighLatency || event.Transition != AlertRecovered {
		t.Fatalf("fast measured proxy did not clear all-slow alert: %+v, %+v", next, event)
	}
}

func TestDefaultPolicyUsesApprovedHTTPProbeURL(t *testing.T) {
	policy := DefaultPolicy()
	if policy.TestURL != "http://cp.cloudflare.com/generate_204" {
		t.Fatalf("default probe URL = %q", policy.TestURL)
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("approved HTTP probe rejected: %v", err)
	}
	policy.TestURL = "ftp://cp.cloudflare.com/generate_204"
	if err := policy.Validate(); err == nil {
		t.Fatal("non-HTTP probe scheme accepted")
	}
	policy.TestURL = "http://user:secret@cp.cloudflare.com/generate_204"
	if err := policy.Validate(); err == nil {
		t.Fatal("probe URL credentials accepted")
	}
}
