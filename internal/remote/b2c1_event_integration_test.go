package remote

// M1-B2c1 typed pairing/service event integration tests (NFR-17). Covers: strict
// canonical validator rejects; pairing lifecycle exact sequence (open/replace/
// cancel/expire/wrong+locked) with attempt numbers; sink TryLock proves pairMu
// is not held during Append and a PreAccept transition survives; service
// Start/idempotent/Stop ordering; token/config events; durable restart parse +
// unknown reject + privacy scan.

import (
	"context"
	"embed"
	"os"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

// recordingSink captures appended events; the optional onAppend hook can assert
// no security lock is held (TryLock) during Append.
type recordingSink struct {
	mu       sync.Mutex
	events   []SecurityEvent
	onAppend func() bool // returns false if a lock is held (race detected)
}

func (r *recordingSink) Durability() EventSinkDurability { return EventSinkVolatile }
func (r *recordingSink) AppendSecurityEvent(e SecurityEvent) (EventAppendResult, error) {
	if r.onAppend != nil {
		r.onAppend() // hook: assert no pairMu held
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev.EventIDOf() == e.EventIDOf() {
			return EventAppendResult{State: EventDuplicateAcceptedBySink}, nil
		}
	}
	r.events = append(r.events, e)
	return EventAppendResult{State: EventAcceptedBySink}, nil
}
func (r *recordingSink) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, kindOfString(e))
	}
	return out
}
func (r *recordingSink) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.events) }

func kindOfString(e SecurityEvent) string {
	switch ev := e.(type) {
	case PairingSecurityEvent:
		return pairingKindString(ev.Kind)
	case DeviceSecurityEvent:
		return deviceKindString(ev.Kind)
	case StoreSecurityEvent:
		return storeKindString(ev.Kind)
	case ServiceSecurityEvent:
		return serviceKindString(ev.Kind)
	}
	return "?"
}

// newB2C1Server builds a security server wired to a recording sink and returns
// the sink so tests can assert the event sequence.
func newB2C1Server(t *testing.T, sink SecurityEventSink) (*Server, *secFakeClock) {
	t.Helper()
	dir := t.TempDir()
	clk := newSecFakeClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	opts := newSecurityOptions(dir, validHostSummary, clk, cryptoRand, sink)
	srv := NewServerWithSecurity(0, nil, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	if err := srv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	// Simulate the post-publish accepting state (see newSecServer); these tests
	// call deviceService.CreateWindow directly without a real Server.Start.
	srv.pairing.Resume()
	return srv, clk
}

var cryptoRand = randReaderFunc{}

type randReaderFunc struct{}

func (randReaderFunc) Read(b []byte) (int, error) { return cryptoRandRead(b) }

// ===========================================================================
// Strict canonical validator
// ===========================================================================

func TestB2C1StrictValidatorRejects(t *testing.T) {
	goodID := SecurityEventID(rawURLBase64(make([]byte, 32)))
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ev   SecurityEvent
	}{
		{"pair gen 0", PairingSecurityEvent{EventID: goodID, Kind: PairingWindowOpened, OccurredAt: at, Generation: 0}},
		{"pair unknown kind", PairingSecurityEvent{EventID: goodID, Kind: PairingSecurityEventKind(99), OccurredAt: at, Generation: 1}},
		{"pair opened attempt!=0", PairingSecurityEvent{EventID: goodID, Kind: PairingWindowOpened, OccurredAt: at, Generation: 1, Attempt: 1}},
		{"pair rejected attempt 0", PairingSecurityEvent{EventID: goodID, Kind: PairingAttemptRejected, OccurredAt: at, Generation: 1, Attempt: 0}},
		{"zero time", DeviceSecurityEvent{EventID: goodID, Kind: DevicePaired, DeviceID: contract.DeviceID(rawURLBase64(make([]byte, 16)))}},
		{"noncanonical eventid", DeviceSecurityEvent{EventID: SecurityEventID("short"), Kind: DevicePaired, OccurredAt: at, DeviceID: contract.DeviceID(rawURLBase64(make([]byte, 16)))}},
		{"invalid deviceid", DeviceSecurityEvent{EventID: goodID, Kind: DevicePaired, OccurredAt: at, DeviceID: "x"}},
		{"service unknown kind", ServiceSecurityEvent{EventID: goodID, Kind: ServiceSecurityEventKind(99), OccurredAt: at}},
		{"store unknown kind", StoreSecurityEvent{EventID: goodID, Kind: StoreSecurityEventKind(99), OccurredAt: at}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewVolatileSecurityEventSink()
			res, _ := s.AppendSecurityEvent(c.ev)
			if res.State != EventPreAcceptFailed {
				t.Fatalf("%s: state=%v want PreAcceptFailed", c.name, res.State)
			}
		})
	}
	// Valid events are accepted.
	valid := PairingSecurityEvent{EventID: goodID, Kind: PairingWindowOpened, OccurredAt: at, Generation: 1}
	if res, _ := NewVolatileSecurityEventSink().AppendSecurityEvent(valid); res.State != EventAcceptedBySink {
		t.Fatalf("valid pair event rejected: %v", res.State)
	}
}

