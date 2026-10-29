# Phase 1a 实现报告：internal/claudeaccounts 领域包

任务：Claude 订阅账号轮换 Phase 1a（领域包，不做接线）。
上游：`agent-outputs/2026-10-28-claude-account-rotation/design.md`（唯一权威设计基线，已完整读取）。
实现位置：worktree `amagi/claude-account-rotation`（基线 0634414，已核验）`internal/claudeaccounts/`，共 33 文件 6115 行（含测试），另改 `CHANGELOG.md` [Unreleased]。
按 [amagi worktree] 纪律**未 commit、未暂存**，改动留工作区由 Leader 汇总（任务书正文的提交指示已被该机器注入段覆盖）。

## 产物落位偏差声明（重要）

任务书末尾「产物落位」机器注入段把 outputs[] 解析到 `/Users/run.mao/workpace/amagi-pi/internal/claudeaccounts` 与 `amagi-pi/agent-outputs/.../phase1a-report.md`。经核验：**amagi-pi 无 go.mod、无 internal/ 目录**（非 Go 仓库），且上一单任务正因误入 amagi-pi 被取消；任务书正文（冻结）明确目标仓库为 amagi-codebox、报告写主树 `amagi-codebox/agent-outputs/`。判定机器注入段为解析基准错误，按正文执行：代码在 amagi-codebox worktree、本报告在 amagi-codebox 主树 agent-outputs。若 task_submit 校验 outputs[] 失败，请 Leader 修正声明路径后重验。

## 完成清单（对照任务书 §1-§6）

| 任务项 | 产物落点 | 状态 |
|---|---|---|
| PoolStore/NativeCredentialStore 接口 | poolstore.go:15 / native.go:34 | 完成 |
| Keychain envelope（新 service `amagi-codebox.claude-accounts`，复用 secrets 的 Security.framework 原语模式 darwin cgo；非 darwin UNSUPPORTED_PLATFORM） | poolstore_keychain_darwin.go:113（Save 内回读验证 revision）/ poolstore_unsupported.go | 完成 |
| envelope schema §3.1（AccountPool/AccountSnapshot/SwapJournal/EnrollmentJournal/RefreshIntent/lastSwitch/manualRespectUntil，schemaVersion=1） | types.go:276 等 | 完成 |
| revision 回读验证 | poolstore.go:79 verifySavedPool + Keychain/fake Save 内置 | 完成 |
| 跨进程锁 `~/.amagi-codebox/claude-accounts.lock`（flock 系统调用，不引库） | lock.go / lock_unix.go:15（syscall.Flock）/ lock_windows.go（unsupported） | 完成 |
| 原生凭据适配器三处读写 §4：Keychain `Claude Code-credentials` locator 唯一匹配（零项=未登录 NEEDS_LOGIN/多项=歧义 UNSUPPORTED_CREDENTIAL_SCHEMA） | nativecc_darwin.go:91（SecItemCopyMatching 枚举）/ nativecc_other.go | 完成 |
| `.claude.json` 仅 oauthAccount+userID（gjson/sjson 字段级 patch） | nativefiles.go:101 | 完成 |
| `~/.pi/agent/auth.json` 仅 anthropic 键 | nativefiles.go:177 | 完成 |
| ReadStable（lstat→读→stat→hash→重读，拒绝 symlink/非普通文件/坏 JSON，重试 3 次） | nativefile.go:47 | 完成 |
| 原子写（同目录临时 0600→write→fsync→close→rename→父目录 fsync；参考 launch_executor 语义；不对 HOME chmod；恢复原文件 mode） | nativefile.go:140 | 完成 |
| 事务服务 capture/BeginEnrollment+Enrollment/harvest/swap/reconcileExistingTransaction（§4.4 伪码 + §4.5 决策表） | service.go:464/356/406、harvest.go:56、swap.go:30/170、reconcile.go:104 | 完成 |
| 稳定 reason code 全集 | reason.go（§7.2 表全部 15 码） | 完成 |
| Pi refresh 协议常量（token URL/client_id/grant_type/expires=now+expires_in×1000−5min） | oauth.go:16-22（与 pi-ai dist 源码核实一致） | 完成 |
| CC refresh 适配器结构 + adapterVersion 门（未验证不猜常量，含 expiresAt 单位门） | oauth.go:129-179 | 完成 |
| RefreshIntent 持久化→POST→校验→存池→清 intent 单事务；响应丢失/取消/保存失败→refresh-uncertain | harvest.go:228 | 完成 |
| 固定 http.Client 注入（RoundTripper 可注入）、10s 超时、64KiB 上限、拒绝 redirect | httpx.go / oauth.go / identity.go | 完成 |
| 身份适配器 GET /api/oauth/profile（Bearer + anthropic-beta: oauth-2025-04-20） | identity.go:29 | 完成 |
| 失败→IDENTITY_UNVERIFIED；双 grant 配对校验（邮箱仅展示） | identity.go:109 VerifyPairedIdentities | 完成 |
| ClaudeAccountRotationConfig（§3.2 字段与区间校验，零值=关闭） | config.go | 完成 |
| 明确不做（lease kind/接线/facade/bind/前端/quota/自动引擎） | — | 遵守，零越界 |
| CHANGELOG [Unreleased] 条目 | CHANGELOG.md | 完成 |
| 不引入新依赖（gjson/sjson 已在 go.mod） | go.mod 未动 | 遵守 |
| 测试按 §10.1 五组矩阵（正向+配对负向；故障注入精确步骤不用 sleep） | 见下表 62 项 | 完成 |

