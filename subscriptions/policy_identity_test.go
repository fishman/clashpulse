package subscriptions

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestCancelChangedSourcesTracksCompleteSubscriptionIntent(t *testing.T) {
	base := config.Subscription{
		ID: "changed", Name: "before", URL: "https://example.invalid/profile", Enabled: true,
		RefreshInterval: time.Hour, Timeout: time.Second, Route: string(download.Direct), AllowHTTP: true,
	}
	changes := []struct {
		name   string
		change func(*config.Subscription)
	}{
		{"name", func(s *config.Subscription) { s.Name = "after" }},
		{"route", func(s *config.Subscription) { s.Route = string(download.SystemProxy) }},
		{"allow-invalid-tls", func(s *config.Subscription) { s.AllowInvalidTLS = true }},
		{"enabled", func(s *config.Subscription) { s.Enabled = false }},
		{"timeout", func(s *config.Subscription) { s.Timeout++ }},
		{"refresh-interval", func(s *config.Subscription) { s.RefreshInterval++ }},
		{"allow-http", func(s *config.Subscription) { s.AllowHTTP = false }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewService(store, Options{
				Transport: func(download.Route, bool) (http.RoundTripper, error) { return http.DefaultTransport, nil },
				Render:    func(context.Context, []byte) ([]byte, error) { return nil, nil },
				Validate:  func(context.Context, []byte) error { return nil },
				Apply:     func(context.Context, []byte) error { return nil },
				Restore:   func(context.Context, []byte) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			unchanged := base
			unchanged.ID = "unchanged"
			before := []config.Subscription{base, unchanged}
			contexts := make(map[string]context.Context, len(before))
			finishes := make([]func(), 0, len(before))
			for _, subscription := range before {
				ctx, finish := service.registerRefresh(context.Background(), subscription.ID)
				contexts[subscription.ID] = ctx
				finishes = append(finishes, finish)
			}
			defer func() {
				for _, finish := range finishes {
					finish()
				}
			}()

			updated := base
			change.change(&updated)
			service.CancelChangedSources(before, []config.Subscription{updated, unchanged})
			if contexts[base.ID].Err() != context.Canceled {
				t.Fatalf("changed intent context error = %v, want canceled", contexts[base.ID].Err())
			}
			if contexts[unchanged.ID].Err() != nil {
				t.Fatalf("unchanged intent context error = %v", contexts[unchanged.ID].Err())
			}
		})
	}
}

func TestRefreshRejectsCandidateWhenSameURLIntentChangesBeforePromotion(t *testing.T) {
	testRefreshRejectsChangedIntent(t, 3)
}

func TestRefreshRollsBackCandidateWhenSameURLIntentChangesAfterPromotion(t *testing.T) {
	testRefreshRejectsChangedIntent(t, 4)
}

func testRefreshRejectsChangedIntent(t *testing.T, changeAt int) {
	t.Helper()
	knownGood := []byte("proxies:\n  - name: retained\n    type: direct\n")
	stale := []byte("proxies:\n  - name: stale\n    type: direct\n")
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			_, _ = w.Write(knownGood)
			return
		}
		_, _ = w.Write(stale)
	}))
	defer server.Close()

	current := config.Subscription{
		ID: "policy", Name: "Policy", URL: server.URL + "/profile?token=private", Enabled: true,
		Timeout: 5 * time.Second, Route: string(download.SystemProxy), AllowHTTP: true, AllowInvalidTLS: true,
	}
	currentCalls := 0
	changePolicy := false
	var transportRoute download.Route
	var transportAllowsInvalidTLS bool
	store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, Options{
		Transport: func(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error) {
			transportRoute, transportAllowsInvalidTLS = route, allowInvalidTLS
			return server.Client().Transport, nil
		},
		Render: func(_ context.Context, profile []byte) ([]byte, error) {
			return append([]byte("generated:\n"), profile...), nil
		},
		Validate: func(context.Context, []byte) error { return nil },
		Apply:    func(context.Context, []byte) error { return nil },
		Restore:  func(context.Context, []byte) error { return nil },
		Current: func(string) (config.Subscription, bool) {
			currentCalls++
			if changePolicy && currentCalls == changeAt {
				current.Route = string(download.Direct)
				current.AllowInvalidTLS = false
			}
			return current, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.Add(current)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || !initial.Changed {
		t.Fatalf("initial refresh = %+v, %v", initial, err)
	}
	if err := service.Activate(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}

	currentCalls = 0
	current.Route = string(download.SystemProxy)
	changePolicy = true
	current.AllowInvalidTLS = true
	if _, err := service.Refresh(context.Background(), entry.ID); err != ErrSourceChanged {
		t.Fatalf("same-URL policy change error = %v, want ErrSourceChanged", err)
	}
	if transportRoute != download.SystemProxy || !transportAllowsInvalidTLS {
		t.Fatalf("fetched with intent (%q, %v), want prior intent (system proxy, true)", transportRoute, transportAllowsInvalidTLS)
	}
	entries := service.List()
	if len(entries) != 1 || entries[0].Hash != initial.Hash || entries[0].PendingActivation {
		t.Fatalf("stale candidate replaced known-good snapshot: %+v", entries)
	}
	profile, err := service.Profile(entry.ID)
	if err != nil || !bytes.Equal(profile, knownGood) {
		t.Fatalf("retained profile = %q, %v", profile, err)
	}
	activeID, activeProfile, err := service.ActiveProfile()
	if err != nil || activeID != entry.ID || !bytes.Equal(activeProfile, knownGood) {
		t.Fatalf("active profile after stale refresh = %q, %q, %v", activeID, activeProfile, err)
	}
}

func TestCancelChangedSourcesInterruptsSameURLRefreshWithoutIDLock(t *testing.T) {
	knownGood := []byte("proxies:\n  - name: retained\n    type: direct\n")
	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			_, _ = w.Write(knownGood)
			return
		}
		close(started)
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()
	store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return server.Client().Transport, nil },
		Render: func(_ context.Context, profile []byte) ([]byte, error) {
			return append([]byte("generated:\n"), profile...), nil
		},
		Validate: func(context.Context, []byte) error { return nil },
		Apply:    func(context.Context, []byte) error { return nil },
		Restore:  func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	before := config.Subscription{
		ID: "rename", Name: "before", URL: server.URL + "/profile", Enabled: true,
		Timeout: 5 * time.Second, Route: string(download.Direct), AllowHTTP: true,
	}
	entry, err := service.Add(before)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || !initial.Changed {
		t.Fatalf("initial refresh = %+v, %v", initial, err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		_, err := service.Refresh(context.Background(), entry.ID)
		refreshDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("long refresh did not start")
	}
	after := before
	after.Name = "after"
	cancelDone := make(chan struct{})
	go func() {
		service.CancelChangedSources([]config.Subscription{before}, []config.Subscription{after})
		close(cancelDone)
	}()
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("same-URL config change waited on the per-ID refresh lock")
	}
	select {
	case <-requestCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("same-URL config change did not cancel the HTTP request")
	}
	select {
	case err := <-refreshDone:
		if err != context.Canceled {
			t.Fatalf("canceled refresh error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled refresh did not release the per-ID lock")
	}
	if _, err := service.Update(after); err != nil {
		t.Fatalf("update after canceled refresh = %v", err)
	}
	entries := service.List()
	if len(entries) != 1 || entries[0].Hash != initial.Hash {
		t.Fatalf("cancellation discarded known-good snapshot: %+v", entries)
	}
}
