package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/amagi"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/opencodeconfig"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
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

type codexLaunchSettings struct {
	Model string
}

const (
	codexModelProviderName     = "amagi-codebox-provider"
	codexOfficialOpenAIAPIHost = "api.openai.com"
)

type codexConfigSyncOptions struct {
	Model                string
	ModelProvider        string
	ProviderBaseURL      string
	EnsureCustomProvider bool
	ForceAPILogin        bool
	CleanupManagedConfig bool
}

type RemoteWebUIStatusResult struct {
	Openable                bool   `json:"openable"`
	Reason                  string `json:"reason"`
	URL                     string `json:"url"`
	Port                    int    `json:"port"`
	Running                 bool   `json:"running"`
	MobileWebRoot           string `json:"mobileWebRoot"`
	MobileWebRootConfigured bool   `json:"mobileWebRootConfigured"`
	MobileWebRootExists     bool   `json:"mobileWebRootExists"`
	MobileWebEmbedded       bool   `json:"mobileWebEmbedded"`
	MobileWebAvailable      bool   `json:"mobileWebAvailable"`
}

type OpenRemoteWebUIResult struct {
	URL     string `json:"url"`
	Port    int    `json:"port"`
	Running bool   `json:"running"`
}

// App 主应用结构体，负责跨服务协调和生命周期管理。
// 通过 Wails 绑定暴露给前端。
type App struct {
	ctx context.Context

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
	EnvCheck       *envcheck.Service

	Capabilities platform.PlatformCapabilities
	CLIResolver  platform.CLIResolver
	FileOpener   platform.FileOpener

	// startupWarnings 记录启动期间的警告信息，供前端拉取后向用户展示。
	startupWarnings   []string
	startupWarningsMu sync.Mutex
}

func NewApp(mobileAssets embed.FS) *App {
	configDir := defaultConfigDir()
	log := logging.NewService(configDir)
	envVarsSvc := envvars.NewEnvVarsService(configDir)
	capabilities := platform.CurrentCapabilities()
	pluginsSvc := plugin.NewService("", log)
	processRunner := platform.NewProcessRunner()

	app := &App{
		Config:         config.NewConfigService(configDir),
		Secrets:        secrets.NewSecretsService(configDir),
		Launcher:       launcher.NewLauncherService(log, envVarsSvc),
		Proxy:          proxy.NewProxyService(),
		Tray:           tray.NewService(),
		Sessions:       session.NewManager(),
		Paths:          paths.NewPathsService(configDir),
		Log:            log,
		Pty:            pty.NewService(log),
		Settings:       settings.NewService(configDir),
		EnvVars:        envVarsSvc,
		Updater:        updater.NewService(Version, log),
		Plugins:        pluginsSvc,
		Workspaces:     workspace.NewService(configDir, pluginsSvc, log),
		Amagi:          amagi.NewService(configDir),
		OpenCodeConfig: opencodeconfig.NewService(),
		EnvCheck:       envcheck.NewServiceWithRunner(processRunner),
		Capabilities:   capabilities,
		CLIResolver:    platform.NewCLIResolver(capabilities),
		FileOpener:     platform.NewFileOpener(processRunner),
	}
	// Remote 先以默认端口 8680 初始化；Startup 加载 Settings 后会同步持久化的端口。
	app.Remote = remote.NewServer(8680, app, log, mobileAssets)
	return app
}

// --- remote.AppInterface 实现 ---

func (a *App) GetSettingsService() *settings.Service   { return a.Settings }
func (a *App) GetPathsService() *paths.PathsService    { return a.Paths }
func (a *App) GetConfigService() *config.ConfigService { return a.Config }

func (a *App) platformCapabilities() platform.PlatformCapabilities {
	if a.Capabilities.PlatformID != "" {
		return a.Capabilities
	}
	return platform.CurrentCapabilities()
}

func (a *App) cliResolver() platform.CLIResolver {
	if a.CLIResolver != nil {
		return a.CLIResolver
	}
	return platform.NewCLIResolver(a.platformCapabilities())
}

func (a *App) fileOpener() platform.FileOpener {
	if a.FileOpener != nil {
		return a.FileOpener
	}
	return platform.NewFileOpener(platform.NewProcessRunner())
}

func (a *App) GetPlatformCapabilities() platform.PlatformCapabilities {
	return a.platformCapabilities()
}

// GetEnvCheckStatus 获取最近一次环境检测结果（可能为缓存）。
func (a *App) GetEnvCheckStatus() *envcheck.OverallStatus {
	return a.EnvCheck.GetCachedStatus()
}

// RunEnvCheck 手动触发环境检测。
func (a *App) RunEnvCheck() (*envcheck.OverallStatus, error) {
	return a.EnvCheck.CheckAll()
}

// CheckTool 手动触发单个 CLI 工具检测。
func (a *App) CheckTool(tool string) (*envcheck.CheckStatus, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("check tool: %w", err)
	}
	return a.EnvCheck.CheckOne(t)
}

// InstallTool 安装指定 CLI 工具（用户确认后调用）。
func (a *App) InstallTool(tool string) (*envcheck.InstallResult, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("install tool: %w", err)
	}
	return a.EnvCheck.Install(t)
}

// UpdateTool 更新指定 CLI 工具。
func (a *App) UpdateTool(tool string) (*envcheck.InstallResult, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("update tool: %w", err)
	}
	return a.EnvCheck.Update(t)
}

// StartInstallToolAsync 异步安装指定 CLI 工具，立即返回操作状态。
// 安装在后台 goroutine 中执行，不受前端页面切换影响。
func (a *App) StartInstallToolAsync(tool string) (*envcheck.OperationState, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("start install tool: %w", err)
	}
	return a.EnvCheck.StartInstallTool(t)
}

// StartUpdateToolAsync 异步更新指定 CLI 工具，立即返回操作状态。
// 更新在后台 goroutine 中执行，不受前端页面切换影响。
func (a *App) StartUpdateToolAsync(tool string) (*envcheck.OperationState, error) {
	t, err := parseCLITool(tool)
	if err != nil {
		return nil, fmt.Errorf("start update tool: %w", err)
	}
	return a.EnvCheck.StartUpdateTool(t)
}

// GetEnvCheckOperationState 获取当前异步操作状态（无操作时返回 nil）。
func (a *App) GetEnvCheckOperationState() *envcheck.OperationState {
	return a.EnvCheck.GetOperationState()
}

// GetEnvCheckSnapshot 获取环境检测快照（包含工具状态和当前操作）。
// 前端可轮询此接口以获取最新状态。
func (a *App) GetEnvCheckSnapshot() *envcheck.EnvCheckSnapshot {
	return a.EnvCheck.GetEnvCheckSnapshot()
}

