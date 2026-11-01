# v1.3.124 发布阻断 lint 修复报告

## 任务与输入

- 目标：修复 GitHub CI 41329538037（run 41329538037）golangci-lint 失败的最小 lint 问题集，不关闭规则、不改 CI、不做无关重构；保持凭据/WSL 安全语义；不 commit/push/tag。
- inputPaths（跨 tree 绝对路径）：
  - `/tmp/codebox-v1.3.124-ci-failed.log`（CI 失败原文，27 mac / 31 windows issues）
  - `/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/release-1.3.124-lint/.golangci.yml`（未改动）
  - `/Users/run.mao/workpace/amagi-codebox/.amagi-worktrees/release-1.3.124-lint/.github/workflows/ci.yml`（未改动）
- 基线：worktree 分支 amagi/release-1.3.124-lint，HEAD = 主树 4830239，tracked 干净，改动仅工作区（30 个文件，+61/−171）。

## 修复清单（macOS 27 项 / Windows 31 项，包含跨平台重叠；两侧均清零）

### ineffassign（3）
| 报错 | 修复 | 行为影响 |
|---|---|---|
| app_wsl_supply.go:1533 `actions = []string{"preview"}` | 删除死赋值（switch 全分支显式返回） | 无 |
| internal/wslsupply/toml_edit.go:275 `path = append(...)` | else 分支改为 `if _, kerr := splitTOMLKeyPath(keyPart); kerr != nil {...}` 只做可解析校验（path 从不在该分支后读取） | 无（rg+读取上下文核实 path 在表内键值路径不消费） |
| launch_planner.go:862 `hasConfigMutation = false` | 删除死赋值（WSL 分支与 `else if hasConfigMutation` 互斥，其后无读者） | 无 |

### staticcheck（6）
| 报错 | 修复 | 行为影响 |
|---|---|---|
| claude_accounts_facade.go:227 SA4011 | 删除 select case 内的无效 `break`（select 之后的 `if evicted { break }` 才是真实退出） | 无（逐行核对，循环退出语义不变） |
| internal/claudeaccounts/identity.go:86 S1016 | `return ProfileIdentity(parsed), nil`（两结构体字段名/类型/顺序一致，安全转换） | 无 |
| internal/envcheck/uninstall_tool_test.go:487 SA4031 | 方法值永不为 nil；改为 `var _ func(*Service, CLITool) (*OperationState, error) = (*Service).startUninstallTool` 编译期钉存在性+签名，不削弱测试意图 | 无（`TestUninstallToolAddsNoExportedServiceMethods` 通过） |
| internal/platform/drvfs_advisory_test.go:73 SA4006 | 删除无效 `snap = snap[:0]`（值隔离与长度隔离均由既有 `len(again)!=1`/`Detail!=""` 断言覆盖） | 无 |
| internal/wslsupply/plugins.go:1290 S1039 | 去掉无格式化的 `fmt.Sprintf` | 无 |
| internal/wslsupply/review_fixes_test.go:186 SA9003 | 空分支改为正向断言：发布成功必须接管并清除 v1 遗留风险标记（与上一子测试"successful new-generation write clears legacy risk"同语义）——测试增强非削弱 | 断言更强；`go test ./internal/wslsupply/` 通过证实语义正确 |

### unused（18 共有 + 2 跨平台字段）
全平台零引用死代码（rg 全仓核实含 *_windows/_other/_linux buildtags，仅定义处命中）直接删除：
- app_wsl_unified_test.go:255 `targetMCPServers`（本轮未用上的 helper，被 mcpServerURL 取代）
- internal/claudeaccounts/fakes_test.go:339 `fakeTransport.callCount`（fakeIdentity.callCount 仍被 harvest 测试使用，保留）
- internal/claudeaccounts/service.go:643 `bytesEqual`
- internal/launcher/wsl_agent_config.go:146 `wslEnsurePrivateStaging`（无 ctx 旧壳；ctx 版 `wslEnsurePrivateStagingContext` 为生产路径保留）
- internal/launcher/wsl_managed_models_test.go:15 `mustWriteFile`（连带清理 os/filepath import）
- internal/wslsupply/a2_consumption_test.go:22 `countingA2.calls` 字段（计数走 `called *int32`）
- internal/wslsupply/claude_adapter.go:34-35 `claudeMCPTransform`/`claudeSettingsTransform` 常量
- internal/wslsupply/codex_adapter.go:328 `toMapKeys`
- internal/wslsupply/coordinator.go:804 `withdrawnUnpersistedResult`、:949 `recordProjectionState`（提交路径经 CommitRequest.Register→`registerProjectionEntryIn`，后者保留；两处陈旧注释同步指向 registerProjectionEntryIn）
- internal/wslsupply/intent_store.go:208 `latestForUnit`、:224 `snapshot`（连带删除仅此处使用的 `sort` import；`record`/`forOp` 仍被使用）
- internal/wslsupply/probes.go:66 `logInfo`（logWarn 保留）
- internal/wslsupply/skills.go:458 `skillIncompatDetail`
- internal/wslsupply/skills_submit.go:558 `reserveHiddenName`

