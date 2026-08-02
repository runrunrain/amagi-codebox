package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// NewRemoteMigrationStore is a test-only shorthand alias.
func NewRemoteMigrationStore(dir string) *RemoteSecurityMigrationStore {
	return NewRemoteSecurityMigrationStore(dir)
}

// --- Detect matrix ---

func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if content == "" {
		return // file absent
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

func TestDetect_MissingSettings_NewInstall(t *testing.T) {
	dir := t.TempDir()
	store := NewRemoteSecurityMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionMissingNewInstall {
		t.Fatalf("state = %s, want missing_new_install", d.State)
	}
}

func TestDetect_V0_NeedsMigration(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"dashboard":{"mode":"embedded"}}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionNeedsMigration {
		t.Fatalf("state = %s, want needs_migration", d.State)
	}
	if d.Prior.Host != "0.0.0.0" {
		t.Fatalf("prior host = %q, want 0.0.0.0", d.Prior.Host)
	}
	if d.Prior.Port != 9999 {
		t.Fatalf("prior port = %d, want 9999", d.Prior.Port)
	}
	if !d.Prior.Enabled {
		t.Fatalf("prior enabled = false, want true")
	}
}

func TestDetect_V0_MissingTupleFields_Defaults(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"dashboard":{"mode":"embedded"}}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionNeedsMigration {
		t.Fatalf("state = %s, want needs_migration", d.State)
	}
	if d.Prior.Host != "127.0.0.1" {
		t.Fatalf("default host = %q, want 127.0.0.1", d.Prior.Host)
	}
	if d.Prior.Port != 8680 {
		t.Fatalf("default port = %d, want 8680", d.Prior.Port)
	}
	if d.Prior.Enabled {
		t.Fatalf("default enabled = true, want false")
	}
}

func TestDetect_V1_Current(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":1,"remoteHost":"127.0.0.1","remotePort":8680,"remoteEnabled":false}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionCurrent {
		t.Fatalf("state = %s, want current", d.State)
	}
}

func TestDetect_FutureVersion2(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":2}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionFutureOrInvalid {
		t.Fatalf("state = %s, want future_or_invalid", d.State)
	}
}

func TestDetect_InvalidVersionString(t *testing.T) {
	dir := t.TempDir()
	// "1" as a JSON string is a non-number version → FutureOrInvalid.
	writeSettings(t, dir, `{"remoteSecurityVersion":"1"}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionFutureOrInvalid {
		t.Fatalf("state = %s, want future_or_invalid", d.State)
	}
}

func TestDetect_InvalidVersionFloat(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":1.5}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionFutureOrInvalid {
		t.Fatalf("state = %s, want future_or_invalid", d.State)
	}
}

func TestDetect_MalformedJSON_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{not valid json`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair", d.State)
	}
}

func TestDetect_WrongTypedHost_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteHost":123}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair (wrong-typed host)", d.State)
	}
}

func TestDetect_WrongTypedPort_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remotePort":"8680"}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair (wrong-typed port)", d.State)
	}
}

func TestDetect_WrongTypedEnabled_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteEnabled":"yes"}`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair (wrong-typed enabled)", d.State)
	}
}

func TestDetect_NullLiteral_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `null`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair", d.State)
	}
}

func TestDetect_TrailingData_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}garbage`)
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair", d.State)
	}
}

// --- Marker / orphan txn dir ---

func TestDetect_MarkerPrepared_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txnDir, markerFileName), canonicalMarker(markerStatePrepared), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair", d.State)
	}
}

func TestDetect_MarkerSettingsCommitted_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":1}`)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txnDir, markerFileName), canonicalMarker(markerStateSettingsCommitted), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair", d.State)
	}
}

func TestDetect_MarkerCorrupt_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":1}`)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txnDir, markerFileName), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair", d.State)
	}
}

func TestDetect_MarkerUnknownState_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":1}`)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txnDir, markerFileName), []byte(`{"state":"unknown_phase","version":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair", d.State)
	}
}

func TestDetect_OrphanTxnDir_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":1}`)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// No marker — orphan txn dir.
	store := NewRemoteMigrationStore(dir)
	d, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.State != DetectionManualRepair {
		t.Fatalf("state = %s, want manual_repair (orphan txn dir)", d.State)
	}
}

func TestBegin_RefusedWhenTxnDirExists(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":1}`)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewRemoteMigrationStore(dir)
	if _, err := store.Begin(); err == nil {
		t.Fatal("Begin should be refused when txn dir exists")
	}
}

// --- remoteToken removal ---

