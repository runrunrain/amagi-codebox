# Phase 1b 实现报告：lease 接线 + App facade + 配置持久化 + 启动恢复

任务：Claude 订阅账号轮换 Phase 1b（接线）。上游设计：`agent-outputs/2026-10-28-claude-account-rotation/design.md`（§6.3/§7/§3.2/§4.5 已完整读取）；1a 接口清单：`phase1a-report.md`（接口形态逐项核对一致）。
实现位置：worktree `amagi/claude-account-rotation`（基线 73cff33 含 1a，HEAD 已核验）。按 [amagi worktree] 注入段**未 commit**，改动留工作区由 Leader 汇总。

## 一、设计 §6.3 接入表逐行对照（文件:行号）

| 接入位置（design 原行） | 实现落点 | 状态 |
|---|---|---|
| control_types.go:381-386 增凭据 kind，旧枚举值不动 | `internal/remote/control_types.go:386`（`SharedServiceClaudeCredentials` 追加在 CodexHeadroom 之后，1/2 不变） | ✅ |
| launchplan/types.go:56-60 | `internal/launchplan/types.go:61`（`SharedClaudeCredentials`=3） | ✅ |
| shared_coordinator.go:171-175 白名单扩凭据+Headroom；卸载 drain 仍限 Headroom | `internal/remote/shared_coordinator.go:44` `isKnownSharedKind`（admission 白名单）；drain 检查保持 `isHeadroomKind`/显式两 kind（凭据不参与 drain，drain 的 empty 判定也忽略凭据 lease）；凭据 admission 强制 config-bound（:171-174） | ✅ 测试 `shared_coordinator_credentials_test.go` 6 项 |
| app.go:2441 Claude 启动前取 admission/复核/转 lease | claim `app.go:2574`（认证读取/配置写入/副作用前）；WSL spec 定谳后释放（:2715）；embedded 提升 `app.go:2742`（`launchEmbeddedPTYWithClaims`，提升时重算指纹=PTY 前复核）；外部 spawn 前复核 `app.go:2799`、成功后提升 `app.go:2917-2937`（失败保守停进程）；HOME 重定向拒绝/排除（:2584-2599）；受管 OAuth 身份边界 `managedClaudeIdentityBoundarySpec`（:2727） | ✅ |
| app.go:3555 Pi 启动同构 | claim `app.go:3744`（models.json 写入/供给之前）；WSL 登录 v1 拒绝（:3748）；WSL API 目标释放（:3908）；embedded 提升 `app.go:3948`；外部复核 `app.go:3984`、提升 `app.go:4006`；退出释放补齐（embedded :3972 / external :4032） | ✅ |
| launch_planner.go:254 / pi_login_launch.go:337-411 纯读计划携带 admission 描述 | `launch_planner.go:349`（Claude）、`:872`（Pi API，OMP 不带）、`pi_login_launch.go:394`（Pi 登录；WSL+受管 → BuildFailure）。BuildPlan 零副作用（接缝 `claudeCredentialAdmission` 纯读，`launch_planner.go:896`） | ✅ |
| remote_session_adapter.go:545/560/771-780/1019/1122 kind 映射与 admission 传递、代际转移、启动前代际变更重新规划 | kind 映射 `:561`（sharedKindFromPlan）；create 复核 `:574`（`verifyCredentialPlanFreshness`，stale → `errSharedCredentialPlanStale` 失败返回，客户端重发即重新规划）；restart 循环同款复核（~:1046）；lease 提升（771-780/1122 原位）经 `acquireLease` configBound 指纹匹配兜底 | ✅ |
| launch_executor.go:98-115 与 PTY 前边界 | executor 不新增凭据逻辑（无新公开 effect DTO，禁令遵守）：匹配凭据 admission 的持有/ready/generation 校验由 adapter step5（admission+新鲜度复核，先于 Prepare/effects）与桌面 `launchEmbeddedPTYWithClaims`（lease 提升在 `Pty.Start` 之前、指纹重算即代际复核）承担；admission 持有期间 mutation 被阻塞，无代际漂移窗口 | ✅（经注入边界实现） |
| headroom_facade.go:61-158 记账/转移惯例 + exact owner/runEpoch 释放 | 凭据 lease 复用 `rememberSharedLease`/`prepareSharedLeaseTransfer`/`releaseSharedLeases(Exact)`/exit/shutdown 链（`control_wiring.go:181` 失败路径、`headroom_facade.go:59` 指纹分支）；外部提升失败保守停进程（"外部窗口失联保持 busy"由进程观察器兜底） | ✅ |
| 保守计入规则（canonical 路径进程全持 lease；判断不明保守计入；可证明独立根排除） | 全部 CodeBox Claude/Pi 宿主根会话持 lease（含 Pi API provider 会话）；WSL 目标（distro 独立根）与 HOME 重定向（独立根）排除；OMP/Codex/OpenCode 不涉及 | ✅ |
| R3 环境边界 | `managedClaudeIdentityEnvKeys`（CLAUDE_CODE_DEVICE_ID/ACCOUNT_UUID/OAUTH_TOKEN/CONFIG_DIR）在 spec.Env 剥除 + POSIX shell `env -u` 重锚（`managedClaudeIdentityBoundarySpec`、`piLoginReAnchorSpec` 变参扩展）；HOME 不可证明 → 拒绝并给出变量名（`app.go:2584`）；不删模型参数 | ✅ |

