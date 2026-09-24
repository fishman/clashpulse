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
)

func TestDeleteQuarantinesBeforeReportingCleanupFailure(t *testing.T) {
	profile := []byte("proxies:\n  - name: retained-private\n    type: direct\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(profile) }))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "subscriptions")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, appliedTestOptions(t, server, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	item := config.Subscription{ID: "retired", URL: server.URL + "/profile?token=private", Enabled: true, AllowHTTP: true}
	if _, err := service.Add(item); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	store.removeAll = func(string) error { return errors.New("private filesystem detail") }

	err = service.Delete(item.ID)
	if !errors.Is(err, ErrCleanupPending) || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "private filesystem detail") {
		t.Fatalf("Delete error = %v; want sanitized cleanup-pending error", err)
	}
	if entries := service.List(); len(entries) != 0 {
		t.Fatalf("logically deleted subscription remains visible: %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, item.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live record path remains after quarantine: %v", err)
	}

	reopened, err := NewStore(root)
	if err != nil {
		t.Fatalf("restart should clean quarantine: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("quarantine leftovers after restart = %v, %v", entries, err)
	}
	if _, err := reopened.Profile(item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted private snapshot reappeared after restart: %v", err)
	}
}
