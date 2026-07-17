---
title: "amagi-codebox 使用统计（AI 模型用量与成本统计）实现设计"
doc_type: design-doc
task_tier: complex
owner: fuxi (architect)
downstream:
  - luban (backend Go)
  - luoshen (frontend Vue3)
status: draft_pending_leader_review
created: 2026-07-17
source_artifacts:
  - internal/settings/service.go
  - internal/proxy/service.go
  - internal/proxy/usage.go
  - internal/appmeta/claude/jsonl.go
  - internal/session/types.go
  - app.go (NewApp/Startup/LaunchSession/defaultConfigDir)
  - main.go
  - frontend/src/views/LogsView.vue
  - frontend/src/router/index.ts
  - frontend/src/components/layout/SidebarNormal.vue
  - frontend/src/stores/session.ts
  - frontend/src/api/proxy.ts
canon_ref:
  - farion1231/cc-switch (Usage & Cost Tracking, Rust+Tauri+React+SQLite, 适配为 Go+Wails+Vue)
out_of_scope:
  - 移动端 Capacitor 前端（mobile/）
  - 远程 HTTP API 暴露 usage（暂仅桌面前端）
  - cost_multiplier 倍率机制（第一期固定 1.0，字段预留）
  - claude subagent/workflow 嵌套 jsonl 第一期不扫
---

# amagi-codebox 使用统计（Usage & Cost）实现设计

## 0. 摘要与决策速览（供主上确认）

本文档为鲁班（后端）和洛神（前端）提供可直接落地的实现蓝图，覆盖 Claude Code / Codex / OpenCode 三类 CLI 的用量与成本统计。

**四个拍板的关键技术决策：**

| # | 决策点 | 推荐 | 关键理由 |
|---|---|---|---|
| 1 | 存储选型 | **方案 A：引入 `modernc.org/sqlite`（纯 Go 无 CGO）** | 数据规模大、需跨源去重+多维聚合+增量同步状态；与 ccswitch 对齐；Wails 跨平台编译零 CGO 风险；JSON 模式在大数据集下查询/去重性能不可接受 |
| 2 | 成本计算精度 | **int64 micro-USD（1e-6 USD）整数计价** | 零新依赖；价格表用 micro-USD-per-million-tokens 整数存储；int64 范围（≤9.2×10^12 USD）远超任何现实值；JSON 序列化稳定无浮点误差 |
| 3 | 前端图表库 | **Chart.js 4 + vue-chartjs 5** | bundle 适中（按需 ~150KB gz）；TS 完整；覆盖折线/饼/柱/堆叠柱；与 Element Plus 主题可用 CSS 变量对齐 |
| 4 | 模型 ID 标准化 | **取最后 `/` 后片段 + 去 `:latest` 类标签 + `@`→`-` + 小写** | 兼容 `anthropic/claude-3-5-sonnet`、`gpt-4@2024-08-06`、`claude-3-5-sonnet:latest` 三类变体；保留日期戳（影响价格） |

**最大风险：** OpenCode SQLite（用户机器 637MB）的只读并发访问——必须用 `?mode=ro&_journal_mode=WAL&_busy_timeout=5000` 只读打开，避免锁住 OpenCode 自身运行；建议第一期将 OpenCode DB 复制为快照读取。详见第 15 章。

---

## 1. 功能范围与需求边界

### 1.1 范围内（第一期）

1. **三类 CLI 全量历史用量解析**：
   - Claude Code：`~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`
   - Codex：`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` + `~/.codex/session_index.jsonl`
   - OpenCode：`~/.local/share/opencode/opencode.db`（SQLite）
2. **实时 proxy 拦截**（辅助路径）：复用 `internal/proxy/usage.go` 已有的 `parseUsageFromJSON` 和 `SSEUsageAccumulator`，在 service.go:640 与 650 钩入。
3. **四维 token 计价**：input / output / cache_read / cache_creation 各自单价；按 app_type 处理 cache 语义分叉。
4. **内置价格表 seed + 用户可编辑**：覆盖 Claude 系 / GPT 系 / GLM 系 / DeepSeek / Kimi / MiniMax / Doubao 等；每个模型带 currency（USD/CNY）。
5. **跨源去重**：Claude 用 `message.id`；Codex 用复合 DedupKey + `INSERT OR IGNORE`；OpenCode 用 session.id；proxy 用 `request_id`。
6. **多维聚合查询**：模型 / 日期 / 供应商 / 数据源 / 会话。
7. **可视化前端**：日趋势折线、模型占比饼图、Token 四维堆叠柱、供应商/模型统计表、明细列表。

### 1.2 范围外（明确不做）

- 移动端 Capacitor 前端的 usage 视图（mobile/）
- 远程 HTTP API 暴露 usage 数据（仅桌面 Wails 绑定）
- `cost_multiplier` 倍率机制（字段预留，第一期固定 1.0）
- Claude Code subagent/workflow 嵌套 jsonl 的递归扫描（第一期仅扫根级 `type=="assistant"` 行；嵌套留待第二期）
- 跨设备同步、云端聚合、团队账户
- 自定义 dashboard 配置（前端布局固定）

### 1.3 非目标

- 不替代 OpenCode 自身的 cost 字段，优先采信其已聚合值；仅在其为 0 或解析失败时回退重算。
- 不与 Headroom 压缩统计合并（Headroom 已有独立 ledger，见 LogsView.vue:17-82）。
- 不向外部 API 上报数据。

---

## 2. 整体架构与端到端数据流

### 2.1 分层架构

```
┌──────────────────────────────────────────────────────────────────┐
│ 前端（Vue3 + Element Plus + Pinia + Chart.js）                   │
│  - UsageView.vue（四态骨架 + 仪表盘 + 图表 + 明细表）             │
│  - stores/usage.ts（Pinia setup style）                          │
│  - api/usage.ts（包装 wailsjs，try/catch + console.error）       │
└────────────────────────┬─────────────────────────────────────────┘
                         │ Wails 自动生成的 TS 绑定
                         │ frontend/wailsjs/go/usage/Service.ts
┌────────────────────────▼─────────────────────────────────────────┐
│ Wails 边界（app.go 装配 + main.go Bind）                          │
│  - App.Usage *usage.Service（新增字段）                           │
│  - Wails 暴露 usage.Service 全部 export 方法                      │
└────────────────────────┬─────────────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────────────┐
│ usage 核心包（internal/usage/）                                   │
│  ├─ service.go       —— Service 主体（持久化骨架仿 settings）     │
│  ├─ store_sqlite.go  —— SQLite 存储层                             │
│  ├─ cost.go          —— 成本计算 + 模型 ID 标准化                 │
│  ├─ pricing.go       —— 价格表 CRUD + seed                        │
│  ├─ sync.go          —— 增量同步调度                              │
│  ├─ aggregate.go     —— 聚合查询（model/day/provider/source）     │
│  ├─ api.go           —— 对外方法（GetUsageSummary 等）            │
│  └─ types.go         —— UsageRecord / UsageEvent / SyncState 等   │
└────────────────────────┬─────────────────────────────────────────┘
                         │ 通过回调注入 + 直接调用
        ┌────────────────┼─────────────────────┐
        │                │                     │
┌───────▼──────┐  ┌──────▼───────┐  ┌──────────▼──────────┐
│ appmeta/     │  │ appmeta/     │  │ appmeta/opencode/   │
│ claude/      │  │ codex/       │  │ （新建）             │
│ （扩展）     │  │ （新建）     │  │ 读 SQLite session 表 │
│ 解析 jsonl   │  │ 解析 jsonl   │  │                     │
└──────────────┘  └──────────────┘  └─────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ proxy 实时钩子（internal/proxy/service.go）                       │
│  - ProxyService 新增 onUsage / SetUsageSink / SetCurrentSession  │
│  - 钩子点：service.go:640（SSE 后 GetUsage）                      │
│           service.go:650（非 SSE parseUsageFromJSON）             │
│  - app.go Startup: a.Proxy.SetUsageSink(a.Usage.Record)          │
└──────────────────────────────────────────────────────────────────┘

持久化：~/.amagi-codebox/usage.db（SQLite，modernc.org/sqlite 驱动）
       ~/.amagi-codebox/usage-pricing.json（价格表，原子写）
```

### 2.2 端到端数据流（三条源）

**路径 1：Claude Code jsonl（主，全量历史）**
```
~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
  → appmeta/claude/ExtractUsageRecords（扩展 jsonl.go）
  → 逐行 JSON 解码，type=="assistant" 行
  → 取 message.id（去重键）、message.usage 四维、message.model、根级 timestamp
  → UsageRecord{app_type=claudecode, source=session_log, ...}
  → usage.Service.RecordBatch
  → SQLite INSERT OR IGNORE（UNIQUE 索引 ensure dedup_key）
```

**路径 2：Codex jsonl（主，全量历史）**
```
~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
  → appmeta/codex/ExtractUsageRecords（新建）
  → 解析 session_meta 首行（取 cwd、model_provider、model）
  → 解析 usage 行（input_tokens 包含 cache_read 语义）
  → billable_input = saturating_sub(input_tokens, cache_read_tokens)
  → DedupKey = hash(app + model + 四维token + timestamp)
  → SQLite INSERT OR IGNORE
```

**路径 3：OpenCode SQLite（主，全量历史）**
```
~/.local/share/opencode/opencode.db
  → appmeta/opencode/QuerySessions（新建）
  → 只读打开（?mode=ro&_busy_timeout=5000）
  → SELECT id, project_id, directory, title, model, cost,
           tokens_input, tokens_output, tokens_reasoning,
           tokens_cache_read, tokens_cache_write,
           time_created, time_updated
    FROM sessions
    WHERE time_updated > ?last_sync
  → 直接用 session.cost（已聚合），cost==0 时回退重算
  → 去重键 = "opencode:" + session.id
  → SQLite INSERT OR IGNORE
```

**路径 4：proxy 实时（辅）**
```
CLI 请求 → ProxyService.handleReverseProxy
  → accumulator / parseUsageFromJSON 提取 UsageData
  → 提取 model（extractModelFromRequest）、provider（inferProviderFromURL）
  → 取 sessionId（由 LaunchSession 注入 ProxyService.setCurrentSession）
  → onUsage(UsageEvent{...})
  → usage.Service.Record（INSERT OR IGNORE，dedup_key = "proxy:"+request_id）
```

### 2.3 数据流时序

```
应用启动 Startup
  ├─ Usage.Load()                  打开 SQLite，建表，加载价格表
  ├─ Usage.SyncAll() [goroutine]   异步触发三类源全量+增量同步
  └─ Proxy.SetUsageSink(a.Usage.Record)

用户打开 UsageView
  ├─ onMounted → stores/usage.fetchSummary()
  │            → api/usage.GetUsageSummary()
  │            → wailsjs → Usage.GetUsageSummary()
  ├─ 定时 30s 自动刷新（参考 LogsView 2s/Headroom 10s 模式）
  └─ 用户点「立即同步」→ Usage.SyncAll()（同步阻塞返回）

用户启动会话（代理模式）
  ├─ app.go LaunchSession 创建 sess 后
  ├─ a.Proxy.SetCurrentSession(sess.ID, provider, preset, appType)
  ├─ 请求处理结束（service.go:640 或 650）
  └─ accumulator.GetUsage() → a.Proxy.onUsage → a.Usage.Record

应用关闭 Shutdown
  └─ Usage.Close()（关闭 SQLite 句柄）
```

---

## 3. 数据模型（Go struct + SQLite schema）

### 3.1 核心记录：`UsageRecord`

`internal/usage/types.go`：

