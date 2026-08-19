# 终端打字/操作巨大延迟 — 根因与修复（皮肤预烘焙）

## 症状

v1.3.32+（皮肤功能上线）后，终端打字回显与整体操作出现巨大延迟。

## 根因

皮肤视觉层把**逐帧渲染成本**加到了每一次按键/UI 重绘上（v1.3.32–34 引入）：

1. `html[data-skin='on'], body { background: transparent }` —— 失去不透明快速路径，全窗 alpha 合成
2. `.skin-layer { filter: blur(var(--skin-blur)) }` 全视口模糊——即使滑块为 0 也是 `blur(0px)`，仍占 filter 渲染面
3. `.skin-dim` 全视口蒙版叠加
4. `--window/--card/--sidebar/--control` 全部 color-mix 带 alpha，半透明面板每次重绘与壁纸混合

GPU 加速正常时勉强可承受；Windows WebView2 在 GPU 黑名单/禁用/RDP/虚拟机场景回退**软件光栅**时，全窗 blur+alpha 合成每帧数百毫秒 → 打字卡顿、操作延迟。

排除项（diff 为证）：终端引擎/PTY 输出链路 v1.3.32→37 零改动；webui 3s 保活探测为 loopback HTTP 不占 UI 线程；ConPTY/代理/多预设改动均为启动时一次性。

## 修复（v1.3.38 预烘焙）

**把逐帧成本变成调参时的一次性成本**：

### `frontend/src/utils/skinBake.ts`（新）

- `createImageBitmap` 异步解码 → 低分辨率离屏 canvas（最长边 ≤1280，成本与屏幕分辨率解耦）→ 一次性 `ctx.filter=blur()` + scale(1.08) 边缘补偿 + dim 黑色 alpha 压暗 → `toDataURL('image/jpeg', 0.85)` 产出烘焙图
- 滑块拖动防抖 500ms 重烘焙；烘焙完成回调原子换图；token 代际取消旧烘焙
- 烘焙中先回落原图直显（不卡 UI）
- 烘焙失败（无 canvas filter/低内存的极老 WebView）→ 本次会话永久回退 CSS 直显（行为同旧版）

### `frontend/src/App.vue`

- `--skin-image` 优先注入烘焙产物；`html[data-skin-baked]` 标记烘焙态；`data-skin-blur-zero` 标记 blur=0 快路径
- `requestBake` 防抖链路接入 `syncSkinDom`

### `frontend/src/styles/skin.css`

- 烘焙态（`[data-skin-baked]`）：`filter: none` + `transform: none` + 蒙版层 `background: transparent`——运行期只剩一张普通背景图
- 回落态：维持旧 blur 路径（正确性兜底）
- blur=0 快路径：回落态也 `filter: none`（免 `blur(0px)` 渲染面）
- 属性选择器统一改用 dataset（`[data-skin-baked]`），不依赖 style 序列化格式

## 效果

- 烘焙态运行期渲染成本 ≈ 无皮肤（普通背景图 + 纯色层），软件光栅下也轻量
- 视觉与旧版一致（blur/dim/边缘补偿等价烘焙进位图）；滑块调参 500ms 后生效

## 验证

`npm --prefix frontend run build`（vue-tsc 类型门禁）通过。

手验：①设皮肤+blur>0 → 等 0.5s 烘焙 → 打字应恢复流畅（对比旧版卡顿）②DevTools 检查 `html` 有 `data-skin-baked`、`.skin-layer` computed filter 为 none ③拖动滑块期间短暂原图直显，停手后烘焙图原子替换 ④关闭皮肤 → 无任何残留层。
