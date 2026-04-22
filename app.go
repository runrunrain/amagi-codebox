package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"amagi-codebox/internal/amagi"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/opencodeconfig"
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
	"amagi-codebox/internal/workspace"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const codexSessionHomeRootDirName = "codex-sessions"

type codexSessionHomeInfo struct {
	Path    string
	HomeKey string
}

type codexLaunchSettings struct {
	Model                      string
	ModelContextWindow         int
	ModelAutoCompactTokenLimit int
}

//go:embed build/windows/icon.ico
var trayIcon []byte

// App 主应用结构体，负责跨服务协调和生命周期管理。
// 通过 Wails 绑定暴露给前端。
type App struct {
	ctx context.Context

	codexSessionHomesMu sync.Mutex
	codexSessionHomes   map[string]codexSessionHomeInfo

	Config         *config.ConfigService
	Secrets        *secrets.SecretsService
	Launcher       *launcher.LauncherService
	Proxy          *proxy.ProxyService
	Tray           *tray.Service
	Sessions       *session.Manager
	Paths          *paths.PathsService
	Log            *logging.Service
	Pty            *pty.Service
	Settings       *settings.Service
	Remote         *remote.Server
	EnvVars        *envvars.EnvVarsService
	Updater        *updater.Service
	Plugins        *plugin.Service
	Workspaces     *workspace.Service
	Amagi          *amagi.Service
	OpenCodeConfig *opencodeconfig.Service
}

