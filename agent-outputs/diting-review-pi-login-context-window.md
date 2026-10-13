# Pi 登录预设上下文窗口配置：full 独立审核

VERDICT: FIX

发现 **4 Major、2 Minor**；另列 2 项 Info（归因/验证限制）。结论针对任务列出的当前源码及相关消费者的静态审核，不代表运行回归已完成。没有修改实现或编写项目测试。

## 基线、输入与验证可信度

- 任务输入：`/Users/run.mao/workpace/amagi-codebox`；任务书给定 HEAD=584a783。
- 独立读取 `.git/HEAD` 与 `.git/refs/heads/master`，当前分支 master，引用值 `584a7835b83d01f644a5e69ad49645520259f3fe`，与给定基线一致。
- 已完整读取新 override 源码及 9 个测试、config 登录源码及测试、登录 planner、前端组件、bind manifest；按相关片段读取 app 装配/启动、config 服务、provider sync、piconfig 登录检查及生成类型。
- 只读参考：`/Users/run.mao/workpace/amagi-pi/extensions/amagi-core/task-run.ts`、`rpc-pool.ts`；本机已安装 pi 包的 `package.json` 确认 **1.1.0**，并读取真实 `provider-composer.js` 及 model runtime/config/agentDir 相关路径。
- **本轮没有 bash/命令执行工具**。未执行 git diff/status、Go 测试、vue-tsc、vitest、浏览器、独立 pi 子进程、race 检测。未将主上转述的实现方实测计作本轮运行证据；以下反例均为静态路径证实，不声称动态复现。
- 上游 artifact：未提供；依据为冻结任务书和以上源码。只写本报告，回滚只需删除本报告；未触及用户源码、真实 ~/.pi 配置或凭据。

## Findings

### F01｜Major｜数字输入的 Vue 隐式转换使窗口配置无法保存

**定位**：`frontend/src/components/provider/PiLoginProviderDetail.vue:26,64`。

**触发条件**：正常填写名称/模型，在窗口框输入 `400000`（或手工输入 `0`），点击保存。编辑已有数值后只要修改数字也会触发。

**实际影响**：输入事件后 `contextWindow.value` 是 number；`contextWindow.value.trim()` 抛 TypeError。错误被 catch 显示，`savePiLoginPreset` 根本未调用，核心新增配置不可用。编辑回填用 String，未经输入的旧值可能保存成功，不能代表新增/修改正常。

**证据**：组件 number input 使用原生 `v-model`；本仓库安装的 `frontend/node_modules/@vue/runtime-dom/dist/runtime-dom.esm-bundler.js:1582-1600` 中 `castToNumber = number || vnode.props.type === "number"`，随后赋值 `looseToNumber(el.value)`。因此未写 `.number` 也会转数值；script 中 ref 的静态 string 声明不能防止运行时赋值。

**最小修复方向**：明确使用 number|string 输入状态并严格归一化/校验，或者保持受控字符串输入；不要对未经类型判定的模型值调用 trim。实施者补真实组件输入→提交正向，以及空值/0/负数/非法数值的交互验证。

### F02｜Major｜预设级窗口落成模型级全局值，remote/restart 与后续子 agent 可读错预设

**定位**：`app.go:530-549,3525-3527`；`pi_login_launch.go:136-159`。

**触发条件（不依赖并发）**：通过 config API 保存预设 A（同 provider/model，cw=256000），再保存 B（同 provider/model，cw=400000），随后经 remote planner 或 restart 启动 A。

**实际影响**：config 中 A 仍是 256000，但 models.json 中该 model 只有一个 400000；planner 只传 provider/model/thinking，不施加 A 的窗口，启动 A 实际读 B 的窗口。同模型的多个登录预设合法，保存接口只按 provider/name 分 key，未限制此条件。若 Leader A 已加载 256000 后保存/启动 B，A 后派生的独立子 agent 将读新文件 400000；继承同一 root 只保证路径一致，不保证配置快照一致。

**证据**：override 存储地址仅 `providers.<provider>.modelOverrides.<model>`，不含 preset；app 保存 listener 无条件写事件值。`buildPiLoginPlan` 119-163 行的实际 effects 只有 PTYStart/bootstrap，未携带窗口变更，注释直接承诺“remote/restart 读已落盘的目标态”。pi 1.1.0 `model-config.js:242-284` 是每次加载的不可变快照；`model-runtime.js:80-81` 新进程从 agentDir/models.json 读取。删除同模型 B 也无条件 cw=0，会移除 A 仍要求的覆写。