// RunEnvFixAction 执行白名单化的环境修复动作。
// 前端传入 FixActionRequest，后端验证 action 类型后执行对应修复。
func (a *App) RunEnvFixAction(action string, tool string, extraPath string) (*envcheck.FixActionResult, error) {
	req := envcheck.FixActionRequest{
		Action:    envcheck.SolutionType(action),
		Tool:      envcheck.CLITool(tool),
		ExtraPath: extraPath,
	}
	result, err := a.EnvCheck.RunFixAction(req)
	if err != nil {
		return nil, fmt.Errorf("run fix action: %w", err)
	}
	// Best-effort: refresh check after successful fix
	if result != nil && result.Success && result.Changed {
		go func() {
			_, _ = a.EnvCheck.CheckAll()
		}()
	}
	return result, nil
}

// InstallClaudeWithMethod installs Claude Code using the specified method.
// method must be "npm" or "native".
func (a *App) InstallClaudeWithMethod(method string) (*envcheck.InstallResult, error) {
	// Convert frontend method string to ClaudeInstallMethod
	var m envcheck.ClaudeInstallMethod
	switch method {
	case "npm":
		m = envcheck.ClaudeInstallNPM
	case "native":
		m = envcheck.ClaudeInstallNative
	default:
		return nil, fmt.Errorf("不支持的安装方式: %s (支持: npm, native)", method)
	}

	return a.EnvCheck.InstallClaudeCodeWithMethod(m)
}

// StartInstallClaudeWithMethodAsync asynchronously installs Claude Code using
// the specified method and exposes progress through GetEnvCheckSnapshot.
func (a *App) StartInstallClaudeWithMethodAsync(method string) (*envcheck.OperationState, error) {
	var m envcheck.ClaudeInstallMethod
	switch method {
	case "npm":
		m = envcheck.ClaudeInstallNPM
	case "native":
		m = envcheck.ClaudeInstallNative
	default:
		return nil, fmt.Errorf("不支持的安装方式: %s (支持: npm, native)", method)
	}

	return a.EnvCheck.StartInstallClaudeCodeWithMethod(m)
}

// CleanClaudeInstall removes an existing Claude Code installation.
// method should be the current install method ("npm" or "native").
func (a *App) CleanClaudeInstall(method string) (*envcheck.InstallResult, error) {
	var m envcheck.InstallMethod
	switch method {
	case "npm":
		m = envcheck.InstallMethodNPM
	case "native":
		m = envcheck.InstallMethodNative
	default:
		return nil, fmt.Errorf("不支持的安装方式: %s", method)
	}

	return a.EnvCheck.CleanClaudeCode(m)
}

// UninstallClaudeCode removes an existing Claude Code installation without reinstalling.
// If method is empty, it auto-detects the current install method from the latest check.
func (a *App) UninstallClaudeCode(method string) (*envcheck.InstallResult, error) {
	targetMethod := method
	if targetMethod == "" {
		// Auto-detect from cached status
		status := a.EnvCheck.GetCachedStatus()
		if status == nil {
			return nil, fmt.Errorf("尚未执行环境检测，无法确定安装方式")
		}
		claudeStatus, ok := status.Items[string(envcheck.ToolClaudeCode)]
		if !ok || !claudeStatus.Installed {
			return nil, fmt.Errorf("Claude Code 未安装，无需卸载")
		}
		targetMethod = string(claudeStatus.InstallMethod)
	}

	var m envcheck.InstallMethod
	switch envcheck.InstallMethod(targetMethod) {
	case envcheck.InstallMethodNPM:
		m = envcheck.InstallMethodNPM
	case envcheck.InstallMethodNative:
		m = envcheck.InstallMethodNative
	default:
		return nil, fmt.Errorf("无法确定安装方式 (%s)，请手动指定", targetMethod)
	}

	result, err := a.EnvCheck.CleanClaudeCode(m)
	if err != nil {
		return result, err
	}
	return result, nil
}

// CheckClaudeConfig scans Claude Code configuration files and reports
// which configuration items are present or missing.
func (a *App) CheckClaudeConfig() (*envcheck.ClaudeConfigStatus, error) {
	return a.EnvCheck.CheckClaudeConfig()
}

// FixClaudeConfig writes a single configuration item to Claude Code settings.
// Only missing items are added; existing values are never overwritten.
func (a *App) FixClaudeConfig(key string, value string, filePath string) (*envcheck.ConfigFixResult, error) {
	// Defense-in-depth: validate file path at the binding layer too
	if filePath != "" {
		expanded := envcheck.ExpandTilde(filePath)
		if !envcheck.IsConfigPathAllowed(expanded) {
			return nil, fmt.Errorf("目标路径 %s 不在允许的配置文件列表中，拒绝写入", expanded)
		}
	}
	return a.EnvCheck.FixClaudeConfig(envcheck.ConfigFixRequest{
		Key:      key,
		Value:    value,
		FilePath: filePath,
	})
}

// parseCLITool 将前端传入的字符串转为 CLITool 枚举。
func parseCLITool(tool string) (envcheck.CLITool, error) {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "claude-code", "claude_code", "claude":
		return envcheck.ToolClaudeCode, nil
	case "opencode", "open-code", "open_code":
		return envcheck.ToolOpenCode, nil
	case "codex":
		return envcheck.ToolCodex, nil
	default:
		return "", fmt.Errorf("unknown CLI tool: %s", tool)
	}
}

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

// GetRemoteWebUIStatus 返回桌面入口 Web UI 的可打开状态。
func (a *App) GetRemoteWebUIStatus() RemoteWebUIStatusResult {
	status := RemoteWebUIStatusResult{
		Port:    a.Remote.GetPort(),
		Running: a.Remote.IsRunning(),
	}

	if a.ctx == nil {
		status.Reason = "app context is not ready"
		return status
	}

	webRoot, configured, exists := a.Remote.GetMobileWebRootStatus()
	embeddedAvailable := a.Remote.HasEmbeddedMobileWeb()

	status.MobileWebRoot = webRoot
	status.MobileWebRootConfigured = configured
	status.MobileWebRootExists = exists
	status.MobileWebEmbedded = embeddedAvailable
	status.MobileWebAvailable = exists || embeddedAvailable

	if !configured && !embeddedAvailable {
		status.Reason = "mobile web root is not configured"
		return status
	}

	if configured && !exists && !embeddedAvailable {
		status.Reason = "mobile web root index.html not found"
		return status
	}

	status.Openable = true
	status.URL = a.Remote.BuildDesktopLaunchURL()
	return status
}

