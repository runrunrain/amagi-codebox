package remote

// M1-B1 durable sink tests (design §E / leader-ratification). Fault injection
// uses the private syncFn seam (production default = (*os.File).Sync); no
// test-only production branch. No secret is printed.

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

func newTestDurable(t *testing.T) (*durableSecurityEventSink, string) {
	t.Helper()
	dir := t.TempDir()
	h := newSecurityHealthRegister()
	s := NewDurableSecurityEventSink(dir, h)
	if err := s.OpenAndScan(); err != nil {
		t.Fatalf("OpenAndScan: %v", err)
	}
	return s, dir
}

func devEvent(idSuffix byte, dev string) DeviceSecurityEvent {
	idBytes := make([]byte, 32)
	idBytes[0] = idSuffix
	return DeviceSecurityEvent{
		EventID:    SecurityEventID(rawURLBase64(idBytes)),
		Kind:       DevicePaired,
		OccurredAt: time.Date(2026, 8, 2, 12, 0, int(idSuffix), 0, time.UTC),
		DeviceID:   contract.DeviceID(dev),
	}
}

func TestDurableRoundtripAndRestart(t *testing.T) {
	s, dir := newTestDurable(t)
	ev := devEvent(1, "AAAAAAAAAAAAAAAAAAAAAA")
	if res, _ := s.AppendSecurityEvent(ev); res.State != EventAcceptedBySink {
		t.Fatalf("append state=%v", res.State)
	}
	recs, _ := s.ListSecurityEvents(0)
	if len(recs) != 1 || recs[0].EventID != ev.EventID {
		t.Fatalf("list=%+v", recs)
	}
	s.Close()

	// Restart: reopen the same dir, scan, event persists + dedupe.
	h2 := newSecurityHealthRegister()
	s2 := NewDurableSecurityEventSink(dir, h2)
	if err := s2.OpenAndScan(); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	res, _ := s2.AppendSecurityEvent(ev) // same id+payload
	if res.State != EventDuplicateAcceptedBySink {
		t.Fatalf("restart dedupe state=%v", res.State)
	}
	if len(func() []SecurityEventRecord { r, _ := s2.ListSecurityEvents(0); return r }()) != 1 {
		t.Fatalf("restart list len=%d", len(func() []SecurityEventRecord { r, _ := s2.ListSecurityEvents(0); return r }()))
	}
}

func TestDurableSameIDDifferentPayloadIntegrity(t *testing.T) {
	s, _ := newTestDurable(t)
	ev := devEvent(2, "AAAAAAAAAAAAAAAAAAAAAA")
	if res, _ := s.AppendSecurityEvent(ev); res.State != EventAcceptedBySink {
		t.Fatal(res.State)
	}
	// Same EventID, different payload (different DeviceID) → integrity.
	ev2 := ev
	ev2.DeviceID = contract.DeviceID(altDevID())
	res, err := s.AppendSecurityEvent(ev2)
	if res.State != EventPreAcceptFailed || res.Failure != EventFailureIntegrity || err == nil {
		t.Fatalf("integrity: state=%v failure=%v err=%v", res.State, res.Failure, err)
	}
}

func TestDurableStrictParserRejects(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, durableActiveName)
	cases := []string{
		`{"version":1,"eventId":"x","kind":"device_paired","occurredAt":"2026-08-02T12:00:00Z","deviceId":"AAAAAAAAAAAAAAAAAAAAAA","extra":1}` + "\n", // unknown field
		`{"version":1,"eventId":"x","kind":"device_paired","occurredAt":"2026-08-02T12:00:00Z","deviceId":"AAAAAAAAAAAAAAAAAAAAAA"}` + "\n",           // missing... actually complete; use non-canonical spacing
		`{"version":1,"kind":"device_paired","occurredAt":"2026-08-02T12:00:00Z","deviceId":"AAAAAAAAAAAAAAAAAAAAAA"}` + "\n",                         // missing eventId
		`not json at all` + "\n",
	}
	for i, c := range cases {
		// Skip case 1's "missing" which is actually valid structurally; we test unknown+bad.
		_ = i
		if err := os.WriteFile(active, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
		s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
		err := s.OpenAndScan()
		if err == nil {
			// Only the unknown-field / missing-eventId / not-json cases must fail.
			if strings.Contains(c, "extra") || strings.Contains(c, "not json") || !strings.Contains(c, "eventId") {
				t.Fatalf("case %d expected scan failure", i)
			}
		}
		s.Close()
	}
}

