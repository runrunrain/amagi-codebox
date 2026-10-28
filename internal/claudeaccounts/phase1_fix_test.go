package claudeaccounts

// phase1_fix_test.go — Phase 1 修复批次的领域反例（diting F03/F05/F06/F13/F14）。
// 全部使用合成凭据与 fake 后端（design §10.1：绝不触碰真实 Keychain/原生文件）。

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// countKeychainReads 返回一个在 fakeKeychain 第 N 次 ReadItem 前执行一次的 hook
//（一次性触发，避免后续读继续变异）。
func countKeychainReads(env *testEnv, nth int, mutate func()) {
	env.t.Helper()
	counts := 0
	env.keychain.onRead = func() {
		counts++
		if counts == nth {
			env.keychain.onRead = nil // 一次性触发
			mutate()
		}
	}
}

// TestSwapStopsOnNativeDriftBeforeCommit：F03-A——最终 source 读取后、短提交锁内
// 复核前原生 CC 项被外部刷新（B→B′）：revision 漂移必须阻止写入，journal 保留
// 交由 reconcile 分类（CC 对 before/after 均 unknown → needs-reconcile），不得
// 覆盖未回收的新 token。
func TestSwapStopsOnNativeDriftBeforeCommit(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB() // active=B，native=B
	pool, _ := env.svc.LoadPool(context.Background())

	// Swap 读序（CombinedNativeStore 每次 ReadStable 含 2 次探测+实读）：
	// 1-2=harvest、3-4=current2、5-6=短锁内复核。在第 5 次读取前把 CC 项
	// 换成 B′（模拟外部刷新；无身份映射——漂移检查先于任何身份使用）。
	countKeychainReads(env, 5, func() {
		env.keychain.mu.Lock()
		env.keychain.items[0].data = syntheticKeychainItem("cc-b-drifted")
		env.keychain.mu.Unlock()
	})

	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("提交前漂移必须失败")
	}
	if ReasonOf(err) != ReasonNeedsReconcile {
		t.Fatalf("漂移错误码 = %s，期望 NEEDS_RECONCILE", ReasonOf(err))
	}
	// 失败后自动 reconcile：CC=B′ 对 before=B/after=A 均不可分类 → journal 保留。
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.Transaction == nil || pool2.Transaction.Swap == nil {
		t.Fatal("漂移后 journal 必须保留（needs-reconcile），不能静默清除")
	}
	if pool2.ActiveGeneration != pool.ActiveGeneration {
		t.Fatalf("代际不应变化：%d → %d", pool.ActiveGeneration, pool2.ActiveGeneration)
	}
	// 原生未被覆盖：CC 仍是 B′（外部刷新值），Pi 仍是 B。
	native, rerr := env.svc.native.ReadStable(context.Background())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(native.ClaudeOAuth), "cc-b-drifted") {
		t.Fatalf("未回收的 B′ token 被覆盖：%s", native.ClaudeOAuth)
	}
	if got := gjson.GetBytes(readJSON(t, env.piAuthPath()), "anthropic.access").String(); got != "pi-b-1" {
		t.Fatalf("Pi 槽位不应被写入，实际 access=%s", got)
	}
	// 后续恢复检查同样分类为 needs-reconcile（保全当前值）。
	outcome, oerr := env.svc.ReconcileExistingTransaction(context.Background())
	if oerr == nil || outcome.Action != ReconcileNeedsReconcile {
		t.Fatalf("恢复分类 = %s/%v，期望 needs-reconcile", outcome.Action, oerr)
	}
}

// TestSwapSecondHarvestReplacesJournalBefore：F03-B——第一次 harvest 与 current2 之间
// 源账号刷新（B→B′）：第二次 harvest 的 OwnedProjections 必须同步替换 journal.Before；
// 采集后崩溃于首写（注入 Keychain 更新失败）→ 全 before（B′）→ reconcile 清 journal
// 而非因 before/expected 分裂卡死 needs-reconcile。
func TestSwapSecondHarvestReplacesJournalBefore(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())

	// B′ 属于账号 B（harvest 的身份校验需要）。
	env.identityFake.responses["cc-b-2"] = ProfileIdentity{AccountUUID: syntheticUUIDB, OrganizationUUID: syntheticOrgB}
	// Swap 读序：1=harvest（B）、2=current2——在第 2 次读取前外部刷新为 B′。
	countKeychainReads(env, 2, func() {
		env.keychain.mu.Lock()
		env.keychain.items[0].data = syntheticKeychainItem("cc-b-2")
		env.keychain.mu.Unlock()
	})
	// 首次原生写入（CC patch）失败：崩溃于 prepared 后第一写。
	env.keychain.updateErrAt = 1

	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("注入的写入失败必须上抛")
	}
	// F03-B 核心：before 与 expected 来自同一次（第二次 harvest 后的）快照——
	// 全 before 分类成立 → journal 清除、源仍活动，且快照已回收 B′。
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.Transaction != nil {
		t.Fatalf("二次 harvest 后 before 一致时，失败事务应被分类为全 before 并清除，实际保留 %+v", pool2.Transaction)
	}
	if pool2.ActiveAccountID == "" {
		t.Fatal("活动账号不应被清空")
	}
	if got := gjson.GetBytes(pool2.Accounts[pool2.ActiveAccountID].Claude.OauthProjection, "accessToken").String(); got != "cc-b-2" {
		t.Fatalf("活动快照应已回收 B′，实际 access=%s", got)
	}
}

