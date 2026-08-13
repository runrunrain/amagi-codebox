# Remote real sessions 7 项 OPEN finding 最终闭环实施报告

- 状态：completed
- 结论：C-01、M-01、M-02、M-03、M-04、M-05、m-01 全部完成实现闭环
- 模式：融合模式（架构、并发状态机、平台 PTY、凭据安全、配置事务、测试证据）
- 基线 HEAD：`007bce80ce9ba4c1c8f9c0c87fdc97a912fc40b9`
- 一句话问题定义：让 remote create/restart 在 Authority 发布前完成同一套可补偿的配置、共享服务、exact PTY ready/start/bootstrap 与 lease 事务，并让所有失败路径保持 publish=0、无 ABA 误写和可观测补偿债务。

## 1. 上游依据与失败历史

### 上游 artifact

- `agent-outputs/diting/remote-real-sessions-final-review.md`
- `agent-outputs/diting/remote-real-sessions-incremental-review.md`
- `agent-outputs/luban/remote-launch-planner-report.md`
- 本文件修订前的 P0 实施报告

### 已知失败历史

1. 初版 remote create 跳过真实 `EffectBootstrapWrite`，ConPTY shell 启动后立即发布 running，无法证明目标 CLI 已执行。
2. shared fingerprint 曾截断字符串，未覆盖真实 upstream/port；Headroom 端口未消费，既有实例未做 exact status 校验。
3. Pi/Omp custom provider 缺 credential 仍可产出 plan，且测试只验证 config shape。
4. Authority restart 曾只提取 process spec 直接启动，绕过 config/shared/bootstrap，旧 registry binding 未释放，Control 可先于 Authority 激活。
5. config rollback 吞掉 chmod/remove/rename/sync 错误，没有 query/retry debt。
6. shared lease owner 只按 SessionID 管理，stop/restart/remove/natural-exit 无 exact generation transfer/release，失败事务还可能误停 singleton。
7. 前次测试缺少 adapter→executor→gate→PTY 的生产链证据。

## 2. 候选方案与决策

| 方案 | 兼容性 | 迁移成本 | 可逆性 | 长期维护 | 结论 |
|---|---:|---:|---:|---:|---|
| A. 在旧 adapter 上逐点补写 bootstrap、config、lease | 低 | 中 | 中 | 低 | 排除。create/restart 仍有不同提交边界，无法证明 publish 前原子性。 |
| B. create/restart 统一复用 typed Planner/Executor，并增加 prepared composite restart、exact lease transfer、typed compensation debt | 高 | 高 | 高 | 高 | 采用。所有 fallible effect 在 Authority publish 前完成，最终 callback 只做预备状态提交。 |
| C. 激活新 Control run 后再补 Authority/config/shared | 中 | 低 | 低 | 低 | 排除。任一后置失败都会产生 orphan process 或 Authority/Control divergence。 |

关键决策：

- create/restart 共用 `launchplan.Plan`、`PreparedExecution` 和 canonical effect order。
- `BootstrapWrite` 只在 `BootstrapShellAttach` 写入；direct/shell-inline 已经执行目标 CLI，保留显式 no-op effect，禁止重复向 CLI stdin 写启动命令。
- Windows shell-attach ready 必须同时满足 exact binding、read/wait pumps armed、进程未退出、首次 shell 输出已观察。
- shared admission 在副作用前绑定 exact `{service, upstream, port}` fingerprint；不同 config 的并发事务在 effect 前拒绝。
- restart 的唯一发布点是 Manager callback 内的 composite commit；旧 binding 在新事务开始前 exact close/release，新 binding 仅在完整准备后发布。
- config compensation 采用 preimage/written digest CAS、同目录唯一 temp、file sync、rename、directory sync和 typed debt；外部改写永不覆盖。
- shared owner 精确到 `{sessionID, runEpoch, kind}`，restart 两阶段 transfer，stop/remove/exit 按 exact generation 释放。

## 3. 根因与证据链

### 根因 1：启动成功被错误等同于“PTY 对象已创建”

证据：旧路径跳过 bootstrap effect；Windows shell-attach 没有 shell-ready 信号。修复后，`internal/pty/readiness.go` 提供平台共用 readiness 状态机，Windows concrete service 在首个 ConPTY 输出时关闭 `shellReady`；executor 等待 exact ready 后才调用 `WriteRawForBinding`。ready/write 失败由 effect 自身 exact close，避免未被 `RecordApplied` 的 partial Apply 绕过补偿。

