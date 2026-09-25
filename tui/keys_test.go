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

func TestKeymapRejectsDuplicateAndUnknownBindings(t *testing.T) {
	for _, source := range []string{
		"[[binding]]\nkey='z'\naction='quit'\nhelp='quit'\n[[binding]]\nkey='z'\naction='quit'\nhelp='quit'\n",
		"[[binding]]\ntab='unknown'\nkey='z'\naction='quit'\nhelp='quit'\n",
		"[[binding]]\nkey='z'\naction='run_shell'\nhelp='run'\n",
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
		{tcell.NewEventKey(tcell.KeyRune, "J", tcell.ModNone), "j"},
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
