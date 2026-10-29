package claudeaccounts

// phase1_fix2_test.go — Phase 1 修复第三轮领域反例（diting 增量复审
// F05 残留 / F06 TOCTOU / R02 恢复基线）。全部使用合成凭据与 fake 后端
// （design §10.1：不触碰真实 Keychain/原生文件）。

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// TestSwapRejectsPiGrantOrganizationMismatch：F05 残留——目标 Pi grant 的
// 组织与记录/CC 不同（账号 UUID 一致）必须拒绝，原生零写入。此前只比较 CC
// 组织，Pi 组织不一致会被静默放行。
func TestSwapRejectsPiGrantOrganizationMismatch(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	env.identityFake.mu.Lock()
	env.identityFake.responses["pi-a-1"] = ProfileIdentity{AccountUUID: syntheticUUIDA, OrganizationUUID: "org-other"}
	env.identityFake.mu.Unlock()

	beforePi := readJSON(t, env.piAuthPath())
	updates := env.keychain.updateCount()
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if ReasonOf(err) != ReasonIdentityMismatch {
		t.Fatalf("Pi 组织不一致错误码 = %s（%v），期望 IDENTITY_MISMATCH", ReasonOf(err), err)
	}
	if string(readJSON(t, env.piAuthPath())) != string(beforePi) || env.keychain.updateCount() != updates {
		t.Fatal("组织不一致时原生槽位必须零写入")
	}
}

// TestSwapRejectsPartialOrganizationEvidence：F05 残留——只有一端报告组织
// （另一端未知）不能当作通过，返回 IDENTITY_UNVERIFIED。
func TestSwapRejectsPartialOrganizationEvidence(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	env.identityFake.mu.Lock()
	env.identityFake.responses["pi-a-1"] = ProfileIdentity{AccountUUID: syntheticUUIDA}
	env.identityFake.mu.Unlock()

	updates := env.keychain.updateCount()
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if ReasonOf(err) != ReasonIdentityUnverified {
		t.Fatalf("单端组织未知错误码 = %s（%v），期望 IDENTITY_UNVERIFIED", ReasonOf(err), err)
	}
	if env.keychain.updateCount() != updates {
		t.Fatal("组织证据不完整时 Keychain 必须零写入")
	}
}

// 组织全部未知与本地 identity 漂移也必须在真实 Swap 原生写入前拒绝。
func TestSwapRejectsUnknownOrganizationAndLocalIdentityDrift(t *testing.T) {
	for _, scenario := range []string{"unknown-organizations", "local-uuid", "local-organization"} {
		t.Run(scenario, func(t *testing.T) {
			env := newTestEnv(t)
			idA, _ := env.enrollAccountB()
			pool, _ := env.svc.LoadPool(context.Background())
			want := ReasonIdentityMismatch
			switch scenario {
			case "unknown-organizations":
				want = ReasonIdentityUnverified
				pool.Account(idA).Identity.OrganizationUUID = ""
				env.identityFake.responses["cc-a-1"] = ProfileIdentity{AccountUUID: syntheticUUIDA}
				env.identityFake.responses["pi-a-1"] = ProfileIdentity{AccountUUID: syntheticUUIDA}
			case "local-uuid":
				pool.Account(idA).ClaudeIdentity.OauthAccount = []byte(`{"accountUuid":"foreign-uuid"}`)
			case "local-organization":
				pool.Account(idA).ClaudeIdentity.OauthAccount = []byte(`{"accountUuid":"` + syntheticUUIDA + `","organizationUuid":"foreign-org"}`)
			}
			if err := env.pool.Save(context.Background(), pool); err != nil {
				t.Fatal(err)
			}
			updates := env.keychain.updateCount()
			beforePi := string(readJSON(t, env.piAuthPath()))
			_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
			if ReasonOf(err) != want {
				t.Fatalf("身份反例 %s = %v，期望 %s", scenario, err, want)
			}
			if env.keychain.updateCount() != updates || string(readJSON(t, env.piAuthPath())) != beforePi {
				t.Fatal("身份拒绝时原生槽位必须零写")
			}
		})
	}
}

