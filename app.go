package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
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
	"sync/atomic"
	"time"

	"amagi-codebox/internal/codexplugin"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/ompplugin"
	"amagi-codebox/internal/opencodeconfig"
	"amagi-codebox/internal/opencodeplugin"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/proxy"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
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

// externalLauncherPort is the narrow Launcher-owned lifecycle used by external
// Claude/Codex sessions. Production uses LauncherService; tests inject a
// deterministic process lifecycle without changing the exported App surface.
type externalLauncherPort interface {
	LaunchGuarded(string, config.Provider, string, string, config.AgentTeamsConfig, session.LaunchMode, string, func() error) (*launcher.LaunchResult, error)
	LaunchCodexGuarded(string, string, session.LaunchMode, string, map[string]string, func() error) (*launcher.LaunchResult, error)
	CaptureProcessIdentity(string) (string, error)
	RecoverProcess(string, int, string) (bool, error)
	IsRunning(string) bool
	Stop(string) error
	StopAll()
}

// externalCleanupClaim is the exact App-owned handoff after an OS process
// starts but its durable PID upgrade or Headroom lease promotion cannot finish.
// It retains a pre-start reservation or upgraded record, the startup admission
// (when the coordinator is still open), and a retriable Launcher owner until a
// true terminal observation. It is not a Control permit and grants no writes.
type externalCleanupClaim struct {
	sessionID        string
	admission        *remote.SharedLaunchAdmission
	lifecycle        externalLauncherPort
	record           externalCleanupRecord
	reservation      externalCleanupReservation
	recordDurable    bool
	recoveryReason   remote.ExternalCleanupRecoveryReason
	terminalObserved bool
	done             chan struct{}
	reaperOnce       sync.Once
	completionMu     sync.Mutex
}

type externalOwnershipAttempt struct {
	sessionID          string
	kind               remote.SharedServiceKind
	startGeneration    uint64
	startCommitted     bool
	rawStartAuthorized bool
	processStarted     bool
	durableReservation bool
}

// piLaunchSettings 是 Pi 会话的启动参数。
// Provider 对应 pi 的 --provider 参数（anthropic/openai 等 Pi 内置 provider 名）；
// piLaunchSettings 是 Pi 会话的启动参数。
type piLaunchSettings struct {
	Provider string // --provider arg（amagi-<name> 或内置 anthropic/openai）
	Model    string // --model arg
	Thinking string // --thinking arg（off/minimal/low/medium/high/xhigh/max），来自预设 ReasoningEffort
}

// ompLaunchSettings 是 Oh My Pi (omp) 会话的启动参数（复刻 piLaunchSettings）。
// omp 与 pi 同源，CLI 参数契约一致：--provider/--model/--thinking。
type ompLaunchSettings struct {
	Provider string // --provider arg（amagi-<name> 或内置 anthropic/openai）
	Model    string // --model arg
	Thinking string // --thinking arg（off/minimal/low/medium/high/xhigh/max/auto），来自预设 ReasoningEffort
}

const (
	codexModelProviderName     = "amagi-codebox-provider"
	codexOfficialOpenAIAPIHost = "api.openai.com"
	maxClipboardImageBytes     = 20 * 1024 * 1024
	// defaultExternalShutdownCleanupBudget is one wall-clock budget shared by
	// handoff observation and StopAll. It bounds only external ownership cleanup;
	// every unrecovered item is returned as a typed report.
	defaultExternalShutdownCleanupBudget = 2 * time.Second
	// CodexGlobalHeadroomDefaultPort is the dedicated port for the independent
	// codex-global headroom instance (8788). It must differ from the claude
	// session headroom DefaultPort (8787) so the two proxies never collide.
	CodexGlobalHeadroomDefaultPort = 8788
	// codexGlobalHeadroomDefaultTarget is the fallback OpenAI-compatible
	// upstream used when the caller does not specify a target. headroom forwards
	// compressed codex traffic here via OPENAI_TARGET_API_URL.
	codexGlobalHeadroomDefaultTarget = "https://api.openai.com/v1"
	// codexGlobalHeadroomMarkerStart / End delimit the amagi-managed
	// openai_base_url block in ~/.codex/config.toml. They are intentionally
	// distinct from the amagi-codebox-inject markers so the provider-section
	// cleanup (removeCodexManagedProviderSection / cleanupCodexManagedProviderConfig)
	// never touches this block.
	codexGlobalHeadroomMarkerStart = "# === amagi-headroom-global-start ==="
	codexGlobalHeadroomMarkerEnd   = "# === amagi-headroom-global-end ==="
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

	Config   *config.ConfigService
	Secrets  *secrets.SecretsService
	Launcher *launcher.LauncherService
	Proxy    *proxy.ProxyService
	Headroom *headroom.HeadroomService
	// CodexHeadroom is a second, independent headroom instance that compresses
	// Codex desktop traffic globally (port 8788, OpenAI target). It is fully
	// separate from Headroom (claude-session, 8787, Anthropic target): each has
	// its own port, lifecycle and target kind, so toggling one never touches the
	// other. Only one Codex-global instance exists app-wide (it is not per
	// session); Start/Stop are idempotent.
	CodexHeadroom   *headroom.HeadroomService
	Tray            *tray.Service
	Sessions        *session.Manager
	Paths           *paths.PathsService
	Log             *logging.Service
	Pty             *pty.Service
	Settings        *settings.Service
	Remote          *remote.Server
	EnvVars         *envvars.EnvVarsService
	Updater         *updater.Service
	Plugins         *plugin.Service
	CodexPlugins    *codexplugin.Service
	OpenCodePlugins *opencodeplugin.Service
	PiPlugins       *piplugin.Service
	OmpPlugins      *ompplugin.Service
	Workspaces      *workspace.Service
	OpenCodeConfig  *opencodeconfig.Service
	EnvCheck        *envcheck.Service
	Usage           *usage.Service

	// Control runtime (M3-A2): the gate authority for all session write side
	// effects. Raw ports (Pty/Proxy/Headroom) sit BEHIND it and are never
	// Wails-bound (design §4.1, §6.3 C-01).
	control     *remote.ControlRuntime
	sharedCoord *remote.SharedServiceCoordinator

	// sharedLeases tracks per-session shared-service leases acquired at launch so
	// they can be released on run terminal/remove (M-006). Guarded by
	// sharedLeaseMu.
	sharedLeaseMu sync.Mutex
	sharedLeases  map[string][]*remote.SharedDependencyLease

	// externalLauncher is a narrow test seam; nil selects the production
	// LauncherService. externalRunPollInterval is zero in production (1s).
	externalLauncher        externalLauncherPort
	externalRunPollInterval time.Duration

	// externalCleanups owns post-start processes whose durable identity upgrade
	// or Headroom lease promotion failed and whose first compensating Stop could
	// not prove terminal. Each exact claim remains until explicit Stop/reaper
	// terminal; pre-start reservations survive App exit and Shutdown reports every
	// bounded abandonment rather than silently forgetting ownership.
	externalOwnershipMu     sync.Mutex
	externalShutdown        atomic.Bool
	externalStartGeneration atomic.Uint64

	externalCleanupMu                     sync.Mutex
	externalCleanups                      map[string]*externalCleanupClaim
	externalDurableRuns                   map[string]externalCleanupRecord
	externalCleanupStore                  externalCleanupStore
	externalCleanupRecoveryBlocked        bool
	externalOwnershipAttempt              *externalOwnershipAttempt
	externalShutdownCleanupBudget         time.Duration
	externalCleanupEvents                 []remote.ExternalCleanupAbandonmentEvent
	externalCleanupEventSink              func(remote.ExternalCleanupAbandonmentEvent)
	externalCleanupRecoveryAuditEvents    []remote.ExternalCleanupRecoveryAuditEvent
	externalCleanupRecoveryAuditEventSink func(remote.ExternalCleanupRecoveryAuditEvent)
	lastExternalCleanupShutdownReport     remote.ExternalCleanupShutdownReport

	// sessionRemove is the narrow Manager.Remove seam used by clear-stopped.
	// Production leaves it nil and calls Sessions.Remove; tests inject exact
	// per-ID failures to prove partial-result propagation without racing internals.
	sessionRemove func(string) error

	Capabilities platform.PlatformCapabilities
	CLIResolver  platform.CLIResolver
	FileOpener   platform.FileOpener

	// startupWarnings 记录启动期间的警告信息，供前端拉取后向用户展示。
	startupWarnings   []string
	startupWarningsMu sync.Mutex

	persistenceMu       sync.RWMutex
	persistentLoadState persistentLoadState

	// configDir 是所有服务共享的配置根（~/.amagi-codebox）。由 NewApp 设置；
	// 迁移 gate 与 Remote 安全面共用它，使 settings.json 与设备存储/备份位于
	// 同一目录（design §C, ratification C-2）。
	configDir string

	// securityLoadAttempts counts LoadSecurityState invocations owned by this
	// App (migration gate step a + normal post-gate path). It is the exactly-once
	// seam for tests: 0 for skip paths (Future/Manual/Detect-error), 1 otherwise.
	securityLoadAttempts int

	// remoteLifecycleMu serializes the App-layer remote lifecycle orchestration
	// (stopped-check → SetHost/SetPort → Settings Save → restart) between
	// SetRemoteEndpoint / SetRemoteHost / SetRemotePort and ToggleRemoteServer
	// (R2-Minor-02). Without it, two concurrent Wails calls could interleave
	// Stop/SetHost/SetPort/Start and leave the live server on a mixed host:port
	// tuple, or race the stopped-check against a toggle. Inner service locks
	// (Settings.saveMu / Server.mu) are always acquired AFTER this one, so lock
	// ordering stays remoteLifecycleMu → inner.
	remoteLifecycleMu sync.Mutex

	// codexGlobalMu serializes the codex-global headroom orchestration between
	// the async startup restore goroutine (restoreCodexGlobalHeadroomOnStartup)
	// and the synchronous UI toggle (SetCodexGlobalHeadroom). Without it, a user
	// disabling the feature in the narrow startup window could race the restore
	// goroutine and end with Enabled=false but the proxy running and config.toml
	// still carrying the marker block. The lock is taken for the full
	// Start/Stop + config-sync + persistence sequence; inner service locks
	// (HeadroomService.mu / settings.Service.mu) are always acquired AFTER this
	// one, so lock ordering stays codexGlobalMu -> inner.
	codexGlobalMu sync.Mutex
}

