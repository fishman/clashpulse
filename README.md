# ClashPulse

English | [简体中文](README.zh-CN.md)

ClashPulse is a lightweight cross-platform Go desktop client for [Mihomo](https://github.com/MetaCubeX/mihomo): one binary, low idle CPU, no polling or busy loops; strict typed TOML across four shallow files that reload once validation passes after a hand edit; and continuous node latency measurement that switches only when a better node is proven better, with every threshold under user control. It is not a Clash Verge Rev feature clone or a general-purpose proxy panel.

## Build

You need Go 1.26.6, a C compiler, and Fyne's native graphics headers. On Debian/Ubuntu:

```sh
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libgl1-mesa-dev xorg-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev
go build -o clashpulse ./cmd/clashpulse
./clashpulse version
```

The source build reports `clashpulse dev`. macOS needs Xcode Command Line Tools; Windows needs MinGW/GCC and `go build -o clashpulse.exe ./cmd/clashpulse`. See [desktop build requirements](docs/dependencies.md) for target details.

`make install` copies the binary, the icon, and the desktop entry into `~/.local` (`PREFIX=/usr/local` and similar override the prefix). The desktop entry is Linux-only.

## Run

```sh
./clashpulse
```

The GUI starts without a proxy profile. Mihomo is a separate executable: put `mihomo` on `PATH`, or choose an absolute executable path in Settings. No Mihomo binary is bundled.

The GUI needs a graphical desktop session (X11 or Wayland on Linux). Its tray uses StatusNotifier; GNOME may need the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/) to show it.

There is one ClashPulse service and one ClashPulse window per machine. If a service already runs (started by the GUI, `clashpulse tui`, or `clashpulse activate`), `./clashpulse` opens its window against that service rather than starting a second one; if a window already runs, `./clashpulse` reports it and exits instead of opening another. The window is an IPC client, so closing it leaves a service it did not start running. Terminal clients are unaffected: any number of `clashpulse tui` sessions may attach.

In the GUI, add an HTTPS subscription, refresh it to fetch and validate the candidate profile, then activate it. Open a second terminal for `./clashpulse tui`; the TUI connects to the running desktop service and does not start one itself. On Windows use `.\clashpulse.exe` and `.\clashpulse.exe tui`.

The GUI, TUI, and CLI use Simplified Chinese when the operating-system locale is `zh-CN` (including `zh_CN.UTF-8` and `zh-Hans-CN`); other locales retain English. Translation catalogs are embedded in the binary, not loaded from the profile or state directory. Machine IDs, proxy names, source URLs, and credentials are never translated. Chinese glyph rendering uses the system's installed fonts; ClashPulse does not bundle or select a CJK font.

To run a local profile without the GUI:

```sh
./clashpulse activate 'my profile.yaml'
```

The command reads the file once, validates it with the selected Mihomo, and prints `local profile active; press Ctrl-C to stop` only after controller readiness. It owns the same local IPC service, so the TUI can connect while it runs. Ctrl-C stops Mihomo and resets System Proxy settings to the desktop defaults. The source is never edited or imported as a subscription; its path and proxy credentials are not shown over IPC. The private generated config is removed on shutdown. Activation failures print a fixed, credential-safe stage and exit nonzero.

The GUI and TUI Overview show **Generated config changes** for the active profile. Each entry names an app-managed field, whether it was added, replaced, or removed, and a fixed reason. The report is not a full YAML diff: it carries no source or generated values, credentials, network endpoints, or URLs. Unchanged fields are omitted; a stopped service shows no active override report.

With the desktop running, these commands hand the work to its service and print each failure; the service validates what it downloads and never activates it. With no service running they take the private-state lock and work directly.

```sh
./clashpulse download subscription [id]
./clashpulse refresh
./clashpulse refresh resource [id]
```