func TestVerifyOrganizationTriple(t *testing.T) {
	cases := []struct {
		name, rec, cc, pi string
		want              Reason
	}{
		{"三方一致", "o", "o", "o", ""},
		{"三方均未报告", "", "", "", ReasonIdentityUnverified},
		{"Pi 不同", "o", "o", "x", ReasonIdentityMismatch},
		{"CC 不同", "o", "x", "o", ReasonIdentityMismatch},
		{"记录不同", "x", "o", "o", ReasonIdentityMismatch},
		{"Pi 未知", "o", "o", "", ReasonIdentityUnverified},
		{"记录未知", "", "o", "o", ReasonIdentityUnverified},
	}
	for _, c := range cases {
		err := verifyOrganizationTriple(c.rec, c.cc, c.pi)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s：期望通过，得到 %v", c.name, err)
			}
			continue
		}
		if err == nil || ReasonOf(err) != c.want {
			t.Errorf("%s：得到 %q，期望 %q", c.name, ReasonOf(err), c.want)
		}
	}
}

// TestRecoverRollsForwardAfterPatchSucceededBeforeProgressSave：R02——CC Patch
// 已成功、紧接着的池进度 Save 失败（「Patch 成功→Save 前终止」窗口），持久化
// journal 的 ExpectedRevisions 仍是旧值。恢复必须在短锁内重分类并冻结新基线
// 完成 roll-forward，而不是因 revision 不符永久卡在 needs-reconcile。
func TestRecoverRollsForwardAfterPatchSucceededBeforeProgressSave(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	env.keychain.afterUpdate = func() {
		env.keychain.afterUpdate = nil
		env.pool.mu.Lock()
		env.pool.saveErrAt = env.pool.saveCalls + 1 // 下一次进度 Save 失败
		env.pool.mu.Unlock()
	}
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("注入的进度 Save 失败必须上抛")
	}
	// Swap 失败后的自动 reconcile（gate 仍持有）应已 roll-forward 完成。
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.Transaction != nil {
		t.Fatalf("Patch 成功→Save 前终止应可恢复，journal 仍保留：%+v", pool2.Transaction.Swap)
	}
	if pool2.ActiveAccountID != idA || pool2.ActiveGeneration != pool.ActiveGeneration+1 {
		t.Fatalf("恢复后 active=%s gen=%d，期望 %s/%d", pool2.ActiveAccountID, pool2.ActiveGeneration, idA, pool.ActiveGeneration+1)
	}
	native, rerr := env.svc.native.ReadStable(context.Background())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !env.svc.nativeMatchesOwned(native, pool2.Account(idA).OwnedProjectionsOf()) {
		t.Fatal("恢复完成后三处原生投影必须等于目标")
	}
}

// TestRecoverRejectsUnknownAfterClassification：R02 反例——恢复短锁内重读时
// 某投影已被外部改为既非 before 也非 after 的值，必须拒绝、不写入任何槽位。
func TestRecoverRejectsUnknownAfterClassification(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	piBefore := ""
	env.keychain.afterUpdate = func() {
		env.keychain.afterUpdate = nil
		env.pool.mu.Lock()
		env.pool.saveErrAt = env.pool.saveCalls + 1
		env.pool.mu.Unlock()
		// 失败后的 reconcile 分类读（2 次 ReadItem）正常；恢复短锁内的
		// 重读（第 3 次起）前外部把 CC 改成第三个值。
		reads := 0
		env.keychain.onRead = func() {
			reads++
			if reads == 3 {
				env.keychain.onRead = nil
				env.keychain.mu.Lock()
				env.keychain.items[0].data = syntheticKeychainItem("cc-foreign")
				env.keychain.mu.Unlock()
			}
		}
	}
	piBefore = string(readJSON(t, env.piAuthPath()))
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("必须失败")
	}
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.Transaction == nil || pool2.Transaction.Swap == nil {
		t.Fatal("出现 unknown 时 journal 必须保留待人工恢复")
	}
	if pool2.ActiveAccountID == idA {
		t.Fatal("unknown 时不得提交到目标")
	}
	if string(readJSON(t, env.piAuthPath())) != piBefore {
		t.Fatal("unknown 时 Pi 槽位不得被 roll-forward 写入")
	}
	native, _ := env.svc.native.ReadStable(context.Background())
	if !strings.Contains(string(native.ClaudeOAuth), "cc-foreign") {
		t.Fatal("外部写入的未知值必须保全")
	}
	if got := gjson.GetBytes(readJSON(t, env.piAuthPath()), "anthropic.access").String(); got != "pi-b-1" {
		t.Fatalf("Pi 仍应为源账号 access，实际 %s", got)
	}
}

