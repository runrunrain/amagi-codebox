package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	// After removing the fallback to provider.presets, only terminal_presets are returned.
	// Since no terminal presets were created, the legacy provider preset should NOT appear.
	for _, mp := range merged {
		if mp.Key == "legacy-provider/old-preset" {
			t.Fatal("legacy provider preset should NOT appear after fallback removal")
		}
	}
}

func TestGetMergedTerminalPresets_Empty(t *testing.T) {
	// With default config (no custom terminal presets), merged should return
	// only terminal_presets entries. Since default config has no terminal presets,
	// the result should be empty (fallback to provider.presets removed).
	svc := newTestConfigService(t)

	merged, err := svc.GetMergedTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetMergedTerminalPresets: %v", err)
	}
	// After removing the fallback, no provider_presets should appear
	for _, mp := range merged {
		if mp.Source != "terminal_preset" {
			t.Fatalf("all entries should be terminal_preset, got source=%q", mp.Source)
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

func TestSetAllTerminalPresets_Replace(t *testing.T) {
	svc := newTestConfigService(t)

	if err := svc.SaveTerminalPreset("claude_code", "existing", TerminalPreset{
		Name: "Existing", Provider: "anthropic",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset existing claude_code: %v", err)
	}
	if err := svc.SaveTerminalPreset("codex", "old-codex", TerminalPreset{
		Name: "Old Codex", Provider: "openai",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset old codex: %v", err)
	}

	if err := svc.SetAllTerminalPresets(&TerminalPresetsConfig{
		ClaudeCode: map[string]TerminalPreset{
			"imported": {Name: "Imported", Provider: "openai"},
		},
		OpenCode: map[string]TerminalPreset{
			"oc-imported": {Name: "OC Imported", Provider: "glm"},
		},
	}); err != nil {
		t.Fatalf("SetAllTerminalPresets: %v", err)
	}

	cc, _ := svc.GetTerminalPresets("claude_code")
	oc, _ := svc.GetTerminalPresets("opencode")
	codex, _ := svc.GetTerminalPresets("codex")

	if _, ok := cc["existing"]; ok {
		t.Fatal("existing preset should be removed after replace")
	}
	if _, ok := cc["imported"]; !ok {
		t.Fatal("imported preset should be present")
	}
	if _, ok := oc["oc-imported"]; !ok {
		t.Fatal("opencode imported preset should be present")
	}
	if len(codex) != 0 {
		t.Fatalf("codex presets should be cleared on replace, got %d", len(codex))
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

func TestReplaceImportedPresetSnapshots_ClearsMissingSnapshots(t *testing.T) {
	svc := newTestConfigService(t)

	if err := svc.SaveTerminalPreset("claude_code", "existing", TerminalPreset{Name: "Existing", Provider: "anthropic"}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}
	if err := svc.SaveOpenCodePreset("existing-oc", OpenCodePreset{Name: "Existing OC", Config: json.RawMessage(`{"model":"openai/gpt-5"}`)}); err != nil {
		t.Fatalf("SaveOpenCodePreset: %v", err)
	}

	if err := svc.ReplaceImportedPresetSnapshots(nil, nil, false); err != nil {
		t.Fatalf("ReplaceImportedPresetSnapshots: %v", err)
	}

	cc, err := svc.GetTerminalPresets("claude_code")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}
	if len(cc) != 0 {
		t.Fatalf("claude_code presets should be cleared, got %d", len(cc))
	}
	if got := svc.GetOpenCodePresets(); len(got) != 0 {
		t.Fatalf("opencode_presets should be cleared, got %d", len(got))
	}
}

func TestReplaceImportedPresetSnapshots_RebuildsOpenCodeFromTerminalSnapshot(t *testing.T) {
	svc := newTestConfigService(t)

	if err := svc.SaveOpenCodePreset("old-native", OpenCodePreset{Name: "Old Native", Config: json.RawMessage(`{"model":"openai/old"}`)}); err != nil {
		t.Fatalf("SaveOpenCodePreset old-native: %v", err)
	}

	terminalSnapshot := &TerminalPresetsConfig{
		OpenCode: map[string]TerminalPreset{
			"glm/new": {
				Name:     "GLM New",
				Provider: "glm",
				Model:    "glm-5",
				OpenCodeCfg: json.RawMessage(`{
					"model": "glm-5",
					"provider": {
						"glm": {
							"options": {
								"apiKey": "should-be-removed"
							}
						}
					}
				}`),
			},
		},
	}

	if err := svc.ReplaceImportedPresetSnapshots(terminalSnapshot, nil, false); err != nil {
		t.Fatalf("ReplaceImportedPresetSnapshots: %v", err)
	}

	got := svc.GetOpenCodePresets()
	if len(got) != 1 {
		t.Fatalf("opencode_presets count = %d, want 1", len(got))
	}
	preset, ok := got["glm/new"]
	if !ok {
		t.Fatal("expected migrated opencode preset at glm/new")
	}
	if preset.Name != "GLM New" {
		t.Fatalf("Name = %q, want %q", preset.Name, "GLM New")
	}
	if preset.Source == nil || preset.Source.Kind != "migrated-overlay" {
		t.Fatalf("Source.Kind = %v, want migrated-overlay", preset.Source)
	}
	if strings.Contains(string(preset.Config), "should-be-removed") {
		t.Fatal("migrated opencode config should scrub apiKey")
	}
	if _, exists := got["old-native"]; exists {
		t.Fatal("old opencode preset should not survive snapshot replace")
	}
}

func TestReplaceImportedPresetSnapshots_PreservesExplicitOpenCodeSnapshot(t *testing.T) {
	svc := newTestConfigService(t)

	terminalSnapshot := &TerminalPresetsConfig{
		OpenCode: map[string]TerminalPreset{
			"shared-key": {Name: "Legacy Terminal", Provider: "openai", Model: "gpt-5"},
		},
	}
	openCodeSnapshot := map[string]OpenCodePreset{
		"shared-key": {
			Name:   "Native Snapshot",
			Config: json.RawMessage(`{"model":"openai/gpt-5.1"}`),
			Source: &OpenCodePresetSource{Kind: "native"},
		},
	}

	if err := svc.ReplaceImportedPresetSnapshots(terminalSnapshot, openCodeSnapshot, true); err != nil {
		t.Fatalf("ReplaceImportedPresetSnapshots: %v", err)
	}

	got, err := svc.GetOpenCodePreset("shared-key")
	if err != nil {
		t.Fatalf("GetOpenCodePreset: %v", err)
	}
	if got.Name != "Native Snapshot" {
		t.Fatalf("Name = %q, want %q", got.Name, "Native Snapshot")
	}
	if got.Source == nil || got.Source.Kind != "native" {
		t.Fatalf("Source.Kind = %v, want native", got.Source)
	}
}

func TestReplaceImportedPresetSnapshots_ExplicitOpenCodeSnapshotDoesNotReviveLegacyKeys(t *testing.T) {
	svc := newTestConfigService(t)

	terminalSnapshot := &TerminalPresetsConfig{
		OpenCode: map[string]TerminalPreset{
			"keep":   {Name: "Keep Legacy", Provider: "openai", Model: "gpt-5"},
			"revive": {Name: "Revive Legacy", Provider: "glm", Model: "glm-5"},
		},
	}
	openCodeSnapshot := map[string]OpenCodePreset{
		"keep": {
			Name:   "Keep Native",
			Config: json.RawMessage(`{"model":"openai/gpt-5.1"}`),
			Source: &OpenCodePresetSource{Kind: "native"},
		},
	}

	if err := svc.ReplaceImportedPresetSnapshots(terminalSnapshot, openCodeSnapshot, true); err != nil {
		t.Fatalf("ReplaceImportedPresetSnapshots: %v", err)
	}

	got := svc.GetOpenCodePresets()
	if len(got) != 1 {
		t.Fatalf("opencode_presets count = %d, want 1", len(got))
	}
	if _, ok := got["keep"]; !ok {
		t.Fatal("expected explicit opencode preset 'keep'")
	}
	if _, ok := got["revive"]; ok {
		t.Fatal("legacy terminal preset key 'revive' should not be revived into opencode_presets")
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

// ============================================================================
// L. Phase A3 -- Provider dual-format upgrade tests
// ============================================================================

// TestMigrateProviderToDualFormat_AnthropicOnly verifies that a legacy provider
// with Type="" and AuthKey="ANTHROPIC_API_KEY" is upgraded to have
// Anthropic.Enabled=true and OpenAI=nil.
func TestMigrateProviderToDualFormat_AnthropicOnly(t *testing.T) {
	models := map[string]Provider{
		"my-anthropic": {
			Type:         "",
			BaseURL:      "https://api.anthropic.com",
			DefaultModel: "claude-sonnet-4-20250514",
			AuthKey:      "ANTHROPIC_API_KEY",
		},
	}

	migrateProviderToDualFormat(models)

	p := models["my-anthropic"]
	if p.Anthropic == nil {
		t.Fatal("expected Anthropic to be non-nil after migration")
	}
	if !p.Anthropic.Enabled {
		t.Fatal("expected Anthropic.Enabled = true after migration")
	}
	if p.OpenAI != nil {
		t.Fatal("expected OpenAI to be nil for Anthropic-only provider")
	}
}

// TestMigrateProviderToDualFormat_OpenAIOnly verifies that a legacy provider
// with Type="openai" is upgraded to have OpenAI.Enabled=true and Anthropic=nil.
func TestMigrateProviderToDualFormat_OpenAIOnly(t *testing.T) {
	models := map[string]Provider{
		"my-openai": {
			Type:         "openai",
			BaseURL:      "https://api.openai.com/v1",
			DefaultModel: "gpt-4o",
			AuthKey:      "OPENAI_API_KEY",
		},
	}

	migrateProviderToDualFormat(models)

	p := models["my-openai"]
	if p.OpenAI == nil {
		t.Fatal("expected OpenAI to be non-nil after migration")
	}
	if !p.OpenAI.Enabled {
		t.Fatal("expected OpenAI.Enabled = true after migration")
	}
	if p.Anthropic != nil {
		t.Fatal("expected Anthropic to be nil for OpenAI-only provider")
	}
}

// TestMigrateProviderToDualFormat_AlreadyMigrated verifies that a provider
// that already has Anthropic/OpenAI fields set is not modified.
func TestMigrateProviderToDualFormat_AlreadyMigrated(t *testing.T) {
	originalAnthropic := &AnthropicFormat{
		Enabled: true,
		BaseURL: "https://custom.anthropic.com",
		AuthKey: "CUSTOM_KEY",
	}
	originalOpenAI := &OpenAIFormat{
		Enabled: true,
		BaseURL: "https://custom.openai.com",
		AuthKey: "CUSTOM_OPENAI_KEY",
	}

	models := map[string]Provider{
		"dual-provider": {
			Type:         "anthropic",
			BaseURL:      "https://old-url.com",
			DefaultModel: "model-v1",
			AuthKey:      "ANTHROPIC_API_KEY",
			Anthropic:    originalAnthropic,
			OpenAI:       originalOpenAI,
		},
	}

	migrateProviderToDualFormat(models)

	p := models["dual-provider"]
	// Should NOT have been overwritten
	if p.Anthropic.BaseURL != "https://custom.anthropic.com" {
		t.Fatalf("Anthropic.BaseURL = %q, want %q (should not be overwritten)",
			p.Anthropic.BaseURL, "https://custom.anthropic.com")
	}
	if p.OpenAI.BaseURL != "https://custom.openai.com" {
		t.Fatalf("OpenAI.BaseURL = %q, want %q (should not be overwritten)",
			p.OpenAI.BaseURL, "https://custom.openai.com")
	}
}

// TestMigrateProviderToDualFormat_SyncsOldFields verifies that after migration,
// the old top-level BaseURL and AuthKey fields are synced (mirrored) from the new format,
// not cleared. This ensures backward compatibility for code paths still reading old fields.
func TestMigrateProviderToDualFormat_SyncsOldFields(t *testing.T) {
	models := map[string]Provider{
		"legacy": {
			Type:         "",
			BaseURL:      "https://api.anthropic.com",
			DefaultModel: "claude-sonnet-4-20250514",
			AuthKey:      "ANTHROPIC_API_KEY",
		},
	}

	migrateProviderToDualFormat(models)

	p := models["legacy"]
	// 旧字段应与新格式字段同步（镜像），而非被清空
	if p.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("BaseURL = %q, want %q (synced from new format)",
			p.BaseURL, "https://api.anthropic.com")
	}
	if p.AuthKey != "ANTHROPIC_API_KEY" {
		t.Fatalf("AuthKey = %q, want %q (synced from new format)",
			p.AuthKey, "ANTHROPIC_API_KEY")
	}
	// 新字段应被填充
	if p.Anthropic == nil || !p.Anthropic.Enabled {
		t.Fatal("expected Anthropic.Enabled = true")
	}
	if p.Anthropic.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("Anthropic.BaseURL = %q, want %q",
			p.Anthropic.BaseURL, "https://api.anthropic.com")
	}
	if p.Anthropic.AuthKey != "ANTHROPIC_API_KEY" {
		t.Fatalf("Anthropic.AuthKey = %q, want %q",
			p.Anthropic.AuthKey, "ANTHROPIC_API_KEY")
	}
}

// TestCleanupMigratedPresets_AllMigrated verifies that when all provider presets
// have corresponding entries in terminal_presets, the provider presets are cleaned up.
func TestCleanupMigratedPresets_AllMigrated(t *testing.T) {
	svc := newTestConfigService(t)

	// Set up a provider with presets
	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"preset-a": {Name: "A", Model: "model-a"},
			"preset-b": {Name: "B", Model: "model-b"},
		},
	})

	// Migrate all presets to terminal_presets
	svc.MigrateProviderPresetsToTerminal()

	// Verify presets still exist on provider before cleanup
	provBefore, _ := svc.GetProvider("test-provider")
	if len(provBefore.Presets) == 0 {
		t.Fatal("expected presets to exist before cleanup")
	}

	// Run cleanup
	cfg := svc.GetConfig()
	CleanupMigratedProviderPresets(cfg)

	// Verify presets were cleaned up
	provAfter := cfg.Models["test-provider"]
	if len(provAfter.Presets) != 0 {
		t.Fatalf("expected 0 presets after cleanup, got %d: %v",
			len(provAfter.Presets), provAfter.Presets)
	}
}

// TestCleanupMigratedPresets_PartialMigration verifies that when only some presets
// have been migrated to terminal_presets, the provider presets are preserved.
func TestCleanupMigratedPresets_PartialMigration(t *testing.T) {
	svc := newTestConfigService(t)

	// Set up a provider with two presets
	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"preset-a": {Name: "A", Model: "model-a"},
			"preset-b": {Name: "B", Model: "model-b"},
		},
	})

	// Manually migrate only preset-a to terminal_presets
	svc.SaveTerminalPreset("claude_code", "test-provider/preset-a", TerminalPreset{
		Name:     "A",
		Provider: "test-provider",
		Model:    "model-a",
	})

	// Run cleanup
	cfg := svc.GetConfig()
	CleanupMigratedProviderPresets(cfg)

	// Provider presets should still contain preset-b (not fully migrated)
	prov := cfg.Models["test-provider"]
	if _, ok := prov.Presets["preset-b"]; !ok {
		t.Fatal("expected preset-b to remain on provider (not fully migrated)")
	}
}

