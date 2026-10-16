package wslsupply

// coordinator.go — 供给协调器核心：TargetRef 解析、批量只读 Inspect、纯计算
// Plan、复核后写入的 Apply、保存事件 MarkDirty 与脱敏 GetStatus。
//
// S0 单元范围：A2 凭据（auth.json，经 A2Adapter 委托 launcher，判定语义零变化）
// + TrackedProjection 接缝（测试桩经 RegisterProjection 注册后可驱动完整
// Plan/Apply/Compensate 链路，生产不注册——S1+ 接真实分发）。

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

// Coordinator 是 WSL 配置供给协调器。同一进程内桌面闸门与 remote effect 共用
// 同一实例：applyMu 串行化全部 Apply（同目标的 merge→write→chmod 序列不会交错）。
type Coordinator struct {
	log    Logger
	probes Probes
	state  *stateStore

	applyMu sync.Mutex

	a2          A2Adapter
	projections map[UnitID]ProjectionAdapter
	// skills 是技能资源作用域（S3：shared/skill/<name> 动态单元；nil 表示
	// 未注册——技能单元全部 unknown）。
	skills *SkillsScope
	// plugins 是插件重放作用域（S5：pi 插件原生重放；与单元词汇表分离，
	// 启动链结构性无法触达）。恒非 nil；裸解析面时操作降级 blocked。
	plugins *PluginScope
	// resources 是 S4 用户资源作用域（claude/codex 资源目录单元；未注册的
	// CLI 其资源单元 unknown）。
	resources map[CLI]*ResourceScope
}

// NewCoordinator 创建协调器。configDir 是 CodeBox 配置目录（状态清单落点）；
// probes 为平台探测面（测试注入假实现）。
func NewCoordinator(configDir string, log Logger, probes Probes) *Coordinator {
	if probes.Identity == nil || probes.ToUNC == nil || probes.Chmod == nil {
		probes = DefaultProbes()
	}
	// 单一状态清单实例：文件同步单元与插件重放登记共用同一落盘面（两个
	// stateStore 各持内存副本会互相覆盖对方写入）。
	state := newStateStore(configDir)
	return &Coordinator{
		log:         log,
		probes:      probes,
		state:       state,
		projections: map[UnitID]ProjectionAdapter{},
		resources:   map[CLI]*ResourceScope{},
		plugins:     NewPluginScope(state, nil, nil, PluginRefChecker{}),
	}
}

// SetPluginSourceParser 注入插件重放的生产解析面（App 装配时调用；wslsupply
// 不直接依赖 piplugin 包，解析语义经接缝注入保持单一来源）。
func (c *Coordinator) SetPluginSourceParser(parser PluginSourceParser, refCheck PluginRefChecker) {
	if c.plugins != nil && parser != nil {
		c.plugins.SetSourceParser(parser, refCheck)
	}
}

// SetPluginCommandRunner 替换插件重放执行面（测试接缝；生产默认
// defaultPluginCommandRunner——wsl.exe --exec bash -lc）。
func (c *Coordinator) SetPluginCommandRunner(runner PluginCommandRunner) {
	if c.plugins != nil && runner != nil {
		c.plugins.runner = runner
	}
}

// SetA2Adapter 替换 A2 适配器（测试接缝；生产默认 DefaultA2Adapter）。
func (c *Coordinator) SetA2Adapter(a A2Adapter) {
	if a != nil {
		c.a2 = a
	}
}

func (c *Coordinator) a2Adapter() A2Adapter {
	if c.a2 != nil {
		return c.a2
	}
	return DefaultA2Adapter{}
}

// ---------------------------------------------------------------------------
// TargetRef
// ---------------------------------------------------------------------------

// generationFor 由可取得的 distro 身份标识与 UID/Home 组成目标 generation；
// machine-id 拿不到（稳定身份不可得）时按 GenerationUnknown 处理，不猜测继承。
func generationFor(id platform.WSLUserIdentity) string {
	if !id.OK || id.MachineID == "" {
		return GenerationUnknown
	}
	sum := sha256.Sum256([]byte(id.MachineID + "|" + id.UID + "|" + id.Home))
	return hexEncode(sum[:])
}

// ResolveTarget 解析 distro 上的 CLI 目标（初版默认 distro，无新增选择器）。
// distro 为空、身份探测失败、目标根不可映射均返回错误——目标不可解析时调用方
// fail closed，不得把失败当空目标继续。
func (c *Coordinator) ResolveTarget(_ context.Context, distro string, cli CLI) (TargetRef, error) {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return TargetRef{}, errors.New("wsl distro is required")
	}
	rel, ok := cliTargetRoot(cli)
	if !ok {
		return TargetRef{}, fmt.Errorf("unsupported wsl supply CLI: %q", string(cli))
	}
	ident := c.probes.Identity(distro)
	if !ident.OK {
		return TargetRef{}, fmt.Errorf("cannot resolve default user identity in WSL distro %q", distro)
	}
	linuxRoot := strings.TrimRight(ident.Home, "/")
	if rel != "" {
		linuxRoot += "/" + rel
	}
	uncRoot := c.probes.ToUNC(distro, linuxRoot)
	if uncRoot == "" {
		return TargetRef{}, fmt.Errorf("WSL UNC share unreachable for distro %q", distro)
	}
	return TargetRef{
		Distro: distro, CLI: cli, UID: ident.UID, Home: ident.Home,
		LinuxRoot: linuxRoot, UNCRoot: uncRoot, Generation: generationFor(ident),
	}, nil
}

// ---------------------------------------------------------------------------
// Inspect
// ---------------------------------------------------------------------------

// Inspect 对已解析目标做批量只读探测（文件元数据经 UNC os.Stat，不写、不
// spawn）。快照 mtime 是 UNC 视图值；A2 权威 mtime 判定在适配器内。普通
// （TrackedProjection）单元文件额外计算内容 hash（仅内存快照，供冲突判定）；
// A2 凭据文件不计算 hash。
func (c *Coordinator) Inspect(_ context.Context, target TargetRef) (*Snapshot, error) {
	if strings.TrimSpace(target.Distro) == "" || target.LinuxRoot == "" || target.UNCRoot == "" {
		return nil, errors.New("inspect requires a resolved target")
	}
	snap := &Snapshot{Target: target, InspectedAt: time.Now()}
	hashed := c.projectionFiles(target.CLI)
	seen := map[string]bool{}
	for _, rel := range c.unitFiles(target.CLI) {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		meta := FileMeta{RelPath: rel}
		if info, err := os.Lstat(filepath.Join(target.UNCRoot, filepath.FromSlash(rel))); err == nil {
			if info.Mode().IsRegular() {
				meta.Exists = true
				meta.Size = info.Size()
				meta.ModTime = info.ModTime()
				if hashed[rel] {
					if data, readErr := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(rel))); readErr == nil {
						meta.Hash = hexHash(data)
					}
				}
			}
		}
		snap.Files = append(snap.Files, meta)
	}
	if err := c.state.mutate(func(st *StateFile) {
		st.LastInspectedAt = snap.InspectedAt
		// F4（S1 diting）：检查时间按 CLI 作用域另记一份——GetStatus 只读本表，
		// pi 作用域的探测不再错配到 shared 面板（全局字段保留写入仅为旧读端
		// 兼容，不再被本包读取）。
		if st.LastInspectedAtByCLI == nil {
			st.LastInspectedAtByCLI = map[CLI]time.Time{}
		}
		st.LastInspectedAtByCLI[target.CLI] = snap.InspectedAt
		st.Distro = target.Distro
		st.Home = target.Home
		st.Generation = target.Generation
		if st.PendingSources == nil {
			st.PendingSources = map[string]uint64{}
		}
	}); err != nil {
		logWarn(c.log, "wslsupply", "状态清单落盘失败（检查时间未记录）", err.Error())
		return snap, nil
	}
	return snap, nil
}

