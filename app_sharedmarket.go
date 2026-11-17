package main

// app_sharedmarket.go — 共用插件市场（~/.agents/marketplaces）的 App 接线与
// 前端门面。域逻辑在 internal/sharedmarket；本文件只负责：
//   - 持有 Service（userHome=用户主目录，构造模式照抄 buildSharedSkillsService）；
//   - Claude Code 注册表 → 可 clone 源的转换（github repo / git url / 本地
//     installLocation 路径；sharedmarket 包对来源引擎无感知）；
//   - Wails 门面方法（List/Refresh/Add/Import/Update/Remove/Deploy/Open 共 8 个），
//     全部不含令牌。
//
// SharedMarkets.Service 不进 bind_list.go：与 SharedSkills/SharedMcp 同模式，
// 经 App 门面暴露。

import (
	"fmt"
	"os"
	"strings"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/sharedmarket"
)

// constructSharedMarkets 在 NewApp 构造共用插件市场服务（与 constructSharedSkills
// 同时机）。纯构造不做文件 IO。
func (a *App) constructSharedMarkets() {
	if a.SharedMarkets != nil {
		return
	}
	a.SharedMarkets = buildSharedMarketService(a)
}

// buildSharedMarketService 构造服务：userHome=用户主目录（~/.agents 所在），
// 解析失败回退 configDir（模式照抄 buildSharedSkillsService——降级时 ~/.agents
// 不可达，List 以空态/错误如实呈现，不影响其余服务）。
func buildSharedMarketService(a *App) *sharedmarket.Service {
	home := defaultConfigDir()
	var log *logging.Service
	if a != nil {
		if a.configDir != "" {
			home = a.configDir
		}
		log = a.Log
	}
	userHome := home
	if resolved, err := os.UserHomeDir(); err == nil {
		userHome = resolved
	}
	return sharedmarket.New(userHome, log)
}

// sharedMarketService 返回共用插件市场服务。生产由 NewApp 构造；未构造的
// App（测试夹具直接拼 struct）现场兜底构造，不写回字段（与 sharedSkillsService
// 同模式，避免与生产构造路径竞态）。
func (a *App) sharedMarketService() *sharedmarket.Service {
	if a != nil && a.SharedMarkets != nil {
		return a.SharedMarkets
	}
	return buildSharedMarketService(a)
}

// ---- Wails 门面 ----

// ListSharedMarkets 返回全部共用市场（状态 + 实时目录扫描）。
func (a *App) ListSharedMarkets() ([]sharedmarket.Marketplace, error) {
	return a.sharedMarketService().List()
}

// RefreshSharedMarkets 显式刷新：服务层无缓存，语义等同 ListSharedMarkets
// （为前端契约点的 Refresh 显式提供，未来加缓存不改前端）。
func (a *App) RefreshSharedMarkets() ([]sharedmarket.Marketplace, error) {
	return a.sharedMarketService().List()
}

// AddSharedMarketplace 从 git 源（URL 或本地路径）新增共用市场，返回最新列表
// （前端免二次请求）。
func (a *App) AddSharedMarketplace(source string) ([]sharedmarket.Marketplace, error) {
	if _, err := a.sharedMarketService().Add(source, ""); err != nil {
		return nil, err
	}
	return a.sharedMarketService().List()
}

// ClaudeMarketplaceForImport 是「从 Claude Code 导入」选择器的条目视图：
// Claude 注册表市场 + 已导入标记（按名称比对共用市场状态）。
type ClaudeMarketplaceForImport struct {
	Name     string `json:"name"`
	Source   string `json:"source"`   // 可 clone 源（转换后）
	Repo     string `json:"repo,omitempty"`
	URL      string `json:"url,omitempty"`
	Imported bool   `json:"imported"` // 已存在于共用市场
}

// claudeMarketplaceCloneSource 把 plugin.Marketplace 的来源字段转换为可 clone
// 源：URL 优先，github repo 合成 https URL，最后回退本地 installLocation。
func claudeMarketplaceCloneSource(repo, url, installLocation string) (string, error) {
	if trimmed := strings.TrimSpace(url); trimmed != "" {
		return trimmed, nil
	}
	if trimmed := strings.TrimSpace(repo); trimmed != "" {
		return "https://github.com/" + trimmed + ".git", nil
	}
	if trimmed := strings.TrimSpace(installLocation); trimmed != "" {
		return trimmed, nil
	}
	return "", fmt.Errorf("市场来源字段为空（repo/url/installLocation 均缺失）")
}

