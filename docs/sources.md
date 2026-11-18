---
layout: default
title: Subscriptions and data
nav_order: 6
---

# Subscriptions and data

ClashPulse never edits a source profile. It keeps a private last-known-good
snapshot of each subscription and adds filters, data resources, and DNS
routes in the generated Mihomo config.

## Subscriptions

Add one in the GUI, with TUI Subscriptions `n`, or in `subscriptions.toml`:

```toml
[[subscription]]
id = "primary"                   # Stable ASCII ID; do not reuse it.
name = "Primary"                  # Safe display name.
url = "https://provider.example/profile?token=replace-me"
enabled = true
refresh_interval = "12h"
route = "auto"
```

HTTPS is required unless the subscription sets `allow_http`;
`allow_invalid_tls` is the other per-subscription exception. URLs with
`user:password@` are rejected. Up to 5 redirects are followed, each checked
the same way. Validators and a custom `user_agent` (printable ASCII, at most
256 bytes, default `clash-pulse`) go only to the original origin. `timeout`
covers download, render, and validation.

Download (`clashpulse download subscription [id]`) stores an inactive
snapshot without touching Mihomo. Refresh (GUI Refresh, TUI `r`,
`clashpulse refresh`) also renders the full config and validates it with
Mihomo, but does not switch. Activate (GUI Activate, TUI `a`) validates and
applies it as one transaction.

A failure leaves the active config and previous snapshot alone. An unchanged
`304` or a body with the stored hash replaces nothing. A profile without
`proxies` or `proxy-providers` is a failure too; providers often send one for
an expired account.

| `route` | connection |
| --- | --- |
| `auto` (default) | through the running Mihomo; while it is stopped, terminal commands honor `http_proxy`, `https_proxy`, and `no_proxy`, and the desktop service connects directly |
| `direct` | direct, ignoring proxy variables |
| `system_proxy` | the OS proxy; fails when none is set |
| `mihomo_proxy` | Mihomo's mixed port; fails while Mihomo is stopped |

An unavailable route is an error, never a fallback. Data resources always use
`auto`. Refresh short-lived provider URLs before they expire.

## Data resources

New configs enable four
[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat)
sources in `resources.toml`:

| id | kind | format |
| --- | --- | --- |
| `geoip` | `geoip.dat` | `dat` |
| `geosite` | `geosite.dat` | `dat` |
| `country` | `Country.mmdb` | `mmdb` |
| `cn` | `rule-set` (domain) | `mrs` |

`cn` routes nothing until a filter or DNS route uses it.

Resources download on refresh or activation, or with
`clashpulse refresh resource [id]`, which also works without an active
profile. With a profile active, Mihomo stops before the files change and
restarts after validation; a failed update restores the old files.

The default `releases/download/latest` URLs move and have no SHA-256 pin. For
a fixed digest, use a versioned asset. Licensing is in
[dependencies](dependencies.html).

## Filter lists

A filter routes a rule-set resource to a proxy or group of the active profile:

```toml
[[filter]]
id = "domestic-filter"
resource = "cn"   # Stable ID of an enabled rule-set or rule-provider resource.
format = "mrs"    # Must match the referenced resource format.
target = "Proxy"  # A proxy or group in the active profile, or DIRECT/REJECT.
enabled = true
```

Managed `RULE-SET` rules go before the profile's own rules. An unknown target
fails the render.

## DNS routing

The DNS listener defaults to `127.0.0.1:1053` (`[dns] listen`) and must be
loopback. Routes map a matcher to a resolver set and become Mihomo's
`nameserver-policy` (GUI Settings > DNS, TUI Settings `v` and `a`):

```toml
[[resolver_set]]
id = "domestic"
endpoints = ["https://dns.example/dns-query"]

[[dns_route]]
resource = "cn"   # Alternatively suffix = "example.cn" or geosite = "cn".
resolver_set = "domestic"
```

Each route has exactly one of `suffix`, `geosite`, or `resource`. An endpoint
that loops back to the listener is a load error.

ClashPulse does not run DNSCrypt. Run `dnscrypt-proxy` on loopback UDP or TCP
and mark the resolver set:

```toml
[[resolver_set]]
id = "domestic"
endpoints = ["udp://127.0.0.1:5353"]
dnscrypt = true   # Requires a reachable loopback UDP/TCP DNSCrypt listener.
```

On every render ClashPulse queries each DNSCrypt endpoint with a 2 second
timeout. If one does not answer, the change fails and the running config
stays.
