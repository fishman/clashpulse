package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fishman/clashpulse/app"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/tui"
	"github.com/fishman/clashpulse/ui"
)

const version = "clashpulse dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	actions := cliActions{
		run: app.Run, desktop: runDesktop, tui: func(ctx context.Context) error { return tui.Run(ctx, "") },
		refresh: func(ctx context.Context, kind, id string, show bool) error {
			return app.RefreshWithOptions(ctx, kind, id, app.RefreshOptions{ShowResponse: show})
		},
		download: func(ctx context.Context, id string, show bool) error {
			return app.DownloadWithOptions(ctx, id, app.RefreshOptions{ShowResponse: show})
		},
	}
	os.Exit(runCLI(ctx, os.Args, os.Stdout, os.Stderr, actions))
}

func runDesktop(ctx context.Context, runner func(context.Context) error) error {
	serviceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner(serviceCtx) }()
	endpoint := ipc.DefaultEndpoint()
	readyCtx, stop := context.WithTimeout(serviceCtx, 5*time.Second)
	defer stop()
	for {
		attempt, closeAttempt := context.WithTimeout(readyCtx, 150*time.Millisecond)
		client, err := ipc.Dial(attempt, endpoint)
		closeAttempt()
		if err == nil {
			client.Close()
			break
		}
		select {
		case err := <-done:
			return err
		case <-readyCtx.Done():
			return fmt.Errorf("clashpulse: desktop service did not become ready: %w", readyCtx.Err())
		case <-time.After(30 * time.Millisecond):
		}
	}
	viewErr := ui.Run(serviceCtx, endpoint)
	cancel()
	serviceErr := <-done
	if viewErr != nil {
		return viewErr
	}
	return serviceErr
}
