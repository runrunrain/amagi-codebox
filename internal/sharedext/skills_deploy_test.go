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

// ---------------------------------------------------------------------------
// P5-Fix：M1 部分失败落盘 / M2 用户自建同名目录保护
// ---------------------------------------------------------------------------

// M1：多引擎部分失败（codex 基准根被文件占位、claude 成功）→ 返回聚合
// 错误但 registry 已记录成功引擎；后续禁用能把已写入目标回收干净
//（diting D5 场景不复现）。
func TestSkillPartialDeployRecordsSuccess(t *testing.T) {
	base := t.TempDir()
	env := newTestService(t)
	// codex 基准根改为已存在文件 → engineSkillsDir 的 MkdirAll 失败。
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.svc.SetEngineBaseDir(EngineCodex, blocker)

	name := importOne(t, env, "partial")
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EngineClaude, EngineCodex}); err != nil {
		t.Fatal(err)
	}

	enableErr := env.svc.SetSharedSkillEnabled(name, true)
	if enableErr == nil {
		t.Fatal("预期 codex 部署失败返回聚合错误")
	}
	claudeTarget := filepath.Join(env.roots[EngineClaude], ".claude", "skills", name, "SKILL.md")
	if _, err := os.Stat(claudeTarget); err != nil {
		t.Fatalf("claude 目标应有 skill 文件（磁盘实况）: %v", err)
	}
	skills, err := env.svc.ListSharedSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || !skills[0].Enabled || !reflect.DeepEqual([]Engine(skills[0].Deployed), []Engine{EngineClaude}) {
		t.Fatalf("registry 应记录 enabled=true deployed=[claude]: %+v", skills)
	}

	// 禁用：按 Deployed 回收 claude（不再永久残留）。
	if err := env.svc.SetSharedSkillEnabled(name, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claudeTarget); !os.IsNotExist(err) {
		t.Fatalf("禁用后 claude 目标应被回收: %v", err)
	}
}

// M2：目标存在同名目录且该引擎不在 Deployed 清单（用户自建）→ 拒绝部署
// 且目录原样；CodeBox 自己部署过的（Deployed 含该引擎）→ 重部署覆盖成功。
func TestSkillDeployUserDirConflictRefused(t *testing.T) {
	env := newTestService(t)
	name := importOne(t, env, "clashy")
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EnginePi}); err != nil {
		t.Fatal(err)
	}

	// 用户自建同名目录。
	piTarget := env.skillsTarget(EnginePi, name)
	if err := os.MkdirAll(piTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	userMD := "用户自建内容，绝不能被删\n"
	if err := os.WriteFile(filepath.Join(piTarget, "SKILL.md"), []byte(userMD), 0o644); err != nil {
		t.Fatal(err)
	}

	// 启用：pi 目标冲突 → 报错且用户目录原样。
	enableErr := env.svc.SetSharedSkillEnabled(name, true)
	if enableErr == nil || !strings.Contains(enableErr.Error(), "非 CodeBox 管理目录") {
		t.Fatalf("用户自建同名目录应拒绝部署: %v", enableErr)
	}
	if got := mustReadFile(t, filepath.Join(piTarget, "SKILL.md")); got != userMD {
		t.Fatalf("用户目录内容应原样保留:\n%s", got)
	}
	skills, err := env.svc.ListSharedSkills()
	if err != nil || len(skills) != 1 {
		t.Fatal(err)
	}
	if len(skills[0].Deployed) != 0 {
		t.Fatalf("冲突引擎不应进 Deployed: %+v", skills[0].Deployed)
	}

	// CodeBox 自己部署过的引擎（Deployed 含该引擎）→ 重部署覆盖成功。
	if err := env.svc.SetSharedSkillTargets(name, []Engine{EngineOmp}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatalf("omp 无冲突应部署成功: %v", err)
	}
	ompTarget := env.skillsTarget(EngineOmp, name)
	// 修改仓库内容后重复启用（Deployed 含 omp）→ 覆盖为新内容。
	repoMD := filepath.Join(env.svc.skillsRepoDir(), name, "SKILL.md")
	if err := os.WriteFile(repoMD, []byte("---\nname: clashy\ndescription: d\n---\n\n第二版正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetSharedSkillEnabled(name, true); err != nil {
		t.Fatalf("重部署自己管理的目录应覆盖成功: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(ompTarget, "SKILL.md")); !contains(got, "第二版正文") {
		t.Fatalf("重部署应覆盖为新内容: %q", got)
	}
	// 用户自建目录在整过程中仍原样。
	if got := mustReadFile(t, filepath.Join(piTarget, "SKILL.md")); got != userMD {
		t.Fatalf("用户目录内容应始终原样:\n%s", got)
	}
}
