// Package main implements the Amagi CodeBox desktop application, coordinating
// service wiring, lifecycle hooks, Wails frontend bindings, and local session/remote control.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"amagi-codebox/internal/agentcli"
	"amagi-codebox/internal/agentcli/install"
	"amagi-codebox/internal/agentprofile"
	"amagi-codebox/internal/aiproxy"
	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/claudewebui"
	"amagi-codebox/internal/cleanupstore"
	"amagi-codebox/internal/codexplugin"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/dockerwsl"
	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/gitassist"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/ompconfig"
	"amagi-codebox/internal/ompplugin"
	"amagi-codebox/internal/opencodeconfig"
	"amagi-codebox/internal/opencodeplugin"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/piconfig"
	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/plugin"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remoteclient"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/sharedext"
	"amagi-codebox/internal/sharedmarket"
	"amagi-codebox/internal/sharedmcp"
	"amagi-codebox/internal/sharedskills"
	"amagi-codebox/internal/skins"
	"amagi-codebox/internal/tray"
	"amagi-codebox/internal/updater"
	"amagi-codebox/internal/usage"
	"amagi-codebox/internal/webui"
	"amagi-codebox/internal/wslsetup"
	"amagi-codebox/internal/wslsupply"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type codexLaunchSettings struct {
	Model              string
	ReasoningEffort    string
	ModelContextWindow int
	AutoCompactLimit   int
	ProviderBaseURL    string
	// WireAPI 为 provider.OpenAI.EffectiveWireAPI() 归一化后的接口协议选择：
	// "chat" -> Chat Completions；""/"responses" -> Responses（legacy 默认，
	// 与既有硬编码行为一致）。实际写入由 codexWireAPI 兑底归一。
	WireAPI string
}

// externalLauncherPort is the narrow Launcher-owned lifecycle used by external
// Claude/Codex sessions. Production uses LauncherService; tests inject a
// deterministic process lifecycle without changing the exported App surface.
type externalLauncherPort interface {
	LaunchGuarded(string, config.Provider, string, string, config.AgentTeamsConfig, session.LaunchMode, string, func() error, []string) (*launcher.LaunchResult, error)
	LaunchCodexGuarded(string, string, session.LaunchMode, string, map[string]string, func() error) (*launcher.LaunchResult, error)
	CaptureProcessIdentity(string) (string, error)
	RecoverProcess(string, int, string) (bool, error)
	IsRunning(string) bool
	Stop(string) error
	StopAll()
}

// codexArgsExternalLauncherPort is the current production Codex launcher
// contract. The legacy string-model method remains on externalLauncherPort so
// persisted recovery test doubles and older embedders stay source compatible.
type codexArgsExternalLauncherPort interface {
	LaunchCodexArgsGuarded(string, []string, session.LaunchMode, string, map[string]string, func() error) (*launcher.LaunchResult, error)
}

// externalCleanupClaim is the exact App-owned handoff after an OS process
// starts but its durable PID upgrade or shared-service lease promotion cannot
// finish. It retains a pre-start reservation or upgraded record, the startup
// admission (when the coordinator is still open), and a retriable Launcher
// owner until a true terminal observation. It is not a Control permit and
// grants no writes.
type externalCleanupClaim struct {
	sessionID        string
	admission        *remote.SharedLaunchAdmission
	lifecycle        externalLauncherPort
	record           cleanupstore.Record
	reservation      cleanupstore.Reservation
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
	// defaultExternalShutdownCleanupBudget is one wall-clock budget shared by
	// handoff observation and StopAll. It bounds only external ownership cleanup;
	// every unrecovered item is returned as a typed report.
	defaultExternalShutdownCleanupBudget = 2 * time.Second
)

type persistentLoadState struct {
	initialized    bool
	configLoaded   bool
	secretsLoaded  bool
	pathsLoaded    bool
	settingsLoaded bool
}

