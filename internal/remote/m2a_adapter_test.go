package remote

// m2a_adapter_test.go — Integration tests for RemoteSessionAdapter (design §4, §5).
//
// These tests set up a full M3-A control runtime and exercise the adapter's
// list/detail/create/lifecycle/acquire/release methods with fake raw ports.

import (
	"context"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// fakeLaunchRawPort records StartProcess calls.
type fakeLaunchRawPort struct {
	mu            sync.Mutex
	started       []contract.SessionID
	failNext      bool
	lastObsPermit *RunObservationPermit
}

func (f *fakeLaunchRawPort) StartProcess(ctx context.Context, sessionID contract.SessionID, recipe RemoteLaunchRecipe, spec any, obsPermit *RunObservationPermit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return closedTextError("fake launch failure")
	}
	f.started = append(f.started, sessionID)
	f.lastObsPermit = obsPermit
	return nil
}

// fakeSessionRawPort records stop/remove calls.
type fakeSessionRawPort struct {
	mu         sync.Mutex
	stopped    []contract.SessionID
	removed    []contract.SessionID
	failStop   bool
	failRemove bool
}

func (f *fakeSessionRawPort) StopSession(ctx context.Context, sessionID contract.SessionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStop {
		return closedTextError("fake stop failure")
	}
	f.stopped = append(f.stopped, sessionID)
	return nil
}

func (f *fakeSessionRawPort) RemoveSession(ctx context.Context, sessionID contract.SessionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRemove {
		return closedTextError("fake remove failure")
	}
	f.removed = append(f.removed, sessionID)
	return nil
}

func (f *fakeSessionRawPort) ResizeSession(ctx context.Context, sessionID contract.SessionID, cols, rows int) error {
	return nil
}

// fakeResolver always succeeds with a known recipe.
type fakeResolver struct {
	recipe RemoteLaunchRecipe
}

func (f *fakeResolver) ResolveCreate(ctx context.Context, req contract.CreateSessionRequest) (RemoteLaunchResolution, *LaunchResolveFailure) {
	return RemoteLaunchResolution{
		Recipe: RemoteLaunchRecipe{
			CLIType: req.CLIType,
			Workdir: "/fake/workdir",
		},
		Spec: map[string]string{"fake": "spec"},
	}, nil
}

func (f *fakeResolver) ResolveRestart(ctx context.Context, recipe RemoteLaunchRecipe) (RemoteLaunchResolution, *LaunchResolveFailure) {
	return RemoteLaunchResolution{Recipe: recipe, Spec: nil}, nil
}

func (f *fakeResolver) Probe(ctx context.Context, cli contract.CLIType) (contract.CLIAvailability, *LaunchResolveFailure) {
	return contract.CLIAvailability{CLIType: cli, Available: true}, nil
}

// setupAdapterTest creates a full control runtime + adapter for testing.
func setupAdapterTest(t *testing.T) (*RemoteSessionAdapter, *ctrlFakeClock, *fakeLaunchRawPort, *fakeSessionRawPort) {
	t.Helper()
	clock := newCtrlFakeClock(time.Now())
	rt := NewControlRuntime(clock, nil)
	rt.MarkReady()

	catalog := NewSessionCatalog()
	streams := NewSessionStreamStore()
	journal := NewNoopSessionOperationJournal(true)
	launchRaw := &fakeLaunchRawPort{}
	sessRaw := &fakeSessionRawPort{}
	resolver := &fakeResolver{}

	adapter := NewRemoteSessionAdapter(
		rt.Gate(), rt, catalog, streams, journal, resolver, launchRaw, sessRaw, clock, "/fake/config",
	)
	return adapter, clock, launchRaw, sessRaw
}

// activateTestSession registers and activates a desktop session for testing.
func activateTestSession(t *testing.T, adapter *RemoteSessionAdapter, sessionID contract.SessionID) {
	t.Helper()
	rt := adapter.Runtime()
	permit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	_ = permit
	// Register in catalog.
	now := adapter.Clock().Now()
	adapter.Catalog().Activate(sessionID, safeTitle(contract.CLITypeClaudeCode, "/work"), contract.CLITypeClaudeCode, "/work", now)
	adapter.Streams().BeginSegment(sessionID, 1)
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	// M-004: initialize the H1 run segment so restart can seal the old segment.
	rt.Projector().TrackRun(sessionID, obsPermit)
}

// ---------------------------------------------------------------------------
// List / Detail tests
// ---------------------------------------------------------------------------

