package main

// remote_security_migration_gate_test.go — M1-B3c startup orchestration tests
// (design §C.3/§C.4, task spec cover matrix). These tests exercise the App-layer
// gate against a REAL security-enabled Remote constructed exactly as NewApp does
// (NewServerWithSecurity + NewProductionSecurityOptions over the test configDir),
// plus the REAL settings.RemoteSecurityMigrationStore on raw bytes. No internal
// package seams are used from main; Stage/Commit/Validate/End/Cleanup fault
// injection that requires unexported seams is covered by the settings (B3b) and
// remote (B3a) package tests, documented per case below.

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
)

// canaryToken is the privacy canary planted in legacy remoteToken fields. It
// must NEVER appear in the post-migration settings.json, device store, device
// backup, WAL, warnings, or any error. Scanning for it is the privacy proof.
const canaryToken = "CANARY_PRIVACY_a4f9c2e1b8d76053"

// newMigrationGateApp builds an App whose Remote is a REAL security-enabled
// server (NewServerWithSecurity + production options) rooted at a temp configDir,
// mirroring NewApp's construction. The App.configDir field is set so the gate
// shares the directory with the Remote's device store, durable sink and the raw
// settings.json operated on by the migration store.
func newMigrationGateApp(t *testing.T) (*App, string) {
	t.Helper()
	configDir := t.TempDir()
	logSvc := logging.NewService(configDir)
	t.Cleanup(logSvc.Close)

	app := &App{
		configDir: configDir,
		Log:       logSvc,
		Settings:  settings.NewService(configDir),
	}
	opts := remote.NewProductionSecurityOptions(configDir, func() (contract.HostSummary, error) {
		return validHostSummaryMain(), nil
	})
	srv := remote.NewServerWithSecurity(0, app, logSvc, embed.FS{}, opts)
	srv.SetHost("127.0.0.1")
	app.Remote = srv
	return app, configDir
}

// writeSettings writes raw JSON bytes to settings.json in configDir.
func writeSettings(t *testing.T, configDir string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), raw, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

// readSettingsRaw reads settings.json bytes (fails the test if absent).
func readSettingsRaw(t *testing.T, configDir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	return b
}

// settingsExists reports whether settings.json exists.
func settingsExists(configDir string) bool {
	_, err := os.Stat(filepath.Join(configDir, "settings.json"))
	return err == nil
}

// txnDirExists reports whether the migration transaction dir exists.
func txnDirExists(configDir string) bool {
	_, err := os.Stat(filepath.Join(configDir, ".remote-security-migration"))
	return err == nil
}

// containsBytesGlob reports whether any file under root (recursive) contains
// needle. Used for the privacy scan over the device store + backup tree.
func containsBytesGlob(t *testing.T, root, needle string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, rerr := os.ReadFile(path); rerr == nil && bytes.Contains(b, []byte(needle)) {
			found = true
		}
		return nil
	})
	return found
}

// hasWarning reports whether any startup warning contains substr.
func (a *App) hasWarning(substr string) bool {
	for _, w := range a.GetStartupWarnings() {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// makeTxnMarker writes the migration txn dir with a marker file of the given
// state string (empty state writes no marker → orphan txn dir).
func makeTxnMarker(t *testing.T, configDir, state string) {
	t.Helper()
	txnDir := filepath.Join(configDir, ".remote-security-migration")
	if err := os.MkdirAll(txnDir, 0o700); err != nil {
		t.Fatalf("mkdir txn dir: %v", err)
	}
	if state == "" {
		return
	}
	var marker []byte
	if state == "<corrupt>" {
		marker = []byte("not-json-at-all")
	} else {
		b, _ := json.Marshal(map[string]any{"state": state, "version": 1})
		marker = append(b, '\n')
	}
	if err := os.WriteFile(filepath.Join(txnDir, "marker"), marker, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 1. MissingNewInstall
// ---------------------------------------------------------------------------

func TestMigrationGate_MissingNewInstall(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	if settingsExists(configDir) {
		t.Fatal("precondition: settings.json must be absent")
	}

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || !startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,true)", secLoaded, startAllowed)
	}
	// No migration artifacts created; settings.json still absent (no eager write).
	if settingsExists(configDir) {
		t.Fatal("MissingNewInstall must not eagerly write settings.json")
	}
	if txnDirExists(configDir) {
		t.Fatal("no txn dir should be created for a new install")
	}
	// Normal path performs the unique LoadSecurityState (exactly once).
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, false)
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1", app.securityLoadAttempts)
	}
}

// ---------------------------------------------------------------------------
// 2. Current (version 1)
// ---------------------------------------------------------------------------

func TestMigrationGate_Current(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	orig := []byte(`{"remoteSecurityVersion":1,"remoteEnabled":false,"remoteHost":"127.0.0.1","remotePort":8680}` + "\n")
	writeSettings(t, configDir, orig)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || !startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,true)", secLoaded, startAllowed)
	}
	// Settings untouched.
	if got := readSettingsRaw(t, configDir); !bytes.Equal(got, orig) {
		t.Fatalf("Current settings mutated:\n got=%s\nwant=%s", got, orig)
	}
	if txnDirExists(configDir) {
		t.Fatal("no txn dir should be created for current settings")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, false)
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1", app.securityLoadAttempts)
	}
}