func NewApp() *App {
	configDir := defaultConfigDir()
	log := logging.NewService(configDir)
	envVarsSvc := envvars.NewEnvVarsService(configDir)
	pluginsSvc := plugin.NewService("", log)

	app := &App{
		codexSessionHomes: map[string]codexSessionHomeInfo{},
		Config:            config.NewConfigService(configDir),
		Secrets:           secrets.NewSecretsService(configDir),
		Launcher:          launcher.NewLauncherService(log, envVarsSvc),
		Proxy:             proxy.NewProxyService(),
		Tray:              tray.NewService(),
		Sessions:          session.NewManager(),
		Paths:             paths.NewPathsService(configDir),
		Log:               log,
		Pty:               pty.NewService(log),
		Settings:          settings.NewService(configDir),
		EnvVars:           envVarsSvc,
		Updater:           updater.NewService(Version, log),
		Plugins:           pluginsSvc,
		Workspaces:        workspace.NewService(configDir, pluginsSvc, log),
		Amagi:             amagi.NewService(configDir),
		OpenCodeConfig:    opencodeconfig.NewService(),
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
	if err := a.Amagi.Load(); err != nil {
		a.Log.Warn("app", "加载 AmagiCode 配置失败", err.Error())
	} else {
		a.Log.Info("app", "AmagiCode 配置加载成功")
	}
	if err := a.Workspaces.Load(); err != nil {
		a.Log.Warn("app", "加载工作区配置失败", err.Error())
	} else {
		a.Log.Info("app", "工作区配置加载成功")
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
	a.cleanupAllCodexSessionHomes()
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
		a.cleanupCodexSessionHome(sessionID)
		return nil
	}

	// 否则走 Launcher
	if err := a.Launcher.Stop(sessionID); err != nil {
		a.Log.Error("session", "停止会话失败", fmt.Sprintf("id=%s err=%v", sessionID, err))
		return err
	}
	a.Sessions.MarkStopped(sessionID)
	a.cleanupCodexSessionHome(sessionID)
	return nil
}

// StopAllSessions 停止所有运行中的会话
func (a *App) StopAllSessions() {
	ids := a.Sessions.GetRunning()
	for _, id := range ids {
		_ = a.Launcher.Stop(id)
		a.Sessions.MarkStopped(id)
		a.cleanupCodexSessionHome(id)
	}
}

// GetSessions 获取所有会话列表
func (a *App) GetSessions() []session.SessionInfo {
	return a.Sessions.List()
}

// RemoveSession 删除已结束的会话记录
func (a *App) RemoveSession(sessionID string) error {
	if err := a.Sessions.Remove(sessionID); err != nil {
		return err
	}
	a.cleanupCodexSessionHome(sessionID)
	return nil
}

// ClearStoppedSessions 清除所有已结束的会话
func (a *App) ClearStoppedSessions() int {
	a.cleanupCodexSessionHomesForStoppedSessions()
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

	launchSettings := codexLaunchSettings{
		Model: normalizeCodexModelName(modelName),
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypeCodex, "codex", providerID, launchSettings.Model, launchMode, workDir, false)
	a.Log.Info("session", "Codex 会话已创建", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, launchSettings.Model, launchMode))

	// 构建环境变量注入：若指定了 providerID，根据 Provider 的 Type 注入对应的环境变量。
	envOverrides := map[string]string{}
	codexOpenAIBaseURL := ""
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
			envOverrides["OPENAI_BASE_URL"] = ""
			if provider.BaseURL != "" {
				codexOpenAIBaseURL = provider.BaseURL
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
			launchSettings = resolveCodexLaunchSettings(*provider, launchSettings.Model)
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
	a.Log.Info("codex", "OPENAI_BASE_URL removed from child env", fmt.Sprintf("%v", envOverrides["OPENAI_BASE_URL"] == ""))
	a.Log.Info("codex", "openai_base_url target", codexOpenAIBaseURL)

	if codexOpenAIBaseURL != "" {
		envOverrides["AMAGI_CODEX_OPENAI_BASE_URL"] = codexOpenAIBaseURL
	}
	if _, err := a.prepareCodexSessionHome(sess.ID, providerID, launchSettings, envOverrides); err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.cleanupCodexSessionHome(sess.ID)
		a.Log.Error("codex", "准备持久 CODEX_HOME 失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("prepare persistent codex home: %w", err)
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
			if launchSettings.Model != "" {
				autoCommand = fmt.Sprintf("codex -m %s", launchSettings.Model)
			}
		}

		pid, err := a.Pty.Start(sess.ID, actualShell, autoCommand, workDir, env, 120, 40)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.cleanupCodexSessionHome(sess.ID)
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
			a.cleanupCodexSessionHome(id)
			a.Log.Info("session", "Codex PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	result, err := a.Launcher.LaunchCodex(sess.ID, launchSettings.Model, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.cleanupCodexSessionHome(sess.ID)
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
		a.cleanupCodexSessionHome(id)
		a.Log.Info("session", "Codex 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

func normalizeCodexModelName(modelName string) string {
	trimmed := strings.TrimSpace(modelName)
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "[1m]") {
		trimmed = strings.TrimSpace(trimmed[:len(trimmed)-len("[1m]")])
	}
	return trimmed
}

func resolveCodexLaunchSettings(provider config.Provider, requestedModel string) codexLaunchSettings {
	normalizedModel := normalizeCodexModelName(requestedModel)
	settings := codexLaunchSettings{
		Model: normalizedModel,
	}

	if normalizedModel == "" {
		normalizedModel = normalizeCodexModelName(provider.DefaultModel)
		settings.Model = normalizedModel
	}

	var matchedPreset *config.Preset
	requestedRaw := strings.TrimSpace(requestedModel)
	for _, preset := range provider.Presets {
		presetModel := strings.TrimSpace(preset.Model)
		if requestedRaw != "" && presetModel == requestedRaw {
			presetCopy := preset
			matchedPreset = &presetCopy
			break
		}
	}
	if matchedPreset == nil && normalizedModel != "" {
		for _, preset := range provider.Presets {
			if normalizeCodexModelName(preset.Model) == normalizedModel {
				presetCopy := preset
				matchedPreset = &presetCopy
				break
			}
		}
	}

	if matchedPreset == nil {
		return settings
	}

	if normalizedPresetModel := normalizeCodexModelName(matchedPreset.Model); normalizedPresetModel != "" {
		settings.Model = normalizedPresetModel
	}

	if matchedPreset.Parameters.ContextWindow != nil {
		settings.ModelContextWindow = matchedPreset.Parameters.ContextWindow.ModelContextWindow
		settings.ModelAutoCompactTokenLimit = matchedPreset.Parameters.ContextWindow.AutoCompactTokenLimit
	}
	if settings.ModelContextWindow == 0 && matchedPreset.Parameters.MaxContextLength > 0 {
		settings.ModelContextWindow = matchedPreset.Parameters.MaxContextLength
	}

	return settings
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
func (a *App) LaunchOpenCode(providerName string, presetName string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 OpenCode 会话请求", fmt.Sprintf("provider=%s preset=%s mode=%s workDir=%s shell=%s", providerName, presetName, mode, workDir, shellPath))

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

		// 基于 Provider + Preset 生成 OPENCODE_CONFIG_CONTENT 注入
		ocOverrides, err := launcher.BuildOpenCodeEnvOverrides(providerName, *provider, presetName, apiKey)
		if err != nil {
			a.Log.Error("session", "构建 OpenCode 配置失败", err.Error())
			return "", fmt.Errorf("build opencode config: %w", err)
		}
		envOverrides = ocOverrides

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
	sess := a.Sessions.Create(session.AppTypeOpenCode, sessionProvider, presetName, "", launchMode, workDir, false)
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
	// 准备 apiKey 用于 LaunchOpenCode 的自动配置生成
	var apiKey string
	if providerName != "" {
		apiKey, _ = a.Secrets.GetAPIKeyWithFallback(providerName)
	}
	result, err := a.Launcher.LaunchOpenCode(sess.ID, launchMode, workDir, envOverrides, providerName, provider, presetName, apiKey)
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

// LaunchAmagiCode 启动 AmagiCode 终端会话。
// groupName: modelPresets 中的预设组名称
// providerName: 服务提供商名称（可选，优先从预设的 provider 字段获取）
// mode: 启动模式（terminal/embedded）
// workDir: 工作目录
// shellPath: shell 路径（内嵌模式时使用）
func (a *App) LaunchAmagiCode(groupName string, providerName string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 AmagiCode 会话请求", fmt.Sprintf("group=%s provider=%s mode=%s workDir=%s shell=%s", groupName, providerName, mode, workDir, shellPath))

	// 从 AmagiService 获取选定的 modelPreset 组
	group, err := a.Amagi.GetModelPreset(groupName)
	if err != nil {
		a.Log.Error("session", "获取 AmagiCode 预设组失败", err.Error())
		return "", fmt.Errorf("get amagi preset group: %w", err)
	}

	// 确定使用的子预设：优先使用 group 的 default_preset，否则取第一个
	var preset *amagi.AmagiModelPreset
	presetName := ""
	if group.DefaultPreset != "" {
		if p, ok := group.Presets[group.DefaultPreset]; ok {
			preset = &p
			presetName = group.DefaultPreset
		}
	}
	// 如果 default_preset 未设置或未找到，取第一个
	if preset == nil {
		for name, p := range group.Presets {
			preset = &p
			presetName = name
			break
		}
	}
	if preset == nil {
		a.Log.Error("session", "预设组中没有可用的子预设", "group="+groupName)
		return "", fmt.Errorf("no sub-presets found in group %q", groupName)
	}

	a.Log.Info("session", "使用子预设", fmt.Sprintf("group=%s preset=%s", groupName, presetName))

	// 确定使用的 provider 名称
	// 优先使用预设中的 provider 字段，其次使用参数传入的 providerName
	actualProviderName := preset.Provider
	if actualProviderName == "" {
		actualProviderName = providerName
	}
	if actualProviderName == "" {
		a.Log.Error("session", "未指定服务提供商", "")
		return "", errors.New("provider not specified in preset or parameter")
	}

	// 从 ConfigService 获取 Provider 配置
	provider, err := a.Config.GetProvider(actualProviderName)
	if err != nil {
		a.Log.Error("session", "获取 AmagiCode 提供商失败", err.Error())
		return "", fmt.Errorf("get provider: %w", err)
	}

	// 从 SecretsService 获取 API Key
	apiKey, keySource := a.Secrets.GetAPIKeyWithFallback(actualProviderName)
	if apiKey == "" {
		a.Log.Error("session", "未找到 AmagiCode API 密钥", "provider="+actualProviderName)
		return "", fmt.Errorf("no API key found for provider %q", actualProviderName)
	}
	a.Log.Info("session", "AmagiCode API 密钥已获取",
		fmt.Sprintf("provider=%s source=%s key=%s len=%d",
			actualProviderName, keySource, secrets.MaskKey(apiKey), len(apiKey)))

	// 构建环境变量注入
	envOverrides := map[string]string{}
	providerType := strings.TrimSpace(strings.ToLower(provider.Type))
	if providerType == "" || providerType == "anthropic" {
		envOverrides["ANTHROPIC_API_KEY"] = apiKey
		envOverrides["ANTHROPIC_AUTH_TOKEN"] = ""
		if provider.BaseURL != "" {
			envOverrides["ANTHROPIC_BASE_URL"] = provider.BaseURL
		}
		// 从 preset 构建模型参数
		if preset.Model != "" {
			envOverrides["ANTHROPIC_MODEL"] = preset.Model
		}
		if preset.Temperature != nil {
			envOverrides["ANTHROPIC_TEMPERATURE"] = fmt.Sprintf("%.2f", *preset.Temperature)
		}
		if preset.MaxTokens != nil {
			envOverrides["ANTHROPIC_MAX_TOKENS"] = fmt.Sprintf("%d", *preset.MaxTokens)
		}
		if preset.Thinking != nil {
			thinkingJSON, _ := json.Marshal(preset.Thinking)
			envOverrides["ANTHROPIC_THINKING"] = string(thinkingJSON)
		}
	} else {
		envOverrides["OPENAI_API_KEY"] = apiKey
		if provider.BaseURL != "" {
			envOverrides["OPENAI_BASE_URL"] = provider.BaseURL
		}
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

	// 创建会话记录（使用组名作为 preset 参数）
	sess := a.Sessions.Create(session.AppTypeAmagiCode, actualProviderName, groupName, preset.Model, launchMode, workDir, false)
	a.Log.Info("session", "AmagiCode 会话已创建", fmt.Sprintf("id=%s group=%s preset=%s model=%s mode=%s", sess.ID, groupName, presetName, preset.Model, launchMode))

	// 根据模式选择启动方式
	if launchMode == session.ModeEmbedded {
		// 内嵌终端模式：使用 ConPTY
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		actualShell := shellPath
		autoCommand := ""
		if actualShell != "" {
			// 用户指定了 shell，在 shell 中自动启动 amagicode
			autoCommand = "amagicode"
		} else {
			// 未指定 shell 时，PTY 直接启动 amagicode 命令
			autoCommand = "amagicode"
		}

		pid, err := a.Pty.Start(sess.ID, actualShell, autoCommand, workDir, env, 120, 40)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "AmagiCode PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start amagicode pty: %w", err)
		}
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "AmagiCode PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

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
			a.Log.Info("session", "AmagiCode PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端模式：使用 Launcher
	result, err := a.Launcher.LaunchAmagiCode(sess.ID, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "AmagiCode 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch amagicode: %w", err)
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "AmagiCode 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

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
		a.Log.Info("session", "AmagiCode 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
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
	if err := a.Amagi.Save(); err != nil {
		return fmt.Errorf("save amagi config: %w", err)
	}
	if err := a.Workspaces.Save(); err != nil {
		return fmt.Errorf("save workspaces: %w", err)
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

func resolveCodexSourceHome(env []string) (string, error) {
	if env == nil {
		env = os.Environ()
	}
	if value := strings.TrimSpace(lookupEnvValue(env, "CODEX_HOME")); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get user home dir: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func lookupEnvValue(env []string, key string) string {
	caseInsensitive := runtime.GOOS == "windows"
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if caseInsensitive {
			if strings.EqualFold(k, key) {
				return v
			}
			continue
		}
		if k == key {
			return v
		}
	}
	return ""
}

func (a *App) codexSessionHomesRootDir() string {
	return filepath.Join(defaultConfigDir(), codexSessionHomeRootDirName)
}

func (a *App) registerCodexSessionHome(sessionID string, info codexSessionHomeInfo) {
	a.codexSessionHomesMu.Lock()
	defer a.codexSessionHomesMu.Unlock()
	if a.codexSessionHomes == nil {
		a.codexSessionHomes = map[string]codexSessionHomeInfo{}
	}
	a.codexSessionHomes[sessionID] = info
}

func buildCodexSessionHomeKey(providerID string, settings codexLaunchSettings, envOverrides map[string]string, sourceConfig []byte) string {
	parts := make([]string, 0, 5)
	providerID = strings.TrimSpace(providerID)
	baseURL := strings.TrimSpace(envOverrides["AMAGI_CODEX_OPENAI_BASE_URL"])
	if baseURL == "" {
		baseURL = strings.TrimSpace(envOverrides["OPENAI_BASE_URL"])
	}

	if providerID != "" {
		parts = append(parts, "provider:"+providerID)
	} else if baseURL != "" {
		parts = append(parts, "base-url:"+baseURL)
	} else {
		parts = append(parts, "default")
	}
	if providerID != "" && baseURL != "" {
		parts = append(parts, "base-url:"+baseURL)
	}
	modelName := strings.TrimSpace(settings.Model)
	if modelName != "" {
		parts = append(parts, "model:"+modelName)
	}
	if settings.ModelContextWindow > 0 {
		parts = append(parts, "model-context-window:"+strconv.Itoa(settings.ModelContextWindow))
	}
	if settings.ModelAutoCompactTokenLimit > 0 {
		parts = append(parts, "auto-compact:"+strconv.Itoa(settings.ModelAutoCompactTokenLimit))
	}
	sourceText := string(sourceConfig)
	if profile := strings.TrimSpace(extractRootLevelConfigValue(sourceText, "profile")); profile != "" {
		parts = append(parts, "profile:"+profile)
	}
	if modelProvider := strings.TrimSpace(extractRootLevelConfigValue(sourceText, "model_provider")); modelProvider != "" {
		parts = append(parts, "root-model-provider:"+modelProvider)
	}
	return strings.Join(parts, "|")
}

func buildCodexSessionHomeDirName(homeKey string) string {
	normalized := strings.ToLower(strings.TrimSpace(homeKey))
	var slug strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug.WriteRune(r)
			lastDash = false
		case !lastDash:
			slug.WriteByte('-')
			lastDash = true
		}
	}
	slugText := strings.Trim(slug.String(), "-")
	if slugText == "" {
		slugText = "default"
	}
	if len(slugText) > 24 {
		slugText = strings.Trim(slugText[:24], "-")
		if slugText == "" {
			slugText = "default"
		}
	}
	sum := sha256.Sum256([]byte(homeKey))
	return fmt.Sprintf("%s-%x", slugText, sum[:6])
}

func (a *App) prepareCodexSessionHome(sessionID string, providerID string, settings codexLaunchSettings, envOverrides map[string]string) (string, error) {
	baseEnv := os.Environ()
	if a.EnvVars != nil {
		baseEnv = a.EnvVars.MergeWithSystem()
	}
	sourceHome, err := resolveCodexSourceHome(baseEnv)
	if err != nil {
		return "", err
	}

	sourceConfig, err := readCodexOptionalFile(filepath.Join(sourceHome, "config.toml"))
	if err != nil {
		return "", err
	}

	homeKey := buildCodexSessionHomeKey(providerID, settings, envOverrides, sourceConfig)
	targetHome := filepath.Join(a.codexSessionHomesRootDir(), buildCodexSessionHomeDirName(homeKey))
	_, statErr := os.Stat(targetHome)
	targetAlreadyExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("stat persistent codex home: %w", statErr)
	}
	if err := os.MkdirAll(targetHome, 0o755); err != nil {
		return "", fmt.Errorf("mkdir persistent codex home: %w", err)
	}
	cleanupTarget := !targetAlreadyExists
	defer func() {
		if cleanupTarget {
			_ = os.RemoveAll(targetHome)
		}
	}()

	isolatedConfig := append([]byte(nil), sourceConfig...)

	baseURL := strings.TrimSpace(envOverrides["AMAGI_CODEX_OPENAI_BASE_URL"])
	if baseURL == "" {
		baseURL = strings.TrimSpace(envOverrides["OPENAI_BASE_URL"])
	}
	delete(envOverrides, "AMAGI_CODEX_OPENAI_BASE_URL")
	delete(envOverrides, "OPENAI_BASE_URL")
	if baseURL != "" || settings.ModelContextWindow > 0 || settings.ModelAutoCompactTokenLimit > 0 {
		isolatedConfig = buildCodexIsolatedConfigToml(sourceConfig, codexLaunchSettings{
			Model:                      settings.Model,
			ModelContextWindow:         settings.ModelContextWindow,
			ModelAutoCompactTokenLimit: settings.ModelAutoCompactTokenLimit,
		}, baseURL)
	}
	if err := os.WriteFile(filepath.Join(targetHome, "config.toml"), isolatedConfig, 0o644); err != nil {
		return "", fmt.Errorf("write persistent config.toml: %w", err)
	}

	sourceAuth, err := readCodexOptionalFile(filepath.Join(sourceHome, "auth.json"))
	if err != nil {
		return "", err
	}
	isolatedAuth, err := buildCodexIsolatedAuthJSON(sourceAuth, envOverrides["OPENAI_API_KEY"])
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(targetHome, "auth.json"), isolatedAuth, 0o600); err != nil {
		return "", fmt.Errorf("write persistent auth.json: %w", err)
	}

	if err := copyCodexSafeAsset(filepath.Join(sourceHome, "version.json"), filepath.Join(targetHome, "version.json")); err != nil {
		return "", err
	}

	envOverrides["CODEX_HOME"] = targetHome
	a.registerCodexSessionHome(sessionID, codexSessionHomeInfo{Path: targetHome, HomeKey: homeKey})
	if a.Log != nil {
		a.Log.Info("codex", "使用持久 CODEX_HOME", fmt.Sprintf("id=%s key=%s path=%s", sessionID, homeKey, targetHome))
	}
	cleanupTarget = false
	return targetHome, nil
}

func readCodexOptionalFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
}

func copyCodexSafeAsset(srcPath, dstPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read safe codex asset %s: %w", filepath.Base(srcPath), err)
	}
	if err := os.WriteFile(dstPath, data, 0o644); err != nil {
		return fmt.Errorf("write safe codex asset %s: %w", filepath.Base(dstPath), err)
	}
	return nil
}

func buildCodexOpenAIBaseURLLine(baseURL string) []byte {
	return fmt.Appendf(nil, "openai_base_url = %q\n\n", baseURL)
}

func buildCodexModelProviderLine(provider string) []byte {
	return fmt.Appendf(nil, "model_provider = %q\n\n", provider)
}

func buildCodexIsolatedConfigToml(source []byte, settings codexLaunchSettings, baseURL string) []byte {
	result := append([]byte(nil), source...)
	if baseURL != "" {
		result = applyCodexBaseURLIsolation(result, baseURL)
	}
	result = applyCodexRootLevelIntSetting(result, "model_context_window", settings.ModelContextWindow)
	result = applyCodexRootLevelIntSetting(result, "model_auto_compact_token_limit", settings.ModelAutoCompactTokenLimit)
	return result
}

func applyCodexBaseURLIsolation(source []byte, baseURL string) []byte {
	if hasInjectedSection(string(source)) {
		if updated, ok := updateInjectedSectionBaseURL(string(source), baseURL); ok {
			return []byte(updated)
		}
	}
	cleaned := []byte(removeInjectedSection(string(source)))
	rootConfig := removeRootLevelOpenAIBaseURLEntries(removeRootLevelModelProviderEntries(cleaned))
	openAIBaseURLLine := buildCodexOpenAIBaseURLLine(baseURL)
	firstTableIdx := findFirstCodexTableHeaderStart(rootConfig)
	if firstTableIdx == -1 {
		result := rootConfig
		if len(result) > 0 && result[len(result)-1] != '\n' {
			result = append(result, '\n')
		}
		return append(result, openAIBaseURLLine...)
	}

	prefix := rootConfig[:firstTableIdx]
	suffix := rootConfig[firstTableIdx:]
	result := append([]byte(nil), prefix...)
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	result = append(result, openAIBaseURLLine...)
	result = append(result, suffix...)
	return result
}

func applyCodexRootLevelIntSetting(source []byte, key string, value int) []byte {
	cleaned := removeRootLevelConfigEntries(source, key)
	if value <= 0 {
		return cleaned
	}
	return insertRootLevelConfigLineBeforeFirstTable(cleaned, fmt.Sprintf("%s = %d\n", key, value))
}

func hasInjectedSection(content string) bool {
	return strings.Contains(content, codexInjectStartMarker) && strings.Contains(content, codexInjectEndMarker)
}

func findFirstCodexTableHeaderStart(source []byte) int {
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := bytes.IndexByte(source[lineStart:], '\n')
		if lineEnd == -1 {
			lineEnd = len(source)
		} else {
			lineEnd += lineStart
		}
		line := source[lineStart:lineEnd]
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) > 0 && trimmed[0] == '[' {
			return lineStart + (len(line) - len(trimmed))
		}
		if lineEnd == len(source) {
			break
		}
		lineStart = lineEnd + 1
	}
	return -1
}

func removeRootLevelModelProviderEntries(source []byte) []byte {
	var result bytes.Buffer
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := bytes.IndexByte(source[lineStart:], '\n')
		hasNewline := true
		if lineEnd == -1 {
			lineEnd = len(source)
			hasNewline = false
		} else {
			lineEnd += lineStart
		}
		line := source[lineStart:lineEnd]
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("model_provider")) {
			result.Write(line)
			if hasNewline {
				result.WriteByte('\n')
			}
		}
		if !hasNewline {
			break
		}
		lineStart = lineEnd + 1
	}
	return result.Bytes()
}

