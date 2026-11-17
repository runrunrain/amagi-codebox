package sharedskills

// service_test.go — 临时目录夹具（模式参照 internal/agentcli/install/skill_test.go），
// 覆盖 spec §2.8 全部用例：List 合并/排序/隐藏过滤/双根缺失空态、frontmatter
// 正常/缺失/坏 YAML/64KB 截断、SetEnabled 往返/目标冲突/穿越拒绝/链接拒绝/
// 源缺失/MkdirAll 失败/移动失败回滚、Delete 生效。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newFixture(t *testing.T) *Service {
	t.Helper()
	return New(t.TempDir(), nil)
}

// writeSkill 在 root 下创建技能目录（frontmatter 为空串时不写 SKILL.md），
// 返回目录绝对路径。
func writeSkill(t *testing.T, root, name, frontmatter string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if frontmatter != "" {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(frontmatter), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func mustList(t *testing.T, s *Service) []Skill {
	t.Helper()
	skills, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return skills
}

// ---- List ----

func TestList_MergesBothRootsSortedAndFiltersHidden(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "zeta", "---\nname: Zeta Skill\ndescription: z\n---\n正文")
	writeSkill(t, s.disabledRoot(), "alpha", "---\nname: Alpha Skill\n---\n")
	// 隐藏目录、普通文件均不列（对齐 wslsupply listSkillDirs 取舍）。
	writeSkill(t, s.SkillsRoot(), ".hidden", "---\nname: H\n---\n")
	if err := os.WriteFile(filepath.Join(s.SkillsRoot(), "afile.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills := mustList(t, s)
	var names []string
	for _, sk := range skills {
		names = append(names, sk.Name)
	}
	want := []string{"alpha", "zeta"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("List 名称序 = %v, want %v", names, want)
	}
	byName := map[string]Skill{}
	for _, sk := range skills {
		byName[sk.Name] = sk
	}
	if sk := byName["alpha"]; sk.Enabled || sk.DisplayName != "Alpha Skill" || sk.Path != filepath.Join(s.disabledRoot(), "alpha") {
		t.Fatalf("alpha = %+v（应位于禁用根：Enabled=false）", sk)
	}
	if sk := byName["zeta"]; !sk.Enabled || sk.DisplayName != "Zeta Skill" || sk.Description != "z" {
		t.Fatalf("zeta = %+v（Enabled 应为 true）", sk)
	}
}

func TestList_EmptyWhenBothRootsMissing(t *testing.T) {
	s := newFixture(t)
	skills, err := s.List()
	if err != nil {
		t.Fatalf("两根皆缺应返回空不报错，got err=%v", err)
	}
	if skills == nil || len(skills) != 0 {
		t.Fatalf("两根皆缺应返回非 nil 空切片，got %#v", skills)
	}
}

func TestList_DuplicateNamePrefersEnabledRoot(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "dup", "---\nname: EnabledDup\n---\n")
	writeSkill(t, s.disabledRoot(), "dup", "---\nname: DisabledDup\n---\n")
	skills := mustList(t, s)
	if len(skills) != 1 {
		t.Fatalf("同名两根只列启用根条目，got %d 条", len(skills))
	}
	if !skills[0].Enabled || skills[0].DisplayName != "EnabledDup" {
		t.Fatalf("应列启用根条目，got %+v", skills[0])
	}
}

// ---- frontmatter（经 List/Detail 驱动） ----

func TestMetadata_HappyPath(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "full", "---\nname: 展示名\ndescription: 技能描述\nmetadata:\n  version: 1.2.3\n  author: someone\n---\n正文")
	sk, err := s.Detail("full")
	if err != nil {
		t.Fatal(err)
	}
	if !sk.HasSkillMD || sk.DisplayName != "展示名" || sk.Description != "技能描述" ||
		sk.Version != "1.2.3" || sk.Author != "someone" {
		t.Fatalf("frontmatter 解析 = %+v", sk)
	}
}

func TestMetadata_MissingSkillMD_FallsBackToDirName(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "bare", "")
	sk, err := s.Detail("bare")
	if err != nil {
		t.Fatal(err)
	}
	if sk.HasSkillMD {
		t.Fatal("SKILL.md 缺失时 HasSkillMD 应为 false")
	}
	if sk.DisplayName != "bare" || sk.Description != "" || sk.Version != "" || sk.Author != "" {
		t.Fatalf("元数据应回退（DisplayName=目录名、其余空串），got %+v", sk)
	}
}