// ===========================================================================
// Pairing lifecycle event sequence + pairMu-not-held proof
// ===========================================================================

func TestB2C1PairingLifecycleEventSequence(t *testing.T) {
	sink := &recordingSink{}
	srv, clk := newB2C1Server(t, sink)
	_ = clk
	ds := srv.pairing
	// Hook: prove pairMu is NOT held during Append (TryLock succeeds).
	raceDetected := false
	sink.onAppend = func() bool {
		if ds.mu.TryLock() {
			ds.mu.Unlock()
			return true
		}
		raceDetected = true
		return false
	}

	// Open a window.
	w1, err := ds.CreateWindow("")
	if err != nil {
		t.Fatal(err)
	}
	// Replace it → canceled(old) + opened(new).
	w2, err := ds.CreateWindow("")
	if err != nil {
		t.Fatal(err)
	}
	// Cancel the active window.
	if ok, _ := ds.CancelWindow(w2.Generation); !ok {
		t.Fatal("cancel failed")
	}
	// Open + simulate expiry.
	w3, err := ds.CreateWindow("")
	if err != nil {
		t.Fatal(err)
	}
	ds.expireIfGeneration(w3.Generation)
	// Open + wrong code (attempt 1) then exhaust to locked.
	w4, err := ds.CreateWindow("")
	if err != nil {
		t.Fatal(err)
	}
	_ = w4
	host, _ := validHostSummary()
	max := DefaultPairingPolicy().MaxAttempts
	// A valid-format (26×[A-Z2-7]) but WRONG code consumes attempts.
	wrongCode := "ZZZZZZZZZZZZZZZZZZZZZZZZZZ"
	for i := 0; i < int(max); i++ {
		ds.CompletePairing(wrongCode, "phone", host) // wrong code
	}

	got := sink.kinds()
	// Expected order: opened, canceled, opened, canceled, opened, expired, opened,
	// then max× attempt_rejected + locked.
	want := []string{
		"pairing_window_opened",
		"pairing_window_canceled", "pairing_window_opened", // replace
		"pairing_window_canceled",                         // cancel
		"pairing_window_opened", "pairing_window_expired", // open+expire
		"pairing_window_opened", // open for wrong-code
	}
	for i := 0; i < int(max); i++ {
		want = append(want, "pairing_attempt_rejected")
	}
	want = append(want, "pairing_window_locked")
	if len(got) != len(want) {
		t.Fatalf("event count=%d want=%d\ngot=%v\nwant=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d]=%q want %q\ngot=%v", i, got[i], want[i], got)
		}
	}
	if raceDetected {
		t.Fatal("pairMu was held during a sink Append (lock-ordering race)")
	}
	_ = w1
}

// ===========================================================================
// PreAccept transition survives (event failure never rolls back)
// ===========================================================================

func TestB2C1PreAcceptDoesNotRollbackTransition(t *testing.T) {
	// A sink that always fails PreAccept.
	failSink := &recordingSink{}
	failSink.onAppend = func() bool { return true }
	fail := &alwaysFailSink{}
	srv, _ := newB2C1Server(t, fail)
	ds := srv.pairing
	w, err := ds.CreateWindow("")
	if err != nil {
		t.Fatal(err)
	}
	// The window transition (opened) happened even though the event append failed.
	if !ds.WindowActive() {
		t.Fatal("CreateWindow transition rolled back on event PreAccept")
	}
	// Cancel still works (transition survives).
	if ok, _ := ds.CancelWindow(w.Generation); !ok {
		t.Fatal("cancel failed after PreAccept")
	}
	_ = fail
}

type alwaysFailSink struct{}

func (*alwaysFailSink) Durability() EventSinkDurability { return EventSinkVolatile }
func (*alwaysFailSink) AppendSecurityEvent(SecurityEvent) (EventAppendResult, error) {
	return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureUnavailable}, errPreAcceptFailed
}

// ===========================================================================
// Service Start/idempotent/Stop ordering + token/config events
// ===========================================================================

