# P2 报告：sharedext 接线——Wails 绑定 + App 门面 + 前端 api/store

> 任务：把 P1 的 internal/sharedext 接入 Wails 绑定面并建前端基础层（api + Pinia store）。不做页面 UI（P3）、不做插件生命周期改动（P4）。
> 上游输入：p1-report.md §一§六（API 清单与接线注意）、bind_list.go、app.go（区域阅读）、frontend/src/api/piPlugin.ts、frontend/src/stores/plugin.ts。
> 产物：app.go（+388 行，尾部新 section）、bind_list.go（+5）、frontend/src/api/sharedExt.ts（新）、frontend/src/stores/sharedExt.ts（新）、app_sharedext_test.go（新）、frontend/src/__tests__/api/sharedExt.test.ts + stores/sharedExt.test.ts（新）、frontend/wailsjs/（wails generate module 生成产物）。

## 一、绑定方法清单

### App 级门面（wailsjs/go/main/App，集中在 app.go 尾部 `// ===== Shared Extensions (共用中心) =====` 段）

| 方法 | 签名要点 | 语义 |
|---|---|---|
| GetSharedSkills | `() []SharedSkill`（无 error，任务契约） | List 透传；失败记日志返回空 |
| ImportSharedSkillFromZip | `(path) ([]SharedSkill, error)` | 同名跳过聚合进 error 文本（部分成功） |
| ImportSharedSkillFromDir | `(path) ([]SharedSkill, error)` | 自身含 SKILL.md=单个，否则一层子目录 |
| DeleteSharedSkill / SetSharedSkillEnabled / SetSharedSkillTargets | `(name[, enabled/targets])` | 删/启停/目标透传 |
| GetSharedMCPServers | `() []SharedMCPServer`（无 error） | List 透传；失败记日志返回空 |
| PreviewSharedMCPJSON | `(text) ([]SharedMCPServer, error)` | 包级纯函数 `sharedext.ParseMCPJSON`（预览不落库） |
| **ImportSharedMCPServers** | `(servers) (ImportSharedMCPResult, error)` | 单值信封 `{Imported, Skipped}`——Wails v2 不支持 (value,value,error) 三返回值，服务层签名折叠为结构体 |
| DeleteSharedMCPServer / SetSharedMCPServerEnabled / SetSharedMCPServerTargets | `(name[, enabled/targets])` | 透传 |
| GetAllSharedSkillsAggregated | `(force bool) ([]AggregatedSkill, error)` | 组装 providers 后调 GetAggregatedSkills；InvalidateSharedExtCache 置位的强制标记在此消费 |
| InvalidateSharedExtCache | `()` | App 层 `sharedExtAggForce atomic.Bool` 置位 → 下次聚合旁路 30s TTL（P1 无公开失效方法，按任务书「App 层 force=true 语义替代」实现；P4 在插件更新/卸载处调用） |
| PickSharedSkillZipFile / PickSharedSkillDir / PickMCPJSONFile | `() (string, error)` | wailsRuntime 对话框（zip / 目录 / json 过滤器）；取消返回空串 |
| ReadTextFile | `(path) (string, error)` | 读 JSON 文本给前端预览；空路径/目录/超 1MB（`readTextFileMaxBytes = 1<<20`，stat 前置校验）报错 |

### 服务直接绑定（bind_list.go 新增 `app.sharedExt` → frontend/wailsjs/go/sharedext/Service）

ListSharedSkills / ImportSkillFromZip / ImportSkillFromDir / DeleteSharedSkill / SetSharedSkillEnabled / SetSharedSkillTargets / ListSharedMCPServers / DeleteSharedMCPServer / SetSharedMCPServerEnabled / SetSharedMCPServerTargets / GetAggregatedSkills / SetEngineBaseDir。
⚠ **生成的 `ImportMCPServers` 绑定丢弃了 skipped 返回值**（实证：`Promise<Array<SharedMCPServer>>`，Wails v2 只保留首个返回值）——前端 api 层显式不包装该方法（测试断言零调用），导入统一走 App.ImportSharedMCPServers 信封。

### 双面说明（决策记录）

任务书同时要求「App 级门面方法（全部列出）」与「bind_list 增加绑定 + frontend/wailsjs/go/sharedext/ 生成」，两面均已落地。前端 api 按仓库既有双面惯例取用（同 piPlugin.ts：服务自有操作走 Service 绑定、需 App 上下文的走 main/App）：简单 CRUD → Service 绑定；对话框/聚合/导入信封/失效挂点/预览 → App 面。App 面 CRUD 门面按任务书保留（未来若需统一 gating/审计可切换，无需再动后端）。