**最小修复方向**：先明确预设级与 provider/model 全局级的契约。若必须预设级及 Leader/子 agent 一致，需在执行边界保证选中预设被实际应用并保证后续子进程的配置一致性；不得为补缺口在 BuildPlan 中写盘。若批准改成全局设置，应统一数据模型/冲突校验及 UI 语义，不继续宣称独立预设双生效。至少验证 A/B 同模型不同窗口、remote 启动 A、删除 B、A 后续派生子 agent。

### F03｜Major｜锁外回调能倒序落盘，providerSyncMu 不保证“最后保存获胜”

**定位**：`internal/config/pi_login.go:141-180`；`app.go:530-549`。

**触发条件**：同一预设两次并发保存：T1 提交 cw=256000、释放 Config.mu 后尚未执行 listener；T2 提交 cw=400000 且先执行完 listener；再恢复 T1 listener。

**实际影响**：CodeBox config 最终 400000，canonical models.json 最终 256000。两次保存都返回 nil。remote/restart 无兜底，持续使用旧值；save/delete 倒序还可将已删除项的 override 重新写回。

**证据**：写锁闭包内完成 `s.config = &next` 并仅捕获 listener，闭包退出后才 `listener(preset,nil)`；app 锁仅包围事件值 Upsert，不检查 config 当前版本。可执行调度明确为 `T1 commit → T2 commit → T2 listener/write → T1 listener/write`。注释称“以最后一次为准”与代码不符。互斥保证读改写不重叠，不保证事件按提交顺序处理。

**最小修复方向**：保留锁外回调，以 revision/事件序号防旧事件覆盖，或在 providerSyncMu 下读取当前权威状态做有确定冲突语义的 reconcile；避免重新持 Config.mu 等待 providerSyncMu。由实施者添加受控调度验证，不靠随机循环证明顺序正确。

### F04｜Major｜语法合法但嵌套结构损坏的 models.json 会被静默替换

**定位**：`internal/launcher/pi_login_override.go:54-60,69-75`；读取边界 `internal/launcher/provider_sync.go:329-352`。

**触发条件**：models.json 内容为 `{"providers":{"anthropic":{"modelOverrides":[{"keep":"manual"}]}}}`，调用 `UpsertPiLoginModelOverride(dir,"anthropic","claude-sonnet-5",300000)`。目标 provider 本身或目标 model override 非 object 也有同类行为。

**实际影响**：原手写 modelOverrides 数组内容被直接替换成新对象并写盘，返回 nil，而非损坏文件报错不覆写。输入是无效 Pi schema，但正因无效，应保留原文件供诊断而非静默修复/丢失内容。

**证据**：strict reader 仅验证 JSON/root/providers 一级形状；嵌套断言失败的错误被 `_` 丢弃，nil 分支新建 map 并覆盖原键。现有 corrupt 测试仅 `{ not json`，未覆盖 schema 损坏。对于结构正确的文件，深合并确实保留兄弟 provider、同键 compat 等；本 finding 不否定该正向路径。

**最小修复方向**：对将要修改的 provider/modelOverrides/model 路径区分不存在与已存在但类型错误，后者返回明确错误且保持原文件。无需借此扩成全仓 schema 框架。

### F05｜Minor｜清除最后一个覆写后留下空 provider，使 Pi 产生配置错误

**定位**：`internal/launcher/pi_login_override.go:78-94`；`internal/launcher/pi_login_override_test.go:169-180`。

**触发条件**：文件只有 `{"providers":{"anthropic":{"modelOverrides":{"claude-sonnet-5":{"contextWindow":300000}}}}}`，对该 model 用 cw=0 清除。

**实际影响**：输出留下 `{"providers":{"anthropic":{}}}`。Pi 1.1.0 认为空 provider overlay 非法，产生 composition error；本机 runtime 会退回 builtin，因此不是核心不可用，按 Minor 而非 Critical/Major。正常“恢复内置窗口”操作不应制造配置诊断错误。

**证据**：函数删除空 modelOverrides 后仍无条件 `providers[provider] = entry`；当前测试反而断言该空 provider 存在。pi 1.1.0 `provider-composer.js:154-165` 在全部配置字段为空时抛 `must specify ...`；`model-runtime.js:151-167` 捕获错误、记录 compositionErrors 并返回 base。

**最小修复方向**：仅当 entry 清除后确实为空时删该 provider 键；保留仍有 baseUrl/models/compat 等内容的 provider。按实际 host 消费验证清除结果，而非仅检查 modelOverrides 被删。

