# AI CLI 本地 Web UI 能力调研（2025–2026）

调研时间：2026-02，基于官方文档与 GitHub 仓库当前 master。

## 1. OpenCode（sst/opencode）— 有，Web UI 与 headless API 并存，均活跃维护

- **`opencode web`**：官方浏览器 UI（前端在仓库 `packages/app`）。默认绑定 `127.0.0.1` 随机可用端口并自动开浏览器；`--port`、`--hostname 0.0.0.0`、mDNS（`--mdns`，广告为 `opencode.local`）、`--cors` 均可配；可 `opencode attach http://localhost:4096` 把 TUI 挂到同一 server 共享会话。维护状态：活跃（docs 持续更新，desktop 版内置"web server mirror"自动启动，见 issue #11997）。
- **`opencode serve`**：headless HTTP server API，默认端口 **4096**、`127.0.0.1`。架构上 TUI 本身就是 server 的客户端（client/server 一体）。关键端点：
  - 健康检查 `GET /global/health` → `{healthy, version}`
  - OpenAPI 3.1 规范 `GET /doc`；SSE `GET /global/event`
  - 会话：`GET/POST /session`、`POST /session/:id/message`、`prompt_async`（异步投递）、`abort`、`fork`、`permissions` 审批
  - 文件/搜索：`/file`、`/find`、`/find/symbol`；MCP：`/mcp`；TUI 驱动：`/tui/*`
- **鉴权（两者共用）**：`OPENCODE_SERVER_PASSWORD` 启用 HTTP Basic Auth，用户名默认 `opencode`，可用 `OPENCODE_SERVER_USERNAME` 覆盖。
- 历史说明：Web UI 曾有移除/回归波动（未逐一核实具体版本），当前明确在维护。

来源：https://opencode.ai/docs/web/ 、https://opencode.ai/docs/server/ 、https://github.com/sst/opencode/issues/11997

## 2. OpenAI Codex CLI（openai/codex）— 无本地 Web UI

- 当前 master `codex-rs/cli/src/main.rs` 子命令全表：exec / review / login / logout / mcp / plugin / mcp-server / **app-server（实验性）** / remote-control / **app（桌面 App，macOS/Windows）** / completion / update / doctor / sandbox / debug / apply / resume / archive / delete / migrate-rollouts / unarchive / fork / cloud / exec-server / features。**无 `codex web`**。
- 官方本地界面只有 TUI、IDE 扩展、`codex app` 桌面 App；`codex app-server` 是给 ChatGPT 桌面端/IDE 用的 WebSocket JSON-RPC 后端（有 WS 鉴权：capability-token / signed-bearer-token），不是浏览器 UI。
- "web UI" 在官方语境指 ChatGPT 云端（仓库 issue label `codex-web` 指向 Codex web/Cloud 体验）；社区有 feature request（如 #28185 "Let ChatGPT Web connect to local Codex"）与第三方壳（0xcaff/codex-web、friuns2/codex-web-ui 等），官方无本地 Web UI roadmap 承诺。

来源：https://github.com/openai/codex/blob/master/codex-rs/cli/src/main.rs 、https://github.com/openai/codex/issues/28185

## 3. Claude Code（anthropics/claude-code）— 官方无本地 Web UI，社区壳以"代理式"为主

- 官方本地终端外界面为 IDE 扩展与 enterprise gateway；官方 Web 是云端 "Claude Code on the web"（claude.ai/code），非本地。
- 社区项目（均需本机已装 `claude` CLI）：
  - **sugyan/claude-code-webui**（npm `claude-code-webui`，默认端口 8080，无内置鉴权）：**代理式为主**——后端 spawn `claude` CLI 子进程并流式转发输出（`backend/cli/`、`handlers/chat.ts`）；会话历史列表另通过解析本地 session JSONL 文件恢复（`backend/history/parser.ts`，即 `~/.claude/projects`）。混合型：实时走进程代理，历史走文件解析。
  - **happier-dev/happier**（原 Happy Coder/ido-plenus/happy-coder，Web/桌面/移动客户端）：**代理式**——本机 daemon（`apps/cli`，127.0.0.1 控制端口 + Socket.IO 云 relay，端到端加密）spawn CLI 会话进程，经 RPC 桥暴露 bash/文件/搜索；不解析 session 文件。
  - 其他：siteboon/claudecodeui、claudeck 等同类壳。

来源：https://github.com/sugyan/claude-code-webui 、https://github.com/happier-dev/happier/tree/master/docs/cli-architecture.md 、https://github.com/anthropics/claude-code

## 对 Wails 内嵌 iframe 集成的关键结论

1. OpenCode 最成熟：`opencode serve` 即官方 headless API（4096 端口、`/global/health` 健康检查、Basic Auth、OpenAPI `/doc`），内嵌 iframe 只需起 serve + 可选 `opencode web` 前端；Web UI 也可直接 iframe（同源 CORS 需配 `--cors`）。
2. Codex 无本地 Web 面，需自建壳（app-server WS 协议复杂，或 TUI 代理）。
3. Claude Code 社区壳均需代理 CLI 子进程，无官方 API；与 Codex 一样"壳自建"成本最高。

未确认项：OpenCode Web UI 历史移除/回归的具体版本；Codex 官方 roadmap 对本地 Web UI 无明确承诺（仅社区 issue）。
