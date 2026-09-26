package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/download"
	"time"

	"github.com/fishman/clashpulse/core"
)

func TestCLIUsesDesktopByDefault(t *testing.T) {
	var desktopCalls, serviceCalls int
	actions := cliActions{
		run: func(context.Context) error { serviceCalls++; return nil },
		desktop: func(_ context.Context, run func(context.Context) error) error {
			desktopCalls++
			return run(context.Background())
		},
	}
	if code := runCLI(context.Background(), []string{"clashpulse"}, new(bytes.Buffer), new(bytes.Buffer), actions); code != 0 || desktopCalls != 1 || serviceCalls != 1 {
		t.Fatalf("root command: exit=%d desktop=%d service=%d", code, desktopCalls, serviceCalls)
	}
}

func TestCLIGuiAndTuiCommandsDispatch(t *testing.T) {
	var guiCalls, tuiCalls int
	actions := cliActions{
		run:     func(context.Context) error { return nil },
		desktop: func(context.Context, func(context.Context) error) error { guiCalls++; return nil },
		tui:     func(context.Context) error { tuiCalls++; return nil },
	}
	for _, command := range []string{"gui", "tui"} {
		if code := runCLI(context.Background(), []string{"clashpulse", command}, new(bytes.Buffer), new(bytes.Buffer), actions); code != 0 {
			t.Fatalf("%s exited %d", command, code)
		}
	}
	if guiCalls != 1 || tuiCalls != 1 {
		t.Fatalf("gui=%d tui=%d", guiCalls, tuiCalls)
	}
}

