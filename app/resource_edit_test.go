package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestIPCResourceAndFilterEditsPersistCrossFileIntent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cn.yaml")
	if err := os.WriteFile(source, []byte("payload:\n  - +.example.cn\n"), 0600); err != nil {
		t.Fatal(err)
	}
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
	send := func(cmd ipc.Command) {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		_, err := client.Send(request, cmd)
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	kind, format, ruleType := "rule-set", "yaml", "domain"
	interval := uint32(3600)
	send(ipc.Command{Kind: ipc.CommandPutResource, ResourceID: "cn", Resource: &ipc.ResourceEdit{Kind: &kind, Format: &format, RuleType: &ruleType, URL: &source, Enabled: new(true), IntervalSeconds: &interval}})
	waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool {
		for _, resource := range s.Resources {
			if resource.ID == "cn" && resource.Enabled {
				return true
			}
		}
		return false
	})
	target := "Proxy"
	send(ipc.Command{Kind: ipc.CommandPutFilter, FilterID: "ads", Filter: &ipc.FilterEdit{ResourceID: new("cn"), Format: &format, Target: &target, Enabled: new(true)}})
	state := waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool { return len(s.Filters) == 1 && s.Filters[0].ID == "ads" })
	if state.Filters[0].Format != "yaml" {
		t.Fatalf("filter format is absent from client snapshot: %+v", state.Filters[0])
	}
	stored, err := config.Load(configDir)
	if err != nil || len(stored.Filters) != 1 || stored.Filters[0].Resource != "cn" {
		t.Fatalf("filter intent not persisted: %+v, %v", stored.Filters, err)
	}
	found := false
	for _, resource := range stored.Resources {
		if resource.ID == "cn" {
			found = resource.Enabled && resource.URL == source && resource.Kind == config.ResourceRuleSet && resource.Format == config.FormatYAML
		} else if resource.Enabled {
			t.Fatalf("unrelated catalog source was enabled: %+v", resource)
		}
	}
	if !found {
		t.Fatalf("managed resource edit not persisted: %+v", stored.Resources)
	}
	send(ipc.Command{Kind: ipc.CommandPutFilter, FilterID: "ads", Filter: &ipc.FilterEdit{ResourceID: new("unknown")}})
	state = waitAppSnapshot(t, ctx, client, func(s core.Snapshot) bool {
		return len(s.Errors) != 0 && s.Errors[0].File == "filters.toml" && s.Errors[0].Key == "filter.resource" && len(s.Jobs) == 0
	})
	if len(state.Filters) != 1 || state.Filters[0].ResourceID != "cn" {
		t.Fatalf("invalid filter edit changed active config: %+v", state.Filters)
	}
}
