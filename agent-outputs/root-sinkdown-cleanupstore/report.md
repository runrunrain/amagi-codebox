# 根目录代码文件规范化下沉 — 迁移报告

等价重构（编译器护航的机械迁移），未 commit。HEAD = 63cdebf。

## A. external_cleanup_store → internal/cleanupstore（新包）

文件：`git mv external_cleanup_store.go internal/cleanupstore/cleanupstore.go`（status: RM）
`git mv external_cleanup_store_test.go internal/cleanupstore/cleanupstore_test.go`（status: RM）

### 符号映射表

| 原符号（package main） | 新符号（package cleanupstore） | 可见性 | 备注 |
|---|---|---|---|
| `externalCleanupStore`（接口） | `Store` | 导出 + godoc | |
| `fileExternalCleanupStore` | `fileStore` | 私有 | 按任务映射 |
| `newFileExternalCleanupStore` | `NewFileStore` | 导出 + godoc | |
| `newFileExternalCleanupStoreWithRename` | `NewFileStoreWithRename` | 导出 + godoc | |
| `externalCleanupReservation` | `Reservation` | 导出 + godoc | 原 fsync 语义注释保留为 godoc |
| `externalCleanupRecord` | `Record` | 导出 + godoc | |
| `externalCleanupJournalEvent` | `JournalEvent` | 导出 + godoc | |
| `externalCleanupJournalAction` | `journalAction` | 私有 | 按任务映射 |
| `externalCleanupJournalVersion` | `JournalVersion` | 导出 + godoc | 包外使用（headroom_facade + 3 测试）→ 以编译为准导出 |
| `externalCleanupJournalName` | `JournalName` | 导出 + godoc | 同上 |
| `externalCleanupJournalFilePerm` | `JournalFilePerm` | 导出 + godoc | 同上 |
| `externalCleanupReserved` 等 4 个 action 常量 | `ActionReserved` / `ActionReservationCompleted` / `ActionRegistered` / `ActionCompleted` | 导出 + godoc | `ActionRegistered` 被 r10/external_recovery 测试构造 journal 事件使用 → 全组统一导出（类型 `journalAction` 保持私有，合法：导出常量可用非导出类型） |
| `errExternalCleanupStoreNotReady` | `ErrStoreNotReady` | 导出 + godoc | 被 headroom_facade.go 包外用（6 处）→ 按任务规则「以编译为准」导出；错误消息字符串不变 |
| `errExternalCleanupInvalidRecord` | `ErrInvalidRecord` | 导出 + godoc | 同上（1 处包外用） |
| `errExternalCleanupStoreFull` | `ErrStoreFull` | 导出 + godoc | 包外未用，但与上两者同组统一导出；消息不变 |
| `externalCleanupJournalMaxBytes` / `MaxLineBytes` / `DirPerm` | 原名不变 | 私有 | 仅包内使用 |
| `validate*` / `same*` / `read*` / `syncExternalCleanupDirectory` | 原名不变 | 私有 | `sameExternalCleanupProcess` 仅被随包测试使用，保持私有 |

头注释：改为 `Package cleanupstore` doc，保留原全部设计锚点（0700/0600、append-only NDJSON、fsync、fail-closed、与 session-operations.log 的隔离说明），并注明「mechanically extracted unchanged from the root package」。

### 计划外偏差（1 项，编译强制）

`external_cleanup_store_test.go` 的最后一个测试 `TestExternalCleanupStoreCorruptFileFailsClosed` 使用 `newTestApp` / `App.recoverExternalCleanups` / `App.acquireSharedMutation` / `App.stopAllHeadroomForUninstall`（App 耦合），无法随包迁入 cleanupstore。处理：该测试函数移入 main 包 `external_recovery_test.go` 末尾（语义 100% 保留，符号加 `cleanupstore.` 前缀，`envcheck` import 随之落在该文件），并留注释说明原因。cleanupstore_test.go 保留 4 个纯 store 测试（import envcheck 随测试移除，remote 保留——任务要求）。

### main 包引用更新统计（7 文件，均加 import `"amagi-codebox/internal/cleanupstore"`）