// TestCleanupMigratedPresets_NoTerminalPresets verifies that when terminal_presets
// is nil, no cleanup happens and provider presets remain intact.
func TestCleanupMigratedPresets_NoTerminalPresets(t *testing.T) {
	svc := newTestConfigService(t)

	svc.SaveProvider("test-provider", Provider{
		Type:    "anthropic",
		AuthKey: "ANTHROPIC_API_KEY",
		Presets: map[string]Preset{
			"preset-a": {Name: "A", Model: "model-a"},
		},
	})

	cfg := svc.GetConfig()
	// Ensure terminal_presets is nil
	cfg.TerminalPresets = nil

	CleanupMigratedProviderPresets(cfg)

	// Presets should remain
	prov := cfg.Models["test-provider"]
	if len(prov.Presets) != 1 {
		t.Fatalf("expected 1 preset (no cleanup when terminal_presets=nil), got %d",
			len(prov.Presets))
	}
}

// TestIsAnthropicCompatible_NewField verifies that IsAnthropicCompatible
// returns true when the new Anthropic format field has Enabled=true.
func TestIsAnthropicCompatible_NewField(t *testing.T) {
	p := Provider{
		Anthropic: &AnthropicFormat{Enabled: true},
	}
	if !p.IsAnthropicCompatible() {
		t.Fatal("expected true when Anthropic.Enabled = true")
	}
}

