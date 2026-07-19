package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/codexplugin"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/headroom"
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
	"amagi-codebox/internal/usage"
	"amagi-codebox/internal/workspace"

	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type codexLaunchSettings struct {
	Model string
}

const (
	codexModelProviderName     = "amagi-codebox-provider"
	codexOfficialOpenAIAPIHost = "api.openai.com"
	maxClipboardImageBytes     = 20 * 1024 * 1024
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

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

type persistentLoadState struct {
	initialized        bool
	configLoaded       bool
	secretsLoaded      bool
	pathsLoaded        bool
	settingsLoaded     bool
	workspacesLoaded   bool
	proxyRulesLoaded   bool
	proxyHistoryLoaded bool
}

// App 主应用结构体，负责跨服务协调和生命周期管理。
// 通过 Wails 绑定暴露给前端。
type App struct {
	ctx context.Context

	Config         *config.ConfigService
	Secrets        *secrets.SecretsService
	Launcher       *launcher.LauncherService
	Proxy          *proxy.ProxyService
	Headroom       *headroom.HeadroomService
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
	CodexPlugins   *codexplugin.Service
	Workspaces     *workspace.Service
	OpenCodeConfig *opencodeconfig.Service
	EnvCheck       *envcheck.Service
	Usage          *usage.Service

	Capabilities platform.PlatformCapabilities
	CLIResolver  platform.CLIResolver
	FileOpener   platform.FileOpener

	// startupWarnings 记录启动期间的警告信息，供前端拉取后向用户展示。
	startupWarnings   []string
	startupWarningsMu sync.Mutex

	persistenceMu       sync.RWMutex
	persistentLoadState persistentLoadState
}

func NewApp(mobileAssets embed.FS) *App {
	configDir := defaultConfigDir()
	log := logging.NewService(configDir)
	envVarsSvc := envvars.NewEnvVarsService(configDir)
	capabilities := platform.CurrentCapabilities()
	pluginsSvc := plugin.NewService("", log)
	codexPluginsSvc := codexplugin.NewService("", log)
	processRunner := platform.NewProcessRunner()

	// headroom-venv lives under the CodeBox config directory. It is shared by
	// envcheck (install/detect/uninstall) and headroom (proxy launch) so both
	// target the same venv. The bin subdir is platform-specific.
	headroomVenvDir := filepath.Join(configDir, "headroom-venv")
	headroomVenvBinDir := headroomVenvBinSubdir(headroomVenvDir)

	headroomSvc := headroom.NewHeadroomService(processRunner, log)
	headroomSvc.SetVenvBinDir(headroomVenvBinDir)

	envCheckSvc := envcheck.NewServiceWithRunner(processRunner)
	envCheckSvc.SetHeadroomVenvDir(headroomVenvDir)
	// Inject the headroom stopper so CleanHeadroom terminates the proxy child
	// process before removing the venv directory. Required on Windows where a
	// running headroom.exe inside the venv is locked and os.RemoveAll fails.
	// HeadroomService.Stop is a no-op (returns nil) when the proxy is not
	// running, so this is safe to call unconditionally during uninstall.
	envCheckSvc.SetHeadroomStopper(headroomSvc.Stop)

	app := &App{
		Config:         config.NewConfigService(configDir),
		Secrets:        secrets.NewSecretsService(configDir),
		Launcher:       launcher.NewLauncherService(log, envVarsSvc),
		Proxy:          proxy.NewProxyService(),
		Headroom:       headroomSvc,
		Tray:           tray.NewService(),
		Sessions:       session.NewManager(),
		Paths:          paths.NewPathsService(configDir),
		Log:            log,
		Pty:            pty.NewService(log),
		Settings:       settings.NewService(configDir),
		EnvVars:        envVarsSvc,
		Updater:        updater.NewService(Version, log),
		Plugins:        pluginsSvc,
		CodexPlugins:   codexPluginsSvc,
		Workspaces:     workspace.NewService(configDir, pluginsSvc, log),
		OpenCodeConfig: opencodeconfig.NewService(),
		EnvCheck:       envCheckSvc,
		Usage:          usage.NewService(configDir, log),
		Capabilities:   capabilities,
		CLIResolver:    platform.NewCLIResolver(capabilities),
		FileOpener:     platform.NewFileOpener(processRunner),
	}
	// Remote 先以默认端口 8680 初始化；Startup 加载 Settings 后会同步持久化的端口。
	app.Remote = remote.NewServer(8680, app, log, mobileAssets)
	// 方案 P：注入用户主目录，供 List 回填已退出 claudecode 会话的标题（直读历史 jsonl）。
	// 获取失败时记日志但不阻塞启动（标题功能降级，不影响会话启动主流程）。
	if home, homeErr := os.UserHomeDir(); homeErr != nil {
		log.Warn("session", "注入 homeDir 失败：已退出会话标题回填将跳过", "err="+homeErr.Error())
	} else {
		app.Sessions.SetHomeDir(home)
	}
	return app
}

func (a *App) setPersistentLoadState(state persistentLoadState) {
	a.persistenceMu.Lock()
	a.persistentLoadState = state
	a.persistenceMu.Unlock()
}

func (a *App) getPersistentLoadState() persistentLoadState {
	a.persistenceMu.RLock()
	defer a.persistenceMu.RUnlock()
	return a.persistentLoadState
}

func shouldSaveLoadedState(state persistentLoadState, loaded bool) bool {
	return !state.initialized || loaded
}

func (a *App) skipPersistentSaveError(name string) error {
	msg := fmt.Sprintf("跳过保存 %s：启动时加载失败，避免用默认空配置覆盖原文件", name)
	if a.Log != nil {
		a.Log.Warn("app", msg)
	}
	return errors.New(msg)
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
	case "headroom":
		return envcheck.ToolHeadroom, nil
	default:
		return "", fmt.Errorf("unknown CLI tool: %s", tool)
	}
}

