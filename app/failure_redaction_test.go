package app

import (
	"strings"
	"testing"
)

func TestPublicFailureLabelNeverContainsSourceURL(t *testing.T) {
	raw := "failed to fetch https://user:secret@provider.invalid/profile?token=private"
	for _, kind := range []string{"subscription", "resource"} {
		label := publicFailureLabel(kind, raw)
		if label == "" || strings.Contains(label, "secret") || strings.Contains(label, "private") || strings.Contains(label, "provider.invalid") {
			t.Fatalf("%s failure leaked a private source: %q", kind, label)
		}
	}
	if label := publicFailureLabel("resource", ""); label != "" {
		t.Fatalf("empty failure became %q", label)
	}
}
