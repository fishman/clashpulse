package localize

import (
	"embed"
	"os"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/jeandeaual/go-locale"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locale/*.toml
var catalogs embed.FS

var (
	bundle  = goi18n.NewBundle(language.English)
	mu      sync.RWMutex
	current = goi18n.NewLocalizer(bundle, "en")
)

func init() {
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	entries, err := catalogs.ReadDir("locale")
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		if _, err := bundle.LoadMessageFileFS(catalogs, "locale/"+entry.Name()); err != nil {
			panic(err)
		}
	}
	SetLanguage("auto")
}

// ResolveLanguage selects Simplified Chinese only for a Mainland locale.
func ResolveLanguage(value string) string {
	if value == "" || value == "auto" {
		if detected, err := locale.GetLocale(); err == nil {
			value = detected
		}
		if value == "" || value == "auto" {
			for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
				if value = os.Getenv(name); value != "" {
					break
				}
			}
		}
	}
	value, _, _ = strings.Cut(value, ".")
	value, _, _ = strings.Cut(value, "@")
	value = strings.ReplaceAll(value, "_", "-")
	tag, err := language.Parse(value)
	if err != nil {
		return "en"
	}
	base, script, region := tag.Raw()
	if base.String() == "zh" && region.String() == "CN" && (script.String() == "Hans" || script.String() == "Zzzz") {
		return "zh-CN"
	}
	return "en"
}

func SetLanguage(value string) {
	selected := goi18n.NewLocalizer(bundle, ResolveLanguage(value), "en")
	mu.Lock()
	current = selected
	mu.Unlock()
}

// T returns the selected message, or the English message ID when absent.
func T(id string) string {
	mu.RLock()
	selected := current
	mu.RUnlock()
	value, err := selected.Localize(&goi18n.LocalizeConfig{MessageID: id})
	if err != nil || value == "" {
		return id
	}
	return value
}

// Code localizes a fixed IPC display code without changing its transport value.
func Code(namespace, value string) string {
	if value == "" {
		return ""
	}
	id := namespace + "." + value
	if translated := T(id); translated != id {
		return translated
	}
	return value
}
