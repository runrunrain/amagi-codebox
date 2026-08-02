package remote

// Device store repository (design §6.3, §7). devices.json is the canonical
// credential/device SNAPSHOT PROJECTION; devices.revocations.wal is the
// StoreID-bound append-only REVOCATION AUTHORITY. Mutations classify strictly
// into NotCommitted|Committed|IndeterminateFailClosed by exact old/next
// read-back (snapshot) or ledger commit Sync (revoke). Every normalStorePermit
// is validated (process nonce + kind) WITHOUT acquiring the gate lock: the gate
// guarantees it cannot leave the normal state while any normal permit is
// outstanding, so a nonce+kind match is sufficient. C-010 caps (1024 known IDs,
// 1MiB snapshot, 8MiB WAL, 1KiB lines) only block NEW pair/window; existing
// auth/revoke always remain.

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// Strict JSON object parser (shared by snapshot + ledger): rejects non-object,
// null, duplicate keys, unknown trailing.
// ---------------------------------------------------------------------------

func strictJSONObject(raw []byte) (map[string]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("store: empty input")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("store: expected JSON object: %w", err)
	}
	delim, ok := t.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("store: expected JSON object")
	}
	m := make(map[string]json.RawMessage)
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, errors.New("store: expected string key")
		}
		if _, dup := m[key]; dup {
			return nil, fmt.Errorf("store: duplicate key %q", key)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("store: field %q: %w", key, err)
		}
		m[key] = v
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("store: unexpected trailing JSON value")
	}
	return m, nil
}

func unmarshalJSON(raw json.RawMessage, v any) error { return json.Unmarshal(raw, v) }

func jsonIsNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

// ---------------------------------------------------------------------------
// Base64 / hash helpers
// ---------------------------------------------------------------------------

func rawURLBase64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func paddedBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func decodePaddedBase64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

func decodeRawURLBase64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func newEventHash() hash.Hash { return sha256.New() }

const snapshotHashDomain = "amagi-codebox/device-snapshot/v1"

// ---------------------------------------------------------------------------
// Credential hash (design §9.3)
// ---------------------------------------------------------------------------

// computeDeviceDigest = SHA-256(domain || 0x00 || salt[16] || secret[32]).
func computeDeviceDigest(salt, secret []byte) []byte {
	h := sha256.New()
	h.Write([]byte(deviceCredentialDomain))
	h.Write([]byte{0x00})
	h.Write(salt)
	h.Write(secret)
	return h.Sum(nil)
}

// verifyDeviceDigest recomputes and constant-time-compares the digest.
func verifyDeviceDigest(salt, secret, storedHash []byte) bool {
	if len(storedHash) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(computeDeviceDigest(salt, secret), storedHash) == 1
}

// generateDeviceID returns a 16-byte RawURL DeviceID (22 chars).
func generateDeviceID(r io.Reader) (contract.DeviceID, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return contract.DeviceID(rawURLBase64(buf)), nil
}

