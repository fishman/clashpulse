# ClashPulse Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver ClashPulse as a cross-platform Go Mihomo client with safe subscriptions/resources, conservative automatic switching, local IPC, Fyne, and a notmutt-inspired terminal client.

**Architecture:** `app` is the only lifecycle owner. Strict TOML enters through `config.Store`, workers receive immutable snapshots, and app applies validated plans transactionally. Mihomo remains an external selected binary; Fyne and TUI are versioned local-IPC clients.

**Tech Stack:** Go 1.26.6, standard library HTTP/process APIs, released `github.com/fishman/notmutt/lib/xdg`, released `github.com/fishman/notmutt/lib/localipc`, strict TOML decoder, Fyne v2, `tcell` plus `lipgloss`.

**Spec:** `docs/superpowers/specs/2026-09-22-clashpulse-design.md`

## Global Constraints

- Pin released exact versions of `github.com/fishman/notmutt/lib/xdg` and `github.com/fishman/notmutt/lib/localipc`; never commit `replace` or `go.work` wiring.
- Mihomo is an external child binary. Never import/fork/embed it.
- System Mihomo is the Arch Linux default; bundled is explicit fallback. Inspect capability before config generation/start.
- All user intent is strict TOML split across `config.toml`, `subscriptions.toml`, `resources.toml`, and `filters.toml`.
- UI/TUI threads never perform controller/network/filesystem/process/scheduler work. Every I/O operation is cancellable.
- No polling while idle, busy loops, per-item goroutines, or redraw ticker.
- IPC is Unix socket in a 0700 directory or Windows named pipe; never TCP.
- Subscription URLs, credentials, controller secret, proxy credentials, and profile body never enter logs, IPC snapshots, or user-visible errors.
- First release controls HTTP/HTTPS System Proxy only. TUN, DNS redirection, service managers, core auto-download, profile editors, and subscription pools are out of scope.
- The user authorized `references/notmutt` tag `lib/tui/v0.1.1`; publish it only after Notmutt tests, then pin module `github.com/fishman/notmutt/lib/tui v0.1.1` exactly in ClashPulse. No local `replace` or copied shared code.
- IPC protocol 3 carries only safe subscription policy fields and a bounded 200-entry sanitized diagnostic ring; full URLs, custom User-Agent, credentials, raw HTTP bodies, and raw Mihomo logs stay private.
- The bottommost TUI row remains the status bar; configuration form tables use declarative keys and do no I/O. The GUI keeps `widget.List` virtualization and has no visible pipe-delimited summaries or data rows.

---

## File structure

```text
go.mod
cmd/clashpulse/main.go
app/app.go
app/app_test.go
config/{types,load,store,watch,write}_*.go
core/{snapshot,event,error}.go
mihomo/{binary,process,controller,render}_*.go
subscriptions/{model,fetch,store,scheduler}_*.go
resources/{model,fetch,render}_*.go
filters/render.go
dns/{model,render,validate}.go
monitor/{model,scheduler,policy}_*.go
sysproxy/{sysproxy_linux,sysproxy_darwin,sysproxy_windows}.go
ipc/{protocol,server,client}_*.go
tui/{app,model,render,keys}.go
ui/{client,window,views}.go
```

`*_test.go` files use `httptest`, temporary directories, fake Mihomo executables, or local Unix sockets. They never use live subscriptions, root, an actual proxy, a real DNSCrypt server, or user state.

## Task 1: Bootstrap the module and shared dependencies

**Files:**
- Create: `go.mod`
- Create: `cmd/clashpulse/main.go`
- Create: `core/snapshot.go`
- Create: `core/snapshot_test.go`

**Interfaces:**
- Produces `core.Snapshot`, the immutable state delivered to IPC/Fyne/TUI.
- Produces `clashpulse version` as a side-effect-free command.

- [ ] **Step 1: Publish shared module versions before adding dependencies**

Create and push tags from notmutt:

```text
lib/xdg/v0.1.0
lib/localipc/v0.1.0
```

Verify from a temporary module:

```sh
go mod init example.test/clashpulse-deps
go get github.com/fishman/notmutt/lib/xdg@v0.1.0
go get github.com/fishman/notmutt/lib/localipc@v0.1.0
go list -m all
```

Expected: both modules resolve by version without `replace`.

- [ ] **Step 2: Write the failing immutable-snapshot test**

```go
func TestSnapshotCopiesSlices(t *testing.T) {
    groups := []GroupSnapshot{{ID: "auto", Selected: "alpha"}}
    snapshot := NewSnapshot(groups)
    groups[0].Selected = "beta"
    if snapshot.Groups[0].Selected != "alpha" {
        t.Fatal("snapshot retained caller-owned memory")
    }
}
```

Run: `go test ./core -run '^TestSnapshotCopiesSlices$'`

Expected: FAIL because `Snapshot`, `GroupSnapshot`, and `NewSnapshot` do not exist.

- [ ] **Step 3: Add the minimal module and snapshot implementation**

Use this module contract:

```go
module github.com/fishman/clashpulse

require (
    github.com/fishman/notmutt/lib/localipc v0.1.0
    github.com/fishman/notmutt/lib/xdg v0.1.0
)
```

```go
type GroupSnapshot struct {
    ID       string
    Selected string
}

type Snapshot struct {
    Revision uint64
    Groups   []GroupSnapshot
}

func NewSnapshot(groups []GroupSnapshot) Snapshot {
    return Snapshot{Groups: append([]GroupSnapshot(nil), groups...)}
}
```

`cmd/clashpulse/main.go` recognizes only `version` and otherwise invokes `app.Run(context.Background())`; `app.Run` initially returns a descriptive `not initialized` error.

- [ ] **Step 4: Run the narrow test and format**

Run:

```sh
gofmt -w cmd/clashpulse/main.go core/snapshot.go core/snapshot_test.go
go test ./core -run '^TestSnapshotCopiesSlices$'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add go.mod go.sum cmd/clashpulse/main.go core/snapshot.go core/snapshot_test.go
git commit -m "feat(core): add clashpulse module foundation"
```

## Task 2: Strict readable TOML and transactional configuration store

**Files:**
- Create: `config/types.go`, `config/load.go`, `config/store.go`, `config/write.go`, `config/watch.go`
- Create: `config/load_test.go`, `config/store_test.go`, `config/watch_test.go`

**Interfaces:**
- Consumes `core.Snapshot` error fields later.
- Produces `config.Snapshot`, `config.Store`, `config.Change`, and `config.Load(dir string)`.

```go
type Snapshot struct {
    App           App
    Mihomo        Mihomo
    Monitor       Monitor
    DNS           DNS
    Subscriptions []Subscription
    Resources     []Resource
    Filters       []Filter
}

type Store struct{}
func NewStore(Snapshot) *Store
func (s *Store) Snapshot() Snapshot
func (s *Store) Subscribe(section string, fn func(Change)) func()
func (s *Store) Replace(Snapshot) Change
func Load(dir string) (Snapshot, error)
```

- [ ] **Step 1: Write failing strict-load tests**

```go
func TestLoadRejectsUnknownKeyWithFile(t *testing.T) {
    dir := t.TempDir()
    writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\nbogus = true\n")
    _, err := Load(dir)
    if err == nil || !strings.Contains(err.Error(), "config.toml") || !strings.Contains(err.Error(), "bogus") {
        t.Fatalf("error = %v", err)
    }
}

func TestLoadRejectsDanglingFilterResource(t *testing.T) {
    dir := t.TempDir()
    writeFile(t, dir, "filters.toml", "[[filter]]\nid = \"ads\"\nresource = \"missing\"\n")
    _, err := Load(dir)
    if err == nil || !strings.Contains(err.Error(), "missing") {
        t.Fatalf("error = %v", err)
    }
}
```

Run: `go test ./config -run 'TestLoadRejects(UnknownKeyWithFile|DanglingFilterResource)$'`

Expected: FAIL because `Load` does not exist.

- [ ] **Step 2: Implement four explicit file schemas**

Decode exactly these files and no glob overlays:

```text
config.toml          [mihomo], [monitor], [dns], [system_proxy]
subscriptions.toml   [[subscription]]
resources.toml       [[resource]], [[resolver_set]], [[dns_route]]
filters.toml         [[filter]]
```

Use one top-level struct per file. Reject undecoded TOML keys. Validate stable IDs globally, all references, URL schemes, and enums. Keep observed hashes, ETags, probe history, and active selections out of TOML.

- [ ] **Step 3: Add atomic replacement and section-diff tests**

```go
func TestReplaceNotifiesOnlyChangedSection(t *testing.T) {
    store := NewStore(defaultSnapshot())
    changed := make(chan Change, 1)
    store.Subscribe("monitor", func(change Change) { changed <- change })
    next := store.Snapshot()
    next.Monitor.Interval = 10 * time.Minute
    store.Replace(next)
    if change := <-changed; change.Section != "monitor" {
        t.Fatalf("section = %q", change.Section)
    }
}
```

- [ ] **Step 4: Implement atomic writer and directory watcher**

Write TOML into a 0600 temporary file in the target directory, `fsync`, then rename. Watch the directory with one audited cross-platform filesystem watcher dependency. Debounce events; run complete `Load` in a cancellable worker; replace only valid snapshots. A no-op snapshot sends no callbacks.

- [ ] **Step 5: Verify autoreload behavior**

Run:

```sh
go test ./config -run 'Test(Load|Replace|Watch)'
```

Expected: PASS for atomic rename/create/delete, invalid-edit retention, and self-write no-op tests.

- [ ] **Step 6: Commit**

```sh
git add go.mod go.sum config
git commit -m "feat(config): add strict reloadable toml store"
```

## Task 3: Mihomo binary selection, rendering, and lifecycle

**Files:**
- Create: `mihomo/binary.go`, `mihomo/render.go`, `mihomo/process.go`, `mihomo/controller.go`
- Create: `mihomo/binary_test.go`, `mihomo/process_test.go`, `mihomo/render_test.go`

**Interfaces:**

