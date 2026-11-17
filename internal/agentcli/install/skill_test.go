package install

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSkillTargetsIndependent(t *testing.T) {
	m := testManager(t, shimUnix, goldenTable())
	for _, target := range SkillTargets {
		if got := m.SkillStatus(target).State; got != StateNotInstalled {
			t.Fatalf("%s 初始状态 = %s, want not-installed", target, got)
		}
	}
	if err := m.InstallSkill(SkillTargetAgents); err != nil {
		t.Fatal(err)
	}
	if got := m.SkillStatus(SkillTargetAgents).State; got != StateUpToDate {
		t.Fatalf("agents 安装后状态 = %s, want up-to-date", got)
	}
	// 其余目标互不影响
	if got := m.SkillStatus(SkillTargetClaude).State; got != StateNotInstalled {
		t.Fatalf("claude 状态 = %s, want not-installed", got)
	}
	if got := m.SkillStatus(SkillTargetCodex).State; got != StateNotInstalled {
		t.Fatalf("codex 状态 = %s, want not-installed", got)
	}

	// 文件位置与内容
	dir := filepath.Join(m.userHome, ".agents", "skills", "amagi-codebox")
	skill, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// 模板渲染：shim 路径以 JSON 字符串形式注入
	if !strings.Contains(string(skill), strconv.Quote(m.ShimPath())) {
		t.Fatal("SKILL.md 应包含 JSON 字符串形式的本机 shim 路径")
	}
	if !strings.Contains(string(skill), "name: amagi-codebox") {
		t.Fatal("SKILL.md frontmatter 应声明 name")
	}
	if strings.Contains(string(skill), "{{") {
		t.Fatal("模板占位符应全部被替换")
	}
	// 末行为托管标记
	lines := strings.Split(strings.TrimRight(string(skill), "\n"), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], "<!-- amagi-codebox managed sha256:") {
		t.Fatalf("SKILL.md 末行应为托管标记，got %q", lines[len(lines)-1])
	}

	cmdsMd, err := os.ReadFile(filepath.Join(dir, "references", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cmdsMd), "### session.launch") || !strings.Contains(string(cmdsMd), "确认值 `LAUNCH`") {
		t.Fatal("references/commands.md 应由命令表生成")
	}

	// 清理：文件与空目录都移除；其余目标不受影响
	if err := m.RemoveSkill(SkillTargetAgents); err != nil {
		t.Fatal(err)
	}
	if got := m.SkillStatus(SkillTargetAgents).State; got != StateNotInstalled {
		t.Fatalf("agents 清理后状态 = %s, want not-installed", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("空技能目录应删除")
	}
	if got := m.SkillStatus(SkillTargetClaude).State; got != StateNotInstalled {
		t.Fatal("清理 agents 不应影响 claude")
	}
}

func TestSkillAllTargetsInstall(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	for _, target := range SkillTargets {
		if err := m.InstallSkill(target); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
		if got := m.SkillStatus(target).State; got != StateUpToDate {
			t.Fatalf("%s 状态 = %s, want up-to-date", target, got)
		}
	}
	// 未安装时清理幂等成功
	if err := m.RemoveSkill(SkillTargetCodex); err != nil {
		t.Fatal(err)
	}
}

func TestSkillOutdatedOnTableChange(t *testing.T) {
	home, userHome := t.TempDir(), t.TempDir()
	old := newManager(home, userHome, "/opt/app", goldenTable(), shimUnix)
	if err := old.InstallSkill(SkillTargetClaude); err != nil {
		t.Fatal(err)
	}
	// 命令表变化（新版本应用）→ references/commands.md 过期 → outdated
	next := newManager(home, userHome, "/opt/app", nil, shimUnix)
	if got := next.SkillStatus(SkillTargetClaude).State; got != StateOutdated {
		t.Fatalf("命令表变化后状态 = %s, want outdated", got)
	}
	if err := next.InstallSkill(SkillTargetClaude); err != nil {
		t.Fatal(err)
	}
	if got := next.SkillStatus(SkillTargetClaude).State; got != StateUpToDate {
		t.Fatalf("更新后状态 = %s, want up-to-date", got)
	}

	// exePath 变化（应用更新换位）：技能体只注入 shim 路径，不受影响；
	// 变的是 shim 文件本身 → ShimStatus 应为 outdated。
	old2 := newManager(home, userHome, "/opt/old-app", goldenTable(), shimUnix)
	if err := old2.InstallShim(); err != nil {
		t.Fatal(err)
	}
	// 换回真实表内容，排除表差异干扰后单独验证 exePath 影响
	if err := old2.InstallSkill(SkillTargetClaude); err != nil {
		t.Fatal(err)
	}
	moved := newManager(home, userHome, "/opt/new-app", goldenTable(), shimUnix)
	if got := moved.ShimStatus().State; got != StateOutdated {
		t.Fatalf("exePath 变化后 shim 状态 = %s, want outdated", got)
	}
	if got := moved.SkillStatus(SkillTargetClaude).State; got != StateUpToDate {
		t.Fatalf("exePath 变化不应使技能过期，got %s", got)
	}
	if err := moved.InstallShim(); err != nil {
		t.Fatal(err)
	}
	if got := moved.ShimStatus().State; got != StateUpToDate {
		t.Fatalf("shim 升级后状态 = %s, want up-to-date", got)
	}
}