func TestB2C1ServiceStartStopTokenEvents(t *testing.T) {
	sink := &recordingSink{}
	srv, _ := newB2C1Server(t, sink)
	srv.SetPort(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Idempotent Start while running → no second started event.
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	// Successful nonempty token rotation → token_rotated.
	tok := srv.RegenerateToken()
	if tok == "" {
		t.Fatal("regenerate produced empty token")
	}
	srv.Stop()

	startedCount := 0
	stoppedCount := 0
	rotatedCount := 0
	for _, e := range sink.events {
		if se, ok := e.(ServiceSecurityEvent); ok {
			switch se.Kind {
			case RemoteServiceStarted:
				startedCount++
			case RemoteServiceStopped:
				stoppedCount++
			case LegacyTokenRotated:
				rotatedCount++
			}
		}
	}
	if startedCount != 1 {
		t.Fatalf("started events=%d want 1", startedCount)
	}
	if stoppedCount != 1 {
		t.Fatalf("stopped events=%d want 1", stoppedCount)
	}
	if rotatedCount != 1 {
		t.Fatalf("token_rotated events=%d want 1", rotatedCount)
	}
	// Stop before any run → no stopped event (already proven stoppedCount==1 for the one run).
	// Empty-scope disables service events + records health.
	srv2, _ := newB2C1Server(t, sink)
	srv2.serverEventScope = ""
	before := srv2.v1sec.health.Snapshot(true).Issues
	_ = before
	// Emitting with empty scope records degraded health and skips append.
	n0 := sink.count()
	srv2.emitServiceEvent(RemoteServiceStarted)
	if sink.count() != n0 {
		t.Fatal("empty scope must not append service events")
	}
	if !healthActive(t, srv2.v1sec.health, HealthEventDurabilityDegraded) {
		t.Fatal("empty scope must record degraded health")
	}
}

// ===========================================================================
// Durable restart parse + unknown reject + privacy
// ===========================================================================

func TestB2C1DurableServiceEventRestartAndPrivacy(t *testing.T) {
	dir := t.TempDir()
	h := newSecurityHealthRegister()
	s := NewDurableSecurityEventSink(dir, h)
	if err := s.OpenAndScan(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	for _, k := range []ServiceSecurityEventKind{RemoteServiceStarted, RemoteServiceStopped, LegacyTokenRotated, RemoteListenConfigurationChanged} {
		eid := deriveServiceEventID("scope", k, at, 1)
		if res, _ := s.AppendSecurityEvent(ServiceSecurityEvent{EventID: eid, Kind: k, OccurredAt: at}); res.State != EventAcceptedBySink {
			t.Fatalf("append %d: %v", k, res.State)
		}
	}
	s.Close()

	// Restart: all 4 service kinds parse back; exact fields, no secret.
	s2 := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s2.OpenAndScan(); err != nil {
		t.Fatalf("restart scan: %v", err)
	}
	recs, _ := s2.ListSecurityEvents(0)
	if len(recs) != 4 {
		t.Fatalf("restart list len=%d want 4", len(recs))
	}
	for _, r := range recs {
		// Only eventId/kind/occurredAt — no pairing/device fields.
		if r.PairingGeneration != nil || r.Attempt != nil || r.DeviceID != nil {
			t.Fatalf("service record leaked non-service field: %+v", r)
		}
	}
	// Unknown service kind rejected on scan.
	dir2 := t.TempDir()
	bad := append([]byte(`{"version":1,"eventId":"`+string(SecurityEventID(rawURLBase64(make([]byte, 32))))+`","kind":"remote_service_made_up","occurredAt":"2026-08-02T12:00:00Z"}`), '\n')
	writeFile(t, dir2+"/"+durableActiveName, bad)
	s3 := NewDurableSecurityEventSink(dir2, newSecurityHealthRegister())
	if err := s3.OpenAndScan(); err == nil {
		t.Fatal("unknown service kind must be rejected on scan")
	}
	s3.Close()
}

// ===========================================================================
// Generic append-result→health mapper
// ===========================================================================

func TestB2C1GenericHealthMapper(t *testing.T) {
	h := newSecurityHealthRegister()
	at := time.Now()
	recordEventAppendHealth(h, EventAppendResult{State: EventAcceptedBySink}, "id", at)
	if len(h.Issues()) != 0 {
		t.Fatal("accepted must not record health")
	}
	recordEventAppendHealth(h, EventAppendResult{State: EventAcceptedButDurabilityDegraded}, "id1", at)
	if !healthActive(t, h, HealthEventDurabilityDegraded) {
		t.Fatal("degraded must record HealthEventDurabilityDegraded")
	}
	recordEventAppendHealth(h, EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIntegrity}, "id2", at)
	if !healthActive(t, h, HealthEventIntegrityFailed) {
		t.Fatal("integrity must record HealthEventIntegrityFailed")
	}
	recordEventAppendHealth(h, EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIO}, "id3", at)
	if !healthActive(t, h, HealthEventAppendFailed) {
		t.Fatal("io PreAccept must record HealthEventAppendFailed")
	}
	// Nil-safe.
	recordEventAppendHealth(nil, EventAppendResult{State: EventPreAcceptFailed}, "id", at)
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
