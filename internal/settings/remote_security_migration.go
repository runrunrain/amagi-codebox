// Package settings — remote security migration raw-file store (design §C,
// leader-ratification C-1/C-2).
//
// This file implements a self-contained, raw-byte settings migration with a
// marker state machine. It operates DIRECTLY on settings.json bytes and never
// imports internal/remote (no package cycle). The old remoteToken is removed
// from the active candidate and NEVER copied to Device Store, Cookie, event,
// error, or log (C-1: RemoteToken is not persisted settings state).
//
// State machine summary (all file identity is same-process pointer-local):
//
//	Detect → {MissingNewInstall, ManualRepair, NeedsMigration, Current, FutureOrInvalid}
//	Begin  → acquires one same-process capability per configDir
//	Stage  → txn dir + backup + prepared marker + validated candidate
//	Commit → rename candidate→settings.json, classify by byte readback
//	         exact-new → marker=settings_committed
//	         exact-old → NotCommitted (caller Rollback)
//	         other/missing → Indeterminate (latch Abort, keep marker+backup)
//	Rollback → RestoreExactOld (restore settings.json, KEEP marker/backup) +
//	          DiscardTransaction (remove artifacts, kill capability).
//	          The gate calls them split around Device Store End so the marker
//	          survives an End failure (R2-Major-02). Rollback() composes both
//	          for B3c callers.
//	Finish   → post settings_committed: marker→backup→txn dir→parent sync→kill
//	Abort    → keep everything, kill capability
//
// Crash/restart: any marker or orphan txn dir → next Detect ManualRepair;
// capabilities NEVER survive a process boundary. No physical-atomicity is
// claimed: classification is by byte readback only.
package settings

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// --- Constants ---

const (
	// migrationTxnDirName is the transaction directory holding all migration
	// artifacts (backup, marker, candidate). Its existence at Detect time means
	// an incomplete migration → ManualRepair.
	migrationTxnDirName = ".remote-security-migration"
	// markerFileName holds the canonical migration marker.
	markerFileName = "marker"
	// backupFileName holds the exact original settings.json bytes.
	backupFileName = "settings.backup"
	// candidateFileName holds the version-1, token-stripped candidate settings.
	candidateFileName = "settings.candidate"

	// settingsFileName is the live settings file written by Service.Save and
	// consumed by migration.
	settingsFileName = "settings.json"

	// markerStatePrepared is written during Stage.
	markerStatePrepared = "prepared"
	// markerStateSettingsCommitted is written after Commit exact-new.
	markerStateSettingsCommitted = "settings_committed"

	// migrationMarkerVersion is the version recorded inside the marker payload.
	migrationMarkerVersion = 1

	// candidateMaxBytes bounds the candidate settings document (defense).
	candidateMaxBytes = 1 << 20 // 1 MiB
)

// --- Detection types ---

// DetectionState classifies the raw settings file for migration decisions.
type DetectionState string

const (
	// DetectionMissingNewInstall: settings.json absent → implicit v1 defaults,
	// zero writes. First ordinary Save persists version 1.
	DetectionMissingNewInstall DetectionState = "missing_new_install"
	// DetectionManualRepair: marker present, orphan txn dir, malformed JSON, or
	// wrong-typed tuple fields. Never auto-restored; requires human intervention.
	DetectionManualRepair DetectionState = "manual_repair"
	// DetectionNeedsMigration: version missing or 0. Migration candidate should
	// be staged. Prior tuple is populated for orchestration.
	DetectionNeedsMigration DetectionState = "needs_migration"
	// DetectionCurrent: version == 1. No migration needed.
	DetectionCurrent DetectionState = "current"
	// DetectionFutureOrInvalid: version > 1 or non-integer/non-number. No
	// mutation ever; remote startup stops with a warning, desktop stays usable.
	DetectionFutureOrInvalid DetectionState = "future_or_invalid"
)

// PriorRemoteConfig records the pre-migration remote listener tuple. Missing
// fields mean defaults (loopback/8680/disabled) — migration never expands them.
type PriorRemoteConfig struct {
	Enabled bool
	Host    string
	Port    int
}

