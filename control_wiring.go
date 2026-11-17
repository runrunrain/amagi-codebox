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

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

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

// launch:progress 事件契约：WSL 冷启动数秒等待中的阶段提示。负载
// {stage, message, appType}，stage 取下方稳定枚举，与前端
// useSessionLaunch 的 LAUNCH_STAGE_TEXT 键一一对应（两端同步修改）。
const (
	launchProgressEvent          = "launch:progress"
	launchProgressStagePreflight = "preflight"
	launchProgressStageSupply    = "supply"
	launchProgressStagePTY       = "pty"
)

// launchProgressEmitFn 是事件发射接缝（生产为带守卫的 wails EventsEmit；
// 测试注入记录器断言发射点，镜像 wslDefaultDistroFn 的替换模式）。
var launchProgressEmitFn = emitLaunchProgressEvent

// emitLaunchProgressEvent best-effort 发射：Wails EventsEmit 对未携带
// events 值的 ctx 会 log.Fatalf（vendor wails/v2/pkg/runtime getEvents），
// 故仅当 ctx 为 Startup 注入的运行时 ctx（携带 "events" 值）时才发射——
// 测试/非 Wails 环境（ctx 缺失或裸 context）自然降级为不发，绝不影响
// 启动主流程。
func emitLaunchProgressEvent(ctx context.Context, event string, data ...any) {
	if ctx == nil || ctx.Value("events") == nil {
		return
	}
	wailsRuntime.EventsEmit(ctx, event, data...)
}

// emitLaunchProgress 向前端发射一个 WSL 启动阶段进度事件（仅阶段提示，
// 发射路径任何失败都不阻断启动）。appType 透传 spec.AppType（五引擎枚举）。
func (a *App) emitLaunchProgress(stage, message, appType string) {
	launchProgressEmitFn(a.ctx, launchProgressEvent, map[string]any{
		"stage":   stage,
		"message": message,
		"appType": appType,
	})
}

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

func stableDesktopRecipe(appType session.AppType, workdir, provider, preset, model, shell string) launchplan.StableRecipe {
	return launchplan.StableRecipe{
		CLIType: contract.CLIType(appType), Workdir: workdir, ProviderRef: provider,
		PresetRef: preset, ModelRef: model, ShellRef: shell,
	}
}

// resolvedDesktopSession publishes the resolved cwd, not the host-side fallback
// used while the reservation was still hidden. The same session projection feeds
// desktop panels and remote snapshots, so a stale cwd is not merely cosmetic.
func resolvedDesktopSession(reserved session.Session, spec platform.ResolvedLaunchSpec) session.Session {
	reserved.WorkDir = spec.WorkDir
	return reserved
}

// launchEmbeddedPTY starts one hidden Authority + Control run. Process, shared
// leases, Control and H1 are fully prepared before Authority becomes the sole
// public membership bit.
func (a *App) launchEmbeddedPTY(reservation *session.CreateReservation, recipe launchplan.StableRecipe, spec platform.ResolvedLaunchSpec, sharedKinds ...remote.SharedServiceKind) (int, error) {
	return a.launchEmbeddedPTYWithAdmission(reservation, recipe, spec, nil, sharedKinds...)
}

func (a *App) launchEmbeddedPTYWithAdmission(reservation *session.CreateReservation, recipe launchplan.StableRecipe, spec platform.ResolvedLaunchSpec, admission *remote.SharedLaunchAdmission, sharedKinds ...remote.SharedServiceKind) (int, error) {
	var claims []*remote.SharedLaunchAdmission
	if admission != nil {
		claims = append(claims, admission)
	}
	return a.launchEmbeddedPTYWithClaims(reservation, recipe, spec, claims, sharedKinds...)
}