### 根因 2：shared config 身份没有贯穿 admission→effect→lease

证据：旧 fingerprint 可由字符串边界和截断碰撞，且 admission 只含 kind。修复后使用长度前缀二进制 tuple 的 SHA-256；`AcquireLaunchAdmissionForConfig` 在副作用前拒绝不同 fingerprint 或 unbound 冲突；effect 校验运行实例 `BackendURL/Port`，Headroom 显式 `SetPort`，Codex 使用 `StartForOpenAI`。

### 根因 3：restart 是 process-only 操作，不是完整 launch transaction

证据：旧实现没有 config/shared/bootstrap，且激活顺序先 Control 后 Authority。修复后 `authorityRestart` 完整执行 BuildPlan、admission、Executor、config/shared/PTY/bootstrap、registry、pending lease、prepared H1/H3/projector、Authority bind，再执行 no-fail composite commit。失败保留旧 run revision、Authority 收敛 unavailable、新 binding exact close、registry/lease/config 全部补偿。

### 根因 4：配置补偿只处理理想文件系统

证据：旧 fixed temp 和被忽略的 I/O 错误无法区分“未替换”“已替换但 durability 不确定”。修复后 `atomicConfigWriteResult.Replaced` 与 `configIOError.step` 精确投影 mkdir/chmod/create/write/sync/close/rename/target-chmod/directory-sync；debt registry 暴露 secret-free query/retry API，并在 unavailable/indeterminate 时保守保留 owner。

### 根因 5：共享资源所有权缺少 run generation

证据：SessionID-only map 会被旧 natural-exit 回调释放新 run。修复后 coordinator 和 App map 使用 exact owner；pending lease 只在 Authority composite callback promotion；旧 exit、失败 transaction、并发 restart 都不能释放 replacement generation。

## 4. Finding 闭环矩阵

| Finding | 状态 | 实现证据 | 测试证据 |
|---|---|---|---|
| C-01 | CLOSED | `waitExactPTYReadiness`；Windows first-output shell signal；`WaitReadyForBinding`、`WriteRawForBinding`；bootstrap 仅 shell-attach；timeout/失败 exact close | 平台共用 readiness 测试；Darwin exact PTY runtime；Windows exact shell-attach target-CLI 测试已编译；生产 create ready/write failure publish=0 |
| M-01 | CLOSED | exact tuple SHA-256；config-bound admission；运行实例 upstream/port 校验；Headroom `SetPort`；Codex `StartForOpenAI` | tuple 边界/长 URL/port/service 防碰撞；running Headroom mismatch；shared restart verification failure |
| M-02 | CLOSED | custom provider 缺 key typed fail；OAuth 仅走 built-in 且清理 inherited credential/base URL；Pi/Omp config/argv/env 一致 | Pi/Omp joint plan；missing key resolver calls=0；OAuth keyless；production adapter create+restart config/argv/env/bootstrap |
| M-03 | CLOSED | restart 复用 Planner/Executor；old exact registry replacement；prepared composite restart；Authority 最终发布 | create/restart success；config/shared/PTY-start/ready/bootstrap 故障；pre-commit exit；boundary→staged FIFO；post-restart lifecycle；publish=0 |
| M-04 | CLOSED | unique atomic temp、mode restore、file/dir sync、typed outcomes、query/retry debt API、secret disposal | 10 个 I/O step fault；Pi/Omp/Codex CAS+mode；external edit；unavailable/indeterminate debt query/retry；partial Apply compensation |
| M-05 | CLOSED | exact owner key；config-bound admission；pending→promoted transfer；stop/remove/exit exact release；coordinator-only compensating Stop | App callback create/restart/exit；stop→restart；stop→remove；并发 old exit/restart；exclusive compensating Stop |
| m-01 | CLOSED | 生产 adapter→executor→gate→PTY harness；Windows/macOS platform tests；真实 App lease callback | Pi/Omp production joint evidence；create/restart fault matrix；shared lifecycle；Windows/Linux test compile；Darwin PTY runtime |

## 5. 实现摘要

### 5.1 PTY 与 bootstrap

