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
		activateFile: app.RunFile,
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
	endpoint := ipc.DefaultEndpoint()
	// One window per user: a second launch finds this lock held and reports the
	// running window instead of opening another. Terminal clients are not window
	// holders, so any number of them may attach to the same service.
	release, err := app.AcquireWindowLock()
	if err != nil {
		return err
	}
	defer release()
	// One lifecycle owner per machine: an owner already serving the endpoint
	// holds the state, the ports, and the System Proxy, so a launch without one
	// stays a client and shows the GUI instead of failing to take the state lock.
	if serviceRunning(ctx, endpoint) {
		return ui.Run(ctx, endpoint)
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner(serviceCtx) }()
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

// serviceRunning reports whether a lifecycle owner already serves the endpoint.
// A refused connection is the only answer that lets this process become the
// owner itself, so an unreadable endpoint is reported as not running.
func serviceRunning(ctx context.Context, endpoint string) bool {
	probe, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	client, err := ipc.Dial(probe, endpoint)
	if err != nil {
		return false
	}
	client.Close()
	return true
}
