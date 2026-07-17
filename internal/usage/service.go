package usage

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"amagi-codebox/internal/logging"
)

// Service 是使用统计主服务。
//
// 持有 SQLite 连接与 PricingService，暴露：
//   - Load/Close：生命周期
//   - Record：单条事件入库（proxy 实时路径）
//   - RecordBatch：批量记录入库（同步器调用）
//   - SyncAll/StartBackgroundSync：调度（见 sync.go）
//   - GetUsageSummary 等（见 api.go）
type Service struct {
	configDir string
	dbPath    string
	db        *sql.DB
	pricing   *PricingService
	log       *logging.Service

	mu       sync.Mutex // 同步串行化（SyncAll 互斥）
	closed   bool
	closeMu  sync.Mutex
	syncMeta SyncRunMeta // 最近一次 SyncAll 结果

	// Ctx 由 app.go Startup 注入（Wails 应用级生命周期 ctx）。
	// Wails v2 仅绑定"方法"，结构体字段（即使是导出字段）不会进入 wailsjs 生成路径。
	// 供 StartBackgroundSync 内部读取，避免把 context.Context 暴露成前端绑定。
	Ctx context.Context
}

// SyncRunMeta 记录最近一次 SyncAll 的统计。
type SyncRunMeta struct {
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
	RecordsAdded   int64     `json:"recordsAdded"`   // 真正新增行（INSERT 生效）
	ProcessedCount int64     `json:"processedCount"` // 处理过的 stub 数（含去重命中）
	FilesScanned   int       `json:"filesScanned"`
	Errors         []string  `json:"errors"`
}

// NewService 创建服务（未 Load；Startup 调 Load）。
func NewService(configDir string, log *logging.Service) *Service {
	return &Service{
		configDir: configDir,
		dbPath:    filepath.Join(configDir, "usage.db"),
		pricing:   NewPricingService(configDir),
		log:       log,
	}
}

// Load 打开 SQLite、建 schema、加载价格表。
func (s *Service) Load() error {
	db, err := openDB(s.dbPath)
	if err != nil {
		return err
	}
	if err := initSchema(db); err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	if err := s.pricing.Load(); err != nil {
		if s.log != nil {
			s.log.Warn("usage", "价格表加载失败，使用内置 seed", err.Error())
		}
		// 不返回错误：seed 已就绪，服务仍可用
	}
	return nil
}

// Close 关闭数据库。
func (s *Service) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// logWarn 安全日志（log 可能为 nil）。
func (s *Service) logWarn(source, msg, detail string) {
	if s.log != nil {
		s.log.Warn(source, msg, detail)
	}
}

// logInfo 安全日志。
func (s *Service) logInfo(source, msg, detail string) {
	if s.log != nil {
		s.log.Info(source, msg, detail)
	}
}

