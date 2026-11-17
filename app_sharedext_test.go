package main

// app_sharedext_test.go — Shared Extensions（共用中心）App 接线的单元测试：
// provider 组装纯函数映射（claude/codex/opencode/pi 四引擎）、
// ImportSharedMCPServers 的 Wails 单值信封、ReadTextFile 的 1MB 上限。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/codexplugin"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/ompplugin"
	"amagi-codebox/internal/opencodeplugin"
	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/sharedext"
)

func TestClaudePluginSkillsProvider(t *testing.T) {
	installed := plugin.InstalledPlugin{ID: "cc-demo", Name: "Demo"}
	detail := &plugin.PluginDetail{
		Skills: []plugin.SkillInfo{
			{Name: "alpha", Description: "A skill"},
			{Name: "beta", Description: "B skill"},
		},
		SubItems: []plugin.SubItem{
			{Type: plugin.SubItemTypeSkill, Name: "alpha", Enabled: true},
			{Type: plugin.SubItemTypeSkill, Name: "beta", Enabled: false}, // 用户级禁用
			{Type: plugin.SubItemTypeAgent, Name: "beta", Enabled: false}, // 非 skill 子项不进禁用表
		},
	}

	p := claudePluginSkillsProvider(installed, detail)
	if p.Engine != sharedext.EngineClaude {
		t.Fatalf("engine = %q, want claude", p.Engine)
	}
	if p.PluginID != "cc-demo" || p.PluginName != "Demo" {
		t.Fatalf("plugin id/name = %q/%q", p.PluginID, p.PluginName)
	}
	if len(p.Skills) != 2 || p.Skills[0].Name != "alpha" || p.Skills[0].Description != "A skill" {
		t.Fatalf("skills mapping wrong: %+v", p.Skills)
	}
	if !p.SubItemsDisabled["skill/beta"] {
		t.Fatalf("disabled skill/beta missing: %+v", p.SubItemsDisabled)
	}
	if _, present := p.SubItemsDisabled["skill/alpha"]; present {
		t.Fatalf("enabled skill/alpha should not be disabled: %+v", p.SubItemsDisabled)
	}
	if len(p.SubItemsDisabled) != 1 {
		t.Fatalf("agent subitem leaked into disabled map: %+v", p.SubItemsDisabled)
	}
}