func NewApp(mobileAssets embed.FS) *App {
	configDir := defaultConfigDir()
	log := logging.NewService(configDir)
	envVarsSvc := envvars.NewEnvVarsService(configDir)
	capabilities := platform.CurrentCapabilities()
	pluginsSvc := plugin.NewService("", log)
	codexPluginsSvc := codexplugin.NewService("", log)
	openCodePluginsSvc := opencodeplugin.NewService("", "", log)
	// Pi 直接共用标准用户配置树 ~/.pi/agent，不再维护
	// ~/.amagi-codebox/pi-runtime 隔离副本。插件面板与 CodeBox 启动的
	// Pi 因此读写同一份 settings/auth/packages/models 状态。
	piPluginsSvc := piplugin.NewService(defaultPiAgentDir(), log)
	// omp 插件 CLI 自带目录语义（~/.omp/plugins），无需 agentDir 注入。
	ompPluginsSvc := ompplugin.NewService(log)
	processRunner := platform.NewProcessRunner()

	// headroom-venv lives under the CodeBox config directory. It is shared by
	// envcheck (install/detect/uninstall) and headroom (proxy launch) so both
	// target the same venv. The bin subdir is platform-specific.
	headroomVenvDir := filepath.Join(configDir, "headroom-venv")
	headroomVenvBinDir := headroomVenvBinSubdir(headroomVenvDir)

	headroomSvc := headroom.NewHeadroomService(processRunner, log)
	headroomSvc.SetVenvBinDir(headroomVenvBinDir)

	// Second, independent headroom instance for the Codex desktop global
	// compression path. It reuses the SAME venv (headroom-venv) as the claude
	// headroom but listens on a dedicated port (CodexGlobalHeadroomDefaultPort,
	// 8788) and targets an OpenAI-compatible upstream. Port is set here once;
	// the App-level SetCodexGlobalHeadroom toggle only starts/stops it.
	codexHeadroomSvc := headroom.NewHeadroomService(processRunner, log)
	codexHeadroomSvc.SetVenvBinDir(headroomVenvBinDir)
	if err := codexHeadroomSvc.SetPort(CodexGlobalHeadroomDefaultPort); err != nil {
		log.Warn("headroom", "设置 codex 全局 headroom 端口失败", err.Error())
	}

	envCheckSvc := envcheck.NewServiceWithRunner(processRunner)
	envCheckSvc.SetHeadroomVenvDir(headroomVenvDir)

	app := &App{
		configDir:       configDir,
		Config:          config.NewConfigService(configDir),
		Secrets:         secrets.NewSecretsService(configDir),
		Launcher:        launcher.NewLauncherService(log, envVarsSvc),
		Proxy:           proxy.NewProxyService(),
		Headroom:        headroomSvc,
		CodexHeadroom:   codexHeadroomSvc,
		Tray:            tray.NewService(),
		Sessions:        session.NewManager(),
		Paths:           paths.NewPathsService(configDir),
		Log:             log,
		Pty:             pty.NewService(log),
		Settings:        settings.NewService(configDir),
		EnvVars:         envVarsSvc,
		Updater:         updater.NewService(Version, log),
		Plugins:         pluginsSvc,
		CodexPlugins:    codexPluginsSvc,
		OpenCodePlugins: openCodePluginsSvc,
		PiPlugins:       piPluginsSvc,
		OmpPlugins:      ompPluginsSvc,
		Workspaces:      workspace.NewService(configDir, pluginsSvc, log),
		OpenCodeConfig:  opencodeconfig.NewService(),
		EnvCheck:        envCheckSvc,
		Usage:           usage.NewService(configDir, log),
		Capabilities:    capabilities,
		CLIResolver:     platform.NewCLIResolver(capabilities),
		FileOpener:      platform.NewFileOpener(processRunner),
	}
	// Remote 先以默认端口 8680 初始化；Startup 加载 Settings 后会同步持久化的端口。
	// M1-A：接线设备安全面（design §14.1）。HostSummary provider 为私有闭包，
	// 遍历 contract.KnownCLITypes 调用真实 CLIResolver.Resolve(AppType)。
	app.Remote = remote.NewServerWithSecurity(8680, app, log, mobileAssets,
		remote.NewProductionSecurityOptions(configDir, app.buildRemoteHostSummary))
	// 方案 P：注入用户主目录，供 List 回填已退出 claudecode 会话的标题（直读历史 jsonl）。
	// 获取失败时记日志但不阻塞启动（标题功能降级，不影响会话启动主流程）。
	if home, homeErr := os.UserHomeDir(); homeErr != nil {
		log.Warn("session", "注入 homeDir 失败：已退出会话标题回填将跳过", "err="+homeErr.Error())
	} else {
		app.Sessions.SetHomeDir(home)
	}
	// Inject the headroom stopper so CleanHeadroom terminates BOTH headroom
	// proxy child processes (claude 8787 + codex-global 8788) before the shared
	// venv directory is removed. Required on Windows where a running
	// headroom.exe inside the venv is locked and os.RemoveAll fails. When the
	// codex-global switch was on, the stopper also clears the openai_base_url
	// marker block and persistence so no dead config points at the removed
	// proxy. The callback is best-effort: CleanHeadroom ignores its error.
	// Wired after `app` exists so the closure can reference both services.
	envCheckSvc.SetHeadroomStopper(app.stopAllHeadroomForUninstall)

	// M3-A2: control runtime + shared-service coordinator. The PTY raw port and
	// Wails context are injected at Startup (they require the Wails ctx). The
	// arbiter/gate/projector are ready immediately; MarkReady is called in
	// Startup after all wiring is complete.
	app.control = remote.NewControlRuntime(remote.NewSystemClock(), log)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	app.sharedLeases = make(map[string][]*remote.SharedDependencyLease)
	app.externalCleanups = make(map[string]*externalCleanupClaim)
	app.externalDurableRuns = make(map[string]externalCleanupRecord)
	app.externalCleanupStore = newFileExternalCleanupStore(configDir)
	// Wire the unbound PTY bridge adapter (design §8.6.3): legacy/v1 WS reaches
	// PTY callbacks through this adapter, not through Wails-bound App methods.
	app.Remote.SetPtyBridge(ptyBridgeAdapter{app: app, pty: app.Pty})

	// M2-A session REST adapter wiring (design §4.2). Inject all M2-A
	// dependencies into the RemoteSessionAdapter and register it with the
	// Server. When wired, session REST index 2-9 + /ws/v1 activate (design §4A
	// hardening gate). The resolver wraps the same platform.CLIResolver that M1
	// HostSummary uses; the journal is the file-backed dangerous-op log.
	homeDir, _ := os.UserHomeDir()
	m2aCatalog := remote.NewSessionCatalog()
	m2aStreams := remote.NewSessionStreamStore()
	m2aJournal := remote.NewSessionOperationJournal(configDir)
	m2aResolver := remote.NewProductionRemoteLaunchResolver(app.CLIResolver, homeDir, os.Environ(), nil)
	m2aLaunchRaw := appLaunchRaw{pty: app.Pty}
	m2aSessRaw := appSessionRaw{pty: app.Pty, sessions: app.Sessions}
	m2aAdapter := remote.NewRemoteSessionAdapter(
		app.control.Gate(), app.control, m2aCatalog, m2aStreams, m2aJournal,
		m2aResolver, m2aLaunchRaw, m2aSessRaw, remote.NewSystemClock(), configDir,
	)
	app.Remote.SetSessionAdapter(m2aAdapter)
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
	case "pi":
		return envcheck.ToolPi, nil
	case "omp":
		return envcheck.ToolOmp, nil
	case "headroom":
		return envcheck.ToolHeadroom, nil
	default:
		return "", fmt.Errorf("unknown CLI tool: %s", tool)
	}
}

// GetHeadroomSavings 查询 headroom 上下文压缩节省统计（压缩次数、节省 token 等）。
// headroom 未安装或查询失败时返回 error，前端据此显示空态；绝不返回伪造零值报告冒充"有数据"。
//
// 注意：savings 读取的是 headroom 共享 ledger（~/.headroom/savings_events.jsonl），
// claude 会话级（8787）与 codex 全局（8788）实例的节省数据均写入同一文件，靠
// SavingsReport.ByClient（client 维度，如 "claude-code" / "codex"）区分来源。
// 前端按 by_client 分项展示即可，后端无需为第二实例改动 savings 逻辑。
func (a *App) GetHeadroomSavings() (*headroom.SavingsReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), headroom.SavingsTimeout)
	defer cancel()
	return a.Headroom.GetSavings(ctx)
}

// GetHeadroomPerfByClient 查询 headroom perf 按 client 聚合的性能统计
// （请求数、平均 cache 命中率、累计节省/读取 token 数、节省百分比），供前端
// codex 卡片突出 cache 命中率。实测（headroom 0.32.x）codex 经 headroom 的
// 实际收益是 prefix cache 稳定（cache 命中率高 → cached token 计费约为新的 1/5），
// 而非输入体积压缩（OpenAI responses 协议 tok_saved 多为 0）；故前端 codex 卡片
// 应突出 AvgCacheHitPct，而非 tokens_saved。
//
// headroom 未安装或查询失败时返回 error，前端据此显示空态；绝不返回伪造数据冒充"有数据"。
// 与 GetHeadroomSavings 一样读取共享 ledger（`headroom perf --format json --raw`），
// 按 record.client 区分来源（codex / claude-code 等），后端无需为第二实例改动 perf 逻辑。
func (a *App) GetHeadroomPerfByClient() ([]headroom.ClientPerfStat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), headroom.PerfTimeout)
	defer cancel()
	return a.Headroom.GetPerfByClient(ctx)
}

// CodexGlobalHeadroomStatus is the frontend-facing snapshot of the codex-global
// headroom toggle. It augments the persisted settings state with the live
// running flag so the UI can render the actual proxy state in one round-trip.
type CodexGlobalHeadroomStatus struct {
	Enabled bool   `json:"enabled"`
	Target  string `json:"target"`
	Port    int    `json:"port"`
	Running bool   `json:"running"`
}

// GetCodexGlobalHeadroom returns the persisted codex-global headroom toggle plus
// the live running state of the second headroom instance (port 8788).
func (a *App) GetCodexGlobalHeadroom() CodexGlobalHeadroomStatus {
	state := a.Settings.GetCodexGlobalHeadroom()
	running := false
	if a.CodexHeadroom != nil {
		running = a.CodexHeadroom.IsRunning()
	}
	return CodexGlobalHeadroomStatus{
		Enabled: state.Enabled,
		Target:  state.Target,
		Port:    state.Port,
		Running: running,
	}
}

// SetCodexGlobalHeadroom toggles the codex-global headroom (independent 8788
// instance with an OpenAI target). It is fully orthogonal to the claude
// session-level headroom (a.Headroom, 8787): this method never starts, stops or
// reconfigures the claude headroom.
//
// enabled=true:
//   - Starts the second headroom instance (StartForOpenAI) if it is not already
//     running. target falls back to codexGlobalHeadroomDefaultTarget when empty;
//     port falls back to CodexGlobalHeadroomDefaultPort when <= 0.
//   - Writes the openai_base_url marker block into ~/.codex/config.toml so codex
//     routes its traffic through the local proxy (with backup + idempotency).
//   - Persists the toggle so Startup can restore it on next launch.
//
// enabled=false:
//   - Removes the openai_base_url marker block first (so codex stops routing
//     through the proxy before the proxy goes away), then stops the instance.
//   - Clears the persisted toggle.
//
// Returns the resulting status. Errors from the proxy start are returned, but
// config/persistence cleanup on disable is best-effort: a failure to rewrite
// config.toml is surfaced as an error so the user knows codex may still point at
// a stopping proxy.
func (a *App) SetCodexGlobalHeadroom(enabled bool, target string, port int) (CodexGlobalHeadroomStatus, error) {
	if a.CodexHeadroom == nil {
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("codex global headroom service is not initialized")
	}
	if a.Settings == nil {
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("settings service is not initialized")
	}

	// Serialize against the startup restore goroutine: the Start/Stop +
	// config-sync + persistence sequence must be atomic with respect to
	// restoreCodexGlobalHeadroomOnStartup, otherwise a UI disable racing the
	// restore can leave Enabled=false with the proxy running and config.toml
	// still carrying the marker block.
	a.codexGlobalMu.Lock()
	defer a.codexGlobalMu.Unlock()

	// M-006: the Codex global headroom is a shared singleton. Acquire/release is
	// session-scoped (Codex launches hold a lease while the toggle is on); the
	// toggle itself is a Start/Stop mutation that MUST consult the coordinator so
	// it cannot reconfigure/stop the singleton out from under active sessions.
	mutation := remote.MutationStartDifferentConfig
	if !enabled {
		mutation = remote.MutationStop
	}
	admission, err := a.acquireSharedMutation(remote.SharedServiceCodexHeadroom, mutation)
	if err != nil {
		return a.GetCodexGlobalHeadroom(), err
	}
	defer a.sharedCoord.ReleaseMutationAdmission(admission)

	if enabled {
		target = strings.TrimSpace(target)
		if target == "" {
			target = codexGlobalHeadroomDefaultTarget
		}
		if port <= 0 {
			port = CodexGlobalHeadroomDefaultPort
		}

		// Start the proxy first; if it fails, do not rewrite config.toml to point
		// codex at a dead proxy. StartForOpenAI is a no-op when already running
		// on this instance.
		if !a.CodexHeadroom.IsRunning() {
			if err := a.CodexHeadroom.StartForOpenAI(target); err != nil {
				return CodexGlobalHeadroomStatus{}, fmt.Errorf("start codex global headroom: %w", err)
			}
			a.Log.Info("headroom", "codex 全局上下文压缩已启用",
				fmt.Sprintf("codex → headroom:127.0.0.1:%d → %s", a.CodexHeadroom.GetPort(), target))
		}

		if err := syncCodexGlobalHeadroomConfig(true, port); err != nil {
			a.Log.Warn("headroom", "写入 codex 全局 openai_base_url 失败", err.Error())
			return CodexGlobalHeadroomStatus{}, fmt.Errorf("write codex openai_base_url: %w", err)
		}

		if err := a.Settings.SetCodexGlobalHeadroom(true, target, port); err != nil {
			a.Log.Warn("headroom", "持久化 codex 全局 headroom 开关失败", err.Error())
			return CodexGlobalHeadroomStatus{}, fmt.Errorf("persist codex global headroom: %w", err)
		}
		return a.GetCodexGlobalHeadroom(), nil
	}

	// Disable path: remove the config marker first (so codex stops targeting the
	// proxy before it stops), then stop the proxy, then clear persistence.
	if err := syncCodexGlobalHeadroomConfig(false, 0); err != nil {
		a.Log.Warn("headroom", "移除 codex 全局 openai_base_url 失败", err.Error())
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("remove codex openai_base_url: %w", err)
	}
	if a.CodexHeadroom.IsRunning() {
		if err := a.CodexHeadroom.Stop(); err != nil {
			a.Log.Warn("headroom", "停止 codex 全局 headroom 失败", err.Error())
		}
	}
	if err := a.Settings.SetCodexGlobalHeadroom(false, "", 0); err != nil {
		a.Log.Warn("headroom", "清除 codex 全局 headroom 持久化失败", err.Error())
		return CodexGlobalHeadroomStatus{}, fmt.Errorf("clear codex global headroom persistence: %w", err)
	}
	return a.GetCodexGlobalHeadroom(), nil
}

