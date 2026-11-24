package tui

import (
	"reflect"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/monitor"
)

// Overview shows the proxy mode and switch policy and cycles each with a
// one-field patch, as Settings does, so the stored stream URL is kept.
func TestOverviewCyclesModeAndPolicy(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{ProxyMode: core.ProxyModeRule, Monitor: core.MonitorSnapshot{SwitchPolicy: monitor.SwitchFailover}}})
	if model.Tab != TabOverview {
		t.Fatalf("tab = %s", model.Tab)
	}
	rows := model.Rows()
	for id, want := range map[string]string{
		"overview:proxy-mode":    localize.Code("proxy.mode", core.ProxyModeRule),
		"overview:switch-policy": localize.Code("monitor.policy", monitor.SwitchFailover),
	} {
		if index := rowIndex(rows, id); index < 0 || rows[index].Detail != want {
			t.Fatalf("row %s missing or detail != %q in %+v", id, want, rows)
		}
	}
	mode, policy := core.ProxyModeGlobal, monitor.SwitchSteadyStream
	for action, want := range map[string]*ipc.Command{
		"cycle_proxy_mode":    configCommand(&ipc.ConfigPatch{ProxyMode: &mode}),
		"cycle_switch_policy": configCommand(&ipc.ConfigPatch{MonitorSwitchPolicy: &policy}),
	} {
		key := model.keymap.KeyFor(TabOverview, action)
		if key == "" {
			t.Fatalf("no Overview key for %s", action)
		}
		_, command, quit := model.HandleKey(key)
		if quit || !reflect.DeepEqual(command, want) {
			t.Errorf("%s on Overview sent %+v, want %+v", action, command, want)
		}
	}
	// A confirming snapshot reaches the rows; nothing else in it changed.
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{ProxyMode: mode, Monitor: core.MonitorSnapshot{SwitchPolicy: policy}}})
	rows = model.Rows()
	if index := rowIndex(rows, "overview:proxy-mode"); index < 0 || rows[index].Detail != localize.Code("proxy.mode", mode) {
		t.Fatalf("mode row after snapshot = %+v", rows)
	}
	if index := rowIndex(rows, "overview:switch-policy"); index < 0 || rows[index].Detail != localize.Code("monitor.policy", policy) {
		t.Fatalf("policy row after snapshot = %+v", rows)
	}
}
