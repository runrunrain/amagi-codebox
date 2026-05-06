package envcheck

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	// CheckClaudeConfig scans Claude Code configuration files and reports
	// whether required configuration items are present.
	CheckClaudeConfig() (*ClaudeConfigStatus, error)

	// FixClaudeConfig writes a single configuration item to the target file.
	// Only missing keys are written; existing keys are never overwritten.
	FixClaudeConfig(req ConfigFixRequest) (*ConfigFixResult, error)

	// CleanClaudeCode removes an existing Claude Code installation.
	// The method parameter specifies which installation to clean (npm or native).
	// After cleaning, it verifies that Claude Code is no longer installed.
	CleanClaudeCode(method InstallMethod) (*InstallResult, error)
}

// Service implements EnvCheckService.
type Service struct {
	mu            sync.RWMutex
	cache         *OverallStatus
	versionCache  map[CLITool]latestVersionCacheEntry
	processRunner platform.ProcessRunner

	// Async operation state
	opMu    sync.Mutex
	current *OperationState
	opSeq   atomic.Int64

	// npmAvailability caches whether npm is resolvable. Populated once per
	// service lifetime to avoid repeated probing during CheckAll.
	npmOnce        sync.Once
	npmAvailable   bool
	npmResolvedErr error // error message when npm is not available
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

// invalidateVersionCache removes the cached latest-version entry for a tool
// so that subsequent calls to CheckLatestVersion fetch a fresh value.
func (s *Service) invalidateVersionCache(tool CLITool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.versionCache, tool)
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
	s.populateCanInstall(status)
	s.enrichWithLatestVersion(status)
	s.cacheToolStatus(status)
	return status, nil
}

// populateCanInstall fills the CanInstall, CanInstallByMethod, InstallBlockedReason,
// and related Issue/Solution fields on the CheckStatus. It probes npm availability
// once and caches the result for the lifetime of the Service to avoid repeated
// lookups across CheckAll calls.
//
// For Claude Code on Windows, CanInstallByMethod reports per-method availability
// so the frontend can enable/disable install buttons based on the selected method
// rather than gating everything on npm.
func (s *Service) populateCanInstall(status *CheckStatus) {
	if status == nil {
		return
	}

	s.npmOnce.Do(func() {
		s.probeNPMAvailability()
	})

	// Compute per-method install availability for Claude Code on Windows.
	if status.Tool == ToolClaudeCode && runtime.GOOS == "windows" {
		status.CanInstallByMethod = map[string]bool{
			"npm":    s.npmAvailable,
			"winget": s.isWingetAvailable(),
			"native": true, // native always allows attempt; accessibility checked at install time
		}
		// CanInstall is true if ANY method is available
		status.CanInstall = s.npmAvailable || status.CanInstallByMethod["winget"] || status.CanInstallByMethod["native"]
	} else {
		status.CanInstallByMethod = map[string]bool{
			"npm": s.npmAvailable,
		}
		status.CanInstall = s.npmAvailable
	}

	if status.CanInstall {
		// For installed tools with errors, offer a reinstall/repair solution.
		if status.Installed && strings.TrimSpace(status.Error) != "" {
			status.Solutions = append(status.Solutions, ResolutionAction{
				Type:        SolutionInstallTool,
				Description: fmt.Sprintf("Reinstall %s to repair the broken installation", displayToolName(status.Tool)),
				Tool:        status.Tool,
				PackageName: npmPackageName(status.Tool),
			})
		}
	} else {
		if s.npmResolvedErr != nil {
			status.InstallBlockedReason = s.npmResolvedErr.Error()
		}
		// Only add npm_not_found issue when the tool is not installed or has errors.
		if !status.Installed || strings.TrimSpace(status.Error) != "" {
			// Determine issue code based on error content
			issueCode := "npm_not_found"
			errMsg := "npm is required but not available"
			if strings.Contains(status.InstallBlockedReason, "node is not in PATH") ||
				strings.Contains(status.InstallBlockedReason, "node: No such file") ||
				strings.Contains(status.InstallBlockedReason, "env: node: No such file") {
				issueCode = "node_missing_for_npm"
				errMsg = "npm is installed but node is not in PATH"
			}
			issue := CheckIssue{
				Severity: SeverityError,
				Code:     issueCode,
				Message:  errMsg,
				Detail:   status.InstallBlockedReason,
				Solutions: []ResolutionAction{
					{
						Type:            SolutionFixPath,
						Description:     "Fix PATH to include node and npm directories",
						RequiresConfirm: true,
						IsPrimary:       true,
					},
					{
						Type:        SolutionInstallNode,
						Description: "Install Node.js to get npm",
					},
				},
			}
			status.Issues = append(status.Issues, issue)
			// Also add to top-level solutions for easy button access
			status.Solutions = append(status.Solutions, ResolutionAction{
				Type:            SolutionFixPath,
				Description:     "Fix PATH to include node/npm",
				RequiresConfirm: true,
				IsPrimary:       true,
			})
			status.Solutions = append(status.Solutions, ResolutionAction{
				Type:        SolutionInstallNode,
				Description: "Install Node.js",
			})
		}
	}
}

