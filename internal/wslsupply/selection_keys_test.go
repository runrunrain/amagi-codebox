package wslsupply

// selection_keys_test.go — SA-03 回归钉子：S4 键级选择的全路径标识贯通
// （UI 预览 KeyPreview.Key → SetUnitSelection → 适配器选择匹配 → 目标文件
// 内容）。修复前 UI 传 "mcpServers/demo" 而后端按叶名匹配，desired 恒空、
// 零导入仍虚报成功；同名 MCP/profile 叶不能因全局截叶名同时被选。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runFirst 执行一次单单元 RunUnits 并返回首个结果。
func runFirst(t *testing.T, c *Coordinator, cli CLI, unit UnitID) UnitResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), "Ubuntu", cli, ReasonManual, []UnitID{unit})
	if err != nil {
		t.Fatalf("RunUnits(%s): %v", unit, err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %+v", res.Results)
	}
	return res.Results[0]
}

func selectKeys(t *testing.T, c *Coordinator, cli CLI, unit UnitID, keys ...string) {
	t.Helper()
	if err := c.SetUnitSelection("Ubuntu", cli, unit, keys); err != nil {
		t.Fatalf("SetUnitSelection(%v): %v", keys, err)
	}
}

func grantConsentFor(t *testing.T, c *Coordinator, cli CLI, unit UnitID) {
	t.Helper()
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", cli, unit); err != nil {
		t.Fatalf("GrantUnitConsent(%s): %v", unit, err)
	}
}

// TestSelectionFullPathKeysReachTargetFile — UI 传预览全路径键，真实写入目标
// 文件（贯通断言：修复前 outcome=fresh 且目标文件不存在）。
func TestSelectionFullPathKeysReachTargetFile(t *testing.T) {
	c, fp, src, _ := newS4ClaudeCoordinator(t)
	src.claude = []byte(`{"mcpServers":{"demo":{"url":"https://example.invalid/mcp"}}}`)
	grantConsentFor(t, c, CLIClaude, UnitClaudeMCP)
	selectKeys(t, c, CLIClaude, UnitClaudeMCP, "mcpServers/demo")
	res := runFirst(t, c, CLIClaude, UnitClaudeMCP)
	if res.Outcome != UnitApplied {
		t.Fatalf("full-path selection must apply, got %+v", res)
	}
	data, err := os.ReadFile(claudeFile(fp, "~/.claude.json"))
	if err != nil || !strings.Contains(string(data), "example.invalid") {
		t.Fatalf("selected server must land in target file: %q %v", data, err)
	}
}

// TestSelectionCodexFullPathKeys — codex 的 mcp_servers/<n> 与 profiles/<n>
// 全路径键贯通；同名叶（mcp_servers/x 与 profiles/x）选择叶名 "x" 时两者都
// 不导入（双义保守，不猜作用域），全路径可精确选择其一。
func TestSelectionCodexFullPathKeys(t *testing.T) {
	c, fp, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.x]\nurl = \"https://x.example/sse\"\n\n[profiles.x]\nmodel = \"gpt-5-mini\"\n")
	grantConsentFor(t, c, CLICodex, UnitCodexConfig)

	// 叶名 "x" 双义：两个作用域都不选——零导入不虚报（fresh 而非 applied）。
	selectKeys(t, c, CLICodex, UnitCodexConfig, "x")
	res := runFirst(t, c, CLICodex, UnitCodexConfig)
	if res.Outcome != UnitFresh {
		t.Fatalf("ambiguous leaf must select nothing, got %+v", res)
	}
	if _, err := os.Stat(codexFile(fp)); !os.IsNotExist(err) {
		t.Fatal("ambiguous leaf must not write the target file")
	}

	// 全路径精确选择 profiles/x：仅该节落地，mcp_servers/x 不动。
	selectKeys(t, c, CLICodex, UnitCodexConfig, "profiles/x")
	res = runFirst(t, c, CLICodex, UnitCodexConfig)
	if res.Outcome != UnitApplied {
		t.Fatalf("full-path profile selection must apply, got %+v", res)
	}
	data, _ := os.ReadFile(codexFile(fp))
	if !strings.Contains(string(data), "[profiles.x]") {
		t.Fatalf("selected profile must land:\n%s", data)
	}
	if strings.Contains(string(data), "[mcp_servers.x]") {
		t.Fatalf("unselected same-leaf mcp section must not land:\n%s", data)
	}
}

// TestSelectionLegacyUniqueLeafCompat — 历史叶名登记（修复前部分入口）在叶名
// 唯一时保持兼容。
func TestSelectionLegacyUniqueLeafCompat(t *testing.T) {
	c, _, src, _ := newS4ClaudeCoordinator(t)
	src.claude = []byte(`{"mcpServers":{"web":{"url":"https://mcp.example/sse"}}}`)
	grantConsentFor(t, c, CLIClaude, UnitClaudeMCP)
	selectKeys(t, c, CLIClaude, UnitClaudeMCP, "web")
	res := runFirst(t, c, CLIClaude, UnitClaudeMCP)
	if res.Outcome != UnitApplied {
		t.Fatalf("legacy unique leaf must keep working, got %+v", res)
	}
}

