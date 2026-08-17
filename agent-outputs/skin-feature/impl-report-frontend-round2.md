# 皮肤透明度前端 + pi Web 平面透皮 — 实现报告（frontend round2）

承接 `impl-report-opacity-backend.md`（后端 `opacity` 字段）。本轮只做前端，工作区既有未提交皮肤改动之上追加，未 commit、未手改 `frontend/wailsjs/`。

## 变更清单

### 1. 透明度语义分层

**`frontend/src/App.vue`**
- `syncSkinDom` 新增 `--skin-panel-alpha` 注入：`opacity(0..100)/100`，clamp [0,100]，非有限值回落 70；皮肤关闭时一并 `removeProperty`。
- watch 依赖增加 `settings.opacity`，拖滑块即时生效。

**`frontend/src/styles/skin.css`**（tokens.css 未动，只在覆盖层变量化）
- `:root` 增加 `--skin-panel-alpha: 0.7` 默认值。
- `html[data-skin='on']` 分支改为：
  ```css
  --skin-panel-mix: calc(max(var(--skin-panel-alpha), 0.12) * 100%);
  --window:   color-mix(in srgb, #FBFBFD var(--skin-panel-mix), transparent);
  --card:     color-mix(in srgb, #FFFFFF var(--skin-panel-mix), transparent);
  --sidebar:  color-mix(in srgb, #F5F5F7 var(--skin-panel-mix), transparent);
  --control / --controlHover: 同理（原色取自 tokens.css 1:1）
  ```
  `color-mix(in srgb, …)` Chrome 111+ / Safari 16.2+ 支持，WebView2 / WKWebView 均在支持范围内；`max()` 参与 `calc()` 同属现代 CSS 数值函数，同一支持矩阵。
- 终端区域 `--termBg` 不透明不变。

**0 档极端可读性说明**：opacity=0 时并非绝对全透——`max(--skin-panel-alpha, 0.12)` 给所有窗口面保 12% 极淡亮底，配合 dim 蒙层（App.vue 保底 35% 黑色压暗）双保险：浅色主题深色文字（--label 近黑）在任意亮色/花图上仍有 12% 白底 + 压暗背景托底，对比度下限可控。滑块到 0 的观感≈纯图上叠极薄纱，符合"给正文容器叠一层极淡的底"的设计取舍，而非物理 0 alpha（那会使深色文字直接落在压暗图上，暗图场景不可读）。

### 2. 设置页

**`frontend/src/views/settings/AppearanceSettings.vue`**
- 新增「内容不透明度」滑块（0–100%，step 1，与调光/模糊并列，preview 拖动 + change 提交同一模式）。
- 滑块区下方新增说明：`调光＝整张背景图压暗；内容不透明度＝窗口/侧栏/卡片等面板透出背景图的程度（100% 为不透明）`；卡片副标题同步更新。
- `.range-label` min-width 32→78px 容纳六字标签。

**`frontend/src/stores/skin.ts`**
- `DEFAULT_SETTINGS` 补 `opacity: 70`；`clear()` 恢复默认一并重置 opacity；`apply` 走 `{...settings, ...patch}` 全量传递，opacity 自动带上。
- `frontend/src/api/skins.ts` 注释同步（enabled/imageId/dim/blur/opacity）。

### 3. pi Web 平面透皮

**`frontend/src/components/terminal/WebPlaneHost.vue`**
- `.web-plane-host` / `.web-frame` 背景 `var(--card)` → `transparent`（iframe 默认透明，webui `#/t=` 内嵌模式 body 自身透明，皮肤层一路透出）。
- `.plane-overlay`（加载中/错误态）：保留 `--card` 实底语义，加 `backdrop-filter: blur(14px) saturate(1.1)`——皮肤模式下 `--card` 已半透明，压花背景保证 overlay 文字可读（可读优先）。
- `.plane-ended-bar`（结束 badge）：加 `backdrop-filter: blur(10px)`，透皮下 badge 与「切回终端」按钮在任意背景图上可读。

**`frontend/src/components/terminal/TerminalView.vue`**（透皮链路必需的一环）
- `.term-body` 原为不透明 `--termBg`（防 xterm teardown 闪白），会挡住皮肤层。新增：
  ```css
  html[data-skin='on'] .term-body.web-active { background: transparent; }
  ```
  仅在皮肤开启且 Web 平面激活时让位；xterm 平面保持不透明不变。

## 验证

- `npm --prefix frontend run build`（= `vue-tsc --noEmit && vite build`）通过，无类型错误、无构建告警。
- `git status` 确认：`frontend/wailsjs/` 无本轮改动（models.ts 为后端轮已重新生成），未 commit。

## 手验步骤（需真实 App 环境）

1. 设置 → 外观 → 选择图片应用皮肤。
2. 拖「内容不透明度」滑块：100%=面板实底（同无皮肤观感），70%=默认半透，0%=极薄纱近纯图——全程文字可读（12% 下限 + dim 蒙层）；调光滑块行为不变且与透明度互不影响。
3. 开一个 pi 会话（webui available）→ 工具栏切到 Web 平面 → 终端区透出皮肤图（webui 内嵌页自身面板化）；加载中与错误态 overlay 仍是可读实底+压花；会话结束后右上角「会话已结束」badge 可读。
4. 切回 TUI 平面 → 终端区恢复不透明深色；再切回 Web 平面 → 页面未重载（v-show 保留），webui 输入框草稿仍在。
5. 恢复默认 → 皮肤关闭，所有面板回归 tokens.css 实色。

## 未覆盖 / 风险

- 未做运行时截图验证（需 `wails dev` 真实桌面环境 + 已导入皮肤 + 在线 pi webui，本环境不具备）；color-mix/max 在目标 WebView 的支持为文档级确认（Chrome 111+/Safari 16.2+），未实测旧版 WKWebView 回落行为——若遇不支持环境，`--skin-panel-mix` 整组声明失效，面板回落 tokens.css 实色（皮肤图仍可见于 html/body 透明区，不破坏功能）。
- iframe 内 webui 页面样式归 pi CLI 侧（`#/t=` 内嵌模式），本仓不可控；透皮最终观感依赖该模式 body 透明的既有契约。
- 老 settings.json 含 skin 键但缺 opacity 子键时读入 opacity=0（后端既有取舍，见 `normalizeSkinSettings` 注释），前端滑块会显示 0%——属预期，用户一滑即修正。
