package remote

// M1-B3a authority tests. These tests intentionally exercise disk authority,
// not merely mechanical handle plumbing.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newHardeningTestStore(t *testing.T) (*fileDeviceStore, *securityMaintenanceGate) {
	t.Helper()
	dir := t.TempDir()
	g := newSecurityMaintenanceGate()
	s := newFileDeviceStore(dir, newSecFakeClock(time.Now()), cryptoRand, g)
	permit, ok := g.issueNormalPermit()
	if !ok {
		t.Fatal("gate not normal")
	}
	if err := s.LoadOrInitialize(permit); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	g.returnNormalPermit(permit)
	return s, g
}

type b3aFixture struct {
	s      *fileDeviceStore
	g      *securityMaintenanceGate
	sess   MaintenanceSession
	backup DeviceStoreBackup
	lb     *liveBackup
}

func newB3AFixture(t *testing.T) b3aFixture {
	t.Helper()
	s, g := newHardeningTestStore(t)
	sess, err := s.BeginMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.BackupForMigration(sess)
	if err != nil {
		t.Fatalf("BackupForMigration: %v", err)
	}
	lb, ok := g.lookupBackup(sess, backup)
	if !ok {
		t.Fatal("backup capability not registered")
	}
	return b3aFixture{s: s, g: g, sess: sess, backup: backup, lb: lb}
}

func readB3AFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeB3AFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func manifestBytes(t *testing.T, m backupManifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func decodeManifest(t *testing.T, raw []byte) backupManifest {
	t.Helper()
	var m backupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func authorityForRaw(a backupAuthority, raw []byte) backupAuthority {
	d := sha256.Sum256(raw)
	a.info.ManifestSHA256 = hex.EncodeToString(d[:])
	return a
}

func TestB3ABaselineSnapshotSHAAndImmediateRegistrationValidation(t *testing.T) {
	s, g := newHardeningTestStore(t)
	sess, err := s.BeginMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	defer s.AbortMaintenance(sess)

	live := readB3AFile(t, s.snapshotPath)
	d := sha256.Sum256(live)
	if got, want := s.maintenanceBaseline.snapshotHash, hex.EncodeToString(d[:]); got != want || got == "" {
		t.Fatalf("baseline snapshot hash=%q want=%q", got, want)
	}

	oldHook := maintenanceBackupBeforeVerify
	maintenanceBackupBeforeVerify = func(dir string) error {
		writeB3AFile(t, filepath.Join(dir, "manifest.json"), []byte("{}\n"))
		return nil
	}
	defer func() { maintenanceBackupBeforeVerify = oldHook }()

	if _, err := s.BackupForMigration(sess); err == nil {
		t.Fatal("backup tampered before registration must fail")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.live.backups) != 0 || len(g.cleanupRecords) != 0 {
		t.Fatal("failed immediate verification must not register authority")
	}
}

func TestB3AStrictManifestAndDiskMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte)
	}{
		{"duplicate", func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
			bad := []byte(strings.Replace(string(raw), "{", `{"version":1,`, 1))
			return authorityForRaw(f.lb.backupAuthority, bad), bad
		}},
		{"unknown", func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
			bad := []byte(strings.TrimSuffix(string(raw), "}\n") + `,"unknown":true}` + "\n")
			return authorityForRaw(f.lb.backupAuthority, bad), bad
		}},
		{"missing", func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
			var obj map[string]any
			_ = json.Unmarshal(raw, &obj)
			delete(obj, "walBytes")
			bad, _ := json.Marshal(obj)
			bad = append(bad, '\n')
			return authorityForRaw(f.lb.backupAuthority, bad), bad
		}},
		{"noncanonical", func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
			bad := append([]byte(" "), raw...)
			return authorityForRaw(f.lb.backupAuthority, bad), bad
		}},
		{"version", manifestMutation(func(m *backupManifest) { m.Version++ })},
		{"id", manifestMutation(func(m *backupManifest) { m.BackupID = strings.Repeat("a", 32) })},
		{"time", manifestMutation(func(m *backupManifest) { m.CreatedAt = "not-a-time" })},
		{"size", manifestMutation(func(m *backupManifest) { m.SnapshotBytes++ })},
		{"hash", manifestMutation(func(m *backupManifest) { m.SnapshotSHA256 = strings.Repeat("0", 64) })},
		{"head", manifestMutation(func(m *backupManifest) { m.LedgerHeadHash = strings.Repeat("0", 64) })},
		{"sequence", manifestMutation(func(m *backupManifest) { m.LedgerSequence++ })},
		{"mode", func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
			if err := os.Chmod(filepath.Join(f.lb.dir, snapshotFilename), 0o644); err != nil {
				t.Fatal(err)
			}
			return f.lb.backupAuthority, raw
		}},
		{"symlink", func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
			p := filepath.Join(f.lb.dir, snapshotFilename)
			outside := filepath.Join(f.s.dir, "outside-snapshot")
			writeB3AFile(t, outside, readB3AFile(t, p))
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, p); err != nil {
				t.Fatal(err)
			}
			return f.lb.backupAuthority, raw
		}},
		{"extra", func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
			writeB3AFile(t, filepath.Join(f.lb.dir, "extra"), []byte("x"))
			return f.lb.backupAuthority, raw
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newB3AFixture(t)
			defer f.s.AbortMaintenance(f.sess)
			mp := filepath.Join(f.lb.dir, "manifest.json")
			raw := readB3AFile(t, mp)
			authority, next := tc.mutate(t, f, raw)
			if !bytes.Equal(raw, next) {
				writeB3AFile(t, mp, next)
			}
			if _, err := f.s.parseAndVerifyBackup(authority); err == nil {
				t.Fatalf("%s must be rejected", tc.name)
			}
		})
	}
}

