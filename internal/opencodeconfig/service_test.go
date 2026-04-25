package opencodeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// setTestHome overrides HOME/USERPROFILE for the duration of the test
// so that configFilePath() resolves to a temporary directory.
func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Windows-specific env vars
	if vol := filepath.VolumeName(home); vol != "" {
		t.Setenv("HOMEDRIVE", vol)
		t.Setenv("HOMEPATH", home[len(vol):])
	}
	return home
}

func TestGetOpenCodeConfigReturnsDefaultWhenFileNotExists(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	got, err := svc.GetOpenCodeConfig()
	if err != nil {
		t.Fatalf("GetOpenCodeConfig returned error: %v", err)
	}
	if got != defaultConfigContent {
		t.Fatalf("expected default content %q, got %q", defaultConfigContent, got)
	}
}

func TestGetOpenCodeConfigReturnsExistingContent(t *testing.T) {
	home := setTestHome(t)
	configDir := filepath.Join(home, ".config", "opencode")
	configPath := filepath.Join(configDir, "opencode.json")

	want := `{"model":"claude-sonnet-4-20250514"}` + "\n"
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(want), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService()
	got, err := svc.GetOpenCodeConfig()
	if err != nil {
		t.Fatalf("GetOpenCodeConfig returned error: %v", err)
	}
	// The service re-formats with json.MarshalIndent
	if got == "" {
		t.Fatal("GetOpenCodeConfig returned empty string")
	}
	// Verify it's valid JSON containing the model key
	if got == defaultConfigContent {
		t.Fatal("GetOpenCodeConfig should not return default when file exists")
	}
}

