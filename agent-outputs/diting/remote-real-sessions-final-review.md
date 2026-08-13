# Remote real sessions P0 合并终审

- VERDICT: FAIL
- 审核模式：只读合并终审；未修改生产代码、配置或测试
- HEAD: `007bce80ce9ba4c1c8f9c0c87fdc97a912fc40b9`
- Diff fingerprint: `sha256:78b915a950e364ece007f2f3d0a1c22003fd78210f7da899997bf7c3ce47e4d2`
- Fingerprint 口径：`git diff --binary` 排除 `.claude/**`、`agent-outputs/**`，再按路径排序追加全部未跟踪源码/测试文件的路径与原始内容
- 审核范围：23 个 tracked changed files + 17 个 untracked 源码/测试文件；排除既有用户 `.claude/*` 与 `agent-outputs/**`

## Diff 基线

### Tracked changed files 与范围摘要

- `app.go`：生产 wiring；五 CLI desktop launch 接入 Authority reservation、process binding、shared lease、defaults 记录；stop/remove/clear 路径。
- `app_test.go`：resolver 接口兼容。
- `control_wiring.go`：PTY raw adapter、embedded composite activation、desktop bootstrap、exact binding。
- `internal/launcher/service.go`：opaque process handle、环境 override、五 CLI guarded/external launch。
- `internal/pty/service.go`：Windows ConPTY run handle、managed bootstrap 分离、exact detach。
- `internal/pty/service_darwin.go`：Darwin PTY run handle、exact detach。
- `internal/pty/service_other_stub.go`：其他平台签名兼容。
- `internal/remote/control_arbiter.go`：prepared/no-fail transition、device revoke/fence/shutdown、output observation。
- `internal/remote/control_attachment.go`：opaque attachment lease、ready gate、按 device 精确 detach。
- `internal/remote/control_causal.go`：initial activation causal kind。
- `internal/remote/control_causal_hub.go`：批量预留、initial activation ticket、订阅 active gate。
- `internal/remote/control_event.go`：subscriber readiness 与 control transition enqueue。
- `internal/remote/control_gate.go`：bounded stop/restart/remove 生命周期执行。
- `internal/remote/control_runtime.go`：desktop remove、attachment handle、run projector staged/open/pump。
- `internal/remote/control_types.go`：control connection lease live 状态。
- `internal/remote/r4_003_ws_delivery_test.go`：attach 期间 control delivery 竞争测试。
- `internal/remote/remote_session_adapter.go`：Authority list/detail/create/lifecycle、Planner/Executor、composite activation、remove GC。
- `internal/remote/run_continuity.go`：run observation commit 与 live feed。
- `internal/remote/server.go`：shutdown flush。
- `internal/remote/session_journal.go`：remove commit evidence 与 archive recovery。
- `internal/remote/ws_v1_session.go`：opaque attach、outbound terminal barrier、input gate。
- `internal/session/manager.go`：Manager 委托 Authority、legacy projection、lifecycle/remove。
- `internal/settings/service.go`：remote launch defaults 与 Codex headroom settings。

### Untracked files 与范围摘要

- `internal/launchplan/defaults.go`：稳定 desktop activation defaults。
- `internal/launchplan/types.go`：Plan/Effect/Execution/secret 类型。
- `internal/launchplan/types_test.go`：plan validation 与 secret dispose 测试。
- `internal/processcap/types.go`：opaque process binding registry 与 exact close evidence。
- `internal/processcap/types_test.go`：binding/generation/exact release 测试。
- `internal/remote/initial_activation.go`：Authority + Control/H1/H3 composite activation。
- `internal/remote/initial_activation_test.go`：prepare/commit/abort/fatal-path 测试。
- `internal/remote/journal_retry.go`：committed journal completion 重试。
- `internal/remote/removal_terminal.go`：terminal/removal delivery prepare/admit。
- `internal/remote/remove_gc.go`：receipt-keyed remove GC pipeline。
- `internal/remote/session_authority_integration_test.go`：Manager/REST/WS 单一真相源集成测试。
- `internal/session/authority.go`：SessionAuthority、reservation、prepared lifecycle、tombstone/receipt。
- `internal/session/authority_test.go`：Authority 可见性、exact exit/remove、legacy wire shape。
- `launch_executor.go`：生产 Effect Executor 与补偿。
- `launch_planner.go`：五 CLI desktop-equivalent Planner。
- `launch_planner_executor_test.go`：Planner effect-shape 与最小 Executor 测试。
- `remote_session_authority_test.go`：生产 wiring 的 Manager-only Authority 断言。

