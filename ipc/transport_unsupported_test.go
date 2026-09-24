//go:build !unix && !windows

package ipc

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedPlatformFailsClearly(t *testing.T) {
	if _, err := NewServer(ServerOptions{Handler: func(context.Context, Command) error { return nil }}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("NewServer error = %v, want unsupported-platform error", err)
	}
	if _, err := Dial(context.Background(), ""); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Dial error = %v, want unsupported-platform error", err)
	}
}
