package ui

import (
	"context"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
)

func TestOverviewDisplaysConfigurationErrorDetails(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(context.Background(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{Errors: []core.ErrorSnapshot{{File: "config.toml", Key: "monitor.interval", Message: "invalid configuration; previous settings remain active"}}})
	fyne.DoAndWait(func() {})
	got := view.errorSummary.Text
	for _, detail := range []string{"config.toml", "monitor.interval", "previous settings remain active"} {
		if !strings.Contains(got, detail) {
			t.Fatalf("service issue detail %q absent from %q", detail, got)
		}
	}
}
