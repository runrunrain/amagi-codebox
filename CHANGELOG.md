# Changelog

本项目所有值得记录的变更都会维护在此文档中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)，版本章节沿用仓库现有 Git 标签。

## [Unreleased]

## [1.3.18] - 2026-08-14

### Added

- 模型下拉目录合并内置提供商（如 openai-codex 等 OAuth 登录提供商）：pi 侧合并 models-store.json 内置模型目录缓存，omp 侧通过 `omp models ls --json`（5 秒超时，CLI 不可用时静默降级为仅注册表）拉取内置模型；与注册表重名时自定义条目优先。
- provider 下拉新增来源标注：已认证 ✓、内置目录「（内置）」（凭据由 CLI 自身管理）、未认证注册表「（未认证）」；openai-codex 的 OAuth 状态由 auth.json 正确识别为已认证。
- 附带封闭式回归测试：内置目录合并/重名覆盖/认证状态标注（builtin_catalog_test.go ×2）。

## [1.3.17] - 2026-08-14

### Added

- Pi 引擎新增「认证登录」子标签：可视化管理 auth.json 提供商凭据——API Key 条目密文可编辑（留空移除字段），OAuth 条目只读展示登录状态（accountId / 过期时间，token 不展示且保存时原样保留），未知类型与额外字段走递归可视化编辑；支持添加 API Key 认证并一键填入注册表中未认证的提供商名。
- 模型目录（pi/omp）新增 hasAuth 认证状态标注：Agent 配置的 provider 下拉以「✓ /（未认证）」标明凭据状态，避免选到无凭据模型。pi 的凭据来源 = auth.json 条目或 models.json 内联 apiKey；omp 凭据内联在 models.yml（apiKey / auth / authHeader），已在注册表编辑器中可编辑。
- 后端 piconfig 新增 auth.json 读写 API（校验 + 原子写入 0600），并附带封闭式回归测试（auth_config_test.go，含目录不泄露凭据内容的断言）。

## [1.3.16] - 2026-08-14

### Changed

- 模型提供商注册表编辑器实现「可视化完全可视化」：移除全部 JSON 兜底文本框，可视化模式下所有字段均用结构化控件编辑。
- 高级字段专用可视化编辑器：thinkingLevelMap 用「输入级别 → 输出级别」行编辑器（标准级别下拉 + null 支持）；thinking 拆分为 mode 输入与 levels 列表编辑；input 用能力列表编辑；cost 用输入/输出/缓存读/缓存写四项数字编辑（全空自动移除字段）。
- 新增 VisualValueEditor 递归可视化值编辑器：按类型分发字符串/数字/布尔/字符串列表/通用数组/嵌套对象，未知字段（auth、compat 等）全程可视化编辑，类型在写回时保持；provider 级其他字段、模型级未知字段与顶层其他键均支持可视化增删改。

## [1.3.15] - 2026-08-14

### Added

- Provider Center 的 Pi / OMP 引擎标签新增三级子标签「Agent 配置 | 模型提供商」，模型提供商注册表（models.json / models.yml）接入可视化编辑。
- 新增 ProviderRegistryEditor 共享组件：每个提供商一张折叠卡片，支持 api 协议下拉（五种已知协议，未知值回退文本输入）、baseUrl、apiKey 密文编辑（留空即移除字段）、模型条目增删改（id / 显示名 / contextWindow / maxTokens / 推理开关）。
- thinkingLevelMap、thinking、cost、compat、auth 等高级字段通过 JSON 兜底编辑器修改并原样保留；顶层未知键同样保留；amagi-* 前缀提供商显示「由提供商中心同步管理」提示。
- 后端 piconfig / ompconfig 新增注册表全文读写 API（校验 + 原子写入 0600），并附带封闭式回归测试（models_config_test.go）。

## [1.3.14] - 2026-08-14

### Added