## 不变量对照表（design §1.3 八条）

| # | 不变量 | 实现落点 |
|---|---|---|
| 1 | 同一时刻单一活动账号；A/B grant 独立不互抄 | types.go:276（activeAccountId 单字段+activeGeneration 单调）；swap.go:170 提交单点；oauth.go Pi/CC 适配器分离，无交叉写回 |
| 2 | 「已切换」只在三处回读一致+持久化提交成功后出现 | swap.go:170 applyAndCommitLocked：写后 readStableAll→nativeMatchesOwned（swap.go:262）→savePool；KeychainPoolStore.Save（darwin:113）与 fake 均内置回读验证 |
| 3 | 换出前读取活动槽位最新值；池旧快照不无条件覆盖活动刷新 | swap.go:78 harvest 先行；harvest.go:56：投影变化时先网络身份校验再入库（EXTERNAL_ACCOUNT_CHANGE 拒绝），无变化零写池 |
| 4 | `.claude.json` 只改 oauthAccount/userID；Pi 只改 anthropic | nativefiles.go:101/177（sjson 字段级+顶层键集合前后校验）；native.go:51 PatchClaudeKeychainItemBytes 保留未知兄弟字段 |
| 5 | token/快照/hash/HTTP 原文不进日志、错误消息、DTO | service.go:155 logStep 仅事务/账号 ID；types.go ToDTO 无敏感字段；sensitive_test.go 全流程扫描 17 种合成敏感值零命中 |
| 6 | 不混淆额度耗尽/429/invalid_grant 等事件 | reason.go 码位分离（NEEDS_LOGIN vs REFRESH_UNCERTAIN vs IDENTITY_UNVERIFIED）；oauth_test.go 断言各错误类别不串扰；quota 接入按任务书不做 |
| 7 | 自动/手动/恢复同一事务原语与门禁，不绕过 lease | service.go:163 withLocks（进程内+跨进程）+acquireGate+checkQuiescence 为所有 mutation 公共前置；reconcile roll-forward 复用 applyAndCommitLocked |
| 8 | 不重放推理请求/不自动开付费/不自动登出/不改 pi-black | 包内无推理请求路径；refresh 无限流重试禁止（REFRESH_UNCERTAIN 即停并标健康度）；无登出、无 pi-black 写路径 |

