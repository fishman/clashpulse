package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fishman/notmutt/lib/tui/form"
	"github.com/gdamore/tcell/v3"
)

func TestEmbeddedKeymapContainsEveryViewAndActionHelp(t *testing.T) {
	keymap, err := DefaultKeymap()
	if err != nil {
		t.Fatal(err)
	}
	for _, tab := range tabs {
		if len(keymap.Help(tab)) == 0 {
			t.Errorf("no embedded help for tab %q", tab)
		}
	}
	for _, tab := range []Tab{TabOverview, TabProxies, TabSubscriptions, TabFilters, TabResources, TabSettings} {
		if action, ok := keymap.Action(tab, map[Tab]string{
			TabOverview: "1", TabProxies: "2", TabSubscriptions: "3",
			TabFilters: "4", TabResources: "5", TabSettings: "6",
		}[tab]); !ok || action == "" {
			t.Errorf("tab %q has no numeric binding", tab)
		}
	}
}

func TestNotmuttStyleKeymapPreservesUppercaseAndShownHints(t *testing.T) {
	keys, err := NewKeymap([]byte(`[schemes.default.global]
"?" = { fun = "toggle_help", desc = "Show help", show = true, inherit = true }
[schemes.default.proxies]
"J" = { fun = "manual_probe", desc = "Probe now", show = true }
"j" = { fun = "move_down", desc = "Move down" }
`))
	if err != nil {
		t.Fatal(err)
	}
	if action, ok := keys.Action(TabProxies, "J"); !ok || action != "manual_probe" {
		t.Fatalf("uppercase action = %q, %t", action, ok)
	}
	if action, ok := keys.Action(TabProxies, "j"); !ok || action != "move_down" {
		t.Fatalf("lowercase action = %q, %t", action, ok)
	}
	if !contains(keys.Help(TabProxies), "J Probe now") || !contains(keys.Hints(TabProxies), "J Probe now") || contains(keys.Hints(TabProxies), "j Move down") {
		t.Fatalf("help/hints did not honor show flag: %v / %v", keys.Help(TabProxies), keys.Hints(TabProxies))
	}
}

func TestKeymapRejectsDuplicateAndUnknownBindings(t *testing.T) {
	for _, source := range []string{
		"[schemes.default.proxies]\n\"z\" = { fun = \"quit\", desc = \"quit\" }\n\"z\" = { fun = \"quit\", desc = \"quit\" }\n",
		"[schemes.default.unknown]\n\"z\" = { fun = \"quit\", desc = \"quit\" }\n",
		"[schemes.default.proxies]\n\"z\" = { fun = \"run_shell\", desc = \"run\" }\n",
		"[[binding]]\nkey='z'\naction='quit'\nhelp='quit'\n",
	} {
		if _, err := NewKeymap([]byte(source)); err == nil {
			t.Errorf("accepted invalid keymap %q", source)
		}
	}
}

func TestEventKeyNamesMatchDeclarativeBindings(t *testing.T) {
	for _, test := range []struct {
		event *tcell.EventKey
		want  string
	}{
		{tcell.NewEventKey(tcell.KeyRune, "J", tcell.ModNone), "J"},
		{tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone), "ctrl+c"},
		{tcell.NewEventKey(tcell.KeyBacktab, "", tcell.ModNone), "shift+tab"},
		{tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone), "enter"},
		{tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone), "space"},
		{tcell.NewEventKey(tcell.KeyCtrlS, "", tcell.ModNone), "ctrl+s"},
		{tcell.NewEventKey(tcell.KeyF2, "", tcell.ModNone), "f2"},
	} {
		if got := eventKeyName(test.event); got != test.want {
			t.Errorf("key name = %q, want %q", got, test.want)
		}
	}
}

func TestPageKeysDispatchInLogAndHelpContexts(t *testing.T) {
	keys, err := DefaultKeymap()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key    tcell.Key
		tab    Tab
		action string
	}{
		{tcell.KeyPgUp, Tab("log"), "log_page_older"},
		{tcell.KeyPgDn, Tab("help"), "help_page_down"},
	} {
		name := eventKeyName(tcell.NewEventKey(tc.key, "", tcell.ModNone))
		if got, ok := keys.Action(tc.tab, name); !ok || got != tc.action {
			t.Fatalf("%s key %q dispatched %q, want %q", tc.tab, name, got, tc.action)
		}
	}
}

func TestFormKeymapExposesToggleAndSave(t *testing.T) {
	keys, err := DefaultKeymap()
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []struct{ key, action string }{{"space", "form_toggle"}, {"ctrl+s", "form_save"}, {"f2", "form_save"}} {
		if got, ok := keys.Action(Tab("form"), binding.key); !ok || got != binding.action {
			t.Fatalf("form key %q action = %q, %t", binding.key, got, ok)
		}
	}
	if !contains(keys.Help(Tab("form")), "space toggle field") {
		t.Fatal("form help omitted toggle binding")
	}
}

func TestFormSpaceEventTogglesInsteadOfTyping(t *testing.T) {
	editor, err := form.New([]form.Field{{ID: "enabled", Label: "Enabled", Kind: form.Toggle, Value: "false"}})
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel()
	model.Modal = &Modal{Kind: ModalSubscription, Form: editor}
	key := keyForModelEvent(model, tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	model, _, _ = model.HandleKey(key)
	if key != "space" || len(model.Modal.Form.Changes()) != 1 || model.Modal.Form.Changes()[0].Value != "true" {
		t.Fatal("space was typed instead of toggling the selected field")
	}
}

func TestRunExplainsUnavailableIPCService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := Run(ctx, t.TempDir()+"/missing.sock")
	if err == nil || !strings.Contains(err.Error(), "cannot connect to the ClashPulse IPC service") {
		t.Fatalf("Run error = %v, want a clear unavailable-service error", err)
	}
}