## 二、provider 组装现状（buildSharedSkillProviders）

| 引擎 | skills 来源 | SubItemsDisabled | 备注 |
|---|---|---|---|
| claude | `Plugins.GetInstalledPlugins` + 逐插件 `GetPluginDetail`（本地文件扫描；单插件失败跳过记日志） | ✅ detail.SubItems 已合并 plugin-subitems.json 用户级禁用，映射为 `"skill/<name>"` 键（与 plugin 包 `SubItemRef.Key()` 的 `skill:name` 格式做了转换） | 一次扫描同取 skills + 禁用态 |
| codex | `CodexPlugins.ListPlugins("")`（CLI 优先、config.toml 回退）+ `GetPluginDetails(PluginSelector{PluginID})` | ❌ 空（服务层子项禁用为兼容占位，未支持） | N 插件 = N+1 次 CLI 调用，30s TTL 缓解；CLI 失败整引擎降级为空 |
| opencode | `ListInstalledPlugins` + `GetPluginDetails(spec)`（纯本地） | ❌ 空（P4） | ResourceInfo 无描述字段，PluginSkill.Description=""；PluginID=Plugin.ID（=Spec，P3 跳详情可直接用） |
| pi | `ListInstalledPackages` + `GetPackageDetails(source)`（纯本地） | ❌ 空（P4） | 取 Resources 中 `Type=="skill"` 条目（piplugin 类型常量包私有，用字面量并注释） |
| omp | ❌ 暂不提供 provider | ❌ | ompplugin 无 skills 扫描能力（grep 全包无 skill 面），按任务书 P4 补 |

映射逻辑拆为 4 个纯函数（claudePluginSkillsProvider 等），Go 单测直接覆盖（见 §四）。

## 三、生成绑定 diff 概览

`wails generate module`（v2.11.0）真实执行，产物变化：
- **新增** `frontend/wailsjs/go/sharedext/`（Service.d.ts/.js，13 方法）；
- `main/App.d.ts`/`App.js`：新增本任务 17 个门面方法；**同时刷新了 HEAD 上已陈旧的绑定**（PiPluginTargeted 系列从 `Promise<any>` 变为强类型、ImportWSLCliConfigKeys 等 reorder、GetDrvFSAdvisories 等）——这些是既有后端方法在 HEAD 未重新生成的欠账，非本任务手改；
- `models.ts`：新增 `namespace sharedext`（SharedSkill/SharedMCPServer/AggregatedSkill/PluginSkill/PluginSkillsProvider）、`main.ImportSharedMCPResult`、`namespace wslsupply` 等陈旧欠账类型；大量 WSL 类为 reorder 噪声；
- `piplugin/Service.*`：补齐 HEAD 欠账的 ResolvedEntityCommit/ResolvedEntityVersion；
- `runtime/` 三文件仅 mode 100644→100755（wails chmod 副产物）。
- 已知生成噪声（既有）：`Not found: time.Time`、`router.Interface` reserved keyword 提示，均不影响产物（vue-tsc 通过）。
- ⚠ 生成器未为具名字符串类型产出 TS 导出（`sharedext.Engine` 在 .d.ts 被引用但 models 无定义，skipLibCheck 下既有 quirk，`plugin.PluginType` 同款）→ 前端 api 层本地定义 `SharedExtEngine` 联合类型。

## 四、测试输出摘要（真实运行，2026-11-18，Go 1.26.1 darwin/arm64）

```
$ go vet ./... && go build ./...
（均 exit 0；仅 internal/secrets 既有 cgo deprecation warning，与本任务无关）

$ go test ./internal/sharedext/ . -count=1
ok  amagi-codebox/internal/sharedext  0.868s
ok  amagi-codebox                     12.189s

$ go test ./... -count=1
RC=0（全部 ok；仅上述既有 cgo warning）

$ go test . -run 'SkillsProvider|ImportSharedMCPServersEnvelope|GetSharedSkillsEmpty|ReadTextFileLimits' -count=1 -v
7/7 PASS（四引擎 provider 纯函数映射 / MCP 导入信封含同名 skipped / 空清单 / ReadTextFile 空路径·目录·超 1MB）

$ npx vitest run（frontend，36 文件）
Test Files 36 passed (36)   Tests 342 passed (342)
  其中新增：api/sharedExt.test.ts（6 用例：双绑定路由、null→[] 归一、
  ImportMCPServers 禁走 Service 面断言、信封缺省、force 透传、引擎清单）
  stores/sharedExt.test.ts（9 用例：加载填充/error 不抛、变更刷新、
  导入部分成功不抛错、空导入不刷新、previewMCPJSON 不污染全局 error、loading 合并态）

$ npm --prefix frontend run build
✓ vue-tsc --noEmit 通过，vite build 成功

$ golangci-lint run .  （附带检查）
2 issues：ineffassign ×2（app_wsl_supply.go:1508 / launch_planner.go:842，均为本任务未触碰文件的既有欠账，非本任务引入；CI 基线 go vet 已零告警）
```

