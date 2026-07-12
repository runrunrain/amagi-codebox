package envcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/platform"
)

const (
	installCommandTimeout               = 120 * time.Second
	// claudeNPMInstallTimeout is the timeout used for `npm install -g
	// @anthropic-ai/claude-code` specifically. Claude Code distributes a ~206MB
	// native binary via the platform subpackage (claude-code-darwin-arm64 etc.),
	// which takes much longer to download/extract than a typical npm package.
	// The generic 120s timeout was identified as a P0 root cause of interrupted
	// installs that leave behind staging directories and truncated binaries
	// (see Claude-Code-安装异常说明.md). 10 minutes leaves headroom for slow
	// networks and postinstall hardlinking while still bounding the operation.
	claudeNPMInstallTimeout             = 10 * time.Minute
	claudeNativeBootstrapCommandTimeout = 20 * time.Minute
	installRecheckAttemptsDefault       = 3
	installRecheckDelayDefault          = 300 * time.Millisecond

	installOperationInstall installOperation = "install"
	installOperationUpdate  installOperation = "update"

	// headroomPipInstallTimeout bounds `venv/bin/python -m pip install
	// headroom-ai[proxy]`. The [proxy] extra still pulls in transformers and
	// onnxruntime (large wheels), so the generic 120s timeout is too tight on
	// slow networks. Mirrors claudeNPMInstallTimeout rationale.
	headroomPipInstallTimeout = 10 * time.Minute

	// headroomVenvCreateTimeout bounds `python3 -m venv <dir>`. venv creation
	// with ensurepip is usually fast but can download wheels on first run.
	headroomVenvCreateTimeout = 120 * time.Second

	// Progress percentages for each phase of an install/update operation.
	progressPrecheck  = 5
	progressPrepare   = 15
	progressRunStart  = 20
	progressRunEnd    = 80
	progressVerify    = 85
	progressRefresh   = 95
	progressCompleted = 100

	progressNativeNPMInstall    = 40
	progressNativeClaudeInstall = 70
	progressNativeVerify        = 90

	homebrewNoAutoremoveEnv       = "HOMEBREW_NO_AUTOREMOVE"
	homebrewNoInstallCleanupEnv   = "HOMEBREW_NO_INSTALL_CLEANUP"
	homebrewCleanupSafetyEnvValue = "1"
	homebrewCleanupSafetyDetail   = "Homebrew autoremove disabled via HOMEBREW_NO_AUTOREMOVE=1; Homebrew install cleanup also disabled via HOMEBREW_NO_INSTALL_CLEANUP=1"

	codexNPMPackageName = "@openai/codex"

	// headroomPackageName is the pip distribution name for Headroom. It is the
	// PyPI package, not an npm package; the name reflects the install channel.
	headroomPackageName = "headroom-ai"
)

var (
	installRecheckAttempts = installRecheckAttemptsDefault
	installRecheckDelay    = installRecheckDelayDefault
)

var (
	installerQuotedSecretPattern    = regexp.MustCompile(`(?i)((?:["']?[\w.-]*(?:token|api[_-]?key|apikey|authorization|password|passwd|secret|private[_-]?key|client[_-]?secret)[\w.-]*["']?\s*[:=]\s*)(["']))[^"']*(["'])`)
	installerBareSecretPattern      = regexp.MustCompile(`(?i)((?:["']?[\w.-]*(?:token|api[_-]?key|apikey|authorization|password|passwd|secret|private[_-]?key|client[_-]?secret)[\w.-]*["']?\s*[:=]\s*)(?:Bearer\s+)?)([^"'\s,;}\]]+)`)
	installerBearerTokenPattern     = regexp.MustCompile(`(?i)\b(Bearer\s+)([A-Za-z0-9._~+/=-]{8,})`)
	claudeInstallerLocationPattern  = regexp.MustCompile(`(?im)^\s*Location:\s*(.+?)\s*$`)
	installerSensitiveValuePatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]{10,}\b`),
		regexp.MustCompile(`\bghp_[A-Za-z0-9_]{16,}\b`),
		regexp.MustCompile(`\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
	}
)

type installOperation string

