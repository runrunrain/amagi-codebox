package main

// app_remote_facade.go — Remote server App interface adapter, host summary/launch
// settings builders, WebUI/external URL helpers, and M1-A pairing/security facades.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/webui"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

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
	// HostSummaryDegraded 透出 v1 HostSummary 缓存最近错误态（P3-B R2，非
	// wire 契约字段）：自检卡据此点亮降级横幅，替代桌面无法直读的探测。
	HostSummaryDegraded bool `json:"hostSummaryDegraded"`
}

type OpenRemoteWebUIResult struct {
	URL     string `json:"url"`
	Port    int    `json:"port"`
	Running bool   `json:"running"`
}

// --- remote.AppInterface 窄适配器（不把内部服务 getter/工具方法暴露给 Wails JS 桥） ---

type remoteAppAdapter struct{ *App }

func (a *App) remoteAdapter() remote.AppInterface {
	return &remoteAppAdapter{a}
}

func (r *remoteAppAdapter) GetSettingsService() *settings.Service   { return r.Settings }
func (r *remoteAppAdapter) GetPathsService() *paths.PathsService    { return r.Paths }
func (r *remoteAppAdapter) GetConfigService() *config.ConfigService { return r.Config }
func (r *remoteAppAdapter) SaveAllConfig() error                    { return r.saveAllConfig() }
func (r *remoteAppAdapter) GetKeyDiagnostics() map[string]map[string]string {
	return r.getKeyDiagnostics()
}

// GetSessionWebUI 返回会话 webui 平面的缓存快照（remote.AppInterface 扩展，
// remote-webui-plane 切片；供 remote server 的 /webui/{sid} 反向代理使用）。
// WebUI 服务未接线（测试构造）时返回 unknown。
func (r *remoteAppAdapter) GetSessionWebUI(sessionID string) (remote.SessionWebUIInfo, bool) {
	if r.WebUI == nil {
		return remote.SessionWebUIInfo{State: string(webui.StateUnknown)}, false
	}
	return r.sessionWebUIInfo(sessionID, r.WebUI.GetWebUIStatus(sessionID)), true
}

// ProbeSessionWebUI 先执行一轮 webui 探测（与桌面端轮询同一状态机推进路径）
// 再返回快照；remote v1 状态端点按此节奏轮询。
func (r *remoteAppAdapter) ProbeSessionWebUI(sessionID string) (remote.SessionWebUIInfo, bool) {
	if r.WebUI == nil {
		return remote.SessionWebUIInfo{State: string(webui.StateUnknown)}, false
	}
	return r.sessionWebUIInfo(sessionID, r.WebUI.ProbeWebUI(sessionID)), true
}

// sessionWebUIInfo 把 webui.Status 投影为 remote 侧快照；仅 available 时
// 附带端口与 capability token（token 供代理出向覆盖式注入，不下发日志）。
func (a *App) sessionWebUIInfo(sessionID string, st webui.Status) remote.SessionWebUIInfo {
	info := remote.SessionWebUIInfo{State: string(st.State)}
	if st.State == webui.StateAvailable {
		if port, token := a.WebUI.SessionEndpoint(sessionID); port > 0 {
			info.Port, info.Token = port, token
		}
	}
	return info
}

// GetRemoteToken 返回远程服务器 Bearer Token，供前端展示给用户用于移动端连接。
func (a *App) GetRemoteToken() string {
	return a.Remote.GetToken()
}

// ListLocalLanAddresses 枚举本机局域网 IPv4 候选地址（P3-B R1）：私网优先、
// 含网卡名与掩码长度；过滤回环；疑似 VPN/虚拟网卡仅标注不硬滤。供配对卡
// addressRequired 兜底单选与自检卡展示。枚举失败返回错误（前端回退手动输入）。
func (a *App) ListLocalLanAddresses() ([]remote.LanAddressInfo, error) {
	return remote.ListLocalLanAddresses()
}

// GetRemoteStatus 返回远程服务器状态信息。
func (a *App) GetRemoteStatus() map[string]any {
	return map[string]any{
		"host":    a.Remote.GetHost(),
		"port":    a.Remote.GetPort(),
		"token":   a.Remote.GetToken(),
		"running": a.Remote.IsRunning(),
		// enabled（持久化意图）与 running（实际监听）分离透出：Startup 恢复失败
		// 时形成 enabled=true / running=false 的可观察漂移窗口（P3-B③），配套
		// lastStartError 给出原因；hostSummaryDegraded 为 v1 HostSummary 缓存
		// 最近错误态（P3-B R2，非 wire 契约字段，供自检卡降级横幅）。
		"enabled":             a.Settings.GetRemoteEnabled(),
		"lastStartError":      a.getLastRemoteStartError(),
		"hostSummaryDegraded": a.Remote.HostSummaryDegraded(),
	}
}

