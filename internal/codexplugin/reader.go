package codexplugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type codexPluginConfig struct {
	Enabled     bool
	Line        int
	SectionLine int
	EndLine     int
	Format      string
	HasEnabled  bool
	Locations   []codexPluginConfigLocation
}

type codexPluginConfigLocation struct {
	Enabled     bool
	Line        int
	SectionLine int
	EndLine     int
	Format      string
	HasEnabled  bool
}

const (
	codexPluginConfigInline  = "inline"
	codexPluginConfigSection = "section"
)

func (s *Service) configPath() string {
	return filepath.Join(s.codexDir, "config.toml")
}

func (s *Service) readPluginStates() (map[string]bool, error) {
	content, err := os.ReadFile(s.configPath())
	if err != nil {
		return nil, fmt.Errorf("read Codex config.toml: %w", err)
	}
	entries, err := parsePluginEntries(string(content))
	if err != nil {
		return nil, err
	}
	states := make(map[string]bool, len(entries))
	for id, entry := range entries {
		states[id] = entry.Enabled
	}
	return states, nil
}

func (s *Service) readConfigPluginsFallback() ([]CodexPlugin, error) {
	states, err := s.readPluginStates()
	if err != nil {
		return nil, err
	}
	plugins := make([]CodexPlugin, 0, len(states))
	for id, enabled := range states {
		name, marketplace := splitPluginID(id)
		plugin := CodexPlugin{ID: id, Name: name, Marketplace: marketplace, Enabled: enabled, Source: "configFallback"}
		if root, manifestPath := s.resolvePluginRoot("", "", name, marketplace); root != "" {
			plugin.InstallPath = root
			plugin.ManifestPath = manifestPath
		}
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	return plugins, nil
}

func (s *Service) readConfigMarketplaces() ([]CodexMarketplace, error) {
	content, err := os.ReadFile(s.configPath())
	if err != nil {
		return nil, fmt.Errorf("read Codex config.toml: %w", err)
	}
	return parseMarketplaceEntries(string(content)), nil
}

func (s *Service) setPluginEnabled(pluginID string, enabled bool) error {
	if err := validatePluginID(pluginID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.configPath()
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			contentBytes = []byte{}
		} else {
			return fmt.Errorf("read Codex config.toml: %w", err)
		}
	}
	updated, err := updatePluginEnabledInToml(string(contentBytes), pluginID, enabled)
	if err != nil {
		return err
	}
	return writeConfigWithBackup(path, []byte(updated), contentBytes)
}

func (s *Service) removePluginConfig(pluginID string) error {
	if err := validatePluginID(pluginID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.configPath()
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Codex config.toml: %w", err)
	}
	updated, err := removePluginEntryFromToml(string(contentBytes), pluginID)
	if err != nil {
		return err
	}
	return writeConfigWithBackup(path, []byte(updated), contentBytes)
}

func parsePluginEntries(content string) (map[string]codexPluginConfig, error) {
	lines := splitTomlLines(content)
	entries := map[string]codexPluginConfig{}
	start, end := findSectionRange(lines, "plugins")
	if start >= 0 {
		for i := start + 1; i < end; i++ {
			trimmed := strings.TrimSpace(stripTomlComment(lines[i]))
			if trimmed == "" {
				continue
			}
			key, value, ok := parseQuotedAssignment(trimmed)
			if !ok {
				continue
			}
			enabled, ok := parseInlineEnabled(value)
			if !ok {
				enabled = true
			}
			addPluginConfigLocation(entries, key, codexPluginConfigLocation{Enabled: enabled, Line: i, SectionLine: start, EndLine: end, Format: codexPluginConfigInline, HasEnabled: ok})
		}
	}
	for i := 0; i < len(lines); i++ {
		pluginID, ok := parsePluginDottedSectionHeader(strings.TrimSpace(stripTomlComment(lines[i])))
		if !ok {
			continue
		}
		_, sectionEnd := findSectionRange(lines[i:], strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(stripTomlComment(lines[i])), "]"), "["))
		if sectionEnd <= 0 {
			sectionEnd = len(lines) - i
		}
		enabled := true
		enabledLine := -1
		hasEnabled := false
		for j := i + 1; j < i+sectionEnd && j < len(lines); j++ {
			key, value, ok := parseBareAssignment(strings.TrimSpace(stripTomlComment(lines[j])))
			if !ok || !strings.EqualFold(key, "enabled") {
				continue
			}
			parsed := strings.EqualFold(strings.TrimSpace(value), "true")
			if parsed || strings.EqualFold(strings.TrimSpace(value), "false") {
				enabled = parsed
				enabledLine = j
				hasEnabled = true
			}
		}
		line := enabledLine
		if line < 0 {
			line = i
		}
		addPluginConfigLocation(entries, pluginID, codexPluginConfigLocation{Enabled: enabled, Line: line, SectionLine: i, EndLine: i + sectionEnd, Format: codexPluginConfigSection, HasEnabled: hasEnabled})
	}
	return entries, nil
}

