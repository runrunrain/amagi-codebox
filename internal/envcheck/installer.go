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
	"time"

	"amagi-codebox/internal/platform"
)

const (
	installCommandTimeout = 120 * time.Second

	installOperationInstall installOperation = "install"
	installOperationUpdate  installOperation = "update"

	// Progress percentages for each phase of an install/update operation.
	progressPrecheck  = 5
	progressPrepare   = 15
	progressRunStart  = 20
	progressRunEnd    = 80
	progressVerify    = 85
	progressRefresh   = 95
	progressCompleted = 100
)

type installOperation string

type installCommand struct {
	description string
	path        string
	args        []string
}

// progressReporter is a callback invoked by installOrUpdate to report
// progress during an async operation. May be nil for synchronous callers.
type progressReporter func(step OperationStep, message string, progress int)

// report safely calls the progress reporter if non-nil.
func (pr progressReporter) report(step OperationStep, message string, progress int) {
	if pr != nil {
		pr(step, message, progress)
	}
}

// monotonicReporter wraps a progressReporter and guarantees that progress
// values are monotonically non-decreasing. If a caller passes a value lower
// than the previous one, it is clamped to the previous high-water mark.
// Thread-safe for concurrent use.
func monotonicReporter(inner progressReporter) progressReporter {
	var mu sync.Mutex
	var last int
	return func(step OperationStep, message string, progress int) {
		mu.Lock()
		if progress < last {
			progress = last
		}
		last = progress
		mu.Unlock()
		if inner != nil {
			inner(step, message, progress)
		}
	}
}

func (s *Service) installOrUpdate(tool CLITool, operation installOperation) (*InstallResult, error) {
	return s.installOrUpdateWithProgress(tool, operation, nil, ClaudeInstallAuto)
}

