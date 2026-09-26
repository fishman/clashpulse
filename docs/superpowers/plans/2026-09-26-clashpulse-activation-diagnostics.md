# Credential-Safe Activation Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace generic activation failure with an actionable stage in GUI, TUI, and future foreground CLI, without exposing profile credentials or source URLs.

**Architecture:** `core` defines the fixed public stages and a safe error wrapper. `app` tags actual lifecycle boundaries; `subscriptions.Activate` strips the raw cause while preserving the stage. IPC snapshots and session diagnostics use only the fixed message, not raw error text.

**Tech Stack:** Go 1.26.6, existing `core`, `app`, `subscriptions`, `ipc`, and standard-library errors. No new dependency or protocol field in this plan.

**Spec:** `docs/superpowers/specs/2026-09-26-clashpulse-local-file-activation-design.md`

## Global Constraints

- Secrets, subscription URLs, proxy names, source paths, and raw Mihomo/controller output never enter IPC snapshots, CLI stderr, logs, or diagnostic events.
- Preserve the original private cause for rollback and `errors.Is` inside `app`; `subscriptions` must discard that cause before returning an activation error to callers.
- Stage phrases are a fixed allowlist. Binary capability and resource failures may append one validated stable resource ID (`[A-Za-z0-9][A-Za-z0-9._-]{0,63}`) obtained from a typed declaration; never a URL, path, raw cause, proxy name, or parsed free-text error. Invalid IDs are omitted; unknown stages remain `activation failed`.
- Keep the existing singleton lifecycle owner and configuration validation behavior unchanged.
- ASCII code and comments; format with `gofmt` after the DRY pass.

## Review Focus

1. An `Apply` error containing `password=private` must reach GUI/TUI as a fixed stage with no raw cause (Task 3).
2. A missing geodata capability names its affected configured resource ID, while controller readiness remains a different stage (Tasks 1-3).
3. A System Proxy Apply error must remain a transactional activation failure, not be silently ignored (Task 3).
4. A failed activation followed by a successful retry must clear only that source's active issue (Task 3).
5. An invalid resource ID carrying a URL/credential is omitted, and unrelated non-activation diagnostics retain their current policy (Tasks 1 and 3).

---

### Task 1: Fixed Public Activation Stages

**Files:**
- Create: `core/activation.go`
- Test: `core/activation_test.go`

**Interfaces:**
- Consumes: ordinary Go errors.
- Produces: `core.ActivationStage` constants `ActivationFileInput`, `ActivationBinary`, `ActivationResources`, `ActivationConfigValidation`, `ActivationProcessStart`, `ActivationControllerReadiness`, `ActivationSystemProxy`, `ActivationStateCommit`, `ActivationRollback`; `core.WrapActivation(stage, cause) error`; `core.WrapActivationResource(stage, resourceID, cause) error`; `core.PublicActivation(err) (*core.ActivationError, bool)`. `ActivationError` exposes only `Stage` and a validated `ResourceID`; `Error()` renders a fixed phrase with optional resource ID and never its private cause. `PublicActivation` returns a new cause-free error and revalidates the ID.

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
func TestResourceActivationStageUsesOnlyValidatedID(t *testing.T) {
    valid, _ := PublicActivation(WrapActivationResource(ActivationBinary, "geosite", errors.New("password=private")))
    if valid.ResourceID != "geosite" || !strings.Contains(valid.Error(), "geosite") || strings.Contains(valid.Error(), "private") {
        t.Fatalf("valid resource identity disappeared or leaked cause: %q", valid)
    }
    invalid, _ := PublicActivation(WrapActivationResource(ActivationResources, "https://feed.invalid/?token=private", errors.New("private")))
    if invalid.ResourceID != "" || strings.Contains(invalid.Error(), "private") || strings.Contains(invalid.Error(), "://") {
        t.Fatalf("unsafe resource identity escaped: %q", invalid)
    }
}
```

- [ ] **Step 2: Run red:** `go test -tags ci ./core -run 'Test(PublicActivationDropsPrivateCause|UnknownActivationStageDoesNotEchoInput|ResourceActivationStageUsesOnlyValidatedID)$' -count=1`. Expected: missing stage/resource APIs.
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
type ActivationError struct { Stage ActivationStage; ResourceID string; cause error }
func validResourceID(id string) bool {
    if len(id) == 0 || len(id) > 64 { return false }
    for i := range len(id) {
        b := id[i]
        alpha := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
        if !alpha && !(i > 0 && (b == '-' || b == '_' || b == '.')) { return false }
    }
    return true
}
func WrapActivation(stage ActivationStage, cause error) error { return &ActivationError{Stage: stage, cause: cause} }
func WrapActivationResource(stage ActivationStage, id string, cause error) error {
    if stage != ActivationBinary && stage != ActivationResources || !validResourceID(id) { id = "" }
    return &ActivationError{Stage: stage, ResourceID: id, cause: cause}
}
func (e *ActivationError) Unwrap() error { return e.cause }
func (e *ActivationError) Error() string {
    if (e.Stage == ActivationBinary || e.Stage == ActivationResources) && validResourceID(e.ResourceID) {
        return e.Stage.Message() + " (resource " + e.ResourceID + ")"
    }
    return e.Stage.Message()
}
func PublicActivation(err error) (*ActivationError, bool) {
    var failure *ActivationError
    if !errors.As(err, &failure) { return nil, false }
    id := failure.ResourceID
    if !validResourceID(id) || failure.Stage != ActivationBinary && failure.Stage != ActivationResources { id = "" }
    return &ActivationError{Stage: failure.Stage, ResourceID: id}, true
}
```

