package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/fishman/clashpulse/download"
)

// The auto route goes through the running Mihomo mixed port. While Mihomo is
// stopped it uses the terminal's environment proxy when one is set, and
// connects directly otherwise.
func TestAutoRouteFollowsRunningMihomoThenEnvironmentProxy(t *testing.T) {
	counting := func(hits *atomic.Int32) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(server.Close)
		return server
	}
	var mihomoHits, environmentHits, originHits atomic.Int32
	mihomo := counting(&mihomoHits)
	environment := counting(&environmentHits)
	origin := counting(&originHits)
	_, port, err := net.SplitHostPort(mihomo.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	var service runtimeService
	service.mixedPort.Store(int32(number))

	get := func(want *atomic.Int32, label string) {
		t.Helper()
		before := [3]int32{mihomoHits.Load(), environmentHits.Load(), originHits.Load()}
		transport, err := service.transport(download.Auto, false)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		after := [3]int32{mihomoHits.Load(), environmentHits.Load(), originHits.Load()}
		total := after[0] - before[0] + after[1] - before[1] + after[2] - before[2]
		if total != 1 || want.Load() == 0 {
			t.Fatalf("%s: hits mihomo/environment/origin %v -> %v", label, before, after)
		}
		mihomoHits.Store(0)
		environmentHits.Store(0)
		originHits.Store(0)
	}
	get(&originHits, "stopped without environment proxy, want direct")
	environmentURL, err := url.Parse(environment.URL)
	if err != nil {
		t.Fatal(err)
	}
	service.autoProxy = http.ProxyURL(environmentURL)
	get(&environmentHits, "stopped with environment proxy, want it")
	service.running.Store(true)
	get(&mihomoHits, "running Mihomo, want its mixed port")
}
