package sharedmcp

// writers_codex_test.go — Codex 写入器测试（spec §2.4）：夹具 = 本机
// config.toml 形态仿真（手写 node_repl 含 env_vars 数组 + startup_timeout_sec
// + [.env] 子表 + 段外注释/CRLF）→ Apply 新条目后 node_repl 段及其注释
// 逐字节不变、新段含 [mcp_servers.x] + [mcp_servers.x.env] 且值正确；
// Upsert 再跑值漂移检测；Remove 后手写段原样、新段消失；env_vars 永不出
// 现在我方段；http+headers → Supports=false；受管段出现不支持形态 → 拒绝
// 且文件不动。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexFixture 仿真本机 config.toml（spec §0.1）：手写 node_repl + 非受管段
// + 注释 + CRLF。
const codexFixture = "# Codex 配置\r\nmodel = \"gpt-5\"\r\n\r\n[marketplaces]\r\nzcode = \"official\"\r\n\r\n# 手写 MCP（勿动）\r\n[mcp_servers.node_repl]\r\ncommand = \"node\"\r\nargs = [\"repl.mjs\", \"--stdio\"]\r\nenv_vars = [\"PATH\", \"HOME\", \"USERPROFILE\", \"APPDATA\"]\r\nstartup_timeout_sec = 30\r\n\r\n[mcp_servers.node_repl.env]\r\nOPENAI_API_KEY = \"sk-PLACEHOLDER\"\r\nOTHER_TOKEN = \"v2\"\r\n\r\n# 尾部注释\r\n"

// nodeReplBlock 是手写 node_repl 段的逐字节期望（含段内尾随空行）。
const nodeReplBlock = "[mcp_servers.node_repl]\r\ncommand = \"node\"\r\nargs = [\"repl.mjs\", \"--stdio\"]\r\nenv_vars = [\"PATH\", \"HOME\", \"USERPROFILE\", \"APPDATA\"]\r\nstartup_timeout_sec = 30\r\n\r\n[mcp_servers.node_repl.env]\r\nOPENAI_API_KEY = \"sk-PLACEHOLDER\"\r\nOTHER_TOKEN = \"v2\"\r\n"

