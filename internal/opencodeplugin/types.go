package opencodeplugin

// Plugin represents one globally configured OpenCode plugin.
type Plugin struct {
	ID           string   `json:"id"`
	Spec         string   `json:"spec"`
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Description  string   `json:"description,omitempty"`
	Author       string   `json:"author,omitempty"`
	Repository   string   `json:"repository,omitempty"`
	Source       string   `json:"source"`
	Scope        string   `json:"scope"`
	Enabled      bool     `json:"enabled"`
	InstallPath  string   `json:"installPath,omitempty"`
	ManifestPath string   `json:"manifestPath,omitempty"`
	LastUpdated  string   `json:"lastUpdated,omitempty"`
	Targets      []string `json:"targets"`
}

// ResourceInfo describes a static resource shipped in a plugin package.
type ResourceInfo struct {
	Name     string `json:"name"`
	FilePath string `json:"filePath"`
}

// PluginDetail contains package metadata and discoverable OpenCode assets.
type PluginDetail struct {
	Plugin
	Skills   []ResourceInfo `json:"skills"`
	Agents   []ResourceInfo `json:"agents"`
	Commands []ResourceInfo `json:"commands"`
	Hooks    []ResourceInfo `json:"hooks"`
	HasMCP   bool           `json:"hasMcp"`
}

// PluginsData is the aggregate response used by the frontend refresh action.
type PluginsData struct {
	Installed []Plugin `json:"installed"`
	Warnings  []string `json:"warnings,omitempty"`
}

// CommandResult reports an OpenCode CLI or config mutation result.
type CommandResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}
