package envcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// makeAllowedConfigDir creates a temporary directory structure that matches
// the isConfigPathAllowed whitelist (<tmpdir>/.claude/settings.json).
func makeAllowedConfigDir(t *testing.T) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Override cwd to the temp dir so isConfigPathAllowed allows it.
	t.Setenv("PWD_OVERRIDE_FOR_TEST", tmpDir)
	configPath := filepath.Join(claudeDir, "settings.json")
	return tmpDir, configPath
}

// ---------------------------------------------------------------------------
// fixClaudeConfig
// ---------------------------------------------------------------------------

func TestFixClaudeConfig_WriteNewKey(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	// Create empty config
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Save and restore cwd
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "1",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got message: %s", result.Message)
	}
	if !result.Changed {
		t.Error("expected Changed=true for new key")
	}

	// Verify written content
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["API_TIMEOUT_MS"] != "1" {
		t.Errorf("API_TIMEOUT_MS = %v, want %q", parsed["API_TIMEOUT_MS"], "1")
	}
}

func TestFixClaudeConfig_NoOverwrite(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	// Create config with existing API_TIMEOUT_MS = "0"
	initial := map[string]interface{}{"API_TIMEOUT_MS": "0"}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "1",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Changed {
		t.Error("should NOT overwrite existing key with non-empty value")
	}

	// Verify original value is preserved
	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["API_TIMEOUT_MS"] != "0" {
		t.Errorf("API_TIMEOUT_MS was incorrectly overwritten; got %v, want %q", parsed["API_TIMEOUT_MS"], "0")
	}
}

func TestFixClaudeConfig_NestedKey(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS",
		Value:    "https://api.test.com",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success || !result.Changed {
		t.Errorf("expected success+changed, got success=%v changed=%v", result.Success, result.Changed)
	}

	// Verify nested write
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	env, ok := parsed["env"].(map[string]interface{})
	if !ok {
		t.Fatal("env key should be a nested object")
	}
	if env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] != "https://api.test.com" {
		t.Errorf("env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS = %v, want %q", env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"], "https://api.test.com")
	}
}

func TestFixClaudeConfig_BackupCreated(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	if err := os.WriteFile(configPath, []byte(`{"existing":"value"}`), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "1",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.BackupPath == "" {
		t.Error("backup should be created when modifying existing file with content")
	}
	// Verify backup file exists
	if _, statErr := os.Stat(result.BackupPath); os.IsNotExist(statErr) {
		t.Errorf("backup file was not created at %q", result.BackupPath)
	}
}

func TestFixClaudeConfig_CreatesParentDir(t *testing.T) {
	// This test now verifies that arbitrary paths are REJECTED.
	// The fixClaudeConfig function no longer allows creating files in arbitrary
	// directories; only .claude/settings.json paths are allowed.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "subdir", "settings.json")

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "1",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("should reject arbitrary path that is not in allowed set")
	}
	if result.Error == "" {
		t.Error("error message should explain the path was rejected")
	}
}

func TestFixClaudeConfig_DefaultValue(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	// Value is empty => should use default from predefinedConfigItems
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "", // use default
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Message)
	}

	// Verify default value was written
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["API_TIMEOUT_MS"] != "3000000" {
		t.Errorf("default value not written; got %v, want %q", parsed["API_TIMEOUT_MS"], "3000000")
	}
}

func TestFixClaudeConfig_NoDefaultNoValue(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "unknown.custom.key", // not in predefinedConfigItems, no default
		Value:    "",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("should fail when key is not in predefinedConfigItems and value is empty")
	}
}

func TestFixClaudeConfig_EmptyExistingKeyOverwrites(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	// Create config with an empty-string value
	if err := os.WriteFile(configPath, []byte(`{"API_TIMEOUT_MS":""}`), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "1",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty string should be treated as "not configured", so overwrite is allowed
	if !result.Changed {
		t.Error("expected Changed=true when overwriting empty existing key")
	}

	// Verify value was written
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["API_TIMEOUT_MS"] != "1" {
		t.Errorf("API_TIMEOUT_MS = %v, want %q", parsed["API_TIMEOUT_MS"], "1")
	}
}

func TestFixClaudeConfig_PreservesExistingKeys(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	initial := `{"existing_key":"preserved","another":42}`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "1",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Message)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["existing_key"] != "preserved" {
		t.Error("existing keys should be preserved")
	}
	if parsed["another"] != float64(42) {
		t.Error("existing numeric keys should be preserved")
	}
	if parsed["API_TIMEOUT_MS"] != "1" {
		t.Error("new key should be added")
	}
}

func TestFixClaudeConfig_RejectsUnknownKey(t *testing.T) {
	tmpDir, configPath := makeAllowedConfigDir(t)

	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "unknown.random.key",
		Value:    "test",
		FilePath: configPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("should reject unknown configuration key")
	}
}