// App 主应用结构体，负责跨服务协调和生命周期管理。
// 通过 Wails 绑定暴露给前端。
type App struct {
	ctx     context.Context
	aiProxy *aiproxy.Service
	// agentCLI 是 agent-CLI 本地命令服务（DESIGN.md §5 A1，独立回环
	// listener/随机端口/端点文件发现）。构造/启停接线在 agentcli_wiring.go；
	// 前端门面在 app_agentcli.go。小写不导出：不进 Wails 绑定，只能经门面。
	agentCLI *agentcli.Server
	// agentCLIInstall 管理 agent-CLI 本机分发（shim 与 agent 技能，DESIGN.md
	// §5 C1/C2）。构造与 Startup 默认安装在 app_agentcli_install.go；前端门面
	// 同文件。小写不导出：不进 Wails 绑定。
	agentCLIInstall *install.Manager

	Config          *config.ConfigService
	Secrets         *secrets.SecretsService
	Launcher        *launcher.LauncherService
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
	OpenCodeConfig  *opencodeconfig.Service
	PiConfig        *piconfig.Service
	OmpConfig       *ompconfig.Service
	AgentProfiles   *agentprofile.Service
	EnvCheck        *envcheck.Service
	Usage           *usage.Service
	// SharedSkills 是共用技能管理服务（~/.agents/skills ⇄ skills-disabled 的
	// 枚举/启停/删除，扩展中心重构 spec §2.1）。**非 Wails 绑定**——经 App
	// 门面（app_sharedskills.go）暴露，managed 标记在门面层组装。
	SharedSkills *sharedskills.Service
	// SharedMcp 是共用 MCP 登记与分发服务（~/.agents/.mcp.json 条目 CRUD +
	// Claude/Codex/OpenCode/Pi 四引擎写入，共用 MCP spec §2）。**非 Wails
	// 绑定**——经 App 门面（app_sharedmcp.go）暴露。
	SharedMcp *sharedmcp.Service
	// SharedMarkets 是共用插件市场服务（~/.agents/marketplaces 克隆管理 +
	// 插件技能部署到 ~/.agents/skills）。**非 Wails 绑定**——经 App 门面
	// （app_sharedmarket.go）暴露。
	SharedMarkets *sharedmarket.Service
	// WebUI 是 pi Web UI 壳集成的发现/探测服务（蓝图 T-1.5，契约 v1.0.2）。
	// 仅 embedded pi 会话注册；其他 CLI 不感知。
	WebUI *webui.Service
	// ClaudeWebUI 是 Claude Code 会话的进程内 Web 数据面管理器（claude-webui
	// design §5.2/§5.3，2026-11）：per-session 回环 server + 转录尾随 + 双车道
	// 输入端口（claudewebui.Manager，起停助手见 app_claudewebui.go）。
	// **非 Wails 绑定**——禁进 bind_list.go（design §10：Bind 清单冻结 21 项）。
	ClaudeWebUI *claudewebui.Manager
	// Skins 是本地图片皮肤管理服务（皮肤图片库 ~/.amagi-codebox/skins +
	// /skins/ 只读资源 Handler）。Startup 注入 Wails ctx 后才能弹选择对话框。
	Skins *skins.Service
	// GitAssist 是 AI 辅助 Git 提交/推送服务（终端会话工作区的分支切换/提交/
	// 推送 + 内置模型生成提交信息）。apiKeyResolver 由 Secrets 适配注入。
	GitAssist *gitassist.Service
	// sharedExt 是公共扩展中心服务（扩展管理页「共用」：公共 skill / 公共 MCP
	// 仓库、按需分发与聚合视图，internal/sharedext）。bind_list 直接绑定服务
	// 本体供简单 CRUD；需要 App 上下文的门面（选择对话框 / provider 聚合 /
	// MCP 导入信封）集中在文件尾 Shared Extensions 段。
	sharedExt *sharedext.Service
	// sharedExtAggForce 聚合视图强制刷新标记：InvalidateSharedExtCache 置位，
	// 下一次 GetAllSharedSkillsAggregated 消费并旁路 sharedext 的 30s TTL
	// 缓存（P4 在插件更新/卸载后调用——插件侧变化不经过 registry 写路径，
	// 不会触发 sharedext 内建的缓存失效）。
	sharedExtAggForce atomic.Bool
	// modalityProbeInFlight 多模态探测在飞去重（key: provider/model）。
	modalityProbeInFlight sync.Map
	// quotaProbeFlights 额度探测单飞表（key: provider 名）。与 modality 的
	// sync.Map 「在飞即丢弃」不同：额度探测调用方都依赖返回值，重复调用阻塞
	// 等待首个在飞探测的结果复用（见 app_quota_probe.go）。
	quotaProbeMu      sync.Mutex
	quotaProbeFlights map[string]*quotaProbeFlight
	// quotaCodexSessionsRoot codex 订阅会话根目录覆盖（空 = 运行时用
	// os.UserHomeDir 解析 ~/.codex/sessions）；单测注入隔离真实家目录用。
	quotaCodexSessionsRoot string

	// RemoteClient 域（桌面端互联蓝图 §7）：作为 v1 契约客户端连接另一台
	// amagi-codebox 宿主。登记簿在 NewApp 构造时从 configDir/remote-hosts.json
	// 装载（纯文件读）；凭据存储与配对服务在 Startup 补齐（复用 App.Secrets，
	// DPAPI/Keychain 条目 codebox-remoteclient/<DeviceID>）；当前至多一条已连接
	// 宿主（多主机并发连接后置，蓝图 §13）。退出不强制断连。绑定方法见
	// app_remoteclient.go。
	rcMu       sync.Mutex
	rcRegistry *remoteclient.HostRegistry
	rcCreds    remoteclient.CredentialStore
	rcPairing  *remoteclient.PairingService
	// rcLegacy 是 legacy 配置面服务（WD-5 过渡方案，RC4-1）：per-host 可选
	// legacy token（Keychain 条目 codebox-remoteclient/<DeviceID>/legacy）。
	rcLegacy *remoteclient.LegacyConfigService
	rcConn   *remoteClientConnection
	// rcTerminals 是当前已连接宿主的终端长连接管理器（RC2：/ws/v1 终端
	// 域，随连接替换/断开/撤销整体 DetachAll）。
	rcTerminals *remoteclient.TerminalManager

	// Control runtime (M3-A2): the gate authority for all session write side
	// effects. Raw ports (e.g. Pty) sit BEHIND it and are never
	// Wails-bound (design §4.1, §6.3 C-01).
	control           *remote.ControlRuntime
	sessionAdapter    *remote.RemoteSessionAdapter
	sharedCoord       *remote.SharedServiceCoordinator
	processRegistry   *processcap.Registry
	remoteDefaults    *launchplan.DefaultStore
	remotePlanner     launchplan.Planner
	compensationDebts *launchplan.CompensationDebtRegistry

	// sharedLeases is keyed by the exact {sessionID, runEpoch, kind} owner. It is
	// updated inside the coordinator's composite transfer critical section.
	sharedLeaseMu sync.Mutex
	sharedLeases  map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease
	// sharedLeaseReleases is the per-session whole-release completion barrier
	// (stop-lease fix): the gate channel of the in-flight releaseSharedLeases
	// round for a SessionID, closed only after that round's durable completion
	// attempt AND coordinator ReleaseExact have finished. Guarded by sharedLeaseMu;
	// never held across the durable write so no global lock waits on disk.
	sharedLeaseReleases map[string]chan struct{}

	// externalLauncher is a narrow test seam; nil selects the production
	// LauncherService. externalRunPollInterval is zero in production (1s).
	externalLauncher        externalLauncherPort
	externalRunPollInterval time.Duration

	// externalCleanups owns post-start processes whose durable identity upgrade
	// or shared-service lease promotion failed and whose first compensating Stop
	// could not prove terminal. Each exact claim remains until explicit
	// Stop/reaper terminal; pre-start reservations survive App exit and Shutdown
	// reports every bounded abandonment rather than silently forgetting ownership.
	externalOwnershipMu     sync.Mutex
	externalShutdown        atomic.Bool
	externalStartGeneration atomic.Uint64

	externalCleanupMu                     sync.Mutex
	externalCleanups                      map[string]*externalCleanupClaim
	externalDurableRuns                   map[string]cleanupstore.Record
	externalCleanupStore                  cleanupstore.Store
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

	// WSLSetup 面向 Windows：向 WSL 发行版内安装受管 CLI（原生 Node + npm i -g）。
	// 非 Windows 平台为 no-op 服务，方法返回 unsupported。
	WSLSetup *wslsetup.Service

	// WSLSupply 是 WSL agent 配置供给协调器（P3-S0）：桌面闸门与 remote/v1
	// effect 的统一凭据/托管配置执行边界（判定语义零变化，只统一执行边界）。
	// 测试可替换为假 Runner；nil 时闸门 fail closed。
	WSLSupply wslsupply.Runner

	// wslUnifiedLoop 是 WSL 统一管理的后台对账循环与延期补推标记（P4 修复
	// F1/F2：来源周期重扫 + 活动会话阻挡后的自动补推；见 app_wsl_unified.go）。
	wslUnifiedLoop wslUnifiedReconcileLoopState

	// DockerWSL 面向 Windows：Docker Desktop ↔ WSL 集成健康检查与一键自愈
	//（2026-09-25 运维复盘 §1.2：共享挂载竞态挂坏的实证修复序列固化）。
	// 非 Windows 平台方法返回不支持。
	DockerWSL *dockerwsl.Service

	Capabilities platform.PlatformCapabilities
	CLIResolver  platform.CLIResolver
	FileOpener   platform.FileOpener

	// startupWarnings 记录启动期间的警告信息，供前端拉取后向用户展示。
	startupWarnings   []string
	startupWarningsMu sync.Mutex

	// lastRemoteStartError 记录最近一次 Startup 恢复路径的远程 Start 失败
	//（P3-B③ 漂移可观察）：enabled 持久化保持开启而 running=false 的窗口由此
	// 变得可判定。任何一次成功 Start（含重试自愈与手动 Toggle）都会清空；
	// 经 GetRemoteStatus.lastStartError 透出供前端分类（如端口占用）。
	lastRemoteStartErrMu sync.Mutex
	lastRemoteStartError string

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

	// portableConfigMu serializes complete export/import dialogs and their
	// cross-service snapshots. Individual services remain independently locked;
	// this prevents two Wails transfer requests from interleaving their
	// replacement/rollback sequences.
	portableConfigMu sync.Mutex

	// providerSyncMu serializes reconciliation of CodeBox-managed provider
	// entries across OpenCode, Pi and OMP. providerSyncEnabled is set only by
	// NewApp; minimal test Apps opt in explicitly with isolated harness paths.
	providerSyncMu      sync.Mutex
	providerSyncEnabled bool
	providerSyncPiDir   string
	providerSyncOmpDir  string

	// Claude 订阅账号轮换（design §2/§6.3/§7.2）：未导出领域服务 + 启动门禁
	// 缓存。claudeAccountsPool 为池快照（nil = 无池，未启用）；loadErr 非空 =
	// 池不可用（blocked）；recovered 在 Startup 恢复完成后置位（§4.5 时序）。
	claudeAccounts   *claudeaccounts.Service
	claudeAccountsMu sync.RWMutex
	// 发布序列锁只覆盖 PoolStore.Load→标记→缓存发布；不得在此锁内取 Service 锁。
	claudeAccountsPublishMu sync.Mutex
	// 隔离测试的语义屏障：claim 已取得、认证/进程副作用尚未发生。
	claudeCredentialsLaunchCheckpoint func() error
	claudeAccountsRecoveryCheckpoint  func()
	claudeAccountsPool                *claudeaccounts.AccountPool
	claudeAccountsLoadErr             error
	claudeAccountsUnsupported         bool
	claudeAccountsRecovered           bool
	// claudeAccountsPoolStore 是池存储直接引用（F01：mutation 释放前发布权威
	// 状态不经 Service 串行锁；F10：「曾初始化」标记判定）。
	claudeAccountsPoolStore claudeaccounts.PoolStore
	// claudeAccountsPoolMarkerPath 是不含秘密的「池曾初始化」标记文件路径
	//（F10；测试可注入）。
	claudeAccountsPoolMarkerPath string
	// nil 使用 File.Sync；测试在真实创建后注入文件/目录同步失败。
	claudeAccountsMarkerSync func(*os.File) error
	// claudeAccountsQuiescenceLister 是进程观察器枚举接缝（nil = 生产 ps）。
	claudeAccountsQuiescenceLister claudeProcessLister
	// operation 注册表（有界缓存 + requestId 幂等，见 claude_accounts_facade.go）。
	claudeAccountsOpsMu    sync.Mutex
	claudeAccountsOps      map[string]*claudeAccountOperation
	claudeAccountsOpsOrder []string
	claudeAccountsRequests map[string]string
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

	envCheckSvc := envcheck.NewServiceWithRunner(processRunner)

	settingsSvc := settings.NewService(configDir)

	app := &App{
		configDir:       configDir,
		aiProxy:         aiproxy.New(filepath.Join(configDir, "ai-proxy")),
		Config:          config.NewConfigService(configDir),
		Secrets:         secrets.NewSecretsService(configDir),
		Launcher:        launcher.NewLauncherService(log, envVarsSvc),
		Tray:            tray.NewService(),
		Sessions:        session.NewManager(),
		Paths:           paths.NewPathsService(configDir),
		Log:             log,
		Pty:             pty.NewService(log),
		Settings:        settingsSvc,
		EnvVars:         envVarsSvc,
		Updater:         updater.NewService(resolveAppVersion(), log),
		Plugins:         pluginsSvc,
		CodexPlugins:    codexPluginsSvc,
		OpenCodePlugins: openCodePluginsSvc,
		PiPlugins:       piPluginsSvc,
		OmpPlugins:      ompPluginsSvc,
		OpenCodeConfig:  opencodeconfig.NewService(),
		PiConfig:        piconfig.NewService(),
		OmpConfig:       ompconfig.NewService(),
		AgentProfiles:   agentprofile.NewService(),
		EnvCheck:        envCheckSvc,
		Usage:           usage.NewService(configDir, log),
		WebUI:           webui.NewService(log, filepath.Join(defaultPiAgentDir(), "amagi", "webui-registry")),
		// Claude Code Web 平面（design §5.3）：per-session 数据面管理器，非 Wails
		// 绑定；nil 安全（测试构造可省略，相关助手零操作）。
		ClaudeWebUI:         claudewebui.NewManager(log),
		Capabilities:        capabilities,
		CLIResolver:         platform.NewCLIResolver(capabilities),
		FileOpener:          platform.NewFileOpener(processRunner),
		providerSyncEnabled: true,
		providerSyncPiDir:   defaultPiAgentDir(),
		providerSyncOmpDir:  defaultOmpAgentDir(),
		processRegistry:     processcap.NewRegistry(),
		remotePlanner:       launchplan.NewFailClosedPlanner(),
		compensationDebts:   launchplan.NewCompensationDebtRegistry(),
		WSLSetup:            wslsetup.NewService(log),
		DockerWSL:           dockerwsl.NewService(log),
		// 公共扩展中心：仓库根 = <configDir>/shared（与 Config/Skins 等同根）。
		sharedExt: sharedext.NewService(configDir, log),
	}
	// P4 插件生命周期联动：四引擎插件服务在成功的写操作（install/uninstall/
	// update/启停）后回调，置位共用中心聚合的强制刷新标记——下次聚合拉取
	// （App.GetAllSharedSkillsAggregated）旁路 30s TTL 重建。Claude 引擎的
	// internal/plugin 不在 P4 写面内：其插件变更依赖 30s TTL + 面板切 tab 重挂
	// 时重新拉取兜底（ExtensionsView 各 pane 为 v-if，切换即重载）。
	invalidateShared := app.InvalidateSharedExtCache
	codexPluginsSvc.OnChange = invalidateShared
	openCodePluginsSvc.OnChange = invalidateShared
	piPluginsSvc.OnChange = invalidateShared
	ompPluginsSvc.OnChange = invalidateShared
	app.Skins = skins.NewServiceWithContext(settingsSvc, filepath.Join(configDir, "skins"), log, app.wailsContext)
	// WSL 供给协调器携带 shared 小文件与 S4 键级投影适配器（Pi 的供给单元已随
	// 1.3.126「WSL 内 Pi 共享 Windows agent 根」退役）；业务快照源闭包引用
	// app，在字面量之后装配。
	app.WSLSupply = newWSLSupplyCoordinator(app, configDir)
	// agent-CLI 本地命令服务（DESIGN.md §5 A1）：命令表 + host Handler 驱动；
	// Startup 按设置决定是否启动（agentcli_wiring.go）。
	app.constructAgentCLI()
	// agent-CLI 本机分发（DESIGN.md §5 C2）：shim 与技能安装管理器；Startup
	// 默认安装见 app_agentcli_install.go。
	app.constructAgentCLIInstall()
	// 共用技能服务（扩展中心重构 spec §2.1）：~/.agents/skills ⇄
	// skills-disabled 的枚举/启停/删除；前端门面在 app_sharedskills.go。
	app.constructSharedSkills()
	// 共用 MCP 服务（共用 MCP spec §2）：~/.agents/.mcp.json 登记 CRUD +
	// 四引擎分发；前端门面在 app_sharedmcp.go。
	app.constructSharedMcp()
	// 共用插件市场服务：~/.agents/marketplaces 克隆管理 + 插件技能部署到
	// ~/.agents/skills；前端门面在 app_sharedmarket.go。
	app.constructSharedMarkets()
	// Claude 订阅账号轮换（design §6.3/§7.2）：装配 1a 领域服务（Keychain 池
	// 后端、canonical 原生适配器、MutationGate/Quiescence 生产实现）。启动
	// 恢复在 Startup（§4.5 时序：池加载 → RecoverAtStartup → 开放受影响启动）。
	app.claudeAccounts = newClaudeAccountsService(app)
	// Remote 先以默认端口 8680 初始化；Startup 加载 Settings 后会同步持久化的端口。
	// HostSummary 与 remote create 共用同一个 launchplan.Planner；当前五类
	// desktop-equivalent builder 未接线，因此两条路径一致地 fail closed。
	app.Remote = remote.NewServerWithSecurity(8680, app.remoteAdapter(), log, mobileAssets,
		remote.NewProductionSecurityOptions(configDir, app.buildRemoteHostSummary))
	// 方案 P：注入用户主目录，供 List 回填已退出 claudecode 会话的标题（直读历史 jsonl）。
	// 获取失败时记日志但不阻塞启动（标题功能降级，不影响会话启动主流程）。
	if home, homeErr := os.UserHomeDir(); homeErr != nil {
		log.Warn("session", "注入 homeDir 失败：已退出会话标题回填将跳过", "err="+homeErr.Error())
	} else {
		app.Sessions.SetHomeDir(home)
	}
	// amagi-media-understanding 导出（契约 docs/vision-export-contract.md §2）：注入 provider → 明文
	// API key 解析器（SecretsService.GetAPIKey 适配为单返回值签名），ConfigService 在
	// preset/provider 增删与配置加载完成后懒导出 ~/.agents/amagi-media-models.json。
	app.Config.SetAPIKeyResolver(func(provider string) string {
		key, err := app.Secrets.GetAPIKey(provider)
		if err != nil || key == "" {
			return ""
		}
		return key
	})
	// 密钥库就绪门禁（密钥面安全）：启动顺序是 Config.Load（尾部即触发导出）先于
	// Secrets.Load，若不门禁，首次导出会用未加载的空密钥缓存把 ~/.agents/
	// amagi-media-models.json 覆盖成无 api_key 版本。注入 Ready 探针后，未就绪
	// （未加载/加载中/上次加载失败）一律跳过本轮写盘；“未就绪”≠“无密钥”，绝不
	// 降级导出；密钥库就绪后的首次成功导出由 Startup 里 Secrets.Load 成功后的
	// ReexportVisionModels 补发。
	// 并发不变量：Secrets.Load 全进程仅在 Startup 顺序执行一次（且 Load 期间 cache
	// 保留旧值直到原子交换），因此探针门禁与导出间不存在并发 reload 窗口；若未来
	// 引入并发/热重载 Load，须改为一次固定 Snapshot 内完成就绪判定+取 key。
	app.Config.SetAPIKeyReadyProbe(app.Secrets.Ready)
	// 多模态实弹探测（契约 v1.2）：预设/服务商保存与配置加载后，对静态知识库
	// 未知的模型自动实弹探测能力并落缓存，供视觉导出与 pi/omp input 声明。
	app.wireModalityProber()
	// 登录预设 contextWindow 同步（pi 登录模型窗口覆写）：预设保存/删除后把
	// providers.<provider>.modelOverrides.<model>.contextWindow 深合并进 canonical
	// root（~/.pi/agent）的 models.json——Leader 与 amagi 子 agent（PI_CODING_AGENT_DIR
	// =canonical root 透传）读同一份生效。**reconcile 语义（非事件值直写）**：事件
	// 只作触发信号，落盘值以 ConfigService 当前权威状态为准——重读该 provider+model
	// 的存活预设，无预设 → 清除覆写（恢复内置窗口），有 → 写其窗口（Save 的
	// 全组对齐保证同模型多预设窗口一致，取首个即权威值）。锁外回调乱序落盘时自然收敛到 config 终值，
	// 不依赖事件接序。写入与后台 provider sync 共享 providerSyncMu（同一 models.json
	// 读改写）。失败仅告警不阻断配置保存；启动链（LaunchPiSession 登录分支）另有
	// 兑底。锁序：回调由 ConfigService 在写锁释放后触发，内部 providerSyncMu →
	// Config 读锁（GetPiLoginPresets）——与后台 sync 路径同序，不构成环。
	app.Config.SetPiLoginPresetChangeListener(func(saved, removed *config.PiLoginPreset) {
		trigger := saved
		if trigger == nil {
			trigger = removed
		}
		if trigger == nil {
			return
		}
		app.providerSyncMu.Lock()
		defer app.providerSyncMu.Unlock()
		// 重读权威状态（不信任事件携带的旧值）：该 provider+model 当前存活预设。
		presets, err := app.Config.GetPiLoginPresets()
		if err != nil {
			app.Log.Warn("pi", "登录预设 contextWindow 同步跳过（配置不可读）", err.Error())
			return
		}
		cw, maxTokens := 0, 0 // 无存活预设 → 清除窗口与输出覆写
		for _, p := range presets {
			if p.Provider == trigger.Provider && p.Model == trigger.Model {
				cw, maxTokens = p.ContextWindow, p.MaxTokens
				break // 全组对齐保证一致，取首个即权威值
			}
		}
		agentDir, dirErr := piconfig.StandardAgentDir()
		if dirErr != nil {
			app.Log.Warn("pi", "登录预设 contextWindow 同步跳过（canonical root 不可用）", dirErr.Error())
			return
		}
		if err := launcher.UpsertPiLoginModelOverride(agentDir, trigger.Provider, trigger.Model, cw, maxTokens); err != nil {
			app.Log.Warn("pi", "登录预设 contextWindow 覆写同步失败", fmt.Sprintf("provider=%s model=%s cw=%d: %v", trigger.Provider, trigger.Model, cw, err))
		}
	})
	// AI 辅助 Git 提交/推送（gitassist）：与视觉导出同样的 keychain 适配模式，
	// GetAPIKey (string,error) → func(string)string，拿不到 key 时由调用方报错。
	app.aiProxy.SetProviderSource(func() []aiproxy.Provider {
		return aiproxy.ProvidersFromConfig(app.Config.GetProviders())
	})
	app.Launcher.SetNetworkEnv(app.aiProxy.ApplyEnv)
	app.GitAssist = gitassist.NewWithHTTPClient(app.Config, app.Settings, func(provider string) string {
		key, err := app.Secrets.GetAPIKey(provider)
		if err != nil || key == "" {
			return ""
		}
		return key
	}, app.aiProxyHTTPClient())

	// fd/ripgrep 搜索工具安装回调（install_wsl_search_tools 修复动作）：把
	// wslsetup.InstallSearchTools 的结果适配成 envcheck.InstallResult 形状。
	envCheckSvc.SetWSLSearchToolsInstaller(func() (*envcheck.InstallResult, error) {
		res, err := app.WSLSetup.InstallSearchTools()
		if res == nil {
			return nil, err
		}
		return &envcheck.InstallResult{Success: res.Success, Message: res.Message, Error: res.Error}, err
	})

	// P2-C 安装/升级双侧联动：Windows 侧 install/update 成功后，同一操作内
	// 同步 WSL 侧 CLI（门控/分派见 app_wsl_cli_sync.go）。
	envCheckSvc.SetWSLCLISync(app.syncCLIToWSL)

	// M3-A2: control runtime + shared-service coordinator. The PTY raw port and
	// Wails context are injected at Startup (they require the Wails ctx). The
	// arbiter/gate/projector are ready immediately; MarkReady is called in
	// Startup after all wiring is complete.
	app.control = remote.NewControlRuntime(remote.NewSystemClock(), log)
	app.sharedCoord = remote.NewSharedServiceCoordinator()
	// remote-webui-plane：run-scoped PTY 退出（remote v1 创建/重启会话的进程
	// 退出径）先把 webui tracker 落 ended，再释放该 run 代的共享租约。桌面
	// embedded 会话另有各自的退出 goroutine 调 Invalidate，此处幂等。
	// claude Web 平面（design §5.3-4）：同 commit 点收掉进程内数据面 server
	//（Stop + Invalidate，幂等；三处 commit 点全覆盖防 listener 泄漏，R9）。
	app.control.Projector().SetRunTerminalCleanup(func(sessionID string, runEpoch uint64) {
		app.stopClaudeWebPlane(sessionID)
		if app.WebUI != nil {
			app.WebUI.Invalidate(sessionID)
		}
		app.releaseSharedLeasesExact(sessionID, runEpoch)
	})
	if defaults, defaultsErr := launchplan.NewDefaultStore(app.Settings); defaultsErr != nil {
		log.Warn("session", "remote launch defaults 不可用，远程创建保持关闭", defaultsErr.Error())
	} else {
		app.remoteDefaults = defaults
		homeDir := ""
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			homeDir = home
		}
		// Production planner: resolves desktop defaults into canonical Plans.
		// Falls back to FailClosedPlanner only if essential services are missing.
		if app.remoteDefaults != nil && app.CLIResolver != nil {
			app.remotePlanner = newAppLaunchPlanner(
				app.Config, app.Secrets, app.remoteDefaults, app.CLIResolver,
				app.Capabilities, app.Paths, app.EnvVars, homeDir,
			)
			app.remotePlanner.(*appLaunchPlanner).networkEnv = app.aiProxy.ApplyEnv
			// remote/restart 计划链的 WSL 环境防线可观测性：与桌面
			// resolveEmbeddedLaunchSpec 同文案记 WARN/INFO（判定在
			// appLaunchPlanner.resolveLaunch，此处只接日志面）。
			app.remotePlanner.(*appLaunchPlanner).envObserver = func(level, message, detail string) {
				if level == "WARN" {
					app.Log.Warn("session", message, detail)
					return
				}
				app.Log.Info("session", message, detail)
			}
			// 凭据 admission 计划接缝（design §6.3）：纯读计划携带凭据 admission
			// 描述；池 blocked（needs-reconcile/损坏/未结事务）时计划构建失败，
			// remote create/restart 的受影响入口同步关闭。
			app.remotePlanner.(*appLaunchPlanner).claudeCredentialAdmission = app.claudeCredentialsPlanAdmission
		}
	}
	app.sharedLeases = make(map[remote.SharedLeaseOwnerKey]*remote.SharedDependencyLease)
	app.externalCleanups = make(map[string]*externalCleanupClaim)
	app.externalDurableRuns = make(map[string]cleanupstore.Record)
	app.externalCleanupStore = cleanupstore.NewFileStore(configDir)
	// Wire the unbound PTY bridge adapter (design §8.6.3): legacy/v1 WS reaches
	// PTY callbacks through this adapter, not through Wails-bound App methods.
	app.Remote.SetPtyBridge(ptyBridgeAdapter{app: app})

	// Production session membership is Manager-only. Remote create and restart
	// remain fail-closed until the five desktop-equivalent planners and executor
	// are implemented; neither a binary-only resolver nor a raw PTY starter is
	// injected into this adapter.
	m2aStreams := remote.NewSessionStreamStore()
	m2aJournal := remote.NewSessionOperationJournal(configDir)
	m2aSessRaw := newAppSessionRaw(app)
	m2aAdapter := remote.NewRemoteSessionAdapter(
		app.control.Gate(), app.control, nil, m2aStreams, m2aJournal,
		remote.NewNoopRemoteLaunchResolver(), nil, m2aSessRaw, remote.NewSystemClock(), configDir,
	)
	m2aAdapter.SetSessionAuthority(app.Sessions, app.processRegistry, app.remotePlanner)
	// remote-webui-plane：v1 remove GC 完成后同步清理 webui tracker（对齐
	// App.RemoveSession 的 removeWebUITracker commit 点），再释放共享租约。
	// claude Web 平面（design §5.3-4）：同 commit 点收掉进程内数据面 server。
	m2aAdapter.SetPostRemoveCleanup(func(sessionID string) {
		app.stopClaudeWebPlane(sessionID)
		if app.WebUI != nil {
			app.WebUI.RemoveSession(sessionID)
		}
		app.releaseSharedLeases(sessionID)
	})
	// Production executor: applies typed Effects for remote create.
	if _, ok := app.remotePlanner.(*appLaunchPlanner); ok {
		exec := newAppLaunchExecutor(launchExecutorDeps{
			pty:            app.Pty,
			sharedCoord:    app.sharedCoord,
			debts:          app.compensationDebts,
			configMu:       &app.providerSyncMu,
			webui:          appWebUILaunchPlane{app: app},
			claudeWebUI:    appClaudeWebUILaunchPlane{app: app},
			claudePlaneLog: claudeWebUILogAdapter{log: log},
			wslSupply:      app.WSLSupply,
			supplyLog:      wslsupplySupplyLog{log: log},
			openCodeLayerPrep: func(ctx context.Context, distro string, cfg map[string]any) (string, error) {
				return app.prepareWSLOpenCodeLayer(distro, cfg)
			},
		})
		m2aAdapter.SetLaunchExecutor(exec, app.sharedCoord)
		m2aAdapter.SetSharedLeaseTransfer(app.prepareSharedLeaseTransfer, app.releaseSharedLeasesExact)
		// 凭据计划新鲜度复核（design §6.3）：启动前代际变更 → 返回重新规划，
		// 不带旧计划放行。
		m2aAdapter.SetClaudeCredentialsPlanCheck(app.claudeCredentialsPlanCheck)
		m2aAdapter.SetClaudeCredentialsAbsenceCheck(app.claudeCredentialsAbsenceCheck)
		m2aAdapter.SetClaudeCredentialsFingerprint(app.claudeCredentialsFingerprint)
		m2aAdapter.SetRestartPtyStart(func(sessionID string, spec any, runHandle any) (processcap.StartEvidence, error) {
			resolved, ok := spec.(platform.ResolvedLaunchSpec)
			if !ok {
				return processcap.StartEvidence{}, fmt.Errorf("restart: invalid spec type")
			}
			return app.Pty.StartResolvedWithRunEvidence(sessionID, resolved, runHandle)
		})
	}
	app.sessionAdapter = m2aAdapter
	app.Remote.SetSessionAdapter(m2aAdapter)

	// 桌面端互联 RC1-5：装载主机登记簿（纯文件读，不碰 Keychain；凭据存储
	// 与配对服务在 Startup 补齐，见 initRemoteClientServices）。
	app.initRemoteClientRegistry(configDir)

	return app
}

