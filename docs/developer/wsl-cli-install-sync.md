# Windows 安装/升级 × WSL 侧 CLI 同步（双侧联动）

> 面向读者：CodeBox 维护者。本文说明「通过 CodeBox 安装或升级 CLI 时，同步
> 安装/升级 WSL 内 Linux 版」这条链路的目标、触发条件、结果语义与失败重试
> 方式。设计基线：`agent-outputs/workflow/windows-wsl-unified/direction.md`
> §2；实现落点：`internal/envcheck`（接缝）+ 根目录 `app_wsl_cli_sync.go`
> （门控/分派）+ `internal/wslsetup`（WSL 侧原语，P2-A）。

## 目标

Windows 是统一管理入口：用户在环境检测页（或修复动作）安装/升级五个受管
CLI（Claude Code、OpenCode、Codex、Pi、Oh My Pi）中的任意一个并成功后，
**同一个操作内**自动把对应的 Linux 版安装/升级到当前默认 WSL 发行版——
用户不需要在「WSL 内的 CLI」卡片里再点一遍。不新增设置项、不操作全部已
安装发行版，只作用于现有有效的默认发行版（`platform.DefaultWSLDistro`，
与 wslsetup/配置供给共用同一目标语义）。

## 触发条件（缺一不可）

1. **Windows 侧成功**：install/update 操作的 Windows 部分返回
   `Success=true`。Windows 失败、uninstall、headroom（宿主侧 Python 代理，
   无 WSL 语义）都不触发，`InstallResult.WSL` 保持 nil。
2. **回调已装配**：App 构造时把 `App.syncCLIToWSL` 经
   `envcheck.Service.SetWSLCLISync` 注入（`app.go` 装配段一行）。envcheck
   本身不 import wslsetup（回调注入模式，与 `SetWSLSearchToolsInstaller`
   同构）；未注入（单测/旧装配）时整条联动是 no-op。
3. **有有效默认发行版**：`DefaultWSLDistro` 为空（非 Windows / 无 WSL）时
   返回 nil——不产出 WSL 行，前端表现与联动存在之前完全一致。
4. **门控（满足其一才执行）**：
   - 该 CLI 的嵌入式会话 shell 设置为 `wsl`
     （`settings.json` `dashboard.claudeShell/openCodeShell/codexShell/
     piShell/ompShell`），或
   - 该 CLI 在 WSL 内已原生安装（快照可用且与当前目标同一发行版时才采信
     其 installed 位；不可用/其它发行版的快照按未装处理）。

   都不满足时返回 `status=skipped` 并附原因——不用 WSL 的用户不会被自动
   塞进 Node 和 CLI，且「为什么没同步」在结果里可观察。

## 分派规则

| Windows 操作 | WSL 内状态 | WSL 侧动作 |
|---|---|---|
| install | 未装 | `wslsetup.InstallTool`（幂等：已装短路 AlreadyOK） |
| install | 已装 | `wslsetup.UpdateTool`（`npm i -g <pkg>@latest`） |
| update | 任意 | `wslsetup.UpdateTool`（同一条 `@latest` 命令，未装会补装） |

键映射：envcheck 的 `claude_code` → wslsetup 的 `claude`；omp 在 P2-A 起
进入 wslsetup 安装面（`@oh-my-pi/pi-coding-agent`）。

覆盖入口：异步 `StartInstallToolAsync` / `StartUpdateToolAsync` /
`StartInstallClaudeWithMethodAsync`（含 Claude 指定通道），同步
`InstallTool` / `UpdateTool` / `InstallClaudeWithMethod`，以及修复动作
`install_tool`（转异步链）与 `install_claude_method`（同步链）。

## 结果语义与部分失败

