package main

// app_webui_remove_test.go — Minor6：WebUI tracker 生命周期清理接线验证。
// 会话删除（单删 RemoveSession / 批量 ClearStoppedSessionsDetailed）的 commit
// 点必须同步清理 webui 探测 tracker，避免残留探测泄漏。

import (
	"testing"

	"amagi-codebox/internal/session"
	"amagi-codebox/internal/webui"
)

// wireWebUI 给测试 App 接上独立 registryDir 的 webui 服务。
func wireWebUI(t *testing.T, app *App) *webui.Service {
	t.Helper()
	svc := webui.NewService(app.Log, t.TempDir())
	app.WebUI = svc
	return svc
}

func TestRemoveSession_CleansWebUITracker(t *testing.T) {
	app := newTestApp(t)
	svc := wireWebUI(t, app)

	sess := app.Sessions.Create(session.AppTypePi, "pi", "", "glm-5", session.ModeTerminal, t.TempDir())
	app.Sessions.MarkStopped(sess.ID)
	svc.RegisterSession(sess.ID, 12345, 0, "tok")
	if st := svc.GetWebUIStatus(sess.ID); st.State != webui.StateProbing {
		t.Fatalf("注册后 state=%s, want probing", st.State)
	}

	if err := app.RemoveSession(sess.ID); err != nil {
		t.Fatalf("RemoveSession: %v", err)
	}
	if st := svc.GetWebUIStatus(sess.ID); st.State != webui.StateUnknown {
		t.Fatalf("RemoveSession commit 后 tracker 应被清理，state=%s", st.State)
	}
}

func TestClearStoppedSessions_CleansWebUITracker(t *testing.T) {
	app := newTestApp(t)
	svc := wireWebUI(t, app)

	// 外部终端模式（非 embedded）会话：走 eligible → removeStoppedSessionRecord
	// 批量 commit 路径。
	sess := app.Sessions.Create(session.AppTypePi, "pi", "", "glm-5", session.ModeTerminal, t.TempDir())
	app.Sessions.MarkStopped(sess.ID)
	svc.RegisterSession(sess.ID, 23456, 0, "")

	res := app.ClearStoppedSessionsDetailed()
	if res.Cleared != 1 {
		t.Fatalf("cleared=%d, want 1（failed=%+v）", res.Cleared, res.Failed)
	}
	if st := svc.GetWebUIStatus(sess.ID); st.State != webui.StateUnknown {
		t.Fatalf("批量清理 commit 后 tracker 应被清理，state=%s", st.State)
	}
}
