package main

// app_wsl_supply.go — WSL 托管配置分发的 App 编排面：
//
//   - 业务快照源（SharedFileSource 的 App 实现）：共享小文件内容；
//   - 启动 lazy 闸门：WSL 会话启动前补漏 shared 小文件与 claude/codex 键级
//     单元（视觉清单资格门 fail-closed；其余失败 → 告警沿用既有回退）；
//   - 保存标脏接真实调度：MarkDirty 后对「已运行且无活动会话」的目标异步推送
//     （停机 distro 只标不唤醒；single-flight 合并推送风暴）；
//   - per-target 视觉清单共享同意（密钥跨界授权入口）；
//   - 最小状态面（GetWSLConfigSupplyStatus：视觉清单/学习层/附加层 已同步/
//     陈旧/失败/冻结/待同意 + 最后检查时间；OpenCode 附加层为进程内记录）。
//
// Pi 的供给单元（托管 models.json、settings/amagi/mcp 键级投影、登录凭据
// A2）已随 1.3.126「WSL 内 Pi 共享 Windows agent 根」退役，不再向 distro
// 分发任何 Pi 配置。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/piplugin"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/wslsupply"
)

// ---------------------------------------------------------------------------
// 业务快照源
// ---------------------------------------------------------------------------

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

// newWSLSupplyCoordinator 构造生产协调器并注册 shared 小文件与 S4 键级投影
// 适配器（Pi 的 S1/S2 供给单元已退役）。
func newWSLSupplyCoordinator(app *App, configDir string) *wslsupply.Coordinator {
	if appLogHook == nil && app.Log != nil {
		appLogHook = func(scope, msg, detail string) { app.Log.Warn(scope, msg, detail) }
	}
	coord := wslsupply.NewCoordinator(configDir, wslsupplySupplyLog{log: app.Log}, wslsupply.DefaultProbes())
	coord.RegisterProjection(wslsupply.NewVisionExportAdapter(&wslSharedFileSource{app: app}))
	coord.RegisterProjection(wslsupply.NewModalityKBAdapter(&wslSharedFileSource{app: app}))
	// P3-S4：Claude/Codex 用户配置导入（键级单元 + 资源目录单元）。
	registerS4Adapters(coord, app)
	// 共享技能：1.3.130 起不再注册 P3-S3 作用域（WSL 内 ~/.agents/skills 由
	// 受管符号链接直连 Windows 技能根，见 app_wsl_shared_skills.go）。
	// P3-S5：插件重放解析面（piplugin 单一来源；wslsupply 经接缝注入）。
	coord.SetPluginSourceParser(wslPluginSourceParserAdapter, wslsupply.PluginRefChecker{
		ExactNpmVersion: piplugin.IsExactNpmVersion,
		FullCommitSHA:   piplugin.IsFullCommitSHA,
	})
	// P2-B：扫描完成回调——skills/资源后台扫描真正结束后触发统一对账推送
	//（避免启用时调度一次早于扫描结束的推送永远不同步）。
	coord.SetScanCompletionCallback(app.wslUnifiedOnScanDone)
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

// runWSLSharedFilesAtLaunch 是 SA-09（wsl-goal-closure）的共享小文件启动
// 补漏/撤回闸门：会话启动前对已注册的 shared 作用域小文件（视觉清单导出
// shared/vision-export、多模态学习层 shared/modality-kb）执行一次供给运行，
// 覆盖「停机期间 Windows 侧关闭导出/删除 provider → 保存推送被停机守卫跳过
// → 旧密钥副本无限留盘继续被会话内工具消费」的缺口。
//
// 边界（冻结接口，C1 接线五 CLI desktop/remote/restart）：
//   - 只处理上述两个 shared 单元：不扫描 skills、不安装插件、不联网；
//   - 普通启动不因「从未授权」阻断：无授权且状态面确认无受管副本的
//     awaiting-consent/unmanaged 等预期态返回 nil；
//   - 受管敏感副本（视觉清单）可能存在时，任何不能确认其安全/撤回完成的
//     结果——RunUnits 错误、UnitFailed（含 withdrawal-remove-failed、staging
//     unverified、写失败）、withdrawal-unconfirmed-frozen——返回明确 error
//     阻断会话（未知不能推断旧副本安全）；
//   - modality-kb 是布尔结论非敏感面：失败不阻断（状态页可见）；
//   - ctx 已取消 → 返回 ctx.Err()（取消传播给启动链）。
//
// 调用时机：已解析 distro、PTY 启动之前。内部 Go API，不进 Wails 绑定。
// runWSLSharedFilesForSpec is shared by desktop activation and the remote/restart
// PTY effect. Keep this out of BuildPlan: distro probing and supply are effects.
func runWSLSharedFilesForSpec(ctx context.Context, runner wslsupply.Runner, spec platform.ResolvedLaunchSpec) error {
	if spec.BootstrapMode != platform.BootstrapWSL || runner == nil {
		return nil
	}
	distro := wslDefaultDistroFn(spec.Env.Variables)
	if err := runWSLSharedFilesAtLaunch(ctx, runner, distro); err != nil {
		return err
	}
	// P2-B 统一管理：Claude/Codex 键级单元的启动 lazy 补漏（只告警不阻断；
	// pi 共享 Windows agent 根无需补漏，资源/技能不进启动链）。
	runWSLCliConfigAtLaunch(ctx, runner, distro, spec.AppType)
	return nil
}

func runWSLSharedFilesAtLaunch(ctx context.Context, runner wslsupply.Runner, distro string) error {
	if runner == nil || strings.TrimSpace(distro) == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	units := wslSupplyUnitsFiltered(runner, wslsupply.UnitSharedVisionExport, wslsupply.UnitSharedModalityKB)
	if len(units) == 0 {
		return nil // 裸运行器无 shared 适配器：不阻断普通启动
	}
	res, runErr := runner.RunUnits(ctx, distro, wslsupply.CLICommon, wslsupply.ReasonLaunch, units)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var visionResult *wslsupply.UnitResult
	for i := range res.Results {
		if res.Results[i].Unit == wslsupply.UnitSharedVisionExport {
			visionResult = &res.Results[i]
		}
	}
	if runErr != nil {
		// 供给运行不可用：由资格核验按事实裁决（无副本线索 → 放行；未知/
		// unsafe → 阻断），不按“无历史即安全”放行。
		if err := gateWSLSharedVisionConsumption(ctx, runner, distro, units, res, nil); err != nil {
			return fmt.Errorf("WSL 共享视觉清单供给运行失败（%v）；%v", runErr, err)
		}
		return nil
	}
	if visionResult != nil && visionResult.Outcome == wslsupply.UnitBlocked &&
		strings.Contains(visionResult.Detail, "withdrawal-unconfirmed") {
		return fmt.Errorf("WSL 内视觉清单密钥副本未确认撤回（%s 目标被手动修改而宿主已撤回导出）；本次启动不消费该副本，请在 WSL 内处理 ~/.agents/amagi-media-models.json 后重试", distro)
	}
	// 学习层（modality-kb）非敏感：失败/阻断不阻断启动。
	return gateWSLSharedVisionConsumption(ctx, runner, distro, units, res, visionResult)
}

// wslSensitiveAssessor 是启动闸门消费的资格核验面（真实 Coordinator 提供；
// 测试 stub 运行器通常不实现——配合单元注册面判定能力缺失）。
type wslSensitiveAssessor interface {
	AssessWSLSensitiveConsumption(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID, res *wslsupply.UnitResult) wslsupply.SensitiveAssessment
	ConfirmLegacyVisionAbsent(ctx context.Context, distro string) error
}

// gateWSLSharedVisionConsumption 是共享视觉清单的启动消费资格门（攻坚契约：
// 真实 assessment/result facts，不靠 LastOutcome 字符串或一个 bool 历史）。
// 本次写已发生/未知、pending、unsafe/unknown 缺资格不放行；Applied/Fresh/
// WarnDegraded 也不能跳过已知风险；请求了敏感单元却缺资格能力不是安全证据；
// 无历史/确无当前副本的普通可选失败不误阻。
func gateWSLSharedVisionConsumption(ctx context.Context, runner wslsupply.Runner, distro string, units []wslsupply.UnitID, res wslsupply.RunResult, visionResult *wslsupply.UnitResult) error {
	requested := false
	for _, u := range units {
		if u == wslsupply.UnitSharedVisionExport {
			requested = true
		}
	}
	if !requested {
		return nil
	}
	if assessor, ok := runner.(wslSensitiveAssessor); ok {
		// 生产路径：真实协调器的逐目标资格核验（真实 assessment/result facts）。
		assessment := assessor.AssessWSLSensitiveConsumption(ctx, distro, wslsupply.CLICommon, wslsupply.UnitSharedVisionExport, visionResult)
		switch assessment.Consumption {
		case wslsupply.ConsumptionSafe, wslsupply.ConsumptionAbsent, wslsupply.ConsumptionUserOwned, wslsupply.ConsumptionNotApplicable:
			if assessment.Reason == "legacy-clue-present-copy-absent" {
				// 手动处理后重新检查/启动即真实解除：持久记录该完整身份的 absent
				// 处置（best-effort；失败仅告警，证据保留）。
				if cerr := assessor.ConfirmLegacyVisionAbsent(ctx, distro); cerr != nil {
					logWSLSharedVisionConfirmWarn(cerr)
				}
			}
			return nil
		case wslsupply.ConsumptionUnsafe:
			if strings.HasPrefix(assessment.Reason, "legacy") {
				return fmt.Errorf("WSL 目标 %s 存在未确认的旧版密钥副本（%s，文件 %s）；请在 WSL 内手动处理该文件后重试启动（处理后本目标将自动核验解除），或重新授权视觉清单共享完成受管接管", distro, assessment.Reason, "~/.agents/amagi-media-models.json")
			}
			return fmt.Errorf("WSL 内视觉清单密钥副本状态未确认（%s）；本次启动不消费该副本，请检查 WSL 内 ~/.agents/amagi-media-models.json 或重试", assessment.Reason)
		default: // unknown / 未判定
			return fmt.Errorf("WSL 内视觉清单密钥副本安全状态无法判定（%s）；本次启动不消费该副本，请检查 WSL 内 ~/.agents/amagi-media-models.json 或重试", assessment.Reason)
		}
	}
	// 无资格核验面的运行器（测试 stub / 裸运行器）：退回持久事实谓词——已知
	// 副本可能仍在时阻断；确无副本线索的普通可选失败不误阻（生产协调器恒走
	// 上方真实 assessment 面）。
	type sensitiveCopyProber interface {
		SensitiveCopyPending(distro string, unit wslsupply.UnitID) bool
		LegacySensitiveCopyPending(distro string) bool
	}
	if p, ok := runner.(sensitiveCopyProber); ok {
		if p.SensitiveCopyPending(distro, wslsupply.UnitSharedVisionExport) || p.LegacySensitiveCopyPending(distro) {
			return fmt.Errorf("WSL 内视觉清单密钥副本可能仍在（受管副本未确认撤回或存在迁移遗留线索）；本次启动不消费该副本，请检查 WSL 内 ~/.agents/amagi-media-models.json 或重试")
		}
	}
	return nil
}

// logWSLSharedVisionConfirmWarn 记录 absent 确认落盘失败（不影响放行判定）。
func logWSLSharedVisionConfirmWarn(err error) {
	if appLogHook != nil {
		appLogHook("wslsupply", "旧副本 absent 确认未持久化（下次启动重新核验）", err.Error())
	}
}

// appLogHook 是无 App 上下文路径的日志接缝（测试注入；生产由 App 装配设置）。
var appLogHook func(scope, msg, detail string)

// 保存标脏 → 异步推送（已运行 distro + 无活动会话）
// ---------------------------------------------------------------------------

// wslSupplyPushState 是异步推送的 single-flight 状态：同一时刻至多一个推送
// 工作循环在执行，期间的标脏合并为一次补推（推送风暴不放大跨界次数）。
// SA-08（wsl-goal-closure）：改为「单 worker 循环消费 rerun」——旧实现的
// defer 内递归调用会再次命中 running=true 而直接返回，running 永不释放，
// 此后所有保存推送停摆直到进程重启。
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
// 保存成功」与「WSL 已同步」是两个结果）。后台 goroutine 由包级 WaitGroup
// 追踪（PR09：测试可确定性等待推送生命周期真正退出后再还原全局接缝，
// 不 sleep 碰运气）。
var wslSupplyPushWG sync.WaitGroup

func (a *App) scheduleWSLSupplyPush() {
	wslSupplyPushWG.Add(1)
	go func() {
		defer wslSupplyPushWG.Done()
		a.pushWSLSupplyOnce()
	}()
}

// waitForWSLSupplyPushIdleForTest 等待全部已调度的后台推送退出（PR09：
// 仅供测试 cleanup 使用——Add 在 schedule 内同步完成，Enable/保存调用
// 返回后本等待即覆盖其全部后台生命周期）。
func waitForWSLSupplyPushIdleForTest() {
	wslSupplyPushWG.Wait()
}

// pushWSLSupplyOnce 是 single-flight 入口：已在工作时只置 rerun 标记；
// 工作循环完成后 rerun 仍置位则就地再消费一轮，所有退出路径都释放 running
// （SA-08：等锁期间/执行中再次标脏的保存不得永久停摆后续推送）。
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
		wslSupplyPushState.running = false
		wslSupplyPushState.mu.Unlock()
	}()
	for {
		a.doPushWSLSupply()
		wslSupplyPushState.mu.Lock()
		if !wslSupplyPushState.rerun {
			wslSupplyPushState.mu.Unlock()
			return
		}
		wslSupplyPushState.rerun = false
		wslSupplyPushState.mu.Unlock()
	}
}

