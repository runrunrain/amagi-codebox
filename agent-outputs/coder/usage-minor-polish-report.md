# 使用统计 5 项 Minor 修复报告（鲁班）

> 范围：amagi-codebox 使用统计功能 diting 审核提出的 M1–M5，收尾打磨，不扩大范围。
> 上游主功能已 diting_pass；本次仅修这 5 项，并补/调对应测试与 wailsjs 重新生成。

## 实现摘要 / 验收映射

### M1 — 消除 wailsjs 无效绑定（StartBackgroundSync）

**采用方案**：去 `context.Context` 参数 + Service 内部 `Ctx` 字段注入。

**根因**：Wails v2 无方法级排除注解（`//wails:skip` 是 v3）；v2 Bind 一个 struct 会暴露其全部导出方法。原签名 `StartBackgroundSync(interval time.Duration, ctx context.Context)` 的 `context.Context` 不可 JS 序列化，wailsjs 生成了一个前端无法调用的无效绑定。

**改法**：
- `internal/usage/service.go:30-43`：`Service` 新增导出字段 `Ctx context.Context`。Wails v2 仅绑定"方法"，结构体字段（即使导出）不进入 `Service.d.ts` 生成路径，因此不会引入新绑定。
- `internal/usage/sync.go:180`：`StartBackgroundSync(interval time.Duration)` 新签名；内部 `ctx := s.Ctx`（nil 时 fallback `context.Background()`）。
- `app.go:741`：Startup 中 `a.Usage.Ctx = ctx`（`ctx` 是 Wails 传入的应用级 ctx，`a.ctx` 同源）。
- `app.go:769`：调用改为 `a.Usage.StartBackgroundSync(5 * time.Minute)`。

**为何不拆类型 / 不重命名以彻底隐藏**：拆类型会改 `wailsjs/go/usage/Service` 生成路径，违反"不破坏前端 import"约束；v2 下"去 ctx 参数 + 内部字段"是最小可行方案。

**wailsjs 重新生成结果**（`~/go/bin/wails generate module`）：
- `frontend/wailsjs/go/usage/Service.d.ts`：`StartBackgroundSync(arg1:time.Duration):Promise<void>;` —— 不再含 `context.Context`，`context` import 也已移除。
- 19 个前端 API 方法（Close / DeleteModelPricing / GetDailyTrends / GetModelPricing / GetModelStats / GetProviderStats / GetRequestLogs / GetSyncState / GetUnknownModels / GetUsageSummary / Load / Pricing / Record / RecordForce / ResetModelPricing / StartBackgroundSync / SyncAll / SyncSessionUsage / UpsertModelPricing）全部仍在，签名未变。

### M2 — 补 5 个 OpenAI 模型到 pricing_seed.go

**文件**：`internal/usage/pricing_seed.go:62-86`

新增（USD / micro-USD-per-million / isBuiltin=true / notes="OpenAI 官方短上下文标准价，参考价"）：

| ModelPattern   | DisplayName     | input     | output     | cacheRead | cacheCreation |
|----------------|-----------------|-----------|------------|-----------|---------------|
| gpt-5.6-sol    | GPT-5.6 Sol     | 5_000_000 | 30_000_000 | 500_000   | 6_250_000     |
| gpt-5.6-terra  | GPT-5.6 Terra   | 2_500_000 | 15_000_000 | 250_000   | 3_125_000     |
| gpt-5.6-luna   | GPT-5.6 Luna    | 1_000_000 | 6_000_000  | 100_000   | 1_250_000     |
| gpt-5.5        | GPT-5.5         | 5_000_000 | 30_000_000 | 500_000   | 0             |
| gpt-5.3-codex  | GPT-5.3 Codex   | 1_750_000 | 14_000_000 | 175_000   | 0             |

**NormalizeModelID 自洽验证**：`TestPricingSeedOpenAIGPT56`（usage_test.go:347）对每个 ModelPattern 调 `NormalizeModelID(pattern) == pattern` 断言，确认 `gpt-5.6-sol` 等命名经标准化后与 seed key 完全一致。`TestNormalizeModelID` 也已含 `gpt-5.6-sol` 用例。

### M3 — daily_rollup 分区刷新

**文件**：`internal/usage/store_sqlite.go:269-356`（`refreshDailyRollup` + `uniqueSortedDays`）

**契约**：`refreshDailyRollup(ctx, db, days []string)`：
- `len(days)==0` → 走原全量重算（DELETE ALL + INSERT SELECT），等价于旧行为，测试 / 兜底用。
- `len(days)>0` → 分区刷新：`DELETE FROM daily_rollup WHERE day IN (...)` + 对这些 day 重新聚合 INSERT。两步同一事务，原子可见。

