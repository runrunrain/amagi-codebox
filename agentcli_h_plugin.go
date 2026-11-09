package main

// agentcli_h_plugin.go — agent-CLI 插件域 Handler（命令表见 internal/agentcli/cmds/plugin.go）。
// 五引擎统一抽象：list 返回统一 DTO；enable/disable 仅支持有启停面的引擎
// （claudecode/codex/omp；opencode 与 pi 引擎无启停面，返回 bad_args）。

import (
	"context"
	"regexp"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/codexplugin"
	"amagi-codebox/internal/ompplugin"
	"amagi-codebox/internal/opencodeplugin"
	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/plugin"
)

func (h *agentCLIHost) pluginHandlers() map[string]spec.Handler {
	return map[string]spec.Handler{
		"plugin.list":      h.pluginList,
		"plugin.enable":    h.pluginEnable,
		"plugin.disable":   h.pluginDisable,
		"plugin.install":   h.pluginInstall,
		"plugin.uninstall": h.pluginUninstall,
	}
}

// agentPluginAppOrder 聚合列出时的引擎顺序。
var agentPluginAppOrder = []string{"claudecode", "codex", "opencode", "pi", "omp"}

func validAgentPluginApp(app string) bool {
	for _, a := range agentPluginAppOrder {
		if a == app {
			return true
		}
	}
	return false
}