- [ ] **Step 4: Run green:** `go test -tags ci ./core -count=1`.
- [ ] **Step 5: Commit:** `git add core/activation.go core/activation_test.go && git commit -m "feat(core): classify activation failure stages"`.

### Task 2: Identify Affected Resources Before Redaction

**Files:**
- Modify: `mihomo/render.go`, `resources/registry.go`, `resources/plan.go`
- Test: `mihomo/render_test.go`, `resources/registry_test.go`

**Interfaces:**
- Consumes: already validated `config.Resource.ID` and documented Mihomo capabilities.
- Produces: `mihomo.CapabilityError{ResourceID string, Kind config.ResourceKind}` for a missing geodata capability and `resources.ResourceFailure{ResourceID string, Err error}` for an invalid/unavailable managed resource; each implements `error`, and `ResourceFailure.Unwrap()` preserves private cause internally. `app` uses `errors.As` on these types and Task 1's `core.WrapActivationResource` to build a cause-free public message. No free-text parsing or resource URL extraction.

- [ ] **Step 1: Write failing typed-error tests.** Use the existing `mihomo/render_test.go` fixture with a configured `geosite` resource and `Capability{SupportsGeoSiteDat:false}`; assert `var missing *CapabilityError; errors.As(err, &missing)` yields `ResourceID == "geosite"` and `Kind == config.ResourceGeoSite`. Extend the pinned-resource regression in `resources/registry_test.go`: staging resource `cn` with a pin mismatch must still satisfy `errors.Is(err, ErrPinMismatch)` and expose `ResourceFailure.ResourceID == "cn"`. A non-due resource lacking a committed version must carry that same typed ID.

In `mihomo/render_test.go`:
```go
var missing *CapabilityError
if !errors.As(err, &missing) || missing.ResourceID != "geosite" || missing.Kind != config.ResourceGeoSite {
    t.Fatalf("missing capability lost affected resource: %v", err)
}
```
In `resources/registry_test.go`:
```go
var failed *ResourceFailure
if !errors.As(stageErr, &failed) || failed.ResourceID != "cn" || !errors.Is(stageErr, ErrPinMismatch) {
    t.Fatalf("pin failure lost resource identity: %v", stageErr)
}
```

