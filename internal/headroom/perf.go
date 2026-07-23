package headroom

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

// PerfTimeout 是调用方为 GetPerfByClient 设置 ctx 超时的推荐上限。
// headroom perf --raw 读取本地 ledger 并逐条序列化，无网络往返，15s 足够宽裕。
// 与 SavingsTimeout 同值但保留独立常量，便于后续按 perf 数据量单独调优；
// 导出供 app 层（App.GetHeadroomPerfByClient）等调用方复用，避免超时魔数重复散落。
const PerfTimeout = 15 * time.Second

// PerfRecord 是 `headroom perf --format json --raw` 输出数组中的单条 record，
// 字段对齐 headroom 0.32.x 实测 schema，勿臆造字段。
//
// 关键字段语义（供聚合使用）：
//   - client        流量来源客户端（如 "codex" / "claude-code"），由客户端注入
//   - tokens_before 压缩前 token 数（savings_percent 分母）
//   - tokens_saved  本次净节省 token 数（codex responses 协议常为 0）
//   - cache_read    命中 prefix cache 的 token 数
//   - cache_hit_pct cache 命中率（0-100）
//
// stages 是 headroom 内部分阶段耗时映射，结构随版本演变，故用 json.RawMessage
// 透传而不强建模，避免 schema 漂移破坏反序列化。
type PerfRecord struct {
	Timestamp    string          `json:"timestamp"`
	RequestID    string          `json:"request_id"`
	Model        string          `json:"model"`
	Client       string          `json:"client"`
	NumMessages  int             `json:"num_messages"`
	TokensBefore int             `json:"tokens_before"`
	TokensAfter  int             `json:"tokens_after"`
	TokensSaved  int             `json:"tokens_saved"`
	CacheRead    int             `json:"cache_read"`
	CacheWrite   int             `json:"cache_write"`
	CacheHitPct  float64         `json:"cache_hit_pct"`
	Transforms   []string        `json:"transforms"`
	TotalMS      int             `json:"total_ms"`
	TokensOut    int             `json:"tokens_out"`
	TTFBMS       int             `json:"ttfb_ms"`
	Stages       json.RawMessage `json:"stages"`
}

// ClientPerfStat 是按 client 聚合后的性能统计，供前端 codex 卡片突出 cache 命中率
// （codex 经 headroom 的实际收益是 prefix cache 稳定，而非输入体积压缩）。
// 字段命名沿用 headroom 原始 schema（snake_case），便于前端直接对照后端文档。
type ClientPerfStat struct {
	Client          string  `json:"client"`
	Requests        int     `json:"requests"`
	AvgCacheHitPct  float64 `json:"avg_cache_hit_pct"`
	TokensSaved     int     `json:"tokens_saved"`
	CacheReadTokens int     `json:"cache_read_tokens"`
	TokensBefore    int     `json:"tokens_before"`
	SavingsPercent  float64 `json:"savings_percent"`
}

// GetPerfByClient 运行 `headroom perf --format json --raw`，按 client 聚合每条 record，
// 返回每客户端的请求数、平均 cache 命中率、累计节省/读取 token 数与节省百分比。
//
// 三条容错路径均返回明确 error，绝不 panic、绝不返回伪造数据：
//  1. headroom 可执行文件找不到 → error
//  2. 子进程非 0 退出 → error（附带 stderr 摘要）
//  3. JSON 解析失败 → error
//
// 注意：必须使用 `--format json --raw`（per-record 数组）。`--format json` 对
// aggregated 输出会解析失败（实测 headroom 0.32.x），故 by-client 聚合在后端完成
// 而非依赖 CLI 的 aggregated 模式。perf 读取的是本地 ledger，不需要 headroom proxy
// 正在运行；claude 会话级（8787）与 codex 全局（8788）实例的 perf 数据均写入同一
// 文件，靠 record.client 区分来源。
//
// GetPerfByClient 复用 GetSavings 的 runner.Run + resolveHeadroomBinWithEnv + 容错
// 三路径范式，仅在 CLI 子命令（perf --format json --raw）与解析目标（[]PerfRecord）
// 上不同。
func (s *HeadroomService) GetPerfByClient(ctx context.Context) ([]ClientPerfStat, error) {
	if s.runner == nil {
		return nil, fmt.Errorf("headroom process runner is not configured")
	}

	// Resolve headroom with an augmented PATH and reuse that environment for the
	// child process so perf works under the same minimal-GUI-PATH conditions as
	// Start()/GetSavings(). See resolveHeadroomBinWithEnv for the rationale.
	binPath, enhancedEnv := s.resolveHeadroomBinWithEnv()
	spec := platform.CommandSpec{
		Path:   binPath,
		Args:   []string{"perf", "--format", "json", "--raw"},
		Env:    enhancedEnv,
		Policy: platform.DefaultProcessPolicy(),
	}

	result, runErr := s.runner.Run(ctx, spec)

	// 容错路径 1：headroom 可执行文件找不到。
	if isExecutableNotFound(runErr) {
		return nil, fmt.Errorf("headroom executable not found: %w", runErr)
	}
	if runErr != nil {
		// 容错路径 2：子进程非 0 退出，附带 stderr 摘要供诊断。
		if summary := stderrSummary(result); summary != "" {
			return nil, fmt.Errorf("run headroom perf failed (stderr: %s): %w", summary, runErr)
		}
		return nil, fmt.Errorf("run headroom perf: %w", runErr)
	}
	if result == nil {
		return nil, fmt.Errorf("headroom perf returned no output")
	}

	stdout := strings.TrimSpace(result.Stdout)
	if stdout == "" {
		return nil, fmt.Errorf("headroom perf produced empty output (stderr: %s)", stderrSummary(result))
	}

	// 容错路径 3：JSON 解析失败。`--format json --raw` 输出顶层 JSON array。
	var records []PerfRecord
	if err := json.Unmarshal([]byte(stdout), &records); err != nil {
		return nil, fmt.Errorf("parse headroom perf json: %w", err)
	}

	return aggregatePerfByClient(records), nil
}

