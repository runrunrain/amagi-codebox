package codexplugin

import (
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Service manages Codex CLI plugins.
type Service struct {
	codexDir string
	log      *logging.Service
	resolver platform.CLIResolver
	runner   platform.ProcessRunner
	mu       sync.Mutex
}

func NewService(codexDir string, log *logging.Service) *Service {
	return NewServiceWithDeps(codexDir, log, platform.NewCLIResolver(platform.CurrentCapabilities()), platform.NewProcessRunner())
}

func NewServiceWithDeps(codexDir string, log *logging.Service, resolver platform.CLIResolver, runner platform.ProcessRunner) *Service {
	if strings.TrimSpace(codexDir) == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(homeDir) != "" {
			codexDir = filepath.Join(homeDir, ".codex")
		} else {
			codexDir = filepath.Join(".", ".codex")
		}
	}
	return &Service{codexDir: codexDir, log: log, resolver: resolver, runner: runner}
}

func (s *Service) runtimeContext() context.Context {
	return context.Background()
}

func (s *Service) ListMarketplaces() ([]CodexMarketplace, error) {
	marketplaces, err := s.listMarketplaces(s.runtimeContext())
	if err != nil && len(marketplaces) > 0 {
		return marketplaces, nil
	}
	return marketplaces, err
}

func (s *Service) listMarketplaces(ctx context.Context) ([]CodexMarketplace, error) {
	configMarketplaces, configErr := s.readConfigMarketplaces()
	result := make(map[string]CodexMarketplace, len(configMarketplaces))
	for _, mp := range configMarketplaces {
		upsertMarketplace(result, mp, false)
	}

	commandResult, cmdErr := s.executeCodexCommand(ctx, "plugin", "marketplace", "list")
	if cmdErr == nil {
		cliMarketplaces, err := parseMarketplaceListOutput(commandResult)
		if err != nil {
			cmdErr = err
		} else {
			for _, mp := range cliMarketplaces {
				upsertMarketplace(result, mp, true)
			}
		}
	}
	if cmdErr != nil {
		for _, mp := range s.inferMarketplacesFromConfigPlugins() {
			upsertMarketplace(result, mp, false)
		}
		for _, mp := range s.inferMarketplacesFromInstalledPlugins(ctx) {
			upsertMarketplace(result, mp, false)
		}
		for _, mp := range s.inferMarketplacesFromCache() {
			upsertMarketplace(result, mp, false)
		}
	}
	if cmdErr != nil && len(result) == 0 {
		if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
			return nil, errors.Join(cmdErr, configErr)
		}
		return nil, cmdErr
	}
	out := make([]CodexMarketplace, 0, len(result))
	for _, mp := range result {
		out = append(out, mp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, cmdErr
}

func upsertMarketplace(result map[string]CodexMarketplace, incoming CodexMarketplace, preferIncoming bool) {
	incoming.Name = strings.TrimSpace(incoming.Name)
	if incoming.Name == "" {
		return
	}
	existing, ok := result[incoming.Name]
	if !ok {
		result[incoming.Name] = incoming
		return
	}
	merged := existing
	if preferIncoming {
		merged = incoming
		fillMarketplaceEmptyFields(&merged, existing)
	} else {
		fillMarketplaceEmptyFields(&merged, incoming)
	}
	result[incoming.Name] = merged
}

func fillMarketplaceEmptyFields(target *CodexMarketplace, fallback CodexMarketplace) {
	if target.Source == "" {
		target.Source = fallback.Source
	}
	if target.Repo == "" {
		target.Repo = fallback.Repo
	}
	if target.URL == "" {
		target.URL = fallback.URL
	}
	if target.InstallLocation == "" {
		target.InstallLocation = fallback.InstallLocation
	}
	if target.SnapshotPath == "" {
		target.SnapshotPath = fallback.SnapshotPath
	}
	if target.LastUpdated == "" {
		target.LastUpdated = fallback.LastUpdated
	}
	if target.RawLine == "" {
		target.RawLine = fallback.RawLine
	}
}

func (s *Service) inferMarketplacesFromConfigPlugins() []CodexMarketplace {
	states, err := s.readPluginStates()
	if err != nil {
		return []CodexMarketplace{}
	}
	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	marketplaces := make([]CodexMarketplace, 0)
	seen := map[string]struct{}{}
	for _, id := range ids {
		_, marketplace := splitPluginID(id)
		if marketplace == "" {
			continue
		}
		if _, ok := seen[marketplace]; ok {
			continue
		}
		seen[marketplace] = struct{}{}
		marketplaces = append(marketplaces, s.inferredMarketplace(marketplace, "inferred from config plugins"))
	}
	return marketplaces
}

func (s *Service) inferMarketplacesFromInstalledPlugins(ctx context.Context) []CodexMarketplace {
	plugins, err := s.listPlugins(ctx, "")
	if err != nil {
		return []CodexMarketplace{}
	}
	marketplaces := make([]CodexMarketplace, 0)
	seen := map[string]struct{}{}
	for _, plugin := range plugins {
		marketplace := strings.TrimSpace(plugin.Marketplace)
		if marketplace == "" {
			continue
		}
		if _, ok := seen[marketplace]; ok {
			continue
		}
		seen[marketplace] = struct{}{}
		marketplaces = append(marketplaces, s.inferredMarketplace(marketplace, "inferred from installed plugins"))
	}
	sort.Slice(marketplaces, func(i, j int) bool { return marketplaces[i].Name < marketplaces[j].Name })
	return marketplaces
}

func (s *Service) inferMarketplacesFromCache() []CodexMarketplace {
	cacheDir := filepath.Join(s.codexDir, "plugins", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return []CodexMarketplace{}
	}
	marketplaces := make([]CodexMarketplace, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" || validateMarketplaceName(name) != nil {
			continue
		}
		marketplaces = append(marketplaces, s.inferredMarketplace(name, "inferred from plugin cache"))
	}
	sort.Slice(marketplaces, func(i, j int) bool { return marketplaces[i].Name < marketplaces[j].Name })
	return marketplaces
}

