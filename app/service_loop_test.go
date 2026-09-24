package app

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/subscriptions"
	"github.com/fishman/clashpulse/sysproxy"
)

func TestRunRefreshJobsDoNotBlockStop(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake Mihomo executable requires POSIX shell")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable for fake controller")
	}

	var blockSubscription, blockResource atomic.Bool
	subscriptionStarted, resourceStarted := make(chan struct{}, 1), make(chan struct{}, 1)
	releaseSubscription, releaseResource := make(chan struct{}), make(chan struct{})
	var releaseSubscriptionOnce, releaseResourceOnce sync.Once
	releaseSubscriptionNow := func() { releaseSubscriptionOnce.Do(func() { close(releaseSubscription) }) }
	releaseResourceNow := func() { releaseResourceOnce.Do(func() { close(releaseResource) }) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subscription":
			if blockSubscription.Load() {
				select {
				case subscriptionStarted <- struct{}{}:
				default:
				}
				<-releaseSubscription
			}
			_, _ = w.Write([]byte("proxies:\n  - name: node-a\n    type: direct\n  - name: node-b\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a, node-b]\n"))
		case "/resource":
			if blockResource.Load() {
				select {
				case resourceStarted <- struct{}{}:
				default:
				}
				<-releaseResource
			}
			_, _ = w.Write([]byte("payload:\n  - example.com\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer releaseSubscriptionNow()
	defer releaseResourceNow()

	root := t.TempDir()
	certPath := filepath.Join(root, "test-root.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certPath)
	binary := fakeAppMihomo(t, root, python)
	configDir := filepath.Join(root, "config")
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = false\n", binary))); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "subscriptions.toml"), []byte(fmt.Sprintf("[[subscription]]\nid = \"daily\"\nurl = %q\nenabled = true\n", server.URL+"/subscription"))); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "resources.toml"), []byte(fmt.Sprintf("[[resource]]\nid = \"ads\"\nkind = \"rule-set\"\nformat = \"yaml\"\nrule_type = \"domain\"\nurl = %q\nenabled = true\n", server.URL+"/resource"))); err != nil {
		t.Fatal(err)
	}

	endpoint := filepath.Join(root, "socket", "ipc.sock")
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
		t.Fatalf("service did not start: %v", <-done)
	}
	defer client.Close()
	send := func(kind ipc.CommandKind, resourceID string) {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		command := ipc.Command{Kind: kind}
		if kind == ipc.CommandRefreshSubscription || kind == ipc.CommandActivateSubscription {
			command.SubscriptionID = "daily"
		}
		if kind == ipc.CommandRefreshResource {
			command.ResourceID = resourceID
		}
		if _, err := client.Send(request, command); err != nil {
			t.Fatal(err)
		}
	}
	waitStarted := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("refresh request did not start")
		}
	}

	send(ipc.CommandRefreshSubscription, "")
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Subscriptions) == 1 && state.Subscriptions[0].HashPrefix != "" && len(state.Jobs) == 0
	})
	send(ipc.CommandActivateSubscription, "")
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && state.Subscriptions[0].Active && len(state.Jobs) == 0
	})

	blockSubscription.Store(true)
	send(ipc.CommandRefreshSubscription, "")
	waitStarted(subscriptionStarted)
	send(ipc.CommandStop, "")
	waitServiceStopped(t, ctx, client, func(core.Snapshot) bool { return true })
	releaseSubscriptionNow()
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool { return len(state.Jobs) == 0 })

	send(ipc.CommandStart, "")
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool { return len(state.Groups) == 1 && len(state.Jobs) == 0 })
	blockResource.Store(true)
	send(ipc.CommandRefreshResource, "ads")
	waitStarted(resourceStarted)
	send(ipc.CommandStop, "")
	waitServiceStopped(t, ctx, client, func(state core.Snapshot) bool { return len(state.Jobs) == 0 })
	releaseResourceNow()
	blockedBinary := filepath.Join(root, "slow-inspect-mihomo")
	started := filepath.Join(root, "inspect-started")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n -v) : > %q; sleep 6; echo 'Mihomo Meta v1.19.31 linux amd64'; exit 0;;\n -t) exit 0;;\nesac\nexit 1\n", started)
	if err := os.WriteFile(blockedBinary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = false\n", blockedBinary))); err != nil {
		t.Fatal(err)
	}
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool { return state.Binary.Desired == blockedBinary })
	send(ipc.CommandStart, "")
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(started); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("binary inspection did not start")
	}
	send(ipc.CommandStop, "")
	waitServiceStopped(t, ctx, client, func(state core.Snapshot) bool { return len(state.Jobs) == 0 })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service shutdown did not finish")
	}
}

