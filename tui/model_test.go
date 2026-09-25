package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestLogOverlayScrollsWithoutDispatch(t *testing.T) {
	model := NewModel()
	model.Tab = TabProxies
	entries := make([]core.DiagnosticSnapshot, 50)
	for i := range entries {
		entries[i] = core.DiagnosticSnapshot{At: int64(i + 100), Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"}
	}
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{
		Groups:      []core.GroupSnapshot{{ID: "main", Proxies: []string{"alpha"}}},
		Proxies:     []core.ProxySnapshot{{ID: "alpha", GroupID: "main"}},
		Diagnostics: entries,
	}})
	selected := model.Selection[TabProxies]
	model, intent, quit := model.HandleKey("~")
	if !model.LogOpen || intent != nil || quit || model.Selection[TabProxies] != selected {
		t.Fatal("opening activity changed control state")
	}
	model, intent, quit = model.HandleKey("pageup")
	if model.LogOffset <= 0 || intent != nil || quit || model.Selection[TabProxies] != selected {
		t.Fatal("scrolling activity moved the proxy cursor or dispatched intent")
	}
	model, intent, quit = model.HandleKey("q")
	if model.LogOpen || intent != nil || quit || model.Selection[TabProxies] != selected || model.Tab != TabProxies {
		t.Fatal("closing activity dispatched quit or changed focus")
	}
}

func TestApplyKeepsStableProxyCursorAndModalFocus(t *testing.T) {
	model := NewModel()
	model.Tab = TabProxies
	model.Selection[TabProxies] = "proxy:g:beta"
	model.Focus = FocusModal
	model.Modal = &Modal{Kind: ModalMonitorInterval, Input: "60"}
	model = model.Apply(eventFromJSON(t, `{"Snapshot":{"Groups":[{"ID":"g","Proxies":["beta","alpha"]}],"Subscriptions":[{"ID":"sub","Name":"before"}]}}`))

	if got := model.Selection[TabProxies]; got != "proxy:g:beta" {
		t.Fatalf("proxy cursor = %q, want stable proxy ID", got)
	}
	if model.Focus != FocusModal || model.Modal == nil || model.Modal.Input != "60" {
		t.Fatalf("modal focus did not survive snapshot: focus=%q modal=%#v", model.Focus, model.Modal)
	}

	model = model.Apply(eventFromJSON(t, `{"Snapshot":{"Revision":2,"Groups":[{"ID":"g","Proxies":["alpha","beta"]}],"Subscriptions":[{"ID":"sub","Name":"after"}]}}`))
	if got := model.Selection[TabProxies]; got != "proxy:g:beta" {
		t.Fatalf("proxy cursor after reorder = %q", got)
	}
}

func TestApplyFallsBackWhenSelectedIDDisappears(t *testing.T) {
	model := NewModel()
	model.Tab = TabSubscriptions
	model.Selection[TabSubscriptions] = "subscription:gone"
	model = model.Apply(eventFromJSON(t, `{"Snapshot":{"Subscriptions":[{"ID":"one"},{"ID":"two"},{"ID":"three"}]}}`))
	if got := model.Selection[TabSubscriptions]; got != "subscription:one" {
		t.Fatalf("missing selection fallback = %q, want first current row", got)
	}
}

func TestKeyHelpAndDispatchUseParsedBindings(t *testing.T) {
	keymap, err := NewKeymap([]byte(`[[binding]]
tab = "proxies"
key = "z"
action = "manual_probe"
help = "custom probe"
`))
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(keymap).Apply(eventFromJSON(t, `{"Snapshot":{"Groups":[{"ID":"main","Type":"Selector"}]}}`))
	model.Tab = TabProxies
	model.Selection[TabProxies] = "group:main"
	if got := model.Help(); len(got) != 1 || got[0] != "z custom probe" {
		t.Fatalf("help = %#v", got)
	}
	model, command, quit := model.HandleKey("z")
	if quit || command == nil || command.Kind != ipc.CommandManualProbe || command.GroupID != "main" {
		t.Fatalf("dispatch = model %#v command %#v quit %t", model, command, quit)
	}
}

func TestExistingListActionsDispatchStableTargets(t *testing.T) {
	for _, test := range []struct {
		tab      Tab
		key      string
		kind     ipc.CommandKind
		wantHelp string
		wantID   string
		snapshot string
		selected string
	}{
		{TabSubscriptions, "r", ipc.CommandRefreshSubscription, "r refresh subscription", "sub-1", `"Subscriptions":[{"ID":"sub-1"}]`, "subscription:sub-1"},
		{TabSubscriptions, "a", ipc.CommandActivateSubscription, "a activate subscription", "sub-1", `"Subscriptions":[{"ID":"sub-1"}]`, "subscription:sub-1"},
		{TabFilters, "r", ipc.CommandRefreshFilter, "r refresh filter", "filter-1", `"Filters":[{"ID":"filter-1"}]`, "filter:filter-1"},
		{TabResources, "r", ipc.CommandRefreshResource, "r refresh resource", "resource-1", `"Resources":[{"ID":"resource-1"}]`, "resource:resource-1"},
	} {
		model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Groups":[{"ID":"group"}],`+test.snapshot+`}}`))
		model.Tab = test.tab
		model.Selection[test.tab] = test.selected
		if !contains(model.Help(), test.wantHelp) {
			t.Errorf("%s help lacks %q: %v", test.tab, test.wantHelp, model.Help())
		}
		got, command, quit := model.HandleKey(test.key)
		if quit || command == nil || command.Kind != test.kind {
			t.Errorf("%s key %q returned command %#v, quit %t", test.tab, test.key, command, quit)
			continue
		}
		switch test.kind {
		case ipc.CommandRefreshSubscription, ipc.CommandActivateSubscription:
			if command.SubscriptionID != test.wantID {
				t.Errorf("subscription target = %q, want %q", command.SubscriptionID, test.wantID)
			}
		case ipc.CommandRefreshFilter:
			if command.FilterID != test.wantID {
				t.Errorf("filter target = %q, want %q", command.FilterID, test.wantID)
			}
		case ipc.CommandRefreshResource:
			if command.ResourceID != test.wantID {
				t.Errorf("resource target = %q, want %q", command.ResourceID, test.wantID)
			}
		}
		if got.Selection[test.tab] != test.selected {
			t.Errorf("%s action changed selection to %q", test.tab, got.Selection[test.tab])
		}
	}
}