// appWebUILaunchPlane 把 internal/webui 适配为 launch executor 的
// webuiLaunchPlane 接线（remote-webui-plane）：remote v1 创建/重启的
// embedded pi 会话与桌面 LaunchPiSession T-1.5 同源——注入
// AMAGI_WEBUI_PORT/AMAGI_WEBUI_TOKEN env 并在 commit 后注册 tracker。
type appWebUILaunchPlane struct{ app *App }

// PrepareEnv 分配空闲端口与 v1.0.2 capability token。任一失败返回零值
// 不阻断启动（与桌面口径一致：扩展自选端口写注册表，探测侧回退发现）。
func (p appWebUILaunchPlane) PrepareEnv() (int, string) {
	port := 0
	if v, err := webui.AllocateFreePort(); err == nil {
		port = v
	} else if p.app.Log != nil {
		p.app.Log.Warn("webui", "webui 空闲端口分配失败，走注册表回退发现", err.Error())
	}
	token := ""
	if t, err := webui.GenerateToken(); err == nil {
		token = t
	} else if p.app.Log != nil {
		p.app.Log.Warn("webui", "webui capability token 生成失败，探测不带 token", err.Error())
	}
	return port, token
}

// RegisterSessionWithWindow 发布 per-session webui 探测 tracker（服务未接线时
// 静默跳过，如测试构造）。window<=0 取服务默认窗口；WSL 目标应传
// webui.DefaultWSLProbingWindow（WSL+DrvFS 下 pi 扩展就绪实测送慢于默认窗口）。
func (p appWebUILaunchPlane) RegisterSessionWithWindow(sessionID string, pid, port int, token string, window time.Duration) {
	if p.app.WebUI != nil {
		p.app.WebUI.RegisterSessionWithWindow(sessionID, pid, port, token, window)
	}
}

