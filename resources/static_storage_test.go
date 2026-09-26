package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

func TestStablePathsUseResourceRoot(t *testing.T) {
	root := t.TempDir()
	resources := []config.Resource{
		{ID: "geoip", Kind: config.ResourceGeoIP, Format: config.FormatDAT, Enabled: true},
		{ID: "geosite", Kind: config.ResourceGeoSite, Format: config.FormatDAT, Enabled: true},
		{ID: "country", Kind: config.ResourceMMDB, Format: config.FormatMMDB, Enabled: true},
		{ID: "cn", Kind: config.ResourceRuleSet, Format: config.FormatMRS, RuleType: config.RuleDomain, Enabled: true},
	}
	bodies := [][]byte{[]byte("geoip"), []byte("geosite"), []byte("\xab\xcd\xefMaxMind.com"), {0x28, 0xb5, 0x2f, 0xfd}}
	for i := range resources {
		path := filepath.Join(root, resources[i].ID+".source")
		if err := os.WriteFile(path, bodies[i], 0o600); err != nil {
			t.Fatal(err)
		}
		resources[i].URL = path
	}
	registry, err := NewRegistry(filepath.Join(root, "resources"), localResourceClient())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: resources}
	plan, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	paths, err := plan.Commit()
	if err != nil {
		t.Fatal(err)
	}
	resolvedHome, activePaths, err := registry.PathsWithHome(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedHome != filepath.Join(root, "resources") {
		t.Fatalf("active resource home = %q", resolvedHome)
	}
	for i, resource := range resources {
		path := paths[resource.ID]
		if filepath.Dir(path) != registry.home || activePaths[resource.ID] != path {
			t.Fatalf("%s path = %q, active path = %q, home = %q", resource.ID, path, activePaths[resource.ID], registry.home)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(bodies[i]) {
			t.Fatalf("%s bytes = %q, err = %v", resource.ID, data, err)
		}
	}
	if err := plan.Finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateV1Generation(t *testing.T) {
	home := t.TempDir()
	resources := staticTestResources()
	bodies := [][]byte{[]byte("geoip"), []byte("geosite"), []byte("\xab\xcd\xefMaxMind.com"), {0x28, 0xb5, 0x2f, 0xfd}}
	generation := "gen-00000000000000000000000000000001"
	oldDir := filepath.Join(home, legacyDirName, generation)
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	states := make(map[string]resourceState, len(resources))
	for i := range resources {
		resources[i].URL = "https://example.test/" + resources[i].ID
		path := filepath.Join(oldDir, filename(resources[i]))
		if err := os.WriteFile(path, bodies[i], 0o400); err != nil {
			t.Fatal(err)
		}
		states[resources[i].ID] = staticTestState(resources[i], bodies[i])
	}
	prior := stateDocument{Version: 1, Generation: generation, CommitID: strings.Repeat("a", 32), Resources: states}
	if err := writeStateAtomic(filepath.Join(home, stateFileName), home, prior); err != nil {
		t.Fatal(err)
	}

	registry, err := NewRegistry(home, localResourceClient())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: resources}
	paths, err := registry.Paths(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for i, resource := range resources {
		path := paths[resource.ID]
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != string(bodies[i]) {
			t.Fatalf("migrated %s bytes = %q, err = %v", resource.ID, data, readErr)
		}
		if filepath.Dir(path) != home {
			t.Fatalf("migrated %s path = %q", resource.ID, path)
		}
		if got := registry.active.Resources[resource.ID]; got != states[resource.ID] {
			t.Fatalf("migrated %s state = %+v, want %+v", resource.ID, got, states[resource.ID])
		}
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old generation remains after migration: %v", err)
	}
}
func TestCommittedV2CleansLegacyGenerationAfterCrash(t *testing.T) {
	home := t.TempDir()
	legacyRoot := filepath.Join(home, legacyDirName)
	legacy := filepath.Join(legacyRoot, "gen-00000000000000000000000000000003")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	resource := config.Resource{ID: "geoip", Kind: config.ResourceGeoIP, Format: config.FormatDAT, Enabled: true}
	body := []byte("migrated")
	if err := os.WriteFile(filepath.Join(home, filename(resource)), body, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, filename(resource)), body, 0o400); err != nil {
		t.Fatal(err)
	}
	state := staticTestState(resource, body)
	manifest := stateDocument{Version: 2, CommitID: strings.Repeat("d", 32), Resources: map[string]resourceState{resource.ID: state}}
	if err := writeStateAtomic(filepath.Join(home, stateFileName), home, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(home, localResourceClient()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("committed v2 left migrated legacy bytes: %v", err)
	}
}

func TestRejectCorruptV1Migration(t *testing.T) {
	home := t.TempDir()
	resource := config.Resource{ID: "geoip", Kind: config.ResourceGeoIP, Format: config.FormatDAT, URL: "https://example.test/geoip", Enabled: true}
	generation := "gen-00000000000000000000000000000002"
	oldDir := filepath.Join(home, legacyDirName, generation)
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldBytes := []byte("known-good geoip")
	if err := os.WriteFile(filepath.Join(oldDir, filename(resource)), []byte("corrupt"), 0o400); err != nil {
		t.Fatal(err)
	}
	prior := stateDocument{
		Version: 1, Generation: generation, CommitID: strings.Repeat("b", 32),
		Resources: map[string]resourceState{resource.ID: staticTestState(resource, oldBytes)},
	}
	manifestPath := filepath.Join(home, stateFileName)
	if err := writeStateAtomic(manifestPath, home, prior); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRegistry(home, localResourceClient()); err == nil {
		t.Fatal("accepted corrupted v1 resource during migration")
	}
	if _, err := os.Stat(filepath.Join(oldDir, filename(resource))); err != nil {
		t.Fatalf("corrupt legacy resource was removed: %v", err)
	}
	manifest, err := loadStateFile(manifestPath)
	if err != nil || manifest.Version != 1 || manifest.Generation != generation {
		t.Fatalf("legacy manifest changed after failed migration: %+v, %v", manifest, err)
	}
}

func TestRecoverPartialStaticPromotion(t *testing.T) {
	home := t.TempDir()
	resources := staticTestResources()[:2]
	resources[0].URL = "https://example.test/geoip"
	resources[1].URL = "https://example.test/geosite"
	oldBodies := [][]byte{[]byte("old geoip"), []byte("old geosite")}
	newBodies := [][]byte{[]byte("new geoip"), []byte("new geosite")}
	previousStates := make(map[string]resourceState, len(resources))
	candidateStates := make(map[string]resourceState, len(resources)+1)
	for i, resource := range resources {
		if err := os.WriteFile(filepath.Join(home, filename(resource)), newBodies[i], 0o600); err != nil {
			t.Fatal(err)
		}
		previousStates[resource.ID] = staticTestState(resource, oldBodies[i])
		candidateStates[resource.ID] = staticTestState(resource, newBodies[i])
	}
	newResource := config.Resource{ID: "country", Kind: config.ResourceMMDB, Format: config.FormatMMDB, URL: "https://example.test/country", Enabled: true}
	newBody := []byte("\xab\xcd\xefMaxMind.com")
	if err := os.WriteFile(filepath.Join(home, filename(newResource)), newBody, 0o600); err != nil {
		t.Fatal(err)
	}
	candidateStates[newResource.ID] = staticTestState(newResource, newBody)

	backupDir := filepath.Join(home, ".resources-txn-"+strings.Repeat("0", 32))
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	journalFiles := make([]map[string]any, 0, 3)
	for i, resource := range resources {
		backupName := fmt.Sprintf("file-%08x.bak", i)
		if err := os.WriteFile(filepath.Join(backupDir, backupName), oldBodies[i], 0o600); err != nil {
			t.Fatal(err)
		}
		journalFiles = append(journalFiles, map[string]any{
			"name": filename(resource), "backup": backupName, "had_previous": true,
			"previous_sha256": digest(oldBodies[i]), "candidate_sha256": digest(newBodies[i]),
		})
	}
	journalFiles = append(journalFiles, map[string]any{
		"name": filename(newResource), "had_previous": false, "candidate_sha256": digest(newBody),
	})
	prior := stateDocument{Version: 2, CommitID: strings.Repeat("c", 32), Resources: previousStates}
	candidate := stateDocument{Version: 2, CommitID: strings.Repeat("d", 32), Resources: candidateStates}
	if err := writeStateAtomic(filepath.Join(home, stateFileName), home, prior); err != nil {
		t.Fatal(err)
	}
	journal := map[string]any{
		"version": 1, "phase": "promoting", "backup_dir": filepath.Base(backupDir), "max_bytes": DefaultMaxBytes,
		"previous": prior, "candidate": candidate, "files": journalFiles,
	}
	encoded, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, transactionFileName), encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := NewRegistry(home, localResourceClient())
	if err != nil {
		t.Fatal(err)
	}
	if !registry.RecoveredRollback() {
		t.Fatal("interrupted promotion was not reported to application recovery")
	}
	for i, resource := range resources {
		data, err := os.ReadFile(filepath.Join(home, filename(resource)))
		if err != nil || string(data) != string(oldBodies[i]) {
			t.Fatalf("recovered %s bytes = %q, err = %v", resource.ID, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, filename(newResource))); !os.IsNotExist(err) {
		t.Fatalf("new resource survived rollback: %v", err)
	}
	if _, err := registry.Paths(config.Snapshot{Resources: resources}); err != nil {
		t.Fatalf("recovered manifest does not resolve prior resources: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, transactionFileName)); !os.IsNotExist(err) {
		t.Fatalf("transaction journal remains after recovery: %v", err)
	}
}

func TestAcceptedTransactionSurvivesRestart(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "source.dat")
	if err := os.WriteFile(source, []byte("accepted bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(filepath.Join(home, "resources"), localResourceClient())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: []config.Resource{{ID: "geoip", Kind: config.ResourceGeoIP, Format: config.FormatDAT, URL: source, Enabled: true}}}
	plan, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Commit(); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(registry.home, transactionFileName)
	journal, _, err := readTransaction(registry.home, journalPath)
	if err != nil {
		t.Fatal(err)
	}
	journal.Phase = "accepted"
	if err := writeJSONAtomic(journalPath, registry.home, journal); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewRegistry(registry.home, localResourceClient())
	if err != nil {
		t.Fatal(err)
	}
	if reopened.RecoveredRollback() {
		t.Fatal("accepted transaction was reported as a rollback")
	}
	paths, err := reopened.Paths(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(paths["geoip"]); err != nil || string(got) != "accepted bytes" {
		t.Fatalf("accepted resource lost after restart: %q, %v", got, err)
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("accepted journal was not cleaned: %v", err)
	}
}

func TestPendingResourceTransactionRejectsSecondCommit(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "source.dat")
	if err := os.WriteFile(source, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(filepath.Join(home, "resources"), localResourceClient())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Resources: []config.Resource{{ID: "geoip", Kind: config.ResourceGeoIP, Format: config.FormatDAT, URL: source, Enabled: true}}}
	initial, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := initial.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	second, err := registry.Stage(context.Background(), snapshot, download.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed() {
		t.Fatal("same candidate unexpectedly changed")
	}
	if err := second.ValidateResources(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Commit(); err == nil {
		t.Fatal("overwrote an unfinalized resource transaction")
	}
	if err := first.Rollback(); err != nil {
		t.Fatalf("first plan can no longer roll back: %v", err)
	}
}

func staticTestResources() []config.Resource {
	return []config.Resource{
		{ID: "geoip", Kind: config.ResourceGeoIP, Format: config.FormatDAT, Enabled: true},
		{ID: "geosite", Kind: config.ResourceGeoSite, Format: config.FormatDAT, Enabled: true},
		{ID: "country", Kind: config.ResourceMMDB, Format: config.FormatMMDB, Enabled: true},
		{ID: "cn", Kind: config.ResourceRuleSet, Format: config.FormatMRS, RuleType: config.RuleDomain, Enabled: true},
	}
}

func staticTestState(resource config.Resource, body []byte) resourceState {
	return resourceState{
		SHA256: digest(body), SourceHash: digest([]byte(resource.URL)),
		ETag: `"stable"`, LastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
		Kind: resource.Kind, Format: resource.Format, RuleType: resource.RuleType,
		LastCheckedUnix: 1700000000, LastSuccessUnix: 1700000001,
	}
}

func localResourceClient() *download.Client {
	return download.NewClient(func(download.Route) (http.RoundTripper, error) {
		return http.DefaultTransport, nil
	})
}
