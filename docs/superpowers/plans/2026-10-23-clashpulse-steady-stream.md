# Steady-Stream Switching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in `steady_stream` monitor policy that pulls a 256 KiB HTTPS file through each probed node via Mihomo and switches away from nodes whose downloads break.

**Architecture:** Mihomo gets a generated hidden `select` group `clashpulse-stream-probe` (`include-all: true`) and an authenticated loopback mixed listener pinned to it. The monitor scheduler runs one global, serialized stream round per `stream_interval`; a pure `DecideSteady` ranks nodes by completed pulls, then throughput, then latency. `app` owns credentials, port, wiring, snapshots; GUI and TUI expose the settings and per-node results.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `crypto/tls`, `net/http/httptest`), existing `gopkg.in/yaml.v3`, Fyne, tcell TUI, go-i18n catalogs. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-23-clashpulse-steady-stream-design.md`

## Global Constraints

- Policy values: `lowest_latency` (default, shown as "Fastest"), `failover`, `steady_stream`. Constant `monitor.SwitchSteadyStream = "steady_stream"`.
- `stream_url` default `https://speed.cloudflare.com/__down?bytes=262144`; HTTPS only, no userinfo, no fragment, at most 2048 bytes.
- `stream_interval` default `5m`; valid range `4m` to `24h`.
- Candidates per automated group: selected node plus 3 lowest-median-latency healthy nodes. Pulls per round capped at 8, selected nodes first. One pull at a time.
- Pull limits: stall 5 s without bytes, 30 s total. Outcomes: `success`, `reset`, `stall`, `truncated`, `tls`, `timeout`, `error`. Missing `Content-Length` is `error`.
- Stream window per node: last `window_size` samples, dropped when older than 6 x `stream_interval`. Samples keyed by node name, shared across groups.
- Switch rule: selected node has `>= max_failures` interrupted pulls in its window; candidate has `>= min_candidate_samples` completed pulls, strictly fewer interruptions, latest pull completed. Rank: fewest interruptions, higher median throughput, lower median latency, then name.
- Probe group and listener name: `clashpulse-stream-probe`. Rendered only under `steady_stream`. Listener `listen: 127.0.0.1`, random `users` credential held in memory, never logged, never in IPC.
- Hidden everywhere: group snapshots, proxies snapshots, tray, GUI, TUI, monitor roster, dial-failure watch.
- Capability gate: `Capability.SupportsPinnedListener`, set only when `-t` validates a fixture with a pinned authenticated listener. Missing -> `CapabilityError{ResourceID: "monitor.switch_policy", Kind: "pinned-listener"}`.
- Profile defining a proxy, group, or listener named `clashpulse-stream-probe` is rejected.
- IPC protocol version 10 -> 11.
- Tests and fixtures: reserved names only (`example.com`, `.invalid`, `192.0.2.0/24`).
- Every task: skip project-wide suites; run only the task's commands. Final task runs the full suite.

## Review Focus

1. A group containing `DIRECT`, `REJECT`, or a nested group: those members are not in the probe group and must never be pulled or counted as interrupted. Pinned by Task 5 `TestStreamTargetsSkipNonMembers`.
2. A node shared by two automated groups is pulled once per round and its samples drive both groups' decisions. Pinned by Task 5 `TestStreamRoundDeduplicatesSharedNodes`.
3. Switching away from `steady_stream` (or disabling the monitor) mid-pull cancels the pull and emits no stream decision. Pinned by Task 5 `TestStreamRoundCancelsOnStop`.
4. A user profile that already uses the name `clashpulse-stream-probe` gets a named render error, not a silently replaced group. Pinned by Task 3 `TestRenderRejectsReservedStreamProbeName`.
5. Proxy credentials for the probe listener never appear in snapshots, diagnostics, or errors. Pinned by Task 2 `TestStreamPullErrorsOmitCredentials` and Task 6 `TestSteadyStreamSnapshotHasNoProbeCredentials`.

---

### Task 1: Monitor policy, stream model, and `DecideSteady`

**Files:**
- Modify: `monitor/model.go`, `monitor/policy.go`
- Create: `monitor/steady.go`, `monitor/steady_test.go`
- Modify: `monitor/policy_test.go` (switch-policy validation table)

**Interfaces:**
- Produces:
  - `const SwitchSteadyStream = "steady_stream"`; `var SwitchPolicies = []string{SwitchLowestLatency, SwitchFailover, SwitchSteadyStream}`
  - `const MinStreamInterval = 4 * time.Minute`, `StreamCandidates = 3`, `StreamPullCap = 8`, `DefaultStreamURL = "https://speed.cloudflare.com/__down?bytes=262144"`
  - `Policy.StreamURL string`, `Policy.StreamInterval time.Duration`
  - `type StreamOutcome string` with `StreamSuccess, StreamReset, StreamStall, StreamTruncated, StreamTLS, StreamTimeout, StreamError`
  - `type StreamSample struct { Proxy, URL string; FinishedAt time.Time; Bytes int64; FirstByte, Duration time.Duration; Outcome StreamOutcome }` with `Completed() bool`, `Throughput() int64` (bytes/s)
  - `type StreamStability struct { Proxy string; Samples, Completed int; MedianThroughput int64 }`; `func MeasureStreams(proxy string, samples []StreamSample, windowSize int) StreamStability`
  - `Decision.Group string`, `Decision.Streams []StreamSample`
  - `const ReasonSteadierStream = "steadier HTTPS downloads"`, `ReasonSelectedStreamsHealthy = "selected node completes its downloads"`
  - `func DecideSteady(policy Policy, state GroupState, latency []Sample, streams map[string][]StreamSample, now time.Time) Decision`
  - `func ValidPolicyName(name string) bool`

- [ ] **Step 1: Write the failing tests**

`monitor/steady_test.go`:

```go
package monitor

import (
	"testing"
	"time"
)

func steadyPolicy() Policy {
	policy := testPolicy()
	policy.SwitchPolicy = SwitchSteadyStream
	policy.StreamURL = DefaultStreamURL
	policy.StreamInterval = 5 * time.Minute
	return policy
}

func pulls(proxy string, outcomes ...StreamOutcome) []StreamSample {
	samples := make([]StreamSample, len(outcomes))
	for i, outcome := range outcomes {
		samples[i] = StreamSample{Proxy: proxy, URL: DefaultStreamURL, FinishedAt: time.Unix(int64(100+i), 0),
			Bytes: 262144, Duration: time.Second, Outcome: outcome}
	}
	return samples
}

func withThroughput(samples []StreamSample, d time.Duration) []StreamSample {
	for i := range samples {
		samples[i].Duration = d
	}
	return samples
}

func TestDecideSteadyKeepsNodeWithOneInterruption(t *testing.T) {
	policy := steadyPolicy()
	streams := map[string][]StreamSample{
		"alpha": pulls("alpha", StreamSuccess, StreamReset, StreamSuccess, StreamSuccess, StreamSuccess),
		"beta":  pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess, StreamSuccess, StreamSuccess),
	}
	decision := DecideSteady(policy, testGroup(), nil, streams, time.Unix(200, 0))
	if decision.Switch || decision.Reason != ReasonSelectedStreamsHealthy {
		t.Fatalf("decision = %+v, want stay on alpha", decision)
	}
}

func TestDecideSteadySwitchesAwayFromInterruptedNode(t *testing.T) {
	policy := steadyPolicy()
	streams := map[string][]StreamSample{
		"alpha": pulls("alpha", StreamSuccess, StreamReset, StreamSuccess, StreamStall, StreamSuccess),
		"beta":  pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess),
	}
	decision := DecideSteady(policy, testGroup(), nil, streams, time.Unix(200, 0))
	if !decision.Switch || decision.New != "beta" || decision.Reason != ReasonSteadierStream || decision.Group != "auto" {
		t.Fatalf("decision = %+v, want switch to beta", decision)
	}
	if len(decision.Streams) != 8 {
		t.Fatalf("evidence = %d stream samples, want old and new windows (8)", len(decision.Streams))
	}
}

func TestDecideSteadyRequiresCompletedLatestAndEnoughPulls(t *testing.T) {
	policy := steadyPolicy()
	alpha := pulls("alpha", StreamReset, StreamReset, StreamSuccess)
	for name, beta := range map[string][]StreamSample{
		"too few":       pulls("beta", StreamSuccess, StreamSuccess),
		"latest broken": pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess, StreamTruncated),
		"not steadier":  pulls("beta", StreamSuccess, StreamReset, StreamSuccess, StreamTLS, StreamSuccess),
	} {
		decision := DecideSteady(policy, testGroup(), nil, map[string][]StreamSample{"alpha": alpha, "beta": beta}, time.Unix(200, 0))
		if decision.Switch {
			t.Fatalf("%s: switched to %s", name, decision.New)
		}
	}
}

func TestDecideSteadyBreaksTiesByThroughputThenLatency(t *testing.T) {
	policy := steadyPolicy()
	state := testGroup()
	state.Proxies = []string{"alpha", "beta", "gamma"}
	alpha := pulls("alpha", StreamReset, StreamReset, StreamSuccess)
	slow := withThroughput(pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess), 4*time.Second)
	fast := withThroughput(pulls("gamma", StreamSuccess, StreamSuccess, StreamSuccess), time.Second)
	decision := DecideSteady(policy, state, nil, map[string][]StreamSample{"alpha": alpha, "beta": slow, "gamma": fast}, time.Unix(200, 0))
	if decision.New != "gamma" {
		t.Fatalf("throughput tie-break chose %q, want gamma", decision.New)
	}
	equal := withThroughput(pulls("gamma", StreamSuccess, StreamSuccess, StreamSuccess), 4*time.Second)
	latency := []Sample{testSample(policy, "auto", "beta", 300*time.Millisecond, 100), testSample(policy, "auto", "gamma", 90*time.Millisecond, 100)}
	decision = DecideSteady(policy, state, latency, map[string][]StreamSample{"alpha": alpha, "beta": slow, "gamma": equal}, time.Unix(200, 0))
	if decision.New != "gamma" {
		t.Fatalf("latency tie-break chose %q, want gamma", decision.New)
	}
}

func TestDecideSteadyHonorsCommonGuards(t *testing.T) {
	policy := steadyPolicy()
	broken := pulls("alpha", StreamReset, StreamReset, StreamReset)
	good := pulls("beta", StreamSuccess, StreamSuccess, StreamSuccess)
	state := testGroup()
	state.ManualOverride = true
	if d := DecideSteady(policy, state, nil, map[string][]StreamSample{"alpha": broken, "beta": good}, time.Unix(200, 0)); d.Switch || d.Reason != ReasonManualOverride {
		t.Fatalf("manual override: %+v", d)
	}
	state = testGroup()
	state.LastSwitchAt = time.Unix(190, 0)
	policy.Cooldown = time.Minute
	if d := DecideSteady(policy, state, nil, map[string][]StreamSample{"alpha": broken, "beta": good}, time.Unix(200, 0)); d.Switch || d.Reason != ReasonCooldown {
		t.Fatalf("cooldown: %+v", d)
	}
	policy.Cooldown = 0
	allBroken := pulls("beta", StreamSuccess, StreamSuccess, StreamReset)
	if d := DecideSteady(policy, testGroup(), nil, map[string][]StreamSample{"alpha": broken, "beta": allBroken}, time.Unix(200, 0)); d.Switch || d.Reason != ReasonAllCandidatesFailed {
		t.Fatalf("all failed: %+v", d)
	}
}

func TestSteadyPolicyValidation(t *testing.T) {
	policy := steadyPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid steady policy: %v", err)
	}
	for name, mutate := range map[string]func(*Policy){
		"http url":       func(p *Policy) { p.StreamURL = "http://speed.example.com/file" },
		"userinfo":       func(p *Policy) { p.StreamURL = "https://user:pass@speed.example.com/file" },
		"short interval": func(p *Policy) { p.StreamInterval = 3 * time.Minute },
	} {
		p := steadyPolicy()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestMeasureStreamsUsesWindowAndMedianThroughput(t *testing.T) {
	samples := append(pulls("alpha", StreamReset), withThroughput(pulls("alpha", StreamSuccess, StreamSuccess, StreamSuccess), 2*time.Second)...)
	stats := MeasureStreams("alpha", samples, 3)
	if stats.Samples != 3 || stats.Completed != 3 || stats.MedianThroughput != 131072 {
		t.Fatalf("stats = %+v", stats)
	}
}
```