// OpenRemoteWebUI 确保远程服务可用后，在默认浏览器中打开移动端 Web UI。
func (a *App) OpenRemoteWebUI() (OpenRemoteWebUIResult, error) {
	status := a.GetRemoteWebUIStatus()
	if !status.Openable {
		if status.Reason == "" {
			status.Reason = "remote web ui is not available"
		}
		return OpenRemoteWebUIResult{}, errors.New(status.Reason)
	}

	if !a.Remote.IsRunning() {
		if err := a.Remote.Start(a.ctx); err != nil {
			a.Log.Error("remote", "打开 Web UI 前启动远程服务器失败", err.Error())
			return OpenRemoteWebUIResult{}, fmt.Errorf("start remote server before opening web ui: %w", err)
		}
	}

	launchURL := a.Remote.BuildDesktopLaunchURL()
	wailsRuntime.BrowserOpenURL(a.ctx, launchURL)

	a.Log.Info("remote", "已打开桌面 Web UI", fmt.Sprintf("host=%s port=%d", a.Remote.GetHost(), a.Remote.GetPort()))
	return OpenRemoteWebUIResult{
		URL:     launchURL,
		Port:    a.Remote.GetPort(),
		Running: a.Remote.IsRunning(),
	}, nil
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

		// 自动迁移：将旧 provider.presets 迁移到 terminal_presets（幂等，不阻断启动）
		if count, changed, migrateErr := a.Config.MigrateProviderPresetsToTerminal(); migrateErr != nil {
			msg := fmt.Sprintf("旧预设自动迁移失败: %s。请前往设置 > 终端预设手动处理，或查看日志了解详情。", migrateErr.Error())
			a.Log.Warn("app", "自动迁移 provider presets 失败", migrateErr.Error())
			a.addStartupWarning(msg)
		} else if changed {
			a.Log.Info("app", "自动迁移完成", fmt.Sprintf("count=%d", count))
		}
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

	// 启动环境检测异步执行，不阻塞应用启动；检测结果由 EnvCheck 服务缓存。
	go func() {
		status, err := a.EnvCheck.CheckAll()
		if err != nil {
			a.Log.Warn("envcheck", "启动环境检测失败", err.Error())
			if status == nil {
				return
			}
		}
		if !status.AllOK {
			for _, issue := range status.Issues {
				a.addStartupWarning("[环境检测] " + issue)
			}
		}
	}()

	// 启动远程 API 服务器
	if err := a.Remote.Start(ctx); err != nil {
		a.Log.Warn("app", "远程服务器启动失败（不影响主功能）", err.Error())
	} else {
		a.Log.Info("app", "远程服务器已启动", fmt.Sprintf("port=%d", a.Remote.GetPort()))
	}

	// 启动系统托盘（仅在平台能力允许时）
	capabilities := a.platformCapabilities()
	if capabilities.SystemTraySupported && len(trayIcon) > 0 {
		a.Tray.Start(ctx, trayIcon, func() {
			a.Shutdown(ctx)
			wailsRuntime.Quit(ctx)
		})
		a.Log.Info("app", "系统托盘已启动")
	} else {
		a.Log.Info("app", "当前平台未启用系统托盘", fmt.Sprintf("platform=%s", capabilities.PlatformID))
	}
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

func (a *App) validateLaunchMode(mode string) error {
	return platform.ValidateLaunchRequest(a.platformCapabilities(), mode)
}

func embeddedDefaultLaunchMode(mode string) session.LaunchMode {
	launchMode := session.LaunchMode(mode)
	if launchMode == "" {
		return session.ModeEmbedded
	}
	return launchMode
}

func (a *App) resolveEmbeddedLaunchSpec(appType session.AppType, mode string, shellPath string, workDir string, env []string, args []string) (platform.ResolvedLaunchSpec, error) {
	if err := a.validateLaunchMode(mode); err != nil {
		return platform.ResolvedLaunchSpec{}, err
	}
	return a.cliResolver().Resolve(platform.ResolveRequest{
		AppType:            string(appType),
		LaunchMode:         mode,
		RequestedShellPath: shellPath,
		WorkDir:            workDir,
		Env:                env,
		CLIArgs:            args,
		PTYCols:            120,
		PTYRows:            40,
	})
}

// --- 多终端会话管理 ---

// LaunchSession 启动一个新的终端会话
func (a *App) LaunchSession(providerName, presetName string, mode string, workDir string, useProxy bool, shellPath string) (string, error) {
	a.Log.Info("session", "启动会话请求", fmt.Sprintf("provider=%s preset=%s mode=%s workDir=%s proxy=%v shell=%s", providerName, presetName, mode, workDir, useProxy, shellPath))

	// ---- terminal_presets 桥接 ----
	// 先尝试用 presetName 作为 terminal_preset 的 stable key 查找新体系
	tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset("claude_code", presetName)
	tpFound := tpErr == nil && tp != nil
	if tpFound {
		// 新体系中 provider 以 tp.Provider 为准，参数中传入的 providerName 作为 fallback
		if tpProvider != "" {
			providerName = tpProvider
		}
		a.Log.Info("session", "命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", presetName, tpProvider, tp.Model))
	}

	provider, err := a.Config.GetProvider(providerName)
	if err != nil {
		a.Log.Error("session", "获取提供商失败", err.Error())
		return "", fmt.Errorf("get provider: %w", err)
	}
	// 若命中 terminal preset，将其桥接为旧 config.Preset 注入 provider 副本
	// 这样后续 BuildOverrides / Launch 走完整旧链路，model + parameters 全部生效
	if tpFound {
		provCopy := *provider
		converted := config.Preset{
			Name:       tp.Name,
			Model:      tp.Model,
			Parameters: tp.Parameters,
		}
		if provCopy.Presets == nil {
			provCopy.Presets = map[string]config.Preset{}
		}
		provCopy.Presets[presetName] = converted
		*provider = provCopy
		a.Log.Info("session", "已桥接 terminal_preset 到 provider.Presets", fmt.Sprintf("key=%s model=%s", presetName, tp.Model))
	}
	if !provider.IsAnthropicCompatible() {
		a.Log.Error("session", "ClaudeCode 需要 Anthropic 格式提供商", "provider="+providerName)
		return "", fmt.Errorf("provider %q is not Anthropic-compatible and cannot be used to launch ClaudeCode", providerName)
	}

	// OAuth 模式（Anthropic）：白板启动，不设置任何代理环境变量
	// 非 OAuth 模式：正常代理启动，设置 ANTHROPIC_API_KEY 和 ANTHROPIC_BASE_URL
	var apiKey, keySource string
	if provider.IsOAuthMode() {
		// OAuth 模式不需要 API 密钥，使用 Claude Code 原生 OAuth 认证
		apiKey = ""
		keySource = "oauth"
		a.Log.Info("session", "使用 OAuth 认证（白板启动）", "provider="+providerName)
	} else {
		apiKey, keySource = a.getProviderAPIKey(providerName, *provider)
		if apiKey == "" {
			a.Log.Error("session", "未找到API密钥", "provider="+providerName)
			return "", fmt.Errorf("no API key found for provider %q", providerName)
		}
		a.Log.Info("session", "API密钥已获取",
			fmt.Sprintf("provider=%s source=%s key=%s len=%d",
				providerName, keySource, secrets.MaskKey(apiKey), len(apiKey)))
	}

	agentTeams := a.Config.GetAgentTeams()

	// 模型名称：由 BuildOverrides 从 provider.Presets[presetName] 中读取
	// （旧链路或已桥接的 terminal preset 均已注入 provider.Presets）
	preset, hasPreset := provider.Presets[presetName]
	model := provider.DefaultModel
	if hasPreset && preset.Model != "" {
		model = preset.Model
	}

	// 确定启动模式
	launchMode := session.LaunchMode(mode)
	if launchMode == "" {
		launchMode = session.ModeTerminal
	}
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
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
			if err := a.Proxy.Start(port, provider.EffectiveBaseURL("anthropic")); err != nil {
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

		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeClaudeCode, string(launchMode), shellPath, workDir, env, nil)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}

		pid, err := a.Pty.StartResolved(sess.ID, spec)
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
// Codex 进程直接继承用户原始环境中的 Codex home，不做任何隔离或改写。
func (a *App) LaunchCodexSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 Codex 会话请求", fmt.Sprintf("model=%s provider=%s mode=%s workDir=%s shell=%s", modelName, providerID, mode, workDir, shellPath))

	// ---- terminal_presets 桥接 ----
	// modelName 可能是 terminal_preset 的 stable key（形如 "provider/presetName"）
	tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset("codex", modelName)
	tpFound := tpErr == nil && tp != nil
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelName = tp.Model
		a.Log.Info("session", "Codex 命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", modelName, tpProvider, tp.Model))
	}

	// ---- legacy provider preset fallback ----
	// 若未命中新体系，且 providerID 非空，检查是否是旧的 provider.Presets key。
	// 旧 key 如 "default" 不是模型名，需要从 preset.Model 中解析真实模型名。
	if !tpFound && providerID != "" {
		if provider, pErr := a.Config.GetProvider(providerID); pErr == nil {
			if preset, ok := provider.Presets[modelName]; ok {
				resolvedModel := preset.Model
				if resolvedModel == "" {
					resolvedModel = provider.DefaultModel
				}
				a.Log.Info("session", "Codex 命中旧 provider preset", fmt.Sprintf("key=%s presetModel=%s defaultModel=%s -> resolved=%s", modelName, preset.Model, provider.DefaultModel, resolvedModel))
				modelName = resolvedModel
			}
		}
	}

	// 确定启动模式
	launchMode := embeddedDefaultLaunchMode(mode)
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
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

	// 构建环境变量注入：若指定了 providerID，根据 Provider 的 Type 注入对应的环境变量。
	envOverrides := map[string]string{}
	codexProviderBaseURL := ""
	injectProviderEnv := func(pid string, provider *config.Provider) {
		apiKey, _ := a.getProviderAPIKey(pid, *provider)
		if apiKey == "" {
			return
		}
		if isOpenAIProvider(*provider) {
			envOverrides["OPENAI_API_KEY"] = apiKey
			if baseURL := provider.EffectiveBaseURL("openai"); baseURL != "" {
				envOverrides["OPENAI_BASE_URL"] = baseURL
				if isCustomCodexOpenAIBaseURL(baseURL) {
					codexProviderBaseURL = baseURL
				}
			}
		} else {
			envOverrides["ANTHROPIC_API_KEY"] = apiKey
			if baseURL := provider.EffectiveBaseURL("anthropic"); baseURL != "" {
				envOverrides["ANTHROPIC_BASE_URL"] = baseURL
			}
		}
	}
	if providerID != "" {
		if provider, err := a.Config.GetProvider(providerID); err == nil {
			launchSettings = resolveCodexLaunchSettings(*provider, launchSettings.Model)
			injectProviderEnv(providerID, provider)
		}
	}

	// 同步 Codex config.toml。仅当 OpenAI 兼容 provider 同时具备自定义 BaseURL 和 API key 时，
	// 写入自定义 provider 与 api 登录约束；官方/无 BaseURL 路径会清理 amagi 托管配置，避免污染官方登录。
	if launchSettings.Model != "" {
		var err error
		if codexProviderBaseURL != "" {
			err = syncCodexCustomProviderConfig(launchSettings.Model, codexProviderBaseURL)
		} else {
			err = syncCodexConfigModel(launchSettings.Model)
		}
		if err != nil {
			a.Log.Warn("codex", "sync config.toml model failed", fmt.Sprintf("model=%s err=%v", launchSettings.Model, err))
		}
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypeCodex, "codex", providerID, launchSettings.Model, launchMode, workDir, false)
	a.Log.Info("session", "Codex 会话已创建", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, launchSettings.Model, launchMode))

	// 调试日志：输出 envOverrides 注入情况
	envKeys := make([]string, 0, len(envOverrides))
	for k := range envOverrides {
		envKeys = append(envKeys, k)
	}
	a.Log.Info("codex", "envOverrides keys", fmt.Sprintf("%v", envKeys))

	// 内嵌终端模式：使用 ConPTY
	if launchMode == session.ModeEmbedded {
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		args := []string{}
		if launchSettings.Model != "" {
			args = append(args, "-m", launchSettings.Model)
		}
		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeCodex, string(launchMode), shellPath, workDir, env, args)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}

		pid, err := a.Pty.StartResolved(sess.ID, spec)
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
	result, err := a.Launcher.LaunchCodex(sess.ID, launchSettings.Model, launchMode, workDir, envOverrides)
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

