package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newTestConfigService creates a ConfigService backed by a temp directory.
func newTestConfigService(t *testing.T) *ConfigService {
	t.Helper()
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return svc
}

// ============================================================================
// A. TerminalPreset CRUD basic behavior
// ============================================================================

func TestGetTerminalPresets_Empty(t *testing.T) {
	svc := newTestConfigService(t)

	presets, err := svc.GetTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}
	if len(presets) != 0 {
		t.Fatalf("expected empty presets, got %d", len(presets))
	}
}

func TestGetTerminalPresets_InvalidType(t *testing.T) {
	svc := newTestConfigService(t)

	_, err := svc.GetTerminalPresets("invalid_type")
	if err == nil {
		t.Fatal("expected error for invalid terminal type, got nil")
	}
}

func TestSaveAndGetTerminalPreset(t *testing.T) {
	svc := newTestConfigService(t)

	tp := TerminalPreset{
		Name:     "My Preset",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
	}

	// Save
	if err := svc.SaveTerminalPreset("claude_code", "my-preset", tp); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}

	// Read back
	presets, err := svc.GetTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}

	got, ok := presets["my-preset"]
	if !ok {
		t.Fatal("preset 'my-preset' not found")
	}
	if got.Name != "My Preset" {
		t.Fatalf("Name = %q, want %q", got.Name, "My Preset")
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Provider = %q, want %q", got.Provider, "anthropic")
	}
	if got.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("Model = %q, want %q", got.Model, "claude-sonnet-4-20250514")
	}
}

func TestSaveTerminalPreset_InvalidType(t *testing.T) {
	svc := newTestConfigService(t)

	tp := TerminalPreset{Name: "test"}
	err := svc.SaveTerminalPreset("bad_type", "test", tp)
	if err == nil {
		t.Fatal("expected error for invalid terminal type, got nil")
	}
}

func TestSaveTerminalPreset_EmptyName(t *testing.T) {
	svc := newTestConfigService(t)

	tp := TerminalPreset{Name: "test"}
	err := svc.SaveTerminalPreset("claude_code", "", tp)
	if err == nil {
		t.Fatal("expected error for empty preset name, got nil")
	}
}

func TestDeleteTerminalPreset(t *testing.T) {
	svc := newTestConfigService(t)

	tp := TerminalPreset{
		Name:     "To Delete",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
	}

	// Save then delete
	if err := svc.SaveTerminalPreset("claude_code", "delete-me", tp); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := svc.DeleteTerminalPreset("claude_code", "delete-me"); err != nil {
		t.Fatalf("DeleteTerminalPreset: %v", err)
	}

	// Verify gone
	presets, err := svc.GetTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}
	if len(presets) != 0 {
		t.Fatalf("expected 0 presets after delete, got %d", len(presets))
	}
}

func TestDeleteTerminalPreset_NonExistent(t *testing.T) {
	svc := newTestConfigService(t)

	// Deleting non-existent preset should not error
	err := svc.DeleteTerminalPreset("claude_code", "ghost")
	if err != nil {
		t.Fatalf("DeleteTerminalPreset on non-existent should not error, got: %v", err)
	}
}

func TestDeleteTerminalPreset_InvalidType(t *testing.T) {
	svc := newTestConfigService(t)

	err := svc.DeleteTerminalPreset("bad_type", "test")
	if err == nil {
		t.Fatal("expected error for invalid terminal type, got nil")
	}
}

func TestSaveTerminalPreset_MultipleTypes(t *testing.T) {
	svc := newTestConfigService(t)

	// Save to different terminal types
	tp1 := TerminalPreset{Name: "Claude Preset", Provider: "anthropic", Model: "claude-sonnet-4-20250514"}
	tp2 := TerminalPreset{Name: "Codex Preset", Provider: "openai", Model: "codex-mini-latest"}
	tp3 := TerminalPreset{Name: "OpenCode Preset", Provider: "glm", Model: "glm-5"}

	if err := svc.SaveTerminalPreset("claude_code", "tp1", tp1); err != nil {
		t.Fatalf("Save claude_code: %v", err)
	}
	if err := svc.SaveTerminalPreset("codex", "tp2", tp2); err != nil {
		t.Fatalf("Save codex: %v", err)
	}
	if err := svc.SaveTerminalPreset("opencode", "tp3", tp3); err != nil {
		t.Fatalf("Save opencode: %v", err)
	}

	// Verify isolation: each type only sees its own presets
	cc, _ := svc.GetTerminalPresets("claude_code")
	cx, _ := svc.GetTerminalPresets("codex")
	oc, _ := svc.GetTerminalPresets("opencode")

	if len(cc) != 1 || cc["tp1"].Name != "Claude Preset" {
		t.Fatalf("claude_code presets = %v, want exactly tp1", cc)
	}
	if len(cx) != 1 || cx["tp2"].Name != "Codex Preset" {
		t.Fatalf("codex presets = %v, want exactly tp2", cx)
	}
	if len(oc) != 1 || oc["tp3"].Name != "OpenCode Preset" {
		t.Fatalf("opencode presets = %v, want exactly tp3", oc)
	}
}