// restoreCodexGlobalHeadroomOnStartup re-establishes the codex-global headroom
// proxy (8788) when it was enabled at last shutdown. It must run AFTER
// Settings.Load has completed (Startup calls it asynchronously, well after the
// synchronous settings load). Every failure is best-effort: a broken proxy or
// config write only downgrades compression, it never blocks app startup or
// affects the claude session headroom. The marker block is re-synced so a config
// rewrite by another tool (or a prior crash mid-write) cannot leave codex
// pointing at the wrong URL.
func (a *App) restoreCodexGlobalHeadroomOnStartup() {
	if a.Settings == nil || a.CodexHeadroom == nil {
		return
	}
	if a.isExternalCleanupRecoveryBlocked() {
		if a.Log != nil {
			a.Log.Warn("headroom", "跳过 codex 全局 headroom 恢复：外部进程清理登记未完成")
		}
		return
	}
	// Hold codexGlobalMu across the read + orchestration so a concurrent UI
	// SetCodexGlobalHeadroom(false) cannot slip in between the Enabled read and
	// the StartForOpenAI / config-sync below (which would otherwise re-enable a
	// proxy the user just disabled). SetCodexGlobalHeadroom takes the same lock,
	// so the two paths are fully serialized.
	a.codexGlobalMu.Lock()
	defer a.codexGlobalMu.Unlock()

	state := a.Settings.GetCodexGlobalHeadroom()
	if !state.Enabled {
		return
	}
	target := strings.TrimSpace(state.Target)
	if target == "" {
		target = codexGlobalHeadroomDefaultTarget
	}
	port := state.Port
	if port <= 0 {
		port = CodexGlobalHeadroomDefaultPort
	}

	// R5-002: startup restoration is a real Headroom Start mutation too. Keep an
	// exact coordinator token live across Start + config sync + persistence so a
	// concurrent uninstall drain cannot observe an empty dependency set midway.
	if a.sharedCoord != nil {
		admission, err := a.sharedCoord.AcquireMutationAdmission(remote.SharedServiceCodexHeadroom, remote.MutationStartDifferentConfig)
		if err != nil {
			a.Log.Warn("headroom", "恢复 codex 全局上下文压缩被共享服务门禁拒绝", err.Error())
			return
		}
		defer a.sharedCoord.ReleaseMutationAdmission(admission)
	}

	if !a.CodexHeadroom.IsRunning() {
		if err := a.CodexHeadroom.StartForOpenAI(target); err != nil {
			a.Log.Warn("headroom", "启动时恢复 codex 全局 headroom 失败", err.Error())
			return
		}
	}
	if err := syncCodexGlobalHeadroomConfig(true, port); err != nil {
		a.Log.Warn("headroom", "启动时校验 codex 全局 openai_base_url 失败", err.Error())
		return
	}
	a.Log.Info("headroom", "codex 全局 headroom 已在启动时恢复",
		fmt.Sprintf("port=%d target=%s", port, target))
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

	status.URL = a.Remote.BuildDesktopLaunchURL()
	if status.URL == "" {
		// 随机源失败 → 未能签发 launch grant：失败闭合，不打开浏览器。
		status.Openable = false
		status.Reason = "failed to issue launch grant"
		return status
	}
	status.Openable = true
	return status
}

// OpenRemoteWebUI 确保远程服务可用后，在默认浏览器中打开移动端 Web UI。
func (a *App) OpenRemoteWebUI() (OpenRemoteWebUIResult, error) {
	a.remoteLifecycleMu.Lock()
	defer a.remoteLifecycleMu.Unlock()
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
		// Persist enabled=true. If persistence fails, STOP the just-started server and
		// return an error (R4-Minor: symmetric with Toggle(true)'s failure path) so
		// we never leave a running server whose enabled state did not persist — that
		// would form a running=true / settings=false / disk=false drift.
		if err := a.Settings.SetRemoteEnabled(true); err != nil {
			a.Remote.Stop()
			a.Log.Error("remote", "远程服务已启动，但无法保存启用状态，已停止服务", err.Error())
			return OpenRemoteWebUIResult{}, fmt.Errorf("persist remote enabled state: %w", err)
		}
	}

	// Reuse the URL/grant already computed in GetRemoteWebUIStatus; do not issue
	// a second launch grant.
	launchURL := status.URL
	if launchURL == "" {
		return OpenRemoteWebUIResult{}, errors.New(status.Reason)
	}
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

// RemoteSecurityEventRecord is the sanitized Wails-facing projection of one
// durable security event (no raw line/path/secret).
type RemoteSecurityEventRecord = remote.SecurityEventRecord

// ListRemoteSecurityEvents returns the sanitized newest-first durable security
// event log. limit 0→100, valid 1..500; invalid limits or a not-open/degraded
// sink return an error.
func (a *App) ListRemoteSecurityEvents(limit int) ([]RemoteSecurityEventRecord, error) {
	return a.Remote.ListRemoteSecurityEvents(limit)
}

// buildRemoteHostSummary 是私有 HostSummary provider（design §14.1）。遍历
// contract.KnownCLITypes，对每项调用真实 CLIResolver.Resolve(AppType,
// ModeEmbedded, os.Environ())，仅把 bool 放入 DTO；不调用 ResolveExecutable、
// 不复制 CLI candidate 字符串、不预 build env；错误/path/diagnostics 不外露。
func (a *App) buildRemoteHostSummary() (contract.HostSummary, error) {
	return hostSummaryFromResolver(a.CLIResolver, resolveAppVersion())
}

// hostSummaryFromResolver 构造 HostSummary。resolver 仅需 Resolve；可注入测试双。
func hostSummaryFromResolver(resolver interface {
	Resolve(platform.ResolveRequest) (platform.ResolvedLaunchSpec, error)
}, serverVersion string) (contract.HostSummary, error) {
	avail := make([]contract.CLIAvailability, 0, len(contract.KnownCLITypes))
	for _, cliType := range contract.KnownCLITypes {
		spec, err := resolver.Resolve(platform.ResolveRequest{
			AppType:    string(cliType),
			LaunchMode: string(session.ModeEmbedded),
			Env:        os.Environ(),
		})
		available := err == nil && spec.CLI.Path != ""
		avail = append(avail, contract.CLIAvailability{CLIType: cliType, Available: available})
	}
	return contract.HostSummary{
		APIVersion:      contract.APIVersionV1,
		ServerVersion:   serverVersion,
		CLIAvailability: avail,
	}, nil
}

// --- M1-A 设备配对/安全 Wails wrappers（design §14.1）---

// CreateRemotePairingWindow 创建配对窗口（code 仅本次返回，不持久/不记录）。
func (a *App) CreateRemotePairingWindow(confirmTerminalExposure bool) (remote.PairingWindowInfo, error) {
	return a.Remote.CreatePairingWindow(confirmTerminalExposure)
}

// GetRemotePairingWindow 返回配对窗口状态（不含 code）。
func (a *App) GetRemotePairingWindow() (remote.PairingWindowStatus, error) {
	return a.Remote.GetPairingWindow()
}

// CancelRemotePairingWindow 按 generation CAS 取消配对窗口。
func (a *App) CancelRemotePairingWindow(generation uint64) (bool, error) {
	return a.Remote.CancelPairingWindow(generation)
}

// ListRemoteDevices 返回已配对设备列表。
func (a *App) ListRemoteDevices() ([]remote.DeviceInfo, error) {
	return a.Remote.ListDevices()
}

// RevokeRemoteDevice 撤销设备（confirm=false 拒绝）。
func (a *App) RevokeRemoteDevice(deviceID string, confirm bool) (remote.RevokeDeviceResult, error) {
	if strings.TrimSpace(deviceID) == "" {
		return remote.RevokeDeviceResult{}, fmt.Errorf("device id required")
	}
	return a.Remote.RevokeDevice(deviceID, confirm)
}

// GetRemoteSecurityHealth 返回有界安全健康快照。
func (a *App) GetRemoteSecurityHealth() remote.SecurityHealthSnapshot {
	return a.Remote.GetSecurityHealth()
}

// AcknowledgeRemoteSecurityHealth 确认一个 closed health code（不 resolve/retry）。
func (a *App) AcknowledgeRemoteSecurityHealth(code string) (remote.SecurityHealthSnapshot, error) {
	return a.Remote.AcknowledgeSecurityHealth(code)
}

// ToggleRemoteServer 启动或停止远程服务器。
func (a *App) ToggleRemoteServer(enabled bool) error {
	a.remoteLifecycleMu.Lock()
	defer a.remoteLifecycleMu.Unlock()
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
	a.remoteLifecycleMu.Lock()
	defer a.remoteLifecycleMu.Unlock()
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
	a.Remote.EmitListenConfigurationChanged() // user-initiated change; not Startup restore
	return nil
}

// SetRemoteHost 设置远程服务器监听地址（需先停止服务器，再设置地址，再启动）。
func (a *App) SetRemoteHost(host string) error {
	a.remoteLifecycleMu.Lock()
	defer a.remoteLifecycleMu.Unlock()
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
	a.Remote.EmitListenConfigurationChanged() // user-initiated change; not Startup restore
	return nil
}

// SetRemoteEndpoint updates the remote listen host AND port in a single backend
// transaction (Minor-02): one validation + one settings Save, so a partial
// failure (host ok, port invalid — or vice-versa) never persists one without the
// other. The whole stopped-check → live tuple swap → restart sequence runs under
// remoteLifecycleMu (shared with ToggleRemoteServer) so two concurrent endpoint
// calls, or an endpoint vs a toggle, cannot interleave and leave the live server
// on a mixed host:port tuple (R2-Minor-02). The server is stopped/restarted
// around the in-memory swap exactly like the individual setters, but only if the
// transactional Save succeeded. The legacy SetRemoteHost/SetRemotePort remain
// for their existing callers.
func (a *App) SetRemoteEndpoint(host string, port int) error {
	a.remoteLifecycleMu.Lock()
	defer a.remoteLifecycleMu.Unlock()
	if err := a.Settings.SetRemoteEndpoint(host, port); err != nil {
		return err
	}
	wasRunning := a.Remote.IsRunning()
	if wasRunning {
		a.Remote.Stop()
	}
	a.Remote.SetHost(host)
	a.Remote.SetPort(port)
	a.Log.Info("remote", "监听地址与端口已更新", fmt.Sprintf("host=%s port=%d", host, port))
	if wasRunning {
		if err := a.Remote.Start(a.ctx); err != nil {
			a.Log.Error("remote", "更换地址与端口后重启服务器失败", err.Error())
			return fmt.Errorf("restart remote server on %s:%d: %w", host, port, err)
		}
		a.Log.Info("remote", "远程服务器已在新地址与端口启动", fmt.Sprintf("host=%s port=%d", host, port))
	}
	a.Remote.EmitListenConfigurationChanged() // user-initiated change; not Startup restore
	return nil
}

