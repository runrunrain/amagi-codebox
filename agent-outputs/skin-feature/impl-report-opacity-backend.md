# 皮肤「内容不透明度 Opacity」后端实现报告

- **节点**: skin-opacity-backend（batch 三联动需求 · amagi-codebox 侧）
- **日期**: 2026-08-18
- **仓库**: amagi-codebox @ 4b7b6b8 (v1.3.32) 工作区之上追加，未 commit

## 1. 需求与实现

主上要求新增与 dim 解耦的「内容面板不透明度」设置：`0 = 窗口/侧栏/卡片等面板全透出皮肤图片，100 = 面板不透明`，默认 70。

| 项 | 实现 |
|---|---|
| 字段 | `internal/settings/service.go` `SkinSettings` 新增 `Opacity int \`json:"opacity"\``（附于 Blur 之后），注释说明 0..100 语义及与 Dim（背景蒙版层）的解耦关系 |
| 默认值 | `DefaultSkinSettings()` → `Dim:35, Blur:0, Opacity:70`；`defaultSettings()` 经由 `DefaultSkinSettings()` 自动同步（无重复字面量） |
| Clamp | `normalizeSkinSettings()` 对 Opacity 取边界 [0,100]（与 Dim 同款 if-clamp）。该函数同时覆盖 `SetSkinSettings`、`Load` 合并路径与 settings 更新路径（service.go 三处调用点），Set/Load 双侧生效 |
| 绑定 | `/Users/maorun/go/bin/wails generate module`（wails v2.11.0）再生成：`frontend/wailsjs/go/models.ts` 的 `settings.SkinSettings` 类 +2 行（`opacity: number` 字段与构造器赋值）；`go/settings|skins/Service.d.ts` 引用该类，`SetSkinSettings(arg1: settings.SkinSettings)` 签名不变、自动携带新字段 |
| 文档 | `docs/api.md` Settings Service 的 Get/SetSkinSettings 小节：返回结构补 `opacity`、默认值 `{false, "", 35, 0, 70}`、clamp 区间补 `[0,100]`、老文件迁移语义说明 |

`internal/skins/service.go` 纯委托（Get 直通、Set 只做 ImageID 存在性校验后转 settings 层），零改动即兼容；仓库内无其他 SkinSettings 构造点（已 grep 确认）。

## 2. 老配置零值取舍（任务要求说明）

**现状**：settings 层无 per-field 默认回填机制，既有惯例是「整个 skin 键缺失 → 结构体零值 → `normalizeSkinSettings` 整体回落默认值」（dim 同款，且注释明确“零值与未写入该键不可区分”）。

**采取方案**（任务契约指定的兜底路径）：Set/Load 时 clamp，Get 不做改写。

- **无 `skin` 键的老用户**（v1.3.32 之前，绝大多数）：整体零值 → 回落默认 `opacity=70`，**无突变**。
- **有 `skin` 键但缺 `opacity` 子键**（仅 v1.3.32 用户改过皮肤设置）：读入 `opacity=0`。取舍理由：
  1. 0 是合法档位（全透），若按 0 回填 70 则用户永远无法持久化 0——契约字段形状是 plain int（`json:"opacity"`），Go unmarshal 无法区分 0 与缺失；
  2. 与 dim 的既有取舍一致（dim=0 同样不可区分，注释原文“误重置无害”）；
  3. `enabled=false` 时零值无渲染影响；`enabled=true` 时表现为面板全透，属一次性可见变化，前端滑块同捆发布可即时调回并持久化；
  4. 若要彻底区分需在 Load 时用 gjson 探测原始键再回填，属新建 per-field 回填机制，超出任务授权的惯例路径。
- 该行为由新增测试 `TestSkinSettings_LegacySkinKeyWithoutOpacity` 固化并在 `normalizeSkinSettings` 注释中说明。

## 3. 测试

`internal/settings/service_test.go`：

- `TestSkinSettings_DefaultAndRoundTrip`：默认 want 补 `Opacity:70`；Set/重载往返补 `Opacity:85`。
- `TestSkinSettings_ClampOutOfRange`：**clamp 边界 -1/101** → 高边界 `Opacity:101→100`，低边界 `Opacity:-1→0`（连同 dim/blur 原边界一起断言）。
- `TestSkinSettings_MissingKeyFallsBackToDefault`：无 skin 键 → 默认含 `Opacity:70`。
- `TestSkinSettings_LegacySkinKeyWithoutOpacity`（新增）：`{"skin":{dim,blur}}` 缺 opacity → 读入 0、其余字段原样，固化第 2 节取舍。
- `TestSkinSettings_LoadOutOfRangeClamped`：手改文件 `opacity:500` → Load clamp 100。

`internal/skins` 既有测试（存在性校验、启用保护等）未改即通过——其字面量不含 Opacity，走零值合法路径。

## 4. 修改文件

| 文件 | 变更 |
|---|---|
| `internal/settings/service.go` | SkinSettings+Opacity、默认 70、clamp [0,100]、注释（含零值取舍） |
| `internal/settings/service_test.go` | 4 个既有皮肤测试增补 opacity + 1 个新增取舍测试 |
| `frontend/wailsjs/go/models.ts` | 生成器再生成（+2 行 opacity），非手改 |
| `docs/api.md` | Settings Service Get/SetSkinSettings 两处补 opacity |

## 5. 验证

- `go vet ./...` exit 0（仅存量 cgo Keychain 弃用告警，与本改动无关）。
- `go test ./internal/skins/ ./internal/settings/ -count=1` → 两包 `ok`；`-run Skin -v` 明细：skins 8 项 PASS、settings 5 项 PASS（含新增）。
- `wails generate module` 后 `grep opacity frontend/wailsjs/go/models.ts` 命中 `opacity: number` 与构造器赋值；`Service.d.ts` 两处 `SkinSettings` 引用确认。

## 6. 副作用与风险

- **wailsjs/runtime 覆写**：执行前工作区有 3 个未提交本地改动（`frontend/wailsjs/runtime/{package.json,runtime.d.ts,runtime.js}`，疑为旧版 wails 生成残留）。`wails generate module` 用 v2.11.0 模板再生成后这些文件回到与 HEAD 一致（生成器自有文件，任务明确要求重新生成绑定，此为该命令的固有行为）。Go 绑定面（`go/` 目录）净差异仅 models.ts +2 行。
- **未覆盖**：前端消费（滑块/CSS 变量 `--window` 等应用 opacity）与 pi webui 草稿端点属 batch 其他节点；本节点未触碰 `frontend/src`、未 commit。
- **已知取舍**：见第 2 节——v1.3.32 且 enabled=true 的老配置升级后首次以 opacity=0 渲染（面板全透），由前端滑块可即时纠正。
