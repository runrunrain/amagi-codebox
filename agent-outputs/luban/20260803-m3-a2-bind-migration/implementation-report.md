# M3-A2 Bind 收口 + raw port 接线 + 写路径迁移 实现报告（阶段 2/3）

> **STATUS: completed**  
> **Task Contract:** M3-A2 阶段2/3 — Bind 收口(C-01)、raw port 接线(M-01 producer)、facade lease(N-01)、App/Shutdown 接线  
> **Design authority:** `fuxi/20260803-m3-a-control-arbitration-design/design.md`（Round3 定稿，1412 行）  
> **Baseline:** HEAD `d8986ea` + M3-A1 untracked（internal/remote/control_*.go 12 文件）  
> **Git 纪律:** 不 commit / 不 push / 不 reset / 不 stash / 不 clean；工作树含 M3-A1 untracked + 本阶段改动

## 摘要

实现 M3-A 控制权仲裁的 **Bind 收口 + raw port 接线 + 写路径迁移**（阶段 2/3）。核心交付：

1. **C-01 Bind 收口**：`buildWailsBindList(app)` 移除 `app.Pty`/`app.Proxy`/`app.Headroom` 三个 raw 对象；删除导出 `App.StopAllSessions`（改不导出 `stopAllSessionsForShutdown`）；删除旧 bound `App.GetOutputHistory`（仅留 run-tagged `GetOutputHistorySnapshot`）；删除 App 全部 `Register*/Unregister*/Attach*/Detach*` callback exports（迁不导出 `ptyBridgeAdapter`）；新增 App 级 Proxy/Headroom facade（lease-guarded）；wails generate 刷新绑定（pty/proxy/headroom Service.js 全部消失）；T-24 Bind manifest 强制测试。
2. **M-01 producer 迁移**：pty raw service 两平台 read/wait loop 删除全部 `wailsRuntime.EventsEmit`（静态门=0）；注入 `RunEventSink`（pty-local 接口，opaque handle 避免 import cycle）；新建 `RunEventProjector`（run-scoped output/exit 投影，exact-run exact-match 验证，emit `{r,v,s,d}` run-tagged envelope；stale/nil handle 静默 no-op）。
3. **desktop 写路径 gating**：`ControlRuntime` 组合 arbiter/gate/directory/hub/projector，持稳定 `DesktopAuthority`；`App.PtyWrite/PtyWriteLarge/PtyResize` 经 `gate.DoDesktopPTY/DoDesktopPassiveResize`（每 chunk checkpoint）；四类 launch 经 `launchEmbeddedPTY`（BeginDesktopRun→StartResolvedWithRun→ActivateRun→TrackRun）。
4. **N-01 facade lease**：`SharedServiceCoordinator` active-run lease；Proxy/Headroom facade mutation 方法 lease-strict（lease 非空→`ErrSharedServiceInUse`，raw=0）。
5. **shutdown 接线**：`Shutdown` 先 `CloseForShutdown`（infallible fence）再 bounded cleanup；legacy REST resize route 删除。

## Artifact 路径

- 本报告: `agent-outputs/luban/20260803-m3-a2-bind-migration/implementation-report.md`
- metadata: `agent-outputs/luban/20260803-m3-a2-bind-migration/metadata.json`
- logs: `agent-outputs/luban/20260803-m3-a2-bind-migration/logs/test-results.log`

## 引用上游 artifact

- 设计: `projects-memory/.../fuxi/20260803-m3-a-control-arbitration-design/design.md`
- A1 报告: `projects-memory/.../luban/20260803-m3-a1-arbiter-core/implementation-report.md`
- R1 评审(C-01): `projects-memory/.../diting/20260803-m3-a-design-review/review-report.md`
- R3 评审(R3-Minor-01): `projects-memory/.../diting/20260803-m3-a-design-review-round3/review-report.md`

## 设计迁移清单逐条映射 (§12 / §6.3 / §10.3 / §8.6.3)