// ---------------------------------------------------------------------------
// 3. FutureOrInvalid — version 2 and non-integer "1"
// ---------------------------------------------------------------------------

func TestMigrationGate_FutureOrInvalid_Version2(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	orig := []byte(`{"remoteSecurityVersion":2,"remoteEnabled":true}` + "\n")
	writeSettings(t, configDir, orig)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("未知或无效的设置版本") {
		t.Fatal("expected future/invalid warning")
	}
	if got := readSettingsRaw(t, configDir); !bytes.Equal(got, orig) {
		t.Fatal("FutureOrInvalid must not mutate settings")
	}
	if app.securityLoadAttempts != 0 {
		t.Fatalf("securityLoadAttempts=%d want 0 (skip LoadSecurityState)", app.securityLoadAttempts)
	}
}

func TestMigrationGate_FutureOrInvalid_NonInteger(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	// version "1" (string) → NonNumber → FutureOrInvalid.
	orig := []byte(`{"remoteSecurityVersion":"1","remoteEnabled":true}` + "\n")
	writeSettings(t, configDir, orig)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("未知或无效的设置版本") {
		t.Fatal("expected future/invalid warning for non-integer version")
	}
	if got := readSettingsRaw(t, configDir); !bytes.Equal(got, orig) {
		t.Fatal("non-integer version must not mutate settings")
	}
	if app.securityLoadAttempts != 0 {
		t.Fatalf("securityLoadAttempts=%d want 0", app.securityLoadAttempts)
	}
}

// ---------------------------------------------------------------------------
// 4. ManualRepair — prepared / settings_committed / corrupt / orphan txn dir
// ---------------------------------------------------------------------------

func TestMigrationGate_ManualRepair_PreparedMarker(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":1}`+"\n"))
	makeTxnMarker(t, configDir, "prepared")

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("手动修复") {
		t.Fatal("expected manual-repair warning")
	}
	if app.securityLoadAttempts != 0 {
		t.Fatalf("securityLoadAttempts=%d want 0", app.securityLoadAttempts)
	}
}

func TestMigrationGate_ManualRepair_SettingsCommittedMarker(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":1}`+"\n"))
	makeTxnMarker(t, configDir, "settings_committed")

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("手动修复") {
		t.Fatal("expected manual-repair warning for settings_committed marker")
	}
}

func TestMigrationGate_ManualRepair_CorruptMarker(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":1}`+"\n"))
	makeTxnMarker(t, configDir, "<corrupt>")

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("手动修复") {
		t.Fatal("expected manual-repair warning for corrupt marker")
	}
}

func TestMigrationGate_ManualRepair_OrphanTxnDir(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":1}`+"\n"))
	makeTxnMarker(t, configDir, "") // txn dir, no marker

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("手动修复") {
		t.Fatal("expected manual-repair warning for orphan txn dir")
	}
}

// ---------------------------------------------------------------------------
// 5. Happy-path v0 migration end-to-end (custom tuple + token canary)
// ---------------------------------------------------------------------------

