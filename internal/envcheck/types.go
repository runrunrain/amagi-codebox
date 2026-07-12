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
	ToolHeadroom   CLITool = "headroom"
)

// InstallMethod describes how a CLI tool was installed on the host.
type InstallMethod string

const (
	InstallMethodNative      InstallMethod = "native"
	InstallMethodNPM         InstallMethod = "npm"
	InstallMethodPip         InstallMethod = "pip"
	InstallMethodHomebrew    InstallMethod = "homebrew"
	InstallMethodCodeboxVenv InstallMethod = "codebox-venv"
	InstallMethodUnknown     InstallMethod = "unknown"
)

// ClaudeInstallMethod represents a user-selected installation method for Claude Code.
type ClaudeInstallMethod string

const (
	ClaudeInstallAuto   ClaudeInstallMethod = ""       // internal default path for generic installs
	ClaudeInstallNPM    ClaudeInstallMethod = "npm"    // npm global install
	ClaudeInstallNative ClaudeInstallMethod = "native" // npm global install followed by `claude install`
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

// Additional fix action types for Claude Code lifecycle management.
const (
	SolutionInstallClaudeMethod SolutionType = "install_claude_method" // install Claude Code via user-selected method
	SolutionCleanClaudeInstall  SolutionType = "clean_claude_install"  // remove Claude Code installation
	SolutionFixClaudeConfig     SolutionType = "fix_claude_config"     // fix a Claude Code configuration item
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

	// Method carries an explicit InstallMethod (e.g. "npm"/"native") that the
	// frontend should target when executing the action. When set, the frontend
	// MUST use this field instead of inferring the method from
	// CheckStatus.InstallMethod (which may be empty or stale). This eliminates
	// the risk of cleaning the wrong installation channel (e.g. wiping a
	// native install when only the npm residue was intended). See F-1 in the
	// 20260621 final review.
	Method InstallMethod `json:"method,omitempty"`
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
	// (e.g. npm is available for npm/native Claude Code methods).
	CanInstall bool `json:"canInstall"`

	// CanInstallByMethod reports whether each specific install method is available.
	// Keys are "npm" and "native" for Claude Code. Values indicate whether that method can be used.
	// Frontend should use this for per-method button enable/disable logic.
	CanInstallByMethod map[string]bool `json:"canInstallByMethod"`

	// InstallBlockedReason is non-empty when CanInstall is false, explaining why.
	InstallBlockedReason string `json:"installBlockedReason"`

	// Config contains the Claude Code configuration check results.
	// Only populated for ToolClaudeCode; nil for other tools.
	Config *ClaudeConfigStatus `json:"config,omitempty"`
}

// OverallStatus aggregates all supported CLI tool checks into one response.
type OverallStatus struct {
	AllOK     bool                   `json:"allOk"`
	Items     map[string]CheckStatus `json:"items"`
	Issues    []string               `json:"issues"`
	CheckedAt time.Time              `json:"checkedAt"`

	// Checking is true while a CheckAll run is in progress. The frontend uses
	// this to render an accurate "checking" state instead of relying on
	// CheckedAt presence + a timeout fallback, which can break on slow disks
	// where CheckAll exceeds the timeout. Set by Service.CheckAll around the
	// detection loop under s.mu. See F-4 in the 20260621 final review.
	Checking bool `json:"checking"`
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
	OperationKindInstall   OperationKind = "install"
	OperationKindUpdate    OperationKind = "update"
	OperationKindUninstall OperationKind = "uninstall"
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

// ---------------------------------------------------------------------------
// Claude Code configuration detection types
// ---------------------------------------------------------------------------

// ClaudeConfigItem describes a single Claude Code configuration item that
// can be detected and optionally configured.
type ClaudeConfigItem struct {
	Key          string `json:"key"`          // config item identifier, e.g. "API_TIMEOUT_MS"
	FilePath     string `json:"filePath"`     // owning config file path, e.g. "~/.claude/settings.json"
	Category     string `json:"category"`     // category: "api", "network", "security", "updates", "windows", "permissions"
	Required     bool   `json:"required"`     // whether this is a required configuration item
	Configured   bool   `json:"configured"`   // whether it is configured with a non-empty value
	CurrentValue string `json:"currentValue"` // sanitized current value; empty string means not configured
	Description  string `json:"description"`  // Chinese description
	DefaultValue string `json:"defaultValue"` // recommended default value
}

// ClaudeConfigStatus aggregates the configuration check results for Claude Code.
type ClaudeConfigStatus struct {
	ConfigItems     []ClaudeConfigItem `json:"configItems"`     // all detected items
	MissingRequired int                `json:"missingRequired"` // count of missing required items
	AllConfigured   bool               `json:"allConfigured"`   // whether all required items are configured
	Warnings        []string           `json:"warnings"`        // non-blocking warnings (e.g. JSON parse errors)
}

// ConfigFixRequest represents a request to fix a specific configuration item.
type ConfigFixRequest struct {
	Key      string `json:"key"`      // target config item identifier
	Value    string `json:"value"`    // value to set; empty string means use default
	FilePath string `json:"filePath"` // target config file path
}

// ConfigFixResult represents the result of a configuration fix operation.
type ConfigFixResult struct {
	Success       bool   `json:"success"`
	Message       string `json:"message"` // Chinese message
	Error         string `json:"error,omitempty"`
	BackupPath    string `json:"backupPath,omitempty"`    // backup file path
	Changed       bool   `json:"changed"`                 // whether an actual change was made
	PreviousValue string `json:"previousValue,omitempty"` // value before change (sanitized)
}
