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
	codexCommandName    = "codex"
	codexVersionTimeout = 10 * time.Second
)

var codexVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.-]+)?`)

func (s *Service) checkCodex() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolCodex,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	pathFromPATH, lookErr := exec.LookPath(codexCommandName)
	status.PATHOk = lookErr == nil && strings.TrimSpace(pathFromPATH) != ""

	executablePath := pathFromPATH
	if strings.TrimSpace(executablePath) == "" {
		resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
		resolved, _, err := resolver.ResolveExecutable(codexCommandName, nil, os.Environ())
		if err == nil {
			executablePath = resolved.Path
		}
	}

	if strings.TrimSpace(executablePath) == "" {
		status.Error = "Codex executable was not found in PATH"
		return status, nil
	}

	realPath := resolveRealExecutablePath(executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = detectCodexInstallMethod(realPath)

	version, err := s.codexVersion(realPath)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	return status, nil
}

func (s *Service) codexVersion(executablePath string) (string, error) {
	for _, args := range [][]string{{"--version"}, {"-V"}} {
		version, err := s.runCodexVersion(executablePath, args)
		if err == nil && version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("parse Codex version using --version or -V")
}

func (s *Service) runCodexVersion(executablePath string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexVersionTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   executablePath,
		Args:   append([]string(nil), args...),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("run codex %s: %s", strings.Join(args, " "), message)
	}

	version := parseCodexVersion(resultText(result))
	if version == "" {
		return "", fmt.Errorf("parse Codex version from output %q", resultText(result))
	}
	return version, nil
}

func parseCodexVersion(output string) string {
	return codexVersionPattern.FindString(strings.TrimSpace(output))
}

func detectCodexInstallMethod(executablePath string) InstallMethod {
	normalized := normalizeCodexPath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}
	if strings.Contains(normalized, "node_modules") || strings.Contains(normalized, pathSegment("npm")) {
		return InstallMethodNPM
	}
	return InstallMethodNative
}

func normalizeCodexPath(path string) string {
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

func pathSegment(segment string) string {
	separator := string(os.PathSeparator)
	if runtime.GOOS == "windows" {
		separator = `\`
	}
	return separator + strings.ToLower(segment) + separator
}
