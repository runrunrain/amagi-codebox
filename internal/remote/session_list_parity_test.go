package remote

// session_list_parity_test.go — v1 会话列表与桌面端一致性回归（adapter 面）：
// 桌面侧关闭的会话（stopped/exited）不得残留在 GET /sessions 投影里，
// 列表顺序与桌面端一致（startedAt 降序）。authority 层的同语义单测见
// internal/session/remote_list_parity_test.go。

import (
	"context"
	"testing"
)

func TestAuthorityListSessionsHidesClosedDesktopSessions(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	list, aerr := fixture.adapter.ListSessions(context.Background(), "viewer")
	if aerr != nil || len(list.List) != 1 || list.List[0].ID != fixture.sid {
		t.Fatalf("open session missing from v1 list: err=%v list=%#v", aerr, list.List)
	}

	// 桌面侧进程退出：会话关闭后必须从远程列表消失（不残留「已停止」卡片）。
	fixture.manager.MarkExited(string(fixture.sid))
	list, aerr = fixture.adapter.ListSessions(context.Background(), "viewer")
	if aerr != nil {
		t.Fatalf("ListSessions after exit: %v", aerr)
	}
	if len(list.List) != 0 {
		t.Fatalf("closed session lingered in v1 list: %#v", list.List)
	}

	// 关闭会话在被移除（tombstone）前详情仍可单查（契约不变）。
	if _, aerr := fixture.adapter.SessionDetail(context.Background(), "req", fixture.sid, "viewer"); aerr != nil {
		t.Fatalf("detail after exit: %v", aerr)
	}
}
