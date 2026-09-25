package subscriptions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestRefreshUsesConfiguredUserAgent(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "clash-verge/v2.5.6" {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		_, _ = w.Write([]byte("proxies:\n  - name: selected\n    type: direct\n"))
	}))
	defer server.Close()
	_, service := makeService(t, server, nil, nil)
	if _, err := service.Add(config.Subscription{ID: "agent", URL: server.URL, UserAgent: "clash-verge/v2.5.6"}); err != nil {
		t.Fatal(err)
	}
	if result, err := service.Refresh(context.Background(), "agent"); err != nil || !result.Changed {
		t.Fatalf("profile was not fetched with configured agent: changed=%t err=%v", result.Changed, err)
	}
}

func TestRefreshReportsSafeHTTPStatus(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte("<html>private-token</html>"))
			return
		}
		_, _ = w.Write([]byte("proxies:\n  - name: recovered\n    type: direct\n"))
	}))
	defer server.Close()
	_, service := makeService(t, server, nil, nil)
	_, err := service.Add(config.Subscription{ID: "status", URL: server.URL + "/profile?token=private-token", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Refresh(context.Background(), "status")
	var status download.StatusError
	if !errors.Is(err, ErrFetch) || !errors.As(err, &status) || status.Code != 406 || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("status was not safely classified: %T", err)
	}
	if entries := service.List(); len(entries) != 1 || entries[0].LastFailure != "HTTP 406" {
		t.Fatal("safe HTTP status absent from subscription state")
	}
	if _, err := service.Refresh(context.Background(), "status"); err != nil {
		t.Fatal(err)
	}
	if entries := service.List(); entries[0].LastFailure != "" {
		t.Fatal("recovery kept active status failure")
	}
}

func TestRefresh304ClearsPriorFailureAndPublishesRecovery(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			w.Header().Set("ETag", `"good"`)
			_, _ = w.Write([]byte("proxies:\n  - name: stable\n    type: direct\n"))
		case 2:
			w.WriteHeader(http.StatusNotAcceptable)
		default:
			if r.Header.Get("If-None-Match") != `"good"` {
				t.Fatal("conditional validator lost after failed refresh")
			}
			w.WriteHeader(http.StatusNotModified)
		}
	}))
	defer server.Close()
	store, service := makeService(t, server, nil, nil)
	changes := 0
	service.options.OnChange = func() { changes++ }
	if _, err := service.Add(config.Subscription{ID: "recovery", URL: server.URL, AllowHTTP: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "recovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "recovery"); !errors.Is(err, ErrFetch) {
		t.Fatalf("failed refresh = %v", err)
	} else {
		var status download.StatusError
		if !errors.As(err, &status) || status.Code != 406 {
			t.Fatal("rejected refresh lost its safe status")
		}
	}
	if _, err := service.Refresh(context.Background(), "recovery"); err != nil {
		t.Fatalf("304 recovery = %v", err)
	}
	if changes != 3 {
		t.Fatalf("recovered issue was not published: changes=%d", changes)
	}
	reopened, err := NewStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	record, ok := reopened.get("recovery")
	if !ok || record.LastFailure != "" {
		t.Fatal("successful 304 left a stale persisted failure")
	}
}