func TestFixClaudeConfig_RejectsArbitraryPath(t *testing.T) {
	tmpDir := t.TempDir()
	arbitraryPath := filepath.Join(tmpDir, "evil", "config.json")

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	svc := &Service{}
	result, err := svc.fixClaudeConfig(ConfigFixRequest{
		Key:      "API_TIMEOUT_MS",
		Value:    "1",
		FilePath: arbitraryPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("should reject arbitrary file path outside allowed set")
	}
}

func TestConfigPathAllowedRejectsWindowsSystem32EvenWhenCWDThere(t *testing.T) {
	// Do not chdir into a real system directory; the path validator must reject
	// the canonical target regardless of the current process directory.
	for _, target := range []string{
		`C:\Windows\System32\claude\settings.json`,
		`C:\Windows\System32\.claude\settings.json`,
		`C:/Windows/System32/.claude/settings.local.json`,
	} {
		if isConfigPathAllowed(target) {
			t.Fatalf("system directory target should be rejected: %s", target)
		}
	}
}

func TestConfigPathAllowedAllowsUserClaudeSettings(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		t.Skip("cannot resolve user home")
	}

	for _, target := range []string{
		filepath.Join(homeDir, ".claude.json"),
		filepath.Join(homeDir, ".claude", "settings.json"),
	} {
		if !isConfigPathAllowed(target) {
			t.Fatalf("user Claude config target should be allowed: %s", target)
		}
	}
}

func TestConfigFilePathsDoNotUseProtectedCWD(t *testing.T) {
	protected := `C:\Windows\System32`
	if !isProtectedConfigPath(protected) {
		t.Fatalf("protected cwd must be detected before it can become a trusted project config root")
	}
}

// ---------------------------------------------------------------------------
// expandTilde
// ---------------------------------------------------------------------------

func TestExpandTilde(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home directory")
	}

	result := expandTilde("~/test/path")
	expected := filepath.Join(homeDir, "test/path")
	if result != expected {
		t.Errorf("expandTilde('~/test/path') = %q, want %q", result, expected)
	}
}

func TestExpandTilde_JustTilde(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home directory")
	}

	result := expandTilde("~")
	if result != homeDir {
		t.Errorf("expandTilde('~') = %q, want %q", result, homeDir)
	}
}

func TestExpandTilde_NoTilde(t *testing.T) {
	result := expandTilde("/absolute/path")
	if result != "/absolute/path" {
		t.Errorf("path without tilde should not change, got %q", result)
	}
}