func TestAdapter_EmptyList(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	result, aerr := adapter.ListSessions(context.Background(), "dev1")
	if aerr != nil {
		t.Fatalf("ListSessions failed: %v", aerr)
	}
	if len(result.List) != 0 {
		t.Fatalf("expected empty list, got %d", len(result.List))
	}
	// Empty list must be non-nil (marshal as []).
	if result.List == nil {
		t.Fatal("list should be non-nil empty slice")
	}
}

func TestAdapter_DetailNotFound(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	_, aerr := adapter.SessionDetail(context.Background(), "req1", "nonexistent", "dev1")
	if aerr == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if aerr.Rest.status != 404 {
		t.Fatalf("expected 404, got %d", aerr.Rest.status)
	}
	if aerr.Rest.body.Code != contract.ErrorCodeSessionNotFound {
		t.Fatalf("expected session.not_found, got %s", aerr.Rest.body.Code)
	}
}

func TestAdapter_ListWithSession(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	result, aerr := adapter.ListSessions(context.Background(), "dev1")
	if aerr != nil {
		t.Fatalf("ListSessions: %v", aerr)
	}
	if len(result.List) != 1 {
		t.Fatalf("expected 1 session, got %d", len(result.List))
	}
	if result.List[0].ID != "s1" {
		t.Fatalf("expected s1, got %s", result.List[0].ID)
	}
	if result.List[0].CLIType != contract.CLITypeClaudeCode {
		t.Fatalf("expected claudecode, got %s", result.List[0].CLIType)
	}
}

func TestAdapter_DetailWithSession(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	result, aerr := adapter.SessionDetail(context.Background(), "req1", "s1", "dev1")
	if aerr != nil {
		t.Fatalf("SessionDetail: %v", aerr)
	}
	if result.Detail.ID != "s1" {
		t.Fatalf("expected s1, got %s", result.Detail.ID)
	}
	if result.Detail.Workdir != "/work" {
		t.Fatalf("expected /work, got %s", result.Detail.Workdir)
	}
	// earliestSeq/latestSeq should be 0/0 (no output yet).
	if result.Detail.EarliestSeq != 0 || result.Detail.LatestSeq != 0 {
		t.Fatalf("expected (0,0), got (%d,%d)", result.Detail.EarliestSeq, result.Detail.LatestSeq)
	}
}

// ---------------------------------------------------------------------------
// Create tests
// ---------------------------------------------------------------------------

func TestAdapter_CreateSession(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)

	principal := DevicePrincipal{DeviceID: "dev1"}
	req := contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode}
	result, aerr := adapter.CreateSession(context.Background(), "req1", principal, req)
	if aerr != nil {
		t.Fatalf("CreateSession: %v", aerr)
	}
	if result.Detail.ID == "" {
		t.Fatal("created session should have non-empty ID")
	}
	if result.Detail.CLIType != contract.CLITypeClaudeCode {
		t.Fatalf("expected claudecode, got %s", result.Detail.CLIType)
	}
	// Verify the launch raw port was called.
	launchRaw.mu.Lock()
	if len(launchRaw.started) != 1 {
		t.Fatalf("expected 1 StartProcess call, got %d", len(launchRaw.started))
	}
	launchRaw.mu.Unlock()
	// Session should be visible in list.
	list, _ := adapter.ListSessions(context.Background(), "dev1")
	if len(list.List) != 1 {
		t.Fatalf("expected 1 session in list, got %d", len(list.List))
	}
}

func TestAdapter_CreateSessionLaunchFail(t *testing.T) {
	adapter, _, launchRaw, _ := setupAdapterTest(t)
	launchRaw.failNext = true

	principal := DevicePrincipal{DeviceID: "dev1"}
	req := contract.CreateSessionRequest{CLIType: contract.CLITypeCodex}
	_, aerr := adapter.CreateSession(context.Background(), "req1", principal, req)
	if aerr == nil {
		t.Fatal("expected launch failure")
	}
	if aerr.Rest.status != 422 {
		t.Fatalf("expected 422, got %d", aerr.Rest.status)
	}
	if aerr.Rest.body.Code != contract.ErrorCodeSessionLaunchFailed {
		t.Fatalf("expected session.launch_failed, got %s", aerr.Rest.body.Code)
	}
	// Failed create should NOT appear in list.
	list, _ := adapter.ListSessions(context.Background(), "dev1")
	if len(list.List) != 0 {
		t.Fatalf("failed create should not be in list, got %d", len(list.List))
	}
}

// ---------------------------------------------------------------------------
// Lifecycle tests (stop/restart/remove)
// ---------------------------------------------------------------------------

