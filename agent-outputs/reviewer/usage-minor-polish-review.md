# 使用统计 5 项 Minor 修复 复审报告（谛听）

> 审核人：谛听（diting）
> 审核对象：鲁班 `agent-outputs/coder/usage-minor-polish-report.md` 描述的 M1–M5
> 审核时间：2026-07-17
> 审核基线：working tree（HEAD = 0a4012233705eb2107624b83f8ee82d253ba1d26 + 未提交改动）
> 审核范围：`internal/usage/`（新建包，未入 git）+ `app.go` + `frontend/wailsjs/go/usage/` + `frontend/wailsjs/go/models.ts`

## 结论

**diting_pass** —— M1/M2/M3/M4/M5 全部 PASS，无 Critical / Major 问题，无功能性回归。
2 项 Minor 观察（不阻断，记入剩余风险）。

回流项数：0。

## 逐项核实

### M1 — StartBackgroundSync 去 ctx 参数 + Service.Ctx 字段注入 — PASS

| 验收点 | 证据 | 结果 |
|---|---|---|
| 签名去掉 context.Context | `frontend/wailsjs/go/usage/Service.d.ts:36` — `StartBackgroundSync(arg1:time.Duration):Promise<void>;` | PASS |
| `context` import 从 d.ts 移除 | d.ts 仅 import `usage` 与 `time`，无 `context` | PASS |
| Ctx 字段不污染 models.ts | `frontend/wailsjs/go/models.ts:2460-2900` usage namespace 仅含 DailyTrendPoint/LogFilter/ModelPricing/ModelStat/PricingService/ProviderStat/StatFilter/SummaryDateRange/Summary/SyncResult/SyncState/UnknownModel/UsageEvent/UsageRecord/TrendFilter/SummaryFilter，**无 Service 类、无 Ctx 字段** | PASS（关键检查点，Wails v2 仅绑定方法验证） |
| app.go Startup 注入 ctx | `app.go:650` `Startup(ctx context.Context)` 接收 Wails ctx；`app.go:743` `a.Usage.Ctx = ctx` | PASS |
| StartBackgroundSync 调用改新签名 | `app.go:769` `a.Usage.StartBackgroundSync(5 * time.Minute)` | PASS |
| 内部 fallback 处理 nil ctx | `internal/usage/sync.go:184-187` `if ctx == nil { ctx = context.Background() }` | PASS |
| ctx 生命周期：Close 时取消 goroutine | `app.go:650` 接收的 ctx 是 Wails app-ctx；`sync.go:193` `<-ctx.Done()` 触发 return；Wails 关闭时该 ctx 被 cancel | PASS |
| 19 个前端 API 签名不变 | d.ts 计数：Close/DeleteModelPricing/GetDailyTrends/GetModelPricing/GetModelStats/GetProviderStats/GetRequestLogs/GetSyncState/GetUnknownModels/GetUsageSummary/Load/Pricing/Record/RecordForce/ResetModelPricing/StartBackgroundSync/SyncAll/SyncSessionUsage/UpsertModelPricing = 19；Go 源码导出方法也是 19（`grep -E "^func \(s \*Service\) [A-Z]"`） | PASS |

### M2 — 5 个 OpenAI 模型补入 pricing_seed — PASS

价格数值核对（USD/1M tokens → micro-USD/M = ×1e6）：

| ModelPattern | 任务给定 | seed 实际（pricing_seed.go:67-81） | 一致 |
|---|---|---|---|
| gpt-5.6-sol | 5 / 0.50 / 6.25 / 30 | 5_000_000 / 500_000 / 6_250_000 / 30_000_000 | PASS |
| gpt-5.6-terra | 2.5 / 0.25 / 3.125 / 15 | 2_500_000 / 250_000 / 3_125_000 / 15_000_000 | PASS |
| gpt-5.6-luna | 1 / 0.10 / 1.25 / 6 | 1_000_000 / 100_000 / 1_250_000 / 6_000_000 | PASS |
| gpt-5.5 | 5 / 0.50 / 0 / 30 | 5_000_000 / 500_000 / 0 / 30_000_000 | PASS |
| gpt-5.3-codex | 1.75 / 0.175 / 0 / 14 | 1_750_000 / 175_000 / 0 / 14_000_000 | PASS |

其余属性：
- isBuiltin=true、provider=openai、currencyCode=USD、notes 标注合理（pricing_seed.go:67-81）
- NormalizeModelID 自洽：`gpt-5.6-sol` 等命名无 `/` `@` `:字母`，经 NormalizeModelID 后不变（cost.go:18-39 逻辑）；测试 `TestNormalizeModelID`（usage_test.go:26）与 `TestPricingSeedOpenAIGPT56`（usage_test.go:483-522）双重断言 PASS。

