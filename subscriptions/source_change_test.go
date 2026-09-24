package subscriptions

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestRefreshRollsBackWhenCurrentURLChangesAtPromotion(t *testing.T) {
	oldProfile := []byte("proxies:\n  - name: retained-source\n    type: direct\n")
	newProfile := []byte("proxies:\n  - name: stale-response\n    type: direct\n")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			_, _ = w.Write(oldProfile)
			return
		}
		_, _ = w.Write(newProfile)
	}))
	defer server.Close()
	store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
	if err != nil {
		t.Fatal("NewStore failed")
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
		t.Fatal("NewService failed")
	}
	oldURL := server.URL + "/old?sig=private-old"
	entry, err := service.Add(config.Subscription{ID: "rotating", URL: oldURL, AllowHTTP: true})
	if err != nil {
		t.Fatal("Add failed")
	}
	first, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || !first.Changed {
		t.Fatalf("initial refresh = %+v, %v", first, err)
	}
	if err := service.Activate(context.Background(), entry.ID); err != nil {
		t.Fatalf("Activate initial generation: %v", err)
	}

	latest := config.Subscription{ID: entry.ID, Name: entry.Name, URL: oldURL, AllowHTTP: true}
	currentCalls := 0
	service.options.Current = func(string) (config.Subscription, bool) {
		currentCalls++
		if currentCalls == 4 {
			latest.URL = server.URL + "/rotated?sig=private-new"
		}
		return latest, true
	}
	if _, err := service.Refresh(context.Background(), entry.ID); err != ErrSourceChanged {
		t.Fatalf("Refresh after final URL rotation = %v, want ErrSourceChanged", err)
	}
	if currentCalls != 4 {
		t.Fatalf("Current callback calls = %d, want initial, fetch, pre-commit, post-commit", currentCalls)
	}
	entries := service.List()
	if len(entries) != 1 || entries[0].Hash != first.Hash || entries[0].PendingActivation {
		t.Fatalf("stale generation was not rolled back: %+v", entries)
	}
	profile, err := service.Profile(entry.ID)
	if err != nil || !bytes.Equal(profile, oldProfile) {
		t.Fatalf("current LKG after URL rotation = %q, %v", profile, err)
	}
	activeID, activeProfile, err := service.ActiveProfile()
	if err != nil || activeID != entry.ID || !bytes.Equal(activeProfile, oldProfile) {
		t.Fatalf("applied LKG after URL rotation = %q, %q, %v", activeID, activeProfile, err)
	}
	reopened, err := NewStore(store.dir)
	if err != nil {
		t.Fatalf("reopen after stale response: %v", err)
	}
	reloaded, err := NewService(reopened, Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return server.Client().Transport, nil },
		Render:    func(context.Context, []byte) ([]byte, error) { return nil, ErrRender },
		Validate:  func(context.Context, []byte) error { return nil },
		Apply:     func(context.Context, []byte) error { return nil },
		Restore:   func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal("reopening service failed")
	}
	activeID, activeProfile, err = reloaded.ActiveProfile()
	if err != nil || activeID != entry.ID || !bytes.Equal(activeProfile, oldProfile) {
		t.Fatalf("reloaded applied LKG = %q, %q, %v", activeID, activeProfile, err)
	}
}
