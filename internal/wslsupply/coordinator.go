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

	// observations 是进程内操作观察（独立于 durable 清单；锁内深复制）。
	observations *observationRegistry
	// faults 是确定性故障接缝（测试注入；生产恒空）。
	faults *commitFaults
	// leaseMu/activeLeases 记录本进程活跃撤销租用（Reconcile 不清理）。
	leaseMu      sync.Mutex
	activeLeases map[string]bool

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
	// SA-06：隔离验证探测缺省时单独补齐（部分注入的探测面只关心身份/UNC/
	// chmod；敏感提交没有它一律 fail closed，不静默降级）。
	if probes.StatMode == nil {
		probes.StatMode = DefaultProbes().StatMode
	}
	// 单一状态清单实例：文件同步单元与插件重放登记共用同一落盘面（两个
	// stateStore 各持内存副本会互相覆盖对方写入）。
	state := newStateStore(configDir)
	c := &Coordinator{
		log:          log,
		probes:       probes,
		state:        state,
		observations: newObservationRegistry(),
		faults:       newCommitFaults(),
		activeLeases: map[string]bool{},
		projections:  map[UnitID]ProjectionAdapter{},
		resources:    map[CLI]*ResourceScope{},
		plugins:      NewPluginScope(state, nil, nil, PluginRefChecker{}),
	}
	c.plugins.commitFileFn = c.commitPluginFile
	return c
}

// commitPluginFile 是插件的内部提交回调（filters 的 settings.json 发布）：
// 统一文件提交/恢复原语（前像备份/intent/no-replace 发布/恢复），发布前重读
// 仍须等于构建基准（ExpectedBefore = 构建时原始字节 hash）。发布后才取消 →
// filter-committed/verification-pending（不声称零写）。
func (c *Coordinator) commitPluginFile(ctx context.Context, target TargetRef, relPath, expectedBeforeHash string, content []byte, pluginMeta map[string]string) *CommitReceipt {
	rcpt := c.commitFile(ctx, CommitRequest{
		Target: target, RelPath: relPath, ExpectedBefore: expectedBeforeHash,
		Candidate: content, Sensitive: false, Kind: OpKindWrite, PluginMeta: pluginMeta,
	})
	if rcpt.Err == nil {
		if cerr := ctx.Err(); cerr != nil {
			rcpt.Err = fmt.Errorf("filter committed to %s but request canceled after commit (verification pending; settings kept): %w", relPath, cerr)
			rcpt.ErrDetail = "filter-committed-verification-pending"
			c.recordObservation(rcpt.TargetKey, rcpt.OpID, "", OpEffectCommitted, OpRegistrationConfirmed, OpRecoveryNone, rcpt.ErrDetail, "plugin-filters")
		}
	}
	return rcpt
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

// generationFor 由 distro 名与可取得的 distro 身份标识组成目标 generation 指纹：
// sha256(distro|machine-id|uid|home)。distro 入指纹（SA-02）：克隆/导出的
// distro 保留 machine-id/uid/home，名字却不同——不含 distro 的指纹会让克隆体
// 继承原体的授权与基准。machine-id 拿不到（稳定身份不可得）时按
// GenerationUnknown 处理，不猜测继承。
func generationFor(distro string, id platform.WSLUserIdentity) string {
	if !id.OK || id.MachineID == "" {
		return GenerationUnknown
	}
	sum := sha256.Sum256([]byte(distro + "|" + id.MachineID + "|" + id.UID + "|" + id.Home))
	return hexEncode(sum[:])
}

// ResolveTarget 解析 distro 上的 CLI 目标（初版默认 distro，无新增选择器）。
// distro 为空、身份探测失败、目标根不可映射均返回错误——目标不可解析时调用方
// fail closed，不得把失败当空目标继续。
func (c *Coordinator) ResolveTarget(ctx context.Context, distro string, cli CLI) (TargetRef, error) {
	// PR07：取消的请求不进入探测面（ctx 不再只是被忽略的形参）。
	if err := ctx.Err(); err != nil {
		return TargetRef{}, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return TargetRef{}, errors.New("wsl distro is required")
	}
	rel, ok := cliTargetRoot(cli)
	if !ok {
		return TargetRef{}, fmt.Errorf("unsupported wsl supply CLI: %q", string(cli))
	}
	ident := c.probes.Identity(ctx, distro)
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
		Distro: distro, CLI: cli, UID: ident.UID, Home: ident.Home, MachineID: ident.MachineID,
		LinuxRoot: linuxRoot, UNCRoot: uncRoot, Generation: generationFor(distro, ident),
		LegacyGeneration: legacyGenerationOf(ident.MachineID, ident.UID, ident.Home),
	}, nil
}

// ---------------------------------------------------------------------------
// Inspect
// ---------------------------------------------------------------------------

