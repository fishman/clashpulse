package mihomo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func packagedFake(t *testing.T, body string) (path, sum string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(body))
	return path, hex.EncodeToString(digest[:])
}

func TestInstallBundledCopiesVerifiedExecutablePrivately(t *testing.T) {
	packaged, sum := packagedFake(t, "fake mihomo")
	root := filepath.Join(t.TempDir(), "mihomo")
	got, err := installBundled(packaged, root, "v1", sum)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "v1-"+sum[:12], "mihomo"); got != want {
		t.Fatalf("copy at %q, want %q", got, want)
	}
	if data, _ := os.ReadFile(got); string(data) != "fake mihomo" {
		t.Fatalf("copy holds %q", data)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{root, filepath.Dir(got), got} {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("%s mode %v, %v; want 0700", path, info.Mode().Perm(), err)
			}
		}
	}
}

func TestInstallBundledRejectsMismatchAndMissing(t *testing.T) {
	packaged, _ := packagedFake(t, "fake mihomo")
	root := filepath.Join(t.TempDir(), "mihomo")
	wrong := strings.Repeat("0", 64)
	if _, err := installBundled(packaged, root, "v1", wrong); err == nil || !strings.Contains(err.Error(), "bundled Mihomo failed verification") {
		t.Fatalf("mismatch error = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "v1-"+wrong[:12]))
	if len(entries) != 0 {
		t.Fatalf("mismatch left files: %v", entries)
	}
	if _, err := installBundled(filepath.Join(t.TempDir(), "mihomo"), root, "v1", wrong); err == nil || !strings.Contains(err.Error(), "bundled Mihomo is not installed") {
		t.Fatalf("missing error = %v", err)
	}
}

func TestInstallBundledReusesMatchingCopyAndReplacesTamperedOne(t *testing.T) {
	packaged, sum := packagedFake(t, "fake mihomo")
	root := filepath.Join(t.TempDir(), "mihomo")
	first, err := installBundled(packaged, root, "v1", sum)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(first, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := installBundled(packaged, root, "v1", sum); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(first); !info.ModTime().Equal(old) {
		t.Fatal("matching copy was rewritten")
	}
	if err := os.WriteFile(first, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := installBundled(packaged, root, "v1", sum); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(first); string(data) != "fake mihomo" {
		t.Fatalf("tampered copy kept %q", data)
	}
}

func TestInstallBundledWithoutIdentityIsNotInstalled(t *testing.T) {
	if _, err := InstallBundled(t.TempDir()); err == nil || !strings.Contains(err.Error(), "bundled Mihomo is not installed") {
		t.Fatalf("error = %v", err)
	}
}

func TestPackagedPathFollowsPackageLayout(t *testing.T) {
	darwin := filepath.Join("A", "ClashPulse.app", "Contents", "MacOS", "clashpulse")
	if got, want := packagedPath("darwin", darwin), filepath.Join("A", "ClashPulse.app", "Contents", "Resources", "mihomo"); got != want {
		t.Fatalf("darwin %q, want %q", got, want)
	}
	windows := filepath.Join("C", "ClashPulse", "clashpulse.exe")
	if got, want := packagedPath("windows", windows), filepath.Join("C", "ClashPulse", "mihomo.exe"); got != want {
		t.Fatalf("windows %q, want %q", got, want)
	}
}

func TestPruneBundledKeepsOnlyActiveVersion(t *testing.T) {
	packaged, sum := packagedFake(t, "fake mihomo")
	state := t.TempDir()
	root := filepath.Join(state, "mihomo")
	stale := filepath.Join(root, "v0-000000000000")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	active, err := installBundled(packaged, root, "v1", sum)
	if err != nil {
		t.Fatal(err)
	}
	PruneBundled(state, active)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale version kept: %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active copy removed: %v", err)
	}
}
