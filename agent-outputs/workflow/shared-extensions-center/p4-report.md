# P4 报告：插件生命周期补齐——单插件更新 / 包启停 / skill 级启停 / 聚合联动

> 任务：Codex 单插件更新；Pi 包启停 + skill 级过滤启停；OMP skills 扫描 + provider 补齐；OpenCode skill 级启停能力对齐；codex/opencode/pi SubItemsDisabled 聚合填充；更新/卸载联动刷新。
> 上游输入：p2-report.md（§二 provider 现状/§七挂点）、p3-report.md（§四联动挂点）、internal/{codexplugin,piplugin,ompplugin,opencodeplugin}/service.go。
> 产物：internal/codexplugin/service.go（UpdatePlugin+OnChange）、internal/piplugin/filters.go（新，过滤语义引擎+启停 API）、internal/piplugin/{service,types}.go、internal/ompplugin/inspect.go（新，skills 扫描+详情）、internal/ompplugin/service.go、internal/opencodeplugin/service.go、app.go（联动接线+omp provider+禁用映射）、frontend api×2/store×2/面板×2、wailsjs 生成物、四包+App 测试、本报告。

## 一、四引擎能力调研结论表（先探测后实现）

| 引擎 | 版本实测 | 单插件更新 | 插件级启停 | skill 级启停 | 结论形态 |
|---|---|---|---|---|---|
| codex | codex-cli **0.169.0-alpha.11.1**（ChatGPT.app 内嵌 `/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex`；独立 codex 不在 PATH） | ❌ 原生（`codex plugin --help`：仅 add/list/marketplace/remove）→ **模拟**：市场刷新+卸载重装+状态恢复 | ✅ 原生（config.toml `[plugins]` enabled，既有 setPluginEnabled） | ❌ 无机制（config 仅插件级 enabled；SetPluginSubItemEnabled 为兼容占位 no-op——**不做假开关**） | 模拟更新 / 原生启停 / 降级无子项 |
| pi | pi **1.5.2**（bundle 源码级探测） | ✅ 原生 `pi update <source>`（既有） | ❌ 无包级 disable 概念（settings schema 无 disabled 字段；`pi config` TUI 仅资源级）→ **模拟**：pi 原生过滤模式 `!**`×4 全排除标记 | ✅ **原生**：packages 条目 skills[] 过滤数组支持 `+path`（强制含）/`-path`（精确排除）/`!glob`/白名单 四形态；`pi config` TUI 的 togglePackageResource 即写 `±relative/path` | 模拟包级 / 原生 skill 级 |
| opencode | opencode **1.19.3** | ✅ 既有 UpdatePlugin（ls-remote→--force 重装+假失败兜底） | ❌（plugin 数组仅成员语义；1.19.3 无 enable 子命令） | ❌ 无子项概念（在线 schema 实测：新 `plugins` 键的 `-id` 指令作用于**插件 ID**（如 `opencode.provider.*`）而非 skill；旧 `plugin` 键为 [string, options] 无 enable 字段）→ **降级**：包级语义，子项不造假开关 | 降级（如实标注） |
| omp | omp **18.3.0**（homebrew 二进制，strings 探测） | ✅ 原生 `omp plugin upgrade`/`install --force`（既有 UpgradePlugin） | ✅ 原生 `omp plugin enable/disable`（既有） | ❌ features（`--enable f1,f2`）是**包可选特性**而非 skill；无子项机制 → 降级 | 原生插件级 / 降级无子项 |

**pi 过滤语义证据**（bundle `chunks/chunk-VEJDBY3O.js`，决定任务 2 实现）：
- `applyPatterns`：plain=白名单（minimatch glob）→ `!p` glob 排除 → `+p` 精确强制含 → `-p` 精确强制排除；
- `togglePackageResource`（TUI 开关算法）：剥前缀去重同目标条目 → push `+rel`/`-rel` → 数组空删键 → 四键全空条目折叠回字符串；
- 匹配基准：rel 路径/文件名；SKILL.md 额外认父目录 rel/父目录名（`-skills/foo` 等价 `-skills/foo/SKILL.md`）；
- **实测互认**（真 pi 1.5.2 + 临时 PI_CODING_AGENT_DIR，只读）：`{"source":...,"skills":["-skills/alpha/SKILL.md"]}` 与 `!**`×4 标记两种形态 `pi list` 均正常解析并列出该包（标注 filtered），无报错。

## 二、实现清单（规格→产物映射）

