package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/proxy"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/tray"
	"amagi-codebox/internal/updater"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

// App 主应用结构体，负责跨服务协调和生命周期管理。
// 通过 Wails 绑定暴露给前端。
type App struct {
	ctx context.Context

	Config   *config.ConfigService
	Secrets  *secrets.SecretsService
	Launcher *launcher.LauncherService
	Proxy    *proxy.ProxyService
	Tray     *tray.Service
	Sessions *session.Manager
	Paths    *paths.PathsService
	Log      *logging.Service
	Pty      *pty.Service
	Settings *settings.Service
	Remote   *remote.Server
	EnvVars  *envvars.EnvVarsService
	Updater  *updater.Service
	Plugins  *plugin.Service
}

func NewApp() *App {
	configDir := defaultConfigDir()
	log := logging.NewService(configDir)

	app := &App{
		Config:   config.NewConfigService(configDir),
		Secrets:  secrets.NewSecretsService(configDir),
		Launcher: launcher.NewLauncherService(log),
		Proxy:    proxy.NewProxyService(),
		Tray:     tray.NewService(),
		Sessions: session.NewManager(),
		Paths:    paths.NewPathsService(configDir),
		Log:      log,
		Pty:      pty.NewService(log),
		Settings: settings.NewService(configDir),
		EnvVars:  envvars.NewEnvVarsService(configDir),
		Updater:  updater.NewService(Version, log),
		Plugins:  plugin.NewService("", log),
	}
	// Remote 先以默认端口 8680 初始化；Startup 加载 Settings 后会同步持久化的端口。
	app.Remote = remote.NewServer(8680, app, log)
	return app
}

// --- remote.AppInterface 实现 ---

func (a *App) GetSettingsService() *settings.Service   { return a.Settings }
func (a *App) GetPathsService() *paths.PathsService    { return a.Paths }
func (a *App) GetConfigService() *config.ConfigService { return a.Config }

func (a *App) GetSession(sessionID string) (session.SessionInfo, error) {
	for _, s := range a.GetSessions() {
		if s.ID == sessionID {
			return s, nil
		}
	}
	return session.SessionInfo{}, fmt.Errorf("session not found: %s", sessionID)
}

// GetRemoteToken 返回远程服务器 Bearer Token，供前端展示给用户用于移动端连接。
func (a *App) GetRemoteToken() string {
	return a.Remote.GetToken()
}

// GetRemoteStatus 返回远程服务器状态信息。
func (a *App) GetRemoteStatus() map[string]any {
	return map[string]any{
		"host":    a.Remote.GetHost(),
		"port":    a.Remote.GetPort(),
		"token":   a.Remote.GetToken(),
		"running": a.Remote.IsRunning(),
	}
}

// RegenerateRemoteToken 重新生成远程 API Token，返回新 Token。
func (a *App) RegenerateRemoteToken() string {
	token := a.Remote.RegenerateToken()
	a.Log.Info("remote", "Token 已重新生成")
	return token
}

// ToggleRemoteServer 启动或停止远程服务器。
func (a *App) ToggleRemoteServer(enabled bool) error {
	if enabled {
		if err := a.Remote.Start(a.ctx); err != nil {
			a.Log.Error("remote", "启动远程服务器失败", err.Error())
			return fmt.Errorf("start remote server: %w", err)
		}
		a.Log.Info("remote", "远程服务器已启动", fmt.Sprintf("port=%d", a.Remote.GetPort()))
	} else {
		a.Remote.Stop()
		a.Log.Info("remote", "远程服务器已停止")
	}
	return nil
}

// SetRemotePort 设置远程服务器端口（需先停止服务器，再设置端口，再启动）。
func (a *App) SetRemotePort(port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("port %d out of valid range [1024, 65535]", port)
	}
	if err := a.Settings.SetRemotePort(port); err != nil {
		return err
	}
	wasRunning := a.Remote.IsRunning()
	if wasRunning {
		a.Remote.Stop()
	}
	a.Remote.SetPort(port)
	a.Log.Info("remote", "端口已更新", fmt.Sprintf("port=%d", port))
	if wasRunning {
		if err := a.Remote.Start(a.ctx); err != nil {
			a.Log.Error("remote", "更换端口后重启服务器失败", err.Error())
			return fmt.Errorf("restart remote server on port %d: %w", port, err)
		}
		a.Log.Info("remote", "远程服务器已在新端口启动", fmt.Sprintf("port=%d", port))
	}
	return nil
}

// SetRemoteHost 设置远程服务器监听地址（需先停止服务器，再设置地址，再启动）。
func (a *App) SetRemoteHost(host string) error {
	if err := a.Settings.SetRemoteHost(host); err != nil {
		return err
	}
	wasRunning := a.Remote.IsRunning()
	if wasRunning {
		a.Remote.Stop()
	}
	a.Remote.SetHost(host)
	a.Log.Info("remote", "监听地址已更新", fmt.Sprintf("host=%s", host))
	if wasRunning {
		if err := a.Remote.Start(a.ctx); err != nil {
			a.Log.Error("remote", "更换地址后重启服务器失败", err.Error())
			return fmt.Errorf("restart remote server on host %s: %w", host, err)
		}
		a.Log.Info("remote", "远程服务器已在新地址启动", fmt.Sprintf("host=%s", host))
	}
	return nil
}

// --- remote.PtyBridge 实现（委托给 pty.Service）---

func (a *App) RegisterOutputCallback(sessionID string, id string, cb func(data []byte)) {
	a.Pty.RegisterOutputCallback(sessionID, id, cb)
}

func (a *App) UnregisterOutputCallback(sessionID string, id string) {
	a.Pty.UnregisterOutputCallback(sessionID, id)
}

func (a *App) RegisterExitCallback(sessionID string, id string, cb func(exitCode uint32)) {
	a.Pty.RegisterExitCallback(sessionID, id, cb)
}

func (a *App) UnregisterExitCallback(sessionID string, id string) {
	a.Pty.UnregisterExitCallback(sessionID, id)
}

