package ui

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestSettingsTunRendersSnapshotAndSendsExactPatches(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	var sent []ipc.Command
	page := newSettingsPage(func(command ipc.Command) { sent = append(sent, command) }, a.NewWindow("ClashPulse"))
	if page.sectionViews["TUN"] == nil {
		t.Fatal("settings has no TUN section")
	}
	options := page.sidebar.Options
	if slices.Index(options, "TUN") != slices.Index(options, "System Proxy")+1 {
		t.Fatalf("TUN section does not follow System Proxy: %q", options)
	}
	page.updateTun(core.TunSnapshot{Enabled: true, Stack: "mixed", DNSMode: "fake-ip", Privilege: core.TunPrivilegeStale, PrivilegedPath: "/state.example/tun/mihomo", LastFailure: "Mihomo did not start the TUN interface"})
	if len(sent) != 0 {
		t.Fatalf("rendering a snapshot sent intent: %+v", sent)
	}
	if !page.tunEnabled.Checked || page.tunStack.Selected != "mixed" || page.tunDNSMode.Selected != "fake-ip" {
		t.Fatalf("TUN controls = enabled %t stack %q dns %q", page.tunEnabled.Checked, page.tunStack.Selected, page.tunDNSMode.Selected)
	}
	for _, want := range []string{"Requested yes, active no", "Mihomo was updated, re-authorize", "Mihomo did not start the TUN interface"} {
		if !strings.Contains(page.tunStatus.Text, want) {
			t.Fatalf("TUN status %q lacks %q", page.tunStatus.Text, want)
		}
	}
	if !page.tunAuthorize.Visible() || page.tunManual.Visible() || page.tunLeftover.Visible() {
		t.Fatal("stale privilege must offer Authorize only")
	}

	disabled, stack, dnsMode := false, "system", "redir-host"
	page.tunEnabled.SetChecked(false)
	page.tunStack.SetSelected(stack)
	page.tunDNSMode.SetSelected(dnsMode)
	page.tunAuthorize.OnTapped()
	want := []ipc.Command{
		{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{TunEnabled: &disabled}},
		{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{TunStack: &stack}},
		{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{TunDNSMode: &dnsMode}},
		{Kind: ipc.CommandAuthorizeTun},
	}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("TUN controls sent %+v", sent)
	}
}

func TestSettingsTunPrivilegeControlsAuthorizeAndManualCommand(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	const path = "/state.example/tun/mihomo"
	for _, tc := range []struct {
		privilege         string
		authorize, manual bool
		controlsDisabled  bool
		reasonFragment    string
	}{
		{core.TunPrivilegeReady, false, false, false, ""},
		{core.TunPrivilegeMissing, true, false, false, "not authorized"},
		{core.TunPrivilegeStale, true, false, false, "re-authorize"},
		{core.TunPrivilegeNoSUID, true, false, false, "nosuid"},
		{core.TunPrivilegeNoAgent, true, true, false, "authentication agent"},
		{core.TunPrivilegeUnsupported, false, false, true, "unsupported on this platform"},
	} {
		page := newSettingsPage(func(ipc.Command) {}, a.NewWindow("ClashPulse"))
		page.updateTun(core.TunSnapshot{Privilege: tc.privilege, PrivilegedPath: path, Stack: "gvisor", DNSMode: "profile"})
		if page.tunAuthorize.Visible() != tc.authorize {
			t.Errorf("%s: Authorize visible = %t", tc.privilege, page.tunAuthorize.Visible())
		}
		if page.tunManual.Visible() != tc.manual {
			t.Errorf("%s: manual command visible = %t", tc.privilege, page.tunManual.Visible())
		}
		if tc.manual && (page.tunManual.Text != "sudo setcap cap_net_admin+ep "+path || !page.tunManual.Selectable) {
			t.Errorf("%s: manual command = %q selectable %t", tc.privilege, page.tunManual.Text, page.tunManual.Selectable)
		}
		if page.tunEnabled.Disabled() != tc.controlsDisabled || page.tunStack.Disabled() != tc.controlsDisabled || page.tunDNSMode.Disabled() != tc.controlsDisabled {
			t.Errorf("%s: controls disabled = %t %t %t", tc.privilege, page.tunEnabled.Disabled(), page.tunStack.Disabled(), page.tunDNSMode.Disabled())
		}
		if tc.reasonFragment != "" && !strings.Contains(page.tunStatus.Text, tc.reasonFragment) {
			t.Errorf("%s: status %q lacks %q", tc.privilege, page.tunStatus.Text, tc.reasonFragment)
		}
	}
}

