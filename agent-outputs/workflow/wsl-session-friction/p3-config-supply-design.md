# WSL agent 配置供给体系：按归属同步，按目标生成

> P3-T1 · 设计态 · 方向级方案，尚未实施。
> 推荐混合模型：启动时校验实际消费配置，面板保存后标记变更并按需推送；用户文件以 WSL 本地修改优先，只有 Pi 登录凭据沿用 A2 的双向新陈判定。

## 一、背景与目标

WSL 会话消费的是 distro 内默认用户的配置，不是 Windows 面板正在编辑的那份配置。当前问题不是缺少一次复制，而是安装、启动、配置编辑和插件操作没有共用目标与归属规则。

本设计要使以下行为成立：

- 已安装、未重装的 WSL CLI 也能获得配置更新；安装不再是唯一供给入口。
- 用户知道哪些文件已同步、哪些保留 WSL 修改、哪些因 Windows 专属形态不能使用。
- CodeBox 托管配置可更新，用户自有配置不被整目录同步覆盖。
- Pi auth.json 延续批次二的双向规则；OpenCode 延续批次一的附加配置层。两者不被通用复制逻辑替代。
- 同步失败可以定位到 distro、CLI、文件和操作，但日志、状态页、计划记录不得携带密钥正文。

### 不变量与范围

1. 未登记或已被 WSL 用户修改的文件，不因 Windows 更新时间更晚而被自动覆盖。路径不存在也不等于获准复制整个目录。
2. 仅 Pi auth.json 使用 hash + mtime 双向同步；它是保护轮转凭据的既有例外，不是普通设置文件的默认算法。
3. `amagi-*` provider 命名空间和 OpenCode `amagi-codebox.json` 延续现有 CodeBox 所有权；命名空间外的用户字段必须保留。
4. 配置同步不复制会话历史，不迁移信任授权，不安装 CLI，不自动执行插件安装脚本。
5. B7 已关闭：不向 wslsetup 增加 omp；用户已手装 WSL omp 的配置供给亦不在本设计范围。保留现有 omp models.yml 启动写入，不新增其同步规则或面板入口。
6. 本轮唯一写入是本文；下文路径、接口、测试均是后续实施规格，不表示已经存在。

### 证据与当前基线

必读上游：

- `agent-outputs/wsl-session-friction/toolcall-evidence.md`：T1/T2/T3 首装整树播种与冻结，T5 用户配置不跨界，T7/T8 共享资产缺位与形态错误。
- `agent-outputs/wsl-session-friction/evidence-report.md`：A2、B5/B6、B7 等启动链边界。
- `agent-outputs/workflow/wsl-session-friction/p2t2-report.md`：尤其“修复轮”M-1、m-2、m-3；首轮单向复制描述已被修复轮替代。

已完整读取三份报告及三个必读源码：`internal/wslsetup/service_windows.go`、`internal/launcher/wsl_agent_config.go`、`internal/launcher/wsl_pi_login_sync.go`。补充读取/定向核对的证据：

| 事实 | 当前源码锚点 | 对设计的影响 |
|---|---|---|
| pi 首装执行 `ensurePiConfig`；AlreadyOK 分支不播种 | `internal/wslsetup/service_windows.go::InstallTool/ensurePiConfig` | 必须脱离安装时机，旧安装也可修复 |
| models.json 在 WSL merge；OpenCode 已写附加层并 chmod | `internal/launcher/wsl_agent_config.go::WriteWSLPiAgentConfig/WriteWSLOpenCodeAgentConfig` | T4 已修，不重新实现 provider 传输 |
| auth 内容相同不写；差异时 2 秒容差，并列取 distro | `internal/launcher/wsl_pi_login_sync.go::SyncWSLPiLoginAuth` | 独立策略适配，保留全部 outcome |
| PiConfig、PiPlugins 和 providerSync 绑定宿主目录 | `app.go:410–485`；`internal/piconfig/service.go:1–220`；`provider_harness_sync.go` | 单纯加复制函数不能解决面板目标歧义 |
| provider reconcile 严格解析，只替换 amagi-* | `internal/launcher/provider_sync.go::ReconcilePiAgentConfig/mergeProviderConfig` | 复用严格解析和命名空间规则，不整文件双向同步 |
| 插件登记在 packages，实体分 npm/git | `internal/piplugin/types.go`；`executor.go` | WSL 重放安装，不复制 node_modules |
| 视觉导出包含明文 api_key；学习层是模型级知识 | `internal/config/vision.go`、`modalities_store.go`；`docs/vision-export-contract.md` | 两份 JSON 不能视为同类普通资源 |
| 环境检测已有批量探测、30 秒 TTL、warning issue | `internal/envcheck/wsl_hybrid.go`；`frontend/src/views/settings/WSLCLISettings.vue:1–160` | 复用展示和缓存，不把状态查询变成同步 |
| 远程计划纯读，副作用由执行器承接 | `pi_login_launch.go:137–200`；`launch_executor.go:1–240`；`internal/launchplan/types.go` 定向检索 | 不在 BuildPlan 中探测 WSL、写文件或安装插件 |

**材料差异必须保留**：P2 报告修复轮记载 shell 用 `-nt`；当前 `pi_login_launch.go:199` 已用 `stat` 秒差 `>2`，说明工作区比报告更晚。当前 shell 片段仍是直接 `cp`，失败后 echo 并继续；Go 通道则有临时文件、rename、chmod 失败清理与 fail-closed 分类。不能将“Go 通道安全边界已成立”推导为“远程载荷同样已成立”。本设计将其列为统一接入前的安全验收门槛，不在本轮修改，也不抹去历史报告。

## 二、现状模型图（文字版）

```text
Windows CodeBox
  ├─ Config / Secrets → providerSync → Windows pi / omp / OpenCode 配置
  ├─ PiConfig 面板     → Windows amagi.json / models.json / auth.json
  ├─ PiPlugins 面板   → Windows settings.packages + npm/git 实体
  ├─ 视觉导出/学习层   → Windows ~/.agents/*.json
  └─ Windows ~/.agents/skills → 无 WSL 供给链

WSL 安装 pi 成功（仅新装分支）
  └─ ensurePiConfig → cp -r Windows ~/.pi/agent
       ├─ 排除 sessions 和一类 settings 备份
       └─ distro agent 目录已存在 → 永远跳过

WSL 启动
  ├─ pi API provider  → distro models.json merge + chmod
  ├─ pi 登录         → A2 auth 双向同步 + WSL agent root 重锚
  ├─ OpenCode        → distro amagi-codebox.json → OPENCODE_CONFIG 附加层
  ├─ Claude / Codex  → env / 参数，用户文件仍各自在本机
  └─ omp             → 现有 models.yml merge（本设计不扩展）
```