// Detection is the stateless result of Detect.
type Detection struct {
	State DetectionState
	Prior PriorRemoteConfig
}

// --- Commit types ---

// CommitOutcome classifies the post-rename byte readback of settings.json.
type CommitOutcome string

const (
	// CommitCommitted: readback == candidate bytes. Migration took effect.
	CommitCommitted CommitOutcome = "committed"
	// CommitNotCommitted: readback == original bytes. Rename did not replace
	// settings.json; caller must Rollback.
	CommitNotCommitted CommitOutcome = "not_committed"
	// CommitIndeterminate: readback is neither exact-old nor exact-new, or the
	// file is missing. Capability is latched (Abort); marker+backup retained.
	// Next Detect → ManualRepair.
	CommitIndeterminate CommitOutcome = "indeterminate"
)

// CommitResult is returned by Commit.
type CommitResult struct {
	Outcome CommitOutcome
}

// --- Error sentinels ---

var (
	// ErrCapabilityActive: a second Begin on a configDir that already has a
	// live same-process capability.
	ErrCapabilityActive = errors.New("settings migration: capability already active for config dir")
	// ErrMigrationClosed: operation on a killed/closed migration capability.
	ErrMigrationClosed = errors.New("settings migration: capability closed")
	// ErrNotStaged: Commit called before Stage.
	ErrNotStaged = errors.New("settings migration: not staged")
	// ErrNotCommitted: Finish called before a successful Commit (exact-new).
	ErrNotCommitted = errors.New("settings migration: not committed")
	// ErrSymlinkConfigDir: configDir (or a parent) is a symlink — rejected.
	ErrSymlinkConfigDir = errors.New("settings migration: config dir is a symlink")
)

// --- Capability registry (process-local) ---

var (
	capMu    sync.Mutex
	liveCaps = make(map[string]bool) // configDir → has live capability
)

// --- RemoteSecurityMigrationStore ---

// RemoteSecurityMigrationStore owns raw settings Detect/Begin. It does NOT
// import internal/remote and does NOT touch the Device Store. One store per
// configDir; Detect is stateless and safe to call repeatedly.
type RemoteSecurityMigrationStore struct {
	configDir string
}

// NewRemoteSecurityMigrationStore creates a store rooted at configDir.
func NewRemoteSecurityMigrationStore(configDir string) *RemoteSecurityMigrationStore {
	return &RemoteSecurityMigrationStore{configDir: configDir}
}

// Detect inspects the raw settings file and any migration artifacts. It never
// mutates state and never returns an error for a recognized classification —
// only for unexpected filesystem errors (returned to the caller; the caller may
// treat those as ManualRepair). A present marker or orphan txn dir is always
// ManualRepair.
func (s *RemoteSecurityMigrationStore) Detect() (Detection, error) {
	// 1. Migration artifacts: txn dir existence → ManualRepair (covers marker
	//    present AND orphan txn dir without marker).
	txnDir := filepath.Join(s.configDir, migrationTxnDirName)
	if li, err := os.Lstat(txnDir); err == nil {
		// Anything at the txn-dir path (dir, file, symlink) → ManualRepair.
		_ = li
		return Detection{State: DetectionManualRepair}, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Detection{}, fmt.Errorf("detect: stat txn dir: %w", err)
	}

	// 2. Settings file.
	settingsPath := filepath.Join(s.configDir, settingsFileName)
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Detection{State: DetectionMissingNewInstall}, nil
		}
		return Detection{}, fmt.Errorf("detect: read settings: %w", err)
	}

	// 3. Strict parse with json.Number semantics.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return Detection{State: DetectionManualRepair}, nil // malformed JSON
	}
	if dec.More() {
		return Detection{State: DetectionManualRepair}, nil // trailing data
	}
	if m == nil {
		// "null" literal — treat as malformed (settings must be an object).
		return Detection{State: DetectionManualRepair}, nil
	}

	// 4. Classify version.
	version, vKind := classifyVersionMap(m)
	switch vKind {
	case versionKindNonNumber, versionKindNonInteger:
		return Detection{State: DetectionFutureOrInvalid}, nil
	case versionKindInteger:
		if version > 1 || version < 0 {
			return Detection{State: DetectionFutureOrInvalid}, nil
		}
		if version == 1 {
			return Detection{State: DetectionCurrent}, nil
		}
		// version == 0 → NeedsMigration, fall through.
	}

	// 5. version == 0 (missing or explicit 0) → NeedsMigration; extract prior.
	prior, perr := extractPriorTuple(m)
	if perr != nil {
		return Detection{State: DetectionManualRepair}, nil // wrong-typed tuple
	}
	return Detection{State: DetectionNeedsMigration, Prior: prior}, nil
}