func (s *Service) installOrUpdateWithProgress(tool CLITool, operation installOperation, reporter progressReporter, method ClaudeInstallMethod) (*InstallResult, error) {
	if !IsValidCLITool(tool) {
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}

	reporter.report(OperationStepPrecheck, fmt.Sprintf("正在检查 %s 当前状态...", displayToolName(tool)), progressPrecheck)

	before, checkErr := s.CheckOne(tool)
	if checkErr != nil && before == nil {
		return installFailure(tool, fmt.Sprintf("%s前检查失败", operationDisplayName(operation)), checkErr), checkErr
	}

	if operation == installOperationInstall && isHealthyAndCurrent(before) {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("%s 已安装且为最新版本", displayToolName(tool)),
			Tool:    tool,
			Version: before.Version,
		}, nil
	}
	if operation == installOperationUpdate && isHealthyAndCurrent(before) {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("%s 已是最新版本", displayToolName(tool)),
			Tool:    tool,
			Version: before.Version,
		}, nil
	}

	reporter.report(OperationStepPrepare, fmt.Sprintf("正在准备 %s 命令...", displayToolName(tool)), progressPrepare)

	commands, err := s.installCommands(tool, operation, before, method)
	if err != nil {
		return installFailure(tool, fmt.Sprintf("准备%s命令失败", operationDisplayName(operation)), err), err
	}

	reporter.report(OperationStepRunCommand, fmt.Sprintf("正在%s %s...", operationDisplayName(operation), displayToolName(tool)), progressRunStart)

	// Extract before-version for update verification.
	beforeVersion := ""
	if before != nil {
		beforeVersion = strings.TrimSpace(before.Version)
	}

	// Try each command with immediate verification. If a command executes
	// successfully but post-command verification fails (e.g. version unchanged,
	// PATH not ok), we record the failure and continue to the next fallback.
	// Only when ALL candidates have been exhausted do we report overall failure.
	numCommands := len(commands)
	lastProgress := progressRunStart

	type attemptResult struct {
		description string
		runErr      string // empty if command succeeded
		verifyErr   string // empty if verification passed
	}
	var attempts []attemptResult
	var overallLastErr error

	for i, command := range commands {
		cmdProgress := progressRunStart
		if numCommands > 1 {
			cmdProgress = progressRunStart + (progressRunEnd-progressRunStart)*i/(numCommands-1)
		}
		if cmdProgress > lastProgress {
			reporter.report(OperationStepRunCommand, fmt.Sprintf("正在尝试 %s...", command.description), cmdProgress)
			lastProgress = cmdProgress
		}

		// Phase 1: Execute the command.
		runErr := s.runInstallCommand(command)
		if runErr != nil {
			attempts = append(attempts, attemptResult{
				description: command.description,
				runErr:      runErr.Error(),
			})
			overallLastErr = runErr
			reporter.report(OperationStepRunCommand, fmt.Sprintf("%s 失败，正在尝试下一种方式...", command.description), lastProgress)
			continue
		}

		// Phase 2: Immediate post-command verification.
		reporter.report(OperationStepVerify, fmt.Sprintf("正在验证 %s 后的结果...", command.description), progressVerify)
		after, verifyErr := s.CheckOne(tool)

		// Check if verification passed.
		verifyOk := after != nil && after.Installed && after.PATHOk && strings.TrimSpace(after.Error) == ""

		// For update operations, also check version changed.
		versionChanged := true
		if verifyOk && operation == installOperationUpdate && beforeVersion != "" {
			afterVersion := strings.TrimSpace(after.Version)
			if afterVersion == beforeVersion {
				versionChanged = false
			}
		}

		if verifyOk && versionChanged {
			// Success! This command worked and verification passed.
			return &InstallResult{
				Success: true,
				Message: fmt.Sprintf("%s 已通过 %s 方式成功%s", displayToolName(tool), command.description, operationDisplayName(operation)),
				Tool:    tool,
				Version: after.Version,
			}, nil
		}

		// Command ran but verification failed. Build a descriptive reason.
		verifyReason := verificationErrorMessage(after)
		if verifyOk && !versionChanged {
			verifyReason = fmt.Sprintf("version unchanged after %s (%s)", command.description, beforeVersion)
		}
		if verifyErr != nil {
			verifyReason = fmt.Sprintf("verification call failed: %v", verifyErr)
		}

		attempts = append(attempts, attemptResult{
			description: command.description,
			verifyErr:   verifyReason,
		})
		overallLastErr = fmt.Errorf("%s: command succeeded but verification failed: %s", command.description, verifyReason)

		// Continue to next fallback with clear progress message.
		if i < numCommands-1 {
			reporter.report(OperationStepRunCommand,
				fmt.Sprintf("%s 执行成功但验证失败 (%s)。正在尝试下一种备选方式...", command.description, verifyReason),
				lastProgress)
		}
	}

	// All candidates exhausted. Build a comprehensive error message.
	var attemptDetails []string
	for _, a := range attempts {
		if a.runErr != "" {
			attemptDetails = append(attemptDetails, fmt.Sprintf("%s: execution failed (%s)", a.description, a.runErr))
		} else if a.verifyErr != "" {
			attemptDetails = append(attemptDetails, fmt.Sprintf("%s: verification failed (%s)", a.description, a.verifyErr))
		}
	}

	message := fmt.Sprintf(
		"%s%s失败：已尝试所有安装方式。详情: [%s]。建议：确保 Node.js 和 npm 已安装，关闭使用该工具的所有终端后重试。",
		displayToolName(tool), operationDisplayName(operation), strings.Join(attemptDetails, "; "),
	)
	if overallLastErr == nil {
		overallLastErr = errors.New(message)
	}
	return installFailure(tool, message, overallLastErr), overallLastErr
}