// TestIsOpenAICompatible_NewField verifies that IsOpenAICompatible
// returns true when the new OpenAI format field has Enabled=true.
func TestIsOpenAICompatible_NewField(t *testing.T) {
	p := Provider{
		OpenAI: &OpenAIFormat{Enabled: true},
	}
	if !p.IsOpenAICompatible() {
		t.Fatal("expected true when OpenAI.Enabled = true")
	}
}

// TestIsAnthropicCompatible_FallbackOldType verifies that IsAnthropicCompatible
// falls back to checking the old Type field when the new Anthropic field is nil.
func TestIsAnthropicCompatible_FallbackOldType(t *testing.T) {
	p := Provider{
		Type:      "anthropic",
		Anthropic: nil, // no new field
	}
	if !p.IsAnthropicCompatible() {
		t.Fatal("expected true when Type = anthropic (fallback)")
	}
}

// TestIsOpenAICompatible_FallbackOldAuthKey verifies that IsOpenAICompatible
// falls back to checking the old AuthKey field when the new OpenAI field is nil.
func TestIsOpenAICompatible_FallbackOldAuthKey(t *testing.T) {
	p := Provider{
		AuthKey: "OPENAI_API_KEY",
		OpenAI:  nil, // no new field
	}
	if !p.IsOpenAICompatible() {
		t.Fatal("expected true when AuthKey = OPENAI_API_KEY (fallback)")
	}
}

