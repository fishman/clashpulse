package mihomo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/filters"
	"github.com/fishman/clashpulse/monitor"
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

func TestRenderPutsControllerOnUnixSocketOnly(t *testing.T) {
	profile := []byte("proxies:\n  - name: alpha\n    type: direct\nexternal-controller: 0.0.0.0:9090\nexternal-controller-unix: /tmp/other.sock\nsecret: old\n")
	socket := "/home/user/.local/state/clashpulse/mihomo.sock"
	got, err := Render(profile, config.Snapshot{}, ManagedPaths{}, ControllerSettings{Socket: socket, Secret: "new-secret"}, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	var rendered map[string]any
	if err := yaml.Unmarshal(got, &rendered); err != nil {
		t.Fatal(err)
	}
	if _, tcp := rendered["external-controller"]; tcp || rendered["external-controller-unix"] != socket || rendered["secret"] != "new-secret" {
		t.Fatalf("controller not confined to the private socket: %v", rendered)
	}
	for _, bad := range []string{"relative.sock", "/" + strings.Repeat("a", 108)} {
		if _, err := Render(profile, config.Snapshot{}, ManagedPaths{}, ControllerSettings{Socket: bad, Secret: "s"}, Capability{}); err == nil {
			t.Errorf("socket %q accepted", bad)
		}
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

func TestRenderAppliesURLTestDelaysToOwnedGroupTypesOnly(t *testing.T) {
	profile := []byte("proxies:\n  - name: alpha\n    type: direct\nproxy-groups:\n  - name: auto\n    type: url-test\n    interval: 300\n    proxies: [alpha]\n  - name: backup\n    type: fallback\n    proxies: [alpha]\n  - name: traffic\n    type: select\n    proxies: [alpha]\n")
	controller := ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}
	rendered := func(t *testing.T, delays config.Mihomo) map[string]map[string]any {
		t.Helper()
		got, err := Render(profile, config.Snapshot{Mihomo: delays}, ManagedPaths{}, controller, Capability{})
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Groups []map[string]any `yaml:"proxy-groups"`
		}
		if err := yaml.Unmarshal(got, &document); err != nil {
			t.Fatal(err)
		}
		byName := make(map[string]map[string]any, len(document.Groups))
		for _, group := range document.Groups {
			byName[group["name"].(string)] = group
		}
		return byName
	}

	untouched := rendered(t, config.Mihomo{})
	if untouched["auto"]["interval"] != 300 || untouched["auto"]["tolerance"] != nil {
		t.Fatalf("unset delay settings rewrote the profile: %v", untouched["auto"])
	}
	configured := rendered(t, config.Mihomo{URLTestInterval: 10 * time.Minute, URLTestTolerance: 50 * time.Millisecond})
	if configured["auto"]["interval"] != 600 || configured["auto"]["tolerance"] != 50 {
		t.Fatalf("url-test group delay = %v", configured["auto"])
	}
	if configured["backup"]["interval"] != 600 || configured["backup"]["tolerance"] != 50 {
		t.Fatalf("fallback group delay = %v", configured["backup"])
	}
	if configured["traffic"]["interval"] != nil || configured["traffic"]["tolerance"] != nil {
		t.Fatalf("select group was given delay settings: %v", configured["traffic"])
	}

	generated, err := Render(profile, config.Snapshot{Mihomo: config.Mihomo{URLTestInterval: 10 * time.Minute}}, ManagedPaths{}, controller, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	overrides, err := ExplainOverrides(profile, generated)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, override := range overrides {
		if override.Key == "proxy-groups" {
			found = true
		}
	}
	if !found {
		t.Fatalf("url-test delay change was not reported: %v", overrides)
	}
	plain, err := Render(profile, config.Snapshot{}, ManagedPaths{}, controller, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	if overrides, err = ExplainOverrides(profile, plain); err != nil {
		t.Fatal(err)
	}
	for _, override := range overrides {
		if override.Key == "proxy-groups" {
			t.Fatalf("untouched groups reported as overridden: %v", overrides)
		}
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

func TestExplainOverridesRedactsSourceValues(t *testing.T) {
	profile := []byte("proxies:\n  - name: https://private.example/proxy\n    type: direct\n    password: bearer-private\nsecret: private-controller\nexternal-controller: 0.0.0.0:9090\nexternal-controller-tls: 0.0.0.0:9443\nmixed-port: 7890\nallow-lan: true\nbind-address: '*'\ndns:\n  enable: true\n  listen: 0.0.0.0:53\n  nameserver: [https://private.example/dns]\ncustom-url: https://private.example/config\n")
	original := bytes.Clone(profile)
	generated, err := Render(profile, config.Snapshot{}, nil, ControllerSettings{Address: "127.0.0.1:9090", Secret: "generated-secret", ProxyPort: 7897}, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExplainOverrides(profile, generated)
	if err != nil {
		t.Fatal(err)
	}
	want := []ConfigOverride{{Key: "mixed-port", Change: "replaced"}, {Key: "port", Change: "added"}, {Key: "socks-port", Change: "added"}, {Key: "external-controller", Change: "replaced"}, {Key: "secret", Change: "replaced"}, {Key: "allow-lan", Change: "replaced"}, {Key: "bind-address", Change: "replaced"}, {Key: "dns.listen", Change: "replaced"}, {Key: "external-controller-tls", Change: "removed"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("override fields = %+v, want %+v", got, want)
	}
	encoded, err := yaml.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private.example", "bearer-private", "generated-secret", "private-controller", "0.0.0.0", "7897"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("report exposed a source or generated value")
		}
	}
	if !bytes.Equal(profile, original) {
		t.Fatal("source profile mutated while explaining overrides")
	}
	if _, err := ExplainOverrides([]byte("dns: ["), generated); err == nil || strings.Contains(err.Error(), "dns:") {
		t.Fatalf("malformed source leaked through report error: %v", err)
	}
}

const (
	tunProfile = "proxies:\n  - name: alpha\n    type: direct\n"
	tunDNS     = "dns:\n  nameserver: [192.0.2.53]\n"
)

func renderTun(profile string, intent config.Snapshot) (map[string]any, error) {
	got, err := Render([]byte(tunProfile+profile), intent, nil, ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}, Capability{SupportsTUN: true})
	if err != nil {
		return nil, err
	}
	var document map[string]any
	return document, yaml.Unmarshal(got, &document)
}

func parseYAML(t *testing.T, source string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestRenderTunReplacesDisabledProfileBlock(t *testing.T) {
	tun := config.Tun{Enabled: true, Stack: core.TunStackMixed, DNSMode: core.TunDNSProfile}
	document, err := renderTun("tun:\n  enable: false\n  device: utun9\n  stack: system\n  strict-route: true\n"+tunDNS, config.Snapshot{Tun: tun})
	if err != nil {
		t.Fatal(err)
	}
	want := parseYAML(t, "enable: true\nstack: mixed\ndevice: clashpulse0\nauto-route: true\nauto-detect-interface: true\ndns-hijack: [any:53]\nmtu: 1500\nstrict-route: false\nauto-redirect: false\n")
	if !reflect.DeepEqual(document["tun"], want) {
		t.Fatalf("tun block = %v, want %v", document["tun"], want)
	}
	if _, err := renderTun("tun:\n  enable: true\n", config.Snapshot{Tun: tun}); err == nil {
		t.Fatal("accepted a profile that enables its own TUN")
	}
}

func TestRenderTunRequiresCapability(t *testing.T) {
	intent := config.Snapshot{Tun: config.Tun{Enabled: true, Stack: core.TunStackGVisor, DNSMode: core.TunDNSProfile}}
	_, err := Render([]byte(tunProfile), intent, nil, ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}, Capability{})
	var missing *CapabilityError
	if !errors.As(err, &missing) || missing.ResourceID != "tun" {
		t.Fatalf("missing TUN capability error = %v", err)
	}
}

func TestRenderTunDNSModes(t *testing.T) {
	const (
		base       = "enable: true\nlisten: 127.0.0.1:1053\n"
		fakeIP     = "enhanced-mode: fake-ip\nfake-ip-range: 198.18.0.1/16\nfake-ip-filter: ['*.lan', '*.local', '*.arpa', 'time.*.com', 'ntp.*.com', '+.pool.ntp.org']\n"
		redirHost  = "enhanced-mode: redir-host\n"
		nameserver = "nameserver: [192.0.2.53]\n"
		ownFakeIP  = "fake-ip-range: 198.19.0.1/16\nfake-ip-filter: [+.example.com]\n" + nameserver
	)
	profiles := map[string]string{
		"fake-ip":    "dns:\n  enable: true\n  enhanced-mode: fake-ip\n  fake-ip-range: 198.19.0.1/16\n  fake-ip-filter: [+.example.com]\n  nameserver: [192.0.2.53]\n",
		"redir-host": "dns:\n  enable: true\n  enhanced-mode: redir-host\n  nameserver: [192.0.2.53]\n",
		"disabled":   "dns:\n  enable: false\n  enhanced-mode: redir-host\n  nameserver: [192.0.2.53]\n",
	}
	for _, test := range []struct{ profile, mode, want string }{
		{"fake-ip", core.TunDNSProfile, base + "enhanced-mode: fake-ip\n" + ownFakeIP},
		{"fake-ip", core.TunDNSFakeIP, base + "enhanced-mode: fake-ip\n" + ownFakeIP},
		{"fake-ip", core.TunDNSRedirHost, base + redirHost + ownFakeIP},
		{"redir-host", core.TunDNSProfile, base + redirHost + nameserver},
		{"redir-host", core.TunDNSFakeIP, base + fakeIP + nameserver},
		{"redir-host", core.TunDNSRedirHost, base + redirHost + nameserver},
		{"disabled", core.TunDNSProfile, base + redirHost + nameserver},
		{"disabled", core.TunDNSFakeIP, base + fakeIP + nameserver},
		{"disabled", core.TunDNSRedirHost, base + redirHost + nameserver},
	} {
		t.Run(test.profile+"/"+test.mode, func(t *testing.T) {
			document, err := renderTun(profiles[test.profile], config.Snapshot{Tun: config.Tun{Enabled: true, Stack: core.TunStackGVisor, DNSMode: test.mode}})
			if err != nil {
				t.Fatal(err)
			}
			if want := parseYAML(t, test.want); !reflect.DeepEqual(document["dns"], want) {
				t.Fatalf("dns = %v, want %v", document["dns"], want)
			}
		})
	}
}

func TestRenderTunRequiresUsableNameserver(t *testing.T) {
	for _, test := range []struct{ profile, wantErr string }{
		{"", "dns.nameserver"},
		{"dns:\n  enable: false\n", "dns.nameserver"},
		{"dns:\n  nameserver: []\n", "dns.nameserver"},
		{"dns:\n  nameserver: [https://dns.example/dns-query]\n", "dns.default-nameserver"},
		{"dns:\n  nameserver: [192.0.2.53]\n  fallback: [tls://dns.example]\n", "dns.default-nameserver"},
		{"dns:\n  nameserver: [dns.example]\n  default-nameserver: []\n", "dns.default-nameserver"},
		{"dns:\n  nameserver: [192.0.2.53]\n  nameserver-policy:\n    '+.example.com': 'https://dns.example/dns-query'\n", "dns.default-nameserver"},
		{"dns:\n  nameserver: [192.0.2.53]\n  proxy-server-nameserver: [tls://dns.example]\n", "dns.default-nameserver"},
		{"dns:\n  nameserver: [192.0.2.53]\n  direct-nameserver: [dns.example]\n", "dns.default-nameserver"},
		{"dns:\n  nameserver: [192.0.2.53, 'tls://192.0.2.54:853', 'https://[2001:db8::53]/dns-query']\n  fallback: ['192.0.2.55:53']\n", ""},
		{"dns:\n  nameserver: [https://dns.example/dns-query]\n  default-nameserver: [192.0.2.53]\n", ""},
	} {
		for _, mode := range []string{core.TunDNSProfile, core.TunDNSFakeIP, core.TunDNSRedirHost} {
			_, err := renderTun(test.profile, config.Snapshot{Tun: config.Tun{Enabled: true, Stack: core.TunStackGVisor, DNSMode: mode}})
			if test.wantErr == "" && err != nil {
				t.Fatalf("%s %q rejected: %v", mode, test.profile, err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("%s %q error = %v, want %s rejection", mode, test.profile, err, test.wantErr)
			}
		}
	}
}

func TestRenderTunFakeIPRange6OnlyWithIPv6(t *testing.T) {
	for _, test := range []struct {
		profile string
		want    any
	}{
		{"ipv6: true\n" + tunDNS, "2001:2::0/64"},
		{"dns:\n  ipv6: true\n  nameserver: [192.0.2.53]\n", "2001:2::0/64"},
		{"ipv6: true\ndns:\n  fake-ip-range6: 2001:db8::/64\n  nameserver: [192.0.2.53]\n", "2001:db8::/64"},
		{tunDNS, nil},
		{"ipv6: false\n" + tunDNS, nil},
		{"ipv6: true\ndns:\n  enhanced-mode: redir-host\n  nameserver: [192.0.2.53]\n", nil},
	} {
		document, err := renderTun(test.profile, config.Snapshot{Tun: config.Tun{Enabled: true, Stack: core.TunStackGVisor, DNSMode: core.TunDNSProfile}})
		if err != nil {
			t.Fatal(err)
		}
		section, _ := document["dns"].(map[string]any)
		if got := section["fake-ip-range6"]; got != test.want {
			t.Fatalf("%q fake-ip-range6 = %v, want %v", test.profile, got, test.want)
		}
	}
}

func TestRenderTunRejectsResolverInsideFakeIPRange(t *testing.T) {
	tun := config.Tun{Enabled: true, Stack: core.TunStackGVisor, DNSMode: core.TunDNSFakeIP}
	for _, profile := range []string{
		"dns:\n  nameserver: [198.18.0.53]\n",
		"dns:\n  nameserver: [192.0.2.53]\n  default-nameserver: ['198.18.0.53:53']\n",
		"dns:\n  nameserver: [192.0.2.53]\n  fallback: ['tls://198.18.0.53:853']\n",
		"dns:\n  nameserver: [192.0.2.53]\n  nameserver-policy:\n    '+.example.com': 'https://198.18.0.53/dns-query'\n",
		"dns:\n  nameserver: [192.0.2.53]\n  nameserver-policy:\n    '+.example.com': ['192.0.2.53', '198.18.0.53#DIRECT']\n",
		"dns:\n  fake-ip-range: 198.19.0.1/16\n  nameserver: [198.19.255.1]\n",
		"ipv6: true\ndns:\n  nameserver: ['https://[2001:2::53]/dns-query']\n",
	} {
		if _, err := renderTun(profile, config.Snapshot{Tun: tun}); err == nil || !strings.Contains(err.Error(), "fake-ip range") {
			t.Fatalf("resolver inside the fake-ip range: %q error = %v", profile, err)
		}
	}
	routed := config.Snapshot{Tun: tun, DNS: config.DNS{
		ResolverSets: []config.ResolverSet{{ID: "local", Endpoints: []string{"udp://198.18.0.9:53"}}},
		Routes:       []config.DNSRoute{{Suffix: "example.com", ResolverSet: "local"}},
	}}
	if _, err := renderTun(tunDNS, routed); err == nil || !strings.Contains(err.Error(), "fake-ip range") {
		t.Fatal("accepted DNS route resolver inside the fake-ip range")
	}
	redirHost := config.Snapshot{Tun: config.Tun{Enabled: true, Stack: core.TunStackGVisor, DNSMode: core.TunDNSRedirHost}}
	if _, err := renderTun("dns:\n  nameserver: [198.18.0.53]\n", redirHost); err != nil {
		t.Fatalf("redir-host has no fake-ip range but rejected resolver: %v", err)
	}
}

func TestRenderTunOffIsUnchanged(t *testing.T) {
	profile := []byte(tunProfile + "tun:\n  enable: false\n  stack: system\ndns:\n  enable: false\n  enhanced-mode: redir-host\n  nameserver: [198.18.0.53]\n")
	controller := ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}
	today, err := Render(profile, config.Snapshot{}, nil, controller, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	off := config.Snapshot{Tun: config.Tun{Stack: core.TunStackMixed, DNSMode: core.TunDNSFakeIP}}
	got, err := Render(profile, off, nil, controller, Capability{SupportsTUN: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, today) {
		t.Fatalf("disabled TUN changed output:\n%s\nwant:\n%s", got, today)
	}
}

func TestExplainOverridesReportsTunKeysOnlyWhenChanged(t *testing.T) {
	tunKeys := []string{"tun", "dns.enable", "dns.enhanced-mode", "dns.fake-ip-range", "dns.fake-ip-range6", "dns.fake-ip-filter"}
	reported := func(t *testing.T, profile string) map[string]string {
		t.Helper()
		source := []byte(tunProfile + profile)
		generated, err := Render(source, config.Snapshot{Tun: config.Tun{Enabled: true, Stack: core.TunStackGVisor, DNSMode: core.TunDNSFakeIP}}, nil, ControllerSettings{Address: "127.0.0.1:9090", Secret: "secret"}, Capability{SupportsTUN: true})
		if err != nil {
			t.Fatal(err)
		}
		overrides, err := ExplainOverrides(source, generated)
		if err != nil {
			t.Fatal(err)
		}
		got := make(map[string]string)
		for _, override := range overrides {
			if slices.Contains(tunKeys, override.Key) {
				got[override.Key] = override.Change
			}
		}
		return got
	}
	if got, want := reported(t, tunDNS), map[string]string{"tun": "added", "dns.enable": "added", "dns.enhanced-mode": "added", "dns.fake-ip-range": "added", "dns.fake-ip-filter": "added"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides from empty DNS = %v, want %v", got, want)
	}
	complete := "ipv6: true\ntun:\n  enable: false\ndns:\n  enable: true\n  enhanced-mode: fake-ip\n  fake-ip-range: 198.19.0.1/16\n  fake-ip-range6: 2001:db8::/64\n  fake-ip-filter: [+.example.com]\n  nameserver: [192.0.2.53]\n"
	if got, want := reported(t, complete), map[string]string{"tun": "replaced"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides from complete fake-ip DNS = %v, want %v", got, want)
	}
}

func TestMixedPortReadsGeneratedConfig(t *testing.T) {
	if port, err := MixedPort([]byte("mixed-port: 7899\nport: 0\n")); err != nil || port != 7899 {
		t.Fatalf("MixedPort = %d, %v", port, err)
	}
	for _, generated := range []string{"", "port: 7890\n", "mixed-port: 0\n", "mixed-port: nope\n", ": not yaml"} {
		if _, err := MixedPort([]byte(generated)); err == nil {
			t.Errorf("MixedPort(%q) accepted a config without a usable mixed port", generated)
		}
	}
}

func TestRenderSetsProxyModeAndReportsOverride(t *testing.T) {
	profile := []byte("mode: rule\nproxies:\n  - name: alpha\n    type: direct\n")
	intent := config.Snapshot{Mihomo: config.Mihomo{Mode: core.ProxyModeGlobal}}
	got, err := Render(profile, intent, ManagedPaths{}, ControllerSettings{Address: "127.0.0.1:9090", Secret: "s"}, Capability{})
	if err != nil {
		t.Fatal(err)
	}
	var rendered map[string]any
	if err := yaml.Unmarshal(got, &rendered); err != nil || rendered["mode"] != core.ProxyModeGlobal {
		t.Fatalf("rendered mode = %v, %v", rendered["mode"], err)
	}
	overrides, err := ExplainOverrides(profile, got)
	if err != nil || !slices.Contains(overrides, ConfigOverride{Key: "mode", Change: "replaced"}) {
		t.Fatalf("mode override not reported: %+v, %v", overrides, err)
	}
}

func TestModeChangeDetectsModeOnlyDifference(t *testing.T) {
	before := []byte("mode: rule\nmixed-port: 7899\n")
	if from, to, ok := ModeChange(before, []byte("mixed-port: 7899\nmode: direct\n")); !ok || from != "rule" || to != "direct" {
		t.Fatalf("mode-only change = %q -> %q, %v", from, to, ok)
	}
	for _, after := range []string{"mode: rule\nmixed-port: 7899\n", "mode: global\nmixed-port: 7900\n", "not yaml: ["} {
		if _, _, ok := ModeChange(before, []byte(after)); ok {
			t.Errorf("ModeChange accepted %q", after)
		}
	}
}

func steadyIntent() config.Snapshot {
	var intent config.Snapshot
	intent.Monitor.SwitchPolicy = monitor.SwitchSteadyStream
	return intent
}

var probeListener = ControllerSettings{Address: "127.0.0.1:9090", Secret: "s", ProxyPort: 7899,
	StreamProbe: StreamListener{Port: 17922, Username: "u", Password: "p"}}

func TestRenderAddsStreamProbeOnlyForSteadyStream(t *testing.T) {
	profile := []byte("proxies:\n  - {name: node-a, type: socks5, server: 192.0.2.1, port: 1080}\nproxy-groups:\n  - {name: main, type: select, proxies: [node-a]}\n")
	capability := Capability{SupportsPinnedListener: true}
	out, err := Render(profile, steadyIntent(), nil, probeListener, capability)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Groups    []map[string]any `yaml:"proxy-groups"`
		Listeners []map[string]any `yaml:"listeners"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Groups) != 2 || doc.Groups[1]["name"] != StreamProbeName || doc.Groups[1]["include-all"] != true {
		t.Fatalf("groups = %v", doc.Groups)
	}
	if len(doc.Listeners) != 1 || doc.Listeners[0]["proxy"] != StreamProbeName || doc.Listeners[0]["listen"] != "127.0.0.1" || doc.Listeners[0]["port"] != 17922 {
		t.Fatalf("listeners = %v", doc.Listeners)
	}
	var intent config.Snapshot
	intent.Monitor.SwitchPolicy = monitor.SwitchLowestLatency
	out, err = Render(profile, intent, nil, probeListener, capability)
	if err != nil || bytes.Contains(out, []byte(StreamProbeName)) {
		t.Fatalf("lowest_latency rendered the probe: %v", err)
	}
}

func TestRenderRejectsReservedStreamProbeName(t *testing.T) {
	for name, profile := range map[string]string{
		"proxy": "proxies:\n  - {name: clashpulse-stream-probe, type: direct}\n",
		"group": "proxies:\n  - {name: node-a, type: direct}\nproxy-groups:\n  - {name: clashpulse-stream-probe, type: select, proxies: [node-a]}\n",
	} {
		if _, err := Render([]byte(profile), steadyIntent(), nil, probeListener, Capability{SupportsPinnedListener: true}); err == nil || !strings.Contains(err.Error(), StreamProbeName) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	listener := "proxies:\n  - {name: node-a, type: direct}\nlisteners:\n  - {name: x, type: mixed, port: 1}\n"
	if _, err := Render([]byte(listener), steadyIntent(), nil, probeListener, Capability{SupportsPinnedListener: true}); err == nil {
		t.Fatal("profile listeners accepted")
	}
}

func TestRenderSteadyStreamRequiresPinnedListener(t *testing.T) {
	profile := []byte("proxies:\n  - {name: node-a, type: direct}\n")
	_, err := Render(profile, steadyIntent(), nil, probeListener, Capability{})
	var missing *CapabilityError
	if !errors.As(err, &missing) || missing.ResourceID != "monitor.switch_policy" || missing.Kind != "pinned-listener" {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderSteadyStreamRejectsNonListProxyGroups(t *testing.T) {
	profile := []byte("proxies:\n  - {name: node-a, type: direct}\nproxy-groups: {a: b}\n")
	out, err := Render(profile, steadyIntent(), nil, probeListener, Capability{SupportsPinnedListener: true})
	if err == nil || !strings.Contains(err.Error(), "proxy-groups must be a list") || bytes.Contains(out, []byte(StreamProbeName)) {
		t.Fatalf("non-list proxy-groups: err = %v, out = %s", err, out)
	}
}
