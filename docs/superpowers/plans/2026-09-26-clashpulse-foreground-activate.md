# Foreground Local-File Activation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `clashpulse activate <profile.yaml>` starts and owns a local profile until Ctrl-C, with a ready signal and credential-safe failures.

**Architecture:** The existing `app` remains the only Mihomo/IPC lifecycle owner. It reads one immutable local source into process memory, uses the same config/resource renderer as subscriptions, and stores only a distinct temporary generated Mihomo config. No subscription record or source path persists. The CLI enters the foreground owner and prints success only on controller readiness.

**Tech Stack:** Go 1.26.6, existing `urfave/cli/v3`, `config.Store`, `mihomo`, `resources`, `ipc`, Fyne and tcell/lipgloss clients, standard library file/process APIs.

**Spec:** `docs/superpowers/specs/2026-09-26-clashpulse-local-file-activation-design.md`

**Prerequisite:** Complete `docs/superpowers/plans/2026-09-26-clashpulse-activation-diagnostics.md` first. `core.ActivationStage` and `core.WrapActivation` are the public failure contract used here.

## Global Constraints

- `app` alone owns the process, controller readiness, resources, System Proxy, IPC, restart, and shutdown; no detached child or second owner.
- Local input is a required regular non-symlink file, bounded by 10 MiB and read once. Never mutate it, persist its path, import it into subscriptions, or emit its content/credentials in CLI, snapshots, events, or logs.
- `generated-local-*.yaml` remains 0600 in private state only while this foreground owner lives; remove it on shutdown and sweep orphan local-generated files under the exclusive owner lock on startup. Do not overwrite the durable `generated.yaml` used by subscriptions.
- CLI exits nonzero and prints a fixed safe stage on failure. It prints success only after an authenticated controller proxy snapshot succeeds, then stays running until cancellation.
- Preserve current subscription activation/rollback behavior and existing command meanings. Explicit subscription activation may replace the local source, but cannot silently switch sources.
- Bump IPC schema version from 3 to 4 when adding the typed active-source field; reject incompatible clients and never include a local file path in the snapshot.
- ASCII code and project prose; no new dependency. Run `gofmt`, focused checks, full tests, and vet after the DRY pass.

## Review Focus

1. Relative paths and filenames with spaces work without shell interpolation; final symlinks, directories, empty files, and a file that grows above 10 MiB after stat are rejected (Task 1).
2. Replacing or deleting the selected source file after readiness does not change restart/resource refresh bytes during this run (Task 3).
3. A second owner gets `errStateInUse` and cannot disturb the first owner's child or System Proxy (Task 2).
4. A local profile with proxies but no groups reaches readiness; a child that exits early yields a failure rather than a false success (Task 2).
5. TUI/GUI identify the local active source without exposing the path, and explicit subscription activation switches identity only after success (Tasks 3-4).

---

### Task 1: Bound and Read the User-Selected File

**Files:**
- Create: `app/local_profile.go`, `app/local_profile_test.go`
- Modify: `subscriptions/store.go`, `subscriptions/model.go` (export the existing `defaultMaxProfileBytes` as `DefaultMaxProfileBytes` and migrate its sole caller)

**Interfaces:**
- Consumes: `core.WrapActivation(core.ActivationFileInput, cause)` from the prerequisite plan.
- Produces: `readLocalProfile(ctx context.Context, path string) ([]byte, error)`; bytes are fresh and have no retained path. It does not perform Mihomo I/O or mutate the file.

- [ ] **Step 1: Write boundary tests** in `app/local_profile_test.go`. A regular source with a space in its name returns its bytes; a symlink and oversized file return the fixed `ActivationFileInput` stage and never reveal the pathname or a fake password in `Error()`. Rewrite the source after the read and assert the returned bytes stay unchanged. On platforms where symlinks cannot be created, skip only that subcase.

```go
source := filepath.Join(t.TempDir(), "my profile.yaml")
if err := os.WriteFile(source, []byte("proxies: []\n"), 0o600); err != nil { t.Fatal(err) }
body, err := readLocalProfile(context.Background(), source)
if err != nil || string(body) != "proxies: []\n" { t.Fatalf("read = %q, %v", body, err) }
if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil { t.Fatal(err) }
if string(body) != "proxies: []\n" { t.Fatal("source bytes changed after read") }
```