// appClaudeWebUILaunchPlane 把 App 的 claude Web 平面接线适配为 launch
// executor 的 claudeWebUILaunchPlane 窄接口（claude-webui design §5.4）：
// remote v1 创建/重启的 embedded claude 会话在 PTY commit 后经此启动进程内
// 数据面 server、记录 --session-id 映射并注册探测 tracker。全部方法 nil
// 安全（服务未接线的测试构造零操作/返回错误，不 panic）。
type appClaudeWebUILaunchPlane struct{ app *App }

// Start 启动 per-session 回环数据面 server，InputPort 桥 t3 的双车道适配器
// appClaudeInputPort（桌面车道 take-first / 设备平面车道无夺权，design
// §5.3/§5.6）。ClaudeWebUI 未接线时返回错误（调用方仅记日志，会话照常）。
func (p appClaudeWebUILaunchPlane) Start(cfg claudewebui.PlaneConfig) (int, error) {
	if p.app == nil || p.app.ClaudeWebUI == nil {
		return 0, errors.New("claude webui manager unavailable")
	}
	return p.app.ClaudeWebUI.Start(cfg, appClaudeInputPort{a: p.app})
}

// RecordClaudeSessionID 把 --session-id 注入值写入 session.Manager（与桌面
// LaunchSession app.go:2793 同一落点：tracker 优先读锁定的 jsonl，列表直读
// 历史 jsonl 的语义同步受益）。
func (p appClaudeWebUILaunchPlane) RecordClaudeSessionID(sessionID, claudeSessionID string) {
	if p.app == nil || p.app.Sessions == nil {
		return
	}
	p.app.Sessions.SetClaudeSessionID(sessionID, claudeSessionID)
}

