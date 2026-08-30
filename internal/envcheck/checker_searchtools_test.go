package envcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// withSimulatedWindowsSearchTools 把 runtimeGOOS 模拟为 windows 并注入假
// distro/探测实现（与 wsl_hybrid_test 的 wslHybridDistro 注入同模式），测试
// 结束还原。distro 传 "" 时模拟“无可用 WSL distro”→ 走 Windows 原生分支。
func withSimulatedWindowsSearchTools(t *testing.T, distro string, probe func(distro string) platform.WSLSearchTools) {
	t.Helper()
	origGOOS, origDistro, origProbe := runtimeGOOS, searchToolsDistroProbe, wslSearchToolStatusProbe
	runtimeGOOS = "windows"
	searchToolsDistroProbe = func() string { return distro }
	if probe != nil {
		wslSearchToolStatusProbe = probe
	}
	t.Cleanup(func() {
		runtimeGOOS = origGOOS
		searchToolsDistroProbe = origDistro
		wslSearchToolStatusProbe = origProbe
	})
}

func findIssueByCode(status *CheckStatus, code string) *CheckIssue {
	for i := range status.Issues {
		if status.Issues[i].Code == code {
			return &status.Issues[i]
		}
	}
	return nil
}

func searchToolsIssueCodes(status *CheckStatus) []string {
	var codes []string
	for _, issue := range status.Issues {
		if strings.Contains(issue.Code, "search_tools") {
			codes = append(codes, issue.Code)
		}
	}
	return codes
}

// WSL 侧矩阵：探测结果 → issue 结构（severity/code/detail/solutions 完整性）。
func TestAppendSearchToolsIssuesWSLMatrix(t *testing.T) {
	cases := []struct {
		name       string
		tool       CLITool
		tools      platform.WSLSearchTools
		wantIssue  bool
		wantCode   string
		wantDetail []string
	}{
		{
			name: "pi both missing", tool: ToolPi,
			tools: platform.WSLSearchTools{}, wantIssue: true, wantCode: "pi_wsl_missing_search_tools",
			wantDetail: []string{"Ubuntu", "fd", "ripgrep"},
		},
		{
			name: "pi fd missing only", tool: ToolPi,
			tools: platform.WSLSearchTools{Ripgrep: true}, wantIssue: true, wantCode: "pi_wsl_missing_search_tools",
			wantDetail: []string{"Ubuntu", "fd"},
		},
		{
			name: "omp rg missing only", tool: ToolOmp,
			tools: platform.WSLSearchTools{FD: true}, wantIssue: true, wantCode: "omp_wsl_missing_search_tools",
			wantDetail: []string{"Ubuntu", "ripgrep"},
		},
		{
			name: "omp both present", tool: ToolOmp,
			tools:     platform.WSLSearchTools{FD: true, Ripgrep: true},
			wantIssue: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withSimulatedWindowsSearchTools(t, "Ubuntu", func(distro string) platform.WSLSearchTools {
				if distro != "Ubuntu" {
					t.Errorf("probe distro = %q, want Ubuntu", distro)
				}
				return tc.tools
			})
			status := &CheckStatus{Tool: tc.tool, Installed: true}
			newTestService().appendSearchToolsIssues(status)

			if !tc.wantIssue {
				if codes := searchToolsIssueCodes(status); len(codes) > 0 {
					t.Fatalf("unexpected issues %v, want none", codes)
				}
				return
			}
			issue := findIssueByCode(status, tc.wantCode)
			if issue == nil {
				t.Fatalf("issue %q not found; got %+v", tc.wantCode, status.Issues)
			}
			if issue.Severity != SeverityWarning {
				t.Errorf("severity = %q, want warning", issue.Severity)
			}
			for _, want := range tc.wantDetail {
				if !strings.Contains(issue.Detail, want) {
					t.Errorf("detail %q missing %q", issue.Detail, want)
				}
			}
			if len(issue.Solutions) != 2 {
				t.Fatalf("solutions = %+v, want 2", issue.Solutions)
			}
			primary := issue.Solutions[0]
			if primary.Type != SolutionInstallWslSearchTools || !primary.IsPrimary || !primary.RequiresConfirm {
				t.Errorf("primary solution = %+v, want install_wsl_search_tools + confirm + primary", primary)
			}
			if primary.Tool != tc.tool {
				t.Errorf("primary solution tool = %q, want %q", primary.Tool, tc.tool)
			}
			manual := issue.Solutions[1]
			if manual.Type != SolutionManualCommand || manual.Command != "sudo apt-get install -y fd-find ripgrep" {
				t.Errorf("manual solution = %+v, want sudo apt-get install -y fd-find ripgrep", manual)
			}
		})
	}
}