func TestSubscriptionCreateEmitsTypedIntent(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Subscriptions":[{"ID":"existing","Name":"Existing"}]}}`))
	model.Tab = TabSubscriptions
	model.Selection[TabSubscriptions] = "subscription:existing"
	model, _, _ = model.HandleKey("n")
	if model.Modal == nil || model.Modal.Form == nil || model.Focus != FocusModal {
		t.Fatal("new subscription did not open a form table")
	}
	model = typeFormText(t, model, "new-feed")
	model, _, _ = model.HandleKey("down")
	model = typeFormText(t, model, "News")
	model, _, _ = model.HandleKey("down")
	model = typeFormText(t, model, "https://feeds.example/news?token=private")
	for range 2 {
		model, _, _ = model.HandleKey("down")
	}
	model, _, _ = model.HandleKey("down")
	model, _, _ = model.HandleKey("enter")
	model = typeFormText(t, model, "3600")
	model, _, _ = model.HandleKey("down")
	model, _, _ = model.HandleKey("enter")
	model = typeFormText(t, model, "12")
	model, _, _ = model.HandleKey("down")
	model, _, _ = model.HandleKey("enter")
	model, _, _ = model.HandleKey("enter")
	for range 2 {
		model, _, _ = model.HandleKey("down")
	}
	model, _, _ = model.HandleKey("space")
	model, command, quit := model.HandleKey("ctrl+s")
	if quit || command == nil || command.Kind != ipc.CommandPutSubscription || command.SubscriptionID != "new-feed" || command.Subscription == nil {
		t.Fatal("form did not create a typed subscription")
	}
	patch := command.Subscription
	if patch.Name == nil || *patch.Name != "News" || patch.URL == nil || *patch.URL != "https://feeds.example/news?token=private" ||
		patch.UserAgent != nil || patch.Enabled == nil || !*patch.Enabled || patch.RefreshIntervalSeconds == nil || *patch.RefreshIntervalSeconds != 3600 ||
		patch.TimeoutSeconds == nil || *patch.TimeoutSeconds != 12 || patch.Route == nil || *patch.Route != "mihomo_proxy" ||
		patch.AllowInvalidTLS == nil || !*patch.AllowInvalidTLS {
		t.Fatal("form did not preserve edited subscription fields")
	}
	if model.Modal != nil || model.Focus != FocusContent || model.Selection[TabSubscriptions] != "subscription:existing" {
		t.Fatal("Save changed tab focus or stable selection")
	}
}

func TestSubscriptionFormKeepsPrivateSourceOnToggle(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Subscriptions: []core.SubscriptionSnapshot{{
		ID: "feed", Name: "Feed", SourceHost: "provider.example", Route: "direct", Enabled: true,
		RefreshIntervalSeconds: 3600, TimeoutSeconds: 30,
	}}}})
	model.Tab = TabSubscriptions
	model.Selection[TabSubscriptions] = "subscription:feed"
	model, _, _ = model.HandleKey("e")
	if model.Modal == nil || model.Modal.Form == nil {
		t.Fatal("subscription form table not opened")
	}
	if !contains(model.Help(), "space toggle field") || !contains(model.Help(), "ctrl+s save form") {
		t.Fatal("form key help did not follow bindings")
	}
	for range 7 {
		model, _, _ = model.HandleKey("down")
	}
	model, _, _ = model.HandleKey("space")
	model, command, quit := model.HandleKey("ctrl+s")
	if quit || model.Modal != nil || command == nil || command.Kind != ipc.CommandPutSubscription || command.Subscription == nil ||
		command.Subscription.AllowHTTP == nil || !*command.Subscription.AllowHTTP || command.Subscription.URL != nil || command.Subscription.UserAgent != nil {
		t.Fatal("HTTP toggle changed private source or failed to submit")
	}
}

func TestSubscriptionFormCreatesPrivateAgentAfterUnrelatedSnapshot(t *testing.T) {
	model := NewModel()
	model.Tab = TabSubscriptions
	model, _, _ = model.HandleKey("n")
	for _, r := range "new-feed" {
		model, _, _ = model.HandleKey(string(r))
	}
	for range 2 {
		model, _, _ = model.HandleKey("down")
	}
	for _, r := range "https://provider.invalid/profile?token=private" {
		model, _, _ = model.HandleKey(string(r))
	}
	model, _, _ = model.HandleKey("down")
	for _, r := range "clash-verge/v2.5.6" {
		model, _, _ = model.HandleKey(string(r))
	}
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{Revision: 2, Jobs: []core.JobSnapshot{{ID: "unrelated"}}}})
	model, command, quit := model.HandleKey("ctrl+s")
	if quit || model.Modal != nil || command == nil || command.SubscriptionID != "new-feed" || command.Subscription == nil ||
		command.Subscription.URL == nil || *command.Subscription.URL != "https://provider.invalid/profile?token=private" ||
		command.Subscription.UserAgent == nil || *command.Subscription.UserAgent != "clash-verge/v2.5.6" ||
		command.Subscription.Enabled == nil || !*command.Subscription.Enabled {
		t.Fatal("form lost private edits or default enabled state across unrelated snapshot")
	}
}

