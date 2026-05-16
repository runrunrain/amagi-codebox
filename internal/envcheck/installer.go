package envcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/platform"
)

const (
	installCommandTimeout               = 120 * time.Second
	nativeInstallCommandTimeout         = 20 * time.Minute
	claudeNativeBootstrapCommandTimeout = 20 * time.Minute
	nativeDirectEvidenceTimeoutDefault  = 30 * time.Second
	installRecheckAttemptsDefault       = 3
	installRecheckDelayDefault          = 300 * time.Millisecond

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

	progressNativeFallbackSwitch        = 86
	progressNativeFallbackNPMInstall    = 88
	progressNativeFallbackClaudeInstall = 92
	progressNativeFallbackVerify        = 95
)

var nativeDirectEvidenceTimeout = nativeDirectEvidenceTimeoutDefault

var nativeDirectInstallerSupported = func() bool {
	return runtime.GOOS == "windows"
}

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

func (s *Service) installClaudeCodeWithMethodProgress(method ClaudeInstallMethod, reporter progressReporter) (*InstallResult, error) {
	targetMethod, err := targetInstallMethodForClaude(method)
	if err != nil {
		return nil, err
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
		return s.installClaudeNativeWithBootstrapFallback(reporter)
	}
	return s.installOrUpdateWithProgress(ToolClaudeCode, installOperationInstall, reporter, method)
}

