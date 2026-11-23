package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/theme"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/monitor"
)

func TestBuildOverviewEmptySnapshot(t *testing.T) {
	m := buildOverview(core.Snapshot{})
	if m.Node.Visible || m.Stream.Visible || m.Usage.Visible {
		t.Fatalf("empty snapshot shows cards: %+v", m)
	}
	if !m.Switch.Empty || m.Switch.Title != "No switch yet" {
		t.Fatalf("empty switch = %+v", m.Switch)
	}
	if m.Health.Issue != "No issues" || m.Health.State != pillOn {
		t.Fatalf("empty health = %+v", m.Health)
	}
	if m.Profile != "None" {
		t.Fatalf("empty profile = %q", m.Profile)
	}
}

func TestBuildOverviewPrefersAutomatedGroup(t *testing.T) {
	s := core.Snapshot{
		Groups: []core.GroupSnapshot{
			{ID: "manual", Label: "Manual", Selected: "a"},
			{ID: "auto", Label: "Auto", Selected: "b", AutomationEnabled: true},
		},
		Proxies: []core.ProxySnapshot{
			{ID: "a", GroupID: "manual", Label: "Example Tokyo 01"},
			{ID: "b", GroupID: "auto", Label: "Example Tokyo 02", LatencyMillis: 120, Outcome: "success"},
		},
	}
	m := buildOverview(s)
	if !m.Node.Visible || m.Node.Node != "Example Tokyo 02" || m.Node.Group != "Auto" || m.Node.Latency != "120" || m.Node.Unit != "ms" {
		t.Fatalf("node card = %+v", m.Node)
	}
	s.Groups[1].AutomationEnabled = false
	if m := buildOverview(s); m.Node.Node != "Example Tokyo 01" || m.Node.Latency != "-" {
		t.Fatalf("fallback node card = %+v", m.Node)
	}
	s.Proxies[0].MihomoMillis = 75
	if m := buildOverview(s); m.Node.Latency != "75" || m.Node.Unit != "ms (mihomo)" {
		t.Fatalf("mihomo latency = %+v", m.Node)
	}
}

func TestBuildOverviewStreamVisibility(t *testing.T) {
	s := core.Snapshot{
		Monitor: core.MonitorSnapshot{SwitchPolicy: monitor.SwitchSteadyStream, IntervalSeconds: 60, TimeoutMillis: 5000},
		Groups:  []core.GroupSnapshot{{ID: "g", Selected: "a", AutomationEnabled: true}},
		Proxies: []core.ProxySnapshot{{ID: "a", GroupID: "g", StreamSamples: 4, StreamCompleted: 3, StreamKBps: 1800}},
	}
	m := buildOverview(s)
	if !m.Stream.Visible || m.Stream.KBps != "1800" || m.Stream.Fraction != 0.75 || m.Stream.Color != theme.ColorNameWarning {
		t.Fatalf("stream card = %+v", m.Stream)
	}
	s.Proxies[0].StreamSamples = 0
	if m := buildOverview(s); m.Stream.Visible || m.Stream.Summary != "URL test every 60 s, timeout 5000 ms" {
		t.Fatalf("no-sample stream card = %+v", m.Stream)
	}
	s.Proxies[0].StreamSamples = 4
	s.Monitor.SwitchPolicy = monitor.SwitchFailover
	if m := buildOverview(s); m.Stream.Visible {
		t.Fatalf("failover stream card visible: %+v", m.Stream)
	}
}

func TestBuildOverviewUsage(t *testing.T) {
	const gib = 1 << 30
	s := core.Snapshot{ActiveSource: "subscription", Subscriptions: []core.SubscriptionSnapshot{{ID: "feed", Name: "Example Feed", Active: true, Usage: &core.UsageSnapshot{UploadedBytes: gib, DownloadedBytes: 49 * gib, TotalBytes: 200 * gib}}}}
	m := buildOverview(s)
	if !m.Usage.Visible || m.Usage.Used != "50.0" || m.Usage.Fraction != 0.25 || m.Profile != "Example Feed" {
		t.Fatalf("usage card = %+v profile %q", m.Usage, m.Profile)
	}
	s.Subscriptions[0].Usage.TotalBytes = 0
	if m := buildOverview(s); m.Usage.Visible {
		t.Fatal("usage with zero total visible")
	}
	s.Subscriptions[0].Usage = nil
	if m := buildOverview(s); m.Usage.Visible {
		t.Fatal("nil usage visible")
	}
}

