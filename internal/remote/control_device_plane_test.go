package remote

// control_device_plane_test.go — 设备平面车道（DoDevicePlanePTY /
// DevicePlaneInput / DevicePlanePasteChunk，design §5.6，v1.1 评审阻断项 1 的
// 回归锚）：
//
//   - connected 持控放行，且写前后 SnapshotForDevice 不变（NO-TAKE：owner/
//     phase/controlEpoch/holderGeneration 全部不动，连发第二条仍放行）；
//   - grace 持控同样放行（镜像反代写面门 SnapshotForDevice 的持控投影宽松；
//     与 WS 车道 DoDevicePTY 的 connected-only 形成有意对照）；
//   - 非持控 / 他设备持有 / 桌面持有 / 空 deviceID / 未知会话 → 拒绝且 raw
//     effect 零执行；
//   - 吊销设备（DenyDeviceRevoked）/ 不再接受远程（DenyNotAccepting）→ 拒绝
//     （镜像 createDevicePTYPermit 的 M-002 先例）；
//   - 对照断言：桌面车道 DesktopInput 写后 holder 变 desktop（take-first 语义
//     保持——本测试组钉死两条车道语义不得互换，R7）；
//   - PasteChunk 1KiB 分块逐 permit；gate/ptyRaw 未就绪 fail-closed。

