package envcheck

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"amagi-codebox/internal/platform"
)

// resolveResult holds the outcome of the unified two-phase executable
// resolution used by every checker.
type resolveResult struct {
	executablePath string
	systemPATHOk   bool
	pathState      PathState
	pathSource     string
}

// resolveExecutable performs the unified two-phase resolution:
//  1. exec.LookPath to check the raw process PATH → SystemPATHOk
//  2. platform CLIResolver with augmented env → full CodeBox visibility
//
// The caller supplies the command name and gets back a structured result
// that captures both phases independently.
func resolveExecutable(commandName string) resolveResult {
	pathFromLookPath, lookErr := exec.LookPath(commandName)
	systemPATHOk := lookErr == nil && strings.TrimSpace(pathFromLookPath) != ""

	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	resolved, diagnostics, resolveErr := resolver.ResolveExecutable(commandName, nil, os.Environ())

	// Phase 2 succeeded: CodeBox can find and launch the tool.
	if resolveErr == nil && strings.TrimSpace(resolved.Path) != "" {
		pathState := PathStateCodeboxPATH
		if systemPATHOk {
			pathState = PathStateSystemPATH
		}
		source := diagnostics.CLISource
		if source == "" {
			source = "path-search"
		}
		// If the source indicates shell fallback, upgrade the path state.
		if source == "fallback" || source == "shell-assisted" {
			if !systemPATHOk {
				pathState = PathStateShellFallback
			}
		}
		if source == "ambient-path" && !systemPATHOk {
			pathState = PathStateCodeboxPATH
		}
		return resolveResult{
			executablePath: resolved.Path,
			systemPATHOk:   systemPATHOk,
			pathState:      pathState,
			pathSource:     source,
		}
	}

	// Phase 2 failed but Phase 1 succeeded (rare but possible if resolver
	// env construction misses something).
	if systemPATHOk {
		return resolveResult{
			executablePath: pathFromLookPath,
			systemPATHOk:   true,
			pathState:      PathStateSystemPATH,
			pathSource:     "lookpath",
		}
	}

	// Neither phase found the executable.
	return resolveResult{
		executablePath: "",
		systemPATHOk:   false,
		pathState:      PathStateMissing,
		pathSource:     "missing",
	}
}

// applyPathStateToStatus sets PATHOk, SystemPATHOk, PathState, PathSource,
// and populates Issues/Solutions on the CheckStatus based on the resolve result.
//
// The key semantics:
//   - PATHOk = true whenever CodeBox can find and launch the tool (resolver
//     succeeded), regardless of whether exec.LookPath saw it.
//   - When resolver succeeds but LookPath fails, an info-level issue with a
//     restart/sync suggestion is added, but OverallStatus should NOT count
//     this as a blocking problem.
func applyPathStateToStatus(status *CheckStatus, rr resolveResult, tool CLITool) {
	if status == nil {
		return
	}

	status.SystemPATHOk = rr.systemPATHOk
	status.PathState = rr.pathState
	status.PathSource = rr.pathSource

	if rr.executablePath != "" {
		status.PATHOk = true
		status.ExecutablePath = rr.executablePath

		// When resolver found it but LookPath did not, add an info-level hint.
		if !rr.systemPATHOk {
			issue := CheckIssue{
				Severity: SeverityInfo,
				Code:     "path_not_in_system_path",
				Message:  fmt.Sprintf("%s 可被 CodeBox 访问但未在系统 PATH 中显示", displayToolName(tool)),
				Detail:   "该工具在 CodeBox 内可用。如需在终端中使用，请修复 shell 配置文件中的 PATH 或重启终端。",
				Solutions: []ResolutionAction{
					{
						Type:            SolutionFixPath,
						Description:     "一键修复: 将工具目录添加到 shell 配置文件 PATH",
						Tool:            tool,
						RequiresConfirm: true,
						IsPrimary:       true,
					},
					{
						Type:        SolutionRestartApp,
						Description: "重启 CodeBox 以刷新检测到的 PATH",
						Tool:        tool,
					},
				},
			}
			status.Issues = append(status.Issues, issue)
			// Also add the fix_path solution to the top-level solutions
			status.Solutions = append(status.Solutions, ResolutionAction{
				Type:            SolutionFixPath,
				Description:     "修复 PATH 使 " + displayToolName(tool) + " 在系统 PATH 中可见",
				Tool:            tool,
				RequiresConfirm: true,
				IsPrimary:       true,
			})
		}
	} else {
		status.PATHOk = false
	}
}

// addMissingToolIssue adds a structured issue for when a tool is not installed.
func addMissingToolIssue(status *CheckStatus, tool CLITool) {
	if status == nil {
		return
	}
	issue := CheckIssue{
		Severity: SeverityError,
		Code:     "tool_not_installed",
		Message:  fmt.Sprintf("%s 未安装", displayToolName(tool)),
		Solutions: []ResolutionAction{
			{
				Type:        SolutionInstallTool,
				Description: fmt.Sprintf("通过 npm 安装 %s", displayToolName(tool)),
				Tool:        tool,
			},
		},
	}
	status.Issues = append(status.Issues, issue)
}

// cliCommandNamesForTool returns the concrete executable names used by the
// platform resolver for a managed tool. CLITool values are stable API IDs
// (for example "claude_code"), not necessarily command names. PATH repair must
// use these command names; otherwise Claude Code is never discovered while
// collecting directories to add to shell profiles.
func cliCommandNamesForTool(tool CLITool) []string {
	switch tool {
	case ToolClaudeCode:
		return []string{claudeCommandName, "claudecode", "claude-code"}
	case ToolOpenCode:
		return []string{"opencode"}
	case ToolCodex:
		return []string{"codex"}
	default:
		return nil
	}
}

// npmPackageName returns the npm package name for a given tool.
func npmPackageName(tool CLITool) string {
	switch tool {
	case ToolClaudeCode:
		return "@anthropic-ai/claude-code"
	case ToolOpenCode:
		return "opencode-ai"
	case ToolCodex:
		return "@openai/codex"
	default:
		return ""
	}
}

var runtimeGOOS = runtime.GOOS

// isWindows reports whether the current OS is Windows.
func isWindows() bool {
	return runtimeGOOS == "windows"
}
