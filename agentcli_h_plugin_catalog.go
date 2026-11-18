package main

import (
	"context"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/codexplugin"
	"amagi-codebox/internal/plugin"
)

func (h *agentCLIHost) pluginUpdate(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	app, id, err := agentPluginAppIDArgs(args)
	if err != nil {
		return nil, err
	}
	if target := args.String("target"); target != "" {
		return h.pluginPiTargeted(app, id, target, "update")
	}
	switch app {
	case "claudecode":
		res, err := h.app.Plugins.UpdatePlugin(id)
		return agentPluginCommandOutcome(app, claudePluginResult(res, err))
	case "codex":
		if err := requireCodexPluginID(id); err != nil {
			return nil, err
		}
		res, err := h.app.CodexPlugins.UpdatePlugin(codexplugin.PluginSelector{PluginID: id})
		return agentPluginCommandOutcome(app, codexPluginResult(res, err))
	case "opencode":
		res, err := h.app.OpenCodePlugins.UpdatePlugin(id)
		return agentPluginCommandOutcome(app, openCodePluginResult(res, err))
	case "pi":
		res, err := h.app.PiPlugins.UpdatePackage(id)
		return agentPluginCommandOutcome(app, piPluginResult(res, err))
	default:
		return nil, agentCLIArgErrf("该引擎没有单插件更新入口")
	}
}

func (h *agentCLIHost) pluginPiTargeted(app, source, target, operation string) (any, error) {
	if app != "pi" {
		return nil, agentCLIArgErrf("--target 仅适用于 Pi 插件")
	}
	result, err := h.app.runPiPluginTargeted(operation, source, target)
	if err != nil {
		return nil, agentExternalError(err)
	}
	for i := range result.Results {
		result.Results[i].Output = ""
		result.Results[i].Detail = agentSafeDiagnostic(result.Results[i].Detail)
	}
	return result, nil
}

type agentPluginMarket struct {
	Name string `json:"name"`
}

func (h *agentCLIHost) pluginMarkets(args spec.Args) (any, error) {
	markets := []agentPluginMarket{}
	switch args.String("app") {
	case "claudecode":
		result, err := h.app.Plugins.GetMarketplaces()
		if err != nil {
			return nil, agentExternalError(err)
		}
		for _, market := range result {
			markets = append(markets, agentPluginMarket{Name: market.Name})
		}
	case "codex":
		result, err := h.app.CodexPlugins.ListMarketplaces()
		if err != nil {
			return nil, agentExternalError(err)
		}
		for _, market := range result {
			markets = append(markets, agentPluginMarket{Name: market.Name})
		}
	}
	return markets, nil
}

type agentCatalogPlugin struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Market      string `json:"market"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

func (h *agentCLIHost) pluginCatalog(args spec.Args) (any, error) {
	market := args.String("market")
	plugins := []agentCatalogPlugin{}
	switch args.String("app") {
	case "claudecode":
		result, err := h.app.Plugins.GetAvailablePlugins()
		if err != nil {
			return nil, agentExternalError(err)
		}
		for _, item := range result {
			p, ok := item.(plugin.AvailablePlugin)
			if ok && (market == "" || p.MarketplaceName == market) {
				plugins = append(plugins, agentCatalogPlugin{p.PluginID, p.Name, p.MarketplaceName, p.Version, p.Description})
			}
		}
	case "codex":
		result, err := h.app.CodexPlugins.ListAvailablePlugins()
		if err != nil {
			return nil, agentExternalError(err)
		}
		for _, p := range result {
			if market == "" || p.MarketplaceName == market {
				plugins = append(plugins, agentCatalogPlugin{p.PluginID, p.Name, p.MarketplaceName, p.Version, p.Description})
			}
		}
	}
	return plugins, nil
}

func (h *agentCLIHost) pluginSubitems(args spec.Args) (any, error) {
	id := args.String("id")
	items := []plugin.SubItem{}
	if args.String("app") == "claudecode" {
		detail, err := h.app.Plugins.GetPluginDetail(id)
		if err != nil {
			return nil, agentExternalError(err)
		}
		return append(items, detail.SubItems...), nil
	}
	detail, err := h.app.PiPlugins.GetPackageDetails(id)
	if err != nil {
		return nil, agentExternalError(err)
	}
	for _, item := range detail.Resources {
		items = append(items, plugin.SubItem{
			Type: plugin.SubItemType(item.Type), Name: item.Name, Path: item.FilePath,
			Enabled: detail.Enabled && !item.Disabled, Selectable: item.Type == "skill",
		})
	}
	return items, nil
}

func (h *agentCLIHost) pluginSetSubitem(args spec.Args) (any, error) {
	id, kind, name := args.String("id"), args.String("type"), args.String("name")
	enabled := args.String("state") == "enabled"
	if args.String("app") == "claudecode" {
		return agentLocalDone(h.app.Plugins.SetPluginSubItemEnabled(id, kind, name, enabled))
	}
	if kind != "skill" {
		return nil, agentCLIArgErrf("Pi 当前只支持技能子项启停")
	}
	result, err := h.app.PiPlugins.SetPackageSkillEnabled(id, name, enabled)
	return agentPluginCommandOutcome("pi", piPluginResult(result, err))
}
