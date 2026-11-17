package plugin

import (
	"amagi-codebox/internal/logging"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Service manages Claude Code plugins.
type Service struct {
	claudeDir string
	log       *logging.Service

	// configDir 是 CodeBox 应用配置目录（plugin-subitems.json 与 stash 根所在）。
	// 空值惰性回退 ~/.amagi-codebox；测试经 NewServiceWithConfigDir 注入临时目录。
	configDir string

	// execCLI 是测试注入点：非 nil 时替代 executeClaudeCommand 执行只读 CLI
	// 查询（生产为 nil，走真实 CLI）。仅读路径经 runCLI 分发，写操作不受影响。
	execCLI func(args ...string) (*CommandResult, error)

	// mu 串行化本地插件子项状态文件（plugin-subitems.json）的读-改-写，
	// 与 codex/opencode/pi/omp 四个引擎的写锁语义对齐：Wails 绑定方法
	// 并发触发时防止后写覆盖先写丢失开关状态。
	mu sync.Mutex
}

func NewService(claudeDir string, log *logging.Service) *Service {
	if strings.TrimSpace(claudeDir) == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(homeDir) != "" {
			claudeDir = filepath.Join(homeDir, ".claude")
		} else {
			claudeDir = filepath.Join(".", ".claude")
		}
	}

	return &Service{
		claudeDir: claudeDir,
		log:       log,
	}
}

// NewServiceWithConfigDir 创建带注入配置目录的服务（测试隔离用：子项状态文件
// 与 stash 落在注入目录，不碰真实 ~/.amagi-codebox）。configDir 传空等同 NewService。
func NewServiceWithConfigDir(claudeDir, configDir string, log *logging.Service) *Service {
	svc := NewService(claudeDir, log)
	svc.configDir = configDir
	return svc
}

func (s *Service) GetMarketplaces() ([]Marketplace, error) {
	fileMarketplaces, fileErr := s.readMarketplacesFile()

	resultMap := make(map[string]Marketplace, len(fileMarketplaces))
	for _, marketplace := range fileMarketplaces {
		resultMap[marketplace.Name] = marketplace
	}

	commandResult, cmdErr := s.runCLI("plugin", "marketplace", "list", "--json")
	if cmdErr == nil {
		var cliMarketplaces []Marketplace
		if err := parseCommandJSON(commandResult, &cliMarketplaces); err != nil {
			cmdErr = err
		} else {
			for _, marketplace := range cliMarketplaces {
				merged := marketplace
				if existing, ok := resultMap[marketplace.Name]; ok {
					if merged.Source == "" {
						merged.Source = existing.Source
					}
					if merged.Repo == "" {
						merged.Repo = existing.Repo
					}
					if merged.URL == "" {
						merged.URL = existing.URL
					}
					if merged.InstallLocation == "" {
						merged.InstallLocation = existing.InstallLocation
					}
					if merged.LastUpdated == "" {
						merged.LastUpdated = existing.LastUpdated
					}
					if !merged.AutoUpdate {
						merged.AutoUpdate = existing.AutoUpdate
					}
				}
				resultMap[merged.Name] = merged
			}
		}
	}

	if cmdErr != nil {
		if s.log != nil {
			s.log.Warn("plugin", "读取插件市场 CLI 失败，回退到本地文件", cmdErr.Error())
		}
		if len(resultMap) == 0 {
			if fileErr != nil {
				return nil, errors.Join(fileErr, cmdErr)
			}
			return nil, cmdErr
		}
	}

	marketplaces := make([]Marketplace, 0, len(resultMap))
	for _, marketplace := range resultMap {
		marketplaces = append(marketplaces, marketplace)
	}

	sort.Slice(marketplaces, func(i, j int) bool {
		return marketplaces[i].Name < marketplaces[j].Name
	})

	return marketplaces, nil
}

func (s *Service) GetInstalledPlugins() ([]InstalledPlugin, error) {
	commandResult, cmdErr := s.runCLI("plugin", "list", "--json")
	if cmdErr == nil {
		plugins, err := parseInstalledPluginsOutput(commandResult)
		if err == nil {
			s.enrichInstalledPluginDescriptions(plugins)
			return plugins, nil
		}
		cmdErr = err
	}

	if s.log != nil && cmdErr != nil {
		s.log.Warn("plugin", "读取已安装插件 CLI 失败，回退到本地文件", cmdErr.Error())
	}

	plugins, fileErr := s.readInstalledPluginsFile()
	if fileErr == nil {
		s.enrichInstalledPluginDescriptions(plugins)
		return plugins, nil
	}

	if cmdErr != nil {
		return nil, errors.Join(cmdErr, fileErr)
	}
	return nil, fileErr
}