## 上游 artifact

- `/Users/maorun/maorun-workpace/projects-memory/projects/amagi-codebox/agent-outputs/architect/20260804-remote-session-truth-source-audit/design-doc.md`
- `/Users/maorun/maorun-workpace/projects-memory/projects/amagi-codebox/agent-outputs/architect/20260804-remote-session-truth-source-audit/composite-commit-addendum.md`
- `/Users/maorun/.pi/agent/amagi/artifacts/b17e2019-1fdb-4f17-9e36-365979e1caa1/result.md`
- `agent-outputs/hongjun/remote-real-sessions-fix-report.md`
- `agent-outputs/luban/remote-launch-planner-report.md`

## 验收项映射

| 验收项 | 实现位置 | 当前证据 | 结论 |
|---|---|---|---|
| SessionAuthority 单一真相源，desktop/v1 同 ID | `internal/session/manager.go`、`internal/session/authority.go`、`internal/remote/remote_session_adapter.go:214-299` | `session_authority_integration_test.go`、`remote_session_authority_test.go`、全量/race 通过 | 通过 |
| 新 session 在 Authority + Control/H1/H3 全部成立前不可见 | `internal/remote/initial_activation.go`、`remote_session_adapter.go:582-639` | composite activation 测试、Authority visibility 测试 | 通过 |
| remove exact close、terminal event、journal、GC、shutdown debt | `remote_session_adapter.go:830-1005`、`remove_gc.go`、`session_journal.go` | integration/race/recovery tests 通过 | 通过 |
| WS attach 使用 opaque Authority handle/attachment lease | `control_attachment.go`、`ws_v1_session.go:751-970` | attach/revoke/race tests 通过 | 通过 |
| 五 CLI Planner/Executor 产生真实 embedded CLI | `launch_planner.go`、`launch_executor.go`、`remote_session_adapter.go:397-639` | 仅 effect-shape/fake resolver；发现 C-01、M-01、M-02 | 不通过 |
| create/restart 与 desktop 等价、失败可完整补偿 | `remote_session_adapter.go:397-639,697-832`、`launch_executor.go` | create 部分路径有 abort；发现 M-03、M-04、M-05 | 不通过 |
| frozen wire/legacy 兼容 | `internal/remote/contract/*`、`internal/session/authority_test.go:205-226` | frozen counts 10/5/8/12、legacy JSON 14 keys 测试通过 | 通过 |
| 跨平台、race、全量测试 | 目标包、全仓、darwin 本机及 Windows/Linux compile-check | 命令均成功；但无 Windows runtime/真实五 CLI E2E | 证据不足以放行 P0 |
| 生产代码无假实现/TODO/emoji | 全部纳入 fingerprint 的生产 diff | 文本扫描无命中；无 TODO/FIXME/HACK/placeholder/emoji | 通过 |

## Findings

### Critical

#### C-01 Windows 远程 create 只启动 shell，不执行目标 CLI，却可提交为 running

- 位置：`internal/pty/service.go:315-317,360-368`；`control_wiring.go:209-219`；`internal/remote/remote_session_adapter.go:498-545,582-639`
- 影响：Windows 是正式目标平台。所有带 `BootstrapShellAttach` 的远程 embedded create 都可能得到一个存活的 `cmd`/PowerShell PTY，而 Claude Code/OpenCode/Codex/Pi/Omp 从未启动。Authority、Control 与移动端会把该 shell 会话发布为真实 running session，直接违反“移动端大厅只能展示并连接真实桌面 CLI 会话”。
- 证据：managed PTY 在 `runHandle != nil` 时明确禁止内部 delayed auto-command（`service.go:363`）；desktop 路径随后通过 `StartupAutoCommand` + `DesktopBootstrap` 补发命令（`control_wiring.go:209-219`）。远程 create 的 effect loop 只 Apply Planner 提供的 effects，而五个 Planner 均未生成 `EffectBootstrapWrite`；成功后仍执行 composite activation。
- 复现：Windows 上为任一 CLI 保存带 shell 的 desktop default，调用 `POST /api/v1/sessions`，随后 attach；观察到 shell prompt，目标 CLI 进程不存在，但 REST detail 为 running。
- 精确修复：Planner 必须在 process effect 后生成携带规范化 startup command 的 `EffectBootstrapWrite`；Executor 必须通过该 session 的 opaque `RunHandle` 调用 control-gated bootstrap write，并把它纳入 Apply/Abort 顺序。不得调用裸 PTY write，不得在 process-only 后提交 activation。增加 Windows ConPTY runtime 测试，断言 CLI child 已启动而不是仅 shell 存活。

