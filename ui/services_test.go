package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

func TestServicesSectionRowsAndCommands(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	section := newServicesSection(func(c ipc.Command) { sent = append(sent, c) }, app.NewWindow("x"))
	section.update([]core.ServiceSnapshot{{ID: "netflix", Name: "Netflix", Enabled: true, IntervalSeconds: 1800, Failures: 2, Outcome: "usable", Region: "US", Selected: "Proxy 3"}})
	if len(sent) != 0 {
		t.Fatalf("applying a snapshot sent %v", sent)
	}
	labels := strings.Join(rowLabels(section.content), "\n")
	for _, want := range []string{"ChatGPT", "Claude", "Gemini", "YouTube Premium", "Netflix", "Spotify", "Usable US - Proxy 3"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("missing %q in %q", want, labels)
		}
	}
	row := section.rows["netflix"]
	row.automatic.SetChecked(true)
	if len(sent) != 1 || sent[0].Kind != ipc.CommandSetService || !sent[0].Service.Automatic || !sent[0].Service.Enabled || sent[0].Service.IntervalSeconds != 1800 || sent[0].Service.Failures != 2 {
		t.Fatalf("automatic toggle sent %+v", sent)
	}
	test.Tap(row.check)
	if last := sent[len(sent)-1]; last.Kind != ipc.CommandCheckService || last.ServiceID != "netflix" {
		t.Fatalf("check now sent %+v", last)
	}
	if !section.rows["claude"].automatic.Disabled() || !section.rows["claude"].check.Disabled() {
		t.Fatal("automatic or check is enabled for a disabled service")
	}
}

// Saving the editor keeps hand-edited values the presets lack, and a resource
// list refreshed while the dialog is open does not change the saved choice.
func TestServiceEditorKeepsUnlistedValues(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("x")
	window.Resize(fyne.NewSize(900, 700))
	var sent []ipc.Command
	section := newServicesSection(func(c ipc.Command) { sent = append(sent, c) }, window)
	section.setResources([]core.ResourceSnapshot{{ID: "media", Kind: "rule-set", RuleType: "domain", Enabled: true}})
	section.update([]core.ServiceSnapshot{{ID: "netflix", Name: "Netflix", Enabled: true, IntervalSeconds: 2700, Failures: 8, DomainsResource: "media"}})
	section.openEditor("netflix")
	section.setResources(nil)
	var save *widget.Button
	for _, object := range test.LaidOutObjects(window.Canvas().Overlays().Top()) {
		if button, ok := object.(*widget.Button); ok && button.Text == localize.T("Save") {
			save = button
		}
	}
	if save == nil {
		t.Fatal("editor has no Save button")
	}
	test.Tap(save)
	if len(sent) != 1 || sent[0].Service.IntervalSeconds != 2700 || sent[0].Service.Failures != 8 || sent[0].Service.DomainsResource != "media" {
		t.Fatalf("save sent %+v", sent)
	}
}