// RegisterTracker 发布 webui.Service 探测 tracker（pi 与 claudecode 共用
// 同一状态机，design §4-D3；注册端口用 Start 返回的 boundPort）。
func (p appClaudeWebUILaunchPlane) RegisterTracker(sessionID string, pid, port int, token string) {
	if p.app == nil || p.app.WebUI == nil {
		return
	}
	p.app.WebUI.RegisterSession(sessionID, pid, port, token)
}

// claudeWebUILogAdapter 把 logging.Service 适配为 launch executor 的
// claudePlaneLog 最小日志接缝（nil 安全）。
type claudeWebUILogAdapter struct{ log *logging.Service }

func (a claudeWebUILogAdapter) Warn(scope, msg, detail string) {
	if a.log != nil {
		a.log.Warn(scope, msg, detail)
	}
}

func (a *App) wailsContext() context.Context { return a.ctx }

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

// Startup Wails 生命周期钩子：应用启动时加载配置和密钥。
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	// Recover durable post-start cleanup ownership before any startup restore or
	// user mutation can touch shared services. A recovered admission
	// fail-closes the fresh coordinator until the identity-verified process
	// reaches terminal.
	if err := a.recoverExternalCleanups(); err != nil {
		a.addStartupWarning("检测到未完成的外部进程清理；请先关闭旧外部终端，再通过恢复确认 API 重新核验并解锁外部进程安全锁定")
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
	if a.aiProxy != nil {
		proxyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := a.aiProxy.Load(proxyCtx); err != nil {
			a.addStartupWarning("AI 代理分流恢复失败：" + err.Error())
		}
		cancel()
	}

	if err := a.Secrets.Load(); err != nil {
		a.Log.Warn("app", "加载密钥失败", err.Error())
	} else {
		loadState.secretsLoaded = true
		a.Log.Info("app", "密钥加载成功")
		// 密钥库就绪后补发视觉模型导出（契约 §2 + 密钥面安全）：启动早期
		// Config.Load 尾部的首轮导出因密钥库未就绪被门禁跳过，由此补上；
		// 同时自愈历史版本遗留的无 key 导出文件。失败仅告警，不阻断启动。
		if err := a.Config.ReexportVisionModels(); err != nil {
			a.Log.Warn("app", "视觉模型导出补发失败", err.Error())
		} else {
			a.Log.Info("app", "视觉模型导出已补发")
		}
	}
	// 桌面端互联 RC1-5：密钥库就绪后补齐远程客户端域（凭据存储 + 配对服务）。
	// 不阻断启动：失败置 nil，相关绑定返回明确错误。
	a.initRemoteClientServices()
	// Claude 订阅账号轮换启动时序（design §4.5/§6.1）：Config/Keychain/池
	// 加载 → RecoverAtStartup → 才开放受影响启动入口。失败只关闭受影响
	// 启动入口并记警告，其他 API 工作流不停用。
	a.startupRecoverClaudeAccounts()
	if loadState.configLoaded && loadState.secretsLoaded {
		if err := a.syncProvidersToHarnesses(); err != nil {
			a.Log.Warn("provider-sync", "启动时同步 OpenCode/Pi/OMP 提供商失败", err.Error())
			a.addStartupWarning("提供商配置未能完整同步到 OpenCode、Pi 或 Oh My Pi；原配置文件已保留，请修复提示中的配置错误后重试保存")
		} else {
			a.Log.Info("provider-sync", "OpenCode/Pi/OMP 提供商配置已同步")
		}
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
	a.setPersistentLoadState(loadState)

	// 使用统计：加载 usage.db，异步触发首次同步并启动后台定时同步。
	if a.Usage != nil {
		if err := a.Usage.Load(); err != nil {
			a.Log.Warn("usage", "使用统计加载失败", err.Error())
		} else {
			a.Log.Info("usage", "使用统计加载成功")
			// 注入应用级 ctx 给 usage.Service（M1：StartBackgroundSync 不再接受 ctx 参数）。
			// Wails v2 仅绑定"方法"，结构体字段（即使导出）不进入 wailsjs 生成路径。
			a.Usage.Ctx = ctx
			go func() {
				if err := a.Usage.SyncAll(); err != nil {
					a.Log.Warn("usage", "首次同步失败", err.Error())
				}
				a.Usage.StartBackgroundSync(5 * time.Minute)
			}()
		}
	}

	// 额度探测（额度查询设计方案 §3）：启动 30s 后延迟自动探测一轮全部
	// provider + codex 订阅条目（成功 10min 冷却；手动路径不受节流）。
	// 需在 Config.Load/Secrets.Load 之后调用（探测要读 provider 表与 key）。
	a.wireQuotaProber()

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

	// 一次性善后：清除旧版本遗留在 ~/.codex/config.toml 的代理标记块，避免
	// 死代理地址继续路由 codex 流量。幂等（无标记/文件不存在即 no-op）；失败仅
	// Warn 不阻断启动。与历史上的启动恢复钩子同位置同时机。
	go a.cleanupCodexLegacyHeadroomMarkerOnStartup()

	// P2-B：WSL 统一配置管理启动对账（统一管理开启时点火后台扫描，随后推一轮
	// 保存后补漏；未开启时与现状行为一致）。
	a.scheduleWSLUnifiedStartupReconcile()

	// 远程 API 仅在用户显式启用后恢复。新安装和没有该设置的旧配置
	// 默认保持 loopback 且不监听，避免无意暴露到局域网。
	// M1-B3c：remote load/start 由迁移 gate 结果守卫。LoadSecurityState 在所有
	// 路径上仅调用一次（迁移路径由 gate 步骤 a 完成；Missing/Current 由此处完成；
	// Future/Manual/失败路径全部跳过）。Start 条件为 remoteEnabled && startAllowed；
	// 回滚成功及一切失败路径永不 Start（design §C.4）。
	a.applyRemoteGateResult(ctx, securityLoaded, startAllowed, remoteEnabled)
	// P3-B③：Startup 恢复路径自愈与 enabled/running 漂移对齐（素材B ⑥）。
	// 初次 Start 失败不再止于日志 Warn：短暂端口占用按固定退避重试自愈；
	// 重试耗尽则保持持久化 enabled 不变，改为可观察（启动警告 + 状态绑定
	// lastStartError/enabled/running），自检卡可据此分类提示。
	a.healRemoteStartupRestore(ctx, remoteEnabled, startAllowed)

	// agent-CLI 本地命令服务（DESIGN.md §3.7）：默认开启；关闭时仅清理陈旧
	// 端点文件不监听。启动失败记警告不阻断主流程。
	a.startAgentCLI()

	// agent-CLI 本机分发（DESIGN.md §5 C2）：默认安装 shim 与 agents 技能
	// （用户清理过的面不装回），每次启动刷新 shim 指向当前可执行文件。
	// 失败只记日志不阻断启动。
	a.installAgentCLIDefaults()

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

	// P4-F2：先收敛 WSL 统一管理后台循环（等待退出；之后不再有循环侧
	// 推送调度，防 goroutine 泄漏）。
	a.stopWSLUnifiedReconcileLoop()

	// RC2：先收尾远程客户端终端长连接（等待读泵退出与 outbox 销毁；
	// 不依赖网络往返，仅本地关闭）。
	a.shutdownRemoteClientTerminals()

	// agent-CLI（A1）：先于会话控制栅栏收口本地命令服务——停止监听、令牌
	// 作废并撤销端点文件（pid 所有权校验），后续不再接受新命令。
	a.stopAgentCLI()

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
	if err := a.saveAllConfig(); err != nil {
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
	// 后续收尾见 Shutdown 尾部。
	// External Launcher processes were already handled by the bounded ownership
	// phase immediately after the authority fence. Embedded PTYs close here.
	a.Pty.CloseAll()
	if a.aiProxy != nil {
		if err := a.aiProxy.Close(); err != nil {
			a.Log.Warn("ai-proxy", "停止 AI 代理内核失败", err.Error())
		}
	}
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

// markWSLSupplyDirty 在 providerSync/配置保存事件后向 WSL 供给状态发布来源
// revision（P3-S0 空实现位：只落 desired revision，不探测不调度；S1 接真实
// 分发）。失败仅告警，绝不阻塞宿主保存——「宿主保存成功」与「WSL 已同步」
// 是两个结果。
func (a *App) markWSLSupplyDirty(source string) {
	if a == nil || a.WSLSupply == nil {
		return
	}
	if err := a.WSLSupply.MarkDirty(source, uint64(time.Now().UnixNano())); err != nil {
		a.Log.Warn("wslsupply", "标记 WSL 供给来源变更失败（不阻塞宿主保存）", err.Error())
		return
	}
	// P3-S1 真实调度：标脏后异步推送一次（仅对已运行且无活动会话的目标；
	// 停机 distro 只保留待同步标记，不唤醒）。
	a.scheduleWSLSupplyPush()
}

// wslsupplySupplyLog 把 logging.Service 适配为 wslsupply.Logger（三参固定形态）。
type wslsupplySupplyLog struct{ log *logging.Service }

func (a wslsupplySupplyLog) Info(scope, msg, detail string) {
	if a.log != nil {
		a.log.Info(scope, msg, detail)
	}
}

func (a wslsupplySupplyLog) Warn(scope, msg, detail string) {
	if a.log != nil {
		a.log.Warn(scope, msg, detail)
	}
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

// saveAllConfig 保存配置和密钥到磁盘。
func (a *App) saveAllConfig() error {
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

	if !state.initialized || (state.configLoaded && state.secretsLoaded) {
		if err := a.syncProvidersToHarnesses(); err != nil {
			saveErrs = append(saveErrs, fmt.Errorf("sync providers to OpenCode/Pi/OMP: %w", err))
		}
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

// ===== Shared Extensions (共用中心) =====
// 扩展管理页「共用」中心的 App 级门面。简单 CRUD 直接透传 internal/sharedext
//（服务本体另经 bind_list 绑定，与既有 20 个服务同风格）；本段集中需要
// App 上下文的入口：系统选择对话框（需 Wails ctx）、五引擎 provider 组装的
// 聚合视图、Wails v2 单值返回信封，以及 P4 插件更新/卸载后的缓存失效挂点。
// 错误原样透传，不在门面层吞掉或降级（聚合的 provider 组装除外：单引擎失败
// 降级为空并记日志，不让整个聚合失败）。

// ImportSharedMCPResult 是 ImportSharedMCPServers 的单值返回信封。
// Wails v2 绑定对 (value, value, error) 多返回值支持有限，服务层
// ImportMCPServers 的三返回值 (imported, skipped, error) 在此折叠为结构体；
// skipped 非空属于部分成功，前端应同时展示两者而不是仅报错。
type ImportSharedMCPResult struct {
	Imported []sharedext.SharedMCPServer `json:"imported"`
	Skipped  []string                    `json:"skipped"`
}

// readTextFileMaxBytes 限制 ReadTextFile 单次读取大小（预览 MCP JSON 用）。
const readTextFileMaxBytes = 1 << 20 // 1MB

// GetSharedSkills 返回公共 skill 仓库清单（含 Missing 失联标记）。
// 签名无 error（任务契约）：仅 registry 磁盘异常才会失败，此时记日志返回空。
func (a *App) GetSharedSkills() []sharedext.SharedSkill {
	skills, err := a.sharedExt.ListSharedSkills()
	if err != nil {
		a.Log.Warn("sharedext", "读取公共 skill 清单失败", err.Error())
		return nil
	}
	return skills
}

// ImportSharedSkillResult 是 ImportSharedSkillFromZip/Dir 的单值返回信封
//（M3，语义同 ImportSharedMCPResult）：部分成功（同名/非法名跳过）不返回
// error，跳过明细折叠进 Skipped 供前端双 toast；仅整体失败（一个都没导入）
// 才返回 error。
type ImportSharedSkillResult struct {
	Imported []sharedext.SharedSkill `json:"imported"`
	Skipped  []string                `json:"skipped"`
}

// partialSkillImportPrefix 是 internal/sharedext 部分成功错误的固定前缀，
// App 层据此把「已导入 + 部分跳过」折叠为信封（内部方法契约不变）。
const partialSkillImportPrefix = "部分 skill 未导入: "

// wrapSkillImport 把服务层 (imported, error) 双返回值折叠为信封：
// error 为 nil → 信封；部分成功（imported 非空 + 错误）→ 跳过明细从固定
// 前缀后的错误文本提取，信封返回不报错；全失败（imported 空 + 错误）→
// 原错误透传（Wails 契约下前端只拿到 error，此时才应如此）。
func wrapSkillImport(skills []sharedext.SharedSkill, err error) (ImportSharedSkillResult, error) {
	if err == nil {
		return ImportSharedSkillResult{Imported: skills, Skipped: []string{}}, nil
	}
	if len(skills) == 0 {
		return ImportSharedSkillResult{}, err
	}
	detail := strings.TrimPrefix(err.Error(), partialSkillImportPrefix)
	return ImportSharedSkillResult{Imported: skills, Skipped: []string{detail}}, nil
}

// ImportSharedSkillFromZip 从 zip 压缩包导入公共 skill（可含多个）。
// 部分成功不报错：已导入项在 Imported，跳过明细在 Skipped。
func (a *App) ImportSharedSkillFromZip(zipPath string) (ImportSharedSkillResult, error) {
	skills, err := a.sharedExt.ImportSkillFromZip(zipPath)
	return wrapSkillImport(skills, err)
}

// ImportSharedSkillFromDir 从文件夹导入公共 skill（自身含 SKILL.md 视为单个，
// 否则扫描一层子目录）。部分成功语义同 ImportSharedSkillFromZip。
func (a *App) ImportSharedSkillFromDir(dirPath string) (ImportSharedSkillResult, error) {
	skills, err := a.sharedExt.ImportSkillFromDir(dirPath)
	return wrapSkillImport(skills, err)
}

// SetSharedSkillTargets 已在 P6 合并中移除：与远端 app_sharedskills.go 的
// ~/.agents 共用技能门面同名冲突，且该透传冗余（前端 api/sharedExt.ts
// 直接消费 sharedext/Service 绑定）。

// GetSharedMCPServers 返回公共 MCP server 清单。签名无 error（任务契约）：
// 失败时记日志返回空。
func (a *App) GetSharedMCPServers() []sharedext.SharedMCPServer {
	servers, err := a.sharedExt.ListSharedMCPServers()
	if err != nil {
		a.Log.Warn("sharedext", "读取公共 MCP 清单失败", err.Error())
		return nil
	}
	return servers
}

// PreviewSharedMCPJSON 纯解析预览 MCP JSON（不落库），供「粘贴 → 预览 → 勾选
// → ImportSharedMCPServers」两步交互的第一步。
func (a *App) PreviewSharedMCPJSON(text string) ([]sharedext.SharedMCPServer, error) {
	return sharedext.ParseMCPJSON(text)
}

// ImportSharedMCPServers 批量落库公共 MCP server；同名/非法条目进 skipped
// （部分成功不报错），仅 registry 写入失败才返回 error。
func (a *App) ImportSharedMCPServers(servers []sharedext.SharedMCPServer) (ImportSharedMCPResult, error) {
	imported, skipped, err := a.sharedExt.ImportMCPServers(servers)
	if err != nil {
		return ImportSharedMCPResult{}, err
	}
	return ImportSharedMCPResult{Imported: imported, Skipped: skipped}, nil
}

// DeleteSharedMCPServer 删除公共 MCP server（先回收各目标引擎写入点）。
func (a *App) DeleteSharedMCPServer(name string) error {
	return a.sharedExt.DeleteSharedMCPServer(name)
}

// SetSharedMCPServerEnabled 启用/禁用公共 MCP server（启停同步分发/回收）。
func (a *App) SetSharedMCPServerEnabled(name string, enabled bool) error {
	return a.sharedExt.SetSharedMCPServerEnabled(name, enabled)
}

// SetSharedMCPServerTargets 设置公共 MCP server 的目标引擎。
func (a *App) SetSharedMCPServerTargets(name string, targets []sharedext.Engine) error {
	return a.sharedExt.SetSharedMCPServerTargets(name, targets)
}

// GetAllSharedSkillsAggregated 返回聚合 skill 视图（仓库手动条目 + 五引擎
// 插件来源动态合并）。force=true 旁路 sharedext 的 30s TTL 缓存；
// InvalidateSharedExtCache 置位的强制刷新标记在此消费。
func (a *App) GetAllSharedSkillsAggregated(force bool) ([]sharedext.AggregatedSkill, error) {
	if a.sharedExtAggForce.Swap(false) {
		force = true
	}
	// m2：providers 组装（含 codex CLI fork 与五引擎扫描）以惰性构建器
	// 传入，缓存命中时不再执行——原无条件组装使 30s TTL 缓存几乎无收益。
	return a.sharedExt.GetAggregatedSkills(a.buildSharedSkillProviders, force)
}

// InvalidateSharedExtCache 使聚合视图缓存失效（下一次聚合强制重建）。
// P4 在插件更新/卸载路径调用：插件来源 skill 的变化不经过 sharedext 的
// registry 写路径，不会触发其内建缓存失效，需要外部挂点。
func (a *App) InvalidateSharedExtCache() {
	a.sharedExtAggForce.Store(true)
}

// PickSharedSkillZipFile 弹出系统文件选择对话框选择 skill zip 压缩包。
// 用户取消返回空串（无错误）。
func (a *App) PickSharedSkillZipFile() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 skill 压缩包",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "ZIP 压缩包 (*.zip)", Pattern: "*.zip"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("open file dialog: %w", err)
	}
	return path, nil
}

// PickSharedSkillDir 弹出系统目录选择对话框选择 skill 文件夹。
// 用户取消返回空串（无错误）。
func (a *App) PickSharedSkillDir() (string, error) {
	dir, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 skill 文件夹",
	})
	if err != nil {
		return "", fmt.Errorf("open directory dialog: %w", err)
	}
	return dir, nil
}

// PickMCPJSONFile 弹出系统文件选择对话框选择 MCP JSON 文件。
// 用户取消返回空串（无错误）。
func (a *App) PickMCPJSONFile() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 MCP JSON 文件",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("open file dialog: %w", err)
	}
	return path, nil
}

