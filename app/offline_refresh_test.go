package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte("gateway denied"))
	}))
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
	if err == nil || !strings.Contains(err.Error(), "subscription feed") || !strings.Contains(err.Error(), "HTTP 406 Not Acceptable") || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("refresh error was not source-scoped and sanitized: %v", err)
	}
	status, ok := download.StatusErrorFrom(err)
	if !ok || status.ResponseBody != "" {
		t.Fatalf("default refresh captured HTTP body: %+v, %v", status, err)
	}
	err = RefreshAtWithOptions(t.Context(), configDir, stateDir, "subscription", "feed", RefreshOptions{ShowResponse: true})
	status, ok = download.StatusErrorFrom(err)
	if !ok || status.ResponseBody != "gateway denied" {
		t.Fatalf("explicit response option did not retain HTTP body: %+v, %v", status, err)
	}
	initial, err = config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRuntimeService(configDir, stateDir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if len(service.snapshot.Errors) != 1 || service.snapshot.Errors[0].SourceID != "feed" || !strings.Contains(service.snapshot.Errors[0].Message, "HTTP 406") {
		t.Fatalf("persisted failure absent from startup snapshot: %+v", service.snapshot.Errors)
	}

}

func TestRefreshAllAtAttemptsEveryEnabledSubscription(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNotAcceptable)
	}))
	defer server.Close()
	configDir, stateDir := t.TempDir(), t.TempDir()
	if err := privateDirectory(configDir); err != nil {
		t.Fatal(err)
	}
	if err := config.Seed(configDir); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id      string
		enabled bool
	}{{"alpha", true}, {"beta", true}, {"disabled", false}} {
		initial, err := config.Load(configDir)
		if err != nil {
			t.Fatal(err)
		}
		name, source := item.id, server.URL+"/profile?token="+item.id
		allowHTTP := true
		interval, timeout := time.Hour, 3*time.Second
		if err := config.PatchSubscription(filepath.Join(configDir, "subscriptions.toml"), initial, item.id, config.SubscriptionEdit{
			Name: &name, URL: &source, Enabled: &item.enabled, AllowHTTP: &allowHTTP, RefreshInterval: &interval, Timeout: &timeout,
		}); err != nil {
			t.Fatal(err)
		}
	}
	err := RefreshAllAt(t.Context(), configDir, stateDir)
	if attempts.Load() != 2 || err == nil || !strings.Contains(err.Error(), "subscription alpha") || !strings.Contains(err.Error(), "subscription beta") || strings.Contains(err.Error(), "token=") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("refresh-all attempts=%d error=%v", attempts.Load(), err)
	}
}

func TestDirectRefreshFailurePreservesHTTPStatus(t *testing.T) {
	err := directRefreshFailure("subscription", "feed", &download.StatusError{Code: http.StatusTooManyRequests})
	if err == nil || !strings.Contains(err.Error(), "HTTP 429 Too Many Requests") {
		t.Fatalf("HTTP status was hidden from refresh failure: %v", err)
	}
}
