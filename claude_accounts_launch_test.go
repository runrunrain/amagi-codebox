package main

// claude_accounts_launch_test.go — Claude 订阅账号轮换启动接线测试（design
// §6.3 + 任务书验收）：启动门禁策略、指纹、claim/复核、计划新鲜度、进程
// 观察器（fake 进程表）、受管环境边界、真实启动入口（LaunchSession/
// LaunchPiSession）在 blocked 状态关闭与恢复后放行。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
)

// ---- 测试用 fake 存储（绝不触碰真实 Keychain / ~/.claude / ~/.pi）----

type fakePoolStore struct {
	mu   sync.Mutex
	pool *claudeaccounts.AccountPool
}

func (f *fakePoolStore) Load(_ context.Context) (*claudeaccounts.AccountPool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pool == nil {
		return nil, nil
	}
	return f.pool.Clone()
}

func (f *fakePoolStore) Save(_ context.Context, pool *claudeaccounts.AccountPool) error {
	clone, err := pool.Clone()
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.pool = clone
	f.mu.Unlock()
	return nil
}

func (f *fakePoolStore) Kind() string { return "fake" }

type fakeNativeStore struct {
	mu    sync.Mutex
	proj  claudeaccounts.NativeProjections
	reads int
}

func (f *fakeNativeStore) ReadStable(_ context.Context) (*claudeaccounts.NativeProjections, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	proj := f.proj
	// 与真实 FileCredentialStore 契约一致：三处 revision 恒非 nil（内容
	// hash 稳定，连续读一致；F03 终复核依赖此不变量）。
	if proj.Revisions.ClaudeKeychain == nil {
		proj.Revisions.ClaudeKeychain = &claudeaccounts.FileRevision{SHA256Hex: claudeaccounts.HashRevision(proj.ClaudeOAuth)}
	}
	if proj.Revisions.ClaudeJSON == nil {
		var data []byte
		if proj.ClaudeIdentity != nil {
			data = append(append([]byte(nil), proj.ClaudeIdentity.OauthAccount...), []byte(proj.ClaudeIdentity.UserID)...)
		}
		proj.Revisions.ClaudeJSON = &claudeaccounts.FileRevision{SHA256Hex: claudeaccounts.HashRevision(data)}
	}
	if proj.Revisions.PiAuth == nil {
		proj.Revisions.PiAuth = &claudeaccounts.FileRevision{SHA256Hex: claudeaccounts.HashRevision(proj.PiAnthropic)}
	}
	return &proj, nil
}

func (f *fakeNativeStore) PatchClaudeKeychainOAuth(_ context.Context, oauth json.RawMessage) (*claudeaccounts.FileRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proj.ClaudeOAuth = append([]byte(nil), oauth...)
	f.proj.ClaudeOAuthRaw = append([]byte(nil), oauth...)
	rev := &claudeaccounts.FileRevision{SHA256Hex: claudeaccounts.HashRevision(oauth)}
	f.proj.Revisions.ClaudeKeychain = rev
	return rev, nil
}

func (f *fakeNativeStore) PatchClaudeIdentity(_ context.Context, fields claudeaccounts.ClaudeIdentityFields) (*claudeaccounts.FileRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proj.ClaudeIdentity = &claudeaccounts.ClaudeIdentityFields{
		OauthAccount: append([]byte(nil), fields.OauthAccount...), UserID: fields.UserID,
	}
	data := append(fields.OauthAccount, []byte(fields.UserID)...)
	rev := &claudeaccounts.FileRevision{SHA256Hex: claudeaccounts.HashRevision(data)}
	f.proj.Revisions.ClaudeJSON = rev
	return rev, nil
}

func (f *fakeNativeStore) PatchPiAnthropic(_ context.Context, anthropic json.RawMessage) (*claudeaccounts.FileRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proj.PiAnthropic = append([]byte(nil), anthropic...)
	rev := &claudeaccounts.FileRevision{SHA256Hex: claudeaccounts.HashRevision(anthropic)}
	f.proj.Revisions.PiAuth = rev
	return rev, nil
}

