package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestIPCPutsAndDeletesPrivateSubscriptionIntent(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	endpoint := filepath.Join(root, "socket", "ipc.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunAt(ctx, configDir, filepath.Join(root, "state"), endpoint) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
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
		t.Fatal("IPC service unavailable")
	}
	defer client.Close()
	url := "https://provider.invalid/profile?token=private-token"
	name := "Daily"
	send := func(command ipc.Command) {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		_, err := client.Send(request, command)
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	send(ipc.Command{Kind: ipc.CommandPutSubscription, SubscriptionID: "daily", Subscription: &ipc.SubscriptionEdit{Name: &name, URL: &url, Enabled: new(true)}})
	state := waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool {
		return len(s.Subscriptions) == 1 && s.Subscriptions[0].Name == name && len(s.Jobs) == 0
	})
	if strings.Contains(fmt.Sprintf("%+v", state), "private-token") {
		t.Fatal("subscription URL leaked in IPC snapshot")
	}
	name = "Renamed"
	send(ipc.Command{Kind: ipc.CommandPutSubscription, SubscriptionID: "daily", Subscription: &ipc.SubscriptionEdit{Name: &name, Enabled: new(false), RefreshIntervalSeconds: new(uint32(900))}})
	waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool {
		return len(s.Subscriptions) == 1 && s.Subscriptions[0].Name == name && !s.Subscriptions[0].Enabled && len(s.Jobs) == 0
	})
	stored, err := config.Load(configDir)
	if err != nil || len(stored.Subscriptions) != 1 || stored.Subscriptions[0].URL != url || stored.Subscriptions[0].RefreshInterval != 900*time.Second {
		t.Fatalf("stored intent lost source or schedule: %+v, %v", stored.Subscriptions, err)
	}
	send(ipc.Command{Kind: ipc.CommandDeleteSubscription, SubscriptionID: "daily"})
	waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool { return len(s.Subscriptions) == 0 && len(s.Jobs) == 0 })
	stored, err = config.Load(configDir)
	if err != nil || len(stored.Subscriptions) != 0 {
		t.Fatalf("deletion did not persist: %+v, %v", stored.Subscriptions, err)
	}
}
