package remote

// Exclusive device-store maintenance epoch (design §7.6). Authorization comes
// ONLY from a live server-owned 32-byte process nonce + a monotonic epoch + an
// unexported pointer-identity session/backup token. A struct copy, manifest or
// backupID can NEVER authorize Restore. While the gate is active/entering, any
// normal security attempt (pair/revoke/seen/register/Start/normal-Load) poisons
// the session and invalidates every backup handle; only Abort is then allowed.
// A stale/mismatched handle only invalidates itself and never affects the live
// session. Process restart leaves every prior handle dead. The same-session
// migration rollback only ever rolls back state that was NEVER visible to the
// normal security services (the gate blocks them for the whole epoch).

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// gateState enumerates the maintenance-gate states.
type gateState uint8

const (
	gateNormal gateState = iota + 1
	gateEntering
	gateActive
	gatePoisoned
)

// backupAuthority is the verifiable projection of a completed backup: the
// owned directory, the live baseline captured at backup time, and the sanitized
// info (BackupID + manifest SHA-256). It is the single input to the strict
// parseAndVerifyBackup path used after backup, before restore, before info, and
// before cleanup. A struct copy shares the same token capability (see gate),
// but the authority itself is plain data bound to its dir.
type backupAuthority struct {
	dir      string
	baseline maintenanceBaseline
	info     MigrationBackupInfo
}

// liveBackup is one in-memory backup capability bound to a live session. It
// embeds backupAuthority so dir/baseline/info are accessible directly while the
// full authority remains available for parseAndVerifyBackup.
type liveBackup struct {
	token *deviceStoreBackupToken
	backupAuthority
	createdAt time.Time
}

// maintenanceBaseline is the authoritative in-memory baseline captured at Begin
// / Backup time. Restore authorization depends on it + live session identity,
// never on a disk head comparison. snapshotHash is the SHA-256 of the live
// snapshot bytes at capture time.
type maintenanceBaseline struct {
	storeID        string
	ledgerHeadHash string
	ledgerLastSeq  uint64
	snapshotHash   string
	tombstones     map[string]ledgerTombstone
}

// liveMaintenanceSession is the single live session (non-nil iff active).
type liveMaintenanceSession struct {
	epoch          uint64
	sessionPtr     *maintenanceSessionToken
	backups        map[*deviceStoreBackupToken]*liveBackup
	backupReserved bool // single-slot reservation held before disk work
	writeGen       uint64
	storeWriteGen  uint64
}

// securityMaintenanceGate is the permit authority + maintenance state machine.
// It performs NO I/O and acquires no other lock.
type securityMaintenanceGate struct {
	mu             sync.Mutex
	processNonce   [32]byte
	epochCounter   uint64
	state          gateState
	normalInFlight int
	maintInFlight  int
	live           *liveMaintenanceSession
	// Process-local backup token→authority mapping for post-End cleanup. Retains
	// dir + baseline + MigrationBackupInfo so info/cleanup work after End.
	// Persists across session end (process-local, not session-scoped).
	cleanupRecords map[*deviceStoreBackupToken]cleanupRecord
}

// cleanupRecord retains everything needed to validate + clean a backup after
// the live session has ended.
type cleanupRecord struct {
	authority backupAuthority
}

// newSecurityMaintenanceGate constructs a gate in the normal-stopped state. The
// 32-byte process nonce is generated from crypto/rand; failure is a programmer
// error (security cannot become ready).
func newSecurityMaintenanceGate() *securityMaintenanceGate {
	g := &securityMaintenanceGate{state: gateNormal}
	if _, err := rand.Read(g.processNonce[:]); err != nil {
		panic("security: failed to generate process nonce")
	}
	return g
}

// nonce returns the immutable per-process nonce.
func (g *securityMaintenanceGate) nonce() [32]byte { return g.processNonce }

// issueNormalPermit signs a single normal-operation permit. Returns false if the
// gate is not in the normal state (maintenance epoch active / poisoned).
func (g *securityMaintenanceGate) issueNormalPermit() (normalStorePermit, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateNormal {
		return normalStorePermit{}, false
	}
	g.normalInFlight++
	return normalStorePermit{nonce: g.processNonce, epoch: g.epochCounter, kind: permitNormal}, true
}

// returnNormalPermit returns a normal permit.
func (g *securityMaintenanceGate) returnNormalPermit(p normalStorePermit) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p.nonce != g.processNonce {
		return
	}
	if g.normalInFlight > 0 {
		g.normalInFlight--
	}
}

// recordNormalSecurityAttempt is called when a normal security entry is
// attempted. If the gate is active/entering/poisoned, the session is poisoned
// (all backup handles invalidated) and the attempt is rejected. Returns true if
// the attempt was rejected/poisoned.
func (g *securityMaintenanceGate) recordNormalSecurityAttempt() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch g.state {
	case gateNormal:
		return false
	case gateActive, gateEntering:
		g.poisonLocked()
		return true
	default: // poisoned
		return true
	}
}

// poisonLocked invalidates the live session and all backup handles.
func (g *securityMaintenanceGate) poisonLocked() {
	g.state = gatePoisoned
	if g.live != nil {
		g.live.backups = nil
	}
}

// beginEnter transitions normal→entering iff no normal operation is in flight.
func (g *securityMaintenanceGate) beginEnter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateNormal || g.normalInFlight != 0 {
		return false
	}
	g.state = gateEntering
	return true
}

// abortEnter rolls back entering→normal.
func (g *securityMaintenanceGate) abortEnter() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == gateEntering {
		g.state = gateNormal
	}
}

