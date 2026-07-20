package envcheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// Venv install full flow
// ---------------------------------------------------------------------------

// headroomVenvInstallRunner is a ProcessRunner double that emulates the full
// CodeBox headroom venv install flow on disk:
//   - python3 --version probe → responds with a version string.
//   - python3 -m venv <dir>   → creates the venv bin directory and writes fake
//     python + headroom executables as a side effect (so detection's fileExists
//     fallback finds headroom after install).
//   - <venv-python> -m pip install headroom-ai[proxy] → responds with pip
//     success output.
//   - <venv-headroom> --version / -V → responds with headroom version.
type headroomVenvInstallRunner struct {
	venvDir string
	mu      sync.Mutex
	calls   []platform.CommandSpec
}

func (r *headroomVenvInstallRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, spec)

	pathBase := strings.ToLower(filepath.Base(spec.Path))
	args := spec.Args

	if strings.Contains(pathBase, "headroom") {
		if len(args) == 1 && (args[0] == "--version" || args[0] == "-V") {
			return &platform.ProcessResult{Stdout: "headroom 0.30.0"}, nil
		}
	}

	if strings.Contains(pathBase, "python") {
		if len(args) == 1 && args[0] == "--version" {
			return &platform.ProcessResult{Stdout: "Python 3.11.6"}, nil
		}
		if len(args) == 3 && args[0] == "-m" && args[1] == "venv" {
			if err := r.createVenvSideEffect(args[2]); err != nil {
				return nil, fmt.Errorf("test venv side effect: %w", err)
			}
			return &platform.ProcessResult{}, nil
		}
		if len(args) >= 3 && args[0] == "-m" && args[1] == "pip" && args[2] == "install" {
			return &platform.ProcessResult{Stdout: "Successfully installed headroom-ai-0.30.0"}, nil
		}
	}

	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *headroomVenvInstallRunner) createVenvSideEffect(dir string) error {
	binDir := filepath.Join(dir, "bin")
	headroomName := "headroom"
	pythonName := "python"
	content := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(dir, "Scripts")
		headroomName = "headroom.exe"
		pythonName = "python.exe"
		content = []byte("@echo off\r\nexit /b 0\r\n")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(binDir, pythonName), content, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(binDir, headroomName), content, 0o755)
}