- [ ] **Step 2: Run red:** `go test -tags ci ./mihomo ./resources -run 'Test(MissingGeoSiteCapabilityNamesResource|PinnedResourceMismatchRetainsPreviousGeneration|StageDueMissingCommittedResourceNamesID)$' -count=1`. Expected: typed error assertions fail; existing pin test alone may pass before the new assertion.
- [ ] **Step 3: Return typed errors at their existing owners.** Keep internal error messages and validations unchanged, but replace the geodata capability `fmt.Errorf` in `mihomo.Render` and the resource-specific `registry.recordFailure` and non-due StageDue wrapper with typed errors:

```go
type CapabilityError struct { ResourceID string; Kind config.ResourceKind }
func (e *CapabilityError) Error() string {
    return fmt.Sprintf("mihomo: resource %q requires %s capability", e.ResourceID, e.Kind)
}
type ResourceFailure struct { ResourceID string; Err error }
func (e *ResourceFailure) Error() string { return fmt.Sprintf("resource %q: %v", e.ResourceID, e.Err) }
func (e *ResourceFailure) Unwrap() error { return e.Err }
```

These types live in their respective packages, not in one new abstraction. Only attach IDs already validated by `resources.ValidateDeclaration`; leave an invalid declaration as a generic validation failure.
- [ ] **Step 4: Run green:** `go test -tags ci ./mihomo ./resources -count=1`.
- [ ] **Step 5: Commit:** `git add mihomo/render.go mihomo/render_test.go resources/registry.go resources/registry_test.go resources/plan.go && git commit -m "fix(resources): retain typed failing resource identity"`.

### Task 3: Apply Stages at Lifecycle and IPC Boundaries

**Files:**
- Modify: `app/lifecycle.go` (selected binary, generated candidate, resource plan, process/controller readiness, System Proxy Apply/rollback branches)
- Modify: `subscriptions/fetch.go` (`Service.Activate`)
- Modify: `app/runtime.go` (`reportErrorScoped`)
- Modify: `app/diagnostics.go` (`safeDiagnostic`)
- Test: `subscriptions/active_test.go`, `app/diagnostics_test.go`, `app/compatibility_test.go`, `app/static_resource_test.go`

**Interfaces:**
- Consumes: Task 1 `core.WrapActivation`, `core.WrapActivationResource`, `core.PublicActivation`, stage constants; Task 2 `mihomo.CapabilityError` and `resources.ResourceFailure`.
- Produces: `subscriptions.Activate` returns `ErrActivation` joined with a cause-free `*core.ActivationError` containing only the fixed stage and validated stable resource ID. `app.reportErrorScoped` and `safeDiagnostic` show that same safe message for `activate_subscription`. Other command diagnostics retain their current policy and the existing compatibility test remains meaningful. The foreground command in the second plan consumes these APIs directly.

- [ ] **Step 1: Write failing boundary tests.** Extend `subscriptions/active_test.go` with an `Apply` callback returning `core.WrapActivation(core.ActivationControllerReadiness, errors.New("password=private"))`; after `Activate`, assert `errors.Is(err, ErrActivation)`, `core.PublicActivation(err)` reports controller readiness, and `err.Error()` omits `private`. Repeat with `core.WrapActivationResource(core.ActivationResources, "geosite", errors.New("https://feed.invalid/?token=private"))`; assert the public error names only `geosite`. Inject `Finalize` and `Restore` causes containing fake credentials; both must remain fixed state-commit/rollback labels without raw text. Extend `app/diagnostics_test.go` with the same safe errors through `reportErrorScoped("activate_subscription", "feed", err)` using a disposable IPC server; assert issue and diagnostic carry the fixed phrase and `geosite` when applicable, never source URLs, and a successful `resolveIssue` removes only `feed`. In `app/compatibility_test.go`, call `s.renderWithHome` with a geosite resource and `Capability{SupportsGeoSiteDat:false}`; assert `core.PublicActivation` names the validated ID, while a raw error with a URL does not become an activation resource ID. Extend `app/static_resource_test.go` to assert controller-readiness and failing resource IDs are preserved; make its fake `gsettings` reject one `set` after readiness to assert `ActivationSystemProxy`, old runtime retention, and no secret text. An unrelated config error must retain its current message.

