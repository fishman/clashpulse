package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

var tunActions = []string{"tun_enable", "tun_disable", "tun_authorize", "edit_tun_stack", "edit_tun_dns_mode"}

func tunModel(tun core.TunSnapshot) Model {
	return NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Tun: tun}}).selectTab(TabSettings)
}

func tunKey(t *testing.T, model Model, action string) string {
	t.Helper()
	key := model.keymap.KeyFor(TabSettings, action)
	if key == "" {
		t.Fatalf("no default settings key for %s", action)
	}
	return key
}

func TestTunSettingsRowShowsRequestActiveAndPrivilege(t *testing.T) {
	model := tunModel(core.TunSnapshot{Enabled: true, Privilege: core.TunPrivilegeStale, Stack: "mixed", DNSMode: "fake-ip",
		LastFailure: "tun did not start", Leftover: true})
	rows := model.Rows()
	index := rowIndex(rows, "setting:tun")
	if index < 0 {
		t.Fatal("TUN settings row missing")
	}
	row := rows[index]
	if row.Detail != "TUN: requested true; active false; privilege stale" || row.Cells[1] != "requested true; active false; privilege stale" {
		t.Fatalf("TUN row = %q / %q", row.Detail, row.Cells[1])
	}
	if row.Cells[2] != tunKey(t, model, "tun_disable") {
		t.Fatalf("enabled TUN row action key = %q", row.Cells[2])
	}
	for id, want := range map[string]string{
		"setting:tun:stack":    "mixed",
		"setting:tun:dns-mode": "fake-ip",
		"setting:tun:failure":  "tun did not start",
		"setting:tun:leftover": "clashpulse0",
	} {
		if i := rowIndex(rows, id); i < 0 || !strings.Contains(rows[i].Cells[1], want) {
			t.Errorf("row %s missing or without value containing %q", id, want)
		}
	}
	clean := tunModel(core.TunSnapshot{Privilege: core.TunPrivilegeReady, Stack: "gvisor", DNSMode: "profile"})
	cleanRows := clean.Rows()
	for _, id := range []string{"setting:tun:failure", "setting:tun:leftover"} {
		if rowIndex(cleanRows, id) >= 0 {
			t.Errorf("row %s shown without a failure or leftover", id)
		}
	}
	if i := rowIndex(cleanRows, "setting:tun"); i < 0 || cleanRows[i].Cells[2] != tunKey(t, clean, "tun_enable") {
		t.Fatalf("disabled TUN row = %+v", cleanRows)
	}
}

func TestTunSettingsRowUpdatesWhenOnlyTunChanges(t *testing.T) {
	model := tunModel(core.TunSnapshot{Privilege: core.TunPrivilegeMissing})
	model.Selection[TabSettings] = "setting:tun"
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{Tun: core.TunSnapshot{Enabled: true, Active: true, Privilege: core.TunPrivilegeReady}}})
	rows := model.Rows()
	if i := rowIndex(rows, "setting:tun"); i < 0 || rows[i].Cells[1] != "requested true; active true; privilege ready" || !rows[i].Selected {
		t.Fatalf("TUN row after update = %+v", rows)
	}
}

// File capabilities are ignored on a nosuid mount, so only a missing polkit
// agent gets the manual setcap command.
func TestTunManualCommandShownOnlyForNoAgent(t *testing.T) {
	const path = "/home/user/.local/state/clashpulse/tun/mihomo"
	for _, privilege := range []string{core.TunPrivilegeNoAgent, core.TunPrivilegeNoSUID, core.TunPrivilegeReady, core.TunPrivilegeMissing, core.TunPrivilegeStale} {
		model := tunModel(core.TunSnapshot{Enabled: true, Privilege: privilege, PrivilegedPath: path})
		rows := model.Rows()
		index := rowIndex(rows, "setting:tun:manual")
		shown := index >= 0
		want := privilege == core.TunPrivilegeNoAgent
		if shown != want {
			t.Errorf("%s: manual command shown = %t, want %t", privilege, shown, want)
		}
		if shown && want && rows[index].Cells[1] != "sudo setcap cap_net_admin+ep "+path {
			t.Errorf("%s: manual command = %q", privilege, rows[index].Cells[1])
		}
		if shown && want {
			// The value column truncates long paths; Enter shows the full command in the notice line.
			model.Selection[TabSettings] = rows[index].ID
			if next, command, _ := model.HandleKey("enter"); command != nil || next.Notice != "sudo setcap cap_net_admin+ep "+path {
				t.Errorf("%s: enter on manual row = %+v, notice %q", privilege, command, next.Notice)
			}
		}
	}
}

