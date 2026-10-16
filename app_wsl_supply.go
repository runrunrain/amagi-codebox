package main

// app_wsl_supply.go — P3-S1 WSL 托管配置分发的 App 编排面：
//
//   - 业务快照源（PiManagedModelsSource / SharedFileSource 的 App 实现）：
//     从 Config/Secrets 同源构建 amagi-* 期望集与共享小文件内容；
//   - 启动 lazy 闸门：pi API-provider WSL 启动前经协调器分发托管 models.json
//     （损坏不覆盖 → 阻断；其余失败 → 告警沿用既有回退）；
//   - 保存标脏接真实调度：MarkDirty 后对「已运行且无活动会话」的目标异步推送
//     （停机 distro 只标不唤醒；single-flight 合并推送风暴）；
//   - per-target 视觉清单共享同意（密钥跨界授权入口）；
//   - 最小状态面（GetWSLConfigSupplyStatus：provider/视觉清单/附加层 已同步/
//     陈旧/失败/冻结/待同意 + 最后检查时间；OpenCode 附加层为进程内记录）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/piconfig"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/wslsupply"
)

// ---------------------------------------------------------------------------
// 业务快照源
// ---------------------------------------------------------------------------

// wslPiManagedSource 把 Config/Secrets 业务快照翻译为 amagi-* 期望集。构建规则
// 与宿主 providerSync（syncProvidersToHarnessesLocked）一致：同 provider builders、
// 同桶（pi 消费 openai 桶）、同「无模型/无 baseURL 不发布」过滤；差异仅一处——
// 构建失败的 provider 记入 RetainProviders（distro 侧保留旧条目不误 prune），
// 而宿主 reconcile 语义不变。
type wslPiManagedSource struct{ app *App }

func (s *wslPiManagedSource) DesiredPiManagedModels() wslsupply.PiManagedModelsSpec {
	spec := wslsupply.PiManagedModelsSpec{Providers: map[string]any{}, RetainProviders: []string{}, LoginOverrides: []wslsupply.PiLoginModelOverrideSpec{}}
	app := s.app
	if app == nil || app.Config == nil || app.Secrets == nil || !app.providerSyncEnabled {
		return spec
	}
	providers := app.Config.GetProviders()
	presets := app.Config.GetAllTerminalPresets()
	probeCache := app.Config.GetModalityProbeCache()
	piOmpModels := collectManagedProviderModels(providers, presets, probeCache, []config.TerminalPresetType{config.TerminalPresetOpenAI})
	openCodeModels := collectManagedProviderModels(providers, presets, probeCache, config.ValidTerminalPresetTypes())

	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		provider := providers[name]
		models := piOmpModels[name]
		// 与宿主同步同一过滤：无模型 / 无显式 endpoint 的条目不发布（发布即
		// broken amagi-*，或顶掉同名内置 provider 依赖）。这类条目不进
		// RetainProviders——它们是「有意不发布」，prune 语义与宿主一致。
		if len(models) == 0 && len(openCodeModels[name]) == 0 {
			continue
		}
		if strings.TrimSpace(provider.EffectiveBaseURL(provider.PreferredFormat())) == "" {
			continue
		}
		apiKey, _ := app.getProviderAPIKey(name, provider)
		piCfg, err := launcher.BuildPiManagedProviderConfig(name, provider, apiKey, models)
		if err != nil {
			// 构建部分失败：不以残缺集合 prune 旧项（design §七）——保留 distro
			// 侧既有条目并逐项报错。
			spec.RetainProviders = append(spec.RetainProviders, launcher.ManagedProviderID(name))
			spec.BuildErrors = append(spec.BuildErrors, fmt.Sprintf("provider %q 构建失败，保留 WSL 侧旧条目: %v", name, err))
			continue
		}
		appendHarnessProviderEntries(spec.Providers, piCfg)
	}

	// 登录预设 contextWindow 窄字段（补齐语义，cw>0）。
	if presets, err := app.Config.GetPiLoginPresets(); err == nil {
		for _, p := range presets {
			if p.ContextWindow > 0 && strings.TrimSpace(p.Provider) != "" && strings.TrimSpace(p.Model) != "" {
				spec.LoginOverrides = append(spec.LoginOverrides, wslsupply.PiLoginModelOverrideSpec{
					Provider: p.Provider, Model: p.Model, ContextWindow: p.ContextWindow,
				})
			}
		}
	}
	return spec
}

// wslSharedFileSource 读取 Config 管理的共享小文件（视觉导出/学习层）。
type wslSharedFileSource struct{ app *App }

func (s *wslSharedFileSource) VisionExportBytes() ([]byte, error) {
	path, err := config.VisionModelsExportPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read host vision export: %w", err)
	}
	return data, nil
}

func (s *wslSharedFileSource) ModalityKBBytes() ([]byte, error) {
	path, err := config.ModalityKBPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 宿主无学习层 → 视为空知识库
		}
		return nil, fmt.Errorf("read host modality kb: %w", err)
	}
	return data, nil
}

// newWSLSupplyCoordinator 构造生产协调器并注册 S1 托管面 + S2 键级投影适配器。
func newWSLSupplyCoordinator(app *App, configDir string) *wslsupply.Coordinator {
	coord := wslsupply.NewCoordinator(configDir, wslsupplySupplyLog{log: app.Log}, wslsupply.DefaultProbes())
	coord.RegisterProjection(wslsupply.NewPiManagedModelsAdapter(&wslPiManagedSource{app: app}))
	coord.RegisterProjection(wslsupply.NewVisionExportAdapter(&wslSharedFileSource{app: app}))
	coord.RegisterProjection(wslsupply.NewModalityKBAdapter(&wslSharedFileSource{app: app}))
	piConfigSource := &wslPiConfigSource{app: app}
	registerPiConfigAdapters(coord, piConfigSource)
	return coord
}

