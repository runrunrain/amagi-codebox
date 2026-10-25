package piconfig

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAdvisorConfigValidation(t *testing.T) {
	for _, good := range []string{`{"advisor":{"model":"openai-codex/strong:high","roles":["luban"],"leader":true,"maxCalls":5}}`, `{"advisor":{"model":null}}`, `{"advisor":{"backend":"codex-chat","codex":{"mode":"managed"}}}`, `{"advisor":{"backend":"codex-chat","codex":{"socketPath":"/tmp/desktop.sock","threadId":"test-thread-123"},"timeoutMs":1000}}`} {
		if err := ValidateAmagiConfig(good); err != nil {
			t.Fatalf("valid advisor: %v", err)
		}
	}
	for _, bad := range []string{`{"advisor":{"model":"plain"}}`, `{"advisor":{"model":"a/b","maxCalls":0}}`, `{"advisor":{"model":"a/b","roles":[true]}}`, `{"advisor":{"backend":"other"}}`, `{"advisor":{"backend":"codex-chat","codex":{"socketPath":"relative","threadId":"test-thread-123"}}}`, `{"advisor":{"backend":"codex-chat","codex":{"socketPath":"/tmp/desktop.sock","threadId":""}}}`, `{"advisor":{"model":"a/b","secret":"PRIVATE"}}`} {
		err := ValidateAmagiConfig(bad)
		if err == nil {
			t.Fatal("invalid advisor accepted")
		}
		if strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("config value leaked")
		}
	}
}
func TestCodexAdvisorMissingConnection(t *testing.T) {
	_, err := new(Service).ListCodexAdvisorChats("")
	if err == nil || !strings.Contains(err.Error(), "socket") {
		t.Fatalf("missing socket: %v", err)
	}
}

func TestAdvisorProbeHappyPath(t *testing.T) {
	original := codexAdvisorCommand
	t.Cleanup(func() { codexAdvisorCommand = original })
	codexAdvisorCommand = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestAdvisorProbeHelper")
		cmd.Env = append(os.Environ(), "CODEBOX_ADVISOR_HELPER=1")
		return cmd
	}
	result, err := new(Service).ListCodexAdvisorChats("/tmp/mock-desktop.sock")
	if err != nil || !strings.Contains(result, "CONSULTATION_CHAT") {
		t.Fatalf("read-only happy path: %q %v", result, err)
	}
}
func TestAdvisorProbeHelper(t *testing.T) {
	if os.Getenv("CODEBOX_ADVISOR_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(2)
		}
		switch m.Method {
		case "initialize":
			fmtPrint := json.NewEncoder(os.Stdout)
			_ = fmtPrint.Encode(map[string]any{"id": m.ID, "result": map[string]any{}})
		case "initialized":
		case "thread/list":
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": m.ID, "result": map[string]any{"data": []any{map[string]any{"id": "mock-thread-123", "name": "CONSULTATION_CHAT"}}}})
			os.Exit(0)
		default:
			os.Exit(3)
		}
	}
	os.Exit(4)
}
