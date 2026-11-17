# P3 报告：前端「共用」主 tab——公共 skill / 公共 MCP 管理 UI

> 任务：扩展管理页新增「共用」主 tab（CommonCenterPanel 容器 + 公共技能/公共 MCP 两个子面板 + MCP 导入对话框）。不做插件生命周期改动（P4）。
> 上游输入：p2-report.md §五、stores/sharedExt.ts、api/sharedExt.ts、ExtensionsView.vue、PluginInstalledPanel.vue（UI 模式参考）。
> 产物：ExtensionsView.vue（+8 行：tab 项/pane/import）、stores/plugin.ts（2 处类型扩展）、components/extensions/common/（4 组件 + 1 模型 + 3 composable，新目录）、__tests__/components/extensions/common/（3 测试文件 27 用例，新目录）、本报告。

## 一、组件清单与职责

| 文件 | 职责 |
|---|---|
| `common/CommonCenterPanel.vue` | 容器：Segmented（underline）子 tab「公共技能 / 公共 MCP」+ 两面板切换（数据加载各自管理） |
| `common/CommonSkillsPanel.vue` | 聚合技能主列表：manual 行（启停 Switch/targets 勾选 chips/删除）+ plugin 行（来源徽章「插件·名」+引擎标识 Badge、「查看插件」跳转、删除禁用+title「来自插件，请在插件管理中卸载」）；顶部导入 zip/导入文件夹/刷新；删除 ConfirmDialog；LoadingState/ErrorState/EmptyState |
| `common/CommonMCPPanel.vue` | 公共 MCP 列表：server 名 + config 摘要（command/url 首行）+ 启停/targets 勾选/删除确认；顶部导入 JSON/刷新；持有 MCPImportDialog（ref 打开） |
| `common/MCPImportDialog.vue` | 导入对话框：textarea 粘贴 + 「从文件导入」（pick→readTextFile 回填）→「解析预览」→ server 名+摘要预览区 →「导入」；预览错误就地红字展示 |
| `common/commonCenterModel.ts` | 纯决策逻辑：`skillRowModel`（manual/plugin 行能力矩阵）、`nextTargets`（勾选切换，SHARED_EXT_ENGINES 固定顺序）、`mcpConfigSummary`/`mcpRowModel`、`ENGINE_LABELS`/`engineLabelOf`、`PLUGIN_DELETE_TITLE` |
| `common/useCommonSkillsPanel.ts` | 技能面板可测内核：聚合+registry 加载、导入/启停/targets/删除流、viewPlugin 跳转、toast |
| `common/useCommonMCPPanel.ts` | MCP 面板可测内核：清单加载、启停/targets/删除流、toast |
| `common/useMCPImportDialog.ts` | 导入对话框状态机：open/text/parsed/previewError/canParse/canImport、text 变更即时作废旧预览（sync watch）、handlePickFile/handleParse/handleImport |

组件=纯模板绑定，全部交互逻辑在 composable（仓库 vitest 为 node 环境无 DOM，见 vitest.config.ts 注释；composable/纯函数在 node 下可直接测——同 useDefaultEngineSettings/quotaModel 模式）。

## 二、交互流说明

- **技能导入**：`pickSkillZip()/pickSkillDir()`（api 层，P2 §五架构；任务书写「store pick」实为 api 面）→ 非空路径即 `importSkillFromZip/Dir`（系统选择对话框即确认步骤）→ 成功 toast「已导入 N 个技能」+ `loadAggregated()`（registry 写路径已内建失效服务端 30s TTL 缓存，无需 force）；取消（空串）不动作。busy 态按动作隔离防重复提交。
- **targets 勾选**：chips 点击 → `nextTargets`（canonical 顺序）→ `setSkillTargets` 即时保存。**非乐观更新**：失败时 store.skills 未变，勾选态随 computed 自动回滚 + toast「保存分发目标失败」。
- **启停**：`setSkillEnabled` + toast；成功后 `loadAggregated()` 同步聚合视图。
- **删除**：manual 行 → ConfirmDialog（danger）→ `deleteSkill` → toast + 聚合刷新。plugin 行删除按钮 disabled + title 提示，`requestDelete` 对非 managed 行直接拒绝（模板外的第二道防线）。
- **「查看插件」跳转**：`setExtMainTab('plugins')` → `setPluginEngine(pluginEngine)` → `setActivePlugin(pluginId)`（顺序敏感：setPluginEngine 会清空 activePluginId）。activePluginId 仅 Claude/Codex 列表消费（store computed），opencode/pi 引擎完成 tab+engine 切换即视为达标；未知引擎不跳转。
- **MCP 导入**：粘贴或选文件填 text →「解析预览」`previewMCPJSON`（纯解析不落库；非法 JSON/空映射错误**就地**红字展示，previewError 与全局 store.error 分离）→「导入」`importMCPServers(parsed)`：成功 toast「已导入 N 个 MCP server」；skipped 非空（部分成功不抛错）追加 warn toast「已跳过同名: a、b」；registry 写入失败 error toast 且对话框保留。text 任何变更即时清空 parsed（sync watch），杜绝「改文本后导入旧解析结果」。
- **首屏加载**：技能面板顺序 `loadSkills()` → `loadAggregated()`（store.error 为两 load 共享单槽且成功方清空，并发会互相抹错；顺序捕获后聚合错误优先、registry 失败降级空 targets，详见 useCommonSkillsPanel.refresh 注释）。initialLoading 只做首屏门（store.loading 是跨 action 合并 busy，会随每次变更闪动，仅用于按钮禁用）。

