//go:build linux

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/sysproxy"
)

func TestSystemProxyTransportFailsClosedWithoutGNOMESession(t *testing.T) {
	for _, key := range []string{"XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "DESKTOP_SESSION", "GNOME_DESKTOP_SESSION_ID"} {
		t.Setenv(key, "")
	}
	var proxyHits, originHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		originHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}

	service := runtimeService{proxy: sysproxy.NewManager()}
	transport, err := service.transport(download.SystemProxy, false)
	if err != nil {
		t.Fatalf("transport construction = %v, want request-time OS proxy error", err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("system proxy request succeeded without a supported OS proxy")
	}
	if proxyHits.Load() != 0 || originHits.Load() != 0 {
		t.Fatalf("unsupported system proxy fell back: proxy hits=%d, origin hits=%d", proxyHits.Load(), originHits.Load())
	}
}