func removeRootLevelOpenAIBaseURLEntries(source []byte) []byte {
	var result bytes.Buffer
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := bytes.IndexByte(source[lineStart:], '\n')
		hasNewline := true
		if lineEnd == -1 {
			lineEnd = len(source)
			hasNewline = false
		} else {
			lineEnd += lineStart
		}
		line := source[lineStart:lineEnd]
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("openai_base_url")) {
			result.Write(line)
			if hasNewline {
				result.WriteByte('\n')
			}
		}
		if !hasNewline {
			break
		}
		lineStart = lineEnd + 1
	}
	return result.Bytes()
}

func removeRootLevelConfigEntries(source []byte, key string) []byte {
	var result bytes.Buffer
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := bytes.IndexByte(source[lineStart:], '\n')
		hasNewline := true
		if lineEnd == -1 {
			lineEnd = len(source)
			hasNewline = false
		} else {
			lineEnd += lineStart
		}
		line := source[lineStart:lineEnd]
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte(key)) {
			result.Write(line)
			if hasNewline {
				result.WriteByte('\n')
			}
		} else {
			rest := bytes.TrimSpace(trimmed[len(key):])
			if len(rest) == 0 || rest[0] != '=' {
				result.Write(line)
				if hasNewline {
					result.WriteByte('\n')
				}
			}
		}
		if !hasNewline {
			break
		}
		lineStart = lineEnd + 1
	}
	return result.Bytes()
}