import (
	"context"
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// devicePlaneHolderFacts 读取 entry 当前 holder 事实（kind/deviceID/phase/
// controlEpoch/holderGeneration），供 no-take 断言。
func devicePlaneHolderFacts(arb *ControlArbiter, sid contract.SessionID) (kind ownerKind, devID contract.DeviceID, phase devicePhase, epoch, gen uint64) {
	entry := arb.entryFor(sid)
	if entry == nil {
		return
	}
	entry.stateMu.Lock()
	defer entry.stateMu.Unlock()
	return entry.owner.kind, entry.owner.deviceID, entry.owner.phase, entry.controlEpoch, uint64(entry.holderGeneration)
}

// devicePlaneProbeEffect 构造记录执行次数的 raw effect（每个 permit 一次
// Checkpoint，模拟真实 WriteRaw 前的最后一步）。
func devicePlaneProbeEffect(ran *bool) RawEffect {
	return func(ctx context.Context, permit *operationPermit) error {
		*ran = true
		return permit.Checkpoint(ctx, 1)
	}
}

// TestControlDevicePlanePTY_ConnectedHolderNoTake：connected 持控设备经设备
// 平面车道写 PTY——放行、raw effect 恰好执行一次、写前后 SnapshotForDevice
// 逐字段不变（无夺权），连发第二条仍放行（阻断项 1：第一条输入绝不能夺走
// 设备自身的控制权，否则第二条起 403 且 ws/v1 输入通道被静默吊销）。
func TestControlDevicePlanePTY_ConnectedHolderNoTake(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	attachAndAcquire(t, dir, arb, "devA", "Device A", "connA", sid)

	before, gErr := arb.SnapshotForDevice(sid, "devA")
	if gErr != nil {
		t.Fatalf("SnapshotForDevice(before): %v", gErr)
	}
	if before.State != contract.ControlStateYou {
		t.Fatalf("setup: expected you, got %s", before.State)
	}
	kind0, devID0, phase0, epoch0, gen0 := devicePlaneHolderFacts(arb, sid)

	ran := false
	if err := gate.DoDevicePlanePTY(context.Background(), "devA", sid, PTYInput, devicePlaneProbeEffect(&ran)); err != nil {
		t.Fatalf("DoDevicePlanePTY(connected holder): %v", err)
	}
	if !ran {
		t.Fatal("raw effect must run exactly once for the holder")
	}

	after, gErr := arb.SnapshotForDevice(sid, "devA")
	if gErr != nil {
		t.Fatalf("SnapshotForDevice(after): %v", gErr)
	}
	if after != before {
		t.Fatalf("no-take violated: snapshot changed %+v -> %+v", before, after)
	}
	if after.State != contract.ControlStateYou {
		t.Fatalf("holder projection after write: %s", after.State)
	}
	// 桌面观察者视角同样不变（未发生 takeover）。
	deskView, gErr := arb.SnapshotForDevice(sid, "")
	if gErr != nil {
		t.Fatalf("SnapshotForDevice(desktop viewer): %v", gErr)
	}
	if deskView.State != contract.ControlStateOther {
		t.Fatalf("desktop viewer must still see other (device holds), got %s", deskView.State)
	}
	// 底层 owner 事实逐字段不变（kind/deviceID/phase/epoch/generation）。
	kind1, devID1, phase1, epoch1, gen1 := devicePlaneHolderFacts(arb, sid)
	if kind1 != kind0 || devID1 != devID0 || phase1 != phase0 || epoch1 != epoch0 || gen1 != gen0 {
		t.Fatalf("no-take violated: owner facts changed (%v/%v/%v/%d/%d -> %v/%v/%v/%d/%d)",
			kind0, devID0, phase0, epoch0, gen0, kind1, devID1, phase1, epoch1, gen1)
	}

	// 连发第二条：仍放行（阻断项 1 的直接回归形态）。
	ran2 := false
	if err := gate.DoDevicePlanePTY(context.Background(), "devA", sid, PTYInput, devicePlaneProbeEffect(&ran2)); err != nil {
		t.Fatalf("DoDevicePlanePTY(second consecutive write): %v", err)
	}
	if !ran2 {
		t.Fatal("second consecutive write must also run")
	}
	snap, gErr := arb.SnapshotForDevice(sid, "devA")
	if gErr != nil || snap.State != contract.ControlStateYou {
		t.Fatalf("after second write: snap=%+v err=%v", snap, gErr)
	}
}

// TestControlDevicePlanePTY_GraceHolderAllowed：grace 持控设备同样放行——与
// 反代写面门（webUIProxyControlHeld → SnapshotForDevice，grace 持有者投影为
// 控制者）保持同一口径；若一侧收紧即出现「代理放行、后端拒绝」的口径分裂。
// 对照：WS 车道 DoDevicePTY 在 grace 期禁止（无精确 live 租约，防僵尸连接）。
func TestControlDevicePlanePTY_GraceHolderAllowed(t *testing.T) {
	arb, gate, _, dir, clk := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	leaseA := attachAndAcquire(t, dir, arb, "devA", "Device A", "connA", sid)

	// 意外断连 → grace（fake clock：不推进，计时器不触发）。
	arb.OnUnexpectedDetachForSession(sid, leaseA, clk.Now())
	if _, devID, phase, _, _ := devicePlaneHolderFacts(arb, sid); devID != "devA" || phase != deviceGrace {
		t.Fatalf("setup: expected devA in grace, got devID=%q phase=%v", devID, phase)
	}
	snapBefore, gErr := arb.SnapshotForDevice(sid, "devA")
	if gErr != nil || snapBefore.State != contract.ControlStateYou {
		t.Fatalf("grace holder must project as controller: snap=%+v err=%v", snapBefore, gErr)
	}

	ran := false
	if err := gate.DoDevicePlanePTY(context.Background(), "devA", sid, PTYInput, devicePlaneProbeEffect(&ran)); err != nil {
		t.Fatalf("DoDevicePlanePTY(grace holder): %v", err)
	}
	if !ran {
		t.Fatal("raw effect must run for the grace holder")
	}
	snapAfter, gErr := arb.SnapshotForDevice(sid, "devA")
	if gErr != nil {
		t.Fatalf("SnapshotForDevice(after): %v", gErr)
	}
	if snapAfter != snapBefore {
		t.Fatalf("no-take violated in grace: %+v -> %+v", snapBefore, snapAfter)
	}
	if _, _, phase, _, _ := devicePlaneHolderFacts(arb, sid); phase != deviceGrace {
		t.Fatalf("write must not resolve the grace phase, got %v", phase)
	}
}

// TestControlDevicePlanePTY_NonHolderDenials：非持控一律 DenyNotController
//（none 持有 / 他设备持有），未知会话 DenySessionNotFound；所有拒绝路径
// raw effect 零执行。（桌面持有与空 deviceID 见下方独立用例。）
func TestControlDevicePlanePTY_NonHolderDenials(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)

	// none 持有：无人持控时任何设备写都被拒。
	sidNone := startSessionDirect(t, arb)
	ran := false
	err := gate.DoDevicePlanePTY(context.Background(), "devA", sidNone, PTYInput, devicePlaneProbeEffect(&ran))
	var gateErr *ControlGateError
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyNotController {
		t.Fatalf("none holder: want DenyNotController, got %v", err)
	}
	if ran {
		t.Fatal("raw effect must not run on denial")
	}

	// 他设备持有：devA 持控，devB 写被拒。
	sidOther := contract.SessionID("sess-other")
	insertDirectSession(arb, sidOther)
	attachAndAcquire(t, dir, arb, "devA", "Device A", "connA", sidOther)
	ran = false
	err = gate.DoDevicePlanePTY(context.Background(), "devB", sidOther, PTYInput, devicePlaneProbeEffect(&ran))
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyNotController {
		t.Fatalf("other device holder: want DenyNotController, got %v", err)
	}
	if ran {
		t.Fatal("raw effect must not run on denial")
	}
}