// publishActive transitions entering→active and issues a session with a fresh
// epoch and unique pointer identity. It CAS-confirms the gate is still in the
// entering state; once poisoned it can never become active again.
func (g *securityMaintenanceGate) publishActive() (MaintenanceSession, uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateEntering {
		return MaintenanceSession{}, 0
	}
	g.epochCounter++
	if g.epochCounter == 0 { // overflow: maintenance permanently refused
		g.epochCounter--
		g.state = gateNormal
		return MaintenanceSession{}, 0
	}
	ptr := &maintenanceSessionToken{}
	g.live = &liveMaintenanceSession{
		epoch:      g.epochCounter,
		sessionPtr: ptr,
		backups:    make(map[*deviceStoreBackupToken]*liveBackup),
	}
	g.state = gateActive
	return MaintenanceSession{token: ptr}, g.epochCounter
}

// verifySession reports whether session matches the live active session.
func (g *securityMaintenanceGate) verifySession(session MaintenanceSession) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state == gateActive && g.live != nil && session.token != nil && session.token == g.live.sessionPtr
}

// enterMaintenance increments the maintenance in-flight for a verified session.
func (g *securityMaintenanceGate) enterMaintenance(session MaintenanceSession) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateActive || g.live == nil || session.token == nil || session.token != g.live.sessionPtr {
		return false
	}
	g.maintInFlight++
	return true
}

// exitMaintenance decrements the maintenance in-flight.
func (g *securityMaintenanceGate) exitMaintenance() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maintInFlight > 0 {
		g.maintInFlight--
	}
}

// beginBackup atomically verifies the session and reserves the single backup
// slot BEFORE any disk work. It is the one-backup-per-session gate checked
// ahead of all disk I/O (orphan scan, mkdir, copy). Returns false if the
// session is invalid or a backup already exists/is reserved. The matching
// releaseBackupReservation must be called on every failure path; registerBackup
// finalizes (clears the reservation) on success.
func (g *securityMaintenanceGate) beginBackup(session MaintenanceSession) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateActive || g.live == nil || session.token == nil || session.token != g.live.sessionPtr {
		return false
	}
	if len(g.live.backups) > 0 || g.live.backupReserved {
		return false
	}
	g.live.backupReserved = true
	return true
}

// releaseBackupReservation clears a pending single-slot reservation after a
// failed backup. Safe to call when no reservation is held.
func (g *securityMaintenanceGate) releaseBackupReservation(session MaintenanceSession) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.live != nil && session.token != nil && session.token == g.live.sessionPtr {
		g.live.backupReserved = false
	}
}

// registerBackup binds a new backup handle to the live session. It finalizes a
// beginBackup reservation: the reservation must be held, and no prior backup may
// exist. The cleanup record (full authority) is registered here so info/cleanup
// survive End.
func (g *securityMaintenanceGate) registerBackup(session MaintenanceSession, auth backupAuthority) (DeviceStoreBackup, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateActive || g.live == nil || session.token == nil || session.token != g.live.sessionPtr {
		return DeviceStoreBackup{}, false
	}
	// One backup per session: atomically reject if any backup already exists.
	if len(g.live.backups) > 0 {
		return DeviceStoreBackup{}, false
	}
	tok := &deviceStoreBackupToken{}
	// Register the cleanup authority (persists past End for explicit cleanup).
	if g.cleanupRecords == nil {
		g.cleanupRecords = make(map[*deviceStoreBackupToken]cleanupRecord)
	}
	g.cleanupRecords[tok] = cleanupRecord{authority: auth}
	g.live.backups[tok] = &liveBackup{token: tok, backupAuthority: auth, createdAt: time.Now().UTC()}
	g.live.backupReserved = false
	return DeviceStoreBackup{token: tok}, true
}

// lookupBackup returns the live backup for a handle, iff it is still bound to
// the live session. A stale/poisoned handle is not found.
func (g *securityMaintenanceGate) lookupBackup(session MaintenanceSession, backup DeviceStoreBackup) (*liveBackup, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateActive || g.live == nil || session.token == nil || session.token != g.live.sessionPtr {
		return nil, false
	}
	if backup.token == nil {
		return nil, false
	}
	lb, ok := g.live.backups[backup.token]
	return lb, ok
}

// bumpWriteGen records a migration write in the session.
func (g *securityMaintenanceGate) bumpWriteGen(session MaintenanceSession) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateActive || g.live == nil || session.token == nil || session.token != g.live.sessionPtr {
		return false
	}
	g.live.writeGen++
	g.live.storeWriteGen = g.live.writeGen
	return true
}

// end terminates an active session: clears the gate and kills all handles.
// Returns whether the session matched (and was thus ended).
func (g *securityMaintenanceGate) end(session MaintenanceSession) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != gateActive || g.live == nil || session.token == nil || session.token != g.live.sessionPtr {
		return false
	}
	g.live = nil
	g.state = gateNormal
	return true
}

// abort terminates a session unconditionally (active or poisoned). Returns
// whether the session pointer matched a live (active) session.
func (g *securityMaintenanceGate) abort(session MaintenanceSession) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	matched := g.live != nil && session.token != nil && session.token == g.live.sessionPtr
	g.live = nil
	g.state = gateNormal
	return matched
}

// isActive / isNormal / isPoisoned (diagnostic).
func (g *securityMaintenanceGate) isActive() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state == gateActive
}

// ---------------------------------------------------------------------------
// Store maintenance coordinator (repository-level private API)
// ---------------------------------------------------------------------------

