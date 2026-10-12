# P2V4 实现任务 — design-v4 父子导航 + 图标矩形归组 + 左栏纯净化（luoshen）

角色：luoshen。主特色：审美判断（UI 实现、完整状态覆盖、视觉验证是交付证据）；附带·轻：工匠闭环（构建/测试自验、报告）。

## 1. 必读 artifact（按序全文，缺失即停止报告）

1. **`agent-outputs/workflow/provider-center-layout-redesign/design-v4.md`（v4 实现规格，全文）**
2. `design-v3.md` / `design-v2.md`（v4 未覆盖处依次沿用）
3. 当前实现树（开工先核对 HEAD 基线）

「必须」条款不回头确认；实现细节级偏差自行决定并记报告；设计级偏差不允许。

## 2. 开工核对（design-v4 §8.4 要求的现场复核）

- `git rev-parse HEAD` 记录完整 SHA（S6 冻结基线）；`git status --porcelain -- frontend/src e2e` 应为空。

## 3. 生产白名单（S7：超出即验收不过，design-v4 §3）

**改**：`frontend/src/views/ProviderCenterView.vue`（§3.1 父子两行 subnav）、`frontend/src/components/ui/SectionNav.vue`（§3.2 纯净化+限高）、`frontend/src/components/provider/PiAmagiConfig.vue`（§3.4 MCP/并发归组）、`frontend/src/components/provider/ProviderRegistryEditor.vue`（§3.5 模型归组+正文次序）、`frontend/src/components/provider/OpenCodeGlobalConfig.vue`（§3.6 三类记录轻矩形 deep 样式）
**新增**：`frontend/src/components/provider/ConfigContentGroup.vue`（§3.3 完整规格）
**零改动**：Auth/OMP/configNavigation/Segmented/EditorSection/EditorToolbar/FieldLabel/icons/ConfigCategoryCard/PresetList/OpenCodePresets/Map 子编辑器/e2e 两文件/4 逻辑测试（§3.7 清单）
**验证工具**：x-harness 新建 `v4-{helpers.js,ab.sh,shots.sh,runtime.mjs,static.mjs}`（v2/v3 原样保留）
**产物**：impl-report.md 追加 §10「v4 轮」；截图 `v4-` 前缀

## 4. 实现切片（design-v4 §8.2）

① 导航+左栏（ProviderCenterView+SectionNav）→ V1/V3 早验 → ② ConfigContentGroup + Pi/Registry 归组 → ③ OpenCode 记录样式（注意 §3.6 选择器拆分，permission/嵌套模型必须保持无框）→ ④ v4-static + 全部批次 + 两档截图读图。

## 5. 验证要求（证据入报告）

| # | 验证 |
|---|---|
| V1/V2/V3 | build exit 0；provider 4 文件 31 测试；Pi e2e 7 passed（e2e 零 diff） |
| V4–V6 | v4-runtime drafts / lifecycle / **content（新增：源码与保存值契约核对）** |
| 七批+ | drafts/dynamic/lifecycle/narrow/states/coverage/interactions 按 §7.4 适配后全跑 |
| X1–X14 | §7.3 口径（385/425 估算与 400/440 基准上限；X13 归组白名单核对；X14 纯净左栏+右侧承载对照截图） |
| 环境 | dev server `npm --prefix frontend run dev -- --port 5199 --strictPort --host 127.0.0.1` |

## 6. 硬约束

- I1–I7 + C1 全部（design-v4 §1.3）；尤其：左栏条目可视层仅名称（I6）；归组不恢复整页卡平铺（I7）；保存/解析/字段函数逐字不变（S6）；7 处 presetLoadKey + 1 处 activePageId；Registry 正文次序调整（其他字段移到模型后）是**有意变更**按 §3.5 执行。
- harness 判定陷阱注意（§7.4-9）：sr-only 描述不能用 toBeHidden 判定；不按 textContent 匹配左栏按钮。
- 中文 UI；token 不动；不新增依赖。视觉裁决自行读图，不能读的标注待 Leader。
