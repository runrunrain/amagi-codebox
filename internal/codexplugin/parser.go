package codexplugin

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

var (
	ansiPattern    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	versionPattern = regexp.MustCompile(`^v?\d+(?:\.\d+){1,3}(?:[-+][A-Za-z0-9._-]+)?$`)
	pluginIDToken  = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+$`)
	localPathToken = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

func parsePluginListOutput(result *CommandResult) ([]CodexPlugin, error) {
	if result == nil {
		return []CodexPlugin{}, nil
	}
	output := normalizeCLIOutput(result.Output)
	if output == "" {
		return []CodexPlugin{}, nil
	}
	if strings.HasPrefix(output, "[") || strings.HasPrefix(output, "{") {
		if plugins, ok := parsePluginListJSON(output); ok {
			return plugins, nil
		}
	}

	plugins := make([]CodexPlugin, 0)
	seen := map[string]struct{}{}
	currentMarketplace := ""
	currentMarketplacePath := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if marketplace, ok := parsePluginMarketplaceHeader(line); ok {
			currentMarketplace = marketplace
			currentMarketplacePath = ""
			continue
		}
		if path, ok := parsePluginMarketplacePath(line); ok {
			if currentMarketplace == "" || strings.Contains(path, "/.agents/plugins/") || strings.Contains(path, `\.agents\plugins\`) || strings.Contains(path, `.agents\plugins\`) {
				currentMarketplacePath = path
			}
			continue
		}
		if shouldSkipTableLine(line) {
			continue
		}
		if isNotInstalledPluginLine(line) {
			continue
		}
		fields := strings.Fields(strings.NewReplacer("|", " ", "•", " ").Replace(line))
		var id, rawName string
		for _, field := range fields {
			token := strings.Trim(field, " ,;()[]{}<>")
			if pluginIDToken.MatchString(token) {
				id = token
				break
			}
			if rawName == "" && currentMarketplace != "" && isPluginNameToken(token) {
				rawName = token
			}
		}
		if id == "" && rawName != "" {
			id = rawName + "@" + currentMarketplace
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		name, marketplace := splitPluginID(id)
		plugin := CodexPlugin{ID: id, Name: name, Marketplace: marketplace, Enabled: true, Source: "cli"}
		lowerLine := strings.ToLower(line)
		if strings.Contains(lowerLine, "disabled") || strings.Contains(lowerLine, "false") {
			plugin.Enabled = false
		}
		for _, field := range fields {
			token := strings.Trim(field, " ,;()[]{}<>")
			if versionPattern.MatchString(token) {
				plugin.Version = token
			}
			if isLocalPath(token) {
				plugin.InstallPath = token
			}
		}
		if plugin.InstallPath == "" && currentMarketplacePath != "" && plugin.Marketplace == currentMarketplace {
			plugin.ManifestPath = currentMarketplacePath
		}
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	return plugins, nil
}

func parsePluginListJSON(output string) ([]CodexPlugin, bool) {
	type rawPlugin struct {
		ID          string `json:"id"`
		PluginID    string `json:"pluginId"`
		Name        string `json:"name"`
		Marketplace string `json:"marketplace"`
		Version     string `json:"version"`
		Enabled     *bool  `json:"enabled"`
		InstallPath string `json:"installPath"`
		Path        string `json:"path"`
		InstalledAt string `json:"installedAt"`
		LastUpdated string `json:"lastUpdated"`
	}
	var raw []rawPlugin
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		var envelope struct {
			Plugins   []rawPlugin `json:"plugins"`
			Installed []rawPlugin `json:"installed"`
		}
		if err := json.Unmarshal([]byte(output), &envelope); err != nil {
			return nil, false
		}
		raw = append(envelope.Plugins, envelope.Installed...)
	}
	plugins := make([]CodexPlugin, 0, len(raw))
	for _, item := range raw {
		id := firstNonEmpty(item.PluginID, item.ID)
		if id == "" && item.Name != "" && item.Marketplace != "" {
			id = item.Name + "@" + item.Marketplace
		}
		if validatePluginID(id) != nil {
			continue
		}
		name, marketplace := splitPluginID(id)
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		plugins = append(plugins, CodexPlugin{
			ID:          id,
			Name:        firstNonEmpty(item.Name, name),
			Marketplace: firstNonEmpty(item.Marketplace, marketplace),
			Version:     item.Version,
			Enabled:     enabled,
			InstallPath: firstNonEmpty(item.InstallPath, item.Path),
			InstalledAt: item.InstalledAt,
			LastUpdated: item.LastUpdated,
			Source:      "cli",
		})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	return plugins, true
}

func parseMarketplaceListOutput(result *CommandResult) ([]CodexMarketplace, error) {
	if result == nil {
		return []CodexMarketplace{}, nil
	}
	output := normalizeCLIOutput(result.Output)
	if output == "" {
		return []CodexMarketplace{}, nil
	}
	if strings.HasPrefix(output, "[") || strings.HasPrefix(output, "{") {
		var direct []CodexMarketplace
		if err := json.Unmarshal([]byte(output), &direct); err == nil {
			return normalizeMarketplaces(direct), nil
		}
		var envelope struct {
			Marketplaces []CodexMarketplace `json:"marketplaces"`
		}
		if err := json.Unmarshal([]byte(output), &envelope); err == nil {
			return normalizeMarketplaces(envelope.Marketplaces), nil
		}
	}

	if strings.Contains(output, "Name:") || strings.Contains(output, "Source:") || strings.Contains(output, "Snapshot:") {
		if marketplaces := parseMarketplaceBlocks(output); len(marketplaces) > 0 {
			return normalizeMarketplaces(marketplaces), nil
		}
	}

	if marketplaces := parseMarketplaceTSV(output); len(marketplaces) > 0 {
		return normalizeMarketplaces(marketplaces), nil
	}

	marketplaces := make([]CodexMarketplace, 0)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if shouldSkipTableLine(line) {
			continue
		}
		parts := splitColumns(line)
		if len(parts) == 0 {
			continue
		}
		name := strings.Trim(parts[0], " ,;:")
		if name == "" || strings.Contains(name, "@") {
			continue
		}
		mp := CodexMarketplace{Name: name, RawLine: line}
		for _, part := range parts[1:] {
			assignMarketplaceField(&mp, part)
		}
		marketplaces = append(marketplaces, mp)
	}
	return normalizeMarketplaces(marketplaces), nil
}

func parseMarketplaceBlocks(output string) []CodexMarketplace {
	marketplaces := make([]CodexMarketplace, 0)
	current := CodexMarketplace{}
	flush := func() {
		if strings.TrimSpace(current.Name) != "" {
			marketplaces = append(marketplaces, current)
		}
		current = CodexMarketplace{}
	}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		key, value, ok := splitKeyValue(trimmed)
		if !ok {
			if current.Name != "" {
				flush()
			}
			current.Name = strings.Trim(trimmed, ":")
			current.RawLine = trimmed
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			if current.Name != "" {
				flush()
			}
			current.Name = value
		case "source":
			current.Source = value
		case "repo", "repository":
			current.Repo = value
		case "url":
			current.URL = value
		case "path", "installlocation", "install location":
			current.InstallLocation = value
		case "snapshot", "snapshotpath", "snapshot path":
			current.SnapshotPath = value
		case "updated", "last updated", "lastupdated":
			current.LastUpdated = value
		}
	}
	flush()
	return marketplaces
}

func splitColumns(line string) []string {
	if strings.Contains(line, "|") {
		parts := strings.Split(line, "|")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}
	return strings.Fields(line)
}

func assignMarketplaceField(mp *CodexMarketplace, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "git@") {
		if mp.Source == "" {
			mp.Source = trimmed
		}
		if mp.URL == "" && strings.HasPrefix(trimmed, "http") {
			mp.URL = trimmed
		}
		return
	}
	if isLocalPath(trimmed) {
		if strings.Contains(strings.ToLower(trimmed), "snapshot") {
			mp.SnapshotPath = trimmed
		} else if mp.InstallLocation == "" {
			mp.InstallLocation = trimmed
		}
		return
	}
	if strings.Contains(trimmed, "/") && mp.Repo == "" {
		mp.Repo = trimmed
	}
}

func parseMarketplaceTSV(output string) []CodexMarketplace {
	marketplaces := make([]CodexMarketplace, 0)
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.Contains(line, "\t") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		path := strings.TrimSpace(parts[1])
		if name == "" || strings.Contains(name, "@") || !isLocalPath(path) {
			continue
		}
		marketplaces = append(marketplaces, CodexMarketplace{
			Name:            name,
			InstallLocation: path,
			SnapshotPath:    path,
			RawLine:         trimmed,
		})
	}
	return marketplaces
}

func parsePluginMarketplaceHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "Marketplace ") {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(trimmed, "Marketplace "))
	value = strings.Trim(value, " `\"'")
	if value == "" {
		return "", false
	}
	return value, true
}

