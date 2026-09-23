package session

// remote_list_parity_test.go — 远程列表与桌面端一致性回归：
//   1. 关闭（stopped/exited/unavailable）的会话不得残留在远程列表
//      （ListRemoteSafeSnapshots 只投影「打开未关闭」的会话，与桌面侧栏
//      isActiveSessionStatus 过滤一致）；
//   2. 排序与桌面端 List() 一致（StartedAt 降序），且跨轮询稳定——
//      不随 lastActivityAt 的活动推进重排。

import (
	"testing"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
)

// activateEmbeddedAt activates a remote-eligible embedded reservation with an
// explicit StartedAt so ordering can be asserted deterministically.
func activateEmbeddedAt(t *testing.T, manager *Manager, id string, startedAt time.Time) {
	t.Helper()
	reservation := reserveEmbedded(t, manager, id)
	binding := processcap.BindingID{Kind: processcap.BackendPTY, Owner: 1, Generation: 1}
	token, err := manager.PrepareActivation(reservation, PreparedAuthorityActivation{
		Recipe:    launchplan.StableRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work", ProviderRef: "private-provider"},
		BindingID: binding, PID: 100, RunRevision: 1,
		StartedAt: startedAt, LastActivityAt: startedAt,
	})
	if err != nil {
		t.Fatalf("PrepareActivation(%s): %v", id, err)
	}
	if _, err := manager.CommitPreparedActivation(token, func() {}); err != nil {
		t.Fatalf("CommitPreparedActivation(%s): %v", id, err)
	}
}

func TestRemoteListMirrorsDesktopOpenSessions(t *testing.T) {
	manager := NewManager()
	base := time.Unix(1000, 0)
	activateEmbeddedAt(t, manager, "old-running", base.Add(1*time.Second))
	activateEmbeddedAt(t, manager, "mid-stopping", base.Add(2*time.Second))
	activateEmbeddedAt(t, manager, "new-running", base.Add(3*time.Second))
	activateEmbeddedAt(t, manager, "newest-stopped", base.Add(4*time.Second))
	activateEmbeddedAt(t, manager, "exited", base.Add(5*time.Second))
	activateEmbeddedAt(t, manager, "failed", base.Add(6*time.Second))

	manager.MarkStopping("mid-stopping")
	manager.MarkStopped("newest-stopped")
	manager.MarkExited("exited")
	manager.MarkFailed("failed", "boom")

	// 关闭/失败的会话必须从远程列表消失；stopping 仍算打开。
	remote := manager.ListRemoteSafeSnapshots()
	var gotIDs []string
	for _, snap := range remote {
		gotIDs = append(gotIDs, snap.Handle.SessionID())
	}
	wantIDs := []string{"new-running", "mid-stopping", "old-running"}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("remote list = %v, want %v", gotIDs, wantIDs)
	}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("remote list = %v, want %v (startedAt desc)", gotIDs, wantIDs)
		}
	}

	// 与桌面端投影保持同一相对顺序：桌面 List() 全量按 StartedAt 降序，
	// 远程列表应恰为其中「打开」前缀过滤后的同一序列。
	var desktopOpenIDs []string
	for _, info := range manager.List() {
		if isActiveSessionStatus(info.Status) {
			desktopOpenIDs = append(desktopOpenIDs, info.ID)
		}
	}
	if len(desktopOpenIDs) != len(gotIDs) {
		t.Fatalf("desktop open = %v, remote = %v", desktopOpenIDs, gotIDs)
	}
	for i := range gotIDs {
		if desktopOpenIDs[i] != gotIDs[i] {
			t.Fatalf("remote order diverges from desktop: desktop=%v remote=%v", desktopOpenIDs, gotIDs)
		}
	}
}