### Major

#### M-01 Claude proxy/headroom Planner 丢失真实 upstream，所有对应远程 create 确定性失败

- 位置：`launch_planner.go:218-241,757-769`；`launch_executor.go:214-269,300-311`
- 影响：任何 desktop default 含 `UseHeadroom` 或 `UseProxy` 时，远程 create 无法达到 PTY 启动；同时 Planner 计算出的 CLI 环境固定指向 5280/Headroom 端口，与 Executor 实际 `Start(0, "")` 也不一致。
- 证据：Planner 计算了 `realBackend`/`proxyUpstream`，但 `SharedStartSpec` 只携带 service + fingerprint。Executor 的 Claude headroom `resolveClaudeBackendURL()`最终固定返回空串（`launch_executor.go:257-269`），proxy 固定调用 `Start(0, "")`（`launch_executor.go:303-311`）。`HeadroomService.Start` 与 `ProxyService.Start` 均拒绝空 upstream，因此不是低概率路径。
- 复现：记录 `UseProxy=true` 或 `UseHeadroom=true` 的 Claude desktop default，远程 create 返回 `session.launch_failed/service_down`；process effect 未执行。
- 精确修复：把非秘密的 `upstream URL` 与确定的 listen port 写入 typed `SharedStartSpec`，并纳入 plan validation/fingerprint；Executor 只能消费该 spec，不得重新猜测。proxy 端口必须与注入到 CLI env 的端口完全相同。增加真实 fake-service 测试，断言 Start 收到 provider backend、proxy->headroom 链和 5280/8787 端口一致。

#### M-02 Pi/Omp 写入 custom provider 配置后仍用旧 provider 启动，且配置构建错误被静默降级

- 位置：`launch_planner.go:558-638`；desktop 对照 `app.go:2805-2806,3024-3025`
- 影响：Pi/Omp 对自定义 BaseURL/provider 的远程会话会选择错误的 built-in provider，写入的 `amagi-*` provider 配置没有被 CLI 使用；请求可能打到官方默认端点、认证失败，或与 desktop 启动结果不等价。
- 证据：`configBuilder` 成功只设置 `hasConfigMutation=true`（`launch_planner.go:612-618`），没有像 desktop 一样把 provider 改成 `launcher.PiProviderID(providerID)`；最终 argv 使用未更新的 `launchResult.Provider`（`launch_planner.go:638`）。builder 失败则直接进入 env fallback（`627-633`），把结构性配置错误伪装成可启动 plan，Probe 也会报告 available。
- 复现：创建带自定义 Anthropic/OpenAI BaseURL 的 Pi 或 Omp desktop default，BuildPlan 后检查 config mutation 中存在 `amagi-<provider>`，但 process args 的 `--provider` 仍是 `anthropic`/`openai`。
- 精确修复：config build 成功时把启动 provider 切换为与生成配置完全一致的 `launcher.PiProviderID(providerID)`；除明确支持且已验证的 built-in provider 外，builder/merge/preimage 失败必须返回 typed `FailureLaunchContext`，不得 fallback。补充 argv + written config 联合断言和 missing-secret cases。

#### M-03 生产 Authority 路径把 restart 端点固定为 service-down，旧 restart 实现成为不可达代码

- 位置：`internal/remote/remote_session_adapter.go:697-699,713-716,830-833,1173-1189`
- 影响：生产 wiring 始终注入 Manager Authority，因此所有 `POST .../restart` 都在 `authorityLifecycle` 入口直接失败；同 ID/same recipe restart、run boundary、旧进程 exact close 与新 run composite commit 均未实现。冻结 wire 仍在，但功能契约缺失。
- 证据：`lifecycle` 在 Authority 非 nil 时无条件转入 `authorityLifecycle`；该函数遇到 `LifecycleRestart` 立即返回 generic error。后面的 `restartRawEffect` 仅属于 legacy catalog 分支，生产不可达。
- 复现：对任一生产 remote-created 或 desktop embedded session 调用 restart，稳定收到 service-down，且没有新 run revision。
- 精确修复：在 Authority 分支实现同 session membership 的 prepared restart：锁定旧 binding/run revision，exact close，按 `OriginRestart` 重建 Plan/Effects，预留新 run 与 H1/H3 boundary，最后在 Authority guard 内 composite no-fail 切换；失败必须保持诚实 terminal state并补偿新 effect。删除或明确隔离不可达 legacy restart。

