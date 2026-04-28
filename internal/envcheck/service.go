package envcheck

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/platform"
)

const (
	latestVersionCacheTTL = 24 * time.Hour
	latestVersionTimeout  = 10 * time.Second
)

type latestVersionCacheEntry struct {
	Version   string
	CheckedAt time.Time
}

// EnvCheckService defines the Wails-facing contract for checking and managing
// supported CLI tools. Concrete detection and installation logic is implemented
// in checker_*.go files.
type EnvCheckService interface {
	CheckAll() (*OverallStatus, error)
	CheckOne(tool CLITool) (*CheckStatus, error)
	CheckLatestVersion(tool CLITool) (latestVersion string, err error)
	Install(tool CLITool) (*InstallResult, error)
	Update(tool CLITool) (*InstallResult, error)
	GetCachedStatus() *OverallStatus
}

// Service implements EnvCheckService.
type Service struct {
	mu            sync.RWMutex
	cache         *OverallStatus
	versionCache  map[CLITool]latestVersionCacheEntry
	processRunner platform.ProcessRunner
}

// NewService creates an EnvCheck service with the default platform process
// runner. Use NewServiceWithRunner in tests to inject a mock runner.
func NewService() *Service {
	return NewServiceWithRunner(platform.NewProcessRunner())
}

// NewServiceWithRunner creates an EnvCheck service with an injected runner.
func NewServiceWithRunner(runner platform.ProcessRunner) *Service {
	if runner == nil {
		runner = platform.NewProcessRunner()
	}
	return &Service{
		cache:         emptyOverallStatus(),
		versionCache:  map[CLITool]latestVersionCacheEntry{},
		processRunner: runner,
	}
}

// CheckAll checks every supported CLI tool and updates the in-memory cache.
func (s *Service) CheckAll() (*OverallStatus, error) {
	overall := emptyOverallStatus()
	var firstErr error
	for _, tool := range SupportedTools() {
		status, err := s.CheckOne(tool)
		if status == nil && err != nil && firstErr == nil {
			firstErr = err
		}
		if status == nil {
			continue
		}
		overall.Items[string(tool)] = *status
		if status.CheckedAt.After(overall.CheckedAt) {
			overall.CheckedAt = status.CheckedAt
		}
		if !status.Installed || !status.PATHOk || strings.TrimSpace(status.Error) != "" {
			overall.Issues = append(overall.Issues, toolIssue(status))
		}
	}
	overall.AllOK = len(overall.Issues) == 0 && len(overall.Items) == len(SupportedTools())

	s.mu.Lock()
	s.cache = cloneOverallStatus(overall)
	s.mu.Unlock()
	return overall, firstErr
}

// CheckOne checks a single supported CLI tool.
func (s *Service) CheckOne(tool CLITool) (*CheckStatus, error) {
	if !IsValidCLITool(tool) {
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}
	switch tool {
	case ToolClaudeCode:
		status, err := s.checkClaudeCode()
		return s.finishToolCheck(status, err)
	case ToolOpenCode:
		status, err := s.checkOpenCode()
		return s.finishToolCheck(status, err)
	case ToolCodex:
		status, err := s.checkCodex()
		return s.finishToolCheck(status, err)
	}
	return nil, fmt.Errorf("envcheck CheckOne not implemented for tool: %s", tool)
}

func (s *Service) finishToolCheck(status *CheckStatus, err error) (*CheckStatus, error) {
	if status == nil {
		return nil, err
	}
	if err != nil && strings.TrimSpace(status.Error) == "" {
		status.Error = err.Error()
	}
	s.enrichWithLatestVersion(status)
	s.cacheToolStatus(status)
	return status, nil
}

func (s *Service) cacheToolStatus(status *CheckStatus) {
	if status == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = emptyOverallStatus()
	}
	if s.cache.Items == nil {
		s.cache.Items = map[string]CheckStatus{}
	}
	s.cache.Items[string(status.Tool)] = *status
	if status.CheckedAt.After(s.cache.CheckedAt) {
		s.cache.CheckedAt = status.CheckedAt
	}
	recomputeOverallSummary(s.cache)
}