func insertRootLevelConfigLineBeforeFirstTable(source []byte, line string) []byte {
	firstTableIdx := findFirstCodexTableHeaderStart(source)
	if firstTableIdx == -1 {
		result := append([]byte(nil), source...)
		if len(result) > 0 && result[len(result)-1] != '\n' {
			result = append(result, '\n')
		}
		return append(result, []byte(line)...)
	}

	prefix := source[:firstTableIdx]
	suffix := source[firstTableIdx:]
	result := append([]byte(nil), prefix...)
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	result = append(result, []byte(line)...)
	result = append(result, suffix...)
	return result
}

const (
	codexInjectStartMarker             = "# === amagi-codebox-inject-start ==="
	codexInjectEndMarker               = "# === amagi-codebox-inject-end ==="
	codexInjectedProviderSectionHeader = "[model_providers.amagi-codebox-provider]"
)

func updateInjectedSectionBaseURL(content string, baseURL string) (string, bool) {
	start := strings.Index(content, codexInjectStartMarker)
	if start == -1 {
		return "", false
	}
	endRel := strings.Index(content[start:], codexInjectEndMarker)
	if endRel == -1 {
		return "", false
	}
	end := start + endRel + len(codexInjectEndMarker)

	section := content[start:end]
	updatedSection, ok := replaceInjectedProviderBaseURL(section, baseURL)
	if !ok {
		return "", false
	}
	updatedSection = removeInjectedRootLevelConfigKey(updatedSection, "model_provider")
	updatedContent := content[:start] + updatedSection + content[end:]
	outerContent := content[:start] + content[end:]
	if hasRootLevelConfigKey(outerContent, "profile") || hasRootLevelConfigKey(outerContent, "model_provider") {
		return updatedContent, true
	}
	return insertRootLevelConfigBlockBeforeFirstTable(updatedContent, string(buildCodexModelProviderLine("amagi-codebox-provider"))), true
}

