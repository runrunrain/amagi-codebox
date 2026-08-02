package remote

// Durable security event sink (design §E / leader-ratification R-M1B-02, hardened
// per Leader B1 findings). A lazy strict-canonical JSONL sink over
// <configDir>/remote-events.log with monotonic archives and a rotate marker.
//
// Caps (R-M1B-02): each active/archive segment ≤ 1MiB; at most 8 segments
// (active + 7 archives) / 8MiB total; 16,384 EventIDs. No eviction/drop-oldest;
// capacity exhaustion is a typed PreAccept failure + health.
//
// Append four-state ownership:
//   - pre-write (canonicalize/validate/capacity/rotation/open) failure, or a
//     write that wrote zero bytes → PreAccept; the sink does NOT retain payload.
//   - a write that wrote ≥1 byte (partial/full) or an uncertain Sync → the sink
//     retains the pending canonical payload by EventID and returns
//     AcceptedButDurabilityDegraded; the caller never re-sends. While a pending
//     entry is unreconciled the sink rejects new (different) events to avoid
//     gaps; a same-ID/same-payload re-drive returns degraded without rewriting.
//   - a full line + successful Sync → Accepted; the global EventID index +
//     projection are updated.
//   - duplicate-before-cap: an already-accepted same-ID/same-payload line returns
//     DuplicateAccepted even at capacity; same-ID/different-payload is a sticky
//     integrity failure.
//
// ConfirmDurable promotes only the target pending entry (or no-ops an already
// accepted id when no OTHER pending exists); it never clears another pending.
// Rotation uses a strict rotate marker so any single crash point converges on
// restart. Close is idempotent and surfaces Sync errors.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

const (
	durableActiveName    = "remote-events.log"
	durableArchivePrefix = durableActiveName + "."
	durableMarkerName    = "remote-events.rotate"
	// durableTornName is the quarantine target for a truncated active tail. It is
	// deliberately OUTSIDE the durableArchivePrefix namespace ("remote-events.log.")
	// so it is never mistaken for an archive segment.
	durableTornName = "remote-events.torn"
	// DurableActiveMaxBytes bounds a single active/archive segment (R-M1B-02: 1MiB).
	DurableActiveMaxBytes = 1 << 20
	// DurableMaxArchives bounds the number of archive segments (7 → 8 total).
	DurableMaxArchives = 7
	// DurableMaxTotalBytes bounds active + all archives (R-M1B-02: 8MiB).
	DurableMaxTotalBytes = 8 << 20
	// DurableMaxEventIDs bounds the global EventID index (R-M1B-02: 16,384).
	DurableMaxEventIDs = 1 << 14
)

// SecurityEventRecord is the sanitized durable-event projection (no raw line,
// path, secret or free text). Explicit json tags; pointer fields omitempty.
type SecurityEventRecord struct {
	EventID           SecurityEventID `json:"eventId"`
	Kind              string          `json:"kind"`
	OccurredAt        string          `json:"occurredAt"`
	PairingGeneration *uint64         `json:"pairingGeneration,omitempty"`
	Attempt           *uint8          `json:"attempt,omitempty"`
	DeviceID          *string         `json:"deviceId,omitempty"`
	Carrier           *string         `json:"carrier,omitempty"`
	RouteClass        *string         `json:"routeClass,omitempty"`
	Outcome           *string         `json:"outcome,omitempty"`
}

type pendingDurable struct {
	id         SecurityEventID
	line       []byte // canonical + LF
	offset     int64
	acceptedAt time.Time
}

type acceptedDurable struct {
	id         SecurityEventID
	line       []byte
	acceptedAt time.Time
}

type durableSecurityEventSink struct {
	mu     sync.Mutex
	dir    string
	health *securityHealthRegister

	opened       bool
	closed       bool
	openErr      error // sticky: if non-nil, Append fails unavailable and OpenAndScan is not retried
	f            *os.File
	syncFn       func(*os.File) error                // production: (*os.File).Sync; seam for fault injection
	writeFn      func(*os.File, []byte) (int, error) // production: writeFull; seam for zero/partial injection
	readFileFn   func(string) ([]byte, error)        // production: os.ReadFile; seam for ConfirmDurable read-failure injection
	renameFn     func(string, string) error          // production: os.Rename; seam for rename-error readback injection
	removeFn     func(string) error                  // production: os.Remove; seam for cleanup-failure injection
	activeBytes  int64
	totalBytes   int64 // active + all archives (actual)
	archiveCount int
	nextArchive  int
	byID         map[SecurityEventID][]byte
	pending      *pendingDurable
	projection   []acceptedDurable
}

// NewDurableSecurityEventSink returns an UNOPENED durable sink. OpenAndScan must
// be called once before Append. health may be nil (best-effort recording).
func NewDurableSecurityEventSink(configDir string, health *securityHealthRegister) *durableSecurityEventSink {
	return &durableSecurityEventSink{
		dir: configDir, health: health, byID: make(map[SecurityEventID][]byte),
		syncFn:     func(f *os.File) error { return f.Sync() },
		writeFn:    writeFull,
		readFileFn: os.ReadFile,
		renameFn:   os.Rename,
		removeFn:   os.Remove,
	}
}

func (s *durableSecurityEventSink) Durability() EventSinkDurability { return EventSinkDurable }

// durableScanError categorizes an OpenAndScan failure as integrity (data
// corruption: parse/canonical/duplicate-diff/empty-archive/marker) vs
// IO/path/unavailable, so LoadSecurityState can map it to the right closed
// health code without latching the store.
type durableScanError struct {
	integrity bool
	msg       string
}

func (e *durableScanError) Error() string { return e.msg }

func integrityScanErr(msg string) error {
	return &durableScanError{integrity: true, msg: "durable sink: " + msg}
}

// isDurableIntegrityError reports whether err is a durable-sink integrity failure.
func isDurableIntegrityError(err error) bool {
	var dse *durableScanError
	return errors.As(err, &dse) && dse.integrity
}

func (s *durableSecurityEventSink) recordHealth(code SecurityHealthCode, id SecurityEventID, at time.Time) {
	if s.health != nil {
		s.health.Record(code, id, at)
	}
}

