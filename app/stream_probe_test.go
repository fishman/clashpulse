package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
)

func TestNewProbeListenersArePrivateDistinctAndFresh(t *testing.T) {
	first, err := newProbeListeners()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newProbeListeners()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != mihomo.ProbeSlots || slices.Equal(first, second) {
		t.Fatalf("listeners = %+v %+v", first, second)
	}
	ports := map[int]bool{}
	for _, listener := range first {
		if listener.Port <= 0 || ports[listener.Port] || len(listener.Username) < 16 || len(listener.Password) < 32 {
			t.Fatalf("listener %+v in %+v", listener, first)
		}
		ports[listener.Port] = true
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
		if entry.Kind == "stream_probe" && entry.Severity == "warning" && entry.SourceID == opaqueID("node-a") && entry.Message == "stream.outcome.reset" {
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

// A latency batch offered while a stream batch is pending must not replace it,
// or the stream round's switch decision is lost until the next interval.
func TestLatencyBatchDoesNotDropPendingStreamBatch(t *testing.T) {
	service := newBareRuntimeForTest(t)
	stream := monitor.Batch{Streams: []monitor.StreamSample{{Proxy: "node-a", Outcome: monitor.StreamSuccess}}, Decisions: []monitor.Decision{{Group: "auto", Switch: true}}}
	latency := monitor.Batch{Samples: []monitor.Sample{{Group: "auto", Proxy: "node-a", Outcome: monitor.OutcomeSuccess}}}
	service.offerBatch(stream)
	service.offerBatch(latency)
	var got []monitor.Batch
	for range 2 {
		select {
		case batch := <-service.batches:
			got = append(got, batch)
		case batch := <-service.streamBatches:
			got = append(got, batch)
		default:
		}
	}
	if len(got) != 2 || len(got[0].Streams)+len(got[1].Streams) != 1 || len(got[0].Samples)+len(got[1].Samples) != 1 {
		t.Fatalf("pending batches = %+v, want the stream and the latency batch", got)
	}
}

func TestSteadyStreamSnapshotHasNoProbeCredentials(t *testing.T) {
	service := newBareRuntimeForTest(t)
	service.probes = []mihomo.ProbeListener{{Port: 1, Username: "probe-user-secret", Password: "probe-password-secret"}}
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
	service.probes = []mihomo.ProbeListener{{Port: freeLoopbackPort(t), Username: "stale", Password: "stale"}}
	path := filepath.Join(t.TempDir(), "generated.yaml")
	generated := fmt.Sprintf("mixed-port: %d\nlisteners:\n", freeLoopbackPort(t))
	for slot := 1; slot <= mihomo.ProbeSlots; slot++ {
		port := freeLoopbackPort(t)
		if slot == 3 {
			port = busy.Addr().(*net.TCPAddr).Port
		}
		generated += fmt.Sprintf("  - {name: %[1]s, type: mixed, listen: 127.0.0.1, port: %[2]d, proxy: %[1]s, users: [{username: user, password: password}]}\n", mihomo.ProbeName(slot), port)
	}
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
	if service.probes != nil {
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
	listeners := service.probes
	second, err := service.renderWithHome(context.Background(), profile, intent, home, nil, capability)
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners) != mihomo.ProbeSlots || !slices.Equal(service.probes, listeners) || string(first) != string(second) {
		t.Fatalf("steady renders differ: listeners %+v -> %+v", listeners, service.probes)
	}
	intent.Monitor.SwitchPolicy = monitor.SwitchLowestLatency
	plain, err := service.renderWithHome(context.Background(), profile, intent, home, nil, capability)
	if err != nil {
		t.Fatal(err)
	}
	if service.probes != nil || strings.Contains(string(plain), "clashpulse-probe") {
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
	if strings.Contains(string(h.service.generated), "clashpulse-probe") || h.service.probes != nil {
		t.Fatal("lowest_latency runtime has a stream probe")
	}
	steady := monitor.SwitchSteadyStream
	if err := h.service.patchSettings(h.ctx, &ipc.ConfigPatch{MonitorSwitchPolicy: &steady}); err != nil {
		t.Fatal(err)
	}
	listeners, err := mihomo.ProbeListeners(h.service.generated)
	if err != nil || len(listeners) != mihomo.ProbeSlots || !slices.Equal(h.service.probes, listeners) {
		t.Fatalf("steady_stream runtime listeners = %+v, %v; service holds %+v", listeners, err, h.service.probes)
	}
	lowest := monitor.SwitchLowestLatency
	if err := h.service.patchSettings(h.ctx, &ipc.ConfigPatch{MonitorSwitchPolicy: &lowest}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(h.service.generated), "clashpulse-probe") || h.service.probes != nil {
		t.Fatal("leaving steady_stream kept the stream probe")
	}
}

func TestStreamProbeCommandReachesSchedulerOrExplainsPolicy(t *testing.T) {
	// Both the persistent issue and the activity log must name the reason.
	streamFailure := func(service *runtimeService) (issue, diagnostic string) {
		for _, entry := range service.snapshot.Errors {
			if entry.Kind == string(ipc.CommandStreamProbe) {
				issue = entry.Message
			}
		}
		for _, entry := range service.snapshot.Diagnostics {
			if entry.Kind == string(ipc.CommandStreamProbe) && entry.Severity == "error" {
				diagnostic = entry.Message
			}
		}
		return issue, diagnostic
	}
	for _, test := range []struct {
		policy string
		issue  string
	}{
		{monitor.SwitchLowestLatency, "steady-stream probing is not enabled"},
		{monitor.SwitchSteadyStream, ""},
	} {
		service := newBareRuntimeForTest(t)
		policy := monitor.DefaultPolicy()
		policy.SwitchPolicy = test.policy
		scheduler, err := monitor.NewScheduler(policy, instantDelay{}, &memberStreamer{pulled: make(chan string, 1)}, func(monitor.Batch) {})
		if err != nil {
			t.Fatal(err)
		}
		service.monitor = scheduler
		service.runIntent(context.Background(), ipc.Command{Kind: ipc.CommandStreamProbe})
		if issue, diagnostic := streamFailure(service); issue != test.issue || diagnostic != test.issue {
			t.Fatalf("%s stream probe issue = %q, diagnostic = %q, want %q", test.policy, issue, diagnostic, test.issue)
		}
	}
}

type instantDelay struct{}

func (instantDelay) Delay(context.Context, string, string, time.Duration) (time.Duration, error) {
	return time.Millisecond, nil
}
