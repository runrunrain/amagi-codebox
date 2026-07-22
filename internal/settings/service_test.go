package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeDashboardDefaults_DoesNotPropagateLegacyTerminalModeToNonClaudeEngines(t *testing.T) {
	d := DashboardDefaults{Mode: "terminal"}

	normalizeDashboardDefaults(&d)

	if d.ClaudeMode != "terminal" {
		t.Fatalf("ClaudeMode = %q, want legacy mode terminal", d.ClaudeMode)
	}
	if d.OpenCodeMode != "embedded" {
		t.Fatalf("OpenCodeMode = %q, want embedded", d.OpenCodeMode)
	}
	if d.CodexMode != "embedded" {
		t.Fatalf("CodexMode = %q, want embedded", d.CodexMode)
	}
	if d.AmagiCodeMode != "embedded" {
		t.Fatalf("AmagiCodeMode = %q, want embedded", d.AmagiCodeMode)
	}
}

func TestNormalizeDashboardDefaults_PreservesExplicitEngineModes(t *testing.T) {
	d := DashboardDefaults{
		Mode:          "terminal",
		OpenCodeMode:  "terminal",
		CodexMode:     "terminal",
		AmagiCodeMode: "terminal",
	}

	normalizeDashboardDefaults(&d)

	if d.OpenCodeMode != "terminal" || d.CodexMode != "terminal" || d.AmagiCodeMode != "terminal" {
		t.Fatalf("explicit modes not preserved: opencode=%q codex=%q amagicode=%q", d.OpenCodeMode, d.CodexMode, d.AmagiCodeMode)
	}
}

func TestSaveStoresSettingsInPrivateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits are validated on macOS/Linux")
	}
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)
	if err := svc.SetGitHubToken("test-token"); err != nil {
		t.Fatalf("SetGitHubToken: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat settings dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("settings dir mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("stat settings file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("settings file mode = %o, want 600", got)
	}
}

func TestDefaultRemoteSettingsAreLocalAndDisabled(t *testing.T) {
	svc := NewService(t.TempDir())
	if host := svc.GetRemoteHost(); host != "127.0.0.1" {
		t.Fatalf("default remote host = %q, want loopback", host)
	}
	if svc.GetRemoteEnabled() {
		t.Fatal("remote server should require explicit enablement by default")
	}
}

// TestCodexGlobalHeadroom_DefaultsOff verifies a fresh settings store reports
// the codex-global headroom toggle as disabled (zero-value), so a new install
// never starts the second proxy unprompted.
func TestCodexGlobalHeadroom_DefaultsOff(t *testing.T) {
	svc := NewService(t.TempDir())
	state := svc.GetCodexGlobalHeadroom()
	if state.Enabled {
		t.Fatalf("codex global headroom should default to disabled, got %+v", state)
	}
	if state.Target != "" || state.Port != 0 {
		t.Fatalf("codex global headroom default target/port should be zero, got %+v", state)
	}
}

// TestCodexGlobalHeadroom_RoundTrip verifies Set persists Enabled/Target/Port and
// Get reads them back, and disabling clears target/port so no stale config
// survives.
func TestCodexGlobalHeadroom_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	if err := svc.SetCodexGlobalHeadroom(true, "https://api.openai.com/v1", 8788); err != nil {
		t.Fatalf("SetCodexGlobalHeadroom(true): %v", err)
	}
	// New service instance reads the persisted file: proves it landed on disk.
	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	state := svc2.GetCodexGlobalHeadroom()
	if !state.Enabled || state.Target != "https://api.openai.com/v1" || state.Port != 8788 {
		t.Fatalf("persisted state mismatch, got %+v", state)
	}

	// Disabling must clear target/port (no stale config).
	if err := svc2.SetCodexGlobalHeadroom(false, "", 0); err != nil {
		t.Fatalf("SetCodexGlobalHeadroom(false): %v", err)
	}
	disabled := svc2.GetCodexGlobalHeadroom()
	if disabled.Enabled || disabled.Target != "" || disabled.Port != 0 {
		t.Fatalf("disabled state should be fully zeroed, got %+v", disabled)
	}
}

