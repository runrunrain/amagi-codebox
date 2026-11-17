package sharedext

// skills_import_test.go — 覆盖清单 1-4：zip 路径穿越攻击、zip 上限、
// 坏 frontmatter 回退、多 skill 导入与同名冲突 skipped。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 覆盖 1：zip 路径穿越攻击（../evil、绝对路径、盘符）→ 拒绝且仓库未写任何文件。
func TestImportSkillZipPathTraversalRejected(t *testing.T) {
	attacks := map[string]map[string]string{
		"../ 穿越": {"../evil.txt": "boom"},
		"嵌套 ../": {"good/SKILL.md": sampleSkillMD, "good/../../evil.txt": "boom"},
		"绝对路径":  {"/tmp/evil-abs.txt": "boom"},
		"盘符路径":   {`C:\evil.txt`: "boom"},
	}
	for label, files := range attacks {
		t.Run(label, func(t *testing.T) {
			env := newTestService(t)
			zipPath := filepath.Join(t.TempDir(), "attack.zip")
			makeZip(t, zipPath, files)

			skills, err := env.svc.ImportSkillFromZip(zipPath)
			if err == nil {
				t.Fatalf("期望拒绝导入，实际成功: %v", skills)
			}
			if len(skills) != 0 {
				t.Fatalf("不应有导入结果: %v", skills)
			}
			// 仓库未写入任何内容：registry 与 skills 目录不存在或为空。
			if entries, rerr := os.ReadDir(env.svc.skillsRepoDir()); rerr == nil && len(entries) > 0 {
				t.Fatalf("仓库不应有内容，实际 %d 项", len(entries))
			}
			if _, rerr := os.Stat(env.svc.registryPath()); !os.IsNotExist(rerr) {
				t.Fatalf("registry.json 不应被写入: %v", rerr)
			}
		})
	}
}

