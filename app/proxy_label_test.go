package app

import (
	"strings"
	"testing"
)

func TestProxyDisplayLabelRejectsCredentialShapedName(t *testing.T) {
	if got := proxyDisplayLabel("node-a", "Proxy", 1); got != "node-a" {
		t.Fatalf("ordinary name = %q", got)
	}
	secret := "https://user:password@example.com/node"
	if got := proxyDisplayLabel(secret, "Proxy", 2); got != "Proxy 2" || strings.Contains(got, "password") {
		t.Fatalf("credential-shaped name leaked: %q", got)
	}
}

func TestProxyDisplayLabelKeepsProfileNodeName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"香港1｜高速", "香港1｜高速"},
		{"美国1｜AI通用", "美国1｜AI通用"},
		{"套餐到期：长期有效", "套餐到期：长期有效"},
		{"剩余流量：881.94 GB", "剩余流量：881.94 GB"},
		{"网址：https://node.example.com", "网址：https://[redacted]"},
		{"网址：a.example.net", "网址：[redacted]"},
	} {
		if got := proxyDisplayLabel(tc.name, "Proxy", 14); got != tc.want {
			t.Fatalf("name %q became %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUniqueLabelNumbersRepeatedNames(t *testing.T) {
	seen := map[string]int{}
	if got := uniqueLabel("香港1", seen); got != "香港1" {
		t.Fatalf("first = %q", got)
	}
	if got := uniqueLabel("香港1", seen); got != "香港1 (2)" {
		t.Fatalf("second = %q", got)
	}
	if got := uniqueLabel("韩国1", seen); got != "韩国1" {
		t.Fatalf("distinct name = %q", got)
	}
}