func replaceInjectedProviderBaseURL(section string, baseURL string) (string, bool) {
	lines := strings.Split(section, "\n")
	providerHeaderIndex := -1
	baseURLLineIndex := -1

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == codexInjectedProviderSectionHeader:
			providerHeaderIndex = i
		case providerHeaderIndex >= 0 && strings.HasPrefix(trimmed, "base_url = "):
			baseURLLineIndex = i
		case providerHeaderIndex >= 0 && strings.HasPrefix(trimmed, "[") && trimmed != codexInjectedProviderSectionHeader:
			i = len(lines)
		}
	}

	if providerHeaderIndex < 0 {
		return "", false
	}

	newLine := "base_url = " + strconv.Quote(baseURL)
	if baseURLLineIndex >= 0 {
		lines[baseURLLineIndex] = replaceConfigLineValue(lines[baseURLLineIndex], newLine)
		return strings.Join(lines, "\n"), true
	}
	lines = insertStringAt(lines, providerHeaderIndex+1, newLine)
	return strings.Join(lines, "\n"), true
}

func hasRootLevelConfigKey(content string, key string) bool {
	_, ok := findRootLevelConfigValue(content, key)
	return ok
}

func extractRootLevelConfigValue(content string, key string) string {
	value, _ := findRootLevelConfigValue(content, key)
	return value
}