// Record 接受单条事件，转 UsageRecord 入库。
//
// 内部完成：NormalizeModelID / cache 语义分叉 / 价格表查询 / 成本计算 / dedup_key 生成 / INSERT OR IGNORE。
// 返回是否实际新增（dedup_key 冲突时返回 false）。
func (s *Service) Record(evt UsageEvent) (bool, error) {
	if s.db == nil {
		return false, errors.New("usage service not loaded")
	}
	rec := s.eventToRecord(evt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return insertRecord(ctx, s.db, rec)
}

// RecordForce 与 Record 类似，但用 INSERT OR REPLACE（累计语义场景，如 OpenCode 同 session 更新）。
//
// 公共 API（19 个前端绑定之一）：保持 `error` 单返回值，签名不变。
// 是否为"真正新增行"的细节由内部 recordForceInternal 暴露给 sync 路径使用（见 M5）。
func (s *Service) RecordForce(evt UsageEvent) error {
	_, err := s.recordForceInternal(evt)
	return err
}

// recordForceInternal 是 RecordForce 的内部变体，返回是否为真正新增行（INSERT 生效）。
//
// 区分语义（设计 M5）：
//   - true：dedup_key 原本不存在，本次 INSERT 生效。
//   - false：dedup_key 已存在，本次为 REPLACE（更新已有行）。
//
// sync 的 OpenCode 路径用此返回值把 SyncResult.recordsAdded 限定为真正新增行，
// 另立 SyncResult.processedCount 表示处理过的 stub 总数。
func (s *Service) recordForceInternal(evt UsageEvent) (bool, error) {
	if s.db == nil {
		return false, errors.New("usage service not loaded")
	}
	rec := s.eventToRecord(evt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return upsertRecord(ctx, s.db, rec)
}

// eventToRecord 把 UsageEvent 转为 UsageRecord（含成本计算与 dedup_key 生成）。
func (s *Service) eventToRecord(evt UsageEvent) UsageRecord {
	normalized := NormalizeModelID(evt.Model)
	billableInput := ComputeBillableInput(evt.AppType, evt.InputTokens, evt.CacheReadInputTokens)

	var (
		in, out, cr, cc, total int64
		currency               string
	)

	if evt.CostProvided {
		// OpenCode 路径：直接使用 session.cost（已聚合，无法拆分四维）
		total = evt.NativeCost
		currency = evt.CurrencyCode
		if currency == "" {
			currency = "USD"
		}
	} else {
		mp, _ := s.pricing.Resolve(normalized)
		in, out, cr, cc, total = ComputeCost(mp, billableInput, evt.OutputTokens, evt.CacheReadInputTokens, evt.CacheCreationInputTokens)
		currency = mp.CurrencyCode
	}

	dedupKey := evt.DedupKey
	if dedupKey == "" {
		dedupKey = generateDedupKey(evt)
	}

	return UsageRecord{
		DedupKey:                dedupKey,
		AppType:                 evt.AppType,
		Source:                  evt.Source,
		Provider:                evt.Provider,
		Model:                   evt.Model,
		NormalizedModel:         normalized,
		SessionID:               evt.SessionID,
		ProjectDir:              evt.ProjectDir,
		Preset:                  evt.Preset,
		InputTokens:             evt.InputTokens,
		OutputTokens:            evt.OutputTokens,
		CacheReadInputTokens:    evt.CacheReadInputTokens,
		CacheCreationInputTokens: evt.CacheCreationInputTokens,
		BillableInputTokens:     billableInput,
		InputCost:               in,
		OutputCost:              out,
		CacheReadCost:           cr,
		CacheCreationCost:       cc,
		TotalCost:               total,
		CurrencyCode:            currency,
		OccurredAt:              evt.OccurredAt.UTC(),
		RecordedAt:              time.Now().UTC(),
		RequestID:               evt.RequestID,
	}
}

// generateDedupKey 按 AppType + Source 生成 dedup_key（设计 7.1）。
//
//   - claudecode: "cc:msg_" + message.id（应在 evt.DedupKey 提前填好；此处用 SessionID+OccurredAt 兜底）
//   - codex:      "cx:" + sha1(model|四维token|timestamp)[:16]
//   - opencode:   "oc:" + session.id
//   - proxy:      "px:" + SessionID + ":" + RequestID
func generateDedupKey(evt UsageEvent) string {
	switch evt.AppType {
	case appClaudeCode:
		if evt.Source == SourceProxy {
			return fmt.Sprintf("%s%s:%s", dedupPrefixProxy, evt.SessionID, evt.RequestID)
		}
		// session_log 的 cc:msg_ 前缀由 parser 直接填；兜底用 hash
		return dedupPrefixClaude + hash16(evt.Model, evt.SessionID, evt.OccurredAt)
	case appCodex:
		return dedupPrefixCodex + hash16(evt.Model, evt.SessionID,
			evt.InputTokens, evt.OutputTokens, evt.CacheReadInputTokens, evt.CacheCreationInputTokens,
			evt.OccurredAt.UnixNano())
	case appOpenCode:
		return dedupPrefixOpenCode + evt.SessionID
	default:
		if evt.Source == SourceProxy {
			return fmt.Sprintf("%s%s:%s", dedupPrefixProxy, evt.SessionID, evt.RequestID)
		}
		return "ux:" + hash16(evt.AppType, evt.Model, evt.SessionID, evt.OccurredAt)
	}
}

// hash16 计算输入字段拼接后的 SHA1，返回前 16 个 hex 字符（64 bit 哈希）。
func hash16(parts ...any) string {
	h := sha1.New()
	fmt.Fprint(h, parts...)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Pricing 暴露 PricingService（api.go 的价格 CRUD 用）。
func (s *Service) Pricing() *PricingService { return s.pricing }
