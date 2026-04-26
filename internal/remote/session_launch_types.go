package remote

type launchProviderOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	DefaultModel string `json:"defaultModel,omitempty"`
}

type launchPresetOption struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Source   string `json:"source,omitempty"`
}

type launchOpenCodePresetOption struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Description  string `json:"description,omitempty"`
	BindingCount int    `json:"bindingCount,omitempty"`
	Source       string `json:"source,omitempty"`
}

type launchAmagiGroupOption struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	Description    string `json:"description,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	DefaultPreset  string `json:"defaultPreset,omitempty"`
	SubPresetCount int    `json:"subPresetCount"`
}

type launchMetaSection struct {
	Providers []launchProviderOption `json:"providers"`
	Presets   []launchPresetOption   `json:"presets"`
}

type launchMetaOpenCodeSection struct {
	Providers []launchProviderOption       `json:"providers"`
	Presets   []launchOpenCodePresetOption `json:"presets"`
}

type launchMetaAmagiSection struct {
	Providers []launchProviderOption   `json:"providers"`
	Groups    []launchAmagiGroupOption `json:"groups"`
}

type launchMetadataResponse struct {
	Paths     []string                  `json:"paths"`
	Claude    launchMetaSection         `json:"claude"`
	OpenCode  launchMetaOpenCodeSection `json:"opencode"`
	Codex     launchMetaSection         `json:"codex"`
	AmagiCode launchMetaAmagiSection    `json:"amagicode"`
}

type launchAmagiRequest struct {
	GroupName    string `json:"groupName"`
	ProviderName string `json:"providerName"`
	Mode         string `json:"mode"`
	WorkDir      string `json:"workDir"`
	ShellPath    string `json:"shellPath"`
}
