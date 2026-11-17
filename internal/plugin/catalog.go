package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 本文件承载 GetAvailablePlugins 的「本地市场目录直读」数据源。
//
// 背景（2026-11 修复）：此前目录数据来自 `claude plugin list --json --available`，
// 该输出有两个不可靠点——
//  1. available 数组只含未安装插件（Claude Code 2.1.x 实测）：插件全部装完的市场
//     显示「0 个可安装」，市场页「已安装」页签永远为空，看起来像功能损坏；
//  2. 输出形状随 CLI 版本漂移：旧版 bare-array 会混入 claude.ai 插件目录数千条
//     条目（不属于任何已注册市场、无可靠安装路径），虚标「全部市场」计数。
//
// claude plugin marketplace add/update 会把每个市场完整 clone 到
// ~/.claude/plugins/marketplaces/<name>/，其 .claude-plugin/marketplace.json 就是
// 完整目录（含已安装插件）。因此目录以本地直读为准；CLI 输出仅用于 installCount
// 合并（按 pluginId 匹配）与本地目录不可读时的降级（降级结果过滤为已注册市场条目）。

// fetchCLIAvailablePlugins 执行 CLI 并解析两种历史输出形状（bare array / envelope）。
func (s *Service) fetchCLIAvailablePlugins() ([]AvailablePlugin, error) {
	result, err := s.runCLI("plugin", "list", "--json", "--available")
	if err != nil {
		return nil, err
	}
	return parseCLIAvailableResult(result)
}

func parseCLIAvailableResult(result *CommandResult) ([]AvailablePlugin, error) {
	var direct []interface{}
	if err := parseCommandJSON(result, &direct); err == nil {
		return normalizeAvailablePlugins(direct), nil
	}

	var envelope availablePluginsEnvelope
	if err := parseCommandJSON(result, &envelope); err != nil {
		return nil, err
	}
	if envelope.Available == nil {
		return []AvailablePlugin{}, nil
	}
	return normalizeAvailablePlugins(envelope.Available), nil
}

// readLocalMarketplaceCatalogs 从本地市场 clone 直读完整目录（含已安装条目）。
// 单个市场读取失败只跳过该市场（记日志），并把该市场名记入 failedNames 供
// 调用方做部分降级（F05：只要有其它市场本地可读，失败市场也不能直接丢条目，
// 否则会从修复前的「CLI 能显示未安装插件」退化成显示 0）。全部不可读时返回
// 错误供调用方整体降级。合法空市场（catalog 可读但 0 插件）不算失败。
func (s *Service) readLocalMarketplaceCatalogs() (entries []AvailablePlugin, failedNames []string, err error) {
	marketplaces, err := s.readMarketplacesFile()
	if err != nil {
		return nil, nil, err
	}

	entries = make([]AvailablePlugin, 0)
	for _, marketplace := range marketplaces {
		if strings.TrimSpace(marketplace.Name) == "" || strings.TrimSpace(marketplace.InstallLocation) == "" {
			continue
		}
		catalog, catalogErr := readMarketplaceCatalog(marketplace.InstallLocation)
		if catalogErr != nil {
			if s.log != nil {
				s.log.Warn("plugin", "读取本地市场目录失败，降级该市场到 CLI 数据", fmt.Sprintf("%s: %v", marketplace.Name, catalogErr))
			}
			failedNames = append(failedNames, marketplace.Name)
			continue
		}
		for _, entry := range catalog.Plugins {
			if strings.TrimSpace(entry.Name) == "" {
				continue
			}
			plugin := availablePluginFromCatalogEntry(marketplace.Name, entry)
			// 目录条目缺 description/version 时尝试读 source 指向的插件 manifest 补全
			if plugin.Description == "" || plugin.Version == "" {
				if manifest, manifestErr := readMarketplaceSourceManifest(marketplace.InstallLocation, catalog.Metadata.PluginRoot, entry); manifestErr == nil {
					plugin.Description = firstNonEmpty(plugin.Description, manifest.Description)
					plugin.Version = firstNonEmpty(plugin.Version, manifest.Version)
				}
			}
			entries = append(entries, plugin)
		}
	}

	if len(entries) == 0 && len(failedNames) == 0 {
		return nil, nil, errors.New("no readable local marketplace catalog entries")
	}
	// 全部市场都读失败 → 整体降级；至少一个市场可读（即使 0 条目）→ 部分降级
	if len(failedNames) > 0 && len(entries) == 0 && len(failedNames) == readableCandidates(marketplaces) {
		return nil, failedNames, errors.New("no readable local marketplace catalog entries")
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].PluginID < entries[j].PluginID
	})
	return entries, failedNames, nil
}

