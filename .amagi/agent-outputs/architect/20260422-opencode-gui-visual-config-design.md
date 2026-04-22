# OpenCode 可视化配置改造设计文档

> **一句话摘要**：把 ProviderDetail 里 OpenCode preset 的"裸 JSON 文本框"升级为结构化表单（provider/model/agents/mcp/permissions），同时保留高级 JSON fallback，改动范围锁定在 Preset 数据结构 + ProviderDetail 前端一个弹窗。

---

## 一、需求背景

当前 amagi-codebox 的 OpenCode 配置路径已打通（提交 60af2fd），但在 ProviderDetail 的预设编辑弹窗中，OpenCode 配置仍是一个原始 JSON 文本框。用户需要手写 JSON 才能配置 OpenCode，这与 Codex preset 的表单化体验形成明显落差。

用户的全局 `opencode.json`（`C:/Users/毛润/.config/opencode/opencode.json`）已包含 provider、agent、mcp、permission、instructions 等复杂结构，说明实际使用中 OpenCode 配置项远不止"选个模型"那么简单。

**目标**：在最小改动前提下，把 OpenCode preset 中最常用的 4 类配置项（provider 认证、model 选择、agents 定义、mcp 服务器、permissions）表单化，让 80% 的场景不需要手写 JSON。其余高级字段（instructions、experimental、compaction、plugin 等）保留 JSON fallback。

---

## 二、现状分析

### 2.1 数据层现状

**Preset 结构**（`internal/config/types.go`）：

```go
type Preset struct {
    Name           string           `json:"name"`
    Model          string           `json:"model"`
    Parameters     Parameters       `json:"parameters"`
    Target         PresetTargetType `json:"target,omitempty"`
    OpenCodeConfig json.RawMessage  `json:"opencode_config,omitempty"`
}
```

- `Target` 已区分 `codex` / `opencode`
- `OpenCodeConfig` 用 `json.RawMessage` 存储原始 JSON，保真不丢字段
- `NormalizeOpenCodeConfig()` 已处理双重编码

**启动链路**（`internal/launcher/opencode_config.go`）：

- `BuildOpenCodeRuntimeConfig()` 从 Provider + Preset 生成运行时配置
- 逻辑：推导 provider ID -> 构建模型 -> 构建 provider 认证 -> 深度合并 `preset.OpenCodeConfig`
- 最终通过 `OPENCODE_CONFIG_CONTENT` 注入

**全局 opencode.json 的实际结构**（用户当前使用）：

```
{
  "provider": {
    "<id>": {
      "options": { "apiKey": "...", "baseURL": "..." },
      "models": { "<model>": { "name": "...", "options": {...}, "variants": {...} } }
    }
  },
  "agent": { "<name>": { "description", "mode", "model", "tools", "prompt" } },
  "mcp": { "<name>": { "type", "url/command", "headers", "environment" } },
  "permission": { "read": "allow", "write": "allow", ... },
  "instructions": [...],
  "plugin": [...],
  "experimental": { ... }
}
```

### 2.2 前端现状

**ProviderDetail.vue** 预设编辑弹窗：
- 已有 target 下拉（Codex / OpenCode）
- 当 target=opencode 时，显示一个 JSON textarea（`editingOpenCodeConfig`）
- 验证逻辑已有（JSON 格式校验）
- 保存时原样写入 `preset.opencode_config`

**Dashboard.vue** OpenCode 启动面板：
- Provider 下拉（openai 类型过滤）
- Preset 下拉（target=opencode 过滤）
- 启动模式、Shell、工作目录

**Settings.vue**：
- 默认 OpenCode Provider 下拉
- 无 OpenCode preset 级别的可视化配置入口

### 2.3 关键约束

| 约束 | 影响 |
|------|------|
| `opencode_config` 是 `json.RawMessage`，必须保持任意 JSON 保真 | 不能拆散为 Go struct，必须保持自由格式 |
| 全局 `opencode.json` 已有完整配置 | Preset 的 `opencode_config` 只是覆盖层，不是全量 |
| 前端 Wails 绑定自动生成 | Preset struct 改动会自动反映到 models.ts |
| `BuildOpenCodeRuntimeConfig` 做深度合并 | 前端只需关心覆盖字段，不需要写全量配置 |

---

