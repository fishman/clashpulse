package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestStageRejectsSymlinkReplacementAndPathTraversalID(t *testing.T) {
	home := t.TempDir()
	victim := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(victim, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("example.com\n"))
	}))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	registry, err := NewRegistry(home, client)
	if err != nil {
		t.Fatal(err)
	}
	resource := config.Resource{ID: "list", Kind: config.ResourceRuleSet, Format: config.FormatText, RuleType: config.RuleDomain, URL: server.URL, Enabled: true}
	plan, err := registry.Stage(context.Background(), config.Snapshot{Resources: []config.Resource{resource}}, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	path := plan.Paths()[resource.ID]
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := plan.Validate(func(string, map[string]string) error { return nil }); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("accepted symlink staged destination: %v", err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "protected" {
		t.Fatalf("symlink target changed to %q, err = %v", got, err)
	}
	if _, err := plan.Commit(); err == nil {
		t.Fatal("committed a symlinked staged resource")
	}
	if err := plan.Abort(); err != nil {
		t.Fatal(err)
	}

	traversal := resource
	traversal.ID = "../../outside"
	if err := Validate(traversal, []byte("example.com\n")); err == nil {
		t.Fatal("accepted path-traversing stable ID")
	}
}
