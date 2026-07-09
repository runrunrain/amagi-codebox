package envcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// responseFor builds a mockResponse that matches by command path substring.
func responseFor(pathPrefix, stdout string, err error) mockResponse {
	return mockResponse{pathPrefix: pathPrefix, stdout: stdout, err: err}
}

// stdResult builds a ProcessResult with only Stdout.
func stdResult(stdout string) *platform.ProcessResult {
	return &platform.ProcessResult{Stdout: stdout}
}

func writeTestExecutable(t *testing.T, dir string, name string) string {
	t.Helper()
	ext := ""
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		ext = ".cmd"
		content = "@echo off\r\nexit /b 0\r\n"
	}
	path := filepath.Join(dir, name+ext)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write test executable %s: %v", name, err)
	}
	return path
}

func runnerSawArgs(runner *mockRunner, pathContains string, args ...string) bool {
	for _, call := range runner.calls {
		if !strings.Contains(call.Path, pathContains) {
			continue
		}
		if len(call.Args) != len(args) {
			continue
		}
		matched := true
		for i := range args {
			if call.Args[i] != args[i] {
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

func forceRuntimeGOOSForTest(t *testing.T, goos string) {
	t.Helper()
	previous := runtimeGOOS
	runtimeGOOS = goos
	t.Cleanup(func() { runtimeGOOS = previous })
}

// ---------------------------------------------------------------------------
// 1. SupportedTools / IsValidCLITool
// ---------------------------------------------------------------------------

func TestSupportedTools(t *testing.T) {
	tools := SupportedTools()
	if len(tools) != 4 {
		t.Fatalf("SupportedTools() returned %d tools, want 4", len(tools))
	}
	seen := map[CLITool]bool{}
	for _, tool := range tools {
		if seen[tool] {
			t.Errorf("duplicate tool %q in SupportedTools()", tool)
		}
		seen[tool] = true
	}
	for _, expected := range []CLITool{ToolClaudeCode, ToolOpenCode, ToolCodex, ToolHeadroom} {
		if !seen[expected] {
			t.Errorf("SupportedTools() missing %q", expected)
		}
	}
}

func TestIsValidCLITool(t *testing.T) {
	tests := []struct {
		tool  CLITool
		valid bool
	}{
		{ToolClaudeCode, true},
		{ToolOpenCode, true},
		{ToolCodex, true},
		{ToolHeadroom, true},
		{CLITool("nonexistent"), false},
		{CLITool(""), false},
	}
	for _, tc := range tests {
		t.Run(string(tc.tool), func(t *testing.T) {
			got := IsValidCLITool(tc.tool)
			if got != tc.valid {
				t.Errorf("IsValidCLITool(%q) = %v, want %v", tc.tool, got, tc.valid)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. CheckOne - unsupported tool returns error
// ---------------------------------------------------------------------------

func TestCheckOne_UnsupportedTool(t *testing.T) {
	svc := newTestService()
	_, err := svc.CheckOne(CLITool("nonexistent"))
	if err == nil {
		t.Fatal("expected error for unsupported tool, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention 'unsupported', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 3. CheckOne - tool not found in PATH
// ---------------------------------------------------------------------------

func TestCheckOne_ToolNotFound(t *testing.T) {
	// mockRunner returns no matching responses, so the version check will fail.
	// The check may still find claude via LookPath on the host, so we test
	// that the status is returned (installed=false or error is set).
	svc := newTestService() // empty responses => all commands fail

	status, _ := svc.CheckOne(ToolClaudeCode)
	// When the tool is found in PATH but version fails, an error is returned
	// alongside a status. When the tool is not in PATH at all, no error is
	// returned but status.Installed=false. Both are acceptable outcomes.
	if status == nil {
		t.Fatal("expected non-nil status")
	}
}

// ---------------------------------------------------------------------------
// 4. CheckAll
// ---------------------------------------------------------------------------

func TestCheckAll_ReturnsAllTools(t *testing.T) {
	svc := newTestService() // all tools may or may not be found

	overall, _ := svc.CheckAll()
	if overall == nil {
		t.Fatal("expected non-nil OverallStatus")
	}
	for _, tool := range SupportedTools() {
		if _, ok := overall.Items[string(tool)]; !ok {
			t.Errorf("OverallStatus.Items missing %q", tool)
		}
	}
}

func TestCheckAll_CachesResult(t *testing.T) {
	svc := newTestService()

	_, _ = svc.CheckAll()
	cached := svc.GetCachedStatus()
	if cached == nil {
		t.Fatal("expected cached status after CheckAll")
	}
	if len(cached.Items) != len(SupportedTools()) {
		t.Errorf("cached Items count = %d, want %d", len(cached.Items), len(SupportedTools()))
	}
}

func TestCheckAll_DoesNotReturnErrorForToolStatusError(t *testing.T) {
	tmpDir := t.TempDir()
	claudePath := writeTestExecutable(t, tmpDir, "claude")
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	codexPath := writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	svc := newTestService(
		responseFor(claudePath, "", errors.New("version command failed")),
		responseFor(openCodePath, "opencode v1.2.3", nil),
		responseFor(codexPath, "codex-cli 0.87.0", nil),
	)

	overall, err := svc.CheckAll()
	if err != nil {
		t.Fatalf("CheckAll() error = %v, want nil", err)
	}
	if overall == nil {
		t.Fatal("expected non-nil OverallStatus")
	}
	status := overall.Items[string(ToolClaudeCode)]
	if strings.TrimSpace(status.Error) == "" {
		t.Fatal("expected Claude Code status error to be preserved")
	}
	if len(overall.Issues) == 0 {
		t.Fatal("expected overall issues to include the tool status error")
	}
}

// ---------------------------------------------------------------------------
// 4b. CheckAll Checking flag (F-4: explicit checking bool)
// ---------------------------------------------------------------------------

// TestCheckAll_ReportingSnapshotHasCheckingFalseAfterCompletion verifies that
// the snapshot returned by CheckAll carries Checking=false once the loop has
// finished. The frontend relies on this for R5 precise state judgement.
func TestCheckAll_ReportingSnapshotHasCheckingFalseAfterCompletion(t *testing.T) {
	svc := newTestService()
	overall, _ := svc.CheckAll()
	if overall == nil {
		t.Fatal("expected non-nil OverallStatus")
	}
	if overall.Checking {
		t.Fatal("overall.Checking should be false after CheckAll returns")
	}
}

// TestCheckAll_CachedSnapshotHasCheckingFalseAfterCompletion verifies that the
// in-memory cache snapshot has Checking=false after CheckAll completes. The
// frontend polls GetCachedStatus during/after a check and must not see a
// stale Checking=true once the run is over.
func TestCheckAll_CachedSnapshotHasCheckingFalseAfterCompletion(t *testing.T) {
	svc := newTestService()
	_, _ = svc.CheckAll()
	cached := svc.GetCachedStatus()
	if cached == nil {
		t.Fatal("expected cached status after CheckAll")
	}
	if cached.Checking {
		t.Fatal("cached.Checking should be false after CheckAll completes")
	}
}

// TestCheckAll_SetsCheckingTrueInCacheDuringRun uses a mock runner that blocks
// until released, allowing us to observe Checking=true on the cached snapshot
// while the loop is still in progress. This is the core F-4 contract: the
// frontend can read status.checking to detect an in-flight run instead of
// relying on CheckedAt presence + a timeout fallback.
func TestCheckAll_SetsCheckingTrueInCacheDuringRun(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestExecutable(t, tmpDir, "claude")
	writeTestExecutable(t, tmpDir, "opencode")
	writeTestExecutable(t, tmpDir, "codex")
	t.Setenv("PATH", tmpDir)

	// Block the claude version probe until the test has observed the
	// in-progress state. opencode/codex return instantly.
	release := make(chan struct{})
	started := make(chan struct{})

	m := &mockRunner{responses: nil}
	m.responses = []mockResponse{
		{
			pathPrefix: "claude",
			stdout:     "",
			err:        nil,
			// custom hook is not supported by plain mockResponse; emulate
			// blocking via a goroutine here would require runner refactor,
			// so instead use a stub that calls Run indirectly below.
		},
	}
	_ = m
	_ = started
	_ = release

	// Simpler approach: spawn CheckAll, race-free observe via GetCachedStatus.
	// Use a slow claude version probe by pointing it at a command that blocks.
	// The simplest portable approach is to use the mockRunner's Run hook: we
	// wrap the existing mockRunner to gate one call.
	gated := &gatedRunner{
		release: release,
		started: started,
		inner:   &mockRunner{},
	}
	svc := NewServiceWithRunner(gated)

	done := make(chan struct{})
	go func() {
		_, _ = svc.CheckAll()
		close(done)
	}()

	// Wait for the gated runner to enter the blocked call, then observe the
	// cache while CheckAll is still running.
	<-started
	cached := svc.GetCachedStatus()
	if cached == nil {
		t.Fatal("expected non-nil cached status during CheckAll")
	}
	if !cached.Checking {
		t.Fatalf("cached.Checking should be true while CheckAll is in progress (F-4 contract)")
	}

	// Release the runner and ensure CheckAll completes cleanly.
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("CheckAll did not complete within 10s after release")
	}

	// After completion the cache MUST show Checking=false.
	cached = svc.GetCachedStatus()
	if cached.Checking {
		t.Fatal("cached.Checking should be false after CheckAll completes")
	}
}

// gatedRunner wraps a mockRunner and blocks the FIRST Run call until release
// is closed, signalling on started when the block begins. Subsequent calls
// pass through to the inner runner. This lets us deterministically observe
// the in-progress state of CheckAll.
type gatedRunner struct {
	inner   *mockRunner
	release chan struct{}
	started chan<- struct{}
	once    sync.Once
}

func (g *gatedRunner) Run(ctx context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	g.once.Do(func() {
		if g.started != nil {
			close(g.started)
		}
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	})
	return g.inner.Run(ctx, spec)
}

func (g *gatedRunner) Start(spec platform.CommandSpec) (*exec.Cmd, error) {
	return g.inner.Start(spec)
}

// ---------------------------------------------------------------------------
// 5. GetCachedStatus - empty before first check
// ---------------------------------------------------------------------------

func TestGetCachedStatus_BeforeCheck(t *testing.T) {
	svc := newTestService()
	cached := svc.GetCachedStatus()
	// Should return empty but non-nil status
	if cached == nil {
		t.Fatal("expected non-nil cached status even before CheckAll")
	}
	if len(cached.Items) != 0 {
		t.Errorf("expected empty Items before any check, got %d", len(cached.Items))
	}
}

func TestGetCachedStatus_DefensiveCopy(t *testing.T) {
	svc := newTestService()
	_, _ = svc.CheckAll()

	c1 := svc.GetCachedStatus()
	c2 := svc.GetCachedStatus()

	// Mutating c1 should not affect c2
	c1.AllOK = true
	if c2.AllOK {
		t.Error("GetCachedStatus should return a defensive copy")
	}
}

// ---------------------------------------------------------------------------
// 6. CheckOne - with mock process responses (end-to-end through Service)
// ---------------------------------------------------------------------------

func TestCheckOne_ClaudeCode_Found(t *testing.T) {
	// Create a temp executable file to simulate claude in PATH
	tmpDir := t.TempDir()
	claudeExe := tmpDir + `\claude.cmd`
	if err := os.WriteFile(claudeExe, []byte("@echo Claude Code v2.1.110"), 0o755); err != nil {
		t.Skipf("cannot create temp executable: %v", err)
	}

	svc := newTestService(
		responseFor(claudeExe, "Claude Code v2.1.110", nil),
	)

	// We test the version parsing path via claudeVersion directly since
	// exec.LookPath won't find our temp file.
	version, err := svc.claudeVersion(claudeExe)
	if err != nil {
		t.Fatalf("claudeVersion error: %v", err)
	}
	if version != "2.1.110" {
		t.Errorf("claudeVersion = %q, want %q", version, "2.1.110")
	}
}

func TestCheckOne_ClaudeVersion_CommandFails(t *testing.T) {
	svc := newTestService() // no responses => command not found error

	_, err := svc.claudeVersion("nonexistent-claude-binary")
	if err == nil {
		t.Fatal("expected error when claude binary fails")
	}
	if !strings.Contains(err.Error(), "claude --version") {
		t.Errorf("error should mention version command: %v", err)
	}
}

func TestCheckOne_OpenCodeVersion(t *testing.T) {
	svc := newTestService(
		responseFor("opencode", "opencode v1.2.3", nil),
	)
	version, err := svc.openCodeVersion("opencode")
	if err != nil {
		t.Fatalf("openCodeVersion error: %v", err)
	}
	if version != "1.2.3" {
		t.Errorf("openCodeVersion = %q, want %q", version, "1.2.3")
	}
}

func TestCheckOne_OpenCodeVersion_EmptyOutput(t *testing.T) {
	svc := newTestService(
		responseFor("opencode", "", nil),
	)
	_, err := svc.openCodeVersion("opencode")
	if err == nil {
		t.Fatal("expected error for empty version output")
	}
}

func TestCheckOne_CodexVersion(t *testing.T) {
	svc := newTestService(
		responseFor("codex", "codex-cli 0.87.0", nil),
	)
	version, err := svc.codexVersion("codex")
	if err != nil {
		t.Fatalf("codexVersion error: %v", err)
	}
	if version != "0.87.0" {
		t.Errorf("codexVersion = %q, want %q", version, "0.87.0")
	}
}

func TestCheckOne_CodexVersion_FallbackToDashV(t *testing.T) {
	// First call (--version) fails, second call (-V) succeeds
	m := &codexFallbackRunner{
		firstErr:  fmt.Errorf("unknown flag --version"),
		firstOut:  "",
		secondOut: "codex 0.5.0",
	}
	svc := NewServiceWithRunner(m)

	version, err := svc.codexVersion("codex")
	if err != nil {
		t.Fatalf("codexVersion error: %v", err)
	}
	if version != "0.5.0" {
		t.Errorf("codexVersion = %q, want %q", version, "0.5.0")
	}
}

func TestCheckOne_CodexVersion_BothFail(t *testing.T) {
	svc := newTestService() // no responses => both fail

	_, err := svc.codexVersion("nonexistent-codex")
	if err == nil {
		t.Fatal("expected error when both codex version commands fail")
	}
}

// codexFallbackRunner returns error for --version but success for -V.
type codexFallbackRunner struct {
	calls     []string
	firstErr  error
	firstOut  string
	secondOut string
	callCount int
	mu        sync.Mutex
}

func (r *codexFallbackRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callCount++

	// First call = --version, second call = -V
	if r.callCount == 1 {
		return stdResult(r.firstOut), r.firstErr
	}
	return stdResult(r.secondOut), nil
}

func (r *codexFallbackRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// 7. Version comparison helpers
// ---------------------------------------------------------------------------

func TestCompareVersionStrings(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"2.1.110", "2.1.111", -1},
		{"0.87.0", "0.87.0", 0},
		{"1.0", "1.0.0", 0}, // missing patch is treated as zero
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"1.0.0-alpha", "1.0.0", -1}, // pre-release is lower than the corresponding stable version
		{"1.0.0", "1.0.0-alpha", 1},
		{"1.0.0+build", "1.0.0", 0}, // build metadata does not affect precedence
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"1.0.0-beta", "1.0.0-alpha", 1},
	}

	for _, tc := range tests {
		t.Run(tc.current+"_vs_"+tc.latest, func(t *testing.T) {
			got := compareVersionStrings(tc.current, tc.latest)
			if got != tc.want {
				t.Errorf("compareVersionStrings(%q, %q) = %d, want %d", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestVersionParts(t *testing.T) {
	tests := []struct {
		version string
		want    []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"0.87.0", []int{0, 87, 0}},
		{"", nil},
		{"v1.0.0", []int{1, 0, 0}},
		{"1.0.0-alpha", []int{1, 0, 0}},
		{"1.0.0+build.1", []int{1, 0, 0}},
		{"1", []int{1}},
		{"x.y.z", nil},
	}

	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			got := versionParts(tc.version)
			if len(got) != len(tc.want) {
				t.Fatalf("versionParts(%q) = %v, want %v", tc.version, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("versionParts(%q)[%d] = %d, want %d", tc.version, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 8. firstNonEmptyLine
// ---------------------------------------------------------------------------

func TestFirstNonEmptyLine(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"hello\nworld", "hello"},
		{"\n\nhello\nworld", "hello"},
		{"  \n  hello  \nworld", "hello"},
		{"", ""},
		{"\n\n", ""},
		{"single", "single"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := firstNonEmptyLine(tc.input)
			if got != tc.expect {
				t.Errorf("firstNonEmptyLine(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 9. resultText
// ---------------------------------------------------------------------------

func TestResultText(t *testing.T) {
	tests := []struct {
		name   string
		result *platform.ProcessResult
		expect string
	}{
		{"nil result", nil, ""},
		{"stdout only", &platform.ProcessResult{Stdout: "hello"}, "hello"},
		{"stderr only", &platform.ProcessResult{Stderr: "error msg"}, "error msg"},
		{"both", &platform.ProcessResult{Stdout: "out", Stderr: "err"}, "out\nerr"},
		{"empty both", &platform.ProcessResult{}, ""},
		{"whitespace stdout", &platform.ProcessResult{Stdout: "  "}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resultText(tc.result)
			if got != tc.expect {
				t.Errorf("resultText() = %q, want %q", got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 10. CheckLatestVersion - unsupported tool
// ---------------------------------------------------------------------------

func TestCheckLatestVersion_UnsupportedTool(t *testing.T) {
	svc := newTestService()
	_, err := svc.CheckLatestVersion(CLITool("nonexistent"))
	if err == nil {
		t.Fatal("expected error for unsupported tool")
	}
}

// ---------------------------------------------------------------------------
// 11. CheckLatestVersion - cache hit
// ---------------------------------------------------------------------------

func TestCheckLatestVersion_CacheHit(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "1.0.0", nil),
	)

	// First call populates cache
	v1, err := svc.CheckLatestVersion(ToolOpenCode)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Second call should hit cache (mockRunner would fail on extra calls
	// but cache prevents that)
	v2, err := svc.CheckLatestVersion(ToolOpenCode)
	if err != nil {
		t.Fatalf("cached call error: %v", err)
	}
	if v1 != v2 {
		t.Errorf("cached version mismatch: first=%q, second=%q", v1, v2)
	}
}

// ---------------------------------------------------------------------------
// 12. emptyOverallStatus / cloneOverallStatus
// ---------------------------------------------------------------------------

func TestEmptyOverallStatus(t *testing.T) {
	status := emptyOverallStatus()
	if status.AllOK {
		t.Error("empty status should not be AllOK")
	}
	if len(status.Items) != 0 {
		t.Error("empty status should have no items")
	}
	if len(status.Issues) != 0 {
		t.Error("empty status should have no issues")
	}
	if status.CheckedAt.IsZero() {
		t.Error("empty status should have a non-zero timestamp")
	}
}

func TestCloneOverallStatus_Nil(t *testing.T) {
	got := cloneOverallStatus(nil)
	if got != nil {
		t.Error("cloneOverallStatus(nil) should return nil")
	}
}

func TestCloneOverallStatus_DefensiveCopy(t *testing.T) {
	original := &OverallStatus{
		AllOK: true,
		Items: map[string]CheckStatus{
			string(ToolClaudeCode): {Tool: ToolClaudeCode, Version: "1.0.0"},
		},
		Issues:    []string{"issue1"},
		CheckedAt: time.Now(),
	}

	cloned := cloneOverallStatus(original)

	// Mutate clone should not affect original
	cloned.AllOK = false
	cloned.Items[string(ToolClaudeCode)] = CheckStatus{Version: "changed"}
	cloned.Issues[0] = "changed"

	if !original.AllOK {
		t.Error("mutating clone affected original AllOK")
	}
	if original.Items[string(ToolClaudeCode)].Version != "1.0.0" {
		t.Error("mutating clone affected original Items")
	}
	if original.Issues[0] != "issue1" {
		t.Error("mutating clone affected original Issues")
	}
}

// ---------------------------------------------------------------------------
// 13. toolIssue
// ---------------------------------------------------------------------------

func TestToolIssue(t *testing.T) {
	tests := []struct {
		name   string
		status *CheckStatus
		expect string
	}{
		{"nil status", nil, "unknown CLI tool status is unavailable"},
		{"has error", &CheckStatus{Tool: ToolClaudeCode, Error: "something broke"}, "claude_code: something broke"},
		{"not installed", &CheckStatus{Tool: ToolClaudeCode, Installed: false}, "claude_code: not installed"},
		{"not in PATH", &CheckStatus{Tool: ToolClaudeCode, Installed: true, PATHOk: false}, "claude_code: executable is not available in PATH"},
		{"generic issue", &CheckStatus{Tool: ToolClaudeCode, Installed: true, PATHOk: true}, "claude_code: check reported an issue"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := toolIssue(tc.status)
			if got != tc.expect {
				t.Errorf("toolIssue() = %q, want %q", got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 14. Install / Update - unsupported tool
// ---------------------------------------------------------------------------

func TestInstall_UnsupportedTool(t *testing.T) {
	svc := newTestService()
	_, err := svc.Install(CLITool("nonexistent"))
	if err == nil {
		t.Fatal("expected error for unsupported tool install")
	}
}

func TestInstall_ContinuesWhenPrecheckReturnsStatusError(t *testing.T) {
	tmpDir := t.TempDir()
	openCodePath := writeTestExecutable(t, tmpDir, "opencode")
	t.Setenv("PATH", tmpDir)

	runner := &mockRunner{responses: []mockResponse{
		responseFor(openCodePath, "", errors.New("version command failed")),
		responseFor("npm", "10.0.0", nil),
	}}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Install(ToolOpenCode)
	if err == nil {
		t.Fatal("expected verification error after install because version command remains broken")
	}
	if result == nil {
		t.Fatal("expected install result")
	}
	if strings.Contains(result.Message, "pre-install check failed") {
		t.Fatalf("install stopped at precheck: %#v", result)
	}
	if !runnerSawArgs(runner, "npm", "install", "-g", "opencode-ai@latest") {
		t.Fatal("expected npm install command to run after recoverable precheck status error")
	}
}

func TestUpdate_UnsupportedTool(t *testing.T) {
	svc := newTestService()
	_, err := svc.Update(CLITool("nonexistent"))
	if err == nil {
		t.Fatal("expected error for unsupported tool update")
	}
}

// ---------------------------------------------------------------------------
// 15. displayToolName
// ---------------------------------------------------------------------------

func TestDisplayToolName(t *testing.T) {
	tests := []struct {
		tool   CLITool
		expect string
	}{
		{ToolClaudeCode, "Claude Code"},
		{ToolOpenCode, "OpenCode"},
		{ToolCodex, "Codex"},
		{ToolHeadroom, "Headroom"},
		{CLITool("other"), "other"},
	}
	for _, tc := range tests {
		t.Run(string(tc.tool), func(t *testing.T) {
			got := displayToolName(tc.tool)
			if got != tc.expect {
				t.Errorf("displayToolName(%q) = %q, want %q", tc.tool, got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 16. isHealthyAndCurrent
// ---------------------------------------------------------------------------

func TestIsHealthyAndCurrent(t *testing.T) {
	tests := []struct {
		name   string
		status *CheckStatus
		expect bool
	}{
		{"nil", nil, false},
		{"not installed", &CheckStatus{Installed: false}, false},
		{"not in PATH", &CheckStatus{Installed: true, PATHOk: false}, false},
		{"has update", &CheckStatus{Installed: true, PATHOk: true, HasUpdate: true}, false},
		{"has error", &CheckStatus{Installed: true, PATHOk: true, Error: "bad"}, false},
		{"healthy", &CheckStatus{Installed: true, PATHOk: true}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isHealthyAndCurrent(tc.status)
			if got != tc.expect {
				t.Errorf("isHealthyAndCurrent() = %v, want %v", got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 17. installFailure
// ---------------------------------------------------------------------------

func TestInstallFailure(t *testing.T) {
	result := installFailure(ToolClaudeCode, "test message", errors.New("underlying error"))
	if result.Success {
		t.Error("installFailure should return Success=false")
	}
	if result.Tool != ToolClaudeCode {
		t.Errorf("Tool = %q, want %q", result.Tool, ToolClaudeCode)
	}
	if result.Error != "underlying error" {
		t.Errorf("Error = %q, want %q", result.Error, "underlying error")
	}
	if result.Message != "test message" {
		t.Errorf("Message = %q, want %q", result.Message, "test message")
	}
}

// ---------------------------------------------------------------------------
// 18. verificationErrorMessage
// ---------------------------------------------------------------------------

func TestVerificationErrorMessage(t *testing.T) {
	tests := []struct {
		name   string
		status *CheckStatus
		expect string
	}{
		{"nil", nil, "安装后工具状态为空"},
		{"has error", &CheckStatus{Error: "some error"}, "some error"},
		{"not installed", &CheckStatus{}, "安装后未找到工具可执行文件"},
		{"not in PATH", &CheckStatus{Installed: true, PATHOk: false}, "工具在 PATH 之外被找到；请在 PATH 变更后重启应用程序或终端"},
		{"generic", &CheckStatus{Installed: true, PATHOk: true}, "工具验证未报告可用安装"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := verificationErrorMessage(tc.status)
			if got != tc.expect {
				t.Errorf("verificationErrorMessage() = %q, want %q", got, tc.expect)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 20. Concurrent access safety
// ---------------------------------------------------------------------------

func TestGetCachedStatus_ConcurrentAccess(t *testing.T) {
	svc := newTestService()
	_, _ = svc.CheckAll()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.GetCachedStatus()
		}()
	}
	wg.Wait()
	// If we reach here without panic or race, the test passes.
}

// ---------------------------------------------------------------------------
// 21. claudeInstallCommands
// ---------------------------------------------------------------------------

func TestClaudeInstallCommands_Install(t *testing.T) {
	svc := newTestService()
	cmds, _ := svc.claudeInstallCommands(installOperationInstall, nil)
	if runtime.GOOS == "windows" {
		if len(cmds) < 1 {
			t.Fatalf("expected at least 1 install command on Windows, got %d", len(cmds))
		}
		// Should include npm (winget removed)
		hasNPM := false
		for _, cmd := range cmds {
			d := strings.ToLower(cmd.description)
			if strings.Contains(d, "npm") {
				hasNPM = true
			}
		}
		if !hasNPM {
			t.Error("expected npm command in install list")
		}
	} else {
		// macOS/Linux: only npm
		if len(cmds) != 1 {
			t.Fatalf("expected 1 install command on non-Windows, got %d", len(cmds))
		}
		if cmds[0].path != "npm" {
			t.Errorf("expected npm command on non-Windows, got path %q", cmds[0].path)
		}
		if cmds[0].args[0] != "install" {
			t.Errorf("expected 'install' action, got args: %v", cmds[0].args)
		}
	}
}

func TestClaudeInstallCommands_UpdateNonWinget(t *testing.T) {
	svc := newTestService()
	cmds, err := svc.claudeInstallCommands(installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethodNative,
	})
	if runtime.GOOS == "windows" {
		if err == nil {
			if len(cmds) != 1 {
				t.Fatalf("expected exactly 1 command, got %d", len(cmds))
			}
			if !strings.Contains(strings.ToLower(cmds[0].description), "npm") {
				t.Errorf("native update uses canonical npm update command after native direct updater removal, got %q", cmds[0].description)
			}
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	} else {
		if len(cmds) != 1 {
			t.Fatalf("expected 1 command on non-Windows, got %d", len(cmds))
		}
		if cmds[0].path != "npm" {
			t.Errorf("expected npm command on non-Windows, got path %q", cmds[0].path)
		}
	}
}

func TestClaudeInstallCommands_UpdateRemovedLegacyMethod(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("removed legacy install-method fallback only relevant on Windows")
	}
	svc := newTestService()
	cmds, err := svc.claudeInstallCommands(installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethod("winget"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Removed legacy install-method values are no longer supported; unknown update falls back to npm only.
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 command (npm fallback for unsupported legacy method), got %d", len(cmds))
	}
	if !strings.Contains(cmds[0].description, "npm") {
		t.Errorf("removed legacy method update should fall back to npm, got %q", cmds[0].description)
	}
}

// ---------------------------------------------------------------------------
// 22. installCommands for OpenCode/Codex
// ---------------------------------------------------------------------------

func TestInstallCommands_OpenCode(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	cmds, err := svc.installCommands(ToolOpenCode, installOperationInstall, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command for OpenCode install, got %d", len(cmds))
	}
	if !isNPMPath(strings.ToLower(cmds[0].path)) {
		t.Errorf("expected npm command, got %s", cmds[0].path)
	}
}

func TestInstallCommands_Codex(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	cmds, err := svc.installCommands(ToolCodex, installOperationInstall, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if !strings.Contains(cmds[0].description, "codex") {
		t.Errorf("expected codex in description, got %q", cmds[0].description)
	}
}

func TestInstallCommands_NPMNotAvailable(t *testing.T) {
	svc := newTestService() // no npm response
	_, err := svc.installCommands(ToolOpenCode, installOperationInstall, nil, ClaudeInstallAuto)
	if err == nil {
		t.Fatal("expected error when npm is not available")
	}
	if !strings.Contains(err.Error(), "npm") {
		t.Errorf("error should mention npm: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 23. ensureNPMAvailable
// ---------------------------------------------------------------------------

func TestEnsureNPMAvailable_Success(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	if err := svc.ensureNPMAvailable(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestEnsureNPMAvailable_Fails(t *testing.T) {
	svc := newTestService() // no npm response
	if err := svc.ensureNPMAvailable(); err == nil {
		t.Error("expected error when npm is not available")
	}
}

// ---------------------------------------------------------------------------
// 24. pathSegment
// ---------------------------------------------------------------------------

func TestPathSegment(t *testing.T) {
	seg := pathSegment("npm")
	if !strings.Contains(seg, "npm") {
		t.Errorf("pathSegment('npm') = %q, should contain 'npm'", seg)
	}
}

// ---------------------------------------------------------------------------
// 25. pathFragment
// ---------------------------------------------------------------------------

func TestPathFragment(t *testing.T) {
	frag := pathFragment("foo", "bar")
	if !strings.Contains(frag, "foo") || !strings.Contains(frag, "bar") {
		t.Errorf("pathFragment('foo','bar') = %q", frag)
	}
}

// ---------------------------------------------------------------------------
// 26. NPM update commands for Claude Code
// ---------------------------------------------------------------------------

func TestInstallCommands_ClaudeUpdate_NPMInstall(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	cmds, err := svc.installCommands(ToolClaudeCode, installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethodNPM,
		Installed:     true,
	}, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runtime.GOOS == "windows" {
		// Strict same-channel: NPM update only, no cross-channel fallback.
		if len(cmds) != 1 {
			t.Fatalf("expected exactly 1 command on Windows (npm only), got %d", len(cmds))
		}
	} else {
		// macOS/Linux: npm update only
		if len(cmds) != 1 {
			t.Fatalf("expected exactly 1 command on non-Windows, got %d", len(cmds))
		}
	}
	// Command should be npm install @latest for Claude Code (update uses
	// install -g @latest which is more reliable than npm update).
	if cmds[0].path != "npm" && !strings.Contains(cmds[0].path, "npm") {
		t.Errorf("first command path = %q, want npm", cmds[0].path)
	}
	if cmds[0].args[0] != "install" {
		t.Errorf("first command should use 'install', got args: %v", cmds[0].args)
	}
	foundClaudePackage := false
	for _, arg := range cmds[0].args {
		if arg == "@anthropic-ai/claude-code@latest" || arg == "@anthropic-ai/claude-code" {
			foundClaudePackage = true
		}
	}
	if !foundClaudePackage {
		t.Errorf("first command args should contain @anthropic-ai/claude-code, got: %v", cmds[0].args)
	}
}

func TestInstallCommands_ClaudeInstall_NPMInstall(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	cmds, err := svc.installCommands(ToolClaudeCode, installOperationInstall, &CheckStatus{
		InstallMethod: InstallMethodNPM,
		Installed:     false,
	}, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runtime.GOOS == "windows" {
		if len(cmds) < 1 {
			t.Fatalf("expected at least 1 command on Windows, got %d", len(cmds))
		}
		// Verify npm is present (winget removed)
		foundNPM := false
		for _, cmd := range cmds {
			if cmd.path == "npm" || strings.Contains(cmd.path, "npm") {
				foundNPM = true
			}
		}
		if !foundNPM {
			t.Error("expected npm command in fresh install list")
		}
	} else {
		if len(cmds) < 1 {
			t.Fatalf("expected at least 1 command on non-Windows, got %d", len(cmds))
		}
		// First command should be NPM install for Claude Code
		if cmds[0].path != "npm" && !strings.Contains(cmds[0].path, "npm") {
			t.Errorf("first command path = %q, want npm", cmds[0].path)
		}
		if cmds[0].args[0] != "install" {
			t.Errorf("first command should use 'install', got args: %v", cmds[0].args)
		}
	}
}

func TestInstallCommands_ClaudeUpdate_NonNPM_NoCrossChannelFallback(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	cmds, err := svc.installCommands(ToolClaudeCode, installOperationUpdate, &CheckStatus{
		InstallMethod: InstallMethodNative,
		Installed:     true,
	}, ClaudeInstallAuto)
	if runtime.GOOS == "windows" {
		if err == nil {
			if len(cmds) != 1 {
				t.Fatalf("expected exactly 1 command, got %d", len(cmds))
			}
			if !strings.Contains(strings.ToLower(cmds[0].description), "npm") {
				t.Errorf("native update uses canonical npm update command after native direct updater removal, got %q", cmds[0].description)
			}
			if strings.Contains(strings.ToLower(cmds[0].description), "winget") {
				t.Errorf("native update should not include winget fallback, got %q", cmds[0].description)
			}
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	} else {
		// macOS/Linux: npm only
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cmds) != 1 {
			t.Fatalf("expected exactly 1 command on non-Windows, got %d", len(cmds))
		}
	}
}

func TestInstallCommands_OpenCode_UsesInstallLatestForUpdateOp(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	cmds, err := svc.installCommands(ToolOpenCode, installOperationUpdate, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].args[0] != "install" {
		t.Errorf("update operation should use 'npm install', got args: %v", cmds[0].args)
	}
	foundLatest := false
	for _, arg := range cmds[0].args {
		if arg == "opencode-ai@latest" {
			foundLatest = true
		}
		if arg == "opencode-ai" {
			t.Errorf("update operation should pin opencode-ai@latest, got unqualified package in args: %v", cmds[0].args)
		}
	}
	if !foundLatest {
		t.Errorf("update operation should force latest OpenCode package, got args: %v", cmds[0].args)
	}
}

func TestInstallCommands_Codex_UsesUpdateForUpdateOp(t *testing.T) {
	svc := newTestService(
		responseFor("npm", "10.0.0", nil),
	)
	cmds, err := svc.installCommands(ToolCodex, installOperationUpdate, nil, ClaudeInstallAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].args[0] != "update" {
		t.Errorf("update operation should use 'npm update', got args: %v", cmds[0].args)
	}
}

// ---------------------------------------------------------------------------
// 27. claudeInstallCommandsForMethod
// ---------------------------------------------------------------------------

func TestClaudeInstallCommandsForMethod_NPM(t *testing.T) {
	cmd, err := claudeInstallCommandsForMethod(ClaudeInstallNPM, installOperationInstall)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.path != "npm" {
		t.Errorf("expected npm path, got %q", cmd.path)
	}
	if len(cmd.args) < 3 {
		t.Errorf("npm install command should have args, got: %v", cmd.args)
	}
	// Verify it uses install -g @anthropic-ai/claude-code
	if cmd.args[0] != "install" {
		t.Errorf("expected 'install' as first arg, got %q", cmd.args[0])
	}
	foundPkg := false
	for _, arg := range cmd.args {
		if arg == "@anthropic-ai/claude-code" {
			foundPkg = true
		}
	}
	if !foundPkg {
		t.Errorf("expected @anthropic-ai/claude-code in args, got: %v", cmd.args)
	}
}

func TestClaudeInstallCommandsForMethod_NPM_Update(t *testing.T) {
	cmd, err := claudeInstallCommandsForMethod(ClaudeInstallNPM, installOperationUpdate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.path != "npm" {
		t.Errorf("expected npm path, got %q", cmd.path)
	}
	// Update uses install -g @latest
	if cmd.args[0] != "install" {
		t.Errorf("expected 'install' as first arg, got %q", cmd.args[0])
	}
	foundLatest := false
	for _, arg := range cmd.args {
		if arg == "@anthropic-ai/claude-code@latest" {
			foundLatest = true
		}
	}
	if !foundLatest {
		t.Errorf("expected @anthropic-ai/claude-code@latest in args, got: %v", cmd.args)
	}
}

func TestClaudeInstallCommandsForMethod_Native(t *testing.T) {
	_, err := claudeInstallCommandsForMethod(ClaudeInstallNative, installOperationInstall)
	if err == nil {
		t.Fatal("native method should be handled by ordered npm + claude install flow, not a single command")
	}
}

func TestClaudeInstallMethodDisplayName_NativeIsPlatformSpecific(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "Native (npm + claude install)"},
		{goos: "linux", want: "Native (npm + claude install)"},
		{goos: "windows", want: "Native (npm + claude install)"},
		{goos: "freebsd", want: "Native (npm + claude install)"},
	}

	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			forceRuntimeGOOSForTest(t, tc.goos)

			got := claudeInstallMethodDisplayName(ClaudeInstallNative)
			if got != tc.want {
				t.Fatalf("claudeInstallMethodDisplayName(native) on %s = %q, want %q", tc.goos, got, tc.want)
			}
			if strings.Contains(got, "PowerShell") || strings.Contains(got, "install.sh") {
				t.Fatalf("native display name must not mention removed direct installers: %q", got)
			}
		})
	}
}

func TestClaudeInstallCommandsForMethod_Unsupported(t *testing.T) {
	_, err := claudeInstallCommandsForMethod(ClaudeInstallMethod("invalid"), installOperationInstall)
	if err == nil {
		t.Error("expected error for unsupported method")
	}
}

func TestClaudeInstallCommandsForMethod_AutoIsUnsupported(t *testing.T) {
	// ClaudeInstallAuto (empty string) is intentionally not supported
	// by claudeInstallCommandsForMethod -- it uses the fallback chain instead.
	_, err := claudeInstallCommandsForMethod(ClaudeInstallAuto, installOperationInstall)
	if err == nil {
		t.Error("expected error for auto method (empty string)")
	}
}

func TestClaudeInstallCommands_AutoUsesOnlyNPM(t *testing.T) {
	svc := newTestService()
	cmds, err := svc.claudeInstallCommands(installOperationInstall, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 1 || cmds[0].path != "npm" {
		t.Fatalf("auto Claude install should expose only npm command, got %+v", cmds)
	}
	combined := strings.ToLower(cmds[0].description + " " + cmds[0].path + " " + strings.Join(cmds[0].args, " "))
	for _, removed := range []string{"winget", "powershell", "install.ps1", "install.sh"} {
		if strings.Contains(combined, removed) {
			t.Fatalf("auto Claude install must not include removed path %q: %+v", removed, cmds[0])
		}
	}
}

func TestInstallCommands_NPMInstallAndUpdateDoNotRunClaudeInstall(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation installOperation
		pkg       string
	}{
		{name: "install", operation: installOperationInstall, pkg: "@anthropic-ai/claude-code"},
		{name: "update", operation: installOperationUpdate, pkg: "@anthropic-ai/claude-code@latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := claudeInstallCommandsForMethod(ClaudeInstallNPM, tc.operation)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd.path != "npm" {
				t.Fatalf("npm method path = %q, want npm", cmd.path)
			}
			wantArgs := []string{"install", "-g", tc.pkg}
			if strings.Join(cmd.args, "\x00") != strings.Join(wantArgs, "\x00") {
				t.Fatalf("npm method args = %v, want %v", cmd.args, wantArgs)
			}
			if strings.Contains(strings.ToLower(cmd.path), "claude") && len(cmd.args) == 1 && cmd.args[0] == "install" {
				t.Fatalf("npm method must not execute claude install: %+v", cmd)
			}
		})
	}
}

func TestClaudeInstallCommandsForMethod_NativeRejectsDirectInstaller(t *testing.T) {
	_, err := claudeInstallCommandsForMethod(ClaudeInstallNative, installOperationInstall)
	if err == nil {
		t.Fatal("native should not expose PowerShell/install.sh as a single command")
	}
}
