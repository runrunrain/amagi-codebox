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
	ID           string         `json:"id"`
	Tool         CLITool        `json:"tool"`
	Kind         OperationKind  `json:"kind"`
	Status       OperationStatus `json:"status"`
	Step         OperationStep  `json:"step"`
	Message      string         `json:"message"`
	Progress     int            `json:"progress"`
	StartedAt    time.Time      `json:"startedAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	FinishedAt   *time.Time     `json:"finishedAt"`
	Result       *InstallResult `json:"result"`
	Error        string         `json:"error"`
	CacheRefreshed bool         `json:"cacheRefreshed"`
}