func TestCLIVersionKeepsOutputAndSkipsApplication(t *testing.T) {
	called := false
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	actions := cliActions{run: func(context.Context) error { called = true; return nil }}
	if code := runCLI(context.Background(), []string{"clashpulse", "version"}, stdout, stderr, actions); code != 0 || called || stdout.String() != "clashpulse dev\n" || stderr.Len() != 0 {
		t.Fatalf("version: exit=%d called=%t stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestCLIUnexpectedArgsFallBackToApplication(t *testing.T) {
	for _, args := range [][]string{
		{"clashpulse", "version", "extra"},
		{"clashpulse", "unknown"},
		{"clashpulse", "unknown", "extra"},
		{"clashpulse", "gui", "extra"},
		{"clashpulse", "tui", "extra"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			called, desktopCalled := false, false
			stderr := new(bytes.Buffer)
			actions := cliActions{
				run: func(context.Context) error {
					called = true
					return errors.New("application fallback")
				},
				desktop: func(context.Context, func(context.Context) error) error {
					desktopCalled = true
					return nil
				},
				tui: func(context.Context) error { return nil },
			}
			if code := runCLI(context.Background(), args, new(bytes.Buffer), stderr, actions); code != 1 || !called || desktopCalled || stderr.String() != "application fallback\n" {
				t.Fatalf("exit=%d called=%t desktop=%t stderr=%q", code, called, desktopCalled, stderr.String())
			}
		})
	}
}

func TestCLIRefreshDispatchesAllAndScopedOperations(t *testing.T) {
	var got []string
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	actions := cliActions{refresh: func(_ context.Context, kind, id string, show bool) error {
		got = append(got, fmt.Sprintf("%s:%s:%t", kind, id, show))
		return nil
	}}
	for _, test := range []struct {
		args []string
		want string
		out  string
	}{
		{[]string{"clashpulse", "refresh"}, "::false", "refreshed all enabled sources\n"},
		{[]string{"clashpulse", "refresh", "subscription", "subscription-a", "--show-response"}, "subscription:subscription-a:true", "refreshed subscription subscription-a\n"},
		{[]string{"clashpulse", "refresh", "--show-response", "subscription", "subscription-a"}, "subscription:subscription-a:true", "refreshed subscription subscription-a\n"},
		{[]string{"clashpulse", "refresh", "resource", "geo"}, "resource:geo:false", "refreshed resource geo\n"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := runCLI(context.Background(), test.args, stdout, stderr, actions); code != 0 || got[len(got)-1] != test.want || stdout.String() != test.out || stderr.Len() != 0 {
			t.Fatalf("%q: exit=%d got=%q stdout=%q stderr=%q", test.args, code, got, stdout.String(), stderr.String())
		}
	}
}

func TestCLIDownloadOnlyDispatchesSubscriptions(t *testing.T) {
	var got []string
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	actions := cliActions{download: func(_ context.Context, id string, show bool) error {
		got = append(got, fmt.Sprintf("%s:%t", id, show))
		return nil
	}}
	for _, test := range []struct {
		args []string
		want string
		out  string
	}{
		{[]string{"clashpulse", "download"}, ":false", "downloaded all enabled subscriptions\n"},
		{[]string{"clashpulse", "download", "subscription", "subscription-a", "--show-response"}, "subscription-a:true", "downloaded subscription subscription-a\n"},
		{[]string{"clashpulse", "download", "--show-response", "subscription", "subscription-a"}, "subscription-a:true", "downloaded subscription subscription-a\n"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := runCLI(context.Background(), test.args, stdout, stderr, actions); code != 0 || got[len(got)-1] != test.want || stdout.String() != test.out || stderr.Len() != 0 {
			t.Fatalf("%q: exit=%d got=%q stdout=%q stderr=%q", test.args, code, got, stdout.String(), stderr.String())
		}
	}
	called := len(got)
	if code := runCLI(context.Background(), []string{"clashpulse", "download", "resource", "geo"}, stdout, stderr, actions); code == 0 || len(got) != called {
		t.Fatal("download accepted a resource target")
	}
}

func TestCLIShowResponsePrintsBoundedBodyOnError(t *testing.T) {
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	actions := cliActions{download: func(context.Context, string, bool) error {
		return fmt.Errorf("clashpulse: download subscription subscription-a: invalid profile: %w", download.StatusError{Code: http.StatusOK, ResponseBody: "bad profile"})
	}}
	code := runCLI(context.Background(), []string{"clashpulse", "download", "subscription", "subscription-a", "--show-response"}, stdout, stderr, actions)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "HTTP 200 OK") || !strings.Contains(stderr.String(), "bad profile") {
		t.Fatalf("show-response: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCLIPropagatesActionError(t *testing.T) {
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	actions := cliActions{refresh: func(context.Context, string, string, bool) error { return errors.New("refresh failed") }}
	if code := runCLI(context.Background(), []string{"clashpulse", "refresh"}, stdout, stderr, actions); code != 1 || stderr.String() != "refresh failed\n" {
		t.Fatalf("action error: exit=%d stderr=%q", code, stderr.String())
	}
}

func TestCLIActivateLocalFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, announced := make(chan struct{}), make(chan struct{})
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	called := 0
	actions := cliActions{activateFile: func(ctx context.Context, path string, announce func() error) error {
		called++
		if path != "my profile.yaml" {
			t.Fatalf("path = %q", path)
		}
		<-ready
		if err := announce(); err != nil {
			return err
		}
		close(announced)
		<-ctx.Done()
		return nil
	}}
	done := make(chan int, 1)
	go func() {
		done <- runCLI(ctx, []string{"clashpulse", "activate", "my profile.yaml"}, stdout, stderr, actions)
	}()
	select {
	case code := <-done:
		t.Fatalf("returned before readiness: %d", code)
	default:
	}
	close(ready)
	select {
	case <-announced:
	case <-time.After(time.Second):
		t.Fatal("readiness was not announced")
	}
	if got := stdout.String(); got != "local profile active; press Ctrl-C to stop\n" {
		t.Fatalf("success output = %q", got)
	}
	select {
	case code := <-done:
		t.Fatalf("exited while active: %d", code)
	default:
	}
	cancel()
	if code := <-done; code != 0 || stderr.Len() != 0 || called != 1 {
		t.Fatalf("shutdown exit=%d stderr=%q calls=%d", code, stderr.String(), called)
	}
	for _, args := range [][]string{{"clashpulse", "activate"}, {"clashpulse", "activate", "a", "b"}} {
		before := called
		if code := runCLI(context.Background(), args, new(bytes.Buffer), new(bytes.Buffer), actions); code == 0 || called != before {
			t.Fatalf("invalid args %v: code=%d calls=%d", args, code, called)
		}
	}
	private := "password=private"
	bad := cliActions{activateFile: func(context.Context, string, func() error) error {
		return core.WrapActivation(core.ActivationControllerReadiness, errors.New(private))
	}}
	stdout.Reset()
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"clashpulse", "activate", "secret profile.yaml"}, stdout, stderr, bad); code != 1 || stdout.Len() != 0 || stderr.String() != core.ActivationControllerReadiness.Message()+"\n" || strings.Contains(stderr.String(), "secret profile.yaml") {
		t.Fatalf("unsafe failure: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCLIActivateOptionLikeFilenameIsPrivate(t *testing.T) {
	called := false
	stderr := new(bytes.Buffer)
	actions := cliActions{activateFile: func(_ context.Context, path string, _ func() error) error {
		called = path == "-private-token.yaml"
		return core.WrapActivation(core.ActivationFileInput, errors.New("password=private"))
	}}
	code := runCLI(context.Background(), []string{"clashpulse", "activate", "-private-token.yaml"}, new(bytes.Buffer), stderr, actions)
	if !called || code != 1 || stderr.String() != core.ActivationFileInput.Message()+"\n" {
		t.Fatalf("unsafe filename handling: called=%t code=%d stderr=%q", called, code, stderr.String())
	}
}