func TestSaveTerminalPreset_Overwrite(t *testing.T) {
	svc := newTestConfigService(t)

	tp1 := TerminalPreset{Name: "V1", Provider: "anthropic", Model: "model-v1"}
	tp2 := TerminalPreset{Name: "V2", Provider: "anthropic", Model: "model-v2"}

	svc.SaveTerminalPreset("claude_code", "my-key", tp1)
	svc.SaveTerminalPreset("claude_code", "my-key", tp2)

	presets, _ := svc.GetTerminalPresets("claude_code")
	if presets["my-key"].Model != "model-v2" {
		t.Fatalf("expected overwrite to model-v2, got %q", presets["my-key"].Model)
	}
}

func TestTerminalPresets_PersistAcrossLoad(t *testing.T) {
	dir := t.TempDir()
	svc1 := NewConfigService(dir)
	svc1.Load()

	tp := TerminalPreset{Name: "Persistent", Provider: "anthropic", Model: "claude-3.5"}
	svc1.SaveTerminalPreset("claude_code", "persist-test", tp)
	svc1.Save()

	// New service instance loading from same dir
	svc2 := NewConfigService(dir)
	svc2.Load()

	presets, err := svc2.GetTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}
	if presets["persist-test"].Name != "Persistent" {
		t.Fatalf("preset not persisted; got %v", presets["persist-test"])
	}
}

// ============================================================================
// B. MigrateProviderPresetsToTerminal basic migration path
// ============================================================================

func TestMigrateProviderPresetsToTerminal_AnthropicToClaudeCode(t *testing.T) {
	svc := newTestConfigService(t)

	// Add an anthropic provider with a preset
	svc.SaveProvider("test-anthropic", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"fast": {Name: "Fast", Model: "claude-3.5-sonnet"},
		},
	})

	count, changed, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("MigrateProviderPresetsToTerminal: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 migrated, got %d", count)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}

	// Verify our custom preset appeared in claude_code terminal presets
	presets, _ := svc.GetTerminalPresets("claude_code")
	stableKey := "test-anthropic/fast"
	tp, ok := presets[stableKey]
	if !ok {
		t.Fatalf("expected preset at key %q, not found", stableKey)
	}
	if tp.Provider != "test-anthropic" {
		t.Fatalf("Provider = %q, want %q", tp.Provider, "test-anthropic")
	}
	if tp.Model != "claude-3.5-sonnet" {
		t.Fatalf("Model = %q, want %q", tp.Model, "claude-3.5-sonnet")
	}
}

func TestMigrateProviderPresetsToTerminal_OpenAIToCodex(t *testing.T) {
	svc := newTestConfigService(t)

	// Add an openai provider with a preset
	svc.SaveProvider("test-openai", Provider{
		Type:    "openai",
		AuthKey: "OPENAI_API_KEY",
		Presets: map[string]Preset{
			"default": {Name: "Default", Model: "codex-mini-latest"},
		},
	})

	count, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 migrated, got %d", count)
	}

	// Should appear in codex, NOT claude_code
	cxPresets, _ := svc.GetTerminalPresets("codex")
	ccPresets, _ := svc.GetTerminalPresets("claude_code")

	stableKey := "test-openai/default"
	if _, ok := cxPresets[stableKey]; !ok {
		t.Fatalf("expected preset at codex/%q", stableKey)
	}
	if _, ok := ccPresets[stableKey]; ok {
		t.Fatalf("openai preset should NOT appear in claude_code")
	}
}

func TestMigrateProviderPresetsToTerminal_OpenCodeTarget(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("my-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {Name: "OC Preset", Model: "claude-3.5", Target: PresetTargetOpenCode},
		},
	})

	count, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 migrated, got %d", count)
	}

	// Should appear in opencode
	ocPresets, _ := svc.GetTerminalPresets("opencode")
	stableKey := "my-provider/oc"
	if _, ok := ocPresets[stableKey]; !ok {
		t.Fatalf("expected preset at opencode/%q", stableKey)
	}
}

func TestMigrateProviderPresetsToTerminal_NoOverwrite(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("anthropic", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"default": {Name: "Default", Model: "original-model"},
		},
	})

	// Pre-create a terminal preset with the same stable key but different model
	svc.SaveTerminalPreset("claude_code", "anthropic/default", TerminalPreset{
		Name:     "Pre-existing",
		Provider: "anthropic",
		Model:    "different-model",
	})

	_, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Verify the pre-existing one was not overwritten
	presets, _ := svc.GetTerminalPresets("claude_code")
	if presets["anthropic/default"].Model != "different-model" {
		t.Fatalf("pre-existing preset should not be overwritten, got model=%q", presets["anthropic/default"].Model)
	}
}

