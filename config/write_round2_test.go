package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteCreatesPrivateTargetDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX mode bits; the user profile's ACL keeps configuration private")
	}
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