```go
package usage

import "time"

// Source 标识用量记录的数据来源。
type Source string

const (
    SourceSessionLog Source = "session_log" // 来自 CLI 自身的会话日志（jsonl/db）
    SourceProxy      Source = "proxy"       // 来自 amagi-codebox proxy 实时拦截
)

// AppType 复用 internal/session/types.go 的 AppType（claudecode/opencode/codex）。
// 这里用 alias 避免循环导入；实际在 service.go 里直接用 session.AppType。
// type AppType = session.AppType

// UsageRecord 是单条用量记录的规范结构，跨三类源 + proxy 实时统一。
//
// 设计要点：
//   - 所有 token 字段使用 int（四维 token 实际值不会超过 int32 范围，
//     但单次 batch 累加可能接近 int32 上限，用 int 留余量）。
//   - 成本字段使用 int64 micro-USD（1e-6 USD）。CNY 模型在价格表里
//     存 micro-CNY；本字段始终为 USD 等价（CNY→USD 换算在 pricing 层完成）。
//     —— 实际实现可选择：本字段语义 = "原生币种的 micro 单位"，
//     另存 currency_code 字段；前端按 currency_code 展示。
//   - dedup_key 是跨源去重的唯一键，SQLite 上建 UNIQUE 索引 + INSERT OR IGNORE。
type UsageRecord struct {
    // 主键（SQLite rowid 自增，不暴露给前端）
    ID int64 `json:"-"`

    // === 跨源去重键（UNIQUE 索引） ===
    // claudecode/session_log: "cc:msg_" + message.id
    // codex/session_log:      "cx:" + sha1(app|model|四维token|timestamp)[:16]
    // opencode/session_log:   "oc:" + session.id
    // proxy:                  "px:" + session_id + ":" + request_id
    DedupKey string `json:"dedupKey"`

    // === 来源与归属 ===
    AppType       string    `json:"appType"`       // claudecode / opencode / codex
    Source        Source    `json:"source"`        // session_log / proxy
    Provider      string    `json:"provider"`      // inferProviderFromURL 或 model_provider
    Model         string    `json:"model"`         // 原始模型名（未标准化）
    NormalizedModel string  `json:"normalizedModel"` // 标准化后用于匹配价格表
    SessionID     string    `json:"sessionId"`     // amagi session id 或外部 session 标识
    ProjectDir    string    `json:"projectDir"`    // 工作目录（若可识别）
    Preset        string    `json:"preset,omitempty"` // proxy 路径才有

    // === 四维 token ===
    InputTokens              int `json:"inputTokens"`
    OutputTokens             int `json:"outputTokens"`
    CacheReadInputTokens     int `json:"cacheReadInputTokens"`
    CacheCreationInputTokens int `json:"cacheCreationInputTokens"`

    // === 计费 token（已处理 cache 语义分叉）===
    // claudecode: billable_input = input_tokens（不减 cache_read）
    // codex:      billable_input = saturating_sub(input_tokens, cache_read_tokens)
    // opencode:   直接用 tokens_input 字段（语义由 OpenCode 决定，本期不二次处理）
    BillableInputTokens int `json:"billableInputTokens"`

    // === 成本（int64 micro-USD，按 model 的 currency 计算；原生币种见 CurrencyCode）===
    InputCost              int64 `json:"inputCost"`
    OutputCost             int64 `json:"outputCost"`
    CacheReadCost          int64 `json:"cacheReadCost"`
    CacheCreationCost      int64 `json:"cacheCreationCost"`
    TotalCost              int64 `json:"totalCost"`
    CurrencyCode           string `json:"currencyCode"` // "USD" / "CNY"

    // === 时间 ===
    OccurredAt time.Time `json:"occurredAt"` // CLI 事件时间（jsonl timestamp / opencode time_created）
    RecordedAt time.Time `json:"recordedAt"` // 入库时间

    // === 调试 ===
    RequestID string `json:"requestId,omitempty"` // proxy 路径填 amagi request_id
    RawLine   string `json:"-"`                   // 调试用：原始行（仅 DEBUG_USAGE 下保留）
}
```

### 3.2 实时事件：`UsageEvent`

proxy 钩子和同步器统一用此结构调用 `Service.Record`：

```go
// UsageEvent 是从 proxy 或 jsonl 解析出的原始事件，进入 Service 后转为 UsageRecord。
//
// 字段语义同 UsageRecord 同名字段；Service.Record 内部完成：
//   1. NormalizedModel = NormalizeModelID(Model)
//   2. 应用 cache 语义分叉（按 AppType 决定 BillableInputTokens）
//   3. 查价格表计算四维 Cost 与 TotalCost
//   4. 查价格表 CurrencyCode 回填
//   5. 生成 DedupKey（若调用方未提供）
//   6. INSERT OR IGNORE 入库
type UsageEvent struct {
    AppType       string
    Source        Source
    Provider      string
    Model         string
    SessionID     string
    ProjectDir    string
    Preset        string

    InputTokens              int
    OutputTokens             int
    CacheReadInputTokens     int
    CacheCreationInputTokens int

    OccurredAt time.Time
    RequestID  string // proxy 路径填
    DedupKey   string // 可选；空则由 Service 按 AppType 约定生成

    // OpenCode 专用：若 CostProvided=true，则直接使用 NativeCost，跳过价格表计算
    CostProvided bool
    NativeCost   int64  // OpenCode session.cost 转换而来的 micro-native-currency
}
```

### 3.3 价格表：`ModelPricing`

```go
// ModelPricing 是单个模型（或模型 pattern）的四维单价。
//
// 价格单位：micro-native-currency per million tokens。
//   - USD 模型：1.0 USD/M = 1_000_000 micro-USD/M
//   - CNY 模型：1.0 CNY/M = 1_000_000 micro-CNY/M
//
// 计算示例（Claude Sonnet 4，USD）：
//   input_per_million_usd = 3.00 → InputCostMicroUSD = tokens × 3_000_000 / 1_000_000
//                                               = tokens × 3（micro-USD）
type ModelPricing struct {
    ID                    string `json:"id"`                    // uuid 或 model_key
    ModelPattern          string `json:"modelPattern"`          // 标准化后的模型 ID（精确匹配）
    DisplayName           string `json:"displayName"`           // 展示名 "Claude Sonnet 4"
    Provider              string `json:"provider"`              // anthropic / openai / glm / ...
    CurrencyCode          string `json:"currencyCode"`          // "USD" / "CNY"

    InputPerMillion       int64  `json:"inputPerMillion"`       // micro-currency per 1M input tokens
    OutputPerMillion      int64  `json:"outputPerMillion"`
    CacheReadPerMillion   int64  `json:"cacheReadPerMillion"`
    CacheCreationPerMillion int64 `json:"cacheCreationPerMillion"`

    IsBuiltin             bool   `json:"isBuiltin"`             // seed 预置不可删（可改价）
    Notes                 string `json:"notes,omitempty"`       // "GLM-4.6 官方价"
    UpdatedAt             time.Time `json:"updatedAt"`
}
```

### 3.4 同步状态：`SyncState`

```go
// SyncState 记录每个被追踪的"源文件/源数据库"的增量同步游标。
// 每个源（jsonl 文件 / opencode.db）一行。
type SyncState struct {
    SourceType  string `json:"sourceType"`  // "claude_jsonl" / "codex_jsonl" / "opencode_db"
    SourceKey   string `json:"sourceKey"`   // 文件路径 或 "opencode_default"
    AppType     string `json:"appType"`

    // 文件类源（claude/codex jsonl）：
    LastMTime    int64 `json:"lastMTime"`    // Unix nano
    LastLineOffset int64 `json:"lastLineOffset"` // 已处理到的字节偏移（断点续传）

    // 数据库类源（opencode）：
    LastTimeUpdated int64 `json:"lastTimeUpdated"` // sessions.time_updated 的最大值

    LastSyncedAt time.Time `json:"lastSyncedAt"`
    LastError   string    `json:"lastError,omitempty"`
    RecordsAdded int64   `json:"recordsAdded"` // 累计入库（含已存在跳过的）
}
```

### 3.5 SQLite 完整 DDL

`internal/usage/store_sqlite.go` 中 `initSchema()`：

```sql
-- 主表：用量记录
CREATE TABLE IF NOT EXISTS usage_records (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    dedup_key               TEXT NOT NULL UNIQUE,
    app_type                TEXT NOT NULL,
    source                  TEXT NOT NULL,
    provider                TEXT NOT NULL,
    model                   TEXT NOT NULL,
    normalized_model        TEXT NOT NULL,
    session_id              TEXT NOT NULL DEFAULT '',
    project_dir             TEXT NOT NULL DEFAULT '',
    preset                  TEXT NOT NULL DEFAULT '',

    input_tokens            INTEGER NOT NULL DEFAULT 0,
    output_tokens           INTEGER NOT NULL DEFAULT 0,
    cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
    billable_input_tokens   INTEGER NOT NULL DEFAULT 0,

    input_cost              INTEGER NOT NULL DEFAULT 0,
    output_cost             INTEGER NOT NULL DEFAULT 0,
    cache_read_cost         INTEGER NOT NULL DEFAULT 0,
    cache_creation_cost     INTEGER NOT NULL DEFAULT 0,
    total_cost              INTEGER NOT NULL DEFAULT 0,
    currency_code           TEXT NOT NULL DEFAULT 'USD',

    occurred_at             INTEGER NOT NULL,  -- Unix nano
    recorded_at             INTEGER NOT NULL,
    request_id              TEXT NOT NULL DEFAULT ''
);

-- 聚合查询主力索引
CREATE INDEX IF NOT EXISTS idx_usage_occurred ON usage_records(occurred_at);
CREATE INDEX IF NOT EXISTS idx_usage_app_model ON usage_records(app_type, normalized_model);
CREATE INDEX IF NOT EXISTS idx_usage_provider ON usage_records(provider);
CREATE INDEX IF NOT EXISTS idx_usage_session ON usage_records(session_id);
CREATE INDEX IF NOT EXISTS idx_usage_source ON usage_records(source);

-- 同步游标
CREATE TABLE IF NOT EXISTS sync_state (
    source_type      TEXT NOT NULL,
    source_key       TEXT NOT NULL,
    app_type         TEXT NOT NULL,
    last_mtime       INTEGER NOT NULL DEFAULT 0,
    last_line_offset INTEGER NOT NULL DEFAULT 0,
    last_time_updated INTEGER NOT NULL DEFAULT 0,
    last_synced_at   INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    records_added    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (source_type, source_key)
);

-- 日归档（按日 + 模型 聚合，加速日趋势查询；由后台任务在同步后刷新）
CREATE TABLE IF NOT EXISTS daily_rollup (
    day              TEXT NOT NULL,  -- "2026-07-17"（UTC 或本地，二选一后固化）
    app_type         TEXT NOT NULL,
    normalized_model TEXT NOT NULL,
    provider         TEXT NOT NULL,
    currency_code    TEXT NOT NULL,

    input_tokens            INTEGER NOT NULL DEFAULT 0,
    output_tokens           INTEGER NOT NULL DEFAULT 0,
    cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
    billable_input_tokens   INTEGER NOT NULL DEFAULT 0,

    input_cost              INTEGER NOT NULL DEFAULT 0,
    output_cost             INTEGER NOT NULL DEFAULT 0,
    cache_read_cost         INTEGER NOT NULL DEFAULT 0,
    cache_creation_cost     INTEGER NOT NULL DEFAULT 0,
    total_cost              INTEGER NOT NULL DEFAULT 0,
    request_count           INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (day, app_type, normalized_model, provider, currency_code)
);

CREATE INDEX IF NOT EXISTS idx_rollup_day ON daily_rollup(day);
CREATE INDEX IF NOT EXISTS idx_rollup_model ON daily_rollup(normalized_model);
```

**为什么 daily_rollup 单独一张表**：
- 日趋势折线图（过去 30/90 天）直接 `SELECT FROM daily_rollup WHERE day BETWEEN ? AND ? GROUP BY day`，无需扫主表。
- 主表用于明细查询（带分页），daily_rollup 用于聚合图表。
- 维护：每次同步新增记录后，对当天 + 受影响历史天重算 rollup 行（`DELETE FROM daily_rollup WHERE day IN (?)` + `INSERT INTO daily_rollup SELECT ... FROM usage_records GROUP BY ...`）。

### 3.6 价格表存储（JSON 单文件，不用 SQLite）

理由：价格表行数少（< 200）、需要人类可编辑、原子写更简单。

`~/.amagi-codebox/usage-pricing.json`：

```json
{
  "version": 1,
  "models": [
    {
      "id": "claude-sonnet-4",
      "modelPattern": "claude-sonnet-4-20250514",
      "displayName": "Claude Sonnet 4",
      "provider": "anthropic",
      "currencyCode": "USD",
      "inputPerMillion": 3000000,
      "outputPerMillion": 15000000,
      "cacheReadPerMillion": 300000,
      "cacheCreationPerMillion": 3750000,
      "isBuiltin": true,
      "notes": "Anthropic 2025 定价",
      "updatedAt": "2026-07-17T00:00:00Z"
    }
  ],
  "fallbackPolicy": {
    "unknownModelStrategy": "zero_cost",
    "defaultCurrency": "USD"
  }
}
```

价格表 Service 完全仿 `internal/settings/service.go` 持久化骨架：

```go
type PricingService struct {
    configPath string
    data       *PricingData
    mu         sync.RWMutex
}

func NewPricingService(configDir string) *PricingService {
    return &PricingService{
        configPath: filepath.Join(configDir, "usage-pricing.json"),
        data:       defaultPricingData(),
    }
}

// Load / Save 与 settings.Service 完全同构：os.IsNotExist 兜底、.tmp + os.Rename 原子写。
```

---

## 4. 存储选型论证与最终方案

### 4.1 候选方案对比

| 维度 | 方案 A：SQLite (modernc.org/sqlite) | 方案 B：JSON/JSONL + 内存聚合 |
|---|---|---|
| **数据规模** | 单文件可承载数百万行无压力；ccswitch 报告用户历史达 637MB（OpenCode 自身 SQLite） | 全量加载到内存不可行（数百 MB JSON）；按文件分片后聚合需 O(N) 扫描 |
| **跨源去重** | `dedup_key UNIQUE + INSERT OR IGNORE` 原生支持，O(log N) | 内存维护 `map[string]struct{}`，启动需 O(N) 重建 |
| **多维聚合** | `GROUP BY model, day, provider` + 索引，毫秒级 | 全量遍历，10 万记录开始明显卡顿 |
| **增量同步** | `sync_state` 表天然合适 | 需要单独 JSON 维护 mtime+offset |
| **构建复杂度** | 引入 modernc.org/sqlite（纯 Go，无 CGO），需 `go mod vendor`；二进制 +约 10MB | 零新依赖 |
| **Wails 跨平台** | 纯 Go 无 CGO，Wails 编译零风险（ccswitch 的 Tauri 同样用 SQLite） | 零风险 |
| **与现有架构一致** | 项目现状是纯 JSON 文件，引入 SQLite 是新模式 | 一致 |
| **可观测性/调试** | sqlite3 CLI 直接查 | cat + jq |
| **并发写入** | WAL 模式下读写并发安全 | 需自己加锁 |
| **数据迁移** | schema_version 表 + 迁移脚本 | JSON 字段加减 |

