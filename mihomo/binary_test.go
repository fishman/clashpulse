package mihomo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fakeBinary(t *testing.T, version string, exitCode int) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("fake executable requires POSIX shell")
	}

	path := filepath.Join(t.TempDir(), "mihomo")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$#\" -eq 1 ] && [ \"$1\" = \"-v\" ]; then\n\tif [ -n %q ]; then\n\t\tprintf '%%s\\n' %q\n\tfi\n\texit %d\nfi\nexit 1\n", version, version, exitCode)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSystemSelectionWins(t *testing.T) {
	path := fakeBinary(t, "v1.19.31", 0)
	t.Setenv("PATH", filepath.Dir(path))

	got, err := Resolve(Selection{Kind: "system"})
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("Resolve() = %q, want %q", got, path)
	}
}

func TestResolveValidatesBundledAndExplicitPaths(t *testing.T) {
	path := fakeBinary(t, "v1.19.31", 0)
	for _, selection := range []Selection{
		{Kind: "bundled", Path: path},
		{Kind: "path", Path: path},
	} {
		got, err := Resolve(selection)
		if err != nil {
			t.Fatalf("Resolve(%+v): %v", selection, err)
		}
		if got != path {
			t.Fatalf("Resolve(%+v) = %q, want %q", selection, got, path)
		}
	}
}

func TestResolveRejectsNonExecutableBundledAndExplicitPaths(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []Selection{
		{Kind: "bundled", Path: plain},
		{Kind: "path", Path: plain},
	} {
		if _, err := Resolve(selection); err == nil {
			t.Fatalf("Resolve(%+v) accepted a non-executable file", selection)
		}
	}
	if _, err := Resolve(Selection{Kind: "invalid"}); err == nil {
		t.Fatal("Resolve accepted an invalid selection")
	}
}

func TestInspectUsesVersionArgv(t *testing.T) {
	path := fakeBinary(t, "v1.19.31", 0)

	got, err := Inspect(context.Background(), Selection{Kind: "path", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != path || got.Version != "v1.19.31" {
		t.Fatalf("Inspect() = %+v", got)
	}
}

func TestInspectRejectsInvalidBinary(t *testing.T) {
	path := fakeBinary(t, "", 1)
	if _, err := Inspect(context.Background(), Selection{Kind: "path", Path: path}); err == nil {
		t.Fatal("Inspect accepted a binary that rejected version inspection")
	}
}

func TestInspectHonorsCanceledContext(t *testing.T) {
	path := fakeBinary(t, "v1.19.31", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Inspect(ctx, Selection{Kind: "path", Path: path}); err == nil {
		t.Fatal("Inspect accepted a canceled context")
	}
}
