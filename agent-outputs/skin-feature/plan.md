# 新功能：本地图片应用皮肤（皮肤/壁纸）

> **状态（2026-08-17）：已完成**。后端切片 A（luban）+ 前端切片 B（luoshen）均实施验收：`go vet ./...` 净，skins/settings/webui 包全绿，`npm --prefix frontend run build`（vue-tsc）通过。实施报告：`impl-report-backend.md`、`impl-report-frontend.md`。未 commit；手工 E2E（选图→生效→重启保持→恢复默认）待主上验证。

## 需求

用户选择本地图片 → 导入管理 → 应用为应用全局皮肤（背景层 + 蒙版调光/模糊），可调参数、可恢复默认。持久化于 settings.json。

## 架构设计

### 数据与存储

- 皮肤图片目录：`~/.amagi-codebox/skins/`（导入即拷贝，文件名 `<id>.<ext>`，id 为随机 hex；源文件不受影响）。
- settings.json 新增（跟随 TerminalSettings 既有 Get/Set 模式）：
  ```json
  "skin": { "enabled": false, "imageId": "", "dim": 35, "blur": 0 }
  ```
  - dim：蒙版不透明度 0..100（默认 35，保证前景可读）；blur：背景模糊半径 px 0..40。
- 校验：png/jpeg/webp（魔数校验，防改后缀），单文件 ≤ 20MB；尺寸解析 png/jpeg 用标准库（webp 容许未知=0）。

### 后端切片 A（luban，amagi-codebox）

1. `internal/settings`：`SkinSettings` 结构 + `GetSkinSettings/SetSkinSettings`（clamp 到合法区间；enabled 时 ImageID 必须存在于皮肤库——该校验放 skins 服务层，settings 层只管持久化与 clamp）。AppSettings 增 `Skin` 字段，默认值按上述；老 settings.json 无该键时零值安全。
2. 新包 `internal/skins`（绑定为 `app.Skins`，照 `internal/webui` 模式）：
   - `Skin{ID, FileName, URL("/skins/<file>"), Bytes, Width, Height, ImportedAt}`。
   - `PickSkinImage() (*Skin, error)`：`wailsRuntime.OpenFileDialog`（png/jpg/webp 过滤，参考 app.go:5017 既有用法；ctx 由 Startup 注入）→ 魔数+大小校验 → 拷贝入库 → 返回 Skin。
   - `ListSkins() ([]Skin, error)`、`RemoveSkin(id) error`（当前 enabled 且被应用的皮肤拒绝删除，返回明确错误）。
   - `GetSkinSettings() SkinSettings` / `SetSkinSettings(s) error`：委托 settings 服务；enabled 时校验 ImageID 存在。
   - `NewAssetHandler(skinsDir) http.Handler`：仅 GET `/skins/<file>`，filepath 清洗防穿越，按扩展名 Content-Type（png/jpeg/webp），目录与越界 404，无列目录。**只读**。
3. `main.go`：assetserver.Options 增 `Handler: skins.NewAssetHandler(...)`；`bind_list.go` 增 `app.Skins`；app.go 装配（rootDir 解析同其他服务）。
4. 测试：internal/skins（导入/魔数拒绝/超大拒绝/列表/删除保护/AssetHandler 穿越 404 与 MIME）+ settings 皮肤字段 round-trip；`go vet ./...`、`go test ./internal/skins/ ./internal/settings/`、bind_manifest 相关测试过。
5. **绑定再生成**：`wails generate module`（本机 wails CLI 可用）刷新 `frontend/wailsjs/go/skins/`，git diff 确认生成物入库；`docs/api.md` 增 skins 服务小节。

### 前端切片 B（luoshen，amagi-codebox，依赖 A 的绑定）

1. `api/skins.ts`：五个方法包装（callApi 模式）。
2. `stores/skin.ts`（Pinia）：`settings` + `currentSkin`；`load()`（GetSkinSettings + ListSkins 匹配出当前皮肤 URL）/`apply(patch)`/`clear()`。
3. 皮肤视觉层（App.vue 挂载点 + 全局样式）：
   - 固定背景层 `div.skin-layer`（inset:0、z-index 低于内容、`background-image: var(--skin-image)` cover、`filter: blur(var(--skin-blur))`、scale 补偿模糊边缘）+ 蒙版层 `rgba(0,0,0,var(--skin-dim))`。
   - html 根节点 `data-skin="on"` 时：`--window` 等窗口面背景转半透明（sidebar/card 用带 alpha 的 rgba，前景可读性优先，dim 默认 35 保底）；终端区域保持不透明（xterm 可读性）。
   - App.vue 启动 load 一次 + watch store 更新 CSS 变量与 data-skin；设置页保存后即时生效。
4. `views/settings/AppearanceSettings.vue`（新"外观"页签，注册进 SettingsView）：
   - 卡片"皮肤"：选择图片按钮（走后端对话框）、已导入皮肤缩略图网格（img src=URL），点击应用、高亮当前、支持删除；调光滑块（dim 0-100）、模糊滑块（0-40）；"恢复默认"清除 enabled。
   - 遵循 ui 组件库（Segmented/AppButton 等）与 tokens 设计语言，不用 Element Plus。
5. 验证：`npm --prefix frontend run build`（vue-tsc 门禁）通过；说明手工验收步骤。

### 边界与不做

- 不做多套完整主题色切换（仅图片皮肤）；不内置壁纸库；不做移动端 remote Web 的皮肤同步（壳内功能）。
- 图片仅本地导入拷贝，不联网。

## 验收（联合）

启动应用 → 设置 → 外观 → 选择本地图片 → 皮肤即时生效（背景+可调光/模糊）→ 重启应用皮肤保持 → 恢复默认回到原浅色外观；删除被应用皮肤被拒、删除未用皮肤成功。
