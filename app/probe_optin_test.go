package app

import "testing"

func TestProbeFollowsMonitorAndAutomationFollowsOptIn(t *testing.T) {
	for _, input := range []struct {
		groupType                 string
		monitor, optedIn          bool
		wantProbe, wantAutomation bool
	}{
		{"Selector", true, false, true, false},
		{"select", true, false, true, false},
		{"URLTest", true, true, false, false},
		{"Selector", false, true, false, false},
		{"Selector", true, true, true, true},
	} {
		probe := probeEnabled(input.groupType, input.monitor)
		automation := automationEnabled(input.groupType, input.monitor, input.optedIn)
		if probe != input.wantProbe || automation != input.wantAutomation {
			t.Fatalf("group %s monitor %t optedIn %t: probe=%t want=%t automation=%t want=%t",
				input.groupType, input.monitor, input.optedIn, probe, input.wantProbe, automation, input.wantAutomation)
		}
		if automation && !probe {
			t.Fatalf("group %s enables switching without measurement", input.groupType)
		}
	}
}
