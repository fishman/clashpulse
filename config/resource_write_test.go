package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func resourceWriteFixture(t *testing.T) (string, Snapshot) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "resources.toml")
	contents := `[[resource]]
id = "cn"
kind = "rule-set"
format = "yaml"
rule_type = "domain"
url = "https://lists.invalid/cn.yaml?token=private"
enabled = true
interval = "12h"

[[resource]]
id = "feed"
kind = "rule-provider"
format = "yaml"
rule_type = "domain"
url = "https://lists.invalid/feed.yaml?token=private"
enabled = false
interval = "6h"

[[resolver_set]]
id = "domestic"
endpoints = ["https://dns.invalid/dns-query"]

[[dns_route]]
resource = "cn"
resolver_set = "domestic"
`
	if err := Write(path, []byte(contents)); err != nil {
		t.Fatal(err)
	}
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return path, current
}

func TestPatchResourceRetainsSourceAndDNSRoute(t *testing.T) {
	path, current := resourceWriteFixture(t)
	enabled := true
	interval := 24 * time.Hour
	if err := PatchResource(path, current, "feed", ResourceEdit{Enabled: &enabled, Interval: &interval}); err != nil {
		t.Fatal(err)
	}
	next, err := Load(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	resource := next.Resources[1]
	if resource.URL != current.Resources[1].URL || !resource.Enabled || resource.Interval != interval {
		t.Fatalf("resource update lost source or fields: %+v", resource)
	}
	if len(next.DNS.Routes) != 1 || next.DNS.Routes[0].Resource != "cn" || next.DNS.Routes[0].ResolverSet != "domestic" {
		t.Fatalf("resource update changed DNS route: %+v", next.DNS.Routes)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	badPin := "not-a-sha256-pin"
	badSource := "http://user:pass@lists.invalid/feed.yaml"
	for _, patch := range []ResourceEdit{{SHA256: &badPin}, {URL: &badSource}} {
		if err := PatchResource(path, next, "feed", patch); err == nil {
			t.Fatal("invalid resource edit accepted")
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatal("invalid resource edit changed file")
		}
	}
}

func TestPatchResourceRequiresSourceForNewResource(t *testing.T) {
	path, current := resourceWriteFixture(t)
	kind, format, ruleType := ResourceRuleProvider, FormatText, RuleDomain
	patch := ResourceEdit{Kind: &kind, Format: &format, RuleType: &ruleType}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := PatchResource(path, current, "extra", patch); err == nil {
		t.Fatal("new resource without source accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("rejected resource creation changed file")
	}

	source := "https://lists.invalid/extra.txt"
	patch.URL = &source
	if err := PatchResource(path, current, "extra", patch); err != nil {
		t.Fatal(err)
	}
	next, err := Load(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Resources) != 3 || next.Resources[2].ID != "extra" || next.Resources[2].URL != source || next.Resources[2].Interval != 12*time.Hour {
		t.Fatalf("new resource was not persisted with defaults: %+v", next.Resources)
	}
	if err := DeleteResource(path, next, "extra"); err != nil {
		t.Fatal(err)
	}
	deleted, err := Load(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Resources) != 2 {
		t.Fatalf("resource deletion did not persist: %+v", deleted.Resources)
	}
}

func TestDeleteResourceRejectsDanglingDNSRoute(t *testing.T) {
	path, current := resourceWriteFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteResource(path, current, "cn"); err == nil {
		t.Fatal("resource referenced by DNS route was deleted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("rejected resource deletion changed file")
	}
}
