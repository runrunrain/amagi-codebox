# P3V4 审查任务 — v4 diff 对抗检验（diting quick）

角色：diting（quick 审核档）。对象：v4 阶段 diff（基线 HEAD=8079acb，工作树未提交改动）。
产物：`agent-outputs/workflow/provider-center-layout-redesign/diting-review-v4.md`。写入边界仅该文件；只读审查（git diff/log/read 允许）。

## 审查对象与规格

- **diff**：`git diff 8079acb -- frontend/src`（5 改 + 未跟踪 `ConfigContentGroup.vue`）+ x-harness/v4-*（验证工具，抽查即可）。
- **规格**：`design-v4.md`（权威）+ `impl-report.md` §10（实现者自报，对抗检验其可复核性）。

## 重点审查面（按风险排序）

1. **OpenCode deep 选择器手术**（最高风险，§3.6）：provider/agent/mcp 三类直接子记录恢复轻矩形，permission/嵌套 pe-model-card/variants 必须保持无框——核对选择器是否精确拆分、有无误伤（合并选择器或过宽祖先）；宿主约束是否只作用本页。
2. **S6 函数逐字保留声明**：Pi/Registry 脚本除新增 import 外与基线逐字符一致；OpenCode 脚本全文相同；View 唯一新增 currentCliLabel；Nav 唯一新增 descriptionText——用 AST 或严格文本比较独立验证。
3. **I6 左栏纯净化**：SectionNav item/child 可视层仅 sn-name；描述节点为规范 sr-only（非 display:none）；meta/invalid 数据接口保留（aria-describedby 仍指真实描述）；notice 独立保留；roving/焦点行为未动。
4. **I1 语义右侧承载**：design-v4 §5.2 表逐项在当前 diff 后的代码中定位（受管警告、压缩 meta/后果/alert、类型 Badge、生效标签、MCP 三态空态、并发 placeholder）——尤其确认净化没有顺手删 navigationItems 数据或右侧 meta。
5. **ConfigContentGroup 新组件**：无状态/无事件/无 IO；icon 仅调用方常量；无 region/h3/tabindex；不影响 EditorSection 可见性上下文。
6. **Registry 正文次序变更**（其他字段移到模型后）按 §3.5 执行且无字段丢失/隐藏。
7. **范围**：S7 白名单恰 6 路径；e2e/4 逻辑测试/依赖/icons/ConfigCategoryCard/Auth/OMP/configNavigation 零 diff。
8. **e2e 契约**：`git diff 8079acb -- e2e` 为空；spec 依赖的选择器/名称在 diff 后仍成立（压缩 region/h3、mss-dd 顺序、导航按钮精确名）。

## 输出（quick 契约）

findings（severity + 文件:行 + 可失败行为）+ 末行 VERDICT（PASS | PASS_WITH_MINOR | FIX）。核对后成立的实现者声明明说「已核对成立」。
