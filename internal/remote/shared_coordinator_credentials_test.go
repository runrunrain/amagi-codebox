package remote

// shared_coordinator_credentials_test.go — SharedServiceClaudeCredentials kind
// 的 admission/lease/closed 行为测试（design §6.3 第一行 + §10.1 lease 行）：
//   - launch admission 白名单扩展（凭据 + Headroom），旧枚举值不动；
//   - 凭据 admission 必须 config-bound（canonical 路径 + activeGeneration 指纹）；
//   - mutation admission 与 pending/run lease / launch admission 互斥
//     （admission→spawn 间插 swap、swap→read 间插 launch）；
//   - 不同代际指纹的并发 launch admission 拒绝（不带旧计划放行）；
//   - Headroom uninstall drain 不误伤凭据 kind；凭据 mutation 不阻塞 Headroom；
//   - ClearAll 关闭后凭据 kind 一律拒绝（closed 行为）。

import (
	"context"
	"errors"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// newCredentialsRunPermit 经真实 gate 构造 RunPermit（AcquireForRun 需要）。
func newCredentialsRunPermit(t *testing.T, gate ControlGate, sid contract.SessionID) *RunPermit {
	t.Helper()
	permit, _ := gate.BeginDesktopLaunch(context.Background(), MintDesktopAuthority())
	rp, _, _ := gate.RegisterStartingSession(context.Background(), permit, sid)
	if err := gate.ActivateRun(context.Background(), rp); err != nil {
		t.Fatalf("ActivateRun(%s): %v", sid, err)
	}
	return rp
}

func newCredentialsGate(t *testing.T) ControlGate {
	t.Helper()
	clock := newCtrlFakeClock(time.Now())
	hub := NewSessionEventHub()
	dir := NewAttachmentDirectory()
	arb := NewControlArbiter(clock, hub, dir)
	arb.MarkReady()
	return NewControlGate(arb, hub, dir)
}

func fingerprintForGeneration(gen byte) [32]byte {
	return [32]byte{1, gen}
}

// TestCredentialsKind_AdmissionRequiresConfigFingerprint：凭据 launch admission
// 必须携带指纹（AcquireLaunchAdmission 拒绝），ForConfig 变体可用。
func TestCredentialsKind_AdmissionRequiresConfigFingerprint(t *testing.T) {
	c := newTestCoordinator()
	if _, err := c.AcquireLaunchAdmission(SharedServiceClaudeCredentials); err == nil {
		t.Fatal("non-config-bound credential admission must be rejected")
	}
	adm, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fingerprintForGeneration(5))
	if err != nil {
		t.Fatalf("config-bound credential admission: %v", err)
	}
	if adm.Kind() != SharedServiceClaudeCredentials {
		t.Fatalf("admission kind = %d, want credentials", adm.Kind())
	}
}

// TestCredentialsKind_ConcurrentLaunchesSameGenerationCoexist：同代际指纹的
// 并发 launch admission 可共存（多会话同时持 lease，只阻塞换号）。
func TestCredentialsKind_ConcurrentLaunchesSameGenerationCoexist(t *testing.T) {
	c := newTestCoordinator()
	gate := newCredentialsGate(t)
	fp := fingerprintForGeneration(7)
	adm1, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp)
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	adm2, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp)
	if err != nil {
		t.Fatalf("second admission same fingerprint: %v", err)
	}
	rp1 := newCredentialsRunPermit(t, gate, "cred-a")
	rp2 := newCredentialsRunPermit(t, gate, "cred-b")
	if _, err := c.AcquireForRunWithAdmission(context.Background(), rp1, SharedServiceClaudeCredentials, fp, adm1); err != nil {
		t.Fatalf("promote first: %v", err)
	}
	if _, err := c.AcquireForRunWithAdmission(context.Background(), rp2, SharedServiceClaudeCredentials, fp, adm2); err != nil {
		t.Fatalf("promote second: %v", err)
	}
	if got := c.LeaseCount(SharedServiceClaudeCredentials); got != 2 {
		t.Fatalf("lease count = %d, want 2", got)
	}
}

// TestCredentialsKind_StaleGenerationAdmissionRejected：不同代际指纹的并发
// launch admission 被拒绝（admission→spawn 之间完成过 swap → 旧计划不放行）。
func TestCredentialsKind_StaleGenerationAdmissionRejected(t *testing.T) {
	c := newTestCoordinator()
	gate := newCredentialsGate(t)
	fp5 := fingerprintForGeneration(5)
	adm, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp5)
	if err != nil {
		t.Fatalf("admission: %v", err)
	}
	rp := newCredentialsRunPermit(t, gate, "cred-stale")
	lease, err := c.AcquireForRunWithAdmission(context.Background(), rp, SharedServiceClaudeCredentials, fp5, adm)
	if err != nil {
		t.Fatalf("promote at gen5: %v", err)
	}
	// 新一代际（swap 已完成）的 launch admission：与既有 lease 指纹冲突。
	if _, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fingerprintForGeneration(6)); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("stale-generation admission should be rejected with ErrSharedServiceInUse, got %v", err)
	}
	// 提升时指纹与 admission 不一致（代际变化）也拒绝。
	if _, err := c.AcquireForRunWithAdmission(context.Background(), newCredentialsRunPermit(t, gate, "cred-stale-2"), SharedServiceClaudeCredentials, fingerprintForGeneration(6), adm); err == nil {
		t.Fatal("promotion with mismatched fingerprint must fail")
	}
	if err := c.ReleaseExact(context.Background(), lease); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestCredentialsKind_MutationBlockedByLeasesAndAdmissions：mutation admission