- [ ] **Step 2: Run red:** `go test -tags ci ./app -run '^TestLocalProfileFileBoundary$' -count=1`. Expected: `readLocalProfile` undefined.
- [ ] **Step 3: Implement the bounded read.** Rename the existing subscription default to `subscriptions.DefaultMaxProfileBytes` (10 MiB); use that value below instead of a second limit. Resolve with `filepath.Abs`, reject a final symlink with `os.Lstat`, compare `os.SameFile` against the opened handle, bound an in-flight growing file using `io.LimitReader`, and check cancellation before/after. Only `core.ActivationFileInput` reaches public stderr; no source path is formatted into its `Error()` string.

```go
func readLocalProfile(ctx context.Context, path string) ([]byte, error) {
    if err := ctx.Err(); err != nil { return nil, core.WrapActivation(core.ActivationFileInput, err) }
    absolute, err := filepath.Abs(path)
    if err != nil { return nil, core.WrapActivation(core.ActivationFileInput, err) }
    info, err := os.Lstat(absolute)
    if err != nil { return nil, core.WrapActivation(core.ActivationFileInput, err) }
    if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > subscriptions.DefaultMaxProfileBytes {
        return nil, core.WrapActivation(core.ActivationFileInput, errors.New("unsafe source file"))
    }
    file, err := os.Open(absolute)
    if err != nil { return nil, core.WrapActivation(core.ActivationFileInput, err) }
    defer file.Close()
    opened, err := file.Stat()
    if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
        return nil, core.WrapActivation(core.ActivationFileInput, errors.New("source file changed"))
    }
    body, err := io.ReadAll(io.LimitReader(file, subscriptions.DefaultMaxProfileBytes+1))
    if err != nil || len(body) == 0 || int64(len(body)) > subscriptions.DefaultMaxProfileBytes {
        return nil, core.WrapActivation(core.ActivationFileInput, errors.New("source read failed or exceeded limit"))
    }
    if err := ctx.Err(); err != nil { return nil, core.WrapActivation(core.ActivationFileInput, err) }
    return body, nil
}
```
- [ ] **Step 4: Run green:** `go test -tags ci ./app -run '^TestLocalProfileFileBoundary$' -count=1`.
- [ ] **Step 5: Commit:** `git add app/local_profile.go app/local_profile_test.go subscriptions/store.go subscriptions/model.go && git commit -m "feat(app): read bounded local profile inputs"`.

### Task 2: Run Local Profile Under the Existing Foreground Owner

**Files:**
- Modify: `app/runtime.go`, `app/serve.go`, `app/lifecycle.go`, `app/service.go`, `app/app.go`, `app/owner_lock.go`
- Test: `app/local_profile_test.go`, `app/owner_lock_test.go`, `app/service_loop_test.go`

**Interfaces:**
- Consumes: Task 1 `readLocalProfile` and safe stages from the prerequisite plan.
- Produces: `app.RunFile(ctx context.Context, path string, ready func() error) error` using XDG dirs/default IPC endpoint; `app.RunFileAt(ctx context.Context, configDir, stateDir, endpoint, path string, ready func() error) error` for deterministic tests. Their shared private owner constructor is `runAt(ctx context.Context, configDir, stateDir, endpoint string, localProfile []byte, ready func() error) error`. `runtimeService.profileForRuntime() ([]byte, error)` returns its immutable local bytes or the saved active subscription profile. A nil `ready` is valid. The callback executes exactly once after authenticated controller readiness and before the service blocks. Both calls own the state lock for their whole lifetime.

