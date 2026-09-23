package remote

// session_list_parity_test.go — v1 会话列表与桌面端一致性回归（adapter 面）：
// 桌面侧关闭的会话（stopped/exited）不得残留在 GET /sessions 投影里，
// 列表顺序与桌面端一致（startedAt 降序）。authority 层的同语义单测见
// internal/session/remote_list_parity_test.go。

import (
	"context"
	"testing"

	"amagi-codebox/internal/session"
)

func TestAuthorityListSessionsHidesClosedDesktopSessions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(m *session.Manager, id string)
	}{{
		name:  "exit",
		close: func(m *session.Manager, id string) { m.MarkExited(id) },
	}, {
		name:  "stop",
		close: func(m *session.Manager, id string) { m.MarkStopped(id) },
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAuthoritativeFixture(t, nil, false)
			list, aerr := fixture.adapter.ListSessions(context.Background(), "viewer")
			if aerr != nil || len(list.List) != 1 || list.List[0].ID != fixture.sid {
				t.Fatalf("open session missing from v1 list: err=%v list=%#v", aerr, list.List)
			}

			// 停止中仍算打开：卡片保留（wire 态 unavailable），与桌面侧栏同口径。
			if tc.name == "stop" {
				fixture.manager.MarkStopping(string(fixture.sid))
				list, aerr = fixture.adapter.ListSessions(context.Background(), "viewer")
				if aerr != nil || len(list.List) != 1 {
					t.Fatalf("stopping session dropped early: err=%v list=%#v", aerr, list.List)
				}
			}

			// 桌面侧会话关闭：必须从远程列表消失（不残留「已停止」卡片）。
			tc.close(fixture.manager, string(fixture.sid))
			list, aerr = fixture.adapter.ListSessions(context.Background(), "viewer")
			if aerr != nil {
				t.Fatalf("ListSessions after %s: %v", tc.name, aerr)
			}
			if len(list.List) != 0 {
				t.Fatalf("closed session (%s) lingered in v1 list: %#v", tc.name, list.List)
			}

			// 关闭会话在被移除（tombstone）前详情仍可单查（契约不变）。
			if _, aerr := fixture.adapter.SessionDetail(context.Background(), "req", fixture.sid, "viewer"); aerr != nil {
				t.Fatalf("detail after %s: %v", tc.name, aerr)
			}
		})
	}
}