// Startup Wails 生命周期钩子：应用启动时加载配置和密钥。
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	// Recover durable post-start cleanup ownership before any startup restore or
	// user mutation can touch Headroom. A recovered admission fail-closes the
	// fresh coordinator until the identity-verified process reaches terminal.
	if err := a.recoverExternalCleanups(); err != nil {
		a.addStartupWarning("检测到未完成的外部进程清理；请先关闭旧外部终端，再通过恢复确认 API 重新核验并解锁 Headroom")
		if a.Log != nil {
			a.Log.Warn("session", "恢复外部进程清理登记失败", err.Error())
		}
	}

	// M3-A2: wire the control runtime. Raw PTY output/exit now flows through
	// the RunEventProjector (no direct EventsEmit); desktop writes go through
	// the ControlGate (design §6.3, §8.6).
	a.Pty.SetRunEventSink(a.control.Projector())
	a.control.SetPTYRawPort(appPTYRaw{a.Pty})
	a.control.SetPTYLifecycleRawPort(appPTYLifecycle{a: a})
	a.control.SetWailsContext(ctx)
	a.control.MarkReady()
	// H2: wire the control lifecycle hook so Server Stop/revoke/security-latch
	// fence remote control first (design §4A.3 fence-first authority order).
	a.Remote.SetControlLifecycleHook(remote.NewControlLifecycleHook(a.control))

	a.Updater.CleanupOldBinary()

	a.Log.Info("app", "应用启动")
	loadState := persistentLoadState{initialized: true}

	// M1-B3c: 在 Settings.Load 前运行远程安全迁移 gate。若 settings.json 为 v0
	// （含 legacy remoteToken），gate 在 raw bytes 上完成 v0→v1 迁移；迁移路径下
	// gate 本身执行唯一的 LoadSecurityState，否则交由下方常规路径执行一次。
	// 任何失败/ManualRepair/Future 路径都记录固定警告（不含路径/值/秘密）且不 Start。
	securityLoaded, startAllowed := a.runRemoteSecurityMigrationGate()

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

	// 异步恢复 codex 全局 headroom（独立第二实例，8788 + OpenAI 目标）。
	// 仅当上次退出前开关为开启时才重建；失败仅 Warn 不阻断启动（主功能不依赖它）。
	// 重建会同时校验/补写 config.toml 的 openai_base_url 标记块，保证 codex 路由一致。
	go a.restoreCodexGlobalHeadroomOnStartup()

	// 远程 API 仅在用户显式启用后恢复。新安装和没有该设置的旧配置
	// 默认保持 loopback 且不监听，避免无意暴露到局域网。
	// M1-B3c：remote load/start 由迁移 gate 结果守卫。LoadSecurityState 在所有
	// 路径上仅调用一次（迁移路径由 gate 步骤 a 完成；Missing/Current 由此处完成；
	// Future/Manual/失败路径全部跳过）。Start 条件为 remoteEnabled && startAllowed；
	// 回滚成功及一切失败路径永不 Start（design §C.4）。
	a.applyRemoteGateResult(ctx, securityLoaded, startAllowed, remoteEnabled)

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

	// M3-A2 §10.3: authoritative control shutdown fence FIRST — cancels all
	// device/desktop launch/run permits and current operations, fences run
	// identities. This is the infallible one-shot; subsequent cleanup is
	// bounded and best-effort. No new operations/launches are admitted after.
	if a.control != nil {
		_ = a.control.CloseForShutdown()
	}
	// Fence new external starts lock-free, close shared admissions, and give all
	// in-flight ownership handoffs/StopAll one wall-clock budget. Unrecovered
	// items retain durable authority and are emitted as typed abandonment events;
	// Shutdown never waits indefinitely for disk or process terminal receipt.
	a.shutdownExternalOwnershipBounded()

	// 先保存配置和密钥
	if err := a.SaveAllConfig(); err != nil {
		a.Log.Error("app", "关闭时保存配置失败", err.Error())
	}

	a.Tray.Stop()
	a.Remote.Stop()
	// M1-B1: idempotently close the durable security event sink on shutdown.
	if err := a.Remote.CloseSecurityState(); err != nil {
		a.Log.Warn("remote", "关闭持久安全事件 sink 失败", err.Error())
	}
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
	// Stop the independent codex-global headroom (8788) too. Its enabled state is
	// persisted, so Startup restores it on next launch; we only stop the live
	// process here. This never touches the claude headroom above.
	if a.CodexHeadroom != nil && a.CodexHeadroom.IsRunning() {
		if err := a.CodexHeadroom.Stop(); err != nil {
			a.Log.Error("app", "关闭 codex 全局 Headroom 失败", err.Error())
		}
	}
	if a.Proxy.IsRunning() {
		if err := a.Proxy.Stop(); err != nil {
			a.Log.Error("app", "关闭代理失败", err.Error())
		}
	}
	// External Launcher processes were already handled by the bounded ownership
	// phase immediately after the authority fence. Embedded PTYs close here.
	a.Pty.CloseAll()
	a.Log.Info("app", "应用已关闭")
	a.Log.Close()
}

func (a *App) externalSessionLauncher() externalLauncherPort {
	if a.externalLauncher != nil {
		return a.externalLauncher
	}
	if a.Launcher == nil {
		return nil
	}
	return a.Launcher
}

func (a *App) externalSessionPollInterval() time.Duration {
	if a.externalRunPollInterval > 0 {
		return a.externalRunPollInterval
	}
	return time.Second
}

// shutdownExternalOwnershipBounded is the testable external-process phase of
// graceful Shutdown. A pre-start reservation means it may return at the budget
// without converting an unknown process into "no owner"; every unresolved item
// is included in a typed report and logged. The StopAll worker may finish later,
// while durable authority and the in-process reaper remain conservative.
func (a *App) shutdownExternalOwnershipBounded() remote.ExternalCleanupShutdownReport {
	started := time.Now()
	budget := a.externalShutdownBudget()
	deadline := started.Add(budget)
	a.fenceExternalProcessStarts()
	a.closeSharedLeasesForShutdown()

	handoffTimedOut := false
	for {
		a.externalCleanupMu.Lock()
		inFlight := a.externalOwnershipAttempt != nil
		a.externalCleanupMu.Unlock()
		if !inFlight {
			break
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			handoffTimedOut = true
			break
		}
		sleep := 5 * time.Millisecond
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}

	stopAllTimedOut := false
	if external := a.externalSessionLauncher(); external != nil {
		done := make(chan struct{}, 1)
		go func() {
			external.StopAll()
			a.completeTerminatedExternalCleanups()
			done <- struct{}{}
		}()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			stopAllTimedOut = true
		} else {
			timer := time.NewTimer(remaining)
			select {
			case <-done:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
				stopAllTimedOut = true
			}
		}
	}

	reason := remote.ExternalCleanupAbandonmentShutdownUnconfirmed
	if stopAllTimedOut {
		reason = remote.ExternalCleanupAbandonmentShutdownStopTimeout
	}
	unrecovered := a.snapshotExternalCleanupAbandonments(reason)
	for _, event := range unrecovered {
		a.recordExternalCleanupAbandonment(event)
	}
	report := remote.ExternalCleanupShutdownReport{
		BudgetMillis:    budget.Milliseconds(),
		ElapsedMillis:   time.Since(started).Milliseconds(),
		StopAllTimedOut: stopAllTimedOut,
		HandoffTimedOut: handoffTimedOut,
		Unrecovered:     unrecovered,
	}
	a.externalCleanupMu.Lock()
	a.lastExternalCleanupShutdownReport = report
	a.externalCleanupMu.Unlock()
	return report
}

