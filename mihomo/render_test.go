package mihomo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/filters"
	"gopkg.in/yaml.v3"
)

func TestRenderPreservesProfileAndControlsLocalController(t *testing.T) {
	profile := []byte("proxies:\n  - name: alpha\n    type: direct\nexternal-controller: 0.0.0.0:9090\nexternal-controller-tls: 0.0.0.0:9443\nexternal-doh-server: /dns-query\nsecret: old\n")
	got, err := Render(profile, config.Snapshot{}, ManagedPaths{}, ControllerSettings{Address: "127.0.0.1:9090", Secret: "new-secret"}, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	var rendered map[string]any
	if err := yaml.Unmarshal(got, &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered["external-controller"] != "127.0.0.1:9090" || rendered["secret"] != "new-secret" || rendered["allow-lan"] != false || rendered["external-controller-tls"] != nil || rendered["external-doh-server"] != nil {
		t.Fatalf("controller settings not private: %v", rendered)
	}
	if !bytes.Contains(got, []byte("alpha")) || bytes.Contains(got, []byte("0.0.0.0:9090")) {
		t.Fatalf("profile lost or unsafe controller retained: %s", got)
	}
}

func TestRenderRejectsMissingManagedResourceAndNeverMutatesSource(t *testing.T) {
	profile := []byte("proxies:\n  - name: alpha\n    type: direct\n")
	s := config.Snapshot{Resources: []config.Resource{{ID: "cn", Kind: "rule-set", Enabled: true}}}
	original := bytes.Clone(profile)
	_, err := Render(profile, s, ManagedPaths{}, ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}, Capability{})
	if err == nil || !strings.Contains(err.Error(), "cn") {
		t.Fatalf("missing resource error = %v", err)
	}
	if !bytes.Equal(profile, original) {
		t.Fatal("source profile was modified")
	}
}

func TestRenderDNSRuleSetUsesStableProvider(t *testing.T) {
	s := config.Snapshot{
		Resources: []config.Resource{{ID: "cn", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, Enabled: true}},
		DNS:       config.DNS{Listen: "127.0.0.1:5354", ResolverSets: []config.ResolverSet{{ID: "domestic", Endpoints: []string{"https://dns.example/dns-query"}}}, Routes: []config.DNSRoute{{Resource: "cn", ResolverSet: "domestic"}}},
	}
	path := filepath.Join(t.TempDir(), filters.ManagedFilename(s.Resources[0]))
	if err := os.WriteFile(path, []byte("payload:\n  - example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Render([]byte("proxy-providers:\n  upstream:\n    type: http\n    url: https://example.com/profile\n"), s, ManagedPaths{"cn": path}, ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		DNS struct {
			Policy map[string][]string `yaml:"nameserver-policy"`
		} `yaml:"dns"`
		Providers map[string]struct {
			Path string `yaml:"path"`
		} `yaml:"rule-providers"`
	}
	if err := yaml.Unmarshal(got, &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered.Providers["managed-cn"].Path != path || len(rendered.DNS.Policy["rule-set:managed-cn"]) != 1 {
		t.Fatalf("managed rule reference absent: %s", got)
	}
}

func TestRenderRejectsMissingGeoCapability(t *testing.T) {
	intent := config.Snapshot{Resources: []config.Resource{{ID: "geo", Kind: "geoip.dat", Enabled: true}}}
	_, err := Render([]byte("proxies:\n  - name: alpha\n    type: direct\n"), intent, ManagedPaths{"geo": "/private/geoip.dat"}, ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}, Capability{})
	if err == nil || !strings.Contains(err.Error(), "geoip.dat") {
		t.Fatalf("unsupported geodata accepted: %v", err)
	}
}

func TestMissingGeoSiteCapabilityNamesResource(t *testing.T) {
	home := t.TempDir()
	intent := config.Snapshot{Resources: []config.Resource{{ID: "geosite", Kind: config.ResourceGeoSite, Format: config.FormatDAT, Enabled: true}}}
	_, err := Render([]byte("proxies:\n  - name: alpha\n    type: direct\n"), intent,
		ManagedPaths{"geosite": filepath.Join(home, "geosite.dat")},
		ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret", HomeDir: home}, Capability{})
	var missing *CapabilityError
	if !errors.As(err, &missing) || missing.ResourceID != "geosite" || missing.Kind != config.ResourceGeoSite {
		t.Fatalf("missing capability lost affected resource: %v", err)
	}
}

func TestRenderGeodataUsesMihomoHomeNames(t *testing.T) {
	intent := config.Snapshot{Resources: []config.Resource{{ID: "geo", Kind: "geoip.dat", Enabled: true}}}
	profile := []byte("proxies:\n  - name: alpha\n    type: direct\n")
	controller := ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret", HomeDir: "/private"}
	capability := Capability{SupportsGeoIPDat: true}
	if _, err := Render(profile, intent, ManagedPaths{"geo": "/private/wrong.dat"}, controller, capability); err == nil {
		t.Fatal("accepted geodata path Mihomo cannot locate")
	}
	if _, err := Render(profile, intent, ManagedPaths{"geo": "/elsewhere/geoip.dat"}, controller, capability); err == nil {
		t.Fatal("accepted geodata outside Mihomo home")
	}
	got, err := Render(profile, intent, ManagedPaths{"geo": "/private/geoip.dat"}, controller, capability)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("geoip-dat:")) {
		t.Fatalf("generated unsupported geoip-dat field: %s", got)
	}
}

func TestValidateUsesSelectedExecutableWithoutExposingProfile(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = -t ] && [ \"$2\" = -f ]\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Validate(context.Background(), Capability{Path: binary}, filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := Validate(context.Background(), Capability{Path: binary}, ""); err == nil {
		t.Fatal("validated empty path")
	}
}