// activePath returns the absolute active-segment path.
func (s *durableSecurityEventSink) activePath() string {
	return filepath.Join(s.dir, durableActiveName)
}

func (s *durableSecurityEventSink) archivePath(gen int) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s%d", durableArchivePrefix, gen))
}

func (s *durableSecurityEventSink) markerPath() string {
	return filepath.Join(s.dir, durableMarkerName)
}

// OpenAndScan validates the config dir, reconciles any rotate marker, scans
// archives (ascending) then the active segment, rebuilds the global index, and
// truncates a single torn active tail. Any immutable/interior/duplicate-diff
// corruption fails closed (returns an error); the sink stays unavailable but the
// caller (LoadSecurityState) still loads the device store.
func (s *durableSecurityEventSink) OpenAndScan() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opened {
		return nil
	}
	if s.closed {
		return errors.New("durable sink: closed")
	}
	if s.openErr != nil {
		// Sticky: a prior failed open is not retried (no rescan / double-count).
		return s.openErr
	}
	if err := s.validateDirLocked(); err != nil {
		s.openErr = err
		return err
	}
	// Reconcile a rotate marker BEFORE scanning (crash convergence).
	if err := s.reconcileMarkerLocked(); err != nil {
		s.openErr = err
		return err
	}
	// Collect archives.
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		s.openErr = err
		return err
	}
	var archives []string
	for _, e := range entries {
		name := e.Name()
		if name == durableActiveName || name == durableMarkerName || name == durableTornName {
			continue
		}
		if strings.HasPrefix(name, durableArchivePrefix) {
			// A prefixed name that is not a valid numeric generation (or overflows)
			// is corruption — reject rather than silently ignore (requirement E).
			if _, ok := genNum(name); !ok {
				s.openErr = integrityScanErr("invalid archive name")
				return s.openErr
			}
			archives = append(archives, name)
		}
	}
	sort.Slice(archives, func(i, j int) bool {
		ai, _ := genNum(archives[i])
		aj, _ := genNum(archives[j])
		return ai < aj
	})
	if len(archives) > DurableMaxArchives {
		s.openErr = integrityScanErr("too many archives")
		return s.openErr
	}
	for _, an := range archives {
		full := filepath.Join(s.dir, an)
		if err := s.scanImmutableLocked(full); err != nil {
			s.openErr = err
			return err
		}
		g, _ := genNum(an)
		if g >= s.nextArchive {
			s.nextArchive = g + 1
		}
		s.archiveCount++
	}
	// Active.
	activePath := s.activePath()
	if err := s.scanActiveLocked(activePath); err != nil {
		s.openErr = err
		return err
	}
	// Open active for append (safe: no symlink follow, mode 0600 enforced).
	f, activeSize, err := s.openActiveSafeLocked(activePath)
	if err != nil {
		s.openErr = err
		return err
	}
	s.f = f
	s.activeBytes = activeSize
	s.totalBytes += activeSize
	if s.totalBytes > DurableMaxTotalBytes {
		f.Close()
		s.openErr = errors.New("durable sink: total bytes exceed cap")
		return s.openErr
	}
	s.opened = true
	s.openErr = nil
	return nil
}

// validateDirLocked ensures configDir is a real directory (no symlink/irregular)
// and tightens its mode to 0700.
func (s *durableSecurityEventSink) validateDirLocked() error {
	li, err := os.Lstat(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return os.MkdirAll(s.dir, 0o700)
		}
		return err
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return errors.New("durable sink: config dir is a symlink")
	}
	if !li.IsDir() {
		return errors.New("durable sink: config dir is not a directory")
	}
	if li.Mode().Perm() != 0o700 {
		if err := os.Chmod(s.dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// openActiveSafeLocked opens the active segment for append without following a
// symlink, enforcing regular + mode 0600. Returns the opened file + its size.
func (s *durableSecurityEventSink) openActiveSafeLocked(path string) (*os.File, int64, error) {
	li, lerr := os.Lstat(path)
	if lerr != nil && !errors.Is(lerr, fs.ErrNotExist) {
		return nil, 0, lerr
	}
	if lerr == nil {
		// Exists: must be regular, no symlink, mode exactly 0600.
		if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
			return nil, 0, errors.New("durable sink: active not regular")
		}
		if li.Mode().Perm() != 0o600 {
			return nil, 0, fmt.Errorf("durable sink: active mode %o not 0600", li.Mode().Perm())
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, 0, err
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, 0, err
		}
		if !os.SameFile(li, fi) {
			f.Close()
			return nil, 0, errors.New("durable sink: active file changed identity")
		}
		return f, fi.Size(), nil
	}
	// Truly absent (Lstat ErrNotExist, not a broken symlink): O_EXCL create.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, 0, err
	}
	return f, 0, nil
}

// scanImmutableLocked strictly validates an archive file; any bad line fails.
func (s *durableSecurityEventSink) scanImmutableLocked(path string) error {
	li, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return errors.New("durable sink: archive not regular")
	}
	if li.Mode().Perm() != 0o600 {
		return fmt.Errorf("durable sink: archive mode %o not 0600", li.Mode().Perm())
	}
	if li.Size() > DurableActiveMaxBytes {
		return errors.New("durable sink: archive segment exceeds 1MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		// An archive is only ever created by rotating a non-empty active, so an
		// empty immutable archive is corrupt (finding 1).
		return integrityScanErr("empty immutable archive")
	}
	s.totalBytes += int64(len(raw))
	if s.totalBytes > DurableMaxTotalBytes {
		return errors.New("durable sink: total bytes exceed cap")
	}
	lines, err := splitDurableLines(raw)
	if err != nil {
		return integrityScanErr("corrupt archive: " + err.Error())
	}
	for _, ln := range lines {
		rec, lerr := parseDurableLine(ln)
		if lerr != nil {
			return integrityScanErr("corrupt archive line: " + lerr.Error())
		}
		if err := s.indexLineLocked(rec, ln); err != nil {
			return err
		}
	}
	return nil
}