func (s *Service) installClaudeNativeWithBootstrapFallback(reporter progressReporter) (*InstallResult, error) {
	reporter.report(OperationStepPrecheck, "正在检查 Claude Code Native 当前状态...", progressPrecheck)

	before, checkErr := s.CheckOne(ToolClaudeCode)
	if checkErr != nil && before == nil {
		return installFailure(ToolClaudeCode, "Claude Code Native 安装前检查失败", checkErr), checkErr
	}
	if isHealthyAndCurrent(before) && before.InstallMethod == InstallMethodNative {
		return &InstallResult{
			Success: true,
			Message: "Claude Code Native 已安装且为最新版本",
			Tool:    ToolClaudeCode,
			Version: before.Version,
		}, nil
	}

	reporter.report(OperationStepRunCommand, "Native 官方安装模式：正在启动 Claude Code direct installer...", progressRunStart)
	directResult, directErr := s.runNativeDirectInstallerWithEvidence(reporter)
	if directErr == nil {
		reporter.report(OperationStepVerify, "Native 官方安装模式：正在验证 Native 直接安装结果...", progressVerify)
		after, verifyErr := s.verifyClaudeNativeAvailableWithHint(resultText(directResult))
		if verifyErr == nil {
			return &InstallResult{
				Success: true,
				Message: nativeInstallSuccessMessage("Claude Code 已通过 Native 官方安装模式 direct installer 安装成功", after),
				Tool:    ToolClaudeCode,
				Version: after.Version,
			}, nil
		}
		directErr = fmt.Errorf("Native 官方安装模式 direct installer succeeded but native verification failed: %w", verifyErr)
	} else if isNativeDirectEvidenceTimeout(directErr) || installCommandErrorLooksRecoverable(directErr) {
		reporter.report(OperationStepVerify, "Native 官方安装模式：命令超时/输出不完整，正在 bounded recheck 确认是否已安装...", progressVerify)
		if after, verifyErr := s.verifyClaudeNativeAvailableWithRecheck(resultText(directResult)); verifyErr == nil {
			return &InstallResult{
				Success: true,
				Message: nativeInstallSuccessMessage("Claude Code Native 官方安装模式命令超时/输出不完整，但 bounded recheck 已确认 Native 官方二进制可用；原始超时诊断已保留", after),
				Tool:    ToolClaudeCode,
				Version: after.Version,
			}, nil
		}
	}

	directSummary := installerDiagnosticSummary(directErr)
	switchMessage := "Native 官方安装模式直接安装失败，切换保底方案：保底安装模式（npm + claude install）..."
	if isNativeDirectEvidenceTimeout(directErr) {
		switchMessage = "Native 官方安装模式：30 秒内未检测到响应，切换保底方案（npm + claude install）..."
	}
	reporter.report(OperationStepPrepare, switchMessage, progressNativeFallbackSwitch)

	if err := s.ensureNPMAvailable(); err != nil {
		fallbackErr := fmt.Errorf("npm 不可用，无法执行 Native 保底安装: %w", err)
		return s.nativeBootstrapFailureResult(directSummary, fallbackErr), fallbackErr
	}

	reporter.report(OperationStepRunCommand, "保底安装模式（npm + claude install）：正在安装 npm 版本 Claude Code，作为 Native 官方二进制安装引导...", progressNativeFallbackNPMInstall)
	npmCmd := s.resolveCommandNPMPath(npmClaudeCommand(installOperationInstall))
	npmResult, npmErr := s.runInstallCommandResult(npmCmd)
	if npmErr != nil {
		reporter.report(OperationStepVerify, "保底安装模式（npm + claude install）：npm 安装命令异常，正在 bounded recheck 确认 npm 包是否已完成安装...", progressNativeFallbackVerify)
		if confirmErr := s.confirmClaudeNPMInstallAfterRecoverableCommand(npmCmd, npmResult, npmErr); confirmErr != nil {
			fallbackErr := fmt.Errorf("安装 npm 版本 Claude Code 失败: %w", confirmErr)
			return s.nativeBootstrapFailureResult(directSummary, fallbackErr), fallbackErr
		}
	} else if err := s.confirmClaudeNPMInstall(); err != nil {
		fallbackErr := fmt.Errorf("npm 版本 Claude Code 安装后确认失败: %w", err)
		return s.nativeBootstrapFailureResult(directSummary, fallbackErr), fallbackErr
	}

	reporter.report(OperationStepRunCommand, "保底安装模式（npm + claude install）：正在执行 claude install 安装 Native 官方二进制...", progressNativeFallbackClaudeInstall)
	bootstrapCmd, bootstrapResolveErr := s.claudeNativeBootstrapCommandAfterNPMInstall()
	if bootstrapResolveErr != nil {
		fallbackErr := fmt.Errorf("npm 版本 Claude Code 安装成功但无法定位 claude install 引导命令: %w", bootstrapResolveErr)
		return s.nativeBootstrapFailureResult(directSummary, fallbackErr), fallbackErr
	}
	bootstrapResult, bootstrapErr := s.runInstallCommandResult(bootstrapCmd)
	if bootstrapErr != nil {
		reporter.report(OperationStepVerify, "保底安装模式（npm + claude install）：claude install 返回非零状态，正在根据成功输出验证 Native 二进制...", progressNativeFallbackVerify)
		if after, verifyErr := s.verifyClaudeNativeInstallFromCommandOutput(resultText(bootstrapResult)); verifyErr == nil {
			return &InstallResult{
				Success: true,
				Message: nativeInstallSuccessMessage("Claude Code 直接 Native 安装失败后，claude install 已完成 Native 官方二进制安装；后续 shell integration 返回非零状态，已按 Location 验证二进制可用", after),
				Tool:    ToolClaudeCode,
				Version: after.Version,
			}, nil
		}
		fallbackErr := fmt.Errorf("执行 claude install 失败: %w", bootstrapErr)
		return s.nativeBootstrapFailureResult(directSummary, fallbackErr), fallbackErr
	}

	reporter.report(OperationStepVerify, "保底安装模式（npm + claude install）：正在验证 Native 官方二进制 Claude Code 可用...", progressNativeFallbackVerify)
	after, verifyErr := s.verifyClaudeNativeAvailableWithHint(resultText(bootstrapResult))
	if verifyErr != nil {
		fallbackErr := fmt.Errorf("claude install 完成后 Native 验证失败: %w", verifyErr)
		return s.nativeBootstrapFailureResult(directSummary, fallbackErr), fallbackErr
	}

	return &InstallResult{
		Success: true,
		Message: nativeInstallSuccessMessage("Claude Code 直接 Native 安装失败后，已通过 npm 引导 claude install 保底方案安装 Native 官方二进制", after),
		Tool:    ToolClaudeCode,
		Version: after.Version,
	}, nil
}

type nativeDirectEvidenceTimeoutError struct {
	timeout time.Duration
	detail  string
}

func (e nativeDirectEvidenceTimeoutError) Error() string {
	message := fmt.Sprintf("Native 官方安装模式：%s 内未检测到 stdout/stderr、下载/安装进度或成功结束，已终止 direct installer", e.timeout)
	if e.detail != "" {
		message += ": " + e.detail
	}
	return message
}

