package core

import (
	"strings"
	"testing"
)

func TestSnapshotCopiesSlices(t *testing.T) {
	groups := []GroupSnapshot{{ID: "auto", Selected: "alpha"}}
	snapshot := NewSnapshot(groups)
	groups[0].Selected = "beta"
	if snapshot.Groups[0].Selected != "alpha" {
		t.Fatal("snapshot retained caller-owned memory")
	}
}

func TestCloneSnapshotSeparatesNestedClientState(t *testing.T) {
	usage := &UsageSnapshot{TotalBytes: 10}
	source := Snapshot{
		Groups:        []GroupSnapshot{{ID: "auto", Proxies: []string{"alpha"}}},
		Subscriptions: []SubscriptionSnapshot{{ID: "sub", Usage: usage}},
		Binary:        BinarySnapshot{Capabilities: []string{"geosite.dat"}},
		Switches:      []SwitchSnapshot{{GroupID: "group", Evidence: []ProbeSnapshot{{ProxyID: "alpha", LatencyMillis: 251}}}},
	}
	copy := CloneSnapshot(source)
	source.Groups[0].Proxies[0] = "other"
	usage.TotalBytes = 11
	source.Binary.Capabilities[0] = "other"
	source.Switches[0].Evidence[0].LatencyMillis = 1
	if copy.Groups[0].Proxies[0] != "alpha" || copy.Subscriptions[0].Usage.TotalBytes != 10 || copy.Binary.Capabilities[0] != "geosite.dat" || copy.Switches[0].Evidence[0].LatencyMillis != 251 {
		t.Fatalf("snapshot retained caller-owned nested state: %+v", copy)
	}
}

func TestSnapshotCopiesDNSResolverEndpoints(t *testing.T) {
	source := Snapshot{DNS: DNSSnapshot{ResolverSets: []ResolverSetSnapshot{{ID: "domestic", Endpoints: []string{"udp://127.0.0.1:5353"}}}}}
	copy := CloneSnapshot(source)
	source.DNS.ResolverSets[0].Endpoints[0] = "udp://127.0.0.1:53"
	if copy.DNS.ResolverSets[0].Endpoints[0] != "udp://127.0.0.1:5353" {
		t.Fatal("IPC snapshot retained mutable resolver endpoint")
	}
}

func TestSnapshotClonesDiagnostics(t *testing.T) {
	source := Snapshot{Diagnostics: []DiagnosticSnapshot{{At: 1, Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"}}}
	copy := CloneSnapshot(source)
	source.Diagnostics[0].Message = "changed"
	if copy.Diagnostics[0].Message != "HTTP 406" {
		t.Fatal("IPC snapshot retained mutable diagnostic entry")
	}
}

func TestConfigOverrideSnapshotIsImmutable(t *testing.T) {
	source := Snapshot{ConfigOverrides: []ConfigOverrideSnapshot{{Key: "dns.listen", Change: "replaced"}}}
	copy := CloneSnapshot(source)
	source.ConfigOverrides[0].Key = "password=private"
	if copy.ConfigOverrides[0].Key != "dns.listen" {
		t.Fatal("config override snapshot retained mutable caller memory")
	}
}

func TestConfigOverrideDescriptionNamesManagedReason(t *testing.T) {
	for _, test := range []struct{ key, reason string }{
		{"external-controller", "local controller"},
		{"secret", "private controller"},
		{"dns.listen", "loopback DNS"},
		{"dns.nameserver-policy", "DNS routing"},
		{"rules", "managed filter"},
	} {
		entry := ConfigOverrideSnapshot{Key: test.key, Change: "replaced"}
		if got := entry.Description(); !strings.Contains(got, test.reason) {
			t.Fatalf("%s reason = %q", test.key, got)
		}
	}
	if got := (ConfigOverrideSnapshot{Key: "password=private", Change: "added"}).Description(); got != "" {
		t.Fatalf("untrusted key received display text: %q", got)
	}
}
