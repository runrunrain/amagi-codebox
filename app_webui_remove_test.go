package main

// app_webui_remove_test.go — Minor6：WebUI tracker 生命周期清理接线验证。
// 会话删除（单删 RemoveSession / 批量 ClearStoppedSessionsDetailed）的 commit
// 点必须同步清理 webui 探测 tracker，避免残留探测泄漏。claude Web 平面
//（claude-webui design §5.3-4）在同一批 commit 点还需 Stop 进程内数据面
// server，否则 listener 泄漏（R9）。

import (
	"testing"

	"amagi-codebox/internal/claudewebui"
	"amagi-codebox/internal/platform"
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

// startRegisteredClaudePlane 给测试 App 接上真实 ClaudeWebUI Manager 并按
// startClaudeWebPlane 的生产路径起一个平面（真实回环 listener），返回会话与
// 端口，供删除/清理 commit 点的 Stop 断言（design §5.3-4 / R9 listener 泄漏）。
func startRegisteredClaudePlane(t *testing.T, app *App, sessID string) int {
	t.Helper()
	app.ClaudeWebUI = claudewebui.NewManager(app.Log)
	port, err := webui.AllocateFreePort()
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	spec := platform.ResolvedLaunchSpec{WorkDir: t.TempDir()}
	app.startClaudeWebPlane(sessID, "sid-remove", spec, 99999, port, "tok-remove")
	if !claudePlanePortOpen(port) {
		t.Fatalf("claude 平面应已在 %d 监听", port)
	}
	return port
}

// TestRemoveSession_CleansClaudeWebPlane：注册 claude 平面后 RemoveSession 的
// commit 点（removeWebUITracker）必须同时 Stop 进程内数据面 server（listener
// 释放）并清理 webui tracker。
func TestRemoveSession_CleansClaudeWebPlane(t *testing.T) {
	app := newTestApp(t)
	svc := wireWebUI(t, app)

	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "", "m", session.ModeTerminal, t.TempDir())
	app.Sessions.MarkStopped(sess.ID)
	port := startRegisteredClaudePlane(t, app, sess.ID)

	if err := app.RemoveSession(sess.ID); err != nil {
		t.Fatalf("RemoveSession: %v", err)
	}
	claudePlaneWaitFor(t, "RemoveSession 后 claude 平面 listener 释放", func() bool {
		return !claudePlanePortOpen(port)
	})
	if st := svc.GetWebUIStatus(sess.ID); st.State != webui.StateUnknown {
		t.Fatalf("RemoveSession 后 tracker 应被清理，state=%s", st.State)
	}
}

// TestClearStoppedSessions_CleansClaudeWebPlane：批量清理 commit 点
// （removeStoppedSessionRecord → removeWebUITracker）同样收掉 claude 平面。
func TestClearStoppedSessions_CleansClaudeWebPlane(t *testing.T) {
	app := newTestApp(t)
	svc := wireWebUI(t, app)

	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "", "m", session.ModeTerminal, t.TempDir())
	app.Sessions.MarkStopped(sess.ID)
	port := startRegisteredClaudePlane(t, app, sess.ID)

	res := app.ClearStoppedSessionsDetailed()
	if res.Cleared != 1 {
		t.Fatalf("cleared=%d, want 1（failed=%+v）", res.Cleared, res.Failed)
	}
	claudePlaneWaitFor(t, "批量清理后 claude 平面 listener 释放", func() bool {
		return !claudePlanePortOpen(port)
	})
	if st := svc.GetWebUIStatus(sess.ID); st.State != webui.StateUnknown {
		t.Fatalf("批量清理后 tracker 应被清理，state=%s", st.State)
	}
}
