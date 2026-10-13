# ClashPulse TUN mode design

Status: approved in conversation 2026-10-13; awaiting written-spec review.

## Goal

Let the user route all host traffic through the ClashPulse-owned Mihomo
process with Mihomo's TUN inbound on Linux, without giving up the product
invariants: one lifecycle owner, transactional configuration, capability
gating, private state, and no standing privilege the user did not ask for.

## Decisions

| Topic | Decision |
|---|---|
| Platforms | Linux only in v1. macOS and Windows show the setting disabled with an "unsupported on this platform" compatibility message. |
| Privilege | A private, TUN-scoped copy of the selected Mihomo binary carries `cap_net_admin`. Created on enable (one `pkexec setcap` prompt), deleted on disable. |
| Apply path | Render, validate, restart through the existing transaction. No live `PATCH /configs`. |
| DNS | Typed `dns_mode`, default `profile`: Clash Verge Rev behavior plus two fixes. |
| System proxy | Independent of TUN. Both may be on; both point at the same Mihomo. |
| Intent on failure | Never auto-disabled. The snapshot shows `requested, inactive` with a named reason. |

Out of scope for v1: macOS/Windows TUN, user-editable `strict-route`,
`auto-redirect`, route include/exclude lists, `mtu`, writing NetworkManager
or systemd-resolved configuration, a privileged helper service.

## 1. Privilege and lifecycle

### Package `tun`

New package owned by `app`; Linux implementation in `tun_linux.go`, an
unsupported-platform implementation in `tun_other.go`. It is the only code
that creates, inspects, or removes the privileged copy.

- Copy location: `<state>/tun/mihomo`; directory 0700, file 0700.
- `Prepare(ctx, source Capability) (Grant, error)`:
  1. Copy the selected (already inspected) binary to the private location
     through a temporary file and atomic rename.
  2. Record the source SHA-256 and inspected version in private state
     (`<state>/tun/grant.json`, 0600).
  3. Run `pkexec /usr/bin/setcap cap_net_admin+ep <copy>` as an explicit
     argv; no shell.
  4. Verify the `security.capability` xattr grants `cap_net_admin` in the
     permitted and effective sets.
- `Check(source Capability) Privilege` before every start and render:
  `ready`, `missing` (no copy or no capability), `stale` (source hash or
  version differs from the grant: a system Mihomo upgrade), `nosuid` (the
  copy's mount has `MS_NOSUID`, read via `statfs`; file capabilities would be
  ignored), `no-agent` (last `Prepare` failed because pkexec found no polkit
  authentication agent), `unsupported-platform`.
- `Revoke()`: delete the copy and grant record. Needs no privilege because
  the directory is the user's.

The hash binds the privileged file to the binary identity ClashPulse
inspected, so the capability model and the running binary cannot diverge
after a pacman upgrade. It is not tamper protection: the kernel already
removes file capabilities when an unprivileged process writes the file.

### pkexec handling

- `Prepare` runs in a cancellable background worker owned by `app`, never on
  the service loop or an IPC handler. It has a 2-minute timeout; timeout or
  cancellation of the polkit dialog becomes `privilege declined`.
- pkexec exit 126 (dialog dismissed) maps to `privilege declined`. Exit 127
  covers both "not authorized" and agent errors, so stderr containing
  "No authentication agent found" maps to `no-agent` and any other 127 to
  `privilege declined`. Clients then show the
  manual command `sudo setcap cap_net_admin+ep <PrivilegedPath>`; `Check`
  accepts a capability set that way exactly like a pkexec grant.
- ClashPulse never runs `sudo` itself and never stores a password.

### Enable flow

1. User intent `[tun] enabled = true` is written through the normal config
   writer; the full-set reload produces a change plan with a `tun` section.
2. `app` runs `Check`; if not `ready`, it runs `Prepare` in the worker.
3. Render the candidate configuration (section 2) and validate it with
   `<copy> -t -f <candidate>`.
4. Existing transaction: reset System Proxy if active, stop Mihomo, promote
   the candidate, start Mihomo from the privileged copy, wait for controller
   readiness, re-apply System Proxy if enabled.
