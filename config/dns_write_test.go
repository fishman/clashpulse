package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceDNSRoutingPreservesResourcesAndRejectsDanglingRoute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resources.toml")
	source := filepath.Join(dir, "cn.yaml")
	if err := Write(path, []byte("[[resource]]\nid=\"cn\"\nkind=\"rule-set\"\nformat=\"yaml\"\nrule_type=\"domain\"\nurl=\""+source+"\"\nenabled=true\n")); err != nil {
		t.Fatal(err)
	}
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	sets := []ResolverSet{{ID: "domestic", Endpoints: []string{"udp://127.0.0.1:5353"}, DNSCrypt: true}}
	routes := []DNSRoute{{Resource: "cn", ResolverSet: "domestic"}}
	if err := ReplaceDNSRouting(path, current, sets, routes); err != nil {
		t.Fatal(err)
	}
	next, err := Load(dir)
	if err != nil || len(next.Resources) != 1 || next.Resources[0].URL != source || len(next.DNS.Routes) != 1 || next.DNS.Routes[0].Resource != "cn" {
		t.Fatalf("DNS edit lost resource or route: %+v, %v", next, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	routes[0].Resource = "unknown"
	if err := ReplaceDNSRouting(path, next, sets, routes); err == nil {
		t.Fatal("dangling resource accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("invalid DNS route replaced known-good config")
	}
}

func TestPatchDNSListenerRejectsNonLoopbackAndKeepsPriorIntent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	listener := "127.0.0.1:5354"
	if err := PatchSettings(path, current, SettingsPatch{DNSListen: &listener}); err != nil {
		t.Fatal(err)
	}
	next, err := Load(dir)
	if err != nil || next.DNS.Listen != listener {
		t.Fatalf("DNS listener not persisted: %+v, %v", next.DNS, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unsafe := "0.0.0.0:53"
	if err := PatchSettings(path, next, SettingsPatch{DNSListen: &unsafe}); err == nil {
		t.Fatal("nonloopback DNS listener accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("invalid listener replaced known-good config")
	}
}
