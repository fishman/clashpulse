# ClashPulse staged architecture

## Goal

ClashPulse is a cross-platform Go desktop client for externally managed Mihomo
binaries. It manages subscriptions, required external data, DNS policy, and
measured proxy switching. A Fyne desktop client and a keyboard-first TUI use the
same local IPC service.

The first usable release is a daily client: it starts a selected Mihomo binary,
manages subscriptions/resources, monitors opted-in selector groups, performs
conservative automatic switching, exposes Fyne and TUI clients, and explicitly
controls the OS HTTP/HTTPS System Proxy. TUN is deferred.

`references/clash-verge-rev/`, `references/notmutt/`, `references/mihomo-tui/`,
and other reference projects are read-only behavior references, not sources of
code or scope.

## Non-goals

- Importing, forking, or embedding Mihomo. It runs as a child process behind
  its documented local controller API.
- TUN, service-manager installation, privileged networking, or DNS redirection.
- Script/merge/rule/profile editors, subscription pools, cloud sync, telemetry,
  remote management, plugin systems, or automatic core downloads.
- Bubble Tea, widget-style terminal frameworks, or mihomo-tui navigation.

## Runtime architecture

```text
cmd/clashpulse
  default -> app service + Fyne IPC client
  tui     -> TUI IPC client

app
  -> config.Store
  -> mihomo
  -> subscriptions
  -> resources -> filters
  -> dns
  -> monitor
  -> sysproxy
  -> ipc

Fyne UI / TUI -> versioned local IPC -> app intent queue
```

`app` is the single lifecycle owner. It owns process readiness, config planning,
resource/subscription schedulers, generated config promotion, monitor lifetime,
and System Proxy rollback. `ui` and `tui` hold view state only; they never
import or own `mihomo`, monitor, downloader, or config writers.

Every I/O operation has a context and runs off the UI/TUI event path. The app
emits coalesced immutable snapshots. A slow client is isolated from process,
monitor, and scheduler progress.

## Shared library dependencies

ClashPulse reuses released, exact versions of focused modules extracted from
notmutt:

- `github.com/fishman/notmutt/lib/xdg` for XDG base-directory resolution;
- `github.com/fishman/notmutt/lib/localipc` for bounded local Unix-socket
  transport, stale-socket handling, and same-user peer checks.

ClashPulse owns its typed IPC messages, protocol-version negotiation, snapshots,
secret redaction, and Windows named-pipe adapter. It does not reuse notmutt's
Lua IPC protocol. Development-only `replace` directives and cross-repository
workspace wiring are forbidden in committed ClashPulse module metadata. A shared
module must be tagged before ClashPulse pins it; new shared utilities are
extracted only after both projects prove the same low-level contract.

## Binary selection and capabilities

Selection is user intent in `config.toml`:

```toml
[mihomo]
binary = "system" # system | bundled | explicit validated path
```

On Arch Linux `system` resolves `mihomo` from `PATH` and is the default.
`bundled` is an explicit platform/version/checksum/provenance-recorded fallback.
An explicit path must be a regular executable selected by the user. No shell
interpolation is permitted.

Before start, generation, and a binary change, the adapter obtains version/build
identity and verifies the executable's own configuration-validation invocation.
It derives a small capability record for controller endpoints, configuration
fields, geodata formats, and validation syntax. The renderer gates every
feature against that record. Unsupported selected-binary capability is an error
naming the setting/resource; it never silently drops intent or substitutes a
binary.

A binary change builds and validates a candidate configuration first. Only a
successful candidate stops the current runtime. Failed inspection/validation
keeps the current binary, config, and process intact.

## User configuration and reload

All user intent is strict TOML in a private configuration directory:

```text
config.toml          app, binary, monitor, DNS, System Proxy
subscriptions.toml   subscription definitions
resources.toml       geodata and DNS-route resource definitions
filters.toml         filter-list definitions
```

Each file has one obvious purpose and shallow, stable tables. It has a complete
annotated example, documented defaults, and no generic overlay, implicit
inheritance, hidden fields, or generated user-facing values. GUI/TUI edit the
same settings. The canonical shapes are:

```toml
# config.toml
[mihomo]
binary = "system"

[monitor]
enabled = true
test_url = "https://example.invalid/generate_204"
interval = "5m"

# subscriptions.toml
[[subscription]]
id = "primary"
name = "Primary"
url = "https://provider.example/subscription"
enabled = true
refresh_interval = "12h"

# resources.toml
[[resource]]
id = "geosite"
kind = "geosite-dat"
url = "https://source.example/geosite.dat"
enabled = true

# filters.toml
[[filter]]
id = "ads"
resource = "ads-rules"
enabled = true
```

Each file decodes 1:1 into named typed Go structs. Optional missing files are
empty sections; unknown files are ignored. Unknown keys, duplicate IDs,
malformed URLs, invalid enums, dangling cross-file IDs, and unsupported
binary/resource use fail the full load. Defaults exist in Go, are applied once
before validation, and are not serialized as observed state.

`config.Store` is the only configuration read path. It owns an immutable
snapshot and per-section subscriptions. A filesystem watcher observes the
configuration directory, debounces rename/create/delete bursts, and reloads the
complete set in a cancellable worker. No polling.

Reload is transactional:

1. Parse all four files.
2. Apply defaults and validate cross-file semantics.
3. Derive typed section diffs and an app change plan.
4. Atomically replace `config.Store` only if validation succeeds.
5. Apply the plan: passive client settings, monitor/scheduler, resources/DNS,
   then rendered Mihomo configuration.

Any error keeps the old snapshot and runtime unchanged and reports a structured
file/key error over IPC. UI/TUI writers use private temporary TOML plus atomic
replace; watcher events caused by self-writes coalesce into a no-op when the
snapshot is unchanged.

## Subscription lifecycle

A subscription is an immutable remote profile input, private metadata, and a
last-known-good snapshot. `subscriptions.toml` records only user intent: stable
ID, display name, enabled state, URL, explicit fetch route, timeout, refresh
interval, and explicit HTTP/invalid-TLS opt-ins. Downloaded YAML, ETag,
Last-Modified, content hash, timestamps, parsed usage/expiry, and failures live
in private state.

Commands: add, list, rename, enable/disable, refresh-now, refresh schedule,
activate, and delete.

The bounded downloader has a context, redirect cap, response cap, per-host
connection limits, and explicit route: direct, OS proxy, or running Mihomo
proxy. HTTPS is default. URL userinfo, unsafe redirect targets, HTTP, and
invalid TLS are rejected unless the per-subscription policy permits them.

Refresh flow:

1. Fetch into a private temporary file using conditional validators.
2. `304 Not Modified`: update checked time only.
3. Parse YAML and require Mihomo proxy-profile shape.
4. Hash the body; unchanged bytes update metadata only.
5. Render the complete active configuration with filters/resources.
6. Validate through the selected Mihomo executable.
7. Atomically promote the snapshot and candidate generated configuration.

Failed refresh or validation preserves the existing snapshot and generated
runtime config. Refresh does not activate a subscription. Activation explicitly
selects a validated snapshot and reloads/restarts Mihomo only if rendered bytes
changed. Deleting waits until another validated snapshot is active.

One timer scheduler owns due subscriptions, applies bounded jitter, and invokes
a small worker pool. It stops with `app` and never uses one ticker/goroutine per
subscription.

## External resources, filters, and DNS

One typed resource registry owns GeoIP, GeoSite, MMDB/ASN data, rule providers,
and DNS route lists. Registry entries include stable ID, kind, declared format,
source URL/local path, enabled state, interval, optional SHA-256 pin, generated
destination, and private observed metadata.

Initial kinds are `geoip.dat`, `geosite.dat`, `Country.mmdb` when selected
Mihomo supports it, Mihomo rule-provider formats, and domain/IP rule-set files
for DNS routing. No third-party URL is silently built in. A default source set
requires recorded provenance, license, maintainer/update ownership, and format
compatibility; every source remains replaceable/disableable.

