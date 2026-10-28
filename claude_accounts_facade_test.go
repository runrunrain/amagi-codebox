package main

// claude_accounts_facade_test.go — App facade 测试（design §7.2）：PoolDTO
// 状态投影、后台 operation 生命周期、requestId 幂等/冲突、SetRotation 校验、
// Reconcile inspect 零写、错误码透传、敏感字段不出 DTO/事件。

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/remote"
)

func twoAccountPool() *claudeaccounts.AccountPool {
	pool := claudeaccounts.NewAccountPool()
	pool.ActiveAccountID = "acc-a"
	pool.ActiveGeneration = 4
	pool.Revision = 2
	pool.Accounts["acc-a"] = &claudeaccounts.AccountSnapshot{
		ID: "acc-a", Alias: "A",
		Claude:           claudeaccounts.ClaudeGrantState{OauthProjection: []byte(`{"accessToken":"SECRET-CC-A"}`)},
		Pi:               claudeaccounts.PiGrantState{Anthropic: []byte(`{"type":"oauth","access":"SECRET-PI-A"}`)},
		ClaudeIdentity:   claudeaccounts.ClaudeIdentityFields{OauthAccount: []byte(`{"accountUuid":"uuid-a"}`), UserID: "user-a"},
		Identity:         claudeaccounts.AccountIdentity{AccountUUID: "uuid-a", OrganizationUUID: "org-a"},
		IdentityEvidence: &claudeaccounts.IdentityEvidence{CCAccountUUID: "uuid-a", PiAccountUUID: "uuid-a"},
		Health:           claudeaccounts.AccountHealth{ClaudeGrant: claudeaccounts.HealthReady, PiGrant: claudeaccounts.HealthReady, IdentityStatus: "verified"},
	}
	pool.Accounts["acc-b"] = &claudeaccounts.AccountSnapshot{
		ID: "acc-b", Alias: "B",
		Claude:           claudeaccounts.ClaudeGrantState{OauthProjection: []byte(`{"accessToken":"SECRET-CC-B"}`)},
		Pi:               claudeaccounts.PiGrantState{Anthropic: []byte(`{"type":"oauth","access":"SECRET-PI-B"}`)},
		ClaudeIdentity:   claudeaccounts.ClaudeIdentityFields{OauthAccount: []byte(`{"accountUuid":"uuid-b"}`), UserID: "user-b"},
		Identity:         claudeaccounts.AccountIdentity{AccountUUID: "uuid-b", OrganizationUUID: "org-b"},
		IdentityEvidence: &claudeaccounts.IdentityEvidence{CCAccountUUID: "uuid-b", PiAccountUUID: "uuid-b"},
		Health:           claudeaccounts.AccountHealth{ClaudeGrant: claudeaccounts.HealthReady, PiGrant: claudeaccounts.HealthReady, IdentityStatus: "verified"},
	}
	return pool
}

func nativeMatchingA() *claudeaccounts.NativeProjections {
	return &claudeaccounts.NativeProjections{
		ClaudeOAuth:    []byte(`{"accessToken":"SECRET-CC-A"}`),
		ClaudeIdentity: &claudeaccounts.ClaudeIdentityFields{OauthAccount: []byte(`{"accountUuid":"uuid-a"}`), UserID: "user-a"},
		PiAnthropic:    []byte(`{"type":"oauth","access":"SECRET-PI-A"}`),
	}
}

func newFacadeTestApp(t *testing.T) (*App, *fakePoolStore, *fakeNativeStore, *fakeIdentityProvider) {
	t.Helper()
	app := newGateTestApp(t)
	pool := twoAccountPool()
	native := nativeMatchingA()
	// F05：换入前目标双 grant 现场验证需要可控身份响应（真实网络不可用）。
	identity := &fakeIdentityProvider{responses: map[string]claudeaccounts.ProfileIdentity{
		"SECRET-CC-A": {AccountUUID: "uuid-a", OrganizationUUID: "org-a"},
		"SECRET-PI-A": {AccountUUID: "uuid-a", OrganizationUUID: "org-a"},
		"SECRET-CC-B": {AccountUUID: "uuid-b", OrganizationUUID: "org-b"},
		"SECRET-PI-B": {AccountUUID: "uuid-b", OrganizationUUID: "org-b"},
	}}
	svc, poolStore, nativeStore := newRotationTestService(t, app, pool, native, identity)
	app.claudeAccounts = svc
	setClaudeAccountsCache(app, pool, nil, true)
	return app, poolStore, nativeStore, identity
}

