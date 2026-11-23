package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/internal/localtest"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
)

// fakeServiceController serves a profile group, two service groups, and
// selection; the members of every group are the profile's nodes.
type fakeServiceController struct {
	mu       sync.Mutex
	nodes    []string
	selected map[string]string
}

var fakeServiceGroups = []string{"main", "ClashPulse - Netflix", "ClashPulse - Claude"}

func serviceProfile(nodes ...string) []string { return nodes }

func (f *fakeServiceController) setNodes(nodes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes = nodes
}

func (f *fakeServiceController) setSelected(group, node string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.selected[group] = node
}

func (f *fakeServiceController) current(group string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.selected[group]
}

func (f *fakeServiceController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/proxies/") {
		var body struct{ Name string }
		data, _ := io.ReadAll(r.Body)
		if json.Unmarshal(data, &body) != nil || !slices.Contains(f.nodes, body.Name) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.selected[strings.TrimPrefix(r.URL.Path, "/proxies/")] = body.Name
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != "/proxies" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	proxies := map[string]any{}
	for _, node := range f.nodes {
		proxies[node] = map[string]any{"name": node, "type": "Direct"}
	}
	for _, group := range fakeServiceGroups {
		now := f.selected[group]
		if !slices.Contains(f.nodes, now) {
			now = f.nodes[0]
			f.selected[group] = now
		}
		proxies[group] = map[string]any{"name": group, "type": "Selector", "all": f.nodes, "now": now}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"proxies": proxies})
}

func newServiceTestRuntime(t *testing.T, nodes []string, configured config.Service) (*runtimeService, *fakeServiceController) {
	t.Helper()
	fake := &fakeServiceController{nodes: nodes, selected: map[string]string{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	configDir := localtest.Dir(t)
	path := filepath.Join(configDir, "config.toml")
	if err := config.Write(path, []byte("")); err != nil {
		t.Fatal(err)
	}
	empty, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.PutService(path, empty, configured); err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRuntimeService(configDir, filepath.Join(configDir, "state"), initial)
	if err != nil {
		t.Fatal(err)
	}
	service.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: localtest.Endpoint(t), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: service.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if service.controller, err = mihomo.NewController(server.URL, "secret", server.Client()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	service.processCtx = ctx
	t.Cleanup(func() {
		cancel()
		service.stopMonitorLoop()
	})
	if err := service.refreshGroups(ctx); err != nil {
		t.Fatal(err)
	}
	return service, fake
}

func findService(items []core.ServiceSnapshot, id string) core.ServiceSnapshot {
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	return core.ServiceSnapshot{}
}

func automaticService(id string) config.Service {
	return config.Service{ID: id, Enabled: true, Automatic: true, CheckInterval: 30 * time.Minute, Failures: 2}
}

func TestServiceSwitchAppliedUnderGuard(t *testing.T) {
	service, controller := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B"), automaticService("netflix"))
	service.applyServiceBatch(context.Background(), monitor.ServiceBatch{Switches: []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "Proxy A", New: "Proxy B", Region: "US", Reason: "blocked"}}})
	if got := controller.current("ClashPulse - Netflix"); got != "Proxy B" {
		t.Fatalf("selected = %q", got)
	}
	if snap := findService(service.snapshot.Services, "netflix"); snap.LastSwitch != "blocked" || snap.Selected == "" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if !service.store.Snapshot().Services[0].Automatic {
		t.Fatal("ClashPulse's own switch counted as a manual pick")
	}
	// A stale switch from a node no longer selected is refused.
	service.applyServiceBatch(context.Background(), monitor.ServiceBatch{Switches: []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "Proxy A", New: "Proxy A", Reason: "blocked"}}})
	if got := controller.current("ClashPulse - Netflix"); got != "Proxy B" {
		t.Fatalf("stale switch applied: %q", got)
	}
}

func TestServiceSwitchRejectedAfterManualPick(t *testing.T) {
	service, controller := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B", "Proxy C"), automaticService("netflix"))
	groupID := opaqueID("ClashPulse - Netflix")
	if err := service.execute(context.Background(), ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: groupID, ChoiceID: opaqueID("Proxy C")}); err != nil {
		t.Fatal(err)
	}
	if service.store.Snapshot().Services[0].Automatic {
		t.Fatal("manual pick did not turn automatic off")
	}
	service.applyServiceBatch(context.Background(), monitor.ServiceBatch{Switches: []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "Proxy C", New: "Proxy B", Reason: "blocked"}}})
	if got := controller.current("ClashPulse - Netflix"); got != "Proxy C" {
		t.Fatalf("manual pick overwritten: %q", got)
	}
}

