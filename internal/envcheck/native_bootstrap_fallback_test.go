package envcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

type nativeBootstrapTestRunner struct {
	mu         sync.Mutex
	calls      []platform.CommandSpec
	nativePath string
	npmPrefix  string
	npmClaude  string

	npmProbeErr           error
	npmInstallErr         error
	npmInstallOut         string
	npmListErr            error
	claudeInstallErr      error
	claudeInstallOut      string
	claudeInstallWait     time.Duration
	createOnClaudeInstall bool
}

func (r *nativeBootstrapTestRunner) Run(ctx context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec)
	r.mu.Unlock()

	pathLower := strings.ToLower(spec.Path)
	args := append([]string(nil), spec.Args...)

	if isNPMPath(pathLower) && len(args) == 1 && args[0] == "--version" {
		if r.npmProbeErr != nil {
			return &platform.ProcessResult{Stderr: "npm token=probe-secret"}, r.npmProbeErr
		}
		return &platform.ProcessResult{Stdout: "10.0.0"}, nil
	}
	if isNPMPath(pathLower) && len(args) == 2 && args[0] == "prefix" && args[1] == "-g" {
		if strings.TrimSpace(r.npmPrefix) == "" {
			return &platform.ProcessResult{}, errors.New("npm prefix not configured")
		}
		return &platform.ProcessResult{Stdout: r.npmPrefix}, nil
	}
	if isNPMPath(pathLower) && len(args) >= 3 && args[0] == "install" && args[1] == "-g" && strings.Contains(args[2], "@anthropic-ai/claude-code") {
		stdout := r.npmInstallOut
		if stdout == "" {
			stdout = "added 1 package"
		}
		if r.npmInstallErr == nil || installerOutputHasNPMInstallSuccessEvidence(stdout) {
			if strings.TrimSpace(r.npmClaude) != "" {
				_ = writeCommandFile(r.npmClaude)
			}
		}
		return &platform.ProcessResult{Stdout: stdout}, r.npmInstallErr
	}
	if isNPMPath(pathLower) && len(args) >= 4 && args[0] == "list" && args[1] == "-g" && args[2] == "@anthropic-ai/claude-code" {
		if r.npmListErr != nil {
			return &platform.ProcessResult{Stderr: "npm list failed"}, r.npmListErr
		}
		return &platform.ProcessResult{Stdout: "@anthropic-ai/claude-code@2.1.110"}, nil
	}
	if isNPMPath(pathLower) && len(args) >= 3 && args[0] == "view" && args[1] == "@anthropic-ai/claude-code" && args[2] == "version" {
		return &platform.ProcessResult{Stdout: "2.1.110"}, nil
	}
	if strings.Contains(pathLower, "claude") && len(args) == 1 && args[0] == "install" {
		if r.claudeInstallWait > 0 {
			select {
			case <-ctx.Done():
				return &platform.ProcessResult{Stdout: "claude install still running token=bootstrap-secret"}, ctx.Err()
			case <-time.After(r.claudeInstallWait):
			}
		}
		if r.claudeInstallErr == nil || r.createOnClaudeInstall {
			_ = writeCommandFile(r.nativePath)
		}
		stdout := r.claudeInstallOut
		if stdout == "" {
			stdout = "Claude Code successfully installed\nLocation: " + r.nativePath
		}
		return &platform.ProcessResult{Stdout: stdout}, r.claudeInstallErr
	}
	if strings.Contains(pathLower, "claude") && len(args) == 1 && args[0] == "--version" {
		return &platform.ProcessResult{Stdout: "Claude Code v2.1.110"}, nil
	}
	if strings.EqualFold(filepath.Clean(spec.Path), filepath.Clean(r.nativePath)) && len(args) == 1 && args[0] == "--version" {
		return &platform.ProcessResult{Stdout: "Claude Code v2.1.110"}, nil
	}
	return &platform.ProcessResult{}, os.ErrNotExist
}

func (r *nativeBootstrapTestRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) { return nil, nil }

