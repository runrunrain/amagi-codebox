package amagi

// AmagiSettings represents the settings_amagi.json configuration structure.
// Covers 9 whitelist fields defined in the requirements.
type AmagiSettings struct {
	Model                     string                         `json:"model"`                               // Default model
	Providers                 map[string]AmagiProvider       `json:"providers"`                           // Service provider configurations
	AvailableModels           []string                       `json:"available_models,omitempty"`          // Available model list
	ModelOverrides            map[string]string              `json:"model_overrides,omitempty"`           // Model mapping
	ModelCapabilityOverrides  map[string]AmagiCapabilityOverride `json:"model_capability_overrides,omitempty"` // Model capability overrides
	ModelPresets              map[string]AmagiModelPreset    `json:"model_presets"`                       // Preset configurations
	AlwaysThinkingEnabled     *bool                          `json:"always_thinking_enabled,omitempty"`   // Always enable thinking
	EffortLevel               string                         `json:"effort_level,omitempty"`              // low/medium/high/max
	AdvisorModel              string                         `json:"advisor_model,omitempty"`             // Advisor model
}

// AmagiProvider represents a service provider configuration in settings_amagi.json.
// APIKey is NOT persisted to the file; it's populated from SecretsService at runtime.
type AmagiProvider struct {
	Protocol string `json:"protocol"` // "anthropic" | "openai"
	BaseURL  string `json:"base_url,omitempty"`
	// APIKey is populated from SecretsService, not stored in JSON
	APIKey string `json:"-"`
}

// AmagiModelPreset represents a model preset configuration in settings_amagi.json.
type AmagiModelPreset struct {
	Provider        string                 `json:"provider"`                   // References providers key
	Model           string                 `json:"model"`
	Temperature     *float64               `json:"temperature,omitempty"`
	MaxTokens       *int                   `json:"max_tokens,omitempty"`
	Thinking        *AmagiThinking         `json:"thinking,omitempty"`
	ProtocolOptions map[string]interface{} `json:"protocol_options,omitempty"`
	ProviderOptions map[string]interface{} `json:"provider_options,omitempty"`
}

// AmagiThinking represents thinking configuration.
type AmagiThinking struct {
	Type         string `json:"type"`                   // "enabled" | "disabled"
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}

// AmagiCapabilityOverride represents capability overrides for a model.
type AmagiCapabilityOverride struct {
	Vision             *bool   `json:"vision,omitempty"`
	ToolUse            *bool   `json:"tool_use,omitempty"`
	ToolUse3Way        *bool   `json:"tool_use_3way,omitempty"`
	MaxOutputTokens    *int    `json:"max_output_tokens,omitempty"`
	ThinkingBudgetTokens *int  `json:"thinking_budget_tokens,omitempty"`
	ComputerUse        *bool   `json:"computer_use,omitempty"`
}

// DefaultAmagiSettings returns default empty settings when file doesn't exist.
func DefaultAmagiSettings() *AmagiSettings {
	return &AmagiSettings{
		Model:                    "",
		Providers:                map[string]AmagiProvider{},
		AvailableModels:          []string{},
		ModelOverrides:           map[string]string{},
		ModelCapabilityOverrides: map[string]AmagiCapabilityOverride{},
		ModelPresets:             map[string]AmagiModelPreset{},
		AlwaysThinkingEnabled:    nil,
		EffortLevel:              "medium",
		AdvisorModel:             "",
	}
}