// scanActiveLocked validates the active file, tolerating exactly one final
// unterminated torn tail (quarantined + truncated + event-durability-degraded
// health). A missing active with ≤7 archives is recovered by creating an empty
// active (events live in archives).
func (s *durableSecurityEventSink) scanActiveLocked(path string) error {
	li, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if s.archiveCount > DurableMaxArchives {
				return errors.New("durable sink: active missing and too many archives")
			}
			return nil // openActiveSafeLocked will create it.
		}
		return err
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return errors.New("durable sink: active not regular")
	}
	if li.Size() > DurableActiveMaxBytes {
		return errors.New("durable sink: active segment exceeds 1MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	complete, torn := splitActiveTail(raw)
	cl, err := splitDurableLines(complete)
	if err != nil {
		return integrityScanErr("corrupt active: " + err.Error())
	}
	for _, ln := range cl {
		rec, lerr := parseDurableLine(ln)
		if lerr != nil {
			return integrityScanErr("corrupt active line: " + lerr.Error())
		}
		if err := s.indexLineLocked(rec, ln); err != nil {
			return err
		}
	}
	if len(torn) > 0 {
		if err := quarantineAndTruncate(path, len(raw)-len(torn), torn); err != nil {
			return err
		}
		s.recordHealth(HealthEventDurabilityDegraded, "", time.Now())
	}
	return nil
}

func (s *durableSecurityEventSink) indexLineLocked(rec SecurityEventRecord, line []byte) error {
	if len(s.byID) >= DurableMaxEventIDs {
		if _, ok := s.byID[rec.EventID]; !ok {
			return integrityScanErr("EventID cap exceeded at scan")
		}
	}
	if prev, ok := s.byID[rec.EventID]; ok {
		if !equalCanonical(prev, line) {
			return integrityScanErr("duplicate EventID with different payload")
		}
		return nil
	}
	s.byID[rec.EventID] = append([]byte(nil), line...)
	s.projection = append(s.projection, acceptedDurable{id: rec.EventID, line: append([]byte(nil), line...), acceptedAt: recTime(rec)})
	return nil
}

func recTime(rec SecurityEventRecord) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, rec.OccurredAt)
	return t
}

// AppendSecurityEvent implements the durable four-state append.
func (s *durableSecurityEventSink) AppendSecurityEvent(e SecurityEvent) (EventAppendResult, error) {
	canonical, err := canonicalizeSecurityEvent(e)
	if err != nil {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureInvalid}, errPreAcceptFailed
	}
	line := append(canonical, '\n')
	if len(line) > MaxSecurityEventCanonicalBytes+1 {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureInvalid}, errPreAcceptFailed
	}
	id := e.EventIDOf()

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.opened || s.closed || s.openErr != nil {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureUnavailable}, errPreAcceptFailed
	}

	// Duplicate-before-cap (already accepted).
	if prev, ok := s.byID[id]; ok {
		if equalCanonical(prev, canonical) {
			return EventAppendResult{State: EventDuplicateAcceptedBySink}, nil
		}
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIntegrity}, errIntegrityFailed
	}
	// Pending (degraded) handling.
	if s.pending != nil {
		if s.pending.id == id {
			if equalCanonical(s.pending.line[:len(s.pending.line)-1], canonical) {
				// Same pending event, same payload → stay degraded (no rewrite).
				return EventAppendResult{State: EventAcceptedButDurabilityDegraded}, nil
			}
			return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIntegrity}, errIntegrityFailed
		}
		// Different ID while pending unreconciled → reject to avoid a gap.
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureUnavailable}, errPreAcceptFailed
	}

	if len(s.byID) >= DurableMaxEventIDs {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureCapacity}, errPreAcceptFailed
	}
	// Total cap is checked BEFORE rotation: rotation cannot reduce total bytes
	// (it moves active→archive), so exhausting the total cap is always Capacity.
	if s.totalBytes+int64(len(line)) > DurableMaxTotalBytes {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureCapacity}, errPreAcceptFailed
	}
	// Rotate before append if the segment would overflow.
	if s.activeBytes+int64(len(line)) > DurableActiveMaxBytes {
		// Archive cap reached → cannot rotate; the active is full → Capacity (not IO).
		if s.archiveCount >= DurableMaxArchives {
			return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureCapacity}, errPreAcceptFailed
		}
		if err := s.rotateLocked(); err != nil {
			return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIO}, errPreAcceptFailed
		}
	}

	offset := s.activeBytes
	// Ownership: copy to pending BEFORE the write.
	s.pending = &pendingDurable{id: id, line: append([]byte(nil), line...), offset: offset, acceptedAt: e.OccurredAtOf()}

	n, werr := s.writeFn(s.f, line)
	if werr != nil && n == 0 {
		// Wrote nothing → PreAccept IO; clear pending, no retention.
		s.pending = nil
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIO}, errPreAcceptFailed
	}
	if werr != nil {
		// Partial write (n>0) → degraded, retain pending.
		s.recordHealth(HealthEventDurabilityDegraded, id, time.Now())
		return EventAppendResult{State: EventAcceptedButDurabilityDegraded}, nil
	}
	if serr := s.syncFn(s.f); serr != nil {
		s.recordHealth(HealthEventDurabilityDegraded, id, time.Now())
		return EventAppendResult{State: EventAcceptedButDurabilityDegraded}, nil
	}
	// Full line + Sync succeeded → accept.
	s.commitLineLocked(id, canonical, line, e.OccurredAtOf(), offset)
	s.pending = nil
	return EventAppendResult{State: EventAcceptedBySink}, nil
}

// commitLineLocked updates the index/projection/bytes for a fully-accepted line.
func (s *durableSecurityEventSink) commitLineLocked(id SecurityEventID, canonical, line []byte, at time.Time, offset int64) {
	s.byID[id] = append([]byte(nil), canonical...)
	s.projection = append(s.projection, acceptedDurable{id: id, line: append([]byte(nil), line...), acceptedAt: at})
	s.activeBytes += int64(len(line))
	s.totalBytes += int64(len(line))
}