func (a *App) RegisterResizeCallback(sessionID string, id string, cb func(cols, rows int)) {
	a.Pty.RegisterResizeCallback(sessionID, id, cb)
}

func (a *App) UnregisterResizeCallback(sessionID string, id string) {
	a.Pty.UnregisterResizeCallback(sessionID, id)
}

// Startup Wails 生命周期钩子：应用启动时加载配置和密钥。
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.Pty.SetContext(ctx)
	a.Updater.CleanupOldBinary()

	a.Log.Info("app", "应用启动")

	// 加载设置并同步 GitHub Token 到 Updater
	if err := a.Settings.Load(); err != nil {
		a.Log.Warn("app", "加载设置失败", err.Error())
	}
	if token := a.Settings.GetGitHubToken(); token != "" {
		a.Updater.SetToken(token)
	}

	if err := a.Config.Load(); err != nil {
		a.Log.Warn("app", "加载配置失败，使用默认值", err.Error())
	} else {
		a.Log.Info("app", "配置加载成功")
	}
	if err := a.Secrets.Load(); err != nil {
		a.Log.Warn("app", "加载密钥失败", err.Error())
	} else {
		a.Log.Info("app", "密钥加载成功")
	}
	if err := a.Paths.Load(); err != nil {
		a.Log.Warn("app", "加载路径失败", err.Error())
	} else {
		a.Log.Info("app", "路径加载成功")
	}
	if err := a.Settings.Load(); err != nil {
		a.Log.Warn("app", "加载设置失败", err.Error())
	} else {
		a.Log.Info("app", "设置加载成功")
		// 将持久化的远程端口和地址同步到 Remote
		if savedHost := a.Settings.GetRemoteHost(); savedHost != "" {
			a.Remote.SetHost(savedHost)
		}
		if savedPort := a.Settings.GetRemotePort(); savedPort != 8680 {
			a.Remote.SetPort(savedPort)
			a.Log.Info("app", "远程端口已从设置恢复", fmt.Sprintf("port=%d", savedPort))
		}
		// 将持久化的移动端 Web 根目录同步到 Remote
		if webRoot := a.Settings.GetMobileWebRoot(); webRoot != "" {
			a.Remote.SetWebRoot(webRoot)
			a.Log.Info("app", "移动端 Web 根目录已设置", fmt.Sprintf("path=%s", webRoot))
		}
	}
	if err := a.EnvVars.Load(); err != nil {
		a.Log.Warn("app", "加载自定义环境变量失败", err.Error())
	} else {
		a.Log.Info("app", "自定义环境变量加载成功")
	}
	if err := a.Proxy.LoadRules(defaultConfigDir()); err != nil {
		a.Log.Warn("app", "加载注入规则失败", err.Error())
	} else {
		a.Log.Info("app", "注入规则加载成功")
	}
	if err := a.Proxy.LoadBackendURLHistory(defaultConfigDir()); err != nil {
		a.Log.Warn("app", "加载后端URL历史记录失败", err.Error())
	} else {
		a.Log.Info("app", "后端URL历史记录加载成功")
	}

	// 启动远程 API 服务器
	if err := a.Remote.Start(ctx); err != nil {
		a.Log.Warn("app", "远程服务器启动失败（不影响主功能）", err.Error())
	} else {
		a.Log.Info("app", "远程服务器已启动", fmt.Sprintf("port=%d", a.Remote.GetPort()))
	}

	// 启动系统托盘
	a.Tray.Start(ctx, trayIcon, func() {
		a.Shutdown(ctx)
		wailsRuntime.Quit(ctx)
	})
	a.Log.Info("app", "系统托盘已启动")
}

// Shutdown Wails 生命周期钩子：应用关闭前停止代理和进程。
func (a *App) Shutdown(ctx context.Context) {
	a.Log.Info("app", "应用关闭中...")

	// 先保存配置和密钥
	if err := a.SaveAllConfig(); err != nil {
		a.Log.Error("app", "关闭时保存配置失败", err.Error())
	}

	a.Tray.Stop()
	a.Remote.Stop()
	if a.Proxy.IsRunning() {
		if err := a.Proxy.Stop(); err != nil {
			a.Log.Error("app", "关闭代理失败", err.Error())
		}
	}
	// 停止所有终端进程
	a.Launcher.StopAll()
	a.Pty.CloseAll()
	a.Log.Info("app", "应用已关闭")
	a.Log.Close()
}

// --- 多终端会话管理 ---

