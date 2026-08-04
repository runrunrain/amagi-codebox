package main

// control_wiring.go — M3-A2 App-layer wiring for the control runtime: the
// PTYRawPort adapter and the desktop launch helper that routes embedded PTY
// creation through the ControlGate (design §6.3, §6.4).
//
// This file exists to keep app.go edits surgical; all control-runtime glue
// that does not touch existing method signatures lives here.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
)

// desktopBootstrapDelay is the wait for the spawned shell to initialize before
// the gated auto-command (bootstrap) write (M-005). Mirrors the legacy PTY
// service's 1s shell-init wait.
const desktopBootstrapDelay = 1000 * time.Millisecond

// appPTYRaw adapts pty.Service to the remote.PTYRawPort interface (raw bytes,
// not base64). It is NEVER Wails-bound (design §4.1 C-01).
type appPTYRaw struct {
	pty ptyWriter
}

// appPTYLifecycle adapts App PTY close/remove + session.Manager to the
// remote.PTYLifecycleRawPort interface (M-005). It is NEVER Wails-bound; it sits
// behind the desktop control gate.
type appPTYLifecycle struct {
	a *App
}

// CloseSession closes the PTY (M-005). The error is propagated (never discarded).
func (l appPTYLifecycle) CloseSession(sessionID contract.SessionID) error {
	return l.a.Pty.Close(string(sessionID))
}

// RemoveSession closes the PTY and removes the session record (M-005).
func (l appPTYLifecycle) RemoveSession(sessionID contract.SessionID) error {
	if err := l.a.Pty.Close(string(sessionID)); err != nil {
		return err
	}
	return l.a.Sessions.Remove(string(sessionID))
}

// ptyWriter is the narrow raw mutation surface of pty.Service needed by the
// gate closures. Declared here (not in pty package) so the adapter has no
// compile-time dependency beyond these methods. R3-004: DetachSession is the
// forceful backend detach invoked from the gate quarantine path.
type ptyWriter interface {
	WriteRaw(ctx context.Context, sessionID string, data []byte) error
	Resize(ctx context.Context, sessionID string, cols, rows int) error
	DetachSession(sessionID string) (*pty.DetachReceipt, error)
}

func (a appPTYRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	return a.pty.WriteRaw(ctx, sessionID, data)
}

func (a appPTYRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	return a.pty.Resize(ctx, sessionID, cols, rows)
}

// DetachSession forcibly detaches the PTY backend (R3-004). Called from the
// gate's quarantine path on a mid-syscall timeout; delegates to pty.Service.
func (a appPTYRaw) DetachSession(sessionID string) (remote.BackendDetachReceipt, error) {
	return a.pty.DetachSession(sessionID)
}

// launchEmbeddedPTY starts an embedded PTY run through the control gate. Shared
// run leases are acquired after BeginDesktopRun but BEFORE raw PTY startup, so a
// drain rejection has zero process side effects. Headroom callers additionally
// pass a pre-side-effect SharedLaunchAdmission via the internal helper below.
func (a *App) launchEmbeddedPTY(sessionID string, spec platform.ResolvedLaunchSpec, sharedKinds ...remote.SharedServiceKind) (int, error) {
	return a.launchEmbeddedPTYWithAdmission(sessionID, spec, nil, sharedKinds...)
}

