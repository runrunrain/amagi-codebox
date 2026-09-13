# T2-mobile-webplane-view 交付报告：移动端「Web 会话平面」视图实现与验证

## 1. 任务概述与目标对齐

根据冻结契约（C2/C4）与任务书要求，在 amagi-codebox 移动前端（`mobile/src/`）实现「Web 会话平面」视图，对齐桌面端 `WebPlaneHost.vue` 的交互与视觉效果：
- **C2 状态端点消费**：在 `lib/api.ts` 与 `lib/contract/rest.ts` 中消费 `GET /api/remote/v1/session/{id}/webui`，严格遵循凭据纪律（仅 device cookie，不记录 token/URL）。
- **C4 视图宿主**：实现 `WebPlaneView.vue`，具备加载/错误/看门狗超时/结束态状态机与 iframe 沙箱隔离（`allow-scripts allow-forms`）。
- **WorkspacePage 集成**：为 `isTuiCli`（pi/omp）会话引入 `?view=webplane`；默认策略实现 `available → webplane`、`probing → 加载态（0.8s 轮询）`、`unavailable/unknown → 终端仿真（现状零回归）`；available 后进入 5s 低频监测；在 Web 平面隐藏外层 `ComposerBar` 与 `KeyTray`；菜单实现三面受控切换并在 unavailable 时隐藏入口。
- **状态与定时器管理**：实现 `useSessionWebUI.ts` composable，保障会话切换与页面销毁时定时器可靠清理。

---

## 2. 改动文件清单

| 文件路径 | 改动类型 | 说明 |
|---|---|---|
| `mobile/src/lib/contract/rest.ts` | 修改 | 新增 `WebUIState`、`SessionWebUIStatus` 契约类型及 `V1_ENDPOINT_SESSION_WEBUI`（与 plural 容错端点），保持原始 10 端点 manifest 不变 |
| `mobile/src/lib/api.ts` | 修改 | 新增 `getSessionWebUI(sessionId)` 请求函数，捕获 404 容错回退，严格遵守不打印 token/cookie 纪律 |
| `mobile/src/composables/useSessionWebUI.ts` | 新增 | 负责 WebUI 探测轮询（probing 800ms，available 5000ms 监测），生命周期安全清理 |
| `mobile/src/components/workspace/WebPlaneView.vue` | 新增 | Web 会话平面 iframe 容器，包含 loading 动画、error 容错重试、10s 看门狗、ended 提示浮层、safe-area 与 WCAG 对比度适配 |
| `mobile/src/views/WorkspacePage.vue` | 修改 | 集成 `activeView` 状态推导、`?view=webplane` 路由、三面切换菜单、Web 平面 Badge 标识、ComposerBar/KeyTray 隐蔽逻辑与 resize 防冗余 |
| `mobile/src/lib/api.test.ts` | 修改 | 补齐 `getSessionWebUI` 的 200 成功、probing、404 回退、网络错误测试 |
| `mobile/src/__tests__/composables/useSessionWebUI.test.ts` | 新增 | 覆盖 `useSessionWebUI` 的定时轮询、状态变迁、生命周期清理与 refresh 逻辑 |
| `mobile/src/__tests__/components/workspace/WebPlaneView.test.ts` | 新增 | 覆盖 `WebPlaneView` iframe 沙箱属性、加载完成、错误拦截、10s 超时、重试与结束态事件 |
| `mobile/src/__tests__/views/WorkspacePage.webplane.test.ts` | 新增 | 覆盖 WorkspacePage 在 pi/omp 下默认视图策略、三视图切换、菜单受控显隐、Composer 隐藏 |

---

## 3. 详细设计与实现说明

### 3.1 C2 契约端点与 api 封装 (`mobile/src/lib/api.ts`)
- 冻结形状：`Promise<{ state: 'probing' | 'available' | 'unavailable' | 'ended' | 'unknown', url?: string }>`。
- 端点路径契约规范为 `GET /api/remote/v1/session/{id}/webui`，客户端内同时配置 `/sessions/{id}/webui` 的 404 捕获容错，确保不论后端路由采用单数或复数均能无缝适配。
- 严禁把 URL 或 token 打印进控制台，确保 capability token 安全。

