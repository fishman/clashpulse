package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/subscriptions"
)

func TestFailedMixedReloadPreservesInactiveSubscriptionSnapshot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake Mihomo executable requires POSIX shell")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable for fake controller")
	}
	root := t.TempDir()
	binary := fakeAppMihomo(t, root, python)
	profile := []byte("proxies:\n  - name: node-a\n    type: direct\n  - name: node-b\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a, node-b]\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(profile) }))
	defer server.Close()
	configDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "state")
	configPath := filepath.Join(configDir, "config.toml")
	subscriptionsPath := filepath.Join(configDir, "subscriptions.toml")
	writeConfig := func(binaryPath string) {
		t.Helper()
		if err := config.Write(configPath, []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = true\n", binaryPath))); err != nil {
			t.Fatal(err)
		}
	}
	writeSubscriptions := func(includeRetired bool) {
		t.Helper()
		value := fmt.Sprintf("[[subscription]]\nid = \"daily\"\nurl = %q\nenabled = true\nallow_http = true\n", server.URL+"/daily")
		if includeRetired {
			value += fmt.Sprintf("\n[[subscription]]\nid = \"retired\"\nurl = %q\nenabled = false\nallow_http = true\n", server.URL+"/retired")
		}
		if err := config.Write(subscriptionsPath, []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(binary)
	writeSubscriptions(true)
	endpoint := filepath.Join(root, "socket", "ipc.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunAt(ctx, configDir, stateDir, endpoint) }()
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
	command := func(command ipc.Command) {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		_, err := client.Send(request, command)
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	command(ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: "retired"})
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		for _, item := range state.Subscriptions {
			if item.ID == "retired" {
				return item.HashPrefix != "" && len(state.Jobs) == 0
			}
		}
		return false
	})
	command(ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: "daily"})
	command(ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: "daily"})
	command(ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: opaqueID("select-main"), ChoiceID: opaqueID("node-b")})
	prior := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && len(state.Jobs) == 0
	})
	generatedPath := filepath.Join(stateDir, "generated.yaml")
	generatedBefore, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}

	rejectedBinary := filepath.Join(root, "mihomo-reject")
	if err := os.WriteFile(rejectedBinary, []byte("#!/bin/sh\nif [ \"$1\" = -v ]; then echo 'Mihomo Meta v1.19.31'; exit 0; fi\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	writeConfig(rejectedBinary)
	writeSubscriptions(false)
	candidate, err := config.Load(configDir)
	if err != nil || len(candidate.Subscriptions) != 1 {
		t.Fatalf("candidate files do not remove retired subscription: %+v, %v", candidate.Subscriptions, err)
	}
	command(ipc.Command{Kind: ipc.CommandReloadConfiguration})
	state := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Errors) != 0 && state.Errors[0].Key == string(ipc.CommandReloadConfiguration) && len(state.Jobs) == 0
	})
	if state.Binary.Desired != binary || state.Binary.ObservedVersion != prior.Binary.ObservedVersion || len(state.Groups) != 1 || state.Groups[0].Selected != opaqueID("node-b") {
		t.Fatalf("failed mixed reload changed runtime: %+v", state)
	}
	foundRetired := false
	for _, item := range state.Subscriptions {
		if item.ID == "retired" {
			foundRetired = item.HashPrefix != ""
		}
	}
	if !foundRetired {
		t.Fatalf("failed mixed reload lost inactive subscription snapshot: %+v", state.Subscriptions)
	}
	generatedAfter, err := os.ReadFile(generatedPath)
	if err != nil || string(generatedAfter) != string(generatedBefore) {
		t.Fatalf("failed mixed reload changed generated config: %v", err)
	}
	retained, err := subscriptions.NewStore(filepath.Join(stateDir, "subscriptions"))
	if err != nil {
		t.Fatal(err)
	}
	retainedProfile, err := retained.Profile("retired")
	if err != nil || string(retainedProfile) != string(profile) {
		t.Fatalf("failed mixed reload lost last-known-good source: %q, %v", retainedProfile, err)
	}

	candidateDir := filepath.Join(root, "candidate")
	if err := os.Mkdir(candidateDir, 0700); err != nil {
		t.Fatal(err)
	}
	candidateBinary := fakeAppMihomo(t, candidateDir, python)
	writeConfig(candidateBinary)
	writeSubscriptions(false)
	command(ipc.Command{Kind: ipc.CommandReloadConfiguration})
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return state.Binary.Desired == candidateBinary && len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && len(state.Subscriptions) == 1 && len(state.Jobs) == 0
	})
	if state.Subscriptions[0].ID != "daily" {
		t.Fatalf("successful reload did not remove the retired subscription: %+v", state.Subscriptions)
	}
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