// GetHeadroomSavings 查询 headroom 上下文压缩节省统计（压缩次数、节省 token 等）。
// headroom 未安装或查询失败时返回 error，前端据此显示空态；绝不返回伪造零值报告冒充"有数据"。
func (a *App) GetHeadroomSavings() (*headroom.SavingsReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), headroom.SavingsTimeout)
	defer cancel()
	return a.Headroom.GetSavings(ctx)
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
		if err := a.Settings.SetRemoteEnabled(true); err != nil {
			a.Log.Warn("remote", "远程服务已启动，但无法保存启用状态", err.Error())
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
		if err := a.Settings.SetRemoteEnabled(true); err != nil {
			a.Remote.Stop()
			return fmt.Errorf("persist remote enabled state: %w", err)
		}
		a.Log.Info("remote", "远程服务器已启动", fmt.Sprintf("port=%d", a.Remote.GetPort()))
	} else {
		a.Remote.Stop()
		if err := a.Settings.SetRemoteEnabled(false); err != nil {
			return fmt.Errorf("persist remote disabled state: %w", err)
		}
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
	loadState := persistentLoadState{initialized: true}

	// 加载设置并同步 GitHub Token 到 Updater
	remoteEnabled := false
	if err := a.Settings.Load(); err != nil {
		a.Log.Warn("app", "加载设置失败", err.Error())
	} else {
		loadState.settingsLoaded = true
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
		if token := a.Settings.GetGitHubToken(); token != "" {
			a.Updater.SetToken(token)
		}
		remoteEnabled = a.Settings.GetRemoteEnabled()
	}

	if err := a.Config.Load(); err != nil {
		a.Log.Warn("app", "加载配置失败，使用默认值", err.Error())
	} else {
		loadState.configLoaded = true
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
		loadState.secretsLoaded = true
		a.Log.Info("app", "密钥加载成功")
	}
	if err := a.Paths.Load(); err != nil {
		a.Log.Warn("app", "加载路径失败", err.Error())
	} else {
		loadState.pathsLoaded = true
		a.Log.Info("app", "路径加载成功")
	}
	if err := a.EnvVars.Load(); err != nil {
		a.Log.Warn("app", "加载自定义环境变量失败", err.Error())
	} else {
		a.Log.Info("app", "自定义环境变量加载成功")
	}
	if err := a.Proxy.LoadRules(defaultConfigDir()); err != nil {
		a.Log.Warn("app", "加载注入规则失败", err.Error())
	} else {
		loadState.proxyRulesLoaded = true
		a.Log.Info("app", "注入规则加载成功")
	}
	if err := a.Proxy.LoadBackendURLHistory(defaultConfigDir()); err != nil {
		a.Log.Warn("app", "加载后端URL历史记录失败", err.Error())
	} else {
		loadState.proxyHistoryLoaded = true
		a.Log.Info("app", "后端URL历史记录加载成功")
	}
	if err := a.Workspaces.Load(); err != nil {
		a.Log.Warn("app", "加载工作区配置失败", err.Error())
	} else {
		loadState.workspacesLoaded = true
		a.Log.Info("app", "工作区配置加载成功")
	}
	a.setPersistentLoadState(loadState)

	// 使用统计：加载 usage.db + 注入 proxy sink + 异步触发首次同步 + 后台定时同步。
	// 失败不阻塞启动；sync_session_log 路径仍可工作，仅 proxy 实时路径降级。
	if a.Usage != nil {
		if err := a.Usage.Load(); err != nil {
			a.Log.Warn("usage", "使用统计加载失败", err.Error())
		} else {
			a.Log.Info("usage", "使用统计加载成功")
			// 注入应用级 ctx 给 usage.Service（M1：StartBackgroundSync 不再接受 ctx 参数）。
			// Wails v2 仅绑定"方法"，结构体字段（即使导出）不进入 wailsjs 生成路径。
			a.Usage.Ctx = ctx
			// 适配闭包：把 proxy.UsageEvent 转 usage.UsageEvent 并入库。
			// 设计 9.1 / 9.3：proxy 包不 import usage 包；app.go 作为适配器在边界层做类型转换。
			a.Proxy.SetUsageSink(func(pevt proxy.UsageEvent) {
				if a.Usage == nil {
					return
				}
				_, _ = a.Usage.Record(usage.UsageEvent{
					AppType:                  pevt.AppType,
					Source:                   usage.SourceProxy,
					Provider:                 pevt.Provider,
					Model:                    pevt.Model,
					SessionID:                pevt.SessionID,
					Preset:                   pevt.Preset,
					InputTokens:              pevt.InputTokens,
					OutputTokens:             pevt.OutputTokens,
					CacheReadInputTokens:     pevt.CacheReadInputTokens,
					CacheCreationInputTokens: pevt.CacheCreationInputTokens,
					OccurredAt:               pevt.OccurredAt,
					RequestID:                pevt.RequestID,
				})
			})
			go func() {
				if err := a.Usage.SyncAll(); err != nil {
					a.Log.Warn("usage", "首次同步失败", err.Error())
				}
				a.Usage.StartBackgroundSync(5 * time.Minute)
			}()
		}
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

	// 远程 API 仅在用户显式启用后恢复。新安装和没有该设置的旧配置
	// 默认保持 loopback 且不监听，避免无意暴露到局域网。
	if remoteEnabled {
		if err := a.Remote.Start(ctx); err != nil {
			a.Log.Warn("app", "远程服务器启动失败（不影响主功能）", err.Error())
		} else {
			a.Log.Info("app", "远程服务器已启动", fmt.Sprintf("port=%d", a.Remote.GetPort()))
		}
	} else {
		a.Log.Info("app", "远程服务器未启用；可在设置中显式启动")
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
	if a.Usage != nil {
		if err := a.Usage.Close(); err != nil {
			a.Log.Error("usage", "关闭使用统计失败", err.Error())
		}
	}
	if a.Headroom != nil && a.Headroom.IsRunning() {
		if err := a.Headroom.Stop(); err != nil {
			a.Log.Error("app", "关闭 Headroom 失败", err.Error())
		}
	}
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
func (a *App) LaunchSession(providerName, presetName string, mode string, workDir string, useProxy bool, useHeadroom bool, shellPath string) (string, error) {
	a.Log.Info("session", "启动会话请求", fmt.Sprintf("provider=%s preset=%s mode=%s workDir=%s proxy=%v headroom=%v shell=%s", providerName, presetName, mode, workDir, useProxy, useHeadroom, shellPath))

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
			Name:        tp.Name,
			Model:       tp.Model,
			ModelHaiku:  tp.ModelHaiku,
			ModelSonnet: tp.ModelSonnet,
			ModelOpus:   tp.ModelOpus,
			Parameters:  tp.Parameters,
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

	// realBackend 是真实 upstream 的 base URL（如 api.anthropic.com）。
	// headroom 与注入代理都需要它：headroom 据此转发，注入代理在非串联模式下据此转发。
	realBackend := provider.EffectiveBaseURL("anthropic")
	switch {
	case useHeadroom && useProxy:
		// 串联叠加: CLI → 注入代理(:5280) → headroom 压缩(:8787) → 真实 API
		// 注入代理的 backendURL 指向 headroom，headroom 再转发到真实 API。
		// 注入代理的 proxyHandler 已透传全部请求头(含 Authorization / x-api-key)，
		// 因此 API key 链路完整，headroom 能拿到认证信息继续转发。
		if !a.Headroom.IsRunning() {
			if err := a.Headroom.Start(realBackend); err != nil {
				a.Log.Error("headroom", "上下文压缩启动失败", err.Error())
				return "", fmt.Errorf("start headroom: %w", err)
			}
			a.Log.Info("headroom", "上下文压缩已启用并生效",
				fmt.Sprintf("CLI → 注入代理:5280 → headroom:127.0.0.1:8787 → %s", realBackend))
		}
		if !a.Proxy.IsRunning() {
			codeboxPort := a.Proxy.GetPort()
			if codeboxPort == 0 {
				codeboxPort = 5280
			}
			headroomUpstream := fmt.Sprintf("http://127.0.0.1:%d", headroom.DefaultPort)
			if err := a.Proxy.Start(codeboxPort, headroomUpstream); err != nil {
				return "", fmt.Errorf("start proxy: %w", err)
			}
		}
		a.Launcher.SetProxyPort(a.Proxy.GetPort())
	case useHeadroom && !useProxy:
		// 只开 headroom: CLI → headroom 压缩(:8787) → 真实 API
		if !a.Headroom.IsRunning() {
			if err := a.Headroom.Start(realBackend); err != nil {
				a.Log.Error("headroom", "上下文压缩启动失败", err.Error())
				return "", fmt.Errorf("start headroom: %w", err)
			}
			a.Log.Info("headroom", "上下文压缩已启用并生效",
				fmt.Sprintf("CLI → headroom:127.0.0.1:8787 → %s", realBackend))
		}
		a.Launcher.SetProxyPort(headroom.DefaultPort)
	case !useHeadroom && useProxy:
		// 只开注入代理(现有逻辑): CLI → 注入代理(:5280) → 真实 API
		if a.Headroom.IsRunning() {
			_ = a.Headroom.Stop()
		}
		if !a.Proxy.IsRunning() {
			port := a.Proxy.GetPort()
			if port == 0 {
				port = 5280
			}
			if err := a.Proxy.Start(port, realBackend); err != nil {
				return "", fmt.Errorf("start proxy: %w", err)
			}
		}
		a.Launcher.SetProxyPort(a.Proxy.GetPort())
	default:
		// 都关: CLI → 真实 API
		if a.Headroom.IsRunning() {
			_ = a.Headroom.Stop()
		}
		a.Launcher.SetProxyPort(0)
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypeClaudeCode, providerName, presetName, model, launchMode, workDir, useProxy)
	a.Log.Info("session", "会话已创建", fmt.Sprintf("id=%s model=%s mode=%s", sess.ID, model, launchMode))

	// === usage：仅 useProxy 时注入 proxy 上下文，让实时钩子关联到本会话（设计 9.4）===
	if useProxy && a.Usage != nil {
		a.Proxy.SetCurrentSession(sess.ID, providerName, presetName, string(session.AppTypeClaudeCode))
	}

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

		// 方案 R：为 embedded claudecode 会话注入 --session-id <uuid>，
		// 让 Claude Code 用 amagi-codebox 指定的 uuid 写 jsonl（与同 workDir 其他会话区分），
		// tracker 优先读"自己锁定的 jsonl"消除串扰，仅当锁定 jsonl 停滞才检测同目录最新跟随。
		claudeSID := uuid.NewString()
		a.Sessions.SetClaudeSessionID(sess.ID, claudeSID)
		a.Log.Info("session", "注入 Claude session-id", fmt.Sprintf("id=%s sid=%s", sess.ID, claudeSID))

		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeClaudeCode, string(launchMode), shellPath, workDir, env, []string{"--session-id", claudeSID})
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

		// 方案 R：tracker 优先读"自己锁定的 jsonl"（上面注入的 --session-id），
		// 锁定 jsonl 停滞（用户 /resume 切走）超 60s 才检测同目录最新跟随。
		// 仅 claudecode 启动（opencode/codex 不写 ~/.claude/projects jsonl）。
		a.startTitleTracker(sess.ID, workDir)

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

	// 方案 R 降级（external 模式）：Launcher.Launch 不支持注入 --session-id，
	// ClaudeSessionID 留空，tracker 走方案 P（FindLatestActiveJSONL 取最新 mtime）。
	// 副作用：external 模式下同 workDir 多会话仍会指向同一最新 jsonl（边缘场景，主上已知）。
	a.startTitleTracker(sess.ID, workDir)

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

// startTitleTracker 启动方案 P 的标题跟踪 goroutine。
//
// 仅用于 claudecode 会话（opencode/codex 不写 ~/.claude/projects jsonl）。
// tracker 退出条件双重保险：
//  1. a.ctx.Done()（app 关闭）；
//  2. mgr.GetStatus != Running（会话已被 MarkStopped/MarkExited/MarkFailed）。
//
// 不持有外部 cancel 句柄：依赖条件 2 在最多一个轮询周期内自动退出，避免泄漏。
// homeDir 由 os.UserHomeDir 获取；获取失败时记录日志但不启动 tracker（功能降级，非崩溃）。
func (a *App) startTitleTracker(amagiSessionID, workDir string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		a.Log.Warn("session", "标题跟踪跳过：无法获取用户主目录", "err="+err.Error())
		return
	}
	go session.TrackTitle(a.ctx, a.Sessions, amagiSessionID, homeDir, workDir, a.Log)
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

// SetPluginSubItemEnabled 设置插件子项启用/禁用状态
// pluginId: 插件 ID
// subItemType: 子项类型（skill/hook/command/agent/mcp）
// subItemId: 子项名称
// enabled: 是否启用
func (a *App) SetPluginSubItemEnabled(pluginId string, subItemType string, subItemId string, enabled bool) error {
	a.Log.Info("plugin", "设置插件子项状态", fmt.Sprintf("plugin=%s type=%s id=%s enabled=%v", pluginId, subItemType, subItemId, enabled))

	// Claude 与 Codex 插件 ID 都使用 `name@marketplace` 格式（见 plugin/service.go 与
	// codexplugin/helpers.go 的 splitPluginID），不能用 strings.Contains(@) 区分。
	// 改为查询 Claude 已安装插件注册表：命中则派 Claude service，否则派 Codex service。
	if a.isClaudePlugin(pluginId) {
		return a.Plugins.SetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled)
	}
	return a.CodexPlugins.SetPluginSubItemEnabled(pluginId, subItemType, subItemId, enabled)
}

// isClaudePlugin 判断 pluginId 是否为 Claude 引擎下已安装的插件。
// 以 Claude 注册表（~/.claude/plugins/installed_plugins.json，经 plugin.Service 抽象）
// 为单一真相源，避免基于字符串启发式误判。
func (a *App) isClaudePlugin(pluginId string) bool {
	if a.Plugins == nil {
		return false
	}
	plugins, err := a.Plugins.GetInstalledPlugins()
	if err != nil {
		// 读注册表失败时保守按非 Claude 处理（落到 Codex 分派），并告警暴露。
		// 注意：Codex SetPluginSubItemEnabled 当前只记日志返回 nil，若实际为 Claude
		// 插件而注册表读取失败，开关会静默不生效，故必须记日志可观测。
		if a.Log != nil {
			a.Log.Warn("plugin", "读取 Claude 已安装插件列表失败，按 Codex 引擎分派", fmt.Sprintf("pluginId=%s err=%v", pluginId, err))
		}
		return false
	}
	for i := range plugins {
		if plugins[i].ID == pluginId {
			return true
		}
	}
	return false
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
		// 当 presetName 为空时，不注入任何 preset 配置，让 OpenCode 读取全局配置
		if presetName == "" {
			a.Log.Info("session", "OpenCode 使用全局配置（preset 为空）", fmt.Sprintf("provider=%s", providerName))
		} else {
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
		} // else 结束：presetName != "" 的情况
	} // 轨道2 结束

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
func (a *App) QuickLaunch(providerName, presetName string, useProxy bool, useHeadroom bool) error {
	_, err := a.LaunchSession(providerName, presetName, "terminal", "", useProxy, useHeadroom, "")
	return err
}

// SaveAllConfig 保存配置和密钥到磁盘。
func (a *App) SaveAllConfig() error {
	state := a.getPersistentLoadState()
	var saveErrs []error

	if shouldSaveLoadedState(state, state.configLoaded) {
		if err := a.Config.Save(); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("save config: %w", err))
		}
	} else {
		saveErrs = append(saveErrs, a.skipPersistentSaveError("models.json"))
	}

	if shouldSaveLoadedState(state, state.secretsLoaded) {
		if err := a.Secrets.Save(); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("save secrets: %w", err))
		}
	} else {
		saveErrs = append(saveErrs, a.skipPersistentSaveError("secrets.enc"))
	}

	if shouldSaveLoadedState(state, state.pathsLoaded) {
		if err := a.Paths.Save(); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("save paths: %w", err))
		}
	} else {
		saveErrs = append(saveErrs, a.skipPersistentSaveError("paths.json"))
	}

	if shouldSaveLoadedState(state, state.settingsLoaded) {
		if err := a.Settings.Save(); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("save settings: %w", err))
		}
	} else {
		saveErrs = append(saveErrs, a.skipPersistentSaveError("settings.json"))
	}

	if shouldSaveLoadedState(state, state.workspacesLoaded) {
		if err := a.Workspaces.Save(); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("save workspaces: %w", err))
		}
	} else {
		saveErrs = append(saveErrs, a.skipPersistentSaveError("workspaces.json/global-enabled.json"))
	}

	if shouldSaveLoadedState(state, state.proxyRulesLoaded) {
		if err := a.Proxy.SaveRules(defaultConfigDir()); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("save injection rules: %w", err))
		}
	} else {
		saveErrs = append(saveErrs, a.skipPersistentSaveError("injection-rules.json"))
	}

	if shouldSaveLoadedState(state, state.proxyHistoryLoaded) {
		if err := a.Proxy.SaveBackendURLHistory(defaultConfigDir()); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("save backend URL history: %w", err))
		}
	} else {
		saveErrs = append(saveErrs, a.skipPersistentSaveError("proxy-backend-url-history.json"))
	}

	return errors.Join(saveErrs...)
}

