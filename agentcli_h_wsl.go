package main

import (
	"strings"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/wslsupply"
)

func (h *agentCLIHost) wslHandlers() map[string]spec.Handler {
	a := h.app
	return agentLocalHandlers(map[string]agentLocalOperation{
		"wsl.status": func(spec.Args) (any, error) {
			return map[string]any{"cli": a.GetWSLCLIStatus(), "supply": a.GetWSLConfigSupplyStatus()}, nil
		},
		"wsl.enable": func(spec.Args) (any, error) {
			return agentWSLOutcomes(a.EnableWSLUnifiedSupply())
		},
		"wsl.disable": func(spec.Args) (any, error) { return agentLocalDone(a.DisableWSLUnifiedSupply()) },
		"wsl.skills-link": h.onWSL(func(spec.Args) (any, error) {
			return a.EnsureWSLSharedSkillsRoot(), nil
		}),
		"wsl.vision-consent": h.onWSL(func(spec.Args) (any, error) {
			return agentLocalDone(a.GrantWSLVisionConsent())
		}),
		"wsl.config.status": func(args spec.Args) (any, error) {
			return a.GetWSLCliSupplyStatus(args.String("cli")), nil
		},
		"wsl.config.preview": h.onWSL(func(args spec.Args) (any, error) {
			return a.PreviewWSLCliConfigSupply(args.String("cli")), nil
		}),
		"wsl.config.select": h.onWSL(func(args spec.Args) (any, error) {
			return agentWSLOutcomes(a.ImportWSLCliConfigKeys(args.String("cli"), args.String("unit"), args.Strings("key")), nil)
		}),
		"wsl.config.apply": h.onWSL(func(args spec.Args) (any, error) {
			return agentWSLOutcomes(a.ApplyWSLCliConfigSupply(args.String("cli")), nil)
		}),
		"wsl.config.keep-local": h.onWSL(func(args spec.Args) (any, error) {
			return agentLocalDone(a.KeepWSLCliConfigUnitLocal(args.String("cli"), args.String("unit")))
		}),
		"wsl.config.takeover": h.onWSL(func(args spec.Args) (any, error) {
			return agentLocalDone(a.TakeOverWSLCliConfigUnit(args.String("cli"), args.String("unit"), args.String("targetHash")))
		}),
		"wsl.resource.check": func(args spec.Args) (any, error) {
			return a.CheckWSLCliResourcesSupply(args.String("cli")), nil
		},
		"wsl.resource.preview": h.onWSL(func(args spec.Args) (any, error) {
			return a.PreviewWSLCliResourcesSupply(args.String("cli")), nil
		}),
		"wsl.resource.apply": h.onWSL(func(args spec.Args) (any, error) {
			return agentWSLOutcomes(a.ApplyWSLCliResourcesSupply(args.String("cli"), args.Strings("resource")), nil)
		}),
		"wsl.resource.keep-local": h.onWSL(func(args spec.Args) (any, error) {
			return agentLocalDone(a.KeepWSLCliResourceLocal(args.String("cli"), args.String("resource")))
		}),
		"wsl.resource.takeover": h.onWSL(func(args spec.Args) (any, error) {
			return agentLocalDone(a.TakeOverWSLCliResource(args.String("cli"), args.String("resource"), args.String("targetHash")))
		}),
		"wsl.opencode.mcp-preview": h.onWSL(func(spec.Args) (any, error) { return a.GetWSLOpenCodeMCPPreview(), nil }),
		"wsl.opencode.mcp-select": h.onWSL(func(args spec.Args) (any, error) {
			return agentLocalDone(a.SetWSLOpenCodeMCPSelection(args.Strings("server")))
		}),
		"wsl.pi.status": func(spec.Args) (any, error) { return agentPiWSLStatus(a.GetPiPluginWSLStatus()), nil },
		"wsl.pi.check":  func(spec.Args) (any, error) { return agentPiWSLStatus(a.CheckPiPluginWSLStatus()), nil },
	})
}

func (h *agentCLIHost) onWSL(operation agentLocalOperation) agentLocalOperation {
	return func(args spec.Args) (any, error) {
		if h.app.WSLSupply == nil || h.app.wslDefaultDistro() == "" {
			return nil, spec.Errorf(spec.ErrNotReady, "未找到可用的 WSL 配置同步目标")
		}
		value, err := operation(args)
		if err != nil {
			return nil, agentExternalError(err)
		}
		return value, nil
	}
}

// Per-unit outcomes carry partial success without hiding the units that did
// change. Errors that prevented the action itself still use the CLI error envelope.
func agentWSLOutcomes(results []WSLConfigUnitOutcome, err error) (any, error) {
	if err != nil && len(results) == 0 {
		return nil, agentExternalError(err)
	}
	for i := range results {
		results[i].Detail = agentSafeDiagnostic(results[i].Detail)
	}
	response := map[string]any{"results": results}
	if err != nil {
		response["error"] = agentSafeDiagnostic(err.Error())
	}
	return response, nil
}

func agentPiWSLStatus(status PiPluginWSLStatus) PiPluginWSLStatus {
	status.Reason = agentSafeDiagnostic(status.Reason)
	status.Snapshot.Reason = agentSafeDiagnostic(status.Snapshot.Reason)
	items := append([]wslsupply.WSLPluginItem{}, status.Snapshot.Items...)
	for i := range items {
		base, ref, hasRef := strings.Cut(items[i].Source, "#")
		items[i].Source = agentSafeDiagnostic(base)
		if hasRef {
			items[i].Source += "#" + agentCLIScrubCredentials(ref, nil)
		}
		items[i].Detail = agentSafeDiagnostic(items[i].Detail)
	}
	status.Snapshot.Items = items
	return status
}