func (r *headroomVenvInstallRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// TestHeadroomVenvInstall_FullFlow exercises the full install pipeline:
// pre-check (not installed) → ensurePythonAvailable → ensureHeadroomVenv →
// venv pip install headroom-ai[proxy] → post-check (installed via venv
// fallback). Asserts the install command uses [proxy] (not [all]) and the
// venv python as the invocation path.
func TestHeadroomVenvInstall_FullFlow(t *testing.T) {
	// Shorten recheck windows so the test stays bounded even if verification
	// retries kick in.
	prevAttempts := installRecheckAttempts
	prevDelay := installRecheckDelay
	installRecheckAttempts = 1
	installRecheckDelay = 0
	t.Cleanup(func() {
		installRecheckAttempts = prevAttempts
		installRecheckDelay = prevDelay
	})

	// Put a fake python3 on PATH so the platform resolver can locate it.
	pathDir := t.TempDir()
	writeTestExecutable(t, pathDir, "python3")
	t.Setenv("PATH", pathDir)

	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	runner := &headroomVenvInstallRunner{venvDir: venvDir}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	result, err := svc.Install(ToolHeadroom)
	if err != nil {
		t.Fatalf("Install(ToolHeadroom) error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("Install(ToolHeadroom) not successful: %+v", result)
	}

	// The venv bin must contain a headroom executable (created by the mock
	// venv side effect).
	headroomExe := filepath.Join(venvDir, "bin", "headroom")
	if runtime.GOOS == "windows" {
		headroomExe = filepath.Join(venvDir, "Scripts", "headroom.exe")
	}
	if !fileExists(headroomExe) {
		t.Fatalf("headroom executable not created at %s", headroomExe)
	}

	// The pip install command must have run with [proxy] extras and the venv
	// python as the invocation path.
	var pipCmd *platform.CommandSpec
	for i := range runner.calls {
		call := runner.calls[i]
		if len(call.Args) >= 3 && call.Args[0] == "-m" && call.Args[1] == "pip" && call.Args[2] == "install" {
			pipCmd = &runner.calls[i]
			break
		}
	}
	if pipCmd == nil {
		t.Fatal("venv pip install command was never invoked")
	}
	foundProxy := false
	for _, arg := range pipCmd.Args {
		if arg == "headroom-ai[proxy]" {
			foundProxy = true
		}
		if arg == "headroom-ai[all]" {
			t.Errorf("install must use [proxy] extra, not [all]; args=%v", pipCmd.Args)
		}
	}
	if !foundProxy {
		t.Errorf("pip install args should contain headroom-ai[proxy], got: %v", pipCmd.Args)
	}
	// The invocation path must be the venv python, not a bare "pip".
	expectedPython := headroomVenvPythonPath(venvDir)
	if !strings.HasSuffix(pipCmd.Path, filepath.Base(expectedPython)) {
		t.Errorf("pip install path = %q, want venv python %q", pipCmd.Path, expectedPython)
	}

	// The venv creation command must have been invoked with -m venv.
	foundVenv := false
	for _, call := range runner.calls {
		if len(call.Args) == 3 && call.Args[0] == "-m" && call.Args[1] == "venv" && call.Args[2] == venvDir {
			foundVenv = true
		}
	}
	if !foundVenv {
		t.Error("python -m venv <dir> was never invoked during install")
	}
}

// TestHeadroomVenvInstall_EnsureVenvIsIdempotent verifies ensureHeadroomVenv
// skips venv creation when the venv directory already contains a runnable
// python. This protects against re-running `python3 -m venv` on every install.
func TestHeadroomVenvInstall_EnsureVenvIsIdempotent(t *testing.T) {
	pathDir := t.TempDir()
	writeTestExecutable(t, pathDir, "python3")
	t.Setenv("PATH", pathDir)

	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	// Pre-create the venv python so headroomVenvPythonExists returns true.
	venvBin := filepath.Join(venvDir, "bin")
	pythonName := "python"
	if runtime.GOOS == "windows" {
		venvBin = filepath.Join(venvDir, "Scripts")
		pythonName = "python.exe"
	}
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(venvBin, pythonName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake venv python: %v", err)
	}

	runner := &headroomVenvInstallRunner{venvDir: venvDir}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	if err := svc.ensureHeadroomVenv(); err != nil {
		t.Fatalf("ensureHeadroomVenv on existing venv: %v", err)
	}

	// The venv creation command must NOT have been invoked.
	for _, call := range runner.calls {
		if len(call.Args) >= 2 && call.Args[0] == "-m" && call.Args[1] == "venv" {
			t.Fatalf("ensureHeadroomVenv should skip creation when venv python exists; got venv command: %+v", call)
		}
	}
}

// headroomVenvRebuildRunner emulates an existing old venv that reports Python
// 3.9 until CodeBox recreates it. The selected external runtime reports 3.12.
type headroomVenvRebuildRunner struct {
	venvPythonPath string
	rebuilt        bool
	calls          []platform.CommandSpec
}

func (r *headroomVenvRebuildRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.calls = append(r.calls, spec)
	if len(spec.Args) == 1 && spec.Args[0] == "--version" {
		if sameNormalizedPath(spec.Path, r.venvPythonPath) && !r.rebuilt {
			return &platform.ProcessResult{Stdout: "Python 3.9.6"}, nil
		}
		return &platform.ProcessResult{Stdout: "Python 3.12.13"}, nil
	}
	if len(spec.Args) >= 3 && spec.Args[0] == "-m" && spec.Args[1] == "venv" {
		r.rebuilt = true
		return &platform.ProcessResult{}, nil
	}
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *headroomVenvRebuildRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func TestHeadroomVenvInstall_RebuildsUnsupportedManagedVenv(t *testing.T) {
	pathDir := t.TempDir()
	compatiblePythonPath := writeTestExecutable(t, pathDir, "python3.12")
	t.Setenv("PATH", pathDir)

	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	venvBinDir := filepath.Dir(headroomVenvPythonPath(venvDir))
	venvPythonPath := headroomVenvPythonPath(venvDir)
	if err := os.MkdirAll(venvBinDir, 0o755); err != nil {
		t.Fatalf("mkdir old venv bin: %v", err)
	}
	if err := os.WriteFile(venvPythonPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write old venv Python: %v", err)
	}

	runner := &headroomVenvRebuildRunner{venvPythonPath: venvPythonPath}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	if err := svc.ensureHeadroomVenv(); err != nil {
		t.Fatalf("ensureHeadroomVenv: %v", err)
	}
	if !sameNormalizedPath(svc.pythonPath, compatiblePythonPath) {
		t.Fatalf("selected runtime = %q, want %q", svc.pythonPath, compatiblePythonPath)
	}

	var rebuildCall *platform.CommandSpec
	for i := range runner.calls {
		call := &runner.calls[i]
		if len(call.Args) >= 2 && call.Args[0] == "-m" && call.Args[1] == "venv" {
			rebuildCall = call
			break
		}
	}
	if rebuildCall == nil {
		t.Fatal("expected old CodeBox venv to be rebuilt")
	}
	wantArgs := []string{"-m", "venv", "--clear", venvDir}
	if strings.Join(rebuildCall.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("rebuild args = %v, want %v", rebuildCall.Args, wantArgs)
	}
}

// ---------------------------------------------------------------------------
// cleanHeadroom
// ---------------------------------------------------------------------------

// TestCleanHeadroom_RemovesVenvDirectory verifies cleanHeadroom deletes the
// managed venv directory and leaves the tool uninstalled.
func TestCleanHeadroom_RemovesVenvDirectory(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	binDir := filepath.Join(venvDir, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	writeTestExecutable(t, binDir, "headroom")

	// Keep PATH empty so only the venv fallback can find headroom.
	t.Setenv("PATH", t.TempDir())

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "headroom", stdout: "headroom 0.30.0"},
	}}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	// Pre-clean check: headroom is installed via venv fallback.
	before, _ := svc.CheckOne(ToolHeadroom)
	if !before.Installed {
		t.Fatalf("expected headroom installed before clean, got %+v", before)
	}

	result, err := svc.CleanHeadroom()
	if err != nil {
		t.Fatalf("CleanHeadroom error: %v", err)
	}
	if !result.Success {
		t.Fatalf("CleanHeadroom not successful: %+v", result)
	}

	// The venv directory must be gone.
	if _, statErr := os.Stat(venvDir); !os.IsNotExist(statErr) {
		t.Fatalf("venv dir should be removed after clean; stat err: %v", statErr)
	}

	// Post-clean check: headroom no longer installed.
	after, _ := svc.CheckOne(ToolHeadroom)
	if after.Installed {
		t.Fatalf("headroom should not be installed after clean, got %+v", after)
	}
}

// TestCleanHeadroom_NotInstalledReturnsSuccess verifies the early-exit when
// headroom is not installed (no venv, nothing to remove).
func TestCleanHeadroom_NotInstalledReturnsSuccess(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	t.Setenv("PATH", t.TempDir())

	svc := NewServiceWithRunner(&mockRunner{})
	svc.SetHeadroomVenvDir(venvDir)

	result, err := svc.CleanHeadroom()
	if err != nil {
		t.Fatalf("CleanHeadroom on uninstalled: %v", err)
	}
	if !result.Success {
		t.Fatalf("CleanHeadroom on uninstalled should succeed: %+v", result)
	}
}

// TestCleanHeadroom_InvokesHeadroomStopperBeforeRemoval asserts that when a
// headroomStopper is injected via SetHeadroomStopper, CleanHeadroom invokes it
// before removing the venv directory. This is the M-1 fix: on Windows a
// running headroom.exe inside the venv is locked by the OS and os.RemoveAll
// would fail, so the stopper (HeadroomService.Stop) must run first. The test
// verifies both the call ordering (stopper before RemoveAll) and that the
// stopper is invoked exactly once.
func TestCleanHeadroom_InvokesHeadroomStopperBeforeRemoval(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	binDir := filepath.Join(venvDir, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	writeTestExecutable(t, binDir, "headroom")

	t.Setenv("PATH", t.TempDir())

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "headroom", stdout: "headroom 0.30.0"},
	}}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	stopperCalls := 0
	stopperVenvAtCall := ""
	var stopperMu sync.Mutex
	svc.SetHeadroomStopper(func() error {
		stopperMu.Lock()
		defer stopperMu.Unlock()
		stopperCalls++
		// Capture whether the venv still exists at the moment the stopper
		// fires. The contract is "stopper runs BEFORE RemoveAll", so the venv
		// must still be on disk when the stopper is invoked.
		if _, err := os.Stat(venvDir); err == nil {
			stopperVenvAtCall = "present"
		} else {
			stopperVenvAtCall = "absent"
		}
		return nil
	})

	result, err := svc.CleanHeadroom()
	if err != nil {
		t.Fatalf("CleanHeadroom error: %v", err)
	}
	if !result.Success {
		t.Fatalf("CleanHeadroom not successful: %+v", result)
	}

	if stopperCalls != 1 {
		t.Errorf("headroomStopper call count = %d, want 1", stopperCalls)
	}
	if stopperVenvAtCall != "present" {
		t.Errorf("stopper observed venv = %q, want %q (stopper must run before RemoveAll)", stopperVenvAtCall, "present")
	}
	// Venv must be gone after CleanHeadroom returns.
	if _, statErr := os.Stat(venvDir); !os.IsNotExist(statErr) {
		t.Fatalf("venv dir should be removed; stat err: %v", statErr)
	}
}