func TestStage_RemovesRemoteTokenString(t *testing.T) {
	const canary = "CANARY-deadbeef-1234"
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"` + canary + `","dashboard":{"mode":"embedded"}}`
	writeSettings(t, dir, original)

	store := NewRemoteMigrationStore(dir)
	d, _ := store.Detect()
	if d.State != DetectionNeedsMigration {
		t.Fatalf("state = %s, want needs_migration", d.State)
	}

	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = m.Abort() })

	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	cand, err := os.ReadFile(filepath.Join(dir, migrationTxnDirName, candidateFileName))
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if strings.Contains(string(cand), "remoteToken") {
		t.Fatal("candidate still contains remoteToken key")
	}
	if strings.Contains(string(cand), canary) {
		t.Fatal("candidate contains canary token value")
	}

	// Verify candidate parses and has version 1.
	var cm map[string]json.RawMessage
	if err := json.Unmarshal(cand, &cm); err != nil {
		t.Fatalf("candidate parse: %v", err)
	}
	if !isIntegerNumber(cm["remoteSecurityVersion"], 1) {
		t.Fatal("candidate version is not integer 1")
	}
}

func TestStage_RemovesMalformedRemoteTokenValue(t *testing.T) {
	dir := t.TempDir()
	// remoteToken present as a non-string (object) — still removed.
	original := `{"remoteHost":"127.0.0.1","remotePort":8680,"remoteEnabled":false,"remoteToken":{"nested":"obj"},"remoteSecurityVersion":0}`
	writeSettings(t, dir, original)

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = m.Abort() })

	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	cand, err := os.ReadFile(filepath.Join(dir, migrationTxnDirName, candidateFileName))
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if strings.Contains(string(cand), "remoteToken") {
		t.Fatal("candidate still contains remoteToken (malformed value not removed)")
	}
}

func TestStage_RemovesNumericRemoteTokenValue(t *testing.T) {
	dir := t.TempDir()
	original := `{"remoteHost":"127.0.0.1","remotePort":8680,"remoteEnabled":false,"remoteToken":12345,"remoteSecurityVersion":0}`
	writeSettings(t, dir, original)

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = m.Abort() })

	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	cand, _ := os.ReadFile(filepath.Join(dir, migrationTxnDirName, candidateFileName))
	if strings.Contains(string(cand), "remoteToken") {
		t.Fatal("candidate still contains remoteToken (numeric value not removed)")
	}
}

// --- Unknown field preservation ---

func TestStage_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"customField":{"nested":[1,2,3]},"anotherUnknown":"hello","remoteSecurityVersion":0}`
	writeSettings(t, dir, original)

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = m.Abort() })

	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	cand, err := os.ReadFile(filepath.Join(dir, migrationTxnDirName, candidateFileName))
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}

	// Parse both and compare unknown fields semantically.
	var origMap, candMap map[string]json.RawMessage
	json.Unmarshal([]byte(original), &origMap)
	json.Unmarshal(cand, &candMap)

	for _, key := range []string{"customField", "anotherUnknown"} {
		eq, err := jsonSemanticallyEqual(origMap[key], candMap[key])
		if err != nil {
			t.Fatalf("compare %s: %v", key, err)
		}
		if !eq {
			t.Fatalf("unknown field %s not preserved semantically", key)
		}
	}
}

// --- Happy path: Stage → Commit → Finish ---

