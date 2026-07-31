# Pi coding agent 兼容性补齐审核报告

## 结论

**不通过**。

当前实现可编译、相关单测和前端构建均通过，但存在 1 个 P0 阻断：`PiPlugins` 服务未加入 Wails `Bind`，Pi 插件页运行时必然无法调用后端。另有多项 P1，涉及 Windows 命令注入、插件管理目录与 CodeBox Pi 运行目录脱节、Pi JSONL 增量元数据丢失、fork/clone 历史重复计费、非 assistant 用量漏计、原生成本币种误判及敏感 headers 明文落盘。

---

## 1. Diff 基线

- 基线：`HEAD`
- 审核时命令：`git status --short`、`git diff HEAD`、逐文件读取全部新增文件。
- 实际工作区与任务描述的“12 个修改文件”不一致：审核时为 **16 个 tracked 修改文件 + 12 个新增代码文件**；另有 5 个未跟踪上游 artifact。
- 本报告写入前的代码变更基线如下。

### 1.1 修改文件（16）

| 文件 | 变更范围摘要 |
|---|---|
| `app.go` | 引入、构造并挂载 `piplugin.Service` |
| `frontend/src/api/index.ts` | 导出 Pi plugin API |
| `frontend/src/stores/plugin.ts` | `pluginEngine` 增加 `pi` |
| `frontend/src/views/ExtensionsView.vue` | Pi tab 与 `PiPluginPanel` 分支 |
| `frontend/src/views/UsageView.vue` | usage 客户端筛选增加 Pi |
| `internal/config/types.go` | `PiCompat`、provider headers/authHeader 及 effective getter |
| `internal/launcher/pi_config.go` | headers/authHeader/compat 透传 |
| `internal/remote/app_interface.go` | 增加 `LaunchPiSession` 接口 |
| `internal/remote/handlers.go` | `/launch-pi` 与 launch-meta Pi 段 |
| `internal/remote/session_launch_types.go` | launch metadata 增加 Pi |
| `internal/remote/websocket_test.go` | mock 补 `LaunchPiSession` 桩 |
| `internal/usage/cost.go` | Pi billable input 分支 |
| `internal/usage/metadata.go` | Pi metadata 行为说明 |
| `internal/usage/service.go` | Pi dedup fallback |
| `internal/usage/sync.go` | Pi 双根枚举、增量同步、provider 归一化 |
| `internal/usage/types.go` | Pi app/dedup 常量 |

### 1.2 新增代码文件（12）

- `internal/piplugin/{executor.go,service.go,service_test.go,source.go,types.go}`：Pi package 后端、源解析与测试。
- `internal/appmeta/pi/{parser.go,parser_test.go}`：Pi JSONL parser 与测试。
- `frontend/src/api/piPlugin.ts`：前端 API 包装。
- `frontend/src/stores/piPlugin.ts`：Pinia store。
- `frontend/src/components/extensions/PiPluginPanel.vue`：Pi package 面板。
- `frontend/wailsjs/go/piplugin/{Service.js,Service.d.ts}`：手写 Wails binding。

### 1.3 必要但未改的关键上下文

- `main.go:49-67`：Wails 服务绑定清单。
- `internal/platform/process_script_windows.go:20-32`：Windows `.cmd/.bat` 通过 `cmd.exe /c` 执行。
- Pi 官方 `docs/session-format.md` 与安装包内 `dist/core/session-manager.js`：session tree、fork/clone、非 assistant usage 的真实 schema。

---

## 2. 验收项映射

| 验收项 | 实现位置 | 当前证据 | 判断 |
|---|---|---|---|
| Pi package 后端与前端链路 | `internal/piplugin/*`、`app.go`、`frontend/src/{api,stores,components}` | Go/TS 构建通过；后端 11 类测试通过 | **未达成**：服务未 Wails bind；运行目录也与 CodeBox Pi 脱节 |
| remote `/launch-pi` 与 launch meta | `internal/remote/handlers.go:23,118-123,225-252` | remote 包测试通过；静态与 Codex handler 对等 | 基本实现，但无端点级测试/L3 |
| Pi usage 同步、去重、计费 | `internal/appmeta/pi/*`、`internal/usage/sync.go:150-181,522-625` | parser 4 组测试与 usage 回归通过 | **未达成**：增量、fork、usage 类型、币种均有实质错误 |
| headers/compat 透传 | `internal/config/types.go`、`internal/launcher/pi_config.go` | 构建通过；无专项测试 | 功能存在，但 secrets 落盘策略不安全 |
| Pi 前端交互 | `PiPluginPanel.vue`、`ExtensionsView.vue` | `vue-tsc` + Vite build 通过 | **未达成**：后端未 bind；未做真实 Wails 交互 |

