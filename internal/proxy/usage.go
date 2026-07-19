package proxy

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// debugUsageEnabled 检查是否启用 usage 调试日志。
func debugUsageEnabled() bool {
	return os.Getenv("DEBUG_USAGE") != ""
}

// debugUsageLog 输出调试日志（仅在 DEBUG_USAGE 环境变量设置时）。
func debugUsageLog(format string, args ...interface{}) {
	if debugUsageEnabled() {
		fmt.Printf("[DEBUG_USAGE] "+format+"\n", args...)
	}
}

// UsageData 从 API 响应中提取的 usage 数据。
type UsageData struct {
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens"`
}

// parseUsageFromJSON extracts a normalized usage value from Anthropic, OpenAI
// compatible, and mixed provider schemas. Several upstreams mix input_tokens
// with completion_tokens, so each dimension deliberately falls back on its
// own instead of requiring one all-or-nothing response format.
func parseUsageFromJSON(body []byte) *UsageData {
	return usageFromPayload(gjson.ParseBytes(body))
}

func usageFromPayload(payload gjson.Result) *UsageData {
	// Different gateways put their normalized usage object at different levels.
	// Try every known location instead of assuming an OpenAI-shaped body.
	for _, candidate := range []gjson.Result{
		payload.Get("usage"),
		payload.Get("message.usage"),
		payload.Get("choices.0.usage"),
		payload.Get("response.usage"),
		payload,
	} {
		if !candidate.Exists() {
			continue
		}
		if usage := usageFromResult(candidate); usage != nil {
			return usage
		}
	}
	return nil
}

func usageFromResult(usage gjson.Result) *UsageData {
	input, inputSet := firstUsageInt(usage, "input_tokens", "prompt_tokens", "input_token_count", "promptTokenCount")
	output, outputSet := firstUsageInt(usage, "output_tokens", "completion_tokens", "output_token_count", "completionTokenCount")
	cacheRead, _ := firstUsageInt(usage,
		"cache_read_input_tokens", "cache_read_tokens", "cached_tokens",
		"prompt_tokens_details.cached_tokens", "input_tokens_details.cached_tokens", "cachedContentTokenCount")
	cacheCreation, _ := firstUsageInt(usage,
		"cache_creation_input_tokens", "cache_write_tokens", "cache_creation_tokens")
	total, totalSet := firstUsageInt(usage, "total_tokens", "totalTokenCount")

	// Providers occasionally omit one dimension but provide a total. Derive only
	// the missing dimension; never overwrite an explicitly reported zero.
	if totalSet && inputSet && !outputSet && total >= input {
		output = total - input
		outputSet = true
	}
	if totalSet && outputSet && !inputSet && total >= output {
		input = total - output
		inputSet = true
	}

	if input == 0 && output == 0 && cacheRead == 0 && cacheCreation == 0 {
		return nil
	}
	result := &UsageData{
		InputTokens:              input,
		OutputTokens:             output,
		CacheReadInputTokens:     cacheRead,
		CacheCreationInputTokens: cacheCreation,
	}
	debugUsageLog("usage parsed: input=%d output=%d cacheRead=%d cacheCreation=%d",
		input, output, cacheRead, cacheCreation)
	return result
}

// firstUsageInt returns the first schema field that is present. Presence (not
// positivity) matters because a valid response can have a zero output or a
// cache-only request.
func firstUsageInt(usage gjson.Result, paths ...string) (int, bool) {
	for _, path := range paths {
		value := usage.Get(path)
		if value.Exists() {
			return int(value.Int()), true
		}
	}
	return 0, false
}

// extractModelFromRequest 从请求 body 中提取 model 字段。
func extractModelFromRequest(body []byte) string {
	return gjson.GetBytes(body, "model").String()
}

// SSEUsageAccumulator SSE 流式响应的 usage 累积器。
// message_start 事件包含 input_tokens（及 cache_* tokens），message_delta
// 与 OpenAI-compatible 结束块则携带累计输出或完整 usage。
type SSEUsageAccumulator struct {
	inputTokens              int
	outputTokens             int
	cacheReadInputTokens     int
	cacheCreationInputTokens int
}

func NewSSEUsageAccumulator() *SSEUsageAccumulator {
	return &SSEUsageAccumulator{}
}

// ProcessLine 处理 SSE 的单行数据，解析 usage 相关字段。
//
// Upstream providers either emit cumulative usage on one final chunk or split
// it across message_start/message_delta chunks. Keeping the maximum value per
// dimension handles both forms and avoids counting a repeated final payload.
func (a *SSEUsageAccumulator) ProcessLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return
	}

	payload := gjson.Parse(data)
	if debugUsageEnabled() {
		debugUsageLog("ProcessLine: eventType=%s rawJSON=%s", payload.Get("type").String(), data)
	}
	a.mergeMax(usageFromPayload(payload))
}

func (a *SSEUsageAccumulator) mergeMax(usage *UsageData) {
	if usage == nil {
		return
	}
	if usage.InputTokens > a.inputTokens {
		a.inputTokens = usage.InputTokens
	}
	if usage.OutputTokens > a.outputTokens {
		a.outputTokens = usage.OutputTokens
	}
	if usage.CacheReadInputTokens > a.cacheReadInputTokens {
		a.cacheReadInputTokens = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens > a.cacheCreationInputTokens {
		a.cacheCreationInputTokens = usage.CacheCreationInputTokens
	}
	debugUsageLog("SSE usage merged: input=%d output=%d cacheRead=%d cacheCreation=%d",
		a.inputTokens, a.outputTokens, a.cacheReadInputTokens, a.cacheCreationInputTokens)
}

// GetUsage 返回累积的 usage 数据，若均为零则返回 nil。
func (a *SSEUsageAccumulator) GetUsage() *UsageData {
	if a.inputTokens == 0 && a.outputTokens == 0 &&
		a.cacheReadInputTokens == 0 && a.cacheCreationInputTokens == 0 {
		return nil
	}
	result := &UsageData{
		InputTokens:              a.inputTokens,
		OutputTokens:             a.outputTokens,
		CacheReadInputTokens:     a.cacheReadInputTokens,
		CacheCreationInputTokens: a.cacheCreationInputTokens,
	}
	debugUsageLog("GetUsage: source=sse-final input=%d output=%d cacheRead=%d cacheCreation=%d",
		a.inputTokens, a.outputTokens, a.cacheReadInputTokens, a.cacheCreationInputTokens)
	return result
}

// UsageEvent 是 proxy 包向 usage 包传递的事件结构（设计 9.1）。
//
// proxy 包不 import usage 包（解耦）；app.go 在 Startup 时把 usage.Service.Record
// 适配成接受 UsageEvent 的闭包，注入 ProxyService.SetUsageSink。
//
// 字段对齐 usage.UsageEvent 同名字段（但定义在 proxy 包内）。
type UsageEvent struct {
	AppType                  string // claudecode / codex / opencode（由 LaunchSession 注入）
	Provider                 string // inferProviderFromURL 或 LaunchSession 注入
	Model                    string // extractModelFromRequest
	SessionID                string // LaunchSession 注入
	Preset                   string // LaunchSession 注入
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	OccurredAt               time.Time // 一般用响应时间
	RequestID                string
}