// projectionFiles 返回普通单元关心的目标相对路径集合（其文件在 Inspect 时
// 计算内容 hash；A2 凭据文件不在内）。
func (c *Coordinator) projectionFiles(cli CLI) map[string]bool {
	out := map[string]bool{}
	prefix := string(cli) + "/"
	for unit, adapter := range c.projections {
		if strings.HasPrefix(string(unit), prefix) {
			for _, rel := range adapter.Files() {
				out[rel] = true
			}
		}
	}
	return out
}

// unitFiles 汇总该 CLI 下全部已注册单元的目标相对路径（确定性排序）。
func (c *Coordinator) unitFiles(cli CLI) []string {
	var out []string
	prefix := string(cli) + "/"
	if strings.HasPrefix(string(UnitPiA2Auth), prefix) {
		out = append(out, c.a2Adapter().Files()...)
	}
	for unit, adapter := range c.projections {
		if strings.HasPrefix(string(unit), prefix) {
			out = append(out, adapter.Files()...)
		}
	}
	sort.Strings(out)
	return out
}

// knownUnit 报告单元是否已注册（A2 恒 true；技能/资源单元随各自作用域注册
// 动态可识别）。unknown 单元拒绝（design §七）。
func (c *Coordinator) knownUnit(unit UnitID) bool {
	if unit == UnitPiA2Auth {
		return true
	}
	if _, ok := ParseSkillUnit(unit); ok {
		return c.skills != nil
	}
	if cli, _, ok := ParseResourceUnit(unit); ok {
		return c.resourceScope(cli) != nil
	}
	_, ok := c.projections[unit]
	return ok
}

// ---------------------------------------------------------------------------
// Plan
// ---------------------------------------------------------------------------

// Plan 是纯计算：由请求与快照推导逐单元动作；不写文件、不 spawn、不触碰探测面。
// 计划不携带候选正文；A2 的新陈判定刻意延迟到适配器 Apply 时执行（语义零变化，
// 计划只携带待供给描述）。
func (c *Coordinator) Plan(req SupplyRequest, snap *Snapshot) (*SupplyPlan, error) {
	if snap == nil {
		return nil, errors.New("plan requires a snapshot")
	}
	if !req.Target.SameIdentity(snap.Target) {
		return nil, errors.New("snapshot target does not match request target")
	}
	if len(req.SelectedUnits) == 0 {
		return nil, errors.New("supply request selects no units")
	}
	state := c.state.load()
	plan := &SupplyPlan{Request: req}
	for _, unit := range req.SelectedUnits {
		if !c.knownUnit(unit) {
			return nil, fmt.Errorf("unknown wsl supply unit: %q", string(unit))
		}
		if _, isSkill := ParseSkillUnit(unit); isSkill {
			// 技能单元（S3）：per-target 同意门与 S1/S2 同构——未同意恒
			// awaiting-consent，不进入归属判定（用户资产跨界导入是授权动作）。
			entry := state.entryFor(unit, req.Target.Generation)
			if !entry.Consented {
				plan.Units = append(plan.Units, PlannedUnit{
					Unit: unit, Policy: PolicyTrackedProjection, Action: ActionBlocked,
					BlockedReason: "awaiting-consent",
				})
				continue
			}
			name, _ := ParseSkillUnit(unit)
			planned := c.planSkill(req.Target, name, &entry)
			plan.Units = append(plan.Units, planned)
			continue
		}
		if resCLI, resRel, isRes := ParseResourceUnit(unit); isRes {
			// 资源单元（S4）：与技能同一同意门（用户资产跨界导入是授权动作）。
			entry := state.entryFor(unit, req.Target.Generation)
			if !entry.Consented {
				plan.Units = append(plan.Units, PlannedUnit{
					Unit: unit, Policy: PolicyTrackedProjection, Action: ActionBlocked,
					BlockedReason: "awaiting-consent",
				})
				continue
			}
			plan.Units = append(plan.Units, c.planResourceUnit(req.Target, unit, resCLI, resRel, &entry))
			continue
		}
		switch unit {
		case UnitPiA2Auth:
			plan.Units = append(plan.Units, PlannedUnit{
				Unit: unit, Policy: PolicyA2Auth, Action: ActionSyncA2,
			})
		default:
			adapter := c.projections[unit]
			entry := state.entryFor(unit, req.Target.Generation)
			// 同意门：需要 per-target 明确同意的单元（视觉清单密钥跨界）在未授权前
			// 恒为阻断待确认，不进入适配器归属判定（design §四）。
			if adapter.RequiresConsent() && !entry.Consented {
				plan.Units = append(plan.Units, PlannedUnit{
					Unit: unit, Policy: adapter.Policy(), Action: ActionBlocked,
					BlockedReason: "awaiting-consent",
				})
				continue
			}
			planned, err := adapter.Plan(req, snap, &entry)
			if err != nil {
				return nil, fmt.Errorf("plan unit %s: %w", string(unit), err)
			}
			planned.Unit = unit
			planned.Policy = adapter.Policy()
			plan.Units = append(plan.Units, planned)
		}
	}
	return plan, nil
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

// Apply 复核目标与 hash 后执行计划并返回逐项结果。目标身份变化拒绝旧计划；
// A2 委托适配器（结果分类与既有闸门一致）；投影单元先复核当前 hash 仍等于
// 计划预期值再经提交原语写入。部分失败不虚报整批成功。
func (c *Coordinator) Apply(ctx context.Context, plan *SupplyPlan) (RunResult, error) {
	if plan == nil || len(plan.Units) == 0 {
		return RunResult{}, errors.New("apply requires a non-empty plan")
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	result := RunResult{Target: plan.Request.Target}

	// 复核目标：重探身份，拒绝计划后的目标替换（重装/换用户/换 Home）。
	current, err := c.ResolveTarget(ctx, plan.Request.Target.Distro, plan.Request.Target.CLI)
	if err != nil || !current.SameIdentity(plan.Request.Target) {
		for _, unit := range plan.Units {
			result.Results = append(result.Results, UnitResult{
				Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitBlocked,
				Detail: "target-changed-since-planning",
				Err:    fmt.Errorf("wsl supply target changed since planning: %v", err),
			})
		}
		return result, nil
	}

	for _, unit := range plan.Units {
		switch unit.Unit {
		case UnitPiA2Auth:
			if unit.Action != ActionSyncA2 {
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitBlocked,
					Detail: "planned-action-mismatch",
					Err:    errors.New("a2 unit requires ActionSyncA2"),
				})
				continue
			}
			outcome, syncErr := c.a2Adapter().Sync(current.Distro)
			unitResult := classifyA2(unit.Unit, outcome, syncErr)
			result.Results = append(result.Results, unitResult)
			c.recordA2State(current, unitResult, outcome)
		default:
			if isSkillUnitID(unit.Unit) {
				result.Results = append(result.Results, c.applySkillPlanned(ctx, current, unit))
				continue
			}
			if resCLI, resRel, isRes := ParseResourceUnit(unit.Unit); isRes {
				result.Results = append(result.Results, c.applyResourcePlanned(ctx, current, unit, resCLI, resRel))
				continue
			}
			adapter := c.projections[unit.Unit]
			if adapter == nil {
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitBlocked,
					Detail: "unit-adapter-missing",
					Err:    errors.New("projection adapter not registered"),
				})
				continue
			}
			switch unit.Action {
			case ActionNone:
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitFresh, Detail: "no-change",
				})
			case ActionBlocked:
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitBlocked,
					Detail: unit.BlockedReason,
				})
				if unit.Freeze {
					// 冻结态持久化（GetStatus 供 UI 显示；下次成功写入时清除）。
					c.recordProjectionFreeze(current, unit.Unit, unit.Policy, unit.BlockedReason)
				} else if unit.BlockedReason == "awaiting-consent" {
					// 待同意态持久化：状态面需要可见「未授权」而非「未检查」。
					c.recordProjectionPendingConsent(current, unit.Unit, unit.Policy)
				}
			case ActionWriteProjection:
				result.Results = append(result.Results, c.applyProjection(ctx, current, unit, adapter))
			default:
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitFailed,
					Detail: "planned-action-mismatch",
					Err:    fmt.Errorf("unsupported projection action: %d", unit.Action),
				})
			}
		}
	}
	c.clearSatisfiedDirtySources(result)
	return result, nil
}

