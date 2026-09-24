package subscriptions

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestRefreshingActiveSubscriptionPreservesAppliedProfile(t *testing.T) {
	firstProfile := []byte("proxies:\n  - name: running-v1\n    type: direct\n")
	secondProfile := []byte("proxies:\n  - name: latest-v2\n    type: direct\n")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			_, _ = w.Write(firstProfile)
			return
		}
		_, _ = w.Write(secondProfile)
	}))
	defer server.Close()

	root := filepath.Join(t.TempDir(), "subscriptions")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal("NewStore failed")
	}
	service, err := NewService(store, appliedTestOptions(t, server, nil, nil))
	if err != nil {
		t.Fatal("NewService failed")
	}
	entry, err := service.Add(config.Subscription{ID: "active", URL: server.URL, AllowHTTP: true})
	if err != nil {
		t.Fatal("Add failed")
	}
	if _, err := service.Refresh(context.Background(), entry.ID); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if err := service.Activate(context.Background(), entry.ID); err != nil {
		t.Fatalf("initial activation: %v", err)
	}
	second, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || !second.Changed {
		t.Fatalf("refresh changed active LKG: %+v, %v", second, err)
	}
	entries := service.List()
	if len(entries) != 1 || !entries[0].Active || entries[0].Hash != second.Hash {
		t.Fatalf("active entry after refresh = %+v", entries)
	}
	if entries[0].AppliedHash != hashBytes(firstProfile)[:12] || !entries[0].PendingActivation {
		t.Fatalf("pending activation metadata = applied %q, pending %v", entries[0].AppliedHash, entries[0].PendingActivation)
	}
	activeID, got, err := service.ActiveProfile()
	if err != nil || activeID != entry.ID || !bytes.Equal(got, firstProfile) {
		t.Fatalf("ActiveProfile after refresh = %q, %q, %v; want the running v1 source", activeID, got, err)
	}

	reopened, err := NewStore(root)
	if err != nil {
		t.Fatalf("reopen after refresh: %v", err)
	}
	reloaded, err := NewService(reopened, appliedTestOptions(t, server, nil, nil))
	if err != nil {
		t.Fatal("reopened NewService failed")
	}
	activeID, got, err = reloaded.ActiveProfile()
	if err != nil || activeID != entry.ID || !bytes.Equal(got, firstProfile) {
		t.Fatalf("reloaded ActiveProfile after refresh = %q, %q, %v", activeID, got, err)
	}
}

func TestActivatePersistenceFailureRestoresPreviousRuntime(t *testing.T) {
	profileA := []byte("proxies:\n  - name: runtime-a\n    type: direct\n")
	profileB := []byte("proxies:\n  - name: runtime-b\n    type: direct\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			_, _ = w.Write(profileA)
			return
		}
		_, _ = w.Write(profileB)
	}))
	defer server.Close()

	root := filepath.Join(t.TempDir(), "subscriptions")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal("NewStore failed")
	}
	var runtime []byte
	activePath := filepath.Join(root, "active.json")
	backupPath := filepath.Join(root, "active.backup")
	targetRecordPath := filepath.Join(root, "b", "record.json")
	backupRecordPath := filepath.Join(root, "b", "record.backup")
	options := appliedTestOptions(t, server, func(_ context.Context, profile []byte) error {
		runtime = bytes.Clone(profile)
		if bytes.Equal(profile, profileB) {
			if err := os.Rename(activePath, backupPath); err != nil {
				return err
			}
			if err := os.Mkdir(activePath, 0o700); err != nil {
				return err
			}
			if err := os.Rename(targetRecordPath, backupRecordPath); err != nil {
				return err
			}
			return os.Mkdir(targetRecordPath, 0o700)
		}
		return nil
	}, func(_ context.Context, previous []byte) error {
		runtime = bytes.Clone(previous)
		if err := os.Remove(activePath); err != nil {
			return err
		}
		if err := os.Rename(backupPath, activePath); err != nil {
			return err
		}
		if err := os.Remove(targetRecordPath); err != nil {
			return err
		}
		return os.Rename(backupRecordPath, targetRecordPath)
	})
	service, err := NewService(store, options)
	if err != nil {
		t.Fatal("NewService failed")
	}
	a, err := service.Add(config.Subscription{ID: "a", URL: server.URL + "/a", AllowHTTP: true})
	if err != nil {
		t.Fatal("Add A failed")
	}
	b, err := service.Add(config.Subscription{ID: "b", URL: server.URL + "/b", AllowHTTP: true})
	if err != nil {
		t.Fatal("Add B failed")
	}
	for _, entry := range []Entry{a, b} {
		if _, err := service.Refresh(context.Background(), entry.ID); err != nil {
			t.Fatalf("refresh %s: %v", entry.ID, err)
		}
	}
	if err := service.Activate(context.Background(), a.ID); err != nil {
		t.Fatalf("activate A: %v", err)
	}
	if !bytes.Equal(runtime, profileA) {
		t.Fatalf("runtime before failed switch = %q", runtime)
	}
	if err := service.Activate(context.Background(), b.ID); err != ErrStore {
		t.Fatalf("Activate B error = %v, want persistence error", err)
	}
	if !bytes.Equal(runtime, profileA) {
		t.Fatalf("runtime after failed activation = %q, want restored A profile", runtime)
	}
	activeID, activeProfile, err := service.ActiveProfile()
	if err != nil || activeID != a.ID || !bytes.Equal(activeProfile, profileA) {
		t.Fatalf("active metadata after failed switch = %q, %q, %v", activeID, activeProfile, err)
	}
	reopened, err := NewStore(root)
	if err != nil {
		t.Fatalf("reopen after failed activation: %v", err)
	}
	if activeID, err := reopened.ActiveID(); err != nil || activeID != a.ID {
		t.Fatalf("persisted active ID after failed switch = %q, %v", activeID, err)
	}
	if record, ok := reopened.get(b.ID); !ok || record.AppliedProfileHash != "" || record.AppliedCandidateHash != "" {
		t.Fatalf("failed activation persisted applied metadata: %+v, exists=%v", record, ok)
	}
}