// Begin acquires the one same-process pointer-identity capability for this
// configDir. A second Begin (on any store for the same configDir) returns
// ErrCapabilityActive. The configDir is validated (no symlink, mkdir 0700,
// chmod 0700 like Save). The returned *Migration is the capability handle.
func (s *RemoteSecurityMigrationStore) Begin() (*Migration, error) {
	capMu.Lock()
	if liveCaps[s.configDir] {
		capMu.Unlock()
		return nil, ErrCapabilityActive
	}
	capMu.Unlock()

	// Refuse if migration artifacts exist (ManualRepair state). Defense-in-
	// depth: orchestration must check Detect first, but Begin itself never
	// proceeds over an incomplete migration.
	txnDir := filepath.Join(s.configDir, migrationTxnDirName)
	if _, err := os.Lstat(txnDir); err == nil {
		return nil, fmt.Errorf("settings migration: begin refused: %s", DetectionManualRepair)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("begin: stat txn dir: %w", err)
	}

	// Validate configDir before committing the capability.
	if err := ensureConfigDir(s.configDir); err != nil {
		return nil, err
	}

	capMu.Lock()
	// Re-check after releasing the lock (another goroutine could have begun).
	if liveCaps[s.configDir] {
		capMu.Unlock()
		return nil, ErrCapabilityActive
	}
	liveCaps[s.configDir] = true
	capMu.Unlock()

	return &Migration{
		store:        s,
		configDir:    s.configDir,
		settingsPath: filepath.Join(s.configDir, settingsFileName),
		txnDir:       filepath.Join(s.configDir, migrationTxnDirName),
		seams:        defaultSeams(),
	}, nil
}

// --- Migration capability ---

// Migration is the pointer-identity capability returned by Begin. Stage/
// Commit/Rollback/Finish/Abort are the lifecycle transitions. Any of
// Rollback/Finish/Abort kills the capability; subsequent calls return
// ErrMigrationClosed. Capabilities are process-local: a crash invalidates the
// handle and the next process sees the on-disk marker → ManualRepair.
type Migration struct {
	store        *RemoteSecurityMigrationStore
	configDir    string
	settingsPath string
	txnDir       string

	mu        sync.Mutex
	closed    bool
	staged    bool
	committed bool // Commit reached exact-new

	backupBytes    []byte
	candidateBytes []byte
	candidateSHA   [32]byte

	seams migrationSeams
}

// migrationSeams holds fault-injection hooks. Production uses defaultSeams();
// tests override individual fields (same package).
type migrationSeams struct {
	backupWrite     func(txnDir string, data []byte) error
	markerWrite     func(txnDir string, data []byte) error
	candidateWrite  func(txnDir string, data []byte) error
	rename          func(old, new string) error
	markerRemove    func(path string) error
	backupRemove    func(path string) error // Finish: delete the token-bearing settings backup
	candidateRemove func(path string) error // Finish: delete the renamed-away candidate
	txnDirRemove    func(path string) error // Finish: remove the txn dir once empty
	parentSync      func(dir string) error  // Finish: durably sync the parent dir
}