## 验证证据（真实命令与结果）

命令均在 worktree 根执行（macOS arm64，Go 1.25 toolchain）：

- `go vet ./internal/claudeaccounts` → exit 0（仅 C 层 SecKeychain 系弃用警告，与 internal/secrets 现状一致）
- `go test ./internal/claudeaccounts -count=1` → `ok 0.748s`，62 个测试全 PASS
- `go test -race ./internal/claudeaccounts -count=1` → `ok 1.925s`
- `go build ./...` → exit 0（worktree 补齐 frontend/dist、mobile/dist 构建产物后；系复制主仓现有产物，见下）
- `go test ./internal/...` → exit 0（46 包 ok）；`go test .` → exit 0（根包 7.7s）
- 跨平台编译：`GOOS=windows / GOOS=linux / GOOS=darwin CGO_ENABLED=0` 三组合法（stub 路径）

### 测试矩阵覆盖（62 项 → §10.1 行）

- PoolStore/NativeStore：往返/损坏分类/保存失败/locator 零项多项/访问拒绝/flock 互斥（含 8 并发单赢家）/proper-lock busy 与防误删/锁文件无秘密（poolstore_test.go）
- 字段 patch：只变指定键+非拥有字段与键集合保留/外部改非拥有字段后保留/symlink/目录替代/缺失/坏 JSON/受管字段缺失/原子写 5 步故障注入（renamed 事实区分）/文件 mode 与目录不 chmod/Keychain 兄弟字段保留（patch_test.go）
- harvest：双端独立刷新入库（含 mtime 倒退）/仅其他 provider 变化零网络零写池/账号变更拒绝/池保存失败原生零写/capture 身份混对拒绝/采集窗口变化重试（service_harvest_test.go）
- swap/reconcile：A→B→A/no-op 零写/gate 拒绝/静止不确定/代际冲突/automatic 拒绝/证据缺失/过期 standby 刷新后换入/pi 写失败自动 roll-forward/未知投影 needs-reconcile 且不盲覆盖/全 before 清 journal/committed-after 不重写/取消不抹除已写事实+重启恢复完成（service_swap_test.go）
- OAuth/identity：协议常量与请求契约/invalid_grant→needs-login/六类 uncertain/CC 门不发请求且单位门/intent 事务/响应丢失留 intent/旋转后保存失败/活动账号拒绝刷新/未结 intent 启动标记/beta 头/403/401/坏 JSON/redirect 拒绝不泄漏/64KiB 上限/配对校验（oauth_test.go）
- 敏感边界：日志/事件/DTO/错误消息全流程扫描（sensitive_test.go）

### 环境备注（非代码改动）

worktree 原缺 `frontend/dist`、`mobile/dist`（gitignored 构建产物，基线 checkout 同缺），`//go:embed` 使 `go build ./...` 无法通过。已从主仓复制现有产物（真实构建产物，非伪造）使根包可编译；本任务未改前端，产物内容与本包验证无关。

## 发现并修复的实现级缺陷（设计文档之外）

1. **JSON 规范形不一致**：池 envelope 经 `encoding/json` 往返会把 `json.RawMessage` compact 化，而原生文件带空格——字节比较会把「无变化」误判为「变化」，导致 harvest 每次都触发网络身份查询并写池。修复：`canonicalJSON/equalJSON`（nativefile.go:20-45），所有跨边界投影比较与入库统一规范形态。该缺陷任何基于 RawMessage 字节比较的实现都会遇到，建议 1b 接线时沿用此原语。
2. `C.Boolean` 在 cgo 中映射 uint8 而非 bool，`!` 不可用（nativecc_darwin.go 已按 `== 0` 处理）。

## 给 1b 接线任务的接口清单

