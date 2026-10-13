# Pi 登录 contextWindow：quick 增量复审

VERDICT: PASS_WITH_MINOR

F01–F06 与 I02 的指定修复路径静态核对通过；发现 1 项 Minor（实现契约注释仍写旧策略）。**结论仅覆盖本轮读取的修复源码/测试，不包含运行验证通过的声明。** 当前工具面只有 read/grep/find/write 等读取与报告工具，无 bash/命令执行通道，未运行任何测试。

## 范围与基线

- 必读上游：`/Users/run.mao/workpace/amagi-codebox/agent-outputs/diting-review-pi-login-context-window.md`，已完整读取；该报告基线为 `584a7835b83d01f644a5e69ad49645520259f3fe`。
- 本轮对象：任务书 F01–F06、I02 修复，以及全组对齐、reconcile 锁序、override map 回写直接链路。源码路径以下均相对 `/Users/run.mao/workpace/amagi-codebox`。
- 未运行 git diff/status，故未独立确认当前 HEAD、完整差异或未提交改动归因；逐项依据当前读取的实际文件，而非实施方描述。不重审上轮未变面/I01。
- 未修改实现、未编写项目测试/修复 patch，仅写本报告。回滚只需删除本报告。

## 修复逐项结论

| 项目 | 结论 | 源码与测试证据 |
|---|---|---|
| F01 数字输入 .trim() 崩溃 | PASS（静态） | `frontend/src/components/provider/piLoginContextWindow.ts:7-9` 先 String 归一化再 trim/parseInt；number 不再调用字符串方法。`PiLoginProviderDetail.vue:42,52,67-68` 实际 import、string\|number 状态及提交消费贯通。`frontend/src/__tests__/components/provider/piLoginContextWindow.test.ts:7-24` 断言 number 400000/0、字符串、空串、abc、负数字符串、null/undefined/NaN 的真实结果。未运行组件交互；该测试是纯函数测试，不冒充组件测试。 |
| F02 同模型预设配置与生效串值 | PASS（按新批准的模型级共享契约） | `internal/config/pi_login.go:133-143,165-185` 在独立复制的 PiLoginPresets map 上，持写锁对同 provider+model 全组对齐，再持久化、切换权威 config；只改兄弟 ContextWindow，名称/思考等字段保留。`internal/config/pi_login_test.go:248-281` 覆盖 A/B 正值共享、清零共享与不同 model 不扩散。删除时 app reconcile 从存活兄弟取值，不再无条件清零（`app.go:544-560`）。不存在“同模型不同窗口独立预设”承诺，故不以旧契约判失败。 |
| F03 锁外旧事件覆盖新配置 | PASS（指定回调路径，静态） | `app.go:533-561` 事件只提供 provider/model 身份，持 providerSyncMu 后重读 GetPiLoginPresets，取当前值或无预设时 0，再 Upsert；不使用事件 ContextWindow。`internal/config/pi_login.go:188-207` 在写锁退出后才回调。T1 commit→T2 commit→T2 callback→T1 callback 两个回调均读取终值；save/delete 倒序同样按存活状态收敛。若读取后发生新 commit，后续回调等待该 mutex，最后再读新状态完成收敛。未见受控倒序调度测试，不能称并发动态验证通过。 |
| F04 嵌套损坏被静默替换 | PASS（目标 provider/overrides/model 三层） | `internal/launcher/pi_login_override.go:56-65,69-76,84-101,135-145` 区分不存在与存在但非对象；正值和清除分支均在损坏类型时返回错误，不调用写盘。`internal/launcher/pi_login_override_test.go:254-277` 覆盖三层 × 两操作，并断言原始字节保留。顶层读取仍复用 `provider_sync.go:329-352` 的既有 reader；本轮不声称新增了完整 schema 校验。 |
| F05 空 provider 残壳 | PASS（静态） | `pi_login_override.go:104-123,126-130` 清除后层层剪枝，只有 entry 为空才删 provider；末尾条件不会复活空 entry。providers 顶层仍回写为空对象。`pi_login_override_test.go:179-204` 分别断言空 provider 删除及带 baseUrl 的 provider 保留、空 overrides 删除。 |
| F06 清除非幂等 | PASS（静态） | `pi_login_override.go:84-101` 无 overrides/无 model/无 ContextWindow 均立即返回，不走 WritePiAgentConfig；有窗口字段才 changed=true。`pi_login_override_test.go:208-224` 对 compat-only 清除断言内容和 mtime 不变；`228-236` 验证无文件清除不创建。无 model 键分支没有专门的新测试，但源码返回路径明确。 |
| 正值幂等 mtime 补证 | PASS（断言确已加入） | `pi_login_override_test.go:103-129` 读 before/after 的 Stat，`121-123` 断言 ModTime 相等，且保留换值更新断言。这修复了“只有字节比较”的事实；仍未采用受控旧时间戳或写调用观察，极粗时间粒度上同内容重写可能漏检，故不是严格 no-write 的独立证明。 |
| I02 测试导出探针 | PASS（静态） | 当前 override 源码/测试均已完整读取；整个 codebox 的 Go 搜索 `PiLoginModelOverrideJSON` 无命中。 |
| 页面文案 | PASS（指定内容） | `PiLoginProviderDetail.vue:6` 已包含“模型、思考等级与可选上下文窗口”，删除旧的“只保存模型和思考”说明。模型级共享的跨预设效果未明确告诉界面使用者，建议补提示，但本轮不把任务书已批准的共享操作本身报作写扩散缺陷。 |

