package ui

import (
	"context"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/core"
)

func TestOverviewDisplaysConfigurationErrorDetails(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(context.Background(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{Errors: []core.ErrorSnapshot{{File: "config.toml", Key: "monitor.interval", Message: "invalid configuration; previous settings remain active"}}})
	fyne.DoAndWait(func() {})
	got := view.errorSummary.Text
	for _, detail := range []string{"config.toml", "monitor.interval", "previous settings remain active"} {
		if !strings.Contains(got, detail) {
			t.Fatalf("service issue detail %q absent from %q", detail, got)
		}
	}
}

func TestActivityDialogShowsSanitizedSnapshot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	view := newDesktopUI(context.Background(), "", window)
	view.postSnapshot(core.Snapshot{Diagnostics: []core.DiagnosticSnapshot{{At: 100, Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"}}})
	fyne.DoAndWait(func() {})
	view.openActivity()
	if window.Canvas().Overlays().Top() == nil {
		t.Fatal("activity did not open an Overview dialog")
	}
	view.postSnapshot(core.Snapshot{Diagnostics: []core.DiagnosticSnapshot{{At: 101, Severity: "info", Kind: "subscription", SourceID: "feed", Message: "recovered"}}})
	fyne.DoAndWait(func() {})
	if view.activity.list.Length() != 1 {
		t.Fatal("open activity did not follow new snapshot")
	}
	item := view.activity.list.CreateItem().(*widget.Label)
	view.activity.list.UpdateItem(0, item)
	if !strings.Contains(item.Text, "recovered") || !strings.Contains(item.Text, "feed") || strings.Contains(item.Text, "HTTP 406") {
		t.Fatalf("activity row did not update: %q", item.Text)
	}
}

func TestGUIRowsAreAlignedWithoutPipes(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(context.Background(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{
		Groups:        []core.GroupSnapshot{{ID: "g", Label: "Main", Type: "Selector", Selected: "node-a", Proxies: []string{"node-a"}}},
		Proxies:       []core.ProxySnapshot{{ID: "node-a", Label: "node-a", GroupID: "g", LatencyMillis: 42}},
		Subscriptions: []core.SubscriptionSnapshot{{ID: "alpha", Name: "Alpha", SourceHost: "feed.example", Enabled: true, Active: true}},
		Resources:     []core.ResourceSnapshot{{ID: "geo", SourceHost: "mirror.example", Enabled: true, Validated: true}},
		Filters:       []core.FilterSnapshot{{ID: "ads", Format: "yaml", Enabled: true}},
	})
	fyne.DoAndWait(func() {})
	for _, testCase := range []struct {
		name string
		list *widget.List
		want []string
	}{
		{"proxy", view.proxyPage.list, []string{"node-a", "Active", "42 ms"}},
		{"subscription", view.subPage.list, []string{"Alpha", "feed.example", "Active"}},
		{"resource", view.resourcePage.list, []string{"geo", "mirror.example", "Ready"}},
		{"filter", view.filterPage.list, []string{"ads", "yaml", "Enabled"}},
	} {
		row := testCase.list.CreateItem()
		testCase.list.UpdateItem(0, row)
		labels := rowLabels(row)
		for _, expected := range testCase.want {
			found := false
			for _, text := range labels {
				found = found || text == expected
			}
			if !found {
				t.Errorf("%s row lacks separate %q cell: %v", testCase.name, expected, labels)
			}
		}
		if strings.Contains(strings.Join(labels, " "), " | ") || strings.Contains(strings.Join(labels, " "), "token=") {
			t.Errorf("%s row exposes delimiter or secret: %v", testCase.name, labels)
		}
	}
}

func TestSettingsBinaryFieldsAreAligned(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(context.Background(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{Binary: core.BinarySnapshot{Desired: "system", ObservedVersion: "v1", Capabilities: []string{"geoip.dat"}, LastCompatibilityFailure: "bundled Mihomo is not installed"}})
	fyne.DoAndWait(func() {})
	labels := rowLabels(view.settingsPage.sectionViews["Mihomo binary"])
	for _, want := range []string{"Desired", "system", "Observed", "v1", "Capabilities", "geoip.dat", "Compatibility", "bundled Mihomo is not installed"} {
		found := false
		for _, label := range labels {
			found = found || label == want
		}
		if !found {
			t.Errorf("binary identity lacks separate %q cell: %v", want, labels)
		}
	}
	if strings.Contains(strings.Join(labels, " "), " | ") {
		t.Errorf("binary identity still has pipe layout: %v", labels)
	}
}

func TestOverviewCountsUseSeparateLabels(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(context.Background(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{Groups: []core.GroupSnapshot{{ID: "main"}}, Jobs: []core.JobSnapshot{{ID: "job-1", Kind: "refresh", State: "running"}}})
	fyne.DoAndWait(func() {})
	labels := rowLabels(view.views["Overview"])
	for _, want := range []string{"Proxy groups", "Jobs", "1"} {
		found := false
		for _, label := range labels {
			found = found || label == want
		}
		if !found {
			t.Errorf("Overview lacks separately labelled %q: %v", want, labels)
		}
	}
	if strings.Contains(strings.Join(labels, " "), " | ") {
		t.Errorf("Overview still has pipe layout: %v", labels)
	}
}

func TestOverviewBinaryFieldsAreAligned(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(context.Background(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{Binary: core.BinarySnapshot{Desired: "system", ObservedVersion: "v1", Capabilities: []string{"geoip.dat"}}})
	fyne.DoAndWait(func() {})
	labels := rowLabels(view.views["Overview"])
	for _, want := range []string{"Desired", "system", "Observed", "v1", "Capabilities", "geoip.dat", "Compatibility"} {
		found := false
		for _, label := range labels {
			found = found || label == want
		}
		if !found {
			t.Errorf("Overview binary identity lacks %q cell: %v", want, labels)
		}
	}
}

func rowLabels(object fyne.CanvasObject) []string {
	switch object := object.(type) {
	case *widget.Label:
		return []string{object.Text}
	case *widget.Form:
		var labels []string
		for _, field := range object.Items {
			labels = append(labels, field.Text)
			labels = append(labels, rowLabels(field.Widget)...)
		}
		return labels
	case *fyne.Container:
		var labels []string
		for _, child := range object.Objects {
			labels = append(labels, rowLabels(child)...)
		}
		return labels
	default:
		return nil
	}
}
