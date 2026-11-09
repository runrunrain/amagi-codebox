package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli"
	"amagi-codebox/internal/agentcli/cmds"
	"amagi-codebox/internal/agentcli/install"
	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/gitassist"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/usage"
)

// newAgentCLIHostFixture 构造带 Settings/EnvCheck/Usage/GitAssist 的最小 App
// （复用 app_test.go 的内存密钥库夹具，避免触碰真实 Keychain/用户目录）。
func newAgentCLIHostFixture(t *testing.T) (*agentCLIHost, *App, string) {
	t.Helper()
	app, configDir := newTestAppWithConfigDir(t)

	settingsSvc := settings.NewService(configDir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	app.Settings = settingsSvc
	app.EnvCheck = envcheck.NewService()

	usageSvc := usage.NewService(configDir, app.Log)
	if err := usageSvc.Load(); err != nil {
		t.Fatalf("load usage: %v", err)
	}
	t.Cleanup(func() { _ = usageSvc.Close() })
	app.Usage = usageSvc

	app.GitAssist = gitassist.New(app.Config, settingsSvc, func(string) string { return "" })
	app.configDir = configDir
	// 安装管理器：home 与技能目标全部指向临时目录，避免 cli info 检查真实
	// ~/.agents / ~/.claude / ~/.codex。
	app.agentCLIInstall = install.New(configDir, t.TempDir(), filepath.Join(configDir, "testbin", "codebox-app"), cmds.Table())
	return newAgentCLIHost(app), app, configDir
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func specErrCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误，实际为 nil")
	}
	se, ok := err.(*spec.Error)
	if !ok {
		t.Fatalf("期望 *spec.Error，实际 %T: %v", err, err)
	}
	return se.Code
}

// ---- 错误映射 ----

func TestAgentCLIServiceErrClassification(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"provider not found: acme", spec.ErrNotFound},
		{"env var not found: FOO", spec.ErrNotFound},
		{"读取分支列表失败: exit 128", spec.ErrNotFound},
		{"invalid provider name \"a/b\"", spec.ErrBadArgs},
		{"provider name is required", spec.ErrBadArgs},
		{"reserved key \"WSLENV\" cannot ...", spec.ErrBadArgs},
		{"usage service not loaded", spec.ErrNotReady},
		{"provider synchronization services are not initialized", spec.ErrNotReady},
		{"disk exploded", spec.ErrInternal},
	}
	for _, c := range cases {
		if got := agentCLIServiceErr(errString(c.msg)).Code; got != c.want {
			t.Errorf("agentCLIServiceErr(%q) = %s, want %s", c.msg, got, c.want)
		}
	}
}

// ---- status ----

func TestAgentCLIStatus(t *testing.T) {
	h, app, configDir := newAgentCLIHostFixture(t)
	// 挂真实（未启动）Server：agentCli 段收用 Server.Status（C2），不读端点文件。
	app.agentCLI = agentcli.NewServer(configDir, "test", cmds.Table(), map[string]spec.Handler{}, nil)
	data, err := h.handleStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("status 返回类型 %T", data)
	}
	for _, key := range []string{"app", "platform", "envCheck", "sessions", "remote", "agentCli"} {
		if _, ok := m[key]; !ok {
			t.Errorf("status 缺少段 %s", key)
		}
	}
	envView := m["envCheck"].(agentCLIEnvStatusView)
	if envView.Checked {
		t.Errorf("未检测过的 envcheck 应 checked=false")
	}
	agentCli := m["agentCli"].(map[string]any)
	if agentCli["endpointPath"] != filepath.Join(configDir, "run", "endpoint.json") {
		t.Errorf("endpointPath = %v", agentCli["endpointPath"])
	}
	if agentCli["running"] != false || agentCli["enabled"] != true {
		t.Errorf("未启动服务应 enabled=true/running=false：%#v", agentCli)
	}
	// 脱敏基线：status 输出不得出现 token 字段。
	if strings.Contains(mustJSON(t, m), `"token"`) {
		t.Errorf("status 输出含 token 字段")
	}
}

