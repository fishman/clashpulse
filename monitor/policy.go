package monitor

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/fishman/clashpulse/core"
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
	MaxFailures           int
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
		MaxFailures:           2,
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
	if p.WindowSize < core.MonitorWindowMin || p.ConsecutiveBadSamples < 1 || p.ConsecutiveBadSamples > p.WindowSize {
		return fmt.Errorf("monitor policy: unhealthy sample count must fit within a window of at least %d", core.MonitorWindowMin)
	}
	if p.MinCandidateSamples < core.MonitorWindowMin || p.MinCandidateSamples > p.WindowSize {
		return fmt.Errorf("monitor policy: candidate sample count must be between %d and the window size", core.MonitorWindowMin)
	}
	if p.MaxFailures < 1 || p.MaxFailures > p.WindowSize {
		return fmt.Errorf("monitor policy: max failures must be between 1 and the window size")
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
	selected := MeasureStability(window, 0)
	comparable := window
	// unstable marks a selected node that answers fast but fails too many of
	// its recent probes; a replacement must be steadier, not faster.
	unstable := false
	if policy.SwitchPolicy == SwitchLowestLatency {
		// The fastest node wins while it still answers, so the selected node's own
		// health is irrelevant; it is only compared against the candidates.
		if len(window) < policy.MinCandidateSamples {
			return decision
		}
	} else {
		if len(window) < policy.ConsecutiveBadSamples && selected.Failures < policy.MaxFailures {
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
		switch {
		case bad >= policy.ConsecutiveBadSamples:
			comparable = window[len(window)-bad:]
		case selected.Failures >= policy.MaxFailures:
			unstable = true
		default:
			decision.Reason = ReasonSelectedHealthy
			return decision
		}
	}

	baseline := MeasureStability(comparable, 0)
	selectedOffline := baseline.Failures == baseline.Samples

	bestName := ""
	var best Stability
	for _, proxy := range state.Proxies {
		if proxy == state.Selected {
			continue
		}
		candidate := recent(byProxy[proxy], policy.WindowSize)
		if len(candidate) == 0 || sampleFailed(candidate[len(candidate)-1]) {
			continue
		}
		stats := MeasureStability(candidate, 0)
		if stats.Samples-stats.Failures < policy.MinCandidateSamples {
			continue
		}
		switch {
		case selectedOffline:
		case unstable:
			if stats.Failures >= selected.Failures || stats.Median > policy.Threshold {
				continue
			}
		default:
			// A candidate that is itself unstable never replaces a node that answers.
			if stats.Failures >= policy.MaxFailures || baseline.Median-stats.Median < policy.MinImprovement {
				continue
			}
		}
		if bestName == "" || steadier(stats, proxy, best, bestName) {
			bestName, best = proxy, stats
		}
	}
	if bestName == "" {
		decision.Reason = ReasonNoBetterCandidate
		return decision
	}
	decision.LowerLatency = !selectedOffline && !unstable && baseline.Samples-baseline.Failures >= policy.MinCandidateSamples && !sampleFailed(comparable[len(comparable)-1])
	decision.Switch = true
	decision.New = bestName
	decision.Reason = ReasonSwitchCandidate
	return decision
}

// steadier ranks candidates by failed probes, then median latency, then jitter,
// so a fast node that drops probes never outranks a stable one.
func steadier(a Stability, aName string, b Stability, bName string) bool {
	if a.Failures != b.Failures {
		return a.Failures < b.Failures
	}
	if a.Median != b.Median {
		return a.Median < b.Median
	}
	if a.Jitter != b.Jitter {
		return a.Jitter < b.Jitter
	}
	return aName < bName
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

// Stability summarizes one proxy's most recent probes. Median and Jitter cover
// successful probes only; Jitter is their median absolute deviation.
type Stability struct {
	Samples  int
	Failures int
	Median   time.Duration
	Jitter   time.Duration
}

// MeasureStability summarizes the newest windowSize samples, or all samples
// when windowSize is zero.
func MeasureStability(samples []Sample, windowSize int) Stability {
	window := recent(samples, windowSize)
	stats := Stability{Samples: len(window)}
	latencies := make([]time.Duration, 0, len(window))
	for _, sample := range window {
		if sampleFailed(sample) {
			stats.Failures++
			continue
		}
		latencies = append(latencies, sample.Latency)
	}
	if len(latencies) == 0 {
		return stats
	}
	stats.Median = median(latencies)
	for i, latency := range latencies {
		latencies[i] = (latency - stats.Median).Abs()
	}
	stats.Jitter = median(latencies)
	return stats
}

func median(values []time.Duration) time.Duration {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return values[middle-1] + (values[middle]-values[middle-1])/2
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
