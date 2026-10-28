package claudeaccounts

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// swap/reconcile 组（design §10.1 矩阵第 4 行）。

func mustSwap(t *testing.T, env *testEnv, targetID string, expectedGen uint64) SwapResult {
	t.Helper()
	res, err := env.svc.Swap(context.Background(), SwapRequest{
		TargetID: targetID, Reason: SwapReasonManual, ExpectedGeneration: expectedGen,
	})
	if err != nil {
		t.Fatalf("swap → %s: %v", targetID, err)
	}
	return res
}

// assertNativeIsAccount 断言三处原生投影当前呈现指定账号的快照值。
func assertNativeIsAccount(t *testing.T, env *testEnv, pool *AccountPool, accountID string) {
	t.Helper()
	native, err := env.svc.native.ReadStable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snap := pool.Account(accountID)
	if snap == nil {
		t.Fatalf("账号 %s 不在池中", accountID)
	}
	if !env.svc.nativeMatchesOwned(native, snap.OwnedProjectionsOf()) {
		t.Fatalf("原生槽位与账号 %s 的快照不一致", accountID)
	}
}

// TestEnrollAndSwapBothWays：A→B→A；分别保留各自最新 CC/Pi grant。
func TestEnrollAndSwapBothWays(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()

	pool, _ := env.svc.LoadPool(context.Background())
	if pool.ActiveAccountID != idB {
		t.Fatalf("录入 B 后活动账号应为 B，实际 %s", pool.ActiveAccountID)
	}

	// A→B（B 是活动，切回 A）。
	res := mustSwap(t, env, idA, pool.ActiveGeneration)
	if res.Generation != pool.ActiveGeneration+1 {
		t.Fatalf("代际应递增，实际 %d → %d", pool.ActiveGeneration, res.Generation)
	}
	assertNativeIsAccount(t, env, pool, idA)

	// 切回 B。
	pool, _ = env.svc.LoadPool(context.Background())
	mustSwap(t, env, idB, pool.ActiveGeneration)
	assertNativeIsAccount(t, env, pool, idB)

	pool, _ = env.svc.LoadPool(context.Background())
	if pool.LastSwitch == nil || pool.LastSwitch.TargetAccountID != idB {
		t.Fatal("lastSwitch 未记录")
	}
	if pool.ManualRespectUntil == nil || !pool.ManualRespectUntil.After(env.clock.Now()) {
		t.Fatal("手动切换成功后应设置尊重期")
	}
	// 预设 key 语义：Pi 其他 provider 条目保留。
	if !gjson.GetBytes(readJSON(t, env.piAuthPath()), "openai").Exists() {
		t.Fatal("Pi 其他 provider 条目丢失")
	}
}

// TestSwapNoOpSameTarget：同目标且三处确实匹配 → no-op、零写入。
func TestSwapNoOpSameTarget(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// 切回 A 后再次切 A → no-op。
	mustSwap(t, env, idA, pool.ActiveGeneration)
	pool, _ = env.svc.LoadPool(context.Background())
	beforeKC := env.keychain.updateCount()
	beforeSaves := env.pool.saveCount()
	res := mustSwap(t, env, idA, pool.ActiveGeneration)
	if !res.NoOp {
		t.Fatal("同目标且匹配应为 no-op")
	}
	if env.keychain.updateCount() != beforeKC || env.pool.saveCount() != beforeSaves {
		t.Fatal("no-op 不得有任何写入")
	}
}

// TestSwapGateDenied：admission 被拒 → SESSIONS_ACTIVE、原生零写。
func TestSwapGateDenied(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	beforeKC := env.keychain.updateCount()
	beforeCJ := readJSON(t, env.claudeJSONPath())
	env.gate.deny = true
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	assertReason(t, err, ReasonSessionsActive)
	if env.keychain.updateCount() != beforeKC {
		t.Fatal("admission 被拒时原生 Keychain 写入计数应为零")
	}
	if string(readJSON(t, env.claudeJSONPath())) != string(beforeCJ) {
		t.Fatal("admission 被拒时 .claude.json 不应被写")
	}
	_ = idB
}

