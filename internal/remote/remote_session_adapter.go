package remote

// remote_session_adapter.go — M2 RemoteSessionAdapter: the application-layer
// adapter that bridges the M3-A control gate/runtime to the REST/WS v1 DTOs
// (design §4.1, §4.2, §5).
//
// The adapter is the ONLY entry point from REST handlers / WS actors to the
// control infrastructure. It composes:
//   - ControlGate (acquire/release/lifecycle/launch);
//   - ControlRuntime (attach/detach + directory/hub);
//   - SessionCatalog (public session metadata);
//   - SessionStreamStore (volatile Seq/window);
//   - SessionOperationJournal (dangerous-op durability);
//   - RemoteLaunchResolver (host launch context);
//   - LaunchRawPort / SessionRawPort (raw effects, behind the gate).
//
// Design constraint (§4.1): "handler不得直调raw或构造permit". The adapter
// encapsulates all gate/raw calls; handlers only see application results.
// The adapter NEVER returns raw errors; it maps all causes via v1ErrorMapper.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Adapter result types
// ---------------------------------------------------------------------------

// SessionListResult is the list outcome.
type SessionListResult struct {
	List contract.SessionList
}

// SessionDetailResult is the detail/create/stop/restart outcome.
type SessionDetailResult struct {
	Detail contract.SessionDetail
}

// ControlResult is the acquire/release outcome.
type ControlResult struct {
	Snapshot contract.ControlSnapshot
}

// AdapterError is the typed application error returned by adapter methods. It
// carries the restError for the handler to write.
type AdapterError struct {
	Rest restError
}

func (e *AdapterError) Error() string { return e.Rest.body.Message }

// newAdapterError wraps a restError into an AdapterError.
func newAdapterError(re restError) *AdapterError { return &AdapterError{Rest: re} }

// ---------------------------------------------------------------------------
// RemoteSessionAdapter
// ---------------------------------------------------------------------------

// RemoteSessionAdapter bridges the control infrastructure to REST/WS DTOs.
type RemoteSessionAdapter struct {
	gate      ControlGate
	runtime   *ControlRuntime
	catalog   *SessionCatalog
	streams   *SessionStreamStore
	journal   SessionOperationJournal
	resolver  RemoteLaunchResolver
	launchRaw LaunchRawPort
	sessRaw   SessionRawPort
	mapper    v1ErrorMapper
	clock     Clock

	// configDir is the host config root (for workdir default etc.).
	configDir string

	// destroyLedger (M3-005), if set, releases the CG-03 per-session input ledger
	// when a session is authoritatively removed (LifecycleRemove commit). Wired by
	// the Server, which owns the SessionInputLedgerRegistry. The App additionally
	// calls Server.DestroySessionInputLedger after desktop RemoveSession / batch
	// clear commits to cover the cross-package desktop paths.
	destroyLedger func(contract.SessionID)
}

// NewRemoteSessionAdapter creates an adapter with the given dependencies.
// launchRaw/sessRaw may be nil (handler returns service.down for ops requiring
// them). resolver may be nil (defaults to noop → all creates fail closed).
func NewRemoteSessionAdapter(
	gate ControlGate,
	runtime *ControlRuntime,
	catalog *SessionCatalog,
	streams *SessionStreamStore,
	journal SessionOperationJournal,
	resolver RemoteLaunchResolver,
	launchRaw LaunchRawPort,
	sessRaw SessionRawPort,
	clock Clock,
	configDir string,
) *RemoteSessionAdapter {
	if resolver == nil {
		resolver = NewNoopRemoteLaunchResolver()
	}
	if clock == nil {
		clock = wallClock{}
	}
	// M-003: wire the v1 stream pump into the projector so committed observations
	// are pumped to the stream Seq + causal hub for remote attach/live delivery.
	if runtime != nil {
		runtime.Projector().SetStreamPump(streams)
	}
	return &RemoteSessionAdapter{
		gate:      gate,
		runtime:   runtime,
		catalog:   catalog,
		streams:   streams,
		journal:   journal,
		resolver:  resolver,
		launchRaw: launchRaw,
		sessRaw:   sessRaw,
		mapper:    newV1ErrorMapper(),
		clock:     clock,
		configDir: configDir,
	}
}

// Gate returns the underlying control gate (for WS adapter attach/detach).
func (a *RemoteSessionAdapter) Gate() ControlGate { return a.gate }