// ============================================================================
// M. Phase -- Persistent layer API key scrub regression tests
// ============================================================================

// TestSaveProvider_ScrubsNestedAPIKeys verifies that SaveProvider strips
// Anthropic.APIKey and OpenAI.APIKey before writing models.json, and that
// other structural fields remain intact.
func TestSaveProvider_ScrubsNestedAPIKeys(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Construct a Provider with sensitive API keys in both format structs
	p := Provider{
		DefaultModel: "test-model-v1",
		Anthropic: &AnthropicFormat{
			Enabled: true,
			APIKey:  "secret-anthropic-key-abc123",
			BaseURL: "https://api.anthropic.com",
			AuthKey: "ANTHROPIC_API_KEY",
		},
		OpenAI: &OpenAIFormat{
			Enabled:      true,
			APIKey:       "secret-openai-key-xyz789",
			BaseURL:      "https://api.openai.com/v1",
			Organization: "org-test-123",
			AuthKey:      "OPENAI_API_KEY",
		},
	}

	if err := svc.SaveProvider("test-scrub", p); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// Read the raw models.json file
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	content := string(data)

	// Assert: secrets must NOT appear in file
	if strings.Contains(content, "secret-anthropic-key-abc123") {
		t.Fatal("models.json contains anthropic API key secret -- scrub failed")
	}
	if strings.Contains(content, "secret-openai-key-xyz789") {
		t.Fatal("models.json contains openai API key secret -- scrub failed")
	}
	// Assert: the literal key name "api_key" should not appear anywhere in file
	// (since both APIKey fields should have been cleared and have omitempty)
	if strings.Contains(content, `"api_key"`) {
		t.Fatalf("models.json contains \"api_key\" field -- scrub incomplete:\n%s", content)
	}

	// Assert: structural fields must survive
	if !strings.Contains(content, `"enabled"`) {
		t.Fatal("models.json missing 'enabled' field -- scrub was too aggressive")
	}
	if !strings.Contains(content, `"default_model"`) {
		t.Fatal("models.json missing 'default_model' field -- scrub was too aggressive")
	}
	if !strings.Contains(content, `"test-model-v1"`) {
		t.Fatal("models.json missing default model value -- scrub was too aggressive")
	}
	if !strings.Contains(content, `"base_url"`) {
		t.Fatal("models.json missing 'base_url' field -- scrub was too aggressive")
	}

	// Verify via reload that data is consistent
	svc2 := NewConfigService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err := svc2.GetProvider("test-scrub")
	if err != nil {
		t.Fatalf("GetProvider after reload: %v", err)
	}
	if got.DefaultModel != "test-model-v1" {
		t.Fatalf("DefaultModel = %q, want %q", got.DefaultModel, "test-model-v1")
	}
	if got.Anthropic == nil || !got.Anthropic.Enabled {
		t.Fatal("Anthropic.Enabled should be true after reload")
	}
	if got.OpenAI == nil || !got.OpenAI.Enabled {
		t.Fatal("OpenAI.Enabled should be true after reload")
	}
	if got.Anthropic.APIKey != "" {
		t.Fatalf("Anthropic.APIKey = %q after reload, want empty", got.Anthropic.APIKey)
	}
	if got.OpenAI.APIKey != "" {
		t.Fatalf("OpenAI.APIKey = %q after reload, want empty", got.OpenAI.APIKey)
	}
}

// TestSave_ScrubsAllProviders verifies that the Save() method (non-locked path)
// also scrubs API keys from all providers before writing to disk.
func TestSave_ScrubsAllProviders(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Inject providers with API keys directly into config (bypassing SaveProvider)
	cfg := svc.GetConfig()
	cfg.Models["leaky-provider"] = Provider{
		DefaultModel: "leaky-model",
		Anthropic: &AnthropicFormat{
			Enabled: true,
			APIKey:  "leaky-secret-key",
			BaseURL: "https://leak.example.com",
		},
		OpenAI: &OpenAIFormat{
			Enabled: true,
			APIKey:  "leaky-openai-key",
			BaseURL: "https://leak-openai.example.com",
		},
	}
	// Save via direct SaveProvider which stores into internal config
	svc.SaveProvider("leaky-provider", cfg.Models["leaky-provider"])

	// Now also modify config directly and call Save()
	cfg2 := svc.GetConfig()
	// Re-inject a key to test the Save() path
	p := cfg2.Models["leaky-provider"]
	p.Anthropic.APIKey = "reinjected-save-key"
	svc.mu.Lock()
	svc.config.Models["leaky-provider"] = p
	svc.mu.Unlock()

	if err := svc.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "reinjected-save-key") {
		t.Fatal("Save() did not scrub reinjected API key")
	}
	if strings.Contains(content, `"api_key"`) {
		t.Fatalf("Save() left api_key field in file:\n%s", content)
	}
}

