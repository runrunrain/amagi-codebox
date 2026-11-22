package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

var subItemTypeOrder = map[SubItemType]int{
	SubItemTypeSkill:   0,
	SubItemTypeAgent:   1,
	SubItemTypeCommand: 2,
	SubItemTypeHook:    3,
	SubItemTypeMCP:     4,
	SubItemTypeClaude:  5,
}

func (s *Service) AnalyzePluginType(pluginID string) (PluginType, error) {
	detail, err := s.GetPluginDetail(pluginID)
	if err != nil {
		return PluginTypeUnknown, err
	}
	return detail.PluginType, nil
}

func (s *Service) GetPluginSubItems(pluginID string) ([]SubItem, error) {
	detail, err := s.GetPluginDetail(pluginID)
	if err != nil {
		return nil, err
	}
	out := make([]SubItem, len(detail.SubItems))
	copy(out, detail.SubItems)
	return out, nil
}

// GetPluginSubItemStates 返回插件子项的禁用状态投影（与详情一致，F-05：
// 从 GetPluginDetail 的最终子项列表投影 Enabled=false 的条目，孤儿 stash
//（同名新文件已在位）不会像直接读 stash 那样给出与详情相反的状态）。
func (s *Service) GetPluginSubItemStates(pluginID string) (PluginSubItemState, error) {
	detail, err := s.GetPluginDetail(pluginID)
	if err != nil {
		return PluginSubItemState{}, err
	}
	refs := make([]SubItemRef, 0, len(detail.SubItems))
	for _, item := range detail.SubItems {
		if !item.Enabled {
			refs = append(refs, SubItemRef{Type: item.Type, Name: item.Name})
		}
	}
	return PluginSubItemState{PluginID: pluginID, DisabledSubItems: normalizeSubItemRefs(refs)}, nil
}