func (s *Service) enrichInstalledPluginDescriptions(plugins []InstalledPlugin) {
	for i := range plugins {
		if strings.TrimSpace(plugins[i].Description) != "" {
			continue
		}
		manifest, err := s.readPluginManifestForInstalled(plugins[i])
		if err == nil {
			plugins[i].Description = manifest.Description
		}
	}
}

func (s *Service) GetPluginDetail(pluginID string) (*PluginDetail, error) {
	plugins, err := s.GetInstalledPlugins()
	if err != nil {
		return nil, err
	}

	var installed *InstalledPlugin
	for i := range plugins {
		if plugins[i].ID == pluginID {
			copy := plugins[i]
			installed = &copy
			break
		}
	}
	if installed == nil {
		return nil, fmt.Errorf("plugin not found: %s", pluginID)
	}

	manifest, err := s.readPluginManifestForInstalled(*installed)
	if err != nil {
		return nil, err
	}
	installed.Description = manifest.Description
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
	claudeMdPath, hasClaudeMd, err := s.findClaudeMd(installed.InstallPath)
	if err != nil {
		return nil, err
	}

	detail := &PluginDetail{
		InstalledPlugin: *installed,
		Manifest:        manifest,
		Skills:          skills,
		Agents:          agents,
		Commands:        commands,
		Hooks:           hooks,
		HasMCP:          len(mcpServers) > 0,
		MCPServers:      cloneMap(mcpServers),
		HasClaudeMd:     hasClaudeMd,
		ClaudeMdPath:    claudeMdPath,
	}
	detail.PluginType = analyzePluginType(detail)
	// 子项状态以文件系统为真相：installPath 在位 = 启用，stash 在位 = 禁用
	//（2026-11 重构：私有记录不再作为 Enabled 来源，避免与真实状态脱钩）
	detail.SubItems = buildSubItems(detail, s.listStashedSubItems(installed.ID))

	return detail, nil
}

func (s *Service) InstallPlugin(pluginName string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "install", pluginName, "--scope", "user")
}

func (s *Service) UninstallPlugin(pluginID string) (*CommandResult, error) {
	// 与子项写操作互斥（F-04）：防止子项搬移与卸载交错后向已卸载路径恢复文件。
	// 锁内跑 CLI（低频操作，可接受互斥等待）。
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executeClaudeCommand("plugin", "uninstall", pluginID, "--scope", "user")
}

func (s *Service) EnablePlugin(pluginID string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "enable", pluginID)
}

func (s *Service) DisablePlugin(pluginID string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "disable", pluginID)
}

func (s *Service) UpdatePlugin(pluginID string) (*CommandResult, error) {
	// 与子项写操作互斥（F-04）：update 会重写插件目录，须防止与子项搬移交错。
	// 先取锁再刷新市场/更新插件（锁内跑 CLI，低频操作可接受）。
	s.mu.Lock()
	defer s.mu.Unlock()
	_, marketplace := splitPluginID(pluginID)
	if marketplace != "" {
		if s.log != nil {
			s.log.Info("plugin", "更新插件前先刷新市场索引", marketplace)
		}
		if _, err := s.executeClaudeCommand("plugin", "marketplace", "update", marketplace); err != nil {
			if s.log != nil {
				s.log.Warn("plugin", "刷新市场索引失败，继续尝试更新插件", err.Error())
			}
		}
	}
	return s.executeClaudeCommand("plugin", "update", pluginID)
}

func (s *Service) UpdateMarketplace(name string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "marketplace", "update", name)
}

func (s *Service) AddMarketplace(source string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "marketplace", "add", source)
}

func (s *Service) RemoveMarketplace(name string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "marketplace", "remove", name)
}

func (s *Service) GetAvailablePlugins() ([]interface{}, error) {
	plugins, err := s.getAvailablePlugins()
	if err != nil {
		return nil, err
	}
	out := make([]interface{}, 0, len(plugins))
	for _, plugin := range plugins {
		out = append(out, plugin)
	}
	return out, nil
}

// getAvailablePlugins 返回市场目录条目（含已安装插件）。
// 目录数据源策略见 catalog.go 头注释：本地市场 clone 直读为主，CLI --available
// 仅作 installCount 合并与本地不可读时的降级（降级结果过滤为已注册市场条目）。
func (s *Service) getAvailablePlugins() ([]AvailablePlugin, error) {
	cliAvailable, cliErr := s.fetchCLIAvailablePlugins()
	if cliErr != nil {
		if s.log != nil {
			s.log.Warn("plugin", "读取可安装插件 CLI 失败，改用本地市场目录", cliErr.Error())
		}
	}

	entries, failedMarkets, catalogErr := s.readLocalMarketplaceCatalogs()
	if catalogErr == nil {
		mergeInstallCounts(entries, cliAvailable)
		// 部分降级（F05）：本地不可读的市场不能直接丢条目——用 CLI available
		//（未安装子集，经已注册市场过滤）补齐，避免比修复前亚能看到的更少
		filtered := s.filterRegisteredMarketplaces(cliAvailable)
		entries = mergeFailedMarketEntries(entries, failedMarkets, filtered)
		return entries, nil
	}

	if cliErr != nil {
		return nil, errors.Join(cliErr, catalogErr)
	}
	return s.filterRegisteredMarketplaces(cliAvailable), nil
}

