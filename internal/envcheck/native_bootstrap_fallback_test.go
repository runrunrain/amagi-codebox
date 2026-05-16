package envcheck

import (
	"context"
	"errors"
	"fmt"
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

	directErr               error
	directStdout            string
	directWait              time.Duration
	npmProbeErr             error
	npmInstallErr           error
	npmInstallOut           string
	npmListErr              error
	claudeInstallErr        error
	claudeInstallOut        string
	claudeInstallWait       time.Duration
	createOnDirect          bool
	createOnEvidenceTimeout bool
	createOnClaudeInstall   bool
	directEvidenceDelay     time.Duration
	directNoOutput          bool
}

func (r *nativeBootstrapTestRunner) Run(ctx context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec)
	r.mu.Unlock()

	pathLower := strings.ToLower(spec.Path)
	args := append([]string(nil), spec.Args...)

	if strings.Contains(pathLower, "powershell") && argsContain(args, "install.ps1") {
		if r.directWait > 0 {
			select {
			case <-ctx.Done():
				return &platform.ProcessResult{Stdout: "native direct still running"}, ctx.Err()
			case <-time.After(r.directWait):
			}
		}
		if r.directErr == nil && r.createOnDirect {
			_ = writeCommandFile(r.nativePath)
		}
		stdout := r.directStdout
		if stdout == "" {
			stdout = "native direct installer"
		}
		return &platform.ProcessResult{Stdout: stdout}, r.directErr
	}

	if filepath.Clean(spec.Path) == filepath.Clean("/bin/sh") && len(args) == 2 && args[0] == "-c" && strings.Contains(args[1], "https://claude.ai/install.sh") {
		if r.directErr == nil && r.createOnDirect {
			_ = writeCommandFile(r.nativePath)
		}
		stdout := r.directStdout
		if stdout == "" {
			stdout = "Claude Code successfully installed\nLocation: " + r.nativePath
		}
		return &platform.ProcessResult{Stdout: stdout}, r.directErr
	}

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
			stdout = "npm install claude"
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
			stdout = "claude install native"
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

