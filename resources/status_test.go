package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestStatusReportsValidAndUnavailableResourcesIndependently(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("payload:\n  - +.example.com\n"))
	}))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) {
		return server.Client().Transport, nil
	})
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	valid := config.Resource{ID: "present", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, URL: server.URL, Enabled: true}
	plan, err := registry.Stage(context.Background(), config.Snapshot{Resources: []config.Resource{valid}}, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Commit(); err != nil {
		t.Fatal(err)
	}
	missing := valid
	missing.ID = "missing"
	missing.URL = "https://resource.example/missing"
	statuses, err := registry.Status(config.Snapshot{Resources: []config.Resource{valid, missing}})
	if err != nil {
		t.Fatalf("status aborted for unavailable resource: %v", err)
	}
	if len(statuses) != 2 || !statuses[0].Validated || statuses[0].SHA256 == "" || statuses[0].LastSuccess.IsZero() {
		t.Fatalf("valid resource status = %#v", statuses)
	}
	if statuses[1].Validated || statuses[1].LastFailure == "" || statuses[1].Destination == "" {
		t.Fatalf("unavailable resource status = %#v", statuses[1])
	}
	if statuses[0].SourceHost != "127.0.0.1" || statuses[1].SourceHost != "resource.example" {
		t.Fatalf("source hosts = %q and %q", statuses[0].SourceHost, statuses[1].SourceHost)
	}
}