// DirtySource* 是保存事件来源名（App markWSLSupplyDirty 的调用方必须使用本
// 常量——dirtySourceUnits 依此映射来源→它滋养的单元集）。
const (
	DirtySourceProviders      = "providers"
	DirtySourcePiLoginPresets = "pi-login-presets"
	// DirtySourcePiConfig（S2）：PiConfig 面板保存（amagi.json / models.json
	// 手工编辑影响引用诊断宇宙）后发布；滋养三个键级投影单元。
	DirtySourcePiConfig = "pi-config"
)

// dirtySourceUnits 把保存事件的来源映射到其滋养的单元集（F2：推送/启动补漏
// 完成后据此清除待同步标记）。provider 保存影响托管 models.json 与两份共享
// 文件（视觉清单的 api_key/baseURL、学习层无直接关联但同批推送）；登录预设
// 只影响托管 models.json 的 contextWindow 覆写。未知来源不映射（保守：其
// pending 标记不因任何单元成功而清除）。
var dirtySourceUnits = map[string][]UnitID{
	DirtySourceProviders:      {UnitPiManagedModels, UnitSharedVisionExport, UnitSharedModalityKB},
	DirtySourcePiLoginPresets: {UnitPiManagedModels},
	DirtySourcePiConfig:       {UnitPiSettings, UnitPiAmagi, UnitPiMCP},
}

// clearSatisfiedDirtySources 在一次 Apply 完成后推进/清除待同步来源（F2：
// 修复前 PendingSources 只写不清，首次保存后 pendingPush 恒亮、「已同步」
// 不可达）。一个来源的滋养单元常跨两次 RunUnits（pi 作用域 + shared 作用域），
// 故按来源累计「标记后已成功（Applied/Fresh）」的单元集（PendingProgress，
// MarkDirty 时重置）；累计集覆盖全部滋养单元才清除来源标记。任一单元失败/
// 阻断则不计入，标记保留待下次补漏；重启后 pending 补漏完成同样经本路径收敛。
func (c *Coordinator) clearSatisfiedDirtySources(result RunResult) {
	if len(result.Results) == 0 {
		return
	}
	succeeded := make([]UnitID, 0, len(result.Results))
	for i := range result.Results {
		if o := result.Results[i].Outcome; o == UnitApplied || o == UnitFresh || o == UnitWarnDegraded {
			succeeded = append(succeeded, result.Results[i].Unit)
		}
	}
	if len(succeeded) == 0 {
		return
	}
	st := c.state.load()
	progressDelta := map[string]map[UnitID]bool{}
	var satisfied []string
	for source := range st.PendingSources {
		units, mapped := dirtySourceUnits[source]
		if !mapped {
			continue
		}
		progress := map[UnitID]bool{}
		for _, u := range st.PendingProgress[source] {
			progress[u] = true
		}
		for _, u := range succeeded {
			progress[u] = true
		}
		done := true
		for _, u := range units {
			if !progress[u] {
				done = false
				break
			}
		}
		if done {
			satisfied = append(satisfied, source)
			continue
		}
		progressDelta[source] = progress
	}
	if len(satisfied) == 0 && len(progressDelta) == 0 {
		return
	}
	sort.Strings(satisfied)
	err := c.state.mutate(func(st *StateFile) {
		if st.PendingProgress == nil {
			st.PendingProgress = map[string][]UnitID{}
		}
		for source, progress := range progressDelta {
			units := make([]UnitID, 0, len(progress))
			for u := range progress {
				units = append(units, u)
			}
			sort.Slice(units, func(i, j int) bool { return units[i] < units[j] })
			st.PendingProgress[source] = units
		}
		for _, source := range satisfied {
			delete(st.PendingSources, source)
			delete(st.PendingProgress, source)
		}
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "清除待同步来源标记失败（下次运行重试）", strings.Join(satisfied, ","))
	}
}

