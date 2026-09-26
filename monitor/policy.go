package monitor

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SwitchFailover moves traffic only after the selected node has failed enough
// consecutive samples; SwitchLowestLatency keeps the fastest measured node and
// trades on every probe round instead.
const (
	SwitchFailover      = "failover"
	SwitchLowestLatency = "lowest_latency"
)

type Policy struct {
	TestURL               string
	SwitchPolicy          string
	Interval              time.Duration
	Timeout               time.Duration
	Concurrency           int
	Threshold             time.Duration
	LatencyAlertThreshold time.Duration
	WindowSize            int
	MinCandidateSamples   int
	ConsecutiveBadSamples int
	MinImprovement        time.Duration
	Cooldown              time.Duration
	Jitter                time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		TestURL:               "http://cp.cloudflare.com/generate_204",
		SwitchPolicy:          SwitchFailover,
		Interval:              1 * time.Minute,
		Timeout:               5 * time.Second,
		Concurrency:           3,
		Threshold:             800 * time.Millisecond,
		LatencyAlertThreshold: 250 * time.Millisecond,
		WindowSize:            5,
		MinCandidateSamples:   3,
		ConsecutiveBadSamples: 3,
		MinImprovement:        100 * time.Millisecond,
		Cooldown:              5 * time.Minute,
		Jitter:                10 * time.Second,
	}
}

func (p Policy) Validate() error {
	parsed, err := url.Parse(p.TestURL)
	if err != nil || strings.ContainsAny(p.TestURL, "\x00\r\n#") || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("monitor policy: test URL must use HTTP or HTTPS without credentials or fragment")
	}
	if p.SwitchPolicy != SwitchFailover && p.SwitchPolicy != SwitchLowestLatency {
		return fmt.Errorf("monitor policy: switch policy must be %q or %q", SwitchFailover, SwitchLowestLatency)
	}
	if p.Interval <= 0 {
		return fmt.Errorf("monitor policy: interval must be positive")
	}
	if p.Timeout <= 0 || p.Timeout > p.Interval {
		return fmt.Errorf("monitor policy: timeout must be positive and no greater than interval")
	}
	if p.Concurrency < 1 || p.Concurrency > 64 {
		return fmt.Errorf("monitor policy: concurrency must be between 1 and 64")
	}
	if p.Threshold <= 0 || p.LatencyAlertThreshold <= 0 {
		return fmt.Errorf("monitor policy: latency thresholds must be positive")
	}
	if p.WindowSize < 1 || p.ConsecutiveBadSamples < 1 || p.ConsecutiveBadSamples > p.WindowSize {
		return fmt.Errorf("monitor policy: unhealthy sample count must fit within a positive window")
	}
	if p.MinCandidateSamples < 2 || p.MinCandidateSamples > p.WindowSize {
		return fmt.Errorf("monitor policy: candidate sample count must be between 2 and the window size")
	}
	if p.MinImprovement <= 0 {
		return fmt.Errorf("monitor policy: minimum improvement must be positive")
	}
	if p.Cooldown < 0 || p.Jitter < 0 || p.Jitter >= p.Interval {
		return fmt.Errorf("monitor policy: cooldown must be nonnegative and jitter must be nonnegative and less than interval")
	}
	return nil
}

const (
	ReasonManualOverride      = "manual override active"
	ReasonAutomationDisabled  = "automation disabled"
	ReasonSwitchInFlight      = "switch already in flight"
	ReasonCooldown            = "switch cooldown active"
	ReasonAllCandidatesFailed = "all candidates failed"
	ReasonInsufficientSamples = "insufficient samples"
	ReasonSelectedHealthy     = "selected proxy is healthy"
	ReasonNoBetterCandidate   = "no materially better candidate"
	ReasonSwitchCandidate     = "materially better candidate"
)