func TestServiceCommandsPersistAndRequest(t *testing.T) {
	service, _ := newServiceTestRuntime(t, serviceProfile("Proxy A"), config.Service{ID: "claude", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2})
	edit := &ipc.ServiceEdit{ID: "claude", Enabled: true, Automatic: true, Regions: []string{"US"}, IntervalSeconds: 3600, Failures: 3}
	if err := service.execute(context.Background(), ipc.Command{Kind: ipc.CommandSetService, Service: edit}); err != nil {
		t.Fatal(err)
	}
	got := service.store.Snapshot().Services[0]
	if !got.Automatic || got.CheckInterval != time.Hour || got.Failures != 3 || !slices.Equal(got.Regions, []string{"US"}) {
		t.Fatalf("persisted = %+v", got)
	}
	if snap := findService(service.snapshot.Services, "claude"); !snap.Automatic || snap.IntervalSeconds != 3600 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if err := service.execute(context.Background(), ipc.Command{Kind: ipc.CommandSetAutomation, GroupID: opaqueID("ClashPulse - Claude"), Automation: &ipc.AutomationSetting{Enabled: true}}); err == nil {
		t.Fatal("group automation accepted on a service group")
	}
	if err := service.execute(context.Background(), ipc.Command{Kind: ipc.CommandCheckService, ServiceID: "netflix"}); err == nil {
		t.Fatal("check accepted for a service that is not enabled")
	}
}

func TestServiceDiagnosticsCarryNoNodeNames(t *testing.T) {
	service, _ := newServiceTestRuntime(t, serviceProfile("node-secret.example.com", "Proxy B"), automaticService("netflix"))
	service.applyServiceBatch(context.Background(), monitor.ServiceBatch{
		Checks:    []monitor.ServiceCheck{{Service: "netflix", Proxy: "node-secret.example.com", At: time.Now()}},
		Switches:  []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "node-secret.example.com", New: "Proxy B", Reason: "blocked"}},
		Exhausted: []string{"netflix"},
	})
	if len(service.snapshot.Diagnostics) < 2 {
		t.Fatalf("diagnostics = %+v", service.snapshot.Diagnostics)
	}
	for _, entry := range service.snapshot.Diagnostics {
		if strings.Contains(entry.Message, "example.com") || strings.Contains(entry.SourceID, "example.com") {
			t.Fatalf("diagnostic leaks a node name: %+v", entry)
		}
	}
	service.applyServiceBatch(context.Background(), monitor.ServiceBatch{Switches: []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "Proxy B", New: "node-secret.example.com", Reason: "blocked"}}})
	if svc := findService(service.snapshot.Services, "netflix"); svc.Selected == "" || strings.Contains(svc.Selected, "example.com") {
		t.Fatalf("snapshot label = %+v", svc)
	}
}

func TestServiceGroupsOutsideLatencyAutomation(t *testing.T) {
	service, _ := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B"), config.Service{ID: "netflix", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2})
	if err := service.execute(context.Background(), ipc.Command{Kind: ipc.CommandSetAutoFastest, Automation: &ipc.AutomationSetting{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	automated := service.store.Snapshot().Monitor.AutomatedGroups
	if !slices.Contains(automated, opaqueID("main")) || slices.Contains(automated, opaqueID("ClashPulse - Netflix")) {
		t.Fatalf("automated groups = %v", automated)
	}
	if !service.stateSnapshot().Monitor.AutoFastest {
		t.Fatal("a service group keeps auto-fastest off")
	}
	for _, name := range service.monitor.ProbedGroups() {
		if name == "ClashPulse - Netflix" {
			t.Fatal("service group is delay-probed by the latency monitor")
		}
	}
}

func TestServiceAutomaticSurvivesRestartWithRemovedNode(t *testing.T) {
	service, controller := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B"), automaticService("netflix"))
	controller.setSelected("ClashPulse - Netflix", "Proxy B")
	if err := service.refreshGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.store.Snapshot().Services[0].Automatic {
		t.Fatal("an external pick did not turn automatic off")
	}
	if err := service.setService(context.Background(), &ipc.ServiceEdit{ID: "netflix", Enabled: true, Automatic: true, IntervalSeconds: 1800, Failures: 2}); err != nil {
		t.Fatal(err)
	}
	// A subscription refresh drops Proxy B; after the restart Mihomo falls back
	// to the first member, which is not a pick.
	controller.setNodes([]string{"Proxy A", "Proxy C"})
	clear(service.serviceObserved)
	if err := service.refreshGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.store.Snapshot().Services[0].Automatic {
		t.Fatal("a fallback after restart turned automatic off")
	}
	// Between restarts, a fallback from a removed node is not a pick either.
	controller.setNodes([]string{"Proxy C"})
	if err := service.refreshGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.store.Snapshot().Services[0].Automatic {
		t.Fatal("a fallback from a removed node turned automatic off")
	}
}