// LaunchSession 启动一个新的终端会话
func (a *App) LaunchSession(providerName, presetName string, mode string, workDir string, useProxy bool, shellPath string) (string, error) {
	a.Log.Info("session", "启动会话请求", fmt.Sprintf("provider=%s preset=%s mode=%s workDir=%s proxy=%v shell=%s", providerName, presetName, mode, workDir, useProxy, shellPath))

	provider, err := a.Config.GetProvider(providerName)
	if err != nil {
		a.Log.Error("session", "获取提供商失败", err.Error())
		return "", fmt.Errorf("get provider: %w", err)
	}
	if strings.EqualFold(provider.Type, "openai") || provider.AuthKey == "OPENAI_API_KEY" {
		a.Log.Error("session", "ClaudeCode 不支持 OpenAI 类型提供商", "provider="+providerName)
		return "", fmt.Errorf("provider %q is OpenAI-compatible and cannot be used to launch ClaudeCode", providerName)
	}

	// OAuth 模式（Anthropic）：白板启动，不设置任何代理环境变量
	// 非 OAuth 模式：正常代理启动，设置 ANTHROPIC_API_KEY 和 ANTHROPIC_BASE_URL
	var apiKey, keySource string
	if provider.AuthKey == config.AuthTypeOAuth {
		// OAuth 模式不需要 API 密钥，使用 Claude Code 原生 OAuth 认证
		apiKey = ""
		keySource = "oauth"
		a.Log.Info("session", "使用 OAuth 认证（白板启动）", "provider="+providerName)
	} else {
		apiKey, keySource = a.Secrets.GetAPIKeyWithFallback(providerName)
		if apiKey == "" {
			a.Log.Error("session", "未找到API密钥", "provider="+providerName)
			return "", fmt.Errorf("no API key found for provider %q", providerName)
		}
		a.Log.Info("session", "API密钥已获取",
			fmt.Sprintf("provider=%s source=%s key=%s len=%d",
				providerName, keySource, secrets.MaskKey(apiKey), len(apiKey)))
	}

	agentTeams := a.Config.GetAgentTeams()

	// 确定模型名称
	preset, ok := provider.Presets[presetName]
	model := provider.DefaultModel
	if ok && preset.Model != "" {
		model = preset.Model
	}

	// 确定启动模式
	launchMode := session.LaunchMode(mode)
	if launchMode == "" {
		launchMode = session.ModeTerminal
	}

	// 如果未指定工作目录，使用默认路径
	if workDir == "" {
		workDir = a.Paths.GetDefaultPath()
	}
	if workDir == "" {
		home, _ := os.UserHomeDir()
		workDir = home
	}

	if useProxy {
		if !a.Proxy.IsRunning() {
			port := a.Proxy.GetPort()
			if port == 0 {
				port = 5280
			}
			if err := a.Proxy.Start(port, provider.BaseURL); err != nil {
				return "", fmt.Errorf("start proxy: %w", err)
			}
		}
		a.Launcher.SetProxyPort(a.Proxy.GetPort())
	} else {
		a.Launcher.SetProxyPort(0)
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypeClaudeCode, providerName, presetName, model, launchMode, workDir, useProxy)
	a.Log.Info("session", "会话已创建", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, model, launchMode))

	// 根据模式选择启动方式
	if launchMode == session.ModeEmbedded {
		// 内嵌终端模式：使用 ConPTY，终端渲染在前端 xterm.js 中
		overrides := a.Launcher.BuildOverrides(*provider, presetName, apiKey, agentTeams)

		// 诊断：记录认证相关的环境变量覆盖
		for _, authVar := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
			if v, ok := overrides[authVar]; ok {
				if v == "" {
					a.Log.Info("session", "环境变量覆盖", fmt.Sprintf("%s → [删除]", authVar))
				} else {
					a.Log.Info("session", "环境变量覆盖", fmt.Sprintf("%s → %s", authVar, secrets.MaskKey(v)))
				}
			}
		}

		// 注入自定义环境变量（自定义 > 系统，再被 overrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, overrides)

		// 确定 shell 路径和自动命令
		actualShell := shellPath
		autoCommand := ""
		if actualShell != "" {
			// 用户指定了 shell，在 shell 中自动启动 claude
			autoCommand = "claude"
		}
		// actualShell 为空时，PTY 会直接启动 "claude"

		pid, err := a.Pty.Start(sess.ID, actualShell, autoCommand, workDir, env, 120, 40)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start pty: %w", err)
		}
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		// 监控 PTY 进程退出
		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.Log.Info("session", "PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	result, err := a.Launcher.Launch(sess.ID, *provider, presetName, apiKey, agentTeams, launchMode, workDir)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch claude: %w", err)
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	// 监控进程退出
	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.Log.Info("session", "进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

// StopSession 停止指定会话
func (a *App) StopSession(sessionID string) error {
	a.Log.Info("session", "停止会话", "id="+sessionID)

	// 优先检查 PTY 会话
	if a.Pty.IsRunning(sessionID) {
		if err := a.Pty.Close(sessionID); err != nil {
			a.Log.Error("session", "停止PTY会话失败", fmt.Sprintf("id=%s err=%v", sessionID, err))
			return err
		}
		a.Sessions.MarkStopped(sessionID)
		return nil
	}

	// 否则走 Launcher
	if err := a.Launcher.Stop(sessionID); err != nil {
		a.Log.Error("session", "停止会话失败", fmt.Sprintf("id=%s err=%v", sessionID, err))
		return err
	}
	a.Sessions.MarkStopped(sessionID)
	return nil
}

// StopAllSessions 停止所有运行中的会话
func (a *App) StopAllSessions() {
	ids := a.Sessions.GetRunning()
	for _, id := range ids {
		_ = a.Launcher.Stop(id)
		a.Sessions.MarkStopped(id)
	}
}

// GetSessions 获取所有会话列表
func (a *App) GetSessions() []session.SessionInfo {
	return a.Sessions.List()
}

// RemoveSession 删除已结束的会话记录
func (a *App) RemoveSession(sessionID string) error {
	return a.Sessions.Remove(sessionID)
}

// ClearStoppedSessions 清除所有已结束的会话
func (a *App) ClearStoppedSessions() int {
	return a.Sessions.ClearStopped()
}

// LaunchCodexSession 启动 Codex CLI 终端会话
// modelName 非空时通过 -m 参数指定模型；providerID 非空时注入对应服务商的 API Key
func (a *App) LaunchCodexSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 Codex 会话请求", fmt.Sprintf("model=%s provider=%s mode=%s workDir=%s shell=%s", modelName, providerID, mode, workDir, shellPath))

	// 确定启动模式
	launchMode := session.LaunchMode(mode)
	if launchMode == "" {
		launchMode = session.ModeTerminal
	}

	// 如果未指定工作目录，使用默认路径
	if workDir == "" {
		workDir = a.Paths.GetDefaultPath()
	}
	if workDir == "" {
		home, _ := os.UserHomeDir()
		workDir = home
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypeCodex, "codex", providerID, modelName, launchMode, workDir, false)
	a.Log.Info("session", "Codex 会话已创建", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, modelName, launchMode))

	// 构建环境变量注入：若指定了 providerID，根据 Provider 的 Type 注入对应的环境变量。
	envOverrides := map[string]string{}
	injectProviderEnv := func(pid string, provider *config.Provider) {
		apiKey, _ := a.Secrets.GetAPIKey(pid)
		if apiKey == "" {
			apiKey, _ = a.Secrets.GetAPIKeyWithFallback(pid)
		}
		if apiKey == "" {
			return
		}
		switch provider.Type {
		case "openai":
			envOverrides["OPENAI_API_KEY"] = apiKey
			if provider.BaseURL != "" {
				envOverrides["OPENAI_BASE_URL"] = provider.BaseURL
			}
		default:
			envOverrides["ANTHROPIC_API_KEY"] = apiKey
			if provider.BaseURL != "" {
				envOverrides["ANTHROPIC_BASE_URL"] = provider.BaseURL
			}
		}
	}
	if providerID != "" {
		if provider, err := a.Config.GetProvider(providerID); err == nil {
			injectProviderEnv(providerID, provider)
		}
	}

	// 调试日志：输出 envOverrides 注入情况
	envKeys := make([]string, 0, len(envOverrides))
	for k := range envOverrides {
		envKeys = append(envKeys, k)
	}
	a.Log.Info("codex", "envOverrides keys", fmt.Sprintf("%v", envKeys))
	a.Log.Info("codex", "OPENAI_API_KEY present", fmt.Sprintf("%v", envOverrides["OPENAI_API_KEY"] != ""))
	a.Log.Info("codex", "OPENAI_BASE_URL", envOverrides["OPENAI_BASE_URL"])

	// 动态注入 model_provider 到 ~/.codex/config.toml：
	// codex CLI 仅凭 OPENAI_BASE_URL/OPENAI_API_KEY 环境变量无法确定第三方服务商的认证方式，
	// 需要 config.toml 中明确声明 model_provider 和 [model_providers.xxx] 来使用 OpenAI 兼容认证。
	if baseURL := envOverrides["OPENAI_BASE_URL"]; baseURL != "" {
		if err := injectCodexConfigToml(baseURL); err != nil {
			a.Log.Warn("codex", "注入 config.toml 失败（不影响启动）", err.Error())
		} else {
			a.Log.Info("codex", "config.toml model_provider 已注入", fmt.Sprintf("base_url=%s", baseURL))
		}
	}

	// 注入 API Key 到 ~/.codex/auth.json：
	// codex CLI 不从环境变量读取 API Key，而是从 auth.json 文件读取。
	// 写入 {"OPENAI_API_KEY": "xxx"} 覆盖现有认证状态，使 codex 使用 API Key 认证。
	if apiKey := envOverrides["OPENAI_API_KEY"]; apiKey != "" {
		if err := injectCodexAuthJSON(apiKey); err != nil {
			a.Log.Warn("codex", "注入 auth.json 失败（不影响启动）", err.Error())
		} else {
			a.Log.Info("codex", "auth.json API Key 已注入", fmt.Sprintf("key=%s len=%d", secrets.MaskKey(apiKey), len(apiKey)))
		}
	}

	// 内嵌终端模式：使用 ConPTY
	if launchMode == session.ModeEmbedded {
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		actualShell := shellPath
		autoCommand := ""
		if actualShell != "" {
			autoCommand = "codex"
			if modelName != "" {
				autoCommand = fmt.Sprintf("codex -m %s", modelName)
			}
		}

		pid, err := a.Pty.Start(sess.ID, actualShell, autoCommand, workDir, env, 120, 40)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "Codex PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start codex pty: %w", err)
		}
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "Codex PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.Log.Info("session", "Codex PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	result, err := a.Launcher.LaunchCodex(sess.ID, modelName, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "Codex 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch codex: %w", err)
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "Codex 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.Log.Info("session", "Codex 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