func defaultSeams() migrationSeams {
	return migrationSeams{
		backupWrite:     defaultArtifactWriter(backupFileName),
		markerWrite:     defaultArtifactWriter(markerFileName),
		candidateWrite:  defaultArtifactWriter(candidateFileName),
		rename:          os.Rename,
		markerRemove:    os.Remove,
		backupRemove:    os.Remove,
		candidateRemove: os.Remove,
		txnDirRemove:    os.Remove,
		parentSync:      syncParentDir,
	}
}

// defaultArtifactWriter returns a seam that writes data to name inside txnDir
// via O_EXCL 0600 + writeFull + Sync + Close.
func defaultArtifactWriter(name string) func(txnDir string, data []byte) error {
	return func(txnDir string, data []byte) error {
		return writeSyncClose(filepath.Join(txnDir, name), data, 0o600)
	}
}

// Stage creates the txn dir, writes backup/marker/candidate, and verifies
// candidate invariants. On success the migration is ready for Commit.
func (m *Migration) Stage() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrMigrationClosed
	}
	if m.staged {
		return errors.New("settings migration: already staged")
	}

	// 1. Txn dir 0700 (reject symlink).
	if err := ensureTxnDir(m.txnDir); err != nil {
		return fmt.Errorf("stage: txn dir: %w", err)
	}

	// 2. Read original settings (must exist — Detect said NeedsMigration).
	original, err := os.ReadFile(m.settingsPath)
	if err != nil {
		return fmt.Errorf("stage: read original: %w", err)
	}
	if len(original) > candidateMaxBytes {
		return errors.New("stage: original settings exceed 1MiB")
	}

	// 3. Backup = exact original bytes.
	if err := m.seams.backupWrite(m.txnDir, original); err != nil {
		return fmt.Errorf("stage: write backup: %w", err)
	}
	// Verify backup by readback.
	bkRead, err := readAllFile(filepath.Join(m.txnDir, backupFileName))
	if err != nil {
		return fmt.Errorf("stage: readback backup: %w", err)
	}
	if !bytes.Equal(bkRead, original) {
		return errors.New("stage: backup readback mismatch")
	}

	// 4. Marker = prepared.
	markerBytes := canonicalMarker(markerStatePrepared)
	if err := m.seams.markerWrite(m.txnDir, markerBytes); err != nil {
		return fmt.Errorf("stage: write marker: %w", err)
	}
	mkRead, err := readAllFile(filepath.Join(m.txnDir, markerFileName))
	if err != nil {
		return fmt.Errorf("stage: readback marker: %w", err)
	}
	if !bytes.Equal(mkRead, markerBytes) {
		return errors.New("stage: marker readback mismatch")
	}

	// 5. Construct candidate.
	candidate, err := buildCandidate(original)
	if err != nil {
		return fmt.Errorf("stage: build candidate: %w", err)
	}

	// 6. Write candidate temp 0600.
	if err := m.seams.candidateWrite(m.txnDir, candidate); err != nil {
		return fmt.Errorf("stage: write candidate: %w", err)
	}
	candRead, err := readAllFile(filepath.Join(m.txnDir, candidateFileName))
	if err != nil {
		return fmt.Errorf("stage: readback candidate: %w", err)
	}
	if !bytes.Equal(candRead, candidate) {
		return errors.New("stage: candidate readback mismatch")
	}

	// 7. Reopen + re-parse invariants.
	if err := validateCandidateInvariants(original, candRead); err != nil {
		return fmt.Errorf("stage: candidate invariant: %w", err)
	}

	m.backupBytes = original
	m.candidateBytes = candidate
	m.candidateSHA = sha256.Sum256(candidate)
	m.staged = true
	return nil
}

