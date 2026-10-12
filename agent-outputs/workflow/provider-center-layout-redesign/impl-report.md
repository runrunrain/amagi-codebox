# Provider Center 布局实现报告（v1 + v2 接力）

结论：design-v2 的统一文字左栏、右侧单分区、动态 master-detail、草稿保留及窄窗规则已实现。V1 构建、V2 4 文件/31 测试、V3 原 6＋新增 1 个 e2e 全通过；其余浏览器验收有真实证据。**不宣称全环境无错误**：V5 的失败 ESM URL 恢复仍有基线限制，X7 皮肤流程出现范围外既有 RangeError；详情见 §4、§8。

上游输入：
- `agent-outputs/workflow/provider-center-layout-redesign/p2r-taskbook.md`
- `agent-outputs/workflow/provider-center-layout-redesign/design-v2.md`（本轮布局规格）
- `agent-outputs/workflow/provider-center-layout-redesign/design.md`（v1 文案/语义规格）
- `agent-outputs/workflow/provider-center-layout-redesign/v1-snapshot/`（实际接力工作树，不是 HEAD）

本报告覆盖两轮，但 v1 的执行结果只按上游已核实事实引用；不把上轮截图或日志冒充本轮执行。本轮无 commit/push，无用户配置/真实凭据写入。以下相对证据路径均以本报告所在目录为根；源码以仓库根为根。

## 1. 改动文件清单与规格映射

### 1.1 v1 已在场的实现（12 个 tracked 文件＋3 个新基件）

以 `v1-snapshot/status.txt`、`tracked.patch` 和三个基件副本为归因基线。

| 文件 | v1 改动（每文件一句） |
|---|---|
| `frontend/src/views/ProviderCenterView.vue` | 删除 zone-label，统一 PageHead 文案，将二三级导航合成 subnav。 |
| `frontend/src/components/provider/PresetList.vue` | 卡列表迁为分组列表，补原生键盘编辑/删除焦点、空态动作及同步徽章。 |
| `frontend/src/components/provider/PiAmagiConfig.vue` | 删除重复页头/教学 hint，Profile 并入角色模型，工具栏/字段 code/InfoTip 承载语义。 |
| `frontend/src/components/provider/OmpGlobalConfig.vue` | 紧凑工具栏承载 YAML 路径和新会话生效，说明迁 InfoTip。 |
| `frontend/src/components/provider/OpenCodePresets.vue` | 分组列表、未搜索时常驻本机默认行，路径复制与搜索/模式/添加合入工具栏。 |
| `frontend/src/components/provider/OpenCodeGlobalConfig.vue` | 删除重复页头和整块空文件说明，配置分类中文化并保留 code。 |
| `frontend/src/components/provider/ProviderRegistryEditor.vue` | 双引擎工具栏、受管常显风险、字段 code 化、专用高级字段编辑保留。 |
| `frontend/src/components/provider/AuthConfigEditor.vue` | OAuth 管理标签/InfoTip、精简添加空态、密钥密文输入；api_key 类型徽章改可读的 source 型。 |
| `frontend/src/components/provider/ConfigCategoryCard.vue` | 向后兼容新增 code/meta/tip，InfoTip 与折叠按钮为兄弟而非嵌套。 |
| `frontend/src/components/ui/EmptyState.vue` | description 可选，无描述时不渲染空段落。 |
| `frontend/src/components/ui/StatusBanner.vue` | 新增 compact 展示，默认消费者不变。 |
| `frontend/src/styles/tokens.css` | 仅追加 popover/shadow-pop/warning-tint 三个设计 token。 |
| `frontend/src/components/ui/InfoTip.vue`（新） | 可聚焦 toggletip、body Teleport、视口钳制/翻转和键盘/指针关闭。 |
| `frontend/src/components/ui/FieldLabel.vue`（新） | 标签、code、术语说明和生效标签统一一行承载。 |
| `frontend/src/components/ui/EditorToolbar.vue`（新） | 路径胶囊、合法/生效状态、动作/搜索插槽；目录可截断但文件名保留。 |

v1 开工时 e2e 无改动；v1 原始截图保留在 `screenshots/` 非 `v2-` 文件中。

### 1.2 v2 本轮增量（12 个既有文件＋4 个新文件）