// TestSetDashboardDefaults_PreservesCodexGlobalHeadroom is the regression test
// for the silent-clobber MAJOR bug. The frontend persistDefaults path
// (useDashboardState / useSessionLaunch) replays whatever it cached for
// codexGlobalHeadroom through SetDashboardDefaults on every session launch and
// dashboard save. Because the frontend cache is typically stale (false), this
// used to overwrite the real persisted value and silently disable the
// codex-global headroom, leaving config.toml pointing at a dead 8788 proxy.
//
// SetDashboardDefaults must now re-pin the three CodexGlobal* fields to the
// currently persisted values, ignoring whatever the caller supplied. This test
// enables the toggle, then calls SetDashboardDefaults with a payload that
// explicitly sets CodexGlobalHeadroom=false (and bogus target/port) and asserts
// the persisted state is unchanged.
func TestSetDashboardDefaults_PreservesCodexGlobalHeadroom(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	// Turn the codex-global headroom ON with concrete target/port.
	if err := svc.SetCodexGlobalHeadroom(true, "https://api.openai.com/v1", 8788); err != nil {
		t.Fatalf("SetCodexGlobalHeadroom(true): %v", err)
	}

	// Simulate the stale frontend payload: dashboard save carrying
	// codexGlobalHeadroom=false + bogus target/port (exactly what
	// useDashboardState would replay from its init-only cache).
	stale := DashboardDefaults{
		Mode:                      "embedded",
		Shell:                     "pwsh",
		CodexGlobalHeadroom:       false,
		CodexGlobalHeadroomTarget: "https://stale.example.com/v1",
		CodexGlobalHeadroomPort:   9999,
	}
	if err := svc.SetDashboardDefaults(stale); err != nil {
		t.Fatalf("SetDashboardDefaults: %v", err)
	}

	state := svc.GetCodexGlobalHeadroom()
	if !state.Enabled {
		t.Fatalf("Enabled = false, want true; SetDashboardDefaults must not clobber the codex-global headroom toggle")
	}
	if state.Target != "https://api.openai.com/v1" {
		t.Fatalf("Target = %q, want %q (preserved)", state.Target, "https://api.openai.com/v1")
	}
	if state.Port != 8788 {
		t.Fatalf("Port = %d, want 8788 (preserved)", state.Port)
	}

	// The non-codex fields the caller DID supply must still land.
	dash := svc.GetDashboardDefaults()
	if dash.Mode != "embedded" || dash.Shell != "pwsh" {
		t.Fatalf("non-codex dashboard fields not persisted: Mode=%q Shell=%q", dash.Mode, dash.Shell)
	}

	// And the same invariant must survive a fresh Load from disk.
	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	state2 := svc2.GetCodexGlobalHeadroom()
	if !state2.Enabled || state2.Target != "https://api.openai.com/v1" || state2.Port != 8788 {
		t.Fatalf("after reload, codex-global state not preserved: %+v", state2)
	}
}

// TestSetDashboardDefaults_PreservesCodexGlobalHeadroomDisabled verifies the
// preservation is symmetric: when the feature is OFF, a stale frontend payload
// claiming codexGlobalHeadroom=true must NOT silently enable it.
func TestSetDashboardDefaults_PreservesCodexGlobalHeadroomDisabled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	// Feature stays OFF (default). Stale payload tries to flip it on.
	stale := DashboardDefaults{
		Mode:                      "embedded",
		CodexGlobalHeadroom:       true,
		CodexGlobalHeadroomTarget: "https://stale.example.com/v1",
		CodexGlobalHeadroomPort:   9999,
	}
	if err := svc.SetDashboardDefaults(stale); err != nil {
		t.Fatalf("SetDashboardDefaults: %v", err)
	}

	state := svc.GetCodexGlobalHeadroom()
	if state.Enabled {
		t.Fatalf("Enabled = true, want false; stale payload must not silently enable codex-global headroom")
	}
	if state.Target != "" || state.Port != 0 {
		t.Fatalf("disabled codex-global state should stay zeroed, got %+v", state)
	}
}