#### M-04 config mutation Abort 不是 exact/CAS 补偿，可能覆盖用户修改；原文件不存在时也不会删除新文件

- 位置：`launch_executor.go:340-439`
- 影响：远程 create 在配置写入后、后续 process/activation/lease 任一步失败时，Abort 可把用户在并发窗口内的新修改无条件覆盖为旧 preimage，造成 `~/.codex/config.toml`、`~/.pi/agent/models.json` 或 `~/.omp/agent/models.yml` 数据丢失。若原文件不存在，`prevContent == nil` 时补偿什么也不做，失败请求永久留下新配置文件。
- 证据：Apply 仅保存旧 bytes；compensate 在 `prevContent != nil` 时直接 `os.WriteFile`，没有“本事务写入内容”的 digest、当前内容校验、原始存在状态、权限/原子替换；nil 分支无删除动作。
- 复现：让 create 完成 config effect 后阻塞 process effect；手动修改目标配置，再触发 process 失败，Abort 会覆盖手动修改。以不存在的目标文件重复，失败后新文件残留。
- 精确修复：Effect 准备阶段记录 `{existed, mode, preimageDigest}`，Apply 后记录 `{writtenDigest}`；Abort 仅在当前文件 digest 仍等于 `writtenDigest` 时 CAS 恢复，原先不存在则 exact-delete，原先存在则原子替换并恢复 mode。CAS 不匹配必须报告 compensation debt，禁止覆盖外部修改。为三个 target 各加 existed/not-existed/concurrent-edit 测试。

#### M-05 shared-service admission 与 lease 生命周期未落地，修复 M-01 后会产生竞态和永久 lease

- 位置：`internal/remote/remote_session_adapter.go:498-579,636-639`；`internal/remote/shared_coordinator.go:130-135,282-290`；`internal/remote/remove_gc.go:72-74`
- 影响：headroom/proxy side effect 在没有 launch admission 的情况下先执行，可能与 uninstall/mutation 并发；process 启动后才用 `AcquireForRun` 获取 lease。成功时 `acquiredLeases` 只存在于局部变量，未转移到任何 session/cleanup owner，stop/remove 无法 exact release，服务会永久显示 in-use。proxy admission 甚至被 coordinator 明确拒绝。
- 证据：adapter 在 effect loop 前未调用 `AcquireLaunchAdmission`，lease 使用无 admission 的 `AcquireForRun`；成功后局部 slice 直接离开作用域。remove GC 虽调用 `releaseShared(sessionID)`，但 remote create 从未把 lease写入该 callback 对应的 App map。coordinator 的 admission API 仅接受两种 headroom，不接受 proxy。
- 复现：修正 M-01 后创建一个带 shared dependency 的 remote session并成功激活，再 stop/remove；查询 coordinator，lease 仍存在，后续 uninstall/mismatched launch 被 `ErrSharedServiceInUse` 拒绝。并发 uninstall 与 create 可在 effect-before-lease 窗口交错。
- 精确修复：在任何 shared side effect 前一次性获取全部 admission；扩展 coordinator 支持 proxy；成功时使用 `AcquireForRunWithAdmission` 原子提升。composite commit 前把 leases 转移到按 session+run generation 管理的持久 owner；stop/restart/remove exact-close 后按 run generation release，失败路径则 release admission/lease 并仅停止本事务实际启动且无人使用的服务。

### Minor

#### m-01 Planner/Executor 测试未统一隔离 HOME，且没有覆盖真实执行语义

- 位置：`launch_planner_executor_test.go:19-32,44-94,127-151,230-318,320-344`；`launch_planner.go:594-618,692-706,730-750`
- 影响：Pi/Omp/Codex Planner 测试可能读取开发机真实 `~/.pi`、`~/.omp`、`~/.codex/config.toml`，造成环境依赖与证据不可复现；fake resolver 固定 `/usr/bin/echo`，测试只检查 effect 类型/顺序，未验证 Windows bootstrap、shared upstream/port、Pi/Omp argv-provider 或真实 config compensation，因此 C-01/M-01/M-02/M-04/M-05 全部可在绿测下存在。
- 证据：公共 `testPlannerSetup` 只把 service configDir/homeDir 字段设为 temp dir，未 `t.Setenv("HOME", ...)`；Planner 的 Pi/Omp default agent dir 和 Codex preimage直接调用 `os.UserHomeDir()`。只有最后一个 no-write case 单独设置 HOME。
- 复现：在真实 HOME 放入不同 Pi/Omp/Codex 配置后直接运行 `go test . -run 'TestBuildPlan'`，计划 preimage/merge 会受本机文件影响。
- 精确修复：公共 setup 第一行统一 `t.Setenv("HOME", t.TempDir())`（Windows 同时隔离对应 user-profile vars），并将 home/path resolver 依赖注入 Planner/Executor。增加 effect spy、config FS、fake proxy/headroom 与 Windows PTY bootstrap 的 end-to-end transaction tests；至少跑一条移动端 REST create -> list/detail -> WS attach -> input/output -> stop/remove 的 L3 证据。

