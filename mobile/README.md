# Amagi CodeBox Mobile

Amagi CodeBox 桌面端的远程控制应用。通过浏览器或 Android APK 连接 Amagi CodeBox Remote API，实现会话管理、终端查看、服务商配置等功能。

支持两种使用方式：**Web 浏览器**（任意设备）和 **Android APK**（原生应用）。

## 技术栈

| 层级 | 技术 |
|------|------|
| 移动框架 | Capacitor 8 |
| 前端 | Vue 3 + TypeScript + Vite 8 |
| 终端渲染 | xterm.js 6 |
| 通信 | WebSocket（终端流）+ REST API（管理操作） |
| 认证 | Bearer Token |
| 构建产物 | 静态 Web 页面 / Android APK |

## 项目结构

```
src/
  api/
    client.ts          # REST API 客户端（20个方法，Bearer Token 认证）
    websocket.ts       # WebSocket 终端连接（双向 base64 编码）
  components/
    AppLayout.vue      # 应用布局框架
    ConnectionStatus.vue  # 连接状态指示器
    DrawerNav.vue      # 侧边导航抽屉
  views/
    ConnectPage.vue    # 连接页：服务器地址、Token 输入、QR 扫码
    DashboardPage.vue  # 仪表盘：活跃会话概览
    TerminalPage.vue   # 终端页：xterm.js 渲染 + 输入转发
    SessionsPage.vue   # 会话管理：启动/停止/移除会话
    ProvidersPage.vue  # Provider 管理：查看/编辑 AI 服务配置
    SettingsPage.vue   # 设置页：连接管理、应用设置
  stores/
    connection.ts      # 连接状态管理（服务器地址、Token、在线状态）
  router/
    index.ts           # Vue Router 路由定义
  main.ts              # 应用入口
  App.vue              # 根组件
```

---

## Web 端远程访问指南

构建产物为纯静态文件（HTML + JS + CSS），使用相对路径和 Hash 路由，可部署到任意 HTTP 服务器。

### 架构说明

Web 页面和 Amagi CodeBox Remote API 是两个独立的服务：

```
任意设备的浏览器
    |
    |  HTTP (加载页面)
    v
静态文件服务 (:8680)          <-- 托管 dist/ 目录

任意设备的浏览器
    |
    |  REST API + WebSocket (数据通信)
    v
Amagi CodeBox Remote API (:8680)  <-- 通过 FRP 隧道到达桌面端
    |
    v
Amagi CodeBox 桌面端 (PTY 终端)
```

### 方式一：部署到远程服务器（推荐）

将 `dist/` 目录上传到公网服务器，用 nginx 或 Python 提供静态文件服务。

**上传构建产物：**

```bash
scp -r dist/* root@你的服务器IP:/var/www/amagi-codebox-mobile/
```

**nginx 配置：**

```nginx
server {
    listen 8680;
    server_name _;
    root /var/www/amagi-codebox-mobile;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

```bash
nginx -t && systemctl reload nginx
```

**或用 Python 快速启动（无需 nginx）：**

```bash
cd /var/www/amagi-codebox-mobile
python3 -m http.server 8680 --bind 0.0.0.0
```

浏览器访问 `http://你的服务器IP:8680`。

### 方式二：本地开发服务器 + FRP 隧道

在本地启动 Vite 开发服务器，通过 FRP 隧道暴露到公网。

```bash
npm run dev -- --host 0.0.0.0
```

在 frpc.toml 中增加隧道规则：

```toml
[[proxies]]
name = "amagi-codebox-mobile-web"
type = "tcp"
localIP = "127.0.0.1"
localPort = 5178        # Vite 实际端口，见终端输出
remotePort = 8680       # 远程服务器暴露端口
```

重启 frpc 后，浏览器访问 `http://你的服务器IP:8680`。

### 方式三：局域网直连

手机和电脑在同一 WiFi 下，直接访问电脑的局域网 IP。

```bash
npm run dev -- --host 0.0.0.0
ipconfig    # 查看本机局域网 IP，如 192.168.1.100
```

手机浏览器访问 `http://192.168.1.100:5178`。

### 连接页面填写说明

打开 Web 页面后，在连接页输入两个字段：

