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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestRunAtRefreshActivateSelectAndStop(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake Mihomo executable requires POSIX shell")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable for fake controller")
	}
	root := t.TempDir()
	binary := fakeAppMihomo(t, root, python)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxies:\n  - name: node-a\n    type: direct\n  - name: node-b\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a, node-b]\n"))
	}))
	defer server.Close()
	configDir := filepath.Join(root, "config")
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = true\n", binary))); err != nil {
		t.Fatal(err)
	}
	secretURL := server.URL + "/subscription?token=short-lived-secret"
	subscriptionsTOML := fmt.Sprintf("[[subscription]]\nid = \"daily\"\nurl = %q\nenabled = true\nallow_http = true\n", secretURL)
	if err := config.Write(filepath.Join(configDir, "subscriptions.toml"), []byte(subscriptionsTOML)); err != nil {
		t.Fatal(err)
	}
	resourceFile := filepath.Join(root, "ads.yaml")
	if err := os.WriteFile(resourceFile, []byte("payload:\n  - example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resourcesTOML := fmt.Sprintf("[[resource]]\nid = \"ads\"\nkind = \"rule-set\"\nformat = \"yaml\"\nrule_type = \"domain\"\nurl = %q\nenabled = true\n", resourceFile)
	if err := config.Write(filepath.Join(configDir, "resources.toml"), []byte(resourcesTOML)); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "filters.toml"), []byte("[[filter]]\nid = \"ads\"\nresource = \"ads\"\nformat = \"yaml\"\ntarget = \"select-main\"\nenabled = true\n")); err != nil {
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
	command := func(kind ipc.CommandKind, group, choice string) {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		_, err := client.Send(request, ipc.Command{Kind: kind, SubscriptionID: map[ipc.CommandKind]string{ipc.CommandRefreshSubscription: "daily", ipc.CommandActivateSubscription: "daily"}[kind], ResourceID: map[ipc.CommandKind]string{ipc.CommandRefreshResource: "ads"}[kind], GroupID: group, ChoiceID: choice})
		stop()
		if err != nil {
			t.Fatal(err)
		}
	}
	command(ipc.CommandRefreshSubscription, "", "")
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Subscriptions) == 1 && state.Subscriptions[0].HashPrefix != "" && len(state.Jobs) == 0
	})
	command(ipc.CommandActivateSubscription, "", "")
	state := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && len(state.Subscriptions) == 1 && state.Subscriptions[0].Active && len(state.Jobs) == 0
	})
	if state.Groups[0].Selected != opaqueID("node-a") || state.Groups[0].ID != opaqueID("select-main") {
		t.Fatalf("initial selection = %+v", state.Groups)
	}
	if state.Groups[0].Label != "select-main" {
		t.Fatalf("safe group label missing: %+v", state.Groups)
	}
	foundLabel := false
	for _, proxy := range state.Proxies {
		if proxy.ID == opaqueID("node-a") {
			foundLabel = proxy.Label == "node-a"
		}
	}
	if !foundLabel {
		t.Fatalf("safe proxy label missing: %+v", state.Proxies)
	}
	command(ipc.CommandManualProbe, opaqueID("select-main"), "")
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		for _, proxy := range state.Proxies {
			if proxy.GroupID == opaqueID("select-main") && proxy.ID == opaqueID("node-a") && proxy.Outcome == "success" && proxy.LatencyMillis == 180 {
				return true
			}
		}
		return false
	})
	if strings.Contains(fmt.Sprintf("%+v", state), "short-lived-secret") {
		t.Fatal("subscription URL escaped into IPC snapshot")
	}
	if len(state.Resources) != 1 || !state.Resources[0].Validated {
		t.Fatalf("managed resource not active: %+v", state.Resources)
	}
	originalBinary, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	updatedBinary := strings.Replace(string(originalBinary), "v1.19.31 linux amd64'", "v1.19.31 linux amd64 rebuilt'", 1)
	if err := os.WriteFile(binary, []byte(updatedBinary), 0700); err != nil {
		t.Fatal(err)
	}
	command(ipc.CommandReloadConfiguration, "", "")
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return strings.Contains(state.Binary.ObservedVersion, "rebuilt") && len(state.Jobs) == 0
	})
	if err := os.WriteFile(resourceFile, []byte("not-a-rule-provider"), 0600); err != nil {
		t.Fatal(err)
	}
	command(ipc.CommandRefreshResource, "", "")
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Errors) != 0 && state.Errors[0].Key == string(ipc.CommandRefreshResource) && len(state.Jobs) == 0
	})
	if !state.Resources[0].Validated || len(state.Groups) != 1 {
		t.Fatalf("bad resource update destroyed known-good runtime: %+v", state)
	}
	auto := ipc.AutomationSetting{Enabled: true}
	request, stop := context.WithTimeout(ctx, time.Second)
	_, err = client.Send(request, ipc.Command{Kind: ipc.CommandSetAutomation, GroupID: opaqueID("select-main"), Automation: &auto})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && state.Groups[0].AutomationEnabled && len(state.Jobs) == 0
	})
	command(ipc.CommandSelectGroup, opaqueID("select-main"), opaqueID("node-b"))
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && len(state.Jobs) == 0
	})
	if state.Groups[0].AutomationEnabled {
		t.Fatal("manual selection did not disable automation")
	}
	persisted, err := config.Load(configDir)
	if err != nil || len(persisted.Monitor.AutomatedGroups) != 0 {
		t.Fatalf("manual override was not persisted: %+v, %v", persisted.Monitor, err)
	}
	command(ipc.CommandRestart, "", "")
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && len(state.Jobs) == 0
	})
	if err := os.WriteFile(resourceFile, []byte("payload:\n  - example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	candidateBinary := filepath.Join(root, "mihomo-new")
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidateBinary, binaryBytes, 0700); err != nil {
		t.Fatal(err)
	}
	badGroup := strings.Repeat("b", 64)
	badConfig := fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = true\nautomated_groups = [%q]\n", candidateBinary, badGroup)
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(badConfig)); err != nil {
		t.Fatal(err)
	}
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return slices.ContainsFunc(state.Errors, func(issue core.ErrorSnapshot) bool {
			return issue.File == "config.toml" && issue.Key == "monitor.automated_groups"
		})
	})
	if len(state.Groups) != 1 || state.Groups[0].Selected != opaqueID("node-b") {
		t.Fatalf("invalid automation destroyed prior runtime: %+v", state.Groups)
	}
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n[monitor]\nenabled = true\n", binary))); err != nil {
		t.Fatal(err)
	}
	rejectedBinary := filepath.Join(root, "mihomo-reject")
	if err := os.WriteFile(rejectedBinary, []byte("#!/bin/sh\nif [ \"$1\" = -v ]; then echo 'Mihomo Meta v1.19.31'; exit 0; fi\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	request, stop = context.WithTimeout(ctx, time.Second)
	_, err = client.Send(request, ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{Binary: &rejectedBinary}})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Jobs) == 0 && slices.ContainsFunc(state.Errors, func(issue core.ErrorSnapshot) bool {
			return issue.Kind == string(ipc.CommandUpdateConfiguration)
		})
	})
	if state.Binary.Desired != binary || len(state.Groups) != 1 || state.Groups[0].Selected != opaqueID("node-b") {
		t.Fatalf("failed binary switch changed known-good runtime: %+v", state)
	}
	retained, err := config.Load(configDir)
	if err != nil || retained.Mihomo.Binary != binary {
		t.Fatalf("failed binary switch changed config file: %q, %v", retained.Mihomo.Binary, err)
	}
	request, stop = context.WithTimeout(ctx, time.Second)
	_, err = client.Send(request, ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{Binary: &candidateBinary}})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return state.Binary.Desired == candidateBinary && len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && len(state.Jobs) == 0
	})
	selected, err := config.Load(configDir)
	if err != nil || selected.Mihomo.Binary != candidateBinary {
		t.Fatalf("binary switch was not persisted: %q, %v", selected.Mihomo.Binary, err)
	}
	command(ipc.CommandStop, "", "")
	waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool { return len(state.Groups) == 0 && len(state.Jobs) == 0 })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not stop")
	}
	restartCtx, restartCancel := context.WithCancel(context.Background())
	defer restartCancel()
	restartedDone := make(chan error, 1)
	go func() { restartedDone <- RunAt(restartCtx, configDir, filepath.Join(root, "state"), endpoint) }()
	var restarted *ipc.Client
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		attempt, stop := context.WithTimeout(restartCtx, 100*time.Millisecond)
		restarted, _ = ipc.Dial(attempt, endpoint)
		stop()
		if restarted != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if restarted == nil {
		restartCancel()
		t.Fatalf("restarted service did not listen: %v", <-restartedDone)
	}
	defer restarted.Close()
	request, stop = context.WithTimeout(restartCtx, time.Second)
	_, err = restarted.Send(request, ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: "daily"})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	state = waitAppSnapshot(t, restartCtx, restarted, func(state core.Snapshot) bool {
		return len(state.Groups) == 1 && len(state.Subscriptions) == 1 && state.Subscriptions[0].Active && len(state.Jobs) == 0
	})
	if state.Groups[0].Selected != opaqueID("node-a") {
		t.Fatalf("restarted controller did not select the active profile: %+v", state.Groups)
	}
	restartCancel()
	select {
	case err := <-restartedDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restarted service did not stop")
	}
}