func (s *Service) installCommands(tool CLITool, operation installOperation, current *CheckStatus, method ClaudeInstallMethod) ([]installCommand, error) {
	switch tool {
	case ToolClaudeCode:
		// If the user explicitly selected an install method, use only that method
		// (no fallback chain).
		if method != ClaudeInstallAuto {
			// Pre-flight checks for specific methods
			switch method {
			case ClaudeInstallNative:
				if runtime.GOOS == "windows" {
					accessible, reason := s.verifyNativeInstallerAccessible()
					if !accessible {
						return nil, fmt.Errorf(
							"Native 安装脚本不可达: %s。建议使用 winget 或 npm 安装方式",
							reason,
						)
					}
				}
			case ClaudeInstallWinget:
				if runtime.GOOS == "windows" {
					if err := s.verifyWingetHealth(); err != nil {
						return nil, err
					}
				}
			}

			cmd, err := claudeInstallCommandsForMethod(method, operation)
			if err != nil {
				return nil, err
			}
			if method == ClaudeInstallNPM {
				if err := s.ensureNPMAvailable(); err != nil {
					return nil, err
				}
				cmd = s.resolveCommandNPMPath(cmd)
			}
			return []installCommand{cmd}, nil
		}
		// claudeInstallCommands already returns a prioritized command sequence
		// that includes npm (on all platforms). We must NOT add a duplicate npm
		// command. Just check npm availability and resolve paths.
		baseCmds, err := s.claudeInstallCommands(operation, current)
		if err != nil {
			return nil, err
		}
		if err := s.ensureNPMAvailable(); err != nil {
			// npm not available: filter out any npm commands from the sequence
			// so only non-npm fallbacks remain (powershell, winget on Windows).
			filtered := make([]installCommand, 0, len(baseCmds))
			for _, cmd := range baseCmds {
				if cmd.path == "npm" {
					continue
				}
				filtered = append(filtered, cmd)
			}
			if len(filtered) == 0 {
				return nil, err
			}
			return filtered, nil
		}
		// Resolve all bare "npm" paths to absolute paths.
		for i := range baseCmds {
			baseCmds[i] = s.resolveCommandNPMPath(baseCmds[i])
		}
		return baseCmds, nil
	case ToolOpenCode:
		if err := s.ensureNPMAvailable(); err != nil {
			return nil, err
		}
		return []installCommand{s.resolveCommandNPMPath(npmOpenCodeCommand(operation))}, nil
	case ToolCodex:
		if err := s.ensureNPMAvailable(); err != nil {
			return nil, err
		}
		return []installCommand{s.resolveCommandNPMPath(npmCodexCommand(operation))}, nil
	default:
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}
}

// npmClaudeCommand returns the npm command for Claude Code.
// For updates, uses "install -g @latest" which is more reliable than
// "update -g": npm update respects version ranges in package.json and may
// not actually upgrade to the latest version, whereas install @latest
// forces the newest release.
func npmClaudeCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm install @anthropic-ai/claude-code@latest",
			path:        "npm",
			args:        []string{"install", "-g", "@anthropic-ai/claude-code@latest"},
		}
	}
	return installCommand{
		description: "npm global install @anthropic-ai/claude-code",
		path:        "npm",
		args:        []string{"install", "-g", "@anthropic-ai/claude-code"},
	}
}

// npmOpenCodeCommand returns the npm install or update command for OpenCode.
func npmOpenCodeCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm global update opencode-ai",
			path:        "npm",
			args:        []string{"update", "-g", "opencode-ai"},
		}
	}
	return installCommand{
		description: "npm global install opencode-ai@latest",
		path:        "npm",
		args:        []string{"install", "-g", "opencode-ai@latest"},
	}
}

// npmCodexCommand returns the npm install or update command for Codex.
func npmCodexCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm global update @openai/codex",
			path:        "npm",
			args:        []string{"update", "-g", "@openai/codex"},
		}
	}
	return installCommand{
		description: "npm global install @openai/codex",
		path:        "npm",
		args:        []string{"install", "-g", "@openai/codex"},
	}
}

// resolveCommandNPMPath replaces bare "npm" command paths with the resolved
// absolute path to npm, ensuring install commands work even when the GUI
// process has a minimal PATH.
func (s *Service) resolveCommandNPMPath(cmd installCommand) installCommand {
	if cmd.path == "npm" {
		if resolved := s.resolveNPMPath(); resolved != "" && resolved != "npm" {
			cmd.path = resolved
		}
	}
	return cmd
}

// claudeInstallCommandsForMethod returns a single install command based on
// the user-specified method. Unlike claudeInstallCommands, it does NOT return
// a fallback chain.
// This is a pure function that does not have access to Service receivers.
// Network accessibility checks for native method are handled at the call site
// in installCommands.
func claudeInstallCommandsForMethod(method ClaudeInstallMethod, operation installOperation) (installCommand, error) {
	switch method {
	case ClaudeInstallNPM:
		return npmClaudeCommand(operation), nil
	case ClaudeInstallNative:
		return nativePowerShellClaudeCommand(), nil
	case ClaudeInstallWinget:
		return wingetClaudeCommand(operation), nil
	default:
		return installCommand{}, fmt.Errorf("unsupported Claude Code install method: %s", method)
	}
}

