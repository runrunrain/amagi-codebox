package remote

// control_force_release_test.go — 桌面端主动收回远程设备控制权（R5-006）
// 仲裁器行为测试：
//   - 设备持有时 ForceReleaseControl → 终态 ownerNone，takeover + released
//     事件序列对设备观察者可观测，且设备随后可重新接管；
//   - 宽限期中的 holder 同样可收回（宽限计时器不复活旧状态）；
//   - owner 已是 none → 幂等 no-op 成功（零事件）；
//   - unknown/removed session → DenySessionNotFound；
//   - ListSessionHolds 形状：仅 device holder、inGrace 标记、Since 为真实
//     转移时刻（fake clock 可精确断言）、canonical SessionID 排序；
//   - controlGate 透传（ListSessionHolds / ForceReleaseControl）同语义。

import (
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// insertDirectSession inserts a public, active, none-held control entry under
// the given id (same construction as startSessionDirect, parametrized id).
func insertDirectSession(arb *ControlArbiter, sid contract.SessionID) {
	entry := &controlEntry{
		sessionID:    sid,
		owner:        controlOwner{kind: ownerNone},
		controlEpoch: 1,
		opLane:       newBoundedOperationLane(),
		runPhase:     runActive,
		backend:      backendHealthy,
	}
	entry.currentRun = &runIdentity{nonce: 1, desktopRunToken: "tok"}
	entry.runEpoch = 1
	entry.stateMirror = contract.SessionStateRunning
	entry.stateMirrorSet = true
	arb.tableMu.Lock()
	arb.entries[sid] = entry
	arb.tableMu.Unlock()
}

// TestControlForceRelease_DeviceHolder asserts the core reclaim chain: device
// holder → ForceReleaseControl → final ownerNone with an observable
// takeover-then-released event sequence, and the device may re-acquire.
func TestControlForceRelease_DeviceHolder(t *testing.T) {
	arb, gate, hub, dir, clk := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	sub := hub.Subscribe(sid, pA.DeviceID, leaseA, nil)

	if _, gErr := arb.Acquire(pA, leaseA, sid); gErr != nil {
		t.Fatalf("Acquire: %v", gErr)
	}
	drainControlEvents(sub) // drop the acquired event; we assert the reclaim pair

	// ForceRelease through the GATE passthrough (binding entry path).
	if err := gate.ForceReleaseControl(sid); err != nil {
		t.Fatalf("gate.ForceReleaseControl: %v", err)
	}

	// Final holder state: none (never a lingering desktop holder).
	snap, gErr := arb.SnapshotForDevice(sid, pA.DeviceID)
	if gErr != nil {
		t.Fatalf("SnapshotForDevice: %v", gErr)
	}
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after force release: expected none, got %s", snap.State)
	}

	// Event sequence visible to the device viewer: desktop/takeover then
	// none/released (device-side visibility is part of the contract).
	evs := drainControlEvents(sub)
	if len(evs) != 2 {
		t.Fatalf("expected exactly takeover+released events, got %+v", evs)
	}
	if evs[0].State != contract.ControlStateDesktop || evs[0].Reason != "takeover" {
		t.Fatalf("event[0]: expected desktop/takeover, got %+v", evs[0])
	}
	if evs[1].State != contract.ControlStateNone || evs[1].Reason != "released" {
		t.Fatalf("event[1]: expected none/released, got %+v", evs[1])
	}

	// The device may re-acquire (it was released, not banned).
	snap, gErr = arb.Acquire(pA, leaseA, sid)
	if gErr != nil {
		t.Fatalf("re-acquire after force release: %v", gErr)
	}
	if snap.State != contract.ControlStateYou {
		t.Fatalf("re-acquire: expected you, got %s", snap.State)
	}
	_ = clk
}

// TestControlForceRelease_GraceHolder asserts a grace-phase holder is equally
// reclaimable and the cancelled grace timer never resurrects stale state.
func TestControlForceRelease_GraceHolder(t *testing.T) {
	arb, _, _, dir, clk := newTestArbiter(t)
	arb.SetGraceDuration(30 * time.Second)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	if _, gErr := arb.Acquire(pA, leaseA, sid); gErr != nil {
		t.Fatalf("Acquire: %v", gErr)
	}

	// Unexpected disconnect → grace. Listed with InGrace=true.
	arb.OnUnexpectedDetachForSession(sid, leaseA, clk.Now())
	holds := arb.ListSessionHolds()
	if len(holds) != 1 || !holds[0].InGrace {
		t.Fatalf("expected one in-grace hold, got %+v", holds)
	}

	if gErr := arb.ForceReleaseControl(sid); gErr != nil {
		t.Fatalf("ForceReleaseControl(grace holder): %v", gErr)
	}
	snap, _ := arb.SnapshotForDevice(sid, pA.DeviceID)
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after force release from grace: expected none, got %s", snap.State)
	}

	// Advancing past the grace deadline must not resurrect any state.
	clk.Advance(31 * time.Second)
	snap, gErr := arb.SnapshotForDevice(sid, pA.DeviceID)
	if gErr != nil {
		t.Fatalf("SnapshotForDevice after grace advance: %v", gErr)
	}
	if snap.State != contract.ControlStateNone {
		t.Fatalf("after grace deadline advance: expected none, got %s", snap.State)
	}
}