In `monitor/policy_test.go`, extend the existing switch-policy validation case (around lines 324-333) so `steady_stream` is accepted when `StreamURL`/`StreamInterval` are set, while `"fastest"` stays rejected:

```go
	steady := DefaultPolicy()
	steady.SwitchPolicy = SwitchSteadyStream
	if err := steady.Validate(); err != nil {
		t.Fatalf("steady_stream with defaults rejected: %v", err)
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./monitor -run 'Steady|MeasureStreams|SwitchPolicy' -count=1`
Expected: FAIL, `undefined: SwitchSteadyStream`, `undefined: DecideSteady`.

- [ ] **Step 3: Implement**

`monitor/model.go` additions:

```go
type StreamOutcome string

const (
	StreamSuccess   StreamOutcome = "success"
	StreamReset     StreamOutcome = "reset"
	StreamStall     StreamOutcome = "stall"
	StreamTruncated StreamOutcome = "truncated"
	StreamTLS       StreamOutcome = "tls"
	StreamTimeout   StreamOutcome = "timeout"
	StreamError     StreamOutcome = "error"
)

// StreamSample is one HTTPS pull through one node. It is keyed by node name,
// not group, because one probe listener serves every group.
type StreamSample struct {
	Proxy, URL          string
	FinishedAt          time.Time
	Bytes               int64
	FirstByte, Duration time.Duration
	Outcome             StreamOutcome
}

func (s StreamSample) Completed() bool { return s.Outcome == StreamSuccess }

// Throughput is bytes per second over the whole pull; 0 when unmeasurable.
func (s StreamSample) Throughput() int64 {
	if s.Duration <= 0 {
		return 0
	}
	return int64(float64(s.Bytes) / s.Duration.Seconds())
}

type StreamStability struct {
	Proxy                string
	Samples, Completed   int
	MedianThroughput     int64
}
```

Add `Group string` and `Streams []StreamSample` to `Decision`, and `Streams []StreamSample` plus `StreamStability []StreamStability` to `Batch`.

`monitor/policy.go`:
- Add the constants listed in Interfaces, `StreamURL`/`StreamInterval` fields, and in `DefaultPolicy` set `StreamURL: DefaultStreamURL, StreamInterval: 5 * time.Minute`.
- Add `func ValidPolicyName(name string) bool { return slices.Contains(SwitchPolicies, name) }` and replace the enum check in `Validate` with:

```go
	if !ValidPolicyName(p.SwitchPolicy) {
		return fmt.Errorf("monitor policy: switch policy must be one of %s", strings.Join(SwitchPolicies, ", "))
	}
	if p.SwitchPolicy == SwitchSteadyStream {
		if err := ValidStreamURL(p.StreamURL); err != nil {
			return fmt.Errorf("monitor policy: %w", err)
		}
		if p.StreamInterval < MinStreamInterval || p.StreamInterval > 24*time.Hour {
			return fmt.Errorf("monitor policy: stream interval must be between 4m and 24h")
		}
	}
```

and add:

```go
// ValidStreamURL accepts an HTTPS URL without credentials or fragment; a
// stream probe through plain HTTP could be rewritten mid-body.
func ValidStreamURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || strings.ContainsAny(raw, "\x00\r\n#") || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return fmt.Errorf("stream URL must use HTTPS without credentials or fragment")
	}
	return nil
}
```

- Extract the shared early returns of `Decide` into one helper and call it from `Decide` (replacing the inline block at lines 120-135):

```go
// guard reports why no switch may happen now, or "" when policy may decide.
func guard(policy Policy, state GroupState, now time.Time) string {
	switch {
	case state.ManualOverride:
		return ReasonManualOverride
	case !state.AutomationEnabled:
		return ReasonAutomationDisabled
	case state.SwitchInFlight:
		return ReasonSwitchInFlight
	case state.LastSwitchAt.After(now) || !state.LastSwitchAt.IsZero() && now.Sub(state.LastSwitchAt) < policy.Cooldown:
		return ReasonCooldown
	}
	return ""
}
```

In `Decide`, set `decision := Decision{Group: state.Group, Old: state.Selected, Reason: ReasonInsufficientSamples}` and `if reason := guard(policy, state, now); reason != "" { decision.Reason = reason; return decision }`.

`monitor/steady.go`:

```go
package monitor

import (
	"sort"
	"time"
)

// DecideSteady leaves a node only after its own downloads break, and only for
// a node with proven completed downloads; speed breaks ties, never forces.
func DecideSteady(policy Policy, state GroupState, latency []Sample, streams map[string][]StreamSample, now time.Time) Decision {
	decision := Decision{Group: state.Group, Old: state.Selected, Reason: ReasonInsufficientSamples}
	if reason := guard(policy, state, now); reason != "" {
		decision.Reason = reason
		return decision
	}
	if state.Selected == "" || len(state.Proxies) == 0 {
		return decision
	}
	windows := make(map[string][]StreamSample, len(state.Proxies))
	allFailed, measured := true, 0
	for _, proxy := range state.Proxies {
		window := recentStreams(streams[proxy], policy.WindowSize)
		windows[proxy] = window
		if len(window) == 0 {
			continue
		}
		measured++
		if window[len(window)-1].Completed() {
			allFailed = false
		}
	}
	if measured > 1 && allFailed {
		decision.Reason = ReasonAllCandidatesFailed
		return decision
	}
	selected := MeasureStreams(state.Selected, windows[state.Selected], 0)
	interrupted := selected.Samples - selected.Completed
	if interrupted < policy.MaxFailures {
		if selected.Samples > 0 {
			decision.Reason = ReasonSelectedStreamsHealthy
		}
		return decision
	}
	latencyByProxy := make(map[string]time.Duration)
	byProxy := make(map[string][]Sample)
	for _, sample := range latency {
		if sample.Group == state.Group {
			byProxy[sample.Proxy] = append(byProxy[sample.Proxy], sample)
		}
	}
	for proxy, samples := range byProxy {
		latencyByProxy[proxy] = MeasureStability(recent(samples, policy.WindowSize), 0).Median
	}
	type candidate struct {
		name  string
		stats StreamStability
	}
	var eligible []candidate
	for _, proxy := range state.Proxies {
		window := windows[proxy]
		if proxy == state.Selected || len(window) == 0 || !window[len(window)-1].Completed() {
			continue
		}
		stats := MeasureStreams(proxy, window, 0)
		if stats.Completed < policy.MinCandidateSamples || stats.Samples-stats.Completed >= interrupted {
			continue
		}
		eligible = append(eligible, candidate{proxy, stats})
	}
	if len(eligible) == 0 {
		decision.Reason = ReasonNoBetterCandidate
		return decision
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if fa, fb := a.stats.Samples-a.stats.Completed, b.stats.Samples-b.stats.Completed; fa != fb {
			return fa < fb
		}
		if a.stats.MedianThroughput != b.stats.MedianThroughput {
			return a.stats.MedianThroughput > b.stats.MedianThroughput
		}
		la, lb := latencyByProxy[a.name], latencyByProxy[b.name]
		if la != lb && la > 0 && lb > 0 {
			return la < lb
		}
		return a.name < b.name
	})
	best := eligible[0].name
	decision.Switch, decision.New, decision.Reason = true, best, ReasonSteadierStream
	decision.Streams = append(append([]StreamSample(nil), windows[state.Selected]...), windows[best]...)
	for _, sample := range latency {
		if sample.Group == state.Group && (sample.Proxy == state.Selected || sample.Proxy == best) {
			decision.Evidence = append(decision.Evidence, sample)
		}
	}
	return decision
}

func recentStreams(samples []StreamSample, limit int) []StreamSample {
	out := append([]StreamSample(nil), samples...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].FinishedAt.Before(out[j].FinishedAt) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// MeasureStreams summarizes the last windowSize pulls (0 = all).
func MeasureStreams(proxy string, samples []StreamSample, windowSize int) StreamStability {
	window := recentStreams(samples, windowSize)
	stats := StreamStability{Proxy: proxy, Samples: len(window)}
	var rates []int64
	for _, sample := range window {
		if sample.Completed() {
			stats.Completed++
			rates = append(rates, sample.Throughput())
		}
	}
	if len(rates) > 0 {
		sort.Slice(rates, func(i, j int) bool { return rates[i] < rates[j] })
		stats.MedianThroughput = rates[len(rates)/2]
	}
	return stats
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./monitor -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w monitor
git add monitor
git commit -m "feat(monitor): decide steady-stream switches from pull outcomes"
```