// Inspect 对已解析目标做批量只读探测（文件元数据经 UNC os.Stat，不写、不
// spawn）。快照 mtime 是 UNC 视图值；A2 权威 mtime 判定在适配器内。普通
// （TrackedProjection）单元文件额外计算内容 hash（仅内存快照，供冲突判定）；
// A2 凭据文件不计算 hash。
func (c *Coordinator) Inspect(ctx context.Context, target TargetRef) (*Snapshot, error) {
	// PR07：取消的请求不做探测（与写入门同一生命周期）。
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("inspect wsl supply target: %w", err)
	}
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
		// F4（S1 diting）：检查时间按 CLI 作用域另记一份（兼容写入；v2 读取
		// 改走 Targets 内的 per-target per-CLI 时间）。
		if st.LastInspectedAtByCLI == nil {
			st.LastInspectedAtByCLI = map[CLI]time.Time{}
		}
		st.LastInspectedAtByCLI[target.CLI] = snap.InspectedAt
		// SA-02：身份索引——GetStatus 据此把分区归属到请求的 distro。
		if st.Targets == nil {
			st.Targets = map[string]TargetRecord{}
		}
		rec := st.Targets[target.Generation]
		rec.Distro = target.Distro
		rec.UID = target.UID
		rec.Home = target.Home
		rec.LegacyGeneration = target.LegacyGeneration
		rec.LastSeen = snap.InspectedAt
		if rec.InspectedAt == nil {
			rec.InspectedAt = map[CLI]time.Time{}
		}
		rec.InspectedAt[target.CLI] = snap.InspectedAt
		st.Targets[target.Generation] = rec
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
	// SA-16：等待锁前检查——已取消的请求不得排队进入写路径。
	if err := ctx.Err(); err != nil {
		return canceledResult(plan), nil
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	// SA-16：取得锁后再次检查——等锁期间取消的请求不得继续写。
	if err := ctx.Err(); err != nil {
		return canceledResult(plan), nil
	}

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
		// SA-16：每个单元提交前检查——批量中途取消时，后续单元不再写。
		if err := ctx.Err(); err != nil {
			result.Results = append(result.Results, UnitResult{
				Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitBlocked,
				Detail: "context-canceled", Err: err,
			})
			continue
		}
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
			// 攻坚契约：首个可能改变 auth/权限的动作前保存无秘密 pending 摘要；
			// 摘要写失败时不开始新的同步写。
			a2OpID := newOpID()
			if serr := c.saveA2PendingSummary(current, a2OpID); serr != nil {
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitFailed,
					Detail: "prepare-state-failed",
					Err:    fmt.Errorf("a2 pending summary persist failed; no sync writes started: %w", serr),
				})
				continue
			}
			outcome, syncErr := c.a2Adapter().Sync(ctx, current)
			unitResult := classifyA2(ctx, unit.Unit, outcome, syncErr)
			unitResult.OperationID = a2OpID
			committedOutcome := syncErr == nil && (outcome == launcher.SyncWSLPiLoginAuthCopied || outcome == launcher.SyncWSLPiLoginAuthDistroNewer || outcome == launcher.SyncWSLPiLoginAuthDistroTieKept)
			// 一致性修正（a2-final-save）：登记确认只在真正持久化后才声明——
			// 结果、observation、状态页与磁盘摘要同事实；state save 持续失败时
			// Registration=pending（PendingOperationFact=true，dirty/聚合不误计成
			// 功）；内容事实保留（不回滚新 token；不存 auth 正文/hash）。
			if serr := c.finishA2Summary(current, a2OpID, unitResult, outcome); serr != nil {
				unitResult.Registration = OpRegistrationPending
				unitResult.Detail = "a2-state-unpersisted"
				if committedOutcome {
					unitResult.Effect = OpEffectCommitted
				}
			} else if committedOutcome {
				unitResult.Effect = OpEffectCommitted
				unitResult.Consumption = ConsumptionNotApplicable // A2 资格由 outcome+权限门表达
				unitResult.Registration = OpRegistrationConfirmed
			}
			result.Results = append(result.Results, unitResult)
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
				if adapter.Sensitive() {
					// PR02：被选择消费的敏感既有副本（Fresh 零写入路径）统一验证
					// 目标权限——unknown 不等于安全；先收紧后复验，仍不可证则以
					// 失败上报（启动闸门据此阻断消费）；健康 0600 保持零字节重写。
					if perr := c.ensureSensitiveTargetPerms(ctx, current, unit.RelPath); perr != nil {
						result.Results = append(result.Results, UnitResult{
							Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitFailed,
							Detail: "sensitive-permissions-unsafe", Err: perr,
						})
						continue
					}
				}
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
				} else {
					// SA-15：非冻结阻断（如 source-unavailable）也逐单元登记——
					// 状态页不得沿用上次「已同步」。
					c.recordProjectionBlocked(current, unit.Unit, unit.Policy, unit.BlockedReason)
				}
			case ActionWriteProjection:
				result.Results = append(result.Results, c.applyProjection(ctx, current, unit, adapter))
			case ActionRemoveProjection:
				result.Results = append(result.Results, c.applyWithdrawal(ctx, current, unit, adapter))
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

// canceledResult 构造整批取消结果（SA-16：已取消请求不进入任何写路径）。
func canceledResult(plan *SupplyPlan) RunResult {
	res := RunResult{Target: plan.Request.Target, Results: make([]UnitResult, 0, len(plan.Units))}
	for _, unit := range plan.Units {
		res.Results = append(res.Results, UnitResult{
			Unit: unit.Unit, Policy: unit.Policy, Outcome: UnitBlocked,
			Detail: "context-canceled", Err: context.Canceled,
		})
	}
	return res
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
		r := result.Results[i]
		// 攻坚契约（PR05）：dirty 进度不把未确认当成功——登记未持久化或携
		// 带未决操作事实的结果不计入；WarnDegraded（降级消费旧副本）不再
		// 一律计成功（副本状态未确认）。
		if r.PendingOperationFact() {
			continue
		}
		if o := r.Outcome; o == UnitApplied || o == UnitFresh {
			succeeded = append(succeeded, r.Unit)
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
	// SA-01/SA-16：提交前重验授权——计划后授权被撤销/停跟随的单元不得写入
	//（Plan 时的同意门不足以覆盖计划→提交窗口内的状态变化）。
	if adapter.RequiresConsent() && !entry.Consented {
		c.recordProjectionBlocked(target, planned.Unit, adapter.Policy(), "awaiting-consent")
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked,
			Detail: "awaiting-consent",
			Err:    errors.New("unit consent was revoked between planning and apply; enable the projection again")}
	}
	if entry.Detached {
		c.recordProjectionBlocked(target, planned.Unit, adapter.Policy(), "detached")
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked,
			Detail: "detached",
			Err:    errors.New("unit was set to keep-local between planning and apply; take over to re-enable")}
	}
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
		c.recordProjectionFailure(target, planned.Unit, adapter.Policy(), detail, err)
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: detail, Err: err}
	}
	// CAS 前置复核已由 commitFile 的 ExpectedBefore 承担（拒绝旧计划，保留
	// 外部编辑）；完整生命周期（前像备份/intent/no-replace 发布/恢复）在
	// commit.go。
	req := CommitRequest{
		Target: target, Unit: planned.Unit, RelPath: planned.RelPath,
		ExpectedBefore: planned.ExpectedTargetHash, Candidate: cand.Content,
		Sensitive: adapter.Sensitive(), Kind: OpKindWrite,
		Register: func(st *StateFile) {
			registerProjectionEntryIn(st, target, planned.Unit, adapter.Policy(), cand.SourceHash,
				hexHash(cand.Content), cand.TransformVersion, cand.Subs, cand.Outcome)
			if legacySensitiveUnits[planned.Unit] {
				clearLegacySensitiveRisk(st, target.Distro, planned.Unit)
			}
		},
	}
	rcpt := c.commitFile(ctx, req)
	res := unitResultFromReceipt(planned.Unit, planned.Policy, rcpt, "write-failed")
	if rcpt.Err == nil {
		detail := cand.Outcome
		if detail == "" {
			detail = "written"
		}
		if len(cand.Notes) > 0 {
			// 部分构建失败/冲突保留等逐项诊断（不含密钥正文）：并入结果明细与日志。
			detail += "; " + strings.Join(cand.Notes, "; ")
			logWarn(c.log, "wslsupply", "供给单元部分诊断", strings.Join(cand.Notes, "; "))
		}
		res.Outcome = UnitApplied
		res.Detail = detail
	} else {
		// 登记面同步失败事实（持久登记失败时 observation/intent 仍在）。
		c.recordProjectionFailure(target, planned.Unit, adapter.Policy(), res.Detail, rcpt.Err)
	}
	return res
}

