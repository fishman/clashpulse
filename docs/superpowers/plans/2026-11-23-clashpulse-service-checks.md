# Service Checks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route ChatGPT, Claude, Gemini, YouTube Premium, Netflix, and Spotify through their own managed node groups, check through Mihomo that each group's node is usable for its service, and (opt-in) move the group when checks keep failing.

**Architecture:** A leaf package `services` holds the six built-in check definitions and runs one node's checks over an `http.RoundTripper`. `mihomo` renders a pool of 4 pinned probe listeners (replacing the single steady-stream listener) plus one `ClashPulse - <Name>` select group and its rules per enabled service, and leases pool slots through `mihomo.ProbePool`. `monitor.ServiceScheduler` (one goroutine, one timer) decides when to check and when to search for a new node; `app` applies its switches under the existing guards and publishes `core.ServiceSnapshot` over IPC to the GUI and TUI.

**Tech Stack:** Go standard library (`net/http`, `net/http/cookiejar`, `net/http/httptest`), existing `go.yaml.in` YAML and strict TOML decoders, Fyne v2, tcell.

**Spec:** `docs/superpowers/specs/2026-11-23-clashpulse-service-checks-design.md`

## Global Constraints

- Nothing is enabled by default; a service absent from `config.toml` or with `enabled = false` renders nothing and downloads nothing.
- `automatic` defaults to `false`; only `automatic = true` lets ClashPulse select a node in a service group. A manual pick sets it back to `false`.
- Group names are ASCII: `ClashPulse - <Name>`; probe groups and listeners are `clashpulse-probe-1` .. `clashpulse-probe-4`.
- Every check request: GET, HTTPS only, TLS 1.2+ with verified certificates, fixed desktop browser `User-Agent`, `DisableKeepAlives`, 15 s per service check across all hops, per-request read cap, fresh in-memory cookie jar per check, at most 10 redirects (Netflix: none) to HTTPS hosts within the definition's `Hosts`.
- Bodies, cookies, node names, and URLs never leave `services` or `app` in a snapshot, event, log, or diagnostic; snapshots carry masked node labels and fixed reason codes only.
- Default `check_interval` 30m (minimum 5m), up to 10% jitter; default `failures` 2 (range 1..10); at most 8 candidates per search, checked in waves of at most 4.
- IPC protocol version becomes 14.
- Tests and fixtures use reserved names only (`example.com`, `example.net`, `.invalid`, `192.0.2.0/24`).
- ASCII in code and prose; conventional commits `type(scope): subject`; no AI marker.
- Each task ends with `gofmt -l` clean and its focused `go test -tags ci` command green; the last task runs `go vet -tags ci ./...` and `go test -tags ci ./...`.

## Review Focus

1. A profile whose own group is already named `ClashPulse - Netflix` must fail render with the service named, and keep the running config (Task 4 test `TestRenderServicesRejectsNameCollision`).
2. A user picks a node in a service group from the Proxies view while a search is running: the search result must not overwrite the manual pick (Task 7 test `TestServiceSwitchRejectedAfterManualPick`).
3. Steady stream and services both enabled: a stream pull and a service check must never hold the same slot (Task 3 test `TestProbePoolLeasesAreExclusive`).
4. A YouTube consent redirect that stays on `youtube.com` but has no `GL` must be `error`, never `usable` (Task 1 test `TestCheckConsentPageWithoutSignalIsError`).
5. Disabling the last service while a check is in flight must cancel it and leave no timer armed (Task 5 test `TestServiceSchedulerIdleWithoutServices`).
6. Lowest-latency automation must never move a service group: `set_auto_fastest` and the latency monitor skip `ClashPulse - <Name>` groups (Task 7 test `TestServiceGroupsOutsideLatencyAutomation`).

## File Structure

| File | Responsibility |
|---|---|
| `services/definitions.go` (new) | The six `Definition` values, `Lookup`, `IDs`, `UserAgent`, `CheckTimeout` |
| `services/check.go` (new) | `Check` and `ProxyTransport`: request policy, redirects, cookies, caps, rule evaluation |
| `services/check_test.go` (new) | `httptest` coverage of every rule, redirect, cookie, cap, and cancellation case |
| `mihomo/probe.go` (new, replaces `mihomo/stream.go` listener parts) | `ProbeListener`, `ProbeName`, `ProbeSlots`, `ProbePool`, `ProbeLease`, `StreamPull` on a lease |
| `mihomo/render.go` | `renderProbes`, `renderServices`, `ServiceGroupName`, `ProbeListeners` parse |
| `mihomo/probe_runtime_test.go` (new) | Installed-binary test: each pool listener egresses only through its own group |
| `config/types.go`, `config/toml_decode.go`, `config/load.go` | `Service` intent, `[[services]]` decode, defaults, validation |
| `config/service_write.go` (new) | `PutService` writer |
| `monitor/service.go` (new) | `ServiceScheduler`, `ServicePlan`, `ServiceTarget`, `ServiceBatch`, candidate and failure policy |
| `monitor/service_test.go` (new) | Scheduler and policy tests with a fake checker |
| `core/snapshot.go` | `ServiceSnapshot`, `Snapshot.Services`, clone |
| `ipc/protocol.go` | `CommandSetService`, `CommandCheckService`, `ServiceEdit`, validation, version 14 |
| `app/services.go` (new) | Plan building, checker adapter, batch application, switch guard, commands |
| `app/lifecycle.go`, `app/runtime.go`, `app/stream_probe.go` | Probe pool lifecycle replaces the single stream listener |
| `ui/services.go` (new), `ui/views.go` | Settings > Services section and edit dialog |
| `tui/model.go`, `tui/keys.toml`, `tui/keys.go` | Service rows and actions |
| `localize/locale/en.toml`, `localize/locale/zh-CN.toml` | New strings |
| `docs/configuration.md`, `docs/zh-CN/configuration.md`, `examples/config.toml`, `README.md` | User docs |

---

### Task 1: `services` package - definitions and checks

**Files:**
- Create: `services/definitions.go`, `services/check.go`
- Test: `services/check_test.go`

**Interfaces:**
- Consumes: nothing (leaf package; imports only the standard library).
- Produces:

```go
package services

type Outcome string

const (
	Usable  Outcome = "usable"
	Blocked Outcome = "blocked"
	Failed  Outcome = "error"
)

type Rule string

const (
	RuleTraceLoc      Rule = "trace_loc"
	RuleBodyMarker    Rule = "body_marker"
	RuleStatusPair    Rule = "status_pair"
	RuleJSONCountry   Rule = "json_country"
	RuleGoogleCountry Rule = "google_country"
)

type Request struct {
	URL       string
	MaxBytes  int64
	Redirects int
}

type Definition struct {
	ID, Name string
	Rule     Rule
	Requests []Request
	Blocked  []string // ISO 3166-1 alpha-2
	Markers  []string // lower-case body phrases meaning "refused here"
	Hosts    []string // redirect targets: equal to or a subdomain of one
	Domains  []string // default DOMAIN-SUFFIX routing set
	Shares   string   // ID whose read this definition evaluates (gemini -> youtube)
}

type Result struct {
	Service string
	Outcome Outcome
	Region  string
}

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
const CheckTimeout = 15 * time.Second

func Definitions() []Definition
func Lookup(id string) (Definition, bool)
func IDs() []string
func Check(ctx context.Context, transport http.RoundTripper, defs []Definition) []Result
func ProxyTransport(proxy *url.URL) *http.Transport
```

- [ ] **Step 1: Write the failing tests**

`services/check_test.go`. Test definitions point at `example.com` hosts; `testTransport` dials every host to the TLS test server and trusts its certificate, so the production URL policy (HTTPS, host allow-list) runs unchanged.

```go
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

func testTransport(t *testing.T, server *httptest.Server) *http.Transport {
	t.Helper()
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
	return testTransport(t, server)
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
		fmt.Sscanf(r.URL.Query().Get("n"), "%d", &hop)
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
		name         string
		one, two     int
		want         Outcome
		wantRegion   string
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags ci ./services`
Expected: FAIL, `undefined: Check` (package has no implementation).

- [ ] **Step 3: Write `services/definitions.go`**

```go
// Package services checks whether a node is usable for a named service. The
// definitions are fixed data: no user-supplied URL or code reaches a check.
package services

import "time"

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// CheckTimeout bounds one service check across all its requests and redirects.
const CheckTimeout = 15 * time.Second

const maxRedirects = 10

var definitions = []Definition{
	{ID: "chatgpt", Name: "ChatGPT", Rule: RuleBodyMarker, Markers: []string{"unsupported_country"},
		Hosts:    []string{"openai.com", "chatgpt.com"},
		Requests: []Request{{URL: "https://chat.openai.com/cdn-cgi/trace", MaxBytes: 4 << 10, Redirects: maxRedirects}, {URL: "https://api.openai.com/compliance/cookie_requirements", MaxBytes: 4 << 10, Redirects: maxRedirects}},
		Domains:  []string{"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com"}},
	{ID: "claude", Name: "Claude", Rule: RuleTraceLoc, Blocked: []string{"AF", "BY", "CN", "CU", "HK", "IR", "KP", "MO", "RU", "SY"},
		Hosts:    []string{"claude.ai", "anthropic.com"},
		Requests: []Request{{URL: "https://claude.ai/cdn-cgi/trace", MaxBytes: 4 << 10, Redirects: maxRedirects}},
		Domains:  []string{"claude.ai", "anthropic.com", "claudeusercontent.com"}},
	{ID: "gemini", Name: "Gemini", Rule: RuleGoogleCountry, Shares: "youtube", Blocked: []string{"CN", "RU", "BY", "CU", "IR", "KP", "SY", "HK", "MO"},
		Domains: []string{"gemini.google.com", "bard.google.com", "generativelanguage.googleapis.com"}},
	{ID: "youtube", Name: "YouTube Premium", Rule: RuleBodyMarker, Markers: []string{"premium is not available in your region"},
		Hosts:    []string{"youtube.com", "google.com"},
		Requests: []Request{{URL: "https://www.youtube.com/premium?hl=en", MaxBytes: 96 << 10, Redirects: maxRedirects}},
		Domains:  []string{"youtube.com", "youtu.be", "googlevideo.com", "ytimg.com", "youtubei.googleapis.com"}},
	{ID: "netflix", Name: "Netflix", Rule: RuleStatusPair, Hosts: []string{"netflix.com"},
		Requests: []Request{{URL: "https://www.netflix.com/title/81280792"}, {URL: "https://www.netflix.com/title/70143836"}, {URL: "https://www.netflix.com/title/80018499"}},
		Domains:  []string{"netflix.com", "netflix.net", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net"}},
	{ID: "spotify", Name: "Spotify", Rule: RuleJSONCountry, Markers: []string{"not available in your country"},
		Hosts:    []string{"spotify.com"},
		Requests: []Request{{URL: "https://www.spotify.com/api/content/v1/country-selector?platform=web&format=json", MaxBytes: 16 << 10, Redirects: maxRedirects}},
		Domains:  []string{"spotify.com", "scdn.co", "spotifycdn.com", "spoti.fi"}},
}

// Definitions returns the built-in services in display order.
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

func Lookup(id string) (Definition, bool) {
	for _, def := range definitions {
		if def.ID == id {
			return def, true
		}
	}
	return Definition{}, false
}

func IDs() []string {
	ids := make([]string, len(definitions))
	for i, def := range definitions {
		ids[i] = def.ID
	}
	return ids
}
```