两份文件内容不同不一定是故障：WSL 用户可能刻意设置 Linux 命令、独立插件或不同工具路径。必须先判断谁拥有该文件，再判断是否同步。

## 三、D1：同步模型与时机

### 候选与推荐

| 候选 | 一致性与本地修改 | 成本 | 结论 |
|---|---|---|---|
| A. 全量 lazy：每次启动扫描所有文件、插件、skills | 能发现面板外编辑；启动期冲突集中暴露 | 大目录扫描、WSL 唤醒、插件安装会拉长启动 | 小文件适用；拒绝全树全量推广 |
| B. 保存事件驱动：每次保存推给 distro | 面板编辑反馈快；漏掉手改、应用关闭期间变更、停机 distro | 保存风暴、多 distro 唤醒；离线要补偿 | 不能单独保证会话正确 |
| C. 混合：关键文件 lazy，保存事件标脏并有限推送 | 启动补漏；事件缩短陈旧时间；统一冲突规则 | 多一个持久化版本/状态清单，但不需常驻守护进程 | **推荐** |

事件只表示“来源变了”，不赋予覆盖权。保存成功与 WSL 同步成功是两个结果：宿主保存成功后，distro 不可达不能回滚宿主保存，也不能显示“两侧已保存”。

### 归属策略

| 策略名 | 对象 | 更新规则 | mtime 的地位 |
|---|---|---|---|
| LocalOnly | 历史、信任、机器状态、未知文件 | 永不同步 | 不比较 |
| ImportOnce | 用户主动选择导入的配置/资产 | 目标缺失才导入；目标存在须预览确认 | 仅展示，不能授权覆盖 |
| TrackedProjection | 已同意持续同步的普通配置字段、选中 skills | 来源改变且目标仍等于上次写入结果才更新；WSL 手改即冻结 | 发现变化的辅助信息，不选胜者 |
| ManagedNamespace | pi amagi-* provider、已声明的托管文件 | 从 CodeBox 业务源生成；保留域外字段；按既有所有权更新 | 不决定方向 |
| A2Auth | Pi auth.json | 沿用 A2 双向 hash + mtime | 唯一自动“双向新者为准”对象 |

这里的“投影”是将 Windows 配置转换为该 distro 可消费的内容；记录源 hash 和转换后目标 hash，不能要求两个文件字节相同。普通配置不自动反向同步，否则 Linux 路径会反写 Windows，且会把 WSL 局部选择传播到其它 distro。

### 普通配置的冲突判定

以最小登记单元处理：小型专用文件可按文件；混合用户文件按明确字段，如一个 MCP server ID；skills 按一个 skill 目录。

- 无记录、目标已存在：标记 `unmanaged-existing`，保留原样；即使与来源相同，也须用户确认“纳入持续同步”才登记所有权。
- 无记录、目标缺失：只有明确允许的默认托管产物可自动创建；用户配置、可执行资产需导入同意。
- 有记录、目标等于上次写入 hash：来源未变则跳过；来源改变则转换、校验、更新。
- 有记录、目标变化或被删除：视为本地修改/本地删除，冻结该单元。Windows 文件即使更晚也不覆盖，不自动补回用户删除的文件。
- 两侧同时改变：继续使用 WSL 现状，列出冲突；不自动合并未知字段，不以 mtime 仲裁。
- 来源删除：默认标记“源已删除”；仅对明确托管且目标未手改的条目执行删除。用户导入文件与 skills 的删除需确认；不得 rm -rf 整个根。
- 解除冻结：保留 WSL 并停止跟随，或预览后用当前来源重新接管。两种操作都带预期目标 hash，旧预览不得覆盖新编辑。

登记信息损坏/丢失时退回“未托管”，不能因状态库丢失重置 WSL 配置。

### A2 的迁移边界

只通过现有 `SyncWSLPiLoginAuth` 适配器执行以下语义，不让通用导入器接触 auth：

1. Windows auth 缺失视为登录失效；不得用 distro 副本复活退出的登录。
2. SHA-256 相同为 Fresh；distro 缺失时前向复制。
3. 不同且宿主 mtime 超前超过 2 秒：前向；distro 超前超过 2 秒：回写；容差内：保留 distro 并回写。
4. distro mtime 探测失败：两侧不动，保留 A2 的 DistroHasCopy 降级，而不是将失败当作时间 0。
5. 写失败沿用 DistroHasCopy / DistroNoAuth 分类；权限补偿和清理双失败必须拒绝。不能将“有文件”当作“可安全降级”。

沿用 A2 的字节透明处理，不解析 token，不将 token、正文、公开可查询的凭据 hash 写进状态页。mtime 不能证明 token 有效，报告已记录“旧内容恢复导致 mtime 变新”的事故；本设计不声称解决 OAuth 并发轮转。

### 时机、缓存与 I/O

- **启动**：确定最终 WSL spec 与目标后、执行 CLI 前，对本 CLI 关键小文件及已启用共享导出做校验；不安装插件、不全扫 skills。A2 每次登录启动仍比对，不用 TTL 替代。
- **保存**：Config/PiConfig/导出成功后增加来源 revision，合并同一目标的待同步项；对已授权、已运行且无相关活动会话的目标可异步推送。停止的 distro 只标待同步，不因保存而唤醒。
- **安装**：完成 CLI 安装后只触发同一供给入口的安全子集；不再独立 `cp -r`，不把配置失败改成 CLI 安装失败。AlreadyOK 同样可触发检查。
- **显式检查/同步**：允许唤醒当前目标，必须展示目标和预计操作；envcheck 普通刷新只检查，不写配置。
- **面板外编辑**：关键小文件在下次启动重新计算 hash；资源目录通过显式刷新/后台有界扫描发现，不能假设 UI 事件覆盖一切。

首次解析 Home/用户需一次目标探测；新增同步正常无变化路径以“一次批量元数据/权限探测 + UNC 小文件读取”为预算，写入时再一批提交/权限验证。复用既有 Home/UNC 接缝，不为每文件启动一次 wsl.exe。该预算不包含现有 A2、provider writer 自带调用；后续合并调用必须先证明语义不变。记录真实启动增量、跨界次数、字节数，不能承诺未经测量的毫秒数。

## 四、D2：覆盖范围与文件清单

### 覆盖策略候选