// TestScrubProviderAPIKeys_PreservesOtherFields verifies the unit-level
// behavior of scrubProviderAPIKeys: only APIKey is cleared, everything
// else is preserved.
func TestScrubProviderAPIKeys_PreservesOtherFields(t *testing.T) {
	p := Provider{
		DefaultModel: "model-x",
		Anthropic: &AnthropicFormat{
			Enabled: true,
			APIKey:  "should-be-gone",
			BaseURL: "https://keep-this.com",
			AuthKey: "KEEP_THIS_AUTH",
		},
		OpenAI: &OpenAIFormat{
			Enabled:      true,
			APIKey:       "also-should-be-gone",
			BaseURL:      "https://keep-this-openai.com",
			Organization: "keep-org",
			AuthKey:      "KEEP_OPENAI_AUTH",
		},
	}

	scrubbed := scrubProviderAPIKeys(p)

	// APIKey must be empty
	if scrubbed.Anthropic.APIKey != "" {
		t.Fatalf("Anthropic.APIKey = %q, want empty", scrubbed.Anthropic.APIKey)
	}
	if scrubbed.OpenAI.APIKey != "" {
		t.Fatalf("OpenAI.APIKey = %q, want empty", scrubbed.OpenAI.APIKey)
	}

	// All other fields preserved
	if !scrubbed.Anthropic.Enabled {
		t.Fatal("Anthropic.Enabled should remain true")
	}
	if scrubbed.Anthropic.BaseURL != "https://keep-this.com" {
		t.Fatalf("Anthropic.BaseURL = %q, want %q", scrubbed.Anthropic.BaseURL, "https://keep-this.com")
	}
	if scrubbed.Anthropic.AuthKey != "KEEP_THIS_AUTH" {
		t.Fatalf("Anthropic.AuthKey = %q, want %q", scrubbed.Anthropic.AuthKey, "KEEP_THIS_AUTH")
	}
	if !scrubbed.OpenAI.Enabled {
		t.Fatal("OpenAI.Enabled should remain true")
	}
	if scrubbed.OpenAI.BaseURL != "https://keep-this-openai.com" {
		t.Fatalf("OpenAI.BaseURL = %q, want %q", scrubbed.OpenAI.BaseURL, "https://keep-this-openai.com")
	}
	if scrubbed.OpenAI.Organization != "keep-org" {
		t.Fatalf("OpenAI.Organization = %q, want %q", scrubbed.OpenAI.Organization, "keep-org")
	}
	if scrubbed.OpenAI.AuthKey != "KEEP_OPENAI_AUTH" {
		t.Fatalf("OpenAI.AuthKey = %q, want %q", scrubbed.OpenAI.AuthKey, "KEEP_OPENAI_AUTH")
	}
	if scrubbed.DefaultModel != "model-x" {
		t.Fatalf("DefaultModel = %q, want %q", scrubbed.DefaultModel, "model-x")
	}
}

// TestScrubProviderAPIKeys_NilFormats verifies scrub handles nil format structs.
func TestScrubProviderAPIKeys_NilFormats(t *testing.T) {
	p := Provider{
		DefaultModel: "plain",
		Anthropic:    nil,
		OpenAI:       nil,
	}

	scrubbed := scrubProviderAPIKeys(p)
	if scrubbed.Anthropic != nil {
		t.Fatal("Anthropic should remain nil")
	}
	if scrubbed.OpenAI != nil {
		t.Fatal("OpenAI should remain nil")
	}
	if scrubbed.DefaultModel != "plain" {
		t.Fatalf("DefaultModel = %q, want %q", scrubbed.DefaultModel, "plain")
	}
}

// ============================================================================
// N. OpenCodePresets CRUD -- 新模型测试
// ============================================================================

func TestGetOpenCodePresets_Empty(t *testing.T) {
	svc := newTestConfigService(t)
	presets := svc.GetOpenCodePresets()
	if len(presets) != 0 {
		t.Fatalf("expected empty opencode_presets, got %d", len(presets))
	}
}