// unitResultFromReceipt 把提交回执映射为单元结果（错误路径 receipt 事实不丢：
// OperationID/Effect/Registration/Recovery/Undo 全部保留）。
func unitResultFromReceipt(unit UnitID, policy Policy, rcpt *CommitReceipt, defaultDetail string) UnitResult {
	res := UnitResult{
		Unit: unit, Policy: policy, OperationID: rcpt.OpID,
		Effect: rcpt.Effect, Registration: rcpt.Registration, Recovery: rcpt.Recovery,
		Undo: rcpt.UndoLease,
	}
	if rcpt.Err == nil {
		res.Outcome = UnitApplied
		return res
	}
	res.Outcome = UnitFailed
	res.Err = rcpt.Err
	if rcpt.ErrDetail != "" {
		res.Detail = rcpt.ErrDetail
	} else {
		res.Detail = defaultDetail
	}
	return res
}

// applyWithdrawal 执行 SA-09 撤回（攻坚契约 PR05 remove 语义）：保存
// remove intent 后条件移除/隔离并复验 ENOENT；T 后改/不可读 →
// withdrawal-unconfirmed（不删混合文件）；已证 absent 但 final save 失败 →
// removed-state-unpersisted（留 remove intent；不复活旧 write intent）。撤回
// 不产生 Undo（撤销会重新物化已撤回的密钥）。
func (c *Coordinator) applyWithdrawal(ctx context.Context, target TargetRef, planned PlannedUnit, adapter ProjectionAdapter) UnitResult {
	if err := ctx.Err(); err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitBlocked,
			Detail: "context-canceled", Err: err}
	}
	rcpt := c.commitRemove(ctx, target, planned.Unit, planned.RelPath, planned.ExpectedTargetHash)
	res := unitResultFromReceipt(planned.Unit, planned.Policy, rcpt, "withdrawal-remove-failed")
	res.Undo = nil
	switch rcpt.ErrDetail {
	case "":
		// 终结成功：登记撤回终态（清空写入基准，保留授权/选择/停跟随）。
		if werr := c.recordProjectionWithdrawn(target, planned.Unit, adapter.Policy()); werr != nil {
			res.Outcome = UnitWarnDegraded
			res.Detail = "withdrawn-state-unpersisted"
			res.Err = fmt.Errorf("target copy withdrawn but state registration failed to persist (re-runs re-confirm idempotently): %w", werr)
		} else {
			res.Outcome = UnitApplied
			if rcpt.Effect == OpEffectRemoved && !rcpt.BeforeExists {
				res.Detail = "withdrawn-already-absent"
			} else {
				res.Detail = "withdrawn"
			}
		}
	case "withdrawal-unconfirmed-frozen":
		c.recordProjectionFreeze(target, planned.Unit, adapter.Policy(), "withdrawal-unconfirmed-frozen")
		res.Outcome = UnitBlocked
	case "target-unreadable":
		c.recordProjectionFailure(target, planned.Unit, adapter.Policy(), "target-unreadable", rcpt.Err)
	case "removed-state-unpersisted":
		res.Outcome = UnitWarnDegraded
	default:
		c.recordProjectionFailure(target, planned.Unit, adapter.Policy(), rcpt.ErrDetail, rcpt.Err)
	}
	return res
}

// withdrawnUnpersistedResult 构造「副本已处理但登记未落盘」的降级结果
// （PR05）：撤回的用户可见目标（密钥副本移除）已达成，但清单未持久化——
// 不报 applied 成功；下次运行会幂等重确认（already-absent / 重新撤回）。
func (c *Coordinator) withdrawnUnpersistedResult(planned PlannedUnit, werr error) UnitResult {
	return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitWarnDegraded,
		Detail: "withdrawn-state-unpersisted",
		Err:    fmt.Errorf("target copy withdrawn but state registration failed to persist (re-runs re-confirm idempotently): %w", werr)}
}

// recordProjectionWithdrawn 登记撤回终态：清空写入基准（下次发布按首次发布
// 判定），保留同意/选择集/停跟随等跨写入字段。PR05：返回持久化错误。
// PR01：撤回确认副本已移除——同时清除该 distro 的 v1 迁移遗留风险标记。
func (c *Coordinator) recordProjectionWithdrawn(target TargetRef, unit UnitID, policy Policy) error {
	err := c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		st.upsertEntry(StateEntry{
			Unit: unit, Policy: policy, Generation: target.Generation,
			LastOutcome: "withdrawn", LastSuccess: time.Now(),
			Consented: prev.Consented, SelectedKeys: prev.SelectedKeys, Detached: prev.Detached,
		}, target.Generation)
		st.Distro = target.Distro
		if legacySensitiveUnits[unit] {
			clearLegacySensitiveRisk(st, target.Distro, unit)
			resolveLegacyEvidenceIn(st, unit, targetKeyOf(target), "withdrawn")
		}
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "撤回状态登记失败（目标副本已处理）", err.Error())
	}
	return err
}

// recordProjectionBlocked 登记非冻结阻断态（source-unavailable 等）：保留同代
// 基准字段，LastOutcome 更新为阻断原因（SA-15：状态页不得沿用上次已同步）。
func (c *Coordinator) recordProjectionBlocked(target TargetRef, unit UnitID, policy Policy, reason string) {
	err := c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		entry := StateEntry{
			Unit: unit, Policy: policy, Generation: target.Generation,
			LastOutcome: reason,
		}
		if prev.Generation == target.Generation {
			entry.SourceHash = prev.SourceHash
			entry.LastWrittenHash = prev.LastWrittenHash
			entry.TransformVersion = prev.TransformVersion
			entry.Subs = prev.Subs
			entry.Detached = prev.Detached
			entry.Consented = prev.Consented
			entry.SelectedKeys = prev.SelectedKeys
			entry.Frozen = prev.Frozen
			entry.FrozenReason = prev.FrozenReason
			entry.LastSuccess = prev.LastSuccess
		}
		st.upsertEntry(entry, target.Generation)
		st.Distro = target.Distro
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "阻断状态登记失败（不影响目标内容）", err.Error())
	}
}

// recordProjectionFailure 登记一次失败尝试（SA-15：新一次供给失败必须反映到
// 状态页，LastSuccess 保留为上次成功时间但 LastOutcome/LastErrorCode 区分
// 当前失败；同代基准字段保留——下次成功重建，不因失败清空）。
func (c *Coordinator) recordProjectionFailure(target TargetRef, unit UnitID, policy Policy, detail string, err error) {
	errCode := detail
	if err != nil {
		errCode = detail + ": " + sanitizeErrorCode(err)
	}
	werr := c.state.mutate(func(st *StateFile) {
		prev, _ := st.entry(unit)
		entry := StateEntry{
			Unit: unit, Policy: policy, Generation: target.Generation,
			LastOutcome: "failed", LastErrorCode: sanitizeErrorCode(errors.New(errCode)),
		}
		if prev.Generation == target.Generation {
			entry.SourceHash = prev.SourceHash
			entry.LastWrittenHash = prev.LastWrittenHash
			entry.TransformVersion = prev.TransformVersion
			entry.Subs = prev.Subs
			entry.Detached = prev.Detached
			entry.Consented = prev.Consented
			entry.SelectedKeys = prev.SelectedKeys
			entry.Frozen = prev.Frozen
			entry.FrozenReason = prev.FrozenReason
			entry.LastSuccess = prev.LastSuccess
		}
		st.upsertEntry(entry, target.Generation)
		st.Distro = target.Distro
	})
	if werr != nil {
		logWarn(c.log, "wslsupply", "失败状态登记失败（下次运行重试）", werr.Error())
	}
}

