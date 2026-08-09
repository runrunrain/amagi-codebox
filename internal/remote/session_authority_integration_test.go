package remote

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

type authorityBinding struct {
	id            processcap.BindingID
	closeCalls    atomic.Int32
	indeterminate bool
	waiter        *authorityCloseWaiter
}

type authorityCloseWaiter struct{ confirmed atomic.Bool }

func (w *authorityCloseWaiter) Wait(ctx context.Context) error {
	for !w.confirmed.Load() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	return nil
}
func (w *authorityCloseWaiter) Confirmed() bool { return w.confirmed.Load() }

func (b *authorityBinding) BindingID() processcap.BindingID { return b.id }
func (b *authorityBinding) CloseExact(context.Context) processcap.ExactCloseEvidence {
	b.closeCalls.Add(1)
	disposition := processcap.CloseConfirmed
	var waiter processcap.CloseWaiter
	if b.indeterminate {
		disposition = processcap.CloseIndeterminate
		if b.waiter == nil {
			b.waiter = &authorityCloseWaiter{}
		}
		waiter = b.waiter
	}
	evidence, err := processcap.NewExactCloseEvidence(b.id, 501, disposition, waiter)
	if err != nil {
		panic(err)
	}
	return evidence
}

type removalCapture struct {
	mu       sync.Mutex
	payloads [][]byte
	code     int
}

func (c *removalCapture) DeliverControlState(contract.ControlStateEvent) {}
func (c *removalCapture) ConsumerAlive() bool                            { return true }
func (c *removalCapture) AdmitRemovalTerminal(item *PreparedRemovalTerminal) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, payload := range item.PayloadsForWriter() {
		c.payloads = append(c.payloads, append([]byte(nil), payload...))
	}
	c.code = item.closeCode
	return true
}

type authoritativeFixture struct {
	manager     *session.Manager
	registry    *processcap.Registry
	runtime     *ControlRuntime
	adapter     *RemoteSessionAdapter
	binding     *authorityBinding
	observation *RunObservationPermit
	handle      session.AuthorityHandle
	sid         contract.SessionID
}

func newAuthoritativeFixture(t *testing.T, journal SessionOperationJournal, indeterminate bool) *authoritativeFixture {
	t.Helper()
	runtime := NewControlRuntime(newCtrlFakeClock(time.Unix(100, 0)), nil)
	runtime.MarkReady()
	manager := session.NewManager()
	registry := processcap.NewRegistry()
	sid := contract.SessionID("desktop-real-id")
	launchPermit, runPermit, observation, err := runtime.BeginDesktopRun(context.Background(), sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	_ = launchPermit
	if err := runtime.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	runtime.Projector().TrackRun(sid, observation)
	reservation, err := manager.ReserveCreate(session.CreateSpec{
		RequestedID: string(sid), AppType: session.AppTypeClaudeCode,
		Origin: launchplan.OriginDesktop, Mode: launchplan.ModeEmbedded,
		Workdir: "/work", RemoteEligible: true, Provider: "private", Model: "private-model",
	})
	if err != nil {
		t.Fatalf("ReserveCreate: %v", err)
	}
	binding := &authorityBinding{id: processcap.BindingID{Kind: processcap.BackendPTY, Owner: 71, Generation: 9}, indeterminate: indeterminate}
	if _, err := registry.Register(binding, observation.RunEpoch()); err != nil {
		t.Fatalf("registry Register: %v", err)
	}
	token, err := manager.PrepareActivation(reservation, session.PreparedAuthorityActivation{
		Recipe:    launchplan.StableRecipe{CLIType: contract.CLITypeClaudeCode, Workdir: "/work", ProviderRef: "private"},
		BindingID: binding.id, PID: 321, RunRevision: observation.RunEpoch(),
		StartedAt: time.Unix(100, 0), LastActivityAt: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatalf("PrepareActivation: %v", err)
	}
	receipt, err := manager.CommitPreparedActivation(token, func() {})
	if err != nil {
		t.Fatalf("CommitPreparedActivation: %v", err)
	}
	if journal == nil {
		journal = NewNoopSessionOperationJournal(true)
	}
	streams := NewSessionStreamStore()
	adapter := NewRemoteSessionAdapter(runtime.Gate(), runtime, NewSessionCatalog(), streams, journal, NewNoopRemoteLaunchResolver(), &countingLaunchRaw{}, noopSessionRawPort{}, newCtrlFakeClock(time.Unix(100, 0)), "")
	adapter.SetSessionAuthority(manager, registry, launchplan.NewFailClosedPlanner())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		adapter.FlushPostCommitDebt(ctx)
		cancel()
	})
	return &authoritativeFixture{manager: manager, registry: registry, runtime: runtime, adapter: adapter, binding: binding, observation: observation, handle: receipt.Authority, sid: sid}
}

