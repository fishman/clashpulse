package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/ipc"
)

func TestRunAtPublishesConfigChangesOverLocalIPC(t *testing.T) {
	root := t.TempDir()
	configDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "state")
	endpoint := filepath.Join(root, "socket", "ipc.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunAt(ctx, configDir, stateDir, endpoint) }()

	var client *ipc.Client
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
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
	request, stop := context.WithTimeout(ctx, time.Second)
	snapshot, err := client.Snapshot(request)
	stop()
	if err != nil || snapshot.Monitor.AlertThresholdMillis != 250 {
		t.Fatalf("initial monitor snapshot = %+v, %v", snapshot.Monitor, err)
	}
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte("[monitor]\nalert_threshold = \"350ms\"\n")); err != nil {
		t.Fatal(err)
	}
	waitMonitorThreshold(t, ctx, client, 350)
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte("[monitor]\nalert_threshold = \"-1ms\"\n")); err != nil {
		t.Fatal(err)
	}
	waitConfigError(t, ctx, client, "config.toml")
	request, stop = context.WithTimeout(ctx, time.Second)
	unchanged, err := client.Snapshot(request)
	stop()
	if err != nil || unchanged.Monitor.AlertThresholdMillis != 350 {
		t.Fatalf("invalid edit changed live settings: %+v, %v", unchanged.Monitor, err)
	}
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte("[monitor]\nalert_threshold = \"350ms\"\n")); err != nil {
		t.Fatal(err)
	}
	value := uint32(400)
	request, stop = context.WithTimeout(ctx, time.Second)
	_, err = client.Send(request, ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{AlertThresholdMillis: &value}})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	waitMonitorThreshold(t, ctx, client, 400)
	data, err := os.ReadFile(filepath.Join(configDir, "config.toml"))
	if err != nil || len(data) == 0 {
		t.Fatalf("atomic user config write = %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not shut down")
	}
}

func waitMonitorThreshold(t *testing.T, ctx context.Context, client *ipc.Client, expected int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		request, stop := context.WithTimeout(ctx, time.Second)
		snapshot, err := client.Snapshot(request)
		stop()
		if err == nil && snapshot.Monitor.AlertThresholdMillis == expected {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("monitor threshold did not reach %d ms", expected)
}

func waitConfigError(t *testing.T, ctx context.Context, client *ipc.Client, file string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		request, stop := context.WithTimeout(ctx, time.Second)
		snapshot, err := client.Snapshot(request)
		stop()
		if err == nil && len(snapshot.Errors) != 0 && snapshot.Errors[0].File == file {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("invalid config error for %s was not published", file)
}

func TestReserveLoopbackPortsDistinct(t *testing.T) {
	controller, proxy, err := reserveLoopbackPorts()
	if err != nil {
		t.Fatal(err)
	}
	if controller == proxy || controller == 0 || proxy == 0 {
		t.Fatalf("ports overlap: controller %d proxy %d", controller, proxy)
	}
}
