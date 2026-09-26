# Foreground Local-File Activation and Safe Failure Stages

## Goal

`clashpulse activate <profile.yaml>` is one command to run a selected local Mihomo profile without adding a subscription or opening the GUI. It reports when the controller is ready, then remains in the foreground as the sole lifecycle owner. Ctrl-C stops Mihomo and restores any System Proxy settings the command changed. Activation failures identify the failed stage without displaying proxy credentials, subscription URLs, source paths, or raw Mihomo output. The same stage is visible to GUI and TUI clients when subscription activation fails.

## Source and lifetime

The operand is a required file path, not a subscription ID or URL. Resolve a relative path against the caller's working directory. Reject a missing, non-regular, symlinked, empty, or oversized file before touching active runtime state; cap the read at the existing subscription profile limit. Check cancellation before and after the bounded read, and retain immutable bytes in the foreground app process. Do not alter the source file, persist its path in TOML, or import it into the subscription store. Use a separate private 0600 `generated-local-*.yaml` for Mihomo, never the durable subscription-generated config. Remove it during controlled shutdown and safely clean orphaned local-generated files under the owner lock at the next start. No local-profile selection survives process exit.

The command uses the existing config.Store intent, selected Mihomo binary/capabilities, resource registry, DNS/filter renderer, process adapter, and controller. Required resources are staged and validated before starting Mihomo. User settings, including an explicitly enabled System Proxy, are honored rather than silently dropped. File-based activation must not mutate a configured subscription or imply that one is active.

## Foreground owner and IPC

Reuse the app's exclusive state lock and lifecycle owner. The command starts the same per-user IPC service without a GUI and refuses to run alongside another desktop or headless owner. It prints a short success line only after the Mihomo controller responds with its initial proxy snapshot; a profile without groups can still be active. It then blocks until cancellation. Signal shutdown waits for child termination and restores System Proxy state; failures return a nonzero exit status. Unexpected child exit ends the foreground command with a safe error rather than leaving it apparently active. There is no daemonization or detached child.

While running, restart, config reload, resource refresh, and monitor work use the immutable in-memory local profile. The existing IPC group-selection command remains available to a TUI client. An explicit later subscription activation may replace the local runtime transactionally; failure retains the local runtime. The subscription scheduler may refresh configured subscriptions but cannot silently switch the active local source.

Add a typed, path-free active-source identity to immutable snapshots (`none`, `local`, `subscription`). Keep subscription rows limited to actual subscriptions; a local file is not a synthetic subscription. GUI/TUI overview and status show `local profile` while appropriate, without the file path or proxy credentials. Bump the IPC schema version and reject older clients rather than guessing the new state shape.

## Credential-safe failures

Use a small typed activation-stage classification at the app/subscription boundary: file input, binary inspection/capability, managed resource, generated config validation, process launch, controller readiness, system proxy, and private-state commit/rollback. Each stage has a fixed public phrase and an exit failure. Binary capability and managed-resource failures may append one affected resource ID only when it is a validated stable ID from the typed resource registry; an invalid ID is omitted. Preserve the affected setting/resource instead of collapsing it into a generic compatibility error. Never pass raw subprocess, controller response, profile field, source path, URL, or arbitrary wrapped error text to IPC, GUI/TUI diagnostics, or CLI stderr. Internal errors still drive rollback and cancellation. Preserve existing sanitized HTTP status reporting for refresh; do not add a verbose mode that dumps configuration or credentials.

`subscriptions.Activate` currently collapses an application failure to a generic error. Carry only the typed safe stage through this boundary. Both the active issue and diagnostic ring show the same bounded reason. A successful retry clears its prior issue. A failed start leaves no active System Proxy and no stale child or IPC endpoint; a failed replacement restores the previous runtime and selection.

## Verification

Use disposable local YAML fixtures with fake credentials, fake Mihomo executables/controllers, and temporary private state. Show red/green behavior for invalid or unsafe file, binary/config rejection, controller-readiness failure, system-proxy failure, successful foreground readiness and signal shutdown, state-lock conflict, and subsequent TUI group selection. Exercise restart/config reload/resource refresh against in-memory local bytes after the source file changes or disappears. Verify that snapshots and stderr contain stage labels but no paths, URLs, secrets, or proxy credentials. Run focused tests, full `go test -tags ci ./...`, `go vet -tags ci ./...`, and compile platform-specific files on Linux, macOS, and Windows; do not claim native Windows runtime behavior from cross-compilation alone.

When a configured `geosite` resource needs an unsupported capability, public diagnostics must name `geosite`; a malformed ID containing a URL or credential must be omitted while keeping the safe stage.
