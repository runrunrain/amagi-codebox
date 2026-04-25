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
//   - OpenAI 兼容且 baseURL 含 api.openai.com -> "openai"
//   - OpenAI 兼容且 baseURL 为其他 -> 使用 providerName（作为 openai-compatible provider 的 ID）
//   - Anthropic 官方 -> "anthropic"
//   - 其他（使用 Anthropic 兼容 API 的第三方） -> 使用 providerName
func deriveOpenCodeProviderID(providerName string, provider config.Provider) string {
	baseURL := strings.TrimSpace(strings.ToLower(provider.EffectiveBaseURL("")))

	if provider.IsOpenAICompatible() {
		if strings.Contains(baseURL, "api.openai.com") {
			return "openai"
		}
		return providerName
	}
	if strings.Contains(baseURL, "api.anthropic.com") {
		return "anthropic"
	}
	return providerName
}

// buildOpenCodeProviderMap 构建 OpenCode provider 配置项（使用 map[string]any 以便深度合并）。
func buildOpenCodeProviderMap(providerName string, provider config.Provider, apiKey string) map[string]any {
	isOpenAIType := provider.IsOpenAICompatible()
	effectiveBaseURL := provider.EffectiveBaseURL("")

	entry := map[string]any{}
	options := map[string]any{}

	if apiKey != "" {
		options["apiKey"] = apiKey
	}

	if isOpenAIType {
		if effectiveBaseURL != "" {
			options["baseURL"] = effectiveBaseURL
		}
		if provider.OpenAI != nil && provider.OpenAI.Organization != "" {
			options["organization"] = provider.OpenAI.Organization
		}
	} else {
		lowerBaseURL := strings.TrimSpace(strings.ToLower(effectiveBaseURL))
		if lowerBaseURL != "" && !strings.Contains(lowerBaseURL, "api.anthropic.com") {
			options["baseURL"] = effectiveBaseURL
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
	if provider.IsOpenAICompatible() {
		if apiKey != "" {
			overrides["OPENAI_API_KEY"] = apiKey
		}
	} else {
		if apiKey != "" {
			overrides["ANTHROPIC_API_KEY"] = apiKey
		}
	}

	return overrides, nil
}

// GetAPIKeyFunc 是获取指定 provider 统一 API key 的函数签名。
// format 参数仅为兼容旧调用约定保留，调用方应忽略它，不得用它切换 key 源。
// 返回 (apiKey, error)。
type GetAPIKeyFunc func(providerName, format string) (string, error)

// GetProviderFunc 是获取指定 provider 配置的函数签名。
// 返回 (*Provider, error)。
type GetProviderFunc func(providerName string) (*config.Provider, error)

// resolveBindingFormat 根据 binding.Format 和本地 Provider 的实际能力推导最终格式。
//
// 推导规则：
//   - binding.Format == "openai"  -> "openai"
//   - binding.Format == "anthropic" -> "anthropic"
//   - binding.Format == "" 或 "auto" -> 按 provider 实际格式推导：
//     1. 若 provider 仅 OpenAI compatible   -> "openai"
//     2. 若 provider 仅 Anthropic compatible -> "anthropic"
//     3. 若双格式都兼容或无法判断 -> 使用 provider.PreferredFormat()
func resolveBindingFormat(binding config.OpenCodeBinding, provider *config.Provider) string {
	f := strings.TrimSpace(strings.ToLower(binding.Format))
	switch f {
	case "openai":
		return "openai"
	case "anthropic":
		return "anthropic"
	}

	// f == "" 或 "auto"：按 provider 实际格式推导
	if provider == nil {
		return "anthropic" // 安全回退
	}

	isOpenAI := provider.IsOpenAICompatible()
	isAnthropic := provider.IsAnthropicCompatible()

	switch {
	case isOpenAI && !isAnthropic:
		return "openai"
	case isAnthropic && !isOpenAI:
		return "anthropic"
	default:
		// 双格式都兼容 或 都不兼容 -> 使用 PreferredFormat
		return provider.PreferredFormat()
	}
}

// BuildOpenCodeRuntimeConfigFromPreset 基于新模型 OpenCodePreset 构建运行时配置。
// 行为：
//   - 解析 preset.Config 为 map[string]any
//   - 遍历 Bindings
//   - 对于每个 binding：找到本地 provider，读取 secrets，注入到 config.provider[providerId].options
//   - secrets 仅在运行时注入，不写回 preset.Config
//
// getProvider 用于读取本地 Provider 的配置（baseURL、organization 等），
// 以便在 inject 列表中包含 baseURL/organization 时能从 Provider 真实注入。
func BuildOpenCodeRuntimeConfigFromPreset(
	preset config.OpenCodePreset,
	getAPIKey GetAPIKeyFunc,
	getProvider GetProviderFunc,
) (map[string]any, error) {
	// 1. 解析 Config
	var result map[string]any
	if len(preset.Config) > 0 {
		if err := json.Unmarshal(preset.Config, &result); err != nil {
			return nil, fmt.Errorf("parse opencode preset config: %w", err)
		}
	}
	if result == nil {
		result = map[string]any{}
	}

	// 2. 确保 provider 节点存在
	providers, _ := result["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}

	// 3. 遍历 Bindings，注入 secrets 和配置
	for ocProviderID, binding := range preset.Bindings {
		if binding.LocalProvider == "" {
			continue
		}

		// 3a. 获取本地 Provider 配置（用于推导格式和注入 baseURL/organization）
		var localProvider *config.Provider
		if getProvider != nil {
			if p, err := getProvider(binding.LocalProvider); err == nil && p != nil {
				localProvider = p
			}
		}

		// 3b. 确定格式
		format := resolveBindingFormat(binding, localProvider)

		// 3c. 读取统一 provider key
		apiKey := ""
		if getAPIKey != nil {
			key, err := getAPIKey(binding.LocalProvider, "")
			if err == nil && key != "" {
				apiKey = key
			}
		}

		// 3d. 确定 inject 列表
		// inject 为空时的默认策略：注入 apiKey 和 baseURL（与前端暴露的选项一致）
		inject := binding.Inject
		if len(inject) == 0 {
			inject = []string{"apiKey", "baseURL"}
		}

		// 3e. 获取或创建 provider entry
		provEntry, _ := providers[ocProviderID].(map[string]any)
		if provEntry == nil {
			provEntry = map[string]any{}
		}
		options, _ := provEntry["options"].(map[string]any)
		if options == nil {
			options = map[string]any{}
		}

		// 3f. 按 inject 列表逐一注入
		for _, field := range inject {
			switch field {
			case "apiKey":
				if apiKey != "" {
					options["apiKey"] = apiKey
				}
			case "baseURL":
				// 从本地 Provider 的对应格式读取 EffectiveBaseURL
				if localProvider != nil {
					if baseURL := localProvider.EffectiveBaseURL(format); baseURL != "" {
						options["baseURL"] = baseURL
					}
				}
			case "organization":
				// 仅 openai 格式支持 organization
				if format == "openai" && localProvider != nil && localProvider.OpenAI != nil {
					if org := localProvider.OpenAI.Organization; org != "" {
						options["organization"] = org
					}
				}
			}
		}

		if len(options) > 0 {
			provEntry["options"] = options
		}
		providers[ocProviderID] = provEntry
	}

	result["provider"] = providers

	return result, nil
}

// BuildOpenCodeEnvOverridesFromPreset 基于新模型 OpenCodePreset 构建环境变量覆盖。
func BuildOpenCodeEnvOverridesFromPreset(
	preset config.OpenCodePreset,
	getAPIKey GetAPIKeyFunc,
	getProvider GetProviderFunc,
) (map[string]string, error) {
	overrides := map[string]string{}

	runtimeConfig, err := BuildOpenCodeRuntimeConfigFromPreset(preset, getAPIKey, getProvider)
	if err != nil {
		return nil, fmt.Errorf("build opencode runtime config from preset: %w", err)
	}

	configJSON, err := json.Marshal(runtimeConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal opencode config: %w", err)
	}
	overrides["OPENCODE_CONFIG_CONTENT"] = string(configJSON)

	// 设置环境变量备用：遍历 bindings，为每个 binding 设置对应的环境变量
	for _, binding := range preset.Bindings {
		if binding.LocalProvider == "" || getAPIKey == nil {
			continue
		}

		// 解析格式（与 BuildOpenCodeRuntimeConfigFromPreset 一致）
		var localProvider *config.Provider
		if getProvider != nil {
			if p, err := getProvider(binding.LocalProvider); err == nil && p != nil {
				localProvider = p
			}
		}
		format := resolveBindingFormat(binding, localProvider)

		apiKey, err := getAPIKey(binding.LocalProvider, "")
		if err != nil || apiKey == "" {
			continue
		}

		switch format {
		case "openai":
			overrides["OPENAI_API_KEY"] = apiKey
		case "anthropic":
			overrides["ANTHROPIC_API_KEY"] = apiKey
		}
	}

	return overrides, nil
}
