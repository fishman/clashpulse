# Bundled Mihomo and Homebrew Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** macOS (arm64, amd64) and Windows (amd64) release packages ship a pinned, verified official Mihomo that ClashPulse selects by default; macOS users can also install a source-built formula from `fishman/homebrew-clashpulse`.

**Architecture:** A link-time `internal/bundled` identity (version + executable SHA-256) marks a build as carrying Mihomo. `config` derives the default binary from it, `mihomo.InstallBundled` verifies the packaged file against it and copies it into private state, and `app` inspects that copy like any other selection. A stdlib Go tool fetches lock-pinned upstream assets in CI.

**Tech Stack:** Go 1.26.6 stdlib (`archive/zip`, `compress/gzip`, `crypto/sha256`, `net/http`), GitHub Actions, Homebrew Ruby formula.

**Spec:** `docs/superpowers/specs/2026-11-23-clashpulse-bundled-mihomo-design.md`

## Global Constraints

- Mihomo pin: v1.19.34; assets `mihomo-darwin-arm64-v1.19.34.gz`, `mihomo-darwin-amd64-v1-v1.19.34.gz`, `mihomo-windows-amd64-v1-v1.19.34.zip`; source `https://github.com/MetaCubeX/mihomo/archive/refs/tags/v1.19.34.tar.gz`.
- Never fetch "latest"; a checksum mismatch fails the build.
- A bundled verification failure is a named compatibility error; never fall back to another binary.
- Private copy: `<state>/mihomo/<version>-<sha256[:12]>/<packaged base name>`, directories 0700, file 0700.
- Default binary: `bundled` iff `bundled.SHA256 != ""`, else `system`. Explicit config values win.
- Windows arm64 is not built. The windows-amd64 package is smoke-run on `windows-11-arm`.
- No new Go module dependency.
- Conventional commits, no AI marker; do not push until the whole plan is green locally.

## Review Focus

1. Fresh install from a release package: seeded `config.toml` must not pin `system` (test: seed then load with `SHA256` set resolves `bundled`).
2. Packaged binary replaced or truncated after install: the private copy must not be trusted blindly (test: tampered existing copy is replaced; mismatched packaged file errors and leaves no copy).
3. Homebrew or source build with an old config saying `bundled`: named error, no fallback (test: `InstallBundled` with empty identity returns the not-installed error).
4. macOS app path layout: `Contents/MacOS/clashpulse` -> `Contents/Resources/mihomo` (test: `packagedPath` per GOOS).
5. Archive with zero or several entries, or a path entry: fetch tool rejects it (test: zip with two entries fails).

## Spec deviation

The spec's Homebrew `test do` asserting the resolved binary needs a CLI command that does not exist; adding a command only for a package test is not justified. The formula test runs `clashpulse version`; the build-default behavior is pinned by the Go seed-then-load test (Task 1), and a Homebrew build sets no identity by construction. The spec is updated in Task 1's commit.

---

### Task 1: Build identity, default binary, seeded config

**Files:**
- Create: `internal/bundled/bundled.go`
- Modify: `config/load.go:69-72` (`applyDefaults`), `examples/config.toml:2-4`
- Test: `config/bundled_default_test.go`
- Modify: spec (Homebrew test line)

**Interfaces:**
- Produces: `bundled.Version string`, `bundled.SHA256 string`, `bundled.Available() bool`; ldflags paths `github.com/fishman/clashpulse/internal/bundled.Version` and `.SHA256`.

- [ ] **Step 1: Failing test**

```go
func TestSeededConfigTakesBuildBinaryDefault(t *testing.T) {
	for _, tc := range []struct{ sum, want string }{{"", "system"}, {strings.Repeat("a", 64), "bundled"}} {
		old := bundled.SHA256
		bundled.SHA256 = tc.sum
		dir := t.TempDir()
		if err := Seed(dir); err != nil { t.Fatal(err) }
		snapshot, err := Load(dir)
		bundled.SHA256 = old
		if err != nil { t.Fatal(err) }
		if snapshot.Mihomo.Binary != tc.want { t.Fatalf("sum %q: binary %q, want %q", tc.sum, snapshot.Mihomo.Binary, tc.want) }
	}
}
```
Plus: an explicit `binary = "system"` with `SHA256` set stays `system`.

- [ ] **Step 2: Run red** - `go test -tags ci ./config -run TestSeededConfigTakesBuildBinaryDefault` fails (package missing, then seeded `system`).
- [ ] **Step 3: Implement**