// aggregatePerfByClient 将 per-record perf 数据按 client 维度聚合为 []ClientPerfStat。
// 纯函数（不触碰 HeadroomService 字段，无 I/O），便于单测覆盖解析与聚合边界。
//
// 聚合规则：
//   - Requests        = 该 client 的 record 计数
//   - AvgCacheHitPct  = cache_hit_pct 的算术均值
//   - TokensSaved     = sum(tokens_saved)
//   - CacheReadTokens = sum(cache_read)
//   - TokensBefore    = sum(tokens_before)
//   - SavingsPercent  = sum(tokens_saved) / sum(tokens_before) * 100
//                       （tokens_before 合计为 0 时返回 0，避免除零；这是 codex
//                       全为 tok_saved=0 时的自然退化，不构成异常）
//
// client 名直接取自 record.client（如 codex / claude-code），不硬编码、不做映射；
// 缺失或空 client 会归入 "" 桶，保持数据透明、不静默丢弃。
//
// 输出按 Requests 降序、Client 升序排序，使前端默认展示高频客户端在前；
// 无数据时返回非 nil 空 slice（JSON 序列化为 `[]` 而非 `null`，便于前端空态判断）。
func aggregatePerfByClient(records []PerfRecord) []ClientPerfStat {
	if len(records) == 0 {
		return []ClientPerfStat{}
	}

	// 第一遍：按 client 累加。保留 sumCacheHit 用于求均值，order 记录首次出现顺序
	// 作为排序的稳定 tie-breaker 兜底（sort.SliceStable 已保证稳定性）。
	type acc struct {
		requests     int
		sumCacheHit  float64
		tokensSaved  int
		cacheRead    int
		tokensBefore int
	}
	accs := make(map[string]*acc)
	order := make([]string, 0, len(records))
	for _, r := range records {
		c := r.Client
		a, ok := accs[c]
		if !ok {
			a = &acc{}
			accs[c] = a
			order = append(order, c)
		}
		a.requests++
		a.sumCacheHit += r.CacheHitPct
		a.tokensSaved += r.TokensSaved
		a.cacheRead += r.CacheRead
		a.tokensBefore += r.TokensBefore
	}

	out := make([]ClientPerfStat, 0, len(accs))
	for _, c := range order {
		a := accs[c]
		stat := ClientPerfStat{
			Client:          c,
			Requests:        a.requests,
			AvgCacheHitPct:  a.sumCacheHit / float64(a.requests),
			TokensSaved:     a.tokensSaved,
			CacheReadTokens: a.cacheRead,
			TokensBefore:    a.tokensBefore,
		}
		// tokens_before 合计为 0 时（理论不会发生，但防御性处理）跳过除法，
		// SavingsPercent 保持零值，避免 NaN/Inf 污染前端展示。
		if a.tokensBefore > 0 {
			stat.SavingsPercent = float64(a.tokensSaved) / float64(a.tokensBefore) * 100
		}
		out = append(out, stat)
	}

	// Requests 降序优先（高频客户端在前），其次 Client 升序保证确定性输出，
	// 便于前端稳定渲染与单测断言。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Client < out[j].Client
	})

	return out
}
