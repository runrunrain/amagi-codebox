# C1 运行链修复实现报告（修订 2）：WSL 启动 cwd/env 三入口一致

- 基线：HEAD 58e7577（worktree 分支 amagi/wsl-goal-runtime）
- worktree 实际路径：`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/wsl-goal-runtime`（未提交，改动留在工作区由 Leader 汇总）
- 对照审计：`runtime-audit.md` D3（Linux 形态 workDir 被宿主校验链吞掉）、D2（remote/restart 计划链 env 防线缺席）。
- 修订说明：按 Leader 复审意见重做工作目录层——① WSL 目标下**显式非空**请求（含 Windows/相对形态）校验失败不再静默回退，直接拒绝启动；② 空请求 + 配置默认目录为 Linux 形态时同样保留；③ 工作目录的目标判定改为**纯目标判据**（解析产出的 `spec.BootstrapMode` 后置定谳），本层零探测，并撤回上一版报告中不成立的「BuildPlan 纯读不 spawn」证明（见「探测与纯度披露」）。SA09 仍未接入（C2 接口未到，见交接）。

## 修复内容（规格 → 产物映射）

### 1. D3：WSL 目标的工作目录三分支语义（纯目标判据）

**根因**：`resolveLaunchWorkDir`（五 CLI 桌面入口）与 `resolveLaunchWorkDirChain`（remote create / restart 的 `BuildPlan`）统一执行宿主 `Clean+Abs+Stat`，失败静默回退默认目录；PTY 底层虽支持 Linux 路径（`wsl.exe --cd` + `conPtyWorkDirFor` B4 定谳），上游先吞掉。

**修复**（`launch_workdir.go` + `app.go` + `launch_planner.go`）：

- 新增 `resolveLaunchWorkDirForSpec(bootstrapWSL, requested, hostResolved, defaultPath, onWSLPreserve)`——在**解析完成后**定谳，以 resolver 产出的 `spec.BootstrapMode` 为唯一目标判据（纯、零探测）：
  - **非 WSL 目标**：返回 `hostResolved`（入口宿主链产物），原生 Windows/macOS 回退语义与修复前逐字一致；
  - **WSL 目标 + 显式非空请求**：Linux 形态（`/…`、`~`、`~/…`）原样（TrimSpace 后）保留给 `wsl.exe --cd`，存在性由目标 distro 判定（校验发生在正确目标/阶段，目录无效时失败在 wsl.exe 处可见）；**其余形态（Windows 盘符/UNC/相对路径）仍由宿主校验，失败直接报错**「WSL 会话请求的工作目录不可用: …；已拒绝启动（不回退换目录）」——不静默换工程；
  - **WSL 目标 + 空请求**：`defaultPath` 为 Linux 形态时同样保留（用户配置的默认 `~/project` 不被宿主校验吞掉；已核对 `PathsService.GetDefaultPath`/`SetDefaultPath` 与 planner `canonicalWorkdir` 均只透传字符串、不先吞值——吞掉的是链的宿主校验，修在同一定谳层）；否则维持宿主链值（默认目录→Home 既有回退）。
- 接线：桌面 `resolveEmbeddedLaunchSpec` 新增 `requestedWorkDir` 参数，在 `Resolve` 之后调用上述函数并写回 `spec.WorkDir`（五入口同步：`requestedWorkDir := workDir` 捕获原始值、解析后 `workDir = spec.WorkDir` 同步后续 recipe/记录）；remote/restart 在 `BuildPlan` 的 `finalizeWorkdir`（计划构建后遍历 PTY effect）完成同一调用，失败映射 `FailureWorkdir`，成功值写回 `Recipe.Workdir`（会话记录/restart 快照的忠实值）。
- 入口宿主链（`resolveLaunchWorkDir`，回退 WARN 保留）不再承担 WSL 判定，仍是原生目标/会话预留记录/外部终端模式的权威值。

### 2. D2：remote/restart 计划链接入与桌面同一套 env 转发规则

**根因**：`appLaunchPlanner.resolveLaunch`（`app_ai_proxy.go`）缺桌面 `resolveEmbeddedLaunchSpec` 的三条防线（`EnvForwardKeys`/`EnvPathFlagKeys`/`StripWSLHostPathEnvWithAllow`）。

