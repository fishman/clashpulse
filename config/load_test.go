package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsUnknownKeyWithFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\nbogus = true\n")

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "config.toml") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsDanglingFilterResource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "filters.toml", "[[filter]]\nid = \"ads\"\nresource = \"missing\"\n")

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v", err)
	}
}