type installCommand struct {
	description string
	path        string
	args        []string
	timeout     time.Duration
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

	if operation == installOperationInstall && isHealthyAndCurrent(before) && !shouldReinstallHealthyUnknownClaude(tool, before) {
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
		runResult, runErr := s.runInstallCommandResult(command)
		if runErr != nil {
			if recovered, after, recoveryDetail := s.verifyToolAfterRecoverableInstallCommand(tool, operation, beforeVersion, command, runResult, runErr); recovered {
				return &InstallResult{
					Success: true,
					Message: fmt.Sprintf("%s %s 命令返回异常，但 bounded recheck 已确认安装可用。方式：%s。诊断：%s", displayToolName(tool), operationDisplayName(operation), command.description, recoveryDetail),
					Tool:    tool,
					Version: after.Version,
				}, nil
			}
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

		npmVerifyDetail := ""
		if isOpenCodeNPMGlobalInstallCommand(tool, command) {
			if npmAfter, npmDetail, npmErr := s.verifyOpenCodeNPMGlobalBinAfterCommand(operation, beforeVersion, after); npmErr == nil {
				return &InstallResult{
					Success: true,
					Message: fmt.Sprintf("%s 已通过 %s 方式成功%s（%s）", displayToolName(tool), command.description, operationDisplayName(operation), npmDetail),
					Tool:    tool,
					Version: npmAfter.Version,
				}, nil
			} else {
				npmVerifyDetail = npmDetail
				if cleanupAfter, cleanupDetail, cleanupErr := s.tryCleanupOpenCodeHomebrewStaleEntryAfterNPMFallback(operation, beforeVersion, after, npmAfter); cleanupErr == nil && cleanupAfter != nil {
					return &InstallResult{
						Success: true,
						Message: fmt.Sprintf("%s 已通过 %s 方式成功%s，并已自动清理旧 Homebrew 入口（%s）", displayToolName(tool), command.description, operationDisplayName(operation), cleanupDetail),
						Tool:    tool,
						Version: cleanupAfter.Version,
					}, nil
				} else if cleanupDetail != "" {
					npmVerifyDetail = strings.Join(nonEmptyStrings(npmVerifyDetail, cleanupDetail), "; ")
				}
			}
		} else if isCodexNPMGlobalInstallCommand(tool, command) {
			if npmAfter, npmDetail, npmErr := s.verifyCodexNPMGlobalBinAfterCommand(operation, beforeVersion, after); npmErr == nil {
				return &InstallResult{
					Success: true,
					Message: fmt.Sprintf("%s 已通过 %s 方式成功%s（%s）", displayToolName(tool), command.description, operationDisplayName(operation), npmDetail),
					Tool:    tool,
					Version: npmAfter.Version,
				}, nil
			} else {
				npmVerifyDetail = npmDetail
				if cleanupAfter, cleanupDetail, cleanupErr := s.tryCleanupCodexStaleNPMEntryAfterFallback(operation, beforeVersion, after, npmAfter); cleanupErr == nil && cleanupAfter != nil {
					return &InstallResult{
						Success: true,
						Message: fmt.Sprintf("%s 已通过 %s 方式成功%s，并已自动清理旧 Homebrew npm 全局入口（%s）", displayToolName(tool), command.description, operationDisplayName(operation), cleanupDetail),
						Tool:    tool,
						Version: cleanupAfter.Version,
					}, nil
				} else if cleanupDetail != "" {
					npmVerifyDetail = strings.Join(nonEmptyStrings(npmVerifyDetail, cleanupDetail), "; ")
				}
			}
		} else if isClaudeNPMGlobalInstallCommand(tool, command) {
			if npmAfter, npmDetail, npmErr := s.verifyClaudeNPMGlobalBinAfterCommand(operation, beforeVersion, after); npmErr == nil {
				return &InstallResult{
					Success: true,
					Message: fmt.Sprintf("%s 已通过 %s 方式成功%s（%s）", displayToolName(tool), command.description, operationDisplayName(operation), npmDetail),
					Tool:    tool,
					Version: npmAfter.Version,
				}, nil
			} else {
				npmVerifyDetail = npmDetail
			}
		}

		// Command ran but verification failed. Build a descriptive reason.
		verifyReason := verificationErrorMessage(after)
		if verifyOk && !versionChanged {
			verifyReason = fmt.Sprintf("version unchanged after %s (%s)", command.description, beforeVersion)
		}
		if verifyErr != nil {
			verifyReason = fmt.Sprintf("verification call failed: %v", verifyErr)
		}
		if npmVerifyDetail != "" {
			verifyReason = fmt.Sprintf("%s; %s", verifyReason, npmVerifyDetail)
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

func isOpenCodeNPMGlobalInstallCommand(tool CLITool, command installCommand) bool {
	if tool != ToolOpenCode {
		return false
	}
	for _, arg := range command.args {
		if strings.Contains(arg, "opencode-ai") || arg == "opencode" {
			return true
		}
	}
	return strings.Contains(command.description, "opencode-ai") || strings.Contains(command.description, "opencode")
}

func isCodexNPMGlobalInstallCommand(tool CLITool, command installCommand) bool {
	if tool != ToolCodex {
		return false
	}
	for _, arg := range command.args {
		if strings.Contains(arg, "@openai/codex") || arg == "codex" {
			return true
		}
	}
	return strings.Contains(command.description, "@openai/codex") || strings.Contains(command.description, "codex")
}

func isClaudeNPMGlobalInstallCommand(tool CLITool, command installCommand) bool {
	if tool != ToolClaudeCode {
		return false
	}
	for _, arg := range command.args {
		if strings.Contains(arg, "@anthropic-ai/claude-code") || strings.Contains(arg, "claude-code") {
			return true
		}
	}
	return strings.Contains(command.description, "@anthropic-ai/claude-code") || strings.Contains(command.description, "claude-code")
}

func (s *Service) verifyOpenCodeNPMGlobalBinAfterCommand(operation installOperation, beforeVersion string, effectiveAfter *CheckStatus) (*CheckStatus, string, error) {
	return s.verifyNPMGlobalCandidateDoesNotConflictWithEffectiveEntry(
		operation,
		beforeVersion,
		effectiveAfter,
		"OpenCode",
		s.checkOpenCodeFromNPMGlobalPrefix,
		openCodeNPMGlobalVerificationDetail,
	)
}

func (s *Service) verifyCodexNPMGlobalBinAfterCommand(operation installOperation, beforeVersion string, effectiveAfter *CheckStatus) (*CheckStatus, string, error) {
	return s.verifyNPMGlobalCandidateDoesNotConflictWithEffectiveEntry(
		operation,
		beforeVersion,
		effectiveAfter,
		"Codex",
		s.checkCodexFromNPMGlobalPrefix,
		codexNPMGlobalVerificationDetail,
	)
}

func (s *Service) verifyClaudeNPMGlobalBinAfterCommand(operation installOperation, beforeVersion string, effectiveAfter *CheckStatus) (*CheckStatus, string, error) {
	return s.verifyNPMGlobalCandidateDoesNotConflictWithEffectiveEntry(
		operation,
		beforeVersion,
		effectiveAfter,
		"Claude Code",
		s.checkClaudeFromNPMGlobalPrefix,
		claudeNPMGlobalVerificationDetail,
	)
}

func (s *Service) verifyNPMGlobalCandidateDoesNotConflictWithEffectiveEntry(
	operation installOperation,
	beforeVersion string,
	effectiveAfter *CheckStatus,
	toolDisplayName string,
	checkNPMCandidate func() (*CheckStatus, []string, error),
	detail func(*CheckStatus, []string, error) string,
) (*CheckStatus, string, error) {
	status, candidates, err := checkNPMCandidate()
	if err != nil {
		return nil, detail(nil, candidates, err), err
	}
	if status == nil || !status.Installed || !status.PATHOk || strings.TrimSpace(status.Error) != "" {
		err := fmt.Errorf("npm global prefix %s status was not healthy: %s", toolDisplayName, verificationErrorMessage(status))
		return nil, detail(status, candidates, err), err
	}
	version := strings.TrimSpace(status.Version)
	if version == "" {
		err := fmt.Errorf("npm global prefix %s reported an empty version", toolDisplayName)
		return nil, detail(status, candidates, err), err
	}
	if operation == installOperationUpdate && strings.TrimSpace(beforeVersion) != "" && version == strings.TrimSpace(beforeVersion) {
		err := fmt.Errorf("npm global prefix %s version was unchanged at %s", toolDisplayName, beforeVersion)
		return nil, detail(status, candidates, err), err
	}
	if err := npmGlobalCandidateConflictWithEffectiveEntry(toolDisplayName, status, effectiveAfter); err != nil {
		return status, detail(status, candidates, err), err
	}
	return status, npmGlobalVerificationDetailWithEffectiveEntry(detail(status, candidates, nil), status, effectiveAfter), nil
}

func (s *Service) tryCleanupOpenCodeHomebrewStaleEntryAfterNPMFallback(operation installOperation, beforeVersion string, effectiveAfter *CheckStatus, npmCandidate *CheckStatus) (*CheckStatus, string, error) {
	if npmCandidate == nil {
		return nil, "", nil
	}
	if err := validateHealthyOpenCodeNPMCandidateForCleanup(operation, beforeVersion, npmCandidate); err != nil {
		return nil, fmt.Sprintf("OpenCode stale Homebrew cleanup skipped: npm candidate is not safe for cleanup: %s", sanitizeInstallerOutput(err.Error())), err
	}

	stalePath, staleVersion, staleErr := validateOpenCodeHomebrewStaleEntry(effectiveAfter, npmCandidate)
	if staleErr != nil {
		return nil, fmt.Sprintf("OpenCode stale Homebrew cleanup skipped: %s", sanitizeInstallerOutput(staleErr.Error())), staleErr
	}

	brewPath, resolveErr := s.resolveBrewPath()
	if resolveErr != nil {
		detail := fmt.Sprintf("OpenCode stale Homebrew cleanup failed before uninstall: stale=%s version=%s; %s", stalePath, staleVersion, sanitizeInstallerOutput(resolveErr.Error()))
		return nil, detail, resolveErr
	}

	prefixOutput, prefixErr := s.runHomebrewCommand(brewPath, []string{"--prefix"})
	if prefixErr != nil {
		err := fmt.Errorf("brew --prefix failed: %s", sanitizeInstallerOutput(prefixErr.Error()))
		detail := fmt.Sprintf("OpenCode stale Homebrew cleanup failed before uninstall: stale=%s version=%s; brew=%s; %s; output=%s", stalePath, staleVersion, brewPath, err.Error(), sanitizeInstallerOutput(prefixOutput))
		return nil, detail, err
	}
	brewPrefix := strings.TrimSpace(firstNonEmptyLine(prefixOutput))
	if !openCodeHomebrewPathUnderPrefix(stalePath, brewPrefix) {
		err := fmt.Errorf("stale path %s is not under Homebrew prefix %s; refusing automatic cleanup", stalePath, brewPrefix)
		detail := fmt.Sprintf("OpenCode stale Homebrew cleanup skipped: brew=%s; %s", brewPath, err.Error())
		return nil, detail, err
	}

	listOutput, listErr := s.runHomebrewCommand(brewPath, []string{"list", "--versions", "opencode"})
	if listErr != nil || !homebrewListVersionsContainsOpenCode(listOutput) {
		errText := "brew list --versions opencode did not confirm an installed OpenCode formula"
		if listErr != nil {
			errText = fmt.Sprintf("brew list --versions opencode failed: %s", sanitizeInstallerOutput(listErr.Error()))
		}
		err := errors.New(errText)
		detail := fmt.Sprintf("OpenCode stale Homebrew cleanup failed before uninstall: stale=%s version=%s; brew=%s; brew --prefix=%s; list output=%s", stalePath, staleVersion, brewPath, brewPrefix, sanitizeInstallerOutput(listOutput))
		return nil, detail, err
	}

	uninstallOutput, uninstallErr := s.runHomebrewCommand(brewPath, []string{"uninstall", "opencode"})
	if uninstallErr != nil {
		err := fmt.Errorf("brew uninstall opencode failed: %s", sanitizeInstallerOutput(uninstallErr.Error()))
		detail := fmt.Sprintf("OpenCode stale Homebrew cleanup failed: stale=%s version=%s; brew=%s; brew --prefix=%s; brew list --versions opencode=%s; %s; brew uninstall opencode output=%s", stalePath, staleVersion, brewPath, brewPrefix, sanitizeInstallerOutput(listOutput), homebrewCleanupSafetyDetail, sanitizeInstallerOutput(uninstallOutput))
		return nil, detail, err
	}

	post, recheckDetail, recheckErr := s.recheckOpenCodeAfterHomebrewCleanup(npmCandidate, stalePath, staleVersion)
	baseDetail := fmt.Sprintf("stale=%s version=%s; npm candidate=%s version=%s; brew=%s; brew --prefix=%s; brew list --versions opencode=%s; %s; brew uninstall opencode output=%s; %s", stalePath, staleVersion, strings.TrimSpace(npmCandidate.ExecutablePath), strings.TrimSpace(npmCandidate.Version), brewPath, brewPrefix, sanitizeInstallerOutput(listOutput), homebrewCleanupSafetyDetail, sanitizeInstallerOutput(uninstallOutput), recheckDetail)
	if recheckErr != nil {
		return nil, "OpenCode stale Homebrew cleanup recheck failed: " + baseDetail, recheckErr
	}
	return post, baseDetail, nil
}

func (s *Service) tryCleanupCodexStaleNPMEntryAfterFallback(operation installOperation, beforeVersion string, effectiveAfter *CheckStatus, npmCandidate *CheckStatus) (*CheckStatus, string, error) {
	if npmCandidate == nil {
		return nil, "", nil
	}
	if err := validateHealthyCodexNPMCandidateForCleanup(operation, beforeVersion, npmCandidate); err != nil {
		return nil, fmt.Sprintf("Codex stale Homebrew npm cleanup skipped: npm candidate is not safe for cleanup: %s", sanitizeInstallerOutput(err.Error())), err
	}

	stale, staleErr := validateCodexStaleNPMEntry(effectiveAfter, npmCandidate)
	if staleErr != nil {
		return nil, fmt.Sprintf("Codex stale Homebrew npm cleanup skipped: %s", sanitizeInstallerOutput(staleErr.Error())), staleErr
	}

	cleanupOutput, cleanupErr := s.runCodexStaleNPMCleanup(stale.prefix)
	cleanupCommand := fmt.Sprintf("npm uninstall -g %s --prefix %s", codexNPMPackageName, stale.prefix)
	if cleanupErr != nil {
		err := fmt.Errorf("%s failed: %s", cleanupCommand, sanitizeInstallerOutput(cleanupErr.Error()))
		detail := fmt.Sprintf("Codex stale Homebrew npm cleanup failed: stale=%s version=%s; stale package root=%s; package.json=%s; npm candidate=%s version=%s; %s; output=%s", stale.path, stale.version, stale.packageRoot, stale.manifestPath, strings.TrimSpace(npmCandidate.ExecutablePath), strings.TrimSpace(npmCandidate.Version), err.Error(), sanitizeInstallerOutput(cleanupOutput))
		return nil, detail, err
	}

	post, recheckDetail, recheckErr := s.recheckCodexAfterStaleNPMCleanup(npmCandidate, stale.path, stale.version)
	baseDetail := fmt.Sprintf("stale=%s version=%s; stale package root=%s; package.json=%s; npm candidate=%s version=%s; %s; cleanup output=%s; %s", stale.path, stale.version, stale.packageRoot, stale.manifestPath, strings.TrimSpace(npmCandidate.ExecutablePath), strings.TrimSpace(npmCandidate.Version), cleanupCommand, sanitizeInstallerOutput(cleanupOutput), recheckDetail)
	if recheckErr != nil {
		return nil, "Codex stale Homebrew npm cleanup recheck failed: " + baseDetail, recheckErr
	}
	return post, baseDetail, nil
}

func validateHealthyOpenCodeNPMCandidateForCleanup(operation installOperation, beforeVersion string, status *CheckStatus) error {
	if status == nil || !status.Installed || !status.PATHOk || strings.TrimSpace(status.Error) != "" {
		return fmt.Errorf("npm global candidate not healthy: %s", verificationErrorMessage(status))
	}
	if strings.TrimSpace(status.ExecutablePath) == "" || strings.TrimSpace(status.Version) == "" {
		return fmt.Errorf("npm global candidate missing executable path or version")
	}
	if status.InstallMethod != InstallMethodNPM {
		return fmt.Errorf("candidate install method is %s, want npm", status.InstallMethod)
	}
	if operation == installOperationUpdate && strings.TrimSpace(beforeVersion) != "" && strings.TrimSpace(status.Version) == strings.TrimSpace(beforeVersion) {
		return fmt.Errorf("npm global candidate version unchanged at %s", beforeVersion)
	}
	return nil
}

func validateHealthyCodexNPMCandidateForCleanup(operation installOperation, beforeVersion string, status *CheckStatus) error {
	if status == nil || !status.Installed || !status.PATHOk || strings.TrimSpace(status.Error) != "" {
		return fmt.Errorf("npm global candidate not healthy: %s", verificationErrorMessage(status))
	}
	if strings.TrimSpace(status.ExecutablePath) == "" || strings.TrimSpace(status.Version) == "" {
		return fmt.Errorf("npm global candidate missing executable path or version")
	}
	if status.InstallMethod != InstallMethodNPM {
		return fmt.Errorf("candidate install method is %s, want npm", status.InstallMethod)
	}
	if operation == installOperationUpdate && strings.TrimSpace(beforeVersion) != "" && strings.TrimSpace(status.Version) == strings.TrimSpace(beforeVersion) {
		return fmt.Errorf("npm global candidate version unchanged at %s", beforeVersion)
	}
	return nil
}

type codexStaleNPMEntry struct {
	path         string
	version      string
	prefix       string
	packageRoot  string
	manifestPath string
}

func validateCodexStaleNPMEntry(effectiveAfter *CheckStatus, npmCandidate *CheckStatus) (codexStaleNPMEntry, error) {
	if effectiveAfter == nil || !effectiveAfter.Installed || strings.TrimSpace(effectiveAfter.ExecutablePath) == "" {
		return codexStaleNPMEntry{}, fmt.Errorf("default/effective Codex entry is missing; nothing to clean")
	}
	stalePath := strings.TrimSpace(effectiveAfter.ExecutablePath)
	candidatePath := strings.TrimSpace(npmCandidate.ExecutablePath)
	if sameNormalizedPath(stalePath, candidatePath) {
		return codexStaleNPMEntry{}, fmt.Errorf("default/effective Codex entry already matches npm candidate path")
	}
	staleVersion := strings.TrimSpace(effectiveAfter.Version)
	candidateVersion := strings.TrimSpace(npmCandidate.Version)
	if staleVersion != "" && candidateVersion != "" && staleVersion == candidateVersion {
		return codexStaleNPMEntry{}, fmt.Errorf("default/effective Codex entry version %s already matches npm candidate", staleVersion)
	}

	packageRoot, stalePrefix, rootErr := codexStaleNPMPackageRootAndPrefix(stalePath)
	if rootErr != nil {
		return codexStaleNPMEntry{}, rootErr
	}
	if !isHomebrewNPMGlobalPrefix(stalePrefix) {
		return codexStaleNPMEntry{}, fmt.Errorf("stale Codex prefix %s is not recognized as a Homebrew npm global prefix; refusing automatic cleanup", stalePrefix)
	}
	if !openCodeHomebrewPathUnderPrefix(stalePath, stalePrefix) {
		return codexStaleNPMEntry{}, fmt.Errorf("stale Codex path %s is not under inferred Homebrew prefix %s; refusing automatic cleanup", stalePath, stalePrefix)
	}

	manifestPath := filepath.Join(packageRoot, "package.json")
	manifestVersion, manifestErr := readCodexPackageJSONVersion(manifestPath)
	if manifestErr != nil {
		return codexStaleNPMEntry{}, fmt.Errorf("package.json guard failed for %s: %w", manifestPath, manifestErr)
	}
	if staleVersion == "" {
		staleVersion = manifestVersion
	}
	return codexStaleNPMEntry{
		path:         stalePath,
		version:      staleVersion,
		prefix:       stalePrefix,
		packageRoot:  packageRoot,
		manifestPath: manifestPath,
	}, nil
}

func codexStaleNPMPackageRootAndPrefix(stalePath string) (string, string, error) {
	path := strings.TrimSpace(stalePath)
	if path == "" {
		return "", "", fmt.Errorf("empty stale Codex path")
	}
	slashPath := filepath.ToSlash(filepath.Clean(path))
	normalized := strings.ToLower(slashPath)
	needle := "/node_modules/@openai/codex"
	idx := strings.Index(normalized, needle)
	if idx < 0 {
		return "", "", fmt.Errorf("stale Codex path %s does not point to node_modules/@openai/codex", stalePath)
	}
	packageRoot := filepath.Clean(slashPath[:idx+len(needle)])
	prefix := slashPath[:idx]
	if strings.HasSuffix(strings.ToLower(prefix), "/lib") {
		prefix = prefix[:len(prefix)-len("/lib")]
	}
	prefix = filepath.Clean(prefix)
	if prefix == "." || strings.TrimSpace(prefix) == "" {
		return "", "", fmt.Errorf("could not infer npm prefix from stale Codex path %s", stalePath)
	}
	return packageRoot, prefix, nil
}

func isHomebrewNPMGlobalPrefix(prefix string) bool {
	normalized := normalizeCodexPath(prefix)
	if normalized == "/opt/homebrew" || normalized == "/usr/local" {
		return true
	}
	segments := strings.Split(strings.Trim(normalized, "/"), "/")
	for _, segment := range segments {
		if segment == "homebrew" {
			return true
		}
	}
	return false
}

func readCodexPackageJSONVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if strings.TrimSpace(manifest.Name) != codexNPMPackageName {
		return "", fmt.Errorf("package manifest name is %q, want %q", strings.TrimSpace(manifest.Name), codexNPMPackageName)
	}
	version := parseCodexVersion(manifest.Version)
	if version == "" {
		return "", fmt.Errorf("package manifest %s did not contain a valid Codex version", path)
	}
	return version, nil
}

func (s *Service) runCodexStaleNPMCleanup(stalePrefix string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()
	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   s.resolveNPMPath(),
		Args:   []string{"uninstall", "-g", codexNPMPackageName, "--prefix", stalePrefix},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	return resultText(result), err
}

func validateOpenCodeHomebrewStaleEntry(effectiveAfter *CheckStatus, npmCandidate *CheckStatus) (string, string, error) {
	if effectiveAfter == nil || !effectiveAfter.Installed || strings.TrimSpace(effectiveAfter.ExecutablePath) == "" {
		return "", "", fmt.Errorf("default/effective OpenCode entry is missing; nothing to clean")
	}
	stalePath := strings.TrimSpace(effectiveAfter.ExecutablePath)
	candidatePath := strings.TrimSpace(npmCandidate.ExecutablePath)
	if sameNormalizedPath(stalePath, candidatePath) {
		return "", "", fmt.Errorf("default/effective OpenCode entry already matches npm candidate path")
	}
	staleVersion := strings.TrimSpace(effectiveAfter.Version)
	candidateVersion := strings.TrimSpace(npmCandidate.Version)
	if staleVersion != "" && candidateVersion != "" && staleVersion == candidateVersion {
		return "", "", fmt.Errorf("default/effective OpenCode entry version %s already matches npm candidate", staleVersion)
	}
	if !isOpenCodeHomebrewPath(normalizeOpenCodePath(stalePath)) {
		if staleVersion == "" {
			staleVersion = "unknown"
		}
		return "", "", fmt.Errorf("default/effective OpenCode entry is not recognized as Homebrew OpenCode: path=%s version=%s", stalePath, staleVersion)
	}
	if staleVersion == "" {
		staleVersion = "unknown"
	}
	return stalePath, staleVersion, nil
}

func (s *Service) resolveBrewPath() (string, error) {
	if path, err := exec.LookPath("brew"); err == nil && strings.TrimSpace(path) != "" {
		if abs, absErr := filepath.Abs(path); absErr == nil && filepath.IsAbs(abs) {
			return filepath.Clean(abs), nil
		}
	}
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, err := resolver.ResolveExecutable("brew", nil, s.buildEnhancedEnv())
	if err == nil && strings.TrimSpace(resolved.Path) != "" {
		path := filepath.Clean(strings.TrimSpace(resolved.Path))
		if filepath.IsAbs(path) {
			return path, nil
		}
		return "", fmt.Errorf("resolved brew path is not absolute: %s", path)
	}
	if err != nil {
		return "", fmt.Errorf("brew executable not found: %w", err)
	}
	return "", fmt.Errorf("brew executable not found")
}

func (s *Service) runHomebrewCommand(brewPath string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()
	env := s.buildEnhancedEnv()
	if isHomebrewOpenCodeUninstallCommand(args) {
		env = upsertEnvValue(env, homebrewNoAutoremoveEnv, homebrewCleanupSafetyEnvValue)
		env = upsertEnvValue(env, homebrewNoInstallCleanupEnv, homebrewCleanupSafetyEnvValue)
	}
	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   brewPath,
		Args:   append([]string(nil), args...),
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	return resultText(result), err
}

func isHomebrewOpenCodeUninstallCommand(args []string) bool {
	return len(args) == 2 && args[0] == "uninstall" && args[1] == "opencode"
}

func upsertEnvValue(env []string, key string, value string) []string {
	assignment := key + "=" + value
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && name == key {
			if !replaced {
				out = append(out, assignment)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, assignment)
	}
	return out
}

func openCodeHomebrewPathUnderPrefix(path string, prefix string) bool {
	for _, normalizedPath := range normalizedPathVariants(path) {
		for _, normalizedPrefix := range normalizedPathVariants(prefix) {
			normalizedPrefix = strings.TrimRight(normalizedPrefix, "/")
			if normalizedPath == "" || normalizedPrefix == "" {
				continue
			}
			if normalizedPath == normalizedPrefix || strings.HasPrefix(normalizedPath, normalizedPrefix+"/") {
				return true
			}
		}
	}
	return false
}

func normalizedPathVariants(path string) []string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	variants := []string{normalizeOpenCodePath(trimmed)}
	if abs, err := filepath.Abs(trimmed); err == nil {
		variants = append(variants, normalizeOpenCodePath(abs))
	}
	if realPath, err := filepath.EvalSymlinks(trimmed); err == nil {
		variants = append(variants, normalizeOpenCodePath(realPath))
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		if variant == "" {
			continue
		}
		if _, ok := seen[variant]; ok {
			continue
		}
		seen[variant] = struct{}{}
		out = append(out, variant)
	}
	return out
}

func homebrewListVersionsContainsOpenCode(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "opencode" {
			return true
		}
	}
	return false
}

func (s *Service) recheckOpenCodeAfterHomebrewCleanup(npmCandidate *CheckStatus, stalePath string, staleVersion string) (*CheckStatus, string, error) {
	wantVersion := strings.TrimSpace(npmCandidate.Version)
	var lastDetail string
	for attempt := 1; attempt <= boundedInstallRecheckAttempts(); attempt++ {
		if attempt > 1 {
			time.Sleep(installRecheckDelay)
		}
		post, err := s.CheckOne(ToolOpenCode)
		lastDetail = openCodeCleanupRecheckDetail(attempt, post, err)
		if err != nil || post == nil || !post.Installed || !post.PATHOk || strings.TrimSpace(post.Error) != "" {
			continue
		}
		postPath := strings.TrimSpace(post.ExecutablePath)
		postVersion := strings.TrimSpace(post.Version)
		if sameNormalizedPath(postPath, stalePath) {
			lastDetail = fmt.Sprintf("recheck attempt %d/%d still resolves stale Homebrew path=%s version=%s", attempt, boundedInstallRecheckAttempts(), postPath, postVersion)
			continue
		}
		if isOpenCodeHomebrewPath(normalizeOpenCodePath(postPath)) {
			lastDetail = fmt.Sprintf("recheck attempt %d/%d still resolves Homebrew OpenCode path=%s version=%s", attempt, boundedInstallRecheckAttempts(), postPath, postVersion)
			continue
		}
		if postVersion != wantVersion {
			lastDetail = fmt.Sprintf("recheck attempt %d/%d resolved path=%s version=%s, want npm candidate version=%s", attempt, boundedInstallRecheckAttempts(), postPath, postVersion, wantVersion)
			continue
		}
		return post, fmt.Sprintf("recheck attempt %d/%d path=%s version=%s; stale Homebrew path no longer effective", attempt, boundedInstallRecheckAttempts(), postPath, postVersion), nil
	}
	return nil, lastDetail, fmt.Errorf("cleanup completed but recheck did not confirm default/effective OpenCode entry moved from stale Homebrew %s version %s to npm candidate version %s", stalePath, staleVersion, wantVersion)
}

func (s *Service) recheckCodexAfterStaleNPMCleanup(npmCandidate *CheckStatus, stalePath string, staleVersion string) (*CheckStatus, string, error) {
	wantVersion := strings.TrimSpace(npmCandidate.Version)
	var lastDetail string
	for attempt := 1; attempt <= boundedInstallRecheckAttempts(); attempt++ {
		if attempt > 1 {
			time.Sleep(installRecheckDelay)
		}
		post, err := s.CheckOne(ToolCodex)
		lastDetail = codexCleanupRecheckDetail(attempt, post, err)
		if err != nil || post == nil || !post.Installed || !post.PATHOk || strings.TrimSpace(post.Error) != "" {
			continue
		}
		postPath := strings.TrimSpace(post.ExecutablePath)
		postVersion := strings.TrimSpace(post.Version)
		if sameNormalizedPath(postPath, stalePath) || codexPathsSharePackageRoot(postPath, stalePath) {
			lastDetail = fmt.Sprintf("recheck attempt %d/%d still resolves stale Homebrew npm Codex path=%s version=%s", attempt, boundedInstallRecheckAttempts(), postPath, postVersion)
			continue
		}
		if postVersion != wantVersion {
			lastDetail = fmt.Sprintf("recheck attempt %d/%d resolved path=%s version=%s, want npm candidate version=%s", attempt, boundedInstallRecheckAttempts(), postPath, postVersion, wantVersion)
			continue
		}
		return post, fmt.Sprintf("recheck attempt %d/%d path=%s version=%s; stale Homebrew npm Codex path no longer effective", attempt, boundedInstallRecheckAttempts(), postPath, postVersion), nil
	}
	return nil, lastDetail, fmt.Errorf("cleanup completed but recheck did not confirm default/effective Codex entry moved from stale Homebrew npm %s version %s to npm candidate version %s", stalePath, staleVersion, wantVersion)
}

func codexPathsSharePackageRoot(pathA string, pathB string) bool {
	rootA, _, errA := codexStaleNPMPackageRootAndPrefix(pathA)
	rootB, _, errB := codexStaleNPMPackageRootAndPrefix(pathB)
	if errA != nil || errB != nil {
		return false
	}
	return sameNormalizedPath(rootA, rootB)
}

func openCodeCleanupRecheckDetail(attempt int, status *CheckStatus, err error) string {
	if err != nil {
		return fmt.Sprintf("recheck attempt %d/%d failed: %s", attempt, boundedInstallRecheckAttempts(), sanitizeInstallerOutput(err.Error()))
	}
	if status == nil {
		return fmt.Sprintf("recheck attempt %d/%d returned empty status", attempt, boundedInstallRecheckAttempts())
	}
	return fmt.Sprintf("recheck attempt %d/%d path=%s version=%s installed=%v pathOk=%v error=%s", attempt, boundedInstallRecheckAttempts(), strings.TrimSpace(status.ExecutablePath), strings.TrimSpace(status.Version), status.Installed, status.PATHOk, sanitizeInstallerOutput(status.Error))
}

func codexCleanupRecheckDetail(attempt int, status *CheckStatus, err error) string {
	if err != nil {
		return fmt.Sprintf("recheck attempt %d/%d failed: %s", attempt, boundedInstallRecheckAttempts(), sanitizeInstallerOutput(err.Error()))
	}
	if status == nil {
		return fmt.Sprintf("recheck attempt %d/%d returned empty status", attempt, boundedInstallRecheckAttempts())
	}
	return fmt.Sprintf("recheck attempt %d/%d path=%s version=%s installed=%v pathOk=%v error=%s", attempt, boundedInstallRecheckAttempts(), strings.TrimSpace(status.ExecutablePath), strings.TrimSpace(status.Version), status.Installed, status.PATHOk, sanitizeInstallerOutput(status.Error))
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func npmGlobalCandidateConflictWithEffectiveEntry(toolDisplayName string, npmCandidate *CheckStatus, effectiveAfter *CheckStatus) error {
	if npmCandidate == nil {
		return fmt.Errorf("npm global prefix %s status was empty", toolDisplayName)
	}
	candidatePath := strings.TrimSpace(npmCandidate.ExecutablePath)
	candidateVersion := strings.TrimSpace(npmCandidate.Version)
	if effectiveAfter == nil || !effectiveAfter.Installed || strings.TrimSpace(effectiveAfter.ExecutablePath) == "" {
		return nil
	}
	effectivePath := strings.TrimSpace(effectiveAfter.ExecutablePath)
	if sameNormalizedPath(effectivePath, candidatePath) {
		return nil
	}
	effectiveVersion := strings.TrimSpace(effectiveAfter.Version)
	if effectiveVersion != "" && candidateVersion != "" && effectiveVersion == candidateVersion {
		return nil
	}
	if effectiveVersion == "" {
		effectiveVersion = "unknown"
	}
	return fmt.Errorf(
		"default/effective %s entry still resolves to %s version %s while npm global candidate is %s version %s; refusing to report update success because the actual command entry is not the updated npm candidate. 建议：调整 PATH 优先级、移除/更新旧安装源，或重启 shell/CodeBox 后重新检测",
		toolDisplayName,
		effectivePath,
		effectiveVersion,
		candidatePath,
		candidateVersion,
	)
}

func npmGlobalVerificationDetailWithEffectiveEntry(base string, npmCandidate *CheckStatus, effectiveAfter *CheckStatus) string {
	if npmCandidate == nil {
		return base
	}
	if effectiveAfter == nil || !effectiveAfter.Installed || strings.TrimSpace(effectiveAfter.ExecutablePath) == "" {
		return base + "; default/effective entry not found after npm command; accepted npm candidate because no stale default entry shadows it"
	}
	effectivePath := strings.TrimSpace(effectiveAfter.ExecutablePath)
	candidatePath := strings.TrimSpace(npmCandidate.ExecutablePath)
	if sameNormalizedPath(effectivePath, candidatePath) {
		return base + "; default/effective entry matches npm candidate path"
	}
	return fmt.Sprintf("%s; default/effective entry path=%s version=%s", base, effectivePath, strings.TrimSpace(effectiveAfter.Version))
}

func openCodeNPMGlobalVerificationDetail(status *CheckStatus, candidates []string, err error) string {
	return npmGlobalVerificationDetail("npm global prefix/bin 验证", status, candidates, err)
}

func codexNPMGlobalVerificationDetail(status *CheckStatus, candidates []string, err error) string {
	return npmGlobalVerificationDetail("Codex npm global prefix/bin 验证", status, candidates, err)
}

func claudeNPMGlobalVerificationDetail(status *CheckStatus, candidates []string, err error) string {
	return npmGlobalVerificationDetail("Claude Code npm global prefix/bin 验证", status, candidates, err)
}

func npmGlobalVerificationDetail(section string, status *CheckStatus, candidates []string, err error) string {
	parts := []string{section}
	if status != nil {
		if strings.TrimSpace(status.ExecutablePath) != "" {
			parts = append(parts, "path="+status.ExecutablePath)
		}
		if strings.TrimSpace(status.Version) != "" {
			parts = append(parts, "version="+strings.TrimSpace(status.Version))
		}
	}
	if len(candidates) > 0 {
		parts = append(parts, "candidates="+strings.Join(candidates, ","))
	}
	if err != nil {
		parts = append(parts, "error="+sanitizeInstallerOutput(err.Error()))
	}
	return strings.Join(parts, "; ")
}

func (s *Service) installClaudeCodeWithMethodProgress(method ClaudeInstallMethod, reporter progressReporter) (*InstallResult, error) {
	targetMethod, err := targetInstallMethodForClaude(method)
	if err != nil {
		return nil, err
	}

	// Self-heal pre-check: if a previous npm install was interrupted, npm
	// leaves behind @anthropic-ai/.claude-code-XXXXXX staging directories
	// that deadlock subsequent install attempts with ENOTEMPTY (errno -66).
	// Clear them BEFORE the conflict check so the new install can proceed.
	// This is a hard requirement for the "install self-heal" P0 capability.
	if cleaned, cleanErr := s.cleanClaudeNPMResidueIfPresent(); cleanErr != nil {
		// Surface but do not abort: the install itself may still succeed if
		// the conflict check / npm operation can proceed without hitting the
		// stale staging directory.
		reporter.report(OperationStepPrecheck, fmt.Sprintf("安装前清理 staging 残留时出错（已忽略，继续尝试安装）: %v", cleanErr), progressPrecheck)
	} else if cleaned != nil {
		reporter.report(OperationStepPrecheck, fmt.Sprintf("检测到上次 npm 安装中断的 staging 残留，已自动清理 %d 项", cleaned.Total()), progressPrecheck)
	}

	reporter.report(OperationStepPrecheck, fmt.Sprintf("正在检查 Claude Code %s 安装冲突...", claudeInstallMethodDisplayName(method)), progressPrecheck)
	conflictResult, conflictErr := s.ensureNoConflictInstall(targetMethod)
	if conflictErr != nil {
		return installFailure(ToolClaudeCode, fmt.Sprintf("Claude Code %s 安装前清理冲突失败", claudeInstallMethodDisplayName(method)), conflictErr), conflictErr
	}
	if conflictResult != nil {
		if !conflictResult.Success {
			return conflictResult, nil
		}
		reporter.report(OperationStepPrepare, conflictResult.Message, progressPrepare)
	}

	reporter.report(OperationStepPrepare, fmt.Sprintf("正在准备 Claude Code %s 安装...", claudeInstallMethodDisplayName(method)), progressPrepare)
	if method == ClaudeInstallNative {
		return s.installClaudeNativeViaNPMBootstrap(reporter)
	}
	return s.installOrUpdateWithProgress(ToolClaudeCode, installOperationInstall, reporter, method)
}

func (s *Service) installClaudeNativeViaNPMBootstrap(reporter progressReporter) (*InstallResult, error) {
	reporter.report(OperationStepPrecheck, "正在检查 Claude Code Native 当前状态...", progressPrecheck)

	before, checkErr := s.CheckOne(ToolClaudeCode)
	if checkErr != nil && before == nil {
		return installFailure(ToolClaudeCode, "Claude Code Native 安装前检查失败", checkErr), checkErr
	}
	if isUsableNativeClaudeStatus(before) {
		return &InstallResult{
			Success: true,
			Message: nativeInstallSuccessMessage("Claude Code Native 已安装可用，无需重新执行 npm + claude install", before),
			Tool:    ToolClaudeCode,
			Version: before.Version,
		}, nil
	}

	if err := s.ensureNPMAvailable(); err != nil {
		installErr := fmt.Errorf("npm 不可用，无法执行 Native 安装: %w", err)
		return s.nativeBootstrapFailureResult(installErr), installErr
	}

	reporter.report(OperationStepRunCommand, "Native 安装（npm + claude install）：正在安装 npm 版本 Claude Code，作为 Native 二进制安装引导...", progressNativeNPMInstall)
	npmCmd := s.resolveCommandNPMPath(npmClaudeCommand(installOperationInstall))
	npmResult, npmErr := s.runInstallCommandResult(npmCmd)
	if npmErr != nil {
		reporter.report(OperationStepVerify, "Native 安装（npm + claude install）：npm 安装命令异常，正在 bounded recheck 确认 npm 包是否已完成安装...", progressNativeVerify)
		if confirmErr := s.confirmClaudeNPMInstallAfterRecoverableCommand(npmCmd, npmResult, npmErr); confirmErr != nil {
			installErr := fmt.Errorf("安装 npm 版本 Claude Code 失败: %w", confirmErr)
			return s.nativeBootstrapFailureResult(installErr), installErr
		}
	} else if err := s.confirmClaudeNPMInstall(); err != nil {
		installErr := fmt.Errorf("npm 版本 Claude Code 安装后确认失败: %w", err)
		return s.nativeBootstrapFailureResult(installErr), installErr
	}
	if afterNPM, verifyErr := s.verifyClaudeNativeAvailable(); verifyErr == nil {
		return &InstallResult{
			Success: true,
			Message: nativeInstallSuccessMessage("Claude Code Native 已安装可用；npm 包引导检查完成后无需继续执行 claude install", afterNPM),
			Tool:    ToolClaudeCode,
			Version: afterNPM.Version,
		}, nil
	}

	reporter.report(OperationStepRunCommand, "Native 安装（npm + claude install）：正在执行 claude install 安装 Native 二进制...", progressNativeClaudeInstall)
	bootstrapCmd, bootstrapResolveErr := s.claudeNativeBootstrapCommandAfterNPMInstall()
	if bootstrapResolveErr != nil {
		installErr := fmt.Errorf("npm 版本 Claude Code 安装成功但无法定位 claude install 引导命令: %w", bootstrapResolveErr)
		return s.nativeBootstrapFailureResultAfterConfirmedNPM(installErr), installErr
	}
	bootstrapResult, bootstrapErr := s.runInstallCommandResult(bootstrapCmd)
	if bootstrapErr != nil {
		reporter.report(OperationStepVerify, "Native 安装（npm + claude install）：claude install 返回非零状态，正在 bounded recheck 确认 Native 二进制是否已可用...", progressNativeVerify)
		if after, verifyErr := s.verifyClaudeNativeInstallFromCommandOutput(resultText(bootstrapResult)); verifyErr == nil {
			return &InstallResult{
				Success: true,
				Message: nativeInstallSuccessMessage("Claude Code 已通过 npm + claude install 完成 Native 二进制安装；后续 shell integration 返回非零状态，已按 Location 验证二进制可用", after),
				Tool:    ToolClaudeCode,
				Version: after.Version,
			}, nil
		}
		if after, verifyErr := s.verifyClaudeNativeAvailableWithRecheck(resultText(bootstrapResult)); verifyErr == nil {
			return &InstallResult{
				Success: true,
				Message: nativeInstallSuccessMessage("Claude Code Native 在 claude install 返回异常后已验证可用；安装流程按已安装处理", after),
				Tool:    ToolClaudeCode,
				Version: after.Version,
			}, nil
		}
		installErr := fmt.Errorf("执行 claude install 失败: %w", bootstrapErr)
		return s.nativeBootstrapFailureResultAfterConfirmedNPM(installErr), installErr
	}

	reporter.report(OperationStepVerify, "Native 安装（npm + claude install）：正在验证 Native 二进制 Claude Code 可用...", progressNativeVerify)
	after, verifyErr := s.verifyClaudeNativeAvailableWithHint(resultText(bootstrapResult))
	if verifyErr != nil {
		installErr := fmt.Errorf("claude install 完成后 Native 验证失败: %w", verifyErr)
		return s.nativeBootstrapFailureResultAfterConfirmedNPM(installErr), installErr
	}

	// 5.11.F: once Native is verified, remove the npm channel residue so the
	// next detection run cannot accidentally prefer an npm shim over the
	// Native versions/ binary. This is best-effort -- failure here is logged
	// and surfaced in the success message but does not flip Success=false,
	// because the Native install itself has already succeeded.
	cleanupNote := s.cleanupNPMBinResidueAfterNativeInstall(reporter)

	return &InstallResult{
		Success: true,
		Message: nativeInstallSuccessMessage(nativeInstallSuccessWithNPMCleanup("Claude Code 已通过 npm + claude install 安装 Native 二进制", cleanupNote), after),
		Tool:    ToolClaudeCode,
		Version: after.Version,
	}, nil
}

// cleanupNPMBinResidueAfterNativeInstall removes the npm global bin entry for
// claude after a successful Native install. It is a best-effort cleanup: any
// error is logged and surfaced in the returned note but does NOT fail the
// overall install (Native has already been verified). Returns a short
// human-readable note; "" when no cleanup was needed.
func (s *Service) cleanupNPMBinResidueAfterNativeInstall(reporter progressReporter) string {
	cleaned, cleanErr := s.cleanClaudeNPMResidue()
	if cleanErr != nil {
		reporter.report(OperationStepVerify, fmt.Sprintf("Native 安装完成；清理 npm 残留时出错（已忽略）: %v", cleanErr), progressNativeVerify)
		return fmt.Sprintf("npm 残留清理失败（可稍后手动执行 npm uninstall -g @anthropic-ai/claude-code）: %v", cleanErr)
	}
	if cleaned == nil || cleaned.Total() == 0 {
		return ""
	}
	reporter.report(OperationStepVerify, fmt.Sprintf("Native 安装完成；已清理 npm 残留 %d 项以避免遮蔽 Native 二进制", cleaned.Total()), progressNativeVerify)
	return fmt.Sprintf("已清理 npm 残留 %d 项以避免遮蔽 Native 二进制", cleaned.Total())
}

func nativeInstallSuccessWithNPMCleanup(base string, cleanupNote string) string {
	if strings.TrimSpace(cleanupNote) == "" {
		return base
	}
	return base + "；" + cleanupNote
}

func (s *Service) nativeBootstrapFailureResult(installErr error) *InstallResult {
	summary := installerDiagnosticSummary(installErr)
	advice := nativeBootstrapFailureAdvice(summary, false)
	message := fmt.Sprintf(
		"Claude Code Native 安装失败：npm + claude install 未完成。诊断：%s。建议：%s",
		summary, advice,
	)
	return installFailure(ToolClaudeCode, message, errors.New(message))
}

func (s *Service) nativeBootstrapFailureResultAfterConfirmedNPM(installErr error) *InstallResult {
	summary := installerDiagnosticSummary(installErr)
	advice := nativeBootstrapFailureAdvice(summary, true)
	message := fmt.Sprintf(
		"Claude Code Native 引导失败：npm package @anthropic-ai/claude-code 已确认安装，失败发生在 claude install / downloads latest 检查阶段；若 Native 已可用，CodeBox 会在 bounded recheck 后按成功处理。诊断：%s。建议：%s",
		summary, advice,
	)
	return installFailure(ToolClaudeCode, message, errors.New(message))
}

func nativeBootstrapFailureAdvice(summary string, npmPackageConfirmed bool) string {
	if npmPackageConfirmed {
		if claudeInstallFailureLooksLikeLatestFetchTimeout(summary) {
			return "npm package 已确认安装，无需优先重复 npm install；claude install 在访问 downloads.claude.ai/claude-code-releases/latest 时超时。请优先检查网络连通性、DNS 与 HTTP_PROXY/HTTPS_PROXY/NO_PROXY 代理配置后重试 Native bootstrap；必要时在可信终端手动执行 claude install --force 覆盖 latest 检查。"
		}
		if strings.Contains(strings.ToLower(summary), "timeout") {
			return "npm package 已确认安装，无需优先重复 npm install；请优先检查网络/代理后重试 Native bootstrap，必要时在可信终端手动运行 claude install --force 重新触发 Native 引导。"
		}
		return "npm package 已确认安装，无需优先重复 npm install；请检查 claude install 输出、网络/代理与 PATH 后重试 Native bootstrap，必要时在可信终端手动运行 claude install --force。"
	}
	base := "确认 Node.js/npm 可用后重试，或手动执行 npm install -g @anthropic-ai/claude-code 后运行 claude install。"
	if claudeInstallFailureLooksLikeLatestFetchTimeout(summary) {
		return "claude install 在访问 downloads.claude.ai/claude-code-releases/latest 时超时；请检查网络连通性、DNS 与 HTTP_PROXY/HTTPS_PROXY/NO_PROXY 代理配置后重试；如本机已有 Native 二进制但状态检查被网络阻断，可在确认来源可信后手动执行 claude install --force 覆盖检查。" + base
	}
	if strings.Contains(strings.ToLower(summary), "timeout") {
		return "安装命令超时；请检查网络/代理后重试，必要时在终端手动运行 claude install --force 重新触发 Native 引导。" + base
	}
	return base
}

func claudeInstallFailureLooksLikeLatestFetchTimeout(summary string) bool {
	lower := strings.ToLower(summary)
	return strings.Contains(lower, "downloads.claude.ai") &&
		strings.Contains(lower, "claude-code-releases/latest") &&
		(strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"))
}

func installerDiagnosticSummary(err error) string {
	if err == nil {
		return "无错误详情"
	}
	text := sanitizeInstallerOutput(err.Error())
	if text == "" {
		return "无错误详情"
	}
	return text
}

func (s *Service) verifyClaudeNativeAvailable() (*CheckStatus, error) {
	return s.verifyClaudeNativeAvailableWithHint("")
}

func (s *Service) verifyClaudeNativeAvailableWithHint(installerOutput string) (*CheckStatus, error) {
	after, verifyErr := s.CheckOne(ToolClaudeCode)
	if isVerifiedNativeStatus(after, verifyErr) {
		return after, nil
	}

	if hinted, err := s.verifyClaudeNativeInstallFromCommandOutput(installerOutput); err == nil {
		return hinted, nil
	}
	for _, candidate := range claudeNativeDefaultExecutableCandidates() {
		if verified, err := s.verifyClaudeNativeExecutablePath(candidate, "native-default-location"); err == nil {
			return verified, nil
		}
	}

	// 5.11.B versions/ truth source fallback: claude install may write the
	// binary into versions/ before (or without) updating the shim. The
	// previous logic only verified via the shim or installer-output Location,
	// causing "claude install 完成后 Native 验证失败" while the install had
	// actually succeeded. Treat any healthy versions/ binary as a successful
	// install by verifying its version directly.
	for _, candidate := range healthyClaudeNativeVersionCandidates(0) {
		if verified, err := s.verifyClaudeNativeExecutablePath(candidate.Path, "native-versions-truth"); err == nil {
			return verified, nil
		}
	}

	if verifyErr != nil {
		return after, fmt.Errorf("verification call failed: %s", installerDiagnosticSummary(verifyErr))
	}
	if after == nil {
		return nil, errors.New("安装后工具状态为空")
	}
	if strings.TrimSpace(after.Error) != "" {
		return after, fmt.Errorf("%s", installerDiagnosticSummary(errors.New(after.Error)))
	}
	if !after.Installed {
		return after, errors.New("安装后未找到工具可执行文件")
	}
	if !after.PATHOk {
		return after, errors.New("工具在 PATH 之外被找到；请在 PATH 变更后重启应用程序或终端")
	}
	if after.InstallMethod != InstallMethodNative {
		return after, fmt.Errorf("安装后检测到 %s 安装方式，未检测到 Native 官方二进制", after.InstallMethod)
	}
	return after, nil
}

func isVerifiedNativeStatus(status *CheckStatus, err error) bool {
	return err == nil &&
		isUsableNativeClaudeStatus(status)
}

func isUsableNativeClaudeStatus(status *CheckStatus) bool {
	return status != nil &&
		status.Installed &&
		status.PATHOk &&
		status.InstallMethod == InstallMethodNative &&
		strings.TrimSpace(status.Error) == ""
}

func (s *Service) verifyClaudeNativeInstallFromCommandOutput(output string) (*CheckStatus, error) {
	if !claudeInstallerOutputHasSuccess(output) {
		return nil, errors.New("installer output did not contain Claude Code native success marker")
	}
	location := parseClaudeInstallerLocation(output)
	if location == "" {
		return nil, errors.New("installer output did not contain Claude Code native Location")
	}
	return s.verifyClaudeNativeExecutablePath(location, "installer-location")
}

func claudeInstallerOutputHasSuccess(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "claude code successfully installed") ||
		strings.Contains(lower, "successfully installed")
}

func parseClaudeInstallerLocation(output string) string {
	matches := claudeInstallerLocationPattern.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	location := strings.TrimSpace(matches[1])
	location = strings.Trim(location, `"'`)
	return filepath.Clean(location)
}

func (s *Service) verifyClaudeNativeExecutablePath(executablePath string, pathSource string) (*CheckStatus, error) {
	cleaned := strings.TrimSpace(executablePath)
	if cleaned == "" {
		return nil, errors.New("Native Location 为空")
	}
	if !fileExists(cleaned) {
		return nil, fmt.Errorf("Native Location 文件不存在: %s", cleaned)
	}
	realPath := resolveRealExecutablePath(cleaned)
	version, err := s.claudeVersion(realPath)
	if err != nil {
		return nil, err
	}
	status := &CheckStatus{
		Tool:           ToolClaudeCode,
		Installed:      true,
		InstallMethod:  s.detectClaudeInstallMethod(realPath),
		Version:        version,
		PATHOk:         true,
		SystemPATHOk:   pathDirInProcessPATH(filepath.Dir(realPath)),
		PathState:      PathStateOutsidePATH,
		PathSource:     pathSource,
		ExecutablePath: realPath,
		CheckedAt:      time.Now(),
	}
	if status.SystemPATHOk {
		status.PathState = PathStateSystemPATH
	}
	if status.InstallMethod != InstallMethodNative {
		return status, fmt.Errorf("Location 指向 %s 安装方式，未检测到 Native 官方二进制: %s", status.InstallMethod, realPath)
	}
	if !status.SystemPATHOk {
		applyPathStateToStatus(status, resolveResult{
			executablePath: realPath,
			systemPATHOk:   false,
			pathState:      status.PathState,
			pathSource:     pathSource,
		}, ToolClaudeCode)
	}
	return status, nil
}

func pathDirInProcessPATH(dir string) bool {
	normalizedDir := normalizeClaudePath(dir)
	if normalizedDir == "" {
		return false
	}
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if normalizeClaudePath(entry) == normalizedDir {
			return true
		}
	}
	return false
}

func nativeInstallSuccessMessage(prefix string, status *CheckStatus) string {
	if status == nil {
		return prefix
	}
	details := []string{}
	if strings.TrimSpace(status.ExecutablePath) != "" {
		details = append(details, "Path: "+status.ExecutablePath)
	}
	if strings.TrimSpace(status.Version) != "" {
		details = append(details, "Version: "+status.Version)
	}
	if !status.SystemPATHOk {
		details = append(details, "提示：安装已成功，但当前进程 PATH 尚未包含 Native 目录；CodeBox 本次会话已使用绝对路径/增强 PATH 验证，可重启终端或修复 PATH 后在外部终端使用 claude")
	}
	if len(details) == 0 {
		return prefix
	}
	return prefix + "（" + strings.Join(details, "；") + "）"
}

func targetInstallMethodForClaude(method ClaudeInstallMethod) (InstallMethod, error) {
	switch method {
	case ClaudeInstallNPM:
		return InstallMethodNPM, nil
	case ClaudeInstallNative:
		return InstallMethodNative, nil
	default:
		return InstallMethodUnknown, fmt.Errorf("unsupported method: %s", method)
	}
}

func claudeInstallMethodDisplayName(method ClaudeInstallMethod) string {
	switch method {
	case ClaudeInstallNPM:
		return "npm"
	case ClaudeInstallNative:
		return "Native (npm + claude install)"
	default:
		return string(method)
	}
}

func (s *Service) installCommands(tool CLITool, operation installOperation, current *CheckStatus, method ClaudeInstallMethod) ([]installCommand, error) {
	switch tool {
	case ToolClaudeCode:
		// If the user explicitly selected an install method, use only that method
		// (no fallback chain).
		if method != ClaudeInstallAuto {
			cmd, err := claudeInstallCommandsForMethod(method, operation)
			if err != nil {
				return nil, err
			}
			if err := s.ensureNPMAvailable(); err != nil {
				return nil, err
			}
			cmd = s.resolveCommandNPMPath(cmd)
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
			return nil, err
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
	case ToolHeadroom:
		// Headroom is installed into a CodeBox-managed venv to avoid PEP 668
		// (externally-managed-environment) which blocks bare `pip install` on
		// Homebrew/system Python. Require python3 (for venv creation), ensure
		// the venv exists, then install headroom-ai[proxy] via the venv's pip.
		if err := s.ensurePythonAvailable(); err != nil {
			return nil, err
		}
		if err := s.ensureHeadroomVenv(); err != nil {
			return nil, err
		}
		return []installCommand{s.headroomVenvInstallCommand(operation)}, nil
	default:
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}
}

// npmClaudeCommand returns the npm command for Claude Code.
// For updates, uses "install -g @latest" which is more reliable than
// "update -g": npm update respects version ranges in package.json and may
// not actually upgrade to the latest version, whereas install @latest
// forces the newest release.
//
// Self-heal (P0-4): Claude Code distributes a ~206MB native binary via the
// platform subpackage. The default 120s install timeout was identified as
// a P0 root cause of interrupted installs (the install gets killed mid-
// extract, leaving staging directories and truncated binaries that trigger
// the ENOTEMPTY / AMFI SIGKILL cascade). The command therefore pins a
// longer claudeNPMInstallTimeout that gives slow networks enough headroom
// to download and hardlink the binary without being killed.
func npmClaudeCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm install @anthropic-ai/claude-code@latest",
			path:        "npm",
			args:        []string{"install", "-g", "@anthropic-ai/claude-code@latest"},
			timeout:     claudeNPMInstallTimeout,
		}
	}
	return installCommand{
		description: "npm global install @anthropic-ai/claude-code",
		path:        "npm",
		args:        []string{"install", "-g", "@anthropic-ai/claude-code"},
		timeout:     claudeNPMInstallTimeout,
	}
}