- [ ] **Step 1: Write failing owner tests.** Use `fakeAppMihomo` and a private local file in a temporary directory. `RunFileAt` must report ready once, keep child/IPC live until cancellation, then remove its local-generated config, restore a fake System Proxy's previous settings, and release the state lock. If `ready()` returns a write error, it must cleanly stop the child and release the lock without claiming success. A second `RunAt` against the same state fails with `errStateInUse` and names no path; the conflict text must not say the command is refreshing. A fake child that exits before readiness returns `ActivationControllerReadiness` or `ActivationProcessStart` without calling `ready`; a child killed after readiness ends the command with a safe process failure. Adapt the fake controller to report zero groups for a proxies-only profile and assert readiness still succeeds. Create an orphan regular `generated-local-*.yaml` and a similarly named symlink before startup: remove only the owned regular file and reject the symlink without following it. Assert `stateDir/generated.yaml` is never overwritten.

```go
ctx, stop := context.WithCancel(context.Background())
ready := make(chan struct{}, 1)
done := make(chan error, 1)
go func() { done <- RunFileAt(ctx, configDir, stateDir, endpoint, profilePath, func() error { ready <- struct{}{}; return nil }) }()
select { case <-ready: case <-time.After(5*time.Second): t.Fatal("controller never ready") }
if err := RunAt(context.Background(), configDir, stateDir, endpoint); !errors.Is(err, errStateInUse) { t.Fatalf("second owner = %v", err) }
stop()
if err := <-done; err != nil { t.Fatal(err) }
```

- [ ] **Step 2: Run red:** `go test -tags ci ./app -run 'Test(LocalProfileForegroundOwner|LocalProfileReadinessFailure|LocalProfileNoGroups|LocalProfileOrphanCleanup|LocalProfileUnexpectedChildExit)$' -count=1`. Expected: `RunFileAt` absent.
- [ ] **Step 3: Reuse startup and lifecycle ownership.** Refactor existing `RunAt` to call `runAt(ctx, configDir, stateDir, endpoint, nil, nil)`; move its lock/config/store/server setup into that private function unchanged. `RunFileAt` passes the owned read-once byte slice to `runAt`, which assigns `s.localProfile` before scheduler startup. In `app/lifecycle.go`, add `profileForRuntime()` and have `s.start(ctx)` use it instead of asking only `subs.ActiveProfile`; this is required for this task's foreground test to pass. Under the owner lock, create a private `generated-local-*.yaml` path and set `s.generatedPath`, leaving `generated.yaml` untouched. Change `s.run(ctx, startup func(context.Context) error)` so it initializes `processCtx`, workers, and cleanup defers before it calls `startup` on the lifecycle goroutine; migrate `app/service_loop_test.go`'s direct `s.run(ctx)` call to `s.run(ctx, nil)`. For local mode the startup callback calls `s.start(ctx)` and then `ready()`; for ordinary `RunAt` it is nil. The controller's first authenticated proxy snapshot, not a nonempty group list, establishes readiness. All setup failures after owner-lock acquisition must run the same private-config and child cleanup. Cancel/early child exit stops the child and restores System Proxy. Sweep only regular app-owned `generated-local-*.yaml` orphan files after acquiring the lock; reject symlinks. Refactor every hardcoded `stateDir/generated.yaml` in lifecycle/rollback to `s.generatedPath`, initialized to the old durable path for subscriptions. Update `errStateInUse` to `clashpulse: private state is in use; stop the other service and retry` for all owners.

```go
func RunFile(ctx context.Context, path string, ready func() error) error {
    configHome, stateHome := xdg.ConfigHome(), xdg.StateHome()
    if configHome == "" || stateHome == "" { return core.WrapActivation(core.ActivationStateCommit, errors.New("missing private home")) }
    return RunFileAt(ctx, filepath.Join(configHome, "clashpulse"), filepath.Join(stateHome, "clashpulse"), "", path, ready)
}
func RunFileAt(ctx context.Context, configDir, stateDir, endpoint, path string, ready func() error) error {
    profile, err := readLocalProfile(ctx, path)
    if err != nil { return err }
    return runAt(ctx, configDir, stateDir, endpoint, profile, ready)
}
func RunAt(ctx context.Context, configDir, stateDir, endpoint string) error {
    return runAt(ctx, configDir, stateDir, endpoint, nil, nil)
}
```
```go
func (s *runtimeService) profileForRuntime() ([]byte, error) {
    if s.localProfile != nil { return s.localProfile, nil }
    _, profile, err := s.subs.ActiveProfile()
    return profile, err
}
```

