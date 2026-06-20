package envcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

const (
	claudeCommandName     = "claude"
	claudeVersionTimeout  = 10 * time.Second
	claudeNPMCheckTimeout = 15 * time.Second
)

var claudeVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

var claudeUserHomeDir = os.UserHomeDir

func (s *Service) checkClaudeCode() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolClaudeCode,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	rr := resolveExecutable(claudeCommandName)
	// versions/ truth source: the newest healthy native binary under
	// ~/.local/share/claude/versions/ is the authoritative native install
	// signal. It ALWAYS wins over the shim and over npm residuals, breaking
	// the historical chicken-and-egg where a missing shim hid native.
	// See native_versions.go for the cross-platform layout and integrity
	// gating; we deliberately do not require the shim to exist.
	nativeVersionsPath := firstHealthyClaudeNativeVersion()
	if nativeVersionsPath != "" {
		rr = applyClaudeNativeVersionsResolution(rr, nativeVersionsPath)
	} else if nativePath := firstExistingClaudeNativeDefaultPath(); shouldPreferClaudeNativeDefaultPath(rr, nativePath) {
		rr = resolveResult{
			executablePath: nativePath,
			systemPATHOk:   false,
			pathState:      PathStateCodeboxPATH,
			pathSource:     "native-default-location",
		}
	} else if strings.TrimSpace(rr.executablePath) == "" {
		if npmPath := s.firstExistingClaudeNPMGlobalPath(); npmPath != "" {
			rr = resolveResult{
				executablePath: npmPath,
				systemPATHOk:   false,
				pathState:      PathStateCodeboxPATH,
				pathSource:     "npm-global-prefix",
			}
		}
	}
	applyPathStateToStatus(status, rr, ToolClaudeCode)

	if strings.TrimSpace(rr.executablePath) == "" {
		status.Error = "未在 PATH 中找到 Claude Code 可执行文件"
		addMissingToolIssue(status, ToolClaudeCode)
		return status, nil
	}

	invocationPath, detectionPath := resolveClaudeExecutablePathsForCheck(rr.executablePath)
	status.Installed = true
	status.ExecutablePath = invocationPath
	status.InstallMethod = s.detectClaudeInstallMethod(detectionPath)
	if status.InstallMethod == InstallMethodUnknown && rr.pathSource == "npm-global-prefix" {
		status.InstallMethod = InstallMethodNPM
	}

	version, err := s.claudeVersion(invocationPath)
	if err != nil {
		// M-1 detect-path self-heal: if the failure is corruption (truncated
		// shard / macOS AMFI SIGKILL), automatically clean up the npm staging
		// residue + orphan bin links so the user's next install attempt does
		// not deadlock on ENOTEMPTY or pick up the same shard. Then surface a
		// structured issue that drives the user to reinstall.
		if looksLikeClaudeCorruptionError(err) {
			outcome := s.selfHealClaudeNPMResidueForDetection(invocationPath)
			status.Issues = append(status.Issues, buildClaudeCorruptionSelfHealIssue(err, outcome))
		}
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	// R6 coexistence hint: when the resolved binary is an npm shim but a
	// healthy native binary also exists under versions/, npm residuals could
	// shadow native in shells where ~/.local/node/bin precedes ~/.local/bin.
	// Surface an info-level issue so the frontend can prompt the user to
	// clean up npm residue and switch to the Native path. We do not force
	// rewrite InstallMethod here -- the user may intentionally keep both
	// channels and CodeBox should reflect what is currently on PATH.
	if status.InstallMethod == InstallMethodNPM {
		if nativeHint := buildClaudeNativeAvailableAlongsideNPMHint(detectionPath); nativeHint != nil {
			status.Issues = append(status.Issues, *nativeHint)
		}
	}

	if status.InstallMethod == InstallMethodNPM {
		if err := s.confirmClaudeNPMInstall(); err != nil {
			if isClaudeNPMPackageBinaryFallbackPath(detectionPath) {
				status.Issues = append(status.Issues, CheckIssue{
					Severity: SeverityWarning,
					Code:     "claude_npm_package_binary_fallback",
					Message:  "检测到可运行的 Claude Code，但 npm 全局入口确认失败",
					Detail:   fmt.Sprintf("CodeBox 已直接通过 npm 包内二进制验证 Claude Code 可用；建议重装或更新以修复 npm shim。确认失败: %v", err),
					Solutions: []ResolutionAction{
						{
							Type:            SolutionInstallTool,
							Description:     "重装 Claude Code 以修复 npm 全局入口",
							Tool:            ToolClaudeCode,
							PackageName:     "@anthropic-ai/claude-code",
							RequiresConfirm: true,
							IsPrimary:       true,
						},
						{
							Type:        SolutionManualCommand,
							Description: "手动修复 npm 全局安装",
							Command:     "npm install -g @anthropic-ai/claude-code@latest",
							Tool:        ToolClaudeCode,
						},
					},
				})
			} else {
				status.InstallMethod = InstallMethodUnknown
				status.Error = fmt.Sprintf("检测到类 npm 的 Claude Code 路径，但 npm 全局包确认失败: %v", err)
				return status, nil
			}
		} else {
			// The official native installer is recommended over npm, but npm is
			// still a valid installation. Record as a non-blocking info issue so
			// the frontend can display the recommendation without treating the
			// tool as broken.
			status.Issues = append(status.Issues, CheckIssue{
				Severity: SeverityInfo,
				Code:     "claude_npm_install_recommended_native",
				Message:  "检测到 Claude Code 通过 npm 全局安装；如需 Native 模式，可在 npm 安装后执行 claude install",
				Solutions: []ResolutionAction{
					{
						Type:        SolutionManualCommand,
						Description: "切换到 Native Claude Code",
						Command:     "npm install -g @anthropic-ai/claude-code && claude install",
					},
				},
			})
		}
	}

	// Integrate Claude Code configuration check
	configStatus, configErr := s.checkClaudeConfig()
	if configErr != nil {
		// Config check failure does not block overall detection; record as info-level issue
		status.Issues = append(status.Issues, CheckIssue{
			Severity: SeverityInfo,
			Code:     "claude_config_check_failed",
			Message:  fmt.Sprintf("配置检测失败: %v", configErr),
		})
	} else {
		status.Config = configStatus
		// P1-4: Convert missing required config items to structured issues
		if configStatus.MissingRequired > 0 {
			status.Issues = append(status.Issues, CheckIssue{
				Severity: SeverityWarning,
				Code:     "claude_config_missing_required",
				Message:  fmt.Sprintf("Claude Code 缺少 %d 项必要配置，可能影响正常使用", configStatus.MissingRequired),
				Detail:   "请在下方配置面板中补充缺失的配置项",
			})
			// 不设置 status.Error -- status.Error 仅反映二进制安装问题（可执行文件找不到、版本解析失败等）
			// 配置缺失不应导致安装验证误判安装失败
		}
	}

	return status, nil
}

