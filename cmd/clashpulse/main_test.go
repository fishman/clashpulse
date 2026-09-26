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

func TestRunWithoutArgsStartsDesktopClient(t *testing.T) {
	var desktopCalls, serviceCalls int
	code := runMainContextWithDesktop(context.Background(), nil, new(bytes.Buffer), new(bytes.Buffer),
		func(context.Context) error { serviceCalls++; return nil },
		func(context.Context, func(context.Context) error) error { desktopCalls++; return nil })
	if code != 0 || desktopCalls != 1 || serviceCalls != 0 {
		t.Fatalf("exit %d, desktop calls %d, direct service calls %d", code, desktopCalls, serviceCalls)
	}
}

func TestRefreshCommandDispatchesTypedSource(t *testing.T) {
	var gotKind, gotID string
	stdout := new(bytes.Buffer)
	err := runRefreshCommand(context.Background(), []string{"resource", "geo"}, stdout, func(_ context.Context, kind, id string) error {
		gotKind, gotID = kind, id
		return nil
	})
	if err != nil || gotKind != "resource" || gotID != "geo" || stdout.String() != "refreshed resource geo\n" {
		t.Fatalf("refresh dispatch = %q, %q, %q, %v", gotKind, gotID, stdout.String(), err)
	}
}

func TestRefreshCommandRejectsUnknownOrIncompleteTarget(t *testing.T) {
	for _, args := range [][]string{{"subscription"}, {"proxy", "alpha"}} {
		called := false
		err := runRefreshCommand(context.Background(), args, new(bytes.Buffer), func(context.Context, string, string) error {
			called = true
			return nil
		})
		if err == nil || called {
			t.Fatalf("refresh accepted %q or invoked a target: %v", args, err)
		}
	}
}

func TestRefreshCommandRoutesWithoutStartingDesktop(t *testing.T) {
	var kind, id string
	var runnerCalls, desktopCalls int
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := runMainContextWithRefresh(context.Background(), []string{"refresh", "subscription", "alpha"}, stdout, stderr,
		func(context.Context) error { runnerCalls++; return nil },
		func(context.Context, func(context.Context) error) error { desktopCalls++; return nil },
		func(_ context.Context, gotKind, gotID string) error { kind, id = gotKind, gotID; return nil })
	if code != 0 || kind != "subscription" || id != "alpha" || runnerCalls != 0 || desktopCalls != 0 || stdout.String() != "refreshed subscription alpha\n" || stderr.Len() != 0 {
		t.Fatalf("refresh entrypoint = code %d, kind %q, id %q, runner %d, desktop %d, stdout %q, stderr %q", code, kind, id, runnerCalls, desktopCalls, stdout.String(), stderr.String())
	}
}

func TestRefreshCommandPrintsOnlyScopedSanitizedFailure(t *testing.T) {
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := runMainContextWithRefresh(context.Background(), []string{"refresh", "subscription", "feed"}, stdout, stderr,
		func(context.Context) error { return nil },
		func(context.Context, func(context.Context) error) error { return nil },
		func(context.Context, string, string) error {
			return errors.New("clashpulse: refresh subscription feed: HTTP 406")
		})
	if code != 1 || stdout.Len() != 0 || stderr.String() != "clashpulse: refresh subscription feed: HTTP 406\n" {
		t.Fatalf("refresh failure output = code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