// Runtime returns the control runtime (for WS adapter attach).
func (a *RemoteSessionAdapter) Runtime() *ControlRuntime { return a.runtime }

// Catalog returns the session catalog.
func (a *RemoteSessionAdapter) Catalog() *SessionCatalog { return a.catalog }

// Streams returns the stream store.
func (a *RemoteSessionAdapter) Streams() *SessionStreamStore { return a.streams }

// Journal returns the operation journal.
func (a *RemoteSessionAdapter) Journal() SessionOperationJournal { return a.journal }

// Mapper returns the error mapper.
func (a *RemoteSessionAdapter) Mapper() v1ErrorMapper { return a.mapper }

// Clock returns the clock.
func (a *RemoteSessionAdapter) Clock() Clock { return a.clock }

// ---------------------------------------------------------------------------
// List (design §5.2 endpoint 2, §5.3)
// ---------------------------------------------------------------------------

// ListSessions returns a sorted list of public, non-removed sessions with
// audience-relative control projection (design §5.3).
func (a *RemoteSessionAdapter) ListSessions(ctx context.Context, viewer contract.DeviceID) (SessionListResult, *AdapterError) {
	entries := a.catalog.ListEntries()
	list := make(contract.SessionList, 0, len(entries))
	for _, e := range entries {
		snap, err := a.gate.SnapshotForDevice(e.id, viewer)
		if err != nil {
			// Gate unhealthy for this session: return 503.
			re, ok := a.mapper.mapGateError(contract.RequestID(""), err)
			if !ok {
				re = a.mapper.mapGenericError(contract.RequestID(""))
			}
			return SessionListResult{}, newAdapterError(re)
		}
		list = append(list, contract.SessionSummary{
			ID:             e.id,
			Title:          e.title,
			CLIType:        e.cliType,
			State:          a.sessionState(e.id),
			Control:        snap,
			LastActivityAt: formatUTC(e.lastActivityAt),
		})
	}
	return SessionListResult{List: list}, nil
}

// ---------------------------------------------------------------------------
// Detail (design §5.2 endpoint 3, §5.3)
// ---------------------------------------------------------------------------