// Commit renames the candidate to settings.json and classifies the outcome by
// byte readback. The live settings.json is NEVER opened O_TRUNC.
func (m *Migration) Commit() (CommitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return CommitResult{}, ErrMigrationClosed
	}
	if !m.staged {
		return CommitResult{}, ErrNotStaged
	}
	if m.committed {
		// Idempotent: already committed in this capability.
		return CommitResult{Outcome: CommitCommitted}, nil
	}

	candidatePath := filepath.Join(m.txnDir, candidateFileName)

	// Rename candidate → settings.json. Even on error we classify by readback.
	if err := m.seams.rename(candidatePath, m.settingsPath); err != nil {
		// Fall through to classification — rename may have partially succeeded.
		_ = err
	}

	// Byte readback classification.
	current, rerr := os.ReadFile(m.settingsPath)
	if rerr != nil {
		// Missing → Indeterminate.
		m.killCapabilityLocked()
		return CommitResult{Outcome: CommitIndeterminate}, nil
	}

	if bytes.Equal(current, m.candidateBytes) {
		// exact-new → committed. chmod 0600, parent sync, rewrite marker.
		if err := os.Chmod(m.settingsPath, 0o600); err != nil {
			return CommitResult{}, fmt.Errorf("commit: chmod settings: %w", err)
		}
		if err := syncParentDir(m.configDir); err != nil {
			return CommitResult{}, fmt.Errorf("commit: sync parent: %w", err)
		}
		committedMarker := canonicalMarker(markerStateSettingsCommitted)
		if err := rewriteMarker(m.txnDir, committedMarker); err != nil {
			return CommitResult{}, fmt.Errorf("commit: rewrite marker: %w", err)
		}
		m.committed = true
		return CommitResult{Outcome: CommitCommitted}, nil
	}

	if bytes.Equal(current, m.backupBytes) {
		// exact-old → rename did not replace settings.json.
		return CommitResult{Outcome: CommitNotCommitted}, nil
	}

	// other → Indeterminate. Latch Abort: keep marker+backup, kill capability.
	m.killCapabilityLocked()
	return CommitResult{Outcome: CommitIndeterminate}, nil
}

// Rollback restores settings.json from the backup and removes all txn
// artifacts. It is the composition of RestoreExactOld + DiscardTransaction
// and is retained for B3c existing callers. The migration gate now uses the
// SPLIT API (RestoreExactOld → Device Store End → DiscardTransaction) so the
// marker is preserved until End succeeds (R2-Major-02).
//
// Failure lifecycle (R3-Major-02): the pre-split Rollback had
// `defer killCapabilityLocked()` and thus killed the capability on EVERY return
// path. The split RestoreExactOld intentionally does NOT kill (the gate relies
// on the capability staying live across the End boundary), so the composition
// must restore that terminal contract itself: if RestoreExactOld fails, Rollback
// preserves the on-disk evidence (marker/backup/txn untouched by the failed
// restore) and kills the capability via Abort before returning the original
// error. DiscardTransaction already kills on all its own return paths. Callable
// pre-commit and post-exact-new (same process only).
func (m *Migration) Rollback() error {
	if err := m.RestoreExactOld(); err != nil {
		// RestoreExactOld does not kill; Rollback is terminal and MUST kill on
		// every return path (B3c contract). Disk evidence is preserved (the failed
		// restore touched nothing). Abort is idempotent (no-op if already closed).
		_ = m.Abort()
		return err
	}
	return m.DiscardTransaction()
}

// RestoreExactOld restores settings.json from the backup (temp+rename+readback)
// and syncs the parent dir, WITHOUT removing any transaction artifacts
// (marker/backup/candidate/txn dir) and WITHOUT killing the capability. This
// is phase 1 of the split rollback (R2-Major-02): the gate calls it BEFORE
// Device Store End so that an End failure leaves the settings marker + backup
// on disk (next Detect → ManualRepair). Call DiscardTransaction afterward to
// remove the artifacts and release the configDir. Pre-commit (Stage failure
// before backup) this is a no-op: settings.json was never modified.
func (m *Migration) RestoreExactOld() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrMigrationClosed
	}
	if len(m.backupBytes) == 0 {
		// Pre-commit / nothing staged to restore: settings.json was never
		// modified. Leave marker+backup untouched for the caller's decision.
		return nil
	}
	if err := restoreViaTempRename(m.settingsPath, m.backupBytes); err != nil {
		return fmt.Errorf("restore-exact-old: restore: %w", err)
	}
	if err := syncParentDir(m.configDir); err != nil {
		return fmt.Errorf("restore-exact-old: sync parent: %w", err)
	}
	return nil
}

