# G0 平台提交原语与 A2 消费/取消闭环 — 实施报告

- 任务：`attack-platform-assignment.md`（worktree `wsl-goal-platform-auth`，初始 HEAD `58e7577` + `resumed-input.patch` 导入）。
- 产物：`attack-platform.delta.patch`（仅本切片净增量，22 个文件，3100 行）。
- 上游输入：`attack-contract.md`（完整读取）、`phase-rereview.md`（完整读取）、`resumed-input.patch`（机械应用）。
- 本报告目录（主树）：`/Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/wsl-goal-closure/`。
- 测试日志：worktree `.amagi/attack-platform/`（含 `import-snapshot.patch` 与全部验证日志）。

## 一、交付物 → 产物映射

### 1. internal/platform 冻结 API（全部按冻结签名实现）

| 冻结接口 | 落点 | 说明 |
|---|---|---|
| `WSLTargetIdentityContext(ctx,distro)` | `wsl_context.go`（新） | 复用 `wslIdentityScript` 与 `parseWSLUserIdentity`；ctx 先查后跑 |
| `WSLFileMTimeContext(ctx,distro,path)` | `wsl_context.go`（新） | 可测核心 `wslFileMTimeContextForOS`（与旧 `wslFileMTimeForOS` 同型；非 windows 恒 false） |
| `WSLFileModeContext(ctx,distro,path)` | `wsl_context.go`（新） | 可测核心 `wslFileModeContextForOS`；解析复用 `parseWSLStatModeOutput` |
| `WSLChmodContext(ctx,distro,mode,paths...)` | `wsl_context.go`（新） | 脚本构造与旧 `WSLChmod` 共用新提取的 `buildWSLChmodScript`（`wsl_home.go`，语义零变化：mode 硬校验、单引号转义、空路径跳过 runner） |
| `MoveNoReplace(source,destination)` | `move_no_replace.go` + 分 OS 文件 | 入口校验（空路径/同路径）→ `moveNoReplaceOS` → 成功后验（source 消失/dest 在场）与冲突后验（ErrExist 时双方实体在场，任何一方丢失报深度异常） |
| `EnsurePrivateDir(path)` / `EnsurePrivateFile(path)` | `ensure_private.go` + `_unix.go`/`_windows.go` | 共享编排（绝对路径校验、前后双查符号链接、类型检查）+ per-OS 实施与验证 |

**ctx 真实性**：`wslScriptRunnerContext`（`wsl_context.go` 包级 var）= `exec.CommandContext("wsl.exe", "-d", distro, "--exec", "sh", "-lc", script)` + `SuppressConsoleWindow`，保留 `--exec` 与 quoting；无 goroutine 假取消。旧 `wslScriptRunner` 与四个无 ctx 函数原样保留（wslsupply `DefaultProbes` 等既有消费者不受影响）。测试 `TestWSLContextRunnerHonorsCanceledContext` 以 PATH 上的可执行替身证明取消 ctx 在 Start 前生效（不执行任何进程）。

**MoveNoReplace 分 OS（build tags，无 runtime.GOOS 分支）**：
- darwin：`unix.RenamexNp(..., RENAME_EXCL)`；
- linux：`unix.Renameat2(AT_FDCWD,..., RENAME_NOREPLACE)`；ENOSYS/EINVAL/ENOTTY 包装为 `ErrMoveNoReplaceUnsupported`（不降级覆盖）；
- windows：`windows.MoveFileEx(src,dst,0)`——不带 `MOVEFILE_REPLACE_EXISTING`/`COPY_ALLOWED`（目标存在 → ERROR_ALREADY_EXISTS，跨卷不静默复制）；
- 其它 OS：显式 `ErrMoveNoReplaceUnsupported`。
- ErrExist 经 `errors.Is` 可判（unix `syscall.Errno.Is` / windows `Errno.Is` 原生支持）。禁止项核验：无 Lstat+Rename、无 `mv -n`、无 WriteFile(O_EXCL) 半文件发布。

