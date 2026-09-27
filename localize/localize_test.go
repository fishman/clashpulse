package localize

import "testing"

func TestResolveLanguageUsesMainlandTagOnly(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"zh_CN.UTF-8", "zh-CN"},
		{"zh-Hans-CN", "zh-CN"},
		{"zh-Hant-CN", "en"},
		{"en_US.UTF-8", "en"},
		{"zh_TW.UTF-8", "en"},
		{"zh_HK", "en"},
		{"C", "en"},
		{"../../zh_CN", "en"},
	} {
		if got := ResolveLanguage(test.input); got != test.want {
			t.Errorf("ResolveLanguage(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestMainlandCatalogAndEnglishFallback(t *testing.T) {
	t.Cleanup(func() { SetLanguage("en") })
	SetLanguage("zh-CN")
	if got := T("Overview"); got != "\u6982\u89c8" {
		t.Fatalf("Chinese Overview = %q", got)
	}
	if got := T("override.dns.listen"); got != "\u4ec5\u5728\u672c\u673a\u76d1\u542c DNS" {
		t.Fatalf("DNS description = %q", got)
	}
	if got := T("untranslated text"); got != "untranslated text" {
		t.Fatalf("unknown key = %q", got)
	}
	SetLanguage("zh-TW")
	if got := T("Overview"); got != "Overview" {
		t.Fatalf("non-Mainland locale = %q", got)
	}
	SetLanguage("en")
	if got := T("override.dns.listen"); got != "loopback DNS listener" {
		t.Fatalf("English description = %q", got)
	}
}

func TestMainlandFixedDisplayCodesAndUnknownFallback(t *testing.T) {
	SetLanguage("zh-CN")
	t.Cleanup(func() { SetLanguage("en") })
	for _, test := range []struct{ prefix, value, want string }{
		{"operation", "refresh_subscription", "\u5237\u65b0\u8ba2\u9605"},
		{"job.state", "running", "\u8fdb\u884c\u4e2d"},
		{"probe.outcome", "timeout", "\u8d85\u65f6"},
		{"diagnostic.severity", "error", "\u9519\u8bef"},
		{"operation", "user-private", "user-private"},
	} {
		if got := Code(test.prefix, test.value); got != test.want {
			t.Errorf("Code(%q,%q) = %q, want %q", test.prefix, test.value, got, test.want)
		}
	}
}