// GetProvidersByType 返回指定类型的 Provider 列表（type 为 "openai" 或 "anthropic"）
// 返回 map，key 为 providerID，value 为 Provider 配置
func (a *App) GetProvidersByType(providerType string) map[string]config.Provider {
	allProviders := a.Config.GetProviders()
	result := make(map[string]config.Provider)
	for id, p := range allProviders {
		pType := p.Type
		if pType == "" {
			pType = "anthropic" // 默认类型
		}
		if pType == providerType {
			result[id] = p
		}
	}
	return result
}

// LaunchOpenCode 启动 OpenCode 终端会话
func (a *App) LaunchOpenCode(providerName string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 OpenCode 会话请求", fmt.Sprintf("provider=%s mode=%s workDir=%s shell=%s", providerName, mode, workDir, shellPath))

	var provider *config.Provider
	envOverrides := map[string]string{}
	if providerName != "" {
		loadedProvider, err := a.Config.GetProvider(providerName)
		if err != nil {
			a.Log.Error("session", "获取 OpenCode 提供商失败", err.Error())
			return "", fmt.Errorf("get opencode provider: %w", err)
		}
		provider = loadedProvider

		apiKey, keySource := a.Secrets.GetAPIKeyWithFallback(providerName)
		if apiKey == "" {
			a.Log.Error("session", "未找到 OpenCode API 密钥", "provider="+providerName)
			return "", fmt.Errorf("no API key found for provider %q", providerName)
		}

		envOverrides = buildOpenCodeEnvOverrides(providerName, *provider, apiKey)
		a.Log.Info("session", "OpenCode API 密钥已获取",
			fmt.Sprintf("provider=%s source=%s key=%s len=%d",
				providerName, keySource, secrets.MaskKey(apiKey), len(apiKey)))
	}

	// 确定启动模式
	launchMode := session.LaunchMode(mode)
	if launchMode == "" {
		launchMode = session.ModeTerminal
	}

	// 如果未指定工作目录，使用默认路径
	if workDir == "" {
		workDir = a.Paths.GetDefaultPath()
	}
	if workDir == "" {
		home, _ := os.UserHomeDir()
		workDir = home
	}

	sessionProvider := "opencode"
	if providerName != "" {
		sessionProvider = providerName
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypeOpenCode, sessionProvider, "", "", launchMode, workDir, false)
	a.Log.Info("session", "OpenCode 会话已创建", fmt.Sprintf("id=%s mode=%s", sess.ID, launchMode))

	// 根据模式选择启动方式
	if launchMode == session.ModeEmbedded {
		// 内嵌终端模式：使用 ConPTY
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		// 确定 shell 路径和自动命令
		actualShell := shellPath
		autoCommand := ""
		if actualShell != "" {
			// 用户指定了 shell，在 shell 中自动启动 opencode
			autoCommand = "opencode"
		}
		// actualShell 为空时，PTY 会直接启动 "opencode"

		pid, err := a.Pty.Start(sess.ID, actualShell, autoCommand, workDir, env, 120, 40)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "OpenCode PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start opencode pty: %w", err)
		}
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "OpenCode PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		// 监控 PTY 进程退出
		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.Log.Info("session", "OpenCode PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	result, err := a.Launcher.LaunchOpenCode(sess.ID, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "OpenCode 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch opencode: %w", err)
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "OpenCode 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	// 监控进程退出
	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.Log.Info("session", "OpenCode 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

func buildOpenCodeEnvOverrides(providerName string, provider config.Provider, apiKey string) map[string]string {
	if providerName == "" || apiKey == "" {
		return map[string]string{}
	}

	overrides := map[string]string{}
	providerType := strings.TrimSpace(strings.ToLower(provider.Type))
	if providerType == "" {
		providerType = "openai"
	}

	switch providerType {
	case "anthropic":
		overrides["ANTHROPIC_API_KEY"] = apiKey
		overrides["ANTHROPIC_AUTH_TOKEN"] = ""
		if provider.BaseURL != "" {
			overrides["ANTHROPIC_BASE_URL"] = provider.BaseURL
		}
	default:
		overrides["OPENAI_API_KEY"] = apiKey
		if provider.BaseURL != "" {
			overrides["OPENAI_BASE_URL"] = provider.BaseURL
		}
	}

	return overrides
}

// --- 路径管理（前端绑定） ---

// BrowseDirectory 打开系统目录选择对话框
func (a *App) BrowseDirectory() (string, error) {
	dir, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择工作目录",
	})
	if err != nil {
		return "", err
	}
	return dir, nil
}