**EnsurePrivate 威胁模型（明示）**：允许主体 = 当前用户 + LOCAL SYSTEM + 内建 Administrators（Windows 文件所有权/备份语义的标准伴生主体）；其它本机用户零访问。POSIX 侧以 owner-only 0700/0600 表达。实现为「设置并验证」：unix chmod 后 Lstat 复验 mode 位；Windows `SetNamedSecurityInfo`（显式 owner-only DACL，`PROTECTED_DACL_SECURITY_INFORMATION` 阻断继承）→ `GetNamedSecurityInfo` 读回逐 ACE 验证（owner 必须当前用户；每个 ACE 必须是 allow-ACE 且 SID ∈ 允许集合）。错误信息不含 SID/DACL 内容（无凭据性材料）。私有文件缺失时按私有目录语义建父级后 `O_CREATE|O_EXCL` 0600 创建。符号链接在共享入口（前后各一次）与 per-OS 收尾复检三处拒绝（chmod/ACL 按名操作会跟随链接）。

### 2. internal/launcher A2（PR02/PR07 闭环）

- `WSLAgentTarget` 新增 `UID, MachineID string`（保留 Distro/Home/LinuxDir/UNCDir）。
- `SyncWSLPiLoginAuthContext(ctx, target)`（`wsl_pi_login_sync.go` 重写核心）：
  - **#2 已解析 target**：`verifyA2Target` 校验结构（LinuxDir == Home/.pi/agent、UNC 映射一致、UID/Home 非空），再以 `wslTargetIdentityContextFn`（= `platform.WSLTargetIdentityContext`）当次探测核对 UID/Home/MachineID 三者相等；全程不调 `wslUserHomeFn`（cachedHome 仅存于旧 resolve 路径）。失配/不完整 → NoAuth + 零写入（`TestA2TargetIdentityMismatch` 6 形态）。
  - **#1 统一消费门** `finishA2Consumption`：Copied/Fresh/DistroNewer/DistroTieKept/DistroHasCopy 全部出口汇入；门序 = ctx → Lstat 常规文件 → ReadFile 可读 → `wslRetighten600Context`（健康 600 零重写；unknown/宽权限 chmod600 后复验；仍不可证 → NoAuth）。同步 cause 与权限 cause `errors.Join` 并存；`errWSLAuthLooseLeftover` 以 `%w` 链入（取消经 `errors.Is(err, context.Canceled)` 可判）。**取消一律 NoAuth，不按存在降为 HasCopy**；成功回写后的门失败不回滚新 token，错误链携带方向性提交说明（"forward copy to distro committed"/"windows-side writeback committed (new token preserved, not rolled back)"）。
  - **#3 ctx 贯通**：identity/mtime/stat/chmod 全部经 ctx 接缝（接缝类型升级为 ctx 形态，生产指向 platform ctx 变体）；`writeWSLAgentFileStagedContext` 在 MkdirAll/chmod/staging 验证/写候选/chmod600/mode 复验/**候选字节读回比对**后各查 ctx，**实际 rename 前最后一次检查**（取消时未提交侧原样，staging 临时文件清理）；`writeWindowsAuthAtomicContext` 同构（EnsurePrivateDir → 候选 → 读回比对 → rename 前 ctx → rename → EnsurePrivateFile 验证）。持续污染由候选读回预验拦截，原 auth 不变。A2 不建旧 token 回滚链；错误/日志不含 token 或 auth hash（SHA-256 仅内存比对）。
  - **#4 兼容**：`SyncWSLPiLoginAuth(distro)` 保留为兼容包装（resolve + 身份补齐 → Context 入口，同样过门，无权限旁路）；`wsl_agent_config.go` 的 `WriteWSLPiAgentConfig/WriteWSLOmpAgentConfig/WriteWSLOpenCodeAgentConfig`、`wslRetighten600`、`wslEnsurePrivateStaging`、`writeWSLAgentFileStaged` 全部保留为 Background 包装，pi/omp/opencode 旧路径行为零变化（既有测试全部通过）；`wsl_opencode_mcp.go` 未动。