// npmOpenCodeCommand returns the npm install or update command for OpenCode.
// For updates, use "install -g opencode-ai@latest" instead of
// "update -g opencode-ai". npm update can be a no-op for global packages when
// the installed package still satisfies npm's recorded range, which leaves the
// opencode shim at the old version and causes post-update verification to fail.
func npmOpenCodeCommand(operation installOperation) installCommand {
	if operation == installOperationUpdate {
		return installCommand{
			description: "npm global install opencode-ai@latest",
			path:        "npm",
			args:        []string{"install", "-g", "opencode-ai@latest"},
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

// claudeInstallCommandsForMethod returns the npm command for the user-specified
// npm method. Native installs are intentionally handled by
// installClaudeNativeViaNPMBootstrap because they require two ordered commands:
// npm install followed by `claude install`.
func claudeInstallCommandsForMethod(method ClaudeInstallMethod, operation installOperation) (installCommand, error) {
	switch method {
	case ClaudeInstallNPM:
		return npmClaudeCommand(operation), nil
	case ClaudeInstallNative:
		return installCommand{}, fmt.Errorf("Claude Code native installation uses ordered npm + claude install commands")
	default:
		return installCommand{}, fmt.Errorf("unsupported Claude Code install method: %s", method)
	}
}

func (s *Service) claudeInstallCommands(operation installOperation, current *CheckStatus) ([]installCommand, error) {
	if operation == installOperationUpdate && current != nil {
		switch current.InstallMethod {
		case InstallMethodNPM, InstallMethodNative:
			return []installCommand{npmClaudeCommand(installOperationUpdate)}, nil
		default:
			return unknownClaudeUpdateCommands(), nil
		}
	}

	return []installCommand{npmClaudeCommand(operation)}, nil
}

func unknownClaudeUpdateCommands() []installCommand {
	return []installCommand{npmClaudeCommand(installOperationUpdate)}
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
	if currentMethod == "" {
		currentMethod = InstallMethodUnknown
	}

	// Different or unknown method installed -- clean it first. Unknown cleanup is
	// limited to safe npm/native residue handling and never invokes removed winget
	// or direct installer paths.
	cleanResult, cleanErr := cleaner(currentMethod)
	if cleanErr != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("检测到已有 %s 安装的 Claude Code，自动清理失败: %v。请手动卸载后重试", currentMethod, cleanErr),
			Tool:    ToolClaudeCode,
		}, cleanErr
	}
	if cleanResult == nil {
		return &InstallResult{
			Success: true,
			Message: fmt.Sprintf("已自动清理 %s 安装的 Claude Code，将继续使用 %s 安装", currentMethod, targetMethod),
			Tool:    ToolClaudeCode,
		}, nil
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
		Message: fmt.Sprintf("已自动清理 %s 安装的 Claude Code，将继续使用 %s 安装", currentMethod, targetMethod),
		Tool:    ToolClaudeCode,
	}, nil
}

func claudeNativeBootstrapCommand() installCommand {
	return installCommand{
		description: "claude install native bootstrap",
		path:        "claude",
		args:        []string{"install"},
		timeout:     claudeNativeBootstrapCommandTimeout,
	}
}

// claudeBootstrapShimRetryDelay is the pause between attempts to locate the
// npm shim after `npm install -g` returns. The value is intentionally short
// -- npm typically finishes flushing the shim within tens of milliseconds,
// and we cap the total retry window at a few hundred ms. Declared as a
// variable so tests can zero it out to keep the test runtime bounded.
var claudeBootstrapShimRetryDelay = 200 * time.Millisecond

// claudeBootstrapShimMaxAttempts bounds the number of times
// resolveClaudeNPMBootstrapPath retries scanning prefix/bin/claude after npm
// install. Declared as a variable so tests can shrink it.
var claudeBootstrapShimMaxAttempts = 3

func claudeBootstrapShimRetryAttempts() int {
	if claudeBootstrapShimMaxAttempts <= 0 {
		return 1
	}
	return claudeBootstrapShimMaxAttempts
}

func (s *Service) claudeNativeBootstrapCommandAfterNPMInstall() (installCommand, error) {
	cmd := claudeNativeBootstrapCommand()
	resolvedPath, source, err := s.resolveClaudeNPMBootstrapPath()
	if err != nil {
		return installCommand{}, err
	}
	cmd.path = resolvedPath
	cmd.description = fmt.Sprintf("claude install native bootstrap via %s", source)
	return cmd, nil
}

func (s *Service) resolveClaudeNPMBootstrapPath() (string, string, error) {
	// 5.11.J fsync race: npm returns from "install -g" before the new bin
	// shim is fully flushed to disk. The first scan of prefix/bin/claude can
	// therefore see "file does not exist" even though the install genuinely
	// succeeded. Retry a bounded number of times with a short sleep so the
	// shim has time to appear. Total wait is bounded by
	// claudeBootstrapShimRetryAttempts * claudeBootstrapShimRetryDelay and
	// stays well under the user-visible "claude install" latency.
	for attempt := 1; attempt <= claudeBootstrapShimRetryAttempts(); attempt++ {
		if attempt > 1 {
			time.Sleep(claudeBootstrapShimRetryDelay)
		}
		if path, source := s.resolveClaudeFromNPMGlobalPrefix(); path != "" {
			return path, source, nil
		}
	}

	env := s.buildEnhancedEnv()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, diagnostics, err := resolver.ResolveExecutable(claudeCommandName, nil, env)
	if err == nil && strings.TrimSpace(resolved.Path) != "" {
		invocationPath := cleanExecutableInvocationPath(resolved.Path)
		realPath := resolveRealExecutablePath(invocationPath)
		if s.detectClaudeInstallMethod(realPath) == InstallMethodNative {
			return "", "", fmt.Errorf("npm 全局包已安装，但解析到的 claude 为 Native 路径 %s，未找到 npm shim；请确认 npm global bin 已创建并重试", realPath)
		}
		source := diagnostics.CLISource
		if strings.TrimSpace(source) == "" {
			source = "resolver"
		}
		return preserveClaudeBootstrapInvocationPath(invocationPath, realPath), source, nil
	}

	detail := ""
	if err != nil {
		detail = ": " + sanitizeInstallerOutput(err.Error())
	}
	return "", "", fmt.Errorf("npm 全局包已安装，但当前 PATH 未刷新且 npm global prefix/bin 下未找到 claude 可执行文件%s", detail)
}

func preserveClaudeBootstrapInvocationPath(invocationPath string, detectionPath string) string {
	if shouldPreserveDarwinClaudeShimInvocationPath(invocationPath, detectionPath) {
		return invocationPath
	}
	return detectionPath
}

func (s *Service) resolveClaudeFromNPMGlobalPrefix() (string, string) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil || strings.TrimSpace(prefix) == "" {
		return "", ""
	}
	npmRoot, _ := s.npmGlobalRootWithPrefixFallback()
	for _, candidate := range claudeNPMGlobalExecutableCandidateInfos(prefix, npmRoot) {
		if fileExists(candidate.path) {
			return filepath.Clean(candidate.path), candidate.source
		}
	}
	return "", ""
}

