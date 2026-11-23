---
layout: default
title: Dependencies
nav_order: 9
---

# Dependencies and desktop builds

`go.mod` pins all direct modules, including Fyne v2.8.2 (released; [upstream](https://github.com/fyne-io/fyne/tree/v2.8.2), BSD-3-Clause), `tcell/v3` v3.6.0, `lipgloss/v2` v2.1.1, and notmutt `lib/xdg` and `lib/localipc` v0.1.0 plus `lib/tui` v0.1.3 (Apache-2.0; independently tagged `lib/tui/v0.1.3`). Go 1.26.6 is required. Mihomo is a separate executable, not a Go dependency.

The macOS (arm64, amd64) and Windows (amd64) release packages bundle the official Mihomo release build pinned in `packaging/mihomo.lock` (currently v1.19.34; GPL-3.0; upstream builds include the gvisor stack). The lock records the SHA-256 of each upstream archive and of the source tag archive; `tools/fetch-mihomo` downloads a pinned asset, rejects any mismatch, and reports the executable digest the build embeds through `internal/bundled`. Mihomo publishes no checksums, so the lock digests were computed once from the upstream assets. Packages carry Mihomo's licence (`packaging/mihomo/LICENSE`) and a provenance file in `licenses/`; each release attaches `mihomo-<version>-source.tar.gz`. ClashPulse and Mihomo are separate programs shipped together; nothing links them. A pin changes only in a reviewed commit that updates every digest; nothing updates it automatically. Windows arm64 is not built: the `windows-11-arm` runner has no aarch64 mingw compiler for Fyne's cgo code.

A `replace` directive builds Fyne from [fishing-agents/fyne](https://github.com/fishing-agents/fyne/tree/fix/wayland-visible-create) `v2.8.2-0.20261001232054-d7b412429937` (BSD-3-Clause): v2.8.1 plus one commit that keeps Wayland windows visible at creation except the systray monitor. Neither upstream v2.8.2 nor the fork's default branch has that fix, so the build lacks upstream's v2.8.2 changes until the fix is rebased.

The TUI module provides shared theme resolution, table geometry, tab strip, and status layout; its only direct dependency is `github.com/mattn/go-runewidth` v0.0.31 (MIT); `github.com/clipperhouse/uax29/v2` stays at v2.7.0. Windows named pipes use [Microsoft/go-winio](https://github.com/Microsoft/go-winio) v0.6.4 (MIT) for `DialPipeContext`, secured `ListenPipe`, and first-instance/remote-rejection behavior; it adds `github.com/sirupsen/logrus` v1.10.2 (MIT) to the graph.

Inspect the module graph with `go list -m all` and its edges with `go mod graph`. `THIRD_PARTY_LICENSES.txt` has the license text and notices for every selected module, including Fyne's bundled fonts. The module cache is not vendored. Any pin change needs a license/security review, graph comparison, and target build checks; remove unused dependencies rather than silently changing their versions. The 2026-11-17 upgrade of every module to its latest release was reviewed this way: no license changed, `github.com/ebitengine/purego` v0.11.3 (Apache-2.0, macOS notifications in beeep) and graph-only `github.com/HugoSmits86/nativewebp` v1.3.0 (MIT) were added, and `github.com/jackmordaunt/icns/v3` was removed.

Fyne's native driver needs CGO and platform graphics libraries. Linux needs X11/OpenGL development headers (Mesa, Xcursor, Xrandr, Xinerama, Xi, Xxf86vm) plus Wayland and xkbcommon headers (Debian: `libwayland-dev`, `libxkbcommon-dev`, `wayland-protocols`), because `go-gl/glfw` v3.4 builds both backends when no `x11` or `wayland` build tag is given. macOS needs Xcode Command Line Tools; Windows needs MinGW/GCC.

Linux release builds run on Ubuntu 22.04 with `CGO_CFLAGS=-march=x86-64`, so the binary needs only baseline x86-64 and glibc 2.35; CI fails if either floor rises. Build natively on each target; `CGO_ENABLED=0` cross-compilation of the `ci`-tagged software renderer is not a release build. See Fyne's [cross-compilation](https://docs.fyne.io/started/cross-compiling/) and [packaging](https://docs.fyne.io/started/packaging/) guides. `.github/workflows/build.yml` runs tagged tests/vet and native builds on Linux, macOS, and Windows and uploads per-platform artifacts, not signed releases.

## Localization

Catalogs are embedded TOML loaded with `github.com/nicksnyder/go-i18n/v2` v2.7.0 (MIT), which Fyne also requires; `Bundle.LoadMessageFileFS` reads them from the binary, never at runtime from disk. `github.com/jeandeaual/go-locale` v0.0.0-20250612000132-0ef82f21eade (MIT) detects the system locale. `golang.org/x/text` v0.44.0 and `github.com/BurntSushi/toml` v1.6.0 were already in the graph and are now direct imports; no new version or transitive package came with them.

## Optional managed data

`examples/resources.toml` seeds four MetaCubeX/meta-rules-dat sources enabled by default: `geoip.dat`, `geosite.dat`, `Country.mmdb`, and the domain-format `cn.mrs` rule-set. They download during validated profile refresh/activation or an explicit resource refresh; `download subscription` stays profile-only. Downloading `cn.mrs` adds no filter or DNS route. No source bytes are bundled. The previous disabled seed is migrated once; customized resource files are never replaced. Each source can be disabled or replaced.

[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) publishes and updates these release assets (repository license: GPL-3.0). Recorded upstream inputs include [Loyalsoldier/geoip](https://github.com/Loyalsoldier/geoip) (CC-BY-SA-4.0), [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community) (MIT), and [blackmatrix7/ios_rule_script](https://github.com/blackmatrix7/ios_rule_script) (GPL-2.0). These provenance and license identifiers do not establish that composite data has one uniform license or grant downstream redistribution rights; review the actual data and upstream terms before redistributing it.

The enabled `releases/download/latest` URLs are mutable and have no static SHA-256 pin. Hardcoding the current release digest against a moving URL would break future updates unless the URL is frozen too. If you need a fixed digest, select a versioned asset and pin its matching digest.

The `cn` rule-set URL comes from [MetaCubeX's documented Mihomo rule-set example](https://github.com/MetaCubeX/meta-rules-dat/blob/master/README.md). Its `meta` branch is mutable and unpinned; the upstream README says `geosite:cn` incorporates [blackmatrix7/ios_rule_script's ChinaMax domain list](https://github.com/blackmatrix7/ios_rule_script/tree/master/rule/Clash/ChinaMax). The default downloads only the declared MRS bytes; a `RULE-SET` filter needs a target from the active profile, or a DNS route needs an explicit resolver set. Neither is enabled by the seed.