func findRootLevelConfigValue(content string, key string) (string, bool) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			return "", false
		}
		if value, ok := parseRootLevelConfigAssignment(trimmed, key); ok {
			return value, true
		}
	}
	return "", false
}

func parseRootLevelConfigAssignment(trimmed string, key string) (string, bool) {
	if !strings.HasPrefix(trimmed, key) {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[len(key):])
	if rest == "" || rest[0] != '=' {
		return "", false
	}
	value := strings.TrimSpace(rest[1:])
	if value == "" {
		return "", true
	}
	if commentIndex := strings.Index(value, "#"); commentIndex >= 0 {
		value = strings.TrimSpace(value[:commentIndex])
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted, true
	}
	return value, true
}

func removeInjectedRootLevelConfigKey(section string, key string) string {
	lines := strings.Split(section, "\n")
	result := make([]string, 0, len(lines))
	needle := key + " ="
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			result = append(result, lines[i:]...)
			return strings.Join(result, "\n")
		}
		if strings.HasPrefix(trimmed, needle) {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func replaceConfigLineValue(originalLine, newValue string) string {
	trimmedLeft := strings.TrimLeft(originalLine, " \t")
	indent := originalLine[:len(originalLine)-len(trimmedLeft)]
	return indent + newValue
}

func insertStringAt(lines []string, index int, value string) []string {
	if index < 0 {
		index = 0
	}
	if index > len(lines) {
		index = len(lines)
	}
	lines = append(lines, "")
	copy(lines[index+1:], lines[index:])
	lines[index] = value
	return lines
}

func insertRootLevelConfigBlockBeforeFirstTable(content string, block string) string {
	firstTableIdx := findFirstCodexTableHeaderStart([]byte(content))
	if firstTableIdx == -1 {
		result := content
		if len(result) > 0 && result[len(result)-1] != '\n' {
			result += "\n"
		}
		return result + block
	}

	prefix := content[:firstTableIdx]
	suffix := content[firstTableIdx:]
	if len(prefix) > 0 && prefix[len(prefix)-1] != '\n' {
		prefix += "\n"
	}
	return prefix + block + suffix
}

func removeInjectedSection(content string) string {
	for {
		start := strings.Index(content, codexInjectStartMarker)
		if start == -1 {
			return content
		}
		end := strings.Index(content[start:], codexInjectEndMarker)
		if end == -1 {
			return content[:start]
		}
		end += start + len(codexInjectEndMarker)
		for end < len(content) && (content[end] == '\r' || content[end] == '\n') {
			end++
		}
		content = content[:start] + content[end:]
	}
}

func injectCodexConfigToml(baseURL string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")

	existing := ""
	if b, err := os.ReadFile(configPath); err == nil {
		existing = string(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read codex config: %w", err)
	}

	cleaned := removeInjectedSection(existing)
	injected := "\n# === amagi-codebox-inject-start ===\n" +
		"openai_base_url = " + strconv.Quote(baseURL) + "\n" +
		"# === amagi-codebox-inject-end ===\n"
	newContent := cleaned + injected

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("mkdir codex config dir: %w", err)
	}
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("write temp codex config: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace codex config: %w", err)
	}
	return nil
}

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

	sourceAuth, err := readCodexOptionalFile(authPath)
	if err != nil {
		return err
	}
	authContent, err := buildCodexIsolatedAuthJSON(sourceAuth, apiKey)
	if err != nil {
		return err
	}

	tmpPath := authPath + ".tmp"
	if err := os.WriteFile(tmpPath, authContent, 0o600); err != nil {
		return fmt.Errorf("write tmp auth.json: %w", err)
	}
	if err := os.Rename(tmpPath, authPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace auth.json: %w", err)
	}
	return nil
}

