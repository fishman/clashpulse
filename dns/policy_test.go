package dns

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
)

func TestBuildRoutesOnlyExplicitEnabledDomainRuleSets(t *testing.T) {
	snapshot := config.Snapshot{DNS: config.DNS{
		Listen:       "127.0.0.1:5354",
		ResolverSets: []config.ResolverSet{{ID: "domestic", Endpoints: []string{"udp://192.0.2.53:53"}}},
		Routes:       []config.DNSRoute{{Resource: "cn", ResolverSet: "domestic"}},
	}, Resources: []config.Resource{{
		ID: "cn", Kind: config.ResourceRuleSet, Format: config.FormatText,
		RuleType: config.RuleDomain, Enabled: true,
	}}}
	policy, err := Build(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got := policy["rule-set:managed-cn"]
	if len(got) != 1 || got[0] != "udp://192.0.2.53:53" {
		t.Fatalf("policy = %#v", policy)
	}

	snapshot.Resources[0].RuleType = config.RuleIPCIDR
	if _, err := Build(snapshot); err == nil || !strings.Contains(err.Error(), "domain rule-set") {
		t.Fatalf("accepted non-domain DNS rule-set: %v", err)
	}
}

func TestBuildRejectsResolverLoopToDNSListener(t *testing.T) {
	snapshot := config.Snapshot{DNS: config.DNS{
		Listen:       "127.0.0.1:5353",
		ResolverSets: []config.ResolverSet{{ID: "crypt", DNSCrypt: true, Endpoints: []string{"127.0.0.1:5353"}}},
	}}
	if _, err := Build(snapshot); err == nil || !strings.Contains(err.Error(), "loops back") {
		t.Fatalf("accepted DNS listener loop: %v", err)
	}
}

func TestValidateRejectsUnavailableDNSCryptListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{DNS: config.DNS{
		Listen:       "127.0.0.1:53",
		ResolverSets: []config.ResolverSet{{ID: "crypt", DNSCrypt: true, Endpoints: []string{"tcp://127.0.0.1:" + strconv.Itoa(port)}}},
	}}
	if err := Validate(context.Background(), snapshot, nil); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("accepted unavailable DNSCrypt endpoint: %v", err)
	}
}

func TestBuildRejectsExternalDNSCryptEndpoint(t *testing.T) {
	snapshot := config.Snapshot{DNS: config.DNS{
		Listen:       "127.0.0.1:53",
		ResolverSets: []config.ResolverSet{{ID: "crypt", DNSCrypt: true, Endpoints: []string{"udp://192.0.2.53:5353"}}},
	}}
	if _, err := Build(snapshot); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("accepted non-loopback DNSCrypt endpoint: %v", err)
	}
}