func (r *nativeBootstrapTestRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func (r *nativeBootstrapTestRunner) RunWithEvidence(ctx context.Context, spec platform.CommandSpec, evidenceTimeout time.Duration, onOutput func(platform.ProcessOutputEvent)) (*platform.EvidenceRunResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec)
	r.mu.Unlock()

	pathLower := strings.ToLower(spec.Path)
	args := append([]string(nil), spec.Args...)
	if !strings.Contains(pathLower, "powershell") || !argsContain(args, "install.ps1") {
		result, err := r.Run(ctx, spec)
		return &platform.EvidenceRunResult{Result: result, EvidenceObserved: resultText(result) != "" || err == nil}, err
	}

	stdout := r.directStdout
	if stdout == "" && !r.directNoOutput {
		stdout = "native direct installer"
	}
	evidenceDelay := r.directEvidenceDelay
	if evidenceDelay <= 0 {
		evidenceDelay = r.directWait
	}
	if evidenceDelay <= 0 {
		evidenceDelay = time.Millisecond
	}

	if stdout != "" {
		select {
		case <-ctx.Done():
			return &platform.EvidenceRunResult{Result: &platform.ProcessResult{}, EvidenceObserved: false}, ctx.Err()
		case <-time.After(evidenceDelay):
		}
		if onOutput != nil {
			onOutput(platform.ProcessOutputEvent{Stream: "stdout", Data: stdout, At: time.Now()})
		}
		remaining := r.directWait - evidenceDelay
		if remaining > 0 {
			select {
			case <-ctx.Done():
				return &platform.EvidenceRunResult{Result: &platform.ProcessResult{Stdout: stdout}, EvidenceObserved: true}, ctx.Err()
			case <-time.After(remaining):
			}
		}
		if r.directErr == nil && r.createOnDirect {
			_ = writeCommandFile(r.nativePath)
		}
		return &platform.EvidenceRunResult{Result: &platform.ProcessResult{Stdout: stdout}, EvidenceObserved: true}, r.directErr
	}

	wait := r.directWait
	if wait <= 0 {
		wait = evidenceTimeout + time.Millisecond
	}
	if wait > evidenceTimeout {
		select {
		case <-ctx.Done():
			return &platform.EvidenceRunResult{Result: &platform.ProcessResult{}, EvidenceObserved: false}, ctx.Err()
		case <-time.After(evidenceTimeout):
			if r.createOnEvidenceTimeout {
				_ = writeCommandFile(r.nativePath)
			}
			return &platform.EvidenceRunResult{Result: &platform.ProcessResult{}, EvidenceObserved: false, EvidenceTimedOut: true}, context.DeadlineExceeded
		}
	}
	select {
	case <-ctx.Done():
		return &platform.EvidenceRunResult{Result: &platform.ProcessResult{}, EvidenceObserved: false}, ctx.Err()
	case <-time.After(wait):
	}
	if r.directErr == nil && r.createOnDirect {
		_ = writeCommandFile(r.nativePath)
	}
	return &platform.EvidenceRunResult{Result: &platform.ProcessResult{}, EvidenceObserved: r.directErr == nil}, r.directErr
}

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
	forceNativeDirectInstallerSupportedForTest(t)
	tmpDir, err := os.MkdirTemp("", "acb-claude-native-")
	if err != nil {
		t.Fatal(err)
	}
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	nativeDir := filepath.Join(tmpDir, "home", ".local", "bin")
	if err := os.MkdirAll(nativeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	npmPrefix := filepath.Join(tmpDir, "npm-global")
	if err := os.MkdirAll(filepath.Join(npmPrefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, filepath.Join(tmpDir, "bin"), "npm")
	writeTestExecutable(t, filepath.Join(tmpDir, "bin"), "node")
	writeTestExecutable(t, filepath.Join(tmpDir, "bin"), "claude")
	if err := writeCommandFile(filepath.Join(tmpDir, "bin", "powershell.exe")); err != nil {
		t.Fatal(err)
	}

	testHome := filepath.Join(tmpDir, "home")
	t.Setenv("USERPROFILE", testHome)
	t.Setenv("HOME", testHome)
	testPATH := strings.Join([]string{nativeDir, filepath.Join(tmpDir, "bin")}, string(os.PathListSeparator))
	t.Setenv("PATH", testPATH)
	t.Setenv("Path", testPATH)
	runner.nativePath = filepath.Join(nativeDir, commandFileName("claude"))
	runner.npmPrefix = npmPrefix
	runner.npmClaude = filepath.Join(npmPrefix, "bin", commandFileName("claude"))
	return NewServiceWithRunner(runner)
}

func forceNativeDirectInstallerSupportedForTest(t *testing.T) {
	t.Helper()
	previous := nativeDirectInstallerSupported
	previousGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	nativeDirectInstallerSupported = func() bool { return true }
	t.Cleanup(func() {
		nativeDirectInstallerSupported = previous
		runtimeGOOS = previousGOOS
	})
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

func TestClaudeNativeDarwinRunsOfficialInstallSHWithoutNPMBootstrap(t *testing.T) {
	forceRuntimeGOOSForTest(t, "darwin")
	tmpDir, err := os.MkdirTemp("", "acb-native-darwin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	binDir := filepath.Join(tmpDir, "bin")
	for _, dir := range []string{homeDir, nativeDir, binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)

	runner := &nativeBootstrapTestRunner{
		nativePath:     filepath.Join(nativeDir, commandFileName("claude")),
		createOnDirect: true,
	}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installClaudeNativeUnixOfficial(nil)
	if err != nil {
		t.Fatalf("expected official install.sh native install success, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful native install.sh result, got %+v", result)
	}
	if !runner.sawCall(func(spec platform.CommandSpec) bool {
		return filepath.Clean(spec.Path) == filepath.Clean("/bin/sh") && len(spec.Args) == 2 && spec.Args[0] == "-c" && spec.Args[1] == claudeNativeUnixInstallScriptCommand
	}) {
		t.Fatalf("expected darwin native install to run official install.sh via /bin/sh; calls=%+v", runner.calls)
	}
	if runner.sawNPMInstall() || runner.sawClaudeInstall() {
		t.Fatalf("darwin native install must not run npm bootstrap or claude install; calls=%+v", runner.calls)
	}
	if runner.sawCall(func(spec platform.CommandSpec) bool {
		return strings.Contains(strings.ToLower(spec.Path), "powershell") ||
			(strings.Contains(strings.ToLower(spec.Path), "claude.exe") && len(spec.Args) == 1 && spec.Args[0] == "install")
	}) {
		t.Fatalf("darwin native install must not run PowerShell or claude.exe install; calls=%+v", runner.calls)
	}
}

func TestClaudeNativeDirectFailureRunsNPMBootstrapFallback(t *testing.T) {
	runner := &nativeBootstrapTestRunner{directErr: errors.New("direct failed")}
	svc := prepareNativeBootstrapTest(t, runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, reporter)

	if err != nil {
		t.Fatalf("native fallback install error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful fallback result, got %+v", result)
	}
	if !runner.sawNPMInstall() {
		t.Fatal("expected fallback to execute npm install for Claude Code")
	}
	if !runner.sawClaudeInstall() {
		t.Fatal("expected fallback to execute claude install")
	}

	snapshots := *snapshotsPtr
	for _, want := range []string{"直接安装失败，切换保底方案", "安装 npm 版本", "claude install", "验证 Native"} {
		if !snapshotsContainMessage(snapshots, want) {
			t.Fatalf("expected progress message containing %q; snapshots=%+v", want, snapshots)
		}
	}
}

func TestClaudeNativeMissingPowerShellSkipsDirectAndUsesNPMGlobalClaude(t *testing.T) {
	forceNativeDirectInstallerSupportedForTest(t)
	tmpDir, err := os.MkdirTemp("", "acb-claude-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	binDir := filepath.Join(tmpDir, "bin")
	npmPrefix := filepath.Join(tmpDir, "npm-global")
	for _, dir := range []string{nativeDir, binDir, filepath.Join(npmPrefix, "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestExecutable(t, binDir, "npm")
	writeTestExecutable(t, binDir, "node")
	writeTestExecutable(t, binDir, "claude")
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)

	runner := &nativeBootstrapTestRunner{
		nativePath: filepath.Join(nativeDir, commandFileName("claude")),
		npmPrefix:  npmPrefix,
		npmClaude:  filepath.Join(npmPrefix, "bin", commandFileName("claude")),
	}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected npm bootstrap to recover when powershell is missing, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful fallback result, got %+v", result)
	}
	if runner.sawCall(func(spec platform.CommandSpec) bool {
		return strings.Contains(strings.ToLower(spec.Path), "powershell")
	}) {
		t.Fatalf("missing powershell direct installer must be skipped before execution; calls=%+v", runner.calls)
	}
	if !runner.sawNPMInstall() {
		t.Fatal("expected fallback to install npm Claude Code package before bootstrap")
	}
	if runner.sawBareClaudeInstall() {
		t.Fatalf("bootstrap must not rely on stale PATH bare claude after npm install; calls=%+v", runner.calls)
	}
	if !runner.sawCall(func(spec platform.CommandSpec) bool {
		return filepath.Clean(spec.Path) == filepath.Clean(runner.npmClaude) && len(spec.Args) == 1 && spec.Args[0] == "install"
	}) {
		t.Fatalf("expected claude install to use npm global prefix CLI %q; calls=%+v", runner.npmClaude, runner.calls)
	}
}

func TestClaudeNativeDirectNoEvidenceCancelsAndRunsNPMBootstrapFallback(t *testing.T) {
	originalTimeout := nativeDirectEvidenceTimeout
	nativeDirectEvidenceTimeout = 20 * time.Millisecond
	t.Cleanup(func() { nativeDirectEvidenceTimeout = originalTimeout })

	runner := &nativeBootstrapTestRunner{
		directNoOutput: true,
		directWait:     time.Second,
	}
	svc := prepareNativeBootstrapTest(t, runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, reporter)

	if err != nil {
		t.Fatalf("expected gate fallback success, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful gate fallback result, got %+v", result)
	}
	if !runner.sawNPMInstall() {
		t.Fatal("expected no-evidence direct installer to fall back to npm install")
	}
	if !runner.sawClaudeInstall() {
		t.Fatal("expected no-evidence direct installer to run claude install fallback")
	}

	snapshots := *snapshotsPtr
	for _, want := range []string{"Native 官方安装模式", "等待安装器响应/下载开始", "30 秒内未检测到响应", "保底安装模式", "安装 npm 版本", "claude install", "验证 Native"} {
		if !snapshotsContainMessage(snapshots, want) {
			t.Fatalf("expected progress message containing %q; snapshots=%+v", want, snapshots)
		}
	}
}

func TestClaudeNativeDirectNoEvidenceTimeoutRecheckSuccess(t *testing.T) {
	originalTimeout := nativeDirectEvidenceTimeout
	originalAttempts := installRecheckAttempts
	originalDelay := installRecheckDelay
	nativeDirectEvidenceTimeout = 20 * time.Millisecond
	installRecheckAttempts = 2
	installRecheckDelay = time.Millisecond
	t.Cleanup(func() {
		nativeDirectEvidenceTimeout = originalTimeout
		installRecheckAttempts = originalAttempts
		installRecheckDelay = originalDelay
	})

	runner := &nativeBootstrapTestRunner{
		directNoOutput:          true,
		directWait:              time.Second,
		createOnEvidenceTimeout: true,
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected bounded recheck success after direct evidence timeout, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful result after bounded recheck, got %+v", result)
	}
	if runner.sawNPMInstall() || runner.sawClaudeInstall() {
		t.Fatalf("bounded recheck success must not run fallback; calls=%+v", runner.calls)
	}
	if !strings.Contains(result.Message, "bounded recheck") {
		t.Fatalf("expected success message to preserve recheck context: %s", result.Message)
	}
}

func TestClaudeNativeDirectEvidenceWithinGateContinuesDirectWithoutFallback(t *testing.T) {
	originalTimeout := nativeDirectEvidenceTimeout
	nativeDirectEvidenceTimeout = 80 * time.Millisecond
	t.Cleanup(func() { nativeDirectEvidenceTimeout = originalTimeout })

	runner := &nativeBootstrapTestRunner{
		directStdout:        "Downloading Claude Code native package...",
		directEvidenceDelay: 10 * time.Millisecond,
		directWait:          40 * time.Millisecond,
		createOnDirect:      true,
	}
	svc := prepareNativeBootstrapTest(t, runner)

	snapshotsPtr, reporter := collectProgressSnapshots()
	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, reporter)

	if err != nil {
		t.Fatalf("expected direct native install success after evidence, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected direct native success, got %+v", result)
	}
	if runner.sawNPMInstall() || runner.sawClaudeInstall() {
		t.Fatalf("direct evidence path must not run fallback; calls=%+v", runner.calls)
	}
	if !snapshotsContainMessage(*snapshotsPtr, "已检测到安装器响应") {
		t.Fatalf("expected progress to show observed installer response; snapshots=%+v", *snapshotsPtr)
	}
}

func TestClaudeNativeDirectSuccessDoesNotRunFallback(t *testing.T) {
	runner := &nativeBootstrapTestRunner{createOnDirect: true}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("direct native install error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected direct install success, got %+v", result)
	}
	if runner.sawNPMInstall() {
		t.Fatal("direct success must not execute npm fallback install")
	}
	if runner.sawClaudeInstall() {
		t.Fatal("direct success must not execute claude install fallback")
	}
}

func TestClaudeNativeBootstrapExitOneWithSuccessLocationVerifiesNative(t *testing.T) {
	runner := &nativeBootstrapTestRunner{
		directErr:             errors.New("direct failed"),
		claudeInstallErr:      errors.New("exit status 1"),
		createOnClaudeInstall: true,
	}
	svc := prepareNativeBootstrapTest(t, runner)
	runner.claudeInstallOut = "Checking installation status...\nInstalling Claude Code native build latest...\nSetting up launcher and shell integration...\n√ Claude Code successfully installed!\nVersion: 2.1.132\nLocation: " + runner.nativePath + "\nNext: Run claude --help to get started\n"

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected fallback success from Location despite exit status 1, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful fallback result, got %+v", result)
	}
	if !strings.Contains(result.Message, "shell integration 返回非零状态") {
		t.Fatalf("expected success message to distinguish post-install shell integration failure: %s", result.Message)
	}
	if strings.Contains(result.Message, "保底方案也未完成") {
		t.Fatalf("success message must not report fallback incomplete: %s", result.Message)
	}
}

func TestClaudeCheckOneFindsNativeDefaultWhenSystemPATHMissing(t *testing.T) {
	tmpDir := t.TempDir()
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, commandFileName("claude"))
	if err := writeCommandFile(nativePath); err != nil {
		t.Fatalf("write native executable: %v", err)
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
	if !status.PATHOk || status.SystemPATHOk {
		t.Fatalf("expected CodeBox-visible but not process-PATH-visible native path, got PATHOk=%v SystemPATHOk=%v state=%s", status.PATHOk, status.SystemPATHOk, status.PathState)
	}
	if status.ExecutablePath == "" || !strings.Contains(strings.ToLower(status.ExecutablePath), strings.ToLower(filepath.Join(".local", "bin"))) {
		t.Fatalf("expected executable path under default native dir, got %+v", status)
	}
}

func TestClaudeCheckOneFindsNPMGlobalWhenProcessPATHMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows PATH stale scenario")
	}
	tmpDir := t.TempDir()
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
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
	if got := svc.firstExistingClaudeNPMGlobalPath(); filepath.Clean(got) != filepath.Clean(npmClaude) {
		t.Fatalf("expected npm global fallback path %q, got %q", npmClaude, got)
	}

	status, err := svc.CheckOne(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckOne returned error: %v", err)
	}
	if status == nil || !status.Installed || !status.PATHOk {
		t.Fatalf("expected npm global claude to be usable through enhanced resolver, got %+v", status)
	}
	if status.SystemPATHOk {
		t.Fatalf("expected current process PATH not to contain npm global claude, got %+v", status)
	}
	if status.InstallMethod != InstallMethodNPM {
		t.Fatalf("expected NPM status for npm global or resolver-visible Claude, got method=%s status=%+v", status.InstallMethod, status)
	}
}

