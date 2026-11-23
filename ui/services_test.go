package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
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

func TestParseRegionsUpperCasesAndDeduplicates(t *testing.T) {
	if got := strings.Join(parseRegions(" us, jp us,,de "), ","); got != "US,JP,DE" {
		t.Fatalf("regions = %q", got)
	}
}