func newCodexTestWriter(t *testing.T) (*codexWriter, string) {
	t.Helper()
	home := t.TempDir()
	w := &codexWriter{env: testEnv(home), userHome: home}
	if err := os.MkdirAll(filepath.Dir(w.path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.path(), []byte(codexFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return w, home
}

func TestCodexWriter_AppendPreservesHandwrittenByteForByte(t *testing.T) {
	w, _ := newCodexTestWriter(t)

	if _, err := w.Apply(stdioEntry("vision")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after, _ := os.ReadFile(w.path())
	if !bytes.Contains(after, []byte(nodeReplBlock)) {
		t.Fatalf("手写 node_repl 段逐字节不变失败:\n%s", after)
	}
	if !bytes.Contains(after, []byte("[mcp_servers.vision]")) {
		t.Fatalf("新段缺失:\n%s", after)
	}
	if !bytes.Contains(after, []byte("[mcp_servers.vision.env]")) {
		t.Fatalf("env 子表段缺失:\n%s", after)
	}
	if !strings.Contains(string(after), "K = \"V\"") {
		t.Fatalf("env 字面值未写入:\n%s", after)
	}
	// 我方段永不含 env_vars（名字数组是 shell 透传语义，不写）。
	visIdx := bytes.Index(after, []byte("[mcp_servers.vision]"))
	nextHeader := visIdx + 1
	for nextHeader < len(after) && after[nextHeader-1] != '[' {
		nextHeader++
	}
	ourSection := string(after[visIdx:])
	if strings.Contains(ourSection, "env_vars") {
		t.Fatalf("我方段不应含 env_vars:\n%s", ourSection)
	}
	// SectionValue 值正确（经引擎重解析）。
	got, err := w.Read("vision")
	if err != nil {
		t.Fatal(err)
	}
	mapped, _ := w.Mapped(stdioEntry("vision"))
	if !equalJSONValue(got, mapped) {
		t.Fatalf("值漂移:\n got=%s\nwant=%s", got, mapped)
	}
	// 段外注释仍在。
	if !bytes.Contains(after, []byte("# 尾部注释")) || !bytes.Contains(after, []byte("# Codex 配置")) {
		t.Fatal("段外注释丢失")
	}
}

func TestCodexWriter_UpsertDriftAndRemove(t *testing.T) {
	w, _ := newCodexTestWriter(t)
	if _, err := w.Apply(stdioEntry("vision")); err != nil {
		t.Fatal(err)
	}
	// Upsert 值漂移：改 env 后重新 Apply → Read 与新 Mapped 相等。
	e2 := stdioEntry("vision")
	e2.Env = map[string]string{"K": "V2", "EXTRA": "1"}
	if _, err := w.Apply(e2); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}
	got, _ := w.Read("vision")
	mapped, _ := w.Mapped(e2)
	if !equalJSONValue(got, mapped) {
		t.Fatalf("Upsert 后值漂移:\n got=%s\nwant=%s", got, mapped)
	}
	// Remove：新段消失，手写段原样。
	if err := w.Remove("vision"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	after, _ := os.ReadFile(w.path())
	if bytes.Contains(after, []byte("[mcp_servers.vision")) {
		t.Fatalf("新段未消失:\n%s", after)
	}
	if !bytes.Contains(after, []byte(nodeReplBlock)) {
		t.Fatalf("Remove 后手写段被改动:\n%s", after)
	}
	if err := w.Remove("vision"); err != nil {
		t.Fatalf("重复 Remove 应幂等: %v", err)
	}
}

func TestCodexWriter_SupportsMatrix(t *testing.T) {
	w, _ := newCodexTestWriter(t)
	if ok, _ := w.Supports(stdioEntry("x")); !ok {
		t.Fatal("stdio 应支持")
	}
	h := httpEntry("x")
	if ok, reason := w.Supports(h); ok || !strings.Contains(reason, "Authorization") {
		t.Fatalf("http+headers 应不支持且原因提及鉴权头: ok=%v reason=%s", ok, reason)
	}
	h.Headers = nil
	if ok, reason := w.Supports(h); !ok || reason != "" {
		t.Fatalf("http 无 headers 应支持: ok=%v reason=%s", ok, reason)
	}
	if ok, _ := w.Supports(SharedMcpEntry{Name: "x", Type: "sse"}); ok {
		t.Fatal("未知 type 应不支持")
	}
}

func TestCodexWriter_UnsupportedManagedFormRefusedAndUntouched(t *testing.T) {
	w, _ := newCodexTestWriter(t)
	// 受管作用域出现数组表 → 不支持形态 → 拒绝且文件不动。
	broken := codexFixture + "\r\n[[mcp_servers.bad_array]]\r\ncommand = \"x\"\r\n"
	if err := os.WriteFile(w.path(), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(w.path())
	if _, err := w.Apply(stdioEntry("x")); KindOf(err) != KindTargetCorrupt {
		t.Fatalf("数组表应拒绝: %v", err)
	}
	if _, err := w.Has("x"); KindOf(err) != KindTargetCorrupt {
		t.Fatalf("Has 应拒绝: %v", err)
	}
	if err := w.Remove("x"); KindOf(err) != KindTargetCorrupt {
		t.Fatalf("Remove 应拒绝: %v", err)
	}
	after, _ := os.ReadFile(w.path())
	if !bytes.Equal(before, after) {
		t.Fatal("文件被改动")
	}
	// 恢复干净夹具后：手写段携带 startup_timeout_sec / env_vars（我方段外
	// 键）→ 不参与、不影响解析。
	if err := os.WriteFile(w.path(), []byte(codexFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.Has("node_repl"); !ok {
		t.Fatal("手写段应可见（Has）")
	}
	raw, err := w.Read("node_repl")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"startup_timeout_sec"`)) {
		t.Fatalf("手写段值应含段外键: %s", raw)
	}
}

func TestCodexWriter_MissingFileCreated(t *testing.T) {
	home := t.TempDir()
	w := &codexWriter{env: testEnv(home), userHome: home} // 未写 fixture
	if _, err := w.Apply(httpEntry("remote-only")); err != nil {
		t.Fatalf("Apply on missing: %v", err)
	}
	data, _ := os.ReadFile(w.path())
	if !bytes.Contains(data, []byte("[mcp_servers.remote-only]")) || !bytes.Contains(data, []byte("url = \"https://demo.example.com/mcp\"")) {
		t.Fatalf("新建文件形态错误:\n%s", data)
	}
	// http 无 env → 不生成 env 子表段。
	if bytes.Contains(data, []byte(".env]")) {
		t.Fatalf("http 条目不应有 env 子表:\n%s", data)
	}
}
