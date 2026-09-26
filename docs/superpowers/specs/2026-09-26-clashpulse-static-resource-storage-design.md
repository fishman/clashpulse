# Static resource storage design

## Goal

Store active Mihomo data resources at stable private paths directly under `$XDG_STATE_HOME/clashpulse/resources/`, for example `geoip.dat`, `geosite.dat`, `Country.mmdb`, and the existing deterministic rule-resource filename. Remove persistent `generations/gen-*` resource directories. Preserve atomic file writes, complete-set consistency, rollback, and safe startup after interruption.

## Decisions

- The registry owns stable destination names and the active resource manifest. User profiles and `resources.toml` remain unchanged.
- Stage every changed resource into a private same-filesystem transaction area. Validate each resource's declared kind/format and optional SHA-256 pin before promotion.
- With an active profile, render and validate the complete candidate Mihomo config using staged files and the selected binary before touching active files. Then reset System Proxy and stop the Mihomo child before replacing any stable resource file. Promote each file with a same-directory temporary file, sync, and atomic rename. Validate the final-path candidate, write the generated config atomically, restart Mihomo, wait for controller readiness, then restore System Proxy.
- Per-file rename is atomic but a group of renames is not. A durable transaction journal records prior manifest and backup identities before promotion. On any error, restore the prior files, manifest, generated config, running process, selected proxies, and System Proxy. On next startup, recover an incomplete transaction before reading resources or starting Mihomo. Keep backups until the new process is ready; then remove the journal and temporary backups.
- Without an active profile, resource refresh validates and atomically promotes resource bytes only. It does not start Mihomo or claim profile compatibility. A later refresh/activation/start validates the complete generated config against the selected binary before applying it.
- Upgrade the resource manifest format to record stable filenames and hashes without a generation-directory pointer. Migrate the current v1 generation by verifying its manifest and bytes, atomically copying them to stable destinations, committing the new manifest, and only then deleting the old generation.
- No new dependency. No bundled data, silent URL additions, resource redistribution, or DNS/filter activation. Existing sources, pins, enabled state, and 24-hour intervals remain user intent.

## Failure and recovery

- Bad download, kind/format, pin, candidate config, or Mihomo validation: no active resource file or runtime change.
- Failure after file promotion but before Mihomo readiness: restore the previous manifest and each prior file atomically before restoring the previous runtime.
- Startup finds an incomplete journal: recover the previous resource set first; refuse to start with a mixed set if recovery fails.
- System Proxy remains disabled during the promotion window and is restored only after the ready Mihomo listener is available.
- A running Mihomo process never reads a mixed resource set: the lifecycle owner stops it before promotion and owns restart/rollback.

## Interfaces

- `resources.Registry` resolves stable paths from the active manifest; no generation lease or generation subdirectory escapes the package.
- `app` remains the sole owner of resource promotion, child stop/start, readiness, rollback, and System Proxy restoration.
- `mihomo.Render` receives stable absolute resource paths under the private resources directory.
- IPC snapshots continue to expose only destination identity and status, never full source URLs or credentials.

## Verification

- Resource tests cover stable destinations, same-directory atomic replacement, pin/format rejection, previous-file retention, and cleanup.
- App tests inject failures before promotion, after the first of multiple promotions, during final config validation, during child readiness, and during restart; each verifies the prior complete resource set and runtime remain active or recover on next start.
- Migration tests cover a valid v1 generation, corrupt manifest/data, and interruption while creating stable files.
- Run `go test -tags ci ./...` and `go vet -tags ci ./...`; CI already runs both tagged commands across desktop targets.