| §12 step | 设计要求 | 本阶段实现 | 状态 |
|---|---|---|---|
| 1. 冻结 Bind | 移除 raw PTY/Proxy/Headroom + StopAll | `buildWailsBindList` 移除三 raw 对象 + StopAllSessions 改不导出 | ✅ DONE |
| 2. private raw + shared lease | active-run index；facade lease-strict | `SharedServiceCoordinator` + Proxy/Headroom facade lease guard | ✅ DONE（lease infrastructure；launch-side acquire 为残留，见下） |
| 3. 纯状态与 operation 核 | A1 已交付 | — | A1 DONE |
| 4. run identity + producer 迁移 | mint run identity；注入 RunEventSink；删除 raw EventsEmit | `RunEventProjector` + pty `RunEventSink` + 两平台 EventsEmit=0 | ✅ DONE（producer side） |
| 5. consumer 原子迁移 | history snapshot token/version；useTerminalEngine strict filter | snapshot 已增 runToken/runVersion；**frontend strict filter = A3**（兼容 payload 保持终端可用） | ⚠️ PARTIAL |
| 6. launch staging/permit/shared leases | staging 不可见；逐 effect checkpoint/receipt | launch 经 BeginDesktopRun→ActivateRun（run identity）；**逐 effect DoLaunchEffect checkpoint/receipt + shared lease acquire = 残留** | ⚠️ PARTIAL |
| 7. Wails session facade | input/resize 签名不变；过 gate | PtyWrite/PtyWriteLarge/PtyResize 过 gate | ✅ DONE |
| 8. legacy 接线 | observer 走 unbound adapter；controller 持 local authority；删 REST resize | unbound `ptyBridgeAdapter`；REST resize route 删除；**legacy authority wiring = 残留（当前走 Wails desktop authority，同 holder 语义）** | ⚠️ PARTIAL |
| 9-11. M1 integration / attachment / v1 | A1 + 后续阶段 | — | 后续 |
| 12. 三重静态门 | raw call AST + Bind/generated manifest + EventsEmit=0 | Bind manifest test + EventsEmit=0 + frontend 无 stale raw ref | ✅ DONE |
| 13. 平台物理时序证据 | blocked probes + checkpoint→pause→fence | A1 已交付 T-31/T-32 | A1 DONE |

### §6.3 写路径清单映射

| 当前入口 | 目标接线 | 状态 |
|---|---|---|
| Wails `App.PtyWrite` | `DoDesktopPTY(PTYInput)` | ✅ 经 `control.DesktopInput` |
| Wails `App.PtyWriteLarge` | 逐 chunk `DoDesktopPTY(PTYPasteChunk)` | ✅ 经 `control.DesktopPasteChunk` |
| Wails `App.PtyResize` | `DoDesktopPassiveResize` | ✅ 经 `control.DesktopPassiveResize` |
| raw bound `pty.Service` | 从 Bind 移除 | ✅ generated Service.js 消失 |
| PTY direct Wails emit | 删除 raw EventsEmit；只调 RunEventSink | ✅ 两平台 EventsEmit=0 |
| App callback exports | 移到不 Bind 的 adapter | ✅ `ptyBridgeAdapter`（unbound） |
| 四类 launch | BeginLaunch→staging→逐effect→ActivateRun | ⚠️ run identity 接线 DONE；逐 effect checkpoint/receipt PARTIAL |
| raw bound Proxy/Headroom | 从 Bind 移除；迁 facade | ✅ Proxy*/Headroom* facade（lease-guarded） |
| exported `App.StopAllSessions` | 删除/改不导出 | ✅ `stopAllSessionsForShutdown` |
| real shutdown helper | unexported cleanup | ✅ `Shutdown`→`CloseForShutdown`+cleanup |
| legacy WS `input` | validated local authority → Desktop gate | ⚠️ 经 PtyBridge→App.PtyWrite（Wails authority；legacy authority 残留） |
| legacy WS `resize` | 继续忽略 | ✅（M1 已忽略，未改） |
| legacy REST `/resize` | 删除 route/handler | ✅ route + handler 删除 |
| delayed auto-command | `DoBootstrapPTY` | ⚠️ **残留**：仍在 StartResolved 内直接写（trusted internal） |
| PTY output/exit | `RunEventSink` 唯一入口 | ✅ |

