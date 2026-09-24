package tui

import (
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestSettingsShowVerifiedBinaryCapabilities(t *testing.T) {
	event := ipc.Event{Snapshot: core.Snapshot{Binary: core.BinarySnapshot{Desired: "system", ObservedVersion: "v1.19.31", Capabilities: []string{"geoip.dat", "Country.mmdb"}}}}
	model := NewModel().Apply(event)
	for _, tab := range []Tab{TabOverview, TabSettings} {
		row := model.selectTab(tab).Rows()[0]
		if !strings.Contains(row.Detail, "geoip.dat") || !strings.Contains(row.Detail, "Country.mmdb") {
			t.Fatalf("%s hides selected binary capabilities: %+v", tab, row)
		}
	}
}
