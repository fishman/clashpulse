---
layout: default
title: Configuration
nav_order: 4
---

# Configuration

Settings live in four TOML files, one purpose each. There is no inheritance
or cross-file precedence. Snapshots, probe history, and controller state are
private state, not TOML.

| OS | config directory |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/clashpulse`, default `~/.config/clashpulse` |
| macOS | `~/Library/Application Support/clashpulse` |
| Windows | `%AppData%\clashpulse` |

Private state (snapshots, generated Mihomo config, cached resources, locks)
lives in `$XDG_STATE_HOME/clashpulse`, default `~/.local/state/clashpulse`.

First start seeds the files from the examples below.

| file | owns |
| --- | --- |
| `config.toml` | Mihomo binary, mixed port, and mode; monitor and switch policy; DNS listener; system proxy; TUN |
| `subscriptions.toml` | subscriptions: source URL, refresh interval, timeout, download route, TLS exceptions |
| `resources.toml` | data resources (GeoIP, GeoSite, MMDB, rule-sets), DNS resolver sets, and DNS routes |
| `filters.toml` | filter lists: a rule-set resource routed to a group of the active profile |

A missing file is an empty section; other files in the directory are ignored.
The GUI and TUI expose every field in these files.

## Validation and reload

These are load errors: unknown keys, duplicate IDs, invalid enum values,
malformed URLs, unsupported binary or resource references, and references to
a resource that does not exist. Defaults live in the program, so your files
hold only what you set.

ClashPulse watches the config directory, so editors that save by atomic
rename work. After a short debounce it loads and validates the whole file
set. Any error keeps the running configuration and reports the file and key
to the GUI, TUI, and IPC clients. Mihomo reloads only when its generated
config actually changed. Overview > Reload configuration (TUI `r`) re-reads
the files on demand.

GUI and TUI edits go through the same path: a private temporary file, an
atomic replace, then the full reload.

## config.toml

```toml
# ClashPulse user intent. Save all four files together in the private config dir.
# Defaults: bundled Mihomo in macOS/Windows release packages, system Mihomo otherwise; enabled health monitoring, disabled system proxy and TUN, loopback DNS.
[mihomo]
# binary = "system" # Default: "bundled" in macOS/Windows release packages, otherwise "system"; or an executable path.
mixed_port = 7899 # HTTP, HTTPS, and SOCKS on 127.0.0.1; 1024-65535. Start fails if the port is taken.
mode = "rule" # "global" sends everything through the GLOBAL group; "direct" bypasses all proxies. Switched live.
# Delay settings for the url-test and fallback groups of the generated profile.
# Unset keeps whatever each group already declares. This is Mihomo's own health
# check, which owns those groups; ClashPulse never probes or switches them.
# url_test_interval = "5m"
# url_test_tolerance = "50ms" # Switch only when a node is at least this much faster.

[monitor]
enabled = true # Every managed select group is measured; manual probes need no opt-in.
test_url = "http://cp.cloudflare.com/generate_204" # Plain HTTP probes can be intercepted; use HTTPS for probe integrity.
switch_policy = "lowest_latency" # "lowest_latency" (fastest): the lowest median latency wins. "failover": move only when the current node is unhealthy. "steady_stream": move away from a node whose HTTPS downloads break.
stream_url = "https://speed.cloudflare.com/__down?bytes=262144" # steady_stream only: a 256 KiB HTTPS file pulled through each probed node.
stream_interval = "5m" # steady_stream only: pulls the selected node and the 3 fastest others, about 12 MB/hour per automated group.
interval = "1m" # Three rounds of probes are needed before a switch, so a shorter interval shortens the reaction time.
timeout = "5s"
concurrency = 3 # Hard upper bound: 64 concurrent controller probes.
threshold = "800ms" # Selected proxy latency threshold for automatic switching.
alert_threshold = "250ms" # Notify only if all measured candidates exceed this.
window_size = 5 # Probes kept per node (2-20); the median, jitter, and failure count shown per node cover this window.
consecutive_bad_samples = 3 # Bad probes of the selected node before failover; at most window_size.
min_candidate_samples = 3 # Successful probes a candidate needs in the window before it can be chosen; 2 to window_size.
max_failures = 2 # Failed probes in the window that mark the selected node unstable; 1 to window_size. This many Mihomo dial failures in a probed group also probe it early, at most every 15s.
min_improvement = "100ms"
cooldown = "5m"
jitter = "10s" # Spreads each round so every probe does not fire at once.
automated_groups = [] # Switches these groups automatically. Opaque group IDs shown by IPC/UI; manual selection removes opt-in.

