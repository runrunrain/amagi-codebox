package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"amagi-codebox/internal/agentcli/install"
	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/session"
)

// 本文件实现 system 域 Handler（status / cli info / env / remote status），
// 并承载 D1 各域 Handler 共用的错误映射与 JSON 参数归一化辅助函数。

// ---- D1 共用辅助（system/config/usage/git 四域 Handler 使用）----

// agentCLIArgErrf 构造参数/用法错误（bad_args → 退出码 2）。
func agentCLIArgErrf(format string, a ...any) *spec.Error {
	return spec.Errorf(spec.ErrBadArgs, format, a...)
}

// agentCLINotFoundErrf 构造资源不存在错误（not_found → 退出码 1）。
func agentCLINotFoundErrf(format string, a ...any) *spec.Error {
	return spec.Errorf(spec.ErrNotFound, format, a...)
}

// agentCLIWrapErr 把任意 error 归一为 *spec.Error：已是 *spec.Error 原样透传，
// 其余按 internal 包装（不吞原始错误文本）。
func agentCLIWrapErr(err error) *spec.Error {
	if err == nil {
		return nil
	}
	if se, ok := err.(*spec.Error); ok {
		return se
	}
	return spec.Errorf(spec.ErrInternal, "%s", err.Error())
}

// agentCLIServiceErr 按服务层错误的常见措辞映射稳定错误码：
// not found/不存在 → not_found；invalid/required/unsupported/保留/reserved/empty → bad_args；
// not loaded/not initialized/未初始化 → not_ready；其余 → internal。
func agentCLIServiceErr(err error) *spec.Error {
	if err == nil {
		return nil
	}
	if se, ok := err.(*spec.Error); ok {
		return se
	}
	msg := err.Error()
	switch {
	case containsAny(msg, "not found", "不存在", "读取分支列表失败"):
		return spec.Errorf(spec.ErrNotFound, "%s", msg)
	case containsAny(msg, "invalid", "required", "unsupported", "reserved", "cannot", "must not", "must be", "is empty", "保留"):
		return spec.Errorf(spec.ErrBadArgs, "%s", msg)
	case containsAny(msg, "not loaded", "not initialized", "未初始化", "services are not initialized"):
		return spec.Errorf(spec.ErrNotReady, "%s", msg)
	default:
		return spec.Errorf(spec.ErrInternal, "%s", msg)
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// agentCLIParseJSONArg 归一 TypeJSON 参数：客户端展开后可能是 JSON 文本，
// 也可能是调用方直接投递的已解析对象；统一返回泛型值。nil/空串返回 nil。
func agentCLIParseJSONArg(v any) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return nil, nil
		}
		var out any
		if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
			return nil, agentCLIArgErrf("json 参数不是合法 JSON: %v", err)
		}
		return out, nil
	default:
		return v, nil
	}
}

// agentCLIHomeDir 返回应用配置根（~/.amagi-codebox）；测试夹具可注入 App.configDir。
func agentCLIHomeDir(a *App) string {
	if a != nil && a.configDir != "" {
		return a.configDir
	}
	return defaultConfigDir()
}

func agentCLIFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// agentCLICtxErr 把入口 ctx 检查失败映射为稳定错误：请求已超时/取消时
// Handler 不再继续执行（D-06）。App/Service 面普遍无 ctx 参数，入口检查是
// CLI 侧的取消边界；无法中途取消的长操作已在命令 Notes 写明
// “超时不等于未生效，先查询再重试”。
func agentCLICtxErr(err error) *spec.Error {
	return spec.Errorf(spec.ErrNotReady, "命令已取消或超时：%v", err)
}

// ---- status ----

type agentCLIEnvToolView struct {
	Tool           string `json:"tool"`
	Installed      bool   `json:"installed"`
	InstallMethod  string `json:"installMethod,omitempty"`
	Version        string `json:"version,omitempty"`
	HasUpdate      bool   `json:"hasUpdate"`
	PathOk         bool   `json:"pathOk"`
	ExecutablePath string `json:"executablePath,omitempty"`
}

