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

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
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

	// authority is the sole production identity/membership owner. catalog remains
	// only as a compatibility fixture when authority is nil.
	authority       *session.Manager
	processRegistry *processcap.Registry
	planner         launchplan.Planner
	executor        launchplan.Executor
	sharedCoord     *SharedServiceCoordinator
	ptyStart        restartPtyStartFunc
	journalRetries  *JournalRetryRegistry
	removeGC        *RemoveGCRegistry
	releaseShared   func(string)

	// prepareSharedLeaseTransfer binds pending leases to the exact Authority
	// composite commit and atomically updates the App owner registry.
	prepareSharedLeaseTransfer func(string, uint64, uint64, []*SharedDependencyLease) (func(), func(), func(), error)
	releaseSharedExact         func(string, uint64)

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

// SetSessionAuthority switches the adapter to the production single-owner path.
// A nil dependency keeps tests fail-closed on the legacy fixture path.
func (a *RemoteSessionAdapter) SetSessionAuthority(authority *session.Manager, registry *processcap.Registry, planner launchplan.Planner) {
	a.authority = authority
	a.processRegistry = registry
	if planner == nil {
		planner = launchplan.NewFailClosedPlanner()
	}
	a.planner = planner
	if authority != nil {
		// Production has no second membership owner. SessionCatalog remains only
		// as an isolated compatibility fixture for pre-Authority unit tests.
		a.catalog = nil
		if a.journalRetries == nil {
			a.journalRetries = NewJournalRetryRegistry()
		}
		if a.removeGC == nil {
			a.removeGC = NewRemoveGCRegistry()
		}
	}
	if a.runtime != nil && authority != nil {
		a.runtime.Projector().SetSessionAuthority(authority)
	}
}

func (a *RemoteSessionAdapter) SetPostRemoveCleanup(releaseShared func(string)) {
	a.releaseShared = releaseShared
}

// SetLaunchExecutor injects the production Executor and shared-service
// coordinator for remote create. Both must be non-nil for create to succeed;
// nil executor keeps create fail-closed.
func (a *RemoteSessionAdapter) SetLaunchExecutor(executor launchplan.Executor, coord *SharedServiceCoordinator) {
	a.executor = executor
	a.sharedCoord = coord
}

// SetSharedLeaseTransfer injects the exact App/coordinator composite owner.
func (a *RemoteSessionAdapter) SetSharedLeaseTransfer(
	prepare func(string, uint64, uint64, []*SharedDependencyLease) (func(), func(), func(), error),
	releaseExact func(string, uint64),
) {
	a.prepareSharedLeaseTransfer = prepare
	a.releaseSharedExact = releaseExact
}

// restartPtyStartFunc starts a new PTY for a restart operation. The spec is
// passed as any to avoid importing platform in this package; the caller
// (root package) type-asserts before calling.
type restartPtyStartFunc func(sessionID string, spec any, runHandle any) (processcap.StartEvidence, error)

// SetRestartPtyStart injects the PTY start function for restart operations.
func (a *RemoteSessionAdapter) SetRestartPtyStart(fn restartPtyStartFunc) {
	a.ptyStart = fn
}

func (a *RemoteSessionAdapter) FlushPostCommitDebt(ctx context.Context) bool {
	journalOK := a.journalRetries == nil || a.journalRetries.Flush(ctx)
	gcOK := a.removeGC == nil || a.removeGC.Flush(ctx)
	return journalOK && gcOK
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
	if a.authority != nil {
		snapshots := a.authority.ListRemoteSafeSnapshots()
		list := make(contract.SessionList, 0, len(snapshots))
		for _, authoritySnapshot := range snapshots {
			sid := contract.SessionID(authoritySnapshot.Handle.SessionID())
			controlSnapshot, err := a.gate.SnapshotForDevice(sid, viewer)
			if err != nil {
				re, ok := a.mapper.mapGateError(contract.RequestID(""), err)
				if !ok {
					re = a.mapper.mapGenericError(contract.RequestID(""))
				}
				return SessionListResult{}, newAdapterError(re)
			}
			list = append(list, contract.SessionSummary{
				ID: sid, Title: authoritySnapshot.SafeTitle, CLIType: contract.CLIType(authoritySnapshot.CLIType),
				State: authorityStateToWire(authoritySnapshot.State), Control: controlSnapshot,
				LastActivityAt: formatUTC(authoritySnapshot.LastActivityAt),
			})
		}
		return SessionListResult{List: list}, nil
	}
	entries := a.catalog.ListEntries()
	list := make(contract.SessionList, 0, len(entries))
	for _, e := range entries {
		snap, err := a.gate.SnapshotForDevice(e.id, viewer)
		if err != nil {
			re, ok := a.mapper.mapGateError(contract.RequestID(""), err)
			if !ok {
				re = a.mapper.mapGenericError(contract.RequestID(""))
			}
			return SessionListResult{}, newAdapterError(re)
		}
		list = append(list, contract.SessionSummary{ID: e.id, Title: e.title, CLIType: e.cliType, State: a.sessionState(e.id), Control: snap, LastActivityAt: formatUTC(e.lastActivityAt)})
	}
	return SessionListResult{List: list}, nil
}

// ---------------------------------------------------------------------------
// Detail (design §5.2 endpoint 3, §5.3)
// ---------------------------------------------------------------------------