func (s *Service) inferredMarketplace(name string, rawLine string) CodexMarketplace {
	name = strings.TrimSpace(name)
	mp := CodexMarketplace{Name: name, RawLine: rawLine}
	cachePath := filepath.Join(s.codexDir, "plugins", "cache", name)
	if info, err := os.Stat(cachePath); err == nil && info.IsDir() {
		mp.InstallLocation = cachePath
	}
	if snapshotPath := s.defaultMarketplaceSnapshotPath(name); snapshotPath != "" {
		if info, err := os.Stat(snapshotPath); err == nil && info.IsDir() {
			mp.SnapshotPath = snapshotPath
		}
	}
	return mp
}

func (s *Service) AddMarketplace(req AddMarketplaceRequest) (*CommandResult, error) {
	source := strings.TrimSpace(req.Source)
	if err := validateSource(source); err != nil {
		return nil, err
	}
	return s.executeCodexCommand(s.runtimeContext(), "plugin", "marketplace", "add", source)
}

func (s *Service) UpgradeMarketplace(name string) (*CommandResult, error) {
	name = strings.TrimSpace(name)
	if err := validateMarketplaceName(name); err != nil {
		return nil, err
	}
	return s.executeCodexCommand(s.runtimeContext(), "plugin", "marketplace", "upgrade", name)
}

func (s *Service) RemoveMarketplace(name string) (*CommandResult, error) {
	name = strings.TrimSpace(name)
	if err := validateMarketplaceName(name); err != nil {
		return nil, err
	}
	return s.executeCodexCommand(s.runtimeContext(), "plugin", "marketplace", "remove", name)
}

func (s *Service) ListPlugins(marketplace string) ([]CodexPlugin, error) {
	return s.listPlugins(s.runtimeContext(), marketplace)
}

func (s *Service) listPlugins(ctx context.Context, marketplace string) ([]CodexPlugin, error) {
	plugins, warnings, err := s.listPluginsWithDiagnostics(ctx, marketplace)
	if s.log != nil {
		for _, warning := range warnings {
			s.log.Warn("codexplugin", "Codex 插件重复安装诊断", warning)
		}
	}
	return plugins, err
}

