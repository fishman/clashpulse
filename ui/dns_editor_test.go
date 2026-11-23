package ui

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestDNSResolverAddEditRemovePreservesUnchangedEntriesUntilSnapshot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newDNSSettings(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	current := core.DNSSnapshot{
		Listen:       "127.0.0.1:5353",
		ResolverSets: []core.ResolverSetSnapshot{{ID: "primary", Endpoints: []string{"udp://192.0.2.53:53"}}},
		Routes:       []core.DNSRouteSnapshot{{Suffix: "example.test", ResolverSet: "primary"}},
	}
	page.update(current)
	page.addResolver.OnTapped()
	page.resolverEditor.id.SetText("backup")
	page.resolverEditor.endpoints.SetText("https://resolver.example/dns-query")
	page.resolverEditor.dialog.Submit()
	if len(sent) != 1 || sent[0].Kind != ipc.CommandSetDNSRouting || sent[0].DNSRouting == nil {
		t.Fatal("add did not send typed DNS routing intent")
	}
	if got := sent[0].DNSRouting; len(got.ResolverSets) != 2 || got.ResolverSets[0].ID != "primary" || got.ResolverSets[1].ID != "backup" || len(got.Routes) != 1 || got.Routes[0].Suffix != "example.test" {
		t.Fatalf("add did not preserve the full current routing table: %+v", got)
	}
	if len(page.snapshot.ResolverSets) != 1 || page.snapshot.ResolverSets[0].ID != "primary" {
		t.Fatal("pending add changed the displayed DNS snapshot before an event")
	}

	page.update(core.DNSSnapshot{Listen: current.Listen, ResolverSets: []core.ResolverSetSnapshot{{ID: "primary", Endpoints: []string{"udp://192.0.2.53:53"}}, {ID: "backup", Endpoints: []string{"https://resolver.example/dns-query"}}}, Routes: []core.DNSRouteSnapshot{{Suffix: "example.test", ResolverSet: "backup"}}})
	page.resolverRows[1].edit.OnTapped()
	page.resolverEditor.endpoints.SetText("tcp://192.0.2.54:53")
	page.resolverEditor.dnscrypt.SetChecked(false)
	page.resolverEditor.id.SetText("secondary")
	page.resolverEditor.dialog.Submit()
	if len(sent) != 2 || sent[1].DNSRouting == nil || sent[1].DNSRouting.ResolverSets[0].ID != "primary" || sent[1].DNSRouting.ResolverSets[1].ID != "secondary" || sent[1].DNSRouting.ResolverSets[1].Endpoints[0] != "tcp://192.0.2.54:53" || sent[1].DNSRouting.Routes[0].ResolverSet != "secondary" {
		t.Fatalf("edit did not preserve unchanged entries: %+v", sent)
	}
	if page.snapshot.ResolverSets[1].Endpoints[0] != "https://resolver.example/dns-query" {
		t.Fatal("pending edit changed the displayed DNS snapshot before an event")
	}

	page.update(core.DNSSnapshot{Listen: current.Listen, ResolverSets: []core.ResolverSetSnapshot{{ID: "primary", Endpoints: current.ResolverSets[0].Endpoints}, {ID: "secondary", Endpoints: []string{"tcp://192.0.2.54:53"}}}, Routes: []core.DNSRouteSnapshot{{Suffix: "example.test", ResolverSet: "secondary"}}})
	page.resolverRows[1].remove.OnTapped()
	if len(sent) != 2 {
		t.Fatal("removed a resolver set still referenced by a route")
	}
	page.update(core.DNSSnapshot{Listen: current.Listen, ResolverSets: []core.ResolverSetSnapshot{{ID: "primary", Endpoints: current.ResolverSets[0].Endpoints}, {ID: "secondary", Endpoints: []string{"tcp://192.0.2.54:53"}}}, Routes: current.Routes})
	page.resolverRows[1].remove.OnTapped()
	if len(sent) != 3 || len(sent[2].DNSRouting.ResolverSets) != 1 || sent[2].DNSRouting.ResolverSets[0].ID != "primary" || len(sent[2].DNSRouting.Routes) != 1 {
		t.Fatalf("remove did not send full table without unrelated route loss: %+v", sent)
	}
	if len(page.snapshot.ResolverSets) != 2 {
		t.Fatal("pending resolver removal changed the displayed snapshot before an event")
	}
}

