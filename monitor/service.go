package monitor

import (
	"context"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/fishman/clashpulse/services"
)

// MaxServiceCandidates bounds one search: a service blocked on every checked
// candidate reports exhaustion rather than walking a whole provider.
const MaxServiceCandidates = 8

// maxSearchWave bounds the candidates checked concurrently in one search wave.
const maxSearchWave = 4

type ServiceChecker interface {
	// CheckServices checks ids on one node and returns one result per id.
	CheckServices(ctx context.Context, proxy string, ids []string) []services.Result
}

type ServiceTarget struct {
	ID        string
	Group     string
	Selected  string
	Members   []string
	Interval  time.Duration
	Failures  int
	Automatic bool
	Regions   []string
}

type ServicePlan struct {
	Targets   []ServiceTarget
	Latency   map[string]time.Duration // proxy -> median latency
	Unhealthy map[string]bool          // proxy -> latest latency probe failed
	Cooldown  time.Duration
	Slots     int
}

type ServiceCheck struct {
	Service, Proxy string
	Result         services.Result
	At             time.Time
}

type ServiceSwitch struct {
	Service, Group, Old, New string
	Region                   string
	Reason                   string // "blocked", "failing"
}

type ServiceBatch struct {
	Checks    []ServiceCheck
	Switches  []ServiceSwitch
	Exhausted []string // services whose search found no usable candidate
	Next      map[string]time.Time
}

// ServiceScheduler owns service check timing and search. One loop holds one
// timer; check calls run on at most plan.Slots workers per round.
type ServiceScheduler struct {
	checker ServiceChecker
	onBatch func(ServiceBatch)

	mu         sync.Mutex
	plan       ServicePlan
	requested  map[string]bool
	wake       chan struct{}
	timerArmed bool
}

type serviceRuntime struct {
	target    ServiceTarget
	next      time.Time
	recent    []services.Outcome // selected node, newest last, bounded to Failures
	last      map[string]services.Result
	coolUntil time.Time
}

func NewServiceScheduler(checker ServiceChecker, onBatch func(ServiceBatch)) *ServiceScheduler {
	return &ServiceScheduler{checker: checker, onBatch: onBatch, requested: make(map[string]bool), wake: make(chan struct{}, 1)}
}

func (s *ServiceScheduler) SetPlan(plan ServicePlan) {
	s.mu.Lock()
	s.plan = plan
	s.mu.Unlock()
	s.signal()
}

// Request makes a service due now.
func (s *ServiceScheduler) Request(service string) {
	s.mu.Lock()
	s.requested[service] = true
	s.mu.Unlock()
	s.signal()
}

func (s *ServiceScheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ServiceScheduler) armed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timerArmed
}

