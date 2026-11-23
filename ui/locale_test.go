package ui

import (
	"context"
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/localize"
)

func TestMain(m *testing.M) {
	localize.SetLanguage("en")
	os.Exit(m.Run())
}

func TestNavigationLabelsAcrossLocales(t *testing.T) {
	t.Cleanup(func() { localize.SetLanguage("en") })
	for _, tc := range []struct{ locale, labels string }{
		{"en", "Overview|Proxies|Subscriptions|Filter Lists|Data Resources|Settings"},
		{"zh-CN", "概览|代理|订阅|过滤列表|数据资源|设置"},
	} {
		localize.SetLanguage(tc.locale)
		a := test.NewApp()
		view := newDesktopUI(context.Background(), "", a.NewWindow("ClashPulse"))
		if got := strings.Join(view.sidebar.labels(), "|"); got != tc.labels {
			t.Errorf("%s GUI views = %q, want %q", tc.locale, got, tc.labels)
		}
		view.showView("Settings")
		if got := view.topBar.title.Text; got != strings.Split(tc.labels, "|")[5] {
			t.Errorf("%s GUI selection = %q", tc.locale, got)
		}
		a.Quit()
	}
}