- Provider Center 新增 Pi（amagi-pi）与 OMP（oh-my-pi）可视化配置入口：在「预设」页新增 Pi / OMP 引擎标签，支持对 amagi.json 与 config.yml 进行可视化/源码双模式编辑。
- Pi 配置可视化：profile 分层策略选择、各 agent 角色的模型分配、MCP 路由（默认服务器 + 角色附加服务器）；模型通过 provider → model → thinking level 三级下拉关联，数据来自 models.json 注册表，避免手写 `provider/model:level` spec 出错。
- OMP 配置可视化：modelRoles 角色模型绑定、task.agentModelOverrides 子代理覆盖（支持 `@role` 引用与直接模型 spec 两种形态并可切换），模型下拉数据来自 models.yml 注册表；其余配置键原样保留。
- 后端新增 piconfig / ompconfig 服务：原子写入（临时文件 + rename，0600）、保存前 JSON/YAML 校验、从模型注册表抽取不含密钥的只读目录供下拉使用。

## [1.3.13] - 2026-08-14

### Added

- Provider Center 新增 OpenCode、Pi 与 Oh My Pi 提供商统一同步：仅接管各配置文件中的 `amagi-*` 命名空间，同步模型、参数与凭据，同时保留用户自有 Provider 和登录认证数据。
- 远程启动器新增每次会话的工作目录、服务提供商、终端预设、模型、Shell 与 Claude Headroom 设置，并通过不含密钥的安全契约传递。

### Changed

- 终端预设改为按 Anthropic、OpenAI 协议格式共享；Claude Code 使用 Anthropic 预设，Codex、Pi 与 Oh My Pi 共用 OpenAI 预设，历史 CLI 专属预设会无损迁移。

### Fixed

- 修复环境检测未查询 Pi 与 Oh My Pi 最新 npm 版本、导致存在新版本时仍显示“已安装”而不是“有更新”的问题。
- 修复远程 Web 将 CLI 可用性错误绑定到“最近一次桌面启动配置”、导致 OpenCode、Codex、Oh My Pi 等已安装终端仍无法启动的问题。
- 修复远程会话停止或进程退出后卡片不会自动清理的问题；大厅会过滤终止状态并定时刷新。

### Removed

- 移除已失去实际用途的工作区管理功能，包括桌面端工作区面板、项目级与全局插件部署、冲突检测、工作区持久化、完整配置中的工作区快照以及对应 Wails 绑定。
- 历史完整配置中的 `portable.workspaces` 字段继续兼容读取但会被忽略；现有工作区清单和部署产物不会被应用自动删除。
- 移除 Prompt 注入代理功能，包括注入规则页、会话代理开关、代理服务、实时代理用量来源、远程契约字段、持久化与 Wails 绑定。
- 历史完整配置中的 `portable.proxy` 字段继续兼容读取但会被忽略；现有注入规则与代理 URL 历史文件不会被应用自动删除。

## [1.3.12] - 2026-08-14

### Added

- 新增遵循 Keep a Changelog 与 Semantic Versioning 的项目变更日志，并接入 README 和发布流程文档。

### Fixed

- 修复 macOS 上“导出完整配置”和“导入完整配置”按钮点击无反应的问题：改用应用内确认弹窗，不再依赖 WebKit 环境中不可靠的浏览器原生确认框。
- 修复 macOS 钥匙串加载缓慢时完整配置操作被内部锁永久阻塞的问题；完整导出会优先打开保存对话框，并在密钥尚未就绪时给出明确提示，避免生成缺失密钥的“完整”配置。
- 完整配置导入、导出操作增加执行中状态、防重复触发和可读错误提示。

## [1.3.11] - 2026-08-13

### Added

- 应用启动后自动检查新版本，并提供全局更新提醒和软件更新页入口。
- Windows 自动更新增加独立更新助手，在主进程退出后替换被锁定的可执行文件，并在失败时回滚。

### Changed

- 更新检查与下载支持瞬时网络错误、GitHub 限流和服务端错误重试。
- 平台安装包尚未上传完成时，仍会报告新版本并提供 Release 下载页。