```go
// Package bundled records the Mihomo executable a release package ships.
// The release workflow sets both values with -ldflags -X; every other build
// leaves them empty and has no bundled Mihomo.
package bundled

var (
	Version string
	SHA256  string
)

func Available() bool { return SHA256 != "" }
```
`applyDefaults`: `s.Mihomo.Binary = "system"; if bundled.Available() { s.Mihomo.Binary = "bundled" }`.
`examples/config.toml`: header "Defaults: bundled Mihomo in macOS/Windows release packages, system Mihomo otherwise, ..."; line 4 becomes `# binary = "system" # Default: "bundled" in macOS/Windows release packages, otherwise "system"; or an executable path.`
- [ ] **Step 4: Green** - `go test -tags ci -count=1 ./config ./examples`; also any test pinning the examples text.
- [ ] **Step 5: Commit** `feat(config): default to the bundled mihomo a package ships`

### Task 2: Verified private copy of the packaged Mihomo

**Files:**
- Create: `mihomo/bundled.go`, `mihomo/bundled_test.go`
- Modify: `app/lifecycle.go:29-39`, `app/runtime.go:565-572`, `ui/display.go:84-86`, `localize/locale/gui.zh-CN.toml`

**Interfaces:**
- Consumes: `bundled.Version`, `bundled.SHA256`, `bundled.Available()`.
- Produces: `mihomo.InstallBundled(stateDir string) (string, error)`; `mihomo.PruneBundled(stateDir, keep string)`; error texts `bundled Mihomo is not installed`, `bundled Mihomo failed verification`.

- [ ] **Step 1: Failing tests** (fake executable bytes in temp dirs, hash computed in test):
  - matching packaged file -> returns `<root>/v1-<sum12>/mihomo`, file bytes equal, dir mode 0700, file mode 0700 (Unix).
  - packaged bytes differ from sum -> error contains `failed verification`; version dir holds no executable.
  - packaged missing -> error contains `not installed`.
  - existing matching copy -> mtime unchanged after second install (reused).
  - existing tampered copy -> replaced with packaged bytes.
  - `packagedPath("darwin", "/A/ClashPulse.app/Contents/MacOS/clashpulse")` == `/A/ClashPulse.app/Contents/Resources/mihomo`; windows -> `<dir>\mihomo.exe` sibling.
  - `PruneBundled` removes sibling version dirs, keeps the active one.
- [ ] **Step 2: Run red** - `go test -tags ci ./mihomo -run Bundled`.
- [ ] **Step 3: Implement** `installBundled(packaged, root, version, sum string) (string, error)`: copy packaged -> temp file in version dir while hashing; compare to `sum`; chmod 0700; rename onto destination. Reuse destination when its hash already equals `sum`. `InstallBundled` returns the not-installed error when `!bundled.Available()`, resolves `os.Executable()` + `packagedPath(runtime.GOOS, exe)`.
  `app.selectedCapability`: for `bundled`, `path, err := mihomo.InstallBundled(s.stateDir)`, then `mihomo.Inspect(ctx, mihomo.Selection{Kind: "bundled", Path: path})`, then `mihomo.PruneBundled(s.stateDir, path)` on success.
  `compatibilityFailure` and `compatibilityLabel` map `bundled Mihomo failed verification`; zh-CN entry `内置 Mihomo 校验失败`.
- [ ] **Step 4: Green** - `go test -tags ci -count=1 ./mihomo ./app ./ui`, `LANGUAGE=zh_CN` for `./ui`.
- [ ] **Step 5: Commit** `feat(mihomo): install the packaged mihomo as a verified private copy`

### Task 3: Lock file and fetch tool

**Files:**
- Create: `packaging/mihomo.lock`, `packaging/mihomo/LICENSE` (from `mihomo-1.19.34/LICENSE` in the pinned source), `tools/fetch-mihomo/main.go`, `tools/fetch-mihomo/main_test.go`

**Interfaces:**
- Produces CLI: `go run ./tools/fetch-mihomo -target <darwin-arm64|darwin-amd64|windows-amd64|source> -out <path> [-provenance <path>]`; stdout `version=<v>` and `sha256=<hex of written file>` (GITHUB_OUTPUT format).