func TestSettingsTunLeftoverAndUnchangedSnapshot(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	page := newSettingsPage(func(ipc.Command) {}, a.NewWindow("ClashPulse"))
	state := core.TunSnapshot{Privilege: core.TunPrivilegeReady, Stack: "gvisor", DNSMode: "profile", Leftover: true}
	page.updateTun(state)
	if !page.tunLeftover.Visible() || page.tunLeftover.Text == "" {
		t.Fatal("leftover TUN device is not reported")
	}
	page.tunStatus.SetText("sentinel")
	page.updateTun(state)
	if page.tunStatus.Text != "sentinel" {
		t.Fatal("unchanged TUN snapshot rewrote the section")
	}
	state.Leftover = false
	page.updateTun(state)
	if page.tunLeftover.Visible() || page.tunStatus.Text == "sentinel" {
		t.Fatal("changed TUN snapshot did not update the section")
	}
}

func TestSnapshotUpdatesSettingsTunSection(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	view := newDesktopUI(context.Background(), "", a.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{Tun: core.TunSnapshot{Enabled: true, Active: true, Stack: "gvisor", DNSMode: "profile", Privilege: core.TunPrivilegeReady}})
	fyne.DoAndWait(func() {})
	if !view.settingsPage.tunEnabled.Checked || !strings.Contains(view.settingsPage.tunStatus.Text, "Requested yes, active yes") {
		t.Fatalf("snapshot did not reach the TUN section: %q", view.settingsPage.tunStatus.Text)
	}
}

func TestTrayTunToggleFollowsSnapshotAndPlatform(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := &desktopUI{connected: true, window: a.NewWindow("ClashPulse"), actions: make(chan ipc.Command, 2)}
	itemFor := func(state core.Snapshot) *fyne.MenuItem {
		items := d.trayMenu(state).Items
		for i, item := range items {
			if strings.HasPrefix(item.Label, "TUN") {
				if !strings.HasPrefix(items[i-1].Label, "System Proxy") {
					t.Fatal("TUN toggle does not follow the system proxy toggle")
				}
				return item
			}
		}
		t.Fatal("tray has no TUN toggle")
		return nil
	}
	state := core.Snapshot{Tun: core.TunSnapshot{Privilege: core.TunPrivilegeReady}}
	item := itemFor(state)
	if item.Checked || item.Disabled {
		t.Fatalf("ready TUN toggle = %+v", item)
	}
	before := trayStateSignature(state, true)
	item.Action()
	command := <-d.actions
	if command.Kind != ipc.CommandUpdateConfiguration || command.Config == nil || command.Config.TunEnabled == nil || !*command.Config.TunEnabled {
		t.Fatalf("enable sent %+v", command)
	}
	state.Tun.Enabled = true
	if trayStateSignature(state, true) == before {
		t.Fatal("TUN snapshot change did not refresh tray menu")
	}
	item = itemFor(state)
	if !item.Checked || !strings.Contains(item.Label, "inactive") {
		t.Fatalf("requested but inactive TUN is misleading: %+v", item)
	}
	item.Action()
	command = <-d.actions
	if command.Config == nil || command.Config.TunEnabled == nil || *command.Config.TunEnabled {
		t.Fatalf("disable sent %+v", command)
	}
	unsupported := core.Snapshot{Tun: core.TunSnapshot{Privilege: core.TunPrivilegeUnsupported}}
	if trayStateSignature(unsupported, true) == trayStateSignature(core.Snapshot{}, true) {
		t.Fatal("unsupported platform did not refresh tray menu")
	}
	item = itemFor(unsupported)
	if !item.Disabled || !strings.Contains(item.Label, "unsupported") {
		t.Fatalf("unsupported TUN toggle = %+v", item)
	}
	d.connected = false
	if item = itemFor(state); !item.Disabled {
		t.Fatal("disconnected tray kept the TUN toggle live")
	}
}