// TestSwapRejectsRefreshedTargetWithoutIdentity：F05——standby grant 刷新后身份
// 不可验证（新 token 无 profile 映射）→ 不写原生、不宣布切换成功。
func TestSwapRejectsRefreshedTargetWithoutIdentity(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// A（standby）的 Pi grant 过期；刷新返回全新 token pi-a-fresh，但故意
	// 不建立身份映射——旧身份证据不能代替现场验证。
	expired := `{"type":"oauth","access":"pi-a-stale","refresh":"pir-a-stale","expires":` +
		strconv.FormatInt(env.clock.Now().Add(-time.Hour).UnixMilli(), 10) + `}`
	env.pool.mu.Lock()
	pool.Accounts[idA].Pi.Anthropic = []byte(expired)
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	env.transport.handler = func(req *http.Request) (*http.Response, error) {
		return httpResponse(200, `{"access_token":"pi-a-fresh","refresh_token":"pir-a-fresh","expires_in":3600}`), nil
	}

	beforePi := readJSON(t, env.piAuthPath())
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("刷新后身份不可验证必须拒绝切换")
	}
	if ReasonOf(err) != ReasonIdentityUnverified {
		t.Fatalf("错误码 = %s，期望 IDENTITY_UNVERIFIED", ReasonOf(err))
	}
	if string(readJSON(t, env.piAuthPath())) != string(beforePi) {
		t.Fatal("身份验证失败时原生 Pi 槽位必须零写入")
	}
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.ActiveGeneration != pool.ActiveGeneration {
		t.Fatalf("代际不应变化：%d → %d", pool.ActiveGeneration, pool2.ActiveGeneration)
	}
}

// TestSwapRejectsRevokedTargetGrant：F05——未过期但已撤销/身份不符的 standby
// grant（token 身份映射到另一账号）→ IDENTITY_MISMATCH，不写原生。
func TestSwapRejectsRevokedTargetGrant(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// 目标 A 的 CC token 现场验证返回账号 C 的身份（未过期但被撤销换发的场景）。
	env.identityFake.mu.Lock()
	env.identityFake.responses["cc-a-1"] = ProfileIdentity{AccountUUID: "uuid-c", OrganizationUUID: "org-c"}
	env.identityFake.mu.Unlock()

	beforePi := readJSON(t, env.piAuthPath())
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("目标身份不符必须拒绝切换")
	}
	if ReasonOf(err) != ReasonIdentityMismatch && ReasonOf(err) != ReasonIdentityUnverified {
		t.Fatalf("错误码 = %s，期望 IDENTITY_MISMATCH/UNVERIFIED", ReasonOf(err))
	}
	if string(readJSON(t, env.piAuthPath())) != string(beforePi) {
		t.Fatal("身份不符时原生槽位必须零写入")
	}
}

// TestSwapNoOpClearsAllBeforeJournal：F06——journal 全 before 时切回 source：
// 先由 reconcile 清 journal，再 no-op 返回；不得带 journal 提前 done。
func TestSwapNoOpClearsAllBeforeJournal(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// B→A 的 swap 失败于首写（journal prepared、原生全 before=B）。
	env.keychain.updateErrAt = 1
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("注入失败必须上抛")
	}
	pool2, _ := env.svc.LoadPool(context.Background())
	// 注入失败后自动 reconcile 清了 journal（全 before）——重新构造一个
	// retained journal：直接注入 prepared journal（source=B, target=A），
	// 原生保持 B（全 before）。
	ownedB := pool2.Account(idB).OwnedProjectionsOf()
	ownedA := pool2.Account(idA).OwnedProjectionsOf()
	pool2.Transaction = &PoolTransaction{Kind: TransactionSwap, Swap: &SwapJournal{
		TxnID: "txn-noop-before", SourceID: idB, TargetID: idA,
		FromGeneration: pool2.ActiveGeneration, Phase: SwapPrepared,
		Before: ownedB, After: ownedA,
	}}
	pool2.Revision++
	env.pool.mu.Lock()
	data, _ := encodePoolEnvelope(pool2)
	env.pool.envelope = data
	env.pool.mu.Unlock()

	// restore-source 语义：Swap(target=source=B) → 先 reconcile（全 before 清
	// journal）→ no-op。此前实现会在 no-op 检查处提前返回，journal 永远残留。
	res, serr := env.svc.Swap(context.Background(), SwapRequest{TargetID: idB, Reason: SwapReasonManual, ExpectedGeneration: pool2.ActiveGeneration})
	if serr != nil {
		t.Fatalf("swap → %s: %v", idB, serr)
	}
	if !res.NoOp {
		t.Fatal("全 before 恢复后同目标应为 no-op")
	}
	pool3, _ := env.svc.LoadPool(context.Background())
	if pool3.Transaction != nil {
		t.Fatal("no-op 前必须先完成未结事务（journal 已清）")
	}
}

