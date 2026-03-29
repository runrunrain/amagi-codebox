package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Service) readInstalledPluginsFile() ([]InstalledPlugin, error) {
	path := filepath.Join(s.claudeDir, "plugins", "installed_plugins.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read installed plugins file: %w", err)
	}

	var raw installedPluginsFile
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse installed plugins file: %w", err)
	}

	enabledPlugins, err := s.readEnabledPlugins()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		enabledPlugins = map[string]bool{}
	}

	plugins := make([]InstalledPlugin, 0, len(raw.Plugins))
	for id, entries := range raw.Plugins {
		entry, ok := selectNewestInstalledEntry(entries)
		if !ok {
			continue
		}

		name, marketplace := splitPluginID(id)
		plugins = append(plugins, InstalledPlugin{
			ID:           id,
			Name:         name,
			Marketplace:  marketplace,
			Version:      entry.Version,
			Scope:        entry.Scope,
			Enabled:      enabledPlugins[id],
			InstallPath:  entry.InstallPath,
			InstalledAt:  entry.InstalledAt,
			LastUpdated:  entry.LastUpdated,
			GitCommitSha: entry.GitCommitSha,
		})
	}

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].ID < plugins[j].ID
	})

	return plugins, nil
}

func (s *Service) readMarketplacesFile() ([]Marketplace, error) {
	path := filepath.Join(s.claudeDir, "plugins", "known_marketplaces.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read marketplaces file: %w", err)
	}

	var raw map[string]marketplaceFileEntry
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse marketplaces file: %w", err)
	}

	marketplaces := make([]Marketplace, 0, len(raw))
	for name, entry := range raw {
		marketplaces = append(marketplaces, Marketplace{
			Name:            name,
			Source:          entry.Source.Source,
			Repo:            entry.Source.Repo,
			URL:             entry.Source.URL,
			InstallLocation: entry.InstallLocation,
			LastUpdated:     entry.LastUpdated,
			AutoUpdate:      entry.AutoUpdate,
		})
	}

	sort.Slice(marketplaces, func(i, j int) bool {
		return marketplaces[i].Name < marketplaces[j].Name
	})

	return marketplaces, nil
}

func (s *Service) readPluginManifest(installPath string) (PluginManifest, error) {
	path := filepath.Join(installPath, ".claude-plugin", "plugin.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return PluginManifest{}, fmt.Errorf("read plugin manifest: %w", err)
	}

	var manifest PluginManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return PluginManifest{}, fmt.Errorf("parse plugin manifest: %w", err)
	}

	return manifest, nil
}

func (s *Service) scanSkills(installPath string) ([]SkillInfo, error) {
	skillsDir := filepath.Join(installPath, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SkillInfo{}, nil
		}
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	skills := make([]SkillInfo, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		filePath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		content, err := os.ReadFile(filePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read skill file %s: %w", filePath, err)
		}

		meta := parseFrontmatter(string(content))
		name := firstNonEmpty(meta["name"], entry.Name())
		description := firstNonEmpty(meta["description"], extractFirstParagraph(string(content)))

		skills = append(skills, SkillInfo{
			Name:        name,
			Description: description,
			FilePath:    filePath,
		})
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return skills, nil
}

func (s *Service) scanAgents(installPath string) ([]AgentInfo, error) {
	agentsDir := filepath.Join(installPath, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []AgentInfo{}, nil
		}
		return nil, fmt.Errorf("read agents dir: %w", err)
	}

	agents := make([]AgentInfo, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}

		filePath := filepath.Join(agentsDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read agent file %s: %w", filePath, err)
		}

		meta := parseFrontmatter(string(content))
		name := firstNonEmpty(meta["name"], extractFirstHeading(string(content)), strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		description := firstNonEmpty(meta["description"], extractFirstParagraph(string(content)))

		agents = append(agents, AgentInfo{
			Name:        name,
			Description: description,
			FilePath:    filePath,
		})
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return agents, nil
}

func (s *Service) scanCommands(installPath string) ([]CommandInfo, error) {
	commandsDir := filepath.Join(installPath, "commands")
	entries, err := os.ReadDir(commandsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []CommandInfo{}, nil
		}
		return nil, fmt.Errorf("read commands dir: %w", err)
	}

	commands := make([]CommandInfo, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}

		commands = append(commands, CommandInfo{
			Name:     strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			FilePath: filepath.Join(commandsDir, entry.Name()),
		})
	}

	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})

	return commands, nil
}