// registerProjectionEntryIn 在一次主清单 mutate 内写入投影单元登记（供提交
// 生命周期的 Register 闭包与独立登记路径共用——settled 与 StateEntry 同次
// 保存）。SourceHash 是规范化业务内容基准，LastWrittenHash 是实际落盘字节
// hash；Consented 等跨写入字段保留不丢失。
func registerProjectionEntryIn(st *StateFile, target TargetRef, unit UnitID, policy Policy, sourceHash, writtenHash, transformVersion string, subs map[string]SubHash, outcome string) {
	if outcome == "" {
		outcome = "written"
	}
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
	// PR01：本代成功写入（或接管）后，该 distro 的 v1 迁移遗留风险由当前
	// 受管状态接管（副本重新受管，后续撤回/状态面可如实判定）。v3 证据按
	// 完整目标键记录处置（clone 各自解除，互不误伤）。
	if legacySensitiveUnits[unit] {
		clearLegacySensitiveRisk(st, target.Distro, unit)
		resolveLegacyEvidenceIn(st, unit, targetKeyOf(target), "re-managed")
	}
}

// resolveLegacyEvidenceIn 在一次 mutate 内把证据条目标记为该完整目标已处置。
// 键位双写（完整 targetKey + 裸 generation）：消费面有两族键——逐目标 assessment
// 用完整键（targetKeyOf，v3 一致性修正后含 CLI 作用域），LegacySensitiveCopyPending
// 按 distro 的代际迭代用裸 generation 键；两键并存保证两族消费面同时收敛。
func resolveLegacyEvidenceIn(st *StateFile, unit UnitID, targetKey, disposition string) {
	for i := range st.LegacyEvidence {
		ev := &st.LegacyEvidence[i]
		if ev.Unit != unit {
			continue
		}
		if ev.Resolved == nil {
			ev.Resolved = map[string]string{}
		}
		ev.Resolved[targetKey] = disposition
		if gen, _, found := strings.Cut(targetKey, "|"); found && gen != "" {
			ev.Resolved[gen] = disposition
		}
	}
}

