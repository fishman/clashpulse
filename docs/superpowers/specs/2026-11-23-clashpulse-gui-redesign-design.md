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

Fyne's live theme change only calls `Refresh()` on each object, and a raw
`canvas.Rectangle` or `canvas.Text` keeps the color it was built with. Cards,
pills, badges, meters, and muted text are therefore small widgets that store
theme color *names* and re-read them in their renderer's `Refresh()`. A test
flips the variant and checks the colors follow.

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

Cards, two-column grid (no width collapse: Fyne's adaptive grid keys on device
orientation, not width). The default window grows from 760x640 to 980x700 so
the sidebar plus two card columns fit:

1. Current node: group label in the caption; node label (masked label from
   the snapshot); latency value; `ProxySnapshot.StabilityLabel` line; automation
   state. Node is the selected member of the first managed group, else the
   first group.
2. Steady stream: shown only when `Monitor.SwitchPolicy` is steady stream and
   the node has stream samples. Value KB/s, `StreamLabel`, meter of
   completed/samples (success color, warning below 100%, error at 0).
   Otherwise the card shows the URL-test settings summary instead.
3. Last switch (full width): absolute time via the existing `detailTime`
   (`ui/display.go`), `old -> new` by name, localized reason, evidence line.
   Empty state: "No switch yet". No relative times anywhere: without a ticker
   they go stale while idle.
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
  muted when unmeasured). With `AlertThresholdMillis <= 0` (not configured),
  success at or below `ThresholdMillis`, error above it., stability label, stream label in muted
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

No timers, animations, or per-frame work. Times are absolute. Every widget
update compares the new text/color with the current one before calling
`Refresh`.

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
- `ui/docs_screenshot_test.go` sets its own theme on `test.NewApp()`, so the
  theme from `Run` never reaches it. Replace `lightTheme{theme.DefaultTheme()}`
  with a variant-pinning wrapper around the new theme, used for a light and a
  dark pass. Regenerate GUI screenshots (`make screenshots`), visually check
  light and dark Overview and Proxies, and update docs images (light images
  stay the docs default).

## Settings panes (approved 2026-11-23)

- Each pane is one or more grouped cards with an uppercase muted caption
  above the card. Inside a card: one setting per row, name left,
  value/control right-aligned, a separator between rows, optional help text
  (caption size, muted) under the row it explains. No bold pane heading.
  Buttons are normal width, right-aligned. Checkboxes sit at the right of
  their row. The section list has a fixed width.
- Mihomo binary: card Version (Desired, Observed, Capabilities,
  Compatibility). Card Binary: row Use with a Select (System, Bundled) that
  sends the existing binary command only on user change, and shows a custom
  path when Desired is a path; row Executable path with "Choose path...".
- System Proxy: Enable system proxy, Mixed port (value + Change), Proxy mode
  (Select); status and port explanation as help text.
- TUN: Enable TUN, Stack, DNS mode; status/privilege text as help under
  Enable.
- Monitor: card Latency monitor (Enable, Monitor interval, Alert threshold +
  Change, Probe policy Edit...). Card Switching (Switch policy short name with
  its description as help, Switch stability, Mihomo url-test delay + Change).
  Card Notifications (Send test).
- DNS: card Listener (value + Change). Cards Resolver sets and DNS routes,
  each with Add right-aligned in its caption row and one row per entry with
  Edit and Remove on the right (resolver: id, endpoints muted, DNSCrypt
  marker; route: matcher -> resolver set). Empty rows: "No resolver sets",
  "No DNS routes". Replaces the Select plus disabled buttons.
- Sidebar entries (main and Settings) are focusable: Tab moves focus, Space
  and Return select, focus is drawn.
- Tests: binary Select sends only on user change; DNS Edit/Remove act on
  their own row's entry (two entries, act on the second); entry keyboard
  selection; panes expose their rows and actions.
