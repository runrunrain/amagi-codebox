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
