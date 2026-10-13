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

// trappingChild starts a fake Mihomo that records each SIGTERM in the returned
// file and exits on it only when exitOnTerm is set.
func trappingChild(t *testing.T, ctx context.Context, process *Process, exitOnTerm bool) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("fake executable requires POSIX shell")
	}
	dir := t.TempDir()
	ready, term := filepath.Join(dir, "ready"), filepath.Join(dir, "term")
	exit := ""
	if exitOnTerm {
		exit = "; exit 0"
	}
	binary := filepath.Join(dir, "mihomo")
	script := fmt.Sprintf("#!/bin/sh\ntrap 'echo term >> %q%s' TERM\n: > %q\nwhile :; do sleep 1 & wait $!; done\n", term, exit, ready)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := process.Start(ctx, StartPlan{Capability: Capability{Path: binary}, ConfigPath: "/tmp/config.yaml"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Stop(context.Background()) })
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			return term
		}
		if time.Now().After(deadline) {
			t.Fatal("fake mihomo did not install its TERM trap")
		}
	}
}

func receivedTerm(t *testing.T, term string) {
	t.Helper()
	if body, err := os.ReadFile(term); err != nil || len(body) == 0 {
		t.Fatalf("child was not sent SIGTERM: %v", err)
	}
}

func TestProcessStopTerminatesGracefully(t *testing.T) {
	process := new(Process)
	term := trappingChild(t, context.Background(), process, true)
	started := time.Now()
	if err := process.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= stopGrace {
		t.Fatalf("graceful child was only stopped by escalation after %v", elapsed)
	}
	receivedTerm(t, term)
}

func TestProcessStopKillsChildIgnoringTermAfterGrace(t *testing.T) {
	process := &Process{grace: 100 * time.Millisecond}
	term := trappingChild(t, context.Background(), process, false)
	started := time.Now()
	if err := process.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < process.grace {
		t.Fatalf("child was killed after %v, before its grace", elapsed)
	}
	receivedTerm(t, term)
}

func TestProcessStopHonorsContextDuringGrace(t *testing.T) {
	process := &Process{grace: time.Minute}
	trappingChild(t, context.Background(), process, false)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := process.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline", err)
	}
	select {
	case <-process.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("canceled stop left the child running")
	}
}

func TestProcessContextCancellationTerminatesGracefully(t *testing.T) {
	process := &Process{grace: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	term := trappingChild(t, ctx, process, false)
	cancel()
	select {
	case <-process.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("canceled child ignoring SIGTERM was never killed")
	}
	receivedTerm(t, term)
}