## 二、任务 A-F 完成对照

| 任务 | 落点 | 状态 |
|---|---|---|
| A 凭据 lease kind（指纹=canonical 路径+activeGeneration，不含 token） | `claude_accounts_launch.go:152` `claudeCredentialsFingerprintFor`（sha256，路径+代际） | ✅ |
| C MutationGate 真实实现 | `claude_accounts_launch.go:272`（包 SharedServiceCoordinator 的凭据 mutation admission，幂等 release） | ✅ |
| C QuiescenceChecker（进程观察器） | `claude_accounts_launch.go:359`（ps 枚举 + claude/pi 可执行名匹配 + 归因闭包 + 存活后代；不可分类/不可归因/扫描失败 → EXTERNAL_SESSION_UNCERTAIN；测试注入 fake 进程表 `claudeAccountsQuiescenceLister`） | ✅ |
| C NewService 装配 + 启动时序 | 装配 `app.go:526`（Keychain 池 + CombinedNativeStore + canonical 路径 + `~/.amagi-codebox/claude-accounts.lock`）；Startup 时序 `app.go:2129`（池加载 → RecoverAtStartup → recovered 置位；needs-reconcile/失败 → 启动警告 + 受影响入口保持关闭） | ✅ |
| D App facade（§7.2 全部 10 方法） | `claude_accounts_facade.go`（GetClaudeAccounts:291 / Begin:397 / Capture:410 / Cancel:428 / Switch:440 / GetOperation:464 / SetRotation:484 / Reconcile:527 / Remove:614 / Rename:625）；operation `{id,kind,stage,state,changed,activeGeneration,code,warnings}` 有界缓存 128；requestId 幂等/冲突；事件只发 operationId/stage/state/generation；reason 码透传（错误文本 `REASON: msg` 前缀） | ✅ 测试 `claude_accounts_facade_test.go` 9 项逐一锚定 |
| E 配置持久化 | `internal/config/types.go:436`（`claude_account_rotation`）；`service.go:318` Load 严格校验（未知 strategy/越界拒绝）；`:1586/:1603` Get/Set（facade 经此写入，账号服务不写 models.json）；GetConfig 指针深拷贝（:755 起）；导入导出不携带（ExportConfig 独立结构 + 测试断言） | ✅ 测试 `rotation_config_test.go` 7 项 |
| F 不做 | 前端 UI/quota/自动引擎未动；WSL：登录会话受管时明确拒绝（文案 `UNSUPPORTED_PLATFORM: …WSL Pi 登录会话暂不支持（v1）`），现有同步逻辑零改动；WSL Claude/API-Pi 按独立根放行不持 lease | ✅ |

### bind_list.go 偏差声明（有依据不执行）
任务 D 要求「bind_list.go 加 facade」，但设计 §7.2 明确「新增根文件 claude_accounts_facade.go，App 持有未导出的领域服务」——facade 方法挂在 App（与 Headroom* facade 同一 C-01 模式），App 本就在 Bind 列表，`bind_list.go` 无需也不应新增绑定对象。已按设计执行：`bind_manifest_test.go` 新增 8 个 facade mutation 的分类断言（`appClassifiedNonSessionMutations`）与 ConfigService 新 Set 方法的登记理由。

### wailsjs 绑定偏差声明
本环境无 wails CLI 且无网络（`go install wails@v2.11.0` 代理重置失败）。已按生成器格式手工补齐 `frontend/wailsjs/go/main/App.js`/`App.d.ts`（10 方法，字母序）与 `models.ts`（namespace main 新增 7 个 DTO 类；字段名遵循 JSON tag 规则），并通过 `vue-tsc --noEmit` 类型闸门（exit 0）。1c 运行 `wails dev`/`wails build` 时将按同一格式再生收敛；再生产物与本手写内容语义一致。

## 三、验证证据（worktree 根，macOS arm64，Go 1.25）