**修复**（`app_ai_proxy.go` + `launch_planner.go` + `app.go` 装配）：
- `launchEnvVarsPort` 扩展 `Keys()`/`PathAllowKeys()`（envvars.EnvVarsService 原生具备）。
- `resolveLaunch` 与桌面同序：networkEnv → 注入 ForwardKeys/PathFlagKeys →（WSL env 门命中时）剥离未放行的宿主盘符路径值 → resolver → `PinProxyEnvironment`。复用既有规则与文案，无新增转发开关；路径值不泄漏。
- 桌面 env strip 门与 planner env 门统一经 `launchTargetsWSLFn`（同谓词同接缝，判定不分叉）。
- 新增 `envObserver`（nil 安全），App 装配接 `app.Log`：remote/restart 的 WARN/INFO 与桌面同文案。

### 3. 探测与纯度披露（撤回上一版不成立的证明）

- **工作目录层零探测**：目标判定唯一来源是 resolver 已产出的 `spec.BootstrapMode`；`isLinuxStyleWorkDirFn` 是纯字符串判据。本切片的工作目录路径不新增任何 wsl.exe 调用。
- **BuildPlan 并非 spawn-free（既有事实，非本切片引入）**：Windows 宿主上 `BuildPlan` 在修复前就会经 `resolveLaunch → resolver.Resolve → resolveShellCandidatePath(wsl)` 触发一次 `wsl.exe -l -q` distro 探测（`availableWSLDistros` 进程内缓存，首调执行、后续命中缓存）——这是解析器判定默认 shell 的**必要既有行为**。本切片新增的 env 剥离门（`launchTargetsWSLFn`）与该探测共享同一缓存：无论剥离门先触达还是解析器先触达，每进程探测次数不变（1 次）。剥离必须在 `Resolve` 之前发生（WSLENV 清单要在解析器内拼好），无法等解析后定谳，故此门保留探测谓词；macOS 宿主上两者均被 runtime 门短路为零调用。
- 上一版报告中「BuildPlan 保持纯读（谓词复用已缓存的探测，无新增 spawn 类别）」的表述已删除：缓存无预热保证时首次探测归属会改变，净次数论据不构成「纯读」证明。正确口径如上两条。

### 4. 测试隔离修正（不删产品代理保护）

- 根因：宿主 shell 继承 `AMAGI_CODEBOX_AI_PROXY=1` + 代理变量 → `MergeWithSystem` 带入 spec → `aiproxy.PinProxyEnvironment` 对 WSL spec 触发 NAT 门 → 四组 WSL 计划/启动测试误失败。
- 修复：`unsetInheritedProxyEnv(t)`（unset marker + 8 个代理键，t.Cleanup 还原）应用于四个原失败测试及新增测试；产品 NAT/mirrored 门零改动。

### 5. 本切片相关平台测试修正（不扩大原生行为）

- `internal/platform/wsl.go`：`WSLFileMTime` 提出可测核心 `wslFileMTimeForOS(osName, …)`（原生行为不变：非 Windows 恒 (0,false)）；测试改经核心驱动注入 runner 矩阵。
- `internal/envvars/wsl_path_allow_test.go::TestSetPathAllow`：大小写断言按 `keysEqual` 平台规则分叉，不改产品语义。

## 新增回归（真实入口层，非仅小 helper）

文件 `launch_wsl_parity_test.go`（+ `launch_workdir_test.go` 链级补充）。WSL 目标由 resolver 测试替身（`piLoginWSLShellResolver` 固定产出 `BootstrapWSL` spec——与生产 `spec.BootstrapMode` 判据同一输入源）驱动；`isLinuxStyleWorkDirFn` 注入镜像 `platform/wsl_form_windows.go` 语义的假实现（生产默认非 Windows 恒 false）。