func Decide(policy Policy, state GroupState, samples []Sample, now time.Time) Decision {
	decision := Decision{Old: state.Selected, Reason: ReasonInsufficientSamples}
	groupSamples := make([]Sample, 0, len(samples))
	byProxy := make(map[string][]Sample)
	for _, sample := range samples {
		if sample.Group != state.Group || sample.Proxy == "" || (policy.TestURL != "" && sample.URL != policy.TestURL) {
			continue
		}
		groupSamples = append(groupSamples, sample)
		byProxy[sample.Proxy] = append(byProxy[sample.Proxy], sample)
	}
	decision.Evidence = groupSamples

	if state.ManualOverride {
		decision.Reason = ReasonManualOverride
		return decision
	}
	if !state.AutomationEnabled {
		decision.Reason = ReasonAutomationDisabled
		return decision
	}
	if state.SwitchInFlight {
		decision.Reason = ReasonSwitchInFlight
		return decision
	}
	if state.LastSwitchAt.After(now) || (!state.LastSwitchAt.IsZero() && now.Sub(state.LastSwitchAt) < policy.Cooldown) {
		decision.Reason = ReasonCooldown
		return decision
	}
	if state.Selected == "" || len(state.Proxies) == 0 {
		return decision
	}
	if allCandidatesFailed(state.Proxies, byProxy, policy.WindowSize) {
		decision.Reason = ReasonAllCandidatesFailed
		return decision
	}

	window := recent(byProxy[state.Selected], policy.WindowSize)
	comparable := window
	if policy.SwitchPolicy == SwitchLowestLatency {
		// The fastest node wins while it still answers, so the selected node's own
		// health is irrelevant; it is only compared against the candidates.
		if len(window) < policy.MinCandidateSamples {
			return decision
		}
	} else {
		if len(window) < policy.ConsecutiveBadSamples {
			return decision
		}
		bad := 0
		for i := len(window) - 1; i >= 0 && bad < policy.ConsecutiveBadSamples; i-- {
			if unhealthy(window[i], policy.Threshold) {
				bad++
				continue
			}
			break
		}
		if bad < policy.ConsecutiveBadSamples {
			decision.Reason = ReasonSelectedHealthy
			return decision
		}
		comparable = window[len(window)-bad:]
	}

	selectedMedian, selectedCount := successfulMedian(comparable)
	selectedOffline := selectedCount == 0
	baseline := selectedMedian

	bestName := ""
	bestMedian := time.Duration(0)
	for _, proxy := range state.Proxies {
		if proxy == state.Selected {
			continue
		}
		candidate := recent(byProxy[proxy], policy.WindowSize)
		median, count := successfulMedian(candidate)
		if len(candidate) == 0 || sampleFailed(candidate[len(candidate)-1]) || count < policy.MinCandidateSamples || (!selectedOffline && baseline-median < policy.MinImprovement) {
			continue
		}
		if bestName == "" || median < bestMedian || (median == bestMedian && proxy < bestName) {
			bestName, bestMedian = proxy, median
		}
	}
	if bestName == "" {
		decision.Reason = ReasonNoBetterCandidate
		return decision
	}
	decision.Switch = true
	decision.New = bestName
	decision.Reason = ReasonSwitchCandidate
	return decision
}

func allCandidatesFailed(proxies []string, byProxy map[string][]Sample, windowSize int) bool {
	if len(proxies) == 0 {
		return false
	}
	for _, proxy := range proxies {
		samples := recent(byProxy[proxy], windowSize)
		if len(samples) == 0 || !sampleFailed(samples[len(samples)-1]) {
			return false
		}
	}
	return true
}

func recent(samples []Sample, limit int) []Sample {
	ordered := append([]Sample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].FinishedAt.Before(ordered[j].FinishedAt)
	})
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[len(ordered)-limit:]
	}
	return ordered
}

func successfulMedian(samples []Sample) (time.Duration, int) {
	latencies := make([]time.Duration, 0, len(samples))
	for _, sample := range samples {
		if !sampleFailed(sample) {
			latencies = append(latencies, sample.Latency)
		}
	}
	if len(latencies) == 0 {
		return 0, 0
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	middle := len(latencies) / 2
	if len(latencies)%2 == 1 {
		return latencies[middle], len(latencies)
	}
	return latencies[middle-1] + (latencies[middle]-latencies[middle-1])/2, len(latencies)
}

func sampleFailed(sample Sample) bool {
	return sample.Outcome != OutcomeSuccess || sample.Latency <= 0
}

func unhealthy(sample Sample, threshold time.Duration) bool {
	return sampleFailed(sample) || sample.Latency > threshold
}

// UpdateLatencyAlert emits only state transitions. A failed, timed-out, or missing
// measurement is unknown rather than evidence of high latency or recovery.
func UpdateLatencyAlert(policy Policy, state GroupState, samples []Sample, previous AlertState) (AlertState, AlertEvent) {
	event := AlertEvent{Group: state.Group, Threshold: policy.LatencyAlertThreshold}
	if len(state.Proxies) == 0 {
		return previous, event
	}
	byProxy := make(map[string][]Sample, len(state.Proxies))
	for _, sample := range samples {
		if sample.Group == state.Group && sample.Proxy != "" && (policy.TestURL == "" || sample.URL == policy.TestURL) {
			byProxy[sample.Proxy] = append(byProxy[sample.Proxy], sample)
		}
	}
	latest := make([]Sample, 0, len(state.Proxies))
	allHigh := true
	anyRecovered := false
	complete := true
	for _, proxy := range state.Proxies {
		proxySamples := recent(byProxy[proxy], 1)
		if len(proxySamples) == 0 || sampleFailed(proxySamples[0]) {
			complete = false
			continue
		}
		sample := proxySamples[0]
		latest = append(latest, sample)
		if sample.Latency <= policy.LatencyAlertThreshold {
			allHigh = false
			anyRecovered = true
		}
	}
	event.Evidence = latest
	if previous.HighLatency && anyRecovered {
		event.Transition = AlertRecovered
		return AlertState{}, event
	}
	if complete && allHigh && !previous.HighLatency {
		event.Transition = AlertHighLatency
		return AlertState{HighLatency: true}, event
	}
	return previous, event
}
