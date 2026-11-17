package wslsupply

// tree_primitives_test.go — 树单元共享原语的独立回归（R1：原随 skills_test.go
// 整文件删除而流失的唯一覆盖，按审核意见移植；这些原语仍服务 claude/codex
// 资源单元）。来源：原 skills_test.go 四个原语级用例，语义不变。

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestTreeScanDirectoryLinkNotRecursed（F1 扫描侧）：目录形态符号链接绝不递归
// ——树外内容不入清单，整目录判不兼容。
func TestTreeScanDirectoryLinkNotRecursed(t *testing.T) {
	root := makeSkillRoot(t)
	outside := t.TempDir() // 树外内容：绝不入清单
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sub", "secret.md"), []byte("tree-external"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkillFile(t, root, "hostile", "SKILL.md", "# doc\n")

	made := false
	// Unix/支持符号链接的 Windows：目录符号链接。
	if err := os.Symlink(outside, filepath.Join(root, "hostile", "dirlink")); err == nil {
		made = true
	} else if runtime.GOOS == "windows" {
		// Windows junction（无需特权；mklink /J 目标为绝对路径）。
		link := filepath.Join(root, "hostile", "junction")
		cmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
		if out, jErr := cmd.CombinedOutput(); jErr == nil {
			made = true
		} else {
			t.Logf("junction creation failed too (%v: %s); skipping link form", jErr, out)
		}
	} else {
		t.Skipf("symlink unsupported: %v", err)
	}
	if !made && runtime.GOOS != "windows" {
		t.Skip("no directory link form available")
	}

	m := scanSkillDir(root, "hostile", DefaultSkillsScanLimits(), nil)
	if m.Portable() {
		t.Fatalf("dir-shaped link must make tree non-portable: %+v", m)
	}
	for _, f := range m.Files {
		if strings.HasPrefix(f.RelPath, "dirlink/") || strings.HasPrefix(f.RelPath, "junction/") {
			t.Fatalf("directory link must not be recursed into manifest: %+v", f)
		}
	}
	codes := incompatCodes(m)
	// 安全谓词：目录形态链接绝不递归、整条目判不兼容（dir-link /
	// absolute-link / non-regular-entry 任一——junction 在不同 Go 版本的类型
	// 位映射不同，但三者均阻断供给）。若三者皆无，树外内容会静默入清单。
	if _, ok := codes[SkillIncompatDirLink]; !ok {
		if _, ok2 := codes[SkillIncompatAbsoluteLink]; !ok2 {
			if _, ok3 := codes[SkillIncompatNonRegularFile]; !ok3 {
				t.Fatalf("expected dir-link/absolute-link/non-regular incompat, got %+v", codes)
			}
		}
	}
}

// TestTreeTargetDirLinkEntryNotRecursed（F1 目标侧）：目标树内的目录符号链接
// 按链接条目入 hash，不递归。
func TestTreeTargetDirLinkEntryNotRecursed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows dir-symlink needs privilege; junction form covered by scanner test")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "extra.md"), []byte("tree-external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "dirlink")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	st := scanTargetSkillDir(dir, DefaultSkillsScanLimits())
	if st.Unverifiable || st.FileCount != 2 { // SKILL.md + dirlink 链接条目，不含 extra.md
		t.Fatalf("dir link must hash as single link entry: %+v", st)
	}
	if st.TreeHash == "" {
		t.Fatalf("link entry target must participate in hash: %+v", st)
	}
}

// TestTreeCredentialLinkNameExcluded（F4）：凭据类名单对链接形态同样生效——
// 名为 .env/*.pem 的树内链接按排除处理，不实体化。
func TestTreeCredentialLinkNameExcluded(t *testing.T) {
	root := makeSkillRoot(t)
	writeSkillFile(t, root, "creds", "SKILL.md", "# doc\n")
	writeSkillFile(t, root, "creds", "real.txt", "plain\n")
	if err := os.Symlink("real.txt", filepath.Join(root, "creds", ".env")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink("real.txt", filepath.Join(root, "creds", "cert.pem")); err != nil {
		t.Fatal(err)
	}
	m := scanSkillDir(root, "creds", DefaultSkillsScanLimits(), nil)
	excluded := strings.Join(m.Excluded, "\n")
	if !strings.Contains(excluded, ".env") || !strings.Contains(excluded, "cert.pem") {
		t.Fatalf("credential-named links must be excluded: %+v", m.Excluded)
	}
	for _, f := range m.Files {
		if f.RelPath == ".env" || f.RelPath == "cert.pem" {
			t.Fatalf("excluded link must not be materialized: %+v", f)
		}
	}
	if !m.Portable() {
		t.Fatalf("excluded links must not block portability: %+v", m)
	}
}

// TestTreeCandidateReverifiesSourceIdentity（SA-05）：候选读取前重验路径身份
// ——扫描后来源被替换为指向树外内容的符号链接（即使字节恰好一致）直接拒绝。
func TestTreeCandidateReverifiesSourceIdentity(t *testing.T) {
	root := makeSkillRoot(t)
	writeSkillFile(t, root, "alpha", "SKILL.md", "# alpha\n")
	writeSkillFile(t, root, "alpha", "data.txt", "secret-ish\n")
	m := scanSkillDir(root, "alpha", DefaultSkillsScanLimits(), nil)
	if !m.Portable() {
		t.Fatalf("fixture must be portable: %+v", m)
	}
	// 扫描后把已登记源文件替换为指向树外凭据的符号链接（内容恰好一致也拒绝）。
	outside := filepath.Join(root, "outside-secret")
	if err := os.WriteFile(outside, []byte("secret-ish\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "alpha", "data.txt")
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, victim); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := readSkillCandidateFiles(m, DefaultSkillsScanLimits()); err == nil {
		t.Fatal("candidate read must reject source replaced by symlink (rescan required)")
	}
}
