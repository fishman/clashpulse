//go:build !linux

package tun

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestUnsupportedPlatform(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "tun"))
	source := Source{Path: "mihomo", Version: "v1.19.31"}
	if got := m.Check(source); got != PrivilegeUnsupported {
		t.Fatalf("Check = %q, want unsupported-platform", got)
	}
	if err := m.Prepare(context.Background(), source); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Prepare error = %v, want ErrUnsupported", err)
	}
}