func TestMetadata_NoFrontmatterButFilePresent(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "plain", "只是一段正文，没有 frontmatter。\n")
	sk, err := s.Detail("plain")
	if err != nil {
		t.Fatal(err)
	}
	if !sk.HasSkillMD || sk.DisplayName != "plain" {
		t.Fatalf("文件在但无 frontmatter：HasSkillMD=true + 回退，got %+v", sk)
	}
}

func TestMetadata_BadYAMLFallsBackWithoutError(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "badyaml", "---\nname: [未闭合\n---\n正文")
	sk, err := s.Detail("badyaml")
	if err != nil {
		t.Fatalf("坏 YAML 不应向用户报错，got %v", err)
	}
	if !sk.HasSkillMD || sk.DisplayName != "badyaml" {
		t.Fatalf("坏 YAML：HasSkillMD=true + 元数据回退，got %+v", sk)
	}
}

func TestMetadata_TruncatedOverLimitTreatedAsMissing(t *testing.T) {
	s := newFixture(t)
	// 首行 `---` 后接超过 64KB 的正文，闭合 `---` 落在读取上限之外——
	// 截断后 frontmatter 不完整，按缺失处理，不 panic。
	body := "---\nname: ShouldNotAppear\n" + strings.Repeat("x", 70*1024) + "\n---\n正文"
	dir := writeSkill(t, s.SkillsRoot(), "huge", body)
	if fi, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil || fi.Size() <= skillMDLimit {
		t.Fatalf("夹具应大于读取上限，size=%v err=%v", fi, err)
	}
	sk, err := s.Detail("huge")
	if err != nil {
		t.Fatal(err)
	}
	if sk.DisplayName != "huge" || sk.HasSkillMD != true {
		t.Fatalf("截断 frontmatter 按缺失处理（HasSkillMD=true、回退元数据），got %+v", sk)
	}
}

// ---- Detail / SkillDir ----

func TestDetail_NotFoundAndInvalidNames(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "real", "")
	for _, name := range []string{"nope", "../x", "a/b", ".", "", ".hidden"} {
		if _, err := s.Detail(name); err == nil {
			t.Fatalf("Detail(%q) 应报错", name)
		}
	}
	if sk, err := s.Detail("real"); err != nil || sk.Name != "real" {
		t.Fatalf("Detail(real) = %+v, %v", sk, err)
	}
}

func TestSkillDir_ResolvesBothRoots(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "on", "")
	writeSkill(t, s.disabledRoot(), "off", "")
	if dir, err := s.SkillDir("on"); err != nil || dir != filepath.Join(s.SkillsRoot(), "on") {
		t.Fatalf("SkillDir(on) = %q, %v", dir, err)
	}
	if dir, err := s.SkillDir("off"); err != nil || dir != filepath.Join(s.disabledRoot(), "off") {
		t.Fatalf("SkillDir(off) = %q, %v", dir, err)
	}
	if _, err := s.SkillDir("missing"); err == nil {
		t.Fatal("SkillDir(missing) 应报错")
	}
}

// ---- SetEnabled ----