- **#5 G1 接线清单**（本切片不实现，不以缺接线宣称 PR02/07 全关）：
  1. `A2Adapter.Sync(ctx, target TargetRef)`：从 Coordinator 已解析 target 构造 `launcher.WSLAgentTarget{Distro, Home, LinuxDir, UNCDir, UID, MachineID}` 调 `launcher.SyncWSLPiLoginAuthContext`；不得用 cached `WSLUserHome` 重选目标。
  2. pending 摘要：A2 首个改 auth/权限动作前经 wslsupply pending 存储落不含 hash/token 的摘要（OpID/完整目标/方向阶段），Kind 独立；`recordA2State` 保存失败返回 observation。
  3. `classifyA2` 已按 outcome 映射（`a2.go` 未动，现兼容面成立）：不接受「成功 outcome + cancellation error」——本切片保证取消时 outcome 必为 NoAuth。
  4. `verdictForWSLLoginSupply`（`pi_login_launch.go:175-211` 早退序）与 App shared gate 聚合归 G1。

## 二、验证证据（真实命令与结果）

本机 darwin/arm64，go1.27.2；全部 cwd=worktree；退出码经落盘/`$?` 直读（无管道假象）。

| 验证 | 命令 | 结果（日志） |
|---|---|---|
| 导入 | `git apply --check` + `git apply resumed-input.patch` | 60 files +5470/−749，快照存 `.amagi/attack-platform/import-snapshot.patch` |
| 目标包测试 | `go test ./internal/launcher ./internal/platform -count=1` | ok×2（`final-target-tests.log`，该轮无失败）；此前一轮（worktree `.amagi/attack-platform/target-tests.log`）platform 曾现 `TestDarwinResolveExecutableFindsCodexInLocalNodeBin` 基线宿主污染（phase-rereview §4 已记录的 9 项之一；resolver.go/codex_home.go 本轮与导入补丁均未触碰；resolver_test.go 属独立测试隔离任务，未动），最终轮未复现 |
| race | `go test -race ./internal/launcher ./internal/platform -count=1` | ok×2（`final-race.log`） |
| vet | `go vet ./...` | 0（`final-vet-all.log`；cgo 弃用告警为既有噪音） |
| Windows 编译 | `GOOS=windows GOARCH=amd64 go build ./...` | 0（`final-windows-build.log`） |
| Windows 测试编译 | `GOOS=windows GOARCH=amd64 go test ./internal/platform ./internal/launcher -run '^$' -exec /usr/bin/true` | ok×2（`final-windows-test-compile.log`） |
| 下游兼容 | `go test ./internal/wslsupply -count=1` | ok（`final-wslsupply.log`；a2.go 兼容包装路径成立） |
| **Delta round-trip** | 模拟主树（HEAD + 导入补丁 + 主树全部未跟踪文件）`git apply --check` + `git apply attack-platform.delta.patch` | 应用成功；22 文件抽样逐字节 `cmp` 全 MATCH；`go build ./internal/...`、两包测试、Windows vet 全绿（`sim2-*.log`） |

### 新增测试矩阵（要点）