func (s *Service) npmGlobalPrefix() (string, error) {
	npmPath := s.resolveNPMPath()
	ctx, cancel := context.WithTimeout(context.Background(), claudeNPMCheckTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"prefix", "-g"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("npm prefix -g: %s", sanitizeInstallerOutput(message))
	}
	prefix := firstNonEmptyLine(resultText(result))
	if strings.TrimSpace(prefix) == "" {
		return "", errors.New("npm prefix -g returned empty prefix")
	}
	return filepath.Clean(strings.TrimSpace(prefix)), nil
}

// npmGlobalRoot runs `npm root -g` and returns the absolute path of the global
// node_modules directory. This is the authoritative location where npm places
// globally-installed packages; on standard Unix layouts it differs from
// `<prefix>/node_modules` (it is `<prefix>/lib/node_modules`), so callers that
// need to locate a scoped package directory under node_modules MUST use this
// resolver rather than deriving the path from npmGlobalPrefix().
//
// See https://docs.npmjs.com/cli/v10/configuring-npm/folders for the layout.
func (s *Service) npmGlobalRoot() (string, error) {
	npmPath := s.resolveNPMPath()
	ctx, cancel := context.WithTimeout(context.Background(), claudeNPMCheckTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"root", "-g"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("npm root -g: %s", sanitizeInstallerOutput(message))
	}
	root := firstNonEmptyLine(resultText(result))
	if strings.TrimSpace(root) == "" {
		return "", errors.New("npm root -g returned empty root")
	}
	return filepath.Clean(strings.TrimSpace(root)), nil
}