func TestDNSRouteAddEditRemoveUsesOneMatcherAndWaitsForSnapshot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newDNSSettings(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	current := core.DNSSnapshot{
		ResolverSets: []core.ResolverSetSnapshot{{ID: "primary", Endpoints: []string{"udp://192.0.2.53:53"}}},
		Routes:       []core.DNSRouteSnapshot{{Suffix: "example.test", ResolverSet: "primary"}},
	}
	page.update(current)
	page.addRoute.OnTapped()
	page.routeEditor.matcher.SetSelected("GeoSite")
	page.routeEditor.value.SetText("private")
	page.routeEditor.resolver.SetSelected("primary")
	page.routeEditor.dialog.Submit()
	if len(sent) != 1 || sent[0].DNSRouting == nil || len(sent[0].DNSRouting.Routes) != 2 {
		t.Fatal("add did not send full typed DNS routes")
	}
	added := sent[0].DNSRouting.Routes[1]
	if added.GeoSite != "private" || added.Suffix != "" || added.Resource != "" || added.ResolverSet != "primary" {
		t.Fatalf("route did not encode exactly one matcher: %+v", added)
	}
	if !reflect.DeepEqual(page.snapshot.Routes, current.Routes) {
		t.Fatal("pending route add changed the displayed DNS snapshot before an event")
	}

	page.update(core.DNSSnapshot{ResolverSets: current.ResolverSets, Routes: []core.DNSRouteSnapshot{{Suffix: "example.test", ResolverSet: "primary"}, {GeoSite: "private", ResolverSet: "primary"}}})
	page.routeRows[1].edit.OnTapped()
	page.routeEditor.matcher.SetSelected("Resource ID")
	page.routeEditor.value.SetText("managed-domains")
	page.routeEditor.dialog.Submit()
	if len(sent) != 2 {
		t.Fatal("route edit did not send an intent")
	}
	updated := sent[1].DNSRouting.Routes[1]
	if updated.Resource != "managed-domains" || updated.Suffix != "" || updated.GeoSite != "" || updated.ResolverSet != "primary" || sent[1].DNSRouting.Routes[0].Suffix != "example.test" {
		t.Fatalf("edit lost unchanged routes or set multiple matchers: %+v", sent[1].DNSRouting.Routes)
	}
	if page.snapshot.Routes[1].GeoSite != "private" {
		t.Fatal("pending route edit changed the displayed DNS snapshot before an event")
	}

	page.routeRows[1].remove.OnTapped()
	if len(sent) != 3 || len(sent[2].DNSRouting.Routes) != 1 || sent[2].DNSRouting.Routes[0].Suffix != "example.test" {
		t.Fatalf("remove did not preserve the remaining route: %+v", sent)
	}
	if len(page.snapshot.Routes) != 2 {
		t.Fatal("pending route removal changed the displayed snapshot before an event")
	}
}

func TestDNSListenerEditSendsLoopbackConfigPatch(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newDNSSettings(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	page.update(core.DNSSnapshot{Listen: "127.0.0.1:5353"})
	page.editListener.OnTapped()
	page.listenerEditor.SetText("[::1]:5354")
	page.listenerDialog.Submit()
	if len(sent) != 1 || sent[0].Kind != ipc.CommandUpdateConfiguration || sent[0].Config == nil || sent[0].Config.DNSListen == nil || *sent[0].Config.DNSListen != "[::1]:5354" {
		t.Fatal("listener change did not send a typed loopback listener patch")
	}
	if page.snapshot.Listen != "127.0.0.1:5353" {
		t.Fatal("pending listener edit changed the displayed DNS snapshot before an event")
	}

	page.editListener.OnTapped()
	page.listenerEditor.SetText("0.0.0.0:53")
	page.listenerDialog.Submit()
	if len(sent) != 1 {
		t.Fatal("non-loopback listener was sent")
	}
}

func TestDNSResolverEditorRejectsEndpointCredentials(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	id, endpoints := widget.NewEntry(), widget.NewEntry()
	id.SetText("primary")
	endpoints.SetText("https://user:secret@resolver.example/dns-query")
	editor := &dnsResolverEditor{id: id, endpoints: endpoints, dnscrypt: widget.NewCheck("DNSCrypt", nil)}
	if _, _, err := editor.command(core.DNSSnapshot{}); err == nil {
		t.Fatal("resolver endpoint with credentials passed desktop validation")
	}
}

func TestDNSRowActionsTargetTheirOwnEntry(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newDNSSettings(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	page.update(core.DNSSnapshot{
		ResolverSets: []core.ResolverSetSnapshot{{ID: "primary", Endpoints: []string{"udp://192.0.2.53:53"}}, {ID: "backup", Endpoints: []string{"udp://192.0.2.54:53"}}},
		Routes:       []core.DNSRouteSnapshot{{Suffix: "one.example", ResolverSet: "primary"}, {Suffix: "two.example", ResolverSet: "primary"}},
	})
	if len(page.resolverRows) != 2 || len(page.routeRows) != 2 {
		t.Fatalf("rows = %d resolver, %d route; want 2 each", len(page.resolverRows), len(page.routeRows))
	}

	page.resolverRows[1].edit.OnTapped()
	if page.resolverEditor == nil || page.resolverEditor.existingID != "backup" {
		t.Fatal("second resolver row did not edit the second set")
	}
	page.resolverEditor.endpoints.SetText("udp://192.0.2.55:53")
	page.resolverEditor.dialog.Submit()
	if got := sent[len(sent)-1].DNSRouting.ResolverSets; len(got) != 2 || got[0].ID != "primary" || got[0].Endpoints[0] != "udp://192.0.2.53:53" || got[1].Endpoints[0] != "udp://192.0.2.55:53" {
		t.Fatalf("resolver edit = %+v", got)
	}
	page.resolverRows[1].remove.OnTapped()
	if got := sent[len(sent)-1].DNSRouting.ResolverSets; len(got) != 1 || got[0].ID != "primary" {
		t.Fatalf("resolver remove = %+v", got)
	}

	page.routeRows[1].edit.OnTapped()
	if page.routeEditor == nil || page.routeEditor.value.Text != "two.example" {
		t.Fatal("second route row did not edit the second route")
	}
	page.routeEditor.value.SetText("three.example")
	page.routeEditor.dialog.Submit()
	if got := sent[len(sent)-1].DNSRouting.Routes; len(got) != 2 || got[0].Suffix != "one.example" || got[1].Suffix != "three.example" {
		t.Fatalf("route edit = %+v", got)
	}
	page.routeRows[1].remove.OnTapped()
	if got := sent[len(sent)-1].DNSRouting.Routes; len(got) != 1 || got[0].Suffix != "one.example" {
		t.Fatalf("route remove = %+v", got)
	}
	if len(sent) != 4 {
		t.Fatalf("sent %d intents, want 4", len(sent))
	}
}