---

## 3. 问题清单

## P0 阻断

### P0-1：`PiPlugins` 未加入 Wails Bind，Pi 插件页运行时后端对象不存在

- 位置：`main.go:49-67`、`app.go:190,235`、`frontend/wailsjs/go/piplugin/Service.js:8-28`
- 影响：进入 Extensions → Pi 后，`onMounted(loadPackages)` 最终调用 `window['go']['piplugin']['Service']['RefreshPackages']`；但 `main.go` 的 `Bind` 仅到 `app.OpenCodePlugins`，没有 `app.PiPlugins`。因此该页面无法列出、安装、更新或移除任何 Pi package。
- 证据：
  1. `app.go` 仅构造并保存在 `App.PiPlugins`；
  2. Wails 实际暴露面由 `main.go:49-67` 的 `Bind` 决定；
  3. 手写 JS 绑定直接解引用不存在的 `window.go.piplugin.Service`。
- 复现推理：正常启动桌面应用 → 打开“扩展管理”→“Pi”；首次刷新即触发 undefined service 调用。前端构建不会检查运行时 Wails 注册表，因此现有 PASS 证据不能覆盖。
- 修复方向：把服务加入正式 Wails bind，并按仓库约定重新生成 bindings；禁止继续以手写文件替代生成链路。修复后必须做真实 Wails 页面刷新/详情/安装流程冒烟。
- 分流建议：功能链路回 `luban`，生成绑定与前端运行时验证由 `luoshen/wukong` 配合。

## P1 应修

### P1-1【安全，置信度 90%】：Windows 下 package source 可穿透到 `cmd.exe /c`，存在命令注入面

- 位置：`internal/piplugin/service.go:90-101,282-301`、`internal/piplugin/executor.go:31,45-49`、`internal/platform/process_script_windows.go:20-32`
- 影响：Windows npm 全局安装的 `pi` 通常是 `.cmd`。`ProcessRunner` 会将其包装为 `cmd.exe /c <pi.cmd> ...`；当前 `validateSource` 只拒绝前导 `-`、NUL、CR/LF，未拒绝或安全处理 `& | < > ^ %` 等 `cmd.exe` 元字符。恶意/误粘贴 source 可在当前用户权限下执行额外命令。
- 利用条件：Windows；解析到 `.cmd/.bat` Pi shim；用户提交含 shell 元字符的 source，例如无空格的 `npm:foo&calc`。
- 证据：source 被原样放入 `cli.Args`，Windows wrapper 又原样追加到 `/c` 参数；没有专用 cmd escaping 或 allowlist。现有 `TestInstallInvokesPiCLI` 只验证 `npm:foo` 参数形状（`internal/piplugin/service_test.go:247-262`）。
- 修复方向：优先绕开 `cmd.exe /c` 执行可信 Node 入口；否则使用经过验证的 Windows cmd 参数转义，并对 npm/git/local 三类 source 做语法级校验。增加 Windows 元字符回归测试。
- 分流建议：`luban`；安全验证由 `wukong` 补充。

### P1-2：插件服务管理默认 `~/.pi/agent`，但 CodeBox 启动的 Pi 使用隔离 `pi-runtime`，安装结果不会被托管会话加载