| 候选 | 优势 | 问题 | 推荐 |
|---|---|---|---|
| 按 CLI 整目录镜像 | 清单简单、表面完整 | 复制历史/凭据/信任/Windows 二进制；删除和冲突无法安全解释 | 否决 |
| 白名单文件/字段 + 原生插件操作 | 归属明确、能逐项验收 | 需维护格式适配器；未知字段不自动携带 | 推荐 |
| 完全不迁移用户文件，只供 provider | 改动最少、风险小 | T2/T3/T5/T7 仍要求用户重配 | 作为未授权目标的默认状态，不作为最终能力 |

“对称”指五 CLI 都有明确边界，不意味着五者拷相同目录。以下是允许范围，不代表系统扫描到就自动导入。

### Pi：`~/.pi/agent` 全景

| 文件/目录 | 策略 | 值得供给的内容 | 排除/原因 |
|---|---|---|---|
| `models.json` | ManagedNamespace + 用户条目显式导入 | 复用 provider 构建与 reconcile；同步全部期望 amagi-*，启动叠加当前预设 | 不复制 Windows 整文件；非 amagi provider、未知顶层字段归 WSL 用户 |
| `models.json` 登录模型覆写 | 窄字段托管 | 从登录预设生成 provider/model 的 contextWindow，复用 `UpsertPiLoginModelOverride` 语义 | 不顺带替换该内置 provider 的其它字段；WSL 自定义同字段须先确认接管 |
| `auth.json` | A2Auth | 仅既有 Pi 登录流程 | 通用播种、插件同步、导出任务均排除；不把 A2 擅自扩成所有 CLI 的凭据迁移 |
| `settings.json` | 字段级 TrackedProjection（同意后） | 已确认跨平台的偏好字段、有效的资源引用 | `packages` 走插件操作；绝对路径先转换；会话/机器字段、未知字段不导入 |
| `amagi.json` | TrackedProjection | profile、角色模型、压缩模型、MCP 路由；保留既有验证器 | 引用的 provider/plugin/server 不存在时报告未满足依赖，不伪造默认值 |
| `mcp.json` 及实际被插件消费的 `.mcp.json` | server 级 TrackedProjection | 经过格式与路径适配的 MCP 定义 | 不假设 Pi 核心原生读所有文件；先验证已装插件的实际配置入口 |
| `AGENTS.md`、`prompts/`、`themes/` | 选中后 ImportOnce / TrackedProjection | 用户可移植文本资源 | 不改写正文中的任意路径；有风险引用时列出，不宣称可运行 |
| `skills/` | 与共享 skills 相同的按目录策略 | 自有、可移植、明确选择的 skill | 不重复复制插件自带 skill；不递归跟随越界链接 |
| `extensions/` | 显式导入或插件重放 | 经用户确认的源码型扩展 | 可执行代码，不能作为普通偏好默认推送；原生构建产物排除 |
| `git/`、`npm/` | 原生操作重放 | 来源、固定版本/ref、资源过滤清单 | 不复制 `.git`、node_modules、缓存及构建实体 |
| `sessions/`、备份、日志、锁、socket、pid、缓存、amagi 运行目录 | LocalOnly | 无 | 会话历史、运行注册表、机器本地状态不能跨界 |
| 未列出的文件/目录 | LocalOnly | 可在预览中显示“未支持” | 禁止以“未排除”代替白名单 |

`settings.json` 不允许先整文件拷入再删除 packages；必须从允许字段构建候选，防止 CLI 提前加载未安装或 Windows 形态的资源。

### Claude Code

| 文件/目录 | 推荐范围 | 必须不同步 | 理由 |
|---|---|---|---|
| `~/.claude.json` | 用户选择的 `mcpServers` 单元可导入、跟随转换 | projects/trust/onboarding、账户状态、未知键 | MCP 可移植；信任授权和首次使用状态不能从另一环境继承 |
| `~/.claude/settings.json` | 明确允许的跨平台偏好；MCP/plugin 启用项须依赖已满足 | 自动信任、放宽权限规则、未知 hooks/命令、宿主路径 env | 设置可能授权执行，不能整文件自动复制 |
| `~/.claude/CLAUDE.md`、`commands/`、`agents/`、`skills/` | 选中后导入；持续同步遵循 WSL 手改冻结 | 未选资源、越界链接 | 文本可共享但仍可能引用 Windows 工具 |
| `~/.claude/plugins/` | 由目标原生插件服务重放，先验证相应操作接口 | 插件缓存/已安装实体/市场缓存整树 | 安装来源与目标实体分离 |
| 凭据文件、projects/history/log/cache、项目本地配置 | LocalOnly | 全部 | Claude 凭据不套用 Pi A2；仓库文件由版本控制负责 |

### Codex

| 文件/目录 | 推荐范围 | 必须不同步 | 理由 |
|---|---|---|---|
| `~/.codex/config.toml` | 按 TOML 节导入 `mcp_servers`；选中的可移植 profiles/偏好 | trust、sandbox/approval 等权限决定、项目绝对路径、未知命令 | provider 仍由既有 env/-c 管理；不整体覆盖用户配置 |
| `~/.codex/AGENTS.md`、用户自有 skills | 选中后导入 | 未选内容与插件运行缓存 | 用户规则与机器态分开 |
| `auth.json`、sessions、history、logs、cache、数据库、锁 | LocalOnly | 全部 | 无 Codex 凭据迁移授权，不套用 A2 |

TOML 编辑必须保留非目标节与注释；解析失败停止该文件，不用 JSON 的整对象序列化替代 TOML 保真编辑。首批 Codex 导入只开放有测试的节，不宣称任意 config.toml 可无损转译。

### OpenCode

| 候选 | 取舍 |
|---|---|
| 复制全局 `opencode.json/jsonc` 到 WSL 并持续覆盖 | 与用户全局配置冲突，重复批次一机制，不采用 |
| 延续 `OPENCODE_CONFIG` 附加层；扩展经过验证的 preset MCP 转换 | 推荐；provider 与用户全局配置保持隔离 |
| 不做任何扩展 | provider 已可用，但 preset 的 Windows MCP 命令仍可能失效，作为第一阶段兼容行为 |

继续使用 `~/.config/opencode/amagi-codebox.json`，从 runtime builder 产物生成，不从 Windows 全局文件镜像。增加 MCP 转换时，仅作用于本次显式选择的 preset/已授权导入项。碰到 distro 全局同名 MCP server，默认保留本地、不把该冲突 server 写入附加层；用户可预览后明确选择会话覆盖。必须实测所支持版本的多层优先级，不能只凭“附加层”三字推断同名键行为。

WSL 全局 `opencode.json/jsonc`、认证状态、sessions、storage、cache、插件实体保持本地。可移植用户规则/skills 走显式导入；插件走目标原生操作，未验证安装契约前只展示状态。

### Oh My Pi