// --- 原有兼容方法 ---

// QuickLaunch 兼容原有接口（使用终端模式）
func (a *App) QuickLaunch(providerName, presetName string, useProxy bool) error {
	_, err := a.LaunchSession(providerName, presetName, "terminal", "", useProxy, "")
	return err
}

// SaveAllConfig 保存配置和密钥到磁盘。
func (a *App) SaveAllConfig() error {
	if err := a.Config.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if err := a.Secrets.Save(); err != nil {
		return fmt.Errorf("save secrets: %w", err)
	}
	if err := a.Paths.Save(); err != nil {
		return fmt.Errorf("save paths: %w", err)
	}
	if err := a.Settings.Save(); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	if err := a.Proxy.SaveRules(defaultConfigDir()); err != nil {
		return fmt.Errorf("save injection rules: %w", err)
	}
	if err := a.Proxy.SaveBackendURLHistory(defaultConfigDir()); err != nil {
		return fmt.Errorf("save backend URL history: %w", err)
	}
	return nil
}

// GetAppInfo 返回应用基本信息。
func (a *App) GetAppInfo() map[string]any {
	version := strings.TrimPrefix(strings.TrimSpace(Version), "v")
	return map[string]any{
		"version":      version,
		"configDir":    defaultConfigDir(),
		"runningCount": a.Sessions.RunningCount(),
		"proxyStatus":  a.Proxy.GetStatus(),
	}
}

func (a *App) CheckForUpdate() (*updater.UpdateInfo, error) {
	return a.Updater.CheckForUpdate()
}

func (a *App) DownloadAndApplyUpdate() error {
	return a.Updater.DownloadAndApply(func(downloaded, total int64) {
		wailsRuntime.EventsEmit(a.ctx, "update:progress", map[string]any{
			"downloaded": downloaded,
			"total":      total,
		})
	})
}

// GetGitHubToken 返回当前配置的 GitHub Token。
func (a *App) GetGitHubToken() string {
	return a.Settings.GetGitHubToken()
}

// SetGitHubToken 保存 GitHub Token 并同步到 Updater。
func (a *App) SetGitHubToken(token string) error {
	if err := a.Settings.SetGitHubToken(token); err != nil {
		return err
	}
	a.Updater.SetToken(token)
	return nil
}

// --- 日志系统 API ---

// GetLogs 获取日志条目（支持过滤）
func (a *App) GetLogs(level string, source string, keyword string, limit int) []logging.Entry {
	return a.Log.GetEntries(level, source, keyword, limit)
}

// GetLogSources 获取所有日志来源
func (a *App) GetLogSources() []string {
	return a.Log.GetSources()
}

// GetLogFiles 获取日志文件列表
func (a *App) GetLogFiles() []string {
	return a.Log.GetLogFiles()
}

// GetLogFileContent 获取日志文件内容
func (a *App) GetLogFileContent(filename string) (string, error) {
	return a.Log.GetLogFileContent(filename)
}

// ClearLogs 清除内存日志
func (a *App) ClearLogs() {
	a.Log.ClearEntries()
	a.Log.Info("app", "内存日志已清除")
}

// ExportLogs 导出日志为JSON
func (a *App) ExportLogs() (string, error) {
	return a.Log.ExportJSON()
}

// --- PTY 终端 API ---

// PtyWrite 向内嵌终端写入数据（前端键盘输入）。data 为 base64 编码。
func (a *App) PtyWrite(sessionID string, data string) error {
	return a.Pty.Write(sessionID, data)
}

// PtyWriteLarge 向内嵌终端分块写入大量数据（用于长文本粘贴）。data 为 base64 编码。
// 内部将数据拆分为 1KB 小块逐步写入，避免 ConPTY 缓冲区溢出截断。
func (a *App) PtyWriteLarge(sessionID string, data string) error {
	return a.Pty.WriteLarge(sessionID, data)
}