// fakeIdentityProvider 是根色 facade 测试的身份注入 fake（token → 身份；
// 未知 token → IDENTITY_UNVERIFIED）。
type fakeIdentityProvider struct {
	mu        sync.Mutex
	responses map[string]claudeaccounts.ProfileIdentity
}

func (f *fakeIdentityProvider) FetchProfile(_ context.Context, accessToken string) (claudeaccounts.ProfileIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.responses[accessToken]; ok {
		return id, nil
	}
	return claudeaccounts.ProfileIdentity{}, claudeaccounts.NewErr(claudeaccounts.ReasonIdentityUnverified, "注入：token 无已知身份")
}

func waitClaudeOpDone(t *testing.T, app *App, opID string, timeout time.Duration) *ClaudeAccountOperationDTO {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dto, err := app.GetClaudeAccountOperation(opID)
		if err != nil {
			t.Fatalf("GetClaudeAccountOperation: %v", err)
		}
		if dto.State != "running" {
			return dto
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("operation %s did not finish within %v", opID, timeout)
	return nil
}

func TestFacade_GetClaudeAccounts_States(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)

	dto, err := app.GetClaudeAccounts()
	if err != nil {
		t.Fatalf("GetClaudeAccounts: %v", err)
	}
	if dto.Capability != "supported" || dto.State != "enabled" {
		t.Fatalf("state = %s/%s, want supported/enabled", dto.Capability, dto.State)
	}
	if dto.ActiveID != "acc-a" || dto.ActiveGeneration != 4 || dto.Revision != 2 {
		t.Fatalf("pool projection mismatch: %+v", dto)
	}
	if len(dto.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(dto.Accounts))
	}
	if dto.RotationEnabled {
		t.Fatal("rotation must default to disabled")
	}

	// enrollment → enrolling；swap journal → needs-reconcile。
	poolStore := &fakePoolStore{pool: twoAccountPool()}
	poolStore.pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionEnrollment, Enrollment: &claudeaccounts.EnrollmentJournal{TxnID: "t"}}
	refreshFacadePool(t, app, poolStore)
	if dto, _ := app.GetClaudeAccounts(); dto.State != "enrolling" {
		t.Fatalf("enrollment state = %s, want enrolling", dto.State)
	}

	poolStore2 := &fakePoolStore{pool: twoAccountPool()}
	poolStore2.pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap, Swap: &claudeaccounts.SwapJournal{TxnID: "t"}}
	refreshFacadePool(t, app, poolStore2)
	if dto, _ := app.GetClaudeAccounts(); dto.State != "needs-reconcile" {
		t.Fatalf("swap journal state = %s, want needs-reconcile", dto.State)
	}

	refreshFacadePool(t, app, &fakePoolStore{})
	if dto, _ := app.GetClaudeAccounts(); dto.State != "disabled" {
		t.Fatalf("no pool state = %s, want disabled", dto.State)
	}
}

