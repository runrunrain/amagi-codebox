package config

import "testing"

func TestDefaultConfigStartsWithoutProvidersOrPresets(t *testing.T) {
	defaults := DefaultConfig()
	if defaults == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if len(defaults.Models) != 0 {
		t.Fatalf("fresh config contains provider presets: %#v", defaults.Models)
	}
	if len(DefaultPiPresets()) != 0 || len(DefaultOmpPresets()) != 0 {
		t.Fatal("fresh config contains Pi/OMP provider presets")
	}
	terminal := DefaultTerminalPresets()
	if terminal == nil || len(terminal.Anthropic) != 0 || len(terminal.OpenAI) != 0 {
		t.Fatalf("fresh terminal presets are not clean: %#v", terminal)
	}
}
