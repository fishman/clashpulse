---
layout: default
title: Installation
nav_order: 2
---

# Installation

ClashPulse runs [Mihomo](https://github.com/MetaCubeX/mihomo). macOS and
Windows release packages after v0.1.1 include a pinned official Mihomo build;
on Linux, with Homebrew, and in source builds you install Mihomo yourself.

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

On Windows, unzip and run `clashpulse.exe`. The amd64 package also starts on
Windows on ARM under x64 emulation (CI runs `clashpulse.exe version` and
`mihomo.exe -v` there; the GUI is not tested on ARM). There is no native
Windows arm64 build yet.

## Homebrew (macOS)

```sh
brew install fishman/clashpulse/clashpulse
clashpulse
```

The formula builds the tagged source on your Mac, so Gatekeeper does not
block it, and installs Homebrew's `mihomo` as a dependency. It installs the
`clashpulse` command, not an app in /Applications.

## Arch Linux

`packaging/aur/` has `clashpulse` (builds the tagged source) and
`clashpulse-bin` (repackages the release). Both depend on AUR `mihomo` or
`mihomo-bin`.

```sh
cd packaging/aur/clashpulse
makepkg -si
```

## Mihomo

Release packages for macOS and Windows default to `binary = "bundled"`: the
Mihomo inside the package, which ClashPulse checks against the digest recorded
at build time, copies into its private state, and runs from there. Other
builds default to `binary = "system"`, the `mihomo` on `PATH`. You can pick
either, or an executable path, in Settings > Mihomo binary. ClashPulse checks
the version and capabilities first. Steady stream needs pinned listener
support (verified with v1.19.31).

The bundled Mihomo changes only with a ClashPulse release. The package
includes its GPL-3.0 licence and provenance in `licenses/`, and each release
attaches the matching Mihomo source.

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