### 4.2 最终建议：方案 A（SQLite + modernc.org/sqlite）

**核心理由**：

1. **数据规模现实**：Claude Code 单会话 jsonl 可能达数万行 assistant 记录；活跃用户累计上万会话；OpenCode 已观测到 637MB。JSON 全量加载方案在这种规模下不可行。
2. **跨源去重必须索引化**：`message.id` / DedupKey / `session.id` / proxy `request_id` 四种键跨三类源+实时路径，UNIQUE 约束 + `INSERT OR IGNORE` 是最低成本正确方案。
3. **增量同步游标需要持久化**：每个 jsonl 文件的 mtime+line_offset 必须跨重启保留，SQLite 行天然合适。
4. **聚合查询性能**：日趋势折线、模型占比、供应商统计都是 `GROUP BY` + 索引扫描；JSON 方案每次刷新都得全量重算。
5. **纯 Go 无 CGO**：`modernc.org/sqlite` 是 ccswitch 同款选型，Wails v2 跨 Windows/macOS 编译零障碍，不影响现有 `-mod=vendor` 流程。
6. **二进制体积可接受**：+10MB 在桌面应用场景无感。

**反对方 B 的代价被低估**：表面"零依赖"，实则把去重索引、聚合缓存、增量游标、原子并发全都重新发明一遍，且仍解决不了大数据集加载问题。

### 4.3 集成注意

- `go.mod` 添加 `modernc.org/sqlite`（直接依赖），其传递依赖（`modernc.org/libc` 等）由 `go mod tidy` 自动管理。
- **vendor 同步**：必须执行 `go mod tidy && go mod vendor` 后提交 `vendor/`（CLAUDE.md 明确："Vendored deps are committed; builds use `-mod=vendor`"）。
- 启用 WAL：`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;`（首次连接时执行）。
- 只读模式打开外部 SQLite（OpenCode）：DSN `file:<path>?mode=ro&_journal_mode=WAL&_busy_timeout=5000`。

---

## 5. 三类数据源解析方案

### 5.1 Claude Code（扩展 `internal/appmeta/claude/jsonl.go`）

**路径定位**：
```go
// 复用已有 pathSepReplacer 与 SessionJSONLPath
homeDir, _ := os.UserHomeDir()
projectsDir := filepath.Join(homeDir, ".claude", "projects")
// 枚举所有 encoded-cwd 子目录 → 枚举所有 .jsonl 文件
```

**新增 API（扩展 jsonl.go）**：

```go
// ExtractUsageRecords 解析单个 Claude jsonl 文件，提取用量记录。
//
// 路径：<homeDir>/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//
// 解析规则：
//   - 逐行 JSON 解码（bufio.Scanner，缓冲扩到 1MiB，复用 ExtractFirstUserMessage 模式）
//   - 仅处理 type == "assistant" 的行
//   - 从 message.usage 取四维 token：input_tokens / output_tokens /
//     cache_read_input_tokens / cache_creation_input_tokens
//   - 从 message.model 取模型名（如 "glm-5-turbo"）
//   - 从 message.id 取去重键（形如 "msg_xxx"，天然全局唯一）
//   - 从根级 timestamp 取 ISO8601 时间
//
// Anthropic 语义：input_tokens 是 fresh input，不含 cache_read。
// 调用方（usage.Service）按 AppType=claudecode 不做扣减。
//
// 返回值：
//   - records：解析出的 UsageEvent 列表（已设置 DedupKey="cc:msg_"+message.id，
//     AppType="claudecode"，Source=session_log，OccurredAt=timestamp）
//   - lastOffset：已读到的字节偏移（供 sync_state 断点续传用）
//   - err：文件级 IO 错误；单行解析失败 continue 不中断
func ExtractUsageRecords(jsonlPath string, startOffset int64) (records []UsageEventStub, lastOffset int64, err error)

// UsageEventStub 是 appmeta 层产出的事件壳，避免 appmeta 反向依赖 usage 包。
// usage.Service 内部把 Stub 转为 UsageEvent。
type UsageEventStub struct {
    DedupKey   string
    Model      string
    InputTokens int
    OutputTokens int
    CacheReadInputTokens int
    CacheCreationInputTokens int
    OccurredAt time.Time
    SessionID  string // 从文件主名提取（去掉 .jsonl）
    ProjectDir string // 从父目录名反推（去 encoded 或保留 encoded）
    RawMessageID string
}
```

**字段映射表**：

| jsonl 字段 | UsageEventStub 字段 | 备注 |
|---|---|---|
| `type=="assistant"` | （过滤条件） | 非 assistant 行跳过 |
| `message.id` | DedupKey = `"cc:msg_" + message.id` | 天然全局唯一 |
| `message.usage.input_tokens` | InputTokens | Anthropic fresh input，不减 cache |
| `message.usage.output_tokens` | OutputTokens | |
| `message.usage.cache_read_input_tokens` | CacheReadInputTokens | |
| `message.usage.cache_creation_input_tokens` | CacheCreationInputTokens | |
| `message.model` | Model | 如 `glm-5-turbo`、`claude-sonnet-4-20250514` |
| `timestamp`（根级） | OccurredAt | ISO8601 解析 |
| 文件主名 | SessionID | `<session-uuid>` |
| 父目录名 | ProjectDir（encoded） | `pathSepReplacer` 反向不完美，保留 encoded |

**实测真实样本**（主上已确认）：
```json
{"input_tokens":48386,"cache_creation_input_tokens":0,"cache_read_input_tokens":2240,"output_tokens":168,...}
```

**cache 语义**：claudecode（Anthropic 语义）—— `input_tokens` 不含 `cache_read`，**计价时不扣减**。

**增量策略**：基于 `sync_state.last_line_offset` 字节偏移断点续传；下次同步仅读 offset 之后的内容；mtime 未变直接跳过。

**subagent/workflow 嵌套 jsonl 第一期不扫**：仅扫根级 type=="assistant" 的行。文档与代码注释明确标注此限制。

**风险**：Claude Code schema 演进可能导致字段位置变化；单行解析失败不中断，记录到 `sync_state.last_error`。

### 5.2 Codex（新建 `internal/appmeta/codex/`）

**路径定位**：
```go
homeDir, _ := os.UserHomeDir()
sessionsRoot := filepath.Join(homeDir, ".codex", "sessions")
// 枚举 ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
// 索引文件：~/.codex/session_index.jsonl（可选用作加速；第一期直接枚举文件）
```

**新增 API**：

```go
package codex

// ExtractUsageRecords 解析单个 Codex rollout jsonl 文件。
//
// 格式（OpenAI Responses API）：
//   - 首行通常是 session_meta：含 cwd、cli_version、model_provider、source、model
//   - 后续行包含 usage 字段（input_tokens 等）
//
// OpenAI 语义：input_tokens **包含** cache_read。
// 调用方（usage.Service）按 AppType=codex 做扣减：
//   billable_input = saturating_sub(input_tokens, cache_read_tokens)
//
// cache 字段位置兼容（实测两种位置都出现过）：
//   - usage.cache_read_input_tokens（直接字段）
//   - usage.input_tokens_details.cached_tokens（嵌套字段）
// 解析时两者都尝试，取非零值；待鲁班实现时用真实文件确认确切字段位置。
//
// 去重：Codex 无全局 message.id；用 DedupKey = "cx:" +
//   sha1(app|model|input|output|cache_read|cache_creation|timestamp)[:16]
//
// 返回值同 claude.ExtractUsageRecords。
func ExtractUsageRecords(jsonlPath string, startOffset int64) (records []UsageEventStub, lastOffset int64, err error)
```

**字段映射表**（待鲁班实测确认）：

| jsonl 字段（推测） | UsageEventStub 字段 | 备注 |
|---|---|---|
| 首行 `session_meta.cwd` | ProjectDir | |
| 首行 `session_meta.model_provider` | Provider | |
| 首行 `session_meta.model` | Model | |
| `usage.input_tokens` | InputTokens | **含 cache_read** |
| `usage.output_tokens` | OutputTokens | |
| `usage.cache_read_input_tokens` 或 `usage.input_tokens_details.cached_tokens` | CacheReadInputTokens | **待实测确认位置** |
| 推测的 `usage.output_tokens_details.reasoning_tokens` | （归入 OutputTokens 或单独维度） | 第一期归入 output |
| 时间戳字段（推测 `timestamp` 或 `created_at`） | OccurredAt | **待实测确认字段名** |

**待鲁班实现时探查清单**：
1. 在用户机器上 `head -1 ~/.codex/sessions/2026/*/*/rollout-*.jsonl` 确认首行 session_meta schema
2. `grep -l '"usage"' ~/.codex/sessions/2026/*/*/*.jsonl | head -3` 后取一行观察 usage 字段位置
3. 确认时间戳字段名（`timestamp` / `created_at` / 根级 `time`）
4. 确认 cache_read 字段位置（直接 `cache_read_input_tokens` vs `input_tokens_details.cached_tokens`）

**cache 语义**：codex（OpenAI 语义）—— `input_tokens` **包含** `cache_read`，计价时**必须 saturating_sub 扣减**：
```go
billableInput := inputTokens
if inputTokens >= cacheReadTokens {
    billableInput = inputTokens - cacheReadTokens
} else {
    billableInput = 0 // saturating
    // 记日志：cache_read > input，疑似数据异常
}
```

**去重**：无全局 message.id，用复合 DedupKey + SQLite UNIQUE + INSERT OR IGNORE。

**增量策略**：同 Claude，基于 mtime + line_offset。

### 5.3 OpenCode（新建 `internal/appmeta/opencode/`）

**路径定位**：
```go
homeDir, _ := os.UserHomeDir()
dbPath := filepath.Join(homeDir, ".local", "share", "opencode", "opencode.db")
// 注意 WAL 文件 opencode.db-wal 与 opencode.db-shm 同目录，只读打开会自动处理
```

**新增 API**：

```go
package opencode

// QuerySessions 查询 OpenCode sessions 表，返回已聚合的 usage 与 cost。
//
// DSN（只读 + WAL + busy_timeout，避免锁住 OpenCode 自身）：
//   file:<dbPath>?mode=ro&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate
//
// 查询（参数化，sinceTimeUpdated=0 表示全量）：
//   SELECT id, project_id, directory, title, model,
//          cost, tokens_input, tokens_output, tokens_reasoning,
//          tokens_cache_read, tokens_cache_write,
//          time_created, time_updated, workspace_id, path, agent
//   FROM sessions
//   WHERE time_updated > ?
//   ORDER BY time_updated ASC
//   LIMIT 5000
//
// 时间换算（待鲁班实测确认单位）：
//   - time_created/time_updated 是 integer，推测为 Unix 毫秒或微秒。
//   - 探查方法：取一行，对比当前 time.Now().UnixMilli() 与 time_updated 数量级。
//   - 若 13 位 → 毫秒；16 位 → 微秒；10 位 → 秒。
//   - 第一期代码用 detectTimestampUnit() 自动嗅探：取最大 time_updated，
//     若 > 1e15 则视为纳秒；> 1e12 视为微秒；> 1e9 视为毫秒；否则视为秒。
//
// cost 处理：直接使用 sessions.cost（real 类型，已聚合）。
//   - 转换：micro-native = int64(cost * 1_000_000)（假设原币种为 USD，待确认）
//   - OpenCode 内部如何确定币种待鲁班查 opencode 源码或配置确认。
//   - 若 cost == 0 且 tokens 非零，回退到 usage.Service 的价格表重算。
//
// tokens_reasoning 处理（OpenCode 独有维度）：
//   - 第一期归入 output_tokens（推理输出），在 NormalizedModel 注释中标注。
//   - 第二期可独立展示。
//
// 返回值：
//   - stubs：解析出的事件列表（DedupKey="oc:"+session.id，AppType="opencode"）
//   - maxTimeUpdated：本次扫到的最大 time_updated，回写 sync_state.last_time_updated
//   - err：打开/查询错误
func QuerySessions(dbPath string, sinceTimeUpdated int64) (stubs []UsageEventStub, maxTimeUpdated int64, err error)
```

**字段映射表**：

| sessions 表列 | UsageEventStub 字段 | 备注 |
|---|---|---|
| `id` | DedupKey = `"oc:" + id`；SessionID = id | 天然主键 |
| `project_id` / `directory` / `path` | ProjectDir | 取 directory 优先，否则 path |
| `title` | RawMessageID（调试用） | |
| `model` | Model | 如 `gemini-2.5-pro`、`qwen-code` |
| `cost` | NativeCost + CostProvided=true | real → int64 micro |
| `tokens_input` | InputTokens | |
| `tokens_output` | OutputTokens | |
| `tokens_reasoning` | （并入 OutputTokens 或单独维度） | 第一期并入 output |
| `tokens_cache_read` | CacheReadInputTokens | |
| `tokens_cache_write` | CacheCreationInputTokens | |
| `time_created` | OccurredAt（候选） | 单位待嗅探 |
| `time_updated` | （用于增量游标） | maxTimeUpdated 回写 sync_state |