func TestSetEnabled_RoundTripMovesDirectory(t *testing.T) {
	s := newFixture(t)
	src := writeSkill(t, s.SkillsRoot(), "mover", "---\nname: Mover\n---\n正文")

	sk, err := s.SetEnabled("mover", false)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Enabled || sk.Path != filepath.Join(s.disabledRoot(), "mover") || sk.DisplayName != "Mover" {
		t.Fatalf("停用后视图 = %+v", sk)
	}
	if _, err := os.Stat(filepath.Join(s.disabledRoot(), "mover", "SKILL.md")); err != nil {
		t.Fatalf("停用后目标目录应含原文件：%v", err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("停用后源目录应不存在，got err=%v", err)
	}

	sk, err = s.SetEnabled("mover", true)
	if err != nil {
		t.Fatal(err)
	}
	if !sk.Enabled || sk.Path != filepath.Join(s.SkillsRoot(), "mover") {
		t.Fatalf("重新启用后视图 = %+v", sk)
	}
	// 已处于目标状态：幂等返回当前视图，不报错。
	sk, err = s.SetEnabled("mover", true)
	if err != nil || !sk.Enabled {
		t.Fatalf("同态幂等 = %+v, %v", sk, err)
	}
}

func TestSetEnabled_TargetExistsRefused(t *testing.T) {
	s := newFixture(t)
	src := writeSkill(t, s.SkillsRoot(), "clash", "---\nname: Clash\n---\n")
	writeSkill(t, s.disabledRoot(), "clash", "")

	_, err := s.SetEnabled("clash", false)
	if err == nil {
		t.Fatal("目标同名目录已存在应报错")
	}
	if !strings.Contains(err.Error(), "目标目录已存在") || !strings.Contains(err.Error(), filepath.Join(s.disabledRoot(), "clash")) {
		t.Fatalf("错误应含目标路径与中文指引，got %v", err)
	}
	// 源目录不动。
	if _, serr := os.Lstat(src); serr != nil {
		t.Fatalf("拒绝后源目录应原样保留：%v", serr)
	}
}

func TestSetEnabled_RejectsPathTraversalNames(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "victim", "")
	for _, name := range []string{"../x", "a/b", `a\b`, ".", "..", "", ".hidden"} {
		if _, err := s.SetEnabled(name, false); err == nil {
			t.Fatalf("SetEnabled(%q) 应拒绝", name)
		} else if !strings.Contains(err.Error(), "无效的技能名") {
			t.Fatalf("SetEnabled(%q) 错误应用户可读，got %v", name, err)
		}
	}
	// victim 未被任何一次拒绝调用触碰。
	if _, err := os.Lstat(filepath.Join(s.SkillsRoot(), "victim")); err != nil {
		t.Fatal(err)
	}
}

// linkDirOrSkip 建一个指向 target 的目录链接：优先 os.Symlink，Windows 无
// 特权时回退 junction（mklink /J）；两者都不可用则跳过用例。
func linkDirOrSkip(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		out, jerr := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if jerr == nil {
			return
		}
		t.Skipf("无法创建目录链接（symlink: %v；junction: %v: %s）", err, jerr, out)
	}
	t.Skipf("无法创建目录链接：%v", err)
}

