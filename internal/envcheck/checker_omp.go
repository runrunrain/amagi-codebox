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
	ompCommandName    = "omp"
	ompVersionTimeout = 10 * time.Second
)

// ompVersionPattern 匹配语义化版本号（如 17.2.10）。
// omp 的 --version 输出形如 "omp/17.2.10"（同 pi 的 "pi/0.81.1" 风格）。
var ompVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.-]+)?`)

// checkOmp 检查 omp 安装状态（复刻 checkPi）。
// 探测顺序：PATH 探测 -> npm global prefix 兜底 -> omp --version -> 安装方式判定。
func (s *Service) checkOmp() (*CheckStatus, error) {
	now := time.Now()
	status := &CheckStatus{
		Tool:          ToolOmp,
		InstallMethod: InstallMethodUnknown,
		CheckedAt:     now,
	}

	rr := resolveExecutable(ompCommandName)
	applyPathStateToStatus(status, rr, ToolOmp)

	if strings.TrimSpace(rr.executablePath) == "" {
		if npmStatus, _, npmErr := s.checkOmpFromNPMGlobalPrefix(); npmErr == nil {
			return npmStatus, nil
		}
		status.Error = "omp executable was not found in PATH"
		addMissingToolIssue(status, ToolOmp)
		return status, nil
	}

	realPath := resolveRealExecutablePath(rr.executablePath)
	status.Installed = true
	status.ExecutablePath = realPath
	status.InstallMethod = detectOmpInstallMethod(realPath)

	version, err := s.ompVersion(realPath)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.Version = version

	return status, nil
}

// checkOmpFromNPMGlobalPrefix 在 PATH 未命中时兜底探测 npm global prefix
// 下的 omp 可执行文件（复刻 checkPiFromNPMGlobalPrefix）。
func (s *Service) checkOmpFromNPMGlobalPrefix() (*CheckStatus, []string, error) {
	prefix, err := s.npmGlobalPrefix()
	if err != nil {
		return nil, nil, err
	}
	npmRoot, rootErr := s.npmGlobalRoot()
	if rootErr != nil {
		npmRoot = inferNPMNodeModulesFromPrefix(prefix)
	}
	candidates := ompNPMGlobalExecutableCandidatesWithRoot(prefix, npmRoot)
	if len(candidates) == 0 {
		return nil, candidates, fmt.Errorf("npm global prefix %q did not produce omp executable candidates", prefix)
	}

	diagnostics := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		invocationPath := filepath.Clean(candidate)
		realPath := resolveRealExecutablePath(invocationPath)
		version, err := s.ompVersion(invocationPath)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: %s", invocationPath, sanitizeInstallerOutput(err.Error())))
			continue
		}
		status := &CheckStatus{
			Tool:           ToolOmp,
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
		return nil, candidates, fmt.Errorf("omp npm global prefix candidates were found but unusable: %s", strings.Join(diagnostics, "; "))
	}
	return nil, candidates, fmt.Errorf("omp executable not found under npm global prefix candidates: %s", strings.Join(candidates, ", "))
}

func ompNPMGlobalExecutableCandidates(prefix string) []string {
	return ompNPMGlobalExecutableCandidatesWithRoot(prefix, "")
}

func ompNPMGlobalExecutableCandidatesWithRoot(prefix, npmRoot string) []string {
	return npmGlobalCommandCandidates(prefix, npmRoot, ompCommandName, ompNPMPackageName)
}

// ompVersion 探测 omp 版本。omp 的 --version 输出形如 "omp/17.2.10"。
// 同时尝试 -v 作为兜底（omp -v 同样输出版本号）。
func (s *Service) ompVersion(executablePath string) (string, error) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		version, err := s.runOmpVersion(executablePath, args)
		if err == nil && version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("parse omp version using --version or -v")
}

func (s *Service) runOmpVersion(executablePath string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ompVersionTimeout)
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
		return "", fmt.Errorf("run omp %s: %s", strings.Join(args, " "), message)
	}

	version := parseOmpVersion(resultText(result))
	if version == "" {
		return "", fmt.Errorf("parse omp version from output %q", resultText(result))
	}
	return version, nil
}

func parseOmpVersion(output string) string {
	return ompVersionPattern.FindString(strings.TrimSpace(output))
}

// detectOmpInstallMethod 判定 omp 的安装方式（复刻 detectPiInstallMethod 并
// 增加 brew 判定）：
//   - 路径含 node_modules / npm 段      -> InstallMethodNPM
//   - 路径含 homebrew / Cellar 段        -> InstallMethodHomebrew
//   - 其余                              -> InstallMethodNative
//
// Homebrew 安装的 omp 位于 /opt/homebrew/Cellar/omp/<version>/bin/omp（symlink
// 解析后含 "Cellar"），brew --prefix 形式含 "/homebrew/"。
func detectOmpInstallMethod(executablePath string) InstallMethod {
	normalized := normalizeOmpPath(executablePath)
	if normalized == "" {
		return InstallMethodUnknown
	}
	if strings.Contains(normalized, "node_modules") || strings.Contains(normalized, pathSegment("npm")) {
		return InstallMethodNPM
	}
	if strings.Contains(normalized, "homebrew") || strings.Contains(normalized, "cellar") {
		return InstallMethodHomebrew
	}
	return InstallMethodNative
}

func normalizeOmpPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	// 始终归一化为正斜杠，便于跨平台子串匹配。
	cleaned := strings.ReplaceAll(filepath.Clean(trimmed), `\`, "/")
	return strings.ToLower(cleaned)
}