func TestSubscriptionEditEmitsPatchWithoutPrivateURL(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Subscriptions":[{"ID":"sub-7","Name":"Before","SourceHost":"feeds.example","Enabled":true,"Route":"direct","RefreshIntervalSeconds":3600,"TimeoutSeconds":30}]}}`))
	model.Tab = TabSubscriptions
	model.Selection[TabSubscriptions] = "subscription:sub-7"
	model, _, _ = model.HandleKey("e")
	if model.Modal == nil || model.Modal.Form == nil || model.Focus != FocusModal {
		t.Fatal("subscription edit form did not open")
	}
	model, _, _ = model.HandleKey("enter")
	model = typeFormText(t, model, "Renamed")
	for range 3 {
		model, _, _ = model.HandleKey("down")
	}
	model, _, _ = model.HandleKey("space")
	model, _, _ = model.HandleKey("down")
	model, _, _ = model.HandleKey("enter")
	model = typeFormText(t, model, "900")
	for range 2 {
		model, _, _ = model.HandleKey("down")
	}
	model, _, _ = model.HandleKey("enter")
	model, _, _ = model.HandleKey("enter")
	for range 2 {
		model, _, _ = model.HandleKey("down")
	}
	model, _, _ = model.HandleKey("space")
	model, command, _ := model.HandleKey("ctrl+s")
	if command == nil || command.Kind != ipc.CommandPutSubscription || command.SubscriptionID != "sub-7" || command.Subscription == nil {
		t.Fatal("edit form did not dispatch a typed intent")
	}
	patch := command.Subscription
	if patch.Name == nil || *patch.Name != "Renamed" || patch.URL != nil || patch.UserAgent != nil ||
		patch.Enabled == nil || *patch.Enabled || patch.RefreshIntervalSeconds == nil || *patch.RefreshIntervalSeconds != 900 ||
		patch.TimeoutSeconds != nil || patch.Route == nil || *patch.Route != "mihomo_proxy" || patch.AllowHTTP != nil ||
		patch.AllowInvalidTLS == nil || !*patch.AllowInvalidTLS {
		t.Fatal("edit lost fields or overwrote private source")
	}
	if model.Modal != nil || model.Focus != FocusContent || model.Selection[TabSubscriptions] != "subscription:sub-7" {
		t.Fatal("edit changed focus or stable selection")
	}
	model = model.CommandQueued().CommandResult(false, errors.New("invalid URL https://private.example/?token=secret"), true)
	if strings.Contains(model.Notice, "private.example") || strings.Contains(model.Notice, "token=secret") {
		t.Fatal("subscription error disclosed private source")
	}
}

func TestSubscriptionEditCanReplaceSource(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Subscriptions":[{"ID":"sub-source","Name":"Feed","SourceHost":"feeds.example"}]}}`))
	model.Tab = TabSubscriptions
	model.Selection[TabSubscriptions] = "subscription:sub-source"
	model, _, _ = model.HandleKey("e")
	model, _, _ = model.HandleKey("down")
	model = typeFormText(t, model, "https://new.example/feed?token=private")
	model, command, _ := model.HandleKey("ctrl+s")
	if command == nil || command.Kind != ipc.CommandPutSubscription || command.SubscriptionID != "sub-source" || command.Subscription == nil ||
		command.Subscription.URL == nil || *command.Subscription.URL != "https://new.example/feed?token=private" || command.Subscription.UserAgent != nil {
		t.Fatal("source replacement form did not preserve private agent")
	}
}

func TestSubscriptionModalEscapeCancelsWithoutIntent(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Subscriptions":[{"ID":"keep","Name":"Keep"}]}}`))
	model.Tab = TabSubscriptions
	model.Selection[TabSubscriptions] = "subscription:keep"
	model, _, _ = model.HandleKey("n")
	model = typeFormText(t, model, "partial")
	model = model.Apply(eventFromJSON(t, `{"Snapshot":{"Subscriptions":[{"ID":"other","Name":"Other"},{"ID":"keep","Name":"Keep"}]}}`))
	if model.Focus != FocusModal || model.Modal == nil || model.Modal.Form == nil ||
		len(model.Modal.Form.Changes()) != 1 || model.Modal.Form.Changes()[0].Value != "partial" || model.Selection[TabSubscriptions] != "subscription:keep" {
		t.Fatal("unrelated snapshot changed pending form or stable selection")
	}
	model, command, _ := model.HandleKey("esc")
	if command != nil || model.Modal != nil || model.Focus != FocusContent || model.Selection[TabSubscriptions] != "subscription:keep" {
		t.Fatal("Escape did not cancel form without intent")
	}
}

func TestDeleteSubscriptionRequiresModalConfirmation(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Subscriptions":[{"ID":"sub-delete","Name":"Work profile"}]}}`))
	model.Tab = TabSubscriptions
	model.Selection[TabSubscriptions] = "subscription:sub-delete"
	model, command, _ := model.HandleKey("d")
	if command != nil || model.Modal == nil || model.Focus != FocusModal {
		t.Fatalf("delete did not request confirmation: model=%#v command=%#v", model, command)
	}
	if !contains(model.Help(), "d delete subscription") {
		t.Fatalf("subscription help omits delete binding: %v", model.Help())
	}
	model, command, _ = model.HandleKey("enter")
	if command == nil || command.Kind != ipc.CommandDeleteSubscription || command.SubscriptionID != "sub-delete" {
		t.Fatalf("confirmed deletion = %#v", command)
	}
	if model.Modal != nil || model.Focus != FocusContent || model.Selection[TabSubscriptions] != "subscription:sub-delete" {
		t.Fatalf("confirmed deletion changed focus or selection: %#v", model)
	}
}

