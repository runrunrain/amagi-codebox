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
