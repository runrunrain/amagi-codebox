package remote

// Append-only revocation ledger (design §7.3). This is the SOLE revocation
// security authority: a device is revoked durably when its commit line's second
// File.Sync succeeds. The ledger is StoreID-bound, hash-chained, and
// append-only. Snapshot replacement is only a projection and can NEVER override
// a committed tombstone. The tail-uniqueness rule defines the ONLY provably
// uncommitted state that may be truncated to the committed prefix; everything
// else latches fail-closed. Any comment claiming Windows rename atomicity, or
// any checksum-as-"latest" claim, is forbidden.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// ledgerDomain is the hash domain separator for ledger record hashes.
	ledgerDomain = "amagi-codebox/revocation-ledger/v1"
	// LedgerLineMaxBytes bounds a single ledger line (C-010: line ≤ 1KiB).
	LedgerLineMaxBytes = 1 << 10
	// LedgerCommittedTombstoneMax bounds committed tombstones per StoreID.
	LedgerCommittedTombstoneMax = 1024
)

// ledgerTombstone is one committed revoke record held in memory.
type ledgerTombstone struct {
	sequence  uint64
	revokedAt time.Time
}

// revocationLedger owns the WAL file and its in-memory validated state.
type revocationLedger struct {
	path       string
	storeID    string
	fileSize   int64  // validated byte length of the committed prefix on disk
	headerHash string // recordHash of the header line (chain root)
	headHash   string // recordHash of the last committed prepare (chain head)
	lastSeq    uint64 // last committed prepare sequence (0 = header only)
	tombstones map[string]ledgerTombstone
	seqHash    map[uint64]string // sequence → prepare recordHash (chain history)
}

// committedTombstoneCount returns the number of committed tombstones (C-010).
func (l *revocationLedger) committedTombstoneCount() int { return len(l.tombstones) }

// ledgerReplayResult classifies the trailing bytes after the committed prefix.
type ledgerTailClassification uint8

const (
	tailClean             ledgerTailClassification = iota + 1 // file ends exactly at committed prefix
	tailProvenUncommitted                                     // complete prepare + (no|strict-prefix) commit → truncatable
	tailIndeterminate                                         // anything else → latch
)

// ---------------------------------------------------------------------------
// Line shapes (canonical, fixed field order; recordHash excluded from hashing)
// ---------------------------------------------------------------------------

type ledgerHeaderLine struct {
	Version    int    `json:"version"`
	Type       string `json:"type"`
	StoreID    string `json:"storeId"`
	Sequence   uint64 `json:"sequence"`
	CreatedAt  string `json:"createdAt"`
	PrevHash   string `json:"prevHash"`
	RecordHash string `json:"recordHash"`
}

type ledgerPrepareLine struct {
	Version    int    `json:"version"`
	Type       string `json:"type"`
	StoreID    string `json:"storeId"`
	Sequence   uint64 `json:"sequence"`
	DeviceID   string `json:"deviceId"`
	RevokedAt  string `json:"revokedAt"`
	PrevHash   string `json:"prevHash"`
	RecordHash string `json:"recordHash"`
}

type ledgerCommitLine struct {
	Version    int    `json:"version"`
	Type       string `json:"type"`
	StoreID    string `json:"storeId"`
	Sequence   uint64 `json:"sequence"`
	RecordHash string `json:"recordHash"`
}

// Hash-input structs exclude RecordHash so hashing is over exactly the typed
// fields. Field order is fixed so remarshal is byte-equal.
type ledgerHeaderHashInput struct {
	Version   int    `json:"version"`
	Type      string `json:"type"`
	StoreID   string `json:"storeId"`
	Sequence  uint64 `json:"sequence"`
	CreatedAt string `json:"createdAt"`
	PrevHash  string `json:"prevHash"`
}

type ledgerPrepareHashInput struct {
	Version   int    `json:"version"`
	Type      string `json:"type"`
	StoreID   string `json:"storeId"`
	Sequence  uint64 `json:"sequence"`
	DeviceID  string `json:"deviceId"`
	RevokedAt string `json:"revokedAt"`
	PrevHash  string `json:"prevHash"`
}