func TestTunActionsSendExactCommands(t *testing.T) {
	model := tunModel(core.TunSnapshot{Privilege: core.TunPrivilegeMissing, Stack: "gvisor", DNSMode: "profile"})
	for _, test := range []struct {
		action string
		want   ipc.Command
	}{
		{"tun_enable", ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{TunEnabled: new(true)}}},
		{"tun_disable", ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{TunEnabled: new(false)}}},
		{"tun_authorize", ipc.Command{Kind: ipc.CommandAuthorizeTun}},
	} {
		_, command, quit := model.HandleKey(tunKey(t, model, test.action))
		if quit || command == nil || !reflect.DeepEqual(*command, test.want) {
			t.Errorf("%s sent %+v, want %+v", test.action, command, test.want)
		}
	}
	for _, test := range []struct {
		action, value string
		want          ipc.ConfigPatch
	}{
		{"edit_tun_stack", "system", ipc.ConfigPatch{TunStack: new("system")}},
		{"edit_tun_dns_mode", "redir-host", ipc.ConfigPatch{TunDNSMode: new("redir-host")}},
	} {
		edited, command, _ := model.HandleKey(tunKey(t, model, test.action))
		if command != nil || edited.Modal == nil || edited.Focus != FocusModal {
			t.Fatalf("%s did not open an input modal: %+v / %+v", test.action, edited.Modal, command)
		}
		for _, r := range test.value {
			if edited, command, _ = edited.HandleKey(string(r)); command != nil {
				t.Fatalf("%s sent while typing", test.action)
			}
		}
		edited, command, _ = edited.HandleKey("enter")
		if command == nil || command.Kind != ipc.CommandUpdateConfiguration || !reflect.DeepEqual(*command.Config, test.want) || edited.Modal != nil {
			t.Errorf("%s sent %+v, want %+v", test.action, command, test.want)
		}
	}
}

func TestTunEnumInputRejectsInvalidValue(t *testing.T) {
	model := tunModel(core.TunSnapshot{Privilege: core.TunPrivilegeReady, Stack: "gvisor", DNSMode: "profile"})
	for _, test := range []struct{ action, value string }{
		{"edit_tun_stack", "lwip"},
		{"edit_tun_stack", ""},
		{"edit_tun_dns_mode", "fakeip"},
		{"edit_tun_dns_mode", "Profile"},
	} {
		edited, _, _ := model.HandleKey(tunKey(t, model, test.action))
		for _, r := range test.value {
			edited, _, _ = edited.HandleKey(string(r))
		}
		opened := edited.Notice
		edited, command, _ := edited.HandleKey("enter")
		if command != nil || edited.Modal == nil || edited.Notice == "" || edited.Notice == opened {
			t.Errorf("%s accepted %q: command %+v, notice %q", test.action, test.value, command, edited.Notice)
		}
	}
}

func TestTunActionsOnUnsupportedPlatformSendNothing(t *testing.T) {
	model := tunModel(core.TunSnapshot{Privilege: core.TunPrivilegeUnsupported})
	for _, action := range tunActions {
		next, command, quit := model.HandleKey(tunKey(t, model, action))
		if quit || command != nil || next.Modal != nil || next.Notice != "TUN is unsupported on this platform." {
			t.Errorf("%s on unsupported platform: command %+v, modal %+v, notice %q", action, command, next.Modal, next.Notice)
		}
	}
}

func TestDefaultKeymapBindsEveryTunActionWithLocalizedHelp(t *testing.T) {
	keymap, err := DefaultKeymap()
	if err != nil {
		t.Fatal(err)
	}
	english := keymap.Help(TabSettings)
	localize.SetLanguage("zh-CN")
	t.Cleanup(func() { localize.SetLanguage("en") })
	mainland := keymap.Help(TabSettings)
	for _, action := range tunActions {
		key := keymap.KeyFor(TabSettings, action)
		if got, ok := keymap.Action(TabSettings, key); key == "" || !ok || got != action {
			t.Errorf("%s bound to %q resolves to %q", action, key, got)
			continue
		}
		for i, entry := range english {
			if strings.HasPrefix(entry, key+" ") && mainland[i] == entry {
				t.Errorf("%s help has no Mainland translation: %q", action, entry)
			}
		}
	}
}