- `InstallResult.Success` 与 `OperationState.Status` **仍只表示 Windows
  侧**——CheckOne 缓存刷新、remote 等既有消费者语义不变。双侧结论由消费
  者读 `InstallResult.WSL`（`WSLSyncResult`）得出：
  - `succeeded`：WSL 侧完成（带 distro/version/alreadyOK）；
  - `failed`：WSL 侧失败，`error` 保留原文（err 与 res.Error 都在时全保留）；
  - `skipped`：门控未命中，`message` 给原因；
  - `nil`：本次没有 WSL 步骤（见触发条件 1–3）。
- **部分失败不得伪报全成功**：
  - 前端异步终态横幅（`envcheckSolutions.ts::operationCompletionBanner`）：
    Windows 成功而 `wsl.status=failed` → warning 横幅「Windows 成功，WSL
    （{distro}）同步失败」，绝不显示纯「成功」；succeeded 追加「WSL 已同步
    v…」；skipped 追加原因。`wsl` 缺失时行为与旧版完全一致（旧后端兼容）。
  - 修复动作 `install_claude_method`：WSL 失败时错误写入
    `FixActionResult.Error` 并追加到 Message（Success 仍表示 Windows 侧）。
- 异步操作在 WSL 步骤期间保持 running，步骤标签翻为 `wsl_sync`（进度约
  92，位于 verify 与 refresh_cache 之间；monotonic 上报保证进度不回退），
  复用同一操作槽与同一套前端轮询；期间其它安装/升级操作照常返回 ErrBusy
  （与既有串行语义一致）。
- WSL 侧探测/安装有 30s 级预算（wslsetup runner 默认 probe 预算 + 长网络
  步骤显式超时），自动联动不会无限占用操作槽。

## 失败重试

- **Windows 失败**：现状语义不变，`wsl` 为 nil；重试走原入口（同一按钮/
  修复动作），成功后自动带上 WSL 步骤。
- **WSL 失败（Windows 已成功）**：无需重装 Windows 侧——去「WSL 内的 CLI」
  卡片点「升级」（`App.UpdateCLIInWSL` → `wslsetup.UpdateTool`，对未装工具
  同样会补装），或在 WSL 终端手动 `npm i -g <pkg>@latest`。下次任意一次
  Windows 侧 install/update 成功也会再次尝试同步。
- **门控 skipped 后想启用**：把该 CLI 的会话 shell 设为 `wsl`（或在 WSL
  内装一次该 CLI），之后的 Windows 侧操作即会同步。

## 消费者审计（部分失败不漏报）

`InstallResult`/`OperationState` 的既有消费者：桌面前端
`frontend/src/api/envcheck.ts` + `EnvCheckSettings.vue`（横幅，本文档所述
warning 逻辑）；修复动作经 `RunEnvFixAction` 返回 `FixActionResult`。
`internal/remote` 与 `mobile/` 无 envcheck 安装面消费（2026-10 审计：
grep `StartInstall|InstallResult|OperationState` 无命中）——远程侧无漏报面。
同步 `InstallTool`/`UpdateTool` API 桌面 UI 未直接调用（仅异步链），新增的
`WSL` 字段对未读它的消费者零影响（omitempty）。

## 测试锚点

- `internal/envcheck/wsl_cli_sync_test.go`：异步/同步/指定通道/修复动作的
  触发与门控（Windows 失败、uninstall、headroom、未注入回调均不触发），
  wsl_sync 步骤可见性，部分失败语义。
- 根目录 `app_wsl_cli_sync_test.go`：门控分派（skipped/Install/Update/
  已装走 Update）、claude_code→claude 与 omp 键映射、无发行版 nil、
  快照可信度（不可用/异发行版不判已装）、err 强制 failed 回归。
- 前端 `src/__tests__/views/settings/envcheckSolutions.test.ts`：wsl
  failed→warning / succeeded→追加 / skipped→原因 / 缺失→旧行为。

真机（Windows + WSL2）行为未在本轮覆盖：npm 网络失败语义、升级幂等、
WSL1 差异等按惯例标 U，归集成验证轮（P3）。
