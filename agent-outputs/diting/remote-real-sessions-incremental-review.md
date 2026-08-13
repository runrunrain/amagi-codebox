# Remote real sessions FAIL 修复增量复审

- VERDICT: FAIL
- 审核结论：不通过
- 审核模式：只读增量复审；不重审已通过的 SessionAuthority 基础；未修改源码、配置或测试
- HEAD: `007bce80ce9ba4c1c8f9c0c87fdc97a912fc40b9`
- 基线 finding：`agent-outputs/diting/remote-real-sessions-final-review.md`
- 修复报告：`agent-outputs/luban/remote-launch-planner-report.md`

## 增量 diff 基线

按修复报告锁定以下增量及必要上下文；当前工作树未提供可直接与终审 fingerprint 做二进制相减的独立补丁，因此以下文件哈希用于本次复审批次判重。

### 修复文件与变更范围

- `app.go`（`sha256:a4a738...b808`）：生产 Executor、shared lease registrar、restart PTY start wiring。
- `internal/launchplan/types.go`（`sha256:7e7cba...f683`）：`SharedStartSpec` upstream/port、bootstrap startup command、effect validation。
- `launch_planner.go`（`sha256:cbe49c...1320`）：五 CLI bootstrap effect、Claude shared chain、Pi/Omp custom provider 与 fail-closed builder。
- `launch_executor.go`（`sha256:025e54...f48`）：shared start effects、config CAS compensation、PTY/bootstrap prepared effects、secret disposal。
- `launch_planner_executor_test.go`（`sha256:927623...23a`）：HOME 隔离、shared spies、Pi/provider、bootstrap shape、config compensation 测试。
- `internal/remote/remote_session_adapter.go`（`sha256:e5d552...1fc`）：create admission/lease/bootstrap、Authority restart、lifecycle cleanup。
- `internal/remote/shared_coordinator.go`（`sha256:d4470c...edd`）：proxy launch admission。

### 仅作为必要上下文读取

- `proxy_headroom_facade.go`：App shared lease owner/release 实现。
- `internal/pty/service.go`：Windows ConPTY shell bootstrap 时序。
- `control_wiring.go`、`internal/remote/control_gate.go`：desktop bootstrap 与 exact `RunPermit` gate 语义。

未重审 SessionAuthority membership、基础 composite activation、opaque attachment、既有 H1/H3 基础实现。

## Finding 逐项判定

| Finding | 判定 | 摘要 |
|---|---|---|
| C-01 | OPEN | 使用了 exact `RunPermit` gate，但 Windows shell bootstrap 无 ready/delay，bootstrap effect 本身被跳过，仍无“目标 CLI 已执行”证据。 |
| M-01 | OPEN | upstream/port 已进入 spec，proxy spy 成立；但 headroom fingerprint 不含真实 upstream，port 未由 executor 执行，既有运行实例配置也未校验。 |
| M-02 | OPEN | custom provider ID 与 builder fail-closed 已实现；非 OAuth 缺 key 仍可生成成功 plan，测试未联合断言真实 argv/config/key，Omp 未覆盖。 |
| M-03 | OPEN | Authority restart 不再固定 503，但绕过 Executor 全部非 process effects，旧 binding 未从 registry 释放，post-activation 失败可遗留新进程/Control run。 |
| M-04 | OPEN | 三个 Pi 基础 CAS case 通过；补偿 I/O 错误被吞、debt 未被 adapter 消费，且 Omp/Codex/mode/error path 未覆盖。 |
| M-05 | OPEN | create 前 admission 与 promotion 已接入；stop/natural exit/restart 未 exact release/transfer，owner 仅按 session ID，失败补偿仍可能误停共享 singleton。 |
| m-01 | OPEN | HOME/USERPROFILE 已隔离；新增 spy 只证明 shared `Start` 参数，未证明 Windows bootstrap、Pi/Omp argv/key 或 shared lifecycle 的真实 effect。 |

## Findings

### Critical

#### C-01 OPEN — Windows bootstrap 仍未证明在 shell ready 后真实执行目标 CLI

