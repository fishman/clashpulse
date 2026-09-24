package subscriptions

import (
	"context"
	"sync"
	"time"
)

const MaxRefreshWorkers = 8

type schedulerJob struct {
	id       string
	resource bool
	enqueue  func(context.Context) error
}

type schedulerCompletion struct {
	id       string
	resource bool
}

// Scheduler owns one timer and one bounded worker set for subscription and
// application resource work. Resource enqueue callbacks must only queue
// app-owned intent promptly; they must not perform network or disk work.
type Scheduler struct {
	service *Service
	workers int

	mu              sync.Mutex
	cancel          context.CancelFunc
	done            chan struct{}
	resourceNext    func(time.Time) time.Time
	resourceEnqueue func(context.Context) error
	resourceWake    chan struct{}
}

// NewScheduler creates a bounded scheduler. Nonpositive sizes select one
// worker; larger values are capped at MaxRefreshWorkers.
func NewScheduler(service *Service, workers int) *Scheduler {
	if workers <= 0 {
		workers = 1
	}
	if workers > MaxRefreshWorkers {
		workers = MaxRefreshWorkers
	}
	return &Scheduler{
		service:      service,
		workers:      workers,
		resourceWake: make(chan struct{}, 1),
	}
}

// ConfigureResources configures app-owned resource scheduling. next returns
// the next due time, or zero if there is no scheduled work. enqueue is called
// by a scheduler worker and MUST promptly enqueue the app's work intent only.
func (s *Scheduler) ConfigureResources(next func(time.Time) time.Time, enqueue func(context.Context) error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.resourceNext = next
	s.resourceEnqueue = enqueue
	s.mu.Unlock()
	s.WakeResources()
}

// WakeResources asks the timer owner to recalculate the combined next deadline.
func (s *Scheduler) WakeResources() {
	if s == nil {
		return
	}
	select {
	case s.resourceWake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) resourceCallbacks() (func(time.Time) time.Time, func(context.Context) error) {
	s.mu.Lock()
	next, enqueue := s.resourceNext, s.resourceEnqueue
	s.mu.Unlock()
	return next, enqueue
}

// Start launches the timer owner and its fixed workers. A scheduler may be
// restarted after Stop has completed, but concurrent Start calls are rejected.
func (s *Scheduler) Start(parent context.Context) error {
	if s == nil || s.service == nil {
		return ErrInvalid
	}
	if parent == nil {
		parent = context.Background()
	}
	s.mu.Lock()
	if s.done != nil {
		s.mu.Unlock()
		return ErrSchedulerRunning
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	s.mu.Unlock()
	go s.run(ctx, done)
	return nil
}

// Stop cancels the timer and all in-flight work contexts, then waits for the
// fixed worker set to exit. It is safe to call when the scheduler is stopped.
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *Scheduler) run(ctx context.Context, done chan struct{}) {
	jobs := make(chan schedulerJob, s.workers)
	completed := make(chan schedulerCompletion, s.workers)
	var workers sync.WaitGroup
	workers.Add(s.workers)
	for range s.workers {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-jobs:
					if !ok || ctx.Err() != nil {
						return
					}
					if job.resource {
						_ = job.enqueue(ctx)
					} else {
						_, _ = s.service.Refresh(ctx, job.id)
					}
					select {
					case completed <- schedulerCompletion{id: job.id, resource: job.resource}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	timer := time.NewTimer(time.Hour)
	stopTimer(timer)
	pending := make(map[string]bool)
	resourcePending := false
	complete := func(job schedulerCompletion) {
		if job.resource {
			resourcePending = false
		} else {
			delete(pending, job.id)
		}
	}
	for ctx.Err() == nil {
		now := time.Now()
		due, subscriptionNext := s.service.dueIDs(now, pending)
		resourceNextFn, resourceEnqueue := s.resourceCallbacks()
		var externalNext time.Time
		if !resourcePending && resourceNextFn != nil && resourceEnqueue != nil {
			externalNext = resourceNextFn(now)
		}

		blocked := false
		for _, id := range due {
			select {
			case jobs <- schedulerJob{id: id}:
				pending[id] = true
			default:
				blocked = true
			}
		}
		if !resourcePending && !externalNext.IsZero() && !externalNext.After(now) && resourceEnqueue != nil {
			select {
			case jobs <- schedulerJob{resource: true, enqueue: resourceEnqueue}:
				resourcePending = true
			default:
				blocked = true
			}
		}
		if blocked {
			stopTimer(timer)
			select {
			case <-ctx.Done():
			case completedJob := <-completed:
				complete(completedJob)
			case <-s.service.wakeups():
			case <-s.resourceWake:
			}
			continue
		}

		_, subscriptionNext = s.service.dueIDs(time.Now(), pending)
		resourceNextFn, resourceEnqueue = s.resourceCallbacks()
		if resourcePending || resourceNextFn == nil || resourceEnqueue == nil {
			externalNext = time.Time{}
		} else {
			externalNext = resourceNextFn(time.Now())
		}
		next := subscriptionNext
		if !externalNext.IsZero() && (next.IsZero() || externalNext.Before(next)) {
			next = externalNext
		}
		if next.IsZero() {
			stopTimer(timer)
		} else {
			wait := time.Until(next)
			if wait < 0 {
				wait = 0
			}
			resetTimer(timer, wait)
		}
		select {
		case <-ctx.Done():
		case <-timer.C:
		case completedJob := <-completed:
			complete(completedJob)
		case <-s.service.wakeups():
		case <-s.resourceWake:
		}
	}

	stopTimer(timer)
	close(jobs)
	workers.Wait()
	s.mu.Lock()
	if s.done == done {
		s.cancel = nil
		s.done = nil
	}
	s.mu.Unlock()
	close(done)
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	stopTimer(timer)
	timer.Reset(delay)
}