// rotateLocked performs the marker-based rotation. Steps (any failure latches
// the sink unavailable and leaves no open writable fd):
// active Sync → marker O_EXCL write+Sync → parent Sync → target absent →
// rename + readback reconcile → parent Sync → new active O_EXCL+Sync →
// parent Sync → remove marker → parent Sync.
func (s *durableSecurityEventSink) rotateLocked() error {
	if s.archiveCount >= DurableMaxArchives {
		return errors.New("durable sink: archive cap reached")
	}
	gen := s.nextArchive
	activePath := s.activePath()
	archivePath := s.archivePath(gen)
	markerPath := s.markerPath()

	// 1. Sync active + capture its content digest.
	if err := s.syncFn(s.f); err != nil {
		s.latchLocked(err)
		return err
	}
	content, err := os.ReadFile(activePath)
	if err != nil {
		s.latchLocked(err)
		return err
	}
	digest := sha256.Sum256(content)
	marker := rotateMarker{Version: 1, Generation: gen, Bytes: len(content), SHA256: hex.EncodeToString(digest[:])}
	markerBytes, _ := json.Marshal(marker)

	// 2. Marker O_EXCL write + Sync + parent sync.
	mf, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		s.latchLocked(err)
		return err
	}
	if _, err := writeFull(mf, markerBytes); err != nil {
		mf.Close()
		s.latchLocked(err)
		return err
	}
	if err := s.syncFn(mf); err != nil {
		mf.Close()
		s.latchLocked(err)
		return err
	}
	if err := mf.Close(); err != nil {
		s.latchLocked(err)
		return err
	}
	if err := syncParentDir(s.dir); err != nil {
		s.latchLocked(err)
		return err
	}

	// 3. Target archive must be absent.
	if _, err := os.Lstat(archivePath); !errors.Is(err, fs.ErrNotExist) {
		s.latchLocked(errors.New("durable sink: rotate target exists"))
		return s.openErr
	}

	// 4. Close active fd (Close error must NOT be swallowed), rename with
	//    readback reconcile (do not blindly latch on an ambiguous OS error —
	//    read back the actual on-disk state first).
	if err := s.f.Close(); err != nil {
		s.f = nil
		s.latchLocked(err)
		return err
	}
	s.f = nil
	rr, rerr := s.renameActiveToArchiveLocked(activePath, archivePath, &marker)
	switch rr {
	case renameReadbackMoved:
		// archive in place + matches marker; continue below.
	case renameReadbackOldIntact:
		// Rename truly failed; old active intact. Abort cleanly: remove marker,
		// parent sync, safe reopen. ANY failure latches unavailable + f nil; only
		// full success keeps the sink usable and returns the original rename error.
		if err := s.removeFn(markerPath); err != nil {
			s.latchLocked(err)
			return err
		}
		if err := syncParentDir(s.dir); err != nil {
			s.latchLocked(err)
			return err
		}
		if err := s.reopenActiveSafeLocked(activePath); err != nil {
			s.latchLocked(err)
			return err
		}
		return rerr
	default: // renameReadbackAmbiguous
		s.latchLocked(rerr)
		return rerr
	}
	if err := syncParentDir(s.dir); err != nil {
		s.latchLocked(err)
		return err
	}

	// 5. New active O_EXCL + Sync + parent sync.
	nf, err := os.OpenFile(activePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.latchLocked(err)
		return err
	}
	if err := s.syncFn(nf); err != nil {
		nf.Close()
		s.latchLocked(err)
		return err
	}
	s.f = nf
	if err := syncParentDir(s.dir); err != nil {
		s.latchLocked(err)
		return err
	}

	// 6. Remove marker + parent sync.
	if err := s.removeFn(markerPath); err != nil {
		s.latchLocked(err)
		return err
	}
	if err := syncParentDir(s.dir); err != nil {
		s.latchLocked(err)
		return err
	}

	// Accounting: bytes moved active→archive (total unchanged); active now empty.
	s.archiveCount++
	s.nextArchive = gen + 1
	s.activeBytes = 0
	return nil
}

// verifyArchiveReconcile checks an installed archive matches its marker.
func (s *durableSecurityEventSink) verifyArchiveReconcile(archivePath string, m *rotateMarker) error {
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		return err
	}
	if len(raw) != m.Bytes {
		return integrityScanErr("archive size mismatch")
	}
	d := sha256.Sum256(raw)
	if hex.EncodeToString(d[:]) != m.SHA256 {
		return integrityScanErr("archive hash mismatch")
	}
	return nil
}

// renameReadback is the outcome of renameActiveToArchiveLocked.
type renameReadback int

const (
	renameReadbackMoved     renameReadback = iota // archive in place + matches marker
	renameReadbackOldIntact                       // old active still exists, archive absent (truly failed)
	renameReadbackAmbiguous                       // both/neither/mismatch — caller must fail
)

// renameActiveToArchiveLocked renames active→archive via the renameFn seam and
// readbacks the actual on-disk state before reporting. If the rename returned
// an error but the file actually moved and matches the marker, it reports Moved
// (so the caller can continue). It never blindly latches on an OS error.
func (s *durableSecurityEventSink) renameActiveToArchiveLocked(activePath, archivePath string, m *rotateMarker) (renameReadback, error) {
	rerr := s.renameFn(activePath, archivePath)
	if rerr == nil {
		if verr := s.verifyArchiveReconcile(archivePath, m); verr != nil {
			return renameReadbackAmbiguous, verr
		}
		return renameReadbackMoved, nil
	}
	activeExists, _, _ := pathState(activePath)
	archiveExists, _, _ := pathState(archivePath)
	switch {
	case !activeExists && archiveExists:
		// Rename actually moved despite the error: verify + continue.
		if verr := s.verifyArchiveReconcile(archivePath, m); verr != nil {
			return renameReadbackAmbiguous, verr
		}
		return renameReadbackMoved, nil
	case activeExists && !archiveExists:
		return renameReadbackOldIntact, rerr
	default:
		return renameReadbackAmbiguous, errors.New("durable sink: ambiguous rename state")
	}
}

