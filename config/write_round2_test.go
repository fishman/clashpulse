package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesPrivateTargetDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := Write(path, []byte("[mihomo]\nbinary = \"system\"\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o", got)
	}
}
