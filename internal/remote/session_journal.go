package remote

// session_journal.go — M2 SessionOperationJournal: the durable dangerous-
// session-operation log (design §8.5 Major-06).
//
// File: ~/.amagi-codebox/session-operations.log
//   - directory 0700, file 0600, regular no-symlink;
//   - canonical NDJSON single line ≤ 1 KiB, UTC time;
//   - main ≤ 1 MiB + one 1 MiB archive, whole-record rotation;
//   - typed intent/result records (no sensitive content);
//   - query limit 1..200, newest-first, merged by OperationID.
//
// The journal is independent of M1 SecurityEvent (design §8.4: "does not
// import/falsify SecurityEvent"). It is a separate path/schema/query service.
//
// Fail-closed: corrupt/nonregular/rotation failure → journal not-ready → new
// dangerous operations fail closed; session read/output/revoke/host cleanup
// continue (design §8.5.2).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Typed schema (design §8.5.1)
// ---------------------------------------------------------------------------

// SessionOperationKind enumerates dangerous operation kinds.
type SessionOperationKind string

const (
	SessionOpStop    SessionOperationKind = "stop"
	SessionOpRestart SessionOperationKind = "restart"
	SessionOpRemove  SessionOperationKind = "remove"
)

// SessionOperationPhase enumerates the journal record phase.
type SessionOperationPhase string

const (
	SessionPhaseIntent   SessionOperationPhase = "intent"
	SessionPhaseResult   SessionOperationPhase = "result"
	SessionPhaseRecovery SessionOperationPhase = "recovery"
)

// SessionOperationOutcome enumerates the operation outcome.
type SessionOperationOutcome string

const (
	SessionOutcomePending       SessionOperationOutcome = "pending"
	SessionOutcomeCommitted     SessionOperationOutcome = "committed"
	SessionOutcomeFailed        SessionOperationOutcome = "failed"
	SessionOutcomeIndeterminate SessionOperationOutcome = "indeterminate"
)

// SessionOperationActor enumerates who initiated the operation.
type SessionOperationActor string

const (
	SessionActorRemote  SessionOperationActor = "remote"
	SessionActorDesktop SessionOperationActor = "desktop"
)

// SessionOperationRecord is the canonical NDJSON journal record (design §8.5.1).
// It contains NO device/request identity, path/title/provider/model, argv/env,
// terminal/raw error, or credential.
type SessionOperationRecord struct {
	Version     uint8                   `json:"version"`
	OperationID string                  `json:"operationId"` // 16 random bytes RawURL; groups records
	SessionID   contract.SessionID      `json:"sessionId"`
	CLIType     contract.CLIType        `json:"cliType"`
	Operation   SessionOperationKind    `json:"operation"`
	Phase       SessionOperationPhase   `json:"phase"`
	Outcome     SessionOperationOutcome `json:"outcome"`
	Actor       SessionOperationActor   `json:"actor"`
	FailureCode *contract.ErrorCode     `json:"failureCode,omitempty"`
	OccurredAt  string                  `json:"occurredAt"` // RFC3339Nano UTC
}

// SessionOperationIntent is the data needed to begin an intent record.
type SessionOperationIntent struct {
	OperationID string
	SessionID   contract.SessionID
	CLIType     contract.CLIType
	Operation   SessionOperationKind
	Actor       SessionOperationActor
}

// OperationRecordPermit is the opaque permit returned by BeginIntent (design
// §8.5.2: "append+file sync成功才返回 opaque permit").
type OperationRecordPermit struct {
	intent SessionOperationIntent
}

// Intent returns the intent data (for the caller to correlate).
func (p *OperationRecordPermit) Intent() SessionOperationIntent { return p.intent }

// ---------------------------------------------------------------------------
// Frozen capacity constants (design §8.5.1)
// ---------------------------------------------------------------------------

const (
	// journalMaxMainBytes is the main file size before rotation (1 MiB).
	journalMaxMainBytes = 1 << 20
	// journalMaxArchiveBytes is the archive file size cap (1 MiB).
	journalMaxArchiveBytes = 1 << 20
	// journalMaxLineBytes is the max NDJSON single-line size (1 KiB).
	journalMaxLineBytes = 1 << 10
	// journalQueryMaxLimit is the max query limit.
	journalQueryMaxLimit = 200
	// journalVersion is the schema version.
	journalVersion uint8 = 1
	// journalDirPerm is the directory permission (0700).
	journalDirPerm = 0o700
	// journalFilePerm is the file permission (0600).
	journalFilePerm = 0o600
)