| 测试 | 入口层级 | 断言 |
|---|---|---|
| `TestBuildPlanPreservesWSLLinuxWorkdir` | `BuildPlan`（remote create + restart 两 Origin） | Linux 形态（含空格中文、`~`、`~/…`、首尾空格）在 `Recipe.Workdir`/PTY `spec.WorkDir` 原样保留；**显式无效宿主形态（`C:\missing\目录`）→ `FailureWorkdir` 拒绝，不静默换工程**；有效 Windows 盘符形态（含空格中文）仍宿主校验并保留 Clean 形态；**空请求 + 默认目录 `~/默认 工程` → 保留**；非 WSL resolver 对照：Linux 形态/无效请求维持宿主回退（原生语义钉住） |
| `TestExecutorConsumesPreservedWSLWorkdir` | 执行器 `Prepare` → PTY effect | 保留的 Linux workdir 原样进入 `ptyStartEffect.spec`，执行段不引入宿主校验/改写 |
| `TestDesktopResolveEmbeddedLaunchSpecWSLWorkdir` | 桌面统一漏斗（五 CLI 内嵌启动共用） | WSL：Linux 请求保留、无效宿主形态拒绝（文案含「拒绝启动」）、空请求 + Linux 形态默认目录保留；非 WSL：返回宿主链值 |
| `TestDesktopLaunchPreservesWSLLinuxWorkdir` | 桌面真实入口 `App.LaunchPiSession` | WSL 目标：Linux 形态请求一路到达 PTY 层（不被目录回退拦截）、无效宿主形态直接拒绝；非 WSL resolver 对照：回退 Home 且到达 PTY（既有行为） |
| `TestPlannerResolveLaunchEnvForwardParity` | `planner.resolveLaunch` + 桌面 `resolveEmbeddedLaunchSpec` 双入口 | 同一输入产出逐字节相同的进程环境；自定义标量保留、`UE_ROOT`（allow）豁免、未放行盘符值（`MY_TOOL_HOME`/`PI_SESSION_FILE`）剥离、Linux 路径值（`OPENCODE_CONFIG`）保留；ForwardKeys/PathFlagKeys 原样传给解析器；observer INFO/WARN 与桌面同文案同顺序 |
| `TestPlannerResolveLaunchNoStripWithoutWSLTarget` | `planner.resolveLaunch` | 非 WSL 目标不剥离（原生对照），转发面接线保持 |
| `TestResolveLaunchWorkDirForSpec`（launch_workdir_test.go） | 定谳函数链级 | 三分支矩阵：保留回调、显式有效/无效宿主形态、Linux 形态默认目录、非 WSL 恒返回宿主链值 |

## 验证记录（真实命令与退出状态）

worktree cwd `/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/wsl-goal-runtime`；Go 1.27.2 darwin/arm64（环境差异与基线一致）。worktree 缺 embed 资产，已从主树复制 `frontend/dist`、`mobile/dist` 到 worktree gitignored 目录仅用于构建（未写主树产品文件）。