func TestFixPathRefreshesCachedClaudeStatusAfterNativeDefaultAdded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell profile PATH repair is non-Windows")
	}
	tmpDir := t.TempDir()
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	nativePath := filepath.Join(nativeDir, commandFileName("claude"))
	binDir := filepath.Join(tmpDir, "bin")
	for _, dir := range []string{homeDir, nativeDir, binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)

	runner := &nativeBootstrapTestRunner{nativePath: nativePath}
	svc := NewServiceWithRunner(runner)
	_, _ = svc.CheckOne(ToolClaudeCode)
	if err := writeCommandFile(nativePath); err != nil {
		t.Fatal(err)
	}

	result, err := svc.RunFixAction(FixActionRequest{Action: SolutionFixPath, Tool: ToolClaudeCode})
	if err != nil {
		t.Fatalf("RunFixAction error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful fix_path, got %+v", result)
	}
	profileBytes, readErr := os.ReadFile(filepath.Join(homeDir, ".zprofile"))
	if readErr != nil {
		t.Fatalf("read profile: %v", readErr)
	}
	if !strings.Contains(string(profileBytes), nativeDir) {
		t.Fatalf("profile should include native default dir %q, content=%s", nativeDir, string(profileBytes))
	}
	cached := svc.GetCachedStatus()
	claude := cached.Items[string(ToolClaudeCode)]
	if !claude.Installed || !claude.PATHOk {
		t.Fatalf("expected cached Claude status refreshed as CodeBox-usable, got %+v", claude)
	}
}

