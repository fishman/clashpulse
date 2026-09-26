package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestTraySelectsProfileAndProxyWithMeasuredLatency(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := &desktopUI{connected: true, window: app.NewWindow("ClashPulse"), quit: func() {}, actions: make(chan ipc.Command, 2)}
	state := core.Snapshot{
		Subscriptions: []core.SubscriptionSnapshot{{ID: "primary", SourceHost: "provider.example", Enabled: true, Active: true, HashPrefix: "abcdef"}},
		Groups: []core.GroupSnapshot{
			{ID: "main", Type: "Selector", Selected: "alpha", Proxies: []string{"alpha", "beta"}},
			{ID: "native", Type: "URLTest", Selected: "gamma", Proxies: []string{"gamma"}},
		},
		Proxies: []core.ProxySnapshot{{GroupID: "main", ID: "alpha", LatencyMillis: 300, Outcome: "success"}, {GroupID: "main", ID: "beta", LatencyMillis: 180, Outcome: "success"}, {GroupID: "native", ID: "gamma", LatencyMillis: 300, Outcome: "success"}},
	}
	menu := view.trayMenu(state)
	if !strings.Contains(menu.Items[0].Label, "selected median 300 ms") {
		t.Fatalf("profile latency absent: %q", menu.Items[0].Label)
	}
	profiles := menu.Items[1].ChildMenu.Items
	if len(profiles) != 1 || !profiles[0].Checked || profiles[0].Disabled {
		t.Fatalf("active profile control = %+v", profiles)
	}
	profiles[0].Action()
	if command := <-view.actions; command.Kind != ipc.CommandActivateSubscription || command.SubscriptionID != "primary" {
		t.Fatalf("profile action = %+v", command)
	}
	groups := menu.Items[2].ChildMenu.Items
	if len(groups) != 1 || len(groups[0].ChildMenu.Items) != 2 {
		t.Fatalf("native URLTest was offered manual selection: %+v", groups)
	}
	entries := groups[0].ChildMenu.Items
	if !strings.HasSuffix(entries[0].Label, "180 ms") || !strings.HasSuffix(entries[1].Label, "300 ms") {
		t.Fatalf("tray proxy list is not ordered fastest-first: %q %q", entries[0].Label, entries[1].Label)
	}
	checked := 0
	for _, item := range entries {
		if item.Checked {
			checked++
			if !strings.HasSuffix(item.Label, "300 ms") {
				t.Fatalf("selected proxy is not the marked one: %q", item.Label)
			}
		}
	}
	if checked != 1 {
		t.Fatalf("marked proxies = %d, want 1", checked)
	}
	entries[0].Action()
	if command := <-view.actions; command.Kind != ipc.CommandSelectGroup || command.GroupID != "main" || command.ChoiceID != "beta" {
		t.Fatalf("proxy action = %+v", command)
	}
	signature := trayStateSignature(state, true)
	state.Jobs = []core.JobSnapshot{{ID: "fetch", Kind: "subscription", State: "running"}}
	if trayStateSignature(state, true) != signature {
		t.Fatal("unrelated job rebuilt tray menu")
	}
	state.Proxies[0].LatencyMillis = 301
	if trayStateSignature(state, true) == signature {
		t.Fatal("new latency did not update tray state")
	}
	state.Proxies[0].Outcome = "timeout"
	if title := profileTrayTitle(state); !strings.Contains(title, "not measured") {
		t.Fatalf("timeout treated as good latency: %q", title)
	}
	if signature := trayStateSignature(state, false); signature != "disconnected" {
		t.Fatalf("offline tray kept stale actions: %q", signature)
	}
}

func TestTrayServiceToggleStartsAndStops(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := &desktopUI{connected: true, window: a.NewWindow("ClashPulse"), actions: make(chan ipc.Command, 2)}
	itemFor := func(state core.Snapshot, label string) *fyne.MenuItem {
		for _, item := range d.trayMenu(state).Items {
			if item.Label == label {
				return item
			}
		}
		return nil
	}
	stopped := core.Snapshot{}
	items := d.trayMenu(stopped).Items
	service, systemProxy := itemFor(stopped, "Service"), itemFor(stopped, "System Proxy")
	if service == nil || service.Checked || service.Disabled || systemProxy == nil {
		t.Fatalf("stopped service toggle = %+v", service)
	}
	if indexOf(items, "Service")+1 != indexOf(items, "System Proxy") {
		t.Fatal("service and system proxy toggles are not adjacent")
	}
	service.Action()
	if command := <-d.actions; command.Kind != ipc.CommandStart {
		t.Fatalf("stopped service toggle sent %+v", command)
	}
	before := trayStateSignature(stopped, true)
	running := core.Snapshot{ServiceRunning: true}
	if trayStateSignature(running, true) == before {
		t.Fatal("service state did not refresh tray menu")
	}
	service = itemFor(running, "Service")
	if service == nil || !service.Checked {
		t.Fatalf("running service toggle = %+v", service)
	}
	service.Action()
	if command := <-d.actions; command.Kind != ipc.CommandStop {
		t.Fatalf("running service toggle sent %+v", command)
	}
}

