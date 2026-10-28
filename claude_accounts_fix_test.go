package main

// claude_accounts_fix_test.go — Phase 1 修复批次（diting F01/F02/F06/F07/F09/
// F10/F12/F15）的根包反例与接缝测试。全部 fake 后端（设计 §10.1：不触真实
// Keychain/原生凭据）。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

// ---- F01：mutation 释放前发布权威状态 ----

// TestF01_LaunchBlockedImmediatelyAfterFailedMutation：失败事务留 journal →
// mutation gate 释放（release）前必须已发布权威 blocked 状态 → 紧随其后的
// launch claim 直接 blocked，不存在「释放 admission→缓存刷新」交接窗口。
// 失败事务代际不增加，仅比代际放不住半切换会话。
func TestF01_LaunchBlockedImmediatelyAfterFailedMutation(t *testing.T) {
	app, poolStore, _, _ := newFacadeTestApp(t)
	// 接线池存储直接引用：gate release 的权威发布路径生效（F01）。
	app.claudeAccountsPoolStore = poolStore
	app.claudeAccountsPoolMarkerPath = "" // F10 标记不参与本测试

	pool, _ := poolStore.Load(context.Background())
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap,
		Swap: &claudeaccounts.SwapJournal{TxnID: "txn-f01", SourceID: "acc-a", TargetID: "acc-b"}}
	if err := poolStore.Save(context.Background(), pool); err != nil {
		t.Fatal(err)
	}

	// 模拟一笔 mutation：取 gate 后释放——release 内必须在释放 admission 之前
	// 把权威 blocked 状态发布进缓存。
	release, err := claudeCredentialsMutationGate{app: app}.Acquire(context.Background(), "f01-test")
	if err != nil {
		t.Fatalf("gate acquire: %v", err)
	}
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsManaged {
		t.Fatalf("pre-release policy = %v, want managed (stale cache)", got)
	}
	release()

	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("post-release policy = %v, want blocked (authoritative publish)", got)
	}
	if _, err := app.beginClaudeCredentialsClaim("f01-after-mutation"); err == nil || !strings.Contains(err.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("claim right after mutation must be blocked, got %v", err)
	}
}

// TestF01_AbsenceCheckAndRecheckCoverBlocked：无凭据计划边界接缝 + claim 复核
// 覆盖 blocked（不只比代际）。
func TestF01_AbsenceCheckAndRecheckCoverBlocked(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)

	// absent → absence check 通过；managed → 拒绝（absent 旧计划不得执行）。
	setClaudeAccountsCache(app, nil, nil, true)
	if err := app.claudeCredentialsAbsenceCheck(); err != nil {
		t.Fatalf("absent must pass absence check, got %v", err)
	}
	setClaudeAccountsCache(app, testAccountPoolWithActive(), nil, true)
	if err := app.claudeCredentialsAbsenceCheck(); !errors.Is(err, errClaudeCredentialsGenerationChanged) {
		t.Fatalf("managed must fail absence check (absent stale plan), got %v", err)
	}

	// claim 复核：失败事务代际不变但状态 blocked → recheck 必须拒绝。
	claim, err := app.beginClaudeCredentialsClaim("recheck-test")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	defer claim.release(app)
	blockedPool := testAccountPoolWithActive()
	blockedPool.ActiveGeneration = 4 // 与 claim 读取的代际一致（不变）
	blockedPool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap,
		Swap: &claudeaccounts.SwapJournal{TxnID: "txn"}}
	setClaudeAccountsCache(app, blockedPool, nil, true)
	if err := app.recheckClaudeCredentialsClaim(claim); err == nil || !strings.Contains(err.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("recheck must reject blocked even with unchanged generation, got %v", err)
	}
}

// ---- F02：absent 状态下已运行会话不再被静止检查豁免 ----

