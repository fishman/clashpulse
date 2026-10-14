//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Mihomo neither checks the secret on its Unix socket nor restricts the socket
// file, so the directory holding it must admit only this user.
func TestControllerSocketDirectoryIsPrivate(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(stateDir, "ctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	address, socket, err := controllerEndpoint(stateDir, 7899)
	if err != nil {
		t.Fatal(err)
	}
	if address != "" || !filepath.IsAbs(socket) {
		t.Fatalf("endpoint = %q, %q", address, socket)
	}
	info, err := os.Stat(filepath.Dir(socket))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Fatalf("controller directory mode = %o, want 700", mode)
	}
}