// BeginMaintenance begins an exclusive maintenance epoch. The Server-level
// preconditions (server stopped, pairing suspended, registry empty) are checked
// by the phase2 Server wrapper BEFORE calling this; this method performs the
// gate transition + store-level strict validation and baseline capture.
func (s *fileDeviceStore) BeginMaintenance() (MaintenanceSession, error) {
	if !s.gate.beginEnter() {
		return MaintenanceSession{}, errSecurityNotReady
	}
	// Strict-validate current store set.
	s.mu.Lock()
	validateErr := s.validateCurrentSetLocked()
	var baseline maintenanceBaseline
	if validateErr == nil {
		baseline = s.captureBaselineLocked()
	}
	s.mu.Unlock()
	if validateErr != nil {
		s.gate.abortEnter()
		return MaintenanceSession{}, validateErr
	}
	session, epoch := s.gate.publishActive()
	if epoch == 0 {
		return MaintenanceSession{}, errSecurityNotReady
	}
	s.mu.Lock()
	s.maintenanceBaseline = baseline
	s.mu.Unlock()
	return session, nil
}

// validateCurrentSetLocked re-validates snapshot + ledger + temp budget. Caller
// holds storeMu. It performs NO gate-lock acquisition.
func (s *fileDeviceStore) validateCurrentSetLocked() error {
	if !s.ready {
		return errSecurityNotReady
	}
	led, err := loadLedger(s.ledgerPath, s.storeID)
	if err != nil {
		return err
	}
	snap, err := parseSnapshotFile(s.snapshotPath, s.storeID)
	if err != nil {
		return err
	}
	if snap.LedgerSequence > led.lastSeq {
		return closedStoreErr(storeErrSchema)
	}
	if hh, ok := led.seqHash[snap.LedgerSequence]; !ok || hh != snap.LedgerHeadHash {
		return closedStoreErr(storeErrSchema)
	}
	scan := startupTempCleanup(s.dir, s.storeID)
	if scan.unsafe {
		return closedStoreErr(storeErrSchema)
	}
	return nil
}

// captureBaselineLocked snapshots the authoritative baseline, including the
// SHA-256 of the current live snapshot bytes. Caller holds storeMu.
func (s *fileDeviceStore) captureBaselineLocked() maintenanceBaseline {
	tb := make(map[string]ledgerTombstone, len(s.ledger.tombstones))
	for k, v := range s.ledger.tombstones {
		tb[k] = v
	}
	snapshotHash := ""
	if raw, err := os.ReadFile(s.snapshotPath); err == nil {
		d := sha256.Sum256(raw)
		snapshotHash = hex.EncodeToString(d[:])
	}
	return maintenanceBaseline{
		storeID:        s.storeID,
		ledgerHeadHash: s.ledger.headHash,
		ledgerLastSeq:  s.ledger.lastSeq,
		snapshotHash:   snapshotHash,
		tombstones:     tb,
	}
}