// computeRecordHash hashes the canonical typed fields (excluding recordHash).
func computeRecordHash(hashInput []byte) string {
	h := sha256.New()
	h.Write([]byte(ledgerDomain))
	h.Write([]byte{0x00})
	h.Write(hashInput)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func canonicalBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}

// storeIDMatches reports whether got equals expected, or expected is empty
// (discover-from-header mode).
func storeIDMatches(got, expected string) bool { return expected == "" || got == expected }

// zeroHashB64 is the padded base64 of 32 zero bytes (the header prevHash root).
func zeroHashB64() string {
	z := make([]byte, 32)
	return base64.StdEncoding.EncodeToString(z)
}

// ---------------------------------------------------------------------------
// Initialization (fresh store)
// ---------------------------------------------------------------------------

// initializeLedger creates a new ledger file with a validated header. It is
// called only when BOTH store files are absent (the sole fresh-store condition).
func initializeLedger(path, storeID string, createdAt time.Time) error {
	header := ledgerHeaderLine{
		Version:    1,
		Type:       "header",
		StoreID:    storeID,
		Sequence:   0,
		CreatedAt:  createdAt.UTC().Format(time.RFC3339Nano),
		PrevHash:   zeroHashB64(),
		RecordHash: "",
	}
	hi, err := canonicalBytes(ledgerHeaderHashInput{
		Version: header.Version, Type: header.Type, StoreID: header.StoreID,
		Sequence: header.Sequence, CreatedAt: header.CreatedAt, PrevHash: header.PrevHash,
	})
	if err != nil {
		return err
	}
	header.RecordHash = computeRecordHash(hi)
	line, err := canonicalBytes(header)
	if err != nil {
		return err
	}
	if len(line)+1 > LedgerLineMaxBytes {
		return errors.New("ledger: header line exceeds limit")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	buf := append(line, '\n')
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ---------------------------------------------------------------------------
// Replay + tail classification
// ---------------------------------------------------------------------------

// loadLedger reads, strictly validates and replays the ledger. It applies the
// tail-uniqueness rule: a provably-uncommitted trailing transaction is truncated
// (Sync + reopen + revalidate) to the exact committed prefix; an indeterminate
// tail returns a closed error so the caller latches. expectedStoreID must match
// the header (the snapshot's storeID).
func loadLedger(path, expectedStoreID string) (*revocationLedger, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	led, tail, committedEnd, err := parseAndValidateLedger(raw, expectedStoreID)
	if err != nil {
		return nil, err
	}
	switch tail {
	case tailClean:
		led.path = path
		led.fileSize = committedEnd
		return led, nil
	case tailProvenUncommitted:
		// Truncate to committed prefix, Sync, reopen, revalidate.
		if err := truncateRevalidate(path, committedEnd, expectedStoreID); err != nil {
			return nil, err
		}
		led.path = path
		led.fileSize = committedEnd
		return led, nil
	default:
		return nil, closedStoreErr(storeErrSchema)
	}
}

// truncateRevalidate truncates the ledger to committedEnd bytes, syncs, and
// revalidates the prefix equals the previously-validated committed view.
func truncateRevalidate(path string, committedEnd int64, expectedStoreID string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(committedEnd); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, tail, _, err := parseAndValidateLedger(raw, expectedStoreID); err != nil {
		return err
	} else if tail != tailClean {
		return closedStoreErr(storeErrSchema)
	}
	return nil
}

// parseAndValidateLedger validates the committed prefix and classifies the tail.
// committedEnd is the byte offset of the end of the committed prefix (for
// truncation). It does NOT perform I/O.
func parseAndValidateLedger(raw []byte, expectedStoreID string) (*revocationLedger, ledgerTailClassification, int64, error) {
	lines, err := splitLedgerLines(raw)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(lines) == 0 {
		return nil, 0, 0, errors.New("ledger: empty file")
	}
	// Header.
	hoff := int64(len(lines[0]) + 1)
	var header ledgerHeaderLine
	if err := strictParseLine(lines[0], &header, "header"); err != nil {
		return nil, 0, 0, err
	}
	if header.Version != 1 || (expectedStoreID != "" && header.StoreID != expectedStoreID) || header.Sequence != 0 || header.PrevHash != zeroHashB64() {
		return nil, 0, 0, errors.New("ledger: invalid header")
	}
	hi, _ := canonicalBytes(ledgerHeaderHashInput{
		Version: header.Version, Type: header.Type, StoreID: header.StoreID,
		Sequence: header.Sequence, CreatedAt: header.CreatedAt, PrevHash: header.PrevHash,
	})
	if header.RecordHash != computeRecordHash(hi) {
		return nil, 0, 0, errors.New("ledger: header hash mismatch")
	}

	led := &revocationLedger{
		storeID:    header.StoreID,
		headerHash: header.RecordHash,
		headHash:   header.RecordHash,
		lastSeq:    0,
		tombstones: make(map[string]ledgerTombstone),
		seqHash:    map[uint64]string{0: header.RecordHash},
	}

	expectedSeq := uint64(1)
	committedEnd := hoff
	i := 1
	for i+1 <= len(lines) {
		prepareRaw := lines[i]
		var prepare ledgerPrepareLine
		if err := strictParseLine(prepareRaw, &prepare, "revoke.prepare"); err != nil {
			// Could be a trailing lone prepare (tail) or corruption.
			return classifyTail(led, lines, i, expectedStoreID, committedEnd, header.RecordHash, expectedSeq)
		}
		if prepare.Sequence != expectedSeq || !storeIDMatches(prepare.StoreID, expectedStoreID) {
			return classifyTail(led, lines, i, expectedStoreID, committedEnd, header.RecordHash, expectedSeq)
		}
		if _, dup := led.tombstones[prepare.DeviceID]; dup {
			return nil, 0, 0, errors.New("ledger: duplicate committed tombstone")
		}
		if prepare.PrevHash != led.headHash {
			return classifyTail(led, lines, i, expectedStoreID, committedEnd, header.RecordHash, expectedSeq)
		}
		phi, _ := canonicalBytes(ledgerPrepareHashInput{
			Version: prepare.Version, Type: prepare.Type, StoreID: prepare.StoreID,
			Sequence: prepare.Sequence, DeviceID: prepare.DeviceID,
			RevokedAt: prepare.RevokedAt, PrevHash: prepare.PrevHash,
		})
		if prepare.RecordHash != computeRecordHash(phi) {
			return classifyTail(led, lines, i, expectedStoreID, committedEnd, header.RecordHash, expectedSeq)
		}
		// Need the matching commit.
		if i+1 >= len(lines) {
			// Prepare with no commit at all → provably uncommitted tail.
			return led, tailProvenUncommitted, committedEnd, nil
		}
		commitRaw := lines[i+1]
		var commit ledgerCommitLine
		if err := strictParseLine(commitRaw, &commit, "revoke.commit"); err != nil {
			// Commit is malformed; it may be a strict prefix of the canonical
			// commit line (crash mid-write). Check prefix match.
			expectedCommit := ledgerCommitLine{
				Version: 1, Type: "revoke.commit", StoreID: prepare.StoreID,
				Sequence: prepare.Sequence, RecordHash: prepare.RecordHash,
			}
			expBytes, _ := canonicalBytes(expectedCommit)
			if isStrictPrefix(expBytes, commitRaw) {
				return led, tailProvenUncommitted, committedEnd, nil
			}
			return nil, 0, 0, closedStoreErr(storeErrSchema)
		}
		if commit.Sequence != prepare.Sequence || !storeIDMatches(commit.StoreID, expectedStoreID) || commit.RecordHash != prepare.RecordHash {
			return nil, 0, 0, errors.New("ledger: commit does not match prepare")
		}
		// Committed pair.
		revokedAt, err := time.Parse(time.RFC3339Nano, prepare.RevokedAt)
		if err != nil {
			return nil, 0, 0, errors.New("ledger: invalid revokedAt")
		}
		led.tombstones[prepare.DeviceID] = ledgerTombstone{sequence: prepare.Sequence, revokedAt: revokedAt.UTC()}
		led.headHash = prepare.RecordHash
		led.lastSeq = prepare.Sequence
		led.seqHash[prepare.Sequence] = prepare.RecordHash
		committedEnd = int64(len(strings.Join(lines[:i+2], "\n")) + 1)
		expectedSeq++
		i += 2
	}
	// Check for a trailing lone prepare after the last committed pair.
	if i < len(lines) {
		return classifyTail(led, lines, i, expectedStoreID, committedEnd, header.RecordHash, expectedSeq)
	}
	return led, tailClean, committedEnd, nil
}

// classifyTail inspects a trailing uncommitted transaction starting at lines[i]
// and returns the tail classification. A complete prepare with no commit, or a
// prepare followed by a strict-prefix commit, is provably uncommitted. A
// complete matching prepare+commit is treated as committed (fail-secure).
func classifyTail(led *revocationLedger, lines []string, i int, expectedStoreID string, committedEnd int64, _ string, expectedSeq uint64) (*revocationLedger, ledgerTailClassification, int64, error) {
	prepareRaw := lines[i]
	var prepare ledgerPrepareLine
	if err := strictParseLine(prepareRaw, &prepare, "revoke.prepare"); err != nil {
		return nil, 0, 0, closedStoreErr(storeErrSchema)
	}
	if prepare.Sequence != expectedSeq || !storeIDMatches(prepare.StoreID, expectedStoreID) || prepare.PrevHash != led.headHash {
		return nil, 0, 0, closedStoreErr(storeErrSchema)
	}
	phi, _ := canonicalBytes(ledgerPrepareHashInput{
		Version: prepare.Version, Type: prepare.Type, StoreID: prepare.StoreID,
		Sequence: prepare.Sequence, DeviceID: prepare.DeviceID,
		RevokedAt: prepare.RevokedAt, PrevHash: prepare.PrevHash,
	})
	if prepare.RecordHash != computeRecordHash(phi) {
		return nil, 0, 0, closedStoreErr(storeErrSchema)
	}
	// No commit present at all.
	if i+1 >= len(lines) {
		return led, tailProvenUncommitted, committedEnd, nil
	}
	commitRaw := lines[i+1]
	expectedCommit := ledgerCommitLine{
		Version: 1, Type: "revoke.commit", StoreID: prepare.StoreID,
		Sequence: prepare.Sequence, RecordHash: prepare.RecordHash,
	}
	expBytes, _ := canonicalBytes(expectedCommit)
	if isStrictPrefix(expBytes, commitRaw) {
		return led, tailProvenUncommitted, committedEnd, nil
	}
	var commit ledgerCommitLine
	if err := strictParseLine(commitRaw, &commit, "revoke.commit"); err != nil {
		return nil, 0, 0, closedStoreErr(storeErrSchema)
	}
	// Complete matching prepare+commit: fail-secure treat as committed.
	if commit.Sequence == prepare.Sequence && storeIDMatches(commit.StoreID, expectedStoreID) && commit.RecordHash == prepare.RecordHash {
		if _, dup := led.tombstones[prepare.DeviceID]; dup {
			return nil, 0, 0, errors.New("ledger: duplicate committed tombstone")
		}
		revokedAt, err := time.Parse(time.RFC3339Nano, prepare.RevokedAt)
		if err != nil {
			return nil, 0, 0, errors.New("ledger: invalid revokedAt")
		}
		led.tombstones[prepare.DeviceID] = ledgerTombstone{sequence: prepare.Sequence, revokedAt: revokedAt.UTC()}
		led.headHash = prepare.RecordHash
		led.lastSeq = prepare.Sequence
		led.seqHash[prepare.Sequence] = prepare.RecordHash
		// Any further lines after this pair are non-tail corruption.
		if i+2 < len(lines) {
			return nil, 0, 0, closedStoreErr(storeErrSchema)
		}
		end := int64(len(strings.Join(lines[:i+2], "\n")) + 1)
		return led, tailClean, end, nil
	}
	return nil, 0, 0, closedStoreErr(storeErrSchema)
}

// isStrictPrefix reports whether maybePrefixRaw is a non-empty strict prefix of
// the canonical expected bytes (crash mid-write of the commit line).
func isStrictPrefix(expected []byte, maybePrefixRaw string) bool {
	mp := []byte(maybePrefixRaw)
	if len(mp) == 0 || len(mp) >= len(expected) {
		return false
	}
	return bytes.Equal(expected[:len(mp)], mp)
}

// reserveSufficient reports whether the WAL can admit one more revoke (two
// lines) within the C-010 budget: committed tombstones < 1024, each line ≤ 1KiB,
// and projected WAL bytes ≤ 8MiB.
const LedgerMaxBytes = 8 << 20

func (l *revocationLedger) reserveSufficient(prepareLine, commitLine []byte) error {
	if l.committedTombstoneCount() >= LedgerCommittedTombstoneMax {
		return closedStoreErr(storeErrCapacity)
	}
	if len(prepareLine)+1 > LedgerLineMaxBytes || len(commitLine)+1 > LedgerLineMaxBytes {
		return closedStoreErr(storeErrCapacity)
	}
	projected := l.fileSize + int64(len(prepareLine)+1) + int64(len(commitLine)+1)
	if projected > LedgerMaxBytes {
		return closedStoreErr(storeErrCapacity)
	}
	return nil
}

// ledgerAppendResult carries the sequence + three-state mutation outcome.
type ledgerAppendResult struct {
	sequence uint64
	mutation StoreMutationResult
}

// appendRevoke performs the prepare→Sync→commit→Sync protocol. The revoke
// linearization point is the SECOND File.Sync (the commit Sync) succeeding. On
// any Write/Sync error the ledger is closed/reopened/reconciled: a provably
// uncommitted tail → StoreNotCommitted; a now-complete matching pair →
// StoreCommitted (fail-secure); anything else → StoreIndeterminateFailClosed.
func (l *revocationLedger) appendRevoke(deviceID string, revokedAt time.Time) (ledgerAppendResult, error) {
	seq := l.lastSeq + 1
	prepare := ledgerPrepareLine{
		Version: 1, Type: "revoke.prepare", StoreID: l.storeID, Sequence: seq,
		DeviceID: deviceID, RevokedAt: revokedAt.UTC().Format(time.RFC3339Nano),
		PrevHash: l.headHash, RecordHash: "",
	}
	phi, err := canonicalBytes(ledgerPrepareHashInput{
		Version: prepare.Version, Type: prepare.Type, StoreID: prepare.StoreID,
		Sequence: prepare.Sequence, DeviceID: prepare.DeviceID,
		RevokedAt: prepare.RevokedAt, PrevHash: prepare.PrevHash,
	})
	if err != nil {
		return ledgerAppendResult{}, err
	}
	prepare.RecordHash = computeRecordHash(phi)
	commit := ledgerCommitLine{
		Version: 1, Type: "revoke.commit", StoreID: l.storeID,
		Sequence: seq, RecordHash: prepare.RecordHash,
	}
	prepareBytes, err := canonicalBytes(prepare)
	if err != nil {
		return ledgerAppendResult{}, err
	}
	commitBytes, err := canonicalBytes(commit)
	if err != nil {
		return ledgerAppendResult{}, err
	}
	if err := l.reserveSufficient(prepareBytes, commitBytes); err != nil {
		return ledgerAppendResult{}, err
	}

	oldEnd := l.fileSize
	oldHead := l.headHash

	// Verify regular identity + size == validated end before appending.
	info, err := os.Stat(l.path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != l.fileSize {
		return ledgerAppendResult{mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}},
			closedStoreErr(storeErrIndeterminate)
	}

	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return ledgerAppendResult{mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}},
			closedStoreErr(storeErrIndeterminate)
	}

	prepareBuf := append(prepareBytes, '\n')
	if _, err := writeFull(f, prepareBuf); err != nil {
		f.Close()
		return l.reconcileAfterAppendError(deviceID, seq, oldEnd, oldHead)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return l.reconcileAfterAppendError(deviceID, seq, oldEnd, oldHead)
	}
	commitBuf := append(commitBytes, '\n')
	if _, err := writeFull(f, commitBuf); err != nil {
		f.Close()
		return l.reconcileAfterAppendError(deviceID, seq, oldEnd, oldHead)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return l.reconcileAfterAppendError(deviceID, seq, oldEnd, oldHead)
	}
	if err := f.Close(); err != nil {
		return l.reconcileAfterAppendError(deviceID, seq, oldEnd, oldHead)
	}

	// Commit Sync succeeded → linearization point reached.
	l.headHash = prepare.RecordHash
	l.lastSeq = seq
	l.tombstones[deviceID] = ledgerTombstone{sequence: seq, revokedAt: revokedAt.UTC()}
	l.fileSize = oldEnd + int64(len(prepareBuf)) + int64(len(commitBuf))
	return ledgerAppendResult{sequence: seq, mutation: StoreMutationResult{State: StoreCommitted}}, nil
}