// SessionDetail returns the detail projection for a session (design §5.3).
// staging/removed/unknown → session.not_found.
func (a *RemoteSessionAdapter) SessionDetail(ctx context.Context, reqID contract.RequestID, sessionID contract.SessionID, viewer contract.DeviceID) (SessionDetailResult, *AdapterError) {
	entry, ok := a.catalog.Entry(sessionID)
	if !ok {
		return SessionDetailResult{}, newAdapterError(restError{
			status: 404,
			body:   newAPIError(reqID, contract.ErrorCodeSessionNotFound, contract.ErrorLayerSession, "session not found", contract.ActionHintRetry),
		})
	}
	snap, err := a.gate.SnapshotForDevice(sessionID, viewer)
	if err != nil {
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}
	earliest, latest := a.streams.SeqBounds(sessionID)
	return SessionDetailResult{
		Detail: contract.SessionDetail{
			SessionSummary: contract.SessionSummary{
				ID:             sessionID,
				Title:          entry.title,
				CLIType:        entry.cliType,
				State:          a.sessionState(sessionID),
				Control:        snap,
				LastActivityAt: formatUTC(entry.lastActivityAt),
			},
			Workdir:     entry.workdir,
			StartedAt:   formatUTC(entry.startedAt),
			EarliestSeq: earliest,
			LatestSeq:   latest,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Create (design §5.2 endpoint 4, §4.4, §5.4)
// ---------------------------------------------------------------------------

// CreateSession starts a new session. It resolves the launch context, begins a
// launch transaction, executes effects, and activates the run (design §4.4).
func (a *RemoteSessionAdapter) CreateSession(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, req contract.CreateSessionRequest) (SessionDetailResult, *AdapterError) {
	if a.launchRaw == nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	// 1. Resolve launch context.
	resolution, lf := a.resolver.ResolveCreate(ctx, req)
	if lf != nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, lf))
	}
	// 2. Begin device launch.
	permit, err := a.gate.BeginDeviceLaunch(ctx, principal)
	if err != nil {
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}
	// 3. Mint session ID.
	sessionID := contract.SessionID(GenerateOperationID())
	if sessionID == "" {
		a.gate.AbortLaunch(ctx, permit, fmt.Errorf("crypto/rand failure"))
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	// 4. Register starting session (staging, not yet public).
	runPermit, obsPermit, err := a.gate.RegisterStartingSession(ctx, permit, sessionID)
	if err != nil {
		a.gate.AbortLaunch(ctx, permit, err)
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(LaunchResolveFailureEffect, resolution.Recipe.CLIType))
		}
		return SessionDetailResult{}, newAdapterError(re)
	}
	// 5. Execute launch effect (start process). M-004: checkpoint before the
	// irreversible start syscall, and register compensation so an abort stops
	// the started process. M-003: pass the obsPermit so the remote-launched PTY
	// is run-scoped (its output/exit flow through the H1 committer).
	if err := a.gate.DoLaunchEffect(ctx, runPermit, LaunchPTYStart, func(ctx context.Context, p *operationPermit, receipt *EffectReceipt) error {
		if err := p.Checkpoint(ctx, 1); err != nil {
			return err
		}
		receipt.MarkApplied(func(compCtx context.Context) error {
			if a.sessRaw != nil {
				return a.sessRaw.StopSession(compCtx, sessionID)
			}
			return nil
		})
		return a.launchRaw.StartProcess(ctx, sessionID, resolution.Recipe, resolution.Spec, obsPermit)
	}); err != nil {
		a.gate.AbortLaunch(ctx, permit, err)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(LaunchResolveFailureEffect, resolution.Recipe.CLIType)))
	}
	// 6. Activate run → public.
	if err := a.gate.ActivateRun(ctx, runPermit); err != nil {
		a.gate.AbortLaunch(ctx, permit, err)
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}
	// 7. Catalog publish.
	now := a.clock.Now()
	title := safeTitle(resolution.Recipe.CLIType, resolution.Recipe.Workdir)
	a.catalog.Activate(sessionID, title, resolution.Recipe.CLIType, resolution.Recipe.Workdir, now)
	a.catalog.StoreRecipe(sessionID, resolution.Recipe) // M-004: faithful restart re-resolve
	a.streams.BeginSegment(sessionID, 1)
	// M-004: activate the H1 run segment for remote-launched sessions too (writes
	// the runActivated first record + initializes feed.currentRun) so a later
	// restart can H1-seal the old segment atomically.
	if a.runtime != nil {
		a.runtime.Projector().TrackRun(sessionID, obsPermit)
	}

	// 8. Build detail response (control starts as none).
	snap, _ := a.gate.SnapshotForDevice(sessionID, principal.DeviceID)
	earliest, latest := a.streams.SeqBounds(sessionID)
	return SessionDetailResult{
		Detail: contract.SessionDetail{
			SessionSummary: contract.SessionSummary{
				ID:             sessionID,
				Title:          title,
				CLIType:        resolution.Recipe.CLIType,
				State:          contract.SessionStateRunning,
				Control:        snap,
				LastActivityAt: formatUTC(now),
			},
			Workdir:     resolution.Recipe.Workdir,
			StartedAt:   formatUTC(now),
			EarliestSeq: earliest,
			LatestSeq:   latest,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Stop (design §5.2 endpoint 5, §5.5)
// ---------------------------------------------------------------------------

// StopSession stops a running session (design §5.5). Requires the holder
// DeviceID (connected or grace); does NOT require a live lease.
func (a *RemoteSessionAdapter) StopSession(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID) (SessionDetailResult, *AdapterError) {
	return a.lifecycle(ctx, reqID, principal, sessionID, LifecycleStop, SessionOpStop)
}

// ---------------------------------------------------------------------------
// Restart (design §5.2 endpoint 6, §4.5, §5.5)
// ---------------------------------------------------------------------------

// RestartSession restarts a session with the same ID/recipe (design §5.5).
func (a *RemoteSessionAdapter) RestartSession(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID) (SessionDetailResult, *AdapterError) {
	return a.lifecycle(ctx, reqID, principal, sessionID, LifecycleRestart, SessionOpRestart)
}

// ---------------------------------------------------------------------------
// Remove (design §5.2 endpoint 7, §5.5)
// ---------------------------------------------------------------------------

// RemoveSession removes a session (design §5.5). Stops if needed, then exact
// remove commit.
func (a *RemoteSessionAdapter) RemoveSession(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID) *AdapterError {
	_, aerr := a.lifecycle(ctx, reqID, principal, sessionID, LifecycleRemove, SessionOpRemove)
	return aerr
}

// lifecycle is the shared stop/restart/remove path (design §5.5).
func (a *RemoteSessionAdapter) lifecycle(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID, op LifecycleOperation, jop SessionOperationKind) (SessionDetailResult, *AdapterError) {
	// Verify the session is public.
	entry, ok := a.catalog.Entry(sessionID)
	if !ok {
		return SessionDetailResult{}, newAdapterError(restError{
			status: 404,
			body:   newAPIError(reqID, contract.ErrorCodeSessionNotFound, contract.ErrorLayerSession, "session not found", contract.ActionHintRetry),
		})
	}
	// Journal: begin intent (fail-closed if journal not ready).
	opID := GenerateOperationID()
	if opID == "" {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	permit, jerr := a.journal.BeginIntent(ctx, SessionOperationIntent{
		OperationID: opID,
		SessionID:   sessionID,
		CLIType:     entry.cliType,
		Operation:   jop,
		Actor:       SessionActorRemote,
	})
	if jerr != nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapJournalError(reqID))
	}
	// Execute lifecycle via gate.
	var rawEffect RawSessionEffect
	switch op {
	case LifecycleStop:
		rawEffect = func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			if a.sessRaw != nil {
				if err := a.sessRaw.StopSession(ctx, sessionID); err != nil {
					return SessionMutationResult{}, err
				}
			}
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		}
	case LifecycleRestart:
		rawEffect = func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			return a.restartRawEffect(ctx, p, sessionID, entry)
		}
	case LifecycleRemove:
		rawEffect = func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
			if err := p.Checkpoint(ctx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			if a.sessRaw != nil {
				if err := a.sessRaw.RemoveSession(ctx, sessionID); err != nil {
					return SessionMutationResult{}, err
				}
			}
			return SessionMutationResult{Removed: true}, nil
		}
	}
	result, err := a.gate.DoDeviceLifecycle(ctx, principal, sessionID, op, rawEffect)
	if err != nil {
		// Journal: failed.
		fc := contract.ErrorCodeServiceDown
		if kind, ok := extractControlDenyKind(err); ok {
			fc = gateDenyToErrorCode(kind)
		}
		_ = a.journal.Complete(ctx, permit, SessionOutcomeFailed, fc)
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}
	// Journal: committed.
	_ = a.journal.Complete(ctx, permit, SessionOutcomeCommitted, "")
	// Post-lifecycle catalog/stream updates.
	now := a.clock.Now()
	a.catalog.TouchActivity(sessionID, now)
	if op == LifecycleRemove && result.Removed {
		a.catalog.Remove(sessionID)
		a.streams.RemoveStream(sessionID)
		// M3-005：权威 remove 提交后销毁该 session 的 CG-03 ledger（remote REST remove
		// 路径）。desktop remove/clear 跨包路径由 App 经 Server.DestroySessionInputLedger
		// 接线（二者命中其一即可，Destroy 幂等）。
		if a.destroyLedger != nil {
			a.destroyLedger(sessionID)
		}
		return SessionDetailResult{}, nil
	}
	if op == LifecycleRestart && result.RestartBoundary {
		// M-004: the restart boundary is committed via H1 CommitRestartSegment inside
		// restartRawEffect (and pumped to the v1 stream there). No manual
		// AppendBoundary / best-effort H3 seal here — the H1 seal already performed
		// SealRunSegmentUnderState atomically in the three-lock domain.
	}
	// Build detail response.
	snap, _ := a.gate.SnapshotForDevice(sessionID, principal.DeviceID)
	earliest, latest := a.streams.SeqBounds(sessionID)
	return SessionDetailResult{
		Detail: contract.SessionDetail{
			SessionSummary: contract.SessionSummary{
				ID:             sessionID,
				Title:          entry.title,
				CLIType:        entry.cliType,
				State:          result.State,
				Control:        snap,
				LastActivityAt: formatUTC(now),
			},
			Workdir:     entry.workdir,
			StartedAt:   formatUTC(entry.startedAt),
			EarliestSeq: earliest,
			LatestSeq:   latest,
		},
	}, nil
}