// wslSupplyUnitsFiltered 按运行器能力过滤单元清单（裸协调器——如测试——缺 S1
// 适配器时自动降级，不报 unknown unit；A2 恒在）。
func wslSupplyUnitsFiltered(runner wslsupply.Runner, units ...wslsupply.UnitID) []wslsupply.UnitID {
	known, ok := runner.(interface{ HasUnit(wslsupply.UnitID) bool })
	out := make([]wslsupply.UnitID, 0, len(units))
	for _, u := range units {
		if ok && !known.HasUnit(u) {
			continue
		}
		out = append(out, u)
	}
	return out
}

// ---------------------------------------------------------------------------
// 启动 lazy 闸门（pi API-provider WSL 路径）
// ---------------------------------------------------------------------------

// runWSLManagedModelsAtLaunch 在 WSL pi API-provider 会话启动前分发托管
// models.json 与三个键级配置投影（P3-S2：settings/amagi/mcp 随启动 lazy 补漏）。
// 失败语义（design §六 + P3 任务书）：
//   - target-corrupt（distro models.json 损坏，两级保护拒绝覆盖）→ 阻断启动
//     （后续 launch overlay 的宽松 merge 会破坏损坏文件，必须先挡住）；
//   - 其余单元失败/阻断（含键级投影的冻结/未托管/转换受阻）→ 告警放行：
//     可选普通偏好同步失败不得阻断会话（design §六「旧 WSL 配置可读且安全
//     → 启动继续」）。
func (a *App) runWSLManagedModelsAtLaunch(distro string) error {
	if a.WSLSupply == nil || strings.TrimSpace(distro) == "" {
		return nil
	}
	units := wslSupplyUnitsFiltered(a.WSLSupply,
		wslsupply.UnitPiManagedModels, wslsupply.UnitPiSettings, wslsupply.UnitPiAmagi, wslsupply.UnitPiMCP)
	if len(units) == 0 {
		return nil
	}
	res, err := a.WSLSupply.RunUnits(context.Background(), distro, wslsupply.CLIPi, wslsupply.ReasonLaunch, units)
	return a.gateWSLPiConfigSupplyResult(res, err, distro)
}

