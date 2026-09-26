# ClashPulse localization design

## Goal

Provide a user-selectable English or Simplified Chinese interface shared by the Fyne desktop client and terminal client. The locale is ordinary persisted user intent; both clients display the same locale through the existing local IPC snapshot flow.

## Decisions

- Support `en` and `zh-CN`; omitted locale defaults to `en`.
- Store locale in `[app]` within `config.toml`. It is not inferred from environment variables or OS locale.
- Validate the locale strictly during complete config loading. Reject unknown tags; never silently coerce user intent.
- Expose the selected locale in immutable app/core snapshots. Update it through the normal typed configuration command and atomic config writer.
- No translation dependency or runtime resource downloads. Keep catalogs in Go and use English as the explicit fallback for an untranslated product-owned string.
- Translate only ClashPulse-owned presentation text: navigation, labels, buttons, editor prompts/errors, status, notices, help descriptions, and empty states. Proxy/profile names, source IDs/hosts, user-entered values, URLs, diagnostics supplied by services, and other external data remain unchanged.

## Architecture and flow

`config.App` owns the locale; config defaults and validation include it, and the existing `app` section diff makes locale a passive UI change. `ipc.ConfigPatch` accepts a pointer-valued locale field so omission differs from an explicit setting. The existing typed update command writes `config.toml`; successful snapshot publication carries the new locale to both clients. No new IPC command, transport, or lifecycle owner.

A small translation catalog owned by the clients maps stable source English strings to Simplified Chinese. Missing catalog entries return English. GUI widgets are created from the current locale and update their product-owned text in place when the locale changes; user-entered and snapshot-provided values are not translated. The TUI resolves table headings, notices, modal labels, status text, and keybinding descriptions through the current locale while preserving action IDs and key sequences. Locale changes do not reset cursor, selection, modal state, or pending intent.

## Boundaries

- The GUI and TUI both consume the locale only from the service snapshot; they do not read TOML or infer a locale independently.
- A config reload remains all-or-nothing. Invalid locale retains the prior config snapshot and runtime state and follows the existing structured config-error path.
- Locale edits are passive: they do not restart Mihomo, change scheduling, or alter network/resource behavior.
- Translated messages must preserve redaction. Interpolated identifiers and service-provided messages remain data; never translate or expose sensitive URLs, credentials, or profile content.
- Tests use deterministic catalog fixtures. Do not translate protocol values, command kinds, table IDs, stable resource IDs, or serialized keys.

## Verification

- Config tests cover default English, accepted `zh-CN`, and rejection of an unsupported locale without replacing the live snapshot.
- IPC tests round-trip a locale patch and locale-bearing snapshot.
- GUI and TUI behavior tests prove locale changes update visible product-owned labels/help without changing selected row, modal state, source-provided text, or command intent.
- Run targeted config, IPC, app, GUI, and TUI tests; run `go vet ./...` and `go test -tags ci ./...` before integration. Exercise the TUI against its terminal screen implementation and GUI widgets with Fyne's software test driver.
