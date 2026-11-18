package docsfixture

import (
	"context"
	"testing"

	"github.com/fishman/clashpulse/internal/localtest"
	"github.com/fishman/clashpulse/ipc"
)

func TestSnapshotCrossesIPCBoundary(t *testing.T) {
	_, err := ipc.NewServer(ipc.ServerOptions{
		Endpoint:        localtest.Endpoint(t),
		Handler:         func(context.Context, ipc.Command) error { return nil },
		InitialSnapshot: Snapshot(),
	})
	if err != nil {
		t.Fatalf("docs snapshot rejected by IPC validation: %v", err)
	}
}