// 被 pending/run lease 与 pending launch admission 阻塞（swap→read 间插 launch
// 与 admission→spawn 间插 swap 两个方向的互斥）。
func TestCredentialsKind_MutationBlockedByLeasesAndAdmissions(t *testing.T) {
	c := newTestCoordinator()
	gate := newCredentialsGate(t)
	fp := fingerprintForGeneration(1)

	// 方向一：pending launch admission 阻塞 mutation（admission→spawn 间插 swap）。
	adm, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp)
	if err != nil {
		t.Fatalf("admission: %v", err)
	}
	if _, err := c.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("mutation during pending admission should be busy, got %v", err)
	}

	// 提升为运行 lease 后仍阻塞；CheckMutation 同样拒绝。
	rp := newCredentialsRunPermit(t, gate, "cred-run")
	lease, err := c.AcquireForRunWithAdmission(context.Background(), rp, SharedServiceClaudeCredentials, fp, adm)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := c.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("mutation during run lease should be busy, got %v", err)
	}
	if err := c.CheckMutation(SharedServiceClaudeCredentials, MutationStop, fp); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("CheckMutation during run lease should be busy, got %v", err)
	}

	// 方向二：mutation admission 持有期间新 launch admission 被阻塞
	//（swap→read 间插 launch）。
	if err := c.ReleaseExact(context.Background(), lease); err != nil {
		t.Fatalf("release: %v", err)
	}
	mut, err := c.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop)
	if err != nil {
		t.Fatalf("mutation after release: %v", err)
	}
	if _, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp); !errors.Is(err, ErrSharedServiceInUse) {
		t.Fatalf("launch admission during mutation should be busy, got %v", err)
	}
	c.ReleaseMutationAdmission(mut)
	// 释放后 launch 恢复（且新代际可用：无 lease 时新指纹建立新代际 entry）。
	if _, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fingerprintForGeneration(2)); err != nil {
		t.Fatalf("launch admission after mutation release: %v", err)
	}
}

// TestCredentialsKind_HeadroomDrainDoesNotBlockCredentials：Headroom 卸载
// drain 不误伤凭据 kind（drain 限定 Headroom）；反之凭据 mutation 不影响
// Headroom 卸载 drain 的 empty 判定。
func TestCredentialsKind_HeadroomDrainDoesNotBlockCredentials(t *testing.T) {
	c := newTestCoordinator()
	gate := newCredentialsGate(t)

	// 凭据 lease 存在时进入 Headroom drain：drain 仍可进入（凭据不算
	// Headroom 依赖），且不阻塞凭据的后续 admission。
	fp := fingerprintForGeneration(3)
	adm, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp)
	if err != nil {
		t.Fatalf("credential admission: %v", err)
	}
	rp := newCredentialsRunPermit(t, gate, "cred-drain")
	if _, err := c.AcquireForRunWithAdmission(context.Background(), rp, SharedServiceClaudeCredentials, fp, adm); err != nil {
		t.Fatalf("promote credential lease: %v", err)
	}
	empty := c.BeginHeadroomUninstallDrain()
	if !empty {
		t.Fatal("headroom drain empty check must ignore credential leases")
	}
	if _, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fingerprintForGeneration(3)); err != nil {
		t.Fatalf("credential admission during headroom drain should pass, got %v", err)
	}
	c.EndHeadroomUninstallDrain()
}

// TestCredentialsKind_ExternalRunLeaseAndClosed：外部运行身份可提升凭据
// lease；ClearAll 关闭后所有凭据操作拒绝（closed 行为）。
func TestCredentialsKind_ExternalRunLeaseAndClosed(t *testing.T) {
	c := newTestCoordinator()
	fp := fingerprintForGeneration(9)
	adm, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp)
	if err != nil {
		t.Fatalf("admission: %v", err)
	}
	identity, err := c.MintExternalRunIdentity("cred-external")
	if err != nil {
		t.Fatalf("mint external identity: %v", err)
	}
	lease, err := c.AcquireForExternalRunWithAdmission(context.Background(), identity, SharedServiceClaudeCredentials, fp, adm)
	if err != nil {
		t.Fatalf("external credential lease: %v", err)
	}
	if !lease.Promoted() {
		t.Fatal("external credential lease must be promoted")
	}
	if err := c.ReleaseExact(context.Background(), lease); err != nil {
		t.Fatalf("release: %v", err)
	}

	c.ClearAll()
	if _, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fp); !errors.Is(err, ErrSharedCoordinatorClosed) {
		t.Fatalf("post-ClearAll admission: expected ErrSharedCoordinatorClosed, got %v", err)
	}
	if _, err := c.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop); !errors.Is(err, ErrSharedCoordinatorClosed) {
		t.Fatalf("post-ClearAll mutation: expected ErrSharedCoordinatorClosed, got %v", err)
	}
}

// TestCredentialsKind_EarlyReleaseIsIdempotent：admission 提升后释放幂等
// （早释放路径安全；二次 release 不 panic 不重复计数）。
func TestCredentialsKind_EarlyReleaseIsIdempotent(t *testing.T) {
	c := newTestCoordinator()
	adm, err := c.AcquireLaunchAdmissionForConfig(SharedServiceClaudeCredentials, fingerprintForGeneration(2))
	if err != nil {
		t.Fatalf("admission: %v", err)
	}
	c.ReleaseLaunchAdmission(adm)
	c.ReleaseLaunchAdmission(adm) // 幂等
	if got := c.LaunchAdmissionCount(SharedServiceClaudeCredentials); got != 0 {
		t.Fatalf("pending admissions after release = %d, want 0", got)
	}
	// 释放后 mutation 可进入（无悬挂 pending claim）。
	mut, err := c.AcquireMutationAdmission(SharedServiceClaudeCredentials, MutationStop)
	if err != nil {
		t.Fatalf("mutation after early release: %v", err)
	}
	c.ReleaseMutationAdmission(mut)
}