// SaveClipboardImage 将 base64 编码的图片数据保存为临时 PNG 文件，返回文件绝对路径。
// 用于处理 Windows 截图工具截图后粘贴到终端的场景。
func (a *App) SaveClipboardImage(base64Data string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	// 使用时间戳生成唯一文件名
	tmpDir := os.TempDir()
	filename := fmt.Sprintf("amagi-codebox-clipboard-%d.png", time.Now().UnixMilli())
	filePath := filepath.Join(tmpDir, filename)

	if err := os.WriteFile(filePath, raw, 0o644); err != nil {
		return "", fmt.Errorf("write temp image: %w", err)
	}
	a.Log.Info("app", "剪贴板图片已保存", filePath)
	return filePath, nil
}

// PtyResize 调整内嵌终端尺寸
func (a *App) PtyResize(sessionID string, cols, rows int) error {
	return a.Pty.Resize(sessionID, cols, rows)
}

// GetOutputHistory 返回指定 PTY 会话的输出历史，供 WebSocket 重放。
// 实现 remote.HistoryProvider 接口。
func (a *App) GetOutputHistory(sessionID string) ([]byte, error) {
	return a.Pty.GetOutputHistory(sessionID)
}

// GetPtyDimensions 返回指定 PTY 会话的当前尺寸。
// 实现 remote.DimensionsProvider 接口。
func (a *App) GetPtyDimensions(sessionID string) (cols, rows int, err error) {
	return a.Pty.GetPtyDimensions(sessionID)
}

// OpenFileInEditor 使用系统默认程序打开指定文件。
// filePath 可以是绝对路径或相对路径；line 参数保留兼容但不使用（系统默认程序通常不支持行号定位）。
func (a *App) OpenFileInEditor(filePath string, line int) error {
	// 先验证文件是否存在，避免打开不存在的路径时创建空文件
	if _, err := os.Stat(filePath); err != nil {
		a.Log.Debug("app", "文件不存在，跳过打开", filePath)
		return fmt.Errorf("file not found: %s", filePath)
	}
	shellCmd := exec.Command("cmd", "/c", "start", "", filePath)
	shellCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := shellCmd.Start(); err != nil {
		a.Log.Warn("app", "打开文件失败", fmt.Sprintf("file=%s err=%v", filePath, err))
		return fmt.Errorf("open file %q: %w", filePath, err)
	}
	a.Log.Info("app", "系统默认程序打开文件", filePath)
	return nil
}

// --- 诊断 API ---

// GetKeyDiagnostics 返回所有提供商的密钥来源诊断信息
func (a *App) GetKeyDiagnostics() map[string]map[string]string {
	providers := a.Config.GetProviderNames()
	return a.Secrets.GetKeyDiagnostics(providers)
}

// ExportConfigToFile 将所有 providers、presets 和 API keys 合并导出为 JSON 文件。
// 通过系统对话框让用户选择保存位置，同时在项目目录保存一份副本。
func (a *App) ExportConfigToFile() (string, error) {
	a.Log.Info("app", "开始导出配置")

	// 构建导出数据
	providers := a.Config.GetProviders()
	agentTeams := a.Config.GetAgentTeams()

	exportProviders := make(map[string]config.ExportProvider, len(providers))
	for name, p := range providers {
		apiKey, _ := a.Secrets.GetAPIKey(name)
		presets := p.Presets
		if presets == nil {
			presets = map[string]config.Preset{}
		}
		exportProviders[name] = config.ExportProvider{
			Type:         p.Type,
			BaseURL:      p.BaseURL,
			DefaultModel: p.DefaultModel,
			AuthKey:      p.AuthKey,
			APIKey:       apiKey,
			Presets:      presets,
		}
	}

	exportCfg := config.ExportConfig{
		Version:    "1.0",
		ExportedAt: time.Now().Format(time.RFC3339),
		Source:     "amagi-codebox",
		Providers:  exportProviders,
		AgentTeams: agentTeams,
	}

	data, err := json.MarshalIndent(exportCfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export config: %w", err)
	}
	data = append(data, '\n')

	// 弹出保存对话框
	savePath, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "导出配置",
		DefaultFilename: "amagi-codebox-config.json",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("open save dialog: %w", err)
	}

	// 用户取消了对话框
	if savePath == "" {
		a.Log.Info("app", "用户取消了配置导出")
		return "", nil
	}

	// atomic 写入用户选择的路径
	if err := atomicWriteFile(savePath, data); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}
	a.Log.Info("app", "配置已导出", savePath)

	// 同时保存一份副本到配置目录
	backupPath := filepath.Join(defaultConfigDir(), "amagi-codebox-config.json")
	if err := atomicWriteFile(backupPath, data); err != nil {
		// 副本写入失败不影响主导出结果，仅记录警告
		a.Log.Warn("app", "副本保存失败", err.Error())
	} else {
		a.Log.Info("app", "配置副本已保存", backupPath)
	}

	return savePath, nil
}

