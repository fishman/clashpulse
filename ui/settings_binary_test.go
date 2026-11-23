package ui

import (
	"image/color"
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

func TestSettingsBinarySelectSendsOnlyUserChoicesAndShowsCompatibilityIssue(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	page := newSettingsPage(func(command ipc.Command) { sent = append(sent, command) }, app.NewWindow("ClashPulse"))
	if !containsObject(page.sectionViews["Mihomo binary"], page.binarySource) || findSettingsButton(page.sectionViews["Mihomo binary"], localize.T("Choose path...")) == nil {
		t.Fatal("binary choice or path action is missing")
	}
	page.update(core.BinarySnapshot{Desired: "system"}, core.MonitorSnapshot{}, core.SystemProxySnapshot{}, core.DNSSnapshot{})
	if len(sent) != 0 || page.binarySource.Selected != localize.T("System") {
		t.Fatalf("snapshot rendering: selected %q, sent %+v", page.binarySource.Selected, sent)
	}
	page.binarySource.SetSelected(localize.T("Bundled"))
	if len(sent) != 1 || sent[0].Kind != ipc.CommandUpdateConfiguration || sent[0].Config == nil || sent[0].Config.Binary == nil || *sent[0].Config.Binary != "bundled" {
		t.Fatalf("bundled choice sent wrong command: %+v", sent)
	}
	page.update(core.BinarySnapshot{Desired: "/opt/mihomo/bin/mihomo"}, core.MonitorSnapshot{}, core.SystemProxySnapshot{}, core.DNSSnapshot{})
	if len(sent) != 1 || page.binarySource.Selected != "/opt/mihomo/bin/mihomo" {
		t.Fatalf("custom path: selected %q, sent %+v", page.binarySource.Selected, sent)
	}
	page.binarySource.SetSelected(localize.T("System"))
	if len(sent) != 2 || *sent[1].Config.Binary != "system" {
		t.Fatalf("system choice sent wrong command: %+v", sent)
	}

	page.update(core.BinarySnapshot{Desired: "bundled", LastCompatibilityFailure: "bundled Mihomo is not installed"}, core.MonitorSnapshot{}, core.SystemProxySnapshot{}, core.DNSSnapshot{})
	if !strings.Contains(page.binaryValues[3].Text, "bundled Mihomo is not installed") {
		t.Fatalf("bundled compatibility issue not surfaced: %q", page.binaryValues[3].Text)
	}
	if page.binaryValues[3].Wrapping != fyne.TextWrapWord {
		t.Fatal("binary capability and compatibility fields clip instead of wrapping")
	}
}

func TestSettingsMonitorShowsStabilityAndSendsTestNotification(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	page := newSettingsPage(func(command ipc.Command) { sent = append(sent, command) }, app.NewWindow("ClashPulse"))
	button := findSettingsButton(page.sectionViews["Monitor"], localize.T("Send test"))
	if button == nil {
		t.Fatal("test notification button is missing from the monitor section")
	}
	button.OnTapped()
	if !reflect.DeepEqual(sent, []ipc.Command{{Kind: ipc.CommandTestNotification}}) {
		t.Fatalf("test notification sent %+v", sent)
	}
	page.update(core.BinarySnapshot{}, core.MonitorSnapshot{WindowSize: 10, MinCandidateSamples: 4, MaxFailures: 3}, core.SystemProxySnapshot{}, core.DNSSnapshot{})
	if got, want := page.stability.Text, "window 10 samples, 4 successes to switch, at most 3 failures"; got != want {
		t.Fatalf("stability summary = %q, want %q", got, want)
	}
}

func TestSettingsNavigationCollapsesOnlyWhenSectionsCannotFit(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("ClashPulse")
	page := newSettingsPage(func(ipc.Command) {}, w)
	w.SetContent(page.view)
	w.Resize(fyne.NewSize(1200, 640))
	if !page.sectionList.Visible() || page.sectionSelect.Visible() {
		t.Fatal("wide settings page did not show its section list")
	}
	for _, e := range page.sectionEntries {
		if e.selected != (e.id == "Mihomo binary") {
			t.Fatalf("initial selection marks %q = %t", e.id, e.selected)
		}
	}
	// Monitor is the widest pane and DNS the narrowest: at this width only
	// DNS fits beside the section list.
	test.Tap(sectionEntry(t, page, "Monitor"))
	if page.sectionSelect.Selected != "Monitor" || !page.sectionViews["Monitor"].Visible() || !sectionEntry(t, page, "Monitor").selected || sectionEntry(t, page, "Mihomo binary").selected {
		t.Fatal("section list did not select the Monitor section")
	}
	w.Resize(fyne.NewSize(430, 640))
	if page.sectionList.Visible() || !page.sectionSelect.Visible() || page.sectionSelect.Selected != "Monitor" || !page.sectionViews["Monitor"].Visible() {
		t.Fatal("collapsed navigation lost the Monitor section")
	}
	if page.sectionViews["Monitor"].MinSize().Width > page.sectionContent.Size().Width {
		t.Fatal("monitor controls overflow the compact settings viewport")
	}
	page.sectionSelect.SetSelected("DNS")
	if !page.sectionList.Visible() || page.sectionSelect.Visible() {
		t.Fatal("settings switched to dropdown despite enough room for the section list")
	}
	page.sectionSelect.SetSelected("Monitor")
	if page.sectionList.Visible() || page.sectionContent.Size().Width < page.sectionViews["Monitor"].MinSize().Width {
		t.Fatal("Monitor controls are clipped instead of collapsing the section list")
	}
	page.sectionSelect.SetSelected("DNS")
	if !sectionEntry(t, page, "DNS").selected || !page.dns.view.Visible() || page.sectionViews["Monitor"].Visible() {
		t.Fatal("dropdown did not change sections")
	}
	w.Resize(fyne.NewSize(1200, 640))
	if !page.sectionList.Visible() || page.sectionSelect.Visible() || !sectionEntry(t, page, "DNS").selected {
		t.Fatal("expanding settings did not restore the selected section")
	}
}

// TestHiddenSettingsSectionFollowsThemeChange flips the variant while another
// section is shown; the hidden DNS section must repaint too.
func TestHiddenSettingsSectionFollowsThemeChange(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	a.Settings().SetTheme(variantTheme{newTheme(), theme.VariantLight})
	w := a.NewWindow("ClashPulse")
	page := newSettingsPage(func(ipc.Command) {}, w)
	w.SetContent(page.view)
	w.Resize(fyne.NewSize(1200, 640))
	page.selectSection("DNS")
	page.selectSection("Monitor")
	dark := variantTheme{newTheme(), theme.VariantDark}
	a.Settings().SetTheme(dark)
	want := dark.Color(theme.ColorNameForeground, theme.VariantDark)
	var lightText color.Color = newTheme().Color(theme.ColorNameForeground, theme.VariantLight)
	repainted := 0
	for _, object := range paintedObjects(page.dns.view) {
		text, ok := object.(*canvas.Text)
		if !ok || text.Text == "" {
			continue
		}
		if text.Color == lightText {
			t.Fatalf("hidden DNS text %q kept the light foreground", text.Text)
		}
		if text.Color == want {
			repainted++
		}
	}
	if repainted == 0 {
		t.Fatal("no DNS text repainted with the dark foreground")
	}
}

func sectionEntry(t *testing.T, page *settingsPage, id string) *sidebarEntry {
	t.Helper()
	for _, e := range page.sectionEntries {
		if e.id == id {
			return e
		}
	}
	t.Fatalf("no settings section %q", id)
	return nil
}

func findSettingsButton(object fyne.CanvasObject, text string) *widget.Button {
	if button, ok := object.(*widget.Button); ok && button.Text == text {
		return button
	}
	switch object := object.(type) {
	case *fyne.Container:
		for _, child := range object.Objects {
			if button := findSettingsButton(child, text); button != nil {
				return button
			}
		}
	case *container.Scroll:
		return findSettingsButton(object.Content, text)
	}
	return nil
}

// A rejected binary choice produces no new snapshot when the rejection repeats,
// so the Use select must keep showing the desired binary from the last
// snapshot rather than the user's pending pick.
func TestSettingsBinarySelectShowsSnapshotChoiceWhilePending(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	page := newSettingsPage(func(command ipc.Command) { sent = append(sent, command) }, app.NewWindow("ClashPulse"))
	page.update(core.BinarySnapshot{Desired: "system"}, core.MonitorSnapshot{}, core.SystemProxySnapshot{}, core.DNSSnapshot{})
	for attempt := 1; attempt <= 2; attempt++ {
		page.binarySource.SetSelected(localize.T("Bundled"))
		if len(sent) != attempt || *sent[attempt-1].Config.Binary != "bundled" {
			t.Fatalf("attempt %d: sent %+v", attempt, sent)
		}
		if page.binarySource.Selected != localize.T("System") {
			t.Fatalf("attempt %d: select shows pending %q, want snapshot choice", attempt, page.binarySource.Selected)
		}
	}
}