列入覆盖矩阵但标记“本批不设计”：不安装、不播种、不增加同步、也不承诺手装 WSL omp 的配置面板可用。已有 `WriteWSLOmpAgentConfig` 与 Windows omp 管理保持原状；共享 `.agents` 文件可能被手装工具自行读取，但不据此宣称完成 omp 集成验收。

### 共享 `~/.agents`

| 对象 | 候选与取舍 | 推荐规则 |
|---|---|---|
| `skills/` | 全量镜像覆盖完整但可能巨大且含链接/平台脚本；按选择同步成本可控 | 按 skill 目录选择，一次预览同意可选全部可移植项；后续按清单增量，不按每次对话“猜需要哪个 skill” |
| `amagi-media-models.json` | 直接当普通文件复制简单但可能泄密/覆盖本地；从既有导出器按目标发布可保留契约 | 单向受管导出，首次明确同意向该 distro 共享其中密钥；保持 v1.5 收录规则、schema 和优先级，不额外扩充模型 |
| `amagi-modalities.json` | 全文件镜像会丢 distro 学习；通用 mtime 双向会传播不可比结论 | schema 校验后按规范化模型 ID 补缺；已经存在且不同的 distro 记录保留并报冲突，不反写宿主 |

视觉清单含密钥，文件 0600；在 0700 私有 staging 中生成和提交，不能在共享目录先落宽权限临时文件。现有 `.agents` 目录可能合法为 0755，不为同步而递归改整个用户树权限。来源 `updated_at` 每次变化不应触发无意义大任务：比较规范化业务内容，目标生成时间仅在实际更新时变动，文件 schema 不变。

对已有手改视觉清单默认冻结。用户选择“保持 WSL 独立”后停止分发；选择接管后才按 CodeBox 当前导出更新。删除 provider/关闭 vision_export 属敏感撤回：已托管且未手改的条目应撤回；若目标冲突或不可达，明确显示“密钥副本未确认撤回”，不能声称已删除。撤销同步不是吊销 API key，用户仍需在提供商处吊销凭据。启用同步的会话若遇到未完成撤回，不静默消费旧导出，按 D4 阻断要求处理。

skills 用目录内容清单（相对路径、类型、大小、hash）做增量；先静态校验再写 staging，最后只替换已登记目录。禁止自动执行安装/验证脚本；不带 `.git`、node_modules、缓存、凭据和会话产物。源软链接只允许明确解析到选中树内的资源；越界链接、循环、绝对链接和无法保留权限的资产列为不兼容。目录很大时后台分批与进度反馈，不放进会话启动等待；检查未完成必须显示“扫描未完成”。

### 插件实体：复制还是重放

| 候选 | 代价与风险 | 决定 |
|---|---|---|
| 复制 git/npm 实体 | 离线快，但含 Windows shim、原生模块、宿主路径；git 脏工作区归属不明 | 不采用 |
| 插件面板按 distro 重放 install/remove/update | 需要网络和 Linux 依赖，但遵循 CLI 自身安装契约 | 推荐 |
| 直接从 /mnt 引用 Windows 插件目录 | 零复制，但共享写入、DrvFS 性能与原生依赖混用 | 不作为自动模式；本地源码映射须显式选择并验证 |

插件操作必须有目标：Windows 或当前 WSL distro。默认保留 Windows；“同时应用到 WSL”需明确勾选，每个目标独立结果，不能 Windows 成功即报整体成功。

重放记录 source、确切版本/ref、资源过滤项与操作 ID；npm 带版本、git 尽量固定 commit，不能把宿主一次 update 等价为目标再次追 latest。WSL 本地增删、git 未提交修改、来源不可解析时冻结，不自动 reset/clean。更新/删除只作用于登记为本系统管理的插件；失败不得写入“已安装”状态，复检 settings 登记与实体是否同时有效。启动只提示缺失，不替用户联网安装或执行 postinstall。

## 五、D3：形态转换层

### 候选与推荐

| 候选 | 优势 | 缺点 | 结论 |
|---|---|---|---|
| 播种时转换一次 | 最小改造 | 后续更新、distro 路径变化继续冻结 | 否决 |
| 每次需要更新时生成 distro 专属候选，校验后提交 | 可追溯、保留源、适配各 CLI | 需要格式适配器和转换版本 | 推荐；lazy 校验不等于每次重写 |
| 只校验报告，不转换 | 无错误改写风险 | npx.cmd 等确定可改问题仍要用户处理 | 未知/不安全形态的强制回退 |

转换在“来源解析 → 目标候选”之间执行，不修改 Windows 原文件，不在任意文件正文中做全局正则替换。转换器输出候选和诊断，不执行 MCP 命令。

### 确定规则

| 输入 | 推荐处理 | 不允许的推断 |
|---|---|---|
| MCP command 为裸 `npx.cmd` / `npm.cmd` | 在目标真实登录 PATH 确认 Linux 原生命令后映射为 npx/npm | 不把任意 `.cmd` 都去后缀 |
| command 为 `C:\...\npx.cmd` | 仅明确识别为已支持工具 shim 时替换成已验证原生命令，否则拒绝该导入单元 | 文件名相同不证明行为等价 |
| command 为 cmd.exe、PowerShell/ps1、Windows exe、任意 shell 字符串 | 标为需手动适配；保持现有 WSL 配置；不生成假可用条目 | 不自动改成 bash，不自动启用 interop |
| 参数中独立的 `C:\...` 路径 | 先匹配本批已部署资产的 WSL 位置；否则经目标 wslpath/挂载验证映射，须存在且用途允许 | 不盲信所有 distro 都挂 `/mnt/c`；不把 Windows 可执行文件当 Linux 程序 |
| env 中明确声明为路径的值 | 按该键的路径类型转换；列表按明确分隔规则处理 | 不把 URL、token 或普通含冒号字符串当路径；不全局分号换冒号 |
| 未知 env 值像 Windows 路径、字符串内嵌路径 | 阻止该条目自动导入，提示具体键名 | 不静默删 env 后声称 server 可用 |
| HTTP/远程 MCP | 保留 URL、认证与协议字段，敏感字段私有处理 | localhost 不自动改成宿主 IP；网络可达性是独立问题 |
| `%USERPROFILE%`、`%VAR%`、相对路径 | 只在已知字段与已确认基目录下解析；无法确定则要求用户提供 Linux 值 | 不执行 shell 展开，不把变量内容写进日志 |

例如，`command=npx.cmd, args=[-y, package]` 只有目标能解析原生 npx 才生成 `command=npx`；`command=node, args=[C:\tools\server.js]` 还需判断该脚本是否随选中资产部署或可从已验证挂载读取。仅把盘符变成 `/mnt/...` 不证明脚本依赖可用。