func manifestMutation(fn func(*backupManifest)) func(*testing.T, b3aFixture, []byte) (backupAuthority, []byte) {
	return func(t *testing.T, f b3aFixture, raw []byte) (backupAuthority, []byte) {
		m := decodeManifest(t, raw)
		fn(&m)
		bad := manifestBytes(t, m)
		return authorityForRaw(f.lb.backupAuthority, bad), bad
	}
}

func TestB3ASharedParserGuardsRestoreInfoAndCleanup(t *testing.T) {
	f := newB3AFixture(t)
	mp := filepath.Join(f.lb.dir, "manifest.json")
	raw := readB3AFile(t, mp)
	writeB3AFile(t, mp, []byte("{}\n"))

	if err := f.s.RestoreMigrationBackup(f.sess, f.backup); err == nil {
		t.Fatal("restore accepted invalid manifest")
	}
	if _, err := f.s.MigrationBackupInfo(f.backup); err == nil {
		t.Fatal("info accepted invalid manifest")
	}
	if err := f.s.EndMaintenance(f.sess); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CleanupMigrationBackup(f.backup); err == nil {
		t.Fatal("cleanup accepted invalid manifest")
	}
	writeB3AFile(t, mp, raw)
	if err := f.s.CleanupMigrationBackup(f.backup); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
}

