package sharedext

// skills_deploy_test.go — 覆盖清单 5-7：启用分发/禁用回收、targets diff、
// 删除清理与 Missing 标记。

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// importOne 导入单个 skill 并返回其名。
func importOne(t *testing.T, env *testEnv, name string) string {
	t.Helper()
	src := t.TempDir()
	writeSkillDir(t, src, name, "---\nname: "+name+"\ndescription: "+name+" 技能\n---\n\n"+name+" 正文\n")
	skills, err := env.svc.ImportSkillFromDir(src)
	if err != nil || len(skills) != 1 {
		t.Fatalf("导入 %s 失败: %v %+v", name, err, skills)
	}
	return skills[0].Name
}

// 覆盖 5：启用 → 分发到 2 引擎，目录内容与源一致；禁用 → 回收后
// Deployed 清空、目录移除。
func TestSkillEnableDeployAndDisableRecycle(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "deployable")

	if err := env.svc.SetSharedSkillTargets(name, []Engine{EngineClaude, EngineCodex}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatal(err)
	}

	// claude: <base>/.claude/skills/<name>/；codex: <base>/skills/<name>/
	claudeTarget := filepath.Join(env.roots[EngineClaude], ".claude", "skills", name)
	codexTarget := filepath.Join(env.roots[EngineCodex], "skills", name)
	for _, dir := range []string{claudeTarget, codexTarget} {
		if got := mustReadFile(t, filepath.Join(dir, "SKILL.md")); !contains(got, name+" 正文") {
			t.Fatalf("目标 SKILL.md 内容与源不一致: %s", dir)
		}
	}

	skills, err := env.svc.ListSharedSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || !skills[0].Enabled {
		t.Fatalf("条目应启用: %+v", skills)
	}
	if !reflect.DeepEqual([]Engine(skills[0].Deployed), []Engine{EngineClaude, EngineCodex}) {
		t.Fatalf("Deployed 应为 [claude codex]: %+v", skills[0].Deployed)
	}

	// 禁用 → 回收全部。
	if err := env.svc.SetSharedSkillEnabled(name, false); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{claudeTarget, codexTarget} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("回收后目录应移除: %s (%v)", dir, err)
		}
	}
	skills, err = env.svc.ListSharedSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills[0].Deployed) != 0 {
		t.Fatalf("禁用后 Deployed 应清空: %+v", skills[0].Deployed)
	}
	if skills[0].Enabled {
		t.Fatal("禁用后条目应为停用状态")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// 覆盖 5 补充：重复启用 = 重新复制覆盖（幂等），内容更新生效。
func TestSkillEnableIdempotentOverwrite(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "idem")
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EnginePi}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatal(err)
	}

	// 修改仓库内容后再次启用 → 目标被覆盖为新内容。
	repoMD := filepath.Join(env.svc.skillsRepoDir(), name, "SKILL.md")
	if err := os.WriteFile(repoMD, []byte("---\nname: idem\ndescription: 更新\n---\n\n新正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(env.roots[EnginePi], "skills", name, "SKILL.md")
	if got := mustReadFile(t, target); !contains(got, "新正文") {
		t.Fatalf("重复启用应覆盖目标内容: %q", got)
	}
}

// 覆盖 6：targets diff：加目标分发、减目标回收；enabled=false 时改 targets
// 只存不分发。
func TestSkillTargetsDiff(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "diffy")

	// enabled=false 时改 targets：只存不分发。
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EngineClaude}); err != nil {
		t.Fatal(err)
	}
	claudeTarget := env.skillsTarget(EngineClaude, name)
	if _, err := os.Stat(claudeTarget); !os.IsNotExist(err) {
		t.Fatal("disabled 状态改 targets 不应分发")
	}

	// 启用后加目标 → 分发。
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EngineClaude, EngineOpenCode}); err != nil {
		t.Fatal(err)
	}
	if !fileExists(t, claudeTarget) || !fileExists(t, env.skillsTarget(EngineOpenCode, name)) {
		t.Fatal("加目标后两个引擎都应部署")
	}

	// 减目标 → 回收（claude 移除，opencode 保留）。
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EngineOpenCode}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claudeTarget); !os.IsNotExist(err) {
		t.Fatal("移除的目标应被回收")
	}
	if !fileExists(t, env.skillsTarget(EngineOpenCode, name)) {
		t.Fatal("保留的目标不应被回收")
	}

	skills, err := env.svc.ListSharedSkills()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]Engine(skills[0].Deployed), []Engine{EngineOpenCode}) {
		t.Fatalf("Deployed 应只剩 opencode: %+v", skills[0].Deployed)
	}
}

// 覆盖 6 补充：非法引擎标识报错。
func TestSkillTargetsInvalidEngine(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "valid8")
	if err := env.svc.SetSharedSkillTargets(name, []Engine{"bogus"}); err == nil {
		t.Fatal("非法引擎应报错")
	}
}

// 覆盖 7：删除 skill → 部署目录与 registry 均清理。
func TestSkillDeleteCleansEverything(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "gone")
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EngineOmp}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatal(err)
	}
	ompTarget := env.skillsTarget(EngineOmp, name)
	if !fileExists(t, ompTarget) {
		t.Fatal("前置：已部署")
	}

	if err := env.svc.DeleteSharedSkill(name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ompTarget); !os.IsNotExist(err) {
		t.Fatal("删除后部署目录应移除")
	}
	if _, err := os.Stat(env.svc.skillsRepoDir() + string(os.PathSeparator) + name); !os.IsNotExist(err) {
		t.Fatal("删除后仓库目录应移除")
	}
	skills, err := env.svc.ListSharedSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("registry 应清空: %+v", skills)
	}
	// 再删 → 明确报错。
	if err := env.svc.DeleteSharedSkill(name); err == nil {
		t.Fatal("删除不存在的 skill 应报错")
	}
}

// 覆盖 7 补充：Missing 标记——仓库目录失踪的条目保留返回并标记。
func TestSkillMissingFlag(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "lost")
	// 模拟仓库目录被外部删除。
	if err := os.RemoveAll(env.svc.skillsRepoDir()); err != nil {
		t.Fatal(err)
	}

	skills, err := env.svc.ListSharedSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || !skills[0].Missing {
		t.Fatalf("失联条目应标记 Missing: %+v", skills)
	}
	// Missing 条目启用应失败（源目录缺失）。
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EnginePi}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillEnabled(name, true); err == nil {
		t.Fatal("Missing 条目启用应失败")
	}
}