// openActiveForAppendLocked is removed; the abort path now uses
// reopenActiveSafeLocked which validates the file is the expected regular 0600
// active with size == activeBytes (no blind reopen).
func (s *durableSecurityEventSink) reopenActiveSafeLocked(path string) error {
	li, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return errors.New("durable sink: active not regular on reopen")
	}
	if li.Mode().Perm() != 0o600 {
		return fmt.Errorf("durable sink: active mode %o not 0600 on reopen", li.Mode().Perm())
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !os.SameFile(li, fi) {
		f.Close()
		return errors.New("durable sink: active changed identity on reopen")
	}
	if fi.Size() != s.activeBytes {
		f.Close()
		return fmt.Errorf("durable sink: active size %d != activeBytes %d on reopen", fi.Size(), s.activeBytes)
	}
	s.f = f
	return nil
}

// latchLocked marks the sink unavailable after an unrecoverable rotation failure
// and closes any open writable fd.
func (s *durableSecurityEventSink) latchLocked(err error) {
	s.openErr = err
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
}

// reconcileMarkerLocked converges a crashed rotation before scanning. Accepts
// exactly: old-only (rename pending), archive-only (new active pending), or
// archive+empty-new-active (marker removal pending). No marker → normal scan.
func (s *durableSecurityEventSink) reconcileMarkerLocked() error {
	markerPath := s.markerPath()
	li, err := os.Lstat(markerPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() || li.Mode().Perm() != 0o600 || li.Size() > 1024 {
		return integrityScanErr("corrupt rotate marker")
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return err
	}
	var m rotateMarker
	if err := json.Unmarshal(raw, &m); err != nil {
		return integrityScanErr("invalid rotate marker")
	}
	if recan, _ := json.Marshal(m); !equalCanonical(recan, raw) {
		return integrityScanErr("rotate marker not canonical")
	}
	if err := m.validate(); err != nil {
		return err
	}
	activePath := s.activePath()
	archivePath := s.archivePath(m.Generation)
	activeExists, activeEmpty, aerr := pathState(activePath)
	archiveExists, _, arerr := pathState(archivePath)
	if aerr != nil {
		return aerr
	}
	if arerr != nil {
		return arerr
	}
	switch {
	case activeExists && !activeEmpty && !archiveExists:
		// old-only: resume rename with readback (do not blindly fail on OS error).
		rr, rerr := s.renameActiveToArchiveLocked(activePath, archivePath, &m)
		switch rr {
		case renameReadbackMoved:
			if err := syncParentDir(s.dir); err != nil {
				return err
			}
			return s.createNewActiveAndRemoveMarker(activePath, markerPath)
		case renameReadbackOldIntact:
			// old intact, archive absent → return error (old left intact for retry).
			return rerr
		default:
			return rerr
		}
	case !activeExists && archiveExists:
		// archive-only: create new active + remove marker.
		if err := s.verifyArchiveReconcile(archivePath, &m); err != nil {
			return err
		}
		return s.createNewActiveAndRemoveMarker(activePath, markerPath)
	case activeExists && activeEmpty && archiveExists:
		// archive+empty-new-active: marker removal pending.
		if err := s.verifyArchiveReconcile(archivePath, &m); err != nil {
			return err
		}
		if err := os.Remove(markerPath); err != nil {
			return err
		}
		// Final parent sync so the marker removal is durable (finding 5 protocol).
		return syncParentDir(s.dir)
	default:
		// both-old / neither / target mismatch → fail closed.
		return integrityScanErr("ambiguous rotate marker state")
	}
}

func (s *durableSecurityEventSink) createNewActiveAndRemoveMarker(activePath, markerPath string) error {
	nf, err := os.OpenFile(activePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := nf.Sync(); err != nil {
		nf.Close()
		return err
	}
	nf.Close()
	if err := syncParentDir(s.dir); err != nil {
		return err
	}
	if err := os.Remove(markerPath); err != nil {
		return err
	}
	// Final parent sync so the marker removal is durable (finding 5 protocol:
	// marker remove → parent Sync). Makes the recovery itself crash-convergent.
	return syncParentDir(s.dir)
}

// pathState reports existence + (for the active) whether it is empty.
func pathState(path string) (exists bool, empty bool, err error) {
	li, e := os.Lstat(path)
	if e != nil {
		if errors.Is(e, fs.ErrNotExist) {
			return false, false, nil
		}
		return false, false, e
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return false, false, errors.New("path not regular")
	}
	return true, li.Size() == 0, nil
}

// ConfirmDurable promotes only the target pending entry. It never clears
// another pending: confirming an already-accepted id while a DIFFERENT pending
// is unreconciled is an error. The read-back is strict: a read failure, an
// active shorter than the pending offset, or any bytes after the pending line
// that are not an exact prefix of the pending line → error + retain pending
// (never truncate/rewrite). A partial tail that IS an exact prefix is safely
// rewritten via the existing append fd (truncate+offset+write+sync). The
// on-disk line is only trusted when its tail is exactly the pending line.
func (s *durableSecurityEventSink) ConfirmDurable(id SecurityEventID) (EventDurabilityConfirmation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.opened || s.closed || s.openErr != nil {
		return EventDurabilityConfirmation{}, errors.New("durable sink: not open")
	}
	// Already fully accepted.
	if _, ok := s.byID[id]; ok {
		if s.pending != nil {
			// Any pending entry coexisting with an already-accepted id is an
			// inconsistent state — never clear pending or blind-confirm; surface it
			// (covers both different-id and anomalous same-id cases).
			return EventDurabilityConfirmation{}, errors.New("durable sink: confirm accepted id while pending unreconciled")
		}
		return EventDurabilityConfirmation{EventID: id, Confirmed: true, PendingEmpty: true}, nil
	}
	if s.pending == nil || s.pending.id != id {
		return EventDurabilityConfirmation{}, errors.New("durable sink: id not pending")
	}
	p := s.pending
	activePath := s.activePath()
	got, rerr := s.readFileFn(activePath)
	if rerr != nil {
		// Read failure → error, retain pending, never truncate/rewrite.
		return EventDurabilityConfirmation{}, fmt.Errorf("durable sink: confirm read failed: %w", rerr)
	}
	if int64(len(got)) < p.offset {
		// Active shorter than the pending offset → corrupt; retain pending.
		return EventDurabilityConfirmation{}, errors.New("durable sink: active shorter than pending offset")
	}
	tail := got[p.offset:]
	if len(tail) > len(p.line) {
		// Extra bytes after the pending line → corrupt; retain pending, never truncate.
		return EventDurabilityConfirmation{}, errors.New("durable sink: extra bytes after pending line")
	}
	if !equalCanonical(tail, p.line[:len(tail)]) {
		// Tail diverges from the pending line → corruption; retain pending.
		return EventDurabilityConfirmation{}, errors.New("durable sink: pending tail not a prefix of pending line")
	}
	// tail is an exact prefix of p.line.
	if len(tail) < len(p.line) {
		// Partial write: safe to rewrite (prefix intact + tail is clean prefix).
		if err := s.confirmRewriteLocked(p); err != nil {
			return EventDurabilityConfirmation{}, err
		}
	} else {
		// Full tail present: the original Append's Sync was uncertain, so Confirm
		// MUST re-Sync before committing. A failed re-Sync retains pending and
		// leaves index/accounting untouched (no commit).
		if err := s.syncFn(s.f); err != nil {
			return EventDurabilityConfirmation{}, err
		}
	}
	// Accounting: bring in-memory activeBytes/totalBytes in sync with the on-disk
	// line (the degraded Append never committed). Applied exactly once.
	s.totalBytes += int64(len(p.line))
	s.activeBytes = p.offset + int64(len(p.line))
	s.byID[id] = append([]byte(nil), p.line[:len(p.line)-1]...)
	s.projection = append(s.projection, acceptedDurable{id: id, line: append([]byte(nil), p.line...), acceptedAt: p.acceptedAt})
	s.pending = nil
	if s.health != nil {
		s.health.Resolve(HealthEventDurabilityDegraded)
	}
	return EventDurabilityConfirmation{EventID: id, Confirmed: true, PendingEmpty: true}, nil
}

// confirmRewriteLocked truncates the active to the pending offset then rewrites
// the full pending line via the existing append fd + Sync. Used only when the
// on-disk tail is a strict prefix of the pending line (partial write).
func (s *durableSecurityEventSink) confirmRewriteLocked(p *pendingDurable) error {
	if s.f == nil {
		return errors.New("durable sink: no active fd for confirm rewrite")
	}
	if err := s.f.Truncate(p.offset); err != nil {
		return err
	}
	n, err := s.writeFn(s.f, p.line)
	if err != nil {
		return err
	}
	if n != len(p.line) {
		return fmt.Errorf("durable sink: confirm rewrite short write %d/%d", n, len(p.line))
	}
	return s.syncFn(s.f)
}

// ListSecurityEvents returns a newest-first sanitized projection and a status
// error. limit 0→100, valid 1..500; negative or >500 is a fixed closed error. A
// not-open/failed/closed sink returns (nil, errSinkNotOpen) — a scan-corrupt
// sink never produced a partial projection (OpenAndScan fails closed). If an
// unreconciled pending entry coexists with accepted records, the accepted
// records are returned together with errSinkListPending so the caller does not
// mistake the list for a complete audit.
func (s *durableSecurityEventSink) ListSecurityEvents(limit int) ([]SecurityEventRecord, error) {
	if limit == 0 {
		limit = 100
	} else if limit < 0 || limit > 500 {
		return nil, errSinkListLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.opened || s.closed || s.openErr != nil {
		return nil, errSinkNotOpen
	}
	n := len(s.projection)
	out := make([]SecurityEventRecord, 0)
	if n == 0 {
		if s.pending != nil {
			return out, errSinkListPending
		}
		return out, nil
	}
	start := n - limit
	if start < 0 {
		start = 0
	}
	for i := n - 1; i >= start; i-- {
		out = append(out, sanitizeRecord(s.projection[i].line))
	}
	if s.pending != nil {
		return out, errSinkListPending
	}
	return out, nil
}

var (
	errSinkNotOpen     = errors.New("durable sink: not open")
	errSinkListLimit   = errors.New("durable sink: invalid list limit")
	errSinkListPending = errors.New("durable sink: durability degraded (unreconciled pending)")
)

// Close is idempotent. It flushes + closes the active handle and surfaces Sync
// errors, and reports any unreconciled pending entry as durability-degraded
// health (finding 4). Best-effort: the fd is closed regardless.
func (s *durableSecurityEventSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var serr error
	if s.f != nil {
		serr = s.syncFn(s.f)
		if serr != nil && s.health != nil {
			s.health.Record(HealthEventDurabilityDegraded, "", time.Now())
		}
		cerr := s.f.Close()
		s.f = nil
		// Best-effort: report an unreconciled pending entry even when Close's own
		// Sync succeeded — the event's authoritative status was never confirmed
		// (finding 4). Idempotent with the Append-time record.
		if s.pending != nil && s.health != nil {
			s.health.Record(HealthEventDurabilityDegraded, s.pending.id, time.Now())
		}
		if serr != nil {
			return serr
		}
		return cerr
	}
	// No open fd: still report an unreconciled pending entry.
	if s.pending != nil && s.health != nil {
		s.health.Record(HealthEventDurabilityDegraded, s.pending.id, time.Now())
	}
	return nil
}

// ---------------------------------------------------------------------------
// Rotate marker
// ---------------------------------------------------------------------------

type rotateMarker struct {
	Version    int    `json:"version"`
	Generation int    `json:"generation"`
	Bytes      int    `json:"bytes"`
	SHA256     string `json:"sha256"`
}

// validate enforces the marker field invariants (requirement E).
func (m *rotateMarker) validate() error {
	if m.Version != 1 {
		return integrityScanErr("marker version != 1")
	}
	if m.Generation < 0 {
		return integrityScanErr("marker generation < 0")
	}
	if m.Bytes < 1 || m.Bytes > DurableActiveMaxBytes {
		return integrityScanErr("marker bytes out of range [1..1MiB]")
	}
	if !validLowerHex64(m.SHA256) {
		return integrityScanErr("marker sha256 not lowercase hex64")
	}
	return nil
}

// validLowerHex64 reports whether s is exactly 64 lowercase hex characters.
func validLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Strict JSONL line parsing (item 1): exact known kind enum + canonicalize byte-equal
// ---------------------------------------------------------------------------

// splitDurableLines splits fully LF-terminated lines; blank lines are rejected.
func splitDurableLines(raw []byte) ([][]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[len(raw)-1] != '\n' {
		return nil, errors.New("missing final LF")
	}
	body := raw[:len(raw)-1]
	if len(body) == 0 {
		return nil, nil
	}
	parts := splitByte(body, '\n')
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			return nil, errors.New("blank line")
		}
		cp := make([]byte, len(p))
		copy(cp, p)
		out = append(out, cp)
	}
	return out, nil
}

// splitActiveTail returns the complete LF-terminated prefix bytes and any final
// unterminated tail bytes.
func splitActiveTail(raw []byte) (complete []byte, torn []byte) {
	if len(raw) == 0 {
		return nil, nil
	}
	lastLF := -1
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] == '\n' {
			lastLF = i
			break
		}
	}
	if lastLF == len(raw)-1 {
		return raw, nil
	}
	if lastLF < 0 {
		return nil, raw
	}
	return raw[:lastLF+1], raw[lastLF+1:]
}

