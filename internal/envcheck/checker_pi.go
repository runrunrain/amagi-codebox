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
	piCommandName    = "pi"
	piVersionTimeout = 10 * time.Second
)

// piVersionPattern 匹配语义化版本号（如 0.81.1）。
// 与 codexVersionPattern 同形：主版本+至少一段次版本+可选预发布/构建后缀。
var piVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.-]+)?`)

func (s *Service) checkPi() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolPi,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	rr := resolveExecutable(piCommandName)
	applyPathStateToStatus(status, rr, ToolPi)

	if strings.TrimSpace(rr.executablePath) == "" {
		if npmStatus, _, npmErr := s.checkPiFromNPMGlobalPrefix(); npmErr == nil {
			return npmStatus, nil
		}
		status.Error = "Pi executable was not found in PATH"
		addMissingToolIssue(status, ToolPi)
		return status, nil
	}

	realPath := resolveRealExecutablePath(rr.executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = detectPiInstallMethod(realPath)

	version, err := s.piVersion(realPath)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	return status, nil
}

func (s *Service) checkPiFromNPMGlobalPrefix() (*CheckStatus, []string, error) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil {
		return nil, nil, err
	}
	npmRoot, rootErr := s.npmGlobalRoot()
	if rootErr != nil {
		npmRoot = inferNPMNodeModulesFromPrefix(prefix)
	}
	candidates := piNPMGlobalExecutableCandidatesWithRoot(prefix, npmRoot)
	if len(candidates) == 0 {
		return nil, candidates, fmt.Errorf("npm global prefix %q did not produce Pi executable candidates", prefix)
	}

	diagnostics := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		invocationPath := filepath.Clean(candidate)
		realPath := resolveRealExecutablePath(invocationPath)
		version, err := s.piVersion(invocationPath)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %s", invocationPath, sanitizeInstallerOutput(err.Error())))
			continue
		}
		status := &CheckStatus{
			Tool:           ToolPi,
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
		return nil, candidates, fmt.Errorf("Pi npm global prefix candidates were found but unusable: %s", strings.Join(diagnostics, "; "))
	}
	return nil, candidates, fmt.Errorf("Pi executable not found under npm global prefix candidates: %s", strings.Join(candidates, ", "))
}

func piNPMGlobalExecutableCandidates(prefix string) []string {
	return piNPMGlobalExecutableCandidatesWithRoot(prefix, "")
}

func piNPMGlobalExecutableCandidatesWithRoot(prefix, npmRoot string) []string {
	return npmGlobalCommandCandidates(prefix, npmRoot, piCommandName, piNPMPackageName)
}

// piVersion 探测 Pi 版本。Pi 的 --version 输出纯版本号（如 "0.81.1"）。
// 同时尝试 -v 作为兜底（pi -v 同样输出版本号）。
func (s *Service) piVersion(executablePath string) (string, error) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		version, err := s.runPiVersion(executablePath, args)
		if err == nil && version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("parse Pi version using --version or -v")
}

func (s *Service) runPiVersion(executablePath string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), piVersionTimeout)
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
		return "", fmt.Errorf("run pi %s: %s", strings.Join(args, " "), message)
	}

	version := parsePiVersion(resultText(result))
	if version == "" {
		return "", fmt.Errorf("parse Pi version from output %q", resultText(result))
	}
	return version, nil
}

func parsePiVersion(output string) string {
	return piVersionPattern.FindString(strings.TrimSpace(output))
}

func detectPiInstallMethod(executablePath string) InstallMethod {
	normalized := normalizePiPath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}
	if strings.Contains(normalized, "node_modules") || strings.Contains(normalized, pathSegment("npm")) {
		return InstallMethodNPM
	}
	return InstallMethodNative
}

func normalizePiPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	// 始终归一化为正斜杠，便于跨平台子串匹配。
	cleaned := strings.ReplaceAll(filepath.Clean(trimmed), `\`, "/")
	return strings.ToLower(cleaned)
}