(Types `Outcome`, `Rule`, `Request`, `Definition`, `Result` and their constants from the Interfaces block go at the top of `check.go`.)

- [ ] **Step 4: Write `services/check.go`**

```go
package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// (Outcome, Rule, Request, Definition, Result declarations from Interfaces.)

var errPolicy = errors.New("services: redirect outside policy")

type response struct {
	status   int
	location string
	final    *url.URL
	body     []byte
}

// ProxyTransport sends every request through one pinned probe listener.
func ProxyTransport(proxy *url.URL) *http.Transport {
	return &http.Transport{
		Proxy:               http.ProxyURL(proxy),
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// Check runs defs for one node through transport. Each definition gets its
// own cookie jar and CheckTimeout budget; a definition with Shares evaluates
// the read of the definition it names, fetching it once when absent.
func Check(ctx context.Context, transport http.RoundTripper, defs []Definition) []Result {
	reads := make(map[string][]response)
	results := make([]Result, len(defs))
	for i, def := range defs {
		source := def
		if def.Shares != "" {
			source, _ = Lookup(def.Shares)
			for _, other := range defs {
				if other.ID == def.Shares {
					source = other
				}
			}
		}
		responses, ok := reads[source.ID]
		if !ok {
			responses = fetchAll(ctx, transport, source)
			reads[source.ID] = responses
		}
		results[i] = evaluate(def, responses)
	}
	return results
}

func fetchAll(ctx context.Context, transport http.RoundTripper, def Definition) []response {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	out := make([]response, 0, len(def.Requests))
	for _, request := range def.Requests {
		got, err := fetch(ctx, transport, jar, def.Hosts, request)
		if err != nil {
			return nil
		}
		out = append(out, got)
	}
	return out
}

func fetch(ctx context.Context, transport http.RoundTripper, jar http.CookieJar, hosts []string, request Request) (response, error) {
	client := &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if request.Redirects == 0 {
			return http.ErrUseLastResponse
		}
		if len(via) > request.Redirects || next.URL.Scheme != "https" || !allowedHost(next.URL.Hostname(), hosts) {
			return errPolicy
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, request.URL, nil)
	if err != nil {
		return response{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, request.MaxBytes))
	if err != nil && !errors.Is(err, io.EOF) {
		return response{}, err
	}
	if request.Redirects > 0 && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return response{}, errPolicy
	}
	return response{status: resp.StatusCode, location: resp.Header.Get("Location"), final: resp.Request.URL, body: body}, nil
}

func allowedHost(host string, hosts []string) bool {
	host = strings.ToLower(host)
	for _, allowed := range hosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func evaluate(def Definition, responses []response) Result {
	result := Result{Service: def.ID, Outcome: Failed}
	if len(responses) == 0 {
		return result
	}
	switch def.Rule {
	case RuleTraceLoc:
		result.Region = traceLoc(responses[0].body)
		if responses[0].status/100 == 2 && result.Region != "" {
			result.Outcome = verdict(def.Blocked, result.Region)
		}
	case RuleBodyMarker:
		last := responses[len(responses)-1]
		if len(responses) > 1 {
			result.Region = traceLoc(responses[0].body)
		} else {
			result.Region = quotedField(last.body, `"GL":"`)
		}
		switch {
		case last.status/100 != 2:
		case hasMarker(last.body, def.Markers):
			result.Outcome = Blocked
		case len(responses) > 1 || result.Region != "":
			result.Outcome = Usable
		}
	case RuleGoogleCountry:
		result.Region = quotedField(responses[0].body, `"GL":"`)
		if responses[0].status/100 == 2 && result.Region != "" {
			result.Outcome = verdict(def.Blocked, result.Region)
		}
	case RuleStatusPair:
		if len(responses) < 3 {
			return result
		}
		one, two := responses[0].status, responses[1].status
		switch {
		case one == http.StatusForbidden || two == http.StatusForbidden:
			result.Outcome = Blocked
		case one == http.StatusNotFound && two == http.StatusNotFound:
			result.Outcome = Blocked
		case available(one) || available(two):
			result.Outcome = Usable
			result.Region = localePath(responses[2].location)
		}
	case RuleJSONCountry:
		last := responses[0]
		result.Region = quotedField(last.body, `"countryCode":"`)
		if result.Region == "" && last.final != nil {
			result.Region = localePath(last.final.String())
		}
		switch {
		case last.status == http.StatusForbidden || last.status == http.StatusUnavailableForLegalReasons:
			result.Outcome = Blocked
		case last.status/100 != 2:
		case hasMarker(last.body, def.Markers):
			result.Outcome = Blocked
		default:
			result.Outcome = Usable
		}
	}
	return result
}

func available(status int) bool {
	return status/100 == 2 || status == http.StatusMovedPermanently || status == http.StatusFound
}

func verdict(blocked []string, region string) Outcome {
	for _, code := range blocked {
		if code == region {
			return Blocked
		}
	}
	return Usable
}

func traceLoc(body []byte) string {
	for _, line := range bytes.Split(body, []byte("\n")) {
		if value, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("loc=")); ok {
			return region(string(value))
		}
	}
	return ""
}

func quotedField(body []byte, key string) string {
	_, rest, ok := bytes.Cut(body, []byte(key))
	if !ok {
		return ""
	}
	value, _, ok := bytes.Cut(rest, []byte(`"`))
	if !ok {
		return ""
	}
	return region(string(value))
}

// localePath reads a region from a locale path segment such as /us-en/.
func localePath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	segment, _, _ := strings.Cut(strings.TrimPrefix(parsed.Path, "/"), "/")
	code, _, _ := strings.Cut(segment, "-")
	return region(code)
}

func hasMarker(body []byte, markers []string) bool {
	lower := bytes.ToLower(body)
	for _, marker := range markers {
		if bytes.Contains(lower, []byte(marker)) {
			return true
		}
	}
	return false
}

// region accepts only a two-letter code, so no other body text leaves the package.
func region(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 2 || value[0] < 'A' || value[0] > 'Z' || value[1] < 'A' || value[1] > 'Z' {
		return ""
	}
	return value
}
```

Note the YouTube cap: `io.LimitReader` stops at 96 KiB, so a `GL` beyond the cap is not seen and the check is `error` (`TestCheckCapStopsReading`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -tags ci -count=1 ./services`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l services
git add services
git commit -m "feat(services): add built-in service checks"
```

---

### Task 2: `config` - `[[services]]` intent and writer

**Files:**
- Modify: `config/types.go` (Snapshot, new `Service`), `config/toml_decode.go` (`configDoc`, `loadConfig`), `config/load.go` (`applyDefaults`, `validateSnapshot`, `cloneSnapshot`)
- Create: `config/service_write.go`
- Test: `config/service_test.go` (new)

**Interfaces:**
- Consumes: `services.Lookup`, `services.IDs` (Task 1).
- Produces:

```go
type Service struct {
	ID              string
	Enabled         bool
	Regions         []string
	CheckInterval   time.Duration
	Failures        int
	Automatic       bool
	DomainsResource string
}

const (
	DefaultServiceInterval = 30 * time.Minute
	MinServiceInterval     = 5 * time.Minute
	DefaultServiceFailures = 2
)

// Snapshot gains: Services []Service
func PutService(path string, current Snapshot, service Service) error
```

- [ ] **Step 1: Write the failing tests** (`config/service_test.go`)

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestServicesDecodeWithDefaults(t *testing.T) {
	snapshot, err := Load(writeConfig(t, "[[services]]\nid = \"chatgpt\"\nenabled = true\nregions = [\"US\", \"JP\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.Services
	if len(got) != 1 || got[0].ID != "chatgpt" || !got[0].Enabled || got[0].Automatic ||
		got[0].CheckInterval != DefaultServiceInterval || got[0].Failures != DefaultServiceFailures || strings.Join(got[0].Regions, ",") != "US,JP" {
		t.Fatalf("services = %+v", got)
	}
}

func TestServicesRejectInvalidIntent(t *testing.T) {
	cases := map[string]string{
		"unknown id":        "[[services]]\nid = \"tiktok\"\n",
		"duplicate id":      "[[services]]\nid = \"claude\"\n[[services]]\nid = \"claude\"\n",
		"bad region":        "[[services]]\nid = \"claude\"\nregions = [\"usa\"]\n",
		"short interval":    "[[services]]\nid = \"claude\"\ncheck_interval = \"1m\"\n",
		"failures range":    "[[services]]\nid = \"claude\"\nfailures = 11\n",
		"unknown key":       "[[services]]\nid = \"claude\"\nauto = true\n",
		"dangling resource": "[[services]]\nid = \"claude\"\ndomains_resource = \"missing\"\n",
	}
	for name, body := range cases {
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestServicesDomainsResourceMustBeDomainRuleSet(t *testing.T) {
	dir := writeConfig(t, "[[services]]\nid = \"claude\"\ndomains_resource = \"cidrs\"\n")
	resources := "[[resources]]\nid = \"cidrs\"\nkind = \"rule-set\"\nformat = \"text\"\nrule_type = \"ipcidr\"\nurl = \"https://example.com/cidrs.txt\"\nenabled = true\ninterval = \"24h\"\n"
	if err := os.WriteFile(filepath.Join(dir, "resources.toml"), []byte(resources), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "domains_resource") {
		t.Fatalf("ipcidr resource accepted as service domains: %v", err)
	}
}

func TestPutServiceRoundTrips(t *testing.T) {
	dir := writeConfig(t, "[monitor]\nenabled = true\n")
	path := filepath.Join(dir, "config.toml")
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Service{ID: "netflix", Enabled: true, Regions: []string{"US"}, CheckInterval: 45 * time.Minute, Failures: 3, Automatic: true}
	if err := PutService(path, current, want); err != nil {
		t.Fatal(err)
	}
	next, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Services) != 1 || next.Services[0].ID != "netflix" || !next.Services[0].Automatic || next.Services[0].CheckInterval != 45*time.Minute || !next.Monitor.Enabled {
		t.Fatalf("after PutService: %+v", next.Services)
	}
	want.Automatic = false
	if err := PutService(path, next, want); err != nil {
		t.Fatal(err)
	}
	if again, _ := Load(dir); len(again.Services) != 1 || again.Services[0].Automatic {
		t.Fatalf("PutService did not replace in place: %+v", again.Services)
	}
}
```

Before writing, read `config/load_test.go` for the existing temp-dir helper; if one with the same shape exists, use it instead of `writeConfig`.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run 'TestServices|TestPutService' ./config`
Expected: FAIL, `undefined: DefaultServiceInterval` / `snapshot.Services undefined`.

- [ ] **Step 3: Implement**

`config/types.go`: add `Services []Service` to `Snapshot` and the `Service` type plus constants from Interfaces.

`config/toml_decode.go`: add to `configDoc`:

```go
	Services []serviceDoc `toml:"services"`
