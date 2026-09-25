package tui

import (
	"strings"
	"testing"
)

func TestPrivateSourceModalNeverDisplaysEnteredURL(t *testing.T) {
	secret := "https://provider.invalid/profile?token=private"
	for _, item := range []*Modal{
		{Kind: ModalResource, Input: secret, managed: managedForm{step: resourceFieldURL}},
		{Kind: ModalDNSResolver, Input: secret, dns: dnsForm{step: dnsSetEndpoints}},
	} {
		text := modalDisplayInput(item)
		if strings.Contains(text, "private") || strings.Contains(text, "provider.invalid") {
			t.Fatal("private URL shown in modal")
		}
	}
	if got := modalDisplayInput(&Modal{Kind: ModalBinary, Input: "system"}); got != "system" {
		t.Fatalf("nonsecret binary selection hidden: %q", got)
	}
}