### M3 — daily_rollup 分区刷新 — PASS

| 验收点 | 证据 | 结果 |
|---|---|---|
| 分区 SQL：事务内 DELETE WHERE day IN (?) + INSERT | `internal/usage/store_sqlite.go:276` BeginTx；`319-322` DELETE；`324-348` INSERT SELECT WHERE strftime IN (...)；`349` Commit；`280` defer Rollback | PASS |
| placeholder 与 days 严格对齐 | store_sqlite.go:311-317 构造 `placeholders[i]="?"`、`args[i]=d`，去重排序由 uniqueSortedDays 保证 | PASS |
| len(days)==0 走全量分支（测试/兜底） | store_sqlite.go:282-307 | PASS |
| 受影响 days 采集完整 | sync.go:91-95（Claude，仅 isNew）、119-123（Codex，仅 isNew）、140-144（OpenCode，REPLACE 也算）、151（始终加今天兜底 proxy 直写） | PASS |
| 串行化：affectedMu 保护 map | sync.go:62 `affectedMu sync.Mutex`；91/119/140 三处加锁 | PASS |
| 测试真的断言等价 | usage_test.go:378-396 分区刷新后再做一次全量刷新，断言 `got == got2`，且 day1 未受影响（requests=1）、day2 重算（requests=2） | PASS |

### M4 — 删 itoa/itoa64，改 strconv — PASS

| 验收点 | 证据 | 结果 |
|---|---|---|
| 全仓无残留 | `grep -rn "itoa\b\|itoa64\b\|func itoa" --include="*.go" .`（排除 vendor）= 0 命中 | PASS |
| strconv 用法正确 | sync.go:166 `strconv.Itoa(filesScanned)`（int）；167/168 `strconv.FormatInt(recordsAdded/processedCount, 10)`（int64）；usage_e2e_test.go:64 `strconv.Itoa(s.CacheCreationInputTokens)`（int 字段，见 claude/jsonl.go:252） | PASS |
| e2e 测试同步更新 | usage_e2e_test.go:14 新增 `"strconv"` 导入；唯一引用点 line 64 已替换 | PASS |

### M5 — upsertRecord 返回 isNew + processedCount 兼容新增 — PASS

| 验收点 | 证据 | 结果 |
|---|---|---|
| upsertRecord "先查再 REPLACE" 返回 isNew | store_sqlite.go:150-181：`SELECT 1 FROM usage_records WHERE dedup_key=?` → `errors.Is(probeErr, sql.ErrNoRows)` 决定 isNew；再 `INSERT OR REPLACE` | PASS |
| isNew 判定正确（无竞态） | upsertRecord 唯一调用方是 `recordForceInternal`（service.go:149），后者唯一调用方是 `syncOpenCode`（sync.go:434）。syncOpenCode 在 SyncAll 主 goroutine 串行执行（sync.go:130-146，无 goroutine），与 Claude/Codex 并发段不重叠。 | PASS |
| 公共 RecordForce 签名不变 | service.go:129-132 `func (s *Service) RecordForce(evt UsageEvent) error`，丢弃 isNew；d.ts:32 `RecordForce(arg1:usage.UsageEvent):Promise<void>;` | PASS |
| recordsAdded 只计真正新增 | sync.go:271-274（Claude，isNew 来自 INSERT OR IGNORE 的 RowsAffected>0）、358-361（Codex，同）、440-442（OpenCode，isNew 来自 recordForceInternal） | PASS |
| processedCount 在 err==nil 后才 ++ | sync.go:271/357/439 三处均先 `if err != nil { continue }` 再 `processed++` | PASS |
| SyncResult 向后兼容 | api_types.go:99-107：仅新增 `ProcessedCount int64 json:"processedCount"`，旧字段 startedAt/finishedAt/duration/recordsAdded/filesScanned/errors 全保留 | PASS |
| models.ts SyncResult 同步 | frontend/wailsjs/go/models.ts:2740-2783：新增 `processedCount: number`，其余字段保留 | PASS |
| SyncSessionUsage 透传 | api.go:78 `ProcessedCount: s.syncMeta.ProcessedCount` | PASS |
| 真机 e2e 验证幂等 | 测试运行结果：首次 added=27694 processed=35857；二次 added=0 processed=0 delta=0 | PASS |
| 测试覆盖 | usage_test.go:399-449 TestRecordForceIsNewSemantic 覆盖：首次 isNew=true、二次（改数据 REPLACE）isNew=false、RecordForce 公共签名未变、count==1 | PASS |

## 验证证据（亲自重跑）

