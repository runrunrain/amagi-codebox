# C5 运行链错误路由/网络门修复 — 实现报告（D1/D4/D5/D6）

- 执行者：鲁班（C5）。worktree：`amagi/wsl-goal-routing`（`/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/wsl-goal-routing`），基线 58e7577，改动留在工作区未提交。
- 上游输入（已读全）：`runtime-audit.md`（baize）、`direction.md`（fuxi）。
- 范围遵守：D2/D3 未触碰（C1）；O7 未触碰；`app_wsl_supply.go`/coordinator/writers 未改（C2）；未改权限配置/全局网络/用户真实文件；未全仓 gofmt（仅对本轮触碰的 app.go 与新文件执行过 gofmt，diff 单 hunk 为注释空格规范化）；cwd/env 段（launch_workdir/resolveEmbeddedLaunchSpec env 段/planner env port）未动，C1 可按 hunk 合并。

## 逐项验收 → 证据

### D1 Claude Headroom × WSL × NAT 启动闸门（desktop+remote 一致）— 完成

**产物**
- 新窄 helper `headroom_wsl_gate.go`（根包）：`headroomWSLNetworkAdmission(targetsWSL, env)`——非 WSL 放行；mirrored 放行（仅准入，文案/断言不宣称连通）；NAT/未知（含无 .wslconfig）返回带修复指引的中文拒绝（钉 127.0.0.1:8787、.wslconfig、networkingMode=mirrored、wsl --shutdown）。`headroomSpecTargetsWSL(spec)` 供 planner 链。
- 桌面链 `app.go` LaunchSession：闸门置于 `useHeadroom` 块首——早于 admission 获取、`Headroom.Start/Stop`、会话预留与 PTY（副作用前拒绝）。legacy remote（`/api/sessions` 直调 LaunchSession）同闸门覆盖。WSL 判定与 resolveEmbeddedLaunchSpec 同源：`EmbeddedLaunchTargetsWSL(mode, shellPath, nil) && DefaultWSLDistro(nil) != ""`。
- remote/restart 链 `launch_planner.go` buildClaudePlan：`recipe.UseHeadroom` 块内、effects 构建前，`spec.BootstrapMode==WSL && NAT` → `FailureCapability` 拒绝——HeadroomStart effect 与 PTY 均不产生（不启动宿主 8787、不启动到不可达身份）。mirrored 放行且计划保留 HeadroomStart effect。

**测试**（`headroom_wsl_gate_test.go`）
- 单测：NAT 拒绝+文案五要素、mirrored 放行、非 WSL 永不拦、端口常量钉定（`strconv.Itoa(headroom.DefaultPort)` 与文案一致性防漂移）。
- planner 集成（wslSpecCLIResolver 注入 BootstrapWSL spec）：`TestBuildClaudePlanHeadroomWSLNATRejected`（拒绝且 plan 不返回）、`...MirroredAdmitted`（放行+effect 在）、`...NonWSLUnaffected`（非 WSL + UseHeadroom 不误伤）。

### D4 ApplyEnv 停用路径不继承旧 marker、启用路径唯一重建 — 完成

**产物**（`internal/aiproxy/environment.go`）
- 新常量 `aiProxyEnvMarker`（三处字面量统一）+ `stripAIProxyEnvMarker`。
- 停用路径：`stripAIProxyEnvMarker(base)` 剥离继承 marker（嵌套 CodeBox 场景 `AMAGI_CODEBOX_AI_PROXY=1` 随 MergeWithSystem 流入导致 PinProxyEnvironment 误判激活）；用户普通代理变量（HTTP_PROXY/NO_PROXY 等）语义原样保留。
- 启用路径：重建 out 时同时剔旧 marker（`key != aiProxyEnvMarker`）再追加新值——marker 唯一。代理键接管语义不变。**未删任何网络保护**（PinProxyEnvironment/mirrored 闸门原样）。

**测试**（`environment_test.go` + `service_test.go`）
- `TestApplyEnvDisabledStripsInheritedMarker`（剥 marker + 用户代理键保留）、`TestApplyEnvDisabledWithoutMarkerUnchanged`（无 marker 原样）。
- 既有启用态测试 `TestNativeRoutingLifecycleAndDirectFallback` base 注入继承 marker，新增断言 marker 恰好一条（唯一重建）。
- aiproxy 包全量 `ok`。

