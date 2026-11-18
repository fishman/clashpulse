---
layout: default
title: Installation
nav_order: 2
---

# Installation

You also need [Mihomo](https://github.com/MetaCubeX/mihomo); no build
bundles it.

## Release downloads

v0.1.1 is an unsigned pre-release on the
[releases page](https://github.com/fishman/clashpulse/releases):

| platform | asset |
| --- | --- |
| Linux x86-64 | `clashpulse-v0.1.1-linux-amd64.tar.gz` |
| macOS Apple silicon | `clashpulse-v0.1.1-darwin-arm64.tar.gz` (`ClashPulse.app`) |
| Windows x86-64 | `clashpulse-v0.1.1-windows-amd64.zip` |

Check it against `SHA256SUMS`:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

Linux needs baseline x86-64 and glibc 2.35 or newer (Ubuntu 22.04, Debian
12). The GUI needs X11 or Wayland; on GNOME the tray may need the
[AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/).

```sh
tar -xzf clashpulse-v0.1.1-linux-amd64.tar.gz
./clashpulse version
```

The macOS app is not notarized. Clear the quarantine flag before first start:

```sh
tar -xzf clashpulse-v0.1.1-darwin-arm64.tar.gz
xattr -dr com.apple.quarantine ClashPulse.app
```

On Windows, unzip and run `clashpulse.exe`.

## Arch Linux

`packaging/aur/` has `clashpulse` (builds the tagged source) and
`clashpulse-bin` (repackages the release). Both depend on AUR `mihomo` or
`mihomo-bin`.

```sh
cd packaging/aur/clashpulse
makepkg -si
```

## Mihomo

Put `mihomo` on `PATH` (the default `binary = "system"`) or pick a path in
Settings > Mihomo binary. ClashPulse checks its version and capabilities
first. Steady stream needs pinned listener support (verified with v1.19.31).

## Build from source

Go 1.26.6, a C compiler, and Fyne's graphics headers. On Debian/Ubuntu:

```sh
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libgl1-mesa-dev xorg-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev libwayland-dev libxkbcommon-dev wayland-protocols
git clone https://github.com/fishman/clashpulse.git
cd clashpulse
go build -o clashpulse ./cmd/clashpulse
./clashpulse version
```

macOS needs the Xcode Command Line Tools, Windows needs MinGW/GCC. Build on
each target; cross compiling the GUI is not supported. See
[dependencies](dependencies.html).

`make install` installs into `~/.local` (override with `PREFIX=/usr/local`).
`make run` and `make tui` build and start the GUI or TUI.
