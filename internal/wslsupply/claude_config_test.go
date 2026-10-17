package wslsupply

// claude_config_test.go — Claude Code 用户配置导入钉子（P3-S4）：仅用户选择的
// mcpServers 导入（server 级，S2 转换器/冲突框架复用）、settings 白名单字段、
// 负面断言（projects/trust/onboarding/未知键/权限字段绝不跨界——验收 2/5）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/launcher"
)

type stubClaudeConfigSource struct {
	claude   []byte
	settings []byte
	err      bool
}

func (s *stubClaudeConfigSource) ClaudeJSON() ([]byte, error) {
	if s.err {
		return nil, os.ErrPermission
	}
	return s.claude, nil
}
func (s *stubClaudeConfigSource) SettingsJSON() ([]byte, error) {
	if s.err {
		return nil, os.ErrPermission
	}
	return s.settings, nil
}

func newS4ClaudeCoordinator(t *testing.T) (*Coordinator, *fakeProbes, *stubClaudeConfigSource, *stubFactsSource) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	src := &stubClaudeConfigSource{}
	facts := &stubFactsSource{}
	facts.set(TransformFacts{
		ResolvedCommands: map[string]string{"npx": "/usr/bin/npx"},
		PrefixMappings:   []PathPrefixRule{{WindowsPrefix: `C:\Users\u\.claude`, LinuxPrefix: "/home/u/.claude"}},
		HostHomeWindows:  `C:\Users\u`,
	})
	c.RegisterProjection(NewClaudeMCPAdapter(src, facts))
	c.RegisterProjection(NewClaudeSettingsAdapter(src, facts))
	return c, fp, src, facts
}

func claudeFile(fp *fakeProbes, name string) string {
	return filepath.Join(fp.uncRoot, filepath.FromSlash("home/u/"+strings.TrimPrefix(name, "~/")))
}

func writeClaudeFile(t *testing.T, fp *fakeProbes, name, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(claudeFile(fp, name)), 0o700)
	if err := os.WriteFile(claudeFile(fp, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readClaudeFile(t *testing.T, fp *fakeProbes, name string) string {
	t.Helper()
	data, err := os.ReadFile(claudeFile(fp, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runClaudeUnit(t *testing.T, c *Coordinator, unit UnitID) UnitResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonSave, []UnitID{unit})
	if err != nil {
		t.Fatalf("RunUnits(%s): %v", unit, err)
	}
	return res.Results[0]
}

func grantClaudeConsent(t *testing.T, c *Coordinator, units ...UnitID) {
	t.Helper()
	for _, unit := range units {
		if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIClaude, unit); err != nil {
			t.Fatalf("grant consent %s: %v", unit, err)
		}
	}
}

func TestClaudeMCPSelectionGate(t *testing.T) {
	c, fp, src, _ := newS4ClaudeCoordinator(t)
	src.claude = []byte(`{
  "mcpServers": {
    "web": {"url": "https://mcp.example/sse"},
    "fetch": {"command": "npx.cmd", "args": ["-y", "fetch-mcp"]},
    "winonly": {"command": "setup.bat"}
  },
  "projects": {"/home/win/proj": {"allowedTools": ["Bash"]}},
  "hasCompletedOnboarding": true,
  "oauthAccount": {"emailAddress": "user@example.com"}
}`)
	grantClaudeConsent(t, c, UnitClaudeMCP)

	// 无选择：不写任何 server（即使已同意——「仅用户选择的单元导入」）。
	res := runClaudeUnit(t, c, UnitClaudeMCP)
	if res.Outcome != UnitFresh {
		t.Fatalf("no selection must be a no-op, got %+v", res)
	}
	if _, err := os.Stat(claudeFile(fp, "~/.claude.json")); !os.IsNotExist(err) {
		t.Fatal("nothing must be written without selection")
	}

	// 选择 web（远程）：写入且仅写 mcpServers 域。
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	res = runClaudeUnit(t, c, UnitClaudeMCP)
	if res.Outcome != UnitApplied {
		t.Fatalf("selected remote server must apply, got %+v", res)
	}
	got := readClaudeFile(t, fp, "~/.claude.json")
	var doc map[string]any
	if json.Unmarshal([]byte(got), &doc) != nil {
		t.Fatalf("written .claude.json invalid: %s", got)
	}
	servers := doc["mcpServers"].(map[string]any)
	if len(servers) != 1 || servers["web"] == nil {
		t.Fatalf("only the selected server may land: %s", got)
	}
	// 负面断言（验收 2/5）：projects/onboarding/账户状态绝不跨界；未知键保持本地。
	for _, forbidden := range []string{"projects", "hasCompletedOnboarding", "oauthAccount", "fetch", "winonly"} {
		if _, ok := doc[forbidden]; ok {
			t.Fatalf("forbidden key %q must never cross the boundary:\n%s", forbidden, got)
		}
	}

	// 追加选择 fetch（shim 经验证映射为 npx）；winonly 仍因形态被拒。
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"web", "fetch", "winonly"}); err != nil {
		t.Fatal(err)
	}
	res = runClaudeUnit(t, c, UnitClaudeMCP)
	if res.Outcome != UnitApplied && res.Outcome != UnitFresh {
		t.Fatalf("second import must apply, got %+v", res)
	}
	got = readClaudeFile(t, fp, "~/.claude.json")
	if !strings.Contains(got, `"command": "npx"`) {
		t.Fatalf("verified shim must map to native npx:\n%s", got)
	}
	var doc2 map[string]any
	json.Unmarshal([]byte(got), &doc2)
	servers2 := doc2["mcpServers"].(map[string]any)
	if _, ok := servers2["winonly"]; ok {
		t.Fatalf("windows-only server must not be written:\n%s", got)
	}
	if len(servers2) != 2 {
		t.Fatalf("exactly web+fetch expected:\n%s", got)
	}

	// 取消选择：来源删除语义——仍等于上次写入的托管条目被移除。
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	res = runClaudeUnit(t, c, UnitClaudeMCP)
	if res.Outcome != UnitApplied {
		t.Fatalf("deselection must remove the managed entry, got %+v", res)
	}
	got = readClaudeFile(t, fp, "~/.claude.json")
	if strings.Contains(got, "fetch-mcp") {
		t.Fatalf("deselected server must be removed:\n%s", got)
	}
	if !strings.Contains(got, "mcp.example") {
		t.Fatalf("still-selected server must remain:\n%s", got)
	}
}

