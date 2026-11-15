package sharedmcp

// engines.go — 引擎元数据与 OMP excluded 静态声明（spec §2.2）。
//
// OMP 无 MCP 配置面是**裁定不是待办**（表述沿用 GetWSLPluginTargetSupport
// 的口径）：仓库与本机均无 ompconfig 的 MCP 佐证，excluded 以常量表静态
// 声明，不写任何 OMP 文件。若未来 ompconfig 出现 MCP 面，按同口径升格。

import "strings"

// 引擎 ID 常量（状态库 Selected/Applied 的 key 域）。
const (
	engineIDClaude   = "claude"
	engineIDCodex    = "codex"
	engineIDOpenCode = "opencode"
	engineIDPi       = "pi"
	engineIDOMP      = "omp"
)

// engineDisplayName 返回面向用户的引擎名。
func engineDisplayName(id string) string {
	switch id {
	case engineIDClaude:
		return "Claude"
	case engineIDCodex:
		return "Codex"
	case engineIDOpenCode:
		return "OpenCode"
	case engineIDPi:
		return "Pi"
	case engineIDOMP:
		return "omp"
	}
	return id
}

// engineIDsCSV 返回四个真实引擎的 ID 列表（错误文案用）。
func engineIDsCSV() string {
	return strings.Join([]string{engineIDClaude, engineIDCodex, engineIDOpenCode, engineIDPi}, "/")
}

// ompExcludedDetail 是 omp 行的原因一句（spec §2.2 原文）。
const ompExcludedDetail = "omp（config.yml）无 MCP 配置面——仓库与本机均无佐证，excluded 是裁定不是待办"

// EngineMeta 是状态查询「每引擎」一行的元数据（支持面 / 目标路径 / 已托管
// 条目数）。
type EngineMeta struct {
	ID string `json:"id"`
	// Name 面向用户的引擎名。
	Name string `json:"name"`
	// Support 是支持面声明："ok"（可分发）| "excluded"（无 MCP 配置面，
	// 仅 omp）。
	Support string `json:"support"`
	// Detail 是支持面附注（omp 为裁定原因；其余为空或补充说明）。
	Detail string `json:"detail,omitempty"`
	// TargetPath 是该引擎的 MCP 配置文件路径（omp 为空）。
	TargetPath string `json:"targetPath,omitempty"`
	// ManagedCount 是当前由本应用托管（Applied）的条目数。
	ManagedCount int `json:"managedCount"`
}

// pathExposer 由写入器实现，暴露目标配置文件路径（仅用于展示）。
type pathExposer interface{ TargetPath() string }

// buildEnginesMeta 组装四引擎 + omp excluded 行。
func buildEnginesMeta(writers []EngineWriter, managedCounts map[string]int) []EngineMeta {
	out := make([]EngineMeta, 0, len(writers)+1)
	for _, w := range writers {
		meta := EngineMeta{ID: w.ID(), Name: engineDisplayName(w.ID()), Support: "ok", ManagedCount: managedCounts[w.ID()]}
		if pe, ok := w.(pathExposer); ok {
			meta.TargetPath = pe.TargetPath()
		}
		out = append(out, meta)
	}
	out = append(out, EngineMeta{
		ID:           engineIDOMP,
		Name:         engineDisplayName(engineIDOMP),
		Support:      "excluded",
		Detail:       ompExcludedDetail,
		ManagedCount: managedCounts[engineIDOMP],
	})
	return out
}
