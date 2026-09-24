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