// GetAppInfo 返回应用基本信息。
//
// 版本来源优先级：
//  1. main.Version 由构建脚本 ldflags 注入（git tag）；为空或 dev 表示未注入。
//  2. 回退到 wails.json 的 info.productVersion（确保至少显示 1.2.57，不依赖 git tag）。
//  3. 最终回退 "dev"。
//
// GoVersion 优先用 runtime.Version()（权威编译器版本），仅当 ldflags 注入了非 unknown 值时才用注入值。
func (a *App) GetAppInfo() map[string]any {
	version := resolveAppVersion()
	goVer := GoVersion
	if goVer == "" || goVer == "unknown" {
		goVer = runtime.Version()
	}
	return map[string]any{
		"productName":  "Amagi CodeBox",
		"version":      version,
		"buildTime":    BuildTime,
		"gitCommit":    GitCommit,
		"goVersion":    goVer,
		"configDir":    defaultConfigDir(),
		"runningCount": a.Sessions.RunningCount(),
		"proxyStatus":  a.Proxy.GetStatus(),
	}
}

// resolveAppVersion 解析最终展示版本号。
// ldflags 注入值优先；为 dev/空时回退到 wails.json productVersion；最终回退 dev。
func resolveAppVersion() string {
	raw := strings.TrimSpace(Version)
	v := strings.TrimPrefix(raw, "v")
	if v != "" && v != "dev" {
		return v
	}
	if pv := readWailsProductVersion(); pv != "" {
		return strings.TrimPrefix(pv, "v")
	}
	return "dev"
}

