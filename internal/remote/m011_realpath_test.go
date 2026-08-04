package remote

// m011_realpath_test.go — M-011 R2 real-path evidence (diting 20260804-m2-int-
// review-round2 §M-011).
//
// This file adds TWO real-path tests that close the two remaining M-011 gaps:
//
//   - TestM011_T43_RealPTYServiceCallbackPath: PTY bytes flow through a REAL
//     internal/pty.Service whose RunEventSink is the real RunEventProjector
//     (production wiring app.go:1119 `Pty.SetRunEventSink(control.Projector())`
//     + control_wiring.go::launchEmbeddedPTY `StartResolvedWithRun(sid,spec,
//     obsPermit)`). Data enters via the real readLoop→`runSink.OfferOutput`
//     callback entry — the test NEVER calls Projector().OfferOutput directly.
//   - TestM011_T44_Cell3_RealRestartProductionPath: restart is driven through
//     the PRODUCTION adapter entry RemoteSessionAdapter.RestartSession (→
//     lifecycle → gate.DoDeviceLifecycle → restartRawEffect: H1
//     SealRestartSegmentForPermit → stop → resolve → CommitRestartRun →
//     StartProcess with the new RunObservationPermit). It does NOT call the
//     committer restart helpers directly.
//
// The older helper-level tests (TestM011_T43_PTYProjectorUniqueProducer_*
// /TestM011_T44_Cell3_SnapshotSegmentAndBoundaryOrdinal) are retained but
// annotated UNIT-LEVEL — they prove the projector/committer units in isolation,
// not the production wiring proven here.
//
// Only raw I/O edges are faked: a deterministic fake CLI (`/bin/sh -c "printf
// ..."` in a real PTY) for T43, and fakeResolver/fakeLaunchRawPort/
// fakeSessionRawPort seams for T44 (the same seams production code already
// defines behind the gate). Synchronization is via bounded channel polls
// (barriers); every spawned PTY goroutine is released via t.Cleanup.

import (
	"bytes"
	"context"
	"encoding/base64"
	runtime_ "runtime"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote/contract"
)

// ===========================================================================
// T43 — REAL pty.Service callback path (unique producer end-to-end)
//
// Drives a REAL internal/pty.Service with the projector wired as its
// RunEventSink (production wiring). A real PTY is spawned bound to the
// RunObservationPermit as the runHandle (production wiring). The service's
// readLoop reads PTY bytes and invokes runSink.OfferOutput(runHandle, seq,
// chunk) — the real callback entry. The projector commits each chunk to the H1
// feed and incrementally pumps to the v1 stream Seq + causal hub. A causal
// subscription (WS subscriber) registered before the PTY starts receives the
// events. The test asserts the FULL chain and NEVER calls
// Projector().OfferOutput directly.
// ===========================================================================

