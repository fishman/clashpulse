---
layout: default
title: 常见问题
parent: 简体中文
nav_order: 8
---

# 常见问题

## 可以正式使用了吗？

还不行。v0.1.1 是未签名的预发布版。

## 名称会改吗？

可能。在商标、命名空间、软件包名和域名检查完成之前，这都只是暂定名称。

## 它能替代 Clash Verge Rev 吗？

不能。它没有脚本、合并、代理、组或规则编辑器，也从不修改源配置。ClashPulse 只负责在 Mihomo 之上选节点。

## 为什么不捆绑 Mihomo？

由发行版或 Mihomo 官方发布来保持更新。ClashPulse 使用 `PATH` 里的 `mihomo` 或你指定的路径，并先检查它的能力；不支持的设置会报错并指出是哪一项。

## macOS 提示应用已损坏

构建没有公证。校验 `SHA256SUMS` 后执行：

```sh
xattr -dr com.apple.quarantine ClashPulse.app
```

## 有中文界面吗？

有。操作系统区域设置为 `zh-CN`（包括 `zh_CN.UTF-8`、`zh-Hans-CN`）时界面显示简体中文。代理名称、URL 和凭据保持原样。中文使用系统已安装的字体。

## 文件放在哪里？

| 系统 | 配置 | 私有状态 |
| --- | --- | --- |
| Linux | `~/.config/clashpulse`（或 `$XDG_CONFIG_HOME/clashpulse`） | `~/.local/state/clashpulse`（或 `$XDG_STATE_HOME/clashpulse`） |
| macOS | `~/Library/Application Support/clashpulse` | `~/.local/state/clashpulse` |
| Windows | `%AppData%\clashpulse` | 用户目录下的 `.local\state\clashpulse` |

见[配置](configuration.html)。

## 在哪些平台上测试？

CI 在 `ubuntu-22.04`、`macos-latest`（arm64）和 `windows-latest` 上原生构建并测试每次推送，并运行 `clashpulse version`。如果 Linux 二进制需要超出基础 x86-64 或 glibc 2.35，不是 PIE，或缺少完整 RELRO，Linux 任务就会失败。

## TUI 提示没有运行中的服务

`clashpulse tui` 只负责连接。先启动 GUI 或 `clashpulse activate <profile.yaml>`。

## GNOME 上看不到托盘图标

托盘使用 StatusNotifier。GNOME 可能需要 [AppIndicator 扩展](https://extensions.gnome.org/extension/615/appindicator-support/)。

## 会上传数据吗？

不会。没有遥测、崩溃上报或云端后台。见[隐私与安全](security.html)。

## 怎么报告问题？

在 [Issues](https://github.com/fishman/clashpulse/issues) 提交。活动记录（GUI 顶栏菜单 > 查看活动记录，TUI `~`）可以放心分享。不要贴订阅 URL、节点名、服务器地址或凭据，请用 `example.com` 这样的名字代替。