func TestClaudeNativeDirectSuccessUsesDefaultLocationWhenPATHNotRefreshed(t *testing.T) {
	forceNativeDirectInstallerSupportedForTest(t)
	tmpDir := t.TempDir()
	if realTmpDir, err := filepath.EvalSymlinks(tmpDir); err == nil && strings.TrimSpace(realTmpDir) != "" {
		tmpDir = realTmpDir
	}
	homeDir := filepath.Join(tmpDir, "home")
	nativeDir := filepath.Join(homeDir, ".local", "bin")
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, binDir, "claude")
	if err := writeCommandFile(filepath.Join(binDir, "powershell.exe")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("PATH", binDir)
	t.Setenv("Path", binDir)
	runner := &nativeBootstrapTestRunner{
		nativePath:     filepath.Join(nativeDir, commandFileName("claude")),
		createOnDirect: true,
		directStdout:   "√ Claude Code successfully installed!\nLocation: " + filepath.Join(nativeDir, commandFileName("claude")),
	}
	svc := NewServiceWithRunner(runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected direct native success despite stale PATH: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected direct install success, got %+v", result)
	}
	if runner.sawNPMInstall() || runner.sawClaudeInstall() {
		t.Fatalf("direct success with verified default Location must not run fallback; calls=%+v", runner.calls)
	}
	if !strings.Contains(result.Message, "当前进程 PATH 尚未包含") {
		t.Fatalf("expected stale PATH hint in success message: %s", result.Message)
	}
}

