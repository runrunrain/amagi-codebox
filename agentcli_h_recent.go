package main

import (
	"encoding/json"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/aiproxy"
	"amagi-codebox/internal/aiproxy/router"
)

func (h *agentCLIHost) recentHandlers() map[string]spec.Handler {
	a := h.app
	return agentLocalHandlers(map[string]agentLocalOperation{
		"env.claude-config":       h.agentClaudeConfig,
		"env.apply-claude-config": h.agentApplyClaudeConfig,
		"session.target":          h.agentSessionTarget,
		"session.directories": func(args spec.Args) (any, error) {
			listing, err := a.SessionListDirectories(args.String("sessionId"), args.String("root"))
			if err != nil {
				return nil, mapAgentSessionError(err)
			}
			return json.RawMessage(listing), nil
		},
		"session.web-status": h.agentSessionWebStatus,
		"proxy.status":       func(spec.Args) (any, error) { return agentProxyStatus(a.GetAIProxyState()) },
		"proxy.traffic": func(args spec.Args) (any, error) {
			offset := args.Int("offset", 0)
			if offset < 0 {
				return nil, agentCLIArgErrf("offset 不能小于 0")
			}
			return a.GetAIProxyTraffic(router.TrafficQuery{
				Search: args.String("search"), Verdict: args.String("verdict"), Provider: args.String("provider"),
				Exit: args.String("exit"), Since: args.String("since"), Until: args.String("until"), Offset: offset,
			})
		},
		"proxy.save":  h.agentProxySave,
		"proxy.start": func(spec.Args) (any, error) { return agentProxyStatus(a.StartAIProxy()) },
		"proxy.stop":  func(spec.Args) (any, error) { return agentProxyStatus(a.StopAIProxy()) },
	})
}

func (h *agentCLIHost) agentClaudeConfig(spec.Args) (any, error) {
	if h.app.EnvCheck == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "环境检测服务未初始化")
	}
	status, err := h.app.CheckClaudeConfig()
	if err != nil {
		return nil, agentExternalError(err)
	}
	knownSecrets := h.knownStoredSecrets()
	for i := range status.ConfigItems {
		item := &status.ConfigItems[i]
		item.CurrentValue = agentClaudeConfigValue(item.Key, item.CurrentValue, knownSecrets)
	}
	return status, nil
}

func agentClaudeConfigValue(key, value string, knownSecrets []string) string {
	if value == "" {
		return ""
	}
	switch key {
	case "permissions.allow", "permissions.deny", "permissions.ask":
		// Permission patterns can include arbitrary shell commands and credentials.
		return "已隐藏权限规则内容"
	default:
		return agentCLIScrubCredentials(agentSafeDiagnostic(value), knownSecrets)
	}
}

func (h *agentCLIHost) agentApplyClaudeConfig(args spec.Args) (any, error) {
	a := h.app
	if a.EnvCheck == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "环境检测服务未初始化")
	}
	status, err := a.CheckClaudeConfig()
	if err != nil {
		return nil, agentExternalError(err)
	}
	key := args.String("key")
	for _, item := range status.ConfigItems {
		if item.Key != key {
			continue
		}
		if item.Configured {
			return map[string]bool{"success": true, "changed": false}, nil
		}
		result, err := a.FixClaudeConfig(item.Key, item.DefaultValue, item.FilePath)
		if err != nil {
			return nil, agentExternalError(err)
		}
		if result == nil || !result.Success {
			message := "配置写入失败"
			if result != nil {
				message = strings.TrimSpace(message + " " + result.Error + " " + result.Message)
			}
			return nil, spec.Errorf(spec.ErrInternal, "%s", agentSafeDiagnostic(message))
		}
		result.PreviousValue = agentClaudeConfigValue(key, result.PreviousValue, h.knownStoredSecrets())
		return result, nil
	}
	return nil, agentCLIArgErrf("配置项不存在，请先执行 env claude-config")
}

func (h *agentCLIHost) agentSessionTarget(args spec.Args) (any, error) {
	a := h.app
	if a.Sessions == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "会话服务未初始化")
	}
	id := strings.TrimSpace(args.String("sessionId"))
	launch, err := a.Sessions.LaunchContextByID(id)
	if err != nil {
		return nil, mapAgentSessionError(err)
	}
	wsl, distro, err := sessionWSLTargetFn(a, id, launch.Mode)
	if err != nil {
		return nil, mapAgentSessionError(err)
	}
	return map[string]any{
		"sessionId": id, "wsl": wsl, "distro": distro, "mode": launch.Mode, "workDir": launch.Workdir,
	}, nil
}

func (h *agentCLIHost) agentSessionWebStatus(args spec.Args) (any, error) {
	a := h.app
	if a.Sessions == nil || a.WebUI == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "会话 Web 视图服务未初始化")
	}
	id := strings.TrimSpace(args.String("sessionId"))
	if _, err := a.GetSession(id); err != nil {
		return nil, mapAgentSessionError(err)
	}
	status := a.WebUI.GetWebUIStatus(id)
	if args.Bool("probe") {
		status = a.WebUI.ProbeWebUI(id)
	}
	return map[string]any{"sessionId": id, "state": status.State}, nil
}

func (h *agentCLIHost) agentProxySave(args spec.Args) (any, error) {
	value, err := agentCLIParseJSONArg(args["settings"])
	obj, ok := value.(map[string]any)
	if err != nil || !ok || obj["routing"] == nil {
		return nil, agentCLIArgErrf("settings 必须是含 routing 的完整 JSON 对象")
	}
	var settings aiproxy.Settings
	encoded, err := json.Marshal(obj)
	if err != nil || json.Unmarshal(encoded, &settings) != nil {
		return nil, agentCLIArgErrf("AI 分流配置字段无效")
	}
	return agentProxyStatus(h.app.SaveAIProxySettings(settings))
}

func agentProxyStatus(state aiproxy.State, err error) (any, error) {
	if err != nil {
		return nil, agentExternalError(err)
	}
	state.Status.Logs = ""
	state.Status.Error = agentSafeDiagnostic(state.Status.Error)
	state.Error = agentSafeDiagnostic(state.Error)
	state.Source.Error = agentSafeDiagnostic(state.Source.Error)
	return state, nil
}