// injectRetainedJournal 构造一个 retained swap journal（source=B, target=A,
// 原生仍全 before）用于恢复命令 guard 测试。
func injectRetainedJournal(t *testing.T, env *testEnv, idA, idB, txnID string) *AccountPool {
	t.Helper()
	p, _ := env.svc.LoadPool(context.Background())
	p.Transaction = &PoolTransaction{Kind: TransactionSwap, Swap: &SwapJournal{
		TxnID: txnID, SourceID: idB, TargetID: idA,
		FromGeneration: p.ActiveGeneration, Phase: SwapPrepared,
		Before: p.Account(idB).OwnedProjectionsOf(), After: p.Account(idA).OwnedProjectionsOf(),
	}}
	p.Revision++
	env.pool.mu.Lock()
	data, _ := encodePoolEnvelope(p)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	return p
}

// TestRecoverTransactionGuardInsideLocks：F06——恢复命令的 expectedRevision 与
// 所选 journal 在同一 withLocks 内校验：陈旧 revision / 不同 TxnID 拒绝且零写入；
// 匹配时正常恢复。
func TestRecoverTransactionGuardInsideLocks(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	p := injectRetainedJournal(t, env, idA, idB, "txn-guard-1")
	saves := env.pool.saveCount()

	if _, err := env.svc.RecoverTransaction(context.Background(), RecoveryGuard{ExpectedRevision: p.Revision - 1, ExpectedTxnID: "txn-guard-1"}); ReasonOf(err) != ReasonRevisionConflict {
		t.Fatalf("陈旧 revision 错误码 = %s，期望 REVISION_CONFLICT", ReasonOf(err))
	}
	if _, err := env.svc.RecoverTransaction(context.Background(), RecoveryGuard{ExpectedRevision: p.Revision, ExpectedTxnID: "txn-other"}); ReasonOf(err) != ReasonRevisionConflict {
		t.Fatalf("不同 journal 错误码 = %s，期望 REVISION_CONFLICT", ReasonOf(err))
	}
	if env.pool.saveCount() != saves {
		t.Fatal("guard 拒绝时不得写池")
	}
	out, err := env.svc.RecoverTransaction(context.Background(), RecoveryGuard{ExpectedRevision: p.Revision, ExpectedTxnID: "txn-guard-1"})
	if err != nil || out.Action != ReconcileClearedJournal {
		t.Fatalf("匹配 guard 恢复 = %s/%v，期望 cleared-journal", out.Action, err)
	}
}

// TestSwapGuardRejectsStaleRestoreSource：F06——restore-source 经 Swap 携带
// guard，revision 在锁内比对；陈旧命令拒绝且 journal 原样保留。
func TestSwapGuardRejectsStaleRestoreSource(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	p := injectRetainedJournal(t, env, idA, idB, "txn-guard-2")
	_, err := env.svc.Swap(context.Background(), SwapRequest{
		TargetID: idB, Reason: SwapReasonManual, ExpectedGeneration: p.ActiveGeneration,
		Guard: &RecoveryGuard{ExpectedRevision: p.Revision + 1, ExpectedTxnID: "txn-guard-2"},
	})
	if ReasonOf(err) != ReasonRevisionConflict {
		t.Fatalf("陈旧 restore-source 错误码 = %s，期望 REVISION_CONFLICT", ReasonOf(err))
	}
	p2, _ := env.svc.LoadPool(context.Background())
	if p2.Transaction == nil || p2.Transaction.Swap.TxnID != "txn-guard-2" {
		t.Fatal("guard 拒绝时 journal 必须原样保留")
	}
}
