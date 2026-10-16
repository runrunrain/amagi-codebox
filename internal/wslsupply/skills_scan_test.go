package wslsupply

// skills_scan_test.go — 有界扫描与静态兼容检查的行为钉子（P3-S3）。全部使用
// 本地临时目录（fake 宿主技能根），不触达真实 WSL。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeSkillRoot 构造一个临时宿主技能根并注册到协调器（fake 探测面目标侧
// 落在另一个临时目录）。
func makeSkillScope(t *testing.T) (*SkillsScope, string) {
	t.Helper()
	root := t.TempDir()
	scope := NewSkillsScope(root, DefaultSkillsScanLimits())
	return scope, root
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
	// 越界链接（解析出 skill 树）。
	if err := os.Symlink("../../outside.txt", filepath.Join(root, skill, "out.md")); err != nil {
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

func TestScanSkillCompatChecks(t *testing.T) {
	scope, root := makeSkillScope(t)
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
	_ = scope
}

func TestScanSkillBoundedLimits(t *testing.T) {
	_, root := makeSkillScope(t)
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
	_, root := makeSkillScope(t)
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

func TestSkillsScopeScanSnapshotAndDeletion(t *testing.T) {
	scope, root := makeSkillScope(t)
	writeSkillFile(t, root, "one", "SKILL.md", "one")
	writeSkillFile(t, root, "two", "SKILL.md", "two")
	scope.Scan()
	snap := scope.Snapshot()
	if !snap.Started || snap.Running || len(snap.Skills) != 2 || snap.Progress.Total != 2 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if m := scope.manifest("one"); m == nil || !m.Portable() {
		t.Fatalf("manifest one missing/non-portable: %+v", m)
	}
	// 来源删除后重扫：缓存剔除该 skill。
	if err := os.RemoveAll(filepath.Join(root, "two")); err != nil {
		t.Fatal(err)
	}
	scope.Scan()
	if m := scope.manifest("two"); m != nil {
		t.Fatalf("deleted skill must leave cache: %+v", m)
	}
	// 空目录 skill：不可移植（empty）。
	if err := os.MkdirAll(filepath.Join(root, "empty-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	scope.Scan()
	m := scope.manifest("empty-skill")
	if m == nil || m.NotPortableReason() != "empty" {
		t.Fatalf("empty skill must be non-portable(empty): %+v", m)
	}
}

func TestSkillsScopeStartScanAsyncNonBlocking(t *testing.T) {
	_, root := makeSkillScope(t)
	for i := 0; i < 40; i++ {
		writeSkillFile(t, root, fmt.Sprintf("skill-%02d", i), "SKILL.md", strings.Repeat("x", 200))
	}
	started := NewSkillsScope(root, DefaultSkillsScanLimits())
	// 后台扫描立即返回（不阻塞调用方——启动链/状态面零等待）。
	if !started.StartScanAsync() {
		t.Fatal("first StartScanAsync must start")
	}
	if started.StartScanAsync() {
		t.Fatal("running scan must single-flight (second call must not restart)")
	}
	// 有限等待收敛：扫描最终完成且全部技能入缓存。
	deadline := time.Now().Add(10 * time.Second)
	for {
		snap := started.Snapshot()
		if !snap.Running && snap.Started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not converge: %+v", snap.Progress)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(started.Snapshot().Skills) != 40 {
		t.Fatalf("expected 40 skills, got %d", len(started.Snapshot().Skills))
	}
}

func TestParseSkillUnitValidation(t *testing.T) {
	for _, bad := range []UnitID{"shared/skill/", "shared/skill/a/b", "shared/skill/.hidden", "shared/skill/..", "pi/skill/x"} {
		if _, ok := ParseSkillUnit(bad); ok {
			t.Fatalf("%q must not parse as skill unit", bad)
		}
	}
	if name, ok := ParseSkillUnit("shared/skill/my-skill"); !ok || name != "my-skill" {
		t.Fatalf("valid skill unit parse failed: %q %v", name, ok)
	}
}
