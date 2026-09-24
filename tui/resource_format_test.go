package tui

import (
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestManagedRowsShowDeclaredFormatAndRuleType(t *testing.T) {
	event := ipc.Event{Snapshot: core.Snapshot{
		Resources: []core.ResourceSnapshot{{ID: "cn", Kind: "rule-set", Format: "yaml", RuleType: "domain", Enabled: true}},
		Filters:   []core.FilterSnapshot{{ID: "ads", ResourceID: "cn", Format: "yaml", Target: "Proxy", Enabled: true}},
	}}
	resource := NewModel().Apply(event).selectTab(TabResources).Rows()[0]
	if !strings.Contains(resource.Detail, "yaml") || !strings.Contains(resource.Detail, "domain") {
		t.Fatalf("resource format and rule type hidden: %+v", resource)
	}
	filter := NewModel().Apply(event).selectTab(TabFilters).Rows()[0]
	if !strings.Contains(filter.Detail, "yaml") {
		t.Fatalf("filter format hidden: %+v", filter)
	}
}

func TestTerminalRowsDoNotEchoPrivateFailureText(t *testing.T) {
	secret := "https://provider.invalid/profile?token=secret"
	event := ipc.Event{Snapshot: core.Snapshot{
		Subscriptions: []core.SubscriptionSnapshot{{ID: "daily", LastFailure: secret}},
		Resources:     []core.ResourceSnapshot{{ID: "cn", LastResult: secret}},
		Filters:       []core.FilterSnapshot{{ID: "ads", LastFailure: secret}},
	}}
	for _, tab := range []Tab{TabSubscriptions, TabResources, TabFilters} {
		row := NewModel().Apply(event).selectTab(tab).Rows()[0]
		if strings.Contains(row.Detail, secret) {
			t.Fatalf("%s row revealed private source: %+v", tab, row)
		}
	}
}
