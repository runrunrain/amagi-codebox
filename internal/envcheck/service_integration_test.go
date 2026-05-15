package envcheck

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// Integration: CheckAll partial tool failure
// ---------------------------------------------------------------------------

// TestCheckAll_PartialToolFailure_ReturnsAllItems verifies that when one tool's
// detection encounters an error, CheckAll still returns an OverallStatus
// containing all three tools with valid data for the successful ones.
func TestCheckAll_PartialToolFailure_ReturnsAllItems(t *testing.T) {
	// Arrange: create temp executables and set PATH
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		// Claude version check fails
		responseFor(claudePath, "", errors.New("claude version command timed out")),
		// OpenCode and Codex succeed
		responseFor(openCodePath, "opencode v1.2.3", nil),
		responseFor(codexPath, "codex-cli 0.87.0", nil),
	)

	// Act
	overall, err := svc.CheckAll()

	// Assert: CheckAll should not return a top-level error
	if err != nil {
		t.Fatalf("CheckAll() error = %v, want nil (partial failure should not propagate error)", err)
	}
	if overall == nil {
		t.Fatal("CheckAll() returned nil OverallStatus")
	}
	if len(overall.Items) != 3 {
		t.Fatalf("overall.Items count = %d, want 3", len(overall.Items))
	}

	// Assert: Claude has error but is present
	claudeStatus := overall.Items[string(ToolClaudeCode)]
	if claudeStatus.Error == "" {
		t.Error("expected Claude Code status to have an error")
	}

	// Assert: OpenCode and Codex are healthy
	ocStatus := overall.Items[string(ToolOpenCode)]
	if !ocStatus.Installed {
		t.Errorf("OpenCode should be installed, got Installed=%v", ocStatus.Installed)
	}
	codexStatus := overall.Items[string(ToolCodex)]
	if !codexStatus.Installed {
		t.Errorf("Codex should be installed, got Installed=%v", codexStatus.Installed)
	}

	// Assert: Issues list should contain Claude's error
	if len(overall.Issues) == 0 {
		t.Fatal("expected at least one issue for the failing Claude tool")
	}
}

// TestCheckAll_TwoToolsFail_StillReturnsAllItems verifies that even when
// two out of three tools fail, all items are returned with the surviving
// tool reported correctly.
func TestCheckAll_TwoToolsFail_StillReturnsAllItems(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor(claudePath, "", errors.New("claude failed")),
		responseFor(openCodePath, "", errors.New("opencode failed")),
		responseFor(codexPath, "codex-cli 0.87.0", nil),
	)

	// Act
	overall, err := svc.CheckAll()

	// Assert
	if err != nil {
		t.Fatalf("CheckAll() error = %v, want nil", err)
	}
	if overall == nil {
		t.Fatal("CheckAll() returned nil")
	}
	if len(overall.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(overall.Items))
	}
	if len(overall.Issues) < 2 {
		t.Fatalf("expected at least 2 issues, got %d", len(overall.Issues))
	}

	// Assert: the surviving Codex is installed
	codexStatus := overall.Items[string(ToolCodex)]
	if !codexStatus.Installed {
		t.Error("Codex should be installed even when other tools fail")
	}
}

// ---------------------------------------------------------------------------
// Integration: CheckAll single tool detection failure returns (overall, nil)
// ---------------------------------------------------------------------------

// TestCheckAll_SingleToolError_StatusNotNil_ErrorNil verifies the core fixed
// behavior: when a single tool's detection fails but returns a non-nil status
// (with error in the status.Error field), CheckAll returns (overall, nil).
// This is the "repair after fix" contract: partial failures never propagate
// as a top-level error as long as a status was produced.
func TestCheckAll_SingleToolError_StatusNotNil_ErrorNil(t *testing.T) {
	// Arrange: one tool broken, two healthy
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor(claudePath, "", errors.New("version check failed")),
		responseFor(openCodePath, "opencode v1.2.3", nil),
		responseFor(codexPath, "codex-cli 0.87.0", nil),
	)

	// Act
	overall, err := svc.CheckAll()

	// Assert: error is nil because status was non-nil for the failing tool
	if err != nil {
		t.Fatalf("CheckAll() error = %v, want nil (status was non-nil for failing tool)", err)
	}
	if overall == nil {
		t.Fatal("CheckAll() returned nil OverallStatus")
	}

	// Assert: failing tool is present with error info
	claudeStatus := overall.Items[string(ToolClaudeCode)]
	if claudeStatus.Tool != ToolClaudeCode {
		t.Errorf("Claude tool = %q, want %q", claudeStatus.Tool, ToolClaudeCode)
	}
	if claudeStatus.Error == "" {
		t.Error("expected Claude status to have error message")
	}
}

