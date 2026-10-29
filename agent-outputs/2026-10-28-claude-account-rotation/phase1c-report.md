# Phase 1c 实现报告：Provider center「Claude 订阅账号」区

任务：Phase 1c 前端账号区。上游：design.md §4.1/§7.1/§7.2（主树 agent-outputs 绝对路径读取）、phase1b-report.md §四（worktree 内接口清单）、docs/developer/frontend-backend.md。
实现位置：worktree `amagi/claude-account-rotation`（HEAD 6024fb7 = 1a+1b，开工时已核验）。按 [amagi worktree] 机器注入段**未 commit**，改动留工作区由 Leader 汇总（任务书正文「worktree 分支提交」与此冲突，按较新注入段执行——与 1a/1b 同例）。

## 一、交付物清单（文件 → 内容）

| 文件 | 内容 |
|---|---|
| `frontend/src/api/claudeAccounts.ts`（新增） | 包装 1b facade 十个绑定（参数顺序逐一与 d.ts 核对）；16 个错误码（1b reason.go 15 个 + OPERATION_NOT_FOUND）→ 中文文案映射表；`parseClaudeAccountError`（"CODE: detail" 解析，普通文本不误判）/`claudeAccountErrorMessage`；`CLAUDE_ROTATION_RISK_ACK_VERSION='claude-rotation-v1'`；callApi 惯例 |
| `frontend/src/stores/claudeAccounts.ts`（新增） | pinia setup store：池快照/三态；`claude-accounts:operation|event` 事件订阅（幂等桥）+ operation 轮询兜底（1s×150 与后端 2min 超时对齐）；operation 进行中防重（busy 抛 `POOL_LOCKED: 原因`）；switch 同目标在途复用同一 promise（同一 requestId，不重发第二笔）；SESSIONS_ACTIVE/EXTERNAL_SESSION_UNCERTAIN → pendingSwitch 等待意图（retry 前先 refresh 拿新代际 + 新 requestId）；录入向导状态机（begin→login→capture→done/error）；别名校验 1..64 |
| `frontend/src/components/provider/claudeAccountsView.ts`（新增） | 纯视图模型：deriveSectionState 八态、grant 健康五值+未知兜底、identity 状态、emailMaskDisplay 显隐不变量（只显示后端遮罩值或占位符，**永不构造完整邮箱**）、operationStatusText（检查登录/等待 Keychain/正在回收/正在切换/等待会话退出/成功/错误码文案）、pendingSwitchText（autoRetry 语义）、reconcileActionOptions（journal→complete-target/restore-source 优先；外部换号→adopt-current 优先）、inspectSummary、ENROLLMENT_STEPS 四步、accountCardActions（活动账号禁移除） |
| `frontend/src/components/provider/ClaudeAccountsSection.vue`（新增） | 账号区组件：absent 首次保存引导；enrollment 向导（§4.1 三步展示+身份不一致错误展示+取消确认）；managed 账号卡（别名/遮罩邮箱显隐/套餐/CC+Pi+identity 健康/活动标记/额度占位「额度显示将于后续版本提供」）+ 切换/更新当前登录（确认弹层）/重命名/移除（确认弹层）；pending 等待面板（SESSIONS_ACTIVE 自动重试：组件轮询 getSessions 至运行会话=0 后重试；EXTERNAL_SESSION_UNCERTAIN 手动重试）；needs-reconcile 自动 inspect + 三选一执行；blocked/error（保留上次值）横幅；自动轮换开关（首次开启 Dialog 风险确认：使用约束+非官方机制+token 失效三项披露 + 「自动切换引擎于后续版本生效」） |
| `frontend/src/components/provider/ProviderGrid.vue`（修改 +4 行） | 在添加提供商弹窗后挂载 `<ClaudeAccountsSection />`（API/登录卡片区下方独立区；capability=unsupported 时组件自隐藏） |
| 测试 ×3（新增，59 用例） | `__tests__/api/claudeAccounts.test.ts`（12）、`__tests__/components/provider/claudeAccountsView.test.ts`（29）、`__tests__/stores/claudeAccounts.test.ts`（18） |
| `CHANGELOG.md`（修改 +1 条） | [Unreleased] Added 完整条目 |

修正计数：api 12 + view 29 + store 18 = 59 用例（vitest 实测 `Tests 59 passed`；此前摘要写 29/47 系笔误，以测试输出为准）。

## 二、状态机说明

- **deriveSectionState**：`loading`（未完成首载，不闪现未录入）→ `hidden`（capability=unsupported，或 GetClaudeAccounts 抛 UNSUPPORTED_PLATFORM——store 合成 unsupported 快照）/ `absent`（disabled/无池）/ `managed`（enabled）/ `enrolling` / `needs-reconcile` / `blocked`（池级故障）；刷新失败且有上次数据 → `error` 保留旧值展示。
- **operation 生命周期**：facade 调用返回 operationId → store 轮询 + 事件驱动；终态 done（capture→清 enrollment、switch→清 pending、组件 toast 成功）/ error（switch+占用码+有意图 → pendingSwitch；其余错误经错误码表展示且保留上次账号数据）。
- **防重与幂等**：同步阶段 busyAction + operation running 双闸；switch 同目标在途 → 复用同一 promise/requestId（api 只调一次）；重试（pending 或手动）视为新逻辑请求 → 新 requestId + 刷新后最新 expectedGeneration（乐观锁不拿陈旧值）。
- **联动**：组件 watch `pool.activeGeneration` 变化 → `providerStore.loadLoginProviders()` 刷新发现（design §7.1）；「登录认证 · Pi」卡片零改动。

## 三、验证证据（真实命令与输出）