一个 MCP server 作为原子导入单元：转换失败则该单元不新增、不更新；已有 WSL 同名 server 保持原样。显式标为会话必需的 server 失败则阻止启动，普通可选 server 失败仅告警。拒绝项保留脱敏原因和字段路径，不悄悄丢弃。

### skills 与其它资产引用

- 推荐资源使用相对 skill 根路径、Linux 可用解释器；CodeBox 不重写第三方脚本或 Markdown 里的任意盘符。
- 结构化资源清单中的路径可按目标映射；源代码/提示文里的绝对路径只静态检查并报告，要求源资产作者适配或用户选择 WSL 版本。
- 原样复制 `.ps1/.cmd` 不算支持。文本检测没有发现问题也不算运行验证，状态应区分“已供给”与“运行已验证”。
- 被引用插件/skill 先准备好再发布依赖它的配置；资源未就绪时不发布悬空引用。
- 多 CLI 复用转换的命令/路径规则，但各自解析与输出适配器独立。不得把 Claude `mcpServers`、Codex `mcp_servers` 和 OpenCode `mcp` 当作相同 schema。

## 六、D4：感知、失败与运维

### 候选与推荐

| 候选 | 优势 | 缺点 | 推荐 |
|---|---|---|---|
| 只记日志 | 最少前端改动 | 继续复现“面板已保存但会话仍旧”的摩擦 | 不采用 |
| 只在 envcheck 展示 | 可复用卡片 | 用户启动时未必访问环境检测 | 必做但不够 |
| 会话设置摘要 + envcheck 详情 + 面板保存反馈 | 使用时可见，运维集中 | 增加结构化状态与前端消费 | 推荐 |

在既有“WSL 内的 CLI”卡片下增加“配置状态”，不混淆安装状态。明确 distro、默认用户/Home、CLI、最后检查时间；会话设置只展示当前目标的摘要与详情入口，不在这里扩展 B5 的多 distro 选择功能。

状态至少包含：未启用、未检查、待同步/陈旧、同步中、已同步、部分完成、保留本地修改（冻结）、不兼容、同步失败、权限风险、目标不可达。安装成功不代表配置已同步；文件同步成功不代表 MCP 已连接或 skill 已执行。

详情按文件/字段列出：相对路径、归属策略、来源 revision、转换版本、最后成功时间、冻结原因、失败阶段、可执行动作。敏感项只显示文件名和方向，不显示内容差异、密钥、完整命令/env 或可公开的凭据 hash。

动作分离为“检查”“预览同步”“应用安全更新”“保留本地”“确认接管”“在插件面板操作”。没有“强制覆盖全部”。修改配置不要求 root；不把普通同步挂到现有 apt/root 安装确认逻辑上。

### 失败语义

| 情况 | 启动/保存结果 | 用户提示示例 |
|---|---|---|
| 可选普通设置同步失败，旧 WSL 配置可读且安全 | 启动继续、结构化警告；宿主保存仍成功 | “Windows 已保存；Ubuntu 的 amagi.json 未更新，正在保留 WSL 修改。” |
| 首次可选 MCP/skill 未供给 | 启动继续；标记该能力不可用 | “此 MCP 使用 npx.cmd，但 Ubuntu 未找到原生 npx；未写入该条目。” |
| 用户标记必需的配置/资源失败 | 当前启动失败，不偷偷移除依赖 | “Ubuntu 的必需 MCP 配置未准备好，请修复或明确取消必需设置。” |
| A2 DistroHasCopy | 延续降级放行并警告，不宣称凭据有效 | “凭据同步未完成，使用 WSL 现有副本；如认证失败请重新登录。” |
| A2 NoAuth、源退出登录、权限不安全/不可确认 | 拒绝登录启动 | “无法安全准备 Ubuntu 的 Pi 登录凭据，本次未启动。” |
| 当前选择的托管 provider 不能安全落地且无可证明等价的 env/参数回退 | 拒绝当前启动 | “模型配置未写入 Ubuntu；未改用默认模型启动。” |
| 同步失败且可能留下宽权限密钥文件 | 拒绝相关启动，显著提示清理；不自动扩大权限重试 | “配置权限设置失败，仍有文件待清理；请修复后重试。” |
| 已启用视觉导出的敏感撤回未完成 | 相关 WSL 启动拒绝静默使用旧清单；用户须修复或明确退出该同步关系并承担独立副本管理 | “WSL 中的密钥副本尚未确认撤回，请先处理配置状态。” |
| distro 不可达 | 标记未知/待同步；不得写到 Windows 冒充成功 | “无法访问 Ubuntu，Windows 保存已完成，WSL 尚未更新。” |

**行为变更声明**：当前 OpenCode 写入失败会记录日志后以无 provider 启动；本方案推荐在安全接入批次改为“当前选择模型不可供给则失败”，防止悄悄运行错误模型。Pi 只有既有 env 回退确实表达同一 provider/model 且不泄密时才可保留回退；不能笼统以“有 env”证明等价。该变化需独立回归与用户文案，不伪装为无行为变化重构。

envcheck 挂接继续使用 `CheckIssue` 的 code/severity/message/detail 模式，普通陈旧与冻结为 warning，不沿用它们去翻转 CLI 安装可用性；阻断与否由具体启动需求判定。状态查询使用既有批量探测/短 TTL 思路，保存、同步和插件操作完成后失效缓存。未探测不写成通过，读取页绝不触发复制或联网安装。

## 七、总体架构与接口边界

### 同步模型（一段式）

在确定的 WSL 目标上，由一个供给协调器收集 CodeBox 业务配置、已授权的用户导入项和资源清单，按文件/字段归属产生候选；候选经过 distro 形态转换、依赖和权限检查后，以预期目标 hash 为前置条件提交，成功才更新来源/目标基线。启动调用该协调器校验关键小文件，保存事件只标记 revision 并调度安全更新，插件操作独立在目标 CLI 中重放。A2 凭据和既有 provider/OpenCode writer 作为专用适配器接入，用户本地修改冻结普通同步，状态统一供会话页和 envcheck 使用。

### 组件责任

推荐新增小型领域包 `internal/wslsupply`，而非把所有规则继续塞进 `wslsetup` 或 `app.go`。它只负责策略、清单、计划、状态与调度；不得导入 App、Wails 或插件 UI。转换器仅先支持已承诺的格式，不建任意文件同步框架。

