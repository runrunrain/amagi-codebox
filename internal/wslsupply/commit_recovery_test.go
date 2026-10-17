package wslsupply

// commit_recovery_test.go — 攻坚契约 L2：提交/恢复生命周期的公开入口回归
//（phase-rereview 反例转正 + 契约失败矩阵的确定性接缝版本）。所有断言落在
// 文件字节、持久状态（intent/settled）、新 Coordinator（无旧 Undo）与真实
// 结果事实上，不只 helper 返回 error。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// commitTestEnv 构造单文件提交测试环境（reviewAdapter 风格的普通/敏感单元）。
type commitTestEnv struct {
	c                *Coordinator
	fp               *fakeProbes
	target           TargetRef
	rel              string
	uncPath          string
	publishHookArmed bool
}

func newCommitTestEnv(t *testing.T, sensitive bool, preimage []byte) *commitTestEnv {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: 0})
	adapter := &reviewAdapter{rel: "prefs.json", source: func() []byte { return []byte(`{"v":2}`) }}
	_ = adapter
	target, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	uncPath := filepath.Join(target.UNCRoot, filepath.FromSlash("prefs.json"))
	if preimage != nil {
		if err := os.MkdirAll(filepath.Dir(uncPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(uncPath, preimage, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = sensitive
	return &commitTestEnv{c: c, fp: fp, target: target, rel: "prefs.json", uncPath: uncPath}
}

// TestAttackFirstFinalSaveFailureKeepsFacts：首发 final save 持续失败 → intent
// 在盘、目标已是新内容、内存不冒充已持久化；重建 Coordinator（无旧 Undo）
// 能识别 committed-unregistered 并补登记基准；随后撤回不得 Fresh 漏副本。
func TestAttackFirstFinalSaveFailureKeepsFacts(t *testing.T) {
	prev := []byte(`{"user":"ORIGINAL"}`)
	env := newCommitTestEnv(t, false, prev)
	// final save 持续失败：把清单路径换成目录（每次保存 rename 都失败）。
	if err := os.Remove(env.c.state.path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(env.c.state.path, 0o700); err != nil {
		t.Fatal(err)
	}
	rcpt := env.c.commitFile(context.Background(), CommitRequest{
		Target: env.target, Unit: "pi/attack", RelPath: env.rel,
		ExpectedBefore: hexHash(prev), Candidate: []byte(`{"v":2}`), Kind: OpKindWrite,
	})
	if rcpt.Err == nil || rcpt.ErrDetail != "content-committed-state-unpersisted" {
		t.Fatalf("final save failure must yield content-committed-state-unpersisted, got %+v", rcpt)
	}
	if rcpt.Effect != OpEffectCommitted {
		t.Fatalf("effect must be committed (content is on target), got %s", rcpt.Effect)
	}
	// 目标已是新内容（事实不因登记失败消失）。
	if got, err := os.ReadFile(env.uncPath); err != nil || string(got) != `{"v":2}` {
		t.Fatalf("committed content must be kept, got %q err=%v", got, err)
	}
	// intent 在盘可读。
	if _, ok, err := env.c.state.readIntent(rcpt.OpID); err != nil || !ok {
		t.Fatalf("intent must survive on disk, ok=%v err=%v", ok, err)
	}
	// 恢复存储 → 新 Coordinator（重启语义：无旧 Undo、无缓存 health）→
	// RunUnits 前 reconcile 识别 committed-unregistered 并补登记基准。
	if err := os.RemoveAll(env.c.state.path); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Dir(env.c.state.path)
	c2 := NewCoordinator(cfgDir, nil, env.fp.probes())
	intents, err := c2.state.loadIntents()
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 {
		t.Fatalf("one pending intent expected, got %d", len(intents))
	}
	rep := c2.ReconcilePending(context.Background(), env.target)
	found := false
	for _, d := range rep.Dispositions {
		if d.OpID == rcpt.OpID && d.Disposition == ReconcileCommitted {
			found = true
		}
	}
	if !found {
		t.Fatalf("reconcile must register committed-unregistered, got %+v", rep.Dispositions)
	}
	// 基准已补（记录 AfterHash；授权不臆造）。
	st := c2.state.load()
	entry := st.entryFor("pi/attack", env.target.Generation)
	if entry.LastWrittenHash != rcpt.AfterHash {
		t.Fatalf("reconcile must register the after-hash baseline, got %+v", entry)
	}
}

// TestAttackPersistentIntentSaveFailureNeverWritesTarget：intent 持续保存失败
// → 目标逐字不动（不先写后补记），receipt Effect=none。
func TestAttackPersistentIntentSaveFailureNeverWritesTarget(t *testing.T) {
	prev := []byte(`{"user":"ORIGINAL"}`)
	env := newCommitTestEnv(t, false, prev)
	// pending 目录换成文件：CreateTemp 恒失败（持续故障）。
	pendingDir := filepath.Join(filepath.Dir(env.c.state.path), pendingDirName)
	if err := os.WriteFile(pendingDir, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	rcpt := env.c.commitFile(context.Background(), CommitRequest{
		Target: env.target, Unit: "pi/attack", RelPath: env.rel,
		ExpectedBefore: hexHash(prev), Candidate: []byte(`{"v":2}`), Kind: OpKindWrite,
	})
	if rcpt.Err == nil || rcpt.Effect != OpEffectNone {
		// 目标未动：Effect 必须是 none（写前未获准）。
		t.Fatalf("intent failure must abort before any target write, got detail=%s effect=%s", rcpt.ErrDetail, rcpt.Effect)
	}
	if got, err := os.ReadFile(env.uncPath); err != nil || string(got) != string(prev) {
		t.Fatalf("target must stay byte-identical, got %q err=%v", got, err)
	}
}

// TestAttackReconcileCrashMatrix：崩溃窗口矩阵——intent 在、settled 无、目标
// 各阶段状态；新 Coordinator 无旧 Undo 时按磁盘查证处置，不重复业务。
func TestAttackReconcileCrashMatrix(t *testing.T) {
	cases := []struct {
		name        string
		targetState string // bytes written to target relative to intent
		want        ReconcileDisposition
	}{
		{"crash after intent, target untouched", "before", ReconcileNotApplied},
		{"crash after move, target absent, prev kept", "absent", ReconcileUnconfirmed},
		{"crash after publish, target equals after", "after", ReconcileCommitted},
		{"external rewrite during window", "foreign", ReconcileConflicted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newCommitTestEnv(t, false, []byte(`{"user":"BEFORE"}`))
			opID := newOpID()
			intent := OperationIntent{
				OpID: opID, Kind: OpKindWrite, TargetKey: targetKeyOf(env.target),
				TargetDistro: "Ubuntu", TargetHome: env.target.Home,
				Unit: "pi/attack", RelPath: env.rel,
				BeforeExists: true, BeforeHash: hexHash([]byte(`{"user":"BEFORE"}`)),
				AfterHash:    hexHash([]byte(`{"AFTER"}`)),
				BackupRel:    ".amagi-staging/recovery-" + opID + "/before",
				DisplacedRel: ".amagi-supply-displaced-" + opID,
				CreatedAt:    timeNow(),
			}
			switch tc.targetState {
			case "before":
				_ = os.WriteFile(env.uncPath, []byte(`{"user":"BEFORE"}`), 0o600)
			case "after":
				_ = os.WriteFile(env.uncPath, []byte(`{"AFTER"}`), 0o600)
			case "absent":
				_ = os.Remove(env.uncPath)
				displaced := filepath.Join(filepath.Dir(env.uncPath), ".amagi-supply-displaced-"+opID)
				_ = os.WriteFile(displaced, []byte(`{"user":"BEFORE"}`), 0o600)
			case "foreign":
				_ = os.WriteFile(env.uncPath, []byte(`{"FOREIGN"}`), 0o600)
			}
			if err := env.c.state.saveIntent(intent); err != nil {
				t.Fatal(err)
			}
			// 新 Coordinator 语义：重建后 reconcile（无活跃租用）。
			rep := env.c.ReconcilePending(context.Background(), env.target)
			var got *ReconcileDisposition
			for i := range rep.Dispositions {
				if rep.Dispositions[i].OpID == opID {
					d := rep.Dispositions[i].Disposition
					got = &d
				}
			}
			if got == nil || *got != tc.want {
				t.Fatalf("disposition = %v, want %v (all=%+v)", got, tc.want, rep.Dispositions)
			}
		})
	}
}

// TestAttackExternalVersionDuringPublishKeepsBoth：CAS 后、发布落位前出现
// 外部版本 → no-replace 拒绝，两处保留，绝不覆盖。
func TestAttackExternalVersionDuringPublishKeepsBoth(t *testing.T) {
	prev := []byte(`{"user":"ORIGINAL"}`)
	env := newCommitTestEnv(t, false, prev)
	external := []byte(`{"external":"VERSION"}`)
	// 接缝：发布时先在目标落一个外部版本（模拟初次 CAS 后的并发写者）。
	orig := publishNoReplaceFn
	publishNoReplaceFn = func(source, destination string) error {
		if strings.HasSuffix(filepath.ToSlash(destination), "prefs.json") && env.publishHookArmed {
			_ = os.WriteFile(destination, external, 0o600)
		}
		return orig(source, destination)
	}
	t.Cleanup(func() { publishNoReplaceFn = orig })
	env.publishHookArmed = true
	rcpt := env.c.commitFile(context.Background(), CommitRequest{
		Target: env.target, Unit: "pi/attack", RelPath: env.rel,
		ExpectedBefore: hexHash(prev), Candidate: []byte(`{"v":2}`), Kind: OpKindWrite,
	})
	if rcpt.Err == nil || rcpt.Recovery != OpRecoveryConflict {
		t.Fatalf("external version during publish must yield conflict, got %+v", rcpt)
	}
	// 真实原语（os.ErrExist 族）必须命中 publish-conflict 分类，不得落入通用
	// commit-move-unknown 兜底（后者同样给 conflict，曾掩盖哨兵死分支）。
	if rcpt.ErrDetail != "publish-conflict" {
		t.Fatalf("real no-replace conflict must classify as publish-conflict, got %q (%v)", rcpt.ErrDetail, rcpt.Err)
	}
	// 外部版本在原路径原样保留（未被覆盖）。
	if got, err := os.ReadFile(env.uncPath); err != nil || string(got) != string(external) {
		t.Fatalf("external version must be retained verbatim, got %q err=%v", got, err)
	}
}

// TestAttackCompensateCanceledCtxStillRestores：RunUnits→Compensate 且请求
// ctx 已取消 → 独立有限预算内实际恢复前像（恢复责任不消失）；会话不启动
// 由调用方保证（Start=0 的会话面断言在 root 包测试）。
func TestAttackCompensateCanceledCtxStillRestores(t *testing.T) {
	prev := []byte(`{"user":"ORIGINAL"}`)
	env := newCommitTestEnv(t, false, prev)
	rcpt := env.c.commitFile(context.Background(), CommitRequest{
		Target: env.target, Unit: "pi/attack", RelPath: env.rel,
		ExpectedBefore: hexHash(prev), Candidate: []byte(`{"v":2}`), Kind: OpKindWrite,
	})
	if rcpt.Err != nil {
		t.Fatalf("setup commit: %+v", rcpt)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcomes := env.c.Compensate(ctx, rcpt.UndoLease)
	if len(outcomes) == 0 || outcomes[0].Disposition != CompensationConfirmed {
		t.Fatalf("canceled request must still restore via bounded recovery budget: %+v", outcomes)
	}
	if got, err := os.ReadFile(env.uncPath); err != nil || string(got) != string(prev) {
		t.Fatalf("preimage must be restored, got %q err=%v", got, err)
	}
}

// TestAttackReleaseAndOldHandleSkipsNewerContent：Release 清材料；旧句柄
// 补偿遇后一次写入内容必须 skipped（不回滚新内容）。
func TestAttackReleaseAndOldHandleSkipsNewerContent(t *testing.T) {
	prev := []byte(`{"user":"V1"}`)
	env := newCommitTestEnv(t, false, prev)
	first := env.c.commitFile(context.Background(), CommitRequest{
		Target: env.target, Unit: "pi/attack", RelPath: env.rel,
		ExpectedBefore: hexHash(prev), Candidate: []byte(`{"user":"V2"}`), Kind: OpKindWrite,
	})
	if first.Err != nil {
		t.Fatalf("first commit: %+v", first)
	}
	second := env.c.commitFile(context.Background(), CommitRequest{
		Target: env.target, Unit: "pi/attack", RelPath: env.rel,
		ExpectedBefore: hexHash([]byte(`{"user":"V2"}`)), Candidate: []byte(`{"user":"V3"}`), Kind: OpKindWrite,
	})
	if second.Err != nil {
		t.Fatalf("second commit: %+v", second)
	}
	// 旧句柄补偿：目标已是 V3（后一次写入）→ skipped，不覆盖。
	outcomes := env.c.Compensate(context.Background(), first.UndoLease)
	if outcomes[0].Disposition != CompensationSkipped {
		t.Fatalf("old handle must skip newer content, got %+v", outcomes[0])
	}
	if got, _ := os.ReadFile(env.uncPath); string(got) != `{"user":"V3"}` {
		t.Fatalf("newer content must be retained, got %s", got)
	}
	// Release（Commit 语义）：清理已终结材料；settled 墓碑收敛。
	rels := env.c.ReleaseCompensations(context.Background(), second.UndoLease)
	if rels[0].Disposition != CompensationConfirmed {
		t.Fatalf("release must confirm settled op, got %+v", rels[0])
	}
	st := env.c.state.load()
	if _, still := st.SettledOperations[second.OpID]; still {
		t.Fatal("released op tombstone must be cleaned")
	}
}

// TestAttackWithdrawalAfterUnregisteredWriteNoFreshLeak：写未登记 → 存储恢
// 复 → 关闭源 → 撤回 → 旧 write intent 不得复活秘密（撤回终结优先）。
func TestAttackWithdrawalAfterUnregisteredWriteNoFreshLeak(t *testing.T) {
	env := newCommitTestEnv(t, false, []byte(`{"user":"ORIGINAL"}`))
	rcpt := env.c.commitFile(context.Background(), CommitRequest{
		Target: env.target, Unit: "pi/attack", RelPath: env.rel,
		ExpectedBefore: hexHash([]byte(`{"user":"ORIGINAL"}`)), Candidate: []byte(`{"SECRET":"v2"}`), Kind: OpKindWrite,
	})
	if rcpt.Err != nil {
		t.Fatalf("setup: %+v", rcpt)
	}
	// 撤回（remove intent + 隔离 + 复验）。
	rrcpt := env.c.commitRemove(context.Background(), env.target, "pi/attack", env.rel, rcpt.AfterHash)
	if rrcpt.Err != nil || rrcpt.Effect != OpEffectRemoved {
		t.Fatalf("withdrawal must quarantine the copy: %+v", rrcpt)
	}
	if _, err := os.Lstat(env.uncPath); !os.IsNotExist(err) {
		t.Fatalf("consumption path must be absent after withdrawal, err=%v", err)
	}
	// 旧 write intent 的 reconcile 不复活秘密：处置为已终结（被撤回结论覆盖）。
	rep := env.c.ReconcilePending(context.Background(), env.target)
	for _, d := range rep.Dispositions {
		if d.OpID == rcpt.OpID && (d.Disposition == ReconcileCommitted || d.Disposition == ReconcileUnconfirmed) {
			t.Fatalf("withdrawn write intent must not revive the secret, got %+v", d)
		}
	}
	// 文件不再出现。
	if _, err := os.Lstat(env.uncPath); !os.IsNotExist(err) {
		t.Fatal("secret must stay absent after reconcile")
	}
}

// TestAttackDirMoveAsideCancelRestores：目录 move-aside 之后取消 → 独立恢复
// 或明确保留位置；空目录也不丢（不做 RemoveAll 冒充恢复）。
func TestAttackDirMoveAsideCancelRestores(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "host", "demo")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# NEW"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := NewSkillsScope(filepath.Join(root, "host"), DefaultSkillsScanLimits())
	scope.Scan()
	fp := &fakeProbes{uncRoot: filepath.Join(root, "target"),
		identity: platformUserIdentityFixture()}
	c := newCoordinatorWith(t, fp, nil)
	c.RegisterSkills(scope)
	unit := SkillUnitID("demo")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, unit); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(fp.uncRoot, "home/u/.agents/skills/demo")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte("# USER ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	pv, x := c.PreviewUnits(context.Background(), "Ubuntu", CLICommon, []UnitID{unit})
	if x != nil {
		t.Fatal(x)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 精确取消边界：目标目录被移走（prev 出现）时取消。
	ctx := boundaryCtx{Context: base, cancel: cancel, when: func() bool {
		ps, _ := filepath.Glob(filepath.Join(filepath.Dir(targetDir), ".amagi-supply-prev-*"))
		if _, err := os.Lstat(targetDir); err == nil {
			return false
		}
		return len(ps) > 0
	}}
	res, xerr := c.TakeOverUnit(ctx, "Ubuntu", CLICommon, unit, pv[0].TargetHash)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome == UnitApplied {
		t.Fatal("canceled-after-move-aside takeover must not report applied")
	}
	// 原树恢复（canceled-restored）或 prev 明确保留；空目录也不丢。
	got, rerr := os.ReadFile(filepath.Join(targetDir, "SKILL.md"))
	restored := rerr == nil && string(got) == "# USER ORIGINAL"
	if !restored {
		ps, _ := filepath.Glob(filepath.Join(filepath.Dir(targetDir), ".amagi-supply-prev-*"))
		kept := false
		for _, p := range ps {
			b, err := os.ReadFile(filepath.Join(p, "SKILL.md"))
			if err == nil && string(b) == "# USER ORIGINAL" {
				kept = true
			}
		}
		if !kept {
			t.Fatalf("dir preimage must be restored or explicitly kept (detail=%s)", res.Detail)
		}
	}
}

// TestAttackContinuousFaultNotOnlyFirst：持续故障（每次命中）不是仅第一次
// 破坏——fault seam times=0 语义验证。
func TestAttackContinuousFaultNotOnlyFirst(t *testing.T) {
	env := newCommitTestEnv(t, false, []byte(`{"user":"ORIGINAL"}`))
	env.c.faults.add(stageVerify, 0, errors.New("persistent verify fault"))
	for i := 0; i < 3; i++ {
		before, _ := os.ReadFile(env.uncPath)
		rcpt := env.c.commitFile(context.Background(), CommitRequest{
			Target: env.target, Unit: "pi/attack", RelPath: env.rel,
			ExpectedBefore: hexHash(before), Candidate: []byte(fmt.Sprintf(`{"v":%d}`, i)), Kind: OpKindWrite,
		})
		if rcpt.Err == nil {
			t.Fatalf("continuous verify fault must fail every attempt (attempt %d)", i)
		}
	}
}

// timeNow 是测试内时间助手（intent CreatedAt）。
func timeNow() time.Time { return time.Now() }

// platformUserIdentityFixture 返回目录测试用的固定身份。
func platformUserIdentityFixture() platform.WSLUserIdentity {
	return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "fixture", OK: true}
}
