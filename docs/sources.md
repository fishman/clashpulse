---
layout: default
title: Subscriptions and data
nav_order: 6
---

# Subscriptions and data

ClashPulse never edits a source profile. A subscription is an immutable
remote input plus private metadata and a last-known-good snapshot; filter
lists, data resources, and DNS routes are rendered into the generated
Mihomo configuration alongside it.

## Subscriptions

Add one in the GUI (Subscriptions > Add subscription), the TUI
(Subscriptions `n`), or `subscriptions.toml`:

```toml
[[subscription]]
id = "primary"                   # Stable ASCII ID; do not reuse it.
name = "Primary"                  # Safe display name.
url = "https://provider.example/profile?token=replace-me"
enabled = true
refresh_interval = "12h"
route = "auto"
```

Sources are HTTPS by default. `allow_http` and `allow_invalid_tls` are
explicit per-subscription exceptions; neither has a global switch. A URL
with embedded `user:password@` is rejected. A download follows at most 5
redirects, and every redirect target passes the same checks (HTTPS unless
`allow_http`, no userinfo). Stored ETag/Last-Modified validators and a
custom `user_agent` are sent only to the subscription's original origin.
Response bodies are size-capped.

`user_agent` (printable ASCII, at most 256 bytes) overrides the default
`clash-pulse` for providers that need it. `timeout` bounds the whole
refresh: download, render, and Mihomo validation. One shared timer
schedules every automatic refresh.

### Download, refresh, activate

Three steps, each explicit:

- **Download** (`clashpulse download subscription [id]`) fetches and parses
  a profile, then stores it as an inactive private snapshot. It does not
  inspect or run Mihomo.
- **Refresh** (GUI Refresh, TUI `r`, `clashpulse refresh`) downloads the
  profile, renders the complete config, validates it with the selected
  Mihomo, and stores the result as the new candidate. Refreshing does not
  switch Mihomo.
- **Activate** (GUI Activate, TUI `a`) renders the profile, validates the
  complete generated config with the selected Mihomo, and applies it
  transactionally.

A failed download or validation leaves the active configuration and the
prior snapshot untouched. Remote sources with ETag or Last-Modified use
conditional requests, so an unchanged `304` downloads no body and replaces
no file; a body whose hash matches the stored snapshot is likewise not
stored again.

### The empty-profile rule

A download must contain at least one proxy or proxy provider (`proxies` or
`proxy-providers`). An empty profile - how providers often answer an
expired account - is a failure, and the last good snapshot stays.

### Download routes

`route` selects how a subscription is fetched:

| route | connection |
| --- | --- |
| `auto` (default) | through the running Mihomo; while Mihomo is stopped, terminal `download` and `refresh` honor `http_proxy`, `https_proxy`, and `no_proxy`, and the desktop service connects directly |
| `direct` | a direct connection, ignoring proxy environment variables |
| `system_proxy` | the OS HTTP or HTTPS proxy setting; fails when none is set |
| `mihomo_proxy` | the running Mihomo's mixed port; fails while Mihomo is stopped |

An unavailable route is an error, not a silent switch to another route.
Data resources always use `auto`.

Short-lived provider URLs: save the latest URL, then request a refresh
before it expires.

## Data resources

`resources.toml` declares the data files Mihomo reads. New configs enable
four reviewed [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat)
sources:

| id | kind | format |
| --- | --- | --- |
| `geoip` | `geoip.dat` | `dat` |
| `geosite` | `geosite.dat` | `dat` |
| `country` | `Country.mmdb` | `mmdb` |
| `cn` | `rule-set` (domain) | `mrs` |

The `cn` list is downloaded but not routed until a filter or DNS route uses
it. Formats are `dat` for geoip.dat/geosite.dat, `mmdb` for Country.mmdb,
and `yaml`, `text`, or `mrs` for rule-sets; rule types are `domain`,
`ipcidr`, or `classical` (classical cannot use `mrs`). Local sources use
absolute paths; Windows network shares are not local.

Resources download during validated profile refresh or activation, or with
`clashpulse refresh resource [id]`, which also works without an active
profile: it caches enabled resources under stable filenames in the private
state directory's `resources/` root without starting Mihomo. When a profile
is active, Mihomo stops before those files change and restarts only after
validation; a failed update restores the prior files and runtime.

The default `releases/download/latest` URLs are mutable and carry no static
SHA-256 pin. If you need a fixed digest, select a versioned asset and pin
its matching digest. Every source can be disabled or replaced; see
[dependencies](dependencies.html) for provenance and licensing notes.

## Filter lists

A filter list routes a rule-set resource to a proxy or group of the active
profile. It references a resource from `resources.toml` - there is no second
downloader:

```toml
[[filter]]
id = "domestic-filter"
resource = "cn"   # Stable ID of an enabled rule-set or rule-provider resource.
format = "mrs"    # Must match the referenced resource format.
target = "Proxy"  # A proxy or group in the active profile, or DIRECT/REJECT.
enabled = true
```

The resource must be a `rule-set` or `rule-provider` resource, `format` must
equal its format, and an enabled filter cannot reference a disabled
resource. Managed `RULE-SET` rules are placed ahead of all of the profile's
own rules; a target that names no proxy, group, `DIRECT`, or `REJECT` in the
active profile fails the render. The source profile is not modified.

## DNS routing

The Mihomo DNS listener defaults to `127.0.0.1:1053` (`[dns] listen` in
`config.toml`) and must be a loopback address.

DNS routing maps matchers to resolver sets and renders Mihomo's
`nameserver-policy`. Resolver sets and routes live in `resources.toml` (GUI
Settings > DNS; TUI Settings `v` adds a resolver set, `a` a route):

```toml
[[resolver_set]]
id = "domestic"
endpoints = ["https://dns.example/dns-query"]

[[dns_route]]
resource = "cn"   # Alternatively suffix = "example.cn" or geosite = "cn".
resolver_set = "domestic"
```

Each route names exactly one matcher: `suffix`, `geosite`, or a domain
rule-set `resource`. Nothing is routed until the resource, resolver set, and
route are all configured. An endpoint that loops back to the DNS listener
is a load error.

### DNSCrypt

ClashPulse does not run DNSCrypt. Run `dnscrypt-proxy` yourself on a
loopback UDP or TCP listener and mark the resolver set:

```toml
[[resolver_set]]
id = "domestic"
endpoints = ["udp://127.0.0.1:5353"]
dnscrypt = true   # Requires a reachable loopback UDP/TCP DNSCrypt listener.
```

Mihomo stays the DNS policy engine and sends the selected queries to that
listener; DNSCrypt encrypts the upstream hop. A non-loopback or non-UDP/TCP
endpoint with `dnscrypt = true` is rejected at load. Every time the config
is rendered (activation, refresh, reload), ClashPulse sends a DNS query to
each DNSCrypt endpoint with a 2 second timeout; if one does not answer, the
change fails and the running configuration stays.