- 新增 exact binding ready/write port。
- Windows `PtySession` 增加 pumps-ready、process-exited、shell-output barriers。
- 平台共用 readiness 状态机拒绝 pre-ready exit、shell-ready 前退出和 context timeout。
- bootstrap write 使用 captured `BindingID`，延迟写不会命中复用 SessionID 的 replacement。
- `ptyStartEffect` 对 validation/ready partial failure自行 exact close；indeterminate close会继续由 `PreparedExecution.Abort` 重试并计入 failed report。
- direct/shell-inline 的 bootstrap effect为空操作，避免目标 CLI已直接启动后再次把命令写入 stdin。

### 5.2 Shared config 与 lease

- fingerprint 序列化为长度前缀 service/upstream 与固定宽度 port，再计算 SHA-256。
- admission 绑定 fingerprint；同 kind 不同 config 或 exact/unbound 交叉事务在副作用前拒绝。
- running Proxy/Headroom 必须 exact status 匹配。
- `SharedLeaseOwnerKey` 包含 SessionID、RunEpoch、Kind。
- restart transfer 在 coordinator 中冻结 old/new lease；commit 同时删除 old、promote new、更新 App owner map；Finish 才开放 blocked exact release。
- shared Stop 补偿必须证明本 transaction 启动 singleton，且无 promoted/竞争 pending owner。

### 5.3 Credential 与配置事务

- Pi/Omp custom provider 必须有 credential；缺失时在 resolver/process planning 前失败。
- OAuth 不生成 custom config，并删除继承的 API key、auth token和base URL。
- Pi/Omp process args、startup command、config provider ID、model、thinking、env credential来自同一 plan。
- config write 使用同目录随机 temp、权限、write、file sync、close、rename、target mode和directory sync。
- compensation 先验证 current digest仍等于 written digest；若 preimage 已恢复但 durability 未确认，会重做 exact restore，而不是误判外部编辑。
- App 暴露 `GetLaunchCompensationDebts` 与 `RetryLaunchCompensationDebt`；projection 不含配置内容或 credential。
- `DisposeSecrets` 清空 plan buffers、PTY env和派生 config candidate；未解决 debt 仅私有保留 retry preimage，confirmed 后立即清零。

### 5.4 Restart composite

- old exact binding close并从 registry release后，new run保持 hidden。
- config/shared/process/bootstrap 全部完成后注册 new binding和pending lease。
- `PreparedCompositeRestart` 预分配 H1 records、H3 reservations和projector barrier。
- Manager callback 内提交 Control/H1/H3、prepared control owner和shared transfer；之后统一 Finish。
- 修复空 staged FIFO 时 projector barrier未打开的死锁：即使 staged=0，Finish也关闭 flush barrier。
- stop 保留已经关闭的 exact capability，允许后续 Remove/Restart；shared lease立即释放。Remove最终释放 registry capability。

## 6. 主要改动文件

- `app.go`
- `control_wiring.go`
- `docs/api.md`
- `config_sync_unix.go`
- `config_sync_windows.go`
- `launch_planner.go`
- `launch_executor.go`
- `launch_planner_executor_test.go`
- `remote_production_transaction_test.go`
- `proxy_headroom_facade.go`
- `internal/launchplan/compensation.go`
- `internal/launchplan/types.go`
- `internal/pty/readiness.go`
- `internal/pty/readiness_test.go`
- `internal/pty/service.go`
- `internal/pty/service_darwin.go`
- `internal/pty/service_other_stub.go`
- `internal/pty/service_test.go`
- `internal/pty/service_darwin_test.go`
- `internal/remote/control_arbiter.go`
- `internal/remote/control_gate.go`
- `internal/remote/control_runtime.go`
- `internal/remote/restart_activation.go`
- `internal/remote/remote_session_adapter.go`
- `internal/remote/run_continuity.go`
- `internal/remote/shared_coordinator.go`
- `internal/remote/session_authority_integration_test.go`

未修改 `mobile/`、`frontend/`、`frontend/wailsjs/`；未 commit；未编辑或回滚既有 `.claude/*` 用户现场。

## 7. 验证证据

### PASS：最终全仓

```text
HOME=<temp> USERPROFILE=<temp> XDG_CONFIG_HOME=<temp> go test ./... -count=1 -timeout=300s
```

结果：全部 package PASS。仅有既有 macOS Security.framework Keychain deprecated 编译警告。

### PASS：race

```text
HOME=<temp> go test -race . ./internal/launchplan ./internal/processcap ./internal/pty ./internal/remote ./internal/session -count=1
HOME=<temp> go test -race . ./internal/pty ./internal/remote -count=1
HOME=<temp> go test -race . -run 'Test(ConfigCompensation|ConfigMutationCASConcurrentEdit|ExecutorAbortRetries)' -count=1
```

