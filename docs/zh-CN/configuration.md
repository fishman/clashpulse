---
layout: default
title: 配置
parent: 简体中文
nav_order: 4
---

# 配置

设置保存在四个 TOML 文件里，每个文件只管一件事，没有继承，也没有跨文件优先级。快照、探测历史和控制器状态属于私有状态，不写进 TOML。

| 系统 | 配置目录 |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/clashpulse`，默认 `~/.config/clashpulse` |
| macOS | `~/Library/Application Support/clashpulse` |
| Windows | `%AppData%\clashpulse` |

私有状态（快照、生成的 Mihomo 配置、缓存的资源、锁文件）在 `$XDG_STATE_HOME/clashpulse`，默认 `~/.local/state/clashpulse`。

首次启动时按下面的示例生成这些文件。

| 文件 | 内容 |
| --- | --- |
| `config.toml` | Mihomo 内核、混合端口和代理模式；延迟监测和切换策略；DNS 监听地址；系统代理；TUN |
| `subscriptions.toml` | 订阅：来源 URL、刷新间隔、超时、下载路由、TLS 例外 |
| `resources.toml` | 数据资源（GeoIP、GeoSite、MMDB、规则集）、DNS 解析器组和 DNS 路由 |
| `filters.toml` | 过滤列表：把一个规则集资源路由到当前配置的某个组 |

缺少的文件视为空；目录里的其他文件会被忽略。GUI 和 TUI 能改这些文件里的每一项。

## 校验与重载

以下都是加载错误：未知键、重复 ID、无效枚举值、格式错误的 URL、不支持的内核或资源引用，以及引用了不存在的资源。默认值在程序里，所以文件里只记录你改过的项。

ClashPulse 监视配置目录，用原子重命名保存的编辑器也没问题。短暂防抖后，它会加载并校验整套文件。任何错误都会保留正在运行的配置，并把出错的文件和键报告给 GUI、TUI 和 IPC 客户端。只有生成的配置确实变了，Mihomo 才会重载。概览 > 重新加载配置（TUI `r`）可以手动重新读取。

在 GUI 和 TUI 里改设置走同一条路：先写私有临时文件，原子替换，再完整重载。

## config.toml

```toml
# ClashPulse user intent. Save all four files together in the private config dir.
# Defaults: system Mihomo, enabled health monitoring, disabled system proxy and TUN, loopback DNS.
[mihomo]
binary = "system" # "bundled" is explicit fallback; or an executable path.
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

延迟监测设置见[自动切换](switching.html)。

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

下载路由和刷新行为见[订阅与数据](sources.html)。

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