- 位置：`app.go:190`、`internal/piplugin/service.go:51-81`、`app.go:1731-1739`
- 影响：`piplugin.NewService("", log)` 在 CodeBox 进程未设置 `PI_CODING_AGENT_DIR` 时管理 `~/.pi/agent/settings.json`；而 `LaunchPiSession` 强制把子进程 `PI_CODING_AGENT_DIR` 指向 `~/.amagi-codebox/pi-runtime`。Pi 的 settings/packages 与 agent dir 绑定，因此面板安装的“全局包”不会出现在 CodeBox 发起的 Pi 会话中。
- 证据：读写服务和启动服务使用两个确定不同的目录；Pi 官方 package manager 从当前 agent dir 读取 `settings.json` 并把实体放在该 dir 的 `npm/`、`git/` 下。
- 复现推理：面板安装 `npm:...` → 默认根出现 settings/entity → 从 CodeBox 启动带 provider 的 Pi → 子进程只读取 `pi-runtime/settings.json`，该包不可见。
- 修复方向：先明确产品语义；若面板管理 CodeBox 托管 Pi，应统一到 `filepath.Join(configDir,"pi-runtime")`，且 CLI 写操作必须显式注入同一 `PI_CODING_AGENT_DIR`。若要同时管理独立 Pi 与托管 Pi，应明确展示 scope 并提供同步/选择，不应静默分裂。
- 分流建议：语义确认给 `fuxi`，实现回 `luban`。

### P1-3：增量解析从 offset 开始后丢失 header 上下文，后续记录的 session/project 元数据错误

- 位置：`internal/appmeta/pi/parser.go:148-158,164-186,228-235`、`internal/appmeta/pi/parser_test.go:117-160`
- 影响：首次全量读取可得到 header 的 session UUID 和 cwd；后续从 `LastLineOffset` seek 后不会再看到首行 header，`headerSessionID/headerCwd` 为空，新增记录退化为“文件名”和编码目录名。一个真实 session 因同步批次不同被拆成不同 session ID，project 维度也不一致。
- 证据：header context 是 parser 调用内局部变量，没有像 Codex parser 一样持久化/预读；增量测试只断言新记录的 `InputTokens` 与 offset，没有断言 `SessionID`、`ProjectDir`。
- 复现推理：先同步包含 header+turn1 的文件，再追加 turn2 并增量同步；turn1 的 session 为 header UUID，turn2 为文件 basename。
- 修复方向：增量调用前读取 header，或把 header context 随 sync state 持久化；补两轮同步后 session/project 保持一致的测试。
- 分流建议：`luban`，测试回 `wukong`。

### P1-4：file-scoped dedup 会把 Pi fork/clone 复制的历史消息再次计费

- 位置：`internal/appmeta/pi/parser.go:247`；Pi 安装包 `dist/core/session-manager.js:1077-1124,1234-1271`
- 影响：Pi 的 `createBranchedSession`/`forkFrom` 会创建新文件并复制原分支/全部非 header entries，entry ID 保持不变。当前 dedup key 为 `hash(filePath, entryID)`，同一历史 API 调用换文件后成为新键，导致 tokens/cost 重复累计。
- 证据：官方 session manager 明确复制原 entry；parser 注释和实现又明确把文件路径纳入 key。当前测试只验证“同 entry ID、不同文件必须不同键”，恰好固化了重复计费行为（`internal/appmeta/pi/parser_test.go:190-226`）。
- 多分支判断：同一文件内不同 branch 的不同 assistant entry 代表实际发生过的独立调用，全部计入合理；问题发生在 fork/clone 将已计费 entry 复制到新文件时。
- 修复方向：dedup 需要识别 `parentSession`/复制 lineage，或使用能跨复制稳定、同时控制 8-hex 碰撞风险的事件指纹；增加 fork fixture 验证总量不增长。
- 分流建议：去重语义可由 `fuxi`确认，代码回 `luban`。

### P1-5：只统计 assistant message，漏掉官方定义为计入总量的 toolResult、compaction、branch_summary usage

- 位置：`internal/appmeta/pi/parser.go:192-201`
- 影响：工具内部 LLM 工作、自动压缩摘要、分支摘要产生的 tokens/cost 全部缺失，复杂长会话会系统性低估。
- 证据：Pi 官方 `docs/session-format.md` 明确：`ToolResultMessage.usage` 可承载 nested LLM usage；`CompactionEntry.usage` 和 `BranchSummaryEntry.usage` “included in session token and cost totals”。当前 parser 仅接受 `type==message && role==assistant`。
- 修复方向：按真实 schema 解析上述三类 usage，并给每个 entry 生成稳定 dedup；新增对应 fixture。
- 分流建议：`luban` / `wukong`。

### P1-6：Pi 原生 `usage.cost` 是美元，但空币种会按 provider 推断，国产 provider 被错误标为 CNY

