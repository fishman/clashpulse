---
layout: default
title: 隐私与安全
parent: 简体中文
nav_order: 7
---

# 隐私与安全

## Mihomo 控制器

Mihomo 按参数列表直接启动，从不经过 shell。Linux 和 macOS 上，控制器是 `ctl` 目录（权限 0700）里的 Unix socket，不开 TCP 端口。Mihomo 创建的这个 socket 人人可访问，也不校验密钥，所以访问控制靠的是这个目录。Windows 上它监听一个随机的回环端口。

每次启动都会生成随机的 32 字节密钥，生成的配置设置 `allow-lan: false` 并绑定 `127.0.0.1`。非回环的控制器地址会被拒绝。密钥从不经过 IPC。

## 本地 IPC

Linux 和 macOS 用 `$XDG_RUNTIME_DIR/clashpulse.sock` 或 `$TMPDIR/clashpulse-<uid>/ipc.sock`，前提是所在目录权限正好是 0700。来自其他用户的连接会被拒绝（Linux 用 `SO_PEERCRED`，macOS 用对端凭据）。Windows 用命名管道，只有当前用户的 SID 能打开，并且会检查管道所属进程。

没有 TCP 传输，协议版本不同的客户端会被拒绝。快照里只有源的主机名，不含 URL。

## 文件

快照、生成的配置、缓存的资源、控制器 socket 和锁文件都在私有状态目录：目录 0700，文件 0600。ClashPulse 写入的配置文件是 0600，所在目录 0700。Windows 上由用户配置文件目录的 ACL 保护。订阅 URL 属于机密，`subscriptions.toml` 请保持 0600。

新配置先写入私有临时文件，用 `mihomo -t` 检查。校验或重启失败时，恢复之前的配置、节点选择和进程。更换 Mihomo 内核也一样。

## 系统代理 {#system-proxy}

操作系统代理只会指向已经在接受连接的回环监听地址，应用失败会回滚。关闭时：Linux（GNOME）把 ClashPulse 写过的键恢复为默认值，之前的值不会保留；macOS 和 Windows 恢复应用前保存的 HTTP 和 HTTPS 设置。

## 显示什么

- 节点名中像主机名的部分会替换成 `[redacted]`。含 `@`、不可打印字符或超过 80 个字符的名称，显示为 `Proxy 2` 这样的序号标签。
- 不写日志文件，Mihomo 的控制台输出直接丢弃。
- 活动记录只保存本次会话的固定消息；IPC 会拒绝任何包含 `://` 的诊断信息。
- 下载失败显示固定标签，例如 `subscription HTTP 403`。
- `clashpulse activate` 的错误信息不含配置文件路径。
- HTTP 响应内容留在服务内部；`--show-response` 在本地最多打印 4 KiB，服务运行时会被拒绝。
- “生成配置变更”只显示字段名和原因。

## 网络访问

没有遥测，也没有崩溃上报。ClashPulse 自己只请求：

- 你配置的订阅和资源 URL；
- Mihomo 控制器，包括发往 `test_url` 的延迟探测；
- 经 Mihomo 私有回环监听器下载的 `stream_url`（稳定流）。

默认 `test_url` 是明文 HTTP，可能被劫持；在意探测结果的完整性时请改用 HTTPS。