// agentPluginSummary 是五引擎统一的插件摘要 DTO（字段取各引擎实际存在的值）。
type agentPluginSummary struct {
	App         string `json:"app"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Source      string `json:"source,omitempty"`
	InstallPath string `json:"installPath,omitempty"`
}

// ---- list ----

func (h *agentCLIHost) pluginList(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	app := strings.TrimSpace(args.String("app"))
	if app != "" && !validAgentPluginApp(app) {
		return nil, spec.Errorf(spec.ErrBadArgs, "app 必须是 %s 之一，得到 %q", strings.Join(agentPluginAppOrder, "|"), app)
	}
	apps := agentPluginAppOrder
	single := false
	if app != "" {
		apps = []string{app}
		single = true
	}
	plugins := []agentPluginSummary{}
	errs := map[string]string{}
	for _, a := range apps {
		summaries, err := h.listPluginsForApp(a)
		if err != nil {
			if single {
				return nil, spec.Errorf(spec.ErrInternal, "读取 %s 插件失败: %v", a, err)
			}
			errs[a] = err.Error()
			continue
		}
		plugins = append(plugins, summaries...)
	}
	resp := map[string]any{"plugins": plugins}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	return resp, nil
}

func (h *agentCLIHost) listPluginsForApp(app string) ([]agentPluginSummary, error) {
	switch app {
	case "claudecode":
		list, err := h.app.Plugins.GetInstalledPlugins()
		if err != nil {
			return nil, err
		}
		out := make([]agentPluginSummary, 0, len(list))
		for _, p := range list {
			out = append(out, agentPluginSummary{
				App: app, ID: p.ID, Name: p.Name, Version: p.Version, Enabled: p.Enabled,
				Description: p.Description, Scope: p.Scope, Source: p.Marketplace, InstallPath: p.InstallPath,
			})
		}
		return out, nil
	case "codex":
		list, err := h.app.CodexPlugins.ListPlugins("")
		if err != nil {
			return nil, err
		}
		out := make([]agentPluginSummary, 0, len(list))
		for _, p := range list {
			out = append(out, agentPluginSummary{
				App: app, ID: p.ID, Name: p.Name, Version: p.Version, Enabled: p.Enabled,
				Description: p.Warning, Source: p.Marketplace, InstallPath: p.InstallPath,
			})
		}
		return out, nil
	case "opencode":
		list, err := h.app.OpenCodePlugins.ListInstalledPlugins()
		if err != nil {
			return nil, err
		}
		out := make([]agentPluginSummary, 0, len(list))
		for _, p := range list {
			out = append(out, agentPluginSummary{
				App: app, ID: p.ID, Name: p.Name, Version: p.Version, Enabled: p.Enabled,
				Description: p.Description, Scope: p.Scope, Source: p.Spec, InstallPath: p.InstallPath,
			})
		}
		return out, nil
	case "pi":
		list, err := h.app.PiPlugins.ListInstalledPackages()
		if err != nil {
			return nil, err
		}
		out := make([]agentPluginSummary, 0, len(list))
		for _, p := range list {
			out = append(out, agentPluginSummary{
				App: app, ID: p.ID, Name: p.Name, Version: p.Version, Enabled: p.Enabled,
				Description: p.Description, Scope: p.Scope, Source: p.Source, InstallPath: p.InstallPath,
			})
		}
		return out, nil
	case "omp":
		list, err := h.app.OmpPlugins.ListPlugins()
		if err != nil {
			return nil, err
		}
		out := make([]agentPluginSummary, 0, len(list))
		for _, p := range list {
			out = append(out, agentPluginSummary{
				App: app, ID: p.ID, Name: p.Name, Version: p.Version, Enabled: p.Enabled,
				Description: p.Description, Scope: p.Scope, Source: p.Kind, InstallPath: p.InstallPath,
			})
		}
		return out, nil
	}
	return nil, spec.Errorf(spec.ErrBadArgs, "未知插件引擎 %q", app)
}

// ---- enable / disable ----

func (h *agentCLIHost) pluginEnable(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	return h.pluginSetEnabled(args, true)
}

func (h *agentCLIHost) pluginDisable(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	return h.pluginSetEnabled(args, false)
}

func (h *agentCLIHost) pluginSetEnabled(args spec.Args, enabled bool) (any, error) {
	app, id, err := agentPluginAppIDArgs(args)
	if err != nil {
		return nil, err
	}
	switch app {
	case "claudecode":
		if enabled {
			res, callErr := h.app.Plugins.EnablePlugin(id)
			return agentPluginCommandOutcome(app, claudePluginResult(res, callErr))
		}
		res, callErr := h.app.Plugins.DisablePlugin(id)
		return agentPluginCommandOutcome(app, claudePluginResult(res, callErr))
	case "codex":
		if err := requireCodexPluginID(id); err != nil {
			return nil, err
		}
		res, callErr := h.app.CodexPlugins.SetPluginEnabled(codexplugin.PluginSelector{PluginID: id}, enabled)
		return agentPluginCommandOutcome(app, codexPluginResult(res, callErr))
	case "omp":
		res, callErr := h.app.OmpPlugins.SetPluginEnabled(id, enabled)
		return agentPluginCommandOutcome(app, ompPluginResult(res, callErr))
	case "opencode":
		return nil, spec.Errorf(spec.ErrBadArgs, "opencode 引擎不支持插件启用/禁用（无启停面，安装/卸载即开关；卸载用 plugin uninstall）")
	case "pi":
		return nil, spec.Errorf(spec.ErrBadArgs, "pi 引擎不支持启用/禁用（packages[] 中存在即启用，移除用 plugin uninstall）")
	}
	return nil, spec.Errorf(spec.ErrBadArgs, "未知插件引擎 %q", app)
}

// ---- install / uninstall ----

func (h *agentCLIHost) pluginInstall(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	app, name, err := agentPluginAppIDArgs(args)
	if err != nil {
		return nil, err
	}
	// 副作用前检查：各引擎安装（网络/包管理器）不可中途取消，取消后不得再
	// 触达引擎（D-06）。
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	switch app {
	case "claudecode":
		res, callErr := h.app.Plugins.InstallPlugin(name)
		return agentPluginCommandOutcome(app, claudePluginResult(res, callErr))
	case "codex":
		if err := requireCodexPluginID(name); err != nil {
			return nil, err
		}
		res, callErr := h.app.CodexPlugins.InstallPlugin(codexplugin.PluginSelector{PluginID: name})
		return agentPluginCommandOutcome(app, codexPluginResult(res, callErr))
	case "opencode":
		res, callErr := h.app.OpenCodePlugins.InstallPlugin(name)
		return agentPluginCommandOutcome(app, openCodePluginResult(res, callErr))
	case "pi":
		res, callErr := h.app.PiPlugins.InstallPackage(name)
		return agentPluginCommandOutcome(app, piPluginResult(res, callErr))
	case "omp":
		res, callErr := h.app.OmpPlugins.InstallPlugin(name)
		return agentPluginCommandOutcome(app, ompPluginResult(res, callErr))
	}
	return nil, spec.Errorf(spec.ErrBadArgs, "未知插件引擎 %q", app)
}

func (h *agentCLIHost) pluginUninstall(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	app, id, err := agentPluginAppIDArgs(args)
	if err != nil {
		return nil, err
	}
	switch app {
	case "claudecode":
		res, callErr := h.app.Plugins.UninstallPlugin(id)
		return agentPluginCommandOutcome(app, claudePluginResult(res, callErr))
	case "codex":
		if err := requireCodexPluginID(id); err != nil {
			return nil, err
		}
		res, callErr := h.app.CodexPlugins.UninstallPlugin(codexplugin.PluginSelector{PluginID: id})
		return agentPluginCommandOutcome(app, codexPluginResult(res, callErr))
	case "opencode":
		res, callErr := h.app.OpenCodePlugins.UninstallPlugin(id)
		return agentPluginCommandOutcome(app, openCodePluginResult(res, callErr))
	case "pi":
		res, callErr := h.app.PiPlugins.RemovePackage(id)
		return agentPluginCommandOutcome(app, piPluginResult(res, callErr))
	case "omp":
		res, callErr := h.app.OmpPlugins.UninstallPlugin(id)
		return agentPluginCommandOutcome(app, ompPluginResult(res, callErr))
	}
	return nil, spec.Errorf(spec.ErrBadArgs, "未知插件引擎 %q", app)
}

// ---- 公共小件 ----

// agentPluginAppIDArgs 提取并校验 (app, id/name) 两个位置参数。
func agentPluginAppIDArgs(args spec.Args) (string, string, error) {
	app := strings.TrimSpace(args.String("app"))
	id := strings.TrimSpace(args.String("id"))
	if app == "" {
		return "", "", spec.Errorf(spec.ErrBadArgs, "app 不能为空（claudecode|codex|opencode|pi|omp）")
	}
	if !validAgentPluginApp(app) {
		return "", "", spec.Errorf(spec.ErrBadArgs, "app 必须是 %s 之一，得到 %q", strings.Join(agentPluginAppOrder, "|"), app)
	}
	if id == "" {
		return "", "", spec.Errorf(spec.ErrBadArgs, "插件标识不能为空")
	}
	return app, id, nil
}

// agentCodexPluginIDPattern 与 codexplugin 的 plugin@marketplace 形态对齐（轻量预检，
// 完整字符集校验仍由引擎负责）。
var agentCodexPluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+$`)

