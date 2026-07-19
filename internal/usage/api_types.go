package usage

import "time"

// SummaryFilter 是所有聚合查询的通用筛选器（设计 11.1）。
type SummaryFilter struct {
	StartDate string `json:"startDate"` // "2026-07-01"，UTC 日期，闭区间；空表示不限
	EndDate   string `json:"endDate"`   // "2026-07-17"，UTC 日期，闭区间；空表示不限
	AppType   string `json:"appType"`   // "claudecode"/"codex"/"opencode"/""=all
	Source    string `json:"source"`    // "session_log"/"proxy"/""=all
	Provider  string `json:"provider"`  // ""=all
}

// Summary 是仪表盘汇总（设计 11.1）。
type Summary struct {
	TotalRequests int64 `json:"totalRequests"`
	// TotalTokens is the complete, non-overlapping token count: fresh input +
	// output + cache-read + cache-write. It uses billable input so Codex cache
	// reads are not counted twice.
	TotalTokens        int64 `json:"totalTokens"`
	TotalInputTokens   int64 `json:"totalInputTokens"`
	TotalOutputTokens  int64 `json:"totalOutputTokens"`
	TotalCacheRead     int64 `json:"totalCacheRead"`
	TotalCacheCreation int64 `json:"totalCacheCreation"`
	TotalBillableInput int64 `json:"totalBillableInput"`

	// 按币种分组的成本（key="USD"/"CNY"，value=micro-currency）。
	TotalCostByCurrency map[string]int64 `json:"totalCostByCurrency"`

	// 主币种展示用（USD 基准；CNY 按固定汇率折算后求和）。
	TotalCostUSD int64 `json:"totalCostUSD"`

	DateRange SummaryDateRange `json:"dateRange"`
}

// SummaryDateRange 是实际数据的日期范围（不含筛选）。
type SummaryDateRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// TrendFilter 日趋势查询筛选器（设计 11.2）。
type TrendFilter struct {
	SummaryFilter
	Granularity string `json:"granularity"` // "day"/"week"，默认 day（第一期仅支持 day）
	Days        int    `json:"days"`        // 最近 N 天，默认 30；与 StartDate/EndDate 互斥
}

// DailyTrendPoint 是日趋势折线图的一个点（设计 11.2）。
type DailyTrendPoint struct {
	Day            string           `json:"day"` // "2026-07-17"
	TotalCostUSD   int64            `json:"totalCostUSD"`
	CostByCurrency map[string]int64 `json:"costByCurrency"`
	InputTokens    int64            `json:"inputTokens"`
	OutputTokens   int64            `json:"outputTokens"`
	Requests       int64            `json:"requests"`
}

// ModelDailyTrendPoint is one model's point on one day. It intentionally does
// not aggregate models: the dashboard can draw one line per model and keep
// models with very different prices or token volumes interpretable.
type ModelDailyTrendPoint struct {
	Day                 string `json:"day"`
	NormalizedModel     string `json:"normalizedModel"`
	DisplayName         string `json:"displayName"`
	Provider            string `json:"provider"`
	CurrencyCode        string `json:"currencyCode"`
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	CacheRead           int64  `json:"cacheRead"`
	CacheCreation       int64  `json:"cacheCreation"`
	BillableInput       int64  `json:"billableInput"`
	TotalTokens         int64  `json:"totalTokens"`
	CacheAdjustedTokens int64  `json:"cacheAdjustedTokens"`
	TotalCost           int64  `json:"totalCost"`
	TotalCostUSD        int64  `json:"totalCostUSD"`
}

// StatFilter 模型/供应商统计筛选器。
type StatFilter struct {
	SummaryFilter
}

// ModelStat 是模型维度的聚合行（设计 11.3）。
type ModelStat struct {
	NormalizedModel string `json:"normalizedModel"`
	DisplayName     string `json:"displayName"`
	Provider        string `json:"provider"`
	CurrencyCode    string `json:"currencyCode"`
	AppType         string `json:"appType"`
	Requests        int64  `json:"requests"`
	InputTokens     int64  `json:"inputTokens"`
	OutputTokens    int64  `json:"outputTokens"`
	CacheRead       int64  `json:"cacheRead"`
	CacheCreation   int64  `json:"cacheCreation"`
	BillableInput   int64  `json:"billableInput"`
	// TotalTokens is the complete non-overlapping token quantity. For Codex,
	// cached input is separated from raw input before being added back once.
	TotalTokens int64 `json:"totalTokens"`
	// CacheHitRate is cache-read / (fresh input + cache-read), in [0,1].
	CacheHitRate float64 `json:"cacheHitRate"`
	// CacheAdjustedTokens translates cache input by its configured discounted
	// price relative to normal input price, then adds output. It lets users see
	// the effective token consumption after a cache hit without hiding raw use.
	CacheAdjustedTokens int64 `json:"cacheAdjustedTokens"`
	InputCost           int64 `json:"inputCost"`
	OutputCost          int64 `json:"outputCost"`
	CacheReadCost       int64 `json:"cacheReadCost"`
	CacheCreationCost   int64 `json:"cacheCreationCost"`
	TotalCost           int64 `json:"totalCost"`
	// CacheReadEstimatedCost and CacheHitSavings are recomputed from the active
	// price table even if a source only supplies an aggregate native cost.
	CacheReadEstimatedCost int64 `json:"cacheReadEstimatedCost"`
	CacheHitSavings        int64 `json:"cacheHitSavings"`
	HasPrice               bool  `json:"hasPrice"`
}

// ProviderStat 是供应商维度的聚合行（设计 11.4）。
type ProviderStat struct {
	Provider       string           `json:"provider"`
	Requests       int64            `json:"requests"`
	TotalCostUSD   int64            `json:"totalCostUSD"`
	CostByCurrency map[string]int64 `json:"costByCurrency"`
	TotalTokens    int64            `json:"totalTokens"`
	ModelCount     int              `json:"modelCount"`
}

// LogFilter 明细日志查询筛选器（设计 11.5）。
type LogFilter struct {
	SummaryFilter
	Model    string `json:"model"`
	Page     int    `json:"page"`     // 1-based
	PageSize int    `json:"pageSize"` // 默认 50，上限 500
}

// SyncResult 是前端「立即同步」的返回值（设计 11.6）。
type SyncResult struct {
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
	Duration       string    `json:"duration"`
	RecordsAdded   int64     `json:"recordsAdded"`   // 真正新增行（INSERT 生效）
	ProcessedCount int64     `json:"processedCount"` // 处理过的 stub 总数（含去重命中 / REPLACE 更新）
	FilesScanned   int       `json:"filesScanned"`
	Errors         []string  `json:"errors"`
}

// UnknownModel 标识一个未在价格表匹配上的模型（设计 11.8）。
type UnknownModel struct {
	NormalizedModel string `json:"normalizedModel"`
	SampleRaw       string `json:"sampleRaw"`
	Requests        int64  `json:"requests"`
	LastSeen        string `json:"lastSeen"`
}