### F06｜Minor｜default 清除路径非幂等，当前测试不能证明“不写盘”

**定位**：`internal/launcher/pi_login_override.go:76-88`；`internal/launcher/pi_login_override_test.go:103-115`。

**触发条件**：文件已有 `modelOverrides.m = {"compat":{"supportsStrictTools":false}}`，连续调用 cw=0。

**实际影响**：每次走 modelKey!=nil 分支并无条件 changed=true，虽未删除任何 contextWindow，仍原子改写文件、刷新文件身份/mtime；违背注释“cw<=0 且无既有覆写不动文件”的幂等承诺。启动 default 预设可重复触发无用 I/O。

**证据**：delete 前未检查 contextWindow 是否存在。正值幂等测试只比较 before/after 字节，写入完全相同内容也会通过，不能证明未写盘或 mtime 未变。

**最小修复方向**：只有实际删除窗口或实际剪枝时标记 changed；实施者以受控时间/文件身份或写入观察验证 no-write，覆盖正值和保留 compat 的清除路径。

## Info / 归因与生产级观察

### I01｜Info｜公共 key 冲突检查未持锁，基线归因未确认

`internal/config/pi_login.go:122,263-276` 在进入 changePiLoginPreset 锁之前读取 s.config 与 TerminalPresets map；函数自己的注释要求持锁调用。并发公共预设保存或 Load 可与该读取竞争，冲突判定也不原子。这是当前源码真实的并发风险，但本轮无法读取 HEAD diff，**不声称本次引入**，不混作上述新增功能 findings。建议 Leader 让实施者确认基线归因，相关调用至少将冲突检查与登录 mutation 放在同一 Config.mu 事务内。

### I02｜Info｜测试专用导出探针暂无生产消费者

搜索整个 codebox Go 源码，`PiLoginModelOverrideJSON` 只在新源文件和同名测试引用。导出只读诊断 API 不是安全缺陷，但任务无需诊断面，测试已经可自行读取真实文件；建议实施者确认保留它的实际诊断用途，避免为了测试扩展公共包面。

## Full 清单覆盖

| 审核项 | 核实与结论 |
|---|---|
| 正确性：深合并 | 合法对象路径只改 contextWindow；兄弟 provider/top-level/model/compat 的保留路径明确，测试有真实值断言。嵌套损坏不保护见 F04。 |
| 清除/幂等 | 删除目标窗口，剩余 compat 保留；空 model/modelOverrides 清理；空 provider 和重复 default 写入见 F05/F06。 |
| 锁序/错误路径 | Config.mu 释放后才回调，app 回调仅取 providerSyncMu 后做文件操作；既有 sync 为 providerSyncMu→Config RLock（provider_harness_sync.go:20-34），本回调无 Config.mu→providerSyncMu 持锁边，未发现该两锁死锁环。config nil 显式设 err，saveLockedConfig 失败赋 err 并返回，s.config/listener 只在成功后更新，错误不被吞。顺序一致性不能因此成立，见 F03。 |
| 并发文件写 | 这两处 app 新写入口与既有 provider sync 共用 mutex，单 App 内读改写不交叠。该锁不覆盖独立 CodeBox 进程/用户编辑器，也不覆盖 unlock→真正 spawn 期间另一个登录预设改写；固定 models.json.tmp 与跨进程排他是已有机制限制，不伪装为本次回归。 |
| 纯读 planner | buildPiLoginPlan 无调用 Upsert、无 ConfigMutation；对应 remote_session_adapter.go:455 纯读要求成立。但“已落盘即选中预设”前提失败见 F02，不应把副作用搬进 planner 修补。 |
| 依赖方向 | 搜索 internal/config 未命中 internal/launcher 或 internal/piconfig 导入；回调接口仅用 config 自身结构体，app 装配连接文件层，依赖方向正确。 |
| bind manifest | 新 setter 列入 configServiceAcceptedMutationSurface，沿既有组装注入清单惯例，未改 session/PTY gate。反射登记只证明已分类，不证明 Wails 生成/函数型参数可绑定；本轮未生成或构建，不为这个既有模式新增安全结论。 |
| 测试质量 | 9 个 launcher 测试具备真实创建/更新/保留字段正向与 corrupt/参数负向，不是只测错误或空长度；Go 不适用 isError/assert.match 字面规则，语义上已有真实返回/落盘值断言。2 个 config 新测试验证 json tag/reload/负数与 saved/removed 事件。listener 测试注释称失败不触发/锁外执行，却未设置失败并断言无事件，也未在回调读取 config 验证释放锁；旧失败测试验证 state/disk 但没有 listener。未覆盖组件数值输入、回调顺序、同模型多预设、remote contextWindow 或真实 host 清除消费。 |
| 前端校验/回填 | 数值正向失败见 F01；空值/0 回填为空，正值 String 回填保留值。HTML min=0 原生拒负数；step=1000 额外限制非千整数，而 Go 允许任意非负 int；输入校验规则需统一。parseInt 兜底把非法/负数默认为0，不是可靠的业务校验。页面第6行仍称只保存模型和思考，与新窗口配置不一致（低影响说明问题）。 |
| JSON/生成类型 | Go `context_window,omitempty` 与 models.ts:905,917 的可选 number/构造读取一致，api/piLogin.ts 直接 alias 同一 config.PiLoginPreset；省略字段经回填得到默认语义。手改生成物的下次生成稳定性未实跑。 |
| 权限/安全 | 新功能不读/写 OAuth token，仍由 RequirePiLogin 读取 credential-free metadata并钉 canonical root。参数空校验、损坏读保护、复用权限0600/原子写有实际用途；防御不足在嵌套损坏。 |
| 性能/维护性 | 数据量为本地配置，O(models.json) 深合并可接受；已有 mutex 复用，不需额外通用框架。default 非幂等与诊断探针见 F06/I02。 |
| 兼容/迁移 | 新字段 int+omitempty 对旧配置零值兼容；Load 不触发新 listener，不能保证导入/迁移后 remote 执行有目标覆写，未将这一点独立升级为 finding，已纳入执行前提风险。 |

