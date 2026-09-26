# Generated Configuration Override Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show why the active generated Mihomo configuration differs from its immutable source profile, without exposing source values or credentials through IPC, GUI, or TUI.

**Architecture:** Mihomo compares source and generated YAML only at controlled apply boundaries, emitting a fixed allowlist of field identifiers and change kinds; it never emits YAML values. The app caches this bounded report with the active runtime and restores it on failed replacement. A versioned IPC snapshot carries the same report to read-only Overview sections in Fyne and TUI. No generic merge engine, script execution, source mutation, or new dependency.

**Tech Stack:** Go 1.26.6, existing yaml.v3, IPC snapshots v5, Fyne v2, tcell/lipgloss.

**Spec:** `docs/superpowers/specs/2026-09-22-clashpulse-design.md`; the user-approved guidance in this conversation is to preserve source proxies/groups/rules and DNS except explicitly managed settings, and make app-owned overrides inspectable.

## Global Constraints

- Treat `test.yaml` and `clash-verge.yaml` as private user data; their IP differences were deliberate anonymization, not evidence of a conversion rule. Never use their credentials in fixtures or output.
- Keep source profiles immutable and the existing generated-config validation/atomic activation boundary intact. The report is informational, not a configuration transformation.
- Controller remains loopback with a random secret; no copying Clash Verge CORS, DNS preset, script, or `profile.store-selected` behavior.
- Report only fixed field identifiers and fixed effect/reason labels. No profile text, proxy names, credentials, IPs, addresses, DNS endpoints, paths, URLs, or raw YAML enter snapshots/events/logs. Reject forged IPC report strings.
- Do not recompute the report during idle polling or each snapshot publish. Compute only on successful application; restore prior report on transactional rollback; clear it while stopped.
- Run gofmt, focused tests, full `go test -tags ci ./...`, and `go vet -tags ci ./...`. Preserve unrelated untracked files.

## Review Focus

1. Source field absent versus present: report `added` versus `replaced`, not a false override of a retained field.
2. User-supplied secrets/URLs/IPs in known or unknown YAML fields: no raw values appear in any report, IPC frame, GUI/TUI row, or error.
3. Failed validation or failed subscription activation: prior known-good report remains visible when prior runtime is restored.
4. Profile restart/resource refresh with identical effective config: no extra report event/redraw or idle reparse.
5. Local source changes to an explicitly activated subscription: report switches only after successful activation; no stale local explanation after cutover.

---

### Task 1: Derive a redacted allowlisted report

**Files:** Modify `mihomo/render.go`; add focused tests to `mihomo/render_test.go`.

**Interface:** `mihomo.ExplainOverrides(source, generated []byte) ([]mihomo.ConfigOverride, error)` where `ConfigOverride` contains only `Key` and `Change` (`added`, `replaced`, `removed`). Allowlist includes `mixed-port`, `port`, `socks-port`, `external-controller`, `secret`, `allow-lan`, `bind-address`, `dns.listen`, `dns.nameserver-policy`, `rule-providers`, and `rules`. Include a key only when its effective value differs; never traverse arbitrary keys for output. Preserve deterministic key order; cap report at the allowlist size.

- [ ] Write a failing test: render a source with a non-loopback controller, a source DNS listener and resolver URL, proxy credentials and a custom unknown key. Compare source/generated; assert only fixed keys/change kinds, no resolver, URL, IP, proxy name, password, or original YAML bytes; source remains unchanged. Assert unchanged source DNS resolvers are not reported.
- [ ] Run `go test -tags ci ./mihomo -run '^TestExplainOverridesRedactsSourceValues$' -count=1` and observe RED.
- [ ] Implement one YAML parse of each input when called, fixed paths only, and fixed change-kind selection; reject malformed inputs without echoing them.
- [ ] Run the focused test and `go test -tags ci ./mihomo -count=1`; gofmt and DRY pass.

### Task 2: Publish the applied report transactionally over IPC

**Files:** Modify `core/snapshot.go`, `ipc/protocol.go`, `app/runtime.go`, `app/lifecycle.go`, `app/commands.go`, `app/serve.go` only as required; add behavioral tests in `app` and `ipc`.

**Contract:** `core.ConfigOverrideSnapshot{Key, Change string}` is a value-only IPC shape. IPC v5 validates both fields against Task 1's finite allowlist and enum; it never accepts arbitrary labels or YAML values. `app.runtimeService` caches the report at successful apply only; `runtimeBackup` retains the prior slice for rollback. App event snapshots expose it only while a controller is running.

- [ ] Write failing tests for active local profile source-vs-effective report, failed replacement retaining the prior report, successful subscription cutover replacing it, stopped runtime hiding it, and IPC rejection of a forged key/value containing a password/path/URL. Extend the version test to reject v4.
- [ ] Run `go test -tags ci ./app ./ipc -run 'Test(ConfigOverride|RejectsPreviousOverrideProtocol)' -count=1` and observe RED.
- [ ] Wire successful `start`, `applyGenerated`, and resource application to compute the report once; store/restore it at the same lifecycle owner as generated config. Clone it in `core.CloneSnapshot`; validate the finite key/change combinations in IPC and bump `ProtocolVersion` from 4 to 5. No report on failed candidate or when stopped.
- [ ] Run focused and full affected-package tests; gofmt and DRY pass.

### Task 3: Render the same safe report in Fyne and TUI

**Files:** Modify `tui/model.go`, `ui/ui.go`; update focused tests in `tui` and `ui`. Keep existing Overview layout and list identity conventions; do not create a new page or editor.

- [ ] Write failing view tests: a snapshot with fixed override keys shows a read-only `Generated config changes` section in both clients; unrelated proxy updates preserve the TUI selection; malicious-looking source bytes cannot appear because views consume only fixed keys and local copy. An empty report shows `No managed overrides` rather than guessed source details.
- [ ] Run `go test -tags ci ./tui ./ui -run '^Test.*ConfigOverride.*$' -count=1` and observe RED.
- [ ] Add Overview rows/labels using fixed local key descriptions (no raw generated YAML). Update only when the immutable report changes; keep Fyne mutations on `fyne.Do`.
- [ ] Run focused tests, exercise the rendered TUI surface with a disposable fake Mihomo and the Fyne test canvas; run full tests/vet, DRY pass, and remove smoke fixtures. Document the read-only report and its privacy boundary in `README.md` after behavioral verification.

## Implementation status

Completed all three tasks. The report is computed only when applying generated configuration, hidden during an uncommitted candidate or failed rollback, and restored with the previous runtime on successful rollback. IPC v5 accepts only fixed keys and change kinds. GUI and TUI Overview expose the same read-only report, including a narrow terminal detail fallback and a scrollable, wrapping Fyne view. Focused tests were observed red then green; the full `go test -tags ci ./... -count=1` and `go vet -tags ci ./...` passed. Native Linux CLI/TUI and the Fyne test canvas were exercised with synthetic profiles. Windows targets compiled; macOS non-GUI targets compiled, while native Fyne/macOS runtime still requires an Apple CGO build environment.