func shouldPreferClaudeNativeDefaultPath(rr resolveResult, nativePath string) bool {
	if strings.TrimSpace(nativePath) == "" {
		return false
	}
	resolvedPath := strings.TrimSpace(rr.executablePath)
	if resolvedPath == "" {
		return true
	}
	if sameNormalizedPath(resolvedPath, nativePath) {
		return false
	}
	return true
}

func firstExistingClaudeNativeDefaultPath() string {
	for _, candidate := range claudeNativeDefaultExecutableCandidates() {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func isClaudeNativeDefaultExecutablePath(path string) bool {
	realPath := resolveRealExecutablePath(path)
	for _, candidate := range claudeNativeDefaultExecutableCandidates() {
		if sameNormalizedPath(realPath, candidate) {
			return true
		}
	}
	return false
}

func (s *Service) firstExistingClaudeNPMGlobalPath() string {
	path, _ := s.resolveClaudeFromNPMGlobalPrefix()
	return path
}

func (s *Service) checkClaudeFromNPMGlobalPrefix() (*CheckStatus, []string, error) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil {
		return nil, nil, err
	}
	candidates := claudeNPMGlobalExecutableCandidates(prefix)
	if len(candidates) == 0 {
		return nil, candidates, fmt.Errorf("npm global prefix %q did not produce Claude Code executable candidates", prefix)
	}

	diagnostics := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		invocationPath, detectionPath := resolveClaudeExecutablePathsForCheck(candidate)
		version, err := s.claudeVersion(invocationPath)
		if err != nil {
			// M-1 detect-path self-heal (npm-global-prefix fallback): when a
			// corrupted shard is identified here, run the same self-heal as
			// the primary check path so the next install attempt is not
			// poisoned by the same staging residue. We deliberately do not
			// surface the issue from this branch -- issues are owned by
			// checkClaudeCode to avoid duplicates -- but we DO trigger the
			// cleanup so the residue never survives the check.
			if looksLikeClaudeCorruptionError(err) {
				s.selfHealClaudeNPMResidueForDetection(invocationPath)
			}
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %s", invocationPath, sanitizeInstallerOutput(err.Error())))
			continue
		}
		status := &CheckStatus{
			Tool:           ToolClaudeCode,
			Installed:      true,
			InstallMethod:  InstallMethodNPM,
			Version:        version,
			PATHOk:         true,
			ExecutablePath: invocationPath,
			CheckedAt:      time.Now(),
			SystemPATHOk:   pathDirInProcessPATH(filepath.Dir(detectionPath)),
			PathState:      PathStateCodeboxPATH,
			PathSource:     "npm global prefix",
		}
		if status.SystemPATHOk {
			status.PathState = PathStateSystemPATH
		}
		return status, candidates, nil
	}

	if len(diagnostics) > 0 {
		return nil, candidates, fmt.Errorf("Claude Code npm global prefix candidates were found but unusable: %s", strings.Join(diagnostics, "; "))
	}
	return nil, candidates, fmt.Errorf("Claude Code executable not found under npm global prefix candidates: %s", strings.Join(candidates, ", "))
}