| 组件 | 职责 | 禁止职责 |
|---|---|---|
| App 编排 | 注入 Config/Secrets 快照、最终启动目标、A2/provider 适配器；挂保存事件 | 自己解析五套配置、在保存锁内等待 WSL |
| wslsupply | Plan/Apply、归属清单、冲突状态、任务合并 | 决定模型业务语义、解析 OAuth token |
| launcher 现有函数 | provider 构建、严格 reconcile、A2 判定、OpenCode 附加层 | 被通用 mirror 取代 |
| platform Windows 传输适配 | 目标解析、UNC、批量 WSL stat/chmod、受控执行 | 按运行时 GOOS 在通用业务逻辑散布分支 |
| CLI 格式适配器 | 已知字段提取、转换、验证与保真提交 | 全文件未知键猜测转换 |
| piplugin/其它插件服务 | 同目标读取与原生命令重放 | 复制 Windows node_modules |
| envcheck / 前端 api wrapper | 查询脱敏状态、发显式动作 | 直接访问 UNC、手改 wailsjs 绑定 |

非 Windows 提供同接口的“不适用”实现，遵循项目 per-OS 文件惯例。

### 拟定接口（规格，不是已新增 API）

| 接口/对象 | 核心字段或行为 | 契约 |
|---|---|---|
| `TargetRef` | distro、解析用户 UID/Home、CLI、目标根、目标 generation | 来自最终 WSL 执行目标；不能各组件重新选 DefaultWSLDistro；初版仍默认 distro，无新增选择器 |
| `SupplyRequest` | target、reason（launch/save/manual/install）、sourceRevision、selectedUnits、requiredUnits | 不接受前端任意绝对目标路径；unknown CLI/单元拒绝 |
| `Inspect(ctx, target)` | 批量探测生成 Snapshot | 可启动只读探测，不写；供显式检查/执行阶段使用，不放 BuildPlan |
| `Plan(request, snapshot)` | 返回单元动作、预期 hash、转换版本、依赖、阻断项 | 纯计算，不写文件、不 spawn；敏感候选仅内存/现有 SecretBufferRef 生命周期 |
| `Apply(ctx, plan)` | 复核目标与 hash、写入、验证、返回逐项结果和撤销记录 | 目标变更/用户修改则拒绝旧计划；可部分成功但不得虚报整批成功 |
| `MarkDirty(source, revision)` | 保存后标脏、合并工作 | 不在配置锁下等待 WSL；落盘 desired revision，重启可补漏 |
| `GetStatus(target)` | 脱敏快照、检查时间、待处理项 | 读缓存不触发安装或同步；无缓存返回未检查 |
| `RunPluginOperation(target, operation)` | 显式目标、来源/ref、预期登记状态 | 与文件同步分开授权与状态；执行后使配置状态失效 |

PiConfig 原有无 target 方法继续默认 Windows，新增显式目标的窄接口；不要在请求期间修改进程级 `PI_CODING_AGENT_DIR`。WSL 面板编辑是直接编辑 WSL 用户文件，成功后让相关投影冻结；它不是把 WSL 文件拉回宿主再广播。PiConfig 的现有 rename 失败直接覆盖回退不能作为 WSL 私有文件 writer 复用，需用安全传输适配器提交。

providerSync 在原宿主同步之外发布 dirty revision；WSL 执行时从 Config/Secrets 同一业务快照构造托管 providers，复用 `ReconcilePiAgentConfig` 的严格解析/命名空间语义。不能复制宿主 models.json，因为其中可能有本机登录、本机路径与非托管 provider。provider 构建部分失败时不以残缺集合 prune 旧项：失败 provider 保留并报错，明确删除才删除；当前选中 provider 不可构建则阻断。

### 状态落点与提交

拟在 CodeBox 配置服务管理的 `~/.amagi-codebox/wsl-config-supply.json` 保存授权/目标/单元清单：schemaVersion、target generation、policy、sourceRevision、sourceHash、lastWrittenHash、transformVersion、冻结状态、lastSuccess/lastErrorCode。敏感 hash 仅私有本地存储，不通过状态 API 返回；不持久化候选正文或认证内容。清单文件走项目私有原子写机制，不由前端自行解析。

目标身份不能只用 distro 名称：重新安装同名 distro 或默认用户/Home 改变时，旧授权不得自动扩散；需要失效清单并重新确认。初版以可取得的 distro 身份标识和 UID/Home 组成 generation；拿不到稳定身份时按未知处理，不猜测继承。

提交采用目标内私有 staging、唯一临时名、权限先收紧再 rename、最终校验；字节走 UNC，不塞进 wsl.exe 命令行，也不将秘密 base64 写日志。配置与资产按依赖先后发布，没有跨文件系统的整批原子性承诺。只有内容已提交且清单记录成功才报“已同步”；若内容成功但清单写失败，下次视为待核实，不重新接管文件。

协调器按目标根/文件串行化，providerSync、launch、插件对同一个 settings/models 的写入使用同一锁边界；A2 反向写宿主时还需锁住宿主 auth 资源，避免多个 distro 同时回写。不同操作预览 hash 过期就重新检查，不做无限自动重试。

外部编辑器和运行中的 CLI 不参与 CodeBox 锁；“写前复核 hash + rename”不能数学上消除外部写入的最后窗口。用户配置/插件在相关活动会话期间暂停后台应用，保留最后可恢复版本且不自动回滚覆盖后续编辑。A2 必须继续容许登录刷新，这是明确剩余风险；不声称用 mutex 解决 OAuth 多进程竞争。

### 桌面与远程执行边界

桌面在最终 WSL 目标确定后、PTY 启动前执行协调器。远程/v1/restart 保持 `BuildPlan` 纯读：计划只携带待供给描述，`launch_executor` 的受控 effect 在 Apply 阶段 Inspect/Plan/Apply，再启动 PTY；不得在 planner 调 WSL Home 探测或同步。

建议增加专用 `EffectWSLConfigSupply`，顺序在其它必要服务准备之后、PTY 之前；效果的 Abort 只撤销本次产生且当前 hash 仍匹配的普通文件写入，沿用现有 CAS 补偿记录。A2 凭据刷新是持久认证状态，不随“PTY 启动失败”回滚成旧 token；其补偿仅清临时物，不恢复旧凭据。远程 effect 的权限/门控/补偿验收与接入同批完成。

接入后桌面与远程共用 A2 Go 适配器，shell 只保留 agent root 与凭据 env 重锚，不再执行第二次不同实现的复制。切换前先覆盖所有旧载荷调用方及 restart 测试；若执行器尚未接入，维持旧路径隔离，不能宣称统一安全模型已覆盖远程。这是消除现有双实现差异，而非另造凭据算法。

## 八、D5：分阶段落地路径

### 排序候选与取舍