// TestF02_QuiescenceAttributesOnlyCredentialLeaseHolders：归因集合只含持有
// SharedServiceClaudeCredentials lease 的会话；无 lease 的 CodeBox 会话（absent
// 状态启动）计入 external → EXTERNAL_SESSION_UNCERTAIN。
func TestF02_QuiescenceAttributesOnlyCredentialLeaseHolders(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)

	// 一个「已运行」的 CodeBox claude 会话（absent 时启动，无凭据 lease）。
	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "", "m", session.ModeEmbedded, t.TempDir())
	app.Sessions.SetPID(sess.ID, 4242)
	defer app.Sessions.Remove(sess.ID)

	app.claudeAccountsQuiescenceLister = func() ([]claudeProcessSample, error) {
		return []claudeProcessSample{{PID: 4242, PPID: 1, Args: "/usr/local/bin/claude --session-id x"}}, nil
	}
	setClaudeAccountsCache(app, nil, nil, true) // 池 absent（从未启用）
	q := claudeProcessQuiescence{
		app:         app,
		lister:      app.claudeAccountsProcessSamples,
		attribution: app.claudeQuiescenceAttribution,
	}
	if err := q.Check(context.Background()); err == nil ||
		claudeaccounts.ReasonOf(err) != claudeaccounts.ReasonExternalSessionUncertain {
		t.Fatalf("absent-era session without lease must be EXTERNAL_SESSION_UNCERTAIN, got %v", err)
	}

	// 持有凭据 lease 的会话被归因豁免（mutation 门由 coordinator 承担）。
	// 本段验证归因机制：会话转为受管（池启用 + 真实提升链路的 lease）。
	setClaudeAccountsCache(app, testAccountPoolWithActive(), nil, true)
	lease := registerTestCredentialLease(t, app, sess.ID)
	app.rememberSharedLease(sess.ID, lease)
	if err := q.Check(context.Background()); err != nil {
		t.Fatalf("lease-holding session should be attributed, got %v", err)
	}
	app.releaseSharedLeases(sess.ID)
}

// TestF02_FirstCaptureFromAbsentWithRunningSessionBlocked：正式反例——从 absent
// 的真实状态进入 capture（非预置 managed cache）：池 store 为空 + 运行中 claude
// 进程 → CaptureFirstAccount 拒绝（§1.3-7/§4.3 所有消费进程静止）。
func TestF02_FirstCaptureFromAbsentWithRunningSessionBlocked(t *testing.T) {
	app := newGateTestApp(t)
	svc, _, _ := newRotationTestService(t, app, nil, &claudeaccounts.NativeProjections{
		ClaudeOAuth: []byte(`{"accessToken":"tok-a"}`),
		PiAnthropic: []byte(`{"type":"oauth","access":"pi-a"}`),
	})
	app.claudeAccounts = svc
	setClaudeAccountsCache(app, nil, nil, true)
	app.claudeAccountsPoolStore = nil // 走 service 路径刷新

	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "", "m", session.ModeEmbedded, t.TempDir())
	app.Sessions.SetPID(sess.ID, 4343)
	defer app.Sessions.Remove(sess.ID)
	app.claudeAccountsQuiescenceLister = func() ([]claudeProcessSample, error) {
		return []claudeProcessSample{{PID: 4343, PPID: 1, Args: "/opt/homebrew/bin/claude"}}, nil
	}

	opID, err := app.CaptureClaudeAccount("", "first", 0)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	dto := waitClaudeOpDone(t, app, opID, 5*time.Second)
	if dto.State != "error" || dto.Code != string(claudeaccounts.ReasonExternalSessionUncertain) {
		t.Fatalf("absent-era running session must block first capture, got %+v", dto)
	}
}

// ---- F06：恢复 outcome 消费与 expectedRevision ----

