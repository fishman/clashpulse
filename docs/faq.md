---
layout: default
title: FAQ
nav_order: 8
---

# FAQ

## Is it ready?

No. v0.1.1 is an unsigned pre-release.

## Will the name change?

Maybe. It is a working name until trademark, namespace, package, and domain
checks are done.

## Does it replace Clash Verge Rev?

No. There is no script, merge, proxy, group, or rule editor, and source
profiles are never edited. ClashPulse picks nodes on top of Mihomo.

## Why is Mihomo not bundled?

Your distribution or Mihomo's releases keep it current. ClashPulse uses
`mihomo` from `PATH` or a path you choose and checks its capabilities; an
unsupported setting is an error naming that setting.

## macOS says the app is damaged

The builds are not notarized. After checking `SHA256SUMS`:

```sh
xattr -dr com.apple.quarantine ClashPulse.app
```

## Can I use it in Chinese?

Yes. The interface is Simplified Chinese when the OS locale is `zh-CN` (also
`zh_CN.UTF-8` and `zh-Hans-CN`). Proxy names, URLs, and credentials stay as
they are. Chinese text uses your system fonts. See the
[Chinese docs](zh-CN/).

## Where are the files?

| OS | config | private state |
| --- | --- | --- |
| Linux | `~/.config/clashpulse` (or `$XDG_CONFIG_HOME/clashpulse`) | `~/.local/state/clashpulse` (or `$XDG_STATE_HOME/clashpulse`) |
| macOS | `~/Library/Application Support/clashpulse` | `~/.local/state/clashpulse` |
| Windows | `%AppData%\clashpulse` | `.local\state\clashpulse` in your user profile |

See [configuration](configuration.html).

## Which platforms are tested?

CI builds and tests every push natively on `ubuntu-22.04`, `macos-latest`
(arm64), and `windows-latest`, and runs `clashpulse version`. The Linux job
fails if the binary needs more than baseline x86-64 or glibc 2.35, is not a
PIE, or lacks full RELRO.

## The TUI says no service is running

`clashpulse tui` only attaches. Start the GUI or
`clashpulse activate <profile.yaml>` first.

## The tray icon is missing on GNOME

The tray uses StatusNotifier. GNOME may need the
[AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/).

## Does it phone home?

No telemetry, crash upload, or cloud backend. See
[privacy and security](security.html).

## How do I report a bug?

Open an [issue](https://github.com/fishman/clashpulse/issues). The activity
view (GUI Overview > View activity, TUI `~`) is safe to share. Do not paste
subscription URLs, node names, server addresses, or credentials; use
`example.com` names.