// TestSwapQuiescenceUncertain：静止条件未确认 → 不写。
func TestSwapQuiescenceUncertain(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	env.quiescence.err = NewErr(ReasonExternalSessionUncertain, "注入：存在不可分类进程")
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	assertReason(t, err, ReasonExternalSessionUncertain)
}

// TestSwapRejectsStaleGeneration：期望代际不符 → REVISION_CONFLICT。
func TestSwapRejectsStaleGeneration(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration + 99})
	assertReason(t, err, ReasonRevisionConflict)
}

// TestSwapRejectsAutomaticReason：前端不可伪造 automatic。
func TestSwapRejectsAutomaticReason(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: "automatic", ExpectedGeneration: pool.ActiveGeneration})
	assertReason(t, err, ReasonRevisionConflict)
}

// TestSwapTargetNeedsIdentityEvidence：目标缺身份证据 → 拒绝。
func TestSwapTargetNeedsIdentityEvidence(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	pool.Accounts[idA].IdentityEvidence = nil // 模拟 standby 证据缺失
	env.pool.mu.Lock()
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	assertReason(t, err, ReasonIdentityUnverified)
}

// TestSwapRefreshesExpiredStandbyPiGrant：过期 standby Pi grant 换入前刷新
// （design §5.1：Phase 1 换入前按需刷新两份 grant；Pi 路径已验证协议）。
func TestSwapRefreshesExpiredStandbyPiGrant(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// 把 A（standby）的 Pi grant 置为已过期（基于注入时钟，避免真实时钟偏差）。
	expired := `{"type":"oauth","access":"pi-a-stale","refresh":"pir-a-stale","expires":` +
		strconv.FormatInt(env.clock.Now().Add(-time.Hour).UnixMilli(), 10) + `}`
	env.identityFake.responses["pi-a-stale"] = ProfileIdentity{AccountUUID: syntheticUUIDA}
	env.pool.mu.Lock()
	pool.Accounts[idA].Pi.Anthropic = []byte(expired)
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()

	// 刷新端点返回新 token。
	env.transport.handler = func(req *http.Request) (*http.Response, error) {
		return httpResponse(200, `{"access_token":"pi-a-fresh","refresh_token":"pir-a-fresh","expires_in":3600}`), nil
	}
	mustSwap(t, env, idA, pool.ActiveGeneration)
	pool2, _ := env.svc.LoadPool(context.Background())
	if got := gjson.GetBytes(pool2.Accounts[idA].Pi.Anthropic, "access").String(); got != "pi-a-fresh" {
		t.Fatalf("换入后应使用刷新后的 Pi grant，实际 %s", got)
	}
	assertNativeIsAccount(t, env, pool2, idA)
}

// TestSwapFailureEntersReconcileRollForward：pi-auth 写入失败后自动恢复
// roll-forward 到 after（design §4.5 混合行）。
func TestSwapFailureEntersReconcileRollForward(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// 注入 pi 文件写入失败（第三次原子写 = pi patch）。
	env.filesOps = newFailOps("rename", 1)
	env.rebuildService()
	_, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("期望注入失败")
	}
	// 失败后自动 reconcile：混合态在 gate 持有下 roll-forward 到 after。
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.ActiveAccountID != idA {
		t.Fatalf("roll-forward 后活动账号应为 A，实际 %s", pool2.ActiveAccountID)
	}
	if pool2.Transaction != nil {
		t.Fatal("roll-forward 后 journal 应清除")
	}
	assertNativeIsAccount(t, env, pool2, idA)
	_ = idB
}

