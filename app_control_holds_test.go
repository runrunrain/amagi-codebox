package main

// app_control_holds_test.go — 桌面端「主动收回远程设备控制权」App 绑定行为
// 测试（R5-006）：GetSessionControlHolds / ReleaseSessionControl 经真实
// ControlRuntime（wireTestControl）驱动真实 gate 链路——设备 Acquire 持有 →
// 枚举形状 → 收回终态 none + 可重新接管 → 幂等 no-op → unknown session 明确
// 报错 → control 未就绪 fail-closed。

import (
	"context"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
)

// acquireDeviceHold attaches a device connection and acquires control of sid
// through the REAL gate path (mirrors the production WS attach+acquire flow).
func acquireDeviceHold(t *testing.T, rt *remote.ControlRuntime, sid, deviceID, deviceName, connID string) {
	t.Helper()
	lease, _ := rt.Directory().Attach(contract.DeviceID(deviceID), deviceName, remote.ConnectionID(connID), contract.SessionID(sid))
	if lease == nil {
		t.Fatal("Directory().Attach returned nil lease")
	}
	principal := remote.DevicePrincipal{
		DeviceID:            contract.DeviceID(deviceID),
		DeviceName:          deviceName,
		AuthenticatedAt:     time.Now(),
		CredentialExpiresAt: time.Now().Add(time.Hour),
	}
	if _, err := rt.Gate().Acquire(context.Background(), principal, lease, contract.SessionID(sid)); err != nil {
		t.Fatalf("gate.Acquire(%s): %v", sid, err)
	}
}

// startGateSession begins + activates a desktop run so the gate owns a public
// active control entry under sid (device holds need a public entry).
func startGateSession(t *testing.T, rt *remote.ControlRuntime, sid string) {
	t.Helper()
	_, runPermit, _, err := rt.BeginDesktopRun(context.Background(), contract.SessionID(sid))
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
}

func TestAppSessionControlHolds_ReleaseLifecycle(t *testing.T) {
	app := newTestApp(t)
	rt := wireTestControl(t, app)
	const sid = "sess-hold-1"

	startGateSession(t, rt, sid)

	// No holds before any device acquires.
	if holds := app.GetSessionControlHolds(); len(holds) != 0 {
		t.Fatalf("expected no holds initially, got %+v", holds)
	}

	// Device acquires → listed with identity + phase.
	acquireDeviceHold(t, rt, sid, "dev-1", "主上手机", "conn-1")
	holds := app.GetSessionControlHolds()
	if len(holds) != 1 {
		t.Fatalf("expected 1 hold, got %+v", holds)
	}
	h := holds[0]
	if h.SessionID != sid || h.DeviceID != "dev-1" || h.DeviceName != "主上手机" || h.InGrace {
		t.Fatalf("hold shape mismatch: %+v", h)
	}

	// Desktop release → success, list empties, final owner none (re-acquirable).
	if err := app.ReleaseSessionControl(sid); err != nil {
		t.Fatalf("ReleaseSessionControl: %v", err)
	}
	if holds := app.GetSessionControlHolds(); len(holds) != 0 {
		t.Fatalf("expected no holds after release, got %+v", holds)
	}

	// Idempotent: releasing an already-none session succeeds as a no-op.
	if err := app.ReleaseSessionControl(sid); err != nil {
		t.Fatalf("idempotent re-release on none holder: %v", err)
	}

	// The device can re-acquire after the reclaim (no permanent 409).
	acquireDeviceHold(t, rt, sid, "dev-1", "主上手机", "conn-2")
	if holds := app.GetSessionControlHolds(); len(holds) != 1 {
		t.Fatalf("expected re-acquired hold, got %+v", holds)
	}
	if err := app.ReleaseSessionControl(sid); err != nil {
		t.Fatalf("ReleaseSessionControl (re-acquired): %v", err)
	}
}

func TestAppReleaseSessionControl_UnknownSessionReadableError(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)

	err := app.ReleaseSessionControl("no-such-session")
	if err == nil {
		t.Fatal("expected error for unknown session, got nil")
	}
	if !strings.Contains(err.Error(), "会话不存在") {
		t.Fatalf("error should carry the explicit not-found message, got: %v", err)
	}
}

func TestAppSessionControlHolds_FailClosedWhenControlUnready(t *testing.T) {
	app := newTestApp(t) // no control runtime wired

	if holds := app.GetSessionControlHolds(); len(holds) != 0 {
		t.Fatalf("unready control must yield empty holds, got %+v", holds)
	}
	if err := app.ReleaseSessionControl("any"); err != remote.ErrControlNotReady {
		t.Fatalf("unready control must fail closed with ErrControlNotReady, got %v", err)
	}
}
