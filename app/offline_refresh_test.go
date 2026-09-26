package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
)

func TestRefreshAtRefusesConcurrentDesktopOwner(t *testing.T) {
	stateDir := t.TempDir()
	release, err := acquireOwnerLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	err = RefreshAt(t.Context(), t.TempDir(), stateDir, "subscription", "profile")
	if !errors.Is(err, errStateInUse) {
		t.Fatalf("offline refresh ran alongside desktop owner: %v", err)
	}
}

func TestRefreshAtReportsSafeSubscriptionStatusWithoutSourceURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotAcceptable) }))
	defer server.Close()
	configDir, stateDir := t.TempDir(), t.TempDir()
	if err := privateDirectory(configDir); err != nil {
		t.Fatal(err)
	}
	if err := config.Seed(configDir); err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	name, source := "Feed", server.URL+"/profile?token=private-token"
	enabled, allowHTTP := true, true
	interval, timeout := time.Hour, 3*time.Second
	if err := config.PatchSubscription(filepath.Join(configDir, "subscriptions.toml"), initial, "feed", config.SubscriptionEdit{
		Name: &name, URL: &source, Enabled: &enabled, AllowHTTP: &allowHTTP, RefreshInterval: &interval, Timeout: &timeout,
	}); err != nil {
		t.Fatal(err)
	}
	err = RefreshAt(t.Context(), configDir, stateDir, "subscription", "feed")
	if err == nil || !strings.Contains(err.Error(), "subscription feed") || !strings.Contains(err.Error(), "HTTP 406") || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("refresh error was not source-scoped and sanitized: %v", err)
	}
}
