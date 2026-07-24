package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/config"
)

// PiModelsConfig 是 pi 的 ~/.pi/agent/models.json（或 PI_CODING_AGENT_DIR/models.json）
// 顶层结构。amagi 仅填充 providers 段，其余字段（如 version）保持最小。
type piModelsConfig struct {
	Providers map[string]map[string]any `json:"providers,omitempty"`
}

// PiProviderID 返回 amagi 托管的 pi provider 标识。
//
// pi 支持在 models.json 里注册任意命名的自定义 provider，并用 --provider 引用。
// 为避免与 pi 内置 provider（anthropic/openai/google/zai/kimi/...）冲突，
// 以及避免多个第三方 provider 互相覆盖，amagi 一律使用 "amagi-<providerName>" 作为
// pi 侧的 provider id。这样每个 amagi provider 在 pi 里都是独立条目，路由互不干扰。
func PiProviderID(providerName string) string {
	return "amagi-" + providerName
}

// piAPIType 把 amagi Provider 的 API 格式映射到 pi models.json 的 api 字段值。
//
// pi 支持四种 api：openai-completions / openai-responses / anthropic-messages /
// google-generative-ai。amagi 的双格式 Provider 映射：
//   - OpenAI 兼容   -> "openai-completions"（最通用）
//   - Anthropic 兼容 -> "anthropic-messages"
//
// 参照 opencode_config.go 的 isOpenAIType 判定逻辑。
func piAPIType(provider config.Provider) string {
	if provider.IsOpenAICompatible() {
		return "openai-completions"
	}
	return "anthropic-messages"
}

// BuildPiModelsConfig 依据 amagi Provider + Preset 参数生成 pi models.json 配置（map 形式）。
//
// 生成逻辑：
//  1. provider id = "amagi-<providerName>"（隔离命名，不碰 pi 内置 provider）
//  2. baseUrl/api/apiKey 从 Provider 双格式推导
//  3. models 注册当前选中的 model（来自 preset 或 provider.DefaultModel），
//     并透传 Parameters 中的 contextWindow/maxTokens/reasoning（pi Model Configuration 字段）
//
// 返回的 map 可直接 json.Marshal 后写入 PI_CODING_AGENT_DIR/models.json。
func BuildPiModelsConfig(
	providerName string,
	provider config.Provider,
	modelName string,
	apiKey string,
	params config.Parameters,
) (map[string]any, error) {
	piID := PiProviderID(providerName)
	format := "anthropic"
	if provider.IsOpenAICompatible() {
		format = "openai"
	}

	baseURL := strings.TrimSpace(provider.EffectiveBaseURL(format))
	if baseURL == "" {
		return nil, fmt.Errorf("provider %q has no baseURL, cannot build pi config", providerName)
	}

	entry := map[string]any{
		"baseUrl": baseURL,
		"api":     piAPIType(provider),
	}
	if apiKey != "" {
		entry["apiKey"] = apiKey
	}

	// 注册 model，使 pi 识别该模型并允许 --model 引用。
	// pi 要求自定义 provider 至少声明其提供的 model id。
	// 同时透传预设参数：contextWindow（最大上下文）/ maxTokens / reasoning（思考开关）。
	model := strings.TrimSpace(modelName)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultModel)
	}
	if model != "" {
		m := map[string]any{
			"id":   model,
			"name": model,
		}
		// 最大上下文窗口：amagi ContextWindow.ModelContextWindow -> pi contextWindow
		if params.ContextWindow != nil && params.ContextWindow.ModelContextWindow > 0 {
			m["contextWindow"] = params.ContextWindow.ModelContextWindow
		}
		// 最大输出 token
		if params.MaxTokens > 0 {
			m["maxTokens"] = params.MaxTokens
		}
		// 思考开关：amagi Thinking.Type=="enabled" -> pi reasoning=true
		// （pi 的思考强度级别通过 --thinking CLI flag 注入，见 app.go LaunchPiSession）
		if params.Thinking != nil && params.Thinking.Type == "enabled" {
			m["reasoning"] = true
		}
		entry["models"] = []map[string]any{m}
	}

	return map[string]any{
		"providers": map[string]map[string]any{
			piID: entry,
		},
	}, nil
}


// WritePiAgentConfig 将 pi models.json 配置原子写入 agentDir/models.json。
//
// agentDir 由调用方提供（amagi 使用 <configDir>/pi-runtime 隔离目录，
// 通过 PI_CODING_AGENT_DIR 环境变量让 pi 读取，完全不碰用户 ~/.pi/agent/）。
//
// 写入采用全代码库统一的原子范式：MkdirAll -> MarshalIndent -> tmp 文件 -> Rename。
// 每次启动覆盖写（幂等，无累积）。
func WritePiAgentConfig(agentDir string, cfg map[string]any) error {
	if agentDir == "" {
		return fmt.Errorf("agentDir is required")
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("mkdir pi agent dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pi models.json: %w", err)
	}
	b = append(b, '\n')

	modelsPath := filepath.Join(agentDir, "models.json")
	tmp := modelsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write pi models.json tmp: %w", err)
	}
	if err := os.Rename(tmp, modelsPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace pi models.json: %w", err)
	}
	return nil
}