// TestF06_CompleteTargetBlockedWhenLeaseHeld：journal 存在且凭据 admission/lease
// 被持 → complete-target 的 operation 终态是 blocked（等待），不是 done。
func TestF06_CompleteTargetBlockedWhenLeaseHeld(t *testing.T) {
	app, poolStore, _, _ := newFacadeTestApp(t)
	// 先在 managed 状态取得凭据 admission（模拟受管会话在跑），再让池出现
	// 未结 journal（失败事务）——恢复需 mutation admission 但被 lease 阻塞。
	claim, err := app.beginClaudeCredentialsClaim("f06-lease")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	defer claim.release(app)

	pool, _ := poolStore.Load(context.Background())
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap,
		Swap: &claudeaccounts.SwapJournal{TxnID: "txn-f06", SourceID: "acc-a", TargetID: "acc-b", FromGeneration: 4}}
	if err := poolStore.Save(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	setClaudeAccountsCache(app, pool, nil, true)

	res, err := app.ReconcileClaudeAccounts("complete-target", pool.Revision)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	dto := waitClaudeOpDone(t, app, res.OperationID, 5*time.Second)
	if dto.State != "blocked" {
		t.Fatalf("complete-target with held lease must end blocked, got %+v", dto)
	}
	if dto.Changed {
		t.Fatal("blocked operation must not claim changed=true")
	}
	pool2, _ := poolStore.Load(context.Background())
	if pool2.Transaction == nil {
		t.Fatal("journal must be retained while blocked")
	}
}

// TestF06_RestoreSourceAllBeforeClearsJournal：全 before journal → restore-source
// 先清 journal 再 no-op → done，入口重新打开（此前提前 no-op 返回 done 但
// journal 残留、入口永远 blocked）。
func TestF06_RestoreSourceAllBeforeClearsJournal(t *testing.T) {
	app, poolStore, _, _ := newFacadeTestApp(t)
	pool, _ := poolStore.Load(context.Background())
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap,
		Swap: &claudeaccounts.SwapJournal{
			TxnID: "txn-f06b", SourceID: "acc-a", TargetID: "acc-b",
			FromGeneration: 4, Phase: claudeaccounts.SwapPrepared,
			Before: pool.Accounts["acc-a"].OwnedProjectionsOf(),
			After:  pool.Accounts["acc-b"].OwnedProjectionsOf(),
		}}
	pool.Revision++
	if err := poolStore.Save(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	setClaudeAccountsCache(app, pool, nil, true)
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("pre-restore policy = %v, want blocked", got)
	}

	res, err := app.ReconcileClaudeAccounts("restore-source", pool.Revision)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	dto := waitClaudeOpDone(t, app, res.OperationID, 5*time.Second)
	if dto.State != "done" {
		t.Fatalf("restore-source all-before must be done, got %+v", dto)
	}
	pool2, _ := poolStore.Load(context.Background())
	if pool2.Transaction != nil {
		t.Fatal("journal must be cleared before no-op done")
	}
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsManaged {
		t.Fatalf("post-restore policy = %v, want managed", got)
	}
}

// TestF06_ReconcileActionsRejectStaleRevision：陈旧 expectedRevision 的恢复命令
// 不得作用于已变化的事务。
func TestF06_ReconcileActionsRejectStaleRevision(t *testing.T) {
	app, poolStore, _, _ := newFacadeTestApp(t)
	pool, _ := poolStore.Load(context.Background())
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionSwap,
		Swap: &claudeaccounts.SwapJournal{TxnID: "txn-f06c", SourceID: "acc-a", TargetID: "acc-b"}}
	pool.Revision++
	if err := poolStore.Save(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	setClaudeAccountsCache(app, pool, nil, true)

	for _, action := range []string{"complete-target", "restore-source"} {
		res, err := app.ReconcileClaudeAccounts(action, pool.Revision+9)
		if err != nil {
			t.Fatalf("%s submit: %v", action, err)
		}
		dto := waitClaudeOpDone(t, app, res.OperationID, 5*time.Second)
		if dto.State != "error" || dto.Code != string(claudeaccounts.ReasonRevisionConflict) {
			t.Fatalf("%s with stale revision must error REVISION_CONFLICT, got %+v", action, dto)
		}
	}
	pool2, _ := poolStore.Load(context.Background())
	if pool2.Transaction == nil {
		t.Fatal("stale command must not clear/alter the journal")
	}
}

// ---- F07：计划/最终命令的身份环境边界 ----