```go
type Selection struct { Kind string; Path string }
type Capability struct {
    Version string
    SupportsGeoIPDat bool
    SupportsGeoSiteDat bool
    ValidateArgs []string
}
type Manager interface {
    Inspect(context.Context, Selection) (Capability, error)
    Validate(context.Context, Capability, string) error
    Start(context.Context, StartPlan) error
    Stop(context.Context) error
    Delay(context.Context, string, string, time.Duration) (time.Duration, error)
    Select(context.Context, string, string) error
}
```

- [ ] **Step 1: Write failing fake-binary tests**

```go
func TestSystemSelectionWinsOnLinux(t *testing.T) {
    path := fakeMihomo(t, fakeOptions{Version: "v1.19.31"})
    t.Setenv("PATH", filepath.Dir(path))
    got, err := Resolve(Selection{Kind: "system"})
    if err != nil || got != path { t.Fatalf("got %q, err %v", got, err) }
}

func TestSwitchRejectsUnsupportedCandidateWithoutStoppingCurrent(t *testing.T) {
    current := fakeMihomo(t, fakeOptions{Version: "v1.19.31", Validate: true})
    candidate := fakeMihomo(t, fakeOptions{Version: "v1.18.0", Validate: false})
    manager := newManagerForTest(t, current)
    err := manager.Switch(context.Background(), Selection{Kind: "path", Path: candidate})
    if err == nil { t.Fatal("Switch accepted an invalid candidate") }
    if manager.StopCount() != 0 { t.Fatal("Switch stopped current Mihomo") }
}
```

Run: `go test ./mihomo -run 'Test(SystemSelectionWinsOnLinux|SwitchRejectsUnsupportedCandidateWithoutStoppingCurrent)$'`

Expected: FAIL before `Resolve` and transactional switch exist.

- [ ] **Step 2: Implement selection and capability inspection**

Resolve `system` through `exec.LookPath`; validate explicit files with `os.Stat` and executable mode. Generate random loopback controller address/secret. Run the selected executable with its capability-defined validation args. Keep desired selection and observed capability separate.

- [ ] **Step 3: Implement deterministic generated YAML**

`Render(config.Snapshot, resources.Plan, dns.Plan, Capability)` returns bytes and named compatibility errors. It renders only capability-supported fields, controller loopback address/secret, deterministic managed paths, and never source-profile mutation.

- [ ] **Step 4: Implement child/controller lifecycle**

Start with `exec.CommandContext`, wait for authenticated local controller readiness, and remove PID/runtime markers on every failed path. `Stop` clears project-owned System Proxy through the later app callback before process exit. Controller methods use typed local HTTP requests; raw controller types do not escape.

- [ ] **Step 5: Verify**

Run:

```sh
go test ./mihomo
go vet ./mihomo
```

Expected: PASS; fake binaries prove no stale child, system precedence, validation rejection, and capability-gated rendering.

- [ ] **Step 6: Commit**

```sh
git add mihomo
git commit -m "feat(mihomo): add selected binary lifecycle"
```

## Task 4: Private subscriptions and timer-owned refresh

**Files:**
- Create: `subscriptions/model.go`, `subscriptions/store.go`, `subscriptions/fetch.go`, `subscriptions/scheduler.go`
- Create: `subscriptions/fetch_test.go`, `subscriptions/scheduler_test.go`

**Interfaces:**

```go
type Result struct {
    ID string
    Changed bool
    CheckedAt time.Time
    Hash string
    Usage *Usage
}
type Service interface {
    Refresh(context.Context, string) (Result, error)
    Activate(context.Context, string) error
    NextDue() time.Time
}
```

- [ ] **Step 1: Write failing conditional-refresh tests**

```go
func TestRefresh304UpdatesCheckWithoutPromotion(t *testing.T) {
    // httptest verifies If-None-Match; server replies 304.
    // Assert snapshot bytes, generated config mtime, and reload count stay unchanged.
}

func TestInvalidProfileRetainsKnownGoodSnapshot(t *testing.T) {
    // Seed valid snapshot, return YAML without proxies/proxy-providers.
    // Assert active snapshot bytes remain unchanged.
}
```

Run: `go test ./subscriptions -run 'TestRefresh(304UpdatesCheckWithoutPromotion|InvalidProfileRetainsKnownGoodSnapshot)$'`

Expected: FAIL before the service exists.

- [ ] **Step 2: Implement private state and bounded fetch**

State directory is 0700; metadata/snapshots are 0600. The shared HTTP client has redirect cap, response cap, per-host limits, context cancellation, and explicit route: direct, system proxy, or running Mihomo proxy. HTTPS is default; HTTP, invalid TLS, URL userinfo, and unsafe redirects require per-subscription intent.

- [ ] **Step 3: Implement validation/promotion/activation separation**

Fetch temp file, parse YAML, require `proxies` or `proxy-providers`, parse optional headers, compare hash, render/validate complete config, then atomically promote. Refresh never activates. Activation chooses an already validated snapshot and invokes app reload only when generated bytes changed.

- [ ] **Step 4: Implement one scheduler**

Use one timer for nearest due subscription, bounded jitter, and a fixed worker pool. Cancellation stops timer and workers. No per-subscription ticker/goroutine.

- [ ] **Step 5: Verify**

Run: `go test ./subscriptions`

Expected: PASS for redirects, cap, TLS policy, 304/hash suppression, failed-update retention, activation, and scheduler cancellation.

- [ ] **Step 6: Commit**

```sh
git add subscriptions
git commit -m "feat(subscriptions): add transactional refresh"
```

## Task 5: Resource registry, filters, and DNS policy

**Files:**
- Create: `resources/model.go`, `resources/fetch.go`, `resources/render.go`
- Create: `filters/render.go`
- Create: `dns/model.go`, `dns/validate.go`, `dns/render.go`
- Create: `resources/fetch_test.go`, `dns/render_test.go`

**Interfaces:**

```go
type ResourcePlan struct { Paths map[string]string; Changed bool }
func Resolve(config.Snapshot, stateDir string) (ResourcePlan, error)
func Render(config.Snapshot, ResourcePlan) ([]byte, error)
func ValidateDNS(config.DNS, ResourcePlan) error
```

- [ ] **Step 1: Write failing resource/DNS tests**

```go
func TestPinnedResourceMismatchRetainsPreviousFile(t *testing.T) {
    path := writeResource(t, "geoip.dat", []byte("known-good"))
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("bad")) }))
    err := Refresh(context.Background(), pinnedGeoIP(server.URL, "00"), path)
    if !errors.Is(err, ErrPinMismatch) { t.Fatalf("error = %v", err) }
    if got := string(readFile(t, path)); got != "known-good" { t.Fatalf("file = %q", got) }
}
func TestChinaRouteRendersManagedRuleSet(t *testing.T) {
    got, err := dns.Render(config.DNS{Routes: []config.DNSRoute{{Resource: "cn", ResolverSet: "domestic"}}}, ResourcePlan{Paths: map[string]string{"cn": "/state/cn.srs"}})
    if err != nil || !bytes.Contains(got, []byte("rule-set:cn")) { t.Fatalf("render = %s, err = %v", got, err) }
}
func TestDNSCryptLoopIsRejected(t *testing.T) {
    err := ValidateDNS(config.DNS{Listen: "127.0.0.1:5353", ResolverSets: []config.ResolverSet{{ID: "crypt", Endpoints: []string{"127.0.0.1:5353"}}}}, ResourcePlan{})
    if err == nil { t.Fatal("accepted DNS listener loop") }
}
```

Run: `go test ./resources ./dns -run 'Test(PinnedResourceMismatchRetainsPreviousFile|ChinaRouteRendersManagedRuleSet|DNSCryptLoopIsRejected)$'`

Expected: FAIL before registry/rendering exists.

- [ ] **Step 2: Implement one resource registry**

Support `geoip.dat`, `geosite.dat`, `Country.mmdb`, Mihomo rule-provider formats, and managed domain/IP rule sets. Validate ID/kind/format, destination names, no symlink/path traversal, size, pin, and conditional fetch metadata. Resource bytes use the subscription downloader semantics and state permissions.

- [ ] **Step 3: Render filter and DNS references**

Filters render deterministic rule-provider names from registry IDs. Resolver sets are named endpoints. DNS routes refer only to suffix, GeoSite, or resource ID; renderer emits `nameserver-policy`. `rule-set:cn` is an explicit opt-in resource, not magic download behavior.

- [ ] **Step 4: Add DNSCrypt external endpoint validation**

Accept only explicitly configured loopback `dnscrypt-proxy` UDP/TCP endpoints. Check endpoint reachability before plan promotion, reject self-listener loops, and retain old config on failure. Do not launch/configure DNSCrypt.

- [ ] **Step 5: Verify and commit**

Run:

```sh
go test ./resources ./filters ./dns
git add resources filters dns
git commit -m "feat(resources): add managed data and dns routes"
```

## Task 6: Monitor policy and conservative switching

**Files:**
- Create: `monitor/model.go`, `monitor/policy.go`, `monitor/scheduler.go`
- Create: `monitor/policy_test.go`, `monitor/scheduler_test.go`

**Interfaces:**

```go
type Sample struct { Group, Proxy, URL string; FinishedAt time.Time; Latency time.Duration; Outcome Outcome }
type Decision struct { Switch bool; Old, New, Reason string; Evidence []Sample }
func Decide(Policy, GroupState, []Sample, time.Time) Decision
```

- [ ] **Step 1: Write failing behavior tests**

```go
func TestDecideDoesNotSwitchOnOneSlowSample(t *testing.T) {
    decision := Decide(testPolicy(), selected("auto", "alpha"), []Sample{slow("auto", "alpha")}, time.Now())
    if decision.Switch { t.Fatal("one slow sample switched proxy") }
}
func TestDecideRequiresMaterialMedianImprovement(t *testing.T) {
    decision := Decide(testPolicy(), selected("auto", "alpha"), samples("auto", "alpha", 500, "beta", 475), time.Now())
    if decision.Switch { t.Fatal("insufficient improvement switched proxy") }
}
func TestDecideStopsWhenAllCandidatesFail(t *testing.T) {
    decision := Decide(testPolicy(), selected("auto", "alpha"), failedSamples("auto", "alpha", "beta"), time.Now())
    if decision.Switch || decision.Reason != "all candidates failed" { t.Fatalf("decision = %+v", decision) }
}
```