---

### Task 2: Stream pull through the probe listener

**Files:**
- Create: `mihomo/stream.go`, `mihomo/stream_test.go`

**Interfaces:**
- Consumes: `Controller.Select(ctx, group, proxy string) error` (mihomo/controller.go:155)
- Produces:
  - `const StreamProbeName = "clashpulse-stream-probe"`, `StreamStallTimeout = 5 * time.Second`, `StreamPullTimeout = 30 * time.Second`
  - `type StreamListener struct { Port int; Username, Password string }`
  - `type StreamResult struct { Bytes int64; FirstByte, Duration time.Duration; Outcome string }` (Outcome values identical to `monitor.StreamOutcome` strings)
  - `func (c *Controller) StreamPull(ctx context.Context, listener StreamListener, proxy, rawURL string) StreamResult`
  - `func pullThrough(ctx context.Context, transport http.RoundTripper, rawURL string, stall, total time.Duration) StreamResult` (unexported; tested directly)

- [ ] **Step 1: Write the failing tests**

`mihomo/stream_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./mihomo -run 'PullThrough|StreamPull' -count=1`
Expected: FAIL, `undefined: pullThrough`.

- [ ] **Step 3: Implement** `mihomo/stream.go`:

```go
package mihomo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// StreamProbeName names both the hidden probe group and its listener.
const StreamProbeName = "clashpulse-stream-probe"

const (
	StreamStallTimeout = 5 * time.Second
	StreamPullTimeout  = 30 * time.Second
)

// StreamListener is the per-start probe listener; its credential lives only
// in memory and is never part of a result or error.
type StreamListener struct {
	Port               int
	Username, Password string
}

type StreamResult struct {
	Bytes               int64
	FirstByte, Duration time.Duration
	Outcome             string
}

// StreamPull points the probe group at proxy and pulls rawURL through the
// pinned listener, so the measurement follows that node only.
func (c *Controller) StreamPull(ctx context.Context, listener StreamListener, proxy, rawURL string) StreamResult {
	if err := c.Select(ctx, StreamProbeName, proxy); err != nil {
		return StreamResult{Outcome: "error"}
	}
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", listener.Port), User: url.UserPassword(listener.Username, listener.Password)}
	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: StreamStallTimeout,
	}
	defer transport.CloseIdleConnections()
	return pullThrough(ctx, transport, rawURL, StreamStallTimeout, StreamPullTimeout)
}

func pullThrough(ctx context.Context, transport http.RoundTripper, rawURL string, stall, total time.Duration) StreamResult {
	ctx, cancel := context.WithTimeout(ctx, total)
	defer cancel()
	started := time.Now()
	result := StreamResult{Outcome: "error"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return result
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		result.Outcome = classify(ctx, err, false)
		return result
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength <= 0 {
		return result
	}
	// The stall timer cancels the request; the flag tells a stall from the
	// overall deadline, which surfaces as DeadlineExceeded instead.
	var stalled atomic.Bool
	timer := time.AfterFunc(stall, func() { stalled.Store(true); cancel() })
	defer timer.Stop()
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if result.Bytes == 0 {
				result.FirstByte = time.Since(started)
			}
			result.Bytes += int64(n)
			timer.Reset(stall)
		}
		if readErr == nil {
			continue
		}
		result.Duration = time.Since(started)
		switch {
		case errors.Is(readErr, io.EOF) && result.Bytes == response.ContentLength:
			result.Outcome = "success"
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			result.Outcome = "truncated"
		default:
			result.Outcome = classify(ctx, readErr, stalled.Load())
		}
		return result
	}
}

// classify maps a transport error to an outcome. A cancellation by the stall
// timer is a stall; the overall deadline is a timeout.
func classify(ctx context.Context, err error, stalled bool) string {
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	switch {
	case errors.As(err, &certificate) || errors.As(err, &record):
		return "tls"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timeout"
	case stalled:
		return "stall"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "reset"
	}
	return "error"
}
```

Add `"sync/atomic"` to the imports. Keep the classification order: TLS first (Go wraps unknown-authority and hostname failures in `*tls.CertificateVerificationError`), total deadline second, stall third, connection errors fourth.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./mihomo -run 'PullThrough|StreamPull' -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w mihomo
git add mihomo/stream.go mihomo/stream_test.go
git commit -m "feat(mihomo): pull an HTTPS file through the stream probe listener"
```

---

### Task 3: Render the probe group and listener; capability gate

**Files:**
- Modify: `mihomo/render.go` (ControllerSettings, Render), `mihomo/binary.go` (Capability, Inspect)
- Modify: `core/snapshot.go` (`ConfigOverrideKeys` gains `"listeners"`), `localize/locale/zh-CN.toml` and `localize/locale/en.toml` (`override.listeners`)
- Modify: `app/runtime.go` (`compatibilityFailure` capability list)
- Test: `mihomo/render_test.go`, `mihomo/binary_test.go`

**Interfaces:**
- Consumes: `monitor.SwitchSteadyStream` (Task 1), `StreamProbeName`, `StreamListener` (Task 2)
- Produces:
  - `ControllerSettings.StreamProbe StreamListener` (zero value = no listener)
  - `Capability.SupportsPinnedListener bool`
  - Render emits group `{name: clashpulse-stream-probe, type: select, include-all: true}` and listener `{name: clashpulse-stream-probe, type: mixed, listen: 127.0.0.1, port, proxy: clashpulse-stream-probe, users: [{username, password}]}` only when `intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream`.

- [ ] **Step 1: Write the failing tests**

Append to `mihomo/render_test.go` (reuse the file's existing profile/intent helpers; `steadyIntent` below sets only what Render reads):

```go
func steadyIntent() config.Snapshot {
	var intent config.Snapshot
	intent.Monitor.SwitchPolicy = monitor.SwitchSteadyStream
	return intent
}

var probeListener = ControllerSettings{Address: "127.0.0.1:9090", Secret: "s", ProxyPort: 7899,
	StreamProbe: StreamListener{Port: 17922, Username: "u", Password: "p"}}