// SetSubItemEnabled 设置插件子项启用/禁用状态（真实生效，2026-11 重构）：
// skill/command/agent = 文件在 installPath ⇄ stash 之间搬移（Claude 扫描不到
// stash 路径即真实不加载），搬移以扫描所得物理相对路径定位（展示名仅作展示，
// F-01）；mcp = .mcp.json 条目移出/写回（读不到即不挂载；新版定义在位时不
// 覆盖，F-02）；hook 暂不支持单独启停（hooks.json 无稳定条目标识），明确报错。
// 私有记录 plugin-subitems.json 同步更新（对账与诊断；Enabled 读取不再依赖它）。
func (s *Service) SetSubItemEnabled(pluginID string, subItemRef SubItemRef, enabled bool) error {
	detail, err := s.GetPluginDetail(pluginID)
	if err != nil {
		return err
	}
	var target *SubItem
	for i := range detail.SubItems {
		if detail.SubItems[i].Type == subItemRef.Type && detail.SubItems[i].Name == subItemRef.Name {
			target = &detail.SubItems[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("plugin %s does not contain subitem %s", pluginID, subItemRef.Key())
	}
	if !target.Selectable {
		return fmt.Errorf("子项 %s 不支持单独启停", subItemRef.Key())
	}
	var physicalPath string
	if !target.Enabled {
		// 目标已在 stash（禁用态）：重复禁用幂等成功；启用走 stash 恢复路径
		//（无需物理相对路径——其 Path 指向 stash 绝对路径，不可用作搬移源）
		if !enabled {
			return nil
		}
	} else if !enabled {
		// 禁用现存（启用态）子项：物理相对路径必填且必须是相对路径
		//（防止把 stash 绝对路径等异常值拼进 installPath）
		if target.Path == "" || filepath.IsAbs(target.Path) {
			return fmt.Errorf("子项 %s 缺少可用的物理相对路径，已拒绝操作", subItemRef.Key())
		}
		physicalPath = target.Path
	}
	// 整段持锁：串行化文件搬移与 .mcp.json 编辑，与 Uninstall/Update 互斥
	//（F-04）；锁内重验插件仍在注册表且安装路径未变（防卸载/更新交错后向
	// 已消失的安装目录恢复孤立文件）。
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyInstalledForSubItemOp(pluginID, detail.InstallPath); err != nil {
		return err
	}

	switch subItemRef.Type {
	case SubItemTypeSkill, SubItemTypeCommand, SubItemTypeAgent:
		if enabled {
			err = s.restoreItemFromStash(detail.InstallPath, pluginID, subItemRef)
		} else {
			err = s.moveItemToStash(detail.InstallPath, pluginID, subItemRef, physicalPath, target.Name)
		}
	case SubItemTypeMCP:
		if enabled {
			err = s.enableMCPServer(detail.InstallPath, pluginID, subItemRef.Name)
		} else {
			err = s.disableMCPServer(detail.InstallPath, pluginID, subItemRef.Name)
		}
	default:
		return fmt.Errorf("不支持的子项类型 %q", subItemRef.Type)
	}
	if err != nil {
		return err
	}

	states, err := s.readSubItemStateFile()
	if err != nil {
		return err
	}
	states = upsertSubItemState(states, pluginID, subItemRef, enabled)
	return s.writeSubItemStateFile(states)
}

// verifyInstalledForSubItemOp 在子项写操作锁内重验安装身份（F-04）：
// 插件仍在注册表、最新条目的 installPath 与详情快照一致（update 换版本目录
// 后不一致 → 拒绝并提示重试）。文件注册表直读，无 CLI 依赖。
func (s *Service) verifyInstalledForSubItemOp(pluginID, expectInstallPath string) error {
	plugins, err := s.readInstalledPluginsFile()
	if err != nil {
		return fmt.Errorf("无法确认插件 %s 的安装状态（%v），请重试", pluginID, err)
	}
	for i := range plugins {
		if plugins[i].ID != pluginID {
			continue
		}
		if plugins[i].InstallPath != expectInstallPath {
			return fmt.Errorf("插件 %s 的安装目录已变化（可能刚更新，%s ≠ %s），请刷新后重试", pluginID, plugins[i].InstallPath, expectInstallPath)
		}
		if _, statErr := os.Lstat(expectInstallPath); statErr != nil {
			return fmt.Errorf("插件 %s 的安装目录已不存在（可能已卸载）：%v", pluginID, statErr)
		}
		return nil
	}
	return fmt.Errorf("插件 %s 已不在注册表（可能已卸载），请刷新后重试", pluginID)
}

// SetPluginSubItemEnabled 设置插件子项启用/禁用状态（字符串参数版本，供 Wails 绑定）
func (s *Service) SetPluginSubItemEnabled(pluginId string, subItemType string, subItemId string, enabled bool) error {
	ref := SubItemRef{Type: SubItemType(subItemType), Name: subItemId}
	return s.SetSubItemEnabled(pluginId, ref, enabled)
}

func analyzePluginType(detail *PluginDetail) PluginType {
	nonZeroTypes := 0
	singleType := PluginTypeUnknown
	counts := []struct {
		count int
		kind  PluginType
	}{
		{count: len(detail.Skills), kind: PluginTypeSkill},
		{count: len(detail.Hooks), kind: PluginTypeHook},
		{count: len(detail.Commands), kind: PluginTypeCommand},
		{count: len(detail.Agents), kind: PluginTypeAgent},
		{count: len(detail.MCPServers), kind: PluginTypeMCP},
	}
	for _, item := range counts {
		if item.count > 0 {
			nonZeroTypes++
			singleType = item.kind
		}
	}
	if nonZeroTypes >= 2 {
		if detail.HasClaudeMd {
			return PluginTypeIntegration
		}
		return PluginTypeHybrid
	}
	if nonZeroTypes == 1 {
		return singleType
	}
	return PluginTypeUnknown
}

// buildSubItems 组装子项列表（文件系统即真相，2026-11 重构）：
//   - installPath 现存子项（skills/agents/commands/hooks/mcp 扫描结果）→ 启用态；
//   - stash 子项 → 禁用态（Claude 扫描不到 stash 路径，真实不加载）；
//   - 与现存同 type+name 的 stash 条目为孤儿（plugin update 重装恢复），
//     新文件优先，不重复入列；孤儿 stash 副本在下次启用操作时弃置；
//   - hooks 无稳定条目标识，不支持单独启停 → Selectable=false（UI 不可切）。
func buildSubItems(detail *PluginDetail, stashed []SubItem) []SubItem {
	subItems := make([]SubItem, 0, len(detail.Skills)+len(detail.Agents)+len(detail.Commands)+len(detail.Hooks)+len(detail.MCPServers)+len(stashed))
	existing := make(map[string]bool, 32)
	for _, skill := range detail.Skills {
		ref := SubItemRef{Type: SubItemTypeSkill, Name: skill.Name}
		existing[ref.Key()] = true
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: relativePluginPath(detail.InstallPath, skill.FilePath), Enabled: true, Selectable: true})
	}
	for _, agent := range detail.Agents {
		ref := SubItemRef{Type: SubItemTypeAgent, Name: agent.Name}
		existing[ref.Key()] = true
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: relativePluginPath(detail.InstallPath, agent.FilePath), Enabled: true, Selectable: true})
	}
	for _, command := range detail.Commands {
		ref := SubItemRef{Type: SubItemTypeCommand, Name: command.Name}
		existing[ref.Key()] = true
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: relativePluginPath(detail.InstallPath, command.FilePath), Enabled: true, Selectable: true})
	}
	for _, hook := range detail.Hooks {
		ref := SubItemRef{Type: SubItemTypeHook, Name: hook.Name}
		existing[ref.Key()] = true
		// hooks.json 无稳定条目标识：不支持单独启停，行不可切换（如实，不静默）
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: filepath.ToSlash(filepath.Join("hooks", "hooks.json")), Enabled: true, Selectable: false})
	}
	mcpNames := make([]string, 0, len(detail.MCPServers))
	for name := range detail.MCPServers {
		mcpNames = append(mcpNames, name)
	}
	sort.Strings(mcpNames)
	for _, name := range mcpNames {
		ref := SubItemRef{Type: SubItemTypeMCP, Name: name}
		existing[ref.Key()] = true
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: ".mcp.json", Enabled: true, Selectable: true})
	}
	// stash 条目（禁用态）：孤儿（同名现存）跳过，新文件优先
	for _, item := range stashed {
		ref := SubItemRef{Type: item.Type, Name: item.Name}
		if existing[ref.Key()] {
			continue
		}
		subItems = append(subItems, item)
	}
	sort.Slice(subItems, func(i, j int) bool {
		if subItems[i].Type == subItems[j].Type {
			return subItems[i].Name < subItems[j].Name
		}
		return subItemTypeRank(subItems[i].Type) < subItemTypeRank(subItems[j].Type)
	})
	return subItems
}

func subItemTypeRank(itemType SubItemType) int {
	if rank, ok := subItemTypeOrder[itemType]; ok {
		return rank
	}
	return len(subItemTypeOrder)
}

func relativePluginPath(installPath, filePath string) string {
	relPath, err := filepath.Rel(installPath, filePath)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	return filepath.ToSlash(relPath)
}