func TestMigrationGate_HappyPath_CustomTuplePreserved(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	// v0: no remoteSecurityVersion. Custom host/port/enabled, a canary token,
	// and an unknown future field that must be preserved.
	orig := []byte(`{` +
		`"remoteToken":"` + canaryToken + `",` +
		`"remoteEnabled":true,` +
		`"remoteHost":"192.168.1.55",` +
		`"remotePort":9999,` +
		`"unknownFutureField":"preserve-me",` +
		`"dashboardMode":"embedded"` +
		`}` + "\n")
	writeSettings(t, configDir, orig)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if !secLoaded || !startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,true)", secLoaded, startAllowed)
	}
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1 (gate owned the load)", app.securityLoadAttempts)
	}

	// settings.json is now v1 and parseable.
	got := readSettingsRaw(t, configDir)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("post-migration settings not parseable: %v\n%s", err, got)
	}
	// version == 1 (integer).
	v, ok := m["remoteSecurityVersion"]
	if !ok {
		t.Fatal("missing remoteSecurityVersion")
	}
	if strings.TrimSpace(string(v)) != "1" {
		t.Fatalf("remoteSecurityVersion=%s want 1", v)
	}
	// Token GONE (scan raw bytes).
	if bytes.Contains(got, []byte(canaryToken)) {
		t.Fatal("canary token still present in settings.json bytes")
	}
	if _, ok := m["remoteToken"]; ok {
		t.Fatal("remoteToken key still present")
	}
	// Tuple preserved exactly.
	if string(m["remoteEnabled"]) != "true" {
		t.Fatalf("remoteEnabled=%s want true", m["remoteEnabled"])
	}
	if string(m["remoteHost"]) != `"192.168.1.55"` {
		t.Fatalf("remoteHost=%s want \"192.168.1.55\"", m["remoteHost"])
	}
	if string(m["remotePort"]) != "9999" {
		t.Fatalf("remotePort=%s want 9999", m["remotePort"])
	}
	// Unknown field preserved.
	if string(m["unknownFutureField"]) != `"preserve-me"` {
		t.Fatalf("unknownFutureField=%s want \"preserve-me\"", m["unknownFutureField"])
	}

	// Settings.Service reads the v1 file with the preserved tuple.
	if err := app.Settings.Load(); err != nil {
		t.Fatalf("Settings.Load after migration: %v", err)
	}
	if app.Settings.GetRemoteHost() != "192.168.1.55" {
		t.Fatalf("GetRemoteHost=%q want 192.168.1.55", app.Settings.GetRemoteHost())
	}
	if app.Settings.GetRemotePort() != 9999 {
		t.Fatalf("GetRemotePort=%d want 9999", app.Settings.GetRemotePort())
	}
	if !app.Settings.GetRemoteEnabled() {
		t.Fatal("GetRemoteEnabled=false want true")
	}

	// All migration artifacts cleaned: txn dir gone, device backup gone.
	if txnDirExists(configDir) {
		t.Fatal("txn dir must be removed on successful migration")
	}
	backupRoot := filepath.Join(configDir, "device-store-backups")
	if entries, err := os.ReadDir(backupRoot); err == nil && len(entries) != 0 {
		t.Fatalf("device backup dir must be empty after cleanup, got %d entries", len(entries))
	}

	// Device store is healthy after End (migration returned true,true ⇒ Validate+End
	// succeeded). Re-confirm via the security health snapshot.
	h := app.Remote.GetSecurityHealth()
	if !h.SecurityReady {
		t.Fatal("device store not ready after successful migration")
	}

	// Privacy: canary absent from the entire configDir tree (store, WAL, backups,
	// durable event log) and from warnings. The only allowed exception would be a
	// retained settings rollback backup, which successful Finish removes.
	if containsBytesGlob(t, configDir, canaryToken) {
		t.Fatal("canary token leaked somewhere under configDir")
	}
	for _, w := range app.GetStartupWarnings() {
		if strings.Contains(w, canaryToken) {
			t.Fatalf("canary token leaked into startup warning: %q", w)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. Happy path with enabled=false — (true,true), Start decision to caller
// ---------------------------------------------------------------------------

func TestMigrationGate_HappyPath_EnabledFalse(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	orig := []byte(`{"remoteToken":"` + canaryToken + `","remoteEnabled":false,"remoteHost":"127.0.0.1","remotePort":8680}` + "\n")
	writeSettings(t, configDir, orig)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if !secLoaded || !startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,true)", secLoaded, startAllowed)
	}
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1", app.securityLoadAttempts)
	}
	// Token removed even when remote was disabled.
	got := readSettingsRaw(t, configDir)
	if bytes.Contains(got, []byte(canaryToken)) {
		t.Fatal("canary token must be removed even when remoteEnabled=false")
	}
	// Start decision left to caller: remoteEnabled=false ⇒ no Start.
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, false)
	if app.Remote.IsRunning() {
		t.Fatal("remote must not start when remoteEnabled=false")
	}
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1 (no extra load)", app.securityLoadAttempts)
	}
}

// ---------------------------------------------------------------------------
// 7. Happy path actually Starts (end-to-end load→start on loopback)
// ---------------------------------------------------------------------------

func TestMigrationGate_HappyPath_StartAllowed(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	// loopback/default tuple + enabled=true so Start binds an ephemeral port.
	orig := []byte(`{"remoteToken":"` + canaryToken + `","remoteEnabled":true,"remoteHost":"127.0.0.1","remotePort":8680}` + "\n")
	writeSettings(t, configDir, orig)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if !secLoaded || !startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,true)", secLoaded, startAllowed)
	}
	// Sync host/port from the migrated settings exactly as Startup does, then Start.
	if err := app.Settings.Load(); err != nil {
		t.Fatalf("Settings.Load: %v", err)
	}
	app.Remote.SetHost(app.Settings.GetRemoteHost())
	// Leave port 0 for an ephemeral bind to avoid CI port conflicts.
	t.Cleanup(app.Remote.Stop)

	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if !app.Remote.IsRunning() {
		t.Fatal("remote should be running after happy-path gate + enabled=true")
	}
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1 (gate owned load; normal path skipped)", app.securityLoadAttempts)
	}
}

// ---------------------------------------------------------------------------
// 8. LoadSecurityState failure (step a) — no mutation, (true,false)
// ---------------------------------------------------------------------------