func (s *Service) claudeInstallCommands(operation installOperation, current *CheckStatus) ([]installCommand, error) {
	// On non-Windows (macOS/Linux), Claude Code is installed exclusively via npm.
	// Do not generate powershell.exe or winget commands.
	if runtime.GOOS != "windows" {
		return []installCommand{npmClaudeCommand(operation)}, nil
	}

	// Windows command construction with smart native accessibility check.

	if operation == installOperationUpdate && current != nil {
		switch current.InstallMethod {
		case InstallMethodNPM:
			// NPM-installed: strict same-channel update, no cross-channel fallback.
			return []installCommand{npmClaudeCommand(installOperationUpdate)}, nil
		case InstallMethodWinget:
			// Winget-installed: strict same-channel update, no cross-channel fallback.
			return []installCommand{wingetClaudeCommand(installOperationUpdate)}, nil
		case InstallMethodNative:
			// Native-installed: strict same-channel update.
			nativeAccessible, _ := s.verifyNativeInstallerAccessible()
			if nativeAccessible {
				return []installCommand{nativePowerShellClaudeCommand()}, nil
			}
			// Native blocked: return error so caller can inform user.
			return nil, fmt.Errorf("Native 安装脚本被 Cloudflare 拦截，无法通过 Native 渠道更新。请使用 winget 或 npm 重新安装")
		default:
			// Unknown method: cannot determine the current install channel.
			// Do NOT fallback to native/winget/npm to avoid multi-channel conflicts.
			// Require the user to explicitly choose an install method to reinstall/switch.
			return nil, fmt.Errorf("无法确定当前 Claude Code 安装渠道，请选择安装方式执行显式重装/切换渠道后再更新")
		}
	}

	// Fresh install on Windows: smart priority based on network accessibility.
	// 1. Check if native installer is accessible
	// 2. If native blocked by Cloudflare -> winget first, then npm
	// 3. If native accessible -> native first (official recommended), then winget, then npm
	accessible, _ := s.verifyNativeInstallerAccessible()

	if accessible {
		// Native accessible: native first (official recommended), winget, npm
		return []installCommand{
			nativePowerShellClaudeCommand(),
			wingetClaudeCommand(installOperationInstall),
			npmClaudeCommand(installOperationInstall),
		}, nil
	}

	// Native blocked: winget first (bypasses Cloudflare), then npm
	return []installCommand{
		wingetClaudeCommand(installOperationInstall),
		npmClaudeCommand(installOperationInstall),
	}, nil
}

// ---------------------------------------------------------------------------
// Native installer accessibility detection
// ---------------------------------------------------------------------------

// verifyNativeInstallerAccessible checks whether the official Claude Code
// native installer URL is reachable and returns a valid PowerShell script
// (rather than a Cloudflare challenge HTML page).
// Returns (accessible bool, reason string).
func (s *Service) verifyNativeInstallerAccessible() (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path: "powershell.exe",
		Args: []string{
			"-NoProfile", "-NonInteractive",
			"-Command", "(irm https://claude.ai/install.ps1 2>$null) -join \"`n\"",
		},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		return false, fmt.Sprintf("无法访问 install.ps1: %v", err)
	}

	output := strings.TrimSpace(resultText(result))

	// Cloudflare challenge HTML contains these patterns
	blockedPatterns := []string{
		"Just a moment",
		"cdn-cgi/challenge-platform",
		"Enable JavaScript",
		"<html",
		"<!DOCTYPE",
	}
	for _, pattern := range blockedPatterns {
		if strings.Contains(output, pattern) {
			return false, "当前网络环境被 Cloudflare 拦截，无法直接获取 Native 安装脚本"
		}
	}

	// Valid response should contain PowerShell content markers
	validMarkers := []string{"Write-Output", "function", "param("}
	for _, marker := range validMarkers {
		if strings.Contains(output, marker) {
			return true, ""
		}
	}

	// Unknown content -- treat as inaccessible
	return false, "install.ps1 返回内容无法识别，可能是网络限制"
}

// ---------------------------------------------------------------------------
// Install conflict detection and cleanup
// ---------------------------------------------------------------------------