// ---------------------------------------------------------------------------
// Integration: GetCachedStatus correctness
// ---------------------------------------------------------------------------

// TestGetCachedStatus_ReflectsLatestCheckAll verifies that the cache is
// replaced after each CheckAll call and reflects the latest results.
func TestGetCachedStatus_ReflectsLatestCheckAll(t *testing.T) {
	// Arrange: first check with all tools succeeding
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor(claudePath, "Claude Code v1.0.0", nil),
		responseFor(openCodePath, "opencode v1.0.0", nil),
		responseFor(codexPath, "codex-cli 1.0.0", nil),
	)

	// Act: first check
	overall1, _ := svc.CheckAll()
	cached1 := svc.GetCachedStatus()

	// Assert: cache matches first check
	if cached1.AllOK != overall1.AllOK {
		t.Errorf("cache AllOK = %v, want %v", cached1.AllOK, overall1.AllOK)
	}
	if len(cached1.Items) != len(overall1.Items) {
		t.Errorf("cache Items count = %d, want %d", len(cached1.Items), len(overall1.Items))
	}

	// Act: second check with different results (Claude broken)
	svc2 := newTestService(
		responseFor(claudePath, "", errors.New("broken after update")),
		responseFor(openCodePath, "opencode v2.0.0", nil),
		responseFor(codexPath, "codex-cli 2.0.0", nil),
	)
	// Replace the runner on the same service to preserve state
	svc.processRunner = svc2.processRunner

	_, _ = svc.CheckAll()
	cached2 := svc.GetCachedStatus()

	// Assert: cache reflects latest check, not the first
	claudeCached := cached2.Items[string(ToolClaudeCode)]
	if claudeCached.Error == "" {
		t.Error("cached Claude should have error from second check")
	}
}

// TestGetCachedStatus_UpdatedAfterCheckOne verifies that calling CheckOne
// for a single tool updates the cache for that tool without removing others.
func TestGetCachedStatus_UpdatedAfterCheckOne(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor(claudePath, "Claude Code v1.0.0", nil),
		responseFor(openCodePath, "opencode v1.0.0", nil),
		responseFor(codexPath, "codex-cli 1.0.0", nil),
	)

	// Act: run full check first
	svc.CheckAll()

	// Act: update just Claude with a new version via CheckOne
	svc.versionCache = map[CLITool]latestVersionCacheEntry{} // clear version cache
	svc2 := newTestService(
		responseFor(claudePath, "Claude Code v2.0.0", nil),
	)
	svc.processRunner = svc2.processRunner
	svc.CheckOne(ToolClaudeCode)

	// Assert: Claude updated in cache
	cached := svc.GetCachedStatus()
	claudeStatus := cached.Items[string(ToolClaudeCode)]
	if claudeStatus.Version != "2.0.0" {
		t.Errorf("Claude version after CheckOne = %q, want %q", claudeStatus.Version, "2.0.0")
	}

	// Assert: other tools still present from the full CheckAll
	if _, ok := cached.Items[string(ToolOpenCode)]; !ok {
		t.Error("OpenCode should still be in cache after CheckOne on Claude")
	}
	if _, ok := cached.Items[string(ToolCodex)]; !ok {
		t.Error("Codex should still be in cache after CheckOne on Claude")
	}
}