```

and

```go
type serviceDoc struct {
	ID              string   `toml:"id"`
	Enabled         bool     `toml:"enabled,omitempty"`
	Regions         []string `toml:"regions,omitempty"`
	CheckInterval   *string  `toml:"check_interval,omitempty"`
	Failures        *int     `toml:"failures,omitempty"`
	Automatic       bool     `toml:"automatic,omitempty"`
	DomainsResource string   `toml:"domains_resource,omitempty"`
}
```

In `loadConfig`, after the monitor block, decode each entry with the existing `durationValue` helper:

```go
	for i, doc := range document.Services {
		key := fmt.Sprintf("services[%d]", i)
		interval, err := durationValue(path, key+".check_interval", doc.CheckInterval)
		if err != nil {
			return err
		}
		failures := 0
		if doc.Failures != nil {
			failures = *doc.Failures
		}
		snapshot.Services = append(snapshot.Services, Service{ID: doc.ID, Enabled: doc.Enabled, Regions: append([]string(nil), doc.Regions...),
			CheckInterval: interval, Failures: failures, Automatic: doc.Automatic, DomainsResource: doc.DomainsResource})
	}
```

(Match the actual local variable names in `loadConfig`; the snapshot under construction may be named differently.)

`config/load.go` `applyDefaults`: for each service, zero `CheckInterval` becomes `DefaultServiceInterval`, zero `Failures` becomes `DefaultServiceFailures`.

`config/load.go` `validateSnapshot`: add `validateServices(snapshot)`:

```go
func validateServices(snapshot Snapshot) error {
	seen := make(map[string]bool, len(snapshot.Services))
	for i, service := range snapshot.Services {
		key := fmt.Sprintf("config.toml: services[%d]", i)
		if _, ok := services.Lookup(service.ID); !ok {
			return fmt.Errorf("%s.id: unknown service %q", key, service.ID)
		}
		if seen[service.ID] {
			return fmt.Errorf("%s.id: duplicate service %q", key, service.ID)
		}
		seen[service.ID] = true
		for _, region := range service.Regions {
			if len(region) != 2 || strings.ToUpper(region) != region || region[0] < 'A' || region[0] > 'Z' || region[1] < 'A' || region[1] > 'Z' {
				return fmt.Errorf("%s.regions: %q is not a two-letter country code", key, region)
			}
		}
		if service.CheckInterval < MinServiceInterval {
			return fmt.Errorf("%s.check_interval: must be at least %s", key, MinServiceInterval)
		}
		if service.Failures < 1 || service.Failures > 10 {
			return fmt.Errorf("%s.failures: must be between 1 and 10", key)
		}
		if service.DomainsResource != "" {
			resource, ok := findResource(snapshot.Resources, service.DomainsResource)
			if !ok || !resource.Enabled || resource.Kind != ResourceRuleSet && resource.Kind != ResourceRuleProvider || resource.RuleType != RuleDomain {
				return fmt.Errorf("%s.domains_resource: %q must be an enabled domain rule-set resource", key, service.DomainsResource)
			}
		}
	}
	return nil
}

func findResource(resources []Resource, id string) (Resource, bool) {
	for _, resource := range resources {
		if resource.ID == id {
			return resource, true
		}
	}
	return Resource{}, false
}
```

If `validateSnapshot` reports errors through a structured error type with file and key fields, use that type in place of `fmt.Errorf` (read the top of `validateSnapshot` first).

`cloneSnapshot`: clone `Services` and each `Regions` slice.

`config/service_write.go`:

```go
package config

// PutService adds or replaces one [[services]] entry in config.toml, leaving
// every other table as written.
func PutService(path string, current Snapshot, service Service) error {
	var document configDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	next := cloneSnapshot(current)
	replaced := false
	for i := range next.Services {
		if next.Services[i].ID == service.ID {
			next.Services[i], replaced = service, true
		}
	}
	if !replaced {
		next.Services = append(next.Services, service)
	}
	if err := validateSnapshot(next); err != nil {
		return err
	}
	interval := service.CheckInterval.String()
	failures := service.Failures
	entry := serviceDoc{ID: service.ID, Enabled: service.Enabled, Regions: append([]string(nil), service.Regions...),
		CheckInterval: &interval, Failures: &failures, Automatic: service.Automatic, DomainsResource: service.DomainsResource}
	for i := range document.Services {
		if document.Services[i].ID == service.ID {
			document.Services[i] = entry
			return writeTOML(path, document)
		}
	}
	document.Services = append(document.Services, entry)
	return writeTOML(path, document)
}
```

`time.Duration.String()` renders `45m0s`; confirm `durationValue` parses that form (it uses `time.ParseDuration`, which does). If the existing writers format durations through a helper, use it instead.

- [ ] **Step 4: Run tests**

Run: `go test -tags ci -count=1 ./config`
Expected: PASS (whole package, so existing load tests catch regressions).

- [ ] **Step 5: Commit**

```bash
gofmt -l config
git add config
git commit -m "feat(config): add services intent"
```

---

### Task 3: `mihomo` - probe pool replaces the single stream listener

**Files:**
- Create: `mihomo/probe.go`; Test: `mihomo/probe_test.go`, `mihomo/probe_runtime_test.go`
- Modify: `mihomo/stream.go` (keep `pullThrough`, `classify`, constants; remove `StreamProbeName`, `StreamListener`, `Controller.StreamPull`), `mihomo/render.go` (`ControllerSettings.StreamProbe` -> `Probes`, `renderStreamProbe` -> `renderProbes`, `StreamProbe` -> `ProbeListeners`, render condition), `mihomo/binary.go` only if its fixture names change (it does not need to)
- Modify callers: `app/stream_probe.go`, `app/lifecycle.go` (lines near 103-118, 785-800, 1016-1050), `app/runtime.go` (`streamProbe` field), and every test that references the removed names (`rg -n 'StreamProbeName|StreamListener|StreamProbe\b|streamProbe' --type go`)

**Interfaces:**
- Consumes: `config.Snapshot.Services` (Task 2) for the render condition.
- Produces:

```go
const ProbeSlots = 4

func ProbeName(slot int) string // "clashpulse-probe-1" .. "clashpulse-probe-4"
func IsProbeName(name string) bool

type ProbeListener struct {
	Port               int
	Username, Password string
}

// ControllerSettings: StreamProbe StreamListener -> Probes []ProbeListener (len 0 or ProbeSlots)

func ProbeListeners(generated []byte) ([]ProbeListener, error)
func NeedsProbes(intent config.Snapshot) bool // steady_stream or any enabled service

type ProbePool struct { /* controller, listeners, free chan int */ }
func NewProbePool(controller *Controller, listeners []ProbeListener) *ProbePool
func (p *ProbePool) Size() int
func (p *ProbePool) Acquire(ctx context.Context, proxy string) (*ProbeLease, error)

type ProbeLease struct { /* pool, slot */ }
func (l *ProbeLease) Transport() *http.Transport
func (l *ProbeLease) StreamPull(ctx context.Context, rawURL string) StreamResult
func (l *ProbeLease) Release()
```

- [ ] **Step 1: Write failing tests** (`mihomo/probe_test.go`)

```go
package mihomo

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
)

func testProbes() []ProbeListener {
	probes := make([]ProbeListener, ProbeSlots)
	for i := range probes {
		probes[i] = ProbeListener{Port: 17891 + i, Username: "u", Password: "p"}
	}
	return probes
}

func TestRenderProbesForServicesWithoutSteadyStream(t *testing.T) {
	intent := minimalIntent() // existing helper in render_test.go; use whatever the file names it
	intent.Services = []config.Service{{ID: "claude", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2}}
	generated := renderForTest(t, intent, ControllerSettings{Probes: testProbes()}) // wrap the existing render test helper
	listeners, err := ProbeListeners(generated)
	if err != nil || len(listeners) != ProbeSlots || listeners[3].Port != 17894 {
		t.Fatalf("listeners = %+v, %v", listeners, err)
	}
	for slot := 1; slot <= ProbeSlots; slot++ {
		if !strings.Contains(string(generated), "name: "+ProbeName(slot)) {
			t.Fatalf("missing %s", ProbeName(slot))
		}
	}
}

func TestRenderOmitsProbesWhenNothingNeedsThem(t *testing.T) {
	intent := minimalIntent()
	intent.Services = []config.Service{{ID: "claude", Enabled: false, CheckInterval: 30 * time.Minute, Failures: 2}}
	generated := renderForTest(t, intent, ControllerSettings{})
	if listeners, _ := ProbeListeners(generated); len(listeners) != 0 || strings.Contains(string(generated), "clashpulse-probe") {
		t.Fatalf("probes rendered for a disabled service")
	}
}

