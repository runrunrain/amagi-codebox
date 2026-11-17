// Package sharedext 实现扩展管理页的「共用」中心后端：跨 CLI 共享的
// 公共 skill 与公共 MCP 管理（仓库存储、导入、删除、启停、按需分发到
// 各 CLI 引擎、以及手动条目与插件来源条目的聚合视图）。
//
// 设计要点（任务书已确认决策）：
//   - 分发方式 = 复制 + 部署清单（不用 symlink）；registry 的 Deployed
//     字段是回收的唯一依据；
//   - 每条目按需勾选目标 CLI（targets），启用时分发、禁用时回收；
//   - 插件来源 skill 不入本仓库，聚合时由调用方（P2 app.go）提供
//     providers 动态合并；删除仅限插件处，本包对 plugin 条目只读。
package sharedext

// Engine 引擎标识（同时是 targets/deployed 的取值）。
type Engine string

const (
	EngineClaude   Engine = "claude"
	EngineCodex    Engine = "codex"
	EngineOpenCode Engine = "opencode"
	EnginePi       Engine = "pi"
	EngineOmp      Engine = "omp"
)

// AllEngines 按固定顺序返回全部受支持引擎。
func AllEngines() []Engine {
	return []Engine{EngineClaude, EngineCodex, EngineOpenCode, EnginePi, EngineOmp}
}

// ValidEngine 报告 e 是否为受支持的引擎标识。
func ValidEngine(e Engine) bool {
	switch e {
	case EngineClaude, EngineCodex, EngineOpenCode, EnginePi, EngineOmp:
		return true
	default:
		return false
	}
}

// SharedSkill 仓库中的一条公共 skill。
// Missing 由 ListSharedSkills 动态计算（SourcePath 失踪时 true），不落盘。
type SharedSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	SourcePath  string   `json:"sourcePath"`           // 仓库内路径 shared/skills/<name>
	Origin      string   `json:"origin"`               // "manual"（插件来源不入此表，聚合时动态合并）
	Enabled     bool     `json:"enabled"`
	Targets     []Engine `json:"targets"`              // 按需勾选，默认空
	Deployed    []Engine `json:"deployed"`             // 实际已部署清单（回收依据）
	DeployedAt  string   `json:"deployedAt,omitempty"`
	CreatedAt   string   `json:"createdAt"`
	Missing     bool     `json:"missing,omitempty"`    // 仅 List 输出时填充
}

// SharedMCPServer 仓库中的一条公共 MCP server 定义。
type SharedMCPServer struct {
	Name      string         `json:"name"`
	Config    map[string]any `json:"config"`   // 单个 server 定义（command/args/env/url...）
	Origin    string         `json:"origin"`   // "manual"
	Enabled   bool           `json:"enabled"`
	Targets   []Engine       `json:"targets"`
	Deployed  []Engine       `json:"deployed"`
	CreatedAt string         `json:"createdAt"`
}

// PluginSkill 是 providers 提供的单个插件来源 skill 摘要。
type PluginSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PluginSkillsProvider 由调用方（P2 app.go）组装的插件 skill 来源。
// SubItemsDisabled 为该插件子项的用户级禁用记录，key 形如 "skill/<name>"。
type PluginSkillsProvider struct {
	Engine           Engine       `json:"engine"`
	PluginID         string       `json:"pluginId"`
	PluginName       string       `json:"pluginName"`
	Skills           []PluginSkill `json:"skills"`
	SubItemsDisabled map[string]bool `json:"subItemsDisabled,omitempty"`
}

// AggregatedSkill 聚合视图条目（手动 + 插件来源动态合并）。
type AggregatedSkill struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Origin       string `json:"origin"`               // "manual" | "plugin"
	PluginEngine Engine `json:"pluginEngine,omitempty"` // claude/codex/opencode/pi/omp
	PluginID     string `json:"pluginId,omitempty"`
	PluginName   string `json:"pluginName,omitempty"`
	Managed      bool   `json:"managed"`               // manual=true（可删可启停）; plugin=false（只读，删除归插件）
	Enabled      bool   `json:"enabled"`
}
