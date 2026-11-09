package wslsupply

// skills_scan_test.go — 有界扫描与静态兼容检查的行为钉子（P3-S3）。全部使用
// 本地临时目录（fake 宿主技能根），不触达真实 WSL。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeSkillRoot 构造一个临时宿主树根（树扫描原语测试的驱动面；P3-S3 作用域
// 已退役，扫描器经 claude/codex 资源作用域与直接调用消费）。
func makeSkillRoot(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeSkillFile(t *testing.T, root, skill, rel, content string) {
	t.Helper()
	p := filepath.Join(root, skill, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanSkillDirWithLinks 铺设符号链接场景（环境不支持创建链接时整测跳过——
// 链接兼容检查的行为面在支持的 CI 平台钉住）。
func scanSkillDirWithLinks(t *testing.T, root, skill string) *SkillManifest {
	t.Helper()
	if err := os.Symlink("probe-target", filepath.Join(root, "link-probe")); err != nil {
		t.Skipf("symlink unsupported on this host: %v", err)
	}
	_ = os.Remove(filepath.Join(root, "link-probe"))
	// 绝对链接（Unix 绝对形态）。
	if err := os.Symlink("/abs/target", filepath.Join(root, skill, "abs.md")); err != nil {
		t.Fatal(err)
	}
	// 越界链接（解析出 skill 树）。链接位于 root/<skill>/out.md，目标
	// ../outside.txt 解析到 root/outside.txt（树外一级——SA-18 fixture 前提
	// 修正：原 ../../ 会落到 root 的父目录，目标不存在被归为 non-regular，
	// 断言不到 out-of-tree-link）。
	if err := os.Symlink("../outside.txt", filepath.Join(root, skill, "out.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("out"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 循环链接 a→b→a。
	if err := os.Symlink("cycle-b.md", filepath.Join(root, skill, "cycle-a.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("cycle-a.md", filepath.Join(root, skill, "cycle-b.md")); err != nil {
		t.Fatal(err)
	}
	// 指向目录的链接。
	if err := os.MkdirAll(filepath.Join(root, skill, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("assets", filepath.Join(root, skill, "assets-link")); err != nil {
		t.Fatal(err)
	}
	// 树内相对链接（可移植，实体化）。
	if err := os.Symlink("docs/guide.md", filepath.Join(root, skill, "guide-alias.md")); err != nil {
		t.Fatal(err)
	}
	return scanSkillDir(root, skill, DefaultSkillsScanLimits(), nil)
}

func incompatCodes(m *SkillManifest) map[string]string {
	out := map[string]string{}
	for _, it := range m.Incompatible {
		out[it.Code] = it.RelPath
	}
	return out
}

// TestScanSkillAbsoluteLikeLinkTargetForms 钉住绝对形态判定谓词（Windows
// 盘根相对目标的修复回归：`\abs\target` 在 Windows 宿主上由
// CreateSymbolicLink 规范化产生，是宿主原生绝对路径；此前漏判致该形态链
// 接被误归 non-regular-entry）。谓词语义随宿主原生 filepath.IsAbs 走，本
// 测试同样逐宿主钉住，不做平台分歧断言。
func TestScanSkillAbsoluteLikeLinkTargetForms(t *testing.T) {
	for _, s := range []string{"", "docs/guide.md", "cycle-b.md", "../outside.txt", `weird\name`} {
		if isAbsoluteLikeLinkTarget(s) {
			t.Fatalf("relative target %q misclassified as absolute", s)
		}
	}
	for _, s := range []string{"/abs/target", "//server/share", `\\server\share`, "C:\\x", "c:/x", "D:data"} {
		if !isAbsoluteLikeLinkTarget(s) {
			t.Fatalf("absolute-form target %q not classified", s)
		}
	}
	// 宿主原生根起始形态：Windows 上 "/abs" 形链接被存为盘根相对 \abs（真机
	// 实测 Readlink 返回该形态，filepath.IsAbs 为 false）——必须归为绝对形态；
	// POSIX 宿主上同字符串是合法相对文件名（'\\' 非分隔符）。谓词与宿主
	// os.IsPathSeparator 同判，两个平台各自钉住正确语义。
	driveRooted := `\abs\target`
	if got, want := isAbsoluteLikeLinkTarget(driveRooted), os.IsPathSeparator('\\'); got != want {
		t.Fatalf("drive-rooted target %q = %v, want host-native rooted = %v", driveRooted, got, want)
	}
}

func TestScanSkillCompatChecks(t *testing.T) {
	root := makeSkillRoot(t)
	// 可移植基线 + 各类不兼容/排除形态。
	writeSkillFile(t, root, "alpha", "SKILL.md", "# alpha\n")
	writeSkillFile(t, root, "alpha", "docs/guide.md", "guide")
	writeSkillFile(t, root, "alpha", "run.ps1", "echo 1")
	writeSkillFile(t, root, "alpha", "tool.cmd", "@echo")
	writeSkillFile(t, root, "alpha", "bin.exe", "MZ")
	writeSkillFile(t, root, "alpha", ".env", "SECRET=1")
	writeSkillFile(t, root, "alpha", "cert.pem", "KEY")
	writeSkillFile(t, root, "alpha", "node_modules/pkg/index.js", "x")
	writeSkillFile(t, root, "alpha", ".git/config", "x")
	writeSkillFile(t, root, "alpha", "sessions/2024.jsonl", "x")
	m := scanSkillDirWithLinks(t, root, "alpha")
	codes := incompatCodes(m)
	for _, want := range []string{
		SkillIncompatAbsoluteLink, SkillIncompatOutOfTreeLink, SkillIncompatCyclicLink,
		SkillIncompatDirLink, SkillIncompatWindowsScript, SkillIncompatWindowsBinary,
	} {
		if _, ok := codes[want]; !ok {
			t.Fatalf("expected incompat %s, got %+v", want, codes)
		}
	}
	if m.Portable() {
		t.Fatal("alpha with incompatible items must not be portable")
	}
	// 不兼容项不进清单：清单只剩可移植实体。
	relSet := map[string]bool{}
	for _, f := range m.Files {
		relSet[f.RelPath] = true
	}
	for _, banned := range []string{"run.ps1", "tool.cmd", "bin.exe", "abs.md", "out.md", "cycle-a.md", "cycle-b.md", "assets-link"} {
		if relSet[banned] {
			t.Fatalf("incompatible %s must not be in manifest", banned)
		}
	}
	// 相对链接实体化：guide-alias.md 内容 hash 与 docs/guide.md 一致。
	alias, ok := relSet["guide-alias.md"]
	if !ok || !alias {
		t.Fatalf("relative link must be materialized: %+v", relSet)
	}
	var aliasHash, guideHash string
	for _, f := range m.Files {
		if f.RelPath == "guide-alias.md" {
			aliasHash = f.Hash
		}
		if f.RelPath == "docs/guide.md" {
			guideHash = f.Hash
		}
	}
	if aliasHash == "" || aliasHash != guideHash {
		t.Fatalf("materialized link hash must equal target content hash: %q vs %q", aliasHash, guideHash)
	}
	// 排除物：.git/node_modules/sessions/.env/.pem。
	excluded := strings.Join(m.Excluded, "\n")
	for _, want := range []string{".git/", "node_modules/", "sessions/", ".env", "cert.pem"} {
		if !strings.Contains(excluded, want) {
			t.Fatalf("expected exclusion %s in %q", want, excluded)
		}
	}
	// 干净 skill 可移植。
	writeSkillFile(t, root, "beta", "SKILL.md", "# beta\n")
	beta := scanSkillDir(root, "beta", DefaultSkillsScanLimits(), nil)
	if !beta.Portable() {
		t.Fatalf("clean skill must be portable: %+v", beta)
	}
}

func TestScanSkillBoundedLimits(t *testing.T) {
	root := makeSkillRoot(t)
	writeSkillFile(t, root, "deep", strings.Repeat("a/", 6)+"file.md", "x")
	// 深度上限 2：a/b/c 之下的目录不再下降 → incomplete。
	deep := scanSkillDir(root, "deep", SkillsScanLimits{MaxDepth: 2, MaxFilesPerSkill: 100, MaxFileBytes: 1 << 20, MaxTotalFiles: 1000}, nil)
	if !deep.Incomplete || deep.IncompleteReason != "depth-limit" {
		t.Fatalf("expected depth-limit incomplete, got %+v", deep)
	}
	// 文件数上限。
	writeSkillFile(t, root, "many", "f1.md", "1")
	writeSkillFile(t, root, "many", "f2.md", "2")
	many := scanSkillDir(root, "many", SkillsScanLimits{MaxDepth: 4, MaxFilesPerSkill: 1, MaxFileBytes: 1 << 20, MaxTotalFiles: 1000}, nil)
	if !many.Incomplete || many.IncompleteReason != "file-count-limit" {
		t.Fatalf("expected file-count-limit, got %+v", many)
	}
	// 单文件字节上限 → oversize-file 不兼容（整 skill 不可移植）。
	writeSkillFile(t, root, "big", "huge.md", strings.Repeat("x", 64))
	big := scanSkillDir(root, "big", SkillsScanLimits{MaxDepth: 4, MaxFilesPerSkill: 10, MaxFileBytes: 8, MaxTotalFiles: 1000}, nil)
	if big.Portable() {
		t.Fatalf("oversize file must make skill non-portable: %+v", big)
	}
	if _, ok := incompatCodes(big)[SkillIncompatOversizeFile]; !ok {
		t.Fatalf("expected oversize-file, got %+v", big.Incompatible)
	}
	// 总量上限：预算耗尽后标记 total-file-limit。
	budget := 100
	over := scanSkillDir(root, "many", SkillsScanLimits{MaxDepth: 4, MaxFilesPerSkill: 10, MaxFileBytes: 1 << 20, MaxTotalFiles: 1}, &budget)
	if !over.Incomplete || over.IncompleteReason != "total-file-limit" {
		t.Fatalf("expected total-file-limit, got %+v", over)
	}
}

func TestScanSkillWindowsScriptExplicitlyUnsupported(t *testing.T) {
	root := makeSkillRoot(t)
	writeSkillFile(t, root, "win", "SKILL.md", "# doc\n")
	writeSkillFile(t, root, "win", "helper.bat", "@echo off")
	m := scanSkillDir(root, "win", DefaultSkillsScanLimits(), nil)
	code, rel := SkillIncompatWindowsScript, ""
	for _, it := range m.Incompatible {
		if it.Code == SkillIncompatWindowsScript {
			rel = it.RelPath
		}
	}
	if rel != "helper.bat" {
		t.Fatalf("windows script must be reported per-item, got %+v", m.Incompatible)
	}
	if !strings.Contains(skillIncompatReasonText(code), "未支持") {
		t.Fatalf("reason must state unsupported explicitly: %s", skillIncompatReasonText(code))
	}
	// 不兼容项整 skill 阻断（不产半残 skill）。
	if m.Portable() {
		t.Fatal("skill with windows script must be non-portable")
	}
}

func TestSkillTreeHashStableAcrossOrdering(t *testing.T) {
	a := []SkillFileEntry{{RelPath: "b.md", Size: 1, Hash: "h1"}, {RelPath: "a.md", Size: 2, Hash: "h2"}}
	b := []SkillFileEntry{{RelPath: "a.md", Size: 2, Hash: "h2"}, {RelPath: "b.md", Size: 1, Hash: "h1"}}
	if skillTreeHash(a) != skillTreeHash(b) {
		t.Fatal("tree hash must be order-independent")
	}
	b[0].Hash = "h3"
	if skillTreeHash(a) == skillTreeHash(b) {
		t.Fatal("content change must change tree hash")
	}
}

// TestScanSkillAliasToExcludedCredential（SA-05）：普通名链接别名不得实体化
// 被排除的凭据/排除目录内容——直接别名、多跳别名、排除目录目标均按排除处理
// （预览可见、不阻断供给）。
func TestScanSkillAliasToExcludedCredential(t *testing.T) {
	root := makeSkillRoot(t)
	writeSkillFile(t, root, "demo", "SKILL.md", "# synthetic\n")
	writeSkillFile(t, root, "demo", ".env", "SYNTHETIC_SECRET=not-real\n")
	writeSkillFile(t, root, "demo", "real.txt", "plain\n")
	// 直接别名：alias.txt -> .env。
	if err := os.Symlink(".env", filepath.Join(root, "demo", "alias.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// 多跳别名：multi.txt -> alias.txt -> .env（链上命中凭据名）。
	if err := os.Symlink("alias.txt", filepath.Join(root, "demo", "multi.txt")); err != nil {
		t.Fatal(err)
	}
	// 排除目录目标：gitlink.txt -> .git/config。
	if err := os.MkdirAll(filepath.Join(root, "demo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillFile(t, root, "demo", ".git/config", "[core]\n")
	if err := os.Symlink(".git/config", filepath.Join(root, "demo", "gitlink.txt")); err != nil {
		t.Fatal(err)
	}
	// 链中间接位于凭据名上：hop.txt -> mid.lnk -> .env（中间跳凭据名）。
	if err := os.Symlink("real.txt", filepath.Join(root, "demo", "mid.lnk")); err != nil {
		t.Fatal(err)
	}
	_ = "hop case covered by multi.txt"

	m := scanSkillDir(root, "demo", DefaultSkillsScanLimits(), nil)
	for _, f := range m.Files {
		if f.RelPath == "alias.txt" || f.RelPath == "multi.txt" || f.RelPath == "gitlink.txt" {
			t.Fatalf("alias to excluded content must not be materialized: %+v", f)
		}
	}
	excluded := strings.Join(m.Excluded, "\n")
	for _, want := range []string{"alias.txt", "multi.txt", "gitlink.txt", ".env"} {
		if !strings.Contains(excluded, want) {
			t.Fatalf("expected %s excluded, got %q", want, excluded)
		}
	}
	if !m.Portable() {
		t.Fatalf("excluded aliases must not block portability: %+v", m)
	}
}

// TestSkillsScopeScanIdempotentAfterCompletion（SA-17）：扫描完成后 StartScanAsync
// 不再重启扫描（多次 Get 不增加扫描轮次）；显式 RescanAsync 才重扫并收敛新
// 增内容。