func TestProbePoolLeasesAreExclusive(t *testing.T) {
	var selects atomic.Int32
	controller := fakeSelectController(t, func(group, proxy string) { selects.Add(1) }) // httptest controller answering PUT /proxies/{group}
	pool := NewProbePool(controller, testProbes())
	ctx := context.Background()
	leases := make([]*ProbeLease, 0, ProbeSlots)
	seen := map[int]bool{}
	for i := 0; i < ProbeSlots; i++ {
		lease, err := pool.Acquire(ctx, "node")
		if err != nil || seen[lease.slot] {
			t.Fatalf("lease %d: slot %d err %v", i, lease.slot, err)
		}
		seen[lease.slot] = true
		leases = append(leases, lease)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(waitCtx, "node"); err == nil {
		t.Fatal("a fifth lease was granted while four are held")
	}
	leases[0].Release()
	if lease, err := pool.Acquire(ctx, "node"); err != nil || lease.slot != leases[0].slot {
		t.Fatalf("released slot not reused: %v", err)
	}
	if selects.Load() != ProbeSlots+1 {
		t.Fatalf("selects = %d", selects.Load())
	}
}

func TestProbePoolReleasesSlotWhenSelectFails(t *testing.T) {
	controller := fakeSelectController(t, nil, withSelectStatus(500))
	pool := NewProbePool(controller, testProbes()[:1])
	if _, err := pool.Acquire(context.Background(), "node"); err == nil {
		t.Fatal("failed select produced a lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// A freed slot reaches Select again (and fails on 500); a leaked one waits out the deadline.
	if _, err := pool.Acquire(ctx, "node"); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire = %v, want a select error", err)
	}
}
```

Read `mihomo/render_test.go` and `mihomo/controller_test.go` first and reuse their render and fake-controller helpers; name the wrappers above after them rather than inventing new ones. `fakeSelectController` must answer only `PUT /proxies/<group>`.

`mihomo/probe_runtime_test.go` (skipped when `mihomo` is not on `PATH`):

```go
func TestProbeListenersFollowOnlyTheirOwnGroup(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo not installed")
	}
	// Two local HTTP servers act as two "nodes": an http proxy on 127.0.0.1
	// that answers every request with its own name ("node-a" / "node-b").
	// Render a config with proxies node-a/node-b (type: http, server 127.0.0.1)
	// and the 4-slot pool, start mihomo with -d tmp -f config, wait for the
	// controller, select node-a on slot 1 and node-b on slot 2, then GET
	// http://192.0.2.10/ through each listener and assert slot 1 answered
	// "node-a" and slot 2 "node-b"; then swap selections and assert again.
}
```

Write this test in full in Step 1 using the start/stop helpers in `mihomo/process_test.go` (read it first); its acceptance is the two assertions in the comment.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run 'TestRenderProbes|TestRenderOmitsProbes|TestProbePool|TestProbeListeners' ./mihomo`
Expected: FAIL, `undefined: ProbeListener`.

- [ ] **Step 3: Implement `mihomo/probe.go`**

```go
package mihomo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/monitor"
	"github.com/fishman/clashpulse/services"
)

const ProbeSlots = 4

const probePrefix = "clashpulse-probe-"

func ProbeName(slot int) string { return probePrefix + strconv.Itoa(slot) }

func IsProbeName(name string) bool {
	slot, err := strconv.Atoi(strings.TrimPrefix(name, probePrefix))
	return strings.HasPrefix(name, probePrefix) && err == nil && slot >= 1 && slot <= ProbeSlots
}

// ProbeListener is one pinned loopback listener; its credential lives only in
// memory and is never part of a result or error.
type ProbeListener struct {
	Port               int
	Username, Password string
}

func NeedsProbes(intent config.Snapshot) bool {
	if intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream {
		return true
	}
	for _, service := range intent.Services {
		if service.Enabled {
			return true
		}
	}
	return false
}

// ProbePool leases the listeners; a lease pins its slot's group to one node
// until Release, so concurrent checks never re-point each other's slot.
type ProbePool struct {
	controller *Controller
	listeners  []ProbeListener
	free       chan int
}

type ProbeLease struct {
	pool *ProbePool
	slot int
}

func NewProbePool(controller *Controller, listeners []ProbeListener) *ProbePool {
	pool := &ProbePool{controller: controller, listeners: append([]ProbeListener(nil), listeners...), free: make(chan int, len(listeners))}
	for slot := 1; slot <= len(listeners); slot++ {
		pool.free <- slot
	}
	return pool
}

func (p *ProbePool) Size() int { return len(p.listeners) }

func (p *ProbePool) Acquire(ctx context.Context, proxy string) (*ProbeLease, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot := <-p.free:
		if err := p.controller.Select(ctx, ProbeName(slot), proxy); err != nil {
			p.free <- slot
			return nil, fmt.Errorf("mihomo: pin probe slot: %w", err)
		}
		return &ProbeLease{pool: p, slot: slot}, nil
	}
}

func (l *ProbeLease) Release() { l.pool.free <- l.slot }

func (l *ProbeLease) Transport() *http.Transport {
	listener := l.pool.listeners[l.slot-1]
	proxy := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", listener.Port), User: url.UserPassword(listener.Username, listener.Password)}
	return services.ProxyTransport(proxy)
}

func (l *ProbeLease) StreamPull(ctx context.Context, rawURL string) StreamResult {
	transport := l.Transport()
	transport.TLSHandshakeTimeout = StreamStallTimeout
	defer transport.CloseIdleConnections()
	return pullThrough(ctx, transport, rawURL, StreamStallTimeout, StreamPullTimeout)
}
```

If `mihomo` importing `monitor` creates a cycle (check `go list -deps ./monitor | grep mihomo`), compare against the string constant the render code already uses for `SwitchSteadyStream` instead.

`mihomo/render.go`:
- `ControllerSettings.StreamProbe StreamListener` becomes `Probes []ProbeListener`.
- Replace the `if intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream {...}` block with:

```go
	if NeedsProbes(intent) {
		if !capability.SupportsPinnedListener {
			resource := "services"
			if intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream {
				resource = "monitor.switch_policy"
			}
			return nil, &CapabilityError{ResourceID: resource, Kind: "pinned-listener"}
		}
		if err := renderProbes(document, controller.Probes); err != nil {
			return nil, err
		}
	}
```

- `renderProbes` is `renderStreamProbe` generalised: require `len(listeners) == ProbeSlots` with every port, user, password set; reject a source `proxies`/`proxy-groups` entry for which `IsProbeName(name)` holds; append one `select`/`include-all: true` group and one `mixed` listener (127.0.0.1, its port, `proxy: ProbeName(slot)`, one user) per slot, slots 1..4 in order.
- `ProbeListeners(generated)` is `StreamProbe` generalised: collect listeners whose name passes `IsProbeName`, ordered by slot, each validated as today; return nil when none, and an error unless exactly `ProbeSlots` are present.

`app/stream_probe.go`: `newStreamListener` becomes `newProbeListeners() ([]mihomo.ProbeListener, error)` returning `ProbeSlots` listeners with distinct free ports (hold all four `net.Listen` sockets open until every port is chosen, then close them). `streamProber` becomes:

```go
type streamProber struct{ pool *mihomo.ProbePool }

func (p streamProber) StreamPull(ctx context.Context, proxy, url string) monitor.StreamSample {
	lease, err := p.pool.Acquire(ctx, proxy)
	if err != nil {
		return monitor.StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Outcome: monitor.StreamOutcome("error")}
	}
	defer lease.Release()
	result := lease.StreamPull(ctx, url)
	return monitor.StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Bytes: result.Bytes,
		FirstByte: result.FirstByte, Duration: result.Duration, Outcome: monitor.StreamOutcome(result.Outcome)}
}
```

`app/runtime.go`: field `streamProbe mihomo.StreamListener` becomes `probes []mihomo.ProbeListener` plus `probePool *mihomo.ProbePool` (created where the controller becomes ready, from `s.probes`; nil when `len(s.probes) == 0`).

`app/lifecycle.go`:
- render path (around line 103): `if mihomo.NeedsProbes(intent) { listeners = s.probes; if len(listeners) == 0 { listeners, err = newProbeListeners() } }`, pass `Probes: listeners`, then `s.probes = listeners`.
- start path (around line 785): re-check every port in `s.probes`; on any conflict clear `s.probes` and return `core.ActivationStreamProbePort` as today.
- `refreshGroups` (around line 1016): replace `group.Name == mihomo.StreamProbeName` with `mihomo.IsProbeName(group.Name)`; stream members come from slot 1 (`group.Name == mihomo.ProbeName(1)`); the `DeleteFunc` and the `group.Selected != ...` check use `mihomo.IsProbeName`.
- Where the monitor scheduler is created, pass `streamProber{pool: s.probePool}`.

- [ ] **Step 4: Run tests**

Run: `go test -tags ci -count=1 ./mihomo ./app`
Expected: PASS, including existing steady-stream tests updated for the new names, and `TestProbeListenersFollowOnlyTheirOwnGroup` (PASS on a host with Mihomo, SKIP elsewhere; record which in the commit body).

- [ ] **Step 5: Commit**

```bash
gofmt -l mihomo app
git add mihomo app
git commit -m "feat(mihomo): replace the stream probe listener with a four-slot pool"
```

---

### Task 4: `mihomo` - service groups and routing rules

**Files:**
- Modify: `mihomo/render.go` (call `renderServices` immediately before `renderFilters`)
- Create: `mihomo/services.go`; Test: `mihomo/services_test.go`

**Interfaces:**
- Consumes: `config.Service` (Task 2), `services.Lookup` (Task 1), `filters.Name` (existing: provider name `managed-<id>`).
- Produces:

```go
func ServiceGroupName(name string) string // "ClashPulse - " + name
func ServiceGroupFor(groupName string) (serviceID string, ok bool)
```

- [ ] **Step 1: Write failing tests** (`mihomo/services_test.go`)

```go
func TestRenderServicesAddsGroupAndRulesAheadOfProfile(t *testing.T) {
	intent := minimalIntent()
	intent.Services = []config.Service{
		{ID: "netflix", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2},
		{ID: "claude", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2},
		{ID: "spotify", Enabled: false, CheckInterval: 30 * time.Minute, Failures: 2},
	}
	generated := renderForTest(t, intent, ControllerSettings{Probes: testProbes()})
	var document struct {
		Groups []map[string]any `yaml:"proxy-groups"`
		Rules  []string         `yaml:"rules"`
	}
	if err := yaml.Unmarshal(generated, &document); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, group := range document.Groups {
		if name, _ := group["name"].(string); strings.HasPrefix(name, "ClashPulse - ") {
			names = append(names, name)
			if group["type"] != "select" || group["include-all"] != true {
				t.Fatalf("group %v", group)
			}
		}
	}
	if strings.Join(names, ",") != "ClashPulse - Claude,ClashPulse - Netflix" {
		t.Fatalf("service groups = %v (definition order, enabled only)", names)
	}
	if document.Rules[0] != "DOMAIN-SUFFIX,claude.ai,ClashPulse - Claude" {
		t.Fatalf("first rule = %q", document.Rules[0])
	}
	last := document.Rules[len(document.Rules)-1]
	if strings.Contains(last, "ClashPulse") {
		t.Fatalf("profile rules were not kept after service rules: %q", last)
	}
	for _, rule := range document.Rules {
		if strings.Contains(rule, "spotify") {
			t.Fatalf("disabled service rendered rule %q", rule)
		}
	}
}

func TestRenderServicesUsesDomainsResource(t *testing.T) {
	intent, paths := intentWithDomainRuleSet(t, "openai-domains") // builds an enabled domain rule-set resource and its managed file
	intent.Services = []config.Service{{ID: "chatgpt", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2, DomainsResource: "openai-domains"}}
	generated := renderWithPaths(t, intent, paths, ControllerSettings{Probes: testProbes()})
	if !strings.Contains(string(generated), "RULE-SET,managed-openai-domains,ClashPulse - ChatGPT") || strings.Contains(string(generated), "DOMAIN-SUFFIX,openai.com,") {
		t.Fatalf("domains_resource not used:\n%s", generated)
	}
}

func TestRenderServicesRejectsNameCollision(t *testing.T) {
	profile := []byte("proxies:\n  - {name: node-a, type: socks5, server: 192.0.2.1, port: 1080}\nproxy-groups:\n  - {name: ClashPulse - Netflix, type: select, proxies: [node-a]}\nrules:\n  - MATCH,DIRECT\n")
	intent := minimalIntent()
	intent.Services = []config.Service{{ID: "netflix", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2}}
	_, err := renderProfile(profile, intent, ControllerSettings{Probes: testProbes()})
	if err == nil || !strings.Contains(err.Error(), "netflix") {
		t.Fatalf("collision err = %v", err)
	}
}

func TestServiceGroupFor(t *testing.T) {
	if id, ok := ServiceGroupFor("ClashPulse - YouTube Premium"); !ok || id != "youtube" {
		t.Fatalf("ServiceGroupFor = %q %v", id, ok)
	}
	if _, ok := ServiceGroupFor("Main"); ok {
		t.Fatal("profile group taken for a service group")
	}
}
```

As in Task 3, adapt the helper names to the existing ones in `mihomo/render_test.go`; add `intentWithDomainRuleSet` next to whatever helper the existing filter render tests use to create a managed rule-set file.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run 'TestRenderServices|TestServiceGroupFor' ./mihomo`
Expected: FAIL, `undefined: ServiceGroupFor`.

- [ ] **Step 3: Implement `mihomo/services.go`**

```go
package mihomo

import (
	"fmt"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/filters"
	"github.com/fishman/clashpulse/services"
)

const serviceGroupPrefix = "ClashPulse - "

func ServiceGroupName(name string) string { return serviceGroupPrefix + name }

func ServiceGroupFor(groupName string) (string, bool) {
	for _, def := range services.Definitions() {
		if ServiceGroupName(def.Name) == groupName {
			return def.ID, true
		}
	}
	return "", false
}

// renderServices adds one select group per enabled service and its rules ahead
// of the profile's rules; renderFilters later prepends filter rules ahead of these.
func renderServices(document map[string]any, intent config.Snapshot) error {
	enabled := make(map[string]config.Service, len(intent.Services))
	for _, service := range intent.Services {
		if service.Enabled {
			enabled[service.ID] = service
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	groups, ok := document["proxy-groups"].([]any)
	if !ok && document["proxy-groups"] != nil {
		return fmt.Errorf("mihomo: source proxy-groups must be a list")
	}
	existing := make(map[string]bool)
	for _, section := range []string{"proxies", "proxy-groups"} {
		items, _ := document[section].([]any)
		for _, item := range items {
			if entry, ok := item.(map[string]any); ok {
				if name, ok := entry["name"].(string); ok {
					existing[name] = true
				}
			}
		}
	}
	sourceRules, ok := document["rules"].([]any)
	if !ok && document["rules"] != nil {
		return fmt.Errorf("mihomo: source rules must be a list")
	}
	var rules []any
	for _, def := range services.Definitions() {
		service, ok := enabled[def.ID]
		if !ok {
			continue
		}
		name := ServiceGroupName(def.Name)
		if existing[name] {
			return fmt.Errorf("mihomo: source profile already defines %q, the group for service %s", name, def.ID)
		}
		groups = append(groups, map[string]any{"name": name, "type": "select", "include-all": true})
		if service.DomainsResource != "" {
			rules = append(rules, "RULE-SET,"+filters.Name(service.DomainsResource)+","+name)
			continue
		}
		for _, domain := range def.Domains {
			rules = append(rules, "DOMAIN-SUFFIX,"+domain+","+name)
		}
	}
	document["proxy-groups"] = groups
	document["rules"] = append(rules, sourceRules...)
	return nil
}
```

In `Render`, call `renderServices(document, intent)` immediately before `renderFilters(document, registry)` and return its error.

- [ ] **Step 4: Run tests**

Run: `go test -tags ci -count=1 ./mihomo`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l mihomo
git add mihomo
git commit -m "feat(mihomo): render service groups and routing rules"
```

---

### Task 5: `monitor` - service scheduler and switching policy

**Files:**
- Create: `monitor/service.go`; Test: `monitor/service_test.go`

**Interfaces:**
- Consumes: `services.Result`, `services.Outcome` constants (Task 1).
- Produces:

```go
type ServiceChecker interface {
	// CheckServices checks ids on one node and returns one result per id.
	CheckServices(ctx context.Context, proxy string, ids []string) []services.Result
}

type ServiceTarget struct {
	ID        string
	Group     string
	Selected  string
	Members   []string
	Interval  time.Duration
	Failures  int
	Automatic bool
	Regions   []string
}

type ServicePlan struct {
	Targets   []ServiceTarget
	Latency   map[string]time.Duration // proxy -> median latency
	Unhealthy map[string]bool          // proxy -> latest latency probe failed
	Cooldown  time.Duration
	Slots     int
}

type ServiceCheck struct {
	Service, Proxy string
	Result         services.Result
	At             time.Time
}

type ServiceSwitch struct {
	Service, Group, Old, New string
	Region                   string
	Reason                   string // "blocked", "failing"
}

type ServiceBatch struct {
	Checks    []ServiceCheck
	Switches  []ServiceSwitch
	Exhausted []string          // services whose search found no usable candidate
	Next      map[string]time.Time
}

const MaxServiceCandidates = 8

func NewServiceScheduler(checker ServiceChecker, onBatch func(ServiceBatch)) *ServiceScheduler
func (s *ServiceScheduler) SetPlan(plan ServicePlan)
func (s *ServiceScheduler) Request(service string)
func (s *ServiceScheduler) Run(ctx context.Context) error

// Pure policy, exported for tests and for the app's switch guard:
func ServiceFailing(target ServiceTarget, recent []services.Outcome, unhealthy bool) bool
func ServiceCandidates(target ServiceTarget, last map[string]services.Result, latency map[string]time.Duration) []string
```

Behaviour the tests pin (from the spec):
- `ServiceFailing`: the last `target.Failures` outcomes for the selected node all count as failures; `Blocked` counts; `Failed` counts only when `unhealthy` is true; `Usable` resets.
- `ServiceCandidates`: members other than `Selected`; excluded when their last result for this service is `Blocked`; when `Regions` is non-empty, a member with a known region outside it is excluded and a member with no result is kept (it will be checked); ordered by latency ascending, members without latency last, then by name; truncated to `MaxServiceCandidates`.
- Scheduler: a due target checks only `Selected`; a changed `Selected` in `SetPlan` makes that target due now; `Request` makes it due now; targets on the same node due together go in one `CheckServices` call; at most `plan.Slots` calls in flight; when `ServiceFailing` and `Automatic` and outside cooldown, a search checks candidates in waves of `min(4, plan.Slots)` and emits one `ServiceSwitch` for the lowest-latency `Usable` candidate in the first wave that has one; none in 8 -> `Exhausted`; after emitting a switch the service is in cooldown for `plan.Cooldown`; with no enabled targets the loop holds no timer.

- [ ] **Step 1: Write failing tests** (`monitor/service_test.go`)

```go
package monitor

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fishman/clashpulse/services"
)

type fakeChecker struct {
	mu       sync.Mutex
	results  map[string]services.Outcome // proxy -> outcome for every service
	regions  map[string]string
	calls    [][]string                  // proxy followed by ids
	inFlight int
	peak     int
	gate     chan struct{}
}

func (f *fakeChecker) CheckServices(ctx context.Context, proxy string, ids []string) []services.Result {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{proxy}, ids...))
	f.inFlight++
	f.peak = max(f.peak, f.inFlight)
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
	out := make([]services.Result, len(ids))
	for i, id := range ids {
		outcome, ok := f.results[proxy]
		if !ok {
			outcome = services.Usable
		}
		out[i] = services.Result{Service: id, Outcome: outcome, Region: f.regions[proxy]}
	}
	return out
}

func target(id, selected string, members ...string) ServiceTarget {
	return ServiceTarget{ID: id, Group: "ClashPulse - " + id, Selected: selected, Members: members, Interval: 30 * time.Minute, Failures: 2, Automatic: true}
}

func TestServiceFailing(t *testing.T) {
	tg := target("claude", "a", "a")
	cases := []struct {
		recent    []services.Outcome
		unhealthy bool
		want      bool
	}{
		{[]services.Outcome{services.Blocked, services.Blocked}, false, true},
		{[]services.Outcome{services.Usable, services.Blocked}, false, false},
		{[]services.Outcome{services.Failed, services.Failed}, false, false},
		{[]services.Outcome{services.Failed, services.Failed}, true, true},
		{[]services.Outcome{services.Blocked, services.Failed}, false, false},
		{[]services.Outcome{services.Blocked}, false, false},
	}
	for _, tc := range cases {
		if got := ServiceFailing(tg, tc.recent, tc.unhealthy); got != tc.want {
			t.Fatalf("ServiceFailing(%v, unhealthy=%v) = %v", tc.recent, tc.unhealthy, got)
		}
	}
}

func TestServiceCandidatesOrderAndFilter(t *testing.T) {
	members := []string{"cur", "jp-blocked", "us-slow", "us-fast", "unknown", "de", "n1", "n2", "n3", "n4", "n5", "n6"}
	tg := target("chatgpt", "cur", members...)
	tg.Regions = []string{"US", "JP"}
	last := map[string]services.Result{
		"jp-blocked": {Outcome: services.Blocked, Region: "JP"},
		"us-slow":    {Outcome: services.Usable, Region: "US"},
		"us-fast":    {Outcome: services.Usable, Region: "US"},
		"de":         {Outcome: services.Usable, Region: "DE"},
	}
	latency := map[string]time.Duration{"us-slow": 300 * time.Millisecond, "us-fast": 80 * time.Millisecond, "n1": 50 * time.Millisecond}
	got := ServiceCandidates(tg, last, latency)
	want := []string{"n1", "us-fast", "us-slow", "n2", "n3", "n4", "n5", "n6"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func runScheduler(t *testing.T, checker *fakeChecker, plan ServicePlan) (*ServiceScheduler, chan ServiceBatch) {
	t.Helper()
	batches := make(chan ServiceBatch, 16)
	s := NewServiceScheduler(checker, func(b ServiceBatch) { batches <- b })
	s.SetPlan(plan)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && err != context.Canceled {
			t.Errorf("Run: %v", err)
		}
	})
	return s, batches
}

func nextBatch(t *testing.T, batches chan ServiceBatch) ServiceBatch {
	t.Helper()
	select {
	case b := <-batches:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("no batch")
		return ServiceBatch{}
	}
}

func TestServiceSchedulerChecksOnlySelectedAndSharesNode(t *testing.T) {
	checker := &fakeChecker{}
	_, batches := runScheduler(t, checker, ServicePlan{Slots: 4, Cooldown: time.Minute,
		Targets: []ServiceTarget{target("youtube", "a", "a", "b"), target("gemini", "a", "a", "b"), target("claude", "b", "a", "b")}})
	b := nextBatch(t, batches)
	if len(b.Checks) != 3 {
		t.Fatalf("checks = %+v", b.Checks)
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if len(checker.calls) != 2 {
		t.Fatalf("calls = %v, want one per node", checker.calls)
	}
	for _, call := range checker.calls {
		if call[0] == "a" && !slices.Equal(call[1:], []string{"youtube", "gemini"}) {
			t.Fatalf("node a call = %v", call)
		}
	}
}

func TestServiceSchedulerSearchesAndSwitches(t *testing.T) {
	checker := &fakeChecker{results: map[string]services.Outcome{"cur": services.Blocked, "slow": services.Usable, "fast": services.Usable}}
	s, batches := runScheduler(t, checker, ServicePlan{Slots: 4, Cooldown: time.Hour,
		Latency: map[string]time.Duration{"slow": 300 * time.Millisecond, "fast": 50 * time.Millisecond},
		Targets: []ServiceTarget{target("netflix", "cur", "cur", "slow", "fast")}})
	nextBatch(t, batches) // first failure
	s.Request("netflix")
	var sw []ServiceSwitch
	for len(sw) == 0 {
		sw = nextBatch(t, batches).Switches
	}
	if sw[0].Old != "cur" || sw[0].New != "fast" || sw[0].Reason != "blocked" || sw[0].Service != "netflix" {
		t.Fatalf("switch = %+v", sw)
	}
}

func TestServiceSchedulerManualModeNeverSwitches(t *testing.T) {
	checker := &fakeChecker{results: map[string]services.Outcome{"cur": services.Blocked}}
	tg := target("netflix", "cur", "cur", "other")
	tg.Automatic = false
	s, batches := runScheduler(t, checker, ServicePlan{Slots: 4, Targets: []ServiceTarget{tg}})
	for i := 0; i < 3; i++ {
		if b := nextBatch(t, batches); len(b.Switches) != 0 {
			t.Fatalf("switched with automatic=false: %+v", b.Switches)
		}
		s.Request("netflix")
	}
}

func TestServiceSchedulerExhaustedSearchCapsAtEight(t *testing.T) {
	members := []string{"cur"}
	results := map[string]services.Outcome{"cur": services.Blocked}
	for i := 0; i < 12; i++ {
		name := string(rune('a' + i))
		members = append(members, name)
		results[name] = services.Blocked
	}
	checker := &fakeChecker{results: results}
	s, batches := runScheduler(t, checker, ServicePlan{Slots: 4, Cooldown: time.Hour, Targets: []ServiceTarget{target("claude", "cur", members...)}})
	nextBatch(t, batches)
	s.Request("claude")
	var b ServiceBatch
	for len(b.Exhausted) == 0 {
		b = nextBatch(t, batches)
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if checks := len(checker.calls); checks != 2+MaxServiceCandidates {
		t.Fatalf("calls = %d, want 2 checks of cur + %d candidates", checks, MaxServiceCandidates)
	}
	if checker.peak > 4 {
		t.Fatalf("peak concurrency %d", checker.peak)
	}
}

func TestServiceSchedulerNodeChangeIsDueNow(t *testing.T) {
	checker := &fakeChecker{}
	s, batches := runScheduler(t, checker, ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "a", "a", "b")}})
	nextBatch(t, batches)
	s.SetPlan(ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "b", "a", "b")}})
	if b := nextBatch(t, batches); len(b.Checks) != 1 || b.Checks[0].Proxy != "b" {
		t.Fatalf("after node change: %+v", b.Checks)
	}
}

func TestServiceSchedulerIdleWithoutServices(t *testing.T) {
	gate := make(chan struct{})
	checker := &fakeChecker{gate: gate}
	s, batches := runScheduler(t, checker, ServicePlan{Slots: 4, Targets: []ServiceTarget{target("claude", "a", "a")}})
	time.Sleep(50 * time.Millisecond) // the first check is now blocked in the checker
	s.SetPlan(ServicePlan{Slots: 4})
	close(gate)
	select {
	case b := <-batches:
		if len(b.Checks) != 0 {
			t.Fatalf("a check for a removed service was published: %+v", b.Checks)
		}
	case <-time.After(200 * time.Millisecond):
	}
	if s.armed() {
		t.Fatal("timer armed with no services")
	}
}
```

`armed()` is an unexported test hook returning whether the loop currently holds a timer; implement it by reading a field the loop sets under `s.mu`.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run 'TestService' ./monitor`
Expected: FAIL, `undefined: ServiceTarget`.

- [ ] **Step 3: Implement `monitor/service.go`**

```go
package monitor

import (
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/fishman/clashpulse/services"
)

const MaxServiceCandidates = 8

type ServiceScheduler struct {
	checker ServiceChecker
	onBatch func(ServiceBatch)

	mu         sync.Mutex
	plan       ServicePlan
	generation uint64
	requested  map[string]bool
	wake       chan struct{}
	timerArmed bool
}

type serviceRuntime struct {
	target     ServiceTarget
	next       time.Time
	recent     []services.Outcome // selected node, newest last, bounded to Failures
	last       map[string]services.Result
	coolUntil  time.Time
}

func NewServiceScheduler(checker ServiceChecker, onBatch func(ServiceBatch)) *ServiceScheduler {
	return &ServiceScheduler{checker: checker, onBatch: onBatch, requested: make(map[string]bool), wake: make(chan struct{}, 1)}
}

func (s *ServiceScheduler) SetPlan(plan ServicePlan) {
	s.mu.Lock()
	s.plan = plan
	s.generation++
	s.mu.Unlock()
	s.signal()
}

func (s *ServiceScheduler) Request(service string) {
	s.mu.Lock()
	s.requested[service] = true
	s.mu.Unlock()
	s.signal()
}

func (s *ServiceScheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ServiceScheduler) armed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timerArmed
}

func ServiceFailing(target ServiceTarget, recent []services.Outcome, unhealthy bool) bool {
	if target.Failures <= 0 || len(recent) < target.Failures {
		return false
	}
	for _, outcome := range recent[len(recent)-target.Failures:] {
		switch outcome {
		case services.Blocked:
		case services.Failed:
			if !unhealthy {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ServiceCandidates(target ServiceTarget, last map[string]services.Result, latency map[string]time.Duration) []string {
	var out []string
	for _, member := range target.Members {
		if member == target.Selected {
			continue
		}
		result, checked := last[member]
		if checked && result.Outcome == services.Blocked {
			continue
		}
		if checked && result.Region != "" && len(target.Regions) > 0 && !slices.Contains(target.Regions, result.Region) {
			continue
		}
		out = append(out, member)
	}
	slices.SortStableFunc(out, func(a, b string) int {
		la, oka := latency[a]
		lb, okb := latency[b]
		switch {
		case oka && !okb:
			return -1
		case !oka && okb:
			return 1
		case oka && okb && la != lb:
			if la < lb {
				return -1
			}
			return 1
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	if len(out) > MaxServiceCandidates {
		out = out[:MaxServiceCandidates]
	}
	return out
}
```

`Run` (same file) owns `runtimes map[string]*serviceRuntime`, one `*time.Timer`, and a bounded set of worker goroutines started per round (never more than `plan.Slots`, joined before the round's batch is published). Structure:

```go
func (s *ServiceScheduler) Run(ctx context.Context) error {
	runtimes := make(map[string]*serviceRuntime)
	var generation uint64
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		s.mu.Lock()
		plan, requested := s.plan, s.requested
		s.requested = make(map[string]bool)
		generation = s.generation
		s.mu.Unlock()
		now := time.Now()
		reconcile(runtimes, plan, now) // add/remove targets; Selected changed -> next = now and recent = nil
		for id := range requested {
			if rt := runtimes[id]; rt != nil {
				rt.next = now
			}
		}
		due := dueByNode(runtimes, now) // map proxy -> []id, ids in plan order
		if len(due) > 0 {
			batch := s.round(ctx, plan, runtimes, due)
			s.mu.Lock()
			stale := s.generation != generation
			s.mu.Unlock()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !stale {
				s.onBatch(batch)
			}
			continue
		}
		deadline, ok := earliest(runtimes)
		s.mu.Lock()
		s.timerArmed = ok
		s.mu.Unlock()
		var timerC <-chan time.Time
		if ok {
			timer.Reset(time.Until(deadline))
			timerC = timer.C
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-s.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timerC:
		}
	}
}
```

`round` checks each due node with at most `plan.Slots` concurrent `CheckServices` calls (a buffered semaphore channel), records each result into `rt.last[proxy]` and, for the selected node, appends to `rt.recent` (trimmed to `target.Failures`), sets `rt.next = now + Interval + rand.N(Interval/10)`, then for every runtime where `rt.target.Automatic && now >= rt.coolUntil && ServiceFailing(rt.target, rt.recent, plan.Unhealthy[rt.target.Selected])` runs `search`. `search` walks `ServiceCandidates(...)` in waves of `min(4, plan.Slots)`, checking each wave concurrently with `[]string{rt.target.ID}`; the first wave with any `Usable` result yields `ServiceSwitch{Service, Group, Old: Selected, New: best, Region, Reason}` where `best` is the lowest-latency usable in that wave (ties by candidate order) and `Reason` is `"blocked"` when the last recorded outcome was `Blocked`, else `"failing"`; it sets `rt.coolUntil = now + plan.Cooldown` and clears `rt.recent`. If no wave finds one, the ID goes to `batch.Exhausted`. `batch.Next` carries every runtime's `next`.

The context passed to each `CheckServices` is `ctx` (the run context); a plan change during a round marks it stale so its batch is dropped (`TestServiceSchedulerIdleWithoutServices`).

- [ ] **Step 4: Run tests**

Run: `go test -tags ci -count=1 -race ./monitor`
Expected: PASS with the race detector.

- [ ] **Step 5: Commit**

```bash
gofmt -l monitor
git add monitor
git commit -m "feat(monitor): schedule service checks and searches"
```

---

### Task 6: `core` and `ipc` - service snapshot and commands

**Files:**
- Modify: `core/snapshot.go` (`ServiceSnapshot`, `Snapshot.Services`, `CloneSnapshot`), `ipc/protocol.go` (commands, `ServiceEdit`, `Command.validate`, `validateSnapshot`, `ProtocolVersion = 14`)
- Test: `ipc/protocol_test.go`, `core/snapshot_test.go`

**Interfaces:**
- Produces:

```go
// core
type ServiceSnapshot struct {
	ID, Name        string
	Enabled         bool
	Automatic       bool
	Regions         []string
	IntervalSeconds uint32
	Failures        int
	DomainsResource string
	GroupID         string
	Selected        string // masked label
	Outcome         string // "usable", "blocked", "error", ""
	Region          string
	LastCheck       int64
	NextCheck       int64
	LastSwitch      string // "", "blocked", "failing", "exhausted"
}
// Snapshot gains: Services []ServiceSnapshot

// ipc
const (
	CommandSetService   CommandKind = "set_service"
	CommandCheckService CommandKind = "check_service"
)
type ServiceEdit struct {
	ID              string   `json:"id"`
	Enabled         bool     `json:"enabled"`
	Automatic       bool     `json:"automatic"`
	Regions         []string `json:"regions,omitempty"`
	IntervalSeconds uint32   `json:"interval_seconds"`
	Failures        int      `json:"failures"`
	DomainsResource string   `json:"domains_resource,omitempty"`
}
// Command gains: ServiceID string `json:"service_id,omitempty"`; Service *ServiceEdit `json:"service,omitempty"`
```

- [ ] **Step 1: Write failing tests** (append to `ipc/protocol_test.go`)

```go
func TestServiceCommandsValidate(t *testing.T) {
	good := Command{Kind: CommandSetService, Service: &ServiceEdit{ID: "claude", Enabled: true, Regions: []string{"US"}, IntervalSeconds: 1800, Failures: 2}}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Command{
		{Kind: CommandSetService},
		{Kind: CommandSetService, Service: &ServiceEdit{ID: "tiktok", IntervalSeconds: 1800, Failures: 2}},
		{Kind: CommandSetService, Service: &ServiceEdit{ID: "claude", IntervalSeconds: 60, Failures: 2}},
		{Kind: CommandSetService, Service: &ServiceEdit{ID: "claude", IntervalSeconds: 1800, Failures: 0}},
		{Kind: CommandSetService, Service: &ServiceEdit{ID: "claude", IntervalSeconds: 1800, Failures: 2, Regions: []string{"usa"}}},
		{Kind: CommandSetService, Service: &ServiceEdit{ID: "claude", IntervalSeconds: 1800, Failures: 2}, GroupID: "x"},
		{Kind: CommandCheckService},
		{Kind: CommandCheckService, ServiceID: "nope"},
	}
	for _, command := range bad {
		if command.validate() == nil {
			t.Fatalf("accepted %+v", command)
		}
	}
	if err := (Command{Kind: CommandCheckService, ServiceID: "netflix"}).validate(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceSnapshotValidates(t *testing.T) {
	snapshot := core.Snapshot{Services: []core.ServiceSnapshot{{ID: "claude", Name: "Claude", Enabled: true, Regions: []string{"US"}, IntervalSeconds: 1800, Failures: 2, Outcome: "usable", Region: "US", Selected: "Proxy 3"}}}
	if err := validateSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Services[0].Outcome = "unlocked"
	if validateSnapshot(snapshot) == nil {
		t.Fatal("unknown outcome accepted")
	}
	snapshot.Services[0].Outcome = "usable"
	snapshot.Services[0].Selected = "https://example.com/x"
	if validateSnapshot(snapshot) == nil {
		t.Fatal("URL in a service label accepted")
	}
}
```

Plus in `core/snapshot_test.go`, a clone test: mutate `clone.Services[0].Regions[0]` and assert the original is unchanged.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run 'TestService|TestClone' ./ipc ./core`
Expected: FAIL, `undefined: CommandSetService`.

- [ ] **Step 3: Implement**

`core/snapshot.go`: add the type and field; in `CloneSnapshot` clone `Services` and each `Regions`.

`ipc/protocol.go`:
- `ProtocolVersion uint16 = 14`.
- Add the two `CommandKind` constants, the `ServiceEdit` type, and `ServiceID`/`Service` fields to `Command`.
- In the stray-payload check at the top of `validate`, `Service` is allowed only for `CommandSetService` and `ServiceID` only for `CommandCheckService`.
- Cases:

```go
	case CommandSetService:
		edit := c.Service
		if edit == nil {
			return errors.New("ipc: set_service requires a service")
		}
		if _, ok := services.Lookup(edit.ID); !ok {
			return errors.New("ipc: unknown service")
		}
		if edit.IntervalSeconds < uint32(config.MinServiceInterval/time.Second) || edit.Failures < 1 || edit.Failures > 10 || len(edit.Regions) > 32 {
			return errors.New("ipc: invalid service settings")
		}
		for _, region := range edit.Regions {
			if !validRegion(region) {
				return errors.New("ipc: invalid service region")
			}
		}
		if edit.DomainsResource != "" && !config.ValidStableID(edit.DomainsResource) {
			return errors.New("ipc: invalid service resource")
		}
	case CommandCheckService:
		if _, ok := services.Lookup(c.ServiceID); !ok {
			return errors.New("ipc: unknown service")
		}
```

with `func validRegion(s string) bool` (two ASCII upper-case letters) shared by the snapshot check. If `ipc` already imports `config`, reuse it; if importing `config` from `ipc` is not already done, hard-code `300` for the minimum interval seconds and note it next to `config.MinServiceInterval` in a one-line comment.
- `validateSnapshot`: add `len(snapshot.Services)` to the item count; per service require a known ID, `Outcome` in `{"", "usable", "blocked", "error"}`, `LastSwitch` in `{"", "blocked", "failing", "exhausted"}`, valid regions, and pass `Name`, `Selected`, `GroupID`, `DomainsResource`, `Region` through `addText`; reject `Selected` containing `://`.

- [ ] **Step 4: Run tests**

Run: `go test -tags ci -count=1 ./ipc ./core`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l core ipc
git add core ipc
git commit -m "feat(ipc): add service snapshot and commands"
```

---

### Task 7: `app` - run checks, apply switches, serve commands

**Files:**
- Create: `app/services.go`; Test: `app/services_test.go`
- Modify: `app/runtime.go` (fields), `app/lifecycle.go` (start/stop the service scheduler next to the monitor; feed plans from `refreshGroups`), `app/commands.go` (`execute` cases; `CommandSelectGroup` on a service group; `CommandSetAutomation` on a service group is rejected), `localize/locale/en.toml`, `localize/locale/zh-CN.toml` (operation labels `operation.set_service`, `operation.check_service`, `operation.service_check`, `operation.service_switch`)

**Interfaces:**
- Consumes: `mihomo.ProbePool`, `mihomo.ServiceGroupFor`, `mihomo.ServiceGroupName` (Tasks 3-4); `monitor.ServiceScheduler` and types (Task 5); `config.PutService`, `config.Service` (Task 2); `core.ServiceSnapshot`, `ipc.CommandSetService`, `ipc.CommandCheckService` (Task 6); `services.Check`, `services.Lookup` (Task 1).
- Produces: nothing new for later tasks beyond the snapshot contents.

Rules this task implements (spec "Switching" and "IPC"):
- `serviceChecker.CheckServices(ctx, proxy, ids)` acquires one lease from `s.probePool` for `proxy`, runs `services.Check(ctx, lease.Transport(), defs)` for the definitions of `ids` (in that order), releases the lease, and returns the results; a failed lease returns `Failed` for every id.
- The plan is rebuilt in `refreshGroups` and on every accepted monitor batch: one `ServiceTarget` per enabled service whose group `ClashPulse - <Name>` is present in the controller's groups, with `Selected` and `Members` from that group, `Interval`/`Failures`/`Automatic`/`Regions` from config, `Latency` per node name as the lowest median across the profile's monitored groups, `Unhealthy` from the latest latency outcome by name, `Cooldown` from `monitor.cooldown`, `Slots` = `s.probePool.Size()`. With no pool or no enabled service, `SetPlan(ServicePlan{})`.
- Service groups stay outside latency automation: in `refreshGroups`, a group for which `mihomo.ServiceGroupFor(group.Name)` holds is listed in the snapshot (with `AutomationEnabled: false`) but never appended to `monitorGroups`; `CommandSetAutoFastest` skips it when building `managed`; and `s.automation[id]` set for one returns `fmt.Errorf("clashpulse: automation group %q is a service group; use services automatic", id)`.
- A `ServiceBatch` is applied on the service loop (same goroutine discipline as `acceptBatch`): update each `core.ServiceSnapshot` outcome, region, last and next check; for each `ServiceSwitch`, re-check the guard on current state (service still enabled and `Automatic`, group `Selected == Old`, no switch in flight for that group), then `controller.Select(group, New)`, record `LastSwitch = Reason`, append a diagnostic `Kind: "service_switch", SourceID: service ID, Severity: "info", Message: "service.switch." + Reason`, and refresh groups. Exhausted -> `LastSwitch = "exhausted"` and a `warning` diagnostic `service.exhausted`.
- `CommandSelectGroup` on a service group performs the selection and then persists `Automatic = false` for that service via `config.PutService`.
- An observed selection change on a service group counts as a manual pick, persisting `Automatic = false`, only when all hold: it is not a ClashPulse selection (service switch or `CommandSelectGroup`), the runtime has not been (re)started or reloaded since the baseline was taken, and the previously observed node is still a member of the group. Every ClashPulse-driven start and reload clears the baseline; the first `refreshGroups` after it records `Selected` without judging it.
- `CommandSetAutomation` with a service group's ID returns an error `"service groups use service automatic switching"`.
- `CommandSetService` converts `ServiceEdit` to `config.Service` and calls `config.PutService`, then the normal reload; `CommandCheckService` calls `s.services.Request(id)`.
- Diagnostics use only service IDs and fixed message codes; node names go through the existing masked label path.

- [ ] **Step 1: Write failing tests** (`app/services_test.go`)

Read `app/stream_probe_test.go` and `app/monitor_policy_test.go` first and build on their fake-controller service constructor.

```go
func TestServiceSwitchAppliedUnderGuard(t *testing.T) {
	service, controller := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B"), config.Service{ID: "netflix", Enabled: true, Automatic: true, CheckInterval: 30 * time.Minute, Failures: 2})
	service.applyServiceBatch(monitor.ServiceBatch{Switches: []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "Proxy A", New: "Proxy B", Region: "US", Reason: "blocked"}}})
	if got := controller.selected("ClashPulse - Netflix"); got != "Proxy B" {
		t.Fatalf("selected = %q", got)
	}
	snap := findService(service.snapshot.Services, "netflix")
	if snap.LastSwitch != "blocked" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestServiceSwitchRejectedAfterManualPick(t *testing.T) {
	service, controller := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B", "Proxy C"), config.Service{ID: "netflix", Enabled: true, Automatic: true, CheckInterval: 30 * time.Minute, Failures: 2})
	groupID := opaqueID("ClashPulse - Netflix")
	if err := service.execute(ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: groupID, ChoiceID: opaqueID("Proxy C")}); err != nil {
		t.Fatal(err)
	}
	service.applyServiceBatch(monitor.ServiceBatch{Switches: []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "Proxy A", New: "Proxy B", Reason: "blocked"}}})
	if got := controller.selected("ClashPulse - Netflix"); got != "Proxy C" {
		t.Fatalf("manual pick overwritten: %q", got)
	}
	if svc := service.store.Snapshot().Services[0]; svc.Automatic {
		t.Fatal("manual pick did not turn automatic off")
	}
}

func TestServiceCommandsPersistAndRequest(t *testing.T) {
	service, _ := newServiceTestRuntime(t, serviceProfile("Proxy A"), config.Service{ID: "claude", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2})
	edit := &ipc.ServiceEdit{ID: "claude", Enabled: true, Automatic: true, Regions: []string{"US"}, IntervalSeconds: 3600, Failures: 3}
	if err := service.execute(ipc.Command{Kind: ipc.CommandSetService, Service: edit}); err != nil {
		t.Fatal(err)
	}
	got := service.store.Snapshot().Services[0]
	if !got.Automatic || got.CheckInterval != time.Hour || got.Failures != 3 {
		t.Fatalf("persisted = %+v", got)
	}
	if err := service.execute(ipc.Command{Kind: ipc.CommandSetAutomation, GroupID: opaqueID("ClashPulse - Claude"), Automation: &ipc.AutomationSetting{Enabled: true}}); err == nil {
		t.Fatal("group automation accepted on a service group")
	}
}

func TestServiceDiagnosticsCarryNoNodeNames(t *testing.T) {
	service, _ := newServiceTestRuntime(t, serviceProfile("node-secret.example.com", "Proxy B"), config.Service{ID: "netflix", Enabled: true, Automatic: true, CheckInterval: 30 * time.Minute, Failures: 2})
	service.applyServiceBatch(monitor.ServiceBatch{Switches: []monitor.ServiceSwitch{{Service: "netflix", Group: "ClashPulse - Netflix", Old: "node-secret.example.com", New: "Proxy B", Reason: "blocked"}}, Exhausted: []string{"netflix"}})
	for _, entry := range service.snapshot.Diagnostics {
		if strings.Contains(entry.Message, "example.com") || strings.Contains(entry.SourceID, "example.com") {
			t.Fatalf("diagnostic leaks a node name: %+v", entry)
		}
	}
	if svc := findService(service.snapshot.Services, "netflix"); strings.Contains(svc.Selected, "example.com") {
		t.Fatalf("snapshot leaks a node name: %+v", svc)
	}
}

func TestServiceGroupsOutsideLatencyAutomation(t *testing.T) {
	service, _ := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B"), config.Service{ID: "netflix", Enabled: true, CheckInterval: 30 * time.Minute, Failures: 2})
	for _, group := range service.monitorGroupsForTest() { // the []monitor.GroupState last handed to the latency monitor
		if group.Group == "ClashPulse - Netflix" {
			t.Fatal("service group is delay-probed by the latency monitor")
		}
	}
	if err := service.execute(ipc.Command{Kind: ipc.CommandSetAutoFastest, Automation: &ipc.AutomationSetting{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(service.store.Snapshot().Monitor.AutomatedGroups, opaqueID("ClashPulse - Netflix")) {
		t.Fatal("set_auto_fastest opted a service group into lowest-latency switching")
	}
}

func TestServiceAutomaticSurvivesRestartWithRemovedNode(t *testing.T) {
	service, controller := newServiceTestRuntime(t, serviceProfile("Proxy A", "Proxy B"), config.Service{ID: "netflix", Enabled: true, Automatic: true, CheckInterval: 30 * time.Minute, Failures: 2})
	controller.setSelected("ClashPulse - Netflix", "Proxy B")
	// A subscription refresh drops Proxy B; after the reload Mihomo falls back to the first member.
	service.reloadForTest(serviceProfile("Proxy A", "Proxy C"))
	controller.setSelected("ClashPulse - Netflix", "Proxy A")
	if err := service.refreshGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.store.Snapshot().Services[0].Automatic {
		t.Fatal("a fallback after reload turned automatic off")
	}
	// A dashboard pick between reloads, from a node still present, is manual.
	controller.setSelected("ClashPulse - Netflix", "Proxy C")
	if err := service.refreshGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.store.Snapshot().Services[0].Automatic {
		t.Fatal("an external pick did not turn automatic off")
	}
}
```

`newServiceTestRuntime`, `serviceProfile`, `findService`, `controller.selected`, `controller.setSelected`, `reloadForTest` (runs the normal ClashPulse reload path with a new source profile), and `monitorGroupsForTest` (returns the `[]monitor.GroupState` from the last `refreshGroups`) are test helpers to add on top of the existing fake controller; `applyServiceBatch` and the `execute` method name must match the real ones in `app/commands.go` (read it first and rename here if `execute` is called differently).

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run 'TestService' ./app`
Expected: FAIL, `undefined: applyServiceBatch` (or the helper names).

- [ ] **Step 3: Implement `app/services.go`** following the rules above, plus the wiring in `runtime.go`, `lifecycle.go`, `commands.go`. The service scheduler goroutine is started and stopped exactly where the monitor scheduler is, with the same cancellation and wait; its batches go through `offerBatch`'s pattern (a bounded channel into the service loop) and are applied by `applyServiceBatch` there, never on the scheduler goroutine.

- [ ] **Step 4: Run tests**

Run: `go test -tags ci -count=1 -race ./app`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l app localize
git add app localize
git commit -m "feat(app): run service checks and apply opt-in switches"
```

---

### Task 8: GUI - Settings > Services

**Files:**
- Create: `ui/services.go`; Test: `ui/services_test.go`
- Modify: `ui/views.go` (add `"Services"` to the settings `sections` list and its `sectionViews` entry; feed snapshots), `localize/locale/*.toml` (`gui.settings.section.Services` and the row strings)

**Interfaces:**
- Consumes: `core.ServiceSnapshot` (Task 6), `ipc.CommandSetService`, `ipc.CommandCheckService`, `services.Definitions()` for the fixed row order and names.
- Produces: `newServicesSection(send sendIntent, window fyne.Window) *servicesSection` with `update([]core.ServiceSnapshot)` and `content fyne.CanvasObject`.

Row layout (one `settingRow` per built-in service, in definition order, inside one `settingsCard("Services", nil, cardBody(...))`): name; value label `"<outcome> <region> - <node>"` (e.g. `Usable US - Proxy 3`, `Not checked` before the first check); controls: `Enable` check, `Automatic` check (disabled while Enable is off), `Check now` button (disabled while Enable is off), `Edit` button; help line below: `Last check 14:02 - next 14:32`, plus for YouTube the spec's limitation sentence.

Edit dialog (`showSized` with `formDialogWidth`): regions entry (comma-separated, upper-cased on save), interval select (`15m`, `30m`, `1h`, `2h`, `6h`), failures select (1..5), domains resource select (`Built-in domains` plus every enabled domain rule-set resource ID from the snapshot's resources). Save sends one `CommandSetService` carrying all fields.

Every toggle sends a full `ServiceEdit` built from the row's current snapshot with the one field changed, guarded by a `refreshing` flag exactly like the existing monitor controls, so applying a snapshot never sends a command.

- [ ] **Step 1: Write failing tests** (`ui/services_test.go`)

```go
func TestServicesSectionRowsAndCommands(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	section := newServicesSection(func(c ipc.Command) { sent = append(sent, c) }, app.NewWindow("x"))
	section.update([]core.ServiceSnapshot{{ID: "netflix", Name: "Netflix", Enabled: true, IntervalSeconds: 1800, Failures: 2, Outcome: "usable", Region: "US", Selected: "Proxy 3"}})
	if len(sent) != 0 {
		t.Fatalf("applying a snapshot sent %v", sent)
	}
	labels := strings.Join(rowLabels(section.content), "\n")
	for _, want := range []string{"ChatGPT", "Claude", "Gemini", "YouTube Premium", "Netflix", "Spotify", "Usable US - Proxy 3"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("missing %q in %q", want, labels)
		}
	}
	row := section.rows["netflix"]
	row.automatic.SetChecked(true)
	if len(sent) != 1 || sent[0].Kind != ipc.CommandSetService || !sent[0].Service.Automatic || !sent[0].Service.Enabled || sent[0].Service.IntervalSeconds != 1800 {
		t.Fatalf("automatic toggle sent %+v", sent)
	}
	test.Tap(row.check)
	if last := sent[len(sent)-1]; last.Kind != ipc.CommandCheckService || last.ServiceID != "netflix" {
		t.Fatalf("check now sent %+v", last)
	}
	if !section.rows["claude"].automatic.Disabled() {
		t.Fatal("automatic is enabled for a disabled service")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run TestServicesSection ./ui`
Expected: FAIL, `undefined: newServicesSection`.

- [ ] **Step 3: Implement** `ui/services.go` with the layout above using `settingRow`, `settingsCard`, `cardBody`, `setCardRows` from `ui/settings_cards.go`; wire it into `newSettingsPage` and the snapshot update path in `ui/views.go`.

- [ ] **Step 4: Run tests and look at it**

Run: `go test -tags ci -count=1 ./ui`
Expected: PASS. Then render Settings > Services to a PNG with a throwaway `zz_shot_test.go` (as done for the activity dialog) at 1000x700 and 360x540, check alignment and wrapping, delete the throwaway file.

- [ ] **Step 5: Commit**

```bash
gofmt -l ui localize
git add ui localize
git commit -m "feat(ui): add settings for service checks"
```

---

### Task 9: TUI - service rows and actions

**Files:**
- Modify: `tui/model.go` (rows in `TabSettings`, row kind `rowService`, `activate`, action handlers), `tui/keys.toml`, `tui/keys.go` (action list), `localize/locale/*.toml`
- Test: `tui/model_test.go`

**Interfaces:**
- Consumes: `core.ServiceSnapshot`, `ipc.CommandSetService`, `ipc.CommandCheckService`.

Rows: one `Row{ID: "service:" + id, Title: name, Cells: [name, enabled, automatic, outcome+region, node, next check], kind: rowService, serviceID: id}` per built-in service after the existing settings rows. Actions in the settings context: `service_toggle` (`s`), `service_automatic` (`a` if free in `[settings]`, else pick a free key and keep help derived from the map), `service_check` (`c` if free). `activate` on a service row opens a modal with interval, failures, and regions fields, saving one `CommandSetService`.

- [ ] **Step 1: Write failing tests** (append to `tui/model_test.go`)

```go
func TestServiceRowsSendCommands(t *testing.T) {
	model := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Services":[{"ID":"netflix","Name":"Netflix","Enabled":true,"IntervalSeconds":1800,"Failures":2,"Outcome":"usable","Region":"US","Selected":"Proxy 3"}]}}`)).selectTab(TabSettings)
	model = model.selectRowByID("service:netflix") // existing helper or add one next to the other selection helpers
	model, command, _ := model.runAction("service_automatic")
	if command == nil || command.Kind != ipc.CommandSetService || !command.Service.Automatic || !command.Service.Enabled {
		t.Fatalf("automatic action = %+v", command)
	}
	model, command, _ = model.runAction("service_check")
	if command == nil || command.Kind != ipc.CommandCheckService || command.ServiceID != "netflix" {
		t.Fatalf("check action = %+v", command)
	}
	if !strings.Contains(strings.Join(model.rowTitles(), ","), "Spotify") {
		t.Fatal("built-in services without a snapshot entry are not listed")
	}
}
```

Read how existing settings actions are dispatched (search `"system_proxy_enable"` in `tui/model.go` around line 457) and use that entry point in place of `runAction` if it is named differently.

- [ ] **Step 2: Run to verify failure**

Run: `go test -tags ci -count=1 -run TestServiceRows ./tui`
Expected: FAIL.

- [ ] **Step 3: Implement** rows, actions, keymap entries (with `desc` and `show = true`), and the edit modal.

- [ ] **Step 4: Run tests**

Run: `go test -tags ci -count=1 ./tui`
Expected: PASS, including the existing test that every exposed action has a default binding.

- [ ] **Step 5: Commit**

```bash
gofmt -l tui localize
git add tui localize
git commit -m "feat(tui): add service check rows and actions"
```

---

### Task 10: Docs, example config, and full verification

**Files:**
- Modify: `docs/configuration.md`, `docs/zh-CN/configuration.md`, `docs/switching.md`, `docs/zh-CN/switching.md`, `examples/config.toml`, `README.md`

- [ ] **Step 1: `examples/config.toml`** - add after `[monitor]`:

```toml
# Service checks (all optional; nothing runs unless a service is listed and enabled).
# Each enabled service gets a "ClashPulse - <Name>" group and routes its domains through it.
# IDs: chatgpt, claude, gemini, youtube, netflix, spotify.
# [[services]]
# id = "chatgpt"
# enabled = true
# regions = ["US", "JP"]   # empty: any region the service does not block
# check_interval = "30m"   # minimum 5m
# failures = 2             # consecutive failed checks before looking for another node
# automatic = false        # true lets ClashPulse switch this group's node
# domains_resource = ""    # optional resources.toml domain rule-set ID replacing the built-in domains
```

- [ ] **Step 2: Docs** - in `docs/configuration.md` add a `[[services]]` section with every key, default, and range from Task 2, the check URLs per service, the traffic estimate (about 0.3 MB an hour for all six at 30m), the YouTube limitation, and that checks need Mihomo with pinned-listener support. In `docs/switching.md` add how service switching differs from group automation (separate `automatic`, manual pick turns it off, cooldown shared with `monitor.cooldown`). Mirror both in `docs/zh-CN/`. Add one README feature line.

- [ ] **Step 3: Full verification**

Run: `gofmt -l $(git ls-files '*.go' | grep -v '^references/')`
Expected: no output.

Run: `go vet -tags ci ./...`
Expected: no output, exit 0.

Run: `go test -tags ci -count=1 ./...`
Expected: every package `ok`.

Run (where Mihomo is installed): `go test -tags ci -count=1 -run TestProbeListenersFollowOnlyTheirOwnGroup -v ./mihomo`
Expected: PASS (not SKIP).

- [ ] **Step 4: Commit**

```bash
git add docs examples README.md
git commit -m "docs(services): document service checks"
```

---

## Self-review notes

- Spec coverage: definitions and rules (Task 1), request policy incl. 15 s, 10 redirects, cookies, UA, caps (Task 1), routing groups/rules/domains_resource/collision (Task 4), probe pool and concurrency (Task 3), scheduling/switching/cooldown/circuit breaker/manual override (Tasks 5, 7), configuration (Task 2), IPC and protocol 14 (Task 6), GUI and TUI (Tasks 8, 9), docs (Task 10), testing list (each task). The spec's "Netflix region from `Location`" maps to `localePath(responses[2].location)`.
- Review Focus items each have a named test in their owning task.
- Type names used across tasks: `services.Result`/`Outcome`/`Definition`, `config.Service`, `mihomo.ProbePool`/`ProbeLease`/`ProbeListener`/`ServiceGroupName`/`ServiceGroupFor`, `monitor.ServiceScheduler`/`ServicePlan`/`ServiceTarget`/`ServiceBatch`/`ServiceSwitch`, `core.ServiceSnapshot`, `ipc.ServiceEdit`/`CommandSetService`/`CommandCheckService`.
