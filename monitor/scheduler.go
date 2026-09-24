package monitor

import (
	"context"
	"errors"
	"fmt"
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

type Scheduler struct {
	policy  Policy
	prober  DelayProber
	onBatch func(Batch)

	mu         sync.Mutex
	groups     []GroupState
	generation uint64
	requests   map[string]struct{}
	wake       chan struct{}
	started    bool
}

func NewScheduler(policy Policy, prober DelayProber, onBatch func(Batch)) (*Scheduler, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if prober == nil {
		return nil, fmt.Errorf("monitor scheduler: delay prober is required")
	}
	if onBatch == nil {
		return nil, fmt.Errorf("monitor scheduler: batch handler is required")
	}
	return &Scheduler{
		policy:   policy,
		prober:   prober,
		onBatch:  onBatch,
		requests: make(map[string]struct{}),
		wake:     make(chan struct{}, 1),
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
		}

		var timerC <-chan time.Time
		deadline := time.Time{}
		if !active {
			deadline = nextDeadline(runtimes)
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

		select {
		case <-runCtx.Done():
			return runCtx.Err()
		case <-s.wake:
			refresh()
		case <-timerC:
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