func TestMigrateProviderPresetsToTerminal_Idempotent(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("test-anthropic", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"fast": {Name: "Fast", Model: "claude-3.5-sonnet"},
		},
	})

	// First migration
	count1, _, _ := svc.MigrateProviderPresetsToTerminal()
	if count1 < 1 {
		t.Fatalf("first migration: expected at least 1, got %d", count1)
	}

	// Second migration should not add duplicates for the same keys
	count2, changed2, _ := svc.MigrateProviderPresetsToTerminal()
	if count2 != 0 {
		t.Fatalf("second migration: expected 0 new, got %d", count2)
	}
	if changed2 {
		t.Fatal("second migration should not report changed=true")
	}
}

func TestMigrateProviderPresetsToTerminal_EmptyConfig(t *testing.T) {
	// Verify migration on a service with no extra providers beyond defaults.
	// DefaultConfig already has providers, so migration will migrate those,
	// but calling again should be idempotent.
	svc := newTestConfigService(t)

	// First migration migrates all default provider presets
	count1, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if count1 == 0 {
		t.Fatal("default config should have at least some provider presets to migrate")
	}

	// Second call should be no-op
	count2, changed2, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate (second): %v", err)
	}
	if count2 != 0 {
		t.Fatalf("second migration: expected 0, got %d", count2)
	}
	if changed2 {
		t.Fatal("second migration should report changed=false")
	}
}

// ============================================================================
// C. Stable key rules and provider/type routing
// ============================================================================

func TestStableKeyFormat(t *testing.T) {
	svc := newTestConfigService(t)

	// Two providers with same preset name "default"
	svc.SaveProvider("provider-a", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"default": {Name: "A Default", Model: "model-a"},
		},
	})
	svc.SaveProvider("provider-b", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"default": {Name: "B Default", Model: "model-b"},
		},
	})

	svc.MigrateProviderPresetsToTerminal()

	presets, _ := svc.GetTerminalPresets("claude_code")

	// Both should exist with different stable keys
	keyA := "provider-a/default"
	keyB := "provider-b/default"

	if _, ok := presets[keyA]; !ok {
		t.Fatalf("expected key %q", keyA)
	}
	if _, ok := presets[keyB]; !ok {
		t.Fatalf("expected key %q", keyB)
	}

	if presets[keyA].Model != "model-a" {
		t.Fatalf("provider-a/default Model = %q, want %q", presets[keyA].Model, "model-a")
	}
	if presets[keyB].Model != "model-b" {
		t.Fatalf("provider-b/default Model = %q, want %q", presets[keyB].Model, "model-b")
	}
}

func TestGetMergedTerminalPresets_NewPriority(t *testing.T) {
	svc := newTestConfigService(t)

	// Old: provider preset
	svc.SaveProvider("test-anthropic", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"fast": {Name: "Fast Old", Model: "old-model"},
		},
	})

	// New: terminal preset with same stable key
	svc.SaveTerminalPreset("claude_code", "test-anthropic/fast", TerminalPreset{
		Name:     "Fast New",
		Provider: "test-anthropic",
		Model:    "new-model",
	})

	merged, err := svc.GetMergedTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetMergedTerminalPresets: %v", err)
	}

	// Find the entry for this stable key
	var found *MergedTerminalPreset
	for i := range merged {
		if merged[i].Key == "test-anthropic/fast" {
			found = &merged[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected to find test-anthropic/fast in merged results")
	}

	// Should be from terminal_preset (new system)
	if found.Source != "terminal_preset" {
		t.Fatalf("Source = %q, want %q", found.Source, "terminal_preset")
	}
	if found.Model != "new-model" {
		t.Fatalf("Model = %q, want %q", found.Model, "new-model")
	}
	if found.Label != "Fast New" {
		t.Fatalf("Label = %q, want %q", found.Label, "Fast New")
	}
}

func TestGetMergedTerminalPresets_FallbackToOld(t *testing.T) {
	svc := newTestConfigService(t)

	// Only old system: provider preset, no terminal preset
	svc.SaveProvider("legacy-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"old-preset": {Name: "Old Preset", Model: "legacy-model"},
		},
	})

	merged, err := svc.GetMergedTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetMergedTerminalPresets: %v", err)
	}

	var found *MergedTerminalPreset
	for i := range merged {
		if merged[i].Key == "legacy-provider/old-preset" {
			found = &merged[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected to find legacy-provider/old-preset in merged results")
	}

	if found.Source != "provider_preset" {
		t.Fatalf("Source = %q, want %q", found.Source, "provider_preset")
	}
	if found.Model != "legacy-model" {
		t.Fatalf("Model = %q, want %q", found.Model, "legacy-model")
	}
}

func TestGetMergedTerminalPresets_Empty(t *testing.T) {
	// With default config (no custom terminal presets), merged should still
	// return the default provider presets via the fallback path.
	svc := newTestConfigService(t)

	merged, err := svc.GetMergedTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetMergedTerminalPresets: %v", err)
	}
	// Default config has anthropic, glm, minimax, kimi as anthropic-type providers,
	// each with a "default" preset. So merged should not be empty.
	for _, mp := range merged {
		if mp.Source != "provider_preset" {
			t.Fatalf("without terminal presets, all entries should be provider_preset, got source=%q", mp.Source)
		}
	}
}

