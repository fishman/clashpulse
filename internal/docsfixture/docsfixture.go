// Package docsfixture provides the mock snapshot the documentation
// screenshots render. Every name, host, and address is reserved or invented.
package docsfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/monitor"
)

// Now is the fixed instant the snapshot describes (2026-11-18 09:30 UTC), so
// renders are stable.
const Now int64 = 1794994200

// ID derives an opaque identifier from a name the way the service does.
func ID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

type node struct {
	name                          string
	latency, median, jitter       int64
	samples, failures             int
	streamSamples, streamDone     int
	streamKBps                    int64
	outcome                       string
	mihomoMillis, finishedAgoSecs int64
}

var nodes = []node{
	{"Example Tokyo 01", 68, 71, 6, 5, 0, 5, 5, 2860, "success", 70, 20},
	{"Example Tokyo 02", 74, 76, 4, 5, 0, 5, 5, 3410, "success", 75, 20},
	{"Example Singapore 01", 112, 118, 31, 5, 1, 5, 3, 1240, "success", 109, 21},
	{"Example Hong Kong 03", 49, 52, 9, 5, 0, 5, 4, 2210, "success", 50, 21},
	{"Example Frankfurt 02", 231, 236, 14, 5, 0, 4, 4, 1980, "success", 228, 22},
	{"Example London 01", 248, 252, 18, 5, 0, 4, 4, 1720, "success", 251, 22},
	{"Example Los Angeles 03", 162, 171, 22, 5, 1, 4, 3, 1530, "success", 160, 23},
	{"Example Toronto 01", 0, 0, 0, 5, 5, 3, 0, 0, "timeout", 0, 24},
}

