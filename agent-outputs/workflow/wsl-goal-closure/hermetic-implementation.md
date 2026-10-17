# Hermetic 修复实施报告（wsl-goal-hermetic）

任务：独立修复全仓测试的 9 项宿主污染，使 WSL 总验收不再依赖用户机器上安装了哪些 CLI。
起始 HEAD 58e7577 + `resumed-input.patch` 完整导入（check+apply 均通过，导入态快照存 `.amagi/hermetic/00-imported-snapshot.patch`）。本工作区改动仅 3 个测试文件，**零生产代码改动**。

## 一、根因（真实泄漏依赖，已复现证实）

1. **internal/envcheck 8 项**：platform 解析器按 `firstExistingFNMBaseDir` 的顺序**优先读取环境变量 `FNM_DIR`**（`internal/platform/path_lookup.go`），再退回 HOME 推导。envcheck 测试夹具隔离了 HOME/PATH/NPM_CONFIG_PREFIX，但**从未清空 `FNM_DIR`**。在 shell 导出 `FNM_DIR` 指向真实 fnm base 的机器上（Leader 主树环境即如此）：
   - `resolveExecutable("claude")` 的受控候选直接看到宿主 fnm base 里的真实 claude → `CheckOne` 报"已有 npm 安装" → `ensureNoConflictInstall` 走清理 → fake runner 无 uninstall 处理器 → 5 项 native bootstrap/异步进度失败；
   - `buildEnhancedEnv` 解析 node/npm 时把真实 fnm bin 目录**前置**到增强 PATH，使 `TestClaudeNativeBootstrapResolverFallback...` 解析到宿主 `.../fnm/aliases/default/bin/claude` 而非夹具 shim；
   - `TestCheckClaudeCodeDetectsNPMPackageBinaryFallback` 的版本探测打到 runner 未预期的宿主路径 → `run claude --version: file does not exist`。
   **复现**：`FNM_DIR=/Users/run.mao/.local/share/fnm go test ./internal/envcheck -count=1` → 旧代码恰好复现 final-go-all.log 的 8 条失败（逐名一致，见 `02-real-fnm-before-fix.log`）。
2. **internal/platform 1 项**：`TestDarwinResolveExecutableFindsCodexInLocalNodeBin` 通过 env 切片传入夹具 HOME，但 `userBinCandidatesForOS`/`firstExistingFNMBaseDir` 还会回退到 `pathLookupUserHomeDir()`（= `os.UserHomeDir` = **进程 $HOME = 真实家目录**），真实 `~/.local/bin/codex` 与真实 fnm 目录先于夹具 `~/.local/node/bin` 命中 → 解析出宿主路径。本工作区基线即稳定复现（`01-worktree-baseline-fullgo.log`）。

## 二、修复（仅测试隔离；未动任何生产注入面）

| 文件 | 改动 |
|---|---|
| `internal/envcheck/native_bootstrap_fallback_test.go` | `prepareNativeBootstrapTest`（覆盖 7 项失败测试）、`TestClaudeNativeBootstrapResolverFallbackKeepsDarwinShimInsteadOfExeRealpath`、`TestClaudeCheckOneFindsNPMGlobalWhenProcessPATHMissing` 各加 `t.Setenv("FNM_DIR", "")`（附注释） |
| `internal/envcheck/checker_claude_test.go` | `TestCheckClaudeCodeDetectsNPMPackageBinaryFallback` 加 `t.Setenv("FNM_DIR", "")` |
| `internal/platform/resolver_test.go` | `TestDarwinResolveExecutableFindsCodexInLocalNodeBin` 用**既有** `pathLookupUserHomeDir` 缝隙 stub 到夹具 HOME（同文件既有模式），真实家目录不再进入候选 |

- 无断言删除、无 skip、无宿主路径当 expected；既有功能断言全部保留。
- `TestClaudeCheckOneFindsNPMGlobalWhenProcessPATHMissing` 是合成污染夹具额外暴露的第 9 个同类隐患（旧代码在真实 fnm 布局下"碰巧"通过：真实 claude 经 node_modules realpath 被分类为 npm；普通二进制布局则失败）——已一并闭环。
- 产物：`hermetic.delta.patch`（74 行，仅本轮差量；对 HEAD 或 HEAD+snapshot 均可直接 `git apply`，round-trip 校验通过；3 个文件在导入态均为 HEAD 原版，故差量不含输入快照内容）。

