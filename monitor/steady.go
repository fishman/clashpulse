package monitor

import (
	"sort"
	"time"
)

// DecideSteady leaves a node only after its own downloads break, and only for
// a node with proven completed downloads; speed breaks ties, never forces.
func DecideSteady(policy Policy, state GroupState, latency []Sample, streams map[string][]StreamSample, now time.Time) Decision {
	decision := Decision{Group: state.Group, Old: state.Selected, Reason: ReasonInsufficientSamples}
	if reason := guard(policy, state, now); reason != "" {
		decision.Reason = reason
		return decision
	}
	if state.Selected == "" || len(state.Proxies) == 0 {
		return decision
	}
	windows := make(map[string][]StreamSample, len(state.Proxies))
	allFailed, measured := true, 0
	for _, proxy := range state.Proxies {
		window := recentStreams(streams[proxy], policy.WindowSize)
		windows[proxy] = window
		if len(window) == 0 {
			continue
		}
		measured++
		if window[len(window)-1].Completed() {
			allFailed = false
		}
	}
	if measured > 1 && allFailed {
		decision.Reason = ReasonAllCandidatesFailed
		return decision
	}
	selected := MeasureStreams(state.Selected, windows[state.Selected], 0)
	interrupted := selected.Samples - selected.Completed
	if interrupted < policy.MaxFailures {
		if selected.Samples > 0 {
			decision.Reason = ReasonSelectedStreamsHealthy
		}
		return decision
	}
	latencyByProxy := make(map[string]time.Duration)
	byProxy := make(map[string][]Sample)
	for _, sample := range latency {
		if sample.Group == state.Group {
			byProxy[sample.Proxy] = append(byProxy[sample.Proxy], sample)
		}
	}
	for proxy, samples := range byProxy {
		latencyByProxy[proxy] = MeasureStability(recent(samples, policy.WindowSize), 0).Median
	}
	type candidate struct {
		name  string
		stats StreamStability
	}
	var eligible []candidate
	for _, proxy := range state.Proxies {
		window := windows[proxy]
		if proxy == state.Selected || len(window) == 0 || !window[len(window)-1].Completed() {
			continue
		}
		stats := MeasureStreams(proxy, window, 0)
		if stats.Completed < policy.MinCandidateSamples || stats.Samples-stats.Completed >= interrupted {
			continue
		}
		eligible = append(eligible, candidate{proxy, stats})
	}
	if len(eligible) == 0 {
		decision.Reason = ReasonNoBetterCandidate
		return decision
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if fa, fb := a.stats.Samples-a.stats.Completed, b.stats.Samples-b.stats.Completed; fa != fb {
			return fa < fb
		}
		if a.stats.MedianThroughput != b.stats.MedianThroughput {
			return a.stats.MedianThroughput > b.stats.MedianThroughput
		}
		la, lb := latencyByProxy[a.name], latencyByProxy[b.name]
		if la != lb && la > 0 && lb > 0 {
			return la < lb
		}
		return a.name < b.name
	})
	best := eligible[0].name
	decision.Switch, decision.New, decision.Reason = true, best, ReasonSteadierStream
	decision.Streams = append(append([]StreamSample(nil), windows[state.Selected]...), windows[best]...)
	for _, sample := range latency {
		if sample.Group == state.Group && (sample.Proxy == state.Selected || sample.Proxy == best) {
			decision.Evidence = append(decision.Evidence, sample)
		}
	}
	return decision
}

func recentStreams(samples []StreamSample, limit int) []StreamSample {
	out := append([]StreamSample(nil), samples...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].FinishedAt.Before(out[j].FinishedAt) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// MeasureStreams summarizes the last windowSize pulls (0 = all).
func MeasureStreams(proxy string, samples []StreamSample, windowSize int) StreamStability {
	window := recentStreams(samples, windowSize)
	stats := StreamStability{Proxy: proxy, Samples: len(window)}
	var rates []int64
	for _, sample := range window {
		if sample.Completed() {
			stats.Completed++
			rates = append(rates, sample.Throughput())
		}
	}
	if len(rates) > 0 {
		sort.Slice(rates, func(i, j int) bool { return rates[i] < rates[j] })
		stats.MedianThroughput = rates[len(rates)/2]
	}
	return stats
}