func TestRunWaitsForRefreshWorkerOnShutdown(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	profile := "proxies:\n  - name: node-a\n    type: direct\n"
	var fetches atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := profile
		if fetches.Add(1) > 1 {
			body += "# changed\n"
		}
		_, _ = w.Write([]byte(body))
	}))
	defer httpServer.Close()
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte("[mihomo]\nbinary = \"system\"\n")); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "subscriptions.toml"), []byte(fmt.Sprintf("[[subscription]]\nid = \"slow\"\nurl = %q\nenabled = true\nallow_http = true\nrefresh_interval = \"24h\"\ntimeout = \"30s\"\n", httpServer.URL))); err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	stateChanged := make(chan struct{}, 1)
	s := &runtimeService{
		configDir: configDir, stateDir: stateDir, store: config.NewStore(initial), proxy: sysproxy.NewManager(),
		intents: make(chan ipc.Command, intentQueueSize), changes: make(chan config.Change, 1), resourceRefresh: make(chan []string, 1),
		stateChanged: stateChanged, backgroundErrors: make(chan error, 2), configErrors: make(chan error, 1),
		notificationErrors: make(chan error, 1), notifications: make(chan time.Duration, 1), notificationDone: make(chan struct{}),
	}
	subscriptionStore, err := subscriptions.NewStore(filepath.Join(stateDir, "subscriptions"))
	if err != nil {
		t.Fatal(err)
	}
	var block atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseWork := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseWork()
	service, err := subscriptions.NewService(subscriptionStore, subscriptions.Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return httpServer.Client().Transport, nil },
		Render: func(_ context.Context, source []byte) ([]byte, error) {
			if block.Load() {
				close(entered)
				<-release
			}
			return append([]byte("generated:\n"), source...), nil
		},
		Validate: func(context.Context, []byte) error { return nil },
		Apply:    func(context.Context, []byte) error { return nil },
		Restore:  func(context.Context, []byte) error { return nil },
		Current: func(id string) (config.Subscription, bool) {
			for _, item := range initial.Subscriptions {
				if item.ID == id {
					return item, true
				}
			}
			return config.Subscription{}, false
		},
		OnChange: func() {
			select {
			case stateChanged <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.subs = service
	s.subScheduler = subscriptions.NewScheduler(service, 1)
	if _, err := service.Add(initial.Subscriptions[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "slow"); err != nil {
		t.Fatal(err)
	}
	s.snapshot = s.stateSnapshot()
	s.lastPublished = core.CloneSnapshot(s.snapshot)
	s.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(root, "socket", "service.sock"), Handler: func(_ context.Context, cmd ipc.Command) error {
		select {
		case s.intents <- cmd:
			return nil
		default:
			return fmt.Errorf("intent queue full")
		}
	}, InitialSnapshot: s.snapshot})
	if err != nil {
		t.Fatal(err)
	}

	block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	runFinished := false
	defer func() {
		if !runFinished {
			cancel()
			releaseWork()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}
	}()
	go func() { done <- s.run(ctx) }()
	s.intents <- ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: "slow"}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		runFinished = true
		t.Fatalf("service returned before owned refresh work ended: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseWork()
	select {
	case err := <-done:
		runFinished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not finish after refresh work was released")
	}
}

func waitServiceStopped(t *testing.T, ctx context.Context, client *ipc.Client, ready func(core.Snapshot) bool) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		request, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		state, err := client.Snapshot(request)
		stop()
		if err == nil && len(state.Groups) == 0 && ready(state) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Stop did not run while refresh was blocked")
}
