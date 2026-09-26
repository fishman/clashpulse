package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestPinnedResourceMismatchRetainsPreviousGeneration(t *testing.T) {
	knownGood := []byte("payload:\n  - +.example.com\n")
	badUpdate := []byte("payload:\n  - +.other.example\n")
	body := knownGood
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	transport := server.Client().Transport
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) {
		return transport, nil
	})
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	goodHash := sha256.Sum256(knownGood)
	resource := config.Resource{
		ID: "domain-list", Kind: config.ResourceRuleSet, Format: config.FormatYAML,
		RuleType: config.RuleDomain, URL: server.URL, Enabled: true,
		SHA256: hex.EncodeToString(goodHash[:]),
	}
	snapshot := config.Snapshot{Resources: []config.Resource{resource}}
	first, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Commit(); !errors.Is(err, ErrPlanUnvalidated) {
		t.Fatalf("committed unvalidated plan: %v", err)
	}
	if err := first.Validate(func(home string, paths map[string]string) error {
		if home != first.Home() || paths[resource.ID] == "" {
			t.Fatal("candidate validator received incomplete generation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	paths, err := first.Commit()
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	if err := first.Finalize(); err != nil {
		t.Fatal(err)
	}
	path := paths[resource.ID]
	before, err := os.ReadFile(path)
	if err != nil || string(before) != string(knownGood) {
		t.Fatalf("initial content = %q, err = %v", before, err)
	}
	oldHome := filepath.Dir(path)

	body = badUpdate
	resource.SHA256 = strings.Repeat("0", 64)
	snapshot.Resources[0] = resource
	_, err = registry.Stage(context.Background(), snapshot, download.Direct)
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	activePaths, err := registry.Paths(config.Snapshot{Resources: []config.Resource{{
		ID: "domain-list", Kind: config.ResourceRuleSet, Format: config.FormatYAML,
		RuleType: config.RuleDomain, URL: server.URL, Enabled: true,
	}}})
	if err != nil {
		t.Fatalf("active generation after mismatch: %v", err)
	}
	if filepath.Dir(activePaths["domain-list"]) != oldHome {
		t.Fatalf("active generation changed after pin mismatch: %q -> %q", oldHome, activePaths["domain-list"])
	}
	statusResource := resource
	statusResource.SHA256 = ""
	statuses, err := registry.Status(config.Snapshot{Resources: []config.Resource{statusResource}})
	if err != nil {
		t.Fatalf("resource status: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Validated || statuses[0].SHA256 != hex.EncodeToString(goodHash[:]) || statuses[0].LastSuccess.IsZero() || statuses[0].LastFailure != ErrPinMismatch.Error() {
		t.Fatalf("resource status = %+v", statuses)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(knownGood) {
		t.Fatalf("known-good content after mismatch = %q, err = %v", after, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("committed file mode = %v, err = %v", info, err)
	}
}

func TestStageDueAttemptsEveryResourceBeforeReturningFailure(t *testing.T) {
	var paths []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("payload:\n  - +.example.com\n"))
	}))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: []config.Resource{
		{ID: "bad", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, URL: server.URL + "/bad", Enabled: true},
		{ID: "good", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, URL: server.URL + "/good", Enabled: true},
	}}
	_, err = registry.StageDue(context.Background(), snapshot, download.Direct, []string{"bad", "good"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || len(paths) != 2 || paths[0] != "/bad" || paths[1] != "/good" {
		t.Fatalf("resource batch paths=%v error=%v", paths, err)
	}
	if home, err := registry.ActiveHome(); err != nil || home != registry.home {
		t.Fatalf("resource root after failed batch = %q, err = %v", home, err)
	}
}

func TestChangedSourceDoesNotValidatePreviousGeneration(t *testing.T) {
	knownGood := []byte("payload:\n  - +.example.com\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(knownGood)
	}))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) {
		return server.Client().Transport, nil
	})
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	resource := config.Resource{
		ID: "domains", Kind: config.ResourceRuleSet, Format: config.FormatYAML,
		RuleType: config.RuleDomain, URL: server.URL, Enabled: true,
	}
	oldSnapshot := config.Snapshot{Resources: []config.Resource{resource}}
	plan, err := registry.Stage(context.Background(), oldSnapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	oldPaths, err := plan.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Finalize(); err != nil {
		t.Fatal(err)
	}

	resource.URL += "/changed-source"
	changedSnapshot := config.Snapshot{Resources: []config.Resource{resource}}
	if _, _, err := registry.PathsWithHome(changedSnapshot); err == nil {
		t.Fatal("resolved previous bytes for a changed source")
	}
	statuses, err := registry.Status(changedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Validated {
		t.Fatalf("changed-source status = %+v", statuses)
	}

	oldCurrent, err := registry.Paths(oldSnapshot)
	if err != nil || oldCurrent[resource.ID] != oldPaths[resource.ID] {
		t.Fatalf("old active paths = %v, err = %v", oldCurrent, err)
	}
	data, err := os.ReadFile(oldPaths[resource.ID])
	if err != nil || string(data) != string(knownGood) {
		t.Fatalf("old active bytes = %q, err = %v", data, err)
	}
}

func TestCandidateValidationFailurePreservesCommittedGeneration(t *testing.T) {
	body := []byte("payload:\n  - +.example.com\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	resource := config.Resource{ID: "domains", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, URL: server.URL, Enabled: true}
	snapshot := config.Snapshot{Resources: []config.Resource{resource}}
	first, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	oldPaths, err := first.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Finalize(); err != nil {
		t.Fatal(err)
	}

	body = []byte("payload:\n  - +.new.example\n")
	second, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Validate(func(string, map[string]string) error { return errors.New("mihomo config rejected") }); err == nil {
		t.Fatal("accepted failed candidate validation")
	}
	if _, err := second.Commit(); !errors.Is(err, ErrPlanUnvalidated) {
		t.Fatalf("failed validation remained committable: %v", err)
	}
	current, err := registry.Paths(snapshot)
	if err != nil || current[resource.ID] != oldPaths[resource.ID] {
		t.Fatalf("active paths after validation error = %v, err = %v", current, err)
	}
	if err := second.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackRestoresPreviousGenerationAfterReadinessFailure(t *testing.T) {
	body := []byte("payload:\n  - +.example.com\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	resource := config.Resource{ID: "domains", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, URL: server.URL, Enabled: true}
	snapshot := config.Snapshot{Resources: []config.Resource{resource}}
	first, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	oldPaths, err := first.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Finalize(); err != nil {
		t.Fatal(err)
	}
	body = []byte("payload:\n  - +.other.example\n")
	second, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Validate(func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := second.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := second.Abort(); err != nil {
		t.Fatal(err)
	}
	current, err := registry.Paths(snapshot)
	if err != nil || current[resource.ID] != oldPaths[resource.ID] {
		t.Fatalf("paths after rollback = %v, err = %v", current, err)
	}
}

func TestStaticPromotionsKeepStablePathsAndRollbackBytes(t *testing.T) {
	bodies := [][]byte{
		[]byte("payload:\n  - +.one.example\n"),
		[]byte("payload:\n  - +.two.example\n"),
		[]byte("payload:\n  - +.three.example\n"),
	}
	body := bodies[0]
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	foreignDirectory := filepath.Join(registry.home, "leave-alone")
	if err := os.Mkdir(foreignDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	resource := config.Resource{ID: "domains", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, URL: server.URL, Enabled: true}
	snapshot := config.Snapshot{Resources: []config.Resource{resource}}
	var stablePath string
	for i, currentBody := range bodies {
		body = currentBody
		plan, err := registry.Stage(context.Background(), snapshot, download.Direct)
		if err != nil {
			t.Fatalf("stage promotion %d: %v", i, err)
		}
		if err := plan.ValidateResources(); err != nil {
			t.Fatalf("validate promotion %d: %v", i, err)
		}
		paths, err := plan.Commit()
		if err != nil {
			t.Fatalf("commit promotion %d: %v", i, err)
		}
		activeHome, err := registry.ActiveHome()
		if err != nil || activeHome != registry.home || plan.Home() != activeHome || filepath.Dir(paths[resource.ID]) != activeHome {
			t.Fatalf("active resource root after promotion %d = %q, paths=%v, err=%v", i, activeHome, paths, err)
		}
		if i == 0 {
			stablePath = paths[resource.ID]
		} else if paths[resource.ID] != stablePath {
			t.Fatalf("resource path changed after promotion %d: %q -> %q", i, stablePath, paths[resource.ID])
		}
		activeBytes, err := os.ReadFile(stablePath)
		if err != nil || string(activeBytes) != string(currentBody) {
			t.Fatalf("active resource after promotion %d = %q, err=%v", i, activeBytes, err)
		}
		if err := plan.Finalize(); err != nil {
			t.Fatalf("finalize promotion %d: %v", i, err)
		}
	}

	body = []byte("payload:\n  - +.rollback.example\n")
	rollbackPlan, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatalf("stage rollback candidate: %v", err)
	}
	if err := rollbackPlan.ValidateResources(); err != nil {
		t.Fatalf("validate rollback candidate: %v", err)
	}
	rollbackPaths, err := rollbackPlan.Commit()
	if err != nil {
		t.Fatalf("commit rollback candidate: %v", err)
	}
	if rollbackPaths[resource.ID] != stablePath {
		t.Fatalf("rollback candidate path = %q, want %q", rollbackPaths[resource.ID], stablePath)
	}
	if err := rollbackPlan.Rollback(); err != nil {
		t.Fatalf("rollback candidate: %v", err)
	}
	if err := rollbackPlan.Abort(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(stablePath); err != nil || string(got) != string(bodies[len(bodies)-1]) {
		t.Fatalf("resource after rollback = %q, err = %v", got, err)
	}
	if info, err := os.Stat(foreignDirectory); err != nil || !info.IsDir() {
		t.Fatalf("unmanaged resource-root entry was removed: info=%v, err=%v", info, err)
	}
}

func TestStageDueRetainsCapturedSuccessfulResponseOnValidationFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not a rule-set"))
	}))
	defer server.Close()
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	client.SetCaptureErrorBody(true)
	registry, err := NewRegistry(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: []config.Resource{{
		ID: "domains", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, URL: server.URL, Enabled: true,
	}}}
	_, err = registry.StageDue(context.Background(), snapshot, download.Direct, []string{"domains"})
	response, ok := download.HTTPResponseFrom(err)
	if err == nil || !ok || response.Code != http.StatusOK || response.ResponseBody != "not a rule-set" {
		t.Fatalf("successful HTTP response disappeared from resource validation error: %+v, %v", response, err)
	}
}
