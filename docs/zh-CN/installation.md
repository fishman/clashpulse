---
layout: default
title: 安装
parent: 简体中文
nav_order: 2
---

# 安装

ClashPulse 运行 [Mihomo](https://github.com/MetaCubeX/mihomo)。v0.1.1 之后的 macOS 和 Windows 发布包内置一个固定版本的官方 Mihomo 构建；Linux、Homebrew 和源码构建需要自己安装 Mihomo。

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

Windows 上解压后运行 `clashpulse.exe`。amd64 包在 Windows on ARM 上通过 x64 仿真也能启动（CI 在那里运行 `clashpulse.exe version` 和 `mihomo.exe -v`；GUI 没有在 ARM 上测试）。目前还没有原生 Windows arm64 构建。

## Homebrew（macOS）

```sh
brew install fishman/clashpulse/clashpulse
clashpulse
```

该 formula 在你的 Mac 上从带标签的源码构建，因此不会被 Gatekeeper 拦截，并会把 Homebrew 的 `mihomo` 作为依赖一起安装。它安装的是 `clashpulse` 命令，不是 /Applications 里的应用。

## Arch Linux

`packaging/aur/` 里有 `clashpulse`（从带标签的源码构建）和 `clashpulse-bin`（重新打包发布版）。两者都依赖 AUR 的 `mihomo` 或 `mihomo-bin`。

```sh
cd packaging/aur/clashpulse
makepkg -si
```

## Mihomo

macOS 和 Windows 发布包默认 `binary = "bundled"`：使用包内的 Mihomo，ClashPulse 先用构建时记录的摘要校验它，复制到私有状态目录，再从那里运行。其他构建默认 `binary = "system"`，即 `PATH` 里的 `mihomo`。两者以及可执行文件路径都可以在 设置 > Mihomo 内核 里选择。ClashPulse 使用前会先检查它的版本和能力。稳定流策略需要支持固定监听器的 Mihomo（v1.19.31 已验证）。

内置 Mihomo 只随 ClashPulse 发布版更新。发布包的 `licenses/` 里有它的 GPL-3.0 许可证和来源说明，每个发布版都附带对应的 Mihomo 源码。

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