## 三、设计目标

| # | 目标 | 可验证标准 |
|---|------|-----------|
| G1 | OpenCode preset 的核心配置（provider/model）通过表单填写 | 不需要手写 JSON 即可完成"选 provider + 选 model" |
| G2 | agents 和 mcp 配置可通过可视化列表编辑 | 至少支持增/删 agent 条目和 mcp 条目 |
| G3 | permissions 通过 checkbox 组编辑 | 6 个权限项各一个开关 |
| G4 | 不支持表单化的字段通过 JSON textarea fallback | 与现有行为完全兼容 |
| G5 | 与全局 opencode.json 的关系清晰可见 | UI 上标注"此配置为覆盖层，与全局配置合并" |
| G6 | 改动范围最小化 | 后端零新接口，前端仅改 ProviderDetail.vue |

---

## 四、方案设计

### 推荐方案：前端侧结构化解析 + 后端保持 RawMessage

**核心思路**：后端 `Preset.OpenCodeConfig` 保持 `json.RawMessage` 不变，不新增 Go struct。所有结构化解析和表单化逻辑完全在前端完成。前端把 `opencode_config` 的 JSON 解析为结构化表单控件，用户编辑后再序列化回 JSON 保存。

> **设计决策**：为什么不新增后端 struct？
> - OpenCode 的 schema 持续演进，硬编码 struct 会引入维护负担
> - `json.RawMessage` 已满足保真需求，且 `BuildOpenCodeRuntimeConfig` 已正确处理深度合并
> - 前端解析更灵活，可以渐进式表单化，不需要后端同步改动
> - 减少改动链路：后端零改动，只需要前端 ProviderDetail.vue 的一个弹窗区域重写

### 4.1 实现边界：哪些表单化，哪些保留 JSON

| 配置项 | 表单化 | 理由 |
|--------|--------|------|
| `model`（顶层） | 已有（Preset.model） | 已在弹窗中 |
| `provider.<id>.options`（apiKey/baseURL） | 不表单化 | 由 amagi-codebox Provider 体系自动推导，手动覆盖是边缘场景 |
| `provider.<id>.models.<model>.options` | 表单化 | thinking/temperature 等模型级参数是高频配置 |
| `provider.<id>.models.<model>.variants` | 不表单化 | 使用频率低，保留 JSON fallback |
| `agent`（整块） | 表单化 | 高频配置，用户有 13 个 agent |
| `mcp`（整块） | 表单化 | 高频配置，用户有 4 个 MCP 服务器 |
| `permission`（整块） | 表单化 | 结构简单（6 个 bool 开关） |
| `instructions` | 不表单化 | 文件路径列表，低频，保留 JSON fallback |
| `plugin` | 不表单化 | 低频 |
| `experimental` | 不表单化 | 不稳定 API，保留 JSON fallback |
| `compaction` | 不表单化 | 低频 |
| 其他未知字段 | 不表单化 | 保真保留在 JSON fallback 中 |

### 4.2 前端数据结构（TypeScript 接口草案）

前端在 ProviderDetail.vue 中定义以下接口，用于解析 `opencode_config` JSON：

```typescript
// opencode_config 解析后的结构化视图
interface OpenCodeConfigView {
  // === 表单化区域 ===
  
  // provider 模型选项覆盖（非认证部分，认证由 Provider 体系推导）
  providerOverrides?: {
    [providerId: string]: {
      models?: {
        [modelId: string]: {
          name?: string
          options?: Record<string, any>  // thinking, temperature, etc.
        }
      }
    }
  }

  // agents 定义
  agents?: {
    [name: string]: {
      description?: string
      mode?: string         // "primary" | "subagent"
      model?: string        // e.g. "zhipuai/glm-5.1"
      color?: string
      tools?: Record<string, boolean>  // e.g. { "bash": false, "edit": true }
      prompt?: string
    }
  }

  // MCP 服务器
  mcpServers?: {
    [name: string]: {
      type?: string         // "local" | "remote"
      url?: string          // remote 类型
      command?: string[]    // local 类型
      headers?: Record<string, string>
      environment?: Record<string, string>
    }
  }

  // 权限配置
  permissions?: {
    read?: boolean
    write?: boolean
    bash?: boolean
    edit?: boolean
    glob?: boolean
    grep?: boolean
    webfetch?: boolean
    apply_patch?: boolean
    task?: boolean
  }

  // === JSON fallback 区域 ===
  rawJson: string  // 无法被表单识别的字段，原样保存
}
```