func (s *Service) isClaudeNPMGlobalExecutablePath(path string) bool {
	path = resolveRealExecutablePath(path)
	prefix, err := s.npmGlobalPrefix()
	if err != nil || strings.TrimSpace(prefix) == "" {
		return false
	}
	for _, candidate := range claudeNPMGlobalExecutableCandidates(prefix) {
		if sameNormalizedPath(path, candidate) {
			return true
		}
	}
	return false
}

func sameNormalizedPath(left string, right string) bool {
	return normalizeClaudePath(resolveRealExecutablePath(left)) == normalizeClaudePath(resolveRealExecutablePath(right))
}

func claudeNativeDefaultExecutableCandidates() []string {
	homes := claudeNativeHomeCandidates()

	candidates := []string{}
	for _, home := range homes {
		dir := filepath.Join(home, ".local", "bin")
		if isWindows() {
			candidates = append(candidates,
				filepath.Join(dir, "claude.exe"),
				filepath.Join(dir, "claude.cmd"),
				filepath.Join(dir, "claude"),
			)
		} else {
			candidates = append(candidates, filepath.Join(dir, "claude"))
		}
	}
	return candidates
}

func claudeNativeHomeCandidates() []string {
	homes := []string{}
	appendHome := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		cleaned := filepath.Clean(value)
		if cleaned == "." {
			return
		}
		for _, existing := range homes {
			if strings.EqualFold(filepath.Clean(existing), cleaned) {
				return
			}
		}
		homes = append(homes, cleaned)
	}

	for _, key := range []string{"USERPROFILE", "HOME"} {
		appendHome(os.Getenv(key))
	}
	if home, err := claudeUserHomeDir(); err == nil {
		appendHome(home)
	}
	return homes
}

