package mihomo

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPullThroughClassifiesOutcomes(t *testing.T) {
	const size = 64 * 1024
	body := strings.Repeat("x", size)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Length", strconv.Itoa(size))
			_, _ = w.Write([]byte(body))
		case "/truncated":
			w.Header().Set("Content-Length", strconv.Itoa(size))
			_, _ = w.Write([]byte(body[:size/2]))
		case "/reset":
			w.Header().Set("Content-Length", strconv.Itoa(size))
			_, _ = w.Write([]byte(body[:size/2]))
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			raw := conn.(*tls.Conn).NetConn().(*net.TCPConn)
			_ = raw.SetLinger(0)
			_ = raw.Close()
		case "/stall":
			w.Header().Set("Content-Length", strconv.Itoa(size))
			_, _ = w.Write([]byte(body[:1024]))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/nolength":
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte(body))
		}
	}))
	defer server.Close()
	transport := server.Client().Transport
	for path, want := range map[string]string{
		"/ok": "success", "/truncated": "truncated", "/reset": "reset", "/stall": "stall", "/nolength": "error",
	} {
		result := pullThrough(context.Background(), transport, server.URL+path, 200*time.Millisecond, 5*time.Second)
		if result.Outcome != want {
			t.Fatalf("%s: outcome %q, want %q (%+v)", path, result.Outcome, want, result)
		}
		if want == "success" && (result.Bytes != size || result.FirstByte <= 0 || result.Duration <= 0) {
			t.Fatalf("success measurements: %+v", result)
		}
	}
}

func TestPullThroughRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	result := pullThrough(context.Background(), http.DefaultTransport, server.URL, time.Second, 5*time.Second)
	if result.Outcome != "tls" {
		t.Fatalf("outcome %q, want tls", result.Outcome)
	}
}

func TestPullThroughTotalTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		for range 100 {
			_, _ = w.Write(make([]byte, 100))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}))
	defer server.Close()
	result := pullThrough(context.Background(), server.Client().Transport, server.URL, time.Second, 300*time.Millisecond)
	if result.Outcome != "timeout" {
		t.Fatalf("outcome %q, want timeout", result.Outcome)
	}
}

func TestStreamPullErrorsOmitCredentials(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer controller.Close()
	c, err := NewController(controller.URL, "controller-secret", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	listener := StreamListener{Port: 1, Username: "probe-user", Password: "probe-password"}
	result := c.StreamPull(context.Background(), listener, "node-a", "https://speed.example.com/file")
	if result.Outcome == "success" {
		t.Fatal("pull through a closed port succeeded")
	}
	if strings.Contains(result.Outcome, "probe-password") || strings.Contains(result.Outcome, "probe-user") {
		t.Fatal("result leaks listener credentials")
	}
}