func TestGetMergedTerminalPresets_InvalidType(t *testing.T) {
	svc := newTestConfigService(t)

	_, err := svc.GetMergedTerminalPresets("invalid")
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestResolveTerminalPreset(t *testing.T) {
	svc := newTestConfigService(t)

	tp := TerminalPreset{
		Name:     "Resolved",
		Provider: "anthropic",
		Model:    "claude-3.5",
	}
	svc.SaveTerminalPreset("claude_code", "anthropic/test-resolve", tp)

	prov, resolved, err := svc.ResolveTerminalPreset("claude_code", "anthropic/test-resolve")
	if err != nil {
		t.Fatalf("ResolveTerminalPreset: %v", err)
	}
	if prov != "anthropic" {
		t.Fatalf("provider = %q, want %q", prov, "anthropic")
	}
	if resolved == nil {
		t.Fatal("expected non-nil TerminalPreset")
	}
	if resolved.Model != "claude-3.5" {
		t.Fatalf("Model = %q, want %q", resolved.Model, "claude-3.5")
	}
}

func TestResolveTerminalPreset_NotFound(t *testing.T) {
	svc := newTestConfigService(t)

	prov, resolved, err := svc.ResolveTerminalPreset("claude_code", "nonexistent")
	if err != nil {
		t.Fatalf("ResolveTerminalPreset: %v", err)
	}
	if prov != "" {
		t.Fatalf("expected empty provider, got %q", prov)
	}
	if resolved != nil {
		t.Fatal("expected nil TerminalPreset")
	}
}

func TestResolveTerminalPreset_InvalidType(t *testing.T) {
	svc := newTestConfigService(t)

	_, _, err := svc.ResolveTerminalPreset("bad_type", "key")
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

// ============================================================================
// D. TerminalPreset type validation
// ============================================================================

func TestIsValidTerminalPresetType(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"claude_code", true},
		{"opencode", true},
		{"codex", true},
		{"Claude_Code", false},
		{"", false},
		{"invalid", false},
	}
	for _, tt := range tests {
		got := IsValidTerminalPresetType(tt.input)
		if got != tt.want {
			t.Errorf("IsValidTerminalPresetType(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ============================================================================
// E. TerminalPreset NormalizeOpenCodeCfg
// ============================================================================

func TestNormalizeOpenCodeCfg_Empty(t *testing.T) {
	tp := &TerminalPreset{}
	tp.NormalizeOpenCodeCfg()
	if tp.OpenCodeCfg != nil {
		t.Fatal("expected nil OpenCodeCfg after normalizing empty")
	}
}

func TestNormalizeOpenCodeCfg_DoubleEncoded(t *testing.T) {
	// Simulate double-encoded JSON: the outer quotes mean the raw is a JSON string
	inner := `{"theme":"dark"}`
	doubleEncoded, _ := json.Marshal(inner) // becomes "\"eyJ...\""

	tp := &TerminalPreset{OpenCodeCfg: json.RawMessage(doubleEncoded)}
	tp.NormalizeOpenCodeCfg()

	var result map[string]string
	if err := json.Unmarshal(tp.OpenCodeCfg, &result); err != nil {
		t.Fatalf("after normalization, OpenCodeCfg should be valid JSON object, got: %v (raw=%s)", err, string(tp.OpenCodeCfg))
	}
	if result["theme"] != "dark" {
		t.Fatalf("theme = %q, want %q", result["theme"], "dark")
	}
}

// ============================================================================
// F. GetMap/SetMap on TerminalPresetsConfig
// ============================================================================

func TestTerminalPresetsConfig_GetSetMap(t *testing.T) {
	tpc := &TerminalPresetsConfig{}

	m := tpc.GetMap(TerminalPresetClaudeCode)
	if m != nil {
		t.Fatal("expected nil for uninitialized map")
	}

	// Set a map and read back
	testMap := map[string]TerminalPreset{
		"test": {Name: "Test", Provider: "anthropic"},
	}
	tpc.SetMap(TerminalPresetClaudeCode, testMap)

	got := tpc.GetMap(TerminalPresetClaudeCode)
	if got["test"].Name != "Test" {
		t.Fatalf("got %v, expected Test preset", got)
	}
}

func TestTerminalPresetsConfig_GetMap_InvalidType(t *testing.T) {
	tpc := &TerminalPresetsConfig{}
	result := tpc.GetMap("invalid")
	if result != nil {
		t.Fatal("expected nil for invalid type")
	}
}

func TestTerminalPresetsConfig_SetMap_NilReceiver(t *testing.T) {
	var tpc *TerminalPresetsConfig
	// Should not panic
	tpc.SetMap(TerminalPresetClaudeCode, map[string]TerminalPreset{})
}

// ============================================================================
// G. ConfigNotLoaded edge cases
// ============================================================================

func TestGetTerminalPresets_ConfigNotLoaded(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	// Don't call Load()

	_, err := svc.GetTerminalPresets("claude_code")
	if err == nil {
		t.Fatal("expected error when config not loaded")
	}
}

func TestSaveTerminalPreset_ConfigNotLoaded(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)

	err := svc.SaveTerminalPreset("claude_code", "test", TerminalPreset{})
	if err == nil {
		t.Fatal("expected error when config not loaded")
	}
}

// ============================================================================
// H. GetAllTerminalPresets / SetAllTerminalPresets
// ============================================================================

func TestGetAllTerminalPresets_Nil(t *testing.T) {
	svc := newTestConfigService(t)
	result := svc.GetAllTerminalPresets()
	if result != nil {
		t.Fatal("expected nil when no terminal presets")
	}
}

func TestSetAllTerminalPresets_Merge(t *testing.T) {
	svc := newTestConfigService(t)

	// Pre-existing preset
	svc.SaveTerminalPreset("claude_code", "existing", TerminalPreset{
		Name: "Existing", Provider: "anthropic",
	})

	// Import new presets
	svc.SetAllTerminalPresets(&TerminalPresetsConfig{
		ClaudeCode: map[string]TerminalPreset{
			"imported": {Name: "Imported", Provider: "openai"},
		},
		OpenCode: map[string]TerminalPreset{
			"oc-imported": {Name: "OC Imported", Provider: "glm"},
		},
	})

	cc, _ := svc.GetTerminalPresets("claude_code")
	oc, _ := svc.GetTerminalPresets("opencode")

	// Both old and new should exist (merge, not replace)
	if _, ok := cc["existing"]; !ok {
		t.Fatal("existing preset should still be present after merge")
	}
	if _, ok := cc["imported"]; !ok {
		t.Fatal("imported preset should be present")
	}
	if _, ok := oc["oc-imported"]; !ok {
		t.Fatal("opencode imported preset should be present")
	}
}

func TestSetAllTerminalPresets_Nil(t *testing.T) {
	svc := newTestConfigService(t)
	// Should not error
	err := svc.SetAllTerminalPresets(nil)
	if err != nil {
		t.Fatalf("SetAllTerminalPresets(nil): %v", err)
	}
}

// ============================================================================
// I. Save and reload round-trip for terminal presets
// ============================================================================

func TestTerminalPresetsFileStructure(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	svc.Load()

	svc.SaveTerminalPreset("claude_code", "anthropic/fast", TerminalPreset{
		Name: "Fast", Provider: "anthropic", Model: "claude-3.5",
	})
	svc.Save()

	// Read the raw JSON file and verify structure
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)

	// Verify terminal_presets exists
	tpRaw, ok := raw["terminal_presets"]
	if !ok {
		t.Fatal("models.json should contain terminal_presets key")
	}

	var tp struct {
		ClaudeCode map[string]TerminalPreset `json:"claude_code"`
	}
	json.Unmarshal(tpRaw, &tp)

	if tp.ClaudeCode["anthropic/fast"].Model != "claude-3.5" {
		t.Fatalf("terminal_presets.claude_code['anthropic/fast'].Model = %q, want %q",
			tp.ClaudeCode["anthropic/fast"].Model, "claude-3.5")
	}
}

// ============================================================================
// J. Phase 5.2 -- OpenCodeConfig / opencode_config migration fidelity tests
// ============================================================================

// TestMigrateOpenCodeConfig_Fidelity verifies that the OpenCodeConfig field
// from a legacy provider preset is carried over intact to the new
// TerminalPreset.OpenCodeCfg during migration.
func TestMigrateOpenCodeConfig_Fidelity(t *testing.T) {
	svc := newTestConfigService(t)

	// Build a realistic opencode_config payload
	ocCfg := map[string]interface{}{
		"theme":  "dark",
		"model":  "claude-sonnet-4-20250514",
		"editor": "vim",
		"mcp": map[string]interface{}{
			"servers": map[string]interface{}{
				"fetch": map[string]interface{}{
					"command": "npx",
					"args":    []string{"-y", "@anthropic-ai/mcp-fetch"},
				},
			},
		},
	}
	ocRaw, err := json.Marshal(ocCfg)
	if err != nil {
		t.Fatalf("marshal opencode_config: %v", err)
	}

	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {
				Name:           "OC Preset",
				Model:          "claude-sonnet-4-20250514",
				Target:         PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(ocRaw),
			},
		},
	})

	count, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 migrated, got %d", count)
	}

	presets, _ := svc.GetTerminalPresets("opencode")
	stableKey := "test-provider/oc"
	tp, ok := presets[stableKey]
	if !ok {
		t.Fatalf("expected preset at opencode/%q", stableKey)
	}

	// Verify OpenCodeCfg preserves the full object structure
	var result map[string]interface{}
	if err := json.Unmarshal(tp.OpenCodeCfg, &result); err != nil {
		t.Fatalf("OpenCodeCfg is not valid JSON: %v (raw=%s)", err, string(tp.OpenCodeCfg))
	}
	if result["theme"] != "dark" {
		t.Fatalf("theme = %v, want dark", result["theme"])
	}
	if result["model"] != "claude-sonnet-4-20250514" {
		t.Fatalf("model = %v, want claude-sonnet-4-20250514", result["model"])
	}
	if result["editor"] != "vim" {
		t.Fatalf("editor = %v, want vim", result["editor"])
	}
	mcp, ok := result["mcp"].(map[string]interface{})
	if !ok {
		t.Fatalf("mcp = %T, want map", result["mcp"])
	}
	servers, ok := mcp["servers"].(map[string]interface{})
	if !ok {
		t.Fatalf("mcp.servers = %T, want map", mcp["servers"])
	}
	fetch, ok := servers["fetch"].(map[string]interface{})
	if !ok {
		t.Fatalf("mcp.servers.fetch = %T, want map", servers["fetch"])
	}
	if fetch["command"] != "npx" {
		t.Fatalf("mcp.servers.fetch.command = %v, want npx", fetch["command"])
	}
}

