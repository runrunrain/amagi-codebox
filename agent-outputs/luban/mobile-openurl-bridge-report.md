# mobile 端 pi webui open-url 链接跳转桥 — 实现报告

- 任务：amagi-codebox mobile 端补 `amagi:open-url` 消息桥（跨仓冻结契约 v2.7.4）
- 作者：luban（work 型，独立节点）
- 范围：`mobile/src`（写入）、`agent-outputs/luban`（报告）；未触碰 `internal/`、`frontend/`、`cmd/`
- 依赖变更：无（`mobile/package.json` 未改）

---

## 0. 结论摘要

1. 新增 `mobile/src/lib/openUrlBridge.ts`：契约常量（`OPEN_URL_MESSAGE_TYPE`、`MAX_OPEN_URL_LENGTH=2048`）+ 两个纯守卫（`extractCapabilityToken` / `parseOpenUrlMessage`）+ 打开动作出口 `openExternalUrl`，语义与桌面端 `frontend/src/components/terminal/quickPathInsert.ts` 逐条对齐。
2. `mobile/src/components/workspace/WebPlaneView.vue` 接线：`onMounted` 注册 / `onBeforeUnmount` 移除 `window` message 监听；`ownToken` 取当前 iframe src（`frameSrc`，含 `?skin=light` 形态）的 `#/t=` 凭证；解析通过后打开外链。
3. 打开方式选型（B）：**`window.open(url, '_blank', 'noopener,noreferrer')`**，零新增依赖。理由见 §2（Capacitor 8.2.0 Android 源码级核实：`_blank`/`window.open` 最终经 `Bridge.launchIntent` → `Intent.ACTION_VIEW` 交系统浏览器；`@capacitor/browser` 因无平台工程会在 Android 上 reject，反而使点击彻底失效）。
4. 测试：纯函数正负成对 14 例（新文件）+ 组件接线 2 例（追加）。**已由 Leader 代跑闭合**（定向 25/25、全量复跑 669/669、`npm run build` 成功，见 §6）；真机打开动作为手验项（§7）。

---

## 1. 起点事实（探明过程与证据）

| 事实 | 证据 |
|---|---|
| mobile 无任何外链打开设施 | `grep -n "window\.open\|@capacitor/browser\|Browser\.open\|openExternalURL\|intent://\|window\.location\.href\s*=" mobile/src` → 仅 `__tests__/utils/renderMarkdown.test.ts:37` 的 `target="_blank"` 断言；生产代码 0 命中 |
| `renderMarkdown.ts` 只做属性加固，无打开实现 | `mobile/src/utils/renderMarkdown.ts:21-33` `hardenLinks`：给 `a[href]` 加 `target=_blank` / `rel=noopener noreferrer`，不拦截点击 |
| 桌面参考语义（冻结契约） | `frontend/src/components/terminal/quickPathInsert.ts`：`OPEN_URL_MESSAGE_TYPE`、私有 `MAX_OPEN_URL_LENGTH = 2048`、`CAPABILITY_PATTERN = /^[A-Za-z0-9_-]{22,}$/`、`extractCapabilityToken`（`#/t=` 定位 + `decodeURIComponent` + 形状校验）、`parseOpenUrlMessage` 守卫顺序 |
| 桌面接线模式 | `frontend/src/components/terminal/WebPlaneHost.vue:110-127` `onFrameMessage`：`parseOpenUrlMessage(event.data, extractCapabilityToken(frameSrc.value))` → 打开；`onMounted`/`onBeforeUnmount` 注册/移除 |
| mobile iframe 实参形态 | `WebPlaneView.vue:42-52` `withLightSkin`：`?skin=light` 插在 `#` 之前，`#/t=<token>` fragment 原样保留 → `extractCapabilityToken(frameSrc.value)` 可直接取凭证 |
| URL 来源 | `mobile/src/lib/api.ts:259`：`available` 时 url 为同源相对路径 `/webui/{sid}/#/t={token}`；`WebPlaneView.test.ts` / `WorkspacePage.webplane.test.ts` 的事实形态一致 |
| mobile 测试运行方式 | `mobile/package.json`：`test = vitest run`（vitest 4.1.0 已装于 `mobile/node_modules`）；`vitest.config.ts` include `src/**/*.{test,spec}.ts`，environment `jsdom`；测试文件按仓库惯例放 `mobile/src/__tests__/` |