Lock content:
```text
version v1.19.34
darwin-arm64  mihomo-darwin-arm64-v1.19.34.gz        1a7a20eca63fbbaec90137717465bfde488d0db2a3a79b8fadebf9b2545b6e4c
darwin-amd64  mihomo-darwin-amd64-v1-v1.19.34.gz     84af3a9ffd4699e02f14b89859c58b4fc380f6a2135037c6e88e6822347ae657
windows-amd64 mihomo-windows-amd64-v1-v1.19.34.zip   703e325bfeee582f874b06d054eaae55ab7c194645d1835411685aafa85da5ea
source        v1.19.34.tar.gz                        e3284a44f1052f1c454c90d418faa244516c2470bf232b455ea18fc4f5fa6028
```

- [ ] **Step 1: Failing tests** with `httptest`: gz asset extracts to exact bytes and prints its sha; zip with one entry extracts; zip with two entries fails; archive checksum mismatch fails and writes no output file; unknown target fails; duplicate lock key fails; `source` writes the archive unchanged.
- [ ] **Step 2: Run red** - `go test ./tools/fetch-mihomo`.
- [ ] **Step 3: Implement** - stdlib only; 64 MiB download cap, 128 MiB extracted cap; write to temp beside `-out`, rename on success; mode 0755 for executables.
- [ ] **Step 4: Green**, plus one real run: `go run ./tools/fetch-mihomo -target darwin-arm64 -out /tmp/m` matches the lock.
- [ ] **Step 5: Commit** `build(packaging): pin mihomo v1.19.34 with a verified fetch tool`

### Task 4: Release packaging

**Files:** `.github/workflows/build.yml`, `.github/workflows/release.yml`

- [ ] **Step 1:** matrix adds `{os: macos-26-intel, target: darwin, arch: amd64}`; darwin/windows jobs run the fetch tool into the package (`Contents/Resources/mihomo`, `mihomo.exe`), add `-X .../internal/bundled.Version=$version -X .../internal/bundled.SHA256=$sha256` to ldflags, copy `packaging/mihomo/LICENSE` to `licenses/mihomo-LICENSE.txt` and the provenance file to `licenses/mihomo-PROVENANCE.txt`.
- [ ] **Step 2:** smoke step also runs bundled `mihomo -v` and `mihomo -t -f fixture.yaml` (single direct proxy).
- [ ] **Step 3:** job `smoke-windows-arm` (`windows-11-arm`, needs `desktop`) downloads `clashpulse-windows-amd64`, unzips, runs `clashpulse.exe version` and `mihomo.exe -v`.
- [ ] **Step 4:** `release.yml` publish job: checkout + setup-go, fetch `source` into `dist/mihomo-<version>-source.tar.gz` before checksums; notes name the bundled Mihomo version, GPL-3.0, the source asset, and the four packages.
- [ ] **Step 5:** push, CI green on all jobs (the only check for workflow changes). Commit `ci(release): bundle verified mihomo in macos and windows packages`.

### Task 5: Docs

**Files:** `docs/faq.md`, `docs/dependencies.md`, `docs/installation.md`, zh-CN mirrors, `README.md`, `README.zh-CN.md`, `docs/configuration.md` (+ zh-CN) where they show `binary = "system"`, new `RELEASING.md`.

- [ ] FAQ entry "Which Mihomo does ClashPulse use?"; dependencies: pinned version, lock, source asset, update policy; installation: four archives, Homebrew; RELEASING: tag, AUR update, tap update, Mihomo pin bump procedure.
- [ ] Commit `docs: describe the bundled mihomo and homebrew install`.

### Task 6: Homebrew tap

**Files (repo `fishman/homebrew-clashpulse`):** `Formula/clashpulse.rb`, `.github/workflows/test.yml`, `README.md`

```ruby
class Clashpulse < Formula
  desc "Desktop Mihomo client that measures nodes and switches only on proven improvement"
  homepage "https://github.com/fishman/clashpulse"
  url "https://github.com/fishman/clashpulse/archive/refs/tags/v0.1.1.tar.gz"
  sha256 "543272cf7d4374bdd8b8da8b53e99e8ef2a88cac1ac0429860bf84c5a4a61503"
  license "Apache-2.0"

  depends_on "go" => :build
  depends_on :macos
  depends_on "mihomo"

  def install
    system "go", "build", *std_go_args(ldflags: "-X 'main.version=clashpulse v#{version}'"), "./cmd/clashpulse"
  end

  test do
    assert_equal "clashpulse v#{version}", shell_output("#{bin}/clashpulse version").strip
  end
end
```
- [ ] Workflow: `brew install --build-from-source`, `brew test`, `brew audit --strict` on `macos-latest` and `macos-26-intel`.
- [ ] Push; tap CI green.
