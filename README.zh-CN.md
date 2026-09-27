# ClashPulse

[English](README.md) | 简体中文

ClashPulse 是一个轻量的跨平台 Go 桌面客户端，面向 [Mihomo](https://github.com/MetaCubeX/mihomo)：单个二进制，空闲低 CPU，没有轮询与空转循环；配置是严格类型的 TOML，按用途拆成四个浅表文件，手工编辑后自动校验重载；节点延迟持续测量，只在更优节点被证明更好时才自动切换，并且每个阈值都由用户控制。它不是 Clash Verge Rev 的功能复刻，也不是通用代理面板。

## 延迟测量与自动切换

Mihomo 仍是数据面与配置权威。ClashPulse 通过 Mihomo 的控制器延迟接口测量，而不是直接发 HTTP：直接请求测的是错误的路径。

- 每次采样记录代理、分组、测试 URL 标识、完成时间、延迟与结果 (`success`、`timeout`、`error`)。`0` 或错误不是有效的延迟值。
- 切换需要同时满足两条：当前节点不健康或连续多次超过阈值，且存在明显更优的候选。使用中位数等鲁棒窗口指标，绝不采信单次快样本。
- 每个分组同一时刻只允许一次切换，切换后进入冷却；全部候选失败时触发熔断。不会在两个节点之间来回震荡。
- 切换会给出确切原因、原节点、新节点与实测证据。
- 托管 `select` 分组仅在用户显式开启后才自动选点。手动选择会关闭该分组的自动化，直到用户重新启用。

默认测试 URL 是 `http://cp.cloudflare.com/generate_204`。明文 HTTP 可能被劫持；延迟是路由信号，不是完整性校验。该值可在设置中修改。

## 构建

需要 Go 1.26.6、C 编译器，以及 Fyne 的原生图形头文件。Debian/Ubuntu：

```sh
sudo apt-get update
sudo apt-get install -y build-essential pkg-config libgl1-mesa-dev xorg-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev
go build -o clashpulse ./cmd/clashpulse
./clashpulse version
```

源码构建报告 `clashpulse dev`。macOS 需要 Xcode Command Line Tools；Windows 需要 MinGW/GCC 与 `go build -o clashpulse.exe ./cmd/clashpulse`。各目标平台的细节见 [桌面构建依赖](docs/dependencies.md)。

`make install` 会把二进制、图标与 desktop 条目复制到 `~/.local`(`PREFIX=/usr/local` 等可覆盖前缀)。desktop 条目仅限 Linux。

GUI 启动时不带代理配置。Mihomo 是独立可执行文件：把它放到 `PATH`，或在设置中选择一个绝对路径。本仓库不捆绑 Mihomo 二进制。

## 运行

```sh
./clashpulse
```

GUI 需要图形桌面会话(Linux 上为 X11 或 Wayland)。托盘使用 StatusNotifier，GNOME 可能需要 [AppIndicator 扩展](https://extensions.gnome.org/extension/615/appindicator-support/)。

每台机器只有一个 ClashPulse 服务和一个 ClashPulse 窗口。若已有服务在运行(由 GUI、`clashpulse tui` 或 `clashpulse activate` 启动)，`./clashpulse` 会让窗口连上该服务，而不是再起一个；若窗口已在运行，`./clashpulse` 会报告并退出，不再开第二个。窗口是 IPC 客户端，因此关闭它不会停止不是它启动的服务。终端客户端不受影响：任意数量的 `clashpulse tui` 会话都可以接入。

在 GUI 中：添加 HTTPS 订阅，刷新以获取并校验候选配置，然后激活。再开一个终端运行 `./clashpulse tui`，它连接到正在运行的桌面服务，自身不启动服务。Windows 上使用 `.\clashpulse.exe` 与 `.\clashpulse.exe tui`。

GUI、TUI 与 CLI 在操作系统区域设置为 `zh-CN`(含 `zh_CN.UTF-8`、`zh-Hans-CN`)时使用简体中文界面，其他区域保持英文。翻译目录嵌入在二进制中，不从配置或状态目录加载。机器 ID、代理名称、来源 URL 与凭据永不翻译。中文字形渲染依赖系统已安装字体；ClashPulse 不捆绑也不指定 CJK 字体。

不带 GUI 运行本地配置：

```sh
./clashpulse activate 'my profile.yaml'
```

该命令只读取文件一次，用所选 Mihomo 校验，并且仅在控制器就绪后才打印 `local profile active; press Ctrl-C to stop`。它占用同一个本地 IPC 服务，因此运行期间 TUI 可以接入。Ctrl-C 会停止 Mihomo 并把系统代理重置为桌面默认值。源文件从不被编辑，也不会作为订阅导入；其路径与代理凭据不会通过 IPC 暴露。生成的私有配置在关闭时删除。激活失败会打印固定的、不含凭据的阶段信息并以非零码退出。

GUI 与 TUI 的 Overview 会显示当前配置的 **Generated config changes**。每条记录说明一个由应用管理的字段、它是被添加、替换还是移除，以及固定原因。该报告不是完整的 YAML diff：不含源值或生成值、凭据、网络端点或 URL。未变更的字段会被省略；服务停止时不显示覆盖报告。

直接运行命令前请先关闭桌面端：它们使用同一把私有状态锁，会拒绝并发访问服务。

```sh
./clashpulse download subscription [id]
./clashpulse refresh
./clashpulse refresh resource [id]
```

`download subscription` 抓取并解析配置(要求 `proxies` 或 `proxy-providers`)，然后存为未激活的私有快照，不检查也不运行 Mihomo。激活会渲染配置、用所选 Mihomo 校验完整生成结果，并以事务方式应用。`refresh resource [id]` 在没有激活配置时也可用：把启用的资源以稳定文件名缓存到私有状态目录的 `resources/` 下，不启动 Mihomo。当配置已激活时，Mihomo 会先停止，校验通过后才重启；更新失败会恢复此前的文件与运行时。带 ETag 或 Last-Modified 的远程源使用条件请求，因此未变化的 `304` 不会下载响应体，也不会替换文件。服务端没有校验器时，必须抓取响应体才能判断是否变化。之后的配置刷新或激活仍会在应用前校验生成配置。不带 ID 时，命令处理作用域内所有启用的源。

加上 `--show-response` 可在失败时把最多 4 KiB 的可打印 HTTP 响应文本输出到本地 stderr。URL 与响应体默认隐藏。

在 TUI 视图中按 `?` 查看键位帮助(编辑器内为 `F1`)，按 `~` 查看并滚动本次会话的活动；`q` 关闭任一面板但不退出。GUI 中使用 Overview > View activity。两种活动视图都只显示经过脱敏的 IPC 诊断，不含订阅 URL 或凭据。

设置包含 Mihomo 二进制、系统代理、监控与 DNS 四节。当侧栏与选中分节无法并排放下时，侧栏会变成下拉框。

托盘菜单中的 Proxies 选择已激活的托管 `select` 分组的成员；System Proxy 项切换请求的操作系统设置。本地 IPC 服务重连后控件恢复。

首次启动会在平台配置目录(Linux 上为 `~/.config/clashpulse`，除非设置了 `$XDG_CONFIG_HOME`)生成 `config.toml`、`subscriptions.toml`、`resources.toml` 与 `filters.toml`。四个经过审阅的数据源默认启用；此前确切的一组禁用资源种子会迁移一次，而用户改过的资源文件保持不动。资源在校验通过的配置刷新/激活或 `refresh resource` 时下载；`download subscription` 只处理配置。`cn` 列表会被下载，但在配置前不参与路由。订阅与过滤器示例保持注释状态。

## 架构

```text
cmd/clashpulse -> app -> core -> mihomo
                       -> filters
                       -> monitor
                       -> ipc
                       -> ui
                       -> tui
```

`app` 是唯一的生命周期持有者，通过 `ipc` 向 GUI 与 TUI 暴露同一套带类型的命令面。`ui` 与 `tui` 从不直接导入或持有 `core`、`monitor`、`mihomo`。

## 工作约定

- 常规提交：`type(scope): imperative lowercase subject`。
- 代码与项目散文使用 ASCII，注释只解释非显然的约束、安全边界或权衡。
- 新增依赖需审阅：上游活跃、许可证与来源清晰、用途单一、版本精确、传递依赖与安全审查通过，并接受移除或升级方案。优先标准库与已有依赖。
- 未经用户明确批准，不添加遥测、崩溃上报、云后端、自动依赖更新或订阅分享。