// applyProjection 执行一个投影单元：先复核当前目标仍等于计划预期值（CAS
// 前置），再经提交原语写入并登记。候选正文只在本次调用内存中存在。
// 键级适配器（S2）在候选中携带完整键级基准（Subs）与部分写入标记
// （Outcome=partial）；全部键保持现状时候选返回 errKeyedNoWrite，按「受阻
// 冻结」登记而不是写失败。
func (c *Coordinator) applyProjection(ctx context.Context, target TargetRef, planned PlannedUnit, adapter ProjectionAdapter) UnitResult {
	st := c.state.load()
	entry := st.entryFor(planned.Unit, target.Generation)
	cand, err := adapter.Candidate(ctx, target, &entry)
	if err != nil {
		var noWrite errKeyedNoWrite
		if errors.As(err, &noWrite) {
			// 无可写键：保持现状（冻结/未托管/转换受阻），不产生写入。
			c.recordProjectionFreeze(target, planned.Unit, adapter.Policy(), noWrite.reason)
			return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked,
				Detail: noWrite.reason + "; " + strings.Join(noWrite.notes, "; "), Err: err}
		}
		detail := "candidate-unavailable"
		if errors.Is(err, ErrTargetCorrupt) {
			// 两级损坏保护：目标存在但严格解析/嵌套形态校验失败 → 不覆盖，
			// 以 target-corrupt 明细暴露给启动闸门（可阻断）与状态页。
			detail = "target-corrupt"
		}
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: detail, Err: err}
	}
	// CAS 前置复核：目标若已被外部修改（不再等于计划预期值），拒绝旧计划，
	// 保留外部编辑（设计：目标变更/用户修改则拒绝旧计划）。
	current, readErr := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(planned.RelPath)))
	switch {
	case readErr == nil:
		if hexHash(current) != planned.ExpectedTargetHash {
			return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
				Detail: "target-changed-since-planning",
				Err:    errors.New("target file changed since planning (external edit retained)")}
		}
	case errors.Is(readErr, os.ErrNotExist):
		if planned.ExpectedTargetHash != "" {
			return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
				Detail: "target-changed-since-planning",
				Err:    errors.New("target file disappeared since planning (local deletion retained)")}
		}
	default:
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "target-unreadable", Err: readErr}
	}
	undo, err := c.submitFile(target, planned.RelPath, cand.Content, adapter.Sensitive())
	if err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "write-failed", Err: err}
	}
	undo.unit = planned.Unit
	c.recordProjectionState(target, planned.Unit, adapter.Policy(), cand.SourceHash, hexHash(cand.Content), cand.TransformVersion, cand.Subs, cand.Outcome)
	detail := cand.Outcome
	if detail == "" {
		detail = "written"
	}
	if len(cand.Notes) > 0 {
		// 部分构建失败/冲突保留等逐项诊断（不含密钥正文）：并入结果明细与日志。
		detail += "; " + strings.Join(cand.Notes, "; ")
		logWarn(c.log, "wslsupply", "供给单元部分诊断", strings.Join(cand.Notes, "; "))
	}
	return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitApplied,
		Detail: detail, Undo: undo}
}

// recordA2State 更新 A2 单元登记：只记录 outcome/时间戳/错误码，永不记录 hash
// （凭据 hash 不落盘、不出状态 API）。
func (c *Coordinator) recordA2State(target TargetRef, res UnitResult, outcome launcher.SyncWSLPiLoginAuthOutcome) {
	entry := StateEntry{
		Unit: res.Unit, Policy: PolicyA2Auth, Generation: target.Generation,
		LastOutcome: a2OutcomeName(outcome),
	}
	if res.Outcome != UnitFailed && res.Outcome != UnitBlocked {
		entry.LastSuccess = time.Now()
	} else if res.Err != nil {
		entry.LastErrorCode = sanitizeErrorCode(res.Err)
	}
	err := c.state.mutate(func(st *StateFile) {
		st.upsertEntry(entry, target.Generation)
		st.Distro = target.Distro
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "A2 单元状态登记失败（凭据本身已同步）", err.Error())
	}
}

// recordProjectionState 更新投影单元登记（普通/托管单元允许记录 hash，仅私有
// 落盘，不经状态 API 返回）。SourceHash 是规范化业务内容基准（不含时间戳），
// LastWrittenHash 是实际落盘字节 hash；键级基准（Subs）与部分写入标记
// （Outcome）随写入更新；Consented 等跨写入字段保留不丢失。
func (c *Coordinator) recordProjectionState(target TargetRef, unit UnitID, policy Policy, sourceHash, writtenHash, transformVersion string, subs map[string]SubHash, outcome string) {
	if outcome == "" {
		outcome = "written"
	}
	err := c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		next := subs
		if next == nil && len(prev.Subs) > 0 {
			// 非键级适配器（Candidate 未携带 Subs 面，nil）不因本调用清空既有键级
			// 基准；键级适配器显式传空 map 是合法终态（全部键被移除）。
			next = prev.Subs
		}
		st.upsertEntry(StateEntry{
			Unit: unit, Policy: policy, Generation: target.Generation,
			SourceHash: sourceHash, LastWrittenHash: writtenHash, TransformVersion: transformVersion,
			LastSuccess: time.Now(), LastOutcome: outcome,
			Subs: next, Consented: prev.Consented, SelectedKeys: prev.SelectedKeys,
		}, target.Generation)
		st.Distro = target.Distro
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "投影单元状态登记失败（内容已提交，下次视为待核实）", err.Error())
	}
}

// recordProjectionPendingConsent 把「待同意」态写入登记（Consented=false，
// LastOutcome=awaiting-consent）；授权后由 GrantUnitConsent 覆写。基线字段
// （Subs/SelectedKeys/hash）仅同代保留——跨代复制会把旧代键级基准泄漏到
// 新代（重新同意后的首发布被误判 local-deletion-frozen，违背 F3 语义）。
func (c *Coordinator) recordProjectionPendingConsent(target TargetRef, unit UnitID, policy Policy) {
	err := c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		entry := StateEntry{
			Unit: unit, Policy: policy, Generation: target.Generation,
			LastOutcome: "awaiting-consent",
		}
		if prev.Generation == target.Generation {
			// F-1 同类（S2 diting）：登记重建不丢键级基准/同意/停跟随标记。
			entry.SourceHash = prev.SourceHash
			entry.LastWrittenHash = prev.LastWrittenHash
			entry.Subs = prev.Subs
			entry.Detached = prev.Detached
			entry.Consented = prev.Consented
			entry.SelectedKeys = prev.SelectedKeys
		}
		st.upsertEntry(entry, target.Generation)
		st.Distro = target.Distro
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "待同意状态登记失败（不影响目标内容）", err.Error())
	}
}

// recordProjectionFreeze 把冻结态写入登记（仅同代保留既有 hash/同意标记/
// 键级基准/选择集/停跟随标记，仅置 Frozen 与原因）；后续成功写入时由
// recordProjectionState 的全新条目清除冻结。F-1（S2 diting）：Subs 必须
// 保留——键级单元的部分键冻结不得摧毁其余健康键的登记（否则下轮全部沦为
// unmanaged-existing，永久停跟随）。
func (c *Coordinator) recordProjectionFreeze(target TargetRef, unit UnitID, policy Policy, reason string) {
	err := c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		entry := StateEntry{
			Unit: unit, Policy: policy, Generation: target.Generation,
			LastOutcome: "frozen", Frozen: true, FrozenReason: reason,
		}
		if prev.Generation == target.Generation {
			entry.SourceHash = prev.SourceHash
			entry.LastWrittenHash = prev.LastWrittenHash
			entry.TransformVersion = prev.TransformVersion
			entry.Consented = prev.Consented
			entry.Subs = prev.Subs
			entry.Detached = prev.Detached
			entry.SelectedKeys = prev.SelectedKeys
		}
		st.upsertEntry(entry, target.Generation)
		st.Distro = target.Distro
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "冻结状态登记失败（不影响目标内容保留）", err.Error())
	}
}