// readWailsProductVersion 从 wails.json 的 info.productVersion 读取版本号。
// 依次在可执行文件目录、当前工作目录、源码根目录（开发模式）查找，找不到返回空。
func readWailsProductVersion() string {
	candidates := make([]string, 0, 3)
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "wails.json"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "wails.json"))
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg struct {
			Info struct {
				ProductVersion string `json:"productVersion"`
			} `json:"info"`
		}
		if err := json.Unmarshal(data, &cfg); err == nil {
			if pv := strings.TrimSpace(cfg.Info.ProductVersion); pv != "" {
				return pv
			}
		}
	}
	return ""
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

// SaveClipboardImage 将 base64 编码的 PNG 保存为私有临时文件，返回文件绝对路径。
// 用于处理 Windows 截图工具截图后粘贴到终端的场景。
func (a *App) SaveClipboardImage(base64Data string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	filePath, err := writeClipboardImage(raw)
	if err != nil {
		return "", err
	}
	a.Log.Info("app", "剪贴板图片已保存", filePath)
	return filePath, nil
}

func writeClipboardImage(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("clipboard image is empty")
	}
	if len(raw) > maxClipboardImageBytes {
		return "", fmt.Errorf("clipboard image exceeds %d MiB limit", maxClipboardImageBytes/(1024*1024))
	}
	if len(raw) < len(pngSignature) || !bytes.Equal(raw[:len(pngSignature)], pngSignature) {
		return "", errors.New("clipboard image must be a PNG")
	}

	file, err := os.CreateTemp("", "amagi-codebox-clipboard-*.png")
	if err != nil {
		return "", fmt.Errorf("create temp image: %w", err)
	}
	filePath := file.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = file.Close()
			_ = os.Remove(filePath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("set temp image permissions: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		return "", fmt.Errorf("write temp image: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close temp image: %w", err)
	}
	succeeded = true
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
// 通过系统对话框让用户选择保存位置；导出文件仅对当前用户可读。
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

// UpdateProvider 统一编辑提供商入口：支持改名 + 属性更新 + 密钥更新。
//   - oldName == newName：仅更新属性，复用 SaveProviderFromJSON 路径（零副作用）。
//   - oldName != newName（改名）：先 config 迁移（RenameProvider 原子写盘），
//     再覆盖新属性（SaveProvider），最后 secrets 密钥迁移并显式落盘。
//
// providerJSON 为完整的 ExportProvider JSON（含可编辑属性与可选 API Key）。
// API Key 为空表示"保持不变"——后端会迁移旧密钥到新 name。
//
// 失败降级（设计 4.6）：config 已原子写成功后 secrets.Save 失败时，不回滚 config，
// 返回带友好提示的 error，用户重新填写密钥即可。
func (a *App) UpdateProvider(oldName, newName string, providerJSON string) error {
	// —— 1. 校验（持锁前，不依赖 config 状态）——
	if oldName == "" {
		return errors.New("provider name is required")
	}
	trimmedNew := strings.TrimSpace(newName)
	if trimmedNew == "" {
		return errors.New("provider name is required")
	}
	if strings.Contains(trimmedNew, "/") {
		return fmt.Errorf("invalid provider name %q: must not contain '/'", trimmedNew)
	}
	newName = trimmedNew

	// —— 2. 解析 ExportProvider JSON ——
	var ep config.ExportProvider
	if err := json.Unmarshal([]byte(providerJSON), &ep); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	// —— 3. 分流：未改名走现成路径 ——
	if oldName == newName {
		return a.SaveProviderFromJSON(newName, providerJSON)
	}

	// —— 改名分支 ——
	// 3a. 预读旧密钥（config 改动前读取，确保可读）。
	// 只读 secrets cache（统一 key + legacy 回退），不查环境变量——环境变量不可迁移。
	oldKey := ""
	if oldProv, err := a.Config.GetProvider(oldName); err == nil && oldProv != nil {
		oldKey = a.readStoredProviderAPIKey(oldName, *oldProv)
	}

	// 3b. config 迁移（原子写盘）
	if err := a.Config.RenameProvider(oldName, newName); err != nil {
		return err
	}

	// 3c. 覆盖新属性（此时 newName 已存在，upsert）。
	// 复用 buildProviderFromExportProvider，保持 JSON 结构与现有路径一致。
	newProvider := buildProviderFromExportProvider(ep)
	if err := a.Config.SaveProvider(newName, newProvider); err != nil {
		// config 已改名（步骤 3b 成功），属性未更新——不回滚，前端可重试。
		a.Log.Warn("app", "provider 改名后属性覆盖失败，可重试", oldName+" -> "+newName+": "+err.Error())
		return fmt.Errorf("rename succeeded but save new properties failed: %w", err)
	}

	// 3d. secrets 迁移（三分支）
	newKey := selectImportedProviderAPIKey(ep)
	secretsChanged := false
	switch {
	case newKey != "":
		// 用户填了新密钥：写入 newName 的统一 key。
		if err := a.Secrets.SetAPIKey(newName, newKey); err != nil {
			return fmt.Errorf("set new API key for %q: %w", newName, err)
		}
		secretsChanged = true
		// 删除 oldName 的所有密钥条目（统一 key + legacy）。
		a.deleteProviderAPIKeys(oldName)
	case oldKey != "":
		// 用户未填新密钥（保持不变）：迁移旧密钥到 newName。
		if err := a.Secrets.SetAPIKey(newName, oldKey); err != nil {
			return fmt.Errorf("migrate API key to %q: %w", newName, err)
		}
		a.deleteProviderAPIKeys(oldName)
		secretsChanged = true
	}
	// newKey 空 且 oldKey 空：无密钥可迁，跳过。

	// 3e. secrets 落盘（仅在发生变更时）
	if secretsChanged {
		if err := a.Secrets.Save(); err != nil {
			// 设计 4.6：config 已一致，secrets 内存 cache 已改但 Save 失败。
			// 不回滚 config（反向操作风险更高）。降级为提示用户重填密钥。
			a.Log.Warn("app", "provider 改名后密钥落盘失败，请重新填写密钥",
				oldName+" -> "+newName+": "+err.Error())
			return fmt.Errorf("config renamed but secrets save failed: %w; please re-enter API key for %s", err, newName)
		}
	}

	a.Log.Info("app", "provider 已改名", oldName+" -> "+newName)
	return nil
}

// readStoredProviderAPIKey 读取 provider 在 secrets cache 中的统一密钥，
// 回退到 legacy providerName:format 命名。不查询环境变量（环境变量不可迁移）。
func (a *App) readStoredProviderAPIKey(providerName string, provider config.Provider) string {
	if key, _ := a.Secrets.GetAPIKey(providerName); strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	for _, format := range legacyProviderAPIKeyCandidates(provider) {
		if key, err := a.Secrets.GetAPIKey(providerName + ":" + format); err == nil {
			if trimmed := strings.TrimSpace(key); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// deleteProviderAPIKeys 删除指定 provider 在 secrets cache 中的所有密钥条目，
// 包含统一 key（providerName）与 legacy 命名（providerName:anthropic / providerName:openai）。
// 用于改名后清理旧 name 的残留，避免 secrets.enc 出现指向已不存在 provider 的孤儿条目。
func (a *App) deleteProviderAPIKeys(providerName string) {
	_ = a.Secrets.DeleteAPIKey(providerName)
	_ = a.Secrets.DeleteAPIKey(providerName + ":anthropic")
	_ = a.Secrets.DeleteAPIKey(providerName + ":openai")
}

// DeleteProvider 删除指定服务商配置。
func (a *App) DeleteProvider(name string) error {
	if name == "" {
		return errors.New("provider name is required")
	}
	return a.Config.DeleteProvider(name)
}

// atomicWriteFile atomically writes a user-private export. It uses an
// exclusive temporary file instead of a predictable .tmp path and preserves
// 0600 permissions after replacement because exports can contain API keys.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-")
	if err != nil {
		return fmt.Errorf("create temp export: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temp export permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp export: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp export: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set export permissions: %w", err)
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

// headroomVenvBinSubdir returns the platform-specific bin directory inside
// the CodeBox-managed headroom venv. Used at wiring time to inject the same
// directory into both envcheck.Service and headroom.HeadroomService.
func headroomVenvBinSubdir(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts")
	}
	return filepath.Join(venvDir, "bin")
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

// GetEnvVarsGlobalSyncStatus 返回环境变量全局同步状态
func (a *App) GetEnvVarsGlobalSyncStatus() envvars.GlobalSyncStatus {
	return a.EnvVars.GetGlobalSyncStatus()
}

// SetEnvVarsGlobalSyncEnabled 开启或关闭环境变量全局同步
func (a *App) SetEnvVarsGlobalSyncEnabled(enabled bool) (envvars.GlobalSyncStatus, error) {
	return a.EnvVars.SetGlobalSyncEnabled(enabled)
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

// --- 已保存工作目录 API ---

// GetSavedWorkDirs 获取已保存的工作目录列表。
func (a *App) GetSavedWorkDirs() ([]settings.WorkDirEntry, error) {
	return a.Settings.GetSavedWorkDirs(), nil
}

// AddSavedWorkDir 添加工作目录（去重，按 path 去重；label 空则用路径末段），返回最新列表。
func (a *App) AddSavedWorkDir(path string, label string) ([]settings.WorkDirEntry, error) {
	if err := a.Settings.AddSavedWorkDir(path, label); err != nil {
		return nil, err
	}
	return a.Settings.GetSavedWorkDirs(), nil
}

// RemoveSavedWorkDir 移除工作目录，返回最新列表。
func (a *App) RemoveSavedWorkDir(path string) ([]settings.WorkDirEntry, error) {
	if err := a.Settings.RemoveSavedWorkDir(path); err != nil {
		return nil, err
	}
	return a.Settings.GetSavedWorkDirs(), nil
}