func (f *fakeNativeStore) Kind() string { return "fake" }

// newRotationTestService 构造 fake 后端的 1a 服务（Gate/Quiescence 用生产
// 实现接线到 app，路径注入临时目录，绝不触碰真实凭据）。可选注入 fake
// 身份提供者（F05：换入前目标双 grant 现场验证需要可控身份响应）。
func newRotationTestService(t *testing.T, app *App, pool *claudeaccounts.AccountPool, native *claudeaccounts.NativeProjections, identity ...claudeaccounts.IdentityProvider) (*claudeaccounts.Service, *fakePoolStore, *fakeNativeStore) {
	t.Helper()
	dir := t.TempDir()
	poolStore := &fakePoolStore{pool: pool}
	nativeStore := &fakeNativeStore{proj: *native}
	client := claudeaccounts.NewConstrainedHTTPClient(nil)
	identityProvider := claudeaccounts.IdentityProvider(claudeaccounts.NewHTTPIdentityProvider(client))
	if len(identity) > 0 && identity[0] != nil {
		identityProvider = identity[0]
	}
	svc, err := claudeaccounts.NewService(claudeaccounts.Deps{
		Pool:       poolStore,
		Native:     nativeStore,
		LockPath:   dir + "/claude-accounts.lock",
		PiAuthPath: dir + "/auth.json",
		Gate:       claudeCredentialsMutationGate{app: app},
		Quiescence: claudeProcessQuiescence{
			app:         app,
			lister:      app.claudeAccountsProcessSamples,
			attribution: app.claudeQuiescenceAttribution,
		},
		Identity:   identityProvider,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, poolStore, nativeStore
}

// setClaudeAccountsCache 直接注入启动门禁缓存状态（等价 refresh 的产物）。
func setClaudeAccountsCache(a *App, pool *claudeaccounts.AccountPool, loadErr error, recovered bool) {
	a.claudeAccountsMu.Lock()
	a.claudeAccountsPool = pool
	a.claudeAccountsLoadErr = loadErr
	a.claudeAccountsRecovered = recovered
	a.claudeAccountsMu.Unlock()
}

func testAccountPoolWithActive() *claudeaccounts.AccountPool {
	pool := claudeaccounts.NewAccountPool()
	pool.ActiveAccountID = "acc-a"
	pool.ActiveGeneration = 4
	pool.Revision = 2
	pool.Accounts["acc-a"] = &claudeaccounts.AccountSnapshot{
		ID: "acc-a", Alias: "A",
		Claude:         claudeaccounts.ClaudeGrantState{OauthProjection: []byte(`{"accessToken":"tok-a"}`)},
		Pi:             claudeaccounts.PiGrantState{Anthropic: []byte(`{"type":"oauth"}`)},
		ClaudeIdentity: claudeaccounts.ClaudeIdentityFields{OauthAccount: []byte(`{"accountUuid":"uuid-a"}`), UserID: "user-a"},
	}
	return pool
}

// ---- 策略与门禁 ----

func newGateTestApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.Capabilities = platform.PlatformCapabilities{
		PlatformID:                  "claude-accounts-gate-test",
		EmbeddedTerminalSupported:   true,
		StandaloneTerminalSupported: true,
	}
	// 进程观察器注入空 fake 进程表（design §10.1：不依赖真实系统进程；
	// 开发机上真实 claude 进程会让 swap 测试不确定）。
	app.claudeAccountsQuiescenceLister = func() ([]claudeProcessSample, error) { return nil, nil }
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	app.sharedLeases = map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease{}
	pool := testAccountPoolWithActive()
	native := &claudeaccounts.NativeProjections{
		ClaudeOAuth:    []byte(`{"accessToken":"tok-a"}`),
		ClaudeIdentity: &claudeaccounts.ClaudeIdentityFields{OauthAccount: []byte(`{"accountUuid":"uuid-a"}`), UserID: "user-a"},
		PiAnthropic:    []byte(`{"type":"oauth"}`),
	}
	svc, _, _ := newRotationTestService(t, app, pool, native)
	app.claudeAccounts = svc
	return app
}

func TestClaudeCredentialsPolicy_States(t *testing.T) {
	app := newGateTestApp(t)

	// 无服务（newTestApp 场景）→ absent（零行为变化）。
	bare := newTestApp(t)
	if got := bare.claudeCredentialsPolicy(); got != claudeCredentialsAbsent {
		t.Fatalf("bare app policy = %v, want absent", got)
	}

	// 池存在 + 已恢复 + 无事务 → managed。
	setClaudeAccountsCache(app, testAccountPoolWithActive(), nil, true)
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsManaged {
		t.Fatalf("managed pool policy = %v, want managed", got)
	}

	// 未恢复（启动恢复未完成）→ blocked。
	setClaudeAccountsCache(app, testAccountPoolWithActive(), nil, false)
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("unrecovered policy = %v, want blocked", got)
	}

	// 未结事务（swap/enrollment）→ blocked（design §4.1 暂停受影响启动）。
	pool := testAccountPoolWithActive()
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionEnrollment, Enrollment: &claudeaccounts.EnrollmentJournal{TxnID: "txn-1"}}
	setClaudeAccountsCache(app, pool, nil, true)
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("enrollment policy = %v, want blocked", got)
	}

	// 池加载失败（损坏/拒绝）→ blocked。
	setClaudeAccountsCache(app, nil, claudeaccounts.NewErr(claudeaccounts.ReasonPoolCorrupt, "x"), true)
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("corrupt pool policy = %v, want blocked", got)
	}

	// 无池（从未启用）→ absent。
	setClaudeAccountsCache(app, nil, nil, true)
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsAbsent {
		t.Fatalf("no pool policy = %v, want absent", got)
	}
}