```go
raw := core.WrapActivation(core.ActivationControllerReadiness, errors.New("password=private"))
service.options.Apply = func(context.Context, []byte) error { return raw }
err := service.Activate(context.Background(), "feed")
if !errors.Is(err, ErrActivation) || strings.Contains(err.Error(), "private") {
    t.Fatalf("activation leaked cause or lost category: %v", err)
}
```

- [ ] **Step 2: Run red:** `go test -tags ci ./subscriptions ./app -run 'Test(ActivationFailureStage|DiagnosticsExposeSafeActivationStage|ActivationCapabilityNamesManagedResource|StaticResourceFailureRestoresRuntime)$' -count=1`. Expected: generic activation result, missing resource IDs, and no staged diagnostics; the existing static failure test may pass before its new ID assertion.
- [ ] **Step 3: Tag source boundaries, then strip private causes at `subscriptions.Activate`.** Wrap `selectedCapability` failures as `ActivationBinary`, `resources.Stage`/commit failures as `ActivationResources`, rendered YAML or Mihomo `-t` errors as `ActivationConfigValidation`, `Process.Start` failures as `ActivationProcessStart`, readiness timeout as `ActivationControllerReadiness`, System Proxy Apply failures as `ActivationSystemProxy`, and private state/rollback errors with their respective stages. At `app.renderWithHome`, use `errors.As` on Task 2's `*mihomo.CapabilityError` to attach its declared `ResourceID` under `ActivationBinary`; at a resource stage error use `errors.As` on `*resources.ResourceFailure` under `ActivationResources`. Never parse an error string for activation identity. `core.WrapActivationResource` revalidates the ID; other stages carry none. Keep the existing rollback code and internal causes. At the subscription Apply boundary use this safe cutover:

```go
if err := s.options.Apply(ctx, bytes.Clone(profile)); err != nil {
    if contextErr := safeContextError(ctx); contextErr != nil { return contextErr }
    if public, ok := core.PublicActivation(err); ok { return errors.Join(ErrActivation, public) }
    return ErrActivation
}
```

At the two `app.applyGenerated` error branches, classify the typed source before a generic stage:

```go
var capability *mihomo.CapabilityError
if errors.As(err, &capability) {
    return core.WrapActivationResource(core.ActivationBinary, capability.ResourceID, err)
}
var resource *resources.ResourceFailure
if errors.As(err, &resource) {
    return core.WrapActivationResource(core.ActivationResources, resource.ResourceID, err)
}
```
For `Finalize`, `setApplied`, pending-marker, and `Restore` failures, return only `ErrActivation`/`ErrStore`/`ErrRestore` plus a cause-free `ActivationStateCommit` or `ActivationRollback` error; do not join raw `err` or `restoreErr` into the public chain. In `app.reportErrorScoped` and `safeDiagnostic`, check `core.PublicActivation(err)` before generic activation labeling and use only `public.Error()` for `activate_subscription`. Retain `app.compatibilityFailure`'s existing non-activation behavior and its regression test; the new activation classification takes the typed path first.
- [ ] **Step 4: Run green and DRY pass:** `gofmt -w core/activation.go subscriptions/fetch.go app/lifecycle.go app/runtime.go app/diagnostics.go subscriptions/active_test.go app/diagnostics_test.go app/compatibility_test.go app/static_resource_test.go && go test -tags ci ./core ./mihomo ./resources ./subscriptions ./app -count=1`. Expected: all packages pass, affected resource IDs remain named, and active issues remain bounded and redacted.
- [ ] **Step 5: Commit:** `git add app/lifecycle.go app/runtime.go app/diagnostics.go app/diagnostics_test.go app/compatibility_test.go app/static_resource_test.go subscriptions/fetch.go subscriptions/active_test.go && git commit -m "fix(app): report safe activation failure stages"`.

### Integration Gate

- [ ] Run `go test -tags ci ./...` and `go vet -tags ci ./...` once after Task 3. Confirm missing geodata capability and managed-resource failure name validated IDs; controller timeout, System Proxy failure, and raw secret causes yield only fixed public messages. No subprocess output or source path belongs in a snapshot or CLI stderr.
