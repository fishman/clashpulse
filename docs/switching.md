---
layout: default
title: Automatic switching
nav_order: 5
---

# Automatic switching

ClashPulse measures every node in an automated group and switches by
`switch_policy` in `config.toml [monitor]` (GUI Settings > Monitor, TUI
Settings, `y` cycles the policy).

## Which groups are automated

Automation applies to managed `select` groups you opt in: GUI Proxies >
**Enable group automation**, TUI Proxies `a`, or their opaque group IDs in
`automated_groups`. Selecting a node by hand removes the opt-in for that
group until you enable it again.

Mihomo's own `url-test` and `fallback` groups are left to Mihomo: it runs
their health checks and owns their choice. ClashPulse never probes or
switches them; `url_test_interval` and `url_test_tolerance` in `[mihomo]`
only set their delay settings in the generated profile.

## Choosing a node automatically

- **Fastest** (`lowest_latency`, default): every round, each node answers a
  tiny HTTP request through Mihomo. The group moves to a node whose median
  latency is lower by at least `min_improvement`. Good for browsing; a node
  can be fast to answer yet drop long downloads.
- **Failover** (`failover`): stays on the current node until it fails or
  exceeds `threshold` for `consecutive_bad_samples` rounds.
- **Steady stream** (`steady_stream`): every `stream_interval`, ClashPulse
  downloads a 256 KiB HTTPS file (`stream_url`) through the current node
  and the 3 fastest others, one at a time, and records whether each
  download finished, reset, stalled, or was cut short. It leaves a node only
  after 2 of its last 5 downloads broke, for a node with fewer broken
  downloads and at least 3 completed ones; ties go to the faster download.
  Good for streams, calls, and large transfers. Costs about 12 MB per hour
  for one automated group, at most 24 MB per hour; a node shared by groups
  is downloaded once. A node that stops answering latency probes still
  leaves under the Failover rule, without waiting for a download round.

Every policy honors cooldown, manual selection, and the all-nodes-failed
stop: when every candidate fails, ClashPulse stops switching instead of
picking a dead node.

Latency is measured through Mihomo's controller delay endpoint, so a probe
takes the same path as your traffic. A timeout or error is recorded as a
failed sample, never as a good latency.

## Steady stream downloads

Steady stream pulls through a private loopback listener that only
ClashPulse can use; your selected node and rules are untouched. It requires
a Mihomo whose capability check confirms pinned listeners (v1.19.31
verified).

The Proxies page **Probe downloads** button (TUI Proxies `d`) runs a
download round immediately. The button is enabled only while a connected
service runs the Steady stream policy.

## Monitor settings

Defaults from `examples/config.toml`; each is editable in GUI Settings >
Monitor and on a TUI Settings key.

![GUI Monitor probe policy dialog with Steady stream selected](assets/screenshots/gui-monitor-policy.png)

| key | default | meaning | TUI key |
| --- | --- | --- | --- |
| `enabled` | `true` | every managed `select` group is measured; manual probes need no opt-in | `m` / `n` |
| `test_url` | `http://cp.cloudflare.com/generate_204` | latency probe URL | `u` |
| `switch_policy` | `"lowest_latency"` | `lowest_latency`, `failover`, or `steady_stream` | `y` |
| `stream_url` | `https://speed.cloudflare.com/__down?bytes=262144` | steady_stream only: the 256 KiB HTTPS file | `O` |
| `stream_interval` | `"5m"` | steady_stream only: time between download rounds | `I` |
| `interval` | `"1m"` | time between latency rounds; three rounds are needed before a switch | `i` |
| `timeout` | `"5s"` | per-probe timeout | `o` |
| `concurrency` | `3` | concurrent probes; hard upper bound 64 | `c` |
| `threshold` | `"800ms"` | selected-node latency above which it counts as bad | `h` |
| `alert_threshold` | `"250ms"` | notify only if all measured candidates exceed this | `t` |
| `window_size` | `5` | probes kept per node (2-20); median, jitter, and failures shown per node cover this window | `W` |
| `consecutive_bad_samples` | `3` | bad probes of the selected node before failover; at most `window_size` | `s` |
| `min_candidate_samples` | `3` | successful probes a candidate needs in the window; 2 to `window_size` | `C` |
| `max_failures` | `2` | failed probes in the window that mark the selected node unstable; 1 to `window_size` | `f` |
| `min_improvement` | `"100ms"` | how much faster a candidate's median must be | `p` |
| `cooldown` | `"5m"` | minimum time between switches of one group | `z` |
| `jitter` | `"10s"` | spreads each round so probes do not fire at once | `w` |
| `automated_groups` | `[]` | opaque group IDs switched automatically | Proxies `a` |

`max_failures` Mihomo dial failures in a probed group also trigger an early
probe of it, at most every 15s.

The default `test_url` is plain HTTP, as in Clash Verge. Plain HTTP can be
intercepted: latency is a routing signal, never an integrity check. Use an
HTTPS URL if probe integrity matters.

GUI Settings > Monitor and TUI Settings `T` send a test notification so you
can check that alerts reach your desktop.
