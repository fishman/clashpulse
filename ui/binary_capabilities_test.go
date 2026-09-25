package ui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
)

func TestOverviewDisplaysVerifiedCapabilities(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(context.Background(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{Binary: core.BinarySnapshot{Desired: "system", ObservedVersion: "v1.19.31", Capabilities: []string{"geoip.dat", "Country.mmdb"}}})
	fyne.DoAndWait(func() {})
	for _, label := range rowLabels(view.views["Overview"]) {
		if label == "geoip.dat, Country.mmdb" {
			return
		}
	}
	t.Fatal("Overview omitted verified capability names")
}