**解析/序列化策略**：

```
parseOpenCodeConfig(jsonStr: string): OpenCodeConfigView
  1. JSON.parse 得到 obj
  2. 提取 obj.agent -> view.agents
  3. 提取 obj.mcp -> view.mcpServers
  4. 提取 obj.permission -> view.permissions (转换 "allow"->true, 其他->false)
  5. 提取 obj.provider (仅 models 子结构，不含 options) -> view.providerOverrides
  6. 剩余未知字段 JSON.stringify -> view.rawJson

serializeOpenCodeConfig(view: OpenCodeConfigView): string
  1. 以 rawJson 为基础（若非空则 JSON.parse）
  2. 覆盖写入 agents/mcpServers/permissions/providerOverrides
  3. JSON.stringify 返回
```

### 4.3 后端接口

**本次不改后端接口。** 后端保持现状：

| 已有接口 | 说明 | 本次变更 |
|----------|------|---------|
| `SavePreset(providerName, presetName, preset)` | 保存 preset，含 `opencode_config` 原始 JSON | 无 |
| `GetProvider(providerName)` | 返回 provider 含所有 presets | 无 |
| `Preset.NormalizeOpenCodeConfig()` | 保存前自动处理双重编码 | 无 |
| `BuildOpenCodeRuntimeConfig()` | 启动时深度合并生成运行时配置 | 无 |

后端数据流不变：
```
前端序列化的 JSON -> Preset.OpenCodeConfig (json.RawMessage) 
    -> 保存到 models.json
    -> 启动时 BuildOpenCodeRuntimeConfig 深度合并
    -> OPENCODE_CONFIG_CONTENT 环境变量
```

### 4.4 前端页面落点

**唯一改动点：ProviderDetail.vue 的预设编辑弹窗**

改动范围限定在 `showPresetDialog` 弹窗内部。当 `editingPresetTarget === 'opencode'` 时，替换现有的 JSON textarea，改为以下分区布局：

```
+--------------------------------------------------+
| 目标平台: [OpenCode v]                            |
+--------------------------------------------------+
|                                                    |
| [1. Provider 模型选项]  (可折叠)                   |
|   Provider ID: [下拉/输入]                         |
|   Model ID:    [输入]                              |
|   Model Options: [JSON textarea, 小]               |
|   [+ 添加 Provider-Model 覆盖]                    |
|                                                    |
| [2. Agents]  (可折叠)                              |
|   +----------------------------------------------+|
|   | agent-name  | model          | mode  | [删] ||
|   | agent-name2 | openai/gpt-5.4 | sub   | [删] ||
|   +----------------------------------------------+|
|   [+ 添加 Agent]                                   |
|   (点击某 agent 展开: description/model/tools/prompt)|
|                                                    |
| [3. MCP 服务器]  (可折叠)                          |
|   +----------------------------------------------+|
|   | mcp-name  | type   | url/command    | [删]   ||
|   +----------------------------------------------+|
|   [+ 添加 MCP 服务器]                              |
|   (点击展开: headers/environment)                   |
|                                                    |
| [4. 权限配置]  (可折叠)                            |
|   [x] read  [x] write  [x] bash  [x] edit         |
|   [x] glob  [x] grep  [x] webfetch  [x] task      |
|                                                    |
| [5. 高级 JSON 配置]  (可折叠, 默认收起)            |
|   [JSON textarea - 保留未识别字段]                  |
|                                                    |
+--------------------------------------------------+
|                    [取消]  [保存]                   |
+--------------------------------------------------+
```

**交互细节**：

| 区域 | 交互方式 | 说明 |
|------|---------|------|
| Provider 模型选项 | 列表 + 展开编辑 | 支持多个 provider-id/model-id 组合 |
| Agents | 表格 + 展开详情 | 表格显示 name/model/mode，点击展开完整配置 |
| MCP 服务器 | 表格 + 展开详情 | 表格显示 name/type/url-or-command |
| 权限 | Checkbox 组 | 开/关切换，初始状态从 `permission` 字段读取 |
| 高级 JSON | textarea（可折叠） | 显示无法被表单识别的字段，编辑后原样保存 |