// SessionControlHoldView 是桌面「控制权管理」卡可消费的会话控制权持有投影
// （frontend 直接消费的 JSON 形状；不透出内部类型）。仅包含 device holder 的
// 展示字段，不含任何凭据或连接内部标识。
type SessionControlHoldView struct {
	SessionID  string `json:"sessionID"`
	DeviceID   string `json:"deviceID"`
	DeviceName string `json:"deviceName"`
	InGrace    bool   `json:"inGrace"`
}

// GetSessionControlHolds 返回当前由远程设备持有控制权的会话列表（桌面管理
// 视图）。安全边界：仅枚举 device holder（无人持有/桌面持有不列——管理目标
// 是「设备持有」）；只读，不改变任何控制权状态，不触碰远程服务开关。control
// 运行时未就绪（启动早期/已关闭）时自然返回空列表——彼时不存在设备持有。
func (a *App) GetSessionControlHolds() []SessionControlHoldView {
	if a.control == nil || !a.control.IsReady() {
		return []SessionControlHoldView{}
	}
	holds := a.control.Gate().ListSessionHolds()
	views := make([]SessionControlHoldView, 0, len(holds))
	for _, h := range holds {
		views = append(views, SessionControlHoldView{
			SessionID:  string(h.SessionID),
			DeviceID:   string(h.DeviceID),
			DeviceName: h.DeviceName,
			InGrace:    h.InGrace,
		})
	}
	return views
}

// ReleaseSessionControl 桌面端主动收回指定会话的远程设备控制权（桌面根
// 权威：物理主机所有者收回权天然正当）。语义：进程内原子执行
// TakeDesktop(Wails 权威) → ReleaseDesktop 组合链——设备持有者先收到
// takeover 事件（手机端提示「桌面端取得控制权」）随即 released 事件，终态
// 为无人持有（不停留 desktop holder，设备不会被永久 409），设备随后可重新
// 接管。安全边界：仅桌面本地 Wails 绑定调用面，不接受远程输入；无人持有时
// 幂等成功；会话不存在时返回明确提示。
func (a *App) ReleaseSessionControl(sessionID string) error {
	if a.control == nil || !a.control.IsReady() {
		return remote.ErrControlNotReady
	}
	if err := a.control.Gate().ForceReleaseControl(contract.SessionID(sessionID)); err != nil {
		// gate 透传层把 *ControlGateError 解引用为值类型返回，这里用值目标
		// 精确匹配本调用链，避免把「会话不存在」误报成笼统失败。
		var deny remote.ControlGateError
		if errors.As(err, &deny) && deny.Kind == remote.DenySessionNotFound {
			return fmt.Errorf("会话不存在或已结束：%s", sessionID)
		}
		return fmt.Errorf("收回控制权失败：%v", err)
	}
	return nil
}

// getLastRemoteStartError 返回最近一次 Startup 恢复路径的 Start 失败文本
// （空串表示无失败记录）。只读，不触发任何重试。
func (a *App) getLastRemoteStartError() string {
	a.lastRemoteStartErrMu.Lock()
	defer a.lastRemoteStartErrMu.Unlock()
	return a.lastRemoteStartError
}

// setLastRemoteStartError 记录 Startup 恢复路径的最终 Start 失败（重试耗尽后）。
func (a *App) setLastRemoteStartError(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	a.lastRemoteStartErrMu.Lock()
	a.lastRemoteStartError = msg
	a.lastRemoteStartErrMu.Unlock()
}

// clearLastRemoteStartError 在任何一次成功 Start 后清空历史失败记录，避免
// 陈旧错误持续点亮自检卡端口横幅。
func (a *App) clearLastRemoteStartError() {
	a.lastRemoteStartErrMu.Lock()
	a.lastRemoteStartError = ""
	a.lastRemoteStartErrMu.Unlock()
}