func TestCodexPluginSkillsProvider(t *testing.T) {
	// Enabled=true：无任何禁用（子项级机制不存在，见 P4 报告）→ 禁用表为空。
	installed := codexplugin.CodexPlugin{ID: "cx-demo", Name: "CxDemo", Enabled: true}
	detail := &codexplugin.CodexPluginDetail{
		Skills: []codexplugin.SkillInfo{{Name: "gamma", Description: "G"}},
	}

	p := codexPluginSkillsProvider(installed, detail)
	if p.Engine != sharedext.EngineCodex {
		t.Fatalf("engine = %q, want codex", p.Engine)
	}
	if p.PluginID != "cx-demo" || p.PluginName != "CxDemo" {
		t.Fatalf("plugin id/name = %q/%q", p.PluginID, p.PluginName)
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "gamma" || p.Skills[0].Description != "G" {
		t.Fatalf("skills mapping wrong: %+v", p.Skills)
	}
	if len(p.SubItemsDisabled) != 0 {
		t.Fatalf("codex subitem disable not supported yet, map must be empty: %+v", p.SubItemsDisabled)
	}
}

func TestOpenCodePluginSkillsProvider(t *testing.T) {
	installed := opencodeplugin.Plugin{ID: "oc-demo", Spec: "oc-demo", Name: "OcDemo"}
	detail := &opencodeplugin.PluginDetail{
		Skills: []opencodeplugin.ResourceInfo{
			{Name: "delta", FilePath: "/x/skills/delta/SKILL.md"},
		},
	}

	p := openCodePluginSkillsProvider(installed, detail)
	if p.Engine != sharedext.EngineOpenCode {
		t.Fatalf("engine = %q, want opencode", p.Engine)
	}
	if p.PluginID != "oc-demo" || p.PluginName != "OcDemo" {
		t.Fatalf("plugin id/name = %q/%q", p.PluginID, p.PluginName)
	}
	// ResourceInfo 无描述字段，仅映射 Name。
	if len(p.Skills) != 1 || p.Skills[0].Name != "delta" || p.Skills[0].Description != "" {
		t.Fatalf("skills mapping wrong: %+v", p.Skills)
	}
}

func TestPiPackageSkillsProvider(t *testing.T) {
	pkg := piplugin.Package{ID: "npm:@foo/bar", Source: "npm:@foo/bar", Name: "@foo/bar"}
	detail := &piplugin.PackageDetail{
		Resources: []piplugin.ResourceInfo{
			{Name: "eps", FilePath: "/x/skills/eps/SKILL.md", Type: "skill"},
			{Name: "main.ts", FilePath: "/x/ext/main.ts", Type: "extension"},
			{Name: "prompt1", FilePath: "/x/prompts/prompt1.md", Type: "prompt"},
		},
	}

	p := piPackageSkillsProvider(pkg, detail)
	if p.Engine != sharedext.EnginePi {
		t.Fatalf("engine = %q, want pi", p.Engine)
	}
	if p.PluginID != "npm:@foo/bar" || p.PluginName != "@foo/bar" {
		t.Fatalf("plugin id/name = %q/%q", p.PluginID, p.PluginName)
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "eps" {
		t.Fatalf("only type=skill resources should map: %+v", p.Skills)
	}
}

func newSharedExtTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	return &App{
		Log:       logging.NewService(dir),
		sharedExt: sharedext.NewService(dir, nil),
	}
}

func TestAppImportSharedMCPServersEnvelope(t *testing.T) {
	a := newSharedExtTestApp(t)

	first, err := a.ImportSharedMCPServers([]sharedext.SharedMCPServer{
		{Name: "demo", Config: map[string]any{"command": "npx"}},
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if len(first.Imported) != 1 || first.Imported[0].Name != "demo" {
		t.Fatalf("imported = %+v", first.Imported)
	}
	if len(first.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want empty", first.Skipped)
	}

	// 同名再导入：进 skipped，属于部分成功，不返回 error。
	second, err := a.ImportSharedMCPServers([]sharedext.SharedMCPServer{
		{Name: "demo", Config: map[string]any{"command": "npx"}},
		{Name: "fresh", Config: map[string]any{"command": "node"}},
	})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(second.Imported) != 1 || second.Imported[0].Name != "fresh" {
		t.Fatalf("imported = %+v", second.Imported)
	}
	if len(second.Skipped) != 1 || !strings.Contains(second.Skipped[0], "demo") {
		t.Fatalf("skipped = %+v, want demo conflict entry", second.Skipped)
	}

	servers := a.GetSharedMCPServers()
	if len(servers) != 2 {
		t.Fatalf("GetSharedMCPServers len = %d, want 2", len(servers))
	}
}

func TestAppGetSharedSkillsEmpty(t *testing.T) {
	a := newSharedExtTestApp(t)
	if skills := a.GetSharedSkills(); len(skills) != 0 {
		t.Fatalf("empty repo should return empty list, got %+v", skills)
	}
}

func TestAppReadTextFileLimits(t *testing.T) {
	a := newSharedExtTestApp(t) // ReadTextFile 不触 Log/sharedExt，仅需方法承载

	if _, err := a.ReadTextFile(""); err == nil {
		t.Fatal("empty path must error")
	}

	dir := t.TempDir()

	// 正常读取
	okPath := filepath.Join(dir, "ok.json")
	if err := os.WriteFile(okPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := a.ReadTextFile(okPath)
	if err != nil || text != `{"mcpServers":{}}` {
		t.Fatalf("read ok.json: text=%q err=%v", text, err)
	}

	// 超过 1MB 上限拒绝（+1 字节即超）
	bigPath := filepath.Join(dir, "big.json")
	if err := os.WriteFile(bigPath, make([]byte, readTextFileMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadTextFile(bigPath); err == nil || !strings.Contains(err.Error(), "1MB") {
		t.Fatalf("oversize file must error with 1MB hint, got %v", err)
	}

	// 目录拒绝
	if _, err := a.ReadTextFile(dir); err == nil {
		t.Fatal("directory path must error")
	}
}

// ============================================================================
// P4：provider 的插件级/子项级禁用映射
// ============================================================================

func TestCodexPluginSkillsProviderDisabledPluginMarksAllSkills(t *testing.T) {
	installed := codexplugin.CodexPlugin{ID: "cx-off", Name: "CxOff", Enabled: false}
	detail := &codexplugin.CodexPluginDetail{
		Skills: []codexplugin.SkillInfo{{Name: "one"}, {Name: "two"}},
	}

	p := codexPluginSkillsProvider(installed, detail)
	if !p.SubItemsDisabled["skill/one"] || !p.SubItemsDisabled["skill/two"] {
		t.Fatalf("disabled plugin should mark all skills disabled: %+v", p.SubItemsDisabled)
	}
	if len(p.SubItemsDisabled) != 2 {
		t.Fatalf("unexpected disabled map size: %+v", p.SubItemsDisabled)
	}
}

func TestPiPackageSkillsProviderMarksSkillAndPackageDisabled(t *testing.T) {
	// 子项级：ResourceInfo.Disabled（skills[] 过滤语义评估结果）。
	pkg := piplugin.Package{ID: "npm:p", Source: "npm:p", Name: "p", Enabled: true}
	detail := &piplugin.PackageDetail{
		Resources: []piplugin.ResourceInfo{
			{Name: "off", FilePath: "/x/skills/off/SKILL.md", Type: "skill", Disabled: true},
			{Name: "on", FilePath: "/x/skills/on/SKILL.md", Type: "skill"},
		},
	}
	p := piPackageSkillsProvider(pkg, detail)
	if !p.SubItemsDisabled["skill/off"] || p.SubItemsDisabled["skill/on"] {
		t.Fatalf("skill-level disable mapping wrong: %+v", p.SubItemsDisabled)
	}

	// 包级：Enabled=false（!**×4 标记）→ 全部 skill 标记停用。
	pkg.Enabled = false
	p = piPackageSkillsProvider(pkg, detail)
	if !p.SubItemsDisabled["skill/off"] || !p.SubItemsDisabled["skill/on"] {
		t.Fatalf("package-level disable should mark all skills: %+v", p.SubItemsDisabled)
	}
}

func TestOmpPluginSkillsProvider(t *testing.T) {
	installed := ompplugin.Plugin{ID: "omp-demo", Name: "omp-demo", Enabled: true}
	detail := &ompplugin.PluginDetail{
		Skills: []ompplugin.ResourceInfo{
			{Name: "alpha", FilePath: "/x/skills/alpha/SKILL.md"},
		},
	}

	p := ompPluginSkillsProvider(installed, detail)
	if p.Engine != sharedext.EngineOmp {
		t.Fatalf("engine = %q, want omp", p.Engine)
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "alpha" || p.Skills[0].Description != "" {
		t.Fatalf("skills mapping wrong: %+v", p.Skills)
	}
	if len(p.SubItemsDisabled) != 0 {
		t.Fatalf("enabled plugin must not disable skills: %+v", p.SubItemsDisabled)
	}

	installed.Enabled = false
	p = ompPluginSkillsProvider(installed, detail)
	if !p.SubItemsDisabled["skill/alpha"] {
		t.Fatalf("disabled plugin should mark all skills: %+v", p.SubItemsDisabled)
	}
}

// ============================================================================
// P5-Fix M3：App 层 skill 导入单值信封（部分成功不退化为失败）
// ============================================================================

func TestAppImportSharedSkillEnvelope(t *testing.T) {
	a := newSharedExtTestApp(t)

	// 准备导入源：两个 skill 目录（alpha + beta）。
	src := t.TempDir()
	alphaMD := "---\nname: alpha\ndescription: A\n---\n\nalpha 正文\n"
	betaMD := "---\nname: beta\ndescription: B\n---\n\nbeta 正文\n"
	for name, md := range map[string]string{"alpha": alphaMD, "beta": betaMD} {
		dir := filepath.Join(src, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 全部成功：信封 Imported=2、Skipped 空、无 error。
	first, err := a.ImportSharedSkillFromDir(src)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if len(first.Imported) != 2 || len(first.Skipped) != 0 {
		t.Fatalf("envelope = %+v", first)
	}

	// 部分成功（beta 同名跳过 + gamma 新导入）：不返回 error，Skipped 透出明细。
	gammaDir := filepath.Join(src, "gamma", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(gammaDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gammaDir, []byte("---\nname: gamma\ndescription: G\n---\n\ngamma 正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := a.ImportSharedSkillFromDir(src)
	if err != nil {
		t.Fatalf("部分成功不应报错: %v", err)
	}
	if len(second.Imported) != 1 || second.Imported[0].Name != "gamma" {
		t.Fatalf("imported = %+v", second.Imported)
	}
	if len(second.Skipped) != 1 || !strings.Contains(second.Skipped[0], "beta") {
		t.Fatalf("skipped = %+v, want beta 同名跳过明细", second.Skipped)
	}

	// 全失败（目录无 SKILL.md）：error 原样透传。
	empty := t.TempDir()
	if _, err := a.ImportSharedSkillFromDir(empty); err == nil {
		t.Fatal("全失败应返回 error")
	}
	if _, err := a.ImportSharedSkillFromZip(filepath.Join(empty, "absent.zip")); err == nil {
		t.Fatal("zip 打不开应返回 error")
	}

	// 落库实况：alpha/beta/gamma 三条入库。
	skills := a.GetSharedSkills()
	if len(skills) != 3 {
		t.Fatalf("GetSharedSkills len = %d, want 3", len(skills))
	}
}