func (s *Service) RefreshPlugins() error {
	var errs []error
	if _, err := s.GetMarketplaces(); err != nil {
		errs = append(errs, err)
	}
	if _, err := s.GetInstalledPlugins(); err != nil {
		errs = append(errs, err)
	}
	if _, err := s.GetAvailablePlugins(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func parseInstalledPluginsOutput(result *CommandResult) ([]InstalledPlugin, error) {
	var raw []cliInstalledPlugin
	if err := parseCommandJSON(result, &raw); err != nil {
		return nil, err
	}

	plugins := make([]InstalledPlugin, 0, len(raw))
	for _, item := range raw {
		name, marketplace := splitPluginID(item.ID)
		plugin := InstalledPlugin{
			ID:           item.ID,
			Name:         name,
			Marketplace:  marketplace,
			Version:      item.Version,
			Scope:        item.Scope,
			Enabled:      item.Enabled,
			InstallPath:  item.InstallPath,
			InstalledAt:  item.InstalledAt,
			LastUpdated:  item.LastUpdated,
			GitCommitSha: item.GitCommitSha,
		}
		plugins = append(plugins, plugin)
	}

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].ID < plugins[j].ID
	})

	return plugins, nil
}

func parseCommandJSON(result *CommandResult, target interface{}) error {
	if result == nil {
		return errors.New("command result is nil")
	}
	if strings.TrimSpace(result.Output) == "" {
		if strings.TrimSpace(result.Error) != "" {
			return fmt.Errorf("empty command output: %s", result.Error)
		}
		return errors.New("empty command output")
	}
	if err := json.Unmarshal([]byte(result.Output), target); err != nil {
		return fmt.Errorf("parse command output json: %w", err)
	}
	return nil
}

func normalizeAvailablePlugins(raw []interface{}) []AvailablePlugin {
	plugins := make([]AvailablePlugin, 0, len(raw))
	for _, item := range raw {
		plugin := normalizeAvailablePlugin(item)
		if plugin.PluginID == "" && plugin.Name == "" {
			continue
		}
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool {
		left := firstNonEmpty(plugins[i].PluginID, plugins[i].MarketplaceName+"/"+plugins[i].Name, plugins[i].Name)
		right := firstNonEmpty(plugins[j].PluginID, plugins[j].MarketplaceName+"/"+plugins[j].Name, plugins[j].Name)
		return left < right
	})
	return plugins
}

func normalizeAvailablePlugin(raw interface{}) AvailablePlugin {
	if text, ok := raw.(string); ok {
		return availablePluginFromID(text, raw)
	}
	object, ok := raw.(map[string]interface{})
	if !ok {
		return AvailablePlugin{Raw: raw}
	}

	pluginID := firstStringField(object, "pluginId", "pluginID", "id")
	name := firstStringField(object, "name", "pluginName")
	marketplace := firstStringField(object, "marketplaceName", "marketplace", "sourceMarketplace")
	if pluginID == "" {
		pluginID = firstStringField(object, "selector", "package")
	}
	if name == "" || marketplace == "" {
		idName, idMarketplace := splitPluginID(pluginID)
		if name == "" {
			name = idName
		}
		if marketplace == "" {
			marketplace = idMarketplace
		}
	}
	if pluginID == "" && name != "" && marketplace != "" {
		pluginID = name + "@" + marketplace
	}

	return AvailablePlugin{
		PluginID:        pluginID,
		Name:            name,
		MarketplaceName: marketplace,
		Version:         firstStringField(object, "version"),
		Description:     firstStringField(object, "description", "summary"),
		Author:          object["author"],
		Repository:      firstStringField(object, "repository", "repo", "homepage"),
		InstallCount:    firstIntField(object, "installCount", "installs", "downloads"),
		Raw:             raw,
	}
}

func availablePluginFromID(pluginID string, raw interface{}) AvailablePlugin {
	pluginID = strings.TrimSpace(pluginID)
	name, marketplace := splitPluginID(pluginID)
	return AvailablePlugin{PluginID: pluginID, Name: name, MarketplaceName: marketplace, Raw: raw}
}

func firstStringField(object map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case fmt.Stringer:
			if text := strings.TrimSpace(typed.String()); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstIntField(object map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed
		case int64:
			return int(typed)
		case float64:
			return int(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return int(parsed)
			}
		}
	}
	return 0
}
