package monitor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type DelayProber interface {
	Delay(context.Context, string, string, time.Duration) (time.Duration, error)
}

type StreamProber interface {
	StreamPull(ctx context.Context, proxy, url string) StreamSample
}

// DialFailureProbeSpacing bounds early probes triggered by real-traffic dial
// failures, so an outage cannot turn into a continuous probe storm.
const DialFailureProbeSpacing = 15 * time.Second

type Scheduler struct {
	policy   Policy
	prober   DelayProber
	streamer StreamProber
	onBatch  func(Batch)
	now      func() time.Time

	mu            sync.Mutex
	groups        []GroupState
	generation    uint64
	requests      map[string]struct{}
	dialFailures  map[string]int
	lastDialProbe map[string]time.Time
	members       map[string]bool
	wake          chan struct{}
	started       bool
}

func NewScheduler(policy Policy, prober DelayProber, streamer StreamProber, onBatch func(Batch)) (*Scheduler, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if prober == nil {
		return nil, fmt.Errorf("monitor scheduler: delay prober is required")
	}
	if policy.SwitchPolicy == SwitchSteadyStream && streamer == nil {
		return nil, fmt.Errorf("monitor scheduler: stream prober is required for %s", SwitchSteadyStream)
	}
	if onBatch == nil {
		return nil, fmt.Errorf("monitor scheduler: batch handler is required")
	}
	return &Scheduler{
		policy:        policy,
		prober:        prober,
		streamer:      streamer,
		onBatch:       onBatch,
		now:           time.Now,
		requests:      make(map[string]struct{}),
		dialFailures:  make(map[string]int),
		lastDialProbe: make(map[string]time.Time),
		wake:          make(chan struct{}, 1),
	}, nil
}

// SetGroups replaces the monitored group snapshot. Only opted-in groups are
// scheduled periodically; a manual Request can probe any group in the snapshot.
func (s *Scheduler) SetGroups(groups []GroupState) error {
	copyOfGroups := make([]GroupState, len(groups))
	seen := make(map[string]struct{}, len(groups))
	for i, group := range groups {
		if strings.TrimSpace(group.Group) == "" {
			return fmt.Errorf("monitor scheduler: group name is required")
		}
		if _, ok := seen[group.Group]; ok {
			return fmt.Errorf("monitor scheduler: duplicate group %q", group.Group)
		}
		seen[group.Group] = struct{}{}
		if group.Selected == "" {
			return fmt.Errorf("monitor scheduler: selected proxy is required for group %q", group.Group)
		}
		proxySeen := make(map[string]struct{}, len(group.Proxies))
		selectedFound := false
		for _, proxy := range group.Proxies {
			if strings.TrimSpace(proxy) == "" {
				return fmt.Errorf("monitor scheduler: blank proxy in group %q", group.Group)
			}
			if _, ok := proxySeen[proxy]; ok {
				return fmt.Errorf("monitor scheduler: duplicate proxy %q in group %q", proxy, group.Group)
			}
			proxySeen[proxy] = struct{}{}
			selectedFound = selectedFound || proxy == group.Selected
		}
		if !selectedFound {
			return fmt.Errorf("monitor scheduler: selected proxy %q is absent from group %q", group.Selected, group.Group)
		}
		if group.AutomationEnabled && !group.ProbeEnabled {
			return fmt.Errorf("monitor scheduler: automated group %q must enable probes", group.Group)
		}
		group.Proxies = append([]string(nil), group.Proxies...)
		copyOfGroups[i] = group
	}

	s.mu.Lock()
	if reflect.DeepEqual(s.groups, copyOfGroups) {
		s.mu.Unlock()
		return nil
	}
	s.groups = copyOfGroups
	s.generation++
	s.mu.Unlock()
	s.signal()
	return nil
}

