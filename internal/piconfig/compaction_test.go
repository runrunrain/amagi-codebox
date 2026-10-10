package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompactionSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()
	for _, field := range []string{"", `,"compaction":{}`, `,"compaction":{"model":null}`,
		`,"compaction":{"model":"amagi-glm/glm-5.3:max"}`, `,"compaction":{"model":"ollama/qwen:32b"}`,
		`,"compaction":{"model":"ollama/qwen:32b:high"}`, `,"compaction":{"model":"dynamic/vendor/model:preview"}`} {
		fixture := `{"agents":{"luban":{"model":"amagi-glm/glm-5.3:max"}},"mcp":{"leader":[]},"future":{"enabled":true}` + field + `}`
		if err := s.SaveAmagiConfig(fixture); err != nil {
			t.Fatalf("save fixture %s: %v", field, err)
		}
		got, err := s.GetAmagiConfig()
		if err != nil {
			t.Fatal(err)
		}
		var expected, actual map[string]any
		if err := json.Unmarshal([]byte(fixture), &expected); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(got), &actual); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(expected, actual) || !strings.Contains(got, "amagi-glm/glm-5.3:max") {
			t.Fatalf("round-trip changed config: %s", got)
		}
		info, err := os.Stat(filepath.Join(dir, "amagi.json"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private config mode: %v, %v", info, err)
		}
	}
}

func TestCompactionRejectPreservesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	s := NewService()
	if err := s.SaveAmagiConfig(`{"compaction":{"model":"p/m"},"agents":{"luban":{"model":"p/work:max"}}}`); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "amagi.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`null`, `[]`, `"oops"`, `{"model":3}`, `{"model":""}`,
		`{"model":"p/"}`, `{"model":"p/:high"}`, `{"model":"/m"}`, `{"model":"p/m high"}`,
		`{"model":" p/m"}`, `{"model":"p/m\uFEFF"}`, `{"model":"p/m","apiKey":"secret-fixture"}`} {
		err := s.SaveAmagiConfig(`{"compaction":` + value + `}`)
		if err == nil || !strings.Contains(err.Error(), "compaction") || strings.Contains(err.Error(), "secret-fixture") {
			t.Fatalf("expected safe validation error for %s, got %v", value, err)
		}
		after, readErr := os.ReadFile(filepath.Join(dir, "amagi.json"))
		if readErr != nil || string(after) != string(before) {
			t.Fatalf("rejection modified config: %v", readErr)
		}
	}
	// Invalid on-disk content stays available to the JSON editor for repair.
	invalid := `{"compaction":{"model":42},"agents":{"luban":{"model":"preserved"}}}`
	if err := os.WriteFile(filepath.Join(dir, "amagi.json"), []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAmagiConfig()
	if err != nil || !strings.Contains(got, `"model": 42`) || !strings.Contains(got, "preserved") {
		t.Fatalf("invalid config was lost: %s, %v", got, err)
	}
}
