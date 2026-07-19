package proxy

import (
	"net/http/httptest"
	"testing"
)

// TestParseUsageFromJSON_AnthropicFormat 测试 Anthropic 标准 JSON 格式
func TestParseUsageFromJSON_AnthropicFormat(t *testing.T) {
	body := []byte(`{
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"cache_read_input_tokens": 20,
			"cache_creation_input_tokens": 10
		}
	}`)

	result := parseUsageFromJSON(body)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
	if result.CacheCreationInputTokens != 10 {
		t.Errorf("Expected CacheCreationInputTokens=10, got %d", result.CacheCreationInputTokens)
	}
}

// TestParseUsageFromJSON_OpenAIFormat 测试 OpenAI 兼容格式
func TestParseUsageFromJSON_OpenAIFormat(t *testing.T) {
	body := []byte(`{
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 50,
			"prompt_tokens_details": {
				"cached_tokens": 20
			}
		}
	}`)

	result := parseUsageFromJSON(body)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
}

// TestParseUsageFromJSON_InputTokensDetailsFormat 测试 input_tokens_details.cached_tokens 格式（GLM 某些格式）
func TestParseUsageFromJSON_InputTokensDetailsFormat(t *testing.T) {
	body := []byte(`{
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"input_tokens_details": {
				"cached_tokens": 20
			}
		}
	}`)

	result := parseUsageFromJSON(body)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
}

// TestParseUsageFromJSON_RootLevelTokens 测试根级别 token 字段
func TestParseUsageFromJSON_RootLevelTokens(t *testing.T) {
	body := []byte(`{
		"input_tokens": 100,
		"output_tokens": 50,
		"cache_read_input_tokens": 20,
		"cache_creation_input_tokens": 10
	}`)

	result := parseUsageFromJSON(body)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
	if result.CacheCreationInputTokens != 10 {
		t.Errorf("Expected CacheCreationInputTokens=10, got %d", result.CacheCreationInputTokens)
	}
}

// Mixed gateways often combine Anthropic's input_tokens with OpenAI's
// completion_tokens. Each dimension must fall back independently.
func TestParseUsageFromJSON_MixedAndTotalSchemas(t *testing.T) {
	mixed := parseUsageFromJSON([]byte(`{"usage":{"input_tokens":120,"completion_tokens":30}}`))
	if mixed == nil || mixed.InputTokens != 120 || mixed.OutputTokens != 30 {
		t.Fatalf("mixed usage = %#v, want input=120 output=30", mixed)
	}

	derived := parseUsageFromJSON([]byte(`{"usage":{"prompt_tokens":80,"total_tokens":100}}`))
	if derived == nil || derived.InputTokens != 80 || derived.OutputTokens != 20 {
		t.Fatalf("derived usage = %#v, want input=80 output=20", derived)
	}

	choice := parseUsageFromJSON([]byte(`{"choices":[{"usage":{"prompt_tokens":50,"completion_tokens":10}}]}`))
	if choice == nil || choice.InputTokens != 50 || choice.OutputTokens != 10 {
		t.Fatalf("choice usage = %#v, want input=50 output=10", choice)
	}
}

func TestParseUsageFromJSON_CacheOnly(t *testing.T) {
	result := parseUsageFromJSON([]byte(`{"usage":{"cache_read_input_tokens":42}}`))
	if result == nil || result.CacheReadInputTokens != 42 {
		t.Fatalf("cache-only usage = %#v, want cache_read=42", result)
	}
}

func TestUsageRequestIDFallbackIsUnique(t *testing.T) {
	service := NewProxyService()
	request := httptest.NewRequest("POST", "http://localhost/v1/messages", nil)
	first := service.usageRequestID(request)
	second := service.usageRequestID(request)
	if first == "" || second == "" || first == second {
		t.Fatalf("fallback request IDs must be non-empty and unique: %q / %q", first, second)
	}
	request.Header.Set("X-Request-ID", "upstream-request")
	if got := service.usageRequestID(request); got != "upstream-request" {
		t.Fatalf("upstream request ID = %q, want upstream-request", got)
	}
}