func TestSetEnabled_RejectsSymlinkedSkillDir(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "victim", "---\nname: Victim\n---\n")
	linkDirOrSkip(t, filepath.Join(s.SkillsRoot(), "victim"), filepath.Join(s.SkillsRoot(), "linked"))

	for _, op := range []func() error{
		func() error { _, err := s.SetEnabled("linked", false); return err },
		func() error { return s.Delete("linked") },
		func() error { _, err := s.SkillDir("linked"); return err },
	} {
		if err := op(); err == nil {
			t.Fatal("符号链接技能目录应被拒绝")
		} else if !strings.Contains(err.Error(), "不是普通目录") {
			t.Fatalf("错误应说明链接拒绝，got %v", err)
		}
	}
	// 链接与目标都未被动过。
	if _, err := os.Lstat(filepath.Join(s.SkillsRoot(), "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(s.SkillsRoot(), "victim", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSetEnabled_SourceMissingErrors(t *testing.T) {
	s := newFixture(t)
	if _, err := s.SetEnabled("ghost", false); err == nil {
		t.Fatal("源目录缺失应报错")
	}
}

func TestSetEnabled_MkdirAllFailureLeavesSourceUntouched(t *testing.T) {
	s := newFixture(t)
	src := writeSkill(t, s.SkillsRoot(), "stuck", "")
	// 把禁用根占成一个普通文件 → MkdirAll 必然失败（此时无任何移动发生）。
	if err := os.WriteFile(s.disabledRoot(), []byte("占位文件"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.SetEnabled("stuck", false)
	if err == nil {
		t.Fatal("MkdirAll 失败应报错")
	}
	if !strings.Contains(err.Error(), "目录失败") {
		t.Fatalf("错误应指明目录创建/检查失败，got %v", err)
	}
	if _, serr := os.Lstat(src); serr != nil {
		t.Fatalf("失败后源目录应原样保留：%v", serr)
	}
}

func TestSetEnabled_RenameFailureRollsBackCreatedRoot(t *testing.T) {
	s := newFixture(t)
	src := writeSkill(t, s.SkillsRoot(), "locked", "---\nname: L\n---\n")

	// 注入 rename 失败（var hook，同 logging.reportWriteError 先例）。
	orig := renameDir
	renameDir = func(oldname, newname string) error {
		return fmt.Errorf("注入的移动失败（%s → %s）", oldname, newname)
	}
	t.Cleanup(func() { renameDir = orig })

	_, err := s.SetEnabled("locked", false)
	if err == nil {
		t.Fatal("移动失败应报错")
	}
	if !strings.Contains(err.Error(), "移动技能目录失败") ||
		!strings.Contains(err.Error(), src) ||
		!strings.Contains(err.Error(), filepath.Join(s.disabledRoot(), "locked")) {
		t.Fatalf("错误应含 src/dst 与底层错误，got %v", err)
	}
	// 源目录未动。
	if _, serr := os.Stat(filepath.Join(src, "SKILL.md")); serr != nil {
		t.Fatalf("移动失败后源文件应原样保留：%v", serr)
	}
	// 回滚：本次新建的禁用根（空目录）应被尽力删除。
	if _, rerr := os.Lstat(s.disabledRoot()); !os.IsNotExist(rerr) {
		t.Fatalf("回滚后新建的禁用根应被删除，got err=%v", rerr)
	}
}

// ---- Delete ----

func TestDelete_RemovesFromBothRootsAndListDropsEntry(t *testing.T) {
	s := newFixture(t)
	writeSkill(t, s.SkillsRoot(), "goner", "---\nname: G\n---\n")
	writeSkill(t, s.disabledRoot(), "gone2", "")

	if err := s.Delete("goner"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("gone2"); err != nil {
		t.Fatal(err)
	}
	for _, sk := range mustList(t, s) {
		if sk.Name == "goner" || sk.Name == "gone2" {
			t.Fatalf("删除后 List 不应再出现：%+v", sk)
		}
	}
	if err := s.Delete("goner"); err == nil {
		t.Fatal("删除不存在的技能应报错")
	}
	if err := s.Delete("../escape"); err == nil {
		t.Fatal("穿越名应被拒绝")
	}
}

// ---- managed 常量冻结 ----

func TestManagedSkillNameMatchesAmagiCodebox(t *testing.T) {
	if ManagedSkillName != "amagi-codebox" {
		t.Fatalf("ManagedSkillName = %q, want amagi-codebox", ManagedSkillName)
	}
	// managed 技能在包内不拦截：SetEnabled/Delete 对其与其他名字一视同仁
	// （拦截在 App 门面层，spec §2.4/§2.8）。
	s := newFixture(t)
	dir := writeSkill(t, s.SkillsRoot(), ManagedSkillName, "")
	if _, err := s.SetEnabled(ManagedSkillName, false); err != nil {
		t.Fatalf("包内对 managed 名不做拦截：%v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.disabledRoot(), ManagedSkillName)); err != nil {
		t.Fatalf("managed 目录应已被移动到禁用根：%v（%s）", err, dir)
	}
}
