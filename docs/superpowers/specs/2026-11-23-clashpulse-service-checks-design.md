# Service checks design

Date: 2026-11-23. Status: design approved in chat; spec awaiting review.

## Goal

Some services refuse a node by region (ChatGPT, Claude, Gemini, YouTube
Premium, Netflix, Spotify). Latency probes cannot see that: a node can be
fast and still be refused. ClashPulse routes each enabled service through its
own managed node group, checks that the group's node is usable for the
service, and moves the group to another node when the check keeps failing.

User decisions (2026-11-23):

- A check reads a small response body through Mihomo, not only a status, so
  region blocks behind Cloudflare are visible.
- Each enabled service gets its own managed group and routing rules.
- First version: ChatGPT, Claude, Gemini, YouTube Premium, Netflix, Spotify.
- The selected node of each service group is checked every 30 minutes.
- Group members are all proxies of the active profile.
- Gemini uses Google's country from the YouTube read, not Gemini's own page.

Nothing is enabled by default. A service with `enabled = false` renders
nothing and downloads nothing.

## Measured facts (2026-11-23, from this host, not through a proxy)

| URL | Size | Signal position |
|---|---|---|
| `https://gemini.google.com` | 917,028 bytes | region marker `,2,1,200,"` at byte 790,387 |
| `https://www.youtube.com/premium?hl=en` (after redirects) | 1,018,981 bytes | `"GL":"` at byte 59,683; `country-code` at 836,211 |

The Cloudflare trace endpoints (`/cdn-cgi/trace`) return a few hundred bytes
of `key=value` lines including `loc=`. The reference client
(`references/clash-verge-rev/crates/clash-verge-media-unlock`) supplied the
check URLs and blocked lists below; ClashPulse reuses the signals, not the
code.

Limitation: the YouTube "not available in your region" phrase could not be
observed from this host. If a blocked page places it beyond the read cap,
the check relies on the country field alone and reports `usable`. This is
recorded in the UI help text for YouTube.

## Check definitions

A check is a built-in Go value in a new package `services`. Users cannot add
check code; they enable, configure regions, and replace routing domains.

```go
type Rule string // "trace_loc", "body_marker", "status_pair", "json_country"

type Definition struct {
	ID       string   // "chatgpt", stable, used in config and group names
	Name     string   // display name, e.g. "ChatGPT"
	Rule     Rule
	URLs     []string // fixed HTTPS URLs, never user input
	Blocked  []string // ISO 3166-1 alpha-2 country codes refused by the service
	Markers  []string // body phrases meaning "refused here"
	MaxBytes int64    // read cap; reading stops early once the signal is found
	Domains  []string // default DOMAIN-SUFFIX routing set
}

type Result struct {
	Service  string
	Proxy    string
	Outcome  Outcome // usable, blocked, error
	Region   string  // detected alpha-2 code, "" when unknown
	At       time.Time
}
```

| ID | Rule | Request(s) | Usable when | Cap |
|---|---|---|---|---|
| `chatgpt` | `trace_loc` + marker | `https://chat.openai.com/cdn-cgi/trace`, then `https://api.openai.com/compliance/cookie_requirements` | `loc` not blocked and the second body lacks `unsupported_country` | 4 KiB each |
| `claude` | `trace_loc` | `https://claude.ai/cdn-cgi/trace` | `loc` not in AF BY CN CU HK IR KP MO RU SY | 4 KiB |
| `gemini` | Google country | shares the YouTube read | `GL` not in CN RU BY CU IR KP SY HK MO | (YouTube read) |
| `youtube` | `body_marker` | `https://www.youtube.com/premium?hl=en` | `GL` found and no `premium is not available in your region` marker in the read prefix | 96 KiB |
| `netflix` | `status_pair` | `https://www.netflix.com/title/81280792` and `https://www.netflix.com/title/70143836`, then `https://www.netflix.com/title/80018499` for the region; no redirects followed | either title not 404 and neither 403; region from the third response's `Location` path segment (`/us-en/...` gives `US`) | headers only |
| `spotify` | `json_country` | `https://www.spotify.com/api/content/v1/country-selector?platform=web&format=json` | status 2xx, body lacks `not available in your country`; region from `countryCode` | 16 KiB |

