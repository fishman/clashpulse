package ui

import (
	"context"
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

func TestPillStates(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		snapshot                 core.Snapshot
		mihomo, systemProxy, tun pillState
	}{
		{"stopped", core.Snapshot{}, pillOff, pillOff, pillOff},
		{"running", core.Snapshot{ServiceRunning: true, SystemProxy: core.SystemProxySnapshot{Enabled: true, Active: true}, Tun: core.TunSnapshot{Active: true}}, pillOn, pillOn, pillOn},
		{"compatibility failure", core.Snapshot{ServiceRunning: true, Binary: core.BinarySnapshot{LastCompatibilityFailure: "version_mismatch"}}, pillFailed, pillOff, pillOff},
		{"system proxy requested not applied", core.Snapshot{ServiceRunning: true, SystemProxy: core.SystemProxySnapshot{Enabled: true}}, pillOn, pillFailed, pillOff},
		{"system proxy requested while stopped", core.Snapshot{SystemProxy: core.SystemProxySnapshot{Enabled: true}}, pillOff, pillOff, pillOff},
		{"tun failure", core.Snapshot{ServiceRunning: true, Tun: core.TunSnapshot{Enabled: true, LastFailure: "permission denied"}}, pillOn, pillOff, pillFailed},
	} {
		mihomo, systemProxy, tun := pillStates(tc.snapshot)
		if mihomo != tc.mihomo || systemProxy != tc.systemProxy || tun != tc.tun {
			t.Errorf("%s: pills = %v %v %v, want %v %v %v", tc.name, mihomo, systemProxy, tun, tc.mihomo, tc.systemProxy, tc.tun)
		}
	}
}

func TestShowViewSelectsSidebarAndTitle(t *testing.T) {
	t.Cleanup(func() { localize.SetLanguage("en") })
	for _, locale := range []string{"en", "zh-CN"} {
		localize.SetLanguage(locale)
		a := test.NewApp()
		view := newDesktopUI(context.Background(), "", a.NewWindow("ClashPulse"))
		view.showView("Settings")
		if got := view.sidebar.selected(); got != "Settings" {
			t.Errorf("%s sidebar selection = %q", locale, got)
		}
		if got, want := view.topBar.title.Text, localize.T("Settings"); got != want {
			t.Errorf("%s title = %q, want %q", locale, got, want)
		}
		a.Quit()
	}
}

func TestTopBarDisconnectedTurnsPillsOff(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	view := newDesktopUI(context.Background(), "", a.NewWindow("ClashPulse"))
	view.topBar.update(core.Snapshot{ServiceRunning: true, Tun: core.TunSnapshot{Active: true}})
	if view.topBar.mihomo.state != pillOn || view.topBar.tun.state != pillOn {
		t.Fatal("running pills not on")
	}
	view.topBar.disconnect()
	if view.topBar.mihomo.state != pillOff || view.topBar.tun.state != pillOff || view.connection.Text != "Disconnected" {
		t.Fatalf("disconnected top bar = %v %v %q", view.topBar.mihomo.state, view.topBar.tun.state, view.connection.Text)
	}
}

func TestTopBarWidthIgnoresStatusLength(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	view := newDesktopUI(context.Background(), "", a.NewWindow("ClashPulse"))
	view.topBar.setConnection("ok")
	short := view.topBar.object.MinSize().Width
	view.topBar.setConnection(strings.Repeat("a very long service status ", 8))
	if got := view.topBar.object.MinSize().Width; got != short {
		t.Fatalf("top bar min width grew from %v to %v", short, got)
	}
}

