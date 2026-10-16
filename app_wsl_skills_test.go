package main

// app_wsl_skills_test.go — P3-S3 技能面 App 门行为钉子：状态面（扫描启动/
// 逐技能状态/不兼容原因）、预览勾选导入（同意门 + RunUnits）、不可移植拒绝、
// 保留本地、配置状态区第五面（skills face）。真协调器 + fake 探测面 + 临时
// 宿主技能根，不触达真实 WSL。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/wslsupply"
)

// newSkillsTestApp 构造带技能作用域协调器的 App（宿主根 = 临时目录）。
func newSkillsTestApp(t *testing.T) (*App, *wslsupply.SkillsScope, string) {
	t.Helper()
	origDefault := wslDefaultDistroFn
	t.Cleanup(func() { wslDefaultDistroFn = origDefault })
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }

	app := newTestApp(t)
	uncRoot := t.TempDir()
	hostRoot := t.TempDir()
	c := wslsupply.NewCoordinator(t.TempDir(), nil, wslsupply.Probes{
		Identity: func(string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m1", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_, _ string, _ ...string) error { return nil },
	})
	scope := wslsupply.NewSkillsScope(hostRoot, wslsupply.DefaultSkillsScanLimits())
	c.RegisterSkills(scope)
	scope.Scan()
	app.WSLSupply = c
	return app, scope, uncRoot
}

