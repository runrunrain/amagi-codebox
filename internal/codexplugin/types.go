package codexplugin

// PluginType 表示 Codex 插件的自动分析类型。
type PluginType string

const (
	PluginTypeUnknown     PluginType = "unknown"
	PluginTypeIntegration PluginType = "integration"
	PluginTypeHybrid      PluginType = "hybrid"
	PluginTypeSkill       PluginType = "skill"
	PluginTypeHook        PluginType = "hook"
	PluginTypeAgent       PluginType = "agent"
	PluginTypeCommand     PluginType = "command"
	PluginTypeMCP         PluginType = "mcp"
)

// AddMarketplaceRequest 是 Codex marketplace add 的 Wails 入参。
type AddMarketplaceRequest struct {
	Source string `json:"source"`
}

// PluginSelector 标识一个 Codex 插件。PluginID 优先，未传时由 Name 和 Marketplace 组合。
type PluginSelector struct {
	PluginID    string `json:"pluginId"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Marketplace string `json:"marketplace,omitempty"`
}

// CodexMarketplace represents a registered Codex plugin marketplace.
type CodexMarketplace struct {
	Name            string `json:"name"`
	Source          string `json:"source,omitempty"`
	Repo            string `json:"repo,omitempty"`
	URL             string `json:"url,omitempty"`
	InstallLocation string `json:"installLocation,omitempty"`
	SnapshotPath    string `json:"snapshotPath,omitempty"`
	LastUpdated     string `json:"lastUpdated,omitempty"`
	RawLine         string `json:"rawLine,omitempty"`
}

// CodexPlugin represents an installed Codex plugin.
type CodexPlugin struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Marketplace  string `json:"marketplace"`
	Version      string `json:"version,omitempty"`
	Enabled      bool   `json:"enabled"`
	InstallPath  string `json:"installPath,omitempty"`
	ManifestPath string `json:"manifestPath,omitempty"`
	InstalledAt  string `json:"installedAt,omitempty"`
	LastUpdated  string `json:"lastUpdated,omitempty"`
	Source       string `json:"source,omitempty"`
}

// CodexAvailablePlugin represents a plugin discovered from a marketplace snapshot.
type CodexAvailablePlugin struct {
	PluginID        string `json:"pluginId"`
	Name            string `json:"name"`
	MarketplaceName string `json:"marketplaceName"`
	Version         string `json:"version,omitempty"`
	Description     string `json:"description,omitempty"`
	Author          string `json:"author,omitempty"`
	Repository      string `json:"repository,omitempty"`
	SnapshotPath    string `json:"snapshotPath,omitempty"`
	ManifestPath    string `json:"manifestPath,omitempty"`
}

// CodexPluginManifest describes a Codex plugin manifest.
type CodexPluginManifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      map[string]string `json:"author,omitempty"`
	License     string            `json:"license,omitempty"`
	Keywords    []string          `json:"keywords,omitempty"`
	Homepage    string            `json:"homepage,omitempty"`
	Repository  string            `json:"repository,omitempty"`
}

// CodexPluginDetail contains full plugin info including local components.
type CodexPluginDetail struct {
	CodexPlugin
	Manifest   CodexPluginManifest    `json:"manifest"`
	Skills     []SkillInfo            `json:"skills"`
	Agents     []AgentInfo            `json:"agents"`
	Commands   []CommandInfo          `json:"commands"`
	Hooks      []HookInfo             `json:"hooks"`
	HasMCP     bool                   `json:"hasMcp"`
	MCPServers map[string]interface{} `json:"mcpServers,omitempty"`
	PluginType PluginType             `json:"pluginType"`
}

// CodexPluginsData is an aggregate response for refresh operations.
type CodexPluginsData struct {
	Marketplaces []CodexMarketplace     `json:"marketplaces"`
	Installed    []CodexPlugin          `json:"installed"`
	Available    []CodexAvailablePlugin `json:"available"`
	Warnings     []string               `json:"warnings,omitempty"`
}

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilePath    string `json:"filePath"`
}

type AgentInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilePath    string `json:"filePath"`
}

type CommandInfo struct {
	Name     string `json:"name"`
	FilePath string `json:"filePath"`
}

type HookInfo struct {
	Name     string `json:"name"`
	Event    string `json:"event"`
	Type     string `json:"type"`
	Command  string `json:"command,omitempty"`
	FilePath string `json:"filePath"`
}

// CommandResult for CLI execution results.
type CommandResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

type mcpConfigFile struct {
	MCPServers map[string]interface{} `json:"mcpServers"`
}

type hooksFile struct {
	Hooks map[string][]hookGroup `json:"hooks"`
}

type hookGroup struct {
	Hooks   []hookEntry `json:"hooks"`
	Type    string      `json:"type,omitempty"`
	Command string      `json:"command,omitempty"`
}

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
}