type agentCLIEnvStatusView struct {
	Checked      bool                  `json:"checked"`
	AllOK        bool                  `json:"allOk"`
	Checking     bool                  `json:"checking"`
	CheckedAt    string                `json:"checkedAt,omitempty"`
	Issues       []string              `json:"issues"`
	Tools        []agentCLIEnvToolView `json:"tools"`
	ToolCount    int                   `json:"toolCount"`
	InstalledCnt int                   `json:"installedCount"`
}

func buildAgentCLIEnvStatusView(st *envcheck.OverallStatus) agentCLIEnvStatusView {
	view := agentCLIEnvStatusView{Issues: []string{}, Tools: []agentCLIEnvToolView{}}
	if st == nil {
		return view
	}
	// 服务端空缓存以 emptyOverallStatus（零工具项 + CheckedAt=now）初始化，
	// 因此 "已检测过" 的判定是存在真实工具项或正在检测，而非缓存非空。
	view.Checked = len(st.Items) > 0 || st.Checking
	view.AllOK = st.AllOK
	view.Checking = st.Checking
	view.CheckedAt = st.CheckedAt.Format("2006-01-02T15:04:05Z07:00")
	if st.Issues != nil {
		view.Issues = st.Issues
	}
	names := make([]string, 0, len(st.Items))
	for name := range st.Items {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cs := st.Items[name]
		view.Tools = append(view.Tools, agentCLIEnvToolView{
			Tool:           string(cs.Tool),
			Installed:      cs.Installed,
			InstallMethod:  string(cs.InstallMethod),
			Version:        cs.Version,
			HasUpdate:      cs.HasUpdate,
			PathOk:         cs.PATHOk,
			ExecutablePath: cs.ExecutablePath,
		})
		if cs.Installed {
			view.InstalledCnt++
		}
	}
	view.ToolCount = len(view.Tools)
	return view
}

func (h *agentCLIHost) handleStatus(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "app 未初始化")
	}
	appSection := map[string]any{}
	if a.Sessions != nil {
		appSection = a.GetAppInfo()
	} else {
		appSection = map[string]any{
			"productName": "Amagi CodeBox", "version": resolveAppVersion(), "configDir": agentCLIHomeDir(a),
		}
	}

	var envView agentCLIEnvStatusView
	if a.EnvCheck != nil {
		envView = buildAgentCLIEnvStatusView(a.GetEnvCheckStatus())
	}

	sessions := map[string]int{"total": 0, "running": 0, "stopping": 0, "stopped": 0, "exited": 0, "failed": 0}
	if a.Sessions != nil {
		for _, s := range a.GetSessions() {
			sessions["total"]++
			switch s.Status {
			case session.StatusRunning:
				sessions["running"]++
			case session.StatusStopping:
				sessions["stopping"]++
			case session.StatusStopped:
				sessions["stopped"]++
			case session.StatusExited:
				sessions["exited"]++
			case session.StatusFailed:
				sessions["failed"]++
			}
		}
	}

	remote := map[string]any{"initialized": false}
	if a.Remote != nil {
		remote = map[string]any{"initialized": true}
		for k, v := range a.GetRemoteStatus() {
			if k == "token" {
				continue // 令牌永不进 CLI 输出
			}
			remote[k] = v
		}
	}

	// agentCli 段收用 A1 的 Server.Status（C2）：运行态快照（enabled/running/
	// url/endpointPath/pid/startedAt），不含令牌，也不读/Stat 端点文件（“运行 =
	// 端点已发布”的不变量由 Server 维护，DESIGN §3.1）。
	agentCli := map[string]any{}
	if a.agentCLI != nil {
		st := a.agentCLI.Status()
		agentCli = map[string]any{
			"enabled":      st.Enabled,
			"running":      st.Running,
			"url":          st.URL,
			"endpointPath": st.EndpointPath,
			"pid":          st.PID,
			"startedAt":    st.StartedAt,
		}
	}

	return map[string]any{
		"app":      appSection,
		"platform": a.GetPlatformCapabilities(),
		"envCheck": envView,
		"sessions": sessions,
		"remote":   remote,
		"agentCli": agentCli,
	}, nil
}

