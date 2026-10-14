package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

func proxyModeModel(mode string) Model {
	return NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{ProxyMode: mode}}).selectTab(TabSettings)
}

func TestProxyModeRowShowsSnapshotModeAndKey(t *testing.T) {
	model := proxyModeModel(core.ProxyModeGlobal)
	rows := model.Rows()
	index := rowIndex(rows, "setting:proxy-mode")
	if index < 0 {
		t.Fatal("proxy mode settings row missing")
	}
	if row := rows[index]; row.Cells[0] != "Proxy mode" || row.Cells[1] != "Global" || row.Cells[2] != tunKey(t, model, "cycle_proxy_mode") {
		t.Fatalf("proxy mode row = %+v", row.Cells)
	}
}

func TestCycleProxyModeSendsNextModeInOrder(t *testing.T) {
	for i, mode := range core.ProxyModes {
		next := core.ProxyModes[(i+1)%len(core.ProxyModes)]
		model := proxyModeModel(mode)
		_, command, quit := model.HandleKey(tunKey(t, model, "cycle_proxy_mode"))
		if want := configCommand(&ipc.ConfigPatch{ProxyMode: &next}); quit || !reflect.DeepEqual(command, want) {
			t.Errorf("%s: cycle sent %+v, want %+v", mode, command, want)
		}
	}
}

func TestProxyModeMainlandText(t *testing.T) {
	keymap, err := DefaultKeymap()
	if err != nil {
		t.Fatal(err)
	}
	key := keymap.KeyFor(TabSettings, "cycle_proxy_mode")
	english := keymap.Help(TabSettings)
	localize.SetLanguage("zh-CN")
	t.Cleanup(func() { localize.SetLanguage("en") })
	mainland := keymap.Help(TabSettings)
	for i, entry := range english {
		if strings.HasPrefix(entry, key+" ") && mainland[i] == entry {
			t.Errorf("proxy mode help has no Mainland translation: %q", entry)
		}
	}
	rows := proxyModeModel(core.ProxyModeDirect).Rows()
	if i := rowIndex(rows, "setting:proxy-mode"); i < 0 || rows[i].Cells[0] != "代理模式" || rows[i].Cells[1] != "直连" {
		t.Fatalf("Mainland proxy mode row = %+v", rows)
	}
}
