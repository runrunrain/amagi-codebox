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
		plugin := InstalledPlugin{
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
		}
		if manifest, err := s.readPluginManifestForInstalled(plugin); err == nil {
			plugin.Description = manifest.Description
		}
		plugins = append(plugins, plugin)
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

func (s *Service) readPluginManifestForInstalled(installed InstalledPlugin) (PluginManifest, error) {
	manifest, err := s.readPluginManifest(installed.InstallPath)
	if err == nil {
		return manifest, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return PluginManifest{}, err
	}

	marketplaceEntry, marketplaceErr := s.readMarketplacePluginEntry(installed)
	if marketplaceErr != nil {
		return PluginManifest{}, errors.Join(err, marketplaceErr)
	}
	if marketplaceEntry.Description == "" && marketplaceEntry.Name == "" && marketplaceEntry.Version == "" {
		return PluginManifest{}, err
	}

	manifest = PluginManifest{
		Name:        firstNonEmpty(marketplaceEntry.Name, installed.Name),
		Version:     firstNonEmpty(marketplaceEntry.Version, installed.Version),
		Description: marketplaceEntry.Description,
		Author:      marketplaceEntry.Author,
		License:     marketplaceEntry.License,
		Keywords:    marketplaceEntry.Keywords,
		Homepage:    marketplaceEntry.Homepage,
		Repository:  marketplaceEntry.Repository,
	}
	return manifest, nil
}

func (s *Service) readMarketplacePluginEntry(installed InstalledPlugin) (marketplacePluginEntry, error) {
	if strings.TrimSpace(installed.Marketplace) == "" || strings.TrimSpace(installed.Name) == "" {
		return marketplacePluginEntry{}, os.ErrNotExist
	}

	marketplaces, err := s.readMarketplacesFile()
	if err != nil {
		return marketplacePluginEntry{}, err
	}

	for _, marketplace := range marketplaces {
		if marketplace.Name != installed.Marketplace || strings.TrimSpace(marketplace.InstallLocation) == "" {
			continue
		}
		entry, err := readMarketplacePluginEntryFromLocation(marketplace.InstallLocation, installed.Name)
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			return entry, err
		}
	}

	return marketplacePluginEntry{}, os.ErrNotExist
}

func readMarketplacePluginEntryFromLocation(installLocation, pluginName string) (marketplacePluginEntry, error) {
	catalogPath := filepath.Join(installLocation, ".claude-plugin", "marketplace.json")
	b, err := os.ReadFile(catalogPath)
	if err != nil {
		return marketplacePluginEntry{}, err
	}

	var catalog marketplaceCatalogFile
	if err := json.Unmarshal(b, &catalog); err != nil {
		return marketplacePluginEntry{}, fmt.Errorf("parse marketplace catalog: %w", err)
	}

	for _, entry := range catalog.Plugins {
		if entry.Name != pluginName {
			continue
		}
		if manifest, err := readMarketplaceSourceManifest(installLocation, catalog.Metadata.PluginRoot, entry); err == nil {
			return marketplacePluginEntryFromManifest(entry, manifest), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return marketplacePluginEntry{}, err
		}
		return entry, nil
	}

	return marketplacePluginEntry{}, os.ErrNotExist
}

func readMarketplaceSourceManifest(installLocation, pluginRoot string, entry marketplacePluginEntry) (PluginManifest, error) {
	if strings.TrimSpace(entry.Source) == "" && strings.TrimSpace(pluginRoot) == "" && strings.TrimSpace(entry.Name) == "" {
		return PluginManifest{}, os.ErrNotExist
	}

	pluginDir := marketplacePluginDir(installLocation, pluginRoot, entry)
	path := filepath.Join(pluginDir, ".claude-plugin", "plugin.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return PluginManifest{}, err
	}

	var manifest PluginManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return PluginManifest{}, fmt.Errorf("parse marketplace plugin manifest: %w", err)
	}
	return manifest, nil
}

func marketplacePluginDir(installLocation, pluginRoot string, entry marketplacePluginEntry) string {
	if strings.TrimSpace(entry.Source) != "" {
		return cleanMarketplacePath(installLocation, entry.Source)
	}
	if strings.TrimSpace(pluginRoot) != "" && strings.TrimSpace(entry.Name) != "" {
		return cleanMarketplacePath(installLocation, filepath.Join(pluginRoot, entry.Name))
	}
	return cleanMarketplacePath(installLocation, entry.Name)
}

func cleanMarketplacePath(base, value string) string {
	value = strings.TrimSpace(value)
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(base, value))
}

func marketplacePluginEntryFromManifest(entry marketplacePluginEntry, manifest PluginManifest) marketplacePluginEntry {
	entry.Name = firstNonEmpty(manifest.Name, entry.Name)
	entry.Version = firstNonEmpty(manifest.Version, entry.Version)
	entry.Description = firstNonEmpty(manifest.Description, entry.Description)
	if len(entry.Author) == 0 {
		entry.Author = manifest.Author
	}
	entry.License = firstNonEmpty(manifest.License, entry.License)
	if len(entry.Keywords) == 0 {
		entry.Keywords = manifest.Keywords
	}
	entry.Homepage = firstNonEmpty(manifest.Homepage, entry.Homepage)
	entry.Repository = firstNonEmpty(manifest.Repository, entry.Repository)
	return entry
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
	seenNames := map[string]int{}
	for event, groups := range raw.Hooks {
		for _, group := range groups {
			if len(group.Hooks) == 0 && group.Type != "" {
				info := HookInfo{Event: event, Type: group.Type, Command: group.Command, FilePath: path}
				info.Name = uniqueHookName(info, seenNames)
				hooks = append(hooks, info)
				continue
			}

			for _, hook := range group.Hooks {
				info := HookInfo{Event: event, Type: hook.Type, Command: hook.Command, FilePath: path}
				info.Name = uniqueHookName(info, seenNames)
				hooks = append(hooks, info)
			}
		}
	}

	sort.Slice(hooks, func(i, j int) bool {
		if hooks[i].Event == hooks[j].Event {
			return hooks[i].Name < hooks[j].Name
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