func TestHappyPath_CommitFinish_LeavesV1SettingsZeroResidue(t *testing.T) {
	const canary = "CANARY-happy-5678"
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"` + canary + `","dashboard":{"mode":"embedded"}}`
	writeSettings(t, dir, original)

	store := NewRemoteMigrationStore(dir)
	d, _ := store.Detect()
	if d.State != DetectionNeedsMigration {
		t.Fatalf("state = %s, want needs_migration", d.State)
	}

	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	res, err := m.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Outcome != CommitCommitted {
		t.Fatalf("outcome = %s, want committed", res.Outcome)
	}
	if err := m.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// settings.json is now v1, no token, tuple preserved.
	final, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read final settings: %v", err)
	}
	var fm map[string]json.RawMessage
	if err := json.Unmarshal(final, &fm); err != nil {
		t.Fatalf("parse final: %v", err)
	}
	if !isIntegerNumber(fm["remoteSecurityVersion"], 1) {
		t.Fatal("final settings version is not 1")
	}
	if _, exists := fm["remoteToken"]; exists {
		t.Fatal("final settings still has remoteToken")
	}
	if strings.Contains(string(final), canary) {
		t.Fatal("canary token survived into final settings.json")
	}
	// tuple preserved
	hv, _ := jsonSemanticallyEqual(json.RawMessage(`"0.0.0.0"`), fm["remoteHost"])
	if !hv {
		t.Fatalf("remoteHost not preserved: %s", fm["remoteHost"])
	}

	// Zero residue: txn dir gone.
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be removed after Finish")
	}

	// Detect now reports Current.
	d2, _ := store.Detect()
	if d2.State != DetectionCurrent {
		t.Fatalf("post-finish Detect = %s, want current", d2.State)
	}

	// Final settings.json is parseable by the ordinary Service.Load.
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Service.Load after migration: %v", err)
	}
	if svc.GetRemoteHost() != "0.0.0.0" {
		t.Fatalf("Service remote host = %q, want 0.0.0.0", svc.GetRemoteHost())
	}
	if svc.GetRemotePort() != 9999 {
		t.Fatalf("Service remote port = %d, want 9999", svc.GetRemotePort())
	}
}

// --- Rollback post-commit ---

func TestRollback_PostCommit_RestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"secret","remoteSecurityVersion":0}`
	writeSettings(t, dir, original)
	origBytes, _ := os.ReadFile(filepath.Join(dir, "settings.json"))

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Post-commit rollback restores exact original bytes.
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	current, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(current) != string(origBytes) {
		t.Fatalf("post-rollback settings != original\n got: %s\nwant: %s", current, origBytes)
	}

	// Zero residue.
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be removed after Rollback")
	}
}

func TestRollback_PreCommit_RestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	original := `{"remoteHost":"127.0.0.1","remotePort":8680,"remoteEnabled":false,"remoteSecurityVersion":0}`
	writeSettings(t, dir, original)
	origBytes, _ := os.ReadFile(filepath.Join(dir, "settings.json"))

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// Pre-commit rollback: settings.json was never modified.
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	current, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(current) != string(origBytes) {
		t.Fatalf("pre-commit rollback changed settings\n got: %s\nwant: %s", current, origBytes)
	}
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be removed")
	}
}

// --- Fault injection ---

func TestFault_BackupWriteFails_StageFails(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	m.seams.backupWrite = func(txnDir string, data []byte) error {
		return errors.New("injected backup failure")
	}
	if err := m.Stage(); err == nil {
		t.Fatal("Stage should fail when backup write fails")
	}
	// Rollback to clean up (settings.json untouched, txn dir removed).
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback after fault: %v", err)
	}
	// settings.json unchanged + no residue.
	orig, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !strings.Contains(string(orig), "remoteSecurityVersion") {
		t.Fatal("settings.json corrupted by failed Stage")
	}
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be removed after Rollback")
	}
}

func TestFault_MarkerWriteFails_StageFails(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	m.seams.markerWrite = func(txnDir string, data []byte) error {
		return errors.New("injected marker failure")
	}
	if err := m.Stage(); err == nil {
		t.Fatal("Stage should fail when marker write fails")
	}
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be removed")
	}
}

func TestFault_CandidateWriteFails_StageFails(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	m.seams.candidateWrite = func(txnDir string, data []byte) error {
		return errors.New("injected candidate failure")
	}
	if err := m.Stage(); err == nil {
		t.Fatal("Stage should fail when candidate write fails")
	}
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be removed")
	}
}

func TestFault_RenameNoOp_NotCommitted(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteSecurityVersion":0}`)
	origBytes, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// rename is a no-op (returns nil without renaming).
	m.seams.rename = func(old, new string) error { return nil }
	res, err := m.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Outcome != CommitNotCommitted {
		t.Fatalf("outcome = %s, want not_committed", res.Outcome)
	}
	// settings.json unchanged.
	current, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(current) != string(origBytes) {
		t.Fatal("settings.json changed despite not_committed")
	}
	// Rollback to clean up.
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be removed after Rollback")
	}
}

func TestFault_RenameCorrupts_Indeterminate(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"tok","remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// rename writes garbage to settings.json.
	m.seams.rename = func(old, new string) error {
		return os.WriteFile(new, []byte("garbage-corrupt"), 0o600)
	}
	res, err := m.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Outcome != CommitIndeterminate {
		t.Fatalf("outcome = %s, want indeterminate", res.Outcome)
	}
	// Capability killed; marker+backup kept.
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if _, err := os.Lstat(txnDir); errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be KEPT after indeterminate")
	}
	if _, err := os.Lstat(filepath.Join(txnDir, markerFileName)); errors.Is(err, os.ErrNotExist) {
		t.Fatal("marker should be KEPT after indeterminate")
	}
	if _, err := os.Lstat(filepath.Join(txnDir, backupFileName)); errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup should be KEPT after indeterminate")
	}
	// Next Detect → ManualRepair.
	store2 := NewRemoteMigrationStore(dir)
	d, _ := store2.Detect()
	if d.State != DetectionManualRepair {
		t.Fatalf("post-indeterminate Detect = %s, want manual_repair", d.State)
	}
}