- 位置：`internal/appmeta/pi/parser.go:69-73,237-259`、`internal/usage/sync.go:593-596`、`internal/usage/provider.go:63-71`
- 影响：Pi 提供非零 native cost 时，GLM/Kimi/Minimax/Qwen 等记录会把美元数值标成 CNY，后续 USD 折算与供应商成本对比约差一个汇率倍数。
- 证据：`@earendil-works/pi-ai/README.md:212` 以 `$${usage.cost.total}` 定义该值；parser 刻意留空 `CurrencyCode`，sync 再调用 `currencyForProvider`，该函数把国产 provider 映射为 CNY。
- 修复方向：Pi 原生 cost 明确写入 `USD`；只有本地价格表回退路径才使用 CodeBox 的 model/provider 币种。
- 分流建议：`luban` / `wukong`。

### P1-7【安全，置信度 90%】：自定义 headers 可携带凭据，却被保存到普通配置和 0644 的 runtime models.json

- 位置：`internal/config/types.go:247-266`、`internal/launcher/pi_config.go:81-85,139-151`、`internal/config/service.go:153,188`
- 影响：若用户在 `Authorization`、`X-API-Key` 等 header 中填入 secret，值会进入 provider config，并原样写入 `pi-runtime/models.json`。目录权限为 0755、文件为 0644；本机其他账号或被导出的配置可读取凭据。该变更扩大了原先 keychain 外的 secret 面。
- 利用条件：header 值含敏感令牌；系统存在可遍历用户目录/读取文件的其他本地主体，或用户导出/分享普通配置。
- 证据：`Headers map[string]string` 属普通 Provider JSON；`BuildPiModelsConfig` 原样透传；写文件没有 0600/目录 0700，也没有 secret 引用或脱敏。
- 修复方向：敏感 header 值进入 secrets/keychain，仅在运行时解析；优先支持 `$ENV` 引用。至少将 runtime agent dir/file 收紧为 0700/0600，并明确导入导出脱敏规则。
- 分流建议：`luban`，安全回归由 `wukong`。

## P2 建议

### P2-1：双根枚举没有做物理路径去重，symlink/别名可造成同一文件重复入库

- 位置：`internal/usage/sync.go:531-541`、`internal/appmeta/pi/parser.go:247`
- 影响：正常默认目录下两个文本根不同；但若 `pi-runtime/sessions` 与默认 root 通过 symlink 指向同一目录，枚举结果会重复。由于 dedup 又包含未 canonicalize 的路径字符串，同一物理 entry 可能产生两个键并重复计数。
- 证据：实现直接 append 两个列表，并以“两个根互不重叠”为假设，没有 `EvalSymlinks`/文件标识去重。
- 修复方向：枚举后按 canonical path 或 inode/file ID 去重；增加双根 alias fixture。

### P2-2：安装对话框给出的 npm 示例不是 Pi 接受的 npm source

- 位置：`frontend/src/components/extensions/PiPluginPanel.vue:172,285-292`
- 影响：占位提示让用户输入 `@scope/pkg`，但 Pi package source 要求 `npm:@scope/pkg`；裸值会被 Pi 当作 local path，常规 npm 安装直接失败。
- 证据：官方 `docs/packages.md` 与当前后端测试都使用 `npm:`；前端不做归一化。
- 修复方向：改提示并在前端/后端明确规范化 npm 输入，或拒绝含糊裸值。

### P2-3：详情扫描与 Pi package-manager 的 manifest 解析不完全一致

- 位置：`internal/piplugin/service.go:447-472`
- 影响：manifest 声明自定义目录或 glob 时，`os.Stat` 遇目录/glob直接跳过，只再扫描四个约定目录；有效 package 的资源详情可能为空或不全。
- 证据：Pi 官方支持 manifest 数组中的目录、glob 和排除规则；当前仅支持直接文件 + convention fallback。
- 修复方向：复用等价 glob/filter 语义，至少递归展开 manifest 目录；增加非约定目录和 glob 测试。

### P2-4：npm 任意版本表达式都被标为 pinned

- 位置：`internal/piplugin/service.go:348-353`
- 影响：`npm:pkg@^1.2.0`、`@latest` 会在 UI 显示 pinned，但 Pi 官方仅 exact semver 视为 pinned；状态展示误导。
- 修复方向：按 semver exact version 判定，不以 `ref != ""` 一概处理。