### 1. Codex 单插件更新（internal/codexplugin/service.go `UpdatePlugin`）
读旧 enabled（config.toml）→ `plugin marketplace upgrade`（失败仅日志继续）→ uninstall+install 重装 → 恢复 enabled。子项状态无需恢复（无存储，见调研表）。CLI 无原生 update 的探测证据见 §一。失败路径诚实：卸载失败=保持原状报错；重装失败=明确提示旧版已卸载需手动重装。前端：api `updateCodexPlugin`、store `updateCxPlugin`、PluginInstalledPanel codex 分支从「整市场 upgrade」改为单插件更新。**无 App 门面**：app.go 查无既有 Codex 插件更新门面（frontend 直调 codexplugin/Service 绑定），新增面走 Service 绑定 + api 层，与仓库双面惯例一致（P2 §一）。

### 2. Pi 包启停 + skill 级过滤（internal/piplugin/filters.go 新 + service.go/types.go）
- **偏差声明（有探测依据的最小决策）**：任务书预案「禁用=packages 移除条目+状态文件恢复」；实测发现 pi 原生过滤模式即可表达全排除（`!**` glob），故改用 **四键 `!**` 标记**：不迁移条目（pi list 仍可见）、不引入外部状态文件（settings 本身即状态，零漂移）、用户既有过滤条目保留（禁用追加/启用移除标记）、完全可逆、与 pi config TUI 语义互通（该包所有资源在 TUI 中如实显示已停用）。判定=四键均含 `!**` 且无 `+` 强制含条目。
- `SetPackageSkillEnabled(source, skillName, enabled)`：与 pi config TUI 算法逐字对齐（同目标去重→`±rel`→空数组删键→四键全空折叠回字符串）；同名多 skill 文件逐文件写模式；只动 skills 键，extensions/prompts/themes 互不干扰。
- settings 编辑全部 gjson/sjson 原位手术 + 原子写（temp+rename，保权限），非法 JSON 拒改。
- `Package.Enabled` 从恒 true 改为标记判定；`ResourceInfo.Disabled`（仅 skill）按过滤语义评估（白名单/`!`/`+`/`-` 四形态 + `**` glob，求值顺序镜像 pi applyPatterns）。

### 3. OMP skills 扫描 + provider（internal/ompplugin/inspect.go 新）
`scanPluginSkills`（skills/<name>/SKILL.md + 顶层 .md，Claude/pi 同源约定）；`InspectPlugin`（列表项→本地扫描，**零 CLI fork**，供聚合 provider 批量取用避免 N+1）；`GetPluginDetails`（CLI list 定位+扫描）。marketplace 条目回退读 `~/.omp/plugins/installed_plugins.json`（binary strings 证实路径；schema 按 Claude 同构做 installPath/install_path/path 三候选容错——**本机无 marketplace 实样，字段形态未实测**，未命中时 skills 诚实为空）。app.go 补 `sharedSkillProvidersOmp`（P2 缺口闭合）。

### 4. OpenCode：降级（不造假开关）
调研结论 §一：无子项机制（在线 schema + CLI help 双证）。SubItemsDisabled 留空；前端 OpenCodePluginPanel 现无 skill 开关（已核对），保持只展示语义。轻量方案（plugins `-id` 指令）作用面是插件 ID 非 skill，且 CodeBox 管理的是旧 `plugin` 键——不实施，如实降级。

### 5. SubItemsDisabled 聚合填充（app.go）
- codex：无子项存储→子项表恒空；**插件级禁用映射**（Enabled=false→全部 skill 标停用）；
- pi：`ResourceInfo.Disabled`（skill 过滤结果）+ 包禁用（Enabled=false→全部）；
- opencode：恒空（降级）；
- omp：插件级禁用映射（omp plugin disable→全部）；子项恒空。
- claude：P2 已完备；插件级 Enabled 不映射（settings enabledPlugins 缺省语义不确定，避免误标——保持既有行为）。

### 6. 更新/卸载联动刷新（OnChange 挂点）
四引擎 Service 新增**导出字段** `OnChange func()`（非方法——避免进入 Wails 绑定面；曾用 SetChangeNotifier 方法，重新生成后发现污染绑定面即改字段并重生成）。成功写操作（install/uninstall/update/upgrade/启停/skill 过滤写）后触发；app.go 装配期注入 `app.InvalidateSharedExtCache`（置 force 标记→下次 `GetAllSharedSkillsAggregated` 旁路 30s TTL）。**前端不强推**（任务书允许项）：ExtensionsView 各 pane 为 v-if，切 tab 即重挂载重拉，聚合天然新鲜；共用面板与插件面板互斥可见，无同屏陈旧窗口。Claude 引擎（internal/plugin）不在 P4 写面：靠 30s TTL + 面板重挂兜底，已在 app.go 注释与本报告如实标注。

## 三、测试输出摘要（真实运行，2026-11-20，Go 1.26.1 darwin/arm64）