// TestGetCachedStatus_MultipleCheckOneCallsPreservesAllItems verifies
// that sequential CheckOne calls for different tools accumulate in the cache.
func TestGetCachedStatus_MultipleCheckOneCallsPreservesAllItems(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor(claudePath, "Claude Code v1.0.0", nil),
		responseFor(openCodePath, "opencode v1.0.0", nil),
		responseFor(codexPath, "codex-cli 1.0.0", nil),
	)

	// Act: call CheckOne for each tool individually (no CheckAll)
	svc.CheckOne(ToolClaudeCode)
	svc.CheckOne(ToolOpenCode)
	svc.CheckOne(ToolCodex)

	// Assert: all three tools present in cache
	cached := svc.GetCachedStatus()
	for _, tool := range SupportedTools() {
		if _, ok := cached.Items[string(tool)]; !ok {
			t.Errorf("cache missing %q after individual CheckOne calls", tool)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration: Concurrent CheckAll safety
// ---------------------------------------------------------------------------

// TestCheckAll_ConcurrentSafety verifies that calling CheckAll from multiple
// goroutines concurrently does not cause data races or panics. This exercises
// both the write path (CheckAll updates cache) and the read path
// (GetCachedStatus reads cache) under contention.
func TestCheckAll_ConcurrentSafety(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor(claudePath, "Claude Code v1.0.0", nil),
		responseFor(openCodePath, "opencode v1.0.0", nil),
		responseFor(codexPath, "codex-cli 1.0.0", nil),
	)

	// Act: concurrent CheckAll + GetCachedStatus
	const goroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*2)

	for i := 0; i < goroutines; i++ {
		wg.Add(2)
		// Writer goroutine
		go func() {
			defer wg.Done()
			overall, err := svc.CheckAll()
			if overall == nil && err == nil {
				errCh <- errors.New("CheckAll returned (nil, nil)")
			}
		}()
		// Reader goroutine
		go func() {
			defer wg.Done()
			cached := svc.GetCachedStatus()
			if cached == nil {
				errCh <- errors.New("GetCachedStatus returned nil during concurrent CheckAll")
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Integration: Install/Update repair broken installation
// ---------------------------------------------------------------------------

// sequentialRunner returns responses in order, one per Run call.
// Thread-safe via mutex. When responses are exhausted, returns a default
// success result.
type sequentialRunner struct {
	responses []seqResponse
	mu        sync.Mutex
	next      int
}

type seqResponse struct {
	stdout string
	stderr string
	err    error
}

func (r *sequentialRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	return r.RunWithSpec(spec)
}

func (r *sequentialRunner) RunWithSpec(spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.next
	if sequentialRunnerShouldBypassOpenCodeFallback(spec, r.peek(idx)) {
		return &platform.ProcessResult{}, errors.New("opencode fallback probe not configured")
	}
	r.next++
	if idx >= len(r.responses) {
		return &platform.ProcessResult{}, nil
	}
	resp := r.responses[idx]
	return &platform.ProcessResult{Stdout: resp.stdout, Stderr: resp.stderr}, resp.err
}

func (r *sequentialRunner) peek(idx int) *seqResponse {
	if idx < 0 || idx >= len(r.responses) {
		return nil
	}
	return &r.responses[idx]
}

func sequentialRunnerShouldBypassOpenCodeFallback(spec platform.CommandSpec, next *seqResponse) bool {
	if !isNPMPath(strings.ToLower(spec.Path)) || len(spec.Args) < 2 {
		return false
	}
	if len(spec.Args) >= 4 && spec.Args[0] == "list" && spec.Args[1] == "-g" && isOpenCodeFallbackPackage(spec.Args[2]) {
		if next == nil {
			return true
		}
		return !strings.Contains(next.stdout, "opencode-ai@") &&
			!strings.Contains(next.stdout, "opencode@") &&
			!strings.Contains(next.stdout, `"name"`)
	}
	if len(spec.Args) >= 2 && spec.Args[0] == "root" && spec.Args[1] == "-g" {
		if next == nil {
			return true
		}
		trimmed := strings.TrimSpace(next.stdout)
		return trimmed == "" || !(strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, `:\`) || strings.Contains(trimmed, "node_modules"))
	}
	return false
}

func isOpenCodeFallbackPackage(pkg string) bool {
	for _, candidate := range openCodeFallbackPackages {
		if pkg == candidate {
			return true
		}
	}
	return false
}

func (r *sequentialRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// TestInstall_RepairsBrokenInstallation verifies that Install proceeds when
// the pre-check detects a broken tool (found in PATH but version command
// fails) and successfully repairs it by running the install command.
func TestInstall_RepairsBrokenInstallation(t *testing.T) {
	// Arrange: create a discoverable opencode executable
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	// Sequential responses for the multi-step install flow:
	//   Call 1: pre-check opencode --version (fails = broken install)
	//   Call 2: ensureNPMAvailable npm --version (succeeds)
	//   Call 3: install command npm install -g (succeeds)
	//   Call 4: verify opencode --version (succeeds = repaired)
	//   Call 5: verify enrichment npm view opencode-ai version (succeeds)
	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("version command crashed")}, // pre-check
		{stdout: "10.0.0", err: nil},                             // npm available
		{stdout: "added 1 package", err: nil},                    // install
		{stdout: "opencode v2.0.0", err: nil},                    // verify version
		{stdout: "2.0.0", err: nil},                              // latest version
	}}
	svc := NewServiceWithRunner(runner)

	// Act
	result, err := svc.Install(ToolOpenCode)

	// Assert: install should succeed (repair)
	if err != nil {
		t.Fatalf("Install() error = %v, want nil (repair should succeed)", err)
	}
	if result == nil {
		t.Fatal("Install() returned nil result")
	}
	if !result.Success {
		t.Errorf("Install result.Success = false, want true; Message: %s, Error: %s",
			result.Message, result.Error)
	}
	if result.Version != "2.0.0" {
		t.Errorf("Install result.Version = %q, want %q", result.Version, "2.0.0")
	}
}

// TestUpdate_RepairsBrokenInstallation verifies that Update proceeds when
// the pre-check finds a broken tool and successfully repairs it.
func TestUpdate_RepairsBrokenInstallation(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("segfault on version check")}, // pre-check broken
		{stdout: "10.0.0", err: nil},                               // npm available
		{stdout: "updated 1 package", err: nil},                    // update command
		{stdout: "opencode v3.0.0", err: nil},                      // verify version
		{stdout: "3.0.0", err: nil},                                // latest version
	}}
	svc := NewServiceWithRunner(runner)

	// Act
	result, err := svc.Update(ToolOpenCode)

	// Assert
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("Update() returned nil result")
	}
	if !result.Success {
		t.Errorf("Update result.Success = false, want true; Message: %s, Error: %s",
			result.Message, result.Error)
	}
}

// TestInstall_PrecheckFails_StatusNil_Aborts verifies that Install aborts
// cleanly when the pre-check returns both nil status and an error (e.g.,
// unsupported tool or tool completely absent from PATH with no fallback).
func TestInstall_PrecheckFails_StatusNil_Aborts(t *testing.T) {
	// Arrange: empty PATH so no tool is discoverable
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)

	svc := newTestService() // no mock responses => commands fail

	// Act: try to install opencode which is not in PATH
	result, err := svc.Install(ToolOpenCode)

	// Assert: should fail because no executable found
	if err == nil {
		t.Fatal("expected error when tool not found and pre-check fails completely")
	}
	if result == nil {
		t.Fatal("expected non-nil InstallResult even on failure")
	}
	if result.Success {
		t.Error("Install should fail when precheck returns nil status")
	}
}

// TestInstall_AlreadyInstalledAndHealthy_SkipsInstall verifies that Install
// returns success without running install commands when the tool is already
// healthy and up to date.
func TestInstall_AlreadyInstalledAndHealthy_SkipsInstall(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		// Pre-check succeeds with current version
		{stdout: "opencode v1.0.0", err: nil},
		// populateCanInstall: npm --version probe
		{stdout: "10.0.0", err: nil},
		// Latest version matches (no update needed)
		{stdout: "1.0.0", err: nil},
		// Post-install refresh: opencode --version
		{stdout: "opencode v1.0.0", err: nil},
	}}
	svc := NewServiceWithRunner(runner)

	// Act
	result, err := svc.Install(ToolOpenCode)

	// Assert
	if err != nil {
		t.Fatalf("Install() error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("Install() returned nil result")
	}
	if !result.Success {
		t.Errorf("result.Success = false, want true; Message: %s", result.Message)
	}
	// Runner calls: pre-check version, npm probe, enrichment, post-refresh version
	runner.mu.Lock()
	callCount := runner.next
	runner.mu.Unlock()
	if callCount > 5 {
		t.Errorf("expected at most 5 runner calls, got %d", callCount)
	}
}

// ---------------------------------------------------------------------------
// Integration: CheckLatestVersion cache TTL
// ---------------------------------------------------------------------------

// TestCheckLatestVersion_CacheTTL verifies that cached version entries
// expire after the 24-hour TTL and a fresh check is performed.
func TestCheckLatestVersion_CacheTTL(t *testing.T) {
	// Arrange: seed cache with a stale entry (25 hours old)
	svc := newTestService(responseFor("npm", "fresh-version", nil))
	svc.mu.Lock()
	svc.versionCache[ToolOpenCode] = latestVersionCacheEntry{
		Version:   "stale-version",
		CheckedAt: time.Now().Add(-25 * time.Hour), // beyond 24h TTL
	}
	svc.mu.Unlock()

	// Act: should bypass cache and call npm
	version, err := svc.CheckLatestVersion(ToolOpenCode)
	if err != nil {
		t.Fatalf("CheckLatestVersion error: %v", err)
	}

	// Assert: should get fresh version from mock, not the stale cached one
	if version == "stale-version" {
		t.Error("should have refreshed expired cache entry, got stale version")
	}
	if version != "fresh-version" {
		t.Errorf("version = %q, want %q", version, "fresh-version")
	}
}

// TestCheckLatestVersion_CacheHit_WithinTTL verifies that a non-expired
// cache entry is returned without calling the runner.
func TestCheckLatestVersion_CacheHit_WithinTTL(t *testing.T) {
	// Arrange: seed cache with a fresh entry (1 hour old)
	svc := newTestService() // no responses => would fail on fresh call
	svc.mu.Lock()
	svc.versionCache[ToolClaudeCode] = latestVersionCacheEntry{
		Version:   "cached-claude-version",
		CheckedAt: time.Now().Add(-1 * time.Hour), // within 24h TTL
	}
	svc.mu.Unlock()

	// Act: should return cached value without calling the runner
	version, err := svc.CheckLatestVersion(ToolClaudeCode)
	if err != nil {
		t.Fatalf("CheckLatestVersion error: %v", err)
	}
	if version != "cached-claude-version" {
		t.Errorf("version = %q, want %q (from cache)", version, "cached-claude-version")
	}
}

// ---------------------------------------------------------------------------
// Integration: installOrUpdate doesn't abort on pre-check failure with status
// ---------------------------------------------------------------------------

// TestInstall_PrecheckReturnsStatusWithError_ProceedsToInstall verifies the
// specific fix: when CheckOne returns a non-nil status with an error (broken
// install detected), installOrUpdate continues to the install phase rather
// than aborting.
func TestInstall_PrecheckReturnsStatusWithError_ProceedsToInstall(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		// Pre-check: tool found but version check error
		{stdout: "", err: errors.New("corrupted binary")}, // pre-check
		{stdout: "10.0.0", err: nil},                      // npm available
		{stdout: "installed", err: nil},                   // install
		{stdout: "opencode v1.5.0", err: nil},             // verify
		{stdout: "1.5.0", err: nil},                       // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	// Act
	result, err := svc.Install(ToolOpenCode)

	// Assert: should NOT have stopped at pre-check
	if err != nil {
		t.Fatalf("Install() error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Errorf("result.Success = false, want true; Message: %s, Error: %s",
			result.Message, result.Error)
	}
	// Verify the install command was actually executed (runner called >= 3 times)
	runner.mu.Lock()
	callCount := runner.next
	runner.mu.Unlock()
	if callCount < 3 {
		t.Errorf("expected at least 3 runner calls (precheck + npm-check + install), got %d", callCount)
	}
}

// TestUpdate_PrecheckReturnsStatusWithError_ProceedsToUpdate verifies that
// Update also continues past a broken pre-check status.
func TestUpdate_PrecheckReturnsStatusWithError_ProceedsToUpdate(t *testing.T) {
	// Arrange
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "", err: errors.New("codex binary corrupted")}, // pre-check
		{stdout: "10.0.0", err: nil},                            // npm available
		{stdout: "updated", err: nil},                           // update command
		{stdout: "codex-cli 0.90.0", err: nil},                  // verify
		{stdout: "0.90.0", err: nil},                            // enrichment
	}}
	svc := NewServiceWithRunner(runner)

	// Act
	result, err := svc.Update(ToolCodex)

	// Assert
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Errorf("result.Success = false, want true; Message: %s, Error: %s",
			result.Message, result.Error)
	}
}

// ---------------------------------------------------------------------------
// Update version unchanged assertion
// ---------------------------------------------------------------------------

// TestUpdate_VersionUnchanged_ReturnsFailure verifies that when an update
// command exits successfully but the installed version does not change,
// installOrUpdate reports failure instead of a false success.
func TestUpdate_VersionUnchanged_ReturnsFailure(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil}, // pre-check version
		{stdout: "2.0.0", err: nil},           // latest version (enrichment)
		{stdout: "10.0.0", err: nil},          // npm available
		{stdout: "updated", err: nil},         // install command succeeds
		{stdout: "opencode v1.0.0", err: nil}, // post-check: SAME version!
		// post-enrichment hits version cache (no runner call)
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Update(ToolOpenCode)

	if err == nil {
		t.Fatal("expected error when version unchanged after update")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected Success=false when version unchanged after update")
	}
	if !strings.Contains(result.Error, "version unchanged") && !strings.Contains(result.Error, "version was unchanged") {
		t.Errorf("error should mention version unchanged, got: %s", result.Error)
	}
}

// ---------------------------------------------------------------------------
// Update success invalidates version cache
// ---------------------------------------------------------------------------

// TestUpdate_Success_InvalidatesVersionCache verifies that a successful update
// invalidates the versionCache so that subsequent snapshot fetches reflect the
// new latest-version state rather than a stale cached value.
func TestUpdate_Success_InvalidatesVersionCache(t *testing.T) {
	tmpDir := t.TempDir()
	_ = writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &sequentialRunner{responses: []seqResponse{
		{stdout: "opencode v1.0.0", err: nil}, // pre-check version
		// enrichment uses pre-seeded cache hit (9.9.9) -- no runner call
		{stdout: "10.0.0", err: nil},          // npm available
		{stdout: "updated", err: nil},         // install command
		{stdout: "opencode v2.0.0", err: nil}, // post-check: version changed!
		// post-enrichment uses cache hit (9.9.9) -- no runner call
		// serializedInstallOrUpdate invalidates cache and calls CheckOne:
		{stdout: "opencode v2.0.0", err: nil}, // re-check version
		{stdout: "2.0.0", err: nil},           // re-enrichment (cache was invalidated)
	}}
	svc := NewServiceWithRunner(runner)

	// Pre-seed version cache with a value that makes HasUpdate=true.
	staleTime := time.Now().Add(-2 * time.Hour)
	svc.mu.Lock()
	svc.versionCache[ToolOpenCode] = latestVersionCacheEntry{
		Version:   "9.9.9",
		CheckedAt: staleTime,
	}
	svc.mu.Unlock()

	result, err := svc.Update(ToolOpenCode)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Update failed: %s, %s", result.Message, result.Error)
	}

	// Verify versionCache was invalidated and re-populated (not the stale value).
	svc.mu.RLock()
	entry, exists := svc.versionCache[ToolOpenCode]
	svc.mu.RUnlock()

	if !exists {
		t.Fatal("versionCache should exist after successful update (re-populated by CheckOne)")
	}
	if entry.Version == "9.9.9" {
		t.Error("versionCache was not invalidated -- still has the pre-seeded stale value")
	}
	if !entry.CheckedAt.After(staleTime) {
		t.Error("versionCache CheckedAt should be newer than the stale entry")
	}
}
