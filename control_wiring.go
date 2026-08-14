package main

// control_wiring.go — M3-A2 App-layer wiring for the control runtime: the
// PTYRawPort adapter and the desktop launch helper that routes embedded PTY
// creation through the ControlGate (design §6.3, §6.4).
//
// This file exists to keep app.go edits surgical; all control-runtime glue
// that does not touch existing method signatures lives here.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
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

func (a *App) reserveLaunchSession(appType session.AppType, provider, preset, model string, mode session.LaunchMode, workdir string) (*session.Session, *session.CreateReservation, error) {
	if mode != session.ModeEmbedded {
		created := a.Sessions.Create(appType, provider, preset, model, mode, workdir)
		if created == nil {
			return nil, nil, session.ErrAuthorityInvalidCreate
		}
		return created, nil, nil
	}
	reservation, err := a.Sessions.ReserveCreate(session.CreateSpec{
		AppType: appType, Origin: launchplan.OriginDesktop, Mode: launchplan.ModeEmbedded,
		Workdir: workdir, RemoteEligible: true, Provider: provider, Preset: preset,
		Model: model,
	})
	if err != nil {
		return nil, nil, err
	}
	created := reservation.Session()
	return &created, reservation, nil
}

func stableDesktopRecipe(appType session.AppType, workdir, provider, preset, model, shell string, useHeadroom bool) launchplan.StableRecipe {
	return launchplan.StableRecipe{
		CLIType: contract.CLIType(appType), Workdir: workdir, ProviderRef: provider,
		PresetRef: preset, ModelRef: model, ShellRef: shell, UseHeadroom: useHeadroom,
	}
}

// launchEmbeddedPTY starts one hidden Authority + Control run. Process, shared
// leases, Control and H1 are fully prepared before Authority becomes the sole
// public membership bit.
func (a *App) launchEmbeddedPTY(reservation *session.CreateReservation, recipe launchplan.StableRecipe, spec platform.ResolvedLaunchSpec, sharedKinds ...remote.SharedServiceKind) (int, error) {
	return a.launchEmbeddedPTYWithAdmission(reservation, recipe, spec, nil, sharedKinds...)
}

func (a *App) launchEmbeddedPTYWithAdmission(reservation *session.CreateReservation, recipe launchplan.StableRecipe, spec platform.ResolvedLaunchSpec, admission *remote.SharedLaunchAdmission, sharedKinds ...remote.SharedServiceKind) (int, error) {
	if reservation == nil || reservation.SessionID() == "" || a.control == nil || a.processRegistry == nil {
		return 0, remote.ErrControlNotReady
	}
	sessionID := reservation.SessionID()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	launchPermit, runPermit, obsPermit, err := a.control.BeginDesktopRun(ctx, contract.SessionID(sessionID))
	if err != nil {
		return 0, fmt.Errorf("begin desktop run: %w", err)
	}
	abortHidden := func(cause error) {
		a.releaseSharedLeases(sessionID)
		a.control.AbortDesktopRun(ctx, launchPermit, cause)
		a.control.RemoveDesktopSession(ctx, contract.SessionID(sessionID))
	}

	if len(sharedKinds) > 0 {
		if a.sharedCoord == nil {
			abortHidden(remote.ErrSharedServiceInUse)
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
				abortHidden(acqErr)
				return 0, fmt.Errorf("acquire shared lease: %w", acqErr)
			}
			a.rememberSharedLease(sessionID, lease)
		}
	}

	start, err := a.Pty.StartResolvedWithRunEvidence(sessionID, spec, obsPermit)
	if err != nil {
		abortHidden(err)
		return 0, err
	}
	if err := start.Validate(processcap.BackendPTY); err != nil {
		_ = start.Binding.CloseExact(ctx)
		abortHidden(err)
		return 0, err
	}
	registryKey, err := a.processRegistry.Register(start.Binding, obsPermit.RunEpoch())
	if err != nil {
		_ = start.Binding.CloseExact(ctx)
		abortHidden(err)
		return 0, err
	}
	compensateProcess := func(cause error) {
		closeEvidence := start.Binding.CloseExact(ctx)
		if closeEvidence.Confirmed() {
			_ = a.processRegistry.ReleaseExact(registryKey.BindingID, registryKey.RunGeneration, start.Binding)
		}
		abortHidden(cause)
	}

	values := session.PreparedAuthorityActivation{
		Session: reservation.Session(), Recipe: recipe, BindingID: registryKey.BindingID,
		PID: start.PID, RunRevision: obsPermit.RunEpoch(), StartedAt: reservation.Session().StartedAt,
		LastActivityAt: time.Now(),
	}
	authorityToken, err := a.Sessions.PrepareActivation(reservation, values)
	if err != nil {
		compensateProcess(err)
		return 0, err
	}
	preparedActivation, err := a.control.PrepareCompositeActivation(contract.SessionID(sessionID), runPermit, obsPermit)
	if err != nil {
		a.Sessions.AbortPreparedActivation(authorityToken)
		compensateProcess(err)
		return 0, err
	}
	if _, err := a.Sessions.CommitPreparedActivation(authorityToken, preparedActivation.CommitNoFail); err != nil {
		a.control.AbortCompositeActivation(preparedActivation)
		a.Sessions.AbortPreparedActivation(authorityToken)
		compensateProcess(err)
		return 0, err
	}
	a.control.FinishCompositeActivation(preparedActivation)
	if a.remoteDefaults != nil {
		if err := a.remoteDefaults.RecordDesktopActivation(recipe); err != nil && a.Log != nil {
			a.Log.Warn("session", "记录 remote launch default 失败", err.Error())
		}
	}

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
	return start.PID, nil
}