Run: `go test ./monitor -run '^TestDecide'`

Expected: FAIL because `Decide` does not exist.

- [ ] **Step 2: Implement pure decision policy**

Treat zero, timeout, and controller errors as failures. Require current selection unhealthy/exceeding threshold for configured consecutive samples and a materially better eligible median. Enforce per-group in-flight switch, cooldown, all-failed circuit breaker, and manual override disables automation.

- [ ] **Step 3: Implement deadline scheduler**

Schedule each opted-in group by next deadline; wake only on timer, manual request, controller event, or context cancellation. Probe with bounded workers/jitter through `mihomo.Manager.Delay`; emit one batch snapshot/decision.

- [ ] **Step 4: Verify and commit**

```sh
go test ./monitor
git add monitor
git commit -m "feat(monitor): add conservative proxy switching"
```

## Task 7: App orchestration and System Proxy transaction

**Files:**
- Create: `app/app.go`, `app/plan.go`, `app/app_test.go`
- Create: `sysproxy/sysproxy.go`, `sysproxy/sysproxy_linux.go`, `sysproxy/sysproxy_darwin.go`, `sysproxy/sysproxy_windows.go`
- Create: `sysproxy/sysproxy_test.go`

**Interfaces:**

```go
type Intent interface{ apply(context.Context, *App) error }
func (a *App) Enqueue(Intent) error
func (a *App) Apply(config.Change) error
```

- [ ] **Step 1: Write failing transaction tests**

```go
func TestApplyKeepsRunningMihomoWhenCandidateValidationFails(t *testing.T) {
    manager := &fakeManager{running: true, validateErr: errors.New("bad config")}
    err := newAppForTest(manager, &fakeProxy{}).Apply(testChange())
    if err == nil || !manager.running { t.Fatalf("err = %v, running = %t", err, manager.running) }
}
func TestSystemProxyAppliesOnlyAfterControllerReady(t *testing.T) {
    events := []string{}
    app := newAppForTest(&fakeManager{events: &events}, &fakeProxy{events: &events})
    if err := app.Apply(testChange()); err != nil { t.Fatal(err) }
    if strings.Join(events, ",") != "validate,start,ready,proxy" { t.Fatalf("events = %v", events) }
}
func TestFailedStartRestoresProjectSystemProxyState(t *testing.T) {
    proxy := &fakeProxy{prior: ProxyState{Host: "old", Port: 8080}}
    err := newAppForTest(&fakeManager{startErr: errors.New("start")}, proxy).Apply(testChange())
    if err == nil || !proxy.restored { t.Fatalf("err = %v, restored = %t", err, proxy.restored) }
}
```

Run: `go test ./app ./sysproxy -run 'Test(ApplyKeepsRunningMihomoWhenCandidateValidationFails|SystemProxyAppliesOnlyAfterControllerReady|FailedStartRestoresProjectSystemProxyState)$'`

Expected: FAIL before orchestration exists.

- [ ] **Step 2: Implement dependency-ordered plans**

`App.Apply` applies passive client settings, monitor/schedulers, resources/DNS, generated config, Mihomo lifecycle, then System Proxy. Candidate render/validate completes before current runtime stop. Unchanged plan performs no reload/redraw.

- [ ] **Step 3: Implement System Proxy adapters**

Adapters expose `Capture`, `ApplyHTTPHTTPS(host, port)`, and `Restore`. Linux/macOS/Windows adapters compile behind build tags. No TUN, PAC, service-manager, or privilege escalation.

- [ ] **Step 4: Verify and commit**

```sh
go test ./app ./sysproxy
git add app sysproxy
git commit -m "feat(app): add transactional lifecycle orchestration"
```

## Task 8: Versioned local IPC

**Files:**
- Create: `ipc/protocol.go`, `ipc/server.go`, `ipc/client.go`
- Create: `ipc/protocol_test.go`, `ipc/server_test.go`

**Interfaces:**

```go
const Version = 1
type Hello struct { Version uint16 }
type Command struct { ID string; Kind string; Payload json.RawMessage }
type Event struct { Revision uint64; Snapshot core.Snapshot; Progress *Progress; Error *StructuredError }
```

- [ ] **Step 1: Write failing protocol tests**

```go
func TestServerRejectsIncompatibleVersion(t *testing.T) {
    reply := serveOne(t, Hello{Version: Version + 1})
    if reply.Error == nil || reply.Error.Code != "incompatible_version" { t.Fatalf("reply = %+v", reply) }
}
func TestSlowClientDoesNotBlockSnapshotPublish(t *testing.T) {
    server, client := newServerAndUnreadClient(t)
    done := make(chan struct{})
    go func() { server.Publish(testSnapshot()); close(done) }()
    select { case <-done: case <-time.After(time.Second): t.Fatal("Publish blocked") }
    client.Close()
}
func TestCommandAcknowledgesQueuedIntent(t *testing.T) {
    server, client, started := newQueuedServer(t)
    defer server.Close()
    reply := client.Call(Command{ID: "1", Kind: "manual_probe"})
    if !reply.Queued { t.Fatalf("reply = %+v", reply) }
    <-started
}
```

Run: `go test ./ipc -run 'Test(ServerRejectsIncompatibleVersion|SlowClientDoesNotBlockSnapshotPublish|CommandAcknowledgesQueuedIntent)$'`

Expected: FAIL before server/client exist.

- [ ] **Step 2: Implement protocol over localipc**

Use localipc Unix primitives on Unix. Add a project-local Windows named-pipe transport behind build tags after reviewing an exact dependency/version/license. Handshake is first frame. Handlers validate typed payload, enqueue app intent, and acknowledge queueing. Events are coalesced per client and omit secrets/full URLs/content.

- [ ] **Step 3: Implement commands and snapshots**

Commands cover lifecycle, config change, manual probe, selection, automation, subscription/resource changes, activation, and System Proxy setting. Expose desired/observed Mihomo identity/capability failure and resource/subscription host/status metadata only.

- [ ] **Step 4: Verify and commit**

```sh
go test ./ipc
git add ipc
git commit -m "feat(ipc): add local versioned control protocol"
```

## Task 9: Terminal client

**Files:**
- Create: `tui/app.go`, `tui/model.go`, `tui/render.go`, `tui/keys.go`
- Create: `tui/model_test.go`, `tui/keys_test.go`
- Modify: `cmd/clashpulse/main.go`

**Interfaces:**

```go
type Model struct { Snapshot core.Snapshot; Focus Focus; Selection map[string]string; Pending map[string]bool }
func (m Model) Apply(ipc.Event) Model
func (m Model) HandleKey(tcell.EventKey) (Model, *ipc.Command)
```

- [ ] **Step 1: Write failing state-preservation tests**

```go
func TestApplyKeepsProxyCursorAcrossSubscriptionRefresh(t *testing.T) {
    model := Model{Focus: FocusProxies, Selection: map[string]string{"proxy": "beta"}}
    next := model.Apply(ipc.Event{Snapshot: core.Snapshot{Revision: 2}})
    if next.Selection["proxy"] != "beta" { t.Fatalf("selection = %q", next.Selection["proxy"]) }
}
func TestProgressDoesNotStealFocus(t *testing.T) {
    model := Model{Focus: FocusProxies}.Apply(ipc.Event{Progress: &ipc.Progress{Kind: "subscription"}})
    if model.Focus != FocusProxies { t.Fatalf("focus = %v", model.Focus) }
}
func TestKeyHelpDerivesFromConfiguredBinding(t *testing.T) {
    keys := NewKeys(config.Bindings{"proxies": {"r": "manual_probe"}})
    if got := keys.Help("proxies", "r"); got != "manual_probe" { t.Fatalf("help = %q", got) }
}
```

Run: `go test ./tui -run 'Test(ApplyKeepsProxyCursorAcrossSubscriptionRefresh|ProgressDoesNotStealFocus|KeyHelpDerivesFromConfiguredBinding)$'`

Expected: FAIL before model/key dispatch exists.

- [ ] **Step 2: Implement notmutt-style event UI**

Use `tcell` and `lipgloss` directly. Maintain focus/selection/modal/pending intent locally. Apply stable-ID snapshot diffs; never own lifecycle state or perform I/O synchronously. Render only on IPC input or terminal resize; no refresh ticker.

- [ ] **Step 3: Add `clashpulse tui` client command**

Connect to an existing IPC service; report a clear unavailable-service error. Default key bindings live in typed TOML data and help derives from that map.

- [ ] **Step 4: Verify and commit**

```sh
go test ./tui
git add tui cmd/clashpulse/main.go
git commit -m "feat(tui): add ipc terminal client"
```

## Task 10: Fyne desktop client and release checks

**Files:**
- Create: `ui/client.go`, `ui/window.go`, `ui/views.go`
- Create: `ui/client_test.go`
- Create: `.github/workflows/build.yml`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- `ui.Client` consumes `ipc.Client`; it never imports `app`, `monitor`, or `mihomo`.

- [ ] **Step 1: Write failing non-display client test**

```go
func TestClientQueuesCommandWithoutBlockingSnapshotApply(t *testing.T) {
    transport := &blockingIPCClient{commands: make(chan ipc.Command)}
    client := NewClient(transport)
    done := make(chan struct{})
    go func() { client.Apply(ipc.Event{Snapshot: core.Snapshot{Revision: 1}}); close(done) }()
    select { case <-done: case <-time.After(time.Second): t.Fatal("Apply blocked") }
}
```

Run: `go test ./ui -run '^TestClientQueuesCommandWithoutBlockingSnapshotApply$'`

Expected: FAIL before `ui.Client` exists.

- [ ] **Step 2: Add released Fyne after dependency review**

Select the exact released Fyne v2 version after recording upstream provenance,
BSD 3-Clause license notice, transitive `go mod graph`, Linux/macOS/Windows
native build requirements, and the removal/upgrade owner in `docs/dependencies.md`.
Vendor only after the review; reject a release candidate.

