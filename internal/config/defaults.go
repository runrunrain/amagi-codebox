package config

// boolPtr remains a small package helper used by configuration tests and
// callers constructing optional boolean fields.
func boolPtr(value bool) *bool { return &value }

// DefaultConfig returns a deliberately clean first-run configuration.
//
// Provider/model presets used to be seeded here (Anthropic, OpenAI, GLM,
// MiniMax and Kimi). That made a new installation look preconfigured and also
// caused deleted built-ins to reappear on a later load. Providers and terminal
// presets are now user-owned from the first launch onward.
func DefaultConfig() *AppConfig {
	return &AppConfig{
		Models: map[string]Provider{},
		AgentTeams: AgentTeamsConfig{
			Enabled:      true,
			TeammateMode: "in-process",
		},
		Version: "1.0.1",
	}
}

// DefaultPiPresets is retained for source compatibility with callers that
// inspect built-in presets. A clean installation has no provider presets.
func DefaultPiPresets() map[string]TerminalPreset {
	return map[string]TerminalPreset{}
}

// DefaultOmpPresets is retained for source compatibility with callers that
// inspect built-in presets. A clean installation has no provider presets.
func DefaultOmpPresets() map[string]TerminalPreset {
	return map[string]TerminalPreset{}
}

// DefaultTerminalPresets returns empty, initialized buckets so UI consumers can
// render a clean state without nil handling or implicit provider creation.
func DefaultTerminalPresets() *TerminalPresetsConfig {
	return &TerminalPresetsConfig{
		Anthropic: map[string]TerminalPreset{},
		OpenAI:    map[string]TerminalPreset{},
	}
}
