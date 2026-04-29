package envcheck

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
	return s.installOrUpdateWithProgress(tool, operation, nil)
}

func (s *Service) installOrUpdateWithProgress(tool CLITool, operation installOperation, reporter progressReporter) (*InstallResult, error) {
	if !IsValidCLITool(tool) {
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}

	reporter.report(OperationStepPrecheck, fmt.Sprintf("Checking current %s status...", displayToolName(tool)), progressPrecheck)

	before, checkErr := s.CheckOne(tool)
	if checkErr != nil && before == nil {
		return installFailure(tool, fmt.Sprintf("pre-%s check failed", operation), checkErr), checkErr
	}

	if operation == installOperationInstall && isHealthyAndCurrent(before) {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("%s is already installed and up to date", displayToolName(tool)),
			Tool:    tool,
			Version: before.Version,
		}, nil
	}
	if operation == installOperationUpdate && isHealthyAndCurrent(before) {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("%s is already up to date", displayToolName(tool)),
			Tool:    tool,
			Version: before.Version,
		}, nil
	}

	reporter.report(OperationStepPrepare, fmt.Sprintf("Preparing %s commands...", displayToolName(tool)), progressPrepare)

	commands, err := s.installCommands(tool, operation, before)
	if err != nil {
		return installFailure(tool, fmt.Sprintf("prepare %s command failed", operation), err), err
	}

	reporter.report(OperationStepRunCommand, fmt.Sprintf("Running %s %s...", displayToolName(tool), operation), progressRunStart)

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
			reporter.report(OperationStepRunCommand, fmt.Sprintf("Trying %s...", command.description), cmdProgress)
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
			reporter.report(OperationStepRunCommand, fmt.Sprintf("%s failed, trying next method...", command.description), lastProgress)
			continue
		}

		// Phase 2: Immediate post-command verification.
		reporter.report(OperationStepVerify, fmt.Sprintf("Verifying after %s...", command.description), progressVerify)
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
				Message: fmt.Sprintf("%s %s completed successfully via %s", displayToolName(tool), operation, command.description),
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
				fmt.Sprintf("%s succeeded but verification failed (%s). Trying next fallback...", command.description, verifyReason),
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
		"%s %s failed after trying all methods. Details: [%s]. Suggestion: ensure Node.js and npm are installed, close any terminal using the tool, and retry.",
		displayToolName(tool), operation, strings.Join(attemptDetails, "; "),
	)
	if overallLastErr == nil {
		overallLastErr = errors.New(message)
	}
	return installFailure(tool, message, overallLastErr), overallLastErr
}

func (s *Service) installCommands(tool CLITool, operation installOperation, current *CheckStatus) ([]installCommand, error) {
	switch tool {
	case ToolClaudeCode:
		// claudeInstallCommands already returns a prioritized command sequence
		// that includes npm (on all platforms). We must NOT add a duplicate npm
		// command. Just check npm availability and resolve paths.
		baseCmds := claudeInstallCommands(operation, current)
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

func claudeInstallCommands(operation installOperation, current *CheckStatus) []installCommand {
	// On non-Windows (macOS/Linux), Claude Code is installed exclusively via npm.
	// Do not generate powershell.exe or winget commands.
	if runtime.GOOS != "windows" {
		return []installCommand{npmClaudeCommand(operation)}
	}

	// Windows command construction.
	// For updates, the command order depends on how Claude was originally installed:
	//   - NPM install  -> npm update first (fastest, most reliable for npm installs)
	//   - Winget install -> winget upgrade first (native package manager)
	//   - Native/Unknown -> npm install @latest first (most reliable cross-version
	//     updater that handles version pinning), then winget, then PowerShell
	//     install.ps1 as last resort (it's a full installer, not a targeted updater)
	//
	// For fresh installs: npm first, then native PowerShell, then winget.

	if operation == installOperationUpdate && current != nil {
		switch current.InstallMethod {
		case InstallMethodNPM:
			// NPM-installed: npm update first, fall back to winget, then native.
			return []installCommand{
				npmClaudeCommand(installOperationUpdate),
				wingetClaudeCommand(installOperationUpdate),
				nativePowerShellClaudeCommand(),
			}
		case InstallMethodWinget:
			// Winget-installed: winget upgrade first, then npm, then native.
			return []installCommand{
				wingetClaudeCommand(installOperationUpdate),
				npmClaudeCommand(installOperationUpdate),
				nativePowerShellClaudeCommand(),
			}
		default:
			// Native or unknown: npm install @latest (handles version upgrade
			// reliably), then winget upgrade, then native install.ps1 as last
			// resort. The install.ps1 script is a full installer, not a targeted
			// updater, so it is less reliable for update operations.
			return []installCommand{
				npmClaudeCommand(installOperationUpdate),
				wingetClaudeCommand(installOperationUpdate),
				nativePowerShellClaudeCommand(),
			}
		}
	}

	// Fresh install on Windows: npm first, then native, then winget.
	return []installCommand{
		npmClaudeCommand(installOperationInstall),
		nativePowerShellClaudeCommand(),
		wingetClaudeCommand(installOperationInstall),
	}
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
		"npm is required to install this tool, but npm is not available in PATH (%s). Install Node.js (https://nodejs.org) or ensure npm is in PATH, then restart CodeBox.",
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
		msg := "npm is not available"
		if resolveErr != nil {
			msg = resolveErr.Error()
		}
		s.npmResolvedErr = fmt.Errorf("%s; install Node.js (https://nodejs.org) and ensure npm is in PATH", msg)
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
			s.npmResolvedErr = fmt.Errorf("npm found at %s but node is not in PATH: %s (install_node or fix_path recommended)", resolved.Path, detail)
		} else {
			s.npmResolvedErr = fmt.Errorf("npm found at %s but not functional: %s", resolved.Path, detail)
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
		return "tool status is empty after installation"
	}
	if strings.TrimSpace(status.Error) != "" {
		return status.Error
	}
	if !status.Installed {
		return "tool executable was not found after installation"
	}
	if !status.PATHOk {
		return "tool was found outside PATH; restart the application or terminal after PATH changes"
	}
	return "tool verification did not report a usable installation"
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