func TestAgentCLIRemoteStatusMasksToken(t *testing.T) {
	app := newTestAppWithRemote(t)
	app.configDir = t.TempDir()
	h := newAgentCLIHost(app)
	data, err := h.handleRemoteStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("remote status: %v", err)
	}
	m := data.(map[string]any)
	if _, has := m["token"]; has {
		t.Fatalf("remote status 暴露 token 字段")
	}
	if _, has := m["tokenConfigured"]; !has {
		t.Errorf("remote status 缺少 tokenConfigured")
	}
	for _, key := range []string{"enabled", "running", "host", "port"} {
		if _, has := m[key]; !has {
			t.Errorf("remote status 缺少 %s", key)
		}
	}
}

// ---- cli info ----

func TestAgentCLICLIInfo(t *testing.T) {
	h, app, configDir := newAgentCLIHostFixture(t)
	// 挂真实（未启动）Server：cli info 的 server 段收用 Server.Status，
	// 不再 Stat/读取端点文件内容。
	app.agentCLI = agentcli.NewServer(configDir, "test", cmds.Table(), map[string]spec.Handler{}, nil)
	data, err := h.handleCLIInfo(context.Background(), nil)
	if err != nil {
		t.Fatalf("cli info: %v", err)
	}
	m := data.(map[string]any)
	if m["home"] != configDir {
		t.Errorf("home = %v, want %s", m["home"], configDir)
	}
	shim := m["shim"].(map[string]any)
	if shim["path"] != filepath.Join(configDir, "bin", "codebox-agent") {
		t.Errorf("shim.path = %v", shim["path"])
	}
	if shim["status"] != string(install.StateNotInstalled) {
		t.Errorf("shim 初始应 not-installed：%v", shim["status"])
	}
	skills := m["skills"].(map[string]any)
	if len(skills) != 3 {
		t.Fatalf("skills 应含三个目标：%#v", skills)
	}
	for _, target := range []string{"agents", "claude", "codex"} {
		sv := skills[target].(map[string]any)
		if sv["status"] != string(install.StateNotInstalled) || sv["path"] == "" {
			t.Errorf("skill %s 状态异常：%#v", target, sv)
		}
	}
	server := m["server"].(map[string]any)
	if server["running"] != false || server["enabled"] != true {
		t.Errorf("server 段应来自 Server.Status（enabled=true/running=false）：%#v", server)
	}
	if server["endpointPath"] != filepath.Join(configDir, "run", "endpoint.json") {
		t.Errorf("server.endpointPath = %v", server["endpointPath"])
	}
	// 安装后状态翻转到 up-to-date（真实 Manager + 临时目录）。
	if _, err := app.InstallAgentCLIShim(); err != nil {
		t.Fatalf("InstallAgentCLIShim: %v", err)
	}
	data2, err := h.handleCLIInfo(context.Background(), nil)
	if err != nil {
		t.Fatalf("cli info 2: %v", err)
	}
	if got := data2.(map[string]any)["shim"].(map[string]any)["status"]; got != string(install.StateUpToDate) {
		t.Errorf("安装后 shim.status = %v, want up-to-date", got)
	}
	// 脱敏基线：cli info 输出不含令牌字段。
	if strings.Contains(mustJSON(t, data2), "cliToken") {
		t.Errorf("cli info 输出泄露 endpoint 内容")
	}
}

// ---- env ----

func TestAgentCLIEnvStatusNotChecked(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	data, err := h.handleEnvStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("env status: %v", err)
	}
	view := data.(agentCLIEnvStatusView)
	if view.Checked || view.AllOK || len(view.Tools) != 0 {
		t.Errorf("空缓存应返回未检测视图: %+v", view)
	}
}

