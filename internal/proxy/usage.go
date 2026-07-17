package proxy

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// debugUsageEnabled 检查是否启用 usage 调试日志
func debugUsageEnabled() bool {
	return os.Getenv("DEBUG_USAGE") != ""
}

// debugUsageLog 输出调试日志（仅在 DEBUG_USAGE 环境变量设置时）
func debugUsageLog(format string, args ...interface{}) {
	if debugUsageEnabled() {
		fmt.Printf("[DEBUG_USAGE] "+format+"\n", args...)
	}
}

// UsageData 从 API 响应中提取的 usage 数据
type UsageData struct {
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens"`
}

// parseUsageFromJSON 从 JSON 响应 body 中提取 usage 字段。
// 支持多种格式：
//   - Anthropic 格式：{"usage":{"input_tokens":N,"output_tokens":N,...}}
//   - OpenAI 兼容格式（百炼/GLM/MiniMax等）：{"usage":{"prompt_tokens":N,"completion_tokens":N,...}}
//   - 某些提供商的直接格式：{"input_tokens":N,"output_tokens":N}
func parseUsageFromJSON(body []byte) *UsageData {
	usage := gjson.GetBytes(body, "usage")
	inputTokens, outputTokens := 0, 0
	cacheReadTokens, cacheCreationTokens := 0, 0

	if usage.Exists() {
		// 优先尝试 Anthropic 格式（input_tokens/output_tokens）
		inputTokens = int(usage.Get("input_tokens").Int())
		outputTokens = int(usage.Get("output_tokens").Int())
		cacheReadTokens = int(usage.Get("cache_read_input_tokens").Int())
		cacheCreationTokens = int(usage.Get("cache_creation_input_tokens").Int())

		// 如果 Anthropic 格式没有缓存 token，尝试 input_tokens_details.cached_tokens
		if cacheReadTokens == 0 {
			if inputTokensDetails := usage.Get("input_tokens_details"); inputTokensDetails.Exists() {
				cacheReadTokens = int(inputTokensDetails.Get("cached_tokens").Int())
			}
		}

		// 回退到 OpenAI 兼容格式（prompt_tokens/completion_tokens）
		if inputTokens == 0 && outputTokens == 0 {
			inputTokens = int(usage.Get("prompt_tokens").Int())
			outputTokens = int(usage.Get("completion_tokens").Int())

			// 解析 OpenAI 格式的缓存 token：prompt_tokens_details.cached_tokens
			if promptTokensDetails := usage.Get("prompt_tokens_details"); promptTokensDetails.Exists() {
				cacheReadTokens = int(promptTokensDetails.Get("cached_tokens").Int())
			}
		}
	} else {
		// 尝试直接从根级别提取（某些提供商的响应格式）
		if directInput := gjson.GetBytes(body, "input_tokens").Int(); directInput > 0 {
			inputTokens = int(directInput)
		}
		if directOutput := gjson.GetBytes(body, "output_tokens").Int(); directOutput > 0 {
			outputTokens = int(directOutput)
		}
		// 再次尝试 prompt_tokens/completion_tokens
		if inputTokens == 0 {
			if promptTokens := gjson.GetBytes(body, "prompt_tokens").Int(); promptTokens > 0 {
				inputTokens = int(promptTokens)
			}
		}
		if outputTokens == 0 {
			if completionTokens := gjson.GetBytes(body, "completion_tokens").Int(); completionTokens > 0 {
				outputTokens = int(completionTokens)
			}
		}
		// 尝试从根级别提取缓存 tokens
		if rootCacheRead := gjson.GetBytes(body, "cache_read_input_tokens").Int(); rootCacheRead > 0 {
			cacheReadTokens = int(rootCacheRead)
		}
		if rootCacheCreation := gjson.GetBytes(body, "cache_creation_input_tokens").Int(); rootCacheCreation > 0 {
			cacheCreationTokens = int(rootCacheCreation)
		}
	}

	// 如果仍然没有找到任何有效的 token 数据，返回 nil
	if inputTokens == 0 && outputTokens == 0 {
		return nil
	}

	result := &UsageData{
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheReadInputTokens:     cacheReadTokens,
		CacheCreationInputTokens: cacheCreationTokens,
	}

	debugUsageLog("parseUsageFromJSON: source=json input=%d output=%d cacheRead=%d cacheCreation=%d",
		inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens)

	return result
}

// extractModelFromRequest 从请求 body 中提取 model 字段
func extractModelFromRequest(body []byte) string {
	return gjson.GetBytes(body, "model").String()
}

