package claudeaccounts

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// harvest 组（design §10.1 矩阵第 3 行）。

// captureAccountA 辅助：录入 A 并返回池 revision。
func captureAccountA(t *testing.T, env *testEnv) uint64 {
	t.Helper()
	_, err := env.svc.CaptureFirstAccount(context.Background(), "account-a", 0)
	if err != nil {
		t.Fatalf("capture A: %v", err)
	}
	pool, err := env.svc.LoadPool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pool.Revision
}

// TestCaptureFirstAccountSucceeds：capture 校验后把 A 加入池并设为活动账号。
func TestCaptureFirstAccountSucceeds(t *testing.T) {
	env := newTestEnv(t)
	rev := captureAccountA(t, env)
	if rev == 0 {
		t.Fatal("capture 后 revision 应递增")
	}
	pool, err := env.svc.LoadPool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pool.ActiveAccountID == "" || len(pool.Accounts) != 1 {
		t.Fatalf("池状态不符: %+v", pool)
	}
	snap := pool.Active()
	if snap.Identity.AccountUUID != syntheticUUIDA {
		t.Fatalf("身份 UUID 应为 A，实际 %s", snap.Identity.AccountUUID)
	}
	if snap.IdentityEvidence == nil || snap.IdentityEvidence.CCAccountUUID != snap.IdentityEvidence.PiAccountUUID {
		t.Fatal("双端身份证据缺失或不成对")
	}
	if len(snap.Claude.OauthProjection) == 0 || len(snap.Pi.Anthropic) == 0 || len(snap.ClaudeIdentity.OauthAccount) == 0 {
		t.Fatal("快照缺少受管投影")
	}
}

// TestCaptureRejectsIdentityMismatch：双端身份不一致 → 拒绝，池零写。
func TestCaptureRejectsIdentityMismatch(t *testing.T) {
	env := newTestEnv(t)
	// Pi token 实际属于 B（A/B 混对）。
	env.identityFake.responses["pi-a-1"] = ProfileIdentity{AccountUUID: syntheticUUIDB}
	_, err := env.svc.CaptureFirstAccount(context.Background(), "account-a", 0)
	assertReason(t, err, ReasonIdentityMismatch)
	pool, _ := env.svc.LoadPool(context.Background())
	if pool != nil {
		t.Fatal("身份不一致时不得写池")
	}
}

// TestCaptureRetryOnNativeChangeDuringIdentityCheck：身份校验期间原生变化
// 则有限重试整个采集（design §4.4）。这里模拟同账号 token 轮换。
func TestCaptureRetryOnNativeChangeDuringIdentityCheck(t *testing.T) {
	env := newTestEnv(t)
	base := &fakeIdentity{responses: map[string]ProfileIdentity{
		"cc-a-1": {AccountUUID: syntheticUUIDA},
		"pi-a-1": {AccountUUID: syntheticUUIDA},
	}}
	var firstFetch bool
	env.identity = &hookIdentity{inner: base, onFirst: func() {
		if firstFetch {
			return
		}
		firstFetch = true
		// 窗口内原生刷新：同账号轮换 pi token，并补身份映射。
		if werr := os.WriteFile(env.piAuthPath(), syntheticPiAuth("pi-a-2", futureExpiresMS(env.clock.Now())), 0o644); werr != nil {
			t.Error(werr)
		}
		base.responses["pi-a-2"] = ProfileIdentity{AccountUUID: syntheticUUIDA}
	}}
	env.rebuildService()
	if _, err := env.svc.CaptureFirstAccount(context.Background(), "account-a", 0); err != nil {
		t.Fatalf("capture（带重试）: %v", err)
	}
	pool, _ := env.svc.LoadPool(context.Background())
	if got := gjson.GetBytes(pool.Active().Pi.Anthropic, "access").String(); got != "pi-a-2" {
		t.Fatalf("重试后应采集到轮换 token，实际 %s", got)
	}
}

type hookIdentity struct {
	inner    *fakeIdentity
	onFirst  func()
	doneOnce bool
}
func (h *hookIdentity) FetchProfile(ctx context.Context, token string) (ProfileIdentity, error) {
	if !h.doneOnce {
		h.doneOnce = true
		h.onFirst()
	}
	return h.inner.FetchProfile(ctx, token)
}