func TestTrayAutoFastestToggleSendsOneIntent(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := &desktopUI{connected: true, window: a.NewWindow("ClashPulse"), actions: make(chan ipc.Command, 2)}
	itemFor := func(state core.Snapshot) *fyne.MenuItem {
		for _, item := range d.trayMenu(state).Items {
			if item.Label == "Auto-select fastest connection" {
				return item
			}
		}
		t.Fatal("tray has no auto-select fastest toggle")
		return nil
	}
	off := core.Snapshot{}
	item := itemFor(off)
	if item.Checked || item.Disabled {
		t.Fatalf("auto fastest off = %+v", item)
	}
	item.Action()
	if command := <-d.actions; command.Kind != ipc.CommandSetAutoFastest || command.Automation == nil || !command.Automation.Enabled || command.GroupID != "" {
		t.Fatalf("enable sent %+v", command)
	}
	on := core.Snapshot{Monitor: core.MonitorSnapshot{AutoFastest: true}}
	if trayStateSignature(on, true) == trayStateSignature(off, true) {
		t.Fatal("auto fastest change did not refresh tray menu")
	}
	item = itemFor(on)
	if !item.Checked {
		t.Fatal("auto fastest on is not checked")
	}
	item.Action()
	if command := <-d.actions; command.Automation == nil || command.Automation.Enabled {
		t.Fatalf("disable sent %+v", command)
	}
	d.connected = false
	if item = itemFor(on); !item.Disabled {
		t.Fatal("disconnected tray kept auto fastest live")
	}
}

func TestTrayGroupMenuTogglesAutomation(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := &desktopUI{connected: true, window: a.NewWindow("ClashPulse"), actions: make(chan ipc.Command, 2)}
	snapshot := func(automated bool) core.Snapshot {
		return core.Snapshot{
			Monitor: core.MonitorSnapshot{Enabled: true},
			Groups:  []core.GroupSnapshot{{ID: "group-a", Label: "main", Type: "Selector", Selected: "proxy-a", Proxies: []string{"proxy-a", "proxy-b"}, AutomationEnabled: automated}},
			Proxies: []core.ProxySnapshot{{ID: "proxy-a", GroupID: "group-a", Label: "node-a"}, {ID: "proxy-b", GroupID: "group-a", Label: "node-b"}},
		}
	}
	toggleFor := func(state core.Snapshot) *fyne.MenuItem {
		for _, item := range d.trayMenu(state).Items {
			if item.Label == "Proxies" {
				group := item.ChildMenu.Items[0]
				if group.ChildMenu == nil || len(group.ChildMenu.Items) < 3 {
					t.Fatalf("group submenu = %+v", group.ChildMenu)
				}
				return group.ChildMenu.Items[0]
			}
		}
		t.Fatal("tray has no Proxies menu")
		return nil
	}
	item := toggleFor(snapshot(false))
	if item.Label != "Automatic switching" || item.Checked || item.Disabled {
		t.Fatalf("automation toggle = %+v", item)
	}
	item.Action()
	if command := <-d.actions; command.Kind != ipc.CommandSetAutomation || command.GroupID != "group-a" || command.Automation == nil || !command.Automation.Enabled {
		t.Fatalf("enable sent %+v", command)
	}
	if trayStateSignature(snapshot(true), true) == trayStateSignature(snapshot(false), true) {
		t.Fatal("automation change did not refresh tray menu")
	}
	if item = toggleFor(snapshot(true)); !item.Checked {
		t.Fatal("automated group is not checked")
	}
	item.Action()
	if command := <-d.actions; command.Automation == nil || command.Automation.Enabled {
		t.Fatalf("disable sent %+v", command)
	}
	// Automation only acts while the monitor runs, so the item must not
	// pretend a click took effect when it is off.
	off := snapshot(false)
	off.Monitor.Enabled = false
	if trayStateSignature(off, true) == trayStateSignature(snapshot(false), true) {
		t.Fatal("monitor change did not refresh tray menu")
	}
	if item = toggleFor(off); !item.Disabled || item.Label != "Automatic switching (monitor off)" {
		t.Fatalf("monitor-off automation toggle = %+v", item)
	}
	d.connected = false
	if item = toggleFor(snapshot(true)); !item.Disabled {
		t.Fatal("disconnected tray kept group automation live")
	}
}