func TestB3ACandidateBoundariesAndPlusOneBeforeLiveWrites(t *testing.T) {
	if err := validateMaintenanceCandidateSizes(MaxSnapshotBytes, LedgerMaxBytes); err != nil {
		t.Fatalf("exact boundaries rejected: %v", err)
	}
	if err := validateMaintenanceCandidateSizes(MaxSnapshotBytes+1, LedgerMaxBytes); err == nil {
		t.Fatal("snapshot +1 accepted")
	}
	if err := validateMaintenanceCandidateSizes(MaxSnapshotBytes, LedgerMaxBytes+1); err == nil {
		t.Fatal("WAL +1 accepted")
	}

	for _, tc := range []struct {
		name string
		snap bool
	}{{"snapshot", true}, {"wal", false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newB3AFixture(t)
			defer f.s.AbortMaintenance(f.sess)
			oldSnap := readB3AFile(t, f.s.snapshotPath)
			oldWal := readB3AFile(t, f.s.ledgerPath)
			snap, wal := oldSnap, oldWal
			if tc.snap {
				snap = make([]byte, MaxSnapshotBytes+1)
			} else {
				wal = make([]byte, LedgerMaxBytes+1)
			}
			if err := f.s.WithMigrationWriter(f.sess, func(w deviceStoreMaintenanceWriter) error {
				return w.StageCandidate(snap, wal)
			}); err == nil {
				t.Fatal("oversize candidate accepted")
			}
			if !bytes.Equal(readB3AFile(t, f.s.snapshotPath), oldSnap) || !bytes.Equal(readB3AFile(t, f.s.ledgerPath), oldWal) {
				t.Fatal("oversize rejection wrote live store")
			}
		})
	}
}

func TestB3AOneBackupPerSessionRejectedBeforeDiskWork(t *testing.T) {
	f := newB3AFixture(t)
	defer f.s.AbortMaintenance(f.sess)
	oldReadDir := maintenanceReadDir
	maintenanceReadDir = func(string) ([]os.DirEntry, error) {
		t.Fatal("second backup reached disk")
		return nil, errors.New("unreachable")
	}
	defer func() { maintenanceReadDir = oldReadDir }()
	if _, err := f.s.BackupForMigration(f.sess); err == nil {
		t.Fatal("second backup accepted")
	}
}

func TestB3ARestoreEveryStageFaultThenRetryExactBaseline(t *testing.T) {
	phases := []string{
		"wal-temp", "wal-write", "wal-sync", "wal-rename", "wal-readback",
		"snapshot-temp", "snapshot-write", "snapshot-sync", "snapshot-rename", "snapshot-readback",
	}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			f := newB3AFixture(t)
			defer f.s.AbortMaintenance(f.sess)
			wantSnap := readB3AFile(t, filepath.Join(f.lb.dir, snapshotFilename))
			wantWal := readB3AFile(t, filepath.Join(f.lb.dir, ledgerFilename))
			writeB3AFile(t, f.s.ledgerPath, []byte("broken-wal\n"))
			writeB3AFile(t, f.s.snapshotPath, []byte("broken-snapshot\n"))

			oldFault := maintenanceStageFault
			fired := false
			maintenanceStageFault = func(got string) error {
				if got == phase && !fired {
					fired = true
					return errors.New("injected " + phase)
				}
				return nil
			}
			if err := f.s.RestoreMigrationBackup(f.sess, f.backup); err == nil || !fired {
				t.Fatalf("fault %s was not observed: %v", phase, err)
			}
			maintenanceStageFault = oldFault
			defer func() { maintenanceStageFault = oldFault }()

			if err := f.s.RestoreMigrationBackup(f.sess, f.backup); err != nil {
				t.Fatalf("retry after %s: %v", phase, err)
			}
			if !bytes.Equal(readB3AFile(t, f.s.snapshotPath), wantSnap) || !bytes.Equal(readB3AFile(t, f.s.ledgerPath), wantWal) {
				t.Fatalf("retry after %s did not restore exact baseline", phase)
			}
			if err := f.s.ValidateMaintenanceStore(f.sess); err != nil {
				t.Fatalf("validate after %s retry: %v", phase, err)
			}
		})
	}
}