## 三、测试证据（真实运行，2026-11-19，node vitest）

```
$ npx vitest run src/__tests__/components/extensions/common/
Test Files  3 passed (3)    Tests  27 passed (27)

$ npm --prefix frontend run test     # （脚本已是 "vitest run"，任务书命令 "-- run" 会把 run 当过滤名）
Test Files  39 passed (39)   Tests  369 passed (369)

$ npm --prefix frontend run build    # vue-tsc --noEmit && vite build
✓ built in 864ms（exit 0）
```

新增 27 用例覆盖（任务书最低要求全中）：
- `commonCenterModel.test.ts`（14）：manual 行全权/plugin 行只读（删除禁用+title/无勾选器/徽章「插件·名」+引擎标识/key 前缀防撞）、targets 非法值过滤+canonical 顺序、nextTargets 增删/不改入参、mcpConfigSummary command优先/url/空容错、mcpRowModel、engineLabelOf。
- `useCommonSkillsPanel.test.ts`（8）：首屏聚合 manual+plugin 渲染决策（含 registry targets 回填）、首屏失败错误态、targets 勾选触发 `setSkillTargets`（顺序断言）+失败回滚 toast、zip 导入流（取消不导入）、文件夹导入失败不刷新、删除确认流（plugin 行拒绝受理）、启停刷新、viewPlugin 三段跳转+未知引擎防御。
- `useCommonMCPPanel.test.ts`（5+5）：MCP 清单行摘要/启停/目标差集保存/删除确认（失败保留对话框）；导入对话框预览→导入全流（空映射/非法 JSON 就地报错、text 变更作废旧预览、skipped warn toast「已跳过同名: dup」、导入失败对话框保留+busy 复位、文件读取回填+取消）。

## 四、给 P4 的联动挂点提示

1. **插件更新/卸载后**：调 `invalidateSharedExtCache()`（api 层已导出）+ 各面板 `loadAggregated(true)`；P3 的技能面板无自动失效（30s TTL 兜底），P4 在五引擎 update/uninstall 成功路径挂上即可。
2. **「查看插件」选中态增强**：`setActivePlugin(pluginId)` 目前仅 Claude（`p.id`=插件名）与 Codex（`pluginId`）列表消费；opencode 的 PluginID=Spec、pi 的=source，两引擎的 activePlugin 详情链路是 P4 范围（P2 §二已注明）。跳转时插件列表若未加载，选中态无落点——P4 可在 ExtensionsView 切 engine 时按 activePluginId 触发 load。
3. **plugin 行 enabled 展示**：目前只有 claude 聚合带回 subitem 禁用态（「已停用」标记）；codex/opencode/pi 的 SubItemsDisabled 为空（P2 §二），P4 补齐后无需改 P3 模板（行模型自动跟随）。
4. omp 引擎暂无 provider（P2 §二），聚合不会出现 omp 插件行；P4 补 provider 后 UI 零改动。

## 五、偏差与边界记录

- **skill 导入无二次确认框**：系统文件选择对话框即确认（任务书「确认后 import」按此理解）；如需导入前预览清单，属 P4+ 增强。
- **skipped toast 用 showWarn**（部分成功的警示色），未用任务书示例三色——语义更贴切，useToast 原生支持。
- **stores/plugin.ts 改动**：仅 `extMainTab` ref 与 `setExtMainTab` 两处类型联合加 `'common'`（允许清单内最小改动，跳转必需）。
- **stores/sharedExt.ts 未改**（不在允许清单）：其共享单槽 error 的并发清除问题在 composable 层顺序捕获解决（见 §二）。
- **`agent-outputs/workflow/shared-extensions-center/workflow.md` 在 git status 中有未提交改动，非本任务产生**（Leader/并行会话现场），未触碰。
- 测试文件路径 `__tests__/components/extensions/common/` 镜像 src 结构，符合 docs/developer/testing.md「测试文件位置与命名规范」。
- 回滚方式：`git checkout -- frontend/src/views/ExtensionsView.vue frontend/src/stores/plugin.ts && rm -rf frontend/src/components/extensions/common frontend/src/__tests__/components/extensions`（无其他文件引用新增面；ExtensionsView 对 CommonCenterPanel 的 import 随 checkout 一并回退）。
