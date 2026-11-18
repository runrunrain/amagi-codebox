package sharedskills

// import_test.go — zip/本地文件夹导入（canonical 共用技能纵切）用例：
//   - zip：目录包裹/顶层 SKILL.md（名取 zip 文件名）/多技能/非技能目录忽略、
//     路径穿越拒绝（仓库零写入）、链接条目跳过、单文件/文件数上限拒绝；
//   - 文件夹：单技能（自身含 SKILL.md）/子目录扫描/空目录报错/根为链接拒绝、
//     链接子项跳过、超限技能跳过（部分成功）；
//   - 同名保护：启用根/禁用根既有条目不覆盖、托管名保留、批内重名仅首个；
//   - 落点：一律 skills-disabled（默认停用）；全部跳过时不创建禁用根；
//   - rename 失败注入：暂存清理 + 新建根回滚。
// 夹具全部走 t.TempDir，不读写真用户配置，不运行真实 CLI。

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeImportZip 构造测试 zip（entry 名 → 内容）。
func makeImportZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// makeImportZipWithSymlinkEntry 构造含符号链接 entry 的 zip（alpha/SKILL.md
// + alpha/link.md → ../SKILL.md）：链接条目应被跳过而非解出。
func makeImportZipWithSymlinkEntry(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := zip.NewWriter(f)
	entry, err := w.Create("alpha/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("---\nname: Alpha\ndescription: A\n---\n正文")); err != nil {
		t.Fatal(err)
	}
	hdr := &zip.FileHeader{Name: "alpha/link.md"}
	hdr.SetMode(os.ModeSymlink | 0o644)
	linkEntry, err := w.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkEntry.Write([]byte("../SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

const importSampleMD = "---\nname: Sample\ndescription: 样例技能\n---\n\n样例正文\n"

// mustImportZip 断言 zip 导入不整体失败并返回结果。
func mustImportZip(t *testing.T, s *Service, zipPath string) ([]Skill, []string) {
	t.Helper()
	imported, skipped, err := s.ImportFromZip(zipPath)
	if err != nil {
		t.Fatalf("ImportFromZip 整体失败: %v", err)
	}
	return imported, skipped
}

// ---- zip 导入 ----

func TestImportFromZip_LandsDisabledByDefault(t *testing.T) {
	s := newFixture(t)
	zipPath := filepath.Join(t.TempDir(), "demo.zip")
	makeImportZip(t, zipPath, map[string]string{
		"demo/SKILL.md":   importSampleMD,
		"demo/extra.txt":  "附属文件",
		"nested/keep.txt": "非技能目录内容",
	})

	imported, skipped := mustImportZip(t, s, zipPath)
	if len(imported) != 1 || len(skipped) != 0 {
		t.Fatalf("应导入 1 个、无跳过: imported=%+v skipped=%v", imported, skipped)
	}
	sk := imported[0]
	if sk.Name != "demo" || sk.Enabled || sk.DisplayName != "Sample" || sk.Description != "样例技能" {
		t.Fatalf("导入条目应为默认停用且带元数据: %+v", sk)
	}
	if sk.Path != filepath.Join(s.disabledRoot(), "demo") {
		t.Fatalf("落点应在禁用根: %s", sk.Path)
	}
	// 内容完整复制；非技能目录不入库。
	if got, err := os.ReadFile(filepath.Join(s.disabledRoot(), "demo", "SKILL.md")); err != nil || string(got) != importSampleMD {
		t.Fatalf("SKILL.md 内容不一致: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.disabledRoot(), "demo", "extra.txt")); err != nil {
		t.Fatalf("附属文件应复制: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.disabledRoot(), "nested")); !os.IsNotExist(err) {
		t.Fatalf("非技能目录不应入库: %v", err)
	}
	// List 可见（禁用态），启停闭环继续有效。
	list := mustList(t, s)
	if len(list) != 1 || list[0].Name != "demo" || list[0].Enabled {
		t.Fatalf("List 应看到默认停用条目: %+v", list)
	}
	if _, err := s.SetEnabled("demo", true); err != nil {
		t.Fatalf("导入后应可启用: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.SkillsRoot(), "demo", "SKILL.md")); err != nil {
		t.Fatalf("启用后目录应在启用根: %v", err)
	}
}

func TestImportFromZip_TopLevelSkillMDNameFromZipStem(t *testing.T) {
	s := newFixture(t)
	zipPath := filepath.Join(t.TempDir(), "cool-skill.zip")
	makeImportZip(t, zipPath, map[string]string{"SKILL.md": importSampleMD})

	imported, _ := mustImportZip(t, s, zipPath)
	if len(imported) != 1 || imported[0].Name != "cool-skill" {
		t.Fatalf("顶层 SKILL.md 应以 zip 文件名为目录名: %+v", imported)
	}
	if _, err := os.Stat(filepath.Join(s.disabledRoot(), "cool-skill", "SKILL.md")); err != nil {
		t.Fatalf("应落盘 cool-skill: %v", err)
	}
}

func TestImportFromZip_MultipleSkills(t *testing.T) {
	s := newFixture(t)
	zipPath := filepath.Join(t.TempDir(), "multi.zip")
	makeImportZip(t, zipPath, map[string]string{
		"alpha/SKILL.md": "---\nname: Alpha\ndescription: A\n---\n正文",
		"beta/SKILL.md":  "---\nname: Beta\ndescription: B\n---\n正文",
		"nested/x.txt":   "非技能目录",
	})

	imported, skipped := mustImportZip(t, s, zipPath)
	if len(imported) != 2 || len(skipped) != 0 {
		t.Fatalf("应导入 alpha/beta: imported=%+v skipped=%v", imported, skipped)
	}
	if imported[0].Name != "alpha" || imported[1].Name != "beta" {
		t.Fatalf("导入名与顺序不符: %+v", imported)
	}
	if _, err := os.Stat(filepath.Join(s.disabledRoot(), "nested")); !os.IsNotExist(err) {
		t.Fatalf("非技能目录不应入库: %v", err)
	}
}

func TestImportFromZip_PathTraversalRejected(t *testing.T) {
	attacks := map[string]map[string]string{
		"../ 穿越": {"../evil.txt": "boom"},
		"嵌套 ../": {"good/SKILL.md": importSampleMD, "good/../../evil.txt": "boom"},
		"绝对路径":   {"/tmp/evil-abs.txt": "boom"},
		"盘符路径":   {`C:\evil.txt`: "boom"},
	}
	for label, files := range attacks {
		t.Run(label, func(t *testing.T) {
			s := newFixture(t)
			zipPath := filepath.Join(t.TempDir(), "attack.zip")
			makeImportZip(t, zipPath, files)

			imported, _, err := s.ImportFromZip(zipPath)
			if err == nil {
				t.Fatalf("期望拒绝导入，实际成功: %v", imported)
			}
			// 拒绝时零写入：禁用根不应被创建（或保持为空）。
			if entries, rerr := os.ReadDir(s.disabledRoot()); rerr == nil && len(entries) > 0 {
				t.Fatalf("禁用根不应有内容，实际 %d 项", len(entries))
			}
		})
	}
}

func TestImportFromZip_SymlinkEntrySkipped(t *testing.T) {
	s := newFixture(t)
	zipPath := filepath.Join(t.TempDir(), "link.zip")
	makeImportZipWithSymlinkEntry(t, zipPath)

	imported, _ := mustImportZip(t, s, zipPath)
	if len(imported) != 1 || imported[0].Name != "alpha" {
		t.Fatalf("链接条目应跳过、alpha 正常导入: %+v", imported)
	}
	if _, err := os.Lstat(filepath.Join(s.disabledRoot(), "alpha", "link.md")); !os.IsNotExist(err) {
		t.Fatalf("链接条目不应被解出: %v", err)
	}
}

func TestImportFromZip_OversizeFileRejected(t *testing.T) {
	s := newFixture(t)
	big := strings.Repeat("a", 10<<20+1)
	zipPath := filepath.Join(t.TempDir(), "big.zip")
	makeImportZip(t, zipPath, map[string]string{"alpha/SKILL.md": importSampleMD, "alpha/blob.bin": big})

	if _, _, err := s.ImportFromZip(zipPath); err == nil {
		t.Fatal("超单文件上限应拒绝整个导入")
	}
	if _, err := os.Stat(s.disabledRoot()); !os.IsNotExist(err) {
		t.Fatalf("拒绝时不应创建禁用根: %v", err)
	}
}

func TestImportFromZip_OversizeSkillMDRejected(t *testing.T) {
	s := newFixture(t)
	zipPath := filepath.Join(t.TempDir(), "bigmd.zip")
	makeImportZip(t, zipPath, map[string]string{"alpha/SKILL.md": strings.Repeat("a", 1<<20+1)})

	if _, _, err := s.ImportFromZip(zipPath); err == nil {
		t.Fatal("SKILL.md 超 1MB 上限应拒绝整个导入")
	}
}

func TestImportFromZip_TooManyFilesRejected(t *testing.T) {
	s := newFixture(t)
	files := map[string]string{"alpha/SKILL.md": importSampleMD}
	for i := 0; i < importMaxFileCount; i++ {
		files[filepath.Join("alpha", "filler", "f"+strings.Repeat("0", 5-len(digits(i)))+digits(i)+".txt")] = "x"
	}
	zipPath := filepath.Join(t.TempDir(), "many.zip")
	makeImportZip(t, zipPath, files)

	if _, _, err := s.ImportFromZip(zipPath); err == nil {
		t.Fatal("超文件数上限应拒绝整个导入")
	}
}

func digits(n int) string {
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

// ---- 文件夹导入 ----

func TestImportFromDir_SingleSkillDir(t *testing.T) {
	s := newFixture(t)
	src := t.TempDir()
	dir := filepath.Join(src, "solo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(importSampleMD), 0o644); err != nil {
		t.Fatal(err)
	}

	imported, skipped, err := s.ImportFromDir(dir)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("单技能导入应成功: err=%v skipped=%v", err, skipped)
	}
	if len(imported) != 1 || imported[0].Name != "solo" || imported[0].Enabled {
		t.Fatalf("应以目录名导入且默认停用: %+v", imported)
	}
	if _, serr := os.Stat(filepath.Join(s.disabledRoot(), "solo", "SKILL.md")); serr != nil {
		t.Fatalf("应落盘禁用根: %v", serr)
	}
}

func TestImportFromDir_Subdirectories(t *testing.T) {
	s := newFixture(t)
	src := t.TempDir()
	writeSkill(t, src, "one", "---\nname: One\ndescription: 一\n---\n正文")
	writeSkill(t, src, "two", "---\nname: Two\ndescription: 二\n---\n正文")
	if err := os.MkdirAll(filepath.Join(src, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	imported, skipped, err := s.ImportFromDir(src)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("子目录导入应成功: err=%v skipped=%v", err, skipped)
	}
	if len(imported) != 2 || imported[0].Name != "one" || imported[1].Name != "two" {
		t.Fatalf("应导入 one/two: %+v", imported)
	}
	if _, serr := os.Stat(filepath.Join(s.disabledRoot(), "not-a-skill")); !os.IsNotExist(serr) {
		t.Fatalf("非技能目录不应入库: %v", serr)
	}
}

func TestImportFromDir_EmptyDirErrors(t *testing.T) {
	s := newFixture(t)
	if _, _, err := s.ImportFromDir(t.TempDir()); err == nil {
		t.Fatal("无 SKILL.md 的目录应报错")
	}
}

func TestImportFromDir_RootSymlinkRejected(t *testing.T) {
	s := newFixture(t)
	target := t.TempDir()
	writeSkill(t, target, "inner", importSampleMD)
	link := filepath.Join(t.TempDir(), "link-root")
	linkDirOrSkip(t, target, link)

	if _, _, err := s.ImportFromDir(link); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("导入根为链接应拒绝: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(s.disabledRoot(), "inner")); !os.IsNotExist(serr) {
		t.Fatalf("拒绝时不应写入: %v", serr)
	}
}

func TestImportFromDir_SymlinkedEntriesSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接夹具在 Windows 无特权时不可用")
	}
	s := newFixture(t)
	src := t.TempDir()
	dir := writeSkill(t, src, "with-links", importSampleMD)
	if err := os.Symlink(filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "linked.md")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(src, "elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(src, "elsewhere"), filepath.Join(dir, "linked-dir")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}

	imported, skipped, err := s.ImportFromDir(dir)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("链接子项应跳过不报错: err=%v skipped=%v", err, skipped)
	}
	if len(imported) != 1 || imported[0].Name != "with-links" {
		t.Fatalf("应导入 with-links: %+v", imported)
	}
	dst := filepath.Join(s.disabledRoot(), "with-links")
	for _, name := range []string{"linked.md", "linked-dir"} {
		if _, lerr := os.Lstat(filepath.Join(dst, name)); !os.IsNotExist(lerr) {
			t.Fatalf("链接子项 %s 不应复制: %v", name, lerr)
		}
	}
}

func TestImportFromDir_OversizeSkillSkippedPartialSuccess(t *testing.T) {
	s := newFixture(t)
	src := t.TempDir()
	good := writeSkill(t, src, "good", importSampleMD)
	_ = good
	big := writeSkill(t, src, "big", importSampleMD)
	if err := os.WriteFile(filepath.Join(big, "blob.bin"), []byte(strings.Repeat("a", 10<<20+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	imported, skipped, err := s.ImportFromDir(src)
	if err != nil {
		t.Fatalf("部分成功不应返回 error: %v", err)
	}
	if len(imported) != 1 || imported[0].Name != "good" {
		t.Fatalf("good 应导入: %+v", imported)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "big") || !strings.Contains(skipped[0], "上限") {
		t.Fatalf("big 应以超限原因跳过: %v", skipped)
	}
	if _, serr := os.Stat(filepath.Join(s.disabledRoot(), "big")); !os.IsNotExist(serr) {
		t.Fatalf("超限技能不应落盘: %v", serr)
	}
}

// ---- 同名保护 ----

func TestImport_NoOverwriteExistingEntries(t *testing.T) {
	s := newFixture(t)
	enabledMarker := "---\nname: EnabledDup\ndescription: 启用根既有\n---\n正文"
	enabled := writeSkill(t, s.SkillsRoot(), "dup", enabledMarker)
	disabledMarker := "---\nname: DisabledDup\ndescription: 禁用根既有\n---\n正文"
	disabled := writeSkill(t, s.disabledRoot(), "taken", disabledMarker)

	zipPath := filepath.Join(t.TempDir(), "dup.zip")
	makeImportZip(t, zipPath, map[string]string{
		"dup/SKILL.md":   "---\nname: NewDup\ndescription: 想覆盖\n---\n正文",
		"taken/SKILL.md": "---\nname: NewTaken\ndescription: 想覆盖\n---\n正文",
		"fresh/SKILL.md": importSampleMD,
	})

	imported, skipped := mustImportZip(t, s, zipPath)
	if len(imported) != 1 || imported[0].Name != "fresh" {
		t.Fatalf("仅 fresh 应导入: %+v", imported)
	}
	if len(skipped) != 2 {
		t.Fatalf("dup/taken 应跳过: %v", skipped)
	}
	for _, sk := range skipped {
		if !strings.Contains(sk, "拒绝覆盖") {
			t.Fatalf("跳过原因应为拒绝覆盖: %v", skipped)
		}
	}
	// 既有目录内容原样保留（未被覆盖）。
	if got, rerr := os.ReadFile(filepath.Join(enabled, "SKILL.md")); rerr != nil || string(got) != enabledMarker {
		t.Fatalf("启用根条目被改动: err=%v", rerr)
	}
	if got, rerr := os.ReadFile(filepath.Join(disabled, "SKILL.md")); rerr != nil || string(got) != disabledMarker {
		t.Fatalf("禁用根条目被改动: err=%v", rerr)
	}
}

func TestImport_ManagedNameReserved(t *testing.T) {
	s := newFixture(t)
	src := t.TempDir()
	writeSkill(t, src, ManagedSkillName, importSampleMD)
	writeSkill(t, src, "normal", importSampleMD)

	imported, skipped, err := s.ImportFromDir(src)
	if err != nil {
		t.Fatalf("托管名跳过属部分成功，不应整体失败: %v", err)
	}
	if len(imported) != 1 || imported[0].Name != "normal" {
		t.Fatalf("normal 应导入: %+v", imported)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], ManagedSkillName) || !strings.Contains(skipped[0], "托管") {
		t.Fatalf("托管名应以明确原因跳过: %v", skipped)
	}
	if _, serr := os.Stat(filepath.Join(s.disabledRoot(), ManagedSkillName)); !os.IsNotExist(serr) {
		t.Fatalf("托管名不应落盘: %v", serr)
	}
}

func TestImport_BatchDuplicateNamesOnlyFirstWins(t *testing.T) {
	s := newFixture(t)
	src := t.TempDir()
	// 两个不同父目录下同名子目录 b：BFS 各自命中 → 批内重名，仅首个导入。
	writeSkill(t, filepath.Join(src, "a"), "b", "---\nname: B1\ndescription: 第一个\n---\n正文")
	writeSkill(t, filepath.Join(src, "c"), "b", "---\nname: B2\ndescription: 第二个\n---\n正文")

	imported, skipped, err := s.ImportFromDir(src)
	if err != nil {
		t.Fatalf("批内重名属部分成功: %v", err)
	}
	if len(imported) != 1 || imported[0].Name != "b" {
		t.Fatalf("仅首个 b 应导入: %+v", imported)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "重名") {
		t.Fatalf("第二个 b 应以重名跳过: %v", skipped)
	}
}

// ---- 市场凭据剥离（steer #1）：手动导入不得带入 CodeBox 私有市场元数据 ----

func TestImport_StripsMarketProvenanceMarker(t *testing.T) {
	const marker = ".amagi-market-provenance.json"
	s := newFixture(t)
	// zip 路径：技能目录携市场凭据（含嵌套深度一份）→ 均不落盘。
	zipPath := filepath.Join(t.TempDir(), "marked.zip")
	makeImportZip(t, zipPath, map[string]string{
		"demo/SKILL.md":      importSampleMD,
		"demo/" + marker:     `{"version":1,"marketplace":"evil","plugin":"p","skill":"demo"}`,
		"demo/sub/" + marker: `{"version":1}`,
	})
	imported, _ := mustImportZip(t, s, zipPath)
	if len(imported) != 1 || imported[0].Name != "demo" {
		t.Fatalf("技能本身应正常导入: %+v", imported)
	}
	for _, p := range []string{
		filepath.Join(s.disabledRoot(), "demo", marker),
		filepath.Join(s.disabledRoot(), "demo", "sub", marker),
	} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("市场凭据不应被导入: %s（err=%v）", p, err)
		}
	}

	// 目录导入路径：源目录携市场凭据 → 不复制。
	s2 := newFixture(t)
	src := t.TempDir()
	dir := writeSkill(t, src, "from-dir", importSampleMD)
	if err := os.WriteFile(filepath.Join(dir, marker), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	imported2, skipped2, err := s2.ImportFromDir(dir)
	if err != nil || len(skipped2) != 0 {
		t.Fatalf("目录导入应成功: err=%v skipped=%v", err, skipped2)
	}
	if len(imported2) != 1 || imported2[0].Name != "from-dir" {
		t.Fatalf("from-dir 应导入: %+v", imported2)
	}
	if _, err := os.Lstat(filepath.Join(s2.disabledRoot(), "from-dir", marker)); !os.IsNotExist(err) {
		t.Fatalf("市场凭据不应被复制（目录导入）: %v", err)
	}
	// 源文件保留不动（剥离只作用于导入副本）。
	if _, err := os.Lstat(filepath.Join(dir, marker)); err != nil {
		t.Fatalf("源目录的市场凭据不应被删除: %v", err)
	}
}

// ---- 失败路径与回滚 ----

func TestImport_AllSkippedRootNotCreated(t *testing.T) {
	s := newFixture(t)
	src := t.TempDir()
	writeSkill(t, src, "dup-name", importSampleMD)
	writeSkill(t, s.SkillsRoot(), "dup-name", importSampleMD) // 预占同名

	imported, skipped, err := s.ImportFromDir(src)
	if err == nil {
		t.Fatal("全部跳过应返回 error（整体失败）")
	}
	if imported != nil || len(skipped) != 1 {
		t.Fatalf("应返回空 imported + 跳过明细: imported=%v skipped=%v", imported, skipped)
	}
	if _, serr := os.Stat(s.disabledRoot()); !os.IsNotExist(serr) {
		t.Fatalf("全部跳过时不应创建禁用根: %v", serr)
	}
}

func TestImport_RenameFailureCleansStagingAndRollsBackRoot(t *testing.T) {
	orig := renameDir
	renameDir = func(oldname, newname string) error { return errors.New("注入的 rename 失败") }
	t.Cleanup(func() { renameDir = orig })

	s := newFixture(t)
	zipPath := filepath.Join(t.TempDir(), "one.zip")
	makeImportZip(t, zipPath, map[string]string{"alpha/SKILL.md": importSampleMD})

	imported, skipped, err := s.ImportFromZip(zipPath)
	if err == nil {
		t.Fatal("rename 失败应整体失败")
	}
	if imported != nil || len(skipped) != 1 || !strings.Contains(skipped[0], "落盘失败") {
		t.Fatalf("失败原因应进入 skipped: imported=%v skipped=%v", imported, skipped)
	}
	// 暂存目录已清理；本次新建的禁用根已回滚删除。
	entries, rerr := os.ReadDir(s.disabledRoot())
	if rerr == nil && len(entries) > 0 {
		t.Fatalf("暂存目录应清理，残留 %d 项", len(entries))
	} else if rerr != nil && !os.IsNotExist(rerr) {
		t.Fatalf("读取禁用根失败: %v", rerr)
	}
	if rerr == nil {
		_ = os.Remove(s.disabledRoot()) // 清理夹具（空根理论上已回滚）
	}
}