func normalizeCodexModelName(modelName string) string {
	trimmed := strings.TrimSpace(modelName)
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "[1m]") {
		trimmed = strings.TrimSpace(trimmed[:len(trimmed)-len("[1m]")])
	}
	return trimmed
}

// syncCodexConfigModel updates the top-level model in an existing Codex config.toml
// and removes amagi-managed provider state so official Codex login can recover.
func syncCodexConfigModel(model string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	return syncCodexConfigFile(configPath, codexConfigSyncOptions{Model: model, CleanupManagedConfig: true})
}

func syncCodexCustomProviderConfig(model, baseURL string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	return syncCodexCustomProviderConfigFile(configPath, model, baseURL)
}

func syncCodexCustomProviderConfigFile(configPath, model, baseURL string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("codex model is empty")
	}
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("codex provider base_url is empty")
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	return syncCodexConfigFile(configPath, codexConfigSyncOptions{
		Model:                model,
		ModelProvider:        codexModelProviderName,
		ProviderBaseURL:      baseURL,
		EnsureCustomProvider: true,
		ForceAPILogin:        true,
	})
}

func syncCodexConfigFile(configPath string, opts codexConfigSyncOptions) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || !opts.EnsureCustomProvider {
			return fmt.Errorf("read config.toml: %w", err)
		}
	}

	content := string(data)
	lines := []string{}
	if content != "" {
		lines = strings.Split(content, "\n")
	}

	if opts.EnsureCustomProvider {
		lines = removeCodexManagedProviderSection(lines, opts.ModelProvider)
		topLevelAssignments := []string{
			"model = " + strconv.Quote(opts.Model),
			"model_provider = " + strconv.Quote(opts.ModelProvider),
		}
		if opts.ForceAPILogin {
			topLevelAssignments = append(topLevelAssignments, "forced_login_method = "+strconv.Quote("api"))
		}
		lines = syncCodexTopLevelAssignments(lines, topLevelAssignments, true)
		lines = appendCodexCustomProviderSection(lines, opts.ModelProvider, opts.ProviderBaseURL)
	} else {
		if opts.CleanupManagedConfig {
			lines = cleanupCodexManagedProviderConfig(lines, codexModelProviderName)
		}
		updated := syncCodexTopLevelAssignments(lines, []string{"model = " + strconv.Quote(opts.Model)}, false)
		if countTopLevelAssignment(updated, "model") == 0 {
			return fmt.Errorf("top-level model field not found in %s", configPath)
		}
		lines = updated
	}

	return os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0644)
}