func TestMigrationGate_LoadSecurityStateFails_NoMutation(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	// v0 settings with the canary → Detect returns NeedsMigration.
	orig := []byte(`{"remoteToken":"` + canaryToken + `","remoteEnabled":true}` + "\n")
	writeSettings(t, configDir, orig)
	// Corrupt the device store so LoadSecurityState fails: a snapshot file with
	// no ledger is an unrecoverable single-file-missing schema latch.
	if err := os.WriteFile(filepath.Join(configDir, "devices.json"), []byte("{not valid snapshot"), 0o600); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false)", secLoaded, startAllowed)
	}
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1 (gate attempted the load)", app.securityLoadAttempts)
	}
	if !app.hasWarning("安全状态加载失败") {
		t.Fatal("expected security-load-failed warning")
	}
	// No mutation: settings.json still carries the v0 document + canary token.
	got := readSettingsRaw(t, configDir)
	if !bytes.Equal(got, orig) {
		t.Fatalf("settings mutated on LoadSecurityState failure:\n got=%s\nwant=%s", got, orig)
	}
	// No Start.
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote must not start when LoadSecurityState failed")
	}
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1 (normal path must not retry)", app.securityLoadAttempts)
	}
}

// ---------------------------------------------------------------------------
// 9. Detect error path — fixed warning, (false,false)
//    settings.json present as a DIRECTORY yields a non-ErrNotExist read error,
//    exercising the gate's Detect-error branch without package-internal seams.
// ---------------------------------------------------------------------------

func TestMigrationGate_DetectError(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	// Replace settings.json with a directory so Detect's ReadFile returns a
	// non-ErrNotExist error (EISDIR) rather than classifying the file.
	settingsPath := filepath.Join(configDir, "settings.json")
	if err := os.Mkdir(settingsPath, 0o700); err != nil {
		t.Fatalf("mkdir settings-as-dir: %v", err)
	}

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("检测阶段失败") {
		t.Fatal("expected detect-failed warning")
	}
	if app.securityLoadAttempts != 0 {
		t.Fatalf("securityLoadAttempts=%d want 0", app.securityLoadAttempts)
	}
}

// ---------------------------------------------------------------------------
// 10. Exactly-once summary across representative paths.
// ---------------------------------------------------------------------------

func TestMigrationGate_LoadSecurityStateExactlyOnce(t *testing.T) {
	cases := []struct {
		name             string
		setup            func(t *testing.T, app *App, configDir string)
		wantSecLoaded    bool
		wantStartAllowed bool
		// expected securityLoadAttempts AFTER gate only (before applyRemoteGateResult)
		wantAttemptsAfterGate int
		// remoteEnabled passed to applyRemoteGateResult
		remoteEnabled bool
		// expected securityLoadAttempts AFTER applyRemoteGateResult
		wantAttemptsAfterApply int
	}{
		{
			name:                   "MissingNewInstall",
			setup:                  func(t *testing.T, app *App, configDir string) {},
			wantSecLoaded:          false,
			wantStartAllowed:       true,
			wantAttemptsAfterGate:  0,
			remoteEnabled:          false,
			wantAttemptsAfterApply: 1,
		},
		{
			name: "Current",
			setup: func(t *testing.T, app *App, configDir string) {
				writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":1}`+"\n"))
			},
			wantSecLoaded:          false,
			wantStartAllowed:       true,
			wantAttemptsAfterGate:  0,
			remoteEnabled:          false,
			wantAttemptsAfterApply: 1,
		},
		{
			name: "FutureOrInvalid",
			setup: func(t *testing.T, app *App, configDir string) {
				writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":2}`+"\n"))
			},
			wantSecLoaded:          false,
			wantStartAllowed:       false,
			wantAttemptsAfterGate:  0,
			remoteEnabled:          true,
			wantAttemptsAfterApply: 0,
		},
		{
			name: "ManualRepair",
			setup: func(t *testing.T, app *App, configDir string) {
				writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":1}`+"\n"))
				makeTxnMarker(t, configDir, "prepared")
			},
			wantSecLoaded:          false,
			wantStartAllowed:       false,
			wantAttemptsAfterGate:  0,
			remoteEnabled:          true,
			wantAttemptsAfterApply: 0,
		},
		{
			name: "NeedsMigrationSuccess",
			setup: func(t *testing.T, app *App, configDir string) {
				writeSettings(t, configDir, []byte(`{"remoteToken":"`+canaryToken+`","remoteEnabled":false}`+"\n"))
			},
			wantSecLoaded:          true,
			wantStartAllowed:       true,
			wantAttemptsAfterGate:  1,
			remoteEnabled:          false,
			wantAttemptsAfterApply: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app, configDir := newMigrationGateApp(t)
			c.setup(t, app, configDir)
			secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
			if secLoaded != c.wantSecLoaded || startAllowed != c.wantStartAllowed {
				t.Fatalf("gate=(%v,%v) want (%v,%v)", secLoaded, startAllowed, c.wantSecLoaded, c.wantStartAllowed)
			}
			if app.securityLoadAttempts != c.wantAttemptsAfterGate {
				t.Fatalf("after gate: securityLoadAttempts=%d want %d", app.securityLoadAttempts, c.wantAttemptsAfterGate)
			}
			app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, c.remoteEnabled)
			if app.securityLoadAttempts != c.wantAttemptsAfterApply {
				t.Fatalf("after apply: securityLoadAttempts=%d want %d", app.securityLoadAttempts, c.wantAttemptsAfterApply)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 11. Begin-refused (pre-existing txn dir) is surfaced as ManualRepair by Detect
//     before Begin is ever called. The settings package's own Begin test covers
//     the Begin-internal refusal; here we assert the App gate never reaches Begin
//     when a txn dir is present.
// ---------------------------------------------------------------------------

func TestMigrationGate_BeginRefused_PreExistingTxnDir(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	writeSettings(t, configDir, []byte(`{"remoteToken":"`+canaryToken+`","remoteEnabled":true}`+"\n"))
	makeTxnMarker(t, configDir, "prepared")

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	// ManualRepair: no load, no start, fixed warning.
	if secLoaded || startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,false)", secLoaded, startAllowed)
	}
	if app.securityLoadAttempts != 0 {
		t.Fatalf("securityLoadAttempts=%d want 0 (Begin never reached)", app.securityLoadAttempts)
	}
	if !app.hasWarning("手动修复") {
		t.Fatal("expected manual-repair warning")
	}
}

// NOTE on Stage/Commit/Validate/End/Cleanup fault coverage:
// The settings.RemoteSecurityMigrationStore Stage/Commit/Rollback/Finish/Abort
// fault paths (rename old/new/other, candidate invariant, marker rewrite) are
// covered by internal/settings/remote_security_migration_test.go (B3b) via the
// package-private migrationSeams. The remote device-store maintenance backup/
// restore/validate/end/abort fault paths are covered by
// internal/remote/device_store_backup_hardening_test.go and security_core_test.go
// (B3a) via the package-private maintenance* seams. From the main package those
// seams are unexported and cannot be injected; the App gate therefore only
// exercises the fault paths reachable via real on-disk state (Future/Manual/
// Detect-error/LoadSecurityState-fail above) and the success path. The gate's
// branching for every Stage/Commit/Validate/End/Finish/Cleanup error is
// structural and covered by code review against the design state machine; the
// underlying primitives' fault behaviour is proven in their own packages.

// ---------------------------------------------------------------------------
// Major-01: LoadSecurityState failure on the Current/Missing path MUST prevent
// Start (the device store is fail-closed, so no listener may be published).
// ---------------------------------------------------------------------------

// corruptDeviceStore writes an unrecoverable single-file-missing snapshot that
// makes LoadSecurityState latch (same technique as the NeedsMigration load-fail
// test above).
func corruptDeviceStore(t *testing.T, configDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(configDir, "devices.json"), []byte("{not valid snapshot"), 0o600); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}
}

