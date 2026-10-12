# P3 审查任务 — Provider Center 两页重构 diff 对抗检验（diting quick）

角色：diting（quick 审核档）。对象：本阶段集成 diff（v1 文案降级重构 + v2 左栏导航重构，两轮合计）。
产物：`agent-outputs/workflow/provider-center-layout-redesign/diting-review.md`。写入边界仅该文件；只读审查，不改代码、不跑破坏性 Git 操作（git diff/log/read 允许）。

## 审查对象与规格基线

- **diff 获取**：`git diff HEAD -- frontend/src e2e` + 未跟踪新文件（`git status --short` 中 `??` 的 frontend/src 部分）：预期 22 个 frontend/src 文件（15 既有改动 + 7 新增）+ e2e 两文件 + tokens.css。基线 HEAD=`0131c80`（**HEAD 不是 v1 态**，v1 中间态快照在 `agent-outputs/workflow/provider-center-layout-redesign/v1-snapshot/`，仅归因用）。
- **规格**：`design-v2.md`（布局权威，§8.2 S1–S12 结构不变量）+ `design.md` v1（T01–T57 文案处置与 §5.3 语义表）。
- **实现者自报**：`impl-report.md`（含 S1–S12 自查、V/X 证据、遗留清单 §8）——你的职责是对抗检验其可复核性，不是复述。

## 重点审查面（按风险排序）

1. **e2e 适配是否放松断言**（最高风险）：对照 `v1-snapshot/tracked.patch` 中旧 e2e（若含）与 git HEAD 中 `e2e/pi-amagi-config.spec.ts` 原版，逐条核对 design-v2 §5.2 的旧→新映射：card→compaction、showCompaction、3 处 reload 后导航、trigger 计数断言、保存对象完整相等断言、save-count 断言是否原样保留。新增结构用例是否真实测试（单显/草稿/roving）而非空转。
2. **I2 行为不回退**：7 处 `:key="presetLoadKey"`、lazyPreset/PresetLoadError 机制、watch(mainTab) engine 按需加载、5 个 handleSave 函数体（对照 v1-snapshot/tracked.patch 或 HEAD 逐字符比较——实现者称「与 v1 逐字符一致」，验证该声明）。
3. **I1 语义不丢**：design-v2 §6.1 表 11 项常显承载逐项在代码中定位（生效标签、受管警告、MCP 三态、并发清空回落、OAuth 只读、压缩后果句）。
4. **S11 泄露面**：Registry/Auth 的 navigation-change snapshot 与 SectionNav props 是否可能携带密钥/令牌/accountId（检查 items 构造与 uiId 逻辑）。
5. **S7 范围**：diff 是否越界（Go/wailsjs/api/stores/manifests/其他视图）；x-harness 与 log 文件在 agent-outputs 产物区（允许），不应出现在源码树。
6. **visited+v-show 草稿保留**（S10）：禁止 `:key="sectionId"` 包整个编辑器、切换不清洗数据的实现正确性；InfoTip/Dropdown 的 sectionVisibility 注入是否会在范围外消费者（其他页面用 Dropdown 的地方）引发回归。
7. **遗留项归因核实**：impl-report §8 声称皮肤 RangeError（App.vue/skinBake.ts）与 OCP 吞读错（store.loadPresets catch）均为**范围外既有**——用 `git diff HEAD --stat` 确认这两个文件不在本阶段 diff 中，验证归因声明。

## 输出格式（quick 契约）

- findings：每条含 severity（Critical/Major/Minor）+ 可复核证据（文件:行）+ 可失败行为描述；可附修复建议。
- 末行 VERDICT：PASS | PASS_WITH_MINOR | FIX。
- 若核对后支持实现者声明，明说「已核对成立」，不留模糊。

## 输入

- `agent-outputs/workflow/provider-center-layout-redesign/{design.md, design-v2.md, impl-report.md, p2r-taskbook.md}`
- `agent-outputs/workflow/provider-center-layout-redesign/v1-snapshot/`（归因基线）
- git 仓库本身（diff/log 自取）