func TestRenderAddsStreamProbeOnlyForSteadyStream(t *testing.T) {
	profile := []byte("proxies:\n  - {name: node-a, type: socks5, server: 192.0.2.1, port: 1080}\nproxy-groups:\n  - {name: main, type: select, proxies: [node-a]}\n")
	capability := Capability{SupportsPinnedListener: true}
	out, err := Render(profile, steadyIntent(), nil, probeListener, capability)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Groups    []map[string]any `yaml:"proxy-groups"`
		Listeners []map[string]any `yaml:"listeners"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Groups) != 2 || doc.Groups[1]["name"] != StreamProbeName || doc.Groups[1]["include-all"] != true {
		t.Fatalf("groups = %v", doc.Groups)
	}
	if len(doc.Listeners) != 1 || doc.Listeners[0]["proxy"] != StreamProbeName || doc.Listeners[0]["listen"] != "127.0.0.1" || doc.Listeners[0]["port"] != 17922 {
		t.Fatalf("listeners = %v", doc.Listeners)
	}
	var intent config.Snapshot
	intent.Monitor.SwitchPolicy = monitor.SwitchLowestLatency
	out, err = Render(profile, intent, nil, probeListener, capability)
	if err != nil || bytes.Contains(out, []byte(StreamProbeName)) {
		t.Fatalf("lowest_latency rendered the probe: %v", err)
	}
}

func TestRenderRejectsReservedStreamProbeName(t *testing.T) {
	for name, profile := range map[string]string{
		"proxy": "proxies:\n  - {name: clashpulse-stream-probe, type: direct}\n",
		"group": "proxies:\n  - {name: node-a, type: direct}\nproxy-groups:\n  - {name: clashpulse-stream-probe, type: select, proxies: [node-a]}\n",
	} {
		if _, err := Render([]byte(profile), steadyIntent(), nil, probeListener, Capability{SupportsPinnedListener: true}); err == nil || !strings.Contains(err.Error(), StreamProbeName) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	listener := "proxies:\n  - {name: node-a, type: direct}\nlisteners:\n  - {name: x, type: mixed, port: 1}\n"
	if _, err := Render([]byte(listener), steadyIntent(), nil, probeListener, Capability{SupportsPinnedListener: true}); err == nil {
		t.Fatal("profile listeners accepted")
	}
}

func TestRenderSteadyStreamRequiresPinnedListener(t *testing.T) {
	profile := []byte("proxies:\n  - {name: node-a, type: direct}\n")
	_, err := Render(profile, steadyIntent(), nil, probeListener, Capability{})
	var missing *CapabilityError
	if !errors.As(err, &missing) || missing.ResourceID != "monitor.switch_policy" || missing.Kind != "pinned-listener" {
		t.Fatalf("err = %v", err)
	}
}
```

Append to `mihomo/binary_test.go`, reusing the existing fake-executable helper in that file; the fake must accept `-t` only when the fixture lacks `listeners` for the "unsupported" case:

```go
func TestInspectRecordsPinnedListenerFromValidation(t *testing.T) {
	supported := fakeInspectBinary(t, "v1.19.31", func(config string) bool { return true })
	capability, err := Inspect(context.Background(), Selection{Kind: "path", Path: supported})
	if err != nil || !capability.SupportsPinnedListener {
		t.Fatalf("supported binary: %+v %v", capability, err)
	}
	rejecting := fakeInspectBinary(t, "v1.19.31", func(config string) bool { return !strings.Contains(config, "listeners:") })
	capability, err = Inspect(context.Background(), Selection{Kind: "path", Path: rejecting})
	if err != nil || capability.SupportsPinnedListener {
		t.Fatalf("rejecting binary: %+v %v", capability, err)
	}
}
```

`fakeInspectBinary(t, version, accept func(config string) bool) string` writes a `#!/bin/sh` script into `t.TempDir()` that prints `Mihomo Meta <version> linux amd64` on `-v`, and on `-t -f <file>` exits 0 if the file content satisfies `accept` (implement by writing two scripts: one that always exits 0, one that greps `listeners:` and exits 1 when found). Skip on non-Linux like the existing fake-binary tests.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./mihomo -run 'StreamProbe|PinnedListener' -count=1`
Expected: FAIL, unknown field `StreamProbe`, `SupportsPinnedListener`.

- [ ] **Step 3: Implement**

`mihomo/render.go`:
- Add `StreamProbe StreamListener` to `ControllerSettings`.
- Keep the existing `listeners`/`tunnels` rejection loop. Immediately after it, add:

```go
	if intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream {
		if !capability.SupportsPinnedListener {
			return nil, &CapabilityError{ResourceID: "monitor.switch_policy", Kind: "pinned-listener"}
		}
		if err := renderStreamProbe(document, controller.StreamProbe); err != nil {
			return nil, err
		}
	}
```

and:

```go
// renderStreamProbe adds the hidden probe group and its authenticated
// listener. include-all lets Mihomo resolve provider nodes the render cannot see.
func renderStreamProbe(document map[string]any, listener StreamListener) error {
	if listener.Port <= 0 || listener.Username == "" || listener.Password == "" {
		return errors.New("mihomo: stream probe listener is not configured")
	}
	for _, section := range []string{"proxies", "proxy-groups"} {
		items, _ := document[section].([]any)
		for _, item := range items {
			if entry, ok := item.(map[string]any); ok && entry["name"] == StreamProbeName {
				return fmt.Errorf("mihomo: source profile already defines %q", StreamProbeName)
			}
		}
	}
	groups, _ := document["proxy-groups"].([]any)
	document["proxy-groups"] = append(groups, map[string]any{"name": StreamProbeName, "type": "select", "include-all": true})
	document["listeners"] = []any{map[string]any{
		"name": StreamProbeName, "type": "mixed", "listen": "127.0.0.1", "port": listener.Port, "proxy": StreamProbeName,
		"users": []any{map[string]any{"username": listener.Username, "password": listener.Password}},
	}}
	return nil
}
```

- `core/snapshot.go`: append `"listeners"` to `ConfigOverrideKeys`. Add catalog entries:
  - `localize/locale/en.toml`: `["override.listeners"]\nother = "ClashPulse adds a private loopback listener for steady-stream download probes."`
  - `localize/locale/zh-CN.toml`: `["override.listeners"]\nother = "ClashPulse 为稳定流下载探测添加了私有回环监听器。"`
  - In `ipc/protocol.go` validateSnapshot the bound `len(snapshot.ConfigOverrides) > len(core.ConfigOverrideKeys)` follows automatically.

`mihomo/binary.go`:
- Add `SupportsPinnedListener bool` to `Capability`.
- In `Inspect`, after the existing `-t` check succeeds, write a second private fixture in the same temp dir and run the same `exec.CommandContext(ctx, path, "-t", "-f", pinned)`; set `SupportsPinnedListener = err == nil`:

```go
const pinnedListenerFixture = "proxies:\n  - name: clashpulse-inspect\n    type: direct\nproxy-groups:\n  - name: clashpulse-inspect-group\n    type: select\n    proxies: [clashpulse-inspect]\nlisteners:\n  - name: clashpulse-inspect-listener\n    type: mixed\n    listen: 127.0.0.1\n    port: 1\n    proxy: clashpulse-inspect-group\n    users:\n      - username: inspect\n        password: inspect\n"
```

Write it with mode `0600` like the existing fixture. A failed pinned check is not an Inspect error.

`app/runtime.go` `compatibilityFailure`: extend the capability list `[]string{"geoip.dat", "geosite.dat", "Country.mmdb"}` with `"pinned-listener"`, so the message becomes `resource monitor.switch_policy requires pinned-listener`. Where `state.Binary.Capabilities` is built (app/runtime.go:354-363), append `"pinned-listener"` when `SupportsPinnedListener`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./mihomo ./core -count=1 && go vet -tags ci ./app`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w mihomo core app
git add mihomo core app/runtime.go localize/locale/en.toml localize/locale/zh-CN.toml
git commit -m "feat(mihomo): render a pinned stream probe listener under steady_stream"
```

---

### Task 4: Settings: config, IPC, snapshots, seed

**Files:**
- Modify: `config/types.go`, `config/toml_decode.go`, `config/load.go`, `config/write.go`
- Modify: `ipc/protocol.go` (ConfigPatch, validation, ProtocolVersion 11, snapshot bounds)
- Modify: `core/snapshot.go` (MonitorSnapshot, ProxySnapshot, SwitchSnapshot)
- Modify: `app/commands.go` (`patchSettings`), `app/lifecycle.go` (`monitorPolicy`), `app/runtime.go` (MonitorSnapshot fill)
- Modify: `examples/config.toml`
- Test: `config/load_test.go`, `ipc/protocol_test.go`, `app/monitor_policy_test.go`

**Interfaces:**
- Consumes: `monitor.SwitchPolicies`, `monitor.ValidPolicyName`, `monitor.ValidStreamURL`, `monitor.MinStreamInterval`, `monitor.DefaultStreamURL` (Task 1)
- Produces:
  - `config.Monitor.StreamURL string`, `config.Monitor.StreamInterval time.Duration`
  - `config.SettingsPatch.MonitorStreamURL *string`, `MonitorStreamInterval *time.Duration`
  - `ipc.ConfigPatch.MonitorStreamURL *string` (`json:"monitor_stream_url,omitempty"`), `MonitorStreamIntervalSeconds *uint32` (`json:"monitor_stream_interval_seconds,omitempty"`)
  - `core.MonitorSnapshot.StreamURLConfigured bool`, `StreamIntervalSeconds int64` (the URL itself stays out of snapshots, like a private source)
  - `core.ProxySnapshot.StreamSamples, StreamCompleted int`, `StreamKBps int64`
  - `core.StreamProbeSnapshot{ProxyID string; FinishedAt int64; Outcome string; KBps int64}`; `core.SwitchSnapshot.StreamEvidence []StreamProbeSnapshot`

- [ ] **Step 1: Write the failing tests**

`config/load_test.go`, next to `TestLoadSwitchPolicyAndURLTestDelays`:

```go
func TestLoadSteadyStreamSettings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.toml"), "[monitor]\nswitch_policy = \"steady_stream\"\nstream_url = \"https://speed.example.com/file\"\nstream_interval = \"10m\"\n")
	snapshot, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Monitor.SwitchPolicy != "steady_stream" || snapshot.Monitor.StreamURL != "https://speed.example.com/file" || snapshot.Monitor.StreamInterval != 10*time.Minute {
		t.Fatalf("monitor = %+v", snapshot.Monitor)
	}
	empty := t.TempDir()
	defaults, err := Load(empty)
	if err != nil || defaults.Monitor.StreamURL != monitor.DefaultStreamURL || defaults.Monitor.StreamInterval != 5*time.Minute {
		t.Fatalf("defaults = %+v, %v", defaults.Monitor, err)
	}
	for name, body := range map[string]string{
		"http stream url":  "[monitor]\nstream_url = \"http://speed.example.com/file\"\n",
		"short interval":   "[monitor]\nstream_interval = \"1m\"\n",
		"unknown policy":   "[monitor]\nswitch_policy = \"fastest\"\n",
	} {
		bad := t.TempDir()
		writeFile(t, filepath.Join(bad, "config.toml"), body)
		if _, err := Load(bad); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
```

Use the file's existing temp-file helper; if it is named differently than `writeFile`, use that name.

Also extend `TestPatchSettingsPersistsSwitchPolicyAndURLTestDelays` with a `MonitorStreamURL` + `MonitorStreamInterval` patch and assert the reloaded snapshot carries both.

`ipc/protocol_test.go`, in the ConfigPatch validation table: valid `MonitorSwitchPolicy: new("steady_stream")`, valid `MonitorStreamURL: new("https://speed.example.com/file")`, `MonitorStreamIntervalSeconds: new(uint32(300))`; invalid `MonitorStreamURL: new("http://speed.example.com/file")`, `MonitorStreamIntervalSeconds: new(uint32(60))`.

`app/monitor_policy_test.go`: in `TestLoadedMonitorPolicyControlsDecisionAndConcurrency`, add a config with `switch_policy = "steady_stream"` and assert `monitorPolicy(snapshot.Monitor)` has `StreamURL` and `StreamInterval` from the file and `Validate()` succeeds.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./config ./ipc -count=1 && go test -tags ci ./app -run 'MonitorPolicy|Seed' -count=1`
Expected: FAIL on unknown keys `stream_url`, `stream_interval` and missing fields.

- [ ] **Step 3: Implement**

- `config/types.go`: add `StreamURL string` and `StreamInterval time.Duration` to `Monitor` after `SwitchPolicy`.
- `config/toml_decode.go`: add to the monitor doc struct `StreamURL *string \`toml:"stream_url"\`` and `StreamInterval *string \`toml:"stream_interval"\``; map with `streamInterval, err := durationValue(path, "monitor.stream_interval", doc.Monitor.StreamInterval)` and copy `*doc.Monitor.StreamURL` when non-nil (explicit empty string stays empty so validation rejects it).
- `config/load.go` `applyDefaults`: `if s.Monitor.StreamURL == "" { s.Monitor.StreamURL = monitor.DefaultStreamURL }` and `if s.Monitor.StreamInterval == 0 { s.Monitor.StreamInterval = monitor.DefaultPolicy().StreamInterval }`. Validation: replace the two-value enum check with:

```go
	if !monitor.ValidPolicyName(m.SwitchPolicy) {
		return fmt.Errorf("config.toml: monitor.switch_policy: must be one of %s", strings.Join(monitor.SwitchPolicies, ", "))
	}
	if err := monitor.ValidStreamURL(m.StreamURL); err != nil {
		return fmt.Errorf("config.toml: monitor.stream_url: %w", err)
	}
	if m.StreamInterval < monitor.MinStreamInterval || m.StreamInterval > maxMonitorInterval {
		return fmt.Errorf("config.toml: monitor.stream_interval: must be between 4m and 24h")
	}
```

  Note the explicit-empty `stream_url` case must fail with `config.toml: monitor.stream_url: required` like `test_url` does.
- `config/write.go`: add `MonitorStreamURL *string` and `MonitorStreamInterval *time.Duration` to `SettingsPatch`; in `PatchSettings`:

```go
	if patch.MonitorStreamURL != nil {
		document.Monitor.StreamURL = patch.MonitorStreamURL
		next.Monitor.StreamURL = *patch.MonitorStreamURL
	}
	if patch.MonitorStreamInterval != nil {
		value := patch.MonitorStreamInterval.String()
		document.Monitor.StreamInterval = &value
		next.Monitor.StreamInterval = *patch.MonitorStreamInterval
	}
```

- `ipc/protocol.go`: `ProtocolVersion uint16 = 11`. Add the two ConfigPatch fields. In `Command.validate`, replace the switch-policy check with `!monitor.ValidPolicyName(*p.MonitorSwitchPolicy)`, and add:

```go
		if p.MonitorStreamURL != nil && monitor.ValidStreamURL(*p.MonitorStreamURL) != nil {
			return errors.New("invalid monitor stream URL")
		}
		if p.MonitorStreamIntervalSeconds != nil && (time.Duration(*p.MonitorStreamIntervalSeconds)*time.Second < monitor.MinStreamInterval || *p.MonitorStreamIntervalSeconds > 86400) {
			return errors.New("monitor stream interval out of range")
		}
```

  In `validateSnapshot`, extend the proxy-stability check: `proxy.StreamCompleted < 0 || proxy.StreamCompleted > proxy.StreamSamples || proxy.StreamSamples > core.MonitorWindowMax || proxy.StreamKBps < 0`; count each switch's `StreamEvidence` with `addItems` and its strings with `addText`.
- `core/snapshot.go`: add the snapshot fields from Interfaces; `CloneSnapshot` deep-copies `Switches[i].StreamEvidence`. `MonitorSnapshot` stays scalar-only (it is compared with `!=`).
- `app/commands.go` `patchSettings`: map `MonitorStreamURL: patch.MonitorStreamURL, MonitorStreamInterval: durationPatch(patch.MonitorStreamIntervalSeconds, time.Second)`.
- `app/lifecycle.go` `monitorPolicy`: `policy.StreamURL = settings.StreamURL; policy.StreamInterval = settings.StreamInterval`.
- `app/runtime.go` MonitorSnapshot fill: `StreamURLConfigured: settings.Monitor.StreamURL != "", StreamIntervalSeconds: int64(settings.Monitor.StreamInterval.Seconds())`.
- `tui/app.go` `commandHasPrivateSource`: also treat `command.Config.MonitorStreamURL != nil` as private.
- `examples/config.toml`: replace the `switch_policy` line with exactly:

```toml
switch_policy = "lowest_latency" # "lowest_latency" (fastest): the lowest median latency wins. "failover": move only when the current node is unhealthy. "steady_stream": move away from a node whose HTTPS downloads break.
stream_url = "https://speed.cloudflare.com/__down?bytes=262144" # steady_stream only: a 256 KiB HTTPS file pulled through each probed node.
stream_interval = "5m" # steady_stream only: pulls the selected node and the 3 fastest others, about 12 MB/hour per automated group.
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./config ./ipc ./core -count=1 && go test -tags ci ./app -run 'MonitorPolicy|Seed|MonitorIPC' -count=1`
Expected: PASS (the seed test compares the seeded file to `examples/config.toml` byte for byte and loads it).

- [ ] **Step 5: Commit**

```bash
gofmt -w config ipc core app
git add config ipc core app examples/config.toml tui/app.go
git commit -m "feat(config): add steady_stream, stream_url, and stream_interval settings"
```

---

### Task 5: Scheduler stream rounds

**Files:**
- Modify: `monitor/scheduler.go`
- Create: `monitor/stream_scheduler_test.go`
- Modify: every `NewScheduler` call site: `app/lifecycle.go:~1019`, `app/proxy_latency_test.go:45`, `app/monitor_policy_test.go:65`, `monitor/scheduler_test.go`, `monitor/outcome_test.go`, `monitor/roster_test.go`, `monitor/notification_test.go`

**Interfaces:**
- Consumes: Task 1 types and `DecideSteady`, `MeasureStreams`
- Produces:
  - `type StreamProber interface { StreamPull(ctx context.Context, proxy, url string) StreamSample }`
  - `func NewScheduler(policy Policy, prober DelayProber, streamer StreamProber, onBatch func(Batch)) (*Scheduler, error)`; `streamer` may be nil unless `policy.SwitchPolicy == SwitchSteadyStream` (then error)
  - `func (s *Scheduler) SetStreamMembers(names []string)` (probe-group members; only these are pulled)
  - `func streamTargets(groups []GroupState, history map[string]map[string][]Sample, members map[string]bool, windowSize int) []string`
  - Stream batches: `Batch{FinishedAt, Streams, StreamStability, Decisions}` where Decisions come from `DecideSteady` per automated group. Under `steady_stream`, latency batches carry no Decisions.

- [ ] **Step 1: Write the failing tests**

`monitor/stream_scheduler_test.go`:

```go
package monitor

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

type recordingStreamer struct {
	mu       sync.Mutex
	pulled   []string
	outcome  map[string]StreamOutcome
	block    chan struct{}
}

func (r *recordingStreamer) StreamPull(ctx context.Context, proxy, url string) StreamSample {
	r.mu.Lock()
	r.pulled = append(r.pulled, proxy)
	outcome, ok := r.outcome[proxy]
	r.mu.Unlock()
	if !ok {
		outcome = StreamSuccess
	}
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Outcome: StreamError}
		}
	}
	return StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Bytes: 262144, Duration: time.Second, Outcome: outcome}
}

func latencyHistory(group string, medians map[string]time.Duration) map[string]map[string][]Sample {
	policy := DefaultPolicy()
	history := map[string][]Sample{}
	for proxy, latency := range medians {
		history[proxy] = []Sample{testSample(policy, group, proxy, latency, 1)}
	}
	return map[string]map[string][]Sample{group: history}
}

func TestStreamTargetsSelectedThenThreeFastest(t *testing.T) {
	groups := []GroupState{{Group: "auto", Selected: "e", Proxies: []string{"a", "b", "c", "d", "e"}, ProbeEnabled: true, AutomationEnabled: true}}
	history := latencyHistory("auto", map[string]time.Duration{"a": 300, "b": 100, "c": 200, "d": 50, "e": 400})
	members := map[string]bool{"a": true, "b": true, "c": true, "d": true, "e": true}
	got := streamTargets(groups, history, members, 5)
	if !slices.Equal(got, []string{"e", "d", "b", "c"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestStreamTargetsSkipNonMembers(t *testing.T) {
	groups := []GroupState{{Group: "auto", Selected: "a", Proxies: []string{"a", "DIRECT", "nested"}, ProbeEnabled: true, AutomationEnabled: true}}
	history := latencyHistory("auto", map[string]time.Duration{"a": 100, "DIRECT": 1, "nested": 2})
	got := streamTargets(groups, history, map[string]bool{"a": true}, 5)
	if !slices.Equal(got, []string{"a"}) {
		t.Fatalf("targets = %v, want only probe-group members", got)
	}
}

func TestStreamRoundDeduplicatesSharedNodes(t *testing.T) {
	groups := []GroupState{
		{Group: "one", Selected: "shared", Proxies: []string{"shared", "x"}, ProbeEnabled: true, AutomationEnabled: true},
		{Group: "two", Selected: "shared", Proxies: []string{"shared", "y"}, ProbeEnabled: true, AutomationEnabled: true},
	}
	history := map[string]map[string][]Sample{}
	for _, g := range []string{"one", "two"} {
		for group, h := range latencyHistory(g, map[string]time.Duration{"shared": 100, "x": 200, "y": 300}) {
			history[group] = h
		}
	}
	got := streamTargets(groups, history, map[string]bool{"shared": true, "x": true, "y": true}, 5)
	if !slices.Equal(got, []string{"shared", "x", "y"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestStreamTargetsCapAtEight(t *testing.T) {
	var groups []GroupState
	members := map[string]bool{}
	history := map[string]map[string][]Sample{}
	for _, g := range []string{"g1", "g2", "g3"} {
		proxies := []string{g + "-sel", g + "-a", g + "-b", g + "-c"}
		groups = append(groups, GroupState{Group: g, Selected: g + "-sel", Proxies: proxies, ProbeEnabled: true, AutomationEnabled: true})
		medians := map[string]time.Duration{}
		for i, p := range proxies {
			members[p] = true
			medians[p] = time.Duration(i+1) * time.Millisecond
		}
		for group, h := range latencyHistory(g, medians) {
			history[group] = h
		}
	}
	got := streamTargets(groups, history, members, 5)
	if len(got) != StreamPullCap || !slices.Equal(got[:3], []string{"g1-sel", "g2-sel", "g3-sel"}) {
		t.Fatalf("targets = %v, want 8 with selected nodes first", got)
	}
}

func steadySchedulerPolicy() Policy {
	policy := DefaultPolicy()
	policy.SwitchPolicy = SwitchSteadyStream
	policy.Interval = time.Hour
	policy.Jitter = 0
	return policy
}

func TestStreamRoundEmitsSteadyDecision(t *testing.T) {
	streamer := &recordingStreamer{outcome: map[string]StreamOutcome{"alpha": StreamReset}}
	batches := make(chan Batch, 16)
	scheduler, err := NewScheduler(steadySchedulerPolicy(), instantProber{}, streamer, func(b Batch) { batches <- b })
	if err != nil {
		t.Fatal(err)
	}
	scheduler.SetStreamMembers([]string{"alpha", "beta"})
	if err := scheduler.SetGroups([]GroupState{{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true, AutomationEnabled: true}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.After(5 * time.Second)
	for {
		select {
		case batch := <-batches:
			if len(batch.Streams) > 0 {
				if len(batch.Streams) != 2 || len(batch.StreamStability) != 2 {
					t.Fatalf("stream batch = %+v", batch)
				}
				return
			}
		case <-deadline:
			t.Fatal("no stream batch")
		}
	}
}

func TestStreamRoundCancelsOnStop(t *testing.T) {
	streamer := &recordingStreamer{block: make(chan struct{})}
	batches := make(chan Batch, 16)
	scheduler, err := NewScheduler(steadySchedulerPolicy(), instantProber{}, streamer, func(b Batch) { batches <- b })
	if err != nil {
		t.Fatal(err)
	}
	scheduler.SetStreamMembers([]string{"alpha", "beta"})
	_ = scheduler.SetGroups([]GroupState{{Group: "auto", Selected: "alpha", Proxies: []string{"alpha", "beta"}, ProbeEnabled: true, AutomationEnabled: true}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	for start := time.Now(); ; {
		streamer.mu.Lock()
		n := len(streamer.pulled)
		streamer.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("pull never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return while a pull was blocked")
	}
	for len(batches) > 0 {
		if batch := <-batches; len(batch.Streams) > 0 {
			t.Fatal("cancelled stream round emitted a batch")
		}
	}
}

func TestNewSchedulerRequiresStreamerForSteadyStream(t *testing.T) {
	if _, err := NewScheduler(steadySchedulerPolicy(), instantProber{}, nil, func(Batch) {}); err == nil {
		t.Fatal("steady_stream scheduler without a streamer accepted")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./monitor -run 'Stream' -count=1`
Expected: FAIL to compile (`NewScheduler` arity, `streamTargets`, `SetStreamMembers`).

- [ ] **Step 3: Implement** in `monitor/scheduler.go`:

- Add `StreamProber` interface; `Scheduler` gains `streamer StreamProber` and `members map[string]bool` (guarded by `mu`). `NewScheduler(policy, prober, streamer, onBatch)` returns an error when `policy.SwitchPolicy == SwitchSteadyStream && streamer == nil`.
- `SetStreamMembers(names []string)`: under `mu`, replace `members` when changed, then `signal()`.
- `streamTargets`:

```go
// streamTargets lists the nodes one stream round pulls: every automated
// group's selected node first, then each group's three fastest healthy
// members, deduplicated and capped so one serialized round fits its interval.
func streamTargets(groups []GroupState, history map[string]map[string][]Sample, members map[string]bool, windowSize int) []string {
	var out []string
	seen := map[string]bool{}
	add := func(proxy string) {
		if members[proxy] && !seen[proxy] && len(out) < StreamPullCap {
			seen[proxy] = true
			out = append(out, proxy)
		}
	}
	for _, group := range groups {
		if group.AutomationEnabled {
			add(group.Selected)
		}
	}
	for _, group := range groups {
		if !group.AutomationEnabled {
			continue
		}
		type ranked struct {
			proxy  string
			median time.Duration
		}
		var fastest []ranked
		for _, proxy := range group.Proxies {
			samples := recent(history[group.Group][proxy], windowSize)
			if proxy == group.Selected || !members[proxy] || len(samples) == 0 || sampleFailed(samples[len(samples)-1]) {
				continue
			}
			fastest = append(fastest, ranked{proxy, MeasureStability(samples, 0).Median})
		}
		sort.SliceStable(fastest, func(i, j int) bool { return fastest[i].median < fastest[j].median })
		for i := 0; i < len(fastest) && i < StreamCandidates; i++ {
			add(fastest[i].proxy)
		}
	}
	return out
}
```

- In `Run`, only when `s.policy.SwitchPolicy == SwitchSteadyStream`:
  - State: `streamNext time.Time` (initially `time.Now()` once any automated group exists), `streamHistory map[string][]StreamSample`, `streamPending []string`, `streamActive bool`, `streamSamples []StreamSample`, `streamGeneration uint64`, and one channel pair `streamJobs chan string` (cap 0) / `streamResults chan StreamSample` (cap 1) served by exactly one worker goroutine started with the delay workers and waited in the same deferred `Wait`. The worker calls `s.streamer.StreamPull(runCtx, proxy, s.policy.StreamURL)` and sends the sample unless `runCtx` is done.
  - Round start (in the `!active` block, after latency due-check): if `!streamActive && !streamNext.IsZero() && !time.Now().Before(streamNext)`, compute `streamPending = streamTargets(groups, histories, members, WindowSize)` where `histories[group] = runtime.history`; set `streamNext = time.Now().Add(s.policy.StreamInterval + s.jitter())`; `streamActive = len(streamPending) > 0`; record `streamGeneration = s.generation`.
  - Timer: `nextDeadline` also considers `streamNext` when steady and not active.
  - Select: add a `streamSend` case (nil channel when no pending pull) sending `streamPending[0]`; on `sample := <-streamResults`, append to `streamSamples` and to `streamHistory[sample.Proxy]`, trimming to `WindowSize` and dropping samples older than `6 * StreamInterval`. When `streamPending` is empty and the last result arrived: if `streamGeneration == s.generation`, emit `Batch{FinishedAt: now, Streams: streamSamples, StreamStability: MeasureStreams per pulled proxy (WindowSize), Decisions: DecideSteady(s.policy, runtime.state, historySamples(runtime), streamHistory, now) for each runtime whose state is AutomationEnabled}`; reset round state.
  - Latency batches: when steady, set `batch.Decisions = nil` (latency still feeds stability, alerts, and the candidate ranking).
  - On `ctx.Done()`: return without emitting a stream batch.
- Update every `NewScheduler` call site to pass `nil` as `streamer` (tests) or the app adapter (Task 6).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./monitor -count=1 -race && go vet -tags ci ./app`
Expected: PASS; app compiles with `nil` streamer.

- [ ] **Step 5: Commit**

```bash
gofmt -w monitor app
git add monitor app
git commit -m "feat(monitor): schedule serialized steady-stream rounds"
```

---

### Task 6: App wiring: credentials, port, hidden group, batches

**Files:**
- Modify: `app/runtime.go` (fields), `app/lifecycle.go` (`renderWithHome`, `startProcess`, `refreshGroups`, scheduler creation), `app/commands.go` (`acceptBatch`), `core/activation.go` (new stage), localize catalogs
- Create: `app/stream_probe.go`, `app/stream_probe_test.go`

**Interfaces:**
- Consumes: Tasks 2-5
- Produces:
  - `runtimeService.streamProbe mihomo.StreamListener` (zero unless steady_stream), regenerated on every render under steady_stream
  - `type streamProber struct{ controller *mihomo.Controller; listener mihomo.StreamListener }` implementing `monitor.StreamProber`
  - `core.ActivationStreamProbePort ActivationStage = "stream_probe_port"` with message `"the stream probe port is unavailable"`
  - Diagnostics kind `"stream_probe"` with `SourceID` = opaque proxy ID and message code `stream.outcome.<outcome>` for interrupted pulls only

- [ ] **Step 1: Write the failing tests**

`app/stream_probe_test.go`:

```go
package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
)

