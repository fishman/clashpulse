# Bundled Mihomo and Homebrew design

Date: 2026-11-23. Status: decisions made in chat; spec awaiting review.

## Goal

macOS and Windows release packages ship a pinned official Mihomo build so
ClashPulse works without a separate Mihomo install. Releases cover macOS arm64
and amd64 and Windows amd64. macOS users can also install from a Homebrew tap.

User decisions: macOS and Windows packages default to `bundled`; Windows
amd64 pins the `amd64-v1` build; Homebrew ships a source-built formula that
depends on Homebrew's `mihomo`; the tap is a separate repository
`fishman/homebrew-clashpulse`; release targets are macOS arm64 and amd64 and
Windows amd64. Windows arm64 is deferred: the `windows-11-arm` runner has
only an x86_64 mingw gcc and an MSVC-target clang, so Fyne's cgo build fails
(probe run 80); it needs an aarch64 mingw toolchain such as llvm-mingw, a new
build dependency. Windows on ARM can run x64 programs under emulation, but
the amd64 package is unverified there until the release workflow's
`windows-11-arm` smoke job (below) passes.

## Facts this relies on

- Mihomo's Makefile builds every release with `-tags with_gvisor`, so the
  official release assets include the gvisor TUN stack. Homebrew's `mihomo`
  formula also builds with `tags: "with_gvisor"`.
- Mihomo v1.19.34 (2026-11-11) publishes `mihomo-darwin-arm64-v1.19.34.gz`,
  `mihomo-darwin-amd64-v1-v1.19.34.gz`, and
  `mihomo-windows-amd64-v1-v1.19.34.zip`, and no checksum file.
- Probe run 80 (`macos-26-intel`) built and tested ClashPulse natively as a
  Mach-O x86_64 binary with Apple clang. Like every target, CI only runs
  `clashpulse version`, which opens no window; GUI startup on Intel macOS is
  unverified without real hardware.
- `binary = "bundled"` is accepted by config but `app/lifecycle.go` always
  fails it with "bundled Mihomo is not installed". The default is hard-coded
  `system` at `config/load.go:71`.
- ClashPulse's TUN support is Linux-only. Shipping a gvisor-capable Mihomo
  does not enable TUN on macOS or Windows; that stays a separate feature.

## Pinned inputs: `packaging/mihomo.lock`

A small line-based file, one line per platform asset plus the source archive:

```text
version v1.19.34
darwin-arm64  mihomo-darwin-arm64-v1.19.34.gz        <sha256>
darwin-amd64  mihomo-darwin-amd64-v1-v1.19.34.gz     <sha256>
windows-amd64 mihomo-windows-amd64-v1-v1.19.34.zip   <sha256>
source        v1.19.34.tar.gz                        <sha256>
```

Checksums are computed once from the upstream assets and committed. Changing
the version or any checksum is a reviewed commit; CI and releases never fetch
"latest". A download whose SHA-256 does not match fails the build.

## Build identity: `internal/bundled`

A package with two variables set by `-ldflags -X` at release build time:
`Version` (e.g. `v1.19.34`) and `SHA256` (hex of the extracted executable,
not the archive). Empty in development builds, Linux builds, and Homebrew
builds. Both `config` (default) and `mihomo` (resolution) read it; it imports
nothing.

`bundled.Available()` is `SHA256 != ""`.

## Default selection

`config` applies `bundled` when `bundled.Available()`, otherwise `system`.
This follows the build, not `GOOS`: a Homebrew or source build on macOS has
no bundle and keeps `system`. Defaults are applied once before validation and
never written back, so existing configs keep their explicit value, and a
config that says `bundled` on a build without a bundle fails with the existing
named compatibility error instead of silently using another binary.

`config.Seed` writes `examples/config.toml` verbatim on first start, and that
file currently sets `binary = "system"` explicitly, which would override the
build default on every fresh install. The seeded key becomes a comment
(`# binary = "system"  # default: "bundled" in macOS/Windows release
packages, "system" otherwise; or an executable path`), and the header line
"Defaults: system Mihomo" is reworded to match. Already-seeded configs keep
their explicit `system`; switching them is a user edit, not a migration.

## Packaged location and private copy

- macOS: `ClashPulse.app/Contents/Resources/mihomo`, found relative to
  `os.Executable()` (`../Resources/mihomo` from `Contents/MacOS/clashpulse`).
- Windows: `mihomo.exe` next to `clashpulse.exe`.

Selecting `bundled`:

1. Resolve the packaged path; missing -> "bundled Mihomo is not installed".
2. Hash it; mismatch with `bundled.SHA256` -> compatibility error naming the
   expected version, never a fallback.