// gateWSLPiConfigSupplyResult 是启动闸门的统一判定（桌面闸门与 remote effect
// 共用，保证同一目标同一结果）：仅 managed-models 的 target-corrupt 阻断；
// 其余失败/阻断告警放行（冻结/未托管/待同意属预期态，不刷错误日志）。
func (a *App) gateWSLPiConfigSupplyResult(res wslsupply.RunResult, err error, distro string) error {
	launchErr, warnMsg, warnDetail := verdictForWSLPiConfigSupply(res, err)
	if launchErr != nil {
		return launchErr
	}
	if warnMsg != "" {
		a.Log.Warn("wslsupply", warnMsg, warnDetail)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 保存标脏 → 异步推送（已运行 distro + 无活动会话）
// ---------------------------------------------------------------------------

// wslSupplyPushState 是异步推送的 single-flight 状态：同一时刻至多一个推送在
// 执行，期间的标脏合并为一次补推（推送风暴不放大跨界次数）。
var wslSupplyPushState struct {
	mu      sync.Mutex
	running bool
	rerun   bool
}

// wslDistroRunningFn 是「distro 正在运行」判定接缝（生产 WSLDistroStates；
// 测试注入）。停机 distro 只标不唤醒——异步推送绝不触发 wsl.exe 启动发行版。
var wslDistroRunningFn = func(distro string) bool {
	return platform.WSLDistroStates(nil)[distro] == "Running"
}

// wslDefaultDistroFn 是「当前默认 distro」接缝（生产 platform.DefaultWSLDistro；
// 测试注入固定值）。动机：真实探测在进程内有负缓存（wsl.exe 一次失败即
// process-wide 缓存空），全量测试并发负载下可被瞬时失败污染——依赖它的
// 单测必须注入固定值而非依赖真机状态。
var wslDefaultDistroFn = platform.DefaultWSLDistro

// scheduleWSLSupplyPush 异步调度一次保存后推送（非阻塞；失败仅告警——「宿主
// 保存成功」与「WSL 已同步」是两个结果）。
func (a *App) scheduleWSLSupplyPush() {
	go a.pushWSLSupplyOnce()
}

func (a *App) pushWSLSupplyOnce() {
	wslSupplyPushState.mu.Lock()
	if wslSupplyPushState.running {
		wslSupplyPushState.rerun = true
		wslSupplyPushState.mu.Unlock()
		return
	}
	wslSupplyPushState.running = true
	wslSupplyPushState.mu.Unlock()
	defer func() {
		wslSupplyPushState.mu.Lock()
		if wslSupplyPushState.rerun {
			wslSupplyPushState.rerun = false
			wslSupplyPushState.mu.Unlock()
			a.pushWSLSupplyOnce() // 合并期间又有标脏：补推一轮
			return
		}
		wslSupplyPushState.running = false
		wslSupplyPushState.mu.Unlock()
	}()

	if a == nil || a.WSLSupply == nil {
		return
	}
	distro := wslDefaultDistroFn(nil)
	if distro == "" {
		return
	}
	if !wslDistroRunningFn(distro) {
		return // 停机 distro：只保留待同步标记（下次启动 lazy 兜底），不唤醒
	}
	if a.hasActiveSessionsForWSLPush() {
		return // 活动会话期间暂停后台应用（design §三），待同步标记保留
	}
	piUnits := wslSupplyUnitsFiltered(a.WSLSupply,
		wslsupply.UnitPiManagedModels, wslsupply.UnitPiSettings, wslsupply.UnitPiAmagi, wslsupply.UnitPiMCP)
	if len(piUnits) > 0 {
		if res, err := a.WSLSupply.RunUnits(context.Background(), distro, wslsupply.CLIPi, wslsupply.ReasonSave, piUnits); err != nil {
			a.Log.Warn("wslsupply", "保存后推送 WSL 托管 provider 配置失败（已保留待同步标记）", err.Error())
		} else {
			for _, r := range res.Results {
				if r.Outcome == wslsupply.UnitFailed || r.Outcome == wslsupply.UnitBlocked {
					a.Log.Warn("wslsupply", "保存后推送 WSL 托管 provider 配置未完成", fmt.Sprintf("unit=%s detail=%s", r.Unit, r.Detail))
				}
			}
		}
	}
	sharedUnits := wslSupplyUnitsFiltered(a.WSLSupply, wslsupply.UnitSharedVisionExport, wslsupply.UnitSharedModalityKB)
	if len(sharedUnits) > 0 {
		if res, err := a.WSLSupply.RunUnits(context.Background(), distro, wslsupply.CLICommon, wslsupply.ReasonSave, sharedUnits); err != nil {
			a.Log.Warn("wslsupply", "保存后推送 WSL 共享配置失败（已保留待同步标记）", err.Error())
		} else {
			for _, r := range res.Results {
				if r.Outcome == wslsupply.UnitFailed || r.Outcome == wslsupply.UnitBlocked {
					//  awaiting-consent / frozen 属预期态（不刷错误日志，状态页可见）。
					if r.Detail != "awaiting-consent" && !strings.Contains(r.Detail, "frozen") && !strings.Contains(r.Detail, "unmanaged-existing") {
						a.Log.Warn("wslsupply", "保存后推送 WSL 共享配置未完成", fmt.Sprintf("unit=%s detail=%s", r.Unit, r.Detail))
					}
				}
			}
		}
	}
}

// hasActiveSessionsForWSLPush 报告是否存在活动（running）内嵌会话。保守取全
// CLI：共享面（视觉清单/学习层）可能被任何会话内的 skill 消费，宽判避免
// 后台推送与运行中会话竞争（S2 预览框架再做按面细分）。
func (a *App) hasActiveSessionsForWSLPush() bool {
	if a.Sessions == nil {
		return false
	}
	for _, s := range a.Sessions.List() {
		if s.Status == session.StatusRunning {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// OpenCode 附加层（最小状态记录 + 失败语义门面）
// ---------------------------------------------------------------------------

// wslOpenCodeLayerRecord 是 OpenCode WSL 附加层写入的进程内状态（无持久化：
// 附加层内容随启动变化，陈旧概念不适用；重启后回「未检查」）。
type wslOpenCodeLayerRecord struct {
	OK     bool
	Detail string
	At     time.Time
}

var wslOpenCodeLayerMu sync.Mutex
var wslOpenCodeLayer = map[string]wslOpenCodeLayerRecord{}

func recordWSLOpenCodeLayer(distro string, ok bool, detail string) {
	wslOpenCodeLayerMu.Lock()
	defer wslOpenCodeLayerMu.Unlock()
	wslOpenCodeLayer[distro] = wslOpenCodeLayerRecord{OK: ok, Detail: detail, At: time.Now()}
}

func lookupWSLOpenCodeLayer(distro string) (wslOpenCodeLayerRecord, bool) {
	wslOpenCodeLayerMu.Lock()
	defer wslOpenCodeLayerMu.Unlock()
	rec, ok := wslOpenCodeLayer[distro]
	return rec, ok
}

// prepareWSLOpenCodeLayer 承担 LaunchOpenCodeSession WSL 分支的附加层准备：
// MCP 形态检查 → 写入（保留 WriteWSLOpenCodeAgentConfig 机制）→ 记录状态。
// 返回 OPENCODE_CONFIG 应指向的 Linux 路径。任一步失败返回中文错误（P3-S1
// 已批准的行为变更：选中模型配置不可安全落地 → 启动失败，不再无 provider
// 静默启动）。
func (a *App) prepareWSLOpenCodeLayer(distro string, runtimeCfg map[string]any) (string, error) {
	sanitized, report := launcher.AdaptWSLOpenCodeMCP(distro, runtimeCfg)
	for _, line := range report {
		a.Log.Warn("opencode-wsl", "OpenCode MCP 形态检查", line)
	}
	linuxPath, wErr := launcher.WriteWSLOpenCodeAgentConfig(distro, sanitized)
	if wErr != nil {
		recordWSLOpenCodeLayer(distro, false, wErr.Error())
		return "", fmt.Errorf("OpenCode 模型配置无法安全写入 WSL（%s）：%v；本次未启动，不再以无 provider 配置静默启动", distro, wErr)
	}
	detail := "written"
	if len(report) > 0 {
		detail = fmt.Sprintf("written (%d 项 MCP 诊断)", len(report))
	}
	recordWSLOpenCodeLayer(distro, true, detail)
	return linuxPath, nil
}

// ---------------------------------------------------------------------------
// 最小状态面（D4 推荐的最小子集：envcheck 卡片下的「配置状态」区）
// ---------------------------------------------------------------------------

// WSLConfigSupplyFace 是一个托管面的脱敏状态（不含内容差异/密钥/完整命令）。
type WSLConfigSupplyFace struct {
	Key       string `json:"key"`       // providers / vision-export / modality-kb / opencode-layer
	Label     string `json:"label"`     // 中文名
	State     string `json:"state"`     // synced / stale / failed / frozen / awaiting-consent / unchecked
	Detail    string `json:"detail"`    // 中文短说明（冻结原因/失败码等）
	LastCheck string `json:"lastCheck"` // RFC3339；空 = 未检查
}

// WSLConfigSupplyStatus 是「配置状态」区的聚合快照（当前 distro + 各托管面）。
type WSLConfigSupplyStatus struct {
	Distro      string                `json:"distro"`
	Available   bool                  `json:"available"`
	Reason      string                `json:"reason,omitempty"`
	Faces       []WSLConfigSupplyFace `json:"faces"`
	PendingPush bool                  `json:"pendingPush"` // 有保存后未推送的来源变更
	// Units 是 P3-S2 键级配置单元的逐项详情（配置状态区扩展）。
	Units []WSLConfigUnitDetail `json:"units"`
}

// GetWSLConfigSupplyStatus 返回当前 WSL 目标的配置供给状态（读缓存，不触发
// 探测/安装/同步；severity-only，不影响 envcheck AllOK）。
func (a *App) GetWSLConfigSupplyStatus() WSLConfigSupplyStatus {
	out := WSLConfigSupplyStatus{Faces: []WSLConfigSupplyFace{}}
	if a == nil {
		return out
	}
	distro := wslDefaultDistroFn(nil)
	out.Distro = distro
	if distro == "" {
		out.Reason = "未找到可用的 WSL 发行版"
		return out
	}
	out.Available = true

	pending := map[string]uint64{}
	piStatus := a.wslSupplyStatus(distro, wslsupply.CLIPi)
	sharedStatus := a.wslSupplyStatus(distro, wslsupply.CLICommon)
	if piStatus.PendingSources != nil {
		for k, v := range piStatus.PendingSources {
			pending[k] = v
		}
	}
	out.PendingPush = len(pending) > 0

	out.Faces = append(out.Faces,
		wslFaceFromUnit("providers", "托管 Provider (Pi)", piStatus, wslsupply.UnitPiManagedModels, out.PendingPush),
		wslVisionFace(sharedStatus, out.PendingPush),
		wslFaceFromUnit("modality-kb", "多模态学习层", sharedStatus, wslsupply.UnitSharedModalityKB, out.PendingPush),
		wslOpenCodeLayerFace(distro),
	)
	// P3-S2：键级配置单元逐项详情（相对路径/策略/冻结原因/可执行动作）。
	out.Units = piConfigUnitDetails(piStatus, out.PendingPush)
	return out
}

func (a *App) wslSupplyStatus(distro string, cli wslsupply.CLI) wslsupply.TargetStatus {
	if a.WSLSupply == nil {
		return wslsupply.TargetStatus{Distro: distro, CLI: cli}
	}
	return a.WSLSupply.GetStatus(distro, cli)
}

func wslFaceFromUnit(key, label string, st wslsupply.TargetStatus, unit wslsupply.UnitID, pending bool) WSLConfigSupplyFace {
	face := WSLConfigSupplyFace{Key: key, Label: label, State: "unchecked", Detail: "尚未检查"}
	if st.Checked {
		face.LastCheck = st.CheckedAt.Format(time.RFC3339)
	}
	for _, u := range st.Units {
		if u.Unit != unit {
			continue
		}
		switch {
		case u.Frozen:
			face.State = "frozen"
			face.Detail = wslFrozenReasonText(u.FrozenReason)
		case u.LastErrorCode != "":
			face.State = "failed"
			face.Detail = "上次同步失败：" + u.LastErrorCode
		case u.LastOutcome == "consented":
			// F5：授权完成但发布未落地（异步推送/下次启动才写目标）——独立
			//「待发布」态，不虚报已同步；发布成功后 LastOutcome=written 收敛。
			face.State = "consented"
			face.Detail = "已授权，待发布到 WSL"
		case pending:
			face.State = "stale"
			face.Detail = "有保存后的变更待推送"
		default:
			face.State = "synced"
			face.Detail = "已同步"
		}
		return face
	}
	if st.Checked {
		// 有检查记录但该单元从未运行（如同意前）：显示未启用语义。
		face.State = "unchecked"
		face.Detail = "该托管面尚未运行"
	}
	return face
}

func wslVisionFace(st wslsupply.TargetStatus, pending bool) WSLConfigSupplyFace {
	face := wslFaceFromUnit("vision-export", "视觉清单共享", st, wslsupply.UnitSharedVisionExport, pending)
	for _, u := range st.Units {
		if u.Unit == wslsupply.UnitSharedVisionExport && !u.Consented {
			face.State = "awaiting-consent"
			face.Detail = "尚未授权向 WSL 共享密钥副本"
			return face
		}
		if u.Unit == wslsupply.UnitSharedVisionExport && u.Frozen && u.FrozenReason == "withdrawal-unconfirmed-frozen" {
			face.State = "frozen"
			face.Detail = "密钥副本未确认撤回：WSL 侧文件已被手动修改，请处理后重新接管"
			return face
		}
	}
	return face
}

func wslOpenCodeLayerFace(distro string) WSLConfigSupplyFace {
	face := WSLConfigSupplyFace{Key: "opencode-layer", Label: "OpenCode 附加层", State: "unchecked", Detail: "本进程内尚未写入"}
	if rec, ok := lookupWSLOpenCodeLayer(distro); ok {
		face.LastCheck = rec.At.Format(time.RFC3339)
		if rec.OK {
			face.State = "synced"
			face.Detail = rec.Detail
		} else {
			face.State = "failed"
			face.Detail = "上次写入失败：" + rec.Detail
		}
	}
	return face
}

func wslFrozenReasonText(reason string) string {
	switch reason {
	case "unmanaged-existing":
		return "WSL 侧已有同名文件（用户自有），保留本地待确认接管"
	case "local-modification-frozen":
		return "WSL 侧条目已被手动修改，保留本地（预览后可重新接管）"
	case "local-deletion-frozen":
		return "WSL 侧条目已被删除，保留该状态待确认"
	case "both-changed-frozen":
		return "双侧均有修改：保留 WSL 现状并列报告冲突（不以时间戳仲裁）"
	case "conversion-blocked":
		return "存在不可安全转换的条目（Windows 专属形态/未验证路径），保持 WSL 现状"
	case "withdrawal-unconfirmed-frozen":
		return "密钥副本未确认撤回：WSL 侧文件已被手动修改"
	default:
		if reason == "" {
			return "已冻结（保留 WSL 侧修改）"
		}
		return "已冻结：" + reason
	}
}

// GrantWSLVisionConsent 授予当前 distro 的视觉清单共享同意（密钥跨界授权），
// 随后异步推送一次（不阻塞 UI）。已同意时幂等成功。
func (a *App) GrantWSLVisionConsent() error {
	if a == nil || a.WSLSupply == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	granter, ok := a.WSLSupply.(interface {
		GrantUnitConsent(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID) error
	})
	if !ok {
		return fmt.Errorf("当前供给运行器不支持共享授权")
	}
	distro := wslDefaultDistroFn(nil)
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	if err := granter.GrantUnitConsent(context.Background(), distro, wslsupply.CLICommon, wslsupply.UnitSharedVisionExport); err != nil {
		return fmt.Errorf("授权失败：%v", err)
	}
	a.Log.Info("wslsupply", "已授权向 WSL 共享视觉清单（密钥副本将以 0600 写入）", "distro="+distro)
	a.scheduleWSLSupplyPush()
	return nil
}

// ---------------------------------------------------------------------------
// P3-S2：Pi 配置键级投影（settings/amagi/mcp）——来源与事实源 + 动作面
// ---------------------------------------------------------------------------

// wslPiConfigSource 把 Windows 侧 pi agent 配置文件暴露为投影来源（经
// piconfig 显式目标窄接口读取；缺失=键集为空，读失败=source-unavailable）。
type wslPiConfigSource struct{ app *App }

func (s *wslPiConfigSource) agentDir() string {
	if dir, err := piconfig.StandardAgentDir(); err == nil {
		return dir
	}
	return ""
}

func (s *wslPiConfigSource) SettingsJSON() ([]byte, error) {
	return s.readAgentFile("settings.json")
}

func (s *wslPiConfigSource) AmagiJSON() ([]byte, error) {
	return s.readAgentFile("amagi.json")
}

func (s *wslPiConfigSource) MCPJSON() ([]byte, error) {
	return s.readAgentFile("mcp.json")
}

// readAgentFile 经 piconfig 显式目标接口原样读取（只读；缺失=键集为空）。
func (s *wslPiConfigSource) readAgentFile(name string) ([]byte, error) {
	dir := s.agentDir()
	if dir == "" {
		return nil, fmt.Errorf("windows pi agent dir unavailable")
	}
	if s.app == nil || s.app.PiConfig == nil {
		return nil, fmt.Errorf("pi config service unavailable")
	}
	return s.app.PiConfig.ReadAgentFileAt(dir, name)
}

// KnownProviderIDs 返回 Windows 侧 provider 全集：models.json 注册表 + 内置
// 目录（GetPiModelCatalog 已合并 builtin，含 openai-codex 等 OAuth provider）。
func (s *wslPiConfigSource) KnownProviderIDs() ([]string, error) {
	raw, err := s.app.PiConfig.GetPiModelCatalog()
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Providers []struct {
			Name string `json:"name"`
		} `json:"providers"`
	}
	if json.Unmarshal([]byte(raw), &catalog) != nil {
		return nil, fmt.Errorf("parse pi model catalog: %w", err)
	}
	names := make([]string, 0, len(catalog.Providers))
	for _, p := range catalog.Providers {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return names, nil
}

// wslTransformFactsSource 在 Apply 阶段收集目标事实（登录 shell command -v +
// wslpath 实测映射 + 已部署资产前缀）。一次供给至多两次批量探测（命令/路径
// 各一次 wsl.exe）；探测失败返回错误（适配器按「不可确认」保守处理）。
type wslTransformFactsSource struct{}

func (wslTransformFactsSource) TransformFacts(_ context.Context, target wslsupply.TargetRef) (wslsupply.TransformFacts, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	facts := wslsupply.TransformFacts{
		ResolvedCommands: platform.WSLResolveCommands(target.Distro, "npx", "npm"),
		PathMappings:     map[string]string{},
		HostHomeWindows:  home,
	}
	// 已部署资产前缀：Windows agent 根 / .agents 根 → distro 内对应根。
	if home != "" && target.Home != "" {
		facts.PrefixMappings = []wslsupply.PathPrefixRule{
			{WindowsPrefix: filepath.Join(home, ".pi", "agent"), LinuxPrefix: strings.TrimRight(target.Home, "/") + "/.pi/agent"},
			{WindowsPrefix: filepath.Join(home, ".agents"), LinuxPrefix: strings.TrimRight(target.Home, "/") + "/.agents"},
		}
	}
	return facts, nil
}

// wslPathMappingProbeFn 是 wslpath 实测接缝（生产 platform.WSLTranslatePaths；
// 供扩展与测试替换——S2 的 args/env 路径映射经预扫描收敛到本探测）。
var wslPathMappingProbeFn = platform.WSLTranslatePaths

// collectExtraPathMappings 预扫描 mcp 来源中的待验证路径并经 wslpath 实测
// （未被资产前缀覆盖的独立盘符路径）。
func collectExtraPathMappings(distro string, source wslsupply.PiConfigSource) map[string]string {
	mcpRaw, err := source.MCPJSON()
	if err != nil || len(mcpRaw) == 0 {
		return map[string]string{}
	}
	var root map[string]any
	if json.Unmarshal(mcpRaw, &root) != nil {
		return map[string]string{}
	}
	mcpServers, _ := root["mcpServers"].(map[string]any)
	entries := make([]map[string]any, 0, len(mcpServers))
	for _, raw := range mcpServers {
		if entry, ok := raw.(map[string]any); ok {
			entries = append(entries, entry)
		}
	}
	_, paths := wslsupply.CollectTransformNeeds(entries)
	if len(paths) == 0 {
		return map[string]string{}
	}
	return wslPathMappingProbeFn(distro, paths...)
}

// registerPiConfigAdapters 注册三个键级投影单元（生产装配；facts 源附带
// wslpath 实测映射的扩展包装）。
func registerPiConfigAdapters(coord *wslsupply.Coordinator, source wslsupply.PiConfigSource) {
	facts := piConfigFactsWithPaths{base: wslTransformFactsSource{}, source: source}
	coord.RegisterProjection(wslsupply.NewPiSettingsAdapter(source, facts))
	coord.RegisterProjection(wslsupply.NewPiAmagiAdapter(source, facts))
	coord.RegisterProjection(wslsupply.NewPiMCPAdapter(source, facts))
}

// piConfigFactsWithPaths 在基础事实之上并入 wslpath 实测映射（一次探测）。
type piConfigFactsWithPaths struct {
	base   wslTransformFactsSource
	source wslsupply.PiConfigSource
}

func (f piConfigFactsWithPaths) TransformFacts(ctx context.Context, target wslsupply.TargetRef) (wslsupply.TransformFacts, error) {
	facts, err := f.base.TransformFacts(ctx, target)
	if err != nil {
		return facts, err
	}
	for win, linux := range collectExtraPathMappings(target.Distro, f.source) {
		if facts.PathMappings == nil {
			facts.PathMappings = map[string]string{}
		}
		facts.PathMappings[win] = linux
	}
	return facts, nil
}

// ---------------------------------------------------------------------------
// P3-S2 动作面（D4 动作分离：检查 / 预览同步 / 应用安全更新 / 保留本地 /
// 确认接管——没有强制覆盖全部）与状态页逐单元详情
// ---------------------------------------------------------------------------

// WSLConfigUnitKeyPreview 是预览面上单个键的脱敏动作（敏感项只显示键名与方向）。
type WSLConfigUnitKeyPreview struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// WSLConfigUnitPreview 是一个键级单元的预览结果（含 CAS 令牌 targetHash，
// 仅供「确认接管」回传，不属展示内容）。
type WSLConfigUnitPreview struct {
	Unit       string                    `json:"unit"`
	RelPath    string                    `json:"relPath"`
	Policy     string                    `json:"policy"`
	Action     string                    `json:"action"`
	Blocked    string                    `json:"blockedReason,omitempty"`
	Frozen     bool                      `json:"frozen"`
	TargetHash string                    `json:"targetHash"`
	Notes      []string                  `json:"notes,omitempty"`
	Keys       []WSLConfigUnitKeyPreview `json:"keys"`
}

// WSLConfigUnitOutcome 是「应用安全更新」后单单元的脱敏结果。
type WSLConfigUnitOutcome struct {
	Unit    string `json:"unit"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail"`
}

// piConfigUnitsForActions 是动作面作用的键级单元清单（固定顺序）。
var piConfigUnitsForActions = []wslsupply.UnitID{
	wslsupply.UnitPiSettings, wslsupply.UnitPiAmagi, wslsupply.UnitPiMCP,
}

// piConfigCoordinator 返回支持 S2 操作面的协调器切片（nil 表示未接线）。
func (a *App) piConfigCoordinator() interface {
	PreviewUnits(ctx context.Context, distro string, cli wslsupply.CLI, units []wslsupply.UnitID) ([]wslsupply.UnitPreview, error)
	DropUnitRegistration(distro string, cli wslsupply.CLI, unit wslsupply.UnitID) error
	TakeOverUnit(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID, expectedTargetHash string) (wslsupply.UnitResult, error)
} {
	if a == nil || a.WSLSupply == nil {
		return nil
	}
	coord, _ := a.WSLSupply.(interface {
		PreviewUnits(ctx context.Context, distro string, cli wslsupply.CLI, units []wslsupply.UnitID) ([]wslsupply.UnitPreview, error)
		DropUnitRegistration(distro string, cli wslsupply.CLI, unit wslsupply.UnitID) error
		TakeOverUnit(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID, expectedTargetHash string) (wslsupply.UnitResult, error)
	})
	return coord
}

// wslDefaultDistro 返回当前 WSL 目标（空串=不可用；经可注入接缝）。
func (a *App) wslDefaultDistro() string { return wslDefaultDistroFn(nil) }

// CheckWSLPiConfigSupply 执行「检查」：对当前目标做只读 Inspect（可唤醒当前
// 目标），刷新状态页；不写任何配置。
func (a *App) CheckWSLPiConfigSupply() WSLConfigSupplyStatus {
	if a == nil || a.WSLSupply == nil {
		return WSLConfigSupplyStatus{}
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return a.GetWSLConfigSupplyStatus()
	}
	// 检查=只读探测：经 PreviewUnits 的 Inspect 路径刷新缓存（可唤醒当前
	// 目标；不写任何配置）。
	if coord := a.piConfigCoordinator(); coord != nil {
		units := wslSupplyUnitsFiltered(a.WSLSupply, piConfigUnitsForActions...)
		if len(units) > 0 {
			_, _ = coord.PreviewUnits(context.Background(), distro, wslsupply.CLIPi, units)
		}
	}
	return a.GetWSLConfigSupplyStatus()
}

// PreviewWSLPiConfigSupply 执行「预览同步」：Inspect + Plan（纯计算），返回
// 逐单元/逐键动作与接管 CAS 令牌；不产生写入。
func (a *App) PreviewWSLPiConfigSupply() []WSLConfigUnitPreview {
	out := []WSLConfigUnitPreview{}
	coord := a.piConfigCoordinator()
	if coord == nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out
	}
	units := wslSupplyUnitsFiltered(a.WSLSupply, piConfigUnitsForActions...)
	previews, err := coord.PreviewUnits(context.Background(), distro, wslsupply.CLIPi, units)
	if err != nil {
		a.Log.Warn("wslsupply", "Pi 配置预览失败", err.Error())
		return out
	}
	for _, pv := range previews {
		row := WSLConfigUnitPreview{
			Unit: string(pv.Unit), RelPath: pv.RelPath, Policy: string(pv.Policy),
			Action: piPreviewActionName(pv.Action), Blocked: pv.BlockedReason,
			Frozen: pv.Freeze, TargetHash: pv.TargetHash,
			Notes: append([]string(nil), pv.Notes...), Keys: []WSLConfigUnitKeyPreview{},
		}
		for _, k := range pv.Keys {
			row.Keys = append(row.Keys, WSLConfigUnitKeyPreview{Key: k.Key, Action: k.Action, Reason: k.Reason})
		}
		out = append(out, row)
	}
	return out
}

func piPreviewActionName(action wslsupply.Action) string {
	switch action {
	case wslsupply.ActionNone:
		return "none"
	case wslsupply.ActionWriteProjection:
		return "write"
	case wslsupply.ActionBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// ApplyWSLPiConfigSupply 执行「应用安全更新」：只更新未冻结键（冻结/未托管/
// 转换受阻键天然保持现状——安全路径即 RunUnits 本身）。
func (a *App) ApplyWSLPiConfigSupply() []WSLConfigUnitOutcome {
	out := []WSLConfigUnitOutcome{}
	if a == nil || a.WSLSupply == nil {
		return out
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out
	}
	units := wslSupplyUnitsFiltered(a.WSLSupply, piConfigUnitsForActions...)
	if len(units) == 0 {
		return out
	}
	res, err := a.WSLSupply.RunUnits(context.Background(), distro, wslsupply.CLIPi, wslsupply.ReasonManual, units)
	if err != nil {
		a.Log.Warn("wslsupply", "Pi 配置安全更新失败", err.Error())
		return out
	}
	for _, r := range res.Results {
		if !isPiConfigUnit(r.Unit) {
			continue
		}
		out = append(out, WSLConfigUnitOutcome{Unit: string(r.Unit), Outcome: r.Outcome.String(), Detail: r.Detail})
	}
	return out
}

func isPiConfigUnit(unit wslsupply.UnitID) bool {
	return unit == wslsupply.UnitPiSettings || unit == wslsupply.UnitPiAmagi || unit == wslsupply.UnitPiMCP
}

// KeepWSLPiConfigUnitLocal 执行「保留本地（停跟随）」：目标内容不动，后续
// 供给零动作；显式接管可解除。
func (a *App) KeepWSLPiConfigUnitLocal(unit string) error {
	coord := a.piConfigCoordinator()
	if coord == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	if !isPiConfigUnit(wslsupply.UnitID(unit)) {
		return fmt.Errorf("未知配置单元：%s", unit)
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	if err := coord.DropUnitRegistration(distro, wslsupply.CLIPi, wslsupply.UnitID(unit)); err != nil {
		return fmt.Errorf("停跟随失败：%v", err)
	}
	a.Log.Info("wslsupply", "已选择保留 WSL 本地配置（停跟随）", "unit="+unit)
	return nil
}

// TakeOverWSLPiConfigUnit 执行「确认接管（预览后）」：以预览目标 hash 做 CAS，
// 用当前来源覆写托管域并重建键级基准；旧预览过期则拒绝并要求重新检查。
func (a *App) TakeOverWSLPiConfigUnit(unit, expectedTargetHash string) error {
	coord := a.piConfigCoordinator()
	if coord == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	if !isPiConfigUnit(wslsupply.UnitID(unit)) {
		return fmt.Errorf("未知配置单元：%s", unit)
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	res, err := coord.TakeOverUnit(context.Background(), distro, wslsupply.CLIPi, wslsupply.UnitID(unit), expectedTargetHash)
	if err != nil {
		return fmt.Errorf("接管失败：%v", err)
	}
	switch res.Outcome {
	case wslsupply.UnitApplied:
		a.Log.Info("wslsupply", "已按预览确认接管 WSL 配置单元", fmt.Sprintf("unit=%s", unit))
		return nil
	case wslsupply.UnitBlocked:
		return fmt.Errorf("预览已过期（WSL 侧文件已变化，旧预览不覆盖新编辑）；请重新检查后再接管")
	default:
		return fmt.Errorf("接管失败：%s", res.Detail)
	}
}

// ---------------------------------------------------------------------------
// 状态页逐单元详情（配置状态区扩展：相对路径/归属策略/冻结原因/失败阶段/
// 可执行动作；敏感项只显示文件名与方向）
// ---------------------------------------------------------------------------

// WSLConfigUnitDetail 是一个键级单元的状态页详情（脱敏）。
type WSLConfigUnitDetail struct {
	Unit    string   `json:"unit"`    // pi/settings 等
	RelPath string   `json:"relPath"` // settings.json / amagi.json / mcp.json（只显示文件名）
	Policy  string   `json:"policy"`  // TrackedProjection
	State   string   `json:"state"`   // synced / stale / frozen / partial / detached / failed / unchecked
	Detail  string   `json:"detail"`  // 中文短说明（冻结原因/失败阶段）
	Actions []string `json:"actions"` // 可执行动作 key
}

var piConfigUnitMeta = map[wslsupply.UnitID]struct{ relPath, label string }{
	wslsupply.UnitPiSettings: {relPath: wslsupply.PiSettingsFile, label: "settings.json 偏好（白名单字段）"},
	wslsupply.UnitPiAmagi:    {relPath: wslsupply.PiAmagiFile, label: "amagi.json 角色与路由"},
	wslsupply.UnitPiMCP:      {relPath: wslsupply.PiMCPFile, label: "mcp.json 服务器（逐个转换）"},
}

// piConfigUnitDetails 从状态清单派生键级单元详情。
func piConfigUnitDetails(st wslsupply.TargetStatus, pendingPush bool) []WSLConfigUnitDetail {
	out := []WSLConfigUnitDetail{}
	for _, unit := range piConfigUnitsForActions {
		meta, ok := piConfigUnitMeta[unit]
		if !ok {
			continue
		}
		detail := WSLConfigUnitDetail{
			Unit: string(unit), RelPath: meta.relPath, Policy: string(wslsupply.PolicyTrackedProjection),
			State: "unchecked", Detail: "尚未检查", Actions: []string{"check", "preview"},
		}
		var entry *wslsupply.UnitStatus
		for i := range st.Units {
			if st.Units[i].Unit == unit {
				entry = &st.Units[i]
				break
			}
		}
		if entry == nil {
			out = append(out, detail)
			continue
		}
		switch {
		case entry.Detached:
			detail.State = "detached"
			detail.Detail = "已保留 WSL 本地（停跟随）；确认接管可重新启用"
			detail.Actions = []string{"preview", "takeover"}
		case entry.LastOutcome == "awaiting-consent" && !entry.Consented:
			// F-3（S2 diting 裁定）：三键级单元首启需 per-target 同意——未同意
			// 只读不写，状态页给「启用投影」入口（与视觉清单同一 UI 模式）。
			detail.State = "awaiting-consent"
			detail.Detail = "尚未同意向 WSL 投影该配置（同意后未手改条目跟随 Windows 面板）"
			detail.Actions = []string{"consent", "preview"}
		case entry.Frozen:
			detail.State = "frozen"
			detail.Detail = wslFrozenReasonText(entry.FrozenReason)
			detail.Actions = []string{"check", "preview", "keep-local", "takeover"}
		case entry.LastErrorCode != "":
			detail.State = "failed"
			detail.Detail = "上次同步失败：" + entry.LastErrorCode
			detail.Actions = []string{"check", "preview", "apply"}
		case entry.LastOutcome == "partial":
			detail.State = "partial"
			detail.Detail = "部分条目保持 WSL 现状（冻结/未托管/转换受阻）；预览查看逐项"
			detail.Actions = []string{"check", "preview", "apply", "keep-local", "takeover"}
		case entry.LastOutcome == "written" || entry.LastOutcome == "takeover-written":
			if pendingPush {
				detail.State = "stale"
				detail.Detail = "有保存后的变更待推送"
			} else {
				detail.State = "synced"
				detail.Detail = "已同步（未手改的条目跟随 Windows 面板）"
			}
			detail.Actions = []string{"check", "preview", "apply"}
		default:
			detail.Actions = []string{"check", "preview", "apply"}
		}
		out = append(out, detail)
	}
	return out
}

// verdictForWSLPiConfigSupply 是纯配置供给（无 A2 单元）结果的闸门判定
// （P3-S2）：仅 managed-models 的 target-corrupt 阻断（后续 overlay 会破坏
// 损坏文件）；其余失败/阻断告警放行（可选偏好面，design §六）。桌面闸门与
// remote effect 共用本函数——同一目标同一结果。
func verdictForWSLPiConfigSupply(res wslsupply.RunResult, runErr error) (launchErr error, warnMsg, warnDetail string) {
	if runErr != nil {
		return nil, "WSL 托管配置分发失败（会话继续，按既有回退语义）", runErr.Error()
	}
	for i := range res.Results {
		r := res.Results[i]
		if r.Outcome == wslsupply.UnitFailed && r.Detail == "target-corrupt" && r.Unit == wslsupply.UnitPiManagedModels {
			return fmt.Errorf("WSL 内 Pi models.json 已损坏，未覆盖（请手动修复 %s 后重试）；本次启动未执行", res.Target.Distro), "", ""
		}
		if r.Outcome != wslsupply.UnitFailed && r.Outcome != wslsupply.UnitBlocked {
			continue
		}
		if r.Detail == "awaiting-consent" || strings.Contains(r.Detail, "frozen") ||
			strings.Contains(r.Detail, "unmanaged-existing") || strings.Contains(r.Detail, "detached") ||
			strings.Contains(r.Detail, "conversion-blocked") {
			continue // 预期态：状态页可见，不作为启动告警面
		}
		return nil, "WSL 托管配置分发未完成（会话继续，按既有回退语义）", fmt.Sprintf("unit=%s detail=%s", r.Unit, r.Detail)
	}
	return nil, "", ""
}

// EnableWSLPiConfigSupply 授予当前 distro 的 Pi 配置投影同意（F-3，S2 diting
// 裁定：settings/amagi/mcp 三单元首启前需 per-target 明确同意——Windows 用户
// 配置跨界是授权动作）。授权后异步推送一次；已同意幂等。distro 重装（generation
// 变化）后旧授权自动失效，需重新同意（与视觉清单共享 GrantUnitConsent 语义）。
func (a *App) EnableWSLPiConfigSupply() error {
	if a == nil || a.WSLSupply == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	granter, ok := a.WSLSupply.(interface {
		GrantUnitConsent(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID) error
	})
	if !ok {
		return fmt.Errorf("当前供给运行器不支持投影授权")
	}
	distro := wslDefaultDistroFn(nil)
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	for _, unit := range piConfigUnitsForActions {
		if err := granter.GrantUnitConsent(context.Background(), distro, wslsupply.CLIPi, unit); err != nil {
			return fmt.Errorf("授权失败（%s）：%v", unit, err)
		}
	}
	a.Log.Info("wslsupply", "已同意向 WSL 投影 Pi 配置（settings/amagi/mcp；未手改条目将跟随 Windows 面板）", "distro="+distro)
	a.scheduleWSLSupplyPush()
	return nil
}