// ---------------------------------------------------------------------------
// RunUnits / MarkDirty / GetStatus
// ---------------------------------------------------------------------------

// RunUnits 是启动链的单一入口：解析目标 → Inspect → Plan → Apply。桌面闸门与
// remote/v1 effect 共用（S0 统一执行边界）；目标不可解析时以逐单元阻断结果
// 返回而不是 error（调用方按 fail-closed 闸门语义消费）。
func (c *Coordinator) RunUnits(ctx context.Context, distro string, cli CLI, reason Reason, units []UnitID) (RunResult, error) {
	blocked := func(detail string, err error) (RunResult, error) {
		res := RunResult{Results: make([]UnitResult, 0, len(units))}
		for _, unit := range units {
			res.Results = append(res.Results, UnitResult{
				Unit: unit, Outcome: UnitBlocked, Detail: detail, Err: err,
			})
		}
		return res, nil
	}
	if len(units) == 0 {
		return RunResult{}, errors.New("run requires at least one unit")
	}
	for _, unit := range units {
		if !c.knownUnit(unit) {
			return RunResult{}, fmt.Errorf("unknown wsl supply unit: %q", string(unit))
		}
	}
	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		return blocked("target-unresolved", err)
	}
	snap, err := c.Inspect(ctx, target)
	if err != nil {
		return blocked("inspect-failed", err)
	}
	plan, err := c.Plan(SupplyRequest{Target: target, Reason: reason, SelectedUnits: units}, snap)
	if err != nil {
		return blocked("plan-failed", err)
	}
	return c.Apply(ctx, plan)
}

// MarkDirty 保存事件后标脏：合并来源 revision 并落盘 desired revision（重启可
// 补漏）。不在配置锁下等待 WSL、不探测、不调度（S0 只落 revision，S1 接分发）。
func (c *Coordinator) MarkDirty(source string, revision uint64) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("dirty source is required")
	}
	return c.state.mutate(func(st *StateFile) {
		if st.PendingSources == nil {
			st.PendingSources = map[string]uint64{}
		}
		if revision > st.PendingSources[source] {
			st.PendingSources[source] = revision
			// F2：重新标脏即来源再次变化——该来源的单元成功进度重置，只有
			// 标记之后的成功才计入清除条件。
			if st.PendingProgress != nil {
				delete(st.PendingProgress, source)
			}
		}
		if revision > st.DesiredRevision {
			st.DesiredRevision = revision
		}
	})
}

// GetStatus 返回脱敏目标状态：只读状态清单与缓存，不触发安装/同步/探测；
// 无缓存返回未检查（Checked=false）。敏感 hash（sourceHash/lastWrittenHash）
// 不出本接口；A2 单元从不持有 hash。
func (c *Coordinator) GetStatus(distro string, cli CLI) TargetStatus {
	distro = strings.TrimSpace(distro)
	st := c.state.load()
	status := TargetStatus{Distro: distro, CLI: cli}
	// 待同步来源与 desired revision 是宿主侧状态，不随目标分发；其余字段
	//（检查时间/身份/单元登记）只在清单属于该目标时返回。
	status.DesiredRevision = st.DesiredRevision
	if len(st.PendingSources) > 0 {
		status.PendingSources = make(map[string]uint64, len(st.PendingSources))
		for k, v := range st.PendingSources {
			status.PendingSources[k] = v
		}
	}
	if st.Distro != distro {
		return status
	}
	status.Generation = st.Generation
	status.Home = st.Home
	// F4：只读该 CLI 作用域的实际探测时间（旧清单无 per-CLI 记录 → 未检查，
	// 诚实呈现而非沿用其它作用域的时间）。
	if t, ok := st.LastInspectedAtByCLI[cli]; ok && !t.IsZero() {
		status.Checked = true
		status.CheckedAt = t
	}
	for _, entry := range st.Units {
		status.Units = append(status.Units, UnitStatus{
			Unit: entry.Unit, Policy: entry.Policy, LastOutcome: entry.LastOutcome,
			LastSuccess: entry.LastSuccess, LastErrorCode: entry.LastErrorCode,
			TransformVersion: entry.TransformVersion, Frozen: entry.Frozen,
			FrozenReason: entry.FrozenReason, Consented: entry.Consented,
			Detached: entry.Detached,
			Stale:    c.skillUnitStale(entry) || c.resourceUnitStale(entry),
		})
	}
	return status
}

// skillUnitStale 报告技能单元登记的来源 hash 与当前扫描缓存是否不一致
// （S3 陈旧判定：脱敏布尔，hash 不出包）。非技能单元恒 false。
func (c *Coordinator) skillUnitStale(entry StateEntry) bool {
	name, ok := ParseSkillUnit(entry.Unit)
	if !ok || c.skills == nil || entry.SourceHash == "" {
		return false
	}
	m := c.skills.manifest(name)
	return m != nil && m.SourceHash != entry.SourceHash
}

// resourceUnitStale 报告资源单元（S4）登记的来源 hash 与扫描缓存是否不一致。
// 非资源单元恒 false。
func (c *Coordinator) resourceUnitStale(entry StateEntry) bool {
	cli, rel, ok := ParseResourceUnit(entry.Unit)
	if !ok || entry.SourceHash == "" {
		return false
	}
	scope := c.resourceScope(cli)
	if scope == nil {
		return false
	}
	m := scope.manifest(rel)
	return m != nil && m.SourceHash != entry.SourceHash
}

// HasUnit 报告单元是否已在协调器注册（A2 恒 true）。启动链据此按能力适配
// 单元清单（裸协调器缺 S1 适配器时自动降级为 A2-only，不报 unknown unit）。
func (c *Coordinator) HasUnit(unit UnitID) bool { return c.knownUnit(unit) }

// ---------------------------------------------------------------------------
// S2 操作面：预览 / 保留本地（停跟随）/ 预览后接管（design §三解除冻结两
// 操作 + §四动作分离：没有强制覆盖全部）
// ---------------------------------------------------------------------------

// UnitPreview 是预览面上单个单元的脱敏描述（不含内容差异与密钥；TargetHash
// 是 CAS 令牌，仅供「确认接管」回传，不属展示内容）。
type UnitPreview struct {
	Unit          UnitID
	RelPath       string
	Policy        Policy
	Action        Action
	BlockedReason string
	Freeze        bool
	TargetHash    string
	Keys          []KeyPreview
	Notes         []string
}