func isNativeDirectEvidenceTimeout(err error) bool {
	var gateErr nativeDirectEvidenceTimeoutError
	return errors.As(err, &gateErr)
}

func (s *Service) runNativeDirectInstallerWithEvidence(reporter progressReporter) (*platform.ProcessResult, error) {
	command, prepareErr := s.nativeDirectClaudeCommand()
	if prepareErr != nil {
		return nil, prepareErr
	}
	timeout := commandTimeout(command)
	reporter.report(OperationStepRunCommand, "Native 官方安装模式：等待安装器响应/下载开始（最多 30 秒）...", progressRunStart+1)

	evidenceRunner, ok := s.processRunner.(platform.EvidenceProcessRunner)
	if !ok {
		result, err := s.runInstallCommandResult(command)
		if err != nil {
			return result, fmt.Errorf("Native 官方安装模式 direct installer failed without streaming evidence support: %w", err)
		}
		return result, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var evidenceOnce sync.Once
	result, err := evidenceRunner.RunWithEvidence(ctx, platform.CommandSpec{
		Path:   command.path,
		Args:   append([]string(nil), command.args...),
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	}, nativeDirectEvidenceTimeout, func(platform.ProcessOutputEvent) {
		evidenceOnce.Do(func() {
			reporter.report(OperationStepRunCommand, "Native 官方安装模式：已检测到安装器响应，继续等待官方 direct installer 完成...", progressRunStart+2)
		})
	})

	processResult := (*platform.ProcessResult)(nil)
	if result != nil {
		processResult = result.Result
	}
	if result != nil && result.EvidenceTimedOut {
		detail := sanitizeInstallerOutput(resultText(processResult))
		return processResult, nativeDirectEvidenceTimeoutError{timeout: nativeDirectEvidenceTimeout, detail: detail}
	}
	if err == nil {
		return processResult, nil
	}

	message := commandFailureMessage(processResult, err, timeout, errors.Is(ctx.Err(), context.DeadlineExceeded))
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		message = fmt.Sprintf("command timed out after %s: %s", timeout, message)
	}
	if errors.Is(err, exec.ErrNotFound) {
		message = fmt.Sprintf("command %q was not found in PATH. Install the required tool or fix PATH. Detail: %s", command.path, message)
	}
	return processResult, fmt.Errorf("Native 官方安装模式 direct installer failed (ran %s %s; timeout %s): %s", command.path, strings.Join(command.args, " "), timeout, message)
}

func (s *Service) nativeDirectClaudeCommand() (installCommand, error) {
	if !nativeDirectInstallerSupported() {
		return installCommand{}, fmt.Errorf("Native 官方安装模式 direct installer skipped: 当前平台 %s 不支持 Windows-only PowerShell direct installer，将改用保底安装模式（npm + claude install）", runtime.GOOS)
	}

	command := nativePowerShellClaudeCommand()
	env := s.buildEnhancedEnv()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, _, err := resolver.ResolveExecutable(command.path, nil, env)
	if err != nil || strings.TrimSpace(resolved.Path) == "" {
		detail := "PowerShell 不在 PATH 中"
		if err != nil {
			detail = sanitizeInstallerOutput(err.Error())
		}
		return installCommand{}, fmt.Errorf("Native 官方安装模式 direct installer skipped: %s，未执行 powershell.exe；将改用保底安装模式（npm + claude install）", detail)
	}
	command.path = resolved.Path
	return command, nil
}

func (s *Service) nativeBootstrapFailureResult(directSummary string, fallbackErr error) *InstallResult {
	fallbackSummary := installerDiagnosticSummary(fallbackErr)
	message := fmt.Sprintf(
		"Claude Code Native 安装失败：Native 官方安装模式直接安装失败，保底安装模式（npm + claude install）也未完成。Direct: [%s]。Fallback: [%s]。建议：确认 Node.js/npm 可用后重试，或手动执行 npm install -g @anthropic-ai/claude-code 后运行 claude install。",
		directSummary,
		fallbackSummary,
	)
	return installFailure(ToolClaudeCode, message, errors.New(message))
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
		status != nil &&
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
	case ClaudeInstallWinget:
		return InstallMethodWinget, nil
	default:
		return InstallMethodUnknown, fmt.Errorf("unsupported method: %s", method)
	}
}