// --- Finish marker-delete failure ---
// With the backup-first deletion order (Major-02), the sensitive backup is
// already gone when the marker delete fails; the marker + txn dir remain so the
// next Detect still flags ManualRepair. The token is NOT left on disk.
func TestFinish_MarkerDeleteFailure_TokenGone_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Inject marker delete failure (runs AFTER backup was already deleted).
	m.seams.markerRemove = func(path string) error {
		return errors.New("injected marker delete failure")
	}
	if err := m.Finish(); err == nil {
		t.Fatal("Finish should fail when marker delete fails")
	}
	// Capability killed (closed).
	if err := m.Abort(); err != ErrMigrationClosed {
		t.Fatalf("use-after-close: got %v, want ErrMigrationClosed", err)
	}
	txnDir := filepath.Join(dir, migrationTxnDirName)
	// Backup already DELETED (token gone) — the sensitive artifact is removed first.
	if _, err := os.Lstat(filepath.Join(txnDir, backupFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup should be DELETED before marker delete is attempted")
	}
	// Marker + txn dir KEPT → repairable evidence, next Detect → ManualRepair.
	if _, err := os.Lstat(filepath.Join(txnDir, markerFileName)); errors.Is(err, os.ErrNotExist) {
		t.Fatal("marker should be KEPT when marker delete fails")
	}
	if _, err := os.Lstat(txnDir); errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir should be KEPT when marker delete fails")
	}
	store2 := NewRemoteMigrationStore(dir)
	d, _ := store2.Detect()
	if d.State != DetectionManualRepair {
		t.Fatalf("Detect = %s, want manual_repair", d.State)
	}
}

// --- Capability / use-after-close ---

func TestOneLiveCapability_SecondBeginRefused(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = m.Abort() })

	// Second Begin on same configDir → refused (even from a new store).
	store2 := NewRemoteMigrationStore(dir)
	if _, err := store2.Begin(); !errors.Is(err, ErrCapabilityActive) {
		t.Fatalf("second Begin err = %v, want ErrCapabilityActive", err)
	}
}

func TestUseAfterClose_ReturnsErrClosed(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	// All subsequent ops → ErrMigrationClosed.
	if err := m.Stage(); err != ErrMigrationClosed {
		t.Fatalf("Stage after close: %v, want ErrMigrationClosed", err)
	}
	if _, err := m.Commit(); err != ErrMigrationClosed {
		t.Fatalf("Commit after close: %v, want ErrMigrationClosed", err)
	}
	if err := m.Rollback(); err != ErrMigrationClosed {
		t.Fatalf("Rollback after close: %v, want ErrMigrationClosed", err)
	}
	if err := m.Finish(); err != ErrMigrationClosed {
		t.Fatalf("Finish after close: %v, want ErrMigrationClosed", err)
	}
	if err := m.Abort(); err != ErrMigrationClosed {
		t.Fatalf("Abort after close: %v, want ErrMigrationClosed", err)
	}
}

func TestBegin_NewConfigDirAfterAbort_Succeeds(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	// After Abort, capability released; new Begin succeeds.
	m2, err := store.Begin()
	if err != nil {
		t.Fatalf("second Begin after Abort: %v", err)
	}
	_ = m2.Abort()
}

// --- File modes ---

func TestModes_TxnDir0700_Artifacts0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits validated on macOS/Linux")
	}
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = m.Abort() })
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	txnDir := filepath.Join(dir, migrationTxnDirName)
	if di, err := os.Stat(txnDir); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("txn dir mode = %v, want 0700", err)
	}
	for _, name := range []string{markerFileName, backupFileName, candidateFileName} {
		fi, err := os.Stat(filepath.Join(txnDir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 0600", name, fi.Mode().Perm())
		}
	}
	// configDir 0700.
	if di, err := os.Stat(dir); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode = %v, want 0700", err)
	}
}

func TestModes_SettingsJson0600_AfterCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits validated on macOS/Linux")
	}
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("stat settings.json: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json mode = %o, want 0600", fi.Mode().Perm())
	}
	_ = m.Finish()
}

// --- Privacy canary ---