---

## 2. 选型：外链打开方式（B 项，先探后选）

### 候选 1 — 复用 mobile 现有设施
**不存在**（见 §1 首行证据）。无可复用出口。

### 候选 2 — `@capacitor/browser` 插件 `open()`
**不采用**，理由有实证：

- `mobile/package.json` 无该依赖（新增即引入原生插件面）。
- 本仓**没有 Android 平台工程**：`read mobile/android/app/src/main/AndroidManifest.xml` → ENOENT；`mobile/src/lib/shell/capability.ts` 文件头明确「本机无 android/ios 平台目录——native 壳工程 escrow 至 M4-C」。
- Capacitor 原生插件必须由平台工程注册（`cap sync` 生成 `capacitor.settings.gradle` / `capacitor.build.gradle`）；平台工程缺失时插件 JS 代理在 Android 上会 reject（"not implemented on android"）。也就是说：现在加 `@capacitor/browser`，链接点击在真机上会**彻底失效**（比现状「无反应」更差，且产生未声明失败路径）。
- 违背任务安全底线「不引入全量 in-app browser 依赖除非必要」。

### 候选 3 — `window.open(url, '_blank', 'noopener,noreferrer')`
**采用**。Capacitor 8.2.0 Android 源码级核实（`mobile/node_modules/@capacitor/android/capacitor/src/main/java/com/getcapacitor/`，已安装依赖，可复核）：

1. `Bridge.java:280`：`webView.setWebChromeClient(new BridgeWebChromeClient(this))`；`BridgeWebChromeClient.java` **未覆写 `onCreateWindow`**；全包 grep **无 `setSupportMultipleWindows`** → WebView 默认多窗口关闭。
2. 多窗口关闭时，`window.open(..., '_blank')` / `a[target=_blank]` 由同一 WebView 承载导航，进入 `BridgeWebViewClient.shouldOverrideUrlLoading`（`BridgeWebViewClient.java:27-30`）→ `Bridge.launchIntent(url)`。
3. `Bridge.java:389-419` `launchIntent`：排除 `data`/`blob`；目标 host ≠ app host（`capacitor.config.ts` `androidScheme: 'https'` → app 为 `https://localhost`）且不在 `allowNavigation` → `new Intent(Intent.ACTION_VIEW, url)` + `startActivity` → **系统浏览器**，返回 `true`（WebView 内不发生导航）。
4. 佐证：顶部窗口（TUI 时间线 markdown）的 `target=_blank` 链接现状走的就是这条链路（`renderMarkdown.ts` 只加 target/rel），用户侧可正常打开；iframe 内因 `sandbox="allow-scripts allow-forms"`（无 allow-popups）才被静默阻断——本桥把 iframe 内的点击请求搬到宿主窗口执行，落回同一条已验证链路。
5. Web 端（`vite dev` / 浏览器打开 `dist`）`window.open` 为浏览器原生行为，无插件依赖。

**安全底线落实**：

- 仅 http(s)：`parseOpenUrlMessage`（契约白名单）+ `openExternalUrl` 二次白名单（对齐桌面 Go 侧 `ValidateExternalURL` 的纵深防御）；其他 scheme 一律拒绝且无副作用。
- `noopener` 语义：features 串含 `noopener`（另加 `noreferrer`，与桌面 Go 打开不带 referrer 的行为对齐，避免向外部站点泄漏宿主地址）。
- 无 in-app browser、无新增依赖、无 WebView 配置改动。