// ReadTextFile 读取所选 JSON 文件的文本内容给前端预览（限制 1MB，防止
// 误选超大文件拖垮 webview）。
func (a *App) ReadTextFile(path string) (string, error) {
	if path == "" {
		return "", errors.New("文件路径为空")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("读取文件信息失败: %w", err)
	}
	if info.IsDir() {
		return "", errors.New("所选路径是目录")
	}
	if info.Size() > readTextFileMaxBytes {
		return "", fmt.Errorf("文件过大（%d 字节，超出 1MB 上限）", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	return string(data), nil
}

// buildSharedSkillProviders 组装五个引擎的插件 skill providers 供聚合视图
// 使用。任一引擎失败只降级为空并记日志，不让整个聚合失败（CLI 不可用、
// 配置读取失败等都是可容忍的降级场景）。
//
// 覆盖现状（P4 后）：
//   - claude：skills + 子项禁用投影（远端 1.3.144 stash 机制：禁用 skill 物理移
//     入 stash，detail.Skills 只含现存启用态；被 stash 的 skill 聚合条目自然
//     消失）；插件级 Enabled 不映射（claude settings enabledPlugins 缺省语义
//     不确定，避免误标，交内建 TTL/刷新）；
//   - codex：skills + 插件级禁用映射（config.toml enabled=false → 全部
//     skill 标记停用）；子项级禁用无存储（CLI/配置均无该机制，探测证据
//     见 P4 报告）→ 空；
//   - opencode：skills 可取（ResourceInfo 无描述字段）；无子项/插件级启停
//     机制（opencode.json plugin 数组仅成员语义，1.19.3 无 enable 字段）→ 空；
//   - pi：skills + skill 级禁用（packages 条目 skills[] 过滤语义评估）+
//     包级禁用（!**×4 标记 → 全部标记停用）；
//   - omp：skills（npm 实体路径扫描；marketplace 回退 installed_plugins.json）
//   - 插件级禁用映射（omp plugin disable → 全部标记停用）；无 skill 级机制。
func (a *App) buildSharedSkillProviders() []sharedext.PluginSkillsProvider {
	providers := make([]sharedext.PluginSkillsProvider, 0, 16)
	providers = append(providers, a.sharedSkillProvidersClaude()...)
	providers = append(providers, a.sharedSkillProvidersCodex()...)
	providers = append(providers, a.sharedSkillProvidersOpenCode()...)
	providers = append(providers, a.sharedSkillProvidersPi()...)
	providers = append(providers, a.sharedSkillProvidersOmp()...)
	// P6 交汇点：共用插件市场部署到 ~/.agents/skills 的技能作为聚合来源
	//（origin 语义为 plugin，Engine=market 供前端区分徽章与禁删文案）。
	providers = append(providers, a.sharedSkillProvidersMarket()...)
	return providers
}

// sharedSkillProvidersClaude 收集 Claude Code 已装插件的 skills。
// GetPluginDetail 是本地文件扫描（manifest + skills/agents/commands/hooks
// 目录），逐插件失败只跳过该插件并记日志。
func (a *App) sharedSkillProvidersClaude() []sharedext.PluginSkillsProvider {
	installed, err := a.Plugins.GetInstalledPlugins()
	if err != nil {
		a.Log.Warn("sharedext", "读取 Claude 已装插件失败，聚合降级为空", err.Error())
		return nil
	}
	out := make([]sharedext.PluginSkillsProvider, 0, len(installed))
	for _, p := range installed {
		detail, err := a.Plugins.GetPluginDetail(p.ID)
		if err != nil {
			a.Log.Warn("sharedext", "读取 Claude 插件详情失败，跳过该插件", "plugin="+p.ID, "err="+err.Error())
			continue
		}
		provider := claudePluginSkillsProvider(p, detail)
		if len(provider.Skills) > 0 {
			out = append(out, provider)
		}
	}
	return out
}

// claudePluginSkillsProvider 把 Claude 插件详情映射为聚合 provider（纯函数）。
// 远端 1.3.144 起 Claude 子项启停为「文件系统即真相」：禁用 skill/command/agent
// = 物理移入 stash（~/.amagi-codebox/plugin-subitems-stash），MCP = 条目移出
// .mcp.json。因此 detail.Skills 只含现存（启用态）skill；被 stash 的 skill
// 从聚合视图自然消失（P6 融合裁定），SubItemsDisabled 反映 stash 条目的
// 禁用态（与仍可见条目取交集后为空，保留投影以防未来扫描面包含 stash 视图）。
func claudePluginSkillsProvider(installed plugin.InstalledPlugin, detail *plugin.PluginDetail) sharedext.PluginSkillsProvider {
	skills := make([]sharedext.PluginSkill, 0, len(detail.Skills))
	for _, sk := range detail.Skills {
		skills = append(skills, sharedext.PluginSkill{Name: sk.Name, Description: sk.Description})
	}
	disabled := make(map[string]bool)
	for _, item := range detail.SubItems {
		if item.Type == plugin.SubItemTypeSkill && !item.Enabled {
			disabled["skill/"+item.Name] = true
		}
	}
	return sharedext.PluginSkillsProvider{
		Engine:           sharedext.EngineClaude,
		PluginID:         installed.ID,
		PluginName:       installed.Name,
		Skills:           skills,
		SubItemsDisabled: disabled,
	}
}

// sharedSkillProvidersCodex 收集 Codex 已装插件的 skills。listPlugins 优先
// 走 codex CLI（失败回退 config.toml）；GetPluginDetails 内部会再次列装，
// 单插件详情失败只跳过该插件并记日志。
func (a *App) sharedSkillProvidersCodex() []sharedext.PluginSkillsProvider {
	plugins, err := a.CodexPlugins.ListPlugins("")
	if err != nil {
		a.Log.Warn("sharedext", "读取 Codex 已装插件失败，聚合降级为空", err.Error())
		return nil
	}
	out := make([]sharedext.PluginSkillsProvider, 0, len(plugins))
	for _, p := range plugins {
		detail, err := a.CodexPlugins.GetPluginDetails(codexplugin.PluginSelector{PluginID: p.ID})
		if err != nil {
			a.Log.Warn("sharedext", "读取 Codex 插件详情失败，跳过该插件", "plugin="+p.ID, "err="+err.Error())
			continue
		}
		provider := codexPluginSkillsProvider(p, detail)
		if len(provider.Skills) > 0 {
			out = append(out, provider)
		}
	}
	return out
}

// codexPluginSkillsProvider 把 Codex 插件详情映射为聚合 provider（纯函数）。
// Codex 无子项级禁用机制（SetPluginSubItemEnabled 为兼容占位，config.toml
// 仅插件级 enabled）——子项禁用表恒空；插件级禁用（Enabled=false）映射为
// 全部 skill 停用（插件不加载，其 skill 均不可用）。
func codexPluginSkillsProvider(installed codexplugin.CodexPlugin, detail *codexplugin.CodexPluginDetail) sharedext.PluginSkillsProvider {
	skills := make([]sharedext.PluginSkill, 0, len(detail.Skills))
	for _, sk := range detail.Skills {
		skills = append(skills, sharedext.PluginSkill{Name: sk.Name, Description: sk.Description})
	}
	var disabled map[string]bool
	if !installed.Enabled {
		disabled = make(map[string]bool, len(skills))
		for _, sk := range skills {
			disabled["skill/"+sk.Name] = true
		}
	}
	return sharedext.PluginSkillsProvider{
		Engine:           sharedext.EngineCodex,
		PluginID:         installed.ID,
		PluginName:       installed.Name,
		Skills:           skills,
		SubItemsDisabled: disabled,
	}
}

// sharedSkillProvidersOpenCode 收集 OpenCode 已装插件的 skills。
// ListInstalledPlugins/GetPluginDetails 均为本地配置与目录扫描，无 CLI 调用。
func (a *App) sharedSkillProvidersOpenCode() []sharedext.PluginSkillsProvider {
	plugins, err := a.OpenCodePlugins.ListInstalledPlugins()
	if err != nil {
		a.Log.Warn("sharedext", "读取 OpenCode 已装插件失败，聚合降级为空", err.Error())
		return nil
	}
	out := make([]sharedext.PluginSkillsProvider, 0, len(plugins))
	for _, p := range plugins {
		detail, err := a.OpenCodePlugins.GetPluginDetails(p.Spec)
		if err != nil {
			a.Log.Warn("sharedext", "读取 OpenCode 插件详情失败，跳过该插件", "plugin="+p.ID, "err="+err.Error())
			continue
		}
		provider := openCodePluginSkillsProvider(p, detail)
		if len(provider.Skills) > 0 {
			out = append(out, provider)
		}
	}
	return out
}

// openCodePluginSkillsProvider 把 OpenCode 插件详情映射为聚合 provider
// （纯函数）。ResourceInfo 只有 Name/FilePath，无描述字段；PluginID 取
// Plugin.ID（与 Spec 同值，P3 跳插件详情可直接用）。OpenCode 无子项级启停
// 机制（P4 探测：opencode.json plugin 数组仅成员语义，1.19.3 CLI 无 enable
// 子命令；新 schema 的 plugins "-id" 指令作用于插件 ID 而非 skill）——
// SubItemsDisabled 恒空，前端 opengine 条目不提供单 skill 开关（不造假开关）。
func openCodePluginSkillsProvider(installed opencodeplugin.Plugin, detail *opencodeplugin.PluginDetail) sharedext.PluginSkillsProvider {
	skills := make([]sharedext.PluginSkill, 0, len(detail.Skills))
	for _, sk := range detail.Skills {
		skills = append(skills, sharedext.PluginSkill{Name: sk.Name})
	}
	return sharedext.PluginSkillsProvider{
		Engine:     sharedext.EngineOpenCode,
		PluginID:   installed.ID,
		PluginName: installed.Name,
		Skills:     skills,
	}
}

// sharedSkillProvidersPi 收集 pi 已装包的 skills（包资源里 type=skill 条目）。
// ListInstalledPackages/GetPackageDetails 均为本地配置与实体目录扫描。
func (a *App) sharedSkillProvidersPi() []sharedext.PluginSkillsProvider {
	packages, err := a.PiPlugins.ListInstalledPackages()
	if err != nil {
		a.Log.Warn("sharedext", "读取 pi 已装包失败，聚合降级为空", err.Error())
		return nil
	}
	out := make([]sharedext.PluginSkillsProvider, 0, len(packages))
	for _, pkg := range packages {
		detail, err := a.PiPlugins.GetPackageDetails(pkg.Source)
		if err != nil {
			a.Log.Warn("sharedext", "读取 pi 包详情失败，跳过该包", "pkg="+pkg.Source, "err="+err.Error())
			continue
		}
		provider := piPackageSkillsProvider(pkg, detail)
		if len(provider.Skills) > 0 {
			out = append(out, provider)
		}
	}
	return out
}

// piPackageSkillsProvider 把 pi 包详情映射为聚合 provider（纯函数）。
// 资源类型字面量 "skill" 对应 piplugin.ResourceInfo.Type 的取值域
// （extension/skill/prompt/theme；piplugin 的类型常量为包私有）。
// 子项禁用（P4）：ResourceInfo.Disabled 按包条目 skills[] 过滤语义评估
// （pi 原生 "-path" 精确排除 / 白名单未命中）；包级禁用（四键 !** 全排除
// 标记，Enabled=false）→ 全部 skill 标记停用。
func piPackageSkillsProvider(pkg piplugin.Package, detail *piplugin.PackageDetail) sharedext.PluginSkillsProvider {
	skills := make([]sharedext.PluginSkill, 0)
	var disabled map[string]bool
	for _, res := range detail.Resources {
		if res.Type != "skill" {
			continue
		}
		skills = append(skills, sharedext.PluginSkill{Name: res.Name})
		if res.Disabled || !pkg.Enabled {
			if disabled == nil {
				disabled = make(map[string]bool)
			}
			disabled["skill/"+res.Name] = true
		}
	}
	return sharedext.PluginSkillsProvider{
		Engine:           sharedext.EnginePi,
		PluginID:         pkg.ID,
		PluginName:       pkg.Name,
		Skills:           skills,
		SubItemsDisabled: disabled,
	}
}

// sharedSkillProvidersOmp 收集 omp 已装插件的 skills（P4 补齐）。
// 一次 CLI list 后对每个插件做本地目录扫描（InspectPlugin 不再 fork CLI），
// 避免 N+1 次 CLI 调用；CLI 不可用时该引擎降级为空。
func (a *App) sharedSkillProvidersOmp() []sharedext.PluginSkillsProvider {
	plugins, err := a.OmpPlugins.ListPlugins()
	if err != nil {
		a.Log.Warn("sharedext", "读取 omp 已装插件失败，聚合降级为空", err.Error())
		return nil
	}
	out := make([]sharedext.PluginSkillsProvider, 0, len(plugins))
	for _, p := range plugins {
		detail := a.OmpPlugins.InspectPlugin(p)
		if detail == nil {
			continue
		}
		provider := ompPluginSkillsProvider(p, detail)
		if len(provider.Skills) > 0 {
			out = append(out, provider)
		}
	}
	return out
}

// ompPluginSkillsProvider 把 omp 插件详情映射为聚合 provider（纯函数）。
// omp 无 skill 级启停机制（P4 探测：omp plugin enable/disable 为插件级，
// features 是包可选特性而非 skill）——子项禁用表恒空；插件级禁用
// （Enabled=false，omp plugin disable）映射为全部 skill 停用。
func ompPluginSkillsProvider(installed ompplugin.Plugin, detail *ompplugin.PluginDetail) sharedext.PluginSkillsProvider {
	skills := make([]sharedext.PluginSkill, 0, len(detail.Skills))
	for _, sk := range detail.Skills {
		skills = append(skills, sharedext.PluginSkill{Name: sk.Name})
	}
	var disabled map[string]bool
	if !installed.Enabled {
		disabled = make(map[string]bool, len(skills))
		for _, sk := range skills {
			disabled["skill/"+sk.Name] = true
		}
	}
	return sharedext.PluginSkillsProvider{
		Engine:           sharedext.EngineOmp,
		PluginID:         installed.ID,
		PluginName:       installed.Name,
		Skills:           skills,
		SubItemsDisabled: disabled,
	}
}

// sharedSkillProvidersMarket 收集共用插件市场部署到 ~/.agents/skills 的技能
// 作为聚合来源（P6 交汇点）。sharedmarket 无部署记录持久化（DeployReport
// 一次性返回），此处按「市场清单可部署技能名 ∩ ~/.agents 现存目录」推导
// 已部署条目：skills/<name> 在位 = 已部署启用，skills-disabled/<name> 在位
// = 已部署但被停用（sharedskills 启停语义：两目录间搬移）。只读推导，
// 不写任何文件；市场不可读时降级为空并记日志（与五引擎 provider 同策略）。
// Engine 用 "market" 标识（非五引擎；前端据此显示「插件市场」徽章、禁删、
// 卸载归共用市场子页管理）。
func (a *App) sharedSkillProvidersMarket() []sharedext.PluginSkillsProvider {
	if a.SharedMarkets == nil {
		return nil
	}
	markets, err := a.SharedMarkets.List()
	if err != nil {
		a.Log.Warn("sharedext", "读取共用市场清单失败，市场来源聚合降级为空", err.Error())
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		a.Log.Warn("sharedext", "解析用户主目录失败，市场来源聚合降级为空", err.Error())
		return nil
	}
	skillsRoot := filepath.Join(home, ".agents", "skills")
	disabledRoot := filepath.Join(home, ".agents", "skills-disabled")
	out := make([]sharedext.PluginSkillsProvider, 0, 8)
	for _, m := range markets {
		for _, p := range m.Plugins {
			if p.ExternalSource {
				continue // 外部引用插件无法本地枚举技能，无部署面
			}
			skills := make([]sharedext.PluginSkill, 0, len(p.Skills))
			disabled := make(map[string]bool)
			for _, name := range p.Skills {
				switch {
				case marketSkillDeployed(filepath.Join(skillsRoot, name)):
					skills = append(skills, sharedext.PluginSkill{Name: name, Description: p.Description})
				case marketSkillDeployed(filepath.Join(disabledRoot, name)):
					skills = append(skills, sharedext.PluginSkill{Name: name, Description: p.Description})
					disabled["skill/"+name] = true
				}
			}
			if len(skills) == 0 {
				continue
			}
			out = append(out, sharedext.PluginSkillsProvider{
				Engine:           sharedext.Engine("market"),
				PluginID:         p.Name + "@" + m.Name,
				PluginName:       p.Name,
				Skills:           skills,
				SubItemsDisabled: disabled,
			})
		}
	}
	return out
}

// marketSkillDeployed 判断目录存在且非空（部署产物是含 SKILL.md 的目录；
// 空目录/残留文件不算已部署，避免误标）。
func marketSkillDeployed(dir string) bool {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}