// TestMigrateOpenCodeConfig_DoubleEncodedFidelity verifies that double-encoded
// OpenCodeConfig in a legacy preset is automatically normalized during migration,
// without any pre-normalization step. This tests the real legacy data scenario
// where old presets.json contains a double-encoded opencode_config value.
func TestMigrateOpenCodeConfig_DoubleEncodedFidelity(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	svc.Load()

	// Simulate double-encoded JSON: inner JSON string wrapped in outer quotes.
	// This is the exact form produced when Wails serializes a JS string into json.RawMessage.
	inner := `{"theme":"dark","model":"gpt-4o"}`
	doubleEncoded, _ := json.Marshal(inner) // becomes `"{"theme":"dark","model":"gpt-4o"}"`

	// Write the provider preset directly into config (bypassing SavePreset's normalization)
	// to simulate genuine legacy data on disk.
	cfg := svc.GetConfig()
	cfg.Models["test-provider"] = Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {
				Name:           "OC Preset Double",
				Model:          "gpt-4o",
				Target:         PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(doubleEncoded),
			},
		},
	}
	// Save the raw (non-normalized) preset to disk
	svc.SaveProvider("test-provider", cfg.Models["test-provider"])

	// Reload from disk to ensure we're reading genuine legacy data
	svc2 := NewConfigService(dir)
	svc2.Load()

	// Now migrate -- the migration path itself should normalize the double-encoded config
	count, _, err := svc2.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 migrated, got %d", count)
	}

	presets, _ := svc2.GetTerminalPresets("opencode")
	tp, ok := presets["test-provider/oc"]
	if !ok {
		t.Fatal("expected preset test-provider/oc in opencode")
	}

	// After migration, OpenCodeCfg should be a clean JSON object (not a double-encoded string)
	var result map[string]interface{}
	if err := json.Unmarshal(tp.OpenCodeCfg, &result); err != nil {
		t.Fatalf("OpenCodeCfg should be valid JSON after migration auto-normalization, got: %v (raw=%s)", err, string(tp.OpenCodeCfg))
	}
	if result["theme"] != "dark" {
		t.Fatalf("theme = %v, want dark", result["theme"])
	}
	if result["model"] != "gpt-4o" {
		t.Fatalf("model = %v, want gpt-4o", result["model"])
	}
}