// isWingetAvailable checks whether winget is available on this system.
// Unlike verifyWingetHealth, this is a lightweight check that does not produce
// user-facing error messages -- it simply reports availability.
func (s *Service) isWingetAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   "winget",
		Args:   []string{"--version"},
		Env:    s.buildEnhancedEnv(),
		Policy: platform.DefaultProcessPolicy(),
	})
	return err == nil
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
	npmPath := s.resolveNPMPath()
	env := s.buildEnhancedEnv()

	ctx, cancel := context.WithTimeout(context.Background(), latestVersionTimeout)
	defer cancel()

	result, err := s.processRunner.Run(ctx, platform.CommandSpec{
		Path:   npmPath,
		Args:   []string{"view", packageName, "version"},
		Env:    env,
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
// It acquires the global serialization gate so it cannot run concurrently with
// StartInstallTool/StartUpdateTool or another synchronous Install/Update call.
func (s *Service) Install(tool CLITool) (*InstallResult, error) {
	return s.serializedInstallOrUpdate(tool, installOperationInstall)
}

// Update updates the requested CLI tool.
// Same concurrency semantics as Install.
func (s *Service) Update(tool CLITool) (*InstallResult, error) {
	return s.serializedInstallOrUpdate(tool, installOperationUpdate)
}

// serializedInstallOrUpdate acquires the global opMu gate before calling
// installOrUpdate. This ensures synchronous Install/Update calls are serialized
// with each other and with async StartInstallTool/StartUpdateTool, while
// preserving the blocking (synchronous) call semantics for the caller.
func (s *Service) serializedInstallOrUpdate(tool CLITool, operation installOperation) (*InstallResult, error) {
	s.opMu.Lock()
	if s.current != nil && s.current.Status == OperationStatusRunning {
		s.opMu.Unlock()
		return nil, ErrBusy
	}
	// Set a placeholder operation state so async callers see the busy state.
	now := time.Now()
	s.current = &OperationState{
		ID:        fmt.Sprintf("sync-%d", s.opSeq.Add(1)),
		Tool:      tool,
		Kind:      OperationKind(operation),
		Status:    OperationStatusRunning,
		Step:      OperationStepPrecheck,
		Message:   fmt.Sprintf("Starting %s %s...", displayToolName(tool), operation),
		StartedAt: now,
		UpdatedAt: now,
	}
	s.opMu.Unlock()

	result, err := s.installOrUpdate(tool, operation)

	// Best-effort: invalidate version cache and refresh tool status on success
	// so that subsequent GetCachedStatus/GetEnvCheckSnapshot calls reflect the
	// new version rather than a stale snapshot.
	if err == nil && result != nil && result.Success {
		s.invalidateVersionCache(tool)
		_, _ = s.CheckOne(tool)
	}

	// Clear the operation state so the gate is released.
	s.opMu.Lock()
	s.current = nil
	s.opMu.Unlock()

	return result, err
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

// recomputeOverallSummary rebuilds the Issues slice and AllOK flag.
// Tools that are usable by CodeBox (Installed && PATHOk && no error) are
// considered healthy even if SystemPATHOk is false (resolver-only visibility).
// A tool with only info-level issues (e.g. path_not_in_system_path) is not
// treated as a blocking problem for AllOK.
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

// ---------------------------------------------------------------------------
// Async operation management
// ---------------------------------------------------------------------------

// ErrBusy is returned when another install/update operation is already running.
var ErrBusy = fmt.Errorf("another install or update operation is in progress")

// ErrAlreadyRunning is returned when the same tool+kind operation is already running.
var ErrAlreadyRunning = fmt.Errorf("this operation is already running")

// StartInstallTool starts an asynchronous install operation for the given tool.
// It returns immediately with the initial OperationState. The actual work runs in
// a background goroutine that survives frontend page navigation.
// If the same tool+kind is already running, it returns the current state.
// If a different operation is running, it returns ErrBusy.
func (s *Service) StartInstallTool(tool CLITool) (*OperationState, error) {
	return s.startOperation(tool, OperationKindInstall)
}

// StartUpdateTool starts an asynchronous update operation for the given tool.
// Same concurrency semantics as StartInstallTool.
func (s *Service) StartUpdateTool(tool CLITool) (*OperationState, error) {
	return s.startOperation(tool, OperationKindUpdate)
}

// GetOperationState returns the current async operation state, or nil if idle.
func (s *Service) GetOperationState() *OperationState {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return cloneOperationState(s.current)
}

// GetEnvCheckSnapshot returns a combined snapshot of the current tool statuses
// and any active operation. This is the primary polling endpoint for the frontend.
func (s *Service) GetEnvCheckSnapshot() *EnvCheckSnapshot {
	snapshot := &EnvCheckSnapshot{}
	snapshot.Status = s.GetCachedStatus()
	snapshot.Operation = s.GetOperationState()
	return snapshot
}

// EnvCheckSnapshot combines cached tool status with the current async operation.
type EnvCheckSnapshot struct {
	Status    *OverallStatus  `json:"status"`
	Operation *OperationState `json:"operation"`
}

// startOperation is the internal entry point for async install/update.
func (s *Service) startOperation(tool CLITool, kind OperationKind) (*OperationState, error) {
	if !IsValidCLITool(tool) {
		return nil, fmt.Errorf("unsupported CLI tool: %s", tool)
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	// If same tool+kind already running, return current state
	if s.current != nil && s.current.Status == OperationStatusRunning &&
		s.current.Tool == tool && s.current.Kind == kind {
		return cloneOperationState(s.current), nil
	}

	// If a different operation is running, reject
	if s.current != nil && s.current.Status == OperationStatusRunning {
		return nil, ErrBusy
	}

	// Create new operation
	now := time.Now()
	opID := fmt.Sprintf("op-%d", s.opSeq.Add(1))
	op := &OperationState{
		ID:        opID,
		Tool:      tool,
		Kind:      kind,
		Status:    OperationStatusRunning,
		Step:      OperationStepPrecheck,
		Message:   fmt.Sprintf("Starting %s %s...", displayToolName(tool), kind),
		Progress:  0,
		StartedAt: now,
		UpdatedAt: now,
	}
	s.current = op

	// Launch background goroutine with context.Background so it survives
	// frontend page navigation. The goroutine updates s.current in place
	// under opMu.
	go s.runOperation(op)

	return cloneOperationState(op), nil
}

// runOperation executes the full install/update lifecycle in a background goroutine.
func (s *Service) runOperation(op *OperationState) {
	// Build a progress reporter that updates the operation state in real-time.
	// The monotonicReporter wrapper guarantees progress never decreases at the
	// callback level, and the inner closure also clamps as defense-in-depth.
	rawReporter := progressReporter(func(step OperationStep, message string, progress int) {
		s.opMu.Lock()
		defer s.opMu.Unlock()
		if s.current == nil || s.current.Status != OperationStatusRunning {
			return
		}
		// Progress must be monotonically non-decreasing.
		if progress < s.current.Progress {
			progress = s.current.Progress
		}
		s.current.Step = step
		s.current.Message = message
		s.current.Progress = progress
		s.current.UpdatedAt = time.Now()
	})
	reporter := monotonicReporter(rawReporter)

	// Run the install/update logic with progress reporting.
	result, err := s.installOrUpdateWithProgress(op.Tool, installOperation(op.Kind), reporter, ClaudeInstallAuto)

	// Best-effort: invalidate version cache so latestVersion is re-fetched.
	s.invalidateVersionCache(op.Tool)

	// Best-effort: refresh the tool cache so the snapshot reflects the new state.
	// Do this before taking opMu so the final state includes the refreshed cache.
	var cacheRefreshed bool
	if err == nil && result != nil && result.Success {
		reporter.report(OperationStepRefreshCache, fmt.Sprintf("Refreshing %s status...", displayToolName(op.Tool)), progressRefresh)
		_, cacheErr := s.CheckOne(op.Tool)
		cacheRefreshed = cacheErr == nil
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	now := time.Now()
	op.UpdatedAt = now
	op.FinishedAt = &now
	op.Step = OperationStepCompleted
	op.Progress = 100
	op.CacheRefreshed = cacheRefreshed

	if err != nil || result == nil || !result.Success {
		// Determine whether this was a timeout rather than a generic failure.
		errText := ""
		if result != nil && result.Error != "" {
			errText = result.Error
		} else if err != nil {
			errText = err.Error()
		}
		if strings.Contains(strings.ToLower(errText), "timed out") {
			op.Status = OperationStatusTimeout
		} else {
			op.Status = OperationStatusFailed
		}
		if result != nil {
			op.Result = result
			op.Message = result.Message
			op.Error = result.Error
		} else if err != nil {
			op.Error = err.Error()
			op.Message = fmt.Sprintf("%s %s failed: %s", displayToolName(op.Tool), op.Kind, err.Error())
		}
		return
	}

	op.Status = OperationStatusSucceeded
	op.Result = result
	op.Message = result.Message
}

// cloneOperationState returns a defensive copy of the operation state.
func cloneOperationState(op *OperationState) *OperationState {
	if op == nil {
		return nil
	}
	copy := *op
	if op.Result != nil {
		resultCopy := *op.Result
		copy.Result = &resultCopy
	}
	if op.FinishedAt != nil {
		fa := *op.FinishedAt
		copy.FinishedAt = &fa
	}
	return &copy
}

// ---------------------------------------------------------------------------
// Claude Code configuration and lifecycle management stubs
// ---------------------------------------------------------------------------

// CheckClaudeConfig scans Claude Code configuration files and reports
// whether required configuration items are present.
func (s *Service) CheckClaudeConfig() (*ClaudeConfigStatus, error) {
	return s.checkClaudeConfig()
}

// FixClaudeConfig writes a single configuration item to the target file.
// Only missing keys are written; existing keys are never overwritten.
func (s *Service) FixClaudeConfig(req ConfigFixRequest) (*ConfigFixResult, error) {
	return s.fixClaudeConfig(req)
}

// CleanClaudeCode removes an existing Claude Code installation.
// The method parameter specifies which installation to clean (npm or native).
// After cleaning, it verifies that Claude Code is no longer installed.
func (s *Service) CleanClaudeCode(method InstallMethod) (*InstallResult, error) {
	return s.cleanClaudeCode(method)
}

// InstallClaudeCodeWithMethod installs Claude Code using a specific method.
// The method is passed as a parameter to the install pipeline, avoiding
// shared mutable state and ensuring thread safety.
func (s *Service) InstallClaudeCodeWithMethod(method ClaudeInstallMethod) (*InstallResult, error) {
	return s.serializedInstallOrUpdateWithMethod(method)
}

// serializedInstallOrUpdateWithMethod acquires the global opMu gate and installs
// Claude Code with a specific method. This ensures the operation is serialized
// with async StartInstallTool/StartUpdateTool and other synchronous calls.
func (s *Service) serializedInstallOrUpdateWithMethod(method ClaudeInstallMethod) (*InstallResult, error) {
	// Validate method BEFORE acquiring the lock and setting operation state,
	// so that an unsupported method does not leave a stale running state.
	var targetMethod InstallMethod
	switch method {
	case ClaudeInstallNPM:
		targetMethod = InstallMethodNPM
	case ClaudeInstallNative:
		targetMethod = InstallMethodNative
	case ClaudeInstallWinget:
		targetMethod = InstallMethodWinget
	default:
		return nil, fmt.Errorf("unsupported method: %s", method)
	}

	s.opMu.Lock()
	if s.current != nil && s.current.Status == OperationStatusRunning {
		s.opMu.Unlock()
		return nil, ErrBusy
	}
	now := time.Now()
	s.current = &OperationState{
		ID:        fmt.Sprintf("sync-method-%d", s.opSeq.Add(1)),
		Tool:      ToolClaudeCode,
		Kind:      OperationKindInstall,
		Status:    OperationStatusRunning,
		Step:      OperationStepPrecheck,
		Message:   fmt.Sprintf("Starting Claude Code install via %s...", method),
		StartedAt: now,
		UpdatedAt: now,
	}
	s.opMu.Unlock()

	// Pre-install: detect and clean conflicting versions from different channels.
	conflictResult, conflictErr := s.ensureNoConflictInstall(targetMethod)
	if conflictErr != nil {
		s.opMu.Lock()
		s.current = nil
		s.opMu.Unlock()
		return nil, conflictErr
	}
	if conflictResult != nil && !conflictResult.Success {
		s.opMu.Lock()
		s.current = nil
		s.opMu.Unlock()
		return conflictResult, nil
	}

	result, err := s.installOrUpdateWithProgress(ToolClaudeCode, installOperationInstall, nil, method)

	if err == nil && result != nil && result.Success {
		s.invalidateVersionCache(ToolClaudeCode)
		_, _ = s.CheckOne(ToolClaudeCode)
	}

	s.opMu.Lock()
	s.current = nil
	s.opMu.Unlock()

	return result, err
}
