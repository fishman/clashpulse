package ui

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/ipc"
)

func TestSettingsMixedPortShowsSnapshotAndSendsOnlyValidPort(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newSettingsPage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	page.updateMixedPort(7899)
	if page.mixedPort.Text != "7899" {
		t.Fatalf("mixed port label = %q, want snapshot port", page.mixedPort.Text)
	}
	edit := findSettingsButton(page.sectionViews["System Proxy"], "Change")
	if edit == nil {
		t.Fatal("mixed port edit is missing from the System Proxy section")
	}
	for _, input := range []string{"80", "70000", "abc"} {
		edit.OnTapped()
		page.mixedPortEditor.SetText(input)
		page.mixedPortDialog.Submit()
	}
	if len(sent) != 0 {
		t.Fatalf("invalid mixed ports sent %+v", sent)
	}
	edit.OnTapped()
	page.mixedPortEditor.SetText("8080")
	page.mixedPortDialog.Submit()
	port := uint32(8080)
	if want := []ipc.Command{{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{MixedPort: &port}}}; !reflect.DeepEqual(sent, want) {
		t.Fatalf("mixed port edit sent %+v", sent)
	}
	if page.mixedPort.Text != "7899" {
		t.Fatal("pending mixed port edit changed the displayed snapshot before an event")
	}
}