func (r *nativeBootstrapTestRunner) sawNPMInstall() bool {
	return r.sawCall(func(spec platform.CommandSpec) bool {
		return isNPMPath(strings.ToLower(spec.Path)) && len(spec.Args) >= 3 && spec.Args[0] == "install" && spec.Args[1] == "-g" && strings.Contains(spec.Args[2], "@anthropic-ai/claude-code")
	})
}

func (r *nativeBootstrapTestRunner) sawClaudeInstall() bool {
	return r.sawCall(func(spec platform.CommandSpec) bool {
		return strings.Contains(strings.ToLower(spec.Path), "claude") && len(spec.Args) == 1 && spec.Args[0] == "install"
	})
}

func (r *nativeBootstrapTestRunner) sawBareClaudeInstall() bool {
	return r.sawCall(func(spec platform.CommandSpec) bool {
		return spec.Path == "claude" && len(spec.Args) == 1 && spec.Args[0] == "install"
	})
}

func (r *nativeBootstrapTestRunner) claudeVersionCallPaths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	paths := []string{}
	for _, call := range r.calls {
		if len(call.Args) == 1 && call.Args[0] == "--version" && strings.Contains(strings.ToLower(call.Path), "claude") {
			paths = append(paths, call.Path)
		}
	}
	return paths
}

func (r *nativeBootstrapTestRunner) sawCall(match func(platform.CommandSpec) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if match(call) {
			return true
		}
	}
	return false
}

func prepareNativeBootstrapTest(t *testing.T, runner *nativeBootstrapTestRunner) *Service {
	t.Helper()
	tmpDir := t.TempDir()
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	binDir := filepath.Join(tmpDir, "bin")
	npmPrefix := filepath.Join(tmpDir, "npm-global")
	for _, dir := range []string{homeDir, nativeDir, binDir, filepath.Join(npmPrefix, "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestExecutable(t, binDir, "npm")
	writeTestExecutable(t, binDir, "node")
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("APPDATA", filepath.Join(tmpDir, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(tmpDir, "AppData", "Local"))
	t.Setenv("NPM_CONFIG_PREFIX", npmPrefix)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)
	previousHomeDir := claudeUserHomeDir
	claudeUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { claudeUserHomeDir = previousHomeDir })
	runner.nativePath = filepath.Join(nativeDir, commandFileName("claude"))
	runner.npmPrefix = npmPrefix
	runner.npmClaude = filepath.Join(npmPrefix, "bin", commandFileName("claude"))
	return NewServiceWithRunner(runner)
}

func commandFileName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func writeCommandFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		content = "@echo off\r\nexit /b 0\r\n"
	}
	return os.WriteFile(path, []byte(content), 0o755)
}

func argsContain(args []string, needle string) bool {
	for _, arg := range args {
		if strings.Contains(arg, needle) {
			return true
		}
	}
	return false
}

func isNPMPath(pathLower string) bool {
	base := strings.ToLower(filepath.Base(pathLower))
	return strings.Contains(pathLower, "npm") || base == "npm" || base == "npm.cmd" || base == "npm.exe"
}

func TestClaudeNativeInstallRunsNPMThenClaudeInstall(t *testing.T) {
	runner := &nativeBootstrapTestRunner{}
	svc := prepareNativeBootstrapTest(t, runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, reporter)
	if err != nil {
		t.Fatalf("native install error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful native result, got %+v", result)
	}
	if !runner.sawNPMInstall() {
		t.Fatal("expected native install to execute npm install first")
	}
	if !runner.sawClaudeInstall() {
		t.Fatal("expected native install to execute claude install")
	}
	if runner.sawBareClaudeInstall() {
		t.Fatalf("claude install must resolve the npm global shim instead of relying on bare PATH; calls=%+v", runner.calls)
	}
	for _, want := range []string{"npm + claude install", "安装 npm 版本", "claude install", "验证 Native"} {
		if !snapshotsContainMessage(*snapshotsPtr, want) {
			t.Fatalf("expected progress message containing %q; snapshots=%+v", want, *snapshotsPtr)
		}
	}
}

