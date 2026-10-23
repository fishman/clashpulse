package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
)

func TestRefreshGroupsKeepsMeasuredLatency(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"proxies":{` +
			`"select-main":{"name":"select-main","type":"Selector","all":["node-a","node-b"],"now":"node-a"},` +
			`"node-a":{"name":"node-a","type":"Direct","history":[{"delay":77}]},` +
			`"node-b":{"name":"node-b","type":"Direct"}}}`))
	}))
	defer controllerServer.Close()
	controller, err := mihomo.NewController(controllerServer.URL, "secret", controllerServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte("[monitor]\nenabled = true\n")); err != nil {
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
	scheduler, err := monitor.NewScheduler(monitor.DefaultPolicy(), controller, nil, func(monitor.Batch) {})
	if err != nil {
		t.Fatal(err)
	}
	service.controller, service.monitor = controller, scheduler
	service.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(configDir, "ipc.sock"), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: service.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	service.snapshot.Proxies = []core.ProxySnapshot{{ID: opaqueID("node-a"), GroupID: opaqueID("select-main"), Label: "node-a", LatencyMillis: 180, FinishedAt: 7, Outcome: "success", MedianMillis: 130, JitterMillis: 20, Samples: 5, Failures: 1}}

	if err := service.refreshGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, proxy := range service.snapshot.Proxies {
		if proxy.ID != opaqueID("node-a") {
			continue
		}
		if proxy.LatencyMillis != 180 || proxy.Outcome != "success" || proxy.FinishedAt != 7 {
			t.Fatalf("refresh discarded a completed probe: %+v", proxy)
		}
		if proxy.MedianMillis != 130 || proxy.JitterMillis != 20 || proxy.Samples != 5 || proxy.Failures != 1 {
			t.Fatalf("refresh discarded the probe window: %+v", proxy)
		}
		if proxy.MihomoMillis != 77 {
			t.Fatalf("mihomo url-test delay missing from the row: %+v", proxy)
		}
		return
	}
	t.Fatalf("node-a missing from the refreshed snapshot: %+v", service.snapshot.Proxies)
}

func TestAcceptBatchReportsWindowStabilityForMatchingProxy(t *testing.T) {
	configDir := t.TempDir()
	initial, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRuntimeService(configDir, filepath.Join(configDir, "state"), initial)
	if err != nil {
		t.Fatal(err)
	}
	service.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(configDir, "ipc.sock"), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: service.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	service.snapshot.Proxies = []core.ProxySnapshot{
		{ID: opaqueID("node-a"), GroupID: opaqueID("select-main")},
		{ID: opaqueID("node-a"), GroupID: opaqueID("select-backup")},
		{ID: opaqueID("node-b"), GroupID: opaqueID("select-main")},
	}
	service.acceptBatch(context.Background(), monitor.Batch{Stability: []monitor.ProxyStability{{
		Group: "select-main", Proxy: "node-a",
		Stability: monitor.Stability{Samples: 5, Failures: 1, Median: 130 * time.Millisecond, Jitter: 20 * time.Millisecond},
	}}})
	for _, proxy := range service.snapshot.Proxies {
		measured := proxy.ID == opaqueID("node-a") && proxy.GroupID == opaqueID("select-main")
		if measured && (proxy.MedianMillis != 130 || proxy.JitterMillis != 20 || proxy.Samples != 5 || proxy.Failures != 1) {
			t.Fatalf("probe window missing from its row: %+v", proxy)
		}
		if !measured && (proxy.MedianMillis != 0 || proxy.JitterMillis != 0 || proxy.Samples != 0 || proxy.Failures != 0) {
			t.Fatalf("probe window leaked into another row: %+v", proxy)
		}
	}
}

func TestDialFailuresProbeGroupEarlyUntilMonitorStops(t *testing.T) {
	var delays atomic.Int32
	emit, streamClosed := make(chan struct{}), make(chan struct{})
	var streams atomic.Int32
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/logs":
			if streams.Add(1) > 1 {
				http.Error(w, "one stream expected", http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-emit:
			case <-r.Context().Done():
				return
			}
			for range 2 {
				fmt.Fprintln(w, `{"type":"warning","payload":"[TCP] dial select-main (match Match/) 192.0.2.1:50000 --> example.com:443 error: i/o timeout"}`)
			}
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(streamClosed)
		case strings.HasSuffix(r.URL.Path, "/delay"):
			delays.Add(1)
			_, _ = w.Write([]byte(`{"delay":120}`))
		default:
			_, _ = w.Write([]byte(`{"proxies":{` +
				`"select-main":{"name":"select-main","type":"Selector","all":["node-a","node-b"],"now":"node-a"},` +
				`"node-a":{"name":"node-a","type":"Direct"},` +
				`"node-b":{"name":"node-b","type":"Direct"}}}`))
		}
	}))
	defer controllerServer.Close()
	controller, err := mihomo.NewController(controllerServer.URL, "secret", controllerServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	initial, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRuntimeService(configDir, filepath.Join(configDir, "state"), initial)
	if err != nil {
		t.Fatal(err)
	}
	service.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(configDir, "ipc.sock"), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: service.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.controller, service.processCtx = controller, ctx
	if err := service.refreshGroups(ctx); err != nil {
		t.Fatal(err)
	}
	waitDelays := func(want int32) {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if delays.Load() >= want {
				return
			}
		}
		t.Fatalf("delay probes = %d, want %d", delays.Load(), want)
	}
	waitDelays(2)
	close(emit)
	// The next scheduled round is a minute away, so a second round now can only
	// come from the reported dial failures.
	waitDelays(4)
	service.stopMonitor()
	<-service.monitorDone
	select {
	case <-streamClosed:
	case <-time.After(time.Second):
		t.Fatal("log stream outlived the monitor")
	}
}