func (s *Service) listPluginsWithDiagnostics(ctx context.Context, marketplace string) ([]CodexPlugin, []string, error) {
	states, stateErr := s.readPluginStates()
	commandResult, cmdErr := s.executeCodexCommand(ctx, "plugin", "list")
	if cmdErr == nil {
		plugins, err := parsePluginListOutput(commandResult)
		if err == nil {
			for i := range plugins {
				if enabled, ok := states[plugins[i].ID]; ok {
					plugins[i].Enabled = enabled
				}
				root, manifestPath := s.resolvePluginRoot(plugins[i].InstallPath, plugins[i].ManifestPath, plugins[i].Name, plugins[i].Marketplace)
				if root != "" {
					plugins[i].InstallPath = root
					plugins[i].ManifestPath = manifestPath
				}
			}
			plugins, duplicateWarnings := diagnoseAndDedupeCodexPlugins(plugins)
			return filterPluginsByMarketplace(plugins, marketplace), duplicateWarnings, nil
		}
		cmdErr = err
	}
	if s.log != nil && cmdErr != nil {
		s.log.Warn("codexplugin", "读取 Codex 已安装插件 CLI 失败，尝试 config.toml 回退", cmdErr.Error())
	}
	if stateErr != nil {
		if cmdErr != nil {
			return nil, nil, errors.Join(cmdErr, stateErr)
		}
		return nil, nil, stateErr
	}
	plugins, err := s.readConfigPluginsFallback()
	if err != nil {
		return nil, nil, err
	}
	plugins, duplicateWarnings := diagnoseAndDedupeCodexPlugins(plugins)
	return filterPluginsByMarketplace(plugins, marketplace), duplicateWarnings, nil
}

func (s *Service) InstallPlugin(selector PluginSelector) (*CommandResult, error) {
	pluginID, err := selectorPluginID(selector)
	if err != nil {
		return nil, err
	}
	result, err := s.executeCodexCommand(s.runtimeContext(), "plugin", "add", pluginID)
	if err != nil {
		return result, err
	}
	if err := s.setPluginEnabled(pluginID, true); err != nil {
		return &CommandResult{Success: false, Output: result.Output, Error: "插件已安装，但同步启用状态失败：" + err.Error()}, err
	}
	return result, nil
}

func (s *Service) UninstallPlugin(selector PluginSelector) (*CommandResult, error) {
	pluginID, err := selectorPluginID(selector)
	if err != nil {
		return nil, err
	}
	result, err := s.executeCodexCommand(s.runtimeContext(), "plugin", "remove", pluginID)
	if err != nil {
		return result, err
	}
	if cleanupErr := s.removePluginConfig(pluginID); cleanupErr != nil && s.log != nil {
		s.log.Warn("codexplugin", "卸载后清理 config.toml 插件项失败", cleanupErr.Error())
		result.Output = strings.TrimSpace(result.Output + "\n插件已卸载，但清理 config.toml 状态失败：" + cleanupErr.Error())
	}
	return result, nil
}

func (s *Service) SetPluginEnabled(selector PluginSelector, enabled bool) (*CommandResult, error) {
	pluginID, err := selectorPluginID(selector)
	if err != nil {
		return nil, err
	}
	if err := s.setPluginEnabled(pluginID, enabled); err != nil {
		return nil, err
	}
	verb := "disabled"
	if enabled {
		verb = "enabled"
	}
	return &CommandResult{Success: true, Output: fmt.Sprintf("plugin %s %s", pluginID, verb)}, nil
}