**受影响日期集合来源**（`internal/usage/sync.go`）：
- SyncAll 新增 `affectedDays map[string]struct{}`，每个 sync 函数（Claude/Codex/OpenCode）通过新返回值 `syncOutcome.days` 回传本轮实际改写过 DB 的行对应的 UTC 日期（`occurred_at.UTC().Format("2006-01-02")`）。
- Claude/Codex：仅 `Record` 返回 `isNew=true` 时记入 days（IGNORE 不改写已有行）。
- OpenCode：`recordForceInternal` 无论 isNew 还是 REPLACE 都记入 days（REPLACE 会改写聚合值）。
- 兜底：始终把"今天"（`time.Now().UTC()`）加入 affectedDays，覆盖 proxy 实时路径绕过 sync 直接写主表的记录。
- 二次同步若所有源游标命中（无变化），affectedDays 仍含今天（rollup 至少刷新当日），代价可忽略。

**正确性验证**：`TestDailyRollupPartitionRefresh`（usage_test.go:267）跨两天各插记录，分区刷新 day2 后：
- day1 requests=1（未受影响，保持原值）；
- day2 requests=2（新聚合）；
- 紧接全量刷新，结果与分区刷新完全一致（等价性）。

### M4 — 删除手写 itoa/itoa64，改 strconv

**文件**：`internal/usage/sync.go`

- 导入新增 `strconv`。
- `sync.go` 日志行（原 line 125）改 `strconv.Itoa` / `strconv.FormatInt(n, 10)`。
- 删除手写 `itoa` 与 `itoa64`（原 sync.go:424 / 445）。
- `usage_e2e_test.go` 中唯一引用 `itoa(s.CacheCreationInputTokens)` 改为 `strconv.Itoa(...)`，并相应新增 `strconv` 导入。

`grep -rn "itoa\|itoa64"` 全仓（排除 vendor）已无残留。

### M5 — OpenCode recordsAdded 语义

**目标**：让 `SyncResult.recordsAdded` 反映"真正新增行（INSERT 生效）"，另立 `processedCount` 表"处理过的 stub 总数"。

**改法**：
- `internal/usage/store_sqlite.go:144-184`：`upsertRecord` 返回 `(bool, error)`。实现走"先查 dedup_key 再 INSERT OR REPLACE"——单写连接（`SetMaxOpenConns(1)`）下查询与写入串行，无竞态；不依赖 `changes()`（REPLACE 始终报 1 行，无法区分新/换）。
- `internal/usage/service.go:131-148`：新增内部 `recordForceInternal(evt) (bool, error)` 暴露 isNew 给 sync 路径；公共 `RecordForce(evt) error` 签名保持不变（仍属 19 个前端 API 之一），内部调用 recordForceInternal 并丢弃 bool。
- `internal/usage/sync.go`：
  - `syncOutcome{added, processed, days, err}` 统一返回值（三个 sync 函数都改）。
  - OpenCode 路径调 `recordForceInternal`，仅 isNew 计入 `added`；`processed` 计成功落库的 stub 数（含更新）。
  - 三个 sync 函数对 `processed++` 都在 `err==nil` 后执行，保证错误不计入处理数。
- `internal/usage/api_types.go:99-107`：`SyncResult` 新增 `ProcessedCount int64 json:"processedCount"`，未删除/改名既有字段，前端向后兼容。
- `internal/usage/service.go:42`：`SyncRunMeta` 同步加 `ProcessedCount`。
- `internal/usage/api.go:72-86`：`SyncSessionUsage` 把 `syncMeta.ProcessedCount` 透传到 `SyncResult`。

**测试**：`TestRecordForceIsNewSemantic`（usage_test.go:343）覆盖：
- 同 dedup_key 第一次 `recordForceInternal` → isNew=true；
- 第二次（改数据触发 REPLACE）→ isNew=false；
- 公共 `RecordForce` 签名/行为未变；
- 行数仍为 1（REPLACE 不新增行）。

**真机 e2e 验证**（`TestRealSyncAllE2E`，主上机器数据）：
- 首次 SyncAll：records=27694，added=27694，processed=35857，errors=0；
- 二次 SyncAll：delta=0、recordsAdded=0、processedCount=0（旧行为下 OpenCode 路径会重复计 added，新语义下严格为 0）。

## 是否改了前端字段（SyncResult）