// Request schedules an immediate probe without enabling automatic switching.
func (s *Scheduler) Request(group string) error {
	s.mu.Lock()
	found := false
	for _, current := range s.groups {
		if current.Group == group {
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("monitor scheduler: unknown group %q", group)
	}
	s.requests[group] = struct{}{}
	s.mu.Unlock()
	s.signal()
	return nil
}

// ProbedGroups returns the names of the currently probe-enabled groups.
func (s *Scheduler) ProbedGroups() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for _, group := range s.groups {
		if group.ProbeEnabled {
			names = append(names, group.Group)
		}
	}
	return names
}

// SetStreamMembers replaces the set of nodes stream rounds may pull: the
// probe group's members, the only nodes reachable through its listener.
func (s *Scheduler) SetStreamMembers(names []string) {
	members := make(map[string]bool, len(names))
	for _, name := range names {
		members[name] = true
	}
	s.mu.Lock()
	if maps.Equal(s.members, members) {
		s.mu.Unlock()
		return
	}
	s.members = members
	s.mu.Unlock()
	s.signal()
}

// ReportDialFailure records a failed real-traffic dial through a probed group.
// MaxFailures failures since the group's last probe force an early probe, at
// most once per DialFailureProbeSpacing; the probe result is the evidence.
func (s *Scheduler) ReportDialFailure(group string) {
	s.mu.Lock()
	probed := false
	for _, current := range s.groups {
		if current.Group == group {
			probed = current.ProbeEnabled
			break
		}
	}
	if !probed {
		s.mu.Unlock()
		return
	}
	s.dialFailures[group]++
	now := s.now()
	if s.dialFailures[group] < s.policy.MaxFailures || now.Sub(s.lastDialProbe[group]) < DialFailureProbeSpacing {
		s.mu.Unlock()
		return
	}
	s.lastDialProbe[group] = now
	s.requests[group] = struct{}{}
	s.mu.Unlock()
	s.signal()
}