| 候选 | 优势 | 风险 | 结论 |
|---|---|---|---|
| 五 CLI、全部面板、双向同步一次上线 | 短期对称 | 冲突与凭据事故面大；无法独立回滚 | 否决 |
| 先做 UI，再逐个补复制 | 可见变化快 | 容易把安装成功/复制成功当可用；T3/T7 实质未闭环 | 不采用 |
| 先安全与托管小文件，再用户配置、资源、插件 | 每批独立验收、保留旧机制 | 全部功能需数批完成 | 推荐 |

二轮证据唯一明确“最高严重度”的 T4 已由批次一修复，本设计将其作为 P0 防回归而非新修复业绩。剩余 P0 指配置供给和密钥边界：T1/T3 的不安全整树播种、T7 的关键导出缺位及 T8 在既有入口的形态错误。完整用户导入与插件更新列为后续，不以“P0 完成”冒充全部 T1–T8 关闭。

工程量是包/文件量级估算（含针对性测试，文件可能跨批重叠），不是排期承诺。

### 批次 S0：安全接入与目标一致性（P0，前置）

- 改动面：wslsupply 最小骨架、platform 目标/私有提交、launcher A2 适配、App/launchplan/executor，停用 `ensurePiConfig` 整树复制，保留 CLI 安装成功语义。
- 交付：单一目标、逐项结果、最小状态查询；A2 桌面/远程走同一判定和安全写，重锚不变；普通配置尚不自动接管。
- 估算：5–6 个包/根编排面，约 12–20 个文件；高风险在 remote effect、补偿和凭据写入。
- 独立验收：首装不带入 sessions/auth/plugin 实体；AlreadyOK 不阻止检查；A2 两侧相同/宿主新/distro 新/2 秒容差/探测失败/退出登录/写失败/chmod+清理双失败逐项通过；BuildPlan 零副作用；桌面与远程真实登录入口结果一致；并发测试不撞临时名。
- 回滚：关闭新协调入口，保留 A2 已验证通道；不得重新启用旧整树播种作为回滚手段，不回滚 token 内容。remote 安全验收未过则不切换该入口。

### 批次 S1：托管 provider 与共享小文件（P0）

- 依赖 S0。复用 provider builders/reconcile，补 WSL dirty/lazy 接入与 Pi 登录 contextWindow；视觉清单按目标授权发布，modalities 补缺；OpenCode 附加层复用并加确定性 MCP 形态检查。
- 最小 UI 同批交付：当前 distro、陈旧/失败/冻结、共享密钥确认。改变 OpenCode“无 provider 仍启动”的失败语义并单独测试。
- 估算：launcher/config/wslsupply/envcheck、App + 前端 API/会话设置/WSL 卡片，约 14–24 个文件；中高风险在密钥与 provider 删除。
- 独立验收：新/旧 distro 不重装即可获得托管更新；非 amagi provider 保留、损坏 JSON 不覆盖；构建部分失败不误 prune；导出 v1.5 不扩大收录；密钥撤回失败可见；0600 从 distro 内实测；OpenCode 全局用户配置仍生效，选中模型配置失败不静默换模型；无变化调用次数有基线记录。
- 回滚：暂停新增分发，保留目标现状；仅按 lastWrittenHash 匹配撤销本批产物，用户后改不动；已复制密钥须单独确认清理。

### 批次 S2：Pi 配置和 MCP 导入（P1，紧随 P0）

- 依赖 S0/S1。落 Pi settings/amagi/MCP 白名单、转换器、冻结/接管操作；PiConfig 显式目标读写；不碰插件实体。
- 估算：wslsupply/launcher/piconfig/platform + App/前端，约 12–20 个文件；高风险在混合文件字段归属。
- 独立验收：Windows 面板修改进入未手改 WSL 配置；WSL 手改后再保存 Windows 必须保留 WSL；来源/目标删除、旧播种树无清单、未知字段、损坏文件、双侧同时修改全部有确定结果；npx.cmd、盘符参数、env 路径、ps1、未知 shell 字符串覆盖；amagi 引用缺 provider/plugin 有明确诊断。
- 回滚：关持续跟随；只恢复仍匹配本次写入 hash 的普通配置，不恢复 auth，不删除未登记文件。

### 批次 S3：共享 skills 与用户资源（P1，可独立于 S2 并行准备）

- 依赖 S0/S1 的授权、状态和私有提交；实现资源清单、有界扫描、增量 staging 与静态兼容检查。
- 估算：wslsupply/platform + 前端资源选择/状态，约 8–14 个文件；风险在软链接、可执行资产、目录量级。
- 独立验收：选中 skill 连同相对依赖可由目标实际消费者发现；源改动增量更新；WSL 改动冻结；大目录不阻塞启动；越界链接不复制；Windows 专用脚本明确未支持。用隔离测试资产验证供给，再选择一项真实非破坏性 skill 冒烟，不能以文件存在代替运行证据。
- 回滚：停止跟随，按登记和 hash 删除/恢复本批自有资源；本地新增、改动或未登记目录不动。

### 批次 S4：Claude/Codex 用户配置导入与 OpenCode 扩展（P2）

- 依赖 S2 的转换/冲突机制。各自格式适配器、导入预览、局部编辑；OpenCode 仅扩展已授权附加层内容。
- 估算：wslsupply + 各 CLI 配置服务/launcher + 前端，约 14–22 个文件；风险在 TOML/JSONC 保真、CLI 版本和权限字段。
- 独立验收：每 CLI 至少一个远程 MCP 和一个 Linux stdio MCP 真实启动；Windows 信任/凭据/历史不进入 distro；TOML 非目标节与注释保留；全局与附加层同名冲突遵循选择；所有 CLI 未知字段保持本地。
- 回滚：禁用各适配器；按单元 hash 条件撤销，不全量恢复用户根。

### 批次 S5：插件目标化与事件推送优化（P2，可延后）

- 依赖 S0/S2 状态/锁/目标接口；Pi 先行，其他 CLI 仅在各自原生操作契约验证后开放；omp 排除。
- 估算：piplugin 及后续 plugin/codexplugin/opencodeplugin、platform/wslsupply + App/前端，约 18–30 个文件；高风险在安装脚本、网络、版本锁定与取消。
- 独立验收：Windows/WSL 插件列表各反映自己的实体；固定 npm 版本和 git ref 可在 WSL 原生安装；单侧失败独立显示；离线、超时、取消、重复操作幂等、WSL 脏仓库保护；启动不自动安装；保存只标脏且不唤醒停机 distro。
- 回滚：关 WSL 插件写入口；已装插件不自动卸载，用户确认后由原生 CLI 移除；不能以同步回滚为名 clean/reset 工作树。