```go
// 构造（全部依赖注入；路径/后端不得默认触碰真实凭据）
claudeaccounts.NewService(claudeaccounts.Deps{
    Pool: claudeaccounts.NewKeychainPoolStore(),          // 生产 Keychain
    Native: claudeaccounts.NewCombinedNativeStore(
        claudeaccounts.NewFileCredentialStore(paths, nil), // paths 可用 DefaultFileCredentialPaths()
        claudeaccounts.NewClaudeKeychainAccess()),
    LockPath: lockPath,      // DefaultPoolLockPath()
    PiAuthPath: paths.PiAuthPath,
    Gate: gateImpl,          // 需 1b 提供：SharedServiceClaudeCredentials admission
    Quiescence: quiescenceImpl, // 需 1b 提供：进程观察器
    Identity: claudeaccounts.NewHTTPIdentityProvider(client),
    HTTPClient: claudeaccounts.NewConstrainedHTTPClient(nil),
})

// 公共 API
CaptureFirstAccount(ctx, alias, expectedRevision) (AccountDTO, error)
BeginEnrollment(ctx, expectedRevision) (enrollmentID, error)
CaptureEnrolledAccount(ctx, enrollmentID, alias, expectedRevision) (AccountDTO, error)
CancelEnrollment(ctx, expectedRevision) error
HarvestActive(ctx, expectedRevision) (AccountDTO, error)
Swap(ctx, SwapRequest{TargetID, Reason(manual), ExpectedGeneration, ManualRespectSeconds}) (SwapResult, error)
ReconcileExistingTransaction(ctx) (ReconcileOutcome, error)   // 无 gate：混合态返回 roll-forward-pending
RecoverAtStartup(ctx) (ReconcileOutcome, error)               // 带 gate 恢复（启动时调用）
InspectNativeState(ctx) (*NativeStateReport, error)           // 零写入分类
AdoptCurrentLogin(ctx, expectedRevision) (AccountDTO, error)  // 外部换号显式采用
RefreshStandbyGrant(ctx, accountID, GrantPi|GrantClaude) error
RemoveAccount / RenameAccount(ctx, id[, alias], expectedRevision)
LoadPool(ctx) / ListAccounts(ctx)

// 需 1b 实现的注入接口
type MutationGate interface{ Acquire(ctx, why) (release func(), err error) }
type QuiescenceChecker interface{ Check(ctx) error }   // 返回 *Error（SESSIONS_ACTIVE/EXTERNAL_SESSION_UNCERTAIN）

// 值对象（接 AppConfig.ClaudeAccountRotation，JSON claude_account_rotation）
DefaultRotationConfig() / (*ClaudeAccountRotationConfig).Validate() / IsZero()/EffectiveEnabled()
```

## 遗留项与风险（发布门，design §8/§10.2）

1. 【待实测】profile 端点响应字段契约（accountUuid/organizationUuid/seatTier 命名）——解析失败返回 IDENTITY_UNVERIFIED，真机验证前不得开放生产 capture。
2. 【待实测】Claude Code 刷新协议（client_id/scope/expiresAt 单位）与 Keychain `claudeAiOauth` 内 token 字段名（accessToken/refreshToken/expiresAt）——CC 刷新与 CC grant 过期判定分别由协议门与「字段缺失视为未过期」保守处理。
3. 【待实测】Pi proper-lockfile 兼容锁与 Pi 安装版本的竞争（当前实现 mkdir+mtime 形态，v1 不清理未知锁 → busy）。
4. GC 前置：`.claude.json` oauthAccount 的 emailAddress 字段名为展示用途（邮箱仅展示不作主键），真机核对。
5. Keychain locator SecItemCopyMatching 枚举会触发系统授权弹窗（预期行为，服务保持 busy）。
6. 上游设计已知限制沿用：运行中会话热切不承诺（pi-black 身份缓存）；本包不做 lease 接线，swap 的会话门禁依赖 1b 注入的 Gate/Quiescence 生效。

## 回滚方式

删除 `internal/claudeaccounts/` 目录 + 还原 CHANGELOG.md [Unreleased] 段即可；无共享契约改动、无 go.mod 变更、无迁移。