// recordProjectionState 更新投影单元登记（独立登记路径；提交路径经
// CommitRequest.Register 同次保存）。
func (c *Coordinator) recordProjectionState(target TargetRef, unit UnitID, policy Policy, sourceHash, writtenHash, transformVersion string, subs map[string]SubHash, outcome string) error {
	err := c.state.mutate(func(st *StateFile) {
		registerProjectionEntryIn(st, target, unit, policy, sourceHash, writtenHash, transformVersion, subs, outcome)
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "投影单元状态登记失败（内容已提交，下次视为待核实）", err.Error())
	}
	return err
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
	// SA-16：入口检查——已取消的请求不得解析目标/探测/写任何配置。
	if err := ctx.Err(); err != nil {
		return blocked("context-canceled", err)
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
	// 攻坚契约 PR05：Plan 前持 applyMu 对请求目标运行 ReconcilePending（只处理
	// 请求目标的登记路径；存储不可读停止新写）。
	// 一致性修正（corrupt-pending）：健康门先完成本次读取/扫描再决策——先扫
	// pending（刷新健康值），后判门；不靠上一轮缓存（首轮健康值是构造默认值，
	// 会把损坏目录当 ok 放行新写）。
	if _, ierr := c.state.loadIntents(); ierr != nil {
		return blocked("pending-store-unreadable", ierr)
	}
	if h := c.state.Health(); h.Main != StoreOK && h.Main != StoreFresh || h.Pending == StoreUnreadable || h.Pending == StoreCorrupt {
		return blocked("store-unhealthy", fmt.Errorf("wsl supply state store unhealthy (main=%s pending=%s)", h.Main, h.Pending))
	}
	c.applyMu.Lock()
	recon := c.ReconcilePending(ctx, target)
	c.applyMu.Unlock()
	_ = recon // 未决项由 Plan/Apply 与启动资格面按事实消费（GetStatus 展示）
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
// 不出本接口；A2 单元从不持有 hash。SA-02：分区归属——按身份索引把请求
// distro 的最近一代分区呈现出来；其它 distro 的登记（包括 legacy 隔离区与
// unknown 槽）不串入本目标的展示。
func (c *Coordinator) GetStatus(distro string, cli CLI) TargetStatus {
	distro = strings.TrimSpace(distro)
	st := c.state.load()
	status := TargetStatus{Distro: distro, CLI: cli}
	// 待同步来源与 desired revision 是宿主侧状态，不随目标分发。
	status.DesiredRevision = st.DesiredRevision
	if len(st.PendingSources) > 0 {
		status.PendingSources = make(map[string]uint64, len(st.PendingSources))
		for k, v := range st.PendingSources {
			status.PendingSources[k] = v
		}
	}
	gen, rec, found := latestTargetRecord(&st, distro)
	// 一致性修正（status-without-target）：未检查（Checked=false）可以，但现有
	// 待确认事实（pending intents）与健康错误必须展示——read-only 状态查询不
	// 能靠先执行一次写入才看见风险。
	status.StoreHealth = c.state.Health()
	status.PendingIntents = c.pendingIntentsForDistro(st, distro)
	if !found {
		return status
	}
	status.Generation = gen
	status.Home = rec.Home
	// 只读该 CLI 作用域在该目标上的实际探测时间（无记录 → 未检查）。
	if t, ok := rec.InspectedAt[cli]; ok && !t.IsZero() {
		status.Checked = true
		status.CheckedAt = t
	}
	// 攻坚契约：合并 durable、intent、当前 observation——pending/失败优先于旧
	// written/consented/awaiting-consent；旧 LastSuccess 保留但明确是历史时间。
	for _, entry := range st.Units {
		if entry.Generation != gen {
			continue // 其它身份分区的登记不串入本目标（SA-02）。
		}
		us := UnitStatus{
			Unit: entry.Unit, Policy: entry.Policy, LastOutcome: entry.LastOutcome,
			LastSuccess: entry.LastSuccess, LastErrorCode: entry.LastErrorCode,
			TransformVersion: entry.TransformVersion, Frozen: entry.Frozen,
			FrozenReason: entry.FrozenReason, Consented: entry.Consented,
			Detached: entry.Detached,
			Stale:    c.skillUnitStale(entry) || c.resourceUnitStale(entry),
		}
		us.LastAttemptOutcome = entry.LastOutcome
		if opID, pending := c.PendingSensitiveOperation(distro, entry.Unit); pending {
			us.PendingOperation = opID
			us.StateUnpersisted = true
			us.LastAttemptOutcome = "pending-operation"
		}
		status.Units = append(status.Units, us)
	}
	return status
}

// pendingIntentsForDistro 汇总该 distro 的未决 intent 摘要（脱敏：OpID/kind/
// unit，无正文/hash）。
func (c *Coordinator) pendingIntentsForDistro(st StateFile, distro string) []PendingIntentSummary {
	intents, err := c.state.loadIntents()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(intents))
	for id := range intents {
		ids = append(ids, id)
	}
	sortStrings(ids)
	var out []PendingIntentSummary
	for _, id := range ids {
		op := intents[id]
		if op.TargetDistro != distro {
			continue
		}
		if _, settled := st.SettledOperations[op.OpID]; settled {
			continue
		}
		out = append(out, PendingIntentSummary{OpID: op.OpID, Kind: op.Kind, Unit: op.Unit, RelPath: op.RelPath})
	}
	return out
}

// latestTargetRecord 返回请求 distro 最近接触的一代（generation、记录、是否
// 命中）。legacy 隔离代与 unknown 槽永不命中（无法归属到具体 distro/身份）。
func latestTargetRecord(st *StateFile, distro string) (string, *TargetRecord, bool) {
	var bestGen string
	var best *TargetRecord
	for gen, rec := range st.Targets {
		if rec.Distro != distro || gen == GenerationUnknown || strings.HasPrefix(gen, legacyGenerationPrefix) {
			continue
		}
		r := rec
		if best == nil || r.LastSeen.After(best.LastSeen) {
			best = &r
			bestGen = gen
		}
	}
	return bestGen, best, best != nil
}

// SensitiveCopyPending 报告该 distro 当前展示代上某敏感单元是否可能仍存在
// CodeBox 受管副本（PR01：启动闸门据此区分「证明无受管副本」与「可能残留」）。
// 判定基于完整登记（GetStatus 的脱敏面不够）：无登记/从未写入 → false；已确认
// 撤回（withdrawn*）→ false；用户显式保留本地（Detached，副本归用户）→ false；
// 目标已被外部删除（local-deletion-frozen）→ false；其余曾写入状态 → true。
// 只看当前展示代：其它 distro / 旧代 / legacy 隔离区的登记不计入（严格区分
// 目标，v1 迁移风险另由 LegacySensitiveCopyPending 覆盖）。
func (c *Coordinator) SensitiveCopyPending(distro string, unit UnitID) bool {
	st := c.state.load()
	gen, _, found := latestTargetRecord(&st, distro)
	if !found {
		// 兼容线索：身份索引尚未记录时用顶层 Distro/Generation。
		if st.Distro != distro || st.Generation == "" || st.Generation == GenerationUnknown || strings.HasPrefix(st.Generation, legacyGenerationPrefix) {
			return false
		}
		gen = st.Generation
	}
	return entrySensitiveCopyPossible(st.entryFor(unit, gen))
}

// LegacySensitiveCopyPending 报告该 distro 是否存在迁移遗留的敏感副本待确认
// 风险（兼容只读包装；启动资格面请用 AssessSensitiveConsumption）。v3 语义：
// 证据按旧指纹归属（TargetRecord.LegacyGeneration）——A 发布→只检查 B→升级
// 时 A 阻（指纹匹配）、B 不凭顶层线索继承风险；该 distro 的所有已解析代中
// 任一未解除匹配指纹 → true。无已解析代但有顶层 distro 线索时保守保留线索
// （不丢事实）；无归属/unknown 不全局误阻其它 distro。
func (c *Coordinator) LegacySensitiveCopyPending(distro string) bool {
	if strings.TrimSpace(distro) == "" {
		return false // 无 distro 的查询无可归属面
	}
	st := c.state.load()
	gensOfDistro := map[string]string{} // gen → legacyGeneration
	for gen, rec := range st.Targets {
		if rec.Distro == distro {
			gensOfDistro[gen] = rec.LegacyGeneration
		}
	}
	for i := range st.LegacyEvidence {
		ev := st.LegacyEvidence[i]
		if ev.Unit != UnitSharedVisionExport || ev.TerminalRiskCleared() {
			continue
		}
		if ev.TopDistro != "" && ev.TopDistro != distro {
			continue // 顶层线索指向其它 distro：不误算（线索只算最近检查）
		}
		if ev.TopDistro == "" && ev.Fingerprint == "" && !ev.LowTrust {
			continue // 无任何归属线索：交给逐目标 assessment，不全局误阻
		}
		resolvedFor := func() bool {
			// 该 distro 的所有已解析代中，任一代匹配指纹或已显式处置 → 解除。
			matchedAny := false
			for gen, legacyGen := range gensOfDistro {
				if d, ok := ev.Resolved[gen]; ok && d != "" {
					return true
				}
				if ev.Fingerprint != "" && legacyGen != "" && ev.Fingerprint == legacyGen {
					matchedAny = true
				}
			}
			if matchedAny {
				return false // 指纹确实匹配该 distro 的某代且未处置：风险仍在
			}
			// 无指纹匹配：仅当无任何已解析代（线索不足以逐目标核验）时保守保留。
			return len(gensOfDistro) > 0
		}
		if !resolvedFor() {
			return true
		}
	}
	return false
}

// entrySensitiveCopyPending 判定单条登记是否意味着受管敏感副本可能仍在。
func entrySensitiveCopyPossible(e StateEntry) bool {
	if e.Detached {
		return false // 用户显式保留本地：副本归属用户，不再是受管副本风险
	}
	if strings.HasPrefix(e.LastOutcome, "withdrawn") {
		return false // 已确认撤回（副本已移除或从未存在）
	}
	if e.Frozen && e.FrozenReason == "local-deletion-frozen" {
		return false // 目标副本已被外部删除
	}
	return e.LastWrittenHash != ""
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
// 目标上也可停跟随——PR04：经身份索引解析明确目标 generation，不取全局
// 最近代；同名 distro 多代并存时拒绝歧义而不改别人）。
func (c *Coordinator) DropUnitRegistration(distro string, cli CLI, unit UnitID) error {
	if _, isSkill := ParseSkillUnit(unit); isSkill {
		if c.skills == nil {
			return fmt.Errorf("unknown or non-projection wsl supply unit: %q", string(unit))
		}
		return c.detachUnitOnDistro(distro, unit, func(prev StateEntry) StateEntry {
			prev.Detached = true
			prev.LastOutcome = "detached"
			prev.LastSuccess = time.Now()
			return prev
		})
	}
	if resCLI, _, isRes := ParseResourceUnit(unit); isRes {
		if c.resourceScope(resCLI) == nil {
			return fmt.Errorf("unknown or non-projection wsl supply unit: %q", string(unit))
		}
		return c.detachUnitOnDistro(distro, unit, func(prev StateEntry) StateEntry {
			prev.Detached = true
			prev.LastOutcome = "detached"
			prev.LastSuccess = time.Now()
			return prev
		})
	}
	adapter, ok := c.projections[unit]
	if !ok {
		return fmt.Errorf("unknown or non-projection wsl supply unit: %q", string(unit))
	}
	_ = adapter
	return c.detachUnitOnDistro(distro, unit, func(prev StateEntry) StateEntry {
		prev.Detached = true
		prev.LastOutcome = "detached"
		prev.LastSuccess = time.Now()
		return prev
	})
}

// detachUnitOnDistro 在解析出的目标 generation 上对登记应用变更（PR04）：
// 零值 entry（未登记）报错而非静默追加空代登记；变更不搬动其它目标的
// 授权与基准（entryFor 同代命中才拿到 prev）。
func (c *Coordinator) detachUnitOnDistro(distro string, unit UnitID, mutate func(prev StateEntry) StateEntry) error {
	var resolveErr error
	err := c.state.mutate(func(st *StateFile) {
		gen, rerr := resolveDistroGeneration(st, distro)
		if rerr != nil {
			resolveErr = rerr
			return
		}
		prev := st.entryFor(unit, gen)
		if prev.Generation != gen {
			resolveErr = fmt.Errorf("wsl supply unit %q has no registration on target %q", string(unit), distro)
			return
		}
		st.upsertEntry(mutate(prev), gen)
	})
	if err != nil {
		return err
	}
	return resolveErr
}

// TakeOverUnit 执行「预览后确认接管」：以预览时目标 hash 做 CAS（旧预览不
// 覆盖新编辑——目标已变则拒绝并要求重新检查），随后用当前来源生成接管候选
// 并提交，重建键级基准。仅键级适配器支持。PR07：入口与等锁后检查取消——
// 已取消的请求不得进入任何接管写入。
func (c *Coordinator) TakeOverUnit(ctx context.Context, distro string, cli CLI, unit UnitID, expectedTargetHash string) (UnitResult, error) {
	if err := ctx.Err(); err != nil {
		return UnitResult{Unit: unit, Outcome: UnitBlocked, Detail: "context-canceled", Err: err}, nil
	}
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
	// PR07：等锁期间取消的请求不得进入接管写入。
	if err := ctx.Err(); err != nil {
		return UnitResult{Unit: unit, Policy: adapter.Policy(), Outcome: UnitBlocked,
			Detail: "context-canceled", Err: err}, nil
	}

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
	rcpt := c.commitFile(ctx, CommitRequest{
		Target: target, Unit: unit, RelPath: planned.RelPath,
		ExpectedBefore: expectedTargetHash, Candidate: cand.Content,
		Sensitive: adapter.Sensitive(), Kind: OpKindWrite,
		Register: func(st *StateFile) {
			registerProjectionEntryIn(st, target, unit, adapter.Policy(), cand.SourceHash,
				hexHash(cand.Content), cand.TransformVersion, cand.Subs, cand.Outcome)
			if legacySensitiveUnits[unit] {
				clearLegacySensitiveRisk(st, target.Distro, unit)
			}
		},
	})
	res := unitResultFromReceipt(unit, planned.Policy, rcpt, "write-failed")
	if rcpt.Err == nil {
		takeoverDetail := "takeover-written"
		if len(cand.Notes) > 0 {
			takeoverDetail += "; " + strings.Join(cand.Notes, "; ")
		}
		res.Outcome = UnitApplied
		res.Detail = takeoverDetail
	}
	return res, nil
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
	// SA-02：无法验证稳定身份的目标（unknown 代）不得持久化跨界授权——密钥
	// 跨界授权必须绑在可验证的身份指纹上，拒绝并说明修复方向，而不是存一个
	// 任何 unknown 目标都能继承的同意标记。
	if target.Generation == GenerationUnknown {
		return fmt.Errorf("无法验证 WSL 目标身份（distro %q 拿不到 machine-id）：跨界授权需要可验证的目标身份；请在 WSL 内恢复 /etc/machine-id 后重试", distro)
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
		// SA-02：授权也记入身份索引（授权后首次 Inspect 前，状态页能归属分区）。
		if st.Targets == nil {
			st.Targets = map[string]TargetRecord{}
		}
		rec := st.Targets[target.Generation]
		rec.Distro = target.Distro
		rec.UID = target.UID
		rec.Home = target.Home
		rec.LegacyGeneration = target.LegacyGeneration
		rec.LastSeen = time.Now()
		st.Targets[target.Generation] = rec
	})
}

// ---------------------------------------------------------------------------
// 提交原语（design §七「状态落点与提交」全段）
// ---------------------------------------------------------------------------

// submitFile 经目标内提交原语提交一个文件，返回可条件撤销记录（普通文件
// 写入专用；A2 不走本原语）。字节走 UNC，不进 wsl.exe 命令行。
//
// 敏感单元（内嵌密钥，SA-06/07）：
//  1. 先在 distro 内建立目标根下的 .amagi-staging 私有目录，distro 内
//     chmod 700 并经 StatMode 探测验证真实权限位（UNC/宿主 stat 的 9P 翻译
//     不可信）——验证失败时在任何秘密字节落盘前失败；
//  2. 秘密字节只写入 staging 内唯一临时名，distro 内 chmod 600 并验证后才
//     rename 到最终路径——最终目录即使 0755，文件也从未以宽权限可见；
//  3. 任何阶段失败都发生在 rename 之前：原有混合配置（含用户自有内容）
//     原样保留，绝不以删整文件充当回滚（SA-07）；rename 后的读回校验失败
//     则先安全恢复前像，恢复也失败才移除并明确报告两种结果。
//
// 普通单元沿用 M2 语义：同目录唯一临时名 → 宿主 chmod 0600 → rename →
// distro 内 chmod 600 补偿；补偿失败优先恢复前像（用户旧配置保持可读）。
// submitFileCtx 是旧签名兼容入口（保留同壳面：既有测试桩/少量调用方）；
// 生产路径（applyProjection/TakeOver/资源/插件）一律直接构造 CommitRequest 走
// commitFile——完整生命周期（intent/备份/no-replace/恢复）在 commit.go。
func (c *Coordinator) submitFileCtx(ctx context.Context, target TargetRef, relPath string, content []byte, sensitive bool) (*CompensationHandle, error) {
	// 旧壳面保留旧「最后写者胜」语义：publish 冲突（并发写同一路径）时以
	// 当前目标为新前像有界重试一次（最多 2 次尝试，不递归）。
	for attempt := 0; attempt < 2; attempt++ {
		rcpt := c.commitFile(ctx, CommitRequest{
			Target: target, RelPath: relPath, Candidate: content, Sensitive: sensitive, Kind: OpKindWrite,
		})
		if rcpt.Err == nil {
			return rcpt.UndoLease, nil
		}
		if rcpt.ErrDetail == "publish-conflict" || rcpt.ErrDetail == "target-changed-since-planning" {
			continue
		}
		return nil, rcpt.Err
	}
	return nil, errors.New("submit file lost a bounded race with concurrent writers; retry")
}

// submitFile 是无 ctx 接线的兼容入口（保留同壳面：测试桩/未来调用方使用；
// 生产路径一律用 commitFile——取消保护由 ctx 检查承担）。
func (c *Coordinator) submitFile(target TargetRef, relPath string, content []byte, sensitive bool) (*CompensationHandle, error) {
	return c.submitFileCtx(context.Background(), target, relPath, content, sensitive)
}

// ensureSensitiveTargetPerms 验证并（必要时）收紧敏感单元既有副本的目标
// 权限（PR02）：健康 0600 → 零字节重写保持；未知（探测不可用）或非 0600
// → 先 chmod 600 再复验，仍不可证 → 返回错误（调用方以失败上报，启动闸门
// 阻断消费）。不删除任何文件。
func (c *Coordinator) ensureSensitiveTargetPerms(ctx context.Context, target TargetRef, relPath string) error {
	if c.probes.StatMode == nil {
		return fmt.Errorf("sensitive supply file %s permissions unverifiable (stat-mode probe unavailable)", relPath)
	}
	linuxPath := target.LinuxRoot + "/" + strings.TrimPrefix(relPath, "/")
	if mode, ok := c.probes.StatMode(ctx, target.Distro, linuxPath); ok && mode == "600" {
		return nil // 健康 0600：保持零字节重写
	}
	// PR07：收紧是写面动作——已取消的请求不做任何权限改写。
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sensitive supply file %s permission re-tighten canceled: %w", relPath, err)
	}
	if err := c.probes.Chmod(ctx, target.Distro, "600", linuxPath); err != nil {
		return fmt.Errorf("re-tighten sensitive supply file %s (unknown or loose mode): %w", relPath, err)
	}
	if mode, ok := c.probes.StatMode(ctx, target.Distro, linuxPath); !ok || mode != "600" {
		return fmt.Errorf("sensitive supply file %s permissions unverified after re-tighten: mode=%q ok=%v (expected 600)", relPath, mode, ok)
	}
	return nil
}