func (s *Service) GetPluginDetails(selector PluginSelector) (*CodexPluginDetail, error) {
	pluginID, err := selectorPluginID(selector)
	if err != nil {
		return nil, err
	}
	plugins, err := s.listPlugins(s.runtimeContext(), "")
	if err != nil {
		return nil, err
	}
	var installed *CodexPlugin
	for i := range plugins {
		if plugins[i].ID == pluginID {
			copy := plugins[i]
			installed = &copy
			break
		}
	}
	if installed == nil {
		return nil, fmt.Errorf("未找到 Codex 插件：%s", pluginID)
	}

	root, resolvedManifestPath := s.resolvePluginRoot(installed.InstallPath, installed.ManifestPath, installed.Name, installed.Marketplace)
	if root != "" {
		installed.InstallPath = root
		installed.ManifestPath = resolvedManifestPath
	}

	manifest, manifestPath, manifestErr := s.readPluginManifest(installed.InstallPath)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return nil, manifestErr
	}
	installed.ManifestPath = firstNonEmpty(manifestPath, installed.ManifestPath)
	if installed.InstallPath == "" {
		return &CodexPluginDetail{CodexPlugin: *installed, Manifest: manifest, DisplayName: manifestDisplayName(manifest, installed.Name), ShortDescription: manifestSummary(manifest), LongDescription: manifestLongDescription(manifest), Skills: []SkillInfo{}, Agents: []AgentInfo{}, Commands: []CommandInfo{}, Hooks: []HookInfo{}, PluginType: PluginTypeUnknown}, nil
	}
	skills, err := s.scanSkills(installed.InstallPath)
	if err != nil {
		return nil, err
	}
	agents, err := s.scanAgents(installed.InstallPath)
	if err != nil {
		return nil, err
	}
	commands, err := s.scanCommands(installed.InstallPath)
	if err != nil {
		return nil, err
	}
	hooks, err := s.scanHooks(installed.InstallPath)
	if err != nil {
		return nil, err
	}
	mcpServers, err := s.readMCPConfig(installed.InstallPath)
	if err != nil {
		return nil, err
	}
	detail := &CodexPluginDetail{CodexPlugin: *installed, Manifest: manifest, DisplayName: manifestDisplayName(manifest, installed.Name), ShortDescription: manifestSummary(manifest), LongDescription: manifestLongDescription(manifest), Skills: skills, Agents: agents, Commands: commands, Hooks: hooks, HasMCP: len(mcpServers) > 0, MCPServers: mcpServers}
	detail.PluginType = analyzePluginType(detail)
	return detail, nil
}

func (s *Service) ListAvailablePlugins() ([]CodexAvailablePlugin, error) {
	marketplaces, err := s.listMarketplaces(s.runtimeContext())
	if err != nil && len(marketplaces) == 0 {
		return nil, err
	}
	return s.findAvailablePlugins(marketplaces)
}

func (s *Service) RefreshPlugins() (*CodexPluginsData, error) {
	ctx := s.runtimeContext()
	marketplaces, marketErr := s.listMarketplaces(ctx)
	installed, duplicateWarnings, installedErr := s.listPluginsWithDiagnostics(ctx, "")
	available, availableErr := s.findAvailablePlugins(marketplaces)
	warnings := codexPluginWarnings(marketErr, installedErr, availableErr)
	warnings = appendUniqueWarnings(warnings, duplicateWarnings...)
	data := &CodexPluginsData{Marketplaces: marketplaces, Installed: installed, Available: available, Warnings: warnings}
	if marketErr != nil && len(marketplaces) == 0 && len(installed) == 0 && len(available) == 0 {
		return data, marketErr
	}
	if installedErr != nil && len(installed) == 0 && len(available) == 0 {
		return data, installedErr
	}
	if availableErr != nil && len(available) == 0 && len(marketplaces) == 0 {
		return data, availableErr
	}
	return data, nil
}

func codexPluginWarnings(errs ...error) []string {
	warnings := make([]string, 0)
	for _, err := range errs {
		if err == nil {
			continue
		}
		message := strings.TrimSpace(err.Error())
		if message == "" {
			continue
		}
		warnings = appendUniqueWarnings(warnings, message)
	}
	return warnings
}

func appendUniqueWarnings(warnings []string, messages ...string) []string {
	seen := make(map[string]struct{}, len(warnings)+len(messages))
	for _, warning := range warnings {
		seen[warning] = struct{}{}
	}
	for _, message := range messages {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		if _, ok := seen[message]; ok {
			continue
		}
		seen[message] = struct{}{}
		warnings = append(warnings, message)
	}
	return warnings
}