跨平台字段（不能误删，按任务书两路径处理）：
- internal/platform/move_no_replace.go:37 `fmtMoveNoReplaceUnsupported`：仅 linux 变体使用（`move_no_replace_linux.go`）→ **随唯一消费者迁入 linux 文件**（平台持有，非删除；darwin/windows 有原生原语）。
- internal/aiproxy/router/manager.go:71 `job uintptr`：仅 Windows tie/release 使用 → 先尝试 OS 拆小结构（embedded processOS），mac lint 将空嵌入类型判为 unused（unused 检查不认"零尺寸占位"）→ **回退为字段级 `//nolint:unused` 并注明"仅 Windows process_windows.go 使用，不能删"**。
- internal/claudeaccounts/lock.go:33 `path string`：仅 Unix（lock_unix.go flock 锁文件路径）使用 → **结构体整体按 OS 拆分**：lock.go 只留文档注释，`crossProcessLock{path,file}` 定义迁入 lock_unix.go，`crossProcessLock{file}` 定义迁入 lock_windows.go（Windows v1 获取一律 unsupported，本就不需要路径）。共享 `release()` 只读 `file`，双平台成立。

### errcheck（4，Windows-only）
internal/aiproxy/router/{orphan_windows.go:47, process_windows.go:68/78/88} `procCloseHandle.Call`：
- 新增 `closeHandle(h uintptr) error`（process_windows.go）：失败**只按 BOOL 返回 r1==0 判定**；注释明确 LazyProc.Call 的 lastErr 即使成功也可能非 nil（GetLastError 残留），绝不用作失败信号。
- 4 处清理调用改为 `defer func() { _ = closeHandle(...) }()` / `_ = closeHandle(...)` 显式尽力丢弃——与仓内既有安全模式（internal/updater/process_windows.go 等 `_ = windows.CloseHandle(handle)`）一致，不吞错不误判。

## 新/旧问题对照

- CI 报错集（macOS 27 项 / Windows 31 项，存在重叠）：全部修复；其中 targetMCPServers 是本轮新增的未使用测试辅助函数，其他项来自既有实现。
- 中间产物问题 1 项（processOS 空嵌入被 unused 判死）当轮回退消解；最终 `golangci-lint run` mac 与 GOOS=windows 双面均 **0 issues**，无新增问题。
- 未关闭任何规则、未改 .golangci.yml/ci.yml；未删除任何平台在用代码（rg 核实 + 双 OS lint/build 双重证明）。

## 验证证据（cwd = worktree 根）

| 项 | 命令 | 结果 |
|---|---|---|
| mac 静态面（同 CI pinned 版本） | `GOTOOLCHAIN=go1.25.0 /tmp/gcl-2.12.2/bin/golangci-lint run` | exit 0，`0 issues` |
| windows 静态面 | `GOOS=windows GOTOOLCHAIN=go1.25.0 ... run`（先涉事包后全量） | exit 0，`0 issues` |
| mac 构建+vet | `go build ./... && go vet ./...` | exit 0（仅既有 cgo SecKeychain 弃用警告，与本次无关） |
| windows 交叉编译+vet | `GOOS=windows GOARCH=amd64 go build ./... && go vet ./...` | exit 0 |
| 针对性测试 | `go test -count=1 ./internal/claudeaccounts/ ./internal/platform/ ./internal/aiproxy/router/ ./internal/launcher/ ./internal/envcheck/ ./internal/wslsupply/ .` | 全部 `ok`（含增强后的 review_fixes、facade 淘汰循环、uninstall/统一供给 e2e） |

- golangci-lint 2.12.2 经 `GOPROXY=goproxy.cn GOBIN=/tmp/gcl-2.12.2/bin go install ...@v2.12.2` 隔离获取（本地 PATH/常用 bin 均无该工具；GitHub 直连与本地代理 18890 均被重置，改走 module proxy 官方分发），未改全局工具链/用户配置；lint 以 `GOTOOLCHAIN=go1.25.0` 对齐 CI pin（本地 1.27.2 std 导出数据 v5 超 2.12.2 typecheck 支持上限，CI 即 1.25.0）。
- root 包测试所需 `frontend/dist`/`mobile/dist` 已从主树 `/Users/run.mao/workpace/amagi-codebox/{frontend,mobile}/dist` 复制真实产物（gitignored，不入 diff，不生产占位）。

## 写面与现场

- 改动 30 文件全部为报错文件及其必要配套平台/测试文件；`git status` 无越界改动。
- 未跟踪 agent-outputs/baize、amagi-task 未触碰；未 commit、未 push、未打 tag。
- 回退前先备份现场，仅反向本次 lint 修复增量并检查上下文，不在共享主树整文件还原；冲突先核对，不强制覆盖。/tmp 下隔离工具与日志可留档。

## 未覆盖/风险

Leader 集成复验：主树使用 `GOTOOLCHAIN=go1.25.0`，macOS/Windows 两侧 golangci-lint 2.12.2 均0 issues；`go test ./... -count=1`、`go vet ./...`、Windows交叉构建全部exit 0。GitHub CI将对后续修复提交重新运行；首次run 41329538037的失败历史保留，不冒充首次全绿。

- Windows 真机行为（CloseHandle/tie/release 孤儿回收）仅静态面+交叉编译验证，无法在本机运行 Windows 测试——但改动只把"未检错误"变"显式尽力丢弃"，调用序列与判定逻辑未变（r1==0 判失败，与 win32 语义一致）。
- 全量 `go test ./...`（含 race、e2e、frontend/mobile）由 Leader 集成后统一执行；本轮已覆盖全部涉事包与 root 包。