| 文件 | v2 增量与对应规格 |
|---|---|
| `frontend/src/views/ProviderCenterView.vue` | §2/§3.1：移除 subnav，workspace grid＋一个 SectionNav，父级常驻 7 模式、owner 防迟到、逐页选择记忆，右侧按 page id 隔离 engine 实例。 |
| `frontend/src/components/provider/PresetList.vue` | §3.2：只补头部/标签换行、名称热区收缩、删除按钮不缩小，弹窗交易保持。 |
| `frontend/src/components/provider/PiAmagiConfig.vue` | §3.3：4 个 EditorSection、visited/v-show、外部/本地导航、隐藏压缩错误定位、<480px 收缩。 |
| `frontend/src/components/provider/OmpGlobalConfig.vue` | §3.4：2 个单显分区、相同展示接口，角色引用/直接模型原逻辑不变。 |
| `frontend/src/components/provider/ProviderRegistryEditor.vue` | §3.5：提供商/root/add 动态叶子、真实默认及新增/删除后继，模型迁无框行组。 |
| `frontend/src/components/provider/AuthConfigEditor.vue` | §3.6：条目/add 动态叶子，内存 entry:N id 跨合法重命名稳定，新增标题聚焦/删除后继。 |
| `frontend/src/components/provider/OpenCodePresets.vue` | §3.7：仅补 key/头部收缩及窄工具栏搜索分行，不增加内部导航。 |
| `frontend/src/components/provider/OpenCodeGlobalConfig.vue` | §3.8：最多 9 个条件分区、默认 provider、visited/v-show，局部 deep 去除内嵌记录卡框并收缩字段。 |
| `frontend/src/components/ui/InfoTip.vue` | §7.2：可选注入分区 active，隐藏时沿用 close 清定时器/监听，阻止延迟再开。 |
| `frontend/src/components/ui/Dropdown.vue` | §7.2：可选注入 active，隐藏时关闭 body 菜单，范围外消费者默认为可见。 |
| `frontend/src/components/ui/FieldLabel.vue` | §4：flex-wrap 与 code 断行，不改业务 props。 |
| `frontend/src/components/ui/EditorToolbar.vue` | §4.2：命名容器及 640/400 档排布，附不支持容器查询的窄布局回退。 |
| `frontend/src/components/provider/configNavigation.ts`（新） | §2.4：无 Vue/API/store/IO 的纯类型、静态导航表及 encodeURIComponent id helper。 |
| `frontend/src/components/ui/SectionNav.vue`（新） | §7.1：nav/list/button、roving Tab、方向/Home/End、原生 Enter/Space、动态限高滚动和完整 aria 路径。 |
| `frontend/src/components/ui/EditorSection.vue`（新） | §7.2：无框 section/region，独立 h3＋code/count/meta/InfoTip，向后代 provide active。 |
| `frontend/src/components/ui/sectionVisibility.ts`（新） | §7.2：仅导出展示布尔值的 InjectionKey，不夹带保存/配置状态。 |

e2e 只改批准的两文件：
- `e2e/pi-amagi-config.spec.ts`：card→compaction helper、显式导航、3 处 reload 后定位；原保存对象断言保留，新增单显/草稿/隐藏错误/roving 用例。
- `e2e/pi-amagi-config.html`：明确 `navigation:'local'`，挂生产导航/外壳而非手写 fixture 导航；原 transport 不变。

本轮仅复用既有 UI kit 和 `--control/--separator/--accent-strong/--popover/--warning-tint` 等 token；v2 没有改 tokens.css、ConfigCategoryCard、StatusBanner、EmptyState。最终源码范围：`frontend/src` 22 文件（15 既有 tracked 改动＋7 新文件，包含 v1 基件），e2e 2 文件；Go/wailsjs/API/stores/manifests/lockfile/其他视图改动为 0。

### 1.3 验证产物（不属于生产代码）

- `x-harness/v2-{mock-wails.js,helpers.js,ab.sh,shots.sh,runtime.mjs,static.mjs}`：复用上轮 harness，新增 v2 隔离 transport、真按钮导航、测量/截图、7 个 L2 批次与只读 v1 不变量比较；没有新第三处 e2e 目录或依赖。
- `v2-*.log`、`v2-final-status.txt`、`screenshots/v2-*.png`（79 张）：命令、状态、交互及窗口证据。
- v1 的 `x-harness` 旧文件/原截图保留，不覆盖；皮肤背景使用已存在的 `x-harness/skin-demo.svg` 隔离素材。

## 2. T01–T57 文案落地核对

源码简称：PC=`views/ProviderCenterView.vue`；PL/PAC/OGC/OCP/OCG/PRE/ACE=`components/provider/{PresetList,PiAmagiConfig,OmpGlobalConfig,OpenCodePresets,OpenCodeGlobalConfig,ProviderRegistryEditor,AuthConfigEditor}.vue`；UI=`components/ui/`；以上前缀均 `frontend/src/`。每行以现代码/文案实读为准；v2 只迁挂载点，未重新添加已降级教学段落。