// restartRawEffect performs a REAL three-phase restart (M-004/R4-001): seal old
// segment → checkpoint+stop old process → re-resolve → reserve/mint a hidden run
// → checkpoint+StartProcess with its observation identity → exact activate. Only
// activate swaps currentRun, commits boundary-first, and marks the backend
// healthy. Every irreversible raw step is checkpointed; every post-seal failure
// compensates any started process and rolls back the exact feed+ledger seal.
//
// R3-001 failure consistency: restart is a transaction. Once any irreversible
// step has a side effect (seal advances the feed, stop kills the process,
// commit swaps the run + writes the boundary), a later failure MUST NOT leave
// the session presenting as running. On any error the entry is reconciled to an
// honest terminal/unavailable state (ReconcileRestartFailure) so the session is
// never fake-running; the recovery path is an explicit Stop then Restart.
// Seal failure before any side effect is also reconciled for a single uniform
// terminal contract (the permit may be stale and the old-segment state
// uncertain); the durable feed records remain as an audit trail.
func (a *RemoteSessionAdapter) restartRawEffect(ctx context.Context, p *operationPermit, sessionID contract.SessionID, entry catalogEntry) (SessionMutationResult, error) {
	rt := a.runtime
	if rt == nil {
		return SessionMutationResult{}, &ControlGateError{Kind: DenyControlUnavailable}
	}
	// R4-001: capture the runEpoch this restart attempt is operating on, so a
	// stale/late reconcile (e.g. from a timeout-abandoned raw goroutine that
	// later errors) is bound to THIS generation and cannot clobber a newer
	// successful run. During stage p.runEpoch deliberately remains the old public
	// generation; only successful activate refreshes it.
	failedEpoch := p.runEpoch
	// reconcile is idempotent + safe to call on any failure path below. It is
	// generation-bound: a no-op if a newer run is already current.
	reconcile := func() { rt.ReconcileRestartFailure(sessionID, failedEpoch) }
	// Step 1 (§4.5 step 2): H1 seal the old segment — fences late old-run
	// observations (ObservationDroppedSegmentSealed) before the old process stops.
	sealReceipt, err := rt.SealRestartSegmentForPermit(p, sessionID)
	if err != nil {
		reconcile()
		return SessionMutationResult{}, err
	}
	// Every failure after seal rolls back this transaction's exact feed+ledger
	// seal before reconciliation. If a newer run superseded us, rollback is an
	// exact no-op and generation-bound reconcile likewise cannot clobber it.
	rollbackSeal := func() { _ = rt.RollbackRestartSealForPermit(p, sealReceipt, sessionID) }
	failBeforeStage := func(cause error) (SessionMutationResult, error) {
		rollbackSeal()
		reconcile()
		return SessionMutationResult{}, cause
	}
	// Step 2: stop the old process (outside the three-lock domain).
	if err := p.Checkpoint(ctx, 1); err != nil {
		return failBeforeStage(err)
	}
	if a.sessRaw != nil {
		if err := a.sessRaw.StopSession(ctx, sessionID); err != nil {
			return failBeforeStage(err)
		}
	}
	// Step 3: re-resolve the stored recipe (host defaults fill provider/model refs).
	if a.resolver == nil || a.launchRaw == nil {
		return failBeforeStage(&ControlGateError{Kind: DenyControlUnavailable})
	}
	recipe, ok := a.catalog.Recipe(sessionID)
	if !ok {
		recipe = RemoteLaunchRecipe{CLIType: entry.cliType, Workdir: entry.workdir}
	}
	resolution, lf := a.resolver.ResolveRestart(ctx, recipe)
	if lf != nil {
		return failBeforeStage(&ControlGateError{Kind: DenyControlUnavailable})
	}
	// Step 4: reserve the new epoch + mint a hidden identity. currentRun/runEpoch
	// remain the old public generation until activate.
	obsPermit, err := rt.StageRestartRun(p, sealReceipt, sessionID)
	if err != nil {
		return failBeforeStage(err)
	}
	// Abort helper for failures after stage. It also performs the exact seal
	// rollback; reconciliation remains bound to the still-public old epoch.
	abortStage := func(cause error, compensateStarted bool) (SessionMutationResult, error) {
		var compensationErr error
		if compensateStarted && a.sessRaw != nil {
			compensationErr = a.sessRaw.StopSession(ctx, sessionID)
		}
		rollbackErr := rt.AbortRestartStage(p, sealReceipt, sessionID)
		reconcile()
		return SessionMutationResult{}, errors.Join(cause, compensationErr, rollbackErr)
	}
	// Step 5: start with the exact staged observation identity. It is not yet the
	// entry/feed current run, so pre-activate output cannot publish a boundary or
	// masquerade as active.
	if err := p.Checkpoint(ctx, 2); err != nil {
		return abortStage(err, false)
	}
	if err := a.launchRaw.StartProcess(ctx, sessionID, resolution.Recipe, resolution.Spec, obsPermit); err != nil {
		// Start APIs can fail after partial host side effects; exact-lane cleanup is
		// idempotent and closes any same-ID partial backend before seal rollback.
		return abortStage(err, true)
	}
	// Step 6: activate is the sole pointer/boundary/healthy commit. If its exact
	// fence fails, close the just-started backend before releasing the lane.
	if _, err := rt.ActivateRestartRun(p, sealReceipt, sessionID); err != nil {
		return abortStage(err, true)
	}
	return SessionMutationResult{State: contract.SessionStateRunning, StateChanged: true, RestartBoundary: true}, nil
}