// reconcileAfterAppendError re-reads and re-validates the ledger to classify the
// outcome after a Write/Sync failure. complete matching pair → Committed
// (fail-secure); provably-uncommitted tail → NotCommitted (after truncate);
// else → Indeterminate (latch).
func (l *revocationLedger) reconcileAfterAppendError(deviceID string, seq uint64, oldEnd int64, oldHead string) (ledgerAppendResult, error) {
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return ledgerAppendResult{mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}},
			closedStoreErr(storeErrIndeterminate)
	}
	led, tail, committedEnd, perr := parseAndValidateLedger(raw, l.storeID)
	if perr != nil {
		return ledgerAppendResult{mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}},
			closedStoreErr(storeErrIndeterminate)
	}
	// Did the full pair commit despite the reported error?
	if t, ok := led.tombstones[deviceID]; ok && t.sequence == seq && led.lastSeq == seq {
		l.headHash = led.headHash
		l.lastSeq = led.lastSeq
		l.tombstones = led.tombstones
		l.fileSize = committedEnd
		return ledgerAppendResult{sequence: seq, mutation: StoreMutationResult{State: StoreCommitted}}, nil
	}
	// Must still be at the old committed prefix.
	if led.lastSeq != seq-1 || led.headHash != oldHead || committedEnd != oldEnd {
		return ledgerAppendResult{mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}},
			closedStoreErr(storeErrIndeterminate)
	}
	switch tail {
	case tailClean, tailProvenUncommitted:
		if tail == tailProvenUncommitted {
			if err := truncateRevalidate(l.path, committedEnd, l.storeID); err != nil {
				return ledgerAppendResult{mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}},
					closedStoreErr(storeErrIndeterminate)
			}
		}
		l.fileSize = oldEnd
		return ledgerAppendResult{mutation: StoreMutationResult{State: StoreNotCommitted}}, nil
	default:
		return ledgerAppendResult{mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}},
			closedStoreErr(storeErrIndeterminate)
	}
}

