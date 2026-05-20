package envcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"amagi-codebox/internal/platform"
)

const (
	openCodeCommandName    = "opencode"
	openCodeVersionTimeout = 10 * time.Second
)

var openCodeVersionPattern = regexp.MustCompile(`v?([0-9]+(?:\.[0-9]+){1,3}(?:[-+][0-9A-Za-z.-]+)?)`)

var openCodeFallbackPackages = []string{"opencode-ai", "opencode"}

func (s *Service) checkOpenCode() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolOpenCode,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	rr := resolveExecutable(openCodeCommandName)
	applyPathStateToStatus(status, rr, ToolOpenCode)

	if strings.TrimSpace(rr.executablePath) == "" {
		status.Error = "OpenCode executable was not found in PATH"
		addMissingToolIssue(status, ToolOpenCode)
		return status, nil
	}

	realPath := resolveRealExecutablePath(rr.executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = detectOpenCodeInstallMethod(realPath)

	version, diagnostics, err := s.openCodeVersionWithDiagnostics(realPath)
	status.Issues = append(status.Issues, diagnostics...)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	return status, nil
}

func (s *Service) checkOpenCodeFromNPMGlobalPrefix() (*CheckStatus, []string, error) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil {
		return nil, nil, err
	}
	candidates := openCodeNPMGlobalExecutableCandidates(prefix)
	if len(candidates) == 0 {
		return nil, candidates, fmt.Errorf("npm global prefix %q did not produce OpenCode executable candidates", prefix)
	}

	diagnostics := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		invocationPath := filepath.Clean(candidate)
		realPath := resolveRealExecutablePath(invocationPath)
		version, issues, err := s.openCodeVersionWithDiagnostics(invocationPath)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %s", invocationPath, sanitizeInstallerOutput(err.Error())))
			continue
		}
		status := &CheckStatus{
			Tool:           ToolOpenCode,
			Installed:      true,
			InstallMethod:  InstallMethodNPM,
			Version:        version,
			PATHOk:         true,
			ExecutablePath: realPath,
			CheckedAt:      time.Now(),
			SystemPATHOk:   false,
			PathState:      PathStateCodeboxPATH,
			PathSource:     "npm global prefix",
			Issues:         issues,
		}
		return status, candidates, nil
	}

	if len(diagnostics) > 0 {
		return nil, candidates, fmt.Errorf("OpenCode npm global prefix candidates were found but unusable: %s", strings.Join(diagnostics, "; "))
	}
	return nil, candidates, fmt.Errorf("OpenCode executable not found under npm global prefix candidates: %s", strings.Join(candidates, ", "))
}

