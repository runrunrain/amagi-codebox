package wslsupply

// resources_test.go — S4 用户资源目录单元钉子：CLAUDE.md（单文件）与
// commands/<name>（目录）的扫描 → 同意 → 供给 → 冻结 → 接管生命周期，与
// ParseResourceUnit 校验、不兼容资源阻断。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/launcher"
)

func newS4ResourceCoordinator(t *testing.T) (*Coordinator, *fakeProbes, string) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	hostRoot := t.TempDir()
	c.RegisterResources(NewResourceScope(CLIClaude, ResourceLayout{
		HostRoot:     hostRoot,
		Files:        []string{"CLAUDE.md"},
		DirUnits:     []string{"commands"},
		TargetPrefix: ".claude/",
	}, DefaultSkillsScanLimits()))
	return c, fp, hostRoot
}

func writeHostResource(t *testing.T, hostRoot, rel, content string) {
	t.Helper()
	path := filepath.Join(hostRoot, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func claudeTargetPath(fp *fakeProbes, rel string) string {
	return filepath.Join(fp.uncRoot, filepath.FromSlash("home/u/.claude/"+rel))
}

func TestResourceUnitParsing(t *testing.T) {
	for _, tc := range []struct {
		id  UnitID
		ok  bool
		cli CLI
		rel string
	}{
		{UnitID("claude/res/CLAUDE.md"), true, CLIClaude, "CLAUDE.md"},
		{UnitID("claude/res/commands/foo"), true, CLIClaude, "commands/foo"},
		{UnitID("codex/res/AGENTS.md"), true, CLICodex, "AGENTS.md"},
		{UnitID("pi/res/CLAUDE.md"), false, "", ""},  // 未开放 CLI
		{UnitID("claude/res/"), false, "", ""},       // 空 rel
		{UnitID("claude/res/a/../b"), false, "", ""}, // 穿越
		{UnitID("claude/res/a\\b"), false, "", ""},   // 反斜杠
		{UnitID("claude/res//x"), false, "", ""},     // 空段
		{UnitID("shared/skill/foo"), false, "", ""},  // 技能前缀不解析为资源
	} {
		cli, rel, ok := ParseResourceUnit(tc.id)
		if ok != tc.ok || (ok && (cli != tc.cli || rel != tc.rel)) {
			t.Fatalf("ParseResourceUnit(%q) = %q,%q,%v want %q,%q,%v", tc.id, cli, rel, ok, tc.cli, tc.rel, tc.ok)
		}
	}
}

func TestResourceFileUnitLifecycle(t *testing.T) {
	c, fp, hostRoot := newS4ResourceCoordinator(t)
	writeHostResource(t, hostRoot, "CLAUDE.md", "# 全局指引\n使用中文回复。\n")
	c.resourceScope(CLIClaude).Scan()
	unit := ResourceUnitID(CLIClaude, "CLAUDE.md")

	// 未同意：awaiting-consent，不写。
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "awaiting-consent" {
		t.Fatalf("unconsented resource must block, got %+v", res.Results[0])
	}
	if _, err := os.Stat(claudeTargetPath(fp, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("nothing written before consent")
	}

	// 同意 → 供给（目标缺失 = 首次创建）。
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIClaude, unit); err != nil {
		t.Fatal(err)
	}
	res, _ = c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit})
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("consented supply must apply, got %+v", res.Results[0])
	}
	got, _ := os.ReadFile(claudeTargetPath(fp, "CLAUDE.md"))
	if !strings.Contains(string(got), "全局指引") {
		t.Fatalf("CLAUDE.md content missing: %s", got)
	}

	// 无变化 → fresh；来源变化 → 更新。
	res, _ = c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit})
	if res.Results[0].Outcome != UnitFresh {
		t.Fatalf("unchanged must be fresh, got %+v", res.Results[0])
	}
	writeHostResource(t, hostRoot, "CLAUDE.md", "# 全局指引 v2\n")
	c.resourceScope(CLIClaude).Scan()
	res, _ = c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit})
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("source change must update, got %+v", res.Results[0])
	}
	got, _ = os.ReadFile(claudeTargetPath(fp, "CLAUDE.md"))
	if !strings.Contains(string(got), "v2") {
		t.Fatalf("update missing: %s", got)
	}

	// WSL 手改 → 冻结（保留本地）。
	os.WriteFile(claudeTargetPath(fp, "CLAUDE.md"), []byte("# WSL 本地版\n"), 0o644)
	writeHostResource(t, hostRoot, "CLAUDE.md", "# 全局指引 v3\n")
	c.resourceScope(CLIClaude).Scan()
	res, _ = c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit})
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "both-changed-frozen" {
		t.Fatalf("hand edit must freeze, got %+v", res.Results[0])
	}
	got, _ = os.ReadFile(claudeTargetPath(fp, "CLAUDE.md"))
	if !strings.Contains(string(got), "WSL 本地版") {
		t.Fatalf("hand edit must be preserved: %s", got)
	}

	// 预览后接管：CAS hash 匹配 → 来源覆写。
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLIClaude, []UnitID{unit})
	if err != nil || len(previews) != 1 {
		t.Fatalf("preview: %v %v", previews, err)
	}
	takeRes, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLIClaude, unit, previews[0].TargetHash)
	if err != nil {
		t.Fatal(err)
	}
	if takeRes.Outcome != UnitApplied {
		t.Fatalf("takeover must apply, got %+v", takeRes)
	}
	got, _ = os.ReadFile(claudeTargetPath(fp, "CLAUDE.md"))
	if !strings.Contains(string(got), "v3") {
		t.Fatalf("takeover must write source: %s", got)
	}
}