func TestRefresh304AndIdenticalHashDoNotWriteState(t *testing.T) {
	profile := []byte("proxies:\n  - name: stable\n    type: direct\n")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 2 && r.Header.Get("If-None-Match") == `"v1"` {
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, requests))
		if requests == 4 {
			_, _ = w.Write([]byte("profile: []\n"))
			return
		}
		_, _ = w.Write(profile)
	}))
	defer server.Close()

	renderCalls, validateCalls := 0, 0
	store, service := makeService(t, server,
		func(_ context.Context, profile []byte) ([]byte, error) {
			renderCalls++
			return append([]byte("generated:\n"), profile...), nil
		},
		func(context.Context, []byte) error {
			validateCalls++
			return nil
		})
	notifications := 0
	failurePersisted := false
	service.options.OnChange = func() {
		notifications++
		if notifications == 2 {
			reopened, err := NewStore(store.dir)
			if err == nil {
				record, ok := reopened.get("unchanged")
				failurePersisted = ok && record.LastFailure == ErrInvalidProfile.Error()
			}
		}
	}
	entry, err := service.Add(config.Subscription{
		ID: "unchanged", Name: "Stable", URL: server.URL, Enabled: true, AllowHTTP: true,
	})
	if err != nil {
		t.Fatal("Add failed")
	}
	first, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || !first.Changed {
		t.Fatalf("initial refresh = %+v, %v", first, err)
	}
	if notifications != 1 {
		t.Fatalf("notifications after changed promotion = %d, want 1", notifications)
	}
	stateDir := filepath.Join(store.dir, entry.ID)
	before := captureFiles(t, stateDir)

	notModified, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || notModified.Changed || notModified.Hash != first.Hash {
		t.Fatalf("304 refresh = %+v, %v", notModified, err)
	}
	if notifications != 1 {
		t.Fatalf("304 triggered a change notification: %d", notifications)
	}
	if got := captureFiles(t, stateDir); !reflect.DeepEqual(got, before) {
		t.Fatal("304 changed private files")
	}

	identical, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || identical.Changed || identical.Hash != first.Hash {
		t.Fatalf("identical-body refresh = %+v, %v", identical, err)
	}
	if notifications != 1 {
		t.Fatalf("identical body triggered a change notification: %d", notifications)
	}
	if got := captureFiles(t, stateDir); !reflect.DeepEqual(got, before) {
		t.Fatal("identical body changed private files")
	}
	if requests != 3 {
		t.Fatalf("request count before failure = %d, want 3", requests)
	}
	if _, err := service.Refresh(context.Background(), entry.ID); err != ErrInvalidProfile {
		t.Fatalf("invalid profile refresh = %v, want ErrInvalidProfile", err)
	}
	if notifications != 2 || !failurePersisted {
		t.Fatalf("persisted failure notifications = %d, failure persisted=%v", notifications, failurePersisted)
	}
	if calls := service.List(); len(calls) != 1 || calls[0].CheckedAt.IsZero() {
		t.Fatal("conditional refresh did not update in-memory check time")
	}
	if renderCalls != 1 || validateCalls != 1 {
		t.Fatalf("unchanged content regenerated %d candidates and validated %d times", renderCalls, validateCalls)
	}
	if info, err := os.Stat(store.dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state directory permissions = %v, %v", info, err)
	}
	for path := range before {
		info, err := os.Stat(filepath.Join(stateDir, path))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private file permissions for %q = %v, %v", path, info, err)
		}
	}
}

