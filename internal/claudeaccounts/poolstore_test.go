package claudeaccounts

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// PoolStore/NativeStore 组（design §10.1 矩阵第 1 行）。

// TestPoolStoreRoundTrip：fake store 保存并回读完整双 grant 与身份。
func TestPoolStoreRoundTrip(t *testing.T) {
	store := &fakePoolStore{}
	now := time.Now()
	pool := NewAccountPool()
	snap := &AccountSnapshot{
		ID:        "id-a",
		Alias:     "A",
		CreatedAt: now,
		UpdatedAt: now,
		Identity:  AccountIdentity{AccountUUID: syntheticUUIDA, OrganizationUUID: syntheticOrgA, Email: "a@example.com"},
		Claude: ClaudeGrantState{
			OauthProjection:     syntheticKeychainItem("cc-a-1"),
			RawCredentialItem:   syntheticKeychainItem("cc-a-1"),
			StoreLocator:        &KeychainLocator{Service: ClaudeKeychainServiceName, Account: "claude"},
			AdapterVersion:      ClaudeKeychainAdapterVersion,
			ObservedAt:          now,
			RevisionFingerprint: &FileRevision{SHA256Hex: "abc"},
		},
		ClaudeIdentity: ClaudeIdentityFields{OauthAccount: []byte(`{"accountUuid":"x"}`), UserID: "u1"},
		Pi: PiGrantState{
			Anthropic:      syntheticPiAuth("pi-a-1", futureExpiresMS(now)),
			AdapterVersion: PiRefreshAdapterVersion,
			ObservedAt:     now,
		},
		IdentityEvidence: &IdentityEvidence{CCAccountUUID: syntheticUUIDA, PiAccountUUID: syntheticUUIDA, CheckedAt: now},
		Health:           AccountHealth{ClaudeGrant: HealthReady, PiGrant: HealthReady, IdentityStatus: "verified"},
	}
	pool.Accounts[snap.ID] = snap
	pool.ActiveAccountID = snap.ID
	pool.ActiveGeneration = 7
	pool.Revision = 3
	if err := store.Save(context.Background(), pool); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if reloaded.Revision != 3 || reloaded.ActiveAccountID != "id-a" || reloaded.ActiveGeneration != 7 {
		t.Fatalf("回读元数据不符: %+v", reloaded)
	}
	got := reloaded.Account("id-a")
	if got == nil {
		t.Fatal("回读缺少账号")
	}
	if !equalJSON(got.Claude.OauthProjection, snap.Claude.OauthProjection) {
		t.Fatal("CC OAuth 投影回读不符")
	}
	if !equalJSON(got.Pi.Anthropic, snap.Pi.Anthropic) {
		t.Fatal("Pi anthropic 回读不符")
	}
	if got.IdentityEvidence == nil || got.IdentityEvidence.CCAccountUUID != syntheticUUIDA {
		t.Fatal("身份证据回读不符")
	}
	if got.Claude.StoreLocator == nil || got.Claude.StoreLocator.Account != "claude" {
		t.Fatal("storeLocator 回读不符")
	}
}