func TestClaudeNativeInstallSkipsBootstrapWhenNativeAlreadyAvailable(t *testing.T) {
	runner := &nativeBootstrapTestRunner{}
	svc := prepareNativeBootstrapTest(t, runner)
	if err := writeCommandFile(runner.nativePath); err != nil {
		t.Fatal(err)
	}

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected existing native install to short-circuit successfully, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful result, got %+v", result)
	}
	if runner.sawNPMInstall() || runner.sawClaudeInstall() {
		t.Fatalf("existing native install must not rerun npm install or claude install; calls=%+v", runner.calls)
	}
	if !strings.Contains(result.Message, "已安装可用") {
		t.Fatalf("expected installed-available message, got %q", result.Message)
	}
}

func TestClaudeNativeBootstrapUsesDarwinShimPathInsteadOfExeSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics are platform-specific")
	}
	forceRuntimeGOOSForTest(t, "darwin")
	prefix := t.TempDir()
	shimPath := filepath.Join(prefix, "bin", "claude")
	packageExe := filepath.Join(prefix, "lib", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	if err := writeCommandFile(packageExe); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(shimPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(packageExe, shimPath); err != nil {
		t.Fatalf("create npm shim symlink: %v", err)
	}
	runner := &nativeBootstrapTestRunner{npmPrefix: prefix}
	svc := NewServiceWithRunner(runner)

	cmd, err := svc.claudeNativeBootstrapCommandAfterNPMInstall()
	if err != nil {
		t.Fatalf("resolve bootstrap command: %v", err)
	}
	if filepath.Clean(cmd.path) != filepath.Clean(shimPath) {
		t.Fatalf("bootstrap path = %q, want npm shim %q rather than resolved target %q", cmd.path, shimPath, packageExe)
	}
	if strings.HasSuffix(strings.ToLower(cmd.path), ".exe") {
		t.Fatalf("darwin bootstrap command must not prefer .exe path: %q", cmd.path)
	}
}

func TestClaudeNativeBootstrapResolverFallbackKeepsDarwinShimInsteadOfExeRealpath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics are platform-specific")
	}
	forceRuntimeGOOSForTest(t, "darwin")
	tmpDir := t.TempDir()
	prefix := filepath.Join(tmpDir, "empty-npm-prefix")
	shimDir := filepath.Join(tmpDir, "path-bin")
	shimPath := filepath.Join(shimDir, "claude")
	packageExe := filepath.Join(tmpDir, "lib", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandFile(packageExe); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(packageExe, shimPath); err != nil {
		t.Fatalf("create npm shim symlink: %v", err)
	}
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmpDir, "home"))
	t.Setenv("PATH", shimDir)
	t.Setenv("Path", shimDir)
	runner := &nativeBootstrapTestRunner{npmPrefix: prefix}
	svc := NewServiceWithRunner(runner)

	cmd, err := svc.claudeNativeBootstrapCommandAfterNPMInstall()
	if err != nil {
		t.Fatalf("resolve bootstrap command through resolver fallback: %v", err)
	}
	if filepath.Clean(cmd.path) != filepath.Clean(shimPath) {
		t.Fatalf("bootstrap resolver fallback path = %q, want npm shim %q rather than realpath %q", cmd.path, shimPath, packageExe)
	}
	if strings.HasSuffix(strings.ToLower(cmd.path), ".exe") {
		t.Fatalf("darwin bootstrap resolver fallback must not return .exe path: %q", cmd.path)
	}
}

