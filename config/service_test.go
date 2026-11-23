package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestServicesDecodeWithDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[[service]]\nid = \"chatgpt\"\nenabled = true\nregions = [\"US\", \"JP\"]\n")
	snapshot, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.Services
	if len(got) != 1 || got[0].ID != "chatgpt" || !got[0].Enabled || got[0].Automatic ||
		got[0].CheckInterval != DefaultServiceInterval || got[0].Failures != DefaultServiceFailures || strings.Join(got[0].Regions, ",") != "US,JP" {
		t.Fatalf("services = %+v", got)
	}
}

func TestServicesRejectInvalidIntent(t *testing.T) {
	cases := map[string]string{
		"unknown id":        "[[service]]\nid = \"tiktok\"\n",
		"duplicate id":      "[[service]]\nid = \"claude\"\n[[service]]\nid = \"claude\"\n",
		"bad region":        "[[service]]\nid = \"claude\"\nregions = [\"usa\"]\n",
		"lower region":      "[[service]]\nid = \"claude\"\nregions = [\"us\"]\n",
		"short interval":    "[[service]]\nid = \"claude\"\ncheck_interval = \"1m\"\n",
		"failures range":    "[[service]]\nid = \"claude\"\nfailures = 11\n",
		"zero failures":     "[[service]]\nid = \"claude\"\nfailures = 0\n",
		"unknown key":       "[[service]]\nid = \"claude\"\nauto = true\n",
		"dangling resource": "[[service]]\nid = \"claude\"\ndomains_resource = \"missing\"\n",
	}
	for name, body := range cases {
		dir := t.TempDir()
		writeFile(t, dir, "config.toml", body)
		if _, err := Load(dir); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestServicesDomainsResourceMustBeDomainRuleSet(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[[service]]\nid = \"claude\"\ndomains_resource = \"cidrs\"\n")
	writeFile(t, dir, "resources.toml", "[[resource]]\nid = \"cidrs\"\nkind = \"rule-set\"\nformat = \"text\"\nrule_type = \"ipcidr\"\nurl = \"https://example.com/cidrs.txt\"\nenabled = true\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "domains_resource") {
		t.Fatalf("ipcidr resource accepted as service domains: %v", err)
	}
	writeFile(t, dir, "resources.toml", "[[resource]]\nid = \"cidrs\"\nkind = \"rule-set\"\nformat = \"text\"\nrule_type = \"domain\"\nurl = \"https://example.com/domains.txt\"\nenabled = true\n")
	if _, err := Load(dir); err != nil {
		t.Fatalf("domain rule-set rejected: %v", err)
	}
}

func TestServicesChangePublishesServicesSection(t *testing.T) {
	old := Snapshot{}
	next := Snapshot{Services: []Service{{ID: "claude", Enabled: true, CheckInterval: DefaultServiceInterval, Failures: 2}}}
	if got := diffSections(old, next); !slices.Equal(got, []string{"services"}) {
		t.Fatalf("sections = %v", got)
	}
}

func TestPutServiceRoundTrips(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[monitor]\nenabled = false\n")
	path := filepath.Join(dir, "config.toml")
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Service{ID: "netflix", Enabled: true, Regions: []string{"US"}, CheckInterval: 45 * time.Minute, Failures: 3, Automatic: true}
	if err := PutService(path, current, want); err != nil {
		t.Fatal(err)
	}
	next, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Services) != 1 || next.Services[0].ID != "netflix" || !next.Services[0].Automatic || next.Services[0].CheckInterval != 45*time.Minute || next.Monitor.Enabled {
		t.Fatalf("after PutService: %+v monitor %v", next.Services, next.Monitor.Enabled)
	}
	want.Automatic = false
	if err := PutService(path, next, want); err != nil {
		t.Fatal(err)
	}
	if again, err := Load(dir); err != nil || len(again.Services) != 1 || again.Services[0].Automatic {
		t.Fatalf("PutService did not replace in place: %+v %v", again.Services, err)
	}
	if err := PutService(path, next, Service{ID: "tiktok", CheckInterval: DefaultServiceInterval, Failures: 2}); err == nil {
		t.Fatal("PutService wrote an unknown service")
	}
}
