# conpty-workdir-fix 修复报告

> 修复 Windows 用户报错：`启动失败: start pty: conpty start: Failed to create console process: The directory name is invalid.`
> Windows 无法在 mac 开发机复现（ConPTY 仅 Windows），本修复基于代码推理定位根因，以防御式校验+回退消除触发面，并用表驱动单测 + Windows 交叉编译验证。

## 1. 根因分析

- `internal/pty/service.go` `StartResolvedWithRunEvidence`：`workDir != ""` 时透传 `conpty.ConPtyWorkDir(workDir)`。
- vendor `github.com/UserExistsError/conpty/conpty.go`：`Start` → `createConsoleProcessAttachedToPTY` 把 `workDir` 转成 UTF16 后作为 `CreateProcessW` 的 `lpCurrentDirectory`；目录不存在/不是目录/含非法字符（引号等）时进程创建直接失败，错误码 `ERROR_DIRECTORY(267)`（"The directory name is invalid"）。
- 该错误在 pty 层被包成 `conpty start: %w`，再被 `app.go:2020`（桌面 embedded 路径）或 `launch_executor.go:673`（plan executor 路径）包成 `start pty: %w`，最终由前端 `useSessionLaunch.ts:240` 前缀 `启动失败: ` 展示——与用户报错逐字吻合。
- **全链路此前没有任何"绝对化 + 存在 + 是目录"校验**：五个 `App.Launch*` 只在 `workDir == ""` 时回退 `GetDefaultPath()`→`os.UserHomeDir()`；非空的陈旧默认目录（paths.json）、前端手动输入的笔误、带引号/尾部空格路径、已被删除的项目目录，全部原样击穿到 `CreateProcessW`。

## 2. workDir 全链路图（修复后）

```
┌─ 前端 workDir 来源（desktop）──────────────────────────────────────────┐
│ SessionSettingsView.vue：手动输入框 / 原生目录选择(:527) / saved dirs  │
│   单选 / 默认目录；dashboard 默认值来自 GetDefaultPath               │
│ useSessionLaunch.ts:169-206 → frontend/src/api/session.ts (workDir)   │
│   → wailsjs 绑定（自动生成，未手改）                                   │
└──────────────────────────────┬────────────────────────────────────────┘
                               ▼
┌─ App 层启动入口（app.go）──────────────────────────────────────────────┐
│ LaunchSession(:1811) / LaunchCodexSession(:2532) /                    │
│ LaunchPiSession(:2900) / LaunchOmpSession(:3165) / LaunchOpenCode(:4372)│
│ ★修复点（5 处统一）: workDir → a.resolveLaunchWorkDir()               │
│   Clean+Abs → os.Stat(存在且为目录)                                    │
│   无效回退: GetDefaultPath()(同样验证) → UserHomeDir(同样验证)         │
│   回退发生 Warn: 原始路径+原因+回退目标；全部无效才报错                │
└──────────────┬───────────────────────────────────┬───────────────────┘
   embedded 模式│                                   │ external 模式
               ▼                                   ▼
 resolveEmbeddedLaunchSpec(:1791)            externalSessionLauncher()
 → platform.CLIResolver.Resolve             （cmd.Dir 同样受益于前置校验）
   (internal/platform/resolver.go:118        workDir 纯透传)
   WorkDir 纯透传)
               ▼
 pty.Service.StartResolvedWithRunEvidence (service.go, //go:build windows)
   workDir != "" → conpty.ConPtyWorkDir(workDir)   ← spec.WorkDir=="" 时
               ▼                                      保持不传（现状未动）
 vendor conpty.go:345 Start → :157 createConsoleProcessAttachedToPTY
   → CreateProcessW lpCurrentDirectory → 目录无效 ⇒ ERROR_DIRECTORY(267)
   ← 修复后到达此处的 workDir 必然"绝对+存在+是目录"

┌─ 远程入口（同样汇入修复）──────────────────────────────────────────────┐
│ HTTP(移动端): internal/remote/handlers.go handleLaunch*/Launch-Codex/  │
│   Pi/Omp/OpenCode → 全部转调 App.Launch*（自动获得校验）               │
│ WS v1 plan 流: remote_session_adapter.go:453/965 →                    │
│   appLaunchPlanner.BuildPlan (launch_planner.go:143)                  │
│   ★修复点: workdir → resolveLaunchWorkDirChain(TrimSpace(req.Workdir),│
│     canonicalWorkdir(), nil) → 全部不可用才 FailureWorkdir            │
│   → EffectSpec PTYStart → launch_executor.go ptyStartEffect.Apply:671 │
│     → 同一 PTY spec（同一 ConPTY 崩溃面，已覆盖）                      │
└────────────────────────────────────────────────────────────────────────┘

macOS 对照：service_darwin.go:284 cmd.Dir = spec.WorkDir，目录无效时
spawn-pty 失败（chdir: no such file or directory）——同类问题，app 层
choke point 同时消除两平台的触发面。
```

