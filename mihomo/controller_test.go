package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUnixControllerTalksOverPrivateSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "mihomo.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.URL.Path != "/proxies" {
			http.Error(w, "unexpected", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"proxies":{"main":{"name":"main","type":"Selector","all":["node-a"],"now":"node-a"},"node-a":{"name":"node-a","type":"Direct"}}}`))
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	controller, err := NewUnixController(socket, "secret", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, groups, err := controller.Proxies(context.Background()); err != nil || len(groups) != 1 || groups[0].Selected != "node-a" {
		t.Fatalf("Proxies over socket = %+v, %v", groups, err)
	}
	if _, err := NewUnixController("relative.sock", "secret", nil); err == nil {
		t.Fatal("relative controller socket accepted")
	}
}

func TestWatchDialFailuresReportsOnlyManagedGroupDials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs" || r.URL.Query().Get("level") != "warning" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request = %s %s auth=%q", r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"))
		}
		flusher := w.(http.Flusher)
		write := func(payload string) {
			_ = json.NewEncoder(w).Encode(map[string]string{"type": "warning", "payload": payload})
			flusher.Flush()
		}
		write("[TCP] dial select main (match DomainSuffix/example.com) 192.0.2.1:50000 --> example.com:443 error: connect failed")
		// Outlives the controller client's timeout: the stream must not be cut.
		time.Sleep(150 * time.Millisecond)
		write("[UDP] dial select 192.0.2.1:50001 --> example.net:443 error: timeout")
		write("[TCP] dial unmanaged 192.0.2.1:50002 --> example.org:443 error: timeout")
		write("[TCP] 192.0.2.1:50003 --> example.com:443 match Match using select")
		_, _ = fmt.Fprintln(w, "not json")
		write("[TCP] dial selectx 192.0.2.1:50004 --> example.com:443 error: timeout")
	}))
	defer server.Close()
	controller, err := NewController(server.URL, "secret", &http.Client{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var reported []string
	err = controller.WatchDialFailures(context.Background(), func() []string { return []string{"select", "select main"} }, func(group string) { reported = append(reported, group) })
	if err == nil || strings.Contains(err.Error(), "example") {
		t.Fatalf("ended stream error = %v", err)
	}
	if !slices.Equal(reported, []string{"select main", "select"}) {
		t.Fatalf("reported = %q", reported)
	}
}

func TestWatchDialFailuresStopsOnCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	controller, err := NewController(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := controller.WatchDialFailures(ctx, func() []string { return []string{"select"} }, func(string) {}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled watch error = %v", err)
	}
}

func TestNewControllerRejectsNonLoopbackBaseURL(t *testing.T) {
	for _, baseURL := range []string{"http://example.com", "http://192.168.1.1", "not a URL"} {
		if _, err := NewController(baseURL, "secret", nil); err == nil {
			t.Errorf("NewController(%q) error = nil", baseURL)
		}
	}
}

func TestControllerRejectsRedirectWithoutLeakingSecret(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
	}))
	defer remote.Close()
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, remote.URL, http.StatusFound)
	}))
	defer controllerServer.Close()

	controller, err := NewController(controllerServer.URL, "secret", controllerServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := controller.Proxies(context.Background()); err == nil {
		t.Fatal("Proxies() error = nil")
	}
}

func TestControllerUsesTypedRequests(t *testing.T) {
	testURL := "https://example.com/path?x=1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer secret"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}

		switch r.URL.EscapedPath() {
		case "/proxies":
			if got, want := r.Method, http.MethodGet; got != want {
				t.Errorf("method = %s, want %s", got, want)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"proxies": map[string]any{
				"Direct":    map[string]any{"name": "Direct", "type": "Direct", "history": []map[string]any{{"delay": 90}, {"delay": 0}, {"delay": 42}}},
				"Group / A": map[string]any{"name": "Group / A", "type": "Selector", "all": []string{"Direct"}, "now": "Direct"},
			}})
		case "/proxies/Group%20%2F%20A/delay":
			if got, want := r.Method, http.MethodGet; got != want {
				t.Errorf("method = %s, want %s", got, want)
			}
			if got := r.URL.Query().Get("url"); got != testURL {
				t.Errorf("url = %q, want %q", got, testURL)
			}
			if got, want := r.URL.Query().Get("timeout"), "5000"; got != want {
				t.Errorf("timeout = %q, want %q", got, want)
			}
			_, _ = w.Write([]byte(`{"delay":42}`))
		case "/proxies/Group%20%2F%20A":
			if got, want := r.Method, http.MethodPut; got != want {
				t.Errorf("method = %s, want %s", got, want)
			}
			var body struct{ Name string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if got, want := body.Name, "Direct / One"; got != want {
				t.Errorf("name = %q, want %q", got, want)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/configs":
			if got, want := r.Method, http.MethodPut; got != want {
				t.Errorf("method = %s, want %s", got, want)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 0 {
				t.Errorf("body = %#v, want empty object", body)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	controller, err := NewController(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}

	proxies, groups, err := controller.Proxies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(proxies), 1; got != want || proxies[0].Name != "Direct" || proxies[0].Type != "Direct" {
		t.Fatalf("proxies = %#v", proxies)
	}
	if got, want := proxies[0].DelayMillis, int64(42); got != want {
		t.Fatalf("newest url-test delay = %d, want %d", got, want)
	}
	if got, want := len(groups), 1; got != want || groups[0].Name != "Group / A" || groups[0].Selected != "Direct" || len(groups[0].Proxies) != 1 || groups[0].Proxies[0] != "Direct" {
		t.Fatalf("groups = %#v", groups)
	}

	delay, err := controller.Delay(context.Background(), "Group / A", testURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := delay, 42*time.Millisecond; got != want {
		t.Fatalf("delay = %s, want %s", got, want)
	}
	if err := controller.Select(context.Background(), "Group / A", "Direct / One"); err != nil {
		t.Fatal(err)
	}
	if err := controller.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestControllerDelayRejectsMissingAndNonpositiveResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"delay":0}`, `{"delay":-1}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			controller, err := NewController(server.URL, "secret", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := controller.Delay(context.Background(), "proxy", "https://example.com", time.Second); err == nil {
				t.Fatal("Delay() error = nil")
			}
		})
	}
}

func TestControllerBoundsResponseBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"proxies":{` + strings.Repeat(`"x":"`, 300000) + `"}}`))
	}))
	defer server.Close()
	controller, err := NewController(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := controller.Proxies(context.Background()); err == nil {
		t.Fatal("Proxies() error = nil")
	}
}

func TestControllerTunEnabledReadsRunningConfig(t *testing.T) {
	for _, test := range []struct {
		body    string
		want    bool
		wantErr bool
	}{
		{body: `{"mode":"rule","tun":{"enable":true,"device":"clashpulse0"}}`, want: true},
		{body: `{"tun":{"enable":false}}`, want: false},
		{body: `{"mode":"rule"}`, wantErr: true},
		{body: `{"tun":{}}`, wantErr: true},
		{body: `{"tun":{"enable":null}}`, wantErr: true},
	} {
		t.Run(test.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/configs" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			controller, err := NewController(server.URL, "secret", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			got, err := controller.TunEnabled(context.Background())
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("TunEnabled() = %v, %v; want %v, error %v", got, err, test.want, test.wantErr)
			}
		})
	}
}