**为什么不在 Settings.vue 加 OpenCode 配置入口？**

- Settings.vue 的职责是"全局默认值"（默认 provider、默认 mode、默认 shell）
- OpenCode preset 的详细配置属于"预设编辑"范畴，天然归属 ProviderDetail
- 加到 Settings 会让页面膨胀，且与现有的"常规设置"定位冲突

**为什么不在 Dashboard.vue 加配置入口？**

- Dashboard 的职责是"快速启动"，不应承担配置编辑职责
- 用户需要配置 preset 时，应导航到 ProviderDetail 进行详细编辑
- Dashboard 只需要做好"选 provider -> 选 preset -> 启动"的流程

### 4.5 Preset 体系与全局 opencode.json 的优先级策略

启动时的配置合并顺序（已有，无需改动）：

```
1. amagi-codebox Provider 认证信息（apiKey/baseURL 自动推导）
2. Preset.model -> 覆盖 model 字段
3. Preset.parameters -> 生成模型选项（thinking/temperature 等）
4. Preset.OpenCodeConfig -> 深度合并覆盖上述
    |
    +-- 最终生成 OPENCODE_CONFIG_CONTENT
5. 全局 opencode.json -> OpenCode 自行加载，作为底层默认
```

**优先级**（从高到低）：

```
Preset.OpenCodeConfig (per-session 覆盖)
    > Preset.parameters (preset 级参数)
    > Provider 认证推导 (provider 级认证)
    > 全局 opencode.json (OpenCode 自身加载)
```

**前端提示**：在预设编辑弹窗的 OpenCode 配置区域顶部加一行提示：

> "此配置在启动时与全局 `~/.config/opencode/opencode.json` 深度合并，仅覆盖此处指定的字段。"

### 4.6 前端改动清单

| 文件 | 改动 | 影响范围 |
|------|------|---------|
| `ProviderDetail.vue` | 重写 `target=opencode` 时的配置区域 | 弹窗内部约 400 行 |
| `ProviderDetail.vue` | 新增 `parseOpenCodeConfig()` / `serializeOpenCodeConfig()` | 新增约 150 行 TS |
| `ProviderDetail.vue` | 新增 `OpenCodeConfigView` 接口定义 | 新增约 50 行 TS |
| `ProviderDetail.vue` | 新增 agents/mcp/permissions 相关响应式状态 | 新增约 40 行 |
| `ProviderDetail.vue` | 修改 `openEditPresetDialog()` | 改动约 10 行（增加解析逻辑） |
| `ProviderDetail.vue` | 修改 `handleSavePreset()` | 改动约 10 行（增加序列化逻辑） |
| `ProviderDetail.vue` | 新增样式（可折叠区域、agent/mcp 表格） | 新增约 100 行 CSS |

**总增量**：约 750 行（含模板+脚本+样式），全部在 ProviderDetail.vue 一个文件内。

**不改的文件**：
- 后端 Go 代码（零改动）
- Dashboard.vue（零改动）
- Settings.vue（零改动）
- models.ts（Wails 自动生成，Preset struct 无变更则无新绑定）

---

## 五、风险与缓解

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| `opencode_config` JSON 结构复杂，前端解析不完整 | 中 | 高 | 保留 rawJson fallback，解析失败时回退到 textarea 模式 |
| 用户已有手动编辑的 JSON，格式不规范 | 低 | 中 | 解析失败时提示"JSON 格式无法解析，请手动修正"，不阻断编辑 |
| Agent/MCP 条目过多导致弹窗过长 | 中 | 低 | 使用可折叠分区 + 虚拟滚动表格 |
| 未来 OpenCode schema 变更导致表单字段过时 | 高 | 低 | 表单化只覆盖稳定字段，其余保留 JSON fallback，不受影响 |
| 前端序列化时丢失 JSON 字段顺序 | 低 | 低 | OpenCode 不依赖字段顺序，只关心值 |

---

## 六、验收标准

