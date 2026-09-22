package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var called int
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	code := runMain([]string{"version"}, stdout, stderr, func(context.Context) error {
		called++
		return errors.New("not initialized")
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if called != 0 {
		t.Fatalf("app.Run called %d times, want 0", called)
	}
	if got := stdout.String(); got != "clashpulse dev\n" {
		t.Fatalf("stdout = %q, want %q", got, "clashpulse dev\n")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunVersionWithExtraArgsUsesAppRun(t *testing.T) {
	var called int
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	code := runMain([]string{"version", "extra"}, stdout, stderr, func(context.Context) error {
		called++
		return errors.New("not initialized")
	})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if called != 1 {
		t.Fatalf("app.Run called %d times, want 1", called)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "not initialized\n" {
		t.Fatalf("stderr = %q, want %q", got, "not initialized\n")
	}
}

func TestRunWithoutArgsUsesAppRun(t *testing.T) {
	var called int
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	code := runMain(nil, stdout, stderr, func(context.Context) error {
		called++
		return errors.New("not initialized")
	})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if called != 1 {
		t.Fatalf("app.Run called %d times, want 1", called)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "not initialized\n" {
		t.Fatalf("stderr = %q, want %q", got, "not initialized\n")
	}
}