// npmGlobalRootWithPrefixFallback resolves the global node_modules directory.
// It prefers the authoritative `npm root -g` result; when that fails it falls
// back to inferring the layout from the npm prefix (which is almost always
// resolvable even in degraded environments). Returns the directory path and a
// flag indicating whether the value came from the authoritative source.
//
// Callers should use this whenever they previously derived node_modules by
// joining the npm prefix with "node_modules" -- that join is wrong on standard
// Unix layouts where the real location is <prefix>/lib/node_modules.
func (s *Service) npmGlobalRootWithPrefixFallback() (root string, authoritative bool) {
	if resolved, err := s.npmGlobalRoot(); err == nil && strings.TrimSpace(resolved) != "" {
		return resolved, true
	}
	prefix, err := s.npmGlobalPrefix()
	if err != nil || strings.TrimSpace(prefix) == "" {
		return "", false
	}
	return inferNPMNodeModulesFromPrefix(prefix), false
}

// inferNPMNodeModulesFromPrefix derives the global node_modules directory from
// an npm prefix using the platform's standard npm layout. On Unix this is
// <prefix>/lib/node_modules; on Windows it is <prefix>/node_modules. This is
// only a fallback for when `npm root -g` cannot be invoked.
func inferNPMNodeModulesFromPrefix(prefix string) string {
	prefix = filepath.Clean(strings.TrimSpace(prefix))
	if prefix == "" || prefix == "." {
		return ""
	}
	if isWindows() {
		return filepath.Join(prefix, "node_modules")
	}
	return filepath.Join(prefix, "lib", "node_modules")
}

