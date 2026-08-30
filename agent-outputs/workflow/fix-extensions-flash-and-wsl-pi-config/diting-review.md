# 谛听阶段集成审核：fix-extensions-flash-and-wsl-pi-config（BugA 闪窗 + BugB WSL pi/omp 配置）

- 审核人：谛听（Reviewer，独立节点）
- 日期：2026-08-30
- 基线：HEAD 未提交改动全量（`git diff HEAD` 11 文件 193+/37- + 8 个新增 untracked 源码/测试文件）
- 输入：bugA-report.md、bugB-report.md、阶段 diff、验收标准
- 模式：只审不改；静态审查 + 只读/缓存级复验命令

---

## FINDINGS

### Critical

无。

### Major

#### M1 · 新增 platform 测试在 macOS CI 必挂（提交即红 CI）

- **位置**：`internal/platform/wsl_home_test.go:34`（`TestWSLUserHomeResolvesAndCaches`）、`internal/platform/wsl_home_test.go:116`（`TestWSLSearchToolStatusParsing`）；对应被测守卫 `internal/platform/wsl_home.go:84`、`internal/platform/wsl_home.go:142`
- **触发条件**：在非 Windows 平台运行 `go test ./internal/platform/`。CI 的全量测试恰好只在 macos-latest 执行（`.github/workflows/ci.yml:45-48`：`if: matrix.os == 'macos-latest'` → `go test ./... -count=1`；windows-latest 仅 compile-check）。
- **实际影响**：`WSLUserHome`/`WSLSearchToolStatus` 带 `runtime.GOOS != "windows"` 早退，测试注入的 fake `wslScriptRunner` 在 macOS/Linux 上永不执行、函数直接返回零值 → `TestWSLUserHomeResolvesAndCaches` 期望 `"/home/wslu"` 必失败；`TestWSLSearchToolStatusParsing` 的 3 个期望 true 的子用例（fdfind fallback / both present / rg only）必失败。本切片全部验证在 Windows go.exe 上执行（bugB 报告 §6），故未暴露。
- **证据**：两测试文件无 `//go:build windows`、无 `runtime.GOOS` skip 守卫（grep GOOS/Skip/go:build 零命中）；wsl_home.go:84/142 的 GOOS 早退；ci.yml:45-48。另注：bugB 报告 §6 称"Linux CI 验证过 GOOS 无关性——单测不依赖 runtime.GOOS=windows 的分支"，与代码事实不符（依赖了），该验证声明不可采信。
- **最小修复方向**：两个测试开头加 `if runtime.GOOS != "windows" { t.Skip(...) }`；或按本切片既有范式（`embeddedLaunchTargetsWSLForOS`）把 `WSLUserHome`/`WSLSearchToolStatus` 抽成 osName 注入内核后直接测内核（更优，可保持 macOS 上也有覆盖）。

### Minor

#### m1 · WSL 侧 merge→write 丢失更新竞态（并发启动同 distro 的两个不同 provider 会话）

- **位置**：`internal/launcher/wsl_agent_config.go:78-96`（`WriteWSLPiAgentConfig`：`MergePiAgentConfig` 读 → `WritePiAgentConfig` 写，无锁）；`app.go:3164` / `app.go:3459` 调用点
- **触发条件**：同一 distro 内近乎同时（约几十~几百 ms 窗口）启动两个使用不同 provider 的 pi（或 omp）embedded 会话。
- **实际影响**：两会话各自 merge 读到旧文件，后写者覆盖先写者 → 先启动会话的 `--provider amagi-<A>` 在 WSL 侧 models.json 中不存在 → 该会话 401/未知 provider。**非回归**：Windows 侧非 WSL 分支（`app.go:3171-3181`）既有同样无锁的 merge+write，本切片是同语义镜像；且后台 `syncProvidersToHarnessesLocked` 只写 Windows 侧，不与 WSL 写碰撞。
- **证据**：wsl_agent_config.go 无任何互斥；app.go WSL 分支调用无串行化；对照 `internal/config` 的 `...Locked` 命名显示仓库对配置写有加锁惯例。
- **最小修复方向**：launcher 包级 `sync.Mutex` 包住 merge+write（可顺带覆盖 Windows 侧两条路径），或后续独立切片统一处理。

#### m2 · chmod 补偿失败路径的凭据残留（0644 明文 apiKey 留在 WSL 侧）