## 其他审核结果

### Authority / composite / remove / attach

- Manager 是生产 membership owner；remote adapter 注入同一 `app.Sessions` Authority，legacy Catalog 仅作无 Authority harness/fallback。
- `CommitPreparedActivation` 在 Manager authority guard 内调用 composite `CommitNoFail`；H1 run activation 与 H3 causal reservation 使用既定锁序，panic 被视为 fatal，不返回伪成功。
- remove 先获得 concrete process binding，`CloseExact` 确认后才提交 Authority tombstone + terminal state；GC 按 receipt 回收 stream/ledger/registry/tombstone，journal committed completion 有重试/recovery。
- WS attach 从 Authority handle 解析并使用 opaque attachment/control lease；未发现由 session ID 直接获取可写能力的新增旁路。

### Wire 与兼容性

- frozen contract counts 仍为 REST `10`、client frames `5`、server event types `8`、error codes `12`。
- `SessionInfo` legacy JSON shape 仍为 14 个既有字段，见 `internal/session/authority_test.go:205-226`。
- 未发现对 `frontend/wailsjs/` 的手改或 Wails bound legacy method shape 破坏。

### 安全与生产质量

- 对纳入 fingerprint 的生产 diff 扫描未发现 TODO/FIXME/HACK、placeholder/demo/伪数据标记或 emoji。
- 未发现置信度大于 80% 的 OWASP 注入、认证绕过、访问控制或 secret 日志泄露问题。
- Plan 内进程环境含 API key，但当前 diff 未见把 env 打入日志/持久化；本项不定级。后续仍应让 `DisposeSecrets` 同时清零所有派生 env 副本，缩短内存驻留。

## 验证与证据审核

以下均在当前 fingerprint 上由 Reviewer 复跑；Go 测试使用临时 HOME，未触碰真实用户配置：

- `git diff --check`：PASS。
- 目标包普通测试：`go test ./internal/session ./internal/processcap ./internal/launchplan ./internal/remote .`：PASS。
- 目标包 race：`go test -race ./internal/session ./internal/processcap ./internal/launchplan ./internal/remote .`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。
- `GOOS=windows CGO_ENABLED=0 go test ./... -run '^$'`：PASS，仅证明交叉编译。
- `GOOS=linux CGO_ENABLED=0 go test ./... -run '^$'`：PASS，仅证明交叉编译。
- frozen contract 定向测试：PASS，counts 为 10/5/8/12。
- `npm --prefix mobile test`：PASS，60 files / 547 tests。
- `npm --prefix mobile run build`：PASS。
- `npm --prefix frontend run build`：PASS；仅有既存 chunk-size warning。
- macOS Go 编译仅出现 Security API deprecated warning，无失败。
- Browser/Web 交互：不适用，本 diff 无前端行为或视觉文件变更；但后端真实 session 的移动端 L3 E2E 证据缺失，不能用 API 单测或 build 替代。

现有 implementation reports 的测试结论与当前 diff 的“测试可通过”一致，但其 Planner/Executor 证据停留在 fake resolver/effect-shape，不能证明目标 CLI 已真实启动。当前全绿测试不能抵消上述静态确定性缺陷。

## 结论

VERDICT FAIL

SessionAuthority 单一真相源、composite activation/remove、opaque attach、wire 兼容和大部分并发基础设施已形成较完整证据链；但 P0 的核心交付“移动端创建并连接真实五 CLI embedded session”尚未成立：Windows 会发布 shell-only 假会话，Claude proxy/headroom 必然失败，Pi/Omp provider 不等价，production restart 固定失败，config/shared-service 补偿与所有权也不完整。

建议回流：

1. `luban` 修复 C-01、M-01、M-02、M-03、M-04、M-05，并补齐 exact transaction tests。
2. `wukong` 独立补 Windows bootstrap、五 CLI argv/env/config、shared lease 生命周期、config CAS rollback 与移动端真实 E2E 证据。
3. 同一 diff 不重复终审；修复后仅提交增量 diff 与对应新证据复审。旧 fingerprint 上的本结论在代码变化后失效。