**已知环境差异（如实记录）**：桌面浏览器（非 Capacitor）中，弹出窗口可能受弹窗拦截影响（父窗口未必持有 transient activation）；Capacitor Android WebView 无弹窗拦截，本桥目标平台不受影响。若后续在浏览器环境观察到被拦，属环境差异而非本桥缺陷，已在手验清单留观测点。

---

## 3. 改动清单

| 文件 | 状态 | 要点 |
|---|---|---|
| `mobile/src/lib/openUrlBridge.ts` | 新增 | 契约常量 + `extractCapabilityToken` + `parseOpenUrlMessage` + `openExternalUrl`；头注释记录跨仓契约依据、与桌面的有意差异、选型结论 |
| `mobile/src/components/workspace/WebPlaneView.vue` | 修改 | ①头注释「核心特征」补 open-url 桥一条；②导入 `onMounted` 与 lib 三函数；③新增 `onWindowMessage`（`ownToken = extractCapabilityToken(frameSrc.value)`）；④`onMounted` 注册 / `onBeforeUnmount` 移除 `window` message 监听（与既有 `clearWatchdog` 合并同一卸载钩子） |
| `mobile/src/__tests__/lib/openUrlBridge.test.ts` | 新增 | 纯函数正负成对用例（§5） |
| `mobile/src/__tests__/components/workspace/WebPlaneView.test.ts` | 修改 | 追加 2 例接线用例（解析通过 → 打开；凭证不符/非 http(s) 拒绝 + 卸载后监听移除） |
| `mobile/package.json` | **未改** | 选型确认零新增依赖，无需变更 |
| `internal/`、`frontend/`、`cmd/` | **未触碰** | 遵守边界 |

未采纳的边界内小增强（记录取舍）：`event.source === iframe.contentWindow` 来源校验——桌面端无对应语义，且 token 已是凭证（与桌面「守卫一致」优先），故不加，避免超出任务对齐范围。

---

## 4. 契约对齐表（桌面 ↔ mobile）

| 守卫/常量 | 桌面 `quickPathInsert.ts` | mobile `openUrlBridge.ts` | 一致性 |
|---|---|---|---|
| `type` 严格匹配 | `!== 'amagi:open-url'` → null | 同 | ✅ |
| 宿主无 token | 早退 null | 同（顺序一致：先 ownToken 后解析字段） | ✅ |
| `data` 非 object / null | null | 同（并覆盖 `undefined`/数字/布尔/字符串） | ✅ |
| `token` 非空且严格相等 | `typeof !== 'string' \|\| token !== ownToken` | 同 | ✅ |
| `url` string、非空、≤2048 | `length === 0 \|\| length > MAX` | 同 | ✅ |
| http(s) 白名单 | `/^https?:\/\//i` | 同 | ✅ |
| 常量值 | `'amagi:open-url'` / `2048` | 同（本模块显式 export `MAX_OPEN_URL_LENGTH` 供测试钉边界） | ✅ |
| token 提取 | `#/t=` + `decodeURIComponent` + `[A-Za-z0-9_-]{22,}` | 同 | ✅ |
| **打开出口（有意差异）** | Go `BrowserOpenURL`（另有 `ValidateExternalURL` 二校） | 本机 `window.open(..., '_blank', 'noopener,noreferrer')`（`openExternalUrl` 二校 scheme） | 差异已声明（§2，平台无 Wails 桥） |

---

## 5. 测试用例清单（正负成对）

`mobile/src/__tests__/lib/openUrlBridge.test.ts`：