func TestM011_T43_RealPTYServiceCallbackPath(t *testing.T) {
	// A real PTY backend exists only on darwin (creack-pty) and windows
	// (ConPTY). On other platforms the pty.Service stub cannot spawn; skip
	// with a clear reason (CI full go test runs on macOS where this executes).
	if runtime_.GOOS != "darwin" && runtime_.GOOS != "windows" {
		t.Skipf("real PTY backend unavailable on %s; T43 real-path requires darwin/windows", runtime_.GOOS)
	}

	// Real control runtime + stream store (production H1/H3 wiring, identical
	// to what NewRemoteSessionAdapter composes).
	clk := newCtrlFakeClock(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	rt := NewControlRuntime(clk, nil)
	rt.MarkReady() // marks the projector ready too
	streams := NewSessionStreamStore()
	rt.Projector().SetStreamPump(streams) // as the adapter wires in production

	ctx := context.Background()
	sid := contract.SessionID("s-t43-realpty")

	// Production run lifecycle: begin → activate → track (activates H1
	// segment 1, writing the runActivated first record).
	_, runPermit, obsPermit, err := rt.BeginDesktopRun(ctx, sid)
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(ctx, runPermit); err != nil {
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(sid, obsPermit)

	// WS subscriber (causal subscription) registered BEFORE the PTY starts,
	// startAfter=0 so it receives the runActivated barrier + all output.
	subLease := &ControlConnectionLease{deviceID: "devT43"}
	subLease.live.Store(true)
	sub := rt.Hub().RegisterCausalSubscription(sid, 0, subLease, nil)
	t.Cleanup(func() { rt.Hub().UnregisterCausalSubscription(sub) })

	// REAL internal/pty.Service with the projector wired as the RunEventSink —
	// this is the production callback entry (app.go:1119
	// `a.Pty.SetRunEventSink(a.control.Projector())`). The service's readLoop
	// is the ONLY caller of runSink.OfferOutput in production; this test does
	// not invoke the projector directly.
	ptySvc := pty.NewService(nil)
	ptySvc.SetRunEventSink(rt.Projector())
	t.Cleanup(func() { _ = ptySvc.Close(string(sid)) })

	// Start a REAL PTY bound to the obsPermit as runHandle (production wiring:
	// control_wiring.go::launchEmbeddedPTY `StartResolvedWithRun(sessionID,
	// spec, obsPermit)`). A deterministic fake CLI (`/bin/sh -c "printf ..."`),
	// not a real claude/codex binary — the "pty fake" the task permits for
	// synchronization, while the data still flows through the real service
	// callback entry.
	spec := platform.ResolvedLaunchSpec{
		WorkDir: t.TempDir(),
		CLI: platform.ResolvedCLI{
			Path: fakeCLIShell(),
			Args: []string{"-c", "printf 'alpha'; printf 'bb'; printf 'gamma'"},
		},
		Env:           platform.ResolvedEnv{Variables: []string{"PATH=/usr/bin:/bin"}},
		PTYCols:       80,
		PTYRows:       24,
		BootstrapMode: platform.BootstrapDirectCommand,
	}
	if _, err := ptySvc.StartResolvedWithRun(string(sid), spec, obsPermit); err != nil {
		// If the host cannot spawn a real PTY at all (e.g. sandboxed CI without
		// /bin/sh), skip rather than fail — but record the reason.
		t.Skipf("cannot spawn real PTY for T43 real-path on this host: %v", err)
	}

	// Barrier: poll the subscriber until the expected output content arrives
	// (bounded). The readLoop is asynchronous; output arrives after spawn.
	// "alpha"+"bb"+"gamma" concatenates to "alphabbgamma".
	wantContent := []byte("alphabbgamma")
	var all []queuedEvent
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all = append(all, sub.Drain()...)
		if bytes.Contains(concatOutputBytes(all), wantContent) {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	// Assertion 1: the subscriber is NOT fenced (queue never overflowed).
	if sub.IsFenced() {
		t.Fatal("subscriber was fenced; the real-PTY output overflowed the queue (unexpected for ~11 bytes)")
	}

	// Assertion 2: at least one output event arrived with the expected content.
	outs := outputEvents(all)
	concat := concatOutputBytes(all)
	if !bytes.Contains(concat, wantContent) {
		t.Fatalf("real PTY output did not reach the WS subscriber through the service callback; got %d output events, concat=%q", len(outs), string(concat))
	}

	// Assertion 3: the runActivated running barrier is the FIRST event (the
	// pump orders it ahead of any output). Filter state events: runActivated is
	// SessionStateEvent{State: running}; a later exit would be State: exited.
	if len(all) == 0 {
		t.Fatal("no events received; runActivated barrier missing")
	}
	if sse, ok := all[0].event.(contract.SessionStateEvent); !ok || sse.State != contract.SessionStateRunning {
		t.Fatalf("first event must be the runActivated running barrier, got %#v", all[0].event)
	}

	// Assertion 4: output events carry a valid wire schema and monotonic Seq
	// starting at 1 (the feed assigns Seq per output record; the real PTY may
	// coalesce reads, so the chunk count N is variable but Seq is dense).
	for i, oe := range outs {
		if oe.Type != contract.ServerEventTypeOutput {
			t.Fatalf("output[%d] type = %q, want %q", i, oe.Type, contract.ServerEventTypeOutput)
		}
		if oe.SessionID != sid {
			t.Fatalf("output[%d] sessionID = %q, want %q", i, oe.SessionID, sid)
		}
		if err := contract.ValidateServerEvent(oe); err != nil {
			t.Fatalf("output[%d] schema invalid: %v", i, err)
		}
	}
	for i, oe := range outs {
		if oe.Seq != contract.Seq(i+1) {
			t.Fatalf("output Seq must be dense/monotonic from 1: output[%d].Seq = %d, want %d", i, oe.Seq, i+1)
		}
	}

	// Assertion 5: unique producer — the H1 feed holds exactly the run-scoped
	// records committed by the projector (runActivated + N outputs; an exit
	// record may also be present if the process already exited). No second
	// producer (legacy naked-SessionID bypass) can add records.
	feed := rt.Committer().EnsureFeed(sid)
	feed.mu.Lock()
	recCount := len(feed.records)
	feed.mu.Unlock()
	nOut := len(outs)
	// runActivated (1) + outputs (nOut), optionally + exit (1) if the process
	// already terminated before the snapshot.
	if recCount != 1+nOut && recCount != 1+nOut+1 {
		t.Fatalf("feed record count = %d, want %d or %d (runActivated + %d outputs [+ exit]); a second producer would inflate this", recCount, 1+nOut, 1+nOut+1, nOut)
	}

	// Assertion 6: stream Seq bounds reflect exactly the N output frames
	// (runActivated/exit consume no Seq; output/boundary consume Seq).
	earliest, latest := streams.SeqBounds(sid)
	if earliest != 1 || latest != contract.Seq(nOut) {
		t.Fatalf("stream Seq bounds = (%d,%d), want (1,%d)", earliest, latest, nOut)
	}

	// Assertion 7: the projector emitted the run-tagged snapshot (proves the
	// runHandle round-tripped through the real service back to the projector —
	// an untyped/nil handle would have left the run untracked).
	token, version := rt.Projector().GetRunSnapshot(sid)
	if token == "" {
		t.Fatal("projector has no run snapshot for the session; the obsPermit runHandle did not round-trip through the real pty.Service")
	}
	_ = version
}

// ===========================================================================
// T44 Cell 3 — REAL production restart entry (adapter.RestartSession)
//
// Drives the PRODUCTION restart path: RemoteSessionAdapter.RestartSession →
// lifecycle → gate.DoDeviceLifecycle → restartRawEffect (H1
// SealRestartSegmentForPermit → sessRaw.StopSession → resolver.ResolveRestart
// → CommitRestartRun [mint exact new run + boundary-first] → launchRaw.
// StartProcess with the NEW RunObservationPermit). The fake resolver is the CLI
// resolver seam; fake launch/sess raw are the PTY seams (the same interfaces
// production code defines behind the gate). It asserts the real call chain
// (seal → mint new run → commit boundary → new-permit output enters the stream)
// with order + watermark, and does NOT call the committer restart helpers.
// ===========================================================================

func TestM011_T44_Cell3_RealRestartProductionPath(t *testing.T) {
	adapter, _, launchRaw, sessRaw := setupAdapterTest(t)
	rt := adapter.Runtime()

	// Production CREATE entry → a device-launched session. CreateSession
	// resolves (fakeResolver), begins a device launch, starts the process
	// (fakeLaunchRawPort captures the obsPermit), activates the run, and
	// activates H1 segment 1 (TrackRun). This is the real create path, not a
	// helper-activated session.
	principal := DevicePrincipal{DeviceID: "devT44c3", DeviceName: "phone"}
	createRes, aerr := adapter.CreateSession(context.Background(), "req-create", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
	if aerr != nil {
		t.Fatalf("CreateSession: %v", aerr)
	}
	sid := createRes.Detail.ID

	// Capture the OLD obs permit bound to the original run (the one the fake
	// launch port received at create time).
	launchRaw.mu.Lock()
	oldPermit := launchRaw.lastObsPermit
	launchRaw.mu.Unlock()
	if oldPermit == nil || oldPermit.run == nil {
		t.Fatal("create did not capture a valid obs permit")
	}

	// Establish a device HOLDER so the production restart entry
	// (gate.DoDeviceLifecycle, which requires owner==device) authorizes the
	// restart. Attach a connection + acquire control (real gate path).
	lease, _ := rt.Directory().Attach(principal.DeviceID, principal.DeviceName, ConnectionID("conn-t44c3"), sid)
	if lease == nil {
		t.Fatal("Attach returned nil lease")
	}
	if _, gErr := adapter.Gate().Acquire(context.Background(), principal, lease, sid); gErr != nil {
		t.Fatalf("Acquire device control: %v", gErr)
	}

	// Establish pre-restart segment-1 state via the production sink (the
	// projector as RunEventSink — the same entry the real PTY uses; T43 proves
	// the PTY→projector wiring). This pumps runActivated + one output,
	// advancing the watermark so the post-restart boundary/new-output ordinals
	// are strictly higher.
	rt.Projector().OfferOutput(oldPermit, 1, []byte("old-chunk"))
	preRestartWm := rt.Hub().WatermarkFor(sid)
	if preRestartWm.Event < 1 {
		t.Fatalf("pre-restart watermark must be >= 1 after old output, got %d", preRestartWm.Event)
	}

	// WS subscriber registered at the pre-restart watermark: it must receive
	// ONLY the post-restart boundary + new output (startAfter filter).
	wmLease := &ControlConnectionLease{deviceID: principal.DeviceID}
	wmLease.live.Store(true)
	sub := rt.Hub().RegisterCausalSubscription(sid, preRestartWm.Event, wmLease, nil)
	t.Cleanup(func() { rt.Hub().UnregisterCausalSubscription(sub) })

	// ---- PRODUCTION RESTART ENTRY ----
	baseStops := len(sessRaw.stopped)
	baseStarts := len(launchRaw.started)
	restartRes, raerr := adapter.RestartSession(context.Background(), "req-restart", principal, sid)
	if raerr != nil {
		t.Fatalf("RestartSession (production entry): %v", raerr)
	}
	if restartRes.Detail.State != contract.SessionStateRunning {
		t.Fatalf("restart result state = %s, want running", restartRes.Detail.State)
	}

	// (a) Real call chain — stop old + start new (via the seams the gate drives).
	if got := len(sessRaw.stopped); got != baseStops+1 {
		t.Fatalf("old process must be stopped once via the production restart path: stops=%d want %d", got, baseStops+1)
	}
	if got := len(launchRaw.started); got != baseStarts+1 {
		t.Fatalf("new process must be started once via the production restart path: starts=%d want %d", got, baseStarts+1)
	}

	// (b) Mint a NEW run identity — the new permit captured by the fake launch
	// port is distinct from the old permit (different run pointer + nonce).
	launchRaw.mu.Lock()
	newPermit := launchRaw.lastObsPermit
	launchRaw.mu.Unlock()
	if newPermit == nil || newPermit.run == nil {
		t.Fatal("restart did not start the new process with a new RunObservationPermit (nil permit — M-004 regression)")
	}
	if newPermit.run == oldPermit.run {
		t.Fatal("restart did not mint a NEW run identity (same run pointer as old)")
	}
	if newPermit.Run().nonce == oldPermit.Run().nonce {
		t.Fatalf("new run nonce = old run nonce (%d); restart must mint a distinct run", newPermit.Run().nonce)
	}

	// (c) H1 SEAL — a late observation for the OLD run is dropped (the seal +
	// run swap fence it). This proves SealRestartSegmentForPermit ran inside
	// the production restart path.
	lateOut := rt.Committer().CommitRunObservation(oldPermit, NewOutputObservation([]byte("late-old-run")))
	if lateOut.Disposition != ObservationDroppedStaleRun && lateOut.Disposition != ObservationDroppedSegmentSealed {
		t.Fatalf("late old-run observation must be dropped after the restart seal, got %s", lateOut.Disposition)
	}

	// (d) COMMIT BOUNDARY — segment 2 exists with the boundary as its first
	// record, and the watermark advanced strictly beyond the pre-restart
	// watermark (the boundary reservation consumes a higher ordinal).
	postRestartWm := rt.Hub().WatermarkFor(sid)
	if postRestartWm.Event <= preRestartWm.Event {
		t.Fatalf("boundary watermark (%d) must be > pre-restart watermark (%d)", postRestartWm.Event, preRestartWm.Event)
	}
	snap2, _, serr := rt.Feed().SnapshotAndSubscribe(sid)
	if serr != nil {
		t.Fatalf("SnapshotAndSubscribe after restart: %v", serr)
	}
	if snap2.Position.SegmentID != 2 {
		t.Fatalf("expected segment 2 after restart, got %d", snap2.Position.SegmentID)
	}
	if len(snap2.Records) == 0 || snap2.Records[0].Kind != LiveRecordRestartBoundary {
		t.Fatalf("expected restart boundary as first record of segment 2, got %d records", len(snap2.Records))
	}

	// (e) NEW-PERMIT OUTPUT ENTERS THE STREAM — output committed via the NEW
	// permit through the production sink (the projector as RunEventSink — the
	// same entry the real PTY uses; T43 proves the PTY→projector wiring) commits
	// to segment 2 and advances the watermark strictly beyond the boundary.
	// Proves the new permit is valid and segment-2-bound.
	rt.Projector().OfferOutput(newPermit, 1, []byte("new-chunk"))
	afterNewWm := rt.Hub().WatermarkFor(sid)
	if afterNewWm.Event <= postRestartWm.Event {
		t.Fatalf("new-permit output watermark (%d) must be > boundary watermark (%d)", afterNewWm.Event, postRestartWm.Event)
	}
	// The new output committed to segment 2 (not segment 1).
	newCommit := rt.Committer().CommitRunObservation(newPermit, NewOutputObservation([]byte("new-chunk-2")))
	if newCommit.Disposition != ObservationCommitted {
		t.Fatalf("second new-permit output must commit, got %s", newCommit.Disposition)
	}
	if newCommit.SegmentID != 2 {
		t.Fatalf("new-permit output must be in segment 2, got %d", newCommit.SegmentID)
	}

	// (f) ORDER + WATERMARK via the subscriber — the subscriber at the
	// pre-restart watermark receives [boundary, newOutput] in that order
	// (boundary pumped inside CommitRestartRun before any new-run output).
	delivered := drainSubscription(sub, 2*time.Second)
	if len(delivered) < 2 {
		t.Fatalf("subscriber must receive boundary + new output, got %d events", len(delivered))
	}
	bnd, ok := delivered[0].event.(contract.SessionRestartBoundaryEvent)
	if !ok || !bnd.RestartBoundary {
		t.Fatalf("first delivered event must be the restart boundary (RestartBoundary=true), got %#v", delivered[0].event)
	}
	oe, ok := delivered[1].event.(contract.OutputEvent)
	if !ok {
		t.Fatalf("second delivered event must be the new-permit output, got %#v", delivered[1].event)
	}
	if string(decodeChunk(t, oe.Chunk)) != "new-chunk" {
		t.Fatalf("new-permit output content = %q, want %q", string(decodeChunk(t, oe.Chunk)), "new-chunk")
	}
	// Boundary ordinal strictly precedes the new output ordinal (order).
	if delivered[0].ordinal >= delivered[1].ordinal {
		t.Fatalf("boundary ordinal (%d) must precede new-output ordinal (%d)", delivered[0].ordinal, delivered[1].ordinal)
	}
}

// ===========================================================================
// small helpers
// ===========================================================================

// fakeCLIShell returns the shell binary used as the deterministic fake CLI.
// /bin/sh exists on darwin; on windows the test is skipped before this matters.
func fakeCLIShell() string {
	if runtime_.GOOS == "windows" {
		return "cmd.exe"
	}
	return "/bin/sh"
}

// outputEvents filters OutputEvent records from a batch of queued events.
func outputEvents(events []queuedEvent) []contract.OutputEvent {
	var outs []contract.OutputEvent
	for _, qe := range events {
		if oe, ok := qe.event.(contract.OutputEvent); ok {
			outs = append(outs, oe)
		}
	}
	return outs
}

// concatOutputBytes decodes and concatenates the base64 chunks of all output
// events (in arrival order) so content assertions are robust to PTY read
// coalescing.
func concatOutputBytes(events []queuedEvent) []byte {
	var buf bytes.Buffer
	for _, oe := range outputEvents(events) {
		if raw, err := base64.StdEncoding.DecodeString(oe.Chunk); err == nil {
			buf.Write(raw)
		}
	}
	return buf.Bytes()
}

// decodeChunk decodes a base64 OutputEvent chunk to raw bytes.
func decodeChunk(t *testing.T, chunk string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(chunk)
	if err != nil {
		t.Fatalf("decode output chunk: %v", err)
	}
	return raw
}

// drainSubscription polls a causal subscription, accumulating events until at
// least minCount have arrived or the deadline elapses. Returns all accumulated
// events in arrival order.
func drainSubscription(sub *causalHubSubscription, timeout time.Duration) []queuedEvent {
	var all []queuedEvent
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		all = append(all, sub.Drain()...)
		// Drain until the deadline to capture all in-order events; the caller
		// asserts on what arrived. A short stabilization wait ensures the
		// pump has flushed.
		time.Sleep(10 * time.Millisecond)
	}
	// final drain
	all = append(all, sub.Drain()...)
	return all
}
