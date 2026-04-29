package envcheck

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
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

	rr := resolveExecutable(codexCommandName)
	applyPathStateToStatus(status, rr, ToolCodex)

	if strings.TrimSpace(rr.executablePath) == "" {
		status.Error = "Codex executable was not found in PATH"
		addMissingToolIssue(status, ToolCodex)
		return status, nil
	}

	realPath := resolveRealExecutablePath(rr.executablePath)
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
	// Always normalize to forward slash for cross-platform substring matching.
	cleaned := strings.ReplaceAll(filepath.Clean(trimmed), `\`, "/")
	return strings.ToLower(cleaned)
}

func pathSegment(segment string) string {
	// Use forward slash to match normalizeCodexPath convention.
	return "/" + strings.ToLower(segment) + "/"
}