// TestCleanHeadroom_StopperErrorDoesNotBlockUninstall asserts that a non-nil
// error from the injected stopper does NOT abort the venv removal. The stopper
// is best-effort: the proxy may legitimately already be dead, and that must
// not prevent the authoritative uninstall (venv removal) from proceeding.
func TestCleanHeadroom_StopperErrorDoesNotBlockUninstall(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	binDir := filepath.Join(venvDir, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	writeTestExecutable(t, binDir, "headroom")

	t.Setenv("PATH", t.TempDir())

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "headroom", stdout: "headroom 0.30.0"},
	}}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	stopperCalled := false
	svc.SetHeadroomStopper(func() error {
		stopperCalled = true
		return fmt.Errorf("simulated stop failure (proxy already dead)")
	})

	result, err := svc.CleanHeadroom()
	if err != nil {
		t.Fatalf("CleanHeadroom error with failing stopper: %v", err)
	}
	if !result.Success {
		t.Fatalf("CleanHeadroom must succeed despite stopper error: %+v", result)
	}
	if !stopperCalled {
		t.Error("headroomStopper was not invoked")
	}
	if _, statErr := os.Stat(venvDir); !os.IsNotExist(statErr) {
		t.Fatalf("venv dir should be removed despite stopper error; stat err: %v", statErr)
	}
}

