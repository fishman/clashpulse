package subscriptions

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fishman/clashpulse/download"
)

func TestSchedulerQueuesDueResourcesOnceAndRecalculates(t *testing.T) {
	service := newIdleService(t)
	scheduler := NewScheduler(service, 1)
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	due := time.Now().Add(-time.Second)
	queued := 0
	scheduler.ConfigureResources(func(time.Time) time.Time {
		mu.Lock()
		defer mu.Unlock()
		return due
	}, func(context.Context) error {
		mu.Lock()
		queued++
		due = time.Time{}
		mu.Unlock()
		started <- struct{}{}
		return nil
	})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(scheduler.Stop)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("resource deadline was not enqueued")
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	got := queued
	mu.Unlock()
	if got != 1 {
		t.Fatalf("resource enqueue count = %d, want 1 after next deadline became empty", got)
	}
}

func TestSchedulerResourceDeadlineStaysPendingUntilEnqueueCompletes(t *testing.T) {
	service := newIdleService(t)
	scheduler := NewScheduler(service, 1)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	due := time.Now().Add(-time.Second)
	nextCalls, enqueueCalls := 0, 0
	scheduler.ConfigureResources(func(time.Time) time.Time {
		mu.Lock()
		nextCalls++
		value := due
		mu.Unlock()
		return value
	}, func(context.Context) error {
		mu.Lock()
		enqueueCalls++
		mu.Unlock()
		started <- struct{}{}
		<-release
		return nil
	})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(scheduler.Stop)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("resource deadline was not enqueued")
	}
	scheduler.WakeResources()
	scheduler.WakeResources()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	gotEnqueue, gotNext := enqueueCalls, nextCalls
	mu.Unlock()
	if gotEnqueue != 1 || gotNext != 1 {
		t.Fatalf("while enqueue pending: enqueue calls=%d, next calls=%d; want 1 and 1", gotEnqueue, gotNext)
	}
	mu.Lock()
	due = time.Time{}
	mu.Unlock()
	close(release)
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	gotEnqueue = enqueueCalls
	mu.Unlock()
	if gotEnqueue != 1 {
		t.Fatalf("resource enqueue repeated after callback recalculated no due work: %d", gotEnqueue)
	}
}

func TestSchedulerWakeResourcesRecalculatesDeadline(t *testing.T) {
	service := newIdleService(t)
	scheduler := NewScheduler(service, 1)
	var mu sync.Mutex
	due := time.Now().Add(time.Hour)
	queued := make(chan struct{}, 1)
	scheduler.ConfigureResources(func(time.Time) time.Time {
		mu.Lock()
		defer mu.Unlock()
		return due
	}, func(context.Context) error {
		queued <- struct{}{}
		mu.Lock()
		due = time.Time{}
		mu.Unlock()
		return nil
	})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(scheduler.Stop)
	mu.Lock()
	due = time.Now().Add(-time.Second)
	mu.Unlock()
	scheduler.WakeResources()
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("resource wake did not recalculate the nearest deadline")
	}
}

func TestSchedulerStopCancelsResourceEnqueue(t *testing.T) {
	service := newIdleService(t)
	scheduler := NewScheduler(service, 1)
	started := make(chan struct{}, 1)
	scheduler.ConfigureResources(func(time.Time) time.Time { return time.Now().Add(-time.Second) }, func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		scheduler.Stop()
		t.Fatal("resource enqueue did not start")
	}
	stopped := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel and join resource enqueue")
	}
}

func newIdleService(t *testing.T) *Service {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
	if err != nil {
		t.Fatal("NewStore failed")
	}
	service, err := NewService(store, Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return http.DefaultTransport, nil },
		Render:    func(context.Context, []byte) ([]byte, error) { return []byte("candidate"), nil },
		Validate:  func(context.Context, []byte) error { return nil },
		Apply:     func(context.Context, []byte) error { return nil },
		Restore:   func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal("NewService failed")
	}
	return service
}