func TestSkillPartialInstallIsOutdated(t *testing.T) {
	m := testManager(t, shimUnix, goldenTable())
	files, err := m.skillFiles(SkillTargetAgents)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(files[0].path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 只装 SKILL.md（缺 references/commands.md）→ outdated（补齐而非重装）
	if err := os.WriteFile(files[0].path, []byte(files[0].body+files[0].mark(sha256Hex(files[0].body))), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.SkillStatus(SkillTargetAgents).State; got != StateOutdated {
		t.Fatalf("半装状态 = %s, want outdated", got)
	}
	if err := m.InstallSkill(SkillTargetAgents); err != nil {
		t.Fatal(err)
	}
	if got := m.SkillStatus(SkillTargetAgents).State; got != StateUpToDate {
		t.Fatalf("补齐后状态 = %s, want up-to-date", got)
	}
}

func TestSkillModifiedByUser(t *testing.T) {
	m := testManager(t, shimUnix, goldenTable())
	if err := m.InstallSkill(SkillTargetClaude); err != nil {
		t.Fatal(err)
	}
	dir, err := m.SkillDir(SkillTargetClaude)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("用户补充说明\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if got := m.SkillStatus(SkillTargetClaude).State; got != StateModifiedByUser {
		t.Fatalf("外部改动后状态 = %s, want modified-by-user", got)
	}
	var ce *ConflictedError
	if err := m.InstallSkill(SkillTargetClaude); !errors.As(err, &ce) {
		t.Fatalf("安装应报冲突，got %v", err)
	}
	if err := m.RemoveSkill(SkillTargetClaude); !errors.As(err, &ce) {
		t.Fatalf("清理应拒绝，got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "用户补充说明") {
		t.Fatal("用户内容必须保留")
	}
}

func TestSkillPathWithSymlinkIsInvalid(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	real := t.TempDir()
	link := filepath.Join(m.userHome, ".agents")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("无法创建符号链接（平台限制）：%v", err)
	}
	st := m.SkillStatus(SkillTargetAgents)
	if st.State != StateInvalid {
		t.Fatalf("链接路径状态 = %s, want invalid", st.State)
	}
	if err := m.InstallSkill(SkillTargetAgents); err == nil {
		t.Fatal("应拒绝经符号链接安装")
	}
}

// TestSkillReferencesSymlinkInvalidatesStatusAndBlocksRemove —
// references 目录是符号链接（指向另一目标）时：SkillStatus 必须 invalid，
// RemoveSkill 必须拒绝且不得删到链接目标里的文件（评审 D-12）。
func TestSkillReferencesSymlinkInvalidatesStatusAndBlocksRemove(t *testing.T) {
	m := testManager(t, shimUnix, goldenTable())
	if err := m.InstallSkill(SkillTargetAgents); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallSkill(SkillTargetClaude); err != nil {
		t.Fatal(err)
	}
	agentsDir, err := m.SkillDir(SkillTargetAgents)
	if err != nil {
		t.Fatal(err)
	}
	claudeDir, err := m.SkillDir(SkillTargetClaude)
	if err != nil {
		t.Fatal(err)
	}

	// 把 claude/references 替换为指向 agents/references 的符号链接。
	if err := os.RemoveAll(filepath.Join(claudeDir, "references")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(agentsDir, "references"), filepath.Join(claudeDir, "references")); err != nil {
		t.Skipf("无法创建符号链接（平台限制）：%v", err)
	}

	// 状态：invalid（不得透过链接把内容当作本目标的管理面）。
	if st := m.SkillStatus(SkillTargetClaude); st.State != StateInvalid {
		t.Fatalf("references 为符号链接时状态=%s, want invalid（msg=%q）", st.State, st.Message)
	}
	// 清理：拒绝且不得删除（agents 目标的 references 文件必须完好）。
	if err := m.RemoveSkill(SkillTargetClaude); err == nil {
		t.Fatal("references 为符号链接时清理应拒绝")
	}
	if _, err := os.Stat(filepath.Join(agentsDir, "references", "commands.md")); err != nil {
		t.Fatalf("链接目标（agents）的参考文档被误删: %v", err)
	}
	// 被链接指向的 agents 目标不受影响。
	if st := m.SkillStatus(SkillTargetAgents); st.State != StateUpToDate {
		t.Fatalf("agents 状态=%s, want up-to-date", st.State)
	}
}

func TestUnknownSkillTargetRejected(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	if _, err := m.SkillDir("nope"); err == nil {
		t.Fatal("未知目标应报错")
	}
	if got := m.SkillStatus("nope").State; got != StateInvalid {
		t.Fatalf("未知目标状态 = %s, want invalid", got)
	}
	if err := m.InstallSkill("nope"); err == nil {
		t.Fatal("未知目标安装应报错")
	}
}

func TestRemoveRecordsPreferenceAndDefaultsRespectIt(t *testing.T) {
	m := testManager(t, shimUnix, goldenTable())
	var errs []error
	m.InstallDefaults(func(err error) { errs = append(errs, err) })
	if len(errs) != 0 {
		t.Fatalf("默认安装应无错误：%v", errs)
	}
	if m.ShimStatus().State != StateUpToDate {
		t.Fatal("默认应安装 shim")
	}
	if m.SkillStatus(SkillTargetAgents).State != StateUpToDate {
		t.Fatal("默认应安装 agents 技能")
	}
	if m.SkillStatus(SkillTargetClaude).State != StateNotInstalled {
		t.Fatal("claude/codex 不属于默认安装")
	}

	// 用户显式清理 shim 与 agents 技能
	if err := m.RemoveShim(); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveSkill(SkillTargetAgents); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.home, "agent-cli-install.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"shim":false`) || !strings.Contains(string(data), `"agents":false`) {
		t.Fatalf("偏好文件应记录清理选择，got %s", data)
	}

	// 再次默认安装：不装回
	errs = nil
	m.InstallDefaults(func(err error) { errs = append(errs, err) })
	if len(errs) != 0 {
		t.Fatalf("跳过已清理面时不应报错：%v", errs)
	}
	if m.ShimStatus().State != StateNotInstalled {
		t.Fatal("清理过的 shim 不应装回")
	}
	if m.SkillStatus(SkillTargetAgents).State != StateNotInstalled {
		t.Fatal("清理过的 agents 技能不应装回")
	}

	// 显式安装会清除清理记录
	if err := m.InstallShim(); err != nil {
		t.Fatal(err)
	}
	declined, err := m.declinedDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if declined[prefShim] {
		t.Fatal("显式安装应清除 shim 清理记录")
	}
	if !declined[prefAgents] {
		t.Fatal("agents 清理记录应保留")
	}
}

func TestSkillRemovalRecordsPreference(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	if err := m.InstallSkill(SkillTargetCodex); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveSkill(SkillTargetCodex); err != nil {
		t.Fatal(err)
	}
	declined, err := m.declinedDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if !declined[prefCodex] {
		t.Fatal("codex 清理选择应持久化")
	}
}

func TestInvalidPreferencesBlockDefaults(t *testing.T) {
	m := testManager(t, shimUnix, goldenTable())
	if err := os.WriteFile(filepath.Join(m.home, "agent-cli-install.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.declinedDefaults(); err == nil {
		t.Fatal("损坏的偏好文件应报错")
	}
	var collected []error
	m.InstallDefaults(func(err error) { collected = append(collected, err) })
	if len(collected) != 1 {
		t.Fatalf("应上报一个偏好错误，got %d", len(collected))
	}
	if m.ShimStatus().State != StateNotInstalled {
		t.Fatal("偏好损坏时不应安装")
	}

	// 已知键值非布尔同样报错
	if err := os.WriteFile(filepath.Join(m.home, "agent-cli-install.json"), []byte(`{"shim":"yes"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.declinedDefaults(); err == nil {
		t.Fatal("非布尔值应报错")
	}
}

func TestPreferencesIgnoreUnknownKeys(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	if err := os.WriteFile(filepath.Join(m.home, "agent-cli-install.json"),
		[]byte("{\"shim\": false, \"future-key\": \"whatever\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	declined, err := m.declinedDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if !declined[prefShim] {
		t.Fatal("应读出 shim 清理记录")
	}
	// 写回时未知键被丢弃（文件由本应用全权管理，键集合封闭）
	if err := m.setDeclined(prefAgents, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.home, "agent-cli-install.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "future-key") {
		t.Fatalf("写回应只含已知键，got %s", data)
	}
}
