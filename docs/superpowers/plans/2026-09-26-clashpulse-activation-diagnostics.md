# Credential-Safe Activation Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace generic activation failure with an actionable stage in GUI, TUI, and future foreground CLI, without exposing profile credentials or source URLs.

**Architecture:** `core` defines the fixed public stages and a safe error wrapper. `app` tags actual lifecycle boundaries; `subscriptions.Activate` strips the raw cause while preserving the stage. IPC snapshots and session diagnostics use only the fixed message, not raw error text.

**Tech Stack:** Go 1.26.6, existing `core`, `app`, `subscriptions`, `ipc`, and standard-library errors. No new dependency or protocol field in this plan.

**Spec:** `docs/superpowers/specs/2026-09-26-clashpulse-local-file-activation-design.md`

## Global Constraints

- Secrets, subscription URLs, proxy names, source paths, and raw Mihomo/controller output never enter IPC snapshots, CLI stderr, logs, or diagnostic events.
- Preserve the original private cause for rollback and `errors.Is` inside `app`; `subscriptions` must discard that cause before returning an activation error to callers.
- Stage messages are a fixed allowlist; unknown errors remain `activation failed`, not a guessed category or raw string.
- Keep the existing singleton lifecycle owner and configuration validation behavior unchanged.
- ASCII code and comments; format with `gofmt` after the DRY pass.

## Review Focus

1. An `Apply` error containing `password=private` must reach GUI/TUI as a fixed stage with no raw cause (Task 2).
2. A failed Mihomo controller readiness check must be distinguishable from binary validation (Task 2).
3. A System Proxy Apply error must remain a transactional activation failure, not be silently ignored (Task 2).
4. A failed activation followed by a successful retry must clear only that source's active issue (Task 2).
5. A non-activation command error must retain its existing classification (Task 2).

---

### Task 1: Fixed Public Activation Stages

**Files:**
- Create: `core/activation.go`
- Test: `core/activation_test.go`

**Interfaces:**
- Consumes: ordinary Go errors.
- Produces: `core.ActivationStage` constants `ActivationFileInput`, `ActivationBinary`, `ActivationResources`, `ActivationConfigValidation`, `ActivationProcessStart`, `ActivationControllerReadiness`, `ActivationSystemProxy`, `ActivationStateCommit`, `ActivationRollback`; `core.WrapActivation(stage, cause) error`; `core.PublicActivation(err) (*core.ActivationError, bool)`. `ActivationError.Error()` returns only a fixed message; `PublicActivation` returns a new cause-free error.

- [ ] **Step 1: Write failing stage and secrecy tests** in `core/activation_test.go`:

```go
func TestPublicActivationDropsPrivateCause(t *testing.T) {
    private := errors.New("password=private https://feed.invalid/?token=private")
    wrapped := WrapActivation(ActivationControllerReadiness, private)
    public, ok := PublicActivation(wrapped)
    if !ok || public.Stage != ActivationControllerReadiness ||
        strings.Contains(public.Error(), "private") || strings.Contains(public.Error(), "://") ||
        !errors.Is(wrapped, private) || errors.Is(public, private) {
        t.Fatalf("public stage lost or private cause escaped: %v, %v", public, ok)
    }
}
func TestUnknownActivationStageDoesNotEchoInput(t *testing.T) {
    if got := WrapActivation(ActivationStage("password=private"), errors.New("token=private")).Error(); got != "activation failed" {
        t.Fatalf("unknown stage escaped: %q", got)
    }
}
```

- [ ] **Step 2: Run red:** `go test -tags ci ./core -run 'Test(PublicActivationDropsPrivateCause|UnknownActivationStageDoesNotEchoInput)$' -count=1`. Expected: missing stage APIs.
- [ ] **Step 3: Add the fixed type** in `core/activation.go`; use a `switch` over the nine constants and a fallback, not a user-controlled label:

```go
type ActivationStage string
const (
    ActivationFileInput ActivationStage = "file_input"
    ActivationBinary ActivationStage = "binary"
    ActivationResources ActivationStage = "resources"
    ActivationConfigValidation ActivationStage = "config_validation"
    ActivationProcessStart ActivationStage = "process_start"
    ActivationControllerReadiness ActivationStage = "controller_readiness"
    ActivationSystemProxy ActivationStage = "system_proxy"
    ActivationStateCommit ActivationStage = "state_commit"
    ActivationRollback ActivationStage = "rollback"
)
func (stage ActivationStage) Message() string {
    switch stage {
    case ActivationFileInput: return "local profile file is unavailable or invalid"
    case ActivationBinary: return "selected Mihomo binary is incompatible or unavailable"
    case ActivationResources: return "managed resources could not be validated"
    case ActivationConfigValidation: return "generated Mihomo configuration was rejected"
    case ActivationProcessStart: return "Mihomo process could not start"
    case ActivationControllerReadiness: return "Mihomo controller did not become ready"
    case ActivationSystemProxy: return "System Proxy could not be applied"
    case ActivationStateCommit: return "private activation state could not be committed"
    case ActivationRollback: return "activation rollback failed; prior runtime needs attention"
    default: return "activation failed"
    }
}
type ActivationError struct { Stage ActivationStage; cause error }
func WrapActivation(stage ActivationStage, cause error) error { return &ActivationError{Stage: stage, cause: cause} }
func (e *ActivationError) Unwrap() error { return e.cause }
func (e *ActivationError) Error() string { return e.Stage.Message() }
func PublicActivation(err error) (*ActivationError, bool) {
    var failure *ActivationError
    if !errors.As(err, &failure) { return nil, false }
    return &ActivationError{Stage: failure.Stage}, true
}
```