func claudeInstallMethodDisplayName(method ClaudeInstallMethod) string {
	switch method {
	case ClaudeInstallNPM:
		return "npm"
	case ClaudeInstallNative:
		return "Native PowerShell"
	case ClaudeInstallWinget:
		return "winget"
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
			// Unknown method: use a conservative, safe repair path instead of failing
			// before doing any work. On macOS/Linux the only automatic channel is npm.
			// On Windows, npm is also the safest non-destructive default; winget is kept
			// as a secondary verifier-backed fallback. Native direct install is not used
			// for unknown updates because it is a full installer, not a targeted update.
			return unknownClaudeUpdateCommands(), nil
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

func unknownClaudeUpdateCommands() []installCommand {
	commands := []installCommand{npmClaudeCommand(installOperationUpdate)}
	if runtime.GOOS == "windows" {
		commands = append(commands, wingetClaudeCommand(installOperationUpdate))
	}
	return commands
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
		timeout:     nativeInstallCommandTimeout,
		args: []string{
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy", "RemoteSigned",
			"-Command", "irm https://claude.ai/install.ps1 | iex",
		},
	}
}

func claudeNativeBootstrapCommand() installCommand {
	return installCommand{
		description: "claude install native bootstrap",
		path:        "claude",
		args:        []string{"install"},
		timeout:     claudeNativeBootstrapCommandTimeout,
	}
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
	if path, source := s.resolveClaudeFromNPMGlobalPrefix(); path != "" {
		return path, source, nil
	}

	env := s.buildEnhancedEnv()
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, diagnostics, err := resolver.ResolveExecutable(claudeCommandName, nil, env)
	if err == nil && strings.TrimSpace(resolved.Path) != "" {
		realPath := resolveRealExecutablePath(resolved.Path)
		if s.detectClaudeInstallMethod(realPath) == InstallMethodNative {
			return "", "", fmt.Errorf("npm 全局包已安装，但解析到的 claude 为 Native 路径 %s，未找到 npm shim；请确认 npm global bin 已创建并重试", realPath)
		}
		source := diagnostics.CLISource
		if strings.TrimSpace(source) == "" {
			source = "resolver"
		}
		return realPath, source, nil
	}

	detail := ""
	if err != nil {
		detail = ": " + sanitizeInstallerOutput(err.Error())
	}
	return "", "", fmt.Errorf("npm 全局包已安装，但当前 PATH 未刷新且 npm global prefix/bin 下未找到 claude 可执行文件%s", detail)
}

func (s *Service) resolveClaudeFromNPMGlobalPrefix() (string, string) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil || strings.TrimSpace(prefix) == "" {
		return "", ""
	}
	for _, candidate := range claudeNPMGlobalExecutableCandidates(prefix) {
		if fileExists(candidate) {
			return resolveRealExecutablePath(candidate), "npm global prefix"
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

func claudeNPMGlobalExecutableCandidates(prefix string) []string {
	prefix = filepath.Clean(strings.TrimSpace(prefix))
	if prefix == "" || prefix == "." {
		return nil
	}

	dirs := []string{filepath.Join(prefix, "bin"), prefix, filepath.Join(prefix, "node_modules", ".bin")}
	names := []string{"claude"}
	if isWindows() {
		names = []string{"claude.cmd", "claude.exe", "claude"}
	}

	candidates := make([]string, 0, len(dirs)*len(names))
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			key := normalizeClaudePath(candidate)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	return candidates
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
		detail := sanitizeInstallerOutput(resultText(result))
		if detail == "" {
			detail = sanitizeInstallerOutput(runErr.Error())
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
	case InstallMethodWinget:
		return s.cleanClaudeCodeWinget()
	case InstallMethodUnknown, InstallMethod(""):
		return s.cleanClaudeCodeUnknown()
	default:
		return &InstallResult{
			Success: false,
			Message: fmt.Sprintf("无法识别的 Claude Code 安装方式 %q，未执行删除。请重新检测后重试；如仍失败，请按检测到的可执行路径选择 npm/native/winget 中的对应卸载方式。", method),
			Tool:    ToolClaudeCode,
		}, nil
	}
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
	case InstallMethodWinget:
		return s.cleanClaudeCodeWinget()
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
