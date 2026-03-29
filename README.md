# Amagi CodeBox

一个用于管理 Claude Code / OpenCode / Codex 多服务提供商配置的 Windows 桌面应用，集成内嵌终端，支持多会话并发运行。


## 功能特性

### 核心功能
- **多应用支持**：支持 Claude Code、OpenCode、Codex 三种应用
- **多服务提供商管理**：内置 Anthropic、OpenAI、GLM、MiniMax、Kimi 等主流提供商，支持自定义添加
- **预设配置管理**：每个提供商支持多套预设，可配置模型、温度、top_p、Max Tokens、思考模式等
- **API 密钥加密存储**：基于 Windows DPAPI 加密，密钥不以明文存储
- **代理注入引擎**：关键字匹配，自动向系统消息注入自定义 Prompt
- **环境变量管理**：支持自定义环境变量的增删改查、导入导出
- **配置导入导出**：支持将提供商配置导出为 JSON 文件，或从文件导入

### 启动模式
| 模式 | 说明 |
|------|------|
| **内嵌终端** | 在应用窗口内运行应用（xterm.js + ConPTY） |
| **独立窗口** | 在新的终端窗口中启动应用 |

### 内嵌终端
- 基于 Windows ConPTY + xterm.js 实现，完整的终端仿真
- 支持多 Tab，多会话并发运行，标签间无缝切换
- 会话跨页面导航保持活跃（keep-alive 机制）
- 滚动缓冲行数可配置（默认 100,000 行）
- 完整 UTF-8 编码支持（base64 传输）
- **WebGL 渲染器**：使用 WebGL 渲染消除滚动重影，提升性能
- **文件路径链接**：终端中的文件路径可点击，在编辑器中打开
- **剪贴板图片粘贴**：支持直接粘贴剪贴板中的截图

### 远程控制
- **移动端支持**：通过局域网 HTTP API，允许移动端控制 Amagi CodeBox
- **QR 码连接**：扫描 QR 码即可自动配置服务器地址和 Token
- **Token 认证**：每次连接需要 Token，确保安全
- **移动端 Web 服务**：可配置移动端前端静态文件目录

### Shell 选择
- 直接启动应用（无 Shell 包装）
- PowerShell 7（pwsh）
- Windows PowerShell
- CMD
- 支持添加自定义 Shell 可执行文件路径

### 其他特性
- **单实例保护**：使用 Windows 互斥量机制，防止多实例运行
- **会话管理**：实时追踪所有会话状态（运行中/已退出/已停止/失败）
- **日志面板**：可查看应用运行日志
- **系统托盘驻留**：最小化到托盘，右键菜单退出
- **持久化设置**：启动模式、提供商、预设、Shell 等默认值跨会话保存
- **Toast 通知**：操作成功/失败即时提示

## 技术栈

| 层级 | 技术 |
|------|------|
| 桌面框架 | Wails v2.11.0 |
| 后端语言 | Go 1.23 |
| 前端框架 | Vue 3 + TypeScript |
| 路由 | vue-router 4 |
| 终端渲染 | @xterm/xterm 6.0.0 + @xterm/addon-fit + @xterm/addon-webgl |
| 伪终端 | conpty v0.1.4（Windows ConPTY） |
| 加密 | Windows DPAPI（billgraziano/dpapi） |
| 系统托盘 | energye/systray v1.0.3 |
| JSON 操作 | tidwall/gjson + tidwall/sjson |
| QR 码生成 | qrcode |

## 项目结构

```
amagi-codebox/
├── app.go                    # 应用入口，服务生命周期协调
├── main.go                   # Wails 启动入口，单实例保护
├── internal/
│   ├── config/               # 提供商和预设配置（~/.amagi-codebox/config.json）
│   ├── secrets/              # API 密钥 DPAPI 加密存储
│   ├── launcher/             # Claude Code 进程启动器
│   ├── proxy/                # 代理注入引擎（关键字匹配 + Prompt 注入）
│   ├── pty/                  # Windows ConPTY 伪终端封装
│   ├── session/              # 多会话生命周期管理
│   ├── settings/             # 应用设置（仪表盘默认值、Shell 路径、终端参数）
│   ├── logging/              # 日志服务
│   ├── paths/                # 配置目录路径管理
│   ├── envvars/              # 环境变量管理服务
│   ├── remote/               # 远程控制 HTTP 服务器
│   └── tray/                 # 系统托盘图标与菜单
├── frontend/
│   └── src/
│       ├── views/
│   │       ├── Dashboard.vue      # 仪表盘（启动应用）
│   │       ├── Terminals.vue      # 内嵌终端多 Tab 页面
│   │       ├── Providers.vue      # 提供商列表（含 API 密钥管理）
│   │       ├── ProviderDetail.vue # 提供商预设详情
│   │       ├── PluginsView.vue    # 插件管理
│   │       ├── Rules.vue          # 注入规则管理
│   │       ├── EnvVarsView.vue    # 环境变量管理
│   │       ├── Logs.vue           # 日志面板
│   │       └── Settings.vue       # 应用设置页（含远程控制）
│       ├── components/
│       │   ├── layout/            # AppLayout、Sidebar
│       │   └── common/            # Toast 通知组件
│       └── composables/
│           ├── useDashboardState.ts   # 仪表盘跨路由状态持久化
│           └── useToast.ts            # Toast 通知
├── mobile/                       # 移动端 Web 客户端（Vue 3 + Capacitor）
├── cmd/codebox/                  # CLI 工具
├── mobile/                       # 移动端 Web 客户端（Vue 3 + Capacitor）
├── cmd/codebox/                  # CLI 工具
└── build/
    └── windows/icon.ico       # 应用图标
```

