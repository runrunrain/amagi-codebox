package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/sharedmarket"
	"amagi-codebox/internal/sharedmcp"
)

type agentLocalOperation func(spec.Args) (any, error)

// Some desktop operations have no cancellable API. Check the request before
// entering them, and leave their completion and rollback to the domain service.
func agentLocalHandlers(operations map[string]agentLocalOperation) map[string]spec.Handler {
	out := make(map[string]spec.Handler, len(operations))
	for name, operation := range operations {
		out[name] = func(ctx context.Context, args spec.Args) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, agentCLICtxErr(err)
			}
			return operation(args)
		}
	}
	return out
}

func agentLocalDone(err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]bool{"success": true}, nil
}

func (h *agentCLIHost) sharedHandlers() map[string]spec.Handler {
	a := h.app
	return agentLocalHandlers(map[string]agentLocalOperation{
		"shared.skill.list": func(spec.Args) (any, error) { return a.ListSharedSkills() },
		"shared.skill.import": func(args spec.Args) (any, error) {
			path := args.String("path")
			if !filepath.IsAbs(path) {
				return nil, agentCLIArgErrf("请提供桌面主机上的绝对路径")
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if info.IsDir() {
				return a.ImportSharedSkillsDir(path)
			}
			if !strings.EqualFold(filepath.Ext(path), ".zip") || !info.Mode().IsRegular() {
				return nil, agentCLIArgErrf("技能导入路径必须指向目录或 ZIP 文件")
			}
			return a.ImportSharedSkillsZip(path)
		},
		"shared.skill.enable": func(args spec.Args) (any, error) {
			return a.SetSharedSkillEnabled(args.String("name"), true)
		},
		"shared.skill.disable": func(args spec.Args) (any, error) {
			return a.SetSharedSkillEnabled(args.String("name"), false)
		},
		"shared.skill.delete": func(args spec.Args) (any, error) { return a.DeleteSharedSkill(args.String("name")) },
		"shared.mcp.list":     func(spec.Args) (any, error) { return agentMcpOverview(a.ListSharedMcp()) },
		"shared.mcp.preview": func(args spec.Args) (any, error) {
			preview, err := a.ParseSharedMcpImportText(args.String("config"))
			return preview, agentMcpError(err)
		},
		"shared.mcp.import": func(args spec.Args) (any, error) {
			result, err := a.ImportSharedMcpEntries(args.String("config"))
			if err != nil {
				return nil, agentMcpError(err)
			}
			overview, _ := agentMcpOverview(&result.Overview, nil)
			return map[string]any{"imported": result.Imported, "skipped": result.Skipped, "overview": overview}, nil
		},
		"shared.mcp.edit": func(args spec.Args) (any, error) {
			var entry sharedmcp.SharedMcpEntry
			if err := json.Unmarshal([]byte(args.String("config")), &entry); err != nil {
				return nil, agentCLIArgErrf("请经 stdin 提供合法的 MCP 条目 JSON 对象")
			}
			return agentMcpOverview(a.UpdateSharedMcpEntry(args.String("name"), entry))
		},
		"shared.mcp.distribute": func(args spec.Args) (any, error) {
			return agentMcpOverview(a.SetSharedMcpEngineSelection(args.String("name"), args.String("engine"), true, args.Bool("force")))
		},
		"shared.mcp.undistribute": func(args spec.Args) (any, error) {
			return agentMcpOverview(a.SetSharedMcpEngineSelection(args.String("name"), args.String("engine"), false, args.Bool("force")))
		},
		"shared.mcp.delete": func(args spec.Args) (any, error) {
			return agentMcpOverview(a.DeleteSharedMcpEntry(args.String("name"), args.Bool("force")))
		},
		"shared.market.list": func(spec.Args) (any, error) { return agentMarketList(a.ListSharedMarkets()) },
		"shared.market.claude-sources": func(spec.Args) (any, error) {
			sources, err := a.ListClaudeMarketplacesForImport()
			for i := range sources {
				sources[i].Source = agentSafeSource(sources[i].Source)
				sources[i].URL = agentSafeSource(sources[i].URL)
			}
			return sources, agentExternalError(err)
		},
		"shared.market.add": func(args spec.Args) (any, error) {
			source := strings.TrimSpace(args.String("source"))
			if strings.HasPrefix(source, "-") {
				return nil, agentCLIArgErrf("市场源需要 Git 地址或仓库路径")
			}
			if parsed, err := url.Parse(source); err == nil && parsed.Host != "" {
				_, hasPassword := parsed.User.Password()
				if hasPassword || (parsed.User != nil && !strings.EqualFold(parsed.Scheme, "ssh")) || parsed.RawQuery != "" || parsed.Fragment != "" {
					return nil, agentCLIArgErrf("Git 地址请使用已配置的凭据或 SSH，避免在参数中附带凭据、查询参数或片段")
				}
			}
			return agentMarketList(a.AddSharedMarketplace(source))
		},
		"shared.market.import-claude": func(args spec.Args) (any, error) {
			name := args.String("market")
			sources, err := a.ListClaudeMarketplacesForImport()
			if err != nil {
				return nil, agentExternalError(err)
			}
			for _, source := range sources {
				if source.Name == name {
					return agentMarketList(a.ImportSharedMarketplacesFromClaude([]string{name}))
				}
			}
			return nil, spec.Errorf(spec.ErrNotFound, "Claude Code 中没有市场 %q", name)
		},
		"shared.market.update": func(args spec.Args) (any, error) {
			return agentMarketList(a.UpdateSharedMarketplace(args.String("market")))
		},
		"shared.market.update-plugin": func(args spec.Args) (any, error) {
			report, err := a.UpdateSharedMarketplacePlugin(args.String("market"), args.String("plugin"))
			return report, agentExternalError(err)
		},
		"shared.market.deploy": func(args spec.Args) (any, error) {
			return a.DeploySharedMarketplacePlugin(args.String("market"), args.String("plugin"))
		},
		"shared.market.uninstall": func(args spec.Args) (any, error) {
			return a.UninstallSharedMarketplacePlugin(args.String("market"), args.String("plugin"))
		},
		"shared.market.remove": func(args spec.Args) (any, error) {
			return agentMarketList(a.RemoveSharedMarketplace(args.String("market")))
		},
	})
}

type agentMcpEntry struct {
	Name              string                          `json:"name"`
	Type              string                          `json:"type"`
	NameDistributable bool                            `json:"nameDistributable"`
	Engines           map[string]sharedmcp.EngineCell `json:"engines"`
}

type agentMcpStatus struct {
	Entries        []agentMcpEntry        `json:"entries"`
	RegistryExists bool                   `json:"registryExists"`
	RegistryPath   string                 `json:"registryPath"`
	EnginesMeta    []sharedmcp.EngineMeta `json:"enginesMeta"`
}

func agentMcpOverview(view *SharedMcpOverview, err error) (agentMcpStatus, error) {
	if err != nil {
		return agentMcpStatus{}, agentMcpError(err)
	}
	entries := make([]agentMcpEntry, 0, len(view.Entries))
	for _, row := range view.Entries {
		entries = append(entries, agentMcpEntry{row.Entry.Name, row.Entry.Type, row.NameDistributable, row.Engines})
	}
	return agentMcpStatus{entries, view.RegistryExists, view.RegistryPath, view.EnginesMeta}, nil
}

func agentMcpError(err error) error {
	if err == nil {
		return nil
	}
	code := spec.ErrInternal
	switch sharedmcp.KindOf(err) {
	case sharedmcp.KindNotFound:
		code = spec.ErrNotFound
	case sharedmcp.KindExists, sharedmcp.KindConflict, sharedmcp.KindForeignModified:
		code = spec.ErrConflict
	case sharedmcp.KindInvalidEntry, sharedmcp.KindNameInvalid, sharedmcp.KindUnsupported:
		code = spec.ErrBadArgs
	}
	return spec.Errorf(code, "%s", agentSafeDiagnostic(err.Error()))
}

var agentURLInDiagnostic = regexp.MustCompile(`(?i)\b(?:https?|ssh|git)://[^\s"'<>]+`)

func agentSafeSource(source string) string {
	parsed, err := url.Parse(source)
	if err == nil && parsed.Host != "" {
		parsed.User, parsed.RawQuery, parsed.Fragment, parsed.RawFragment, parsed.ForceQuery = nil, "", "", "", false
		source = parsed.String()
	} else if err != nil && agentURLInDiagnostic.MatchString(source) {
		return "<无效 URL>"
	}
	return agentCLIScrubCredentials(source, nil)
}

func agentSafeDiagnostic(message string) string {
	return agentCLIScrubCredentials(agentURLInDiagnostic.ReplaceAllStringFunc(message, agentSafeSource), nil)
}

func agentExternalError(err error) error {
	if err == nil {
		return nil
	}
	return spec.Errorf(spec.ErrInternal, "%s", agentSafeDiagnostic(err.Error()))
}

func agentMarketList(markets []sharedmarket.Marketplace, err error) (any, error) {
	if err != nil {
		return nil, agentExternalError(err)
	}
	for i := range markets {
		markets[i].Source = agentSafeSource(markets[i].Source)
	}
	return markets, nil
}
