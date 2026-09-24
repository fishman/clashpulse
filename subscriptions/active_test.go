package subscriptions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestActiveProfileSwitchPersistsAcrossRestart(t *testing.T) {
	profile := []byte("proxies:\n  - name: shared\n    type: direct\n")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write(profile)
	}))
	defer server.Close()

	root := filepath.Join(t.TempDir(), "subscriptions")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal("NewStore failed")
	}
	applyCalls := 0
	service, err := NewService(store, Options{
		Transport: func(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error) {
			if route != download.Direct || allowInvalidTLS {
				return nil, ErrInvalid
			}
			return server.Client().Transport, nil
		},
		Render: func(_ context.Context, source []byte) ([]byte, error) {
			return append([]byte("generated:\n"), source...), nil
		},
		Validate: func(context.Context, []byte) error { return nil },
		Apply: func(context.Context, []byte) error {
			applyCalls++
			return nil
		},
		Restore: func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal("NewService failed")
	}
	first, err := service.Add(config.Subscription{
		ID: "profile-a", Name: "A", URL: server.URL + "/a", AllowHTTP: true,
	})
	if err != nil {
		t.Fatal("Add first profile failed")
	}
	second, err := service.Add(config.Subscription{
		ID: "profile-b", Name: "B", URL: server.URL + "/b", AllowHTTP: true,
	})
	if err != nil {
		t.Fatal("Add second profile failed")
	}
	for _, entry := range []Entry{first, second} {
		if _, err := service.Refresh(context.Background(), entry.ID); err != nil {
			t.Fatalf("Refresh %s: %v", entry.ID, err)
		}
	}
	if err := service.Activate(context.Background(), first.ID); err != nil {
		t.Fatalf("Activate A: %v", err)
	}
	if err := service.Activate(context.Background(), second.ID); err != nil {
		t.Fatalf("Activate B: %v", err)
	}
	if err := service.Activate(context.Background(), first.ID); err != nil {
		t.Fatalf("switch back to A: %v", err)
	}
	if applyCalls != 3 {
		t.Fatalf("Apply calls = %d, want 3 (including equal-byte switches)", applyCalls)
	}
	if requests != 2 {
		t.Fatalf("refresh count = %d, want 2; activation must not fetch", requests)
	}
	if info, err := os.Stat(filepath.Join(root, "active.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("active pointer permissions = %v, %v", info, err)
	}
	activeID, activeProfile, err := service.ActiveProfile()
	if err != nil || activeID != first.ID || string(activeProfile) != string(profile) {
		t.Fatalf("ActiveProfile = %q, %q, %v", activeID, activeProfile, err)
	}
	activeProfile[0] = 'X'
	freshProfile, err := service.Profile(first.ID)
	if err != nil || string(freshProfile) != string(profile) {
		t.Fatalf("Profile returned mutable store bytes: %q, %v", freshProfile, err)
	}
	entries := service.List()
	if len(entries) != 2 || !entries[0].Active || entries[1].Active {
		t.Fatalf("active entry flags = %+v", entries)
	}

	reopened, err := NewStore(root)
	if err != nil {
		t.Fatal("reopening active store failed")
	}
	replayed := false
	newService, err := NewService(reopened, Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return server.Client().Transport, nil },
		Render:    func(context.Context, []byte) ([]byte, error) { return nil, ErrRender },
		Validate:  func(context.Context, []byte) error { return nil },
		Apply:     func(context.Context, []byte) error { replayed = true; return nil },
		Restore:   func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal("reopening service failed")
	}
	gotID, gotProfile, err := newService.ActiveProfile()
	if err != nil || gotID != first.ID || string(gotProfile) != string(profile) {
		t.Fatalf("reloaded ActiveProfile = %q, %q, %v", gotID, gotProfile, err)
	}
	if err := newService.Activate(context.Background(), first.ID); err != nil || !replayed {
		t.Fatalf("re-activating persisted selection did not rebuild the current runtime: replayed %v, err %v", replayed, err)
	}
	if err := newService.Delete(first.ID); err != ErrActive {
		t.Fatalf("Delete active profile = %v, want ErrActive", err)
	}
	gotID, gotProfile, err = newService.ActiveProfile()
	if err != nil || gotID != first.ID || string(gotProfile) != string(profile) {
		t.Fatalf("failed Delete changed active profile: %q, %q, %v", gotID, gotProfile, err)
	}
	if err := newService.Activate(context.Background(), second.ID); err != nil {
		t.Fatalf("move activation to replacement: %v", err)
	}
	if err := newService.Delete(first.ID); err != nil {
		t.Fatalf("Delete inactive profile: %v", err)
	}
	if id, err := newService.ActiveID(); err != nil || id != second.ID {
		t.Fatalf("ActiveID after deleting old profile = %q, %v", id, err)
	}
}