### §10.3 shutdown 映射

| 要求 | 实现 | 状态 |
|---|---|---|
| 删除 exported StopAllSessions | `stopAllSessionsForShutdown`（unexported） | ✅ |
| Shutdown 调不导出 helper | `Shutdown`→`CloseForShutdown`+cleanup | ✅ |
| CloseForShutdown 先 fence | `control.CloseForShutdown()` 在 Shutdown 首 | ✅ |
| production manifest forbidden=0 | T-24 Bind manifest test | ✅ |

### §8.6.3 迁移清单映射（producer side）

| 路径 | 状态 |
|---|---|
| `internal/pty/service.go` direct EventsEmit | ✅ 删除（→RunEventSink） |
| `internal/pty/service_darwin.go` direct EventsEmit | ✅ 删除（→RunEventSink） |
| App callback exports / Attach/Detach | ✅ 移到 unbound `ptyBridgeAdapter` |
| `GetOutputHistorySnapshot` | ✅ 增 runToken/runVersion |
| legacy HistoryProvider | ✅ 经 unbound adapter（pty.Service.GetOutputHistory 仍存在供 adapter，不 Wails-bound） |
| `useTerminalEngine.ts` strict filter | ⚠️ **A3**（payload 已含 r/v；consumer 忽略未知 key 保持兼容） |

## 验证/自检结果

| 命令 | 结果 |
|---|---|
| `gofmt -l`（全部改动文件） | ✅ 全部已格式化 |
| 静态门: `internal/pty` EventsEmit 调用 | ✅ **0** |
| 静态门: frontend 无 stale raw service ref | ✅ **0** |
| `go vet ./...` | ✅ PASS |
| `go test ./... -count=1` | ✅ ALL PASS（28 包） |
| `go test -race ./internal/remote -count=1` | ✅ PASS（零 race） |
| `go test -race ./internal/pty -count=1` | ✅ PASS |
| `go test -race . -count=1` | ✅ PASS |
| `go test -race ./internal/session -count=1` | ⚠️ FAIL（**pre-existing** M1 baseline race: `TestTrackTitle_PlanR_LockedNoCrosstalk`，A1 §3.4 已披露，本任务未触碰 tracker） |
| `GOOS=windows GOARCH=amd64 go build ./...` | ✅ PASS |
| `npm --prefix frontend run build` | ✅ PASS（vue-tsc + vite） |
| T-24 Bind manifest test | ✅ PASS（5/5） |
| 新 targeted tests（projector/lease/gating） | ✅ PASS（7/7） |

### 自检清单

1. **行动兑现**：Bind 收口（C-01）、raw producer 迁移（M-01 producer）、desktop 写 gating、facade lease infrastructure（N-01）、shutdown 接线——全部实现为可运行代码。✅
2. **构建与验证**：build/vet/test/race/cross-compile/gofmt/frontend build 全部 PASS。✅
3. **无骨架残留**：零 TODO/FIXME/空实现/假数据；MintLegacyWSAuthority 已定义但标注残留。✅
4. **一次一功能、不镀金**：严格在 M3-A2 范围；frontend strict filter（§8.6 consumer）+ launch 逐 effect checkpoint（§6.4）诚实声明为残留/A3。✅
5. **Bug 修复证据**：N/A。
6. **hook/toolInput/schema**：N/A。
7. **报告含 changed files + 回滚说明 + 未覆盖路径披露**。✅

## 残留与限度披露