func TestValidateInHomePassesManagedDataDirectory(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mihomo")
	home := filepath.Join(dir, "state")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ \"$1\" = -t ] && [ \"$2\" = -f ] && [ \"$4\" = -d ] && [ \"$5\" = \"$EXPECTED_HOME\" ]\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPECTED_HOME", home)
	if err := ValidateInHome(context.Background(), Capability{Path: binary}, filepath.Join(dir, "generated.yaml"), home); err != nil {
		t.Fatal(err)
	}
}

func TestRenderKeepsSourceDNSOnLoopback(t *testing.T) {
	profile := []byte("proxies:\n  - name: alpha\n    type: direct\ndns:\n  enable: true\n  listen: 0.0.0.0:53\n")
	got, err := Render(profile, config.Snapshot{}, nil, ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		DNS struct {
			Listen string `yaml:"listen"`
		} `yaml:"dns"`
	}
	if err := yaml.Unmarshal(got, &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered.DNS.Listen != "127.0.0.1:1053" {
		t.Fatalf("source DNS listener exposed: %q", rendered.DNS.Listen)
	}
}

func TestRenderPrependsFilterRuleBeforeSourceCatchall(t *testing.T) {
	profile := []byte("proxies:\n  - name: alpha\n    type: direct\nproxy-groups:\n  - name: traffic\n    type: select\n    proxies: [alpha]\nrules:\n  - MATCH,DIRECT\n")
	intent := config.Snapshot{
		Resources: []config.Resource{{ID: "ads", Kind: config.ResourceRuleSet, Format: config.FormatYAML, RuleType: config.RuleDomain, Enabled: true}},
		Filters:   []config.Filter{{ID: "ads", Resource: "ads", Format: config.FormatYAML, Target: "traffic", Enabled: true}},
	}
	controller := ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}
	path := filepath.Join(t.TempDir(), filters.ManagedFilename(intent.Resources[0]))
	if err := os.WriteFile(path, []byte("payload:\n  - example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Render(profile, intent, ManagedPaths{"ads": path}, controller, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(got, &rendered); err != nil {
		t.Fatal(err)
	}
	if len(rendered.Rules) != 2 || rendered.Rules[0] != "RULE-SET,managed-ads,traffic" || rendered.Rules[1] != "MATCH,DIRECT" {
		t.Fatalf("filter was placed after source catchall: %v", rendered.Rules)
	}
	intent.Filters[0].Target = "missing"
	if _, err := Render(profile, intent, ManagedPaths{"ads": path}, controller, Capability{}); err == nil {
		t.Fatal("accepted missing filter target")
	}
}

func TestRenderRejectsUnsupportedTUNAndRemoteListeners(t *testing.T) {
	controller := ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}
	for _, source := range []string{
		"proxies:\n  - name: alpha\n    type: direct\ntun:\n  enable: true\n",
		"proxies:\n  - name: alpha\n    type: direct\nlisteners:\n  - name: remote\n    type: http\n    listen: 0.0.0.0\n    port: 7890\n",
	} {
		if _, err := Render([]byte(source), config.Snapshot{}, nil, controller, Capability{}); err == nil {
			t.Fatalf("accepted unsupported data-plane listener: %s", source)
		}
	}
}