// remoteStartRetryDelays bounds the Startup-restore self-heal (P3-B③): after
// the initial restore Start failed inside applyRemoteGateResult, a transient
// listen failure — e.g. the configured port still held by a previous instance
// mid-shutdown — is retried with a short fixed backoff via
// remote.Server.StartWithRetry. Small by design: persistent conflicts (real
// port occupancy) surface as an observable state (startup warning +
// GetRemoteStatus.lastStartError) instead of blocking boot. Fixed production
// values; the retry engine itself is schedule-parameterized and unit-tested
// in internal/remote (StartWithRetry).
var remoteStartRetryDelays = []time.Duration{500 * time.Millisecond, 1 * time.Second}

// healRemoteStartupRestore runs the bounded Startup-restore self-heal and the
// enabled/running drift alignment (P3-B③; 素材B ⑥)。 MUST be called by Startup
// AFTER applyRemoteGateResult — the initial restore Start (and its
// fail-closed gate) happens there; this step only retries an actually-failed
// start and records the observable outcome:
//
//   - retry succeeds → running=true, drift self-healed, stale error cleared;
//   - retries exhausted → enabled stays persisted-on (silently flipping the
//     user's intent off would stop the next boot from even trying); instead
//     the drift becomes observable: fixed startup warning + GetRemoteStatus
//     lastStartError/enabled/running so the self-check card can classify it
//     (e.g. port conflict) instead of the old log-only Warn.
//
// The post-load allowStart is reconstructed from the device-store ready latch:
// applyRemoteGateResult may have latched allowStart=false on a
// LoadSecurityState failure invisible from here — a not-ready store means
// exactly that, and Start is never retried across a failed security load
// (Major-01 fail-closed).
func (a *App) healRemoteStartupRestore(ctx context.Context, remoteEnabled, startAllowed bool) {
	if !remoteEnabled || !startAllowed {
		return
	}
	if a.Remote.IsRunning() {
		return // 初次恢复启动已成功，无漂移可自愈
	}
	if !a.Remote.GetSecurityHealth().SecurityReady {
		return // 安全状态未就绪（load 失败/被 gate 拦截）：绝不越过 fail-closed 重试
	}
	if err := a.Remote.StartWithRetry(ctx, remoteStartRetryDelays, nil); err != nil {
		a.setLastRemoteStartError(err)
		a.addStartupWarning(fmt.Sprintf(
			"远程服务自启动失败（含 %d 次重试）：远程开关保持开启，下次启动或手动开启时会重试；若端口被占用，请到设置 › 远程访问更换监听端口。（原因：%s）",
			len(remoteStartRetryDelays), err.Error()))
		a.Log.Warn("app", "远程服务器启动失败（不影响主功能；已记录到启动警告与远程状态供自检透出）", err.Error())
	} else {
		a.clearLastRemoteStartError()
		a.Log.Info("app", "远程服务器启动自愈成功",
			fmt.Sprintf("retries=%d", len(remoteStartRetryDelays)))
	}
}

// GetRemoteWebUIStatus 返回桌面入口 Web UI 的可打开状态。
func (a *App) GetRemoteWebUIStatus() RemoteWebUIStatusResult {
	status := RemoteWebUIStatusResult{
		Port:    a.Remote.GetPort(),
		Running: a.Remote.IsRunning(),
		// 在所有早退分支之前填充：自检卡无论 WebRoot 是否就绪都需要该降级态。
		HostSummaryDegraded: a.Remote.HostSummaryDegraded(),
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

// ValidateExternalURL 校验外部链接可安全交给系统浏览器打开：仅 http/https 且带 host。
// 独立于 App 方法便于单测（web 平面 amagi:open-url 宿主桥的 Go 侧白名单）。
func ValidateExternalURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid url: 仅支持带主机的 http/https 链接")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q: 仅支持 http/https", u.Scheme)
	}
	return nil
}