func TestAgentCLIEnvCheckBadTool(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	_, err := h.handleEnvCheck(context.Background(), spec.Args{"tool": "nope"})
	if code := specErrCode(t, err); code != spec.ErrBadArgs {
		t.Errorf("env check 未知工具错误码 = %s, want bad_args", code)
	}
}

func TestAgentCLIEnvInstallBadTool(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	// 合法工具名但走校验失败路径会触发真实安装，故用非法工具名只测校验。
	_, err := h.handleEnvInstall(context.Background(), spec.Args{"tool": "definitely-not"})
	if code := specErrCode(t, err); code != spec.ErrBadArgs {
		t.Errorf("env install 未知工具错误码 = %s, want bad_args", code)
	}
	_, err = h.handleEnvInstall(context.Background(), spec.Args{})
	if code := specErrCode(t, err); code != spec.ErrBadArgs {
		t.Errorf("env install 缺参错误码 = %s, want bad_args", code)
	}
}

func TestAgentCLIServiceNotReadyGuards(t *testing.T) {
	app, configDir := newTestAppWithConfigDir(t)
	app.configDir = configDir
	h := newAgentCLIHost(app)
	for name, fn := range map[string]func() (any, error){
		"env.status":    func() (any, error) { return h.handleEnvStatus(context.Background(), nil) },
		"settings.get":  func() (any, error) { return h.handleSettingsGet(context.Background(), nil) },
		"usage.summary": func() (any, error) { return h.handleUsageSummary(context.Background(), spec.Args{}) },
		"git.repo-info": func() (any, error) {
			return h.handleGitRepoInfo(context.Background(), spec.Args{"dir": configDir})
		},
	} {
		_, err := fn()
		if code := specErrCode(t, err); code != spec.ErrNotReady {
			t.Errorf("%s 未初始化服务应映射 not_ready，实际 %v", name, err)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestAgentCLIHandlersEntryContextCheck D-06 回归：所有 Handler 入口先检查
// ctx.Err()——已取消/超时的请求不得继续执行（返回 not_ready），无论后续业务
// 是否可用。修复前各 Handler 把 ctx 命名为 _ 直接放行。
func TestAgentCLIHandlersEntryContextCheck(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, call := range map[string]func() (any, error){
		"status":        func() (any, error) { return h.handleStatus(ctx, nil) },
		"cli.info":      func() (any, error) { return h.handleCLIInfo(ctx, nil) },
		"env.status":    func() (any, error) { return h.handleEnvStatus(ctx, nil) },
		"provider.list": func() (any, error) { return h.handleProviderList(ctx, spec.Args{}) },
		"provider.get":  func() (any, error) { return h.handleProviderGet(ctx, spec.Args{"name": "ghost"}) },
		"key.status":    func() (any, error) { return h.handleKeyStatus(ctx, spec.Args{}) },
		"preset.list":   func() (any, error) { return h.handlePresetList(ctx, spec.Args{"type": "claude_code"}) },
		"settings.get":  func() (any, error) { return h.handleSettingsGet(ctx, nil) },
		"envvars.list":  func() (any, error) { return h.handleEnvVarsList(ctx, nil) },
		"workdir.list":  func() (any, error) { return h.handleWorkdirList(ctx, nil) },
		"usage.summary": func() (any, error) { return h.handleUsageSummary(ctx, spec.Args{}) },
		"logs":          func() (any, error) { return h.handleLogs(ctx, spec.Args{}) },
		"session.list":  func() (any, error) { return h.sessionList(ctx, spec.Args{}) },
		"session.get":   func() (any, error) { return h.sessionGet(ctx, spec.Args{"sessionId": "ghost"}) },
	} {
		_, err := call()
		if err == nil {
			t.Errorf("%s：已取消 ctx 下 Handler 不应成功返回", name)
			continue
		}
		if code := specErrCode(t, err); code != spec.ErrNotReady {
			t.Errorf("%s：已取消 ctx 应在入口返回 not_ready，得到 %s（%v）", name, code, err)
		}
	}
}
