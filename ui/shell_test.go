package ui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/fishman/clashpulse/core"
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
	localize.SetLanguage("en")
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
