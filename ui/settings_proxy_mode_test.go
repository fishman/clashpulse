package ui

import (
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestSettingsProxyModeSelectShowsSnapshotAndSendsChange(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newSettingsPage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	if !containsObject(page.sectionViews["System Proxy"], page.proxyMode) {
		t.Fatal("proxy mode select is missing from the System Proxy section")
	}
	if got := strings.Join(page.proxyMode.Options, "|"); got != "Rule|Global|Direct" {
		t.Fatalf("proxy mode options = %q", got)
	}
	page.updateProxyMode(core.ProxyModeGlobal)
	if page.proxyMode.Selected != "Global" || len(sent) != 0 {
		t.Fatalf("snapshot mode shown as %q and sent %+v", page.proxyMode.Selected, sent)
	}
	page.proxyMode.SetSelected("Direct")
	want := []ipc.Command{{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{ProxyMode: new(core.ProxyModeDirect)}}}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("proxy mode change sent %+v", sent)
	}
	page.updateProxyMode(core.ProxyModeRule)
	if page.proxyMode.Selected != "Rule" || !reflect.DeepEqual(sent, want) {
		t.Fatalf("snapshot update echoed intent: shown %q, sent %+v", page.proxyMode.Selected, sent)
	}
}

func containsObject(object, target fyne.CanvasObject) bool {
	if object == target {
		return true
	}
	if box, ok := object.(*fyne.Container); ok {
		for _, child := range box.Objects {
			if containsObject(child, target) {
				return true
			}
		}
	}
	return false
}