// ensurePrivateStaging 建立/验证目标根下的 .amagi-staging：MkdirAll → distro
// 内 chmod 700 → StatMode 验证。验证不可用或不等于 700 都拒绝（SA-06：隔离
// 未验证 = 未隔离，fail closed，不得把探测失败当作已验证）。
func (c *Coordinator) ensurePrivateStaging(ctx context.Context, target TargetRef) (uncDir, linuxDir string, err error) {
	uncDir = filepath.Join(target.UNCRoot, ".amagi-staging")
	linuxDir = strings.TrimSuffix(target.LinuxRoot, "/") + "/.amagi-staging"
	if err := os.MkdirAll(uncDir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir supply staging dir: %w", err)
	}
	if err := c.probes.Chmod(ctx, target.Distro, "700", linuxDir); err != nil {
		return "", "", fmt.Errorf("chmod 700 supply staging dir in distro: %w", err)
	}
	if c.probes.StatMode == nil {
		return "", "", errors.New("supply staging isolation cannot be verified (stat-mode probe unavailable); refusing to write secrets")
	}
	mode, ok := c.probes.StatMode(ctx, target.Distro, linuxDir)
	if !ok || mode != "700" {
		return "", "", fmt.Errorf("supply staging isolation unverified: mode=%q ok=%v (expected 700); refusing to write secrets", mode, ok)
	}
	return uncDir, linuxDir, nil
}

