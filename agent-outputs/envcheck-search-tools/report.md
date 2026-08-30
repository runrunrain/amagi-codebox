# envcheck-search-tools：pi/omp 搜索工具（fd/ripgrep）环境检测 + 补充安装

- 日期：2026-08-31
- 范围：产品化「WSL distro 缺 fd/fdfind/rg → pi 启动 Warning + 搜索降级」问题（Leader 曾在用户 WSL 手动 apt 安装，本切片把检测与补充安装纳入环境检测体系）
- 任务契约：`PI_OFFLINE=1` 注入下 pi 不自下载搜索工具；WSL 模式 pi 运行于 distro 内

## 1. 设计要点

### 1.1 检测矩阵（internal/envcheck/checker_searchtools.go）

| 宿主状态 | 探测方式 | 缺失时产物 |
|---|---|---|
| Windows + 可用 WSL distro（默认内嵌会话路径） | `platform.WSLSearchToolStatus(distro)`（包级缓存，一次 fork wsl.exe；CheckAll 的 pi+omp 共享一次探测） | warning issue `pi|omp_wsl_missing_search_tools`，Detail 含 distro 名与缺失清单 |
| Windows + 无可用 distro（pi 必然原生运行） | `exec.LookPath`（fd/fd.exe、rg/rg.exe）+ pi/omp 自管目录 `~/.pi|omp/agent/bin`（文件存在性） | warning issue `pi|omp_missing_search_tools_native` + winget manual_command |

- **两侧互斥、WSL 优先**：Windows 会话默认内嵌 WSL（resolver WSL 分支为默认路径），distro 可用时原生侧缺失不构成会话实际遇到的问题，补 issue 只是噪音（决策记录于文件头注释）。
- **仅在 status.Installed 时检测**：工具本体没装时 fd/rg issue 是噪音（门控 + 单测覆盖）。
- 挂载点：checkPi / checkOmp 成功路径（版本解析后）各一行 `s.appendSearchToolsIssues(status)`。

### 1.2 注入链（服务注入回调，对齐 SetHeadroomStopper 模式）

```
EnvCheckSettings.vue executeSolution(sol.type='install_wsl_search_tools')
  → App.RunEnvFixAction (既有绑定)
  → envcheck.Service.RunFixAction → whitelist → runInstallWSLSearchTools()
  → s.wslSearchToolsInstaller()（未注入 → 明确失败，nil 安全）
  → app.go 装配的适配包装 → wslsetup.Service.InstallSearchTools()
  → wslExecRoot(distro, "DEBIAN_FRONTEND=… && apt-get update -qq && apt-get install -y fd-find ripgrep")
```

- app.go 侧改动 10 行（SetWSLSearchToolsInstaller + 适配闭包），只做 wslsetup.InstallResult → envcheck.InstallResult 的形状映射。
- 非 Windows stub（service_other.go）同样提供 `InstallSearchTools()`，保持跨平台可编译与统一失败契约。
- `InstallResult.Tool = "search-tools"`（wslsetup.Service.go 导出 `SearchToolsKey`），Package = `fd-find ripgrep`。

### 1.3 缓存失效链（本特性的正确性核心）

```
InstallSearchTools 成功路径
  → platform.ResetWSLSearchToolCache(distro)   # 新导出；定向清 wslToolsCache，空 distro 清全部
  → 后续 WSLSearchToolStatus(distro) 重新 fork wsl.exe 探测
  → App.RunEnvFixAction 见 Changed=true → 异步 CheckAll
  → checkPi 复检 → appendSearchToolsIssues → 新探测（fdfind/rg 已在）→ issue 消失
```

- 失效点有两处：**安装前**（刷新更早探测留下的 stale 快照，决定 AlreadyOK 短路真值）与**安装后**（让复检读到新状态）。安装后另有验证探测（fdfind 与 rg 均须命中），不命中则结构化失败 + 手动兜底命令。
- `resetWSLHomeCachesForTest` 保持不变（其内部清空即含新语义）。

### 1.4 前端（EnvCheckSettings.vue，最小改动）

