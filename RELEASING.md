# Releasing ClashPulse

## Tag a release

1. On a green `master`, update the version shown in `docs/installation.md`,
   `docs/zh-CN/installation.md`, and `docs/faq.md` if the release changes them.
2. Tag and push: `git tag -a vX.Y.Z -m 'ClashPulse vX.Y.Z' && git push origin vX.Y.Z`.
3. `.github/workflows/release.yml` builds Linux amd64, macOS arm64 and amd64,
   and Windows amd64, runs the Windows package on `windows-11-arm`, attaches
   `mihomo-<version>-source.tar.gz` for the bundled Mihomo, writes
   `SHA256SUMS`, and publishes a pre-release.

## Arch Linux (AUR)

The PKGBUILDs live in `packaging/aur/`. After the release is published:

1. `clashpulse`: set `pkgver`, reset `pkgrel=1`, and replace `sha256sums`
   with the digest of `https://github.com/fishman/clashpulse/archive/refs/tags/vX.Y.Z.tar.gz`.
2. `clashpulse-bin`: set `pkgver`, reset `pkgrel=1`, and replace both
   `sha256sums` with the digests of the release's linux-amd64 tarball (also in
   `SHA256SUMS`) and of `ui/clashpulse.svg` at the tag.
3. In each directory run `makepkg -si` to build and install it, then
   `makepkg --printsrcinfo > .SRCINFO` and push the directory to its AUR
   repository.
4. Commit the PKGBUILD changes here: `build(aur): update to vX.Y.Z`.

## Homebrew tap

The formula is `Formula/clashpulse.rb` in
[fishman/homebrew-clashpulse](https://github.com/fishman/homebrew-clashpulse).
After the release is published, set `url` to the new tag archive and `sha256`
to its digest (the same archive as the AUR `clashpulse` package), and push. The
tap's CI builds and tests the formula on Apple silicon and Intel macOS. Nothing
updates the tap automatically.

## Bumping the bundled Mihomo

The macOS and Windows packages ship the Mihomo release pinned in
`packaging/mihomo.lock`. To move the pin:

1. Read the upstream release notes for configuration or controller changes
   that affect ClashPulse.
2. Download the three platform assets and the source tag archive
   (`https://github.com/MetaCubeX/mihomo/archive/refs/tags/<version>.tar.gz`),
   compute each SHA-256, and update `version` and every digest in the lock.
3. Replace `packaging/mihomo/LICENSE` with `LICENSE` from that source archive.
4. Check each target: `go run ./tools/fetch-mihomo -target <target> -out /tmp/mihomo`.
5. Commit as `build(packaging): pin mihomo <version>`. CI's packaging smoke
   step validates a config with each bundled binary.
