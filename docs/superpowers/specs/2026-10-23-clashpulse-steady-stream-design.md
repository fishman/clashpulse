# Steady-stream switching design

Date: 2026-10-23. Status: design approved in chat; spec awaiting review.

## Goal

The monitor keeps choosing the fastest node by default. A new opt-in policy,
`steady_stream`, prefers nodes that complete real HTTPS downloads without
interruption (reset, stall, truncation, TLS failure). Interruptions are
measured by pulling a small file through each node, through Mihomo.

User decisions: Cloudflare speed endpoint, 256 KiB pulls, the selected node
plus the 3 fastest, completion first and then speed. Policy names: "fastest"
is the existing `lowest_latency` value (unchanged so existing configs load);
the new value is `steady_stream`. `lowest_latency` stays the default. Nothing
is downloaded unless `steady_stream` is selected.

## Verified against Mihomo v1.19.31 (installed binary)

Checked with `mihomo -t` and a running instance plus curl:

- A `listeners:` entry `{type: mixed, listen: 127.0.0.1, port: N, proxy:
  clashpulse-stream-probe}` validates and pins its traffic to that group,
  independent of rules and of the user's selection.
- Changing the probe group's member with controller `PUT
  /proxies/clashpulse-stream-probe` changes the outbound of the listener only.
- `users: [{username, password}]` on that listener refuses connections
  without or with wrong credentials (HTTP and SOCKS); correct credentials
  pull the full 262144 bytes.
- `https://speed.cloudflare.com/__down?bytes=262144` returns
  `Content-Length: 262144`.
- The probe group appears in `GET /proxies`.

These checks become the capability gate. Capability inspection runs the
selected binary's own validation (`-t`) on a fixed private fixture: one
`select` group and one authenticated mixed listener pinned to it. It records
`pinned-listener` only when that validation succeeds, so the gate follows the
binary, not its version string (v1.19.31 verified). Selecting `steady_stream`
without it is a named compatibility error naming `monitor.switch_policy`;
there is no fallback.

## Approach

One hidden `select` group plus one authenticated loopback listener pinned to
it. Rejected: one listener per node (needs a port per node, and the config
must be rebuilt and Mihomo reloaded whenever the node list changes); pulling
only through the selected node (cannot show a replacement is steadier, which
AGENTS.md requires before switching).

## Components

### Generated configuration (`mihomo`)

Rendered only when `switch_policy = "steady_stream"`:

- Group `clashpulse-stream-probe`, `type: select`, members: every proxy of
  every probed managed group, deduplicated, in profile order.
- Listener `clashpulse-stream-probe`, `type: mixed`, `listen: 127.0.0.1`,
  `port`: a free loopback port chosen at each start, `proxy:
  clashpulse-stream-probe`, `users`: one random username and password held
  in memory like the controller secret, never logged or sent over IPC.
- The render currently rejects `listeners`; it rejects a profile's own
  `listeners` as before and only emits this managed one. A profile that
  defines a proxy, group, or listener named `clashpulse-stream-probe` is
  rejected with a named error rather than overwritten. The override report
  names the added group and listener.
- Port conflict: a start that fails because the port is taken retries once
  with a new port, then fails transactionally like `mixed_port`.

The probe group is filtered out of Groups/Proxies snapshots, tray, GUI, TUI,
monitor rosters, and the dial-failure log watch.

### Stream prober (`mihomo` adapter + `monitor`)

`Controller.StreamPull(ctx, proxy, url)`: select the proxy in the probe
group, then GET the URL through the listener with TLS verification on. Read
the body into `io.Discard` in a loop with a per-read stall deadline; no fixed
client timeout on the body. Returns bytes, time to first byte, duration, and
an outcome:

| Outcome | Meaning |
|---|---|
| `success` | exactly `Content-Length` bytes received |
| `reset` | connection error mid-body |
| `stall` | no bytes for 5 s |
| `truncated` | EOF before `Content-Length` |
| `tls` | handshake or certificate failure |
| `timeout` | 30 s total exceeded |
| `error` | any other failure, including a missing `Content-Length` |

Pulls are serialized (one probe group); see Scheduling for the round cap and
interval floor.

### Scheduling (`monitor.Scheduler`)

One global stream deadline in the existing timer loop, because the single
probe listener serializes every pull; no extra goroutine per node or group.
A round's pull set is the union over automated groups of the selected node
plus the 3 healthy nodes with the lowest median latency, deduplicated by node
name and capped at 8 pulls (selected nodes first, then candidates in group
order). Worst case 8 x 30 s = 4 min, so `stream_interval` must be at least
`4m`. Cancellable; stopped when no group is automated under `steady_stream`.
Data: about 12 MB/hour for one group, at most 24 MB/hour at the cap.

