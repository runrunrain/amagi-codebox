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
)

// Service manages Claude Code plugins.
type Service struct {
	claudeDir string
	log       *logging.Service
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

func (s *Service) GetMarketplaces() ([]Marketplace, error) {
	fileMarketplaces, fileErr := s.readMarketplacesFile()

	resultMap := make(map[string]Marketplace, len(fileMarketplaces))
	for _, marketplace := range fileMarketplaces {
		resultMap[marketplace.Name] = marketplace
	}

	commandResult, cmdErr := s.executeClaudeCommand("plugin", "marketplace", "list", "--json")
	if cmdErr == nil {
		var cliMarketplaces []Marketplace
		if err := parseCommandJSON(commandResult, &cliMarketplaces); err != nil {
			cmdErr = err
		} else {
			for _, marketplace := range cliMarketplaces {
				merged := marketplace
				if existing, ok := resultMap[marketplace.Name]; ok {
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
	commandResult, cmdErr := s.executeClaudeCommand("plugin", "list", "--json")
	if cmdErr == nil {
		plugins, err := parseInstalledPluginsOutput(commandResult)
		if err == nil {
			return plugins, nil
		}
		cmdErr = err
	}

	if s.log != nil && cmdErr != nil {
		s.log.Warn("plugin", "读取已安装插件 CLI 失败，回退到本地文件", cmdErr.Error())
	}

	plugins, fileErr := s.readInstalledPluginsFile()
	if fileErr == nil {
		return plugins, nil
	}

	if cmdErr != nil {
		return nil, errors.Join(cmdErr, fileErr)
	}
	return nil, fileErr
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

	manifest, err := s.readPluginManifest(installed.InstallPath)
	if err != nil {
		return nil, err
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
	claudeMdPath, hasClaudeMd, err := s.findClaudeMd(installed.InstallPath)
	if err != nil {
		return nil, err
	}
	disabledRefs, err := s.readDisabledSubItems(pluginID)
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
	detail.SubItems = buildSubItems(detail, disabledRefs)

	return detail, nil
}

func (s *Service) InstallPlugin(pluginName string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "install", pluginName, "--scope", "user")
}

func (s *Service) UninstallPlugin(pluginID string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "uninstall", pluginID, "--scope", "user")
}

func (s *Service) EnablePlugin(pluginID string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "enable", pluginID)
}

func (s *Service) DisablePlugin(pluginID string) (*CommandResult, error) {
	return s.executeClaudeCommand("plugin", "disable", pluginID)
}

func (s *Service) UpdatePlugin(pluginID string) (*CommandResult, error) {
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
	commandResult, err := s.executeClaudeCommand("plugin", "list", "--json", "--available")
	if err != nil {
		return nil, err
	}

	var direct []interface{}
	if err := parseCommandJSON(commandResult, &direct); err == nil {
		return direct, nil
	}

	var envelope availablePluginsEnvelope
	if err := parseCommandJSON(commandResult, &envelope); err != nil {
		return nil, err
	}

	if envelope.Available == nil {
		return []interface{}{}, nil
	}

	return envelope.Available, nil
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
		plugins = append(plugins, InstalledPlugin{
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
		})
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