// launchEmbeddedPTYWithAdmission executes the dependency/run transaction:
//
//  1. BeginDesktopRun (hidden run identity)
//  2. acquire/promote all shared dependency leases
//  3. StartResolvedWithRun (first raw PTY side effect)
//  4. ActivateDesktopRun + TrackRun
//
// Every failure releases acquired leases and aborts in reverse order.
func (a *App) launchEmbeddedPTYWithAdmission(sessionID string, spec platform.ResolvedLaunchSpec, admission *remote.SharedLaunchAdmission, sharedKinds ...remote.SharedServiceKind) (int, error) {
	if a.control == nil {
		if admission != nil || len(sharedKinds) > 0 {
			return 0, remote.ErrControlNotReady
		}
		// Fallback only for dependency-free pre-Startup diagnostics.
		return a.Pty.StartResolved(sessionID, spec)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	launchPermit, runPermit, obsPermit, err := a.control.BeginDesktopRun(ctx, contract.SessionID(sessionID))
	if err != nil {
		return 0, fmt.Errorf("begin desktop run: %w", err)
	}

	// R5-002: acquire every run lease before PTY startup. For Headroom, atomically
	// promote the exact admission that protected earlier Headroom.Start/config
	// work; a later uninstall drain therefore loses to this already-admitted run.
	if len(sharedKinds) > 0 {
		if a.sharedCoord == nil {
			a.control.AbortDesktopRun(ctx, launchPermit, remote.ErrSharedServiceInUse)
			return 0, remote.ErrSharedServiceInUse
		}
		for _, kind := range sharedKinds {
			var lease *remote.SharedDependencyLease
			var acqErr error
			if admission != nil && admission.Kind() == kind {
				lease, acqErr = a.sharedCoord.AcquireForRunWithAdmission(ctx, runPermit, kind, a.sharedFingerprint(kind), admission)
			} else {
				lease, acqErr = a.sharedCoord.AcquireForRun(ctx, runPermit, kind, a.sharedFingerprint(kind))
			}
			if acqErr != nil {
				a.releaseSharedLeases(sessionID)
				a.control.AbortDesktopRun(ctx, launchPermit, acqErr)
				return 0, fmt.Errorf("acquire shared lease: %w", acqErr)
			}
			a.rememberSharedLease(sessionID, lease)
		}
	}

	pid, err := a.Pty.StartResolvedWithRun(sessionID, spec, obsPermit)
	if err != nil {
		a.releaseSharedLeases(sessionID)
		a.control.AbortDesktopRun(ctx, launchPermit, err)
		return 0, err
	}
	if err := a.control.ActivateDesktopRun(ctx, runPermit); err != nil {
		_ = a.Pty.Close(sessionID)
		a.releaseSharedLeases(sessionID)
		a.control.AbortDesktopRun(ctx, launchPermit, err)
		a.control.RemoveDesktopSession(ctx, contract.SessionID(sessionID))
		return 0, fmt.Errorf("activate run: %w", err)
	}
	a.control.Projector().TrackRun(contract.SessionID(sessionID), obsPermit)
	// M-005: route the delayed bootstrap (auto-command) through the control gate's
	// DoBootstrapPTY instead of a raw cpty.Write goroutine. StartResolvedWithRun
	// skips its own delayed write for control-managed runs (runHandle != nil), so
	// the only path the auto-command reaches the PTY is this gated write. The run
	// permit is revalidated by DoBootstrapPTY; a revoke/stop during the delay
	// denies the write safely. Best-effort: a denied bootstrap only means the
	// auto-command is not sent (the session is already fenced/stopped).
	if autoCmd := a.Pty.StartupAutoCommand(spec); autoCmd != "" {
		rp := runPermit
		sid := contract.SessionID(sessionID)
		cmd := []byte(autoCmd + "\r\n")
		go func() {
			select {
			case <-time.After(desktopBootstrapDelay):
			case <-ctx.Done():
				return
			}
			_ = a.control.DesktopBootstrap(ctx, rp, sid, cmd)
		}()
	}
	return pid, nil
}

// sharedFingerprintForProxy computes a non-secret config fingerprint for the
// Claude proxy singleton (design §6.7.1: canonical port/topology/upstream
// digest, no credentials/rules body). A zero fingerprint is used when no
// launch is in progress (the lease is acquired only by launches that use the
// shared proxy).
//
// --- M2-A remote raw ports (design §4.2, §4.4) ---
//
// appLaunchRaw adapts the PTY service to remote.LaunchRawPort. It starts the
// CLI process inside the gated DoLaunchEffect callback (the gate has already
// acquired the launch permit + registered the staging session). Residual
// (disclosed): the obs permit (RunObservationPermit) is not plumbed through
// the LaunchRawPort.StartProcess signature, so remote-launched PTY output does
// not yet route through the run-scoped RunEventProjector. It uses StartResolved
// (non-run-scoped) rather than StartResolvedWithRun. Full obs-permit routing
// is a deeper wiring follow-up.
type appLaunchRaw struct {
	pty ptyStarter
}

type ptyStarter interface {
	StartResolved(sessionID string, spec platform.ResolvedLaunchSpec) (int, error)
	StartResolvedWithRun(sessionID string, spec platform.ResolvedLaunchSpec, runHandle any) (int, error)
}

// StartProcess starts the CLI process run-scoped (remote.LaunchRawPort). M-003:
// when an obsPermit is provided the PTY is started run-scoped so its output/exit
// flows through the H1 committer; otherwise it falls back to the non-run-scoped
// start.
func (a appLaunchRaw) StartProcess(ctx context.Context, sessionID contract.SessionID, recipe remote.RemoteLaunchRecipe, spec any, obsPermit *remote.RunObservationPermit) error {
	resolved, ok := spec.(platform.ResolvedLaunchSpec)
	if !ok {
		return fmt.Errorf("launch: invalid resolved spec type")
	}
	if obsPermit != nil {
		_, err := a.pty.StartResolvedWithRun(string(sessionID), resolved, obsPermit)
		return err
	}
	_, err := a.pty.StartResolved(string(sessionID), resolved)
	return err
}

// appSessionRaw adapts PTY close/resize + session.Manager to
// remote.SessionRawPort (stop/remove/resize behind the gate).
type appSessionRaw struct {
	pty      ptyCloserResizer
	sessions sessionLifecycle
}

type ptyCloserResizer interface {
	Close(sessionID string) error
	Resize(ctx context.Context, sessionID string, cols, rows int) error
}

type sessionLifecycle interface {
	Remove(id string) error
	MarkStopped(id string)
}

// StopSession closes the PTY and marks the session stopped (remote.SessionRawPort).
// M-004: the PTY Close error is propagated (never discarded); MarkStopped still
// runs as a best-effort state update because the session is logically stopped
// regardless of whether the kernel had already reaped the PTY.
func (a appSessionRaw) StopSession(ctx context.Context, sessionID contract.SessionID) error {
	closeErr := a.pty.Close(string(sessionID))
	a.sessions.MarkStopped(string(sessionID))
	return closeErr
}

// RemoveSession closes the PTY and removes the session record.
// M-004: the PTY Close error is propagated (never discarded).
func (a appSessionRaw) RemoveSession(ctx context.Context, sessionID contract.SessionID) error {
	closeErr := a.pty.Close(string(sessionID))
	if rErr := a.sessions.Remove(string(sessionID)); rErr != nil && closeErr == nil {
		return rErr
	}
	return closeErr
}

// ResizeSession resizes the PTY.
func (a appSessionRaw) ResizeSession(ctx context.Context, sessionID contract.SessionID, cols, rows int) error {
	return a.pty.Resize(ctx, string(sessionID), cols, rows)
}

// ptyBridgeAdapter is the unbound PTY bridge for the legacy/v1 remote WS path
// (design §8.6.3). It is NOT Wails-bound. M-003: the legacy naked-SessionID
// output/exit/resize callback delegation is REMOVED — all PTY output/exit is
// unified through the run-scoped RunEventProjector. Only the gated PtyWrite/
// PtyResize input surface remains. The pty field is retained for app.go
// construction compatibility (app.Remote.SetPtyBridge(ptyBridgeAdapter{...})).
type ptyBridgeAdapter struct {
	app *App
	pty ptyStarter // retained for app.go construction; legacy callbacks removed (M-003)
}

func (b ptyBridgeAdapter) PtyWrite(sessionID string, data string) error {
	return b.app.PtyWrite(sessionID, data)
}
func (b ptyBridgeAdapter) PtyResize(sessionID string, cols, rows int) error {
	return b.app.PtyResize(sessionID, cols, rows)
}

// isControlUnknownSession reports whether err is a control-gate
// "session not found" denial, meaning the gate does not manage this session
// (so a desktop lifecycle caller should fall back to the legacy direct path).
// Used by StopSession/RemoveSession to keep legacy Launcher sessions working.
func isControlUnknownSession(err error) bool {
	var ge *remote.ControlGateError
	if errors.As(err, &ge) {
		return ge.Kind == remote.DenySessionNotFound
	}
	return false
}

func sharedFingerprintForProxy(backendURL string, port int) [32]byte {
	// Minimal stable fingerprint over non-secret config. Not a security hash;
	// only used to detect incompatible-config launches while a lease exists.
	var fp [32]byte
	s := fmt.Sprintf("proxy|%s|%d", backendURL, port)
	for i := 0; i < len(s) && i < 32; i++ {
		fp[i] = s[i]
	}
	return fp
}

// sharedFingerprintForHeadroom computes a non-secret config fingerprint for a
// headroom singleton.
func sharedFingerprintForHeadroom(target string, port int) [32]byte {
	var fp [32]byte
	s := fmt.Sprintf("headroom|%s|%d", target, port)
	for i := 0; i < len(s) && i < 32; i++ {
		fp[i] = s[i]
	}
	return fp
}
