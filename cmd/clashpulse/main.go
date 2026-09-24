package main

import (
	"context"
	"fmt"
	"io"
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
	os.Exit(runMainContext(ctx, os.Args[1:], os.Stdout, os.Stderr, app.Run))
}

func runMain(args []string, stdout, stderr io.Writer, runner func(context.Context) error) int {
	return runMainContext(context.Background(), args, stdout, stderr, runner)
}

func runMainContext(ctx context.Context, args []string, stdout, stderr io.Writer, runner func(context.Context) error) int {
	return runMainContextWithDesktop(ctx, args, stdout, stderr, runner, runDesktop)
}

func runMainContextWithDesktop(ctx context.Context, args []string, stdout, stderr io.Writer, runner func(context.Context) error, desktop func(context.Context, func(context.Context) error) error) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	var err error
	if len(args) == 0 {
		err = desktop(ctx, runner)
	} else if len(args) == 1 {
		switch args[0] {
		case "tui":
			err = tui.Run(ctx, "")
		case "gui":
			err = desktop(ctx, runner)
		default:
			err = runner(ctx)
		}
	} else {
		err = runner(ctx)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
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