// PreviewUnits 做一次「检查 + 预览」：解析目标 → Inspect（只读探测，可唤醒
// 当前目标）→ Plan（纯计算），不执行任何写入。键级适配器附带逐键动作与
// 依赖诊断（design §四：amagi 引用缺 provider/server 的明确诊断）。
func (c *Coordinator) PreviewUnits(ctx context.Context, distro string, cli CLI, units []UnitID) ([]UnitPreview, error) {
	if len(units) == 0 {
		return nil, errors.New("preview requires at least one unit")
	}
	for _, unit := range units {
		if !c.knownUnit(unit) {
			return nil, fmt.Errorf("unknown wsl supply unit: %q", string(unit))
		}
	}
	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		return nil, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	snap, err := c.Inspect(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("inspect wsl supply target: %w", err)
	}
	state := c.state.load()
	out := make([]UnitPreview, 0, len(units))
	for _, unit := range units {
		entry := state.entryFor(unit, target.Generation)
		prev := UnitPreview{Unit: unit, Action: ActionNone}
		if unit == UnitPiA2Auth {
			prev.BlockedReason = "not-previewable"
			out = append(out, prev)
			continue
		}
		if name, isSkill := ParseSkillUnit(unit); isSkill {
			out = append(out, c.previewSkillUnit(target, name, &entry))
			continue
		}
		if resCLI, resRel, isRes := ParseResourceUnit(unit); isRes {
			out = append(out, c.previewResourceUnit(target, unit, resCLI, resRel, &entry))
			continue
		}
		adapter, ok := c.projections[unit]
		if !ok {
			prev.BlockedReason = "unit-adapter-missing"
			out = append(out, prev)
			continue
		}
		prev.RelPath = firstFileOrDefault(adapter)
		prev.Policy = adapter.Policy()
		if meta, ok := snap.File(prev.RelPath); ok && meta.Exists {
			prev.TargetHash = meta.Hash
		}
		if entry.Detached {
			// 已停跟随：预览仍给出目标 hash 令牌（接管需要它）。
			prev.BlockedReason = "detached"
			out = append(out, prev)
			continue
		}
		planned, pErr := adapter.Plan(SupplyRequest{Target: target, Reason: ReasonManual, SelectedUnits: []UnitID{unit}}, snap, &entry)
		if pErr != nil {
			prev.Action = ActionBlocked
			prev.BlockedReason = pErr.Error()
			out = append(out, prev)
			continue
		}
		prev.Action = planned.Action
		prev.BlockedReason = planned.BlockedReason
		prev.Freeze = planned.Freeze
		prev.RelPath = planned.RelPath
		prev.Policy = adapter.Policy()
		if meta, ok := snap.File(planned.RelPath); ok && meta.Exists {
			prev.TargetHash = meta.Hash
		}
		if keyed, ok := adapter.(KeyedAdapter); ok {
			keys, blocked := keyed.PreviewKeys(snap, &entry)
			prev.Keys = keys
			if blocked != "" && blocked != "detached" {
				prev.Notes = append(prev.Notes, "单元整体不可分析："+blocked)
			}
		}
		out = append(out, prev)
	}
	return out, nil
}

// previewSkillUnit 构造单个技能单元的预览（脱敏：不兼容项逐条原因、排除物
// 摘要、目标 hash 令牌仅供接管回传；不写盘不 spawn）。
func (c *Coordinator) previewSkillUnit(target TargetRef, name string, entry *StateEntry) UnitPreview {
	prev := UnitPreview{Unit: SkillUnitID(name), Policy: PolicyTrackedProjection, Action: ActionNone, RelPath: skillRelDir(name)}
	if c.skills == nil {
		prev.BlockedReason = "skills-scope-unavailable"
		return prev
	}
	limits := c.skills.Limits()
	tgt := scanTargetSkillDir(filepath.Join(target.UNCRoot, filepath.FromSlash(skillRelDir(name))), limits)
	if !tgt.Unverifiable {
		prev.TargetHash = tgt.TreeHash
	}
	if m := c.skills.manifest(name); m != nil {
		for _, it := range m.Incompatible {
			prev.Notes = append(prev.Notes, fmt.Sprintf("不兼容：%s：%s", it.RelPath, it.Reason))
		}
		if len(m.Excluded) > 0 {
			prev.Notes = append(prev.Notes, fmt.Sprintf("将排除 %d 项（.git/node_modules/缓存/凭据/会话产物）", len(m.Excluded)))
		}
		if m.Incomplete {
			prev.Notes = append(prev.Notes, "扫描未完成（"+m.IncompleteReason+"），不可供给")
		}
	}
	if entry != nil && entry.Detached {
		prev.BlockedReason = "detached"
		return prev
	}
	if entry == nil || !entry.Consented {
		// F6：预览镜像 Plan 的同意门——同一单元同一答案（未同意恒
		// awaiting-consent，不显示「会写入」）；目标 hash 令牌与目标存在性
		// 附注仍保留（接管流程需要它们）。
		prev.Action = ActionBlocked
		prev.BlockedReason = "awaiting-consent"
		prev.Notes = append(prev.Notes, "尚未选择导入（未授权）；勾选导入授权后才会写入")
		if tgt.Exists {
			prev.Notes = append(prev.Notes, "目标已存在内容（授权后将判定为未托管，需预览后确认接管）")
		}
		return prev
	}
	planned := c.planSkill(target, name, entry)
	prev.Action = planned.Action
	prev.BlockedReason = planned.BlockedReason
	prev.Freeze = planned.Freeze
	return prev
}

// DropUnitRegistration 执行「保留本地（停跟随）」：目标内容不动，登记
// Detached 标记，后续供给零动作；显式接管可解除。不要求 distro 可达（停机
// 目标上也可停跟随）。
func (c *Coordinator) DropUnitRegistration(distro string, cli CLI, unit UnitID) error {
	if _, isSkill := ParseSkillUnit(unit); isSkill {
		if c.skills == nil {
			return fmt.Errorf("unknown or non-projection wsl supply unit: %q", string(unit))
		}
		return c.state.mutate(func(st *StateFile) {
			prev, _ := st.entry(unit)
			st.upsertEntry(StateEntry{
				Unit: unit, Policy: PolicyTrackedProjection, Generation: prev.Generation,
				SourceHash: prev.SourceHash, LastWrittenHash: prev.LastWrittenHash,
				TransformVersion: prev.TransformVersion,
				Subs:             prev.Subs, Detached: true, Consented: prev.Consented, LastOutcome: "detached",
				LastSuccess: time.Now(),
			}, prev.Generation)
		})
	}
	if resCLI, _, isRes := ParseResourceUnit(unit); isRes {
		if c.resourceScope(resCLI) == nil {
			return fmt.Errorf("unknown or non-projection wsl supply unit: %q", string(unit))
		}
		return c.state.mutate(func(st *StateFile) {
			prev, _ := st.entry(unit)
			st.upsertEntry(StateEntry{
				Unit: unit, Policy: PolicyTrackedProjection, Generation: prev.Generation,
				SourceHash: prev.SourceHash, LastWrittenHash: prev.LastWrittenHash,
				TransformVersion: prev.TransformVersion, SelectedKeys: prev.SelectedKeys,
				Subs: prev.Subs, Detached: true, Consented: prev.Consented, LastOutcome: "detached",
				LastSuccess: time.Now(),
			}, prev.Generation)
		})
	}
	adapter, ok := c.projections[unit]
	if !ok {
		return fmt.Errorf("unknown or non-projection wsl supply unit: %q", string(unit))
	}
	return c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		next := StateEntry{
			Unit: unit, Policy: adapter.Policy(), Generation: prev.Generation,
			SourceHash: prev.SourceHash, LastWrittenHash: prev.LastWrittenHash,
			TransformVersion: prev.TransformVersion,
			Subs:             prev.Subs, Detached: true, Consented: prev.Consented, LastOutcome: "detached",
			LastSuccess: time.Now(),
		}
		st.upsertEntry(next, prev.Generation)
	})
}