**cache 语义**：opencode 自带 cost 字段已聚合，第一期**直接使用**；cost==0 才回退重算。

**去重**：session.id 天然主键，DedupKey = `"oc:" + session.id`。

**增量策略**：基于 `sync_state.last_time_updated`（不用 mtime+line_offset，因为是数据库）。

**只读打开关键点**：
1. 必须用 `mode=ro`，避免 amagi 写入与 OpenCode 运行时冲突。
2. `_busy_timeout=5000` 容忍 OpenCode 长事务。
3. `_journal_mode=WAL` 不强求（只读连接无法修改 journal_mode，但 OpenCode 自身可能是 WAL）。
4. **建议第一期采用「快照拷贝」策略**：每次同步前 `cp opencode.db /tmp/usage-snapshot-<ts>.db`，读快照，读完删除。代价是磁盘 IO + 637MB 临时空间，但完全规避锁竞争。**该策略待鲁班实测后决定是否保留**——如果直接只读打开无锁问题，则去掉拷贝步骤。

---

## 6. 成本计算模型

### 6.1 四维 token 计价公式

```
input_cost         = billable_input_tokens         × input_per_million         / 1_000_000
output_cost        = output_tokens                 × output_per_million        / 1_000_000
cache_read_cost    = cache_read_input_tokens       × cache_read_per_million    / 1_000_000
cache_creation_cost= cache_creation_input_tokens   × cache_creation_per_million/ 1_000_000

total_cost = input_cost + output_cost + cache_read_cost + cache_creation_cost
```

**单位约定**：
- `*_per_million` 字段：micro-native-currency per 1M tokens（整数 int64）
- `*_cost` 字段：micro-native-currency（整数 int64）
- 计算示例：1000 tokens × 3_000_000 micro-USD/M ÷ 1_000_000 = 3000 micro-USD = 0.003 USD

### 6.2 cache 语义分叉决策表

| AppType / 路径 | 语义来源 | input_tokens 是否含 cache_read | billable_input_tokens 计算 | 备注 |
|---|---|---|---|---|
| claudecode (session_log) | Anthropic | 否（fresh input） | `= input_tokens` | 不扣减 |
| codex (session_log) | OpenAI Responses | **是** | `= saturating_sub(input_tokens, cache_read_tokens)` | 必须扣减 |
| opencode (session_log) | OpenCode 已聚合 | —— | 不参与计算（直接用 session.cost） | cost==0 才回退重算 |
| proxy 实时（Anthropic 后端） | inferProviderFromURL=="anthropic" | 否 | `= input_tokens` | |
| proxy 实时（OpenAI 兼容后端） | inferProviderFromURL∈{openai, glm, dashscope, minimax, deepseek} | **是**（保守假设） | `= saturating_sub(input_tokens, cache_read_tokens)` | 不确定时保守扣减，避免双计 |

`internal/usage/cost.go` 实现：

```go
// ComputeBillableInput 按 AppType 决定 input 是否扣减 cache_read。
func ComputeBillableInput(appType string, inputTokens, cacheReadTokens int) int {
    switch appType {
    case "claudecode":
        return inputTokens // Anthropic 语义：不扣减
    case "codex":
        return saturatingSub(inputTokens, cacheReadTokens) // OpenAI 语义：扣减
    case "opencode":
        return inputTokens // 由 OpenCode 自身决定，本期不处理
    default:
        // proxy 路径由调用方按 provider 显式传入 appType=claudecode/codex
        return inputTokens
    }
}

func saturatingSub(a, b int) int {
    if a >= b {
        return a - b
    }
    return 0
}
```

### 6.3 Decimal vs int64 微单位——选型与理由

| 方案 | 精度 | 依赖 | 序列化 | 运算稳定性 |
|---|---|---|---|---|
| `shopspring/decimal` | 任意精度 | 新增依赖 | JSON 字符串 | 稳定 |
| `float64` | 15-17 位有效数字 | 零依赖 | JSON number | 累积误差，禁用 |
| **`int64` micro-native-currency** | 1e-6 单位 | 零依赖 | JSON number（int64 范围内） | 完全稳定 |

**最终选择：int64 micro-native-currency**

**理由**：
1. 零新依赖，与 minimal-dependency 偏好一致。
2. 1e-6 精度足够（最小单位 0.000001 USD = 0.0001 分，远低于任何模型实际计价粒度）。
3. int64 范围 ±9.2×10^18，即使 micro-USD 也覆盖 ±9.2×10^12 USD，无溢出风险。
4. 价格表用 micro-per-million（int64）存储，前端展示时 `value / 1_000_000` 得到 USD/M。
5. 所有运算保持整数，无浮点累积误差。
6. JSON 序列化为 number，前端 `Number` 安全（int64 最大值远超 `Number.MAX_SAFE_INTEGER`，但实际值不会接近——单条记录成本通常 < 1 USD = 10^6 micro）。

**唯一边界情况**：极端聚合值（全历史总成本）。即便 100 万条记录 × 平均 0.1 USD/条 = 10^5 USD = 10^11 micro，仍远低于 int64 上限。前端展示时若担心精度，可序列化为字符串。

### 6.4 模型 ID 标准化算法

`internal/usage/cost.go`：

```go
// NormalizeModelID 把各种原始模型名变体标准化为价格表匹配键。
//
// 步骤：
//  1. 取最后一个 "/" 后的部分：去 vendor 前缀
//     "anthropic/claude-sonnet-4"  → "claude-sonnet-4"
//     "openai/gpt-4o"              → "gpt-4o"
//  2. 把 "@" 替换为 "-"：
//     "gpt-4@2024-08-06" → "gpt-4-2024-08-06"
//  3. 去掉 ":latest" ":free" 等字母标签，保留 ":YYYYMMDD" 日期戳：
//     "claude-3-5-sonnet:latest"  → "claude-3-5-sonnet"
//     "claude-3-5-sonnet:20241022" → "claude-3-5-sonnet:20241022" （保留日期）
//  4. 全小写
//
// 价格表匹配优先级（PricingService.Resolve）：
//  1. 精确匹配 NormalizedModel == ModelPattern
//  2. 前缀匹配 NormalizedModel 以 ModelPattern 开头（按 ModelPattern 长度降序）
//     例：表里 "claude-sonnet-4" 能匹配原始 "claude-sonnet-4-20250514"
//  3. 失配 → 未知模型兜底
func NormalizeModelID(raw string) string {
    s := strings.TrimSpace(raw)
    if s == "" {
        return ""
    }
    // 1. 去最后一个 "/" 前的前缀
    if idx := strings.LastIndex(s, "/"); idx >= 0 {
        s = s[idx+1:]
    }
    // 2. @ → -
    s = strings.ReplaceAll(s, "@", "-")
    // 3. 去 :latest :free :auto 等字母标签，保留 :<digits>
    if idx := strings.Index(s, ":"); idx >= 0 {
        head := s[:idx]
        tail := s[idx+1:]
        // tail 全数字视为日期/版本号保留
        if !isAllDigits(tail) {
            s = head
        }
    }
    // 4. 小写
    return strings.ToLower(s)
}

func isAllDigits(s string) bool {
    for _, r := range s {
        if r < '0' || r > '9' {
            return false
        }
    }
    return s != ""
}
```

**示例**：

| 输入 | 输出 |
|---|---|
| `anthropic/claude-sonnet-4-20250514` | `claude-sonnet-4-20250514` |
| `claude-sonnet-4:latest` | `claude-sonnet-4` |
| `claude-3-5-sonnet:20241022` | `claude-3-5-sonnet:20241022`（保留日期） |
| `gpt-4@2024-08-06` | `gpt-4-2024-08-06` |
| `openai/gpt-4o` | `gpt-4o` |
| `glm-4.6` | `glm-4.6` |
| `deepseek-chat` | `deepseek-chat` |
| `moonshot-v1-128k` | `moonshot-v1-128k` |

### 6.5 币种处理

- **价格表每个模型带 `currencyCode`**（USD 或 CNY），不是全局单一币种。
- **cost 字段语义**：`*_cost` 字段存储的是"该模型 currencyCode 下的 micro 单位"。即：
  - Claude Sonnet（USD）的记录：`total_cost` 单位是 micro-USD
  - GLM-4.6（CNY）的记录：`total_cost` 单位是 micro-CNY
- **聚合时**：按 `(day, model, provider, currency_code)` 分组（见 daily_rollup DDL），不同币种不混算。
- **前端展示**：每个模型/供应商按各自币种展示；汇总卡片可选择主币种（默认 USD），CNY 按 JSON 配置的固定汇率折算（第一期固定 `cny_to_usd: 0.14`，设置项可改）。

### 6.6 未知模型兜底

价格表 Resolve 失配时，三种策略可选，**推荐策略 1**：

1. **`zero_cost`（推荐）**：cost 字段全部置 0，token 照常记录；前端在模型列表标"无价格"徽章；UI 提供快速"为该模型设置价格"入口。
2. `placeholder_price`：用同 provider 默认价兜底（容易误导）。
3. `error`：拒绝入库（会丢数据，不推荐）。

`fallbackPolicy.unknownModelStrategy` 字段控制，默认 `"zero_cost"`。

### 6.7 cost_multiplier 倍率机制

**第一期固定 1.0，字段预留**：

```go
type ModelPricing struct {
    // ... 其他字段 ...
    Multiplier int `json:"multiplier,omitempty"` // 第一期固定 1000（表示 1.000×）；预留
}
```

前端 UI 隐藏该字段；第二期开放给用户做"折扣/加价"实验。

---

## 7. 跨源去重策略

### 7.1 各源去重键

| 源 | AppType | DedupKey 生成 | 碰撞概率 |
|---|---|---|---|
| Claude Code jsonl | claudecode | `"cc:msg_" + message.id` | 几乎 0（msg_xxx 是 Anthropic 全局 UUID） |
| Codex jsonl | codex | `"cx:" + sha1(model \| input \| output \| cache_read \| cache_creation \| timestamp)[:16]` | 极低（16 hex = 64 bit） |
| OpenCode SQLite | opencode | `"oc:" + session.id` | 0（session.id 是主键） |
| proxy 实时 | 由 LaunchSession 注入 | `"px:" + amagi_session_id + ":" + request_id` | 0 |

### 7.2 跨源重复处理

**问题场景**：用户开启 proxy 启动 Claude Code 会话，同一条 assistant 响应可能：
- 被实时 proxy 拦截记为 `px:` 记录
- 之后被 Claude jsonl 扫描记为 `cc:msg_xxx` 记录

两条记录 DedupKey 不同，会被双计入库。

**第一期策略（简单）**：
- 默认不去重跨源记录；前端提供 `source` 过滤器，用户可切换「仅 session_log / 仅 proxy / 全部」。
- 聚合查询默认按 `source=session_log` 显示（主路径），proxy 作为"实时预览"在单独区域展示。

**第二期策略（可选增强）**：
- proxy 记录入库时，记录 `proxy_session_id` + `proxy_approximate_time`（±5 分钟窗口）。
- session_log 同步后，反向标记匹配的 proxy 记录为 `superseded=true`（加字段），聚合时排除。
- 复杂度高，第一期不做。

### 7.3 SQLite 层保障

```sql
dedup_key TEXT NOT NULL UNIQUE  -- 主表约束
```

Go 代码：

```go
_, err := db.ExecContext(ctx, `
    INSERT OR IGNORE INTO usage_records
    (dedup_key, app_type, source, ...) VALUES (?, ?, ?, ...)
`, ...)
if err != nil {
    // 真错误（非 UNIQUE 冲突）
    return err
}
// INSERT OR IGNORE 静默跳过已存在的 dedup_key
```

---

## 8. 增量同步机制

### 8.1 调度时机

| 时机 | 触发 | 同步范围 | 备注 |
|---|---|---|---|
| 应用启动 | `Startup` 末尾 `go a.Usage.SyncAll()` | 三类源增量 | 异步，不阻塞 UI |
| 定时同步 | `time.Tick(5 * time.Minute)` | 三类源增量 | 后台 goroutine |
| 手动刷新 | 前端「立即同步」按钮 → `Usage.SyncAll()` 阻塞返回 | 三类源增量 | 同步完成后刷新视图 |
| proxy 实时 | `onUsage` 回调 | 单条记录 | 不走批量同步 |

### 8.2 文件类源（Claude / Codex）增量算法