// 门控矩阵：未安装 / 非 pi-omp 工具 / 非 Windows 宿主 / 无 distro 时不做 WSL 检测。
func TestAppendSearchToolsIssuesGating(t *testing.T) {
	probeCalled := false
	withSimulatedWindowsSearchTools(t, "Ubuntu", func(string) platform.WSLSearchTools {
		probeCalled = true
		return platform.WSLSearchTools{}
	})

	notInstalled := &CheckStatus{Tool: ToolPi, Installed: false}
	newTestService().appendSearchToolsIssues(notInstalled)
	if probeCalled {
		t.Fatal("probe must not run when the tool itself is not installed (fd/rg issue would be noise)")
	}
	if codes := searchToolsIssueCodes(notInstalled); len(codes) > 0 {
		t.Fatalf("unexpected issues for uninstalled tool: %v", codes)
	}

	otherTool := &CheckStatus{Tool: ToolClaudeCode, Installed: true}
	newTestService().appendSearchToolsIssues(otherTool)
	if probeCalled {
		t.Fatal("probe must not run for non-pi/omp tools")
	}
	if codes := searchToolsIssueCodes(otherTool); len(codes) > 0 {
		t.Fatalf("unexpected issues for claude: %v", codes)
	}

	// 非 Windows 宿主（还原 runtimeGOOS 为真实值）。
	origGOOS := runtimeGOOS
	runtimeGOOS = "linux"
	t.Cleanup(func() { runtimeGOOS = origGOOS })
	linuxStatus := &CheckStatus{Tool: ToolPi, Installed: true}
	newTestService().appendSearchToolsIssues(linuxStatus)
	if probeCalled {
		t.Fatal("probe must not run on non-Windows hosts")
	}
}

// Windows 原生侧（无 WSL distro → pi 必然原生运行）：PATH 命中即无 issue；
// PATH 与自管 bin 目录都缺失时追加 native issue + winget 手动指引；
// 自管 bin 目录（~/.pi/agent/bin）命中可替代 PATH。
func TestAppendSearchToolsIssuesNative(t *testing.T) {
	dir := t.TempDir()
	// 候选名同时覆盖 unix（fd/rg）与 Windows（fd.exe/rg.exe）语义。
	for _, name := range []string{"fd", "rg", "fd.exe", "rg.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("path present no issue", func(t *testing.T) {
		withSimulatedWindowsSearchTools(t, "", nil)
		t.Setenv("PATH", dir)
		status := &CheckStatus{Tool: ToolPi, Installed: true}
		newTestService().appendSearchToolsIssues(status)
		if codes := searchToolsIssueCodes(status); len(codes) > 0 {
			t.Fatalf("unexpected issues: %v", codes)
		}
	})

	t.Run("agent bin fallback", func(t *testing.T) {
		home := t.TempDir()
		bin := filepath.Join(home, ".pi", "agent", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"fd.exe", "rg.exe", "fd", "rg"} {
			if err := os.WriteFile(filepath.Join(bin, name), []byte{}, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		withSimulatedWindowsSearchTools(t, "", nil)
		t.Setenv("PATH", t.TempDir()) // PATH 无 fd/rg，靠自管目录兜底
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		status := &CheckStatus{Tool: ToolPi, Installed: true}
		newTestService().appendSearchToolsIssues(status)
		if codes := searchToolsIssueCodes(status); len(codes) > 0 {
			t.Fatalf("agent bin should satisfy the probe, got issues: %v", codes)
		}
	})

	t.Run("missing issue structure", func(t *testing.T) {
		home := t.TempDir()
		withSimulatedWindowsSearchTools(t, "", nil)
		t.Setenv("PATH", t.TempDir())
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		status := &CheckStatus{Tool: ToolOmp, Installed: true}
		newTestService().appendSearchToolsIssues(status)
		issue := findIssueByCode(status, "omp_missing_search_tools_native")
		if issue == nil {
			t.Fatalf("native issue not found; got %+v", status.Issues)
		}
		if issue.Severity != SeverityWarning {
			t.Errorf("severity = %q, want warning", issue.Severity)
		}
		if len(issue.Solutions) != 1 {
			t.Fatalf("solutions = %+v, want exactly the manual winget command", issue.Solutions)
		}
		sol := issue.Solutions[0]
		if sol.Type != SolutionManualCommand || sol.Command != "winget install sharkdp.fd BurntSushi.ripgrep.MSVC" || !sol.IsPrimary {
			t.Errorf("solution = %+v, want primary manual_command with winget ids", sol)
		}
		if !strings.Contains(issue.Detail, "fd") || !strings.Contains(issue.Detail, "ripgrep") {
			t.Errorf("detail should list the missing tools: %q", issue.Detail)
		}
	})
}

// WSL 侧优先：distro 可用时不再追加原生侧 issue（Windows 会话默认内嵌 WSL，
// 原生缺失不构成会话实际遇到的问题——见 checker_searchtools.go 文件头注释）。
func TestAppendSearchToolsIssuesWSLTakesPrecedence(t *testing.T) {
	// PATH 上没有任何 fd/rg，但 WSL 探测全部命中。
	empty := t.TempDir()
	withSimulatedWindowsSearchTools(t, "Ubuntu", func(string) platform.WSLSearchTools {
		return platform.WSLSearchTools{FD: true, Ripgrep: true}
	})
	t.Setenv("PATH", empty)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	status := &CheckStatus{Tool: ToolPi, Installed: true}
	newTestService().appendSearchToolsIssues(status)
	if codes := searchToolsIssueCodes(status); len(codes) > 0 {
		t.Fatalf("WSL probe present must suppress native issue, got %v", codes)
	}
}
