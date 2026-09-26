package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