// TakeOverUnit 执行「预览后确认接管」：以预览时目标 hash 做 CAS（旧预览不
// 覆盖新编辑——目标已变则拒绝并要求重新检查），随后用当前来源生成接管候选
// 并提交，重建键级基准。仅键级适配器支持。
func (c *Coordinator) TakeOverUnit(ctx context.Context, distro string, cli CLI, unit UnitID, expectedTargetHash string) (UnitResult, error) {
	if name, isSkill := ParseSkillUnit(unit); isSkill {
		return c.takeOverSkill(ctx, distro, unit, name, expectedTargetHash)
	}
	if resCLI, resRel, isRes := ParseResourceUnit(unit); isRes {
		return c.takeOverResource(ctx, distro, unit, resCLI, resRel, expectedTargetHash)
	}
	adapter, ok := c.projections[unit]
	if !ok {
		return UnitResult{}, fmt.Errorf("unknown wsl supply unit: %q", string(unit))
	}
	keyed, ok := adapter.(KeyedAdapter)
	if !ok {
		return UnitResult{}, fmt.Errorf("wsl supply unit %q does not support takeover", string(unit))
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		return UnitResult{}, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	// 锁内重读登记；代际不匹配视为无登记（entryFor）。
	st := c.state.load()
	entry := st.entryFor(unit, target.Generation)
	// F-3（S2 diting）：接管是显式写入动作，不得绕过 per-target 同意门——
	// 未同意的单元先用「启用投影」授权（与首启同一门）。
	if adapter.RequiresConsent() && !entry.Consented {
		return UnitResult{Unit: unit, Policy: adapter.Policy(), Outcome: UnitBlocked,
			Detail: "awaiting-consent",
			Err:    errors.New("unit is not consented for this target; enable the projection first")}, nil
	}
	planned := PlannedUnit{Unit: unit, Policy: adapter.Policy(), RelPath: firstFileOrDefault(adapter)}
	current, readErr := os.ReadFile(filepath.Join(target.UNCRoot, filepath.FromSlash(planned.RelPath)))
	switch {
	case readErr == nil:
		if hexHash(current) != expectedTargetHash {
			return UnitResult{Unit: unit, Policy: planned.Policy, Outcome: UnitBlocked,
				Detail: "preview-expired",
				Err:    errors.New("target file changed since preview; re-check before takeover")}, nil
		}
	case errors.Is(readErr, os.ErrNotExist):
		if expectedTargetHash != "" {
			return UnitResult{Unit: unit, Policy: planned.Policy, Outcome: UnitBlocked,
				Detail: "preview-expired",
				Err:    errors.New("target file disappeared since preview; re-check before takeover")}, nil
		}
	default:
		return UnitResult{}, fmt.Errorf("read wsl supply target: %w", readErr)
	}
	cand, err := keyed.AdoptCandidate(ctx, target, &entry)
	if err != nil {
		detail := "adopt-unavailable"
		if errors.Is(err, ErrTargetCorrupt) {
			detail = "target-corrupt"
		}
		return UnitResult{Unit: unit, Policy: planned.Policy, Outcome: UnitFailed, Detail: detail, Err: err}, nil
	}
	undo, err := c.submitFile(target, planned.RelPath, cand.Content, adapter.Sensitive())
	if err != nil {
		return UnitResult{Unit: unit, Policy: planned.Policy, Outcome: UnitFailed, Detail: "write-failed", Err: err}, nil
	}
	undo.unit = unit
	c.recordProjectionState(target, unit, adapter.Policy(), cand.SourceHash, hexHash(cand.Content), cand.TransformVersion, cand.Subs, cand.Outcome)
	detail := "takeover-written"
	if len(cand.Notes) > 0 {
		detail += "; " + strings.Join(cand.Notes, "; ")
	}
	return UnitResult{Unit: unit, Policy: planned.Policy, Outcome: UnitApplied, Detail: detail, Undo: undo}, nil
}

// firstFileOrDefault 返回适配器首个目标文件（接管路径叼接用；键级适配器单文件）。
func firstFileOrDefault(adapter ProjectionAdapter) string {
	files := adapter.Files()
	if len(files) > 0 {
		return files[0]
	}
	return ""
}

// GrantUnitConsent 为目标登记一个 per-target 明确同意（S1：视觉清单密钥跨界
// 共享，design §四）。同意按目标 generation 记录：distro 重装/换用户后旧授权
// 不自动扩散，需重新确认。仅 RequiresConsent 单元接受本操作。
func (c *Coordinator) GrantUnitConsent(ctx context.Context, distro string, cli CLI, unit UnitID) error {
	policy := Policy("")
	if _, isSkill := ParseSkillUnit(unit); isSkill {
		if c.skills == nil {
			return fmt.Errorf("wsl supply unit %q does not accept consent", string(unit))
		}
		policy = PolicyTrackedProjection
	} else if resCLI, _, isRes := ParseResourceUnit(unit); isRes {
		if c.resourceScope(resCLI) == nil {
			return fmt.Errorf("wsl supply unit %q does not accept consent", string(unit))
		}
		policy = PolicyTrackedProjection
	} else {
		adapter, ok := c.projections[unit]
		if !ok || !adapter.RequiresConsent() {
			return fmt.Errorf("wsl supply unit %q does not accept consent", string(unit))
		}
		policy = adapter.Policy()
	}
	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		return fmt.Errorf("resolve wsl supply target: %w", err)
	}
	grantPolicy := policy
	return c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		entry := StateEntry{
			Unit: unit, Policy: grantPolicy, Generation: target.Generation,
			Consented: true, LastOutcome: "consented", LastSuccess: time.Now(),
		}
		// F3（S1 diting）：仅同代才保留 hash 基线；跨代（distro 重装/换默认
		// 用户后重新同意）时新 generation 无任何基线——旧代 SourceHash/
		// LastWrittenHash 不继承（继承会让新 distro 首发布被误判
		// local-deletion-frozen，重新同意即失效）。S4：同代的键级基准与选择集
		// 同样保留（幂等再授权不得摧毁 Subs/SelectedKeys——否则后续运行会把
		// 已托管键误判为 unmanaged-existing）。
		if prev.Generation == target.Generation {
			entry.SourceHash = prev.SourceHash
			entry.LastWrittenHash = prev.LastWrittenHash
			entry.Subs = prev.Subs
			entry.SelectedKeys = prev.SelectedKeys
		}
		st.upsertEntry(entry, target.Generation)
		st.Distro = target.Distro
		st.Home = target.Home
	})
}