// Compensate 条件撤销普通文件写入（攻坚契约「补偿与释放」）：持 applyMu、
// 逆提交顺序；先保存 compensation intent（ParentOpID 指原操作）再恢复；使用
// 与请求取消独立的有限恢复预算（context.WithTimeout(WithoutCancel(ctx), 10s)，
// 每步至多一次）——取消停止业务，不阻止事实登记/恢复；预算耗尽留 pending。
// 恢复采用保留 displaced 的 no-replace 流程，绝不先删当前再试写备份；目标
// 已被外部修改 → skipped（旧句柄补偿遇后一次内容必须 skipped）。不递归补偿。
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

func (c *Coordinator) compensateOne(requestCtx context.Context, h *CompensationHandle) CompensationOutcome {
	outcome := CompensationOutcome{Unit: h.unit, RelPath: h.relPath}
	if h.isDir {
		return c.compensateSkillDir(h)
	}
	// 撤销租用仍在：新写不得清理本操作材料（Reconcile 尊重活跃租用）。
	if h.opID != "" {
		c.setLeaseActive(h.opID, true)
	}
	// 保存 compensation intent（ParentOpID 指原操作；逆提交顺序的写前事实）。
	compOpID := newOpID()
	if h.opID != "" {
		if err := c.state.saveIntent(OperationIntent{
			OpID: compOpID, Kind: OpKindCompensate, TargetKey: h.targetKey,
			TargetDistro: h.target.Distro, TargetHome: h.target.Home,
			TargetCLI: string(h.target.CLI),
			Unit: h.unit, RelPath: h.relPath, Sensitive: h.sensitive,
			BeforeExists: h.prevExisted, BeforeHash: hexHash(h.prev), AfterHash: h.writtenHash,
			ParentOpID: h.opID, CreatedAt: time.Now(),
		}); err != nil {
			// 补偿 intent 保存失败：不开始恢复写（不先写后补记），如实报 pending。
			outcome.Disposition = CompensationUnavailable
			outcome.Message = "compensation intent persist failed: " + err.Error()
			return outcome
		}
	}
	rctx, cancel := boundedRecoveryCtx(requestCtx)
	defer cancel()
	current, err := os.ReadFile(h.uncPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !h.prevExisted {
			outcome.Disposition = CompensationConfirmed
			c.finishCompensation(h, compOpID, "restored")
			return outcome
		}
		outcome.Disposition = CompensationIndeterminate
		outcome.Message = err.Error()
		return outcome
	}
	if hexHash(current) != h.writtenHash {
		// 目标已不等于本次写入：外部编辑/后一次写入保留，不覆盖（CAS 失配）。
		outcome.Disposition = CompensationSkipped
		outcome.Message = "target changed after write; external content retained"
		c.finishCompensation(h, compOpID, "not-applied")
		return outcome
	}
	if !h.prevExisted {
		// 撤回本次新增（保留 displaced 再核验；失败不盲删）。
		if rmErr := os.Remove(h.uncPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			outcome.Disposition = CompensationUnavailable
			outcome.Message = rmErr.Error()
			return outcome
		}
		outcome.Disposition = CompensationConfirmed
		c.finishCompensation(h, compOpID, "restored")
		return outcome
	}
	if err := c.restoreWrittenToPreimage(rctx, h, compOpID); err != nil {
		outcome.Disposition = CompensationUnavailable
		outcome.Message = err.Error()
		c.recordObservation(h.targetKey, compOpID, h.unit, OpEffectUnknown, OpRegistrationPending, OpRecoveryAvailable, "compensation-pending", "compensate")
		return outcome
	}
	outcome.Disposition = CompensationConfirmed
	c.finishCompensation(h, compOpID, "restored")
	return outcome
}