// ensureNoConflictInstall checks whether Claude Code is currently installed
// via a DIFFERENT method than the one being used. If so, it uninstalls the
// conflicting version before proceeding.
func (s *Service) ensureNoConflictInstall(targetMethod InstallMethod) (*InstallResult, error) {
	status, err := s.CheckOne(ToolClaudeCode)
	if err != nil {
		return nil, fmt.Errorf("安装前检测失败: %v", err)
	}
	return resolveConflictAction(status, targetMethod, s.cleanClaudeCode)
}

// resolveConflictAction is a pure function that determines whether a conflict
// exists between the current installation method and the target method, and
// invokes the cleaner if needed. Extracted from ensureNoConflictInstall for
// testability -- callers inject a mock cleaner instead of depending on real
// platform state.
func resolveConflictAction(
	status *CheckStatus,
	targetMethod InstallMethod,
	cleaner func(InstallMethod) (*InstallResult, error),
) (*InstallResult, error) {
	if status == nil || !status.Installed {
		return nil, nil // No existing installation, proceed
	}

	currentMethod := status.InstallMethod
	if currentMethod == targetMethod {
		return nil, nil // Same method, upgrading is fine
	}
	if currentMethod == InstallMethodUnknown {
		return nil, nil // Can't determine method, don't risk auto-cleanup
	}

	// Different method installed -- clean it first
	cleanResult, cleanErr := cleaner(currentMethod)
	if cleanErr != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("检测到已有 %s 安装的 Claude Code，自动清理失败: %v。请手动卸载后重试", currentMethod, cleanErr),
			Tool:    ToolClaudeCode,
		}, cleanErr
	}
	if !cleanResult.Success {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("检测到已有 %s 安装的 Claude Code，但自动清理失败。%s", currentMethod, cleanResult.Message),
			Tool:    ToolClaudeCode,
		}, nil
	}

	// Cleanup successful, proceed
	return &InstallResult{
		Success: true,
		Message: fmt.Sprintf("已自动卸载 %s 安装的 Claude Code，将继续使用 %s 安装", currentMethod, targetMethod),
		Tool:    ToolClaudeCode,
	}, nil
}

// ---------------------------------------------------------------------------
// Winget health check
// ---------------------------------------------------------------------------

// verifyWingetHealth checks winget availability and returns an error if
// winget is not functional. This should be called before attempting a winget
// install to provide a clear error message.
func (s *Service) verifyWingetHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "winget",
		Args:   []string{"--version"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		return fmt.Errorf("winget 不可用: %v。请确保已安装 App Installer (https://aka.ms/getwinget)", err)
	}
	return nil
}

// nativePowerShellClaudeCommand returns the PowerShell-based Claude installer.
// This is a full install script, not a targeted updater -- prefer npm/winget
// for update operations.
func nativePowerShellClaudeCommand() installCommand {
	return installCommand{
		description: "Claude Code native PowerShell installer",
		path:        "powershell.exe",
		args: []string{
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy", "RemoteSigned",
			"-Command", "irm https://claude.ai/install.ps1 | iex",
		},
	}
}

// wingetClaudeCommand returns the winget install or upgrade command for Claude Code.
func wingetClaudeCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "winget upgrade Anthropic.ClaudeCode",
			path:        "winget",
			args:        []string{"upgrade", "Anthropic.ClaudeCode", "--accept-source-agreements", "--accept-package-agreements"},
		}
	}
	return installCommand{
		description: "winget install Anthropic.ClaudeCode",
		path:        "winget",
		args:        []string{"install", "Anthropic.ClaudeCode", "--accept-source-agreements", "--accept-package-agreements"},
	}
}

func (s *Service) ensureNPMAvailable() error {
	// Fast path: if populateCanInstall already probed npm, reuse the result.
	s.npmOnce.Do(func() {
		// If the once block has not run yet, run the full probe.
		s.probeNPMAvailability()
	})
	if s.npmAvailable {
		return nil
	}
	if s.npmResolvedErr != nil {
		return s.npmResolvedErr
	}

	// Slow path: once block was initialized by populateCanInstall but npm
	// was not found. Try once more through the runner.
	npmPath := s.resolveNPMPath()
	env := s.buildEnhancedEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"--version"},
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(resultText(result))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf(
		"安装此工具需要 npm，但 npm 未在 PATH 中找到 (%s)。请安装 Node.js (https://nodejs.org) 并确保 npm 在 PATH 中，然后重启 CodeBox。",
		message,
	)
}