func TestLatestURLPromotesValidatedSnapshotAndRetainsItOnInvalidUpdate(t *testing.T) {
	goodOld := []byte("proxies:\n  - name: old\n    type: direct\n")
	goodNew := []byte("proxies:\n  - name: new\n    type: direct\n")
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		var body []byte
		switch r.URL.Path {
		case "/old":
			body = goodOld
		case "/new":
			body = goodNew
		case "/invalid":
			body = []byte("profile: []\n")
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", r.URL.Path)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	var latest config.Subscription
	service, store := makeServiceWithCurrent(t, server, func(string) (config.Subscription, bool) {
		return latest, latest.ID != ""
	}, nil, nil)
	applied := make([]string, 0, 1)
	service.options.Apply = func(_ context.Context, candidate []byte) error {
		applied = append(applied, string(candidate))
		return nil
	}
	latest = config.Subscription{
		ID: "rotating", Name: "Travel", URL: server.URL + "/old?sig=old-secret", Enabled: true, AllowHTTP: true,
	}
	entry, err := service.Add(latest)
	if err != nil {
		t.Fatal("Add failed")
	}
	initial, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || !initial.Changed {
		t.Fatalf("initial refresh = %+v, %v", initial, err)
	}

	latest.URL = server.URL + "/new?sig=minute-secret"
	if _, err := service.Update(latest); err != nil {
		t.Fatal("URL update failed")
	}
	updated, err := service.Refresh(context.Background(), entry.ID)
	if err != nil || !updated.Changed || updated.Hash == initial.Hash {
		t.Fatalf("latest URL refresh = %+v, %v", updated, err)
	}
	if len(applied) != 0 {
		t.Fatal("Refresh activated a snapshot without an explicit request")
	}
	if err := service.Activate(context.Background(), entry.ID); err != nil {
		t.Fatalf("Activate validated update: %v", err)
	}
	if len(applied) != 1 || !strings.Contains(applied[0], "name: new") {
		t.Fatalf("activated candidate = %q", applied)
	}
	mu.Lock()
	requestsBeforeActivation := requests["/new"]
	mu.Unlock()
	if requestsBeforeActivation != 1 {
		t.Fatalf("new link request count = %d, want 1", requestsBeforeActivation)
	}

	latest.URL = server.URL + "/invalid?sig=invalid-secret"
	if _, err := service.Refresh(context.Background(), entry.ID); err != ErrInvalidProfile {
		t.Fatalf("invalid profile error = %v, want ErrInvalidProfile", err)
	}
	entries := service.List()
	if len(entries) != 1 || !entries[0].HasSnapshot || entries[0].Hash != updated.Hash {
		t.Fatalf("last-known-good snapshot was not retained: %+v", entries)
	}
	latest.URL = server.URL + "/missing?sig=fetch-secret"
	if _, err := service.Refresh(context.Background(), entry.ID); !errors.Is(err, ErrFetch) || strings.Contains(err.Error(), "fetch-secret") {
		t.Fatal("failed new-link fetch lost safe error classification")
	} else {
		var status download.StatusError
		if !errors.As(err, &status) || status.Code != 404 {
			t.Fatal("missing profile response lost its HTTP status")
		}
	}
	entries = service.List()
	if len(entries) != 1 || !entries[0].HasSnapshot || entries[0].Hash != updated.Hash {
		t.Fatalf("failed fetch discarded the last-known-good snapshot: %+v", entries)
	}
	if err := service.Activate(context.Background(), entry.ID); err != nil {
		t.Fatalf("Activate retained candidate: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests["/old"] != 1 || requests["/new"] != 1 || requests["/invalid"] != 1 || requests["/missing"] != 1 {
		t.Fatalf("request paths = %#v", requests)
	}
	public := fmt.Sprint(entries[0])
	if strings.Contains(public, "minute-secret") || strings.Contains(public, "invalid-secret") || strings.Contains(public, "fetch-secret") {
		t.Fatalf("public entry leaked URL credentials: %q", public)
	}
	if strings.Contains(ErrInvalidProfile.Error(), "invalid-secret") {
		t.Fatal("refresh error leaked the subscription URL")
	}
	if strings.Contains(ErrFetch.Error(), "fetch-secret") {
		t.Fatal("fetch error leaked the subscription URL")
	}
	if _, err := os.Stat(filepath.Join(store.dir, entry.ID, "record.json")); err != nil {
		t.Fatal("private metadata missing after failed update")
	}
}

func TestRefreshUsesLatestConfigSnapshotWithoutRefreshWith(t *testing.T) {
	profile := []byte("proxies: []\n")
	paths := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		_, _ = w.Write(profile)
	}))
	defer server.Close()
	current := config.Subscription{ID: "latest", Name: "Latest", URL: server.URL + "/first?token=private-one", Enabled: true, AllowHTTP: true}
	service, _ := makeServiceWithCurrent(t, server, func(id string) (config.Subscription, bool) {
		return current, id == current.ID
	}, nil, nil)
	if _, err := service.Add(current); err != nil {
		t.Fatal("Add failed")
	}
	if _, err := service.Refresh(context.Background(), current.ID); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	current.URL = server.URL + "/second?token=private-two"
	if _, err := service.Refresh(context.Background(), current.ID); err != nil {
		t.Fatalf("refresh from current config: %v", err)
	}
	if first, second := <-paths, <-paths; first != "/first" || second != "/second" {
		t.Fatalf("fetched paths = %q, %q", first, second)
	}
}

type diskFile struct {
	mode    os.FileMode
	modTime time.Time
	data    []byte
}

func captureFiles(t *testing.T, root string) map[string]diskFile {
	t.Helper()
	files := make(map[string]diskFile)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative] = diskFile{mode: info.Mode().Perm(), modTime: info.ModTime(), data: bytes.Clone(data)}
		return nil
	})
	if err != nil {
		t.Fatal("capturing private state")
	}
	return files
}

func snapshotFiles(files map[string]diskFile) map[string]diskFile {
	snapshots := make(map[string]diskFile)
	for name, file := range files {
		if strings.HasPrefix(name, "profile-") || strings.HasPrefix(name, "candidate-") {
			snapshots[name] = file
		}
	}
	return snapshots
}

