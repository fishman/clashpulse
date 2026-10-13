# ClashPulse TUN Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Linux TUN mode through a ClashPulse-owned Mihomo child, with a private TUN-scoped privileged copy, transactional apply, and verified activation.

**Architecture:** `tun` owns the privileged copy; `mihomo` renders the `tun` block and TUN DNS, reports `tun.enable` via `GET /configs`, and stops Mihomo gracefully; `app` sequences privilege, render, validation, restart, verification, and rollback; `ui`/`tui` are IPC clients.

**Tech Stack:** Go standard library (`os/exec`, `syscall`/`golang.org/x/sys` only if already a dependency), existing `config`, `ipc`, `core`, `mihomo`, `app`, `ui` (Fyne), `tui` (tcell/lipgloss).

**Spec:** `docs/superpowers/specs/2026-10-13-clashpulse-tun-design.md` (read it in full; it is normative).

## Global Constraints

- Linux only; other platforms report `unsupported-platform` and never render TUN.
- Privileged copy: `<state>/tun/mihomo`, dir 0700, file 0700, grant record `<state>/tun/grant.json` 0600.
- Elevation only via `pkexec <abs setcap> cap_net_admin+ep <copy>`; explicit argv, no shell, no sudo, no stored password.
- `Prepare` runs off the service loop with a 2-minute timeout; timeout/cancel = privilege declined.
- Automatic starts never prompt; without `ready` privilege they render without TUN and report `requested, inactive`.
- Rendered tun: `enable, stack, device: clashpulse0, auto-route: true, auto-detect-interface: true, dns-hijack: [any:53], mtu: 1500, strict-route: false, auto-redirect: false`.
- TUN DNS: force `dns.enable: true`; `dns_mode` profile|fake-ip|redir-host; fake-ip fills `fake-ip-range: 198.18.0.1/16`, `fake-ip-range6: 2001:2::0/64` (IPv6 only), default filter `["*.lan","*.local","*.arpa","time.*.com","ntp.*.com","+.pool.ntp.org"]` only when the profile has none.
- TUN activation is confirmed only by `GET /configs` `tun.enable == true` after controller readiness.
- Stop: SIGTERM, SIGKILL after 3 seconds.
- Intent is never auto-disabled. IPC protocol version 7.
- ASCII in code; test data uses reserved names only; strict TOML.

## Landed contract (already in the tree; do not rename)

- `config.Snapshot.Tun config.Tun{Enabled bool; Stack, DNSMode string}`; constants `config.TunStackGVisor|TunStackMixed|TunStackSystem`, `config.TunDNSProfile|TunDNSFakeIP|TunDNSRedirHost`; config section name `"tun"` in `diffSections`.
- `config.SettingsPatch.TunEnabled *bool, TunStack *string, TunDNSMode *string`.
- `ipc.ProtocolVersion = 7`; `ipc.CommandAuthorizeTun = "authorize_tun"`; `ipc.ConfigPatch.TunEnabled *bool (tun_enabled), TunStack *string (tun_stack), TunDNSMode *string (tun_dns_mode)`.
- `core.Snapshot.Tun core.TunSnapshot{Enabled, Active bool; Stack, DNSMode, Privilege, PrivilegedPath, Device, LastFailure string; Leftover bool}`; `core.TunPrivilegeReady|Missing|Stale|NoSUID|NoAgent|Unsupported`.
- `core.ActivationTunPrivilege`, `core.ActivationTunStart` with English messages.
- `mihomo.Capability.SupportsTUN bool`.

## Interfaces produced by tasks

Task 2 (`tun`):
```go
type Privilege string // values equal the core.TunPrivilege* strings
type Source struct{ Path, Version string } // the inspected selected binary
func NewManager(dir string) *Manager      // dir = <state>/tun
func (m *Manager) Path() string           // privileged copy path
func (m *Manager) Check(source Source) Privilege // ready|missing|stale|nosuid|unsupported-platform
func (m *Manager) Prepare(ctx context.Context, source Source) error
func (m *Manager) Revoke() error
var ErrDeclined, ErrNoAgent, ErrUnsupported error // Prepare outcomes, errors.Is-able
```

Task 3 (`mihomo`):
```go
const TunDevice = "clashpulse0"
func (c *Controller) TunEnabled(ctx context.Context) (bool, error) // GET /configs -> tun.enable
// Render honors intent.Tun (Enabled && capability.SupportsTUN) or returns a
// *CapabilityError naming "tun"; app passes intent with Tun.Enabled=false to
// render without TUN.
// Inspect sets SupportsTUN from the documented version floor.
// (*Process).Stop: SIGTERM, then SIGKILL after 3s (Windows: Kill).
```

## Tasks