// TestMigrateOpenCodeConfig_EmptyPreserved verifies that an empty OpenCodeConfig
// (nil or zero-length) is not lost or corrupted during migration.
func TestMigrateOpenCodeConfig_EmptyPreserved(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {
				Name:           "OC No Cfg",
				Model:          "claude-3.5",
				Target:         PresetTargetOpenCode,
				OpenCodeConfig: nil, // explicitly nil
			},
		},
	})

	svc.MigrateProviderPresetsToTerminal()

	presets, _ := svc.GetTerminalPresets("opencode")
	tp := presets["test-provider/oc"]

	if tp.OpenCodeCfg != nil {
		t.Fatalf("expected nil OpenCodeCfg when source was nil, got: %s", string(tp.OpenCodeCfg))
	}
	// Other fields should still be correct
	if tp.Model != "claude-3.5" {
		t.Fatalf("Model = %q, want claude-3.5", tp.Model)
	}
}

// TestMigrateOpenCodeConfig_ParametersFidelity verifies that model parameters
// are preserved through migration alongside OpenCodeConfig.
func TestMigrateOpenCodeConfig_ParametersFidelity(t *testing.T) {
	svc := newTestConfigService(t)

	ocCfg := `{"theme":"light"}`
	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {
				Name:   "OC With Params",
				Model:  "claude-3.5",
				Target: PresetTargetOpenCode,
				Parameters: Parameters{
					Temperature: 0.7,
					MaxTokens:   4096,
					Thinking:    &ThinkingConfig{Type: "enabled", BudgetTokens: 10000},
					Stream:      boolPtr(true),
				},
				OpenCodeConfig: json.RawMessage(ocCfg),
			},
		},
	})

	svc.MigrateProviderPresetsToTerminal()

	presets, _ := svc.GetTerminalPresets("opencode")
	tp := presets["test-provider/oc"]

	// Verify parameters fidelity
	if tp.Parameters.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", tp.Parameters.Temperature)
	}
	if tp.Parameters.MaxTokens != 4096 {
		t.Fatalf("MaxTokens = %v, want 4096", tp.Parameters.MaxTokens)
	}
	if tp.Parameters.Thinking == nil || tp.Parameters.Thinking.Type != "enabled" {
		t.Fatalf("Thinking = %v, want type=enabled", tp.Parameters.Thinking)
	}
	if tp.Parameters.Thinking.BudgetTokens != 10000 {
		t.Fatalf("Thinking.BudgetTokens = %v, want 10000", tp.Parameters.Thinking.BudgetTokens)
	}
	if tp.Parameters.Stream == nil || *tp.Parameters.Stream != true {
		t.Fatalf("Stream = %v, want true", tp.Parameters.Stream)
	}

	// Verify OpenCodeCfg fidelity
	var result map[string]string
	json.Unmarshal(tp.OpenCodeCfg, &result)
	if result["theme"] != "light" {
		t.Fatalf("theme = %q, want light", result["theme"])
	}
}

