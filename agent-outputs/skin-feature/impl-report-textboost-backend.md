# 皮肤「字体调光 TextBoost」后端实现报告

- **节点**: skin-textboost-backend（batch 三联动需求 · amagi-codebox 侧）
- **日期**: 2026-08-18
- **仓库**: amagi-codebox @ 9348d6d (v1.3.33) 之上追加，未 commit
- **模式**: 完全照搬上一轮 opacity 字段实施模式（参考 impl-report-opacity-backend.md）

## 1. 需求与实现

新增与 dim、opacity 解耦的「前景文字加深」设置 `TextBoost`（字体浓度 0..100，默认 0=不增强、保持现状）。

| 项 | 实现 |
|---|---|
| 字段 | `internal/settings/service.go` `SkinSettings` 新增 `TextBoost int \`json:"textBoost"\``（附于 Opacity 之后），注释说明 0..100 语义及与 Dim（背景蒙版调光）、Opacity（内容面板不透明度）的三者解耦：TextBoost 只作用于前景文字加深与底衬强度 |
| 默认值 | `DefaultSkinSettings()` → `Dim:35, Blur:0, Opacity:70, TextBoost:0`（0 即"不增强"）；`defaultSettings()` 经由 `DefaultSkinSettings()` 自动同步 |
| Clamp | `normalizeSkinSettings()` 对 TextBoost 取边界 [0,100]（与 Dim/Opacity 同款 if-clamp）。该函数同时覆盖 `SetSkinSettings`、`Load` 合并路径与 settings 更新路径（service.go 三处调用点），Set/Load 双侧生效 |
| 绑定 | `/Users/maorun/go/bin/wails generate module`（wails v2.11.0）再生成：`frontend/wailsjs/go/models.ts` 的 `settings.SkinSettings` 类 +2 行（`textBoost: number` 字段与构造器赋值）；`go/settings|skins/Service.d.ts` 引用该类，`SetSkinSettings(arg1: settings.SkinSettings)` 签名不变、自动携带新字段 |
| 文档 | `docs/api.md` Settings Service 的 Get/SetSkinSettings 小节：返回结构补 `textBoost`、默认值 `{false, "", 35, 0, 70, 0}`、clamp 区间补 [0,100]、文案语义（前景文字加深+底衬强度，与背景调光 dim、面板透明度 opacity 独立）、老文件缺子键读入 0 说明 |

`internal/skins/service.go` 纯委托零改动；仓库内其他 SkinSettings 构造点仅 `internal/skins/skins_test.go`（字面量不含 TextBoost，走零值合法路径，未改即通过）。

## 2. 老配置零值取舍（比 opacity 更干净）

与 opacity（0≠默认 70）不同，**TextBoost 的默认值就是 0**：老 settings.json 含 `skin` 键但缺 `textBoost` 子键时读入 0，恰好等于默认值（不增强、保持现状），**无任何突变问题**——零值与"未写入该键"不可区分，但两者语义重合，无需回填机制。该取舍由新增测试 `TestSkinSettings_LegacySkinKeyWithoutTextBoost` 固化，并在 `normalizeSkinSettings` 注释中说明。

- 无 `skin` 键的老用户：整体零值 → 回落默认（textBoost=0），无变化。
- 含 `skin` 键缺 `textBoost` 子键（v1.3.33 及之前写出）：读入 0=默认，无变化。
- 全量用户升级后行为完全保持现状，直到主动调节。

## 3. 测试

`internal/settings/service_test.go`（照 opacity 既有用例模式增补）：

- `TestSkinSettings_DefaultAndRoundTrip`：默认 want 补 `TextBoost:0`；Set/重载往返补 `TextBoost:60`。
- `TestSkinSettings_ClampOutOfRange`：**clamp 边界 -1/101** → `TextBoost:101→100`、`TextBoost:-1→0`（连同 dim/blur/opacity 边界一起断言）。
- `TestSkinSettings_MissingKeyFallsBackToDefault`：无 skin 键 → 默认含 `TextBoost:0`。
- `TestSkinSettings_LegacySkinKeyWithoutOpacity`：既有测试原样保留（opacity 取舍）。
- `TestSkinSettings_LegacySkinKeyWithoutTextBoost`（新增）：`{"skin":{dim,blur,opacity}}` 缺 textBoost → 读入 0、其余字段原样，固化第 2 节取舍。
- `TestSkinSettings_LoadOutOfRangeClamped`：手改文件 `textBoost:500` → Load clamp 100。

## 4. 修改文件

| 文件 | 变更 |
|---|---|
| `internal/settings/service.go` | SkinSettings+TextBoost、默认 0、clamp [0,100]、注释（含零值取舍） |
| `internal/settings/service_test.go` | 5 个既有皮肤测试增补 textBoost + 1 个新增取舍测试 |
| `frontend/wailsjs/go/models.ts` | 生成器再生成（+2 行 textBoost），非手改 |
| `docs/api.md` | Settings Service Get/SetSkinSettings 两处补 textBoost |

## 5. 验证

- `go vet ./...` exit 0（净，无新增告警）。
- `go test ./internal/skins/ ./internal/settings/ -count=1` → 两包 `ok`；`-run Skin -v` 明细：skins 9 项 PASS（含 SetSkinSettings 存在性校验）、settings 6 项 PASS（含新增 LegacySkinKeyWithoutTextBoost）。
- `wails generate module` 后 `grep textBoost frontend/wailsjs/go/models.ts` 命中 `textBoost: number`（L2959）与构造器赋值（L2972）；`go/settings/Service.d.ts`（L31/L63）与 `go/skins/Service.d.ts`（L10/L22）的 `SkinSettings` 引用确认。
- `git status`：工作区仅上述 4 文件改动，未 commit、未碰 `frontend/src`。

## 6. 副作用与风险

- **基线说明**：任务描述提到"工作区有未提交皮肤改动"，实际上一轮 opacity 后端已随 v1.3.33（9348d6d）commit；本轮追加时工作区干净，diff 全部为本节点改动。`wails generate module` 未产生其他文件漂移（runtime/ 目录与 HEAD 一致）。
- **未覆盖**：前端消费（滑块/CSS 变量应用 textBoost）与 pi webui 端点属 batch 其他节点；本节点未触碰 `frontend/src`、未 commit。
- **无已知突变**：textBoost 默认 0=保持现状，见第 2 节。
