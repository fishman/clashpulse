package docsfixture

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fishman/clashpulse/ipc"
)

func TestSnapshotCrossesIPCBoundary(t *testing.T) {
	_, err := ipc.NewServer(ipc.ServerOptions{
		Endpoint:        filepath.Join(t.TempDir(), "private", "ipc.sock"),
		Handler:         func(context.Context, ipc.Command) error { return nil },
		InitialSnapshot: Snapshot(),
	})
	if err != nil {
		t.Fatalf("docs snapshot rejected by IPC validation: %v", err)
	}
}
