---
layout: default
title: 简体中文
nav_order: 10
has_children: true
---

[English](../)

# ClashPulse

ClashPulse 是 [Mihomo](https://github.com/MetaCubeX/mihomo) 的桌面客户端。它持续测量自动切换组里的每个节点，只有另一个节点确实更好时才切换。所有阈值都可以在设置里改。

Linux、macOS、Windows 各一个 Go 二进制，内含 GUI、TUI 和命令行。不捆绑 Mihomo，ClashPulse 把它作为独立进程运行。

![ClashPulse 代理页面：一个托管组及各节点的延迟测量](../assets/screenshots/gui-proxies.png)

## 快速开始

1. [安装](installation.html) ClashPulse 和 Mihomo，确保 `mihomo` 在 `PATH` 中。
2. 运行 `./clashpulse`。
3. 添加一个 HTTPS 订阅，刷新，然后启用。
4. 需要终端界面时，在另一个终端运行 `./clashpulse tui`。

Windows 上用 `.\clashpulse.exe`。首次启动会在 `~/.config/clashpulse`（Linux）写入四个配置文件。接下来看[使用](usage.html)。

## 工作方式

- 切换看的是一组样本的中位数，不看单次快样本，而且候选节点要快出 `min_improvement` 才行。冷却时间和“全部候选失败即停止”避免来回跳。见[自动切换](switching.html)。
- 每台机器一个服务，负责 Mihomo 和延迟监测；GUI、TUI 和命令行都通过本地 IPC 连接它。
- 配置是四个严格校验的 TOML 文件。写错了就是加载错误，正在运行的配置不变。见[配置](configuration.html)。
- 订阅 URL 只在编辑请求中经过 IPC；快照和事件只带来源主机名，从不带 URL 或凭据。见[隐私与安全](security.html)。

v0.1.1 是未签名的预发布版，ClashPulse 也只是暂定名称。见[常见问题](faq.html)。
