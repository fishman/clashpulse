# ClashPulse implementation handoff

## Workspace

- Repository: `/home/timebomb/git/opencode/go-clash`
- Implementation worktree: `/home/timebomb/git/opencode/go-clash/.worktrees/clashpulse-foundation`
- Branch: `feat/clashpulse-foundation`
- Plan: `docs/superpowers/plans/2026-09-22-clashpulse-foundation.md`
- Spec: `docs/superpowers/specs/2026-09-22-clashpulse-design.md`
- SDD ledger: `.superpowers/sdd/2026-09-22-clashpulse-foundation/progress.md`

Continue only in the implementation worktree. `master` contains the architecture/spec/plan baseline at `9228a8a`.

## Design decisions

- `app` is sole lifecycle owner. Fyne and TUI are IPC clients only.
- Mihomo is always an external child binary; never import/embed/fork it.
- Arch default is system Mihomo; bundled is explicit fallback.
- TUI uses `tcell` and `lipgloss`, event-driven snapshots, stable list IDs, declarative keybindings. No Bubble Tea or mihomo-tui navigation patterns.
- Strict TOML user intent: `config.toml`, `subscriptions.toml`, `resources.toml`, `filters.toml`.
- Config watch is event-driven with `fsnotify`, no polling. User config directories/files are private.
- System Proxy only in first release; no TUN, service manager, core downloading, remote control, profile editor, subscription pools, telemetry, or DNS redirection.
- Notmutt shared modules are currently pinned at `github.com/fishman/notmutt/lib/xdg v0.1.0` and `github.com/fishman/notmutt/lib/localipc v0.1.0`; do not add `replace` or `go.work`.
- `gopkg.in/yaml.v3 v3.0.1` is approved for Mihomo YAML.
- `github.com/fsnotify/fsnotify v1.9.0` is approved for config directory events.

## Completed implementation

### Foundation

- `2637847 feat(core): add clashpulse module foundation`
- `bf909bd feat(core): fix CLI fallback behavior`
- `core.Snapshot` copies caller-owned slices.
- `clashpulse version` is side-effect free; no arguments and `version extra` use the expected nonzero `app.Run` fallback.

### Strict TOML configuration

- `6b2aff5 feat(config): add strict reloadable toml store`
- `64418b3 feat(config): add strict reloadable toml store`
- `772835e fix(config): strict toml decode and private writes`
- Uses `BurntSushi/toml`, strict undecoded-key detection, four explicit schemas, defaults applied once, cross-file validation, immutable store snapshots, section diffs, event-driven watcher, cancellation, 0600 temp files, fsync/rename/directory sync, 0700 config directories.
- Last confirmed command:
  ```sh
  go test -count=1 ./config -run 'Test(Load|Replace|Watch)'
  ```
  passed.

### Mihomo adapter slices

- `3fc17b7 feat(mihomo): add binary selection`
- `9eca7a1 fix(mihomo): context binary inspection`
- `4d3b028 feat(mihomo): add child process lifecycle`
- `df98cf7 feat(mihomo): add controller adapter`
- `8e23cc4 fix(mihomo): block controller redirects`

Implemented:

- `mihomo/binary.go`: `Selection`, `Capability`, `Resolve`, context-aware `Inspect`; no shell interpolation.
- `mihomo/process.go`: `StartPlan`, explicit argv child start, one child at a time, cleanup on failure/cancellation, `Stop` kills and waits.
- `mihomo/controller.go`: loopback-only controller URL, Bearer secret, bounded response body, escaped path names, typed proxy/group data, delay errors for missing/nonpositive values, redirect blocking to prevent secret forwarding.

Last confirmed commands:

```sh
go test -count=1 ./mihomo
go test -count=1 ./mihomo -run '^TestControllerRejectsRedirectWithoutLeakingSecret$'
go test -count=1 ./mihomo -run '^TestProcess'
```

