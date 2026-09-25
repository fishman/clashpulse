# ClashPulse

The GUI starts without a proxy profile. Mihomo is a separate executable: put `mihomo` on `PATH`, or choose an absolute executable path in Settings. No Mihomo binary is bundled.

## Build

You need Go 1.26.6, a C compiler, and Fyne's native graphics headers. On Debian/Ubuntu:

```sh
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libgl1-mesa-dev xorg-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev
go build -o clashpulse ./cmd/clashpulse
./clashpulse version
```

The source build reports `clashpulse dev`. macOS needs Xcode Command Line Tools; Windows needs MinGW/GCC and `go build -o clashpulse.exe ./cmd/clashpulse`. See [desktop build requirements](docs/dependencies.md) for target details.

## Run

```sh
./clashpulse
```

The GUI needs a graphical desktop session (X11 or Wayland on Linux). Its tray uses StatusNotifier; GNOME may need the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/) to show it.

In the GUI, add an HTTPS subscription, refresh it, then activate the downloaded profile. Open a second terminal for `./clashpulse tui`; the TUI connects to the running desktop service and does not start one itself. On Windows use `.\clashpulse.exe` and `.\clashpulse.exe tui`.

Press `~` in the TUI to view and scroll current-session activity; press `q` to close it. In the GUI, use Overview > View activity. Both show only sanitized IPC diagnostics, not subscription URLs or credentials.

Settings has Mihomo binary, System Proxy, Monitor, and DNS sections. Its sidebar becomes a dropdown when the sidebar and selected section cannot fit side by side.

The tray's Proxies menu selects a member of an active managed `select` group; its System Proxy item changes the requested OS setting. Controls return when the local IPC service reconnects.

First start seeds `config.toml`, `subscriptions.toml`, `resources.toml`, and `filters.toml` in the platform config directory (`~/.config/clashpulse` on Linux unless `$XDG_CONFIG_HOME` is set). Existing files are never replaced. The seeds come from [`examples/`](examples/); data URLs are disabled until you enable them, and subscription/filter examples remain commented out.

Proxy-delay tests default to Clash Verge's `http://cp.cloudflare.com/generate_204`, through Mihomo. Plain HTTP can be intercepted; change the URL in Settings if that matters.
