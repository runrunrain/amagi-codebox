package usage

import "time"

// Source 标识用量记录的数据来源。
type Source string

const (
	// SourceSessionLog 来自 CLI 自身的会话日志（jsonl / SQLite）。
	SourceSessionLog Source = "session_log"
	// SourceProxy 来自 amagi-codebox proxy 实时拦截。
	SourceProxy Source = "proxy"
)

// 跨源去重键前缀（设计第 7 章）。
const (
	dedupPrefixClaude   = "cc:msg_"
	dedupPrefixCodex    = "cx:"
	dedupPrefixOpenCode = "oc:"
	dedupPrefixProxy    = "px:"
)

// AppType 常量字符串（复用 internal/session.AppType 的值，避免在 usage 包内反向依赖 session 包）。
const (
	appClaudeCode = "claudecode"
	appCodex      = "codex"
	appOpenCode   = "opencode"
)

// UsageRecord 是单条用量记录的规范结构，跨三类源 + proxy 实时统一。
//
// 字段语义：
//   - 所有 token 字段使用 int（单次 batch 累加仍远低于 int32 上限，用 int 留余量）。
//   - 成本字段使用 int64 micro-native-currency（1e-6）。CurrencyCode 决定具体币种。
//   - DedupKey 是跨源去重唯一键，SQLite 上 UNIQUE 索引 + INSERT OR IGNORE。
type UsageRecord struct {
	// ID 是 SQLite rowid 自增主键，不暴露给前端用于业务语义。
	ID int64 `json:"-"`

	// DedupKey 跨源去重键（UNIQUE 索引）。
	DedupKey string `json:"dedupKey"`

	// 来源与归属。
	AppType          string `json:"appType"`          // claudecode / codex / opencode
	Source           Source `json:"source"`           // session_log / proxy
	Provider         string `json:"provider"`         // inferProviderFromURL 或 model_provider
	Model            string `json:"model"`            // 原始模型名（未标准化）
	NormalizedModel  string `json:"normalizedModel"`  // 标准化后用于匹配价格表
	SessionID        string `json:"sessionId"`        // amagi session id 或外部 session 标识
	ProjectDir       string `json:"projectDir"`       // 工作目录（若可识别）
	Preset           string `json:"preset,omitempty"` // proxy 路径才有

	// 四维 token。
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens"`

	// BillableInputTokens 已处理 cache 语义分叉（claudecode 不扣 / codex 扣）。
	BillableInputTokens int `json:"billableInputTokens"`

	// 成本（int64 micro-native-currency，按 CurrencyCode 决定币种）。
	InputCost         int64 `json:"inputCost"`
	OutputCost        int64 `json:"outputCost"`
	CacheReadCost     int64 `json:"cacheReadCost"`
	CacheCreationCost int64 `json:"cacheCreationCost"`
	TotalCost         int64 `json:"totalCost"`
	CurrencyCode      string `json:"currencyCode"` // "USD" / "CNY"

	// 时间。
	OccurredAt time.Time `json:"occurredAt"` // CLI 事件时间
	RecordedAt time.Time `json:"recordedAt"` // 入库时间

	// 调试（仅 proxy 路径填 amagi request_id）。
	RequestID string `json:"requestId,omitempty"`
}

// UsageEvent 是从 proxy 或 jsonl 解析出的原始事件，进入 Service 后转为 UsageRecord。
//
// Service.Record 内部完成：
//  1. NormalizedModel = NormalizeModelID(Model)
//  2. 应用 cache 语义分叉（按 AppType 决定 BillableInputTokens）
//  3. 查价格表计算四维 Cost 与 TotalCost（CostProvided=true 时跳过，直接用 NativeCost）
//  4. 查价格表 CurrencyCode 回填（CostProvided=true 时用 CurrencyCode 字段）
//  5. 生成 DedupKey（若调用方未提供）
//  6. INSERT OR IGNORE 入库
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

	// OpenCode 专用：若 CostProvided=true，跳过价格表计算，直接用 NativeCost 作为 TotalCost
	// （其余四维 Cost 置 0，无法拆分；OpenCode 自身已聚合）。
	CostProvided  bool
	NativeCost    int64  // OpenCode session.cost 转换而来的 micro-native-currency
	CurrencyCode  string // OpenCode 路径按 providerID 推断；其他路径由价格表决定
}

// ModelPricing 是单个模型（或模型 pattern）的四维单价。
//
// 价格单位：micro-native-currency per million tokens。
//   - USD 模型：1.0 USD/M = 1_000_000 micro-USD/M
//   - CNY 模型：1.0 CNY/M = 1_000_000 micro-CNY/M
type ModelPricing struct {
	ID                      string    `json:"id"`                      // uuid 或 model_key
	ModelPattern            string    `json:"modelPattern"`            // 标准化后的模型 ID（精确匹配）
	DisplayName             string    `json:"displayName"`             // 展示名
	Provider                string    `json:"provider"`                // anthropic / openai / glm / ...
	CurrencyCode            string    `json:"currencyCode"`            // "USD" / "CNY"
	InputPerMillion         int64     `json:"inputPerMillion"`         // micro-currency per 1M input tokens
	OutputPerMillion        int64     `json:"outputPerMillion"`
	CacheReadPerMillion     int64     `json:"cacheReadPerMillion"`
	CacheCreationPerMillion int64     `json:"cacheCreationPerMillion"`
	IsBuiltin               bool      `json:"isBuiltin"` // seed 预置不可删（可改价）
	Notes                   string    `json:"notes,omitempty"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

// PricingData 是价格表持久化结构（usage-pricing.json）。
type PricingData struct {
	Version         int            `json:"version"`
	Models          []ModelPricing `json:"models"`
	FallbackPolicy  FallbackPolicy `json:"fallbackPolicy"`
}

// FallbackPolicy 控制价格表失配时的兜底行为。
type FallbackPolicy struct {
	UnknownModelStrategy string `json:"unknownModelStrategy"` // "zero_cost"（默认）
	DefaultCurrency      string `json:"defaultCurrency"`      // "USD"
	// CNYToUSDFixedRate CNY→USD 折算固定汇率（展示用，不影响存储）。
	CNYToUSDFixedRate float64 `json:"cnyToUsdFixedRate"`
}

// SyncState 记录每个被追踪的源文件/源数据库的增量同步游标。
type SyncState struct {
	SourceType      string    `json:"sourceType"` // claude_jsonl / codex_jsonl / opencode_db
	SourceKey       string    `json:"sourceKey"`  // 文件路径 或 "opencode_default"
	AppType         string    `json:"appType"`
	LastMTime       int64     `json:"lastMTime"`         // Unix nano（文件类源）
	LastLineOffset  int64     `json:"lastLineOffset"`    // 已处理字节偏移（文件断点续传）
	LastTimeUpdated int64     `json:"lastTimeUpdated"`   // sessions.time_updated 最大值（opencode 增量）
	LastSyncedAt    time.Time `json:"lastSyncedAt"`
	LastError       string    `json:"lastError,omitempty"`
	RecordsAdded    int64     `json:"recordsAdded"`
}