func resolveRealExecutablePath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." {
		return strings.TrimSpace(path)
	}
	// filepath.Clean on non-Windows does not treat backslash as separator,
	// so strip trailing backslashes explicitly for cross-platform consistency.
	cleaned = strings.TrimRight(cleaned, `/\`)
	if cleaned == "" {
		return strings.TrimSpace(path)
	}
	if evaluated, err := filepath.EvalSymlinks(cleaned); err == nil && strings.TrimSpace(evaluated) != "" {
		return evaluated
	}
	return cleaned
}

func resolveClaudeExecutablePathsForCheck(path string) (invocationPath string, detectionPath string) {
	invocationPath = cleanExecutableInvocationPath(path)
	detectionPath = resolveRealExecutablePath(invocationPath)
	if shouldPreserveDarwinClaudeShimInvocationPath(invocationPath, detectionPath) {
		return invocationPath, detectionPath
	}
	return detectionPath, detectionPath
}

func cleanExecutableInvocationPath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." {
		return strings.TrimSpace(path)
	}
	cleaned = strings.TrimRight(cleaned, `/\`)
	if cleaned == "" {
		return strings.TrimSpace(path)
	}
	return cleaned
}

func shouldPreserveDarwinClaudeShimInvocationPath(invocationPath string, detectionPath string) bool {
	if runtimeGOOS != "darwin" {
		return false
	}
	if strings.TrimSpace(invocationPath) == "" || strings.TrimSpace(detectionPath) == "" {
		return false
	}
	if normalizeClaudePath(invocationPath) == normalizeClaudePath(detectionPath) {
		return false
	}
	if strings.EqualFold(filepath.Ext(invocationPath), ".exe") || !strings.EqualFold(filepath.Ext(detectionPath), ".exe") {
		return false
	}
	return looksLikeClaudeNPMPath(detectionPath)
}

func (s *Service) claudeVersion(executablePath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeVersionTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   executablePath,
		Args:   []string{"--version"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		// Self-heal (P0-3): classify the failure instead of collapsing every
		// error into "run claude --version: ...". This lets the frontend
		// distinguish "not installed" from "installed but corrupted" (truncated
		// shard / macOS AMFI SIGKILL on an unsigned binary) and trigger the
		// appropriate fix flow.
		return "", classifyClaudeVersionError(executablePath, result, err)
	}

	version := parseClaudeVersion(resultText(result))
	if version == "" {
		return "", fmt.Errorf("parse Claude Code version from output %q", resultText(result))
	}
	return version, nil
}

// buildClaudeCorruptionSelfHealIssue renders the user-facing CheckIssue for
// the M-1 detect-path self-heal. It always carries the reinstall + clean
// solutions so the frontend (EnvCheckSettings.vue) can offer a one-click
// recovery flow even when automatic cleanup succeeded only partially.
//
// outcome.Triggered is always true when this function is called (it is only
// invoked from the corruption branch of checkClaudeCode). The issue severity
// stays SeverityWarning rather than SeverityError so that an otherwise-
// healthy check does not flip the overall status to critical when the
// reinstall has already been made safe by the cleanup.
func buildClaudeCorruptionSelfHealIssue(classifiedErr error, outcome claudeNPMResidueSelfHealOutcome) CheckIssue {
	issue := CheckIssue{
		Severity: SeverityWarning,
		Code:     "claude_install_interrupted_residue_cleaned",
		Message:  "检测到 Claude Code 安装中断残留（残缺二进制 + staging 冲突），已自动清理，建议重新安装",
		Detail:   detailTextForClaudeSelfHeal(classifiedErr, outcome),
		Solutions: []ResolutionAction{
			{
				Type:            SolutionInstallClaudeMethod,
				Description:     "重新安装 Claude Code（npm / Native）",
				Tool:            ToolClaudeCode,
				PackageName:     "@anthropic-ai/claude-code",
				RequiresConfirm: true,
				IsPrimary:       true,
			},
			{
				Type:            SolutionCleanClaudeInstall,
				Description:     "再次清理 Claude Code 安装残留",
				Tool:            ToolClaudeCode,
				RequiresConfirm: true,
			},
			{
				Type:        SolutionManualCommand,
				Description: "手动重装（终端）",
				Command:     "npm install -g @anthropic-ai/claude-code@latest",
				Tool:        ToolClaudeCode,
			},
		},
	}
	if !outcome.Triggered {
		return issue
	}
	if outcome.CleanupErr != nil {
		issue.Code = "claude_install_interrupted_residue_cleanup_failed"
		issue.Message = "检测到 Claude Code 安装中断残留，自动清理未完成，请手动清理后重装"
		issue.Detail = fmt.Sprintf("%s\n清理失败原因: %v", issue.Detail, outcome.CleanupErr)
	}
	return issue
}

// detailTextForClaudeSelfHeal composes the human-readable detail block for
// the self-heal issue. It surfaces the original classified error, the
// integrity finding, and the entries removed during cleanup so the frontend
// can render a precise diagnosis without re-deriving it.
func detailTextForClaudeSelfHeal(classifiedErr error, outcome claudeNPMResidueSelfHealOutcome) string {
	parts := make([]string, 0, 4)
	if errText := strings.TrimSpace(classifiedErr.Error()); errText != "" {
		parts = append(parts, fmt.Sprintf("诊断: %s", errText))
	}
	if outcome.Integrity.Exists {
		parts = append(parts, fmt.Sprintf("二进制完整性: %s", outcome.Integrity.Reason))
	}
	if outcome.Cleanup != nil && outcome.Cleanup.Total() > 0 {
		parts = append(parts, fmt.Sprintf(
			"已清理: staging 目录 %d 个, 主包目录 %d 个, 孤儿 bin 链接 %d 个",
			len(outcome.Cleanup.StagingDirs), len(outcome.Cleanup.PackageDirs), len(outcome.Cleanup.OrphanBinLinks),
		))
	} else if outcome.Cleanup != nil {
		parts = append(parts, "未发现可清理的 npm 残留（可能是 Native 通道或用户外部路径损坏）")
	}
	return strings.Join(parts, "\n")
}

func parseClaudeVersion(output string) string {
	return claudeVersionPattern.FindString(strings.TrimSpace(output))
}

func resultText(result *platform.ProcessResult) string {
	if result == nil {
		return ""
	}
	combined := strings.TrimSpace(result.Stdout)
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += stderr
	}
	return combined
}

// applyClaudeNativeVersionsResolution merges a versions/-discovered native
// binary with the resolveResult obtained from the regular PATH/shim lookup.
//
// Priority policy (versions/ truth source):
//  1. When the PATH resolver already pointed at the same binary (via shim
//     symlink resolution), keep systemPATHOk so the frontend still knows
//     the tool is reachable from the user's shell.
//  2. Otherwise prefer the versions/ binary directly; the shim is demoted
//     to a PATH-entry hint and never gates native detection.
//
// pathSource is set explicitly so debugging traces distinguish the
// versions/ source from the legacy shim source.
func applyClaudeNativeVersionsResolution(rr resolveResult, versionsPath string) resolveResult {
	versionsPath = strings.TrimSpace(versionsPath)
	if versionsPath == "" {
		return rr
	}
	if existing := strings.TrimSpace(rr.executablePath); existing != "" {
		if sameNormalizedPath(existing, versionsPath) {
			// Resolver already saw this binary (possibly through the shim
			// symlink). Preserve the PATH-level metadata so SystemPATHOk
			// is not lost -- this is the "PATH already correct" case.
			rr.pathSource = "native-versions-and-shim"
			return rr
		}
	}
	return resolveResult{
		executablePath: versionsPath,
		systemPATHOk:   rr.systemPATHOk,
		pathState:      PathStateCodeboxPATH,
		pathSource:     "native-versions-truth",
	}
}

func (s *Service) detectClaudeInstallMethod(executablePath string) InstallMethod {
	normalized := normalizeClaudePath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}

	if looksLikeClaudeNPMPath(executablePath) || s.isClaudeNPMGlobalExecutablePath(executablePath) {
		return InstallMethodNPM
	}

	// versions/ truth source: any path inside ~/.local/share/claude/versions/
	// is unambiguously a native binary regardless of shim presence or PATH
	// ordering. This must be checked BEFORE the shim-only probe so a healthy
	// versions/ binary is not misclassified when the shim has been removed.
	if isClaudeNativeVersionsBinaryPath(executablePath) {
		return InstallMethodNative
	}

	if isPathUnderEnvDir(normalized, "USERPROFILE", `.local\bin`) || isPathUnderEnvDir(normalized, "HOME", `.local/bin`) || isClaudeNativeDefaultExecutablePath(executablePath) {
		return InstallMethodNative
	}

	return InstallMethodUnknown
}

func looksLikeClaudeNPMPath(path string) bool {
	normalized := normalizeClaudePath(path)
	return strings.Contains(normalized, "/node_modules/") || strings.Contains(normalized, "/npm/")
}

func isClaudeNPMPackageBinaryFallbackPath(path string) bool {
	normalized := normalizeClaudePath(path)
	if normalized == "" {
		return false
	}
	if !strings.Contains(normalized, "/node_modules/@anthropic-ai/") {
		return false
	}
	if !(strings.HasSuffix(normalized, "/claude.exe") || strings.HasSuffix(normalized, "/claude")) {
		return false
	}
	for _, marker := range []string{
		"/claude-code-win32-arm64/",
		"/claude-code-win32-x64/",
		"/claude-code-darwin-arm64/",
		"/claude-code-darwin-x64/",
		"/claude-code-linux-arm64/",
		"/claude-code-linux-x64/",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func normalizeClaudePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	// Always normalize separators to forward slash for consistent substring
	// matching across platforms. Windows paths like C:\Tools\Claude.exe and
	// C:/Tools/Claude.exe should produce the same normalized result.
	cleaned := strings.ReplaceAll(filepath.Clean(trimmed), `\`, "/")
	return strings.ToLower(cleaned)
}

func isPathUnderEnvDir(normalizedPath string, envKey string, relativeDir string) bool {
	base := strings.TrimSpace(os.Getenv(envKey))
	if base == "" {
		return false
	}
	root := normalizeClaudePath(filepath.Join(base, relativeDir))
	return pathHasPrefix(normalizedPath, root)
}

func pathHasPrefix(path string, prefix string) bool {
	if prefix == "" {
		return false
	}
	if path == prefix {
		return true
	}
	// Use forward slash as the universal separator since normalizeClaudePath
	// normalizes all paths to forward slashes.
	separator := "/"
	return strings.HasPrefix(path, strings.TrimRight(prefix, `/\`)+separator)
}

// buildClaudeNativeAvailableAlongsideNPMHint returns a non-blocking info
// issue when the active claude resolves to an npm path but a healthy native
// binary is also available under versions/. This addresses R6 from the baize
// exploration report: when PATH ordering puts an npm residual shim ahead of
// the native shim, CodeBox should still surface the existence of a healthy
// Native install and offer the user a way to clean up the npm residue. The
// hint is purely informational -- it never mutates status.InstallMethod.
//
// Returns nil when no versions/ native binary exists, or when the active
// detection path already IS the versions/ binary (no ambiguity).
func buildClaudeNativeAvailableAlongsideNPMHint(activeDetectionPath string) *CheckIssue {
	if activeDetectionPath == "" {
		return nil
	}
	nativePath, err := resolveClaudeNativeVersionsBinary()
	if err != nil || nativePath == "" {
		return nil
	}
	if sameNormalizedPath(activeDetectionPath, nativePath) {
		return nil
	}
	return &CheckIssue{
		Severity: SeverityInfo,
		Code:     "claude_native_available_alongside_npm",
		Message:  "检测到 Native Claude Code 二进制可用，但当前 PATH 命中的是 npm 安装；如需切换到 Native 模式可清理 npm 残留后重新检测",
		Detail: fmt.Sprintf(
			"Native 二进制: %s\n当前 PATH 命中: %s",
			nativePath, activeDetectionPath,
		),
		Solutions: []ResolutionAction{
			{
				Type:            SolutionCleanClaudeInstall,
				Description:     "清理 npm 安装残留并切换到 Native",
				Tool:            ToolClaudeCode,
				RequiresConfirm: true,
				IsPrimary:       true,
			},
			{
				Type:        SolutionManualCommand,
				Description: "手动清理 npm Claude Code 残留",
				Command:     "npm uninstall -g @anthropic-ai/claude-code",
				Tool:        ToolClaudeCode,
			},
		},
	}
}

func (s *Service) confirmClaudeNPMInstall() error {
	npmPath := s.resolveNPMPath()
	env := s.buildEnhancedEnv()

	ctx, cancel := context.WithTimeout(context.Background(), claudeNPMCheckTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"list", "-g", "@anthropic-ai/claude-code", "--depth=0"},
		Env:    env,
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("npm list -g @anthropic-ai/claude-code --depth=0: %s", message)
	}

	if !strings.Contains(resultText(result), "@anthropic-ai/claude-code") {
		return fmt.Errorf("global npm package @anthropic-ai/claude-code was not listed")
	}
	return nil
}