func TestBuildOverviewSwitchUsesLabelsOrPrefix(t *testing.T) {
	s := core.Snapshot{
		Switches: []core.SwitchSnapshot{{OldID: "aaaaaaaa11111111", NewID: "bbbbbbbb22222222", Reason: "materially better candidate", At: 1767225600,
			Evidence:       []core.ProbeSnapshot{{ProxyID: "aaaaaaaa11111111", Outcome: "timeout"}, {ProxyID: "bbbbbbbb22222222", Outcome: "success", LatencyMillis: 180}},
			StreamEvidence: []core.StreamProbeSnapshot{{ProxyID: "aaaaaaaa11111111", Outcome: "reset"}, {ProxyID: "bbbbbbbb22222222", Outcome: "success", KBps: 1800}},
		}},
		Proxies: []core.ProxySnapshot{{ID: "bbbbbbbb22222222", Label: "Example Tokyo 02"}},
	}
	m := buildOverview(s)
	if m.Switch.Empty || m.Switch.Title != "aaaaaaaa -> Example Tokyo 02" || m.Switch.Time != "2026-01-01 00:00 UTC" || m.Switch.Reason != "materially better candidate" {
		t.Fatalf("switch card = %+v", m.Switch)
	}
	for _, piece := range []string{"aaaaaaaa timeout 0 ms", "Example Tokyo 02 success 180 ms", "aaaaaaaa download reset", "Example Tokyo 02 download completed \u00b7 1800 KB/s"} {
		if !strings.Contains(m.Switch.Evidence, piece) {
			t.Fatalf("switch evidence missing %q: %q", piece, m.Switch.Evidence)
		}
	}
	if strings.Contains(m.Switch.Evidence, "download reset \u00b7") {
		t.Fatalf("interrupted pull shows a throughput: %q", m.Switch.Evidence)
	}
}

func TestBuildOverviewHealth(t *testing.T) {
	m := buildOverview(core.Snapshot{Errors: []core.ErrorSnapshot{{File: "config.toml", Key: "monitor.interval", Message: "invalid configuration; previous settings remain active"}}})
	if m.Health.State != pillFailed {
		t.Fatalf("health state = %v", m.Health.State)
	}
	for _, detail := range []string{"config.toml", "monitor.interval", "previous settings remain active"} {
		if !strings.Contains(m.Health.Issue, detail) {
			t.Fatalf("health issue lacks %q: %q", detail, m.Health.Issue)
		}
	}
	m = buildOverview(core.Snapshot{Binary: core.BinarySnapshot{LastCompatibilityFailure: "bundled Mihomo is not installed"}})
	if m.Health.State != pillFailed || m.Health.Issue != "bundled Mihomo is not installed" {
		t.Fatalf("compatibility health = %+v", m.Health)
	}
}

func TestBuildOverviewShortLabels(t *testing.T) {
	s := core.Snapshot{
		Monitor:       core.MonitorSnapshot{SwitchPolicy: monitor.SwitchSteadyStream},
		Groups:        []core.GroupSnapshot{{ID: "g", Label: "Proxy", Type: "Selector", Selected: "a", AutomationEnabled: true}},
		Proxies:       []core.ProxySnapshot{{ID: "a", GroupID: "g", StreamSamples: 1, StreamCompleted: 1}},
		Subscriptions: []core.SubscriptionSnapshot{{ID: "one"}},
		Filters:       []core.FilterSnapshot{{ID: "ads"}},
	}
	m := buildOverview(s)
	if m.Policy != "Steady stream" || m.Stream.Title != "Steady stream" || m.Node.Group != "Proxy" {
		t.Fatalf("labels = %q %q %q", m.Policy, m.Stream.Title, m.Node.Group)
	}
	if m.Health.Counts != "Subscriptions 1 \u00b7 Data resources 0 \u00b7 Filter lists 1" {
		t.Fatalf("counts = %q", m.Health.Counts)
	}
	s.Proxies[0].StreamSamples = 0
	if m := buildOverview(s); m.Stream.Title != "URL test" {
		t.Fatalf("summary title = %q", m.Stream.Title)
	}
	localize.SetLanguage("zh-CN")
	t.Cleanup(func() { localize.SetLanguage("en") })
	if m := buildOverview(s); m.Policy != "\u7a33\u5b9a\u6d41" || m.Health.Counts != "\u8ba2\u9605 1 \u00b7 \u6570\u636e\u8d44\u6e90 0 \u00b7 \u8fc7\u6ee4\u5217\u8868 1" {
		t.Fatalf("zh labels = %q %q", m.Policy, m.Health.Counts)
	}
}