func makeService(t *testing.T, server *httptest.Server, render RenderFunc, validate ValidateFunc) (*Store, *Service) {
	service, store := makeServiceWithCurrent(t, server, nil, render, validate)
	return store, service
}

func makeServiceWithCurrent(t *testing.T, server *httptest.Server, current CurrentFunc, render RenderFunc, validate ValidateFunc) (*Service, *Store) {
	t.Helper()
	var apply ApplyFunc = func(context.Context, []byte) error { return nil }
	if render == nil {
		render = func(_ context.Context, profile []byte) ([]byte, error) {
			return append([]byte("generated:\n"), profile...), nil
		}
	}
	if validate == nil {
		validate = func(context.Context, []byte) error { return nil }
	}
	store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
	if err != nil {
		t.Fatal("NewStore failed")
	}
	service, err := NewService(store, Options{
		Transport: func(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error) {
			if route != download.Direct || allowInvalidTLS {
				return nil, fmt.Errorf("unexpected transport intent")
			}
			return server.Client().Transport, nil
		},
		Render:   render,
		Validate: validate,
		Apply:    apply,
		Restore:  func(context.Context, []byte) error { return nil },
		Current:  current,
	})
	if err != nil {
		t.Fatal("NewService failed")
	}
	return service, store
}

func TestRefreshPassesRouteAndTLSIntent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	defer server.Close()
	store, err := NewStore(filepath.Join(t.TempDir(), "subscriptions"))
	if err != nil {
		t.Fatal("NewStore failed")
	}
	var gotRoute download.Route
	var gotInvalidTLS bool
	service, err := NewService(store, Options{
		Transport: func(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error) {
			gotRoute, gotInvalidTLS = route, allowInvalidTLS
			return server.Client().Transport, nil
		},
		Render:   func(context.Context, []byte) ([]byte, error) { return []byte("candidate"), nil },
		Validate: func(context.Context, []byte) error { return nil },
		Apply:    func(context.Context, []byte) error { return nil },
		Restore:  func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal("NewService failed")
	}
	entry, err := service.Add(config.Subscription{
		ID: "intent", URL: server.URL, AllowHTTP: true, Route: string(download.SystemProxy), AllowInvalidTLS: true,
	})
	if err != nil {
		t.Fatal("Add failed")
	}
	if _, err := service.Refresh(context.Background(), entry.ID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if gotRoute != download.SystemProxy || !gotInvalidTLS {
		t.Fatalf("transport intent = (%q, %v), want (system proxy, true)", gotRoute, gotInvalidTLS)
	}
}

func TestCandidateValidationFailureRetainsKnownGoodGeneration(t *testing.T) {
	good := []byte("proxies:\n  - name: known-good\n    type: direct\n")
	rejected := []byte("proxies:\n  - name: reject\n    type: direct\n")
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		count := requests
		mu.Unlock()
		if count == 1 {
			_, _ = w.Write(good)
			return
		}
		_, _ = w.Write(rejected)
	}))
	defer server.Close()
	validate := func(_ context.Context, candidate []byte) error {
		if bytes.Contains(candidate, []byte("name: reject")) {
			return errors.New("validator failure with private URL token")
		}
		return nil
	}
	service, store := makeServiceWithCurrent(t, server, nil, nil, validate)
	entry, err := service.Add(config.Subscription{
		ID: "rollback", URL: server.URL + "?token=private", AllowHTTP: true,
	})
	if err != nil {
		t.Fatal("Add failed")
	}
	first, err := service.Refresh(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	before := captureFiles(t, filepath.Join(store.dir, entry.ID))
	if _, err := service.Refresh(context.Background(), entry.ID); err != ErrValidation || strings.Contains(err.Error(), "private") {
		t.Fatalf("validation failure = %v, want sanitized ErrValidation", err)
	}
	if after := captureFiles(t, filepath.Join(store.dir, entry.ID)); !reflect.DeepEqual(snapshotFiles(after), snapshotFiles(before)) {
		t.Fatal("rejected candidate changed the committed snapshot")
	}
	entries := service.List()
	if len(entries) != 1 || entries[0].Hash != first.Hash || !entries[0].HasSnapshot {
		t.Fatalf("known-good metadata was not retained: %+v", entries)
	}
	var applied []byte
	service.options.Apply = func(_ context.Context, candidate []byte) error {
		applied = bytes.Clone(candidate)
		return nil
	}
	if err := service.Activate(context.Background(), entry.ID); err != nil {
		t.Fatalf("Activate retained generation: %v", err)
	}
	if !bytes.Contains(applied, []byte("name: known-good")) || bytes.Contains(applied, []byte("name: reject")) {
		t.Fatalf("activated candidate was not last-known-good: %q", applied)
	}
}

