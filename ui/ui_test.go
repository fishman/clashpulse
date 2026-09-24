package ui

import (
	"context"
	"testing"
)

func TestRunCanceledBeforeStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, "unused"); err != nil {
		t.Fatalf("Run returned error for canceled context: %v", err)
	}
}