func splitByte(b []byte, sep byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == sep {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	out = append(out, b[start:])
	return out
}

// parseDurableLine strictly validates one JSONL line by constructing the concrete
// typed SecurityEvent for the exact known kind and requiring the canonical bytes
// to be byte-equal. This rejects unknown kinds, version!=1, and invalid
// kind/field combinations (e.g. device kind without deviceId).
func parseDurableLine(line []byte) (SecurityEventRecord, error) {
	fields, err := strictJSONObject(line)
	if err != nil {
		return SecurityEventRecord{}, err
	}
	version, verr := reqInt(fields, "version")
	if verr != nil || version != 1 {
		return SecurityEventRecord{}, errors.New("invalid version")
	}
	kind, Kerr := reqStr(fields, "kind")
	if Kerr != nil {
		return SecurityEventRecord{}, errors.New("missing kind")
	}
	ev, eerr := eventFromFields(fields, kind)
	if eerr != nil {
		return SecurityEventRecord{}, eerr
	}
	canonical, cerr := canonicalizeSecurityEvent(ev)
	if cerr != nil {
		return SecurityEventRecord{}, cerr
	}
	if !equalCanonical(canonical, line) {
		return SecurityEventRecord{}, errors.New("line not canonical")
	}
	return sanitizeRecord(line), nil
}

// eventFromFields builds the concrete typed event for an exact known kind.
func eventFromFields(f map[string]json.RawMessage, kind string) (SecurityEvent, error) {
	eid, err := reqStr(f, "eventId")
	if err != nil || !validDurableEventID(eid) {
		return nil, errors.New("invalid eventId")
	}
	occ, err := reqStr(f, "occurredAt")
	if err != nil || !validUTCNano(occ) {
		return nil, errors.New("invalid occurredAt")
	}
	t, _ := time.Parse(time.RFC3339Nano, occ)
	pk := pairingKindFromString(kind)
	if pk != 0 {
		gen, gerr := reqUint(f, "pairingGeneration")
		if gerr != nil {
			return nil, errors.New("pairing event missing generation")
		}
		att, aerr := reqUint(f, "attempt")
		if aerr != nil || att > 255 {
			return nil, errors.New("pairing event invalid attempt")
		}
		// Reject any non-allowlist field for this kind.
		if err := rejectUnknownDurable(f, "version", "eventId", "kind", "occurredAt", "pairingGeneration", "attempt"); err != nil {
			return nil, err
		}
		return PairingSecurityEvent{EventID: SecurityEventID(eid), Kind: pk, OccurredAt: t, Generation: gen, Attempt: uint8(att)}, nil
	}
	dk := deviceKindFromString(kind)
	if dk != 0 {
		dev, derr := reqStr(f, "deviceId")
		if derr != nil || !validRawURLID(dev) {
			return nil, errors.New("device event invalid deviceId")
		}
		if err := rejectUnknownDurable(f, "version", "eventId", "kind", "occurredAt", "deviceId"); err != nil {
			return nil, err
		}
		return DeviceSecurityEvent{EventID: SecurityEventID(eid), Kind: dk, OccurredAt: t, DeviceID: contract.DeviceID(dev)}, nil
	}
	sk := storeKindFromString(kind)
	if sk != 0 {
		if err := rejectUnknownDurable(f, "version", "eventId", "kind", "occurredAt"); err != nil {
			return nil, err
		}
		return StoreSecurityEvent{EventID: SecurityEventID(eid), Kind: sk, OccurredAt: t}, nil
	}
	svk := serviceKindFromString(kind)
	if svk != 0 {
		if err := rejectUnknownDurable(f, "version", "eventId", "kind", "occurredAt"); err != nil {
			return nil, err
		}
		return ServiceSecurityEvent{EventID: SecurityEventID(eid), Kind: svk, OccurredAt: t}, nil
	}
	if kind == "legacy_auth_deprecated" {
		carStr, _ := reqStr(f, "carrier")
		rcStr, _ := reqStr(f, "routeClass")
		outStr, _ := reqStr(f, "outcome")
		car := legacyCarrierFromString(carStr)
		rc := legacyRouteClassFromString(rcStr)
		out := legacyOutcomeFromString(outStr)
		if car == 0 || rc == 0 || out == 0 {
			return nil, errors.New("invalid legacy auth event field")
		}
		if err := rejectUnknownDurable(f, "version", "eventId", "kind", "occurredAt", "carrier", "routeClass", "outcome"); err != nil {
			return nil, err
		}
		return LegacyAuthSecurityEvent{EventID: SecurityEventID(eid), OccurredAt: t, Carrier: car, RouteClass: rc, Outcome: out}, nil
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// pairingKindFromString reverses pairingKindString; 0 = unknown.
func pairingKindFromString(s string) PairingSecurityEventKind {
	switch s {
	case "pairing_window_opened":
		return PairingWindowOpened
	case "pairing_window_canceled":
		return PairingWindowCanceled
	case "pairing_window_expired":
		return PairingWindowExpired
	case "pairing_attempt_rejected":
		return PairingAttemptRejected
	case "pairing_window_locked":
		return PairingWindowLocked
	}
	return 0
}

func deviceKindFromString(s string) DeviceSecurityEventKind {
	switch s {
	case "device_paired":
		return DevicePaired
	case "device_revoked":
		return DeviceRevoked
	}
	return 0
}

func storeKindFromString(s string) StoreSecurityEventKind {
	if s == "store_durability_degraded" {
		return StoreDurabilityDegraded
	}
	return 0
}

// serviceKindFromString reverses serviceKindString; 0 = unknown.
func serviceKindFromString(s string) ServiceSecurityEventKind {
	switch s {
	case "remote_service_started":
		return RemoteServiceStarted
	case "remote_service_stopped":
		return RemoteServiceStopped
	case "legacy_token_rotated":
		return LegacyTokenRotated
	case "remote_listen_configuration_changed":
		return RemoteListenConfigurationChanged
	}
	return 0
}

func legacyCarrierFromString(s string) LegacyAuthCarrier {
	switch s {
	case "bearer":
		return CarrierBearer
	case "query_token":
		return CarrierQueryToken
	case "local_session":
		return CarrierLocalSession
	case "launch_grant":
		return CarrierLaunchGrant
	}
	return 0
}

func legacyRouteClassFromString(s string) LegacyAuthRouteClass {
	switch s {
	case "bootstrap":
		return RouteBootstrap
	case "api_read":
		return RouteAPIRead
	case "api_write":
		return RouteAPIWrite
	case "websocket":
		return RouteWebSocket
	}
	return 0
}

func legacyOutcomeFromString(s string) LegacyAuthOutcome {
	if s == "accepted" {
		return LegacyAuthAccepted
	}
	return 0
}

func sanitizeRecord(line []byte) SecurityEventRecord {
	rec := SecurityEventRecord{}
	fields, _ := strictJSONObject(line)
	if v, ok := fields["eventId"]; ok {
		_ = json.Unmarshal(v, &rec.EventID)
	}
	if v, ok := fields["kind"]; ok {
		_ = json.Unmarshal(v, &rec.Kind)
	}
	if v, ok := fields["occurredAt"]; ok {
		_ = json.Unmarshal(v, &rec.OccurredAt)
	}
	if v, ok := fields["pairingGeneration"]; ok {
		var g uint64
		if json.Unmarshal(v, &g) == nil {
			rec.PairingGeneration = &g
		}
	}
	if v, ok := fields["attempt"]; ok {
		var a uint8
		if json.Unmarshal(v, &a) == nil {
			rec.Attempt = &a
		}
	}
	if v, ok := fields["deviceId"]; ok {
		var d string
		if json.Unmarshal(v, &d) == nil {
			rec.DeviceID = &d
		}
	}
	if v, ok := fields["carrier"]; ok {
		var c string
		if json.Unmarshal(v, &c) == nil {
			rec.Carrier = &c
		}
	}
	if v, ok := fields["routeClass"]; ok {
		var rc string
		if json.Unmarshal(v, &rc) == nil {
			rec.RouteClass = &rc
		}
	}
	if v, ok := fields["outcome"]; ok {
		var o string
		if json.Unmarshal(v, &o) == nil {
			rec.Outcome = &o
		}
	}
	return rec
}

func rejectUnknownDurable(f map[string]json.RawMessage, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	for k := range f {
		if !set[k] {
			return fmt.Errorf("unknown field %q", k)
		}
	}
	return nil
}

func validDurableEventID(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(32) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(b) == s
}

// quarantineAndTruncate safely quarantines a torn active tail then truncates the
// active to keepLen. The torn bytes are written to durableTornName via O_EXCL
// 0600 + full-write + Sync + parent-sync BEFORE the truncate; an existing .torn
// is fail-closed (never overwritten). The active truncate uses Lstat+open+
// SameFile; no read/write error is ignored.
func quarantineAndTruncate(path string, keepLen int, torn []byte) error {
	if keepLen < 0 {
		keepLen = 0
	}
	// 1. Lstat active: strict regular 0600.
	li, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return errors.New("durable sink: active not regular for quarantine")
	}
	if li.Mode().Perm() != 0o600 {
		return fmt.Errorf("durable sink: active mode %o not 0600 for quarantine", li.Mode().Perm())
	}
	// 2. Quarantine the torn tail BEFORE truncating.
	if len(torn) > 0 {
		tornPath := filepath.Join(filepath.Dir(path), durableTornName)
		if tli, terr := os.Lstat(tornPath); terr == nil {
			if tli.Mode()&os.ModeSymlink != 0 || !tli.Mode().IsRegular() {
				return errors.New("durable sink: torn quarantine not regular")
			}
			return errors.New("durable sink: torn quarantine already exists (fail-closed)")
		} else if !errors.Is(terr, fs.ErrNotExist) {
			return terr
		}
		tf, terr := os.OpenFile(tornPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if terr != nil {
			return terr
		}
		if _, werr := writeFull(tf, torn); werr != nil {
			tf.Close()
			return werr
		}
		if serr := tf.Sync(); serr != nil {
			tf.Close()
			return serr
		}
		tf.Close()
		if err := syncParentDir(filepath.Dir(path)); err != nil {
			return err
		}
	}
	// 3. Truncate active via Lstat+open+SameFile (no ignored errors).
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(li, fi) {
		return errors.New("durable sink: active changed identity during quarantine")
	}
	if err := f.Truncate(int64(keepLen)); err != nil {
		return err
	}
	return f.Sync()
}

func isNumericGen(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func genNum(name string) (int, bool) {
	s := strings.TrimPrefix(name, durableArchivePrefix)
	if !isNumericGen(s) {
		return 0, false
	}
	if len(s) > 6 { // overflow guard: any archive generation > 6 digits is corrupt
		return 0, false
	}
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n, true
}

var _ = io.EOF
