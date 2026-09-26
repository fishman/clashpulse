package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/fishman/clashpulse/core"
)

func TestSourceHostLabelRejectsURLsAndCredentials(t *testing.T) {
	for _, host := range []string{"", "https://user:secret@example.org/path", "user:secret@example.org", "example.org/path", "example.org?token=secret"} {
		if got := sourceHostLabel(host); got != "Unknown source" {
			t.Errorf("sourceHostLabel(%q) = %q, want safe fallback", host, got)
		}
	}
	if got := sourceHostLabel("provider.example:8443"); got != "provider.example:8443" {
		t.Fatalf("valid host label = %q", got)
	}
}

func TestStatusLabelsDoNotExposeFailureDetails(t *testing.T) {
	secret := "authorization bearer-secret"
	subscription := core.SubscriptionSnapshot{ID: "feed-1", Enabled: true, Active: true, LastFailure: secret}
	resource := core.ResourceSnapshot{ID: "resource-1", Enabled: true, Validated: true, LastResult: "failed: " + secret}
	for _, got := range []string{subscriptionStatus(subscription), resourceStatus(resource)} {
		if strings.Contains(got, secret) {
			t.Fatalf("status leaked failure details: %q", got)
		}
	}
	if got := subscriptionStatus(subscription); got != "Active (Needs attention)" {
		t.Errorf("active subscription failure status = %q", got)
	}
	subscription.Active = false
	if got := subscriptionStatus(subscription); got != "Needs attention" {
		t.Errorf("subscription failure status = %q", got)
	}
	if got := resourceStatus(resource); got != "Needs attention" {
		t.Errorf("resource failure status = %q", got)
	}
	resource.LastResult = "updated successfully"
	if got := resourceStatus(resource); got != "Needs attention" {
		t.Errorf("resource failure signal status = %q", got)
	}
	resource.LastResult = ""
	if got := resourceStatus(resource); got != "Ready" {
		t.Errorf("validated resource status = %q", got)
	}
}

func TestProxyRowShowsOnlySelectionAndLatency(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	page := newProxyPage(nil)
	page.update(core.Snapshot{
		Groups:  []core.GroupSnapshot{{ID: "g1", Type: "Selector", Selected: "p1", Proxies: []string{"p1"}}},
		Proxies: []core.ProxySnapshot{{GroupID: "g1", ID: "p1", Label: "node-a", LatencyMillis: 42, Outcome: "private detail"}},
	})
	row := page.list.CreateItem()
	page.list.UpdateItem(0, row)
	labels := rowLabels(row)
	if strings.Join(labels, ",") != "node-a,Active,42 ms" || strings.Contains(strings.Join(labels, " "), "private detail") {
		t.Fatalf("proxy row exposed outcome or lost separate cells: %v", labels)
	}
}

func TestMonitorThresholdAndCompatibilityLabelsAreSafe(t *testing.T) {
	if got := monitorThresholdLabel(core.MonitorSnapshot{AlertThresholdMillis: 250}); got != "250 ms" {
		t.Fatalf("threshold label = %q", got)
	}
	if got := monitorThresholdLabel(core.MonitorSnapshot{}); got != "Not configured" {
		t.Fatalf("empty threshold label = %q", got)
	}
	failure := "secret path /home/user/private/config.toml"
	if got := compatibilityLabel(failure); got != "Compatibility issue reported" || strings.Contains(got, failure) {
		t.Fatalf("compatibility label exposed detail: %q", got)
	}
	if got := compatibilityLabel("resource geo requires geoip.dat"); got != "resource geo requires geoip.dat" {
		t.Fatalf("safe named capability was hidden: %q", got)
	}
}

func TestLastSwitchSummaryShowsDecisionEvidence(t *testing.T) {
	view := core.Snapshot{Switches: []core.SwitchSnapshot{{
		OldID: "aaaaaaaa11111111", NewID: "bbbbbbbb22222222", Reason: "materially better candidate",
		Evidence: []core.ProbeSnapshot{{ProxyID: "aaaaaaaa11111111", Outcome: "timeout"}, {ProxyID: "bbbbbbbb22222222", Outcome: "success", LatencyMillis: 180}},
	}}}
	got := lastSwitchSummary(view)
	for _, piece := range []string{"aaaaaaaa -> bbbbbbbb", "materially better candidate", "timeout 0 ms", "success 180 ms"} {
		if !strings.Contains(got, piece) {
			t.Fatalf("switch evidence missing %q: %q", piece, got)
		}
	}
}

func TestLocalActiveSourceGUIStatus(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(t.Context(), "", app.NewWindow("ClashPulse"))
	state := core.Snapshot{ActiveSource: "local", Subscriptions: []core.SubscriptionSnapshot{{ID: "prior", Name: "private-path.yaml", Active: true}}}
	view.postSnapshot(state)
	fyne.DoAndWait(func() {})
	status := view.connection.Text
	if !strings.Contains(status, "local profile") || strings.Contains(status, "private-path.yaml") {
		t.Fatalf("unsafe GUI status: %q", status)
	}
	if title := profileTrayTitle(state); !strings.Contains(title, "local profile") || strings.Contains(title, "private-path.yaml") {
		t.Fatalf("unsafe tray title: %q", title)
	}
}

func TestDetailViewsExposeStateWithoutFailureSecrets(t *testing.T) {
	const sensitive = "https://user:secret@provider.example/subscription"
	sub := core.SubscriptionSnapshot{SourceHost: "provider.example", Enabled: true, Active: true, PendingActivation: true, LastCheck: 1700000000, LastSuccess: 1700000000, NextDue: 1700003600, HashPrefix: "abcdef", AppliedHashPrefix: "123456", LastFailure: sensitive, Usage: &core.UsageSnapshot{UploadedBytes: 12, DownloadedBytes: 8, TotalBytes: 100}}
	resource := core.ResourceSnapshot{ID: "cn", Kind: "rule-set", Format: "yaml", SourceHost: "provider.example", Enabled: true, Validated: true, HashPrefix: "fedcba", Destination: "cn.yaml", LastSuccess: 1700000000, NextDue: 1700003600, LastResult: sensitive}
	filter := core.FilterSnapshot{ResourceID: "cn", Target: "Proxy", SourceHost: "provider.example", Enabled: true, Validated: true, HashPrefix: "fedcba", Destination: "cn.yaml", LastFailure: sensitive}
	for _, detail := range []string{subscriptionDetails(sub), resourceDetails(resource), filterDetails(filter)} {
		if strings.Contains(detail, sensitive) || !strings.Contains(detail, "provider.example") {
			t.Fatalf("unsafe or missing source detail: %q", detail)
		}
	}
	if got := subscriptionDetails(sub); !strings.Contains(got, "Active (Needs attention)") || !strings.Contains(got, "abcdef") || !strings.Contains(got, "up 12") {
		t.Fatalf("subscription metadata missing: %q", got)
	}
}
