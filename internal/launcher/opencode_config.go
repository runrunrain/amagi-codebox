package launcher

import (
	"encoding/json"
	"fmt"
	"strings"

	"amagi-codebox/internal/config"
)

// BuildOpenCodeRuntimeConfig 依据 Provider + Preset 生成 OpenCode 运行时配置（map 形式）。
//
// 生成逻辑：
//  1. 依据当前 provider 推导 OpenCode provider ID 和配置（apiKey/baseURL/openai-compatible 合理推导）
//  2. 依据 preset target/model/parameters 生成默认 OpenCode config 片段（model 选择、thinking 等）
//  3. 与 preset.opencode_config 深度合并（opencode_config 覆盖默认值，未知字段保留）
//
// 返回的 map 可直接 json.Marshal 后作为 OPENCODE_CONFIG_CONTENT 注入。
func BuildOpenCodeRuntimeConfig(
	providerName string,
	provider config.Provider,
	presetName string,
	apiKey string,
) (map[string]any, error) {
	result := map[string]any{}

	// 1. 确定 OpenCode provider ID
	ocProviderID := deriveOpenCodeProviderID(providerName, provider)

	// 2. 确定模型和 preset
	preset, hasPreset := provider.Presets[presetName]
	model := provider.DefaultModel
	if hasPreset && strings.TrimSpace(preset.Model) != "" {
		model = preset.Model
	}

	// 3. 构建 model 字段
	if model != "" {
		result["model"] = ocProviderID + "/" + model
	}

	// 4. 构建 provider 配置（使用 map[string]any 以便后续深度合并）
	providerEntry := buildOpenCodeProviderMap(providerName, provider, apiKey)

	// 5. 根据 preset parameters 构建 provider.models 中的选项
	if hasPreset {
		modelOptions := buildOpenCodeModelOptions(preset)
		if len(modelOptions) > 0 && model != "" {
			modelsMap, _ := providerEntry["models"].(map[string]any)
			if modelsMap == nil {
				modelsMap = map[string]any{}
			}
			modelsMap[model] = map[string]any{
				"options": modelOptions,
			}
			providerEntry["models"] = modelsMap
		}
	}

	result["provider"] = map[string]any{
		ocProviderID: providerEntry,
	}

	// 6. 深度合并 opencode_config（先规范化，防止双重编码）
	if hasPreset && len(preset.OpenCodeConfig) > 0 {
		preset.NormalizeOpenCodeConfig()
		if len(preset.OpenCodeConfig) > 0 {
			var userConfig map[string]any
			if err := json.Unmarshal(preset.OpenCodeConfig, &userConfig); err != nil {
				return nil, fmt.Errorf("parse opencode_config: %w", err)
			}
			result = deepMerge(result, userConfig)
		}
	}

	return result, nil
}

// deriveOpenCodeProviderID 从 amagi-codebox 的 provider 配置推导 OpenCode 的 provider ID。
//
// 映射规则：
//   - type=openai 且 baseURL 含 api.openai.com -> "openai"
//   - type=openai 且 baseURL 为其他 -> 使用 providerName（作为 openai-compatible provider 的 ID）
//   - anthropic 官方 -> "anthropic"
//   - 其他（使用 Anthropic 兼容 API 的第三方） -> 使用 providerName
func deriveOpenCodeProviderID(providerName string, provider config.Provider) string {
	providerType := strings.TrimSpace(strings.ToLower(provider.Type))
	baseURL := strings.TrimSpace(strings.ToLower(provider.BaseURL))

	switch {
	case providerType == "openai" || provider.AuthKey == "OPENAI_API_KEY":
		if strings.Contains(baseURL, "api.openai.com") {
			return "openai"
		}
		return providerName
	default:
		if strings.Contains(baseURL, "api.anthropic.com") {
			return "anthropic"
		}
		return providerName
	}
}

// buildOpenCodeProviderMap 构建 OpenCode provider 配置项（使用 map[string]any 以便深度合并）。
func buildOpenCodeProviderMap(providerName string, provider config.Provider, apiKey string) map[string]any {
	providerType := strings.TrimSpace(strings.ToLower(provider.Type))
	isOpenAIType := providerType == "openai" || provider.AuthKey == "OPENAI_API_KEY"

	entry := map[string]any{}
	options := map[string]any{}

	if apiKey != "" {
		options["apiKey"] = apiKey
	}

	if isOpenAIType {
		if provider.BaseURL != "" {
			options["baseURL"] = provider.BaseURL
		}
	} else {
		baseURL := strings.TrimSpace(strings.ToLower(provider.BaseURL))
		if baseURL != "" && !strings.Contains(baseURL, "api.anthropic.com") {
			options["baseURL"] = provider.BaseURL
		}
	}

	if len(options) > 0 {
		entry["options"] = options
	}

	return entry
}

// buildOpenCodeModelOptions 根据 preset parameters 构建 OpenCode 模型选项
func buildOpenCodeModelOptions(preset config.Preset) map[string]any {
	opts := map[string]any{}
	p := preset.Parameters

	if p.Thinking != nil {
		thinking := map[string]any{
			"type": p.Thinking.Type,
		}
		if p.Thinking.BudgetTokens > 0 {
			thinking["budgetTokens"] = p.Thinking.BudgetTokens
		}
		opts["thinking"] = thinking
	}

	if p.Temperature != 0 {
		opts["temperature"] = p.Temperature
	}

	if p.TopP != 0 {
		opts["topP"] = p.TopP
	}

	if p.MaxTokens != 0 {
		opts["maxTokens"] = p.MaxTokens
	}

	return opts
}

// deepMerge 递归合并两个 map，other 中的值覆盖 base 中的值。
// 未知字段保留不丢失。两个 map 中相同 key 且都为 map[string]any 时递归合并。
func deepMerge(base, other map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(other))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range other {
		existing, exists := result[k]
		if exists {
			existingMap, ok1 := existing.(map[string]any)
			otherMap, ok2 := v.(map[string]any)
			if ok1 && ok2 {
				result[k] = deepMerge(existingMap, otherMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

// BuildOpenCodeEnvOverrides 构建启动 OpenCode 所需的环境变量覆盖。
// 主要生成 OPENCODE_CONFIG_CONTENT 并设置 API Key 环境变量。
func BuildOpenCodeEnvOverrides(
	providerName string,
	provider config.Provider,
	presetName string,
	apiKey string,
) (map[string]string, error) {
	overrides := map[string]string{}

	runtimeConfig, err := BuildOpenCodeRuntimeConfig(providerName, provider, presetName, apiKey)
	if err != nil {
		return nil, fmt.Errorf("build opencode runtime config: %w", err)
	}

	configJSON, err := json.Marshal(runtimeConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal opencode config: %w", err)
	}
	overrides["OPENCODE_CONFIG_CONTENT"] = string(configJSON)

	// 设置 API Key 环境变量作为备用
	providerType := strings.TrimSpace(strings.ToLower(provider.Type))
	switch {
	case providerType == "openai" || provider.AuthKey == "OPENAI_API_KEY":
		if apiKey != "" {
			overrides["OPENAI_API_KEY"] = apiKey
		}
	default:
		if apiKey != "" {
			overrides["ANTHROPIC_API_KEY"] = apiKey
		}
	}

	return overrides, nil
}
