package wslsupply

// skills_test.go — 技能单元全链行为钉子（P3-S3）：同意门、首次供给、增量
// 更新（只重写变更目录）、WSL 手改冻结、未托管/删除冻结、不兼容阻断、接管
// CAS、条件撤销、停跟随。全部经 fake 探测面（目标侧 = 本地临时目录），不触
// 达真实 WSL。

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newSkillsEnv 构造带技能作用域的协调器（宿主根与目标根都是临时目录）。
func newSkillsEnv(t *testing.T) (*Coordinator, *SkillsScope, string) {
	t.Helper()
	fp := newFakeProbes(t)
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	hostRoot := t.TempDir()
	scope := NewSkillsScope(hostRoot, DefaultSkillsScanLimits())
	c.RegisterSkills(scope)
	scope.Scan() // 同步扫描：测试确定性
	return c, scope, fp.uncRoot
}

// targetSkillPath 返回目标侧 skill 目录的本地（fake UNC）路径。
func targetSkillPath(uncRoot, name string) string {
	return filepath.Join(uncRoot, "home", "u", ".agents", "skills", name)
}

// consentAndRun 授权并执行一次供给。
func consentAndRun(t *testing.T, c *Coordinator, name string) RunResult {
	t.Helper()
	ctx := context.Background()
	if err := c.GrantUnitConsent(ctx, "Ubuntu", CLICommon, SkillUnitID(name)); err != nil {
		t.Fatalf("consent %s: %v", name, err)
	}
	res, err := c.RunUnits(ctx, "Ubuntu", CLICommon, ReasonManual, []UnitID{SkillUnitID(name)})
	if err != nil {
		t.Fatalf("RunUnits %s: %v", name, err)
	}
	return res
}

func firstResult(t *testing.T, res RunResult) UnitResult {
	t.Helper()
	if len(res.Results) != 1 {
		t.Fatalf("expected one result: %+v", res.Results)
	}
	return res.Results[0]
}