```go
// 文件级断点续传
func (s *Service) syncOneJSONL(appType, path string) error {
    // 1. 取文件 mtime 与 size
    info, err := os.Stat(path)
    if err != nil { return err }

    // 2. 查 sync_state
    state := s.getSyncState(appType, path)
    if state.LastMTime == info.ModTime().UnixNano() &&
       state.LastLineOffset == info.Size() {
        return nil // 未变，跳过
    }

    // 3. 调对应 parser 从 LastLineOffset 开始解析
    var stubs []UsageEventStub
    var lastOffset int64
    switch appType {
    case "claudecode":
        stubs, lastOffset, err = claude.ExtractUsageRecords(path, state.LastLineOffset)
    case "codex":
        stubs, lastOffset, err = codex.ExtractUsageRecords(path, state.LastLineOffset)
    }
    if err != nil { return err }

    // 4. 批量入库
    added := s.recordBatch(stubs)

    // 5. 更新 sync_state
    s.updateSyncState(appType, path, info.ModTime().UnixNano(), lastOffset, added)
    return nil
}
```

**注意**：若 mtime 变了但 size 没变（理论上不会发生，但 Claude jsonl 不追加只覆盖时可能），保守从头重扫，覆盖更新（INSERT OR IGNORE 保证幂等）。

### 8.3 数据库类源（OpenCode）增量算法

```go
func (s *Service) syncOpenCode() error {
    dbPath := opencodeDBPath()
    state := s.getSyncState("opencode", "opencode_default")

    stubs, maxUpdated, err := opencode.QuerySessions(dbPath, state.LastTimeUpdated)
    if err != nil { return err }

    added := s.recordBatch(stubs)

    s.updateSyncStateOpencode(maxUpdated, added)
    return nil
}
```

### 8.4 枚举策略

```go
func (s *Service) SyncAll() error {
    // 1. Claude Code：枚举 ~/.claude/projects/*/*.jsonl
    claudeFiles := enumerateJSONLs(filepath.Join(home, ".claude", "projects"))
    for _, f := range claudeFiles {
        if err := s.syncOneJSONL("claudecode", f); err != nil {
            s.Log.Warn("usage.sync", "claude jsonl sync error", err.Error())
        }
    }

    // 2. Codex：枚举 ~/.codex/sessions/YYYY/MM/DD/*.jsonl
    codexFiles := enumerateJSONLs(filepath.Join(home, ".codex", "sessions"))
    for _, f := range codexFiles {
        if err := s.syncOneJSONL("codex", f); err != nil {
            s.Log.Warn("usage.sync", "codex jsonl sync error", err.Error())
        }
    }

    // 3. OpenCode
    if err := s.syncOpenCode(); err != nil {
        s.Log.Warn("usage.sync", "opencode db sync error", err.Error())
    }

    // 4. 刷新 daily_rollup
    s.refreshDailyRollup()

    return nil
}
```

**首次全量**：`sync_state` 表为空时，`last_*=0`，自动全量扫描。Claude 单次可能数千文件，并发处理（`semaphore.Weighted(4)` 限制 4 并发）。

### 8.5 daily_rollup 维护

```go
// refreshDailyRollup 重算受影响日期的 rollup。
// 简化版：每次同步后重算最近 90 天 + 当天；全量重算只在用户手动触发。
func (s *Service) refreshDailyRollup() error {
    // 简化策略：DELETE ALL + INSERT SELECT（数据量可控时）
    _, err := s.db.Exec(`
        DELETE FROM daily_rollup;
        INSERT INTO daily_rollup
            (day, app_type, normalized_model, provider, currency_code,
             input_tokens, output_tokens, cache_read_input_tokens,
             cache_creation_input_tokens, billable_input_tokens,
             input_cost, output_cost, cache_read_cost, cache_creation_cost,
             total_cost, request_count)
        SELECT
            substr(strftime('%Y-%m-%d', occurred_at / 1e9, 'unixepoch'), 1, 10) AS day,
            app_type, normalized_model, provider, currency_code,
            SUM(input_tokens), SUM(output_tokens),
            SUM(cache_read_input_tokens), SUM(cache_creation_input_tokens),
            SUM(billable_input_tokens),
            SUM(input_cost), SUM(output_cost),
            SUM(cache_read_cost), SUM(cache_creation_cost),
            SUM(total_cost), COUNT(*)
        FROM usage_records
        GROUP BY day, app_type, normalized_model, provider, currency_code;
    `)
    return err
}
```

**优化方向**（第二期）：只重算 `records_added > 0` 的日期分区。第一期简化版可接受（usage_records 万级时 DELETE+INSERT 在 WAL 模式下毫秒级）。

**时区决策**：`occurred_at / 1e9` 把 Unix nano 转 Unix 秒，`strftime(..., 'unixepoch')` 按 UTC。**第一期固定 UTC**，避免用户跨时区数据漂移；前端展示时按用户本地时区格式化日期。在 daily_rollup 的 day 字段注释中明确"UTC 日期"。

---

## 9. proxy 实时钩子方案

### 9.1 ProxyService 新增字段与方法

`internal/proxy/service.go` 修改（**仅添加，不改现有方法签名**）：

```go
type ProxyService struct {
    // ... 现有字段 ...

    // === 新增：usage 钩子 ===
    // onUsage 是注入的 usage sink；nil 时跳过（解耦，proxy 包不 import usage 包）
    onUsage       func(UsageEvent)
    // currentSession 是 LaunchSession 注入的当前会话上下文
    currentSession *currentSessionCtx
    sessMu         sync.RWMutex
}

// currentSessionCtx 携带当前活跃会话的关联信息。
type currentSessionCtx struct {
    SessionID string
    Provider  string
    Preset    string
    AppType   string // "claudecode" / "codex" / "opencode"
}

// UsageEvent 是 proxy 向 usage 包传递的事件结构（避免循环依赖，
// 此结构定义在 proxy 包；usage 包定义同名转换器）。
type UsageEvent struct {
    AppType       string
    Provider      string
    Model         string
    SessionID     string
    Preset        string

    InputTokens              int
    OutputTokens             int
    CacheReadInputTokens     int
    CacheCreationInputTokens int

    OccurredAt time.Time
    RequestID  string
}

// SetUsageSink 由 app.go 在 Startup 注入：a.Proxy.SetUsageSink(a.Usage.RecordFromProxy)
func (s *ProxyService) SetUsageSink(fn func(UsageEvent)) {
    s.mu.Lock()
    s.onUsage = fn
    s.mu.Unlock()
}

// SetCurrentSession 由 LaunchSession 在创建会话后注入。
// app.go LaunchSession 的 claudecode 分支（app.go:980 之后）调用。
func (s *ProxyService) SetCurrentSession(sessionID, provider, preset, appType string) {
    s.sessMu.Lock()
    s.currentSession = &currentSessionCtx{
        SessionID: sessionID,
        Provider:  provider,
        Preset:    preset,
        AppType:   appType,
    }
    s.sessMu.Unlock()
}

// ClearCurrentSession 在请求结束或会话停止时调用（第一期可选，由 LaunchSession 停止时清）。
func (s *ProxyService) ClearCurrentSession() {
    s.sessMu.Lock()
    s.currentSession = nil
    s.sessMu.Unlock()
}
```

### 9.2 钩子插入点

修改 `service.go` 的 `handleReverseProxy`（640 与 650 行附近）：

**SSE 分支（640 行附近）**：
```go
if isSSE {
    w.WriteHeader(resp.StatusCode)
    flusher, canFlush := w.(http.Flusher)
    accumulator := NewSSEUsageAccumulator()

    scanner := bufio.NewScanner(resp.Body)
    scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
    for scanner.Scan() {
        line := scanner.Text()
        accumulator.ProcessLine(line)
        w.Write([]byte(line))
        w.Write([]byte("\n"))
        if canFlush {
            flusher.Flush()
        }
    }
    // === 新增钩子：SSE 流结束后取累积 usage ===
    if usage := accumulator.GetUsage(); usage != nil && s.onUsage != nil {
        s.emitUsageEvent(usage, body, r)
    }
} else {
    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        w.WriteHeader(http.StatusBadGateway)
        fmt.Fprintf(w, `{"error":"read response: %s"}`, err.Error())
        return
    }
    w.Header().Set("Content-Length", fmt.Sprintf("%d", len(respBody)))
    w.WriteHeader(resp.StatusCode)
    w.Write(respBody)
    // === 新增钩子：非 SSE 从 body 提取 usage ===
    if usage := parseUsageFromJSON(respBody); usage != nil && s.onUsage != nil {
        s.emitUsageEvent(usage, body, r)
    }
}
```

```go
// emitUsageEvent 把 ProxyService.UsageData 转换为 UsageEvent 并调用 sink。
func (s *ProxyService) emitUsageEvent(data *UsageData, reqBody []byte, r *http.Request) {
    s.sessMu.RLock()
    ctx := s.currentSession
    s.sessMu.RUnlock()

    model := extractModelFromRequest(reqBody)
    provider := inferProviderFromURL(s.backendURL)
    if ctx != nil && ctx.Provider != "" {
        provider = ctx.Provider // 优先采信 LaunchSession 注入的 provider
    }

    evt := UsageEvent{
        Provider:  provider,
        Model:     model,
        OccurredAt: time.Now(),
        RequestID: r.Header.Get("x-request-id"), // 由 frontend/proxy 生成；若空则用 nanoid
        InputTokens:              data.InputTokens,
        OutputTokens:             data.OutputTokens,
        CacheReadInputTokens:     data.CacheReadInputTokens,
        CacheCreationInputTokens: data.CacheCreationInputTokens,
    }
    if ctx != nil {
        evt.SessionID = ctx.SessionID
        evt.Preset = ctx.Preset
        // AppType 按 provider 反推（proxy 路径只在 useProxy=true 时有效，
        // 而 Claude Code 才能走 Claude API；Codex/OpenCode 一般不走 proxy）：
        //   provider=="anthropic" → AppType="claudecode"
        //   其他 → AppType="claudecode"（保守，因为只有 Claude Code 默认接 proxy）
        // 注：amagi-codebox 现状 proxy 只对 Claude Code 启用，
        //     LaunchSession 的 opencode/codex 分支不会启用 proxy（待鲁班确认）。
        evt.AppType = ctx.AppType
    }

    // DedupKey 由 usage.Service 内部按 AppType 生成：
    //   "px:" + SessionID + ":" + RequestID

    // 调 sink（在 goroutine 里跑避免阻塞响应链）
    go s.onUsage(evt)
}
```

### 9.3 app.go 装配

```go
// NewApp 中创建：
app := &App{
    // ...
    Usage: usage.NewService(configDir, log),
}

// Startup 中（a.Proxy.LoadRules 后）：
if err := a.Usage.Load(); err != nil {
    a.Log.Warn("app", "加载使用统计失败", err.Error())
} else {
    a.Log.Info("app", "使用统计加载成功")
    a.Proxy.SetUsageSink(a.Usage.RecordFromProxy)
    go func() {
        if err := a.Usage.SyncAll(); err != nil {
            a.Log.Warn("usage", "首次同步失败", err.Error())
        }
        // 启动 5 分钟定时同步
        go a.Usage.StartBackgroundSync(5 * time.Minute, a.ctx)
    }()
}

// Shutdown 中：
if a.Usage != nil {
    _ = a.Usage.Close()
}
```

### 9.4 LaunchSession sessionId 注入

app.go:980（`sess := a.Sessions.Create(...)` 之后）：

```go
sess := a.Sessions.Create(session.AppTypeClaudeCode, providerName, presetName, model, launchMode, workDir, useProxy)
a.Log.Info("session", "会话已创建", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, model, launchMode))

// === 新增：注入 proxy 上下文（仅在 useProxy 时） ===
if useProxy {
    a.Proxy.SetCurrentSession(sess.ID, providerName, presetName, string(session.AppTypeClaudeCode))
}
```

**Codex / OpenCode 的 LaunchSession 分支**（如果它们也走 proxy）同样加注入；现状只有 claudecode 默认启用 proxy，待鲁班在实现时核对其他分支。

**会话停止时清空**（可选）：`a.Sessions.Stop` 或 `MarkExited` 路径里调 `a.Proxy.ClearCurrentSession()`。**第一期保守策略**：currentSession 是单值，新会话启动会覆盖；停止不清空也无大碍（只是后续 proxy 调用关联到已停止会话）。

---

## 10. 后端模块划分与文件清单

### 10.1 新建文件

```
internal/usage/
├── service.go          # Service 主体：Load/Save/Close/Record/RecordFromProxy/RecordBatch
├── types.go            # UsageRecord / UsageEvent(内部) / SyncState / Source / 常量
├── store_sqlite.go     # SQLite 存储层：openDB/initSchema/insert/aggregate 查询
├── pricing.go          # PricingService：Load/Save/Resolve/CRUD/seed
├── pricing_seed.go     # 内置价格表 seed 数据（分开文件便于维护）
├── cost.go             # NormalizeModelID/ComputeBillableInput/ComputeCost
├── sync.go             # SyncAll/syncOneJSONL/syncOpenCode/enumerateJSONLs
├── aggregate.go        # 刷新 daily_rollup；按维度聚合的 SQL 查询构造
├── api.go              # 对前端暴露的方法（GetUsageSummary 等；Wails 绑定）
└── usage_test.go       # 至少覆盖：模型ID标准化、cache语义分叉、cost计算、去重

internal/appmeta/codex/
├── parser.go           # ExtractUsageRecords
└── parser_test.go

internal/appmeta/opencode/
├── query.go            # QuerySessions / detectTimestampUnit
└── query_test.go
```