// doPushWSLSupply 执行一轮实际推送（守卫：停机 distro 不唤醒、活动会话暂停）。
func (a *App) doPushWSLSupply() {
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
		// 活动会话期间暂停后台应用（design §三）。P4-F2：保留待对账标记，
		// 会话归零后由统一管理后台循环异步补推（无需用户再保存一次）。
		a.wslUnifiedMarkPushPending()
		return
	}
	// P4-F3：统一管理固定单元的幂等续接（未启用统一管理时为 no-op；已就绪
	// 分组零成本跳过）——先补授权/物化，本推送后续分支随写。
	a.reconcileWSLUnifiedFixedUnits(distro)
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
	// P2-B 统一管理增量：claude/codex 键级单元（含选择集物化）与技能/资源
	// 对账（未启用统一管理时两者都是 no-op，行为与现状一致）。
	a.pushWSLUnifiedCliConfig(context.Background(), distro)
	a.reconcileWSLUnifiedResources(distro)
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
	// P3-S4：同名冲突默认保留本地（S1 语义）；仅用户在选择面勾选的覆盖项
	// 写入附加层（allowed=nil——本批不引入逐项导入门，preset 本身即显式选择）。
	sanitized, report := launcher.AdaptWSLOpenCodeMCPWithOptions(distro, runtimeCfg, nil, a.wslOpenCodeMCPOverrideSet(distro))
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
	// Unified（P2-B）：当前 distro 是否已启用统一配置管理（一次授权后启动/
	// 保存/对账自动同步）。false = 显示「启用统一管理」入口。
	Unified bool `json:"unified"`
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
	// P2-B：统一管理状态（只读标记，不探测）。
	out.Unified = a.wslUnifiedCached(distro)

	pending := map[string]uint64{}
	sharedStatus := a.wslSupplyStatus(distro, wslsupply.CLICommon)
	if sharedStatus.PendingSources != nil {
		for k, v := range sharedStatus.PendingSources {
			pending[k] = v
		}
	}
	out.PendingPush = len(pending) > 0

	out.Faces = append(out.Faces,
		wslVisionFace(sharedStatus, out.PendingPush),
		wslFaceFromUnit("modality-kb", "多模态学习层", sharedStatus, wslsupply.UnitSharedModalityKB, out.PendingPush),
		wslOpenCodeLayerFace(distro),
		wslSharedSkillsFace(),
		wslPluginsFace(a),
	)
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
		case u.PendingOperation != "":
			// 攻坚契约：未决持久操作优先于旧 written/consented——不得显示
			// 「已同步」掩盖待确认事实。
			face.State = "pending-op"
			face.Detail = "有未完成的写入操作待确认（" + u.LastAttemptOutcome + "）；重启后事实保留，待下次检查收敛"
		case u.StateUnpersisted:
			face.State = "pending-op"
			face.Detail = "内容已写入 WSL 但登记未持久化；重启后会重新核验"
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
	if face.State == "synced" && len(st.PendingIntents) > 0 {
		// 未决操作摘要存在时不得宣称已同步（事实优先于旧 LastSuccess）。
		face.State = "pending-op"
		face.Detail = "有未完成的供给操作待确认；重启后事实保留"
	}
	for _, u := range st.Units {
		if u.Unit == wslsupply.UnitSharedVisionExport && u.LastOutcome == "withdrawn" {
			// SA-09：撤回终态——密钥副本已从 WSL 移除，不虚报「已同步」。
			face.State = "unchecked"
			face.Detail = "已撤回（WSL 内密钥副本已删除）；重新开启导出后会再次发布"
			return face
		}
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

// wslPluginsFace 聚合插件重放面卡片（S5）：只读登记摘要（不唤醒；启动路径
// 的「缺失插件」提示数据源——只提示，不自动安装）。Installed=false 的登记
// （缺失/上次失败）以 warning 语义呈现，不冒充已同步。
func wslPluginsFace(a *App) WSLConfigSupplyFace {
	face := WSLConfigSupplyFace{Key: "pi-plugins", Label: "Pi 插件（WSL 重放）", State: "unchecked", Detail: "尚无本系统登记的 WSL 插件重放"}
	coord := a.wslPluginCoordinator()
	if coord == nil {
		face.Detail = "插件重放未启用"
		return face
	}
	s := coord.PluginRegistrationSummary()
	if s.Managed == 0 {
		return face
	}
	if s.LastOpAtRC != "" {
		face.LastCheck = s.LastOpAtRC
	}
	switch {
	case s.Missing > 0 || s.Failed > 0:
		face.State = "failed"
		face.Detail = fmt.Sprintf("登记 %d：已安装 %d，缺失/未完成 %d（启动不会自动安装，请在插件面板显式重放）", s.Managed, s.Installed, s.Missing+s.Failed)
	case s.Frozen > 0:
		face.State = "frozen"
		face.Detail = fmt.Sprintf("登记 %d：已安装 %d，冻结 %d（WSL 侧修改保留本地）", s.Managed, s.Installed, s.Frozen)
	default:
		face.State = "synced"
		face.Detail = fmt.Sprintf("登记 %d 项已重放安装（settings 登记与实体同时有效）", s.Installed)
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
// 键级投影的共享 DTO 与目标事实源（S4 claude/codex 消费；Pi 的三键级单元已退役）
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// S4 动作面共用 DTO（检查 / 预览同步 / 应用安全更新 / 保留本地 / 确认接管
// ——没有强制覆盖全部；Pi 专属动作面已退役）
// ---------------------------------------------------------------------------

// WSLConfigUnitKeyPreview 是预览面上单个键的脱敏动作（敏感项只显示键名与方向）。
type WSLConfigUnitKeyPreview struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	// Before/After：S4 转换前→转换后脱敏摘要（命令形态与参数个数；不含 env
	// 值/密钥）。
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Selected/Importable：S4 选择面（仅选择的条目导入；可勾选状态）。
	Selected   bool `json:"selected"`
	Importable bool `json:"importable"`
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

// wslDefaultDistro 返回当前 WSL 目标（空串=不可用；经可注入接缝）。
func (a *App) wslDefaultDistro() string { return wslDefaultDistroFn(nil) }

func wslPreviewActionName(action wslsupply.Action) string {
	switch action {
	case wslsupply.ActionNone:
		return "none"
	case wslsupply.ActionWriteProjection:
		return "write"
	case wslsupply.ActionRemoveProjection:
		return "remove" // SA-09：撤回（移除受管密钥副本）
	case wslsupply.ActionBlocked:
		return "blocked"
	default:
		return "unknown"
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

// ---------------------------------------------------------------------------
// P3-S3：共享技能（Windows ~/.agents/skills → WSL ~/.agents/skills）。
// 资源清单有界扫描（后台分批，不进会话启动等待）、预览同意导入（一次勾选
// 全部可移植项）、清单增量跟随（TrackedProjection：WSL 手改即冻结）、静态
// 兼容检查（越界/循环/绝对链接、Windows 专用脚本明确未支持——只查不执行）。
// 状态诚实区分「已供给」（文本检查通过）与「运行已验证」（从未宣称——本批
// 不执行任何脚本内容）。
// ---------------------------------------------------------------------------

// wslSkillsHostRoot 返回宿主技能根（Windows ~/.agents/skills；不可解析为空）。
func wslSkillsHostRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "skills")
}

// WSLSkillSummary 是状态页单技能摘要（脱敏：不兼容项只报相对路径与原因）。
type WSLSkillSummary struct {
	Name         string   `json:"name"`
	State        string   `json:"state"` // importable / awaiting-consent / supplied / stale / frozen / incompatible / scan-incomplete / empty / detached / source-deleted / failed / unchecked
	Detail       string   `json:"detail"`
	Incompatible []string `json:"incompatible,omitempty"`
	Excluded     int      `json:"excluded"`
	Files        int      `json:"files"`
	Bytes        int64    `json:"bytes"`
	Selectable   bool     `json:"selectable"` // 预览勾选面：可导入（可移植且未入供给）
	Actions      []string `json:"actions"`
}

// wslSkillSummary 从扫描清单与登记派生单技能摘要（hasEntry=false 表示从未
// 入供给——可勾选导入）。
func wslSkillSummary(m *wslsupply.SkillManifest, entry wslsupply.UnitStatus, hasEntry bool) WSLSkillSummary {
	s := WSLSkillSummary{
		Name: m.Name, Files: len(m.Files), Bytes: m.TotalBytes, Excluded: len(m.Excluded),
		Actions: []string{"preview"},
	}
	for _, it := range m.Incompatible {
		s.Incompatible = append(s.Incompatible, it.RelPath+"："+it.Reason)
	}
	switch {
	case m.Incomplete:
		s.State = "scan-incomplete"
		s.Detail = "扫描未完成（" + m.IncompleteReason + "）：不冒充完成，暂不可供给"
	case len(m.Incompatible) > 0:
		s.State = "incompatible"
		s.Detail = fmt.Sprintf("含 %d 项不兼容内容（越界/绝对/循环链接或 Windows 专用脚本），整目录未供给", len(m.Incompatible))
	case len(m.Files) == 0:
		s.State = "empty"
		s.Detail = "空技能目录（无可移植内容）"
	case !hasEntry:
		s.State = "importable"
		s.Selectable = true
		s.Detail = "可移植，未选择导入；勾选后随预览导入（文件以 0644/目录 0755 写入）"
	default:
		s.State, s.Detail, s.Actions = wslSkillRegisteredState(entry)
	}
	return s
}

// wslSkillRegisteredState 派生已登记技能的状态面（TrackedProjection 语义）。
func wslSkillRegisteredState(entry wslsupply.UnitStatus) (state, detail string, actions []string) {
	switch {
	case entry.Detached:
		return "detached", "已保留 WSL 本地（停跟随）；确认接管可重新启用", []string{"preview", "takeover"}
	case entry.Frozen:
		return "frozen", wslFrozenReasonText(entry.FrozenReason), []string{"preview", "keep-local", "takeover"}
	case entry.LastErrorCode != "":
		return "failed", "上次同步失败：" + entry.LastErrorCode, []string{"preview", "apply"}
	case entry.LastOutcome == "awaiting-consent" && !entry.Consented:
		return "awaiting-consent", "已选择导入但尚未写入目标（重试应用安全更新）", []string{"preview", "apply"}
	case entry.LastOutcome == "written" || entry.LastOutcome == "takeover-written":
		if entry.Stale {
			return "stale", "Windows 侧已变化（按清单增量，仅重写该目录）", []string{"preview", "apply"}
		}
		return "supplied", "已供给（静态检查通过；未执行内容——「已供给」不等于「运行已验证」）", []string{"preview", "apply"}
	case entry.LastOutcome == "consented":
		return "awaiting-consent", "已授权，待写入目标", []string{"preview", "apply"}
	default:
		return "unchecked", "尚未检查", []string{"preview"}
	}
}

// WSLSkillPreview 是预览面上单技能的脱敏动作描述。
type WSLSkillPreview struct {
	Name         string   `json:"name"`
	Portable     bool     `json:"portable"`
	Action       string   `json:"action"`
	Blocked      string   `json:"blockedReason,omitempty"`
	Frozen       bool     `json:"frozen"`
	TargetHash   string   `json:"targetHash"`
	Incompatible []string `json:"incompatible,omitempty"`
	Excluded     int      `json:"excluded"`
	Notes        []string `json:"notes,omitempty"`
}

// WSLSkillsSupplyStatus 是技能面聚合状态（含扫描进度；扫描未完成不冒充完成）。
type WSLSkillsSupplyStatus struct {
	Distro         string            `json:"distro"`
	Available      bool              `json:"available"`
	Reason         string            `json:"reason,omitempty"`
	RootMissing    bool              `json:"rootMissing"`
	ScanStarted    bool              `json:"scanStarted"`
	ScanRunning    bool              `json:"scanRunning"`
	ScanScanned    int               `json:"scanScanned"`
	ScanTotal      int               `json:"scanTotal"`
	ScanIncomplete bool              `json:"scanIncomplete"`
	Skills         []WSLSkillSummary `json:"skills"`
}
