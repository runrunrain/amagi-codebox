package envcheck

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/platform"
)

// wslHybridProbeRunner is a ProcessRunner test-double that only meaningfully
// answers the batched wsl.exe probe; everything else returns its canned stdout.
type wslHybridProbeRunner struct {
	mu     sync.Mutex
	calls  []platform.CommandSpec
	stdout string
	err    error
}

func (r *wslHybridProbeRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, spec)
	if r.err != nil {
		return &platform.ProcessResult{}, r.err
	}
	return &platform.ProcessResult{Stdout: r.stdout}, nil
}

func (r *wslHybridProbeRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func (r *wslHybridProbeRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// withWSLHybridGates installs the C7 probe gates (host OS + distro) for one
// test; without injection the probe is a no-op on non-Windows hosts and hosts
// without a usable distro.
func withWSLHybridGates(t *testing.T, goos, distro string) {
	t.Helper()
	prevGOOS, prevDistro := runtimeGOOS, wslHybridDistro
	runtimeGOOS = goos
	wslHybridDistro = func() string { return distro }
	t.Cleanup(func() {
		runtimeGOOS, wslHybridDistro = prevGOOS, prevDistro
	})
}

func findIssue(status *CheckStatus, code string) *CheckIssue {
	for i := range status.Issues {
		if status.Issues[i].Code == code {
			return &status.Issues[i]
		}
	}
	return nil
}

func TestParseWSLLoginProbeOutput(t *testing.T) {
	out := "claude\t/mnt/c/Users/x/npm/claude\n" +
		"opencode\t\n" +
		"pi\t/home/u/.npm-global/bin/pi\r\n" +
		"\n" +
		"codex\tcodex\n" +
		"garbage-no-tab\n"
	paths := parseWSLLoginProbeOutput(out)
	if len(paths) != 2 {
		t.Fatalf("parsed %d paths, want 2: %#v", len(paths), paths)
	}
	if paths["claude"] != "/mnt/c/Users/x/npm/claude" {
		t.Errorf("claude = %q", paths["claude"])
	}
	if paths["pi"] != "/home/u/.npm-global/bin/pi" {
		t.Errorf("pi = %q", paths["pi"])
	}
	if _, ok := paths["opencode"]; ok {
		t.Errorf("unresolved opencode must be dropped: %#v", paths)
	}
	if _, ok := paths["codex"]; ok {
		t.Errorf("non-path hit (builtin name) must be dropped: %#v", paths)
	}
}

func TestAppendWSLHybridArchWarning_WarnsOnWindowsPassthrough(t *testing.T) {
	withWSLHybridGates(t, "windows", "Ubuntu-Test")
	runner := &wslHybridProbeRunner{stdout: "pi\t/mnt/c/Users/u/AppData/Roaming/npm/pi\n"}
	svc := NewServiceWithRunner(runner)

	status := &CheckStatus{Tool: ToolPi}
	svc.appendWSLHybridArchWarning(status)

	issue := findIssue(status, "wsl_windows_passthrough")
	if issue == nil {
		t.Fatalf("expected wsl_windows_passthrough issue, got %#v", status.Issues)
	}
	if issue.Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning", issue.Severity)
	}
	if !strings.Contains(issue.Message, "Windows 侧") || !strings.Contains(issue.Message, "Pi") {
		t.Errorf("message should say the WSL session runs the Windows-side tool: %q", issue.Message)
	}
	if !strings.Contains(issue.Detail, "/mnt/c/Users/u/AppData/Roaming/npm/pi") {
		t.Errorf("detail should carry the resolved passthrough path: %q", issue.Detail)
	}
}

func TestAppendWSLHybridArchWarning_QuietForNativeResolution(t *testing.T) {
	withWSLHybridGates(t, "windows", "Ubuntu-Test")
	runner := &wslHybridProbeRunner{stdout: "pi\t/home/u/.npm-global/bin/pi\n"}
	svc := NewServiceWithRunner(runner)

	status := &CheckStatus{Tool: ToolPi}
	svc.appendWSLHybridArchWarning(status)
	if findIssue(status, "wsl_windows_passthrough") != nil {
		t.Errorf("native resolution must not warn: %#v", status.Issues)
	}
	if runner.callCount() != 1 {
		t.Errorf("probe ran %d times, want 1", runner.callCount())
	}
}

func TestAppendWSLHybridArchWarning_Gates(t *testing.T) {
	// Headroom never runs inside WSL: no probe, no warning.
	withWSLHybridGates(t, "windows", "Ubuntu-Test")
	runner := &wslHybridProbeRunner{stdout: "headroom\t/mnt/c/tools/headroom\n"}
	svc := NewServiceWithRunner(runner)
	status := &CheckStatus{Tool: ToolHeadroom}
	svc.appendWSLHybridArchWarning(status)
	if findIssue(status, "wsl_windows_passthrough") != nil || runner.callCount() != 0 {
		t.Errorf("headroom must be skipped entirely: issues=%#v probes=%d", status.Issues, runner.callCount())
	}

	// Non-Windows host: WSL sessions are not the launch path.
	withWSLHybridGates(t, "darwin", "Ubuntu-Test")
	runner2 := &wslHybridProbeRunner{stdout: "pi\t/mnt/c/Users/u/npm/pi\n"}
	svc2 := NewServiceWithRunner(runner2)
	status2 := &CheckStatus{Tool: ToolPi}
	svc2.appendWSLHybridArchWarning(status2)
	if findIssue(status2, "wsl_windows_passthrough") != nil || runner2.callCount() != 0 {
		t.Errorf("non-Windows host must be skipped: issues=%#v probes=%d", status2.Issues, runner2.callCount())
	}

	// No usable distro: nothing to probe.
	withWSLHybridGates(t, "windows", "")
	runner3 := &wslHybridProbeRunner{stdout: "pi\t/mnt/c/Users/u/npm/pi\n"}
	svc3 := NewServiceWithRunner(runner3)
	status3 := &CheckStatus{Tool: ToolPi}
	svc3.appendWSLHybridArchWarning(status3)
	if findIssue(status3, "wsl_windows_passthrough") != nil || runner3.callCount() != 0 {
		t.Errorf("no-distro host must be skipped: issues=%#v probes=%d", status3.Issues, runner3.callCount())
	}
}

func TestAppendWSLHybridArchWarning_ProbeCachedAcrossTools(t *testing.T) {
	withWSLHybridGates(t, "windows", "Ubuntu-Test")
	runner := &wslHybridProbeRunner{
		stdout: "claude\t/mnt/c/Users/u/npm/claude\npi\t/home/u/.npm-global/bin/pi\n",
	}
	svc := NewServiceWithRunner(runner)

	for _, tool := range []CLITool{ToolClaudeCode, ToolPi, ToolCodex} {
		svc.appendWSLHybridArchWarning(&CheckStatus{Tool: tool})
	}
	if got := runner.callCount(); got != 1 {
		t.Errorf("batched probe ran %d times across tools, want 1 (TTL cache)", got)
	}
}

// TestFinishToolCheckAppendsWSLHybridWarning pins the C7 wiring: the warning
// rides the single finishToolCheck choke point every CheckOne path passes
// through, without touching Install/PATH verdicts.
func TestFinishToolCheckAppendsWSLHybridWarning(t *testing.T) {
	withWSLHybridGates(t, "windows", "Ubuntu-Test")
	runner := &wslHybridProbeRunner{stdout: "pi\t/mnt/c/Users/u/npm/pi\n"}
	svc := NewServiceWithRunner(runner)

	status, err := svc.finishToolCheck(&CheckStatus{Tool: ToolPi}, nil)
	if err != nil {
		t.Fatalf("finishToolCheck: %v", err)
	}
	if findIssue(status, "wsl_windows_passthrough") == nil {
		t.Errorf("finishToolCheck must append the hybrid warning: %#v", status.Issues)
	}
	if status.Error != "" {
		t.Errorf("warning must not set Error: %q", status.Error)
	}
}