### 10.2 修改文件

| 文件 | 修改 |
|---|---|
| `internal/proxy/service.go` | 加 `onUsage/currentSession/sessMu` 字段；加 `SetUsageSink/SetCurrentSession/ClearCurrentSession` 方法；640/650 行钩入 `emitUsageEvent` |
| `internal/proxy/usage.go` | 新增 `UsageEvent` struct（proxy 包内） |
| `internal/appmeta/claude/jsonl.go` | 新增 `ExtractUsageRecords` 与 `UsageEventStub` |
| `app.go` | App struct 加 `Usage *usage.Service` 字段；NewApp 创建；Startup 加载+钩子+后台同步；Shutdown Close；LaunchSession 注入 SetCurrentSession |
| `main.go` | `Bind: []any{..., app.Usage}` |
| `go.mod` / `go.sum` / `vendor/` | `go get modernc.org/sqlite && go mod vendor` |

### 10.3 关键函数签名汇总

```go
// internal/usage/service.go
func NewService(configDir string, log *logging.Service) *Service
func (s *Service) Load() error
func (s *Service) Close() error
func (s *Service) Record(evt UsageEvent) error              // 单条入库（proxy 用）
func (s *Service) RecordFromProxy(evt proxy.UsageEvent) error // 桥接 proxy.UsageEvent → internal
func (s *Service) recordBatch(stubs []UsageEventStub) int64  // 批量入库（同步器用）
func (s *Service) SyncAll() error
func (s *Service) StartBackgroundSync(interval time.Duration, ctx context.Context)

// internal/usage/pricing.go
func NewPricingService(configDir string) *PricingService
func (p *PricingService) Load() error
func (p *PricingService) Save() error
func (p *PricingService) Resolve(normalizedModel string) (ModelPricing, bool)
func (p *PricingService) List() []ModelPricing
func (p *PricingService) Upsert(mp ModelPricing) error
func (p *PricingService) Delete(id string) error
func (p *PricingService) ResetBuiltin() error // 恢复 seed

// internal/usage/cost.go
func NormalizeModelID(raw string) string
func ComputeBillableInput(appType string, input, cacheRead int) int
func ComputeCost(model ModelPricing, rec UsageRecord) UsageRecord // 填充 *_cost 字段

// internal/usage/api.go（前端调用）
func (s *Service) GetUsageSummary(filter SummaryFilter) (Summary, error)
func (s *Service) GetDailyTrends(filter TrendFilter) ([]DailyTrendPoint, error)
func (s *Service) GetModelStats(filter StatFilter) ([]ModelStat, error)
func (s *Service) GetProviderStats(filter StatFilter) ([]ProviderStat, error)
func (s *Service) GetRequestLogs(filter LogFilter) ([]UsageRecord, error)
func (s *Service) SyncSessionUsage() (SyncResult, error)
func (s *Service) GetModelPricing() []ModelPricing
func (s *Service) UpsertModelPricing(mp ModelPricing) error
func (s *Service) DeleteModelPricing(id string) error
func (s *Service) ResetModelPricing() error
func (s *Service) GetSyncState() []SyncState
func (s *Service) GetUnknownModels() []string // 价格表失配的模型列表
```

---

## 11. 后端 API 契约（暴露给前端）

所有方法在 `internal/usage/api.go` 中实现，通过 `main.go` Bind 暴露给前端。Wails 自动生成 `frontend/wailsjs/go/usage/Service.ts`。

### 11.1 总览汇总

**Go**：
```go
type SummaryFilter struct {
    StartDate string `json:"startDate"` // "2026-07-01"，包含；空表示不限
    EndDate   string `json:"endDate"`   // "2026-07-17"，包含；空表示不限
    AppType   string `json:"appType"`   // "claudecode"/"codex"/"opencode"/""=all
    Source    string `json:"source"`    // "session_log"/"proxy"/""=all
    Provider  string `json:"provider"`  // ""=all
}

type Summary struct {
    TotalRequests      int64 `json:"totalRequests"`
    TotalInputTokens   int64 `json:"totalInputTokens"`
    TotalOutputTokens  int64 `json:"totalOutputTokens"`
    TotalCacheRead     int64 `json:"totalCacheRead"`
    TotalCacheCreation int64 `json:"totalCacheCreation"`
    TotalBillableInput int64 `json:"totalBillableInput"`

    // 按币种分组的成本（key = "USD"/"CNY"，value = micro-currency）
    TotalCostByCurrency map[string]int64 `json:"totalCostByCurrency"`

    // 主币种展示用（USD 为基准；CNY 按 fallbackPolicy 折算）
    TotalCostUSD int64 `json:"totalCostUSD"`

    DateRange struct {
        Start string `json:"start"`
        End   string `json:"end"`
    } `json:"dateRange"`
}

func (s *Service) GetUsageSummary(filter SummaryFilter) (Summary, error)
```

**TS**：
```ts
export interface SummaryFilter {
  startDate?: string;
  endDate?: string;
  appType?: 'claudecode' | 'codex' | 'opencode' | '';
  source?: 'session_log' | 'proxy' | '';
  provider?: string;
}
export interface Summary {
  totalRequests: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  totalCacheRead: number;
  totalCacheCreation: number;
  totalBillableInput: number;
  totalCostByCurrency: Record<string, number>;
  totalCostUSD: number;
  dateRange: { start: string; end: string };
}
```

### 11.2 日趋势

```go
type TrendFilter struct {
    SummaryFilter
    Granularity string `json:"granularity"` // "day"/"week"，默认 day
    Days        int    `json:"days"`        // 最近 N 天，默认 30；与 StartDate/EndDate 互斥
}

type DailyTrendPoint struct {
    Day           string           `json:"day"` // "2026-07-17"
    TotalCostUSD  int64            `json:"totalCostUSD"`
    CostByCurrency map[string]int64 `json:"costByCurrency"`
    InputTokens   int64            `json:"inputTokens"`
    OutputTokens  int64            `json:"outputTokens"`
    Requests      int64            `json:"requests"`
}

func (s *Service) GetDailyTrends(filter TrendFilter) ([]DailyTrendPoint, error)
```

查询：`SELECT day, SUM(total_cost), SUM(input_tokens), ... FROM daily_rollup WHERE day BETWEEN ? AND ? GROUP BY day`。多币种按 currency_code 分组，前端聚合为单值或双轴。

### 11.3 模型统计

```go
type ModelStat struct {
    NormalizedModel string `json:"normalizedModel"`
    DisplayName     string `json:"displayName"`     // 价格表的 displayName 或回退到 normalizedModel
    Provider        string `json:"provider"`
    CurrencyCode    string `json:"currencyCode"`
    AppType         string `json:"appType"`

    Requests       int64 `json:"requests"`
    InputTokens    int64 `json:"inputTokens"`
    OutputTokens   int64 `json:"outputTokens"`
    CacheRead      int64 `json:"cacheRead"`
    CacheCreation  int64 `json:"cacheCreation"`

    InputCost      int64 `json:"inputCost"`
    OutputCost     int64 `json:"outputCost"`
    CacheReadCost  int64 `json:"cacheReadCost"`
    CacheCreationCost int64 `json:"cacheCreationCost"`
    TotalCost      int64 `json:"totalCost"`

    HasPrice       bool  `json:"hasPrice"` // 是否在价格表中匹配到
}

func (s *Service) GetModelStats(filter StatFilter) ([]ModelStat, error)
```

### 11.4 供应商统计

```go
type ProviderStat struct {
    Provider     string `json:"provider"`
    Requests     int64  `json:"requests"`
    TotalCostUSD int64  `json:"totalCostUSD"`
    CostByCurrency map[string]int64 `json:"costByCurrency"`
    TotalTokens  int64  `json:"totalTokens"`
    ModelCount   int    `json:"modelCount"`
}

func (s *Service) GetProviderStats(filter StatFilter) ([]ProviderStat, error)
```

### 11.5 明细日志

```go
type LogFilter struct {
    SummaryFilter
    Model    string `json:"model"`
    Page     int    `json:"page"`     // 1-based
    PageSize int    `json:"pageSize"` // 默认 50，上限 500
}

// 返回的 UsageRecord 同 types.go 中的定义。
func (s *Service) GetRequestLogs(filter LogFilter) ([]UsageRecord, error)
```

### 11.6 同步

```go
type SyncResult struct {
    StartedAt       time.Time `json:"startedAt"`
    FinishedAt      time.Time `json:"finishedAt"`
    Duration        string    `json:"duration"`
    RecordsAdded    int64     `json:"recordsAdded"`
    FilesScanned    int       `json:"filesScanned"`
    Errors          []string  `json:"errors"`
}

// SyncSessionUsage 阻塞执行一次同步，返回结果（前端"立即同步"按钮用）。
func (s *Service) SyncSessionUsage() (SyncResult, error)

// GetSyncState 返回所有源的当前同步游标（前端调试与状态展示用）。
func (s *Service) GetSyncState() []SyncState
```

### 11.7 价格表 CRUD

```go
func (s *Service) GetModelPricing() []ModelPricing
func (s *Service) UpsertModelPricing(mp ModelPricing) error  // 新增或更新（按 ID）
func (s *Service) DeleteModelPricing(id string) error         // 内置模型返回错误
func (s *Service) ResetModelPricing() error                    // 恢复 seed
```

### 11.8 未知模型

```go
// GetUnknownModels 返回 usage_records 中存在但价格表未匹配的模型列表，
// 供前端"快速设置价格"入口使用。
func (s *Service) GetUnknownModels() ([]UnknownModel, error)

type UnknownModel struct {
    NormalizedModel string `json:"normalizedModel"`
    SampleRaw       string `json:"sampleRaw"`       // 样例原始模型名
    Requests        int64  `json:"requests"`
    LastSeen        string `json:"lastSeen"`
}
```

---

## 12. 前端方案

### 12.1 图表库选型与安装

**推荐：Chart.js 4 + vue-chartjs 5**

| 候选 | bundle (gzip) | TS 支持 | 覆盖图类型 | 学习曲线 | 推荐 |
|---|---|---|---|---|---|
| ECharts (vue-echarts) | ~300KB（按需） | 良好 | 全 | 中 | 备选 |
| **Chart.js (vue-chartjs)** | **~150KB** | **完整** | **折线/饼/柱/堆叠柱** | 低 | **推荐** |
| ApexCharts (vue3-apexcharts) | ~120KB | 良好 | 全 | 低 | 备选 |
| 纯 SVG 自绘 | 0 | —— | 自定义 | 高（开发量大） | 不推荐 |

**安装**（鲁班/洛神负责）：
```bash
npm --prefix frontend install chart.js vue-chartjs
# chart.js 自带类型定义；vue-chartjs 自带类型定义
```

**需要哪些图**：
1. **日趋势折线图**（Line）：X 轴日期，Y 轴双轴（左轴 USD 成本，右轴 token 数）
2. **模型占比饼图**（Doughnut）：按 totalCostUSD 切分
3. **Token 四维堆叠柱状图**（Stacked Bar）：X 轴模型，Y 轴 tokens，4 段堆叠
4. **供应商对比柱状图**（Bar）：X 轴 provider，Y 轴 USD
5. **明细表格**（Element Plus el-table）：分页、排序、筛选

### 12.2 路由与导航

`frontend/src/router/index.ts` 加：
```ts
{
  path: '/usage',
  name: 'Usage',
  component: () => import('../views/UsageView.vue')
},
```

`frontend/src/components/layout/SidebarNormal.vue` `navItems` 数组加：
```ts
{
  path: '/usage',
  label: '使用统计',
  icon: '<path d="M3 3v18h18"/><path d="M7 14l4-4 4 4 6-6"/>', // 简易折线图 SVG
},
```

### 12.3 api/usage.ts

完全仿 `frontend/src/api/proxy.ts` 模式：try/catch + `console.error('[api.usage.fn]', error)`。

```ts
/**
 * Usage Stats API
 * Encapsulates AI model usage and cost statistics.
 * Wraps wailsjs/go/usage/Service (auto-generated).
 */
import {
  GetUsageSummary,
  GetDailyTrends,
  GetModelStats,
  GetProviderStats,
  GetRequestLogs,
  SyncSessionUsage,
  GetSyncState,
  GetModelPricing,
  UpsertModelPricing,
  DeleteModelPricing,
  ResetModelPricing,
  GetUnknownModels,
} from '../../wailsjs/go/usage/Service';
import { usage } from '../../wailsjs/go/models';

type SummaryFilter = usage.SummaryFilter;
type Summary = usage.Summary;
// ... 其他类型别名

export async function getUsageSummary(filter: SummaryFilter): Promise<Summary> {
  try {
    return await GetUsageSummary(filter);
  } catch (error) {
    console.error('[api.usage.getUsageSummary]', error);
    throw error;
  }
}

export async function getDailyTrends(filter: TrendFilter): Promise<DailyTrendPoint[]> {
  try {
    return await GetDailyTrends(filter);
  } catch (error) {
    {
      console.error('[api.usage.getDailyTrends]', error);
      throw error;
    }
  }
}

// ... 其余方法同模式
```

### 12.4 stores/usage.ts（Pinia setup style，仿 stores/session.ts）