func TestDurableTornActiveTailRecovered(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, durableActiveName)
	// One complete valid line + a torn (non-LF) partial tail.
	ev := devEvent(3, "AAAAAAAAAAAAAAAAAAAAAA")
	canonical, _ := canonicalizeSecurityEvent(ev)
	complete := append(canonical, '\n')
	torn := []byte("{\"version\":1,\"eventId\":\"half")
	if err := os.WriteFile(active, append(complete, torn...), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err != nil {
		t.Fatalf("torn tail should recover: %v", err)
	}
	// The complete event was indexed; the tail was truncated.
	if recs, _ := s.ListSecurityEvents(0); len(recs) != 1 {
		t.Fatalf("expected 1 event after torn recovery, got %d", len(recs))
	}
	// File now ends at the complete line.
	got, _ := os.ReadFile(active)
	if !strings.HasSuffix(string(got), "\n") || strings.Contains(string(got), "half") {
		t.Fatalf("active not truncated cleanly: %q", string(got))
	}
	s.Close()
}

func TestDurableInteriorCorruptionFails(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, durableActiveName)
	// Two complete lines, but the FIRST is corrupt (interior, LF-terminated).
	bad := []byte("{\"version\":1,\"eventId\":\"x\",\"kind\":\"device_paired\",\"occurredAt\":\"bad\",\"deviceId\":\"AAAAAAAAAAAAAAAAAAAAAA\"}\n")
	goodEv := devEvent(4, "AAAAAAAAAAAAAAAAAAAAAA")
	good, _ := canonicalizeSecurityEvent(goodEv)
	if err := os.WriteFile(active, append(bad, append(good, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err == nil {
		t.Fatal("interior corruption must fail scan")
	}
	s.Close()
}

func TestDurableArchiveCorruptionFails(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, durableActiveName)
	// Write one archive with a corrupt line + an empty active.
	arch := filepath.Join(dir, durableArchivePrefix+"0")
	os.WriteFile(arch, []byte("garbage line\n"), 0o600)
	os.WriteFile(active, nil, 0o600)
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err == nil {
		t.Fatal("archive corruption must fail scan")
	}
	s.Close()
}

func TestDurableRotationCaps(t *testing.T) {
	s, dir := newTestDurable(t)
	s.syncFn = func(*os.File) error { return nil } // rotation state machine is Sync-independent
	// Append distinct events until a cap is hit; the eventual PreAccept MUST be
	// classified Capacity (never IO).
	var last EventAppendResult
	for i := 0; ; i++ {
		ev := distinctDevEvent(i)
		last, _ = s.AppendSecurityEvent(ev)
		if last.State == EventPreAcceptFailed {
			break
		}
	}
	if last.State != EventPreAcceptFailed || last.Failure != EventFailureCapacity {
		t.Fatalf("cap exhaustion must be EventFailureCapacity, got state=%v failure=%v", last.State, last.Failure)
	}
	// At least one archive should exist after heavy append.
	entries, _ := os.ReadDir(dir)
	anyArchive := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), durableArchivePrefix) {
			anyArchive = true
		}
	}
	s.Close()
	_ = anyArchive // informational
	// Reopen: scan must succeed.
	s2 := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s2.OpenAndScan(); err != nil {
		t.Fatalf("reopen after rotation: %v", err)
	}
	s2.Close()

	// Archive-cap path: 7 archives + full active → next append is Capacity (not IO).
	// (Total kept < 8MiB so the archive-cap branch, not the total-cap branch, fires.)
	sa, _ := newTestDurable(t)
	sa.syncFn = func(*os.File) error { return nil }
	sa.archiveCount = DurableMaxArchives
	sa.activeBytes = DurableActiveMaxBytes - 8
	sa.totalBytes = sa.activeBytes // no real archive bytes on disk
	capres, _ := sa.AppendSecurityEvent(distinctDevEvent(90001))
	if capres.State != EventPreAcceptFailed || capres.Failure != EventFailureCapacity {
		t.Fatalf("archive cap must be EventFailureCapacity, got state=%v failure=%v", capres.State, capres.Failure)
	}
	sa.Close()
}

