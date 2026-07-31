package piplugin

// 本包管理 Pi coding agent 的插件/包（pi packages）。
//
// Pi 包 = npm 包或 git 仓库，安装记录写在
//   ~/.pi/agent/settings.json（或 $PI_CODING_AGENT_DIR/settings.json）
// 的顶层 packages 数组里；元素为字符串源（如 "npm:@foo/bar@1.0.0"）或
// 过滤对象（{source, extensions[], skills[], prompts[], themes[]}）。
// 实体落 <agentDir>/npm/node_modules/<name>（npm 源）或
// <agentDir>/git/<host>/<user>/<project>（git 源）；本地路径不拷贝。
//
// 读写策略（对标 internal/opencodeplugin）：
//   - 读操作（list / details）优先解析 settings.json + 扫描实体目录，避免 fork pi CLI；
//   - 写操作（install / remove / update）走 pi CLI（pi install / pi remove / pi update）。

// Package represents one pi package configured in settings.json.
//
// Package 表示 settings.json packages[] 中登记的一个 pi 包。
type Package struct {
	// ID 是去重/前端 key 用的稳定标识，取值与 Source 相同。
	ID string `json:"id"`
	// Source 是原始源字符串，如 "npm:@foo/bar@1.0.0" / "git:github.com/u/r@v1" / "/abs/path"。
	Source string `json:"source"`
	// SourceType 归类：npm / git / local。
	SourceType string `json:"sourceType"`
	// Name 是显示名，优先取 package.json 的 name，否则从 Source 推导。
	Name string `json:"name"`
	// Version 取自 package.json version。
	Version string `json:"version,omitempty"`
	// Description 取自 package.json description。
	Description string `json:"description,omitempty"`
	// Author 取自 package.json author。
	Author string `json:"author,omitempty"`
	// Repository 取自 package.json repository.url。
	Repository string `json:"repository,omitempty"`
	// Scope 固定 "user"——amagi 只管理用户级 settings.json（~/.pi/agent/settings.json），
	// 不触碰项目级 .pi/settings.json。
	Scope string `json:"scope"`
	// Enabled：packages[] 中存在即视为启用（pi 的禁用语义走 pi config，不在 packages[] 表达）。
	Enabled bool `json:"enabled"`
	// InstallPath 是实体落盘目录（npm/git 实体或本地路径）。
	InstallPath string `json:"installPath,omitempty"`
	// ManifestPath 是 package.json 路径。
	ManifestPath string `json:"manifestPath,omitempty"`
	// LastUpdated 取实体 package.json 的修改时间（RFC3339）。
	LastUpdated string `json:"lastUpdated,omitempty"`
	// Pinned：npm 带精确版本或 git 带 ref 时为 true（pi update 不会移动 pinned ref）。
	Pinned bool `json:"pinned,omitempty"`
	// 以下为过滤对象（packages[] 元素为对象时）携带的子资源过滤清单；
	// 元素为字符串源时这些字段为空（表示加载该类型全部资源）。
	Extensions []string `json:"extensions,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Prompts    []string `json:"prompts,omitempty"`
	Themes     []string `json:"themes,omitempty"`
}

// ResourceInfo describes one discoverable resource inside a pi package.
//
// ResourceInfo 描述包内一个可发现的资源文件（extension/skill/prompt/theme）。
type ResourceInfo struct {
	Name     string `json:"name"`
	FilePath string `json:"filePath"`
	// Type 取值：extension / skill / prompt / theme。
	Type string `json:"type"`
}

// PackageDetail contains package metadata plus the discoverable pi resources.
//
// PackageDetail 含包元数据 + pi 能发现的子资源清单。
type PackageDetail struct {
	Package
	// Resources 是扫描到的 extensions/skills/prompts/themes 文件清单。
	Resources []ResourceInfo `json:"resources"`
	// ManifestDeclared 标记 package.json 是否声明了 pi manifest（pi 键）。
	ManifestDeclared bool `json:"manifestDeclared"`
}

// PackagesData is the aggregate refresh response used by the frontend.
//
// PackagesData 是前端刷新动作的聚合返回。
type PackagesData struct {
	Installed []Package `json:"installed"`
	// Warnings 列出"已配置但实体目录缺失"等提示。
	Warnings []string `json:"warnings,omitempty"`
}

// CommandResult reports a pi CLI or settings mutation result.
//
// CommandResult 报告一次 pi CLI 调用或 settings.json 变更的结果。
type CommandResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}