| # | 验收项 | 验证方法 |
|---|--------|---------|
| A1 | 创建新 opencode preset，不写 JSON 即可选 provider/model | ProviderDetail -> 添加预设 -> target=opencode -> 表单填写 -> 保存 |
| A2 | 编辑含 agent 配置的 preset，表单正确显示已有数据 | 打开已有 opencode preset，agents 列表显示正确 |
| A3 | 添加/删除 agent 条目后保存，JSON 保真 | 添加 agent -> 保存 -> 重新打开 -> agent 仍在，其他字段不丢失 |
| A4 | 添加/删除 MCP 服务器后保存，JSON 保真 | 同 A3 |
| A5 | 权限 checkbox 切换后保存，permission 字段正确 | 勾选/取消权限 -> 保存 -> 重新打开 -> 状态一致 |
| A6 | 高级 JSON 区域的未知字段保存不丢失 | 在 JSON fallback 中写入自定义字段 -> 保存 -> 重新打开 -> 字段仍在 |
| A7 | 启动 OpenCode 时，preset 的 opencode_config 正确合并到运行时 | 配置 preset -> Dashboard 启动 OpenCode -> 检查日志中的 OPENCODE_CONFIG_CONTENT |
| A8 | 兼容旧数据：无 target 的 preset 不受影响 | 打开已有 codex preset -> 编辑保存 -> 数据无变化 |
| A9 | 全局 opencode.json 未被修改 | 多次编辑保存 preset 后，全局文件内容不变 |

---

## 七、实现指南（给鲁班的建议）

### 7.1 推荐实施顺序

1. **先写解析/序列化函数**（`parseOpenCodeConfig` / `serializeOpenCodeConfig`）+ 单元测试
2. **替换模板**：把 `v-if="editingPresetTarget === 'opencode'"` 区域内的 JSON textarea 替换为分区表单
3. **逐个接入表单区域**：permissions（最简单）-> MCP -> Agents -> Provider 模型选项
4. **最后接入 JSON fallback**：高级 JSON textarea
5. **端到端测试**：A1-A9 验收标准逐项过

### 7.2 关键注意点

- `opencode_config` 的 `permission` 字段值是 `"allow"` 字符串，不是 boolean。前端解析时需要 `"allow" -> true`，序列化时 `true -> "allow"`，`false -> "deny"`（或不写该字段）
- Agent 的 `tools` 是 `Record<string, boolean>`，但前端展示需要把 `false` 的工具也列出来（灰色状态），因为"显式禁止"和"未配置"语义不同
- MCP 的 `type` 为 `"local"` 时用 `command` 数组，为 `"remote"` 时用 `url` 字符串，前端需要条件渲染
- 序列化时，如果 `rawJson` 是空字符串，则以表单字段构建整个 JSON 对象；如果非空，则需要将表单字段合并进去

### 7.3 可复用的组件模式

ProviderDetail.vue 已经有"可折叠区域"的模式（context-window-config）。新增的 5 个区域可以复用相同的折叠交互：

```html
<div class="config-section">
  <h3 class="section-subtitle" @click="sectionExpanded.agents = !sectionExpanded.agents">
    Agents <span class="expand-icon">{{ sectionExpanded.agents ? '-' : '+' }}</span>
  </h3>
  <div v-if="sectionExpanded.agents" class="section-body">
    <!-- agent 编辑内容 -->
  </div>
</div>
```

---

## 八、建议下一步

1. **鲁班（coder）** 按"七、实现指南"的顺序实施，预计 2-3 小时
2. 完成后 **谛听（reviewer）** 重点检查 JSON 解析/序列化的边界情况
3. 最后由主上验收 A1-A9

---

## 附录 A：用户全局 opencode.json 中各配置项的分布统计

基于用户实际的全局配置文件，统计各配置项的条目数：

| 配置项 | 条目数 | 表单化 | 说明 |
|--------|--------|--------|------|
| provider | 7 个 | 部分（仅 models） | 含 anthropic/openai/zhipuai/minimax/deepseek/bailian/github-copilot |
| agent | 13 个 | 是 | 从 baize 到 wukong 的完整 Agent 体系 |
| mcp | 4 个 | 是 | zhipu 系列 3 个 + minimax 1 个 |
| permission | 11 个键 | 是 | 全部是 "allow" |
| instructions | 1 个数组 | 否 | 15 个 md 文件路径 |
| plugin | 1 个数组 | 否 | 当前为空 |
| experimental | 2 个键 | 否 | batch_tool + mcp_timeout |