func TestPrivacy_CanaryAbsentFromCandidateMarkerErrors(t *testing.T) {
	const canary = "PRIVACY-CANARY-9abc-def0"
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"` + canary + `","remoteSecurityVersion":0}`
	writeSettings(t, dir, original)

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = m.Abort() })
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	txnDir := filepath.Join(dir, migrationTxnDirName)
	// Candidate must NOT contain canary.
	cand, _ := os.ReadFile(filepath.Join(txnDir, candidateFileName))
	if strings.Contains(string(cand), canary) {
		t.Fatal("canary leaked into candidate")
	}
	// Marker must NOT contain canary.
	mk, _ := os.ReadFile(filepath.Join(txnDir, markerFileName))
	if strings.Contains(string(mk), canary) {
		t.Fatal("canary leaked into marker")
	}
	// Backup MAY contain canary (0600, retention for rollback).
	bk, _ := os.ReadFile(filepath.Join(txnDir, backupFileName))
	if !strings.Contains(string(bk), canary) {
		t.Fatal("backup should contain canary (exact original bytes)")
	}
	// Backup mode 0600 (POSIX only).
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(txnDir, backupFileName))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("backup mode = %o, want 0600", fi.Mode().Perm())
		}
	}
}

func TestPrivacy_CanaryAbsentFromStageError(t *testing.T) {
	const canary = "PRIVACY-ERR-CANARY-7777"
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"` + canary + `","remoteSecurityVersion":0}`
	writeSettings(t, dir, original)

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// Inject a candidate-write failure so Stage returns an error; verify the
	// error string never contains the canary token value.
	m.seams.candidateWrite = func(txnDir string, data []byte) error {
		return errors.New("injected candidate write failure")
	}
	stageErr := m.Stage()
	if stageErr == nil {
		t.Fatal("Stage should have failed")
	}
	if strings.Contains(stageErr.Error(), canary) {
		t.Fatalf("canary leaked into Stage error: %s", stageErr)
	}
	_ = m.Rollback()
}

// --- Symlink rejection ---

func TestBegin_RejectsSymlinkConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics validated on POSIX")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	store := NewRemoteMigrationStore(link)
	if _, err := store.Begin(); !errors.Is(err, ErrSymlinkConfigDir) {
		t.Fatalf("Begin on symlink: err = %v, want ErrSymlinkConfigDir", err)
	}
}

// --- Service integration: defaultSettings carries v1 ---

func TestDefaultSettings_RemoteSecurityVersion1(t *testing.T) {
	svc := NewService(t.TempDir())
	s := svc.GetSettings()
	if s.RemoteSecurityVersion != 1 {
		t.Fatalf("default RemoteSecurityVersion = %d, want 1", s.RemoteSecurityVersion)
	}
}

func TestServiceSave_PersistsRemoteSecurityVersion1(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !isIntegerNumber(m["remoteSecurityVersion"], 1) {
		t.Fatal("saved settings does not have remoteSecurityVersion == 1")
	}
}

// --- Missing new install → implicit v1, no eager write ---

func TestMissingNewInstall_NoEagerWrite(t *testing.T) {
	dir := t.TempDir()
	store := NewRemoteMigrationStore(dir)
	d, _ := store.Detect()
	if d.State != DetectionMissingNewInstall {
		t.Fatalf("state = %s, want missing_new_install", d.State)
	}
	// No settings.json created by Detect.
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Detect must not eagerly write settings.json for new install")
	}
}

// --- Major-02: Finish atomicity (backup/candidate/marker/txn/sync all checked) ---
// These use the package-private migrationSeams to prove every Finish step is
// checked and a failure preserves repairable evidence (txn dir → ManualRepair).

// stageCommitForFinish runs a successful Stage+Commit and returns the capability
// ready for Finish. The canary lets tests assert the token-bearing backup state.
func stageCommitForFinish(t *testing.T, dir string) *Migration {
	t.Helper()
	writeSettings(t, dir, `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"CANARY-finish-9af1","remoteSecurityVersion":0}`)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return m
}

func TestFinish_BackupRemoveFails_KeepsToken_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	m := stageCommitForFinish(t, dir)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	m.seams.backupRemove = func(string) error { return errors.New("injected backup remove failure") }

	if err := m.Finish(); err == nil {
		t.Fatal("Finish must fail when the token-bearing backup cannot be deleted")
	}
	// The sensitive backup + marker are KEPT → repairable evidence.
	if _, err := os.Lstat(filepath.Join(txnDir, backupFileName)); errors.Is(err, os.ErrNotExist) {
		t.Fatal("token-bearing backup must be KEPT when its deletion fails")
	}
	if _, err := os.Lstat(filepath.Join(txnDir, markerFileName)); errors.Is(err, os.ErrNotExist) {
		t.Fatal("marker must be KEPT when backup deletion fails")
	}
	store2 := NewRemoteMigrationStore(dir)
	d, _ := store2.Detect()
	if d.State != DetectionManualRepair {
		t.Fatalf("Detect=%s want manual_repair", d.State)
	}
}