func TestClaudeCheckOneDarwinNPMShimDoesNotDisplayOrExecuteExeRealpath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics are platform-specific")
	}
	forceRuntimeGOOSForTest(t, "darwin")
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	binDir := filepath.Join(tmpDir, "bin")
	shimPath := filepath.Join(binDir, "claude")
	packageExe := filepath.Join(tmpDir, "lib", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	for _, dir := range []string{homeDir, binDir, filepath.Dir(packageExe)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestExecutable(t, binDir, "npm")
	if err := writeCommandFile(packageExe); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(packageExe, shimPath); err != nil {
		t.Fatalf("create npm shim symlink: %v", err)
	}
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)
	runner := &nativeBootstrapTestRunner{npmPrefix: tmpDir, npmClaude: shimPath}
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckOne returned error: %v", err)
	}
	if status == nil || !status.Installed || status.InstallMethod != InstallMethodNPM {
		t.Fatalf("expected npm installation through shim, got %+v", status)
	}
	if filepath.Clean(status.ExecutablePath) != filepath.Clean(shimPath) {
		t.Fatalf("ExecutablePath = %q, want display/invocation shim %q rather than .exe realpath %q", status.ExecutablePath, shimPath, packageExe)
	}
	if strings.HasSuffix(strings.ToLower(status.ExecutablePath), ".exe") {
		t.Fatalf("darwin CheckOne must not display .exe path: %q", status.ExecutablePath)
	}
	versionPaths := runner.claudeVersionCallPaths()
	if len(versionPaths) == 0 {
		t.Fatalf("expected claude --version call, calls=%+v", runner.calls)
	}
	for _, path := range versionPaths {
		if filepath.Clean(path) == filepath.Clean(packageExe) || strings.HasSuffix(strings.ToLower(path), ".exe") {
			t.Fatalf("darwin CheckOne must execute shim for --version, not .exe realpath; version paths=%+v", versionPaths)
		}
	}
}

func TestClaudeNativeBootstrapExitOneWithSuccessLocationVerifiesNative(t *testing.T) {
	runner := &nativeBootstrapTestRunner{
		claudeInstallErr:      errors.New("exit status 1"),
		createOnClaudeInstall: true,
	}
	svc := prepareNativeBootstrapTest(t, runner)
	runner.claudeInstallOut = "Checking installation status...\nClaude Code successfully installed\nVersion: 2.1.132\nLocation: " + runner.nativePath + "\n"

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected success from Location despite exit status 1, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful result, got %+v", result)
	}
	if !strings.Contains(result.Message, "shell integration 返回非零状态") {
		t.Fatalf("expected shell integration diagnostic in success message: %s", result.Message)
	}
}

func TestClaudeNativeBootstrapLatestTimeoutSucceedsWhenNativeBecomesAvailable(t *testing.T) {
	runner := &nativeBootstrapTestRunner{
		claudeInstallErr:      errors.New("exit status 1"),
		createOnClaudeInstall: true,
		claudeInstallOut: "Checking installation status...\nInstalling Claude Code native build latest...\n✘ Installation failed\n" +
			"Failed to fetch version from https://downloads.claude.ai/claude-code-releases/latest: timeout of 30000ms exceeded\n" +
			"Try running with --force to override checks",
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected native availability fallback to succeed, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful result, got %+v", result)
	}
	if !strings.Contains(result.Message, "已验证可用") {
		t.Fatalf("expected verified-available fallback message, got %q", result.Message)
	}
}

func TestClaudeNativeBootstrapLatestTimeoutFailureIncludesNetworkProxyForceAdvice(t *testing.T) {
	runner := &nativeBootstrapTestRunner{
		claudeInstallErr: errors.New("exit status 1"),
		claudeInstallOut: "Checking installation status...\nInstalling Claude Code native build latest...\n✘ Installation failed\n" +
			"Failed to fetch version from https://downloads.claude.ai/claude-code-releases/latest: timeout of 30000ms exceeded\n" +
			"Try running with --force to override checks",
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err == nil {
		t.Fatal("expected timeout failure without usable native binary")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	for _, want := range []string{"npm package @anthropic-ai/claude-code 已确认安装", "失败发生在 claude install / downloads latest 检查阶段", "downloads.claude.ai", "timeout of 30000ms exceeded", "timeout 20m0s", "HTTP_PROXY", "HTTPS_PROXY", "claude install --force", "重试 Native bootstrap"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("failure message missing %q: %s", want, result.Message)
		}
	}
}

func TestClaudeCheckOneFindsNativeDefaultWhenSystemPATHMissing(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, commandFileName("claude"))
	if err := writeCommandFile(nativePath); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)
	runner := &nativeBootstrapTestRunner{nativePath: nativePath}
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckOne returned error: %v", err)
	}
	if status == nil || !status.Installed || status.InstallMethod != InstallMethodNative {
		t.Fatalf("expected native installation from default path, got %+v", status)
	}
}