type claudeNPMGlobalExecutableCandidate struct {
	path   string
	source string
}

// claudeNPMGlobalExecutableCandidates enumerates the plausible claude
// executable paths for a given npm prefix and global node_modules root.
// npmRoot is the directory reported by `npm root -g` (or inferred from the
// prefix when empty); prefix is the result of `npm prefix -g`. The two are
// distinct on standard Unix layouts (<prefix>/lib/node_modules vs
// <prefix>/node_modules), so passing only the prefix used to silently miss the
// real install location.
func claudeNPMGlobalExecutableCandidates(prefix string, npmRoot string) []string {
	infos := claudeNPMGlobalExecutableCandidateInfos(prefix, npmRoot)
	candidates := make([]string, 0, len(infos))
	for _, info := range infos {
		candidates = append(candidates, info.path)
	}
	return candidates
}

func claudeNPMGlobalExecutableCandidateInfos(prefix string, npmRoot string) []claudeNPMGlobalExecutableCandidate {
	prefix = filepath.Clean(strings.TrimSpace(prefix))
	if prefix == "" || prefix == "." {
		return nil
	}
	npmRoot = filepath.Clean(strings.TrimSpace(npmRoot))

	// bin stays under prefix (it is <prefix>/bin, not under node_modules).
	// The .bin shim directory, however, lives inside the global node_modules
	// root, so it must be derived from npmRoot -- not prefix/node_modules.
	dotBin := ""
	if npmRoot != "" && npmRoot != "." {
		dotBin = filepath.Join(npmRoot, ".bin")
	} else {
		dotBin = filepath.Join(inferNPMNodeModulesFromPrefix(prefix), ".bin")
	}
	dirs := []string{filepath.Join(prefix, "bin"), prefix, dotBin}
	names := []string{"claude"}
	if isWindows() {
		names = []string{"claude.cmd", "claude.exe", "claude"}
	}

	candidates := make([]claudeNPMGlobalExecutableCandidate, 0, len(dirs)*len(names))
	seen := map[string]struct{}{}

	appendCandidate := func(path string, source string) {
		key := normalizeClaudePath(path)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, claudeNPMGlobalExecutableCandidate{
			path:   path,
			source: source,
		})
	}

	for _, dir := range dirs {
		for _, name := range names {
			appendCandidate(filepath.Join(dir, name), "npm global prefix")
		}
	}
	// Self-heal: gate the npm package-binary fallback by an on-disk integrity
	// check. If a previous install was interrupted mid-extract, npm may leave
	// a truncated unsigned shard under .claude-code-*/node_modules/@anthropic-ai/
	// that is hardlinked into bin/claude. Without this filter the checker would
	// happily report the shard as a working Claude Code binary and mask the
	// corruption from the user (and from the install pre-check).
	for _, candidate := range claudeNPMPackageBinaryFallbackCandidates(npmRoot) {
		if !claudeBinaryCandidateLooksHealthy(candidate) {
			continue
		}
		appendCandidate(candidate, "npm package binary fallback")
	}
	return candidates
}

// claudeBinaryCandidateLooksHealthy returns true when the candidate either
// does not exist (callers will handle absence elsewhere) or passes the
// integrity inspection (size threshold + macOS codesign). Truncated shards
	// from interrupted installs are rejected here.
func claudeBinaryCandidateLooksHealthy(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	info, err := os.Stat(candidate)
	if err != nil {
		// Missing file is acceptable -- the caller iterates many candidates.
		return true
	}
	if info.IsDir() {
		return false
	}
	report := InspectClaudeBinaryIntegrity(candidate)
	return report.Exists && !report.Corrupted
}

// claudeNPMPackageBinaryFallbackCandidates enumerates the platform-specific
// claude binaries that ship inside the @anthropic-ai npm packages under the
// global node_modules root. npmRoot is the directory reported by `npm root -g`
// (or inferred from the prefix by callers); the scoped package directory lives
// directly under it as <npmRoot>/@anthropic-ai.
func claudeNPMPackageBinaryFallbackCandidates(npmRoot string) []string {
	npmRoot = filepath.Clean(strings.TrimSpace(npmRoot))
	if npmRoot == "" || npmRoot == "." {
		return nil
	}

	packageNames, executableNames := claudeNPMPackageBinaryNames()
	if len(packageNames) == 0 || len(executableNames) == 0 {
		return nil
	}

	scoped := filepath.Join(npmRoot, "@anthropic-ai")
	roots := []string{
		scoped,
		filepath.Join(scoped, "claude-code", "node_modules", "@anthropic-ai"),
	}
	if matches, err := filepath.Glob(filepath.Join(scoped, ".claude-code-*", "node_modules", "@anthropic-ai")); err == nil {
		roots = append(roots, matches...)
	}

	candidates := []string{}
	seen := map[string]struct{}{}
	for _, root := range roots {
		for _, packageName := range packageNames {
			for _, executableName := range executableNames {
				candidate := filepath.Join(root, packageName, executableName)
				key := normalizeClaudePath(candidate)
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, candidate)
			}
		}
	}
	return candidates
}