// stopExternalProcessesForShutdown preserves the Round8 test seam while routing
// through the bounded Round9 protocol.
func (a *App) stopExternalProcessesForShutdown() remote.ExternalCleanupShutdownReport {
	return a.shutdownExternalOwnershipBounded()
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

	// R5-002/R6-001: every Headroom-dependent launch crosses the uninstall
	// admission barrier before Headroom.Start, session creation, resolution, or
	// PTY/external startup. Embedded launches promote before PTY start; external
	// launches keep the admission through Launcher startup and atomically promote
	// it to an opaque external-run lifetime lease before reporting success.
	var headroomAdmission *remote.SharedLaunchAdmission
	if useHeadroom {
		if a.isExternalCleanupRecoveryBlocked() {
			return "", remote.ErrSharedServiceInUse
		}
		if a.sharedCoord == nil {
			return "", remote.ErrSharedServiceInUse
		}
		var admissionErr error
		headroomAdmission, admissionErr = a.sharedCoord.AcquireLaunchAdmission(remote.SharedServiceClaudeHeadroom)
		if admissionErr != nil {
			return "", fmt.Errorf("headroom launch admission: %w", admissionErr)
		}
		defer func() {
			a.sharedCoord.ReleaseLaunchAdmission(headroomAdmission)
		}()
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

		// M-006: acquire shared-service leases for proxy/headroom so the
		// coordinator guard is non-empty while this run is active.
		var sharedKinds []remote.SharedServiceKind
		if useProxy {
			sharedKinds = append(sharedKinds, remote.SharedServiceClaudeProxy)
		}
		if useHeadroom {
			sharedKinds = append(sharedKinds, remote.SharedServiceClaudeHeadroom)
		}
		pid, err := a.launchEmbeddedPTYWithAdmission(sess.ID, spec, headroomAdmission, sharedKinds...)
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
			a.releaseSharedLeases(id) // M-006: release shared leases on natural PTY exit
			a.Log.Info("session", "PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher。R6-001 keeps the
	// startup admission live until a successful process start can atomically
	// promote to an opaque external-run dependency lease.
	external := a.externalSessionLauncher()
	if external == nil {
		err := errors.New("external launcher is not initialized")
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	var externalRun *remote.ExternalRunIdentity
	if headroomAdmission != nil {
		if storeErr := a.requireExternalCleanupStore(); storeErr != nil {
			a.Sessions.MarkFailed(sess.ID, storeErr.Error())
			return "", fmt.Errorf("prepare durable external Claude cleanup: %w", storeErr)
		}
		externalRun, err = a.sharedCoord.MintExternalRunIdentity(contract.SessionID(sess.ID))
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", fmt.Errorf("prepare external Claude run: %w", err)
		}
	}
	startGeneration, startErr := a.captureExternalProcessStartGeneration()
	if startErr != nil {
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	a.externalOwnershipMu.Lock()
	if a.externalShutdown.Load() {
		a.externalOwnershipMu.Unlock()
		err := remote.ErrSharedCoordinatorClosed
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	defer a.externalOwnershipMu.Unlock()
	ownershipKind := remote.SharedServiceKind(0) // no shared dependency
	if headroomAdmission != nil {
		ownershipKind = remote.SharedServiceClaudeHeadroom
	}
	attempt := a.beginExternalOwnershipAttempt(sess.ID, ownershipKind, startGeneration)
	defer a.endExternalOwnershipAttempt(attempt)
	var reservation externalCleanupReservation
	if headroomAdmission != nil {
		var reserveErr error
		reservation, reserveErr = a.reserveExternalProcessOwnership(sess.ID, remote.SharedServiceClaudeHeadroom)
		if reserveErr != nil {
			a.Sessions.MarkFailed(sess.ID, reserveErr.Error())
			return "", fmt.Errorf("prepare durable external Claude ownership: %w", reserveErr)
		}
		a.markExternalOwnershipReservation(attempt)
	}
	if !a.commitExternalProcessStart(attempt, startGeneration) {
		startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	result, err := external.LaunchGuarded(
		sess.ID, *provider, presetName, apiKey, agentTeams, launchMode, workDir,
		func() error { return a.authorizeExternalRawStart(attempt, startGeneration) },
	)
	if err != nil {
		if errors.Is(err, remote.ErrSharedCoordinatorClosed) {
			startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
			a.Sessions.MarkFailed(sess.ID, startErr.Error())
			return "", startErr
		}
		completionErr := a.completeExternalProcessReservation(reservation)
		a.releaseSharedLeases(sess.ID)
		launchErr := fmt.Errorf("launch claude: %w", err)
		if completionErr != nil {
			launchErr = errors.Join(launchErr, completionErr)
		}
		a.Sessions.MarkFailed(sess.ID, launchErr.Error())
		a.Log.Error("session", "进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, launchErr))
		return "", launchErr
	}
	// Record PID and fsync OS identity immediately after start, before lease
	// promotion. Successful runs and failed promotions therefore share one
	// durable ownership chain across graceful Shutdown.
	a.markExternalOwnershipStarted(attempt)
	a.Sessions.SetPID(sess.ID, result.PID)
	if a.externalStartGeneration.Load() != startGeneration || a.externalShutdown.Load() {
		cleanupErr := a.handoffPostCommitShutdownStart(sess.ID, result.PID, ownershipKind, reservation, headroomAdmission, external)
		headroomAdmission = nil
		a.Sessions.MarkFailed(sess.ID, cleanupErr.Error())
		return "", cleanupErr
	}
	if headroomAdmission != nil {
		durableRecord, ownershipErr := a.persistExternalProcessOwnership(
			sess.ID, result.PID, remote.SharedServiceClaudeHeadroom, external,
		)
		if ownershipErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, reservation, false, headroomAdmission, external)
			headroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			a.recordExternalCleanupAbandonment(remote.ExternalCleanupAbandonmentEvent{
				SessionID:          sess.ID,
				Kind:               remote.SharedServiceClaudeHeadroom,
				Reason:             remote.ExternalCleanupAbandonmentDurabilityHandoff,
				DurableReservation: true,
			})
			activationErr := fmt.Errorf("persist external Claude process ownership: %w", ownershipErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Claude process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		if leaseErr := a.acquireAndRememberExternalSharedLease(sess.ID, externalRun, remote.SharedServiceClaudeHeadroom, headroomAdmission); leaseErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, externalCleanupReservation{}, true, headroomAdmission, external)
			headroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			activationErr := fmt.Errorf("activate external Claude headroom lease: %w", leaseErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Claude process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		a.rememberExternalDurableRun(durableRecord)
	}

	a.Log.Info("session", "进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	// 方案 R 降级（external 模式）：Launcher.Launch 不支持注入 --session-id，
	// ClaudeSessionID 留空，tracker 走方案 P（FindLatestActiveJSONL 取最新 mtime）。
	// 副作用：external 模式下同 workDir 多会话仍会指向同一最新 jsonl（边缘场景，主上已知）。
	a.startTitleTracker(sess.ID, workDir)

	// 监控进程退出。Release happens only after the Launcher reports terminal;
	// delayed observation is conservative (blocks mutation longer, never shorter).
	monitorCtx := a.ctx
	if monitorCtx == nil {
		monitorCtx = context.Background()
	}
	go func(id string, lifecycle externalLauncherPort) {
		for lifecycle.IsRunning(id) {
			select {
			case <-monitorCtx.Done():
				return
			case <-time.After(a.externalSessionPollInterval()):
			}
		}
		a.Sessions.MarkExited(id)
		a.releaseSharedLeases(id)
		a.Log.Info("session", "进程已退出", "id="+id)
	}(sess.ID, external)

	return sess.ID, nil
}

// StopSession 停止指定会话
// M-005: embedded PTY sessions are the control gate's domain — route through
// the gate and FAIL-CLOSED (never bypass to raw Pty.Close). When the control
// runtime is nil/not-ready, or the gate denies (including DenySessionNotFound
// for an unknown PTY session), the stop is rejected rather than falling back to
// a raw PTY close. External Launcher sessions (terminal/VSCode/Zed — no embedded
// PTY) remain Launcher-owned: Launcher is their legitimate authority, not a
// gate bypass.
func (a *App) StopSession(sessionID string) error {
	a.Log.Info("session", "停止会话", "id="+sessionID)

	if a.Pty.IsRunning(sessionID) {
		// Embedded PTY session → gate-authoritative stop (fail-closed).
		if a.control == nil || !a.control.IsReady() {
			return remote.ErrControlNotReady
		}
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := a.control.DesktopStop(ctx, contract.SessionID(sessionID)); err != nil {
			return err // fail-closed (incl. DenySessionNotFound)
		}
		a.Sessions.MarkStopped(sessionID)
		a.releaseSharedLeases(sessionID) // M-006
		return nil
	}

	// Non-PTY external Launcher session: Launcher owns the process (not a gate
	// bypass). A failed stop retains the lease because the dependency may still
	// be live; successful/already-terminal stop releases it exactly once.
	external := a.externalSessionLauncher()
	if external != nil && external.IsRunning(sessionID) {
		if err := external.Stop(sessionID); err != nil {
			a.Log.Error("session", "停止会话失败", fmt.Sprintf("id=%s err=%v", sessionID, err))
			return err
		}
		a.Sessions.MarkStopping(sessionID)
		if external.IsRunning(sessionID) {
			// Stop was accepted but Launcher Wait has not proven terminal. Stopping
			// remains active/non-removable; monitor or cleanup reaper owns the lease.
			return nil
		}
	}
	a.completeExternalCleanupForSession(sessionID)
	a.Sessions.MarkStopped(sessionID)
	a.releaseSharedLeases(sessionID)
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

// stopAllSessionsForShutdown is the unexported shutdown cleanup helper (design
// §10.3). It is called ONLY from App.Shutdown and never appears in the Wails
// Bind manifest. The authoritative shutdown fence is
// ControlRuntime.CloseForShutdown (called from Shutdown).
func (a *App) stopAllSessionsForShutdown() {
	ids := a.Sessions.GetRunning()
	external := a.externalSessionLauncher()
	for _, id := range ids {
		if external != nil {
			if err := external.Stop(id); err != nil || external.IsRunning(id) {
				continue // terminal owner/lease remains until monitor or reaper receipt
			}
		}
		a.completeExternalCleanupForSession(id)
		a.Sessions.MarkStopped(id)
		a.releaseSharedLeases(id)
	}
}

// GetSessions 获取所有会话列表
func (a *App) GetSessions() []session.SessionInfo {
	return a.Sessions.List()
}

// RemoveSession 删除已结束的会话记录
// M-005: route through the control gate so a stopped/running gate-managed
// session has its PTY closed AND its control tombstone cleaned by the desktop
// authority (DesktopRemove). FAIL-CLOSED: a running PTY session whose control
// runtime is unavailable is rejected rather than bypassed to the raw manager.
// A DenySessionNotFound (the gate does not manage the session) is the signal
// that the session is external/legacy → manager record cleanup.
func (a *App) RemoveSession(sessionID string) error {
	if a.control != nil && a.control.IsReady() {
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := a.control.DesktopRemove(ctx, contract.SessionID(sessionID)); err != nil {
			if !isControlUnknownSession(err) {
				return err // gate denial → fail-closed
			}
			// DenySessionNotFound: gate does not manage this session → external/legacy.
		} else {
			a.Remote.DestroySessionInputLedger(contract.SessionID(sessionID)) // M3-005: gate remove committed
			a.releaseSharedLeases(sessionID)                                  // M-006
			return nil
		}
	} else if a.Pty.IsRunning(sessionID) {
		// PTY running but gate unavailable → fail-closed (no raw manager bypass).
		return remote.ErrControlNotReady
	}
	// External Launcher / legacy record (PTY not running here): manager cleanup.
	// Release only after Manager.Remove confirms the record is non-running; a
	// failed remove must not drop a dependency from a potentially active run.
	if err := a.Sessions.Remove(sessionID); err != nil {
		return err
	}
	a.Remote.DestroySessionInputLedger(contract.SessionID(sessionID)) // M3-005: manager remove committed
	a.releaseSharedLeases(sessionID)
	return nil
}

// ClearStoppedSessions 清除所有已结束的会话
// M-005 + R3-005: route control-tombstone cleanup through the desktop authority
// (desktop authoritative batch semantics) before clearing the session manager,
// so the batch clear is gate-authorized rather than a raw manager mutation.
// Stopped sessions have no live PTY; this only removes their control entries.
//
// R3-005 fail-closed: control-MANAGED (embedded) stopped sessions are cleared
// through the gate ONLY when control is ready; if control is nil/not-ready they
// are SKIPPED (neither control entry nor manager record is touched) so the two
// stores can never diverge with a dangling control entry. Legacy/terminal
// sessions (no control entry by construction) are still cleared from the manager
// directly. Returns the count of manager records cleared.
func (a *App) ClearStoppedSessions() int {
	return a.ClearStoppedSessionsDetailed().Cleared
}

// ClearStoppedSessionFailure is one per-ID failure from either the control Gate
// or Manager.Remove. Reason is diagnostic text and contains no session content.
type ClearStoppedSessionFailure struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// ClearStoppedSessionsResult is the truthful partial result of a batch clear.
// Cleared/ClearedIDs reflect actual successful Manager.Remove calls only.
// RetainedIDs includes gate-skipped and manager-failed records; Failed separates
// operational errors from ordinary skips such as a concurrent restart.
type ClearStoppedSessionsResult struct {
	Cleared     int                          `json:"cleared"`
	ClearedIDs  []string                     `json:"clearedIds"`
	RetainedIDs []string                     `json:"retainedIds"`
	Failed      []ClearStoppedSessionFailure `json:"failed"`
}

// removeStoppedSessionRecord calls the production Manager.Remove unless a test
// injects the narrow per-ID failure seam above.
func (a *App) removeStoppedSessionRecord(id string) error {
	var err error
	if a.sessionRemove != nil {
		err = a.sessionRemove(id)
	} else {
		err = a.Sessions.Remove(id)
	}
	if err != nil {
		return err
	}
	a.Remote.DestroySessionInputLedger(contract.SessionID(id)) // M3-005: manager remove committed (batch clear path)
	a.releaseSharedLeases(id)
	return nil
}

// ClearStoppedSessionsDetailed clears stopped sessions and returns a typed
// per-store partial result. Control-managed IDs first pass the authoritative
// Gate; every eligible ID then calls Manager.Remove individually. Manager
// failures are retained and propagated, never counted as cleared.
func (a *App) ClearStoppedSessionsDetailed() ClearStoppedSessionsResult {
	result := ClearStoppedSessionsResult{}
	var eligible []string
	var controlManaged []string
	for _, s := range a.Sessions.List() {
		switch s.Status {
		case session.StatusRunning:
			continue
		case session.StatusStopping:
			result.RetainedIDs = append(result.RetainedIDs, s.ID)
			result.Failed = append(result.Failed, ClearStoppedSessionFailure{ID: s.ID, Reason: session.ErrSessionStopping.Error()})
			continue
		}
		if s.Mode == session.ModeEmbedded {
			controlManaged = append(controlManaged, s.ID)
		} else {
			eligible = append(eligible, s.ID)
		}
	}

	controlReady := a.control != nil && a.control.IsReady()
	if len(controlManaged) > 0 {
		if !controlReady {
			// Fail-closed: do NOT clear only the manager when control authority is
			// unavailable; these are ordinary retained/skipped IDs, not raw errors.
			result.RetainedIDs = append(result.RetainedIDs, controlManaged...)
		} else {
			ctx := a.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			ids := make([]contract.SessionID, len(controlManaged))
			for i, id := range controlManaged {
				ids[i] = contract.SessionID(id)
			}
			controlResult := a.control.DesktopClearStopped(ctx, ids)
			for _, perID := range controlResult.Results {
				id := string(perID.ID)
				switch perID.Status {
				case remote.DesktopClearCleared:
					eligible = append(eligible, id)
				case remote.DesktopClearErrored:
					result.RetainedIDs = append(result.RetainedIDs, id)
					result.Failed = append(result.Failed, ClearStoppedSessionFailure{ID: id, Reason: perID.Reason})
					if a.Log != nil {
						a.Log.Warn("session", "控制面清理已停止会话失败", fmt.Sprintf("id=%s reason=%s", id, perID.Reason))
					}
				default:
					result.RetainedIDs = append(result.RetainedIDs, id)
				}
			}
		}
	}

	// R4-005 residual: Manager.Remove is authoritative for the returned count.
	// A control tombstone may already be gone, but a manager failure is reported
	// as retained/failed and never converted into a false success.
	for _, id := range eligible {
		if err := a.removeStoppedSessionRecord(id); err != nil {
			result.RetainedIDs = append(result.RetainedIDs, id)
			result.Failed = append(result.Failed, ClearStoppedSessionFailure{ID: id, Reason: err.Error()})
			if a.Log != nil {
				a.Log.Warn("session", "会话管理器清理失败", fmt.Sprintf("id=%s err=%v", id, err))
			}
			continue
		}
		result.ClearedIDs = append(result.ClearedIDs, id)
	}
	result.Cleared = len(result.ClearedIDs)
	return result
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

	// R5-002: when Codex is configured to use global Headroom, acquire the
	// uninstall admission before config sync, session creation, resolution, or
	// PTY startup. Persisted Enabled remains true while uninstall has stopped the
	// process but has not yet removed the marker, closing that drain window too.
	var codexHeadroomAdmission *remote.SharedLaunchAdmission
	codexUsesGlobalHeadroom := a.CodexHeadroom != nil && a.CodexHeadroom.IsRunning()
	if a.Settings != nil && a.Settings.GetCodexGlobalHeadroom().Enabled {
		codexUsesGlobalHeadroom = true
	}
	if codexUsesGlobalHeadroom {
		if a.isExternalCleanupRecoveryBlocked() {
			return "", remote.ErrSharedServiceInUse
		}
		if a.sharedCoord == nil {
			return "", remote.ErrSharedServiceInUse
		}
		var admissionErr error
		codexHeadroomAdmission, admissionErr = a.sharedCoord.AcquireLaunchAdmission(remote.SharedServiceCodexHeadroom)
		if admissionErr != nil {
			return "", fmt.Errorf("codex headroom launch admission: %w", admissionErr)
		}
		defer func() {
			a.sharedCoord.ReleaseLaunchAdmission(codexHeadroomAdmission)
		}()
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

		// M-006: a Codex session that routes through the global headroom (8788)
		// depends on it; acquire a lease so the toggle/uninstall coordinator guard is
		// non-empty for the run lifetime.
		var sharedKinds []remote.SharedServiceKind
		if codexHeadroomAdmission != nil {
			sharedKinds = append(sharedKinds, remote.SharedServiceCodexHeadroom)
		}
		pid, err := a.launchEmbeddedPTYWithAdmission(sess.ID, spec, codexHeadroomAdmission, sharedKinds...)
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
			a.releaseSharedLeases(id) // M-006: release Codex headroom lease on natural exit
			a.Log.Info("session", "Codex PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher。Keep the Codex
	// Headroom admission through startup, then promote before returning success.
	external := a.externalSessionLauncher()
	if external == nil {
		err := errors.New("external launcher is not initialized")
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	var externalRun *remote.ExternalRunIdentity
	if codexHeadroomAdmission != nil {
		if storeErr := a.requireExternalCleanupStore(); storeErr != nil {
			a.Sessions.MarkFailed(sess.ID, storeErr.Error())
			return "", fmt.Errorf("prepare durable external Codex cleanup: %w", storeErr)
		}
		identity, identityErr := a.sharedCoord.MintExternalRunIdentity(contract.SessionID(sess.ID))
		if identityErr != nil {
			a.Sessions.MarkFailed(sess.ID, identityErr.Error())
			return "", fmt.Errorf("prepare external Codex run: %w", identityErr)
		}
		externalRun = identity
	}
	startGeneration, startErr := a.captureExternalProcessStartGeneration()
	if startErr != nil {
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	a.externalOwnershipMu.Lock()
	if a.externalShutdown.Load() {
		a.externalOwnershipMu.Unlock()
		err := remote.ErrSharedCoordinatorClosed
		a.Sessions.MarkFailed(sess.ID, err.Error())
		return "", err
	}
	defer a.externalOwnershipMu.Unlock()
	ownershipKind := remote.SharedServiceKind(0) // no shared dependency
	if codexHeadroomAdmission != nil {
		ownershipKind = remote.SharedServiceCodexHeadroom
	}
	attempt := a.beginExternalOwnershipAttempt(sess.ID, ownershipKind, startGeneration)
	defer a.endExternalOwnershipAttempt(attempt)
	var reservation externalCleanupReservation
	if codexHeadroomAdmission != nil {
		var reserveErr error
		reservation, reserveErr = a.reserveExternalProcessOwnership(sess.ID, remote.SharedServiceCodexHeadroom)
		if reserveErr != nil {
			a.Sessions.MarkFailed(sess.ID, reserveErr.Error())
			return "", fmt.Errorf("prepare durable external Codex ownership: %w", reserveErr)
		}
		a.markExternalOwnershipReservation(attempt)
	}
	if !a.commitExternalProcessStart(attempt, startGeneration) {
		startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
		a.Sessions.MarkFailed(sess.ID, startErr.Error())
		return "", startErr
	}
	result, err := external.LaunchCodexGuarded(
		sess.ID, launchSettings.Model, launchMode, workDir, envOverrides,
		func() error { return a.authorizeExternalRawStart(attempt, startGeneration) },
	)
	if err != nil {
		if errors.Is(err, remote.ErrSharedCoordinatorClosed) {
			startErr = a.rejectExternalProcessStartAfterFence(sess.ID, ownershipKind, reservation)
			a.Sessions.MarkFailed(sess.ID, startErr.Error())
			return "", startErr
		}
		completionErr := a.completeExternalProcessReservation(reservation)
		a.releaseSharedLeases(sess.ID)
		launchErr := fmt.Errorf("launch codex: %w", err)
		if completionErr != nil {
			launchErr = errors.Join(launchErr, completionErr)
		}
		a.Sessions.MarkFailed(sess.ID, launchErr.Error())
		a.Log.Error("session", "Codex 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, launchErr))
		return "", launchErr
	}
	a.markExternalOwnershipStarted(attempt)
	a.Sessions.SetPID(sess.ID, result.PID)
	if a.externalStartGeneration.Load() != startGeneration || a.externalShutdown.Load() {
		cleanupErr := a.handoffPostCommitShutdownStart(sess.ID, result.PID, ownershipKind, reservation, codexHeadroomAdmission, external)
		codexHeadroomAdmission = nil
		a.Sessions.MarkFailed(sess.ID, cleanupErr.Error())
		return "", cleanupErr
	}
	if codexHeadroomAdmission != nil {
		durableRecord, ownershipErr := a.persistExternalProcessOwnership(
			sess.ID, result.PID, remote.SharedServiceCodexHeadroom, external,
		)
		if ownershipErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, reservation, false, codexHeadroomAdmission, external)
			codexHeadroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			a.recordExternalCleanupAbandonment(remote.ExternalCleanupAbandonmentEvent{
				SessionID:          sess.ID,
				Kind:               remote.SharedServiceCodexHeadroom,
				Reason:             remote.ExternalCleanupAbandonmentDurabilityHandoff,
				DurableReservation: true,
			})
			activationErr := fmt.Errorf("persist external Codex process ownership: %w", ownershipErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Codex process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		if leaseErr := a.acquireAndRememberExternalSharedLease(sess.ID, externalRun, remote.SharedServiceCodexHeadroom, codexHeadroomAdmission); leaseErr != nil {
			cleanup := a.registerExternalCleanup(durableRecord, externalCleanupReservation{}, true, codexHeadroomAdmission, external)
			codexHeadroomAdmission = nil
			stopErr := a.compensateExternalCleanup(cleanup)
			activationErr := fmt.Errorf("activate external Codex headroom lease: %w", leaseErr)
			if stopErr != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("compensate external Codex process: %w", stopErr))
			}
			a.Sessions.MarkFailed(sess.ID, activationErr.Error())
			return "", activationErr
		}
		a.rememberExternalDurableRun(durableRecord)
	}

	a.Log.Info("session", "Codex 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	monitorCtx := a.ctx
	if monitorCtx == nil {
		monitorCtx = context.Background()
	}
	go func(id string, lifecycle externalLauncherPort) {
		for lifecycle.IsRunning(id) {
			select {
			case <-monitorCtx.Done():
				return
			case <-time.After(a.externalSessionPollInterval()):
			}
		}
		a.Sessions.MarkExited(id)
		a.releaseSharedLeases(id)
		a.Log.Info("session", "Codex 进程已退出", "id="+id)
	}(sess.ID, external)

	return sess.ID, nil
}

// LaunchPiSession 启动一个新的 Pi coding agent 会话。
// providerID 非空时根据 Provider 双格式映射到 Pi 内置 provider（anthropic/openai），
// 注入对应 API Key 环境变量，并通过 --provider 参数指定；
// modelName 非空时通过 --model 参数指定模型。
// 与 Codex 不同，Pi 无配置文件，纯靠进程环境变量 + CLI 参数驱动。
func (a *App) LaunchPiSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 Pi 会话请求", fmt.Sprintf("model=%s provider=%s mode=%s workDir=%s shell=%s", modelName, providerID, mode, workDir, shellPath))

	// ---- terminal_presets 桥接 ----
	// modelName 可能是 terminal_preset 的 stable key（形如 "provider/presetName"）
	tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset("pi", modelName)
	tpFound := tpErr == nil && tp != nil
	// presetParams 收集命中的预设参数（contextWindow/thinking/effort/maxTokens），
	// 供后续写入 pi models.json 的 model 配置与解析 --thinking 级别。
	var presetParams config.Parameters
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelName = tp.Model
		presetParams = tp.Parameters
		a.Log.Info("session", "Pi 命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", modelName, tpProvider, tp.Model))
	}

	// ---- legacy provider preset fallback ----
	// 若未命中新体系，且 providerID 非空，检查是否是旧的 provider.Presets key。
	if !tpFound && providerID != "" {
		if provider, pErr := a.Config.GetProvider(providerID); pErr == nil {
			if preset, ok := provider.Presets[modelName]; ok {
				resolvedModel := preset.Model
				if resolvedModel == "" {
					resolvedModel = provider.DefaultModel
				}
				a.Log.Info("session", "Pi 命中旧 provider preset", fmt.Sprintf("key=%s presetModel=%s defaultModel=%s -> resolved=%s", modelName, preset.Model, provider.DefaultModel, resolvedModel))
				modelName = resolvedModel
				presetParams = preset.Parameters
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

	launchSettings := piLaunchSettings{
		Model: strings.TrimSpace(modelName),
	}

	// 构建环境变量注入 + pi models.json 配置写入。
	//
	// Pi 从标准用户目录 ~/.pi/agent 读取 models.json 的 providers 配置。
	// amagi 把当前选中的 provider 翻译成 pi 的一个自定义命名 provider（"amagi-<name>"），
	// 合并写入 ~/.pi/agent/models.json。启动时显式移除 PI_CODING_AGENT_DIR，
	// 避免系统或 CodeBox 自定义环境里的旧值又把 Pi 导向独立副本。
	// 这样 baseURL/api/apiKey 都正确注入，第三方 Anthropic/OpenAI 兼容 provider（如 GLM）
	// 会正确路由到其真实 endpoint，不再误打 pi 内置 anthropic/openai 的官方地址。
	//
	// 与 Claude Code（ANTHROPIC_BASE_URL env）/ Codex（config.toml）/ OpenCode
	//（OPENCODE_CONFIG_CONTENT env）同构：amagi 的 provider 配置作为通用底层，
	// 各引擎各自翻译消费。
	envOverrides := map[string]string{
		// BuildEnv 约定：空值表示从子进程环境中删除该变量。
		"PI_CODING_AGENT_DIR": "",
	}
	if providerID != "" {
		if provider, err := a.Config.GetProvider(providerID); err == nil {
			launchSettings = resolvePiLaunchSettings(*provider, launchSettings.Model, presetParams)
			apiKey, _ := a.getProviderAPIKey(providerID, *provider)

			// 合并写入 pi 标准 agent 目录的 models.json。
			// 仅当成功生成配置时才改写 launchSettings.Provider 为 amagi-<name>；
			// 失败则回退到 piProviderMapping 的旧兜底（保持向后兼容，不阻断启动）。
			// presetParams 透传 contextWindow/maxTokens/thinking 到 pi model 配置。
			if piCfg, cfgErr := launcher.BuildPiModelsConfig(providerID, *provider, launchSettings.Model, apiKey, presetParams); cfgErr == nil {
				agentDir := defaultPiAgentDir()
				// 保留用户 models.json 中已有的 provider 和其他顶层配置，
				// 当次 amagi 生成的同名 provider 优先。
				piCfg = launcher.MergePiAgentConfig(piCfg, agentDir)
				if writeErr := launcher.WritePiAgentConfig(agentDir, piCfg); writeErr == nil {
					launchSettings.Provider = launcher.PiProviderID(providerID)
					a.Log.Info("pi", "已写入 pi models.json", fmt.Sprintf("provider=%s baseURL=%s -> %s/models.json",
						launcher.PiProviderID(providerID), provider.EffectiveBaseURL(""), agentDir))
				} else {
					a.Log.Warn("pi", "写入 pi models.json 失败，回退内置 provider", writeErr.Error())
					launchSettings.Provider, _ = piProviderMapping(*provider)
				}
			} else {
				a.Log.Warn("pi", "生成 pi models.json 失败，回退内置 provider", cfgErr.Error())
				launchSettings.Provider, _ = piProviderMapping(*provider)
			}

			// 冗余兜底：注入对应 API Key env var（与 OpenCode 的双路注入一致）。
			// pi 自定义 provider 已内嵌 apiKey，这里仅作为额外保险。
			if apiKey != "" {
				if _, apiKeyEnv := piProviderMapping(*provider); apiKeyEnv != "" {
					envOverrides[apiKeyEnv] = apiKey
				}
			}
		}
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypePi, "pi", providerID, launchSettings.Model, launchMode, workDir, false)
	a.Log.Info("session", "Pi 会话已创建", fmt.Sprintf("id=%s provider=%s model=%s mode=%s", sess.ID, launchSettings.Provider, launchSettings.Model, launchMode))

	// 调试日志：输出 envOverrides 注入情况
	envKeys := make([]string, 0, len(envOverrides))
	for k := range envOverrides {
		envKeys = append(envKeys, k)
	}
	a.Log.Info("pi", "envOverrides keys", fmt.Sprintf("%v", envKeys))

	// 内嵌终端模式：使用 ConPTY
	if launchMode == session.ModeEmbedded {
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		args := []string{}
		if launchSettings.Provider != "" {
			args = append(args, "--provider", launchSettings.Provider)
		}
		if launchSettings.Model != "" {
			args = append(args, "--model", launchSettings.Model)
		}
		if launchSettings.Thinking != "" {
			args = append(args, "--thinking", launchSettings.Thinking)
		}
		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypePi, string(launchMode), shellPath, workDir, env, args)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}

		pid, err := a.launchEmbeddedPTY(sess.ID, spec)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "Pi PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start pi pty: %w", err)
		}
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "Pi PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.Log.Info("session", "Pi PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	result, err := a.Launcher.LaunchPi(sess.ID, launchSettings.Provider, launchSettings.Model, launchSettings.Thinking, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "Pi 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch pi: %w", err)
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "Pi 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.Log.Info("session", "Pi 进程已退出", "id="+id)
	}(sess.ID)

	return sess.ID, nil
}

// LaunchOmpSession 启动一个新的 Oh My Pi (omp) 会话（完整复刻 LaunchPiSession）。
//
// omp 与 pi 同源：同样的 --provider/--model/--thinking CLI 契约、同样的
// PI_CODING_AGENT_DIR agent 根重定位、同样的会话 JSONL 结构。差异：
//   - agent 根为 ~/.omp/agent（defaultOmpAgentDir），models.yml 为 YAML 格式；
//   - 模型选择走 --model provider/model（--provider 为 legacy 仍可用）；
//   - 插件管理由 internal/ompplugin 服务承载（omp plugin list --json + 写操作
//     CLI，npm 与 marketplace 双源，接线见 bind_list.go）。
//
// 主链路：terminal_presets 桥接（type "omp"）→ 写 ~/.omp/agent/models.yml
// （BuildOmpModelsConfig + MergeOmpModelsConfig + WriteOmpAgentConfig，成功则
// Provider=amagi-<name>，失败回退 ompProviderMapping）→ embedded/terminal 双模式。
func (a *App) LaunchOmpSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	a.Log.Info("session", "启动 omp 会话请求", fmt.Sprintf("model=%s provider=%s mode=%s workDir=%s shell=%s", modelName, providerID, mode, workDir, shellPath))

	// ---- terminal_presets 桥接 ----
	// modelName 可能是 terminal_preset 的 stable key（形如 "provider/presetName"）
	tpProvider, tp, tpErr := a.Config.ResolveTerminalPreset("omp", modelName)
	tpFound := tpErr == nil && tp != nil
	// presetParams 收集命中的预设参数（contextWindow/thinking/effort/maxTokens），
	// 供后续写入 omp models.yml 的 model 配置与解析 --thinking 级别。
	var presetParams config.Parameters
	if tpFound {
		if tpProvider != "" {
			providerID = tpProvider
		}
		modelName = tp.Model
		presetParams = tp.Parameters
		a.Log.Info("session", "omp 命中 terminal_preset", fmt.Sprintf("key=%s provider=%s model=%s", modelName, tpProvider, tp.Model))
	}

	// ---- legacy provider preset fallback ----
	// 若未命中新体系，且 providerID 非空，检查是否是旧的 provider.Presets key。
	if !tpFound && providerID != "" {
		if provider, pErr := a.Config.GetProvider(providerID); pErr == nil {
			if preset, ok := provider.Presets[modelName]; ok {
				resolvedModel := preset.Model
				if resolvedModel == "" {
					resolvedModel = provider.DefaultModel
				}
				a.Log.Info("session", "omp 命中旧 provider preset", fmt.Sprintf("key=%s presetModel=%s defaultModel=%s -> resolved=%s", modelName, preset.Model, provider.DefaultModel, resolvedModel))
				modelName = resolvedModel
				presetParams = preset.Parameters
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

	launchSettings := ompLaunchSettings{
		Model: strings.TrimSpace(modelName),
	}

	// 构建环境变量注入 + omp models.yml 配置写入。
	//
	// omp 从标准用户目录 ~/.omp/agent 读取 models.yml 的 providers 配置。
	// amagi 把当前选中的 provider 翻译成 omp 的一个自定义命名 provider（"amagi-<name>"），
	// 合并写入 ~/.omp/agent/models.yml。启动时显式移除 PI_CODING_AGENT_DIR，
	// 避免系统或 CodeBox 自定义环境里的旧值又把 omp 导向独立副本
	//（实测：PI_CODING_AGENT_DIR 只重定位会话目录，models.yml 始终从默认根读取）。
	envOverrides := map[string]string{
		// BuildEnv 约定：空值表示从子进程环境中删除该变量。
		"PI_CODING_AGENT_DIR": "",
	}
	if providerID != "" {
		if provider, err := a.Config.GetProvider(providerID); err == nil {
			launchSettings = resolveOmpLaunchSettings(*provider, launchSettings.Model, presetParams)
			apiKey, _ := a.getProviderAPIKey(providerID, *provider)

			// 合并写入 omp 标准 agent 目录的 models.yml。
			// 仅当成功生成配置时才改写 launchSettings.Provider 为 amagi-<name>；
			// 失败则回退到 ompProviderMapping 的旧兜底（保持向后兼容，不阻断启动）。
			if ompCfg, cfgErr := launcher.BuildOmpModelsConfig(providerID, *provider, launchSettings.Model, apiKey, presetParams); cfgErr == nil {
				agentDir := defaultOmpAgentDir()
				// 保留用户 models.yml 中已有的 provider 和其他顶层配置，
				// 当次 amagi 生成的同名 provider 优先。
				ompCfg = launcher.MergeOmpModelsConfig(ompCfg, agentDir)
				if writeErr := launcher.WriteOmpAgentConfig(agentDir, ompCfg); writeErr == nil {
					launchSettings.Provider = launcher.OmpProviderID(providerID)
					a.Log.Info("omp", "已写入 omp models.yml", fmt.Sprintf("provider=%s baseURL=%s -> %s/models.yml",
						launcher.OmpProviderID(providerID), provider.EffectiveBaseURL(""), agentDir))
				} else {
					a.Log.Warn("omp", "写入 omp models.yml 失败，回退内置 provider", writeErr.Error())
					launchSettings.Provider, _ = ompProviderMapping(*provider)
				}
			} else {
				a.Log.Warn("omp", "生成 omp models.yml 失败，回退内置 provider", cfgErr.Error())
				launchSettings.Provider, _ = ompProviderMapping(*provider)
			}

			// 冗余兜底：注入对应 API Key env var（与 OpenCode/Pi 的双路注入一致）。
			// omp 自定义 provider 已内嵌 apiKey，这里仅作为额外保险。
			if apiKey != "" {
				if _, apiKeyEnv := ompProviderMapping(*provider); apiKeyEnv != "" {
					envOverrides[apiKeyEnv] = apiKey
				}
			}
		}
	}

	// 创建会话记录
	sess := a.Sessions.Create(session.AppTypeOhMyPi, "omp", providerID, launchSettings.Model, launchMode, workDir, false)
	a.Log.Info("session", "omp 会话已创建", fmt.Sprintf("id=%s provider=%s model=%s mode=%s", sess.ID, launchSettings.Provider, launchSettings.Model, launchMode))

	// 调试日志：输出 envOverrides 注入情况
	envKeys := make([]string, 0, len(envOverrides))
	for k := range envOverrides {
		envKeys = append(envKeys, k)
	}
	a.Log.Info("omp", "envOverrides keys", fmt.Sprintf("%v", envKeys))

	// 内嵌终端模式：使用 ConPTY
	if launchMode == session.ModeEmbedded {
		// 注入自定义环境变量（自定义 > 系统，再被 envOverrides 覆盖）
		baseEnv := a.EnvVars.MergeWithSystem()
		env := launcher.BuildEnv(baseEnv, envOverrides)

		args := []string{}
		if launchSettings.Provider != "" {
			args = append(args, "--provider", launchSettings.Provider)
		}
		if launchSettings.Model != "" {
			args = append(args, "--model", launchSettings.Model)
		}
		if launchSettings.Thinking != "" {
			args = append(args, "--thinking", launchSettings.Thinking)
		}
		spec, err := a.resolveEmbeddedLaunchSpec(session.AppTypeOhMyPi, string(launchMode), shellPath, workDir, env, args)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			return "", err
		}

		pid, err := a.launchEmbeddedPTY(sess.ID, spec)
		if err != nil {
			a.Sessions.MarkFailed(sess.ID, err.Error())
			a.Log.Error("session", "omp PTY启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
			return "", fmt.Errorf("start omp pty: %w", err)
		}
		a.Sessions.SetPID(sess.ID, pid)
		a.Log.Info("session", "omp PTY进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, pid))

		go func(id string) {
			for a.Pty.IsRunning(id) {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
			a.Sessions.MarkExited(id)
			a.Log.Info("session", "omp PTY进程已退出", "id="+id)
		}(sess.ID)

		return sess.ID, nil
	}

	// 外部终端/VSCode/Zed 模式：使用 Launcher
	result, err := a.Launcher.LaunchOmp(sess.ID, launchSettings.Provider, launchSettings.Model, launchSettings.Thinking, launchMode, workDir, envOverrides)
	if err != nil {
		a.Sessions.MarkFailed(sess.ID, err.Error())
		a.Log.Error("session", "omp 进程启动失败", fmt.Sprintf("id=%s err=%v", sess.ID, err))
		return "", fmt.Errorf("launch omp: %w", err)
	}

	a.Sessions.SetPID(sess.ID, result.PID)
	a.Log.Info("session", "omp 进程已启动", fmt.Sprintf("id=%s pid=%d", sess.ID, result.PID))

	go func(id string) {
		for a.Launcher.IsRunning(id) {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		a.Sessions.MarkExited(id)
		a.Log.Info("session", "omp 进程已退出", "id="+id)
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

// --- Codex global headroom openai_base_url marker block ---
//
// syncCodexGlobalHeadroomConfig writes or removes the top-level openai_base_url
// assignment that routes codex desktop traffic through the independent
// codex-global headroom proxy (port 8788). It is intentionally separate from
// syncCodexConfigFile / syncCodexCustomProviderConfigFile:
//
//   - It uses a distinct marker pair (amagi-headroom-global-start/-end) so the
//     provider-section cleanup (removeCodexManagedProviderSection /
//     cleanupCodexManagedProviderConfig) never strips it, and so a model-only
//     sync (syncCodexConfigModel) leaves the openai_base_url intact.
//   - It edits the file surgically (line-based) to preserve every other config
//     key (plugins / marketplaces / mcp_servers / profiles / ...).
//   - It backs up the previous content to config.toml.bak.<ts> before writing.
//
// enabled=true inserts/refreshes the block; enabled=false removes it. Both paths
// are idempotent: re-running with the same state is a no-op (apart from a fresh
// backup when content actually changes).
func syncCodexGlobalHeadroomConfig(enabled bool, port int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")

	previous, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read codex config.toml: %w", err)
		}
		// No config yet. Only create one when enabling; disabling is a no-op.
		if !enabled {
			return nil
		}
		previous = nil
	}

	content := string(previous)
	lines := splitTomlLinesSafe(content)
	originalJoined := strings.Join(lines, "\n")

	lines = removeCodexGlobalHeadroomBlock(lines)
	if enabled {
		if port <= 0 {
			port = CodexGlobalHeadroomDefaultPort
		}
		lines = insertCodexGlobalHeadroomBlock(lines, port)
	}

	updatedJoined := strings.Join(lines, "\n")
	if updatedJoined == originalJoined {
		// Idempotent no-op: avoid touching mtimes / writing a backup when the
		// desired state is already present.
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	return writeCodexConfigWithBackup(configPath, []byte(updatedJoined), previous)
}

// removeCodexGlobalHeadroomBlock strips the amagi-headroom-global marker block
// (inclusive of both marker lines and any lines between them). It is a no-op
// when the block is absent and is safe to run on config that also contains the
// amagi-codebox-inject block (the markers are distinct).
func removeCodexGlobalHeadroomBlock(lines []string) []string {
	updated := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == codexGlobalHeadroomMarkerStart {
			skip = true
			continue
		}
		if skip {
			if trimmed == codexGlobalHeadroomMarkerEnd {
				skip = false
			}
			continue
		}
		updated = append(updated, line)
	}
	// A removed block often leaves a doubled blank line behind; collapse only
	// runs of 2+ blanks introduced around the removed region without touching
	// intentional spacing elsewhere is overkill -- trimTrailingEmptyLines keeps
	// the tail tidy which is the only place a dangling blank survives.
	return trimTrailingEmptyLines(updated)
}

// insertCodexGlobalHeadroomBlock inserts a fresh marker block containing the
// top-level openai_base_url assignment. The block is placed at the end of the
// top-level region (immediately before the first [section] header) so the
// assignment stays top-level in TOML terms; when the file has no section it is
// appended at the end. An existing stray top-level openai_base_url outside any
// marker block is removed first to avoid a duplicate key (TOML forbids
// duplicate top-level keys).
func insertCodexGlobalHeadroomBlock(lines []string, port int) []string {
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	block := []string{
		codexGlobalHeadroomMarkerStart,
		"openai_base_url = " + strconv.Quote(baseURL),
		codexGlobalHeadroomMarkerEnd,
	}

	// Drop any pre-existing top-level openai_base_url so we never emit a
	// duplicate top-level key (which would make the TOML invalid).
	cleaned := make([]string, 0, len(lines))
	inTopLevel := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			inTopLevel = false
		}
		if inTopLevel {
			if key, ok := tomlAssignmentKey(trimmed); ok && key == "openai_base_url" {
				continue
			}
		}
		cleaned = append(cleaned, line)
	}

	// Find the index of the first section header and insert the block just
	// before it; fall back to appending at the end when there is no section.
	insertAt := len(cleaned)
	inTopLevel = true
	for i, line := range cleaned {
		trimmed := strings.TrimSpace(line)
		if inTopLevel && isTomlTableHeader(trimmed) {
			insertAt = i
			break
		}
		if isTomlTableHeader(trimmed) {
			inTopLevel = false
		}
	}

	out := make([]string, 0, len(cleaned)+len(block)+2)
	out = append(out, cleaned[:insertAt]...)
	// Separate the block from preceding top-level content with a blank line when
	// there is content above and it is not already blank.
	if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
		out = append(out, "")
	}
	out = append(out, block...)
	out = append(out, cleaned[insertAt:]...)
	return out
}

// splitTomlLinesSafe normalizes CR/CRLF to LF and splits into lines. Unlike the
// codexplugin variant it never returns nil for empty input (returns an empty
// slice) so downstream join logic is uniform.
func splitTomlLinesSafe(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.Split(content, "\n")
}

// writeCodexConfigWithBackup writes next to path, backing up previous to
// path.bak.<YYYYMMDDHHMMSS_msec>_<rand4> first (only when previous is non-empty).
// The timestamp carries millisecond precision plus a short random suffix so two
// writes within the same second (or even same millisecond) do not silently
// overwrite each other's backup. It matches the codexplugin.writeConfigWithBackup
// contract but lives in package main so the global-headroom config layer does
// not depend on internal/codexplugin internals. Mode preserves the existing file
// mode (default 0600).
func writeCodexConfigWithBackup(path string, next []byte, previous []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	if len(previous) > 0 {
		backupPath := fmt.Sprintf("%s.bak.%s.%s", path, time.Now().Format("20060102150405.000"), randHexSuffix(4))
		if err := os.WriteFile(backupPath, previous, 0600); err != nil {
			return fmt.Errorf("backup codex config.toml: %w", err)
		}
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary codex config: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(next); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary codex config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary codex config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary codex config: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temporary codex config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace codex config.toml: %w", err)
	}
	cleanup = false
	return nil
}

// randHexSuffix returns a short random hex string of byteLen*2 hex characters
// (e.g. byteLen=4 -> 8 hex chars). It is used to disambiguate config backups
// that land within the same clock millisecond. On any read error it falls back
// to a fixed string so the caller never fails solely because the suffix could
// not be generated.
func randHexSuffix(byteLen int) string {
	if byteLen <= 0 {
		byteLen = 4
	}
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "00000000"[:byteLen*2]
	}
	return hex.EncodeToString(b)
}

// removeCodexGlobalHeadroomConfig is the package-level disable entry used by the
// envcheck uninstall hook (stopAllHeadroomForUninstall) which does not have
// access to the App-bound wrapper. It removes the marker block best-effort and
// is idempotent.
func removeCodexGlobalHeadroomConfig() error {
	return syncCodexGlobalHeadroomConfig(false, 0)
}

// stopAllHeadroomForUninstall is the headroom stopper injected into envcheck so
// CleanHeadroom terminates BOTH proxy child processes before the shared venv is
// removed. On Windows a running headroom.exe inside the venv is locked by the OS
// and os.RemoveAll would fail; stopping both instances first avoids that. When
// the codex-global switch was persisted on, it also clears the openai_base_url
// marker block and persistence so codex does not keep pointing at the removed
// proxy (a dead openai_base_url would break codex until manually fixed).
//
// R3-002: when the coordinator rejects because active sessions hold headroom
// leases, this returns envcheck.ErrHeadroomInUse (wrapping the coordinator
// detail) so CleanHeadroom treats it as a FATAL rejection and aborts BEFORE the
// venv is removed. Plain stop failures (process already dead, etc.) remain
// best-effort and non-fatal. Individual failures are logged.
func (a *App) stopAllHeadroomForUninstall() (error, func()) {
	if a.isExternalCleanupRecoveryBlocked() {
		return fmt.Errorf("%w: %w", envcheck.ErrHeadroomInUse, remote.ErrSharedServiceInUse), func() {}
	}
	// M-006 / R3-002: consult the coordinator BEFORE tearing down shared headroom.
	// R4-002 (TOCTOU): instead of two instantaneous LeaseCount checks (which left a
	// window for a concurrent launch to acquire a lease before RemoveAll), this
	// enters a SINGLE install-drain critical section on the SharedServiceCoordinator
	// that blocks new AcquireForRun for BOTH headroom kinds, confirms both are
	// lease-free, and stays held until CleanHeadroom finishes the venv removal. It
	// returns (stopErr, releaseDrain): releaseDrain releases the drain and is
	// invoked by CleanHeadroom via defer after RemoveAll (or on abort). When
	// existing leases are present, it returns ErrHeadroomInUse (fatal) plus the
	// releaseDrain (still set so the caller releases the drain it entered).
	release := func() {} // default no-op when coordinator is absent
	if a.sharedCoord != nil {
		empty := a.sharedCoord.BeginHeadroomUninstallDrain()
		release = a.sharedCoord.EndHeadroomUninstallDrain
		if !empty {
			cn := a.sharedCoord.LeaseCount(remote.SharedServiceClaudeHeadroom)
			xn := a.sharedCoord.LeaseCount(remote.SharedServiceCodexHeadroom)
			if a.Log != nil {
				a.Log.Warn("headroom", "卸载联动拒绝：仍有活跃会话占用 headroom", fmt.Sprintf("claude=%d codex=%d", cn, xn))
			}
			return fmt.Errorf("%w: %w", envcheck.ErrHeadroomInUse, remote.ErrSharedServiceInUse), release
		}
	}
	var firstErr error
	logErr := func(scope string, err error) {
		if err == nil {
			return
		}
		if a.Log != nil {
			a.Log.Warn("headroom", scope, err.Error())
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if a.Headroom != nil {
		logErr("卸载联动：停止 claude 会话级 headroom 失败", a.Headroom.Stop())
	}
	if a.CodexHeadroom != nil {
		logErr("卸载联动：停止 codex 全局 headroom 失败", a.CodexHeadroom.Stop())
	}
	// Only tear down the codex-global config/persistence when the toggle was on;
	// otherwise this is a plain uninstall of the claude-side headroom.
	if a.Settings != nil && a.Settings.GetCodexGlobalHeadroom().Enabled {
		logErr("卸载联动：清理 codex 全局 openai_base_url 失败", removeCodexGlobalHeadroomConfig())
		logErr("卸载联动：清除 codex 全局 headroom 持久化失败", a.Settings.SetCodexGlobalHeadroom(false, "", 0))
	}
	return firstErr, release
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

// piProviderMapping 把 amagi Provider 映射成 Pi 的内置 provider 名 + 对应 API Key env var。
//
// 注意：这是**回退路径**，仅当 LaunchPiSession 未能生成 pi models.json 时使用。
// 主路径是 launcher.BuildPiModelsConfig + WritePiAgentConfig，会把 amagi provider
// 翻译成 pi 的自定义命名 provider（"amagi-<name>"，含完整 baseURL/api/apiKey），
// 合并到 Pi 的标准 ~/.pi/agent/models.json 后加载。
//
// 此回退映射把第三方 Anthropic/OpenAI 兼容 provider 粗略归到 pi 内置的
// anthropic/openai，会丢失自定义 baseURL（导致第三方 provider 误打官方 endpoint），
// 因此仅作兜底，不应作为常态路径。
func piProviderMapping(p config.Provider) (piProvider, apiKeyEnv string) {
	if p.IsAnthropicCompatible() {
		return "anthropic", "ANTHROPIC_API_KEY"
	}
	if p.IsOpenAICompatible() {
		return "openai", "OPENAI_API_KEY"
	}
	return "", ""
}

// resolvePiLaunchSettings 解析 Pi 会话启动参数：确定 provider / model / thinking。
// requestedModel 为空时回退到 provider.DefaultModel。
// params.ReasoningEffort（low/medium/high/xhigh/max）直接作为 pi 的 --thinking 级别，
// 两者值域兼容；若 Thinking.Type=="disabled" 则强制 off。
//
// 返回的 Provider 是初始猜测（piProviderMapping 的内置 provider 名）；
// LaunchPiSession 在成功写入 pi models.json 后会用 "amagi-<name>" 覆盖它。
func resolvePiLaunchSettings(provider config.Provider, requestedModel string, params config.Parameters) piLaunchSettings {
	model := strings.TrimSpace(requestedModel)
	piProvider, _ := piProviderMapping(provider)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultModel)
	}
	thinking := strings.TrimSpace(params.ReasoningEffort)
	// Thinking.Type=disabled 显式关闭思考（pi 用 off）。
	// 注意：只有 ReasoningEffort 为空且 Thinking 显式 disabled 时才映射 off，
	// 避免 disabled + 某个 effort 值的歧义（effort 优先）。
	if thinking == "" && params.Thinking != nil && params.Thinking.Type == "disabled" {
		thinking = "off"
	}
	return piLaunchSettings{
		Provider: piProvider,
		Model:    model,
		Thinking: thinking,
	}
}

// ompProviderMapping 把 amagi Provider 映射成 omp 的内置 provider 名 + 对应 API Key env var。
//
// 注意：这是**回退路径**，仅当 LaunchOmpSession 未能生成 omp models.yml 时使用。
// 主路径是 launcher.BuildOmpModelsConfig + WriteOmpAgentConfig，会把 amagi provider
// 翻译成 omp 的自定义命名 provider（"amagi-<name>"，含完整 baseURL/api/apiKey），
// 合并到 omp 的标准 ~/.omp/agent/models.yml 后加载。
//
// 此回退映射把第三方 Anthropic/OpenAI 兼容 provider 粗略归到 omp 内置的
// anthropic/openai，会丢失自定义 baseURL（导致第三方 provider 误打官方 endpoint），
// 因此仅作兜底，不应作为常态路径（复刻 piProviderMapping）。
func ompProviderMapping(p config.Provider) (ompProvider, apiKeyEnv string) {
	if p.IsAnthropicCompatible() {
		return "anthropic", "ANTHROPIC_API_KEY"
	}
	if p.IsOpenAICompatible() {
		return "openai", "OPENAI_API_KEY"
	}
	return "", ""
}

// resolveOmpLaunchSettings 解析 omp 会话启动参数：确定 provider / model / thinking
// （复刻 resolvePiLaunchSettings）。
// requestedModel 为空时回退到 provider.DefaultModel。
// params.ReasoningEffort（low/medium/high/xhigh/max）直接作为 omp 的 --thinking 级别，
// 两者值域兼容；若 Thinking.Type=="disabled" 则强制 off。
//
// 返回的 Provider 是初始猜测（ompProviderMapping 的内置 provider 名）；
// LaunchOmpSession 在成功写入 omp models.yml 后会用 "amagi-<name>" 覆盖它。
func resolveOmpLaunchSettings(provider config.Provider, requestedModel string, params config.Parameters) ompLaunchSettings {
	model := strings.TrimSpace(requestedModel)
	ompProvider, _ := ompProviderMapping(provider)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultModel)
	}
	thinking := strings.TrimSpace(params.ReasoningEffort)
	// Thinking.Type=disabled 显式关闭思考（omp 用 off）。
	// 注意：只有 ReasoningEffort 为空且 Thinking 显式 disabled 时才映射 off，
	// 避免 disabled + 某个 effort 值的歧义（effort 优先）。
	if thinking == "" && params.Thinking != nil && params.Thinking.Type == "disabled" {
		thinking = "off"
	}
	return ompLaunchSettings{
		Provider: ompProvider,
		Model:    model,
		Thinking: thinking,
	}
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

		pid, err := a.launchEmbeddedPTY(sess.ID, spec)
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
// M-005: fail-closed — when the control runtime is not ready the write is
// rejected rather than falling back to the raw PTY service (design §6.3 C-01:
// every mutation must go through the Gate). The control runtime is created in
// the App constructor and MarkReady runs in Startup before any embedded PTY is
// created, so this only rejects writes during the startup/shutdown window.
func (a *App) PtyWrite(sessionID string, data string) error {
	if a.control == nil || !a.control.IsReady() {
		return remote.ErrControlNotReady
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	return a.control.DesktopInput(a.ctx, contract.SessionID(sessionID), raw)
}

// PtyWriteLarge 向内嵌终端分块写入大量数据（用于长文本粘贴）。data 为 base64 编码。
// 内部将数据拆分为 1KB 小块逐步写入，避免 ConPTY 缓冲区溢出截断。
// M-005: fail-closed (see PtyWrite).
func (a *App) PtyWriteLarge(sessionID string, data string) error {
	if a.control == nil || !a.control.IsReady() {
		return remote.ErrControlNotReady
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	return a.control.DesktopPasteChunk(a.ctx, contract.SessionID(sessionID), raw)
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
// M-005: fail-closed (see PtyWrite).
func (a *App) PtyResize(sessionID string, cols, rows int) error {
	if a.control == nil || !a.control.IsReady() {
		return remote.ErrControlNotReady
	}
	return a.control.DesktopPassiveResize(a.ctx, contract.SessionID(sessionID), cols, rows)
}

// GetOutputHistorySnapshot returns a JSON-encoded snapshot of the output history
// along with the emitSeq and the current run token/version at snapshot time.
// The JSON structure is:
//
//	{"data": "<base64-encoded bytes>", "seq": <uint64>, "runToken": "<opaque>", "runVersion": "<decimal>"}
//
// Frontend uses the seq to deduplicate live events: any live event with
// seq <= the returned seq is already contained in the history snapshot.
// runToken/runVersion enable A3's strict run-scoped filtering (design §8.6.1).
func (a *App) GetOutputHistorySnapshot(sessionID string) (string, error) {
	data, seq, err := a.Pty.GetOutputHistoryWithSeq(sessionID)
	if err != nil {
		return "", err
	}
	if a.control != nil {
		return a.control.Projector().FormatSnapshotJSON(contract.SessionID(sessionID), data, seq)
	}
	result := struct {
		Data string `json:"data"`
		Seq  uint64 `json:"seq"`
	}{
		Data: base64.StdEncoding.EncodeToString(data),
		Seq:  seq,
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal history snapshot: %w", err)
	}
	return string(b), nil
}

// GetPtyDimensions 返回指定 PTY 会话的当前尺寸。
// 实现 remote.DimensionsProvider 接口。
func (a *App) GetPtyDimensions(sessionID string) (cols, rows int, err error) {
	return a.Pty.GetPtyDimensions(sessionID)
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

// defaultPiAgentDir 返回 Pi 的标准用户级 agent 目录。Pi 的用户数据树
// 位于 ~/.pi，其 settings.json/models.json/auth.json/packages/sessions 位于
// ~/.pi/agent。CodeBox 不应把它改写到自己的 configDir。
func defaultPiAgentDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".pi", "agent")
	}
	return filepath.Join(home, ".pi", "agent")
}

// defaultOmpAgentDir 返回 Oh My Pi (omp) 的标准用户级 agent 目录（复刻
// defaultPiAgentDir）。omp 的用户数据树位于 ~/.omp，models.yml/sessions 位于
// ~/.omp/agent。CodeBox 不应把它改写到自己的 configDir。
func defaultOmpAgentDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".omp", "agent")
	}
	return filepath.Join(home, ".omp", "agent")
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