// refreshFacadePool 用给定池存储重建服务并刷新缓存（等价启动加载）。
func refreshFacadePool(t *testing.T, app *App, poolStore *fakePoolStore) {
	t.Helper()
	dir := t.TempDir()
	client := claudeaccounts.NewConstrainedHTTPClient(nil)
	svc, err := claudeaccounts.NewService(claudeaccounts.Deps{
		Pool:       poolStore,
		Native:     &fakeNativeStore{proj: *nativeMatchingA()},
		LockPath:   dir + "/claude-accounts.lock",
		PiAuthPath: dir + "/auth.json",
		Gate:       claudeCredentialsMutationGate{app: app},
		Quiescence: claudeProcessQuiescence{app: app, lister: func() ([]claudeProcessSample, error) {
			return nil, nil
		}},
		Identity:   claudeaccounts.NewHTTPIdentityProvider(client),
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	app.claudeAccounts = svc
	app.refreshClaudeAccountsCache(context.Background())
}

func TestFacade_SwitchOperation_RequestIdIdempotencyAndLeaseConflict(t *testing.T) {
	app, _, native, _ := newFacadeTestApp(t)

	// requestId 幂等：同参数两次提交返回同一 operation。
	opA, err := app.SwitchClaudeAccount("acc-b", 4, "req-1")
	if err != nil {
		t.Fatalf("SwitchClaudeAccount: %v", err)
	}
	opB, err := app.SwitchClaudeAccount("acc-b", 4, "req-1")
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if opA != opB {
		t.Fatalf("same requestId+params must return original operation: %s vs %s", opA, opB)
	}
	// 不同参数复用 requestId → 冲突。
	if _, err := app.SwitchClaudeAccount("acc-a", 4, "req-1"); err == nil || !strings.Contains(err.Error(), "REVISION_CONFLICT") {
		t.Fatalf("conflicting params must error, got %v", err)
	}

	dto := waitClaudeOpDone(t, app, opA, 5*time.Second)
	if dto.State != "done" {
		t.Fatalf("switch op state = %s code=%s, want done (err=%+v)", dto.State, dto.Code, dto)
	}
	if !dto.Changed || dto.ActiveGeneration != 5 {
		t.Fatalf("switch op result: changed=%v generation=%d", dto.Changed, dto.ActiveGeneration)
	}
	// 原生投影已切到 B。
	if !strings.Contains(string(native.proj.ClaudeOAuth), "SECRET-CC-B") {
		t.Fatalf("native CC projection must be switched to B: %s", native.proj.ClaudeOAuth)
	}

	// 迟到重放：generation 已前移 → 领域层 REVISION_CONFLICT，不默默重放。
	opLate, err := app.SwitchClaudeAccount("acc-a", 4, "req-2")
	if err != nil {
		t.Fatalf("late replay submit: %v", err)
	}
	late := waitClaudeOpDone(t, app, opLate, 5*time.Second)
	if late.State != "error" || late.Code != string(claudeaccounts.ReasonRevisionConflict) {
		t.Fatalf("late replay op = %s/%s, want error/REVISION_CONFLICT", late.State, late.Code)
	}
}

func TestFacade_SwitchBlockedByCredentialLease(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)
	// 持有凭据运行 lease（模拟受管会话在跑）→ swap 拒绝 SESSIONS_ACTIVE。
	claim, err := app.beginClaudeCredentialsClaim("lease-test")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	defer claim.release(app)
	externalRun, err := app.sharedCoord.MintExternalRunIdentity("facade-lease-session")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := app.sharedCoord.AcquireForExternalRunWithAdmission(context.Background(), externalRun, remote.SharedServiceClaudeCredentials, claim.fingerprint, claim.admission); err != nil {
		t.Fatalf("lease: %v", err)
	}
	opID, err := app.SwitchClaudeAccount("acc-b", 4, "req-lease")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	dto := waitClaudeOpDone(t, app, opID, 5*time.Second)
	if dto.State != "error" || dto.Code != string(claudeaccounts.ReasonSessionsActive) {
		t.Fatalf("switch under lease = %s/%s, want error/SESSIONS_ACTIVE", dto.State, dto.Code)
	}
}

func TestFacade_SetClaudeAccountRotation(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)

	// 无风险确认 → 拒绝。
	if err := app.SetClaudeAccountRotation(true, 2, "wrong"); err == nil {
		t.Fatal("enable with wrong riskAckVersion must fail")
	}
	// revision 不匹配 → 拒绝。
	if err := app.SetClaudeAccountRotation(true, 99, claudeAccountsRiskAckVersion); err == nil {
		t.Fatal("enable with wrong revision must fail")
	}
	// 合法启用 → 配置落盘 + DTO 反映。
	if err := app.SetClaudeAccountRotation(true, 2, claudeAccountsRiskAckVersion); err != nil {
		t.Fatalf("enable: %v", err)
	}
	cfg := app.Config.GetClaudeAccountRotation()
	if cfg == nil || !cfg.Enabled || cfg.AcknowledgedRiskVersion == nil || *cfg.AcknowledgedRiskVersion != claudeAccountsRiskAckVersion {
		t.Fatalf("rotation config after enable: %+v", cfg)
	}
	dto, _ := app.GetClaudeAccounts()
	if !dto.RotationEnabled {
		t.Fatal("PoolDTO must reflect rotation enabled")
	}
	// 停用永远允许（安全出口）。
	if err := app.SetClaudeAccountRotation(false, 0, ""); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if app.Config.GetClaudeAccountRotation().Enabled {
		t.Fatal("rotation must be disabled")
	}
}