// TestSSEUsageAccumulator_AnthropicMessageStart 测试 Anthropic message_start 事件
func TestSSEUsageAccumulator_AnthropicMessageStart(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data:{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}}}`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
	if result.CacheCreationInputTokens != 10 {
		t.Errorf("Expected CacheCreationInputTokens=10, got %d", result.CacheCreationInputTokens)
	}
}

// TestSSEUsageAccumulator_AnthropicMessageDelta 测试 Anthropic message_delta 事件
func TestSSEUsageAccumulator_AnthropicMessageDelta(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data:{"type":"message_delta","usage":{"output_tokens":50}}`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
}

// TestSSEUsageAccumulator_OpenAIUsage 测试 OpenAI 兼容 usage 对象
func TestSSEUsageAccumulator_OpenAIUsage(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data:{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":20}}}`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
}

// TestSSEUsageAccumulator_InputTokensDetails 测试 input_tokens_details.cached_tokens（GLM 格式）
func TestSSEUsageAccumulator_InputTokensDetails(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data:{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":50,"input_tokens_details":{"cached_tokens":20}}}`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
}

// TestSSEUsageAccumulator_ResponseCompleted 测试 response.completed 事件
func TestSSEUsageAccumulator_ResponseCompleted(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data:{"type":"response.completed","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":20}}`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
}

// TestSSEUsageAccumulator_DoneEvent 测试 done 事件
func TestSSEUsageAccumulator_DoneEvent(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data:{"type":"done","usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":20}}}`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
}

// TestSSEUsageAccumulator_RootLevelCacheTokens 测试根级别缓存 token 字段
func TestSSEUsageAccumulator_RootLevelCacheTokens(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data:{"prompt_tokens":100,"completion_tokens":50,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
	if result.CacheCreationInputTokens != 10 {
		t.Errorf("Expected CacheCreationInputTokens=10, got %d", result.CacheCreationInputTokens)
	}
}

// TestSSEUsageAccumulator_MaxStrategy 测试"取最大值"策略
func TestSSEUsageAccumulator_MaxStrategy(t *testing.T) {
	acc := NewSSEUsageAccumulator()

	// 第一个 chunk
	acc.ProcessLine(`data:{"usage":{"prompt_tokens":50,"completion_tokens":20}}`)

	// 第二个 chunk（更大的值）
	acc.ProcessLine(`data:{"usage":{"prompt_tokens":100,"completion_tokens":50}}`)

	// 第三个 chunk（更小的值，应该被忽略）
	acc.ProcessLine(`data:{"usage":{"prompt_tokens":30,"completion_tokens":10}}`)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	// 应该取最大值 100 和 50
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
}

// TestSSEUsageAccumulator_MultipleFormats 测试多种格式混合
func TestSSEUsageAccumulator_MultipleFormats(t *testing.T) {
	acc := NewSSEUsageAccumulator()

	// message_start 事件
	acc.ProcessLine(`data:{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":20}}}`)

	// message_delta 事件
	acc.ProcessLine(`data:{"type":"message_delta","usage":{"output_tokens":50}}`)

	result := acc.GetUsage()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected InputTokens=100, got %d", result.InputTokens)
	}
	if result.OutputTokens != 50 {
		t.Errorf("Expected OutputTokens=50, got %d", result.OutputTokens)
	}
	if result.CacheReadInputTokens != 20 {
		t.Errorf("Expected CacheReadInputTokens=20, got %d", result.CacheReadInputTokens)
	}
}

// TestParseUsageFromJSON_EmptyUsage 测试空 usage 的情况
func TestParseUsageFromJSON_EmptyUsage(t *testing.T) {
	body := []byte(`{}`)

	result := parseUsageFromJSON(body)
	if result != nil {
		t.Error("Expected nil result for empty body")
	}
}

// TestSSEUsageAccumulator_IgnoreNonData 测试忽略非 data: 开头的行
func TestSSEUsageAccumulator_IgnoreNonData(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `: keep-alive`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result != nil {
		t.Error("Expected nil result for comment line")
	}
}

// TestSSEUsageAccumulator_IgnoreDone 测试忽略 [DONE] 消息
func TestSSEUsageAccumulator_IgnoreDone(t *testing.T) {
	acc := NewSSEUsageAccumulator()
	line := `data: [DONE]`

	acc.ProcessLine(line)

	result := acc.GetUsage()
	if result != nil {
		t.Error("Expected nil result for [DONE] message")
	}
}