结果：全部 PASS；后两条分别覆盖最终 readiness/coordinator 与同步 compensation-debt 修订。

### PASS：静态检查

```text
gofmt -w <本批 Go 文件>
git diff --check
HOME=<temp> go vet ./...
```

结果：PASS；vet 仅输出既有 Keychain deprecated warning。

### PASS：异平台编译

```text
HOME=<temp> GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -exec=/usr/bin/true ./... -run '^$' -count=1
HOME=<temp> GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -exec=/usr/bin/true ./... -run '^$' -count=1
```

结果：全部 package PASS。最终增量另对根包、`internal/pty`、`internal/remote` 再次编译 PASS。

### 诊断记录

未隔离 HOME 的一次 `go test ./...` 被 `TestDarwinResolveExecutableFindsCodexInChatGPTAppBundle` 中真实 `~/.local/node/bin/codex` 抢先命中，只有该无关用例失败。按任务要求切换隔离 HOME 后同一全仓命令 PASS，证明失败来源是测试宿主污染，不是本批代码回归。

## 8. 被排除假设

- “所有 bootstrap effect 都应写命令”：错误。direct/shell-inline 已执行 CLI，再写会污染 stdin；仅 shell-attach需要写。
- “read/wait goroutine已启动就等于 Windows shell ready”：错误。必须等待 exact ConPTY 首次输出。
- “同 kind admission 足以保护 singleton”：错误。必须在副作用前绑定 exact config fingerprint。
- “Apply 返回 error 就一定没有副作用”：错误。Start/rename/ready/write都可能 partial；Abort必须检查 effect内部 ownership，而非只依赖 `RecordApplied`。
- “restart 可先激活 Control 再绑定 Authority”：错误。后续任何失败都会造成 divergence。
- “Stop 应立即丢弃 closed binding”：错误。这样 Stop 后无法 Remove/Restart；正确做法是释放 run lease但保留 exact closed capability到 replacement/remove。
- “CAS mismatch 可强行恢复原文件”：错误。可能覆盖用户并发编辑，必须保留 indeterminate debt。

## 9. 未覆盖风险

1. 【待核验】当前宿主是 macOS arm64，不能实际执行 Windows ConPTY 测试二进制；Windows exact shell-attach target-CLI 测试已实现并通过 Windows 编译，平台共用 readiness 状态机、生产 transaction spy和Darwin exact PTY runtime均已实际执行。
2. 未使用真实第三方 provider credential启动五种外部 CLI；为避免触碰用户账户，本批以真实 planner/config转换加 production adapter fake PTY验证参数、配置、credential和时序。
3. 未做 frontend/mobile 浏览器验证，因为任务明确禁止修改该范围，且本批无 UI diff。
4. 不再请求 diting；这是用户明确指定的最终修复轮次。最终是否提交由 Leader 决定。

以上风险不改变 7 个 finding 的代码闭环；其中 Windows 真实宿主 smoke 是发布环境验证项，不是继续保留旧路径或伪成功的理由。

## 10. 回滚方式

禁止使用破坏性 Git 命令。若 Leader 决定回滚，按以下依赖逆序整组撤销：

1. App API/owner wiring：`app.go`、`proxy_headroom_facade.go`、`docs/api.md`。
2. adapter/composite/coordinator：`internal/remote/remote_session_adapter.go`、`restart_activation.go`、`control_gate.go`、`control_runtime.go`、`shared_coordinator.go`。
3. executor/planner/config debt：`launch_executor.go`、`launch_planner.go`、`internal/launchplan/*`、`config_sync_*`。
4. PTY exact readiness/write：`internal/pty/readiness*`、Windows/Darwin/stub service改动。
5. 对应测试文件。

不得只回滚 interface 一侧；否则会留下 Planner/Executor、PTY port或lease owner不匹配。`.claude/*`、frontend/mobile和用户配置不在回滚范围。

## 11. 建议下一步

1. Leader 复核本报告与工作树边界后决定是否提交。
2. 在真实 Windows 10/11 发布机执行 `TestExactShellAttachReadyThenBootstrapExecutesTargetCLI` 和一次五 CLI smoke。
3. 提交前保持当前 diff 不再进入第二次审核循环；用户已明确本轮不再请求 diting。