- [ ] **Step 3: Implement one-window IPC UI**

Create Overview, Proxies, Subscriptions, Filter Lists, Data Resources, and Settings. Use `widget.List` virtualization. Apply snapshots with `fyne.Do`; commands enqueue through IPC. Tray actions use the same IPC commands.

- [ ] **Step 4: Add packaging checks**

CI runs targeted Go tests and `go vet ./...` for Linux, macOS, and Windows build-tag surfaces. Packaging jobs remain target-native where Fyne graphics dependencies require it.

- [ ] **Step 5: Verify and commit**

```sh
go test ./ui
go vet ./...
git add ui .github/workflows/build.yml docs/dependencies.md go.mod go.sum
git commit -m "feat(ui): add fyne ipc client"
```

## Task 11: Catppuccin TUI chrome and status segments

**State:** Tasks 1-10 describe the original foundation; this task updates the
already-shipped TUI against the approved `### TUI chrome and tables` spec.

**Files:** Modify `tui/render.go`, `tui/render_test.go`; leave `tui/app.go`,
`core/snapshot.go`, `ipc/`, and the read-only `references/notmutt/` untouched.

**Interfaces:** Consume `Model.Tab`, `Model.Pending`, `Model.Progress()`,
`Model.snapshot.Snapshot.Subscriptions`, `chrome.Tabs`, `chrome.Status`, and
`theme.Resolve`. Produce the existing `render(tcell.Screen, Model, *renderCache)`;
Task 12 consumes its row 0 tabs, row 1 header, rows 2 onward data, and last
three rows notice, key-help, status (status is bottommost).

- [ ] **Step 1: Write renderer regressions before changing code**

In `tui/render_test.go`, use the existing `mockRender` helper and import `core`
and `ipc`:

```go
func TestRenderTabsOccupyFirstRow(t *testing.T) {
    model := NewModel()
    model.Tab = TabResources
    rows := mockRender(t, model, 80, 12)
    if !strings.Contains(rows[0], "Resources") || strings.Contains(rows[0], "ClashPulse") {
        t.Fatalf("top tab row = %q", rows[0])
    }
    if !strings.Contains(rows[1], "Name") { t.Fatalf("table header = %q", rows[1]) }
}
func TestRenderStatusIdentifiesConnectionAndActiveProfile(t *testing.T) {
    model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{
        Subscriptions: []core.SubscriptionSnapshot{{ID: "primary", Name: "Primary", Active: true}},
    }})
    model.Pending = 2
    rows := mockRender(t, model, 80, 12)
    if !strings.Contains(rows[11], "IPC connected") || !strings.Contains(rows[11], "Primary") || !strings.Contains(rows[11], "sending 2") {
        t.Fatalf("status segments = %q", rows[11])
    }
    model = NewModel()
    if row := mockRender(t, model, 80, 12)[11]; !strings.Contains(row, "profile not reported") {
        t.Fatalf("unknown profile was invented: %q", row)
    }
}
func TestRenderCatppuccinMochaPalette(t *testing.T) {
    if styles["normal"].Bg != "#1e1e2e" || styles["normal"].Fg != "#cdd6f4" || styles["tabbar.active"].Bg != "#89b4fa" {
        t.Fatalf("theme = %#v", styles)
    }
}
```

Run: `go test -tags ci ./tui -run '^TestRender(TabsOccupyFirstRow|StatusIdentifiesConnectionAndActiveProfile|CatppuccinMochaPalette)$' -count=1`.
Expect failures on the old title row, absent status segments, and black/cyan
palette. Existing renderer tests with hard-coded row numbers must be updated to
the new geometry, not weakened or removed.

- [ ] **Step 2: Replace chrome styles and remove the title row**

Use `theme.Resolve` once with the spec's Mocha base/text/surface/muted/blue/
green/yellow/red colors; map semantic normal, muted, accent, selected, error,
modal, tabbar, tabbar.active, status, connection, profile, and progress styles
to named palette entries. Use filled blue text-on-accent for the active tab and
selected row; keep low-contrast text on surface for inactive tabs/status.
Delete `headerLayout`, its lipgloss-only title render, and its first-row call.
Render `chrome.Tabs(labels, active, width, ...)` at y=0, existing Name/Details
header at y=1, data from y=2, with `contentHeight := max(0, height-5)`.
Do not add a title row, redraw timer, or theme dependency.

- [ ] **Step 3: Populate one status row with typed segments**

At y=`height-1`, call `chrome.Status` with left segments `IPC connected`
(priority 10), `profile <active subscription Name>` (priority 9, use ID when
Name is blank, else `profile not reported`), and job progress (priority 2).
Put pending command count on the right (priority 3). Keep the notice/error row
at `height-3` and key-help immediately above status at `height-2`. Use only
sanitized subscription display metadata from the IPC snapshot; no URL or
profile YAML.
Do not add reconnect: `tui.Run` still exits with its existing clear error if
IPC closes.
Use `chrome.Status`' existing priority fitting rather than a second width
calculation; keep the changed-row cache and short-height bounds.

- [ ] **Step 4: Verify the chrome slice**

Run `gofmt -w tui/render.go tui/render_test.go`, then
`go test -tags ci ./tui -count=1`. Review the changed files for duplicated
formatting or state; no new server/client contract is needed.

## Task 12: Snapshot-backed tables for each TUI tab

**Files:** Modify `tui/model.go`, `tui/render.go`, `tui/model_test.go`,
`tui/render_test.go`; create `tui/table.go` for per-tab column definitions
and priority fitting. Do not change IPC snapshots, the shared Notmutt library,
or the existing command/key dispatch.

**Interfaces:** Extend `Row` with `Cells []string` built from its snapshot in
`rowsForSnapshot`, preserving `ID`, `Title`, `Detail`, `kind`, and selected
identity. `render` consumes those cells, existing `table.Layout` and
`visibleRows`; status and tab positions are Task 11's contract. Per-tab
headings/widths/priorities are one local typed table definition, not a second
model or generic widget framework.

In `tui/table.go`, use these concrete local types (no new public API):

```go
type tableColumn struct {
    heading string
    width table.Col
    dropRank int // smaller drops first; 10 means mandatory if it fits
}
func tableColumns(tab Tab) []tableColumn
func fitTable(columns []tableColumn, width int) ([]int, table.Layout, []int)
```

Resource floors/caps in display order: Name 12/24, Type 12/18,
Format 6/8, Source 14/24, Enabled 7/8, Validated 9/10, Next 12/18;
separator `"  "`. Drop order at narrow widths: Next, Source, Format,
Type, Validated, Enabled, Name; retain Enabled with Name whenever the
terminal accommodates both. Other tab drop orders: Subscriptions Usage,
Next, Last, Source, State, Name; Filters Next, Source, Format, Target,
Validated, Enabled, Name; Proxies Outcome, Automation, Latency, Selected,
Group, Proxy. Settings uses Setting/Value only; Overview keeps its summary.

- [ ] **Step 1: Write full-width and narrow-width table regressions**

In `tui/render_test.go`, use `mockRender` and immutable `ipc.Event` fixtures:

```go
func TestRenderResourceTableAlignsAndUsesSourceHost(t *testing.T) {
    model := NewModel()
    model.Tab = TabResources
    model = model.Apply(ipc.Event{Snapshot: core.Snapshot{Resources: []core.ResourceSnapshot{
        {ID: "geo", Kind: "geosite.dat", Format: "dat", SourceHost: "mirror.example", Enabled: true, Validated: true},
    }}})
    wide := mockRender(t, model, 120, 12)
    for _, label := range []string{"Name", "Type", "Format", "Source", "Enabled", "Validated"} {
        if !strings.Contains(wide[1], label) { t.Fatalf("missing %s: %q", label, wide[1]) }
    }
    if !strings.Contains(wide[2], "mirror.example") || !strings.Contains(wide[2], "geosite.dat") {
        t.Fatalf("resource cells = %q", wide[2])
    }
    narrow := mockRender(t, model, 30, 12)
    if !strings.Contains(narrow[1], "Name") || !strings.Contains(narrow[1], "Enabled") || strings.Contains(narrow[1], "Source") {
        t.Fatalf("narrow resource columns = %q", narrow[1])
    }
}
```

Add a table-driven test for the remaining tab contracts using literal fixture
snapshots and consumer-visible headers/cells:

```go
func TestRenderTabTables(t *testing.T) {
    cases := []struct {
        tab Tab
        snapshot core.Snapshot
        header, row string
    }{
        {TabSubscriptions, core.Snapshot{Subscriptions: []core.SubscriptionSnapshot{{ID: "primary", Name: "Primary", SourceHost: "provider.example", Enabled: true}}}, "Source", "provider.example"},
        {TabFilters, core.Snapshot{Filters: []core.FilterSnapshot{{ID: "ads", Format: "yaml", Target: "REJECT", SourceHost: "rules.example", Enabled: true}}}, "Target", "REJECT"},
        {TabProxies, core.Snapshot{Groups: []core.GroupSnapshot{{ID: "main", Label: "Main", Selected: "alpha", Proxies: []string{"alpha"}}}, Proxies: []core.ProxySnapshot{{GroupID: "main", ID: "alpha", Outcome: "success", LatencyMillis: 45}}}, "Latency", "alpha"},
        {TabSettings, core.Snapshot{Binary: core.BinarySnapshot{Desired: "system"}}, "Value", "system"},
    }
    for _, tc := range cases {
        t.Run(string(tc.tab), func(t *testing.T) {
            model := NewModel()
            model.Tab = tc.tab
            model = model.Apply(ipc.Event{Snapshot: tc.snapshot})
            lines := mockRender(t, model, 120, 12)
            if !strings.Contains(lines[1], tc.header) || !strings.Contains(strings.Join(lines[2:8], " "), tc.row) {
                t.Fatalf("tab %s: header %q, rows %q", tc.tab, lines[1], lines[2:8])
            }
        })
    }
}
```

At width 20, keep the identifying cells and drop optional columns:

