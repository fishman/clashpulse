---
layout: default
title: 安装
parent: 简体中文
nav_order: 2
---

# 安装

另外需要安装 [Mihomo](https://github.com/MetaCubeX/mihomo)，任何构建都不附带它。

## 发布版下载

v0.1.1 是未签名的预发布版，在 [Releases 页面](https://github.com/fishman/clashpulse/releases)：

| 平台 | 文件 |
| --- | --- |
| Linux x86-64 | `clashpulse-v0.1.1-linux-amd64.tar.gz` |
| macOS Apple 芯片 | `clashpulse-v0.1.1-darwin-arm64.tar.gz`（`ClashPulse.app`） |
| Windows x86-64 | `clashpulse-v0.1.1-windows-amd64.zip` |

用 `SHA256SUMS` 校验：

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

Linux 版需要基础 x86-64 和 glibc 2.35 及以上（Ubuntu 22.04、Debian 12）。GUI 需要 X11 或 Wayland；GNOME 下托盘图标可能要装 [AppIndicator 扩展](https://extensions.gnome.org/extension/615/appindicator-support/)。

```sh
tar -xzf clashpulse-v0.1.1-linux-amd64.tar.gz
./clashpulse version
```

macOS 版没有公证，首次启动前先去掉隔离属性：

```sh
tar -xzf clashpulse-v0.1.1-darwin-arm64.tar.gz
xattr -dr com.apple.quarantine ClashPulse.app
```

Windows 上解压后运行 `clashpulse.exe`。

## Arch Linux

`packaging/aur/` 里有 `clashpulse`（从带标签的源码构建）和 `clashpulse-bin`（重新打包发布版）。两者都依赖 AUR 的 `mihomo` 或 `mihomo-bin`。

```sh
cd packaging/aur/clashpulse
makepkg -si
```

## Mihomo

把 `mihomo` 放进 `PATH`（默认的 `binary = "system"`），或在 设置 > Mihomo 内核 里选一个路径。ClashPulse 使用前会先检查它的版本和能力。稳定流策略需要支持固定监听器的 Mihomo（v1.19.31 已验证）。

## 从源码构建

需要 Go 1.26.6、C 编译器和 Fyne 的图形头文件。Debian/Ubuntu：

```sh
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libgl1-mesa-dev xorg-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev libwayland-dev libxkbcommon-dev wayland-protocols
git clone https://github.com/fishman/clashpulse.git
cd clashpulse
go build -o clashpulse ./cmd/clashpulse
./clashpulse version
```

macOS 需要 Xcode Command Line Tools，Windows 需要 MinGW/GCC。每个平台都要在本机构建，不支持交叉编译 GUI。见[依赖](dependencies.html)。

`make install` 安装到 `~/.local`（用 `PREFIX=/usr/local` 改位置）。`make run` 和 `make tui` 构建并启动 GUI 或 TUI。