func claudeNPMPackageBinaryNames() ([]string, []string) {
	if isWindows() {
		return []string{"claude-code-win32-arm64", "claude-code-win32-x64"}, []string{"claude.exe"}
	}
	switch runtimeGOOS {
	case "darwin":
		return []string{"claude-code-darwin-arm64", "claude-code-darwin-x64"}, []string{"claude"}
	case "linux":
		return []string{"claude-code-linux-arm64", "claude-code-linux-x64"}, []string{"claude"}
	default:
		return nil, nil
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
	message := sanitizeInstallerOutput(resultText(result))
	if message == "" {
		message = sanitizeInstallerOutput(err.Error())
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
	npmPath := "npm"
	if resolveErr != nil || strings.TrimSpace(resolved.Path) == "" {
		// The platform resolver is intentionally conservative. Fall through to a
		// runner probe with the bare command so OS-level command resolution and
		// injected test runners can still prove npm availability without requiring
		// resolver-visible filesystem evidence.
		if resolveErr != nil {
			s.npmResolvedErr = fmt.Errorf("%s；请安装 Node.js (https://nodejs.org) 并确保 npm 在 PATH 中", resolveErr.Error())
		}
	} else {
		npmPath = resolved.Path
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, runErr := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"--version"},
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	if runErr != nil {
		s.npmAvailable = false
		detail := sanitizeInstallerOutput(resultText(result))
		if detail == "" {
			detail = sanitizeInstallerOutput(runErr.Error())
		}
		// Detect "node not found" specifically
		if strings.Contains(detail, "node: No such file") || strings.Contains(detail, "env: node: No such file") {
			s.npmResolvedErr = fmt.Errorf("在 %s 找到 npm 但 node 不在 PATH 中: %s（建议安装 Node.js 或修复 PATH）", npmPath, detail)
		} else {
			if strings.TrimSpace(detail) == "" && resolveErr != nil {
				detail = resolveErr.Error()
			}
			s.npmResolvedErr = fmt.Errorf("在 %s 找到 npm 但无法正常运行: %s", npmPath, detail)
		}
		return
	}
	s.npmAvailable = true
	s.npmResolvedErr = nil
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

// headroomVenvInstallCommand returns the venv-pip command for Headroom. Uses
// the venv's python -m pip so the install is immune to PEP 668. Installs
// `headroom-ai[proxy]` (only the proxy extra CodeBox needs, not [all]);
// updates use `-U` to force an upgrade.
func (s *Service) headroomVenvInstallCommand(operation installOperation) installCommand {
	pythonPath := headroomVenvPythonPath(s.headroomVenvDir)
	if operation == installOperationUpdate {
		return installCommand{
			description: "venv pip install --upgrade headroom-ai[proxy]",
			path:        pythonPath,
			args:        []string{"-m", "pip", "install", "-U", "headroom-ai[proxy]"},
			timeout:     headroomPipInstallTimeout,
		}
	}
	return installCommand{
		description: "venv pip install headroom-ai[proxy]",
		path:        pythonPath,
		args:        []string{"-m", "pip", "install", "headroom-ai[proxy]"},
		timeout:     headroomPipInstallTimeout,
	}
}

// headroomVenvBinDir returns the platform-specific bin directory inside the
// venv ("<dir>/bin" on POSIX, "<dir>/Scripts" on Windows). Returns "" when
// venvDir is empty.
func headroomVenvBinDir(venvDir string) string {
	venvDir = strings.TrimSpace(venvDir)
	if venvDir == "" {
		return ""
	}
	if isWindows() {
		return filepath.Join(venvDir, "Scripts")
	}
	return filepath.Join(venvDir, "bin")
}

// headroomVenvPythonPath returns the absolute path to the venv's python
// executable.
func headroomVenvPythonPath(venvDir string) string {
	binDir := headroomVenvBinDir(venvDir)
	if binDir == "" {
		return ""
	}
	if isWindows() {
		return filepath.Join(binDir, "python.exe")
	}
	return filepath.Join(binDir, "python")
}

// headroomVenvBinDir is the Service-level helper used by buildEnhancedEnv and
// detection to derive the venv bin directory from the injected root.
func (s *Service) headroomVenvBinDir() string {
	return headroomVenvBinDir(s.headroomVenvDir)
}

// resolvePythonPath returns the absolute path to python3, preferring "python3"
// then "python", falling back to the bare "python3" name. Mirrors resolveNPMPath.
func (s *Service) resolvePythonPath() string {
	env := s.buildEnhancedEnv()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	for _, name := range []string{"python3", "python"} {
		resolved, _, err := resolver.ResolveExecutable(name, nil, env)
		if err == nil && strings.TrimSpace(resolved.Path) != "" {
			return resolved.Path
		}
	}
	return "python3"
}

// ensurePythonAvailable probes python3 availability (cached once per service
// lifetime via pythonOnce). Returns an error explaining how to remediate when
// python3 is not found. Mirrors ensureNPMAvailable.
func (s *Service) ensurePythonAvailable() error {
	s.pythonOnce.Do(func() {
		s.probePythonAvailability()
	})
	if s.pythonAvailable {
		return nil
	}
	if s.pythonResolvedErr != nil {
		return s.pythonResolvedErr
	}
	return fmt.Errorf("python3 未在 PATH 中找到。请安装 Python 3 (https://www.python.org) 并确保 python3 可用，然后重启 CodeBox。")
}

// probePythonAvailability performs the actual python3 availability check and
// stores the result in the service's cache fields. Called exactly once via
// pythonOnce. It prefers "python3" then "python" and verifies the binary
// actually runs. python3 availability implies venv-creation capability (the
// venv module ships with the standard library).
func (s *Service) probePythonAvailability() {
	env := s.buildEnhancedEnv()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	for _, name := range []string{"python3", "python"} {
		resolved, _, resolveErr := resolver.ResolveExecutable(name, nil, env)
		if resolveErr != nil || strings.TrimSpace(resolved.Path) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, runErr := s.processRunner.Run(ctx, platform.CommandSpec{
			Path:   resolved.Path,
			Args:   []string{"--version"},
			Env:    env,
			Policy: platform.DefaultProcessPolicy(),
		})
		cancel()
		if runErr == nil {
			s.pythonAvailable = true
			s.pythonResolvedErr = nil
			return
		}
		detail := sanitizeInstallerOutput(resultText(result))
		if detail == "" {
			detail = sanitizeInstallerOutput(runErr.Error())
		}
		s.pythonResolvedErr = fmt.Errorf("在 %s 找到 %s 但无法正常运行: %s", resolved.Path, name, detail)
		return
	}
	s.pythonResolvedErr = fmt.Errorf("python3/python 未在 PATH 中找到；请安装 Python 3 (https://www.python.org) 并确保 python3 可用")
}

// ensureHeadroomVenv creates the CodeBox-managed headroom venv if it does not
// already exist. Idempotent: when the venv directory already contains a
// runnable python executable, creation is skipped. This is a prepare-phase
// side effect (precedent: ensurePythonAvailable / ensureNPMAvailable running
// probes during installCommands).
func (s *Service) ensureHeadroomVenv() error {
	venvDir := strings.TrimSpace(s.headroomVenvDir)
	if venvDir == "" {
		return fmt.Errorf("headroom venv 目录未配置")
	}
	if headroomVenvPythonExists(venvDir) {
		return nil
	}
	pythonPath := s.resolvePythonPath()
	ctx, cancel := context.WithTimeout(context.Background(), headroomVenvCreateTimeout)
	defer cancel()
	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   pythonPath,
		Args:   []string{"-m", "venv", venvDir},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("创建 headroom venv 失败 (%s -m venv %s): %s", pythonPath, venvDir, sanitizeInstallerOutput(message))
	}
	return nil
}

// headroomVenvPythonExists reports whether the venv directory already contains
// a runnable python executable. Used by ensureHeadroomVenv for idempotency.
func headroomVenvPythonExists(venvDir string) bool {
	pythonPath := headroomVenvPythonPath(venvDir)
	return pythonPath != "" && fileExists(pythonPath)
}

func (s *Service) runInstallCommand(command installCommand) error {
	_, err := s.runInstallCommandResult(command)
	return err
}

func (s *Service) runInstallCommandResult(command installCommand) (*platform.ProcessResult, error) {
	timeout := commandTimeout(command)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
		return result, nil
	}

	message := commandFailureMessage(result, err, timeout, errors.Is(ctx.Err(), context.DeadlineExceeded))
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		message = fmt.Sprintf("command timed out after %s: %s", timeout, message)
	}
	if errors.Is(err, exec.ErrNotFound) {
		message = fmt.Sprintf("command %q was not found in PATH. Install the required tool or fix PATH. Detail: %s", command.path, message)
	}
	return result, fmt.Errorf("%s (ran %s %s; timeout %s): %s", command.description, command.path, strings.Join(command.args, " "), timeout, message)
}

func (s *Service) verifyToolAfterRecoverableInstallCommand(tool CLITool, operation installOperation, beforeVersion string, command installCommand, result *platform.ProcessResult, runErr error) (bool, *CheckStatus, string) {
	if !installCommandOutcomeNeedsRecheck(command, result, runErr) {
		return false, nil, ""
	}

	var lastReason string
	for attempt := 1; attempt <= boundedInstallRecheckAttempts(); attempt++ {
		if attempt > 1 {
			time.Sleep(installRecheckDelay)
		}
		after, verifyErr := s.CheckOne(tool)
		verifyOk := after != nil && after.Installed && after.PATHOk && strings.TrimSpace(after.Error) == ""
		versionChanged := true
		if verifyOk && operation == installOperationUpdate && strings.TrimSpace(beforeVersion) != "" {
			versionChanged = strings.TrimSpace(after.Version) != strings.TrimSpace(beforeVersion)
		}
		if verifyOk && versionChanged {
			return true, after, fmt.Sprintf("原始命令异常：%s；复核次数：%d/%d", installerDiagnosticSummary(runErr), attempt, boundedInstallRecheckAttempts())
		}
		if verifyErr != nil {
			lastReason = verifyErr.Error()
		} else {
			lastReason = verificationErrorMessage(after)
		}
	}
	_ = lastReason
	return false, nil, ""
}

func (s *Service) confirmClaudeNPMInstallAfterRecoverableCommand(command installCommand, result *platform.ProcessResult, runErr error) error {
	if !installCommandOutcomeNeedsRecheck(command, result, runErr) {
		return runErr
	}
	var lastErr error
	for attempt := 1; attempt <= boundedInstallRecheckAttempts(); attempt++ {
		if attempt > 1 {
			time.Sleep(installRecheckDelay)
		}
		if err := s.confirmClaudeNPMInstall(); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return fmt.Errorf("%w；bounded recheck 未确认 npm 包安装成功: %v", runErr, lastErr)
	}
	return runErr
}