// TestSwapNeedsReconcileOnUnknownProjection：journal 存在且原生出现未知值
// → 不盲覆盖、保全 journal（design §4.5 未知行）。
func TestSwapNeedsReconcileOnUnknownProjection(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// 手工构造 prepared journal：before=B 投影，after=A 投影。
	env.pool.mu.Lock()
	pool.Transaction = &PoolTransaction{Kind: TransactionSwap, Swap: &SwapJournal{
		TxnID: "txn-x", SourceID: idB, TargetID: idA,
		FromGeneration: pool.ActiveGeneration, Phase: SwapPrepared,
		Before: pool.Accounts[idB].OwnedProjectionsOf(),
		After:  pool.Accounts[idA].OwnedProjectionsOf(),
	}}
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	// 原生仍为 B（全 before），但把 pi 文件换成第三种未知值。
	unknown := syntheticPiAuth("pi-unknown-x", futureExpiresMS(env.clock.Now()))
	if werr := os.WriteFile(env.piAuthPath(), unknown, 0o644); werr != nil {
		t.Fatal(werr)
	}
	outcome, rerr := env.svc.ReconcileExistingTransaction(context.Background())
	if rerr == nil {
		t.Fatalf("期望 needs-reconcile 错误，实际 outcome=%s", outcome.Action)
	}
	if outcome.Action != ReconcileNeedsReconcile {
		t.Fatalf("期望 needs-reconcile，实际 %s", outcome.Action)
	}
	pool3, _ := env.svc.LoadPool(context.Background())
	if pool3.Transaction == nil {
		t.Fatal("未知投影必须保全 journal")
	}
	// 未知值不被盲覆盖。
	if got := gjson.GetBytes(readJSON(t, env.piAuthPath()), "anthropic.access").String(); got != "pi-unknown-x" {
		t.Fatalf("未知值不应被覆盖，实际 %s", got)
	}
}

// TestReconcileClearedJournal：prepared 后全 before → 清 journal，源仍活动。
func TestReconcileClearedJournal(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// 注入 Keychain patch 失败 → journal prepared，原生仍全 before。
	env.keychain.mu.Lock()
	env.keychain.updateErrAt = env.keychain.updateCalls + 1
	env.keychain.mu.Unlock()
	if _, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration}); err == nil {
		t.Fatal("期望注入失败")
	}
	// 失败路径自动 reconcile 已把全 before 判定为未应用并清除 journal。
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.Transaction != nil {
		t.Fatalf("全 before 应清除 journal，实际 %+v", pool2.Transaction)
	}
	if pool2.ActiveAccountID != idB {
		t.Fatal("源账号应仍活动")
	}
	assertNativeIsAccount(t, env, pool2, idB)
}

// TestReconcileCommittedAfter：三处已等于 after → 完成提交不重复写文件。
func TestReconcileCommittedAfter(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	// 正常完成一次 swap（无注入）。
	mustSwap(t, env, idA, pool.ActiveGeneration)
	// 手工构造一个“崩溃于 commit 前”的 journal：三处已 == after。
	pool2, _ := env.svc.LoadPool(context.Background())
	target := pool2.Accounts[idA]
	env.pool.mu.Lock()
	pool2.Transaction = &PoolTransaction{Kind: TransactionSwap, Swap: &SwapJournal{
		TxnID: "txn-crash", SourceID: idB, TargetID: idA,
		FromGeneration: pool2.ActiveGeneration - 1, Phase: SwapCommitting,
		Before: pool2.Accounts[idB].OwnedProjectionsOf(),
		After:  target.OwnedProjectionsOf(),
	}}
	pool2.ActiveAccountID = idB // 模拟池提交未发生
	pool2.ActiveGeneration--
	data, _ := encodePoolEnvelope(pool2)
	env.pool.envelope = data
	env.pool.mu.Unlock()

	beforeKC := env.keychain.updateCount()
	outcome, err := env.svc.RecoverAtStartup(context.Background())
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if outcome.Action != ReconcileCommittedAfter {
		t.Fatalf("期望 committed-after，实际 %s", outcome.Action)
	}
	if env.keychain.updateCount() != beforeKC {
		t.Fatal("committed-after 不得重复写文件")
	}
	pool3, _ := env.svc.LoadPool(context.Background())
	if pool3.ActiveAccountID != idA || pool3.Transaction != nil {
		t.Fatal("恢复后活动账号/journal 状态不符")
	}
}

