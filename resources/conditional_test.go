package resources

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

func TestUnchangedRefreshRetainsCommittedGeneration(t *testing.T) {
	body := []byte("payload:\n  - example.com\n")
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			w.Header().Set("ETag", `"version-1"`)
			_, _ = w.Write(body)
		case 2:
			if got := r.Header.Get("If-None-Match"); got != `"version-1"` {
				t.Errorf("304 If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"version-1"`)
			w.WriteHeader(http.StatusNotModified)
		case 3:
			if got := r.Header.Get("If-None-Match"); got != `"version-1"` {
				t.Errorf("identical body If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"version-2"`)
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected request %d", requests)
		}
	}))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) {
		return server.Client().Transport, nil
	})
	resource := config.Resource{
		ID: "domains", Kind: config.ResourceRuleSet, Format: config.FormatYAML,
		RuleType: config.RuleDomain, URL: server.URL, Enabled: true,
	}
	snapshot := config.Snapshot{Resources: []config.Resource{resource}}
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	firstPaths, err := first.Commit()
	if err != nil {
		t.Fatal(err)
	}
	activeHome, err := registry.ActiveHome()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(firstPaths[resource.ID]) != activeHome {
		t.Fatalf("first path home = %q, active home = %q", filepath.Dir(firstPaths[resource.ID]), activeHome)
	}
	resourceInfo, err := os.Stat(firstPaths[resource.ID])
	if err != nil {
		t.Fatal(err)
	}
	initialGenerations, err := os.ReadDir(registry.root)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		plan, err := registry.Stage(context.Background(), snapshot, download.Direct)
		if err != nil {
			t.Fatalf("unchanged stage %d: %v", i, err)
		}
		if plan.Changed() {
			t.Fatalf("unchanged response %d marked resource generation changed", i)
		}
		if plan.Home() != activeHome || plan.Paths()[resource.ID] != firstPaths[resource.ID] {
			t.Fatalf("unchanged stage %d changed home or paths", i)
		}
		if err := plan.Validate(func(string, map[string]string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Commit(); err != nil {
			t.Fatalf("commit unchanged stage %d: %v", i, err)
		}
		gotHome, err := registry.ActiveHome()
		if err != nil || gotHome != activeHome {
			t.Fatalf("active home after unchanged stage %d = %q, err = %v", i, gotHome, err)
		}
		currentInfo, err := os.Stat(firstPaths[resource.ID])
		if err != nil || !currentInfo.ModTime().Equal(resourceInfo.ModTime()) {
			t.Fatalf("unchanged stage %d rewrote active resource file: info=%v err=%v", i, currentInfo, err)
		}
		generations, err := os.ReadDir(registry.root)
		if err != nil || len(generations) != len(initialGenerations) {
			t.Fatalf("unchanged stage %d created a generation: count=%d err=%v", i, len(generations), err)
		}
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}
