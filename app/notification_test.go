package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/localize"
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

func TestRunNotificationsDeliversLocalizedLowerLatencyMessage(t *testing.T) {
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

}