| 命令 | 退出 | 结果 |
|---|---|---|
| `go build ./...` | 0 | PASS（仅 secrets 既有 cgo 弃用告警） |
| `go vet ./...` | 0 | PASS |
| `go test . -count=1` | 0 | 根包全量 PASS（宿主环境自带 8 个代理变量） |
| `AMAGI_CODEBOX_AI_PROXY=1 go test . -count=1 -run '（四组原失败 + 全部新增）'` | 0 | 全 PASS——完整复现基线失败环境后仍通过（测试隔离生效，产品 NAT 门未删） |
| `go test ./internal/envvars ./internal/pty ./internal/launchplan ./internal/launcher -count=1` | 0 | 全 PASS |
| `go test ./internal/platform -count=1` | 1 | 仅 `TestDarwinResolveExecutableFindsCodexInLocalNodeBin` 失败——**基线既有**（宿主真实 `~/.local/bin/codex` 干扰 ambient-path 解析，verification.md 已列范围外，任务书未授权该项修正）；`TestWSLFileMTime` 修复后 PASS |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test ./... -exec=true -run '^$'` | 0 | Windows 测试二进制交叉编译 PASS（未运行任何 Windows 测试，与基线口径一致） |
| `gofmt -l/-w`（仅本批 12 个文件） | 0 | 已格式化，未全仓格式化 |

## Changed files

产品：`app.go`（五入口捕获原始请求 + 传递/同步、resolveEmbeddedLaunchSpec 后置定谳与 env 门统一、envObserver 装配）、`app_ai_proxy.go`（resolveLaunch env 防线 + 探测披露注释）、`launch_planner.go`（端口扩展 + envObserver + `finalizeWorkdir`）、`launch_workdir.go`（`resolveLaunchWorkDirForSpec` + 两个测试接缝）、`internal/platform/wsl.go`（WSLFileMTime 可测核心，原生行为不变）。

测试：`launch_wsl_parity_test.go`（新增）、`launch_workdir_test.go`、`pi_login_wsl_test.go`、`wsl_s2_remote_test.go`、`internal/platform/wsl_test.go`、`internal/envvars/wsl_path_allow_test.go`。

未触碰：`internal/wslsupply`、launcher 各供给 writer、`launch_executor.go`（无产品改动，仅被回归消费）、`pi_login_launch.go` 产品逻辑（planner 兜底剥离保留为冗余防线）、D1/D4/D5/D6 相关段（已派 C5）、O7 restart 身份、前端。

回滚方式：worktree 内丢弃上述 tracked 文件的未提交改动即可（无 commit、无主树写入、无全局配置变更）；`frontend/dist`、`mobile/dist` 为 gitignored 构建资产可随 worktree 清理。

## 未覆盖 / 剩余风险（明示）

1. **真机验证缺失**：全部为代码级验证。Windows 真机上「UI/remote → WSL distro 实际落在请求目录、wsl.exe 对不存在 Linux/无效宿主目录的失败形态、WSLENV 转发到 distro 后的实际值」未运行——真机项不伪称已过，留 Leader 安排。
2. **会话预留记录的展示层差异**：`reserveLaunchSession` 在解析前记录宿主链值；WSL + Linux 形态请求的会话记录显示回退值而 PTY 实际 cwd 为请求值（解析后 recipe 已同步为真实值；仅 reserve 时的展示层差异）。远程链无此问题（`finalizeWorkdir` 写回 `Recipe.Workdir`）。如需收敛可在会话管理器补一个解析后更新入口，未借本切片扩大。
3. **无 distro 的 Windows 宿主 + 显式 wsl shell 请求**：resolver 会回退 pwsh（原生目标），此时按原生语义回退（`BootstrapMode` 为权威判据天然正确）；此类机器上显式 Linux 形态 workdir 的会话记录与实际 cwd 同样存在第 2 条的展示层差异。
4. **UNC 形态 workDir**（`\\wsl.localhost\...`、`\\server\share`）行为未测（审计亦列未覆盖）：现走宿主链 Stat + WSL 分支宿主校验，语义未收敛。
5. **remote 计划链 workdir 回退仍静默**（桌面记 WARN、remote `onFallback=nil`）：WSL 显式请求已不再回退（拒绝）；空请求 + 宿主形态默认目录无效时的回退可观测性不对称是既有小缺口，未扩 seam。
6. `TestDarwinResolveExecutableFindsCodexInLocalNodeBin`、envcheck 包 7 个失败、wslsupply 包 3 个失败：基线既有、非本切片范围，未处理未验证。
7. 前端 `SessionSettingsView.vue` 发送 `~` 路径的 UI 面未改：后端已保留语义，前端展示行为未验证。

## SA09 交接（Leader 调整 #1，接口未到、不留占位）

C2 冻结接口 `runWSLSharedFilesAtLaunch(ctx, runner, distro)` 截至本轮**未在本 worktree 及 wsl-goal-core 落地**（已核对两树均无该符号）。按调整指示未提交占位函数。建议接线点：
- 桌面：各 `Launch*` 入口已解析 `wslDistro` 且 PTY 启动之前（pi/omp 现有 `wslDistro != ""` 判定块内，与 `runWSLManagedModelsAtLaunch` 同层）；
- remote/restart：执行器 `wslConfigSupplyEffect.Apply` 的 distro 解析处（与 A2/托管面同闸门位置），或在 `BuildPlan` 的 WSL 分支追加计划内 effect（保持 BuildPlan 纯读约束时须复用计划描述模式，勿在计划期 spawn）。

## 建议下一步

1. Leader 按 hunk 合并本切片与 C5（routing/D1/D4/D5/D6）、C2（SA09 gate）——本切片在 app.go 的改动段为 640s 装配区、2356–2430 漏斗、五入口 choke point/调用点；C5 的 Headroom/provider 段、C2 的 supply 段互不重叠。
2. 集成后在 Windows 真机跑 direction.md 验收矩阵「启动身份」与「工程路径」两组：空格中文 Windows 工程、`/home/...`、`~`、不存在目录（预期拒绝而非换目录）、Linux 形态默认目录、NAT/mirrored 下 env 转发实际值。