func syncCodexTopLevelAssignments(lines []string, assignmentLines []string, insertMissing bool) []string {
	wanted := make(map[string]string, len(assignmentLines))
	order := make([]string, 0, len(assignmentLines))
	seen := make(map[string]bool, len(assignmentLines))
	for _, assignment := range assignmentLines {
		key, ok := tomlAssignmentKey(strings.TrimSpace(assignment))
		if !ok {
			continue
		}
		wanted[key] = assignment
		order = append(order, key)
	}

	updated := make([]string, 0, len(lines)+len(assignmentLines)+1)
	inTopLevel := true
	insertMissingBeforeSection := func() {
		if !insertMissing {
			return
		}
		for _, key := range order {
			if !seen[key] {
				updated = append(updated, wanted[key])
				seen[key] = true
			}
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			insertMissingBeforeSection()
			inTopLevel = false
		}

		if inTopLevel {
			if key, ok := tomlAssignmentKey(trimmed); ok {
				if replacement, exists := wanted[key]; exists {
					if seen[key] {
						continue
					}
					updated = append(updated, replacement)
					seen[key] = true
					continue
				}
			}
		}

		updated = append(updated, line)
	}

	insertMissingBeforeSection()
	return trimTrailingEmptyLines(updated)
}

func removeCodexManagedProviderSection(lines []string, modelProvider string) []string {
	if modelProvider == "" {
		return lines
	}
	providerHeader := "[model_providers." + modelProvider + "]"
	providerSubHeaderPrefix := "[model_providers." + modelProvider + "."
	updated := make([]string, 0, len(lines))
	skipOwnedBlock := false
	skipProviderSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# === amagi-codebox-inject-start ===" {
			skipOwnedBlock = true
			continue
		}
		if skipOwnedBlock {
			if trimmed == "# === amagi-codebox-inject-end ===" {
				skipOwnedBlock = false
			}
			continue
		}
		if isTomlTableHeader(trimmed) {
			if trimmed == providerHeader || strings.HasPrefix(trimmed, providerSubHeaderPrefix) {
				skipProviderSection = true
				continue
			}
			skipProviderSection = false
		}
		if skipProviderSection {
			continue
		}
		updated = append(updated, line)
	}
	return trimTrailingEmptyLines(updated)
}

func cleanupCodexManagedProviderConfig(lines []string, modelProvider string) []string {
	if modelProvider == "" {
		return lines
	}
	managedTopLevelProvider := topLevelAssignmentValueEquals(lines, "model_provider", modelProvider)

	lines = removeCodexManagedProviderSection(lines, modelProvider)
	updated := make([]string, 0, len(lines))
	inTopLevel := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			inTopLevel = false
		}
		if inTopLevel {
			if key, ok := tomlAssignmentKey(trimmed); ok {
				switch key {
				case "model_provider":
					if tomlAssignmentValueEquals(trimmed, modelProvider) {
						continue
					}
				case "forced_login_method":
					if managedTopLevelProvider && tomlAssignmentValueEquals(trimmed, "api") {
						continue
					}
				}
			}
		}
		updated = append(updated, line)
	}
	return trimTrailingEmptyLines(updated)
}

func topLevelAssignmentValueEquals(lines []string, key, value string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isTomlTableHeader(trimmed) {
			return false
		}
		if gotKey, ok := tomlAssignmentKey(trimmed); ok && gotKey == key {
			return tomlAssignmentValueEquals(trimmed, value)
		}
	}
	return false
}

func tomlAssignmentValueEquals(trimmedLine, value string) bool {
	idx := strings.Index(trimmedLine, "=")
	if idx == -1 {
		return false
	}
	rawValue := strings.TrimSpace(trimmedLine[idx+1:])
	if rawValue == "" {
		return false
	}
	if rawValue[0] == '"' || rawValue[0] == '\'' {
		quote := rawValue[0]
		for i := 1; i < len(rawValue); i++ {
			if quote == '"' && rawValue[i] == '\\' {
				i++
				continue
			}
			if rawValue[i] == quote {
				literal := rawValue[:i+1]
				if quote == '\'' {
					return literal[1:len(literal)-1] == value
				}
				unquoted, err := strconv.Unquote(literal)
				return err == nil && unquoted == value
			}
		}
	}
	if commentIdx := strings.Index(rawValue, "#"); commentIdx != -1 {
		rawValue = strings.TrimSpace(rawValue[:commentIdx])
	}
	if unquoted, err := strconv.Unquote(rawValue); err == nil {
		return unquoted == value
	}
	return strings.Trim(rawValue, "\"'") == value
}

func appendCodexCustomProviderSection(lines []string, modelProvider, baseURL string) []string {
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "")
	}
	lines = append(lines,
		"# === amagi-codebox-inject-start ===",
		"[model_providers."+modelProvider+"]",
		"name = "+strconv.Quote(modelProvider),
		"base_url = "+strconv.Quote(baseURL),
		"env_key = "+strconv.Quote("OPENAI_API_KEY"),
		"requires_openai_auth = false",
		"wire_api = "+strconv.Quote("responses"),
		"# === amagi-codebox-inject-end ===",
	)
	return lines
}

func countTopLevelAssignment(lines []string, key string) int {
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isTomlTableHeader(trimmed) {
			return count
		}
		if got, ok := tomlAssignmentKey(trimmed); ok && got == key {
			count++
		}
	}
	return count
}

func trimTrailingEmptyLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func tomlAssignmentKey(trimmedLine string) (string, bool) {
	if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
		return "", false
	}
	idx := strings.Index(trimmedLine, "=")
	if idx == -1 {
		return "", false
	}
	key := strings.TrimSpace(trimmedLine[:idx])
	if key == "" {
		return "", false
	}
	return key, true
}

func isTomlTableHeader(trimmedLine string) bool {
	return strings.HasPrefix(trimmedLine, "[")
}

func isCustomCodexOpenAIBaseURL(baseURL string) bool {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return false
	}
	parseTarget := trimmed
	if !strings.Contains(parseTarget, "://") {
		parseTarget = "https://" + parseTarget
	}
	parsed, err := url.Parse(parseTarget)
	if err != nil || parsed.Host == "" {
		normalized := strings.TrimRight(strings.ToLower(trimmed), "/")
		return normalized != "https://api.openai.com" && normalized != "https://api.openai.com/v1" && normalized != "api.openai.com" && normalized != "api.openai.com/v1"
	}
	if strings.ToLower(parsed.Hostname()) != codexOfficialOpenAIAPIHost {
		return true
	}
	path := strings.TrimRight(strings.ToLower(parsed.EscapedPath()), "/")
	return path != "" && path != "/v1"
}

