package tui

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gdamore/tcell/v3"
)

//go:embed keys.toml
var defaultKeyData []byte

type keyFile struct {
	Bindings []keyBinding `toml:"binding"`
}

type keyBinding struct {
	Tab    string `toml:"tab"`
	Key    string `toml:"key"`
	Action string `toml:"action"`
	Help   string `toml:"help"`
}

// Keymap contains the parsed, immutable keybinding table used by both key
// dispatch and the rendered help line.
type Keymap struct {
	bindings []keyBinding
	lookup   map[string]string
}

// DefaultKeymap parses the embedded declarative TOML bindings.
func DefaultKeymap() (Keymap, error) {
	return NewKeymap(defaultKeyData)
}

// NewKeymap parses TOML keybindings. An empty tab applies in every view.
func NewKeymap(data []byte) (Keymap, error) {
	var file keyFile
	if _, err := toml.Decode(string(data), &file); err != nil {
		return Keymap{}, fmt.Errorf("decode TUI keybindings: %w", err)
	}

	keymap := Keymap{
		bindings: append([]keyBinding(nil), file.Bindings...),
		lookup:   make(map[string]string, len(file.Bindings)),
	}
	for i := range keymap.bindings {
		binding := &keymap.bindings[i]
		binding.Key = normalizeKey(binding.Key)
		if binding.Key == "" || binding.Action == "" || binding.Help == "" {
			return Keymap{}, fmt.Errorf("TUI keybinding %d requires key, action, and help", i+1)
		}
		if binding.Tab != "" && binding.Tab != "form" && !knownTab(Tab(binding.Tab)) {
			return Keymap{}, fmt.Errorf("TUI keybinding %d has unknown tab %q", i+1, binding.Tab)
		}
		if !knownAction(binding.Action) {
			return Keymap{}, fmt.Errorf("TUI keybinding %d has unknown action %q", i+1, binding.Action)
		}
		lookupKey := binding.Tab + "\x00" + binding.Key
		if _, exists := keymap.lookup[lookupKey]; exists {
			return Keymap{}, fmt.Errorf("duplicate TUI keybinding %q on tab %q", binding.Key, binding.Tab)
		}
		keymap.lookup[lookupKey] = binding.Action
	}
	return keymap, nil
}

// Action resolves a key for the current tab, preferring a view-specific
// binding to a global binding.
func (k Keymap) Action(tab Tab, key string) (string, bool) {
	key = normalizeKey(key)
	if action, ok := k.lookup[string(tab)+"\x00"+key]; ok {
		return action, true
	}
	action, ok := k.lookup["\x00"+key]
	return action, ok
}

// Help returns the human-readable help entries directly from the bindings
// shown for the given view.
func (k Keymap) Help(tab Tab) []string {
	entries := make([]string, 0, len(k.bindings))
	for _, binding := range k.bindings {
		if tab == "form" && binding.Tab != "form" {
			continue
		}
		if binding.Tab != "" && binding.Tab != string(tab) {
			continue
		}
		entries = append(entries, binding.Key+" "+binding.Help)
	}
	return entries
}

func normalizeKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func knownAction(action string) bool {
	switch action {
	case "quit", "next_tab", "prev_tab", "move_up", "move_down", "activate", "cancel_modal",
		"tab_overview", "tab_proxies", "tab_subscriptions", "tab_filters", "tab_resources", "tab_settings",
		"start", "stop", "reload_configuration", "manual_probe", "toggle_automation",
		"refresh_subscription", "activate_subscription", "new_subscription", "edit_subscription", "delete_subscription", "refresh_filter", "refresh_resource", "new_filter", "edit_filter", "new_resource", "edit_resource",
		"system_proxy_enable", "system_proxy_disable", "monitor_enable", "monitor_disable", "edit_monitor_interval", "edit_alert_threshold", "edit_binary",
		"edit_monitor_url", "edit_monitor_timeout", "edit_monitor_concurrency", "edit_monitor_threshold", "edit_monitor_bad_samples", "edit_monitor_improvement", "edit_monitor_cooldown", "edit_monitor_jitter",
		"edit_dns_listen", "new_dns_resolver", "new_dns_route", "edit_dns_entry", "delete_dns_entry",
		"form_up", "form_down", "form_toggle", "form_edit", "form_left", "form_right", "form_backspace", "form_save", "form_cancel":
		return true
	default:
		return false
	}
}

func eventKeyName(event *tcell.EventKey) string {
	if event == nil {
		return ""
	}
	if event.Key() == tcell.KeyRune {
		key := strings.ToLower(event.Str())
		if event.Str() == " " {
			return "space"
		}
		if event.Modifiers()&tcell.ModCtrl != 0 && key != "" {
			return "ctrl+" + key
		}
		return key
	}
	switch event.Key() {
	case tcell.KeyEnter:
		return "enter"
	case tcell.KeyEsc:
		return "esc"
	case tcell.KeyTab:
		return "tab"
	case tcell.KeyBacktab:
		return "shift+tab"
	case tcell.KeyUp:
		return "up"
	case tcell.KeyDown:
		return "down"
	case tcell.KeyLeft:
		return "left"
	case tcell.KeyRight:
		return "right"
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return "backspace"
	case tcell.KeyCtrlC:
		return "ctrl+c"
	case tcell.KeyCtrlS:
		return "ctrl+s"
	default:
		if name, ok := tcell.KeyNames[event.Key()]; ok {
			return normalizeKey(name)
		}
		return ""
	}
}