// TestControlDevicePlanePTY_DesktopHolderDenied：桌面持有时设备平面写被拒。
func TestControlDevicePlanePTY_DesktopHolderDenied(t *testing.T) {
	arb, gate, _, _, _ := newTestArbiter(t)
	sid := contract.SessionID("sess-desk")
	insertDirectSession(arb, sid)
	if gErr := arb.TakeDesktop(newWailsAuthority(1), sid); gErr != nil {
		t.Fatalf("TakeDesktop: %v", gErr)
	}
	ran := false
	err := gate.DoDevicePlanePTY(context.Background(), "devA", sid, PTYInput, devicePlaneProbeEffect(&ran))
	var gateErr *ControlGateError
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyNotController {
		t.Fatalf("desktop holder: want DenyNotController, got %v", err)
	}
	if ran {
		t.Fatal("raw effect must not run on denial")
	}
}

// TestControlDevicePlanePTY_UnknownSessionAndEmptyDevice：未知会话
// DenySessionNotFound；空 deviceID（快照 API 的「桌面观察者」拼写）fail-closed。
func TestControlDevicePlanePTY_UnknownSessionAndEmptyDevice(t *testing.T) {
	arb, gate, _, dir, _ := newTestArbiter(t)
	ran := false
	err := gate.DoDevicePlanePTY(context.Background(), "devA", "no-such-session", PTYInput, devicePlaneProbeEffect(&ran))
	var gateErr *ControlGateError
	if !errors.As(err, &gateErr) || gateErr.Kind != DenySessionNotFound {
		t.Fatalf("unknown session: want DenySessionNotFound, got %v", err)
	}
	if ran {
		t.Fatal("raw effect must not run on denial")
	}

	sid := contract.SessionID("sess-empty-dev")
	insertDirectSession(arb, sid)
	attachAndAcquire(t, dir, arb, "devA", "Device A", "connA", sid)
	ran = false
	err = gate.DoDevicePlanePTY(context.Background(), "", sid, PTYInput, devicePlaneProbeEffect(&ran))
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyNotController {
		t.Fatalf("empty deviceID: want DenyNotController, got %v", err)
	}
	if ran {
		t.Fatal("raw effect must not run on denial")
	}
}