- 位置：`internal/remote/remote_session_adapter.go:548-552,608-620`；`internal/pty/service.go:360-371`；对照 `control_wiring.go:209-219`
- 影响：Windows remote create 仍可能在 shell 尚未初始化时立即写入 startup command，随后发布 running；目标 CLI 是否成为 child 未被确认，C-01 的 shell-only 假会话风险不能关闭。
- 证据：
  - adapter 明确跳过 `EffectBootstrapWrite`，没有调用 prepared effect 的 `ArmOwnership/Apply/RecordApplied`。
  - 随后直接调用 `DesktopBootstrap(ctx, runPermit, ...)`；`DesktopBootstrap` 最终确实进入 `DoBootstrapPTY`，因此 exact `RunPermit` gate 这一点成立，但调用发生在 ConPTY start 返回后立即执行。
  - Windows PTY 自身只在 `runHandle == nil` 时等待 1 秒再写；托管 remote run 不等待。desktop 路径同类写入有 `desktopBootstrapDelay`，remote 路径没有 ready signal、输出/prompt gate或最小等待。
  - `TestBootstrapEffectCarriesStartupCommand` 只检查 plan 字段非空；fake resolver 对五 CLI 均固定 `StartupCommand: "claude"`（`launch_planner_executor_test.go:23-32,475-491`），没有触发 adapter/gate/raw PTY，也没有断言 CLI child。
- 修复方向：bootstrap 必须成为同一 execution transaction 的真实 effect；在 exact staged run permit 下等待明确 PTY-ready 条件后写入，写失败/ready 超时必须 exact close + registry release + full compensation，且 publish 前以 Windows ConPTY runtime/effect spy 证明 startup command 已进入对应 session，而不是仅证明 plan shape。

### Major

#### M-01 OPEN — shared upstream/port/fingerprint 链仍不等价桌面

- 位置：`launch_planner.go:221-246,441-449`；`launch_executor.go:146-161,181-304`；`control_wiring.go:351-370`
- 影响：并发或既有 shared service 场景可把请求绑定到错误 upstream/config；错误 fingerprint 还会导致兼容实例被拒绝或不兼容实例被误共享。
- 证据：
  - Claude upstream 与 proxy port 已进入 `SharedStartSpec`，proxy executor 也把 port/upstream 传给 `Proxy.Start`；该子项已修复。
  - Claude headroom fingerprint 却用字面量 `"claude"`，Codex 用 `"codex"`，而桌面 owner 从 service status 的真实 `BackendURL/Port` 计算 fingerprint。计划 fingerprint 与真实运行配置不等价。
  - fingerprint helper 只是截取字符串前 32 bytes；长 URL 时端口和 URL 后缀不进入 fingerprint，存在确定性碰撞。
  - headroom effect 保存 `listenPort`，但 `Apply` 从不消费该字段；只依赖实例预配置。
  - `IsRunning()==true` 时 effect 直接成功，不核验当前 service 的 backend/port 与计划 fingerprint；lease promotion发生在 process/bootstrap 后，不能补救已经使用错误 singleton 的启动。
- 修复方向：从 `{service, exact upstream, exact listen port}` 生成完整哈希并让 admission/effect/lease 使用同一 fingerprint；effect 对已运行实例必须核验 status；headroom 端口要么由 typed executor 显式设置，要么从 spec 删除并以已验证的 immutable instance config 表达，禁止“字段存在但未消费”。

#### M-02 OPEN — Pi/Omp 缺 key 仍会生成可发布 plan，联合证据不成立

- 位置：`launch_planner.go:617-647,650-689`；`internal/launcher/pi_config.go:54-77`；`launch_planner_executor_test.go:405-445`
- 影响：非 OAuth custom provider 缺少 secret 时，remote create 仍能写出不含 `apiKey` 的 custom provider，并带 `--provider amagi-*` 启动；会发布一个确定性认证失败的 session。
- 证据：
  - config builder 成功后切换到 `PiProviderID/OmpProviderID`，builder error 返回 `FailureLaunchContext`；这两个子项已修复。
  - 但 planner 对 Pi/Omp 的 `apiKey == ""` 没有 fail-closed 判断；builder 仅在 key 非空时写 `apiKey`，空 key 不报错。
  - `TestBuildPlanPiProviderIDMatchesConfig` 只检查 config providers map；没有读取 process `CLI.Args`/`StartupCommand`，没有核对 key，也没有 Omp 对称 case。测试注释声称检查 argv，但代码未执行该断言。
- 修复方向：按 provider auth mode 区分合法无 key 与缺失 secret；custom provider 缺必需 credential 时返回 typed launch-context failure。为 Pi 与 Omp 各做 config provider ID + argv `--provider/--model/--thinking` + secret/env 联合断言，并覆盖 missing-secret。

#### M-03 OPEN — restart 不是完整 Plan/Executor transaction，且存在 orphan/divergence 路径