3. Copy to private state `<state>/mihomo/<version>-<sha256[:12]>/mihomo[.exe]`
   (directory 0700, file 0700 on Unix), write to a temp file in that
   directory, hash the copy, then atomically rename. Reuse an existing copy
   only when its hash matches. Old version directories are removed after the
   new one validates.
4. Run the normal capability inspection on the private copy.

The private copy keeps the running binary independent of the app bundle
(macOS translocation, the user deleting or replacing the package while
running) and matches the AGENTS rule that the bundled executable is private
executable state. Copying happens in the lifecycle worker, never on the UI
thread.

## Release workflow

- Targets: linux-amd64 (unchanged, no bundle), darwin-arm64 (`macos-latest`),
  darwin-amd64 (`macos-26-intel`), windows-amd64 (`windows-latest`). Each
  desktop job builds natively so the existing smoke run (`clashpulse
  version`, bundled `mihomo -v`) executes on the target architecture.
- A `windows-11-arm` job downloads the windows-amd64 artifact and runs the
  same smoke checks (`clashpulse.exe version`, bundled `mihomo.exe -v`)
  under x64 emulation. It proves the binaries start, not that the GUI opens.
- macOS and Windows jobs: read `packaging/mihomo.lock`, download the asset,
  verify its SHA-256, extract, hash the executable, place it in the package,
  pass `Version` and the executable hash via `-ldflags -X`, add
  `licenses/mihomo-LICENSE.txt` (Mihomo's GPL-3.0 text from the source
  archive) and `licenses/mihomo-PROVENANCE.txt` (version, asset name, archive
  and executable SHA-256, upstream URL), then run `mihomo -t` on a generated
  fixture config with the bundled binary.
- The publish job attaches the verified Mihomo source archive as
  `mihomo-v1.19.34-source.tar.gz`, satisfying GPL-3.0 section 6 by shipping
  the corresponding source next to the binaries. Release notes state that the
  macOS and Windows packages include Mihomo v1.19.34 (GPL-3.0) and where the
  source is.
- `SHA256SUMS` covers every asset, including the source archive.

ClashPulse stays Apache-2.0; Mihomo is a separate program shipped alongside
it (aggregate), not linked.

## Homebrew tap: `fishman/homebrew-clashpulse`

- `Formula/clashpulse.rb`: `url` = the ClashPulse tag archive with its
  SHA-256 (same archive as AUR), `license "Apache-2.0"`, `depends_on "go" =>
  :build`, `depends_on "mihomo"`, `depends_on :macos`. Builds with
  `go build -trimpath -ldflags "-X main.version=clashpulse v<version>"` into
  `bin/clashpulse`. No bundle variables, so the default stays `system` and
  Homebrew's gvisor-enabled `mihomo` is used. `test do` runs
  `clashpulse version` and asserts the version string.
- `.github/workflows/test.yml` in the tap: on push and pull request, run
  `brew install --build-from-source ./Formula/clashpulse.rb`, `brew test
  clashpulse`, and `brew audit --strict --formula clashpulse` on `macos-latest`
  and `macos-26-intel`.
- Release process: after a ClashPulse tag is published, update `url` and
  `sha256` in the tap (documented in a new `RELEASING.md` that also records
  the existing AUR update steps, which are undocumented today). No token-based automatic push.

## Docs

- `docs/faq.md` "Why is Mihomo not bundled?" becomes "Which Mihomo does
  ClashPulse use?": bundled on macOS/Windows release packages, system on
  Linux and Homebrew, how to switch, how updates happen (with a ClashPulse
  release only).
- `docs/dependencies.md`: replace "no core binary is bundled" with the pinned
  version, the lock file, and the GPL source asset.
- `docs/installation.md`: Homebrew instructions and the four desktop
  archives; zh-CN mirrors.
- `THIRD_PARTY_LICENSES.txt` is for Go modules; Mihomo's license ships in
  `licenses/` inside the packages instead.

## Testing

- `internal/bundled` + `config`: default is `bundled` when `SHA256` is set
  and `system` when empty; an explicit `system` stays `system`.
- Seed then load: a fresh config directory seeded from `examples/` resolves
  to `bundled` when `SHA256` is set and to `system` when empty.
- Homebrew `test do` runs against a fresh temporary config directory and
  asserts the resolved binary is `system`, not only `clashpulse version`.
- `mihomo` bundled resolution with a fake executable in a temp package
  layout: hash match installs a private copy with 0700 dir/file and inspects
  it; mismatch errors and leaves no copy; missing file errors; an existing
  matching copy is reused without rewriting; a tampered existing copy is
  replaced.
- `app`: switching to `bundled` on a build without a bundle keeps the prior
  binary and config (existing transactional switch rule).
- Release workflow: lock parsing and checksum verification as a script with
  a test over a fixture lock and fake assets; the desktop jobs fail when any
  checksum mismatches (checked once by editing a digest on a throwaway
  branch).
- Homebrew: the tap workflow above.
