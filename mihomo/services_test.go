package mihomo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/filters"
)

const serviceProfile = "proxies:\n  - {name: node-a, type: direct}\nproxy-groups:\n  - {name: main, type: select, proxies: [node-a]}\nrules:\n  - MATCH,main\n"

func serviceIntent(services ...config.Service) config.Snapshot {
	for i := range services {
		services[i].CheckInterval, services[i].Failures = config.DefaultServiceInterval, config.DefaultServiceFailures
	}
	return config.Snapshot{Services: services}
}

type renderedServices struct {
	Groups []map[string]any `yaml:"proxy-groups"`
	Rules  []string         `yaml:"rules"`
}

func renderServicesForTest(t *testing.T, profile string, intent config.Snapshot, paths ManagedPaths) renderedServices {
	t.Helper()
	out, err := Render([]byte(profile), intent, paths, probeListener, Capability{SupportsPinnedListener: true})
	if err != nil {
		t.Fatal(err)
	}
	var rendered renderedServices
	if err := yaml.Unmarshal(out, &rendered); err != nil {
		t.Fatal(err)
	}
	return rendered
}

func TestRenderServicesAddsGroupAndRulesAheadOfProfile(t *testing.T) {
	rendered := renderServicesForTest(t, serviceProfile, serviceIntent(
		config.Service{ID: "netflix", Enabled: true},
		config.Service{ID: "claude", Enabled: true},
		config.Service{ID: "spotify", Enabled: false},
	), nil)
	var names []string
	for _, group := range rendered.Groups {
		if name, _ := group["name"].(string); strings.HasPrefix(name, "ClashPulse - ") {
			names = append(names, name)
			if group["type"] != "select" || group["include-all"] != true {
				t.Fatalf("group %v", group)
			}
		}
	}
	if strings.Join(names, ",") != "ClashPulse - Claude,ClashPulse - Netflix" {
		t.Fatalf("service groups = %v (definition order, enabled only)", names)
	}
	if rendered.Rules[0] != "DOMAIN-SUFFIX,claude.ai,ClashPulse - Claude" || rendered.Rules[len(rendered.Rules)-1] != "MATCH,main" {
		t.Fatalf("rules = %v", rendered.Rules)
	}
	for _, rule := range rendered.Rules {
		if strings.Contains(rule, "spotify") {
			t.Fatalf("disabled service rendered rule %q", rule)
		}
	}
}

func TestRenderServicesUsesDomainsResource(t *testing.T) {
	intent := serviceIntent(config.Service{ID: "chatgpt", Enabled: true, DomainsResource: "openai-domains"})
	intent.Resources = []config.Resource{{ID: "openai-domains", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, Enabled: true}}
	path := filepath.Join(t.TempDir(), filters.ManagedFilename(intent.Resources[0]))
	if err := os.WriteFile(path, []byte("payload:\n  - example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rendered := renderServicesForTest(t, serviceProfile, intent, ManagedPaths{"openai-domains": path})
	joined := strings.Join(rendered.Rules, "\n")
	if rendered.Rules[0] != "RULE-SET,managed-openai-domains,ClashPulse - ChatGPT" || strings.Contains(joined, "DOMAIN-SUFFIX,openai.com,") {
		t.Fatalf("domains_resource not used: %v", rendered.Rules)
	}
}

func TestRenderServicesRejectsNameCollision(t *testing.T) {
	profile := "proxies:\n  - {name: node-a, type: direct}\nproxy-groups:\n  - {name: ClashPulse - Netflix, type: select, proxies: [node-a]}\nrules:\n  - MATCH,DIRECT\n"
	_, err := Render([]byte(profile), serviceIntent(config.Service{ID: "netflix", Enabled: true}), nil, probeListener, Capability{SupportsPinnedListener: true})
	if err == nil || !strings.Contains(err.Error(), "netflix") {
		t.Fatalf("collision err = %v", err)
	}
}

func TestServiceGroupFor(t *testing.T) {
	if id, ok := ServiceGroupFor("ClashPulse - YouTube Premium"); !ok || id != "youtube" {
		t.Fatalf("ServiceGroupFor = %q %v", id, ok)
	}
	if _, ok := ServiceGroupFor("Main"); ok {
		t.Fatal("profile group taken for a service group")
	}
}