func TestResourceDirUnitLifecycle(t *testing.T) {
	c, fp, hostRoot := newS4ResourceCoordinator(t)
	writeHostResource(t, hostRoot, "commands/deploy/README.md", "# deploy\n")
	writeHostResource(t, hostRoot, "commands/deploy/run.md", "---\ndescription: deploy\n---\nbody\n")
	c.resourceScope(CLIClaude).Scan()
	unit := ResourceUnitID(CLIClaude, "commands/deploy")

	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIClaude, unit); err != nil {
		t.Fatal(err)
	}
	res, _ := c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit})
	if res.Results[0].Outcome != UnitApplied {
		t.Fatalf("dir unit supply must apply, got %+v", res.Results[0])
	}
	if _, err := os.Stat(claudeTargetPath(fp, "commands/deploy/run.md")); err != nil {
		t.Fatalf("deploy/run.md missing: %v", err)
	}

	// 未托管既有目录：同 scope 下另一目录若目标已存在 → unmanaged-existing。
	writeHostResource(t, hostRoot, "commands/existing/keep.md", "x")
	c.resourceScope(CLIClaude).Scan()
	os.MkdirAll(claudeTargetPath(fp, "commands/existing"), 0o755)
	os.WriteFile(claudeTargetPath(fp, "commands/existing/keep.md"), []byte("user own"), 0o644)
	unit2 := ResourceUnitID(CLIClaude, "commands/existing")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIClaude, unit2); err != nil {
		t.Fatal(err)
	}
	res, _ = c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit2})
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "unmanaged-existing" {
		t.Fatalf("existing target dir must freeze unmanaged, got %+v", res.Results[0])
	}
	got, _ := os.ReadFile(claudeTargetPath(fp, "commands/existing/keep.md"))
	if string(got) != "user own" {
		t.Fatalf("user dir must be untouched: %s", got)
	}
}

func TestResourceIncompatibleBlocksSupply(t *testing.T) {
	c, _, hostRoot := newS4ResourceCoordinator(t)
	writeHostResource(t, hostRoot, "commands/winps/build.md", "doc\n")
	writeHostResource(t, hostRoot, "commands/winps/run.ps1", "Write-Host hi\n")
	c.resourceScope(CLIClaude).Scan()
	unit := ResourceUnitID(CLIClaude, "commands/winps")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIClaude, unit); err != nil {
		t.Fatal(err)
	}
	res, _ := c.RunUnits(context.Background(), "Ubuntu", CLIClaude, ReasonManual, []UnitID{unit})
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "incompatible-items" {
		t.Fatalf("windows script resource must be incompatible, got %+v", res.Results[0])
	}
}

