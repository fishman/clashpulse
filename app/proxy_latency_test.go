package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
	scheduler, err := monitor.NewScheduler(monitor.DefaultPolicy(), controller, func(monitor.Batch) {})
	if err != nil {
		t.Fatal(err)
	}
	service.controller, service.monitor = controller, scheduler
	service.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: filepath.Join(configDir, "ipc.sock"), Handler: func(context.Context, ipc.Command) error { return nil }, InitialSnapshot: service.snapshot})
	if err != nil {
		t.Fatal(err)
	}
	service.snapshot.Proxies = []core.ProxySnapshot{{ID: opaqueID("node-a"), GroupID: opaqueID("select-main"), Label: "node-a", LatencyMillis: 180, FinishedAt: 7, Outcome: "success"}}

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
		if proxy.MihomoMillis != 77 {
			t.Fatalf("mihomo url-test delay missing from the row: %+v", proxy)
		}
		return
	}
	t.Fatalf("node-a missing from the refreshed snapshot: %+v", service.snapshot.Proxies)
}