func TestFinish_TxnDirRemoveFails_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	m := stageCommitForFinish(t, dir)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	m.seams.txnDirRemove = func(string) error { return errors.New("injected txn dir remove failure") }

	if err := m.Finish(); err == nil {
		t.Fatal("Finish must fail when the txn dir cannot be removed")
	}
	// Backup was deleted first (token gone), but txn dir + marker remain.
	if _, err := os.Lstat(filepath.Join(txnDir, backupFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup must be DELETED before the txn-dir step")
	}
	if _, err := os.Lstat(txnDir); errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir must be KEPT when its removal fails")
	}
	store2 := NewRemoteMigrationStore(dir)
	d, _ := store2.Detect()
	if d.State != DetectionManualRepair {
		t.Fatalf("Detect=%s want manual_repair", d.State)
	}
}

func TestFinish_ParentSyncFails_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	m := stageCommitForFinish(t, dir)
	txnDir := filepath.Join(dir, migrationTxnDirName)
	m.seams.parentSync = func(string) error { return errors.New("injected parent sync failure") }

	if err := m.Finish(); err == nil {
		t.Fatal("Finish must fail when the parent-dir Sync fails")
	}
	// All files were removed but the Sync failed; the error must propagate so the
	// caller forbids Start. (The txn dir is already gone; the failure is the Sync.)
	if _, err := os.Lstat(txnDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("txn dir should be removed before the Sync step: %v", err)
	}
}

func TestFinish_HappyPath_AllArtifactsRemoved(t *testing.T) {
	dir := t.TempDir()
	m := stageCommitForFinish(t, dir)
	if err := m.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	txnDir := filepath.Join(dir, migrationTxnDirName)
	if _, err := os.Lstat(txnDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir must be fully removed on Finish success")
	}
	final, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(final), "CANARY-finish-9af1") {
		t.Fatal("canary token must be gone from settings.json after Finish")
	}
	// Capability is closed after Finish.
	if err := m.Abort(); err != ErrMigrationClosed {
		t.Fatalf("use-after-close: got %v want ErrMigrationClosed", err)
	}
}

// --- R2-Major-02: split rollback (RestoreExactOld + DiscardTransaction) ---
//
// The marker file MUST survive RestoreExactOld so that a Device Store End
// failure (which runs between RestoreExactOld and DiscardTransaction in the
// gate) leaves the marker on disk → next Detect ManualRepair. The old single
// Rollback deleted the marker before End could fail.

