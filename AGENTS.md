# ClashPulse - project rules

ClashPulse is a cross-platform Go desktop client for Mihomo. It makes a proxy
selection trustworthy: it measures candidates regularly, switches only when a
better candidate is proven, and applies user-controlled rule lists before
Mihomo starts.

`references/clash-verge-rev/` is read-only reference material, not a required
feature list or implementation template. It demonstrates Mihomo profile
management, controller-driven node selection, bounded latency checks, system
proxy and TUN support, and desktop packaging. Reuse a pattern only when it
serves ClashPulse's goal; prefer a smaller Go-native design and omit features that
do not.

## Product invariants

- Mihomo remains the data plane and configuration authority. The client starts,
  stops, observes, and configures a child Mihomo process through its documented
  local controller API. Do not fork or import Mihomo into this module.
- Bind the controller to loopback, generate a random secret, keep it out of
  logs, and reject an endpoint that is not loopback unless the user explicitly
  opts in.
- One lifecycle owner owns the process, controller readiness, configuration
  generation, restart, and shutdown. A failed start leaves no stale PID, port,
  or active system proxy state.
- Configuration changes are transactional: write a private temporary file,
  validate it with the selected Mihomo binary, atomically replace the active
  generated config, then restart. Preserve the prior known-good config and
  surface validation errors without destroying it.
- User source profiles are immutable inputs. Generated configuration, fetched
  subscriptions, filter caches, logs, and local settings are separate,
  private state. Use 0700 directories and 0600 files.
- No shell interpolation. Start Mihomo with an explicit executable and argv.
  Validate all paths and URLs at the boundary. Never log subscription URLs,
  controller secrets, credentials, or proxy credentials.

## Mihomo binary selection and compatibility

ClashPulse supports a system-installed Mihomo executable and an optional
shipped executable. On Arch Linux, system Mihomo is the default and preferred
selection; the shipped binary is an explicit fallback, not a replacement for
the distribution package.

- Persist binary selection as typed user intent: `system`, `bundled`, or an
  explicitly validated executable path. Resolve `system` using `PATH` without
  shell interpolation. Validate an explicit path is a regular executable owned
  or selected by the user.
- Inspect the selected binary before start or configuration generation: obtain
  version/build identity and validate it can run its own configuration check.
  Store the observed identity separately from desired selection.
- Maintain a small typed Mihomo capability model derived from documented
  version/build behavior: supported configuration fields, geodata formats,
  controller endpoints, and validation invocation. Generated configuration and
  UI settings must be gated by capability, never assumed from the executable
  name.
- A capability missing from the selected binary is a clear compatibility error
  with the affected setting/resource named. Never silently discard user intent,
  write unsupported fields, or substitute a different binary.
- Changing binary is transactional: stop only after the new binary is
  inspected and validates a newly generated configuration; on failure preserve
  the prior selected binary, known-good configuration, and running process.
- The bundled executable is versioned and platform-specific, has recorded
  provenance/checksum/license, is private executable state, and is never
  downloaded or updated silently. System executable updates are detected at
  the next controlled start/reload and re-run capability inspection.
- Expose desired binary, observed version/build, capability summary, and last
  compatibility failure over IPC for both GUI and TUI. Never claim feature
  parity between system and bundled Mihomo without a verified capability match.

## Architecture

Keep packages directional and boring:

```text
cmd/clashpulse -> app -> core -> mihomo
                       -> filters
                       -> monitor
                       -> ipc
                       -> ui
                       -> tui

`app` is the sole lifecycle owner and exposes the same typed command surface to
both clients through `ipc`; `ui` and `tui` never import or own `core`,
`monitor`, or `mihomo` directly.

- `core` owns typed configuration, events, persistence boundaries, and
  lifecycle state. It imports neither Fyne widgets nor Mihomo HTTP types.
- `mihomo` is the sole controller/process adapter. It exposes typed operations
  such as `Start`, `Stop`, `Proxies`, `Delay`, `Select`, `Reload`, and status
  subscriptions. Controller request/response types do not escape this package.
- `filters` fetches, pins, validates, compiles, and emits Mihomo `rule-providers`
  and `RULE-SET` references. It never mutates a source profile in place.
- `monitor` owns probe scheduling, history, decision policy, and switch events.
  The UI may request a manual run but never implements health policy.
- `ui` renders snapshots and sends intent. The Fyne UI thread MUST NEVER block
  on controller I/O, network I/O, filesystem I/O, subprocess waits, locks, or
  probe scheduling. Core events update UI state on Fyne's main thread with
  `fyne.Do`; use `fyne.DoAndWait` only for a short in-memory UI mutation whose
  caller needs its result.

Every URL test, profile refresh, filter refresh, controller request, and
automatic-switch calculation runs in a cancellable background worker. The UI
receives immutable snapshots only. Use contexts for every subprocess, HTTP
request, refresh loop, and shutdown. Goroutines have one owner, cancellation
path, and completion wait. Events must be bounded or coalesced; a slow UI must
not stall controller polling.

Low CPU is a product invariant. No busy loops, unconditional redraw timers,
polling while idle, or per-item goroutines. Schedule each monitor at its next
deadline with a timer, stop it when automation is disabled, and wake early only
for a user action, controller event, or cancellation. Batch due probes with a
small bounded worker pool; coalesce results into one model update per batch or
short debounce window. Do not repeatedly re-sort, re-render, parse config, or
write state when the observed state is unchanged.

## IPC and alternate clients

ClashPulse must be controllable from a TUI without duplicating control logic.
The desktop process hosts a local IPC service; the Fyne UI and TUI are thin
clients over the same versioned command and snapshot/event protocol.

- IPC binds only to a per-user Unix domain socket on Unix and a per-user named
  pipe on Windows. Its containing directory is 0700; reject remote/TCP binds.
- Authenticate peers using OS ownership and restrictive endpoint permissions.
  Do not expose controller secrets, subscription URLs, or proxy credentials in
  IPC messages or logs.
- Use a small request/response plus coalesced snapshot-event protocol. Commands
  cover lifecycle, configuration changes, manual probes, group selection, and
  automation settings. Events carry immutable state snapshots, never transport
  or UI objects.
- Define request and event schema versions from the first message. Reject an
  incompatible client rather than guessing. Keep the first protocol concrete;
  do not add a plugin RPC framework.
- IPC handlers enqueue intent to `app` and return promptly. They never run a
  probe, controller request, process wait, config reload, or filesystem write
  inline. Slow or disconnected clients must not delay monitor or lifecycle
  work.
- The TUI is an optional client command, not another lifecycle owner. It
  connects to an already-running desktop service and reports a clear error when
  no service is available.

## TUI decision

Use the architecture lessons from `references/notmutt/`: TUI state machines,
event handling, and rendering primitives are isolated from control logic; the
TUI is a reference client over IPC, never a lifecycle owner. Use `tcell` and
`lipgloss` as renderer primitives, not a widget framework. Do not use Bubble
Tea or adopt `references/mihomo-tui/` navigation/layout patterns.

- The TUI owns only view state: current snapshot, focus, selection, modal or
  dialogue state, key dispatch, and a bounded pending-intent indicator. It must
  not perform controller, network, filesystem, subprocess, scheduler, or
  configuration work.
- Background commands report coalesced immutable snapshot/progress/error events
  through IPC. Rendering is event-driven; no UI refresh ticker. A progress row
  is derived from job state, never blocks focus, and disappears on completion.
- Keybindings are declarative TOML data by context. Default bindings are
  keyboard-first and every exposed action has a default binding; help derives
  from the same map. Add alternate keymaps only when a real second scheme is
  required.
- Lists use stable identities and incremental snapshot diffs. Preserve cursor
  and modal state across unrelated updates; never rebuild the full screen for a
  single probe, subscription, or resource result.

## URL-test and automatic switching

Mihomo `url-test` groups choose within their configured policy. For a managed
`select` group, ClashPulse may choose a member through the controller only when
that group is explicitly opted in. Manual selection disables automation for the
group until the user re-enables it.

A probe policy contains: HTTPS test URL, interval, timeout, bounded
concurrency, threshold, required consecutive bad samples, required improvement,
and switch cooldown. Defaults must be conservative and visible.

- Measure through Mihomo's controller delay endpoint, not direct outbound HTTP;
  a direct request tests the wrong path.
- Record each sample with proxy, group, test URL identity, finish time, latency,
  and outcome (`success`, `timeout`, `error`). `0` or an error is not a good
  latency value.
- A switch needs both: the current selected proxy is unhealthy or exceeds the
  threshold for the configured consecutive samples, and an eligible candidate
  is materially better. Use median or another documented robust window metric,
  never a single fast sample.
- Enforce one switch at a time per group, cooldown after a switch, and a
  circuit-breaker when every candidate fails. Never oscillate between two
  proxies. Emit the exact reason, old proxy, new proxy, and measured evidence.
- Probe batches have bounded concurrency, per-probe timeout, jitter, and
  cancellation. Coalesce UI updates rather than redrawing per response.
- Persist history only if needed for user-visible charts or decisions. Bound
  retention and never store credentials in it.

## Subscriptions and profiles

A subscription is a remote immutable profile input plus private metadata and a
last-known-good downloaded snapshot. Manage subscriptions directly; do not copy
Clash Verge Rev's script, merge, proxy, group, or rule editor feature set.

- Support add, list, rename, enable/disable, refresh now, configure automatic
  refresh, activate, and delete. A subscription has a stable ID, display name,
  source URL, enabled state, refresh interval, HTTP timeout, last success,
  last failure, content hash, and optional parsed usage/expiry metadata.
- Treat the subscription URL and every downloaded body as sensitive. Keep URLs
  and snapshots in private state, redact URLs in all events and logs, and never
  expose proxy credentials through UI, TUI, or IPC.
- Accept HTTPS sources by default. Reject other schemes, URL userinfo, and
  redirect targets that violate the policy. HTTP or invalid TLS requires a
  visible per-subscription opt-in; invalid TLS must never be a global default.
- Download through one bounded HTTP client with request context, redirect cap,
  response-size cap, per-host connection limits, and no retries that bypass
  cancellation. A connection route is explicit: direct, system proxy, or the
  currently running Mihomo proxy. Do not silently fall through between routes.
- Fetch to a private temporary file. Check the HTTP result, parse YAML, require
  a Mihomo proxy profile shape, generate the complete active configuration with
  filters, validate it using Mihomo, then atomically promote the snapshot and
  generated configuration. A failed fetch or validation leaves the active
  configuration and prior snapshot untouched.
- Persist ETag and Last-Modified validators when provided. A 304 response is a
  successful unchanged refresh: update the checked time but do not write files,
  restart Mihomo, or redraw clients. Hash successful bodies to suppress an
  otherwise identical update.
- Parse standard subscription metadata headers only as optional display data:
  traffic usage, total allowance, and expiry. Metadata parse failure never
  rejects an otherwise valid profile.
- A single scheduler owns automatic refresh. It schedules the next due enabled
  subscription with a timer, adds bounded jitter, runs a small worker pool, and
  stops when the application shuts down. No interval polling loops or one
  goroutine per subscription.
- Activation is explicit. An enabled subscription may refresh without changing
  the active Mihomo profile; only activation applies a newly validated snapshot
  and restarts Mihomo when the generated configuration changed.
- Deleting removes private metadata and snapshots only after activation has
  moved away from that subscription. Never delete an active known-good snapshot
  before its replacement validates.
- Expose subscription snapshots and commands over IPC so GUI and TUI have the
  same behavior. UI/TUI display source host, state, last check/success/failure,
  next refresh, hash prefix, and usage metadata, not secrets.

## Filter lists

A filter list is declarative data, not executable code. It has a stable ID,
HTTPS URL or local path, format, update interval, SHA-256 pin when supplied,
and enabled state. The client fetches to a temporary file, applies size and
response limits, validates the declared format, and atomically promotes the
last valid version. A failed refresh keeps the previous valid list.

Support Mihomo-compatible rule providers first. Generate deterministic provider
names from stable IDs, and generate `RULE-SET` references from that one
registry. Reject duplicate IDs, invalid targets, non-HTTPS remote URLs unless
explicitly allowed, malformed rules, and unknown formats. No JavaScript, Lua,
or shell hooks in filtering. Display source, last success, version hash,
validation failure, and active generated output.

## External data resources

ClashPulse manages the external data files required by its active Mihomo
configuration: GeoIP, GeoSite, optional MMDB/ASN data, and configured
rule-provider lists. Treat all of them as untrusted versioned resources, not as
bundled opaque files or executable extensions.

- Use one typed resource registry. Every resource has a stable ID, kind,
  declared format, source URL or local path, enabled state, update interval,
  optional SHA-256 pin, current hash, last check/success/failure, and generated
  destination. Filter lists use this registry rather than a parallel downloader.
- Ship no silent hard-coded third-party URLs. Provide a documented default
  source set only after provenance, license, update ownership, and format
  compatibility review; users can disable or replace every source.
- Initially support Mihomo-compatible `geoip.dat`, `geosite.dat`, and
  `Country.mmdb` where the selected Mihomo version/configuration uses them,
  plus existing rule-provider formats. Add new resource kinds only with a
  documented Mihomo configuration consumer and validator.
- Support configured domain/IP rule-set resources for DNS routing, including a
  China-domain list used by a Mihomo `nameserver-policy` entry such as
  `rule-set:cn`. The rule-set is a normal registry resource with format and
  source explicitly declared; it is not an implicit special download.
- Resolve resources from the generated configuration before startup or reload.
  If an enabled required resource has no valid local version, keep the current
  known-good runtime configuration and show the resource-specific failure;
  never start a partially generated configuration.
- Reuse the bounded downloader, URL/TLS policy, conditional request metadata,
  temporary file, hash comparison, atomic promotion, and single timer-owned
  scheduler used for subscriptions. Resource refresh must neither poll while
  idle nor fan out unbounded goroutines.
- Validate identity before promotion: registry-controlled destination names,
  no path traversal or symlinks, exact format/kind match, response-size cap,
  and SHA-256 when pinned. A bad download must not replace the last valid file.
- Generate deterministic paths and Mihomo references from registry IDs. The
  original subscription is never edited; rendered configuration alone points
  to managed resource files.
- Resource updates apply only after complete configuration validation. Reload
  Mihomo only when the active generated configuration or referenced resource
  bytes changed; coalesce simultaneous resource updates into one reload.
- Show and expose over IPC the resource kind, source host, current hash,
  validated state, last result, next update, and active destination identity,
  never credentials or full sensitive URLs.

## DNS routing policy

Manage DNS routing as declarative generated Mihomo configuration: matchers map
to resolver sets. Typical China DNS policy is a maintained China-domain
rule-set mapped to designated domestic resolvers; GeoIP fallback filtering is a
separate decision, not a substitute for domain routing.

- Model resolver sets separately from their routing rules. A resolver set has
  named endpoints; a routing rule references a domain suffix, GeoSite selector,
  or managed `RULE-SET` resource and selects one resolver set.
- Validate resolver URL/scheme, rule reference, and resource format at load
  time. Generate `nameserver-policy` and any required `rule-providers`
  deterministically from the registry. Do not paste provider URLs into profile
  files or user-entered YAML.
- No China-specific default is enabled silently. Ship an opt-in reviewed preset
  only after source provenance and resolver privacy policy are documented; users
  can inspect, disable, or replace its resources and resolvers.
- A DNS policy/resource update is transactional with the generated
  configuration. Keep the active policy when a referenced list or resolver
  validation fails, and coalesce the resulting Mihomo reload with other
  resource changes.
- Support DNSCrypt through an optional externally managed `dnscrypt-proxy`
  listener, represented as a named resolver endpoint (normally loopback UDP or
  TCP on a non-conflicting port such as `127.0.0.1:5353`). Mihomo remains the
  DNS policy engine and sends selected queries to that endpoint; DNSCrypt
  encrypts the upstream hop. Do not bundle, launch, configure, or silently
  update DNSCrypt in the first implementation.
- Default the Mihomo DNS listener to loopback. Validate no resolver endpoint
  loops back to the same listener, detect an unavailable configured DNSCrypt
  endpoint before applying the policy, and fail transactionally rather than
  silently falling back to a different resolver. System DNS redirection,
  privileged port binding, and service-manager integration remain explicit
  platform features, not implicit side effects of enabling DNSCrypt.

## GUI decision: Fyne

Use Fyne v2 for the desktop GUI. BrowserChooser demonstrates the required model:
a single Go binary for Linux, macOS, and Windows; Fyne renders on Wayland/X11
without Electron, Node, or WebKit; a Fyne theme follows system dark/light;
platform-only code is isolated behind build-tag files; packaging is per target.

Implementation rules:

- Start with one `fyne.App`, one main window, a tray menu, and only the views
  required by shipped behavior: Overview, Proxies, Subscriptions, Filter Lists,
  Data Resources, and Settings. Do not build a widget abstraction layer before
  repeated behavior exists.
- Keep Fyne calls on its UI thread. Background monitor/controller work sends
  immutable view snapshots over the app event layer; UI applies them with
  `fyne.Do`.
- Use Fyne `widget.List` virtualization for proxy/filter tables. Do not rebuild
  the window or walk filesystem trees during refreshes.
- Set theme variant before `app.NewWithID`; use system theme by default. A
  forced theme is a typed setting, not an environment-variable side effect
  after startup.
- Platform behavior belongs in small `*_linux.go`, `*_darwin.go`, and
  `*_windows.go` files. Keep core and UI testable without a display.
- Use explicit Linux, macOS, and Windows packaging jobs. Fyne desktop builds
  use native graphics dependencies; test each release target rather than
  assuming cross-compilation works.

Fyne due diligence is positive but not automatic approval: BrowserChooser uses
Fyne `v2.8.1-rc3`, vendors its dependency graph, has focused Go tests, and the
vendored Fyne license is BSD 3-Clause. ClashPulse must select a released Fyne v2
version, pin it exactly, review its full transitive graph and platform build
requirements, record license notices, run `go vet` and targeted tests on all
three desktop targets, and vendor only after that review. Any new dependency
needs the same review: maintained upstream, clear license/provenance, narrow
purpose, exact version, transitive-license/security review, and an accepted
removal or upgrade plan. Prefer stdlib and existing dependencies.

## Go and configuration

- Go standard library first. Clear names, small concrete types, no speculative
  interfaces, no hidden globals. One concept has one owner.
- All ClashPulse user intent is strict typed TOML. Use YAML only for generated
  Mihomo configuration and formats Mihomo requires. Keep downloaded snapshots,
  conditional-request validators, probe history, and observed controller state
  out of TOML in separate private state.
- Use an explicit split-file schema in the private configuration directory:
  `config.toml` for app/core/monitor/DNS settings, `subscriptions.toml` for
  subscriptions, `resources.toml` for geodata and DNS-route resources, and
  `filters.toml` for filter lists. A missing optional file is an empty section;
  unknown files are ignored rather than merged implicitly. Do not build a
  generic TOML overlay language.
- File shape is schema shape: decode each file 1:1 into named typed structs,
  reject unknown keys, duplicate stable IDs, invalid enums, malformed URLs,
  unsupported binary/resource references, and cross-file dangling references.
  Defaults live in Go and are applied once before validation; the serialized
  TOML records user intent only.
- Human-editability is a product requirement: each file has one obvious purpose,
  shallow tables, stable names, documented defaults, and a complete annotated
  example. Avoid generic maps, implicit inheritance, generated user-facing
  values, deeply nested overrides, and cross-file precedence rules. GUI/TUI
  expose the same fields; no setting exists only in a hidden configuration form.
- `config.Store` is the only in-process configuration read path. It owns the
  immutable current snapshot and exposes section subscriptions. Workers receive
  snapshots or derived plans; they never reread TOML ad hoc. Store replacement
  computes a typed section diff and notifies only changed sections.
- Watch the configuration directory, not individual files, so atomic-save
  rename/create/delete events work. Debounce a burst, then load and validate
  the complete file set in a cancellable background worker. Do not use polling:
  low idle CPU is mandatory. Add an audited cross-platform filesystem watcher
  dependency only if the standard library cannot provide this behavior.
- Reload is all-or-nothing: parse every file, apply defaults, validate complete
  cross-file semantics, derive a new snapshot and change plan, then atomically
  replace the store. Any error keeps the old snapshot and reports a structured
  configuration error to GUI/TUI/IPC with file and key, without partially
  restarting Mihomo or schedulers.
- Apply a reload plan in dependency order: update passive UI settings, then
  scheduler/monitor policy, then resource and DNS plans, then generated Mihomo
  configuration. Only the final validated plan may reload Mihomo; unchanged
  configuration does nothing. Binary changes retain their separate
  transactional switch rule.
- UI/TUI edits use the same config writer: private temporary TOML files plus
  atomic replace, followed by the normal full-set reload. Filesystem events
  caused by self-writes are coalesced and the resulting no-op snapshot emits no
  duplicate work or redraw.
- Target Linux, macOS, and Windows. Guard OS-specific code with build tags;
  compile all target-specific files in CI.

## Testing and verification

- Unit test policy independently from UI and controller transport: no switch on
  one spike, timeout/error handling, hysteresis, cooldown, all-failed behavior,
  manual override, and cancellation.
- Test filter-list validation and atomic promotion with a local HTTP server and
temporary directories: bad update retains last good output; invalid and
oversized input cannot become active.
- Test subscription import, conditional refresh, redirect policy, size limits,
  atomic promotion, failed-update retention, activation, and scheduler
  cancellation with `httptest` and temporary directories.
- Test binary selection and capability gating with disposable fake Mihomo
  executables: system precedence on Arch Linux, version discrepancy rejection,
  transactional binary switch failure, and generated configuration validation.
- Test external resource validation, pin mismatch, conditional refresh, atomic
  retention, generated references, and coalesced reload behavior with `httptest`
  and temporary directories.
- Test DNS policy generation: valid China-domain rule-set references render
  deterministic `nameserver-policy`; unknown resources, invalid resolvers,
  DNSCrypt listener loops/unavailability, or invalid updates retain the prior
  generated configuration.
- Test strict TOML loading and autoreload with temporary directories: atomic
  rename/create/delete reloads the complete file set once, cross-file changes
  publish only affected sections, unknown keys/dangling references preserve the
  live snapshot, and self-writes cause no duplicate work.
- Regression fixes are TDD: add the failing test, run it red against current
  behavior, then fix and run it green. Do not weaken or delete a regression
test without explicit user approval.
- Every non-trivial change leaves one runnable check. Run the narrowest relevant
  `go test` command, then `go vet ./...` before merging broad changes.

## Working rules

- Conventional commits: `type(scope): imperative lowercase subject`. No AI
  marker or co-author line in code commits.
- ASCII in code and project prose. Comments explain only a non-obvious
  constraint, security boundary, or tradeoff.
- Do not add telemetry, crash upload, a cloud backend, automatic dependency
  updates, or subscription sharing without explicit user approval.
- Do not claim a framework, dependency, Mihomo controller endpoint, or
  cross-platform packaging path works without a documented check in this repo.

## Project name

Working name: **ClashPulse**. It carries the familiar Clash ecosystem name and
the product promise: continuously measure proxy health and react before latency
becomes user-visible. Before public release, perform trademark, GitHub
namespace, package-name, and domain checks; rename if any conflict appears.