// TestF07_ClaudePlanStripsManagedIdentityEnv：受管 OAuth 的 remote Claude 计划：
// 用户自定义 envvars 中的身份/认证覆盖变量不得进入最终 spec（remote
// create/restart 与桌面 embedded 同一边界）。
func TestF07_ClaudePlanStripsManagedIdentityEnv(t *testing.T) {
	app := newGateTestApp(t)
	setClaudeAccountsCache(app, testAccountPoolWithActive(), nil, true)

	homeDir := isolatedHome(t)
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := cfgSvc.SaveProvider("oauth-provider", config.Provider{
		Anthropic: &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.anthropic.com", AuthKey: config.AuthTypeOAuth},
		DefaultModel: "claude-x",
		Presets:      map[string]config.Preset{"oauth-preset": {Name: "oauth-preset", Model: "claude-x"}},
	}); err != nil {
		t.Fatal(err)
	}
	secSvc := secrets.NewSecretsService(dir)
	settingsSvc := settings.NewService(dir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatal(err)
	}
	envSvc := envvars.NewEnvVarsService(dir)
	for _, kv := range managedClaudeIdentityEnvKeys {
		if err := envSvc.Set(kv, kv+"-evil"); err != nil {
			t.Fatalf("Set %s: %v", kv, err)
		}
	}
	defaults, err := launchplan.NewDefaultStore(settingsSvc)
	if err != nil {
		t.Fatal(err)
	}
	if err := defaults.RecordDesktopActivation(launchplan.StableRecipe{
		CLIType: contract.CLITypeClaudeCode, Workdir: dir,
		ProviderRef: "oauth-provider", PresetRef: "oauth-preset", ModelRef: "claude-x", ShellRef: "/bin/sh",
	}); err != nil {
		t.Fatal(err)
	}
	planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envSvc, homeDir)
	planner.claudeCredentialAdmission = app.claudeCredentialsPlanAdmission
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan failure: %#v", failure)
	}
	defer plan.Secrets.Dispose()
	checked := 0
	for _, eff := range plan.Effects {
		if eff.Process == nil {
			continue
		}
		checked++
		for _, kv := range eff.Process.Resolved.Env.Variables {
			k, _, _ := strings.Cut(kv, "=")
			for _, key := range managedClaudeIdentityEnvKeys {
				if strings.EqualFold(k, key) {
					t.Fatalf("managed OAuth plan env still carries identity key %s", key)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("plan must carry a process effect")
	}
	// 非受管（absent）对照：身份键保留（零行为变化）。
	setClaudeAccountsCache(app, nil, nil, true)
	plan2, failure2 := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure2 != nil {
		t.Fatalf("absent BuildPlan failure: %#v", failure2)
	}
	defer plan2.Secrets.Dispose()
	found := false
	for _, eff := range plan2.Effects {
		if eff.Process == nil {
			continue
		}
		for _, kv := range eff.Process.Resolved.Env.Variables {
			if strings.HasPrefix(kv, "CLAUDE_CODE_ACCOUNT_UUID=") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("absent plan must keep user env unchanged (zero behavior change)")
	}
}

// TestF07_PiLoginBoundaryHelpersCarryManagedKeys：受管 Pi 登录：env 构建
//（piLoginEnvOverrides）与最终重锚（piLoginReAnchorSpec）同步剥离身份变量。
func TestF07_PiLoginBoundaryHelpersCarryManagedKeys(t *testing.T) {
	env := piLoginEnvOverrides("/canonical/.pi/agent", managedClaudeIdentityEnvKeys...)
	for _, key := range managedClaudeIdentityEnvKeys {
		if v, ok := env[key]; !ok || v != "" {
			t.Fatalf("piLoginEnvOverrides must unset %s (empty override), got %q", key, v)
		}
	}
	spec := platform.ResolvedLaunchSpec{
		BootstrapMode:  platform.BootstrapShellInline,
		Shell:          &platform.ResolvedShell{Key: "zsh", Path: "/bin/zsh"},
		Env:            platform.ResolvedEnv{Variables: []string{"CLAUDE_CODE_ACCOUNT_UUID=uuid-evil", "HOME=/h"}},
		StartupCommand: "claude-evil-start",
	}
	spec = piLoginReAnchorSpec(spec, "/canonical/.pi/agent", managedClaudeIdentityEnvKeys...)
	joined := strings.Join(spec.Env.Variables, "\n") + "\n" + spec.StartupCommand
	for _, key := range managedClaudeIdentityEnvKeys {
		if !strings.Contains(joined, "-u "+key) {
			t.Fatalf("re-anchor must strip %s after shell init: %s", key, joined)
		}
	}
}

// TestF07_ExternalLaunchStripsManagedIdentityEnv：外部终端路径在最终 env 剥离
// 受管身份键（与 embedded 的 managedClaudeIdentityBoundarySpec 同一键集）；
// 非受管（nil 键集）零变化。
func TestF07_ExternalLaunchStripsManagedIdentityEnv(t *testing.T) {
	base := []string{
		"CLAUDE_CODE_ACCOUNT_UUID=uuid-evil",
		"CLAUDE_CODE_DEVICE_ID=dev-evil",
		"CLAUDE_CONFIG_DIR=/other/root",
		"ANTHROPIC_MODEL=claude-x",
		"PATH=/usr/bin",
	}
	got := stripEnvKeys(base, managedClaudeIdentityEnvKeys)
	if len(got) != 2 {
		t.Fatalf("managed strip must drop identity keys, got %v", got)
	}
	if same := stripEnvKeys(base, nil); len(same) != len(base) {
		t.Fatalf("unmanaged (nil) must be zero-change, got %v", same)
	}
}

// ---- F09：重启/刷新后续接 enrollment ----

// TestF09_PoolDTOCarriesEnrollmentId：持久化 enrollment 的 TxnID 进入安全 DTO；
// drifted enrollment 不返回续接 ID（needs-reconcile）。
func TestF09_PoolDTOCarriesEnrollmentId(t *testing.T) {
	app, poolStore, _, _ := newFacadeTestApp(t)
	pool, _ := poolStore.Load(context.Background())
	pool.Transaction = &claudeaccounts.PoolTransaction{Kind: claudeaccounts.TransactionEnrollment,
		Enrollment: &claudeaccounts.EnrollmentJournal{TxnID: "enroll-resume", Harvested: true}}
	pool.Revision++
	if err := poolStore.Save(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	setClaudeAccountsCache(app, pool, nil, true)

	dto, err := app.GetClaudeAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if dto.State != "enrolling" || dto.EnrollmentID != "enroll-resume" {
		t.Fatalf("DTO must expose resumable enrollmentId, got state=%s id=%q", dto.State, dto.EnrollmentID)
	}

	pool2, _ := poolStore.Load(context.Background())
	pool2.Transaction.Enrollment.DriftedReason = string(claudeaccounts.ReasonExternalAccountChange)
	pool2.Revision++
	if err := poolStore.Save(context.Background(), pool2); err != nil {
		t.Fatal(err)
	}
	setClaudeAccountsCache(app, pool2, nil, true)
	dto2, _ := app.GetClaudeAccounts()
	if dto2.State != "needs-reconcile" || dto2.EnrollmentID != "" {
		t.Fatalf("drifted enrollment must be needs-reconcile without resume id, got state=%s id=%q", dto2.State, dto2.EnrollmentID)
	}
}

// TestF09_EnrollmentSurvivesBackendRebuild：开始录入 → 重建 App/service 读取
//（模拟重启/刷新）→ 凭 DTO enrollmentId 续接保存成功。
func TestF09_EnrollmentSurvivesBackendRebuild(t *testing.T) {
	app, poolStore, native, identity := newFacadeTestApp(t)
	app.claudeAccountsPoolStore = poolStore

	enrollmentID, err := app.BeginClaudeAccountEnrollment(2)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// 模拟重启/刷新：重建 facade 缓存（新 App 等价）——DTO 仍能给出续接 ID。
	setClaudeAccountsCache(app, nil, nil, false)
	app.refreshClaudeAccountsCache(context.Background())
	dto, err := app.GetClaudeAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if dto.EnrollmentID != enrollmentID {
		t.Fatalf("rebuild must expose persisted enrollmentId %q, got %q", enrollmentID, dto.EnrollmentID)
	}

	// 原生登录 B（身份映射齐备）→ 凭续接 ID 完成保存。
	if _, err := native.PatchClaudeKeychainOAuth(context.Background(), []byte(`{"accessToken":"SECRET-CC-B"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := native.PatchClaudeIdentity(context.Background(), claudeaccounts.ClaudeIdentityFields{
		OauthAccount: []byte(`{"accountUuid":"uuid-b"}`), UserID: "user-b",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := native.PatchPiAnthropic(context.Background(), []byte(`{"type":"oauth","access":"SECRET-PI-B"}`)); err != nil {
		t.Fatal(err)
	}
	_ = identity
	pool, _ := poolStore.Load(context.Background())
	opID, err := app.CaptureClaudeAccount(dto.EnrollmentID, "B-resumed", pool.Revision)
	if err != nil {
		t.Fatalf("resumed capture submit: %v", err)
	}
	op := waitClaudeOpDone(t, app, opID, 10*time.Second)
	if op.State != "done" {
		t.Fatalf("resumed capture must succeed, got %+v", op)
	}
}

// ---- F10：已创建池被外部删除 → fail closed ----

// TestF10_PoolMissingAfterExternalDeletionFailsClosed：标记存在 + 池消失 →
// POOL_MISSING blocked（不当未启用）；首次 capture 拒绝重建空池。
func TestF10_PoolMissingAfterExternalDeletionFailsClosed(t *testing.T) {
	app, poolStore, _, _ := newFacadeTestApp(t)
	markerPath := t.TempDir() + "/pool-initialized"
	app.claudeAccountsPoolMarkerPath = markerPath
	app.claudeAccountsPoolStore = poolStore

	app.refreshClaudeAccountsCache(context.Background())
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsManaged {
		t.Fatalf("policy with pool = %v, want managed", got)
	}
	if !app.claudeAccountsMarkerExists() {
		t.Fatal("marker must be written once pool exists")
	}

	// 外部删除池项 → 刷新后 POOL_MISSING blocked。
	poolStore.mu.Lock()
	poolStore.pool = nil
	poolStore.mu.Unlock()
	app.refreshClaudeAccountsCache(context.Background())
	if got := app.claudeCredentialsPolicy(); got != claudeCredentialsBlocked {
		t.Fatalf("policy after external deletion = %v, want blocked", got)
	}
	app.claudeAccountsMu.RLock()
	loadErr := app.claudeAccountsLoadErr
	app.claudeAccountsMu.RUnlock()
	if domain, ok := claudeaccounts.AsError(loadErr); !ok || domain.Reason != claudeaccounts.ReasonPoolMissing {
		t.Fatalf("loadErr = %v, want POOL_MISSING", loadErr)
	}
	if _, err := app.CaptureClaudeAccount("", "rebuild", 0); err == nil || !strings.Contains(err.Error(), "POOL_MISSING") {
		t.Fatalf("first capture after pool deletion must fail closed, got %v", err)
	}

	// 对照：从未创建过池（无标记）→ absent 正常未启用。
	app2, poolStore2, _, _ := newFacadeTestApp(t)
	poolStore2.mu.Lock()
	poolStore2.pool = nil
	poolStore2.mu.Unlock()
	app2.claudeAccountsPoolStore = poolStore2
	app2.claudeAccountsPoolMarkerPath = t.TempDir() + "/absent-marker"
	app2.refreshClaudeAccountsCache(context.Background())
	if got := app2.claudeCredentialsPolicy(); got != claudeCredentialsAbsent {
		t.Fatalf("never-created pool must stay absent, got %v", got)
	}
}

// ---- F12：进程观察器覆盖官方 RPC 入口 ----

// TestF12_ObserverMatchesPiRpcAndPackagePaths：pi-rpc 进程标题与 node 直跑官方
// 包入口都必须命中观察器，不能因标题不同漏掉（宁可误报不漏报）。
func TestF12_ObserverMatchesPiRpcAndPackagePaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want bool
	}{
		{"官方 RPC 标题", "pi-rpc", true},
		{"node 直跑官方包入口", "node /Users/u/.fnm/node_modules/@earendil-works/pi-coding-agent/dist/rpc-entry.js", true},
		{"普通 claude", "/usr/local/bin/claude", true},
		{"普通 pi", "/opt/homebrew/bin/pi --provider anthropic", true},
		{"无关 zsh", "/bin/zsh -il", false},
		{"名字包含 pi 的无关进程", "/usr/bin/ping example.com", false},
	} {
		if got := claudeProcessArgsMatch(tc.args); got != tc.want {
			t.Fatalf("%s: claudeProcessArgsMatch(%q) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}

	app, _, _, _ := newFacadeTestApp(t)
	app.claudeAccountsQuiescenceLister = func() ([]claudeProcessSample, error) {
		return []claudeProcessSample{{PID: 4405, PPID: 1, Args: "pi-rpc"}}, nil
	}
	q := claudeProcessQuiescence{app: app, lister: app.claudeAccountsProcessSamples, attribution: func() map[int]bool { return nil }}
	if err := q.Check(context.Background()); err == nil || claudeaccounts.ReasonOf(err) != claudeaccounts.ReasonExternalSessionUncertain {
		t.Fatalf("live pi-rpc must be EXTERNAL_SESSION_UNCERTAIN, got %v", err)
	}
}

// ---- F15：operation 缓存淘汰不得删除运行中操作 ----

// TestF15_CacheEvictionKeepsRunningOperations：缓存超限时只淘汰已完成 victim；
// 运行中最旧操作保留（同 requestId 查询/幂等仍命中）。
func TestF15_CacheEvictionKeepsRunningOperations(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)
	app.claudeAccountsOpsMu.Lock()
	app.claudeAccountsOps = map[string]*claudeAccountOperation{}
	app.claudeAccountsOpsOrder = nil
	app.claudeAccountsRequests = map[string]string{}

	running := &claudeAccountOperation{
		dto: ClaudeAccountOperationDTO{ID: "ca-running", Kind: "switch", State: "running"},
		requestID: "req-running", paramsDigest: "d", started: true,
		done: make(chan struct{}),
	}
	app.claudeAccountsOps["ca-running"] = running
	app.claudeAccountsOpsOrder = append(app.claudeAccountsOpsOrder, "ca-running")
	app.claudeAccountsRequests["req-running"] = "ca-running"
	for i := 0; i < claudeAccountOperationCacheLimit; i++ {
		id := "ca-done-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		op := &claudeAccountOperation{
			dto: ClaudeAccountOperationDTO{ID: id, Kind: "switch", State: "done"},
			paramsDigest: "d", done: closedChan(),
		}
		app.claudeAccountsOps[id] = op
		app.claudeAccountsOpsOrder = append(app.claudeAccountsOpsOrder, id)
	}
	app.claudeAccountsOpsMu.Unlock()

	if _, conflict := app.claudeOpBegin("switch", "req-new", "dn", 1); conflict {
		t.Fatal("unexpected conflict")
	}

	app.claudeAccountsOpsMu.Lock()
	stillThere := app.claudeAccountsOps["ca-running"] != nil
	reqStillMapped := app.claudeAccountsRequests["req-running"] == "ca-running"
	app.claudeAccountsOpsMu.Unlock()
	if !stillThere || !reqStillMapped {
		t.Fatalf("running operation must survive eviction (op=%v req=%v)", stillThere, reqStillMapped)
	}
	if _, err := app.GetClaudeAccountOperation("ca-running"); err != nil {
		t.Fatalf("running op query after eviction: %v", err)
	}
	same, conflict2 := app.claudeOpBegin("switch", "req-running", "d", 1)
	if conflict2 || same != running {
		t.Fatalf("idempotent requestId must hit the running op, conflict=%v", conflict2)
	}
}

// ---- 测试辅助 ----

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// registerTestCredentialLease 构造一个已提升的外部运行凭据 lease（真实链路：
// claim → MintExternalRunIdentity → coordinator 提升）。
func registerTestCredentialLease(t *testing.T, app *App, sessionID string) *remote.SharedDependencyLease {
	t.Helper()
	claim, err := app.beginClaudeCredentialsClaim("lease-helper")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	t.Cleanup(func() { claim.release(app) })
	externalRun, err := app.sharedCoord.MintExternalRunIdentity(contract.SessionID(sessionID))
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	lease, err := app.sharedCoord.AcquireForExternalRunWithAdmission(context.Background(), externalRun, remote.SharedServiceClaudeCredentials, claim.fingerprint, claim.admission)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	return lease
}
