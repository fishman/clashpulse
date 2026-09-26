package subscriptions

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
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

func TestFinalizeFailureRestoresPreviousAppliedProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxies:\n  - name: " + r.URL.Path[1:] + "\n    type: direct\n"))
	}))
	defer server.Close()
	store, service := makeService(t, server, nil, nil)
	for _, id := range []string{"old", "new"} {
		if _, err := service.Add(config.Subscription{ID: id, URL: server.URL + "/" + id, AllowHTTP: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Refresh(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Activate(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	restored := false
	service.options.Restore = func(_ context.Context, profile []byte) error {
		restored = string(profile) == "proxies:\n  - name: old\n    type: direct\n"
		return nil
	}
	service.options.Finalize = func() error { return errors.New("resource journal cannot finalize") }
	if err := service.Activate(context.Background(), "new"); err == nil {
		t.Fatal("accepted activation after resource finalization failure")
	}
	if !restored {
		t.Fatal("activation failure did not restore previous runtime")
	}
	reopened, err := NewStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	id, profile, err := reopened.ActiveProfile()
	if err != nil || id != "old" || string(profile) != "proxies:\n  - name: old\n    type: direct\n" {
		t.Fatalf("persisted active profile after failed finalization = %q, %q, %v", id, profile, err)
	}
}

func TestPendingActivationRestoresSelectionAfterResourceRollback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxies:\n  - name: " + r.URL.Path[1:] + "\n    type: direct\n"))
	}))
	defer server.Close()
	store, service := makeService(t, server, nil, nil)
	for _, id := range []string{"old", "new"} {
		if _, err := service.Add(config.Subscription{ID: id, URL: server.URL + "/" + id, AllowHTTP: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Refresh(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Activate(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	oldID, oldProfile, oldCandidate := store.appliedIdentity()
	if err := store.markActivationPending(oldID, oldProfile, oldCandidate, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	newRecord, exists := store.get("new")
	if !exists {
		t.Fatal("candidate subscription disappeared")
	}
	if err := store.setApplied("new", newRecord.Hash, newRecord.CandidateHash); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.RecoverPendingActivation("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	id, profile, err := reopened.ActiveProfile()
	if err != nil || id != "old" || string(profile) != "proxies:\n  - name: old\n    type: direct\n" {
		t.Fatalf("recovered active profile = %q, %q, %v", id, profile, err)
	}
	if err := reopened.markActivationPending(oldID, oldProfile, oldCandidate, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.setApplied("new", newRecord.Hash, newRecord.CandidateHash); err != nil {
		t.Fatal(err)
	}
	if err := reopened.markActivationAccepted(); err != nil {
		t.Fatal(err)
	}
	accepted, err := NewStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := accepted.RecoverPendingActivation("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if id, err := accepted.ActiveID(); err != nil || id != "new" {
		t.Fatalf("accepted activation was reverted after a later resource update: %q, %v", id, err)
	}
}

func TestActivationFailureStage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("proxies:\n  - name: alpha\n    type: direct\n"))
	}))
	defer server.Close()
	_, service := makeService(t, server, nil, nil)
	if _, err := service.Add(config.Subscription{ID: "feed", URL: server.URL, AllowHTTP: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "feed"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		stage core.ActivationStage
		id    string
	}{
		{core.ActivationControllerReadiness, ""},
		{core.ActivationResources, "geosite"},
	} {
		service.options.Apply = func(context.Context, []byte) error {
			return core.WrapActivationResource(test.stage, test.id, errors.New("password=private https://feed.invalid/?token=private"))
		}
		err := service.Activate(context.Background(), "feed")
		public, ok := core.PublicActivation(err)
		if !errors.Is(err, ErrActivation) || !ok || public.Stage != test.stage || public.ResourceID != test.id ||
			strings.Contains(err.Error(), "password=private") || strings.Contains(err.Error(), "token=private") || strings.Contains(err.Error(), "feed.invalid") {
			t.Fatalf("activation lost stage or leaked private cause: %v", err)
		}
	}
	service.options.Apply = func(context.Context, []byte) error { return nil }
	service.options.Finalize = func() error { return errors.New("https://feed.invalid/?token=private") }
	service.options.Restore = func(context.Context, []byte) error { return nil }
	err := service.Activate(context.Background(), "feed")
	public, ok := core.PublicActivation(err)
	if !ok || public.Stage != core.ActivationStateCommit || strings.Contains(err.Error(), "token=private") || strings.Contains(err.Error(), "feed.invalid") {
		t.Fatalf("state commit failure leaked private cause: %v", err)
	}
	service.options.Restore = func(context.Context, []byte) error { return errors.New("password=private") }
	err = service.Activate(context.Background(), "feed")
	public, ok = core.PublicActivation(err)
	if !ok || public.Stage != core.ActivationRollback || strings.Contains(err.Error(), "password=private") {
		t.Fatalf("rollback failure leaked private cause: %v", err)
	}
}