- 位置：`internal/remote/remote_session_adapter.go:917-1054`；`internal/processcap/types.go:157-205`
- 影响：restart 对 Pi/Omp/Codex 不重做 config mutation，对 Claude 不执行 proxy/headroom admission/effects，对 Windows 不执行 bootstrap；旧 registry entry 永久保留。若 Control 新 run 已激活后 Authority bind/commit 失败，新进程与新 registry binding不会关闭，Control/Authority 可分叉。
- 证据：
  - restart 只从 plan 提取 `ProcessStartSpec.Resolved`，直接调用 `ptyStart`；没有 `executor.Prepare/Apply/Abort`、shared admission/lease、config effect、bootstrap effect。
  - old binding 通过 `processRegistry.CloseExact` 关闭，但成功后没有 `ReleaseExact`；registry replacement 不成立。
  - `ActivateRestartRun` 在 `BindPreparedRestartResult/CommitPreparedRestart` 之前执行。后两者失败分支只 abort token + reconcile old run revision，没有 close/release new binding，也没有回滚已激活的新 Control run。
  - 当前所谓 restart 测试仍是 `TestAuthorityRestartFailsClosedWithoutReplacingBinding`，只验证缺 wiring 时返回 503；没有 production Authority success、same ID、H1/H3、processcap replacement、stale receipt与失败补偿的联合测试。
- 修复方向：restart 复用与 create 同一 typed execution transaction；old exact close 后释放 exact old registry key；新 config/shared/process/bootstrap 全部在 staged run 上完成，再以 Authority guard 做 no-fail composite switch。任何 activation 后失败必须有新 binding exact close、registry release、shared/config compensation和 generation-bound reconciliation。

#### M-04 OPEN — CAS 基本语义已补，但 compensation debt 不可观测且恢复失败被伪装成功

- 位置：`launch_executor.go:320-460,578-605`；`internal/remote/remote_session_adapter.go:568-573,650-675,693-713`
- 影响：删除、临时文件写、chmod、rename 失败时 Abort 仍可能报告成功；用户配置未恢复却没有持久 debt/日志/状态，违反失败诚实性。
- 证据：
  - existed/nonexistent/concurrent-edit 三个 Pi case 均通过；current digest 与 written digest 不一致时不会覆盖外部编辑，原不存在时会删除，原 mode 被记录。这些子项已修复。
  - `os.Remove`、`os.Chmod`、`os.Rename` 错误全部被忽略；fixed `configPath + ".amagi-tmp"` 也不是并发事务唯一 temp。
  - `CompensationDebts()` 只在 executor 内定义，生产 adapter 对 `execution.Abort(ctx)` 返回的 `CompensationReport` 与 debt 都不读取、不记录、不持久化。
  - 新测试只覆盖 Pi，未覆盖 Omp YAML、Codex TOML、mode restore、I/O failure/debt propagation。
- 修复方向：补偿每一步返回 typed outcome；使用同目录唯一 temp + sync/rename 结果检查；失败写入可查询/可重试 debt owner。adapter 必须消费 Abort report/debt，并在 API/journal/运行状态中保守呈现，不得丢弃。

#### M-05 OPEN — shared lease 未跨 composite/lifecycle exact 移交

- 位置：`internal/remote/remote_session_adapter.go:516-544,636-672,917-1054,1176-1194`；`app.go:230-234,457-461`；`proxy_headroom_facade.go:82-92,990-1005`
- 影响：remote stop 与 natural exit 后 lease 长期残留；restart 既不释放旧 generation，也不获取新 generation。失败 create 还会在 App owner 留下 stale lease pointer；并发失败事务可停止另一成功 run 正在使用的 singleton。
- 证据：
  - create 在 shared effects 前获取 admission，并用 `AcquireForRunWithAdmission` promotion；proxy admission也已允许。这些子项已修复。
  - registrar 在 composite prepare/commit 前把 leases 写入 App；后续失败只在 coordinator `releaseLeases`，不从 App map 删除。
  - App owner map key 只有 session ID，不含 run generation，与注释“session+run-generation owner”不符。
  - Authority stop 成功分支直接返回 detail，没有调用 `releaseShared`；remove 只在 GC 最终释放。remote PTY natural exit没有对应 App release hook。
  - restart 完全不处理 shared admissions/leases，因此旧 lease遗留且新 recipe/config没有 exact lease。
  - `headroomStartEffect/proxyStartEffect.compensate` 仅凭 `startedByMe` 直接 `Stop()`；未向 coordinator确认是否已有其他 promoted lease，存在失败事务误停共享服务的窗口。