func TestClaudeCredentialsFingerprint_StableAndGenerationBound(t *testing.T) {
	paths := claudeaccounts.FileCredentialPaths{ClaudeJSONPath: "/home/u/.claude.json", PiAuthPath: "/home/u/.pi/agent/auth.json"}
	gen5a := claudeCredentialsFingerprintFor(paths, 5)
	gen5b := claudeCredentialsFingerprintFor(paths, 5)
	if gen5a != gen5b {
		t.Fatal("same paths+generation must yield identical fingerprint")
	}
	if gen5a == claudeCredentialsFingerprintFor(paths, 6) {
		t.Fatal("different generation must change fingerprint (activeGeneration is part of it)")
	}
	other := paths
	other.PiAuthPath = "/elsewhere/auth.json"
	if gen5a == claudeCredentialsFingerprintFor(other, 5) {
		t.Fatal("different canonical path must change fingerprint")
	}
}

func TestClaudeCredentialsClaim_AcquireRecheckRelease(t *testing.T) {
	app := newGateTestApp(t)
	setClaudeAccountsCache(app, testAccountPoolWithActive(), nil, true)

	claim, err := app.beginClaudeCredentialsClaim("test")
	if err != nil {
		t.Fatalf("managed claim: %v", err)
	}
	if claim == nil {
		t.Fatal("managed claim must be non-nil")
	}
	if got := app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceClaudeCredentials); got != 1 {
		t.Fatalf("pending admissions = %d, want 1", got)
	}

	// mutation admission 被 pending claim 阻塞（admission→spawn 间插 swap 拒绝）。
	if _, err := app.sharedCoord.AcquireMutationAdmission(remote.SharedServiceClaudeCredentials, remote.MutationStop); !errors.Is(err, remote.ErrSharedServiceInUse) {
		t.Fatalf("mutation during pending claim: %v", err)
	}

	// 代际未变 → 复核通过；代际变化 → 重新规划错误。
	if err := app.recheckClaudeCredentialsClaim(claim); err != nil {
		t.Fatalf("recheck same generation: %v", err)
	}
	pool2 := testAccountPoolWithActive()
	pool2.ActiveGeneration = 5
	setClaudeAccountsCache(app, pool2, nil, true)
	if err := app.recheckClaudeCredentialsClaim(claim); !errors.Is(err, errClaudeCredentialsGenerationChanged) {
		t.Fatalf("recheck changed generation: %v", err)
	}
	if err := app.claudeCredentialsPlanCheck(claim.fingerprint); !errors.Is(err, errClaudeCredentialsGenerationChanged) {
		t.Fatalf("plan check changed generation: %v", err)
	}

	claim.release(app)
	claim.release(app) // 幂等
	if got := app.sharedCoord.LaunchAdmissionCount(remote.SharedServiceClaudeCredentials); got != 0 {
		t.Fatalf("pending admissions after release = %d, want 0", got)
	}
}