// CheckLatestVersion returns the latest available version for a supported CLI
// tool. Successful results are cached in memory for 24 hours to avoid repeated
// registry or package-manager requests.
func (s *Service) CheckLatestVersion(tool CLITool) (latestVersion string, err error) {
	if !IsValidCLITool(tool) {
		return "", fmt.Errorf("unsupported CLI tool: %s", tool)
	}
	now := time.Now()
	s.mu.RLock()
	if entry, ok := s.versionCache[tool]; ok && strings.TrimSpace(entry.Version) != "" && now.Sub(entry.CheckedAt) < latestVersionCacheTTL {
		s.mu.RUnlock()
		return entry.Version, nil
	}
	s.mu.RUnlock()

	latestVersion, err = s.checkLatestVersion(tool)
	if err != nil {
		return "", err
	}
	latestVersion = strings.TrimSpace(latestVersion)
	if latestVersion == "" {
		return "", fmt.Errorf("latest version for %s was empty", tool)
	}

	s.mu.Lock()
	if s.versionCache == nil {
		s.versionCache = map[CLITool]latestVersionCacheEntry{}
	}
	s.versionCache[tool] = latestVersionCacheEntry{Version: latestVersion, CheckedAt: now}
	s.mu.Unlock()
	return latestVersion, nil
}

func (s *Service) enrichWithLatestVersion(status *CheckStatus) {
	if status == nil || !status.Installed || strings.TrimSpace(status.Version) == "" {
		return
	}
	latestVersion, err := s.CheckLatestVersion(status.Tool)
	if err != nil || strings.TrimSpace(latestVersion) == "" {
		return
	}
	status.LatestVersion = latestVersion
	status.HasUpdate = compareVersionStrings(status.Version, latestVersion) < 0
}

func (s *Service) checkLatestVersion(tool CLITool) (string, error) {
	switch tool {
	case ToolClaudeCode:
		version, err := s.npmPackageVersion("@anthropic-ai/claude-code")
		if err == nil && version != "" {
			return version, nil
		}
		if runtime.GOOS == "windows" {
			if wingetVersion, wingetErr := s.wingetUpgradeVersion("Anthropic.ClaudeCode"); wingetErr == nil && wingetVersion != "" {
				return wingetVersion, nil
			}
		}
		return "", err
	case ToolOpenCode:
		return s.npmPackageVersion("opencode-ai")
	case ToolCodex:
		return s.npmPackageVersion("@openai/codex")
	default:
		return "", fmt.Errorf("unsupported CLI tool: %s", tool)
	}
}

func (s *Service) npmPackageVersion(packageName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), latestVersionTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "npm",
		Args:   []string{"view", packageName, "version"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("npm view %s version: %s", packageName, message)
	}
	version := firstNonEmptyLine(resultText(result))
	if version == "" {
		return "", fmt.Errorf("npm view %s version returned no version", packageName)
	}
	return strings.TrimPrefix(version, "v"), nil
}

func (s *Service) wingetUpgradeVersion(packageID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), latestVersionTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "winget",
		Args:   []string{"upgrade", "--id", packageID, "--accept-source-agreements"},
		Policy: platform.DefaultProcessPolicy(),
	})
	if err != nil {
		message := strings.TrimSpace(resultText(result))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("winget upgrade --id %s: %s", packageID, message)
	}
	return parseWingetLatestVersion(resultText(result), packageID)
}

// Install installs the requested CLI tool.
func (s *Service) Install(tool CLITool) (*InstallResult, error) {
	return s.installOrUpdate(tool, installOperationInstall)
}

// Update updates the requested CLI tool.
func (s *Service) Update(tool CLITool) (*InstallResult, error) {
	return s.installOrUpdate(tool, installOperationUpdate)
}

// GetCachedStatus returns a defensive copy of the last known overall status.
func (s *Service) GetCachedStatus() *OverallStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneOverallStatus(s.cache)
}

// SupportedTools returns the stable checking order for all managed CLI tools.
func SupportedTools() []CLITool {
	return []CLITool{ToolClaudeCode, ToolOpenCode, ToolCodex}
}

// IsValidCLITool reports whether tool is supported by EnvCheck.
func IsValidCLITool(tool CLITool) bool {
	switch tool {
	case ToolClaudeCode, ToolOpenCode, ToolCodex:
		return true
	default:
		return false
	}
}

func emptyOverallStatus() *OverallStatus {
	now := time.Now()
	return &OverallStatus{
		AllOK:     false,
		Items:     map[string]CheckStatus{},
		Issues:    []string{},
		CheckedAt: now,
	}
}

func cloneOverallStatus(status *OverallStatus) *OverallStatus {
	if status == nil {
		return nil
	}
	copyStatus := *status
	if status.Items != nil {
		copyStatus.Items = make(map[string]CheckStatus, len(status.Items))
		for tool, item := range status.Items {
			copyStatus.Items[tool] = item
		}
	}
	if status.Issues != nil {
		copyStatus.Issues = append([]string(nil), status.Issues...)
	}
	return &copyStatus
}