// TestResourceClaudeLeafMarkdownUnits（SA-10）：Claude commands/agents 的顶层
// Markdown 文件（review.md/helper.md——真实消费者发现形态）进入资源清单并
// 可供给到消费者路径；一级子目录（嵌套名 frontend/component.md）仍为目录
// 单元；codex skills 不虚构顶层文件单元语义。
func TestResourceClaudeLeafMarkdownUnits(t *testing.T) {
	c, fp, hostRoot := newS4ResourceCoordinator(t)
	// 布局对齐生产（commands/agents 声明顶层 md 文件单元；skills 目录单元）。
	c.RegisterResources(NewResourceScope(CLIClaude, ResourceLayout{
		HostRoot:     hostRoot,
		Files:        []string{"CLAUDE.md"},
		DirUnits:     []string{"commands", "agents", "skills"},
		FileLeafUnits: []string{"commands", "agents"},
		TargetPrefix: ".claude/",
	}, DefaultSkillsScanLimits()))
	writeHostResource(t, hostRoot, "commands/review.md", "# /review\n")
	writeHostResource(t, hostRoot, "agents/helper.md", "# helper agent\n")
	writeHostResource(t, hostRoot, "commands/frontend/component.md", "# frontend:component\n")
	writeHostResource(t, hostRoot, "commands/notes.txt", "not a command")
	scope := c.resourceScope(CLIClaude)
	scope.Scan()
	snap := scope.Snapshot()
	units := map[string]bool{}
	for _, m := range snap.Skills {
		units[m.Name] = true
	}
	for _, want := range []string{"commands/review.md", "agents/helper.md", "commands/frontend"} {
		if !units[want] {
			t.Fatalf("expected unit %s in inventory, got %+v", want, units)
		}
	}
	if units["commands/notes.txt"] {
		t.Fatal("non-markdown top-level file must not be a unit")
	}
	// isFileUnit：顶层 md 文件走单文件探测；目录单元走树探测。
	if !scope.isFileUnit("commands/review.md") || scope.isFileUnit("commands/frontend") {
		t.Fatal("isFileUnit must distinguish leaf md units from dir units")
	}
	// 供给贯通：顶层 md 文件落到消费者路径 .claude/commands/review.md。
	grantConsentFor(t, c, CLIClaude, ResourceUnitID(CLIClaude, "commands/review.md"))
	res := runFirst(t, c, CLIClaude, ResourceUnitID(CLIClaude, "commands/review.md"))
	if res.Outcome != UnitApplied {
		t.Fatalf("leaf md unit must supply, got %+v", res)
	}
	data, err := os.ReadFile(claudeTargetPath(fp, "commands/review.md"))
	if err != nil || string(data) != "# /review\n" {
		t.Fatalf("leaf md must land at consumer path .claude/commands/review.md: %q %v", data, err)
	}
	// 目录单元（嵌套名）同样贯通。
	grantConsentFor(t, c, CLIClaude, ResourceUnitID(CLIClaude, "commands/frontend"))
	res = runFirst(t, c, CLIClaude, ResourceUnitID(CLIClaude, "commands/frontend"))
	if res.Outcome != UnitApplied {
		t.Fatalf("nested dir unit must supply, got %+v", res)
	}
	nested, err := os.ReadFile(claudeTargetPath(fp, "commands/frontend/component.md"))
	if err != nil || string(nested) != "# frontend:component\n" {
		t.Fatalf("nested dir unit must land: %q %v", nested, err)
	}
}

// TestResourceCodexSkillsNoLeafFileUnits（SA-10 消费边界标注）：codex skills 按
// 目录发现——顶层散落 md 不入清单（不虚构 CLI 未定义的文件单元语义）。
func TestResourceCodexSkillsNoLeafFileUnits(t *testing.T) {
	c := newCoordinatorWith(t, newFakeProbes(t), nil)
	hostRoot := t.TempDir()
	c.RegisterResources(NewResourceScope(CLICodex, ResourceLayout{
		HostRoot: hostRoot,
		Files:    []string{"AGENTS.md"},
		DirUnits: []string{"skills"},
	}, DefaultSkillsScanLimits()))
	writeHostResource(t, hostRoot, "skills/loose.md", "# not a skill folder\n")
	writeHostResource(t, hostRoot, "skills/pack/SKILL.md", "# pack\n")
	scope := c.resourceScope(CLICodex)
	scope.Scan()
	snap := scope.Snapshot()
	if len(snap.Skills) != 1 || snap.Skills[0].Name != "skills/pack" {
		t.Fatalf("codex skills must stay directory-only, got %+v", snap.Skills)
	}
}

// TestResourceScopeScanIdempotentAndRescan（SA-17）：资源扫描完成后 Get 不重启
//（StartScanAsync 幂等）；显式 RescanAsync 才重扫。
func TestResourceScopeScanIdempotentAndRescan(t *testing.T) {
	hostRoot := t.TempDir()
	writeHostResource(t, hostRoot, "CLAUDE.md", "# v1\n")
	scope := NewResourceScope(CLIClaude, ResourceLayout{
		HostRoot: hostRoot, Files: []string{"CLAUDE.md"}, TargetPrefix: ".claude/",
	}, DefaultSkillsScanLimits())
	if !scope.StartScanAsync() {
		t.Fatal("first start must begin scanning")
	}
	deadline := time.Now().Add(10 * time.Second)
	for scope.Snapshot().Running {
		if time.Now().After(deadline) {
			t.Fatal("scan did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	writeHostResource(t, hostRoot, "CLAUDE.md", "# v2\n")
	if scope.StartScanAsync() {
		t.Fatal("completed scan must not restart via StartScanAsync (Get path)")
	}
	scope.Scan() // 同步重扫等价 Rescan 的扫描体；此处直接校验缓存跟随。
	// 上一行是同步扫描（测试用）；真正的强制重扫入口验证：
	writeHostResource(t, hostRoot, "CLAUDE.md", "# v3\n")
	if !scope.RescanAsync() {
		t.Fatal("RescanAsync must start after completion")
	}
	deadline = time.Now().Add(10 * time.Second)
	for scope.Snapshot().Running {
		if time.Now().After(deadline) {
			t.Fatal("rescan did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	found := ""
	for _, m := range scope.Snapshot().Skills {
		if m.Name == "CLAUDE.md" {
			found = string(m.SourceHash)
		}
	}
	if found == "" {
		t.Fatal("rescan must keep the file unit visible")
	}
}
