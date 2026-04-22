# 设计实现报告 -- OpenCode 预设结构化 GUI 改造

## 概要

| 项目 | 内容 |
|------|------|
| 任务 | 将 ProviderDetail.vue 中 OpenCode 预设的原始 JSON textarea 改造为结构化 GUI 编辑器 |
| 变更范围 | 1 个文件 |
| 设计方向 | 折叠面板式结构化表单 + 高级 JSON fallback 双向同步，暗色主题一致 |

## 设计决策

1. **折叠面板架构**: 将 `opencode_config` JSON 的 6 大高频配置域拆为独立可折叠面板（Provider/Model、MCP Servers、Agents、Permissions、Instructions、Plugins），用户按需展开，避免信息过载。

2. **双向同步机制**: GUI -> Raw JSON 方向为实时同步（每次 GUI input 事件触发 `ocGuiToRaw()`）；Raw JSON -> GUI 方向为手动触发（用户点击"从 JSON 同步到面板"），避免用户在高级 JSON 中编辑时 GUI 循环刷新。

3. **未知字段保真**: 所有不在 `OC_KNOWN_KEYS` 列表中的顶层 JSON key 被存入 `unknownFieldsRaw`，序列化时原样合并回输出，确保任意高级字段（`experimental`、`$schema`、未来新增字段）不丢失。

4. **预设卡片摘要增强**: 原先的 OC Config 行直接显示原始 JSON 字符串，改为结构化摘要（如 `model: anthropic/claude-opus-4-6 | mcp: 4 servers | agents: 12`），一眼可见配置概况。

5. **对话框自适应宽度**: 当 target=opencode 且包含结构化 GUI 面板时，对话框最大宽度从 600px 扩展到 740px（CSS `:has()` 选择器），确保 MCP/Agent 等子配置有足够空间。

## 代码变更清单

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `frontend/src/views/ProviderDetail.vue` | 重构 | 替换 OpenCode JSON textarea 为结构化 GUI + 高级 JSON fallback |

### 变更详情

**模板部分**:
- 移除原始 `editingPresetTarget === 'opencode'` 下的纯 textarea
- 新增 `.opencode-gui-section` 容器，含 7 个折叠面板:
  - Provider / Model: `model` 字符串输入 + `provider` JSON 子编辑器
  - MCP Servers: 列表式编辑，每项含 name/type/url/command/headers/environment/oauth
  - Agents: 列表式编辑，每项含 name/description/mode/model/color/prompt/tools
  - Permissions: key-value 行列表，value 为 allow/deny/ask 下拉
  - Instructions: 字符串列表
  - Plugins: 字符串列表
  - 高级 JSON: 完整 JSON textarea + "从 JSON 同步到面板" 按钮

**脚本部分**:
- 新增接口: `OcMcpEntry`, `OcAgentEntry`, `OcPermEntry`
- 新增状态: `ocExpandedSections`, `ocGuiState`
- 新增函数:
  - `rawToOcGui()`: 解析 raw JSON 到 GUI 结构
  - `ocGuiToRaw()`: 序列化 GUI 状态回 raw JSON
  - `onAdvancedJsonInput()`: 高级 JSON 手动编辑处理
  - `addOcMcp/removeOcMcp/addOcAgent/removeOcAgent/...`: 列表 CRUD
  - `ocConfigSummary()`: 预设卡片配置摘要
- 修改 `openAddPresetDialog`: 初始化 OC GUI 状态
- 修改 `openEditPresetDialog`: 载入时调用 `rawToOcGui()`
- 新增 `watch(editingPresetTarget)`: target 切换到 opencode 时初始化 GUI

**样式部分**:
- 新增 `.opencode-gui-section` 系列样式（折叠面板、列表项、KV 行、计数徽章等）
- 新增 `.dialog:has(.opencode-gui-section)` 扩展对话框宽度
- 新增 `.oc-config-summary` 预设卡片摘要样式

## 自测报告

| 检查项 | 状态 | 说明 |
|--------|------|------|
| 前端构建通过 | OK | `vue-tsc --noEmit && vite build` 零错误 |
| Go 构建通过 | OK | `go build ./...` 零错误 |
| 全 7 态覆盖 | OK | 折叠面板展开/收起态正常；空态/有数据态/编辑态/删除态/添加态/错误态(JSON invalid)/成功态均覆盖 |
| 视觉一致性 | OK | 沿用现有 card/panel/input/btn 样式，无新增色彩方案，暗色主题一致 |
| 反 AI 垃圾 | OK | 无紫色渐变、无 cookie-cutter 卡片、无系统字体堆叠 |
| 双向同步 | OK | GUI->JSON 实时；JSON->GUI 手动同步按钮；未知字段不丢失 |
| 响应式适配 | OK | 弹窗内布局沿用 `form-grid-2` 已有的自适应行为 |
| 无障碍 | OK | 所有交互元素可键盘聚焦；颜色对比度沿用现有标准 |

## 已知限制

1. **嵌套 provider 子编辑**: provider 字段内部结构复杂（嵌套 options/models/variants），目前提供原始 JSON 子编辑器而非完全结构化。后续可按需深化。
2. **experimental 字段**: 已在 unknownFieldsRaw 中保真保留，但未提供独立面板（因 schema 开放性极高）。高级 JSON 面板已覆盖。
3. **Agent tools 子项**: tools 黑名单以 JSON 子编辑器呈现，未拆为独立 checkbox。对于高频工具名可后续优化。

## 建议下一步

建议下一步：谛听（reviewer）审核 `ProviderDetail.vue` 的 GUI 逻辑正确性、双向同步边界条件、以及未知字段保真测试。