func TestFacade_ReconcileInspectIsZeroWrite(t *testing.T) {
	app, poolStore, native, _ := newFacadeTestApp(t)
	nativeBefore := native.proj
	result, err := app.ReconcileClaudeAccounts("inspect", 0)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if result.OperationID != "" || result.Inspect == nil {
		t.Fatalf("inspect must return synchronous report: %+v", result)
	}
	if result.Inspect.ActiveID != "acc-a" || result.Inspect.Generation != 4 {
		t.Fatalf("inspect report: %+v", result.Inspect)
	}
	if !result.Inspect.MatchesActiveSnapshot {
		t.Fatalf("native matching active snapshot must report matches=true: %+v", result.Inspect)
	}
	// 零写：池与原生未变化。
	poolAfter, _ := poolStore.Load(context.Background())
	if poolAfter.Revision != 2 || poolAfter.ActiveAccountID != "acc-a" {
		t.Fatalf("inspect must not write pool: %+v", poolAfter)
	}
	if string(native.proj.ClaudeOAuth) != string(nativeBefore.ClaudeOAuth) {
		t.Fatal("inspect must not write native slots")
	}
	// 未知动作拒绝。
	if _, err := app.ReconcileClaudeAccounts("restore-from-arbitrary-path", 0); err == nil {
		t.Fatal("unknown reconcile action must be rejected")
	}
}

func TestFacade_RemoveRenamePassthrough(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)
	// 活动账号移除被 1a 拒绝（须先切换/停止管理）。
	if err := app.RemoveClaudeAccount("acc-a", 2); err == nil || !strings.Contains(err.Error(), "REVISION_CONFLICT") {
		t.Fatalf("remove active account must fail, got %v", err)
	}
	if err := app.RenameClaudeAccount("acc-a", "别名A", 2); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// 别名长度边界（1..64 由 1a 强制）。
	if err := app.RenameClaudeAccount("acc-a", "", 3); err == nil {
		t.Fatal("empty alias must fail")
	}
	if err := app.RenameClaudeAccount("acc-a", strings.Repeat("x", 65), 3); err == nil {
		t.Fatal("65-char alias must fail")
	}
}

func TestFacade_SensitiveFieldsNeverLeaveDTO(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)
	opID, err := app.SwitchClaudeAccount("acc-b", 4, "req-sensitive")
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	waitClaudeOpDone(t, app, opID, 5*time.Second)
	dto, err := app.GetClaudeAccounts()
	if err != nil {
		t.Fatalf("GetClaudeAccounts: %v", err)
	}
	blob, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"SECRET-CC-A", "SECRET-CC-B", "SECRET-PI-A", "SECRET-PI-B", "uuid-a@"} {
		if strings.Contains(string(blob), secret) {
			t.Fatalf("PoolDTO leaked secret %q", secret)
		}
	}
	opDTO, err := app.GetClaudeAccountOperation(opID)
	if err != nil {
		t.Fatalf("GetClaudeAccountOperation: %v", err)
	}
	opBlob, _ := json.Marshal(opDTO)
	if strings.Contains(string(opBlob), "SECRET-") {
		t.Fatal("operation DTO leaked secret material")
	}
}

