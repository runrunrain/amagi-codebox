# P4-A1 报告：GitAssist/PathPicker 路径形态感知 + DrvFS advisory 扩面

- 任务：T11/C1-C3 落点（p4-evidence E4/E7/E8）
- 上游输入：`agent-outputs/wsl-session-friction/p4-evidence.md`（§8-3 git.exe fatal 定谳、E8 静默错位链路）
- 状态：完成。全部验证自跑；4 项预存在环境失败已逐一在 HEAD 快照复现归因（非本批回归）。

## 1. 改动文件

### 共享层（新增）
| 文件 | 内容 |
|---|---|
| `internal/platform/wsl_form_windows.go` | 形态判定事实源（自 pty 提升）：`IsLinuxStylePath` / `WorkDirMapsToDrvFS` / `DrvFSMountPath`（含 isLinuxDrvFSMount/isDriveLetter） |
| `internal/platform/wsl_form_other.go` | 非 Windows no-op（macOS/Linux 上 `/` 即本机原生形态，判定恒否——gitassist/paths 在 macOS 行为不变） |
| `internal/platform/drvfs_advisory.go` | advisory 记录面：`RecordDrvFSAdvisory`（(Source,WorkDir) 原位去重、50 条环形截断）+ `DrvFSAdvisories` 只读快照（副本） |
| `app_drvfs_advisory.go` | `App.GetDrvFSAdvisories() (string, error)`——JSON 数组快照，按需拉取，无轮询/后台任务 |

### 修复 1｜PathPicker 形态感知（E8）
- `internal/paths/list_directories.go`：root 为 Linux 形态时返回结构化 `*LinuxStylePathError`（消息含稳定标记 `LinuxStylePathMarker = "WSL/Linux 路径形态"`，自带可操作文案）；`/mnt/<drive>` 形态同步写 advisory（source "paths"）。不再走 Abs+Stat 静默失败。
- `frontend/src/components/terminal/pathPickerModel.ts`：`LINUX_FORM_PATH_MARKER`（与后端成对维护）；`linuxForm`/`linuxFormRoot` 状态；loadRoot catch 先识别标记——形态声明**不触发 home 兜底**；`retry()` 优先重试原 Linux root（堵住「重试→空串→home 兜底」回流错位树）。
- `frontend/src/components/terminal/PathPickerDialog.vue`：错误态下 linuxForm 时按需拉取 advisory 渲染琥珀色关联提示行（best-effort，拉取失败不掩盖主错误）。
- 边界保留：workDir 为裸 `~` 时初始 root 为空串 → 后端 home 兜底（展示中立可用的 home 树，非遗错位；`~/x` 形态则命中 `~` root 显式声明）。

### 修复 2｜GitAssist 形态感知（E7）
- `internal/gitassist/service.go`：`validateWorkDirForWindowsGit` 前置拦截（消息「工作目录为 WSL/Linux 形态（%s），Windows 侧 git 无法访问；请在 WSL 终端内使用 git，或改用 Windows 盘符路径」）；7 个公共方法入口各拦一次 + `runGit`/`commitViaStdin`/`Push.push` 三个 exec 构造点兜底（机械保证零 git.exe 调用）；`RepoInfo` 不再把形态问题吞成 `IsGitRepo=false`；`/mnt` 形态写 advisory（source "gitassist"）。
- `frontend/src/components/terminal/GitPanel.vue`：新增读取失败空态分支（`!repo && actionError`）——原先 repo=null 时错误横幅藏在 `v-else` 模板内根本不渲染、被误标为「不是 Git 仓库」；现展示后端全文 + advisory 关联提示。

### 修复 3｜DrvFS advisory 扩面（E4+C1）
- `internal/pty/service.go`：形态判定三函数改为 platform 薄委托（wsl_drvfs_test.go/wsl_workdir_test.go 锚定不变、全过）；`buildWSLCommandLine` 在原 `log.Warn` 语义不变的前提下追加 advisory 写入（source "pty"，nil logger 也记录）。
- 前端：`frontend/src/api/drvfsAdvisory.ts`（App 绑定封装 + JSON 解析）、`frontend/src/components/terminal/drvfsHint.ts`（纯匹配助手：workDir 等值或 mountPath 路径段前缀命中，按 at 取最新）。

### wailsjs 手工补（本机无 wails CLI）
- `frontend/wailsjs/go/main/App.js`：`GetDrvFSAdvisories()`（无参，无 arg1/arg2 需要）。
- `frontend/wailsjs/go/main/App.d.ts`：`Promise<any>` + 「wails generate 再生归一」注释（参照 P3-S5 既有样式）；models.ts 未动。

## 2. 新增测试

