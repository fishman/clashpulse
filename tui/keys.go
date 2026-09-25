package tui

import (
	_ "embed"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	sharedkeymap "github.com/fishman/notmutt/lib/tui/keymap"
	"github.com/gdamore/tcell/v3"
)

//go:embed keys.toml
var defaultKeyData []byte

type keyFile struct {
	Schemes map[string]map[string]map[string]sharedkeymap.Binding `toml:"schemes"`
}

type Keymap struct{ table sharedkeymap.Table }

func DefaultKeymap() (Keymap, error) { return NewKeymap(defaultKeyData) }

// NewKeymap accepts one Notmutt-style context scheme; all actions remain client-owned.
func NewKeymap(data []byte) (Keymap, error) {
	var file keyFile
	meta, err := toml.Decode(string(data), &file)
	if err != nil {
		return Keymap{}, fmt.Errorf("decode TUI keybindings: %w", err)
	}
	if len(meta.Undecoded()) != 0 {
		return Keymap{}, fmt.Errorf("unknown TUI keybinding section %q", meta.Undecoded()[0])
	}
	if len(file.Schemes) != 1 || file.Schemes["default"] == nil {
		return Keymap{}, fmt.Errorf("TUI requires one default keybinding scheme")
	}
	scheme := file.Schemes["default"]
	parents := make(map[string]string, len(scheme))
	for context, bindings := range scheme {
		switch context {
		case "global", "form", "dialog", "log", "help":
		default:
			if !knownTab(Tab(context)) {
				return Keymap{}, fmt.Errorf("unknown TUI keybinding context %q", context)
			}
			if scheme["global"] != nil {
				parents[context] = "global"
			}
		}
		for key, binding := range bindings {
			if key == "" || normalizeKey(key) != key || !knownAction(binding.Fun) {
				return Keymap{}, fmt.Errorf("invalid TUI binding %q in context %q", key, context)
			}
		}
	}
	compiled, err := sharedkeymap.Compile(scheme, parents)
	if err != nil {
		return Keymap{}, err
	}
	return Keymap{table: compiled}, nil
}

func (k Keymap) Action(tab Tab, key string) (string, bool) {
	return k.table.Action(string(tab), normalizeKey(key))
}

func (k Keymap) Help(tab Tab) []string                { return formatBindings(k.table.Entries(string(tab))) }
func (k Keymap) Hints(tab Tab) []string               { return formatBindings(k.table.Hints(string(tab))) }
func (k Keymap) KeyFor(tab Tab, action string) string { return k.table.KeyFor(string(tab), action) }

func formatBindings(entries []sharedkeymap.Entry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		description := entry.Desc
		if description == "" {
			description = entry.Fun
		}
		labels = append(labels, entry.Key+" "+description)
	}
	return labels
}

func normalizeKey(key string) string {
	if utf8.RuneCountInString(key) == 1 && key != " " {
		return key
	}
	return strings.ToLower(strings.TrimSpace(key))
}

func knownAction(action string) bool {
	switch action {
	case "quit", "next_tab", "prev_tab", "move_up", "move_down", "activate", "cancel_modal", "toggle_log", "log_older", "log_newer", "log_page_older", "log_page_newer", "log_oldest", "log_newest", "log_close", "toggle_help", "help_up", "help_down", "help_page_up", "help_page_down", "help_home", "help_end", "help_close",
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
		key := event.Str()
		if event.Str() == " " {
			return "space"
		}
		if event.Modifiers()&tcell.ModCtrl != 0 && key != "" {
			return "ctrl+" + strings.ToLower(key)
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
	case tcell.KeyPgDn:
		return "pgdown"
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
