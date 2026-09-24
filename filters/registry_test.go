package filters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fishman/clashpulse/config"
)

func TestBuildCarriesExplicitFilterTarget(t *testing.T) {
	resource := config.Resource{ID: "domains", Kind: config.ResourceRuleProvider, Format: config.FormatText, RuleType: config.RuleDomain, Enabled: true}
	path := filepath.Join(t.TempDir(), ManagedFilename(resource))
	if err := os.WriteFile(path, []byte("example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{
		Resources: []config.Resource{resource},
		Filters:   []config.Filter{{ID: "block-domains", Resource: "domains", Format: config.FormatText, Target: "Proxy", Enabled: true}},
	}
	registry, err := Build(snapshot, map[string]string{"domains": path})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := registry.RuleSetReference("block-domains")
	if err != nil || rule != "RULE-SET,managed-domains,Proxy" {
		t.Fatalf("rule = %q, err = %v", rule, err)
	}
	if got := registry.Providers(); len(got) != 1 || got[0].Behavior != "domain" || got[0].Format != "text" {
		t.Fatalf("providers = %#v", got)
	}
}

func TestBuildRejectsCommaInFilterTarget(t *testing.T) {
	snapshot := config.Snapshot{Filters: []config.Filter{{ID: "bad", Target: "Proxy,DIRECT"}}}
	if _, err := Build(snapshot, nil); err == nil {
		t.Fatal("accepted a filter target that injects another rule field")
	}
}
