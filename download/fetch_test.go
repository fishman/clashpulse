package download

import (
	"context"
	"errors"
	"fmt"
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

func TestFetchRejectsDisallowedRedirectTargets(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://example.com/next", http.StatusFound)
		}))
		defer server.Close()

		client := NewClient(func(Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
		if _, err := client.Fetch(context.Background(), Request{URL: server.URL, Route: Direct, MaxBytes: 8}); err == nil {
			t.Fatal("Fetch accepted an HTTPS->HTTP redirect")
		}
	})

	t.Run("userinfo", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://user@example.com/next", http.StatusFound)
		}))
		defer server.Close()

		client := NewClient(func(Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
		if _, err := client.Fetch(context.Background(), Request{URL: server.URL, Route: Direct, MaxBytes: 8}); err == nil {
			t.Fatal("Fetch accepted a redirect with userinfo")
		}
	})
}

func TestFetchConditionalHeadersStayOnOriginalAndSameOriginOnly(t *testing.T) {
	t.Run("same-origin", func(t *testing.T) {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/start":
				if got := r.Header.Get("If-None-Match"); got != `"abc"` {
					t.Fatalf("start If-None-Match = %q", got)
				}
				if got := r.Header.Get("If-Modified-Since"); got != time.Unix(1700000000, 0).UTC().Format(http.TimeFormat) {
					t.Fatalf("start If-Modified-Since = %q", got)
				}
				http.Redirect(w, r, server.URL+"/next", http.StatusFound)
			case "/next":
				if got := r.Header.Get("If-None-Match"); got != `"abc"` {
					t.Fatalf("next If-None-Match = %q", got)
				}
				if got := r.Header.Get("If-Modified-Since"); got != time.Unix(1700000000, 0).UTC().Format(http.TimeFormat) {
					t.Fatalf("next If-Modified-Since = %q", got)
				}
				_, _ = io.WriteString(w, "ok")
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := NewClient(func(Route) (http.RoundTripper, error) { return http.DefaultTransport, nil })
		got, err := client.Fetch(context.Background(), Request{
			URL:          server.URL + "/start",
			Route:        Direct,
			ETag:         `"abc"`,
			LastModified: time.Unix(1700000000, 0).UTC().Format(http.TimeFormat),
			MaxBytes:     8,
			AllowHTTP:    true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.StatusCode != http.StatusOK || string(got.Body) != "ok" {
			t.Fatalf("response = %+v", got)
		}
	})

	t.Run("cross-origin", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("If-None-Match"); got != "" {
				t.Fatalf("target If-None-Match = %q", got)
			}
			if got := r.Header.Get("If-Modified-Since"); got != "" {
				t.Fatalf("target If-Modified-Since = %q", got)
			}
			_, _ = io.WriteString(w, "ok")
		}))
		defer target.Close()

		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("If-None-Match"); got != `"abc"` {
				t.Fatalf("origin If-None-Match = %q", got)
			}
			if got := r.Header.Get("If-Modified-Since"); got != time.Unix(1700000000, 0).UTC().Format(http.TimeFormat) {
				t.Fatalf("origin If-Modified-Since = %q", got)
			}
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer origin.Close()

		client := NewClient(func(Route) (http.RoundTripper, error) { return http.DefaultTransport, nil })
		got, err := client.Fetch(context.Background(), Request{
			URL:          origin.URL,
			Route:        Direct,
			ETag:         `"abc"`,
			LastModified: time.Unix(1700000000, 0).UTC().Format(http.TimeFormat),
			MaxBytes:     8,
			AllowHTTP:    true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.StatusCode != http.StatusOK || string(got.Body) != "ok" {
			t.Fatalf("response = %+v", got)
		}
	})
}

func TestFetchReturns304ValidatorsAndBodyless(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"def"`)
		w.Header().Set("Last-Modified", time.Unix(1700000100, 0).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusNotModified)
		_, _ = io.WriteString(w, "ignored")
	}))
	defer server.Close()

	client := NewClient(func(Route) (http.RoundTripper, error) { return server.Client().Transport, nil })
	got, err := client.Fetch(context.Background(), Request{URL: server.URL, Route: Direct, MaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusNotModified || len(got.Body) != 0 || got.ETag != `"def"` || got.LastModified != time.Unix(1700000100, 0).UTC().Format(http.TimeFormat) {
		t.Fatalf("response = %+v", got)
	}
}

func TestFetchCancelsDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	body := &cancelBody{cancel: cancel}
	client := NewClient(func(Route) (http.RoundTripper, error) {
		return roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK), Body: body, Header: make(http.Header)}, nil
		}), nil
	})

	got, err := client.Fetch(ctx, Request{URL: "https://example.com/list", Route: Direct, MaxBytes: 8})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Fetch() = %+v, %v", got, err)
	}
	if body.reads != 1 {
		t.Fatalf("body reads = %d, want 1", body.reads)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cancelBody struct {
	reads  int
	cancel func()
}

func (b *cancelBody) Read(p []byte) (int, error) {
	switch b.reads {
	case 0:
		b.reads++
		b.cancel()
		p[0] = 'a'
		return 1, nil
	case 1, 2:
		b.reads++
		p[0] = 'b'
		return 1, nil
	default:
		return 0, io.EOF
	}
}

func (b *cancelBody) Close() error { return nil }

func TestFetchFailsOnSixthRedirect(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Query().Get("n"), "%d", &n); err != nil {
			n = 1
		}
		if n < 7 {
			http.Redirect(w, r, fmt.Sprintf("%s?n=%d", server.URL, n+1), http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := NewClient(func(Route) (http.RoundTripper, error) { return http.DefaultTransport, nil })
	if _, err := client.Fetch(context.Background(), Request{URL: server.URL + "?n=1", Route: Direct, MaxBytes: 8, AllowHTTP: true}); err == nil {
		t.Fatal("Fetch accepted a sixth redirect")
	}
}
