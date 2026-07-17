// Package opencode 提供只读查询 OpenCode SQLite sessions 表的工具。
//
// 路径：~/.local/share/opencode/opencode.db
//
// 实测真相（设计 17.3 / 17.4 已确认）：
//   - 表名是单数 session（非 sessions）
//   - time_created/time_updated 是 13 位 Unix 毫秒
//   - model 字段是 JSON 字符串：{"id":"glm-5.2","providerID":"zhipuai","variant":"high"}
//     用 gjson 取 .id 作模型名，.providerID 推断币种
//   - cost 是 real（浮点），币种按 providerID 推断（zhipuai/deepseek/moonshot/
//     minimax/doubao/qwen → CNY，其他 → USD）
//   - tokens_reasoning 有值，第一期归 output_tokens
//
// 只读打开（DSN）：file:<path>?mode=ro&_busy_timeout=5000
// 主上机器 opencode.db 达 608MB，只读打开避免锁住 OpenCode 自身运行。
// 实测 sqlite3 -readonly 命令能正常查询，第一期不采用快照拷贝（设计 15.1 待定项确认无需）。
package opencode

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动
)

// UsageEventStub 是 opencode 包产出的中立事件结构。
type UsageEventStub struct {
	DedupKey                  string
	Model                     string
	Provider                  string
	ProjectDir                string
	SessionID                 string
	RawMessageID              string
	InputTokens               int
	OutputTokens              int
	CacheReadInputTokens      int
	CacheCreationInputTokens  int
	OccurredAt                time.Time

	// OpenCode 专用：session.cost 已聚合，直接使用，跳过价格表计算。
	CostProvided bool
	NativeCost   int64  // session.cost × 1_000_000（micro-native-currency）
	CurrencyCode string // 由 providerID 推断
	TimeUpdated  int64  // session.time_updated 原值（毫秒，供增量游标）
}

// QuerySessions 只读查询 OpenCode session 表（增量：time_updated > sinceTimeUpdated）。
//
// 入参：
//   - dbPath：opencode.db 完整路径
//   - sinceTimeUpdated：上次同步的 maxTimeUpdated（毫秒）；0 表示全量
//
// 返回值：
//   - stubs：每个 session 一条记录（DedupKey="oc:"+session.id）
//   - maxTimeUpdated：本次扫描到的最大 time_updated（回写 sync_state）
//   - err：打开/查询错误
//
// 单次最多返回 5000 行（LIMIT 5000），防止意外大查询；多批次由调用方通过 sinceTimeUpdated 推进。
func QuerySessions(dbPath string, sinceTimeUpdated int64) (stubs []UsageEventStub, maxTimeUpdated int64, err error) {
	// 只读 DSN：mode=ro + busy_timeout，避免锁住 OpenCode 自身运行
	dsn := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", normalizeDBPath(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, sinceTimeUpdated, fmt.Errorf("open opencode db: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const q = `SELECT
		id, directory, model, cost,
		tokens_input, tokens_output, tokens_reasoning,
		tokens_cache_read, tokens_cache_write,
		time_created, time_updated
		FROM session
		WHERE time_updated > ?
		ORDER BY time_updated ASC
		LIMIT 5000`
	rows, err := db.QueryContext(ctx, q, sinceTimeUpdated)
	if err != nil {
		return nil, sinceTimeUpdated, fmt.Errorf("query opencode sessions: %w", err)
	}
	defer rows.Close()

	maxTimeUpdated = sinceTimeUpdated
	for rows.Next() {
		var (
			id, directory     string
			modelJSON         sql.NullString
			cost              float64
			tokensInput       sql.NullInt64
			tokensOutput      sql.NullInt64
			tokensReasoning   sql.NullInt64
			tokensCacheRead   sql.NullInt64
			tokensCacheWrite  sql.NullInt64
			timeCreated       sql.NullInt64
			timeUpdated       sql.NullInt64
		)
		if err := rows.Scan(
			&id, &directory, &modelJSON, &cost,
			&tokensInput, &tokensOutput, &tokensReasoning,
			&tokensCacheRead, &tokensCacheWrite,
			&timeCreated, &timeUpdated,
		); err != nil {
			return stubs, maxTimeUpdated, fmt.Errorf("scan opencode row: %w", err)
		}

		// model 字段是 JSON 字符串 {"id":"glm-5.2","providerID":"zhipuai",...}；
		// 实测部分行 model 可能为 NULL，用 sql.NullString + gjson 兜底取空。
		modelRaw := modelJSON.String
		modelName := gjson.Get(modelRaw, "id").String()
		providerID := gjson.Get(modelRaw, "providerID").String()
		currency := inferCurrencyByProvider(providerID)

		// reasoning 归 output（第一期）
		out := int(tokensOutput.Int64) + int(tokensReasoning.Int64)

		// time_* 是 13 位 Unix 毫秒（已实测确认）
		var occurredAt time.Time
		if timeCreated.Valid {
			occurredAt = time.UnixMilli(timeCreated.Int64).UTC()
		} else if timeUpdated.Valid {
			occurredAt = time.UnixMilli(timeUpdated.Int64).UTC()
		} else {
			occurredAt = time.Now().UTC()
		}

		// micro-native-currency：cost × 1e6，四舍五入
		nativeCost := int64(cost * 1_000_000)
		if cost < 0 {
			nativeCost = 0
		}

		var tUpd int64
		if timeUpdated.Valid {
			tUpd = timeUpdated.Int64
		}

		stubs = append(stubs, UsageEventStub{
			DedupKey:                 "oc:" + id,
			Model:                    modelName,
			Provider:                 providerID,
			ProjectDir:               directory,
			SessionID:                id,
			InputTokens:              int(tokensInput.Int64),
			OutputTokens:             out,
			CacheReadInputTokens:     int(tokensCacheRead.Int64),
			CacheCreationInputTokens: int(tokensCacheWrite.Int64),
			OccurredAt:               occurredAt,
			CostProvided:             true,
			NativeCost:               nativeCost,
			CurrencyCode:             currency,
			TimeUpdated:              tUpd,
		})

		if tUpd > maxTimeUpdated {
			maxTimeUpdated = tUpd
		}
	}
	return stubs, maxTimeUpdated, rows.Err()
}

// inferCurrencyByProvider 按 providerID 推断币种。
//
// 国产模型供应商的 OpenCode session.cost 通常是 CNY，海外是 USD。
// 不在白名单的默认 USD（保守）。
func inferCurrencyByProvider(providerID string) string {
	switch strings.ToLower(providerID) {
	case "zhipuai", "zhipu", "glm", "bigmodel":
		return "CNY"
	case "deepseek":
		return "CNY"
	case "moonshot", "kimi":
		return "CNY"
	case "minimax":
		return "CNY"
	case "doubao", "volcengine", "bytedance":
		return "CNY"
	case "qwen", "alibaba", "aliyun", "dashscope":
		return "CNY"
	case "baichuan":
		return "CNY"
	case "01ai", "lingyiwanxiang":
		return "CNY"
	default:
		return "USD"
	}
}

// normalizeDBPath 规范化路径用于 DSN（确保绝对路径）。
//
// modernc/sqlite 的 file: DSN 要求路径为绝对路径；相对路径会以工作目录为基准。
// filepath.Abs 在路径已是绝对时返回原值。
func normalizeDBPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