// OpenExternalURL 在系统默认浏览器打开外部链接（Web 平面 iframe 内 amagi:open-url
// 宿主桥的消费端：sandbox 无 allow-popups，页面内 target=_blank 被阻断，改由宿主
// 代开。前端已做 token 凭证 + http(s) 白名单校验，此处二次白名单纵深防御）。
func (a *App) OpenExternalURL(rawURL string) error {
	if err := ValidateExternalURL(rawURL); err != nil {
		return err
	}
	wailsRuntime.BrowserOpenURL(a.ctx, strings.TrimSpace(rawURL))
	return nil
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

// buildRemoteHostSummary is the private HostSummary provider. It probes every
// known CLI through the same Planner instance consumed by remote create; a
// missing planner is fail-closed rather than a binary-only availability fallback.
func (a *App) buildRemoteHostSummary() (contract.HostSummary, error) {
	planner := a.remotePlanner
	if planner == nil {
		planner = launchplan.NewFailClosedPlanner()
	}
	availability := make([]contract.CLIAvailability, 0, len(contract.KnownCLITypes))
	for _, cliType := range contract.KnownCLITypes {
		item, _ := planner.Probe(context.Background(), cliType)
		item.CLIType = cliType
		availability = append(availability, item)
	}
	return contract.HostSummary{
		APIVersion: contract.APIVersionV1, ServerVersion: resolveAppVersion(),
		CLIAvailability: availability, LaunchSettings: a.buildRemoteLaunchSettings(),
	}, nil
}

// buildRemoteLaunchSettings returns the complete authorized-device launch
// form without exposing provider URLs, credentials or environment values.
func (a *App) buildRemoteLaunchSettings() *contract.LaunchSettings {
	settingsSurface := &contract.LaunchSettings{
		Workdirs: []contract.LaunchPathOption{}, Shells: []contract.LaunchPathOption{},
		CLIs: make([]contract.CLILaunchSettings, 0, len(contract.KnownCLITypes)),
	}
	seenWorkdirs := map[string]bool{}
	addWorkdir := func(path, label string) {
		path = strings.TrimSpace(path)
		if path == "" || seenWorkdirs[path] {
			return
		}
		label = strings.TrimSpace(label)
		if label == "" {
			label = filepath.Base(path)
		}
		settingsSurface.Workdirs = append(settingsSurface.Workdirs, contract.LaunchPathOption{Path: path, Label: label})
		seenWorkdirs[path] = true
	}
	if a.Paths != nil {
		addWorkdir(a.Paths.GetDefaultPath(), "默认目录")
		for _, item := range a.Paths.GetPaths() {
			addWorkdir(item.Path, item.Label)
		}
	}
	if a.Settings != nil {
		for _, item := range a.Settings.GetSettings().SavedWorkDirs {
			addWorkdir(item.Path, item.Label)
		}
		seenShells := map[string]bool{}
		addShell := func(path, label string) {
			path = strings.TrimSpace(path)
			if path == "" || seenShells[path] {
				return
			}
			label = strings.TrimSpace(label)
			if label == "" {
				label = filepath.Base(path)
			}
			settingsSurface.Shells = append(settingsSurface.Shells, contract.LaunchPathOption{Path: path, Label: label})
			seenShells[path] = true
		}
		for _, item := range a.Settings.GetShellPaths() {
			addShell(item.Path, item.Label)
		}
		dashboard := a.Settings.GetDashboardDefaults()
		for _, item := range []struct{ path, label string }{
			{dashboard.ClaudeShell, "Claude Code 默认 Shell"},
			{dashboard.OpenCodeShell, "OpenCode 默认 Shell"},
			{dashboard.CodexShell, "Codex 默认 Shell"},
			{dashboard.PiShell, "Pi 默认 Shell"},
			{dashboard.OmpShell, "Oh My Pi 默认 Shell"},
		} {
			addShell(item.path, item.label)
		}
	}

	providers := map[string]config.Provider{}
	if a.Config != nil {
		providers = a.Config.GetProviders()
	}
	providerKeys := make([]string, 0, len(providers))
	for key := range providers {
		providerKeys = append(providerKeys, key)
	}
	sort.Strings(providerKeys)
	defaults := map[string]settings.RemoteLaunchDefaultV1{}
	if a.Settings != nil {
		defaults = a.Settings.GetRemoteLaunchDefaultsV1()
	}
	presetFormat := map[contract.CLIType]string{
		contract.CLITypeClaudeCode: string(config.TerminalPresetAnthropic),
		contract.CLITypeCodex:      string(config.TerminalPresetOpenAI),
	}
	// Pi/Omp opt-in 扩面（HarnessSync）：openai 桶（pi/omp 原生桶）全量之外，
	// anthropic 桶中带 HarnessSync 标记的预设附加在选择面末尾（撞 key 时
	// openai 先注册胜出）；这些标记预设所属的 provider 即使非 OpenAI 兼容也
	// 进入 Pi/Omp Providers 下拉——用户已显式标记其预设供 pi/omp 消费。
	// 两 CLI 共享同一份派生数据，先在循环外算好（marked provider 集合 +
	// 合并预设项），循环内只消费。
	markedHarnessSyncProviders := map[string]bool{}
	piOmpPresetOptions := []contract.LaunchPresetOption{}
	if a.Config != nil {
		seenPiOmpPresets := map[string]bool{}
		if presets, err := a.Config.GetMergedTerminalPresets(string(config.TerminalPresetOpenAI)); err == nil {
			for _, preset := range presets {
				piOmpPresetOptions = append(piOmpPresetOptions, contract.LaunchPresetOption{
					Ref: preset.Key, Label: preset.Label, ProviderRef: preset.Provider, ModelRef: preset.Model,
				})
				seenPiOmpPresets[preset.Key] = true
			}
		}
		if presets, err := a.Config.GetMergedTerminalPresets(string(config.TerminalPresetAnthropic)); err == nil {
			for _, preset := range presets {
				if !preset.HarnessSync {
					continue
				}
				markedHarnessSyncProviders[preset.Provider] = true
				if seenPiOmpPresets[preset.Key] {
					continue
				}
				piOmpPresetOptions = append(piOmpPresetOptions, contract.LaunchPresetOption{
					Ref: preset.Key, Label: preset.Label, ProviderRef: preset.Provider, ModelRef: preset.Model,
				})
				seenPiOmpPresets[preset.Key] = true
			}
		}
	}
	for _, cliType := range contract.KnownCLITypes {
		entry := contract.CLILaunchSettings{
			CLIType: cliType, Providers: []contract.LaunchProviderOption{}, Presets: []contract.LaunchPresetOption{},
		}
		for _, key := range providerKeys {
			provider := providers[key]
			if cliType == contract.CLITypeClaudeCode && !provider.IsAnthropicCompatible() {
				continue
			}
			if cliType == contract.CLITypeCodex && !provider.IsOpenAICompatible() {
				continue
			}
			// Pi/Omp：OpenAI 兼容全集 + anthropic 桶有 HarnessSync 标记预设的
			// provider（opt-in 扩面，其余非 OpenAI 兼容 provider 仍排除）。
			if (cliType == contract.CLITypePi || cliType == contract.CLITypeOmp) &&
				!provider.IsOpenAICompatible() && !markedHarnessSyncProviders[key] {
				continue
			}
			entry.Providers = append(entry.Providers, contract.LaunchProviderOption{
				Ref: key, Label: key, Kind: provider.EffectiveType(), DefaultModel: provider.DefaultModel,
			})
		}
		if a.Config != nil {
			seenPresets := map[string]bool{}
			if cliType == contract.CLITypeOpenCode {
				openCodePresets := a.Config.GetOpenCodePresets()
				keys := make([]string, 0, len(openCodePresets))
				for key := range openCodePresets {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					label := strings.TrimSpace(openCodePresets[key].Name)
					if label == "" {
						label = key
					}
					entry.Presets = append(entry.Presets, contract.LaunchPresetOption{Ref: key, Label: label})
					seenPresets[key] = true
				}
			}
			format, usesCommonPresets := presetFormat[cliType]
			if usesCommonPresets {
				presets, err := a.Config.GetMergedTerminalPresets(format)
				if err == nil {
					for _, preset := range presets {
						if seenPresets[preset.Key] {
							continue
						}
						entry.Presets = append(entry.Presets, contract.LaunchPresetOption{
							Ref: preset.Key, Label: preset.Label, ProviderRef: preset.Provider, ModelRef: preset.Model,
						})
						seenPresets[preset.Key] = true
					}
				}
			} else if cliType == contract.CLITypePi || cliType == contract.CLITypeOmp {
				// Pi/Omp 预设面：openai 桶全量 + anthropic 桶 HarnessSync 标记项
				// 附加在后（piOmpPresetOptions 已在循环外算好；seenPresets 去重
				// 保留，撞 key 时先注册者胜出）。
				for _, preset := range piOmpPresetOptions {
					if seenPresets[preset.Ref] {
						continue
					}
					entry.Presets = append(entry.Presets, preset)
					seenPresets[preset.Ref] = true
				}
			}
		}
		if refs, ok := defaults[string(cliType)]; ok {
			entry.Defaults = &contract.LaunchDefaults{
				ProviderRef: refs.ProviderRef, PresetRef: refs.PresetRef, ModelRef: refs.ModelRef,
				ShellRef: refs.ShellRef, UseHeadroom: refs.UseHeadroom,
			}
		}
		settingsSurface.CLIs = append(settingsSurface.CLIs, entry)
	}
	return settingsSurface
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
		// 手动开启成功：清空 Startup 恢复路径可能遗留的 Start 失败记录，避免
		// 陈旧错误继续点亮自检卡端口横幅（P3-B③）。
		a.clearLastRemoteStartError()
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