- [ ] **Step 4: Run green:** `go test -tags ci ./app -count=1`. Expected: all app lifecycle tests pass, including orphan cleanup, no-group readiness, child exit, owner conflict, and the existing direct `s.run(ctx, nil)` caller.
- [ ] **Step 5: Commit:** `git add app/runtime.go app/serve.go app/lifecycle.go app/service.go app/app.go app/owner_lock.go app/local_profile_test.go app/owner_lock_test.go app/service_loop_test.go && git commit -m "feat(app): own local profile in foreground"`.

### Task 3: Preserve Local Source Across App-Owned Work

**Files:**
- Modify: `app/lifecycle.go`, `app/serve.go`, `app/resources.go`, `app/commands.go`, `app/runtime.go`
- Test: `app/local_profile_test.go`, `app/static_resource_test.go`

**Interfaces:**
- Consumes: Task 2 `runtimeService.localProfile []byte`, `runtimeService.generatedPath string`, `runtimeService.profileForRuntime`, and `RunFileAt`.
- Produces: local-source aware `resourceDeadlines`, `prepareResourceRefresh`, config reload, and explicit subscription activation. After a successful explicit subscription activation, local mode and its private generated config are cleared only after the subscription's durable commit; failure restores local bytes/runtime/selection.

- [ ] **Step 1: Write failing transition tests.** After `RunFileAt` signals ready, overwrite and remove its source path, then send IPC `CommandRestart` and refresh one configured local resource using a tiny `httptest` or temporary-file fixture; verify the controller comes back using the original profile's group identity and selected proxy without rereading the source. Use an explicit empty resources file for tests not exercising refresh, so reviewed default downloads do not affect timing. Test config reload with an unchanged local profile. Configure an inactive subscription, make its activation fail, and assert `s.localProfile`, local generated config path, selected group, and System Proxy state remain; on a valid later activation assert `s.localProfile == nil` and `s.subs.ActiveID()` is the selected subscription. The subscription scheduler must never select an inactive downloaded source automatically.

```go
before, err := client.Snapshot(ctx)
if err != nil { t.Fatal(err) }
if err := os.Remove(profilePath); err != nil { t.Fatal(err) }
if _, err := client.Send(ctx, ipc.Command{Kind: ipc.CommandRestart}); err != nil { t.Fatal(err) }
state := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
    return state.Revision > before.Revision+1 && len(state.Groups) == 1 &&
        state.Groups[0].ID == opaqueID("select-main") && state.Groups[0].Selected == opaqueID("node-b") && len(state.Jobs) == 0
})
if len(state.Groups) != 1 { t.Fatal("local profile was lost") }
```
- [ ] **Step 2: Run red:** `go test -tags ci ./app -run 'Test(LocalProfileSurvivesSourceRemoval|LocalProfileRollbackAfterSubscriptionFailure|LocalProfileResourceRefresh)$' -count=1`. Expected: resource preparation still treats local mode as an inactive resource-only cache, and explicit source switching does not retain the local backup.
- [ ] **Step 3: Route remaining work through the active source.** Keep Task 2's one in-memory byte owner and `profileForRuntime`; do not add a generic profile-provider interface. `app.prepareResourceRefresh` and `app.resourceDeadlines` must treat the local profile as active. `app.applyChange` already reaches `s.start`, which now uses `profileForRuntime`; verify its rollback preserves the local generated path. Add local source bytes and generated path to `runtimeBackup` when an IPC subscription activation is attempted. Validate the subscription candidate before switching from the local generated config to durable `generated.yaml`; clear local bytes/path and delete the local config only after `setApplied` and resource finalization succeed. Restore local bytes/path before restarting the old process on any failure. Do not change remote subscription snapshots or URLs. A local IPC Stop must end the foreground run, rather than leave a CLI that claims to be active.

```go
if s.localProfile == nil {
    if _, err := s.subs.ActiveID(); err != nil { return time.Time{}, nil }
}
```