| 文件 | 覆盖 |
|---|---|
| `internal/platform/drvfs_advisory_test.go` | 记录/去重/At 默认/环形截断/快照副本隔离/JSON 形态钉定 |
| `internal/paths/list_directories_wsl_windows_test.go` | `/mnt/c`、`~/`、`~`、`/`、`/home` 五形态全部结构化拒绝（errors.As + 标记）；/mnt 写 advisory、ext4 不写 |
| `internal/gitassist/service_wsl_windows_test.go` | 7 公共方法 × 5 形态全部前置拦截；错误无 fatal/cannot change to（证明零 git.exe 裸撞）；RepoInfo 不吞成 IsGitRepo=false；advisory /mnt 写/ext4 不写/盘符路径放行 |
| `internal/pty/wsl_drvfs_advisory_test.go` | pty 入口 advisory（mountPath 明细）+ ext4 不写；原 log.Warn 语义由既有 wsl_drvfs_test.go 保持 |
| `frontend/src/__tests__/components/terminal/pathPickerModel.test.ts`（+5 用例） | /mnt 不降级（单次调用断言）、`~/projects`→`~` 命中、retry 不落 home、普通失败仍降级（不误伤）、形态后导航恢复 |
| `frontend/src/__tests__/components/terminal/drvfsHint.test.ts`（6 用例） | workDir 等值/挂载点前缀（尾斜杠归一）/最新优先/ext4 与 /mnt/wsl 不命中/空 mountPath 跳过/路径段边界（/mnt/x2 ≠ /mnt/x） |

## 3. 验证证据（自跑，cwd=X:\WorkSpace\amagi-codebox，go=C:\Program Files\Go\bin\go.exe 1.25.5）

| 项 | 命令/方式 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | exit 0 |
| vet | `go vet . ./internal/paths/ ./internal/gitassist/ ./internal/pty/ ./internal/platform/` | exit 0 |
| gofmt | `gofmt -l <全部新增/改动 Go 文件>` | 零输出（见 §5 注 1） |
| Go 测试 | `go test ./internal/paths/ ./internal/gitassist/ ./internal/pty/ -count=1` | paths ok、gitassist ok、pty 仅 1 项预存在环境失败（§4） |
| Go 测试 | `go test ./internal/platform/ -run 'TestDrvFSAdvisory...'` 等 | 全 PASS |
| 前端类型 | `npx vue-tsc --noEmit`（frontend/） | exit 0 |
| 前端单测 | `npx vitest run`（frontend/） | 30 文件 / 271 测试全过（含本批 +11） |
| wailsjs | App.js/App.d.ts 手工补齐，vue-tsc 引用通过 | ✅ |

环境注：WSL→Windows interop 需 `WSLENV='GOCACHE/w' GOCACHE='C:\Users\毛润\AppData\Local\go-build'`（%LocalAppData% 不跨界）。

## 4. 预存在失败归因（隔离 worktree @HEAD 复现，均非本批回归）

| 失败 | 归因证据 |
|---|---|
| pty `TestExactShellAttachReadyThenBootstrapExecutesTargetCLI`（10s 超时等 env dump） | HEAD 快照复现同样失败（cmd.exe+fake shim 本机环境问题）；本批 pty 改动仅 WSL 命令行分支与薄委托 |
| platform `TestDarwinFNMDefaultAliasDeduplicatedAgainstPhysicalInstallDir`（os.Symlink “A required privilege is not held by the client”） | resolver_test.go 与 HEAD 一致（本批未触碰）；symlink 权限属环境；CI 全量测试跑 macos-latest 不受影响 |
| 根 `TestConfigCompensation*`×2（Access is denied）、`TestResolveLaunchWorkDirChain`（跨盘 filepath.Rel）、`TestRC1_0_...`（PTY 端口） | 四项在 HEAD 快照（补 frontend/dist、mobile/dist embed 后）逐一复现 |

## 5. 其他说明

1. `internal/pty/service.go` 的 gofmt 通过需要一行**预存在**注释归一：gofmt 1.25.5 会把 bashSingleQuote 文档注释里的 `'\''` 规范化为 `'\”`（HEAD 即脏，已按工具链要求归一；语义无涉）。gitassist/pty 内我新增注释统一「兜底」拼写（仓库 120:11 主流用字）。
2. 兄弟任务并行写面（app.go、launcher/wsl_agent_config、platform/resolver.go、wsl.go 等）与本批零文件重叠；上述预存在失败与兄弟中间态无关（HEAD 复现）。`.tmp-tests/` 下非本批构造物（codex-workdir、go-cache、pty-opencode-workdir、wgo*）未触碰；本批的两个归因 worktree 已移入 /tmp/amagi-trash.* 自然回收并 `git worktree prune`。
3. 未覆盖项：GitPanel/PathPicker 组件级挂载测试未写（形态分支逻辑全部落在纯模型/助手层并已覆盖；组件只做渲染接线，符合仓库「可测逻辑在纯 TS」先例）；advisory 无清除接口（进程生命周期即边界，环形截断 50 条）。

## 6. 回滚说明

本批全部为未提交工作区改动，无 commit。回滚 = 丢弃以下文件的改动/删除新增文件：
- 新增：`internal/platform/wsl_form_windows.go`、`wsl_form_other.go`、`drvfs_advisory.go`、`drvfs_advisory_test.go`；`internal/paths/list_directories_wsl_windows_test.go`；`internal/gitassist/service_wsl_windows_test.go`；`internal/pty/wsl_drvfs_advisory_test.go`；`app_drvfs_advisory.go`；`frontend/src/api/drvfsAdvisory.ts`、`frontend/src/components/terminal/drvfsHint.ts`、`frontend/src/__tests__/components/terminal/drvfsHint.test.ts`
- 修改（还原至 HEAD 即可）：`internal/paths/list_directories.go`、`internal/gitassist/service.go`、`internal/pty/service.go`、`frontend/src/components/terminal/pathPickerModel.ts`、`PathPickerDialog.vue`、`GitPanel.vue`、`frontend/src/__tests__/components/terminal/pathPickerModel.test.ts`、`frontend/wailsjs/go/main/App.js`、`App.d.ts`
