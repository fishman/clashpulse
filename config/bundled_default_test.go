package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/internal/bundled"
)

func withBundledSHA(t *testing.T, sum string) {
	t.Helper()
	old := bundled.SHA256
	bundled.SHA256 = sum
	t.Cleanup(func() { bundled.SHA256 = old })
}

func TestSeededConfigTakesBuildBinaryDefault(t *testing.T) {
	for _, tc := range []struct{ sum, want string }{{"", "system"}, {strings.Repeat("a", 64), "bundled"}} {
		withBundledSHA(t, tc.sum)
		dir := t.TempDir()
		if err := Seed(dir); err != nil {
			t.Fatal(err)
		}
		snapshot, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Mihomo.Binary != tc.want {
			t.Fatalf("bundle sum %q: binary %q, want %q", tc.sum, snapshot.Mihomo.Binary, tc.want)
		}
	}
}

func TestExplicitSystemBinaryOverridesBundledDefault(t *testing.T) {
	withBundledSHA(t, strings.Repeat("a", 64))
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	seeded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(seeded), "[mihomo]\n", "[mihomo]\nbinary = \"system\"\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Mihomo.Binary != "system" {
		t.Fatalf("explicit system became %q", snapshot.Mihomo.Binary)
	}
}