// Run owns the sole scheduling timer and a fixed-size worker pool. onBatch runs
// on the scheduler goroutine and should enqueue quickly rather than do I/O.
func (s *Scheduler) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("monitor scheduler: Run may only be called once")
	}
	s.started = true
	s.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	jobs := make(chan probeTask, s.policy.Concurrency)
	results := make(chan probeResult, s.policy.Concurrency)
	var workers sync.WaitGroup
	workers.Add(s.policy.Concurrency)
	for range s.policy.Concurrency {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-runCtx.Done():
					return
				case task := <-jobs:
					probeCtx, probeCancel := context.WithTimeout(runCtx, s.policy.Timeout)
					latency, err := s.prober.Delay(probeCtx, task.proxy, s.policy.TestURL, s.policy.Timeout)
					probeCancel()
					sample := Sample{
						Group:      task.group,
						Proxy:      task.proxy,
						URL:        s.policy.TestURL,
						FinishedAt: time.Now(),
						Latency:    latency,
						Outcome:    OutcomeSuccess,
					}
					if err != nil || latency <= 0 {
						sample.Outcome = OutcomeError
						if errors.Is(err, context.DeadlineExceeded) || probeCtx.Err() == context.DeadlineExceeded {
							sample.Outcome = OutcomeTimeout
						}
						sample.Latency = 0
					}
					select {
					case results <- probeResult{task: task, sample: sample}:
					case <-runCtx.Done():
						return
					}
				}
			}
		}()
	}
	steady := s.policy.SwitchPolicy == SwitchSteadyStream
	var streamJobs chan string
	var streamResults chan StreamSample
	if steady {
		streamJobs = make(chan string)
		streamResults = make(chan StreamSample, 1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-runCtx.Done():
					return
				case proxy := <-streamJobs:
					sample := s.streamer.StreamPull(runCtx, proxy, s.policy.StreamURL)
					select {
					case streamResults <- sample:
					case <-runCtx.Done():
						return
					}
				}
			}
		}()
	}
	defer func() {
		cancel()
		workers.Wait()
	}()

	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	runtimes := make(map[string]*groupRuntime)
	forced := make(map[string]bool)
	var pending []probeTask
	active := false
	remaining := make(map[string]int)
	activeGroups := make([]string, 0)
	batchSamples := make([]Sample, 0)
	var batchGeneration uint64
	var streamNext time.Time
	streamHistory := make(map[string][]StreamSample)
	var streamPending []string
	streamActive := false
	streamInFlight := 0
	var streamSamples []StreamSample
	var streamGeneration uint64
	streamRound := 0

	refresh := func() {
		s.mu.Lock()
		inputs := cloneGroups(s.groups)
		requests := s.requests
		s.requests = make(map[string]struct{})
		s.mu.Unlock()

		present := make(map[string]struct{}, len(inputs))
		for _, state := range inputs {
			present[state.Group] = struct{}{}
			runtime := runtimes[state.Group]
			if runtime == nil {
				runtime = &groupRuntime{history: make(map[string][]Sample)}
				runtimes[state.Group] = runtime
				if state.ProbeEnabled {
					runtime.next = time.Now()
				}
			} else {
				if !reflect.DeepEqual(runtime.state, state) && state.ProbeEnabled {
					forced[state.Group] = true
				}
				if state.ProbeEnabled && !runtime.state.ProbeEnabled {
					runtime.next = time.Now()
				}
				if !state.ProbeEnabled {
					runtime.next = time.Time{}
					runtime.history = make(map[string][]Sample)
					runtime.alert = AlertState{}
				}
			}
			runtime.state = state
		}
		for name := range runtimes {
			if _, ok := present[name]; !ok {
				delete(runtimes, name)
				delete(forced, name)
			}
		}
		if steady {
			automated := false
			for _, runtime := range runtimes {
				automated = automated || runtime.state.AutomationEnabled
			}
			if !automated {
				streamNext = time.Time{}
			} else if streamNext.IsZero() {
				streamNext = time.Now()
			}
		}
		for name := range requests {
			if runtimes[name] != nil {
				forced[name] = true
			}
		}
	}
	refresh()

	for {
		if err := runCtx.Err(); err != nil {
			return err
		}
		if !active {
			pending = pending[:0]
			activeGroups = activeGroups[:0]
			remaining = make(map[string]int)
			batchSamples = batchSamples[:0]
			s.mu.Lock()
			batchGeneration = s.generation
			s.mu.Unlock()
			for _, name := range sortedGroupNames(runtimes) {
				runtime := runtimes[name]
				due := runtime.state.ProbeEnabled && !runtime.next.IsZero() && !time.Now().Before(runtime.next)
				if !due && !forced[name] {
					continue
				}
				delete(forced, name)
				s.mu.Lock()
				delete(s.dialFailures, name)
				s.mu.Unlock()
				if runtime.state.ProbeEnabled {
					runtime.next = time.Now().Add(s.policy.Interval + s.jitter())
				}
				activeGroups = append(activeGroups, name)
				remaining[name] = len(runtime.state.Proxies)
				for _, proxy := range runtime.state.Proxies {
					pending = append(pending, probeTask{group: name, proxy: proxy})
				}
			}
			active = len(activeGroups) > 0
			if steady && !active && !streamActive && !streamNext.IsZero() && !time.Now().Before(streamNext) {
				groups := make([]GroupState, 0, len(runtimes))
				histories := make(map[string]map[string][]Sample, len(runtimes))
				for _, name := range sortedGroupNames(runtimes) {
					groups = append(groups, runtimes[name].state)
					histories[name] = runtimes[name].history
				}
				s.mu.Lock()
				members := s.members
				streamGeneration = s.generation
				s.mu.Unlock()
				streamPending = streamTargets(groups, histories, members, s.policy.WindowSize, streamRound)
				streamRound++
				streamNext = time.Now().Add(s.policy.StreamInterval + s.jitter())
				streamSamples = nil
				streamActive = len(streamPending) > 0
			}
		}

		var timerC <-chan time.Time
		deadline := time.Time{}
		if !active {
			deadline = nextDeadline(runtimes)
			if steady && !streamActive && !streamNext.IsZero() && (deadline.IsZero() || streamNext.Before(deadline)) {
				deadline = streamNext
			}
		}
		if deadline.IsZero() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		} else {
			delay := time.Until(deadline)
			if delay < 0 {
				delay = 0
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
			timerC = timer.C
		}

		var send chan probeTask
		var nextTask probeTask
		if len(pending) > 0 {
			send = jobs
			nextTask = pending[0]
		}
		var streamSend chan string
		var nextStream string
		if len(streamPending) > 0 {
			streamSend = streamJobs
			nextStream = streamPending[0]
		}

		select {
		case <-runCtx.Done():
			return runCtx.Err()
		case <-s.wake:
			refresh()
		case <-timerC:
		case streamSend <- nextStream:
			streamPending = streamPending[1:]
			streamInFlight++
		case sample := <-streamResults:
			streamInFlight--
			now := time.Now()
			cutoff := now.Add(-6 * s.policy.StreamInterval)
			streamSamples = append(streamSamples, sample)
			streamHistory[sample.Proxy] = pruneStreams(append(streamHistory[sample.Proxy], sample), cutoff, s.policy.WindowSize)
			if len(streamPending) > 0 || streamInFlight > 0 {
				break
			}
			streamActive = false
			s.mu.Lock()
			validBatch := streamGeneration == s.generation
			s.mu.Unlock()
			if !validBatch {
				break
			}
			for proxy, samples := range streamHistory {
				if samples = pruneStreams(samples, cutoff, s.policy.WindowSize); len(samples) == 0 {
					delete(streamHistory, proxy)
				} else {
					streamHistory[proxy] = samples
				}
			}
			batch := Batch{FinishedAt: now, Streams: streamSamples}
			for _, pulled := range streamSamples {
				batch.StreamStability = append(batch.StreamStability, MeasureStreams(pulled.Proxy, streamHistory[pulled.Proxy], s.policy.WindowSize))
			}
			for _, name := range sortedGroupNames(runtimes) {
				if runtime := runtimes[name]; runtime.state.AutomationEnabled {
					batch.Decisions = append(batch.Decisions, DecideSteady(s.policy, runtime.state, historySamples(runtime), streamHistory, now))
				}
			}
			s.onBatch(batch)
		case send <- nextTask:
			pending = pending[1:]
		case result := <-results:
			batchSamples = append(batchSamples, result.sample)
			if runtime := runtimes[result.task.group]; runtime != nil {
				items := append(runtime.history[result.task.proxy], result.sample)
				if len(items) > s.policy.WindowSize {
					items = append([]Sample(nil), items[len(items)-s.policy.WindowSize:]...)
				}
				runtime.history[result.task.proxy] = items
			}
			remaining[result.task.group]--
			if remaining[result.task.group] == 0 {
				allDone := true
				for _, count := range remaining {
					if count > 0 {
						allDone = false
						break
					}
				}
				if allDone {
					batch := Batch{FinishedAt: time.Now(), Samples: append([]Sample(nil), batchSamples...)}
					s.mu.Lock()
					validBatch := batchGeneration == s.generation
					s.mu.Unlock()
					if !validBatch {
						for _, name := range activeGroups {
							if runtime := runtimes[name]; runtime != nil {
								runtime.history = make(map[string][]Sample)
								runtime.alert = AlertState{}
							}
						}
					}
					for _, name := range activeGroups {
						if !validBatch {
							break
						}
						runtime := runtimes[name]
						if runtime == nil {
							continue
						}
						batch.Decisions = append(batch.Decisions, Decide(s.policy, runtime.state, historySamples(runtime), batch.FinishedAt))
						for _, proxy := range runtime.state.Proxies {
							batch.Stability = append(batch.Stability, ProxyStability{Group: name, Proxy: proxy, Stability: MeasureStability(runtime.history[proxy], s.policy.WindowSize)})
						}
						groupSamples := make([]Sample, 0, len(runtime.state.Proxies))
						for _, sample := range batch.Samples {
							if sample.Group == name {
								groupSamples = append(groupSamples, sample)
							}
						}
						nextAlert, alert := UpdateLatencyAlert(s.policy, runtime.state, groupSamples, runtime.alert)
						runtime.alert = nextAlert
						if alert.Transition != AlertNoChange {
							batch.Alerts = append(batch.Alerts, alert)
						}
					}
					s.onBatch(batch)
					active = false
					refresh()
				}
			}
		}
	}
}

