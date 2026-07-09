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
	headroomCommandName    = "headroom"
	headroomVersionTimeout = 10 * time.Second
)

// headroomVersionPattern matches a dotted numeric version string such as
// "0.30.0" or "1.2.3-rc1", mirroring codexVersionPattern.
var headroomVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.-]+)?`)

// checkHeadroom detects the Headroom context-compression proxy CLI. It mirrors
// checkCodex: resolve the executable, probe `headroom --version`, and populate
// a CheckStatus.
func (s *Service) checkHeadroom() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolHeadroom,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	rr := resolveExecutable(headroomCommandName)
	applyPathStateToStatus(status, rr, ToolHeadroom)

	if strings.TrimSpace(rr.executablePath) == "" {
		status.Error = "Headroom executable was not found in PATH"
		addMissingToolIssue(status, ToolHeadroom)
		return status, nil
	}

	realPath := resolveRealExecutablePath(rr.executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = detectHeadroomInstallMethod(realPath)

	version, err := s.headroomVersion(realPath)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	return status, nil
}

// headroomVersion runs `headroom --version` (falling back to -V) and extracts
// a dotted version number from the output.
func (s *Service) headroomVersion(executablePath string) (string, error) {
	for _, args := range [][]string{{"--version"}, {"-V"}} {
		version, err := s.runHeadroomVersion(executablePath, args)
		if err == nil && version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("parse Headroom version using --version or -V")
}

func (s *Service) runHeadroomVersion(executablePath string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), headroomVersionTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   executablePath,
		Args:   append([]string(nil), args...),
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("run headroom %s: %s", strings.Join(args, " "), message)
	}

	version := headroomVersionPattern.FindString(strings.TrimSpace(resultText(result)))
	if version == "" {
		return "", fmt.Errorf("parse Headroom version from output %q", resultText(result))
	}
	return version, nil
}

// detectHeadroomInstallMethod infers how headroom was installed from its path.
// pip installs land under site-packages (POSIX) or a Scripts directory
// (Windows). Anything else falls back to native (e.g. a manual binary drop).
func detectHeadroomInstallMethod(executablePath string) InstallMethod {
	normalized := normalizeHeadroomPath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}
	if strings.Contains(normalized, "site-packages") ||
		strings.Contains(normalized, "/scripts/") ||
		strings.HasSuffix(normalized, "/scripts") {
		return InstallMethodPip
	}
	return InstallMethodNative
}

// normalizeHeadroomPath normalizes a filesystem path for cross-platform
// substring matching: cleaned, forward-slashed, lowercased. Mirrors the
// per-checker normalize*Path helpers.
func normalizeHeadroomPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(filepath.Clean(trimmed), `\`, "/")
	return strings.ToLower(cleaned)
}
