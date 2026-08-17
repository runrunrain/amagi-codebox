# 皮肤功能前端切片 B 实施报告

日期：2026-XX（实施会话）；依据 `agent-outputs/skin-feature/plan.md` 前端切片 B。

## 交付文件

| 文件 | 内容 |
|---|---|
| `frontend/src/api/skins.ts` | 包装 6 个 skins 绑定（PickSkinImage/ImportSkinImage/ListSkins/RemoveSkin/GetSkinSettings/SetSkinSettings），callApi 模式；PickSkinImage 取消（后端返回 nil）映射为 null |
| `frontend/src/stores/skin.ts` | Pinia setup store：settings/skins/currentSkin/active；load()（GetSkinSettings+ListSkins 并发，匹配当前皮肤）；apply(patch) 持久化后以服务端 clamp 结果重载、失败回滚重载；preview(patch) 滑块拖动本地预览；clear() 恢复默认；importImage()（对话框导入）；remove(id)（错误原样上抛） |
| `frontend/src/styles/skin.css` | 皮肤视觉层样式：.skin-layer（fixed inset:0、cover、filter:blur(var(--skin-blur))、scale(1.08) 补偿模糊边缘）+ .skin-dim（rgba(0,0,0,var(--skin-dim)) 蒙版），均 z-index:-1 沉底、pointer-events:none；`html[data-skin="on"]` 下 --window/--card/--sidebar/--control/--controlHover 转半透明 rgba，html/body 背景转透明；prefers-reduced-motion 下关过渡。已注册进 `styles/index.css` |
| `frontend/src/App.vue` | 挂载两个皮肤层 div（v-if=store.active，aria-hidden）；启动 load() 一次；watch store → 在 `<html>` 写 --skin-image（url(skin.url)）/--skin-blur/--skin-dim（保底 0.35）与 data-skin 开关，保存后即时生效 |
| `frontend/src/views/settings/AppearanceSettings.vue` | 新"外观"页：选择图片（导入后自动应用）、缩略图网格（img src=skin.url、点击应用、accent 边框+"使用中"角标高亮当前、hover 显示删除按钮、ConfirmDialog 确认）、dim 0-100 与 blur 0-40 滑块（input 预览 / change 持久化、未启用时禁用）、恢复默认按钮；空库用 EmptyState；删除被应用皮肤的后端拒绝信息原样 toast；全量使用 ui 组件库 + tokens，无 Element Plus |
| `frontend/src/views/settings/SettingsView.vue` | 注册 appearance 页签组件与 PageHead META（外观 / 皮肤背景、调光与模糊） |
| `frontend/src/components/layout/SidebarSettings.vue` | 设置侧栏增"外观"导航项（image 图标，位于终端设置之后） |

## 关键决策

- **z-index 方案**：皮肤层用 `z-index:-1` 而非 0。AppShell `.main` 为 position:relative，若皮肤层 z-index:0 会与内容同级争层序；负 z-index 绘于根画布背景之上、所有内容之下，配合 `data-skin=on` 时 html/body 背景透明即可透出，层序无歧义。
- **终端不透明**：终端区域使用不透明的 --termBg（#1B1B1F），不受 token 半透明化影响，无需额外处理。
- **dim 双保底**：后端 clamp 之外，App.vue 写入 CSS 变量时 `Math.max(35, dim)`，防止 0 值蒙版下过亮图片吞掉前景。
- **滑块两段式**：input 事件走 store.preview（仅本地、即时视觉反馈），change 事件才 apply 持久化，避免拖动过程刷写 settings.json。
- 未改动后端与 `frontend/wailsjs/` 生成物；`api/index.ts` 未加 re-export（与 webui.ts 现状一致，视图直接 import stores/api 模块）。

## 验证

- `npm --prefix frontend run build`（= vue-tsc --noEmit && vite build）**通过**，产物含 SettingsView chunk，无类型错误。
- 后端契约核对：PickSkinImage 取消返回 nil（前端按 null 处理）；RemoveSkin 对被应用皮肤返回"皮肤正在使用，请先停用后再删除"（前端 toast 原样展示）；SetSkinSettings enabled 时校验 imageId 存在。

## 手工验收步骤

1. `wails dev`（或 build/bin 启动应用）→ 设置 → 侧栏出现"外观"页签。
2. 外观页 → 「选择图片」→ 选一张本地 png/jpg/webp → 皮肤即时生效：全局背景变为图片 + 默认 35% 调光蒙版，侧栏/卡片呈半透明；缩略图网格出现该图并高亮"使用中"。
3. 拖"调光"滑块：拖动中实时预览，松开后保存；拖"模糊"滑块：背景模糊变化且边缘无透明缝（scale 补偿）。
4. 终端视图确认终端区域保持不透明深色，文字可读。
5. 删除当前使用中的皮肤 → 后端拒绝，toast 提示"皮肤正在使用，请先停用后再删除"；先「恢复默认」（背景回到原浅色外观）再删除 → 成功，缩略图消失。
6. 应用皮肤后**重启应用** → 皮肤保持（settings.json 持久化）。
7. 导入非图片文件/改后缀的假图片/超大文件 → 后端魔数+大小校验拒绝，toast 报错。
8. 键盘操作：Tab 聚焦缩略图，Enter/Space 应用；焦点环可见。
9. 开启系统"减少动态效果"偏好 → 皮肤层无过渡动画。

## 未覆盖项

- 未做运行时真实界面截图验证（需 wails dev 图形会话）；层序方案经 CSS 规范推导，建议按上述步骤人工过一遍。
- vite 纯前端 dev server（不经 wails）下 `/skins/<file>` 无 AssetServer，图片不显示——属预期，壳外无皮肤后端。
- 移动端 remote Web 不同步皮肤（plan 明确不做）。