// TestSelectionEmptyWithdrawsManagedEntries — 空选择显式撤回：已导入条目按
// 来源删除语义移除（目标仍是上次写入才删；不再虚报导入成功）。
func TestSelectionEmptyWithdrawsManagedEntries(t *testing.T) {
	c, fp, src, _ := newS4ClaudeCoordinator(t)
	src.claude = []byte(`{"mcpServers":{"web":{"url":"https://mcp.example/sse"}}}`)
	grantConsentFor(t, c, CLIClaude, UnitClaudeMCP)
	selectKeys(t, c, CLIClaude, UnitClaudeMCP, "mcpServers/web")
	if res := runFirst(t, c, CLIClaude, UnitClaudeMCP); res.Outcome != UnitApplied {
		t.Fatalf("initial import failed: %+v", res)
	}
	selectKeys(t, c, CLIClaude, UnitClaudeMCP) // 空选择 = 全不选
	res := runFirst(t, c, CLIClaude, UnitClaudeMCP)
	if res.Outcome != UnitApplied {
		t.Fatalf("empty selection must withdraw managed entry (applied), got %+v", res)
	}
	data, _ := os.ReadFile(claudeFile(fp, "~/.claude.json"))
	if strings.Contains(string(data), "mcp.example") {
		t.Fatalf("managed server must be removed on empty selection:\n%s", data)
	}
}

// TestSelectionPreviewReflectsFullKeyState — 预览面 Selected 与后端选择语义一致
//（全路径键选中；同名叶不误标）。
func TestSelectionPreviewReflectsFullKeyState(t *testing.T) {
	c, _, src, _ := newS4CodexCoordinator(t)
	src.toml = []byte("[mcp_servers.x]\nurl = \"https://x.example/sse\"\n\n[profiles.x]\nmodel = \"gpt-5-mini\"\n")
	grantConsentFor(t, c, CLICodex, UnitCodexConfig)
	selectKeys(t, c, CLICodex, UnitCodexConfig, "mcp_servers/x")
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICodex, []UnitID{UnitCodexConfig})
	if err != nil || len(previews) != 1 {
		t.Fatalf("preview: %v %+v", err, previews)
	}
	selected := map[string]bool{}
	for _, k := range previews[0].Keys {
		selected[k.Key] = k.Selected
	}
	if !selected["mcp_servers/x"] {
		t.Fatalf("full-path selected key must be reflected: %+v", previews[0].Keys)
	}
	if selected["profiles/x"] {
		t.Fatalf("unselected same-leaf key must not be marked selected: %+v", previews[0].Keys)
	}
}

// TestSelectionUnknownKeyImportsNothing — 选择集含未知键（既非全路径也非唯一
// 叶）时零导入，RunUnits 返回 fresh（不产生写入）。
func TestSelectionUnknownKeyImportsNothing(t *testing.T) {
	c, fp, src, _ := newS4ClaudeCoordinator(t)
	src.claude = []byte(`{"mcpServers":{"web":{"url":"https://mcp.example/sse"}}}`)
	grantConsentFor(t, c, CLIClaude, UnitClaudeMCP)
	selectKeys(t, c, CLIClaude, UnitClaudeMCP, "mcpServers/ghost")
	res := runFirst(t, c, CLIClaude, UnitClaudeMCP)
	if res.Outcome != UnitFresh {
		t.Fatalf("unknown key selection must be a no-op, got %+v", res)
	}
	if _, err := os.Stat(filepath.Dir(claudeFile(fp, "~/.claude.json"))); err == nil {
		if _, serr := os.Stat(claudeFile(fp, "~/.claude.json")); serr == nil {
			t.Fatal("unknown key must not create the target file")
		}
	}
}

// TestClaudeSettingsHasWorkingEnableEntry（SA-03 启用入口核实）：claude/settings
// 必须接受键级选择（selectLeaf）——否则 SetUnitSelection 拒绝、UI 导入授权
// 路径断裂，单元永远无法启用（awaiting-consent 悬死）。
func TestClaudeSettingsHasWorkingEnableEntry(t *testing.T) {
	c, fp, src, _ := newS4ClaudeCoordinator(t)
	src.settings = []byte(`{"model":"demo","permissions":{"allow":["Bash"]},"env":{"FOO":"bar"}}`)
	grantConsentFor(t, c, CLIClaude, UnitClaudeSettings)
	// 白名单字段（键名即叶名）可勾选导入；permissions/env 不在白名单不可选。
	selectKeys(t, c, CLIClaude, UnitClaudeSettings, "model")
	res := runFirst(t, c, CLIClaude, UnitClaudeSettings)
	if res.Outcome != UnitApplied {
		t.Fatalf("claude/settings must accept key selection and apply, got %+v", res)
	}
	data, err := os.ReadFile(claudeFile(fp, "~/.claude/settings.json"))
	if err != nil || !strings.Contains(string(data), `"model": "demo"`) {
		t.Fatalf("whitelist field must land: %q %v", data, err)
	}
	if strings.Contains(string(data), "permissions") || strings.Contains(string(data), `"env"`) {
		t.Fatalf("non-whitelist fields must never cross:\n%s", data)
	}
	// 空选择撤回。
	selectKeys(t, c, CLIClaude, UnitClaudeSettings)
	res = runFirst(t, c, CLIClaude, UnitClaudeSettings)
	if res.Outcome != UnitApplied {
		t.Fatalf("empty selection must withdraw, got %+v", res)
	}
	data, _ = os.ReadFile(claudeFile(fp, "~/.claude/settings.json"))
	if strings.Contains(string(data), "demo") {
		t.Fatalf("withdrawal must remove managed field:\n%s", data)
	}
}