- `npm --prefix frontend run test` → **39 文件 / 390 用例全绿**（新增 59 用例：api 12 + view 29 + store 18；既有套件无回归）
- `npm --prefix frontend run build`（= `vue-tsc --noEmit && vite build`）→ exit 0，`✓ built in 395ms`，vue-tsc 零错误
- wails CLI：`command -v wails` 未命中 → 按任务书沿用 1b 手写绑定；**逐方法校对** Go facade 签名 ↔ `App.d.ts`：10/10 参数顺序与类型一致（GetClaudeAccounts()↔():Promise<PoolDTO>、Capture(string,string,uint64)↔(s,s,n):Promise<string>、Switch(s,uint64,s)↔(s,n,s):Promise<string>、Reconcile(s,uint64)↔(s,n):Promise<ResultDTO> 等）；models.ts 七个 DTO 字段与 Go JSON tag 一致（1b 已核对，本次消费侧复核）
- piAdvisor 相关文件：`git status` 确认零触碰（主树在途工作不受影响）
- `frontend/wailsjs/`：本次零改动（只消费）

## 四、设计映射与偏差声明

1. **遮罩邮箱「显隐」**（任务书 B「hover/点击显隐」）：design §1.3-5 规定完整邮箱不出后端，DTO 只有 `emailMasked`。实现为「显示/隐藏」切换后端遮罩值本身（隐藏时占位符），**不伪造完整邮箱**；测试锚定该不变量。若主上要求可见完整邮箱，需后端先放宽契约，前端不越界。
2. **「更新当前登录」** 映射 `ReconcileClaudeAccounts('adopt-current')`：1b facade 十方法未暴露 HarvestActive；AdoptCurrentLogin 对「原生仍属活动账号」场景 = 校验+回收快照（语义即更新当前登录），对「原生已换成池内另一账号」= 采用该账号（组件确认弹层已明示此行为）。若希望两语义分离，需后续版本在 facade 增加 harvest 入口。
3. **提交方式**：按 [amagi worktree] 注入段不 commit（见页首）。
4. **切换成功 toast**：组件 watch operation running→done 触发；池 refresh 后代际变化触发 loadLoginProviders。
5. pending 自动重试触发面：轮询 `getSessions` 至「运行中会话=0」；比后端 lease 面（仅 Claude/Pi）保守——方向安全，后端各闸仍会复核。

## 五、测试锚点（对应任务验收）

- 空池→录入→A/B 卡→切换 pending→成功→需恢复：`stores/claudeAccounts.test.ts`「全链路」用例（真实动作序列：capture('')→begin→capture(enr)→IDENTITY_MISMATCH 不清 enrollment→done→switch(gen3)→SESSIONS_ACTIVE pending→retry(新 requestId+gen4)→done 清 pending→needs-reconcile）
- 错误码全集中文映射：api 测试遍历 16 码非空中文且不含码原文 + 无多余码
- requestId 幂等：并发同目标两次 → api 一次、同一 opId；busy 时不同目标/录入/轮换全拒（负向）
- 三态渲染：view 测试 deriveSectionState 全分支正负配对（含 error 保留上次值）
- 遮罩邮箱显隐：view 测试三条（显/隐/不构造完整邮箱不变量）
- operation 文案全集、pending 两码语义、恢复三选一、移除限制、别名校验均有正负配对

## 六、未覆盖项 / 风险

1. **未验证（无真实后端环境）**：Wails 事件桥在真机的 operation 事件到达时序（轮询兜底已设计）、GetClaudeAccounts 的 Keychain 读延迟对首载的影响、真实 Keychain 授权弹窗期间 UI「等待授权」文案的可感性。属 Phase 1 真机 Gate 范畴。
2. 视觉走查按任务书明确不在本阶段（每个状态真实可触发，无假数据渲染——额度条为占位文案）。
3. `GetClaudeAccounts` 的 `PoolDTO.operation` 字段 1b facade 实际未填充（store 已按空值处理，轮询承担跟踪）；如后续后端填充，前端自动并入。
4. E2E（浏览器级组件渲染）仓库前端测试基建为 node 纯逻辑（vitest environment:'node'，无 DOM），组件交互逻辑全部下沉到 store/view 模型测试；.vue 模板正确性由 vue-tsc + vite build 闸门保证。

## 七、Phase 1 集成验收清单建议（供 Leader 做 Phase 1 Gate）

1. `go vet ./...`、`go test ./... -count=1`（1b 基线 48 包）、`npm --prefix frontend run test`、`npm --prefix frontend run build` 全绿。
2. wails CLI 可用环境跑一次 `wails build`（或 `wails dev`）再生 wailsjs 绑定，diff 确认与 1b 手写产物语义收敛（预期仅格式差异）。
3. 隔离链路冒烟：fake Keychain/临时 HOME 下走 UI 全路径（保存 A→enroll B→切换 A↔B→needs-reconcile 恢复三动作），对照 `phase1b-report.md` §四错误码逐一在 UI 验证中文文案。
4. design §10.2 真机验证项（profile 契约、CC 刷新常量、Keychain locator、pi-black 身份缓存）——沿用 1a/1b【待实测】清单，Gate 前必须完成。
5. 敏感面扫描：UI 快照/事件 payload 不含完整邮箱、token、hash（facade 层已强制，前端消费侧无反例）。
6. 回归：Provider Center 既有三 tab、登录认证·Pi 卡、预设 CRUD、导出导入不受影响（本阶段仅 ProviderGrid +4 行挂载）。

## 八、回滚方式

还原 6 个新增文件（api/store/view/组件/测试×3）+ ProviderGrid.vue 4 行 + CHANGELOG 1 条即可；无 wailsjs/Go/共享契约改动，无迁移。