- `internal/platform/move_no_replace_test.go`：目标存在（文件/非空目录/**空目录**）绝不替换且双方保留；目标不存在原子移动（文件/非空目录树）；**16 并发竞争单一目标恰好一个赢家、失败者 source 全保留、目标字节=某完整写者**；source 缺失不创建目标、空路径/同路径拒绝。
- `internal/platform/ensure_private_test.go`：建立/收紧/验证（目录 0700、文件 0600、收紧不动字节）、目录/文件类型不符拒绝、**符号链接（含 dangling）拒绝且链接目标不被跟随改写**、相对路径拒绝。
- `internal/platform/wsl_context_test.go`：取消 ctx 在默认 runner Start 前生效；取消 ctx 下四个探测入口零 runner 调用；解析语义与旧函数一致（身份/mtime/mode/chmod 脚本逐字一致、fail-closed）；runner 错误传播。
- `internal/launcher/a2_consumption_test.go`：**5 出口 × 5 权限形态（25 格）矩阵**（600 零收紧放行 / 644 收紧复验放行 / 644+chmod 失败 NoAuth / unknown+复验失败 NoAuth / 644 复验仍 644 NoAuth），断言 distro 与 Windows 两侧**文件字节**、收紧次数、`errWSLAuthLooseLeftover` 链、反向/并列**成功回写后门失败不回滚新 token**（字节断言）+ 方向性报告；ctx 取消矩阵（身份探测中/mtime 探测中/distro 候选后 rename 前/Windows 候选后 rename 前（countingCtx 确定性钉点）/前向提交后/回写提交后）——未提交侧原样、提交后如实 NoAuth 且新 token 保留；**持续 staging 候选污染**（每次探测时改写候选字节）原 auth 不变；身份失配 6 形态零写入；兼容包装过门（健康降级/不可证 fail closed/成功路径）。
- 既有测试适配（签名升级为 ctx 形态，断言不变）：`wsl_pi_login_sync_test.go`、`wsl_agent_config_test.go`、两份 windows e2e（仅补 ctx 实参），及主树 review-fix 轮未跟踪的 `wsl_review_fixes_test.go`/`wsl_supply_core_regression_test.go`（回写失败 seam 由「chmod 真实目录 0500」改为 `hostEnsurePrivateDirFn` 注入——新 writer 会先把 0500 收紧回 0700，旧手法不再触发失败；断言语义不变）。

## 三、边界与未覆盖项

1. **Windows/UNC 未真机验证**：`MoveNoReplace`（MoveFileEx 无标志形态、UNC 9P 上目录移动/错误码/并发）、`EnsurePrivate*` 真实 ACL（继承阻断、SID/ACE 形态）、wsl.exe ctx 取消后 Linux 子进程存活/后继写。交叉编译与 `-exec /usr/bin/true` 只证明编译面。
2. **基线宿主污染**：`TestDarwinResolveExecutableFindsCodexInLocalNodeBin` 在本机失败（真实 `~/.local/bin/codex` 存在，resolver 采到宿主真实 HOME 路径）——phase-rereview §4 已记录的基线项，属独立任务写面（resolver_test.go 未触碰，未删/未 skip 断言）。
3. **G1 依赖**：A2Adapter.Sync(ctx,target)/pending 摘要/App gate 聚合/`verdictForWSLLoginSupply` 早退序未接线——全产品 PR02/PR07 关闭以 G1 集成后为准。
4. **delta 中两个「适配 hunk」**：`wsl_review_fixes_test.go`/`wsl_supply_core_regression_test.go` 的 hunk 前像取自主树未跟踪副本（review-fix 轮产物，非本切片内容）；delta 在「主树形态」（含这些未跟踪文件）上 `git apply --check` 通过并全量验证。若在无这两个文件的树上导入，这两 hunk 会失败，其余 20 文件 hunk 独立成立。
5. A2 取消后 wsl.exe 与 Linux 子进程的进程级回收语义（契约「外部命令中取消」行的 Windows 验证项）不在本切片可验证面。

## 四、回滚方式

- worktree 源码：不可整目录 `checkout -- internal/launcher internal/platform`——那会连带回退导入态中 `resumed-input.patch` 对这两个目录既有跟踪文件（`wsl.go`/`wsl_home.go`/`wsl_test.go`/`wsl_agent_config.go` 等）的改动，丢失输入快照。精确回滚：在 worktree 内先 `git apply -R --check attack-platform.delta.patch` 验证通过再 `git apply -R attack-platform.delta.patch`（仅撤销本切片 22 文件净增量，保留 HEAD + 导入态，新增文件随 -R 删除）；或直接丢弃该 worktree（无 commit，无历史污染；重放 = HEAD + resumed-input.patch + delta）。
- 主树产物：删除 `agent-outputs/workflow/wsl-goal-closure/attack-platform.delta.patch` 与本文件即可（不涉及源码/配置/凭据）。