type countingLaunchRaw struct{ calls atomic.Int32 }

func (r *countingLaunchRaw) StartProcess(context.Context, contract.SessionID, RemoteLaunchRecipe, any, *RunObservationPermit) error {
	r.calls.Add(1)
	return nil
}

func TestAuthorityDesktopAndV1ShareSameIDAndExternalIsHidden(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	if fixture.adapter.Catalog() != nil {
		t.Fatal("production adapter retained a second membership catalog")
	}
	list, aerr := fixture.adapter.ListSessions(context.Background(), "viewer")
	if aerr != nil || len(list.List) != 1 {
		t.Fatalf("ListSessions err=%v list=%#v", aerr, list.List)
	}
	if list.List[0].ID != fixture.sid || fixture.manager.List()[0].ID != string(fixture.sid) {
		t.Fatalf("desktop/v1 ID mismatch: desktop=%s v1=%s", fixture.manager.List()[0].ID, list.List[0].ID)
	}
	external := fixture.manager.Create(session.AppTypeCodex, "private-provider", "", "private-model", session.ModeTerminal, "/work", false)
	if external == nil {
		t.Fatal("external create failed")
	}
	list, aerr = fixture.adapter.ListSessions(context.Background(), "viewer")
	if aerr != nil || len(list.List) != 1 {
		t.Fatalf("external leaked to v1: err=%v list=%#v", aerr, list.List)
	}
	if _, aerr := fixture.adapter.SessionDetail(context.Background(), "req", contract.SessionID(external.ID), "viewer"); aerr == nil || aerr.Rest.status != 404 {
		t.Fatalf("external detail = %#v", aerr)
	}
}

func TestAuthorityRestartFailsClosedWithoutReplacingBinding(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	_, aerr := fixture.adapter.RestartSession(context.Background(), "req", DevicePrincipal{DeviceID: "dev"}, fixture.sid)
	if aerr == nil || aerr.Rest.status != 503 {
		t.Fatalf("restart error = %#v", aerr)
	}
	if fixture.binding.closeCalls.Load() != 0 || fixture.registry.Count() != 1 {
		t.Fatalf("restart mutated binding: closes=%d registry=%d", fixture.binding.closeCalls.Load(), fixture.registry.Count())
	}
}

func TestAuthorityRunOutputAndExitUpdateExactActivityAndLifecycle(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	before, err := fixture.manager.RemoteSnapshotByID(string(fixture.sid))
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.Projector().OfferOutput(fixture.observation, 1, []byte("output"))
	afterOutput, err := fixture.manager.RemoteSnapshotByID(string(fixture.sid))
	if err != nil {
		t.Fatal(err)
	}
	if !afterOutput.LastActivityAt.After(before.LastActivityAt) || afterOutput.Revisions.Activity <= before.Revisions.Activity {
		t.Fatalf("output activity before=%#v after=%#v", before, afterOutput)
	}
	stale := &RunObservationPermit{
		entry: fixture.observation.entry, run: fixture.observation.run,
		runEpoch: fixture.observation.runEpoch + 1, backendEpoch: fixture.observation.backendEpoch,
	}
	fixture.runtime.Projector().OfferOutput(stale, 2, []byte("stale"))
	afterStale, err := fixture.manager.RemoteSnapshotByID(string(fixture.sid))
	if err != nil {
		t.Fatal(err)
	}
	if afterStale.Revisions != afterOutput.Revisions {
		t.Fatalf("stale output changed revisions: before=%#v after=%#v", afterOutput.Revisions, afterStale.Revisions)
	}
	fixture.runtime.Projector().OfferExit(fixture.observation, 0, false)
	afterExit, err := fixture.manager.RemoteSnapshotByID(string(fixture.sid))
	if err != nil {
		t.Fatal(err)
	}
	if afterExit.State != session.AuthorityExited || afterExit.Revisions.Lifecycle <= afterOutput.Revisions.Lifecycle || afterExit.Revisions.Activity <= afterOutput.Revisions.Activity {
		t.Fatalf("exit snapshot = %#v", afterExit)
	}
}