func diagnoseAndDedupeCodexPlugins(plugins []CodexPlugin) ([]CodexPlugin, []string) {
	if len(plugins) <= 1 {
		return plugins, nil
	}
	groups := map[string][]CodexPlugin{}
	order := make([]string, 0)
	for _, plugin := range plugins {
		key := codexDuplicateGroupKey(plugin)
		if key == "" {
			key = plugin.ID
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], plugin)
	}
	result := make([]CodexPlugin, 0, len(groups))
	warnings := make([]string, 0)
	for _, key := range order {
		items := groups[key]
		if len(items) == 0 {
			continue
		}
		canonicalIndex := preferredCodexPluginIndex(items)
		canonical := items[canonicalIndex]
		duplicates := make([]CodexPlugin, 0, len(items)-1)
		for i, item := range items {
			if i == canonicalIndex {
				continue
			}
			item.DuplicateOf = canonical.ID
			item.Warning = codexDuplicateWarning(item, canonical)
			duplicates = append(duplicates, item)
		}
		if len(duplicates) > 0 && shouldWarnCodexDuplicateGroup(canonical, duplicates) {
			canonical.Warning = codexDuplicateGroupWarning(canonical, duplicates)
			warnings = appendUniqueWarnings(warnings, canonical.Warning)
		}
		result = append(result, canonical)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, warnings
}

func codexDuplicateGroupKey(plugin CodexPlugin) string {
	name := codexDuplicatePluginName(plugin)
	marketplace := strings.TrimSpace(plugin.Marketplace)
	if name != "" && marketplace != "" && !isPlaceholderPluginName(name) {
		return strings.ToLower(name + "@" + marketplace)
	}
	return strings.ToLower(strings.TrimSpace(plugin.ID))
}

func codexDuplicatePluginName(plugin CodexPlugin) string {
	name := strings.TrimSpace(plugin.Name)
	if name != "" && !isPlaceholderPluginName(name) {
		return name
	}
	if idName, _ := splitPluginID(strings.TrimSpace(plugin.ID)); idName != "" && !isPlaceholderPluginName(idName) {
		return idName
	}
	if pathName := pluginNameFromInstallPath(plugin.InstallPath); pathName != "" {
		return pathName
	}
	if pathName := pluginNameFromInstallPath(pluginRootFromManifestPath(plugin.ManifestPath)); pathName != "" {
		return pathName
	}
	return name
}

func preferredCodexPluginIndex(plugins []CodexPlugin) int {
	best := 0
	bestScore := codexPluginCanonicalScore(plugins[0])
	for i := 1; i < len(plugins); i++ {
		score := codexPluginCanonicalScore(plugins[i])
		if score > bestScore || (score == bestScore && plugins[i].ID < plugins[best].ID) {
			best = i
			bestScore = score
		}
	}
	return best
}

func codexPluginCanonicalScore(plugin CodexPlugin) int {
	score := 0
	if isCodexPluginCachePath(plugin.InstallPath) || isCodexPluginCachePath(plugin.ManifestPath) {
		score += 200
	}
	if isCodexTemporaryMarketplacePath(plugin.InstallPath) || isCodexTemporaryMarketplacePath(plugin.ManifestPath) {
		score -= 150
	}
	if strings.EqualFold(plugin.Source, "cli") {
		score += 100
	}
	if !isPlaceholderPluginName(plugin.Name) {
		score += 50
	}
	nameFromID, marketplaceFromID := splitPluginID(plugin.ID)
	if plugin.Name != "" && plugin.Marketplace != "" && strings.EqualFold(plugin.Name, nameFromID) && strings.EqualFold(plugin.Marketplace, marketplaceFromID) {
		score += 25
	}
	if plugin.ManifestPath != "" {
		score += 10
	}
	if plugin.InstallPath != "" {
		score += 5
	}
	return score
}

func shouldWarnCodexDuplicateGroup(canonical CodexPlugin, duplicates []CodexPlugin) bool {
	if len(duplicates) == 0 {
		return false
	}
	if !isCodexPluginCacheRecord(canonical) {
		return true
	}
	for _, duplicate := range duplicates {
		if isCodexTemporaryMarketplaceRecord(duplicate) {
			continue
		}
		if isSameInstallPathPlaceholderDuplicate(canonical, duplicate) {
			continue
		}
		return true
	}
	return false
}

func isSameInstallPathPlaceholderDuplicate(canonical CodexPlugin, duplicate CodexPlugin) bool {
	if !isPlaceholderCodexPluginRecord(duplicate) {
		return false
	}
	canonicalPath := normalizedCodexEffectivePathForMatch(canonical)
	duplicatePath := normalizedCodexEffectivePathForMatch(duplicate)
	if canonicalPath == "" || duplicatePath == "" {
		return false
	}
	return canonicalPath == duplicatePath
}