// ---------------------------------------------------------------------------
// 提交原语（design §七「状态落点与提交」全段）
// ---------------------------------------------------------------------------

// submitFile 经目标内私有 staging 提交一个文件：唯一临时名（os.CreateTemp，
// 并发提交不撞名）→ 权限先收紧（0600，CreateTemp 本就以 0600 创建，共享目录
// 不落宽权限临时文件）→ rename → distro 内 chmod 600 补偿（9P 不落 POSIX 位）
// → 读回校验。字节走 UNC，不进 wsl.exe 命令行。返回可条件撤销记录（普通文件
// 写入专用；A2 不走本原语）。
//
// chmod 补偿失败的处置按单元敏感度分叉（diting M2）:
//   - 敏感单元（内嵌密钥）：维持 fail-closed 删除——宽松权限明文不留盘；
//   - 普通单元：不得留下半提交状态——优先恢复前像（用户旧配置保持可读，
//     design §六「旧 WSL 配置可读且安全 → 启动继续」）；恢复也失败才整体
//     清理（绝不落半提交新内容）。取舍：恢复写不经 chmod（原内容无密钥，
//     umask 默认权限可接受；有密钥的单元不会走到本分支）。
func (c *Coordinator) submitFile(target TargetRef, relPath string, content []byte, sensitive bool) (*CompensationHandle, error) {
	uncPath := filepath.Join(target.UNCRoot, filepath.FromSlash(relPath))
	dir := filepath.Dir(uncPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir supply target dir: %w", err)
	}
	prev, prevErr := os.ReadFile(uncPath)
	prevExisted := prevErr == nil
	if prevErr != nil && !errors.Is(prevErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read supply target preimage: %w", prevErr)
	}
	tmp, err := os.CreateTemp(dir, ".amagi-supply-*")
	if err != nil {
		return nil, fmt.Errorf("create supply staging file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("write supply staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("close supply staging file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("chmod supply staging file: %w", err)
	}
	if err := os.Rename(tmpPath, uncPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("commit supply file: %w", err)
	}
	linuxPath := target.LinuxRoot + "/" + strings.TrimPrefix(relPath, "/")
	if err := c.probes.Chmod(target.Distro, "600", linuxPath); err != nil {
		if sensitive {
			// fail closed：宽松权限明文不留盘（对齐 A2/WriteWSL*AgentConfig 契约）。
			_ = os.Remove(uncPath)
			return nil, fmt.Errorf("chmod 600 supply file in distro: %w", err)
		}
		if restoreErr := restorePreimage(uncPath, dir, prev, prevExisted); restoreErr != nil {
			_ = os.Remove(uncPath)
			return nil, fmt.Errorf("chmod 600 supply file in distro: %w (preimage restore failed: %v; target removed)", err, restoreErr)
		}
		return nil, fmt.Errorf("chmod 600 supply file in distro: %w (preimage restored)", err)
	}
	got, err := os.ReadFile(uncPath)
	if err != nil || hexHash(got) != hexHash(content) {
		return nil, fmt.Errorf("verify supply file after commit: read-back mismatch (err=%v)", err)
	}
	return newCompensationHandle(target, relPath, content, prev, prevExisted, sensitive), nil
}

// restorePreimage 以唯一临时名 + rename 恢复前像（M2 普通单元 chmod 失败路径；
// 不做 chmod 补偿——原内容无密钥，umask 默认权限可接受）。首次创建后失败的
// 场景（无前像）删除新增目标。
func restorePreimage(uncPath, dir string, prev []byte, prevExisted bool) error {
	if !prevExisted {
		if err := os.Remove(uncPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".amagi-supply-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(prev); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, uncPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// Compensate 条件撤销普通文件写入：仅当目标当前 hash 仍等于本次写入值才恢复
// 原内容/删除新增文件；目标已被外部修改则保留外部内容（CAS，design §七）。
// A2 凭据刷新是持久认证状态，永不参与补偿。
//
// M1：补偿的「读 current→CAS 判定→submitFile/Remove」全程与 Apply 共用
// applyMu 串行化——无锁时 Abort 补偿可与并发 Apply 交错（TOCTOU：CAS 读过
// 后另一会话已 rename 新内容，补偿会删/覆盖该新写入），违背 design §七
// 「协调器按目标根/文件串行化」对补偿写入的声明。
func (c *Coordinator) Compensate(ctx context.Context, handles ...*CompensationHandle) []CompensationOutcome {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	out := make([]CompensationOutcome, 0, len(handles))
	for _, h := range handles {
		if h == nil {
			continue
		}
		out = append(out, c.compensateOne(ctx, h))
	}
	return out
}

func (c *Coordinator) compensateOne(ctx context.Context, h *CompensationHandle) CompensationOutcome {
	outcome := CompensationOutcome{Unit: h.unit, RelPath: h.relPath}
	if h.isDir {
		return c.compensateSkillDir(h)
	}
	if err := ctx.Err(); err != nil {
		outcome.Disposition = CompensationUnavailable
		outcome.Message = err.Error()
		return outcome
	}
	current, err := os.ReadFile(h.uncPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !h.prevExisted {
			outcome.Disposition = CompensationConfirmed
			return outcome
		}
		outcome.Disposition = CompensationIndeterminate
		outcome.Message = err.Error()
		return outcome
	}
	if hexHash(current) != h.writtenHash {
		// 目标已不等于本次写入：外部编辑保留，不覆盖（CAS 失配跳过）。
		outcome.Disposition = CompensationSkipped
		outcome.Message = "target changed after write; external content retained"
		return outcome
	}
	if !h.prevExisted {
		if err := os.Remove(h.uncPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			outcome.Disposition = CompensationUnavailable
			outcome.Message = err.Error()
			return outcome
		}
		outcome.Disposition = CompensationConfirmed
		return outcome
	}
	if _, err := c.submitFile(h.target, h.relPath, h.prev, h.sensitive); err != nil {
		outcome.Disposition = CompensationUnavailable
		outcome.Message = err.Error()
		return outcome
	}
	outcome.Disposition = CompensationConfirmed
	return outcome
}

// sha256Sum 是 hash 计算的统一小助手。
func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// sanitizeErrorCode 截断错误链为状态码式短串（不含路径细节与任何内容；
// launcher 的 A2 错误本就不含凭据内容，这里再加长度上限）。
func sanitizeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return msg
}
