package app

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
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/subscriptions"
)

func TestFailedMultiDeleteDoesNotRecreateRemovedSourceEmpty(t *testing.T) {
	profiles := map[string][]byte{
		"/a": []byte("proxies:\n  - name: retired-a\n    type: direct\n"),
		"/b": []byte("proxies:\n  - name: retired-b\n    type: direct\n"),
		"/c": []byte("proxies:\n  - name: kept-c\n    type: direct\n"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(profiles[r.URL.Path])
	}))
	defer server.Close()

	stateDir := filepath.Join(t.TempDir(), "subscriptions")
	privateStore, err := subscriptions.NewStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := subscriptions.NewService(privateStore, subscriptions.Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return http.DefaultTransport, nil },
		Render:    func(_ context.Context, source []byte) ([]byte, error) { return source, nil },
		Validate:  func(context.Context, []byte) error { return nil },
		Apply:     func(context.Context, []byte) error { return nil },
		Restore:   func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	before := config.Snapshot{}
	for _, id := range []string{"a", "b", "c"} {
		item := config.Subscription{ID: id, URL: server.URL + "/" + id + "?token=private", Enabled: true, AllowHTTP: true}
		if _, err := service.Add(item); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Refresh(context.Background(), id); err != nil {
			t.Fatalf("refresh %s: %v", id, err)
		}
		before.Subscriptions = append(before.Subscriptions, item)
	}
	if profile, err := service.Profile("c"); err != nil || !bytes.Equal(profile, profiles["/c"]) {
		t.Fatalf("initial last-known-good profile = %q, %v", profile, err)
	}

	// Simulate a later physical removal failure after the first ID is removed.
	if err := os.RemoveAll(filepath.Join(stateDir, "b")); err != nil {
		t.Fatal(err)
	}
	after := before
	serverIPC, err := ipc.NewServer(ipc.ServerOptions{Handler: func(context.Context, ipc.Command) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer serverIPC.Close()
	after.Subscriptions = []config.Subscription{before.Subscriptions[2]}
	runtime := &runtimeService{
		store: config.NewStore(before), lastAppliedSettings: before, subs: service, server: serverIPC,
		configErrors: make(chan error, 1),
	}
	runtime.store.Replace(after)
	if err := runtime.applyChange(context.Background(), config.Change{}); err != nil {
		t.Fatalf("applyChange returned %v", err)
	}

	if got := runtime.store.Snapshot().Subscriptions; len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("failed deletion restored removed config: %+v", got)
	}
	if runtime.lastAppliedSettings.Subscriptions[0].ID != "c" {
		t.Fatalf("last applied subscriptions = %+v", runtime.lastAppliedSettings.Subscriptions)
	}
	warning := <-runtime.configErrors
	if warning == nil || !strings.Contains(warning.Error(), subscriptions.ErrStore.Error()) || strings.Contains(warning.Error(), "token=private") || strings.Contains(warning.Error(), server.URL) {
		t.Fatalf("cleanup failure warning = %v; want sanitized store error", warning)
	}
	runtime.reportError("config", warning)
	if len(runtime.snapshot.Errors) != 1 || !strings.Contains(runtime.snapshot.Errors[0].Message, "new settings remain active") || strings.Contains(runtime.snapshot.Errors[0].Message, "previous settings remain active") {
		t.Fatalf("partial deletion warning misreported active settings: %+v", runtime.snapshot.Errors)
	}

	reopened, err := subscriptions.NewStore(stateDir)
	if err != nil {
		t.Fatalf("restart private store: %v", err)
	}
	if _, err := reopened.Profile("a"); !errors.Is(err, subscriptions.ErrNotFound) {
		t.Fatalf("removed source reappeared after restart: %v", err)
	}
	if profile, err := reopened.Profile("c"); err != nil || !bytes.Equal(profile, profiles["/c"]) {
		t.Fatalf("surviving source last-known-good profile = %q, %v", profile, err)
	}
}