// writeFull writes the full buffer, looping on short writes.
func writeFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Write(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// splitLedgerLines splits raw into lines, enforcing LF-terminated, no CR, no
// embedded control chars, non-empty, and ≤ LedgerLineMaxBytes each. A trailing
// LF is required.
func splitLedgerLines(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("ledger: empty")
	}
	if raw[len(raw)-1] != '\n' {
		return nil, errors.New("ledger: file must end with newline")
	}
	body := string(raw[:len(raw)-1])
	if body == "" {
		return nil, errors.New("ledger: empty")
	}
	parts := strings.Split(body, "\n")
	for _, p := range parts {
		if p == "" {
			return nil, errors.New("ledger: empty line")
		}
		if len(p) > LedgerLineMaxBytes {
			return nil, errors.New("ledger: line exceeds limit")
		}
		for _, c := range []byte(p) {
			if c == '\r' || c == 0 {
				return nil, errors.New("ledger: illegal control char")
			}
		}
	}
	return parts, nil
}

// strictParseLine strictly parses one JSON object line into dst, rejecting
// unknown fields, null required fields, trailing values and type mismatch. The
// expected type string is cross-checked.
func strictParseLine(raw string, dst any, wantType string) error {
	fields, err := strictJSONObject([]byte(raw))
	if err != nil {
		return err
	}
	if v, ok := fields["type"]; !ok || string(bytes.TrimSpace(v)) != "\""+wantType+"\"" {
		return fmt.Errorf("ledger: expected type %q", wantType)
	}
	return json.Unmarshal([]byte(raw), dst)
}
