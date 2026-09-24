package subscriptions

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestSchedulerStopCancelsInFlightRefresh(t *testing.T) {
	entered := make(chan struct{}, 1)
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
	if err != nil {
		t.Fatal("NewStore failed")
	}
	service, err := NewService(store, Options{
		Transport: func(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error) {
			if route != download.Direct || allowInvalidTLS {
				return nil, ErrInvalid
			}
			return transport, nil
		},
		Render:   func(context.Context, []byte) ([]byte, error) { return []byte("candidate"), nil },
		Validate: func(context.Context, []byte) error { return nil },
		Apply:    func(context.Context, []byte) error { return nil },
		Restore:  func(context.Context, []byte) error { return nil },
	})
	if _, err := service.Add(config.Subscription{
		ID: "blocked", Name: "Blocked", URL: "https://example.invalid/sub?token=short-lived", Enabled: true, Timeout: time.Minute,
	}); err != nil {
		t.Fatal("Add failed")
	}
	scheduler := NewScheduler(service, MaxRefreshWorkers+4)
	if scheduler.workers != MaxRefreshWorkers {
		t.Fatalf("worker limit = %d, want %d", scheduler.workers, MaxRefreshWorkers)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		scheduler.Stop()
		t.Fatal("scheduler did not start its due refresh")
	}
	stopped := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel and join the in-flight request")
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("restart after Stop: %v", err)
	}
	scheduler.Stop()
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestAddRejectsURLUserinfo(t *testing.T) {
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
	_, err = service.Add(config.Subscription{ID: "unsafe", URL: "https://user:secret@example.invalid/list"})
	if err != ErrInvalid || strings.Contains(err.Error(), "secret") {
		t.Fatalf("Add error = %v, want sanitized ErrInvalid", err)
	}
}