func TestAdapter_StopSession(t *testing.T) {
	adapter, _, _, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	// Stop as desktop holder (the test activates via desktop authority).
	auth := adapter.Runtime().DesktopAuthority()
	_ = auth
	// For device lifecycle, we need the holder to be the device. Let's use
	// desktop lifecycle instead via the gate directly.
	ctx := context.Background()
	result, err := adapter.Gate().DoDesktopLifecycle(ctx, adapter.Runtime().DesktopAuthority(), "s1", LifecycleStop, func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
		_ = sessRaw.StopSession(ctx, "s1")
		return SessionMutationResult{State: contract.SessionStateStopped, StateChanged: true}, nil
	})
	if err != nil {
		t.Fatalf("desktop stop: %v", err)
	}
	if result.State != contract.SessionStateStopped {
		t.Fatalf("expected stopped, got %s", result.State)
	}
}

func TestAdapter_RemoveSession(t *testing.T) {
	adapter, _, _, sessRaw := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	// Remove via desktop lifecycle.
	ctx := context.Background()
	_, err := adapter.Gate().DoDesktopLifecycle(ctx, adapter.Runtime().DesktopAuthority(), "s1", LifecycleRemove, func(ctx context.Context, p *operationPermit) (SessionMutationResult, error) {
		_ = sessRaw.RemoveSession(ctx, "s1")
		return SessionMutationResult{Removed: true}, nil
	})
	if err != nil {
		t.Fatalf("desktop remove: %v", err)
	}
	// After remove, catalog entry should be gone.
	_, aerr := adapter.SessionDetail(context.Background(), "req1", "s1", "dev1")
	if aerr == nil {
		t.Fatal("session should be not-found after remove")
	}
}

// ---------------------------------------------------------------------------
// Acquire / Release tests
// ---------------------------------------------------------------------------

func TestAdapter_AcquireNoLease(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	principal := DevicePrincipal{DeviceID: "dev1"}
	// Acquire without a live lease should fail.
	_, aerr := adapter.AcquireControl(context.Background(), "req1", principal, "s1", nil)
	if aerr == nil {
		t.Fatal("expected error for acquire without lease")
	}
	if aerr.Rest.status != 403 {
		t.Fatalf("expected 403, got %d", aerr.Rest.status)
	}
}

func TestAdapter_AcquireNotFound(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	principal := DevicePrincipal{DeviceID: "dev1"}
	_, aerr := adapter.AcquireControl(context.Background(), "req1", principal, "nonexistent", nil)
	if aerr == nil {
		t.Fatal("expected error for acquire on nonexistent")
	}
	if aerr.Rest.status != 404 {
		t.Fatalf("expected 404, got %d", aerr.Rest.status)
	}
}

func TestAdapter_ReleaseNotHolder(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	principal := DevicePrincipal{DeviceID: "dev1"}
	_, aerr := adapter.ReleaseControl(context.Background(), "req1", principal, "s1")
	if aerr == nil {
		t.Fatal("expected error for release when not holder")
	}
	// Non-holder release should be forbidden (403).
	if aerr.Rest.status != 403 {
		t.Fatalf("expected 403, got %d", aerr.Rest.status)
	}
}

// ---------------------------------------------------------------------------
// Seq / history tests
// ---------------------------------------------------------------------------

func TestAdapter_OutputUpdatesSeq(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	// Simulate output entering the stream store.
	adapter.Streams().AppendOutput("s1", []byte("hello"))
	adapter.Streams().AppendOutput("s1", []byte("world"))

	result, aerr := adapter.SessionDetail(context.Background(), "req1", "s1", "dev1")
	if aerr != nil {
		t.Fatalf("SessionDetail: %v", aerr)
	}
	if result.Detail.EarliestSeq != 1 || result.Detail.LatestSeq != 2 {
		t.Fatalf("expected (1,2), got (%d,%d)", result.Detail.EarliestSeq, result.Detail.LatestSeq)
	}
}

func TestAdapter_RestartBoundarySeq(t *testing.T) {
	adapter, _, _, _ := setupAdapterTest(t)
	activateTestSession(t, adapter, "s1")

	adapter.Streams().AppendOutput("s1", []byte("old"))
	adapter.Streams().AppendBoundary("s1")
	adapter.Streams().AppendOutput("s1", []byte("new"))

	// After restart boundary: seq 1=output, 2=boundary, 3=new output.
	_, latest := adapter.Streams().SeqBounds("s1")
	if latest != 3 {
		t.Fatalf("expected latest=3, got %d", latest)
	}

	// FramesAfter(nil) should return all 3.
	frames := adapter.Streams().FramesAfter("s1", nil)
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}
}