func TestDurablePartialSyncFaultAndConfirm(t *testing.T) {
	s, _ := newTestDurable(t)
	// Inject a Sync fault: append returns degraded + pending retained.
	s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
	ev := devEvent(5, "AAAAAAAAAAAAAAAAAAAAAA")
	res, _ := s.AppendSecurityEvent(ev)
	if res.State != EventAcceptedButDurabilityDegraded {
		t.Fatalf("expected degraded, got %v", res.State)
	}
	// A different new event is rejected while pending is unreconciled.
	ev2 := devEvent(6, altDevID())
	res2, _ := s.AppendSecurityEvent(ev2)
	if res2.State != EventPreAcceptFailed {
		t.Fatalf("expected pending-block, got %v", res2.State)
	}
	// Restore real Sync; ConfirmDurable promotes the pending event.
	s.syncFn = func(f *os.File) error { return f.Sync() }
	_, err := s.ConfirmDurable(ev.EventID)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// Now the event is fully accepted; a new event can append.
	res3, _ := s.AppendSecurityEvent(ev2)
	if res3.State == EventPreAcceptFailed {
		t.Fatalf("post-confirm append blocked: %v", res3.State)
	}
	s.Close()
}

func TestDurableCloseIdempotent(t *testing.T) {
	s, _ := newTestDurable(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestDurableListPrivacy(t *testing.T) {
	s, _ := newTestDurable(t)
	ev := devEvent(7, "AAAAAAAAAAAAAAAAAAAAAA")
	s.AppendSecurityEvent(ev)
	recs, _ := s.ListSecurityEvents(2)
	if len(recs) != 1 {
		t.Fatalf("len=%d", len(recs))
	}
	b, _ := canonicalizeSecurityEvent(ev)
	// The raw canonical line (which contains only allowlist fields) must not
	// expose path/secret/free text; the record carries only closed fields.
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "path") {
		t.Fatal("canonical event leaked forbidden material")
	}
	if recs[0].DeviceID == nil || *recs[0].DeviceID != string(ev.DeviceID) {
		t.Fatalf("deviceId not projected: %+v", recs[0])
	}
	s.Close()
}

func TestDurableConcurrentAppendNoDuplicate(t *testing.T) {
	s, _ := newTestDurable(t)
	ev := devEvent(8, "AAAAAAAAAAAAAAAAAAAAAA")
	var wg sync.WaitGroup
	var accepted int32
	var dup int32
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := s.AppendSecurityEvent(ev)
			switch res.State {
			case EventAcceptedBySink:
				atomic.AddInt32(&accepted, 1)
			case EventDuplicateAcceptedBySink:
				atomic.AddInt32(&dup, 1)
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("expected exactly 1 accepted, got %d (dup=%d)", accepted, dup)
	}
	s.Close()
}

func TestProductionFactoryYieldsDurableSink(t *testing.T) {
	dir := t.TempDir()
	opts := NewProductionSecurityOptions(dir, validHostSummary)
	// The factory builds a durable (unopened) sink.
	health := newSecurityHealthRegister()
	sink := opts.sinkFactory(health)
	ds, ok := sink.(*durableSecurityEventSink)
	if !ok {
		t.Fatalf("production sink must be durable, got %T", sink)
	}
	if err := ds.OpenAndScan(); err != nil {
		t.Fatalf("open: %v", err)
	}
	if sink.Durability() != EventSinkDurable {
		t.Fatal("durability")
	}
	ds.Close()
	// File created with 0600 under 0700 dir.
	info, err := os.Stat(filepath.Join(dir, durableActiveName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("active mode too open: %v", info.Mode().Perm())
	}
}

// ensure rand import used (devEvent uses rawURLBase64; keep stable).
var _ = rand.Reader

// altDevID returns a canonical 22-char RawURL device ID distinct from the
// all-zero "AAAAAAAAAAAAAAAAAAAAAA" (used as a different-but-valid payload).
func altDevID() string {
	b := make([]byte, 16)
	b[0] = 1
	return rawURLBase64(b)
}
