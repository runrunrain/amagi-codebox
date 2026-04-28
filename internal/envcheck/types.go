package envcheck

import "time"

// CLITool identifies a supported command-line tool managed by EnvCheck.
type CLITool string

const (
	ToolClaudeCode CLITool = "claude_code"
	ToolOpenCode   CLITool = "opencode"
	ToolCodex      CLITool = "codex"
)

// InstallMethod describes how a CLI tool was installed on the host.
type InstallMethod string

const (
	InstallMethodNative  InstallMethod = "native"
	InstallMethodWinget  InstallMethod = "winget"
	InstallMethodNPM     InstallMethod = "npm"
	InstallMethodUnknown InstallMethod = "unknown"
)

// CheckStatus is the frontend-facing status snapshot for a single CLI tool.
type CheckStatus struct {
	Tool           CLITool       `json:"tool"`
	Installed      bool          `json:"installed"`
	InstallMethod  InstallMethod `json:"installMethod"`
	Version        string        `json:"version"`
	HasUpdate      bool          `json:"hasUpdate"`
	LatestVersion  string        `json:"latestVersion"`
	PATHOk         bool          `json:"pathOk"`
	ExecutablePath string        `json:"executablePath"`
	Error          string        `json:"error"`
	CheckedAt      time.Time     `json:"checkedAt"`
}

// OverallStatus aggregates all supported CLI tool checks into one response.
type OverallStatus struct {
	AllOK     bool                   `json:"allOk"`
	Items     map[string]CheckStatus `json:"items"`
	Issues    []string               `json:"issues"`
	CheckedAt time.Time              `json:"checkedAt"`
}

// InstallResult is returned by install and update operations.
type InstallResult struct {
	Success bool    `json:"success"`
	Message string  `json:"message"`
	Tool    CLITool `json:"tool"`
	Version string  `json:"version"`
	Error   string  `json:"error"`
}