func TestSaveOpenCodePreset_Basic(t *testing.T) {
	svc := newTestConfigService(t)

	preset := OpenCodePreset{
		Name: "Test Preset",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {
					"options": {"baseURL": "https://api.openai.com/v1"}
				}
			}
		}`),
		Bindings: map[string]OpenCodeBinding{
			"openai": {
				LocalProvider: "openai",
				Format:        "openai",
				Inject:        []string{"apiKey", "baseURL"},
			},
		},
	}

	if err := svc.SaveOpenCodePreset("test-preset", preset); err != nil {
		t.Fatalf("SaveOpenCodePreset: %v", err)
	}

	got, err := svc.GetOpenCodePreset("test-preset")
	if err != nil {
		t.Fatalf("GetOpenCodePreset: %v", err)
	}
	if got.Name != "Test Preset" {
		t.Fatalf("Name = %q, want %q", got.Name, "Test Preset")
	}
	if got.ID != "test-preset" {
		t.Fatalf("ID = %q, want %q (auto-filled)", got.ID, "test-preset")
	}
	if len(got.Bindings) != 1 {
		t.Fatalf("Bindings count = %d, want 1", len(got.Bindings))
	}
}

// TestSaveOpenCodePreset_ScrubsAPIKey 验证保存时 apiKey 被清除。
func TestSaveOpenCodePreset_ScrubsAPIKey(t *testing.T) {
	svc := newTestConfigService(t)

	preset := OpenCodePreset{
		Name: "Leaky Preset",
		Config: json.RawMessage(`{
			"model": "openai/gpt-5",
			"provider": {
				"openai": {
					"options": {
						"apiKey": "sk-secret-key-12345",
						"baseURL": "https://api.openai.com/v1"
					}
				},
				"anthropic": {
					"options": {
						"apiKey": "sk-ant-secret-67890"
					}
				}
			}
		}`),
	}

	if err := svc.SaveOpenCodePreset("leaky", preset); err != nil {
		t.Fatalf("SaveOpenCodePreset: %v", err)
	}

	// 从磁盘读取验证
	dir := filepath.Dir(svc.configPath)
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "sk-secret-key-12345") {
		t.Fatal("models.json contains openai API key secret -- scrub failed")
	}
	if strings.Contains(content, "sk-ant-secret-67890") {
		t.Fatal("models.json contains anthropic API key secret -- scrub failed")
	}

	// 验证其他字段保留
	if !strings.Contains(content, `"baseURL"`) {
		t.Fatal("models.json missing baseURL field -- scrub was too aggressive")
	}
	if !strings.Contains(content, `"openai"`) {
		t.Fatal("models.json missing provider id 'openai' -- scrub was too aggressive")
	}

	// 验证通过 GetOpenCodePreset 读取时 Config 不含 apiKey
	got, err := svc.GetOpenCodePreset("leaky")
	if err != nil {
		t.Fatalf("GetOpenCodePreset: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(got.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if _, hasAPIKey := options["apiKey"]; hasAPIKey {
		t.Fatal("opencode_preset config still contains apiKey after save")
	}
	if options["baseURL"] != "https://api.openai.com/v1" {
		t.Fatalf("baseURL should be preserved, got %v", options["baseURL"])
	}
}

func TestSaveOpenCodePreset_AutoID(t *testing.T) {
	svc := newTestConfigService(t)

	preset := OpenCodePreset{
		Name:   "No ID",
		Config: json.RawMessage(`{}`),
	}

	if err := svc.SaveOpenCodePreset("my-key", preset); err != nil {
		t.Fatalf("SaveOpenCodePreset: %v", err)
	}

	got, _ := svc.GetOpenCodePreset("my-key")
	if got.ID != "my-key" {
		t.Fatalf("ID = %q, want %q (auto-filled from key)", got.ID, "my-key")
	}

	// 明确设置了 ID 则保留
	preset2 := OpenCodePreset{
		ID:     "explicit-id",
		Name:   "Has ID",
		Config: json.RawMessage(`{}`),
	}
	svc.SaveOpenCodePreset("key2", preset2)
	got2, _ := svc.GetOpenCodePreset("key2")
	if got2.ID != "explicit-id" {
		t.Fatalf("ID = %q, want %q (explicit)", got2.ID, "explicit-id")
	}
}

func TestSaveOpenCodePreset_NormalizesConfig(t *testing.T) {
	svc := newTestConfigService(t)

	// 双重编码的 Config
	inner := `{"model":"openai/gpt-5"}`
	doubleEncoded, _ := json.Marshal(inner)

	preset := OpenCodePreset{
		Name:   "Double Encoded",
		Config: json.RawMessage(doubleEncoded),
	}

	if err := svc.SaveOpenCodePreset("de", preset); err != nil {
		t.Fatalf("SaveOpenCodePreset: %v", err)
	}

	got, _ := svc.GetOpenCodePreset("de")
	if len(got.Config) == 0 || got.Config[0] != '{' {
		t.Fatalf("Config should be normalized to start with '{', got: %s", string(got.Config))
	}
}

func TestDeleteOpenCodePreset(t *testing.T) {
	svc := newTestConfigService(t)

	preset := OpenCodePreset{Name: "To Delete", Config: json.RawMessage(`{}`)}
	if err := svc.SaveOpenCodePreset("del-me", preset); err != nil {
		t.Fatalf("SaveOpenCodePreset: %v", err)
	}
	if err := svc.SaveTerminalPreset("opencode", "del-me", TerminalPreset{Name: "Legacy To Delete", Provider: "openai", Model: "gpt-5"}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}

	if err := svc.DeleteOpenCodePreset("del-me"); err != nil {
		t.Fatalf("DeleteOpenCodePreset: %v", err)
	}

	if _, err := svc.GetOpenCodePreset("del-me"); err == nil {
		t.Fatal("expected error after delete, got nil")
	}
	legacy, err := svc.GetTerminalPresets("opencode")
	if err != nil {
		t.Fatalf("GetTerminalPresets: %v", err)
	}
	if _, ok := legacy["del-me"]; ok {
		t.Fatal("legacy terminal preset should be deleted together with opencode preset")
	}
}

func TestDeleteOpenCodePreset_NonExistent(t *testing.T) {
	svc := newTestConfigService(t)
	err := svc.DeleteOpenCodePreset("ghost")
	if err != nil {
		t.Fatalf("deleting non-existent should not error, got: %v", err)
	}
}

func TestSaveOpenCodePreset_EmptyKey(t *testing.T) {
	svc := newTestConfigService(t)
	err := svc.SaveOpenCodePreset("", OpenCodePreset{})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestOpenCodePresets_PersistAcrossLoad(t *testing.T) {
	dir := t.TempDir()
	svc1 := NewConfigService(dir)
	svc1.Load()

	preset := OpenCodePreset{
		Name:   "Persistent",
		Config: json.RawMessage(`{"model":"openai/gpt-5"}`),
		Bindings: map[string]OpenCodeBinding{
			"openai": {LocalProvider: "openai", Format: "openai"},
		},
	}
	svc1.SaveOpenCodePreset("persist-test", preset)
	svc1.Save()

	svc2 := NewConfigService(dir)
	svc2.Load()

	got, err := svc2.GetOpenCodePreset("persist-test")
	if err != nil {
		t.Fatalf("GetOpenCodePreset after reload: %v", err)
	}
	if got.Name != "Persistent" {
		t.Fatalf("Name = %q, want Persistent", got.Name)
	}
	if len(got.Bindings) != 1 {
		t.Fatalf("Bindings count = %d, want 1", len(got.Bindings))
	}
}

// TestMigrateTerminalPresetsToOpenCodePresets 验证 terminal_presets.opencode
// 自动迁移到 opencode_presets。
func TestMigrateTerminalPresetsToOpenCodePresets(t *testing.T) {
	svc := newTestConfigService(t)

	// 创建一个 terminal_preset (opencode 类型)
	tp := TerminalPreset{
		Name:     "Migrated OC",
		Provider: "openai",
		Model:    "gpt-5",
		OpenCodeCfg: json.RawMessage(`{
			"model": "openai/gpt-5",
			"theme": "dark",
			"provider": {
				"openai": {
					"options": {"apiKey": "sk-secret-to-be-removed", "baseURL": "https://api.openai.com/v1"}
				}
			}
		}`),
	}
	svc.SaveTerminalPreset("opencode", "openai/my-oc", tp)

	// 重新加载触发自动迁移
	svc.Save()
	svc2 := NewConfigService(filepath.Dir(svc.configPath))
	svc2.Load()

	// 验证 opencode_presets 中有迁移后的条目
	got, err := svc2.GetOpenCodePreset("openai/my-oc")
	if err != nil {
		t.Fatalf("expected migrated opencode_preset at 'openai/my-oc', got error: %v", err)
	}

	if got.Name != "Migrated OC" {
		t.Fatalf("Name = %q, want %q", got.Name, "Migrated OC")
	}
	if got.Source == nil || got.Source.Kind != "migrated-overlay" {
		t.Fatalf("Source.Kind should be 'migrated-overlay', got %v", got.Source)
	}
	if got.Source.LegacyProvider != "openai" {
		t.Fatalf("Source.LegacyProvider = %q, want openai", got.Source.LegacyProvider)
	}

	// 验证 Config 不含 apiKey（scrubbed）
	var cfg map[string]any
	if err := json.Unmarshal(got.Config, &cfg); err != nil {
		t.Fatalf("unmarshal migrated config: %v", err)
	}
	providers, _ := cfg["provider"].(map[string]any)
	openaiProv, _ := providers["openai"].(map[string]any)
	options, _ := openaiProv["options"].(map[string]any)
	if _, hasAPIKey := options["apiKey"]; hasAPIKey {
		t.Fatal("migrated config should NOT contain apiKey (should be scrubbed)")
	}

	// 验证 bindings
	if len(got.Bindings) == 0 {
		t.Fatal("migrated preset should have at least one binding")
	}

	// 验证幂等：再次 reload 不会覆盖
	svc2.Save()
	svc3 := NewConfigService(filepath.Dir(svc.configPath))
	svc3.Load()
	got2, _ := svc3.GetOpenCodePreset("openai/my-oc")
	if got2.Source.Kind != "migrated-overlay" {
		t.Fatalf("second reload should preserve migrated preset")
	}
}

// TestMigrateTerminalPresetsToOpenCodePresets_NoOverwriteExisting
// 验证迁移不会覆盖已有的 opencode_preset。
func TestMigrateTerminalPresetsToOpenCodePresets_NoOverwriteExisting(t *testing.T) {
	svc := newTestConfigService(t)

	// 先创建一个 opencode_preset（native）
	svc.SaveOpenCodePreset("native-key", OpenCodePreset{
		Name:   "Native Preset",
		Config: json.RawMessage(`{"model":"openai/gpt-4"}`),
		Source: &OpenCodePresetSource{Kind: "native"},
	})

	// 再创建一个同名的 terminal_preset
	svc.SaveTerminalPreset("opencode", "native-key", TerminalPreset{
		Name:     "Legacy Preset",
		Provider: "openai",
		Model:    "gpt-5",
	})

	// 重新加载触发迁移
	svc.Save()
	svc2 := NewConfigService(filepath.Dir(svc.configPath))
	svc2.Load()

	got, _ := svc2.GetOpenCodePreset("native-key")
	// 不应被覆盖
	if got.Name != "Native Preset" {
		t.Fatalf("Name = %q, want 'Native Preset' (should not be overwritten)", got.Name)
	}
	if got.Source.Kind != "native" {
		t.Fatalf("Source.Kind = %q, want 'native' (should not be overwritten)", got.Source.Kind)
	}
}

// ============================================================================
// O. OpenCodePreset scrub / normalize edge cases
// ============================================================================

func TestScrubOpenCodePresetConfig_NilConfig(t *testing.T) {
	result := scrubOpenCodePresetConfig(nil)
	if result != nil {
		t.Fatalf("nil config should return nil, got %s", string(result))
	}
}

func TestScrubOpenCodePresetConfig_NoProvider(t *testing.T) {
	raw := json.RawMessage(`{"model":"openai/gpt-5","theme":"dark"}`)
	result := scrubOpenCodePresetConfig(raw)
	if string(result) != string(raw) {
		t.Fatalf("config without provider should be unchanged\ngot:  %s\nwant: %s", string(result), string(raw))
	}
}

func TestScrubOpenCodePresetConfig_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`not valid json`)
	result := scrubOpenCodePresetConfig(raw)
	if string(result) != string(raw) {
		t.Fatalf("invalid JSON should be returned as-is")
	}
}

func TestNormalizeOpenCodePresetConfig_Empty(t *testing.T) {
	result := normalizeOpenCodePresetConfig(nil)
	if result != nil {
		t.Fatal("nil should return nil")
	}
	result = normalizeOpenCodePresetConfig(json.RawMessage(""))
	if result != nil {
		t.Fatal("empty should return nil")
	}
	result = normalizeOpenCodePresetConfig(json.RawMessage("  "))
	if result != nil {
		t.Fatal("whitespace should return nil")
	}
}

func TestNormalizeOpenCodePresetConfig_DoubleEncoded(t *testing.T) {
	inner := `{"model":"openai/gpt-5"}`
	doubleEncoded, _ := json.Marshal(inner)

	result := normalizeOpenCodePresetConfig(json.RawMessage(doubleEncoded))
	if result[0] != '{' {
		t.Fatalf("should start with '{', got: %s", string(result))
	}
}

func TestGetOpenCodePreset_NotFound(t *testing.T) {
	svc := newTestConfigService(t)
	_, err := svc.GetOpenCodePreset("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent preset")
	}
}

func TestGetOpenCodePreset_ConfigNotLoaded(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	// Don't call Load()
	_, err := svc.GetOpenCodePreset("any")
	if err == nil {
		t.Fatal("expected error when config not loaded")
	}
}

func TestSaveOpenCodePreset_ConfigNotLoaded(t *testing.T) {
	dir := t.TempDir()
	svc := NewConfigService(dir)
	err := svc.SaveOpenCodePreset("any", OpenCodePreset{})
	if err == nil {
		t.Fatal("expected error when config not loaded")
	}
}

// ============================================================================
// P. Phase 6 -- Reasoning Effort 字段测试
// ============================================================================

func TestSaveTerminalPreset_ValidReasoningEffort(t *testing.T) {
	svc := newTestConfigService(t)

	validEfforts := []string{"", "low", "medium", "high", "xhigh", "max"}
	for _, effort := range validEfforts {
		tp := TerminalPreset{
			Name:     "Valid Effort",
			Provider: "anthropic",
			Model:    "claude-sonnet-4-20250514",
			Parameters: Parameters{
				ReasoningEffort: effort,
			},
		}
		err := svc.SaveTerminalPreset("claude_code", "valid-"+effort, tp)
		if err != nil {
			t.Fatalf("SaveTerminalPreset with valid reasoning_effort=%q failed: %v", effort, err)
		}
	}
}

func TestSaveTerminalPreset_InvalidReasoningEffort(t *testing.T) {
	svc := newTestConfigService(t)

	invalidEfforts := []string{"invalid", "none", "ultra", "LOW", "Medium", "HIGH"}
	for _, effort := range invalidEfforts {
		tp := TerminalPreset{
			Name:     "Invalid Effort",
			Provider: "anthropic",
			Model:    "claude-sonnet-4-20250514",
			Parameters: Parameters{
				ReasoningEffort: effort,
			},
		}
		err := svc.SaveTerminalPreset("claude_code", "invalid-"+effort, tp)
		if err == nil {
			t.Fatalf("SaveTerminalPreset with invalid reasoning_effort=%q should fail, but got nil", effort)
		}
	}
}

func TestIsValidClaudeReasoningEffort(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"low", true},
		{"medium", true},
		{"high", true},
		{"xhigh", true},
		{"max", true},
		{"invalid", false},
		{"none", false},
		{"ultra", false},
		{"LOW", false},    // 大小写敏感
		{"Medium", false}, // 大小写敏感
	}

	for _, tt := range tests {
		got := IsValidClaudeReasoningEffort(tt.input)
		if got != tt.want {
			t.Errorf("IsValidClaudeReasoningEffort(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestCompressDuplicatedPrefixPresetKey 验证 terminal_preset key 的重复前缀压缩逻辑。
// 覆盖：2 段纯重复、3/4 层含 tail、正常 key 不变、幂等性、边界。
func TestCompressDuplicatedPrefixPresetKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string
		want    bool
	}{
		// 2 段纯重复（M1 新增覆盖）
		{"2段纯重复压缩", "glm/glm", "glm", true},
		{"2段纯重复其他前缀", "agent/agent", "agent", true},
		// 3+ 层含 tail（现有逻辑）
		{"3层含tail压缩", "glm/glm/glm/max", "glm/max", true},
		{"4层含tail压缩", "glm/glm/glm/glm/max", "glm/max", true},
		// 正常 key 不变（幂等保证）
		{"prefix+name正常key不变", "glm/max", "glm/max", false},
		{"prefix+其他name不变", "glm/code", "glm/code", false},
		{"单段不变", "agent", "agent", false},
		// 幂等性：已压缩结果再跑不变
		{"压缩后glm不变", "glm", "glm", false},
		// 边界
		{"空串不变", "", "", false},
		{"前缀段不同不压缩", "glm/max/glm", "glm/max/glm", false},
		{"含空段不误压缩", "glm//max", "glm//max", false},
		{"尾段空保持压缩", "glm/glm/", "glm/", true},
		{"仅2段但非纯重复不压缩", "glm/max", "glm/max", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, got := compressDuplicatedPrefixPresetKey(tt.input)
			if gotKey != tt.wantKey || got != tt.want {
				t.Errorf("compressDuplicatedPrefixPresetKey(%q) = (%q, %v), want (%q, %v)",
					tt.input, gotKey, got, tt.wantKey, tt.want)
			}
		})
	}
}

// TestCompressDuplicatedPrefixPresetKey_Idempotent 验证二次压缩结果稳定。
func TestCompressDuplicatedPrefixPresetKey_Idempotent(t *testing.T) {
	inputs := []string{
		"glm/glm",
		"glm/glm/glm/max",
		"glm/glm/glm/glm/max",
		"glm/max",
		"agent",
	}
	for _, in := range inputs {
		first, changed1 := compressDuplicatedPrefixPresetKey(in)
		second, changed2 := compressDuplicatedPrefixPresetKey(first)
		if second != first {
			t.Errorf("idempotent failed: %q -> %q -> %q", in, first, second)
		}
		if changed1 && changed2 {
			t.Errorf("idempotent failed: second pass still changed for %q (first=%q)", in, first)
		}
	}
}