// TestControlForceRelease_IdempotentWhenNone asserts the none-holder no-op:
// success with zero events (no spurious takeover/released pair on idle sessions).
func TestControlForceRelease_IdempotentWhenNone(t *testing.T) {
	arb, _, hub, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", sid)
	sub := hub.Subscribe(sid, pA.DeviceID, leaseA, nil)

	if gErr := arb.ForceReleaseControl(sid); gErr != nil {
		t.Fatalf("none holder: expected no-op success, got %v", gErr)
	}
	if gErr := arb.ForceReleaseControl(sid); gErr != nil {
		t.Fatalf("none holder (second call): expected no-op success, got %v", gErr)
	}
	if evs := drainControlEvents(sub); len(evs) != 0 {
		t.Fatalf("no-op must emit zero events, got %+v", evs)
	}
}

// TestControlForceRelease_UnknownSession asserts the unknown-session denial.
func TestControlForceRelease_UnknownSession(t *testing.T) {
	arb, _, _, _, _ := newTestArbiter(t)
	gErr := arb.ForceReleaseControl("no-such-session")
	if gErr == nil || gErr.Kind != DenySessionNotFound {
		t.Fatalf("expected DenySessionNotFound, got %v", gErr)
	}
	// Gate passthrough must surface the same typed denial as an error.
	gate := NewControlGate(arb, nil, nil)
	if err := gate.ForceReleaseControl("no-such-session"); err == nil {
		t.Fatal("gate.ForceReleaseControl(unknown): expected error, got nil")
	}
}

// TestControlListSessionHolds_Shape asserts the listing shape: only device
// holders are listed (none/desktop excluded), fields carry device identity,
// InGrace reflects the phase, Since is the real committed transition time,
// and the result is canonically ordered by SessionID.
func TestControlListSessionHolds_Shape(t *testing.T) {
	arb, _, _, dir, clk := newTestArbiter(t)
	insertDirectSession(arb, "sess-a") // held by device (connected)
	insertDirectSession(arb, "sess-b") // held by desktop → NOT listed
	insertDirectSession(arb, "sess-c") // held by none → NOT listed

	pA := newTestDevicePrincipal("devA", "Device A")
	leaseA, _ := dir.Attach(pA.DeviceID, pA.DeviceName, "connA", "sess-a")
	if _, gErr := arb.Acquire(pA, leaseA, "sess-a"); gErr != nil {
		t.Fatalf("Acquire: %v", gErr)
	}
	if gErr := arb.TakeDesktop(newWailsAuthority(1), "sess-b"); gErr != nil {
		t.Fatalf("TakeDesktop: %v", gErr)
	}

	holds := arb.ListSessionHolds()
	if len(holds) != 1 {
		t.Fatalf("expected exactly the device-held session, got %+v", holds)
	}
	h := holds[0]
	if h.SessionID != "sess-a" || h.DeviceID != "devA" || h.DeviceName != "Device A" {
		t.Fatalf("hold identity mismatch: %+v", h)
	}
	if h.InGrace {
		t.Fatalf("connected holder must report InGrace=false, got %+v", h)
	}
	// Since is the committed acquire transition time (fake clock is frozen).
	if !h.Since.Equal(clk.Now()) {
		t.Fatalf("Since must equal the committed transition time %v, got %v", clk.Now(), h.Since)
	}

	// Gate passthrough returns the same listing.
	gate := NewControlGate(arb, nil, nil)
	gateHolds := gate.ListSessionHolds()
	if len(gateHolds) != len(holds) || gateHolds[0] != holds[0] {
		t.Fatalf("gate.ListSessionHolds() = %+v, arbiter = %+v", gateHolds, holds)
	}
}

// TestControlListSessionHolds_CanonicalOrder asserts multi-hold ordering is
// stable (canonical SessionID order) for the desktop UI.
func TestControlListSessionHolds_CanonicalOrder(t *testing.T) {
	arb, _, _, dir, _ := newTestArbiter(t)
	for _, sid := range []contract.SessionID{"sess-z", "sess-a", "sess-m"} {
		insertDirectSession(arb, sid)
		p := newTestDevicePrincipal(string(sid)+"-dev", "Device "+string(sid))
		lease, _ := dir.Attach(p.DeviceID, p.DeviceName, ConnectionID("conn-"+string(sid)), sid)
		if _, gErr := arb.Acquire(p, lease, sid); gErr != nil {
			t.Fatalf("Acquire(%s): %v", sid, gErr)
		}
	}
	holds := arb.ListSessionHolds()
	if len(holds) != 3 {
		t.Fatalf("expected 3 holds, got %+v", holds)
	}
	if holds[0].SessionID != "sess-a" || holds[1].SessionID != "sess-m" || holds[2].SessionID != "sess-z" {
		t.Fatalf("holds not in canonical SessionID order: %+v", holds)
	}
}