// ---------------------------------------------------------------------------
// checkHeadroom venv fallback
// ---------------------------------------------------------------------------

// TestCheckHeadroom_FallsBackToVenvCandidate verifies that when headroom is
// not on PATH, the check falls back to the CodeBox venv candidate (mirroring
// the Claude Native default-location fallback pattern). This is the core fix:
// resolveExecutable reads os.Environ() and cannot see the enhanced PATH, so
// the venv bin injection alone is insufficient for detection.
func TestCheckHeadroom_FallsBackToVenvCandidate(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	binDir := filepath.Join(venvDir, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	writeTestExecutable(t, binDir, "headroom")

	// Empty PATH so resolveExecutable cannot find headroom; only the venv
	// fallback should locate it.
	t.Setenv("PATH", t.TempDir())

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "headroom", stdout: "headroom 0.30.0"},
	}}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	status, err := svc.CheckOne(ToolHeadroom)
	if err != nil {
		t.Fatalf("CheckOne(ToolHeadroom) error: %v", err)
	}
	if !status.Installed {
		t.Fatalf("expected headroom installed via venv fallback, got %+v", status)
	}
	if status.PathSource != "codebox-venv" {
		t.Errorf("PathSource = %q, want %q", status.PathSource, "codebox-venv")
	}
	if status.PathState != PathStateCodeboxPATH {
		t.Errorf("PathState = %q, want %q", status.PathState, PathStateCodeboxPATH)
	}
	if status.InstallMethod != InstallMethodCodeboxVenv {
		t.Errorf("InstallMethod = %q, want %q", status.InstallMethod, InstallMethodCodeboxVenv)
	}
	if status.Version != "0.30.0" {
		t.Errorf("Version = %q, want %q", status.Version, "0.30.0")
	}
}