// ============================================================================
// K. Phase 5.2 -- Bad data / edge case migration tests
// ============================================================================

// TestMigrate_EmptyModel verifies migration succeeds when preset has an empty model.
func TestMigrate_EmptyModel(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"empty-model": {Name: "Empty Model", Model: ""},
		},
	})

	count, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 migrated, got %d", count)
	}

	presets, _ := svc.GetTerminalPresets("claude_code")
	tp := presets["test-provider/empty-model"]
	if tp.Model != "" {
		t.Fatalf("Model = %q, want empty string", tp.Model)
	}
	if tp.Provider != "test-provider" {
		t.Fatalf("Provider = %q, want test-provider", tp.Provider)
	}
}

// TestMigrate_EmptyPresetMap verifies migration handles a provider with no presets.
func TestMigrate_EmptyPresetMap(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("empty-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{},
	})

	count, changed, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// No presets to migrate from this provider (but default config presets exist)
	_ = count
	_ = changed
}

// TestMigrate_NilPresetMap verifies migration handles a provider with nil presets.
func TestMigrate_NilPresetMap(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("nil-presets", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: nil,
	})

	// Should not panic or error
	count, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	_ = count
}

// TestMigrate_MultipleProvidersMixedTargets verifies migration across multiple
// providers with mixed target types routes presets to correct terminal buckets.
func TestMigrate_MultipleProvidersMixedTargets(t *testing.T) {
	svc := newTestConfigService(t)

	// Provider A: anthropic with opencode target preset
	svc.SaveProvider("provider-a", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc-preset": {Name: "OC A", Model: "claude-3.5", Target: PresetTargetOpenCode},
			"cc-preset": {Name: "CC A", Model: "claude-3.5"},
		},
	})

	// Provider B: openai with codex preset
	svc.SaveProvider("provider-b", Provider{
		Type:    "openai",
		AuthKey: "OPENAI_API_KEY",
		Presets: map[string]Preset{
			"cx-preset": {Name: "CX B", Model: "codex-mini-latest"},
		},
	})

	// Provider C: anthropic with no target (should go to claude_code)
	svc.SaveProvider("provider-c", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"default": {Name: "C Default", Model: "glm-5"},
		},
	})

	count, _, err := svc.MigrateProviderPresetsToTerminal()
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if count < 3 {
		t.Fatalf("expected at least 3 migrated, got %d", count)
	}

	// Verify routing
	cc, _ := svc.GetTerminalPresets("claude_code")
	oc, _ := svc.GetTerminalPresets("opencode")
	cx, _ := svc.GetTerminalPresets("codex")

	// provider-a/cc-preset -> claude_code
	if _, ok := cc["provider-a/cc-preset"]; !ok {
		t.Fatal("expected provider-a/cc-preset in claude_code")
	}
	// provider-a/oc-preset -> opencode
	if _, ok := oc["provider-a/oc-preset"]; !ok {
		t.Fatal("expected provider-a/oc-preset in opencode")
	}
	// provider-b/cx-preset -> codex
	if _, ok := cx["provider-b/cx-preset"]; !ok {
		t.Fatal("expected provider-b/cx-preset in codex")
	}
	// provider-c/default -> claude_code
	if _, ok := cc["provider-c/default"]; !ok {
		t.Fatal("expected provider-c/default in claude_code")
	}

	// OpenCode should NOT have provider-a/cc-preset
	if _, ok := oc["provider-a/cc-preset"]; ok {
		t.Fatal("provider-a/cc-preset should NOT be in opencode")
	}
}

