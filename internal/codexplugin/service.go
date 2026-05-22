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
	return s.listMarketplaces(s.runtimeContext())
}

func (s *Service) listMarketplaces(ctx context.Context) ([]CodexMarketplace, error) {
	configMarketplaces, configErr := s.readConfigMarketplaces()
	result := make(map[string]CodexMarketplace, len(configMarketplaces))
	for _, mp := range configMarketplaces {
		result[mp.Name] = mp
	}

	commandResult, cmdErr := s.executeCodexCommand(ctx, "plugin", "marketplace", "list")
	if cmdErr == nil {
		cliMarketplaces, err := parseMarketplaceListOutput(commandResult)
		if err != nil {
			cmdErr = err
		} else {
			for _, mp := range cliMarketplaces {
				merged := mp
				if existing, ok := result[mp.Name]; ok {
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
					if merged.SnapshotPath == "" {
						merged.SnapshotPath = existing.SnapshotPath
					}
					if merged.LastUpdated == "" {
						merged.LastUpdated = existing.LastUpdated
					}
				}
				result[merged.Name] = merged
			}
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
	return out, nil
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
	states, stateErr := s.readPluginStates()
	commandResult, cmdErr := s.executeCodexCommand(ctx, "plugin", "list")
	if cmdErr == nil {
		plugins, err := parsePluginListOutput(commandResult)
		if err == nil {
			for i := range plugins {
				if enabled, ok := states[plugins[i].ID]; ok {
					plugins[i].Enabled = enabled
				}
				if plugins[i].ManifestPath == "" && plugins[i].InstallPath != "" {
					_, manifestPath, _ := s.readPluginManifest(plugins[i].InstallPath)
					plugins[i].ManifestPath = manifestPath
				}
			}
			return filterPluginsByMarketplace(plugins, marketplace), nil
		}
		cmdErr = err
	}
	if s.log != nil && cmdErr != nil {
		s.log.Warn("codexplugin", "读取 Codex 已安装插件 CLI 失败，尝试 config.toml 回退", cmdErr.Error())
	}
	if stateErr != nil {
		if cmdErr != nil {
			return nil, errors.Join(cmdErr, stateErr)
		}
		return nil, stateErr
	}
	plugins, err := s.readConfigPluginsFallback()
	if err != nil {
		return nil, err
	}
	return filterPluginsByMarketplace(plugins, marketplace), nil
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

	manifest, manifestPath, manifestErr := s.readPluginManifest(installed.InstallPath)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return nil, manifestErr
	}
	installed.ManifestPath = manifestPath
	if installed.InstallPath == "" {
		return &CodexPluginDetail{CodexPlugin: *installed, Manifest: manifest, Skills: []SkillInfo{}, Agents: []AgentInfo{}, Commands: []CommandInfo{}, Hooks: []HookInfo{}, PluginType: PluginTypeUnknown}, nil
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
	detail := &CodexPluginDetail{CodexPlugin: *installed, Manifest: manifest, Skills: skills, Agents: agents, Commands: commands, Hooks: hooks, HasMCP: len(mcpServers) > 0, MCPServers: mcpServers}
	detail.PluginType = analyzePluginType(detail)
	return detail, nil
}

func (s *Service) ListAvailablePlugins() ([]CodexAvailablePlugin, error) {
	marketplaces, err := s.listMarketplaces(s.runtimeContext())
	if err != nil {
		return nil, err
	}
	return s.findAvailablePlugins(marketplaces)
}

func (s *Service) RefreshPlugins() (*CodexPluginsData, error) {
	ctx := s.runtimeContext()
	marketplaces, marketErr := s.listMarketplaces(ctx)
	installed, installedErr := s.listPlugins(ctx, "")
	available, availableErr := s.findAvailablePlugins(marketplaces)
	return &CodexPluginsData{Marketplaces: marketplaces, Installed: installed, Available: available}, errors.Join(marketErr, installedErr, availableErr)
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
