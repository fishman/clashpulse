---
layout: default
title: FAQ
nav_order: 8
---

# FAQ

## Is ClashPulse ready?

It is a pre-release. v0.1.1 is published as a GitHub pre-release with
unsigned native builds for Linux (amd64), macOS (Apple silicon), and Windows
(amd64).

## Will the name change?

Possibly. **ClashPulse** is a working name: it carries the familiar Clash
ecosystem name and the product promise - continuously measure proxy health
and react before latency becomes user-visible. Trademark, GitHub namespace,
package-name, and domain checks are still due before a public release, and
the project is renamed if any of them conflicts.

## Is it a Clash Verge Rev replacement?

No. It is not a feature clone or a general-purpose proxy panel. There is no
script, merge, proxy, group, or rule editor; subscriptions are managed
directly and source profiles are never edited. The focus is trustworthy
node selection on top of Mihomo.

## Why is Mihomo not bundled?

Mihomo is the data plane, and your distribution or Mihomo's own releases
keep it updated. ClashPulse uses the system Mihomo by default
(`binary = "system"`, resolved from `PATH`) or an executable path you
select, and inspects its version and capabilities before using it. A
setting the selected Mihomo cannot support is reported as a compatibility
error naming the setting, not silently dropped. On Arch Linux the prepared
packages depend on the AUR `mihomo` or `mihomo-bin` package.

## macOS says the app is damaged or from an unidentified developer

The builds are unsigned and the app is not notarized. Remove the
quarantine attribute:

```sh
xattr -dr com.apple.quarantine ClashPulse.app
```

Verify every download against the release's `SHA256SUMS` first.

## Can I use it in Chinese?

Yes. The GUI, TUI, and CLI use Simplified Chinese when the operating-system
locale is `zh-CN` (including `zh_CN.UTF-8` and `zh-Hans-CN`); other locales
use English. Translations are embedded in the binary. Machine IDs, proxy
names, source URLs, and credentials are never translated. Chinese glyphs
use the system's installed fonts; ClashPulse does not bundle a CJK font.

## Where does it keep its files?

| OS | config | private state |
| --- | --- | --- |
| Linux | `~/.config/clashpulse` (or `$XDG_CONFIG_HOME/clashpulse`) | `~/.local/state/clashpulse` (or `$XDG_STATE_HOME/clashpulse`) |
| macOS | `~/Library/Application Support/clashpulse` | `~/.local/state/clashpulse` |
| Windows | `%AppData%\clashpulse` | `.local\state\clashpulse` in your user profile |

The config directory holds the four TOML files you edit; private state
holds snapshots, generated config, and cached resources. See
[configuration](configuration.html).

## Which platforms are tested?

CI builds every push and pull request natively on three runners:

| runner | target |
| --- | --- |
| `ubuntu-22.04` | Linux amd64 |
| `macos-latest` | macOS arm64 |
| `windows-latest` | Windows amd64 |

Each runs `go test -tags ci` and `go vet -tags ci`, builds the native
package, and smoke-runs `clashpulse version`. The Linux job also fails if
the binary needs more than baseline x86-64 or glibc 2.35, is not a PIE, or
lacks full RELRO. A `v*` tag runs the same matrix and publishes the release
with `SHA256SUMS`.

## The TUI says no service is running

`clashpulse tui` is a client, not a service: it connects to an
already-running service and reports an error when there is none. Start the
GUI, or run `clashpulse activate <profile.yaml>`, then attach.

## The tray icon does not show on GNOME

The tray uses StatusNotifier. GNOME may need the
[AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/).

## Does ClashPulse send anything home?

No. There is no telemetry, crash upload, or cloud backend. See
[privacy and security](security.html).

## How do I report a bug?

Open an issue on the
[repository](https://github.com/fishman/clashpulse/issues). The activity
view (GUI Overview > View activity, TUI `~`) shows sanitized diagnostics
that are safe to share. Never paste a subscription URL, node name, server
address, or credential; reproduce with `example.com` names instead.