// DiscardTransaction removes all transaction artifacts (marker/backup/
// candidate/txn dir) and syncs the parent dir, then kills the capability. This
// is phase 2 of the split rollback (R2-Major-02): the gate calls it ONLY after
// Device Store End succeeded, so the marker is never removed while the device
// store could still fail and force a ManualRepair classification. It kills the
// capability on every return path (defer killCapabilityLocked), matching the
// pre-split Rollback terminal contract (R3-Major-02). It reuses the m.seams
// parentSync hook (default syncParentDir) so tests can inject a sync failure.
func (m *Migration) DiscardTransaction() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrMigrationClosed
	}
	defer m.killCapabilityLocked()
	if err := removeTxnArtifacts(m.txnDir); err != nil {
		return fmt.Errorf("discard-transaction: cleanup: %w", err)
	}
	if err := m.seams.parentSync(m.configDir); err != nil {
		return fmt.Errorf("discard-transaction: sync parent: %w", err)
	}
	return nil
}

// Finish completes the migration after a settings_committed marker. It deletes
// the token-bearing settings backup FIRST (so a later-step failure can never
// leave the old remoteToken on disk), then the candidate, the marker, the txn
// dir, and finally syncs the parent. EVERY step is checked: success is returned
// only when the backup is gone, the txn dir is cleared and the Sync completed.
// Any failure returns an error and leaves the remaining txn artifacts in place
// (txn dir presence → next Detect ManualRepair = repairable evidence); the caller
// must forbid Start. The capability is always killed (defer) so the configDir is
// released for the next process.
func (m *Migration) Finish() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrMigrationClosed
	}
	if !m.committed {
		return ErrNotCommitted
	}
	defer m.killCapabilityLocked()

	// 1. Delete the sensitive settings backup (contains the old remoteToken)
	//    FIRST so no later-step failure can leave the token on disk.
	if err := m.seams.backupRemove(filepath.Join(m.txnDir, backupFileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("finish: delete backup: %w", err)
	}
	// 2. Candidate was renamed to settings.json by Commit; ErrNotExist is the
	//    expected/already-cleared state. Any other removal error is a hard fail.
	if err := m.seams.candidateRemove(filepath.Join(m.txnDir, candidateFileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("finish: delete candidate: %w", err)
	}
	// 3. Marker: together with the txn dir its presence gates Detect → ManualRepair.
	if err := m.seams.markerRemove(filepath.Join(m.txnDir, markerFileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("finish: delete marker: %w", err)
	}
	// 4. Remove the txn dir (only succeeds once empty — steps 1-3 must have run).
	if err := m.seams.txnDirRemove(m.txnDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("finish: remove txn dir: %w", err)
	}
	// 5. Durably flush all deletions to disk.
	if err := m.seams.parentSync(m.configDir); err != nil {
		return fmt.Errorf("finish: sync parent: %w", err)
	}
	return nil
}

// Abort keeps all on-disk artifacts and kills the capability. Intended for
// indeterminate situations where cleanup is unsafe.
func (m *Migration) Abort() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrMigrationClosed
	}
	m.killCapabilityLocked()
	return nil
}

// killCapabilityLocked releases the configDir from the live-capability registry
// and marks the handle closed. Caller must hold m.mu.
func (m *Migration) killCapabilityLocked() {
	m.closed = true
	m.staged = false
	capMu.Lock()
	delete(liveCaps, m.configDir)
	capMu.Unlock()
}

// --- Internal helpers: durable writes ---

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