func TestFailedPostRestartRefreshRestoresRuntimeState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake Mihomo executable requires POSIX shell")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable for fake controller")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "fail-third-proxy-read")
	binary := fakeAppMihomoFailingThirdProxyRead(t, root, python, marker)
	candidateBinary := filepath.Join(root, "mihomo-candidate")
	if err := os.WriteFile(candidateBinary, []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = -v ]; then echo 'Mihomo Meta v1.19.32 linux amd64'; exit 0; fi\nexec %q \"$@\"\n", binary)), 0700); err != nil {
		t.Fatal(err)
	}
	profile := []byte("proxies:\n  - name: node-a\n    type: direct\n  - name: node-b\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a, node-b]\n")
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(profile) }))
	defer source.Close()
	configDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "state")
	configPath := filepath.Join(configDir, "config.toml")
	if err := config.Write(configPath, []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = true\n", binary))); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "subscriptions.toml"), []byte(fmt.Sprintf("[[subscription]]\nid = \"daily\"\nurl = %q\nenabled = true\nallow_http = true\n", source.URL))); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(root, "socket", "ipc.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunAt(ctx, configDir, stateDir, endpoint) }()
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
	command := func(command ipc.Command) {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		_, err := client.Send(request, command)
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	command(ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: "daily"})
	command(ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: "daily"})
	command(ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: opaqueID("select-main"), ChoiceID: opaqueID("node-b")})
	prior := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && len(state.Jobs) == 0
	})
	generatedPath := filepath.Join(stateDir, "generated.yaml")
	generatedBefore, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(configPath, []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = false\n", candidateBinary))); err != nil {
		t.Fatal(err)
	}
	command(ipc.Command{Kind: ipc.CommandReloadConfiguration})
	state := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Errors) != 0 && state.Errors[0].Key == string(ipc.CommandReloadConfiguration) && len(state.Jobs) == 0
	})
	if state.Binary.Desired != binary || state.Binary.ObservedVersion != prior.Binary.ObservedVersion || state.Monitor.Enabled != prior.Monitor.Enabled || len(state.Groups) != 1 || state.Groups[0].Selected != opaqueID("node-b") {
		t.Fatalf("failed post-restart reload changed runtime: before=%+v after=%+v", prior, state)
	}
	generatedAfter, err := os.ReadFile(generatedPath)
	if err != nil || string(generatedAfter) != string(generatedBefore) {
		t.Fatalf("failed post-restart reload changed generated config: %v", err)
	}
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

func fakeAppMihomoFailingThirdProxyRead(t *testing.T, root, python, marker string) string {
	t.Helper()
	server := filepath.Join(root, "fake-failure.py")
	source := fmt.Sprintf(`import http.server, json, os, re, sys
text = open(sys.argv[1], encoding='utf-8').read()
address = re.search(r'^external-controller:\s*(\S+)', text, re.M).group(1)
secret = re.search(r'^secret:\s*(\S+)', text, re.M).group(1)
marker = %q
proxy_reads = 0
selected = ['node-a']
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        global proxy_reads
        if self.headers.get('Authorization') != 'Bearer ' + secret:
            self.send_error(401); return
        if self.path.startswith('/proxies/node-a/delay'):
            self.send_response(200); self.end_headers(); self.wfile.write(b'{"delay":180}'); return
        if self.path.startswith('/proxies/node-b/delay'):
            self.send_response(200); self.end_headers(); self.wfile.write(b'{"delay":160}'); return
        if self.path.startswith('/proxies'):
            proxy_reads += 1
            if os.path.exists(marker) and proxy_reads == 4:
                self.send_error(503); return
            payload = {'proxies': {'select-main': {'name': 'select-main', 'type': 'Selector', 'all': ['node-a','node-b'], 'now': selected[0]}, 'node-a': {'name':'node-a', 'type':'Direct'}, 'node-b': {'name':'node-b', 'type':'Direct'}}}
            self.send_response(200); self.end_headers(); self.wfile.write(json.dumps(payload).encode()); return
        self.send_error(404)
    def do_PUT(self):
        if self.headers.get('Authorization') != 'Bearer ' + secret:
            self.send_error(401); return
        if self.path == '/proxies/select-main':
            selected[0] = json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))))['name']
            self.send_response(204); self.end_headers(); return
        self.send_error(404)
    def log_message(self, *args): pass
http.server.HTTPServer(('127.0.0.1', int(address.rsplit(':',1)[1])), Handler).serve_forever()
`, marker)
	if err := os.WriteFile(server, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "mihomo")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n -v) echo 'Mihomo Meta v1.19.31 linux amd64'; exit 0;;\n -t) exit 0;;\n -f) exec %q %q \"$2\";;\nesac\nexit 1\n", python, server)
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return binary
}