```ts
import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import * as usageApi from '../api/usage';
import type { usage } from '../../wailsjs/go/models';

export const useUsageStore = defineStore('usage', () => {
  // === State ===
  const summary = ref<usage.Summary | null>(null);
  const trends = ref<usage.DailyTrendPoint[]>([]);
  const modelStats = ref<usage.ModelStat[]>([]);
  const providerStats = ref<usage.ProviderStat[]>([]);
  const pricing = ref<usage.ModelPricing[]>([]);
  const unknownModels = ref<usage.UnknownModel[]>([]);

  const loading = ref(false);
  const error = ref('');
  const syncing = ref(false);
  const lastSyncedAt = ref<string>('');

  // === Filter ===
  const filter = ref<usage.SummaryFilter>({
    startDate: '',
    endDate: '',
    appType: '',
    source: 'session_log', // 默认只看 session_log（避免与 proxy 双计）
    provider: '',
  });

  // === Actions ===
  async function fetchAll() {
    loading.value = true;
    error.value = '';
    try {
      const [s, t, m, p] = await Promise.all([
        usageApi.getUsageSummary(filter.value),
        usageApi.getDailyTrends({ ...filter.value, days: 30 }),
        usageApi.getModelStats(filter.value),
        usageApi.getProviderStats(filter.value),
      ]);
      summary.value = s;
      trends.value = t;
      modelStats.value = m;
      providerStats.value = p;
    } catch (err) {
      error.value = String(err);
    } finally {
      loading.value = false;
    }
  }

  async function syncNow() {
    syncing.value = true;
    try {
      const result = await usageApi.syncSessionUsage();
      lastSyncedAt.value = result.finishedAt;
      await fetchAll();
      return result;
    } catch (err) {
      error.value = String(err);
      throw err;
    } finally {
      syncing.value = false;
    }
  }

  async function fetchPricing() {
    pricing.value = await usageApi.getModelPricing();
  }

  async function fetchUnknownModels() {
    unknownModels.value = await usageApi.getUnknownModels();
  }

  return {
    summary, trends, modelStats, providerStats, pricing, unknownModels,
    loading, error, syncing, lastSyncedAt, filter,
    fetchAll, syncNow, fetchPricing, fetchUnknownModels,
  };
});
```

### 12.5 UsageView.vue 页面结构

仿 `LogsView.vue` 四态骨架（loading/error/empty/success）+ CSS 仪表盘 + onMounted/onUnmounted 定时器：

```vue
<template>
  <section class="view-usage">
    <!-- Loading state -->
    <LoadingState v-if="loading && !summary" message="加载使用统计中..." />

    <!-- Error state -->
    <ErrorState v-else-if="error && !summary" :message="error" :on-retry="handleRetry" />

    <!-- Main content -->
    <template v-else>
      <PageHead title="使用统计" description="AI 模型用量与成本统计">
        <template #actions>
          <AppButton variant="ghost" size="small" @click="handleSync" :disabled="syncing">
            {{ syncing ? '同步中...' : '立即同步' }}
          </AppButton>
        </template>
      </PageHead>

      <!-- 空态：从未同步过且无数据 -->
      <EmptyState
        v-if="summary && summary.totalRequests === 0"
        icon=""
        title="暂无使用数据"
        description="点击右上角「立即同步」从 ~/.claude / ~/.codex / ~/.local/share/opencode 拉取历史"
      />

      <template v-else>
        <!-- 1. 仪表盘卡片（仿 LogsView Headroom 卡片） -->
        <ConfigCard class="summary-card">
          <!-- 4 维指标：累计请求 / 累计成本 / 累计 input / 累计 output -->
          <!-- 多币种时显示主币种 + 子文本"含 CNY ¥xxx" -->
        </ConfigCard>

        <!-- 2. 筛选器卡片 -->
        <ConfigCard>
          <!-- 日期范围 / AppType / Source / Provider 下拉 -->
        </ConfigCard>

        <!-- 3. 日趋势折线图 -->
        <ConfigCard>
          <h2>成本与 Token 趋势（最近 30 天）</h2>
          <Line :data="trendChartData" :options="trendChartOptions" />
        </ConfigCard>

        <!-- 4. 双列：模型占比饼图 + 供应商对比柱状图 -->
        <div class="grid-2">
          <ConfigCard>
            <h2>模型占比</h2>
            <Doughnut :data="modelPieData" :options="pieOptions" />
          </ConfigCard>
          <ConfigCard>
            <h2>供应商对比</h2>
            <Bar :data="providerBarData" :options="barOptions" />
          </ConfigCard>
        </div>

        <!-- 5. Token 四维堆叠柱状图 -->
        <ConfigCard>
          <h2>Token 分布（按模型）</h2>
          <Bar :data="tokenStackData" :options="stackOptions" />
        </ConfigCard>

        <!-- 6. 模型明细表 -->
        <ConfigCard>
          <div class="card-head">
            <h2>模型明细</h2>
            <span class="log-count">{{ modelStats.length }} 个模型</span>
          </div>
          <table class="usage-table">
            <thead>
              <tr>
                <th>模型</th><th>供应商</th><th>请求数</th>
                <th>Input</th><th>Output</th><th>Cache Read</th>
                <th>Cache Write</th><th>成本</th><th>状态</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="m in modelStats" :key="m.normalizedModel">
                <!-- ... -->
                <td>
                  <span v-if="!m.hasPrice" class="badge-warn">无价格</span>
                </td>
              </tr>
            </tbody>
          </table>
        </ConfigCard>

        <!-- 7. 价格管理入口 -->
        <ConfigCard>
          <div class="card-head">
            <h2>价格表</h2>
            <AppButton variant="ghost" size="small" @click="openPricingDialog">
              管理价格
            </AppButton>
          </div>
          <!-- 内置/自定义模型折叠列表 -->
        </ConfigCard>
      </template>
    </template>

    <!-- 价格编辑对话框 -->
    <PricingDialog v-model:open="pricingDialogOpen" :model="editingModel" @saved="handlePricingSaved" />
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue';
import { Line, Bar, Doughnut } from 'vue-chartjs';
import {
  Chart as ChartJS, CategoryScale, LinearScale, PointElement,
  LineElement, BarElement, ArcElement, Title, Tooltip, Legend,
  Filler, TimeScale,
} from 'chart.js';
import PageHead from '../components/ui/PageHead.vue';
import ConfigCard from '../components/ui/ConfigCard.vue';
import AppButton from '../components/ui/AppButton.vue';
import EmptyState from '../components/ui/EmptyState.vue';
import LoadingState from '../components/ui/LoadingState.vue';
import ErrorState from '../components/ui/ErrorState.vue';
import { useUsageStore } from '../stores/usage';
import { useToast } from '../composables/useToast';
import PricingDialog from '../components/usage/PricingDialog.vue';

ChartJS.register(
  CategoryScale, LinearScale, PointElement, LineElement,
  BarElement, ArcElement, Title, Tooltip, Legend, Filler,
);

const store = useUsageStore();
const { showSuccess, showError } = useToast();

const REFRESH_INTERVAL = 30000; // 30s（仿 LogsView 2s + Headroom 10s 模式）
let refreshTimer: number | null = null;

// === 计算属性：图表数据 ===
const trendChartData = computed(() => ({ /* datasets from store.trends */ }));
const modelPieData = computed(() => ({ /* from store.modelStats */ }));
const providerBarData = computed(() => ({ /* from store.providerStats */ }));
const tokenStackData = computed(() => ({ /* from store.modelStats */ }));

// === Handlers ===
async function handleRetry() {
  await store.fetchAll();
}
async function handleSync() {
  try {
    const result = await store.syncNow();
    showSuccess(`同步完成：新增 ${result.recordsAdded} 条`);
  } catch (err) {
    showError('同步失败: ' + err);
  }
}

// === Lifecycle ===
onMounted(async () => {
  await store.fetchAll();
  refreshTimer = window.setInterval(() => store.fetchAll(), REFRESH_INTERVAL);
});
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer);
});

watch(() => store.filter, () => store.fetchAll(), { deep: true });
</script>
```

### 12.6 四态交互

完全对齐 LogsView.vue：

| 状态 | 触发 | UI |
|---|---|---|
| loading（首次） | `onMounted` 中 `fetchAll` 进行时 | `<LoadingState message="加载使用统计中..." />` |
| error（首次失败） | `fetchAll` reject 且 summary 为 null | `<ErrorState :message="error" :on-retry="handleRetry" />` |
| empty（首次成功但无数据） | summary.totalRequests === 0 | `<EmptyState title="暂无使用数据" description="..." />` |
| success | summary.totalRequests > 0 | 完整图表 + 表格 |
| 后台刷新失败（已有数据） | 定时器/手动刷新失败 | 保留旧数据 + `console.warn`（不刷屏 toast，仿 LogsView:316-323） |
| 同步中 | 点「立即同步」后 | 按钮置灰显示"同步中..."，不阻塞主视图 |

### 12.7 新增前端组件清单

```
frontend/src/
├── views/UsageView.vue                       # 主视图
├── stores/usage.ts                           # Pinia store
├── api/usage.ts                              # API 封装
├── components/usage/
│   ├── PricingDialog.vue                     # 价格编辑对话框（新增/编辑模型）
│   ├── ModelPricingTable.vue                 # 价格表表格
│   ├── UsageChartLine.vue                    # 折线图封装（可选，也可内联）
│   ├── UsageChartDoughnut.vue                # 饼图封装
│   └── UsageChartBar.vue                     # 柱状图封装
└── utils/usage-format.ts                     # formatCost(micro, currency)、formatTokens(n)
```

---

## 13. 与现有架构集成点清单

| 集成点 | 文件 | 修改内容 |
|---|---|---|
| App struct 新增字段 | `app.go:94` | 加 `Usage *usage.Service` |
| App 构造 | `app.go:129 NewApp` | `Usage: usage.NewService(configDir, log)` |
| App 启动 | `app.go:647 Startup` | `a.Usage.Load()` + `a.Proxy.SetUsageSink(a.Usage.RecordFromProxy)` + `go a.Usage.SyncAll()` + `go a.Usage.StartBackgroundSync(...)` |
| App 关闭 | `app.go Shutdown` | `a.Usage.Close()` |
| Wails 绑定 | `main.go:49 Bind` | 加 `app.Usage` |
| proxy 钩子装配 | `internal/proxy/service.go` | 加字段、方法、钩入 640/650 |
| proxy 类型导出 | `internal/proxy/usage.go` | 加 `UsageEvent` struct |
| Claude 解析扩展 | `internal/appmeta/claude/jsonl.go` | 加 `ExtractUsageRecords`、`UsageEventStub` |
| Codex 新包 | `internal/appmeta/codex/parser.go` | 新建 |
| OpenCode 新包 | `internal/appmeta/opencode/query.go` | 新建 |
| sessionId 注入 | `app.go:980 LaunchSession` | `if useProxy { a.Proxy.SetCurrentSession(sess.ID, providerName, presetName, string(appType)) }` |
| 路由 | `frontend/src/router/index.ts` | 加 `/usage` |
| 导航 | `frontend/src/components/layout/SidebarNormal.vue:129 navItems` | 加 `{ path: '/usage', label: '使用统计', icon: '...' }` |
| 依赖 | `go.mod` + `vendor/` | `go get modernc.org/sqlite && go mod vendor` |
| 前端依赖 | `frontend/package.json` | `chart.js` + `vue-chartjs` |

---

## 14. 实现阶段划分与任务拆分

### 14.1 阶段划分

```
[阶段 1] 后端契约与骨架（1-2 天）
  ├─ go.mod 引入 modernc.org/sqlite + vendor
  ├─ internal/usage/types.go 完整 struct
  ├─ internal/usage/store_sqlite.go（建表 + CRUD）
  ├─ internal/usage/cost.go（NormalizeModelID + ComputeCost）
  ├─ internal/usage/pricing.go + pricing_seed.go
  └─ app.go / main.go 装配骨架（不接钩子）

[阶段 2] 前端契约对齐（0.5 天）
  ├─ wails dev 生成 wailsjs/go/usage/Service.ts
  ├─ frontend/src/api/usage.ts 包装
  └─ frontend/src/stores/usage.ts Pinia store（mock 数据跑通）

[阶段 3a] 后端解析器（鲁班，与 3b 并行）（2-3 天）
  ├─ appmeta/claude ExtractUsageRecords
  ├─ appmeta/codex parser（含实测探查）
  ├─ appmeta/opencode query（含实测探查）
  └─ sync.go 调度 + daily_rollup 刷新

[阶段 3b] 前端视图骨架（洛神，与 3a 并行）（2-3 天）
  ├─ UsageView.vue 四态骨架
  ├─ 图表组件（Line/Doughnut/Bar）
  ├─ 筛选器卡片
  └─ 明细表格

[阶段 4] proxy 钩子与实时路径（0.5 天，依赖 3a）
  ├─ ProxyService onUsage/SetUsageSink/SetCurrentSession
  ├─ LaunchSession sessionId 注入
  └─ app.go Startup 钩子装配

[阶段 5] 联调与验收（1 天）
  ├─ 三类源真机测试（Claude Code / Codex / OpenCode 各跑一次会话）
  ├─ 数据准确性核对（与 OpenCode 自带 cost 对齐）
  ├─ 前端四态测试（断网/空库/全量）
  └─ 性能测试（万级记录查询响应 < 500ms）
```

