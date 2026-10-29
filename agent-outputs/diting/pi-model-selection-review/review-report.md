# Pi 模型选择修复 quick 独立审核

VERDICT: PASS（限本轮指定 diff；无新增 finding）

## 范围与基线
- 基线 HEAD：efca673ce3b9dbb4737bce0d8aa6269e7d420ba8。
- 上游输入：任务描述及工作区 diff；无另附上游 artifact。
- 审核：internal/piconfig/service.go、builtin_catalog_test.go、新 catalog_overrides_test.go、ModelSpecSelector.vue、useModelCatalog.ts、对应单测及 e2e/pi-amagi-config.{html,spec.ts}。
- 共享树只读，未修代码、未提交；只生成本目录报告和 Playwright 证据。

## 核验结果
1. **同名 provider 缓存保留：通过。** service.go:383–435 将缓存与自定义 provider 合并，再按 id 合并模型；仅 modelOverrides、仅 baseUrl/headers、空 models 不再排除整个缓存 provider。新 Go fixture 精确构造 anthropic/openai-codex 的 overrides-only 情形，检查模型数量、认证标识和未知 override 不造模型。
2. **Pi 原生语义：通过（目录消费字段）。** 本机安装源码 `/Users/run.mao/.local/share/fnm/node-versions/v24.21.0/installation/lib/node_modules/@earendil-works/pi-coding-agent/dist/core/provider-composer.js`：150–188 的 applyModelsJson 按 id 替换/追加 models；354–367 在模型 upsert 后应用 modelOverrides；54–85 以非空覆盖普通字段、thinkingLevelMap 按键展开（保留 null）。本轮 mergeCatalogModels 对目录消费的 name/reasoning/contextWindow/thinkingLevelMap 遵循该顺序；不保留被替换模型的旧 reasoning/context/map。Pi 自带默认 contextWindow 等完整运行时归一化不在本次目录修复中，未将其声称为完整运行时模型镜像。
3. **provider/ 可恢复：通过。** ModelSpecSelector.vue:79 仅选择器显式 allowEmptyModel；空目录仍展示 provider 下拉、禁用模型/档位，并可切回有模型 provider 或手动输入。Chrome E2E 的三个新增测试全部通过，含从 anthropic/ 恢复、切认证 provider、空目录切换、手动未知 provider 和已知 provider 未收录模型。
4. **严格保存及兼容：通过。** useModelCatalog.ts 默认参数仍拒绝 p/、p/:high；piConcurrency.ts:189、piCompaction.ts:44、piAdvisor.ts:29 均未启用宽松参数。单测拒绝不完整 spec，接受动态完整 spec 和模型内斜杠/冒号；现有 E2E 不完整保存被阻断用例通过。未知完整 spec 不被目录强制清除。
5. **原 bug 的验证有效性：通过。** 后端 fixture 覆盖“同名 provider 只有 modelOverrides，缓存含 models”，前端 E2E 覆盖 provider/ 中间态和空目录切换，两条原失败链均有对应断言。E2E 使用显式合成目录，不冒充真实 OAuth/后端联调。

## 独立执行证据
cwd 均为 `/Users/run.mao/workpace/amagi-codebox`。
- `go test ./internal/piconfig -count=1`：exit 0，包通过（0.463s）。
- `npm --prefix frontend run test -- src/__tests__/components/provider/useModelCatalog.test.ts src/__tests__/components/provider/piConcurrency.test.ts src/__tests__/components/provider/piCompaction.test.ts src/__tests__/components/provider/piAdvisor.test.ts`：exit 0，实际匹配 3 文件、23 测试全部通过；并非四文件均存在/均执行。
- `git diff --check`：exit 0。
- `npx playwright test --config e2e/pi-amagi-config.playwright.config.ts --output agent-outputs/diting/pi-model-selection-review/playwright-results`：exit 1，缺 Playwright chromium_headless_shell，全部在启动阶段失败，不是产品断言证据。
- 确认系统 Chrome 可执行文件已安装后，通过配置本身提供的通道选择运行：`PI_CONFIG_BROWSER_CHANNEL=chrome npx playwright test --config e2e/pi-amagi-config.playwright.config.ts --output agent-outputs/diting/pi-model-selection-review/playwright-chrome-results`：exit 1，11 通过/1 失败。新增三个用例全部通过。

## 既有观察（不计本轮 finding）
- e2e/pi-amagi-config.spec.ts:238 的 Advisor 测试查找 `Codex 桌面聊天` 超时；实际 PiAdvisorConfig.vue:65 为 `Codex Work 咨询`。`git show HEAD:...` 证实基线双方即存在该不一致（基线 spec:183）。与本轮修改无因果关系，不据此否定本轮修复；建议实施者另行修复旧断言。
- 失败证据：`playwright-chrome-results/pi-amagi-config-Advisor-sw-820a2--and-rejects-invalid-budget/error-context.md` 及同目录截图。截图未作视觉评审。

## 盲区与可信度
- quick，仅审核指定 diff 及保存/原生合并直接消费链，未做全仓安全/性能审计。
- 未读取真实 auth.json、apiKey 或用户模型文件；17/7 模型事实来自任务输入，未独立重读真实配置。仅读取本机安装程序源码及合成测试数据。
- 未启动 Wails 桌面或真实 OAuth API 联调；E2E 为实际 Vue 组件配合合成 Wails bridge，后端真实 Go 入口另由 fixture 测试覆盖。
- 共享树非隔离快照；起止指定 diff 统计一致，但不等于验证未来提交。
- 报告回滚：本轮无项目代码修改；只需由 Leader 按归档策略处理本报告目录，不回滚实施者代码。

VERDICT: PASS