// TestApplyGateResult_Current_CorruptStore_NoStart: Current settings + corrupt
// device store + remoteEnabled=true. The normal path calls LoadSecurityState,
// it fails, and applyRemoteGateResult MUST latch allowStart=false so NO listener
// is published, with a fixed warning and exactly-once load.
func TestApplyGateResult_Current_CorruptStore_NoStart(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	writeSettings(t, configDir, []byte(`{"remoteSecurityVersion":1,"remoteEnabled":true,"remoteHost":"127.0.0.1","remotePort":8680}`+"\n"))
	corruptDeviceStore(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || !startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,true) for Current", secLoaded, startAllowed)
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1 (exactly-once load attempted)", app.securityLoadAttempts)
	}
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start when LoadSecurityState failed on the Current path")
	}
	if !app.hasWarning("安全状态加载失败") {
		t.Fatal("expected the fixed security-load-failed startup warning")
	}
}

// TestApplyGateResult_Missing_CorruptStore_NoStart: Missing settings + corrupt
// device store + remoteEnabled=true. Same fail-closed guarantee as Current.
func TestApplyGateResult_Missing_CorruptStore_NoStart(t *testing.T) {
	app, configDir := newMigrationGateApp(t)
	if settingsExists(configDir) {
		t.Fatal("precondition: settings.json must be absent for Missing")
	}
	corruptDeviceStore(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded || !startAllowed {
		t.Fatalf("gate=(%v,%v) want (false,true) for Missing", secLoaded, startAllowed)
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1", app.securityLoadAttempts)
	}
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start when LoadSecurityState failed on the Missing path")
	}
	if !app.hasWarning("安全状态加载失败") {
		t.Fatal("expected the fixed security-load-failed startup warning")
	}
}

// ---------------------------------------------------------------------------
// Major-02: App-layer rollback order + fault injection + Finish-fault → no Start.
// The migrationFaultInjector seam (nil in production) lets these main-package
// tests prove the gate orchestration that the package-private settings/remote
// seams cannot reach from main.
// ---------------------------------------------------------------------------

// oversizeV0Settings writes a valid v0 settings document larger than the 1 MiB
// candidate cap so that Stage fails (readback ok, size check fails) and the gate
// runs the recoverable rollback. The canary token lets us assert evidence state.
func oversizeV0Settings(t *testing.T, configDir string) {
	t.Helper()
	// 1.1 MiB of padding inside a valid JSON string value → total > 1<<20.
	pad := strings.Repeat("x", 1<<20+100_000)
	raw := `{"remoteToken":"` + canaryToken + `","remoteEnabled":true,"remoteHost":"127.0.0.1","remotePort":8680,"pad":"` + pad + `"}` + "\n"
	writeSettings(t, configDir, []byte(raw))
}