func TestSkillConsentGateBlocksFirstSupply(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()

	res, err := c.RunUnits(context.Background(), "Ubuntu", CLICommon, ReasonManual, []UnitID{SkillUnitID("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	r := firstResult(t, res)
	if r.Outcome != UnitBlocked || r.Detail != "awaiting-consent" {
		t.Fatalf("unconsented skill must block: %+v", r)
	}
	if _, statErr := os.Lstat(targetSkillPath(uncRoot, "alpha")); !os.IsNotExist(statErr) {
		t.Fatal("consent gate must not write anything")
	}
}

func TestSkillFirstSupplyAndIdempotentSecondRun(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	writeSkillFile(t, scope.Root(), "alpha", "docs/guide.md", "guide")
	scope.Scan()

	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitApplied || r.Detail != "written" {
		t.Fatalf("first supply must be written: %+v", r)
	}
	got, err := os.ReadFile(filepath.Join(targetSkillPath(uncRoot, "alpha"), "docs", "guide.md"))
	if err != nil || string(got) != "guide" {
		t.Fatalf("supplied content mismatch: %q %v", got, err)
	}
	skillMd, err := os.ReadFile(filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md"))
	if err != nil || string(skillMd) != "# alpha\n" {
		t.Fatalf("SKILL.md mismatch: %q %v", skillMd, err)
	}
	// 登记面：written 且不陈旧。
	st := c.GetStatus("Ubuntu", CLICommon)
	var found *UnitStatus
	for i := range st.Units {
		if st.Units[i].Unit == SkillUnitID("alpha") {
			found = &st.Units[i]
		}
	}
	if found == nil || found.LastOutcome != "written" || found.Stale {
		t.Fatalf("unexpected unit status: %+v", found)
	}
	// 第二次运行：fresh，目标文件 mtime 不变（无重写）。
	marker := filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md")
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	r2 := firstResult(t, consentAndRun(t, c, "alpha"))
	if r2.Outcome != UnitFresh {
		t.Fatalf("second run must be fresh: %+v", r2)
	}
	after, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("fresh run must not rewrite files: %v vs %v", before.ModTime(), after.ModTime())
	}
}

func TestSkillIncrementalUpdateOnlyRewritesChangedDir(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha v1\n")
	writeSkillFile(t, scope.Root(), "beta", "SKILL.md", "# beta\n")
	scope.Scan()
	if r := firstResult(t, consentAndRun(t, c, "alpha")); r.Outcome != UnitApplied {
		t.Fatalf("alpha supply: %+v", r)
	}
	if r := firstResult(t, consentAndRun(t, c, "beta")); r.Outcome != UnitApplied {
		t.Fatalf("beta supply: %+v", r)
	}
	betaMarker := filepath.Join(targetSkillPath(uncRoot, "beta"), "SKILL.md")
	betaBefore, _ := os.Stat(betaMarker)
	alphaMarker := filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md")
	alphaBefore, _ := os.Stat(alphaMarker)

	// 来源变化：重扫后仅 alpha 目录被重写（mtime 变化），beta 保持不动。
	time.Sleep(2 * time.Millisecond) // FAT/跨设备 mtime 粒度保护
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha v2\n")
	scope.Scan()
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitApplied {
		t.Fatalf("alpha update: %+v", r)
	}
	got, _ := os.ReadFile(alphaMarker)
	if string(got) != "# alpha v2\n" {
		t.Fatalf("alpha content must be updated: %q", got)
	}
	alphaAfter, _ := os.Stat(alphaMarker)
	if !alphaAfter.ModTime().After(alphaBefore.ModTime()) {
		t.Fatalf("changed dir must be rewritten (mtime evidence): %v -> %v", alphaBefore.ModTime(), alphaAfter.ModTime())
	}
	betaAfter, _ := os.Stat(betaMarker)
	if !betaAfter.ModTime().Equal(betaBefore.ModTime()) {
		t.Fatalf("unchanged dir must not be rewritten: %v -> %v", betaBefore.ModTime(), betaAfter.ModTime())
	}
}

func TestSkillWSLModificationFreezesUnit(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	if r := firstResult(t, consentAndRun(t, c, "alpha")); r.Outcome != UnitApplied {
		t.Fatalf("supply: %+v", r)
	}
	// WSL 侧手改该 skill 目录。
	edited := filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md")
	if err := os.WriteFile(edited, []byte("# local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 来源随后更新：不覆盖本地修改。
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha v2\n")
	scope.Scan()
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitBlocked || r.Detail != "both-changed-frozen" {
		t.Fatalf("manual edit must freeze: %+v", r)
	}
	got, _ := os.ReadFile(edited)
	if string(got) != "# local edit\n" {
		t.Fatalf("local edit must be retained: %q", got)
	}
	st := c.GetStatus("Ubuntu", CLICommon)
	for _, u := range st.Units {
		if u.Unit == SkillUnitID("alpha") && (!u.Frozen || u.FrozenReason != "both-changed-frozen") {
			t.Fatalf("freeze must be recorded: %+v", u)
		}
	}
}

func TestSkillLocalDeletionFreezesUnit(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	if r := firstResult(t, consentAndRun(t, c, "alpha")); r.Outcome != UnitApplied {
		t.Fatalf("supply: %+v", r)
	}
	if err := os.RemoveAll(targetSkillPath(uncRoot, "alpha")); err != nil {
		t.Fatal(err)
	}
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitBlocked || r.Detail != "local-deletion-frozen" {
		t.Fatalf("deletion must freeze: %+v", r)
	}
}

func TestSkillUnmanagedExistingBlocksFirstSupply(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	// 目标侧已存在用户自有目录：不因来源更晚而覆盖。
	if err := os.MkdirAll(targetSkillPath(uncRoot, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md"), []byte("user own"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitBlocked || r.Detail != "unmanaged-existing" {
		t.Fatalf("unmanaged dir must block: %+v", r)
	}
	got, _ := os.ReadFile(filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md"))
	if string(got) != "user own" {
		t.Fatalf("user content must be retained: %q", got)
	}
}

func TestSkillIncompatibleBlocksSupplyEntirely(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "win", "SKILL.md", "# doc\n")
	writeSkillFile(t, scope.Root(), "win", "run.ps1", "echo")
	scope.Scan()
	r := firstResult(t, consentAndRun(t, c, "win"))
	if r.Outcome != UnitBlocked || r.Detail != "incompatible-items" {
		t.Fatalf("incompatible skill must block: %+v", r)
	}
	if _, statErr := os.Lstat(targetSkillPath(uncRoot, "win")); !os.IsNotExist(statErr) {
		t.Fatal("incompatible skill must not be copied at all")
	}
}

func TestSkillScanNotReadyAndSourceDeleted(t *testing.T) {
	fp := newFakeProbes(t)
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	hostRoot := t.TempDir()
	scope := NewSkillsScope(hostRoot, DefaultSkillsScanLimits())
	c.RegisterSkills(scope)
	// 未扫描：scan-not-ready（不冒充完成；同意门在前——先授权再验证）。
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, SkillUnitID("alpha")); err != nil {
		t.Fatal(err)
	}
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLICommon, ReasonManual, []UnitID{SkillUnitID("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	if r := firstResult(t, res); r.Outcome != UnitBlocked || r.Detail != "scan-not-ready" {
		t.Fatalf("expected scan-not-ready: %+v", r)
	}
	// 已供给后来源被删除：source-deleted，不自动删目标。
	writeSkillFile(t, hostRoot, "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	if r := firstResult(t, consentAndRun(t, c, "alpha")); r.Outcome != UnitApplied {
		t.Fatalf("supply: %+v", r)
	}
	if err := os.RemoveAll(filepath.Join(hostRoot, "alpha")); err != nil {
		t.Fatal(err)
	}
	scope.Scan()
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitBlocked || r.Detail != "source-deleted" {
		t.Fatalf("expected source-deleted: %+v", r)
	}
	if _, statErr := os.Lstat(targetSkillPath(fp.uncRoot, "alpha")); statErr != nil {
		t.Fatalf("target must be retained: %v", statErr)
	}
}

func TestSkillTakeOverCAS(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	// 未托管目标：预览给出 hash 令牌；F6 后未同意预览恒 awaiting-consent
	//（与 Plan 同一答案），授权后再预览才见 unmanaged-existing。
	if err := os.MkdirAll(targetSkillPath(uncRoot, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md"), []byte("user own"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	previews, err := c.PreviewUnits(ctx, "Ubuntu", CLICommon, []UnitID{SkillUnitID("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 || previews[0].BlockedReason != "awaiting-consent" || previews[0].TargetHash == "" {
		t.Fatalf("unconsented preview must be awaiting-consent with token: %+v", previews)
	}
	if err := c.GrantUnitConsent(ctx, "Ubuntu", CLICommon, SkillUnitID("alpha")); err != nil {
		t.Fatal(err)
	}
	previews, err = c.PreviewUnits(ctx, "Ubuntu", CLICommon, []UnitID{SkillUnitID("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 || previews[0].BlockedReason != "unmanaged-existing" || previews[0].TargetHash == "" {
		t.Fatalf("unexpected consented preview: %+v", previews)
	}
	if err := c.GrantUnitConsent(ctx, "Ubuntu", CLICommon, SkillUnitID("alpha")); err != nil {
		t.Fatal(err)
	}
	res, err := c.TakeOverUnit(ctx, "Ubuntu", CLICommon, SkillUnitID("alpha"), "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != UnitBlocked || res.Detail != "preview-expired" {
		t.Fatalf("stale preview must be rejected: %+v", res)
	}
	res, err = c.TakeOverUnit(ctx, "Ubuntu", CLICommon, SkillUnitID("alpha"), previews[0].TargetHash)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != UnitApplied {
		t.Fatalf("takeover must apply: %+v", res)
	}
	got, _ := os.ReadFile(filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md"))
	if string(got) != "# alpha\n" {
		t.Fatalf("takeover must rewrite from source: %q", got)
	}
	// 接管后基准重建：再跑一次为 fresh。
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitFresh {
		t.Fatalf("post-takeover run must be fresh: %+v", r)
	}
}

func TestSkillCompensationRestoresOrDeletes(t *testing.T) {
	c, scope, uncRoot := newSkillsEnv(t)
	ctx := context.Background()
	// 新增供给 → 撤销删除新增目录。
	writeSkillFile(t, scope.Root(), "fresh", "SKILL.md", "# fresh\n")
	scope.Scan()
	r := firstResult(t, consentAndRun(t, c, "fresh"))
	if r.Outcome != UnitApplied || r.Undo == nil {
		t.Fatalf("fresh supply must carry undo: %+v", r)
	}
	if out := c.Compensate(ctx, r.Undo); len(out) != 1 || out[0].Disposition != CompensationConfirmed {
		t.Fatalf("compensation must delete new dir: %+v", out)
	}
	if _, statErr := os.Lstat(targetSkillPath(uncRoot, "fresh")); !os.IsNotExist(statErr) {
		t.Fatal("compensated new dir must be gone")
	}
	// 更新既有 → 撤销恢复前像。
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha v1\n")
	scope.Scan()
	r = firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitApplied {
		t.Fatalf("alpha supply: %+v", r)
	}
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha v2\n")
	scope.Scan()
	r = firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitApplied || r.Undo == nil {
		t.Fatalf("update must carry undo: %+v", r)
	}
	if out := c.Compensate(ctx, r.Undo); len(out) != 1 || out[0].Disposition != CompensationConfirmed {
		t.Fatalf("compensation must restore preimage: %+v", out)
	}
	got, _ := os.ReadFile(filepath.Join(targetSkillPath(uncRoot, "alpha"), "SKILL.md"))
	if string(got) != "# alpha v1\n" {
		t.Fatalf("preimage must be restored: %q", got)
	}
	// 撤销后外部修改保留（CAS 失配跳过；独立单元避免补偿后登记基准
	// 与目标不一致的连锁冻结——那是另一条已钉住的语义）。
	writeSkillFile(t, scope.Root(), "bravo", "SKILL.md", "# bravo v1\n")
	scope.Scan()
	r = firstResult(t, consentAndRun(t, c, "bravo"))
	if r.Outcome != UnitApplied {
		t.Fatalf("bravo supply: %+v", r)
	}
	if err := os.WriteFile(filepath.Join(targetSkillPath(uncRoot, "bravo"), "SKILL.md"), []byte("post-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := c.Compensate(ctx, r.Undo)
	if len(out) != 1 || out[0].Disposition != CompensationSkipped {
		t.Fatalf("external edit must skip compensation: %+v", out)
	}
	got, _ = os.ReadFile(filepath.Join(targetSkillPath(uncRoot, "bravo"), "SKILL.md"))
	if string(got) != "post-edit" {
		t.Fatalf("external edit must be retained: %q", got)
	}
}

func TestSkillDropRegistrationDetached(t *testing.T) {
	c, scope, _ := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	scope.Scan()
	if r := firstResult(t, consentAndRun(t, c, "alpha")); r.Outcome != UnitApplied {
		t.Fatalf("supply: %+v", r)
	}
	if err := c.DropUnitRegistration("Ubuntu", CLICommon, SkillUnitID("alpha")); err != nil {
		t.Fatal(err)
	}
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLICommon, ReasonManual, []UnitID{SkillUnitID("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	if r := firstResult(t, res); r.Outcome != UnitBlocked || r.Detail != "detached" {
		t.Fatalf("detached unit must zero-act: %+v", r)
	}
}

func TestSkillPreviewUnitsReportsIncompatReasons(t *testing.T) {
	c, scope, _ := newSkillsEnv(t)
	writeSkillFile(t, scope.Root(), "alpha", "SKILL.md", "# alpha\n")
	writeSkillFile(t, scope.Root(), "win", "SKILL.md", "# doc\n")
	writeSkillFile(t, scope.Root(), "win", "run.ps1", "echo")
	scope.Scan()
	// F6：未同意预览恒 awaiting-consent（与 Plan 同一答案）。
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICommon, []UnitID{SkillUnitID("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 || previews[0].Action != ActionBlocked || previews[0].BlockedReason != "awaiting-consent" {
		t.Fatalf("unconsented preview must mirror plan consent gate: %+v", previews)
	}
	// 同意后：可便携→write；不兼容→逐项报因。
	ctx := context.Background()
	for _, name := range []string{"alpha", "win"} {
		if err := c.GrantUnitConsent(ctx, "Ubuntu", CLICommon, SkillUnitID(name)); err != nil {
			t.Fatal(err)
		}
	}
	previews, err = c.PreviewUnits(ctx, "Ubuntu", CLICommon, []UnitID{SkillUnitID("alpha"), SkillUnitID("win")})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]UnitPreview{}
	for _, p := range previews {
		byName[string(p.Unit)] = p
	}
	if p := byName[string(SkillUnitID("alpha"))]; p.Action != ActionWriteProjection {
		t.Fatalf("consented portable skill should plan write: %+v", p)
	}
	p := byName[string(SkillUnitID("win"))]
	if p.Action != ActionBlocked || p.BlockedReason != "incompatible-items" {
		t.Fatalf("incompatible preview: %+v", p)
	}
	joined := ""
	for _, n := range p.Notes {
		joined += n
	}
	if !strings.Contains(joined, "run.ps1") || !strings.Contains(joined, "未支持") {
		t.Fatalf("preview notes must carry per-item reason: %+v", p.Notes)
	}
}

// ---------------------------------------------------------------------------
// diting FIX 修复轮（F1/F2/F4/F5/F7）行为钉子。
// ---------------------------------------------------------------------------

// TestScanSkillDirectoryLinkNotRecursed（F1）：目录形态链接（Unix 目录符号
// 链接；Windows 附加 junction——两者在 Go 1.23+ Windows readdir 下均携带
// ModeDir|ModeSymlink）绝不按目录递归下降，树外内容不入清单，整条目判不兼容。
func TestScanSkillDirectoryLinkNotRecursed(t *testing.T) {
	_, root := makeSkillScope(t)
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
		t.Fatalf("dir-shaped link must make skill non-portable: %+v", m)
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

// TestScanTargetTreeDirLinkEntryNotRecursed（F1 目标/前像侧）：目标树内的
// 目录符号链接按链接条目入 hash/前像，不递归。
func TestScanTargetTreeDirLinkEntryNotRecursed(t *testing.T) {
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
	entries, existed, snapshot, _ := snapshotSkillPreimage(dir, DefaultSkillsScanLimits())
	if !existed || !snapshot {
		t.Fatalf("preimage must snapshot: existed=%v snapshot=%v", existed, snapshot)
	}
	linkSeen := false
	for _, e := range entries {
		if e.RelPath == "dirlink" {
			linkSeen = e.IsLink
		}
		if e.RelPath == "dirlink/extra.md" {
			t.Fatal("preimage must not recurse through dir link")
		}
	}
	if !linkSeen {
		t.Fatalf("preimage must record dir link as link entry: %+v", entries)
	}
}

// TestScanSkillLinkNameExcluded（F4）：凭据类名单对链接形态同样生效——
// 名为 .env/*.pem 的树内链接按排除处理，不实体化。（SA-18 fixture 前提
// 修正：原 fixture 建的是 "dot-env"，不在凭据名单内——修正为真 .env，
// 不以扩大黑名单凑绿。）
func TestScanSkillLinkNameExcluded(t *testing.T) {
	_, root := makeSkillScope(t)
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

// TestSkillOversizeTotalBlocked（F5）：总量超过候选预算 → 计划期 blocked
// oversize-skill；不静默截断。
func TestSkillOversizeTotalBlocked(t *testing.T) {
	c, scope, _ := newSkillsEnv(t)
	// 9MB（> 8MB 候选/前像封顶），拆两个文件避开单文件上限。
	big := strings.Repeat("a", 5<<20)
	writeSkillFile(t, scope.Root(), "fat", "part1.md", big)
	writeSkillFile(t, scope.Root(), "fat", "part2.md", big)
	scope.Scan()
	m := scope.manifest("fat")
	if m == nil || !m.Portable() || m.TotalBytes <= skillCandidateCap {
		t.Fatalf("fixture must be portable-but-oversize: %+v", m)
	}
	r := firstResult(t, consentAndRun(t, c, "fat"))
	if r.Outcome != UnitBlocked || r.Detail != "oversize-skill" {
		t.Fatalf("oversize skill must block at plan: %+v", r)
	}
}

// TestRestorePreimageRenameFailureKeepsStaging（F2）：恢复写 rename 失败时
// 唯一持久前像保留在 staging，不被 defer 清理。
func TestRestorePreimageRenameFailureKeepsStaging(t *testing.T) {
	fp := newFakeProbes(t)
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	hostRoot := t.TempDir()
	scope := NewSkillsScope(hostRoot, DefaultSkillsScanLimits())
	c.RegisterSkills(scope)
	writeSkillFile(t, hostRoot, "alpha", "SKILL.md", "# alpha v1\n")
	scope.Scan()
	if r := firstResult(t, consentAndRun(t, c, "alpha")); r.Outcome != UnitApplied {
		t.Fatalf("supply: %+v", r)
	}
	writeSkillFile(t, hostRoot, "alpha", "SKILL.md", "# alpha v2\n")
	scope.Scan()
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitApplied || r.Undo == nil {
		t.Fatalf("update must carry undo: %+v", r)
	}
	orig := skillPreimageRenameFn
	skillPreimageRenameFn = func(string, string) error { return errors.New("9P transient failure") }
	defer func() { skillPreimageRenameFn = orig }()
	// 一致性修正（resource-abort）后补偿优先用持久 prev 树（不经 rename 接
	// 缝）；本测试钉住的是内存快照恢复路径的 F2 语义——先移除 prev 材料（模拟
	// 外部丢失）退回快照兜底。
	if err := os.RemoveAll(absOf(r.Undo.target, r.Undo.displacedRel)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	parent := filepath.Dir(r.Undo.uncPath)
	out := c.Compensate(context.Background(), r.Undo)
	if len(out) != 1 || out[0].Disposition != CompensationUnavailable {
		t.Fatalf("rename failure must yield unavailable: %+v", out)
	}
	if !strings.Contains(out[0].Message, "preimage kept in staging") {
		t.Fatalf("error must honestly point at kept staging: %s", out[0].Message)
	}
	// staging 前像仍在（未被 defer 清理）：目录下存在 .amagi-supply-skill-*。
	des, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, e := range des {
		if strings.HasPrefix(e.Name(), ".amagi-supply-skill-") {
			kept = true
		}
	}
	if !kept {
		t.Fatal("staging preimage must be kept after rename failure (F2)")
	}
}

// TestRestorePreimageChirmodAndVerify（F7）：恢复写经 chmod 补偿 + 树 hash
// 读回校验（与主提交原语同构）后内容正确恢复。
func TestRestorePreimageChirmodAndVerify(t *testing.T) {
	fp := newFakeProbes(t)
	c := NewCoordinator(t.TempDir(), nil, fp.probes())
	hostRoot := t.TempDir()
	scope := NewSkillsScope(hostRoot, DefaultSkillsScanLimits())
	c.RegisterSkills(scope)
	writeSkillFile(t, hostRoot, "alpha", "SKILL.md", "# alpha v1\n")
	scope.Scan()
	r := firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitApplied {
		t.Fatalf("supply: %+v", r)
	}
	writeSkillFile(t, hostRoot, "alpha", "SKILL.md", "# alpha v2\n")
	scope.Scan()
	r = firstResult(t, consentAndRun(t, c, "alpha"))
	if r.Outcome != UnitApplied {
		t.Fatalf("update: %+v", r)
	}
	before := atomic.LoadInt32(&fp.chmodCalls)
	// 同 F2 测试：持久 prev 树优先（不经重写路径）；F7 钉的是快照重建路径的
	// chmod 补偿语义——移除 prev 材料后退回快照兜底。
	if err := os.RemoveAll(absOf(r.Undo.target, r.Undo.displacedRel)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out := c.Compensate(context.Background(), r.Undo)
	if len(out) != 1 || out[0].Disposition != CompensationConfirmed {
		t.Fatalf("compensation must confirm: %+v", out)
	}
	if atomic.LoadInt32(&fp.chmodCalls) <= before {
		t.Fatal("restore must run distro chmod compensation (F7)")
	}
	got, _ := os.ReadFile(filepath.Join(targetSkillPath(fp.uncRoot, "alpha"), "SKILL.md"))
	if string(got) != "# alpha v1\n" {
		t.Fatalf("preimage restored: %q", got)
	}
}

// TestSkillCandidateReverifiesSourceIdentity（SA-05）：候选读取前重验路径身份
// ——扫描后来源被替换为符号链接/非常规形态时拒绝供给（不追随新链接目标）。
func TestSkillCandidateReverifiesSourceIdentity(t *testing.T) {
	_, root := makeSkillScope(t)
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