func openCodeNPMGlobalExecutableCandidates(prefix string) []string {
	prefix = filepath.Clean(strings.TrimSpace(prefix))
	if prefix == "" || prefix == "." {
		return nil
	}

	dirs := []string{filepath.Join(prefix, "bin"), prefix, filepath.Join(prefix, "node_modules", ".bin")}
	names := []string{openCodeCommandName}
	if isWindows() {
		names = []string{"opencode.cmd", "opencode.exe", openCodeCommandName}
	}

	candidates := make([]string, 0, len(dirs)*len(names))
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			key := normalizeOpenCodePath(candidate)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func (s *Service) openCodeVersion(executablePath string) (string, error) {
	version, _, err := s.openCodeVersionWithDiagnostics(executablePath)
	return version, err
}

func (s *Service) openCodeVersionWithDiagnostics(executablePath string) (string, []CheckIssue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openCodeVersionTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   executablePath,
		Args:   []string{"--version"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		directDiagnostic := openCodeVersionFailureDiagnostic(result, err)
		if openCodeVersionErrorAllowsFallback(directDiagnostic) {
			if fallbackVersion, fallbackDiagnostic, fallbackErr := s.openCodeFallbackVersion(executablePath); fallbackErr == nil {
				return fallbackVersion, []CheckIssue{
					{
						Severity: SeverityWarning,
						Code:     "opencode_version_fallback_used",
						Message:  "OpenCode 主入口版本检测异常，已通过 npm/package manifest 替代检测确认安装可用",
						Detail:   fmt.Sprintf("主入口诊断：%s；替代检测：%s", directDiagnostic, fallbackDiagnostic),
						Solutions: []ResolutionAction{
							{Type: SolutionRetry, Description: "重新检测 OpenCode 状态", Tool: ToolOpenCode},
						},
					},
				}, nil
			}
		}
		return "", nil, fmt.Errorf("run opencode --version: %s；替代检测也未确认 OpenCode 可用。可能是 npm 入口、Windows spawn 或 Node.js v24 兼容异常，请重新检测或重新安装 opencode-ai", directDiagnostic)
	}

	version := parseOpenCodeVersion(resultText(result))
	if version == "" {
		return "", nil, fmt.Errorf("parse OpenCode version from output %q", resultText(result))
	}
	return version, nil, nil
}

func openCodeVersionErrorAllowsFallback(diagnostic string) bool {
	lower := strings.ToLower(strings.TrimSpace(diagnostic))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"spawn",
		"eftype",
		"enoexec",
		"exec format",
		"cannot execute binary",
		"bad cpu type",
		"node.js",
		"node v",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func openCodeVersionFailureDiagnostic(result *platform.ProcessResult, err error) string {
	message := strings.TrimSpace(resultText(result))
	if message == "" && err != nil {
		message = err.Error()
	}
	if message == "" {
		message = "no stdout/stderr captured"
	}
	return sanitizeInstallerOutput(message)
}

func (s *Service) openCodeFallbackVersion(executablePath string) (string, string, error) {
	if version, detail, err := s.openCodeVersionFromNPMList(); err == nil {
		return version, detail, nil
	}
	if version, detail, err := openCodeVersionFromExecutableManifest(executablePath); err == nil {
		return version, detail, nil
	}
	if version, detail, err := s.openCodeVersionFromNPMRootManifest(); err == nil {
		return version, detail, nil
	}
	return "", "", fmt.Errorf("no OpenCode fallback detector confirmed a version")
}

func (s *Service) openCodeVersionFromNPMList() (string, string, error) {
	npmPath := s.resolveNPMPath()
	var diagnostics []string
	for _, pkg := range openCodeFallbackPackages {
		ctx, cancel := context.WithTimeout(context.Background(), openCodeVersionTimeout)
		result, err := s.processRunner.Run(ctx, platform.CommandSpec{
			Path:   npmPath,
			Args:   []string{"list", "-g", pkg, "--depth=0"},
			Env:    s.buildEnhancedEnv(),
			Policy: platform.DefaultProcessPolicy(),
		})
		cancel()
		output := resultText(result)
		if err == nil {
			if version := parseOpenCodeNPMListVersion(output, pkg); version != "" {
				return version, fmt.Sprintf("npm list -g %s --depth=0", pkg), nil
			}
		}
		detail := openCodeVersionFailureDiagnostic(result, err)
		diagnostics = append(diagnostics, fmt.Sprintf("%s: %s", pkg, detail))
	}
	return "", strings.Join(diagnostics, "; "), fmt.Errorf("npm list fallback failed")
}

func parseOpenCodeNPMListVersion(output string, pkg string) string {
	pattern := regexp.MustCompile(regexp.QuoteMeta(pkg) + `@` + `v?([0-9]+(?:\.[0-9]+){1,3}(?:[-+][0-9A-Za-z.-]+)?)`)
	match := pattern.FindStringSubmatch(output)
	if len(match) >= 2 {
		return match[1]
	}
	return parsePackageJSONVersion(output, pkg)
}

func (s *Service) openCodeVersionFromNPMRootManifest() (string, string, error) {
	npmPath := s.resolveNPMPath()
	ctx, cancel := context.WithTimeout(context.Background(), openCodeVersionTimeout)
	defer cancel()
	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"root", "-g"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		return "", "", err
	}
	root := strings.TrimSpace(firstNonEmptyLine(resultText(result)))
	if root == "" {
		return "", "", fmt.Errorf("npm root -g returned empty output")
	}
	for _, pkg := range openCodeFallbackPackages {
		manifestPath := filepath.Join(root, pkg, "package.json")
		if version, err := readPackageJSONVersion(manifestPath, pkg); err == nil {
			return version, manifestPath, nil
		}
	}
	return "", "", fmt.Errorf("OpenCode package manifest not found under npm root %s", root)
}

func openCodeVersionFromExecutableManifest(executablePath string) (string, string, error) {
	path := filepath.Clean(strings.TrimSpace(executablePath))
	if path == "" {
		return "", "", fmt.Errorf("empty executable path")
	}
	dir := filepath.Dir(path)
	for i := 0; i < 8 && dir != "." && dir != string(filepath.Separator); i++ {
		manifestPath := filepath.Join(dir, "package.json")
		for _, pkg := range openCodeFallbackPackages {
			if version, err := readPackageJSONVersion(manifestPath, pkg); err == nil {
				return version, manifestPath, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", "", fmt.Errorf("no package.json for OpenCode near %s", executablePath)
}

func readPackageJSONVersion(path string, expectedName string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	version := parsePackageJSONVersion(string(data), expectedName)
	if version == "" {
		return "", fmt.Errorf("package manifest %s did not contain expected OpenCode version", path)
	}
	return version, nil
}

func parsePackageJSONVersion(content string, expectedName string) string {
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return ""
	}
	name := strings.TrimSpace(manifest.Name)
	if expectedName != "" && name != "" && name != expectedName {
		return ""
	}
	return parseOpenCodeVersion(manifest.Version)
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

	if isOpenCodeNPMPath(normalized) {
		return InstallMethodNPM
	}
	if isOpenCodeChocolateyPath(normalized) || isOpenCodeScoopPath(normalized) {
		return InstallMethodNative
	}
	if isOpenCodeHomebrewPath(normalized) {
		return InstallMethodHomebrew
	}
	return InstallMethodUnknown
}

func normalizeOpenCodePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	// Always normalize to forward slash for cross-platform substring matching.
	cleaned := strings.ReplaceAll(filepath.Clean(trimmed), `\`, "/")
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

func isOpenCodeHomebrewPath(normalizedPath string) bool {
	if normalizedPath == "" || isOpenCodeNPMPath(normalizedPath) {
		return false
	}
	return pathHasSegmentSequence(normalizedPath, "cellar", "opencode") ||
		pathHasSegmentSequence(normalizedPath, "homebrew", "opt", "opencode") ||
		strings.HasSuffix(normalizedPath, pathFragment("homebrew", "bin", "opencode"))
}

func pathHasSegmentSequence(normalizedPath string, sequence ...string) bool {
	if normalizedPath == "" || len(sequence) == 0 {
		return false
	}
	segments := strings.Split(strings.Trim(normalizedPath, "/"), "/")
	if len(segments) < len(sequence) {
		return false
	}
	for i := 0; i <= len(segments)-len(sequence); i++ {
		matched := true
		for j, want := range sequence {
			if segments[i+j] != strings.ToLower(want) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func pathFragment(parts ...string) string {
	// Always use forward slash for cross-platform substring matching,
	// since normalizeOpenCodePath normalizes all inputs to forward slashes.
	return strings.ToLower(strings.Join(parts, "/"))
}