// deviceBackupDirEmpty reports whether the device-store-backups dir under
// configDir has no entries (i.e. cleanup ran).
func deviceBackupDirEmpty(configDir string) bool {
	entries, err := os.ReadDir(filepath.Join(configDir, "device-store-backups"))
	if err != nil {
		return true // dir absent → trivially empty
	}
	return len(entries) == 0
}

// TestMigrationGate_RollbackSuccess_OrderAndCleanup: Stage fails (oversize
// settings) and the rollback runs every step in the authority order. The
// injector only OBSERVES (records step names, returns nil), so the real
// rollback completes: evidence cleaned, no Start, stage-failed warning.
func TestMigrationGate_RollbackSuccess_OrderAndCleanup(t *testing.T) {
	var order []string
	migrationFaultInjector = func(step string) error {
		order = append(order, step)
		return nil
	}
	t.Cleanup(func() { migrationFaultInjector = nil })

	app, configDir := newMigrationGateApp(t)
	oversizeV0Settings(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false): gate owned the load, Start forbidden after rollback", secLoaded, startAllowed)
	}
	wantOrder := []string{
		"rollback_device_restore",
		"rollback_validate",
		"rollback_settings_restore",
		"rollback_end",
		"rollback_settings_discard",
		"rollback_cleanup",
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("rollback step order=%v want %v", order, wantOrder)
	}
	for i, s := range wantOrder {
		if order[i] != s {
			t.Fatalf("rollback step %d=%q want %q (full order=%v)", i, order[i], s, order)
		}
	}
	if app.securityLoadAttempts != 1 {
		t.Fatalf("securityLoadAttempts=%d want 1", app.securityLoadAttempts)
	}
	if !app.hasWarning("设置暂存失败并已回滚") {
		t.Fatal("expected the stage-failed (rollback succeeded) warning")
	}
	if app.hasWarning("不确定状态") {
		t.Fatal("must NOT record the indeterminate warning when rollback succeeds")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start after a Stage failure even on successful rollback")
	}
	// Rollback succeeded → all evidence cleaned.
	if txnDirExists(configDir) {
		t.Fatal("txn dir must be removed on successful rollback")
	}
	if !deviceBackupDirEmpty(configDir) {
		t.Fatal("device backup must be cleaned on successful rollback")
	}
}

// TestMigrationGate_RollbackValidateFails_AbortPreserveEvidence: Stage fails;
// the injector fails the rollback at the Validate step. The gate MUST Abort,
// preserve the device backup + settings marker, forbid Start, and record the
// indeterminate (manual-repair) warning. Steps after Validate never run.
func TestMigrationGate_RollbackValidateFails_AbortPreserveEvidence(t *testing.T) {
	var order []string
	migrationFaultInjector = func(step string) error {
		order = append(order, step)
		if step == "rollback_validate" {
			return errors.New("injected validate failure")
		}
		return nil
	}
	t.Cleanup(func() { migrationFaultInjector = nil })

	app, configDir := newMigrationGateApp(t)
	oversizeV0Settings(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false)", secLoaded, startAllowed)
	}
	// restore ran, validate failed → later steps (settings/end/cleanup) never reached.
	wantPrefix := []string{"rollback_device_restore", "rollback_validate"}
	if len(order) != len(wantPrefix) {
		t.Fatalf("rollback step order=%v want exactly %v (later steps must not run)", order, wantPrefix)
	}
	for i, s := range wantPrefix {
		if order[i] != s {
			t.Fatalf("rollback step %d=%q want %q", i, order[i], s)
		}
	}
	if !app.hasWarning("不确定状态") {
		t.Fatal("expected the indeterminate (manual-repair) warning on rollback step failure")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start after an indeterminate rollback")
	}
	// Evidence preserved: settings txn dir (marker) + device backup retained.
	if !txnDirExists(configDir) {
		t.Fatal("settings txn dir must be PRESERVED (marker evidence) on rollback step failure")
	}
	if deviceBackupDirEmpty(configDir) {
		t.Fatal("device backup must be PRESERVED on rollback step failure (no cleanup)")
	}
}