- [ ] **Step 4: Run green:** `go test -tags ci ./core -count=1`.
- [ ] **Step 5: Commit:** `git add core/activation.go core/activation_test.go && git commit -m "feat(core): classify activation failure stages"`.

### Task 2: Apply Stages at Lifecycle and IPC Boundaries

**Files:**
- Modify: `app/lifecycle.go` (selected binary, generated candidate, resource plan, process/controller readiness, System Proxy Apply/rollback branches)
- Modify: `subscriptions/fetch.go` (`Service.Activate`)
- Modify: `app/runtime.go` (`reportErrorScoped`)
- Modify: `app/diagnostics.go` (`safeDiagnostic`)
- Test: `subscriptions/active_test.go`, `app/diagnostics_test.go`, `app/static_resource_test.go`

**Interfaces:**
- Consumes: Task 1 `core.WrapActivation`, `core.PublicActivation`, stage constants.
- Produces: `subscriptions.Activate` returns `ErrActivation` joined with a cause-free `*core.ActivationError` when `Apply` returned a staged failure. `app.reportErrorScoped` and `safeDiagnostic` show the fixed stage for `activate_subscription` only. Other command diagnostics retain their current policy. The foreground command in the second plan consumes these APIs directly.

- [ ] **Step 1: Write failing boundary tests.** Extend `subscriptions/active_test.go` with an `Apply` callback returning `core.WrapActivation(core.ActivationControllerReadiness, errors.New("password=private"))`; after `Activate`, assert `errors.Is(err, ErrActivation)`, `core.PublicActivation(err)` reports the controller stage, and `err.Error()` omits `private`. Inject a `Finalize` error containing `https://private.invalid/?token=private` and a `Restore` error containing `password=private`; verify both remain fixed state-commit/rollback labels with no raw cause. Extend `app/diagnostics_test.go` to report a safe activation error through `reportErrorScoped("activate_subscription", "feed", err)` using a disposable IPC server; assert its issue and diagnostic both say `Mihomo controller did not become ready`, contain no `password`, and a successful `resolveIssue` removes only `feed`. Extend the existing `app/static_resource_test.go` readiness failure to assert `core.PublicActivation` identifies controller readiness. Add a System Proxy Apply failure by making that file's fake `gsettings` reject a single `set` call after controller readiness; assert `ActivationSystemProxy`, old runtime retention, and no secret text. An unrelated config error must retain its current message.

```go
raw := core.WrapActivation(core.ActivationControllerReadiness, errors.New("password=private"))
service.options.Apply = func(context.Context, []byte) error { return raw }
err := service.Activate(context.Background(), "feed")
if !errors.Is(err, ErrActivation) || strings.Contains(err.Error(), "private") {
    t.Fatalf("activation leaked cause or lost category: %v", err)
}
```

- [ ] **Step 2: Run red:** `go test -tags ci ./subscriptions ./app -run 'Test(ActivationFailureStage|DiagnosticsExposeSafeActivationStage|StaticResourceFailureRestoresRuntime)$' -count=1`. Expected: generic activation result and no staged diagnostics; the existing static failure test may pass in isolation but does not satisfy the new stage assertion.
- [ ] **Step 3: Tag source boundaries, then strip private causes at `subscriptions.Activate`.** Wrap `selectedCapability` failures as `ActivationBinary`, resource stage/commit failures as `ActivationResources`, rendered YAML or Mihomo `-t` errors as `ActivationConfigValidation`, `Process.Start` failures as `ActivationProcessStart`, readiness timeout as `ActivationControllerReadiness`, System Proxy Apply failures as `ActivationSystemProxy`, and private state/rollback errors with their respective stages. Keep the existing rollback code and error handling; only public classification changes. At the subscription Apply boundary use this safe cutover:

```go
if err := s.options.Apply(ctx, bytes.Clone(profile)); err != nil {
    if contextErr := safeContextError(ctx); contextErr != nil { return contextErr }
    if public, ok := core.PublicActivation(err); ok { return errors.Join(ErrActivation, public) }
    return ErrActivation
}
```

For `Finalize`, `setApplied`, pending-marker, and `Restore` failures, return only `ErrActivation`/`ErrStore`/`ErrRestore` plus a cause-free `ActivationStateCommit` or `ActivationRollback` error; do not join the raw `err` or `restoreErr` into the public chain. In `app.reportErrorScoped` and `safeDiagnostic`, check `core.PublicActivation(err)` before string-based generic activation labeling and use only `public.Error()` for `activate_subscription`. Other commands retain their existing diagnostic policy.
- [ ] **Step 4: Run green and DRY pass:** `gofmt -w core/activation.go subscriptions/fetch.go app/lifecycle.go app/runtime.go app/diagnostics.go subscriptions/active_test.go app/diagnostics_test.go app/static_resource_test.go && go test -tags ci ./core ./subscriptions ./app -count=1`. Expected: all three packages pass and active issues remain bounded and redacted.
- [ ] **Step 5: Commit:** `git add app/lifecycle.go app/runtime.go app/diagnostics.go app/diagnostics_test.go app/static_resource_test.go subscriptions/fetch.go subscriptions/active_test.go && git commit -m "fix(app): report safe activation failure stages"`.

### Integration Gate

- [ ] Run `go test -tags ci ./...` and `go vet -tags ci ./...` once after Task 2. Confirm controller timeout, System Proxy failure, and raw secret cause yield only fixed public messages. No subprocess output or source path belongs in a snapshot or CLI stderr.
