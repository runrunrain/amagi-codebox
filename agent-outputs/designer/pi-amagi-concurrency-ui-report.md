# Pi 配置（amagi-pi）并发限制（concurrency）可视化编辑器适配报告

## 1. 任务背景与配置契约

根据 amagi-pi 插件新增的按 provider/model 分池并发限制配置契约，amagi-codebox 在 Provider Center 的 Pi 配置（`amagi-pi`）编辑器中增加可视化配置与校验支持。

### amagi.json 配置契约（冻结版本）
```json
"concurrency": {
  "default": 4,
  "providers": { "<providerName>": 8 },
  "models": { "<provider/model>": 3 }
}
```
- `default`：未单独匹配 provider 或 model 时的默认并发池大小（正整数，可选）。
- `providers`：按服务商名覆盖的并发限制表（可选）。
- `models`：按精确 `provider/model` 覆盖的并发限制表（优先级高于 `providers`，可选）。
- 三键全为空/删除时，写回 `amagi.json` 自动剔除 `concurrency` 键，不留空对象。

---

## 2. 修改文件清单

| 文件路径 | 变更类型 | 说明 |
|---|---|---|
| `frontend/src/components/provider/PiAmagiConfig.vue` | 修改 | 新增「并发限制（concurrency）」折叠卡片（默认收起）、`default` 正整数输入、`providers` 键值表编辑与智能建议、`models` 键值表编辑与智能建议（提取 agent 模型并去除 `:thinking` 后缀）、`cleanupConcurrency` 空对象清理逻辑与徽标计数。 |
| `frontend/src/components/provider/icons.ts` | 修改 | 扩展图标库与配色表，新增 `concurrency` 类别专属 SVG 图标与 HIG 语义强调色（`#32ADE6` systemCyan）。 |
| `internal/piconfig/amagi_config_test.go` | 新增 | 后端 Go 回归测试：覆盖 `amagi.json` 读写、默认骨架缺失回退、包含 `concurrency` 完整结构往返持久化验证、0600 私有文件权限验证与非法 JSON 校验。 |
| `frontend/src/__tests__/components/provider/useModelCatalog.test.ts` | 新增 | 前端 Vitest 单元测试：覆盖模型 spec 解析、thinking 级别去除与拼装。 |

---

## 3. 核心实现要点

1. **可视化卡片与交互**：
   - 纳入 `expanded.concurrency` 控制，默认折叠。
   - 徽标 `badge` 动态计算配置项总数（`default` 有效值 + `providers` 项数 + `models` 项数）。
   - 复用现有 `TextInput`、`AppButton`、`ConfigCategoryCard` 组件风格与设计变量。

2. **智能建议提取**：
   - `providers` 建议：从模型目录（`catalog.providers`）与已配置角色模型（提取 `/` 前缀）自动提取未配置的服务商候选项。
   - `models` 建议：从已配置角色的 `model` 自动去除 `:thinkingLevel`（如 `anthropic/claude-3-7-sonnet:high` → `anthropic/claude-3-7-sonnet`），辅以模型目录候选项。

3. **数据同步与清除契约**：
   - 直接修改 `configData.value.concurrency`。
   - `cleanupConcurrency()` 检查并在全空时调用 `delete configData.value.concurrency`，确保序列化时不残留空键。

---

## 4. 验证命令与测试结果

### 4.1 Go 后端测试
- 命令：`go test -count=1 -v ./internal/piconfig/...`
- 结果：全部通过
```
=== RUN   TestAmagiConfigRoundTrip
--- PASS: TestAmagiConfigRoundTrip (0.00s)
=== RUN   TestAuthConfigRoundTrip
--- PASS: TestAuthConfigRoundTrip (0.00s)
=== RUN   TestBuiltinCatalogMerge
--- PASS: TestBuiltinCatalogMerge (0.00s)
=== RUN   TestModelsConfigRoundTrip
--- PASS: TestModelsConfigRoundTrip (0.00s)
PASS
ok  	amagi-codebox/internal/piconfig	0.427s
```

- 命令：`go vet ./...`
- 结果：通过（无输出）

### 4.2 前端测试与构建
- 命令：`npm --prefix frontend run test`
- 结果：2 test files, 10 tests passed (0 failures)
```
 Test Files  2 passed (2)
      Tests  10 passed (10)
   Duration  108ms
```

- 命令：`npm --prefix frontend run build` (`vue-tsc --noEmit && vite build`)
- 结果：类型检查通过，打包成功
```
✓ built in 494ms
```

---

## 5. 遗留问题与风险
- 无遗留问题。与 amagi-pi 并行开发保持完全隔离，未修改 amagi-pi 仓库内任何文件。

---

## 6. 返修（实测问题根因、方案、下拉选型理由与验证证据）

### 6.1 问题根因剖析

1. **问题 1（修改数值后保存数值不变，P0）**：
   - **根因**：`updateConcurrencyProviderLimit`、`updateConcurrencyModelLimit` 及 `updateConcurrencyDefault` 中直接调用 `val.trim()`。在 `TextInput`（`type="number"`）或部分交互触发场景下，传递的 `val` 为 `number`（或 `null`/`undefined`），调用 `.trim()` 直接抛出 `TypeError: val.trim is not a function`，导致更新链中断，数值无法写回 `configData` 与 `jsonContent`。

2. **问题 2（退格清空输入框导致整行被删、再点建议回退旧值，P0）**：
   - **根因**：原实现在每次 `@update:model-value` 输入事件中均同步调用 `cleanEmptyLimits()` 与 `cleanupConcurrency()`。当用户退格清空输入框准备输入新值时，空串 `""` 立即被识别为空并 `delete` 掉了对象里的键，使得 `v-for` 绑定的列表项瞬间在 DOM 中被销毁。若用户再去点击底部的建议按钮，重新调用 `addConcurrency*` 则以初始默认值（4 或 2）重新插入，造成值回退和极差的输入体验。