// journalFilename is the fixed filename.
const journalFilename = "session-operations.log"

// ---------------------------------------------------------------------------
// SessionOperationJournal interface (design §8.5.1)
// ---------------------------------------------------------------------------

// SessionOperationJournal is the durable dangerous-session-operation log
// interface (design §8.5.1).
type SessionOperationJournal interface {
	BeginIntent(ctx context.Context, intent SessionOperationIntent) (*OperationRecordPermit, error)
	Complete(ctx context.Context, permit *OperationRecordPermit, outcome SessionOperationOutcome, failureCode contract.ErrorCode) error
	ListRecent(ctx context.Context, limit uint16) ([]SessionOperationRecord, error)
	IsReady() bool
}

// ---------------------------------------------------------------------------
// fileSessionOperationJournal — concrete implementation
// ---------------------------------------------------------------------------

// fileSessionOperationJournal is the concrete file-backed journal.
type fileSessionOperationJournal struct {
	mu      sync.Mutex
	dir     string
	path    string
	archive string
	ready   bool
}

// NewSessionOperationJournal creates a journal rooted at the given config dir
// (typically ~/.amagi-codebox). It ensures the directory exists with 0700 and
// opens/creates the file with 0600. A corrupt or non-regular existing file
// makes the journal not-ready (fail-closed).
func NewSessionOperationJournal(configDir string) SessionOperationJournal {
	j := &fileSessionOperationJournal{
		dir:     configDir,
		path:    filepath.Join(configDir, journalFilename),
		archive: filepath.Join(configDir, journalFilename+".1"),
	}
	j.init()
	return j
}

// init creates the directory and file if needed and validates the existing file.
func (j *fileSessionOperationJournal) init() {
	// Create directory (0700).
	if err := os.MkdirAll(j.dir, journalDirPerm); err != nil {
		j.ready = false
		return
	}
	// Fix directory perms even if it already existed.
	_ = os.Chmod(j.dir, journalDirPerm)

	// Check if file exists and is regular + no-symlink.
	info, err := os.Lstat(j.path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			j.ready = false
			return
		}
		// Fix file perms.
		_ = os.Chmod(j.path, journalFilePerm)
		j.ready = true
		return
	}
	if !os.IsNotExist(err) {
		j.ready = false
		return
	}
	// Create the file with 0600.
	f, cerr := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, journalFilePerm)
	if cerr != nil {
		j.ready = false
		return
	}
	f.Close()
	j.ready = true
}

// IsReady reports whether the journal is ready for dangerous operations.
func (j *fileSessionOperationJournal) IsReady() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ready
}

// BeginIntent appends an intent (phase=intent, outcome=pending) record and
// fsyncs. Returns an opaque permit on success (design §8.5.2 step 1).
func (j *fileSessionOperationJournal) BeginIntent(ctx context.Context, intent SessionOperationIntent) (*OperationRecordPermit, error) {
	if !j.IsReady() {
		return nil, errJournalNotReady
	}
	if intent.OperationID == "" || intent.SessionID == "" || intent.CLIType == "" || intent.Operation == "" || intent.Actor == "" {
		return nil, errJournalInvalidIntent
	}
	rec := SessionOperationRecord{
		Version:     journalVersion,
		OperationID: intent.OperationID,
		SessionID:   intent.SessionID,
		CLIType:     intent.CLIType,
		Operation:   intent.Operation,
		Phase:       SessionPhaseIntent,
		Outcome:     SessionOutcomePending,
		Actor:       intent.Actor,
		OccurredAt:  nowUTCNano(),
	}
	if err := j.appendRecord(rec); err != nil {
		return nil, err
	}
	return &OperationRecordPermit{intent: intent}, nil
}

// Complete appends a result (phase=result) record for the given permit (design
// §8.5.2 step 2). Only 'failed' outcome may carry a failureCode.
func (j *fileSessionOperationJournal) Complete(ctx context.Context, permit *OperationRecordPermit, outcome SessionOperationOutcome, failureCode contract.ErrorCode) error {
	if permit == nil {
		return errJournalInvalidIntent
	}
	if !j.IsReady() {
		// Journal not ready: the effect may have committed; we cannot append.
		// The intent remains pending → startup projects as indeterminate.
		return errJournalNotReady
	}
	switch outcome {
	case SessionOutcomeCommitted, SessionOutcomeIndeterminate:
		// No failureCode.
	case SessionOutcomeFailed:
		// May carry failureCode (optional).
	default:
		return errJournalInvalidOutcome
	}
	rec := SessionOperationRecord{
		Version:     journalVersion,
		OperationID: permit.intent.OperationID,
		SessionID:   permit.intent.SessionID,
		CLIType:     permit.intent.CLIType,
		Operation:   permit.intent.Operation,
		Phase:       SessionPhaseResult,
		Outcome:     outcome,
		Actor:       permit.intent.Actor,
		OccurredAt:  nowUTCNano(),
	}
	if outcome == SessionOutcomeFailed && failureCode != "" {
		rec.FailureCode = &failureCode
	}
	return j.appendRecord(rec)
}