// launchEmbeddedPTYWithClaims 按 kind 匹配多笔启动 admission（design §6.3）：
// 匹配的 admission 原子提升为运行 lease，未匹配的 kind 走无 admission 获取
// （提升前无中间副作用时不适用此路径）。
// 凭据 kind 的指纹在提升时重算（canonical 路径 + 当前池代际），代际变化
// → 提升失败 → 启动以重新规划语义失败（不带旧代际放行）。
func (a *App) launchEmbeddedPTYWithClaims(reservation *session.CreateReservation, recipe launchplan.StableRecipe, spec platform.ResolvedLaunchSpec, claims []*remote.SharedLaunchAdmission, sharedKinds ...remote.SharedServiceKind) (int, error) {
	if reservation == nil || reservation.SessionID() == "" || a.control == nil || a.processRegistry == nil {
		return 0, remote.ErrControlNotReady
	}
	sessionID := reservation.SessionID()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// WSL 启动预检融合：一次 wsl.exe 调用同时取得身份三件套与工作目录定谳，
	// 结果只在本次启动 RPC 作用域内显式传递（供给闸门 + PTY 启动共用），不做
	// 跨启动缓存。失败 fail-closed 阻断启动；成功后 WorkDir 即定谳绝对路径。
	var preflight *platform.WSLLaunchPreflight
	if spec.BootstrapMode == platform.BootstrapWSL {
		preflightStart := time.Now()
		// 阶段提示（best-effort）：冷启动探测前先让前端亮出「正在唤醒 WSL」，
		// 数秒等待不再无反馈。
		a.emitLaunchProgress(launchProgressStagePreflight, "WSL 预检开始：探测发行版与工作目录", spec.AppType)
		pf, err := wslLaunchPreflightProbeFn(ctx, wslDefaultDistroFn(spec.Env.Variables), spec.WorkDir)
		if err != nil {
			return 0, err
		}
		preflight = &pf
		spec.WorkDir = pf.ResolvedWorkDir
		spec.WSLWorkDirPreflightResolved = true
		a.emitLaunchProgress(launchProgressStagePreflight, "WSL 预检完成：发行版已就绪", spec.AppType)
		if a.Log != nil {
			a.Log.Info("session", "WSL 启动预检完成（一次探测取得身份与定谳目录）",
				fmt.Sprintf("id=%s 耗时=%dms distro用户uid=%s 定谳目录=%s", sessionID, time.Since(preflightStart).Milliseconds(), pf.Identity.UID, pf.ResolvedWorkDir))
		}
	}
	gateStart := time.Now()
	if err := runWSLSharedFilesForSpec(ctx, a.WSLSupply, spec, preflight); err != nil {
		return 0, fmt.Errorf("WSL shared supply: %w", err)
	}
	if preflight != nil {
		// 仅 WSL 启动发进度事件（非 WSL 无事件 → 前端维持现状文案）。
		a.emitLaunchProgress(launchProgressStageSupply, "共享配置供给完成", spec.AppType)
		if a.Log != nil {
			a.Log.Info("session", "WSL 供给闸门完成", fmt.Sprintf("id=%s 耗时=%dms", sessionID, time.Since(gateStart).Milliseconds()))
		}
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
			var admission *remote.SharedLaunchAdmission
			for _, claim := range claims {
				if claim != nil && claim.Kind() == kind {
					admission = claim
					break
				}
			}
			var lease *remote.SharedDependencyLease
			var acqErr error
			if admission != nil {
				lease, acqErr = a.sharedCoord.AcquireForRunWithAdmission(ctx, runPermit, kind, a.sharedFingerprint(kind), admission)
			} else {
				lease, acqErr = a.sharedCoord.AcquireForRun(ctx, runPermit, kind, a.sharedFingerprint(kind))
			}
			if acqErr != nil {
				if kind == remote.SharedServiceClaudeCredentials {
					// 代际变化/协调器关闭：返回重新规划语义错误（design §6.3：
					// 启动前代际变更 → 重新规划，不带旧代际放行）。
					abortHidden(errClaudeCredentialsGenerationChanged)
					return 0, fmt.Errorf("acquire shared credential lease: %w", errClaudeCredentialsGenerationChanged)
				}
				abortHidden(acqErr)
				return 0, fmt.Errorf("acquire shared lease: %w", acqErr)
			}
			a.rememberSharedLease(sessionID, lease)
		}
	}

	ptyStart := time.Now()
	start, err := a.Pty.StartResolvedWithRunEvidence(sessionID, spec, obsPermit)
	if err != nil {
		abortHidden(err)
		return 0, err
	}
	if preflight != nil {
		a.emitLaunchProgress(launchProgressStagePTY, "PTY 已启动", spec.AppType)
	}
	if a.Log != nil {
		a.Log.Info("session", "PTY 启动完成", fmt.Sprintf("id=%s 耗时=%dms", sessionID, time.Since(ptyStart).Milliseconds()))
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
		Session: resolvedDesktopSession(reservation.Session(), spec), Recipe: recipe, BindingID: registryKey.BindingID,
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
//
// RC1-0 缺口 A（T0-1 完成度矩阵 §4.2）：appSessionRaw 同时满足 remote.PTYRawPort。
// v1 WS 数据面的写入目标唯一取自 adapter.sessRaw.(PTYRawPort)
// （ws_v1_session.go rawPTYPort）；生产 sessRaw 若缺
// WriteRaw/ResizeRaw/DetachSession 三方法，断言失败 → raw==nil → input/resize
// 帧静默丢弃。三个新方法委托既有 appPTYRaw（pty.Service 的唯一适配层）。
type appSessionRaw struct {
	pty      ptySessionRawBackend
	sessions sessionLifecycle
}

// newAppSessionRaw is the single production assembly of the remote adapter's
// sessRaw (app.go NewRemoteSessionAdapter wiring). Root-package tests prove via
// this constructor that the production value satisfies remote.PTYRawPort and
// really writes through the real PTY data plane.
func newAppSessionRaw(app *App) appSessionRaw {
	return appSessionRaw{pty: app.Pty, sessions: app.Sessions}
}

// Compile-time proof (RC1-0 缺口 A): the production sessRaw type carries the
// v1 WS raw port in addition to remote.SessionRawPort.
var (
	_ remote.SessionRawPort = appSessionRaw{}
	_ remote.PTYRawPort     = appSessionRaw{}
)

// ptySessionRawBackend is the *pty.Service surface appSessionRaw needs: legacy
// lifecycle close/resize plus the raw write/detach port for the v1 WS data
// plane (RC1-0 缺口 A). Production satisfies it with app.Pty.
type ptySessionRawBackend interface {
	Close(sessionID string) error
	Resize(ctx context.Context, sessionID string, cols, rows int) error
	WriteRaw(ctx context.Context, sessionID string, data []byte) error
	DetachSession(sessionID string) (*pty.DetachReceipt, error)
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

// WriteRaw/ResizeRaw/DetachSession implement remote.PTYRawPort for the v1 WS
// data plane (RC1-0 缺口 A). Each delegates to the existing appPTYRaw adapter
// (the single pty.Service adaptation, shared with the desktop control path);
// the ws handlers keep their raw==nil fail-closed drop path for adapters whose
// sessRaw still lacks the port.
func (a appSessionRaw) WriteRaw(ctx context.Context, sessionID string, data []byte) error {
	return appPTYRaw{a.pty}.WriteRaw(ctx, sessionID, data)
}

func (a appSessionRaw) ResizeRaw(ctx context.Context, sessionID string, cols, rows int) error {
	return appPTYRaw{a.pty}.ResizeRaw(ctx, sessionID, cols, rows)
}

func (a appSessionRaw) DetachSession(sessionID string) (remote.BackendDetachReceipt, error) {
	return appPTYRaw{a.pty}.DetachSession(sessionID)
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

// sharedServiceFingerprint computes SHA-256 over the exact service, upstream,
// and listen-port tuple. The same tuple is used by planner, executor
// verification, mutation guards, and lease promotion.
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
