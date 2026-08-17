# 皮肤「字体调光 TextBoost」前端实现报告

- **节点**: textboost-frontend（batch 三联动需求 · amagi-codebox 侧）
- **日期**: 2026-08-18
- **前置**: 后端 `TextBoost` 字段已落地（impl-report-textboost-backend.md），wailsjs 绑定已含 `textBoost: number`
- **模式**: 照搬 opacity 前端模式（impl-report-frontend-round2.md）：store 默认值 → App.vue CSS 变量注入 → skin.css 覆盖层消费 → 设置页滑块
- **范围纪律**: 未碰 `frontend/wailsjs/`、未 commit；在本节点派发时工作区既有未提交皮肤改动之上追加

## 1. 变更清单

### `frontend/src/stores/skin.ts`
- `DEFAULT_SETTINGS` 补 `textBoost: 0`（0=不增强保持现状，与后端默认一致；老配置缺子键读入 0 恰好等于默认值，零突变）。
- `apply` 走 `{...settings, ...patch}` 全量传递，textBoost 自动带上，无需改签名。
- `clear()` 恢复默认一并重置 `textBoost`（照 opacity 同模式）。
- 头注释同步变量清单。

### `frontend/src/api/skins.ts`
- `getSkinSettings` 注释同步字段列表（+textBoost），纯注释。

### `frontend/src/App.vue`（syncSkinDom）
- 新增 `--skin-text-boost` 注入：`textBoost(0..100)/100`，clamp [0,100]，非有限值回落 0。
- **0 档移除变量**（`removeProperty`），使 skin.css 中依赖该变量的 color-mix 声明整组失效、前景 token 回退 tokens.css 原色——0 档视觉与无皮肤/升级前完全一致。
- 皮肤关闭分支同步 `removeProperty('--skin-text-boost')`。
- watch 依赖增加 `settings.textBoost`，拖滑块即时生效（preview 不写后端，change 提交）。

### `frontend/src/styles/skin.css`
- `:root` 增加 `--skin-text-boost: 0` 默认值（防御：即使变量存在，0% 权重混合结果即原色）。
- `html[data-skin='on']` 新增前景三级文字 token 覆盖（原色取自 tokens.css 1:1，仅覆盖层变量化，不改设计规范文件）：
  ```css
  --label:     color-mix(in srgb, #1D1D1F, black calc(var(--skin-text-boost) * 100%));
  --secondary: color-mix(in srgb, #6E6E73, black calc(var(--skin-text-boost) * 100%));
  --tertiary:  color-mix(in srgb, #8E8E93, black calc(var(--skin-text-boost) * 100%));
  ```
- 底衬：**选 text-shadow 淡色光晕**，弃容器背景方案。取舍理由：不涂色块、零布局影响、不与容器既有背景/边框/圆角冲突，且对容器内所有文本节点生效（背景方案只罩容器盒不罩文字）。命中面克制在 3 个类：
  ```css
  html[data-skin='on'] .setting-row label,   /* 5 个设置视图的行标签 */
  html[data-skin='on'] .sess-title,          /* 侧栏会话列表主标题 */
  html[data-skin='on'] .nav-item {           /* 侧栏主导航项 */
    text-shadow: 0 0 6px color-mix(in srgb, black calc(var(--skin-text-boost) * 25%), transparent);
  }
  ```
  boost=100 时 25% alpha 黑色柔光晕，把文字边缘从复杂背景纹理中柔化分离；boost=0 时变量缺失、整组声明失效。
- embedded webui 为 iframe 独立文档，不继承宿主 `html[data-skin]` 规则，确认无冲突。

### `frontend/src/views/settings/AppearanceSettings.vue`
- 新增「字体调光」滑块（0–100%，step 1，默认 0），与调光/模糊/内容不透明度并列，同一 preview 拖动 + change 提交模式（`onTextBoostInput` / `onTextBoostCommit`）。
- 三项语义说明对齐为一条 hint：`调光＝整张背景图压暗；内容不透明度＝…透出背景图的程度（100% 为不透明）；字体调光＝独立于背景调光，加深前景文字并加淡底衬，保证任意背景图下文字可读（0% 为不增强）`；卡片副标题同步补「字体调光」。

## 2. 对任务书 color-mix 公式的一处语义纠偏（已按语义实现并在此说明）

任务书给出的写法 `color-mix(in srgb, <原色> calc(var(--skin-text-boost)*1%), black)` 按 CSS 规范（单一百分比时另一色取 100%−p）实际效果为：**任意 boost>0 时 black 权重 ≥99%，文字直接近纯黑，无极间过渡**；且 `*1%` 作用在 0..1 的变量上权重区间仅 0..1%。这与字段语义「0..100 平滑加深、0=现状」不符。

实际实现把权重放在 black 侧：`black calc(var(--skin-text-boost) * 100%)`（0..1 变量 → 0%..100% 平滑混合，boost=100 全黑，0 档靠移除变量整组失效回退原色）。底衬同理取 `black calc(var(--skin-text-boost) * 25%)`（0..25% alpha 平滑区间，对应任务书"boost 满档 25% 强度"的描述）。语义与任务书文字描述完全一致，仅修正了百分比落点。

## 3. 验证

- `npm --prefix frontend run build`（= `vue-tsc --noEmit && vite build`）**通过**，无类型错误、无构建告警（411ms）。
- 未运行 `wails generate`；`git status` 层面未碰 `frontend/wailsjs/`、未 commit。

## 4. 手验步骤（需真实 App 环境）

1. 设置 → 外观 → 选择图片应用皮肤（选一张深色或花哨背景图效果最明显）。
2. 拖「字体调光」滑块 0→100：设置页行标签、侧栏会话标题、导航项文字由灰（--secondary/--tertiary）平滑加深至近黑，并出现淡黑色柔光底衬；与「调光」「内容不透明度」互不影响（三者解耦）。
3. 滑块回 0：`--skin-text-boost` 变量被移除（DevTools 查 `<html>` inline style 可证），所有文字颜色与无皮肤状态 1:1 一致。
4. 恢复默认 → 皮肤关闭，`--skin-text-boost` 与 data-skin 一并清除，界面回归 tokens.css 原色。
5. 开一个 pi 会话切 Web 平面：iframe 内 webui 页面不受字体调光影响（独立文档）。
6. 持久化：调非 0 值后重启 App，设置页滑块与文字加深效果保持（settings.json `skin.textBoost` 落盘，后端 clamp [0,100]）。

## 5. 未覆盖 / 风险

- 未做运行时截图验证（需 `wails dev` 真实桌面环境 + 已导入皮肤，本环境不具备）；color-mix 支持矩阵与 opacity 轮相同（Chrome 111+/Safari 16.2+），不支持时本组声明失效回退 tokens.css 原色，不破坏功能。
- text-shadow 底衬只覆盖 3 个高命中容器类，卡片正文段落（如 `.set-sub`、`.footer-hint`）不叠光晕、仅靠 token 加深——属刻意克制，避免全局光晕造成"脏屏"观感；若实测不够可再扩类。
- batch 其余两节点（pi Web 平面透皮修复、webui 409 自动排队）不在本节点范围。
