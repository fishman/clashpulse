---
layout: default
title: Installation
nav_order: 2
---

# Installation

ClashPulse is a front-end: it needs a separately installed
[Mihomo](https://github.com/MetaCubeX/mihomo) executable. No Mihomo binary
is bundled with any build.

## Release downloads

The current release is **v0.1.1**, a pre-release, on the
[releases page](https://github.com/fishman/clashpulse/releases). The release
workflow builds each platform natively from a `v*` tag:

| platform | asset | contents |
| --- | --- | --- |
| Linux x86-64 | `clashpulse-v0.1.1-linux-amd64.tar.gz` | `clashpulse`, `clashpulse.desktop`, README, LICENSE, THIRD_PARTY_LICENSES.txt |
| macOS Apple silicon | `clashpulse-v0.1.1-darwin-arm64.tar.gz` | `ClashPulse.app` |
| Windows x86-64 | `clashpulse-v0.1.1-windows-amd64.zip` | `clashpulse.exe`, README, LICENSE, THIRD_PARTY_LICENSES.txt |

The builds are unsigned.

### Verify the download

Each release carries a `SHA256SUMS` file covering every asset. Download it
next to the archive and check:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

### Linux

The Linux binary needs only baseline x86-64 and glibc 2.35 or newer
(Ubuntu 22.04, Debian 12, and later). CI builds it on Ubuntu 22.04 and
fails the build if either floor rises; it is also a PIE with full RELRO.

```sh
tar -xzf clashpulse-v0.1.1-linux-amd64.tar.gz
./clashpulse version
```

The GUI needs a graphical session (X11 or Wayland). Its tray uses
StatusNotifier; GNOME may need the
[AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/)
to show it.

### macOS

The app is not notarized. Remove the quarantine attribute before the first
start:

```sh
tar -xzf clashpulse-v0.1.1-darwin-arm64.tar.gz
xattr -dr com.apple.quarantine ClashPulse.app
```

### Windows

Unpack the zip and run `clashpulse.exe`; the terminal client is
`.\clashpulse.exe tui`.

## Arch Linux

`packaging/aur/` in the repository holds two prepared AUR packages, both
x86_64 and at version 0.1.1:

- `clashpulse` builds the tagged source and runs the test suite in
  `check()`.
- `clashpulse-bin` repackages the Linux release build.

Both depend on `mihomo`, which the AUR `mihomo` or `mihomo-bin` package
provides, so the default `binary = "system"` finds `/usr/bin/mihomo`. Build
one locally inside its directory:

```sh
cd packaging/aur/clashpulse
makepkg -si
```

Both install the binary to `/usr/bin/clashpulse` along with the desktop
entry and the icon.

## Mihomo

Install Mihomo with your platform's package manager or from its releases,
then either put `mihomo` on `PATH` (the default `binary = "system"` setting)
or select an absolute executable path in Settings > Mihomo binary. ClashPulse
inspects the selected binary's version and capabilities before it starts it
or generates a configuration for it. The Steady stream switch policy needs a
Mihomo whose capability check confirms pinned listeners (v1.19.31 verified).

## Build from source

You need Go 1.26.6, a C compiler, and Fyne's native graphics headers. On
Debian/Ubuntu:

```sh
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libgl1-mesa-dev xorg-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev libwayland-dev libxkbcommon-dev wayland-protocols
git clone https://github.com/fishman/clashpulse.git
cd clashpulse
go build -o clashpulse ./cmd/clashpulse
./clashpulse version
```

The source build reports `clashpulse dev`. macOS needs Xcode Command Line
Tools; Windows needs MinGW/GCC and
`go build -o clashpulse.exe ./cmd/clashpulse`. Native builds must run on
each target: cross-compiling the desktop GUI is not a supported release
path. See [dependencies](dependencies.html) for the pinned module graph.

`make install` copies the binary, the icon, and the desktop entry into
`~/.local`; `PREFIX=/usr/local` and similar override the prefix. The desktop
entry is Linux-only.

| target | action |
| --- | --- |
| `make build` | build `./clashpulse` |
| `make install` | build and install into `$(PREFIX)` |
| `make test` | `go test -tags ci ./...` |
| `make vet` | `go vet -tags ci ./...` |
| `make run` | build and start the GUI |
| `make tui` | build and start the TUI |