func isPlaceholderCodexPluginRecord(plugin CodexPlugin) bool {
	if isPlaceholderPluginName(plugin.Name) {
		return true
	}
	nameFromID, _ := splitPluginID(strings.TrimSpace(plugin.ID))
	return isPlaceholderPluginName(nameFromID)
}

func normalizedCodexEffectivePathForMatch(plugin CodexPlugin) string {
	path := strings.TrimSpace(plugin.InstallPath)
	if path == "" {
		path = pluginRootFromManifestPath(plugin.ManifestPath)
	}
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if clean == "." {
		return ""
	}
	return normalizedCodexPathForMatch(clean)
}

func isCodexPluginCacheRecord(plugin CodexPlugin) bool {
	return isCodexPluginCachePath(plugin.InstallPath) || isCodexPluginCachePath(plugin.ManifestPath)
}

func isCodexTemporaryMarketplaceRecord(plugin CodexPlugin) bool {
	return isCodexTemporaryMarketplacePath(plugin.InstallPath) || isCodexTemporaryMarketplacePath(plugin.ManifestPath)
}

func isCodexPluginCachePath(path string) bool {
	path = normalizedCodexPathForMatch(path)
	return strings.Contains(path, "/plugins/cache/") || strings.HasSuffix(path, "/plugins/cache")
}

func isCodexTemporaryMarketplacePath(path string) bool {
	path = normalizedCodexPathForMatch(path)
	return strings.Contains(path, "/.tmp/marketplaces/") || strings.HasSuffix(path, "/.tmp/marketplaces")
}

func normalizedCodexPathForMatch(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.ToLower(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func isPlaceholderPluginName(name string) bool {
	trimmed := strings.TrimSpace(name)
	return strings.EqualFold(trimmed, "PLUGIN")
}

func normalizedPluginInstallPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func pluginNameFromInstallPath(path string) string {
	path = normalizedPluginInstallPath(path)
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" || isVersionDirectoryName(base) {
		parent := filepath.Dir(path)
		if parent == path || parent == "." {
			return ""
		}
		base = filepath.Base(parent)
	}
	if validatePluginID(base+"@placeholder") != nil {
		return ""
	}
	return base
}

func isVersionDirectoryName(name string) bool {
	name = strings.TrimPrefix(strings.TrimSpace(name), "v")
	if name == "" {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func codexDuplicateWarning(duplicate CodexPlugin, canonical CodexPlugin) string {
	return fmt.Sprintf("检测到 Codex 插件重复记录 %s(path=%s)，已归并到 canonical 记录 %s(path=%s)；未删除任何用户文件", duplicate.ID, codexPluginPathSummary(duplicate), canonical.ID, codexPluginPathSummary(canonical))
}

func codexDuplicateGroupWarning(canonical CodexPlugin, duplicates []CodexPlugin) string {
	duplicateIDs := make([]string, 0, len(duplicates))
	for _, duplicate := range duplicates {
		duplicateIDs = append(duplicateIDs, fmt.Sprintf("%s(path=%s)", duplicate.ID, codexPluginPathSummary(duplicate)))
	}
	sort.Strings(duplicateIDs)
	return fmt.Sprintf("检测到 Codex 插件重复记录：canonical=%s(path=%s) duplicates=%s；已在刷新结果中按 canonical 归并，未删除任何用户文件", canonical.ID, codexPluginPathSummary(canonical), strings.Join(duplicateIDs, ","))
}

func codexPluginPathSummary(plugin CodexPlugin) string {
	if path := strings.TrimSpace(plugin.InstallPath); path != "" {
		return path
	}
	if path := strings.TrimSpace(plugin.ManifestPath); path != "" {
		return path
	}
	return "unknown"
}

func filterPluginsByMarketplace(plugins []CodexPlugin, marketplace string) []CodexPlugin {
	marketplace = strings.TrimSpace(marketplace)
	if marketplace == "" {
		return plugins
	}
	out := make([]CodexPlugin, 0)
	for _, plugin := range plugins {
		if plugin.Marketplace == marketplace {
			out = append(out, plugin)
		}
	}
	return out
}