func requireCodexPluginID(id string) *spec.Error {
	if strings.Count(id, "@") != 1 || !agentCodexPluginIDPattern.MatchString(id) {
		return spec.Errorf(spec.ErrBadArgs, "codex 插件标识须为 plugin@marketplace 形态（仅字母/数字/点/下划线/连字符），得到 %q", id)
	}
	return nil
}

// pluginOutcome 是各引擎 CommandResult 的归一化形态。
type pluginOutcome struct {
	success bool
	output  string
	err     error
}

// 五引擎的 CommandResult 是不同具名类型，逐引擎写 nil 安全的归一化适配器。
func claudePluginResult(res *plugin.CommandResult, err error) pluginOutcome {
	if res == nil {
		return pluginOutcome{err: err}
	}
	return pluginOutcome{success: res.Success, output: res.Output, err: err}
}

func codexPluginResult(res *codexplugin.CommandResult, err error) pluginOutcome {
	if res == nil {
		return pluginOutcome{err: err}
	}
	return pluginOutcome{success: res.Success, output: res.Output, err: err}
}

func openCodePluginResult(res *opencodeplugin.CommandResult, err error) pluginOutcome {
	if res == nil {
		return pluginOutcome{err: err}
	}
	return pluginOutcome{success: res.Success, output: res.Output, err: err}
}

func piPluginResult(res *piplugin.CommandResult, err error) pluginOutcome {
	if res == nil {
		return pluginOutcome{err: err}
	}
	return pluginOutcome{success: res.Success, output: res.Output, err: err}
}

func ompPluginResult(res *ompplugin.CommandResult, err error) pluginOutcome {
	if res == nil {
		return pluginOutcome{err: err}
	}
	return pluginOutcome{success: res.Success, output: res.Output, err: err}
}

// agentPluginCommandOutcome 统一收口：引擎报错或 Success=false → internal 业务错误
// （消息含引擎输出，供 agent 自纠）；成功 → {app, success, output}。
// 引擎的入参校验类错误（如非法 spec）也走此路径——引擎无类型化错误可区分，
// 已在交付报告中提出更细分类的契约改进项。
func agentPluginCommandOutcome(app string, o pluginOutcome) (any, error) {
	if o.err != nil {
		msg := strings.TrimSpace(o.output)
		if msg != "" {
			msg += ": "
		}
		return nil, spec.Errorf(spec.ErrInternal, "%s 插件操作失败: %s%v", app, msg, o.err)
	}
	if !o.success {
		msg := strings.TrimSpace(o.output)
		if msg == "" {
			msg = "引擎未返回失败原因"
		}
		return nil, spec.Errorf(spec.ErrInternal, "%s 插件操作失败: %s", app, msg)
	}
	return map[string]any{"app": app, "success": true, "output": strings.TrimSpace(o.output)}, nil
}