// isOpenAIProvider reports whether the provider uses the OpenAI-compatible API.
// Delegates to the Provider's dual-format compatibility method.
func isOpenAIProvider(p config.Provider) bool {
	return p.IsOpenAICompatible()
}

func appendUniqueNonEmpty(values []string, candidates ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(candidates))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		seen[trimmed] = struct{}{}
	}
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(strings.ToLower(candidate))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		values = append(values, trimmed)
		seen[trimmed] = struct{}{}
	}
	return values
}

func legacyProviderAPIKeyCandidates(provider config.Provider) []string {
	candidates := []string{}
	preferred := provider.PreferredFormat()
	if preferred != "" {
		candidates = appendUniqueNonEmpty(candidates, preferred)
	}
	if provider.IsAnthropicCompatible() {
		candidates = appendUniqueNonEmpty(candidates, "anthropic")
	}
	if provider.IsOpenAICompatible() {
		candidates = appendUniqueNonEmpty(candidates, "openai")
	}
	return candidates
}

func legacyProviderAPIKeyCandidatesForFormat(format string) []string {
	candidates := []string{}
	switch strings.TrimSpace(strings.ToLower(format)) {
	case "anthropic":
		candidates = appendUniqueNonEmpty(candidates, "anthropic", "openai")
	case "openai":
		candidates = appendUniqueNonEmpty(candidates, "openai", "anthropic")
	default:
		candidates = appendUniqueNonEmpty(candidates, "anthropic", "openai")
	}
	return candidates
}

func (a *App) getProviderAPIKeyWithLegacyCandidates(providerName string, legacyCandidates []string) (string, string) {
	if key, source := a.Secrets.GetAPIKeyWithFallback(providerName); key != "" {
		return key, source
	}
	for _, format := range legacyCandidates {
		if key, err := a.Secrets.GetAPIKey(providerName + ":" + format); err == nil {
			trimmed := strings.TrimSpace(key)
			if trimmed != "" {
				return trimmed, "legacy:" + format
			}
		}
	}
	return "", ""
}

// getProviderAPIKey 读取指定 provider 的统一 API key。
// 新模型优先读取 providerName；若缺失，再兼容旧命名 providerName:anthropic / providerName:openai。
// 返回 (apiKey, source)。
func (a *App) getProviderAPIKey(providerName string, provider config.Provider) (string, string) {
	return a.getProviderAPIKeyWithLegacyCandidates(providerName, legacyProviderAPIKeyCandidates(provider))
}

// getProviderAPIKeyForFormat 读取指定格式下可用的统一 API key。
// 新模型优先读取 providerName；若缺失，再兼容旧命名 providerName:format。
// 若指定格式的 legacy key 不存在，会继续尝试另一种 legacy key，确保旧数据可读。
func (a *App) getProviderAPIKeyForFormat(providerName, format string) (string, string) {
	return a.getProviderAPIKeyWithLegacyCandidates(providerName, legacyProviderAPIKeyCandidatesForFormat(format))
}

func buildProviderFromExportProvider(ep config.ExportProvider) config.Provider {
	return ep.ToProvider()
}

func selectImportedProviderAPIKey(ep config.ExportProvider) string {
	return ep.UnifiedAPIKey()
}

func buildExportProvider(provider config.Provider, apiKey string) config.ExportProvider {
	return config.BuildExportProvider(provider, apiKey)
}

func (a *App) saveImportedProviderAPIKey(providerName, apiKey string) error {
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return nil
	}
	if err := a.Secrets.SetAPIKey(providerName, trimmed); err != nil {
		return err
	}
	_ = a.Secrets.DeleteAPIKey(providerName + ":anthropic")
	_ = a.Secrets.DeleteAPIKey(providerName + ":openai")
	return nil
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

	if matchedPreset != nil {
		if normalizedPresetModel := normalizeCodexModelName(matchedPreset.Model); normalizedPresetModel != "" {
			settings.Model = normalizedPresetModel
		}
	}

	return settings
}

// GetProvidersByType 返回指定类型的 Provider 列表（type 为 "openai" 或 "anthropic"）
// 使用双格式兼容方法判断 Provider 类型。
func (a *App) GetProvidersByType(providerType string) map[string]config.Provider {
	allProviders := a.Config.GetProviders()
	result := make(map[string]config.Provider)
	for name, p := range allProviders {
		switch providerType {
		case "openai":
			if p.IsOpenAICompatible() {
				result[name] = p
			}
		case "anthropic":
			if p.IsAnthropicCompatible() {
				result[name] = p
			}
		}
	}
	return result
}