```go
func TestRenderNarrowTable(t *testing.T) {
    model := NewModel()
    model.Tab = TabResources
    model = model.Apply(ipc.Event{Snapshot: core.Snapshot{Resources: []core.ResourceSnapshot{
        {ID: "geo-active", Kind: "geosite.dat", SourceHost: "mirror.example", Enabled: true},
    }}})
    rows := mockRender(t, model, 20, 12)
    if !strings.Contains(rows[1], "Name") || strings.Contains(rows[1], "Source") || !strings.Contains(rows[2], "geo-active") {
        t.Fatalf("narrow table lost identity: header %q, row %q", rows[1], rows[2])
    }
}
```

Use a separate long multibyte ID fixture to verify the last visible glyph
does not straddle its name cell; the shared `table.Layout.Line` already pads
and clips by display width. Preserve `TestApplyKeepsStableProxyCursorAndModalFocus`
and `TestPrivateSourceModalNeverDisplaysEnteredURL`. Update
`TestRenderAlignsSettingsDetails` to assert new Setting/Value alignment rather
than the obsolete Details label. Overview retains its status summary rows.
Only `SourceHost` exists in snapshots: test the host is rendered; do not
write a tautological test that an absent URL field is absent.

Run: `go test -tags ci ./tui -run '^TestRender(ResourceTableAlignsAndUsesSourceHost|TabTables|NarrowTable)$' -count=1`.
Expect failure against the two-column renderer.

- [ ] **Step 2: Build cells from each snapshot once**

In `rowsForSnapshot`, set `Row.Cells` alongside each existing stable `Row.ID`:
Proxies `[group, proxy, selected, latency, outcome, automation]`; subscription
`[name, source host, state, last check, next refresh, usage]`; filter `[ID,
format, target, source host, enabled, validated, next update]`; resource `[ID,
kind, format, source host, enabled, validated, next update]`; Settings `[setting,
current value]`. Keep Overview's current summary rows. Derive active/disabled
and validated flags from typed booleans and preserve `Detail` for selected-row
hashes, destination, failure, usage, timestamps, and other secondary data.
Never put full source URLs, credentials, or profile contents into a cell,
detail, event, or error. Keep `Model.Apply`'s stable-ID selection and diff
logic unchanged; no new snapshot traversal on every painted row.

- [ ] **Step 3: Render prioritized columns and selected-row detail**

Use the per-tab headings and `table.Col{Floor,Cap}` widths above in display
order. `fitTable` removes the lowest-rank optional index while column floors
plus two-cell separators exceed available width; retain surviving indexes
in display order, then call the shared `table.Layout.Sizes(width, false)`.
If only the identity column remains, allow its width to shrink to the terminal
width and use `table.Layout.Line` for safe rune-width clipping. Header and
every row use exactly those same indexes and sizes. Keep Name/Enabled at
30 columns for the resource fixture; the 20-column case may show Name alone.
Place the selected `Row.Detail` in one sanitized detail line at `height-4`;
reserve it above the existing three footer rows, with visible data rows
`max(0, height-6)` starting at y=2. Keep inactive tab selections, focus,
modal, and bounded changed-row rendering intact. No horizontal scroll state,
per-cell goroutines, or list widget abstraction.

- [ ] **Step 4: Verify UI behavior and integrate**

Run `gofmt -w tui/model.go tui/render.go tui/table.go tui/model_test.go tui/render_test.go`,
then `go test -tags ci ./tui -count=1`,
`go test -tags ci -count=1 ./...`, and `go vet -tags ci ./...`. Exercise a real
`clashpulse tui` session on a PTY against a disposable local IPC server,
resize from wide to narrow, switch tabs, and observe the rendered status and
resource columns; do not use production subscriptions or credentials. Perform
the project DRY pass, rerun formatter and focused test, then commit only the
TUI files with `feat(tui): add catppuccin chrome and tables` if the user
authorizes a commit.

## Task 13: Release shared modal geometry and configuration form table

**Scope:** `references/notmutt/` is a separate Git repository. The user
authorized the nested module release `lib/tui/v0.1.1`. Notmutt consumes
geometry/wrapping; its mail actions stay local. ClashPulse consumes the new
form table. No new dependency or raw secret logging.

**Files:** Create `references/notmutt/lib/tui/modal/modal.go`,
`modal/modal_test.go`, `references/notmutt/lib/tui/form/form.go`, and
`form/form_test.go`; modify `references/notmutt/src/tui/model.go` at
`spliceBox` and `textDialogue.wrap`. Its `compose.go` caller stays unchanged.

**Produces in package `modal`:**

```go
type Box struct { X, Y, Width, Height, BodyRows int }
func Bottom(width, height, bodyRows, footerRows int) (Box, bool)
func Wrap(text string, cursorByte, cellWidth, maxRows int) (rows []string, cursorRow, cursorCol int)
```

**Produces in sibling package `form`:**

```go
type Kind uint8
const (Text Kind = iota; Toggle; Choice)
type Field struct { ID, Label, Value string; Kind Kind; Choices []string; Sensitive, ReadOnly bool }
type Change struct { ID, Value string }
type Row struct { ID, Text string; Selected bool }
type Form struct { fields []Field; selected, offset, cursor int; editing bool; edited map[string]string }
func New(fields []Field) (*Form, error)
func (f *Form) Clone() *Form
func (f *Form) Move(delta int)
func (f *Form) Toggle() bool
func (f *Form) Cycle(delta int) bool
func (f *Form) SetText(value string) bool
func (f *Form) Insert(text string) bool
func (f *Form) Backspace() bool
func (f *Form) MoveCursor(delta int)
func (f *Form) Changes() []Change
func (f *Form) Cancel()
func (f *Form) Rows(width, height int) []Row
```

`Form` receives action intentions after the client resolves its TOML
keymap; it owns no key strings or IPC commands. `Rows` aligns Field/Value
with the existing shared `table.Layout`, masks every sensitive input,
and windows to the selected stable field ID. Changes contain only edited
fields; Toggle flips `true`/`false` and Choice cycles declared values.
`form.New` rejects duplicate IDs, unknown kinds, and invalid choices,
and copies its input fields/options so a snapshot update cannot mutate an
in-progress form. Text is bounded to 4096 bytes; domain validation and
conversion to typed intent remain with ClashPulse. A blank sensitive
field means unchanged; an explicit `-` clear is interpreted by the client.

- [ ] **Step 1: Write failing shared-library tests**

In `modal/modal_test.go` and `form/form_test.go`:

```go
func TestBottomReservesFooterAndCapsBody(t *testing.T) {
    box, ok := Bottom(40, 10, 10, 2)
    if !ok || box.Y != 1 || box.Height != 7 || box.BodyRows != 5 { t.Fatalf("box = %+v %t", box, ok) }
    if _, ok := Bottom(2, 10, 1, 2); ok { t.Fatal("border did not fit") }
}
func TestWrapKeepsWideRuneAndCursorVisible(t *testing.T) {
    text := "\u4e2d\u56fdabc"
    rows, row, col := Wrap(text, len(text), 4, 2)
    if len(rows) != 2 || rows[0] != "\u4e2d\u56fd" || rows[1] != "abc" || row != 1 || col != 3 {
        t.Fatalf("wrap = %q cursor %d,%d", rows, row, col)
    }
}
```

In `form/form_test.go` (package `form`):

```go
func TestFormTogglesAndMasksSensitiveValues(t *testing.T) {
    form, err := New([]Field{{ID: "url", Label: "Source", Kind: Text, Sensitive: true},
        {ID: "enabled", Label: "Enabled", Kind: Toggle, Value: "false"}})
    if err != nil { t.Fatal(err) }
    form.SetText("https://private.invalid/?token=secret")
    form.Move(1)
    if !form.Toggle() || len(form.Changes()) != 2 { t.Fatal("toggle or edit was lost") }
    for _, row := range form.Rows(40, 2) {
        if strings.Contains(row.Text, "secret") { t.Fatal("sensitive value rendered") }
    }
    form.Cancel()
    if len(form.Changes()) != 0 { t.Fatal("cancel retained pending edits") }
}
```

Add a choice-cycle and long-field-list viewport test that asserts the
selected field remains visible after scrolling. Run from `lib/tui`:
`go test ./modal ./form -run '^Test(BottomReservesFooterAndCapsBody|WrapKeepsWideRuneAndCursorVisible|FormTogglesAndMasksSensitiveValues)$' -count=1`;
expected RED because the package does not exist.

- [ ] **Step 2: Implement separate modal geometry and form-table state**

`Bottom` reserves one top tab row, two border rows, and `footerRows`;
it caps body rows and returns false when no body row fits. `Wrap` counts
`runewidth` cells, clamps a byte cursor to a rune boundary, and windows
its rows. `Form` stores only its copied field descriptions and pending
values; `Rows` returns display text (masked for Sensitive) and selection,
never a raw URL. `Changes` returns edited IDs/values only; `Cancel`
discards pending edits. No raw key events, filesystem, or network.
Run `go test ./modal ./form -count=1` GREEN.

- [ ] **Step 3: Migrate Notmutt's existing overlay primitives**

In `spliceBox`, call `modal.Bottom(width,len(lines),len(content),2)`,
window body rows to `box.BodyRows` before lipgloss adds the existing
configurable border, and splice complete rows at `box.Y`. In
`textDialogue.wrap`, keep the current sanitization and label width,
then delegate entry wrapping/cursor to
`modal.Wrap(entry,d.cur,w,max(1,m.height-5))`. Do not move the mail
`dialogue.handle` interface, Lua hooks, or Notmutt styling. Notmutt need
not instantiate `form.Form` in this release.

- [ ] **Step 4: Verify and publish the authorized module tag**

Run `gofmt -w lib/tui/modal/modal.go lib/tui/modal/modal_test.go lib/tui/form/form.go lib/tui/form/form_test.go src/tui/model.go`,
`go test ./...` from `lib/tui`, and
`go test ./tui -run '^Test(DialogueBox|DialogueCursorEditing|DialogueEditorKeys)' -count=1`
from `src`. Recheck Notmutt status for user edits; stage only the files
above and commit `feat(tui): share modal layout and form tables`.
Confirm `git tag -l lib/tui/v0.1.1` is empty, tag that tested commit,
then `git push origin lib/tui/v0.1.1` (authorized). Do not push a branch
or force-push without separate authorization. Verify nested module tag
fetchability; on failure preserve local commits and report the blocker.

