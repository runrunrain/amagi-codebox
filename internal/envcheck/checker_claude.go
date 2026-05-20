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
	if nativePath := firstExistingClaudeNativeDefaultPath(); shouldPreferClaudeNativeDefaultPath(rr, nativePath) {
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
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	if status.InstallMethod == InstallMethodNPM {
		if err := s.confirmClaudeNPMInstall(); err != nil {
			status.InstallMethod = InstallMethodUnknown
			status.Error = fmt.Sprintf("检测到类 npm 的 Claude Code 路径，但 npm 全局包确认失败: %v", err)
			return status, nil
		}
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
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("run claude --version: %s", message)
	}

	version := parseClaudeVersion(resultText(result))
	if version == "" {
		return "", fmt.Errorf("parse Claude Code version from output %q", resultText(result))
	}
	return version, nil
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

func (s *Service) detectClaudeInstallMethod(executablePath string) InstallMethod {
	normalized := normalizeClaudePath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}

	if looksLikeClaudeNPMPath(executablePath) || s.isClaudeNPMGlobalExecutablePath(executablePath) {
		return InstallMethodNPM
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