## 修复接缝核对

### 全组对齐的写扩散与事务边界

`pi_login.go:165-169` 先复制配置结构与登录预设 map，兄弟预设为值类型；`133-143` 使用 provider/model 双条件，仅更新 ContextWindow。由此不同 provider 同 model、同 provider 不同 model 均不会被代码选中；没有修改公共 TerminalPresets/API provider、没有兄弟逐个保存或递归 listener。保存失败在切换 s.config/捕获 listener 前返回（182-189），不会留下只在内存完成的全组修改。测试目前没有跨 provider、兄弟非窗口字段、全组持久化 reload 的独立断言，以上为静态路径证据。

### 锁序

- 回调：`app.go:541-545` 为 providerSyncMu → Config RLock（`pi_login.go:98-100`），GetPiLoginPresets 拷贝后即释放读锁。
- 既有同步：`provider_harness_sync.go:20-31` 同为 providerSyncMu → Config getters。
- mutation：`pi_login.go:158-160,188-207` Config 写锁释放后才取回调中的 providerSyncMu；setter `internal/config/service.go:142-146` 只装配 listener，不调用它。
- 因此指定两锁链无 Config 写锁 → providerSyncMu 的持锁反向边；没有因 reconcile 新增两锁死锁环。未运行 race/并发测试，不扩成全仓锁审核。

### overrideMapAt 新建对象回写

新 provider 时 entry 为新 map（`pi_login_override.go:63-65`）；两个 helper 返回的新 map 由 `81-82` 显式串接 modelKey → overrides → entry；entry 此时 len>0，`126-129` 串接 entry → providers → existing，`130` 写真实文件。不存在漏回写 parent 的断链。`pi_login_override_test.go:42-50` 的全新 root happy-path 正是这一完整链路的落盘断言，而非只检查函数不报错。

## 新发现

### N01｜Minor｜旧策略注释与当前共享写入契约冲突

- **定位**：`internal/launcher/pi_login_override.go:25-26`。
- **触发条件**：实施者依据该函数的合并语义说明维护同模型多预设保存逻辑。
- **实际影响**：注释仍称“保存侧拒绝窗口不一致的共存”，并引用不存在的 `ConfigService.checkPiLoginContextWindowClash`；真实行为是允许保存并全组覆盖。该处是函数的行为契约说明，可能让后续维护错误恢复旧拒绝策略或误判写扩散，不是纯风格偏好。无当前运行故障，按 Minor。
- **证据**：本轮实际读取的上述注释与 `internal/config/pi_login.go:128-143,176-180`、测试 `pi_login_test.go:258-263` 明确矛盾。
- **最小修复方向**：改为“模型级共享，保存时全组对齐”，引用实际 alignPiLoginContextWindowLocked。无需实现改动。

## 验证状态与剩余边界

以下要求命令均因**无命令执行工具**而未运行，退出状态未知，未构造替代执行通道：

1. cwd `/Users/run.mao/workpace/amagi-codebox`：`export PATH="$HOME/.local/go/bin:$PATH"; GOFLAGS=-mod=vendor GOTOOLCHAIN=local go test ./internal/config/ ./internal/launcher/ ./internal/piconfig/ . -count=1`。
2. cwd `/Users/run.mao/workpace/amagi-codebox/frontend`：`npx vue-tsc --noEmit && npx vitest run`。

Leader 提供的真实 pi-1.1.0 ModelRuntime 加载 zai/glm-5.2→400000 成功作为外部输入记录；任务不要求本轮重跑，且本轮未读取该实测日志，不把它写成本轮独立证据。

保留而不重报未变面：固定 root 不冻结已启动进程的 model snapshot；LaunchPiSession 仍直接写先前解析的 loginPreset.ContextWindow（`app.go:3536-3538`），不是本次 reconcile 化的回调，不能把回调的收敛证明扩大成所有并发启动/保存的全局线性一致性保证。模型切换时旧 model override 的生命周期、导入旧预设不一致、跨进程写盘均未在本轮新增修复承诺内全面审计。

建议 Leader：修 N01 的说明文字；结合已有实测证据与要求的 Go/前端运行结果决定集成验收。本报告不签发运行测试通过结论。