- [ ] **Step 4: Run green:** `go test -tags ci ./app -run 'Test(LocalProfileSurvivesSourceRemoval|LocalProfileRollbackAfterSubscriptionFailure|LocalProfileResourceRefresh)$' -count=1`.
- [ ] **Step 5: Commit:** `git add app/lifecycle.go app/serve.go app/resources.go app/commands.go app/runtime.go app/local_profile_test.go app/static_resource_test.go && git commit -m "feat(app): keep local profile through runtime changes"`.

### Task 4: Show a Path-Free Local Source Over IPC

**Files:**
- Modify: `core/snapshot.go`, `ipc/protocol.go`, `app/runtime.go`, `tui/render.go`, `ui/ui.go`, `ui/views.go`
- Test: `core/snapshot_test.go`, `ipc/protocol_test.go`, `ipc/server_test.go`, `tui/render_test.go`, `ui/display_test.go`

**Interfaces:**
- Consumes: Task 3 active-source decision.
- Produces: `core.Snapshot.ActiveSource string` with `none`, `local`, or `subscription`; no path or body. `ipc.ProtocolVersion = 4`. The app publisher always supplies one of those three values; IPC also accepts an empty value as `none` for existing synthetic zero-value test snapshots. GUI/TUI label `local` as `local profile` and preserve the existing safe subscription display and group-selection behavior.

- [ ] **Step 1: Write failing snapshot and client tests.** IPC rejects an `ActiveSource` containing a path, URL, or password; accepts `none`, `local`, and `subscription`. Feed `core.Snapshot{ActiveSource:"local", Groups: []core.GroupSnapshot{{ID:"managed", Type:"Selector", Selected:"alpha"}}}` to the TUI mock screen and Fyne test app, assert visible status says `local profile` and contains neither the source filename nor a credential. A subscription snapshot still displays its existing safe name. Confirm old protocol version 3 is rejected against v4.

```go
if err := validateSnapshot(core.Snapshot{ActiveSource:"/tmp/private.yaml"}); err == nil { t.Fatal("IPC accepted a source path") }
if err := validateSnapshot(core.Snapshot{ActiveSource:"local"}); err != nil { t.Fatal(err) }
```

- [ ] **Step 2: Run red:** `go test -tags ci ./core ./ipc ./tui ./ui -run 'Test(LocalActiveSource|SnapshotRejectsSourcePath|RenderStatusIdentifiesConnectionAndActiveProfile)$' -count=1`. Expected: missing `ActiveSource`, incorrect local status, and v3 handshake.
- [ ] **Step 3: Add the typed, path-free snapshot field** and wire its existing publisher/client render sites. Keep the subscription list unchanged; local mode is not a fake subscription row. Use a fixed label and v4 handshake; do not add a general profile editor or expose the filesystem source.

```go
// Insert this field in core.Snapshot without dropping existing fields:
ActiveSource string

// Set it at the app snapshot owner before publishing:
state.ActiveSource = "none"
if s.controller != nil {
    if s.localProfile != nil { state.ActiveSource = "local" } else { state.ActiveSource = "subscription" }
}
```

`validateSnapshot` rejects every other nonempty value with a fixed error that does not echo input. Existing zero-value test snapshots may use `""` as `none`; actual app snapshots always publish explicit `none`. `stateSnapshot` never copies the local source path or body.
- [ ] **Step 4: Run green:** `go test -tags ci ./core ./ipc ./tui ./ui -count=1`.
- [ ] **Step 5: Commit:** `git add core/snapshot.go core/snapshot_test.go ipc/protocol.go ipc/protocol_test.go ipc/server_test.go app/runtime.go tui/render.go tui/render_test.go ui/ui.go ui/views.go ui/display_test.go && git commit -m "feat(ipc): report local active profile without path"`.

### Task 5: Expose the Foreground CLI and Verify Actual Interaction

**Files:**
- Modify: `cmd/clashpulse/cli.go`, `cmd/clashpulse/main.go`, `cmd/clashpulse/main_test.go`, `README.md`
- Test: `app/local_profile_test.go` (IPC and signal scenario)

**Interfaces:**
- Consumes: Task 2 `app.RunFile`, Task 4 v4 IPC snapshot, and prerequisite safe stage errors.
- Produces: `clashpulse activate <profile.yaml>` (required one path). It writes `local profile active; press Ctrl-C to stop` once after readiness, stays foreground until cancellation, and returns nonzero with a fixed safe stage on activation/cleanup failure. It does not add `activate subscription` or daemonize.