func TestExpandTilde_Empty(t *testing.T) {
	result := expandTilde("")
	if result != "" {
		t.Errorf("empty path should stay empty, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// getNestedValue / setNestedValue
// ---------------------------------------------------------------------------

func TestGetNestedValue_SimpleKey(t *testing.T) {
	data := map[string]interface{}{
		"key": "value",
	}
	val, ok := getNestedValue(data, []string{"key"})
	if !ok {
		t.Fatal("expected to find key")
	}
	if val != "value" {
		t.Errorf("val = %v, want %q", val, "value")
	}
}

func TestGetNestedValue_NestedKey(t *testing.T) {
	data := map[string]interface{}{
		"env": map[string]interface{}{
			"ANTHROPIC_BASE_URL": "https://api.example.com",
		},
	}
	val, ok := getNestedValue(data, []string{"env", "ANTHROPIC_BASE_URL"})
	if !ok {
		t.Fatal("expected to find nested key")
	}
	if val != "https://api.example.com" {
		t.Errorf("val = %v, want %q", val, "https://api.example.com")
	}
}

func TestGetNestedValue_MissingKey(t *testing.T) {
	data := map[string]interface{}{
		"key": "value",
	}
	_, ok := getNestedValue(data, []string{"nonexistent"})
	if ok {
		t.Error("should not find missing key")
	}
}

func TestGetNestedValue_EmptyParts(t *testing.T) {
	data := map[string]interface{}{
		"key": "value",
	}
	_, ok := getNestedValue(data, []string{})
	if ok {
		t.Error("empty key parts should return false")
	}
}

func TestGetNestedValue_IntermediateNotMap(t *testing.T) {
	data := map[string]interface{}{
		"env": "not-a-map",
	}
	_, ok := getNestedValue(data, []string{"env", "subkey"})
	if ok {
		t.Error("should not find key when intermediate value is not a map")
	}
}

func TestSetNestedValue_SimpleKey(t *testing.T) {
	data := make(map[string]interface{})
	if err := setNestedValue(data, []string{"key"}, "value"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data["key"] != "value" {
		t.Errorf("data['key'] = %v, want %q", data["key"], "value")
	}
}

func TestSetNestedValue_NestedKey(t *testing.T) {
	data := make(map[string]interface{})
	if err := setNestedValue(data, []string{"env", "ANTHROPIC_BASE_URL"}, "https://api.test.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env, ok := data["env"].(map[string]interface{})
	if !ok {
		t.Fatal("env should be a nested map")
	}
	if env["ANTHROPIC_BASE_URL"] != "https://api.test.com" {
		t.Errorf("env.ANTHROPIC_BASE_URL = %v, want %q", env["ANTHROPIC_BASE_URL"], "https://api.test.com")
	}
}

func TestSetNestedValue_DeepNesting(t *testing.T) {
	data := make(map[string]interface{})
	if err := setNestedValue(data, []string{"a", "b", "c"}, "deep_value"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a, ok := data["a"].(map[string]interface{})
	if !ok {
		t.Fatal("a should be a map")
	}
	b, ok := a["b"].(map[string]interface{})
	if !ok {
		t.Fatal("a.b should be a map")
	}
	if b["c"] != "deep_value" {
		t.Errorf("a.b.c = %v, want %q", b["c"], "deep_value")
	}
}

func TestSetNestedValue_EmptyParts(t *testing.T) {
	data := make(map[string]interface{})
	if err := setNestedValue(data, []string{}, "value"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not panic; data should remain empty
	if len(data) != 0 {
		t.Error("empty key parts should not modify data")
	}
}

func TestSetNestedValue_OverwritesNonMapIntermediate(t *testing.T) {
	data := map[string]interface{}{
		"env": "string-value",
	}
	err := setNestedValue(data, []string{"env", "KEY"}, "val")
	// The string "string-value" should NOT be replaced with a map; should return error
	if err == nil {
		t.Fatal("expected error when intermediate node is not a map, but got nil")
	}
	// Original data should be unchanged
	if data["env"] != "string-value" {
		t.Errorf("env was unexpectedly changed to %v; original value should be preserved", data["env"])
	}
}

// ---------------------------------------------------------------------------
// Extended AC5/AC6: protected path coverage for all protected roots
// ---------------------------------------------------------------------------

func TestIsProtectedConfigPath_AllProtectedRoots(t *testing.T) {
	protectedCases := []struct {
		name string
		path string
	}{
		{"system32 settings", `C:\Windows\System32\.claude\settings.json`},
		{"system32 claude dir", `C:\Windows\System32\claude\settings.json`},
		{"syswow64 settings", `C:\Windows\SysWOW64\.claude\settings.json`},
		{"program files settings", `C:\Program Files\.claude\settings.json`},
		{"program files nested", `C:\Program Files\Claude\settings.json`},
		{"program files x86 settings", `C:\Program Files (x86)\.claude\settings.json`},
		{"program files x86 nested", `C:\Program Files (x86)\SomeApp\config.json`},
		{"windows root settings", `C:\Windows\.claude\settings.json`},
		{"windows forward slash", `C:/Windows/System32/.claude/settings.json`},
		{"mixed case system32", `C:\WINDOWS\SYSTEM32\.claude\settings.json`},
	}

	for _, tc := range protectedCases {
		t.Run(tc.name, func(t *testing.T) {
			if !isProtectedConfigPath(tc.path) {
				t.Errorf("expected path to be protected: %s", tc.path)
			}
		})
	}
}

func TestIsProtectedConfigPath_NonProtectedPaths(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		t.Skip("cannot resolve user home")
	}

	nonProtectedCases := []struct {
		name string
		path string
	}{
		{"user home", homeDir},
		{"user claude settings", filepath.Join(homeDir, ".claude", "settings.json")},
		{"user claude json", filepath.Join(homeDir, ".claude.json")},
		{"temp dir", os.TempDir()},
	}

	for _, tc := range nonProtectedCases {
		t.Run(tc.name, func(t *testing.T) {
			if isProtectedConfigPath(tc.path) {
				t.Errorf("expected path to NOT be protected: %s", tc.path)
			}
		})
	}
}

func TestIsConfigPathAllowed_RejectsAllProtectedRoots(t *testing.T) {
	rejectedPaths := []struct {
		name string
		path string
	}{
		{"program files settings", `C:\Program Files\.claude\settings.json`},
		{"program files x86 settings", `C:\Program Files (x86)\.claude\settings.json`},
		{"windows system32 settings", `C:\Windows\System32\.claude\settings.json`},
		{"windows syswow64 settings", `C:\Windows\SysWOW64\.claude\settings.json`},
		{"windows root claude json", `C:\Windows\.claude.json`},
	}

	for _, tc := range rejectedPaths {
		t.Run(tc.name, func(t *testing.T) {
			if isConfigPathAllowed(tc.path) {
				t.Errorf("expected protected path to be rejected: %s", tc.path)
			}
		})
	}
}

func TestNormalizeConfigSecurityPath_Consistency(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`C:\Windows\System32`, "c:/windows/system32"},
		{`C:/Windows/System32/`, "c:/windows/system32"},
		{"", ""},
		{"  ", ""},
		{`C:\Program Files`, "c:/program files"},
	}

	for _, tc := range tests {
		got := normalizeConfigSecurityPath(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeConfigSecurityPath(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