func (s *Service) scanHooks(installPath string) ([]HookInfo, error) {
	path := filepath.Join(installPath, "hooks", "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []HookInfo{}, nil
		}
		return nil, fmt.Errorf("read hooks file: %w", err)
	}

	var raw hooksFile
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse hooks file: %w", err)
	}

	hooks := make([]HookInfo, 0)
	for event, groups := range raw.Hooks {
		for _, group := range groups {
			if len(group.Hooks) == 0 && group.Type != "" {
				hooks = append(hooks, HookInfo{
					Event:   event,
					Type:    group.Type,
					Command: group.Command,
				})
				continue
			}

			for _, hook := range group.Hooks {
				hooks = append(hooks, HookInfo{
					Event:   event,
					Type:    hook.Type,
					Command: hook.Command,
				})
			}
		}
	}

	sort.Slice(hooks, func(i, j int) bool {
		if hooks[i].Event == hooks[j].Event {
			if hooks[i].Type == hooks[j].Type {
				return hooks[i].Command < hooks[j].Command
			}
			return hooks[i].Type < hooks[j].Type
		}
		return hooks[i].Event < hooks[j].Event
	})

	return hooks, nil
}

func (s *Service) readMCPConfig(installPath string) (map[string]interface{}, error) {
	path := filepath.Join(installPath, ".mcp.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read mcp config: %w", err)
	}

	var raw mcpConfigFile
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}

	if raw.MCPServers == nil {
		return map[string]interface{}{}, nil
	}

	return raw.MCPServers, nil
}

func (s *Service) readEnabledPlugins() (map[string]bool, error) {
	path := filepath.Join(s.claudeDir, "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read settings file: %w", err)
	}

	var raw settingsFile
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse settings file: %w", err)
	}

	if raw.EnabledPlugins == nil {
		return map[string]bool{}, nil
	}

	return raw.EnabledPlugins, nil
}

func selectNewestInstalledEntry(entries []installedPluginEntry) (installedPluginEntry, bool) {
	if len(entries) == 0 {
		return installedPluginEntry{}, false
	}

	best := entries[0]
	bestTime := parsePluginTimestamp(best.LastUpdated)
	if bestTime.IsZero() {
		bestTime = parsePluginTimestamp(best.InstalledAt)
	}

	for i := 1; i < len(entries); i++ {
		candidate := entries[i]
		candidateTime := parsePluginTimestamp(candidate.LastUpdated)
		if candidateTime.IsZero() {
			candidateTime = parsePluginTimestamp(candidate.InstalledAt)
		}
		if candidateTime.After(bestTime) {
			best = candidate
			bestTime = candidateTime
		}
	}

	return best, true
}

func parsePluginTimestamp(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func splitPluginID(pluginID string) (string, string) {
	idx := strings.LastIndex(pluginID, "@")
	if idx <= 0 || idx >= len(pluginID)-1 {
		return pluginID, ""
	}
	return pluginID[:idx], pluginID[idx+1:]
}

func parseFrontmatter(content string) map[string]string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]string{}
	}

	meta := map[string]string{}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			return meta
		}
		if line == "" || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)
		meta[key] = value
	}

	return meta
}

func extractFirstHeading(content string) string {
	for _, line := range bodyLines(content) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
	}
	return ""
}

func extractFirstParagraph(content string) string {
	lines := bodyLines(content)
	paragraph := make([]string, 0)
	inCodeBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}
		if trimmed == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, trimmed)
	}

	return strings.Join(paragraph, " ")
}

func bodyLines(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				return lines[i+1:]
			}
		}
	}
	return lines
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