## 配置文件

| 文件 | 内容 |
|------|------|
| `~/.amagi-codebox/config.json` | 提供商和预设配置 |
| `~/.amagi-codebox/secrets.json` | DPAPI 加密的 API 密钥 |
| `~/.amagi-codebox/settings.json` | 应用设置（默认值、Shell 路径、终端参数） |
| `~/.amagi-codebox/envvars.json` | 自定义环境变量 |

## 环境要求

- Windows 10/11（ConPTY 需要 Windows 10 1903+）
- Go >= 1.23
- Node.js >= 18
- Wails CLI v2：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`

## 构建和运行

```powershell
# 开发模式（热重载）
wails dev

# 生产构建（EXE 输出到 build/bin/）
wails build
```

### 安装

从 [GitHub Releases](https://github.com/yourusername/amagi-codebox/releases) 下载最新的 MSI 安装包或 EXE 可执行文件。

### 部署到用户目录（可选）

```powershell
Copy-Item build\bin\amagi-codebox.exe "$env:USERPROFILE\.amagi-codebox\amagi-codebox.exe"
```

## 提供商与预设配置说明

应用预置多个提供商：Anthropic、OpenAI、GLM、MiniMax、Kimi。用户可自行添加更多提供商。

每个提供商包含：
- `base_url`：API 服务地址
- `auth_key`：认证环境变量类型（`ANTHROPIC_API_KEY`、`OPENAI_API_KEY` 或 `ANTHROPIC_AUTH_TOKEN`）
- `type`：提供商类型（`anthropic` 或 `openai`）
- `presets`：预设配置列表

每个预设包含：
- `model`：模型名称
- `temperature`：温度（输出随机性）
- `top_p`：核采样参数
- `max_tokens`：最大生成 Token 数
- `stream`：是否启用流式输出
- `thinking`：思考模式配置（`type`: enabled/disabled，`budgetTokens`：思考预算）

## 注入规则说明

### 规则字段

| 字段 | 说明 |
|------|------|
| `id` | 规则唯一标识（UUID） |
| `name` | 规则名称 |
| `keywords` | 关键词列表（空列表 = 默认规则，始终注入） |
| `prompt` | 注入的 Prompt 文本 |
| `enabled` | 启用状态 |
| `priority` | 优先级（数字越大越先执行） |

### 工作原理

1. 代理层拦截应用发出的请求
2. 扫描用户消息是否包含规则关键词
3. 匹配成功后将对应 Prompt 注入到系统消息
4. 多规则匹配时按优先级从高到低依次执行

## 远程控制 API

启用远程控制后，Amagi CodeBox 会在指定端口启动 HTTP 服务器，提供以下 API：

- `GET /api/status` - 获取提供商列表和默认配置
- `POST /api/launch` - 启动应用会话
- `GET /api/sessions` - 获取会话列表
- `DELETE /api/sessions/:id` - 停止会话
- `WebSocket /ws/terminal/:id` - 连接到内嵌终端

所有 API 请求需要在 `Authorization` 头中携带 Token。

## 贡献

欢迎提交 Issue 和 Pull Request。

1. Fork 本仓库
2. 创建功能分支 (`git checkout -b feature/your-feature`)
3. 提交变更 (`git commit -m 'Add your feature'`)
4. 推送到分支 (`git push origin feature/your-feature`)
5. 创建 Pull Request

## 贡献

欢迎提交 Issue 和 Pull Request。

1. Fork 本仓库
2. 创建功能分支 (`git checkout -b feature/your-feature`)
3. 提交变更 (`git commit -m 'Add your feature'`)
4. 推送到分支 (`git push origin feature/your-feature`)
5. 创建 Pull Request

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件 License - 详见 [LICENSE](LICENSE) 文件
