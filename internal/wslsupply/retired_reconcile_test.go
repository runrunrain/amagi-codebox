package wslsupply

// retired_reconcile_test.go — R1-F03 回归：退役单元的未决 intent 在对账时收口
//（retired-closed），不重建在役登记、不触碰 WSL 侧旧副本。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRetiredPendingIntentClosedWithoutResurrection 正负成对：
//   - 正：shared/skill/* 与 pi/* 的 dir-swap pending intent → ReconcileRetired、
//     intent 终结删除、状态清单与 GetStatus 均无该单元、目标树原样保留；
//   - 负：在役单元（claude 资源目录）同等 intent 仍走 committed 补基准——
//     退役门不得误伤在役恢复链。
func TestRetiredPendingIntentClosedWithoutResurrection(t *testing.T) {
	cfgDir := t.TempDir()
	fp := &fakeProbes{uncRoot: filepath.Join(cfgDir, "target"), identity: platformUserIdentityFixture()}
	c := NewCoordinator(cfgDir, nil, fp.probes())
	trCommon, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}

	// 退役技能单元的未决 intent：目标树 == after（旧代码会走 committed 复活）。
	dst := filepath.Join(trCommon.UNCRoot, ".agents", "skills", "alpha")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("# legacy copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := scanTargetSkillDir(dst, DefaultSkillsScanLimits()).TreeHash
	op := OperationIntent{
		OpID: newOpID(), Kind: OpKindDirSwap, TargetKey: targetKeyOf(trCommon),
		TargetDistro: "Ubuntu", TargetHome: trCommon.Home, TargetCLI: string(CLICommon),
		Unit: "shared/skill/alpha", RelPath: ".agents/skills/alpha",
		BeforeExists: true, AfterHash: after,
	}
	if err := c.state.saveIntent(op); err != nil {
		t.Fatal(err)
	}

	rep := c.ReconcilePending(context.Background(), trCommon)
	found := false
	for _, d := range rep.Dispositions {
		if d.OpID == op.OpID {
			found = true
			if d.Disposition != ReconcileRetired {
				t.Fatalf("retired intent must close as retired, got %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("reconcile must process the retired intent, got %+v", rep.Dispositions)
	}
	if rep.Blocked {
		t.Fatalf("retired closure must not block: %+v", rep)
	}
	// 登记不复活（状态清单与 GetStatus 双口径）。
	st := c.state.load()
	for _, u := range st.Units {
		if retiredSkillUnit(u.Unit) || retiredPiUnit(u.Unit) {
			t.Fatalf("retired unit must not be re-registered: %+v", u)
		}
	}
	for _, u := range c.GetStatus("Ubuntu", CLICommon).Units {
		if retiredSkillUnit(u.Unit) || retiredPiUnit(u.Unit) {
			t.Fatalf("retired unit must not surface in GetStatus: %+v", u)
		}
	}
	// intent 已终结删除；目标侧旧副本原样保留（不再受管，不臆动文件系统）。
	if _, still, _ := c.state.readIntent(op.OpID); still {
		t.Fatal("retired intent must be settled and removed")
	}
	if data, rerr := os.ReadFile(filepath.Join(dst, "SKILL.md")); rerr != nil || string(data) != "# legacy copy" {
		t.Fatalf("legacy WSL copy must stay untouched, err=%v", rerr)
	}

	// 负例：在役 claude 资源目录的同等 intent 仍走 committed 补基准。
	c2 := NewCoordinator(t.TempDir(), nil, fp.probes())
	host := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, "skills", "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host, "skills", "live", "SKILL.md"), []byte("# live"), 0o644); err != nil {
		t.Fatal(err)
	}
	c2.RegisterResources(NewResourceScope(CLIClaude, ResourceLayout{
		HostRoot: host, DirUnits: []string{"skills"}, TargetPrefix: ".claude/",
	}, DefaultSkillsScanLimits()))
	trClaude, err := c2.ResolveTarget(context.Background(), "Ubuntu", CLIClaude)
	if err != nil {
		t.Fatal(err)
	}
	liveDst := filepath.Join(trClaude.UNCRoot, ".claude", "skills", "live")
	if err := os.MkdirAll(liveDst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDst, "SKILL.md"), []byte("# live"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveAfter := scanTargetSkillDir(liveDst, DefaultSkillsScanLimits()).TreeHash
	liveOp := OperationIntent{
		OpID: newOpID(), Kind: OpKindDirSwap, TargetKey: targetKeyOf(trClaude),
		TargetDistro: "Ubuntu", TargetHome: trClaude.Home, TargetCLI: string(CLIClaude),
		Unit: ResourceUnitID(CLIClaude, "skills/live"), RelPath: ".claude/skills/live",
		BeforeExists: true, AfterHash: liveAfter, CreatedAt: time.Now(),
	}
	if err := c2.state.saveIntent(liveOp); err != nil {
		t.Fatal(err)
	}
	rep2 := c2.ReconcilePending(context.Background(), trClaude)
	ok := false
	for _, d := range rep2.Dispositions {
		if d.OpID == liveOp.OpID {
			if d.Disposition != ReconcileCommitted {
				t.Fatalf("active unit intent must still reconcile to committed, got %+v", d)
			}
			ok = true
		}
	}
	if !ok {
		t.Fatalf("active intent must be processed, got %+v", rep2.Dispositions)
	}
	st2 := c2.state.load()
	reg := false
	for _, u := range st2.Units {
		if u.Unit == ResourceUnitID(CLIClaude, "skills/live") {
			reg = true
		}
	}
	if !reg {
		t.Fatal("active unit baseline must be registered after committed reconcile")
	}
}