The Netflix title IDs are the ones the reference client probes
(`netflix.rs:39` and `:74`).

Outcome rules:

- `blocked`: the service refused the node (blocked country, refusal marker,
  403, or both Netflix titles 404).
- `error`: timeout, TLS failure, connection reset, unexpected status, or a
  body without the expected signal. `error` is not region evidence.
- A blocked list match uses the detected region; an empty region with no
  marker is `error`, never `usable`.

Every request: GET, HTTPS only, TLS 1.2+, no cookies, `DisableKeepAlives`,
a 10 s total timeout, the read cap above, and context cancellation. Region
codes and outcomes are the only parsed values that leave the `services`
package; bodies are discarded.

## Routing

For each enabled service, `mihomo.Render` adds:

- a `select` group named `ClashPulse · <Name>` whose members are every proxy
  of the profile, provider nodes included (`include-all: true`, as the
  existing probe group renders),
- `DOMAIN-SUFFIX,<domain>,ClashPulse · <Name>` rules for the service's
  domains, placed ahead of the profile's rules and after ClashPulse's own
  managed filter rules, in service-definition order.

Default domain sets:

| Service | Domains |
|---|---|
| ChatGPT | `openai.com`, `chatgpt.com`, `oaistatic.com`, `oaiusercontent.com` |
| Claude | `claude.ai`, `anthropic.com`, `claudeusercontent.com` |
| Gemini | `gemini.google.com`, `bard.google.com`, `generativelanguage.googleapis.com` |
| YouTube | `youtube.com`, `youtu.be`, `googlevideo.com`, `ytimg.com`, `youtubei.googleapis.com` |
| Netflix | `netflix.com`, `netflix.net`, `nflxvideo.net`, `nflximg.net`, `nflxext.com`, `nflxso.net` |
| Spotify | `spotify.com`, `scdn.co`, `spotifycdn.com`, `spoti.fi` |

A service may instead name a `domains_resource`: the ID of an enabled
`resources.toml` rule-set resource. Rendering then emits one
`RULE-SET,<provider>,ClashPulse · <Name>` rule from the existing registry
instead of the default suffixes. An unknown, disabled, or non-domain resource
is a load-time validation error.

A profile group already named `ClashPulse · <Name>` is a render error naming
the service; ClashPulse never renames silently. The `proxy-groups` and
`rules` entries are already listed in `core.ConfigOverrideKeys`, so the
Activity "Generated config changes" tab reports them.

The group selection persists across restarts through Mihomo's cache file like
any select group. A missing selection starts on Mihomo's first member, and
the first scheduled check follows immediately.

## Probing

Checks run through the existing private loopback probe listener and its
pinned group (`clashpulse-stream-probe`). Today the listener renders only for
`switch_policy = "steady_stream"`; it now renders when steady stream **or**
any service is enabled. Its credential handling is unchanged.

Selecting a node on the probe group and reading through the listener is not
concurrent-safe, so the monitor runs steady-stream pulls and service checks
on one probe worker, one request sequence at a time.

Scheduling, owned by the existing `monitor.Scheduler` timer:

- Each enabled service has a next-check deadline (`check_interval`, default
  30 minutes, plus up to 10% jitter) merged into the one timer through
  `nextDeadline`. Disabled services have no deadline; with no enabled service
  and no steady stream, no probe work is scheduled.
- A due round checks only the node currently selected in that service's
  group.
- A check also becomes due immediately when the group's node changes (by
  ClashPulse or by the user) and on "Check now".
- Gemini and YouTube share one YouTube read per node when both are due.

Traffic estimate: ChatGPT and Claude about 1 KB, Spotify up to 16 KB,
Netflix headers only, YouTube and Gemini up to 96 KB together, plus TLS
handshakes. All six services at two checks an hour: about 0.3 MB an hour.

