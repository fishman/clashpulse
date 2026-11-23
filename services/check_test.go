package services

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testTransport dials every host to the TLS test server and trusts its
// certificate, so the production URL policy (HTTPS, host allow-list) runs
// unchanged against reserved example hosts.
func testTransport(server *httptest.Server) *http.Transport {
	address := server.Listener.Addr().String()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	transport.DisableKeepAlives = true
	return transport
}

func serve(t *testing.T, handler http.HandlerFunc) *http.Transport {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return testTransport(server)
}

func traceDef() Definition {
	return Definition{ID: "trace", Name: "Trace", Rule: RuleTraceLoc, Blocked: []string{"CN"}, Hosts: []string{"example.com"},
		Requests: []Request{{URL: "https://example.com/cdn-cgi/trace", MaxBytes: 4096, Redirects: 10}}}
}

func one(t *testing.T, transport http.RoundTripper, def Definition) Result {
	t.Helper()
	results := Check(context.Background(), transport, []Definition{def})
	if len(results) != 1 || results[0].Service != def.ID {
		t.Fatalf("results = %+v", results)
	}
	return results[0]
}

func TestCheckTraceLoc(t *testing.T) {
	for loc, want := range map[string]Outcome{"US": Usable, "CN": Blocked} {
		transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.UserAgent() != UserAgent {
				t.Errorf("user agent = %q", r.UserAgent())
			}
			_, _ = w.Write([]byte("fl=1\nip=192.0.2.1\nloc=" + loc + "\ntls=TLSv1.3\n"))
		})
		if got := one(t, transport, traceDef()); got.Outcome != want || got.Region != loc {
			t.Fatalf("loc %s: %+v, want %s", loc, got, want)
		}
	}
}

func TestCheckMissingSignalIsError(t *testing.T) {
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("fl=1\n")) })
	if got := one(t, transport, traceDef()); got.Outcome != Failed || got.Region != "" {
		t.Fatalf("missing loc: %+v", got)
	}
}

func TestCheckBodyMarkerWithTraceRegion(t *testing.T) {
	def := Definition{ID: "marker", Name: "Marker", Rule: RuleBodyMarker, Markers: []string{"unsupported_country"}, Hosts: []string{"example.com"},
		Requests: []Request{{URL: "https://example.com/cdn-cgi/trace", MaxBytes: 4096, Redirects: 10}, {URL: "https://example.com/compliance", MaxBytes: 4096, Redirects: 10}}}
	for body, want := range map[string]Outcome{`{"ok":true}`: Usable, `{"cf_details":"Unsupported_Country"}`: Blocked} {
		transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/trace") {
				_, _ = w.Write([]byte("loc=JP\n"))
				return
			}
			_, _ = w.Write([]byte(body))
		})
		if got := one(t, transport, def); got.Outcome != want || got.Region != "JP" {
			t.Fatalf("body %s: %+v, want %s", body, got, want)
		}
	}
}

// A refusal often arrives as 403 with the marker in its body; a bare 403 or
// 451 is a refusal too, while another non-2xx says nothing about the region.
func TestCheckBodyMarkerRefusalStatus(t *testing.T) {
	def := Definition{ID: "marker", Name: "Marker", Rule: RuleBodyMarker, Markers: []string{"unsupported_country"}, Hosts: []string{"example.com"},
		Requests: []Request{{URL: "https://example.com/cdn-cgi/trace", MaxBytes: 4096, Redirects: 10}, {URL: "https://example.com/compliance", MaxBytes: 4096, Redirects: 10}}}
	for _, tc := range []struct {
		status int
		body   string
		want   Outcome
	}{
		{http.StatusForbidden, `{"error":"unsupported_country"}`, Blocked},
		{http.StatusForbidden, "", Blocked},
		{http.StatusUnavailableForLegalReasons, "", Blocked},
		{http.StatusInternalServerError, "", Failed},
	} {
		transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/trace") {
				_, _ = w.Write([]byte("loc=JP\n"))
				return
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		if got := one(t, transport, def); got.Outcome != tc.want {
			t.Fatalf("status %d body %q: %+v, want %s", tc.status, tc.body, got, tc.want)
		}
	}
}

func youtubeDefs() []Definition {
	youtube := Definition{ID: "youtube", Name: "YouTube", Rule: RuleBodyMarker, Markers: []string{"premium is not available in your region"}, Hosts: []string{"example.com"},
		Requests: []Request{{URL: "https://example.com/premium", MaxBytes: 96 << 10, Redirects: 10}}}
	gemini := Definition{ID: "gemini", Name: "Gemini", Rule: RuleGoogleCountry, Blocked: []string{"CN"}, Shares: "youtube"}
	return []Definition{youtube, gemini}
}

func TestCheckGoogleCountrySharesYouTubeRead(t *testing.T) {
	reads := 0
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		reads++
		_, _ = w.Write([]byte(strings.Repeat(" ", 1000) + `"GL":"CN"` + strings.Repeat(" ", 1000)))
	})
	results := Check(context.Background(), transport, youtubeDefs())
	if reads != 1 {
		t.Fatalf("reads = %d, want one shared read", reads)
	}
	if results[0].Outcome != Usable || results[0].Region != "CN" || results[1].Outcome != Blocked || results[1].Region != "CN" {
		t.Fatalf("results = %+v", results)
	}
}

func TestCheckGoogleCountryAloneStillReadsYouTube(t *testing.T) {
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`"GL":"US"`)) })
	gemini := youtubeDefs()[1]
	if got := one(t, transport, gemini); got.Outcome != Usable || got.Region != "US" {
		t.Fatalf("gemini alone = %+v", got)
	}
}