func TestClaudeNativeBootstrapTrueFailureKeepsDiagnosticsRedacted(t *testing.T) {
	runner := &nativeBootstrapTestRunner{
		directErr:        errors.New("direct failed api_key=direct-secret-value-123456"),
		claudeInstallErr: errors.New("exit status 1 token=fallback-secret-value-123456"),
		claudeInstallOut: "Installing Claude Code native build latest...\nfailed before success token=output-secret-value-123456",
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err == nil {
		t.Fatal("expected true fallback failure")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	combined := result.Message + "\n" + result.Error
	for _, want := range []string{"Direct:", "Fallback:", "执行 claude install 失败"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected combined diagnostic to contain %q: %s", want, combined)
		}
	}
	for _, leaked := range []string{"direct-secret-value-123456", "fallback-secret-value-123456", "output-secret-value-123456"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("diagnostic leaked sensitive value %q: %s", leaked, combined)
		}
	}
}

func TestClaudeNativeGateFallbackFailureMentionsModesAndRedactsDiagnostics(t *testing.T) {
	originalTimeout := nativeDirectEvidenceTimeout
	nativeDirectEvidenceTimeout = 20 * time.Millisecond
	t.Cleanup(func() { nativeDirectEvidenceTimeout = originalTimeout })

	runner := &nativeBootstrapTestRunner{
		directNoOutput:   true,
		directWait:       time.Second,
		claudeInstallErr: errors.New("exit status 1 token=fallback-secret-value-123456"),
		claudeInstallOut: "bootstrap failed api_key=bootstrap-output-secret-123456",
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err == nil {
		t.Fatal("expected fallback failure")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	combined := result.Message + "\n" + result.Error
	for _, want := range []string{"Native 官方安装模式", "保底安装模式", "Direct:", "Fallback:", "未检测到 stdout/stderr"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected combined diagnostic to contain %q: %s", want, combined)
		}
	}
	for _, leaked := range []string{"fallback-secret-value-123456", "bootstrap-output-secret-123456"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("diagnostic leaked sensitive value %q: %s", leaked, combined)
		}
	}
}

