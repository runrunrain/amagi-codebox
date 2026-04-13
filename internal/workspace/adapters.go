package workspace

import (
	"amagi-codebox/internal/plugin"
	"path/filepath"
)

type SupportLevel string

const (
	SupportLevelNative       SupportLevel = "native"
	SupportLevelCompatible   SupportLevel = "compatible"
	SupportLevelAdapted      SupportLevel = "adapted"
	SupportLevelExperimental SupportLevel = "experimental"
	SupportLevelUnsupported  SupportLevel = "unsupported"
)

type ToolAdapter interface {
	ToolType() ToolType
	SupportsType(itemType plugin.SubItemType) SupportLevel
	GlobalRoot() string
	WorkspaceRoot(workspaceDir string) string
}

type claudeAdapter struct {
	homeDir string
}

func (a claudeAdapter) ToolType() ToolType { return ToolTypeClaude }
func (a claudeAdapter) SupportsType(itemType plugin.SubItemType) SupportLevel {
	switch itemType {
	case plugin.SubItemTypeSkill, plugin.SubItemTypeHook, plugin.SubItemTypeCommand, plugin.SubItemTypeAgent, plugin.SubItemTypeMCP, plugin.SubItemTypeClaude:
		return SupportLevelNative
	default:
		return SupportLevelUnsupported
	}
}
func (a claudeAdapter) GlobalRoot() string                       { return filepath.Join(a.homeDir, ".claude") }
func (a claudeAdapter) WorkspaceRoot(workspaceDir string) string { return workspaceDir }

type boundaryAdapter struct{ tool ToolType }

func (a boundaryAdapter) ToolType() ToolType { return a.tool }
func (a boundaryAdapter) SupportsType(plugin.SubItemType) SupportLevel {
	return SupportLevelUnsupported
}
func (a boundaryAdapter) GlobalRoot() string                       { return "" }
func (a boundaryAdapter) WorkspaceRoot(workspaceDir string) string { return workspaceDir }

func newAdapterForTool(tool ToolType, homeDir string) ToolAdapter {
	switch tool {
	case ToolTypeClaude:
		return claudeAdapter{homeDir: homeDir}
	case ToolTypeOpenCode, ToolTypeCursor, ToolTypeVSCode:
		return boundaryAdapter{tool: tool}
	default:
		return boundaryAdapter{tool: tool}
	}
}