func parsePluginMarketplacePath(line string) (string, bool) {
	key, value, ok := splitKeyValue(strings.TrimSpace(line))
	if !ok || !strings.EqualFold(key, "path") || !isLocalPath(value) {
		return "", false
	}
	return value, true
}

func isNotInstalledPluginLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "not installed") || strings.Contains(lower, "not-installed")
}

func isPluginNameToken(token string) bool {
	if token == "" || strings.Contains(token, "@") || strings.Contains(token, "/") || strings.Contains(token, "\\") {
		return false
	}
	if strings.ContainsAny(token, "()[]{}<>,;:|") {
		return false
	}
	return regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(token)
}

func isLocalPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "/") || localPathToken.MatchString(trimmed)
}

func normalizeMarketplaces(input []CodexMarketplace) []CodexMarketplace {
	seen := map[string]CodexMarketplace{}
	for _, mp := range input {
		mp.Name = strings.TrimSpace(mp.Name)
		if mp.Name == "" {
			continue
		}
		seen[mp.Name] = mp
	}
	out := make([]CodexMarketplace, 0, len(seen))
	for _, mp := range seen {
		out = append(out, mp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func normalizeCLIOutput(output string) string {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	output = ansiPattern.ReplaceAllString(output, "")
	return strings.TrimSpace(output)
}

func shouldSkipTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "name") && (strings.Contains(lower, "marketplace") || strings.Contains(lower, "source") || strings.Contains(lower, "plugin")) {
		return true
	}
	return strings.Trim(trimmed, "-+| ") == ""
}

func splitKeyValue(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}