// ---- cli info ----

// handleCLIInfo 返回 agent-CLI 自身信息：本机分发面（install 状态机，C2）+
// 服务运行态（A1 Status）。不含令牌；不读取端点文件内容（其中含令牌）。
func (h *agentCLIHost) handleCLIInfo(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "app 未初始化")
	}
	info := map[string]any{"home": agentCLIHomeDir(a)}
	// 本机分发面：shim 与三个技能目标的状态（install.Manager）。
	if m := a.agentCLIInstaller(); m != nil {
		shim := m.ShimStatus()
		info["shim"] = map[string]any{
			"path":     m.ShimPath(),
			"status":   string(shim.State),
			"binDir":   m.BinDir(),
			"pathHint": m.PathHint(),
		}
		info["command"] = filepath.Base(m.ShimPath())
		skills := make(map[string]any, len(install.SkillTargets))
		for _, t := range install.SkillTargets {
			st := m.SkillStatus(t)
			skills[string(t)] = map[string]any{"path": st.Path, "status": string(st.State)}
		}
		info["skills"] = skills
	}
	// 服务运行态（A1 Status，不含令牌；不读端点文件内容）。
	server := map[string]any{}
	if a.agentCLI != nil {
		st := a.agentCLI.Status()
		server = map[string]any{
			"enabled":      st.Enabled,
			"running":      st.Running,
			"url":          st.URL,
			"endpointPath": st.EndpointPath,
			"pid":          st.PID,
			"startedAt":    st.StartedAt,
		}
	}
	info["server"] = server
	return info, nil
}

// ---- env ----

func (h *agentCLIHost) handleEnvStatus(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.EnvCheck == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "envcheck 服务未初始化")
	}
	return buildAgentCLIEnvStatusView(h.app.GetEnvCheckStatus()), nil
}

func (h *agentCLIHost) handleEnvCheck(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.EnvCheck == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "envcheck 服务未初始化")
	}
	tool := strings.TrimSpace(args.String("tool"))
	if tool == "" {
		st, err := h.app.RunEnvCheck()
		if err != nil {
			return nil, agentCLIServiceErr(err)
		}
		return buildAgentCLIEnvStatusView(st), nil
	}
	if _, err := parseCLITool(tool); err != nil {
		return nil, agentCLIArgErrf("未知工具 %q（合法值见 --help 的 tool 枚举）", tool)
	}
	cs, err := h.app.CheckTool(tool)
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return cs, nil
}

func (h *agentCLIHost) handleEnvInstall(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.EnvCheck == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "envcheck 服务未初始化")
	}
	tool := strings.TrimSpace(args.String("tool"))
	if tool == "" {
		return nil, agentCLIArgErrf("缺少必填参数 tool")
	}
	if _, err := parseCLITool(tool); err != nil {
		return nil, agentCLIArgErrf("未知工具 %q（合法值见 --help 的 tool 枚举）", tool)
	}
	// 副作用前检查：外部安装器不可中途取消，取消后不得再启动。
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	res, err := h.app.InstallTool(tool)
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return res, nil
}

// ---- remote status ----

func (h *agentCLIHost) handleRemoteStatus(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Remote == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "remote 服务未初始化")
	}
	out := map[string]any{}
	for k, v := range a.GetRemoteStatus() {
		if k == "token" {
			continue // 令牌永不进 CLI 输出
		}
		out[k] = v
	}
	out["tokenConfigured"] = strings.TrimSpace(a.Remote.GetToken()) != ""
	return out, nil
}

func (h *agentCLIHost) systemHandlers() map[string]spec.Handler {
	return map[string]spec.Handler{
		"status":        h.handleStatus,
		"cli.info":      h.handleCLIInfo,
		"env.status":    h.handleEnvStatus,
		"env.check":     h.handleEnvCheck,
		"env.install":   h.handleEnvInstall,
		"remote.status": h.handleRemoteStatus,
	}
}