func TestClaudeCredentialsClaim_BlockedReturnsStableError(t *testing.T) {
	app := newGateTestApp(t)
	pool := testAccountPoolWithActive()
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap, Swap: &claudeaccounts.SwapJournal{TxnID: "txn"}}
	setClaudeAccountsCache(app, pool, nil, true)
	_, err := app.beginClaudeCredentialsClaim("test")
	if err == nil || !strings.Contains(err.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("blocked claim must return stable NEEDS_RECONCILE error, got %v", err)
	}
	// 计划新鲜度接缝在 blocked 下同样拒绝（remote 入口关闭）。
	if err := app.claudeCredentialsPlanCheck([32]byte{}); err == nil {
		t.Fatal("plan check must reject while blocked")
	}
}

// ---- 真实启动入口：blocked 关闭、恢复后放行 ----

func TestLaunchSession_BlockedWhilePoolNeedsReconcile(t *testing.T) {
	app := newGateTestApp(t)
	pool := testAccountPoolWithActive()
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap, Swap: &claudeaccounts.SwapJournal{TxnID: "txn"}}
	setClaudeAccountsCache(app, pool, nil, true)

	if err := app.Config.SaveProvider("blocked-provider", config.Provider{
		Type: "anthropic", BaseURL: "https://api.anthropic.com", AuthKey: "ANTHROPIC_API_KEY", DefaultModel: "claude-x",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey("blocked-provider", "sk-test"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	_, err := app.LaunchSession("blocked-provider", "", "terminal", t.TempDir(), false, "")
	if err == nil || !strings.Contains(err.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("LaunchSession during needs-reconcile must fail closed, got %v", err)
	}
}

func TestLaunchPiSession_BlockedWhilePoolNeedsReconcile(t *testing.T) {
	app := newGateTestApp(t)
	pool := testAccountPoolWithActive()
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap, Swap: &claudeaccounts.SwapJournal{TxnID: "txn"}}
	setClaudeAccountsCache(app, pool, nil, true)
	_, err := app.LaunchPiSession("some-model", "", "terminal", t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("LaunchPiSession during needs-reconcile must fail closed, got %v", err)
	}
}

func TestLaunchEntries_ReopenAfterRecovery(t *testing.T) {
	app := newGateTestApp(t)
	// 恢复完成：无事务 → managed → claim 成功（受影响启动入口重新放行）。
	setClaudeAccountsCache(app, testAccountPoolWithActive(), nil, true)
	claim, err := app.beginClaudeCredentialsClaim("reopen")
	if err != nil || claim == nil {
		t.Fatalf("claim after recovery: %v", err)
	}
	claim.release(app)

	// LaunchSession 继续越过门禁（后续失败必须是别的原因，不含账号池错误）。
	// 非 headroom 分支会触碰共享 headroom 下线检查——注入假 runner。
	app.Headroom = headroom.NewHeadroomService(&r5CountingHeadroomRunner{}, app.Log)
	t.Cleanup(func() { _ = app.Headroom.Stop() })
	if err := app.Config.SaveProvider("reopen-provider", config.Provider{
		Type: "anthropic", BaseURL: "https://api.anthropic.com", AuthKey: "ANTHROPIC_API_KEY", DefaultModel: "claude-x",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey("reopen-provider", "sk-test"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	_, launchErr := app.LaunchSession("reopen-provider", "", "terminal", t.TempDir(), false, "")
	if launchErr != nil && strings.Contains(launchErr.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("launch must not be blocked by account pool after recovery: %v", launchErr)
	}
}

// ---- 进程观察器（fake 进程表）----

func TestQuiescence_FakeProcessTable(t *testing.T) {
	app := newGateTestApp(t)
	check := claudeProcessQuiescence{
		app: app,
		lister: func() ([]claudeProcessSample, error) {
			return []claudeProcessSample{
				{PID: 100, PPID: 1, Args: "/usr/local/bin/claude --session-id x"},
			}, nil
		},
		attribution: func() map[int]bool { return map[int]bool{} },
	}
	if err := check.Check(context.Background()); err == nil {
		t.Fatal("unattributed claude process must be EXTERNAL_SESSION_UNCERTAIN")
	} else if domain, ok := claudeaccounts.AsError(err); !ok || domain.Reason != claudeaccounts.ReasonExternalSessionUncertain {
		t.Fatalf("expected EXTERNAL_SESSION_UNCERTAIN, got %v", err)
	}

	// 归因进程（CodeBox 会话）→ 允许（lease 门禁另有 coordinator 负责）。
	check.attribution = func() map[int]bool { return map[int]bool{100: true} }
	if err := check.Check(context.Background()); err != nil {
		t.Fatalf("attributed claude process should pass, got %v", err)
	}

	// 无匹配进程 → 允许。
	check.lister = func() ([]claudeProcessSample, error) {
		return []claudeProcessSample{{PID: 200, PPID: 1, Args: "/bin/zsh -il"}}, nil
	}
	if err := check.Check(context.Background()); err != nil {
		t.Fatalf("no claude/pi process should pass, got %v", err)
	}

	// pi 进程（canonical ~/.pi/agent 消费者）→ uncertain。
	check.lister = func() ([]claudeProcessSample, error) {
		return []claudeProcessSample{{PID: 201, PPID: 1, Args: "/opt/homebrew/bin/pi --provider anthropic"}}, nil
	}
	if err := check.Check(context.Background()); err == nil {
		t.Fatal("unattributed pi process must be EXTERNAL_SESSION_UNCERTAIN")
	}

	// 存活后代（未归因 claude 的子进程，无论名字）→ uncertain。
	check.lister = func() ([]claudeProcessSample, error) {
		return []claudeProcessSample{
			{PID: 300, PPID: 1, Args: "/usr/local/bin/claude"},
			{PID: 301, PPID: 300, Args: "/usr/bin/python3 helper"},
		}, nil
	}
	if err := check.Check(context.Background()); err == nil {
		t.Fatal("live descendants of unattributed claude must be uncertain")
	}

	// 扫描失败 → uncertain（不自动切换）。
	check.lister = func() ([]claudeProcessSample, error) { return nil, errors.New("ps unavailable") }
	if err := check.Check(context.Background()); err == nil {
		t.Fatal("scan failure must be EXTERNAL_SESSION_UNCERTAIN")
	}
}

// ---- 受管环境边界（design §6.3 R3）----

func TestManagedClaudeIdentityBoundary(t *testing.T) {
	env := []string{
		"CLAUDE_CODE_DEVICE_ID=dev-x",
		"CLAUDE_CODE_ACCOUNT_UUID=uuid-x",
		"CLAUDE_CODE_OAUTH_TOKEN=secret-token",
		"CLAUDE_CONFIG_DIR=/other/root",
		"ANTHROPIC_MODEL=claude-x", // 模型参数不动
	}
	got := stripEnvKeys(env, managedClaudeIdentityEnvKeys)
	if len(got) != 1 || got[0] != "ANTHROPIC_MODEL=claude-x" {
		t.Fatalf("stripEnvKeys must only drop identity/auth keys, got %v", got)
	}

	if override := managedHomeOverride([]string{"HOME=/elsewhere"}); override != "HOME" {
		t.Fatalf("HOME override detection = %q, want HOME", override)
	}
	if override := managedHomeOverride(nil); override != "" {
		t.Fatalf("no override expected, got %q", override)
	}
	if override := managedHomeOverride([]string{"OTHER=1"}); override != "" {
		t.Fatalf("unrelated env must not trigger, got %q", override)
	}
}