type probeTask struct {
	group string
	proxy string
}

type probeResult struct {
	task   probeTask
	sample Sample
}

type groupRuntime struct {
	state   GroupState
	next    time.Time
	history map[string][]Sample
	alert   AlertState
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) jitter() time.Duration {
	if s.policy.Jitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(s.policy.Jitter) + 1))
}

func cloneGroups(groups []GroupState) []GroupState {
	cloned := make([]GroupState, len(groups))
	for i, group := range groups {
		group.Proxies = append([]string(nil), group.Proxies...)
		cloned[i] = group
	}
	return cloned
}

func sortedGroupNames(groups map[string]*groupRuntime) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func nextDeadline(groups map[string]*groupRuntime) time.Time {
	var next time.Time
	for _, runtime := range groups {
		if !runtime.state.ProbeEnabled || runtime.next.IsZero() {
			continue
		}
		if next.IsZero() || runtime.next.Before(next) {
			next = runtime.next
		}
	}
	return next
}

func historySamples(runtime *groupRuntime) []Sample {
	var samples []Sample
	for _, proxySamples := range runtime.history {
		samples = append(samples, proxySamples...)
	}
	return samples
}

// pruneStreams keeps the last windowSize pulls finished at or after cutoff;
// samples arrive in completion order, so expired ones form a prefix.
func pruneStreams(samples []StreamSample, cutoff time.Time, windowSize int) []StreamSample {
	start := 0
	for start < len(samples) && samples[start].FinishedAt.Before(cutoff) {
		start++
	}
	start = max(start, len(samples)-windowSize)
	if start == 0 {
		return samples
	}
	return append([]StreamSample(nil), samples[start:]...)
}

