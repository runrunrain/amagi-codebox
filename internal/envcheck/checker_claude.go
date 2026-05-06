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

func (s *Service) checkClaudeCode() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolClaudeCode,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	rr := resolveExecutable(claudeCommandName)
	applyPathStateToStatus(status, rr, ToolClaudeCode)

	if strings.TrimSpace(rr.executablePath) == "" {
		status.Error = "未在 PATH 中找到 Claude Code 可执行文件"
		addMissingToolIssue(status, ToolClaudeCode)
		return status, nil
	}

	realPath := resolveRealExecutablePath(rr.executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = s.detectClaudeInstallMethod(realPath)

	version, err := s.claudeVersion(realPath)
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
			Message:  "检测到 Claude Code 通过 npm 全局安装；推荐使用官方 Native 安装程序以获得更好的集成体验",
			Solutions: []ResolutionAction{
				{
					Type:        SolutionManualCommand,
					Description: "安装官方 Native Claude Code 安装程序",
					Command:     "irm https://claude.ai/install.ps1 | iex",
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

	if strings.Contains(normalized, "node_modules") || strings.Contains(normalized, "npm") {
		return InstallMethodNPM
	}

	if isPathUnderEnvDir(normalized, "USERPROFILE", `.local\bin`) {
		return InstallMethodNative
	}

	if isPathUnderEnvDir(normalized, "LOCALAPPDATA", `programs\claude code`) || isWingetPath(normalized) {
		return InstallMethodWinget
	}

	return InstallMethodUnknown
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

func isWingetPath(normalizedPath string) bool {
	return strings.Contains(normalizedPath, "/winget/") ||
		strings.Contains(normalizedPath, "/microsoft/winget/") ||
		strings.Contains(normalizedPath, "/windowsapps/") ||
		strings.Contains(normalizedPath, "/packages/microsoft.desktopappinstaller_")
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