// TestContextCancelPreservesWrittenFacts：context 取消不能抹除已写事实
// （design §10.1 swap/reconcile 行）：第一处写入后取消，journal 留存已写
// 记录，后续恢复以回读为准继续完成。
func TestContextCancelPreservesWrittenFacts(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	// 在第二处写入（.claude.json patch）前取消 context。
	hook := &hookNative{inner: env.svcNative(), onBeforePatch: func(key ProjectionKey) {
		if key == ProjectionClaudeJSON {
			cancel()
		}
	}}
	env.setNative(hook)
	_, err := env.svc.Swap(ctx, SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration})
	if err == nil {
		t.Fatal("期望取消错误")
	}
	// 取消不抹除已写事实：cc-keychain 已写入 after 值；journal 保留；活动
	// 账号未提交（仍为 B）。取消的 ctx 同样阻断自动 roll-forward。
	pool2, lerr := env.svc.LoadPool(context.Background())
	if lerr != nil {
		t.Fatalf("取消后池不可读: %v", lerr)
	}
	if pool2.Transaction == nil {
		t.Fatal("取消后应保留 journal")
	}
	if pool2.ActiveAccountID != idB {
		t.Fatal("取消后未完成提交，活动账号应仍为源")
	}
	ccAfter := gjson.GetBytes(pool2.Accounts[idA].Claude.OauthProjection, "accessToken").String()
	ccNative, rerr := env.svcNative().ReadStable(context.Background())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if got := gjson.GetBytes(ccNative.ClaudeOAuth, "accessToken").String(); got != ccAfter {
		t.Fatalf("已写入的 Keychain 事实不应被取消抹除：期望 %s，实际 %s", ccAfter, got)
	}
	// 新 context 下带 gate 恢复 → roll-forward 完成。
	outcome, rerr := env.svc.RecoverAtStartup(context.Background())
	if rerr != nil {
		t.Fatalf("recover: %v（outcome=%s）", rerr, outcome.Action)
	}
	if outcome.Action != ReconcileRolledForward {
		t.Fatalf("期望 rolled-forward，实际 %s", outcome.Action)
	}
	pool3, _ := env.svc.LoadPool(context.Background())
	if pool3.ActiveAccountID != idA {
		t.Fatal("roll-forward 后活动账号应为 A")
	}
	assertNativeIsAccount(t, env, pool3, idA)
	_ = idB
}

type hookNative struct {
	inner        NativeCredentialStore
	onBeforePatch func(key ProjectionKey)
	patchCount   int
}

func (h *hookNative) ReadStable(ctx context.Context) (*NativeProjections, error) {
	return h.inner.ReadStable(ctx)
}

func (h *hookNative) PatchClaudeKeychainOAuth(ctx context.Context, oauth json.RawMessage) (*FileRevision, error) {
	h.patchCount++
	return h.inner.PatchClaudeKeychainOAuth(ctx, oauth)
}

func (h *hookNative) PatchClaudeIdentity(ctx context.Context, fields ClaudeIdentityFields) (*FileRevision, error) {
	if h.onBeforePatch != nil {
		h.onBeforePatch(ProjectionClaudeJSON)
	}
	return h.inner.PatchClaudeIdentity(ctx, fields)
}

func (h *hookNative) PatchPiAnthropic(ctx context.Context, anthropic json.RawMessage) (*FileRevision, error) {
	if h.onBeforePatch != nil {
		h.onBeforePatch(ProjectionPiAuth)
	}
	return h.inner.PatchPiAnthropic(ctx, anthropic)
}

func (h *hookNative) Kind() string { return h.inner.Kind() }
