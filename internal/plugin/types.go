package plugin

// Marketplace represents a registered plugin marketplace.
type Marketplace struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Repo            string `json:"repo,omitempty"`
	URL             string `json:"url,omitempty"`
	InstallLocation string `json:"installLocation"`
	LastUpdated     string `json:"lastUpdated,omitempty"`
	AutoUpdate      bool   `json:"autoUpdate,omitempty"`
}

// InstalledPlugin represents a plugin from installed_plugins.json.
type InstalledPlugin struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Marketplace  string `json:"marketplace"`
	Version      string `json:"version"`
	Scope        string `json:"scope"`
	Enabled      bool   `json:"enabled"`
	InstallPath  string `json:"installPath"`
	InstalledAt  string `json:"installedAt"`
	LastUpdated  string `json:"lastUpdated"`
	GitCommitSha string `json:"gitCommitSha,omitempty"`
}

// PluginManifest from .claude-plugin/plugin.json.
type PluginManifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      map[string]string `json:"author,omitempty"`
	License     string            `json:"license,omitempty"`
	Keywords    []string          `json:"keywords,omitempty"`
	Homepage    string            `json:"homepage,omitempty"`
	Repository  string            `json:"repository,omitempty"`
}

// PluginDetail contains full plugin info including components.
type PluginDetail struct {
	InstalledPlugin
	Manifest   PluginManifest         `json:"manifest"`
	Skills     []SkillInfo            `json:"skills"`
	Agents     []AgentInfo            `json:"agents"`
	Commands   []CommandInfo          `json:"commands"`
	Hooks      []HookInfo             `json:"hooks"`
	HasMCP     bool                   `json:"hasMcp"`
	MCPServers map[string]interface{} `json:"mcpServers,omitempty"`
}

// SkillInfo represents a plugin skill.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilePath    string `json:"filePath"`
}

// AgentInfo represents a plugin agent.
type AgentInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilePath    string `json:"filePath"`
}

// CommandInfo represents a plugin command.
type CommandInfo struct {
	Name     string `json:"name"`
	FilePath string `json:"filePath"`
}

// HookInfo represents a hook event.
type HookInfo struct {
	Event   string `json:"event"`
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
}

// CommandResult for CLI execution results.
type CommandResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

type installedPluginsFile struct {
	Version int                               `json:"version"`
	Plugins map[string][]installedPluginEntry `json:"plugins"`
}

type installedPluginEntry struct {
	Scope        string `json:"scope"`
	InstallPath  string `json:"installPath"`
	Version      string `json:"version"`
	InstalledAt  string `json:"installedAt"`
	LastUpdated  string `json:"lastUpdated"`
	GitCommitSha string `json:"gitCommitSha,omitempty"`
}

type marketplaceFileEntry struct {
	Source          marketplaceSource `json:"source"`
	InstallLocation string            `json:"installLocation"`
	LastUpdated     string            `json:"lastUpdated,omitempty"`
	AutoUpdate      bool              `json:"autoUpdate,omitempty"`
}

type marketplaceSource struct {
	Source string `json:"source"`
	Repo   string `json:"repo,omitempty"`
	URL    string `json:"url,omitempty"`
}

type settingsFile struct {
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
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

type cliInstalledPlugin struct {
	ID           string                 `json:"id"`
	Version      string                 `json:"version"`
	Scope        string                 `json:"scope"`
	Enabled      bool                   `json:"enabled"`
	InstallPath  string                 `json:"installPath"`
	InstalledAt  string                 `json:"installedAt"`
	LastUpdated  string                 `json:"lastUpdated"`
	GitCommitSha string                 `json:"gitCommitSha,omitempty"`
	MCPServers   map[string]interface{} `json:"mcpServers,omitempty"`
}

type availablePluginsEnvelope struct {
	Installed []interface{} `json:"installed"`
	Available []interface{} `json:"available"`
}