func TestActivatePersistenceFailureRestoresNilPreviousProfile(t *testing.T) {
	profile := []byte("proxies:\n  - name: just-started\n    type: direct\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(profile)
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "subscriptions")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal("NewStore failed")
	}
	restored := false
	options := appliedTestOptions(t, server, func(context.Context, []byte) error {
		return os.Mkdir(filepath.Join(root, "active.json"), 0o700)
	}, func(_ context.Context, previous []byte) error {
		restored = previous == nil
		return nil
	})
	service, err := NewService(store, options)
	if err != nil {
		t.Fatal("NewService failed")
	}
	entry, err := service.Add(config.Subscription{ID: "first", URL: server.URL, AllowHTTP: true})
	if err != nil {
		t.Fatal("Add failed")
	}
	if _, err := service.Refresh(context.Background(), entry.ID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := service.Activate(context.Background(), entry.ID); err != ErrStore {
		t.Fatalf("Activate error = %v, want persistence failure", err)
	}
	if !restored {
		t.Fatal("Restore was not called with nil previous profile")
	}
	if _, err := store.ActiveID(); err != ErrNoSnapshot {
		t.Fatalf("failed initial activation changed selected ID: %v", err)
	}
}

func TestCancelChangedSourcesCancelsRemovedAndUpdatedURLs(t *testing.T) {
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
	before := []config.Subscription{
		{ID: "changed", URL: "https://old.invalid"},
		{ID: "removed", URL: "https://removed.invalid"},
		{ID: "same", URL: "https://same.invalid"},
	}
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
	service.CancelChangedSources(before, []config.Subscription{
		{ID: "changed", URL: "https://new.invalid"},
		{ID: "same", URL: "https://same.invalid"},
	})
	for _, id := range []string{"changed", "removed"} {
		select {
		case <-contexts[id].Done():
		default:
			t.Fatalf("%s refresh was not cancelled", id)
		}
	}
	if err := contexts["same"].Err(); err != nil {
		t.Fatalf("unchanged URL refresh was cancelled: %v", err)
	}
}

func appliedTestOptions(t *testing.T, server *httptest.Server, apply ApplyFunc, restore RestoreFunc) Options {
	t.Helper()
	if apply == nil {
		apply = func(context.Context, []byte) error { return nil }
	}
	if restore == nil {
		restore = func(context.Context, []byte) error { return nil }
	}
	return Options{
		Transport: func(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error) {
			if route != download.Direct || allowInvalidTLS {
				return nil, ErrInvalid
			}
			return server.Client().Transport, nil
		},
		Render: func(_ context.Context, profile []byte) ([]byte, error) {
			return append([]byte("generated:\n"), profile...), nil
		},
		Validate: func(context.Context, []byte) error { return nil },
		Apply:    apply,
		Restore:  restore,
	}
}

func TestActivateReportsRestoreFailureWithoutCallbackDetails(t *testing.T) {
	profileA := []byte("proxies:\n  - name: previous\n    type: direct\n")
	profileB := []byte("proxies:\n  - name: requested\n    type: direct\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			_, _ = w.Write(profileA)
			return
		}
		_, _ = w.Write(profileB)
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "subscriptions")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal("NewStore failed")
	}
	var runtime []byte
	restoreCalled := false
	options := appliedTestOptions(t, server, func(_ context.Context, profile []byte) error {
		runtime = bytes.Clone(profile)
		if bytes.Equal(profile, profileB) {
			activePath := filepath.Join(root, "active.json")
			if err := os.Remove(activePath); err != nil {
				return err
			}
			return os.Mkdir(activePath, 0o700)
		}
		return nil
	}, func(_ context.Context, previous []byte) error {
		restoreCalled = true
		runtime = bytes.Clone(previous)
		return errors.New("private restore detail and token")
	})
	service, err := NewService(store, options)
	if err != nil {
		t.Fatal("NewService failed")
	}
	a, err := service.Add(config.Subscription{ID: "restore-a", URL: server.URL + "/a", AllowHTTP: true})
	if err != nil {
		t.Fatal("Add A failed")
	}
	b, err := service.Add(config.Subscription{ID: "restore-b", URL: server.URL + "/b", AllowHTTP: true})
	if err != nil {
		t.Fatal("Add B failed")
	}
	for _, entry := range []Entry{a, b} {
		if _, err := service.Refresh(context.Background(), entry.ID); err != nil {
			t.Fatalf("Refresh %s: %v", entry.ID, err)
		}
	}
	if err := service.Activate(context.Background(), a.ID); err != nil {
		t.Fatalf("activate previous source: %v", err)
	}
	err = service.Activate(context.Background(), b.ID)
	if !errors.Is(err, ErrStore) || !errors.Is(err, ErrRestore) || strings.Contains(err.Error(), "private restore detail") {
		t.Fatalf("activation error = %v; want sanitized store and restore error", err)
	}
	if !restoreCalled || !bytes.Equal(runtime, profileA) {
		t.Fatalf("restore called=%v, runtime=%q", restoreCalled, runtime)
	}
	activeID, activeProfile, err := service.ActiveProfile()
	if err != nil || activeID != a.ID || !bytes.Equal(activeProfile, profileA) {
		t.Fatalf("failed rollback changed active selection: %q, %q, %v", activeID, activeProfile, err)
	}
}
