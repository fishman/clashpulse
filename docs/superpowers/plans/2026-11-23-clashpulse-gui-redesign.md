# GUI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the Fyne desktop UI (theme, sidebar shell, card Overview, list rows) using only data the snapshot already carries.

**Architecture:** A custom `theme.Theme` in `ui/theme.go`, three small card helpers in `ui/card.go`, a sidebar/top-bar shell in `ui/ui.go`, an Overview built from pure model functions in `ui/overview.go`, and restyled rows in `ui/views.go`. `core.ProxyLabel` becomes the one ID-to-label lookup for GUI, TUI, and diagnostics.

**Tech Stack:** Go 1.26.6, Fyne v2.8.2 (pinned fork via `replace`), existing `localize` catalogs.

**Spec:** `docs/superpowers/specs/2026-11-23-clashpulse-gui-redesign-design.md`

## Global Constraints

- No new dependency, no new IPC/snapshot field, no new config setting.
- No timers, tickers, animations; times are absolute (`detailTime`).
- Fyne calls only on the UI thread (inside `postSnapshot`'s `fyne.Do` or constructors).
- Every widget update compares old vs new text/color before `Refresh`/`SetText`.
- Every new user-visible string goes through `localize.T` and gets a zh-CN entry in `localize/locale/gui.zh-CN.toml` (and `gui.en.toml` if that catalog lists GUI ids).
- Node names shown in the UI come from `ProxySnapshot.Label` (already masked) or `core.ProxyLabel`; never from another source.
- Tests: fixtures use reserved names only (`example.com`, `Example Tokyo 01`). Run with `-tags ci`.
- ASCII in code; conventional commits `type(scope): subject`; no AI co-author lines.

## Review Focus

1. Empty snapshot (first paint, disconnected, no groups/subscriptions): Overview shows empty states, never panics on index 0. Test in Task 4.
2. Switch event whose old/new node left `Proxies` after a refresh: card shows 8-char prefix. Test in Task 1 and Task 4.
3. Usage with `TotalBytes == 0` or nil `Usage`: usage card hidden, no divide by zero. Test in Task 4.
4. zh_CN locale: sidebar labels and new card captions translated; existing locale tests stay green. Test in Task 3.
5. Latency badge with unmeasured node (`Samples == 0`, outcome empty) or Mihomo-only latency: muted, not green. Test in Task 5.

---

### Task 1: `core.ProxyLabel`

**Files:** Modify `core/snapshot.go` (DiagnosticSource), `ui/display.go` (lastSwitchSummary, remove `shortID` if unused), `tui/model.go` (replace `shortProxyID`). Test: `core/snapshot_test.go` (or the existing core test file holding DiagnosticSource tests), `tui/model_test.go`.

**Interfaces:** Produces `func ProxyLabel(proxies []ProxySnapshot, id string) string`.

- [ ] Step 1: failing table test: row with label -> label; row without label -> prefix; missing row with 64-char ID -> first 8; short ID -> itself; empty proxies -> prefix.
- [ ] Step 2: run `go test -tags ci ./core -run TestProxyLabel` -> FAIL (undefined).
- [ ] Step 3: implement; `DiagnosticSource` returns `ProxyLabel(proxies, diagnostic.SourceID)` for `stream_probe`.

```go
// ProxyLabel names a node for display: its masked row label, or the
// 8-character ID prefix when no row carries it.
func ProxyLabel(proxies []ProxySnapshot, id string) string {
	for _, proxy := range proxies {
		if proxy.ID == id && proxy.Label != "" {
			return proxy.Label
		}
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
```

- [ ] Step 4: TUI switch summary and `ui.lastSwitchSummary` use `core.ProxyLabel(snapshot.Proxies, id)`; delete `shortProxyID`/`shortID` if no caller remains. Add a TUI test: switch with labeled rows renders labels.
- [ ] Step 5: `go test -tags ci ./core ./tui ./ui` green (English and `LANGUAGE=zh_CN`). Commit `refactor(core): share proxy label lookup for switch summaries`.

### Task 2: Theme and card helpers

**Files:** Create `ui/theme.go`, `ui/card.go`, `ui/theme_test.go`. Modify `ui/ui.go` (`Run`: `a.Settings().SetTheme(newTheme())` after `app.NewWithID`; drop the obsolete comment), `ui/docs_screenshot_test.go` (variant wrapper around `newTheme()`).

**Interfaces:** Produces:
- `func newTheme() fyne.Theme` (type `clashTheme struct{ fyne.Theme }` wrapping `theme.DefaultTheme()`).
- Color names `colorNameCard`, `colorNameSidebar` (`fyne.ThemeColorName("clashpulseCard")`, `"clashpulseSidebar"`); size names `sizeNameCardRadius`, `sizeNameCaption`, `sizeNameValue`.
- `func newCard(caption string, body ...fyne.CanvasObject) *fyne.Container`
- `func newValue(value, unit string) (*fyne.Container, *canvas.Text, *canvas.Text)` (returns texts for updates)
- `type meter struct` with `newMeter() *meter`, `(m *meter) set(fraction float64, color fyne.ThemeColorName)`, field `object fyne.CanvasObject`. Fraction clamped to [0,1]; no-op when unchanged.
- `func pill(label string) *statusPill` with `(p *statusPill) set(state pillState)`; `pillState` is `pillOff|pillOn|pillFailed`.

Theme values exactly as the spec table: background, card, sidebar, separator/input border, primary, success/warning/error, foreground, placeholder/muted. Sizes: `theme.SizeNamePadding` 6, `SizeNameInnerPadding` 10, `SizeNameInputRadius` 8, `SizeNameSelectionRadius` 8, `SizeNameText` 14, card radius 14, caption 11, value 32. Everything else delegates.

- [ ] Step 1: failing tests: `newTheme().Color(theme.ColorNameBackground, theme.VariantLight)` == `#F5F5F7`, dark == `#1C1C1E`; custom card color per variant; `Size(sizeNameCardRadius)` == 14; unknown name delegates to default; `meter.set(1.5, ...)` clamps to full width, `set(-1)` to zero.
- [ ] Step 2: run -> FAIL. Step 3: implement. Step 4: green.
- [ ] Step 5: screenshot test: replace `lightTheme{theme.DefaultTheme()}` with `variantTheme{newTheme(), theme.VariantLight}` (generic variant pin). `go vet -tags 'ci screenshots' ./ui` clean. Commit `feat(ui): add the clashpulse theme and card primitives`.

### Task 3: Sidebar shell and top bar

**Files:** Modify `ui/ui.go`; create `ui/shell.go` (sidebar + top bar); update `ui/errors_test.go:184`, `ui/locale_test.go:26-31`; add `ui/shell_test.go`.

**Interfaces:** Consumes `newTheme` names, `statusPill`. Produces:
- `type sidebar struct` with `newSidebar(onSelect func(id string)) *sidebar`, `(s *sidebar) labels() []string` (localized, in order), `(s *sidebar) selected() string` (view ID), `(s *sidebar) setSelected(id string)`, field `object fyne.CanvasObject`.
- `type topBar struct` with `title *canvas.Text`, pills `mihomo, systemProxy, tun *statusPill`, `connection *widget.Label`, `menu *widget.Button`; `(t *topBar) update(s core.Snapshot)`.
- `func pillStates(s core.Snapshot) (mihomo, systemProxy, tun pillState)` pure.

Sidebar order: Overview, Proxies, caption "SOURCES" (localized "Sources"), Subscriptions, Filter Lists, Data Resources, spacer, Settings. Entry = tappable widget (icon + label) with rounded accent tint (`canvas.Rectangle`, primary color alpha ~0x2E) when selected. Icons: `theme.HomeIcon`, `theme.ComputerIcon`, `theme.DownloadIcon`, `theme.ListIcon`, `theme.StorageIcon`, `theme.SettingsIcon`.

`showView` keeps its contract; it sets `sidebar.setSelected`, the top-bar title (`localize.T(name)`), and swaps `viewStack`. Remove `viewSelect` and the "Choose a view" placeholder (delete its catalog entries if unused).

Pill rules: Mihomo on when `ServiceRunning`, failed when `Binary.LastCompatibilityFailure != ""`; System proxy on when `SystemProxy.Active`, failed when `SystemProxy.Enabled && !SystemProxy.Active && ServiceRunning`; TUN on when `Tun.Active`, failed when `Tun.LastFailure != ""`. Disconnected: all off and connection label "Disconnected".

Menu button (`theme.MoreVerticalIcon`) shows `widget.NewPopUpMenu` with Start service, Stop service, Restart service, Reload configuration, View activity; same commands as today's Overview buttons. Keep `d.connection` text (proxyStatus) as the muted label left of the pills.

- [ ] Step 1: failing tests: `pillStates` table (running/stopped/compat failure/system proxy requested not applied/tun failure); sidebar labels per locale (port the existing en/zh expectations from `locale_test.go`); `showView("Settings")` -> `sidebar.selected()=="Settings"` and title text localized.
- [ ] Step 2: FAIL. Step 3: implement, rewire `newDesktopUI`, `postSnapshot` (call `topBar.update`), `postDisconnected`, `postStatus`.
- [ ] Step 4: `go test -tags ci ./ui` and `LANGUAGE=zh_CN go test -tags ci ./ui` green. Commit `feat(ui): replace the view picker with a sidebar and status bar`.

### Task 4: Overview cards

**Files:** Create `ui/overview.go`, `ui/overview_test.go`; modify `ui/ui.go` (remove `overviewCounts`, `overviewCountValues`, `binarySummary`/`binaryValues` from Overview if Settings already shows them, `switchSummary`, `errorSummary`, Overview buttons; `postSnapshot` calls `d.overview.update(snapshot)`); catalogs.

**Interfaces:** Produces pure model + view:

```go
type overviewModel struct {
	Profile, Mode, Policy, Mihomo string
	Node       nodeCard     // Visible false when no group
	Stream     streamCard   // Visible only for steady stream with samples
	Switch     switchCard   // Empty -> "No switch yet"
	Usage      usageCard    // Visible only with Usage and TotalBytes > 0
	Health     healthCard
}
func buildOverview(s core.Snapshot) overviewModel
type overviewView struct{ object fyne.CanvasObject /* widgets */ }
func newOverviewView(openActivity func()) *overviewView
func (v *overviewView) update(m overviewModel) // compares before SetText/Refresh
```

Rules (from spec): node = selected member of first group with `AutomationEnabled`, else first group's selected; latency text from `LatencyMillis` when outcome success, else `MihomoMillis` with "(mihomo)", else "-"; detail `StabilityLabel()`; automation on/off/manual. Stream card: policy `monitor.SwitchSteadyStream` and node `StreamSamples > 0`; value `StreamKBps`, label `StreamLabel()`, meter fraction completed/samples, color success at 1, warning in (0,1), error at 0. Otherwise card shows "URL test every %d s, timeout %d ms". Switch card: last of `Switches`; title `ProxyLabel(old) -> ProxyLabel(new)`; time `detailTime(At)`; reason `localize.T(Reason)`; evidence via existing summary pieces using `ProxyLabel`. Usage: active subscription (`Active` true); used = Uploaded+Downloaded; GB with one decimal; expiry `detailTime(ExpiresAt)` when > 0. Health: counts of subscriptions/resources/filters; issue text = first `Errors[0]` message via existing `serviceErrors`, else compatibility label when failure set, else "No issues"; dot state accordingly. Profile: active subscription Name, or "Local profile" when `ActiveSource == "local"`, else "None". Mode: `localize.Code("proxy.mode", ProxyMode)` if that namespace exists, else `localize.T`. Policy: the existing localized policy label used by Settings.

Layout: facts row (`GridWithColumns(4)` of caption/value VBoxes); `container.NewGridWrap`-free responsive grid: `container.NewAdaptiveGrid(2, node, stream)`, switch full width, `NewAdaptiveGrid(2, usage, health)`; everything in a `VScroll`. Health card is tappable -> `openActivity`.

- [ ] Step 1: failing tests for `buildOverview`: empty snapshot (no panic, node hidden, "No switch yet", usage hidden, health "No issues"); automated group preferred; stream visibility both ways; usage TotalBytes 0 hidden and nil hidden; switch with old ID missing from Proxies -> 8-char prefix; errors -> health failed.
- [ ] Step 2: FAIL. Step 3: implement model, view, wiring; add catalog entries (en + zh-CN) for every new string.
- [ ] Step 4: `go test -tags ci ./ui` both locales green; any old test asserting removed Overview widgets is updated to the card model (contract) or deleted if it pinned layout only. Commit `feat(ui): show the overview as cards`.

### Task 5: List rows

**Files:** Modify `ui/views.go` (proxy rows, subscription/resource/filter rows, add buttons, settings section list styling); add tests to `ui/proxy_action_test.go` or a new `ui/rows_test.go`.

**Interfaces:** Produces pure helpers:
- `func latencyBadge(p core.ProxySnapshot, m core.MonitorSnapshot) (text string, color fyne.ThemeColorName)`: success latency <= `AlertThresholdMillis` -> success; <= `ThresholdMillis` -> warning; above or outcome timeout/error -> error; unmeasured (no success latency) -> `theme.ColorNameDisabled` with Mihomo latency text if any, else "-".
- `func rowState(enabled, validated bool, failure string) pillState`.

Rows keep `widget.List` and their existing create/update callbacks; add badge (`canvas.Rectangle` radius 8 + `canvas.Text`), status dot, muted secondary text (`canvas.Text` with muted color). Update colors only when changed. Subscriptions add `meter` when usage present. Add buttons get `widget.HighImportance`. Settings section list: put on card surface background via `newCard`-style rectangle; no behavior change.

- [ ] Step 1: failing tests: `latencyBadge` table (120 ms with alert 250/threshold 800 -> success; 400 -> warning; 900 -> error; timeout -> error; unmeasured -> disabled "-"; mihomo-only -> disabled "75 ms (mihomo)"); `rowState` table.
- [ ] Step 2: FAIL. Step 3: implement. Step 4: `go test -tags ci ./ui` both locales green. Commit `feat(ui): restyle list rows with status and latency badges`.

### Task 6: Screenshots, docs, final checks

**Files:** `ui/docs_screenshot_test.go`, `docs/assets/screenshots/gui-*.png`, Makefile only if needed.

- [ ] Step 1: screenshot test captures light set (docs defaults) and, for manual check only, a dark Overview written to `$DOCS_SCREENSHOTS_DARK` when set (not committed).
- [ ] Step 2: `make screenshots`; view every GUI PNG; check dark Overview manually.
- [ ] Step 3: `gofmt -l .`, `go vet -tags ci ./...`, `go vet -tags 'ci screenshots' ./ui ./tui`, `go test -tags ci -count=1 ./...`, `LANGUAGE=zh_CN go test -tags ci -count=1 ./ui ./tui`, and Windows/macOS compile `GOOS=windows go vet -tags ci ./ui`, `GOOS=darwin go vet -tags ci ./ui` (expect cgo limits; use `CGO_ENABLED=1` only where the host supports it, otherwise rely on CI).
- [ ] Step 4: commit `docs: refresh gui screenshots for the redesign`.
