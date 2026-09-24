package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestRunAtCancelsStaleURLWhileIntentIsRunning(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake binary requires POSIX shell")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	binary := fakeAppMihomo(t, root, python)
	oldStarted, oldCanceled := make(chan struct{}, 1), make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			oldStarted <- struct{}{}
			<-r.Context().Done()
			oldCanceled <- struct{}{}
			return
		}
		if r.URL.Path == "/new" {
			_, _ = w.Write([]byte("proxies:\n  - name: fresh\n    type: direct\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	configDir := filepath.Join(root, "config")
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n", binary))); err != nil {
		t.Fatal(err)
	}
	writeURL := func(path string) {
		value := fmt.Sprintf("[[subscription]]\nid = \"rotating\"\nurl = %q\nallow_http = true\n", server.URL+path+"?token=short-lived")
		if err := config.Write(filepath.Join(configDir, "subscriptions.toml"), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	writeURL("/old")
	endpoint := filepath.Join(root, "ipc", "service.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunAt(ctx, configDir, filepath.Join(root, "state"), endpoint) }()
	var client *ipc.Client
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		attempt, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		client, _ = ipc.Dial(attempt, endpoint)
		stop()
		if client != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if client == nil {
		cancel()
		t.Fatalf("service did not listen: %v", <-done)
	}
	defer client.Close()
	refresh := func() {
		request, stop := context.WithTimeout(ctx, time.Second)
		_, err := client.Send(request, ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: "rotating"})
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	select {
	case <-oldStarted:
	case <-time.After(time.Second):
		t.Fatal("old URL fetch did not start")
	}
	writeURL("/new")
	select {
	case <-oldCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("old URL was not cancelled by config watcher")
	}
	refresh()
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Subscriptions) == 1 && state.Subscriptions[0].HashPrefix != "" && len(state.Jobs) == 0
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not stop")
	}
}
