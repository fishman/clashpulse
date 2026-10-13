package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/subscriptions"
)

func TestAllMeasuredSlowRequiresEveryRecentSuccessfulProxy(t *testing.T) {
	now := time.Unix(1700000000, 0)
	groups := []core.GroupSnapshot{{ID: opaqueID("auto"), Type: "Selector", Proxies: []string{opaqueID("alpha"), opaqueID("beta")}}}
	proxies := []core.ProxySnapshot{
		{GroupID: opaqueID("auto"), ID: opaqueID("alpha"), Outcome: "success", LatencyMillis: 251, FinishedAt: now.Unix()},
		{GroupID: opaqueID("auto"), ID: opaqueID("beta"), Outcome: "success", LatencyMillis: 251, FinishedAt: now.Unix()},
	}
	if high, fast := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); !high || fast {
		t.Fatalf("all high = %v, any fast = %v", high, fast)
	}
	proxies[1].LatencyMillis = 250
	if high, fast := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); high || !fast {
		t.Fatalf("boundary high = %v, any fast = %v", high, fast)
	}
	proxies[1].Outcome, proxies[1].LatencyMillis = "timeout", 0
	if high, fast := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); high || fast {
		t.Fatalf("timeout high = %v, any fast = %v", high, fast)
	}
	proxies[1].Outcome, proxies[1].LatencyMillis = "success", 301
	groups = append(groups, core.GroupSnapshot{ID: opaqueID("native"), Type: "URLTest", Proxies: []string{opaqueID("gamma")}})
	proxies = append(proxies, core.ProxySnapshot{GroupID: opaqueID("native"), ID: opaqueID("gamma"), Outcome: "success", LatencyMillis: 310, FinishedAt: now.Add(-3 * time.Minute).Unix()})
	if high, _ := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); !high {
		t.Fatal("staggered recent URLTest sample was ignored")
	}
	proxies[2].FinishedAt = now.Add(-12 * time.Minute).Unix()
	if high, _ := allMeasuredSlow(groups, proxies, 250*time.Millisecond, now, 5*time.Minute); high {
		t.Fatal("stale URLTest sample claimed all connections were slow")
	}
}

func TestRunNotificationsDeliversLocalizedMessages(t *testing.T) {
	localize.SetLanguage("zh-CN")
	t.Cleanup(func() { localize.SetLanguage("en") })
	delivered := make(chan desktopNotification, 1)
	ctx, cancel := context.WithCancel(context.Background())
	service := &runtimeService{
		notifications: make(chan time.Duration, 1), improvementNotifications: make(chan struct{}, 1),
		notificationErrors: make(chan error, 1), notificationDone: make(chan struct{}),
		notify: func(notification desktopNotification) error { delivered <- notification; return nil },
	}
	go service.runNotifications(ctx)
	t.Cleanup(func() { cancel(); <-service.notificationDone })
	service.improvementNotifications <- struct{}{}
	select {
	case notification := <-delivered:
		if notification.title != "连接已改善" || notification.message != "已自动切换到延迟更低的连接" {
			t.Fatalf("lower-latency notification = %+v", notification)
		}
		if strings.Contains(notification.title+notification.message, "node-") || strings.Contains(notification.title+notification.message, "http") {
			t.Fatalf("notification exposed connection details: %+v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("notification worker did not deliver lower-latency switch")
	}
	service.notifications <- 300 * time.Millisecond
	select {
	case notification := <-delivered:
		if notification.title != "ClashPulse 连接状态" || notification.message != "所有已测速连接均慢于 300 毫秒" {
			t.Fatalf("slow-connections notification = %+v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("notification worker did not deliver slow-connections alert")
	}
}

func TestTestNotificationCommandReportsDesktopDelivery(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte("[monitor]\nenabled = false\n")); err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRuntimeService(configDir, filepath.Join(root, "state"), initial)
	if err != nil {
		t.Fatal(err)
	}
	var unavailable atomic.Bool
	unavailable.Store(true)
	delivered := make(chan desktopNotification, 4)
	service.notify = func(notification desktopNotification) error {
		delivered <- notification
		if unavailable.Load() {
			return errors.New("no notification daemon")
		}
		return nil
	}
	service.subScheduler = subscriptions.NewScheduler(service.subs, 1)
	endpoint := filepath.Join(root, "socket", "ipc.sock")
	service.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: endpoint, Handler: func(_ context.Context, cmd ipc.Command) error {
		return service.enqueueIntent(cmd)
	}, InitialSnapshot: core.CloneSnapshot(service.snapshot)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.run(ctx, nil, nil) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := dialAppClient(ctx, endpoint)
	if client == nil {
		t.Fatal("IPC service unavailable")
	}
	defer client.Close()
	sendTest := func() {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		if _, err := client.Send(request, ipc.Command{Kind: ipc.CommandTestNotification}); err != nil {
			t.Fatal(err)
		}
		select {
		case notification := <-delivered:
			if notification.title != "ClashPulse notifications" || notification.message != "Notifications are working" {
				t.Fatalf("test notification = %+v", notification)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("test notification was not delivered")
		}
	}
	notificationIssue := func(state core.Snapshot) bool {
		for _, issue := range state.Errors {
			if issue.Kind == "notification" && issue.Message == "desktop notification unavailable" {
				return true
			}
		}
		return false
	}

	sendTest()
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool { return notificationIssue(state) && len(state.Jobs) == 0 })
	select {
	case extra := <-delivered:
		t.Fatalf("one test command delivered twice: %+v", extra)
	default:
	}
	unavailable.Store(false)
	sendTest()
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool { return !notificationIssue(state) && len(state.Jobs) == 0 })
	select {
	case extra := <-delivered:
		t.Fatalf("one test command delivered twice: %+v", extra)
	default:
	}
}
