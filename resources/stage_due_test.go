package resources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestStageDueRefreshesOnlyDueResourcesAndCopiesOthers(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	bodies := map[string]string{
		"/one": "example.com\n",
		"/two": "other.example\n",
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		counts[r.URL.Path]++
		_, _ = w.Write([]byte(bodies[r.URL.Path]))
	}))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) {
		return server.Client().Transport, nil
	})
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: []config.Resource{
		{ID: "one", Kind: config.ResourceRuleProvider, Format: config.FormatText, RuleType: config.RuleDomain, URL: server.URL + "/one", Enabled: true},
		{ID: "two", Kind: config.ResourceRuleProvider, Format: config.FormatText, RuleType: config.RuleDomain, URL: server.URL + "/two", Enabled: true},
	}}
	initial, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := initial.Finalize(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	bodies["/one"] = "updated.example\n"
	mu.Unlock()
	candidate, err := registry.StageDue(context.Background(), snapshot, download.Direct, []string{"one"})
	if err != nil {
		t.Fatalf("stage due: %v", err)
	}
	if !candidate.Changed() {
		t.Fatal("updated due resource did not mark generation changed")
	}
	paths := candidate.Paths()
	if len(paths) != 2 {
		t.Fatalf("candidate paths = %#v", paths)
	}
	for id, want := range map[string]string{"one": "updated.example\n", "two": "other.example\n"} {
		got, err := readManaged(paths[id], DefaultMaxBytes)
		if err != nil || string(got) != want {
			t.Fatalf("candidate resource %s = %q, err = %v", id, got, err)
		}
	}
	if err := candidate.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Finalize(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if counts["/one"] != 2 || counts["/two"] != 1 {
		t.Fatalf("HTTP request counts = %#v", counts)
	}
}

func TestStageDueMissingCommittedResourceNamesID(t *testing.T) {
	home := t.TempDir()
	first, second := filepath.Join(home, "first.dat"), filepath.Join(home, "second.dat")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("valid geodata"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry(filepath.Join(home, "managed"), localResourceClient())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: []config.Resource{
		{ID: "one", Kind: config.ResourceGeoIP, Format: config.FormatDAT, URL: first, Enabled: true},
		{ID: "two", Kind: config.ResourceGeoSite, Format: config.FormatDAT, URL: second, Enabled: true},
	}}
	_, err = registry.StageDue(context.Background(), snapshot, download.Direct, []string{"one"})
	var failure *ResourceFailure
	if !errors.As(err, &failure) || failure.ResourceID != "two" {
		t.Fatalf("missing non-due resource lost identity: %v", err)
	}
}