func TestAuthorityCreateFailsClosedBeforeAnyRawStart(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	raw := &countingLaunchRaw{}
	fixture.adapter.launchRaw = raw
	_, aerr := fixture.adapter.CreateSession(context.Background(), "req", DevicePrincipal{DeviceID: "dev"}, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if aerr == nil || aerr.Rest.status != 422 {
		t.Fatalf("create error = %#v", aerr)
	}
	if raw.calls.Load() != 0 {
		t.Fatalf("raw starts = %d", raw.calls.Load())
	}
}

func TestDesktopExternalUsesAuthorityExactCapabilityAndRemainsRemoteHidden(t *testing.T) {
	runtime := NewControlRuntime(newCtrlFakeClock(time.Unix(100, 0)), nil)
	runtime.MarkReady()
	manager := session.NewManager()
	registry := processcap.NewRegistry()
	reservation, err := manager.ReserveCreate(session.CreateSpec{
		RequestedID: "external-real", AppType: session.AppTypePi,
		Origin: launchplan.OriginDesktop, Mode: launchplan.ModeExternal, Workdir: "/work", RemoteEligible: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := &authorityBinding{id: processcap.BindingID{Kind: processcap.BackendExternalLauncher, Owner: 81, Generation: 4}}
	if _, err := registry.Register(binding, 1); err != nil {
		t.Fatal(err)
	}
	token, err := manager.PrepareActivation(reservation, session.PreparedAuthorityActivation{
		BindingID: binding.id, PID: 701, RunRevision: 1, StartedAt: time.Unix(100, 0), LastActivityAt: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitPreparedExternalActivation(token); err != nil {
		t.Fatal(err)
	}
	adapter := NewRemoteSessionAdapter(runtime.Gate(), runtime, NewSessionCatalog(), NewSessionStreamStore(), NewNoopSessionOperationJournal(true), NewNoopRemoteLaunchResolver(), &countingLaunchRaw{}, noopSessionRawPort{}, newCtrlFakeClock(time.Unix(100, 0)), "")
	adapter.SetSessionAuthority(manager, registry, launchplan.NewFailClosedPlanner())
	if got := manager.ListRemoteSafeSnapshots(); len(got) != 0 {
		t.Fatalf("external leaked remotely: %#v", got)
	}
	if err := adapter.DesktopStopAuthoritative(context.Background(), "external-real"); err != nil {
		t.Fatalf("DesktopStopAuthoritative: %v", err)
	}
	if registry.Count() != 1 || manager.GetStatus("external-real") != session.StatusStopped {
		t.Fatalf("after stop registry=%d status=%s", registry.Count(), manager.GetStatus("external-real"))
	}
	if err := adapter.DesktopRemoveAuthoritative(context.Background(), "external-real"); err != nil {
		t.Fatalf("DesktopRemoveAuthoritative: %v", err)
	}
	if registry.Count() != 0 {
		t.Fatalf("binding survived remove: %d", registry.Count())
	}
	if _, err := manager.Get("external-real"); err == nil {
		t.Fatal("external membership survived remove")
	}
}

func TestAuthorityRemovePreflightNotFoundClosesZero(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	aerr := fixture.adapter.RemoveSession(context.Background(), "req", DevicePrincipal{DeviceID: "dev"}, "missing")
	if aerr == nil || aerr.Rest.status != 404 {
		t.Fatalf("remove missing = %#v", aerr)
	}
	if fixture.binding.closeCalls.Load() != 0 {
		t.Fatalf("close calls = %d", fixture.binding.closeCalls.Load())
	}
}

func TestPreparedAttachCapturesCausalEventBeforeFinalCommit(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	resolved, err := fixture.manager.ResolveRemoteHandle(string(fixture.sid))
	if err != nil {
		t.Fatal(err)
	}
	consumer := &removalCapture{}
	watermark := fixture.runtime.Hub().WatermarkFor(fixture.sid)
	prepared, gateErr := fixture.runtime.PrepareRemoteAttach(DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}, "conn", fixture.sid, consumer, watermark, noopFencer{})
	if gateErr != nil {
		t.Fatal(gateErr)
	}
	defer fixture.runtime.AbortPreparedRemoteAttach(prepared)
	ticket, err := fixture.runtime.Hub().ReserveRunRecordUnderState(fixture.sid, RunCausalPosition{SegmentID: 1, Source: 2}, CausalReplay)
	if err != nil {
		t.Fatal(err)
	}
	outcome := fixture.runtime.Hub().PublishReserved(ticket, contract.OutputEvent{Type: contract.ServerEventTypeOutput, SessionID: fixture.sid, Seq: 1, Chunk: "eA=="})
	if outcome.Disposition != CausalPublished {
		t.Fatalf("publish = %#v", outcome)
	}
	var commitGateErr *ControlGateError
	if err := fixture.manager.CommitResolvedAttach(resolved, func() {
		_, _, commitGateErr = fixture.runtime.CommitPreparedRemoteAttachNoAlloc(prepared)
	}); err != nil || commitGateErr != nil {
		t.Fatalf("commit err=%v gate=%v", err, commitGateErr)
	}
	fixture.runtime.FinishPreparedRemoteAttach(prepared)
	queued := prepared.CausalSubscription().Drain()
	if len(queued) != 1 || queued[0].ordinal != ticket.Ordinal() {
		t.Fatalf("hidden causal queue = %#v", queued)
	}
	fixture.runtime.DetachControl(prepared.Handle(), false)
	prepared.CausalSubscription().BeginTerminal()
	fixture.runtime.Hub().UnregisterCausalSubscription(prepared.CausalSubscription())
}

func TestPreparedAttachSupersedesConnectedHolderWithExactNewLease(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	principal := DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}
	commitAttach := func(connectionID ConnectionID) *PreparedRemoteAttach {
		t.Helper()
		resolved, err := fixture.manager.ResolveRemoteHandle(string(fixture.sid))
		if err != nil {
			t.Fatal(err)
		}
		prepared, gateErr := fixture.runtime.PrepareRemoteAttach(principal, connectionID, fixture.sid, &removalCapture{}, fixture.runtime.Hub().WatermarkFor(fixture.sid), noopFencer{})
		if gateErr != nil {
			t.Fatal(gateErr)
		}
		var commitGateErr *ControlGateError
		if err := fixture.manager.CommitResolvedAttach(resolved, func() {
			_, _, commitGateErr = fixture.runtime.CommitPreparedRemoteAttachNoAlloc(prepared)
		}); err != nil || commitGateErr != nil {
			fixture.runtime.AbortPreparedRemoteAttach(prepared)
			t.Fatalf("commit attach: manager=%v gate=%v", err, commitGateErr)
		}
		fixture.runtime.FinishPreparedRemoteAttach(prepared)
		return prepared
	}

	first := commitAttach("conn-1")
	if _, err := fixture.runtime.Gate().Acquire(context.Background(), principal, first.Handle().Lease(), fixture.sid); err != nil {
		t.Fatal(err)
	}
	second := commitAttach("conn-2")
	if first.Handle().Lease().IsLive() {
		t.Fatal("superseded lease remained live")
	}
	oldCalls := 0
	if err := fixture.runtime.Gate().DoDevicePTY(context.Background(), first.Handle().Lease(), fixture.sid, PTYInput, func(context.Context, *operationPermit) error {
		oldCalls++
		return nil
	}); err == nil {
		t.Fatal("superseded lease retained PTY authority")
	}
	newCalls := 0
	if err := fixture.runtime.Gate().DoDevicePTY(context.Background(), second.Handle().Lease(), fixture.sid, PTYInput, func(context.Context, *operationPermit) error {
		newCalls++
		return nil
	}); err != nil {
		t.Fatalf("replacement lease lacks PTY authority: %v", err)
	}
	if oldCalls != 0 || newCalls != 1 {
		t.Fatalf("raw calls old=%d new=%d", oldCalls, newCalls)
	}

	for _, prepared := range []*PreparedRemoteAttach{first, second} {
		fixture.runtime.DetachControl(prepared.Handle(), false)
		prepared.CausalSubscription().BeginTerminal()
		fixture.runtime.Hub().UnregisterCausalSubscription(prepared.CausalSubscription())
	}
}

func TestPreparedTerminalReservationIsInvisibleUntilRemoveCommit(t *testing.T) {
	hub := NewSessionEventHub()
	hub.MarkReady()
	sessionID := contract.SessionID("terminal-cut")
	before := hub.WatermarkFor(sessionID)
	prepared, err := hub.PrepareTerminalStateReservation(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if afterPrepare := hub.WatermarkFor(sessionID); afterPrepare != before {
		t.Fatalf("prepared terminal advanced visible cut: before=%#v after=%#v", before, afterPrepare)
	}
	hub.CommitTerminalStateReservationNoFail(prepared)
	hub.FinishTerminalStateReservation(prepared)
	if afterCommit := hub.WatermarkFor(sessionID); afterCommit.Event <= before.Event {
		t.Fatalf("committed terminal did not advance cut: before=%#v after=%#v", before, afterCommit)
	}
}

func TestPreparedStopServerFenceWaitsForCompositeResolution(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	principal := DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}
	attach, _, _, gateErr := fixture.runtime.AttachControl(principal, "conn", fixture.sid, &removalCapture{})
	if gateErr != nil {
		t.Fatal(gateErr)
	}
	if _, err := fixture.runtime.Gate().Acquire(context.Background(), principal, attach.Lease(), fixture.sid); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.manager.RemoteSnapshotByID(string(fixture.sid))
	if err != nil {
		t.Fatal(err)
	}
	processRef, err := fixture.manager.ProcessRef(snapshot.Handle)
	if err != nil {
		t.Fatal(err)
	}
	managerToken, err := fixture.manager.PrepareLifecycle(snapshot.Handle, session.LifecycleStop, session.LifecycleExpected{
		MembershipRevision: snapshot.Revisions.Membership, LifecycleRevision: snapshot.Revisions.Lifecycle, RunRevision: snapshot.Revisions.Run,
	}, processRef.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	closeEvidence := fixture.binding.CloseExact(context.Background())
	controlToken, err := fixture.runtime.PrepareDeviceControlStop(context.Background(), principal, fixture.sid, func(context.Context, *operationPermit) (SessionMutationResult, error) {
		return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fenceDone := make(chan struct{})
	go func() {
		fixture.runtime.FenceAllRemote()
		close(fenceDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		controlToken.entry.stateMu.Lock()
		claimed := controlToken.postCommitFence
		controlToken.entry.stateMu.Unlock()
		if claimed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server fence did not claim prepared stop")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-fenceDone:
		t.Fatal("server fence returned before stop resolution")
	default:
	}
	if _, err := fixture.manager.CommitPreparedStop(managerToken, closeEvidence, func() {
		fixture.runtime.CommitPreparedControlStopNoFail(controlToken)
	}); err != nil {
		t.Fatal(err)
	}
	fixture.runtime.FinishPreparedControlStop(controlToken)
	select {
	case <-fenceDone:
	case <-time.After(time.Second):
		t.Fatal("server fence did not finish after stop resolution")
	}
}

func TestAuthorityStopCommitsControlAndManagerAndRetainsClosedBinding(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	principal := DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}
	terminal := &removalCapture{}
	attach, _, _, gateErr := fixture.runtime.AttachControl(principal, "conn", fixture.sid, terminal)
	if gateErr != nil {
		t.Fatal(gateErr)
	}
	if _, err := fixture.runtime.Gate().Acquire(context.Background(), principal, attach.Lease(), fixture.sid); err != nil {
		t.Fatal(err)
	}
	result, aerr := fixture.adapter.StopSession(context.Background(), "req", principal, fixture.sid)
	if aerr != nil {
		t.Fatalf("StopSession: %v", aerr)
	}
	if result.Detail.State != contract.SessionStateStopped || fixture.manager.GetStatus(string(fixture.sid)) != session.StatusStopped {
		t.Fatalf("wire=%s manager=%s", result.Detail.State, fixture.manager.GetStatus(string(fixture.sid)))
	}
	if fixture.registry.Count() != 1 || fixture.binding.closeCalls.Load() != 1 {
		t.Fatalf("registry=%d closes=%d", fixture.registry.Count(), fixture.binding.closeCalls.Load())
	}
}

func TestAuthorityRemoveExactCloseTombstoneTerminalAndGC(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, false)
	principal := DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}
	terminal := &removalCapture{}
	attach, _, _, gateErr := fixture.runtime.AttachControl(principal, "conn", fixture.sid, terminal)
	if gateErr != nil {
		t.Fatalf("AttachControl: %v", gateErr)
	}
	if _, err := fixture.runtime.Gate().Acquire(context.Background(), principal, attach.Lease(), fixture.sid); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if aerr := fixture.adapter.RemoveSession(context.Background(), "req", principal, fixture.sid); aerr != nil {
		t.Fatalf("RemoveSession: %v", aerr)
	}
	if fixture.binding.closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d", fixture.binding.closeCalls.Load())
	}
	if _, err := fixture.manager.Get(string(fixture.sid)); err == nil {
		t.Fatal("manager membership survived committed remove")
	}
	if fixture.registry.Count() != 0 {
		t.Fatalf("binding registry count = %d", fixture.registry.Count())
	}
	terminal.mu.Lock()
	payloads := append([][]byte(nil), terminal.payloads...)
	code := terminal.code
	terminal.mu.Unlock()
	if code != 1000 || len(payloads) != 2 {
		t.Fatalf("terminal code=%d payloads=%d", code, len(payloads))
	}
	var first, second struct {
		Type  string                `json:"type"`
		State contract.SessionState `json:"state"`
	}
	if err := json.Unmarshal(payloads[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payloads[1], &second); err != nil {
		t.Fatal(err)
	}
	if first.Type != contract.ServerEventTypeControlState || second.Type != contract.ServerEventTypeSessionState || second.State != contract.SessionStateRemoved {
		t.Fatalf("terminal order: first=%s second=%s/%s", first.Type, second.Type, second.State)
	}
}

func TestAuthorityIndeterminateCloseKeepsMembershipUnavailable(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, true)
	principal := DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}
	terminal := &removalCapture{}
	attach, _, _, gateErr := fixture.runtime.AttachControl(principal, "conn", fixture.sid, terminal)
	if gateErr != nil {
		t.Fatal(gateErr)
	}
	if _, err := fixture.runtime.Gate().Acquire(context.Background(), principal, attach.Lease(), fixture.sid); err != nil {
		t.Fatal(err)
	}
	aerr := fixture.adapter.RemoveSession(context.Background(), "req", principal, fixture.sid)
	if aerr == nil || aerr.Rest.status != 503 {
		t.Fatalf("indeterminate remove = %#v", aerr)
	}
	snapshot, err := fixture.manager.RemoteSnapshotByID(string(fixture.sid))
	if err != nil || snapshot.State != session.AuthorityUnavailable {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if len(terminal.payloads) != 0 {
		t.Fatalf("terminal delivered before commit: %d", len(terminal.payloads))
	}
}

type flakyResultJournal struct {
	base          SessionOperationJournal
	completeCalls atomic.Int32
}

func (j *flakyResultJournal) BeginIntent(ctx context.Context, intent SessionOperationIntent) (*OperationRecordPermit, error) {
	return j.base.BeginIntent(ctx, intent)
}
func (j *flakyResultJournal) Complete(ctx context.Context, permit *OperationRecordPermit, outcome SessionOperationOutcome, code contract.ErrorCode) error {
	if j.completeCalls.Add(1) == 1 && outcome == SessionOutcomeCommitted {
		return errors.New("injected result append failure")
	}
	return j.base.Complete(ctx, permit, outcome, code)
}
func (j *flakyResultJournal) ListRecent(ctx context.Context, limit uint16) ([]SessionOperationRecord, error) {
	return j.base.ListRecent(ctx, limit)
}
func (j *flakyResultJournal) IsReady() bool { return true }

func TestAuthorityIndeterminateRemoveRetriesSameBindingAfterTerminalConfirmation(t *testing.T) {
	fixture := newAuthoritativeFixture(t, nil, true)
	principal := DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}
	terminal := &removalCapture{}
	attach, _, _, _ := fixture.runtime.AttachControl(principal, "conn", fixture.sid, terminal)
	_, _ = fixture.runtime.Gate().Acquire(context.Background(), principal, attach.Lease(), fixture.sid)
	if aerr := fixture.adapter.RemoveSession(context.Background(), "first", principal, fixture.sid); aerr == nil {
		t.Fatal("first indeterminate remove unexpectedly succeeded")
	}
	fixture.binding.waiter.confirmed.Store(true)
	if aerr := fixture.adapter.RemoveSession(context.Background(), "retry", principal, fixture.sid); aerr != nil {
		t.Fatalf("confirmed retry: %v", aerr)
	}
	if fixture.binding.closeCalls.Load() != 1 || fixture.registry.Count() != 0 {
		t.Fatalf("CloseExact must be invoked once: closes=%d registry=%d", fixture.binding.closeCalls.Load(), fixture.registry.Count())
	}
}

func TestRemoveGCRegistryRetriesReceiptKeyedDebt(t *testing.T) {
	registry := NewRemoveGCRegistry()
	var calls atomic.Int32
	entry := &removeGCEntry{
		receipt: session.RemoveReceipt{ReceiptID: 41, SessionID: "gc", MembershipRevision: 2, LifecycleRevision: 2},
		steps: []removeGCStep{{run: func() error {
			if calls.Add(1) == 1 {
				return errors.New("injected gc failure")
			}
			return nil
		}}},
	}
	registry.Activate(entry)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !registry.Flush(ctx) {
		t.Fatal("remove GC debt did not flush")
	}
	if registry.Count() != 0 || calls.Load() < 2 {
		t.Fatalf("remaining=%d calls=%d", registry.Count(), calls.Load())
	}
}

func TestJournalRecoveryNeverInfersCommittedFromPendingRemove(t *testing.T) {
	dir := t.TempDir()
	journal := NewSessionOperationJournal(dir)
	_, err := journal.BeginIntent(context.Background(), SessionOperationIntent{
		OperationID: "pending-remove", SessionID: "sid", CLIType: contract.CLITypeClaudeCode,
		Operation: SessionOpRemove, Actor: SessionActorRemote,
	})
	if err != nil {
		t.Fatalf("BeginIntent: %v", err)
	}
	restarted := NewSessionOperationJournal(dir)
	records, err := restarted.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(records) < 2 || records[0].Phase != SessionPhaseRecovery || records[0].Outcome != SessionOutcomeIndeterminate {
		t.Fatalf("recovery records = %#v", records)
	}
	for _, record := range records {
		if record.OperationID == "pending-remove" && record.Outcome == SessionOutcomeCommitted {
			t.Fatalf("pending remove was fabricated as committed: %#v", record)
		}
	}
}

func TestJournalCommittedRetryDebtUsesOperationAndReceipt(t *testing.T) {
	journal := &flakyResultJournal{base: NewNoopSessionOperationJournal(true)}
	fixture := newAuthoritativeFixture(t, journal, false)
	principal := DevicePrincipal{DeviceID: "dev", DeviceName: "Device"}
	terminal := &removalCapture{}
	attach, _, _, _ := fixture.runtime.AttachControl(principal, "conn", fixture.sid, terminal)
	_, _ = fixture.runtime.Gate().Acquire(context.Background(), principal, attach.Lease(), fixture.sid)
	if aerr := fixture.adapter.RemoveSession(context.Background(), "req", principal, fixture.sid); aerr != nil {
		t.Fatalf("remove changed by journal failure: %v", aerr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !fixture.adapter.journalRetries.Flush(ctx) {
		t.Fatal("journal retry debt did not flush")
	}
	if fixture.adapter.journalRetries.Count() != 0 || journal.completeCalls.Load() < 2 {
		t.Fatalf("debt=%d completeCalls=%d", fixture.adapter.journalRetries.Count(), journal.completeCalls.Load())
	}
}