### 验证层级

每批至少 L2：纯策略表测、注入传输故障、相关 Go/前端测试及真实 WSL 入口冒烟；S0/S1 涉及安全和远程执行，增加相关全链回归。Go 测试按语义主题命名，前端测试放 `frontend/src/__tests__/`，绑定经 Wails 生成。全项目 L3 留给合并/发布，不在每个修复轮无差别重跑。

真机验证必须在隔离用户/测试目录或专用 distro；不能再对日常 auth 文件做“只快照一侧”的 e2e。确需使用真实凭据时双侧保护内容、mtime、权限，取得授权；报告不输出正文。权限必须从 Linux 内 stat 验证，Windows os.Chmod 返回成功不能替代证据。

## 九、风险清单与未知项

| 风险/未知 | 影响 | 判别/约束 | 可逆性 |
|---|---|---|---|
| A2 shell 与 Go 的安全差异 | remote 可能绕过原子写/fail-closed | S0 统一执行边界前逐故障对齐；当前只观察，未实测失败 | 凭据轮转不可简单回滚；切换入口可回退 |
| mtime 恢复、时钟精度、并发 refresh | 新陈判定可能选择旧 token | 保留 A2 2 秒容差和 outcome，测试恢复场景；不宣称 hash 能判断有效性 | 必要时重新登录；不自动恢复旧 token |
| 外部进程不遵循锁 | 写前 hash 与提交之间仍有竞态 | 活动会话暂停普通同步、唯一 tmp、hash 条件恢复；A2 风险显式保留 | 普通文件可条件恢复；用户后改不可覆盖 |
| OpenCode 单一附加文件被不同会话先后覆盖 | 进程锁只保护写，不保证 CLI 尚未读完 | S1 并发启动与重载实测；若确证互串，后续改私有 per-launch 文件并继续 OPENCODE_CONFIG，不换机制 | 需清理生命周期；本轮不预建第二套配置源 |
| Windows 路径并不等价于 /mnt 路径 | 自定义挂载、UNC、网络盘不可达 | 目标内映射/可读验证；失败拒绝单元 | 不改原文件，风险低 |
| CLI/plugin schema 版本差异 | 文件已写但不消费或字段含义变化 | 每适配器支持版本与实际读入口必须测试；未知版本只校验/提示 | 关闭该适配器即可 |
| `.agents` 导出含密钥 | 同步扩大秘密落盘范围、撤回未完成 | per-target 明确同意，0600，敏感清单不进日志/备份；显示撤回状态 | 删除副本不等于吊销 key |
| 学习层跨 provider 泛化不是运行时能力证明 | WSL 网络/端点仍可能不通 | 只按现有模型知识契约补缺；不把学习记录当联网验证 | 目标记录保留本地，可停止跟随 |
| 大 skills、链接、原生扩展 | I/O、代码执行与平台不兼容 | 有界扫描、选择式导入、不自动执行、插件原生重放 | 停跟随、只撤销自有未改目录 |
| 默认 distro/用户变化 | 配置误写给新环境 | 目标 generation 失效，实际启动与同步目标一致 | 重新确认，不自动迁移授权 |
| 新文件与旧清单部分提交 | 重启后误报已同步或重复覆盖 | 结果逐项，清单成功才确认；缺记录退为未托管 | 条件修复，不盲目整批回滚 |

不在本设计中处理：B5 多 distro 会话选择产品功能、网络 NAT/mirrored 自动适配、GitAssist/PathPicker Linux 路径、locale/编码、工具链全面安装，以及 omp 手装场景。相关诊断可以引用既有能力，不借配置供给扩大实现范围。

## 十、与既有机制的衔接声明

- **批次一**：保留 `WriteWSLOpenCodeAgentConfig` 和 `OPENCODE_CONFIG` 附加层；仅在该输入侧做已知字段转换、结果可视化与明确的失败语义调整。不恢复 `OPENCODE_CONFIG_CONTENT` 作为跨界唯一载体。
- **批次二 A2**：保留双向新陈规则、2 秒容差、并列取 distro、源退出不复活、mtime 失败不动、chmod/清理 fail-closed 和字节透明。统一的是调用边界与安全实现，不是改成普通配置镜像。远程现有差异在 S0 验收前不能记作已解决。
- **现有 pi provider**：复用构建/严格 reconcile/启动 overlay，扩展目标而非复制宿主文件；现有 amagi-* 所有权是明确例外。登录 contextWindow 是窄字段补齐，不变成内置 provider 全量托管。
- **现有 wslsetup**：只负责 CLI/依赖安装；配置供给由同一协调入口处理。停止 cp-r 播种不删除已经存在的旧副本，旧副本全部按未托管现状识别。
- **现有面板**：无 target API 继续 Windows 语义；新增目标必须显式传入。绑定通过生成，前端经 api wrapper 使用，不手改 `frontend/wailsjs/`。
- **B7**：关闭保持关闭，五 CLI 清单中的 omp 为排除声明，不是变相新增支持。

## 十一、本轮验证与交付自检

本轮工作目录为 `X:\WorkSpace\amagi-codebox`。执行的是 read/grep/find 只读取证与本文新建，没有 shell、构建、测试或真机 WSL 操作；上游报告中的 PASS 是上游证据，不是本轮复测。

- 输入覆盖：三份必读报告和三个必读源码均完整读取。补充大文件仅按上文列出的范围/符号读取，未宣称完整扫描项目。
- 路径纠正：补查根 `provider_sync.go` 返回 ENOENT 后，经 find 定位为 `internal/launcher/provider_sync.go`，并读取 `provider_harness_sync.go`；补查 `internal/launchplan/plan.go` 不存在后定向核对 `types.go`。不影响必读输入完整性。
- 决策自检：D1–D5 均有至少两个候选、明确推荐和代价；覆盖逐 CLI 与 `.agents` 文件清单、普通文件冲突、转换、失败、接口、分批验收和回滚。
- 安全自检：A2 与普通文件策略隔离；auth 不入通用清单；敏感导出另行同意；旧播种无清单不接管；用户修改不被 mtime 覆盖；远程纯计划不承载副作用。
- 未覆盖验证：CLI 版本兼容、真实 MCP/skill 消费、WSL 性能/权限/并发、前端交互与视觉均未在本轮验证，已落实为各批验收门槛。
- 实际产物：仅 `agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`。未修改项目代码、配置、凭据或其它 artifact；metadata 由 harness 托管。
- 本轮回滚：如不采纳设计，只撤回本文即可；没有运行时迁移。后续实现回滚必须按各批归属与 hash 条件执行，不使用整树覆盖恢复。