// buildClaudeImportViews 是 ListClaudeMarketplacesForImport 的纯组装逻辑
//（抽出便于单测：无 CLI/git 依赖）。source 转换失败时置空串，由前端如实呈现。
func buildClaudeImportViews(
	marketplaces []plugin.Marketplace,
	shared []sharedmarket.Marketplace,
) []ClaudeMarketplaceForImport {
	imported := make(map[string]bool, len(shared))
	for _, market := range shared {
		imported[market.Name] = true
	}

	out := make([]ClaudeMarketplaceForImport, 0, len(marketplaces))
	for _, market := range marketplaces {
		if strings.TrimSpace(market.Name) == "" {
			continue
		}
		source, _ := claudeMarketplaceCloneSource(market.Repo, market.URL, market.InstallLocation)
		out = append(out, ClaudeMarketplaceForImport{
			Name:     market.Name,
			Source:   source,
			Repo:     market.Repo,
			URL:      market.URL,
			Imported: imported[market.Name],
		})
	}
	return out
}

// ListClaudeMarketplacesForImport 列出 Claude Code 已注册市场并标记是否已导入
// 共用市场（前端导入选择器数据源）。
func (a *App) ListClaudeMarketplacesForImport() ([]ClaudeMarketplaceForImport, error) {
	marketplaces, err := a.Plugins.GetMarketplaces()
	if err != nil {
		return nil, fmt.Errorf("读取 Claude 市场列表失败：%w", err)
	}
	shared, sharedErr := a.sharedMarketService().List()
	if sharedErr != nil {
		shared = nil
	}
	return buildClaudeImportViews(marketplaces, shared), nil
}

// ImportSharedMarketplacesFromClaude 按名称从 Claude Code 注册表导入市场到
// 共用市场（保持同名，源经 claudeMarketplaceCloneSource 转换）。单个失败即
// 中止并携带市场名（已成功导入的保留——Add 单市场原子，批量不做回滚）。
func (a *App) ImportSharedMarketplacesFromClaude(names []string) ([]sharedmarket.Marketplace, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("未选择要导入的市场")
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			wanted[trimmed] = true
		}
	}

	marketplaces, err := a.Plugins.GetMarketplaces()
	if err != nil {
		return nil, fmt.Errorf("读取 Claude 市场列表失败：%w", err)
	}

	svc := a.sharedMarketService()
	for _, market := range marketplaces {
		if !wanted[market.Name] {
			continue
		}
		source, convErr := claudeMarketplaceCloneSource(market.Repo, market.URL, market.InstallLocation)
		if convErr != nil {
			return nil, fmt.Errorf("市场 %q：%v", market.Name, convErr)
		}
		if _, addErr := svc.Add(source, market.Name); addErr != nil {
			return nil, fmt.Errorf("导入市场 %q 失败：%v", market.Name, addErr)
		}
	}
	return svc.List()
}

// UpdateSharedMarketplace 拉取市场最新内容（缺失时重新 clone），返回最新列表。
func (a *App) UpdateSharedMarketplace(name string) ([]sharedmarket.Marketplace, error) {
	if _, err := a.sharedMarketService().Update(name); err != nil {
		return nil, err
	}
	return a.sharedMarketService().List()
}

// RemoveSharedMarketplace 删除共用市场（clone 目录 + 状态条目；已部署技能
// 不受影响），返回最新列表（前端须先 danger 确认）。
func (a *App) RemoveSharedMarketplace(name string) ([]sharedmarket.Marketplace, error) {
	if err := a.sharedMarketService().Remove(name); err != nil {
		return nil, err
	}
	return a.sharedMarketService().List()
}

// DeploySharedMarketplacePlugin 把市场内插件的技能部署到 ~/.agents/skills
// （成为共用技能，由共用技能子页统一启停管理）。
func (a *App) DeploySharedMarketplacePlugin(marketName, pluginName string) (*sharedmarket.DeployReport, error) {
	return a.sharedMarketService().DeployPlugin(marketName, pluginName)
}

// OpenSharedMarketplaceFolder 用系统文件管理器打开市场 clone 目录（模式照抄
// OpenSharedSkillFolder：先校验存在再打开）。
func (a *App) OpenSharedMarketplaceFolder(name string) error {
	dir, err := a.sharedMarketService().MarketDir(name)
	if err != nil {
		return err
	}
	if err := a.fileOpener().Open(dir); err != nil {
		if a.Log != nil {
			a.Log.Warn("sharedmarket", "打开市场目录失败", fmt.Sprintf("dir=%s err=%v", dir, err))
		}
		return fmt.Errorf("打开市场目录失败：%s（%v）", dir, err)
	}
	if a.Log != nil {
		a.Log.Info("sharedmarket", "系统文件管理器打开市场目录", dir)
	}
	return nil
}