func TestResourceCreateEmitsTypedPrivateIntent(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Resources":[{"ID":"keep"}]}}`))
	model.Tab = TabResources
	model.Selection[TabResources] = "resource:keep"
	model, command, _ := model.HandleKey("n")
	if model.Modal == nil || model.Focus != FocusModal || !contains(model.Help(), "n new resource") {
		t.Fatalf("new resource did not open from its binding: %#v", model)
	}
	url := "https://lists.example/rules.yaml?token=secret"
	sha := strings.Repeat("ab", 32)
	for i, value := range []string{"resource-new", "rule-set", "yaml", "domain", url, "no", "3600", sha} {
		model, command = fillModalField(t, model, value)
		if i < 7 && command != nil {
			t.Fatalf("field %d unexpectedly emitted intent: %#v", i, command)
		}
	}
	if command == nil || command.Kind != ipc.CommandPutResource || command.ResourceID != "resource-new" || command.Resource == nil {
		t.Fatalf("resource create intent = %#v", command)
	}
	patch := command.Resource
	if patch.Kind == nil || *patch.Kind != "rule-set" || patch.Format == nil || *patch.Format != "yaml" || patch.RuleType == nil || *patch.RuleType != "domain" || patch.URL == nil || *patch.URL != url || patch.Enabled == nil || *patch.Enabled || patch.IntervalSeconds == nil || *patch.IntervalSeconds != 3600 || patch.SHA256 == nil || *patch.SHA256 != sha {
		t.Fatalf("resource create fields = %#v", patch)
	}
	if model.Modal != nil || model.Focus != FocusContent || model.Selection[TabResources] != "resource:keep" {
		t.Fatalf("resource create changed focus or selection: %#v", model)
	}
	if !commandHasPrivateSource(*command) {
		t.Fatal("resource edit result was not marked private")
	}
	model = model.CommandQueued().CommandResult(false, errors.New("invalid source "+url), commandHasPrivateSource(*command))
	if strings.Contains(model.Notice, url) || strings.Contains(model.Notice, "token=secret") {
		t.Fatalf("resource error disclosed private URL: %q", model.Notice)
	}
}

func TestResourceEditTargetsStableIDAndOmitsPrivateURL(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Resources":[{"ID":"resource-7","Kind":"rule-set","Format":"yaml","RuleType":"domain","Enabled":true}]}}`))
	model.Tab = TabResources
	model.Selection[TabResources] = "resource:resource-7"
	model, _, _ = model.HandleKey("e")
	if model.Modal == nil || model.Focus != FocusModal || !contains(model.Help(), "e edit resource") {
		t.Fatalf("edit resource did not open from its binding: %#v", model)
	}
	var command *ipc.Command
	for i, value := range []string{"rule-provider", "text", "classical", "", "no", "", "-"} {
		model, command = replaceModalField(t, model, value)
		if i < 6 && command != nil {
			t.Fatalf("field %d unexpectedly emitted intent: %#v", i, command)
		}
	}
	if command == nil || command.Kind != ipc.CommandPutResource || command.ResourceID != "resource-7" || command.Resource == nil {
		t.Fatalf("resource edit intent = %#v", command)
	}
	patch := command.Resource
	if patch.Kind == nil || *patch.Kind != "rule-provider" || patch.Format == nil || *patch.Format != "text" || patch.RuleType == nil || *patch.RuleType != "classical" || patch.URL != nil || patch.Enabled == nil || *patch.Enabled || patch.IntervalSeconds != nil || patch.SHA256 == nil || *patch.SHA256 != "" {
		t.Fatalf("resource edit fields = %#v", patch)
	}
	if model.Modal != nil || model.Focus != FocusContent || model.Selection[TabResources] != "resource:resource-7" {
		t.Fatalf("resource edit changed focus or stable selection: %#v", model)
	}
}

func TestResourceEditCanReplacePrivateURL(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Resources":[{"ID":"resource-url","Kind":"geoip.dat","Format":"dat","Enabled":true}]}}`))
	model.Tab = TabResources
	model.Selection[TabResources] = "resource:resource-url"
	model, _, _ = model.HandleKey("e")
	var command *ipc.Command
	for i, value := range []string{"", "", "", "https://new.example/geoip?token=private", "", "", ""} {
		if i == 3 {
			model, command = replaceModalField(t, model, value)
		} else {
			model, command = fillModalField(t, model, value)
		}
	}
	if command == nil || command.Kind != ipc.CommandPutResource || command.ResourceID != "resource-url" || command.Resource == nil || command.Resource.URL == nil || *command.Resource.URL != "https://new.example/geoip?token=private" {
		t.Fatalf("resource URL replacement intent = %#v", command)
	}
}

func TestFilterCreateEmitsTypedIntent(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Filters":[{"ID":"keep-filter"}]}}`))
	model.Tab = TabFilters
	model.Selection[TabFilters] = "filter:keep-filter"
	model, _, _ = model.HandleKey("n")
	if model.Modal == nil || model.Focus != FocusModal || !contains(model.Help(), "n new filter") {
		t.Fatalf("new filter did not open from its binding: %#v", model)
	}
	var command *ipc.Command
	for i, value := range []string{"filter-new", "resource-rules", "text", "DIRECT", "yes"} {
		model, command = fillModalField(t, model, value)
		if i < 4 && command != nil {
			t.Fatalf("field %d unexpectedly emitted intent: %#v", i, command)
		}
	}
	if command == nil || command.Kind != ipc.CommandPutFilter || command.FilterID != "filter-new" || command.Filter == nil {
		t.Fatalf("filter create intent = %#v", command)
	}
	patch := command.Filter
	if patch.ResourceID == nil || *patch.ResourceID != "resource-rules" || patch.Format == nil || *patch.Format != "text" || patch.Target == nil || *patch.Target != "DIRECT" || patch.Enabled == nil || !*patch.Enabled {
		t.Fatalf("filter create fields = %#v", patch)
	}
	if model.Modal != nil || model.Focus != FocusContent || model.Selection[TabFilters] != "filter:keep-filter" {
		t.Fatalf("filter create changed focus or selection: %#v", model)
	}
}

