package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/localize"
)

func TestMain(m *testing.M) {
	localize.SetLanguage("en")
	os.Exit(m.Run())
}

func TestNavigationLabelsAcrossLocales(t *testing.T) {
	for _, tc := range []struct{ locale, labels string }{
		{"en", "Overview|Proxies|Subscriptions|Filter Lists|Data Resources|Settings"},
		{"zh-CN", "概览|代理|订阅|过滤列表|数据资源|设置"},
	} {
		localize.SetLanguage(tc.locale)
		labels := make([]string, len(tabs))
		for i, tab := range tabs {
			labels[i] = viewTitle(tab)
		}
		if got := strings.Join(labels, "|"); got != tc.labels {
			t.Errorf("%s TUI tabs = %q, want %q", tc.locale, got, tc.labels)
		}
		model := NewModel()
		model.Tab = TabSettings
		if row := mockRender(t, model, 100, 9)[0]; !strings.Contains(row, labels[5]) {
			t.Errorf("%s rendered tabs missing selected label: %q", tc.locale, row)
		}
	}
	localize.SetLanguage("en")
}
