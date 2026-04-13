package workspace

import "amagi-codebox/internal/plugin"

type ToolType string

const (
	ToolTypeClaude   ToolType = "claude"
	ToolTypeOpenCode ToolType = "opencode"
	ToolTypeCursor   ToolType = "cursor"
	ToolTypeVSCode   ToolType = "vscode"
)

type MergeType string

const (
	MergeTypeExclusive MergeType = "exclusive"
	MergeTypeMerged    MergeType = "merged"
)

type DeploymentStatus string

const (
	DeploymentStatusActive   DeploymentStatus = "active"
	DeploymentStatusOrphaned DeploymentStatus = "orphaned"
)

type SourceScope string

const (
	SourceScopeWorkspace SourceScope = "workspace"
	SourceScopeGlobal    SourceScope = "global"
)

type Workspace struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Path      string            `json:"path"`
	Tools     []ToolType        `json:"tools"`
	Plugins   []WorkspacePlugin `json:"plugins"`
	CreatedAt string            `json:"createdAt"`
	UpdatedAt string            `json:"updatedAt"`
}

type WorkspacePlugin struct {
	PluginID        string              `json:"pluginId"`
	EnabledSubItems []plugin.SubItemRef `json:"enabledSubItems"`
	DeployScope     string              `json:"deployScope"`
}

type GlobalEnabled struct {
	PluginID        string              `json:"pluginId"`
	EnabledAll      bool                `json:"enabledAll"`
	EnabledSubItems []plugin.SubItemRef `json:"enabledSubItems"`
	Tools           []ToolType          `json:"tools"`
	DeployedAt      string              `json:"deployedAt"`
}

type DeploymentManifest struct {
	Version     string            `json:"version"`
	GeneratedAt string            `json:"generatedAt"`
	Entries     []DeploymentEntry `json:"entries"`
}

type DeploymentEntry struct {
	PluginID      string            `json:"pluginId"`
	PluginVersion string            `json:"pluginVersion"`
	SubItemRef    plugin.SubItemRef `json:"subItemRef"`
	TargetPath    string            `json:"targetPath"`
	MergeType     MergeType         `json:"mergeType"`
	Status        DeploymentStatus  `json:"status"`
	Checksum      string            `json:"checksum"`
	DeployedAt    string            `json:"deployedAt"`
	ContentMarker string            `json:"contentMarker,omitempty"`
	MergeOrder    int               `json:"mergeOrder,omitempty"`
	SourceScope   SourceScope       `json:"sourceScope"`
}

type ConflictType string

const (
	ConflictTypeTargetPath   ConflictType = "target_path"
	ConflictTypeUserFile     ConflictType = "user_file"
	ConflictTypeMCPKey       ConflictType = "mcp_key"
	ConflictTypeModifiedFile ConflictType = "modified_file"
)

type Conflict struct {
	Type       ConflictType `json:"type"`
	PluginID   string       `json:"pluginId,omitempty"`
	TargetPath string       `json:"targetPath,omitempty"`
	Message    string       `json:"message"`
	Blocking   bool         `json:"blocking"`
}

type DeployResult struct {
	TargetID  string             `json:"targetId"`
	Warnings  []string           `json:"warnings"`
	Conflicts []Conflict         `json:"conflicts"`
	Manifest  DeploymentManifest `json:"manifest"`
	Deployed  []DeploymentEntry  `json:"deployed"`
	Removed   []string           `json:"removed"`
}

type SyncResult = DeployResult

type CleanResult struct {
	TargetID string             `json:"targetId"`
	Warnings []string           `json:"warnings"`
	Manifest DeploymentManifest `json:"manifest"`
	Removed  []string           `json:"removed"`
}

type AvailablePlugin struct {
	plugin.PluginDetail
	GloballyEnabledAll bool `json:"globallyEnabledAll"`
}

type workspacesFile struct {
	Workspaces []Workspace `json:"workspaces"`
}

type globalEnabledFile struct {
	Entries []GlobalEnabled `json:"entries"`
}