func TestFilterEditTargetsStableIDAndCanDisable(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Filters":[{"ID":"filter-9","ResourceID":"resource-old","Target":"DIRECT","Enabled":true}]}}`))
	model.Tab = TabFilters
	model.Selection[TabFilters] = "filter:filter-9"
	model, _, _ = model.HandleKey("e")
	var command *ipc.Command
	for i, value := range []string{"resource-new", "text", "DIRECT", "no"} {
		model, command = replaceModalField(t, model, value)
		if i < 3 && command != nil {
			t.Fatalf("field %d unexpectedly emitted intent: %#v", i, command)
		}
	}
	if command == nil || command.Kind != ipc.CommandPutFilter || command.FilterID != "filter-9" || command.Filter == nil {
		t.Fatalf("filter edit intent = %#v", command)
	}
	if !commandHasPrivateSource(*command) {
		t.Fatal("filter edit result was not marked private")
	}
	patch := command.Filter
	if patch.ResourceID == nil || *patch.ResourceID != "resource-new" || patch.Format == nil || *patch.Format != "text" || patch.Target == nil || *patch.Target != "DIRECT" || patch.Enabled == nil || *patch.Enabled {
		t.Fatalf("filter edit fields = %#v", patch)
	}
	if model.Modal != nil || model.Focus != FocusContent || model.Selection[TabFilters] != "filter:filter-9" {
		t.Fatalf("filter edit changed focus or stable selection: %#v", model)
	}
}

func TestManagedEditorEscapeCancelsWithoutIntent(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Resources":[{"ID":"keep"}]}}`))
	model.Tab = TabResources
	model.Selection[TabResources] = "resource:keep"
	model, _, _ = model.HandleKey("n")
	for _, key := range []string{"p", "a", "r", "t", "i", "a", "l"} {
		model, _, _ = model.HandleKey(key)
	}
	model, command, _ := model.HandleKey("esc")
	if command != nil || model.Modal != nil || model.Focus != FocusContent || model.Selection[TabResources] != "resource:keep" {
		t.Fatalf("Escape did not cancel managed editor: model=%#v command=%#v", model, command)
	}
}

func TestEnterOnReadOnlyRowsPreservesFocusedGroup(t *testing.T) {
	for _, test := range []struct {
		tab   Tab
		field string
		rowID string
	}{
		{TabSubscriptions, `"Subscriptions":[{"ID":"sub"}]`, "subscription:sub"},
		{TabFilters, `"Filters":[{"ID":"filter"}]`, "filter:filter"},
		{TabResources, `"Resources":[{"ID":"resource"}]`, "resource:resource"},
	} {
		model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Groups":[{"ID":"focused"}],`+test.field+`}}`))
		model.Tab = test.tab
		model.Selection[test.tab] = test.rowID
		got, command, _ := model.HandleKey("enter")
		if command != nil || got.CurrentGroupID() != "focused" {
			t.Errorf("Enter on %s row changed focus: group=%q command=%#v", test.rowID, got.CurrentGroupID(), command)
		}
	}
}

