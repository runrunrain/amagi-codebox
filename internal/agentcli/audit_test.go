package agentcli

// audit_test.go — 审计 JSONL：非 read 命令逐条落盘、只记参数名绝不记参数值
//（Secret 与普通参数一律脱敏）、read 命令不审计、panic 命令记 internal。

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

func auditLines(t *testing.T, configDir string) []map[string]any {
	t.Helper()
	path := filepath.Join(configDir, "logs", auditFileName)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("审计行不是合法 JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestAuditRecordsMutateButNotRead(t *testing.T) {
	s, ep := startTestServer(t)

	// read 命令：不审计。
	if _, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.echo",
		spec.Request{Args: map[string]any{"name": "n1"}}); !env.OK {
		t.Fatal("echo 应成功")
	}
	// read 错误（demo.fail）也不审计。
	_, _ = call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.fail",
		spec.Request{Args: map[string]any{"code": "not_found"}})

	// mutate 命令：审计一行。
	const secretValue = "sk-live-DO-NOT-LEAK-9f8e7d"
	if _, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.save",
		spec.Request{Args: map[string]any{"name": "my-provider", "apiKey": secretValue}}); !env.OK {
		t.Fatal("save 应成功")
	}
	// exec 命令（confirm 前置错误也应审计：mutate 级以上无论成败都记）。
	_, _ = call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.launch",
		spec.Request{Args: map[string]any{"appType": "claudecode"}}) // 缺 confirm → 409
	// exec 命令 panic：审计 code=internal。
	_, _ = call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.boom",
		spec.Request{Confirm: "BOOM"})

	lines := auditLines(t, s.configDir)
	if len(lines) != 3 {
		t.Fatalf("审计行数=%d, want 3（save + launch 409 + boom 500）", len(lines))
	}

	byCmd := map[string]map[string]any{}
	for _, l := range lines {
		byCmd[l["command"].(string)] = l
	}
	save := byCmd["demo.save"]
	if save == nil || save["risk"] != string(spec.RiskMutate) || save["code"] != "ok" {
		t.Fatalf("save 审计行=%v", save)
	}
	launch := byCmd["demo.launch"]
	if launch == nil || launch["code"] != spec.ErrConfirmationRequired {
		t.Fatalf("launch 审计行=%v（409 也应记）", launch)
	}
	boom := byCmd["demo.boom"]
	if boom == nil || boom["code"] != spec.ErrInternal {
		t.Fatalf("boom 审计行=%v", boom)
	}

	// 参数名记录（排序），参数值绝不出现。
	names, _ := save["paramNames"].([]any)
	namesStr := make([]string, 0, len(names))
	for _, n := range names {
		namesStr = append(namesStr, n.(string))
	}
	if strings.Join(namesStr, ",") != "apiKey,name" {
		t.Fatalf("paramNames=%v, want [apiKey name]", namesStr)
	}

	// 全文件脱敏：任何审计行不得含参数值（含 Secret 值）。
	path := filepath.Join(s.configDir, "logs", auditFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secretValue) {
		t.Fatal("审计文件泄漏了参数值（Secret 参数必须脱敏）")
	}
	if strings.Contains(string(b), "my-provider") {
		t.Fatal("审计文件不应包含任何参数值（连普通参数值也不记）")
	}
}

func TestAuditFilePermissions(t *testing.T) {
	s, ep := startTestServer(t)
	if _, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.save",
		spec.Request{Args: map[string]any{"name": "x"}}); !env.OK {
		t.Fatal("save 应成功")
	}
	path := filepath.Join(s.configDir, "logs", auditFileName)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("审计文件权限=%o, want 600", perm)
	}
	dirSt, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirSt.Mode().Perm(); perm != 0o700 {
		t.Fatalf("logs 目录权限=%o, want 700", perm)
	}
	_ = s
}