**是，向后兼容地新增字段**：`SyncResult` 新增 `processedCount: number`；`recordsAdded`/`filesScanned`/`errors`/`duration`/`startedAt`/`finishedAt` 均保留。`models.ts` 已通过 `wails generate module` 重新生成。前端无需改动即可继续读旧字段；新字段为可选增量信息。

## 变更文件清单

| 文件 | 变更 | Minor |
|------|------|-------|
| `internal/usage/service.go` | `Ctx` 字段；`recordForceInternal`；`SyncRunMeta.ProcessedCount` | M1, M5 |
| `internal/usage/sync.go` | `StartBackgroundSync` 新签名；`syncOutcome`；分区 days 采集；strconv；删 itoa/itoa64 | M1, M3, M4, M5 |
| `internal/usage/store_sqlite.go` | `upsertRecord` 返回 isNew；`refreshDailyRollup(ctx,db,days)`；`uniqueSortedDays` | M3, M5 |
| `internal/usage/pricing_seed.go` | 5 个 OpenAI 模型 | M2 |
| `internal/usage/api_types.go` | `SyncResult.ProcessedCount` | M5 |
| `internal/usage/api.go` | `SyncSessionUsage` 透传 ProcessedCount | M5 |
| `internal/usage/usage_test.go` | 加 `TestDailyRollupPartitionRefresh` / `TestRecordForceIsNewSemantic` / `TestPricingSeedOpenAIGPT56`；改 rollup 全量测试为新签名 | M2, M3, M5 |
| `internal/usage/usage_e2e_test.go` | 改 itoa→strconv；加幂等 recordsAdded=0 断言 | M4, M5 |
| `app.go` | `a.Usage.Ctx = ctx`；`StartBackgroundSync(5 * time.Minute)` | M1 |
| `frontend/wailsjs/go/usage/Service.d.ts` | wails 重新生成：StartBackgroundSync 无 ctx | M1 |
| `frontend/wailsjs/go/usage/Service.js` | wails 重新生成 | M1 |
| `frontend/wailsjs/go/models.ts` | wails 重新生成：SyncResult.processedCount | M5 |

## 自验证结果

| 命令 | 结果 |
|------|------|
| `go vet ./...` | PASS（无输出） |
| `go build -mod=vendor ./...` | PASS（无输出） |
| `go test -mod=vendor -count=1 ./internal/usage/... ./internal/appmeta/... ./internal/proxy/...` | PASS（5 包全绿） |
| `go test -mod=vendor -tags manual_e2e -run TestRealSyncAllE2E -v ./internal/usage/...` | PASS（首次 added=27694 processed=35857；二次 added=0 processed=0 delta=0；summary OK） |
| `~/go/bin/wails generate module` | 成功；`Service.d.ts` StartBackgroundSync 签名 `arg1:time.Duration`，无 context；19 个 API 仍在 |
| `npm --prefix frontend run build` | PASS（vue-tsc + vite 全绿，无 TS 错误，import 路径未变） |
| 新增测试定向跑（`-run TestDailyRollupPartitionRefresh|TestRecordForceIsNewSemantic|TestPricingSeedOpenAIGPT56|TestDailyRollupRefresh`） | 4 个测试全 PASS |

## 未覆盖路径 / 风险

- M3 分区刷新对"proxy 实时路径直接写主表"的覆盖通过"始终把今天加入 affectedDays"兜底；若 proxy 在 UTC 午夜边界附近写入，理论存在当日归类漂移的可能（例如 UTC 23:59 写入 → rollup 在 00:01 跑，"今天"已切到新一天）。当前业务对单日聚合精确性要求未到该粒度，未做跨日处理。
- M3 分区刷新依赖 `occurred_at` 与 `strftime` 一致性，所有路径（proxy / sync）均已写 UTC（service.go:179 `OccurredAt: evt.OccurredAt.UTC()`），与时区假设一致。
- 真机 e2e 仅在主上机器跑过；其他机器路径（Windows）未跑（usage_e2e_test.go 已在 windows skip）。

## 回滚说明

改动全部集中在 usage 包内部 + app.go 两行 + wails 生成产物。回滚步骤：
1. 恢复 `internal/usage/{service,sync,store_sqlite,pricing_seed,api,api_types}.go`、`internal/usage/usage_test.go`、`internal/usage/usage_e2e_test.go`、`app.go`；
2. 重跑 `~/go/bin/wails generate module` 恢复 wailsjs 产物；
3. 不涉及 DB schema 变更或数据迁移，无需额外处理已有 `usage.db`。