[dns]
listen = "127.0.0.1:1053" # No privileged port or system DNS redirect is enabled implicitly.

[system_proxy]
enabled = false # Applied only after Mihomo controller and proxy are ready.

[tun]
enabled = false # Linux only. Routes all host traffic through Mihomo's TUN device clashpulse0.
# Enabling grants cap_net_admin to a private Mihomo copy with one pkexec prompt; without a polkit agent, run the shown `sudo setcap` command.
stack = "gvisor" # gvisor | mixed | system
dns_mode = "profile" # profile keeps the profile's DNS mode (fake-ip if unset); fake-ip | redir-host force it.
```

Monitor settings are explained in [automatic switching](switching.html).

## subscriptions.toml

```toml
# Subscription URLs are secrets. Keep this file 0600 and its directory 0700.
# Save the latest short-lived URL, then request a refresh before it expires.
# Activation is separate from refresh; refreshing does not switch Mihomo.
#
# [[subscription]]
# id = "primary"                   # Stable ASCII ID; do not reuse it.
# name = "Primary"                  # Safe display name.
# url = "https://provider.example/profile?token=replace-me"
# user_agent = "clash-verge/v2.5.6" # Optional provider override; default clash-pulse.
# enabled = true
# refresh_interval = "12h"         # Single shared timer schedules refreshes.
# timeout = "30s"                  # Context bound includes download and validation.
# route = "auto"                   # auto (Mihomo when running; else http_proxy for terminal commands, or direct) | direct | system_proxy | mihomo_proxy
# allow_http = false               # Explicit per-subscription exception only.
# allow_invalid_tls = false        # Explicit per-subscription exception only.
```

Routes and refresh behavior are in [subscriptions and data](sources.html).

## resources.toml

```toml
# Reviewed data sources are enabled for new configs; disable any source below.
# Resource IDs determine private generated filenames; source bytes are not edited.
# Formats: dat for geoip.dat/geosite.dat; mmdb for Country.mmdb;
# yaml, text, or mrs for rule-set/rule-provider.
# Rule types: domain, ipcidr, or classical (classical cannot use mrs).
# Local sources use absolute paths; Windows network shares are not local.
#
[[resource]]
id = "geoip"
kind = "geoip.dat"
format = "dat"
url = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat"
enabled = true
interval = "24h"

[[resource]]
id = "geosite"
kind = "geosite.dat"
format = "dat"
url = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat"
enabled = true
interval = "24h"

[[resource]]
id = "country"
kind = "Country.mmdb"
format = "mmdb"
url = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb"
enabled = true
interval = "24h"

# CN data is downloaded by default; configure a filter or DNS route to use it.
[[resource]]
id = "cn"
kind = "rule-set"
format = "mrs"
rule_type = "domain"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/cn.mrs"
enabled = true
interval = "24h"
#
# [[resolver_set]]
# id = "domestic"
# endpoints = ["https://dns.example/dns-query"]
# dnscrypt = false # true requires a reachable loopback UDP/TCP DNSCrypt listener.
#
# [[dns_route]]
# resource = "cn" # Alternatively suffix = "example.cn" or geosite = "cn".
# resolver_set = "domestic"
#
# DNS routing is not enabled until its resource, resolver, and route are
# deliberately configured. No DNSCrypt process or system DNS redirect is run.
```

## filters.toml

```toml
# Filters reference a resource from resources.toml; no second downloader exists.
# Target must name a proxy or group in the active source profile. Managed rules
# are rendered before that profile's catch-all rule; the source is immutable.
#
# [[filter]]
# id = "domestic-filter"
# resource = "cn" # Stable ID of an enabled rule-set or rule-provider resource.
# format = "mrs" # Must match the referenced resource format.
# target = "Proxy" # Must exist in the active subscription profile.
# enabled = false
```