// TestMigrate_UnknownFieldsPreserved verifies that unknown/extra fields in
// OpenCodeConfig survive migration without being stripped.
func TestMigrate_UnknownFieldsPreserved(t *testing.T) {
	svc := newTestConfigService(t)

	// opencode_config with unknown future fields
	ocCfg := `{
		"theme": "solarized",
		"future_field_1": "some_value",
		"future_field_2": 42,
		"nested_unknown": {"deep": [1, 2, 3]}
	}`

	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {
				Name:           "Future Proof",
				Model:          "claude-3.5",
				Target:         PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(ocCfg),
			},
		},
	})

	svc.MigrateProviderPresetsToTerminal()

	presets, _ := svc.GetTerminalPresets("opencode")
	tp := presets["test-provider/oc"]

	var result map[string]interface{}
	if err := json.Unmarshal(tp.OpenCodeCfg, &result); err != nil {
		t.Fatalf("unmarshal OpenCodeCfg: %v", err)
	}
	if result["future_field_1"] != "some_value" {
		t.Fatalf("future_field_1 = %v, want some_value", result["future_field_1"])
	}
	if result["future_field_2"] != float64(42) {
		t.Fatalf("future_field_2 = %v, want 42", result["future_field_2"])
	}
	nested, ok := result["nested_unknown"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested_unknown type = %T, want map", result["nested_unknown"])
	}
	deep, ok := nested["deep"].([]interface{})
	if !ok || len(deep) != 3 {
		t.Fatalf("nested_unknown.deep = %v, want [1,2,3]", nested["deep"])
	}
}

// TestMigrate_ConfigNotLoaded verifies migration returns error when config is not loaded.
func TestMigrate_ConfigNotLoaded(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	// Don't call Load()

	_, _, err := svc.MigrateProviderPresetsToTerminal()
	if err == nil {
		t.Fatal("expected error when config not loaded")
	}
}

// TestMigrate_PersistAndReload verifies migration result survives a full
// save + reload cycle.
func TestMigrate_PersistAndReload(t *testing.T) {
	dir := t.TempDir()

	// First instance: create provider preset and migrate
	svc1 := NewConfigService(dir)
	svc1.Load()

	svc1.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {
				Name:           "Persistent OC",
				Model:          "claude-3.5",
				Target:         PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(`{"theme":"dark"}`),
			},
		},
	})

	svc1.MigrateProviderPresetsToTerminal()
	svc1.Save()

	// Second instance: reload and verify
	svc2 := NewConfigService(dir)
	svc2.Load()

	presets, err := svc2.GetTerminalPresets("opencode")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}

	tp, ok := presets["test-provider/oc"]
	if !ok {
		t.Fatal("expected test-provider/oc to survive reload")
	}
	if tp.Model != "claude-3.5" {
		t.Fatalf("Model = %q, want claude-3.5", tp.Model)
	}
	var cfg map[string]string
	json.Unmarshal(tp.OpenCodeCfg, &cfg)
	if cfg["theme"] != "dark" {
		t.Fatalf("theme = %q, want dark", cfg["theme"])
	}

	// Second migration should be no-op (idempotent after reload)
	count2, changed2, _ := svc2.MigrateProviderPresetsToTerminal()
	if count2 != 0 {
		t.Fatalf("expected 0 new migrations after reload, got %d", count2)
	}
	if changed2 {
		t.Fatal("expected changed=false after reload (already migrated)")
	}
}

// TestMigrate_NormalizeDoesNotCorruptCleanJSON verifies that the migration-time
// normalization does NOT corrupt a clean (non-double-encoded) OpenCodeConfig.
// This is a regression guard for the fix that added NormalizeOpenCodeConfig to
// the migration path.
func TestMigrate_NormalizeDoesNotCorruptCleanJSON(t *testing.T) {
	svc := newTestConfigService(t)

	// Clean JSON -- not double-encoded
	cleanJSON := `{"theme":"solarized","model":"claude-3.5","editor":"vim"}`

	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"oc": {
				Name:           "Clean OC",
				Model:          "claude-3.5",
				Target:         PresetTargetOpenCode,
				OpenCodeConfig: json.RawMessage(cleanJSON),
			},
		},
	})

	svc.MigrateProviderPresetsToTerminal()

	presets, _ := svc.GetTerminalPresets("opencode")
	tp := presets["test-provider/oc"]

	// The clean JSON must be byte-for-byte preserved (modulo whitespace in object)
	var result map[string]interface{}
	if err := json.Unmarshal(tp.OpenCodeCfg, &result); err != nil {
		t.Fatalf("clean JSON should still be valid after migration normalization: %v", err)
	}
	if result["theme"] != "solarized" {
		t.Fatalf("theme = %v, want solarized", result["theme"])
	}
	if result["model"] != "claude-3.5" {
		t.Fatalf("model = %v, want claude-3.5", result["model"])
	}
	if result["editor"] != "vim" {
		t.Fatalf("editor = %v, want vim", result["editor"])
	}
}