## Switching

Per service group:

1. The selected node fails `failures` consecutive checks (default 2). A
   `blocked` result counts. An `error` counts only when that node's latest
   latency probe also failed; otherwise an `error` is recorded and retried at
   the next deadline.
2. Candidates: every other member whose latest check for this service is not
   `blocked`, filtered to the service's `regions` when set (a node with no
   detected region for this service qualifies only after it is checked).
   Ordered by median latency from the existing delay history; nodes without
   latency history go last. At most 8 candidates per search.
3. Candidates are checked one at a time on the probe worker. The first
   `usable` candidate in the preferred regions wins.
4. The app selects it through the controller with the existing guard rules:
   one switch in flight per group, the policy cooldown, and no switch while
   the group has a manual override.
5. If every candidate fails, the group keeps its node, a circuit-breaker
   diagnostic is recorded, and the next search waits for the next deadline.

Each switch emits a diagnostic with old node, new node, outcome, and region.
Node names are masked through the existing label path before display.

A manual node pick in a service group (GUI, TUI, or Mihomo dashboard
observed through the controller) disables automation for that service until
the user re-enables it, matching existing group automation.

## Configuration

`config.toml`, strict decoding, one table per configured service:

```toml
[[services]]
id = "chatgpt"            # one of the built-in IDs; each at most once
enabled = true
regions = ["US", "JP"]    # optional; empty means any region that is not blocked
check_interval = "30m"    # optional; default 30m, minimum 5m
failures = 2              # optional; consecutive failed checks before a search
automatic = true          # optional; false checks and reports but never switches
# domains_resource = "geosite-openai"  # optional resources.toml rule-set ID
```

Validation: unknown ID, duplicate ID, region not two upper-case letters,
interval below 5m, `failures` outside 1..10, and a dangling or wrong-format
`domains_resource` are errors with file and key. `examples/config.toml`
gains an annotated commented-out entry. Services not listed are disabled.

## IPC and clients

`core.Snapshot` gains `Services []ServiceSnapshot`:

```go
type ServiceSnapshot struct {
	ID, Name      string
	Enabled       bool
	Automatic     bool
	Regions       []string
	GroupID       string // opaque group ID of the managed group
	Selected      string // masked node label
	Outcome       string // usable, blocked, error, "" before the first check
	Region        string
	LastCheck     int64
	NextCheck     int64
	LastSwitch    string // fixed reason code, never free text
}
```

Commands: `set_service` (enabled, regions, interval, failures, automatic,
domains_resource; written through the normal config writer and reload) and
`check_service` (enqueue a check now). `validateSnapshot` bounds the new
slice. Protocol version 14.

GUI: Settings → Services, one row per built-in service: Enable, Automatic,
regions, current node, outcome with region, last/next check, "Check now",
and an edit dialog for interval, failures, and routing domains. TUI: the
same rows and actions through the declarative keymap.

## Testing

- `services`: `httptest` TLS servers for each rule kind: usable, blocked by
  region, blocked by marker, Netflix originals-only and 403, missing signal is
  `error`, cap stops reading, cancellation, no redirect leak of cookies.
  Fixtures use reserved names and synthetic bodies only.
- `monitor`: only the selected node is checked on a due round; node change
  and "Check now" force a check; candidate search caps at 8 and honours
  regions; cooldown, manual override, circuit breaker; probe worker never
  overlaps a stream pull and a service check; disabled services schedule
  nothing.
- `mihomo`: deterministic group and rule rendering, `domains_resource`
  rendering, name collision error, listener rendered for services without
  steady stream; validated with the selected Mihomo binary in the existing
  render test path.
- `config`: strictness and validation cases above.
- `ipc`: the new snapshot slice and both commands validate and round-trip.

## Out of scope

Disney+, TikTok, Prime Video, and other services; user-defined checks; body
checks through the delay endpoint; per-service member groups; DNS changes
for service domains.