func TestDownloadedProfileActivatesAfterDesktopStarts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake Mihomo executable requires POSIX shell")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable for fake controller")
	}
	root := t.TempDir()
	binary := fakeAppMihomo(t, root, python)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("proxies:\n  - name: node-a\n    type: direct\n  - name: node-b\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a, node-b]\n"))
	}))
	defer server.Close()
	configDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "state")
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte(fmt.Sprintf("[mihomo]\nbinary = %q\n", binary))); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "subscriptions.toml"), []byte(fmt.Sprintf("[[subscription]]\nid = \"daily\"\nurl = %q\nenabled = true\nallow_http = true\n", server.URL+"/profile?token=private"))); err != nil {
		t.Fatal(err)
	}
	if err := DownloadAtWithOptions(t.Context(), configDir, stateDir, "daily", RefreshOptions{}); err != nil {
		t.Fatalf("offline download: %v", err)
	}

	endpoint := filepath.Join(root, "socket", "ipc.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunAt(ctx, configDir, stateDir, endpoint) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("desktop service: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("desktop service did not stop")
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
		t.Fatal("desktop service did not become ready")
	}
	defer client.Close()
	request, stop := context.WithTimeout(ctx, time.Second)
	_, err = client.Send(request, ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: "daily"})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	state := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return len(state.Subscriptions) == 1 && state.Subscriptions[0].Active && len(state.Groups) == 1 && len(state.Jobs) == 0
	})
	if state.Groups[0].Label != "select-main" {
		t.Fatalf("activated group missing: %+v", state.Groups)
	}
}