## 三、验证（cwd=本 worktree；详细日志 `.amagi/hermetic/`）

| # | 命令 | exit | 日志 |
|---|---|---|---|
| 1 | `go test ./... -count=1`（修复前基线，无 FNM_DIR） | 1（仅 platform 1 失败 + root `frontend/dist` embed 缺失，见下） | 01 |
| 2 | 修复前 `FNM_DIR=<真实fnm> go test ./internal/envcheck -count=1` | 1，8 失败，与主树 final-go-all.log 逐名一致 | 02 |
| 3 | 修复前 `FNM_DIR=<合成rig> go test ./internal/envcheck -count=1` | 1，8 失败（rig 仅含假二进制，无真实安装） | 03 |
| 4 | 修复后 `go test ./internal/envcheck ./internal/platform -count=1` | 0 | 04 |
| 5 | 修复后 `FNM_DIR=<真实fnm>` 同上 | 0 | 05 |
| 6 | 修复后 `FNM_DIR=<合成rig>` 同上 | 0 | 06 |
| 7 | `go test -race ./internal/envcheck ./internal/platform -count=1` | 0 | 07 |
| 8 | `FNM_DIR=<合成rig>` 下同上 -race | 0 | 08 |
| 9 | `FNM_DIR=<合成rig> go test ./... -count=1` | 1，**仅** root 包 setup（`frontend/dist` 为本 worktree 未构建的嵌入产物，非测试失败；主树已有构建产物） | 09 |
| 10 | `go vet ./internal/envcheck ./internal/platform`；`gofmt -l`（仅3个改动文件） | 0 / 无输出 | — |
| 11 | 9 个具名测试 `-run` 过滤在干净/真实污染/合成污染下逐项 PASS（8+1） | 0 | 会话记录 |

**受控污染 rig**：`mktemp -d` 下构造 `aliases/default/bin/{claude,node,npm}` 与 `node-versions/v9.9.9/installation/bin/claude`（`#!/bin/sh` 假脚本），`FNM_DIR` 指向它——不依赖、不触碰用户真实安装（rig 路径存 `.amagi/hermetic/rig-path.txt`）。

**测试自证不触真实环境**：envcheck 夹具将 HOME/USERPROFILE/APPDATA/LOCALAPPDATA/NPM_CONFIG_PREFIX 全部指向 t.TempDir()，本次仅追加 `FNM_DIR=""`（阻断读到真实 fnm）；platform 测试将 user-home 回退 stub 到夹具。所有命令执行均经 fake processRunner（`NewServiceWithRunner`），无真实安装/卸载/npm 调用；未读取任何密钥；未修改宿主 Claude/Codex/node。

## 四、未覆盖 / 残余风险

- **系统级 shell 泄漏**：darwin 解析兜底会真实执行 `/bin/zsh -ilc 'command -v …'`，若某机器在 `/etc/zshrc`/`/etc/zprofile` 注入 fnm/brew 路径仍可能外漏（本机与主树均无此配置；关闭需 platform 导出测试钩子，超出本轮 9 项所需，未做）。
- **brew 基线目录**：`darwinBaselinePATH`（/opt/homebrew/bin 等）为真实系统目录，若用户把 claude/codex/node/npm 装在其中，envcheck 部分测试仍受影响（本机与主树均未装；非本轮 9 项通道）。
- 本 worktree root 包 `go test ./...` 因缺 `frontend/dist` 构建产物 setup failed（环境缺口，主树集成复跑不受影响；未构建前端，超出写边界）。
- 平台其余 darwin 测试在同污染环境下已验证通过；`-race` 仅覆盖两个目标包（任务书指定目标包层级）。

## 五、回滚

`git checkout -- internal/envcheck/native_bootstrap_fallback_test.go internal/envcheck/checker_claude_test.go internal/platform/resolver_test.go`（或反向应用 hermetic.delta.patch）；无其它工作区改动。