## Task 14: Pin shared modal and migrate subscription editing

**Files:** Modify `go.mod`, `go.sum`, `core/snapshot.go`, `app/runtime.go`,
`app/snapshot_stability_test.go`, `ipc/protocol.go`, `ui/views.go`,
`ui/subscription_editor_test.go`, `tui/model.go`, `tui/render.go`,
`tui/keys.go`, `tui/keys.toml`, `tui/model_test.go`,
`tui/render_test.go`, `tui/secret_modal_test.go`. Do not add a URL or
custom User-Agent to snapshots. Task 15 bumps IPC once for the combined
schema before either task is shipped.

**Consumes:** Tagged module `github.com/fishman/notmutt/lib/tui v0.1.1`:
`modal.Bottom`, `modal.Wrap`, and `form.Form`, `form.Field`, `form.Row`,
`form.Change` from Task 13.
**Produces:** `core.SubscriptionSnapshot` additionally carries safe current
`Route string`, `AllowHTTP`, `AllowInvalidTLS` booleans,
`RefreshIntervalSeconds`, and `TimeoutSeconds` (bounded integers).
`Model.Modal` retains a cloned `*form.Form` for an open configuration form;
commands remain typed `ipc.SubscriptionEdit`.

- [ ] **Step 1: Write subscription form and footer tests RED**

In `tui/model_test.go`, open a new subscription with `n`, enter stable ID,
name, private source URL and optional custom agent in their Field/Value
rows, toggle Enabled with Space, and Save with Ctrl+S. Assert exactly one
`CommandPutSubscription` carries the changed typed fields; Cancel carries
none. For editing, use a snapshot with Route `direct`, HTTP/TLS false,
refresh and timeout values. Toggle AllowHTTP and select `mihomo_proxy`;
Save must retain the absent URL and User-Agent, preserve the selected
subscription ID, and send the current toggles. `Model.Apply` of an
unrelated job event must preserve pending form edits. Replace the old
step-by-step wizard tests with these observable command/transition tests,
not a parallel deprecated editor.

```go
func TestSubscriptionFormKeepsPrivateSourceOnToggle(t *testing.T) {
    model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Subscriptions:
        []core.SubscriptionSnapshot{{ID: "feed", Name: "Feed", SourceHost: "provider.example", Route: "direct"}}}})
    model.Tab = TabSubscriptions
    model.Selection[TabSubscriptions] = "subscription:feed"
    model, _, _ = model.HandleKey("e")
    if model.Modal == nil || model.Modal.Form == nil { t.Fatal("form table not opened") }
    for i := 0; i < 7; i++ { model, _, _ = model.HandleKey("down") }
    model, _, _ = model.HandleKey("space")
    model, command, quit := model.HandleKey("ctrl+s")
    if quit || model.Modal != nil || command == nil || command.Subscription == nil ||
        command.Subscription.AllowHTTP == nil || !*command.Subscription.AllowHTTP ||
        command.Subscription.URL != nil || command.Subscription.UserAgent != nil {
        t.Fatal("HTTP toggle changed private source or failed to submit")
    }
}
```

The final test must assert the `ipc.Command` payload and no source URL/agent
field, not merely the modal closing. In `tui/render_test.go`, assert a
bordered form at `80x16`, status at bottom, hotkeys above it, masked URL
and agent, and scroll-preserved selection at `30x9`. At `20x5` no box may
draw outside the viewport. Name tests
`TestSubscriptionFormKeepsPrivateSourceOnToggle`,
`TestSubscriptionFormMasksInputsAndKeepsFooter`, and
`TestSubscriptionSafePolicySnapshot`. Run
`go test -tags ci ./tui ./app -run '^TestSubscription(FormKeepsPrivateSourceOnToggle|FormMasksInputsAndKeepsFooter|SafePolicySnapshot)$' -count=1`;
expected RED against the wizard and missing snapshot policy fields.

- [ ] **Step 2: Pin and render the released modal package**

Run `go get github.com/fishman/notmutt/lib/tui@v0.1.1`; verify exact pin,
sum, and no committed `replace` or `go.work`. Use `modal.Bottom` with
three reserved footer rows and `modal.Wrap` on masked display text.
Draw the border and aligned shared form rows with Catppuccin tcell styles;
keep focus and pending input if width/height cannot fit. Binary and
confirmation modals use the same geometry, not a duplicate box builder.

- [ ] **Step 3: Cut over the subscription editor completely**

Construct fields ID, Name, URL, User-Agent, Enabled, Refresh seconds,
Timeout seconds, Route choice, Allow HTTP, and Allow invalid TLS in
`tui/model.go`. Add declarative `form` context bindings to keys.toml:
up/down select, Space toggle, Enter edit/cycle, Left/Right choice, Ctrl+S
save, Esc cancel. Extend `NewKeymap` to recognize the form context;
`Model.HandleKey` resolves its actions before app-specific validation.
On edit, start on Name (ID is read-only) and preserve selection by stable
field ID across unrelated snapshots.
Clone the shared `Form` when copying `Model.Modal`, so unrelated IPC
snapshots do not mutate it. Map `Form.Changes()` to a single validated
`ipc.SubscriptionEdit` on Save; URL/User-Agent unchanged unless explicitly
edited, and `-` clears the override. Remove old field-step state and
prompt/wizard code for subscriptions. In `app.stateSnapshot`, expose only
the safe current policy fields above; `ipc.validateSnapshot` bounds them.
Preselect GUI route, refresh, timeout, and HTTP/TLS opt-ins from those safe
snapshot fields; keep URL and custom User-Agent as blank private edit
inputs. Changing one field never overwrites another or triggers refresh
before the normal app command path.
Do not advance IPC version independently here; Task 15 completes the
version-3 schema before release.

- [ ] **Step 4: Verify package and subscriptions editor**

Run `gofmt -w` on modified Go files,
`go test -tags ci ./tui ./app ./ipc -count=1`, and
`GOOS=windows CGO_ENABLED=0 go build -tags ci ./...`. Smoke-run
subscription edit against a disposable IPC service on a PTY. Never use
the real subscription token or raw proxy profile as a fixture.

## Task 15: Typed HTTP status and app-owned diagnostic snapshots

**Files:** Modify `download/client.go`, `download/fetch_test.go`,
`subscriptions/fetch.go`, `subscriptions/model.go`, `subscriptions/store.go`,
`subscriptions/fetch_test.go`, `app/runtime.go`, `app/serve.go`,
`core/snapshot.go`, `core/snapshot_test.go`, `ipc/protocol.go`,
`ipc/protocol_test.go`; create `app/diagnostics_test.go`. Keep raw HTTP
bodies and raw transport error strings out of the protocol.

**Interfaces:**

```go
// download
type StatusError struct{ Code int }
func (e StatusError) Error() string
// subscriptions
type FetchStatusError struct{ Code int }
func (e FetchStatusError) Error() string
func (e FetchStatusError) Unwrap() error // returns ErrFetch
// core
type DiagnosticSnapshot struct { At int64; Severity, Kind, SourceID, Message string }
// Snapshot gains Diagnostics []DiagnosticSnapshot; ErrorSnapshot gains SourceID.
```

```go
// app only: raw errors enter safeDiagnostic, not the ring writer.
type diagnosticEvent struct { Severity, Kind, SourceID, Message string; At time.Time }
func safeDiagnostic(kind, sourceID string, err error) diagnosticEvent
func (s *runtimeService) appendDiagnostic(event diagnosticEvent)
func (s *runtimeService) resolveIssue(kind, sourceID string)
```

`runtimeService` is the sole writer of a maximum 200-entry in-memory ring;
`core.CloneSnapshot` copies it. `ipc.ProtocolVersion` becomes 3 and
`validateSnapshot` bounds count and message bytes. Active issues are keyed
by `(kind, sourceID)`; a successful operation clears its own issue only.
Client views consume this typed snapshot in Task 16.

- [ ] **Step 1: Prove HTTP 406 classification without exposing a body**

Use `httptest.NewServer` returning `406` with an HTML body containing a
synthetic token; `download.Client.Fetch` must return `StatusError{Code:406}`
and no token/body in `Error()`. In subscriptions, call `Service.Refresh`
through a real service fixture and assert `errors.Is(err, ErrFetch)` plus
`errors.As(err, &status)` where `status.Code == 406`; last failure is a
bounded safe `HTTP 406` label, not the URL. Add a second `200` response and
assert the existing recovery behavior clears that failure. Write the failing test:

```go
func TestHTTPStatusErrorIsSafe(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusNotAcceptable)
        _, _ = w.Write([]byte("<html>private-token</html>"))
    }))
    defer server.Close()
    client := NewClient(func(Route) (http.RoundTripper, error) { return http.DefaultTransport, nil })
    _, err := client.Fetch(context.Background(), Request{
        URL: server.URL + "/profile?token=private-token", Route: Direct, MaxBytes: 64, AllowHTTP: true,
    })
    var status StatusError
    if !errors.As(err, &status) || status.Code != 406 || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "<html>") {
        t.Fatalf("unsafe status classification: %T", err)
    }
}
```

Run: `go test ./download ./subscriptions -run '^Test(HTTPStatusErrorIsSafe|RefreshReportsSafeHTTPStatus)$' -count=1`;
expected RED before typed errors exist.

- [ ] **Step 2: Implement safe status propagation**

In `download.Client.Fetch`, replace the 2xx-range error at the status branch
with `StatusError{Code:response.StatusCode}` after closing the body. In
`subscriptions.refresh`, recognize only that typed error with `errors.As`,
store a failure string constructed from its numeric code, and return
`FetchStatusError` wrapping `ErrFetch`; retain existing behavior for network,
parse, validation, and cancellation failures. In `Store.validFailure`, accept
only `HTTP 400` through `HTTP 599` in addition to existing fixed sentinel
strings. `publicFailureLabel` displays the safe code but never a URL.
Preserve old known-good bytes on every failed request.