// restoreWrittenToPreimage 用保留 displaced 的 no-replace 流程把本次写入恢复
// 为前像：独立恢复 staging（复验前像字节）→ 目标移入唯一 displaced 名 → 核验
// → no-replace 落位 → 读回确认；外部新目标拒绝并保留两处；每步至多一次。
func (c *Coordinator) restoreWrittenToPreimage(rctx context.Context, h *CompensationHandle, compOpID string) error {
	if err := rctx.Err(); err != nil {
		return fmt.Errorf("recovery budget exhausted: %w", err)
	}
	// 独立恢复 staging：前像字节先持久并复验（不用内存唯一副本直接覆盖）。
	stagingRel, err := c.persistCandidateStaging(rctx, h.target, "restore-"+compOpID, ".amagi-staging/restore-"+compOpID+"/candidate", h.prev, h.sensitive)
	if err != nil {
		return fmt.Errorf("stage verified preimage: %w", err)
	}
	displaced := h.uncPath + ".compensated-" + compOpID
	if _, derr := os.Lstat(displaced); derr == nil {
		return fmt.Errorf("displaced slot occupied: %s", filepath.Base(displaced))
	}
	if err := os.Rename(h.uncPath, displaced); err != nil {
		_ = os.RemoveAll(absOf(h.target, stagingRel))
		return fmt.Errorf("displace current write: %w", err)
	}
	if got, derr := os.ReadFile(displaced); derr != nil || hexHash(got) != h.writtenHash {
		// 移走的不是本次写入：no-replace 放回，保留两处。
		if rbErr := publishNoReplaceFn(displaced, h.uncPath); rbErr != nil {
			_ = os.RemoveAll(absOf(h.target, stagingRel))
			return fmt.Errorf("displaced entity mismatch; put-back failed (%v); both retained", rbErr)
		}
		_ = os.RemoveAll(absOf(h.target, stagingRel))
		return errors.New("target changed to unknown content; external version retained")
	}
	if err := publishNoReplaceFn(absOf(h.target, stagingRel), h.uncPath); err != nil {
		if isNoReplaceExists(err) {
			// 原路径出现新目标：保留两处并报冲突，绝不删任何一处。
			return fmt.Errorf("external target appeared during restore; both retained (preimage staged at %s, displaced at %s)", stagingRel, filepath.Base(displaced))
		}
		return fmt.Errorf("publish verified preimage (no-replace): %w (preimage staged at %s; displaced at %s)", err, stagingRel, filepath.Base(displaced))
	}
	got, rerr := os.ReadFile(h.uncPath)
	if rerr != nil || hexHash(got) != hexHash(h.prev) {
		return fmt.Errorf("verify restored bytes (err=%v)", rerr)
	}
	_ = os.Remove(displaced)
	if h.sensitive {
		if err := c.probes.Chmod(rctx, h.target.Distro, "600", h.linuxPath); err != nil {
			return fmt.Errorf("retighten restored sensitive file: %w", err)
		}
		if mode, ok := c.probes.StatMode(rctx, h.target.Distro, h.linuxPath); !ok || mode != "600" {
			return fmt.Errorf("verify restored sensitive mode: got %q ok=%v", mode, ok)
		}
	}
	return nil
}

// finishCompensation 保存补偿 settled + 相关 Parent 结论，终结清理（同次
// 记录 RelatedOpIDs，不能只删最新 intent 留旧发布重新出现）。
func (c *Coordinator) finishCompensation(h *CompensationHandle, compOpID, disposition string) {
	err := c.state.mutate(func(st *StateFile) {
		if st.SettledOperations == nil {
			st.SettledOperations = map[string]SettledOperation{}
		}
		related := []string{}
		if h.opID != "" {
			related = append(related, h.opID)
		}
		st.SettledOperations[compOpID] = SettledOperation{
			TargetKey: h.targetKey, Unit: h.unit, Disposition: disposition,
			RelatedOpIDs: related, SettledAt: time.Now(),
		}
		if h.opID != "" {
			// 父操作结论同次记录（旧 write intent 不复活秘密）。
			st.SettledOperations[h.opID] = SettledOperation{
				TargetKey: h.targetKey, Unit: h.unit, Disposition: disposition,
				RelatedOpIDs: []string{compOpID}, SettledAt: time.Now(),
			}
		}
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "补偿 settled 保存失败（intent 保留待收敛）", compOpID)
		return
	}
	_ = c.state.deleteIntent(compOpID)
	if h.opID != "" {
		c.setLeaseActive(h.opID, false)
		_ = c.state.deleteIntent(h.opID)
		err = c.state.mutate(func(st *StateFile) {
			delete(st.SettledOperations, compOpID)
			delete(st.SettledOperations, h.opID)
		})
	}
	c.recordObservation(h.targetKey, compOpID, h.unit, OpEffectRestored, OpRegistrationConfirmed, OpRecoveryNone, "compensated", "compensate")
	if err != nil {
		logWarn(c.log, "wslsupply", "补偿墓碑清理失败（下次重启收敛）", compOpID)
	}
}

// ReleaseCompensations 解除已终结操作的撤销租用（不表示撤回）：仅清理
// settled 操作的前像/候选/displaced 材料与 intent/墓碑；未确认操作不可清
// 材料。remote effect Commit→Release；desktop/manual/save 成功路径同样调用。
func (c *Coordinator) ReleaseCompensations(ctx context.Context, handles ...*CompensationHandle) []CompensationOutcome {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	out := make([]CompensationOutcome, 0, len(handles))
	for _, h := range handles {
		if h == nil {
			continue
		}
		outcome := CompensationOutcome{Unit: h.unit, RelPath: h.relPath}
		if h.opID == "" {
			// 旧内存句柄（无持久操作）：直接确认释放。
			outcome.Disposition = CompensationConfirmed
			out = append(out, outcome)
			continue
		}
		c.setLeaseActive(h.opID, false)
		if settled, ok := c.settledOf(h.opID); !ok || settled.Disposition == "restored" || settled.Disposition == "removed" || settled.Disposition == "not-applied" {
			// 未终结（或已补偿/撤回终结）的句柄：材料保留，责任不丢。
			outcome.Disposition = CompensationSkipped
			outcome.Message = "operation not settled as committed; materials retained"
			out = append(out, outcome)
			continue
		}
		rcpt := &CommitReceipt{OpID: h.opID, BackupRel: h.backupRel, StagingRel: h.stagingRel, DisplacedRel: h.displacedRel}
		c.cleanupMaterial(h.target, rcpt, stageCleanup)
		_ = c.state.deleteIntent(h.opID)
		if err := c.state.mutate(func(st *StateFile) {
			if st.SettledOperations != nil {
				delete(st.SettledOperations, h.opID)
			}
		}); err != nil {
			logWarn(c.log, "wslsupply", "释放时墓碑清理失败（下次重启收敛）", h.opID)
		}
		outcome.Disposition = CompensationConfirmed
		out = append(out, outcome)
	}
	return out
}

// setLeaseActive 维护本进程活跃撤销租用表。
func (c *Coordinator) setLeaseActive(opID string, active bool) {
	c.leaseMu.Lock()
	defer c.leaseMu.Unlock()
	if active {
		c.activeLeases[opID] = true
	} else {
		delete(c.activeLeases, opID)
	}
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