- 修复方向：lease owner必须以 `{sessionID, runEpoch, kind}` exact key 管理，在 composite commit 中原子转移；stop/restart/remove/exit按 exact generation释放。失败前若已临时登记必须撤销 owner记录；shared service stop compensation必须在 coordinator 证明“本事务启动且当前无人使用”后执行。

### Minor

#### m-01 OPEN — HOME 已隔离，但测试仍主要验证 shape 而非真实 effect

- 位置：`launch_planner_executor_test.go:23-55,329-401,405-491,497-585`
- 影响：测试全绿仍不能发现 C-01、M-02、M-03、M-05 的生产接线/时序问题。
- 证据：公共 setup 设置 `HOME` 与 `USERPROFILE`，本次定向测试在外层临时 HOME 下运行；环境隔离子项已关闭。
- 证据缺口：fake resolver 对全部 CLI 固定 `/usr/bin/echo` + `StartupCommand: "claude"`；shared spy 绕过 adapter/gate/composite；Pi test不查 argv/key；无 Omp 对称 case、Windows ConPTY runtime/bootstrap spy、Authority restart success、shared stop/restart/remove/exit tests。
- 修复方向：保留 HOME 隔离，增加能走生产 adapter/executor/gate 的 effect spy与 Windows runtime证据；每个测试映射真实风险，删除误导性注释或补足其断言。

## 本增量新增 Critical/Major 检查

- 未发现独立于 C-01/M-01～M-05 的新 OWASP Critical/Major；无前端改动，浏览器验证不适用。
- 但不能确认“本增量没有引入 Critical/Major”：新增 `authorityRestart` 的 post-Control-activation failure 分支会遗留新进程/registry binding并造成 Authority/Control 分叉，属于 M-03 内的 Major 级增量缺陷；shared compensation/owner接线也形成 M-05 内的 Major 级增量缺陷。
- 安全审计：未发现置信度 >80% 的新增注入、认证绕过、访问控制或 secret 日志泄露问题。

## 定向验证

所有 Go 运行测试均设置临时 `HOME` 与 `USERPROFILE`，未触碰真实用户配置。

1. `go test . -run 'Test(BuildPlanClaudeProxyHeadroomUpstreamPort|ExecutorHeadroomProxyStartWithUpstreamPort|BuildPlanPiProviderIDMatchesConfig|BuildPlanPiBuilderFailureFailsClosed|BootstrapEffectCarriesStartupCommand|ConfigMutationCASExisted|ConfigMutationCASNonexistent|ConfigMutationCASConcurrentEdit|DisposeSecretsZeroesEnv)$' -count=1`：PASS。
2. `go test ./internal/remote -run 'Test(AuthorityRestartFailsClosedWithoutReplacingBinding|R4_001_LateReconcile_DoesNotClobberNewerRun|N002_StaleLifecycleCompletionDoesNotCommit|M004_RestartExactRunTransition_NonNilPermit|M004_RestartSealsOldSegment_DropsLateOldRun)$' -count=1`：PASS；其中 Authority test只验证缺 wiring fail-closed，不能证明新 production restart。
3. `go test -race . -run 'Test(ExecutorHeadroomProxyStartWithUpstreamPort|ConfigMutationCASConcurrentEdit)$' -count=1`：PASS。
4. `go test -race ./internal/remote -run 'Test(R5_002_LaunchAdmissionVsDrainConcurrentLinearization|R5_002_AdmissionFirstBlocksDrainAndPromotesAtomically|SharedServiceCoordinator_IncompatibleConfigRejected)$' -count=1`：PASS；只证明 coordinator primitive，不证明 adapter ownership/lifecycle接线。
5. `git diff --check -- <增量文件>`：PASS。
6. 首次使用 `GOOS=windows ... go test ... -run '^$'` 在 macOS 尝试执行 Windows test binary，原始结果为 `exec format error`，归因为命令参数/交叉执行方式，不是代码失败；改用 `go test -c` 后 `./internal/pty`、`./internal/remote`、根包均 Windows compile PASS。

## 结论

VERDICT FAIL

C-01、M-01、M-02、M-03、M-04、M-05、m-01 均未达到 CLOSED。已有单测通过只证明局部 plan shape、shared Start 参数、Pi 基础 config rollback和 coordinator primitives；不能抵消静态确定的 restart transaction缺口、shared lifecycle泄漏、compensation debt丢失与 Windows bootstrap ready缺口。

建议回流原实现方按上述七项做一次完整修复批次；本次为允许的唯一增量复审，按审核循环规则不再自动发起第三轮 diting，是否升级或继续由 Leader 决定。
