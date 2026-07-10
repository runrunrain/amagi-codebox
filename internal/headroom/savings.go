package headroom

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

// SavingsTimeout 是调用方为 GetSavings 设置 ctx 超时的推荐上限。
// headroom savings 只读取本地 ledger 文件、无网络往返，15s 足够宽裕。
// 导出供 app 层（App.GetHeadroomSavings）等调用方复用，避免超时魔数重复散落。
const SavingsTimeout = 15 * time.Second

// SavingsBucket 是各时间窗口与分组通用的统计桶，字段对齐
// `headroom savings --json`（schema_version 1）的子对象 schema。
type SavingsBucket struct {
	TokensSaved    int     `json:"tokens_saved"`
	TokensBefore   int     `json:"tokens_before"`
	CostUSD        float64 `json:"cost_usd"`
	Calls          int     `json:"calls"`
	SavingsPercent float64 `json:"savings_percent"`
}

// SavingsWindows 聚合 today/last_7_days/all_time 三个同构时间窗口。
type SavingsWindows struct {
	Today     SavingsBucket `json:"today"`
	Last7Days SavingsBucket `json:"last_7_days"`
	AllTime   SavingsBucket `json:"all_time"`
}

// ModelSavings 是 by_model 数组元素：在共享统计桶上附加 model 维度。
// 内嵌 SavingsBucket 使其字段在 JSON 序列化时平铺，对齐实测 schema。
type ModelSavings struct {
	Model string `json:"model"`
	SavingsBucket
}

// ClientSavings 是 by_client 数组元素：在共享统计桶上附加 client 维度。
type ClientSavings struct {
	Client string `json:"client"`
	SavingsBucket
}

// SavingsReport 是 `headroom savings --json` 的反序列化结果，
// 字段与 schema_version 1 的实测输出严格对齐，勿臆造字段。
type SavingsReport struct {
	SchemaVersion int             `json:"schema_version"`
	Path          string          `json:"path"`
	TopModel      string          `json:"top_model"`
	Lifetime      SavingsBucket   `json:"lifetime"`
	Windows       SavingsWindows  `json:"windows"`
	ByModel       []ModelSavings  `json:"by_model"`
	ByClient      []ClientSavings `json:"by_client"`
}

// GetSavings 运行 `headroom savings --json` 并解析返回上下文压缩节省统计。
//
// 三条容错路径均返回明确 error，绝不 panic、绝不返回伪造零值报告：
//  1. headroom 可执行文件找不到 → error
//  2. 子进程非 0 退出 → error（附带 stderr 摘要）
//  3. JSON 解析失败 → error
//
// 注意：savings 读取的是本地 ledger 文件，不需要 headroom proxy 正在运行。
func (s *HeadroomService) GetSavings(ctx context.Context) (*SavingsReport, error) {
	if s.runner == nil {
		return nil, fmt.Errorf("headroom process runner is not configured")
	}

	spec := platform.CommandSpec{
		Path:   resolveHeadroomBin(),
		Args:   []string{"savings", "--json"},
		Env:    os.Environ(),
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
			return nil, fmt.Errorf("run headroom savings failed (stderr: %s): %w", summary, runErr)
		}
		return nil, fmt.Errorf("run headroom savings: %w", runErr)
	}
	if result == nil {
		return nil, fmt.Errorf("headroom savings returned no output")
	}

	stdout := strings.TrimSpace(result.Stdout)
	if stdout == "" {
		return nil, fmt.Errorf("headroom savings produced empty output (stderr: %s)", stderrSummary(result))
	}

	// 容错路径 3：JSON 解析失败。
	var report SavingsReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return nil, fmt.Errorf("parse headroom savings json: %w", err)
	}
	return &report, nil
}

// isExecutableNotFound 判断错误是否源于 headroom 可执行文件缺失。
// 覆盖 exec.LookPath 失败与跨平台路径不存在的表现（POSIX/Windows）。
func isExecutableNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "the system cannot find the file") ||
		strings.Contains(msg, "the system cannot find the path")
}

// stderrSummary 从 ProcessResult 提取并裁剪 stderr，用于错误诊断输出。
func stderrSummary(result *platform.ProcessResult) string {
	if result == nil {
		return ""
	}
	s := strings.TrimSpace(result.Stderr)
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