func TestB3AInfoBeforeAfterEndAndServerWrapper(t *testing.T) {
	f := newB3AFixture(t)
	before, err := f.s.MigrationBackupInfo(f.backup)
	if err != nil || before.BackupID == "" || before.ManifestSHA256 == "" {
		t.Fatalf("info before End: %+v %v", before, err)
	}
	server := &Server{store: f.s}
	wrapped, err := server.MigrationBackupInfo(f.backup)
	if err != nil || wrapped != before {
		t.Fatalf("server wrapper before End: %+v %v", wrapped, err)
	}
	if err := f.s.EndMaintenance(f.sess); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.MigrationBackupInfo(f.backup)
	if err != nil || after != before {
		t.Fatalf("info after End: %+v %v", after, err)
	}
	wrapped, err = server.MigrationBackupInfo(f.backup)
	if err != nil || wrapped != before {
		t.Fatalf("server wrapper after End: %+v %v", wrapped, err)
	}
}

func TestB3ACleanupRetryRecordLifecycleAndCapabilities(t *testing.T) {
	f := newB3AFixture(t)
	mp := filepath.Join(f.lb.dir, "manifest.json")
	raw := readB3AFile(t, mp)
	if err := f.s.EndMaintenance(f.sess); err != nil {
		t.Fatal(err)
	}
	writeB3AFile(t, mp, []byte("{}\n"))
	if err := f.s.CleanupMigrationBackup(f.backup); err == nil {
		t.Fatal("invalid cleanup accepted")
	}
	f.g.mu.Lock()
	_, retained := f.g.cleanupRecords[f.backup.token]
	f.g.mu.Unlock()
	if !retained {
		t.Fatal("validation failure discarded cleanup record")
	}
	writeB3AFile(t, mp, raw)

	copyOfCapability := f.backup
	if err := f.s.CleanupMigrationBackup(copyOfCapability); err != nil {
		t.Fatalf("struct copy is the same capability: %v", err)
	}
	f.g.mu.Lock()
	_, retained = f.g.cleanupRecords[f.backup.token]
	f.g.mu.Unlock()
	if retained {
		t.Fatal("successful cleanup retained record")
	}
	if err := f.s.CleanupMigrationBackup(f.backup); err == nil {
		t.Fatal("second cleanup must be stale, not false success")
	}
	if err := f.s.CleanupMigrationBackup(DeviceStoreBackup{}); err == nil {
		t.Fatal("zero handle accepted")
	}
	other, _ := newHardeningTestStore(t)
	if err := other.CleanupMigrationBackup(f.backup); err == nil {
		t.Fatal("cross-store handle accepted")
	}
}

func TestB3AOrphanReadDirErrorPropagatesWithoutSkip(t *testing.T) {
	s, _ := newHardeningTestStore(t)
	sess, err := s.BeginMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	defer s.AbortMaintenance(sess)
	sentinel := errors.New("read-dir-fault")
	oldReadDir := maintenanceReadDir
	maintenanceReadDir = func(path string) ([]os.DirEntry, error) {
		if path == s.backupDir {
			return nil, sentinel
		}
		return oldReadDir(path)
	}
	defer func() { maintenanceReadDir = oldReadDir }()
	if _, err := s.BackupForMigration(sess); err == nil {
		t.Fatal("ReadDir error was ignored")
	}
}

func TestB3AFailureCleanupLeavesForeignEntryNeverRemoveAll(t *testing.T) {
	s, g := newHardeningTestStore(t)
	sess, err := s.BeginMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	defer s.AbortMaintenance(sess)
	var foreign string
	oldHook := maintenanceBackupBeforeVerify
	maintenanceBackupBeforeVerify = func(dir string) error {
		foreign = filepath.Join(dir, "foreign")
		writeB3AFile(t, foreign, []byte("owner-unknown"))
		writeB3AFile(t, filepath.Join(dir, "manifest.json"), []byte("{}\n"))
		return nil
	}
	defer func() { maintenanceBackupBeforeVerify = oldHook }()
	if _, err := s.BackupForMigration(sess); err == nil {
		t.Fatal("tampered backup accepted")
	}
	if got := readB3AFile(t, foreign); string(got) != "owner-unknown" {
		t.Fatal("failure cleanup removed or changed foreign entry")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.live.backups) != 0 || len(g.cleanupRecords) != 0 {
		t.Fatal("failed backup retained capability records")
	}
}