// TestSplitRollback_RestoreExactOld_PreservesMarker proves the marker + backup
// survive RestoreExactOld (phase 1). The capability is NOT killed, so
// DiscardTransaction (phase 2) can still be called.
func TestSplitRollback_RestoreExactOld_PreservesMarker(t *testing.T) {
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"secret","remoteSecurityVersion":0}`
	writeSettings(t, dir, original)
	origBytes, _ := os.ReadFile(filepath.Join(dir, "settings.json"))

	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	txnDir := filepath.Join(dir, migrationTxnDirName)

	// Phase 1: restore settings.json from backup WITHOUT removing any artifacts.
	if err := m.RestoreExactOld(); err != nil {
		t.Fatalf("RestoreExactOld: %v", err)
	}
	// settings.json restored to exact original.
	current, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(current) != string(origBytes) {
		t.Fatalf("after RestoreExactOld settings != original\n got: %s\nwant: %s", current, origBytes)
	}
	// Marker + backup + txn dir STILL PRESENT (the R2-Major-02 guarantee).
	if _, err := os.Lstat(filepath.Join(txnDir, markerFileName)); err != nil {
		t.Fatal("marker MUST survive RestoreExactOld (End has not run yet)")
	}
	if _, err := os.Lstat(filepath.Join(txnDir, backupFileName)); err != nil {
		t.Fatal("backup MUST survive RestoreExactOld")
	}
	if _, err := os.Lstat(txnDir); err != nil {
		t.Fatal("txn dir MUST survive RestoreExactOld")
	}
	// Capability NOT killed: DiscardTransaction is still callable.
	if m.closed {
		t.Fatal("RestoreExactOld must NOT kill the capability (DiscardTransaction must remain callable)")
	}
}

// TestSplitRollback_DiscardTransaction_RemovesArtifacts_KillsCapability proves
// phase 2 removes the marker/backup/txn dir and kills the capability. It is the
// ONLY step that removes the marker, so calling it ONLY after End succeeds is
// what makes the gate's marker ordering correct.
func TestSplitRollback_DiscardTransaction_RemovesArtifacts_KillsCapability(t *testing.T) {
	dir := t.TempDir()
	original := `{"remoteHost":"127.0.0.1","remotePort":8680,"remoteEnabled":false,"remoteSecurityVersion":0}`
	writeSettings(t, dir, original)
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := m.RestoreExactOld(); err != nil {
		t.Fatalf("RestoreExactOld: %v", err)
	}
	// Phase 2: now End has (hypothetically) succeeded → safe to discard.
	if err := m.DiscardTransaction(); err != nil {
		t.Fatalf("DiscardTransaction: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir must be REMOVED by DiscardTransaction")
	}
	// Capability killed: further calls return ErrMigrationClosed.
	if err := m.DiscardTransaction(); err != ErrMigrationClosed {
		t.Fatalf("DiscardTransaction after close: %v, want ErrMigrationClosed", err)
	}
	if err := m.RestoreExactOld(); err != ErrMigrationClosed {
		t.Fatalf("RestoreExactOld after close: %v, want ErrMigrationClosed", err)
	}
}

// TestSplitRollback_RestoreExactOld_PreCommit_Noop proves RestoreExactOld is a
// no-op when Stage failed before writing a backup (pre-commit): settings.json is
// untouched and the marker (if any) is preserved for the caller's decision.
func TestSplitRollback_RestoreExactOld_PreCommit_Noop(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"remoteSecurityVersion":0}`)
	origBytes, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// No Stage → no backup bytes. RestoreExactOld is a no-op.
	if err := m.RestoreExactOld(); err != nil {
		t.Fatalf("RestoreExactOld pre-commit: %v", err)
	}
	current, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(current) != string(origBytes) {
		t.Fatal("pre-commit RestoreExactOld must not touch settings.json")
	}
	if m.closed {
		t.Fatal("pre-commit RestoreExactOld must not kill the capability")
	}
}