func writeHostSkill(t *testing.T, root, skill, rel, content string) {
	t.Helper()
	p := filepath.Join(root, skill, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWSLSkillsSupplyStatusListsSkills(t *testing.T) {
	app, scope, _ := newSkillsTestApp(t)
	writeHostSkill(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	writeHostSkill(t, scope.Root(), "win", "SKILL.md", "# doc\n")
	writeHostSkill(t, scope.Root(), "win", "run.ps1", "echo")
	scope.Scan()

	st := app.GetWSLSkillsSupplyStatus()
	if !st.Available || !st.ScanStarted || len(st.Skills) != 2 {
		t.Fatalf("unexpected status: %+v", st)
	}
	byName := map[string]WSLSkillSummary{}
	for _, s := range st.Skills {
		byName[s.Name] = s
	}
	if s := byName["alpha"]; s.State != "importable" || !s.Selectable {
		t.Fatalf("alpha must be importable/selectable: %+v", s)
	}
	if s := byName["win"]; s.State != "incompatible" || s.Selectable || len(s.Incompatible) == 0 {
		t.Fatalf("win must be incompatible with reasons: %+v", s)
	}
	if !strings.Contains(strings.Join(byName["win"].Incompatible, ";"), "run.ps1") {
		t.Fatalf("incompat reason must name the item: %+v", byName["win"].Incompatible)
	}
}

func TestApplyWSLSkillsSupplyConsentsAndSupplies(t *testing.T) {
	app, scope, uncRoot := newSkillsTestApp(t)
	writeHostSkill(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	writeHostSkill(t, scope.Root(), "win", "SKILL.md", "# doc\n")
	writeHostSkill(t, scope.Root(), "win", "run.ps1", "echo")
	scope.Scan()

	out := app.ApplyWSLSkillsSupply([]string{"alpha", "win", "ghost"})
	byUnit := map[string]WSLConfigUnitOutcome{}
	for _, o := range out {
		byUnit[o.Unit] = o
	}
	if o := byUnit["shared/skill/alpha"]; o.Outcome != "applied" {
		t.Fatalf("alpha must be applied: %+v", out)
	}
	if o := byUnit["shared/skill/win"]; o.Outcome != "blocked" || !strings.Contains(o.Detail, "不可移植") {
		t.Fatalf("incompatible must be rejected with reason: %+v", o)
	}
	if o := byUnit["shared/skill/ghost"]; o.Outcome != "blocked" {
		t.Fatalf("unknown skill must be rejected: %+v", o)
	}
	got, err := os.ReadFile(filepath.Join(uncRoot, "home", "u", ".agents", "skills", "alpha", "SKILL.md"))
	if err != nil || string(got) != "# alpha\n" {
		t.Fatalf("supplied content mismatch: %q %v", got, err)
	}
	if _, statErr := os.Lstat(filepath.Join(uncRoot, "home", "u", ".agents", "skills", "win")); !os.IsNotExist(statErr) {
		t.Fatal("incompatible skill must not be written")
	}
	// 状态面收敛为 supplied（诚实标注：静态检查≠运行验证）。
	st := app.GetWSLSkillsSupplyStatus()
	for _, s := range st.Skills {
		if s.Name == "alpha" && s.State != "supplied" {
			t.Fatalf("alpha state must be supplied: %+v", s)
		}
	}
}

func TestWSLSkillsKeepLocalAndPreview(t *testing.T) {
	app, scope, uncRoot := newSkillsTestApp(t)
	writeHostSkill(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	if out := app.ApplyWSLSkillsSupply([]string{"alpha"}); len(out) != 1 || out[0].Outcome != "applied" {
		t.Fatalf("apply: %+v", out)
	}
	previews := app.PreviewWSLSkillsSupply()
	if len(previews) != 1 || previews[0].Name != "alpha" || previews[0].Action != "none" {
		t.Fatalf("preview: %+v", previews)
	}
	if err := app.KeepWSLSkillLocal("alpha"); err != nil {
		t.Fatal(err)
	}
	st := app.GetWSLSkillsSupplyStatus()
	if len(st.Skills) != 1 || st.Skills[0].State != "detached" {
		t.Fatalf("detached state: %+v", st.Skills)
	}
	// 停随后目标内容保留。
	got, _ := os.ReadFile(filepath.Join(uncRoot, "home", "u", ".agents", "skills", "alpha", "SKILL.md"))
	if string(got) != "# alpha\n" {
		t.Fatalf("local content must be retained: %q", got)
	}
}

func TestWSLConfigSupplyStatusIncludesSkillsFace(t *testing.T) {
	app, _, _ := newSkillsTestApp(t)
	st := app.GetWSLConfigSupplyStatus()
	found := false
	for _, f := range st.Faces {
		if f.Key == "skills" {
			found = true
		}
	}
	if !found {
		t.Fatalf("skills face missing: %+v", st.Faces)
	}
}

func TestWSLSkillsTakeOverViaFacade(t *testing.T) {
	app, scope, uncRoot := newSkillsTestApp(t)
	writeHostSkill(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	// 未托管目标：预览后接管。
	own := filepath.Join(uncRoot, "home", "u", ".agents", "skills", "alpha")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "SKILL.md"), []byte("user own"), 0o644); err != nil {
		t.Fatal(err)
	}
	previews := app.PreviewWSLSkillsSupply()
	// F6：未同意预览恒 awaiting-consent（与 Plan 同一答案），仍携带接管令牌。
	if len(previews) != 1 || previews[0].Blocked != "awaiting-consent" || previews[0].TargetHash == "" {
		t.Fatalf("unconsented preview must be awaiting-consent with token: %+v", previews)
	}
	// 未选择导入（未授权）时接管被拦。
	if err := app.TakeOverWSLSkill("alpha", previews[0].TargetHash); err == nil || !strings.Contains(err.Error(), "导入") {
		t.Fatalf("unconsented takeover must be rejected: %v", err)
	}
	if out := app.ApplyWSLSkillsSupply([]string{"alpha"}); len(out) != 1 || out[0].Outcome != "blocked" {
		t.Fatalf("apply over unmanaged must block: %+v", out)
	}
	// 授权后预览见真实归属态，接管成功。
	previews = app.PreviewWSLSkillsSupply()
	if len(previews) != 1 || previews[0].Blocked != "unmanaged-existing" {
		t.Fatalf("consented preview must show ownership state: %+v", previews)
	}
	// 授权后接管成功。
	if err := app.TakeOverWSLSkill("alpha", previews[0].TargetHash); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(own, "SKILL.md"))
	if string(got) != "# alpha\n" {
		t.Fatalf("takeover must rewrite from source: %q", got)
	}
}
