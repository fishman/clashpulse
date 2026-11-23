---
layout: default
title: 依赖
parent: 简体中文
nav_order: 9
---

# 依赖与桌面构建

`go.mod` 固定了所有直接依赖的模块，包括 Fyne v2.8.2（正式版；[上游](https://github.com/fyne-io/fyne/tree/v2.8.2)，BSD-3-Clause）、`tcell/v3` v3.6.0、`lipgloss/v2` v2.1.1，以及 notmutt 的 `lib/xdg` 和 `lib/localipc` v0.1.0 与 `lib/tui` v0.1.3（Apache-2.0；单独打标签 `lib/tui/v0.1.3`）。需要 Go 1.26.6。Mihomo 是单独的可执行文件，不是 Go 依赖。

macOS（arm64、amd64）和 Windows（amd64）发布包内置 `packaging/mihomo.lock` 固定的官方 Mihomo 发布构建（目前 v1.19.34；GPL-3.0；上游构建包含 gvisor 栈）。锁文件记录每个上游压缩包和源码标签压缩包的 SHA-256；`tools/fetch-mihomo` 下载固定的文件，任何不匹配都会失败，并给出构建通过 `internal/bundled` 嵌入的可执行文件摘要。Mihomo 不发布校验和，锁文件中的摘要是从上游文件计算一次得到的。发布包的 `licenses/` 里有 Mihomo 的许可证（`packaging/mihomo/LICENSE`）和来源说明；每个发布版附带 `mihomo-<version>-source.tar.gz`。ClashPulse 和 Mihomo 是一起分发的两个独立程序，没有链接关系。固定版本只在更新全部摘要的审阅提交中改变，不会自动更新。没有 Windows arm64 构建：`windows-11-arm` 运行器没有可编译 Fyne cgo 代码的 aarch64 mingw 编译器。

`replace` 指令让 Fyne 从 [fishing-agents/fyne](https://github.com/fishing-agents/fyne/tree/fix/wayland-visible-create) `v2.8.2-0.20261001232054-d7b412429937`（BSD-3-Clause）构建：即 v2.8.1 加一个提交，让 Wayland 窗口在创建时保持可见（systray 监视器除外）。上游 v2.8.2 和该 fork 的默认分支都没有这个修复，所以在修复变基之前，构建不包含上游 v2.8.2 的改动。

TUI 模块提供共享的主题解析、表格布局、标签栏和状态栏；它唯一的直接依赖是 `github.com/mattn/go-runewidth` v0.0.31（MIT）；`github.com/clipperhouse/uax29/v2` 保持 v2.7.0。Windows 命名管道使用 [Microsoft/go-winio](https://github.com/Microsoft/go-winio) v0.6.4（MIT）提供的 `DialPipeContext`、带安全描述符的 `ListenPipe`，以及首实例与拒绝远程连接的行为；它给依赖图带来 `github.com/sirupsen/logrus` v1.10.2（MIT）。

用 `go list -m all` 查看模块图，用 `go mod graph` 查看依赖边。`THIRD_PARTY_LICENSES.txt` 包含每个选中模块的许可证文本和声明，包括 Fyne 自带的字体。模块缓存不 vendor。任何版本变动都需要重新做许可证与安全审查、依赖图比对和目标平台构建检查；不用的依赖应删除，而不是悄悄改版本。2026-11-17 把所有模块升级到最新版时按此审查：许可证没有变化，新增 `github.com/ebitengine/purego` v0.11.3（Apache-2.0，beeep 的 macOS 通知）和仅出现在依赖图中的 `github.com/HugoSmits86/nativewebp` v1.3.0（MIT），移除了 `github.com/jackmordaunt/icns/v3`。

Fyne 的原生驱动需要 CGO 和平台图形库。Linux 需要 X11/OpenGL 开发头文件（Mesa、Xcursor、Xrandr、Xinerama、Xi、Xxf86vm）以及 Wayland 和 xkbcommon 头文件（Debian：`libwayland-dev`、`libxkbcommon-dev`、`wayland-protocols`），因为没有 `x11` 或 `wayland` 构建标签时，`go-gl/glfw` v3.4 会同时构建两个后端。macOS 需要 Xcode Command Line Tools；Windows 需要 MinGW/GCC。

Linux 发布版在 Ubuntu 22.04 上用 `CGO_CFLAGS=-march=x86-64` 构建，因此只需要基础 x86-64 和 glibc 2.35；任一下限提高，CI 就会失败。每个平台都要在本机构建；`CGO_ENABLED=0` 交叉编译 `ci` 标签的软件渲染器不算发布构建。参见 Fyne 的[交叉编译](https://docs.fyne.io/started/cross-compiling/)和[打包](https://docs.fyne.io/started/packaging/)指南。`.github/workflows/build.yml` 在 Linux、macOS 和 Windows 上运行带标签的测试、vet 和原生构建，上传各平台的构建产物，不是签名发布版。

## 本地化

翻译目录是嵌入的 TOML，由 `github.com/nicksnyder/go-i18n/v2` v2.7.0（MIT，Fyne 也依赖它）加载；`Bundle.LoadMessageFileFS` 从二进制中读取，运行时不读磁盘。`github.com/jeandeaual/go-locale` v0.0.0-20250612000132-0ef82f21eade（MIT）检测系统区域设置。`golang.org/x/text` v0.44.0 和 `github.com/BurntSushi/toml` v1.6.0 原本就在依赖图里，现在改为直接导入，没有引入新版本或新的传递依赖。

## 可选的托管数据

`examples/resources.toml` 默认启用四个 MetaCubeX/meta-rules-dat 数据源：`geoip.dat`、`geosite.dat`、`Country.mmdb` 和 domain 格式的 `cn.mrs` 规则集。它们在校验通过的配置刷新/启用或手动刷新资源时下载；`download subscription` 只处理订阅配置。下载 `cn.mrs` 不会添加过滤列表或 DNS 路由。不捆绑任何源数据。之前那组禁用的种子只迁移一次；改过的资源文件从不被替换。每个数据源都可以停用或替换。

[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) 发布并更新这些文件（仓库许可证：GPL-3.0）。记录的上游输入包括 [Loyalsoldier/geoip](https://github.com/Loyalsoldier/geoip)（CC-BY-SA-4.0）、[v2fly/domain-list-community](https://github.com/v2fly/domain-list-community)（MIT）和 [blackmatrix7/ios_rule_script](https://github.com/blackmatrix7/ios_rule_script)（GPL-2.0）。这些来源和许可证标识并不说明合成数据只有一种统一许可证，也不授予再分发权；再分发前请检查实际数据和上游条款。

默认启用的 `releases/download/latest` 地址会变，也没有固定的 SHA-256。对着会变的地址写死当前版本的摘要，除非同时固定地址，否则以后的更新会失败。需要固定摘要时，请选择带版本号的文件并固定对应摘要。

`cn` 规则集的地址来自 [MetaCubeX 文档中的 Mihomo 规则集示例](https://github.com/MetaCubeX/meta-rules-dat/blob/master/README.md)。它的 `meta` 分支会变且未固定；上游 README 说明 `geosite:cn` 包含 [blackmatrix7/ios_rule_script 的 ChinaMax 域名列表](https://github.com/blackmatrix7/ios_rule_script/tree/master/rule/Clash/ChinaMax)。默认只下载声明的 MRS 文件；`RULE-SET` 过滤列表需要当前配置中的目标，DNS 路由需要明确的解析器组。默认种子两者都不启用。
