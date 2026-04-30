<div align="center">

# Amagi CodeBox

**管理 Claude Code / OpenCode / Codex 多服务提供商配置的跨平台桌面应用**

[![Version](https://img.shields.io/badge/version-1.1.20-blue)](https://github.com/runrunrain/amagi-codebox)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24.0-00ADD8?logo=go)](https://go.dev)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vue.js)](https://vuejs.org)
[![Wails](https://img.shields.io/badge/Wails-v2.11.0-4342ea?logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAxMjguOSAxMjguOSI+PGNpcmNsZSBjeD0iNjQuNSIgY3k9IjY0LjUiIHI9IjY0LjUiIGZpbGw9IiM0MzQyZWEiLz48cGF0aCBkPSJNNjQuNSAzMmMxNy43IDAgMzIgMTQuMyAzMiA0NHMtMTQuMyA0NC0zMiA0NC0zMi0xNC4zLTMyLTQ0IDE0LjMtNDQgMzItNDR6IiBmaWxsPSIjZmZmIi8+PC9zdmc+)](https://wails.io)

[快速开始](#快速开始) | [文档](#文档) | [贡献](#贡献)

</div>

---

## 截图

<!-- TODO: 添加应用截图 -->

<div align="center">
  <img src="docs/screenshot-dashboard.png" alt="仪表盘界面" width="800"/>
  <p>仪表盘界面</p>
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

从 [GitHub Releases](https://github.com/runrunrain/amagi-codebox/releases) 下载最新版本：

- **Windows**：MSI 安装包或 EXE 可执行文件
- **macOS**：DMG 镜像文件

### 环境要求

| 平台 | 要求 |
|------|------|
| Windows | Windows 10 1903+ |
| macOS | macOS 10.15+ |
| Go | >= 1.24.0 |
| Node.js | >= 18 |
| Wails CLI | v2（`go install github.com/wailsapp/wails/v2/cmd/wails@latest`） |

### 构建命令

**Windows**

```powershell
# 开发模式（热重载）
wails dev

# 生产构建
wails build
```

**macOS**

```bash
# 开发模式（热重载）
wails dev

# 生产构建
wails build
```

---

## 技术栈

- [Wails](https://wails.io) v2.11.0 - 桌面框架
- [Go](https://go.dev) 1.24.0 - 后端
- [Vue 3](https://vuejs.org) + TypeScript - 前端
- [xterm.js](https://xtermjs.org) - 终端渲染
- [creack/pty](https://github.com/creack/pty) - macOS 伪终端
- [conpty](https://github.com/UserExistsError/conpty) - Windows 伪终端
- [gorilla/websocket](https://github.com/gorilla/websocket) - WebSocket 通信
- [Capacitor](https://capacitorjs.com) - 移动端客户端

---

## 项目结构

```
amagi-codebox/
├── app.go                   # 应用入口
├── main.go                  # Wails 启动
├── cmd/codebox/             # CLI 工具
├── internal/                # 后端服务模块
│   ├── amagi/               # Amagi 配置
│   ├── config/              # 提供商/预设
│   ├── envcheck/            # 环境检测
│   ├── envvars/             # 环境变量
│   ├── launcher/            # 进程启动
│   ├── logging/             # 日志
│   ├── opencodeconfig/      # OpenCode 配置
│   ├── paths/               # 路径管理
│   ├── platform/            # 平台抽象层
│   ├── plugin/              # 插件系统
│   ├── proxy/               # 代理注入
│   ├── pty/                 # 伪终端
│   ├── remote/              # 远程控制
│   ├── secrets/             # 密钥存储
│   ├── session/             # 会话管理
│   ├── settings/            # 应用设置
│   ├── tray/                # 系统托盘
│   ├── updater/             # 自动更新
│   └── workspace/           # 工作空间
├── frontend/src/            # Vue 3 前端
├── mobile/                  # 移动端客户端
├── API.md                   # API 文档
└── SECURITY.md              # 安全策略
```

---

## 配置文件

| 文件 | 说明 |
|------|------|
| config.json | 提供商和预设配置 |
| secrets.json | 加密的 API 密钥 |
| settings.json | 应用设置 |
| envvars.json | 自定义环境变量 |
| settings_amagi.json | Amagi 模型配置 |
| global-enabled.json | 全局启用插件 |

配置目录：`~/.amagi-codebox/`

---

## 远程控制 API

启用远程控制后，Amagi CodeBox 会在指定端口启动 HTTP 服务器，提供以下 API：

- `GET /api/status` - 获取提供商列表和默认配置
- `POST /api/launch` - 启动应用会话
- `GET /api/sessions` - 获取会话列表
- `DELETE /api/sessions/:id` - 停止会话
- `POST /api/regenerate-token` - 重新生成访问 Token
- `WebSocket /ws/terminal/:id` - 连接到内嵌终端

所有 API 请求需要在 `Authorization` 头中携带 Token。

---

## 文档

- [API 文档](API.md) - Wails 绑定的后端 API 参考
- [安全策略](SECURITY.md) - 数据加密和传输安全详细说明

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
