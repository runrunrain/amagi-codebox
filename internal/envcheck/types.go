package envcheck

import (
	"os"
	"time"
)

// fileExists reports whether the named file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

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

// PathState describes how the CLI executable was located relative to PATH.
type PathState string

const (
	// PathStateMissing means the executable was not found anywhere.
	PathStateMissing PathState = "missing"
	// PathStateSystemPATH means the executable was found via exec.LookPath in
	// the process-inherited PATH.
	PathStateSystemPATH PathState = "system_path"
	// PathStateCodeboxPATH means the executable was found by the platform
	// resolver's augmented PATH (baseline + caller entries) but NOT by
	// exec.LookPath. CodeBox can still launch it.
	PathStateCodeboxPATH PathState = "codebox_path"
	// PathStateShellFallback means the executable was found via a shell login
	// fallback probe (e.g. zsh -ilc "command -v claude") on macOS.
	PathStateShellFallback PathState = "shell_fallback"
	// PathStateOutsidePATH means the executable path was discovered but does
	// not belong to any recognised PATH source.
	PathStateOutsidePATH PathState = "outside_path"
)

// IssueSeverity defines the severity level of a CheckIssue.
type IssueSeverity string

const (
	SeverityInfo     IssueSeverity = "info"
	SeverityWarning  IssueSeverity = "warning"
	SeverityError    IssueSeverity = "error"
	SeverityCritical IssueSeverity = "critical"
)

// SolutionType identifies the kind of resolution action a user can take.
type SolutionType string

const (
	SolutionInstallTool   SolutionType = "install_tool"
	SolutionInstallNode   SolutionType = "install_node"
	SolutionFixPath       SolutionType = "fix_path"
	SolutionRestartApp    SolutionType = "restart_app"
	SolutionRetry         SolutionType = "retry"
	SolutionManualCommand SolutionType = "manual_command"
)

// CheckIssue describes a single detected problem with a CLI tool environment.
type CheckIssue struct {
	Severity  IssueSeverity      `json:"severity"`
	Code      string             `json:"code"`
	Message   string             `json:"message"`
	Detail    string             `json:"detail,omitempty"`
	Solutions []ResolutionAction `json:"solutions,omitempty"`
}

// ResolutionAction describes an actionable step the user can take to resolve
// an issue.
type ResolutionAction struct {
	Type            SolutionType `json:"type"`
	Description     string       `json:"description"`
	Command         string       `json:"command,omitempty"`
	Tool            CLITool      `json:"tool,omitempty"`
	PackageName     string       `json:"packageName,omitempty"`
	RequiresConfirm bool         `json:"requiresConfirm,omitempty"`
	IsPrimary       bool         `json:"isPrimary,omitempty"`
}

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

	// SystemPATHOk is true when exec.LookPath can find the command in the
	// raw process-inherited PATH. Unlike PATHOk (which is true whenever
	// CodeBox can launch the tool), SystemPATHOk reflects the host shell's
	// visibility.
	SystemPATHOk bool `json:"systemPathOk"`

	// PathState describes how the executable path was resolved.
	PathState PathState `json:"pathState"`

	// PathSource is a human-readable description of where the path came from
	// (e.g. "path-search", "fallback", "ambient-path").
	PathSource string `json:"pathSource"`

	// Issues contains structured problem descriptions for the frontend.
	Issues []CheckIssue `json:"issues"`

	// Solutions contains actionable resolution steps.
	Solutions []ResolutionAction `json:"solutions"`

	// CanInstall is true when the service believes installation is possible
	// (e.g. npm is available).
	CanInstall bool `json:"canInstall"`

	// InstallBlockedReason is non-empty when CanInstall is false, explaining why.
	InstallBlockedReason string `json:"installBlockedReason"`
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

// ---------------------------------------------------------------------------
// Async operation types for long-running install/update tasks
// ---------------------------------------------------------------------------

// OperationKind distinguishes install from update operations.
type OperationKind string

const (
	OperationKindInstall OperationKind = "install"
	OperationKindUpdate  OperationKind = "update"
)

// OperationStatus represents the lifecycle state of an async operation.
type OperationStatus string

const (
	OperationStatusIdle      OperationStatus = "idle"
	OperationStatusRunning   OperationStatus = "running"
	OperationStatusSucceeded OperationStatus = "succeeded"
	OperationStatusFailed    OperationStatus = "failed"
	OperationStatusTimeout   OperationStatus = "timeout"
)

// OperationStep represents the current phase within a running operation.
type OperationStep string

const (
	OperationStepPrecheck     OperationStep = "precheck"
	OperationStepPrepare      OperationStep = "prepare"
	OperationStepRunCommand   OperationStep = "run_command"
	OperationStepVerify       OperationStep = "verify"
	OperationStepRefreshCache OperationStep = "refresh_cache"
	OperationStepCompleted    OperationStep = "completed"
)

// OperationState holds the full state of a single async install/update operation.
// It is safe to serialize to JSON and send to the frontend.
type OperationState struct {
	ID             string          `json:"id"`
	Tool           CLITool         `json:"tool"`
	Kind           OperationKind   `json:"kind"`
	Status         OperationStatus `json:"status"`
	Step           OperationStep   `json:"step"`
	Message        string          `json:"message"`
	Progress       int             `json:"progress"`
	StartedAt      time.Time       `json:"startedAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	FinishedAt     *time.Time      `json:"finishedAt"`
	Result         *InstallResult  `json:"result"`
	Error          string          `json:"error"`
	CacheRefreshed bool            `json:"cacheRefreshed"`
}