All passed when run.

## Current work: downloader fix round 1

Commit made:

- `0593e49 feat(download): add bounded fetch client`

Files:

- `download/client.go`
- `download/fetch_test.go`
- Report: `.superpowers/sdd/2026-09-22-clashpulse-foundation/task-4-download-report.md`

Initial test passed:

```sh
go test ./download
```

Review findings still open. The original worker `DownloaderImplementation` was sent the following fix request and may be active/idle depending on transfer timing:

1. Cap redirects at five; the sixth redirect must fail.
2. Do not forward `If-None-Match` / `If-Modified-Since` to a cross-origin redirect target. Same-origin redirect may retain them.
3. On HTTP 304, return response `ETag` and `Last-Modified`, but no body.
4. Add HTTPS-to-disallowed-HTTP and redirect-to-userinfo tests, plus sixth-hop test.
5. Use custom `RoundTripper` with nonempty 304 body to prove returned 304 result is bodyless; `httptest` suppresses 304 bodies.
6. Add cancellation during fetch/read coverage.
7. TDD: red run before fix, green `go test ./download` after fix. Append evidence to the report. Commit the fix.

After fix, produce review package:

```sh
/home/timebomb/.omp/plugins/cache/plugins/claude-plugins-official___superpowers___6.3.0/skills/subagent-driven-development/scripts/review-package \
  docs/superpowers/plans/2026-09-22-clashpulse-foundation.md \
  0593e49 HEAD
```

Review the fix scope. Do not proceed to subscriptions/resources until all Important findings are fixed.

## SDD review exceptions

Several reviewer/worker attempts failed before execution with provider error:

```text
421 Misdirected Request
```

When this occurred, the controller performed an inline scoped code review plus the targeted test and recorded a ledger ruling. Preserve that discipline: prefer an independent reviewer; only fall back to inline review when the provider fails before execution.

## Task order adjustment

The original Task 3/4/5 plan has a dependency cycle: subscription/resource promotion requires Mihomo rendering/validation; the original renderer named future resource/DNS plan types; resources were intended to reuse subscription downloader.

Ruling in ledger: use shared `download` package; renderer should consume `config.Snapshot` plus a small `mihomo.ManagedPaths` map rather than future `resources.Plan` / `dns.Plan` types. This avoids duplicating types and lets subscriptions/resources share fetch semantics.

Suggested next dependency order after downloader review:

1. Finish/download review.
2. Define `mihomo.ManagedPaths` and deterministic render/validate slice, using only config and paths map.
3. Build subscription state/fetch/promotion on downloader + renderer + fake Mihomo validation.
4. Build resource registry/filter/DNS paths on downloader + renderer.
5. Add monitor policy/scheduler.
6. App orchestration/System Proxy.
7. Versioned IPC, then TUI and Fyne.

## Safety constraints to preserve

- Never log or emit subscription URLs, controller secret, credentials, proxy credentials, or profile body.
- Validate subscription URLs at boundary: HTTPS default; userinfo rejected; explicit per-subscription HTTP/invalid-TLS opt-in only.
- 304 must not cause writes/reload/redraw.
- Resource and subscription promotion must be temporary-file plus validation plus atomic replacement; failure preserves known-good state.
- No periodic idle polling or per-item goroutines.
- IPC local-only: Unix socket in 0700 directory; Windows named pipe later; no TCP.
- `go vet ./...` happens only at broad milestone/final verification, not per worker task.

## Ledger rulings that must appear in final delivery

- Shared module v0.1.0 retention despite tag force-update concern, per user direction.
- `fsnotify v1.9.0` accepted for cross-platform config watches.
- `yaml.v3 v3.0.1` accepted for Mihomo YAML.
- Task 3 split into binary/process/controller/render slices.
- Render/subscription/resource cycle broken by shared downloader and `mihomo.ManagedPaths`.
- Any inline review fallback due to provider 421 errors.