func recomputeOverallSummary(status *OverallStatus) {
	if status == nil {
		return
	}
	status.Issues = status.Issues[:0]
	for _, tool := range SupportedTools() {
		item, ok := status.Items[string(tool)]
		if !ok {
			continue
		}
		if !item.Installed || !item.PATHOk || strings.TrimSpace(item.Error) != "" {
			status.Issues = append(status.Issues, toolIssue(&item))
		}
	}
	status.AllOK = len(status.Issues) == 0 && len(status.Items) == len(SupportedTools())
}

func toolIssue(status *CheckStatus) string {
	if status == nil {
		return "unknown CLI tool status is unavailable"
	}
	if strings.TrimSpace(status.Error) != "" {
		return fmt.Sprintf("%s: %s", status.Tool, status.Error)
	}
	if !status.Installed {
		return fmt.Sprintf("%s: not installed", status.Tool)
	}
	if !status.PATHOk {
		return fmt.Sprintf("%s: executable is not available in PATH", status.Tool)
	}
	return fmt.Sprintf("%s: check reported an issue", status.Tool)
}

func firstNonEmptyLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseWingetLatestVersion(output string, packageID string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.Contains(strings.ToLower(trimmed), strings.ToLower(packageID)) {
			continue
		}
		fields := strings.Fields(trimmed)
		for i := len(fields) - 1; i >= 0; i-- {
			candidate := strings.TrimPrefix(fields[i], "v")
			if codexVersionPattern.MatchString(candidate) {
				return codexVersionPattern.FindString(candidate), nil
			}
		}
	}
	if strings.Contains(strings.ToLower(output), "no installed package found") || strings.Contains(strings.ToLower(output), "no available upgrade") {
		return "", fmt.Errorf("winget did not report an available version for %s", packageID)
	}
	return "", fmt.Errorf("parse winget latest version for %s from output %q", packageID, output)
}

func compareVersionStrings(current string, latest string) int {
	currentVersion := parseComparableVersion(current)
	latestVersion := parseComparableVersion(latest)
	for i := 0; i < 3; i++ {
		if currentVersion.parts[i] < latestVersion.parts[i] {
			return -1
		}
		if currentVersion.parts[i] > latestVersion.parts[i] {
			return 1
		}
	}
	return comparePrerelease(currentVersion.prerelease, latestVersion.prerelease)
}

type comparableVersion struct {
	parts      [3]int
	prerelease string
}

func parseComparableVersion(version string) comparableVersion {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if buildIndex := strings.Index(version, "+"); buildIndex >= 0 {
		version = version[:buildIndex]
	}
	parsed := comparableVersion{}
	if prereleaseIndex := strings.Index(version, "-"); prereleaseIndex >= 0 {
		parsed.prerelease = version[prereleaseIndex+1:]
		version = version[:prereleaseIndex]
	}
	for i, part := range strings.Split(version, ".") {
		if i >= len(parsed.parts) {
			break
		}
		if part == "" {
			continue
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		parsed.parts[i] = number
	}
	return parsed
}

func comparePrerelease(current string, latest string) int {
	if current == "" && latest == "" {
		return 0
	}
	if current == "" {
		return 1
	}
	if latest == "" {
		return -1
	}
	currentIDs := strings.Split(current, ".")
	latestIDs := strings.Split(latest, ".")
	maxLen := len(currentIDs)
	if len(latestIDs) > maxLen {
		maxLen = len(latestIDs)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(currentIDs) {
			return -1
		}
		if i >= len(latestIDs) {
			return 1
		}
		cmp := comparePrereleaseIdentifier(currentIDs[i], latestIDs[i])
		if cmp != 0 {
			return cmp
		}
	}
	return 0
}

func comparePrereleaseIdentifier(current string, latest string) int {
	currentNum, currentErr := strconv.Atoi(current)
	latestNum, latestErr := strconv.Atoi(latest)
	currentIsNum := currentErr == nil
	latestIsNum := latestErr == nil
	if currentIsNum && latestIsNum {
		if currentNum < latestNum {
			return -1
		}
		if currentNum > latestNum {
			return 1
		}
		return 0
	}
	if currentIsNum {
		return -1
	}
	if latestIsNum {
		return 1
	}
	if current < latest {
		return -1
	}
	if current > latest {
		return 1
	}
	return 0
}

func versionParts(version string) []int {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if idx := strings.IndexAny(version, "-+"); idx >= 0 {
		version = version[:idx]
	}
	if version == "" {
		return nil
	}
	parts := strings.Split(version, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			numbers = append(numbers, 0)
			continue
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		numbers = append(numbers, number)
	}
	return numbers
}