- `solutionLabel` 补 `install_wsl_search_tools: '安装搜索工具'`。
- executeSolution 通用兜底 `runEnvFixAction(sol.type, sol.tool || cardKey, '')` 天然覆盖该 type（switch 末尾分支）——**零逻辑新增**。
- 确认弹窗清单（`fix_path || install_node || clean_claude_install`）加入该 type，文案「此操作将在 WSL 内以 root 安装 fd-find 与 ripgrep。是否继续？」，对应后端 `RequiresConfirm: true`。

## 2. 改动清单

| 文件 | 改动 |
|---|---|
| `internal/platform/wsl_home.go` | 新导出 `ResetWSLSearchToolCache(distro)`（复用 wslToolsMu；空/空白 distro 清全部） |
| `internal/platform/wsl_home_test.go` | `TestResetWSLSearchToolCache`：缓存命中/定向失效/空白与空串全清/no-op 不重探（windows-skip 模式） |
| `internal/wslsetup/service.go` | 导出 `SearchToolsKey = "search-tools"` 常量 |
| `internal/wslsetup/service_windows.go` | `InstallSearchTools()` + 包级 var `searchToolStatus` / `resetSearchToolCache`（测试注入 + 缓存失效可断言）+ `searchToolsAptPackages` |
| `internal/wslsetup/service_other.go` | `InstallSearchTools()` 非 Windows stub（结构化失败） |
| `internal/wslsetup/install_search_tools_windows_test.go` | 新：AlreadyOK 短路 / apt 脚本参数与双重缓存失效 / 非 apt 指引 / 验证失败四组用例 |
| `internal/envcheck/types.go` | `SolutionInstallWslSearchTools SolutionType = "install_wsl_search_tools"` |
| `internal/envcheck/checker_searchtools.go` | 新：两侧检测 + issue/solutions 构造 + 门控；包级 var `wslSearchToolStatusProbe` / `searchToolsDistroProbe` 供测试注入 |
| `internal/envcheck/checker_searchtools_test.go` | 新：WSL 矩阵（severity/code/detail/solutions 完整性）、门控矩阵、原生侧 PATH/自管目录/缺失结构、WSL 优先 |
| `internal/envcheck/checker_pi.go` / `checker_omp.go` | 成功路径各 +1 行调用（含注释） |
| `internal/envcheck/service.go` | `wslSearchToolsInstaller` 字段 + `SetWSLSearchToolsInstaller(fn)`（nil 安全） |
| `internal/envcheck/fix_dispatcher.go` | whitelist + switch 分支 + `runInstallWSLSearchTools()`（成功置 Changed=true） |
| `internal/envcheck/fix_dispatcher_test.go` | 未注入明确失败 / 成功透传+Changed / 结构化失败保形 / error 冒泡 |
| `app.go` | NewService 装配区 +10 行：注入 `wslsetup.InstallSearchTools` 适配包装 |
| `frontend/src/views/settings/EnvCheckSettings.vue` | label + 确认弹窗（兜底路径零改动） |
| `docs/api.md` | RunEnvFixAction 白名单描述补 install_wsl_search_tools |

### issue / 方案结构示例（E2E 实测 JSON）

```json
{
  "severity": "warning",
  "code": "pi_wsl_missing_search_tools",
  "message": "WSL 内缺少 fd/ripgrep，pi/omp 的文件搜索能力受限（PI_OFFLINE 下不会自动下载）",
  "detail": "发行版 Ubuntu 缺少：fd、ripgrep。CodeBox 向 pi/omp 会话注入 PI_OFFLINE=1，pi 不会自动下载搜索工具；缺失时启动打印 Warning 且文件搜索降级。",
  "solutions": [
    { "type": "install_wsl_search_tools", "description": "在 WSL 内安装 fd-find 与 ripgrep（apt，需 sudo，由 CodeBox 以 root 执行）", "tool": "pi", "requiresConfirm": true, "isPrimary": true },
    { "type": "manual_command", "description": "在 WSL 终端内手动安装", "command": "sudo apt-get install -y fd-find ripgrep", "tool": "pi" }
  ]
}
```

## 3. 验证证据（全部实际执行）

