package remote

// B1-leader-hardening tests (owner-transfer session). One focused test per
// leader finding 1-7, plus the full rotation crash-point matrix (finding 5).
// These close the exact repair contract in b1-leader-findings.md. No secret is
// printed; fault injection uses the private syncFn seam only.

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

// healthActive reports whether code is active in the register.
func healthActive(t *testing.T, h *securityHealthRegister, code SecurityHealthCode) bool {
	t.Helper()
	for _, iss := range h.Snapshot(true).Issues {
		if iss.Code == code && iss.Active {
			return true
		}
	}
	return false
}

// healthOccurrences returns the occurrence count for a code (0 if absent).
func healthOccurrences(t *testing.T, h *securityHealthRegister, code SecurityHealthCode) uint64 {
	t.Helper()
	for _, iss := range h.Snapshot(true).Issues {
		if iss.Code == code {
			return iss.Occurrences
		}
	}
	return 0
}

// distinctDevEvent builds a device event with a distinct EventID for distinct i
// (used to force rotation past a single 1MiB segment).
func distinctDevEvent(i int) DeviceSecurityEvent {
	idBytes := make([]byte, 32)
	binary.BigEndian.PutUint32(idBytes, uint32(i+1))
	dev := make([]byte, 16)
	binary.BigEndian.PutUint64(dev, uint64(i+1))
	return DeviceSecurityEvent{
		EventID:    SecurityEventID(rawURLBase64(idBytes)),
		Kind:       DevicePaired,
		OccurredAt: time.Date(2026, 8, 2, 12, 0, i%60, 0, time.UTC),
		DeviceID:   contract.DeviceID(rawURLBase64(dev)),
	}
}

// writeRotateMarker writes a canonical rotate marker for the given generation
// and content (size+sha256) into dir.
func writeRotateMarker(t *testing.T, dir string, gen int, content []byte) {
	t.Helper()
	digest := sha256.Sum256(content)
	m := rotateMarker{Version: 1, Generation: gen, Bytes: len(content), SHA256: hex.EncodeToString(digest[:])}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, durableMarkerName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// markerGone reports whether the rotate marker is absent.
func markerGone(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(dir, durableMarkerName))
	return errors.Is(err, fs.ErrNotExist)
}

// ===========================================================================
// Finding 1: strict scan — empty immutable archive must fail
// ===========================================================================

