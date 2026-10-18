package envcheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// RC-3: install_node must not report fake success on Windows when winget
// fails. The old branch returned Success=true with manual guidance, so the
// dependency stayed missing while the UI treated the action as handled.
// ---------------------------------------------------------------------------

func TestInstallNodeWindowsWingetFailureReportsFailure(t *testing.T) {
	guiEnvIsolation(t)
	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		if filepath.Base(spec.Path) == "winget" {
			return &platform.ProcessResult{Stderr: "0x8a15002f"}, errors.New("winget install failed")
		}
		return &platform.ProcessResult{}, os.ErrNotExist
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.runInstallNodeWindows()
	if err != nil {
		t.Fatalf("runInstallNodeWindows returned error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("winget failure must not report success, got %+v", result)
	}
	if result.Error == "" || !strings.Contains(result.Error, "winget") {
		t.Fatalf("failure should carry the winget error, got %+v", result)
	}
	if len(result.NextSteps) == 0 {
		t.Fatal("failure should keep manual installation guidance")
	}
}

func TestInstallNodeWindowsWingetSuccessProbesNPMAvailability(t *testing.T) {
	home := guiEnvIsolation(t)
	t.Setenv("FNM_DIR", "")
	// Hermetic fake npm so the post-install probe can succeed.
	npmBin := filepath.Join(home, "npm-global", "bin")
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, npmBin, "npm")
	t.Setenv("PATH", npmBin)

	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		switch filepath.Base(spec.Path) {
		case "winget":
			return &platform.ProcessResult{Stdout: "Successfully installed"}, nil
		case "npm":
			return &platform.ProcessResult{Stdout: "10.0.0"}, nil
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.runInstallNodeWindows()
	if err != nil {
		t.Fatalf("runInstallNodeWindows returned error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("winget success + npm available should report success, got %+v", result)
	}
	if !strings.Contains(result.Message, "npm") {
		t.Fatalf("message should mention the verified npm state: %+v", result)
	}
}

// integration finding #5：winget exit 0 但 npm 可用性未确认时，不得报安装
// 成功；结果必须明确“未确认 + 需重启复检”。
func TestInstallNodeWindowsWingetSuccessNPMAvailabilityUnconfirmed(t *testing.T) {
	guiEnvIsolation(t) // no npm anywhere: post-install probe must fail

	runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		if filepath.Base(spec.Path) == "winget" {
			return &platform.ProcessResult{Stdout: "Successfully installed"}, nil
		}
		return &platform.ProcessResult{}, os.ErrNotExist
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.runInstallNodeWindows()
	if err != nil {
		t.Fatalf("runInstallNodeWindows returned error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("unconfirmed dependency must not report install success, got %+v", result)
	}
	if !result.RequiresRestart {
		t.Fatal("unconfirmed dependency should require restart + recheck")
	}
	if !strings.Contains(result.Message+result.Error, "未确认") {
		t.Fatalf("result must explicitly say the dependency is unconfirmed, got %+v", result)
	}
}

// integration finding #5：Linux 手动指引分支不得报成功（依赖未安装也未确认）。
func TestInstallNodeLinuxManualGuidanceNotSuccess(t *testing.T) {
	guiEnvIsolation(t)
	svc := NewServiceWithRunner(&specRunner{respond: func(platform.CommandSpec) (*platform.ProcessResult, error) {
		return &platform.ProcessResult{}, os.ErrNotExist
	}})

	result, err := svc.runInstallNodeLinux()
	if err != nil {
		t.Fatalf("runInstallNodeLinux returned error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("manual guidance must not claim success, got %+v", result)
	}
	if len(result.NextSteps) == 0 {
		t.Fatal("manual guidance must keep installation steps")
	}
}

// ---------------------------------------------------------------------------
// Detection lock: the fnm stable install root must be recognized in a GUI
// (minimal-PATH) environment. Locks the resolution chain the Leader verified
// as missing before fnm support landed (local-path-evidence.md).
// ---------------------------------------------------------------------------

func TestCheckPiDetectsFNMStableRootInGUIEnv(t *testing.T) {
	home := guiEnvIsolation(t)
	// fnm stable layout: <base>/node-versions/vX/installation/bin (+ package
	// symlink so the checker classifies it as an npm install).
	fnmBin := filepath.Join(home, ".local", "share", "fnm", "node-versions", "v24.21.0", "installation", "bin")
	pkgBin := filepath.Join(home, ".local", "share", "fnm", "node-versions", "v24.21.0", "installation", "lib", "node_modules", "@earendil-works", "pi-coding-agent", "bin")
	if err := os.MkdirAll(pkgBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, pkgBin, "pi")
	if err := os.MkdirAll(fnmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(pkgBin, "pi"), filepath.Join(fnmBin, "pi")); err != nil {
		t.Fatal(err)
	}

	runner := versionProbeRunner("pi", "pi 0.81.1")
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolPi)
	if err != nil {
		t.Fatalf("CheckOne(pi): %v", err)
	}
	if !status.Installed {
		t.Fatalf("pi installed under the fnm stable root must be detected in a GUI env; status=%+v", status)
	}
	if !status.PATHOk {
		t.Fatal("CodeBox must be able to launch the fnm-managed pi")
	}
	if status.PathState != PathStateCodeboxPATH {
		t.Fatalf("GUI env should resolve via the resolver (codebox_path), got %q (source %q)", status.PathState, status.PathSource)
	}
	if status.SystemPATHOk {
		t.Fatal("the minimal GUI PATH itself must not see the fnm dir (precondition)")
	}
	if status.Version != "0.81.1" {
		t.Fatalf("version should come from the version probe, got %q", status.Version)
	}
}