5. Confirm TUN with a new typed controller method `TunEnabled(ctx)`
   (`GET /configs`, field `tun.enable`). Verified 2026-10-13 with Mihomo
   v1.19.31 started unprivileged with `tun.enable: true`: the process keeps
   running, logs `Start TUN listening error: configure tun interface:
   operation not permitted`, and `GET /configs` reports `tun.enable: false`.
   This read is therefore the only reliable signal.
6. Any failure in steps 2-5 restores the prior generated configuration and
   binary, restarts the prior runtime, and records `LastFailure`; intent stays
   saved and the snapshot reports `requested, inactive`.

### Automatic starts

Starts not initiated by a TUN command (login autostart, crash recovery,
configuration or subscription reloads) never prompt. If `Check` is not
`ready`, ClashPulse renders without TUN, starts the normally selected binary,
and reports `requested, inactive` with the privilege reason; the user
re-authorizes with `CommandAuthorizeTun`, which re-runs the enable flow.

### Disable flow

Render without TUN, run the transaction with the normally selected binary,
then `Revoke()`. The copy is revoked even if the restart fails; the restart
failure is reported separately. Disabling never prompts.

### Stopping Mihomo

`Process.Stop` changes from SIGKILL to SIGTERM, then SIGKILL after 3 seconds
(strictly shorter than the 5-second cleanup deadlines that wrap it),
so Mihomo can remove its routes, ip rules, and nftables state. At start, if
the `clashpulse0` device exists while no ClashPulse-owned Mihomo runs, the
snapshot reports leftover TUN state with a fixed message; ClashPulse does
not tear down network state it did not just create.

## 2. Generated configuration and DNS

### Capability

`mihomo.Capability` gains `SupportsTUN bool`, derived from the documented
version floor already used for geodata (1.19.0; every such version supports
the `gvisor`, `mixed`, and `system` stacks) and confirmed by validating the
candidate with the privileged copy. A missing capability is a compatibility
error naming `tun`.

### User intent (`config.toml`)

```toml
[tun]
enabled = false        # Linux only in this release
stack = "gvisor"       # gvisor | mixed | system
dns_mode = "profile"   # profile | fake-ip | redir-host
```

Strict decoding; unknown keys and invalid enums are rejected with file and
key. Defaults live in Go and are applied once before validation.

### Rendered `tun` block

```yaml
tun:
  enable: true
  stack: <stack>
  device: clashpulse0
  auto-route: true
  auto-detect-interface: true
  dns-hijack: [any:53]
  mtu: 1500
  strict-route: false
  auto-redirect: false
```

`device` is fixed so a user can match it in a NetworkManager
`unmanaged-devices` rule. A source profile that enables its own `tun` is
still rejected (`unsupported TUN mode`); a disabled profile `tun` block is
replaced. All existing forced settings remain (loopback controller and
secret, `allow-lan: false`, `bind-address: 127.0.0.1`, managed mixed port).

### DNS when TUN is on

- `dns.enable: true` is always forced so `dns-hijack` has a resolver to
  reach (fix over Verge, which leaves a disabled redir-host DNS untouched).
- `enhanced-mode`: `profile` keeps the profile's mode, or `fake-ip` when
  absent; `fake-ip` and `redir-host` force that mode.
- In fake-ip mode: `fake-ip-range: 198.18.0.1/16` when missing;
  `fake-ip-range6: 2001:2::0/64` when the profile enables IPv6 and none is
  set.
- In fake-ip mode with no profile `fake-ip-filter`, render
  `["*.lan", "*.local", "*.arpa", "time.*.com", "ntp.*.com", "+.pool.ntp.org"]`
  so LAN and NTP names keep real addresses (fix over Verge, which has no
  filter outside its opt-in DNS override).
- Unchanged: the loopback DNS listener (`127.0.0.1:1053` default), the
  managed `nameserver-policy` and its rule-providers, DNSCrypt endpoints.
- New validation: no resolver endpoint may fall inside the fake-ip range.
- Fail closed: TUN requires a non-empty `dns.nameserver`, and a non-empty
  `dns.default-nameserver` when any nameserver or fallback is a hostname.
  Otherwise Mihomo silently uses its built-in resolvers (doh.pub, 223.5.5.5,
  114.114.114.114) for every hijacked query, which AGENTS.md forbids.

When TUN is off, DNS renders exactly as today.