| # | 用例 | 期望 |
|---|---|---|
| 1 | `extractCapabilityToken('/webui/sess-1/#/t=<22位>')` | 返回该 token |
| 2 | 带 `?skin=light#/t=`（宿主实参形态，相对/绝对 URL 两例） | 返回该 token |
| 3 | fragment 缺失 / token 10 位 / 空 token / `%E0%A4%A` 编码失败 / token 后带 `&x=1` | 全部 null |
| 4 | `parseOpenUrlMessage` 合法消息 | 返回**具体 URL 值** `https://example.com/docs` |
| 5 | `http://` 与 `HTTPS://` 大小写 scheme | 原样返回 |
| 6 | token 不符 / ownToken 为 null | null |
| 7 | token 缺失 / 非 string（42） | null |
| 8 | type 不符（`amagi:insert-input`）/ 缺失 | null |
| 9 | data 为字符串 / null / undefined / 42 / true | null |
| 10 | url 非 string / 空串 | null |
| 11 | 非 http(s)：`javascript:` `file:` `mailto:` `/relative` `ftp:` `data:` `intent:` | 全部 null |
| 12 | 边界：长度恰 2048 通过；2049 → null | 两端分别断言 |
| 13 | `openExternalUrl('https://...')` | `window.open` 调用一次，实参 `(url, '_blank', 含 noopener 与 noreferrer)`，返回 true |
| 14 | `openExternalUrl` 非 http(s)/空串 | 返回 false 且 `window.open` **零调用** |

`mobile/src/__tests__/components/workspace/WebPlaneView.test.ts`（追加）：

| # | 用例 | 期望 |
|---|---|---|
| 15 | 挂载（有效 token）→ `window` 收合法消息 | `window.open` 一次，`(https://example.com/docs, '_blank', 含 noopener)` |
| 16 | 凭证不符 + `javascript:` → 不打开；合法消息正对照可打开；`unmount` 后再发合法消息 | 拒绝段零调用 → 正对照一次 → 卸载后零调用（证明注册/移除闭环） |

环境静态核实（保证用例本身可运行）：

- jsdom 中 `window.open` 为 own、writable、configurable 数据属性（`mobile/node_modules/jsdom/lib/jsdom/browser/Window.js:919-934` + `lib/jsdom/utils.js:12-17` define 语义）→ `vi.spyOn(window, 'open')` 可用。
- `tsconfig.app.json` include `src/**/*.ts`（含 `__tests__`），`strict` + `noUnusedLocals` + `verbatimModuleSyntax`；新增文件已按此复核（无未用符号、无仅类型值导入、断言合法）。
- `vitest.config.ts` include 覆盖 `src/**/*.{test,spec}.ts`；新测试路径命中。

---

## 6. 验证证据与未闭合项（如实声明）

### 已执行（静态证据，可复核）

1. 逐条核对守卫顺序、常量值、正则与桌面端参考实现（§4 对照表）。
2. 核实 Capacitor 8.2.0 Android 的 `_blank`/`window.open` 落点（§2，源码文件与行号可复核）。
3. 核实 jsdom `window.open` 可 spy、tsconfig/vitest 覆盖新文件（§5 末）。
4. 核实 mobile 现状无外链设施、边界文件未被动过（写入范围仅 `mobile/src` + 报告）。

### 执行历史（首轮未运行 → Leader 代跑闭合）

本会话工具面为 read/grep/find/write/edit/amagi_resource，无 shell，首轮**未运行**以下命令；后由 **Leader 代跑**完成验证：

```bash
# 全量单测（验收 D 第一条）
npm --prefix mobile run test

# 仅本次相关（快速回环）
npm --prefix mobile run test -- src/__tests__/lib/openUrlBridge.test.ts src/__tests__/components/workspace/WebPlaneView.test.ts

# 构建 / 类型检查（验收 D 第二条）
npm --prefix mobile run build   # = vue-tsc -b && vite build
```

→ **状态：已执行并全绿（Leader 代跑，任务验收通过）**：

| 命令 | 结果 |
|---|---|
| `npx vitest run src/__tests__/lib/openUrlBridge.test.ts src/__tests__/components/workspace/WebPlaneView.test.ts` | **25/25 全绿**（openUrlBridge 14 + WebPlaneView 11） |
| `npx vitest run`（全量） | 首跑 668/669（1 例 flaky）；同代码复跑 **669/669 全绿** |
| `npm run build`（= `vue-tsc -b && vite build`） | 成功，`dist/` 产物正常生成 |