func buildCodexIsolatedAuthJSON(source []byte, apiKey string) ([]byte, error) {
	if apiKey == "" {
		if len(source) == 0 {
			return []byte("{}\n"), nil
		}
		return append([]byte(nil), source...), nil
	}

	authDoc := map[string]json.RawMessage{}
	trimmed := bytes.TrimSpace(source)
	if len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &authDoc); err != nil {
			authDoc = map[string]json.RawMessage{}
		} else if authDoc == nil {
			authDoc = map[string]json.RawMessage{}
		}
	}
	if apiKey != "" {
		apiKeyJSON, err := json.Marshal(apiKey)
		if err != nil {
			return nil, fmt.Errorf("marshal OPENAI_API_KEY: %w", err)
		}
		authDoc["OPENAI_API_KEY"] = apiKeyJSON
	}
	keys := make([]string, 0, len(authDoc))
	for key := range authDoc {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	if len(keys) > 0 {
		buf.WriteByte('\n')
		for i, key := range keys {
			buf.WriteString("  ")
			buf.WriteString(strconv.Quote(key))
			buf.WriteString(": ")
			buf.Write(authDoc[key])
			if i < len(keys)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
	}
	buf.WriteByte('}')
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func (a *App) cleanupCodexSessionHome(sessionID string) {
	a.codexSessionHomesMu.Lock()
	info, ok := a.codexSessionHomes[sessionID]
	if ok {
		delete(a.codexSessionHomes, sessionID)
	}
	a.codexSessionHomesMu.Unlock()
	if !ok || info.Path == "" {
		return
	}
	if a.Log != nil {
		a.Log.Info("codex", "持久 CODEX_HOME 已解除会话绑定", fmt.Sprintf("id=%s key=%s path=%s", sessionID, info.HomeKey, info.Path))
	}
}

func (a *App) cleanupCodexSessionHomesForStoppedSessions() {
	if a.Sessions == nil {
		return
	}
	a.codexSessionHomesMu.Lock()
	ids := make([]string, 0, len(a.codexSessionHomes))
	for sessionID := range a.codexSessionHomes {
		ids = append(ids, sessionID)
	}
	a.codexSessionHomesMu.Unlock()
	for _, sessionID := range ids {
		sess, err := a.Sessions.Get(sessionID)
		if err != nil || sess.Status != session.StatusRunning {
			a.cleanupCodexSessionHome(sessionID)
		}
	}
}

func (a *App) cleanupAllCodexSessionHomes() {
	a.codexSessionHomesMu.Lock()
	if a.codexSessionHomes == nil {
		a.codexSessionHomesMu.Unlock()
		return
	}
	count := len(a.codexSessionHomes)
	a.codexSessionHomes = map[string]codexSessionHomeInfo{}
	a.codexSessionHomesMu.Unlock()
	if a.Log != nil && count > 0 {
		a.Log.Info("codex", "已清空持久 CODEX_HOME 会话绑定", fmt.Sprintf("count=%d", count))
	}
}

// --- AmagiCode 设置 API ---

// GetAmagiSettings 返回 AmagiCode 完整设置。
// providers 中的 apiKey 字段从 SecretsService 填充后返回，不暴露原始文件中的值。
func (a *App) GetAmagiSettings() (*amagi.AmagiSettings, error) {
	settings := a.Amagi.GetSettings()
	if settings == nil {
		return nil, errors.New("amagi settings not loaded")
	}

	// 填充 providers 中的 API Key（从 SecretsService 获取）
	for name := range settings.Providers {
		if apiKey, _ := a.Secrets.GetAPIKey(name); apiKey != "" {
			if settings.Providers == nil {
				settings.Providers = map[string]amagi.AmagiProvider{}
			}
			provider := settings.Providers[name]
			provider.APIKey = apiKey
			settings.Providers[name] = provider
		}
	}

	return settings, nil
}

// SaveAmagiModelPreset 保存或更新 AmagiCode 模型预设组。
func (a *App) SaveAmagiModelPreset(name string, group amagi.ModelPresetGroup) error {
	return a.Amagi.SaveModelPreset(name, group)
}

// DeleteAmagiModelPreset 删除 AmagiCode 模型预设组。
func (a *App) DeleteAmagiModelPreset(name string) error {
	return a.Amagi.DeleteModelPreset(name)
}

// RenameAmagiModelPreset 重命名 AmagiCode 模型预设组。
func (a *App) RenameAmagiModelPreset(oldName, newName string) error {
	return a.Amagi.RenameModelPreset(oldName, newName)
}

// GetAmagiSubPreset 获取指定组内的子预设。
func (a *App) GetAmagiSubPreset(groupName, presetName string) (*amagi.AmagiModelPreset, error) {
	return a.Amagi.GetSubPreset(groupName, presetName)
}

// SaveAmagiSubPreset 保存或更新组内的子预设。
func (a *App) SaveAmagiSubPreset(groupName, presetName string, preset amagi.AmagiModelPreset) error {
	return a.Amagi.SaveSubPreset(groupName, presetName, preset)
}

// DeleteAmagiSubPreset 删除组内的子预设。
func (a *App) DeleteAmagiSubPreset(groupName, presetName string) error {
	return a.Amagi.DeleteSubPreset(groupName, presetName)
}

// GetAmagiSettingsJSON 返回 AmagiCode 设置的 JSON 字符串。
// 供前端 JSON 视图使用。
func (a *App) GetAmagiSettingsJSON() (string, error) {
	return a.Amagi.GetSettingsJSON()
}

// SaveAmagiSettingsJSON 从 JSON 字符串保存 AmagiCode 设置。
// 仅写入 9 个白名单字段，过滤未知字段。
// 供前端 JSON 视图使用。
func (a *App) SaveAmagiSettingsJSON(jsonStr string) error {
	return a.Amagi.SaveSettingsJSON(jsonStr)
}

// SetAmagiModel 设置 AmagiCode 默认模型。
func (a *App) SetAmagiModel(model string) error {
	return a.Amagi.SetModel(model)
}

// SetAmagiEffortLevel 设置 AmagiCode 努力级别。
func (a *App) SetAmagiEffortLevel(level string) error {
	return a.Amagi.SetEffortLevel(level)
}

// SetAmagiAvailableModels 设置 AmagiCode 可用模型列表。
func (a *App) SetAmagiAvailableModels(models []string) error {
	return a.Amagi.SetAvailableModels(models)
}

// --- 全局 OpenCode 配置 API ---

// GetOpenCodeConfig 读取全局 OpenCode 配置文件内容（JSON 文本）。
// 若文件不存在则返回默认空配置；若文件内容非法 JSON 则原样返回供用户修正。
func (a *App) GetOpenCodeConfig() (string, error) {
	return a.OpenCodeConfig.GetOpenCodeConfig()
}

// SaveOpenCodeConfig 校验并保存全局 OpenCode 配置文件。
// content 必须为合法 JSON，否则返回错误。
// 保存采用原子写入（先写临时文件再 rename），避免损坏。
func (a *App) SaveOpenCodeConfig(content string) error {
	return a.OpenCodeConfig.SaveOpenCodeConfig(content)
}

// GetOpenCodeConfigPath 返回全局 OpenCode 配置文件的绝对路径，供前端展示。
func (a *App) GetOpenCodeConfigPath() (string, error) {
	return a.OpenCodeConfig.GetOpenCodeConfigPath()
}
