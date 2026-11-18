---
layout: default
title: 订阅与数据
parent: 简体中文
nav_order: 6
---

# 订阅与数据

ClashPulse 从不修改源配置。它为每个订阅保存一份私有的“最近一次可用”快照，并在生成的 Mihomo 配置里加入过滤列表、数据资源和 DNS 路由。

## 订阅

在 GUI 里添加，或在 TUI 订阅页按 `n`，也可以写进 `subscriptions.toml`：

```toml
[[subscription]]
id = "primary"                   # Stable ASCII ID; do not reuse it.
name = "Primary"                  # Safe display name.
url = "https://provider.example/profile?token=replace-me"
enabled = true
refresh_interval = "12h"
route = "auto"
```

除非该订阅设置了 `allow_http`，否则必须用 HTTPS；`allow_invalid_tls` 是另一个按订阅设置的例外。带 `user:password@` 的 URL 会被拒绝。最多跟随 5 次重定向，每次都做同样的检查。条件请求校验值和自定义的 `user_agent`（可打印 ASCII，最长 256 字节，默认 `clash-pulse`）只发给原始源站。`timeout` 覆盖下载、渲染和校验的全过程。

下载（`clashpulse download subscription [id]`）只保存一份未启用的快照，不碰 Mihomo。刷新（GUI 刷新、TUI `r`、`clashpulse refresh`）还会渲染完整配置并用 Mihomo 校验，但不切换。启用（GUI 启用、TUI `a`）校验后以事务方式应用。

任何失败都不影响当前配置和之前的快照。返回 `304` 或内容哈希与已存快照相同时，什么都不替换。不含 `proxies` 或 `proxy-providers` 的配置也算失败；账号过期时，服务商常常返回这种空配置。

| `route` | 连接方式 |
| --- | --- |
| `auto`（默认） | 经正在运行的 Mihomo；Mihomo 停止时，终端命令遵循 `http_proxy`、`https_proxy` 和 `no_proxy`，桌面服务直连 |
| `direct` | 直连，忽略代理环境变量 |
| `system_proxy` | 操作系统代理；未设置时失败 |
| `mihomo_proxy` | Mihomo 的混合端口；Mihomo 停止时失败 |

路由不可用就报错，不会换用其他路由。数据资源总是用 `auto`。有效期很短的订阅 URL，要在过期前刷新。

## 数据资源

新配置在 `resources.toml` 里默认启用四个 [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) 数据源：

| id | 类型 | 格式 |
| --- | --- | --- |
| `geoip` | `geoip.dat` | `dat` |
| `geosite` | `geosite.dat` | `dat` |
| `country` | `Country.mmdb` | `mmdb` |
| `cn` | `rule-set`（domain） | `mrs` |

`cn` 会下载，但在过滤列表或 DNS 路由用到它之前不参与路由。

资源在刷新或启用配置时下载，也可以用 `clashpulse refresh resource [id]`，没有启用的配置也能用。有配置在运行时，Mihomo 会在文件变更前停止，校验通过后再启动；更新失败会恢复旧文件。

默认的 `releases/download/latest` 地址会变，也没有固定 SHA-256。需要固定摘要时，请改用带版本号的文件。许可信息见[依赖](dependencies.html)。

## 过滤列表

过滤列表把一个规则集资源路由到当前配置中的某个代理或组：

```toml
[[filter]]
id = "domestic-filter"
resource = "cn"   # Stable ID of an enabled rule-set or rule-provider resource.
format = "mrs"    # Must match the referenced resource format.
target = "Proxy"  # A proxy or group in the active profile, or DIRECT/REJECT.
enabled = true
```

托管的 `RULE-SET` 规则排在配置自身规则之前。目标不存在时渲染失败。

## DNS 路由

DNS 监听地址默认 `127.0.0.1:1053`（`[dns] listen`），必须是回环地址。DNS 路由把匹配条件对应到解析器组，生成 Mihomo 的 `nameserver-policy`（GUI 设置 > DNS，TUI 设置页 `v` 和 `a`）：

```toml
[[resolver_set]]
id = "domestic"
endpoints = ["https://dns.example/dns-query"]

[[dns_route]]
resource = "cn"   # Alternatively suffix = "example.cn" or geosite = "cn".
resolver_set = "domestic"
```

每条路由只能有 `suffix`、`geosite`、`resource` 中的一个。指回 DNS 监听地址本身的端点是加载错误。

ClashPulse 不运行 DNSCrypt。自己在回环 UDP 或 TCP 上运行 `dnscrypt-proxy`，再标记解析器组：

```toml
[[resolver_set]]
id = "domestic"
endpoints = ["udp://127.0.0.1:5353"]
dnscrypt = true   # Requires a reachable loopback UDP/TCP DNSCrypt listener.
```

每次渲染配置时，ClashPulse 都会向每个 DNSCrypt 端点发一次查询，超时 2 秒。有一个不响应，这次变更就失败，正在运行的配置保持不变。
