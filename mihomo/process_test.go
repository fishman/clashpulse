package mihomo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func blockingBinary(t *testing.T, argsFile string) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("fake executable requires POSIX shell")
	}

	path := filepath.Join(t.TempDir(), "mihomo")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nmv %q %q\nexec tail -f /dev/null\n", argsFile+".tmp", argsFile+".tmp", argsFile)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcessStartUsesExplicitArgv(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	binary := blockingBinary(t, argsFile)
	process := new(Process)

	if err := process.Start(context.Background(), StartPlan{
		Capability: Capability{Path: binary},
		ConfigPath: "/tmp/config.yaml",
		Args:       []string{"-d", "/tmp/home"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Stop(context.Background()) })

	deadline := time.After(time.Second)
	for {
		args, err := os.ReadFile(argsFile)
		if err == nil && len(args) > 0 {
			if got, want := strings.Fields(string(args)), []string{"-f", "/tmp/config.yaml", "-d", "/tmp/home"}; !slices.Equal(got, want) {
				t.Fatalf("argv = %q, want %q", got, want)
			}
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-deadline:
			t.Fatal("mihomo did not record argv")
		default:
			runtime.Gosched()
		}
	}
}

func TestProcessStartRejectsConcurrentChild(t *testing.T) {
	process := new(Process)
	plan := StartPlan{Capability: Capability{Path: blockingBinary(t, filepath.Join(t.TempDir(), "args"))}, ConfigPath: "/tmp/config.yaml"}
	if err := process.Start(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Stop(context.Background()) })

	if err := process.Start(context.Background(), plan); err == nil {
		t.Fatal("Start() error = nil, want running child rejection")
	}
}

func TestProcessStartFailureDoesNotRetainChild(t *testing.T) {
	process := new(Process)
	invalid := StartPlan{Capability: Capability{Path: filepath.Join(t.TempDir(), "missing")}, ConfigPath: "/tmp/config.yaml"}
	if err := process.Start(context.Background(), invalid); err == nil {
		t.Fatal("Start() error = nil, want invalid binary failure")
	}

	plan := StartPlan{Capability: Capability{Path: blockingBinary(t, filepath.Join(t.TempDir(), "args"))}, ConfigPath: "/tmp/config.yaml"}
	if err := process.Start(context.Background(), plan); err != nil {
		t.Fatalf("Start() after failure: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop(context.Background()) })
}

func TestProcessCanceledStartDoesNotRetainChild(t *testing.T) {
	process := new(Process)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan := StartPlan{Capability: Capability{Path: blockingBinary(t, filepath.Join(t.TempDir(), "args"))}, ConfigPath: "/tmp/config.yaml"}
	if err := process.Start(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context cancellation", err)
	}

	if err := process.Start(context.Background(), plan); err != nil {
		t.Fatalf("Start() after cancellation: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop(context.Background()) })
}

func TestProcessStopWaitsForChild(t *testing.T) {
	process := new(Process)
	plan := StartPlan{Capability: Capability{Path: blockingBinary(t, filepath.Join(t.TempDir(), "args"))}, ConfigPath: "/tmp/config.yaml"}
	if err := process.Start(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if err := process.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.Start(context.Background(), plan); err != nil {
		t.Fatalf("Start() after Stop(): %v", err)
	}
	t.Cleanup(func() { _ = process.Stop(context.Background()) })
}

func TestProcessExitedClosesAfterChildStops(t *testing.T) {
	process := new(Process)
	if process.Exited() != nil {
		t.Fatal("idle process has an exit channel")
	}
	plan := StartPlan{Capability: Capability{Path: blockingBinary(t, filepath.Join(t.TempDir(), "args"))}, ConfigPath: "/tmp/config.yaml"}
	if err := process.Start(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	finished := process.Exited()
	if finished == nil {
		t.Fatal("running process has no exit channel")
	}
	if err := process.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("child exit was not published")
	}
}

func TestProcessExitedRemainsObservableAfterRapidExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake executable needs POSIX shell")
	}
	binary := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	process := new(Process)
	if err := process.Start(context.Background(), StartPlan{Capability: Capability{Path: binary}, ConfigPath: "/tmp/local.yaml"}); err != nil {
		t.Fatal(err)
	}
	exited := process.Exited()
	if exited == nil {
		t.Fatal("rapidly terminated child lost its exit signal")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("child did not exit")
	}
	if process.Exited() != exited {
		t.Fatal("completed child exit signal was discarded")
	}
}