// ImportConfigFromFile 通过文件选择对话框导入 JSON 配置文件，
// 将其中的 providers 和 AgentTeams 配置合并到当前配置中。
func (a *App) ImportConfigFromFile() (string, error) {
	a.Log.Info("app", "开始导入配置")

	// 弹出文件选择对话框
	filePath, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "导入配置",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("open file dialog: %w", err)
	}

	// 用户取消了对话框
	if filePath == "" {
		a.Log.Info("app", "用户取消了配置导入")
		return "", nil
	}

	// 读取文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	// 剥离 UTF-8 BOM（Windows 编辑器可能在文件开头添加 BOM）
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	// 解析为 ExportConfig 结构体
	var exportCfg config.ExportConfig
	if err := json.Unmarshal(data, &exportCfg); err != nil {
		return "", fmt.Errorf("parse JSON: %w", err)
	}

	// 验证基本字段
	if exportCfg.Version == "" || exportCfg.Source == "" {
		return "", fmt.Errorf("invalid config file: missing version or source field")
	}

	// 遍历 providers 并导入
	importCount := 0
	for name, ep := range exportCfg.Providers {
		provider := config.Provider{
			Type:         ep.Type,
			BaseURL:      ep.BaseURL,
			DefaultModel: ep.DefaultModel,
			AuthKey:      ep.AuthKey,
			Presets:      ep.Presets,
		}
		if provider.Presets == nil {
			provider.Presets = map[string]config.Preset{}
		}

		if err := a.Config.SaveProvider(name, provider); err != nil {
			return "", fmt.Errorf("save provider %q: %w", name, err)
		}

		if ep.APIKey != "" {
			if err := a.Secrets.SetAPIKey(name, ep.APIKey); err != nil {
				a.Log.Warn("app", "保存 API key 失败", fmt.Sprintf("provider=%s err=%v", name, err))
			}
		}

		importCount++
	}

	// 导入 AgentTeams 配置（如果存在）
	if exportCfg.AgentTeams.TeammateMode != "" || exportCfg.AgentTeams.Enabled {
		if err := a.Config.SetAgentTeams(exportCfg.AgentTeams); err != nil {
			a.Log.Warn("app", "导入 AgentTeams 配置失败", err.Error())
		}
	}

	msg := fmt.Sprintf("成功导入 %d 个提供商配置", importCount)
	a.Log.Info("app", msg, filePath)
	return msg, nil
}

// GetProviderExportJSON 返回指定提供商的配置（含 API key）的格式化 JSON 字符串，
// 供前端 JSON 编辑功能使用。
func (a *App) GetProviderExportJSON(providerName string) (string, error) {
	provider, err := a.Config.GetProvider(providerName)
	if err != nil {
		return "", fmt.Errorf("get provider %q: %w", providerName, err)
	}

	apiKey, _ := a.Secrets.GetAPIKey(providerName)

	presets := provider.Presets
	if presets == nil {
		presets = map[string]config.Preset{}
	}

	ep := config.ExportProvider{
		Type:         provider.Type,
		BaseURL:      provider.BaseURL,
		DefaultModel: provider.DefaultModel,
		AuthKey:      provider.AuthKey,
		APIKey:       apiKey,
		Presets:      presets,
	}

	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal provider JSON: %w", err)
	}
	return string(data), nil
}

// SaveProviderFromJSON 将前端传入的 JSON 字符串解析后保存到指定提供商，
// 若 APIKey 有变更则同步更新密钥存储。
func (a *App) SaveProviderFromJSON(providerName string, jsonStr string) error {
	var ep config.ExportProvider
	if err := json.Unmarshal([]byte(jsonStr), &ep); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	provider := config.Provider{
		Type:         ep.Type,
		BaseURL:      ep.BaseURL,
		DefaultModel: ep.DefaultModel,
		AuthKey:      ep.AuthKey,
		Presets:      ep.Presets,
	}
	if provider.Presets == nil {
		provider.Presets = map[string]config.Preset{}
	}

	if err := a.Config.SaveProvider(providerName, provider); err != nil {
		return fmt.Errorf("save provider %q: %w", providerName, err)
	}

	if ep.APIKey != "" {
		if err := a.Secrets.SetAPIKey(providerName, ep.APIKey); err != nil {
			return fmt.Errorf("save API key for %q: %w", providerName, err)
		}
	}

	a.Log.Info("app", "已从 JSON 保存提供商配置", providerName)
	return nil
}

// atomicWriteFile 原子写入文件（先写 .tmp 再 rename）。
func atomicWriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".amagi-codebox"
	}
	return filepath.Join(home, ".amagi-codebox")
}

// --- URL 历史 API ---

// GetUrlHistory 获取指定Provider的URL历史
func (a *App) GetUrlHistory(providerID string) ([]string, error) {
	return a.Config.GetUrlHistory(providerID)
}

// AddUrlToHistory 添加URL到历史记录（自动去重并调整到最前）
func (a *App) AddUrlToHistory(providerID, url string) error {
	return a.Config.AddUrlToHistory(providerID, url)
}

// RemoveUrlFromHistory 从历史记录中删除指定URL
func (a *App) RemoveUrlFromHistory(providerID, url string) error {
	return a.Config.RemoveUrlFromHistory(providerID, url)
}

// --- 注入规则后端URL历史API ---

// GetProxyBackendURLHistory 获取注入规则后端URL历史记录
func (a *App) GetProxyBackendURLHistory() []string {
	return a.Proxy.GetBackendURLHistory()
}

// AddProxyBackendURL 添加注入规则后端URL到历史记录（自动去重并调整到最前）
func (a *App) AddProxyBackendURL(url string) error {
	if err := a.Proxy.AddBackendURL(url); err != nil {
		return err
	}
	// 自动保存配置
	return a.Proxy.SaveBackendURLHistory(defaultConfigDir())
}

// RemoveProxyBackendURL 从历史记录中删除指定注入规则后端URL
func (a *App) RemoveProxyBackendURL(url string) error {
	if err := a.Proxy.RemoveBackendURL(url); err != nil {
		return err
	}
	// 自动保存配置
	return a.Proxy.SaveBackendURLHistory(defaultConfigDir())
}

// SetProxyBackendURL 设置当前使用的注入规则后端URL，并自动添加到历史记录
func (a *App) SetProxyBackendURL(url string) error {
	return a.Proxy.SetBackendURL(url)
}

// --- 自定义环境变量 API ---

// GetEnvVars 返回所有自定义环境变量
func (a *App) GetEnvVars() ([]envvars.EnvVar, error) {
	return a.EnvVars.GetAll(), nil
}

// SetEnvVar 设置单个自定义环境变量（不存在则新增，存在则更新）
func (a *App) SetEnvVar(key, value string) error {
	return a.EnvVars.Set(key, value)
}

// DeleteEnvVar 删除指定 key 的自定义环境变量
func (a *App) DeleteEnvVar(key string) error {
	return a.EnvVars.Delete(key)
}