### Override report

`ExplainOverrides` and `core.ConfigOverrideSnapshot.Valid` gain `tun`,
`dns.enable`, `dns.enhanced-mode`, `dns.fake-ip-range`, `dns.fake-ip-range6`,
and `dns.fake-ip-filter`, reported only when the rendered value differs from
the profile.

## 3. Clients, errors, tests

### IPC (protocol version 6 -> 7)

- `ConfigPatch`: `TunEnabled *bool`, `TunStack *string`,
  `TunDNSMode *string`; enum validation; included in the empty-patch check.
- `CommandAuthorizeTun`: re-run `Prepare` without changing intent when
  `Check` is not `ready` (for example after a Mihomo upgrade); a no-op when
  it is, so a manual `sudo setcap` grant survives. Enqueued like every command.
- `core.TunSnapshot`: `Enabled`, `Active`, `Stack`, `DNSMode`, `Privilege`
  (`ready|missing|stale|nosuid|no-agent|unsupported-platform`),
  `PrivilegedPath` (non-secret; used to show the manual setcap command),
  `Device`, `LastFailure` (fixed public message), `Leftover bool`.
- `core.ActivationTunPrivilege` and `core.ActivationTunStart` stages with
  English and zh-CN messages.

### GUI

Settings gains a "TUN" section: enable checkbox, stack and DNS-mode selects,
a status line (`requested, inactive: Mihomo was updated, re-authorize`), an
"Authorize" button when privilege is not `ready`, and a copyable manual
command (`core.TunSnapshot.ManualGrantCommand`) only when privilege is
`no-agent`; on `nosuid` setcap cannot help because file capabilities are
ignored on that mount, so only the reason is shown. The tray gets a TUN toggle,
disabled with its reason on unsupported platforms. All actions are
non-blocking sends; no I/O on the Fyne thread.

### TUI

Settings row `TUN: requested X; active Y; privilege Z`; actions `tun_enable`,
`tun_disable`, `tun_authorize`, `edit_tun_stack`, `edit_tun_dns_mode` with
default keys in `keys.toml`; help derived from the keymap.

### Tests (fakes only; no real privilege)

- `tun`: copy, hash and staleness, `nosuid` detection through a fakeable
  `statfs`, pkexec outcomes (success, declined 126, no agent 127, timeout)
  via a fake `pkexec` on `PATH`, revoke.
- `mihomo`: render golden cases for each `dns_mode` against profile DNS
  shapes (none, fake-ip, redir-host, `enable: false`), filter fallback,
  resolver-in-fake-ip-range rejection, `TunEnabled` parsing, SIGTERM then
  SIGKILL on stop.
- `app`: fake Mihomo reporting `tun.enable` true or false; enable success,
  TUN-did-not-start rollback restoring the prior runtime, stale grant after a
  simulated upgrade, disable revoking the copy, `Prepare` timeout not
  blocking the service loop.
- `config`, `ipc`, `ui`, `tui`: strict keys and enums, patch bounds,
  snapshot rendering, key dispatch.

## Risks

- systemd-resolved authorizes per-link DNS changes by uid through polkit, not
  by capability. sing-tun (bundled in Mihomo; DNS mode `hijack` whenever
  `auto-route` is on, not configurable from Mihomo) runs `resolvectl domain`,
  `default-route`, and `dns` on every TUN start and `revert` on stop, so a
  non-root Mihomo raises three polkit prompts per start. ClashPulse starts the
  privileged copy with an empty `PATH`: sing-tun finds no `resolvectl` and
  skips those calls, so the only prompt is the one-time `setcap`. Mihomo runs
  no external command on Linux, and sing-tun's only other lookups serve
  `auto-redirect` (rendered `false`) and OpenWrt `fw4`. DNS still reaches
  Mihomo through `auto-route` plus `dns-hijack`; resolved keeps its own
  upstream view in `resolvectl status`.
- NetworkManager may manage the TUN link and overwrite its DNS. The fixed
  `clashpulse0` name lets the user add an `unmanaged-devices` rule; ClashPulse
  never writes NetworkManager configuration.
- While TUN is enabled, any process running as the user can execute the
  privileged copy. This is the accepted trust boundary; disabling TUN removes
  it.