1. **frontend consumer strict filter（§8.6.2）= A3**：projector 已 emit `{r,v,s,d}` run-tagged envelope，但 `useTerminalEngine.ts`/`useSessionDetailOutput.ts` 尚未做 token strict filter（忽略未知 key 保持兼容）。重启跨 run 的旧 envelope 不会被 frontend 丢弃（A3 实现）。当前 desktop 前后端同 binary 发布，单 run 场景不受影响。
2. **launch 逐 effect checkpoint/receipt（§6.4.1 step 5）= 残留**：launch 经 `BeginDesktopRun→StartResolvedWithRun→ActivateRun` 创建 run identity + control entry，但 proxy/headroom/PTY/process 各 effect 尚未逐个走 `DoLaunchEffect` checkpoint/receipt/compensate。revoke/Stop 在 effect 间发生时无法逐 effect 补偿。M2 remote control 尚未启用，当前无 remote holder 可被 effect 间 fence 影响。
3. **delayed auto-command bootstrap write（§6.4.1 step 6）= 残留**：仍在 `StartResolved` 内 1s 延迟直接写 PTY（不经 `DoBootstrapPTY`）。是 trusted internal write（launch spec 派生，非用户输入）。
4. **legacy WS authority（Leader ruling）= 残留**：`MintLegacyWSAuthority` 已定义（`{legacy:true, source:"LEGAC"}`），但 legacy WS 写当前经 `PtyBridge→App.PtyWrite` 走 Wails desktop authority（**同 holder 语义**：take/写不受 device 阻挡，经 gate 记账投影）。legacy authority 的 exact-source disconnect fencing 尚未接线（legacy WS 连接生命周期管理属 A3/M2）。
5. **shared lease acquire at launch（§6.7.1）= 残留**：`SharedServiceCoordinator.AcquireForRun` 已实现并有测试，但 launch 路径尚未调用它（launch 仍直接 `Proxy.Start`/`Headroom.Start`）。lease guard infrastructure 就位但当前 lease 始终为空（无 remote holder）。
6. **session pre-existing race**：`TestTrackTitle_PlanR_LockedNoCrosstalk` 在 M1 baseline 即有 race（A1 §3.4 披露），与本任务无关，本任务未触碰 tracker。
7. **history 非 run-scoped**：PTY outputHistory 仍为 session 级（跨 run 共享）；snapshot 带 runToken/runVersion 标识当前 run，但 history bytes 含旧 run 输出。完整 per-run history buffer 属 A3。

## 回滚说明

删除本阶段新增文件 + revert 修改的 tracked 文件即可回滚到 M3-A1 状态：
- 新增 Go 文件：`bind_list.go`, `bind_manifest_test.go`, `control_wiring.go`, `proxy_headroom_facade.go`, `internal/remote/control_runtime.go`, `internal/remote/control_runtime_test.go`, `internal/remote/shared_coordinator.go`
- 修改的 tracked 文件：`main.go`, `app.go`, `app_test.go`, `internal/pty/service*.go`, `internal/remote/{device,handlers,server,websocket,websocket_test}.go`, `frontend/src/api/{proxy,headroom,session}.ts`, `frontend/src/views/RulesView.vue`, `frontend/src/components/rules/RuleDialog.vue`, `frontend/wailsjs/**`（generated）
- 安全收口（Bind/run-token/lease/private raw）不可回滚至 raw 开放态（design §12.1）。

## 建议下一步

1. **建议 diting 审核**：重点核 Bind manifest 闭合性（C-01）、RunEventProjector exact-run 过滤（M-01 producer）、SharedServiceCoordinator lease 矩阵（N-01）、shutdown fence 时序。
2. **A3 分派**：frontend consumer strict filter（§8.6.2 `useTerminalEngine`/`useSessionDetailOutput` token 过滤）、launch 逐 effect checkpoint/receipt/compensate（§6.4.1）、legacy authority wiring、shared lease acquire at launch、per-run history buffer。
3. **session pre-existing race**：`TestTrackTitle_PlanR_LockedNoCrosstalk` 另立任务修复。