执行环境注记（Leader 侧）：Windows 侧 node 在无控制台进程组下无法启动，最终在 WSL 侧补装 `@rolldown/binding-linux-x64-gnu@1.0.3`（`--no-save`，win32/musl binding 共存不受影响）后执行。误写的 `.tmp-openurl-probe.txt` 已由 Leader 删除。任务验收结论：代码、测试、报告全部通过；CHANGELOG / 版本号 bump 由 Leader 总收口统一处理（不在本任务范围）。

### 不可在单测中验证、必须真机手验的部分

- `window.open` → 系统浏览器的真实落点（无设备、无平台工程，无法自动验）。

### 附注：本任务过程中产生的仓库根杂项文件（需清理）

- `.tmp-openurl-probe.txt`（仓库根，git 未跟踪）：本会话工具面无 bash/exec，一次工具面探测失误以 `write` 落在仓库根（超出本任务写入范围）；本会话无删除工具，无法自行清理。请 Leader 侧删除该文件；除此之外未在授权范围外产生任何写入。

---

## 7. 手验清单（真机 / 手动）

1. 桌面端开启 remote server 并拉起 pi 会话（webui available）；手机连入同一 host，进入 Web 平面视图。
2. 在 webui 消息流内点击一个 `https://…` 链接：
   - 期望：**系统浏览器**打开该链接；
   - 期望：App 停留在 Web 平面（不白屏、不被导航覆盖、会话仍在）。
3. 负例（浏览器 dev 环境可在宿主页面 DevTools 执行）：
   - `window.postMessage({ type: 'amagi:open-url', token: 'WRONG', url: 'https://example.com' }, '*')` → 无任何打开动作；
   - 正确 token 但 `url: 'javascript:alert(1)'` → 无任何打开动作。
4. 回归：webui 页面内表单提交（`allow-forms`）、滚动、输入仍正常；iframe 的 `sandbox` 属性未被改动（应仍为 `allow-scripts allow-forms`）。
5. 从系统浏览器返回 App：Web 平面 iframe 内容与连接保持；多会话/多 WebPlane 并存时，其他会话平面不响应本会话凭证的消息。
6. 记录观测：设备型号、Android/WebView 版本、默认浏览器、是否出现弹窗拦截提示（若出现，记录后回报，供后续在 `openExternalUrl` 单点换设备能力实现）。

---

## 8. 剩余风险与未覆盖

| 风险 | 等级 | 说明 / 缓解 |
|---|---|---|
| 测试未在本轮运行 | 已闭合 | §6 回填：Leader 代跑，定向 25/25、全量复跑 669/669、构建成功 |
| 真机打开未验 | 中 | 无设备/无平台工程；链路已在 Capacitor 源码级核实（非运行时证据） |
| 个别 WebView 版本行为差异 | 低 | Capacitor 8 默认多窗口策略为框架不变量；真机 WebView 版本更新可能改变行为，手验覆盖 |
| 浏览器 dev 环境弹窗拦截 | 低 | Capacitor 目标平台无此问题；已在 §7 留观测点 |
| 自验非独立审核 | 中 | 跨仓契约一致性建议由独立审核（谛听类）复核本报告 §4 与测试用例 |

---

## 9. 建议后续（范围外，仅报告不执行）

1. CHANGELOG 条目（1.3.89 或对应版本 `Added`）与 mobile 版本号 bump（`mobile/package.json` 1.0.5、`wails.json` `info.productVersion`）——本次受写入边界约束未改，请 Leader 决定。
2. M4-C 原生壳接线时若确定要 in-app browser，只需替换 `openExternalUrl` 单点实现（`@capacitor/browser`），公共 API 与守卫不变。
3. 若后续需要「打开失败提示」，可把 `openExternalUrl` 扩展为结构化结果；当前按冻结契约「静默尽力而为」（对齐桌面 `console.warn` 策略）不引入额外 UI。
<!--AMAGI:REPORT:agent-outputs/luban/mobile-openurl-bridge-report.md-->