func addPluginConfigLocation(entries map[string]codexPluginConfig, pluginID string, loc codexPluginConfigLocation) {
	entry := entries[pluginID]
	entry.Locations = append(entry.Locations, loc)
	if shouldPreferPluginConfigLocation(loc, codexPluginConfigLocation{Enabled: entry.Enabled, Line: entry.Line, SectionLine: entry.SectionLine, EndLine: entry.EndLine, Format: entry.Format, HasEnabled: entry.HasEnabled}) {
		entry.Enabled = loc.Enabled
		entry.Line = loc.Line
		entry.SectionLine = loc.SectionLine
		entry.EndLine = loc.EndLine
		entry.Format = loc.Format
		entry.HasEnabled = loc.HasEnabled
	}
	entries[pluginID] = entry
}

func shouldPreferPluginConfigLocation(candidate codexPluginConfigLocation, current codexPluginConfigLocation) bool {
	if current.Format == "" {
		return true
	}
	if candidate.Format == codexPluginConfigSection && current.Format != codexPluginConfigSection {
		return true
	}
	if candidate.Format != codexPluginConfigSection && current.Format == codexPluginConfigSection {
		return false
	}
	return candidate.Line >= current.Line
}

func updatePluginEnabledInToml(content, pluginID string, enabled bool) (string, error) {
	lines := splitTomlLines(content)
	entries, err := parsePluginEntries(content)
	if err != nil {
		return "", err
	}
	start, end := findSectionRange(lines, "plugins")
	enabledText := "false"
	if enabled {
		enabledText = "true"
	}
	if entry, ok := entries[pluginID]; ok {
		if len(entry.Locations) > 1 {
			lines = removePluginConfigLocations(lines, nonSelectedPluginConfigLocations(entry))
			entries, err = parsePluginEntries(joinTomlLines(lines))
			if err != nil {
				return "", err
			}
			entry = entries[pluginID]
		}
		if entry.Format == codexPluginConfigSection {
			if entry.HasEnabled {
				lines[entry.Line] = replaceBareEnabled(lines[entry.Line], enabledText)
			} else {
				insertAt := entry.SectionLine + 1
				lines = append(lines[:insertAt], append([]string{"enabled = " + enabledText}, lines[insertAt:]...)...)
			}
			return joinTomlLines(lines), nil
		}
		lines[entry.Line] = replaceInlineEnabled(lines[entry.Line], enabledText)
		return joinTomlLines(lines), nil
	}
	newLine := fmt.Sprintf("%q = { enabled = %s }", pluginID, enabledText)
	if start < 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "[plugins]", newLine)
		return joinTomlLines(lines), nil
	}
	insertAt := end
	lines = append(lines[:insertAt], append([]string{newLine}, lines[insertAt:]...)...)
	return joinTomlLines(lines), nil
}

func removePluginEntryFromToml(content, pluginID string) (string, error) {
	lines := splitTomlLines(content)
	entries, err := parsePluginEntries(content)
	if err != nil {
		return "", err
	}
	entry, ok := entries[pluginID]
	if !ok {
		return joinTomlLines(lines), nil
	}
	lines = removePluginConfigLocations(lines, entry.Locations)
	return joinTomlLines(lines), nil
}

func nonSelectedPluginConfigLocations(entry codexPluginConfig) []codexPluginConfigLocation {
	locations := make([]codexPluginConfigLocation, 0, len(entry.Locations))
	for _, loc := range entry.Locations {
		if loc.Format == entry.Format && loc.Line == entry.Line && loc.SectionLine == entry.SectionLine {
			continue
		}
		locations = append(locations, loc)
	}
	return locations
}

func removePluginConfigLocations(lines []string, locations []codexPluginConfigLocation) []string {
	type interval struct {
		start int
		end   int
	}
	intervals := make([]interval, 0, len(locations))
	for _, loc := range locations {
		start := loc.Line
		end := loc.Line + 1
		if loc.Format == codexPluginConfigSection {
			start = loc.SectionLine
			end = loc.EndLine
		}
		if start < 0 || start >= len(lines) {
			continue
		}
		if end <= start || end > len(lines) {
			end = start + 1
		}
		intervals = append(intervals, interval{start: start, end: end})
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start == intervals[j].start {
			return intervals[i].end > intervals[j].end
		}
		return intervals[i].start > intervals[j].start
	})
	lastStart := len(lines) + 1
	for _, item := range intervals {
		if item.end > lastStart {
			item.end = lastStart
		}
		if item.start >= item.end {
			continue
		}
		lines = append(lines[:item.start], lines[item.end:]...)
		lastStart = item.start
	}
	return lines
}

func parsePluginDottedSectionHeader(line string) (string, bool) {
	if !strings.HasPrefix(line, "[plugins.") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "[plugins."), "]"))
	if len(inner) < 2 || inner[0] != '"' || inner[len(inner)-1] != '"' {
		return "", false
	}
	pluginID := strings.TrimSpace(inner[1 : len(inner)-1])
	if validatePluginID(pluginID) != nil {
		return "", false
	}
	return pluginID, true
}

