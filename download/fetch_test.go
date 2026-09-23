package download

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchUsesRouteValidatorsAndBodyLimit(t *testing.T) {
	var gotRoute Route
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"abc"` {
			t.Fatalf("If-None-Match = %q", got)
		}
		if got := r.Header.Get("If-Modified-Since"); got != time.Unix(1700000000, 0).UTC().Format(http.TimeFormat) {
			t.Fatalf("If-Modified-Since = %q", got)
		}
		w.Header().Set("ETag", `"def"`)
		w.Header().Set("Last-Modified", time.Unix(1700000100, 0).UTC().Format(http.TimeFormat))
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := NewClient(func(route Route) (http.RoundTripper, error) {
		gotRoute = route
		return server.Client().Transport, nil
	})

	got, err := client.Fetch(context.Background(), Request{
		URL:          server.URL,
		Route:        MihomoProxy,
		ETag:         `"abc"`,
		LastModified: time.Unix(1700000000, 0).UTC().Format(http.TimeFormat),
		MaxBytes:     16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRoute != MihomoProxy {
		t.Fatalf("route = %q", gotRoute)
	}
	if got.StatusCode != http.StatusOK || string(got.Body) != "ok" || got.ETag != `"def"` || got.LastModified != time.Unix(1700000100, 0).UTC().Format(http.TimeFormat) {
		t.Fatalf("response = %+v", got)
	}
}

func TestFetchRejectsHTTPAndUserinfo(t *testing.T) {
	client := NewClient(func(Route) (http.RoundTripper, error) { return http.DefaultTransport, nil })
	for _, req := range []Request{
		{URL: "http://example.com", Route: Direct, MaxBytes: 1},
		{URL: "https://user@example.com", Route: Direct, MaxBytes: 1},
	} {
		if _, err := client.Fetch(context.Background(), req); err == nil {
			t.Fatalf("Fetch(%+v) error = nil", req)
		}
	}
}

func TestFetchFollowsRedirectsAndBoundsBody(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "xxxxx")
	}))
	defer final.Close()

	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer hop.Close()

	client := NewClient(func(route Route) (http.RoundTripper, error) {
		if route != SystemProxy {
			t.Fatalf("route = %q", route)
		}
		return hop.Client().Transport, nil
	})

	if _, err := client.Fetch(context.Background(), Request{URL: hop.URL, Route: SystemProxy, MaxBytes: 4, AllowHTTP: true}); err == nil {
		t.Fatal("Fetch accepted an oversized body")
	}
}

func TestFetchReturns304Bodyless(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
		_, _ = io.WriteString(w, "ignored")
	}))
	defer server.Close()

	client := NewClient(func(Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	got, err := client.Fetch(context.Background(), Request{URL: server.URL, Route: Direct, MaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusNotModified || len(got.Body) != 0 {
		t.Fatalf("response = %+v", got)
	}
}