### P2-5：增量 parser 假设 EOF 总有换行，缺少尾部不完整行保护

- 位置：`internal/appmeta/pi/parser.go:164-175,263-267`、`internal/usage/sync.go:550-625`
- 影响：并发读取或异常中断留下无换行/半条 JSON 时，scanner 仍把 `len+1` 计入 offset；坏行被跳过后该 offset 会持久化，后续补全内容可能被越过。
- 证据：实现无“只提交完整换行行”的判断；报告中“动态尾部下次再读”的结论没有测试支撑。
- 修复方向：仅在确认读到分隔符后推进 committed offset；对未完成尾行保留旧 offset。

---

## 4. 安全审查结论

- Remote：`handleLaunchPi` 的 JSON `DisallowUnknownFields`、字段形状、调用路径与 Codex handler 对等；所有 REST route 仍由服务器 Auth Middleware 包裹。未发现 Pi 特有的新增鉴权绕过。
- Remote 输入：handler 自身与 Codex/OpenCode 一样不做 mode/workDir/shellPath 业务校验，依赖既有 `LaunchPiSession`/launcher。属于基线一致性，不单独定级。
- Package source：Unix 路径使用 argv，不经 shell；Windows `.cmd/.bat` wrapper 是新增链路的实质注入风险，见 P1-1。
- Headers：存在敏感值明文持久化风险，见 P1-7。
- 未发现硬编码凭据、用户名绝对路径或伪数据实现。

---

## 5. 测试证据审核

### 5.1 本次抽检

| 命令 | 结果 |
|---|---|
| `go test -count=1 ./internal/piplugin/... ./internal/appmeta/pi/... ./internal/usage/... ./internal/remote/...` | PASS |
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `npm --prefix frontend run build` | PASS（`vue-tsc --noEmit` + Vite） |
| `git diff --check` | PASS |

### 5.2 证据映射评价

- `internal/piplugin` 用例覆盖 settings 解析、npm/git 实体路径、CLI 参数和 source parser，均映射真实风险；未发现明显“凑数”或冗余测试。
- parser 四组用例覆盖 happy path、追加读取、坏行和跨文件短 ID，但关键断言缺失：
  - 增量测试没有断言第二批记录的 session/project；
  - 跨文件测试把 fork 重复计费固化成预期；
  - 无 compaction/branch_summary/toolResult usage；
  - 无半行、截断、双根 alias、sync_state 全链路测试。
- `internal/usage` 现有 suite 通过，但没有 Pi sync 的 service/DB 级测试，无法证明 `syncPiJSONL` 的 offset、Record、rollup、currency 行为。
- `internal/remote/websocket_test.go:132-134` 只为接口编译补桩；没有 `/launch-pi`、launch-meta Pi 段、鉴权、坏 body 的 handler 测试。
- headers/authHeader/PiCompat 没有 launcher 专项测试。
- 构建通过不能发现 Wails `Bind` 缺项，P0-1 正是证据盲区。

---

## 6. 前端验证与覆盖缺口

- 未执行真实 `wails dev` + agent-browser 交互；当前环境没有可供 agent-browser 连接的桌面 Wails 页面。
- 静态审查确认组件状态流、loading/error、详情与 mutation 基本自洽，TypeScript 构建通过。
- 但真实交互缺口**不可接受为放行证据**：P0-1 会使页面首次加载即失败，且 npm 示例也会把正常用户带到失败路径。
- 修复后最小 L3：打开 Pi tab → 列表/空态 → 选择详情 → 安装 `npm:` source → 更新 → 移除 → 重新启动 CodeBox Pi 验证 package 实际加载；同时核对截图中的布局、状态文案和错误态。

---

## 7. 建议下一步

1. 先修 P0-1，并决定 P1-2 的 agent-dir 产品语义；未完成前不应进入前端验收。
2. usage 回流 `luban`：修 P1-3/P1-4/P1-5/P1-6，并由 `wukong`补 DB 级增量/fork/币种测试。
3. 安全回流 `luban`：处理 Windows argv/cmd 转义与 header secret 持久化。
4. 重新生成 Wails binding，完成真实桌面交互和一次真实 Pi session → usage sync 端到端验证。
5. diff 发生变化后，本报告结论失效；复审只审修复增量。