```
$ go vet ./...                          → exit 0（零告警）
$ go test ./... -count=1                → RC=0，48 包全 ok（一次全量跑中 sharedext
                                          TestConcurrentRegistryAccess 偶发失败=该测试
                                          前置导入与并发导入者的既有竞态（P1 代码，非本
                                          任务改动面）；单独重跑与全量重跑均绿
$ go test ./internal/{codexplugin,piplugin,ompplugin,opencodeplugin}/ . -count=1 → 全 ok

新增 Go 用例（14 个）：
- codexplugin：UpdatePlugin 重装序列（marketplace upgrade→remove→add→list）+ 恢复
  禁用态；保持启用态+通知计数；重装失败错误诚实（提示旧版已卸载）；runner 加 run 注入钩子
- piplugin：包禁用/启用往返（折叠回字符串）；用户过滤器保留往返；skill 启停写 TUI 同形
  ±pattern + 详情 Disabled 标记；未知 skill/未登记报错；local 源绝对形态归一匹配；
  过滤语义 12 断言（nil/空数组/白名单/glob 排除/精确排除/父目录别名/强制含压倒排除…）；
  3 写路径通知计数
- ompplugin：约定目录扫描（含无 skills 目录诚实为空）；InspectPlugin 零 CLI；
  marketplace installed_plugins.json 回退；GetPluginDetails 定位+未知报错；4 写操作通知
- opencodeplugin：卸载成功通知恰 1 次 + 失败不通知
- app（app_sharedext_test.go）：codex/pi/omp provider 禁用映射（插件级+skill 级+包级）；
  既有 TestCodexPluginSkillsProvider 夹具补 Enabled:true（行为变更同步锚点）

$ npx vitest run（frontend，41 文件）    → 375 passed（新增 6：api/pluginLifecycle
                                          （codex 更新透传+pi 启停路由×2）、
                                          stores/piPluginEnable（setEnabled 刷新+详情重拉/
                                          失败抛错/setSkillEnabled 只重拉详情））
$ npm --prefix frontend run build        → vue-tsc 0 错 + vite ✓ built
$ wails generate module（v2.11.0）       → 真实执行；diff 仅本任务新面
  （codexplugin.UpdatePlugin、piplugin.SetPackageEnabled/SetPackageSkillEnabled、
  ompplugin.GetPluginDetails/InspectPlugin、piplugin.ResourceInfo.disabled、
  ompplugin.PluginDetail/ResourceInfo）；无手改生成文件
```

## 四、给 P5 的审核关注点

1. **pi `!**` 标记与用户手写 `+` 共存**：包禁用判定要求「无任何 `+` 条目」——用户手动 force-include 某资源时包不算禁用（该资源确实仍加载，语义如实）；审核确认此交互口径可接受。
2. **pi 过滤评估的 glob 近似**：`!`/白名单条目用自实现 `**`/`*` 匹配（minimatch 近似，不支持字符类）；CodeBox 与 TUI 写入的都是精确路径（无 glob），仅用户手写复杂 glob 时展示态可能偏差（不写坏配置）。审核可评估是否需要引入 minimatch 级库（当前判：不值得）。
3. **omp marketplace 路径解析**：installed_plugins.json 字段形态无实样可验（本机无 marketplace 插件）；三候选字段容错+未命中为空。审核如有 omp marketplace 环境建议实测一例。
4. **codex UpdatePlugin 重装失败窗口**：卸载成功+重装失败时插件缺失（输出明确提示手动重装）——codex CLI 无原子更新，此为机制下界；审核确认提示文案与 UI 行为（store 失败仍 force 刷新列表，列表如实反映缺失）。
5. **OnChange 联动范围**：codex/pi/omp/opencode 四引擎 + claude 引擎未挂（internal/plugin 不在写面）；P5 若补 claude，模式照抄（导出 OnChange 字段+app 装配注入）。
6. **聚合 SubItemsDisabled 新语义**：插件级禁用→该插件全部 skill 在共用中心显示「已停用」（codex/omp/pi 三引擎）；P3 模板零改动自动跟随。审核抽验共用中心对已禁用插件的展示。
7. `SetPluginEnabled`（codex）现也触发 OnChange（启停改变聚合输入）——语义正确但属行为新增，审核知悉。

## 五、边界与回滚

- 改动面：internal/{codexplugin,piplugin,ompplugin,opencodeplugin}/、app.go（装配段+共用中心 provider 段）、app_sharedext_test.go、frontend/src/{api,stores,components,__tests__}、frontend/wailsjs（wails generate 产物）。internal/sharedext 零改动；internal/plugin（claude）零改动。工作区另有 workflow.md 未提交改动（Leader/并行现场，未触碰）。
- 回滚：`git checkout -- app.go app_sharedext_test.go internal frontend/src frontend/wailsjs && rm internal/piplugin/filters.go internal/ompplugin/inspect.go frontend/src/__tests__/api/pluginLifecycle.test.ts frontend/src/__tests__/stores/piPluginEnable.test.ts`。