### D5 Pi/OMP WSL provider 构建/写入失败明确阻断 — 完成（remote 契约对接单列）

**产物**（`app.go`）
- pi `writePiProviderModelsAtLaunch`：WSL 写失败分支从「Warn + 回退 piProviderMapping」改为阻断 `wslPiProviderWriteError`（与既有构建失败阻断同判）；锁纪律不变（defer Unlock，测试钉 TryLock 可用）。
- omp：临界区整体提取为对称 helper `writeOmpProviderModelsAtLaunch`（defer Unlock，消除原内联 Lock/Unlock 对提前 return 的脆弱性）；WSL 写失败 → `wslOmpProviderWriteError` 阻断；WSL 构建失败 → `wslOmpProviderBuildError` 阻断（原为告警回退）；Windows 路径两种失败均维持既有回退语义。
- 阻断语义 = 本次未启动、未改用默认模型（不产生对官方 endpoint 的错误网络请求、不启动到非等价身份）。

**测试**（`app_wsl_supply_test.go` 追加）
- `TestPiProviderWriteWSLFailureBlocksLaunch` / `TestOmpProviderWriteWSLFailureBlocksLaunch` / `TestOmpProviderBuildWSLFailureBlocksLaunch`：非 Windows 宿主 `launcher.WSLUserHome` 恒空 → `WriteWSLPi/OmpAgentConfig("FakeDistro")` 真实失败触发分支；断言中文文案 + `providerSyncMu.TryLock` 可用。
- `TestPiOmpWindowsPathWriteFailureKeepsFallback`：隔离 home 下 agent 根被同名文件占位 → Windows 路径写失败仍回退内置 provider（回归钉，不阻断）。

**remote 契约对接（不改 C2 函数，报 Leader）**：remote executor 纯配置供给面 `verdictForWSLPiConfigSupply`（launch_executor.go:866-869 + app_wsl_supply.go:1092-1120）目前仅 target-corrupt 阻断、其余告警放行——与桌面新收紧的「写失败阻断」语义仍不一致。需要 C2 侧把「amagi-* provider 期望集未落地」升级为阻断或提供可判定的落地结果；本轮未改其函数。

### D6 Codex 全局 Headroom 对 WSL 的真实选择 — 完成（advisory + remote 拒绝，不新增路由体系）

**核实结论**（代码级）：codex 全局压缩路由载体是宿主侧 `~/.codex/config.toml` openai_base_url 标记块（`syncCodexGlobalHeadroomConfig`）；WSL codex 读发行版侧独立配置（HOME 不同），wslsupply UnitCodexConfig 不含 headroom 标记块 → 任何网络模式下压缩对 WSL 会话都不生效（文件不同步，与 NAT/mirrored 无关）；WSL codex 不携带错误 base_url（不会产生到 8788 的错误请求），风险是用户以为压缩生效。

**产物**
- 桌面链 `LaunchCodexSession`：全局开关开启（或 8788 在跑）+ 内嵌 WSL 会话 → 启动日志 WARN advisory（`codexHeadroomWSLIneffectiveMsg`：路由写入 Windows 侧 config.toml、WSL 会话读发行版侧、不会生效、将直连）。不阻断会话（会话本身可正常工作），不新增路由注入。
- remote/restart 链 buildCodexPlan：`recipe.UseHeadroom`（v1 会话级显式压缩选择）+ WSL spec → `FailureCapability` 拒绝（任何网络模式）——显式选择无法兑现按明确失败处理，与 Claude D1 对称，不默默以直连冒充压缩生效。
- 未改 GetCodexGlobalHeadroom/UI 契约（准确状态字段属公开契约，待产品裁决，见待验项）。

**测试**：`TestCodexHeadroomWSLIneffectiveMsg`（文案钉 8788/config.toml/不生效）+ `TestBuildCodexPlanHeadroomWSLRejected`（空/mirrored/nat 三态均拒）+ `TestBuildCodexPlanHeadroomNonWSLUnaffected`（非 WSL 维持既有 HeadroomStart effect）。