## 3. 修复实现

| 文件 | 改动 |
|---|---|
| `launch_workdir.go`（新增） | choke point：`App.resolveLaunchWorkDir`（Warn 日志封装）+ 包级 `resolveLaunchWorkDirChain`（requested → defaultPath → home 候选链）+ `validateLaunchWorkDir`（Clean+Abs+Stat/IsDir） |
| `app.go` | 5 个 `Launch*` 的 `workDir==""` 回退块（各 8 行、五处重复）收敛为统一调用 `a.resolveLaunchWorkDir(workDir)` |
| `launch_planner.go` | `BuildPlan` 的 canonical workdir 段改走同一 chain；全部候选不可用仍映射 `FailureWorkdir`（移动端错误语义不变） |
| `launch_workdir_test.go`（新增） | 15 个用例（见 §5） |
| `launch_planner_executor_test.go` | 追加 2 个 BuildPlan workdir 用例 |

关键语义：

- **相对路径**（`.`、项目名）：`Clean+Abs` 基于进程 cwd 解析，存在即可用（Abs 内部调用 Getwd，与解析一致）。
- **引号/尾部空格路径**：Clean 不做字符串清洗，Stat 自然失败 → 回退（assignment 指定不做额外清洗）。
- **斜杠差异**：Windows 反斜杠/正斜杠由 `os.Stat`/`filepath` 统一吸收，无平台分支。
- **空 requested**：视为"无偏好"，静默走回退链（与旧行为一致，不产生 Warn）。
- **非空但无效**：每个被淘汰候选在链上有幸存者时逐个 Warn（`requested=%q reason=%s fallback=%q`）。
- **全部无效**：`no usable working directory: requested workDir "X": stat: ...; default path ...: ...; home ...`——含原始路径与逐级原因，可定位。
- 回退后 workDir 一路传给 session 记录（`reserveLaunchSession` CreateSpec、`stableDesktopRecipe`、`startTitleTracker`、plan `Recipe.Workdir`），会话详情展示的是实际启动目录。

## 4. 校验放 app 层 vs pty service 层的取舍（assignment 第 4 点）

`internal/pty/service.go` 的 ConPTY 透传逻辑**有意未动**（`workDir != ""` 才传 `ConPtyWorkDir`；空值不传的现状保持）。对比：

- service 层（darwin 分支确有先例：`cmd.Dir` + `formatLaunchFailure("spawn-pty")` 的错误包装，但也只是包装、无回退）若做校验：
  - 回退需要业务上下文（`GetDefaultPath` 是应用配置，pty 传输层无权访问）；
  - 通用层静默换目录会掩盖用户输入错误、并把用户未授权的目录写进会话语义；
  - `pty.Service` 被 plan executor、legacy `Start` 等多调用方共享，"忠实执行 spec"职责单一更安全。
- app 层 choke point：
  - 单点覆盖桌面 5 入口 + remote HTTP（汇入 `App.Launch*`）；
  - plan 流（ws v1）在 planner 复用同一 chain 函数，同样收口；
  - Warn/错误信息携带应用语义（原始路径、默认目录、Home）。
- 结论：校验+回退属应用策略，放 app/planner 层；service 层保留"spec 合法则忠实执行"。残余风险：未来若有绕过 App/planner 直接调 `pty.Service` 的新调用方，需自行保证 spec 合法（现存 legacy 7 参 `Service.Start` 无生产调用方，仅测试使用）。

## 5. 测试

`launch_workdir_test.go`（表驱动，`t.TempDir` 伪造目录，`t.Setenv` 隔离 HOME/USERPROFILE）：