// probeNPMAvailability performs the actual npm availability check and stores
// the result in the service's cache fields. Called exactly once via npmOnce.
// It uses an enhanced PATH (including directories from the platform resolver)
// so that npm/node found outside the GUI process PATH can still be used.
func (s *Service) probeNPMAvailability() {
	env := s.buildEnhancedEnv()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, resolveErr := resolver.ResolveExecutable("npm", nil, env)
	if resolveErr != nil || strings.TrimSpace(resolved.Path) == "" {
		// Check if npm path was found but node is missing
		s.npmAvailable = false
		msg := "npm 不可用"
		if resolveErr != nil {
			msg = resolveErr.Error()
		}
		s.npmResolvedErr = fmt.Errorf("%s；请安装 Node.js (https://nodejs.org) 并确保 npm 在 PATH 中", msg)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, runErr := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   resolved.Path,
		Args:   []string{"--version"},
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	if runErr != nil {
		s.npmAvailable = false
		detail := strings.TrimSpace(resultText(result))
		if detail == "" {
			detail = runErr.Error()
		}
		// Detect "node not found" specifically
		if strings.Contains(detail, "node: No such file") || strings.Contains(detail, "env: node: No such file") {
			s.npmResolvedErr = fmt.Errorf("在 %s 找到 npm 但 node 不在 PATH 中: %s（建议安装 Node.js 或修复 PATH）", resolved.Path, detail)
		} else {
			s.npmResolvedErr = fmt.Errorf("在 %s 找到 npm 但无法正常运行: %s", resolved.Path, detail)
		}
		return
	}
	s.npmAvailable = true
}

// resolveNPMPath returns the absolute path to the npm executable found by
// the platform resolver, falling back to a bare "npm" name.
func (s *Service) resolveNPMPath() string {
	env := s.buildEnhancedEnv()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, err := resolver.ResolveExecutable("npm", nil, env)
	if err == nil && strings.TrimSpace(resolved.Path) != "" {
		return resolved.Path
	}
	// Final fallback: bare name, let OS resolve.
	return "npm"
}

func (s *Service) runInstallCommand(command installCommand) error {
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()

	// Use enhanced env for npm commands so /usr/bin/env node can find node
	env := s.buildEnhancedEnv()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   command.path,
		Args:   append([]string(nil), command.args...),
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(resultText(result))
	if message == "" {
		message = err.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		message = fmt.Sprintf("command timed out after %s: %s", installCommandTimeout, message)
	}
	if errors.Is(err, exec.ErrNotFound) {
		message = fmt.Sprintf("command %q was not found in PATH. Install the required tool or fix PATH. Detail: %s", command.path, message)
	}
	return fmt.Errorf("%s (ran %s %s): %s", command.description, command.path, strings.Join(command.args, " "), message)
}

func isHealthyAndCurrent(status *CheckStatus) bool {
	return status != nil &&
		status.Installed &&
		status.PATHOk &&
		!status.HasUpdate &&
		strings.TrimSpace(status.Error) == ""
}

func installFailure(tool CLITool, message string, err error) *InstallResult {
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	return &InstallResult{
		Success: false,
		Message: message,
		Tool:    tool,
		Error:   errorMessage,
	}
}

func verificationErrorMessage(status *CheckStatus) string {
	if status == nil {
		return "安装后工具状态为空"
	}
	if strings.TrimSpace(status.Error) != "" {
		return status.Error
	}
	if !status.Installed {
		return "安装后未找到工具可执行文件"
	}
	if !status.PATHOk {
		return "工具在 PATH 之外被找到；请在 PATH 变更后重启应用程序或终端"
	}
	return "工具验证未报告可用安装"
}

// ---------------------------------------------------------------------------
// Claude Code clean / uninstall operations
// ---------------------------------------------------------------------------

// cleanClaudeCode removes an existing Claude Code installation.
// After removal, it re-checks to confirm the tool is no longer installed.
func (s *Service) cleanClaudeCode(method InstallMethod) (*InstallResult, error) {
	switch method {
	case InstallMethodNPM:
		return s.cleanClaudeCodeNPM()
	case InstallMethodNative:
		return s.cleanClaudeCodeNative()
	case InstallMethodWinget:
		return s.cleanClaudeCodeWinget()
	default:
		return &InstallResult{
			Success: false,
			Message: "无法确定当前安装方式，无法自动清理",
			Tool:    ToolClaudeCode,
		}, nil
	}
}

func (s *Service) cleanClaudeCodeNPM() (*InstallResult, error) {
	// 1. npm uninstall -g @anthropic-ai/claude-code
	npmPath := s.resolveNPMPath()
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()
	_, runErr := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"uninstall", "-g", "@anthropic-ai/claude-code"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if runErr != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("npm 卸载失败: %v", runErr),
			Tool:    ToolClaudeCode,
		}, nil
	}
	// Note: We intentionally do NOT delete files under %USERPROFILE%\.local\bin\
	// (claude.cmd, claude, claude.exe) because those belong to the Native install
	// channel. Deleting them here would break independent channel uninstall.
	// Native channel cleanup is handled exclusively by cleanClaudeCodeNative.
	// 2. Verify
	after, _ := s.CheckOne(ToolClaudeCode)
	if after != nil && after.Installed {
		return &InstallResult{
			Success: false,
			Message: "清理后 Claude Code 仍然可被检测到，请手动检查",
			Tool:    ToolClaudeCode,
		}, nil
	}
	return &InstallResult{
		Success: true,
		Message: "Claude Code (npm) 已成功卸载",
		Tool:    ToolClaudeCode,
	}, nil
}

