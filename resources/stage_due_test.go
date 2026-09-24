package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	mu.Lock()
	defer mu.Unlock()
	if counts["/one"] != 2 || counts["/two"] != 1 {
		t.Fatalf("HTTP request counts = %#v", counts)
	}
}