func TestHiddenPageFollowsThemeChange(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	a.Settings().SetTheme(variantTheme{newTheme(), theme.VariantLight})
	window := a.NewWindow("ClashPulse")
	view := newDesktopUI(context.Background(), "", window)
	window.Resize(fyne.NewSize(1000, 700))
	view.postSnapshot(core.Snapshot{
		Groups:  []core.GroupSnapshot{{ID: "g", Label: "Proxy", Type: "Selector", Selected: "a", Proxies: []string{"a"}}},
		Proxies: []core.ProxySnapshot{{ID: "a", GroupID: "g", Label: "Example Tokyo 01"}},
	})
	fyne.DoAndWait(func() {})
	view.showView("Proxies")
	view.showView("Overview")
	dark := variantTheme{newTheme(), theme.VariantDark}
	a.Settings().SetTheme(dark)
	want := dark.Color(theme.ColorNameForeground, theme.VariantDark)
	rowColor := func() color.Color {
		for _, object := range paintedObjects(view.proxyPage.list) {
			if text, ok := object.(*canvas.Text); ok && text.Text == "Example Tokyo 01" {
				return text.Color
			}
		}
		t.Fatal("proxy row label not rendered")
		return nil
	}
	// The hidden page must take the theme change itself: list rows skip
	// unchanged setters, so showing the page later does not repaint them.
	if got := rowColor(); got != want {
		t.Fatalf("hidden row label color = %v, want dark foreground %v", got, want)
	}
	view.showView("Proxies")
	if got := rowColor(); got != want {
		t.Fatalf("shown row label color = %v, want dark foreground %v", got, want)
	}
}

// paintedObjects collects the objects under o through the renderers that
// already exist, without laying anything out again, so colors are what the
// last theme pass left behind.
func paintedObjects(o fyne.CanvasObject) []fyne.CanvasObject {
	objects := []fyne.CanvasObject{o}
	switch o := o.(type) {
	case fyne.Widget:
		for _, child := range test.WidgetRenderer(o).Objects() {
			objects = append(objects, paintedObjects(child)...)
		}
	case *fyne.Container:
		for _, child := range o.Objects {
			objects = append(objects, paintedObjects(child)...)
		}
	}
	return objects
}

func TestSidebarEntryKeyboard(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	var tapped []string
	entry := newSidebarEntry("DNS", "DNS", nil, func(id string) { tapped = append(tapped, id) })
	window := a.NewWindow("ClashPulse")
	window.SetContent(entry)
	var focusable fyne.Focusable = entry
	window.Canvas().Focus(focusable)
	if entry.tint.StrokeWidth == 0 || entry.tint.StrokeColor != theme.Color(theme.ColorNameFocus) {
		t.Fatalf("focused entry ring = width %v color %v", entry.tint.StrokeWidth, entry.tint.StrokeColor)
	}
	for _, key := range []fyne.KeyName{fyne.KeySpace, fyne.KeyReturn, fyne.KeyEnter, fyne.KeyTab} {
		entry.TypedKey(&fyne.KeyEvent{Name: key})
	}
	if len(tapped) != 3 || tapped[0] != "DNS" {
		t.Fatalf("keys selected %v, want Space, Return, and Enter only", tapped)
	}
	window.Canvas().Unfocus()
	if entry.tint.StrokeWidth != 0 {
		t.Fatalf("unfocused entry keeps its ring: %v", entry.tint.StrokeWidth)
	}
}

func TestPowerButtonTogglesService(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	view := newDesktopUI(context.Background(), "", a.NewWindow("ClashPulse"))
	if !view.topBar.power.Disabled() {
		t.Fatal("power button enabled before the service is reachable")
	}
	view.connected = true
	for _, tc := range []struct {
		running bool
		want    ipc.CommandKind
	}{{false, ipc.CommandStart}, {true, ipc.CommandStop}} {
		view.topBar.update(core.Snapshot{ServiceRunning: tc.running})
		test.Tap(view.topBar.power)
		if got := <-view.actions; got.Kind != tc.want {
			t.Fatalf("running=%t: power sent %q, want %q", tc.running, got.Kind, tc.want)
		}
	}
	view.topBar.disconnect()
	if !view.topBar.power.Disabled() {
		t.Fatal("power button enabled after disconnect")
	}
}