func TestNewStreamListenerIsPrivateAndFresh(t *testing.T) {
	first, err := newStreamListener()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newStreamListener()
	if err != nil {
		t.Fatal(err)
	}
	if first.Port <= 0 || len(first.Username) < 16 || len(first.Password) < 32 || first == second {
		t.Fatalf("listeners = %+v %+v", first, second)
	}
}

func TestAcceptStreamBatchFillsProxiesAndLogsInterruptions(t *testing.T) {
	service := newBareRuntimeForTest(t)
	service.snapshot.Proxies = []core.ProxySnapshot{
		{ID: opaqueID("node-a"), GroupID: opaqueID("one"), Label: "node-a"},
		{ID: opaqueID("node-a"), GroupID: opaqueID("two"), Label: "node-a"},
	}
	at := time.Unix(1000, 0)
	service.acceptBatch(context.Background(), monitor.Batch{
		FinishedAt: at,
		Streams:    []monitor.StreamSample{{Proxy: "node-a", FinishedAt: at, Outcome: monitor.StreamReset}},
		StreamStability: []monitor.StreamStability{{Proxy: "node-a", Samples: 5, Completed: 3, MedianThroughput: 2048000}},
	})
	for _, proxy := range service.snapshot.Proxies {
		if proxy.StreamSamples != 5 || proxy.StreamCompleted != 3 || proxy.StreamKBps != 2000 {
			t.Fatalf("proxy row = %+v", proxy)
		}
	}
	found := false
	for _, entry := range service.snapshot.Diagnostics {
		if entry.Kind == "stream_probe" && entry.SourceID == opaqueID("node-a") && entry.Message == "stream.outcome.reset" {
			found = true
		}
		if strings.Contains(entry.Message, "node-a") {
			t.Fatal("diagnostic leaks the node name")
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", service.snapshot.Diagnostics)
	}
}

func TestSteadyStreamSnapshotHasNoProbeCredentials(t *testing.T) {
	service := newBareRuntimeForTest(t)
	service.streamProbe = mihomo.StreamListener{Port: 1, Username: "probe-user-secret", Password: "probe-password-secret"}
	data := fmt.Sprintf("%+v", service.stateSnapshot())
	if strings.Contains(data, "probe-user-secret") || strings.Contains(data, "probe-password-secret") {
		t.Fatal("snapshot carries probe credentials")
	}
}
```

`newBareRuntimeForTest(t)` is the minimal constructor used by `TestAcceptBatchReportsWindowStabilityForMatchingProxy` in `app/proxy_latency_test.go:77`; extract it there into a named helper and reuse it (DRY), rather than copying its body. Add `"fmt"` to the imports.

Hidden-group test: extend `TestRefreshGroupsKeepsMeasuredLatency` (app/proxy_latency_test.go:21) so the fake `/proxies` JSON also contains a `clashpulse-stream-probe` Selector with members `["node-a"]`, and assert:
- no `s.snapshot.Groups` entry and no `s.snapshot.Proxies` row has `GroupID == opaqueID("clashpulse-stream-probe")`;
- the monitor roster (`s.monitor.ProbedGroups()`) does not contain it;
- `streamMembers` recorded from that group equals `["node-a"]` (expose via the scheduler: call `SetStreamMembers` from refreshGroups; assert through a package-level test hook `s.lastStreamMembers`).

Port conflict test in `app/stream_probe_test.go`, modelled on the busy mixed port test at `app/static_resource_test.go:252`: occupy `service.streamProbe.Port` with `net.Listen` before `startProcess`, assert the error is an `*core.ActivationError` with `Stage == core.ActivationStreamProbePort` and that no Mihomo process remains.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags ci ./app -run 'Stream|RefreshGroupsKeepsMeasuredLatency' -count=1`
Expected: FAIL, `undefined: newStreamListener`.

- [ ] **Step 3: Implement**

`app/stream_probe.go`:

```go
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"

	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
)

// newStreamListener picks a free loopback port and a random credential for
// the probe listener. The port is re-checked before Mihomo starts.
func newStreamListener() (mihomo.StreamListener, error) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return mihomo.StreamListener{}, err
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		return mihomo.StreamListener{}, err
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return mihomo.StreamListener{}, err
	}
	return mihomo.StreamListener{Port: port, Username: hex.EncodeToString(secret[:8]), Password: hex.EncodeToString(secret[8:])}, nil
}

type streamProber struct {
	controller *mihomo.Controller
	listener   mihomo.StreamListener
}

func (p streamProber) StreamPull(ctx context.Context, proxy, url string) monitor.StreamSample {
	result := p.controller.StreamPull(ctx, p.listener, proxy, url)
	return monitor.StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Bytes: result.Bytes,
		FirstByte: result.FirstByte, Duration: result.Duration, Outcome: monitor.StreamOutcome(result.Outcome)}
}
```

(add `"time"` to imports.)

`app/lifecycle.go`:
- `renderWithHome`: when `intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream`, call `newStreamListener()` and set both `s.streamProbe` and the `ControllerSettings.StreamProbe` passed to `mihomo.Render`; otherwise set `s.streamProbe = mihomo.StreamListener{}`. Render failure leaves the prior `s.streamProbe` untouched (assign only after `Render` succeeds).
- `startProcess`: after the existing mixed-port `net.Listen` pre-check, when `s.streamProbe.Port > 0` do the same check and on failure `return core.WrapActivation(core.ActivationStreamProbePort, err)`.
- `refreshGroups`: at the top of the group loop, `if group.Name == mihomo.StreamProbeName { streamMembers = group.Proxies; continue }`. After the loop and once the scheduler exists, `s.monitor.SetStreamMembers(streamMembers)` and set `s.lastStreamMembers = streamMembers`. The Proxies loop that builds `s.snapshot.Proxies` iterates groups, so skipping the group hides its rows too.
- Scheduler creation: `var streamer monitor.StreamProber; if policy.SwitchPolicy == monitor.SwitchSteadyStream { streamer = streamProber{controller: s.controller, listener: s.streamProbe} }` and pass it to `monitor.NewScheduler(policy, s.controller, streamer, ...)`.
- The dial-failure watcher already uses `scheduler.ProbedGroups`, which no longer contains the probe group.
- Carry `StreamSamples`, `StreamCompleted`, `StreamKBps` across rebuilds where `MedianMillis`/`JitterMillis` are carried (lifecycle.go ~988-994).

`app/commands.go` `acceptBatch`:
- Group lookup: replace `decision.Evidence[0].Group` with `decision.Group` and drop the "no Evidence" guard (steady decisions may carry only stream evidence).
- For each `batch.StreamStability` item, update every proxy row whose `ID == opaqueID(item.Proxy)`: `StreamSamples = item.Samples`, `StreamCompleted = item.Completed`, `StreamKBps = item.MedianThroughput / 1024`.
- For each `batch.Streams` sample with `!sample.Completed()`, `s.appendDiagnostic(core.DiagnosticSnapshot{At: sample.FinishedAt.Unix(), Severity: "warning", Kind: "stream_probe", SourceID: opaqueID(sample.Proxy), Message: "stream.outcome." + string(sample.Outcome)})` (use the existing diagnostic helper signature in app/diagnostics.go).
- When building `core.SwitchSnapshot`, map `decision.Streams` into `StreamEvidence` (`ProxyID: opaqueID(p)`, `FinishedAt`, `Outcome: string(sample.Outcome)`, `KBps: sample.Throughput() / 1024`), sorted by `FinishedAt`.

`core/activation.go`: add the stage and message. Catalogs: `zh-CN.toml` `["the stream probe port is unavailable"]` -> `"稳定流探测端口不可用"`; `stream.outcome.*` codes in `en.toml` and `zh-CN.toml`:

| code | en | zh-CN |
|---|---|---|
| `stream.outcome.success` | download completed | 下载完成 |
| `stream.outcome.reset` | download reset | 下载被重置 |
| `stream.outcome.stall` | download stalled | 下载停滞 |
| `stream.outcome.truncated` | download cut short | 下载被截断 |
| `stream.outcome.tls` | TLS failed | TLS 失败 |
| `stream.outcome.timeout` | download timed out | 下载超时 |
| `stream.outcome.error` | download failed | 下载失败 |

The `stream.outcome` prefix must be registered wherever `localize.Code` validates known prefixes (see `localize/localize.go`, mirror `probe.outcome`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags ci ./app -run 'Stream|RefreshGroups|AcceptBatch|Automatic' -count=1 -race && go test ./core ./localize -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w app core
git add app core localize
git commit -m "feat(app): wire steady-stream probing into the runtime"
```

---

### Task 7: GUI and TUI controls and display

**Files:**
- Modify: `ui/monitor_editor.go`, `ui/display.go`, `ui/views.go` (no layout change beyond the dialog)
- Modify: `tui/model.go`, `tui/keys.toml`, `tui/keys.go`
- Modify: `localize/locale/gui.en.toml`, `gui.zh-CN.toml`, `tui.en.toml`, `tui.zh-CN.toml`, `zh-CN.toml`
- Test: `ui/monitor_editor_test.go`, `ui/display_test.go`, `tui/model_test.go`

**Interfaces:**
- Consumes: Task 4 `ipc.ConfigPatch.MonitorStreamURL`, `MonitorStreamIntervalSeconds`, `core.MonitorSnapshot.StreamIntervalSeconds`, `StreamURLConfigured`, `core.ProxySnapshot.StreamSamples/StreamCompleted/StreamKBps`; Task 1 `monitor.SwitchPolicies`
- Produces: `func streamLabel(proxy core.ProxySnapshot) string` in `core/snapshot.go` (shared by GUI and TUI): `""` when `StreamSamples == 0`, else `fmt.Sprintf(localize.T("%d/%d downloads"), StreamCompleted, StreamSamples)` plus `" · " + fmt.Sprintf(localize.T("%d KB/s"), StreamKBps)` when `StreamKBps > 0`. Name it `ProxySnapshot.StreamLabel()` (method, next to `StabilityLabel`).

- [ ] **Step 1: Write the failing tests**

`ui/monitor_editor_test.go`: change `TestMonitorSwitchPolicySelectRoundTrips` to expect `len(selects.Options) == len(monitor.SwitchPolicies)` and round-trip each policy label. Add:

```go
func TestMonitorPolicyPatchCarriesStreamSettings(t *testing.T) {
	input := stabilityPolicyInput()
	input.SwitchPolicy = switchPolicyLabel(monitor.SwitchSteadyStream)
	input.StreamURL = "https://speed.example.com/file"
	input.StreamIntervalSeconds = "600"
	patch, err := monitorPolicyPatch(input)
	if err != nil {
		t.Fatal(err)
	}
	if *patch.MonitorSwitchPolicy != monitor.SwitchSteadyStream || *patch.MonitorStreamURL != input.StreamURL || *patch.MonitorStreamIntervalSeconds != 600 {
		t.Fatalf("patch = %+v", patch)
	}
	input.StreamURL = "http://speed.example.com/file"
	if _, err := monitorPolicyPatch(input); err == nil {
		t.Fatal("HTTP stream URL accepted")
	}
	input.StreamURL = "https://speed.example.com/file"
	input.StreamIntervalSeconds = "60"
	if _, err := monitorPolicyPatch(input); err == nil {
		t.Fatal("1 minute stream interval accepted")
	}
}
```

`ui/display_test.go`, next to the existing "120 ms · median ..." case:

```go
	snapshot.Proxies[0].StreamSamples, snapshot.Proxies[0].StreamCompleted, snapshot.Proxies[0].StreamKBps = 5, 4, 1800
	if got := proxyLatency(snapshot, groupID, proxyID); !strings.HasSuffix(got, " · 4/5 downloads · 1800 KB/s") {
		t.Fatalf("latency cell = %q", got)
	}
```

`tui/model_test.go`:

```go
func TestSettingsCycleSwitchPolicySendsNextPolicy(t *testing.T) {
	model := settingsModel(t) // existing helper used by the settings-row tests (~l.659); reuse its name
	model.snapshot.Snapshot.Monitor.SwitchPolicy = monitor.SwitchLowestLatency
	_, command, _ := model.HandleKey("y")
	if command == nil || command.Config == nil || *command.Config.MonitorSwitchPolicy != monitor.SwitchFailover {
		t.Fatalf("command = %+v", command)
	}
}

func TestStreamSettingsEditValidates(t *testing.T) {
	model := settingsModel(t)
	model, _, _ = model.HandleKey("I")
	model = typeFormText(t, model, "60")
	if _, command, _ := model.HandleKey("ctrl+s"); command != nil {
		t.Fatal("60 second stream interval accepted")
	}
}
```

and extend the proxy-stability render test (~l.854) to set `StreamSamples: 5, StreamCompleted: 4, StreamKBps: 1800` and assert the row contains `4/5 downloads`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags ci ./ui ./tui -run 'SwitchPolicy|Stream|ProxyStability|Latency' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

GUI:
- `ui/monitor_editor.go`: `var switchPolicyIDs = monitor.SwitchPolicies` (delete the local list). `monitorPolicyInput` gains `StreamURL, StreamIntervalSeconds string`. In `monitorPolicyPatch`, validate `monitor.ValidStreamURL(input.StreamURL)` (error `localize.T("stream URL must use HTTPS without credentials or fragment")`) and parse the interval with the existing `parse` helper bounds `240..86400` and label `"Stream interval (seconds)"`; set both patch fields. In `editMonitorPolicy`, add entries `add(localize.T("Stream URL"), "")` with placeholder `localize.T("Leave blank to keep the current stream URL")` (the URL is not in the snapshot; blank keeps it: only set `MonitorStreamURL` when non-blank) and `add(localize.T("Stream interval (seconds)"), strconv.FormatInt(state.StreamIntervalSeconds, 10))`.
- `ui/display.go` `proxyLatency`: after the stability part, `if stream := proxy.StreamLabel(); stream != "" { parts = append(parts, stream) }`.
- Catalog `gui.en.toml`/`gui.zh-CN.toml`:
  - `gui.monitor.switch-policy.lowest_latency`: en `Fastest (always keep the lowest-latency node)`, zh `最快（始终使用延迟最低的节点）`
  - `gui.monitor.switch-policy.steady_stream`: en `Steady stream (leave nodes whose downloads break)`, zh `稳定流（离开下载会中断的节点）`
  - zh entries for `Stream URL` -> `流测试 URL`, `Stream interval (seconds)` -> `流探测间隔（秒）`, `Leave blank to keep the current stream URL` -> `留空则保留当前流测试 URL`, `stream URL must use HTTPS without credentials or fragment` -> `流测试 URL 必须使用 HTTPS，且不含凭据或片段`.

TUI:
- `tui/keys.toml` `[schemes.default.settings]`: `"y" = { fun = "cycle_switch_policy", desc = "cycle switch policy", show = true }`, `"O" = { fun = "edit_stream_url", desc = "set stream test URL", show = true }`, `"I" = { fun = "edit_stream_interval", desc = "set stream interval in seconds", show = true }`; add the three names to the allowlist in `tui/keys.go`.
- `tui/model.go`: `monitorField` consts `monitorStreamURL = "stream_url"`, `monitorStreamInterval = "stream_interval"`. Settings rows after the test URL row: `{"setting:monitor:switch-policy", localize.T("Switch policy"), localize.Code("monitor.policy", monitor.SwitchPolicy), "cycle_switch_policy", ""}`, `{"setting:monitor:stream-url", localize.T("Stream URL"), configured/not configured from StreamURLConfigured, "edit_stream_url", monitorStreamURL}`, `{"setting:monitor:stream-interval", localize.T("Stream interval"), strconv.FormatInt(StreamIntervalSeconds, 10), "edit_stream_interval", monitorStreamInterval}`. Dispatch `cycle_switch_policy` to the next entry of `monitor.SwitchPolicies` after the current one (wrap) via `configCommand(&ipc.ConfigPatch{MonitorSwitchPolicy: &next})`. Add `stream_url` (Text, Sensitive) and `stream_interval` (Text) to the monitor form; in `monitorSettingPatch`, `stream_url` uses `monitor.ValidStreamURL` (notice `localize.T("Stream URL must use HTTPS without credentials or fragment.")`), `stream_interval` uses `monitorRange` with `case monitorStreamInterval: return 240, 86400, true`; `monitorFormIntent` maps them to `MonitorStreamURL` / `MonitorStreamIntervalSeconds`.
- Proxies rows: `if stream := proxy.StreamLabel(); stream != "" { info.latency = appendStability(info.latency, stream) }` after the stability label.
- Catalogs `tui.en.toml`/`tui.zh-CN.toml`: `tui.monitor.range.stream_interval` en `Stream interval must be between %d and %d seconds.`, zh `流探测间隔必须在 %d 到 %d 秒之间。`; zh `Stream URL` -> `流测试 URL`, `Stream interval` -> `流探测间隔`, the stream URL notice -> `流测试 URL 必须使用 HTTPS，且不含凭据或片段。`.
- `zh-CN.toml` (shared): `monitor.policy.lowest_latency` 最快, `monitor.policy.failover` 故障切换, `monitor.policy.steady_stream` 稳定流 (and `en.toml` Fastest / Failover / Steady stream); `%d/%d downloads` -> `%d/%d 次下载完成`, `%d KB/s` -> `%d KB/s`. Register the `monitor.policy` prefix like `probe.outcome`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags ci ./ui ./tui ./localize -count=1 && LANGUAGE=zh_CN go test -tags ci ./ui ./tui -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w ui tui core
git add ui tui core localize
git commit -m "feat(ui): choose steady stream and show download completion per node"
```

---

### Task 8: README and real-Mihomo interruption check

**Files:**
- Modify: `README.md`
- Create (throwaway, not committed): `/tmp/steady-e2e/` script and two local proxies

**Interfaces:**
- Consumes: the built binary from Tasks 1-7.

- [ ] **Step 1: README**

Add a section `### Choosing a node automatically` after the Settings paragraph (README line ~64), with the exact text from the spec's "README text" section.

- [ ] **Step 2: Full verification**

Run:

```bash
gofmt -l app cmd config core download ipc localize mihomo monitor subscriptions tui ui tun sysproxy
go vet -tags ci ./... && GOOS=windows go vet -tags ci ./app ./ipc ./cmd/clashpulse ./ui ./tui ./monitor ./mihomo && GOOS=darwin go vet -tags ci ./app ./ipc ./monitor ./mihomo ./config
go test -tags ci -count=1 ./...
go test -race -tags ci -count=1 ./app ./monitor ./mihomo ./ipc
LANGUAGE=zh_CN go test -tags ci -count=1 ./ui ./tui ./cmd/clashpulse
git diff --check
go build -tags wayland -o clashpulse ./cmd/clashpulse
```

Expected: no gofmt output, all `ok`, `BUILT`.

- [ ] **Step 3: Real Mihomo, real HTTPS pulls, injected interruption**

Write `/tmp/steady-e2e/proxy.py`: an HTTP CONNECT proxy on `127.0.0.1:<port>` taking `--cut-after BYTES` (0 = never). It relays the tunnel and, when `--cut-after` is set, closes both sockets with `SO_LINGER 0` after relaying that many server-to-client bytes. Start two: `clean` (no cut) and `flaky` (`--cut-after 65536`).

Write a private XDG config dir with:
- `config.toml`: `[mihomo] binary = "system"`, `[monitor] switch_policy = "steady_stream"`, `stream_interval = "4m"`, `interval = "10s"`, `jitter = "0s"`, `cooldown = "0s"`, `automated_groups` set to the group ID shown by the TUI/IPC for `main` (read it from `clashpulse tui` or IPC snapshot after first start).
- A local profile with `proxies: [{name: clean-node, type: http, server: 127.0.0.1, port: <clean>}, {name: flaky-node, type: http, server: 127.0.0.1, port: <flaky>}]`, `proxy-groups: [{name: main, type: select, proxies: [flaky-node, clean-node]}]`.

Run `./clashpulse activate <profile.yaml>` in the background with the private `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_RUNTIME_DIR`. Use the real 4 minute floor; do not shorten it with build flags. Rounds run at start, +4 m, and +8 m, so check after about 9 minutes.

Expected:
- the flaky proxy log shows connections cut after 65536 bytes; the clean proxy log shows complete 262144-byte relays to `speed.cloudflare.com`;
- the activity log (`clashpulse tui`, `~`) shows `download reset` entries for the flaky node;
- after the selected `flaky-node` has 2 interrupted pulls and `clean-node` has 3 completed, the group switches to `clean-node`, and the last-switch row shows reason `steadier HTTPS downloads` with stream evidence;
- `ss -ltnp` shows the probe listener only on `127.0.0.1`;
- `curl -x http://127.0.0.1:<probe-port> https://speed.cloudflare.com/__down?bytes=1024` without credentials fails.

Record the observed output in the final report. Delete `/tmp/steady-e2e` afterwards.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: explain fastest and steady-stream switching"
```