// LaunchOpenCode 启动 OpenCode 终端会话。
// 双轨兼容：优先查 opencode_presets（新模型），回退到 terminal_presets.opencode（旧模型）。
func (a *App) LaunchOpenCode(providerName string, presetName string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 OpenCode 会话请求", fmt.Sprintf("provider=%s preset=%s mode=%s workDir=%s shell=%s", providerName, presetName, mode, workDir, shellPath))

	envOverrides := map[string]string{}
	var provider *config.Provider

	// ============================================================
	// 轨道 1（新模型）：opencode_presets
	// ============================================================
	if presetName != "" {
		if ocPreset, err := a.Config.GetOpenCodePreset(presetName); err == nil && ocPreset != nil {
			a.Log.Info("session", "OpenCode 命中 opencode_preset（新模型）", fmt.Sprintf("key=%s name=%s bindings=%d", presetName, ocPreset.Name, len(ocPreset.Bindings)))

			// 构建 getAPIKey 函数：按 binding 的 local_provider + format 读取 secrets
			getAPIKey := func(localProvider, _ string) (string, error) {
				if local, err := a.Config.GetProvider(localProvider); err == nil && local != nil {
					key, _ := a.getProviderAPIKey(localProvider, *local)
					return key, nil
				}
				key, _ := a.getProviderAPIKeyWithLegacyCandidates(localProvider, legacyProviderAPIKeyCandidatesForFormat(""))
				return key, nil
			}

			// 构建 getProvider 函数：读取本地 Provider 配置（用于推导格式和注入 baseURL/organization）
			getProvider := func(providerName string) (*config.Provider, error) {
				return a.Config.GetProvider(providerName)
			}

			// 用新模型构建运行时配置
			ocOverrides, err := launcher.BuildOpenCodeEnvOverridesFromPreset(*ocPreset, getAPIKey, getProvider)
			if err != nil {
				a.Log.Error("session", "构建 OpenCode 配置失败（新模型）", err.Error())
				return "", fmt.Errorf("build opencode config from preset: %w", err)
			}
			envOverrides = ocOverrides

			// 新模型下 providerName 无意义，session 中记录 preset key
			providerName = "opencode-preset:" + presetName

			// 验证 bindings 中本地 provider 是否存在
			for ocProvID, binding := range ocPreset.Bindings {
				if binding.LocalProvider != "" {
					if _, pErr := a.Config.GetProvider(binding.LocalProvider); pErr != nil {
						a.Log.Warn("session", "binding 引用的本地 provider 不存在", fmt.Sprintf("binding=%s localProvider=%s err=%v", ocProvID, binding.LocalProvider, pErr))
						// 不直接阻断，但记录警告
					}
				}
			}

			goto launchCommon
		}
	}

	// ============================================================
	// 轨道 2（旧模型回退）：terminal_presets.opencode
	// ============================================================
	{
		// presetName 可能是 terminal_preset 的 stable key
		tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset("opencode", presetName)
		tpFound := tpErr == nil && tp != nil
		if tpFound {
			if tpProvider != "" {
				providerName = tpProvider
			}
			a.Log.Info("session", "OpenCode 命中 terminal_preset（旧模型回退）", fmt.Sprintf("key=%s provider=%s model=%s hasCfg=%v", presetName, tpProvider, tp.Model, len(tp.OpenCodeCfg) > 0))
		}

		if providerName != "" {
			loadedProvider, err := a.Config.GetProvider(providerName)
			if err != nil {
				a.Log.Error("session", "获取 OpenCode 提供商失败", err.Error())
				return "", fmt.Errorf("get opencode provider: %w", err)
			}
			provider = loadedProvider

			// 若命中 terminal preset，桥接为旧 config.Preset 注入 provider 副本
			if tpFound {
				provCopy := *provider
				converted := config.Preset{
					Name:           tp.Name,
					Model:          tp.Model,
					Parameters:     tp.Parameters,
					OpenCodeConfig: tp.OpenCodeCfg,
				}
				if provCopy.Presets == nil {
					provCopy.Presets = map[string]config.Preset{}
				}
				provCopy.Presets[presetName] = converted
				*provider = provCopy
				a.Log.Info("session", "OpenCode 已桥接 terminal_preset 到 provider.Presets", fmt.Sprintf("key=%s model=%s", presetName, tp.Model))
			}

			apiKey, keySource := a.getProviderAPIKey(providerName, *provider)
			if apiKey == "" {
				a.Log.Error("session", "未找到 OpenCode API 密钥", "provider="+providerName)
				return "", fmt.Errorf("no API key found for provider %q", providerName)
			}

			// 基于 Provider + Preset（含桥接后的 terminal preset）生成 OPENCODE_CONFIG_CONTENT 注入
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
	}

launchCommon:
	// 确定启动模式
	launchMode := embeddedDefaultLaunchMode(mode)
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
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
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeOpenCode, string(launchMode), shellPath, workDir, env, nil)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}

		pid, err := a.Pty.StartResolved(sess.ID, spec)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "OpenCode PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start opencode pty: %w", err)
		}
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "OpenCode PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

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
	var apiKey string
	if provider != nil {
		apiKey, _ = a.getProviderAPIKey(sessionProvider, *provider)
	}
	result, err := a.Launcher.LaunchOpenCode(sess.ID, launchMode, workDir, envOverrides, "", provider, presetName, apiKey)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "OpenCode 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch opencode: %w", err)
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "OpenCode 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

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
	apiKey, keySource := a.getProviderAPIKey(actualProviderName, *provider)
	if apiKey == "" {
		a.Log.Error("session", "未找到 AmagiCode API 密钥", "provider="+actualProviderName)
		return "", fmt.Errorf("no API key found for provider %q", actualProviderName)
	}
	a.Log.Info("session", "AmagiCode API 密钥已获取",
		fmt.Sprintf("provider=%s source=%s key=%s len=%d",
			actualProviderName, keySource, secrets.MaskKey(apiKey), len(apiKey)))

	// 构建环境变量注入
	envOverrides := map[string]string{}
	if provider.IsOpenAICompatible() {
		envOverrides["OPENAI_API_KEY"] = apiKey
		if baseURL := provider.EffectiveBaseURL("openai"); baseURL != "" {
			envOverrides["OPENAI_BASE_URL"] = baseURL
		}
	} else {
		envOverrides["ANTHROPIC_API_KEY"] = apiKey
		envOverrides["ANTHROPIC_AUTH_TOKEN"] = ""
		if baseURL := provider.EffectiveBaseURL("anthropic"); baseURL != "" {
			envOverrides["ANTHROPIC_BASE_URL"] = baseURL
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
	}

	// 确定启动模式
	launchMode := embeddedDefaultLaunchMode(mode)
	if err := a.validateLaunchMode(string(launchMode)); err != nil {
		return "", err
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

		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeAmagiCode, string(launchMode), shellPath, workDir, env, nil)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}

		pid, err := a.Pty.StartResolved(sess.ID, spec)
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

// addStartupWarning 记录一条启动期间的警告，供前端通过 GetStartupWarnings 拉取。
func (a *App) addStartupWarning(msg string) {
	a.startupWarningsMu.Lock()
	a.startupWarnings = append(a.startupWarnings, msg)
	a.startupWarningsMu.Unlock()
}

// GetStartupWarnings 返回启动期间积累的警告信息列表。
// 前端在 onMounted 中调用一次，用 toast 展示给用户。
func (a *App) GetStartupWarnings() []string {
	a.startupWarningsMu.Lock()
	defer a.startupWarningsMu.Unlock()
	if len(a.startupWarnings) == 0 {
		return []string{}
	}
	out := make([]string, len(a.startupWarnings))
	copy(out, a.startupWarnings)
	return out
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

// GetOutputHistorySnapshot returns a JSON-encoded snapshot of the output history
// along with the emitSeq at snapshot time. The JSON structure is:
//
//	{"data": "<base64-encoded bytes>", "seq": <uint64>}
//
// Frontend uses the seq to deduplicate live events: any live event with
// seq <= the returned seq is already contained in the history snapshot.
func (a *App) GetOutputHistorySnapshot(sessionID string) (string, error) {
	data, seq, err := a.Pty.GetOutputHistoryWithSeq(sessionID)
	if err != nil {
		return "", err
	}
	result := struct {
		Data string `json:"data"`
		Seq  uint64 `json:"seq"`
	}{
		Data: base64.StdEncoding.EncodeToString(data),
		Seq:  seq,
	}
	bytes, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal history snapshot: %w", err)
	}
	return string(bytes), nil
}

// GetPtyDimensions 返回指定 PTY 会话的当前尺寸。
// 实现 remote.DimensionsProvider 接口。
func (a *App) GetPtyDimensions(sessionID string) (cols, rows int, err error) {
	return a.Pty.GetPtyDimensions(sessionID)
}

// AttachSessionObserver 原子返回 history / dimensions 快照并注册 live 回调。
func (a *App) AttachSessionObserver(sessionID string, id string, outputCB func(data []byte), resizeCB func(cols, rows int)) ([]byte, int, int, error) {
	return a.Pty.AttachSessionObserver(sessionID, id, outputCB, resizeCB)
}

// DetachSessionObserver 注销通过 AttachSessionObserver 注册的 live 回调。
func (a *App) DetachSessionObserver(sessionID string, id string) {
	a.Pty.DetachSessionObserver(sessionID, id)
}