func TestFinding1EmptyImmutableArchiveFails(t *testing.T) {
	dir := t.TempDir()
	// Empty archive segment (an archive is only ever created by rotating a
	// non-empty active, so an empty archive is corrupt) + empty active.
	if err := os.WriteFile(filepath.Join(dir, durableArchivePrefix+"0"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, durableActiveName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err == nil {
		t.Fatal("empty immutable archive must fail scan (finding 1)")
	}
	s.Close()
}

// Finding 1: unknown kind rejected (exact enum only).
func TestFinding1UnknownKindRejected(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, durableActiveName)
	// "device_foo" is not a known kind → must fail even though structurally valid.
	line := []byte(`{"version":1,"eventId":"` + string(devEvent(1, "AAAAAAAAAAAAAAAAAAAAAA").EventID) + `","kind":"device_foo","occurredAt":"2026-08-02T12:00:00Z","deviceId":"AAAAAAAAAAAAAAAAAAAAAA"}` + "\n")
	if err := os.WriteFile(active, line, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err == nil {
		t.Fatal("unknown kind must fail strict scan (finding 1)")
	}
	s.Close()
}

// ===========================================================================
// Finding 3: ConfirmDurable no-rewrite path must update accounting
// ===========================================================================

func TestFinding3ConfirmNoRewriteUpdatesAccounting(t *testing.T) {
	s, dir := newTestDurable(t)
	// Inject a Sync fault: the line IS written to the fd, only Sync is uncertain
	// → degraded + pending retained, but commitLineLocked is NOT called, so
	// activeBytes/totalBytes are stale.
	s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
	ev := devEvent(11, "AAAAAAAAAAAAAAAAAAAAAA")
	if res, _ := s.AppendSecurityEvent(ev); res.State != EventAcceptedButDurabilityDegraded {
		t.Fatalf("expected degraded, got %v", res.State)
	}
	if s.activeBytes != 0 {
		t.Fatalf("pre-confirm activeBytes=%d want 0", s.activeBytes)
	}
	// Restore real Sync; ConfirmDurable promotes via read-back (no rewrite).
	s.syncFn = func(f *os.File) error { return f.Sync() }
	if _, err := s.ConfirmDurable(ev.EventID); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// In-memory accounting must now equal the on-disk active size.
	fi, err := os.Stat(filepath.Join(dir, durableActiveName))
	if err != nil {
		t.Fatal(err)
	}
	if s.activeBytes != fi.Size() {
		t.Fatalf("post-confirm activeBytes=%d != on-disk size %d (finding 3)", s.activeBytes, fi.Size())
	}
	if s.totalBytes != fi.Size() {
		t.Fatalf("post-confirm totalBytes=%d != on-disk size %d (finding 3)", s.totalBytes, fi.Size())
	}
	s.Close()
}

// ===========================================================================
// Finding 4: Close must report an unreconciled pending entry
// ===========================================================================

func TestFinding4CloseReportsUnreconciledPending(t *testing.T) {
	h := newSecurityHealthRegister()
	s := NewDurableSecurityEventSink(t.TempDir(), h)
	if err := s.OpenAndScan(); err != nil {
		t.Fatal(err)
	}
	// Sync fault → degraded + pending retained (records health once during Append).
	s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
	ev := devEvent(12, "AAAAAAAAAAAAAAAAAAAAAA")
	if res, _ := s.AppendSecurityEvent(ev); res.State != EventAcceptedButDurabilityDegraded {
		t.Fatalf("expected degraded, got %v", res.State)
	}
	occAfterAppend := healthOccurrences(t, h, HealthEventDurabilityDegraded)
	// Restore real Sync so Close's own Sync succeeds — this isolates the pending
	// path from the Sync-failure health path.
	s.syncFn = func(f *os.File) error { return f.Sync() }
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close must record degraded health AGAIN for the unreconciled pending entry
	// (finding 4), distinct from the Append-time record.
	occAfterClose := healthOccurrences(t, h, HealthEventDurabilityDegraded)
	if occAfterClose <= occAfterAppend {
		t.Fatalf("Close did not report unreconciled pending: occurrences %d->%d (finding 4)", occAfterAppend, occAfterClose)
	}
}

// Finding 4: Close reports a Sync failure (already handled; confirmed here).
func TestFinding4CloseReportsSyncFailure(t *testing.T) {
	h := newSecurityHealthRegister()
	s := NewDurableSecurityEventSink(t.TempDir(), h)
	if err := s.OpenAndScan(); err != nil {
		t.Fatal(err)
	}
	s.syncFn = func(*os.File) error { return errors.New("sync unavailable") }
	if err := s.Close(); err == nil {
		t.Fatal("Close must surface Sync error (finding 4)")
	}
	if !healthActive(t, h, HealthEventDurabilityDegraded) {
		t.Fatal("Close must record HealthEventDurabilityDegraded on Sync failure (finding 4)")
	}
}

// ===========================================================================
// Finding 5: rotation crash-point matrix (every crash point converges)
// ===========================================================================

// Real rotation produces an archive and leaves NO leftover marker.
func TestFinding5RealRotationNoLeftoverMarker(t *testing.T) {
	s, dir := newTestDurable(t)
	// No-op Sync: the rotation STATE MACHINE (marker/rename/reconcile) is
	// independent of Sync semantics; durability is covered by the fault tests.
	s.syncFn = func(*os.File) error { return nil }
	rotated := false
	for i := 0; i < 12000; i++ {
		ev := distinctDevEvent(i)
		res, _ := s.AppendSecurityEvent(ev)
		if res.State == EventPreAcceptFailed {
			break // hit a cap
		}
		if !rotated {
			if _, err := os.Lstat(filepath.Join(dir, durableArchivePrefix+"0")); err == nil {
				rotated = true
			}
		}
	}
	s.Close()
	if !rotated {
		t.Fatal("test setup failed: rotation never triggered (distinct events too small?)")
	}
	// No leftover marker after a completed rotation.
	if !markerGone(t, dir) {
		t.Fatal("leftover rotate marker after successful rotation (finding 5)")
	}
	// Reopen: scan succeeds, archive + active reconciled.
	s2 := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s2.OpenAndScan(); err != nil {
		t.Fatalf("reopen after rotation: %v", err)
	}
	s2.Close()
}

// Crash point: after active Sync, BEFORE marker write → no marker, normal scan.
func TestFinding5CrashBeforeMarkerNormalScan(t *testing.T) {
	dir := t.TempDir()
	content := append(canonicalOrFatal(t, devEvent(20, "AAAAAAAAAAAAAAAAAAAAAA")), '\n')
	if err := os.WriteFile(filepath.Join(dir, durableActiveName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	// No marker on disk.
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err != nil {
		t.Fatalf("normal scan (crash before marker): %v", err)
	}
	if len(func() []SecurityEventRecord { r, _ := s.ListSecurityEvents(0); return r }()) != 1 {
		t.Fatalf("expected 1 event, got %d", len(func() []SecurityEventRecord { r, _ := s.ListSecurityEvents(0); return r }()))
	}
	if s.archiveCount != 0 {
		t.Fatalf("archiveCount=%d want 0", s.archiveCount)
	}
	s.Close()
}

// Crash point: after marker write, BEFORE rename → old-only (active non-empty,
// archive absent) → resume rename.
func TestFinding5CrashOldOnlyReconciles(t *testing.T) {
	dir := t.TempDir()
	content := append(canonicalOrFatal(t, devEvent(21, "AAAAAAAAAAAAAAAAAAAAAA")), '\n')
	if err := os.WriteFile(filepath.Join(dir, durableActiveName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	writeRotateMarker(t, dir, 0, content)
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err != nil {
		t.Fatalf("old-only reconcile: %v", err)
	}
	// active renamed to archive0, new empty active created, marker gone.
	if _, err := os.Lstat(filepath.Join(dir, durableArchivePrefix+"0")); err != nil {
		t.Fatalf("archive0 missing after old-only reconcile: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, durableActiveName))
	if err != nil || fi.Size() != 0 {
		t.Fatalf("active not empty after old-only reconcile (size=%v err=%v)", fi, err)
	}
	if !markerGone(t, dir) {
		t.Fatal("marker not removed after old-only reconcile")
	}
	if s.archiveCount != 1 {
		t.Fatalf("archiveCount=%d want 1", s.archiveCount)
	}
	s.Close()
}

// Crash point: after rename, BEFORE new active → archive-only (active absent,
// archive present) → create new active.
func TestFinding5CrashArchiveOnlyReconciles(t *testing.T) {
	dir := t.TempDir()
	content := append(canonicalOrFatal(t, devEvent(22, "AAAAAAAAAAAAAAAAAAAAAA")), '\n')
	if err := os.WriteFile(filepath.Join(dir, durableArchivePrefix+"0"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	writeRotateMarker(t, dir, 0, content)
	// No active.
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err != nil {
		t.Fatalf("archive-only reconcile: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, durableActiveName)); err != nil {
		t.Fatalf("active not created after archive-only reconcile: %v", err)
	}
	if !markerGone(t, dir) {
		t.Fatal("marker not removed after archive-only reconcile")
	}
	s.Close()
}

// Crash point: after new active create, BEFORE marker remove → archive +
// empty-new-active → remove marker.
func TestFinding5CrashArchiveEmptyActiveReconciles(t *testing.T) {
	dir := t.TempDir()
	content := append(canonicalOrFatal(t, devEvent(23, "AAAAAAAAAAAAAAAAAAAAAA")), '\n')
	if err := os.WriteFile(filepath.Join(dir, durableArchivePrefix+"0"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, durableActiveName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeRotateMarker(t, dir, 0, content)
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err != nil {
		t.Fatalf("archive+empty-active reconcile: %v", err)
	}
	if !markerGone(t, dir) {
		t.Fatal("marker not removed after archive+empty-active reconcile")
	}
	s.Close()
}

// Crash point: ambiguous states fail closed.
func TestFinding5CrashAmbiguousStatesFail(t *testing.T) {
	content := append(canonicalOrFatal(t, devEvent(24, "AAAAAAAAAAAAAAAAAAAAAA")), '\n')
	cases := []struct {
		name   string
		setdir func(t *testing.T, dir string)
	}{
		{
			name: "both-old(active-nonempty+archive)",
			setdir: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, durableActiveName), content, 0o600)
				os.WriteFile(filepath.Join(dir, durableArchivePrefix+"0"), content, 0o600)
				writeRotateMarker(t, dir, 0, content)
			},
		},
		{
			name: "neither(marker-only)",
			setdir: func(t *testing.T, dir string) {
				writeRotateMarker(t, dir, 0, content)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setdir(t, dir)
			s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
			if err := s.OpenAndScan(); err == nil {
				t.Fatalf("ambiguous marker state %q must fail closed", c.name)
			}
			s.Close()
		})
	}
}

// Crash point: marker hash mismatch → fail closed.
func TestFinding5CrashMarkerHashMismatchFails(t *testing.T) {
	dir := t.TempDir()
	content := append(canonicalOrFatal(t, devEvent(25, "AAAAAAAAAAAAAAAAAAAAAA")), '\n')
	other := append(canonicalOrFatal(t, devEvent(26, altDevID())), '\n')
	os.WriteFile(filepath.Join(dir, durableArchivePrefix+"0"), other, 0o600) // archive != marker content
	writeRotateMarker(t, dir, 0, content)
	s := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s.OpenAndScan(); err == nil {
		t.Fatal("marker/archive hash mismatch must fail closed")
	}
	s.Close()
}

// Finding 5 accounting: after rotation, segment count + totals are consistent
// across restart.
func TestFinding5RotationAccountingConsistent(t *testing.T) {
	s, dir := newTestDurable(t)
	s.syncFn = func(*os.File) error { return nil }
	for i := 0; i < 12000; i++ {
		ev := distinctDevEvent(i + 5000)
		res, _ := s.AppendSecurityEvent(ev)
		if res.State == EventPreAcceptFailed {
			break
		}
	}
	preCount := s.archiveCount
	preTotal := s.totalBytes
	s.Close()
	// Reopen: archive count + total must be reconstructed identically.
	s2 := NewDurableSecurityEventSink(dir, newSecurityHealthRegister())
	if err := s2.OpenAndScan(); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if s2.archiveCount != preCount {
		t.Fatalf("archiveCount after restart=%d want %d", s2.archiveCount, preCount)
	}
	if s2.totalBytes != preTotal {
		t.Fatalf("totalBytes after restart=%d want %d", s2.totalBytes, preTotal)
	}
	s2.Close()
}

// ===========================================================================
// Finding 6: sink corruption must not disable store authority
// ===========================================================================

func TestFinding6CorruptEventLogKeepsStoreAuthority(t *testing.T) {
	dir := t.TempDir()
	// Corrupt active event log (a complete but unparseable line).
	if err := os.WriteFile(filepath.Join(dir, durableActiveName), []byte("garbage line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := NewProductionSecurityOptions(dir, validHostSummary)
	srv := NewServerWithSecurity(0, nil, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	// LoadSecurityState must NOT fail; the store loads (authority intact) while
	// the sink stays unavailable (finding 6).
	if err := srv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	if !srv.securityReady() {
		t.Fatal("store must remain ready with corrupt event log (finding 6)")
	}
	// Health records the integrity failure.
	if !healthActive(t, srv.v1sec.health, HealthEventIntegrityFailed) {
		t.Fatal("integrity health not recorded for corrupt event log (finding 6)")
	}
	// The durable sink is unavailable for appends (PreAccept/Unavailable).
	if srv.durableSink == nil {
		t.Fatal("durable sink not wired")
	}
	res, _ := srv.durableSink.AppendSecurityEvent(devEvent(30, "AAAAAAAAAAAAAAAAAAAAAA"))
	if res.State != EventPreAcceptFailed || res.Failure != EventFailureUnavailable {
		t.Fatalf("sink append state=%v failure=%v, want PreAccept/Unavailable (finding 6)", res.State, res.Failure)
	}
	if err := srv.CloseSecurityState(); err != nil {
		t.Fatalf("CloseSecurityState: %v", err)
	}
}

// ===========================================================================
// Finding 7: torn-tail health is Event durability (not store); production uses
// the durable sink.
// ===========================================================================

func TestFinding7TornTailHealthIsEventDurabilityDegraded(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, durableActiveName)
	ev := devEvent(40, "AAAAAAAAAAAAAAAAAAAAAA")
	canonical := canonicalOrFatal(t, ev)
	complete := append(canonical, '\n')
	torn := []byte(`{"version":1,"eventId":"half`)
	if err := os.WriteFile(active, append(complete, torn...), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newSecurityHealthRegister()
	s := NewDurableSecurityEventSink(dir, h)
	if err := s.OpenAndScan(); err != nil {
		t.Fatalf("torn recover: %v", err)
	}
	if !healthActive(t, h, HealthEventDurabilityDegraded) {
		t.Fatal("torn tail must record HealthEventDurabilityDegraded (finding 7)")
	}
	if healthActive(t, h, HealthStoreDurabilityDegraded) {
		t.Fatal("torn tail must NOT record store durability (finding 7)")
	}
	s.Close()
}

// Finding 7: the production factory yields a DURABLE sink (not volatile).
func TestFinding7ProductionFactoryIsDurable(t *testing.T) {
	dir := t.TempDir()
	opts := NewProductionSecurityOptions(dir, validHostSummary)
	sink := opts.sinkFactory(newSecurityHealthRegister())
	if sink.Durability() != EventSinkDurable {
		t.Fatalf("production sink durability=%v, want Durable (finding 7)", sink.Durability())
	}
}

// canonicalOrFatal returns the canonical bytes for e or fails the test.
func canonicalOrFatal(t *testing.T, e SecurityEvent) []byte {
	t.Helper()
	b, err := canonicalizeSecurityEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
