package usage

import (
	"strings"
)

// NormalizeModelID 把各种原始模型名变体标准化为价格表匹配键。
//
// 步骤（设计 6.4）：
//  1. 取最后一个 "/" 后的部分：去 vendor 前缀
//     "anthropic/claude-sonnet-4" → "claude-sonnet-4"
//  2. 把 "@" 替换为 "-"：
//     "gpt-4@2024-08-06" → "gpt-4-2024-08-06"
//  3. 去掉 ":latest" ":free" 等字母标签，保留 ":YYYYMMDD" 日期戳：
//     "claude-3-5-sonnet:latest"   → "claude-3-5-sonnet"
//     "claude-3-5-sonnet:20241022" → "claude-3-5-sonnet:20241022"
//  4. 全小写
func NormalizeModelID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// 1. 去最后一个 "/" 前的前缀（vendor 命名空间）
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	// 2. @ → -（OpenAI 兼容的日期戳写法）
	s = strings.ReplaceAll(s, "@", "-")
	// 3. 去除 ":latest" ":free" 等字母标签，但保留 ":<digits>" 日期戳
	if idx := strings.Index(s, ":"); idx >= 0 {
		head := s[:idx]
		tail := s[idx+1:]
		if !isAllDigits(tail) {
			s = head
		}
	}
	// 4. 全小写
	return strings.ToLower(s)
}

// isAllDigits 判断字符串是否非空且全为 0-9 数字（用于区分日期戳 vs 字母标签）。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ComputeBillableInput 按 AppType 决定 input 是否扣减 cache_read（设计 6.2）。
//
//   - claudecode (Anthropic 语义)：input_tokens 是 fresh input，不含 cache_read，不扣减。
//   - codex (OpenAI 语义)：input_tokens 包含 cache_read，必须 saturating_sub 扣减。
//   - opencode：直接用 session.cost，不参与计算（返回原值）。
//   - proxy 实时路径：调用方按 provider 反推 AppType 后传入。
func ComputeBillableInput(appType string, inputTokens, cacheReadTokens int) int {
	switch appType {
	case appClaudeCode:
		return inputTokens
	case appCodex:
		return saturatingSub(inputTokens, cacheReadTokens)
	case appOpenCode:
		return inputTokens
	default:
		// proxy 路径若未显式传 AppType，保守按不扣减处理
		// （调用方应在 proxy.UsageEvent 里显式传 AppType=claudecode/codex）。
		return inputTokens
	}
}

// saturatingSub 饱和减法：a < b 时返回 0（cache_read > input 视为数据异常，不计负数）。
func saturatingSub(a, b int) int {
	if a >= b {
		return a - b
	}
	return 0
}

// ComputeCost 按 ModelPricing 四维单价与 token 数计算 micro-native-currency 成本。
//
// 公式（设计 6.1）：
//
//	input_cost         = billable_input_tokens × input_per_million         / 1_000_000
//	output_cost        = output_tokens        × output_per_million        / 1_000_000
//	cache_read_cost    = cache_read_tokens    × cache_read_per_million    / 1_000_000
//	cache_creation_cost= cache_creation_tokens× cache_creation_per_million/ 1_000_000
//	total = sum of above
//
// 返回 (input, output, cacheRead, cacheCreation, total)。
func ComputeCost(p ModelPricing, billableInput, output, cacheRead, cacheCreation int) (int64, int64, int64, int64, int64) {
	in := mulDivInt64(int64(billableInput), p.InputPerMillion, 1_000_000)
	out := mulDivInt64(int64(output), p.OutputPerMillion, 1_000_000)
	cr := mulDivInt64(int64(cacheRead), p.CacheReadPerMillion, 1_000_000)
	cc := mulDivInt64(int64(cacheCreation), p.CacheCreationPerMillion, 1_000_000)
	return in, out, cr, cc, in + out + cr + cc
}

// mulDivInt64 计算 (value × perMillion) / 1_000_000，整数运算避免浮点误差。
//
// 单条 token 计价：value ≤ 1e7，perMillion ≤ 1e8 → 乘积 ≤ 1e15，
// 远低于 int64 上限 9.2e18，无溢出风险。
func mulDivInt64(value, perMillion int64, million int64) int64 {
	if value <= 0 || perMillion <= 0 {
		return 0
	}
	return value * perMillion / million
}