// streamTargets lists the nodes one stream round pulls: every automated
// group's selected node first, then candidates one per group in turn
// (fastest first), starting at a group that rotates with round, so the cap
// never starves a later group. Deduplicated and capped so one serialized
// round fits its interval.
func streamTargets(groups []GroupState, history map[string]map[string][]Sample, members map[string]bool, windowSize, round int) []string {
	var out []string
	seen := map[string]bool{}
	add := func(proxy string) {
		if members[proxy] && !seen[proxy] && len(out) < StreamPullCap {
			seen[proxy] = true
			out = append(out, proxy)
		}
	}
	for _, group := range groups {
		if group.AutomationEnabled {
			add(group.Selected)
		}
	}
	type ranked struct {
		proxy  string
		median time.Duration
	}
	var candidates [][]ranked
	for _, group := range groups {
		if !group.AutomationEnabled {
			continue
		}
		var fastest []ranked
		for _, proxy := range group.Proxies {
			samples := recent(history[group.Group][proxy], windowSize)
			if proxy == group.Selected || !members[proxy] || len(samples) == 0 || sampleFailed(samples[len(samples)-1]) {
				continue
			}
			fastest = append(fastest, ranked{proxy, MeasureStability(samples, 0).Median})
		}
		sort.SliceStable(fastest, func(i, j int) bool { return fastest[i].median < fastest[j].median })
		candidates = append(candidates, fastest)
	}
	for rank := range StreamCandidates {
		for i := range candidates {
			group := candidates[(round+i)%len(candidates)]
			if rank < len(group) {
				add(group[rank].proxy)
			}
		}
	}
	return out
}