| 命令 | 结果 |
|---|---|
| `go vet ./...` | PASS（无输出） |
| `go build -mod=vendor ./...` | PASS（无输出） |
| `go test -mod=vendor -count=1 ./internal/usage/... ./internal/appmeta/... ./internal/proxy/...` | 5 包全 PASS（usage 1.204s / claude 0.791s / codex 0.402s / opencode 1.596s / proxy 1.986s） |
| `go test -mod=vendor -count=1 -run "TestDailyRollupPartitionRefresh\|TestRecordForceIsNewSemantic\|TestPricingSeedOpenAIGPT56\|TestDailyRollupRefresh\|TestNormalizeModelID" -v ./internal/usage/...` | 5 测试全 PASS |
| `go test -mod=vendor -tags manual_e2e -run TestRealSyncAllE2E -v ./internal/usage/...` | PASS；首次 added=27694 processed=35857 errors=0；二次 added=0 processed=0 delta=0；summary requests=27694 |
| `go test -mod=vendor -count=1 -race ./internal/usage/...` | PASS（无数据竞争） |
| `npm --prefix frontend run build` | PASS（vue-tsc + vite 全绿；唯一 warning 是既有 chunk>500kB 提示，与本修复无关） |
| `grep -rn "Ctx\b" frontend/wailsjs/go/models.ts`（M1 关键检查） | 0 命中——Ctx 字段未污染 models.ts |

## 回归确认

| 关注点 | 结果 |
|---|---|
| 前端 import 路径 | `frontend/src/api/usage.ts:23` 仍从 `'../../wailsjs/go/usage/Service'` 导入，19 函数名未变 | 未受影响 |
| proxy 钩子 | `internal/proxy/service.go` / `internal/proxy/usage.go` 未修改；`app.go:746-764` 仍用 `s.Usage.Record`（INSERT OR IGNORE），非 RecordForce | 未受影响 |
| cost/cache 语义 | cost.go 未修改；`ComputeBillableInput` claudecode 不扣减 / codex 饱和扣减 / opencode 不参与，分叉保持 | 未受影响 |
| 去重（dedup_key） | insertRecord（IGNORE）+ upsertRecord（REPLACE）两路语义清晰；hash16 与 prefix 不变 | 未受影响 |
| Claude/Codex 同步并发 | sync.go:60 `sem = make(chan struct{}, syncConcurrency)` 仍限制 4 并发；errorsMu/affectedMu 锁保持 | 未受影响 |
| 公共 API 计数 | 19 个 exported 方法，与 d.ts 一致 | 未受影响 |
| 其他包（config/proxy/appmeta 等） | 本次未触碰 | 未受影响 |

## Minor 观察（不阻断，记入剩余风险）

1. **M5 - upsertRecord 注释与实际语义有轻微出入**：`store_sqlite.go:148-149` 注释称"单写连接（`SetMaxOpenConns(1)`）下查询与写入天然串行，无并发竞态"。严格地说，`database/sql` 在 `QueryRowContext.Scan()` 完成后即归还连接到池，再到 `ExecContext` 取连接之间存在间隙；`SetMaxOpenConns(1)` 不能阻止两条 SQL 之间的同包内交错。**但**：`upsertRecord` 唯一调用链是 `recordForceInternal` → `syncOpenCode`（SyncAll 主 goroutine 串行执行，sync.go:130-146），不存在并发调用方，实际无竞态。建议将注释改为"由调用方串行化保证无并发"以避免误导未来维护者。Minor。
2. **M3 - UTC 午夜边界漂移**：鲁班已自报（report.md:133）。`affectedDays` 始终加入"今天"（`time.Now().UTC()`），proxy 实时路径写 `occurred_at = evt.OccurredAt.UTC()`（service.go:201）。在 UTC 23:59 写入的记录可能落在"昨天"，但 rollup 用"今天"覆盖。当前业务对单日聚合精度未到该粒度，可接受。Minor。

## 剩余风险

- 真机 e2e 仅在主上 macOS 跑过；Windows 路径已被 e2e 测试显式 skip（usage_e2e_test.go:137-139），未做 Windows 端到端验证。鲁班已声明。
- 真机 e2e 第二次 processed=0 严格依赖"所有源游标命中后跳过 stub 提取"——该假设在 OpenCode 增量游标（`time_updated > last_sync`）和 Claude/Codex（mtime+size 未变跳过）下成立，已被实测验证。

## 回流清单

无。可直接回流 Leader 进入提交门（`diting_pass` 已签）。

## 报告路径

- `/Users/maorun/maorun-workpace/amagi-codebox/agent-outputs/reviewer/usage-minor-polish-review.md`