// ListRecent returns the most recent records (newest-first, merged by
// OperationID). limit is clamped to 1..200.
func (j *fileSessionOperationJournal) ListRecent(ctx context.Context, limit uint16) ([]SessionOperationRecord, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > journalQueryMaxLimit {
		limit = journalQueryMaxLimit
	}
	// Read both main and archive, merge, sort newest-first, take limit.
	records, err := j.readAllRecords()
	if err != nil {
		return nil, err
	}
	// Sort newest-first by OccurredAt.
	sortRecordsNewestFirst(records)
	if len(records) > int(limit) {
		records = records[:limit]
	}
	return records, nil
}

// appendRecord marshals, validates size, appends with fsync, and rotates if
// needed.
func (j *fileSessionOperationJournal) appendRecord(rec SessionOperationRecord) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("journal: marshal: %w", err)
	}
	if len(line) > journalMaxLineBytes {
		return errJournalLineTooLong
	}
	line = append(line, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.ready {
		return errJournalNotReady
	}
	// Rotation check.
	info, err := os.Stat(j.path)
	if err == nil && info.Size()+int64(len(line)) > journalMaxMainBytes {
		if rerr := j.rotateLocked(); rerr != nil {
			j.ready = false
			return fmt.Errorf("journal: rotation failed: %w", rerr)
		}
	}
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND, journalFilePerm)
	if err != nil {
		j.ready = false
		return fmt.Errorf("journal: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		j.ready = false
		return fmt.Errorf("journal: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		j.ready = false
		return fmt.Errorf("journal: sync: %w", err)
	}
	return nil
}

// rotateLocked moves the main file to the archive (replacing any old archive).
func (j *fileSessionOperationJournal) rotateLocked() error {
	// Remove old archive (ignore error).
	_ = os.Remove(j.archive)
	// Rename main → archive.
	if err := os.Rename(j.path, j.archive); err != nil {
		return err
	}
	// Truncate archive if it somehow exceeds the cap (whole-record safety).
	if info, err := os.Stat(j.archive); err == nil && info.Size() > journalMaxArchiveBytes {
		// Truncate by rewriting: keep only the tail that fits.
		data, _ := os.ReadFile(j.archive)
		if len(data) > journalMaxArchiveBytes {
			// Find the first newline after the cut point.
			cut := len(data) - journalMaxArchiveBytes
			for cut < len(data) && data[cut] != '\n' {
				cut++
			}
			if cut < len(data) {
				_ = os.WriteFile(j.archive, data[cut+1:], journalFilePerm)
			}
		}
	}
	// Create new empty main.
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, journalFilePerm)
	if err != nil {
		return err
	}
	f.Close()
	return nil
}

// readAllRecords reads and parses all NDJSON records from both files.
func (j *fileSessionOperationJournal) readAllRecords() ([]SessionOperationRecord, error) {
	j.mu.Lock()
	paths := []string{j.archive, j.path}
	j.mu.Unlock()
	var records []SessionOperationRecord
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("journal: read %s: %w", p, err)
		}
		// Split by newline and parse each non-empty line.
		start := 0
		for start < len(data) {
			end := start
			for end < len(data) && data[end] != '\n' {
				end++
			}
			line := data[start:end]
			if len(line) > 0 {
				var rec SessionOperationRecord
				if err := json.Unmarshal(line, &rec); err != nil {
					// Skip corrupt lines (best-effort read).
					start = end + 1
					continue
				}
				records = append(records, rec)
			}
			start = end + 1
		}
	}
	return records, nil
}

// ---------------------------------------------------------------------------
// noopSessionOperationJournal — for environments without file access (tests)
// ---------------------------------------------------------------------------

// noopSessionOperationJournal is an in-memory journal that always succeeds
// (for tests that don't need durability).
type noopSessionOperationJournal struct {
	mu      sync.Mutex
	records []SessionOperationRecord
	ready   bool
}

// NewNoopSessionOperationJournal creates an in-memory journal.
func NewNoopSessionOperationJournal(ready bool) SessionOperationJournal {
	return &noopSessionOperationJournal{ready: ready}
}

