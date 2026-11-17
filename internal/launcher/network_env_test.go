package launcher

import (
	"errors"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/session"
)

func TestNetworkPolicyRejectsAllExternalLaunchKindsBeforeSpawn(t *testing.T) {
	s := NewLauncherService(nil, nil)
	sentinel := errors.New("AI proxy core is stopped")
	calls := 0
	s.SetNetworkEnv(func(env []string) ([]string, error) { calls++; return nil, sentinel })
	provider := config.Provider{Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.anthropic.com"}, Presets: map[string]config.Preset{"test": {Name: "test", Model: "model"}}}
	_, claude := s.LaunchGuarded("claude", provider, "test", "key", config.AgentTeamsConfig{}, session.ModeTerminal, t.TempDir(), nil, nil)
	_, codex := s.LaunchCodexArgsGuarded("codex", nil, session.ModeTerminal, t.TempDir(), nil, nil)
	_, pi := s.LaunchPi("pi", "anthropic", "model", "", session.ModeTerminal, t.TempDir(), nil)
	_, omp := s.LaunchOmp("omp", "openai", "model", "", session.ModeTerminal, t.TempDir(), nil)
	_, opencode := s.LaunchOpenCode("opencode", session.ModeTerminal, t.TempDir(), nil, "", nil, "", "")
	for _, err := range []error{claude, codex, pi, omp, opencode} {
		if !errors.Is(err, sentinel) {
			t.Fatalf("unexpected launch error: %v", err)
		}
	}
	if calls != 5 || s.RunningCount() != 0 {
		t.Fatal("a CLI bypassed the network policy")
	}
}