- `go vet ./...` → exit 0
- `go build ./...` → exit 0
- `go test ./internal/remote ./internal/launchplan ./internal/launcher ./internal/config . -count=1` → 全 ok（remote 27.4s / launchplan 1.8s / launcher 2.5s / config 2.4s / 根包 10.3s）
- `go test -race ./internal/remote ./internal/claudeaccounts -count=1` → ok（32.5s / 2.2s）；`go test -race . -run TestFacade|TestClaudeCredentials|TestLaunch|TestQuiescence|TestBindManifest` → ok 4.0s
- 全量 `go test ./... -count=1` → **48 包全 ok，0 FAIL**（1a 基线 46 包 + 新增测试面）
- 跨平台编译：`GOOS=windows` / `GOOS=linux` / `GOOS=darwin CGO_ENABLED=0` 三组 `go build ./...` 全通过（非 darwin 走 unsupported 路径：facade 返回 UNSUPPORTED_PLATFORM、启动门禁短路 absent 零行为变化）
- 前端：`vue-tsc --noEmit` → exit 0（wailsjs 手写绑定通过类型闸门）
- 新增测试 22 项：coordinator 6（admission 白名单/同代际共存/异代际拒绝/mutation 双向互斥/drain 不误伤/外部身份+closed+幂等）、config 7、facade 9（状态投影/requestId 幂等与冲突/lease 下 SESSIONS_ACTIVE/SetRotation 校验/inspect 零写/Remove-Rename/敏感扫描/operation 查询）、launch 8（策略五态/指纹/claim-复核-释放/入口关闭×2/恢复放行/观察器六情形/环境边界）

### §10.1 lease 行覆盖说明
- admission→spawn 间插 swap：coordinator 测试 `MutationBlockedByLeasesAndAdmissions` 方向一（pending admission 阻塞 mutation）+ 桌面链 admission 在一切副作用前获取。
- swap→read 间插 launch：同测试方向二（mutation admission 持有期间 launch admission 拒绝；桌面链 `beginClaudeCredentialsClaim` 返回 SESSIONS_ACTIVE 稳定错误）。
- 早释放：`EarlyReleaseIsIdempotent`（幂等、无悬挂 pending、mutation 立即可入）。
- 退出/代际转移竞态：exit 监视器统一走 `releaseSharedLeases(Exact)`（exact owner/runEpoch）；remote restart 走既有 `PrepareLeaseTransfer` 复合提交（凭据 kind 自动纳入，`acquireLease` configBound 匹配）。
- Headroom drain 不误伤新 kind：`HeadroomDrainDoesNotBlockCredentials`。

### 启动时序验收（任务书专项）
`TestLaunchSession_BlockedWhilePoolNeedsReconcile` / `TestLaunchPiSession_BlockedWhilePoolNeedsReconcile`：needs-reconcile 时真实启动入口（LaunchSession/LaunchPiSession）以稳定 `NEEDS_RECONCILE` 错误关闭；`TestLaunchEntries_ReopenAfterRecovery`：恢复完成（无未结事务）后同一入口越过门禁（claim 成功 + LaunchSession 不再被账号池错误拦截）。`TestClaudeCredentialsPolicy_States` 锁定五态判定。

## 四、给 1c（前端）的接口清单

- 绑定：`frontend/wailsjs/go/main/App` 新增 10 方法（见上文 facade 表；参数顺序与 d.ts 一致）。
- 事件：`claude-accounts:operation`（payload `{operationId,stage,state,generation}`，无敏感字段）与 `claude-accounts:event`（`{kind,generation}`）。
- 错误识别：同步错误文本以稳定 reason 前缀开头（如 `SESSIONS_ACTIVE: …`、`REVISION_CONFLICT: …`）；异步结果用 `GetClaudeAccountOperation` 读 `{state,code,warnings}`；未知 operationId → `OPERATION_NOT_FOUND`（重启后请重读状态）。
- PoolDTO.state 值域：`disabled|enabled|enrolling|needs-reconcile|blocked|unsupported`；capability：`supported|unsupported`。SetRotation 启用需 `expectedRevision`=池 revision、`riskAckVersion`="claude-rotation-v1"。
- SwitchClaudeAccount 需 requestId（幂等键）；CaptureClaudeAccount enrollmentID 空串=首次录入。

## 五、遗留项与风险

1. 【待实测】沿用 1a 发布门：profile 字段契约、CC 刷新协议常量、真机 Keychain locator/授权弹窗、pi-black 身份缓存——本阶段未新增真机验证。
2. 进程观察器匹配为保守规则（可执行/参数 basename 精确命中 claude/pi；`vim claude` 类参数误命中会保守阻塞切换，方向安全）；孤儿后代（父已退）无法追溯，由「匹配进程自身在列」覆盖主路径，文档化于 `claude_accounts_launch.go`。
3. 外部终端模式的用户 shell 内变量覆盖（CodeBox 注入面之外）不可控；R3 边界覆盖 CodeBox 可注入面（spec.Env + POSIX 重锚），外部终端由凭据 lease + 观察器兜底。
4. `GetClaudeAccounts` 每次刷新缓存做一次 Keychain 读（仅 UI 打开 Provider Center 时调用；启动门禁日常判定用内存缓存，单实例 app 内一致性由所有 mutation 后 refresh 保证）。
5. wailsjs 为手写（见偏差声明）；1c 首次 `wails build` 再生后应 diff 确认收敛。

## 六、回滚方式

还原本阶段全部改动（16 修改 + 5 新增文件，见 git status）即可；无共享契约破坏（新枚举值追加、旧值不动）、无 go.mod 变更、无迁移（旧配置无 `claude_account_rotation` 字段=关闭）。1a 领域包与 Keychain envelope 不受影响。