- **位置**：`internal/launcher/wsl_agent_config.go:87-92`（rename 已成功后 chmod 700/600 失败 → 返回 error）；`internal/launcher/pi_config.go:308-317`（tmp 文件经 UNC 落盘为 umask 默认 0644）
- **触发条件**：models.json 已 rename 到位后 `wsl.exe chmod` 失败（WSL 服务抖动等）；或 rename 前/失败清理时 tmp（0644，含明文 apiKey）短暂存在/`os.Remove` 再失败残留。
- **实际影响**：fail-closed 回退内置 provider 后，已写入的 WSL 侧 models.json 以 0644（含 apiKey）留存，直到下一次成功启动才被 chmod 600 收紧；窗口内 distro 内其他用户可读。现实暴露面窄（单用户 distro 为主、`\\wsl.localhost` 访问受 Windows 账号会话约束），但与"0600 契约优先"的 fail-closed 初衷不完全自洽——文件本身留下了。
- **证据**：WritePiAgentConfig rename 成功后无回滚；WriteWSLPiAgentConfig chmod 失败仅返回错误不清理；bugB 报告 §4.1 自证 UNC 落盘 0644、os.Chmod 不改 POSIX 位。
- **最小修复方向**：chmod 失败分支 best-effort `os.Remove(<UNC>/models.json)`（写入已被判定失败，删除合理且与"回退 env 兜底"一致）；tmp 残留可接受（Remove 已尽力）。

#### m3 · 探测缓存无失效机制（负结果缓存到进程结束）

- **位置**：`internal/platform/wsl_home.go:69-96`（home 缓存含负结果）、`:124-140` 附近（tools 缓存）、UNC root 缓存
- **触发条件**：首次探测失败（WSL 服务未就绪/distro 冷启动超时）后缓存 ""；或 distro 被删除后缓存 stale 正值。
- **实际影响**：负缓存 → 本进程内后续每次启动都走 WARN + 回退内置 provider，直到重启应用；stale 正缓存 → 写入失败后同样回退（安全方向）。均为优雅降级 + 有日志，无功能性损坏。
- **证据**：三组 cache 均无 TTL/失效钩子，仅 `resetWSLHomeCachesForTest`。
- **最小修复方向**：可暂不修（bugB 报告 §3 已声明该权衡）；若加固，home/tools 的负结果不缓存或缓存带短 TTL 即可。

#### m4 · 真实 WSL E2E 测试改动用户真实配置，恢复依赖外部动作

- **位置**：`internal/launcher/wsl_agent_e2e_windows_test.go:50-80`（`TestManualRealWSLWritePiConfig`）
- **触发条件**：手动设 `AMAGI_WSL_E2E=1` 运行；运行后忘记执行文件头所述"外部脚本恢复快照"。
- **实际影响**：`amagi-wsltest`（假 key `sk-manual-e2e`）经 merge 永久驻留真实 WSL 侧 `~/.pi/agent/models.json`（merge 保留既有 providers；Windows 侧的托管清理不跨边界）。非真实凭据泄漏，属测试卫生问题。另：文件头命令写 `-tags manual` 但文件只有 `//go:build windows`、无 `manual` tag（虚构参数，无害但误导）。
- **最小修复方向**：测试内 `t.Cleanup` 做备份/恢复（读回原内容+权限，defer 写回）；删掉 `-tags manual` 字样。

#### m5 · `.tmp-tests/`（313MB 本地产物）untracked 且不在 .gitignore

- **位置**：仓库根 `.tmp-tests/`（go-cache、codex-workdir-1457759485、pty-opencode-workdir-2334700954）
- **判断**：应加入 `.gitignore`（任务关注点 6）。实测 `git status --porcelain` 显示 `?? .tmp-tests/`、`git check-ignore .tmp-tests` rc=1 → **未忽略**；bugA 报告 §5 的"目录 .tmp-tests/ 任务前已存在且未跟踪"表述属实但易被读成"已忽略"。误执行 `git add -A` 会带入 313MB 构建缓存。顺带：`git check-ignore -v .tmp-tests/`（带尾斜杠）会误报命中 .gitignore:71 空行，勿用带尾斜杠形态判断。
- **最小修复方向**：`.gitignore` 增加一行 `.tmp-tests/`；本地可整目录删除（两个切片报告均确认无副作用依赖）。

### 观察项（不构成 finding，记录备考）