3. **问题 3（需要已有配置的标准下拉选择，P1）**：
   - **根因**：手输文本框搭配零散的“+ 建议项”按钮不仅视觉不够规整，且在模型和提供商较多时操作繁琐。用户需要直接从现有注册表目录与已配置 Agent 的集合中通过标准下拉进行单选，同时保留自定义自由输入能力。

---

### 6.2 修复方案与架构改进

1. **逻辑解耦与纯函数提炼（`piConcurrency.ts`）**：
   - 新建 `frontend/src/components/provider/piConcurrency.ts`，将并发配置的状态归一化、保存收口清理、下拉候选项集合提取与去重排序纯函数化。
   - **输入归一化（`normalizeLimitInput`）**：入口统一使用 `const raw = String(val ?? '').trim()`，安全兼容 `number | string | null | undefined`，中间态（空串或非数字字符）作为字符串安全保留，杜绝 `TypeError` 且不打断输入。
   - **保存收口契约（`cleanConcurrencyConfig`）**：集中封装空值剔除、正整数转换、空对象修剪及 `concurrency` 顶层删除逻辑。

2. **输入生命周期与删键时机规范**：
   - **编辑过程中**：永不删键、永不删行。用户清空输入框时，键值保留为中间态空串 `""`，DOM 行完好无损，焦点不丢失。
   - **删键收口两处触发**：
     1. 显式点击行尾的 `×` 按钮；
     2. 用户点击「保存配置」（`handleSave`）执行时调用 `cleanConcurrencyConfig` 统一规范化正整数并剔除无效键。
   - **稳定行标识（Row ID）**：引入 `getProviderRowId` / `getModelRowId`，为 `v-for` 提供跨重命名的稳定唯一 ID，彻底杜绝 Vue 在 key 变更时销毁重建 DOM 导致的失焦问题。

3. **标准下拉组件选型与自定义兼顾**：
   - **下拉组件选型理由**：
     - **视觉与规范一致性**：直接复用组件库标准 `frontend/src/components/ui/Dropdown.vue`（与 `PiAmagiConfig` 中的 `profile` 策略下拉及 `ModelSpecSelector` 完全同款），遵循 Apple HIG / Amagi 设计语言。
     - **滚动与浮层防护**：`Dropdown.vue` 内部采用 `Teleport to="body"` + `position: fixed`，并配置 `max-height: 320px` 与 `overflow-y: auto` 滚动容器，彻底摆脱外层卡片与主页面的 overflow 裁剪，在长列表中滚动顺畅。
     - **键盘可访问与外部点击收起**：原生支持 `Esc` 收起、点击菜单外部遮罩关闭与选择后自动聚焦。
   - **选项集合构建契约**：
     - **Providers 下拉**：`catalog.providers` ∪ `agents` 角色模型提供商（提取 `/` 前缀）∪ 已配置 `concurrency.providers` 键，过滤去重并按 `localeCompare` 升序稳定排序，末尾附加 `＋ 自定义服务商...`。
     - **Models 下拉**：`catalog.providers` 全量 `${p.name}/${m.id}` ∪ `agents` 角色模型 spec（剥离 `:thinkingLevel`，须含 `/`）∪ 已配置 `concurrency.models` 键，过滤去重并稳定排序，末尾附加 `＋ 自定义模型...`。
   - **双模平滑切换**：
     - 预设模式下展示 `Dropdown`；
     - 选择 `＋ 自定义...` 或处于自定义状态时，无缝切换为带「从列表选择」按钮的 `TextInput`；
     - 点击「从列表选择」随时切回 `Dropdown`，兼顾标准选择的高效与自定义填写的自由度。

---

### 6.3 验证证据与测试覆盖

1. **三项实测场景逐条验证**：
   - **场景 ①（改数值→保存→重新加载回读）**：
     - 修改 `concurrency.default`、`concurrency.providers.openrouter` 或 `concurrency.models` 数值；
     - 点击「保存配置」，保存成功提示触发；
     - 切换到 JSON 模式及重新加载回读，修改后的正整数值均精确保留。
   - **场景 ②（退格清空数值→行仍在→输入新值→保存生效）**：
     - 在数值输入框中连续退格直至内容为空；
     - 对应行在界面上持续稳定显示，DOM 结构不销毁、不跳动；
     - 输入新正整数（如从空输入 `16`），点击保存后成功落盘为正整数 `16`。
   - **场景 ③（下拉列出已有提供商且去重正确、选择后写回正确）**：
     - 下拉列表中正确聚合目录 provider、agent 使用的 provider 及已配置键，无重复项且稳定排序；
     - 选择下拉项即可切换目标 provider/model；切换自定义模式后可自由输入并在保存时正确序列化。

2. **前端单元测试与构建检查**：
   - 新增 `frontend/src/__tests__/components/provider/piConcurrency.test.ts` 覆盖：
     - 输入类型归一化（数字、字符串、空格、非法非正整数中间态）；
     - 保存收口清理契约（正整数转换、空值剔除、空对象裁剪、顶层 `concurrency` 剔除）；
     - `buildProviderDropdownOptions` 与 `buildModelDropdownOptions` 集合并集、去重、斜杠过滤、排序与自定义项测试。
   - 运行测试：`npm --prefix frontend run test`（3 个测试文件，20 个用例全绿通过）。
   - 运行构建：`npm --prefix frontend run build`（`vue-tsc --noEmit && vite build` 0 错误通过）。

3. **Go 后端测试与静态检查**：
   - `go test ./...` 与 `go vet ./...` 全量通过，确保桌面全链路契约完好。