// TestCheckHeadroom_NoFallbackWhenVenvDirUnset verifies that when the venv dir
// is not configured, detection behaves like the legacy PATH-only behaviour
// (no venv fallback).
func TestCheckHeadroom_NoFallbackWhenVenvDirUnset(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	svc := NewServiceWithRunner(&mockRunner{})
	// Deliberately do NOT call SetHeadroomVenvDir.

	status, err := svc.CheckOne(ToolHeadroom)
	if err != nil {
		t.Fatalf("CheckOne error: %v", err)
	}
	if status.Installed {
		t.Fatalf("headroom should not be installed when venv is unset and PATH is empty, got %+v", status)
	}
}

// ---------------------------------------------------------------------------
// detectHeadroomInstallMethod
// ---------------------------------------------------------------------------

// TestDetectHeadroomInstallMethod_VenvPath verifies venv paths are classified
// as InstallMethodCodeboxVenv (not Pip, even though venv site-packages also
// contains the site-packages marker).
func TestDetectHeadroomInstallMethod_VenvPath(t *testing.T) {
	venvDir := filepath.Join("/tmp", "amagi-codebox", "headroom-venv")
	svc := NewServiceWithRunner(&mockRunner{})
	svc.SetHeadroomVenvDir(venvDir)

	// A headroom binary under the venv bin directory.
	binPath := filepath.Join(venvDir, "bin", "headroom")
	got := svc.detectHeadroomInstallMethod(binPath)
	if got != InstallMethodCodeboxVenv {
		t.Errorf("detectHeadroomInstallMethod(%q) = %q, want %q", binPath, got, InstallMethodCodeboxVenv)
	}

	// Even a site-packages path under the venv root must be CodeboxVenv, not Pip.
	sitePackagesPath := filepath.Join(venvDir, "lib", "python3.11", "site-packages", "headroom", "cli.py")
	got = svc.detectHeadroomInstallMethod(sitePackagesPath)
	if got != InstallMethodCodeboxVenv {
		t.Errorf("detectHeadroomInstallMethod(venv site-packages) = %q, want %q", got, InstallMethodCodeboxVenv)
	}
}

// TestDetectHeadroomInstallMethod_SystemPipPath verifies system pip paths are
// still classified as Pip (not swept into CodeboxVenv).
func TestDetectHeadroomInstallMethod_SystemPipPath(t *testing.T) {
	svc := NewServiceWithRunner(&mockRunner{})
	svc.SetHeadroomVenvDir(filepath.Join("/tmp", "amagi-codebox", "headroom-venv"))

	systemPipPath := filepath.Join("/usr", "local", "lib", "python3.11", "site-packages", "headroom", "cli.py")
	got := svc.detectHeadroomInstallMethod(systemPipPath)
	if got != InstallMethodPip {
		t.Errorf("detectHeadroomInstallMethod(system pip) = %q, want %q", got, InstallMethodPip)
	}
}

// ---------------------------------------------------------------------------
// populateHeadroomCanInstall
// ---------------------------------------------------------------------------

// TestPopulateHeadroomCanInstall_ProbesPython verifies canInstall now probes
// python3 (venv capability) and exposes the method key as "venv" (not "pip").
func TestPopulateHeadroomCanInstall_ProbesPython(t *testing.T) {
	pathDir := t.TempDir()
	writeTestExecutable(t, pathDir, "python3")
	t.Setenv("PATH", pathDir)

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "python", stdout: "Python 3.11.6"},
	}}
	svc := NewServiceWithRunner(runner)

	status := &CheckStatus{Tool: ToolHeadroom}
	svc.populateHeadroomCanInstall(status)

	if !status.CanInstall {
		t.Errorf("CanInstall = false, want true (python3 is available)")
	}
	if !status.CanInstallByMethod["venv"] {
		t.Errorf("CanInstallByMethod[venv] = false, want true")
	}
}