// 覆盖 1 补充：符号链接 entry 跳过（以 mode 标记构造）。
func TestImportSkillZipSymlinkEntrySkipped(t *testing.T) {
	env := newTestService(t)
	zipPath := filepath.Join(t.TempDir(), "link.zip")
	makeZipWithSymlink(t, zipPath)
	skills, err := env.svc.ImportSkillFromZip(zipPath)
	if err != nil {
		t.Fatalf("符号链接 entry 应跳过而非整体失败: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "alpha" {
		t.Fatalf("期望仅导入 alpha: %+v", skills)
	}
}

// 覆盖 2：zip 超单文件 10MB 上限 → 拒绝整个导入。
func TestImportSkillZipOversizeFileRejected(t *testing.T) {
	env := newTestService(t)
	big := strings.Repeat("a", 10<<20+1)
	zipPath := filepath.Join(t.TempDir(), "big.zip")
	makeZip(t, zipPath, map[string]string{"alpha/SKILL.md": sampleSkillMD, "alpha/blob.bin": big})

	if _, err := env.svc.ImportSkillFromZip(zipPath); err == nil {
		t.Fatal("期望超单文件上限被拒绝")
	}
}

// 覆盖 2：zip 超文件数 2000 上限 → 拒绝整个导入。
func TestImportSkillZipTooManyFilesRejected(t *testing.T) {
	env := newTestService(t)
	files := map[string]string{"alpha/SKILL.md": sampleSkillMD}
	for i := 0; i < zipMaxFileCount; i++ {
		files[filepath.Join("alpha", "filler", "f"+strings.Repeat("0", 5-len(digitsOf(i)))+digitsOf(i)+".txt")] = "x"
	}
	zipPath := filepath.Join(t.TempDir(), "many.zip")
	makeZip(t, zipPath, files)

	if _, err := env.svc.ImportSkillFromZip(zipPath); err == nil {
		t.Fatal("期望超文件数上限被拒绝")
	}
}

func digitsOf(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

// 覆盖 3：坏 frontmatter（无闭合 ---、非 map frontmatter）→ name 回退目录名。
func TestImportSkillBadFrontmatterFallsBackToDirName(t *testing.T) {
	cases := map[string]string{
		"无闭合":   "---\nname: fromfm\ndescription: d\n",
		"非 map": "---\n- 列表项\n- 另一项\n---\n\n正文\n",
		"空 name": "---\nname: \"\"\ndescription: d\n---\n\n正文\n",
		"非法 name": "---\nname: ../evil\ndescription: d\n---\n\n正文\n",
	}
	for label, md := range cases {
		t.Run(label, func(t *testing.T) {
			env := newTestService(t)
			src := t.TempDir()
			writeSkillDir(t, src, "dirname-skill", md)

			skills, err := env.svc.ImportSkillFromDir(src)
			if err != nil {
				t.Fatalf("导入失败: %v", err)
			}
			if len(skills) != 1 {
				t.Fatalf("期望 1 个 skill: %+v", skills)
			}
			if skills[0].Name != "dirname-skill" {
				t.Fatalf("name 应回退目录名 dirname-skill，实际 %q", skills[0].Name)
			}
			if skills[0].Description == "" && label != "非 map" {
				t.Logf("description: %q", skills[0].Description)
			}
		})
	}
}

// 覆盖 4：多 skill zip 导入；同名冲突 skipped。
func TestImportSkillZipMultipleAndConflict(t *testing.T) {
	env := newTestService(t)
	zipPath := filepath.Join(t.TempDir(), "multi.zip")
	makeZip(t, zipPath, map[string]string{
		"alpha/SKILL.md":  sampleSkillMD,
		"beta/SKILL.md":   "---\nname: beta\ndescription: B 技能\n---\n\nbeta 正文\n",
		"nested/keep.txt": "附属文件",
	})

	skills, err := env.svc.ImportSkillFromZip(zipPath)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("期望导入 alpha/beta 两个 skill: %+v", skills)
	}
	if skills[0].Name != "alpha" || skills[1].Name != "beta" {
		t.Fatalf("导入名不符: %+v", skills)
	}
	// 仓库目录内容与源一致（SKILL.md + 附属文件）。
	if got := mustReadFile(t, filepath.Join(env.svc.skillsRepoDir(), "alpha", "SKILL.md")); got != sampleSkillMD {
		t.Fatalf("alpha SKILL.md 内容不一致")
	}
	// 非 skill 目录（nested 无 SKILL.md）不入仓库：导入只复制含 SKILL.md 的目录。
	if fileExists(t, filepath.Join(env.svc.skillsRepoDir(), "nested", "keep.txt")) {
		t.Fatal("非 skill 目录不应复制入库")
	}

	// 同名冲突：再次导入含 alpha 的 zip → skipped 且 error 列出跳过项。
	conflictZip := filepath.Join(t.TempDir(), "conflict.zip")
	makeZip(t, conflictZip, map[string]string{
		"alpha/SKILL.md": sampleSkillMD,
		"gamma/SKILL.md": "---\nname: gamma\ndescription: G\n---\n\ng 正文\n",
	})
	skills2, err2 := env.svc.ImportSkillFromZip(conflictZip)
	if err2 == nil {
		t.Fatal("同名冲突应返回聚合错误")
	}
	if !strings.Contains(err2.Error(), "alpha") {
		t.Fatalf("错误应列出跳过项 alpha: %v", err2)
	}
	if len(skills2) != 1 || skills2[0].Name != "gamma" {
		t.Fatalf("gamma 应成功导入: %+v", skills2)
	}
}

// 覆盖 4 补充：一层子目录文件夹导入。
func TestImportSkillFromDirSubdirectories(t *testing.T) {
	env := newTestService(t)
	src := t.TempDir()
	writeSkillDir(t, src, "one", "---\nname: one\ndescription: 一\n---\n\n正文\n")
	writeSkillDir(t, src, "two", "---\nname: two\ndescription: 二\n---\n\n正文\n")
	// 非 skill 目录（无 SKILL.md）被忽略。
	if err := os.MkdirAll(filepath.Join(src, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	skills, err := env.svc.ImportSkillFromDir(src)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(skills) != 2 || skills[0].Name != "one" || skills[1].Name != "two" {
		t.Fatalf("期望导入 one/two: %+v", skills)
	}

	// 空目录 → 明确报错。
	if _, err := env.svc.ImportSkillFromDir(t.TempDir()); err == nil {
		t.Fatal("无 SKILL.md 的目录应报错")
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}