- [ ] **Step 1: Write failing command and interaction tests.** Extend `main_test.go` with a CLI action that invokes `ready()` then blocks on `ctx.Done()`; start `runCLI` in a goroutine, assert stdout is empty before ready, receives exactly the success line after readiness, stays blocked until cancellation, and exits 0. Missing/extra operand fails without invoking an action. A fake action returning `core.WrapActivation(core.ActivationControllerReadiness, errors.New("password=private"))` exits 1 with the fixed stage and neither the password nor file path. Extend `app/local_profile_test.go` to dial the actual per-user test IPC endpoint while the foreground app runs, select a managed group through IPC, and confirm the coalesced snapshot changes selection.

```go
ctx, cancel := context.WithCancel(context.Background())
ready, announced := make(chan struct{}), make(chan struct{})
stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
actions := cliActions{activateFile: func(ctx context.Context, _ string, announce func() error) error {
    <-ready
    if err := announce(); err != nil { return err }
    close(announced)
    <-ctx.Done()
    return nil
}}
done := make(chan int, 1)
go func() { done <- runCLI(ctx, []string{"clashpulse", "activate", "profile.yaml"}, stdout, stderr, actions) }()
select { case <-done: t.Fatal("returned before activation"); default: }
close(ready)
select { case <-announced: case <-time.After(time.Second): t.Fatal("readiness was not announced") }
if stdout.String() != "local profile active; press Ctrl-C to stop\n" { t.Fatalf("output = %q", stdout.String()) }
cancel()
if code := <-done; code != 0 || stderr.Len() != 0 { t.Fatalf("exit=%d stderr=%q", code, stderr.String()) }
```

- [ ] **Step 2: Run red:** `go test -tags ci ./cmd/clashpulse ./app -run 'Test(CLIActivateLocalFile|LocalProfileIPCSelectsGroup)$' -count=1`. Expected: unknown `activate` command and missing `cliActions.activateFile`.
- [ ] **Step 3: Add the one-path CLI command and wire ownership.** Add `activateFile func(context.Context, string, func() error) error` to `cliActions`. `newCLICommand` adds `activate` with one required string path argument; its action supplies a callback that writes the fixed readiness line to `command.Root().Writer`. `main.go` wires it to `app.RunFile`. The CLI displays only the error's fixed public `Error()` string. Document the foreground behavior and privacy boundary in README.

```go
{Name:"activate", ArgsUsage:"<profile.yaml>", Arguments:[]cli.Argument{&cli.StringArgs{Name:"path", Min:1, Max:1}},
    Action:func(ctx context.Context, command *cli.Command) error {
        if command.Args().Len() != 0 || len(command.StringArgs("path")) != 1 { return errors.New("activate requires one file") }
        if actions.activateFile == nil { return errors.New("clashpulse: activation is unavailable") }
        return actions.activateFile(ctx, command.StringArgs("path")[0], func() error {
            _, err := fmt.Fprintln(command.Root().Writer, "local profile active; press Ctrl-C to stop")
            return err
        })
    },
}
```

Use the repo's existing urfave/cli v3 `StringArgs` argument getter; never print the operand path on errors.
- [ ] **Step 4: Run green and native smoke:** `go test -tags ci ./cmd/clashpulse ./app -count=1`. Run `go build -tags ci -o /tmp/clashpulse-local-smoke ./cmd/clashpulse`, then launch the actual `activate` command against a disposable fake Mihomo and local file in a supervised terminal, observe the readiness line, send Ctrl-C, verify child exit/private cleanup, and remove the temporary binary. No real proxy credentials belong in smoke output.
- [ ] **Step 5: Run final verification and commit:** `go test -tags ci ./... && go vet -tags ci ./...`; compile Windows/macOS test targets, record that native runtime was not exercised if unavailable; run the DRY pass and focused checks again. `git add cmd/clashpulse/cli.go cmd/clashpulse/main.go cmd/clashpulse/main_test.go app/local_profile_test.go README.md && git commit -m "feat(cli): activate local profile in foreground"`.
