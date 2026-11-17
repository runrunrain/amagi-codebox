package piconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodemodeConfigValidation(t *testing.T) {
	for _, s := range []string{`{}`, `{"codemode":{}}`, `{"codemode":{"preference":"auto"}}`, `{"codemode":{"preference":"prefer","budget":{"maxOutputTokensPerScript":4000,"maxTimeoutMsPerScript":30000}}}`, `{"codemode":{"mode":"off","roles":{"leader":"off"},"rpc":"off","budget":{"maxScriptsPerSession":1}}}`} {
		if err := ValidateAmagiConfig(s); err != nil {
			t.Fatalf("valid: %s %v", s, err)
		}
	}
	for _, s := range []string{`{"codemode":null}`, `{"codemode":{"preference":"always"}}`, `{"codemode":{"budget":{"maxOutputTokensPerScript":0}}}`, `{"codemode":{"budget":{"maxTimeoutMsPerScript":1.5}}}`, `{"codemode":{"preference":"SECRET"}}`} {
		err := ValidateAmagiConfig(s)
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("invalid not safely rejected: %v", err)
		}
	}
}

func TestCodemodeSaveRoundTripAndReject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	service := NewService()
	fixture := `{"agents":{"luban":{"model":"provider/worker:high"}},"advisor":{"model":null},"future":{"kept":true},"codemode":{"preference":"prefer","mode":"off","roles":{"leader":"off"},"rpc":"off","budget":{"maxOutputTokensPerScript":4000,"maxTimeoutMsPerScript":30000,"maxScriptsPerSession":1}}}`
	if err := service.SaveAmagiConfig(fixture); err != nil {
		t.Fatal(err)
	}
	got, err := service.GetAmagiConfig()
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
	if !reflect.DeepEqual(expected, actual) || !strings.Contains(got, "prefer") {
		t.Fatalf("round-trip changed config: %s", got)
	}
	file := filepath.Join(dir, "amagi.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveAmagiConfig(`{"codemode":{"budget":{"maxTimeoutMsPerScript":0}}}`); err == nil || !strings.Contains(err.Error(), "maxTimeoutMsPerScript") {
		t.Fatalf("invalid config error: %v", err)
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(before) {
		t.Fatalf("rejection changed file: %v", err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private mode: %v %v", info, err)
	}
}