### 14.2 鲁班任务边界（后端）

1. 后端骨架（types + store + cost + pricing + seed）
2. 三类源解析器（claude/codex/opencode）
3. 同步调度（SyncAll + StartBackgroundSync + daily_rollup）
4. proxy 钩子（与 4 后可并行）
5. app.go / main.go 装配
6. 后端单元测试（模型ID标准化、cache语义、cost计算、去重幂等）
7. 写一份 `docs/usage.md` 简要说明配置目录、数据库位置、价格表格式

**鲁班产出可被洛神使用的最小契约（阶段 1 完成时即冻结）**：
- 所有 `internal/usage/api.go` 方法签名（见第 11 章）
- `wailsjs/go/usage/Service.ts` 自动生成（鲁班跑 `wails dev` 一次即产出）

### 14.3 洛神任务边界（前端）

1. 路由 + 导航项
2. api/usage.ts 包装
3. stores/usage.ts
4. UsageView.vue 主视图（四态骨架 + 筛选器 + 仪表盘卡片 + 三个图表 + 明细表）
5. PricingDialog.vue（价格编辑）
6. utils/usage-format.ts（成本/token 格式化）
7. 图表主题与 Element Plus / 现有 design token 对齐

**洛神不写**：后端方法、SQL、解析器、proxy 修改。

### 14.4 并行点与依赖

```
[阶段 1] 鲁班独占 ──┐
                    ├─→ [阶段 2] 契约生成（鲁班 wails dev 一次）─→ [阶段 3b] 洛神开始
                    │                                              （与 3a 并行）
                    └─→ [阶段 3a] 鲁班继续解析器 ──┐
                                                ├─→ [阶段 4] 鲁班钩子
                                                │
                                                └─→ [阶段 5] 联调
```

**关键契约冻结点**：阶段 1 完成时，第 11 章所有方法签名 **不得再变**（除非双方协商）。任何签名调整必须同步更新本文档第 11 章。

---

## 15. 风险与回滚

### 15.1 风险矩阵

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| **OpenCode SQLite 大文件（637MB）只读锁竞争** | 中 | 高（阻塞 OpenCode 自身运行） | 1) DSN `mode=ro&_busy_timeout=5000`；2) **第一期默认采用快照拷贝策略**：`cp opencode.db /tmp/usage-<ts>.db`，读快照；3) 待鲁班实测确认是否需要长期保留拷贝步骤 |
| **Codex usage 字段位置不确定** | 中 | 中（解析错误） | 鲁班实现时用真实文件实测；两种位置（直接字段 vs 嵌套）都兼容；解析失败跳过单行不中断 |
| **OpenCode time_created 单位不确定** | 中 | 低（时间戳错位） | `detectTimestampUnit()` 自动嗅探；按数量级判定纳秒/微秒/毫秒/秒 |
| **模型价格不准导致成本偏差大** | 高 | 中（用户体验） | 1) UI 明确标"价格表为参考值，以供应商账单为准"；2) 未知模型走 zero_cost 兜底，不臆造价格；3) 用户可手动编辑 |
| **modernc.org/sqlite 编译问题** | 低 | 高（阻塞构建） | 1) 纯 Go 无 CGO，Wails 兼容性好；2) go mod vendor 后离线构建可验；3) 兜底：回退到 `mattn/go-sqlite3`（CGO，最后选项） |
| **Claude jsonl schema 演进** | 中 | 低（部分字段缺失） | 单行解析失败 continue；sync_state.last_error 记录；前端显示解析警告 |
| **proxy 实时与 session_log 双计** | 高 | 低（默认过滤） | 默认 `filter.source=session_log`；前端提供切换；第二期增强 superseded 标记 |
| **首次全量同步慢**（数千文件） | 高 | 中（首次体验） | Startup 异步触发；前端显示进度（基于 `GetSyncState`）；并发 4 文件处理 |
| **daily_rollup 刷新慢** | 低 | 低 | WAL 模式 + 索引；万级记录 DELETE+INSERT 毫秒级；第二期分区刷新 |
| **int64 cost 前端精度**（极端聚合） | 低 | 低 | 单条记录成本通常 < 1 USD = 1e6 micro；聚合值仍远低于 `Number.MAX_SAFE_INTEGER`；如担心，序列化为 string |

### 15.2 回滚预案

1. **功能级回滚**：UsageView 路由与 navItems 是独立新增项；删除即可隐藏。后端 SQLite DB 文件独立，不影响其他功能。
2. **存储级回滚**：若 SQLite 引入后出现不可控问题，保留 `usage.Service` 的接口，把 `store_sqlite.go` 换成 `store_json.go`（JSON fallback 实现）；数据迁移脚本 `internal/usage/migrate_sqlite_to_json.go`。
3. **proxy 钩子回滚**：`SetUsageSink` 接受 nil 即跳过；只需 `a.Proxy.SetUsageSink(nil)` 即断开实时路径，session_log 路径不受影响。

---

## 16. 验收标准

### 16.1 功能验收

| # | 验收项 | 方法 |
|---|---|---|
| 1 | Claude Code jsonl 能解析出 assistant 行的四维 token 与 model | 单元测试 + 真机：跑一次 Claude Code 会话后查 usage_records 表 |
| 2 | Codex jsonl 能解析并应用 saturating_sub 扣减 cache_read | 单元测试 + 真机 |
| 3 | OpenCode SQLite 能读出 session.cost 与 tokens_* | 真机：与 OpenCode UI 显示对比误差 < 1% |
| 4 | proxy 实时钩子在 SSE 与非 SSE 两种响应下都能提取 usage | 真机：开 proxy 跑会话，立即在前端看到 proxy 来源的记录 |
| 5 | 跨源去重：同一条记录重复同步不产生重复行 | 单元测试：同步两次，count 不变 |
| 6 | 模型 ID 标准化：所有示例输入产出预期输出 | 单元测试覆盖第 6.4 节全部示例 |
| 7 | cache 语义分叉：claude 不扣、codex 扣 | 单元测试 |
| 8 | 价格表 CRUD：增删改持久化 | 真机：编辑价格 → 重启应用 → 数据保留 |
| 9 | 未知模型走 zero_cost 兜底，token 照记 | 真机：用一个价格表里没有的模型名测试 |
| 10 | 三类源同时启用时前端能正确聚合显示 | 真机：三源都有数据的用户机器 |

### 16.2 性能验收

| # | 验收项 | 阈值 |
|---|---|---|
| 1 | 首次全量同步 1000 个 jsonl 文件 | < 30 秒 |
| 2 | 增量同步（无变化） | < 1 秒 |
| 3 | GetUsageSummary（万级记录） | < 200ms |
| 4 | GetDailyTrends 90 天 | < 300ms |
| 5 | GetRequestLogs 分页 50 条 | < 100ms |
| 6 | 前端 UsageView 首次渲染 | < 2 秒 |

### 16.3 UI 验收

| # | 验收项 |
|---|---|
| 1 | loading / error / empty / success 四态完整且文案准确 |
| 2 | 后台刷新失败不刷屏（保留旧数据 + console.warn，仿 LogsView 模式） |
| 3 | 筛选器切换（日期/AppType/Source/Provider）触发刷新且不闪烁 |
| 4 | 「立即同步」按钮有 loading 反馈，完成后 toast 提示 |
| 5 | 价格管理对话框可新增、编辑、删除（内置模型禁用删除） |
| 6 | 未知模型在表格中显示"无价格"徽章 |
| 7 | 多币种场景（USD + CNY）汇总卡片正确显示 |
| 8 | 图表主题与 Element Plus dark/light 模式一致 |

### 16.4 代码质量验收

| # | 验收项 |
|---|---|
| 1 | `go vet ./...` 通过 |
| 2 | `go test ./internal/usage/... ./internal/appmeta/...` 通过 |
| 3 | 前端 `npm --prefix frontend run build`（含 vue-tsc）通过 |
| 4 | 无 TODO/FIXME/HACK 占位（测试文件除外） |
| 5 | 持久化骨架遵循 settings/service.go 模式（os.IsNotExist 兜底 + .tmp + Rename） |
| 6 | proxy 包不 import usage 包（解耦） |
| 7 | appmeta 子包不反向依赖 usage 包（用 UsageEventStub 中立结构） |

---

## 17. 未决项（待鲁班实测确认）

| # | 项 | 探查方法 | 影响 |
|---|---|---|---|
| 1 | Codex jsonl 中 usage 字段的精确位置（直接 `cache_read_input_tokens` vs 嵌套 `input_tokens_details.cached_tokens`） | `head -3 ~/.codex/sessions/2026/*/*/rollout-*.jsonl` 真实样本 | 解析器兼容两种位置 |
| 2 | Codex jsonl 时间戳字段名（`timestamp` / `created_at` / 根级 `time`） | 同上 | OccurredAt 解析 |
| 3 | OpenCode `time_created` 的 epoch 单位（秒/毫秒/微秒/纳秒） | 取一行对比 `time.Now().UnixMilli()` 数量级 | 时间换算（已设计 `detectTimestampUnit` 嗅探） |
| 4 | OpenCode `sessions.cost` 的币种假设（是否恒为 USD） | 查 OpenCode 源码或配置文件 | currency_code 字段回填 |
| 5 | OpenCode 只读打开是否真正无锁竞争（是否需要快照拷贝） | 真机：amagi-codebox 同步时让 OpenCode 跑会话，观察是否卡顿 | 同步策略 |
| 6 | Codex/OpenCode 的 LaunchSession 分支是否启用 proxy（现状仅 claudecode 默认开） | 读 app.go LaunchSession 完整代码 | proxy sessionId 注入是否需要扩展到其他分支 |
| 7 | OpenCode 是否有 `tokens_reasoning` 字段（DeepSeek-R1/o1 类） | schema 检查 | 是否单独展示还是归入 output |
| 8 | Claude Code subagent jsonl 的嵌套结构（第二期才扫） | 取一个使用过 subagent 的会话 jsonl 观察 | 第二期解析器设计 |

---

## 附录 A：ccswitch 对标说明

farion1231/cc-switch（Rust + Tauri + React + SQLite）的 Usage & Cost Tracking 模块为我们提供了基本架构验证：
- **存储**：SQLite（rusqlite crate）→ 我们用 modernc.org/sqlite（纯 Go）
- **三类源**：Claude/Codex/OpenCode → 我们完全对齐
- **去重**：message.id（Claude）/ hash（Codex）/ session.id（OpenCode）→ 我们对齐
- **价格表**：用户可编辑 → 我们对齐
- **差异**：ccswitch 是 React，我们是 Vue3；ccswitch 用 Tauri，我们用 Wails；ccswitch 走 CGO sqlite3，我们走纯 Go modernc

ccswitch 的存在证明此架构在桌面应用场景可行；我们的适配保留其核心思路，技术栈对齐 amagi-codebox 现状。

---

## 附录 B：价格表 seed 预置清单

`internal/usage/pricing_seed.go` 第一期预置（鲁班可按需扩充）：

**Claude 系（Anthropic，USD）**：
- claude-sonnet-4 / claude-sonnet-4-20250514
- claude-opus-4 / claude-opus-4-20250514
- claude-haiku-4 / claude-3-5-haiku-20241022
- claude-3-5-sonnet（含 :20241022）
- claude-3-opus
- claude-3-haiku

**GPT 系（OpenAI，USD）**：
- gpt-4o / gpt-4o-2024-08-06
- gpt-4o-mini
- gpt-4-turbo
- gpt-4
- o1 / o1-preview / o1-mini
- o3 / o3-mini
- gpt-4.1 / gpt-4.1-mini

**GLM 系（智谱，CNY）**：
- glm-4.6 / glm-4.6-air
- glm-4.5 / glm-4.5-air
- glm-4-plus
- glm-4-flash（免费，价格 0）
- glm-4-long
- glm-4v / glm-4v-plus（视觉）

**DeepSeek（CNY）**：
- deepseek-chat / deepseek-v3
- deepseek-reasoner / deepseek-r1
- deepseek-coder

**Kimi / Moonshot（CNY）**：
- moonshot-v1-8k / moonshot-v1-32k / moonshot-v1-128k
- kimi-k2

**MiniMax（CNY）**：
- abab6.5-chat
- minimax-text-01

**Doubao（字节，CNY）**：
- doubao-pro-32k / doubao-pro-128k
- doubao-lite-32k

**Gemini（Google，USD）**：
- gemini-2.5-pro / gemini-2.5-flash
- gemini-2.0-flash
- gemini-1.5-pro / gemini-1.5-flash

**Qwen（阿里，CNY）**：
- qwen-max / qwen-plus / qwen-turbo
- qwen-code

**推理模型特殊处理**：DeepSeek-R1 / o1 / o3 系列的 reasoning_tokens 第一期归入 output_tokens；价格表 Notes 字段标注"含 reasoning"。

---

**文档结束。**

主上确认后，建议分派鲁班从阶段 1（后端骨架）启动；洛神在阶段 2 完成后接入。