// TestDecodePoolEnvelopeCorrupt：坏 JSON / schema 版本 / 活动账号悬空。
func TestDecodePoolEnvelopeCorrupt(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"坏 JSON", "{not json"},
		{"空对象", "{}"},
		{"未知 schema", `{"schemaVersion":99,"accounts":{}}`},
		{"活动账号悬空", `{"schemaVersion":1,"revision":1,"activeAccountId":"ghost","accounts":{}}`},
		{"未知事务类型", `{"schemaVersion":1,"revision":1,"accounts":{},"transaction":{"kind":"weird"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodePoolEnvelope([]byte(tc.data))
			assertReason(t, err, ReasonPoolCorrupt)
		})
	}
}

// TestPoolStoreSaveFailure：注入保存失败（保存后读失败语义）。
func TestPoolStoreSaveFailure(t *testing.T) {
	store := &fakePoolStore{saveErrAt: 1}
	pool := NewAccountPool()
	err := store.Save(context.Background(), pool)
	if err == nil {
		t.Fatal("期望注入的保存失败")
	}
}

// TestKeychainLocatorAmbiguity：零项报缺失、多项报歧义（design §3.1）。
func TestKeychainLocatorAmbiguity(t *testing.T) {
	kc := &fakeKeychain{}
	_, err := kc.LocateItem(context.Background())
	assertReason(t, err, ReasonNeedsLogin)

	kc.items = []fakeKCItem{
		{account: "one", data: syntheticKeychainItem("t1")},
		{account: "two", data: syntheticKeychainItem("t2")},
	}
	_, err = kc.LocateItem(context.Background())
	assertReason(t, err, ReasonUnsupportedCredentialSchema)
}

// TestKeychainStoreDenied：读取被拒分类（design §3.3 locked/denied 分开）。
func TestKeychainStoreDenied(t *testing.T) {
	kc := &fakeKeychain{items: []fakeKCItem{{account: "claude", data: syntheticKeychainItem("t1")}}}
	kc.readErr = NewErr(ReasonNeedsReconcile, "注入：Keychain 访问被拒")
	_, err := kc.ReadItem(context.Background(), &KeychainLocator{Service: ClaudeKeychainServiceName, Account: "claude"})
	assertReason(t, err, ReasonNeedsReconcile)
}

// TestCrossProcessLockMutualExclusion：跨进程锁互斥（flock 语义）。
func TestCrossProcessLockMutualExclusion(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "sub", "claude-accounts.lock")
	first, err := acquireFileLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_, err = acquireFileLock(lockPath)
	assertReason(t, err, ReasonPoolLocked)
	first.release()
	second, err := acquireFileLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	second.release()
}

// TestCrossProcessLockConcurrentSingleWinner：并发竞争只有一个赢家——真实
// 双持锁互斥断言（F 测试有效性修复：此前只断言 total≥1，无法支撑「并发
// 只有一个赢家」）。持锁期间记录重叠：任一时刻活跃持锁者必须 ≤1。
func TestCrossProcessLockConcurrentSingleWinner(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "claude-accounts.lock")
	var mu sync.Mutex
	active := 0
	maxActive := 0
	winners := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := acquireFileLock(lockPath)
			if err != nil {
				return // 竞争失败者：正常路径
			}
			mu.Lock()
			winners++
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			lock.release()
		}()
	}
	wg.Wait()
	if winners == 0 {
		t.Fatal("至少应有一个持锁者成功")
	}
	if maxActive > 1 {
		t.Fatalf("flock 互斥被违反：同时存在 %d 个持锁者", maxActive)
	}
	if winners < 8 {
		t.Logf("竞争赢家 %d/8（未全部拿到锁但互斥成立）", winners)
	}
}

// TestProperLockBusyAndRelease：Pi 兼容锁的占用与释放。
func TestProperLockBusyAndRelease(t *testing.T) {
	target := filepath.Join(t.TempDir(), "auth.json")
	now := time.Now()
	lock, err := AcquireProperLock(target, now)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// 已持有时再次获取 → busy（不自动清理未知/过期原生锁）。
	_, err = AcquireProperLock(target, now.Add(time.Second))
	assertReason(t, err, ReasonNativeLockBusy)
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// 释放后可重新获取。
	again, err := AcquireProperLock(target, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatalf("re-release: %v", err)
	}
	// 锁被外部接管（mtime 内容变化）时不删除。
	third, err := AcquireProperLock(target, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target+".lock", "mtime"), []byte("999"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := third.Release(); err == nil {
		t.Fatal("被接管的锁不应被删除")
	}
}

// TestPoolLockFileHasNoSecrets：锁文件不含秘密（design §3.3）。
func TestPoolLockFileHasNoSecrets(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "claude-accounts.lock")
	lock, err := acquireFileLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("锁文件应为空，实际 %d 字节", len(data))
	}
}