func (a *App) registerExternalProcessEvidence(result *launcher.LaunchResult) (processcap.RegistryKey, bool, error) {
	if result == nil || result.Evidence.Validate(processcap.BackendExternalLauncher) != nil {
		if a.externalLauncher != nil {
			return processcap.RegistryKey{}, false, nil
		}
		return processcap.RegistryKey{}, false, session.ErrAuthorityProcessUnavailable
	}
	if a.processRegistry == nil {
		return processcap.RegistryKey{}, false, session.ErrAuthorityProcessUnavailable
	}
	key, err := a.processRegistry.Register(result.Evidence.Binding, result.Evidence.Binding.BindingID().Generation)
	return key, err == nil, err
}

func closeUnregisteredExternalEvidence(ctx context.Context, result *launcher.LaunchResult) {
	if result == nil || result.Evidence.Binding == nil {
		return
	}
	_ = result.Evidence.Binding.CloseExact(ctx)
}

func (a *App) compensateExternalProcessEvidence(ctx context.Context, result *launcher.LaunchResult, key processcap.RegistryKey, registered bool) {
	if !registered || result == nil || result.Evidence.Binding == nil {
		return
	}
	closeEvidence := result.Evidence.Binding.CloseExact(ctx)
	if closeEvidence.Confirmed() {
		_ = a.processRegistry.ReleaseExact(key.BindingID, key.RunGeneration, result.Evidence.Binding)
	}
}

func (a *App) finalizeExternalAuthority(sessionID string, result *launcher.LaunchResult, key processcap.RegistryKey, registered bool, recipe launchplan.StableRecipe) error {
	if !registered {
		return nil
	}
	if _, err := a.Sessions.BindLegacyProcess(sessionID, result.Evidence, recipe); err != nil {
		return err
	}
	if a.remoteDefaults != nil {
		if err := a.remoteDefaults.RecordDesktopActivation(recipe); err != nil && a.Log != nil {
			a.Log.Warn("session", "记录 remote launch default 失败", err.Error())
		}
	}
	return nil
}

// --- M2-A remote lifecycle raw port (design §4.2, §4.4) ---
//
// No production LaunchRawPort is wired: remote create remains fail-closed until
// a complete five-CLI launchplan.Executor can replay desktop configuration.
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
// PtyResize input surface remains.
type ptyBridgeAdapter struct {
	app *App
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

// sharedFingerprintForHeadroom computes SHA-256 over the exact service,
// upstream, and listen-port tuple. The same tuple is used by planner, executor
// verification, mutation guards, and lease promotion.
func sharedFingerprintForHeadroom(service, upstreamURL string, port int) [32]byte {
	return sharedServiceFingerprint(service, upstreamURL, port)
}

func sharedServiceFingerprint(service, upstreamURL string, port int) [32]byte {
	payload := make([]byte, 0, 16+len(service)+len(upstreamURL)+8)
	var scalar [8]byte
	binary.BigEndian.PutUint64(scalar[:], uint64(len(service)))
	payload = append(payload, scalar[:]...)
	payload = append(payload, service...)
	binary.BigEndian.PutUint64(scalar[:], uint64(len(upstreamURL)))
	payload = append(payload, scalar[:]...)
	payload = append(payload, upstreamURL...)
	binary.BigEndian.PutUint64(scalar[:], uint64(int64(port)))
	payload = append(payload, scalar[:]...)
	return sha256.Sum256(payload)
}