// TestMergePiAnthropicRefreshPreservesUnknownFields：F13——刷新在既有条目上
// 字段级更新，未知扩展字段保留。
func TestMergePiAnthropicRefreshPreservesUnknownFields(t *testing.T) {
	existing := []byte(`{"type":"oauth","access":"old-acc","refresh":"old-ref","expires":1,"future_extension":{"x":2},"custom_note":"keep-me"}`)
	out, err := MergePiAnthropicRefresh(existing, GrantRefreshResult{
		AccessToken: "new-acc", RefreshToken: "new-ref",
		ExpiresAt: time.UnixMilli(2_000_000_000_000),
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := gjson.GetBytes(out, "access").String(); got != "new-acc" {
		t.Fatalf("access = %s", got)
	}
	if got := gjson.GetBytes(out, "refresh").String(); got != "new-ref" {
		t.Fatalf("refresh = %s", got)
	}
	if got := gjson.GetBytes(out, "expires").Float(); int64(got) != 2_000_000_000_000 {
		t.Fatalf("expires = %v", got)
	}
	if !gjson.GetBytes(out, "future_extension.x").Exists() || gjson.GetBytes(out, "custom_note").String() != "keep-me" {
		t.Fatalf("未知字段必须保留：%s", out)
	}
	if gjson.GetBytes(out, "type").String() != "oauth" {
		t.Fatalf("type 必须保持 oauth：%s", out)
	}

	// 非法既有条目：拒绝（无法证明字段归属），不从零重建。
	if _, err := MergePiAnthropicRefresh([]byte(`{broken`), GrantRefreshResult{}); err == nil {
		t.Fatal("非法既有条目必须拒绝合并")
	}
	// 空既有条目：退回全新构造（首次写入语义）。
	fresh, err := MergePiAnthropicRefresh(nil, GrantRefreshResult{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.UnixMilli(1)})
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if gjson.GetBytes(fresh, "access").String() != "a" {
		t.Fatalf("fresh access = %s", fresh)
	}
}

// TestRefreshStandbyPiKeepsUnknownFieldsEndToEnd：F13 端到端——standby Pi 条目含
// 扩展字段，换入前刷新后扩展字段仍在池内新条目中。
func TestRefreshStandbyPiKeepsUnknownFieldsEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	expired := `{"type":"oauth","access":"pi-a-stale","refresh":"pir-a-stale","expires":` +
		strconv.FormatInt(env.clock.Now().Add(-time.Hour).UnixMilli(), 10) + `,"tier_hint":"beta"}`
	env.identityFake.responses["pi-a-fresh"] = ProfileIdentity{AccountUUID: syntheticUUIDA, OrganizationUUID: syntheticOrgA}
	env.pool.mu.Lock()
	pool.Accounts[idA].Pi.Anthropic = []byte(expired)
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	env.transport.handler = func(req *http.Request) (*http.Response, error) {
		return httpResponse(200, `{"access_token":"pi-a-fresh","refresh_token":"pir-a-fresh","expires_in":3600}`), nil
	}
	mustSwap(t, env, idA, pool.ActiveGeneration)
	pool2, _ := env.svc.LoadPool(context.Background())
	if gjson.GetBytes(pool2.Accounts[idA].Pi.Anthropic, "tier_hint").String() != "beta" {
		t.Fatalf("刷新后扩展字段丢失：%s", pool2.Accounts[idA].Pi.Anthropic)
	}
}

// TestRemoveAccountRejectsStaleRevision：F14——stale revision 的删除请求被拒，
// 账号零删除。
func TestRemoveAccountRejectsStaleRevision(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	err := env.svc.RemoveAccount(context.Background(), idA, pool.Revision+5)
	if err == nil || ReasonOf(err) != ReasonRevisionConflict {
		t.Fatalf("stale revision 删除必须 REVISION_CONFLICT，实际 %v", err)
	}
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.Account(idA) == nil {
		t.Fatal("stale revision 删除不得移除账号")
	}
	// 正确 revision 删除成功。
	if err := env.svc.RemoveAccount(context.Background(), idA, pool2.Revision); err != nil {
		t.Fatalf("正确 revision 删除应成功：%v", err)
	}
	pool3, _ := env.svc.LoadPool(context.Background())
	if pool3.Account(idA) != nil || pool3.ActiveAccountID != idB {
		t.Fatal("删除后账号应移除且活动账号不变")
	}
}

// 辅助：读取文件内容（测试内已存在 readJSON；此处供错误信息使用）。
var _ = os.ReadFile