// SSEUsageAccumulator SSE 流式响应的 usage 累积器。
// message_start 事件包含 input_tokens（及 cache_* tokens）
// message_delta 事件包含 output_tokens
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
// 支持两种 SSE 格式：
//   - Anthropic 格式：type=message_start/message_delta 事件
//   - OpenAI 兼容格式（百炼/GLM/MiniMax等）：choices[].delta + usage 字段
func (a *SSEUsageAccumulator) ProcessLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return
	}

	// 添加原始数据日志用于调试
	if debugUsageEnabled() {
		// 输出完整的原始 JSON 用于调试（仅在启用时）
		debugUsageLog("ProcessLine: eventType=%s rawJSON=%s", gjson.Get(data, "type").String(), data)
	}

	eventType := gjson.Get(data, "type").String()
	switch eventType {
	case "message_start":
		// Anthropic 格式：message_start 包含 message.usage，有 input_tokens 及 cache tokens
		usage := gjson.Get(data, "message.usage")
		if usage.Exists() {
			a.inputTokens += int(usage.Get("input_tokens").Int())
			a.cacheReadInputTokens += int(usage.Get("cache_read_input_tokens").Int())
			a.cacheCreationInputTokens += int(usage.Get("cache_creation_input_tokens").Int())
			debugUsageLog("ProcessLine: source=sse-message_start input=%d output=%d cacheRead=%d cacheCreation=%d",
				a.inputTokens, a.outputTokens, a.cacheReadInputTokens, a.cacheCreationInputTokens)
		}
	case "message_delta":
		// Anthropic 格式：message_delta 包含 usage.output_tokens
		usage := gjson.Get(data, "usage")
		if usage.Exists() {
			a.outputTokens += int(usage.Get("output_tokens").Int())
			debugUsageLog("ProcessLine: source=sse-message_delta input=%d output=%d",
				a.inputTokens, a.outputTokens)
		}
	case "response.completed", "done", "finished":
		// 某些 API（如 GLM）可能使用 response.completed/done/finished 事件携带最终 usage
		// 这些事件中的 usage 数据通常是完整的最终值
		usage := gjson.Get(data, "usage")
		if !usage.Exists() {
			usage = gjson.Get(data, "message.usage")
		}
		if usage.Exists() {
			if promptTokens := int(usage.Get("prompt_tokens").Int()); promptTokens > 0 {
				a.inputTokens = promptTokens
			}
			if inputTokens := int(usage.Get("input_tokens").Int()); inputTokens > 0 {
				a.inputTokens = inputTokens
			}
			if completionTokens := int(usage.Get("completion_tokens").Int()); completionTokens > 0 {
				a.outputTokens = completionTokens
			}
			if outputTokens := int(usage.Get("output_tokens").Int()); outputTokens > 0 {
				a.outputTokens = outputTokens
			}
			// 解析缓存 tokens
			if cached := int(usage.Get("cache_read_input_tokens").Int()); cached > 0 {
				a.cacheReadInputTokens = cached
			}
			if cached := int(usage.Get("cache_creation_input_tokens").Int()); cached > 0 {
				a.cacheCreationInputTokens = cached
			}
			if promptTokensDetails := usage.Get("prompt_tokens_details"); promptTokensDetails.Exists() {
				if cached := int(promptTokensDetails.Get("cached_tokens").Int()); cached > 0 {
					a.cacheReadInputTokens = cached
				}
			}
			if inputTokensDetails := usage.Get("input_tokens_details"); inputTokensDetails.Exists() {
				if cached := int(inputTokensDetails.Get("cached_tokens").Int()); cached > 0 {
					a.cacheReadInputTokens = cached
				}
			}
			debugUsageLog("ProcessLine: source=sse-%s input=%d output=%d cacheRead=%d cacheCreation=%d",
				eventType, a.inputTokens, a.outputTokens, a.cacheReadInputTokens, a.cacheCreationInputTokens)
		}
	default:
		// OpenAI 兼容格式（百炼/GLM/MiniMax等）：部分 API 每个 chunk 都携带累计 usage，
		// 另一些只在最后一个 chunk 携带。
		// 格式：{"choices":[...],"usage":{"prompt_tokens":N,"completion_tokens":N,"total_tokens":N}}
		// 使用"取最大值"策略：无论 usage 出现在哪个 chunk，都能正确提取最终累计值，
		// 同时避免对每个 chunk 的值直接累加导致重复计数。

		// 策略1：标准 usage 对象（OpenAI 格式）
		usage := gjson.Get(data, "usage")
		if usage.Exists() {
			promptTokens := int(usage.Get("prompt_tokens").Int())
			completionTokens := int(usage.Get("completion_tokens").Int())
			if promptTokens > 0 || completionTokens > 0 {
				if promptTokens > a.inputTokens {
					a.inputTokens = promptTokens
				}
				if completionTokens > a.outputTokens {
					a.outputTokens = completionTokens
				}
				debugUsageLog("ProcessLine: source=sse-usage input=%d output=%d",
					a.inputTokens, a.outputTokens)
			}

			// 解析 OpenAI 格式的缓存 token：prompt_tokens_details.cached_tokens
			if promptTokensDetails := usage.Get("prompt_tokens_details"); promptTokensDetails.Exists() {
				cached := int(promptTokensDetails.Get("cached_tokens").Int())
				if cached > a.cacheReadInputTokens {
					a.cacheReadInputTokens = cached
					debugUsageLog("ProcessLine: source=sse-usage-cached input=%d output=%d cacheRead=%d",
						a.inputTokens, a.outputTokens, a.cacheReadInputTokens)
				}
			}

			// 解析 GLM 格式的缓存 token：input_tokens_details.cached_tokens
			if inputTokensDetails := usage.Get("input_tokens_details"); inputTokensDetails.Exists() {
				cached := int(inputTokensDetails.Get("cached_tokens").Int())
				if cached > a.cacheReadInputTokens {
					a.cacheReadInputTokens = cached
					debugUsageLog("ProcessLine: source=sse-usage-input-tokens-details input=%d output=%d cacheRead=%d",
						a.inputTokens, a.outputTokens, a.cacheReadInputTokens)
				}
			}
		}

		// 策略2：根级别的 prompt_tokens/completion_tokens（GLM 可能格式）
		// 某些提供商可能将 token 信息放在根级别而非 usage 对象内
		if promptTokens := gjson.Get(data, "prompt_tokens").Int(); promptTokens > 0 {
			if promptTokens > int64(a.inputTokens) {
				a.inputTokens = int(promptTokens)
				debugUsageLog("ProcessLine: source=sse-root-prompt input=%d output=%d",
					a.inputTokens, a.outputTokens)
			}
		}
		if completionTokens := gjson.Get(data, "completion_tokens").Int(); completionTokens > 0 {
			if completionTokens > int64(a.outputTokens) {
				a.outputTokens = int(completionTokens)
				debugUsageLog("ProcessLine: source=sse-root-completion input=%d output=%d",
					a.inputTokens, a.outputTokens)
			}
		}

		// 策略3：根级别的 input_tokens/output_tokens（非标准格式）
		if altInput := gjson.Get(data, "input_tokens").Int(); altInput > 0 {
			if altInput > int64(a.inputTokens) {
				a.inputTokens = int(altInput)
				debugUsageLog("ProcessLine: source=sse-root-input input=%d output=%d",
					a.inputTokens, a.outputTokens)
			}
		}
		if altOutput := gjson.Get(data, "output_tokens").Int(); altOutput > 0 {
			if altOutput > int64(a.outputTokens) {
				a.outputTokens = int(altOutput)
				debugUsageLog("ProcessLine: source=sse-root-output input=%d output=%d",
					a.inputTokens, a.outputTokens)
			}
		}

			// 策略4：choices[0].usage.prompt_tokens（某些 OpenAI 兼容格式）
		if choiceUsage := gjson.Get(data, "choices.0.usage"); choiceUsage.Exists() {
			promptTokens := int(choiceUsage.Get("prompt_tokens").Int())
			completionTokens := int(choiceUsage.Get("completion_tokens").Int())
			if promptTokens > 0 || completionTokens > 0 {
				if promptTokens > a.inputTokens {
					a.inputTokens = promptTokens
				}
				if completionTokens > a.outputTokens {
					a.outputTokens = completionTokens
				}
				debugUsageLog("ProcessLine: source=sse-choice-usage input=%d output=%d",
					a.inputTokens, a.outputTokens)
			}
		}

		// 策略5：input_tokens_details.cached_tokens（GLM 某些格式）
		if inputTokensDetails := gjson.Get(data, "input_tokens_details"); inputTokensDetails.Exists() {
			cached := int(inputTokensDetails.Get("cached_tokens").Int())
			if cached > a.cacheReadInputTokens {
				a.cacheReadInputTokens = cached
				debugUsageLog("ProcessLine: source=sse-input_tokens_details input=%d output=%d cacheRead=%d",
					a.inputTokens, a.outputTokens, a.cacheReadInputTokens)
			}
		}

		// 策略6：根级别的 cache_read_input_tokens / cache_creation_input_tokens
		if rootCacheRead := gjson.Get(data, "cache_read_input_tokens").Int(); rootCacheRead > 0 {
			if rootCacheRead > int64(a.cacheReadInputTokens) {
				a.cacheReadInputTokens = int(rootCacheRead)
				debugUsageLog("ProcessLine: source=sse-root-cache-read input=%d output=%d cacheRead=%d",
					a.inputTokens, a.outputTokens, a.cacheReadInputTokens)
			}
		}
		if rootCacheCreation := gjson.Get(data, "cache_creation_input_tokens").Int(); rootCacheCreation > 0 {
			if rootCacheCreation > int64(a.cacheCreationInputTokens) {
				a.cacheCreationInputTokens = int(rootCacheCreation)
				debugUsageLog("ProcessLine: source=sse-root-cache-creation input=%d output=%d cacheCreation=%d",
					a.inputTokens, a.outputTokens, a.cacheCreationInputTokens)
			}
		}
	}
}

// GetUsage 返回累积的 usage 数据，若均为零则返回 nil
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
	AppType       string // claudecode / codex / opencode（由 LaunchSession 注入）
	Provider      string // inferProviderFromURL 或 LaunchSession 注入
	Model         string // extractModelFromRequest
	SessionID     string // LaunchSession 注入
	Preset        string // LaunchSession 注入
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	OccurredAt    time.Time // 一般用响应时间
	RequestID     string
}
