package codexplugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxSnapshotDepth = 6
	maxSnapshotFiles = 5000
)

func (s *Service) findAvailablePlugins(marketplaces []CodexMarketplace) ([]CodexAvailablePlugin, error) {
	available := make([]CodexAvailablePlugin, 0)
	seen := map[string]struct{}{}
	for _, marketplace := range marketplaces {
		root := firstNonEmpty(marketplace.SnapshotPath, marketplace.InstallLocation)
		if strings.TrimSpace(root) == "" {
			root = s.defaultMarketplaceSnapshotPath(marketplace.Name)
		}
		if strings.TrimSpace(root) == "" {
			continue
		}
		items, err := scanMarketplaceSnapshot(root, marketplace.Name)
		if err != nil {
			if s.log != nil {
				s.log.Warn("codexplugin", "扫描 Codex marketplace snapshot 失败", fmt.Sprintf("marketplace=%s err=%v", marketplace.Name, err))
			}
			continue
		}
		for _, item := range items {
			if _, ok := seen[item.PluginID]; ok {
				continue
			}
			seen[item.PluginID] = struct{}{}
			available = append(available, item)
		}
	}
	sort.Slice(available, func(i, j int) bool { return available[i].PluginID < available[j].PluginID })
	return available, nil
}

func (s *Service) defaultMarketplaceSnapshotPath(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	base := strings.TrimSpace(s.codexDir)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		base = filepath.Join(home, ".codex")
	}
	return filepath.Join(base, ".tmp", "marketplaces", name)
}

func scanMarketplaceSnapshot(root string, marketplace string) ([]CodexAvailablePlugin, error) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []CodexAvailablePlugin{}, nil
	}
	items := make([]CodexAvailablePlugin, 0)
	visited := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxSnapshotFiles {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != "." && strings.Count(rel, string(os.PathSeparator)) > maxSnapshotDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isManifestPath(path) {
			return nil
		}
		manifest, err := readManifestFile(path)
		if err != nil {
			return nil
		}
		pluginRoot := filepath.Dir(path)
		if strings.EqualFold(filepath.Base(pluginRoot), ".codex-plugin") || strings.EqualFold(filepath.Base(pluginRoot), ".claude-plugin") {
			pluginRoot = filepath.Dir(pluginRoot)
		}
		name := firstNonEmpty(manifest.Name, filepath.Base(pluginRoot))
		id := name + "@" + marketplace
		if validatePluginID(id) != nil {
			return nil
		}
		items = append(items, CodexAvailablePlugin{
			PluginID:        id,
			Name:            name,
			MarketplaceName: marketplace,
			Version:         manifest.Version,
			Description:     manifestSummary(manifest),
			Author:          manifestAuthor(manifest.Author),
			Repository:      manifest.Repository,
			SnapshotPath:    root,
			ManifestPath:    path,
		})
		return nil
	})
	return items, err
}

func (s *Service) readPluginManifest(installPath string) (CodexPluginManifest, string, error) {
	if strings.TrimSpace(installPath) == "" {
		return CodexPluginManifest{}, "", os.ErrNotExist
	}
	paths := []string{
		filepath.Join(installPath, ".codex-plugin", "plugin.json"),
		filepath.Join(installPath, ".claude-plugin", "plugin.json"),
		filepath.Join(installPath, "plugin.json"),
	}
	for _, path := range paths {
		manifest, err := readManifestFile(path)
		if err == nil {
			return manifest, path, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return CodexPluginManifest{}, path, err
		}
	}
	return CodexPluginManifest{}, "", os.ErrNotExist
}