// SessionDetail returns the detail projection for a session (design §5.3).
// staging/removed/unknown → session.not_found.
func (a *RemoteSessionAdapter) SessionDetail(ctx context.Context, reqID contract.RequestID, sessionID contract.SessionID, viewer contract.DeviceID) (SessionDetailResult, *AdapterError) {
	if a.authority != nil {
		authoritySnapshot, err := a.authority.RemoteSnapshotByID(string(sessionID))
		if err != nil {
			return SessionDetailResult{}, newAdapterError(sessionNotFoundRestError(reqID))
		}
		controlSnapshot, gateErr := a.gate.SnapshotForDevice(sessionID, viewer)
		if gateErr != nil {
			re, ok := a.mapper.mapGateError(reqID, gateErr)
			if !ok {
				re = a.mapper.mapGenericError(reqID)
			}
			return SessionDetailResult{}, newAdapterError(re)
		}
		earliest, latest := a.streams.SeqBounds(sessionID)
		return authorityDetail(authoritySnapshot, controlSnapshot, earliest, latest), nil
	}
	entry, ok := a.catalog.Entry(sessionID)
	if !ok {
		return SessionDetailResult{}, newAdapterError(sessionNotFoundRestError(reqID))
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
	return SessionDetailResult{Detail: contract.SessionDetail{SessionSummary: contract.SessionSummary{ID: sessionID, Title: entry.title, CLIType: entry.cliType, State: a.sessionState(sessionID), Control: snap, LastActivityAt: formatUTC(entry.lastActivityAt)}, Workdir: entry.workdir, StartedAt: formatUTC(entry.startedAt), EarliestSeq: earliest, LatestSeq: latest}}, nil
}

// ---------------------------------------------------------------------------
// Create (design §5.2 endpoint 4, §4.4, §5.4)
// ---------------------------------------------------------------------------

// CreateSession starts a new session. For production (authority != nil), it
// resolves the launch context via the Planner, begins a launch transaction,
// executes typed Effects via the Executor, and activates the run through the
// composite activation flow (design §4.4, §5.4).
func (a *RemoteSessionAdapter) CreateSession(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, req contract.CreateSessionRequest) (SessionDetailResult, *AdapterError) {
	if a.authority != nil {
		return a.authorityCreateSession(ctx, reqID, principal, req)
	}
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

// authorityCreateSession implements the production remote create flow:
// BuildPlan → ReserveCreate → BeginDeviceLaunch → RegisterStarting →
// Executor Prepare/Apply → shared leases → ProcessEvidence → registry →
// PrepareCompositeActivation → PrepareActivation → CommitPreparedActivation →
// FinishCompositeActivation → detail. Every failure path does exact
// compensation and leaves no public Authority/Control/active process.
func (a *RemoteSessionAdapter) authorityCreateSession(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, req contract.CreateSessionRequest) (SessionDetailResult, *AdapterError) {
	planner := a.planner
	if planner == nil {
		planner = launchplan.NewFailClosedPlanner()
	}
	workdir := ""
	if req.Workdir != nil {
		workdir = *req.Workdir
	}
	stableRefs := &launchplan.StableLaunchRefs{}
	if req.ProviderRef != nil {
		stableRefs.ProviderRef = *req.ProviderRef
	}
	if req.PresetRef != nil {
		stableRefs.PresetRef = *req.PresetRef
	}
	if req.ModelRef != nil {
		stableRefs.ModelRef = *req.ModelRef
	}
	if req.ShellRef != nil {
		stableRefs.ShellRef = *req.ShellRef
	}
	stableRefs.UseHeadroom = req.UseHeadroom

	// 1. BuildPlan (pure read, zero side effects).
	plan, failure := planner.BuildPlan(ctx, launchplan.BuildRequest{
		CLIType: req.CLIType, Origin: launchplan.OriginRemote,
		Mode: launchplan.ModeEmbedded, Workdir: workdir, StableRefs: stableRefs,
	})
	if failure != nil {
		if plan != nil {
			plan.Secrets.Dispose()
		}
		kind := LaunchResolveFailureContext
		switch failure.Kind {
		case launchplan.FailureWorkdir:
			kind = LaunchResolveFailureWorkdir
		case launchplan.FailureCapability:
			kind = LaunchResolveFailureCapability
		}
		return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(kind, req.CLIType)))
	}

	// If no production executor is wired, fail closed (plan was read-only).
	if a.executor == nil {
		plan.Secrets.Dispose()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(LaunchResolveFailureContext, req.CLIType)))
	}

	// 2. Authority hidden ReserveCreate.
	reservation, reserveErr := a.authority.ReserveCreate(session.CreateSpec{
		AppType:        session.AppType(req.CLIType),
		Origin:         launchplan.OriginRemote,
		Mode:           launchplan.ModeEmbedded,
		Workdir:        plan.Recipe.Workdir,
		RemoteEligible: true,
		Provider:       plan.Recipe.ProviderRef,
		Preset:         plan.Recipe.PresetRef,
		Model:          plan.Recipe.ModelRef,
	})
	if reserveErr != nil {
		plan.Secrets.Dispose()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	sessionID := contract.SessionID(reservation.SessionID())

	abortReservation := func() {
		a.authority.AbortCreate(reservation)
	}

	// 3. Begin device launch.
	launchPermit, err := a.gate.BeginDeviceLaunch(ctx, principal)
	if err != nil {
		plan.Secrets.Dispose()
		abortReservation()
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}

	// 4. Register starting session (staging, not yet public).
	runPermit, obsPermit, err := a.gate.RegisterStartingSession(ctx, launchPermit, sessionID)
	if err != nil {
		plan.Secrets.Dispose()
		a.gate.AbortLaunch(ctx, launchPermit, err)
		abortReservation()
		re, ok := a.mapper.mapGateError(reqID, err)
		if !ok {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}

	// 5. Acquire launch admissions before Executor preparation or any shared
	// side effect. The exact admissions are bound into their prepared effects.
	var admissions []*SharedLaunchAdmission
	sharedAdmissions := make(map[launchplan.SharedServiceKind]any)
	releaseAdmissions := func() {
		for _, adm := range admissions {
			if a.sharedCoord != nil {
				a.sharedCoord.ReleaseLaunchAdmission(adm)
			}
		}
	}
	for _, planAdm := range plan.Admissions {
		if a.sharedCoord == nil {
			continue
		}
		remoteKind, ok := sharedKindFromPlan(planAdm.Service)
		if !ok {
			continue
		}
		adm, acqErr := a.sharedCoord.AcquireLaunchAdmissionForConfig(remoteKind, planAdm.ConfigFingerprint)
		if acqErr != nil {
			releaseAdmissions()
			plan.Secrets.Dispose()
			a.gate.AbortLaunch(ctx, launchPermit, acqErr)
			abortReservation()
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
		admissions = append(admissions, adm)
		sharedAdmissions[planAdm.Service] = adm
	}

	// 6. Executor Prepare (all effect allocations; opaque exact run handle).
	execution, err := a.executor.Prepare(ctx, plan, launchplan.ExecutionBinding{
		SessionID: string(sessionID), RunEpoch: obsPermit.RunEpoch(), RunHandle: obsPermit,
		SharedAdmissions: sharedAdmissions,
	})
	if err != nil {
		releaseAdmissions()
		plan.Secrets.Dispose()
		a.gate.AbortLaunch(ctx, launchPermit, err)
		abortReservation()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(LaunchResolveFailureEffect, req.CLIType)))
	}

	// 7. Effect loop (each effect, including exact PTY bootstrap, goes through
	// the same staged run permit and gate checkpoint before publication).
	for i := 0; i < execution.Count(); i++ {
		effectKind := launchEffectKindForSpec(plan.Effects[i])
		effectErr := a.gate.DoLaunchEffect(ctx, runPermit, effectKind, func(effectCtx context.Context, p *operationPermit, receipt *EffectReceipt) error {
			effect := execution.Effect(i)
			effect.ArmOwnership()
			if cpErr := p.Checkpoint(effectCtx, 1); cpErr != nil {
				return cpErr
			}
			evidence, applyErr := effect.Apply(effectCtx)
			if applyErr != nil {
				return applyErr
			}
			execution.RecordApplied(i, evidence)
			return nil
		})
		if effectErr != nil {
			releaseAdmissions()
			execution.Abort(ctx)
			a.gate.AbortLaunch(ctx, launchPermit, effectErr)
			abortReservation()
			execution.DisposeSecrets()
			return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(LaunchResolveFailureEffect, req.CLIType)))
		}
	}

	// 8. Process evidence → register binding.
	start, hasProcess := execution.ProcessEvidence()
	if !hasProcess {
		releaseAdmissions()
		execution.Abort(ctx)
		a.gate.AbortLaunch(ctx, launchPermit, errors.New("no process evidence"))
		abortReservation()
		execution.DisposeSecrets()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(LaunchResolveFailureEffect, req.CLIType)))
	}
	if vErr := start.Validate(processcap.BackendPTY); vErr != nil {
		_ = start.Binding.CloseExact(ctx)
		releaseAdmissions()
		execution.Abort(ctx)
		a.gate.AbortLaunch(ctx, launchPermit, vErr)
		abortReservation()
		execution.DisposeSecrets()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	registryKey, regErr := a.processRegistry.Register(start.Binding, obsPermit.RunEpoch())
	if regErr != nil {
		_ = start.Binding.CloseExact(ctx)
		releaseAdmissions()
		execution.Abort(ctx)
		a.gate.AbortLaunch(ctx, launchPermit, regErr)
		abortReservation()
		execution.DisposeSecrets()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	// 9. M-005: Promote admissions to exact run leases.
	var acquiredLeases []*SharedDependencyLease
	releaseLeases := func() {
		for _, lease := range acquiredLeases {
			if a.sharedCoord != nil {
				_ = a.sharedCoord.ReleaseExact(context.TODO(), lease)
			}
		}
	}
	for idx, planAdm := range plan.Admissions {
		if a.sharedCoord == nil || idx >= len(admissions) {
			continue
		}
		remoteKind, ok := sharedKindFromPlan(planAdm.Service)
		if !ok {
			continue
		}
		lease, acqErr := a.sharedCoord.AcquirePendingForRunWithAdmission(ctx, runPermit, remoteKind, planAdm.ConfigFingerprint, admissions[idx])
		if acqErr != nil {
			closeEvidence := start.Binding.CloseExact(ctx)
			if closeEvidence.Confirmed() {
				_ = a.processRegistry.ReleaseExact(registryKey.BindingID, registryKey.RunGeneration, start.Binding)
			}
			releaseLeases()
			releaseAdmissions()
			execution.Abort(ctx)
			a.gate.AbortLaunch(ctx, launchPermit, acqErr)
			abortReservation()
			execution.DisposeSecrets()
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
		acquiredLeases = append(acquiredLeases, lease)
	}
	// Reserve the exact pending→promoted transfer. No App owner or promoted lease
	// is visible until the Authority composite callback executes.
	sharedCommit := func() {}
	sharedFinish := func() {}
	sharedAbort := func() {}
	if len(acquiredLeases) > 0 {
		if a.prepareSharedLeaseTransfer == nil {
			closeEvidence := start.Binding.CloseExact(ctx)
			if closeEvidence.Confirmed() {
				_ = a.processRegistry.ReleaseExact(registryKey.BindingID, registryKey.RunGeneration, start.Binding)
			}
			releaseLeases()
			releaseAdmissions()
			execution.Abort(ctx)
			a.gate.AbortLaunch(ctx, launchPermit, ErrSharedServiceInUse)
			abortReservation()
			execution.DisposeSecrets()
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
		var transferErr error
		sharedCommit, sharedFinish, sharedAbort, transferErr = a.prepareSharedLeaseTransfer(string(sessionID), 0, obsPermit.RunEpoch(), acquiredLeases)
		if transferErr != nil {
			closeEvidence := start.Binding.CloseExact(ctx)
			if closeEvidence.Confirmed() {
				_ = a.processRegistry.ReleaseExact(registryKey.BindingID, registryKey.RunGeneration, start.Binding)
			}
			releaseLeases()
			releaseAdmissions()
			execution.Abort(ctx)
			a.gate.AbortLaunch(ctx, launchPermit, transferErr)
			abortReservation()
			execution.DisposeSecrets()
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
	}

	// 10. Prepare composite activation (seal prepared state).
	preparedActivation, prepErr := a.runtime.PrepareCompositeActivation(sessionID, runPermit, obsPermit)
	if prepErr != nil {
		sharedAbort()
		closeEvidence := start.Binding.CloseExact(ctx)
		if closeEvidence.Confirmed() {
			_ = a.processRegistry.ReleaseExact(registryKey.BindingID, registryKey.RunGeneration, start.Binding)
		}
		releaseLeases()
		execution.Abort(ctx)
		a.gate.AbortLaunch(ctx, launchPermit, prepErr)
		abortReservation()
		execution.DisposeSecrets()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	// 10. Prepare Authority activation (token/receipt allocation, still hidden).
	values := session.PreparedAuthorityActivation{
		Session: reservation.Session(), Recipe: plan.Recipe, BindingID: registryKey.BindingID,
		PID: start.PID, RunRevision: obsPermit.RunEpoch(), StartedAt: reservation.Session().StartedAt,
		LastActivityAt: a.clock.Now(),
	}
	authorityToken, actErr := a.authority.PrepareActivation(reservation, values)
	if actErr != nil {
		a.runtime.AbortCompositeActivation(preparedActivation)
		sharedAbort()
		closeEvidence := start.Binding.CloseExact(ctx)
		if closeEvidence.Confirmed() {
			_ = a.processRegistry.ReleaseExact(registryKey.BindingID, registryKey.RunGeneration, start.Binding)
		}
		releaseLeases()
		execution.Abort(ctx)
		a.gate.AbortLaunch(ctx, launchPermit, actErr)
		abortReservation()
		execution.DisposeSecrets()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	// 12. Commit Authority, Control/H1, and shared ownership in one no-fail callback.
	receipt, commitErr := a.authority.CommitPreparedActivation(authorityToken, func() {
		preparedActivation.CommitNoFail()
		sharedCommit()
	})
	if commitErr != nil {
		a.runtime.AbortCompositeActivation(preparedActivation)
		sharedAbort()
		a.authority.AbortPreparedActivation(authorityToken)
		closeEvidence := start.Binding.CloseExact(ctx)
		if closeEvidence.Confirmed() {
			_ = a.processRegistry.ReleaseExact(registryKey.BindingID, registryKey.RunGeneration, start.Binding)
		}
		releaseLeases()
		execution.Abort(ctx)
		a.gate.AbortLaunch(ctx, launchPermit, commitErr)
		abortReservation()
		execution.DisposeSecrets()
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	// 13. Finish hidden projections and release transfer waiters.
	a.runtime.FinishCompositeActivation(preparedActivation)
	sharedFinish()

	// 14. Mark committed + dispose secrets.
	execution.MarkCommitted()
	execution.DisposeSecrets()

	// 14. Build detail response.
	controlSnapshot, _ := a.gate.SnapshotForDevice(sessionID, principal.DeviceID)
	earliest, latest := a.streams.SeqBounds(sessionID)
	return authorityDetail(receipt.Snapshot, controlSnapshot, earliest, latest), nil
}

// sharedKindFromPlan maps a launchplan.SharedServiceKind to the remote
// SharedServiceKind. Returns false for unknown kinds.
func sharedKindFromPlan(planKind launchplan.SharedServiceKind) (SharedServiceKind, bool) {
	switch planKind {
	case launchplan.SharedClaudeHeadroom:
		return SharedServiceClaudeHeadroom, true
	case launchplan.SharedCodexHeadroom:
		return SharedServiceCodexHeadroom, true
	default:
		return 0, false
	}
}

// launchEffectKindForSpec maps an EffectSpec to the gate's LaunchEffectKind.
func launchEffectKindForSpec(spec launchplan.EffectSpec) LaunchEffectKind {
	switch spec.Kind {
	case launchplan.EffectHeadroomStart:
		return LaunchHeadroomStart
	case launchplan.EffectConfigMutation:
		return LaunchConfigMutation
	case launchplan.EffectPTYStart:
		return LaunchPTYStart
	case launchplan.EffectExternalProcessStart:
		return LaunchProcessStart
	case launchplan.EffectBootstrapWrite:
		return LaunchBootstrapWrite
	default:
		return LaunchPTYStart
	}
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
	if a.authority != nil {
		return a.authorityLifecycle(ctx, reqID, principal, sessionID, op, jop)
	}
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
	// M-004: the restart boundary is committed via H1 CommitRestartSegment inside
	// restartRawEffect (and pumped to the v1 stream there). No manual
	// AppendBoundary / best-effort H3 seal for op == LifecycleRestart &&
	// result.RestartBoundary — the H1 seal already performed SealRunSegmentUnderState
	// atomically in the three-lock domain.
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

// authorityRestart implements M-03: same-ID restart with exact old binding
// close, OriginRestart plan, H1/H3 restart boundary, and composite run switch.
// Preserves startedAt/history/ledger. On failure, reconciles to honest
// stopped/unavailable and compensates the new run.
func (a *RemoteSessionAdapter) authorityRestart(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID) (SessionDetailResult, *AdapterError) {
	if a.planner == nil || a.executor == nil || a.processRegistry == nil || a.journal == nil ||
		a.runtime == nil || a.sharedCoord == nil || a.prepareSharedLeaseTransfer == nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	snapshot, err := a.authority.RemoteSnapshotByID(string(sessionID))
	if err != nil {
		return SessionDetailResult{}, newAdapterError(sessionNotFoundRestError(reqID))
	}
	processRef, err := a.authority.ProcessRef(snapshot.Handle)
	if err != nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	oldBinding, ok := a.processRegistry.ResolveExact(processRef.BindingID, processRef.RunRevision)
	if !ok {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	plan, failure := a.planner.BuildPlan(ctx, launchplan.BuildRequest{
		CLIType: contract.CLIType(snapshot.CLIType), Origin: launchplan.OriginRestart,
		Mode: launchplan.ModeEmbedded, Workdir: snapshot.Workdir,
	})
	if failure != nil {
		if plan != nil {
			plan.Secrets.Dispose()
		}
		return SessionDetailResult{}, newAdapterError(a.mapper.mapLaunchFailure(reqID, newLaunchResolveFailure(LaunchResolveFailureContext, contract.CLIType(snapshot.CLIType))))
	}
	defer plan.Secrets.Dispose()

	lifecycleToken, err := a.authority.PrepareLifecycle(snapshot.Handle, session.LifecycleRestart,
		session.LifecycleExpected{MembershipRevision: snapshot.Revisions.Membership, LifecycleRevision: snapshot.Revisions.Lifecycle, RunRevision: snapshot.Revisions.Run},
		processRef.BindingID)
	if err != nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	opID := GenerateOperationID()
	if opID == "" {
		a.authority.AbortPreparedLifecycle(lifecycleToken)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	journalPermit, err := a.journal.BeginIntent(ctx, SessionOperationIntent{
		OperationID: opID, SessionID: sessionID, CLIType: contract.CLIType(snapshot.CLIType),
		Operation: SessionOpRestart, Actor: SessionActorRemote,
	})
	if err != nil {
		a.authority.AbortPreparedLifecycle(lifecycleToken)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapJournalError(reqID))
	}

	var admissions []*SharedLaunchAdmission
	sharedAdmissions := make(map[launchplan.SharedServiceKind]any)
	releaseAdmissions := func() {
		for _, admission := range admissions {
			a.sharedCoord.ReleaseLaunchAdmission(admission)
		}
	}
	for _, spec := range plan.Admissions {
		kind, known := sharedKindFromPlan(spec.Service)
		if !known {
			releaseAdmissions()
			a.authority.AbortPreparedLifecycle(lifecycleToken)
			_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeFailed, contract.ErrorCodeServiceDown)
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
		admission, admissionErr := a.sharedCoord.AcquireLaunchAdmissionForConfig(kind, spec.ConfigFingerprint)
		if admissionErr != nil {
			releaseAdmissions()
			a.authority.AbortPreparedLifecycle(lifecycleToken)
			_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeFailed, contract.ErrorCodeServiceDown)
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
		admissions = append(admissions, admission)
		sharedAdmissions[spec.Service] = admission
	}

	var (
		restartPermit     *operationPermit
		sealReceipt       *RunSegmentSealReceipt
		newObsPermit      *RunObservationPermit
		execution         launchplan.PreparedExecution
		start             processcap.StartEvidence
		newRegistryKey    processcap.RegistryKey
		newRegistered     bool
		oldCloseAttempted bool
		oldCloseConfirmed bool
		newLeases         []*SharedDependencyLease
		controlRestart    *PreparedControlRestart
		restartActivation *PreparedCompositeRestart
		sharedCommit   = func() {}
		sharedFinish   func()
		sharedAbort    = func() {}
		sharedPrepared bool
	)

	abortTransaction := func(cause error) launchplan.CompensationReport {
		if sharedPrepared {
			sharedAbort()
			sharedPrepared = false
		}
		if restartActivation != nil {
			_ = a.runtime.AbortCompositeRestart(restartActivation)
			restartActivation = nil
		} else if restartPermit != nil && sealReceipt != nil {
			if restartPermit.restartStage != nil {
				_ = a.runtime.AbortRestartStage(restartPermit, sealReceipt, sessionID)
			} else {
				_ = a.runtime.RollbackRestartSealForPermit(restartPermit, sealReceipt, sessionID)
			}
		}
		if controlRestart != nil {
			a.runtime.AbortPreparedControlRestart(controlRestart)
			controlRestart = nil
		}
		compCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		report := launchplan.CompensationReport{}
		if execution != nil {
			report = execution.Abort(compCtx)
		}
		if newRegistered && start.Binding != nil {
			closeEvidence := start.Binding.CloseExact(compCtx)
			if closeEvidence.Confirmed() {
				_ = a.processRegistry.ReleaseExact(newRegistryKey.BindingID, newRegistryKey.RunGeneration, start.Binding)
			}
		}
		for _, lease := range newLeases {
			_ = a.sharedCoord.ReleaseExact(compCtx, lease)
		}
		releaseAdmissions()
		if execution != nil {
			execution.DisposeSecrets()
		}
		if oldCloseConfirmed && a.releaseSharedExact != nil {
			a.releaseSharedExact(string(sessionID), processRef.RunRevision)
		}
		a.authority.AbortPreparedLifecycle(lifecycleToken)
		if oldCloseAttempted {
			a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, true, a.clock.Now())
		}
		_ = cause
		return report
	}

	controlRestart, err = a.runtime.PrepareDeviceControlRestart(ctx, principal, sessionID, func(restartCtx context.Context, permit *operationPermit) (SessionMutationResult, error) {
		restartPermit = permit
		if checkpointErr := permit.Checkpoint(restartCtx, 1); checkpointErr != nil {
			return SessionMutationResult{}, checkpointErr
		}
		sealReceipt, err = a.runtime.SealRestartSegmentForPermit(permit, sessionID)
		if err != nil {
			return SessionMutationResult{}, err
		}
		oldCloseAttempted = true
		closeEvidence, closeOK := a.processRegistry.CloseExact(restartCtx, processRef.BindingID, processRef.RunRevision)
		if !closeOK || !closeEvidence.Confirmed() {
			return SessionMutationResult{}, errExactCloseIndeterminate
		}
		oldCloseConfirmed = true
		if err = a.processRegistry.ReleaseExact(processRef.BindingID, processRef.RunRevision, oldBinding); err != nil {
			return SessionMutationResult{}, err
		}
		newObsPermit, err = a.runtime.StageRestartRun(permit, sealReceipt, sessionID)
		if err != nil {
			return SessionMutationResult{}, err
		}
		execution, err = a.executor.Prepare(restartCtx, plan, launchplan.ExecutionBinding{
			SessionID: string(sessionID), RunEpoch: newObsPermit.RunEpoch(), RunHandle: newObsPermit,
			SharedAdmissions: sharedAdmissions,
		})
		if err != nil {
			return SessionMutationResult{}, err
		}
		for i := 0; i < execution.Count(); i++ {
			if err = permit.Checkpoint(restartCtx, uint32(i+2)); err != nil {
				return SessionMutationResult{}, err
			}
			effect := execution.Effect(i)
			effect.ArmOwnership()
			evidence, applyErr := effect.Apply(restartCtx)
			if applyErr != nil {
				return SessionMutationResult{}, applyErr
			}
			execution.RecordApplied(i, evidence)
			if err = permit.Checkpoint(restartCtx, uint32(i+3)); err != nil {
				return SessionMutationResult{}, err
			}
		}
		var hasProcess bool
		start, hasProcess = execution.ProcessEvidence()
		if !hasProcess {
			return SessionMutationResult{}, errors.New("restart execution produced no process evidence")
		}
		if err = start.Validate(processcap.BackendPTY); err != nil {
			return SessionMutationResult{}, err
		}
		newRegistryKey, err = a.processRegistry.Register(start.Binding, newObsPermit.RunEpoch())
		if err != nil {
			return SessionMutationResult{}, err
		}
		newRegistered = true
		for idx, spec := range plan.Admissions {
			kind, known := sharedKindFromPlan(spec.Service)
			if !known || idx >= len(admissions) {
				return SessionMutationResult{}, ErrSharedServiceInUse
			}
			lease, leaseErr := a.sharedCoord.AcquirePendingForObservationWithAdmission(
				restartCtx, newObsPermit, kind, spec.ConfigFingerprint, admissions[idx],
			)
			if leaseErr != nil {
				return SessionMutationResult{}, leaseErr
			}
			newLeases = append(newLeases, lease)
		}
		return SessionMutationResult{State: contract.SessionStateRunning, StateChanged: true, RestartBoundary: true}, nil
	})
	if err != nil {
		report := abortTransaction(err)
		outcome := SessionOutcomeFailed
		if oldCloseAttempted || report.Failed > 0 {
			outcome = SessionOutcomeIndeterminate
		}
		_ = a.journal.Complete(ctx, journalPermit, outcome, contract.ErrorCodeServiceDown)
		re, mapped := a.mapper.mapGateError(reqID, err)
		if !mapped {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}

	if err = a.authority.BindPreparedRestartResult(lifecycleToken, session.PreparedRestartValues{
		BindingID: newRegistryKey.BindingID, PID: start.PID, RunRevision: newObsPermit.RunEpoch(), Recipe: plan.Recipe,
	}); err != nil {
		report := abortTransaction(err)
		outcome := SessionOutcomeIndeterminate
		if report.Failed == 0 && !oldCloseAttempted {
			outcome = SessionOutcomeFailed
		}
		_ = a.journal.Complete(ctx, journalPermit, outcome, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	restartActivation, err = a.runtime.PrepareCompositeRestart(sessionID, restartPermit, sealReceipt)
	if err != nil {
		_ = abortTransaction(err)
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	sharedCommit, sharedFinish, sharedAbort, err = a.prepareSharedLeaseTransfer(
		string(sessionID), processRef.RunRevision, newObsPermit.RunEpoch(), newLeases,
	)
	if err != nil {
		_ = abortTransaction(err)
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	sharedPrepared = true
	if err = a.runtime.BindPreparedControlRestart(controlRestart, restartActivation); err != nil {
		_ = abortTransaction(err)
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	receipt, commitErr := a.authority.CommitPreparedRestart(lifecycleToken, func() {
		restartActivation.CommitNoFail()
		a.runtime.CommitPreparedControlRestartNoFail(controlRestart)
		sharedCommit()
	})
	if commitErr != nil {
		_ = abortTransaction(commitErr)
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	execution.MarkCommitted()
	execution.DisposeSecrets()
	a.runtime.FinishCompositeRestart(restartActivation)
	a.runtime.FinishPreparedControlRestart(controlRestart)
	sharedFinish()
	sharedPrepared = false
	releaseAdmissions()

	if restartActivation.PostCommitFenced() {
		closeEvidence := start.Binding.CloseExact(context.Background())
		if closeEvidence.Confirmed() {
			_ = a.processRegistry.ReleaseExact(newRegistryKey.BindingID, newRegistryKey.RunGeneration, start.Binding)
		}
		if a.releaseSharedExact != nil {
			a.releaseSharedExact(string(sessionID), newObsPermit.RunEpoch())
		}
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}

	_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeCommitted, "")
	controlSnapshot, _ := a.gate.SnapshotForDevice(sessionID, principal.DeviceID)
	earliest, latest := a.streams.SeqBounds(sessionID)
	return authorityDetail(receipt.Snapshot, controlSnapshot, earliest, latest), nil
}

func (a *RemoteSessionAdapter) authorityLifecycle(ctx context.Context, reqID contract.RequestID, principal DevicePrincipal, sessionID contract.SessionID, op LifecycleOperation, jop SessionOperationKind) (SessionDetailResult, *AdapterError) {
	if op == LifecycleRestart {
		return a.authorityRestart(ctx, reqID, principal, sessionID)
	}
	if a.processRegistry == nil || a.journal == nil || a.runtime == nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	snapshot, err := a.authority.RemoteSnapshotByID(string(sessionID))
	if err != nil {
		return SessionDetailResult{}, newAdapterError(sessionNotFoundRestError(reqID))
	}
	processRef, err := a.authority.ProcessRef(snapshot.Handle)
	if err != nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	binding, ok := a.processRegistry.ResolveExact(processRef.BindingID, processRef.RunRevision)
	if !ok {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	opID := GenerateOperationID()
	if opID == "" {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	journalPermit, err := a.journal.BeginIntent(ctx, SessionOperationIntent{
		OperationID: opID, SessionID: sessionID, CLIType: contract.CLIType(snapshot.CLIType),
		Operation: jop, Actor: SessionActorRemote,
	})
	if err != nil {
		return SessionDetailResult{}, newAdapterError(a.mapper.mapJournalError(reqID))
	}

	var removeToken *session.PreparedRemoveToken
	var lifecycleToken *session.PreparedLifecycleToken
	expectedRemove := session.RemoveExpected{MembershipRevision: snapshot.Revisions.Membership, LifecycleRevision: snapshot.Revisions.Lifecycle, RunRevision: snapshot.Revisions.Run}
	expectedLifecycle := session.LifecycleExpected{MembershipRevision: snapshot.Revisions.Membership, LifecycleRevision: snapshot.Revisions.Lifecycle, RunRevision: snapshot.Revisions.Run}
	if op == LifecycleRemove {
		removeToken, err = a.authority.PrepareRemove(snapshot.Handle, expectedRemove, processRef.BindingID)
	} else {
		lifecycleToken, err = a.authority.PrepareLifecycle(snapshot.Handle, session.LifecycleStop, expectedLifecycle, processRef.BindingID)
	}
	if err != nil {
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeFailed, contract.ErrorCodeControlBusy)
		if errors.Is(err, session.ErrAuthorityNotFound) {
			return SessionDetailResult{}, newAdapterError(sessionNotFoundRestError(reqID))
		}
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	abortManagerToken := func() {
		if removeToken != nil {
			a.authority.AbortPreparedRemove(removeToken)
		}
		if lifecycleToken != nil {
			a.authority.AbortPreparedLifecycle(lifecycleToken)
		}
	}

	var terminalTicket *PreparedTerminalStateReservation
	var terminalDeliveries []preparedRemovalDelivery
	var closeEvidence processcap.ExactCloseEvidence
	effect := func(effectCtx context.Context, permit *operationPermit) (SessionMutationResult, error) {
		if err := permit.Checkpoint(effectCtx, 1); err != nil {
			return SessionMutationResult{}, err
		}
		var closeOK bool
		closeEvidence, closeOK = a.processRegistry.CloseExact(effectCtx, processRef.BindingID, processRef.RunRevision)
		if !closeOK || !closeEvidence.Confirmed() {
			return SessionMutationResult{}, errExactCloseIndeterminate
		}
		if op == LifecycleRemove {
			return SessionMutationResult{Removed: true}, nil
		}
		return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
	}
	var preparedControlRemove *PreparedControlRemove
	var preparedControlStop *PreparedControlStop
	var lifecycleErr error
	if op == LifecycleRemove {
		preparedControlRemove, lifecycleErr = a.runtime.PrepareDeviceControlRemove(ctx, principal, sessionID, effect)
	} else {
		preparedControlStop, lifecycleErr = a.runtime.PrepareDeviceControlStop(ctx, principal, sessionID, effect)
	}
	if lifecycleErr != nil {
		abortManagerToken()
		a.runtime.Hub().AbortTerminalStateReservation(terminalTicket)
		outcome := SessionOutcomeFailed
		if closeEvidence.ReceiptID() != 0 {
			outcome = SessionOutcomeIndeterminate
			a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, true, a.clock.Now())
		}
		_ = a.journal.Complete(ctx, journalPermit, outcome, contract.ErrorCodeServiceDown)
		re, mapped := a.mapper.mapGateError(reqID, lifecycleErr)
		if !mapped {
			re = a.mapper.mapGenericError(reqID)
		}
		return SessionDetailResult{}, newAdapterError(re)
	}

	if op == LifecycleRemove {
		terminalTicket, err = a.runtime.Hub().PrepareTerminalStateReservation(sessionID)
		if err == nil {
			terminalDeliveries, err = a.runtime.Hub().PrepareRemovalDeliveries(sessionID, func(viewer contract.DeviceID) (contract.ControlSnapshot, error) {
				return a.gate.SnapshotForDevice(sessionID, viewer)
			}, a.clock.Now())
		}
		if err != nil {
			abortManagerToken()
			a.runtime.AbortPreparedControlRemove(preparedControlRemove)
			a.runtime.Hub().AbortTerminalStateReservation(terminalTicket)
			a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, true, a.clock.Now())
			_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
	}

	if op == LifecycleStop {
		lifecycleReceipt, commitErr := a.authority.CommitPreparedStop(lifecycleToken, closeEvidence, func() {
			a.runtime.CommitPreparedControlStopNoFail(preparedControlStop)
		})
		if commitErr != nil {
			a.authority.AbortPreparedLifecycle(lifecycleToken)
			a.runtime.AbortPreparedControlStop(preparedControlStop)
			a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, true, a.clock.Now())
			_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
		a.runtime.FinishPreparedControlStop(preparedControlStop)
		// Stop retains the already-closed exact capability so a later Remove or
		// Restart can still resolve the same generation. Shared dependencies are
		// nevertheless terminal for this run and release immediately.
		if a.releaseSharedExact != nil {
			a.releaseSharedExact(string(sessionID), processRef.RunRevision)
		}
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeCommitted, "")
		controlSnapshot, snapErr := a.gate.SnapshotForDevice(sessionID, principal.DeviceID)
		if snapErr != nil {
			return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
		}
		earliest, latest := a.streams.SeqBounds(sessionID)
		return authorityDetail(lifecycleReceipt.Snapshot, controlSnapshot, earliest, latest), nil
	}

	removedAt := a.clock.Now()
	removeReceipt, commitErr := a.authority.CommitPreparedRemove(removeToken, closeEvidence, removedAt, func() {
		a.runtime.Hub().CommitTerminalStateReservationNoFail(terminalTicket)
		a.runtime.CommitPreparedControlRemoveNoFail(preparedControlRemove)
	})
	if commitErr != nil {
		a.authority.AbortPreparedRemove(removeToken)
		a.runtime.AbortPreparedControlRemove(preparedControlRemove)
		a.runtime.Hub().AbortTerminalStateReservation(terminalTicket)
		a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, true, removedAt)
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	a.runtime.Hub().FinishTerminalStateReservation(terminalTicket)
	a.runtime.FinishPreparedControlRemove(preparedControlRemove)
	if err := a.processRegistry.ReleaseExact(processRef.BindingID, processRef.RunRevision, binding); err != nil {
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return SessionDetailResult{}, newAdapterError(a.mapper.mapGenericError(reqID))
	}
	if a.releaseSharedExact != nil {
		a.releaseSharedExact(string(sessionID), processRef.RunRevision)
	}
	admitRemovalDeliveries(terminalDeliveries)
	if a.removeGC != nil {
		a.removeGC.Activate(newRemoveGCEntry(removeReceipt, a.runtime, a.streams, a.destroyLedger, a.releaseShared, a.processRegistry, binding, processRef.RunRevision, a.authority))
	}
	commitEvidence := SessionOperationCommitEvidence{ReceiptID: removeReceipt.ReceiptID, MembershipRevision: removeReceipt.MembershipRevision, LifecycleRevision: removeReceipt.LifecycleRevision}
	journalPermit.BindCommitEvidence(commitEvidence)
	if err := a.journal.Complete(ctx, journalPermit, SessionOutcomeCommitted, ""); err != nil && a.journalRetries != nil {
		a.journalRetries.ActivateCommitted(a.journal, journalPermit, commitEvidence)
	}
	return SessionDetailResult{}, nil
}

// DesktopStopAuthoritative and DesktopRemoveAuthoritative use the same Manager
// preflight, concrete Binding and receipt-keyed cleanup as v1 lifecycle. They
// never fall back to a SessionID/PID close.
func (a *RemoteSessionAdapter) DesktopStopAuthoritative(ctx context.Context, sessionID contract.SessionID) error {
	return a.desktopAuthorityLifecycle(ctx, sessionID, LifecycleStop, SessionOpStop)
}

func (a *RemoteSessionAdapter) DesktopRemoveAuthoritative(ctx context.Context, sessionID contract.SessionID) error {
	return a.desktopAuthorityLifecycle(ctx, sessionID, LifecycleRemove, SessionOpRemove)
}

func (a *RemoteSessionAdapter) desktopAuthorityLifecycle(ctx context.Context, sessionID contract.SessionID, op LifecycleOperation, journalOp SessionOperationKind) error {
	if a.authority == nil || a.processRegistry == nil || a.runtime == nil || a.journal == nil {
		return ErrControlNotReady
	}
	snapshot, err := a.authority.LifecycleSnapshotByID(string(sessionID))
	if err != nil {
		return err
	}
	processRef, err := a.authority.ProcessRef(snapshot.Handle)
	if err != nil {
		return err
	}
	binding, ok := a.processRegistry.ResolveExact(processRef.BindingID, processRef.RunRevision)
	if !ok {
		return session.ErrAuthorityProcessUnavailable
	}
	opID := GenerateOperationID()
	if opID == "" {
		return errors.New("remote: operation identity unavailable")
	}
	journalPermit, err := a.journal.BeginIntent(ctx, SessionOperationIntent{
		OperationID: opID, SessionID: sessionID, CLIType: contract.CLIType(snapshot.CLIType),
		Operation: journalOp, Actor: SessionActorDesktop,
	})
	if err != nil {
		return err
	}

	var removeToken *session.PreparedRemoveToken
	var lifecycleToken *session.PreparedLifecycleToken
	if op == LifecycleRemove {
		removeToken, err = a.authority.PrepareRemove(snapshot.Handle, session.RemoveExpected{
			MembershipRevision: snapshot.Revisions.Membership, LifecycleRevision: snapshot.Revisions.Lifecycle, RunRevision: snapshot.Revisions.Run,
		}, processRef.BindingID)
	} else {
		lifecycleToken, err = a.authority.PrepareLifecycle(snapshot.Handle, session.LifecycleStop, session.LifecycleExpected{
			MembershipRevision: snapshot.Revisions.Membership, LifecycleRevision: snapshot.Revisions.Lifecycle, RunRevision: snapshot.Revisions.Run,
		}, processRef.BindingID)
	}
	if err != nil {
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeFailed, contract.ErrorCodeControlBusy)
		return err
	}
	abort := func() {
		if removeToken != nil {
			a.authority.AbortPreparedRemove(removeToken)
		}
		if lifecycleToken != nil {
			a.authority.AbortPreparedLifecycle(lifecycleToken)
		}
	}
	controlManaged := snapshot.Mode == launchplan.ModeEmbedded
	var ticket *PreparedTerminalStateReservation
	var deliveries []preparedRemovalDelivery
	var closeEvidence processcap.ExactCloseEvidence
	var lifecycleErr error
	var preparedControlRemove *PreparedControlRemove
	var preparedControlStop *PreparedControlStop
	if controlManaged {
		effect := func(effectCtx context.Context, permit *operationPermit) (SessionMutationResult, error) {
			if err := permit.Checkpoint(effectCtx, 1); err != nil {
				return SessionMutationResult{}, err
			}
			var closeOK bool
			closeEvidence, closeOK = a.processRegistry.CloseExact(effectCtx, processRef.BindingID, processRef.RunRevision)
			if !closeOK || !closeEvidence.Confirmed() {
				return SessionMutationResult{}, errExactCloseIndeterminate
			}
			if op == LifecycleRemove {
				return SessionMutationResult{Removed: true}, nil
			}
			return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
		}
		if op == LifecycleRemove {
			preparedControlRemove, lifecycleErr = a.runtime.PrepareDesktopControlRemove(ctx, sessionID, effect)
		} else {
			preparedControlStop, lifecycleErr = a.runtime.PrepareDesktopControlStop(ctx, sessionID, effect)
		}
	} else {
		var closeOK bool
		closeEvidence, closeOK = a.processRegistry.CloseExact(ctx, processRef.BindingID, processRef.RunRevision)
		if !closeOK || !closeEvidence.Confirmed() {
			lifecycleErr = errExactCloseIndeterminate
		}
	}
	if lifecycleErr != nil {
		abort()
		a.runtime.Hub().AbortTerminalStateReservation(ticket)
		outcome := SessionOutcomeFailed
		if closeEvidence.ReceiptID() != 0 {
			outcome = SessionOutcomeIndeterminate
			a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, controlManaged, a.clock.Now())
		}
		_ = a.journal.Complete(ctx, journalPermit, outcome, contract.ErrorCodeServiceDown)
		return lifecycleErr
	}
	if op == LifecycleRemove && controlManaged {
		ticket, err = a.runtime.Hub().PrepareTerminalStateReservation(sessionID)
		if err == nil {
			deliveries, err = a.runtime.Hub().PrepareRemovalDeliveries(sessionID, func(viewer contract.DeviceID) (contract.ControlSnapshot, error) {
				return a.gate.SnapshotForDevice(sessionID, viewer)
			}, a.clock.Now())
		}
		if err != nil {
			abort()
			a.runtime.AbortPreparedControlRemove(preparedControlRemove)
			a.runtime.Hub().AbortTerminalStateReservation(ticket)
			a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, true, a.clock.Now())
			_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
			return err
		}
	}
	if op == LifecycleStop {
		if _, err := a.authority.CommitPreparedStop(lifecycleToken, closeEvidence, func() {
			if controlManaged {
				a.runtime.CommitPreparedControlStopNoFail(preparedControlStop)
			}
		}); err != nil {
			a.authority.AbortPreparedLifecycle(lifecycleToken)
			a.runtime.AbortPreparedControlStop(preparedControlStop)
			a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, controlManaged, a.clock.Now())
			_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
			return err
		}
		a.runtime.FinishPreparedControlStop(preparedControlStop)
		if a.releaseSharedExact != nil {
			a.releaseSharedExact(string(sessionID), processRef.RunRevision)
		}
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeCommitted, "")
		return nil
	}
	removedAt := a.clock.Now()
	receipt, err := a.authority.CommitPreparedRemove(removeToken, closeEvidence, removedAt, func() {
		if controlManaged {
			a.runtime.Hub().CommitTerminalStateReservationNoFail(ticket)
			a.runtime.CommitPreparedControlRemoveNoFail(preparedControlRemove)
		}
	})
	if err != nil {
		a.authority.AbortPreparedRemove(removeToken)
		a.runtime.AbortPreparedControlRemove(preparedControlRemove)
		a.runtime.Hub().AbortTerminalStateReservation(ticket)
		a.reconcileClosedRunWithoutCommit(sessionID, processRef.RunRevision, controlManaged, removedAt)
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return err
	}
	if controlManaged {
		a.runtime.Hub().FinishTerminalStateReservation(ticket)
		a.runtime.FinishPreparedControlRemove(preparedControlRemove)
	}
	if err := a.processRegistry.ReleaseExact(processRef.BindingID, processRef.RunRevision, binding); err != nil {
		_ = a.journal.Complete(ctx, journalPermit, SessionOutcomeIndeterminate, contract.ErrorCodeServiceDown)
		return err
	}
	if a.releaseSharedExact != nil {
		a.releaseSharedExact(string(sessionID), processRef.RunRevision)
	}
	admitRemovalDeliveries(deliveries)
	if a.removeGC != nil {
		a.removeGC.Activate(newRemoveGCEntry(receipt, a.runtime, a.streams, a.destroyLedger, a.releaseShared, a.processRegistry, binding, processRef.RunRevision, a.authority))
	}
	commitEvidence := SessionOperationCommitEvidence{ReceiptID: receipt.ReceiptID, MembershipRevision: receipt.MembershipRevision, LifecycleRevision: receipt.LifecycleRevision}
	journalPermit.BindCommitEvidence(commitEvidence)
	if err := a.journal.Complete(ctx, journalPermit, SessionOutcomeCommitted, ""); err != nil && a.journalRetries != nil {
		a.journalRetries.ActivateCommitted(a.journal, journalPermit, commitEvidence)
	}
	return nil
}

func (a *RemoteSessionAdapter) reconcileClosedRunWithoutCommit(sessionID contract.SessionID, runRevision uint64, controlManaged bool, at time.Time) {
	if controlManaged && a.runtime != nil {
		a.runtime.ReconcileRestartFailure(sessionID, runRevision)
	}
	if a.authority != nil {
		a.authority.CommitExactRunUnavailable(string(sessionID), runRevision, at)
	}
}

var errExactCloseIndeterminate = errors.New("remote: exact close indeterminate")

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
	if a.authority != nil {
		if _, resolveErr := a.authority.RemoteSnapshotByID(string(sessionID)); resolveErr != nil {
			return ControlResult{}, newAdapterError(sessionNotFoundRestError(reqID))
		}
	} else if _, ok := a.catalog.Entry(sessionID); !ok {
		return ControlResult{}, newAdapterError(sessionNotFoundRestError(reqID))
	}
	if lease == nil || !lease.IsLive() {
		return ControlResult{}, newAdapterError(restError{status: 403, body: newAPIError(reqID, contract.ErrorCodeControlForbidden, contract.ErrorLayerControl, "active session connection required", contract.ActionHintRequestControl)})
	}
	if a.authority != nil {
		resolved, resolveErr := a.authority.ResolveRemoteHandle(string(sessionID))
		if resolveErr != nil {
			return ControlResult{}, newAdapterError(sessionNotFoundRestError(reqID))
		}
		var snap contract.ControlSnapshot
		var gateErr error
		if err := a.authority.CommitResolvedAttach(resolved, func() { snap, gateErr = a.gate.Acquire(ctx, principal, lease, sessionID) }); err != nil {
			return ControlResult{}, newAdapterError(sessionNotFoundRestError(reqID))
		}
		if gateErr != nil {
			re, ok := a.mapper.mapGateError(reqID, gateErr)
			if !ok {
				re = a.mapper.mapGenericError(reqID)
			}
			return ControlResult{}, newAdapterError(re)
		}
		return ControlResult{Snapshot: snap}, nil
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
	if a.authority != nil {
		resolved, resolveErr := a.authority.ResolveRemoteHandle(string(sessionID))
		if resolveErr != nil {
			return ControlResult{}, newAdapterError(sessionNotFoundRestError(reqID))
		}
		var snap contract.ControlSnapshot
		var gateErr error
		if err := a.authority.CommitResolvedAttach(resolved, func() { snap, gateErr = a.gate.Release(ctx, principal, sessionID) }); err != nil {
			return ControlResult{}, newAdapterError(sessionNotFoundRestError(reqID))
		}
		if gateErr != nil {
			re, ok := a.mapper.mapGateError(reqID, gateErr)
			if !ok {
				re = a.mapper.mapGenericError(reqID)
			}
			return ControlResult{}, newAdapterError(re)
		}
		return ControlResult{Snapshot: snap}, nil
	}
	if _, ok := a.catalog.Entry(sessionID); !ok {
		return ControlResult{}, newAdapterError(sessionNotFoundRestError(reqID))
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

type ResolvedRemoteMembershipHandle struct {
	authority session.ResolvedRemoteHandle
}

func (a *RemoteSessionAdapter) ResolveRemoteHandle(sessionID contract.SessionID) (ResolvedRemoteMembershipHandle, *AdapterError) {
	if a.authority == nil {
		return ResolvedRemoteMembershipHandle{}, nil
	}
	resolved, err := a.authority.ResolveRemoteHandle(string(sessionID))
	if err != nil {
		return ResolvedRemoteMembershipHandle{}, newAdapterError(sessionNotFoundRestError(""))
	}
	return ResolvedRemoteMembershipHandle{authority: resolved}, nil
}

func (a *RemoteSessionAdapter) CommitResolvedAttach(handle ResolvedRemoteMembershipHandle, noFail func()) error {
	if a.authority == nil {
		if noFail != nil {
			noFail()
		}
		return nil
	}
	return a.authority.CommitResolvedAttach(handle.authority, noFail)
}

func (a *RemoteSessionAdapter) TouchActivity(sessionID contract.SessionID, at time.Time) {
	if a.authority != nil {
		snapshot, err := a.authority.RemoteSnapshotByID(string(sessionID))
		if err == nil {
			a.authority.TouchActivity(string(sessionID), snapshot.Revisions.Run, at)
		}
		return
	}
	if a.catalog != nil {
		a.catalog.TouchActivity(sessionID, at)
	}
}

func sessionNotFoundRestError(reqID contract.RequestID) restError {
	return restError{status: 404, body: newAPIError(reqID, contract.ErrorCodeSessionNotFound, contract.ErrorLayerSession, "session not found", contract.ActionHintRetry)}
}

func authorityStateToWire(state session.AuthorityLifecycleState) contract.SessionState {
	switch state {
	case session.AuthorityRunning:
		return contract.SessionStateRunning
	case session.AuthorityStopping:
		return contract.SessionStateUnavailable
	case session.AuthorityStopped:
		return contract.SessionStateStopped
	case session.AuthorityExited:
		return contract.SessionStateExited
	default:
		return contract.SessionStateUnavailable
	}
}

func authorityDetail(snapshot session.AuthoritySnapshot, control contract.ControlSnapshot, earliest, latest contract.Seq) SessionDetailResult {
	return SessionDetailResult{Detail: contract.SessionDetail{
		SessionSummary: contract.SessionSummary{
			ID: contract.SessionID(snapshot.Handle.SessionID()), Title: snapshot.SafeTitle,
			CLIType: contract.CLIType(snapshot.CLIType), State: authorityStateToWire(snapshot.State),
			Control: control, LastActivityAt: formatUTC(snapshot.LastActivityAt),
		},
		Workdir: snapshot.Workdir, StartedAt: formatUTC(snapshot.StartedAt), EarliestSeq: earliest, LatestSeq: latest,
	}}
}

// sessionState returns the wire session state for a session. Authority is the
// production source; the catalog fallback exists only for legacy fixtures.
func (a *RemoteSessionAdapter) sessionState(sessionID contract.SessionID) contract.SessionState {
	if a.authority != nil {
		if snapshot, err := a.authority.RemoteSnapshotByID(string(sessionID)); err == nil {
			return authorityStateToWire(snapshot.State)
		}
		return contract.SessionStateUnavailable
	}
	if a.runtime != nil {
		mirror, _, ok := a.runtime.Arbiter().SessionStateMirror(sessionID)
		if ok && mirror != "" {
			return mirror
		}
	}
	// Fallback: if in the compatibility catalog, assume running.
	if a.catalog != nil && a.catalog.IsPublic(sessionID) {
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