// ImportEnvVars 从 JSON 字符串导入自定义环境变量（全量替换）
func (a *App) ImportEnvVars(jsonStr string) error {
	return a.EnvVars.Import(jsonStr)
}

// ExportEnvVars 导出自定义环境变量为 JSON 字符串
func (a *App) ExportEnvVars() (string, error) {
	return a.EnvVars.Export()
}

// GetEnvVarsJSON 获取所有自定义环境变量的 JSON 格式（供 JSON 编辑器使用）
func (a *App) GetEnvVarsJSON() (string, error) {
	return a.EnvVars.GetJSON()
}

// SaveEnvVarsJSON 从 JSON 字符串保存自定义环境变量（供 JSON 编辑器使用）
func (a *App) SaveEnvVarsJSON(jsonStr string) error {
	return a.EnvVars.SaveJSON(jsonStr)
}

// ExportEnvVarsToFile 弹出保存对话框，将自定义环境变量导出到用户选择的 JSON 文件。
func (a *App) ExportEnvVarsToFile() error {
	data, err := a.EnvVars.Export()
	if err != nil {
		return fmt.Errorf("export envvars: %w", err)
	}

	savePath, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "导出环境变量",
		DefaultFilename: "envvars.json",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return fmt.Errorf("open save dialog: %w", err)
	}
	if savePath == "" {
		return nil // 用户取消
	}

	if err := atomicWriteFile(savePath, []byte(data+"\n")); err != nil {
		return fmt.Errorf("write envvars file: %w", err)
	}
	a.Log.Info("app", "环境变量已导出", savePath)
	return nil
}

// ImportEnvVarsFromFile 弹出打开对话框，从用户选择的 JSON 文件导入自定义环境变量（全量替换）。
func (a *App) ImportEnvVarsFromFile() error {
	filePath, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "导入环境变量",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return fmt.Errorf("open file dialog: %w", err)
	}
	if filePath == "" {
		return nil // 用户取消
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// 剥离 UTF-8 BOM（Windows 编辑器可能在文件开头添加 BOM）
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}

	if err := a.EnvVars.Import(string(data)); err != nil {
		return fmt.Errorf("import envvars: %w", err)
	}
	a.Log.Info("app", "环境变量已导入", filePath)
	return nil
}

// injectCodexConfigToml 在 ~/.codex/config.toml 中注入 model_provider 和 [model_providers.xxx]。
// codex CLI 需要这两项配置才能正确使用第三方 OpenAI 兼容服务商认证（requires_openai_auth = true）。
// 仅当 baseURL 非空（非官方 OpenAI）时应调用。
// 使用特殊注释标记包裹注入段，每次启动前重新写入，保证配置准确。
func injectCodexConfigToml(baseURL string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")

	// 读取现有内容
	existing := ""
	if b, err := os.ReadFile(configPath); err == nil {
		existing = string(b)
	}

	// 移除之前由 amagi-codebox 注入的段落（如果存在）
	cleaned := removeInjectedSection(existing)

	// 构建注入段（使用固定的 provider name，避免 TOML key 冲突）
	injected := "\n# === amagi-codebox-inject-start ===\n" +
		"model_provider = \"amagi-codebox-provider\"\n\n" +
		"[model_providers.amagi-codebox-provider]\n" +
		"name = \"amagi-codebox-provider\"\n" +
		"base_url = \"" + baseURL + "\"\n" +
		"wire_api = \"responses\"\n" +
		"requires_openai_auth = true\n" +
		"# === amagi-codebox-inject-end ===\n"

	newContent := cleaned + injected

	// 原子写入
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("mkdir codex config dir: %w", err)
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("write temp codex config: %w", err)
	}
	if err := os.Rename(tmp, configPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace codex config: %w", err)
	}
	return nil
}

// injectCodexAuthJSON 将 API Key 写入 ~/.codex/auth.json。
// codex CLI 不从环境变量读取 API Key，而是从 auth.json 文件读取。
// 写入格式为 {"OPENAI_API_KEY": "xxx"}，覆盖现有认证状态（如 ChatGPT OAuth token），
// 使 codex 使用 API Key 认证而非 ChatGPT OAuth。
// 文件权限设为 0600，仅用户可读写，保护 API Key 安全。
func injectCodexAuthJSON(apiKey string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		return fmt.Errorf("mkdir codex dir: %w", err)
	}
	authPath := filepath.Join(codexDir, "auth.json")

	// 只写 OPENAI_API_KEY，不写 auth_mode 和 tokens，
	// 使 codex 使用 API Key 认证而非 ChatGPT OAuth。
	authContent := fmt.Sprintf("{\n  \"OPENAI_API_KEY\": %q\n}\n", apiKey)

	// 原子写入，先写临时文件再重命名，避免 codex 读到半写文件
	tmpPath := authPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(authContent), 0o600); err != nil {
		return fmt.Errorf("write tmp auth.json: %w", err)
	}
	if err := os.Rename(tmpPath, authPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename auth.json: %w", err)
	}
	return nil
}

// removeInjectedSection 从 TOML 文件内容中移除 amagi-codebox 注入的段落。
// 段落由 "# === amagi-codebox-inject-start ===" 和 "# === amagi-codebox-inject-end ===" 标记包裹。
func removeInjectedSection(content string) string {
	const startMarker = "# === amagi-codebox-inject-start ==="
	const endMarker = "# === amagi-codebox-inject-end ==="

	before, rest, found := strings.Cut(content, startMarker)
	if !found {
		return content
	}
	_, after, found := strings.Cut(rest, endMarker)
	if !found {
		// 只有开始标记没有结束标记，截断到开始标记前
		return strings.TrimRight(before, "\n") + "\n"
	}
	// 去掉 endMarker 后紧跟的换行
	after = strings.TrimLeft(after, "\n")
	before = strings.TrimRight(before, "\n")
	if before == "" {
		return after
	}
	if after == "" {
		return before + "\n"
	}
	return before + "\n" + after
}