// TestControlDevicePlanePTY_RevokedAndNotAccepting：吊销设备 / 服务不再接受
// 远程（Server Stop 一段）→ 稳定拒绝（镜像 createDevicePTYPermit 的 M-002
// revoke-window 纪律），raw effect 零执行。
func TestControlDevicePlanePTY_RevokedAndNotAccepting(t *testing.T) {
	// 吊销窗口：MarkDeviceRevoked 尚未清 holder，写必须已被拒。
	arb, gate, _, dir, _ := newTestArbiter(t)
	sid := startSessionDirect(t, arb)
	attachAndAcquire(t, dir, arb, "devA", "Device A", "connA", sid)
	arb.MarkDeviceRevoked("devA")
	ran := false
	err := gate.DoDevicePlanePTY(context.Background(), "devA", sid, PTYInput, devicePlaneProbeEffect(&ran))
	var gateErr *ControlGateError
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyDeviceRevoked {
		t.Fatalf("revoked device: want DenyDeviceRevoked, got %v", err)
	}
	if ran {
		t.Fatal("raw effect must not run on denial")
	}

	// 不再接受远程（FenceAllRemote 一段；holder 不受影响，靠许可校验拒绝）。
	arb2, gate2, _, dir2, _ := newTestArbiter(t)
	sid2 := startSessionDirect(t, arb2)
	attachAndAcquire(t, dir2, arb2, "devB", "Device B", "connB", sid2)
	arb2.FenceAllRemote()
	ran = false
	err = gate2.DoDevicePlanePTY(context.Background(), "devB", sid2, PTYInput, devicePlaneProbeEffect(&ran))
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyNotAccepting {
		t.Fatalf("not accepting: want DenyNotAccepting, got %v", err)
	}
	if ran {
		t.Fatal("raw effect must not run on denial")
	}
}

// TestControlRuntime_DevicePlaneNoTakeVsDesktopTakeFirst：阻断项 1 的车道对照
// 锚——同一会话上：
//
//	① 设备平面车道连发两条均放行且 holder 保持该设备（无夺权）；
//	② 桌面车道 DesktopInput 一写 holder 即变 desktop（take-first，本地用户
//	   意图，与 xterm 打字同语义——不得被本任务的改动波及）；
//	③ 夺权后设备平面车道 fail-closed（DenyNotController）。
func TestControlRuntime_DevicePlaneNoTakeVsDesktopTakeFirst(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clk, nil)
	raw := &countingPTYRaw{}
	rt.SetPTYRawPort(raw)
	rt.MarkReady()

	sid := contract.SessionID("sess-plane-contrast")
	launchPermit, runPermit, _, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}

	principal := newTestDevicePrincipal("devA", "Device A")
	lease, _ := rt.Directory().Attach(principal.DeviceID, principal.DeviceName, "connA", sid)
	if lease == nil {
		t.Fatal("attach lease nil")
	}
	if _, gErr := rt.Arbiter().Acquire(principal, lease, sid); gErr != nil {
		t.Fatalf("Acquire: %v", gErr)
	}

	// ① 设备平面车道：连发两条均放行，holder 保持该设备。
	for i := 0; i < 2; i++ {
		if err := rt.DevicePlaneInput(context.Background(), sid, "devA", []byte("k")); err != nil {
			t.Fatalf("DevicePlaneInput #%d: %v", i+1, err)
		}
	}
	if raw.writeCount != 2 {
		t.Fatalf("raw writes=%d want 2", raw.writeCount)
	}
	snap, gErr := rt.Arbiter().SnapshotForDevice(sid, "devA")
	if gErr != nil || snap.State != contract.ControlStateYou {
		t.Fatalf("after device-plane writes: snap=%+v err=%v (no-take violated)", snap, gErr)
	}

	// ② 对照锚：桌面车道 take-first。
	if err := rt.DesktopInput(context.Background(), sid, []byte("x")); err != nil {
		t.Fatalf("DesktopInput: %v", err)
	}
	if raw.writeCount != 3 {
		t.Fatalf("raw writes=%d want 3", raw.writeCount)
	}
	snap, gErr = rt.Arbiter().SnapshotForDevice(sid, "devA")
	if gErr != nil {
		t.Fatalf("SnapshotForDevice: %v", gErr)
	}
	if snap.State != contract.ControlStateDesktop {
		t.Fatalf("take-first regression anchor: holder after DesktopInput=%s want desktop", snap.State)
	}

	// ③ 控制权已归桌面：设备平面写 fail-closed。
	var gateErr *ControlGateError
	err = rt.DevicePlaneInput(context.Background(), sid, "devA", []byte("k2"))
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyNotController {
		t.Fatalf("device-plane write after desktop takeover: want DenyNotController, got %v", err)
	}
	if raw.writeCount != 3 {
		t.Fatalf("raw writes after denial=%d want 3", raw.writeCount)
	}
}