// TestMigrationGate_FinishFault_NoStart_EvidencePreserved: the migration commits
// successfully, but Finish is short-circuited by the injector. The gate MUST
// forbid Start, record the finish-failed warning, and PRESERVE the token-bearing
// settings backup (repairable evidence for the next-boot ManualRepair). This
// directly proves the Major-02 concern: a Finish failure never allows Start while
// leaving the sensitive backup.
func TestMigrationGate_FinishFault_NoStart_EvidencePreserved(t *testing.T) {
	migrationFaultInjector = func(step string) error {
		if step == "finish" {
			return errors.New("injected finish failure")
		}
		return nil
	}
	t.Cleanup(func() { migrationFaultInjector = nil })

	app, configDir := newMigrationGateApp(t)
	orig := []byte(`{"remoteToken":"` + canaryToken + `","remoteEnabled":true,"remoteHost":"127.0.0.1","remotePort":8680}` + "\n")
	writeSettings(t, configDir, orig)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false): Finish failed → no Start", secLoaded, startAllowed)
	}
	if !app.hasWarning("迁移标记清理失败") {
		t.Fatal("expected the finish-failed warning")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start when Finish failed")
	}
	// Finish did not run → the token-bearing settings backup is PRESERVED as
	// repairable evidence (next boot Detect → ManualRepair). The canary is still
	// on disk, but the remote is NOT started.
	if !txnDirExists(configDir) {
		t.Fatal("txn dir must be PRESERVED when Finish fails (ManualRepair evidence)")
	}
	if !containsBytesGlob(t, configDir, canaryToken) {
		t.Fatal("token-bearing settings backup must be PRESERVED as repairable evidence when Finish fails")
	}
	// Device backup cleanup (step i) never reached either.
	if deviceBackupDirEmpty(configDir) {
		t.Fatal("device backup must be PRESERVED when Finish fails")
	}
	// Confirm the next-boot classification is ManualRepair.
	migStore := settings.NewRemoteSecurityMigrationStore(configDir)
	det, err := migStore.Detect()
	if err != nil || det.State != settings.DetectionManualRepair {
		t.Fatalf("Detect=(%v,%v) want ManualRepair after Finish failure", det.State, err)
	}
}

// TestMigrationGate_RollbackEndFails_TxnDirAndDeviceBackupPreserved_ManualRepair
// is the R2-Major-02 regression at the gate level: during a Stage-failure
// rollback, the injector fails the Device Store End step (rollback_end). With
// the OLD single-shot mig.Rollback() the settings txn dir was already removed
// before End ran (Rollback deleted all artifacts in step 3), so an End failure
// left NO txn dir and next Detect classified NeedsMigration instead of the
// authority-mandated ManualRepair. With the split API (RestoreExactOld → End →
// DiscardTransaction) the txn dir is preserved until End succeeds, so an End
// failure leaves the txn dir + device backup on disk → next Detect ManualRepair.
func TestMigrationGate_RollbackEndFails_TxnDirAndDeviceBackupPreserved_ManualRepair(t *testing.T) {
	migrationFaultInjector = func(step string) error {
		if step == "rollback_end" {
			return errors.New("injected End failure")
		}
		return nil
	}
	t.Cleanup(func() { migrationFaultInjector = nil })

	app, configDir := newMigrationGateApp(t)
	oversizeV0Settings(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false): End failure must forbid Start", secLoaded, startAllowed)
	}
	if !app.hasWarning("不确定状态") {
		t.Fatal("expected the indeterminate (manual-repair) warning on rollback End failure")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start after a rollback End failure")
	}
	// Settings txn dir PRESERVED (RestoreExactOld ran, DiscardTransaction never reached).
	// Its existence (even empty) is what makes Detect classify ManualRepair.
	if !txnDirExists(configDir) {
		t.Fatal("settings txn dir must be PRESERVED when End fails (ManualRepair evidence)")
	}
	// Device backup PRESERVED (cleanup never reached).
	if deviceBackupDirEmpty(configDir) {
		t.Fatal("device backup must be PRESERVED when End fails (no cleanup)")
	}
	// Next-boot classification: ManualRepair (txn dir present).
	migStore := settings.NewRemoteSecurityMigrationStore(configDir)
	det, err := migStore.Detect()
	if err != nil || det.State != settings.DetectionManualRepair {
		t.Fatalf("Detect=(%v,%v) want ManualRepair after End failure", det.State, err)
	}
}

// TestMigrationGate_RollbackSettingsDiscardFails_TxnDirPreserved_ManualRepair:
// End succeeds but DiscardTransaction (the ONLY step that removes the txn dir)
// is injected to fail. The txn dir must be PRESERVED → next Detect ManualRepair.
// This proves the split keeps the txn evidence alive across the End→Discard
// boundary (the old Rollback would have removed it in step 3, before End).
func TestMigrationGate_RollbackSettingsDiscardFails_TxnDirPreserved_ManualRepair(t *testing.T) {
	migrationFaultInjector = func(step string) error {
		if step == "rollback_settings_discard" {
			return errors.New("injected DiscardTransaction failure")
		}
		return nil
	}
	t.Cleanup(func() { migrationFaultInjector = nil })

	app, configDir := newMigrationGateApp(t)
	oversizeV0Settings(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false)", secLoaded, startAllowed)
	}
	if !app.hasWarning("不确定状态") {
		t.Fatal("expected the indeterminate warning on DiscardTransaction failure")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start after DiscardTransaction failure")
	}
	// End succeeded but DiscardTransaction failed → txn dir remains → ManualRepair.
	if !txnDirExists(configDir) {
		t.Fatal("settings txn dir must be PRESERVED when DiscardTransaction fails")
	}
	if deviceBackupDirEmpty(configDir) {
		t.Fatal("device backup must be PRESERVED when DiscardTransaction fails")
	}
	migStore := settings.NewRemoteSecurityMigrationStore(configDir)
	det, err := migStore.Detect()
	if err != nil || det.State != settings.DetectionManualRepair {
		t.Fatalf("Detect=(%v,%v) want ManualRepair after DiscardTransaction failure", det.State, err)
	}
}