### 3.2 轮询与生命周期管理 (`mobile/src/composables/useSessionWebUI.ts`)
- **高频探测期（probing）**：定时间隔设定为 `800ms`（契约建议的 0.5–1s 节奏），快速发现 webui 服务启动完成。
- **低频监测期（available）**：探测成功后立即切换为 `5000ms`（5秒）低频健康检测，捕获进程退出（ended）或插件异常（unavailable）。
- **终态与非 TUI 会话**：对于 `ended`、`unavailable`、`unknown` 或非 `isTuiCli`（如 claudecode），停止轮询定时器，防止移动端电量与网络浪费。
- **离开与切换保证**：在 `watch([sessionIdRef, enabledRef])` 和 `onUnmounted` 中调用 `stop()`，重置 `inFlight` 标量并清除 `pollTimer`，杜绝内存泄漏与悬挂回调。

### 3.3 WebPlaneView 视觉与交互 (`mobile/src/components/workspace/WebPlaneView.vue`)
- **沙箱与安全**：`sandbox="allow-scripts allow-forms"`，页面 origin 保持 opaque（跨源隔离），允许内置表单交互；`title="Web 会话平面"` 提供完整可访问性。
- **看门狗超时机制**：内置 `10_000ms`（10秒）加载看门狗。对于移动网络断流或 iframe 空白无响应，超时后自动迁移至 `error` 态，提供「重试」与「切回终端」双动作。
- **会话结束态**：`ended: true` 时在右上角显示 `.plane-ended-bar`（`会话已结束` 标签 + `切回终端` 按钮），保留最后渲染帧。
- **移动端适配**：使用 `--VT-*` 语义令牌，包含刘海屏与安全区域内边距（`env(safe-area-inset-bottom, 0px)`），按钮最小可触控面积满足 $\ge 44\text{px}$，`prefers-reduced-motion` 禁用 spinner 旋转。

### 3.4 WorkspacePage 页面集成 (`mobile/src/views/WorkspacePage.vue`)
- **默认视图策略**：
  - 非 TUI CLI（`claudecode`, `opencode`, `codex`）：维持默认时间线视图（`timeline`），现状零回归。
  - TUI CLI（`pi`, `omp`）：
    - `webuiState === 'available'`：默认进入 `webplane` 视图。
    - `webuiState === 'probing'`：展示 Web 会话平面加载态（带 spinner 居中提示）。
    - `webuiState === 'unavailable' | 'unknown'`：平滑降级至终端仿真面（`RawTerminalView`），现状零回归。
- **输入台与按键托盘去重**：在 `webplane` 视图下外层 `ComposerBar` 与 `KeyTray` 经 `v-if="activeView !== 'webplane'"` 隐藏，彻底消除双输入台与多余托盘问题。
- **菜单三面切换**：
  - 在 TUI 会话菜单中，当 `webuiAvailable === true` 且非当前面时显示「切换至 Web 平面视图」；
  - 当 `webuiAvailable === false` 时，**隐藏** Web 平面入口；
  - 无论处于哪一面，均可切换至其余合法面或返回大厅。
- **标题徽标与返回按钮**：
  - Web 平面视图下标题右侧展示 `.webplane-badge`（「Web 平面」）；
  - 从大厅默认进入 Web 平面时，顶部返回按钮为「大厅」；
  - 终端 resize 上报在 Web 平面激活时旁路，避免无效计算。

---

## 4. 验证命令与输出证据