func (s *Service) cleanClaudeCodeNative() (*InstallResult, error) {
	homeDir, _ := os.UserHomeDir()
	patterns := []string{
		filepath.Join(homeDir, ".local", "bin", "claude.exe"),
		filepath.Join(homeDir, ".local", "bin", "claude.cmd"),
		filepath.Join(homeDir, ".local", "bin", "claude"),
	}
	var removed []string
	var failed []string
	for _, p := range patterns {
		if _, err := os.Stat(p); err == nil {
			if os.Remove(p) == nil {
				removed = append(removed, p)
			} else {
				failed = append(failed, p)
			}
		}
	}
	if len(removed) == 0 && len(failed) == 0 {
		return &InstallResult{
			Success: false,
			Message: "未找到 Native 安装的 Claude Code 文件",
			Tool:    ToolClaudeCode,
		}, nil
	}
	after, _ := s.CheckOne(ToolClaudeCode)
	if after != nil && after.Installed {
		detail := fmt.Sprintf("已删除 %d 个文件", len(removed))
		if len(failed) > 0 {
			detail += fmt.Sprintf("，%d 个文件删除失败", len(failed))
		}
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("%s，但 Claude Code 仍可被检测到", detail),
			Tool:    ToolClaudeCode,
		}, nil
	}
	msg := fmt.Sprintf("已清理 %d 个 Native 安装文件", len(removed))
	if len(failed) > 0 {
		msg += fmt.Sprintf("（%d 个文件删除失败）", len(failed))
	}
	return &InstallResult{
		Success: true,
		Message: msg,
		Tool:    ToolClaudeCode,
	}, nil
}

func (s *Service) cleanClaudeCodeWinget() (*InstallResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()
	_, runErr := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "winget",
		Args:   []string{"uninstall", "Anthropic.ClaudeCode"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if runErr != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("winget 卸载失败: %v", runErr),
			Tool:    ToolClaudeCode,
		}, nil
	}
	after, _ := s.CheckOne(ToolClaudeCode)
	if after != nil && after.Installed {
		return &InstallResult{
			Success: false,
			Message: "winget 卸载后 Claude Code 仍可被检测到",
			Tool:    ToolClaudeCode,
		}, nil
	}
	return &InstallResult{
		Success: true,
		Message: "Claude Code (winget) 已成功卸载",
		Tool:    ToolClaudeCode,
	}, nil
}

func displayToolName(tool CLITool) string {
	switch tool {
	case ToolClaudeCode:
		return "Claude Code"
	case ToolOpenCode:
		return "OpenCode"
	case ToolCodex:
		return "Codex"
	default:
		return string(tool)
	}
}

// operationDisplayName returns the Chinese display name for an install operation.
func operationDisplayName(op installOperation) string {
	switch op {
	case installOperationInstall:
		return "安装"
	case installOperationUpdate:
		return "更新"
	default:
		return string(op)
	}
}