`download subscription` fetches and parses a profile (`proxies` or `proxy-providers` required), then stores it as an inactive private snapshot. It does not inspect or run Mihomo. Activation renders the profile, validates the complete generated config with the selected Mihomo, and applies it transactionally. `refresh resource [id]` also works without an active profile: it caches enabled resources under stable filenames in the private state directory's `resources/` root, without starting Mihomo. When a profile is active, Mihomo stops before those files change and restarts only after validation; a failed update restores the prior files and runtime. Remote sources with ETag or Last-Modified use conditional requests, so an unchanged `304` does not download a body or replace a file. Without server validators, the body must be fetched to detect changes. A later profile refresh or activation still validates the generated config before applying it. Without an ID, each command handles all enabled sources in its scope.

Add `--show-response` to print up to 4 KiB of printable HTTP response text to local stderr on failure. It needs the desktop closed, because response bodies never leave the service. URLs and response bodies remain hidden by default.

A download must contain at least one proxy or proxy provider; an empty profile (how providers often answer an expired account) is a failure, and the last good snapshot stays. Downloads use the subscription's `route`: the default `auto` goes through the running Mihomo. While Mihomo is stopped, terminal `download` and `refresh` honor `http_proxy`, `https_proxy`, and `no_proxy`, and the desktop service connects directly. Data resources always use `auto`.

Press `?` in TUI views for keyboard help (`F1` inside editors), or `~` to view and scroll current-session activity; `q` closes either overlay without quitting. In the GUI, use Overview > View activity. Both activity views show only sanitized IPC diagnostics, not subscription URLs or credentials.

Settings has Mihomo binary, System Proxy, Monitor, and DNS sections. Its sidebar becomes a dropdown when the sidebar and selected section cannot fit side by side.

### Choosing a node automatically

ClashPulse measures every node in an automated group and switches by `switch_policy` in `config.toml [monitor]` (GUI Settings > Monitor, TUI Settings):

- **Fastest** (`lowest_latency`, default): every round, each node answers a tiny HTTP request through Mihomo. The group moves to a node whose median latency is lower by at least `min_improvement`. Good for browsing; a node can be fast to answer yet drop long downloads.
- **Failover** (`failover`): stays on the current node until it fails or exceeds `threshold` for `consecutive_bad_samples` rounds.
- **Steady stream** (`steady_stream`): every `stream_interval`, ClashPulse downloads a 256 KiB HTTPS file (`stream_url`) through the current node and the 3 fastest others, one at a time, and records whether each download finished, reset, stalled, or was cut short. It leaves a node only after 2 of its last 5 downloads broke, for a node with fewer broken downloads and at least 3 completed ones; ties go to the faster download. Good for streams, calls, and large transfers. Costs about 12 MB per hour for one automated group, at most 24 MB per hour; a node shared by groups is downloaded once. A node that stops answering latency probes still leaves under the Failover rule, without waiting for a download round.

Steady stream pulls through a private loopback listener that only ClashPulse can use; your selected node and rules are untouched. It requires a Mihomo whose capability check confirms pinned listeners (v1.19.31 verified). Every policy honors cooldown, manual selection, and the all-nodes-failed stop.

The tray's Proxies menu selects a member of an active managed `select` group; its System Proxy item changes the requested OS setting. Controls return when the local IPC service reconnects.

First start seeds `config.toml`, `subscriptions.toml`, `resources.toml`, and `filters.toml` in the platform config directory (`~/.config/clashpulse` on Linux unless `$XDG_CONFIG_HOME` is set). The four reviewed data sources are enabled by default; the exact previous disabled resource seed is migrated once, while customized resource files are left alone. Resources download during validated profile refresh/activation or `refresh resource`; `download subscription` remains profile-only. The `cn` list is downloaded but not routed until configured. Subscription and filter examples remain commented out.

Proxy-delay tests default to Clash Verge's `http://cp.cloudflare.com/generate_204`, through Mihomo. Plain HTTP can be intercepted; change the URL in Settings if that matters.
