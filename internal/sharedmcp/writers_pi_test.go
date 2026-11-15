package sharedmcp

// writers_pi_test.go — Pi 写入器测试（spec §2.4）：含 2 手写条目夹具 →
// 新增不动手写；PI_CODING_AGENT_DIR 注入路径生效；文件缺失创建；Remove。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"
)

// piFixture 仿真本机 ~/.pi/agent/mcp.json（2 个手写 stdio server，
// spec §0.1）。
const piFixture = "{\r\n  \"mcpServers\": {\r\n    \"tavily-mcp\": {\r\n      \"type\": \"stdio\",\r\n      \"command\": \"npx\",\r\n      \"args\": [\"-y\", \"tavily-mcp\"],\r\n      \"env\": {\r\n        \"TAVILY_API_KEY\": \"SECRET-1\"\r\n      },\r\n      \"description\": \"tavily\"\r\n    },\r\n    \"firecrawl-mcp\": {\r\n      \"type\": \"stdio\",\r\n      \"command\": \"npx\",\r\n      \"args\": [\"-y\", \"firecrawl-mcp\"],\r\n      \"env\": {\r\n        \"FIRECRAWL_API_KEY\": \"SECRET-2\"\r\n      },\r\n      \"description\": \"firecrawl\"\r\n    }\r\n  }\r\n}"

func newPiTestWriter(t *testing.T) (*piWriter, string) {
	t.Helper()
	home := t.TempDir()
	w := &piWriter{env: testEnv(home), userHome: home}
	return w, home
}

func TestPiWriter_AgentDirEnvInjectionAndFixture(t *testing.T) {
	w, home := newPiTestWriter(t)
	wantDir := filepath.Join(home, ".pi", "agent")
	if w.path() != filepath.Join(wantDir, "mcp.json") {
		t.Fatalf("PI_CODING_AGENT_DIR 未生效: %s", w.path())
	}
	if err := os.MkdirAll(wantDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.path(), []byte(piFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(w.path())

	if _, err := w.Apply(stdioEntry("new-one")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after, _ := os.ReadFile(w.path())
	// 手写条目 raw 逐字节保留。
	for _, name := range []string{"tavily-mcp", "firecrawl-mcp"} {
		if gjson.GetBytes(after, "mcpServers."+name).Raw != gjson.GetBytes(before, "mcpServers."+name).Raw {
			t.Fatalf("手写条目 %s 被改动", name)
		}
	}
	if !gjson.GetBytes(after, "mcpServers.new-one").Exists() {
		t.Fatalf("新条目缺失: %s", after)
	}
	// verbatim：description 保留、type/command/env 原样。
	n := gjson.GetBytes(after, "mcpServers.new-one")
	if n.Get("type").String() != "stdio" || n.Get("command").String() != "npx" || n.Get("environment.K").Exists() {
		t.Fatalf("verbatim 映射错误（environment 是 OpenCode 语义）: %s", n.Raw)
	}
	// Remove 新条目 → 手写仍在、顶层键集不变（mcpServers 保留）。
	if err := w.Remove("new-one"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	final, _ := os.ReadFile(w.path())
	if gjson.GetBytes(final, "mcpServers.new-one").Exists() {
		t.Fatal("新条目未删除")
	}
	for _, name := range []string{"tavily-mcp", "firecrawl-mcp"} {
		if !gjson.GetBytes(final, "mcpServers."+name).Exists() {
			t.Fatalf("手写条目 %s 丢失", name)
		}
	}
	if !gjson.GetBytes(final, "mcpServers").Exists() {
		t.Fatal("mcpServers 键被删")
	}
}

func TestPiWriter_MissingFileCreated(t *testing.T) {
	w, _ := newPiTestWriter(t)
	if _, err := w.Apply(httpEntry("first")); err != nil {
		t.Fatalf("Apply on missing: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	if !gjson.ValidBytes(data) || !gjson.GetBytes(data, "mcpServers.first").Exists() {
		t.Fatalf("新建文件形态错误: %s", data)
	}
	raw, err := w.Read("first")
	if err != nil || raw == nil {
		t.Fatalf("Read: %v", raw)
	}
	mapped, _ := w.Mapped(httpEntry("first"))
	if !equalJSONValue(raw, mapped) {
		t.Fatalf("Read 与 Mapped 不一致:\n%s\n%s", raw, mapped)
	}
}
