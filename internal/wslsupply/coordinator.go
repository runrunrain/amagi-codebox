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
}

// NewCoordinator 创建协调器。configDir 是 CodeBox 配置目录（状态清单落点）；
// probes 为平台探测面（测试注入假实现）。
func NewCoordinator(configDir string, log Logger, probes Probes) *Coordinator {
	if probes.Identity == nil || probes.ToUNC == nil || probes.Chmod == nil {
		probes = DefaultProbes()
	}
	return &Coordinator{
		log:         log,
		probes:      probes,
		state:       newStateStore(configDir),
		projections: map[UnitID]ProjectionAdapter{},
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
	linuxRoot := strings.TrimRight(ident.Home, "/") + "/" + rel
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

// knownUnit 报告单元是否已注册（A2 或投影接缝）。unknown 单元拒绝（design §七）。
func (c *Coordinator) knownUnit(unit UnitID) bool {
	if unit == UnitPiA2Auth {
		return true
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
		switch unit {
		case UnitPiA2Auth:
			plan.Units = append(plan.Units, PlannedUnit{
				Unit: unit, Policy: PolicyA2Auth, Action: ActionSyncA2,
			})
		default:
			adapter := c.projections[unit]
			entry, _ := state.entry(unit)
			planned, err := adapter.Plan(req, snap, &entry)
			if err != nil {
				return nil, fmt.Errorf("plan unit %s: %w", string(unit), err)
			}
			planned.Unit = unit
			planned.Policy = PolicyTrackedProjection
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
					Unit: unit.Unit, Policy: PolicyTrackedProjection, Outcome: UnitFresh, Detail: "no-change",
				})
			case ActionBlocked:
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: PolicyTrackedProjection, Outcome: UnitBlocked,
					Detail: unit.BlockedReason,
				})
			case ActionWriteProjection:
				result.Results = append(result.Results, c.applyProjection(ctx, current, unit, adapter))
			default:
				result.Results = append(result.Results, UnitResult{
					Unit: unit.Unit, Policy: PolicyTrackedProjection, Outcome: UnitFailed,
					Detail: "planned-action-mismatch",
					Err:    fmt.Errorf("unsupported projection action: %d", unit.Action),
				})
			}
		}
	}
	return result, nil
}

// applyProjection 执行一个投影单元：先复核当前目标仍等于计划预期值（CAS
// 前置），再经提交原语写入并登记。候选正文只在本次调用内存中存在。
func (c *Coordinator) applyProjection(ctx context.Context, target TargetRef, planned PlannedUnit, adapter ProjectionAdapter) UnitResult {
	candidate, transformVersion, err := adapter.Candidate(ctx, target)
	if err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "candidate-unavailable", Err: err}
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
	undo, err := c.submitFile(target, planned.RelPath, candidate)
	if err != nil {
		return UnitResult{Unit: planned.Unit, Policy: planned.Policy, Outcome: UnitFailed,
			Detail: "write-failed", Err: err}
	}
	undo.unit = planned.Unit
	c.recordProjectionState(target, planned.Unit, hexHash(candidate), transformVersion)
	return UnitResult{Unit: planned.Unit, Policy: PolicyTrackedProjection, Outcome: UnitApplied,
		Detail: "written", Undo: undo}
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

// recordProjectionState 更新投影单元登记（普通单元允许记录 hash，仅私有落盘，
// 不经状态 API 返回）。
func (c *Coordinator) recordProjectionState(target TargetRef, unit UnitID, sourceHash, transformVersion string) {
	err := c.state.mutate(func(st *StateFile) {
		st.upsertEntry(StateEntry{
			Unit: unit, Policy: PolicyTrackedProjection, Generation: target.Generation,
			SourceHash: sourceHash, LastWrittenHash: sourceHash, TransformVersion: transformVersion,
			LastSuccess: time.Now(), LastOutcome: "written",
		}, target.Generation)
		st.Distro = target.Distro
	})
	if err != nil {
		logWarn(c.log, "wslsupply", "投影单元状态登记失败（内容已提交，下次视为待核实）", err.Error())
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
	if !st.LastInspectedAt.IsZero() {
		status.Checked = true
		status.CheckedAt = st.LastInspectedAt
	}
	for _, entry := range st.Units {
		status.Units = append(status.Units, UnitStatus{
			Unit: entry.Unit, Policy: entry.Policy, LastOutcome: entry.LastOutcome,
			LastSuccess: entry.LastSuccess, LastErrorCode: entry.LastErrorCode,
			TransformVersion: entry.TransformVersion, Frozen: entry.Frozen,
			FrozenReason: entry.FrozenReason,
		})
	}
	return status
}

// ---------------------------------------------------------------------------
// 提交原语（design §七「状态落点与提交」全段）
// ---------------------------------------------------------------------------

// submitFile 经目标内私有 staging 提交一个文件：唯一临时名（os.CreateTemp，
// 并发 Apply 不撞名）→ 权限先收紧（0600）再 rename → distro 内 chmod 600 补偿
// （9P 不落 POSIX 位；失败即清理落盘内容，fail closed）→ 读回校验。字节走 UNC，
// 不进 wsl.exe 命令行。返回可条件撤销记录（普通文件写入专用；A2 不走本原语）。
func (c *Coordinator) submitFile(target TargetRef, relPath string, content []byte) (*CompensationHandle, error) {
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
		// fail closed：宽松权限明文不留盘（对齐 A2/WriteWSL*AgentConfig 契约）。
		_ = os.Remove(uncPath)
		return nil, fmt.Errorf("chmod 600 supply file in distro: %w", err)
	}
	got, err := os.ReadFile(uncPath)
	if err != nil || hexHash(got) != hexHash(content) {
		return nil, fmt.Errorf("verify supply file after commit: read-back mismatch (err=%v)", err)
	}
	return newCompensationHandle(target, relPath, content, prev, prevExisted), nil
}

// Compensate 条件撤销普通文件写入：仅当目标当前 hash 仍等于本次写入值才恢复
// 原内容/删除新增文件；目标已被外部修改则保留外部内容（CAS，design §七）。
// A2 凭据刷新是持久认证状态，永不参与补偿。
func (c *Coordinator) Compensate(ctx context.Context, handles ...*CompensationHandle) []CompensationOutcome {
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
	if _, err := c.submitFile(h.target, h.relPath, h.prev); err != nil {
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