### Fixed

- 修复未注入构建版本时，界面版本与更新服务当前版本不一致的问题。
- 修复下载响应被截断但未返回读取错误时可能应用不完整更新包的问题。
- 升级 npm 依赖以修复已知安全告警。

## [1.3.10] - 2026-08-13

### Added

- 新增完整配置导出与导入，可迁移服务提供商、密钥、预设、应用设置、路径、环境变量、工作区、代理规则、价格表和 OpenCode 全局配置。
- 完整配置导入采用整体替换语义，并在失败时尽力回滚原配置。
- 新增 Codex 配置可移植性支持。

### Changed

- 清理内置服务提供商和终端预设，新安装使用干净的初始环境。
- 完善远程会话的配置与状态闭环。

## [1.3.09] - 2026-08-10

### Changed

- 统一桌面端与远程端的真实会话模型、生命周期和控制权限。
- 增强远程会话的启动规划、进程身份校验、断线恢复与清理补偿机制。

### Fixed

- 修复 OMP 启动测试在不同运行环境下不稳定的问题。
- 修复远程会话状态、PTY 就绪和连续性处理中的一致性问题。

## [1.3.08] - 2026-08-09

### Fixed

- 修复 Pi 终端多行输入处理。
- 修复终端刷新时视图意外跳回顶部的问题。

## [1.3.07] - 2026-08-09

### Fixed

- 修复 OMP 自定义服务提供商配置未正确写入 `models.yml` 的问题。

## [1.3.06] - 2026-08-07

### Added

- Provider Center 新增 OMP 预设管理。
- 扩展管理新增 OMP 插件面板，支持插件列表、安装、更新和卸载。

## [1.3.05] - 2026-08-07

### Added

- 集成 Oh My Pi（OMP），支持会话启动、`models.yml` 配置、环境检测与安装。
- 新增 OMP 用量解析与同步，并在桌面端和移动端展示 OMP 会话。

### Fixed

- 修复远程长会话连续性缓冲淘汰时可能误判故障的问题。

## [1.3.04] - 2026-08-06

### Fixed

- 修复用量仪表盘的聚合、读取和模型趋势展示问题。
- 修复 macOS PTY 与终端渲染稳定性问题。

## [1.3.03] - 2026-08-06

### Changed

- 增强终端渲染与 Provider 配置校验。

### Fixed

- 修复 OpenCode 插件更新成功后被误报为失败的问题。
- 修复 Codex、Claude 超长 JSONL 用量记录解析及历史游标迁移。

## [1.3.02] - 2026-08-06

### Added

- 完成远程控制基础、安全、会话工作区、连续性恢复和移动端体验。
- 新增远程配对、受信设备、单控制者仲裁和会话恢复能力。

## [1.3.01] - 2026-08-01

### Changed

- Pi 直接使用标准用户配置目录 `~/.pi/agent`，移除 CodeBox 隔离运行时副本。

## [1.3.00] - 2026-08-01

### Added

- 新增系统代理注入能力。

### Fixed

- 修复 Pi 兼容模式默认值。

[Unreleased]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.13...HEAD
[1.3.13]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.12...v1.3.13
[1.3.12]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.11...v1.3.12
[1.3.11]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.10...v1.3.11
[1.3.10]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.09...v1.3.10
[1.3.09]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.08...v1.3.09
[1.3.08]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.07...v1.3.08
[1.3.07]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.06...v1.3.07
[1.3.06]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.05...v1.3.06
[1.3.05]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.04...v1.3.05
[1.3.04]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.03...v1.3.04
[1.3.03]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.02...v1.3.03
[1.3.02]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.01...v1.3.02
[1.3.01]: https://github.com/runrunrain/amagi-codebox/compare/v1.3.00...v1.3.01
[1.3.00]: https://github.com/runrunrain/amagi-codebox/releases/tag/v1.3.00
