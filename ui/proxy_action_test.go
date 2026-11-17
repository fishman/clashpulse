package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/monitor"
)

func TestURLTestGroupDoesNotOfferManagedActions(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	page := newProxyPage(func(command ipc.Command) { sent = append(sent, command) })
	page.update(core.Snapshot{Groups: []core.GroupSnapshot{{ID: "url-test", Type: "URLTest", Selected: "alpha", Proxies: []string{"alpha", "beta"}}}})
	if !page.automation.Disabled() || !page.probe.Disabled() {
		t.Fatal("managed-group controls remain enabled for URLTest")
	}
	page.list.OnSelected(1)
	page.automation.OnChanged(true)
	page.probe.OnTapped()
	if len(sent) != 0 {
		t.Fatalf("URLTest generated unsupported intent: %+v", sent)
	}
}

func TestManagedProxyGroupSelectionSendsIntent(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	page := newProxyPage(func(command ipc.Command) { sent = append(sent, command) })
	page.update(core.Snapshot{
		Groups:  []core.GroupSnapshot{{ID: "managed", Label: "select-main", Type: "Selector", Selected: "alpha", Proxies: []string{"alpha", "beta"}}},
		Proxies: []core.ProxySnapshot{{GroupID: "managed", ID: "alpha", Label: "node-a"}, {GroupID: "managed", ID: "beta", Label: "node-b"}},
	})
	if page.groupSelect.Selected != "select-main (Selector)" || page.list.Hidden || page.probe.Disabled() {
		t.Fatal("active managed group did not expose proxy choices")
	}
	page.list.OnSelected(1)
	if len(sent) != 1 || sent[0].Kind != ipc.CommandSelectGroup || sent[0].GroupID != "managed" || sent[0].ChoiceID != "beta" {
		t.Fatalf("proxy row selection command = %+v", sent)
	}
}

func TestStreamProbeButtonFollowsSteadyStreamPolicy(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(t.Context(), "", app.NewWindow("ClashPulse"))
	button := view.proxyPage.streamProbe
	if !button.Disabled() {
		t.Fatal("stream probe enabled before the service connected")
	}
	view.postSnapshot(core.Snapshot{Monitor: core.MonitorSnapshot{SwitchPolicy: monitor.SwitchSteadyStream}})
	fyne.DoAndWait(func() {})
	if button.Disabled() {
		t.Fatal("stream probe disabled under steady_stream")
	}
	button.OnTapped()
	select {
	case command := <-view.actions:
		if command.Kind != ipc.CommandStreamProbe || command.GroupID != "" {
			t.Fatalf("stream probe sent %+v", command)
		}
	default:
		t.Fatal("stream probe sent nothing")
	}
	view.postSnapshot(core.Snapshot{Monitor: core.MonitorSnapshot{SwitchPolicy: monitor.SwitchLowestLatency}})
	fyne.DoAndWait(func() {})
	if !button.Disabled() {
		t.Fatal("stream probe enabled under lowest_latency")
	}
	view.postSnapshot(core.Snapshot{Monitor: core.MonitorSnapshot{SwitchPolicy: monitor.SwitchSteadyStream}})
	view.postDisconnected()
	fyne.DoAndWait(func() {})
	if !button.Disabled() {
		t.Fatal("stream probe enabled while disconnected")
	}
}