func TestFacade_BeginEnrollmentAndCaptureFlow(t *testing.T) {
	app, poolStore, native, identity := newFacadeTestApp(t)
	enrollmentID, err := app.BeginClaudeAccountEnrollment(2)
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	if enrollmentID == "" {
		t.Fatal("enrollment ID must be non-empty")
	}
	// enrollment 期间受影响启动关闭。
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("policy during enrollment = %v, want blocked", got)
	}
	// 原生换成 B（模拟用户在原生客户端登录 B）。
	if _, err := native.PatchClaudeKeychainOAuth(context.Background(), []byte(`{"accessToken":"SECRET-CC-B"}`)); err != nil {
		t.Fatalf("patch cc: %v", err)
	}
	if _, err := native.PatchClaudeIdentity(context.Background(), claudeaccounts.ClaudeIdentityFields{
		OauthAccount: []byte(`{"accountUuid":"uuid-b"}`), UserID: "user-b",
	}); err != nil {
		t.Fatalf("patch cj: %v", err)
	}
	if _, err := native.PatchPiAnthropic(context.Background(), []byte(`{"type":"oauth","access":"SECRET-PI-B"}`)); err != nil {
		t.Fatalf("patch pi: %v", err)
	}
	// capture 走网络身份校验：删除 B 的 Pi 身份映射，模拟 fake 身份后端不
	// 认识该 token → 预期 IDENTITY_UNVERIFIED 失败而非崩溃；验证 operation
	// 错误路径与码位透传。
	identity.mu.Lock()
	delete(identity.responses, "SECRET-PI-B")
	identity.mu.Unlock()
	opID, err := app.CaptureClaudeAccount(enrollmentID, "B-second", 3)
	if err != nil {
		t.Fatalf("CaptureClaudeAccount submit: %v", err)
	}
	dto := waitClaudeOpDone(t, app, opID, 10*time.Second)
	if dto.State != "error" {
		t.Fatalf("capture against fake identity backend should fail with typed error, got %+v", dto)
	}

	// F04（修正后的语义）：原生槽位已漂移到 B，取消录入不得无条件清 journal
	// 重开启动入口——取消本身走 mutation/静止门并分类当前投影；漂移时保留
	// enrollment（标记 drifted → needs-reconcile），返回稳定错误。
	err = app.CancelClaudeAccountEnrollment(3)
	if err == nil || !strings.Contains(err.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("cancel with drifted native must refuse with NEEDS_RECONCILE, got %v", err)
	}
	pool, _ := poolStore.Load(context.Background())
	if pool.Transaction == nil || pool.Transaction.Enrollment == nil ||
		pool.Transaction.Enrollment.TxnID != enrollmentID ||
		pool.Transaction.Enrollment.DriftedReason == "" {
		t.Fatalf("drifted enrollment journal must be retained with drift marker, got %+v", pool.Transaction)
	}
	// 门禁与 DTO 状态：保留事务 → blocked / needs-reconcile（不是 managed）。
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("policy after drifted cancel = %v, want blocked", got)
	}
	if state, _ := app.GetClaudeAccounts(); state.State != "needs-reconcile" {
		t.Fatalf("pool state after drifted cancel = %s, want needs-reconcile", state.State)
	}

	// 人工恢复路径 A：在原生客户端重新登录原账号（换回 A 投影）→ 再次取消
	//（revision 已被 drifted 标记递增到 4）→ 原生与活动源一致 → 清除成功。
	if _, err := native.PatchClaudeKeychainOAuth(context.Background(), []byte(`{"accessToken":"SECRET-CC-A"}`)); err != nil {
		t.Fatalf("restore cc: %v", err)
	}
	if _, err := native.PatchClaudeIdentity(context.Background(), claudeaccounts.ClaudeIdentityFields{
		OauthAccount: []byte(`{"accountUuid":"uuid-a"}`), UserID: "user-a",
	}); err != nil {
		t.Fatalf("restore cj: %v", err)
	}
	if _, err := native.PatchPiAnthropic(context.Background(), []byte(`{"type":"oauth","access":"SECRET-PI-A"}`)); err != nil {
		t.Fatalf("restore pi: %v", err)
	}
	if err := app.CancelClaudeAccountEnrollment(4); err != nil {
		t.Fatalf("cancel after native restored to source: %v", err)
	}
	pool, _ = poolStore.Load(context.Background())
	if pool.Transaction != nil {
		t.Fatal("enrollment journal must be cleared after clean cancel")
	}
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsManaged {
		t.Fatalf("policy after clean cancel = %v, want managed", got)
	}
}

func TestFacade_OperationNotFoundAdvisesReread(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)
	if _, err := app.GetClaudeAccountOperation("ca-nonexistent"); err == nil || !strings.Contains(err.Error(), "OPERATION_NOT_FOUND") {
		t.Fatalf("unknown operation must advise re-read, got %v", err)
	}
}