func waitAppSnapshot(t *testing.T, ctx context.Context, client *ipc.Client, ready func(core.Snapshot) bool) core.Snapshot {
	t.Helper()
	var state core.Snapshot
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		request, stop := context.WithTimeout(ctx, time.Second)
		var err error
		state, err = client.Snapshot(request)
		stop()
		if err == nil && ready(state) {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected application state not reached: errors=%+v groups=%+v proxies=%+v monitor=%+v", state.Errors, state.Groups, state.Proxies, state.Monitor)
	return state
}

func fakeAppMihomo(t *testing.T, root, python string) string {
	t.Helper()
	server := filepath.Join(root, "fake.py")
	source := `import http.server, json, re, sys
text = open(sys.argv[1], encoding='utf-8').read()
address = re.search(r'^external-controller:\s*(\S+)', text, re.M).group(1)
secret = re.search(r'^secret:\s*(\S+)', text, re.M).group(1)
selected = ['node-a']
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.headers.get('Authorization') != 'Bearer ' + secret:
            self.send_error(401); return
        if self.path.startswith('/proxies/node-a/delay'):
            self.send_response(200); self.end_headers(); self.wfile.write(b'{"delay":180}'); return
        if self.path.startswith('/proxies/node-b/delay'):
            self.send_response(200); self.end_headers(); self.wfile.write(b'{"delay":160}'); return
        if self.path.startswith('/proxies'):
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
`
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
