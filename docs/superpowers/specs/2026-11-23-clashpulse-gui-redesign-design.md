# GUI redesign design

Date: 2026-11-23. Status: design approved in chat (approach B); spec awaiting
review.

## Goal

Restyle the Fyne desktop UI toward the look of Surge Mac's Activity window:
sidebar navigation, a status top bar, and a card dashboard. Show only data the
snapshot already carries. No new data collection, no traffic stream, no new
IPC fields, no new dependency. Fyne stays at the pinned v2.8.2 fork.

User decisions: approach B (custom theme plus new layout), restyle only,
light/dark follows the system.

## Non-goals

- Live upload/download rates, connection counts, traffic charts.
- Custom-drawn replacements for Fyne controls (entries, selects, dialogs).
- Translucent sidebar, merged title bar, drop shadows: Fyne cannot draw them.
- Changes to editors, dialogs, validation, commands, or the tray menu beyond
  picking up the theme.

## Theme (`ui/theme.go`)

One `theme.Theme` implementation, set once in `Run` with
`a.Settings().SetTheme` right after `app.NewWithID` and before the window is
built. It wraps `theme.DefaultTheme()` and overrides only:

| Name | Light | Dark |
|---|---|---|
| Background | #F5F5F7 | #1C1C1E |
| Card surface (custom name) | #FFFFFF | #2C2C2E |
| Sidebar surface (custom name) | #ECECEF | #232325 |
| Separator / input border | #E0E0E4 | #3A3A3C |
| Primary (accent, selection) | #0A84FF | #0A84FF |
| Success / warning / error | #34C759 / #FF9F0A / #FF453A | same |
| Foreground | #1D1D1F | #F5F5F7 |
| Muted text (placeholder, labels) | #86868B | #98989D |

Sizes: padding 6, inner padding 10, input and selection radius 8, card radius
14 (custom), text 14, caption 11 (custom), card value 32 (custom). Colors are
chosen from the `variant` argument Fyne passes in, so the UI keeps following
the system light/dark setting live, as today. There is no theme setting and
this design adds none.

Custom names are package constants with a `clashpulse` prefix. Colors are
`color.NRGBA` literals; no runtime parsing.

## Shell (`ui/ui.go`)

- Replace the view `widget.Select` with a sidebar: a fixed-width (190) column
  on the sidebar surface. Entries, in order: Overview, Proxies; section
  caption SOURCES; Subscriptions, Filter Lists, Data Resources; Settings pinned
  to the bottom. Each entry is a theme icon plus localized label; the selected
  entry has a rounded accent tint. Implemented with plain containers and a
  small tappable entry widget, not `widget.List`: six fixed rows need no
  virtualization.
- `showView(name)` keeps its contract (same view IDs, same localized labels);
  it updates the sidebar selection instead of the select.
- Top bar above the page: page title (26 pt bold), then right-aligned status
  pills: Mihomo (running/stopped from `ServiceRunning`), System proxy
  (`SystemProxy.Active`), TUN (`Tun.Active`); green dot when active, muted
  when off, red when the matching last failure is set. A service menu button
  (`theme.MoreVerticalIcon`) opens a popup menu with Start, Stop, Restart,
  Reload configuration, View activity: the same commands the Overview buttons
  send today. The disconnected/connection label moves into the top bar.
- Pills and title update only from `postSnapshot`; a pill whose state is
  unchanged is not refreshed.

## Card primitive (`ui/card.go`)

`newCard(caption string, body fyne.CanvasObject) *fyne.Container`: a
`container.NewStack` of a rounded `canvas.Rectangle` (card surface, separator
stroke 1, card radius) and a padded VBox of an uppercase caption
(`canvas.Text`, caption size, muted) and the body. Plus `newValue(text, unit)`
for a large number with a smaller muted unit, and `newMeter(fraction, color)`
for an 8 px rounded bar. These are the only new helpers; each is used by at
least two cards or pages.

## Overview

Facts row (caption above value): PROFILE (active subscription name from
`ActiveSource`, or the local file name), MODE (`ProxyMode`), SWITCH POLICY
(`Monitor.SwitchPolicy`, localized), MIHOMO (observed version plus desired
binary).

Cards, two-column grid that collapses to one column below 760 px:

1. Current node: group label in the caption; node label (masked label from
   the snapshot); latency value; `ProxySnapshot.StabilityLabel` line; automation
   state. Node is the selected member of the first managed group, else the
   first group.
2. Steady stream: shown only when `Monitor.SwitchPolicy` is steady stream and
   the node has stream samples. Value KB/s, `StreamLabel`, meter of
   completed/samples (success color, warning below 100%, error at 0).
   Otherwise the card shows the URL-test settings summary instead.
3. Last switch (full width): relative time, `old -> new` by name, localized
   reason, evidence line. Empty state: "No switch yet".
4. Subscription usage: active subscription; used of total GB, expiry date,
   meter. Hidden when the active source has no usage metadata.
5. Health: counts of subscriptions, resources, filters; status dot plus
   "No issues" or the first service error / compatibility failure. Tapping it
   opens the activity window.

The existing compatibility and capability rows move to Settings > Binary,
where they already exist; Overview keeps only the version in the facts row.

## Names instead of IDs

Extract `core.ProxyLabel(proxies []ProxySnapshot, id string) string` from
`core.DiagnosticSource`: masked row label when a row carries the ID, else the
8-character prefix. `DiagnosticSource` calls it. The Overview last-switch card
and the TUI switch summary (`tui/model.go`, replacing `shortProxyID`) use it,
so a node that left the snapshot after a refresh still renders as a prefix,
never as a raw name from elsewhere.

## List pages

- Proxies: unchanged structure (group list left, node list right). Node rows
  become: selection dot, label, latency badge (rounded rect; success at or below
  `AlertThresholdMillis` (default 250 ms), warning at or below
  `ThresholdMillis` (default 800 ms), error above it or on timeout/error,
  muted when unmeasured), stability label, stream label in muted
  text. Rows stay in `widget.List`; badge colors change via the existing
  update path, no rebuild.
- Subscriptions, Filter Lists, Data Resources: rows get a status dot
  (enabled+validated green, disabled muted, last failure red), name, source
  host, last check and next due in muted text. Subscriptions add a usage meter
  when usage is present. Row actions and editors are unchanged. Page toolbars
  use the theme's primary button for Add.
- Settings: keeps `settingsLayout` and its section list, restyled as a
  secondary sidebar on the card surface.

## Performance

No timers, animations, or per-frame work. Relative times ("12 min ago") are
computed when a snapshot arrives, not ticked. Every widget update compares the
new text/color with the current one before calling `Refresh`.

## Testing

- Update tests that drive `viewSelect` (`ui/errors_test.go`,
  `ui/locale_test.go`) to the sidebar: labels in order per locale, selected
  entry after `showView`.
- New: `core.ProxyLabel` table test (row label, masked label, missing row ->
  prefix, short ID); TUI switch summary shows a label.
- New: Overview card logic as pure functions tested without a display:
  current-node choice, stream card visibility, latency badge color by
  threshold, usage fraction and hidden state, pill state.
- Existing UI tests stay green in English and zh_CN; `go vet` with the ci and
  screenshots tags.
- Regenerate GUI screenshots (`make screenshots`) and visually check light and
  dark Overview and Proxies; update docs images.
