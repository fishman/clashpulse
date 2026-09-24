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

func TestIPCDNSRoutingIntentPersistsAndPublishes(t *testing.T) {
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
	command := ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: &ipc.DNSRoutingEdit{
		ResolverSets: []ipc.DNSResolverSet{{ID: "domestic", Endpoints: []string{"udp://127.0.0.1:5353"}}},
		Routes:       []ipc.DNSRoute{{Suffix: "example.cn", ResolverSet: "domestic"}},
	}}
	request, stop := context.WithTimeout(ctx, time.Second)
	_, err := client.Send(request, command)
	stop()
	if err != nil {
		t.Fatal(err)
	}
	state := waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool {
		return len(s.DNS.ResolverSets) == 1 && len(s.DNS.Routes) == 1 && s.DNS.Routes[0].Suffix == "example.cn" && len(s.Jobs) == 0
	})
	if state.DNS.ResolverSets[0].ID != "domestic" || state.DNS.Listen == "" {
		t.Fatalf("DNS policy missing: %+v", state.DNS)
	}
	stored, err := config.Load(configDir)
	if err != nil || len(stored.DNS.Routes) != 1 || stored.DNS.Routes[0].Suffix != "example.cn" {
		t.Fatalf("DNS policy not persisted: %+v, %v", stored.DNS, err)
	}
}