// TestHarvestSavesIndependentlyRefreshedGrants：A 两端独立刷新后均入池
// （design §4.3：受管投影变化则保存新值，即使 mtime 相等或倒退）。
func TestHarvestSavesIndependentlyRefreshedGrants(t *testing.T) {
	env := newTestEnv(t)
	rev := captureAccountA(t, env)
	// 模拟原生 Claude Code 刷新了 CC grant、Pi 刷新了 anthropic grant。
	if err := os.WriteFile(env.claudeJSONPath(), syntheticClaudeJSON(syntheticUUIDA, "user-amagi-a-2", "a@example.com"), 0o644); err != nil {
		t.Fatal(err)
	}
	newKC := syntheticKeychainItem("cc-a-2")
	env.keychain.items = []fakeKCItem{{account: "claude", data: newKC}}
	env.identityFake.responses["cc-a-2"] = ProfileIdentity{AccountUUID: syntheticUUIDA, OrganizationUUID: syntheticOrgA}
	// mtime 倒退：把 pi 文件时间设置为过去，但内容轮换。
	if err := os.WriteFile(env.piAuthPath(), syntheticPiAuth("pi-a-3", futureExpiresMS(env.clock.Now())), 0o644); err != nil {
		t.Fatal(err)
	}
	past := env.clock.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(env.piAuthPath(), past, past); err != nil {
		t.Fatal(err)
	}
	env.identityFake.responses["pi-a-3"] = ProfileIdentity{AccountUUID: syntheticUUIDA}
	if _, err := env.svc.HarvestActive(context.Background(), rev); err != nil {
		t.Fatalf("harvest: %v", err)
	}
	pool, _ := env.svc.LoadPool(context.Background())
	snap := pool.Active()
	if got := gjson.GetBytes(snap.Claude.OauthProjection, "accessToken").String(); got != "cc-a-2" {
		t.Fatalf("CC 新 grant 未入池，实际 %s", got)
	}
	if got := gjson.GetBytes(snap.Pi.Anthropic, "access").String(); got != "pi-a-3" {
		t.Fatalf("Pi 新 grant 未入池（mtime 倒退场景），实际 %s", got)
	}
	if got := gjson.GetBytes(snap.ClaudeIdentity.OauthAccount, "accountUuid").String(); got != syntheticUUIDA {
		t.Fatal("身份投影不应变化")
	}
}

// TestHarvestNoOpWhenOnlyOtherProviderChanges：仅其他 provider 更新 →
// 不触发网络身份查询、不写池。
func TestHarvestNoOpWhenOnlyOtherProviderChanges(t *testing.T) {
	env := newTestEnv(t)
	rev := captureAccountA(t, env)
	beforeCalls := env.identityFake.callCount()
	// 只改 openai 条目（Codex 等其他工具写入），anthropic 保持字节不变。
	data := readJSON(t, env.piAuthPath())
	data, err := sjsonSetRawBytes(data, "openai", []byte(`{"type":"api_key","key":"sk-rotated"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.piAuthPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	beforeRev := env.pool.saveCount()
	if _, err := env.svc.HarvestActive(context.Background(), rev); err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if env.identityFake.callCount() != beforeCalls {
		t.Fatal("受管投影未变化时不应发起网络身份查询")
	}
	if env.pool.saveCount() != beforeRev {
		t.Fatal("受管投影未变化时不应写池")
	}
}

// TestHarvestRejectsAccountChange：原生槽位换成别的账号 → 拒绝。
func TestHarvestRejectsAccountChange(t *testing.T) {
	env := newTestEnv(t)
	rev := captureAccountA(t, env)
	env.seedAccountB()
	_, err := env.svc.HarvestActive(context.Background(), rev)
	assertReason(t, err, ReasonExternalAccountChange)
	pool, _ := env.svc.LoadPool(context.Background())
	if got := gjson.GetBytes(pool.Active().Pi.Anthropic, "access").String(); got != "pi-a-1" {
		t.Fatal("账号变更被拒绝时池快照不应被覆盖")
	}
}

// TestHarvestPoolSaveFailureZeroNativeWrite：池保存失败 → 原生零写。
func TestHarvestPoolSaveFailureZeroNativeWrite(t *testing.T) {
	env := newTestEnv(t)
	rev := captureAccountA(t, env)
	beforeKCUpdates := env.keychain.updateCount()
	beforeCJ := readJSON(t, env.claudeJSONPath())
	// 触发变化的原生刷新 + 注入下一次 Save 失败。
	if err := os.WriteFile(env.piAuthPath(), syntheticPiAuth("pi-a-4", futureExpiresMS(env.clock.Now())), 0o644); err != nil {
		t.Fatal(err)
	}
	env.identityFake.responses["pi-a-4"] = ProfileIdentity{AccountUUID: syntheticUUIDA}
	env.pool.mu.Lock()
	env.pool.saveErrAt = env.pool.saveCalls + 1
	env.pool.mu.Unlock()
	_, err := env.svc.HarvestActive(context.Background(), rev)
	if err == nil {
		t.Fatal("期望池保存失败错误")
	}
	if env.keychain.updateCount() != beforeKCUpdates {
		t.Fatal("harvest 不得写原生 Keychain")
	}
	if string(readJSON(t, env.claudeJSONPath())) != string(beforeCJ) {
		t.Fatal("harvest 不得写原生文件")
	}
}