| 字段 | 说明 | 示例 |
|------|------|------|
| Server URL | Amagi CodeBox Remote API 地址（不是 Web 页面地址） | `http://你的服务器IP:8680` |
| Token | Amagi CodeBox 桌面端生成的 Bearer Token | 在桌面端设置页查看 |

也可使用 Scan QR Code 扫描桌面端生成的二维码自动填入。

### 页面功能

| 页面 | 路径 | 功能 |
|------|------|------|
| Connect | `/#/` | 输入服务器地址和 Token 连接 |
| Dashboard | `/#/dashboard` | 活跃会话概览 |
| Terminal | `/#/terminal/{id}` | Observer 模式终端查看（不影响桌面端尺寸） |
| Sessions | `/#/sessions` | 启动/停止/管理会话 |
| Providers | `/#/providers` | AI 服务商配置管理 |
| Settings | `/#/settings` | 连接和应用设置 |

### 注意事项

- Web 页面地址和 Server URL 是不同的服务，端口不同
- 终端页使用 Observer 模式：只查看输出，不会改变桌面端的 PTY 窗口尺寸
- Token 可在 Amagi CodeBox 桌面端的 Remote API 设置中查看或重新生成
- 首次连接成功后，Server URL 和 Token 会保存在浏览器 localStorage 中

---

## 开发环境要求

- Node.js >= 18
- JDK 21（路径：`C:/jdk21/jdk-21.0.10+7/`）
- Android SDK（路径：`C:\Android\Sdk`）
- Amagi CodeBox 桌面端（Remote API 开启，默认端口 8680）

## 开发调试

### 方式一：浏览器预览（日常开发，推荐）

```bash
npm run dev
```

Vite 启动 dev server（默认 `http://localhost:5173`），浏览器直接访问。支持 HMR 热更新，改代码即时生效。

适用：UI 开发、API 调试、页面逻辑。浏览器完全覆盖本项目的 WebSocket + REST 功能。

### 方式二：Android 真机/模拟器

```bash
npm run build
npx cap sync android
npx cap run android       # 直接运行到设备
# 或
npx cap open android      # 打开 Android Studio
```

适用：测试触屏交互、Capacitor 原生功能、最终验收。

### 方式三：Live Reload（真机 + 热更新）

临时修改 `capacitor.config.ts`，添加 `url` 指向开发机 Vite dev server：

```typescript
server: {
  androidScheme: 'https',
  url: 'http://你的局域网IP:5173',  // 临时添加
  cleartext: true,                    // 允许 HTTP
},
```

然后双终端启动：

```bash
# 终端 1
npm run dev

# 终端 2
npx cap run android
```

手机上的 App 直接加载电脑的 dev server，代码改动实时反映。手机和电脑须在同一局域网。

**发布前必须移除 `url` 和 `cleartext` 配置。**

### 推荐开发流程

```
日常开发 --> npm run dev（浏览器 HMR）
需要测试移动端适配 --> Live Reload（方式三）
准备发版 --> 构建 APK（方式二）
```

## 构建 APK

```bash
npm run build
npx cap sync android
cd android
./gradlew assembleRelease
```

产物位置：`android/app/build/outputs/apk/release/app-release.apk`（约 3.2MB）

## 与桌面端的关系

本应用是 Amagi CodeBox 桌面端的远程控制前端，依赖桌面端的 Remote API 服务：

- 桌面端项目：同一仓库内的 amagi-codebox 主项目（仓库根目录）
- Remote API 端口：8680（Bearer Token 认证，可在桌面端设置中修改）
- 会话模式：Claude / OpenCode / Codex（与桌面端 AppType 对应）
- WebSocket 协议：客户端发送 `input`(base64) / `resize`(cols,rows)，服务端推送 `output`(base64) / `exit`(code)

## 常用命令

| 命令 | 说明 |
|------|------|
| `npm run dev` | 启动 Vite dev server |
| `npm run build` | TypeScript 类型检查 + Vite 构建 |
| `npm run preview` | 预览构建产物 |
| `npx cap sync android` | 同步 Web 产物到 Android 项目 |
| `npx cap run android` | 构建并运行到 Android 设备 |
| `npx cap open android` | 用 Android Studio 打开项目 |