func (s *Service) verifyClaudeNativeAvailableWithRecheck(installerOutput string) (*CheckStatus, error) {
	var lastErr error
	for attempt := 1; attempt <= boundedInstallRecheckAttempts(); attempt++ {
		if attempt > 1 {
			time.Sleep(installRecheckDelay)
		}
		status, err := s.verifyClaudeNativeAvailableWithHint(installerOutput)
		if err == nil {
			return status, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("bounded recheck 未确认 Claude Code Native 可用")
}

func boundedInstallRecheckAttempts() int {
	if installRecheckAttempts <= 0 {
		return 1
	}
	return installRecheckAttempts
}

func installCommandOutcomeNeedsRecheck(command installCommand, result *platform.ProcessResult, err error) bool {
	if err == nil {
		return false
	}
	if installCommandErrorLooksRecoverable(err) {
		return true
	}
	output := resultText(result)
	if installerOutputHasNPMInstallSuccessEvidence(output) {
		return true
	}
	return installerOutputHasGenericInstallProgressEvidence(output) && installCommandLooksLikePackageInstall(command)
}

func installCommandErrorLooksRecoverable(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "context deadline") ||
		strings.Contains(lower, "process killed")
}

func installerOutputHasNPMInstallSuccessEvidence(output string) bool {
	lower := strings.ToLower(output)
	return regexp.MustCompile(`(?i)\badded\s+\d+\s+packages?\b`).MatchString(lower) ||
		regexp.MustCompile(`(?i)\bchanged\s+\d+\s+packages?\b`).MatchString(lower) ||
		regexp.MustCompile(`(?i)\bup\s+to\s+date\b`).MatchString(lower) ||
		regexp.MustCompile(`(?i)\baudited\s+\d+\s+packages?\b`).MatchString(lower)
}

func installerOutputHasGenericInstallProgressEvidence(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{"installing", "downloading", "extracting", "successfully installed", "completed"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func installCommandLooksLikePackageInstall(command installCommand) bool {
	description := strings.ToLower(command.description)
	if strings.Contains(description, "install") || strings.Contains(description, "update") || strings.Contains(description, "upgrade") {
		return true
	}
	for _, arg := range command.args {
		switch strings.ToLower(arg) {
		case "install", "update", "upgrade":
			return true
		}
	}
	return false
}

func commandTimeout(command installCommand) time.Duration {
	if command.timeout > 0 {
		return command.timeout
	}
	return installCommandTimeout
}

func commandFailureMessage(result *platform.ProcessResult, err error, timeout time.Duration, timedOut bool) string {
	detail := ""
	if err != nil {
		detail = sanitizeInstallerOutput(err.Error())
	}
	output := sanitizeInstallerOutput(resultText(result))
	if output == "" {
		output = "no stdout/stderr captured"
	}
	parts := []string{fmt.Sprintf("timeout=%s", timeout)}
	if timedOut {
		parts = append(parts, "deadline exceeded")
	}
	if detail != "" {
		parts = append(parts, "process error: "+detail)
	}
	parts = append(parts, "output: "+output)
	return strings.Join(parts, "; ")
}

func sanitizeInstallerOutput(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	text = redactInstallerSensitiveValues(text)
	const maxDiagnosticChars = 4000
	if len(text) > maxDiagnosticChars {
		text = text[:maxDiagnosticChars] + "... [truncated]"
	}
	return text
}

func redactInstallerSensitiveValues(text string) string {
	text = installerQuotedSecretPattern.ReplaceAllString(text, `${1}[redacted]${3}`)
	text = installerBareSecretPattern.ReplaceAllString(text, `${1}[redacted]`)
	text = installerBearerTokenPattern.ReplaceAllString(text, `${1}[redacted]`)
	for _, pattern := range installerSensitiveValuePatterns {
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	return strings.TrimSpace(text)
}

func isHealthyAndCurrent(status *CheckStatus) bool {
	return status != nil &&
		status.Installed &&
		status.PATHOk &&
		!status.HasUpdate &&
		strings.TrimSpace(status.Error) == ""
}

func shouldReinstallHealthyUnknownClaude(tool CLITool, status *CheckStatus) bool {
	return tool == ToolClaudeCode && status != nil && status.InstallMethod == InstallMethodUnknown
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
	case InstallMethodUnknown, InstallMethod(""):
		return s.cleanClaudeCodeUnknown()
	default:
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("无法识别的 Claude Code 安装方式 %q，未执行删除。请重新检测后重试；如仍失败，请按检测到的可执行路径选择 npm/native 中的对应卸载方式。", method),
			Tool:    ToolClaudeCode,
		}, nil
	}
}

// cleanHeadroom removes the CodeBox-managed Headroom venv directory. Headroom
// is installed entirely inside this venv, so deleting the directory fully
// uninstalls it without touching the system Python. The public CleanHeadroom
// entry (service.go) is responsible for stopping a running proxy first via the
// injected headroomStopper callback before this method runs, so the venv
// directory is safe to RemoveAll even on Windows where a running headroom.exe
// would otherwise be locked.
func (s *Service) cleanHeadroom() (*InstallResult, error) {
	status, checkErr := s.CheckOne(ToolHeadroom)
	if checkErr != nil && status == nil {
		return installFailure(ToolHeadroom, "卸载前检测 Headroom 失败", checkErr), nil
	}
	if status == nil || !status.Installed {
		return &InstallResult{Success: true, Message: "未检测到 Headroom，无需卸载", Tool: ToolHeadroom}, nil
	}

	venvDir := strings.TrimSpace(s.headroomVenvDir)
	if venvDir == "" {
		err := fmt.Errorf("headroom venv 目录未配置，无法卸载")
		return installFailure(ToolHeadroom, err.Error(), err), err
	}
	if err := os.RemoveAll(venvDir); err != nil {
		return installFailure(ToolHeadroom, fmt.Sprintf("删除 Headroom venv 目录失败: %s", sanitizeInstallerOutput(err.Error())), err), err
	}

	after, _ := s.CheckOne(ToolHeadroom)
	if after != nil && !after.Installed {
		return &InstallResult{Success: true, Message: "Headroom 已通过删除 venv 目录成功卸载", Tool: ToolHeadroom}, nil
	}
	version := ""
	if after != nil {
		version = after.Version
	}
	return &InstallResult{
		Success: true,
		Message: fmt.Sprintf("已删除 Headroom venv 目录 %s；请重新检测确认卸载结果", venvDir),
		Tool:    ToolHeadroom,
		Version: version,
	}, nil
}

func (s *Service) cleanClaudeCodeUnknown() (*InstallResult, error) {
	status, checkErr := s.CheckOne(ToolClaudeCode)
	if checkErr != nil && status == nil {
		return installFailure(ToolClaudeCode, "卸载前检测 Claude Code 失败", checkErr), nil
	}
	if status == nil || !status.Installed {
		return &InstallResult{Success: true, Message: "未检测到 Claude Code，无需卸载", Tool: ToolClaudeCode}, nil
	}

	inferred := s.inferClaudeInstallMethodForCleanup(status)
	switch inferred {
	case InstallMethodNPM:
		return s.cleanClaudeCodeNPM()
	case InstallMethodNative:
		return s.cleanClaudeCodeNative()
	}

	path := strings.TrimSpace(status.ExecutablePath)
	message := "无法安全推断 Claude Code 的安装方式，未删除任何文件。"
	if path != "" {
		message += fmt.Sprintf(" 检测到的可执行路径: %s。", path)
	}
	message += " 可执行建议：如果它来自 npm，请运行 `npm uninstall -g @anthropic-ai/claude-code`；如果它是官方 Native 默认路径 `~/.local/bin/claude`，请重新检测或选择 Native 卸载；如果它位于自定义目录，请确认该路径只属于 Claude Code 后再手动移除。"
	return &InstallResult{Success: false, Message: message, Tool: ToolClaudeCode}, nil
}

func (s *Service) inferClaudeInstallMethodForCleanup(status *CheckStatus) InstallMethod {
	if status == nil {
		return InstallMethodUnknown
	}
	if status.InstallMethod != InstallMethodUnknown && status.InstallMethod != "" {
		return status.InstallMethod
	}
	path := strings.TrimSpace(status.ExecutablePath)
	if path == "" {
		return InstallMethodUnknown
	}
	if method := s.detectClaudeInstallMethod(path); method != InstallMethodUnknown {
		return method
	}
	if s.isClaudeNPMGlobalExecutablePath(path) {
		return InstallMethodNPM
	}
	if isClaudeNativeDefaultExecutablePath(path) {
		return InstallMethodNative
	}
	return InstallMethodUnknown
}

func (s *Service) cleanClaudeCodeNPM() (*InstallResult, error) {
	_, initialErr := s.runClaudeNPMUninstall()
	if initialErr != nil {
		// npm uninstall hit the well-known staging-directory deadlock
		// (ENOTEMPTY while renaming @anthropic-ai/claude-code ->
		// @anthropic-ai/.claude-code-XXXXXX, errno -66). This happens when a
		// previous install was interrupted mid-extract and left a stale staging
		// directory behind. Recover by manually removing the staging residue,
		// the main package directory, and any orphan npm bin links, then retry
		// the uninstall once. See Claude-Code-安装异常说明.md section 四.2 for
		// the manually-verified recovery sequence this code automates.
		if !looksLikeClaudeNPMStagingConflict(initialErr) {
			return &InstallResult{
				Success: false,
				Message: fmt.Sprintf("npm 卸载失败: %v", initialErr),
				Tool:    ToolClaudeCode,
			}, nil
		}
		cleaned, cleanErr := s.cleanClaudeNPMResidue()
		if cleanErr != nil {
			return &InstallResult{
				Success: false,
				Message: fmt.Sprintf("npm 卸载失败且自动清理 staging 残留时出错: %v (原始 npm 错误: %v)", cleanErr, initialErr),
				Tool:    ToolClaudeCode,
			}, nil
		}
		// Retry the uninstall once after cleanup. If npm had already removed
		// everything it could, the retry is expected to be a no-op success or
		// a benign "not installed" error.
		if retryErr := s.runClaudeNPMUninstallSilent(); retryErr != nil {
			// Retry failed but cleanup succeeded -- only fail if Claude Code
			// can still be detected afterwards.
			_ = retryErr
		}
		message := "npm 卸载遇到 staging 目录冲突 (ENOTEMPTY / errno -66)，已自动清理残留 staging 目录、主包目录与孤儿 bin 链接"
		if cleaned != nil && cleaned.Total() > 0 {
			message += fmt.Sprintf("（共清理 %d 项）", cleaned.Total())
		}
		after, _ := s.CheckOne(ToolClaudeCode)
		if after != nil && after.Installed {
			return &InstallResult{
				Success: false,
				Message: message + "，但清理后 Claude Code 仍然可被检测到，请手动检查",
				Tool:    ToolClaudeCode,
			}, nil
		}
		return &InstallResult{
			Success: true,
			Message: message + "，Claude Code (npm) 已成功卸载",
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

func (s *Service) runClaudeNPMUninstall() (*platform.ProcessResult, error) {
	npmPath := s.resolveNPMPath()
	ctx, cancel := context.WithTimeout(context.Background(), installCommandTimeout)
	defer cancel()
	return s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"uninstall", "-g", "@anthropic-ai/claude-code"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
}

// runClaudeNPMUninstallSilent runs the npm uninstall command but swallows the
// result/error. It is used as a best-effort retry after manual staging cleanup
// -- by that point the package directory has already been removed by
// cleanClaudeNPMResidue, so npm's job is essentially a no-op.
func (s *Service) runClaudeNPMUninstallSilent() error {
	_, err := s.runClaudeNPMUninstall()
	return err
}

// looksLikeClaudeNPMStagingConflict reports whether err looks like the npm
// staging-directory deadlock: ENOTEMPTY, errno -66, or a message referencing
// the .claude-code-XXXXXX staging rename target.
func looksLikeClaudeNPMStagingConflict(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "enotempty") {
		return true
	}
	if strings.Contains(lower, "errno -66") || strings.Contains(lower, "errno=-66") {
		return true
	}
	// npm formats the failing rename as "... .claude-code-XXXXXX" in its output.
	if strings.Contains(lower, ".claude-code-") && strings.Contains(lower, "rename") {
		return true
	}
	if strings.Contains(lower, "directory not empty") {
		return true
	}
	return false
}

func (s *Service) cleanClaudeCodeNative() (*InstallResult, error) {
	_, npmErr := s.runClaudeNPMUninstall()
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
	// R7 transparency: cleanClaudeCodeNative only removes the shim. The
	// versions/ directory holds the real native binaries and is preserved
	// intentionally so a future reinstall can reuse the signed binaries
	// without re-downloading. With the versions/-aware detection from this
	// change, CheckOne will still report Native Installed when versions/
	// has healthy binaries -- we surface that explicitly rather than letting
	// the user think uninstall failed.
	versionsKept := listClaudeNativeVersionsPathsForUninstallReport()
	if len(removed) == 0 && len(failed) == 0 && len(versionsKept) == 0 && npmErr != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("Native 清理失败：未找到 Native 安装文件，且 npm 包卸载失败: %v", npmErr),
			Tool:    ToolClaudeCode,
		}, nil
	}
	after, _ := s.CheckOne(ToolClaudeCode)
	if after != nil && after.Installed {
		detail := fmt.Sprintf("已执行 npm 卸载并删除 %d 个 Native shim 文件", len(removed))
		if len(failed) > 0 {
			detail += fmt.Sprintf("，%d 个文件删除失败", len(failed))
		}
		if len(versionsKept) > 0 {
			detail += fmt.Sprintf("；versions/ 目录下仍保留 %d 个 Native 二进制（CodeBox 检测仍能识别），如需彻底卸载请手动删除 %s", len(versionsKept), claudeNativeVersionsDir())
		}
		if npmErr != nil {
			detail += fmt.Sprintf("，npm 卸载失败: %v", npmErr)
		}
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("%s，但 Claude Code 仍可被检测到", detail),
			Tool:    ToolClaudeCode,
		}, nil
	}
	if npmErr != nil {
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("已删除 %d 个 Native shim 文件，但 npm 包卸载失败: %v", len(removed), npmErr),
			Tool:    ToolClaudeCode,
		}, nil
	}
	msg := fmt.Sprintf("已卸载 npm 包并清理 %d 个 Native shim 文件", len(removed))
	if len(failed) > 0 {
		msg += fmt.Sprintf("（%d 个文件删除失败）", len(failed))
	}
	if len(versionsKept) > 0 {
		msg += fmt.Sprintf("；versions/ 仍保留 %d 个 Native 二进制（重装时可复用，无需重新下载）", len(versionsKept))
	}
	return &InstallResult{
		Success: true,
		Message: msg,
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
	case ToolHeadroom:
		return "Headroom"
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