func TestClaudeNativeFallbackNPMUnavailableKeepsRedactedDiagnostics(t *testing.T) {
	runner := &nativeBootstrapTestRunner{
		directErr:    errors.New("exit status 1"),
		directStdout: "download failed api_key=direct-secret-value-123456",
	}
	svc := prepareNativeBootstrapTest(t, runner)
	svc.npmOnce.Do(func() {
		svc.npmAvailable = false
		svc.npmResolvedErr = fmt.Errorf("npm failed token=fallback-secret-value-123456")
	})

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err == nil {
		t.Fatal("expected fallback failure when npm is unavailable")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	combined := result.Message + "\n" + result.Error
	for _, want := range []string{"Direct:", "Fallback:", "npm 不可用"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected combined diagnostic to contain %q: %s", want, combined)
		}
	}
	for _, leaked := range []string{"direct-secret-value-123456", "fallback-secret-value-123456"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("diagnostic leaked sensitive value %q: %s", leaked, combined)
		}
	}
	if !strings.Contains(combined, "[redacted]") {
		t.Fatalf("expected redacted marker in diagnostics: %s", combined)
	}
}

func TestClaudeNativeFallbackNPMInstallFailureKeepsDirectAndFallbackDiagnostics(t *testing.T) {
	runner := &nativeBootstrapTestRunner{
		directErr:     errors.New("direct native failed"),
		npmInstallErr: errors.New("npm install failed api_key=npm-secret-value-123456"),
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err == nil {
		t.Fatal("expected npm install fallback failure")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	combined := result.Message + "\n" + result.Error
	for _, want := range []string{"Direct:", "Fallback:", "安装 npm 版本 Claude Code 失败"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected combined diagnostic to contain %q: %s", want, combined)
		}
	}
	if strings.Contains(combined, "npm-secret-value-123456") {
		t.Fatalf("diagnostic leaked npm secret: %s", combined)
	}
}

func TestClaudeNativeFallbackNPMTimeoutAddedPackagesRecheckSuccess(t *testing.T) {
	originalAttempts := installRecheckAttempts
	originalDelay := installRecheckDelay
	installRecheckAttempts = 2
	installRecheckDelay = time.Millisecond
	t.Cleanup(func() {
		installRecheckAttempts = originalAttempts
		installRecheckDelay = originalDelay
	})

	runner := &nativeBootstrapTestRunner{
		directErr:             errors.New("direct failed"),
		npmInstallErr:         context.DeadlineExceeded,
		npmInstallOut:         "added 2 packages in 3m",
		createOnClaudeInstall: true,
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err != nil {
		t.Fatalf("expected npm timeout with added packages to recover through bounded recheck, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected successful native bootstrap result, got %+v", result)
	}
	if !runner.sawClaudeInstall() {
		t.Fatal("expected claude install to continue after npm package recheck succeeded")
	}
}

func TestClaudeNativeFallbackNPMTimeoutWithoutRecheckRemainsFailure(t *testing.T) {
	originalAttempts := installRecheckAttempts
	originalDelay := installRecheckDelay
	installRecheckAttempts = 1
	installRecheckDelay = time.Millisecond
	t.Cleanup(func() {
		installRecheckAttempts = originalAttempts
		installRecheckDelay = originalDelay
	})

	runner := &nativeBootstrapTestRunner{
		directErr:     errors.New("direct failed"),
		npmInstallErr: context.DeadlineExceeded,
		npmInstallOut: "npm ERR! network failed",
		npmListErr:    errors.New("package not installed"),
	}
	svc := prepareNativeBootstrapTest(t, runner)

	result, err := svc.installClaudeCodeWithMethodProgress(ClaudeInstallNative, nil)
	if err == nil {
		t.Fatal("expected true npm failure when bounded recheck cannot confirm installation")
	}
	if result == nil || result.Success {
		t.Fatalf("expected failed result, got %+v", result)
	}
	if runner.sawClaudeInstall() {
		t.Fatal("must not run claude install when npm package recheck failed")
	}
}

func TestClaudeNativeBootstrapCommandTimeoutAndConstruction(t *testing.T) {
	cmd := claudeNativeBootstrapCommand()
	if cmd.path != "claude" {
		t.Fatalf("path = %q, want claude", cmd.path)
	}
	if len(cmd.args) != 1 || cmd.args[0] != "install" {
		t.Fatalf("args = %v, want [install]", cmd.args)
	}
	if commandTimeout(cmd) < nativeInstallCommandTimeout {
		t.Fatalf("claude install timeout = %v, want at least native timeout %v", commandTimeout(cmd), nativeInstallCommandTimeout)
	}
}

func TestClaudeNativeBootstrapTimeoutDiagnosticIsSanitized(t *testing.T) {
	runner := &deadlineCaptureRunner{
		result: &platform.ProcessResult{Stdout: "claude install started token=bootstrap-secret-value-123456"},
		err:    context.DeadlineExceeded,
	}
	svc := NewServiceWithRunner(runner)

	err := svc.runInstallCommand(claudeNativeBootstrapCommand())
	if err == nil {
		t.Fatal("expected timeout/error from claude install command")
	}
	if runner.deadline < nativeInstallCommandTimeout-time.Second {
		t.Fatalf("claude install deadline = %v, want about %v", runner.deadline, nativeInstallCommandTimeout)
	}
	text := err.Error()
	if !strings.Contains(text, "claude install") {
		t.Fatalf("expected diagnostic to include safe command shape: %s", text)
	}
	if strings.Contains(text, "bootstrap-secret-value-123456") {
		t.Fatalf("timeout diagnostic leaked sensitive token: %s", text)
	}
}

func snapshotsContainMessage(snapshots []progressSnapshot, want string) bool {
	for _, snap := range snapshots {
		if strings.Contains(snap.message, want) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// AC1/AC2 edge case: evidence detection helpers
// ---------------------------------------------------------------------------

func TestInstallerOutputHasNPMInstallSuccessEvidence(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"added packages", "added 2 packages in 3m", true},
		{"changed packages", "changed 1 package in 10s", true},
		{"up to date", "up to date, audited 15 packages", true},
		{"audited packages", "audited 150 packages in 5s", true},
		{"no evidence", "npm ERR! network failed", false},
		{"empty output", "", false},
		{"partial match no number", "added packages in 3m", false},
		{"added zero packages", "added 0 packages in 1s", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := installerOutputHasNPMInstallSuccessEvidence(tc.output)
			if got != tc.want {
				t.Errorf("installerOutputHasNPMInstallSuccessEvidence(%q) = %v, want %v", tc.output, got, tc.want)
			}
		})
	}
}

func TestInstallCommandErrorLooksRecoverable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"timeout message", errors.New("command timed out after 2m0s"), true},
		{"context deadline message", errors.New("context deadline exceeded"), true},
		{"process killed", errors.New("process killed"), true},
		{"exit status 1", errors.New("exit status 1"), false},
		{"generic error", errors.New("something went wrong"), false},
		{"nil error", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := installCommandErrorLooksRecoverable(tc.err)
			if got != tc.want {
				t.Errorf("installCommandErrorLooksRecoverable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestInstallCommandOutcomeNeedsRecheck(t *testing.T) {
	tests := []struct {
		name    string
		command installCommand
		result  *platform.ProcessResult
		err     error
		want    bool
	}{
		{
			name:    "nil error means no recheck",
			command: installCommand{description: "npm install"},
			result:  &platform.ProcessResult{Stdout: "success"},
			err:     nil,
			want:    false,
		},
		{
			name:    "timeout error triggers recheck",
			command: installCommand{description: "npm install"},
			result:  &platform.ProcessResult{},
			err:     context.DeadlineExceeded,
			want:    true,
		},
		{
			name:    "non-recoverable error without evidence means no recheck",
			command: installCommand{description: "npm install"},
			result:  &platform.ProcessResult{Stderr: "npm ERR! network failed"},
			err:     errors.New("exit status 1"),
			want:    false,
		},
		{
			name:    "non-recoverable error but output has added packages triggers recheck",
			command: installCommand{description: "npm install"},
			result:  &platform.ProcessResult{Stdout: "added 2 packages in 3m"},
			err:     errors.New("exit status 1"),
			want:    true,
		},
		{
			name:    "deadline exceeded with no output triggers recheck",
			command: installCommand{description: "claude install"},
			result:  &platform.ProcessResult{},
			err:     fmt.Errorf("timeout 2m0s: deadline exceeded"),
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := installCommandOutcomeNeedsRecheck(tc.command, tc.result, tc.err)
			if got != tc.want {
				t.Errorf("installCommandOutcomeNeedsRecheck() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBoundedInstallRecheckAttempts_Default(t *testing.T) {
	original := installRecheckAttempts
	defer func() { installRecheckAttempts = original }()

	installRecheckAttempts = 0
	if got := boundedInstallRecheckAttempts(); got != 1 {
		t.Errorf("boundedInstallRecheckAttempts() with 0 = %d, want 1", got)
	}

	installRecheckAttempts = -1
	if got := boundedInstallRecheckAttempts(); got != 1 {
		t.Errorf("boundedInstallRecheckAttempts() with -1 = %d, want 1", got)
	}

	installRecheckAttempts = 5
	if got := boundedInstallRecheckAttempts(); got != 5 {
		t.Errorf("boundedInstallRecheckAttempts() with 5 = %d, want 5", got)
	}
}