func TestClaudeCheckOneFindsNativeDefaultViaUserHomeFallbackWhenEnvHomeMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS HOME fallback scenario")
	}
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, commandFileName("claude"))
	if err := writeCommandFile(nativePath); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	previousHomeDir := claudeUserHomeDir
	claudeUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { claudeUserHomeDir = previousHomeDir })
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOME", "")
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)
	runner := &nativeBootstrapTestRunner{nativePath: nativePath}
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckOne returned error: %v", err)
	}
	if status == nil || !status.Installed || status.InstallMethod != InstallMethodNative {
		t.Fatalf("expected native installation from user home fallback, got %+v", status)
	}
	if !sameNormalizedPath(status.ExecutablePath, nativePath) {
		t.Fatalf("executable path = %q, want %q", status.ExecutablePath, nativePath)
	}
}

func TestClaudeCheckOnePrefersNativeDefaultOverNPMShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS native default precedence scenario")
	}
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, commandFileName("claude"))
	if err := writeCommandFile(nativePath); err != nil {
		t.Fatal(err)
	}
	npmDir := filepath.Join(tmpDir, "npm", "bin")
	npmShim := filepath.Join(npmDir, commandFileName("claude"))
	if err := writeCommandFile(npmShim); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", npmDir)
	t.Setenv("Path", npmDir)
	runner := &nativeBootstrapTestRunner{nativePath: nativePath, npmClaude: npmShim}
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckOne returned error: %v", err)
	}
	if status == nil || !status.Installed {
		t.Fatalf("expected installed status, got %+v", status)
	}
	if status.InstallMethod != InstallMethodNative {
		t.Fatalf("install method = %q, want %q; status=%+v", status.InstallMethod, InstallMethodNative, status)
	}
	if !sameNormalizedPath(status.ExecutablePath, nativePath) {
		t.Fatalf("executable path = %q, want native default %q instead of npm shim %q", status.ExecutablePath, nativePath, npmShim)
	}
}

func TestClaudeCheckOneFindsNPMGlobalWhenProcessPATHMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows PATH stale scenario")
	}
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	binDir := filepath.Join(tmpDir, "bin")
	npmPrefix := filepath.Join(tmpDir, "npm-global")
	npmClaude := filepath.Join(npmPrefix, "bin", commandFileName("claude"))
	for _, dir := range []string{homeDir, binDir, filepath.Dir(npmClaude)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestExecutable(t, binDir, "npm")
	writeTestExecutable(t, binDir, "node")
	if err := writeCommandFile(npmClaude); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)
	runner := &nativeBootstrapTestRunner{npmPrefix: npmPrefix, npmClaude: npmClaude}
	svc := NewServiceWithRunner(runner)

	status, err := svc.CheckOne(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckOne returned error: %v", err)
	}
	if status == nil || !status.Installed || status.InstallMethod != InstallMethodNPM {
		t.Fatalf("expected npm status, got %+v", status)
	}
}

func TestClaudeNativeBootstrapTrueFailureKeepsDiagnosticsRedacted(t *testing.T) {
	runner := &nativeBootstrapTestRunner{claudeInstallErr: errors.New("exit status 1 token=fallback-secret-value-123456")}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err == nil {
		t.Fatal("expected native bootstrap failure")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	if strings.Contains(result.Message, "fallback-secret-value-123456") || strings.Contains(result.Error, "fallback-secret-value-123456") {
		t.Fatalf("failure diagnostics leaked sensitive value: result=%+v", result)
	}
}

func snapshotsContainMessage(snapshots []progressSnapshot, want string) bool {
	for _, snapshot := range snapshots {
		if strings.Contains(snapshot.message, want) {
			return true
		}
	}
	return false
}
