---
layout: default
title: 自动切换
parent: 简体中文
nav_order: 5
---

# 自动切换

ClashPulse 只切换你开启了自动切换的托管 `select` 组：GUI 代理 > 启用组自动切换、TUI 代理页按 `a`，或写进 `automated_groups`。手动选节点会取消该组的自动切换。

Mihomo 自己的 `url-test` 和 `fallback` 组归 Mihomo 管。ClashPulse 从不测速也不切换它们；`url_test_interval` 和 `url_test_tolerance` 只设置这些组的测速参数。

## 切换策略

在 `[monitor]` 的 `switch_policy`、GUI 设置 > 延迟监测，或 TUI 设置页按 `y` 修改。

最快（`lowest_latency`，默认）：切到延迟中位数至少低 `min_improvement` 的节点。响应快不代表节点能撑住长时间下载。

故障切换（`failover`）：当前节点失效，或连续 `consecutive_bad_samples` 轮超过 `threshold` 之前不动。

稳定流（`steady_stream`）：每隔 `stream_interval`，通过当前节点和另外 3 个最快的节点下载一个 256 KiB 的 HTTPS 文件。某节点最近 5 次下载中断 2 次（被重置、卡住或被截断）后，换到中断更少且至少完成 3 次的节点。每个组每小时约 12 MB，最多 24 MB。节点不再响应延迟探测时，仍按故障切换规则离开。需要支持固定监听器的 Mihomo（v1.19.31 已验证）。

所有策略都遵守冷却时间和手动选择；全部候选都失败时停止切换。探测走 Mihomo 的延迟接口，和你的流量同一条路径；超时或出错记为失败样本。

## 监测设置 {#monitor-settings}

![GUI 监测探测策略对话框，选中稳定流](../assets/screenshots/gui-monitor-policy.png)

| 键 | 默认值 | 含义 | TUI |
| --- | --- | --- | --- |
| `enabled` | `true` | 测量托管组 | `m` / `n` |
| `test_url` | `http://cp.cloudflare.com/generate_204` | 延迟探测 URL | `u` |
| `switch_policy` | `"lowest_latency"` | 或 `failover`、`steady_stream` | `y` |
| `stream_url` | `https://speed.cloudflare.com/__down?bytes=262144` | 稳定流下载的文件 | `O` |
| `stream_interval` | `"5m"` | 两轮下载之间的间隔 | `I` |
| `interval` | `"1m"` | 两轮延迟探测之间的间隔；切换至少要三轮 | `i` |
| `timeout` | `"5s"` | 单次探测超时 | `o` |
| `concurrency` | `3` | 并发探测数，最多 64 | `c` |
| `threshold` | `"800ms"` | 超过即算异常的延迟 | `h` |
| `alert_threshold` | `"250ms"` | 所有候选都更慢时才通知 | `t` |
| `window_size` | `5` | 每个节点保留的样本数（2-20） | `W` |
| `consecutive_bad_samples` | `3` | 故障切换前的连续异常样本数 | `s` |
| `min_candidate_samples` | `3` | 候选节点需要的成功次数 | `C` |
| `max_failures` | `2` | 判定节点不稳定的失败次数 | `f` |
| `min_improvement` | `"100ms"` | 中位数至少要改善多少 | `p` |
| `cooldown` | `"5m"` | 同一组两次切换的最短间隔 | `z` |
| `jitter` | `"10s"` | 在一轮内错开探测 | `w` |
| `automated_groups` | `[]` | 自动切换的组 | 代理页 `a` |

一个被探测的组里出现 `max_failures` 次 Mihomo 拨号失败，也会提前触发探测，最多每 15 秒一次。

默认 `test_url` 是明文 HTTP，可能被劫持。如果在意探测结果的完整性，请换成 HTTPS。TUI 按 `T` 或在延迟监测分节里可以发送测试通知。