Resources reuse subscription downloader/scheduler semantics. Promotion verifies
a registry-controlled destination, no traversal/symlink, response limit,
kind/format, and optional SHA-256. Bad bytes retain the last valid resource.
Generated Mihomo YAML contains deterministic local paths and `RULE-SET`/
rule-provider references; subscription source YAML is never edited.

DNS is declarative. Named resolver sets hold validated endpoint URLs. Routing
rules select a resolver set using suffixes, GeoSite selectors, or managed
rule-set IDs. A China-domain list can render `nameserver-policy` using
`rule-set:cn`, but it is an explicit resource. GeoIP fallback filtering is not
a substitute for domain routing. China DNS is an opt-in preset only after source
and resolver privacy review.

DNSCrypt is an optional external `dnscrypt-proxy` listener, normally a loopback
UDP/TCP resolver such as `127.0.0.1:5353`. Mihomo remains the policy engine;
DNSCrypt encrypts the selected upstream hop. ClashPulse does not bundle, launch,
configure, or update DNSCrypt. Before apply, validate listener availability and
reject a resolver loop to Mihomo's own loopback DNS listener. No silent resolver
fallback. System DNS redirection and port-53 privilege handling remain deferred.

Resource/DNS changes validate the complete generated configuration and coalesce
into one reload only when active bytes or rendered config changes.

## Monitor and switching policy

The monitor runs only for explicitly opted-in managed `select` groups. Mihomo
`url-test` groups keep Mihomo's native policy. Manual selection disables
automation for that group until explicit re-enable.

A policy defines HTTPS URL, interval, timeout, bounded concurrency, unhealthy
threshold, bad-sample count, minimum improvement, and cooldown. Probes call
Mihomo's controller delay endpoint, never direct outbound HTTP. Each sample
records group/proxy/test-URL identity/time/outcome/latency; timeout, error, and
zero are failures rather than good latency.

A switch requires both an unhealthy/excessively slow current choice across the
required window and an eligible candidate with a materially better robust
metric, initially median. Per group: one switch in flight, cooldown, and an
all-failed circuit breaker. Emit old/new proxy and exact evidence.

The scheduler sleeps until its next deadline, wakes only for deadline, manual
command, controller event, or cancellation, and probes through a bounded worker
pool with jitter. Batch results become one coalesced model update. No busy loop,
idle polling, per-proxy goroutine, unconditional redraw, unchanged state write,
or repeated sort.

## Process, controller, and System Proxy

`mihomo` is a small in-repo adapter over explicit `exec.CommandContext` argv,
`net/http`, and only required controller WebSockets. It owns controller secret
in memory, random loopback controller address/secret generation, readiness,
`Start`, `Stop`, `Reload`, `Proxies`, `Delay`, `Select`, and sanitized status
subscriptions. Controller HTTP types do not leave the package.

Lifecycle:

1. Inspect selected binary and capabilities.
2. Resolve active validated subscription/resources/DNS.
3. Render private candidate YAML with random loopback controller secret.
4. Validate with selected Mihomo.
5. Atomically promote candidate YAML.
6. Start/reload Mihomo; wait for authenticated controller readiness.
7. Apply requested System Proxy only after readiness.

Failure at any step stops partial state and restores/retains the last known-good
runtime. System Proxy platform adapters set OS HTTP/HTTPS proxy values to the
ready Mihomo listener and capture enough prior state to clear/restore project
changes on controlled shutdown, failed start, listener change, or unexpected
child exit. TUN is out of scope.

## IPC and clients

IPC uses a per-user Unix socket in a 0700 directory on Unix and a per-user named
pipe on Windows. TCP listeners are forbidden. Endpoint permissions and OS peer
ownership authenticate clients. The first message negotiates schema version;
unsupported versions are rejected. Controller secrets, profile content, and proxy
credentials never cross IPC. Subscription and resource URLs may appear only in
authenticated local edit requests, never snapshots, events, responses, or logs.

Protocol surface is intentionally small:

- request/response: service status, snapshots, lifecycle, config write,
  subscription/resource commands, activate, manual probe, group select,
  automation and System Proxy settings;
- coalesced event: immutable service snapshot, progress, structured error;
- command acknowledgement means intent queued, not work completed.

Handlers enqueue to `app` and return. They never perform I/O inline.
Disconnected clients lose events but never delay lifecycle or monitoring.

Fyne runs as a local IPC client. It applies immutable snapshots with `fyne.Do`
and never blocks its UI thread.

The TUI runs as `clashpulse tui` and connects to an existing service. No service
is a clear user error. It uses `tcell` and `lipgloss`, not Bubble Tea. Following
notmutt's architecture, view model, selection/focus/modal state, declarative
key dispatch, incremental stable-ID list diffs, and rendering are separate from
control logic. It redraws from IPC events only; progress is a non-focus-stealing
row driven by job events.

## Staged delivery

### Stage 1: foundations and configuration

Create Go module, XDG/private directory handling, strict TOML structs/defaults,
`config.Store`, transaction writer, cross-platform directory watcher, typed
errors, and a headless app event/snapshot model.

Gate: strict multi-file load; unknown key/file attribution; atomic rename,
create/delete debounce; cross-file rejection retaining old store; self-write
no-op; 0700/0600 permissions.

### Stage 2: binary and process lifecycle

Implement binary selection, capability inspection, private generated config,
fake-binary validation, child lifecycle, loopback random-secret controller
readiness, and typed controller operations.

Gate: fake executables prove Arch system precedence, capability rejection,
failed transactional switch retention, secret redaction, and no stale child or
System Proxy state after failed start.

### Stage 3: subscriptions

Implement TOML intent, private snapshot/metadata storage, bounded conditional
downloader, explicit routes, YAML/profile validation, activation transaction,
and timer-owned refresh scheduler.

Gate: `httptest` proves redirects/TLS policy/size bounds/304/hash suppression,
failed refresh retention, explicit activation, scheduler cancellation, and no
secret in snapshot IPC.

### Stage 4: resources, filters, and DNS

Implement registry, format validators, promotion, filter rendering, resolver
sets, DNS routes, China rule-set opt-in, and optional external DNSCrypt endpoint.

Gate: pin mismatch and malformed files retain valid bytes; generated references
are deterministic; DNS route rendering is deterministic; invalid resolver,
missing resource, DNSCrypt unavailability, or loop preserves known-good config;
simultaneous updates cause one reload.

### Stage 5: monitor and switching

Implement due-time scheduling, bounded probing through controller delay,
history/median decisions, manual overrides, cooldown, all-failed circuit
breaker, and reasoned switch events.

Gate: no switch on one spike; failures are never good latency; hysteresis,
cooldown, all-failed, cancellation, manual override, and exact switch evidence.

### Stage 6: IPC, TUI, and System Proxy

Implement local versioned IPC, app intent queue, snapshots/events, System Proxy
platform adapters, notmutt-inspired tcell/lipgloss TUI, and headless client
smoke coverage.

Gate: incompatible client rejection, permissions, slow/disconnected-client
isolation, TUI state preservation across unrelated diffs, System Proxy only
applies after readiness and resets on failure/shutdown.

### Stage 7: Fyne desktop client and packaging

Implement one-window Fyne client, tray, Overview/Proxies/Subscriptions/Filter
Lists/Data Resources/Settings views, virtualized lists, and platform packaging.

Gate: manual UI smoke against running IPC service; no UI-thread blocking path;
Linux/macOS/Windows targeted tests and `go vet`; released Fyne exact-version,
license, transitive dependency, and native-build review before vendoring.

## Verification policy

Use unit tests for decisions and parsers, `httptest` for all downloader behavior,
and disposable fake Mihomo executables for process/capability contracts. Tests
never need live subscriptions, root, a VPN, or user profiles. Regression fixes
are test-first: demonstrate failure before the fix and green after it. Each
stage leaves a narrow runnable check; broad changes also run `go vet ./...`.