Stream samples are keyed by node name, so a node shared by two groups is
pulled once and its samples serve both. Each node keeps its last
`window_size` (5) stream samples regardless of its current rank, so a node
that moves in and out of the top 3 still accumulates evidence. Samples older
than 6 stream intervals are dropped.

### Decision (`monitor.Decide`, policy `steady_stream`)

Latency samples keep running as today. A switch happens only when:

1. the selected node interrupted at least `max_failures` (2) of its last
   `window_size` (5) pulls, and
2. a candidate has at least `min_candidate_samples` (3) completed pulls,
   strictly fewer interruptions than the selected node, and its latest pull
   completed.

Ranking among eligible candidates: fewest interruptions, then higher median
throughput, then lower median latency. Cooldown, one switch at a time, the
all-candidates-failed circuit breaker, and manual override are unchanged.
The switch event carries the reason, old and new node, and the pull evidence.

### Settings, IPC, clients

`config.toml [monitor]`, strict TOML, exposed in GUI Settings > Monitor and
the TUI:

- `switch_policy`: `lowest_latency` | `failover` | `steady_stream`
- `stream_url`: HTTPS only, no credentials; default
  `https://speed.cloudflare.com/__down?bytes=262144`
- `stream_interval`: default `5m`, at least `4m`

Snapshots add per node: stream completion (`completed/total`) and median
throughput. Proxies views in GUI and TUI show them next to latency. Stream
outcomes go to the activity log with masked node names. IPC protocol version
increments.

## Default configuration text (`examples/config.toml`, first-run seed)

```toml
switch_policy = "lowest_latency" # "lowest_latency" (fastest): the lowest median latency wins. "failover": move only when the current node is unhealthy. "steady_stream": move away from a node whose HTTPS downloads break.
stream_url = "https://speed.cloudflare.com/__down?bytes=262144" # steady_stream only: a 256 KiB HTTPS file pulled through each probed node.
stream_interval = "5m" # steady_stream only: pulls the selected node and the 3 fastest others, about 12 MB/hour per automated group.
```

## README text

### Choosing a node automatically

ClashPulse measures every node in an automated group and switches by
`switch_policy` in `config.toml [monitor]` (GUI Settings > Monitor, TUI
Settings):

- **Fastest** (`lowest_latency`, default): every round, each node answers a
  tiny HTTP request through Mihomo. The group moves to a node whose median
  latency is lower by at least `min_improvement`. Good for browsing; a node
  can be fast to answer yet drop long downloads.
- **Failover** (`failover`): stays on the current node until it fails or
  exceeds `threshold` for `consecutive_bad_samples` rounds.
- **Steady stream** (`steady_stream`): every `stream_interval`, ClashPulse
  downloads a 256 KiB HTTPS file (`stream_url`) through the current node and
  the 3 fastest others, one at a time, and records whether each download
  finished, reset, stalled, or was cut short. It leaves a node only after 2
  of its last 5 downloads broke, for a node with fewer broken downloads and
  at least 3 completed ones; ties go to the faster download. Good for
  streams, calls, and large transfers. Costs about 12 MB per hour for one
  automated group, at most 24 MB per hour; a node shared by groups is
  downloaded once.

Steady stream pulls through a private loopback listener that only
ClashPulse can use; your selected node and rules are untouched. It requires
a Mihomo whose capability check confirms pinned listeners (v1.19.31
verified). Every policy honors cooldown, manual selection, and the all-nodes-
failed stop.

## Testing

- `monitor` policy: no switch on one interruption; selected node with 2/5
  interruptions switches to an eligible steadier node; tie broken by
  throughput; candidate with a broken latest pull ineligible; rank rotation
  keeps evidence; cooldown, all-failed, manual override.
- Stream pull: local TLS server that resets mid-body, stalls, truncates,
  omits `Content-Length`, presents a bad certificate, or succeeds.
- Render: probe group and listener only under `steady_stream`, profile
  `listeners` still rejected, probe group hidden from snapshots and rosters.
- Config: strict keys, HTTPS-only `stream_url`, interval floor, capability
  error without `pinned-listener`.
- End to end with real Mihomo and real HTTPS pulls from Cloudflare: two
  nodes, `DIRECT` and a local throwaway proxy that cuts each connection after
  about 64 KiB. Expect recorded interruptions on the flaky node and a switch
  to `DIRECT` with evidence.