func indexOf(items []*fyne.MenuItem, label string) int {
	for i, item := range items {
		if item.Label == label {
			return i
		}
	}
	return -1
}

func TestTrayProxyOrderRanksMeasuredBeforeUnknownAndUnavailable(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := &desktopUI{connected: true, window: app.NewWindow("ClashPulse"), quit: func() {}, actions: make(chan ipc.Command, 2)}
	state := core.Snapshot{
		Groups: []core.GroupSnapshot{{ID: "main", Type: "Selector", Selected: "slow", Proxies: []string{"failed", "unknown", "slow", "fast"}}},
		Proxies: []core.ProxySnapshot{
			{GroupID: "main", ID: "failed", Outcome: "timeout"},
			{GroupID: "main", ID: "unknown"},
			{GroupID: "main", ID: "slow", Outcome: "success", LatencyMillis: 900},
			{GroupID: "main", ID: "fast", Outcome: "success", LatencyMillis: 100},
		},
	}
	items := view.trayMenu(state).Items[2].ChildMenu.Items[0].ChildMenu.Items
	want := []string{"100 ms", "900 ms", "not measured", "unavailable"}
	if len(items) != len(want) {
		t.Fatalf("tray entries = %d, want %d", len(items), len(want))
	}
	for i, suffix := range want {
		if !strings.HasSuffix(items[i].Label, " - "+suffix) {
			t.Fatalf("tray entry %d = %q, want suffix %q", i, items[i].Label, suffix)
		}
	}
}

func TestTrayLatencyUsesMedianAcrossSelectedGroups(t *testing.T) {
	state := core.Snapshot{
		Subscriptions: []core.SubscriptionSnapshot{{ID: "active", Active: true}},
		Groups:        []core.GroupSnapshot{{ID: "one", Type: "Selector", Selected: "alpha"}, {ID: "two", Type: "URLTest", Selected: "beta"}},
		Proxies:       []core.ProxySnapshot{{GroupID: "one", ID: "alpha", Outcome: "success", LatencyMillis: 300}, {GroupID: "two", ID: "beta", Outcome: "success", LatencyMillis: 200}},
	}
	if latency, ok := selectedProfileLatency(state); !ok || latency != 250 {
		t.Fatalf("profile median = %d, %t", latency, ok)
	}
	state.Proxies = state.Proxies[:1]
	if _, ok := selectedProfileLatency(state); ok {
		t.Fatal("missing group sample claimed profile latency")
	}
}

func TestTraySystemProxyToggleWaitsForSnapshot(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	d := &desktopUI{connected: true, window: a.NewWindow("ClashPulse"), actions: make(chan ipc.Command, 2)}
	itemFor := func(state core.Snapshot) *fyne.MenuItem {
		for _, item := range d.trayMenu(state).Items {
			if strings.HasPrefix(item.Label, "System Proxy") {
				return item
			}
		}
		return nil
	}
	state := core.Snapshot{}
	item := itemFor(state)
	if item == nil || item.Checked || item.Disabled {
		t.Fatalf("system proxy toggle unavailable in connected tray: %+v", item)
	}
	before := trayStateSignature(state, true)
	item.Action()
	command := <-d.actions
	if command.Kind != ipc.CommandUpdateConfiguration || command.Config == nil || command.Config.SystemProxyEnabled == nil || !*command.Config.SystemProxyEnabled || item.Checked {
		t.Fatalf("enable changed state before an IPC snapshot or sent wrong command: %+v", command)
	}
	state.SystemProxy.Enabled = true
	if trayStateSignature(state, true) == before {
		t.Fatal("system proxy snapshot change did not refresh tray menu")
	}
	item = itemFor(state)
	if item == nil || !item.Checked || !strings.Contains(item.Label, "inactive") {
		t.Fatalf("requested but inactive system proxy status is misleading: %+v", item)
	}
	item.Action()
	command = <-d.actions
	if command.Config == nil || command.Config.SystemProxyEnabled == nil || *command.Config.SystemProxyEnabled {
		t.Fatalf("disable did not send a typed system proxy command: %+v", command)
	}
}