// ServiceFailing reports whether the selected node's last target.Failures
// outcomes all count as failures. A check error counts only while the node's
// latency probe also fails, so a flaky check site never moves a healthy node.
func ServiceFailing(target ServiceTarget, recent []services.Outcome, unhealthy bool) bool {
	if target.Failures <= 0 || len(recent) < target.Failures {
		return false
	}
	for _, outcome := range recent[len(recent)-target.Failures:] {
		switch outcome {
		case services.Blocked:
		case services.Failed:
			if !unhealthy {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ServiceCandidates orders the members worth checking: not the selected node,
// not known blocked, not known outside target.Regions; lowest latency first,
// unmeasured last, then by name; at most MaxServiceCandidates.
func ServiceCandidates(target ServiceTarget, last map[string]services.Result, latency map[string]time.Duration) []string {
	var out []string
	for _, member := range target.Members {
		if member == target.Selected {
			continue
		}
		result, checked := last[member]
		if checked && result.Outcome == services.Blocked {
			continue
		}
		if checked && result.Region != "" && len(target.Regions) > 0 && !slices.Contains(target.Regions, result.Region) {
			continue
		}
		out = append(out, member)
	}
	slices.SortStableFunc(out, func(a, b string) int {
		la, oka := latency[a]
		lb, okb := latency[b]
		switch {
		case oka && !okb:
			return -1
		case !oka && okb:
			return 1
		case oka && okb && la != lb:
			if la < lb {
				return -1
			}
			return 1
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	if len(out) > MaxServiceCandidates {
		out = out[:MaxServiceCandidates]
	}
	return out
}

func (s *ServiceScheduler) Run(ctx context.Context) error {
	runtimes := make(map[string]*serviceRuntime)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		s.mu.Lock()
		plan, requested := s.plan, s.requested
		s.requested = make(map[string]bool)
		s.mu.Unlock()
		now := time.Now()
		reconcileServices(runtimes, plan, now)
		for id := range requested {
			if rt := runtimes[id]; rt != nil {
				rt.next = now
			}
		}
		if due := dueByNode(runtimes, plan, now); len(due) > 0 {
			batch := s.round(ctx, plan, runtimes, due)
			if err := ctx.Err(); err != nil {
				return err
			}
			// A plan change during the round drops results for services it
			// removed, and switches whose old node is no longer selected.
			s.mu.Lock()
			current := s.plan
			s.mu.Unlock()
			if batch = currentBatch(batch, current); len(batch.Checks)+len(batch.Switches)+len(batch.Exhausted) > 0 {
				s.onBatch(batch)
			}
			continue
		}
		var deadline time.Time
		for _, rt := range runtimes {
			if deadline.IsZero() || rt.next.Before(deadline) {
				deadline = rt.next
			}
		}
		s.mu.Lock()
		s.timerArmed = !deadline.IsZero()
		s.mu.Unlock()
		var timerC <-chan time.Time
		if !deadline.IsZero() {
			timer.Reset(time.Until(deadline))
			timerC = timer.C
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.wake:
			timer.Stop()
		case <-timerC:
		}
	}
}

func currentBatch(batch ServiceBatch, plan ServicePlan) ServiceBatch {
	selected := make(map[string]string, len(plan.Targets))
	for _, target := range plan.Targets {
		selected[target.ID] = target.Selected
	}
	present := func(id string) bool { _, ok := selected[id]; return ok }
	batch.Checks = slices.DeleteFunc(batch.Checks, func(check ServiceCheck) bool { return !present(check.Service) })
	batch.Switches = slices.DeleteFunc(batch.Switches, func(change ServiceSwitch) bool { return selected[change.Service] != change.Old })
	batch.Exhausted = slices.DeleteFunc(batch.Exhausted, func(id string) bool { return !present(id) })
	maps.DeleteFunc(batch.Next, func(id string, _ time.Time) bool { return !present(id) })
	return batch
}

// reconcileServices adds new targets as due, drops removed ones, and makes a
// target due when its selected node changed.
func reconcileServices(runtimes map[string]*serviceRuntime, plan ServicePlan, now time.Time) {
	present := make(map[string]bool, len(plan.Targets))
	for _, target := range plan.Targets {
		present[target.ID] = true
		rt := runtimes[target.ID]
		if rt == nil {
			runtimes[target.ID] = &serviceRuntime{target: target, next: now, last: make(map[string]services.Result)}
			continue
		}
		if rt.target.Selected != target.Selected {
			rt.next, rt.recent = now, nil
		}
		rt.target = target
	}
	for id := range runtimes {
		if !present[id] {
			delete(runtimes, id)
		}
	}
}

type serviceJob struct {
	proxy string
	ids   []string
}

// dueByNode groups due targets by selected node, in plan order.
func dueByNode(runtimes map[string]*serviceRuntime, plan ServicePlan, now time.Time) []serviceJob {
	var jobs []serviceJob
	index := make(map[string]int)
	for _, target := range plan.Targets {
		rt := runtimes[target.ID]
		if rt == nil || rt.next.After(now) {
			continue
		}
		i, ok := index[target.Selected]
		if !ok {
			i = len(jobs)
			index[target.Selected] = i
			jobs = append(jobs, serviceJob{proxy: target.Selected})
		}
		jobs[i].ids = append(jobs[i].ids, target.ID)
	}
	return jobs
}

func (s *ServiceScheduler) round(ctx context.Context, plan ServicePlan, runtimes map[string]*serviceRuntime, due []serviceJob) ServiceBatch {
	batch := ServiceBatch{Next: make(map[string]time.Time, len(runtimes))}
	results := s.checkAll(ctx, max(1, plan.Slots), due)
	now := time.Now()
	var checked []*serviceRuntime
	for i, job := range due {
		for _, result := range results[i] {
			rt := runtimes[result.Service]
			if rt == nil {
				continue
			}
			batch.Checks = append(batch.Checks, ServiceCheck{Service: result.Service, Proxy: job.proxy, Result: result, At: now})
			rt.last[job.proxy] = result
			rt.recent = append(rt.recent, result.Outcome)
			if n := max(1, rt.target.Failures); len(rt.recent) > n {
				rt.recent = rt.recent[len(rt.recent)-n:]
			}
			rt.next = now.Add(rt.target.Interval + jitter(rt.target.Interval))
			checked = append(checked, rt)
		}
	}
	for _, rt := range checked {
		if !rt.target.Automatic || now.Before(rt.coolUntil) || !ServiceFailing(rt.target, rt.recent, plan.Unhealthy[rt.target.Selected]) {
			continue
		}
		if sw, ok := s.search(ctx, plan, rt, &batch); ok {
			batch.Switches = append(batch.Switches, sw)
		} else if ctx.Err() == nil {
			batch.Exhausted = append(batch.Exhausted, rt.target.ID)
		}
	}
	for id, rt := range runtimes {
		batch.Next[id] = rt.next
	}
	return batch
}

// search checks candidates in waves and picks the lowest-latency usable node
// of the first wave that has one.
func (s *ServiceScheduler) search(ctx context.Context, plan ServicePlan, rt *serviceRuntime, batch *ServiceBatch) (ServiceSwitch, bool) {
	candidates := ServiceCandidates(rt.target, rt.last, plan.Latency)
	wave := max(1, min(maxSearchWave, plan.Slots))
	for start := 0; start < len(candidates) && ctx.Err() == nil; start += wave {
		jobs := make([]serviceJob, 0, wave)
		for _, proxy := range candidates[start:min(start+wave, len(candidates))] {
			jobs = append(jobs, serviceJob{proxy: proxy, ids: []string{rt.target.ID}})
		}
		results := s.checkAll(ctx, wave, jobs)
		now := time.Now()
		best, bestResult := "", services.Result{}
		for i, job := range jobs {
			for _, result := range results[i] {
				batch.Checks = append(batch.Checks, ServiceCheck{Service: rt.target.ID, Proxy: job.proxy, Result: result, At: now})
				rt.last[job.proxy] = result
				if result.Outcome != services.Usable || best != "" && !lowerLatency(plan.Latency, job.proxy, best) {
					continue
				}
				best, bestResult = job.proxy, result
			}
		}
		if best == "" {
			continue
		}
		reason := "failing"
		if rt.recent[len(rt.recent)-1] == services.Blocked {
			reason = "blocked"
		}
		rt.coolUntil, rt.recent = now.Add(plan.Cooldown), nil
		return ServiceSwitch{Service: rt.target.ID, Group: rt.target.Group, Old: rt.target.Selected, New: best, Region: bestResult.Region, Reason: reason}, true
	}
	return ServiceSwitch{}, false
}

// lowerLatency reports whether a is strictly faster than b; unmeasured is slowest.
func lowerLatency(latency map[string]time.Duration, a, b string) bool {
	la, oka := latency[a]
	lb, okb := latency[b]
	return oka && (!okb || la < lb)
}

// checkAll runs jobs on at most slots workers and returns results by job index.
func (s *ServiceScheduler) checkAll(ctx context.Context, slots int, jobs []serviceJob) [][]services.Result {
	results := make([][]services.Result, len(jobs))
	var next sync.Mutex
	cursor := 0
	var wg sync.WaitGroup
	for range min(slots, len(jobs)) {
		wg.Go(func() {
			for {
				next.Lock()
				i := cursor
				cursor++
				next.Unlock()
				if i >= len(jobs) || ctx.Err() != nil {
					return
				}
				results[i] = s.checker.CheckServices(ctx, jobs[i].proxy, jobs[i].ids)
			}
		})
	}
	wg.Wait()
	return results
}

func jitter(interval time.Duration) time.Duration {
	if interval < 10 {
		return 0
	}
	return rand.N(interval / 10)
}
