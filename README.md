<div align="center">

# Amagi CodeBox

**管理 Claude Code / OpenCode / Codex 多服务提供商配置的跨平台桌面应用**

[![Version](https://img.shields.io/badge/version-1.2.83-blue)](https://github.com/runrunrain/amagi-codebox)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25.0-00ADD8?logo=go)](https://go.dev)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vue.js)](https://vuejs.org)
[![Wails](https://img.shields.io/badge/Wails-v2.11.0-4342ea?logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAxMjguOSAxMjguOSI+PGNpcmNsZSBjeD0iNjQuNSIgY3k9IjY0LjUiIHI9IjY0LjUiIGZpbGw9IiM0MzQyZWEiLz48cGF0aCBkPSJNNjQuNSAzMmMxNy43IDAgMzIgMTQuMyAzMiA0NHMtMTQuMyA0NC0zMiA0NC0zMi0xNC4zLTMyLTQ0IDE0LjMtNDQgMzItNDR6IiBmaWxsPSIjZmZmIi8+PC9zdmc+)](https://wails.io)

[快速开始](#快速开始) | [核心架构](#核心架构) | [远程控制](#远程控制) | [文档](#文档) | [贡献](#贡献)

</div>

---

## 功能特性

- **跨平台支持**：Windows 10/11 和 macOS 原生支持
- **多应用管理**：Claude Code、OpenCode、Codex 三种应用统一管理
- **多服务提供商**：Anthropic、OpenAI、GLM、MiniMax、Kimi 等，支持自定义添加
- **预设配置管理**：每个提供商支持多套预设，可配置模型、温度、思考模式等
- **API 密钥安全存储**：Windows DPAPI / macOS Keychain 加密存储
- **代理注入引擎**：关键字匹配，自动注入自定义 Prompt
- **Claude Code 插件系统**：浏览市场、安装/卸载/更新插件
- **工作空间管理**：多工作空间创建、插件部署、冲突检测
- **内嵌终端**：xterm.js + ConPTY/macOS PTY，多 Tab 并发运行
- **远程控制**：HTTP API + WebSocket 终端桥接，支持移动端控制
- **自动更新**：GitHub Releases 检测，支持 Windows 和 macOS 一键下载安装
- **环境检测与一键修复**：CLI 工具安装状态检测、问题诊断、一键修复（修复 PATH、安装工具、安装 Node.js）
- **CLI 工具**：独立命令行工具，支持无头模式操作
- **单实例保护**：使用操作系统机制防止多实例运行
- **系统托盘驻留**：最小化到托盘，右键菜单退出

---

## 快速开始

### 下载安装

从 [GitHub Releases](https://github.com/runrunrain/amagi-codebox/releases) 下载对应平台的发行包。

- **Windows**：解压后运行 `amagi-codebox.exe`
- **macOS**：解压后拖入「应用程序」，首次运行若被 Gatekeeper 拦截，前往「系统设置 > 隐私与安全性」点击「仍要打开」

应用启动后在 `~/.amagi-codebox/` 生成配置目录，单实例保护会阻止重复启动，关闭窗口可驻留系统托盘。

### 运行环境

| 用途 | 要求 |
|------|------|
| 运行（Windows） | Windows 10 1903+ |
| 运行（macOS） | macOS 10.15+ |
| 自行构建 | Go 1.25+、Node.js 18+、Wails CLI v2 |

### 构建命令

```bash
# 安装 Wails CLI（首次）
go install github.com/wailsapp/wails/v2/cmd/wails@latest

wails dev     # 开发模式（热重载）
wails build   # 生产构建，产物在 build/bin/
```

一键构建脚本：`./build.sh`（macOS/Linux）、`build.bat`（Windows），会依次构建桌面前端、移动端前端与桌面主二进制，并通过 git tag / `wails.json` 注入版本号。

---

## 核心架构

Amagi CodeBox 基于 **Wails v2**：Go 后端与 Vue 3 + TypeScript 前端编译为**单一二进制**，前端产物由 `//go:embed` 嵌入。

**绑定主干**：`main.go` 通过 Wails `Bind` 把 `App` 枢纽与 14 个服务 struct（共 15 个绑定）暴露给前端。`app.go` 是中央枢纽，持有所有服务指针并负责会话编排、环境检测、远程控制等跨服务协调。每个服务 struct 的导出方法会被 Wails 自动生成为 TypeScript 绑定（`frontend/wailsjs/go/`），前端经 `frontend/src/api/*.ts` 包装层与 Pinia store 调用。

**后端服务包**：`internal/` 下 22 个服务包，各遵循「一个 `Service`/`ConfigService` struct + `New...()` 构造函数 + 导出方法」范式，包括 `config`（提供商/预设）、`secrets`（密钥存储）、`session`（会话管理）、`pty`（伪终端）、`plugin`/`codexplugin`（插件系统）、`proxy`（代理注入）、`headroom`（上下文压缩）、`remote`（远程控制）、`envcheck`（环境检测与修复）、`updater`（自动更新）、`workspace`（工作空间）等。

**三种应用类型**：`claudecode` / `opencode` / `codex`（外加已弃用的 `amagicode`）。`LaunchSession` 是会话启动核心入口，按预设解析提供商、编排代理与 headroom 链路、注入环境变量与 `--session-id`，最终在 PTY 中启动 CLI。

**跨平台**：平台差异通过 Go `//go:build` 约束在编译期分流（如 secrets 在 Windows 用 DPAPI、macOS 用 Keychain、Linux 为不支持），启动时由 `platform.CurrentCapabilities()` 一次性解析能力集合，运行期只读。

**移动端**：另有一份独立的 Capacitor 前端（`mobile/`）经 `//go:embed` 嵌入，由远程控制 HTTP 服务器对外提供，用于从手机控制桌面端。

> 更深入的架构、调用链与各平台实现差异见 [架构文档](docs/developer/architecture.md) 与 [前后端桥接](docs/developer/frontend-backend.md)。

---

## 项目结构

```
amagi-codebox/
├── main.go                  # Wails 启动、版本注入、资源嵌入
├── app.go                   # 应用枢纽：绑定 + 会话/环境/远程编排
├── cmd/codebox/             # 独立 CLI 工具（无头模式）
├── internal/                # 后端服务模块（22 个包，见上）
├── frontend/                # Vue 3 + TypeScript 前端
│   └── src/api/             # 包装 Wails 绑定的类型化 API 层
├── mobile/                  # Capacitor 移动端客户端
├── docs/                    # 项目文档（按受众分层）
├── build.sh / build.bat     # 跨平台一键构建脚本
└── wails.json               # Wails 构建配置与产品版本
```

---

## 配置

配置目录：`~/.amagi-codebox/`

| 文件 | 说明 |
|------|------|
| `config.json` | 提供商与预设（含 `terminal_presets`） |
| `secrets.json` | 加密的 API 密钥（Windows DPAPI / macOS Keychain） |
| `settings.json` | 应用设置（远程端口、移动端 Web 根、GitHub Token 等） |
| `envvars.json` | 自定义环境变量 |
| `settings_amagi.json` | Amagi 模型配置 |
| `global-enabled.json` | 全局启用插件 |

**提供商与预设模型**：每个 Provider 支持多套 Preset，Preset 携带 `Parameters`（模型、温度、max_tokens）、`ThinkingConfig`（思考模式）、`ContextWindowConfig`（上下文窗口）等。内置 anthropic、openai、glm、minimax、kimi 五个默认提供商，可自定义添加。

> 字段定义与完整结构见 [提供商与预设配置](docs/user/providers.md)。

---

## 远程控制

启用远程控制后，Amagi CodeBox 在指定端口（默认 8680）启动 HTTP + WebSocket 服务器，供移动端远程控制桌面端。所有请求需在 `Authorization` 头携带 Token（Token 在桌面端生成，无法经远程端点重置）。

核心端点（核实自 `internal/remote/handlers.go`）：

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/info` | 服务信息 |
| `GET` | `/api/sessions` | 会话列表 |
| `POST` | `/api/sessions/launch` | 启动 Claude Code 会话 |
| `POST` | `/api/sessions/launch-codex` | 启动 Codex 会话 |
| `POST` | `/api/sessions/launch-opencode` | 启动 OpenCode 会话 |
| `DELETE` | `/api/sessions/{id}` | 停止会话 |
| `GET` / `PUT` | `/api/providers`、`/api/providers/{name}` | 提供商读写 |
| `GET` / `PUT` | `/api/settings` | 应用设置读写 |
| `GET` | `/api/logs`、`/api/paths`、`/api/secrets/diagnostics` | 日志、路径、密钥诊断 |
| `WebSocket` | `/ws/terminal/{sessionID}` | 终端桥接 |

> 完整端点、鉴权流程（Token / launch grant / 本地 cookie）与移动端连接见 [远程控制与移动端](docs/user/remote-mobile.md)。

---

## 文档

完整文档位于 [`docs/`](docs/README.md)，按受众分层：

- **[用户文档](docs/user/)** — 安装、界面使用、提供商配置、终端、插件、远程控制、常见问题
- **[开发者文档](docs/developer/)** — 架构、前后端桥接、跨平台机制、构建开发、测试、API 参考
- **[运维文档](docs/ops/)** — 打包发布、版本管理、CI/CD
- **[API 参考](docs/api.md)** — Wails 绑定的后端 API 全量方法清单
- **[安全策略](docs/security.md)** — 数据加密与传输安全
- **[CLAUDE.md](CLAUDE.md)** — 面向 AI 助手的项目导览

---

## 贡献

欢迎提交 Issue 和 Pull Request。

1. Fork 本仓库
2. 创建功能分支 (`git checkout -b feature/your-feature`)
3. 提交变更 (`git commit -m 'Add your feature'`)
4. 推送到分支 (`git push origin feature/your-feature`)
5. 创建 Pull Request

---

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件