| 项 | 命令 | 结果 |
|---|---|---|
| Windows 构建 | `go.exe build ./...`（WSLENV 转发 GOCACHE） | exit 0 |
| 静态检查 | `go.exe vet ./internal/envcheck/ ./internal/wslsetup/ ./internal/platform/ .` | exit 0 |
| platform 单测 | `go.exe test ./internal/platform/ -count=1` | ok（含新 Reset 测试，-v PASS） |
| wslsetup 单测 | `go.exe test ./internal/wslsetup/ -count=1` | ok（含 4 组新用例，-v PASS） |
| envcheck 单测 | `go.exe test ./internal/envcheck/ -count=1` | 新增 SearchTools/InstallWSLSearch 全部 PASS；`TestUpdate_ClaudeVerifyFail_ContinuesToFallback`、`TestOpenCodeNPMGlobalExecutableCandidates_…` 失败——**已用独立 worktree 在纯净 HEAD 复跑确认为存量 Windows-only 失败**（CI 全量仅在 macOS 跑），与本切片无关 |
| 前端 typecheck | `npx vue-tsc --noEmit`（frontend/，先 `npm install` 补齐缺失依赖；package-lock.json 已还原） | exit 0 |
| 真实 WSL E2E | 见 §3.1 | 三阶段全过 |

### 3.1 真实 WSL2 Ubuntu E2E（本机）

1. 卸载：`sudo DEBIAN_FRONTEND=noninteractive apt-get remove -y fd-find ripgrep`
   → `Removing fd-find (10.3.0-2ubuntu1) … Removing ripgrep (15.1.0-1ubuntu1)`；`command -v fdfind rg` 无命中，dpkg 0 个 ii。
2. harness（`.tmp-tests/searchtools_e2e/main.go`，go.exe 运行，跑完已删除）：
   - Phase1：`svc.CheckOne(ToolPi)`（装配 `SetWSLSearchToolsInstaller(wslsetup.NewService(nil).InstallSearchTools)` 适配）→ 检出 `pi_wsl_missing_search_tools`（JSON 见 §2）；
   - Phase2：`svc.RunFixAction({Action: SolutionInstallWslSearchTools})` → `success=true changed=true message="已在 WSL（Ubuntu）内安装 fd-find 与 ripgrep（…PI_OFFLINE…）"`；
   - Phase3：重跑 `CheckOne(ToolPi)` → issue 消失（证明 安装 → ResetWSLSearchToolCache → 重探测 链路正确，非 stale 读数）。
3. 还原确认：`command -v fdfind rg` → `/usr/bin/fdfind`、`/usr/bin/rg`；dpkg 恢复 `fd-find 10.3.0-2ubuntu1`、`ripgrep 15.1.0-1ubuntu1`（与卸载前同版本，等价还原用户环境）。

## 4. 回滚说明

改动全部为新增文件 + 少量挂载点，无数据迁移、无配置/契约（wailsjs 绑定）变更：

1. `git checkout -- app.go docs/api.md frontend/src/views/settings/EnvCheckSettings.vue internal/ frontend/package-lock.json` 并删除三个新文件（checker_searchtools*.go、install_search_tools_windows_test.go）即可整体回滚。
2. 运行期状态：`wslToolsCache` 为进程内内存缓存，回滚后旧二进制重启即恢复原语义；WSL 内 fd-find/ripgrep 已装好，属用户环境改善，无需卸载（如需：`sudo apt-get remove -y fd-find ripgrep`）。
3. 无持久化 schema 变化（issue/solution 均为检测期动态产物）。

## 5. 与任务边界的偏差（均已记录理由）

1. **apt 脚本含 `apt-get update -qq` 前置**（任务字面命令仅 install）：与 wslsetup.ensureNode 的 root apt 安装同构，避免全新 distro 空索引报 Unable to locate package；E2E 实测通过。
2. **WSL 侧与原生侧互斥（WSL 优先）**：任务未明确同机两 issue 并存策略；依据「Windows 会话默认内嵌 WSL」采用互斥，避免默认场景双份噪音。若用户显式配置原生 launch 且 distro 存在，原生侧缺失暂不提示（envcheck 无 launch-mode 视图，注入超任务预算）。
3. **App 侧装配 10 行**（任务说 ≤5 行）：闭包形状映射（nil 判定 + 结构体转换）压不到 5 行以内；仍是最小侵入，未引入新绑定。
4. Windows 原生自动安装明确不做（winget/UAC 复杂度超范围）——代码注释已记录。
5. `docs/api.md` 白名单描述同步 +1 个 type（任务未列，属既有文档跟随）。