// BackupForMigration creates a complete store-set backup bound to the live
// session. The single backup slot is reserved atomically in the gate BEFORE any
// disk work; the manifest is revalidated immediately via parseAndVerifyBackup
// before the capability is registered. File copy happens under storeMu.
// Failure cleanup removes ONLY the exact owned files + directory — never
// RemoveAll — so a tamper/fault cannot delete foreign entries.
func (s *fileDeviceStore) BackupForMigration(session MaintenanceSession) (DeviceStoreBackup, error) {
	// One-backup-per-session slot reserved atomically before disk work.
	if !s.gate.beginBackup(session) {
		return DeviceStoreBackup{}, errSecurityNotReady
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate backup root: Lstat regular dir, no symlink, 0700.
	if err := validateBackupRootLocked(s.backupDir); err != nil {
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	// Orphan detection: any existing entry in the backup root is a manual-repair
	// signal — never auto-delete/overwrite. ReadDir errors propagate (never ignored).
	entries, err := maintenanceReadDir(s.backupDir)
	if err != nil {
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	for _, e := range entries {
		if e.Name() != "." && e.Name() != ".." {
			s.gate.releaseBackupReservation(session)
			return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
		}
	}
	// Backup ID: canonical hex32; entropy failure → fail closed.
	id, err := randomBackupID(s.random)
	if err != nil {
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrEntropy)
	}
	bdir := filepath.Join(s.backupDir, id)
	// O_EXCL Mkdir 0700 (never MkdirAll for the bdir).
	if err := os.Mkdir(bdir, 0o700); err != nil {
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}

	// On failure remove ONLY the exact owned files we created (snapshot, ledger,
	// manifest) then best-effort Remove the dir. Never RemoveAll: a fault may have
	// left an unknown entry that must survive (orphan blocks the next backup).
	cleanupOwned := func() {
		for _, fn := range []string{snapshotFilename, ledgerFilename, "manifest.json"} {
			_ = os.Remove(filepath.Join(bdir, fn))
		}
		_ = os.Remove(bdir)
	}

	// Copy snapshot (Lstat regular/no-symlink/0600/cap, O_EXCL 0600 + writeFull + Sync).
	snapBytes, snapSHA, err := copyFileSyncedSafe(s.snapshotPath, filepath.Join(bdir, snapshotFilename), MaxSnapshotBytes)
	if err != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	// Copy WAL/ledger (cap = 8 MiB LedgerMaxBytes).
	walBytes, walSHA, err := copyFileSyncedSafe(s.ledgerPath, filepath.Join(bdir, ledgerFilename), LedgerMaxBytes)
	if err != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}

	// Manifest: strict canonical JSON, all required fields.
	manifest := backupManifest{
		Version:        1,
		BackupID:       id,
		StoreID:        s.storeID,
		CreatedAt:      s.clock.Now().UTC().Format(time.RFC3339Nano),
		SnapshotBytes:  len(snapBytes),
		SnapshotSHA256: snapSHA,
		WalBytes:       len(walBytes),
		WalSHA256:      walSHA,
		LedgerSequence: s.ledger.lastSeq,
		LedgerHeadHash: s.ledger.headHash,
	}
	mb, err := json.Marshal(manifest)
	if err != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	if len(mb) > 1024 {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	manifestPath := filepath.Join(bdir, "manifest.json")
	mf, err := os.OpenFile(manifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	if _, err := writeFull(mf, append(mb, '\n')); err != nil {
		mf.Close()
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	if err := mf.Sync(); err != nil {
		mf.Close()
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	if err := mf.Close(); err != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	if err := sfileSync(bdir); err != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	if err := sfileSync(s.backupDir); err != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
	}
	// Manifest SHA-256 (hash includes the LF) for sanitized info + authority.
	manifestFullBytes := append(mb, '\n')
	manifestHash := sha256.Sum256(manifestFullBytes)
	baseline := s.captureBaselineLocked()
	info := MigrationBackupInfo{BackupID: id, ManifestSHA256: hex.EncodeToString(manifestHash[:])}
	auth := backupAuthority{dir: bdir, baseline: baseline, info: info}
	// Pre-registration hook: a fault/tamper injected here must be caught by the
	// strict verify below; the hook returning an error fails the backup.
	if maintenanceBackupBeforeVerify != nil {
		if herr := maintenanceBackupBeforeVerify(bdir); herr != nil {
			cleanupOwned()
			s.gate.releaseBackupReservation(session)
			return DeviceStoreBackup{}, closedStoreErr(storeErrIndeterminate)
		}
	}
	// Immediate strict revalidation of everything just written, before the
	// capability is registered. Any failure leaves no authority behind.
	if _, verr := s.parseAndVerifyBackup(auth); verr != nil {
		cleanupOwned()
		s.gate.releaseBackupReservation(session)
		return DeviceStoreBackup{}, verr
	}
	backup, ok := s.gate.registerBackup(session, auth)
	if !ok {
		cleanupOwned()
		return DeviceStoreBackup{}, errSecurityNotReady
	}
	return backup, nil
}

// RestoreMigrationBackup restores the complete store set from a live matching
// backup capability. This is the ONLY legitimate head rollback; authorization
// depends on live session + backup pointer identity, never on disk comparison.
func (s *fileDeviceStore) RestoreMigrationBackup(session MaintenanceSession, backup DeviceStoreBackup) error {
	if !s.gate.enterMaintenance(session) {
		return errSecurityNotReady
	}
	defer s.gate.exitMaintenance()
	lb, ok := s.gate.lookupBackup(session, backup)
	if !ok {
		return errSecurityNotReady
	}
	s.mu.Lock()
	restoreErr := s.restoreFromBackupLocked(lb.backupAuthority)
	s.mu.Unlock()
	if restoreErr != nil {
		return restoreErr
	}
	s.gate.bumpWriteGen(session)
	return nil
}

// restoreFromBackupLocked validates the backup via the shared parseAndVerifyBackup
// path, then stages the validated snapshot+WAL bytes via the staged authority
// write (WAL first, then snapshot). The live files are NEVER opened O_TRUNC;
// each is replaced by a unique O_EXCL temp + Sync + Close + rename + exact
// read-back. A fault at any phase leaves the session + backup capability
// retryable; a same-session retry converges to the exact baseline. Caller holds
// storeMu.
func (s *fileDeviceStore) restoreFromBackupLocked(auth backupAuthority) error {
	m, err := s.parseAndVerifyBackup(auth)
	if err != nil {
		return err
	}
	snap, _, err := readAndVerifyFile(filepath.Join(auth.dir, snapshotFilename), m.SnapshotSHA256, m.SnapshotBytes)
	if err != nil {
		return closedStoreErr(storeErrIndeterminate)
	}
	wal, _, err := readAndVerifyFile(filepath.Join(auth.dir, ledgerFilename), m.WalSHA256, m.WalBytes)
	if err != nil {
		return closedStoreErr(storeErrIndeterminate)
	}
	// Shared candidate prevalidation before the first live write.
	if err := prevalidateMaintenanceCandidate(snap, wal, m.StoreID); err != nil {
		return err
	}
	// Stage authority: WAL first, then snapshot (no live O_TRUNC).
	if err := stageAuthorityFile(s.ledgerPath, wal, "wal"); err != nil {
		return err
	}
	if err := stageAuthorityFile(s.snapshotPath, snap, "snapshot"); err != nil {
		return err
	}
	if err := sfileSync(s.dir); err != nil {
		return closedStoreErr(storeErrIndeterminate)
	}
	return s.loadOrInitializeLocked()
}

// ValidateMaintenanceStore validates snapshot + WAL + temp + StoreID + baseline
// tombstone preservation. Updates the session write generation on success.
func (s *fileDeviceStore) ValidateMaintenanceStore(session MaintenanceSession) error {
	if !s.gate.enterMaintenance(session) {
		return errSecurityNotReady
	}
	defer s.gate.exitMaintenance()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateCurrentSetLocked(); err != nil {
		return err
	}
	if s.storeID != s.maintenanceBaseline.storeID {
		return closedStoreErr(storeErrSchema)
	}
	// Baseline committed tombstones must be preserved (superset).
	for id, bt := range s.maintenanceBaseline.tombstones {
		cur, ok := s.ledger.tombstones[id]
		if !ok || cur.sequence != bt.sequence || !cur.revokedAt.Equal(bt.revokedAt) {
			return closedStoreErr(storeErrSchema)
		}
	}
	return nil
}

// EndMaintenance terminates the session: success publishes ready; validation
// failure latches. Both terminal: all handles die.
func (s *fileDeviceStore) EndMaintenance(session MaintenanceSession) error {
	if !s.gate.verifySession(session) {
		return errSecurityNotReady
	}
	s.mu.Lock()
	verr := s.validateCurrentSetLocked()
	if verr == nil && s.storeID != s.maintenanceBaseline.storeID {
		verr = closedStoreErr(storeErrSchema)
	}
	if verr == nil {
		for id, bt := range s.maintenanceBaseline.tombstones {
			cur, ok := s.ledger.tombstones[id]
			if !ok || cur.sequence != bt.sequence || !cur.revokedAt.Equal(bt.revokedAt) {
				verr = closedStoreErr(storeErrSchema)
				break
			}
		}
	}
	if verr != nil {
		s.latchLocked()
	}
	s.mu.Unlock()
	s.gate.end(session)
	return verr
}

// AbortMaintenance terminates the session without guessing a backup. If the
// current store is invalid or the session was poisoned/unknown, ready latches
// false. Abort itself is always terminal and returns nil (it did its job);
// the ready latch is the side effect. Gate verification happens OUTSIDE storeMu.
func (s *fileDeviceStore) AbortMaintenance(session MaintenanceSession) error {
	healthy := s.gate.verifySession(session)
	s.mu.Lock()
	invalid := false
	if healthy {
		if err := s.validateCurrentSetLocked(); err != nil {
			invalid = true
		}
	} else {
		// Poisoned or unknown session: latch per design (no repair here).
		invalid = true
	}
	if invalid {
		s.latchLocked()
	}
	s.mu.Unlock()
	s.gate.abort(session)
	return nil
}

// MigrationBackupInfo is the sanitized projection of a completed backup for
// desktop consumption (no paths).
type MigrationBackupInfo struct {
	BackupID       string `json:"backupId"`
	ManifestSHA256 string `json:"manifestSha256"`
}

// MigrationBackupInfo returns the sanitized backup info (BackupID + ManifestSHA256)
// for a live or post-End backup handle. The backup is strictly revalidated via
// the shared parseAndVerifyBackup path before info is returned, so a tampered
// manifest is rejected before and after End. Stale/zero/cross-store handles fail.
func (s *fileDeviceStore) MigrationBackupInfo(backup DeviceStoreBackup) (MigrationBackupInfo, error) {
	s.gate.mu.Lock()
	var auth backupAuthority
	found := false
	if s.gate.live != nil && backup.token != nil {
		if lb, ok := s.gate.live.backups[backup.token]; ok {
			auth = lb.backupAuthority
			found = true
		}
	}
	if !found && backup.token != nil {
		if rec, ok := s.gate.cleanupRecords[backup.token]; ok {
			auth = rec.authority
			found = true
		}
	}
	s.gate.mu.Unlock()
	if !found {
		return MigrationBackupInfo{}, errSecurityNotReady
	}
	if _, err := s.parseAndVerifyBackup(auth); err != nil {
		return MigrationBackupInfo{}, err
	}
	return auth.info, nil
}

// CleanupMigrationBackup deletes exactly the validated backup for the given
// token. It strictly revalidates the full authority (canonical manifest + exact
// fields + file Lstat/mode/hashes + clean WAL replay + live baseline/info
// equality) BEFORE deleting anything. A validation failure retains the cleanup
// record so the caller can retry after manual repair. On success it removes the
// exact owned files + dir + sync root, then deletes the process-local cleanup
// record; a second cleanup is a stale error, not false success. Stale/zero/
// cross-store handles fail closed. A struct copy shares the same token
// capability (the gate token is an unexported pointer) and is accepted.
func (s *fileDeviceStore) CleanupMigrationBackup(backup DeviceStoreBackup) error {
	if backup.token == nil {
		return errSecurityNotReady
	}
	s.gate.mu.Lock()
	rec, ok := s.gate.cleanupRecords[backup.token]
	s.gate.mu.Unlock()
	if !ok {
		return errSecurityNotReady // stale/cross-store denied
	}
	// Strict revalidation before any deletion; failure retains the record.
	if _, err := s.parseAndVerifyBackup(rec.authority); err != nil {
		return err
	}
	bdir := rec.authority.dir
	// Delete exact owned files + dir + sync root.
	for _, fn := range []string{snapshotFilename, ledgerFilename, "manifest.json"} {
		if err := os.Remove(filepath.Join(bdir, fn)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return closedStoreErr(storeErrIndeterminate)
		}
	}
	if err := os.Remove(bdir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return closedStoreErr(storeErrIndeterminate)
	}
	if err := sfileSync(s.backupDir); err != nil {
		return closedStoreErr(storeErrIndeterminate)
	}
	// Success: delete the process-local cleanup record. A second cleanup is stale.
	s.gate.mu.Lock()
	delete(s.gate.cleanupRecords, backup.token)
	s.gate.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// deviceStoreMaintenanceWriter: callback-scoped migration writer (design §7.6.2)
// ---------------------------------------------------------------------------

// deviceStoreMaintenanceWriter allows a same-session migration to stage a
// candidate complete store-set and validate it. It never exposes a raw live
// handle and cannot escape the callback.
type deviceStoreMaintenanceWriter struct {
	store   *fileDeviceStore
	session MaintenanceSession
}

// StageCandidate replaces the live snapshot+WAL with the provided candidate
// bytes (staged + synced + read-back). It is only callable inside a live
// maintenance session. Gate in/out happen outside storeMu (no lock nesting).
func (w deviceStoreMaintenanceWriter) StageCandidate(snapshotBytes, ledgerBytes []byte) error {
	if !w.store.gate.enterMaintenance(w.session) {
		return errSecurityNotReady
	}
	defer w.store.gate.exitMaintenance()
	w.store.mu.Lock()
	err := w.stageLocked(snapshotBytes, ledgerBytes)
	w.store.mu.Unlock()
	if err != nil {
		return err
	}
	w.store.gate.bumpWriteGen(w.session)
	return nil
}

// stageLocked prevalidates the candidate (shared caps + structural checks) then
// stages it via the same authority write path as restore: WAL first, then
// snapshot, each via unique O_EXCL temp + Sync + Close + rename + exact
// read-back. No live O_TRUNC. Oversize/invalid candidates are rejected before
// the first write. Caller holds storeMu.
func (w deviceStoreMaintenanceWriter) stageLocked(snapshotBytes, ledgerBytes []byte) error {
	if err := prevalidateMaintenanceCandidate(snapshotBytes, ledgerBytes, w.store.storeID); err != nil {
		return err
	}
	if err := stageAuthorityFile(w.store.ledgerPath, ledgerBytes, "wal"); err != nil {
		return err
	}
	if err := stageAuthorityFile(w.store.snapshotPath, snapshotBytes, "snapshot"); err != nil {
		return err
	}
	if err := sfileSync(w.store.dir); err != nil {
		return closedStoreErr(storeErrIndeterminate)
	}
	return w.store.loadOrInitializeLocked()
}

// WithMigrationWriter issues a callback-scoped writer for the live session.
func (s *fileDeviceStore) WithMigrationWriter(session MaintenanceSession, fn func(deviceStoreMaintenanceWriter) error) error {
	if !s.gate.verifySession(session) {
		return errSecurityNotReady
	}
	return fn(deviceStoreMaintenanceWriter{store: s, session: session})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// Test/diagnostic seams (package vars; production defaults are real I/O / no-op).
// They let the B3a authority tests deterministically inject ReadDir faults,
// pre-verify tamper, and (R2) staged-write faults without test-only production
// branches. maintenanceStageFault is consumed by the R2 staged authority write.
var (
	maintenanceReadDir            = os.ReadDir
	maintenanceBackupBeforeVerify func(dir string) error
	maintenanceStageFault         func(phase string) error
)

// validateMaintenanceCandidateSizes enforces the snapshot (1 MiB) and WAL
// (8 MiB) boundaries for a staged candidate BEFORE any live write. Exact
// boundaries are accepted; +1 is rejected.
func validateMaintenanceCandidateSizes(snapshotBytes, ledgerBytes int) error {
	if snapshotBytes > MaxSnapshotBytes {
		return SecurityCapacityError{Code: CapacitySnapshotBytes}
	}
	if ledgerBytes > LedgerMaxBytes {
		return SecurityCapacityError{Code: CapacityRevocationReserve}
	}
	return nil
}

// prevalidateMaintenanceCandidate is the shared structural prevalidation used by
// both restore and StageCandidate BEFORE the first live write: snapshot ≤ 1 MiB
// with a strict-matching expected StoreID, and WAL ≤ 8 MiB that replays cleanly
// (tailClean) with a matching store + valid head. It performs NO live I/O.
func prevalidateMaintenanceCandidate(snapshotBytes, ledgerBytes []byte, expectedStoreID string) error {
	if err := validateMaintenanceCandidateSizes(len(snapshotBytes), len(ledgerBytes)); err != nil {
		return err
	}
	if _, err := parseSnapshotBytes(snapshotBytes, expectedStoreID); err != nil {
		return closedStoreErr(storeErrIndeterminate)
	}
	led, tail, _, err := parseAndValidateLedger(ledgerBytes, expectedStoreID)
	if err != nil || tail != tailClean || led.headHash == "" {
		return closedStoreErr(storeErrIndeterminate)
	}
	return nil
}

// stageFault is the nil-safe invocation of the maintenanceStageFault seam. A nil
// seam means no fault (production default).
func stageFault(phase string) error {
	if maintenanceStageFault != nil {
		return maintenanceStageFault(phase)
	}
	return nil
}

// stageAuthorityFile writes `bytes` to `livePath` via the staged authority path:
// a unique O_EXCL 0600 temp, writeFull, Sync, Close, rename into the live path,
// then an exact read-back reconcile. The live file is NEVER opened O_TRUNC; the
// only live mutation is the rename. The maintenanceStageFault seam is invoked at
// each named phase (phasePrefix + "-temp|-write|-sync|-rename|-readback"); a fault
// aborts with only the exact owned temp removed (ambiguous removal is retained /
// fail-closed — the operation already failed). Windows ambiguous rename errors
// are decided SOLELY by the post-rename byte read-back; no physical-atomicity is
// claimed for the rename itself.
func stageAuthorityFile(livePath string, bytes []byte, phasePrefix string) error {
	dir := filepath.Dir(livePath)
	fail := func() error { return closedStoreErr(storeErrIndeterminate) }

	if err := stageFault(phasePrefix + "-temp"); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+phasePrefix+"-stage-*")
	if err != nil {
		return fail()
	}
	tempName := temp.Name()
	// Exact-owned temp cleanup on any failure path; never a directory scan. If the
	// removal itself is ambiguous the temp is retained (fail-closed) and the
	// original error is returned — the caller does not pretend success.
	cleanupOwned := func() { _ = os.Remove(tempName) }

	if err := stageFault(phasePrefix + "-write"); err != nil {
		temp.Close()
		cleanupOwned()
		return err
	}
	if _, err := writeFull(temp, bytes); err != nil {
		temp.Close()
		cleanupOwned()
		return fail()
	}

	if err := stageFault(phasePrefix + "-sync"); err != nil {
		temp.Close()
		cleanupOwned()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		cleanupOwned()
		return fail()
	}
	if err := temp.Close(); err != nil {
		cleanupOwned()
		return fail()
	}

	if err := stageFault(phasePrefix + "-rename"); err != nil {
		cleanupOwned()
		return err
	}
	if err := os.Rename(tempName, livePath); err != nil {
		cleanupOwned()
		return fail()
	}

	// The rename consumed the temp; the live path now holds whatever rename left.
	// Decide success SOLELY by exact byte read-back (Windows rename is best-effort).
	if err := stageFault(phasePrefix + "-readback"); err != nil {
		return err
	}
	got, err := os.ReadFile(livePath)
	if err != nil || !bytesEqual(got, bytes) {
		return fail()
	}
	return nil
}

// parseAndVerifyBackup is the single strict verification path for a completed
// backup. It enforces: canonical manifest JSON+LF ≤1 KiB, strictJSONObject
// (duplicate/unknown/missing rejection), exact 10 fields, version/ID/store/
// time/sizes/hashes/head/sequence, dir entry Lstat modes (0600 regular, no
// symlink, no extra), file size+SHA-256, strict snapshot storeID, clean WAL
// replay (head+sequence), and equality to the live baseline + recorded manifest
// SHA. Any failure returns a closed-store error; the caller leaves no authority.
func (s *fileDeviceStore) parseAndVerifyBackup(a backupAuthority) (backupManifest, error) {
	reject := func() (backupManifest, error) { return backupManifest{}, closedStoreErr(storeErrIndeterminate) }
	dir := a.dir
	// 1. Manifest: read, canonical, strict.
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return reject()
	}
	if len(raw) == 0 || len(raw) > 1024 || raw[len(raw)-1] != '\n' {
		return reject()
	}
	body := raw[:len(raw)-1]
	fields, err := strictJSONObject(body)
	if err != nil {
		return reject()
	}
	if err := rejectUnknownStore(fields,
		"version", "backupId", "storeId", "createdAt",
		"snapshotBytes", "snapshotSha256", "walBytes", "walSha256",
		"ledgerSequence", "ledgerHeadHash"); err != nil {
		return reject()
	}
	version, err := reqInt(fields, "version")
	if err != nil || version != 1 {
		return reject()
	}
	backupID, err := reqStr(fields, "backupId")
	if err != nil || !validLowerHex(backupID, 16) {
		return reject()
	}
	// The backup ID must match its owning directory name.
	if backupID != filepath.Base(dir) {
		return reject()
	}
	storeID, err := reqStr(fields, "storeId")
	if err != nil || !validRawURLID(storeID) {
		return reject()
	}
	createdAt, err := reqStr(fields, "createdAt")
	if err != nil || !validUTCNano(createdAt) {
		return reject()
	}
	snapshotBytes, err := reqInt(fields, "snapshotBytes")
	if err != nil || snapshotBytes < 0 || snapshotBytes > MaxSnapshotBytes {
		return reject()
	}
	snapshotSHA, err := reqStr(fields, "snapshotSha256")
	if err != nil || !validLowerHex(snapshotSHA, 32) {
		return reject()
	}
	walBytes, err := reqInt(fields, "walBytes")
	if err != nil || walBytes < 0 || walBytes > LedgerMaxBytes {
		return reject()
	}
	walSHA, err := reqStr(fields, "walSha256")
	if err != nil || !validLowerHex(walSHA, 32) {
		return reject()
	}
	ledgerSequence, err := reqUint(fields, "ledgerSequence")
	if err != nil {
		return reject()
	}
	ledgerHeadHash, err := reqStr(fields, "ledgerHeadHash")
	if err != nil || !validPaddedHash(ledgerHeadHash) {
		return reject()
	}
	m := backupManifest{
		Version: version, BackupID: backupID, StoreID: storeID, CreatedAt: createdAt,
		SnapshotBytes: snapshotBytes, SnapshotSHA256: snapshotSHA,
		WalBytes: walBytes, WalSHA256: walSHA,
		LedgerSequence: ledgerSequence, LedgerHeadHash: ledgerHeadHash,
	}
	// Canonical check: re-marshaling the parsed manifest must equal the body byte
	// for byte (catches non-canonical whitespace/ordering the strict object parse
	// tolerates via TrimSpace).
	canon, mErr := json.Marshal(m)
	if mErr != nil || !bytes.Equal(canon, body) {
		return reject()
	}
	// 2. Directory: exactly 3 owned entries, all 0600 regular, no symlink/extra.
	entries, err := maintenanceReadDir(dir)
	if err != nil {
		return reject()
	}
	if len(entries) != 3 {
		return reject()
	}
	wantNames := map[string]bool{snapshotFilename: false, ledgerFilename: false, "manifest.json": false}
	for _, e := range entries {
		li, lerr := os.Lstat(filepath.Join(dir, e.Name()))
		if lerr != nil {
			return reject()
		}
		if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() || li.Mode().Perm() != 0o600 {
			return reject()
		}
		seen, ok := wantNames[e.Name()]
		if !ok || seen {
			return reject()
		}
		wantNames[e.Name()] = true
	}
	for _, ok := range wantNames {
		if !ok {
			return reject()
		}
	}
	// 3-4. Snapshot + WAL files: size + SHA-256 (readAndVerifyFile also re-reads;
	// correctness over a redundant read for bounded files).
	snapBytes, _, err := readAndVerifyFile(filepath.Join(dir, snapshotFilename), snapshotSHA, snapshotBytes)
	if err != nil {
		return reject()
	}
	walFileBytes, _, err := readAndVerifyFile(filepath.Join(dir, ledgerFilename), walSHA, walBytes)
	if err != nil {
		return reject()
	}
	// 5. Strict snapshot parse: storeID must match manifest.
	if _, err := parseSnapshotBytes(snapBytes, storeID); err != nil {
		return reject()
	}
	// 6. Clean WAL replay: head + sequence must match the manifest exactly.
	led, tail, _, err := parseAndValidateLedger(walFileBytes, storeID)
	if err != nil || tail != tailClean || led.headHash != ledgerHeadHash || led.lastSeq != ledgerSequence {
		return reject()
	}
	// 7. Equality to the live baseline captured at backup time.
	if a.baseline.storeID != storeID ||
		a.baseline.ledgerHeadHash != ledgerHeadHash ||
		a.baseline.ledgerLastSeq != ledgerSequence {
		return reject()
	}
	sd := sha256.Sum256(snapBytes)
	if a.baseline.snapshotHash != hex.EncodeToString(sd[:]) {
		return reject()
	}
	// 8. Manifest SHA equality: the recorded info hash must match the on-disk bytes.
	mh := sha256.Sum256(raw)
	if a.info.ManifestSHA256 != hex.EncodeToString(mh[:]) {
		return reject()
	}
	return m, nil
}

// validLowerHex reports whether s is exactly byteLen*2 lowercase hex characters
// (canonical lowercase hex of byteLen bytes).
func validLowerHex(s string, byteLen int) bool {
	if len(s) != byteLen*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

type backupManifest struct {
	Version        int    `json:"version"`
	BackupID       string `json:"backupId"`
	StoreID        string `json:"storeId"`
	CreatedAt      string `json:"createdAt"`
	SnapshotBytes  int    `json:"snapshotBytes"`
	SnapshotSHA256 string `json:"snapshotSha256"`
	WalBytes       int    `json:"walBytes"`
	WalSHA256      string `json:"walSha256"`
	LedgerSequence uint64 `json:"ledgerSequence"`
	LedgerHeadHash string `json:"ledgerHeadHash"`
}

// readAndVerifyFile reads a file, verifies its size and SHA-256 hash against
// expected values, and returns the bytes + actual hash. Fails on mismatch.
func readAndVerifyFile(path, expectedSHA string, expectedBytes int) ([]byte, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(raw) != expectedBytes {
		return nil, "", errors.New("size mismatch")
	}
	d := sha256.Sum256(raw)
	actualSHA := hex.EncodeToString(d[:])
	if actualSHA != expectedSHA {
		return nil, "", errors.New("hash mismatch")
	}
	return raw, actualSHA, nil
}

// randomBackupID generates a canonical lowercase-hex32 backup ID from 16
// random bytes. Entropy failure returns an error (caller fails closed); the
// temp buffer is always zeroed.
func randomBackupID(r interface{ Read([]byte) (int, error) }) (string, error) {
	buf := make([]byte, 16)
	defer zeroBytes(buf)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// copyFileSyncedSafe copies src→dst with strict safety: Lstat src (regular,
// no symlink, mode 0600), cap check, O_EXCL create dst 0600, writeFull, Sync,
// Close. Returns the copied bytes and their SHA-256. Any failure cleans dst.
func copyFileSyncedSafe(src, dst string, maxBytes int) ([]byte, string, error) {
	li, err := os.Lstat(src)
	if err != nil {
		return nil, "", err
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return nil, "", errors.New("backup: source not regular")
	}
	if li.Mode().Perm() != 0o600 {
		return nil, "", fmt.Errorf("backup: source mode %o not 0600", li.Mode().Perm())
	}
	if li.Size() > int64(maxBytes) {
		return nil, "", errors.New("backup: source exceeds cap")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}
	if _, err := writeFull(f, raw); err != nil {
		f.Close()
		os.Remove(dst)
		return nil, "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(dst)
		return nil, "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(dst)
		return nil, "", err
	}
	d := sha256.Sum256(raw)
	return raw, hex.EncodeToString(d[:]), nil
}

func sfileSync(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		// path may be a directory; use os.Open for dir sync.
		f2, e2 := os.Open(path)
		if e2 != nil {
			return err
		}
		defer f2.Close()
		return f2.Sync()
	}
	defer f.Close()
	return f.Sync()
}

// validateBackupRootLocked ensures the backup root is a real 0700 dir (no
// symlink/irregular). Creates it with O_EXCL semantics if absent.
func validateBackupRootLocked(backupDir string) error {
	li, err := os.Lstat(backupDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return os.Mkdir(backupDir, 0o700)
		}
		return err
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.IsDir() {
		return errors.New("backup root not a real directory")
	}
	if li.Mode().Perm() != 0o700 {
		return os.Chmod(backupDir, 0o700)
	}
	return nil
}

// errors
var (
	errMaintenanceSession = errors.New("maintenance session rejected")
)

func init() {
	// alias errSecurityNotReady for maintenance refusal to keep a single closed text.
	_ = errMaintenanceSession
}