func TestSubscriptionCRUDPersistsSecretFreeEntries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "subscriptions")
	store, err := NewStore(root)
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
	entry, err := service.Add(config.Subscription{URL: "https://provider.invalid/list?token=private-token"})
	if err != nil || entry.ID == "" {
		t.Fatalf("Add = %+v, %v", entry, err)
	}
	if strings.Contains(fmt.Sprint(entry), "private-token") {
		t.Fatal("Add returned the private URL")
	}
	entry, err = service.Rename(entry.ID, "Renamed")
	if err != nil || entry.Name != "Renamed" {
		t.Fatalf("Rename = %+v, %v", entry, err)
	}
	entry, err = service.SetEnabled(entry.ID, true)
	if err != nil || !entry.Enabled {
		t.Fatalf("SetEnabled = %+v, %v", entry, err)
	}
	store.mu.Lock()
	record := store.records[entry.ID]
	record.Usage = &Usage{Upload: 17, Download: 23, Total: 40}
	store.records[entry.ID] = record
	store.mu.Unlock()
	entriesWithUsage := service.List()
	entriesWithUsage[0].Usage.Upload = 99
	if got := service.List()[0].Usage.Upload; got != 17 {
		t.Fatalf("public usage mutated private snapshot: Upload = %d", got)
	}
	reopened, err := NewStore(root)
	if err != nil {
		t.Fatal("reopening private store failed")
	}
	reloaded, err := NewService(reopened, Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return http.DefaultTransport, nil },
		Render:    func(context.Context, []byte) ([]byte, error) { return []byte("candidate"), nil },
		Validate:  func(context.Context, []byte) error { return nil },
		Apply:     func(context.Context, []byte) error { return nil },
		Restore:   func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal("reopening service failed")
	}
	entries := reloaded.List()
	if len(entries) != 1 || entries[0].ID != entry.ID || entries[0].Name != "Renamed" || !entries[0].Enabled {
		t.Fatalf("reloaded entries = %+v", entries)
	}
	if err := reloaded.Delete(entry.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if entries := reloaded.List(); len(entries) != 0 {
		t.Fatalf("entries after Delete = %+v", entries)
	}
}

func TestRefreshUsageMetadataIsOptionalAndPrivate(t *testing.T) {
	var version int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version++
		if version == 1 {
			w.Header().Set("Subscription-Userinfo", "upload=12; download=8; total=100; expire=1800000000")
		} else {
			w.Header().Set("Subscription-Userinfo", "upload=not-a-number")
		}
		_, _ = fmt.Fprintf(w, "proxies:\n  - name: node-%d\n    type: direct\n", version)
	}))
	defer server.Close()
	_, service := makeService(t, server,
		func(_ context.Context, profile []byte) ([]byte, error) {
			return append([]byte("generated:\n"), profile...), nil
		},
		func(context.Context, []byte) error { return nil })
	if _, err := service.Add(config.Subscription{ID: "usage", URL: server.URL, Enabled: true, AllowHTTP: true}); err != nil {
		t.Fatal(err)
	}
	first, err := service.Refresh(context.Background(), "usage")
	if err != nil || first.Usage == nil || first.Usage.Upload != 12 || first.Usage.Download != 8 || first.Usage.Total != 100 || first.Usage.ExpiresAt != 1800000000 {
		t.Fatalf("usage refresh = %+v, %v", first, err)
	}
	second, err := service.Refresh(context.Background(), "usage")
	if err != nil || !second.Changed || second.Usage != nil {
		t.Fatalf("malformed optional metadata rejected valid profile: %+v, %v", second, err)
	}
	if entries := service.List(); len(entries) != 1 || entries[0].Usage != nil {
		t.Fatalf("stale usage retained after new body: %+v", entries)
	}
}
