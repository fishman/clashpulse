# Static Resource Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store active Mihomo resources at stable files under `state/clashpulse/resources/` without persistent generation directories, while preserving atomic writes, rollback, and crash recovery.

**Architecture:** `resources.Registry` owns stable destinations and a versioned manifest. It stages bytes and backups in a temporary transaction area under the same private filesystem, atomically replaces each final file, and recovers any interrupted set transaction before startup. `app` remains the sole owner of Mihomo stop/start, readiness, rollback, and System Proxy transitions.

**Tech Stack:** Go 1.26.6, existing `resources.Registry` and `Plan`, standard-library filesystem operations, existing Mihomo validator. No new dependency.

**Spec:** `docs/superpowers/specs/2026-09-26-clashpulse-static-resource-storage-design.md`

## Global Constraints

- Mihomo remains an external child process and the configuration authority.
- `app` remains the only lifecycle owner for process start/stop, readiness, configuration generation, restart, and shutdown.
- Generated config changes remain transactional; preserve the prior known-good config and runtime on validation or start failure.
- User profiles remain immutable inputs; resource files stay in private state with 0700 directories and 0600 files.
- No shell interpolation. Mihomo starts with an explicit executable and argv.
- Low CPU remains mandatory; no polling loops, busy loops, or per-resource goroutines.
- No new dependencies, bundled data, silent source changes, or user-source redistribution.
- Resource data is independently validated when cached without a profile; complete Mihomo config validation is required before a profile uses those paths.

## Review Focus

1. Crash after the first of several resource renames must recover the complete prior set before Mihomo starts.
2. A failed validation or restart must restore previous bytes, generated config, selected proxy state, and System Proxy state.
3. Migration from v1 generation metadata must reject missing, tampered, symlinked, or declaration-mismatched files without deleting the old generation.
4. Windows and Unix must use same-filesystem atomic file replacement and recover an interrupted transaction without exposing a partial file.
5. With no profile active, resource-only refresh must cache valid bytes but must not write generated config, start Mihomo, or enable System Proxy.

---

### Task 1: Static Resource Registry and Recovery

**Files:**
- Modify: `resources/registry.go`
- Modify: `resources/plan.go`
- Modify: `resources/status.go`
- Test: `resources/static_storage_test.go`
- Test: `resources/security_test.go`

**Interfaces:**
- Consumes: `config.Resource`, `resources.Validate`, and the current v1 `.resources.json` generation manifest.
- Produces: `Registry.PathsWithHome(snapshot)` resolves stable paths directly under the resource root; `Plan.Validate` validates using staged paths; `Plan.Commit` atomically promotes files and returns stable paths; `Plan.Rollback` restores the prior manifest/files; `NewRegistry` recovers an interrupted transaction and migrates v1 before exposing paths.

- [ ] **Step 1: Write stable-path and crash-recovery tests**

Use the existing local-file registry setup pattern. Cover all fixed basenames (`geoip.dat`, `geosite.dat`, `Country.mmdb`) plus the deterministic rule-resource filename. Verify committed paths have the resource root as their parent. Add a valid v1 manifest fixture and confirm migration preserves IDs, hashes, declarations, and bytes. Simulate a durable journal with two prior resource files and one new file already renamed; opening `NewRegistry` must restore both prior files before returning. Add a corrupt v1 file case proving migration fails without deleting the old generation.

```go
func TestStablePathsUseResourceRoot(t *testing.T) {
	root := t.TempDir()
	resources := []config.Resource{
		{ID: "geoip", Kind: config.ResourceGeoIP, Format: config.FormatDAT, Enabled: true},
		{ID: "geosite", Kind: config.ResourceGeoSite, Format: config.FormatDAT, Enabled: true},
		{ID: "country", Kind: config.ResourceMMDB, Format: config.FormatMMDB, Enabled: true},
		{ID: "cn", Kind: config.ResourceRuleSet, Format: config.FormatMRS, RuleType: config.RuleDomain, Enabled: true},
	}
	bodies := [][]byte{[]byte("geoip"), []byte("geosite"), []byte("\xab\xcd\xefMaxMind.com"), {0x28, 0xb5, 0x2f, 0xfd}}
	for i := range resources {
		path := filepath.Join(root, resources[i].ID+".source")
		if err := os.WriteFile(path, bodies[i], 0o600); err != nil { t.Fatal(err) }
		resources[i].URL = path
	}
	client := download.NewClient(func(download.Route) (http.RoundTripper, error) { return http.DefaultTransport, nil })
	registry, err := NewRegistry(filepath.Join(root, "resources"), client)
	if err != nil { t.Fatal(err) }
	plan, err := registry.Stage(context.Background(), config.Snapshot{Resources: resources}, download.Direct)
	if err != nil { t.Fatal(err) }
	if err := plan.ValidateResources(); err != nil { t.Fatal(err) }
	paths, err := plan.Commit()
	if err != nil { t.Fatal(err) }
	for _, id := range []string{"geoip", "geosite", "country", "cn"} {
		if filepath.Dir(paths[id]) != registry.home { t.Fatalf("%s path = %q", id, paths[id]) }
	}
}
```
- [ ] **Step 2: Run tests red against generation directories**

