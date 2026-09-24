package ui

import (
	"strings"
	"testing"

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
	if !groups[0].ChildMenu.Items[0].Checked {
		t.Fatal("selected proxy was not marked")
	}
	groups[0].ChildMenu.Items[1].Action()
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