| 文件 | `cleanupstore.` 前缀出现 | 明细 |
|---|---|---|
| app.go | 10 | 字段/映射类型 6（claim.record/reservation、externalDurableRuns×2、Store 字段类型）+ 构造器 1 + `var reservation`×2 + 空字面量×2 —与任务「10 处」吻合；仅符号前缀，App 结构与绑定面未动 |
| headroom_facade.go | 23 行（36 处引用） | 类型/常量/错误机械替换；`a.externalCleanupStore` 字段访问保持原名 |
| shutdown_persistence_stop_gate_test.go | 6 行（13 处） | 含嵌入式接口 `struct{ cleanupstore.Store }`、复合字面量键 `{Store: underlying}` |
| durable_ownership_r9_test.go | 10 行（12 处） | 同上（2 个包装 store 类型嵌入式接口 + 2 处字面量键） |
| durable_ownership_r10_test.go | 10 行（15 处） | 同上 + `s.Store.Reserve`（嵌入字段访问） |
| external_recovery_test.go | 12 行（8+9 处） | 同上 + 承接移入的 App 级测试 |
| external_headroom_lease_test.go | 1 行（2 处） | 构造器替换 |

## B. config_sync_* → internal/platform

- `git mv config_sync_unix.go internal/platform/config_sync_unix.go`（`//go:build !windows` 保留）
- `git mv config_sync_windows.go internal/platform/config_sync_windows.go`（`//go:build windows` 保留）
- `package main → platform`；`syncConfigDirectory → SyncConfigDirectory`（导出 + 双平台 godoc，说明 FILE_FLAG_BACKUP_SEMANTICS/FlushFileBuffers 与 unix open+Sync 差异）
- `launch_executor.go` 3 处调用点 → `platform.SyncConfigDirectory`（SyncDirectory 接口实现行 + 2 处直接调用）；该文件本已 import platform

## C. 未触碰（按任务边界核实）

main.go、bind_list.go、app.go 本体结构（仅 A 项前缀替换）、app_remoteclient.go、app_config_portable.go、control_wiring.go、provider_harness_sync.go、remote_security_migration_gate.go、launch_workdir.go、launch_planner.go、tray_icon_*（embed 资产绑定）、其余根目录 _test.go — 全部零改动。

## D. CLAUDE.md

"Test file locations" 节后新增 `### Root-directory Go files` 小节（3 行正文，英文）：根目录仅允许 wails 入口、App 绑定/编排、深耦合 launch planner/executor、embed 绑定平台文件；独立域组件下沉 `internal/<domain>`，平台工具进 `internal/platform` per-OS 文件。

## 验证结果（全绿）

| 命令 | 结果 |
|---|---|
| `go vet ./...` | ✅ 无输出 |
| `go test . -count=1` | ✅ ok amagi-codebox 7.747s（main 包全量，含 7 个改动测试文件 + 移回的 App 级测试） |
| `go test ./internal/cleanupstore ./internal/platform -count=1` | ✅ ok 0.993s / 0.516s |
| `go build ./...` | ✅ |
| `GOOS=windows go build ./...`（附加护航，config_sync_windows.go 迁移） | ✅ |
| `gofmt`（全部触碰文件） | ✅ 我改动区域无格式问题；app.go 第 166 行 codexConfigSyncOptions 对齐为 HEAD 既有（go1.26 gofmt 与 1.25 差异），未触碰 |

## git status 改动清单（与方案一致：4 个 mv + 8 个符号修改 + CLAUDE.md）

```
 M CLAUDE.md                          ← D 项（注意：CLAUDE.md 还有任务前既有未提交改动，见下）
 M app.go / headroom_facade.go        ← A 项前缀
 M launch_executor.go                 ← B 项调用点
 M shutdown_persistence_stop_gate_test.go / durable_ownership_r9_test.go /
   durable_ownership_r10_test.go / external_recovery_test.go / external_headroom_lease_test.go  ← A 项
RM external_cleanup_store.go    -> internal/cleanupstore/cleanupstore.go
RM external_cleanup_store_test.go -> internal/cleanupstore/cleanupstore_test.go
RM config_sync_unix.go          -> internal/platform/config_sync_unix.go
RM config_sync_windows.go       -> internal/platform/config_sync_windows.go
```

## 未覆盖风险 / 备注

1. **工作区并非任务前提所述的干净**：开始前已存在与本任务无关的未提交改动 — `.github/workflows/ci.yml`、`CLAUDE.md`（他人改动的区域与本报告新增小节不同段落）、`docs/developer/testing.md`、`frontend/package.json`、`frontend/package-lock.json`、未跟踪 `frontend/src/__tests__/`、`frontend/vitest.config.ts`。全部未触碰；本任务未 commit。
2. gofmt 基线差异：本地 go1.26.1 与仓库 HEAD（Go 1.25 格式化）在带注释 struct 对齐上存在系统性差异，故未对整文件执行 gofmt，仅修正我引入的格式（r10/r9 复合字面量、cleanupstore.go、launch_executor.go 双平台 SyncDirectory 一行函数）。
3. 错误变量与 Journal 常量/action 常量的导出超出任务显式映射表，但符合任务「若被包外用则导出，以编译为准」条款；错误消息字符串全部未变。
