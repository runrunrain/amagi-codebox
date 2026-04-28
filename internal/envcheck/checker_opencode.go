package envcheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

const (
	openCodeCommandName    = "opencode"
	openCodeVersionTimeout = 10 * time.Second
)

var openCodeVersionPattern = regexp.MustCompile(`v?([0-9]+(?:\.[0-9]+){1,3}(?:[-+][0-9A-Za-z.-]+)?)`)

func (s *Service) checkOpenCode() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolOpenCode,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	pathFromPATH, lookErr := exec.LookPath(openCodeCommandName)
	status.PATHOk = lookErr == nil && strings.TrimSpace(pathFromPATH) != ""

	executablePath := pathFromPATH
	if strings.TrimSpace(executablePath) == "" {
		resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
		resolved, _, err := resolver.ResolveExecutable(openCodeCommandName, nil, os.Environ())
		if err == nil {
			executablePath = resolved.Path
		}
	}

	if strings.TrimSpace(executablePath) == "" {
		status.Error = "OpenCode executable was not found in PATH"
		return status, nil
	}

	realPath := resolveRealExecutablePath(executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = detectOpenCodeInstallMethod(realPath)

	version, err := s.openCodeVersion(realPath)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	return status, nil
}

func (s *Service) openCodeVersion(executablePath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openCodeVersionTimeout)
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
		return "", fmt.Errorf("run opencode --version: %s", message)
	}

	version := parseOpenCodeVersion(resultText(result))
	if version == "" {
		return "", fmt.Errorf("parse OpenCode version from output %q", resultText(result))
	}
	return version, nil
}

func parseOpenCodeVersion(output string) string {
	match := openCodeVersionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func detectOpenCodeInstallMethod(executablePath string) InstallMethod {
	normalized := normalizeOpenCodePath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}

	if isOpenCodeChocolateyPath(normalized) || isOpenCodeScoopPath(normalized) {
		return InstallMethodNative
	}
	if isOpenCodeNPMPath(normalized) {
		return InstallMethodNPM
	}
	return InstallMethodUnknown
}

func normalizeOpenCodePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if runtime.GOOS == "windows" {
		cleaned = strings.ReplaceAll(cleaned, "/", `\`)
	}
	return strings.ToLower(cleaned)
}

func isOpenCodeNPMPath(normalizedPath string) bool {
	if strings.Contains(normalizedPath, "node_modules") {
		return true
	}
	if isPathUnderEnvDir(normalizedPath, "APPDATA", "npm") || isPathUnderEnvDir(normalizedPath, "LOCALAPPDATA", "npm") {
		return true
	}
	if isPathUnderEnvDir(normalizedPath, "HOME", filepath.Join(".npm-global", "bin")) {
		return true
	}
	return strings.Contains(normalizedPath, pathFragment(".npm-global", "bin"))
}

func isOpenCodeChocolateyPath(normalizedPath string) bool {
	return strings.Contains(normalizedPath, pathFragment("programdata", "chocolatey")) ||
		strings.Contains(normalizedPath, pathFragment("chocolatey", "bin"))
}

func isOpenCodeScoopPath(normalizedPath string) bool {
	return strings.Contains(normalizedPath, pathFragment("scoop", "apps")) ||
		strings.Contains(normalizedPath, pathFragment("scoop", "shims"))
}

func pathFragment(parts ...string) string {
	fragment := filepath.Join(parts...)
	if runtime.GOOS == "windows" {
		fragment = strings.ReplaceAll(fragment, "/", `\`)
	}
	return strings.ToLower(fragment)
}
