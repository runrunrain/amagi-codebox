package remote

// control_hooks.go — H2: ControlLifecycleHook interface + production wiring seam
// (design §4A.3).
//
// The hook is injected by M3-A control_wiring (App layer) before Server.Start. It
// is synchronous, idempotent, and performs NO network/file I/O. The Server's
// production paths (revoke, security latch, Stop, maintenance, Start) call the
// hook at the exact authority order points mandated by design §4A.3.
//
// Authority order (design §4A.3):
//
//   revoke RevokeDevice:       ledger→Mark→registry FenceDevice→Terminate→Release→event
//   security latch latchSecurity: store latch→Fence→registry Stop→Terminate→Release
//   Server Stop stopInternal:   admission→**FenceAllRemote (FIRST lock-free action)**→
//                               pairing.Suspend→registry Stop→Terminate→Release→
//                               HTTP/listener/running=false→startedDone wait→stopped event
//   maintenance Begin:          stopped precheck→idempotent Fence+Release(maintenance)→store Begin→postcheck
//   Start publishLifecycleAcceptance: normal listen/run→**RestartRemote(new gen)**→registry/pairing acceptance
//
// The hook decouples the Server from the concrete ControlRuntime: the Server
// only depends on the interface, and the App injects the real implementation.

import (
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ControlLifecycleCause is the closed set of lifecycle causes (design §4A.3).
type ControlLifecycleCause uint8

const (
	ControlCauseServerStopped ControlLifecycleCause = iota + 1
	ControlCauseSecurityUnavailable
	ControlCauseMaintenance
)

// ControlLifecycleHook is the synchronous, idempotent hook called by the Server's
// production paths (design §4A.3). All methods return within the state budget
// (250ms) and perform NO network/file I/O. The caller must NOT hold any
// server/pairing/registry/store/hub/journal lock when invoking these methods.
type ControlLifecycleHook interface {
	// IsReady reports whether the control runtime is ready.
	IsReady() bool
	// MarkDeviceRevoked is the global no-new-admission fence for a device. Called
	// after the revoke ledger commit, BEFORE registry FenceDevice (design §4A.3).
	MarkDeviceRevoked(deviceID contract.DeviceID)
	// ReleaseRevokedDevice clears the revoked device's holders. Called AFTER
	// registry Terminate (design §4A.3).
	ReleaseRevokedDevice(notice DeviceRevocationNotice)
	// FenceAllRemote is phase 1 of Server Stop / security latch. For Server Stop
	// it is the FIRST lock-free action after admission (before pairing.Suspend).
	// It sets accepting=false, increments acceptanceGeneration, and cancels all
	// device launch/connection-bound ops + device-owned lifecycle intents.
	FenceAllRemote(cause ControlLifecycleCause, at time.Time)
	// ReleaseAllRemote is phase 2: clears all device holders and emits the
	// appropriate transition (design §4A.3).
	ReleaseAllRemote(cause ControlLifecycleCause, at time.Time)
	// RestartRemote marks the arbiter accepting again with a new acceptance
	// generation. Called after a successful Start (design §4A.3).
	RestartRemote(at time.Time)
}

// noopLifecycleHook is a no-op hook used when the control runtime is not wired
// (e.g., legacy Server construction without M3-A). All methods are safe no-ops.
type noopLifecycleHook struct{}

func (noopLifecycleHook) IsReady() bool                                     { return false }
func (noopLifecycleHook) MarkDeviceRevoked(contract.DeviceID)               {}
func (noopLifecycleHook) ReleaseRevokedDevice(DeviceRevocationNotice)       {}
func (noopLifecycleHook) FenceAllRemote(ControlLifecycleCause, time.Time)   {}
func (noopLifecycleHook) ReleaseAllRemote(ControlLifecycleCause, time.Time) {}
func (noopLifecycleHook) RestartRemote(time.Time)                           {}

// lifecycleHookAdapter adapts ControlRuntime to ControlLifecycleHook. It is
// created by the App layer when wiring the control runtime to the Server.
type lifecycleHookAdapter struct {
	runtime *ControlRuntime
}

// NewControlLifecycleHook adapts a ControlRuntime to the hook interface.
func NewControlLifecycleHook(runtime *ControlRuntime) ControlLifecycleHook {
	if runtime == nil {
		return noopLifecycleHook{}
	}
	return &lifecycleHookAdapter{runtime: runtime}
}

func (a *lifecycleHookAdapter) IsReady() bool { return a.runtime.IsReady() }

func (a *lifecycleHookAdapter) MarkDeviceRevoked(deviceID contract.DeviceID) {
	a.runtime.arbiter.MarkDeviceRevoked(deviceID)
}

func (a *lifecycleHookAdapter) ReleaseRevokedDevice(notice DeviceRevocationNotice) {
	a.runtime.arbiter.ReleaseRevokedDevice(notice)
}

func (a *lifecycleHookAdapter) FenceAllRemote(_ ControlLifecycleCause, _ time.Time) {
	a.runtime.FenceAllRemote()
}

func (a *lifecycleHookAdapter) ReleaseAllRemote(cause ControlLifecycleCause, _ time.Time) {
	reason := reasonServiceStopped
	if cause == ControlCauseSecurityUnavailable {
		reason = reasonSecurityUnavailable
	}
	a.runtime.arbiter.ReleaseAllRemote(reason)
}

func (a *lifecycleHookAdapter) RestartRemote(_ time.Time) {
	a.runtime.RestartRemote()
}
