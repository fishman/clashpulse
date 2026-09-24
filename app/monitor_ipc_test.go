package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestIPCMonitorPolicyPatchPreservesMillisecondTimeout(t *testing.T) {
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
	timeout := uint32(750)
	concurrency := uint32(2)
	cooldown := uint32(0)
	request, stop := context.WithTimeout(ctx, time.Second)
	_, err := client.Send(request, ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{MonitorTimeoutMillis: &timeout, MonitorConcurrency: &concurrency, MonitorCooldownSeconds: &cooldown}})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool {
		return s.Monitor.TimeoutMillis == 750 && s.Monitor.Concurrency == 2 && s.Monitor.CooldownSeconds == 0 && len(s.Jobs) == 0
	})
	if len(snapshot.Errors) != 0 {
		t.Fatalf("monitor policy reported error: %+v", snapshot.Errors)
	}
	stored, err := config.Load(configDir)
	if err != nil || stored.Monitor.Timeout != 750*time.Millisecond || stored.Monitor.Concurrency != 2 || stored.Monitor.Cooldown != 0 {
		t.Fatalf("monitor policy did not persist: %+v, %v", stored.Monitor, err)
	}
}