## Leader / 子 agent 链路证据及边界

1. `internal/piconfig/login.go:15-21` StandardAgentDir 用 os.UserHomeDir 得标准 ~/.pi/agent；RequirePiLogin 返回检查过的同一目录，不追随旧 PI_CODING_AGENT_DIR。
2. `pi_login_launch.go:69-75` piLoginEnvOverrides 显式设置该 root；app 登录启动分支复用它，planner BuildEnv 也复用；POSIX 执行边界以 env 前缀再锚定（同文件 101-115）。
3. amagi-pi `task-run.ts` 实际函数名是 **buildMinimalEnv**（不是任务书中的 buildChildEnv）；164-167 行按 PI_ 前缀复制，1579-1584 行将其用于 shell:false 的真实 spawn。`rpc-pool.ts:756-774` buildWorkerEnv 同样复制 PI_，1122 行用于 worker 装配；显式 worker env overrides 可覆盖继承值，默认链成立。
4. pi 1.1.0 `dist/config.js:491-496` getAgentDir 优先环境变量；`dist/core/model-runtime.js:80-81` 加载 root/models.json；`provider-composer.js:74` 对既有模型应用 override.contextWindow；完整 provider composer 已读取，模型层实际消费规则确认。
5. 因此 **无需为 PI_ 透传再改 amagi-pi** 的机制判断有静态支持。amagi-* custom model contextWindow 也在 composer 的 modelFromJson 中消费。但 `amagi-glm/glm-5.3→400K` 独立进程实测本轮未重跑；没有把源码支持等同于本轮端到端运行通过。
6. 继承 root 不冻结 models.json；Pi 每个进程自己的配置快照加载时间不同。这正是 F02 的边界，不能写“一处写入两侧必然同值”。

## 未运行验证清单与建议回派

以下命令均 **未运行，退出状态未知**（不是通过也不是失败）：

- cwd `/Users/run.mao/workpace/amagi-codebox`：`export PATH="$HOME/.local/go/bin:$PATH"; GOFLAGS=-mod=vendor GOTOOLCHAIN=local go test ./internal/config/ ./internal/launcher/ ./internal/piconfig/ . -count=1`。
- cwd `/Users/run.mao/workpace/amagi-codebox/frontend`：`npx vue-tsc --noEmit`、`npx vitest run`。
- `git diff`/未跟踪文件范围确认；真实浏览器输入→保存；独立 pi 1.1.0 --list-models、清除配置后 host diagnostics；针对 callback 的受控并发/race。

本轮未运行 internal/envcheck，不报任务书提到的预存在8个环境性失败。模型凭据、远程真实账户、视觉审美不在本轮验证中。未编写复现脚本（无执行通道，不能产出未经运行的脚本冒充证据）；findings 已给可直接实现的输入/时序与最小方向，正式测试由实施者编写。建议先修 F01/F02/F03/F04，再以实际 host 消费和组件输入验证回派复审。