func TestGroupAndProxyLabelsDoNotReplaceStableIdentities(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Groups":[{"ID":"group-id","Label":"Friendly group","Type":"Selector","Selected":"proxy-id","Proxies":["proxy-id"]}],"Proxies":[{"ID":"proxy-id","GroupID":"group-id","Label":"Friendly proxy"}]}}`))
	model.Tab = TabProxies
	rows := model.Rows()
	if len(rows) != 2 || rows[0].ID != "group:group-id" || rows[0].Title != "Friendly group" || !strings.Contains(rows[0].Detail, "selected Friendly proxy") {
		t.Fatalf("group label rendering or identity = %#v", rows)
	}
	if rows[1].ID != "proxy:group-id:proxy-id" || rows[1].Title != "  Friendly proxy" {
		t.Fatalf("proxy label rendering or identity = %#v", rows[1])
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSettingsShowSanitizedMonitorConfiguration(t *testing.T) {
	model := NewModel()
	model.Tab = TabSettings
	model = model.Apply(eventFromJSON(t, `{"Snapshot":{"Monitor":{"Enabled":true,"IntervalSeconds":30,"AlertThresholdMillis":250}}}`))
	details := make(map[string]string)
	for _, row := range model.Rows() {
		details[row.ID] = row.Detail
	}
	if details["setting:monitor"] != "enabled" || !strings.Contains(details["setting:interval"], "30 seconds") || !strings.Contains(details["setting:alert-threshold"], "250 ms") || !strings.Contains(details["setting:alert-threshold"], "above threshold") {
		t.Fatalf("settings details = %#v", details)
	}
}

func TestSettingsIntervalModalProducesBoundedConfigurationIntent(t *testing.T) {
	model := NewModel()
	model, _, _ = model.HandleKey("6")
	model, _, _ = model.HandleKey("i")
	if model.Focus != FocusModal || model.Modal == nil {
		t.Fatalf("interval binding did not focus modal: %#v", model)
	}
	for _, key := range []string{"6", "0", "0", "enter"} {
		var command *ipc.Command
		model, command, _ = model.HandleKey(key)
		if key == "enter" {
			if command == nil || command.Kind != ipc.CommandUpdateConfiguration || command.Config == nil || command.Config.MonitorIntervalSeconds == nil || *command.Config.MonitorIntervalSeconds != 600 {
				t.Fatalf("interval command = %#v", command)
			}
		}
	}
	if model.Modal != nil || model.Focus != FocusContent {
		t.Fatalf("modal did not close after valid interval: %#v", model)
	}

	model, _, _ = model.HandleKey("i")
	for _, key := range []string{"0", "enter"} {
		var command *ipc.Command
		model, command, _ = model.HandleKey(key)
		if command != nil {
			t.Fatal("accepted out-of-range interval")
		}
	}
	if model.Modal == nil || !strings.Contains(model.Notice, "between 1 and 86400") {
		t.Fatalf("invalid interval did not remain visible: %#v", model)
	}
}

func TestAlertThresholdSettingEmitsExplicitBoundedIntent(t *testing.T) {
	model := NewModel()
	model, _, _ = model.HandleKey("6")
	model, _, _ = model.HandleKey("t")
	if model.Modal == nil || model.Modal.Kind != ModalAlertThreshold || model.Focus != FocusModal {
		t.Fatalf("alert-threshold key did not open its modal: %#v", model)
	}
	for _, key := range []string{"2", "5", "0"} {
		var command *ipc.Command
		model, command, _ = model.HandleKey(key)
		if command != nil {
			t.Fatalf("digit unexpectedly emitted intent: %#v", command)
		}
	}
	model, command, _ := model.HandleKey("enter")
	if command == nil || command.Kind != ipc.CommandUpdateConfiguration || command.Config == nil || command.Config.AlertThresholdMillis == nil || *command.Config.AlertThresholdMillis != 250 || command.Config.MonitorIntervalSeconds != nil {
		t.Fatalf("alert-threshold intent = %#v", command)
	}
	if model.Modal != nil || model.Focus != FocusContent {
		t.Fatalf("successful threshold edit left modal open: %#v", model)
	}

	model, _, _ = model.HandleKey("t")
	for _, key := range []string{"6", "0", "0", "0", "0"} {
		model, _, _ = model.HandleKey(key)
	}
	model, command, _ = model.HandleKey("enter")
	if command == nil || command.Config.AlertThresholdMillis == nil || *command.Config.AlertThresholdMillis != 60000 {
		t.Fatalf("inclusive upper threshold intent = %#v", command)
	}

	model, _, _ = model.HandleKey("t")
	for _, key := range []string{"6", "0", "0", "0", "1"} {
		model, _, _ = model.HandleKey(key)
	}
	model, command, _ = model.HandleKey("enter")
	if command != nil || model.Modal == nil || !strings.Contains(model.Notice, "1 and 60000 milliseconds") {
		t.Fatalf("threshold above IPC bound was not rejected: model=%#v command=%#v", model, command)
	}
}

func TestJobsProgressAndCommandResultDoNotChangeFocus(t *testing.T) {
	model := NewModel()
	model.Tab = TabProxies
	model.Focus = FocusContent
	model = model.Apply(eventFromJSON(t, `{"Snapshot":{"Jobs":[{"ID":"j","Kind":"subscription","State":"running"}]}}`))
	if got := model.Progress(); !strings.Contains(got, "subscription: running") {
		t.Fatalf("progress = %q", got)
	}
	model = model.CommandQueued().CommandResult(false, errors.New("unavailable"))
	if model.Focus != FocusContent || model.Pending != 0 || !strings.Contains(model.Notice, "unavailable") {
		t.Fatalf("command completion stole focus or lost state: %#v", model)
	}
}

func eventFromJSON(t *testing.T, encoded string) ipc.Event {
	t.Helper()
	var event ipc.Event
	if err := json.Unmarshal([]byte(encoded), &event); err != nil {
		t.Fatalf("decode test event: %v", err)
	}
	return event
}

func TestURLTestGroupHasNoManualSelectionOrManagedProbe(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Groups":[{"ID":"test","Type":"URLTest","Selected":"node-a","Proxies":["node-a","node-b"]}]}}`))
	model.Tab = TabProxies
	model.Selection[TabProxies] = "proxy:test:node-b"
	for _, key := range []string{"enter", "r", "a"} {
		var command *ipc.Command
		model, command, _ = model.HandleKey(key)
		if command != nil {
			t.Fatalf("URLTest key %q dispatched unsupported command: %+v", key, command)
		}
	}
}

func TestBinaryModalPreservesExecutablePathCase(t *testing.T) {
	model := NewModel()
	model, _, _ = model.HandleKey("6")
	model, _, _ = model.HandleKey("b")
	if model.Modal == nil {
		t.Fatal("binary action did not open input modal")
	}
	for _, key := range "/tmp/Mihomo" {
		model, _, _ = model.HandleKey(string(key))
	}
	model, command, _ := model.HandleKey("enter")
	if command == nil || command.Config == nil || command.Config.Binary == nil || *command.Config.Binary != "/tmp/Mihomo" {
		t.Fatalf("binary path case lost: %+v", command)
	}
	model, _, _ = model.HandleKey("b")
	for _, key := range "relative" {
		model, _, _ = model.HandleKey(string(key))
	}
	model, command, _ = model.HandleKey("enter")
	if command != nil || model.Modal == nil {
		t.Fatalf("relative binary path was dispatched: %+v", command)
	}
}

func typeFormText(t *testing.T, model Model, value string) Model {
	t.Helper()
	for _, r := range value {
		var command *ipc.Command
		model, command, _ = model.HandleKey(string(r))
		if command != nil {
			t.Fatal("typing a form field sent a command before Save")
		}
	}
	return model
}

func fillModalField(t *testing.T, model Model, value string) (Model, *ipc.Command) {
	t.Helper()
	for _, char := range value {
		var command *ipc.Command
		model, command, _ = model.HandleKey(string(char))
		if command != nil {
			t.Fatalf("typing field unexpectedly emitted intent: %#v", command)
		}
	}
	model, command, _ := model.HandleKey("enter")
	return model, command
}
func replaceModalField(t *testing.T, model Model, value string) (Model, *ipc.Command) {
	t.Helper()
	for range []rune(model.Modal.Input) {
		model, _, _ = model.HandleKey("backspace")
	}
	return fillModalField(t, model, value)
}

func TestMonitorPolicyEditsKeepSubsecondUnitsAndAllowZero(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Monitor":{"TimeoutMillis":750,"CooldownSeconds":30,"JitterMillis":120}}}`))
	model, _, _ = model.HandleKey("6")
	model, _, _ = model.HandleKey("o")
	for _, key := range "750" {
		model, _, _ = model.HandleKey(string(key))
	}
	model, command, _ := model.HandleKey("enter")
	if command == nil || command.Kind != ipc.CommandUpdateConfiguration || command.Config == nil || command.Config.MonitorTimeoutMillis == nil || *command.Config.MonitorTimeoutMillis != 750 {
		t.Fatalf("timeout command = %#v", command)
	}
	model, _, _ = model.HandleKey("z")
	model, command, _ = model.HandleKey("0")
	if command != nil {
		t.Fatalf("cooldown input emitted early: %#v", command)
	}
	model, command, _ = model.HandleKey("enter")
	if command == nil || command.Config.MonitorCooldownSeconds == nil || *command.Config.MonitorCooldownSeconds != 0 {
		t.Fatalf("zero cooldown command = %#v", command)
	}
	model, _, _ = model.HandleKey("w")
	model, _, _ = model.HandleKey("0")
	model, command, _ = model.HandleKey("enter")
	if command == nil || command.Config.MonitorJitterMillis == nil || *command.Config.MonitorJitterMillis != 0 {
		t.Fatalf("zero jitter command = %#v", command)
	}
}

func TestDNSSetAndRouteEditsPreserveFullPolicyUntilSnapshot(t *testing.T) {
	model := dnsPolicyModel(t)
	model.Selection[TabSettings] = "dns:set:one"
	var oldDetail string
	for _, row := range model.Rows() {
		if row.ID == "dns:set:one" {
			oldDetail = row.Detail
		}
	}
	if oldDetail == "" {
		t.Fatal("selected DNS row missing")
	}
	model, _, _ = model.HandleKey("g")
	model, command, _ := model.HandleKey("enter")
	if command != nil || model.Modal.dns.step != dnsSetEndpoints {
		t.Fatalf("resolver edit did not advance: modal=%#v command=%#v", model.Modal, command)
	}
	model, _ = replaceModalField(t, model, "udp://8.8.8.8:53")
	model, command = replaceModalField(t, model, "yes")
	if command == nil || command.Kind != ipc.CommandSetDNSRouting || command.DNSRouting == nil {
		t.Fatalf("resolver edit command = %#v", command)
	}
	if len(command.DNSRouting.ResolverSets) != 2 || command.DNSRouting.ResolverSets[0].Endpoints[0] != "udp://8.8.8.8:53" || !command.DNSRouting.ResolverSets[0].DNSCrypt || command.DNSRouting.ResolverSets[1].ID != "two" || len(command.DNSRouting.Routes) != 2 {
		t.Fatalf("resolver replacement lost policy entries: %#v", command.DNSRouting)
	}
	for _, row := range model.Rows() {
		if row.ID == "dns:set:one" && (row.Detail != oldDetail || strings.Contains(row.Detail, "8.8.8.8")) {
			t.Fatalf("model changed before snapshot: %#v", row)
		}
	}

	model = dnsPolicyModel(t)
	model.Selection[TabSettings] = "dns:route:suffix:example.com"
	model, _, _ = model.HandleKey("g")
	model, command, _ = model.HandleKey("enter")
	if command != nil {
		t.Fatalf("matcher input emitted early: %#v", command)
	}
	model, _ = replaceModalField(t, model, "changed.example.com")
	model, command = replaceModalField(t, model, "two")
	if command == nil || len(command.DNSRouting.ResolverSets) != 2 || len(command.DNSRouting.Routes) != 2 {
		t.Fatalf("route replacement command = %#v", command)
	}
	if command.DNSRouting.Routes[0].Suffix != "changed.example.com" || command.DNSRouting.Routes[0].GeoSite != "" || command.DNSRouting.Routes[0].Resource != "" || command.DNSRouting.Routes[0].ResolverSet != "two" || command.DNSRouting.Routes[1].GeoSite != "geolocation-cn" {
		t.Fatalf("route edit did not preserve one matcher and other routes: %#v", command.DNSRouting.Routes)
	}
}

func TestDNSFormsAddRemoveCancelAndRetainSelection(t *testing.T) {
	model := dnsPolicyModel(t)
	model.Selection[TabSettings] = "dns:set:one"
	model, _, _ = model.HandleKey("g")
	event := model.snapshot
	event.Snapshot.Monitor.TimeoutMillis = 900
	model = model.Apply(event)
	if model.Modal == nil || model.Focus != FocusModal || model.Selection[TabSettings] != "dns:set:one" {
		t.Fatalf("unrelated snapshot changed editor focus: %#v", model)
	}
	model, command, _ := model.HandleKey("esc")
	if command != nil || model.Modal != nil || model.Focus != FocusContent || model.Selection[TabSettings] != "dns:set:one" {
		t.Fatalf("cancel changed DNS selection or emitted intent: %#v %#v", model, command)
	}
	model, _, _ = model.HandleKey("x")
	model, command, _ = model.HandleKey("esc")
	if command != nil || model.Selection[TabSettings] != "dns:set:one" {
		t.Fatalf("remove cancellation changed selection: %#v %#v", model, command)
	}
	model, _, _ = model.HandleKey("x")
	model, command, _ = model.HandleKey("enter")
	if command == nil || len(command.DNSRouting.ResolverSets) != 1 || command.DNSRouting.ResolverSets[0].ID != "two" || len(command.DNSRouting.Routes) != 2 {
		t.Fatalf("resolver removal replacement = %#v", command)
	}
	if _, ok := model.selectedRow(); !ok || model.Selection[TabSettings] != "dns:set:one" {
		t.Fatalf("selection changed before removal snapshot: %#v", model.Selection)
	}

	model = dnsPolicyModel(t)
	model.Selection[TabSettings] = "dns:route:suffix:example.com"
	model, _, _ = model.HandleKey("x")
	model, command, _ = model.HandleKey("enter")
	if command == nil || len(command.DNSRouting.Routes) != 1 || command.DNSRouting.Routes[0].GeoSite != "geolocation-cn" || len(command.DNSRouting.ResolverSets) != 2 {
		t.Fatalf("route removal replacement = %#v", command)
	}

	model = dnsPolicyModel(t)
	model, _, _ = model.HandleKey("l")
	for range []rune(model.Modal.Input) {
		model, _, _ = model.HandleKey("backspace")
	}
	for _, key := range "127.0.0.1:5353" {
		model, _, _ = model.HandleKey(string(key))
	}
	model, command, _ = model.HandleKey("enter")
	if command == nil || command.Config == nil || command.Config.DNSListen == nil || *command.Config.DNSListen != "127.0.0.1:5353" || model.snapshot.Snapshot.DNS.Listen != "127.0.0.1:1053" {
		t.Fatalf("DNS listener intent or immutable snapshot = %#v, %#v", command, model.snapshot.Snapshot.DNS)
	}

	model = dnsPolicyModel(t)
	model, _, _ = model.HandleKey("v")
	model, _ = fillModalField(t, model, "newset")
	model, _ = fillModalField(t, model, "udp://127.0.0.1:5353")
	model, command = replaceModalField(t, model, "yes")
	if command == nil || len(command.DNSRouting.ResolverSets) != 3 || command.DNSRouting.ResolverSets[2].ID != "newset" || !command.DNSRouting.ResolverSets[2].DNSCrypt || len(command.DNSRouting.Routes) != 2 {
		t.Fatalf("resolver create replacement = %#v", command)
	}

	model = dnsPolicyModel(t)
	model, _, _ = model.HandleKey("a")
	model, _, _ = model.HandleKey("enter")
	model, _ = fillModalField(t, model, "new.example.com")
	model, command = fillModalField(t, model, "one")
	if command == nil || len(command.DNSRouting.Routes) != 3 || command.DNSRouting.Routes[2].Suffix != "new.example.com" || command.DNSRouting.Routes[2].ResolverSet != "one" || command.DNSRouting.Routes[2].GeoSite != "" || command.DNSRouting.Routes[2].Resource != "" {
		t.Fatalf("route create replacement = %#v", command)
	}
}

func TestDNSCredentialsAreRejectedWithoutLeakingURL(t *testing.T) {
	secretURL := "https://user:secret@resolver.example/dns-query"
	model := dnsPolicyModel(t)
	model, _, _ = model.HandleKey("v")
	model, _ = fillModalField(t, model, "private-set")
	model, _ = fillModalField(t, model, secretURL)
	if model.Modal == nil || strings.Contains(model.Notice, secretURL) || strings.Contains(model.Notice, "secret") {
		t.Fatalf("credential endpoint was accepted or leaked: modal=%#v notice=%q", model.Modal, model.Notice)
	}
	command := ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: &ipc.DNSRoutingEdit{ResolverSets: []ipc.DNSResolverSet{{ID: "private-set", Endpoints: []string{secretURL}}}}}
	if !commandHasPrivateSource(command) {
		t.Fatal("DNS routing intent was not treated as private")
	}
	failed := NewModel().CommandQueued().CommandResult(false, errors.New("invalid endpoint "+secretURL), commandHasPrivateSource(command))
	if strings.Contains(failed.Notice, secretURL) || strings.Contains(failed.Notice, "secret") {
		t.Fatalf("private command error leaked URL: %q", failed.Notice)
	}
}

func TestSettingsRenderCurrentMonitorAndDNSPolicy(t *testing.T) {
	model := dnsPolicyModel(t)
	event := model.snapshot
	monitor := event.Snapshot.Monitor
	monitor.Enabled, monitor.TestURL = true, "https://monitor.example/204"
	monitor.IntervalSeconds, monitor.TimeoutMillis, monitor.Concurrency = 60, 750, 3
	monitor.ThresholdMillis, monitor.AlertThresholdMillis, monitor.ConsecutiveBadSamples = 800, 250, 2
	monitor.MinImprovementMillis, monitor.CooldownSeconds, monitor.JitterMillis = 100, 0, 200
	event.Snapshot.Monitor = monitor
	model = model.Apply(event)
	details := make(map[string]string)
	for _, row := range model.Rows() {
		details[row.ID] = row.Title + " " + row.Detail
	}
	for id, value := range map[string]string{
		"setting:interval":             "60 seconds",
		"setting:monitor:timeout":      "750 ms",
		"setting:monitor:concurrency":  "3",
		"setting:monitor:threshold":    "800 ms",
		"setting:alert-threshold":      "250 ms",
		"setting:monitor:bad-samples":  "2",
		"setting:monitor:improvement":  "100 ms",
		"setting:monitor:cooldown":     "0 seconds",
		"setting:monitor:jitter":       "200 ms",
		"setting:dns-listen":           "127.0.0.1:1053",
		"dns:route:suffix:example.com": "resolver one",
	} {
		if !strings.Contains(details[id], value) {
			t.Errorf("row %s = %q, want %q", id, details[id], value)
		}
	}
	for _, detail := range details {
		if strings.Contains(detail, "https://monitor.example/204") || strings.Contains(detail, "udp://1.1.1.1:53") {
			t.Fatal("Settings detail exposed monitored URL or resolver endpoint")
		}
	}
	help := strings.Join(model.Help(), ";")
	for _, binding := range []string{"u set monitor test URL", "o set monitor timeout in milliseconds", "z set monitor cooldown in seconds", "w set monitor jitter in milliseconds", "v add DNS resolver set", "a add DNS route", "x remove selected DNS entry"} {
		if !strings.Contains(help, binding) {
			t.Errorf("settings help missing %q: %s", binding, help)
		}
	}
}

func TestSettingsWarnsAboutPlainHTTPProbe(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Monitor":{"TestURL":"http://cp.cloudflare.com/generate_204"}}}`)).selectTab(TabSettings)
	for _, row := range model.Rows() {
		if row.ID == "setting:monitor:test-url" {
			if !strings.Contains(row.Detail, "intercepted") {
				t.Fatalf("plain HTTP risk is hidden: %q", row.Detail)
			}
			return
		}
	}
	t.Fatal("monitor URL setting is missing")
}
func dnsPolicyModel(t *testing.T) Model {
	t.Helper()
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"DNS":{"Listen":"127.0.0.1:1053","ResolverSets":[{"ID":"one","Endpoints":["udp://1.1.1.1:53"],"DNSCrypt":false},{"ID":"two","Endpoints":["udp://9.9.9.9:53"],"DNSCrypt":true}],"Routes":[{"Suffix":"example.com","ResolverSet":"one"},{"GeoSite":"geolocation-cn","ResolverSet":"two"}]}}}`))
	model, _, _ = model.HandleKey("6")
	return model
}
