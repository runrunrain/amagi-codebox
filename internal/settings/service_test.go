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