func parseMarketplaceEntries(content string) []CodexMarketplace {
	lines := splitTomlLines(content)
	result := map[string]CodexMarketplace{}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(stripTomlComment(lines[i]))
		if !strings.HasPrefix(trimmed, "[marketplaces.") || !strings.HasSuffix(trimmed, "]") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "[marketplaces."), "]")
		name = strings.Trim(name, `"'`)
		if name == "" {
			continue
		}
		_, end := findSectionRange(lines[i:], strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "["))
		if end <= 0 {
			end = len(lines) - i
		}
		mp := CodexMarketplace{Name: name}
		for j := i + 1; j < i+end && j < len(lines); j++ {
			key, value, ok := parseBareAssignment(strings.TrimSpace(stripTomlComment(lines[j])))
			if !ok {
				continue
			}
			value = strings.Trim(value, `"'`)
			switch strings.ToLower(key) {
			case "source":
				mp.Source = value
			case "repo", "repository":
				mp.Repo = value
			case "url":
				mp.URL = value
			case "installlocation", "install_location", "path":
				mp.InstallLocation = value
			case "snapshotpath", "snapshot_path", "snapshot":
				mp.SnapshotPath = value
			case "lastupdated", "last_updated", "updated":
				mp.LastUpdated = value
			}
		}
		result[name] = mp
	}
	out := make([]CodexMarketplace, 0, len(result))
	for _, mp := range result {
		out = append(out, mp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func writeConfigWithBackup(path string, next []byte, previous []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create Codex config dir: %w", err)
	}
	if len(previous) > 0 {
		backupPath := fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102150405"))
		if err := os.WriteFile(backupPath, previous, 0600); err != nil {
			return fmt.Errorf("backup Codex config.toml: %w", err)
		}
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary Codex config: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(next); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary Codex config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary Codex config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary Codex config: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temporary Codex config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace Codex config.toml: %w", err)
	}
	cleanup = false
	return nil
}

func splitTomlLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if content == "" {
		return []string{}
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func joinTomlLines(lines []string) string {
	return strings.Join(lines, "\n") + "\n"
}

func findSectionRange(lines []string, section string) (int, int) {
	target := "[" + section + "]"
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(stripTomlComment(line))
		if start < 0 {
			if trimmed == target {
				start = i
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			return start, i
		}
	}
	if start >= 0 {
		return start, len(lines)
	}
	return -1, len(lines)
}

func stripTomlComment(line string) string {
	inQuote := rune(0)
	for i, r := range line {
		if (r == '\'' || r == '"') && (i == 0 || line[i-1] != '\\') {
			if inQuote == 0 {
				inQuote = r
			} else if inQuote == r {
				inQuote = 0
			}
		}
		if r == '#' && inQuote == 0 {
			return line[:i]
		}
	}
	return line
}

func parseQuotedAssignment(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "\"") {
		return "", "", false
	}
	end := -1
	for i := 1; i < len(line); i++ {
		if line[i] == '"' && line[i-1] != '\\' {
			end = i
			break
		}
	}
	if end <= 1 {
		return "", "", false
	}
	rest := strings.TrimSpace(line[end+1:])
	if !strings.HasPrefix(rest, "=") {
		return "", "", false
	}
	return line[1:end], strings.TrimSpace(rest[1:]), true
}

func parseBareAssignment(line string) (string, string, bool) {
	idx := strings.Index(line, "=")
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

func parseInlineEnabled(value string) (bool, bool) {
	re := regexp.MustCompile(`(?i)(^|[,{\s])enabled\s*=\s*(true|false)(\s|[,}])`)
	match := re.FindStringSubmatch(value)
	if len(match) < 3 {
		return false, false
	}
	return strings.EqualFold(match[2], "true"), true
}

func replaceInlineEnabled(line string, enabledText string) string {
	re := regexp.MustCompile(`(?i)(enabled\s*=\s*)(true|false)`)
	if re.MatchString(line) {
		return re.ReplaceAllString(line, `${1}`+enabledText)
	}
	idx := strings.LastIndex(line, "}")
	if idx >= 0 {
		prefix := strings.TrimRight(line[:idx], " ")
		if strings.HasSuffix(prefix, "{") {
			return prefix + " enabled = " + enabledText + " }" + line[idx+1:]
		}
		return prefix + ", enabled = " + enabledText + " }" + line[idx+1:]
	}
	return line + " # unsupported plugin entry format"
}

func replaceBareEnabled(line string, enabledText string) string {
	re := regexp.MustCompile(`(?i)^(\s*enabled\s*=\s*)(true|false)(.*)$`)
	if re.MatchString(line) {
		return re.ReplaceAllString(line, `${1}`+enabledText+`${3}`)
	}
	return "enabled = " + enabledText
}