// readableCandidates 统计有 name+installLocation 的市场总数（分母：判断是否全部失败）。
func readableCandidates(marketplaces []Marketplace) int {
	count := 0
	for _, marketplace := range marketplaces {
		if strings.TrimSpace(marketplace.Name) != "" && strings.TrimSpace(marketplace.InstallLocation) != "" {
			count++
		}
	}
	return count
}

// mergeFailedMarketEntries 为本地不可读的市场补充 CLI available 条目（仅未安装
// 子集，仍比丢失好），并保持结果按 pluginId 有序。
func mergeFailedMarketEntries(entries []AvailablePlugin, failedNames []string, cliAvailable []AvailablePlugin) []AvailablePlugin {
	if len(failedNames) == 0 || len(cliAvailable) == 0 {
		return entries
	}
	failed := make(map[string]bool, len(failedNames))
	for _, name := range failedNames {
		failed[name] = true
	}
	for _, plugin := range cliAvailable {
		if failed[plugin.MarketplaceName] {
			entries = append(entries, plugin)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].PluginID < entries[j].PluginID
	})
	return entries
}

// readMarketplaceCatalog 解析某市场 clone 根下的 .claude-plugin/marketplace.json。
func readMarketplaceCatalog(installLocation string) (marketplaceCatalogFile, error) {
	catalogPath := filepath.Join(installLocation, ".claude-plugin", "marketplace.json")
	b, err := os.ReadFile(catalogPath)
	if err != nil {
		return marketplaceCatalogFile{}, err
	}

	var catalog marketplaceCatalogFile
	if err := json.Unmarshal(b, &catalog); err != nil {
		return marketplaceCatalogFile{}, fmt.Errorf("parse marketplace catalog: %w", err)
	}
	return catalog, nil
}

func availablePluginFromCatalogEntry(marketplaceName string, entry marketplacePluginEntry) AvailablePlugin {
	return AvailablePlugin{
		PluginID:        entry.Name + "@" + marketplaceName,
		Name:            entry.Name,
		MarketplaceName: marketplaceName,
		Version:         entry.Version,
		Description:     entry.Description,
		Author:          authorDisplayName(entry.Author),
		Repository:      firstNonEmpty(entry.Repository, entry.Homepage),
	}
}

// authorDisplayName 把 marketplace.json 的 author 对象（{name,url,...}）压成展示名，
// 避免前端把对象当字符串处理（搜索过滤、来源徽章都按字符串消费）。
func authorDisplayName(author map[string]string) string {
	if author == nil {
		return ""
	}
	if name, ok := author["name"]; ok && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	// 无 name 键时取第一个非空值兜底
	for _, value := range author {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// mergeInstallCounts 用 CLI available 输出（含 installCount、仅未安装子集）
// 按完整 pluginId 合并安装量；已安装条目 CLI 侧无数据，保持 0。
func mergeInstallCounts(entries []AvailablePlugin, cliAvailable []AvailablePlugin) {
	if len(cliAvailable) == 0 {
		return
	}
	counts := make(map[string]int, len(cliAvailable))
	for _, plugin := range cliAvailable {
		if plugin.InstallCount > 0 {
			counts[plugin.PluginID] = plugin.InstallCount
		}
	}
	for i := range entries {
		if count, ok := counts[entries[i].PluginID]; ok {
			entries[i].InstallCount = count
		}
	}
}

// filterRegisteredMarketplaces 过滤 CLI 输出中不属于任何已注册市场的条目
// （旧版 CLI 会混入 claude.ai 插件目录条目）。市场注册表不可读时原样返回。
func (s *Service) filterRegisteredMarketplaces(plugins []AvailablePlugin) []AvailablePlugin {
	marketplaces, err := s.readMarketplacesFile()
	if err != nil {
		return plugins
	}

	known := make(map[string]bool, len(marketplaces))
	for _, marketplace := range marketplaces {
		if name := strings.TrimSpace(marketplace.Name); name != "" {
			known[name] = true
		}
	}

	filtered := make([]AvailablePlugin, 0, len(plugins))
	for _, plugin := range plugins {
		if known[plugin.MarketplaceName] {
			filtered = append(filtered, plugin)
		}
	}
	return filtered
}
