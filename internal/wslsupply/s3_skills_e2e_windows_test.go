//go:build windows

package wslsupply

// s3_skills_e2e_windows_test.go — 技能供给的真实 WSL 冒烟（P3-S3，AMAGI_WSL_E2E=1
// 手动触发；隔离规约与 s1_e2e_windows_test.go 相同）。
//
// 两段：
//  1. 隔离测试资产（临时目录假 skills + fake Home 重锚 /tmp/amagi-wslsupply-e2e）
//     全链验证：扫描（含相对链接实体化/越界链接不复制/.ps1 不复制/排除物）→
//     同意 → RunUnits 供给 → distro 内 stat 权限实证 + wsl.exe cat 消费者可发现 →
//     增量更新（重写变更目录）→ WSL 手改冻结。
//  2. 真实非破坏性 skill：从真实 Windows ~/.agents/skills 按清单挑一个纯文本
//     可移植 skill，供给到 distro 临时验证位（/tmp/amagi-wslsupply-e2e-real-*），
//     核对消费者可发现（file 存在 + 结构完整），不执行其内容。结束后清理。

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// e2eSkillWslCat 经 wsl.exe 在 distro 内读取目标文件前 64 字节（消费者视角）。
func e2eSkillWslCat(t *testing.T, distro, linuxPath string) string {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-d", distro, "--", "head", "-c", "64", linuxPath).Output()
	if err != nil {
		t.Fatalf("read %s in distro: %v", linuxPath, err)
	}
	return strings.TrimSpace(string(out))
}

func e2eSkillWslStatMode(t *testing.T, distro, linuxPath string) string {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-d", distro, "--", "stat", "-c", "%a", linuxPath).Output()
	if err != nil {
		t.Fatalf("stat %s in distro: %v", linuxPath, err)
	}
	return strings.TrimSpace(strings.Trim(string(out), "\r\n\x00"))
}