Run: `go test -tags ci ./resources -run 'Test(StablePathsUseResourceRoot|RecoverPartialStaticPromotion|MigrateV1Generation|RejectCorruptV1Migration)$' -count=1`
Expected: FAIL because current paths are under `generations/gen-*` and no static transaction journal exists.

- [ ] **Step 3: Implement static destinations and recovery journal**

Keep `filename(resource)` and `ManagedFilename(resource)` as the only basename registry. Stage replacements and prior-file backups in a temporary private same-filesystem transaction directory. Persist a 0600 journal with the prior manifest, per-file backup names/hashes, and transaction phase before promoting files. Write each candidate via a 0600 sibling temporary file, sync, and atomic rename; sync the resource directory. Write the v2 manifest only after the complete file set is present. Recover an unfinished journal in `NewRegistry` before reading status or starting Mihomo. Migrate v1 generations through this transaction and remove old generation directories only after the stable manifest commits. `Plan.Validate` receives staged paths; `Plan.Commit` returns stable paths.

- [ ] **Step 4: Run registry tests green**

Run: `go test -tags ci ./resources -count=1`
Expected: PASS; files resolve directly under `resources/`, interrupted promotion restores the complete prior set, and invalid v1 data is retained.

- [ ] **Step 5: Commit registry storage**

```sh
git add resources/registry.go resources/plan.go resources/status.go resources/static_storage_test.go resources/security_test.go
git commit -m "feat(resources): store data at stable paths"
```

### Task 2: Lifecycle Promotion and Runtime Rollback

**Files:**
- Modify: `app/lifecycle.go`
- Modify: `app/serve.go`
- Modify: `app/resources.go`
- Modify: `app/runtime.go`
- Modify: `README.md`
- Test: `app/static_resource_test.go`
- Test: `app/lifecycle_test.go`

**Interfaces:**
- Consumes: Task 1's staged-path candidate validation, stable `Registry.PathsWithHome`, atomic `Plan.Commit`/`Rollback`, and startup recovery.
- Produces: `app` quiesces Mihomo/System Proxy before file promotion, regenerates/validates against stable paths, restarts, and restores the full previous runtime on failure.

- [ ] **Step 1: Write lifecycle tests for static resource replacement**

Use local resource files and the existing fake Mihomo controller. Cover active-profile refresh of multiple resources, requiring the old process to stop before the first stable file changes; verify the generated candidate references stable paths. Inject final validation and controller-readiness failures after file promotion; assert old file hashes, manifest, generated config, proxy selection, active snapshot, and System Proxy state are restored. Verify startup recovery runs before Mihomo starts after a partial journal is left behind. Keep the existing `TestRefreshResourceWithoutActiveProfileCachesEnabledResources` path green.

- [ ] **Step 2: Run lifecycle tests red against the current runtime**

Run: `go test -tags ci ./app -run 'Test(StaticResourcesPromoteAfterQuiesce|StaticResourceFailureRestoresRuntime|StaticJournalRecoversBeforeStart|RefreshResourceWithoutActiveProfileCachesEnabledResources)$' -count=1`
Expected: FAIL because runtime currently passes generation homes to Mihomo and has no static-file promotion boundary.

- [ ] **Step 3: Integrate stable files with lifecycle ownership**

For an active profile, validate the candidate against staged paths, restore System Proxy, stop Mihomo, commit the stable file set, resolve stable paths from the registry, regenerate and validate the final-path config, write generated config atomically, restart, wait for controller readiness, then restore System Proxy. On any post-promotion error, roll back the resource journal and restore generated config, selections, child process, and proxy state. With no profile, retain resource-only cache behavior and do not start Mihomo. Remove generation leases and generation-path state only after all callers use stable paths. Document the final resource locations in README.

- [ ] **Step 4: Run focused and full verification**

Run: `go test -tags ci ./app -run 'Test(StaticResourcesPromoteAfterQuiesce|StaticResourceFailureRestoresRuntime|StaticJournalRecoversBeforeStart)$' -count=1`
Expected: PASS; active Mihomo never observes mixed files and all failure paths retain the prior runtime/resource set.

Run: `go test -tags ci ./...`
Expected: all packages pass.

Run: `go vet -tags ci ./...`
Expected: no diagnostics.

- [ ] **Step 5: Commit lifecycle integration**

```sh
git add app/lifecycle.go app/serve.go app/resources.go app/runtime.go app/static_resource_test.go app/lifecycle_test.go README.md
git commit -m "feat(app): promote static resources transactionally"
```