// writeSyncClose creates/replaces path via O_CREATE|O_EXCL (never O_TRUNC the
// live settings), writes data, Syncs, and closes. The file is 0600.
func writeSyncClose(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := writeFull(f, data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// readAllFile reads and returns the full contents of path.
func readAllFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// rewriteMarker rewrites the marker file in-place via remove+O_EXCL write. It
// is used by Commit to transition prepared→settings_committed.
func rewriteMarker(txnDir string, data []byte) error {
	markerPath := filepath.Join(txnDir, markerFileName)
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writeSyncClose(markerPath, data, 0o600)
}

// restoreViaTempRename writes data to a temp file, renames it over settingsPath,
// and verifies exact byte readback.
func restoreViaTempRename(settingsPath string, data []byte) error {
	dir := filepath.Dir(settingsPath)
	tmp, err := os.CreateTemp(dir, ".settings-restore-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := writeFull(tmp, data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, settingsPath); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(settingsPath, 0o600); err != nil {
		return err
	}
	// Exact readback.
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return errors.New("restore readback mismatch")
	}
	return nil
}

// removeTxnArtifacts removes the known artifact files and the txn dir. Missing
// files are not errors.
func removeTxnArtifacts(txnDir string) error {
	for _, name := range []string{markerFileName, backupFileName, candidateFileName} {
		_ = os.Remove(filepath.Join(txnDir, name))
	}
	if err := os.Remove(txnDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// --- Internal helpers: directories ---

// ensureConfigDir validates/creates configDir at 0700, rejecting symlinks.
func ensureConfigDir(configDir string) error {
	li, err := os.Lstat(configDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		if li.Mode()&os.ModeSymlink != 0 {
			return ErrSymlinkConfigDir
		}
		if !li.IsDir() {
			return errors.New("settings migration: config dir is not a directory")
		}
	}
	if err := os.Chmod(configDir, 0o700); err != nil {
		return err
	}
	return nil
}

// ensureTxnDir creates the txn dir at 0700, rejecting if it is a symlink or
// non-directory.
func ensureTxnDir(txnDir string) error {
	li, err := os.Lstat(txnDir)
	if err == nil {
		if li.Mode()&os.ModeSymlink != 0 {
			return errors.New("txn dir is a symlink")
		}
		if !li.IsDir() {
			return errors.New("txn path is not a directory")
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(txnDir, 0o700); err != nil {
			return err
		}
	} else {
		return err
	}
	if err := os.Chmod(txnDir, 0o700); err != nil {
		return err
	}
	return nil
}

// --- Internal helpers: marker ---

// markerPayload is the canonical marker JSON structure.
type markerPayload struct {
	State   string `json:"state"`
	Version int    `json:"version"`
}

// canonicalMarker returns the canonical marker bytes (compact JSON + LF).
func canonicalMarker(state string) []byte {
	b, _ := json.Marshal(markerPayload{State: state, Version: migrationMarkerVersion})
	return append(b, '\n')
}

// --- Internal helpers: version classification ---

type versionKind int

const (
	versionKindInteger versionKind = iota
	versionKindNonInteger
	versionKindNonNumber
)

// classifyVersionMap inspects the remoteSecurityVersion key in a map decoded
// with UseNumber. Missing → integer 0. Non-number (string/bool/null/object) →
// NonNumber. Non-integer number (1.5, negative) → NonInteger.
func classifyVersionMap(m map[string]any) (int, versionKind) {
	v, exists := m["remoteSecurityVersion"]
	if !exists {
		return 0, versionKindInteger
	}
	num, ok := v.(json.Number)
	if !ok {
		return 0, versionKindNonNumber
	}
	i64, err := num.Int64()
	if err != nil {
		return 0, versionKindNonInteger
	}
	return int(i64), versionKindInteger
}

// extractPriorTuple reads the remote listener tuple from a map decoded with
// UseNumber. Missing fields default to loopback/8680/disabled. Wrong-typed
// fields return an error (→ ManualRepair).
func extractPriorTuple(m map[string]any) (PriorRemoteConfig, error) {
	var p PriorRemoteConfig
	p.Host = "127.0.0.1"
	p.Port = 8680

	if v, exists := m["remoteEnabled"]; exists {
		b, ok := v.(bool)
		if !ok {
			return p, errors.New("remoteEnabled: not bool")
		}
		p.Enabled = b
	}
	if v, exists := m["remoteHost"]; exists {
		s, ok := v.(string)
		if !ok {
			return p, errors.New("remoteHost: not string")
		}
		p.Host = s
	}
	if v, exists := m["remotePort"]; exists {
		num, ok := v.(json.Number)
		if !ok {
			return p, errors.New("remotePort: not number")
		}
		i64, err := num.Int64()
		if err != nil {
			return p, errors.New("remotePort: not integer")
		}
		if i64 < 0 || i64 > 65535 {
			return p, errors.New("remotePort: out of range")
		}
		p.Port = int(i64)
	}
	return p, nil
}

// --- Internal helpers: candidate construction ---

// buildCandidate parses original bytes as map[string]json.RawMessage, deletes
// remoteToken (any value type, value never inspected/copied), sets
// remoteSecurityVersion=1, and marshals with MarshalIndent+newline matching the
// Save format. Every other key is preserved with per-value JSON equality.
func buildCandidate(original []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(original, &m); err != nil {
		return nil, fmt.Errorf("parse original for candidate: %w", err)
	}
	if m == nil {
		return nil, errors.New("original settings is not a JSON object")
	}
	// DELETE remoteToken (any value type, including malformed). The value bytes
	// are never logged, copied, or inspected.
	delete(m, "remoteToken")
	// Set version 1.
	m["remoteSecurityVersion"] = json.RawMessage("1")
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	if len(out) > candidateMaxBytes {
		return nil, errors.New("candidate exceeds 1MiB")
	}
	return out, nil
}

// validateCandidateInvariants re-parses the candidate and verifies:
//   - remoteSecurityVersion is integer 1;
//   - remoteToken is absent;
//   - remoteHost/remotePort/remoteEnabled are JSON-semantically equal to the
//     original (primitives — compared by compacted bytes);
//   - size ≤ 1MiB.
func validateCandidateInvariants(originalBytes, candidateBytes []byte) error {
	if len(candidateBytes) > candidateMaxBytes {
		return errors.New("candidate > 1MiB")
	}
	var orig, cand map[string]json.RawMessage
	if err := json.Unmarshal(originalBytes, &orig); err != nil {
		return fmt.Errorf("reparse original: %w", err)
	}
	if err := json.Unmarshal(candidateBytes, &cand); err != nil {
		return fmt.Errorf("reparse candidate: %w", err)
	}

	// version == 1 (integer).
	vRaw, exists := cand["remoteSecurityVersion"]
	if !exists {
		return errors.New("candidate missing remoteSecurityVersion")
	}
	if !isIntegerNumber(vRaw, 1) {
		return errors.New("candidate remoteSecurityVersion is not integer 1")
	}

	// no remoteToken.
	if _, exists := cand["remoteToken"]; exists {
		return errors.New("candidate still contains remoteToken")
	}

	// tuple equal (semantic: compact both and compare bytes).
	for _, key := range []string{"remoteHost", "remotePort", "remoteEnabled"} {
		ov := orig[key]
		cv := cand[key]
		eq, err := jsonSemanticallyEqual(ov, cv)
		if err != nil {
			return fmt.Errorf("compare %s: %w", key, err)
		}
		if !eq {
			return fmt.Errorf("candidate %s changed", key)
		}
	}
	return nil
}

// isIntegerNumber reports whether raw is a JSON integer equal to want.
func isIntegerNumber(raw json.RawMessage, want int64) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return false
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	if err != nil {
		return false
	}
	return i == want
}

// jsonSemanticallyEqual compacts both RawMessages and compares bytes. This
// normalizes insignificant whitespace while preserving number representation.
func jsonSemanticallyEqual(a, b json.RawMessage) (bool, error) {
	// Missing on both sides → equal (absent key preserved as absent).
	if len(a) == 0 && len(b) == 0 {
		return true, nil
	}
	if len(a) == 0 || len(b) == 0 {
		return false, nil
	}
	ca, err := compactJSON(a)
	if err != nil {
		return false, err
	}
	cb, err := compactJSON(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ca, cb), nil
}

// compactJSON removes insignificant whitespace from a valid JSON value.
func compactJSON(raw json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