// Snapshot returns a populated, connected service snapshot that passes IPC
// validation.
func Snapshot() core.Snapshot {
	proxyGroup, globalGroup := ID("Proxy"), ID("GLOBAL")
	selected, previous := ID("Example Tokyo 02"), ID("Example Singapore 01")

	members := make([]string, len(nodes))
	proxies := make([]core.ProxySnapshot, 0, 2*len(nodes)+3)
	for i, n := range nodes {
		members[i] = ID(n.name)
		proxies = append(proxies, core.ProxySnapshot{
			ID: members[i], Label: n.name, GroupID: proxyGroup,
			LatencyMillis: n.latency, FinishedAt: Now - n.finishedAgoSecs, Outcome: n.outcome, MihomoMillis: n.mihomoMillis,
			MedianMillis: n.median, JitterMillis: n.jitter, Samples: n.samples, Failures: n.failures,
			StreamSamples: n.streamSamples, StreamCompleted: n.streamDone, StreamKBps: n.streamKBps,
		})
	}
	globalMembers := append([]string{proxyGroup, ID("DIRECT"), ID("REJECT")}, members...)
	for _, name := range []string{"Proxy", "DIRECT", "REJECT"} {
		proxies = append(proxies, core.ProxySnapshot{ID: ID(name), Label: name, GroupID: globalGroup})
	}
	for i, n := range nodes {
		proxies = append(proxies, core.ProxySnapshot{ID: members[i], Label: n.name, GroupID: globalGroup, MihomoMillis: n.mihomoMillis})
	}

	const gib = 1 << 30
	return core.Snapshot{
		Revision:       4182,
		ActiveSource:   "subscription",
		ServiceRunning: true,
		ConfigOverrides: []core.ConfigOverrideSnapshot{
			{Key: "mixed-port", Change: "replaced"},
			{Key: "external-controller", Change: "replaced"},
			{Key: "secret", Change: "replaced"},
			{Key: "allow-lan", Change: "replaced"},
			{Key: "dns.listen", Change: "added"},
			{Key: "rule-providers", Change: "added"},
			{Key: "rules", Change: "replaced"},
			{Key: "mode", Change: "replaced"},
		},
		Groups: []core.GroupSnapshot{
			{ID: proxyGroup, Label: "Proxy", Type: "Selector", Selected: selected, Proxies: members, AutomationEnabled: true},
			{ID: globalGroup, Label: "GLOBAL", Type: "Selector", Selected: proxyGroup, Proxies: globalMembers},
		},
		Proxies: proxies,
		Subscriptions: []core.SubscriptionSnapshot{
			{
				ID: "example-provider", Name: "Example Provider", SourceHost: "example.com",
				HashPrefix: "3f9a1c7e5b20", AppliedHashPrefix: "3f9a1c7e5b20", Route: "auto",
				RefreshIntervalSeconds: 43200, TimeoutSeconds: 30, Enabled: true, Active: true,
				LastCheck: Now - 1500, LastSuccess: Now - 1500, NextDue: Now + 41700,
				Usage: &core.UsageSnapshot{UploadedBytes: 3 * gib, DownloadedBytes: 41 * gib, TotalBytes: 200 * gib, ExpiresAt: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC).Unix()},
			},
			{
				ID: "backup-mirror", Name: "Backup Mirror", SourceHost: "mirror.example.net",
				HashPrefix: "a41d09e6c3b8", Route: "direct",
				RefreshIntervalSeconds: 86400, TimeoutSeconds: 30, Enabled: true,
				LastCheck: Now - 7200, LastSuccess: Now - 7200, NextDue: Now + 79200,
				Usage: &core.UsageSnapshot{UploadedBytes: gib / 2, DownloadedBytes: 6 * gib, TotalBytes: 50 * gib},
			},
			{
				ID: "travel-plan", Name: "Travel Plan", SourceHost: "travel.example.net",
				HashPrefix: "c07be2914d5f", Route: "mihomo_proxy",
				RefreshIntervalSeconds: 86400, TimeoutSeconds: 30,
				LastCheck: Now - 864000, LastSuccess: Now - 864000,
			},
		},
		Resources: []core.ResourceSnapshot{
			{ID: "geoip", Kind: "geoip.dat", Format: "dat", SourceHost: "geodata.example.com", HashPrefix: "5c2e8d1f0a93", Destination: "geoip.dat", Validated: true, Enabled: true, LastCheck: Now - 3600, LastSuccess: Now - 3600, NextDue: Now + 82800},
			{ID: "geosite", Kind: "geosite.dat", Format: "dat", SourceHost: "geodata.example.com", HashPrefix: "e81b4a6c72d0", Destination: "geosite.dat", Validated: true, Enabled: true, LastCheck: Now - 3600, LastSuccess: Now - 3600, NextDue: Now + 82800},
			{ID: "mmdb", Kind: "Country.mmdb", Format: "mmdb", SourceHost: "geodata.example.com", HashPrefix: "9d07f3b25e1a", Destination: "Country.mmdb", Validated: true, Enabled: true, LastCheck: Now - 3600, LastSuccess: Now - 3600, NextDue: Now + 82800},
			{ID: "cn", Kind: "rule-set", Format: "mrs", RuleType: "ipcidr", SourceHost: "rules.example.net", HashPrefix: "41aa6e0c9b37", Destination: "cn.mrs", Validated: true, Enabled: true, LastCheck: Now - 5400, LastSuccess: Now - 5400, NextDue: Now + 81000},
		},
		Filters: []core.FilterSnapshot{
			{ID: "cn-direct", ResourceID: "cn", Format: "mrs", Target: "DIRECT", SourceHost: "rules.example.net", HashPrefix: "41aa6e0c9b37", Destination: "cn.mrs", Enabled: true, Validated: true, LastSuccess: Now - 5400, NextDue: Now + 81000},
		},
		Binary: core.BinarySnapshot{Desired: "system", ObservedVersion: "v1.19.31", Capabilities: []string{"geoip.dat", "geosite.dat", "Country.mmdb", "pinned-listener"}},
		Monitor: core.MonitorSnapshot{
			Enabled: true, TestURL: "https://probe.example.com/generate_204", SwitchPolicy: monitor.SwitchSteadyStream,
			StreamURLConfigured: true, StreamIntervalSeconds: 300,
			URLTestIntervalSeconds: 300, URLTestToleranceMillis: 50,
			IntervalSeconds: 60, TimeoutMillis: 5000, Concurrency: 3,
			ThresholdMillis: 800, AlertThresholdMillis: 250, ConsecutiveBadSamples: 3,
			WindowSize: 5, MinCandidateSamples: 3, MaxFailures: 2,
			MinImprovementMillis: 100, CooldownSeconds: 300, JitterMillis: 10000,
		},
		DNS: core.DNSSnapshot{
			Listen:       "127.0.0.1:1053",
			ResolverSets: []core.ResolverSetSnapshot{{ID: "default", Endpoints: []string{"https://dns.example.net/dns-query", "192.0.2.53"}}},
			Routes:       []core.DNSRouteSnapshot{{Suffix: "corp.invalid", ResolverSet: "default"}},
		},
		SystemProxy: core.SystemProxySnapshot{Enabled: true, Active: true},
		MixedPort:   7899,
		ProxyMode:   core.ProxyModeRule,
		Tun:         core.TunSnapshot{Stack: core.TunStackMixed, DNSMode: core.TunDNSFakeIP, Privilege: core.TunPrivilegeReady},
		FinishedJobs: []core.JobSnapshot{
			{ID: "job-41", Kind: "refresh_subscription", State: "succeeded"},
			{ID: "job-42", Kind: "stream_probe", State: "succeeded"},
		},
		Switches: []core.SwitchSnapshot{{
			GroupID: proxyGroup, OldID: previous, NewID: selected, Reason: monitor.ReasonSteadierStream, At: Now - 840,
			Evidence: []core.ProbeSnapshot{
				{ProxyID: previous, FinishedAt: Now - 845, LatencyMillis: 118, Outcome: "success"},
				{ProxyID: selected, FinishedAt: Now - 845, LatencyMillis: 76, Outcome: "success"},
			},
			StreamEvidence: []core.StreamProbeSnapshot{
				{ProxyID: previous, FinishedAt: Now - 850, Outcome: "stall"},
				{ProxyID: selected, FinishedAt: Now - 850, Outcome: "success", KBps: 3410},
			},
		}},
		Diagnostics: []core.DiagnosticSnapshot{
			{At: Now - 2100, Severity: "info", Kind: "stream_probe", SourceID: ID("Example Toronto 01"), Message: "stream.outcome.timeout"},
			{At: Now - 1500, Severity: "info", Kind: "refresh_subscription", SourceID: "example-provider", Message: "recovered"},
			{At: Now - 850, Severity: "info", Kind: "stream_probe", SourceID: previous, Message: "stream.outcome.stall"},
		},
	}
}