func (s *Service) resolvePluginRoot(installPath, manifestPath, name, marketplace string) (string, string) {
	candidates := make([]string, 0, 8)
	if strings.TrimSpace(manifestPath) != "" {
		candidates = append(candidates, pluginRootFromManifestPath(manifestPath))
	}
	if strings.TrimSpace(installPath) != "" {
		clean := filepath.Clean(installPath)
		candidates = append(candidates, clean)
		if resolved := s.resolveInstallPathFromCache(clean, name, marketplace); resolved != "" {
			candidates = append(candidates, resolved)
		}
	}
	if resolved := s.resolveInstallPathFromCache("", name, marketplace); resolved != "" {
		candidates = append(candidates, resolved)
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate == "" || candidate == "." {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if isPluginRoot(candidate) {
			return candidate, firstExistingManifestPath(candidate)
		}
	}
	return "", ""
}

func (s *Service) resolveInstallPathFromCache(base, name, marketplace string) string {
	name = strings.TrimSpace(name)
	marketplace = strings.TrimSpace(marketplace)
	candidates := make([]string, 0, 3)
	if strings.TrimSpace(base) != "" {
		candidates = append(candidates, filepath.Clean(base))
	}
	if marketplace != "" && name != "" {
		candidates = append(candidates, filepath.Join(s.codexDir, "plugins", "cache", marketplace, name))
	}
	if marketplace != "" {
		candidates = append(candidates, filepath.Join(s.codexDir, "plugins", "cache", marketplace))
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if root := newestPluginRootUnder(candidate); root != "" {
			return root
		}
	}
	return ""
}

func newestPluginRootUnder(root string) string {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return ""
	}
	if isPluginRoot(root) {
		return root
	}
	var matches []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != "." && strings.Count(rel, string(os.PathSeparator)) > 2 {
			return filepath.SkipDir
		}
		if path != root && isPluginRoot(path) {
			matches = append(matches, path)
			return filepath.SkipDir
		}
		return nil
	})
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func isPluginRoot(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	if firstExistingManifestPath(root) != "" {
		return true
	}
	for _, rel := range []string{"skills", "agents", "commands", "hooks", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return true
		}
	}
	return false
}

func firstExistingManifestPath(root string) string {
	for _, path := range []string{
		filepath.Join(root, ".codex-plugin", "plugin.json"),
		filepath.Join(root, ".claude-plugin", "plugin.json"),
		filepath.Join(root, "plugin.json"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func pluginRootFromManifestPath(manifestPath string) string {
	if strings.TrimSpace(manifestPath) == "" {
		return ""
	}
	root := filepath.Dir(filepath.Clean(manifestPath))
	if strings.EqualFold(filepath.Base(root), ".codex-plugin") || strings.EqualFold(filepath.Base(root), ".claude-plugin") {
		root = filepath.Dir(root)
	}
	return root
}

func readManifestFile(path string) (CodexPluginManifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return CodexPluginManifest{}, err
	}
	var manifest CodexPluginManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return CodexPluginManifest{}, fmt.Errorf("parse plugin manifest %s: %w", path, err)
	}
	return manifest, nil
}

func isManifestPath(path string) bool {
	if !strings.EqualFold(filepath.Base(path), "plugin.json") {
		return false
	}
	parent := filepath.Base(filepath.Dir(path))
	return strings.EqualFold(parent, ".codex-plugin") || strings.EqualFold(parent, ".claude-plugin") || parent != ""
}

func manifestAuthor(author map[string]string) string {
	if len(author) == 0 {
		return ""
	}
	return firstNonEmpty(author["name"], author["email"], author["url"])
}

func manifestDisplayName(manifest CodexPluginManifest, fallback string) string {
	if manifest.Interface != nil {
		return firstNonEmpty(manifest.Interface.DisplayName, manifest.Name, fallback)
	}
	return firstNonEmpty(manifest.Name, fallback)
}

func manifestSummary(manifest CodexPluginManifest) string {
	if manifest.Interface != nil {
		return firstNonEmpty(manifest.Interface.ShortDescription, manifest.Description, manifest.Interface.LongDescription)
	}
	return manifest.Description
}

func manifestLongDescription(manifest CodexPluginManifest) string {
	if manifest.Interface != nil {
		return firstNonEmpty(manifest.Interface.LongDescription, manifest.Interface.ShortDescription, manifest.Description)
	}
	return manifest.Description
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
		skills = append(skills, SkillInfo{Name: firstNonEmpty(meta["name"], entry.Name()), Description: firstNonEmpty(meta["description"], extractFirstParagraph(string(content))), FilePath: filePath})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
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
		agents = append(agents, AgentInfo{Name: extractAgentName(meta, string(content), entry.Name()), Description: firstNonEmpty(meta["description"], extractFirstParagraph(string(content))), FilePath: filePath})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
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
		filePath := filepath.Join(commandsDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read command file %s: %w", filePath, err)
		}
		meta := parseFrontmatter(string(content))
		commands = append(commands, CommandInfo{Name: strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), Description: firstNonEmpty(meta["description"], extractFirstParagraph(string(content))), FilePath: filePath})
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
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
	seen := map[string]int{}
	for event, groups := range raw.Hooks {
		for _, group := range groups {
			if len(group.Hooks) == 0 && group.Type != "" {
				info := HookInfo{Event: event, Type: group.Type, Command: group.Command, FilePath: path}
				info.Name = uniqueHookName(info, seen)
				hooks = append(hooks, info)
				continue
			}
			for _, hook := range group.Hooks {
				info := HookInfo{Event: event, Type: hook.Type, Command: hook.Command, FilePath: path}
				info.Name = uniqueHookName(info, seen)
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
		var servers map[string]interface{}
		if err := json.Unmarshal(b, &servers); err != nil {
			return nil, fmt.Errorf("parse mcp config: %w", err)
		}
		return servers, nil
	}
	return raw.MCPServers, nil
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
		meta[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.Trim(strings.TrimSpace(parts[1]), `"'`)
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

func extractAgentName(meta map[string]string, content string, fileName string) string {
	if name := strings.TrimSpace(meta["name"]); name != "" {
		return name
	}
	if heading := extractFirstHeading(content); heading != "" && !isGenericAgentHeading(heading) {
		return heading
	}
	return strings.TrimSuffix(fileName, filepath.Ext(fileName))
}

func isGenericAgentHeading(heading string) bool {
	normalized := normalizeAgentHeading(heading)
	if normalized == "" {
		return true
	}
	genericHeadings := map[string]struct{}{
		"角色定位": {},
		"核心能力": {},
		"思维模式": {},
		"工作流":  {},
		"协作规范": {},
		"输出规范": {},
		"自检清单": {},
	}
	_, ok := genericHeadings[normalized]
	return ok
}

func normalizeAgentHeading(heading string) string {
	value := strings.ToLower(strings.TrimSpace(heading))
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "\t", "")
	sectionPrefixes := []string{
		"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、",
		"1.", "2.", "3.", "4.", "5.", "6.", "7.", "8.", "9.", "10.",
		"1、", "2、", "3、", "4、", "5、", "6、", "7、", "8、", "9、", "10、",
	}
	changed := true
	for changed {
		changed = false
		for _, prefix := range sectionPrefixes {
			if strings.HasPrefix(value, prefix) {
				value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
				changed = true
			}
		}
	}
	return strings.Trim(value, "#：:.-—")
}

func extractFirstParagraph(content string) string {
	paragraph := make([]string, 0)
	inCodeBlock := false
	for _, line := range bodyLines(content) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
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
		if isMarkdownNonParagraphLine(trimmed) {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, trimmed)
	}
	return strings.Join(paragraph, " ")
}

func isMarkdownNonParagraphLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "|") {
		return true
	}
	if isMarkdownRule(trimmed) || isPureListMarker(trimmed) {
		return true
	}
	return false
}

func isMarkdownRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return false
	}
	marker := rune(trimmed[0])
	if marker != '-' && marker != '*' && marker != '_' && marker != '=' {
		return false
	}
	for _, r := range trimmed {
		if r != marker && r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

func isPureListMarker(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "-" || trimmed == "*" || trimmed == "+" {
		return true
	}
	if strings.HasSuffix(trimmed, ".") || strings.HasSuffix(trimmed, ")") {
		number := strings.TrimSuffix(strings.TrimSuffix(trimmed, "."), ")")
		if number == "" {
			return false
		}
		for _, r := range number {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
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

func uniqueHookName(info HookInfo, seen map[string]int) string {
	base := firstNonEmpty(info.Event+":"+info.Type, info.Event)
	if info.Command != "" {
		base = base + ":" + filepath.Base(strings.Fields(info.Command)[0])
	}
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s#%d", base, seen[base])
}

func analyzePluginType(detail *CodexPluginDetail) PluginType {
	nonZero := 0
	single := PluginTypeUnknown
	counts := []struct {
		count int
		kind  PluginType
	}{
		{len(detail.Skills), PluginTypeSkill},
		{len(detail.Hooks), PluginTypeHook},
		{len(detail.Commands), PluginTypeCommand},
		{len(detail.Agents), PluginTypeAgent},
		{len(detail.MCPServers), PluginTypeMCP},
	}
	for _, item := range counts {
		if item.count > 0 {
			nonZero++
			single = item.kind
		}
	}
	if nonZero >= 2 {
		return PluginTypeHybrid
	}
	if nonZero == 1 {
		return single
	}
	return PluginTypeUnknown
}
