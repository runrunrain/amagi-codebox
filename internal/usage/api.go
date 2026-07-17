package usage

import (
	"context"
	"fmt"
	"time"
)

// ===== 前端 API 方法（Wails 自动生成 frontend/wailsjs/go/usage/Service.{js,d.ts}） =====
// 签名严格对齐设计第 11 章；洛神前端依赖这些契约。
// 方法返回值类型必须可 JSON 序列化（struct with json tags / 基本类型）。

// GetUsageSummary 返回仪表盘汇总（设计 11.1）。
func (s *Service) GetUsageSummary(filter SummaryFilter) (Summary, error) {
	if s.db == nil {
		return Summary{TotalCostByCurrency: map[string]int64{}}, fmt.Errorf("usage service not loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := s.querySummary(ctx, filter)
	if err != nil {
		return Summary{TotalCostByCurrency: map[string]int64{}}, err
	}
	if summary.TotalCostByCurrency == nil {
		summary.TotalCostByCurrency = map[string]int64{}
	}
	return summary, nil
}

// GetDailyTrends 返回日趋势折线图数据（设计 11.2）。
func (s *Service) GetDailyTrends(filter TrendFilter) ([]DailyTrendPoint, error) {
	if s.db == nil {
		return []DailyTrendPoint{}, fmt.Errorf("usage service not loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.queryDailyTrends(ctx, filter)
}

// GetModelStats 返回按模型聚合的统计（设计 11.3）。
func (s *Service) GetModelStats(filter StatFilter) ([]ModelStat, error) {
	if s.db == nil {
		return []ModelStat{}, fmt.Errorf("usage service not loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.queryModelStats(ctx, filter)
}

// GetProviderStats 返回按供应商聚合的统计（设计 11.4）。
func (s *Service) GetProviderStats(filter StatFilter) ([]ProviderStat, error) {
	if s.db == nil {
		return []ProviderStat{}, fmt.Errorf("usage service not loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.queryProviderStats(ctx, filter)
}

// GetRequestLogs 返回明细日志（分页，设计 11.5）。
func (s *Service) GetRequestLogs(filter LogFilter) ([]UsageRecord, error) {
	if s.db == nil {
		return []UsageRecord{}, fmt.Errorf("usage service not loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.queryRequestLogs(ctx, filter)
}

// SyncSessionUsage 阻塞执行一次同步，返回结果（设计 11.6 / 前端「立即同步」按钮）。
func (s *Service) SyncSessionUsage() (SyncResult, error) {
	started := time.Now().UTC()
	err := s.SyncAll()
	res := SyncResult{
		StartedAt:      started,
		FinishedAt:     time.Now().UTC(),
		RecordsAdded:   s.syncMeta.RecordsAdded,
		ProcessedCount: s.syncMeta.ProcessedCount,
		FilesScanned:   s.syncMeta.FilesScanned,
		Errors:         s.syncMeta.Errors,
	}
	res.Duration = res.FinishedAt.Sub(res.StartedAt).String()
	if err != nil {
		return res, err
	}
	return res, nil
}

// GetSyncState 返回所有源的同步游标（设计 11.6）。
func (s *Service) GetSyncState() []SyncState {
	if s.db == nil {
		return []SyncState{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := listSyncStates(ctx, s.db)
	if err != nil {
		s.logWarn("usage", "GetSyncState 失败", err.Error())
		return []SyncState{}
	}
	if out == nil {
		return []SyncState{}
	}
	return out
}

// GetModelPricing 返回价格表全量（设计 11.7）。
func (s *Service) GetModelPricing() []ModelPricing {
	if s.pricing == nil {
		return []ModelPricing{}
	}
	out := s.pricing.List()
	if out == nil {
		return []ModelPricing{}
	}
	return out
}

// UpsertModelPricing 新增或更新价格表条目（设计 11.7）。
func (s *Service) UpsertModelPricing(mp ModelPricing) error {
	if s.pricing == nil {
		return fmt.Errorf("pricing not initialized")
	}
	return s.pricing.Upsert(mp)
}

// DeleteModelPricing 删除自定义价格条目（内置不可删，设计 11.7）。
func (s *Service) DeleteModelPricing(id string) error {
	if s.pricing == nil {
		return fmt.Errorf("pricing not initialized")
	}
	return s.pricing.Delete(id)
}

// ResetModelPricing 重置为内置 seed（设计 11.7）。
func (s *Service) ResetModelPricing() error {
	if s.pricing == nil {
		return fmt.Errorf("pricing not initialized")
	}
	return s.pricing.ResetBuiltin()
}

// GetUnknownModels 返回价格表未匹配的模型列表（设计 11.8）。
func (s *Service) GetUnknownModels() ([]UnknownModel, error) {
	if s.db == nil {
		return []UnknownModel{}, fmt.Errorf("usage service not loaded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := s.queryUnknownModels(ctx)
	if err != nil {
		return []UnknownModel{}, err
	}
	if out == nil {
		return []UnknownModel{}, nil
	}
	return out, nil
}