// TestMigrationGate_RollbackCleanupFails_WarnOnly_NoManualRepair: the rollback
// critical section (restore→Validate→RestoreExactOld→End) AND DiscardTransaction
// all succeed; only the device backup cleanup (rollback_cleanup) is injected to
// fail. The settings txn dir is already gone (DiscardTransaction succeeded) so
// the rollback is logically complete; the leftover device backup is a storage
// leak, NOT a ManualRepair condition. The gate records the cleanup warning,
// forbids Start (rollback path always does), and next Detect → NeedsMigration.
func TestMigrationGate_RollbackCleanupFails_WarnOnly_NoManualRepair(t *testing.T) {
	migrationFaultInjector = func(step string) error {
		if step == "rollback_cleanup" {
			return errors.New("injected device backup cleanup failure")
		}
		return nil
	}
	t.Cleanup(func() { migrationFaultInjector = nil })

	app, configDir := newMigrationGateApp(t)
	oversizeV0Settings(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false)", secLoaded, startAllowed)
	}
	// Cleanup failure is warn-only; rollback is considered successful (stage-failed
	// warning), PLUS the cleanup-backup warning.
	if !app.hasWarning("设置暂存失败并已回滚") {
		t.Fatal("expected the stage-failed (rollback succeeded) warning")
	}
	if !app.hasWarning("设备备份清理失败") {
		t.Fatal("expected the cleanup-backup warning on device backup cleanup failure")
	}
	if app.hasWarning("不确定状态") {
		t.Fatal("must NOT record indeterminate when only device backup cleanup fails")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start after a Stage failure even on successful rollback")
	}
	// Settings txn dir GONE (DiscardTransaction succeeded) → next Detect NOT ManualRepair.
	if txnDirExists(configDir) {
		t.Fatal("settings txn dir must be REMOVED when DiscardTransaction succeeds")
	}
	// Device backup leftover (cleanup failed) — storage leak, not ManualRepair.
	if deviceBackupDirEmpty(configDir) {
		t.Fatal("device backup must REMAIN (orphan) when cleanup fails")
	}
	// Next-boot classification: NeedsMigration (settings.json is v0, txn dir gone).
	migStore := settings.NewRemoteSecurityMigrationStore(configDir)
	det, err := migStore.Detect()
	if err != nil {
		t.Fatalf("Detect err: %v", err)
	}
	if det.State != settings.DetectionNeedsMigration {
		t.Fatalf("Detect=%v want NeedsMigration after rollback cleanup failure (txn dir gone, v0 restored)", det.State)
	}
}

// TestMigrationGate_RollbackValidateFails_EndAndDiscardNeverRun proves the
// critical-section ordering: an early Validate failure never reaches End or
// DiscardTransaction, so the txn dir + device backup are preserved (ManualRepair).
func TestMigrationGate_RollbackValidateFails_EndAndDiscardNeverRun(t *testing.T) {
	var order []string
	migrationFaultInjector = func(step string) error {
		order = append(order, step)
		if step == "rollback_validate" {
			return errors.New("injected Validate failure")
		}
		return nil
	}
	t.Cleanup(func() { migrationFaultInjector = nil })

	app, configDir := newMigrationGateApp(t)
	oversizeV0Settings(t, configDir)

	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if secLoaded != true || startAllowed {
		t.Fatalf("gate=(%v,%v) want (true,false)", secLoaded, startAllowed)
	}
	// restore ran, validate failed → End / DiscardTransaction / cleanup never reached.
	wantPrefix := []string{"rollback_device_restore", "rollback_validate"}
	if len(order) != len(wantPrefix) {
		t.Fatalf("rollback step order=%v want exactly %v (End/Discard/cleanup must not run)", order, wantPrefix)
	}
	for i, s := range wantPrefix {
		if order[i] != s {
			t.Fatalf("rollback step %d=%q want %q", i, order[i], s)
		}
	}
	if !app.hasWarning("不确定状态") {
		t.Fatal("expected indeterminate warning on Validate failure")
	}
	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	if app.Remote.IsRunning() {
		t.Fatal("remote MUST NOT start after Validate failure")
	}
	if !txnDirExists(configDir) {
		t.Fatal("settings txn dir must be PRESERVED on Validate failure")
	}
	if deviceBackupDirEmpty(configDir) {
		t.Fatal("device backup must be PRESERVED on Validate failure")
	}
	migStore := settings.NewRemoteSecurityMigrationStore(configDir)
	det, err := migStore.Detect()
	if err != nil || det.State != settings.DetectionManualRepair {
		t.Fatalf("Detect=(%v,%v) want ManualRepair after Validate failure", det.State, err)
	}
}