## 五、给 P3 的 store API 摘要

`useSharedExtStore`（defineStore('sharedExt')，setup 风格）：

- **state**：`skills: SharedSkill[]`（含 missing 标记→「清理失效条目」入口 deleteSkill）、`mcpServers: SharedMCPServer[]`、`aggregated: AggregatedSkill[]`（origin: 'manual'|'plugin'；plugin 条目 Managed=false 只读、PluginEngine/PluginID/PluginName 供跳转插件详情）、`loading`（跨 load 合并 busy，内部 ref 计数）、`error: string`。
- **加载**：`loadSkills()` / `loadMCPServers()` / `loadAggregated(force=false)`——失败写 error 不抛错（UI 读 error 渲染错误态）。
- **skill**：`importSkillFromZip(path)→SharedSkill[]`、`importSkillFromDir(path)`、`deleteSkill(name)`、`setSkillEnabled(name, enabled)`、`setSkillTargets(name, targets)`——成功后自动刷新 skills；失败写 error 并抛出（同名跳过明细在后端 error 消息文本里）。
- **MCP**：`previewMCPJSON(text)→SharedMCPServer[]`（纯透传不落库，非法 JSON 抛后端中文报错、不污染全局 error——就地展示）；`importMCPServers(servers)→{imported, skipped}`（**skipped 非空=部分成功不抛错**，UI 用结果提示；imported>0 才刷新清单）；`deleteMCPServer` / `setMCPServerEnabled` / `setMCPServerTargets` 同 skill 约定。
- **目标引擎**：`type SharedExtEngine = 'claude'|'codex'|'opencode'|'pi'|'omp'`；`SHARED_EXT_ENGINES` 常量（固定顺序）供勾选 UI。
- **文件入口**（api 层）：`pickSkillZip()/pickSkillDir()/pickMCPJsonFile()`（取消返回空串）、`readTextFile(path)`（≤1MB）——「选 MCP JSON 文件 → readTextFile → previewMCPJSON → 勾选 → importMCPServers」链路已通。
- **刷新语义**：手动 skill/MCP 变更后无需手动刷聚合（registry 写路径内建失效 sharedext 30s TTL）；刷新按钮 `loadAggregated(true)`；插件侧变化 P4 挂 `invalidateSharedExtCache()`。

## 六、与任务书的偏差记录（均为最小决策，理由如下）

1. **targets 用 `[]sharedext.Engine` 而非任务书字面 `[]string`**：Service 绑定原生即 `[]Engine`（wire 同为 string[]），App 面同型避免两面签名分叉；非法值由服务层校验报错。
2. **`ParseMCPJSON` 是包级纯函数而非 Service 方法**（P1 事实）：App.PreviewSharedMCPJSON 包 `sharedext.ParseMCPJSON`，Service 绑定面无此方法（生成产物证实）。
3. **InvalidateSharedExtCache 以 App 层 force 标志实现**：P1 未导出失效方法，采用任务书预判的「App 层 force=true 语义替代」，internal/sharedext 零改动（任务边界「不改 internal/sharedext 源码」完整遵守）。
4. **frontend/wailsjs 除 sharedext 外的 diff 均为 HEAD 欠账刷新**（§三），未手改任何生成文件。
5. GetSharedSkills/GetSharedMCPServers 按任务书签名无 error 返回——失败路径为记日志 + 空列表（注释已说明取舍）。

## 七、遗留 / 给 P4 的挂点

- `InvalidateSharedExtCache()` 已挂 App 方法，P4 需在五引擎插件 update/uninstall 成功路径调用（codex/opencode/pi/omp 的子项禁用读取与 omp provider 也归 P4）。
- sharedext/Service.ImportMCPServers 生成绑定弃用警告已写进 api 层注释与测试；若未来想根治，可在 wails.json bindings config 排除该方法或给服务层加信封（超出本任务边界）。
- 回滚方式：`git checkout -- app.go bind_list.go frontend/wailsjs && rm app_sharedext_test.go frontend/src/api/sharedExt.ts frontend/src/stores/sharedExt.ts frontend/src/__tests__/api/sharedExt.test.ts frontend/src/__tests__/stores/sharedExt.test.ts`（无其他包引用本任务新增面；wailsjs 欠账刷新部分随 checkout 一并回退，属可接受副作用）。