1. **镜像判定等价性已核实**：`capabilitiesForTarget("windows", runtime.GOARCH)` 与 resolver 运行时的 `CurrentCapabilities()` 为同一构造函数逐字段等价；env=nil 时 PATH 回退到继承 PATH，是 resolver 有效 PATH（caller+inherited 并集）的子集，方向上"镜像 true ⇒ resolver true"保守成立，实际漂移面可忽略。mode 门语义与 `normalizedLaunchMode` 一致（`embeddedDefaultLaunchMode` 已把空 mode 归一为 embedded）。
2. **env 剥离时序正确**：pi（app.go:3259）/omp（app.go:3529）均在 `BuildEnv` 后、`resolveEmbeddedLaunchSpec`（内部 `appendWSLENVForwarding`，resolver.go:137）前执行 `StripWSLHostPathPIEnv`；剥离面（`PI_` 前缀 + 盘符路径值）与 `wslENVForwardPrefixes` 的 `PI_` 转发面精确对齐（omp 无独立 OMP_ 前缀转发）；`PI_OFFLINE`/标量/合法 Linux 路径值保留，单测矩阵覆盖（`TestStripWSLHostPathPIEnv`）。
3. **进程标志**：`SuppressConsoleWindow` 15 个调用点均无预置 `cmd.SysProcAttr`（逐一核对 diff），无覆盖回退；`DETACHED_PROCESS` 与 `CREATE_NO_WINDOW` 互斥在纯函数内核+双平台测试固化；PTY/ConPTY 零交叉（internal/pty 无 policy/SuppressConsoleWindow 引用，conpty.Start 不经 exec.Command）；launcher/service.go 的 8 处 exec.Command 为外部终端模式有意可见窗口 + exempted，updater×3/darwin×2 豁免理由复核成立。
4. **非 WSL 回归面**：非 Windows 平台 `SuppressConsoleWindow` 为 no-op、`applyProcessPolicy` 原状；gitassist/ompconfig/path_lookup 在 darwin 行为零变化；app.go 两方法的 else 分支逐行保持（仅提取 `wroteProvider` 布尔 + omp 分支 `pompCfg` 局部变量重命名，行为等价）。
5. `WSLSearchToolStatus` 用 `sh -lc` 探测 PATH，与 PTY 会话 `bash -lic` 的交互式 rc 增补可能有差异 → 仅影响 WARN 提示准确度，不阻断。`wslSearchToolsWarnOnce` 捕获首个 App 实例的 Log，生产单例无影响。
6. 外部终端/VSCode/Zed 模式 + WSL shell 的 pi/omp 仍写 Windows 侧配置（同类 Bug 未覆盖）——切片边界内已声明（bugB §3/§7），非回归，建议后续切片跟进。

---

## 审核覆盖范围

- 全量 diff：11 个修改文件逐 hunk 审阅 + 8 个新增文件全文审阅（process_policy 内核/windows/nonwindows、wsl_home、wsl_agent_config、4 个测试文件）。
- 高风险调用链深读：`WritePiAgentConfig`/`WriteOmpAgentConfig`（原子写+权限）、`MergePiAgentConfig`/`MergeOmpModelsConfig`（读回容错）、resolver WSL 分支 + `appendWSLENVForwarding` + `wslENVForwardPrefixes`、`resolveRequestedShell`/`defaultShellForCapabilities`/`capabilitiesForTarget`（镜像等价性）、`buildEffectiveEnvForOS`（nil env PATH 回退）、ci.yml 测试矩阵。
- 凭据面：UNC 写入路径、tmp 生命周期（WriteFile→Chmod→Rename→失败 Remove 全路径）、chmod 补偿 fail-closed、日志内容（仅 provider/baseURL，无 apiKey）、E2E 测试 key（假 key）。
- 全仓 `exec.Command` 清单与报告 §2.2/2.3 豁免表交叉核对（16 文件，含 gitassist 第 4 处为注释的排除确认）。

## 集成交叉复验（本审核独立执行，Windows Go 工具链，两切片共存工作树）

| 命令 | 结果 |
|---|---|
| `go vet ./internal/platform/ ./internal/launcher/ ./internal/ompconfig/ ./internal/gitassist/ ./internal/wslsetup/ .` | ✅ VET-OK |
| `go test -count=1 -run 'TestProcessPolicy|TestApplyProcessPolicy|TestWSL|TestWriteWSL|TestResolveWSLAgentTarget|TestStripWSL' ./internal/platform/ ./internal/launcher/` | ✅ ok（0.060s / 0.115s） |
| `go test -count=1 ./internal/platform/` | ✅ ok（0.313s） |
| `git status --porcelain` 与两报告文件清单比对 | ✅ 完全一致，无冲突残留 |

## 未覆盖盲区

- Windows 桌面会话的实机闪窗目视复现（无 GUI 环境；接受切片的静态证据链 + 标志落位单测）。
- 真实 WSL E2E 未在本审核中重跑（切片 §4.2 有实证记录；E2E 会改动真实配置，按 m4 建议不宜随意触发）。macOS 实机未运行（M1 依静态分析判定，代码路径无歧义）。
- vendor/ 目录、前端、mobile 未审（diff 未触及）。

## 验证可信度评估

切片自报验证在 Windows 工具链执行、命令与结果可复核（本审核抽验一致）；但 bugB"Linux CI 验证过 GOOS 无关性"声明与代码事实矛盾（见 M1）——该单项声明不可采信，其余抽验项（构建/vet/测试结果、文件清单、豁免表）均复核成立。两个预存失败（ompconfig/piconfig 权限断言在 Windows 宿主）有 HEAD worktree 对照证据，采信为环境性预存问题。

## 结论

两切片的根因诊断、方案选型（UNC 直写 + chmod 补偿 + fail-closed）与实现质量整体扎实，进程标志互斥与凭据 0600 契约的工程化处理到位，env 剥离时序与镜像判定等价性经独立核实成立。**唯一阻断项是 M1：两个新增 platform 测试会在 CI 唯一跑全量测试的 macOS 平台上必然失败，提交即红 CI**，属低成本可修（skip 守卫或内核抽取）。m1–m5 为局部/低影响项，不阻断，建议随 M1 一批修复（m4/m5 尤其便宜）。

**VERDICT: CONCERNS**（要求修复 M1 后再提交；Minor 项可并入同批或后续切片）
