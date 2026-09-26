package core

import (
	"errors"
	"strings"
	"testing"
)

func TestPublicActivationDropsPrivateCause(t *testing.T) {
	private := errors.New("password=private https://feed.invalid/?token=private")
	wrapped := WrapActivation(ActivationControllerReadiness, private)
	public, ok := PublicActivation(wrapped)
	if !ok || public.Stage != ActivationControllerReadiness ||
		strings.Contains(public.Error(), "private") || strings.Contains(public.Error(), "://") ||
		!errors.Is(wrapped, private) || errors.Is(public, private) {
		t.Fatalf("public stage lost or private cause escaped: %v, %v", public, ok)
	}
}

func TestUnknownActivationStageDoesNotEchoInput(t *testing.T) {
	if got := WrapActivation(ActivationStage("password=private"), errors.New("token=private")).Error(); got != "activation failed" {
		t.Fatalf("unknown stage escaped: %q", got)
	}
}

func TestResourceActivationStageUsesOnlyValidatedID(t *testing.T) {
	valid, _ := PublicActivation(WrapActivationResource(ActivationBinary, "geosite", errors.New("password=private")))
	if valid.ResourceID != "geosite" || !strings.Contains(valid.Error(), "geosite") || strings.Contains(valid.Error(), "private") {
		t.Fatalf("valid resource identity disappeared or leaked cause: %q", valid)
	}
	invalid, _ := PublicActivation(WrapActivationResource(ActivationResources, "https://feed.invalid/?token=private", errors.New("private")))
	if invalid.ResourceID != "" || strings.Contains(invalid.Error(), "private") || strings.Contains(invalid.Error(), "://") {
		t.Fatalf("unsafe resource identity escaped: %q", invalid)
	}
}