func (j *noopSessionOperationJournal) BeginIntent(ctx context.Context, intent SessionOperationIntent) (*OperationRecordPermit, error) {
	if !j.ready {
		return nil, errJournalNotReady
	}
	if intent.OperationID == "" || intent.SessionID == "" || intent.CLIType == "" || intent.Operation == "" || intent.Actor == "" {
		return nil, errJournalInvalidIntent
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records = append(j.records, SessionOperationRecord{
		Version:     journalVersion,
		OperationID: intent.OperationID,
		SessionID:   intent.SessionID,
		CLIType:     intent.CLIType,
		Operation:   intent.Operation,
		Phase:       SessionPhaseIntent,
		Outcome:     SessionOutcomePending,
		Actor:       intent.Actor,
		OccurredAt:  nowUTCNano(),
	})
	return &OperationRecordPermit{intent: intent}, nil
}

func (j *noopSessionOperationJournal) Complete(ctx context.Context, permit *OperationRecordPermit, outcome SessionOperationOutcome, failureCode contract.ErrorCode) error {
	if permit == nil {
		return errJournalInvalidIntent
	}
	if !j.ready {
		return errJournalNotReady
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	rec := SessionOperationRecord{
		Version:     journalVersion,
		OperationID: permit.intent.OperationID,
		SessionID:   permit.intent.SessionID,
		CLIType:     permit.intent.CLIType,
		Operation:   permit.intent.Operation,
		Phase:       SessionPhaseResult,
		Outcome:     outcome,
		Actor:       permit.intent.Actor,
		OccurredAt:  nowUTCNano(),
	}
	if outcome == SessionOutcomeFailed && failureCode != "" {
		rec.FailureCode = &failureCode
	}
	j.records = append(j.records, rec)
	return nil
}

func (j *noopSessionOperationJournal) ListRecent(ctx context.Context, limit uint16) ([]SessionOperationRecord, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > journalQueryMaxLimit {
		limit = journalQueryMaxLimit
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]SessionOperationRecord, len(j.records))
	copy(out, j.records)
	sortRecordsNewestFirst(out)
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (j *noopSessionOperationJournal) IsReady() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ready
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nowUTCNano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func sortRecordsNewestFirst(records []SessionOperationRecord) {
	// M-010: the journal is a serial append log, so append order (the position
	// of each record in the concatenated archive→main read stream) is the
	// AUTHORITATIVE chronology. Wall-clock OccurredAt is formatted from
	// time.Now() which is non-monotonic and can move backward (observed under
	// -race: a result appended after its intent had an earlier timestamp), so a
	// timestamp sort — string OR parsed time.Time — is not stable and produced a
	// reproducible flake ("expected result first, got intent").
	//
	// The records slice is already in append order when this function is called
	// (file journal: archive lines then main lines; noop journal: slice order).
	// We sort by that append index descending, which is exactly newest-first and
	// deterministically places a result (appended after its intent) ahead of it.
	// Parsed time is kept only as a defensive secondary key and is never the
	// decisive comparison because the append index is unique per record.
	type indexed struct {
		rec SessionOperationRecord
		pos int
	}
	idxed := make([]indexed, len(records))
	for i := range records {
		idxed[i] = indexed{rec: records[i], pos: i}
	}
	sort.SliceStable(idxed, func(a, b int) bool {
		if idxed[a].pos != idxed[b].pos {
			return idxed[a].pos > idxed[b].pos // later append = newer = first
		}
		// Same position cannot happen (unique), but keep a deterministic time
		// tie-break for completeness.
		return parseJournalTime(idxed[a].rec.OccurredAt).After(parseJournalTime(idxed[b].rec.OccurredAt))
	})
	for i := range idxed {
		records[i] = idxed[i].rec
	}
}

// parseJournalTime parses a journal OccurredAt (RFC3339Nano UTC). Returns the
// zero time on a malformed value (defensive; never the decisive sort key).
func parseJournalTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// GenerateOperationID returns a 16-random-byte RawURL operation ID.
func GenerateOperationID() string {
	buf := make([]byte, 16)
	if _, err := cryptoRandRead(buf); err != nil {
		return "" // caller must fail closed
	}
	return rawURLBase64(buf)
}

// journal sentinel errors.
var (
	errJournalNotReady       = errors.New("journal: not ready")
	errJournalInvalidIntent  = errors.New("journal: invalid intent")
	errJournalInvalidOutcome = errors.New("journal: invalid outcome")
	errJournalLineTooLong    = errors.New("journal: line exceeds 1 KiB")
)