// TestControlRuntime_DevicePlanePasteChunk：1KiB 分块逐 permit（2500B → 3 次原始
// 写），holder 不变；失去持控后整段粘贴在首个分块即拒绝、零写入。
func TestControlRuntime_DevicePlanePasteChunk(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clk, nil)
	raw := &countingPTYRaw{}
	rt.SetPTYRawPort(raw)
	rt.MarkReady()

	sid := contract.SessionID("sess-plane-paste")
	launchPermit, runPermit, _, err := rt.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	principal := newTestDevicePrincipal("devA", "Device A")
	lease, _ := rt.Directory().Attach(principal.DeviceID, principal.DeviceName, "connA", sid)
	if _, gErr := rt.Arbiter().Acquire(principal, lease, sid); gErr != nil {
		t.Fatalf("Acquire: %v", gErr)
	}

	data := make([]byte, 2500) // 1024 + 1024 + 452
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	if err := rt.DevicePlanePasteChunk(context.Background(), sid, "devA", data); err != nil {
		t.Fatalf("DevicePlanePasteChunk: %v", err)
	}
	if raw.writeCount != 3 {
		t.Fatalf("raw writes=%d want 3 (1KiB chunks)", raw.writeCount)
	}
	snap, gErr := rt.Arbiter().SnapshotForDevice(sid, "devA")
	if gErr != nil || snap.State != contract.ControlStateYou {
		t.Fatalf("after paste: snap=%+v err=%v (no-take violated)", snap, gErr)
	}

	// 桌面夺权后整段粘贴：首个分块即 DenyNotController，零写入。
	if gErr := rt.Arbiter().TakeDesktop(newWailsAuthority(1), sid); gErr != nil {
		t.Fatalf("TakeDesktop: %v", gErr)
	}
	var gateErr *ControlGateError
	err = rt.DevicePlanePasteChunk(context.Background(), sid, "devA", data)
	if !errors.As(err, &gateErr) || gateErr.Kind != DenyNotController {
		t.Fatalf("paste after takeover: want DenyNotController, got %v", err)
	}
	if raw.writeCount != 3 {
		t.Fatalf("raw writes after takeover paste=%d want 3", raw.writeCount)
	}
}

// TestControlRuntime_DevicePlaneFailClosed：ptyRaw 未接线（gate 未就绪同语义）
// → ErrControlNotReady（对齐 DesktopInput 的 fail-closed 口径）。
func TestControlRuntime_DevicePlaneFailClosed(t *testing.T) {
	clk := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clk, nil)
	rt.MarkReady() // 未 SetPTYRawPort
	sid := contract.SessionID("sess-plane-fc")
	if err := rt.DevicePlaneInput(context.Background(), sid, "devA", []byte("x")); !errors.Is(err, ErrControlNotReady) {
		t.Fatalf("DevicePlaneInput without raw port: %v", err)
	}
	if err := rt.DevicePlanePasteChunk(context.Background(), sid, "devA", []byte("x")); !errors.Is(err, ErrControlNotReady) {
		t.Fatalf("DevicePlanePasteChunk without raw port: %v", err)
	}
}