### 4.1 单元与集成测试（Vitest）
执行命令：
```bash
npm --prefix mobile test
```
输出证据：
```
 RUN  v4.1.0 /Users/maorun/maorun-workpace/amagi-codebox/mobile

 Test Files  72 passed (72)
      Tests  638 passed (638)
   Start at  05:05:40
   Duration  6.23s (transform 5.58s, setup 0ms, import 7.96s, tests 4.46s, environment 33.71s)
```
其中本次新增/变更的测试覆盖全部通过：
- `src/lib/api.test.ts` (23 passed)
- `src/__tests__/composables/useSessionWebUI.test.ts` (6 passed)
- `src/__tests__/components/workspace/WebPlaneView.test.ts` (8 passed)
- `src/__tests__/views/WorkspacePage.webplane.test.ts` (9 passed)
- `src/__tests__/views/WorkspacePage.cliType.test.ts` (4 passed，证明非 WebUI 会话与旧逻辑零回归)

### 4.2 类型检查与构建验证（vue-tsc / Vite）
执行命令：
```bash
npm run build:mobile
```
输出证据：
```
> amagi-codebox-mobile@1.0.5 build
> vue-tsc -b && vite build

vite v8.0.16 building client environment for production...
✓ 187 modules transformed.
rendering chunks...
computing gzip size...
dist/index.html                           1.29 kB │ gzip:   0.86 kB
dist/assets/WorkspacePage-BERVLUlk.js    99.14 kB │ gzip:  32.01 kB
dist/assets/index-CzuJuugl.js           115.30 kB │ gzip:  44.08 kB
dist/assets/xterm-DooSxjI5.js           340.34 kB │ gzip:  86.29 kB
dist/assets/esm-BYgFYVRP.js             369.42 kB │ gzip: 108.54 kB

✓ built in 741ms
```

### 4.3 首屏 Bundle 与无 xterm 泄漏检查
执行命令：
```bash
npm --prefix mobile run check:bundle
```
输出证据：
```
[xterm-bundle] PASS
  ✓ 入口 chunk（index-CzuJuugl.js）剥离动态导入映射后无 xterm 内容
  ✓ 独立 xterm chunk：xterm-DooSxjI5.js
  ✓ index.html 无 xterm 预加载（首屏不加载诊断引擎）
```

### 4.4 视觉与对比度规范校验（WCAG Contrast）
执行命令：
```bash
node mobile/scripts/check-contrast.mjs
```
输出证据：
```
总计 45 对（含登记），通过 43 对，失败 0 对，装饰豁免登记 2 对
```

### 4.5 后端 Go 联动检查（零破坏）
执行命令：
```bash
go vet ./...
go test ./internal/remote/... -count=1
go test ./internal/webui/... -count=1
```
输出证据：
```
ok  	amagi-codebox/internal/remote	27.767s
ok  	amagi-codebox/internal/remote/contract	1.242s
ok  	amagi-codebox/internal/webui	0.589s
```

---

## 5. 遗留风险与未覆盖项

1. **同源沙箱 Cookie 携带风险（G1 联调项）**：
   - 契约设计中 iframe 为同源相对路径 `/webui/{sid}/`，移动端浏览器在 `sandbox="allow-scripts allow-forms"`（无 `allow-same-origin`）下为 opaque origin。
   - 依赖 remote server 代理在入向时依据客户端请求自动校验 device cookie，并由 remote server 向 127.0.0.1 pi webui 注入 Bearer token。该链路已由 T1 的 Go 测试验证，将在 G1 阶段由真实浏览器端到端进一步确认。
2. **移动端页面内键盘弹起尺寸适配**：
   - WebPlaneView 使用 `height: 100%` 并继承父级 visualViewport（`--vvh`），iframe 内部页面内的输入框聚焦将由内部 webui 页面自适应处理，宿主层不进行滚动干预。

---

## 6. 边界与纪律声明

- 严格遵守白名单约束，全部工作代码限定在 `mobile/src/`；
- 未引入任何第三方依赖包；
- 未执行 `git commit`、`git reset` 或 `git checkout`，保护工作区现场；
- 桌面端现有 `WebPlaneHost.vue` 零变动、零回归。
