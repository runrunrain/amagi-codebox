package plugin

import (
	"fmt"
	"path/filepath"
	"sort"
)

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

func (s *Service) GetPluginSubItemStates(pluginID string) (PluginSubItemState, error) {
	detail, err := s.GetPluginDetail(pluginID)
	if err != nil {
		return PluginSubItemState{}, err
	}
	allowed := make(map[string]SubItemRef, len(detail.SubItems))
	for _, item := range detail.SubItems {
		ref := SubItemRef{Type: item.Type, Name: item.Name}
		allowed[ref.Key()] = ref
	}
	disabledRefs, err := s.readDisabledSubItems(pluginID)
	if err != nil {
		return PluginSubItemState{}, err
	}
	filtered := make([]SubItemRef, 0, len(disabledRefs))
	for _, ref := range disabledRefs {
		if _, ok := allowed[ref.Key()]; ok {
			filtered = append(filtered, ref)
		}
	}
	return PluginSubItemState{PluginID: pluginID, DisabledSubItems: normalizeSubItemRefs(filtered)}, nil
}

func (s *Service) SetSubItemEnabled(pluginID string, subItemRef SubItemRef, enabled bool) error {
	detail, err := s.GetPluginDetail(pluginID)
	if err != nil {
		return err
	}
	if !detail.hasSubItem(subItemRef) {
		return fmt.Errorf("plugin %s does not contain subitem %s", pluginID, subItemRef.Key())
	}
	states, err := s.readSubItemStateFile()
	if err != nil {
		return err
	}
	states = upsertSubItemState(states, pluginID, subItemRef, enabled)
	return s.writeSubItemStateFile(states)
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

func buildSubItems(detail *PluginDetail, disabledRefs []SubItemRef) []SubItem {
	disabled := make(map[string]struct{}, len(disabledRefs))
	for _, ref := range disabledRefs {
		disabled[ref.Key()] = struct{}{}
	}
	subItems := make([]SubItem, 0, len(detail.Skills)+len(detail.Agents)+len(detail.Commands)+len(detail.Hooks)+len(detail.MCPServers))
	for _, skill := range detail.Skills {
		ref := SubItemRef{Type: SubItemTypeSkill, Name: skill.Name}
		_, off := disabled[ref.Key()]
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: relativePluginPath(detail.InstallPath, skill.FilePath), Enabled: !off, Selectable: !off})
	}
	for _, agent := range detail.Agents {
		ref := SubItemRef{Type: SubItemTypeAgent, Name: agent.Name}
		_, off := disabled[ref.Key()]
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: relativePluginPath(detail.InstallPath, agent.FilePath), Enabled: !off, Selectable: !off})
	}
	for _, command := range detail.Commands {
		ref := SubItemRef{Type: SubItemTypeCommand, Name: command.Name}
		_, off := disabled[ref.Key()]
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: relativePluginPath(detail.InstallPath, command.FilePath), Enabled: !off, Selectable: !off})
	}
	for _, hook := range detail.Hooks {
		ref := SubItemRef{Type: SubItemTypeHook, Name: hook.Name}
		_, off := disabled[ref.Key()]
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: filepath.ToSlash(filepath.Join("hooks", "hooks.json")), Enabled: !off, Selectable: !off})
	}
	mcpNames := make([]string, 0, len(detail.MCPServers))
	for name := range detail.MCPServers {
		mcpNames = append(mcpNames, name)
	}
	sort.Strings(mcpNames)
	for _, name := range mcpNames {
		ref := SubItemRef{Type: SubItemTypeMCP, Name: name}
		_, off := disabled[ref.Key()]
		subItems = append(subItems, SubItem{Type: ref.Type, Name: ref.Name, Path: ".mcp.json", Enabled: !off, Selectable: !off})
	}
	sort.Slice(subItems, func(i, j int) bool {
		if subItems[i].Type == subItems[j].Type {
			return subItems[i].Name < subItems[j].Name
		}
		return subItems[i].Type < subItems[j].Type
	})
	return subItems
}

func relativePluginPath(installPath, filePath string) string {
	relPath, err := filepath.Rel(installPath, filePath)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	return filepath.ToSlash(relPath)
}

func (d *PluginDetail) hasSubItem(ref SubItemRef) bool {
	for _, item := range d.SubItems {
		if item.Type == ref.Type && item.Name == ref.Name {
			return true
		}
	}
	return false
}