// ---------------------------------------------------------------------------
// Acquire / Release (design §5.2 endpoints 8/9)
// ---------------------------------------------------------------------------

// AcquireControl grants control to the device (design §5.2 endpoint 8).
// Requires a current live lease (from WS attach).
func (a *RemoteSessionAdapter) AcquireControl(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID, lease *ControlConnectionLease) (ControlResult, *AdapterError) {
	if _, ok := a.catalog.Entry(sessionID); !ok {
		return ControlResult{}, newAdapterError(restError{
			status: 404,
			body:   newAPIError(reqID, contract.ErrorCodeSessionNotFound, contract.ErrorLayerSession, "session not found", contract.ActionHintRetry),
		})
	}
	if lease == nil || !lease.IsLive() {
		return ControlResult{}, newAdapterError(restError{
			status: 403,
			body:   newAPIError(reqID, contract.ErrorCodeControlForbidden, contract.ErrorLayerControl, "active session connection required", contract.ActionHintRequestControl),
		})
	}
	snap, err := a.gate.Acquire(ctx, principal, lease, sessionID)
	if err != nil {
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return ControlResult{}, newAdapterError(re)
	}
	return ControlResult{Snapshot: snap}, nil
}

// ReleaseControl releases control (design §5.2 endpoint 9). Exact current
// device holder.
func (a *RemoteSessionAdapter) ReleaseControl(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID) (ControlResult, *AdapterError) {
	if _, ok := a.catalog.Entry(sessionID); !ok {
		return ControlResult{}, newAdapterError(restError{
			status: 404,
			body:   newAPIError(reqID, contract.ErrorCodeSessionNotFound, contract.ErrorLayerSession, "session not found", contract.ActionHintRetry),
		})
	}
	snap, err := a.gate.Release(ctx, principal, sessionID)
	if err != nil {
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return ControlResult{}, newAdapterError(re)
	}
	return ControlResult{Snapshot: snap}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// sessionState returns the wire session state for a session. It reads the
// arbiter's state mirror (if available) or falls back to catalog presence.
func (a *RemoteSessionAdapter) sessionState(sessionID contract.SessionID) contract.SessionState {
	if a.runtime != nil {
		mirror, _, ok := a.runtime.Arbiter().SessionStateMirror(sessionID)
		if ok && mirror != "" {
			return mirror
		}
	}
	// Fallback: if in catalog, assume running.
	if a.catalog.IsPublic(sessionID) {
		return contract.SessionStateRunning
	}
	return contract.SessionStateUnavailable
}

// gateDenyToErrorCode maps a ControlDenyKind to a wire ErrorCode (for journal
// failure code).
func gateDenyToErrorCode(kind ControlDenyKind) contract.ErrorCode {
	switch kind {
	case DenyBusy:
		return contract.ErrorCodeControlBusy
	case DenyNotController, DenyNoAuthoritativeAttachment:
		return contract.ErrorCodeControlForbidden
	case DenySessionNotFound:
		return contract.ErrorCodeSessionNotFound
	case DenyDeviceRevoked:
		return contract.ErrorCodeAuthRevoked
	default:
		return contract.ErrorCodeServiceDown
	}
}

// formatUTC formats a time as RFC3339Nano UTC 'Z'.
func formatUTC(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// wallClock wraps systemClock (implements Clock).
type wallClock = systemClock