// OpenFileInEditor 使用系统默认程序打开指定文件。
// filePath 可以是绝对路径或相对路径；line 参数保留兼容但不使用（系统默认程序通常不支持行号定位）。
func (a *App) OpenFileInEditor(filePath string, line int) error {
	_ = line
	// 先验证文件是否存在，避免打开不存在的路径时创建空文件
	if _, err := os.Stat(filePath); err != nil {
		a.Log.Debug("app", "文件不存在，跳过打开", filePath)
		return fmt.Errorf("file not found: %s", filePath)
	}
	if err := a.fileOpener().Open(filePath); err != nil {
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
	terminalPresets := a.Config.GetAllTerminalPresets()
	openCodePresets := a.Config.GetAllOpenCodePresets()

	exportProviders := make(map[string]config.ExportProvider, len(providers))
	for name, p := range providers {
		apiKey, _ := a.getProviderAPIKey(name, p)
		exportProviders[name] = buildExportProvider(p, apiKey)
	}

	exportCfg := config.ExportConfig{
		Version:         "1.0",
		ExportedAt:      time.Now().Format(time.RFC3339),
		Source:          "amagi-codebox",
		Providers:       exportProviders,
		AgentTeams:      agentTeams,
		TerminalPresets: terminalPresets,
		OpenCodePresets: openCodePresets,
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

// ImportConfigFromFile 通过文件选择对话框导入 JSON 配置文件。
// providers / AgentTeams 按现有导入逻辑写入，terminal_presets / opencode_presets 采用快照替换语义。
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
	var exportRaw struct {
		OpenCodePresets *json.RawMessage `json:"opencode_presets"`
	}
	if err := json.Unmarshal(data, &exportRaw); err != nil {
		return "", fmt.Errorf("parse import snapshot metadata: %w", err)
	}

	// 验证基本字段
	if exportCfg.Version == "" || exportCfg.Source == "" {
		return "", fmt.Errorf("invalid config file: missing version or source field")
	}

	// 遍历 providers 并导入
	importCount := 0
	for name, ep := range exportCfg.Providers {
		if err := a.saveImportedProviderAPIKey(name, selectImportedProviderAPIKey(ep)); err != nil {
			a.Log.Warn("app", "保存 provider API key 失败", fmt.Sprintf("provider=%s err=%v", name, err))
		}

		provider := buildProviderFromExportProvider(ep)

		if err := a.Config.SaveProvider(name, provider); err != nil {
			return "", fmt.Errorf("save provider %q: %w", name, err)
		}

		importCount++
	}

	// 导入 AgentTeams 配置（如果存在）
	if exportCfg.AgentTeams.TeammateMode != "" || exportCfg.AgentTeams.Enabled {
		if err := a.Config.SetAgentTeams(exportCfg.AgentTeams); err != nil {
			a.Log.Warn("app", "导入 AgentTeams 配置失败", err.Error())
		}
	}

	// 导入 preset 快照。
	// 为避免 omitempty / 字段缺失导致旧数据残留，nil 视为空快照。
	hasExplicitOpenCodeSnapshot := exportRaw.OpenCodePresets != nil
	if err := a.Config.ReplaceImportedPresetSnapshots(exportCfg.TerminalPresets, exportCfg.OpenCodePresets, hasExplicitOpenCodeSnapshot); err != nil {
		a.Log.Warn("app", "导入 preset 快照失败", err.Error())
	} else {
		a.Log.Info("app", "preset 快照已导入")
	}

	msg := fmt.Sprintf("成功导入 %d 个提供商配置", importCount)
	a.Log.Info("app", msg, filePath)
	return msg, nil
}

// GetProviderExportJSON 返回指定提供商的配置（含 API key）的格式化 JSON 字符串，
// 供前端 JSON 编辑功能使用。
// 支持双格式结构导出，同时保留旧字段兼容。
func (a *App) GetProviderExportJSON(providerName string) (string, error) {
	provider, err := a.Config.GetProvider(providerName)
	if err != nil {
		return "", fmt.Errorf("get provider %q: %w", providerName, err)
	}

	apiKey, _ := a.getProviderAPIKey(providerName, *provider)
	ep := buildExportProvider(*provider, apiKey)

	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal provider JSON: %w", err)
	}
	return string(data), nil
}

// SaveProviderFromJSON 将前端传入的 JSON 字符串解析后保存到指定提供商，
// 若 APIKey 有变更则同步更新密钥存储。
// 支持双格式结构导入，同时兼容旧 JSON 格式。
// API key 仅写入密钥存储（secrets.enc），永远不会明文落盘到 models.json。
func (a *App) SaveProviderFromJSON(providerName string, jsonStr string) error {
	var ep config.ExportProvider
	if err := json.Unmarshal([]byte(jsonStr), &ep); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	if err := a.saveImportedProviderAPIKey(providerName, selectImportedProviderAPIKey(ep)); err != nil {
		return fmt.Errorf("save provider API key for %q: %w", providerName, err)
	}

	provider := buildProviderFromExportProvider(ep)

	if err := a.Config.SaveProvider(providerName, provider); err != nil {
		return fmt.Errorf("save provider %q: %w", providerName, err)
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

// --- 终端预设 API ---

// GetTerminalPresets 获取指定终端类型的所有预设。
func (a *App) GetTerminalPresets(terminalType string) (map[string]config.TerminalPreset, error) {
	return a.Config.GetTerminalPresets(terminalType)
}

// SaveTerminalPreset 保存指定终端类型的预设。
func (a *App) SaveTerminalPreset(terminalType string, presetName string, preset config.TerminalPreset) error {
	return a.Config.SaveTerminalPreset(terminalType, presetName, preset)
}

// DeleteTerminalPreset 删除指定终端类型的预设。
func (a *App) DeleteTerminalPreset(terminalType string, presetName string) error {
	return a.Config.DeleteTerminalPreset(terminalType, presetName)
}

// MigrateProviderPresetsToTerminal 将旧的 provider.presets 迁移到 terminal_presets。
// 返回 (迁移数量, error)。
func (a *App) MigrateProviderPresetsToTerminal() (int, error) {
	count, _, err := a.Config.MigrateProviderPresetsToTerminal()
	return count, err
}

// GetMergedTerminalPresets 返回指定终端类型的合并预设列表（新体系优先，旧体系回退）。
func (a *App) GetMergedTerminalPresets(terminalType string) ([]config.MergedTerminalPreset, error) {
	return a.Config.GetMergedTerminalPresets(terminalType)
}

// ResolveTerminalPreset 按 terminal type + key 解析出 terminal preset 的详情。
// 返回值: (providerName, model, hasOpenCodeCfg, openCodeCfgJSON, found)
func (a *App) ResolveTerminalPreset(terminalType string, key string) (string, string, string, bool) {
	provName, tp, err := a.Config.ResolveTerminalPreset(terminalType, key)
	if err != nil || tp == nil {
		return "", "", "", false
	}
	ocCfg := ""
	if len(tp.OpenCodeCfg) > 0 {
		ocCfg = string(tp.OpenCodeCfg)
	}
	return provName, tp.Model, ocCfg, true
}
