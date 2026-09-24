package app

import "testing"

func TestAutomaticProbeRequiresManagedSelectorOptIn(t *testing.T) {
	for _, input := range []struct {
		groupType              string
		monitor, optedIn, want bool
	}{
		{"Selector", true, false, false},
		{"URLTest", true, true, false},
		{"Selector", false, true, false},
		{"Selector", true, true, true},
	} {
		if got := automaticProbeEnabled(input.groupType, input.monitor, input.optedIn); got != input.want {
			t.Fatalf("group %s monitor %t optedIn %t: probe=%t want=%t", input.groupType, input.monitor, input.optedIn, got, input.want)
		}
	}
}