- [ ] **Step 3: Prove bounded redacted history and independent issue clearing**

Add app/core/ipc regressions with synthetic safe IDs and a private URL in
an underlying `errors.New` value: an app error entry must exclude the URL,
the 201st entry drops the oldest and keeps the latest 200, and recovery of
subscription `alpha` removes only `(refresh_subscription, alpha)` while
`(resource, beta)` remains. Snapshot cloning must not share the diagnostic
slice; a protocol-2 client must be rejected by a protocol-3 service.
Write this failing ring test and the named issue/clone/version tests:

```go
func TestDiagnosticsBoundedAndRedacted(t *testing.T) {
    service := &runtimeService{}
    secret := "https://private.invalid/profile?token=secret"
    event := safeDiagnostic("refresh_subscription", "alpha", errors.New(secret))
    if strings.Contains(event.Message, "secret") || strings.Contains(event.Message, "private.invalid") {
        t.Fatal("raw error escaped the diagnostic boundary")
    }
    for i := 0; i < 201; i++ {
        event.At = time.Unix(int64(i+1), 0)
        service.appendDiagnostic(event)
    }
    if got := service.snapshot.Diagnostics; len(got) != 200 || got[0].At != 2 || got[199].At != 201 {
        t.Fatal("session log did not evict its oldest entry")
    }
}
```

Run:
`go test -tags ci ./app ./core ./ipc -run '^Test(DiagnosticsBoundedAndRedacted|IssueResolutionPreservesOtherSource|SnapshotClonesDiagnostics|RejectsOlderDiagnosticProtocol)$' -count=1`;
expected RED before the new snapshot field and issue owner exist.

- [ ] **Step 4: Implement one sanitized event path in app**

Add `DiagnosticSnapshot` and `ErrorSnapshot.SourceID` in core and update
clone/validation. `runtimeService` appends only fixed known messages and
numeric HTTP status to its bounded ring; never forward `err.Error()` or a
downloaded body. Replace the current wholesale `snapshot.Errors = ...`
and unconditional `= nil` with upsert/remove by operation and stable source
ID. Thread `cmd.SubscriptionID`, `ResourceID`, or `FilterID` through
`finishServiceIntent`; migrate other `reportError` callers to empty source
ID. On `stateChanged`, reconcile the sanitized subscription failure
transitions, so scheduled recovery clears the matching issue and appends
one recovery entry. Leave unrelated failures active. `publish` still
coalesces identical snapshots and never polls.

- [ ] **Step 5: Verify backend contract**

Run `gofmt -w` on modified Go files, `go test -tags ci ./download ./subscriptions ./app ./core ./ipc -count=1`,
and `go vet -tags ci ./...`. Run the existing
`TestRefresh304ClearsPriorFailureAndPublishesRecovery` regression and ensure
a healthy 304 emits no duplicate.

## Task 16: Notmutt-style log viewers over one IPC snapshot

**Files:** Modify `tui/model.go`, `tui/render.go`, `tui/keys.go`,
`tui/keys.toml`, `tui/model_test.go`, `tui/render_test.go`, `ui/ui.go`,
`ui/errors_test.go`, `README.md`; create `ui/activity.go` for the
virtualized Overview activity dialog. No new top-level GUI view or
client-owned log ring.

**Consumes:** `core.Snapshot.Diagnostics []core.DiagnosticSnapshot` from
Task 15, immutable through IPC. Neither client rereads files or subscribes
to a second logging service.

- [ ] **Step 1: Write view-state and secrecy tests RED**

Use a real snapshot fixture with one proxy and two safe diagnostics:

```go
func TestLogOverlayScrollsWithoutDispatch(t *testing.T) {
    model := NewModel()
    model.Tab = TabProxies
    model = model.Apply(ipc.Event{Snapshot: core.Snapshot{
        Groups: []core.GroupSnapshot{{ID: "main", Proxies: []string{"alpha"}}},
        Proxies: []core.ProxySnapshot{{ID: "alpha", GroupID: "main"}},
        Diagnostics: []core.DiagnosticSnapshot{
            {At: 100, Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"},
            {At: 101, Severity: "info", Kind: "subscription", SourceID: "feed", Message: "recovered"},
        },
    }})
    selected := model.Selection[TabProxies]
    model, intent, quit := model.HandleKey("~")
    if !model.LogOpen || intent != nil || quit || model.Selection[TabProxies] != selected {
        t.Fatal("log overlay changed control state")
    }
    model, intent, quit = model.HandleKey("q")
    if model.LogOpen || intent != nil || quit || model.Selection[TabProxies] != selected {
        t.Fatal("closing log dispatched quit or moved cursor")
    }
}
```

Also scroll two or more pages with `up` and assert the offset changes;
render safe entries with timestamp/severity/source ID and bottom status
at wide and narrow widths. Test URL redaction at Task 15's app boundary,
not with an impossible secret-bearing sanitized snapshot. In
`ui/errors_test.go`, open `View activity` in Fyne's virtual app and assert
the `widget.List` follows a later immutable snapshot without rebuilding
the window. Name the render and GUI tests
`TestRenderLogKeepsBottomStatus` and
`TestActivityDialogShowsSanitizedSnapshot`. Run
`go test -tags ci ./tui ./ui -run '^Test(LogOverlayScrollsWithoutDispatch|RenderLogKeepsBottomStatus|ActivityDialogShowsSanitizedSnapshot)$' -count=1`;
expected RED before the log viewers exist.

- [ ] **Step 2: Add declarative log navigation**

Add a global `~` -> `toggle_log` binding to `tui/keys.toml` and
`knownAction`. When the overlay is open, handle up/down/page/home/end as
view-only scroll and consume any other key to close without executing its
underlying action (including `q`). The overlay is a view state over the
snapshot, not a second mutable log. Its help derives from the keymap;
preserve `Model.Tab`, cursor, and modal focus across snapshot updates.
Use `tcell` and the existing width-safe renderer; append the latest error
as a low-priority `chrome.Status` segment after IPC/profile on wide screens.

- [ ] **Step 3: Show the same ring in GUI Overview**

Add one `View activity` button without introducing a seventh top-level
view. Open a scrollable `widget.List` dialog and update its immutable
entries only through the current `fyne.Do` snapshot path; no direct file,
controller, or process calls. Render severity and time, stable source ID,
and safe message. Keep secrets absent by construction: do not display
URLs from edit forms or raw Mihomo logs.
Update the existing README run section with `~` for the TUI session log
and Overview > View activity for GUI diagnostics; both show only
sanitized current-session events.

- [ ] **Step 4: Verify live client behavior**

Run `gofmt -w` on modified Go files, `go test -tags ci ./tui ./ui -count=1`,
and a PTY smoke run against a disposable IPC service emitting an error
then a recovery. Open `~`, scroll, close, switch tabs; confirm GUI activity
dialog contents with Fyne's virtual test app. No timer-driven redraw.

## Task 17: Align TUI Settings and every visible GUI data row

**Files:** Modify `tui/model.go`, `tui/table.go`, `tui/render.go`,
`tui/render_test.go`, `ui/views.go`, `ui/display.go`, `ui/ui.go`,
`ui/settings_binary_test.go`, `ui/display_test.go`, `ui/errors_test.go`,
`ui/proxy_action_test.go`. Keep the responsive Settings sidebar and
`widget.List` virtualization; no new GUI widget framework.

**Consumes:** Immutable `core.BinarySnapshot`, monitor, DNS, and system
proxy snapshots; `Keymap` remains the sole TUI shortcut source. Produces
three Settings columns, `Setting | Value | Action`, in the existing
`table.Layout` geometry. `Row.Detail` remains a plain selected-row
description, not the source of table cells.

- [ ] **Step 1: Prove aligned columns and original controls RED**

In `tui/render_test.go`, apply a snapshot with desired system Mihomo,
observed version, enabled monitor, and active system proxy; render at
100 columns. Assert row 1 contains `Setting`, `Value`, and `Action` at
distinct aligned columns; a binary row shows its actual desired value,
the system proxy row shows requested and active state, and no Settings
header/data/selected-detail line contains ` | `. Repeat at 30 columns:
retain Setting and Value, drop Action. Bind the binary edit action to a
different key in `NewKeymap` and verify the Action cell follows the
keymap. In headless Fyne tests, assert Overview counts, binary identity,
proxy rows, subscription rows, resource rows, filter rows, and Settings
binary fields render in separate aligned labels without visible ` | `.
All existing selection and action buttons remain operable.

```go
func TestSettingsTableAlignedWithoutPipes(t *testing.T) {
    model := NewModel()
    model.Tab = TabSettings
    model = model.Apply(ipc.Event{Snapshot: core.Snapshot{
        Binary: core.BinarySnapshot{Desired: "system", ObservedVersion: "v1"},
        SystemProxy: core.SystemProxySnapshot{Enabled: true, Active: true},
    }})
    lines := mockRender(t, model, 100, 12)
    if !strings.Contains(lines[1], "Setting") || !strings.Contains(lines[1], "Value") || !strings.Contains(lines[1], "Action") ||
        !strings.Contains(lines[2], "system") || strings.Contains(strings.Join(lines[1:9], ""), " | ") {
        t.Fatalf("unaligned Settings: %q", lines[1:9])
    }
}
```

In the GUI test, update a virtual window with one group/proxy,
subscription, resource, and filter. Assert independent status/source/
format labels in the `widget.List` row objects, no pipe delimiter in
visible text, and no accidental URL or credential display. Rebind a
Settings TUI action with `NewKeymap` and assert its Action cell changes.
Run `go test -tags ci ./tui ./ui -run '^Test(SettingsTableAlignedWithoutPipes|SettingsActionUsesKeymap|GUIRowsAreAlignedWithoutPipes|SettingsBinaryFieldsAreAligned)$' -count=1`;
expected RED against current `strings.Cut` and Fyne pipe labels.

- [ ] **Step 2: Build typed Settings cells once per row**

