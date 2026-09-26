package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/examples"
	"github.com/fishman/clashpulse/ipc"
)

func TestRunAtSeedsPrivateConfigWithoutReplacingUserEdits(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	endpoint := filepath.Join(root, "socket", "ipc.sock")
	start := func() {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
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
		client.Close()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	start()
	for _, name := range []string{"config.toml", "subscriptions.toml", "resources.toml", "filters.toml"} {
		path := filepath.Join(configDir, name)
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			t.Fatalf("startup did not seed %s: %v", name, err)
		}
		seed, err := examples.Files.ReadFile(name)
		if err != nil || !bytes.Equal(data, seed) {
			t.Fatalf("%s differs from annotated example: %v", name, err)
		}
		if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("seed %s is not private: %v, %v", name, info, err)
		}
	}
	snapshot, err := config.Load(configDir)
	if err != nil || len(snapshot.Subscriptions) != 0 || len(snapshot.Filters) != 0 || len(snapshot.Resources) != 4 {
		t.Fatalf("seed catalog is incomplete: %+v, %v", snapshot, err)
	}
	if snapshot.Monitor.TestURL != "http://cp.cloudflare.com/generate_204" {
		t.Fatalf("seeded latency URL = %q", snapshot.Monitor.TestURL)
	}
	want := map[string]string{
		"geoip":   "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat",
		"geosite": "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat",
		"country": "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb",
		"cn":      "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/cn.mrs",
	}
	for _, resource := range snapshot.Resources {
		if !resource.Enabled || resource.URL != want[resource.ID] || resource.Interval != 24*time.Hour {
			t.Fatalf("default resource is disabled or unexpected: %+v", resource)
		}
		if resource.ID == "cn" && (resource.Kind != config.ResourceRuleSet || resource.Format != config.FormatMRS || resource.RuleType != config.RuleDomain) {
			t.Fatalf("CN source cannot generate a domain MRS rule-set: %+v", resource)
		}
		delete(want, resource.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing geodata sources: %+v", want)
	}
	custom := []byte("[mihomo]\nbinary = \"system\"\n# user-preserved\n")
	if err := config.Write(filepath.Join(configDir, "config.toml"), custom); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(configDir, "filters.toml")); err != nil {
		t.Fatal(err)
	}
	start()
	got, err := os.ReadFile(filepath.Join(configDir, "config.toml"))
	if err != nil || string(got) != string(custom) {
		t.Fatalf("restart replaced user config: %q, %v", got, err)
	}
	filter, err := os.ReadFile(filepath.Join(configDir, "filters.toml"))
	if err != nil {
		t.Fatal(err)
	}
	seed, err := examples.Files.ReadFile("filters.toml")
	if err != nil || !bytes.Equal(filter, seed) {
		t.Fatalf("restart did not restore missing optional seed: %v", err)
	}
}