- 绝对有效目录保留原值
- 相对路径基于进程 cwd 解析为绝对路径
- 不存在路径 → 回退默认目录（1 次回退回调，reason 非空、目标=最终值）
- 文件而非目录 → 回退（reason 含 "not a directory"，独立断言用例）
- 含引号路径 → 回退
- 尾部空格路径 → 回退
- requested+default 均无效 → 回退 Home（2 次回调）
- 空 requested → 默认目录（0 回调）
- 空 requested+空 default → Home（0 回调）
- 全部无效 → 报错含原始路径与 "no usable working directory"
- 全部为空 → 报错 "all empty"
- App 封装（newTestApp 模式）：无效路径 + SetDefaultPath(临时目录) → 回退默认目录，且实际触发 `WARN [session] 启动工作目录不可用，已回退`（测试输出可见）

`launch_planner_executor_test.go` 追加：

- `TestBuildPlanInvalidWorkdirFallsBackToCanonical`：远程 BuildPlan 传不存在 workdir → 计划成功，`Recipe.Workdir` 为存在的回退目录
- `TestBuildPlanAllWorkdirsInvalidFails`：requested/homeDir/HOME 全不可用 → `FailureWorkdir`

## 6. 验证证据（本机 macOS）

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ |
| `go vet ./...` | ✅ 净 |
| `go test . -count=1`（根包全量，含既有 planner/executor/app 套件） | ✅ ok 5.68s |
| `go test . -run 'WorkDir|Workdir' -count=1 -v` | ✅ 15 用例全过 |
| `go test ./internal/pty/ ./internal/platform/ ./internal/remote/ ./internal/launchplan/ ./internal/session/ -count=1` | ✅ 全 ok（paths 包无测试文件） |
| `GOOS=windows go build ./...` | ✅（vendor 模式） |
| `GOOS=windows go vet ./...` | ✅ |

## 7. Windows 手验步骤（需真机）

1. `build.bat`（或 CI release 产物）安装后启动 app。
2. 场景 A（默认目录失效）：编辑 `%USERPROFILE%\.amagi-codebox\paths.json` 把 `DefaultPath` 改为不存在的 `C:\nonexistent-xyz`，前端不填 workDir 启动任意 CLI → 期望正常启动于用户 Home，日志出现 `WARN [session] 启动工作目录不可用，已回退 requested="C:\nonexistent-xyz" ... fallback="C:\Users\<me>"`，不再出现 "The directory name is invalid"。
3. 场景 B（笔误/引号/尾部空格）：启动面板手动输入 `"C:\Users"`（带引号）、`C:\Users\ `（尾部空格）、`Z:\nope` → 全部回退后正常启动。
4. 场景 C（有效相对路径）：输入存在的相对名（如进程 cwd 下的目录名）→ 正常以绝对路径启动（会话详情 workDir 为解析后的绝对路径）。
5. 场景 D（文件路径）：输入某个 `.exe`/文档完整路径 → 回退默认目录启动。
6. 移动端 remote：`POST /api/sessions/launch`（或 launch-pi 等）带无效 `workDir` → 会话照常启动于回退目录；WS v1 `create session` 同理（会话详情展示实际目录）。
7. 极端：默认目录无效且 `%USERPROFILE%` 不可用（如注册表/环境异常）→ 启动报错，错误信息含原始路径与各级原因。

## 8. 未覆盖 / 剩余风险

- Windows 真机行为未实测（macOS 无法复现 ConPTY）；依赖单测 + 交叉编译 + 代码推理，建议按 §7 抽验场景 A/B。
- 回退是"静默换目录"：用户笔误时会话落在默认/Home 而非预期目录。已通过 Warn 日志 + 会话详情 workDir 缓解；前端 toast 提示属范围外，未做。
- plan 流（ws v1）回退无 Warn：planner 是纯读组件、无 logger（注入需改构造签名，波及 ~10 处测试装配）；实际目录记录在 `plan.Recipe.Workdir`/会话详情，PTY 日志 `创建 ConPTY 会话 ... workDir=` 可见。
- 未改前端（后端防御式修复按任务约定）；`frontend/wailsjs/` 未触碰。
- 工作区内 `app_config_portable.go`、`docs/`、`frontend/src/*` 等改动属并行任务，本任务未触碰。
