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
// a CheckStatus. When the executable is not on PATH, it falls back to the
// CodeBox-managed venv candidates (mirrors the Claude Native default-location
// fallback pattern) because resolveExecutable uses os.Environ() and cannot
// see the enhanced PATH where the venv bin is injected.
func (s *Service) checkHeadroom() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolHeadroom,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	rr := resolveExecutable(headroomCommandName)
	// Venv fallback: resolveExecutable reads os.Environ(), which does not
	// include the venv bin directory buildEnhancedEnv injects. When the PATH
	// lookup misses, check the CodeBox venv candidates directly. This mirrors
	// firstExistingClaudeNativeDefaultPath so detection works even though the
	// enhanced PATH is invisible to the resolver.
	if strings.TrimSpace(rr.executablePath) == "" {
		if venvPath := s.firstExistingHeadroomVenvPath(); venvPath != "" {
			rr = resolveResult{
				executablePath: venvPath,
				systemPATHOk:   false,
				pathState:      PathStateCodeboxPATH,
				pathSource:     "codebox-venv",
			}
		}
	}
	applyPathStateToStatus(status, rr, ToolHeadroom)

	if strings.TrimSpace(rr.executablePath) == "" {
		status.Error = "Headroom executable was not found in PATH"
		addMissingToolIssue(status, ToolHeadroom)
		return status, nil
	}

	realPath := resolveRealExecutablePath(rr.executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = s.detectHeadroomInstallMethod(realPath)

	version, err := s.headroomVersion(realPath)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	return status, nil
}

// headroomVenvExecutableCandidates returns the platform-specific headroom
// executable paths inside the CodeBox-managed venv. Mirrors
// claudeNativeDefaultExecutableCandidates.
func (s *Service) headroomVenvExecutableCandidates() []string {
	binDir := s.headroomVenvBinDir()
	if binDir == "" {
		return nil
	}
	if isWindows() {
		return []string{
			filepath.Join(binDir, "headroom.exe"),
			filepath.Join(binDir, "headroom.cmd"),
			filepath.Join(binDir, "headroom"),
		}
	}
	return []string{filepath.Join(binDir, "headroom")}
}

// firstExistingHeadroomVenvPath returns the first venv headroom candidate that
// exists on disk, or "" when none is present. Mirrors
// firstExistingClaudeNativeDefaultPath.
func (s *Service) firstExistingHeadroomVenvPath() string {
	for _, candidate := range s.headroomVenvExecutableCandidates() {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
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
// CodeBox venv installs land under the managed headroom-venv directory and are
// distinguished from system pip installs (which also contain site-packages)
// by checking the venv root first. System pip installs land under
// site-packages (POSIX) or a Scripts directory (Windows). Anything else falls
// back to native (e.g. a manual binary drop).
func (s *Service) detectHeadroomInstallMethod(executablePath string) InstallMethod {
	normalized := normalizeHeadroomPath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}
	// CodeBox venv paths take priority: a venv install lives under the
	// managed headroom-venv directory and would otherwise be misclassified as
	// a system pip install because venv site-packages also contains the
	// "site-packages" marker. Resolve symlinks on the venv root so the prefix
	// comparison agrees with the already-resolved executable path (e.g. macOS
	// /var → /private/var).
	if venvDir := strings.TrimSpace(s.headroomVenvDir); venvDir != "" {
		venvRootNormalized := normalizeHeadroomPath(resolveRealExecutablePath(venvDir))
		if venvRootNormalized != "" && pathHasPrefix(normalized, venvRootNormalized) {
			return InstallMethodCodeboxVenv
		}
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