func TestClaudeMCPRespectsExistingUserFile(t *testing.T) {
	c, fp, src, _ := newS4ClaudeCoordinator(t)
	src.claude = []byte(`{"mcpServers": {"web": {"url": "https://mcp.example/sse"}}}`)
	// distro 侧已有用户自有 .claude.json（含自己的 server 与未知键）。
	writeClaudeFile(t, fp, "~/.claude.json", `{
  "mcpServers": {"own": {"command": "local-tool"}, "web": {"url": "https://user-own.example/sse"}},
  "customUserKey": true
}`)
	grantClaudeConsent(t, c, UnitClaudeMCP)
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	res := runClaudeUnit(t, c, UnitClaudeMCP)
	if res.Outcome != UnitBlocked || res.Detail != "unmanaged-existing" {
		t.Fatalf("same-name server on target must freeze as unmanaged, got %+v", res)
	}
	got := readClaudeFile(t, fp, "~/.claude.json")
	if !strings.Contains(got, "user-own.example") || !strings.Contains(got, "customUserKey") {
		t.Fatalf("user-owned file must be untouched:\n%s", got)
	}
}

func TestClaudeSettingsWhitelist(t *testing.T) {
	c, fp, src, _ := newS4ClaudeCoordinator(t)
	src.settings = []byte(`{
  "model": "claude-opus-4",
  "spinnerTipsEnabled": false,
  "cleanupPeriodDays": 30,
  "permissions": {"allow": ["Bash(*)"]},
  "hooks": {"PreToolUse": [{"hooks": [{"type": "command", "command": "C:\\tools\\hook.ps1"}]}]},
  "env": {"MY_TOOL": "C:\\tools\\bin"},
  "forceLoginMethod": "1",
  "unknownFutureField": {"nested": true}
}`)
	grantClaudeConsent(t, c, UnitClaudeSettings)
	// SA-03：settings 白名单字段同为键级选择单元——显式勾选白名单字段导入。
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeSettings, []string{"model", "spinnerTipsEnabled", "cleanupPeriodDays"}); err != nil {
		t.Fatal(err)
	}
	res := runClaudeUnit(t, c, UnitClaudeSettings)
	if res.Outcome != UnitApplied {
		t.Fatalf("whitelist projection must apply, got %+v", res)
	}
	got := readClaudeFile(t, fp, "~/.claude/settings.json")
	var doc map[string]any
	if json.Unmarshal([]byte(got), &doc) != nil {
		t.Fatalf("written settings invalid: %s", got)
	}
	if doc["model"] != "claude-opus-4" || doc["spinnerTipsEnabled"] != false || doc["cleanupPeriodDays"] != float64(30) {
		t.Fatalf("whitelisted fields missing:\n%s", got)
	}
	// 负面断言（验收 2/5，红线「设置可能授权执行」）：权限规则/hooks/env/未知字段
	// 绝不导入。
	for _, forbidden := range []string{"permissions", "hooks", "env", "forceLoginMethod", "unknownFutureField"} {
		if _, ok := doc[forbidden]; ok {
			t.Fatalf("forbidden settings field %q must never be imported:\n%s", forbidden, got)
		}
	}
}

func TestClaudeKeyedPreviewAndTakeOver(t *testing.T) {
	c, _, src, _ := newS4ClaudeCoordinator(t)
	src.claude = []byte(`{"mcpServers": {"web": {"url": "https://mcp.example/sse"}, "bat": {"command": "run.bat"}}}`)
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLIClaude, []UnitID{UnitClaudeMCP})
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 {
		t.Fatalf("expected 1 preview, got %v", previews)
	}
	var webKey, batKey *KeyPreview
	for i := range previews[0].Keys {
		k := previews[0].Keys[i]
		switch k.Key {
		case "mcpServers/web":
			webKey = &previews[0].Keys[i]
		case "mcpServers/bat":
			batKey = &previews[0].Keys[i]
		}
	}
	if webKey == nil || !webKey.Selected || !webKey.Importable {
		t.Fatalf("selected web key wrong: %+v", webKey)
	}
	if webKey.Before == "" || !strings.Contains(webKey.Before, "远程") {
		t.Fatalf("remote before-summary missing: %+v", webKey)
	}
	if batKey == nil || batKey.Selected {
		t.Fatalf("unselected bat key wrong: %+v", batKey)
	}
	// 选择含不可转换项：预览后导入，bat 保持现状并记因。
	if err := c.SetUnitSelection("Ubuntu", CLIClaude, UnitClaudeMCP, []string{"web", "bat"}); err != nil {
		t.Fatal(err)
	}
	grantClaudeConsent(t, c, UnitClaudeMCP)
	res := runClaudeUnit(t, c, UnitClaudeMCP)
	if res.Outcome != UnitApplied || !strings.Contains(res.Detail, "partial") {
		t.Fatalf("partial write expected (bat blocked), got %+v", res)
	}
	if !strings.Contains(res.Detail, "run.bat") && !strings.Contains(res.Detail, "手动适配") {
		t.Fatalf("blocked conversion reason missing: %s", res.Detail)
	}
}