// TestSplitRollback_ComposedRollback_MatchesOldBehavior proves Rollback() (the
// B3c-compat composition) still restores settings, removes all artifacts, and
// kills the capability — identical to the pre-split behavior.
func TestSplitRollback_ComposedRollback_MatchesOldBehavior(t *testing.T) {
	dir := t.TempDir()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"secret","remoteSecurityVersion":0}`
	writeSettings(t, dir, original)
	origBytes, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	current, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(current) != string(origBytes) {
		t.Fatalf("composed Rollback settings != original\n got: %s\nwant: %s", current, origBytes)
	}
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir must be REMOVED by composed Rollback")
	}
	if err := m.Rollback(); err != ErrMigrationClosed {
		t.Fatalf("Rollback after close: %v, want ErrMigrationClosed", err)
	}
}

// --- R3-Major-02: composed Rollback() failure lifecycle ---
//
// Rollback() must kill the capability on EVERY return path (the pre-split
// contract had `defer killCapabilityLocked`). RestoreExactOld intentionally
// does NOT kill (the gate relies on it staying live across End), so the
// composition must restore the terminal contract itself.

// rollbackFailCommon stages+commits a migration so Rollback has a real backup to
// restore from, returning the capability and original bytes.
func rollbackFailCommon(t *testing.T, dir string) (*Migration, []byte) {
	t.Helper()
	original := `{"remoteHost":"0.0.0.0","remotePort":9999,"remoteEnabled":true,"remoteToken":"secret","remoteSecurityVersion":0}`
	writeSettings(t, dir, original)
	origBytes, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	store := NewRemoteMigrationStore(dir)
	m, err := store.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := m.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return m, origBytes
}

// TestRollback_RestoreFails_KillsCapability_PreservesEvidence proves a
// RestoreExactOld failure (here: configDir made read-only so CreateTemp fails)
// still kills the capability (next op → ErrMigrationClosed) and preserves the
// marker/backup/txn evidence (next Detect → ManualRepair).
func TestRollback_RestoreFails_KillsCapability_PreservesEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX chmod-based fault injection")
	}
	dir := t.TempDir()
	m, _ := rollbackFailCommon(t, dir)
	txnDir := filepath.Join(dir, migrationTxnDirName)

	// Make configDir read-only so RestoreExactOld's CreateTemp fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir readonly: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := m.Rollback(); err == nil {
		t.Fatal("Rollback must fail when the restore fails (CreateTemp EACCES)")
	}
	// Capability killed: every subsequent op returns ErrMigrationClosed.
	if err := m.RestoreExactOld(); err != ErrMigrationClosed {
		t.Fatalf("RestoreExactOld after failed Rollback: %v, want ErrMigrationClosed", err)
	}
	if err := m.DiscardTransaction(); err != ErrMigrationClosed {
		t.Fatalf("DiscardTransaction after failed Rollback: %v, want ErrMigrationClosed", err)
	}
	if err := m.Abort(); err != ErrMigrationClosed {
		t.Fatalf("Abort after failed Rollback: %v, want ErrMigrationClosed", err)
	}
	// Evidence preserved (restore failed before touching disk): marker/backup/txn
	// remain → next Detect ManualRepair.
	if _, err := os.Lstat(filepath.Join(txnDir, markerFileName)); err != nil {
		t.Fatal("marker must be PRESERVED when Rollback's restore fails")
	}
	store := NewRemoteMigrationStore(dir)
	det, err := store.Detect()
	if err != nil || det.State != DetectionManualRepair {
		t.Fatalf("Detect=(%v,%v) want ManualRepair after restore-failure Rollback", det.State, err)
	}
}

// TestRollback_DiscardCleanupFails_KillsCapability_ManualRepair proves a
// DiscardTransaction cleanup failure (an extra file blocks os.Remove(txnDir))
// kills the capability and leaves the txn dir → ManualRepair.
func TestRollback_DiscardCleanupFails_KillsCapability_ManualRepair(t *testing.T) {
	dir := t.TempDir()
	m, _ := rollbackFailCommon(t, dir)
	txnDir := filepath.Join(dir, migrationTxnDirName)

	// Plant an extra file in the txn dir so removeTxnArtifacts' os.Remove(txnDir)
	// returns ENOTEMPTY.
	if err := os.WriteFile(filepath.Join(txnDir, "leftover"), []byte("x"), 0o600); err != nil {
		t.Fatalf("plant leftover: %v", err)
	}

	if err := m.Rollback(); err == nil {
		t.Fatal("Rollback must fail when DiscardTransaction cleanup fails (ENOTEMPTY)")
	}
	if err := m.Rollback(); err != ErrMigrationClosed {
		t.Fatalf("Rollback after failed Discard: %v, want ErrMigrationClosed", err)
	}
	// settings.json WAS restored (RestoreExactOld succeeded before Discard failed).
	// But the txn dir remains (with the leftover) → ManualRepair.
	if _, err := os.Lstat(txnDir); err != nil {
		t.Fatal("txn dir must REMAIN when DiscardTransaction cleanup fails")
	}
	store := NewRemoteMigrationStore(dir)
	det, err := store.Detect()
	if err != nil || det.State != DetectionManualRepair {
		t.Fatalf("Detect=(%v,%v) want ManualRepair after cleanup-failure Rollback", det.State, err)
	}
}

// TestRollback_DiscardSyncFails_KillsCapability proves a DiscardTransaction
// parent-sync failure (injected via the parentSync seam) kills the capability.
// removeTxnArtifacts already succeeded (txn dir gone), so next Detect classifies
// by settings.json — restored to exact-old v0 → NeedsMigration (re-migratable).
func TestRollback_DiscardSyncFails_KillsCapability(t *testing.T) {
	dir := t.TempDir()
	m, _ := rollbackFailCommon(t, dir)

	// Inject parentSync failure in DiscardTransaction (runs AFTER removeTxnArtifacts).
	m.seams.parentSync = func(string) error { return errors.New("injected sync failure") }

	if err := m.Rollback(); err == nil {
		t.Fatal("Rollback must fail when DiscardTransaction sync fails")
	}
	if err := m.DiscardTransaction(); err != ErrMigrationClosed {
		t.Fatalf("DiscardTransaction after failed sync: %v, want ErrMigrationClosed", err)
	}
	// removeTxnArtifacts succeeded before sync failed → txn dir GONE → Detect
	// classifies by settings.json (restored exact-old v0) → NeedsMigration.
	if _, err := os.Lstat(filepath.Join(dir, migrationTxnDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("txn dir must be GONE (removeTxnArtifacts succeeded before sync fail)")
	}
	store := NewRemoteMigrationStore(dir)
	det, err := store.Detect()
	if err != nil {
		t.Fatalf("Detect err: %v", err)
	}
	if det.State != DetectionNeedsMigration {
		t.Fatalf("Detect=%v want NeedsMigration after sync-failure Rollback (txn gone, v0 restored)", det.State)
	}
}
