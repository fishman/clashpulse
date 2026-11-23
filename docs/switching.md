---
layout: default
title: Automatic switching
nav_order: 5
---

# Automatic switching

ClashPulse switches only managed `select` groups you opt in: GUI Proxies >
Enable group automation, TUI Proxies `a`, or `automated_groups`. Picking a
node by hand removes the opt-in.

Mihomo's `url-test` and `fallback` groups belong to Mihomo. ClashPulse never
probes or switches them; `url_test_interval` and `url_test_tolerance` only set
their delay settings.

## Policies

Set `switch_policy` in `[monitor]`, GUI Settings > Monitor, or TUI Settings
`y`.

Fastest (`lowest_latency`, default) moves to a node whose median latency is
lower by at least `min_improvement`. A fast answer does not mean a node holds
long downloads.

Failover (`failover`) stays put until the current node fails or exceeds
`threshold` for `consecutive_bad_samples` rounds.

Steady stream (`steady_stream`) downloads a 256 KiB HTTPS file every
`stream_interval` through the current node and the 3 fastest others. It
leaves a node after 2 of its last 5 downloads broke (reset, stalled, or cut
short), for one with fewer breaks and at least 3 completed downloads. That is
about 12 MB per hour per group, at most 24 MB. A node that stops answering
latency probes still leaves under the Failover rule. It needs a Mihomo with
pinned listener support (verified with v1.19.31).

All policies respect cooldown and manual selection, and stop switching when
every candidate fails. Probes go through Mihomo's delay endpoint, the same
path as your traffic; a timeout or error is a failed sample.

## Monitor settings

![GUI Monitor probe policy dialog with Steady stream selected](assets/screenshots/gui-monitor-policy.png)

| key | default | meaning | TUI |
| --- | --- | --- | --- |
| `enabled` | `true` | measure managed groups | `m` / `n` |
| `test_url` | `http://cp.cloudflare.com/generate_204` | latency probe URL | `u` |
| `switch_policy` | `"lowest_latency"` | or `failover`, `steady_stream` | `y` |
| `stream_url` | `https://speed.cloudflare.com/__down?bytes=262144` | Steady stream file | `O` |
| `stream_interval` | `"5m"` | between download rounds | `I` |
| `interval` | `"1m"` | between latency rounds; a switch needs three | `i` |
| `timeout` | `"5s"` | per probe | `o` |
| `concurrency` | `3` | parallel probes, at most 64 | `c` |
| `threshold` | `"800ms"` | latency that counts as bad | `h` |
| `alert_threshold` | `"250ms"` | notify if every candidate is slower | `t` |
| `window_size` | `5` | samples kept per node (2-20) | `W` |
| `consecutive_bad_samples` | `3` | bad samples before failover | `s` |
| `min_candidate_samples` | `3` | successes a candidate needs | `C` |
| `max_failures` | `2` | failures that mark a node unstable | `f` |
| `min_improvement` | `"100ms"` | required median gain | `p` |
| `cooldown` | `"5m"` | between switches of one group | `z` |
| `jitter` | `"10s"` | spread within a round | `w` |
| `automated_groups` | `[]` | groups switched automatically | Proxies `a` |

`max_failures` Mihomo dial failures in a probed group also trigger an early
probe, at most every 15s.

The default `test_url` is plain HTTP and can be intercepted. Use HTTPS if
probe integrity matters. TUI `T` or the Monitor section sends a test
notification.

## Service checks

Some services refuse a node by region; a fast node can still be refused.
A service check reads a small response through a node and reports `usable`,
`blocked`, or `error`, with the detected region. Nothing is enabled by
default. Configure `[[service]]` in `config.toml`, GUI Settings > Services, or
TUI Settings (`Enter` on a service row edits it).

Each enabled service gets a `select` group `ClashPulse - <Name>` holding every
proxy of the profile, and `DOMAIN-SUFFIX` rules sending its domains there
ahead of the profile's own rules. `domains_resource` replaces those domains
with one `RULE-SET` rule for an enabled domain rule-set resource. A profile
group already named `ClashPulse - <Name>` is an error; ClashPulse never
renames it.

| Service | ID | Check | Usable when |
| --- | --- | --- | --- |
| ChatGPT | `chatgpt` | `https://chat.openai.com/cdn-cgi/trace`, `https://api.openai.com/compliance/cookie_requirements` | second answer is 2xx without `unsupported_country` |
| Claude | `claude` | `https://claude.ai/cdn-cgi/trace` | region not blocked by Claude |
| Gemini | `gemini` | the YouTube read | Google's country is not blocked by Gemini |
| YouTube Premium | `youtube` | `https://www.youtube.com/premium?hl=en` (first 96 KiB) | a country is found and no "not available in your region" notice |
| Netflix | `netflix` | three `https://www.netflix.com/title/...` pages, headers only | a title is available and none is refused |
| Spotify | `spotify` | `https://www.spotify.com/api/content/v1/country-selector?platform=web&format=json` | 2xx without "not available in your country" |

Every check uses HTTPS with verified TLS, a 15 s limit, redirects only within
the service's own hosts, and a fresh in-memory cookie jar. Only the outcome and
region are kept. All six services at the default interval use about 0.3 MB an
hour.

Limitation: if YouTube places its region notice beyond the first 96 KiB, the
check relies on the country alone and may report `usable`.

| key | default | meaning | TUI |
| --- | --- | --- | --- |
| `id` | | `chatgpt`, `claude`, `gemini`, `youtube`, `netflix`, `spotify`; each at most once | |
| `enabled` | `false` | render the group and check it | `E` |
| `regions` | `[]` | preferred two-letter country codes; empty: any region not blocked | `Enter` |
| `check_interval` | `"30m"` | between checks of the selected node; at least `5m` | `Enter` |
| `failures` | `2` | consecutive failed checks before a search (1-10) | `Enter` |
| `automatic` | `false` | let ClashPulse switch this service's group | `G` |
| `domains_resource` | `""` | domain rule-set resource replacing the built-in domains | `Enter` |

TUI `K` (GUI Check now) checks the selected node immediately. A check also
runs when the group's node changes.

Service switching is separate from group automation. The service groups are
never latency-probed, and `automated_groups` cannot name them. With
`automatic = true`, a group moves after `failures` consecutive failed checks
of its node: `blocked` counts; `error` counts only when the node's latest
latency probe also failed. The search checks up to 8 other nodes, 4 at a
time in latency order, and picks the fastest usable one in `regions`. It
honors `cooldown` from `[monitor]` and one switch at a time. If no candidate
is usable, the node stays and the next search waits for the next check.
Picking a node by hand in a service group turns `automatic` off for that
service.

Checks and steady-stream downloads share 4 private loopback probe listeners,
so services need a Mihomo with pinned listener support (verified with
v1.19.31).