// TestHeadroomPythonProbePrefersSupportedVersionedRuntime protects the macOS
// regression where /usr/bin/python3 (3.9) shadowed an installed Homebrew
// python3.12. Headroom requires Python 3.10+, so discovery must prefer the
// versioned compatible candidate rather than stop at the generic name.
func TestHeadroomPythonProbePrefersSupportedVersionedRuntime(t *testing.T) {
	pathDir := t.TempDir()
	python39Path := writeTestExecutable(t, pathDir, "python3")
	python312Path := writeTestExecutable(t, pathDir, "python3.12")
	t.Setenv("PATH", pathDir)

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "python3.12", stdout: "Python 3.12.13"},
		{pathPrefix: "python", stdout: "Python 3.9.6"},
	}}
	svc := NewServiceWithRunner(runner)

	if err := svc.ensurePythonAvailable(); err != nil {
		t.Fatalf("ensurePythonAvailable: %v", err)
	}
	if !svc.pythonAvailable {
		t.Fatal("pythonAvailable = false, want true")
	}
	if !sameNormalizedPath(svc.pythonPath, python312Path) {
		t.Fatalf("selected Python path = %q, want compatible Python %q (not %q)", svc.pythonPath, python312Path, python39Path)
	}
	if svc.pythonVersion != "3.12.13" {
		t.Fatalf("selected Python version = %q, want 3.12.13", svc.pythonVersion)
	}
}

func TestPopulateHeadroomCanInstall_RejectsUnsupportedPythonVersion(t *testing.T) {
	pathDir := t.TempDir()
	writeTestExecutable(t, pathDir, "python3")
	t.Setenv("PATH", pathDir)

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "python", stdout: "Python 3.9.6"},
	}}
	svc := NewServiceWithRunner(runner)
	status := &CheckStatus{Tool: ToolHeadroom}
	svc.populateHeadroomCanInstall(status)

	if status.CanInstall {
		t.Fatal("CanInstall = true, want false for Python 3.9")
	}
	if !hasIssueCode(status, "python_version_unsupported") {
		t.Fatalf("expected python_version_unsupported issue, got %+v", status.Issues)
	}
	if !strings.Contains(status.InstallBlockedReason, "Python 3.10+") {
		t.Fatalf("blocked reason should explain the minimum version, got %q", status.InstallBlockedReason)
	}
}

// TestPopulateHeadroomCanInstall_PythonNotAvailable verifies the blocked path
// surfaces a python_not_found issue (not pip_not_found).
func TestPopulateHeadroomCanInstall_PythonNotAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty PATH, no python

	svc := NewServiceWithRunner(&mockRunner{})

	status := &CheckStatus{Tool: ToolHeadroom}
	svc.populateHeadroomCanInstall(status)

	if status.CanInstall {
		t.Errorf("CanInstall = true, want false (python3 not available)")
	}
	if !hasIssueCode(status, "python_not_found") {
		t.Errorf("expected python_not_found issue, got %+v", status.Issues)
	}
	if hasIssueCode(status, "pip_not_found") {
		t.Errorf("legacy pip_not_found issue must not appear, got %+v", status.Issues)
	}
}

// ---------------------------------------------------------------------------
// headroomVenvInstallCommand extras and path
// ---------------------------------------------------------------------------

// TestHeadroomVenvInstallCommand_UsesProxyExtrasAndVenvPython verifies the
// install command targets [proxy] extras and the venv python binary.
func TestHeadroomVenvInstallCommand_UsesProxyExtrasAndVenvPython(t *testing.T) {
	venvDir := filepath.Join("/tmp", "amagi-codebox", "headroom-venv")
	svc := NewServiceWithRunner(&mockRunner{})
	svc.SetHeadroomVenvDir(venvDir)

	t.Run("install", func(t *testing.T) {
		cmd := svc.headroomVenvInstallCommand(installOperationInstall)
		expectedPython := headroomVenvPythonPath(venvDir)
		if cmd.path != expectedPython {
			t.Errorf("install path = %q, want %q", cmd.path, expectedPython)
		}
		wantArgs := []string{"-m", "pip", "install", "headroom-ai[proxy]"}
		if strings.Join(cmd.args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Errorf("install args = %v, want %v", cmd.args, wantArgs)
		}
		if cmd.timeout != headroomPipInstallTimeout {
			t.Errorf("install timeout = %v, want %v", cmd.timeout, headroomPipInstallTimeout)
		}
	})

	t.Run("update", func(t *testing.T) {
		cmd := svc.headroomVenvInstallCommand(installOperationUpdate)
		expectedPython := headroomVenvPythonPath(venvDir)
		if cmd.path != expectedPython {
			t.Errorf("update path = %q, want %q", cmd.path, expectedPython)
		}
		wantArgs := []string{"-m", "pip", "install", "-U", "headroom-ai[proxy]"}
		if strings.Join(cmd.args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Errorf("update args = %v, want %v", cmd.args, wantArgs)
		}
	})
}
