// Package localtest provides test fixtures that respect each platform's
// local limits: short private directories, IPC endpoints, and absolute paths.
package localtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

var pipes atomic.Uint64

// Dir is a private temporary directory whose path does not grow with the test
// name, so a Unix socket inside it stays under the sun_path limit (104 bytes on
// macOS, whose temporary directory alone is about 50). The path is canonical,
// so it equals paths the code under test resolves through an aliased ancestor
// such as macOS /var or a Windows 8.3 short name.
func Dir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// Endpoint is a private IPC endpoint for one test: a Unix socket in its own
// short directory, or a uniquely named pipe on Windows.
func Endpoint(t testing.TB) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\clashpulse-test-%d-%d`, os.Getpid(), pipes.Add(1))
	}
	return filepath.Join(Dir(t), "ipc", "ipc.sock")
}

// Abs joins elem below the filesystem root, so a fixture path such as
// Abs("usr", "bin", "mihomo") is absolute on Windows as well.
func Abs(elem ...string) string {
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	return filepath.Join(append([]string{root}, elem...)...)
}