func generateDeviceSecret(r io.Reader) ([]byte, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func generateDeviceSalt(r io.Reader) ([]byte, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// generateUniqueDeviceID generates a DeviceID not in the current known set,
// resampling up to 3 times on collision.
func generateUniqueDeviceID(r io.Reader, known func(contract.DeviceID) bool) (contract.DeviceID, error) {
	for i := 0; i < 3; i++ {
		id, err := generateDeviceID(r)
		if err != nil {
			return "", err
		}
		if !known(id) {
			return id, nil
		}
	}
	return "", closedStoreErr(storeErrEntropy)
}

// ---------------------------------------------------------------------------
// Repository interface (design §6.3)
// ---------------------------------------------------------------------------

type revokeStoreResult struct {
	Device         deviceRecord
	AlreadyRevoked bool
	LedgerSequence uint64
	Mutation       StoreMutationResult
}

type deviceRepository interface {
	LoadOrInitialize(normalStorePermit) error
	Lookup(normalStorePermit, contract.DeviceID) (deviceRecord, bool, error)
	List(normalStorePermit) ([]deviceRecord, error)
	Create(normalStorePermit, deviceRecord) (StoreMutationResult, error)
	Revoke(normalStorePermit, contract.DeviceID, time.Time) (revokeStoreResult, error)
	TouchSeen(normalStorePermit, deviceSeenObservation, time.Duration) (deviceRecord, seenStoreDisposition, StoreMutationResult, error)
	BackupForMigration(MaintenanceSession) (DeviceStoreBackup, error)
	RestoreMigrationBackup(MaintenanceSession, DeviceStoreBackup) error
	ValidateMaintenanceStore(MaintenanceSession) error
}

// ---------------------------------------------------------------------------
// fileDeviceStore
// ---------------------------------------------------------------------------

const (
	knownDeviceIDCap   = 1024 // C-010
	snapshotFilename   = "devices.json"
	ledgerFilename     = "devices.revocations.wal"
	credentialSchemeV1 = "sha256-salt-v1"
)

// fileDeviceStore is the production device repository.
type fileDeviceStore struct {
	dir          string
	snapshotPath string
	ledgerPath   string
	backupDir    string
	clock        Clock
	random       io.Reader
	gate         *securityMaintenanceGate
	processNonce [32]byte

	mu                  sync.Mutex // storeMu
	ready               bool       // securityReady latch
	storeID             string
	devices             map[contract.DeviceID]deviceRecord
	ledger              *revocationLedger
	tempNamespaceUnsafe bool
	maintenanceBaseline maintenanceBaseline
}

// newFileDeviceStore constructs a not-ready store bound to the given gate.
func newFileDeviceStore(dir string, clock Clock, random io.Reader, gate *securityMaintenanceGate) *fileDeviceStore {
	return &fileDeviceStore{
		dir:          dir,
		snapshotPath: filepath.Join(dir, snapshotFilename),
		ledgerPath:   filepath.Join(dir, ledgerFilename),
		backupDir:    filepath.Join(dir, "device-store-backups"),
		clock:        clock,
		random:       random,
		gate:         gate,
		processNonce: gate.nonce(),
		devices:      make(map[contract.DeviceID]deviceRecord),
	}
}

// nonce returns the immutable per-process nonce (no lock required).
func (s *fileDeviceStore) nonce() [32]byte { return s.processNonce }

// validateNormalPermit validates a normal permit WITHOUT acquiring the gate
// lock. The nonce is immutable; kind is checked. The gate guarantees it cannot
// leave normal while any normal permit is outstanding.
func (s *fileDeviceStore) validateNormalPermit(p normalStorePermit) error {
	if p.kind != permitNormal || p.nonce != s.processNonce {
		return errSecurityNotReady
	}
	return nil
}

// Ready reports the securityReady latch (diagnostic).
func (s *fileDeviceStore) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// latchReady latches the security face unavailable (no file mutation).
func (s *fileDeviceStore) latchReady() {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
}

// AdmitNewDevice checks C-010 admission for a future pair without writing. It
// verifies the known-ID cap, WAL reserve and temp namespace safety.
func (s *fileDeviceStore) AdmitNewDevice(p normalStorePermit) error {
	if err := s.validateNormalPermit(p); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return errSecurityNotReady
	}
	if s.knownDeviceCountLocked() >= knownDeviceIDCap {
		return SecurityCapacityError{Code: CapacityKnownDeviceIDs}
	}
	if s.ledger.committedTombstoneCount()+1 > LedgerCommittedTombstoneMax {
		return SecurityCapacityError{Code: CapacityRevocationReserve}
	}
	if s.ledger.fileSize+int64(2*(LedgerLineMaxBytes+1)) > LedgerMaxBytes {
		return SecurityCapacityError{Code: CapacityRevocationReserve}
	}
	if s.tempNamespaceUnsafe {
		return SecurityCapacityError{Code: CapacitySnapshotTemps}
	}
	return nil
}

// knownDeviceCountLocked returns |snapshot IDs ∪ tombstone IDs|.
func (s *fileDeviceStore) knownDeviceCountLocked() int {
	n := len(s.devices)
	for id := range s.ledger.tombstones {
		if _, ok := s.devices[contract.DeviceID(id)]; !ok {
			n++
		}
	}
	return n
}

// admissionCapacityLocked enforces C-010 admission for a NEW pair: known IDs,
// candidate snapshot bytes, and WAL reserve for the device's future single
// revoke. Caps only block new pair/window.
func (s *fileDeviceStore) admissionCapacityLocked(nextBytes []byte) error {
	if s.knownDeviceCountLocked() >= knownDeviceIDCap {
		return SecurityCapacityError{Code: CapacityKnownDeviceIDs}
	}
	if len(nextBytes) > MaxSnapshotBytes {
		return SecurityCapacityError{Code: CapacitySnapshotBytes}
	}
	// Reserve for this device's future single revoke (2 lines).
	if s.ledger.committedTombstoneCount()+1 > LedgerCommittedTombstoneMax {
		return SecurityCapacityError{Code: CapacityRevocationReserve}
	}
	if s.ledger.fileSize+int64(2*(LedgerLineMaxBytes+1)) > LedgerMaxBytes {
		return SecurityCapacityError{Code: CapacityRevocationReserve}
	}
	return nil
}

// tempBudgetOKLocked reports whether a new snapshot temp may be created.
func (s *fileDeviceStore) tempBudgetOKLocked(nextBytes []byte) bool {
	if s.tempNamespaceUnsafe {
		return false
	}
	scan := startupTempCleanup(s.dir, s.storeID)
	if scan.unsafe || scan.cleanupFailed {
		s.tempNamespaceUnsafe = scan.unsafe
		return false
	}
	// In-flight reservation counts as one additional file/bytes.
	count := scan.leftoverCount + 1
	bytes := scan.leftoverBytes + int64(len(nextBytes))
	return count <= MaxSnapshotTempFiles && bytes <= MaxSnapshotTempBytes
}

// ---------------------------------------------------------------------------
// LoadOrInitialize (design §7.4)
// ---------------------------------------------------------------------------

func (s *fileDeviceStore) LoadOrInitialize(_ normalStorePermit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadOrInitializeLocked()
}

func (s *fileDeviceStore) loadOrInitializeLocked() error {
	_, ledgerMissing := os.Stat(s.ledgerPath)
	_, snapMissing := os.Stat(s.snapshotPath)
	ledgerAbsent := os.IsNotExist(ledgerMissing)
	snapAbsent := os.IsNotExist(snapMissing)

	// Sole fresh-store condition: both absent.
	if ledgerAbsent && snapAbsent {
		return s.initializeFreshLocked()
	}
	// Header-only interrupted init: ledger valid header present, snapshot absent.
	if snapAbsent && !ledgerAbsent {
		if err := s.recoverHeaderOnlyLocked(); err != nil {
			s.ready = false
			return err
		}
		s.ready = true
		return nil
	}
	// Any other single-file-missing combination latches.
	if ledgerAbsent != snapAbsent {
		s.ready = false
		return closedStoreErr(storeErrSchema)
	}
	// Both present: load + reconcile.
	led, err := loadLedger(s.ledgerPath, "") // storeID learned from header below
	if err != nil {
		s.ready = false
		return err
	}
	snap, err := parseSnapshotFile(s.snapshotPath, led.storeID)
	if err != nil {
		s.ready = false
		return err
	}
	if err := s.reconcileLocked(led, snap); err != nil {
		s.ready = false
		return err
	}
	s.ready = true
	return nil
}

// initializeFreshLocked creates the ledger header + empty snapshot, each
// create/Sync/close/reopen-validated.
func (s *fileDeviceStore) initializeFreshLocked() error {
	storeID, err := generateStoreID(s.random)
	if err != nil {
		s.ready = false
		return err
	}
	createdAt := s.clock.Now().UTC()
	if err := initializeLedger(s.ledgerPath, string(storeID), createdAt); err != nil {
		s.ready = false
		return err
	}
	led, err := loadLedger(s.ledgerPath, string(storeID))
	if err != nil {
		s.ready = false
		return err
	}
	// Empty snapshot at sequence 0 with headHash = header hash.
	empty := snapshotView{
		StoreID:        string(storeID),
		LedgerSequence: 0,
		LedgerHeadHash: led.headerHash,
		Devices:        []snapshotDeviceJSON{},
	}
	bytes, err := marshalSnapshot(empty)
	if err != nil {
		s.ready = false
		return err
	}
	if err := writeSnapshotAtomic(s.snapshotPath, bytes); err != nil {
		s.ready = false
		return err
	}
	snap, err := parseSnapshotFile(s.snapshotPath, string(storeID))
	if err != nil {
		s.ready = false
		return err
	}
	if err := s.reconcileLocked(led, snap); err != nil {
		s.ready = false
		return err
	}
	s.ready = true
	return nil
}

// recoverHeaderOnlyLocked handles a valid header-only ledger with missing
// snapshot (provable interrupted init): create an empty snapshot for the same
// StoreID, close/reopen validate.
func (s *fileDeviceStore) recoverHeaderOnlyLocked() error {
	// Load ledger with storeID discovered from header.
	raw, err := os.ReadFile(s.ledgerPath)
	if err != nil {
		return err
	}
	lines, err := splitLedgerLines(raw)
	if err != nil || len(lines) == 0 {
		return closedStoreErr(storeErrSchema)
	}
	var header ledgerHeaderLine
	if err := strictParseLine(lines[0], &header, "header"); err != nil {
		return err
	}
	led, err := loadLedger(s.ledgerPath, header.StoreID)
	if err != nil {
		return err
	}
	if led.lastSeq != 0 || len(led.tombstones) != 0 {
		return closedStoreErr(storeErrSchema)
	}
	empty := snapshotView{
		StoreID:        header.StoreID,
		LedgerSequence: 0,
		LedgerHeadHash: led.headerHash,
		Devices:        []snapshotDeviceJSON{},
	}
	bytes, err := marshalSnapshot(empty)
	if err != nil {
		return err
	}
	if err := writeSnapshotAtomic(s.snapshotPath, bytes); err != nil {
		return err
	}
	snap, err := parseSnapshotFile(s.snapshotPath, header.StoreID)
	if err != nil {
		return err
	}
	return s.reconcileLocked(led, snap)
}

// reconcileLocked fuses the ledger authority with the snapshot projection.
func (s *fileDeviceStore) reconcileLocked(led *revocationLedger, snap *snapshotView) error {
	s.storeID = led.storeID
	s.ledger = led
	s.devices = make(map[contract.DeviceID]deviceRecord)

	// Snapshot must not be ahead of the ledger.
	if snap.LedgerSequence > led.lastSeq {
		return closedStoreErr(storeErrSchema)
	}
	// Snapshot headHash must match the chain at its declared sequence.
	if hh, ok := led.seqHash[snap.LedgerSequence]; !ok || hh != snap.LedgerHeadHash {
		return closedStoreErr(storeErrSchema)
	}

	// Build device records from snapshot.
	seen := make(map[string]bool)
	for _, d := range snap.Devices {
		rec, err := parseSnapshotDevice(d)
		if err != nil {
			return err
		}
		if seen[string(rec.ID)] {
			return closedStoreErr(storeErrSchema)
		}
		seen[string(rec.ID)] = true
		// If the snapshot claims revoked, the ledger must have a matching
		// tombstone at/before the snapshot sequence with equal revokedAt.
		if rec.RevokedAt != nil {
			t, ok := led.tombstones[string(rec.ID)]
			if !ok || t.sequence > snap.LedgerSequence || !t.revokedAt.Equal(*rec.RevokedAt) {
				return closedStoreErr(storeErrSchema)
			}
		}
		s.devices[rec.ID] = rec
	}
	// Apply ledger authority: a device with a tombstone is revoked regardless of
	// snapshot projection.
	for id, t := range led.tombstones {
		rec, ok := s.devices[contract.DeviceID(id)]
		if ok {
			ra := t.revokedAt
			rec.RevokedAt = &ra
			s.devices[contract.DeviceID(id)] = rec
		}
		// Tombstone for an ID absent from snapshot: reserved revoked-only known ID.
	}
	// Startup temp namespace cleanup.
	scan := startupTempCleanup(s.dir, s.storeID)
	s.tempNamespaceUnsafe = scan.unsafe
	return nil
}

// generateStoreID returns a 16-byte RawURL store ID.
func generateStoreID(r io.Reader) (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return rawURLBase64(buf), nil
}

// writeSnapshotAtomic writes snapshot bytes via O_EXCL temp + Sync + rename +
// read-back; used for initial creation only (not the three-state mutation path).
func writeSnapshotAtomic(path string, bytes []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".init-snap-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(bytes); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytesEqual(got, bytes) {
		return closedStoreErr(storeErrIndeterminate)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Lookup / List
// ---------------------------------------------------------------------------

func (s *fileDeviceStore) Lookup(p normalStorePermit, id contract.DeviceID) (deviceRecord, bool, error) {
	if err := s.validateNormalPermit(p); err != nil {
		return deviceRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return deviceRecord{}, false, errSecurityNotReady
	}
	rec, ok := s.devices[id]
	return rec, ok, nil
}

func (s *fileDeviceStore) List(p normalStorePermit) ([]deviceRecord, error) {
	if err := s.validateNormalPermit(p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return nil, errSecurityNotReady
	}
	out := make([]deviceRecord, 0, len(s.devices))
	for _, rec := range s.devices {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// knownIDs returns the current known DeviceID set (for collision checks).
func (s *fileDeviceStore) knownIDs() map[contract.DeviceID]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[contract.DeviceID]bool, len(s.devices)+len(s.ledger.tombstones))
	for id := range s.devices {
		set[id] = true
	}
	for id := range s.ledger.tombstones {
		set[contract.DeviceID(id)] = true
	}
	return set
}

// ---------------------------------------------------------------------------
// Create (pair)
// ---------------------------------------------------------------------------

func (s *fileDeviceStore) Create(p normalStorePermit, rec deviceRecord) (StoreMutationResult, error) {
	if err := s.validateNormalPermit(p); err != nil {
		return StoreMutationResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return StoreMutationResult{}, errSecurityNotReady
	}
	if _, exists := s.devices[rec.ID]; exists {
		return StoreMutationResult{}, errSecurityNotReady
	}
	if _, ok := s.ledger.tombstones[string(rec.ID)]; ok {
		return StoreMutationResult{}, errSecurityNotReady
	}
	oldBytes, err := s.marshalCurrentLocked()
	if err != nil {
		s.latchLocked()
		return StoreMutationResult{State: StoreIndeterminateFailClosed}, closedStoreErr(storeErrIndeterminate)
	}
	nextBytes, err := s.marshalWithDeviceLocked(rec)
	if err != nil {
		s.latchLocked()
		return StoreMutationResult{State: StoreIndeterminateFailClosed}, closedStoreErr(storeErrIndeterminate)
	}
	if capErr := s.admissionCapacityLocked(nextBytes); capErr != nil {
		return StoreMutationResult{}, capErr
	}
	if !s.tempBudgetOKLocked(nextBytes) {
		return StoreMutationResult{}, SecurityCapacityError{Code: CapacitySnapshotTemps}
	}
	res, _ := replaceSnapshot(s.snapshotPath, oldBytes, nextBytes, s.random)
	return s.applyCreateLocked(rec, nextBytes, res)
}

func (s *fileDeviceStore) applyCreateLocked(rec deviceRecord, nextBytes []byte, res snapshotReconcileResult) (StoreMutationResult, error) {
	switch res.state {
	case StoreCommitted:
		s.devices[rec.ID] = rec
		s.cleanupTempLocked(res)
		_ = syncParentDir(s.dir) // evidence only; never a commit condition
		return StoreMutationResult{State: StoreCommitted}, nil
	case StoreNotCommitted:
		s.cleanupTempLocked(res)
		return StoreMutationResult{State: StoreNotCommitted}, nil
	default:
		s.latchLocked()
		s.cleanupTempLocked(res)
		return StoreMutationResult{State: StoreIndeterminateFailClosed}, closedStoreErr(storeErrIndeterminate)
	}
}

// ---------------------------------------------------------------------------
// Revoke (design §11.1: ledger Sync → projection)
// ---------------------------------------------------------------------------

func (s *fileDeviceStore) Revoke(p normalStorePermit, id contract.DeviceID, at time.Time) (revokeStoreResult, error) {
	if err := s.validateNormalPermit(p); err != nil {
		return revokeStoreResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return revokeStoreResult{}, errSecurityNotReady
	}
	rec, ok := s.devices[id]
	if !ok {
		// Unknown device: not revocable.
		return revokeStoreResult{}, errSecurityNotReady
	}
	if t, revoked := s.ledger.tombstones[string(id)]; revoked {
		// Already revoked: no ledger write; original sequence.
		return revokeStoreResult{Device: rec, AlreadyRevoked: true, LedgerSequence: t.sequence,
			Mutation: StoreMutationResult{}}, nil
	}
	ar, err := s.ledger.appendRevoke(string(id), at)
	if err != nil && ar.mutation.State != StoreNotCommitted && ar.mutation.State != StoreIndeterminateFailClosed {
		// Unexpected error category.
		s.latchLocked()
		return revokeStoreResult{Mutation: StoreMutationResult{State: StoreIndeterminateFailClosed}}, closedStoreErr(storeErrIndeterminate)
	}
	switch ar.mutation.State {
	case StoreNotCommitted:
		return revokeStoreResult{Device: rec, Mutation: ar.mutation}, nil
	case StoreIndeterminateFailClosed:
		s.latchLocked()
		return revokeStoreResult{Device: rec, Mutation: ar.mutation}, closedStoreErr(storeErrIndeterminate)
	}
	// Committed: publish revoked to memory, then best-effort projection rewrite.
	// Projection failure NEVER downgrades the revoke (DurabilityDegraded only).
	oldBytes, omErr := s.marshalCurrentLocked()
	if omErr != nil {
		return revokeStoreResult{Device: rec, LedgerSequence: ar.sequence,
			Mutation: StoreMutationResult{State: StoreCommitted, DurabilityDegraded: true}}, nil
	}
	ra := at.UTC()
	rec.RevokedAt = &ra
	s.devices[id] = rec
	nextBytes, _ := s.marshalCurrentLocked()
	degraded := false
	if len(nextBytes) == 0 || len(nextBytes) > MaxSnapshotBytes || !s.tempBudgetOKLocked(nextBytes) {
		degraded = true
	} else {
		res, _ := replaceSnapshot(s.snapshotPath, oldBytes, nextBytes, s.random)
		switch res.state {
		case StoreCommitted:
			s.cleanupTempLocked(res)
			_ = syncParentDir(s.dir)
		case StoreNotCommitted:
			degraded = true
			s.cleanupTempLocked(res)
		default:
			degraded = true
			s.latchLocked()
			s.cleanupTempLocked(res)
		}
	}
	return revokeStoreResult{Device: rec, LedgerSequence: ar.sequence,
		Mutation: StoreMutationResult{State: StoreCommitted, DurabilityDegraded: degraded}}, nil
}

// ---------------------------------------------------------------------------
// TouchSeen (design §9.5)
// ---------------------------------------------------------------------------

func (s *fileDeviceStore) TouchSeen(p normalStorePermit, obs deviceSeenObservation, interval time.Duration) (deviceRecord, seenStoreDisposition, StoreMutationResult, error) {
	if err := s.validateNormalPermit(p); err != nil {
		return deviceRecord{}, seenSkippedRevoked, StoreMutationResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return deviceRecord{}, seenSkippedRevoked, StoreMutationResult{}, errSecurityNotReady
	}
	rec, ok := s.devices[obs.DeviceID]
	if !ok || rec.RevokedAt != nil {
		return rec, seenSkippedRevoked, StoreMutationResult{}, nil
	}
	if !obs.CredentialExpiresAt.Equal(rec.CredentialExpiresAt) || !obs.AuthenticatedAt.Before(rec.CredentialExpiresAt) {
		return rec, seenSkippedExpired, StoreMutationResult{}, nil
	}
	if !obs.AuthenticatedAt.After(rec.LastSeenAt) || obs.AuthenticatedAt.Sub(rec.LastSeenAt) < interval {
		return rec, seenCoalesced, StoreMutationResult{}, nil
	}
	oldBytes, err := s.marshalCurrentLocked()
	if err != nil {
		s.latchLocked()
		return rec, seenPersist, StoreMutationResult{State: StoreIndeterminateFailClosed}, closedStoreErr(storeErrIndeterminate)
	}
	oldRec := rec // pre-update record, for rollback on NotCommitted
	rec.LastSeenAt = obs.AuthenticatedAt.UTC()
	// Update in-memory state BEFORE marshaling nextBytes so the persisted bytes
	// reflect the new LastSeenAt (otherwise the snapshot would lag memory by one
	// update and the next reconcile would classify as Indeterminate).
	s.devices[obs.DeviceID] = rec
	nextBytes, err := s.marshalCurrentLocked()
	if err != nil {
		s.devices[obs.DeviceID] = oldRec
		s.latchLocked()
		return rec, seenPersist, StoreMutationResult{State: StoreIndeterminateFailClosed}, closedStoreErr(storeErrIndeterminate)
	}
	if !s.tempBudgetOKLocked(nextBytes) {
		s.devices[obs.DeviceID] = oldRec
		return rec, seenPersist, StoreMutationResult{}, SecurityCapacityError{Code: CapacitySnapshotTemps}
	}
	res, _ := replaceSnapshot(s.snapshotPath, oldBytes, nextBytes, s.random)
	switch res.state {
	case StoreCommitted:
		s.cleanupTempLocked(res)
		_ = syncParentDir(s.dir)
		return rec, seenPersist, StoreMutationResult{State: StoreCommitted}, nil
	case StoreNotCommitted:
		s.devices[obs.DeviceID] = oldRec // rollback: disk proved unchanged
		s.cleanupTempLocked(res)
		return oldRec, seenPersist, StoreMutationResult{State: StoreNotCommitted}, nil
	default:
		s.latchLocked()
		s.cleanupTempLocked(res)
		return rec, seenPersist, StoreMutationResult{State: StoreIndeterminateFailClosed}, closedStoreErr(storeErrIndeterminate)
	}
}

// ---------------------------------------------------------------------------
// Internal marshaling / latch / cleanup
// ---------------------------------------------------------------------------

// latchLocked permanently marks the security face unavailable for this run.
func (s *fileDeviceStore) latchLocked() { s.ready = false }

// cleanupTempLocked records a failed temp cleanup (replaceSnapshot already
// cleaned the exact temp for Committed/NotCommitted; Indeterminate retains it).
func (s *fileDeviceStore) cleanupTempLocked(res snapshotReconcileResult) {
	if res.cleanupFailed {
		s.tempNamespaceUnsafe = true
	}
}

// marshalCurrentLocked returns the canonical bytes of the current in-memory view.
func (s *fileDeviceStore) marshalCurrentLocked() ([]byte, error) {
	return s.marshalViewLocked(nil, false)
}

// marshalWithDeviceLocked returns canonical bytes including an additional device.
func (s *fileDeviceStore) marshalWithDeviceLocked(rec deviceRecord) ([]byte, error) {
	return s.marshalViewLocked(&rec, true)
}

// marshalViewLocked marshals the snapshot. If add is true, rec is included.
func (s *fileDeviceStore) marshalViewLocked(add *deviceRecord, include bool) ([]byte, error) {
	devs := make([]snapshotDeviceJSON, 0, len(s.devices)+1)
	for _, rec := range s.devices {
		devs = append(devs, deviceRecordToJSON(rec))
	}
	if include && add != nil {
		devs = append(devs, deviceRecordToJSON(*add))
	}
	view := snapshotView{
		StoreID:        s.storeID,
		LedgerSequence: s.ledger.lastSeq,
		LedgerHeadHash: s.ledger.headHash,
		Devices:        devs,
	}
	return marshalSnapshot(view)
}

// ---------------------------------------------------------------------------
// Snapshot schema v1 (design §7.2): strict parse + canonical marshal
// ---------------------------------------------------------------------------

type snapshotDeviceJSON struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	CredentialSalt      string  `json:"credentialSalt"`
	CredentialHash      string  `json:"credentialHash"`
	PairedAt            string  `json:"pairedAt"`
	LastSeenAt          string  `json:"lastSeenAt"`
	CredentialExpiresAt string  `json:"credentialExpiresAt"`
	RevokedAt           *string `json:"revokedAt,omitempty"`
}

type snapshotRootJSON struct {
	Version          int                  `json:"version"`
	StoreID          string               `json:"storeId"`
	CredentialScheme string               `json:"credentialScheme"`
	LedgerSequence   uint64               `json:"ledgerSequence"`
	LedgerHeadHash   string               `json:"ledgerHeadHash"`
	Devices          []snapshotDeviceJSON `json:"devices"`
	SnapshotHash     string               `json:"snapshotHash"`
}

type snapshotHashInput struct {
	Version          int                  `json:"version"`
	StoreID          string               `json:"storeId"`
	CredentialScheme string               `json:"credentialScheme"`
	LedgerSequence   uint64               `json:"ledgerSequence"`
	LedgerHeadHash   string               `json:"ledgerHeadHash"`
	Devices          []snapshotDeviceJSON `json:"devices"`
}

// snapshotView is the parsed/constructed projection used internally.
type snapshotView struct {
	StoreID          string
	LedgerSequence   uint64
	LedgerHeadHash   string
	Devices          []snapshotDeviceJSON
	CredentialScheme string
}

// marshalSnapshot canonicalizes: devices sorted by ID, 2-space indent, final LF.
// snapshotHash proves bytes self-consistency ONLY (never "latest").
func marshalSnapshot(v snapshotView) ([]byte, error) {
	devs := make([]snapshotDeviceJSON, len(v.Devices))
	copy(devs, v.Devices)
	sort.Slice(devs, func(i, j int) bool { return devs[i].ID < devs[j].ID })
	scheme := v.CredentialScheme
	if scheme == "" {
		scheme = credentialSchemeV1
	}
	hi, err := json.Marshal(snapshotHashInput{
		Version: 1, StoreID: v.StoreID, CredentialScheme: scheme,
		LedgerSequence: v.LedgerSequence, LedgerHeadHash: v.LedgerHeadHash, Devices: devs,
	})
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write([]byte(snapshotHashDomain))
	h.Write([]byte{0x00})
	h.Write(hi)
	root := snapshotRootJSON{
		Version: 1, StoreID: v.StoreID, CredentialScheme: scheme,
		LedgerSequence: v.LedgerSequence, LedgerHeadHash: v.LedgerHeadHash,
		Devices: devs, SnapshotHash: paddedBase64(h.Sum(nil)),
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// parseSnapshotFile reads + strictly validates a snapshot file.
func parseSnapshotFile(path, expectedStoreID string) (*snapshotView, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxSnapshotBytes {
		return nil, closedStoreErr(storeErrSchema)
	}
	return parseSnapshotBytes(raw, expectedStoreID)
}

// parseSnapshotBytes strictly validates snapshot bytes and returns the view.
func parseSnapshotBytes(raw []byte, expectedStoreID string) (*snapshotView, error) {
	f, err := strictJSONObject(raw)
	if err != nil {
		return nil, closedStoreErr(storeErrSchema)
	}
	if err := rejectUnknownStore(f, "version", "storeId", "credentialScheme", "ledgerSequence", "ledgerHeadHash", "devices", "snapshotHash"); err != nil {
		return nil, closedStoreErr(storeErrSchema)
	}
	version, err := reqInt(f, "version")
	if err != nil || version != 1 {
		return nil, closedStoreErr(storeErrSchema)
	}
	storeID, err := reqStr(f, "storeId")
	if err != nil || !validRawURLID(storeID) {
		return nil, closedStoreErr(storeErrSchema)
	}
	if expectedStoreID != "" && storeID != expectedStoreID {
		return nil, closedStoreErr(storeErrSchema)
	}
	scheme, err := reqStr(f, "credentialScheme")
	if err != nil || scheme != credentialSchemeV1 {
		return nil, closedStoreErr(storeErrSchema)
	}
	seq, err := reqUint(f, "ledgerSequence")
	if err != nil {
		return nil, closedStoreErr(storeErrSchema)
	}
	headHash, err := reqStr(f, "ledgerHeadHash")
	if err != nil || !validPaddedHash(headHash) {
		return nil, closedStoreErr(storeErrSchema)
	}
	devsRaw, ok := f["devices"]
	if !ok || jsonIsNull(devsRaw) {
		return nil, closedStoreErr(storeErrSchema)
	}
	var devsArr []json.RawMessage
	if err := json.Unmarshal(devsRaw, &devsArr); err != nil {
		return nil, closedStoreErr(storeErrSchema)
	}
	devs := make([]snapshotDeviceJSON, 0, len(devsArr))
	seenDev := make(map[string]bool)
	for _, dr := range devsArr {
		df, err := strictJSONObject(dr)
		if err != nil {
			return nil, closedStoreErr(storeErrSchema)
		}
		if err := rejectUnknownStore(df, "id", "name", "credentialSalt", "credentialHash", "pairedAt", "lastSeenAt", "credentialExpiresAt", "revokedAt"); err != nil {
			return nil, closedStoreErr(storeErrSchema)
		}
		dj, err := parseOneDevice(df)
		if err != nil {
			return nil, closedStoreErr(storeErrSchema)
		}
		if seenDev[dj.ID] {
			return nil, closedStoreErr(storeErrSchema)
		}
		seenDev[dj.ID] = true
		devs = append(devs, dj)
	}
	declaredHash, err := reqStr(f, "snapshotHash")
	if err != nil || !validPaddedHash(declaredHash) {
		return nil, closedStoreErr(storeErrSchema)
	}
	// Recompute snapshotHash and verify self-consistency.
	sortedDevs := make([]snapshotDeviceJSON, len(devs))
	copy(sortedDevs, devs)
	sort.Slice(sortedDevs, func(i, j int) bool { return sortedDevs[i].ID < sortedDevs[j].ID })
	hi, _ := json.Marshal(snapshotHashInput{1, storeID, scheme, seq, headHash, sortedDevs})
	h := sha256.New()
	h.Write([]byte(snapshotHashDomain))
	h.Write([]byte{0x00})
	h.Write(hi)
	if paddedBase64(h.Sum(nil)) != declaredHash {
		return nil, closedStoreErr(storeErrSchema)
	}
	return &snapshotView{StoreID: storeID, LedgerSequence: seq, LedgerHeadHash: headHash, Devices: devs, CredentialScheme: scheme}, nil
}

func parseOneDevice(f map[string]json.RawMessage) (snapshotDeviceJSON, error) {
	id, err := reqStr(f, "id")
	if err != nil || !validRawURLID(id) {
		return snapshotDeviceJSON{}, errors.New("invalid id")
	}
	name, err := reqStr(f, "name")
	if err != nil {
		return snapshotDeviceJSON{}, errors.New("invalid name")
	}
	salt, err := reqStr(f, "credentialSalt")
	if err != nil || !validPaddedLen(salt, 16) {
		return snapshotDeviceJSON{}, errors.New("invalid salt")
	}
	hash, err := reqStr(f, "credentialHash")
	if err != nil || !validPaddedLen(hash, 32) {
		return snapshotDeviceJSON{}, errors.New("invalid hash")
	}
	pairedAt, err := reqStr(f, "pairedAt")
	if err != nil || !validUTCNano(pairedAt) {
		return snapshotDeviceJSON{}, errors.New("invalid pairedAt")
	}
	lastSeenAt, err := reqStr(f, "lastSeenAt")
	if err != nil || !validUTCNano(lastSeenAt) {
		return snapshotDeviceJSON{}, errors.New("invalid lastSeenAt")
	}
	expiresAt, err := reqStr(f, "credentialExpiresAt")
	if err != nil || !validUTCNano(expiresAt) {
		return snapshotDeviceJSON{}, errors.New("invalid expiresAt")
	}
	dj := snapshotDeviceJSON{ID: id, Name: name, CredentialSalt: salt, CredentialHash: hash,
		PairedAt: pairedAt, LastSeenAt: lastSeenAt, CredentialExpiresAt: expiresAt}
	// Optional revokedAt: present-and-non-null only.
	if rv, ok := f["revokedAt"]; ok {
		if jsonIsNull(rv) {
			return snapshotDeviceJSON{}, errors.New("revokedAt must not be null")
		}
		var rvs string
		if err := json.Unmarshal(rv, &rvs); err != nil || !validUTCNano(rvs) {
			return snapshotDeviceJSON{}, errors.New("invalid revokedAt")
		}
		dj.RevokedAt = &rvs
	}
	return dj, nil
}

// parseSnapshotDevice converts a strict JSON device to a record, validating
// temporal invariants.
func parseSnapshotDevice(d snapshotDeviceJSON) (deviceRecord, error) {
	salt, err := decodePaddedBase64(d.CredentialSalt)
	if err != nil || len(salt) != 16 {
		return deviceRecord{}, errors.New("invalid salt")
	}
	hash, err := decodePaddedBase64(d.CredentialHash)
	if err != nil || len(hash) != 32 {
		return deviceRecord{}, errors.New("invalid hash")
	}
	pairedAt, err := parseUTCNano(d.PairedAt)
	if err != nil {
		return deviceRecord{}, err
	}
	lastSeenAt, err := parseUTCNano(d.LastSeenAt)
	if err != nil {
		return deviceRecord{}, err
	}
	expiresAt, err := parseUTCNano(d.CredentialExpiresAt)
	if err != nil {
		return deviceRecord{}, err
	}
	if lastSeenAt.Before(pairedAt) || !lastSeenAt.Before(expiresAt) {
		return deviceRecord{}, errors.New("invalid timestamps")
	}
	rec := deviceRecord{
		ID: contract.DeviceID(d.ID), Name: d.Name, CredentialSalt: salt, CredentialHash: hash,
		PairedAt: pairedAt, LastSeenAt: lastSeenAt, CredentialExpiresAt: expiresAt,
	}
	if d.RevokedAt != nil {
		rt, err := parseUTCNano(*d.RevokedAt)
		if err != nil || rt.Before(pairedAt) {
			return deviceRecord{}, errors.New("invalid revokedAt")
		}
		rec.RevokedAt = &rt
	}
	return rec, nil
}

func deviceRecordToJSON(rec deviceRecord) snapshotDeviceJSON {
	dj := snapshotDeviceJSON{
		ID:                  string(rec.ID),
		Name:                rec.Name,
		CredentialSalt:      paddedBase64(rec.CredentialSalt),
		CredentialHash:      paddedBase64(rec.CredentialHash),
		PairedAt:            rec.PairedAt.UTC().Format(time.RFC3339Nano),
		LastSeenAt:          rec.LastSeenAt.UTC().Format(time.RFC3339Nano),
		CredentialExpiresAt: rec.CredentialExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	if rec.RevokedAt != nil {
		s := rec.RevokedAt.UTC().Format(time.RFC3339Nano)
		dj.RevokedAt = &s
	}
	return dj
}

// ---------------------------------------------------------------------------
// Strict field helpers (store-local; closed errors only)
// ---------------------------------------------------------------------------

func rejectUnknownStore(f map[string]json.RawMessage, allowed ...string) error {
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

func reqStr(f map[string]json.RawMessage, k string) (string, error) {
	v, ok := f[k]
	if !ok || jsonIsNull(v) {
		return "", fmt.Errorf("missing %q", k)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", err
	}
	return s, nil
}

func reqInt(f map[string]json.RawMessage, k string) (int, error) {
	v, ok := f[k]
	if !ok || jsonIsNull(v) {
		return 0, fmt.Errorf("missing %q", k)
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		return 0, err
	}
	return strconvAtoi(n.String())
}

func reqUint(f map[string]json.RawMessage, k string) (uint64, error) {
	n, err := reqInt(f, k)
	if err != nil || n < 0 {
		return 0, err
	}
	return uint64(n), nil
}

func strconvAtoi(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// validRawURLID reports whether s is a canonical 22-char RawURL encoding of 16 bytes.
func validRawURLID(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(16) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 16 {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(b) == s // reject non-canonical trailing bits
}

// validPaddedHash reports whether s is a canonical 44-char padded base64 of 32 bytes.
func validPaddedHash(s string) bool {
	if len(s) != base64.StdEncoding.EncodedLen(32) {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return false
	}
	return base64.StdEncoding.EncodeToString(b) == s
}

// validPaddedLen reports whether s decodes to exactly n bytes via padded base64.
func validPaddedLen(s string, n int) bool {
	if len(s) != base64.StdEncoding.EncodedLen(n) {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != n {
		return false
	}
	return base64.StdEncoding.EncodeToString(b) == s
}

func validUTCNano(s string) bool {
	if !strings.HasSuffix(s, "Z") {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

func parseUTCNano(s string) (time.Time, error) {
	if !strings.HasSuffix(s, "Z") {
		return time.Time{}, errors.New("timestamp must be UTC Z")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