func TestCheckCapStopsReading(t *testing.T) {
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 200<<10) + `"GL":"US"`))
	})
	if got := one(t, transport, youtubeDefs()[0]); got.Outcome != Failed {
		t.Fatalf("signal past the cap was read: %+v", got)
	}
}

func TestCheckFollowsInBoundsRedirectsWithCookies(t *testing.T) {
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var hop int
		_, _ = fmt.Sscanf(r.URL.Query().Get("n"), "%d", &hop)
		if hop == 0 {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "1", Path: "/"})
		} else if _, err := r.Cookie("session"); err != nil {
			t.Errorf("hop %d lost the cookie", hop)
		}
		if hop < 10 {
			http.Redirect(w, r, fmt.Sprintf("https://example.com/next?n=%d", hop+1), http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("loc=US\n"))
	})
	if got := one(t, transport, traceDef()); got.Outcome != Usable {
		t.Fatalf("10 in-bounds hops with cookies: %+v", got)
	}
}

func TestCheckRedirectPolicy(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"eleventh hop": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.com/again", http.StatusFound)
		},
		"other host": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.net/trace", http.StatusFound)
		},
		"plain http": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://example.com/trace", http.StatusFound)
		},
	}
	for name, handler := range cases {
		if got := one(t, serve(t, handler), traceDef()); got.Outcome != Failed {
			t.Fatalf("%s: %+v", name, got)
		}
	}
}

func TestCheckConsentPageWithoutSignalIsError(t *testing.T) {
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/premium" {
			http.Redirect(w, r, "https://consent.example.com/consent", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html>Before you continue</html>"))
	})
	if got := one(t, transport, youtubeDefs()[0]); got.Outcome != Failed {
		t.Fatalf("consent page = %+v", got)
	}
}

func TestCheckCookiesDoNotCrossChecks(t *testing.T) {
	sawCookie := false
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if len(r.Cookies()) > 0 {
			sawCookie = true
		}
		http.SetCookie(w, &http.Cookie{Name: "region", Value: "US", Path: "/"})
		_, _ = w.Write([]byte("loc=US\n"))
	})
	one(t, transport, traceDef())
	one(t, transport, traceDef())
	if sawCookie {
		t.Fatal("a cookie from one check reached the next")
	}
}

func netflixDef() Definition {
	return Definition{ID: "netflix", Name: "Netflix", Rule: RuleStatusPair, Hosts: []string{"example.com"}, Requests: []Request{
		{URL: "https://example.com/title/1"}, {URL: "https://example.com/title/2"}, {URL: "https://example.com/title/3"}}}
}

func TestCheckStatusPair(t *testing.T) {
	cases := []struct {
		name       string
		one, two   int
		want       Outcome
		wantRegion string
	}{
		{"full", 200, 200, Usable, "US"},
		{"originals only", 404, 404, Blocked, ""},
		{"refused", 403, 200, Blocked, ""},
		{"one licensed", 404, 200, Usable, "US"},
		{"server error", 500, 500, Failed, ""},
	}
	for _, tc := range cases {
		transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/title/1":
				w.WriteHeader(tc.one)
			case "/title/2":
				w.WriteHeader(tc.two)
			default:
				w.Header().Set("Location", "https://example.com/us-en/title/3")
				w.WriteHeader(http.StatusFound)
			}
		})
		if got := one(t, transport, netflixDef()); got.Outcome != tc.want || got.Region != tc.wantRegion {
			t.Fatalf("%s: %+v", tc.name, got)
		}
	}
}

func TestCheckJSONCountry(t *testing.T) {
	def := Definition{ID: "spotify", Name: "Spotify", Rule: RuleJSONCountry, Markers: []string{"not available in your country"}, Hosts: []string{"example.com"},
		Requests: []Request{{URL: "https://example.com/api/country", MaxBytes: 16 << 10, Redirects: 10}}}
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/country" {
			http.Redirect(w, r, "https://example.com/de-de/api", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{"label":"x"}`))
	})
	if got := one(t, transport, def); got.Outcome != Usable || got.Region != "DE" {
		t.Fatalf("region from final path = %+v", got)
	}
	transport = serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"countryCode":"gb"}`)) })
	if got := one(t, transport, def); got.Outcome != Usable || got.Region != "GB" {
		t.Fatalf("countryCode = %+v", got)
	}
	transport = serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	if got := one(t, transport, def); got.Outcome != Blocked {
		t.Fatalf("403 = %+v", got)
	}
}

func TestCheckBudgetSpansHopsAndCancels(t *testing.T) {
	transport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	results := Check(ctx, transport, []Definition{traceDef()})
	if results[0].Outcome != Failed || time.Since(started) > 2*time.Second {
		t.Fatalf("cancelled check = %+v after %v", results, time.Since(started))
	}
}

func TestDefinitionsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, def := range Definitions() {
		if seen[def.ID] || def.Name == "" || len(def.Domains) == 0 {
			t.Fatalf("definition %+v", def)
		}
		seen[def.ID] = true
		for _, request := range def.Requests {
			if !strings.HasPrefix(request.URL, "https://") {
				t.Fatalf("%s request %q is not HTTPS", def.ID, request.URL)
			}
		}
	}
	if strings.Join(IDs(), ",") != "chatgpt,claude,gemini,youtube,netflix,spotify" {
		t.Fatalf("IDs = %v", IDs())
	}
}