func TestRemoteListOrderStableAcrossActivity(t *testing.T) {
	manager := NewManager()
	base := time.Unix(2000, 0)
	activateEmbeddedAt(t, manager, "a-older", base)
	activateEmbeddedAt(t, manager, "b-newer", base.Add(1*time.Second))

	before := manager.ListRemoteSafeSnapshots()

	// 会话输出会推进 lastActivityAt（旧排序下 a-older 会反超到列表头）。
	// 直接推进活动时钟模拟活动：老会话活动晚于新会话。
	if !manager.TouchActivity("a-older", 1, base.Add(10*time.Second)) {
		t.Fatal("TouchActivity rejected")
	}

	after := manager.ListRemoteSafeSnapshots()
	if len(after) != 2 || after[0].Handle.SessionID() != "b-newer" || after[1].Handle.SessionID() != "a-older" {
		t.Fatalf("order changed with activity: %v", idsOf(after))
	}
	if before[0].Handle.SessionID() != after[0].Handle.SessionID() {
		t.Fatalf("order unstable: before=%v after=%v", idsOf(before), idsOf(after))
	}
}

func idsOf(snaps []AuthoritySnapshot) []string {
	ids := make([]string, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.Handle.SessionID())
	}
	return ids
}

// TestRemoteListOrderMatchesDesktopAcrossDSTOffset 锁定 M1 修复：桌面 List()
// 曾对 RFC3339 字符串二次排序，跨 DST 偏移（+02:00/+01:00）时字符串序与
// 时间序相反，与远程列表分叉。两端现在同一时间序 + ID tiebreak。
func TestRemoteListOrderMatchesDesktopAcrossDSTOffset(t *testing.T) {
	manager := NewManager()
	// A=00:30Z（本地 02:30+02:00），B=01:15Z（本地 02:15+01:00）：时间序 B 新，
	// RFC3339 字符串序却是 A 在前——修复前的桌面二次排序恰好把顺序排反。
	a := time.Date(2026, 10, 25, 2, 30, 0, 0, time.FixedZone("+02:00", 2*60*60))
	b := time.Date(2026, 10, 25, 2, 15, 0, 0, time.FixedZone("+01:00", 60*60))
	activateEmbeddedAt(t, manager, "sess-a", a)
	activateEmbeddedAt(t, manager, "sess-b", b)

	remote := idsOf(manager.ListRemoteSafeSnapshots())
	if len(remote) != 2 || remote[0] != "sess-b" || remote[1] != "sess-a" {
		t.Fatalf("remote list = %v, want [sess-b sess-a] (time order)", remote)
	}
	var desktop []string
	for _, info := range manager.List() {
		desktop = append(desktop, info.ID)
	}
	if len(desktop) != 2 || desktop[0] != remote[0] || desktop[1] != remote[1] {
		t.Fatalf("desktop order %v diverges from remote %v (DST string-sort regression)", desktop, remote)
	}
}

// TestRemoteListOrderSameSecondTiebreak 锁定同秒并列：两端均按 sessionId
// 升序打破（桌面曾是 sort.Slice 不稳定任意序）。
func TestRemoteListOrderSameSecondTiebreak(t *testing.T) {
	manager := NewManager()
	sec := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	// 同秒不同纳秒；ID 字典序与启动先后故意相反（z 先启、a 后启）。
	activateEmbeddedAt(t, manager, "sess-z", sec.Add(100*time.Millisecond))
	activateEmbeddedAt(t, manager, "sess-a", sec.Add(200*time.Millisecond))

	remote := idsOf(manager.ListRemoteSafeSnapshots())
	if len(remote) != 2 || remote[0] != "sess-a" || remote[1] != "sess-z" {
		t.Fatalf("remote tiebreak = %v, want [sess-a sess-z] (id asc)", remote)
	}
	var desktop []string
	for _, info := range manager.List() {
		desktop = append(desktop, info.ID)
	}
	if len(desktop) != 2 || desktop[0] != remote[0] || desktop[1] != remote[1] {
		t.Fatalf("desktop tiebreak %v diverges from remote %v", desktop, remote)
	}
}