### Task 1: Config, IPC validation, core reporting (owner: ConfigIPC)
Files: `config/toml_decode.go`, `config/load.go`, `config/write.go`, `config/*_test.go`, `examples/config.toml`, `ipc/protocol.go`, `ipc/*_test.go`, `core/snapshot.go` (`ConfigOverrideSnapshot.Valid` keys `tun`, `dns.enable`, `dns.enhanced-mode`, `dns.fake-ip-range`, `dns.fake-ip-range6`, `dns.fake-ip-filter`), `localize/locale/zh-CN.toml` (override.* and activation messages), `localize/locale/en.toml` if English override descriptions live there.
- [ ] Failing tests: `[tun]` defaults (false, gvisor, profile); strict rejection of unknown keys and invalid enums with `config.toml: tun.<key>`; PatchSettings writes the three fields; ipc rejects invalid stack/dns_mode, includes Tun fields in the empty-patch check, `authorize_tun` validates with no payload; snapshot validation bounds the Tun strings (known enums, path length, no newline); version-rejection tests for 7.
- [ ] Implement; `go test ./config ./ipc ./core ./localize`.

### Task 2: `tun` package (owner: TunPkg)
Files: create `tun/tun.go`, `tun/tun_linux.go`, `tun/tun_other.go`, `tun/tun_linux_test.go`.
- [ ] Failing tests with seams (fake `pkexec` script on PATH, injectable capability probe and statfs): Prepare copies via temp+rename with 0700, records grant; pkexec success/126/127+"No authentication agent found"/127 other/ctx timeout map to nil/ErrDeclined/ErrNoAgent/ErrDeclined/ErrDeclined; Check reports missing, stale (source hash changed), nosuid, ready; Revoke removes copy and grant and is idempotent; other platforms return unsupported.
- [ ] Implement; `go test ./tun`.

### Task 3: Mihomo render, controller, process (owner: MihomoTun)
Files: `mihomo/render.go`, `mihomo/binary.go`, `mihomo/controller.go`, `mihomo/process.go` (+ `process_unix.go`/`process_windows.go` if signals need build tags), tests.
- [ ] Failing golden tests: tun block exact; DNS for each dns_mode against profile DNS none / fake-ip / redir-host / `enable: false`; filter fallback only when absent; fake-ip-range6 only with IPv6; resolver endpoint inside the fake-ip range rejected; Tun.Enabled without SupportsTUN -> CapabilityError "tun"; TUN off renders byte-identical to today; ExplainOverrides reports the new keys only when changed; `TunEnabled` parses `{"tun":{"enable":true}}` and errors on missing field; Stop sends SIGTERM and escalates after 5s (test with a fake child ignoring SIGTERM, using a shortened injectable grace).
- [ ] Implement; `go test ./mihomo`.

### Task 4: App integration (owner: AppTun; starts after Tasks 2 and 3)
Files: `app/service.go`, `app/lifecycle.go`, `app/commands.go`, `app/runtime.go`, `app/serve.go`, new `app/tun.go`, tests.
- [ ] Failing tests with fake Mihomo (reports `tun.enable` true/false) and a fake privilege seam: enable success (Prepare once, start from copy, Active=true); TUN did not start -> prior runtime restored, LastFailure set, intent kept; Prepare declined/no-agent -> prior runtime untouched, Privilege no-agent; Prepare timeout does not block other commands or monitor batches; automatic start with stale privilege renders without TUN and reports requested/inactive without prompting; disable -> restart with selected binary then Revoke (revoke even if restart fails); authorize_tun re-runs Prepare without changing intent; leftover `clashpulse0` with no owned Mihomo -> Leftover=true.
- [ ] Implement; `go test -tags ci ./app`.

### Task 5: GUI (owner: GUITun)
Files: `ui/views.go`, `ui/tray.go`, tests, `localize/locale/gui.zh-CN.toml`.
- [ ] Failing tests: TUN section renders state from `core.TunSnapshot`; checkbox/selects send exact ConfigPatch; Authorize button visible unless privilege ready and sends `authorize_tun`; manual command `sudo setcap cap_net_admin+ep <PrivilegedPath>` shown only for no-agent (nosuid ignores file capabilities); tray toggle disabled with reason for unsupported-platform.
- [ ] Implement; `go test -tags ci ./ui`.

### Task 6: TUI (owner: TUITun)
Files: `tui/model.go`, `tui/keys.go`, `tui/keys.toml`, tests, `localize/locale/tui.en.toml`, `localize/locale/tui.zh-CN.toml`.
- [ ] Failing tests: settings row `TUN: requested X; active Y; privilege Z`; actions tun_enable, tun_disable, tun_authorize, edit_tun_stack, edit_tun_dns_mode send exact commands; invalid enum input sends nothing; keymap has defaults for all and no collisions; manual command shown only for no-agent.
- [ ] Implement; `go test -tags ci ./tui`.

### Task 7: Integration verification (owner: Main)
- [ ] `gofmt -l`, `go vet -tags ci ./...`, `go test -tags ci ./...`, race on app/mihomo/tun/ui/tui.
- [ ] Real-binary check: render a TUN config with ClashPulse's renderer and start the real Mihomo unprivileged; ClashPulse must report `requested, inactive` with `tun did not start` and keep the prior runtime.
- [ ] Whole-branch review.

## Review Focus

- A pacman upgrade between runs: next automatic start must not prompt and must not run the stale copy.
- The polkit dialog left open: other commands and monitor batches keep flowing.
- A profile whose DNS has `enable: false` with redir-host: TUN must still force `dns.enable: true`.
- Disabling TUN while Mihomo fails to restart: the copy is still revoked.
- System Proxy enabled together with TUN: both apply and both reset correctly on stop and failed start.
