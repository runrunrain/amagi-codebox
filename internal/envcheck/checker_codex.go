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
		if npmStatus, _, npmErr := s.checkCodexFromNPMGlobalPrefix(); npmErr == nil {
			return npmStatus, nil
		}
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

func (s *Service) checkCodexFromNPMGlobalPrefix() (*CheckStatus, []string, error) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil {
		return nil, nil, err
	}
	npmRoot, rootErr := s.npmGlobalRoot()
	if rootErr != nil {
		npmRoot = inferNPMNodeModulesFromPrefix(prefix)
	}
	candidates := codexNPMGlobalExecutableCandidatesWithRoot(prefix, npmRoot)
	if len(candidates) == 0 {
		return nil, candidates, fmt.Errorf("npm global prefix %q did not produce Codex executable candidates", prefix)
	}

	diagnostics := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		invocationPath := filepath.Clean(candidate)
		realPath := resolveRealExecutablePath(invocationPath)
		version, err := s.codexVersion(invocationPath)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %s", invocationPath, sanitizeInstallerOutput(err.Error())))
			continue
		}
		status := &CheckStatus{
			Tool:           ToolCodex,
			Installed:      true,
			InstallMethod:  InstallMethodNPM,
			Version:        version,
			PATHOk:         true,
			ExecutablePath: realPath,
			CheckedAt:      time.Now(),
			SystemPATHOk:   pathDirInProcessPATH(filepath.Dir(realPath)),
			PathState:      PathStateCodeboxPATH,
			PathSource:     "npm global prefix",
		}
		if status.SystemPATHOk {
			status.PathState = PathStateSystemPATH
		}
		return status, candidates, nil
	}

	if len(diagnostics) > 0 {
		return nil, candidates, fmt.Errorf("Codex npm global prefix candidates were found but unusable: %s", strings.Join(diagnostics, "; "))
	}
	return nil, candidates, fmt.Errorf("Codex executable not found under npm global prefix candidates: %s", strings.Join(candidates, ", "))
}

func codexNPMGlobalExecutableCandidates(prefix string) []string {
	return codexNPMGlobalExecutableCandidatesWithRoot(prefix, "")
}

func codexNPMGlobalExecutableCandidatesWithRoot(prefix, npmRoot string) []string {
	return npmGlobalCommandCandidates(prefix, npmRoot, codexCommandName, codexNPMPackageName)
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
		Env:    s.buildEnhancedEnv(),
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