In `rowsForSnapshot`, construct Settings `Row.Cells` directly as
`[]string{title, value, ""}` from typed fields; remove the trailing loop
that calls `strings.Cut(rows[i].Detail, " | ")`. Give each editable row
its existing action ID (`edit_binary`, `edit_monitor_interval`, etc.) so
`Model.Rows` derives the Action cell using `Keymap`'s declarative bindings.
Convert `Row.Detail` to a plain sentence or show nothing when the table
already carries the value/action; remove pipe-delimited Settings detail
assembly. Keep monitor test URL and DNS resolver credentials out of
incidental detail rows; edit modals retain their private input policy.
In `tui/table.go`, add Action with a lower drop priority than Value,
and let Setting/Value floors shrink at 30 columns.

- [ ] **Step 3: Align GUI summaries and virtualized rows**

In `ui/ui.go`, replace the Overview's six pipe-separated counters and
binary identity chain with a labelled grid/form; update only changed
labels on a new snapshot. In `ui/views.go`, keep `widget.List` rows but
lay out name, source/format, and status as separate reusable grid cells
for Subscriptions, Data Resources, and Filter Lists. Proxies gets
separate name/state/latency cells while preserving its selection
callback and group automation controls. In `ui/display.go`, remove
concatenated `" | "` status/group labels and any helper that only exists
to produce them. Settings Mihomo binary becomes a `widget.Form` with
Desired, Observed, Capabilities, and Compatibility values. Replace the
single-line `Connected | action queued` status text with plain text.
At narrow widths wrap/truncate labels rather than overlap; preserve
theme and button focus. Never render source URLs or resolver credentials.

- [ ] **Step 4: Verify layout and behavior**

Run `gofmt -w` on modified Go files,
`go test -tags ci ./tui ./ui -count=1`, and the full
`go test -tags ci -count=1 ./...` plus `go vet -tags ci ./...`.
Use `mockRender` at 30 and 100 columns, Fyne's headless canvas at 480
and 1200 widths, and a disposable IPC PTY smoke run to confirm logs and
Settings actions remain usable. Perform the project DRY pass, rerun
formatter and focused checks. Commit only this change's tracked files;
never stage `references/`, `test.yaml`, or `list.yaml` through the
ClashPulse repository.

## Task 18: Migrate remaining TUI configuration editors to shared form tables

**Files:** Modify `tui/model.go`, `tui/managed_editor.go`, `tui/render.go`,
`tui/model_test.go`, `tui/resource_format_test.go`,
`tui/dns_rename_test.go`, `tui/secret_modal_test.go`. Reuse Task 14's
tagged `form.Form`, form-context keymap, bottom-anchored `modal` renderer,
typed command surface. Do not add another generic editor or import app
services into the TUI.

**Consumes:** `form.New`, `Form.Clone`, `Form.Changes`, and the
declarative form bindings from Tasks 13-14. Produces no new IPC schema:
resource/filter/DNS/monitor commands already exist.

- [ ] **Step 1: Write per-editor behavioral tests RED**

In `tui/model_test.go` open edit modals for an enabled resource, filter,
DNS resolver with DNSCrypt, and monitor policy. Each must show all its
fields as aligned rows, retain selected field and pending edits across
unrelated snapshot events, toggle a boolean via Space, and send exactly
one validated IPC command on Ctrl+S. For resource/filter edits, leave
Source URL blank and assert the command omits it; Cancel sends nothing.
For DNS resolver/route forms preserve all other resolver sets/routes in
the resulting typed policy intent. For monitor form verify interval,
timeout, threshold, and Enabled changes are one `ConfigPatch` rather
than a sequence of single-field wizard submissions.

```go
func TestResourceFormToggleRetainsPrivateSource(t *testing.T) {
    model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Resources:
        []core.ResourceSnapshot{{ID: "geo", Kind: "geosite.dat", Format: "dat", Enabled: true}}}})
    model.Tab = TabResources
    model.Selection[TabResources] = "resource:geo"
    model, _, _ = model.HandleKey("e")
    if model.Modal == nil || model.Modal.Form == nil { t.Fatal("resource form absent") }
    for i := 0; i < 4; i++ { model, _, _ = model.HandleKey("down") }
    model, _, _ = model.HandleKey("space")
    model, command, _ := model.HandleKey("ctrl+s")
    if command == nil || command.Resource == nil || command.Resource.URL != nil ||
        command.Resource.Enabled == nil || *command.Resource.Enabled {
        t.Fatal("resource edit changed private source or ignored toggle")
    }
}
```

Name focused tests
`TestResourceFormToggleRetainsPrivateSource`,
`TestFilterFormToggleRetainsSource`, `TestDNSFormPreservesOtherRoutes`,
and `TestMonitorFormSubmitsPolicy`. Run
`go test -tags ci ./tui -run '^Test(ResourceFormToggleRetainsPrivateSource|FilterFormToggleRetainsSource|DNSFormPreservesOtherRoutes|MonitorFormSubmitsPolicy)$' -count=1`;
expected RED against the current one-field-at-a-time wizards.

- [ ] **Step 2: Replace managed resource and filter wizard paths**

Build form fields with existing resource ID, kind, format, rule type,
private source, enabled toggle, interval, and optional pin; filters
use ID, resource ID, format, target, and enabled toggle. Start edits
on the first mutable field and leave private Source URL empty with a
masked `unchanged` display. Map `Form.Changes()` into existing
`ipc.ResourceEdit` and `ipc.FilterEdit` after existing domain checks.
Remove `managedForm.step`, `managedModalPrompt`, and obsolete wizard
dispatch. No credentials in displayed rows or notices.

- [ ] **Step 3: Replace DNS and monitor wizard paths**

Build DNS resolver/set and route forms over the typed policy snapshot;
mask sensitive resolver endpoints. The Save action validates the whole
candidate policy and sends one `CommandSetDNSRouting`; do not drop
unrelated sets/routes. Monitor form edits Enabled, URL, interval,
timeout, concurrency, thresholds, bad samples, improvement, cooldown,
and jitter before one `CommandUpdateConfiguration`. Keep simple
delete-confirmation and binary-selection modals on shared box geometry.
Remove old field-step state and unreachable prompt branches; key help
derives from one declarative form map.

- [ ] **Step 4: Verify complete form behavior and code cutover**

Run `gofmt -w tui/model.go tui/managed_editor.go tui/render.go tui/model_test.go tui/resource_format_test.go tui/dns_rename_test.go tui/secret_modal_test.go`,
`go test -tags ci ./tui -count=1`, `go test -tags ci -count=1 ./...`,
`go vet -tags ci ./...`,
`GOOS=windows CGO_ENABLED=0 go build -tags ci ./...`, and
`GOOS=darwin CGO_ENABLED=0 go build -tags ci ./...`. Smoke-run each
config form on a PTY against an authenticated disposable IPC server;
no real source URL or
credentials. Perform the DRY pass, remove unused wizard helpers/tests,
then rerun formatter and focused checks. Release readiness requires
Tasks 13-18 together; the subscription snapshot and diagnostics both
negotiate protocol 3 only after this complete cutover.

## Modal and diagnostics review focus

- A 406 HTML challenge with a token in its body must become a numeric,
  URL-free diagnostic; Task 15's HTTP classification test owns it.
- A successful 304 after failure clears only its source issue and emits one
  recovery without writing healthy no-op checks; Task 15 app tests plus
  `TestRefresh304ClearsPriorFailureAndPublishesRecovery` own it.
- An already-running desktop service must deliver earlier log entries to
  a newly connected TUI while its selected proxy stays put; Task 16's
  overlay test owns it.
- A tiny or Unicode-width terminal must retain modal footer rows, the
  selected form field, and masked private input; shared `modal`/`form`
  tests in Task 13 and consumer tests in Tasks 14 and 18 own it.
- Customized Settings shortcuts and GUI-wide status/source columns must
  stay aligned without visible pipe separators or stale actions; Task 17
  tests own both.

## TUI redesign review focus

- Narrow terminal with a selected resource: preserve its identity and enabled
  state before source/format/next-update columns. `TestRenderNarrowTable`.
- Long multibyte resource ID: truncate on display-cell boundaries; no partial
  wide rune in the last cell. `TestRenderNarrowTable`.
- Missing active subscription: show `profile not reported`, not the first
  enabled subscription. `TestRenderStatusIdentifiesConnectionAndActiveProfile`.
- Short height with a modal: no negative layout/row overlap or panic; preserve
  modal focus. Extend `TestRenderKeepsActiveTabVisible` with 3-row input.
- Secret in an edit modal: mask input while showing safe source hosts in table
  rows. Preserve `TestPrivateSourceModalNeverDisplaysEnteredURL` and use
  `SourceHost` in `TestRenderResourceTableAlignsAndUsesSourceHost`.

## Plan self-review

- Spec coverage: Tasks 1-12 are already shipped. Task 13 extracts and
  releases independent Notmutt `modal` and `form` packages. Task 14 pins
  them and cuts over subscription editing with safe policy metadata;
  Task 15 owns typed HTTP errors, active issues, diagnostics, and the
  final IPC version 3. Task 16 presents the ring in both clients, Task
  17 removes all visible GUI pipes and aligns TUI Settings, and Task 18
  completes resource/filter/DNS/monitor form cutover. Chinese translation
  is separately requested for the end and not implied by these tasks.
- Placeholder scan: each new task names files, consumes/produces contracts,
  concrete red behavior checks, focused verification, and privacy limits.
- Type consistency: `modal.Bottom`/`modal.Wrap` and `form.New` are sibling
  packages under the same tagged module. `core.SubscriptionSnapshot` adds
  only safe policy intent; `core.DiagnosticSnapshot` adds bounded sanitized
  history. `ipc.ProtocolVersion` advances once from 2 to 3 before release.

## Execution Handoff

Review Tasks 13-18 in this updated existing plan. The previously chosen
Native execution method is preserved. After review, execute sequentially:
Notmutt `lib/tui/v0.1.1` release before the ClashPulse import; complete
the shared subscription/diagnostic IPC version-3 schema before shipping;
then logs, GUI alignment, and remaining form editors. Do not execute
the already-shipped foundation and chrome tasks again. Run one
fresh-context review over the complete diff after green checks;
resolve Important/Critical findings before branch integration.