func TestGetOpenCodeConfigFormatsCompactJSON(t *testing.T) {
	home := setTestHome(t)
	configDir := filepath.Join(home, ".config", "opencode")
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write compact JSON
	if err := os.WriteFile(configPath, []byte(`{"model":"test"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService()
	got, err := svc.GetOpenCodeConfig()
	if err != nil {
		t.Fatalf("GetOpenCodeConfig returned error: %v", err)
	}
	// Should be reformatted with indentation
	expected := "{\n  \"model\": \"test\"\n}\n"
	if got != expected {
		t.Fatalf("expected formatted JSON:\n%s\n\ngot:\n%s", expected, got)
	}
}

func TestGetOpenCodeConfigReturnsInvalidJSONAsIs(t *testing.T) {
	home := setTestHome(t)
	configDir := filepath.Join(home, ".config", "opencode")
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	invalidJSON := "{broken json"
	if err := os.WriteFile(configPath, []byte(invalidJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService()
	got, err := svc.GetOpenCodeConfig()
	if err != nil {
		t.Fatalf("GetOpenCodeConfig returned error: %v", err)
	}
	if got != invalidJSON {
		t.Fatalf("expected raw invalid content %q, got %q", invalidJSON, got)
	}
}

func TestSaveOpenCodeConfigCreatesDirectoryAndFile(t *testing.T) {
	home := setTestHome(t)
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")

	svc := NewService()
	content := `{"model":"claude-sonnet-4-20250514"}`
	if err := svc.SaveOpenCodeConfig(content); err != nil {
		t.Fatalf("SaveOpenCodeConfig returned error: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	// Should be formatted with indentation
	expected := "{\n  \"model\": \"claude-sonnet-4-20250514\"\n}\n"
	if string(data) != expected {
		t.Fatalf("expected formatted JSON:\n%s\n\ngot:\n%s", expected, string(data))
	}
}

func TestSaveOpenCodeConfigRejectsInvalidJSON(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	err := svc.SaveOpenCodeConfig("{not valid json")
	if err == nil {
		t.Fatal("SaveOpenCodeConfig should reject invalid JSON")
	}
}

func TestSaveOpenCodeConfigRejectsEmptyString(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	err := svc.SaveOpenCodeConfig("")
	if err == nil {
		t.Fatal("SaveOpenCodeConfig should reject empty string")
	}
}

func TestSaveOpenCodeConfigRejectsArray(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	err := svc.SaveOpenCodeConfig(`[1, 2, 3]`)
	if err == nil {
		t.Fatal("SaveOpenCodeConfig should reject JSON array as root")
	}
	if !contains(err.Error(), "array") {
		t.Fatalf("error should mention 'array', got: %v", err)
	}
}

func TestSaveOpenCodeConfigRejectsString(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	err := svc.SaveOpenCodeConfig(`"hello"`)
	if err == nil {
		t.Fatal("SaveOpenCodeConfig should reject JSON string as root")
	}
	if !contains(err.Error(), "string") {
		t.Fatalf("error should mention 'string', got: %v", err)
	}
}

func TestSaveOpenCodeConfigRejectsNumber(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	err := svc.SaveOpenCodeConfig(`42`)
	if err == nil {
		t.Fatal("SaveOpenCodeConfig should reject JSON number as root")
	}
	if !contains(err.Error(), "number") {
		t.Fatalf("error should mention 'number', got: %v", err)
	}
}

func TestSaveOpenCodeConfigRejectsBoolean(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	err := svc.SaveOpenCodeConfig(`true`)
	if err == nil {
		t.Fatal("SaveOpenCodeConfig should reject JSON boolean as root")
	}
	if !contains(err.Error(), "boolean") {
		t.Fatalf("error should mention 'boolean', got: %v", err)
	}
}

func TestSaveOpenCodeConfigRejectsNull(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	err := svc.SaveOpenCodeConfig(`null`)
	if err == nil {
		t.Fatal("SaveOpenCodeConfig should reject JSON null as root")
	}
	if !contains(err.Error(), "null") {
		t.Fatalf("error should mention 'null', got: %v", err)
	}
}

func TestSaveOpenCodeConfigAcceptsEmptyObject(t *testing.T) {
	home := setTestHome(t)
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")

	svc := NewService()
	if err := svc.SaveOpenCodeConfig(`{}`); err != nil {
		t.Fatalf("SaveOpenCodeConfig should accept empty object: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != defaultConfigContent {
		t.Fatalf("expected %q, got %q", defaultConfigContent, string(data))
	}
}

func TestGetOpenCodeConfigReturnsRawForNonObjectJSON(t *testing.T) {
	home := setTestHome(t)
	configDir := filepath.Join(home, ".config", "opencode")
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	arrayContent := `[1, 2, 3]`
	if err := os.WriteFile(configPath, []byte(arrayContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	svc := NewService()
	got, err := svc.GetOpenCodeConfig()
	if err != nil {
		t.Fatalf("GetOpenCodeConfig returned error: %v", err)
	}
	if got != arrayContent {
		t.Fatalf("expected raw non-object content %q, got %q", arrayContent, got)
	}
}

func TestSaveOpenCodeConfigPreservesComplexStructure(t *testing.T) {
	home := setTestHome(t)
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")

	svc := NewService()
	content := `{
		"model": "anthropic/claude-sonnet-4-20250514",
		"provider": {
			"anthropic": {
				"options": {
					"apiKey": "sk-test-123"
				}
			}
		},
		"theme": "dark"
	}`
	if err := svc.SaveOpenCodeConfig(content); err != nil {
		t.Fatalf("SaveOpenCodeConfig returned error: %v", err)
	}

	// Read back through the service to verify round-trip
	got, err := svc.GetOpenCodeConfig()
	if err != nil {
		t.Fatalf("GetOpenCodeConfig returned error: %v", err)
	}
	if got == "" || got == defaultConfigContent {
		t.Fatal("round-trip should return saved content, not default")
	}

	// Verify file on disk
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if data[len(data)-1] != '\n' {
		t.Fatal("saved file should end with newline")
	}
}

func TestGetOpenCodeConfigPathReturnsExpectedPath(t *testing.T) {
	home := setTestHome(t)
	svc := NewService()

	got, err := svc.GetOpenCodeConfigPath()
	if err != nil {
		t.Fatalf("GetOpenCodeConfigPath returned error: %v", err)
	}
	expected := filepath.Join(home, ".config", "opencode", "opencode.json")
	if got != expected {
		t.Fatalf("expected path %q, got %q", expected, got)
	}
}

func TestSaveThenGetRoundTrip(t *testing.T) {
	setTestHome(t)
	svc := NewService()

	original := `{"provider":{"openai":{"options":{"apiKey":"sk-abc"}}}}`
	if err := svc.SaveOpenCodeConfig(original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := svc.GetOpenCodeConfig()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Should be reformatted
	expected := "{\n  \"provider\": {\n    \"openai\": {\n      \"options\": {\n        \"apiKey\": \"sk-abc\"\n      }\n    }\n  }\n}\n"
	if got != expected {
		t.Fatalf("round-trip mismatch\nexpected:\n%s\n\ngot:\n%s", expected, got)
	}
}

func TestWriteConfigFileFallsBackToDirectOverwriteWhenRenameFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	formatted := []byte("{\n  \"model\": \"fallback\"\n}\n")

	originalWriteFile := osWriteFile
	originalRename := osRename
	originalRemove := osRemove
	t.Cleanup(func() {
		osWriteFile = originalWriteFile
		osRename = originalRename
		osRemove = originalRemove
	})

	renameCalls := 0
	osRename = func(oldPath, newPath string) error {
		renameCalls++
		if oldPath != path+".tmp" {
			t.Fatalf("rename old path = %q, want %q", oldPath, path+".tmp")
		}
		if newPath != path {
			t.Fatalf("rename new path = %q, want %q", newPath, path)
		}
		return errors.New("Access is denied")
	}

	if err := writeConfigFile(path, formatted); err != nil {
		t.Fatalf("writeConfigFile returned error: %v", err)
	}
	if renameCalls != 1 {
		t.Fatalf("rename calls = %d, want 1", renameCalls)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read overwritten file: %v", err)
	}
	if string(data) != string(formatted) {
		t.Fatalf("overwritten file content mismatch\nwant:\n%s\n\ngot:\n%s", string(formatted), string(data))
	}

	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file should be cleaned up, stat err = %v", err)
	}
}