## 验证汇总（真实命令，cwd=worktree）

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS（仅既有 cgo deprecation 警告） |
| `go vet ./...` | PASS rc=0 |
| `go test . -count=1`（根包全量） | `ok amagi-codebox` 7.7s（含全部新增测试） |
| `go test ./internal/aiproxy/ ./internal/launcher/ ./internal/launchplan/ ./internal/headroom/ ./internal/remote/...` | 全 `ok` |
| `GOOS=windows GOARCH=amd64 go build .` | PASS rc=0 |
| `GOOS=windows go vet . ./internal/aiproxy` | PASS rc=0（Windows 测试文件编译检查） |

已知基线失败复现且与本轮无关（audit §4 分类）：`TestSetPathAllow`/`TestWSLFileMTime`/`TestDarwinResolveExecutableFindsCodexInLocalNodeBin`（平台假设/宿主干扰，C1/另切片）。本轮未引入新失败；根包四组 marker 基线失败在本环境 PASS（D4 产品面修复 + 干净 shell env）。

## 未覆盖 / 待验项（真机依赖，非阻塞）

1. **D1/D6 Windows 真机**：闸门在真实 NAT 宿主的拒绝形态、mirrored 实际连通（mirrored 放行是准入策略，非连通验证）——需 Windows + WSL 测试机；direction.md 验收矩阵「网络」组。
2. **v1 错误文案透传**：remote v1 对 FailureCapability 返回固定英文 "CLI is unavailable on this host"（v1_error_mapper.go 既有契约），NAT/压缩细节不达客户端。扩 BuildFailure message 属公开契约变更，本轮未做；宿主侧日志（桌面链）与桌面链中文拒绝承载细节。
3. **D6 状态面**：GetCodexGlobalHeadroom/UI 增加「对 WSL 会话不生效」准确状态字段未做（公开契约，待产品裁决）；remote codex UseHeadroom 在非 WSL 时依赖宿主 config.toml 已有路由（全局开关未开则压缩同样不生效）为既有行为，未在本轮扩大。
4. **D5 remote 契约**：见上——C2 的 `verdictForWSLPiConfigSupply` 告警放行与桌面阻断语义待统一。

## Changed files（worktree amagi/wsl-goal-routing，未提交）

| 文件 | 变更 |
|---|---|
| `headroom_wsl_gate.go` | 新增：D1/D6 共用窄判定与文案 |
| `headroom_wsl_gate_test.go` | 新增：闸门单测 + planner 双链集成测试 |
| `app.go` | D1 桌面闸门（LaunchSession useHeadroom 块首）；D6 桌面 advisory（LaunchCodexSession）；D5 pi WSL 写失败阻断 + omp 临界区提取为 writeOmpProviderModelsAtLaunch（WSL 构建/写失败阻断）+ 4 个文案函数 |
| `launch_planner.go` | D1 buildClaudePlan UseHeadroom 闸门；D6 buildCodexPlan UseHeadroom WSL 拒绝（两个 hunk，均在 UseHeadroom 块内，不涉 cwd/env 段） |
| `internal/aiproxy/environment.go` | D4：marker 常量统一、停用剥离、启用唯一重建 |
| `internal/aiproxy/environment_test.go` / `service_test.go` | D4 测试（停用剥离/原样保留/启用唯一重建） |
| `app_wsl_supply_test.go` | D5 测试（pi/omp WSL 阻断 + Windows 回归钉） |

回滚方式：`git checkout -- app.go launch_planner.go internal/aiproxy/ app_wsl_supply_test.go && rm headroom_wsl_gate.go headroom_wsl_gate_test.go`（或 Leader 按 hunk 挑选合并时直接忽略对应 hunk）。

## 建议下一步

- Leader 按 hunk 合并时注意：app.go 的 7 个 hunk 与 C1 的 cwd/env hunk 无重叠；launch_planner.go 仅 2 个 hunk（均 UseHeadroom 块内）。
- 将 D5 remote 契约需求转 C2：`verdictForWSLPiConfigSupply` 对「amagi-* provider 期望集未落地」的阻断化。
- Windows 真机验证排期（D1 拒绝形态、D6 advisory 可见性、mirrored 连通）。
