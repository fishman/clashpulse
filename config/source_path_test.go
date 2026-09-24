package config

import (
	"path/filepath"
	"testing"
)

func TestResourceSourceRequiresAbsoluteLocalPath(t *testing.T) {
	if err := validateSourceOrPath("relative/rules.yaml", true); err == nil {
		t.Fatal("relative resource path accepted")
	}
	if path := filepath.Join(t.TempDir(), "rules.yaml"); validateSourceOrPath(path, true) != nil {
		t.Fatalf("absolute local resource path rejected: %q", path)
	}
}
