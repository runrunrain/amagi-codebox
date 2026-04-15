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

type claudeAdapter struct{ homeDir string }
type cursorAdapter struct{ homeDir string }
type openCodeAdapter struct{ homeDir string }
type vscodeAdapter struct{ homeDir string }
type boundaryAdapter struct{ tool ToolType }

func (a claudeAdapter) ToolType() ToolType { return ToolTypeClaude }
func (a claudeAdapter) SupportsType(itemType plugin.SubItemType) SupportLevel {
	switch itemType {
	case plugin.SubItemTypeSkill, plugin.SubItemTypeHook, plugin.SubItemTypeCommand, plugin.SubItemTypeAgent, plugin.SubItemTypeMCP, plugin.SubItemTypeClaude:
		return SupportLevelNative
	default:
		return SupportLevelUnsupported
	}
}
func (a claudeAdapter) GlobalRoot() string                       { return a.homeDir }
func (a claudeAdapter) WorkspaceRoot(workspaceDir string) string { return workspaceDir }

func (a cursorAdapter) ToolType() ToolType { return ToolTypeCursor }
func (a cursorAdapter) SupportsType(itemType plugin.SubItemType) SupportLevel {
	switch itemType {
	case plugin.SubItemTypeClaude, plugin.SubItemTypeMCP:
		return SupportLevelAdapted
	default:
		return SupportLevelUnsupported
	}
}
func (a cursorAdapter) GlobalRoot() string                       { return a.homeDir }
func (a cursorAdapter) WorkspaceRoot(workspaceDir string) string { return workspaceDir }

func (a openCodeAdapter) ToolType() ToolType { return ToolTypeOpenCode }
func (a openCodeAdapter) SupportsType(itemType plugin.SubItemType) SupportLevel {
	switch itemType {
	case plugin.SubItemTypeClaude:
		return SupportLevelCompatible
	case plugin.SubItemTypeAgent, plugin.SubItemTypeMCP:
		return SupportLevelAdapted
	case plugin.SubItemTypeSkill:
		return SupportLevelCompatible
	case plugin.SubItemTypeCommand:
		return SupportLevelUnsupported
	case plugin.SubItemTypeHook:
		return SupportLevelUnsupported
	default:
		return SupportLevelUnsupported
	}
}
func (a openCodeAdapter) GlobalRoot() string                       { return a.homeDir }
func (a openCodeAdapter) WorkspaceRoot(workspaceDir string) string { return workspaceDir }

func (a vscodeAdapter) ToolType() ToolType { return ToolTypeVSCode }
func (a vscodeAdapter) SupportsType(itemType plugin.SubItemType) SupportLevel {
	switch itemType {
	case plugin.SubItemTypeClaude:
		return SupportLevelCompatible
	case plugin.SubItemTypeMCP:
		return SupportLevelExperimental
	default:
		return SupportLevelUnsupported
	}
}
func (a vscodeAdapter) GlobalRoot() string                       { return a.homeDir }
func (a vscodeAdapter) WorkspaceRoot(workspaceDir string) string { return workspaceDir }

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
	case ToolTypeCursor:
		return cursorAdapter{homeDir: homeDir}
	case ToolTypeOpenCode:
		return openCodeAdapter{homeDir: homeDir}
	case ToolTypeVSCode:
		return vscodeAdapter{homeDir: homeDir}
	default:
		return boundaryAdapter{tool: tool}
	}
}

func globalRelativeRoot(tool ToolType) string {
	switch tool {
	case ToolTypeClaude:
		return ".claude"
	case ToolTypeCursor:
		return ".cursor"
	case ToolTypeOpenCode:
		return filepath.Join(".config", "opencode")
	default:
		return ""
	}
}
