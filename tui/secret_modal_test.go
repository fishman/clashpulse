package tui

import (
	"strings"
	"testing"
)

func TestPrivateSourceModalNeverDisplaysEnteredURL(t *testing.T) {
	secret := "https://provider.invalid/profile?token=private"
	for _, modal := range []*Modal{
		{Kind: ModalSubscription, Input: secret, subscription: subscriptionForm{step: subscriptionFieldURL}},
		{Kind: ModalResource, Input: secret, managed: managedForm{step: resourceFieldURL}},
	} {
		text := modalDisplayInput(modal)
		if strings.Contains(text, "private") || strings.Contains(text, "provider.invalid") {
			t.Fatalf("private URL shown in %s modal: %q", modal.Kind, text)
		}
	}
	ordinary := &Modal{Kind: ModalSubscription, Input: "Daily", subscription: subscriptionForm{step: subscriptionFieldName}}
	if got := modalDisplayInput(ordinary); got != "Daily" {
		t.Fatalf("nonsecret field hidden: %q", got)
	}
}
