package envcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// checkClaudeConfig
// ---------------------------------------------------------------------------

func TestCheckClaudeConfig_NoConfigFiles(t *testing.T) {
	// In a directory with no .claude/ files, checkClaudeConfig should
	// return a valid ClaudeConfigStatus with all items unconfigured.
	svc := &Service{}
	status, err := svc.checkClaudeConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status == nil {
		t.Fatal("status should not be nil")
	}
	if status.ConfigItems == nil {
		t.Error("ConfigItems should not be nil")
	}
	// All items should be unconfigured since no config files exist in
	// the test environment (the project itself is not in ~/.claude/).
	if len(status.ConfigItems) != len(predefinedConfigItems) {
		t.Errorf("ConfigItems count = %d, want %d", len(status.ConfigItems), len(predefinedConfigItems))
	}
}

func TestCheckClaudeConfig_WithMockConfig(t *testing.T) {
	// Create a temporary .claude directory with settings.json
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write test config: env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS is set but env.CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS is not
	testConfig := map[string]interface{}{
		"env": map[string]interface{}{
			"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1",
		},
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
	data, err := json.MarshalIndent(testConfig, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	// Switch to temp directory so configFilePaths() finds it
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	svc := &Service{}
	status, err := svc.checkClaudeConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status == nil {
		t.Fatal("status should not be nil")
	}

	// Verify: env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS should be configured
	// Note: global ~/.claude.json may also configure items, so we only
	// assert POSITIVE (items that SHOULD be configured), not negative.
	foundAgentTeams := false
	foundNonEssential := false
	for _, item := range status.ConfigItems {
		switch item.Key {
		case "env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":
			foundAgentTeams = true
			if !item.Configured {
				t.Error("env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS should be configured")
			}
		case "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":
			foundNonEssential = true
			if !item.Configured {
				t.Error("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC should be configured")
			}
		}
	}
	if !foundAgentTeams {
		t.Error("env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS not found in config items")
	}
	if !foundNonEssential {
		t.Error("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC not found in config items")
	}
}

// ---------------------------------------------------------------------------
// flattenJSON
// ---------------------------------------------------------------------------

func TestFlattenJSON_Nested(t *testing.T) {
	input := map[string]interface{}{
		"env": map[string]interface{}{
			"ANTHROPIC_BASE_URL": "https://api.example.com",
			"ANTHROPIC_MODEL":    "claude-sonnet-4-20250514",
		},
		"permissions": map[string]interface{}{
			"allow": []interface{}{"Bash", "Read"},
		},
		"DISABLE_AUTOUPDATER": "1",
	}

	result := make(map[string]string)
	flattenJSON(input, "", result)

	// Verify nested string values
	if result["env.ANTHROPIC_BASE_URL"] != "https://api.example.com" {
		t.Errorf("env.ANTHROPIC_BASE_URL = %q, want %q", result["env.ANTHROPIC_BASE_URL"], "https://api.example.com")
	}
	if result["env.ANTHROPIC_MODEL"] != "claude-sonnet-4-20250514" {
		t.Errorf("env.ANTHROPIC_MODEL = %q, want %q", result["env.ANTHROPIC_MODEL"], "claude-sonnet-4-20250514")
	}
	// Verify top-level value
	if result["DISABLE_AUTOUPDATER"] != "1" {
		t.Errorf("DISABLE_AUTOUPDATER = %q, want %q", result["DISABLE_AUTOUPDATER"], "1")
	}
	// Verify array serialization
	if result["permissions.allow"] != `["Bash","Read"]` {
		t.Errorf("permissions.allow = %q, want %q", result["permissions.allow"], `["Bash","Read"]`)
	}
}

func TestFlattenJSON_Empty(t *testing.T) {
	result := make(map[string]string)
	flattenJSON(map[string]interface{}{}, "", result)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d items", len(result))
	}
}

func TestFlattenJSON_NilValue(t *testing.T) {
	input := map[string]interface{}{
		"key": nil,
	}
	result := make(map[string]string)
	flattenJSON(input, "", result)
	// nil values should be skipped (treated as not configured)
	if _, ok := result["key"]; ok {
		t.Error("nil values should not appear in flattened result")
	}
}

func TestFlattenJSON_BoolValue(t *testing.T) {
	input := map[string]interface{}{
		"enabled": true,
	}
	result := make(map[string]string)
	flattenJSON(input, "", result)
	if result["enabled"] != "true" {
		t.Errorf("enabled = %q, want %q", result["enabled"], "true")
	}
}

func TestFlattenJSON_FloatValue(t *testing.T) {
	input := map[string]interface{}{
		"count": float64(42),
	}
	result := make(map[string]string)
	flattenJSON(input, "", result)
	if result["count"] != "42" {
		t.Errorf("count = %q, want %q", result["count"], "42")
	}
}

func TestFlattenJSON_WithPrefix(t *testing.T) {
	input := map[string]interface{}{
		"key": "value",
	}
	result := make(map[string]string)
	flattenJSON(input, "prefix", result)
	if result["prefix.key"] != "value" {
		t.Errorf("prefix.key = %q, want %q", result["prefix.key"], "value")
	}
}

// ---------------------------------------------------------------------------
// maskSensitiveValue
// ---------------------------------------------------------------------------

func TestMaskSensitiveValue(t *testing.T) {
	token := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"
	masked := maskSensitiveValue("env.ANTHROPIC_AUTH_TOKEN", token)
	if masked == token {
		t.Error("sensitive value should be masked")
	}
	if len(masked) < 4 {
		t.Error("masked value too short")
	}
	// Should show first 4 and last 4 chars
	expected := "sk-a****wxyz"
	if masked != expected {
		t.Errorf("masked = %q, want %q", masked, expected)
	}
}

func TestMaskSensitiveValue_ShortToken(t *testing.T) {
	// Tokens <= 8 chars should be fully masked
	masked := maskSensitiveValue("env.ANTHROPIC_AUTH_TOKEN", "short")
	if masked != "****" {
		t.Errorf("short token should be fully masked, got %q", masked)
	}
}

func TestMaskSensitiveValue_NonSensitive(t *testing.T) {
	// Non-sensitive keys should not be masked
	normal := maskSensitiveValue("DISABLE_AUTOUPDATER", "1")
	if normal != "1" {
		t.Errorf("non-sensitive value should not be masked, got %q", normal)
	}
}

func TestMaskSensitiveValue_APIKey(t *testing.T) {
	// ANTHROPIC_API_KEY should also be masked
	key := "sk-ant-api03-1234567890abcdef"
	masked := maskSensitiveValue("env.ANTHROPIC_API_KEY", key)
	if masked == key {
		t.Error("API key should be masked")
	}
}

// ---------------------------------------------------------------------------
// resolveConfigFilePath
// ---------------------------------------------------------------------------

func TestResolveConfigFilePath_NoPaths(t *testing.T) {
	result := resolveConfigFilePath(nil, "env.ANTHROPIC_BASE_URL")
	if result != "" {
		t.Errorf("expected empty result for empty paths, got %q", result)
	}
}

func TestResolveConfigFilePath_FirstPathAsDefault(t *testing.T) {
	tmpDir := t.TempDir()
	paths := []string{
		filepath.Join(tmpDir, "settings.json"),
	}
	// No file exists; should return first path as default
	result := resolveConfigFilePath(paths, "nonexistent.key")
	if result != paths[0] {
		t.Errorf("expected first path as default, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// configFilePaths
// ---------------------------------------------------------------------------

func TestConfigFilePaths_ReturnsPaths(t *testing.T) {
	paths := configFilePaths()
	// Should return at least one path (home dir)
	if len(paths) == 0 {
		t.Error("configFilePaths() should return at least one path")
	}
}