// TestManualRealWSLSkillsSupply 隔离资产全链 + 真实 skill 冒烟。
func TestManualRealWSLSkillsSupply(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL S3 skills smoke")
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine")
	}
	_ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", "/tmp/amagi-wslsupply-e2e").Run()

	ctx := context.Background()
	hostRoot := t.TempDir()
	// 隔离资产：可移植 skill（含树内相对链接与排除物）+ 不兼容 skill（.ps1）。
	writeSkillFile(t, hostRoot, "e2e-portable", "SKILL.md", "# e2e portable skill\n")
	writeSkillFile(t, hostRoot, "e2e-portable", "docs/guide.md", "guide body")
	writeSkillFile(t, hostRoot, "e2e-portable", "node_modules/pkg/index.js", "x")
	writeSkillFile(t, hostRoot, "e2e-portable", ".env", "SECRET=1")
	writeSkillFile(t, hostRoot, "e2e-win", "SKILL.md", "# win only\n")
	writeSkillFile(t, hostRoot, "e2e-win", "run.ps1", "echo 1")
	if err := os.Symlink("docs/guide.md", filepath.Join(hostRoot, "e2e-portable", "guide-alias.md")); err != nil {
		// 无符号链接权限的 Windows：跳过链接实体化段（树内相对链接的实体化
		// 行为由跨平台单测钉住）；其余全链继续。
		t.Logf("symlink unsupported on this host (%v); link materialization covered by unit tests", err)
	}

	c := NewCoordinator(t.TempDir(), nil, e2eIsolatedProbes(distro))
	scope := NewSkillsScope(hostRoot, DefaultSkillsScanLimits())
	c.RegisterSkills(scope)
	scope.Scan()

	portable := scope.manifest("e2e-portable")
	if portable == nil || !portable.Portable() {
		t.Fatalf("e2e-portable must be portable: %+v", portable)
	}
	aliasOK := false
	for _, f := range portable.Files {
		if f.RelPath == "guide-alias.md" && f.LinkTarget != "" {
			aliasOK = true
		}
	}
	if _, linkErr := os.Lstat(filepath.Join(hostRoot, "e2e-portable", "guide-alias.md")); linkErr == nil && !aliasOK {
		t.Fatalf("relative link must be materialized in manifest: %+v", portable.Files)
	}
	if m := scope.manifest("e2e-win"); m.Portable() {
		t.Fatalf("e2e-win with .ps1 must not be portable: %+v", m)
	}

	// 供给可移植 skill；不兼容 skill 整体阻断（同意门在前——两者都授权，
	// 验证不兼容判定本身）。
	for _, skill := range []string{"e2e-portable", "e2e-win"} {
		if err := c.GrantUnitConsent(ctx, distro, CLICommon, SkillUnitID(skill)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := c.RunUnits(ctx, distro, CLICommon, ReasonManual, []UnitID{SkillUnitID("e2e-portable"), SkillUnitID("e2e-win")})
	if err != nil {
		t.Fatal(err)
	}
	byUnit := map[string]UnitResult{}
	for _, r := range res.Results {
		byUnit[string(r.Unit)] = r
	}
	if r := byUnit[string(SkillUnitID("e2e-portable"))]; r.Outcome != UnitApplied {
		t.Fatalf("portable supply failed: %+v", r)
	}
	if r := byUnit[string(SkillUnitID("e2e-win"))]; r.Outcome != UnitBlocked || r.Detail != "incompatible-items" {
		t.Fatalf("windows-script skill must block: %+v", r)
	}

	// 消费者可发现：distro 内 file 存在 + 内容前缀一致 + 权限位实证（0644/0755）。
	base := "/tmp/amagi-wslsupply-e2e/.agents/skills"
	if got := e2eSkillWslCat(t, distro, base+"/e2e-portable/SKILL.md"); !strings.HasPrefix(got, "# e2e portable skill") {
		t.Fatalf("SKILL.md must be readable from distro: %q", got)
	}
	if _, linkErr := os.Lstat(filepath.Join(hostRoot, "e2e-portable", "guide-alias.md")); linkErr == nil {
		if got := e2eSkillWslCat(t, distro, base+"/e2e-portable/guide-alias.md"); !strings.Contains(got, "guide body") {
			t.Fatalf("materialized link must carry target content: %q", got)
		}
	}
	if mode := e2eSkillWslStatMode(t, distro, base+"/e2e-portable/SKILL.md"); mode != "644" {
		t.Fatalf("skill file mode must be 644 in distro, got %q", mode)
	}
	if mode := e2eSkillWslStatMode(t, distro, base+"/e2e-portable"); mode != "755" {
		t.Fatalf("skill dir mode must be 755 in distro, got %q", mode)
	}
	// 排除物与不兼容项没有跨界。
	if _, err := exec.Command("wsl.exe", "-d", distro, "--", "test", "-e", base+"/e2e-portable/.env").Output(); err == nil {
		t.Fatal(".env must not be supplied")
	}
	if _, err := exec.Command("wsl.exe", "-d", distro, "--", "test", "-e", base+"/e2e-portable/node_modules").Output(); err == nil {
		t.Fatal("node_modules must not be supplied")
	}
	if _, err := exec.Command("wsl.exe", "-d", distro, "--", "test", "-e", base+"/e2e-win").Output(); err == nil {
		t.Fatal("incompatible skill dir must not exist in distro")
	}

	// 增量更新：源改动 → 仅重写该目录（内容更新）。
	writeSkillFile(t, hostRoot, "e2e-portable", "SKILL.md", "# e2e portable skill v2\n")
	scope.Scan()
	res, err = c.RunUnits(ctx, distro, CLICommon, ReasonManual, []UnitID{SkillUnitID("e2e-portable")})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Results[0]; r.Outcome != UnitApplied {
		t.Fatalf("incremental update failed: %+v", r)
	}
	if got := e2eSkillWslCat(t, distro, base+"/e2e-portable/SKILL.md"); !strings.Contains(got, "v2") {
		t.Fatalf("updated content must reach distro: %q", got)
	}

	// WSL 手改冻结：来源再更新不覆盖本地修改。
	if out, err := exec.Command("wsl.exe", "-d", distro, "--", "sh", "-c",
		fmt.Sprintf("printf 'local edit' > %s/e2e-portable/SKILL.md", base)).Output(); err != nil {
		t.Fatalf("manual edit in distro: %v %s", err, out)
	}
	writeSkillFile(t, hostRoot, "e2e-portable", "SKILL.md", "# e2e portable skill v3\n")
	scope.Scan()
	res, err = c.RunUnits(ctx, distro, CLICommon, ReasonManual, []UnitID{SkillUnitID("e2e-portable")})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Results[0]; r.Outcome != UnitBlocked || (r.Detail != "both-changed-frozen" && r.Detail != "local-modification-frozen") {
		t.Fatalf("manual edit must freeze: %+v", r)
	}
	if got := e2eSkillWslCat(t, distro, base+"/e2e-portable/SKILL.md"); !strings.Contains(got, "local edit") {
		t.Fatalf("local edit must be retained: %q", got)
	}
	t.Logf("isolated full-chain PASS (distro=%s)", distro)

	// ---- 第二段：真实非破坏性 skill 冒烟（临时验证位） ----
	realRoot := os.Getenv("USERPROFILE")
	if realRoot == "" {
		if home, err := os.UserHomeDir(); err == nil {
			realRoot = home
		}
	}
	realSkills := filepath.Join(realRoot, ".agents", "skills")
	entries, err := os.ReadDir(realSkills)
	if err != nil {
		t.Skipf("host skills root unavailable (%v); skipping real-skill smoke", err)
	}
	realScope := NewSkillsScope(realSkills, DefaultSkillsScanLimits())
	realScope.Scan()
	snap := realScope.Snapshot()
	var picked *SkillManifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := realScope.manifest(e.Name())
		if m != nil && m.Portable() {
			picked = m
			break
		}
	}
	if picked == nil {
		t.Skipf("no portable skill found under %s (scanned %d, incomplete=%v)", realSkills, len(snap.Skills), snap.Incomplete())
	}
	t.Logf("real skill smoke: %q（%d 文件，%d 字节，%d 排除项）", picked.Name, len(picked.Files), picked.TotalBytes, len(picked.Excluded))

	verifyDir := fmt.Sprintf("/tmp/amagi-wslsupply-e2e-real-%d", os.Getpid())
	t.Cleanup(func() {
		_, _ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", verifyDir).Output()
	})
	_ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", verifyDir).Run()
	c2 := NewCoordinator(t.TempDir(), nil, Probes{
		// 重锚到临时验证位：Identity 把 Home 指向 /tmp 验证位，ResolveTarget/
		// Apply 复核自然落在同一重锚目标上——绝不写入真实 ~/.agents。
		Identity: func(d string) platform.WSLUserIdentity {
			id := platform.WSLTargetIdentity(d)
			id.Home = verifyDir
			return id
		},
		ToUNC: platform.WSLToUNC,
		Chmod: platform.WSLChmod,
	})
	c2.RegisterSkills(realScope)
	if err := c2.GrantUnitConsent(ctx, distro, CLICommon, SkillUnitID(picked.Name)); err != nil {
		t.Fatal(err)
	}
	res, err = c2.RunUnits(ctx, distro, CLICommon, ReasonManual, []UnitID{SkillUnitID(picked.Name)})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Results[0]; r.Outcome != UnitApplied {
		t.Fatalf("real skill supply failed: %+v", r)
	}
	// 消费者可发现：结构完整（清单逐文件存在）+ SKILL.md 可读。
	linuxSkill := verifyDir + "/.agents/skills/" + picked.Name
	for _, f := range picked.Files {
		if out, err := exec.Command("wsl.exe", "-d", distro, "--", "test", "-f", linuxSkill+"/"+f.RelPath).Output(); err != nil {
			t.Fatalf("manifest file missing in distro: %s (%v %s)", f.RelPath, err, out)
		}
	}
	head := e2eSkillWslCat(t, distro, linuxSkill+"/SKILL.md")
	t.Logf("real skill discoverable: %s/SKILL.md head=%q", linuxSkill, head)
	if mode := e2eSkillWslStatMode(t, distro, linuxSkill); mode != "755" {
		t.Fatalf("real skill dir mode: %q", mode)
	}
	// 清理临时验证位（真机内容不动）。
	if out, err := exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", "/tmp/amagi-wslsupply-e2e").Output(); err != nil {
		t.Logf("cleanup warning: %v %s", err, out)
	}
	t.Logf("real skill smoke PASS")
}
