package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
)

func TestNewStreamListenerIsPrivateAndFresh(t *testing.T) {
	first, err := newStreamListener()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newStreamListener()
	if err != nil {
		t.Fatal(err)
	}
	if first.Port <= 0 || len(first.Username) < 16 || len(first.Password) < 32 || first == second {
		t.Fatalf("listeners = %+v %+v", first, second)
	}
}

func TestAcceptStreamBatchFillsProxiesAndLogsInterruptions(t *testing.T) {
	service := newBareRuntimeForTest(t)
	service.snapshot.Proxies = []core.ProxySnapshot{
		{ID: opaqueID("node-a"), GroupID: opaqueID("one"), Label: "node-a"},
		{ID: opaqueID("node-a"), GroupID: opaqueID("two"), Label: "node-a"},
	}
	at := time.Unix(1000, 0)
	service.acceptBatch(context.Background(), monitor.Batch{
		FinishedAt:      at,
		Streams:         []monitor.StreamSample{{Proxy: "node-a", FinishedAt: at, Outcome: monitor.StreamReset}},
		StreamStability: []monitor.StreamStability{{Proxy: "node-a", Samples: 5, Completed: 3, MedianThroughput: 2048000}},
	})
	for _, proxy := range service.snapshot.Proxies {
		if proxy.StreamSamples != 5 || proxy.StreamCompleted != 3 || proxy.StreamKBps != 2000 {
			t.Fatalf("proxy row = %+v", proxy)
		}
	}
	found := false
	for _, entry := range service.snapshot.Diagnostics {
		if entry.Kind == "stream_probe" && entry.Severity == "info" && entry.SourceID == opaqueID("node-a") && entry.Message == "stream.outcome.reset" {
			found = true
		}
		if strings.Contains(entry.Message, "node-a") {
			t.Fatal("diagnostic leaks the node name")
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", service.snapshot.Diagnostics)
	}
}

func TestSteadyStreamSnapshotHasNoProbeCredentials(t *testing.T) {
	service := newBareRuntimeForTest(t)
	service.streamProbe = mihomo.StreamListener{Port: 1, Username: "probe-user-secret", Password: "probe-password-secret"}
	data := fmt.Sprintf("%+v", service.stateSnapshot())
	if strings.Contains(data, "probe-user-secret") || strings.Contains(data, "probe-password-secret") {
		t.Fatal("snapshot carries probe credentials")
	}
}

// The listener comes from the generated file being started, not from the last
// render, and a conflict forces the next render onto a new port.
func TestStartProcessRefusesBusyStreamProbePort(t *testing.T) {
	service := newBareRuntimeForTest(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	service.streamProbe = mihomo.StreamListener{Port: freeLoopbackPort(t), Username: "stale", Password: "stale"}
	path := filepath.Join(t.TempDir(), "generated.yaml")
	generated := fmt.Sprintf("mixed-port: %d\nlisteners:\n  - {name: %s, type: mixed, listen: 127.0.0.1, port: %d, proxy: %s, users: [{username: user, password: password}]}\n",
		freeLoopbackPort(t), mihomo.StreamProbeName, busy.Addr().(*net.TCPAddr).Port, mihomo.StreamProbeName)
	if err := os.WriteFile(path, []byte(generated), 0o600); err != nil {
		t.Fatal(err)
	}
	err = service.startProcess(context.Background(), mihomo.Capability{}, service.registry.Home(), path)
	var activation *core.ActivationError
	if !errors.As(err, &activation) || activation.Stage != core.ActivationStreamProbePort {
		t.Fatalf("busy stream probe port error = %v", err)
	}
	if service.process.Exited() != nil || service.controller != nil {
		t.Fatal("a Mihomo process started despite the busy stream probe port")
	}
	if service.streamProbe != (mihomo.StreamListener{}) {
		t.Fatal("a conflicting stream probe listener survives for the next render")
	}
}

// Identical intents must render identical bytes, or every reload restarts Mihomo.
func TestSteadyStreamRendersReuseTheListener(t *testing.T) {
	service := newBareRuntimeForTest(t)
	intent := service.store.Snapshot()
	intent.Monitor.SwitchPolicy = monitor.SwitchSteadyStream
	home := t.TempDir()
	profile := []byte("proxies:\n  - {name: node-a, type: direct}\n")
	capability := mihomo.Capability{SupportsPinnedListener: true}
	first, err := service.renderWithHome(context.Background(), profile, intent, home, nil, capability)
	if err != nil {
		t.Fatal(err)
	}
	listener := service.streamProbe
	second, err := service.renderWithHome(context.Background(), profile, intent, home, nil, capability)
	if err != nil {
		t.Fatal(err)
	}
	if listener.Port <= 0 || service.streamProbe != listener || string(first) != string(second) {
		t.Fatalf("steady renders differ: listener %d -> %d", listener.Port, service.streamProbe.Port)
	}
	intent.Monitor.SwitchPolicy = monitor.SwitchLowestLatency
	plain, err := service.renderWithHome(context.Background(), profile, intent, home, nil, capability)
	if err != nil {
		t.Fatal(err)
	}
	if service.streamProbe != (mihomo.StreamListener{}) || strings.Contains(string(plain), mihomo.StreamProbeName) {
		t.Fatal("lowest_latency render kept the stream probe listener")
	}
}

// A steady decision may carry only stream evidence; the switch must still apply.
func TestAutomaticSteadySwitchRecordsStreamEvidence(t *testing.T) {
	h := newStaticRuntime(t)
	h.activate(t)
	groupID := opaqueID("select-main")
	h.service.automation[groupID] = true
	h.service.acceptBatch(h.ctx, monitor.Batch{Decisions: []monitor.Decision{{
		Switch: true, Group: "select-main", Old: "node-a", New: "node-b", Reason: monitor.ReasonSteadierStream,
		Streams: []monitor.StreamSample{
			{Proxy: "node-b", FinishedAt: time.Unix(20, 0), Bytes: 4096, Duration: time.Second, Outcome: monitor.StreamSuccess},
			{Proxy: "node-a", FinishedAt: time.Unix(10, 0), Outcome: monitor.StreamReset},
		},
	}}})
	if selected := h.service.groups[groupID].Selected; selected != "node-b" {
		t.Fatalf("controller selection = %q, want steadier candidate", selected)
	}
	switches := h.service.snapshot.Switches
	if len(switches) != 1 {
		t.Fatalf("switches = %+v", switches)
	}
	evidence := switches[0].StreamEvidence
	if len(evidence) != 2 || evidence[0].ProxyID != opaqueID("node-a") || evidence[0].Outcome != "reset" ||
		evidence[1].ProxyID != opaqueID("node-b") || evidence[1].Outcome != "success" || evidence[1].KBps != 4 || evidence[1].FinishedAt != 20 {
		t.Fatalf("stream evidence = %+v", evidence)
	}
}

// The listener and group only exist in the generated config, so a runtime
// policy change into or out of steady_stream must re-render and restart.
func TestSwitchPolicyChangeRendersStreamProbe(t *testing.T) {
	h := newStaticRuntime(t)
	h.activate(t)
	if strings.Contains(string(h.service.generated), mihomo.StreamProbeName) || h.service.streamProbe.Port != 0 {
		t.Fatal("lowest_latency runtime has a stream probe")
	}
	steady := monitor.SwitchSteadyStream
	if err := h.service.patchSettings(h.ctx, &ipc.ConfigPatch{MonitorSwitchPolicy: &steady}); err != nil {
		t.Fatal(err)
	}
	listener, err := mihomo.StreamProbe(h.service.generated)
	if err != nil || listener.Port <= 0 || h.service.streamProbe != listener {
		t.Fatalf("steady_stream runtime listener = %d, %v; service holds port %d", listener.Port, err, h.service.streamProbe.Port)
	}
	lowest := monitor.SwitchLowestLatency
	if err := h.service.patchSettings(h.ctx, &ipc.ConfigPatch{MonitorSwitchPolicy: &lowest}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(h.service.generated), mihomo.StreamProbeName) || h.service.streamProbe != (mihomo.StreamListener{}) {
		t.Fatal("leaving steady_stream kept the stream probe")
	}
}