| ID | 实际落点/核对结果 | v2 迁移 |
|---|---|---|
| T01 | PC PageHead「管理服务提供商、公共预设与各 CLI 独立配置」。 | 保留。 |
| T02 | PC `formatConsumers`：用于 Claude Code / 用于 Codex · Pi · OMP。 | 从 subnav 迁右侧 `.pc-format-caption`。 |
| T03 | CLI zone-label 删除；一级仍「CLI 独立配置」。 | 不恢复重复说明。 |
| T04 | 原二三级选项名保留。 | v1 subnav 被单左栏取代。 |
| T05 | PL 空态标题＋添加预设/显示全部动作，教学描述删除。 | 分组列表右侧保留。 |
| T06 | PL 常显「同步 Pi/OMP」，不依赖 title；识图字用 success-strong。 | 不删徽章。 |
| T07 | PAC 原 h2 删除。 | 当前分区 h3 不是重复页头。 |
| T08 | PAC 文件名在工具栏；目录来源在 AGENTS_TIP。 | 外壳改变，语义保留。 |
| T09 | PAC PROFILE_ITEMS 保留 tiered/inherit 术语及 profile 控件。 | agents 分区顶部。 |
| T10 | PAC COMPACTION_TIP 第 1 段说明 Leader/RPC 共用及用途。 | 压缩 h3 旁 InfoTip。 |
| T11 | PAC COMPACTION_MODE_CAPTIONS 当前态短句＋MODE_ITEMS 三态完整解释。 | 压缩分区，原按钮名/aria-pressed 不变。 |
| T12 | PAC 压缩「下次压缩生效」及项目优先/目录继承/重新派发/认证全文。 | 左栏次级 meta＋section meta＋标题 InfoTip。 |
| T13 | PAC custom 常显「窗口不足或调用失败时取消本次压缩，历史保留」；tip 含配置/认证/摘要失败。 | 800px 仍完整可读。 |
| T14 | PAC「luban：… / luban 未配置模型」；tip 说明独立副本不联动。 | 原复制动作仍在压缩分区。 |
| T15 | PAC AGENTS_TIP 含角色覆盖优先、agents/*.md 与候选来源。 | agents 角色覆盖字段旁。 |
| T16 | PAC Leader 服务器＋code mcp.leader。 | MCP 分区，不再括注长句。 |
| T17 | PAC 默认服务器＋code mcp.default。 | 同上。 |
| T18 | PAC MCP_MODE_ITEMS leader/default 六条完整三态对照；MCP_MODE_HINTS 删除。 | FieldLabel InfoTip 保留。 |
| T19 | PAC Leader 字段常显「重启 pi 生效」；tip 含装配读一次/路由独立。 | MCP FieldLabel effect。 |
| T20 | PAC 默认字段常显「即时生效」；tip 含每次派发读盘/并集。 | MCP FieldLabel effect。 |
| T21 | PAC all/none/list 三态空文本；[] 不加载、空白名单保存等同不加载。 | 原 StringListEditor，角色附加例外在完整对照中。 |
| T22 | PAC 角色附加服务器＋mcp.agents＋取并集 tip。 | 同一 MCP 分区。 |
| T23 | PAC CONCURRENCY_TIP「模型 > 服务商 > 默认」及未匹配回退。 | 并发 h3 旁 InfoTip。 |
| T24 | PAC placeholder「留空 = 内置默认 4」；tip 含应用预填10/清空移除键。 | 并发 default 字段。 |
| T25 | PAC 服务商并发＋providers，冗余优先级 subhint 删除。 | 并发同页，规则在 T23。 |
| T26 | PAC 模型并发＋models，冗余 subhint 删除。 | 同上。 |
| T27 | PAC UI/EditorToolbar 路径胶囊 aria「复制配置文件路径：…」。 | 工具栏保持文件级。 |
| T28 | PAC ✓ JSON 合法与 `.pac-json-error` compact role=alert。 | 全文件错误工具栏下；压缩局部 alert 不改。 |
| T29 | PAC `.pac-catalog-warn` 保留模型目录失败原始错误。 | 全文件级，不藏分区。 |
| T30 | OGC 原 h2 删除。 | 当前角色/覆盖分区 h3。 |
| T31 | OGC 路径胶囊＋工具栏常显「新会话生效」。 | 所有分区/源码都有效。 |
| T32 | OGC OMP_ROLES_TIP 含 @role 引用及 models.yml 来源。 | modelRoles section tip。 |
| T33 | OGC OMP_OVERRIDES_TIP 含 @role 或直接模型。 | overrides section tip。 |
| T34 | OGC 路径/✓ YAML 合法/解析 alert/目录 warning 保留。 | 文件级工具栏与警示。 |
| T35 | OCP 未搜索时常驻「本机默认配置」及「未选择预设时使用本机 ~/.config/opencode 配置」。 | 分组列表，伪 key:(本机) 删除。 |
| T36 | OCP 暂无/无匹配＋添加/清除搜索。 | 原动作不被左栏替代。 |
| T37 | OCP 一处工具栏路径及 copyPath，删除重复底部路径。 | 保留。 |
| T38 | OCP JSON 空态「文件为空或读取失败」。 | JSON 仍只读 pre。 |
| T39 | OCP 搜索 leading、模式/添加 actions。 | 右侧窄时搜索/路径分行。 |
| T40 | OCG 原 h2 删除。 | 单分区 h3。 |
| T41 | OCG 路径胶囊＋工具栏常显「即时生效」。 | 分区/源码都在。 |
| T42 | OCG 巨大 empty hint 删除，工具栏状态「空文件」。 | 空文件默认 provider 直接可编辑。 |
| T43 | OCG MODEL_TIP 说明 agent 级/顶层 model；model 仅键存在时显示。 | model section 标题 tip。 |
| T44 | OCG 中文分类＋code provider/agent/mcp/permission/instructions/plugin/experimental；其他键。 | 迁左栏及相邻 h3/code。 |
| T45 | PRE Pi/OMP 重复 h2 删除，文件名由 models.json/models.yml 胶囊承载。 | 动态提供商 h3。 |
| T46 | PRE 工具栏「重新进入后生效」＋REGISTRY_EFFECT_TIP 候选来源/刷新方式。 | 文件级，非某提供商专属。 |
| T47 | PRE amagi-* 左栏/标题「受管」＋常显 `.pre-managed-warn` 全文。 | 风险没有降到 hover。 |
| T48 | PRE 协议/api、baseUrl、apiKey、name/contextWindow/maxTokens、thinkingLevelMap/thinking/input/cost；USD/百万 token 尾注。 | 原专用编辑器/code/placeholder 保留。 |
| T49 | PRE「暂无提供商」。 | add 叶子表单上方，非所有提供商页重复添加。 |
| T50 | PRE「暂无其他字段/暂无高级字段」空行删除。 | 原添加入口仍在。 |
| T51 | PRE 解析错误「可视化不可用，请切换到 JSON/YAML 模式修复」。 | 文件级 compact alert。 |
| T52 | ACE 原 h2 删除。 | 当前条目 h3。 |
| T53 | ACE OAuth 常显锁图标「由 pi CLI 管理」＋OAUTH_TIP 只读/令牌原样保存。 | 当前凭据正文；snapshot 不带令牌/账号。 |
| T54 | ACE「暂无凭据／可在下方添加 API Key；OAuth 需通过 pi CLI 登录」。 | add 叶子表单上方，途径仍常显。 |
| T55 | ACE access/refresh 原样保留全文合入 T53 tip。 | 不重复灰字，不改变保存函数。 |
| T56 | ACE 凭据路径 aria、添加 API Key、提供商名（与注册表一致）、未认证。 | 添加标题仅由 EditorSection 输出。 |
| T57 | ACE 解析错误「可视化不可用，请切换到 JSON 模式修复」。 | 文件级 compact alert。 |

I1/§6.1 复核：T19/T31/T20/T41/T12/T46 的已证实生效时机分别常显；T47 覆盖风险、T53–55 OAuth 管理/只读与 T13 取消压缩后果没有只藏 tooltip。MCP 三态/空名单、default 清空回落4的完整说明可由键盘打开。角色模型/profile/并发/auth **未擅自补生效时机**。

## 3. S1–S12 自查

| 编号 | 结果 | 依据 |
|---|---|---|
| S1 | PASS | `v2-static.log`：五编辑器旧 h2/卡壳为0，教学 hint 移除；`v2-coverage.log`：九视图右侧无 h2，当前 section 独立 h3。 |
| S2 | PASS | 同静态日志：6 个 EditorToolbar 使用者；路径只截目录且保留文件名，长 Windows/Unix 路径截图可读。 |
| S3 | PASS | 两页无 pc-subnav，每视图 1 个 SectionNav；父级 7 模式在 chunk 延迟/失败、数据 loading/error 都存在。 |
| S4 | PASS | §2 T01–T57 全部有现代码落点，T02/T04/T54 按 v2 迁移。 |
| S5 | PASS | §2 末语义复核＋`v2-pi-compaction-consequence-800x600.png`/受管长名/OAuth N1 截图；后果不只 hover。 |
| S6 | PASS（既存失败恢复缺口另列） | `v2-static.mjs` 从 HEAD＋v1 patch 只读重建基线：五个 handleSave 与 v1 逐字符一致；lazyPreset、配置档/导入/导出函数一致；7 个 presetLoadKey；watch(mainTab)/按需 engine 链路保留。PresetLoadError 仅新增 mounted 展示报告。V5 运行见 §4。 |
| S7 | PASS | `v2-final-status.txt` 与静态脚本：禁止 Go/wailsjs/API/store/其他视图改动为0；e2e 精确两文件。报告/harness 在授权产物区。 |
| S8 | PASS | manifests/lockfiles diff 为0；未安装/新增依赖。 |
| S9 | PASS | 五个编辑器 ConfigCategoryCard import/使用为0；公共文件仍在且与 v1 字节一致；无隐形旧卡欺骗测试。 |
| S10 | PASS | `v2-drafts.log` 与 e2e：每次一个 section 可见、已访问 v-show 保实例；非法局部 JSON/空中间态回来还在；同文件切区 API 计数不增。 |
| S11 | PASS | snapshot 仅 owner/phase/mode/activeId/items；Registry items 只 id/label/受管，Auth 只 uiId/name/type，未包含 key/access/refresh/accountId/expires；父级 owner guard，迟到 Pi 请求不污染 OMP。未缓存跨 engine 动态数据。 |
| S12 | PASS | EditorSection 无框/底/阴影；OCG 指定祖先 deep 将 pe/ae/me/perm 记录改分隔列表；PRE model 无框行组。展开记录截图不是仅换 class 名。 |

`git diff HEAD --check -- frontend/src e2e` exit=0，见 `v2-diff-check.log`。有意保留有效的 v1 token/公共组件，不以全局清理扩大范围。

## 4. V1–V5：真实命令与行为证据

所有命令 cwd：`/Users/run.mao/workpace/amagi-codebox`。浏览器页面为 `http://127.0.0.1:5199/?s=<fixture>#/provider`：真实 AppShell/ProviderCenterView/生产组件＋浏览器隔离 Wails transport；**不是桌面 WebView/真实磁盘/API 集成证据**。使用已安装 Chromium、agent-browser（九视图截图）与 Playwright（断言批次），未加依赖。

| 编号 | 命令/执行 | 退出/结果 | 日志 |
|---|---|---|---|
| V1 | `npm --prefix frontend run build` | 0；vue-tsc 类型门＋Vite build 通过，编辑器仍为独立 lazy chunks。 | `v2-build-final.log` |
| V2 | `npm --prefix frontend run test -- src/__tests__/components/provider` | 0；4 files、31 tests passed，4 个测试及逻辑模块未改。 | `v2-vitest-final.log` |
| V3 | `npx playwright test -c e2e/pi-amagi-config.playwright.config.ts` | 0；7 passed（原6＋结构1），3.8s。 | `v2-pi-e2e-final.log` |
| V4 | `node agent-outputs/workflow/provider-center-layout-redesign/x-harness/v2-runtime.mjs drafts` | 0；6 checks，0 page errors。 | `v2-drafts.log` |
| V5 | `node agent-outputs/workflow/provider-center-layout-redesign/x-harness/v2-runtime.mjs lifecycle` | 0；6 checks，0 page errors；失败 ESM URL 恢复为部分覆盖，见下表。 | `v2-lifecycle.log` |

切片构建：`v2-build-{foundation,container,pi,dynamic,opencode}.log` 均通过；Pi 切片完成即跑 V3，`v2-pi-e2e-first.log` 7/7。最终日志才是最后代码状态的命令级依据。上轮 v1 build exit=0 来自接力任务书的 Leader 已核实事实，本轮没有再装作在 v1 树构建。

### 4.1 V4 实际步骤

1. Pi 压缩选空 custom 并进手动模型态→MCP 选空白名单→并发 default 清空→再回压缩：custom/手动态/空输入/局部 alert 均保留。
2. MCP 回来仍白名单，常显「名单为空，保存后等同『不加载』」；并发回来仍空字符串，而非被重置成4/10。
3. 三分区来回前后 `GetAmagi/GetPi/GetOmp/GetOpenCode` 调用数组完全相同。
4. OpenCode unknown RawJsonEditor 输入 `{"retain invalid draft"`→provider→unknown：原非法局部文本和错误仍在，没有 emit/清洗成合法数据；这不等于非法局部草稿会被写盘。
5. 压缩错误隐藏时工具栏「上下文压缩待修正」可点击回定位；程序选区（没有 pointerdown/blur）也关闭旧 InfoTip/Dropdown。

### 4.2 V5 实际步骤与边界

| 路径 | 真实观察 | 判定 |
|---|---|---|
| 同文件切区 | 读取调用数量完全不增，不调用 engine setter、不增加 remount key。 | PASS |
| 配置档应用 | 点真实 quick-switch 应用按钮，真实 applied 事件使 Pi 重挂载；GetAmagiConfig 从1到2，新的 fixture profile=inherit。 | PASS；隔离后端 |
| Pi→OMP Registry | 路径 models.json→models.yml；YAML 源码以 providers: 开头，旧实例未复用。 | PASS |
| 初次读取失败→重试 | 原 ErrorState 显示 isolated first read failed；真实「重试」重新读到 ready。 | PASS |
| 当前 chunk 延迟 | 拦截 PiAmagiConfig.vue 请求，未挂编辑器时7模式仍可点；父级预选压缩，释放 chunk 后真正显示压缩。 | PASS |
| 当前 chunk 失败 | 原 PresetLoadError 显示，7模式不消失；点重试使 owner/presetLoadKey 改变；切 OMP 能工作。 | PASS：导航与 remount；**不是失败资源恢复全通过** |
| 同 URL 失败资源恢复 | Chromium 缓存失败 ESM import，解除 route 拦截后原 URL 仍不重取，attempts=1，当前模式仍 error。 | 基线缺口保留；没有改 loader、cache-bust 或吞错。另存 `v2-lifecycle-cached-import-failure.log`。 |
| 旧 Pi 数据请求迟到 | Pi registry 仍 pending 时切 OMP，随后释放 Pi promise，OMP 路径/子项未被旧事件覆盖。 | PASS |

### 4.3 其余 L2 批次

共同命令：`node agent-outputs/workflow/provider-center-layout-redesign/x-harness/v2-runtime.mjs <batch>`。

| batch | exit | 断言/页面错误 | 主要证据 |
|---|---|---|---|
| dynamic | 0 | 10 checks / 0 | Registry/Auth N=0/1/20、长名、新增标题焦点、重名停留、取消、下一/上一/add 后继、root 删除、auth 重命名 id/焦点稳定和保存无 uiId。 |
| narrow | 0 | 20 / 0 | 800 全15静态分区展开记录无横溢，1024 两抽样，OpenCode 7/9 条件分区/回退。 |
| states | 0 | 22 / 0 | 七模式 loading/error、六种空态、Pi 保存成功/失败反馈与按钮恢复。OCP error 既存吞错另列。 |
| interactions | **1** | 4 断言成立 / **1 page error** | 3 个真实键盘弹窗/删除焦点或只读 JSON；skin InfoTip 实色；RangeError 如实使批次失败。 |
| coverage | 0 | 26 / 0 | 九宽视图结构尺寸、15叶子从其他模式两次真实点击、底部 tip 向上翻转、窄档完整压缩后果。 |

日志为 `v2-{dynamic,narrow,states,interactions,coverage}.log`，记录 COMMAND/CWD/EXIT_CODE。连同 drafts/lifecycle：6 个干净批次共 **90** checks；interactions 的4个行为断言虽成立，但有page error，**不能汇总成94项全绿**。

只读自查：`node agent-outputs/workflow/provider-center-layout-redesign/x-harness/v2-static.mjs` exit=0，`v2-static.log`；用保留 patch 重建 v1 字符串后比较保存函数/loader/共享件，不执行 restore 或写共享树。

## 5. X1–X10 视觉/交互结果与截图

本轮已用 read 实际打开九视图两档基准图、各复杂窄分区、N 矩阵、提示/错误/状态/弹窗等关键截图，不以文件名推断视觉。其他状态图已存且有 DOM 断言，未声称每张逐像素审查。下列路径均在 `screenshots/`。

### 5.1 九视图基准截图（18张）

| 视图 | 1280×800 | 800×600 |
|---|---|---|
| Anthropic 预设 | `v2-presets-anthropic-1280x800.png` | `v2-presets-anthropic-800x600.png` |
| OpenAI 预设 | `v2-presets-openai-1280x800.png` | `v2-presets-openai-800x600.png` |
| Pi Agent | `v2-pi-agents-1280x800.png` | `v2-pi-agents-800x600.png` |
| Pi Registry | `v2-pi-registry-1280x800.png` | `v2-pi-registry-800x600.png` |
| Pi Auth | `v2-pi-auth-1280x800.png` | `v2-pi-auth-800x600.png` |
| OMP Agent | `v2-omp-agents-1280x800.png` | `v2-omp-agents-800x600.png` |
| OMP Registry | `v2-omp-registry-1280x800.png` | `v2-omp-registry-800x600.png` |
| OpenCode 预设 | `v2-opencode-presets-1280x800.png` | `v2-opencode-presets-800x600.png` |
| OpenCode 全局 | `v2-opencode-global-1280x800.png` | `v2-opencode-global-800x600.png` |

`v2-shots-{1280,800}.log` 为 `bash agent-outputs/workflow/provider-center-layout-redesign/x-harness/v2-shots.sh 1280 800` / `... 800 600` 的九视图记录：全为1左栏、overflow=[]、应用侧栏264；grid分别184/712、112/356。1024抽测144/524，见 narrow 日志。没有隐藏主应用侧栏来挤宽正文。

### 5.2 验收逐项

| 编号 | 判定与视觉结论 | 截图/交互证据 |
|---|---|---|
| X1 | PASS：宽档格式/CLI模式归属清楚，当前叶蓝色边/底，正文只有工具栏与当前h3，无重复h2或水平二三级Tab。 | §5.1宽档九图＋coverage 9项。 |
| X2 | PASS（Chromium）：800文字栏112、正文356、应用栏264；工具栏换行不盖控件，长路径保文件名，复杂字段单列。1024工具栏两行适配。 | §5.1窄档九图；`v2-pi-agents-{compaction,mcp,concurrency}-800x600.png`；`v2-omp-agents-overrides-800x600.png`；`v2-opencode-global-{model,provider,agent,mcp,permission,instructions,plugin,experimental,unknown}-800x600.png`；`v2-{pi-registry,opencode-presets}-1024x768.png`。 |
| X3 | PASS：说明已降级为字段code/状态/可打开完整说明；压缩custom三条必要常显内容（当前态、后果、luban）保留；长受管风险自然换行。 | `v2-pi-compaction-consequence-800x600.png`、`v2-pi-registry-N1-800x600.png`；源码/T核对。 |
| X4 | PASS：导航单Tab入口；Up/Down/Home/End移动但不加载，Enter激活，Space用原生按钮；InfoTip Enter/Space/Escape回焦；页面级aria含完整CLI/模式路径。 | V3新增结构用例、drafts；`v2-pi-tip-800x600.png`、`v2-pi-bottom-field-tip-800x600.png`。 |
| X5 | PASS：压缩标题长tip视口内完整，底部MCP字段tip自动向上；切区后Teleport浮层无残留。 | 上述两tip图；drafts程序切区证明非靠pointerdown关闭；coverage测量8px边界与翻转。 |
| X6 | PASS：三个预设行可键盘Enter开原生产弹窗；公共预设删除独立Tab焦点并取消；OpenCode搜索空态、JSON只读保持。 | `v2-{presets-anthropic,presets-openai,opencode-presets}-keyboard-dialog.png`，interactions前3项无page error；截图等待原过渡到opacity1后实读。 |
| X7 | **局部观察通过／整体有既存错误**：皮肤开启后InfoTip白色实底，computed background=rgb(255,255,255)，字可读；同时范围外皮肤流程RangeError，不能称皮肤全路径通过。 | `v2-skin-tip-1280x800.png`；`v2-interactions.log` exit1；定位见§8。 |
| X8 | PASS：所有15静态叶从其他模式均两次真实点击到达；Pi/OMP注册表/Auth N=0/1/20全部实测。动态限高192，N20可滚动不无限推远其他入口；长名左栏两行/右侧完整换行，新增/删除/重命名反馈明确。 | `v2-{pi-registry,omp-registry,pi-auth}-N{0,1,20}-800x600.png`（9张均实读）；dynamic/coverage。数据明确为fixture，不是真凭据。 |
| X9 | PASS：有model/unknown时9项，无时7项；源码删条件键回可视化回退provider，没有虚构键；空文件可直接添加provider。 | `v2-opencode-seven-sections.png`、`v2-opencode-empty.png`；narrow。 |
| X10 | PASS：源码是整个文件而非当前分区；源码时叶禁用并提示；回可视化保留有效选择；非法Pi源码按原e2e不被覆盖；隐藏压缩错误有定位。 | `v2-opencode-invalid-local-draft.png`、`v2-pi-hidden-error-800x600.png`；drafts和V3原6项。 |

状态走查补充：七模式loading/error时保留7入口；Registry/Auth等empty有新增表单/动作；source叶disabled；Pi保存成功toast「配置已保存」、失败toast含真实拒绝原因并恢复按钮；截图 `v2-pi-agents-loading-800x600.png`、`v2-pi-registry-error-800x600.png`、`v2-opencode-presets-error-800x600.png`、`v2-pi-save-{success,error}.png` 已实读。成功/失败toast截图等待动画opacity1，不以隐藏DOM当视觉证据。

视觉对照实际打开：v1 `pi-agents-long-800x600.png` 仍有水平二三级入口＋分区卡头/内边距；v2 `v2-pi-agents-800x600.png` 以文字左栏和无框单分区替代。v1 `presets-anthropic-long-800x600.png` 的格式横条迁为 v2左栏，右侧分组列表/徽章仍完整。旧图不覆盖。

## 6. 实现细节偏差与决策

1. `SectionNav` 增可选 notice prop 承载源码/动态loading/error提示；不新增另一条导航或loading业务接口。
2. `PresetLoadError` 增 onMounted owner校验/phase=error展示报告，以在chunk失败时准确禁用叶子；重试仍只增加presetLoadKey，loader函数原样。
3. EditorSection/title ID 使用模块计数，不依赖Vue3.5 useId；导航项使用映射查找而非盲split用户名称。
4. 空动态默认由加载后的真实items决定；Auth `entry:N` 仅在内存保持，合法重命名迁移映射，保存未序列化uiId。
5. 窄档走查发现OpenCode provider的MaskedValue「显示」可能挤成竖排；只在OpenCode全局祖先限定deep补 option value整行、toggle/reveal不缩小和white-space。未改共享MaskedValue/ProviderMapEditor源码或密钥显示行为。
6. MCP/并发仍每分区比较多个关联字段段，不拆新的导航；OC嵌套记录原展开逻辑保留，仅呈现成separator列表。
7. 窄图自动展开只点pe/ae/me记录头；没有把所有aria-expanded=false按钮（包括InfoTip）同时打开。弹窗/toast取图等待既有过渡结束，避免截图捕获透明起始帧。
8. 不为通过V5改失败ESM缓存策略、不为通过全状态测试修改store吞错；不越界修皮肤递归。详细风险明确交Leader，而非削弱断言/吞pageerror。interactions遇pageerror返回1。

以上是实现细节/取证改进，无布局结构、文案含义、保存边界或原e2e业务断言含义的设计级偏离。

## 7. 回滚说明（未执行）

当前 HEAD=`0131c80cce1c2eb8a34677115fdc1de50f3c264d`，**HEAD不是v1**。`v1-snapshot/tracked.patch` 保存开工时tracked的完整HEAD→v1差异；`v1-snapshot/{InfoTip,FieldLabel,EditorToolbar}.vue` 保存当时三个未跟踪基件；`status.txt` 记录现场。快照仅用于归因，不授权覆盖共享树。

若主上/Leader授权回到v1：
1. 在隔离目录以匹配HEAD＋快照patch重建v1内容（本轮static脚本已用只读字符串方式验证可重建），与当前文件逐hunk比较；有他人并行修改时保留/合并，不能整文件覆盖。
2. 先撤销ProviderCenterView/五编辑器v2调用与导航状态，恢复v1容器/卡壳；再回退指定e2e两文件，撤销InfoTip/Dropdown可见性注入和工具栏窄查询增量。
3. v2新增SectionNav/EditorSection/configNavigation/sectionVisibility确认无引用且另获授权才删除；可暂留无引用文件。
4. 保留v1基件/文案/分组列表/token及ConfigCategoryCard扩展，不碰用户配置格式/内容。
5. 重跑v1构建/4逻辑测试及与v1结构相匹配的e2e；旧截图只作对照，不当本次回滚验证。

无自动restore/reset/clean/stash/revert，无提交，亦未执行任何回滚。

## 8. 覆盖缺口与遗留

| 项目 | 事实、影响、处理边界 |
|---|---|
| 皮肤RangeError（范围外） | `v2-interactions.log` 的真实stack反复 `syncSkinDom(App.vue)`→`requestBake(skinBake.ts)`；实读App.vue第55行把syncSkinDom作为onDone，skinBake.ts在blur<=0/不支持/已有缓存时同步调用onDone，造成递归。本fixture blur=0触发。两个文件均不在git diff，本轮未改；X7实色观察成立但整皮肤流程不通过。建议Leader单独安排修复，不能凭截图宣称整体健康。 |
| chunk失败URL缓存 | 失败import在Chromium同页面缓存；重试key/owner真实更新，但原URL不重取。导航可逃离、其他模式可用；完整chunk恢复未达成，保持原loader/API契约。 |
| OCP读错被吞 | store.loadPresets既有catch吞读错，OpenCodePresets局部error仍为空，表现为默认行＋暂无预设而非ErrorState；`v2-opencode-presets-error-800x600.png`与states日志记录。未改store，不冒称错误态提示完善。 |
| 真实平台/后端未测 | Chromium浏览器＋隔离transport不证明Windows WebView2、macOS WKWebView/原生Wails、真实磁盘权限/原子写入/密钥库；Windows非覆盖滚动条340px预算未现场测；不支持container-query的fallback仅静态核对，未用旧WebView执行。 |
| 视口/极值边界 | 实测1280×800、800×600、1024×768抽样；未测更小非法窗口、操作系统DPI缩放、200%字体/读屏器、远程横幅开启、任意无限深递归/巨量条目。N=20覆盖限高滚动，不代表无限规模性能承诺。 |
| 原有草稿保存边界 | 同文件visited会话保草稿；跨模式/engine照旧卸载丢弃未保存内容，没有自动保存/跨文件缓存。RawJsonEditor非法局部文本不emit，原handleSave可能仍保存上次合法modelValue；本轮保留，不声称全编辑器具Pi同等级阻止非法局部草稿写入能力。 |
| 状态取证范围 | 七模式加载/读取错误、相关空态、源码disabled、Pi成功/失败保存已测；其他编辑器保存请求spinner的长等待及每个字段业务保存未逐项扩展，主要由原handleSave字节相同支撑不回退。弹窗fixture缺本地provider候选，已如实显示原「当前无可用的Provider」，不代表真实账户配置。 |
| 后台进程 | 测试消费已可达5199 Vite；`v2-vite.pid`旧记录63956已不在，实测监听为node35887（frontend vite --port5199 --strictPort）。无法据旧PID可靠确认归属，未擅自终止共享进程。可由Leader复核后清理。 |

任务内自检闭合；上述未覆盖/既存缺口保持明确，不以构建/测试数替代视觉或真实集成证据。
