package wslsupply

// unified.go — P2-B「统一配置管理」的协调器原语（Windows 统一配置入口）。
//
// 语义（Leader 裁定，覆盖 direction.md §1.2 的通配键方案）：
//   - 不持久化仅含通配 "*" 的选择集：旧版本二进制会把 "*" 当普通叶名，选择集
//     实际变空，已托管未手改的键走来源删除路径被移除（降级删除托管配置）。
//     本实现改为「每次配置对账时物化显式可导入键到选择集」：不引入通配键、
//     不改 selectionMatchesKey，选择协议与旧版本完全同形。
//   - 同代 TargetRecord.Unified 标记使未来新增键自动纳入：每次对账（启用/
//     会话启动 lazy/保存推送/启动对账）都把来源侧当前可导入键并入
//     SelectedKeys（只增不减，永不写入 "*"）——新键在下一次对账自动跟随。
//   - 保护不变：per-generation 授权（I1）、detached 永不被自动授权覆盖（I2，
//     GrantUnitConsent 重建 entry 不保留 Detached 的陷阱由「跳过已登记单元」
//     规避）、冻结/unmanaged-existing/冲突判定不改（I3）、敏感字段白名单与
//     LocalOnly 不改（I4）、unknown 身份拒绝持久授权（SA-02）。
//   - 扫描完成回调：skills / CLI resources 的后台扫描结束时触发一次通知，
//     App 层据此在对账时机上真正等到「扫描完成」，而不是启用时调度一次
//     早于扫描结束的推送导致资源永远不同步。

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SelectAllMarker 是「跟随全部可导入键」的保留标记。当前实现只把它当防御性
// 保留字（ExtendUnitSelectionIfUnified 拒绝写入）；统一管理走显式键物化，
// 该标记永不持久化到选择集。
const SelectAllMarker = "*"

// SetTargetUnified 设置目标代的「统一管理」授权标记（Enable/Disable 的底层
// 原语）。与 per-target 同意同一安全边界：身份不可验证（unknown 代）的目标
// 不接受持久授权（SA-02），拒绝并说明修复方向。on=false 只停止自动纳入新项，
// 已授权单元、选择集与既有副本全部保持不动（撤回走单元「保留本地」）。
func (c *Coordinator) SetTargetUnified(ctx context.Context, distro string, on bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("set target unified: %w", err)
	}
	target, err := c.ResolveTarget(ctx, distro, CLIPi)
	if err != nil {
		return fmt.Errorf("resolve wsl supply target: %w", err)
	}
	// 身份探测后复查：已取消的请求不提交新的统一管理授权。
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("set target unified canceled: %w", err)
	}
	if target.Generation == GenerationUnknown {
		return fmt.Errorf("无法验证 WSL 目标身份（distro %q 拿不到 machine-id）：统一管理授权需要可验证的目标身份；请在 WSL 内恢复 /etc/machine-id 后重试", distro)
	}
	return c.state.mutate(func(st *StateFile) {
		if st.Targets == nil {
			st.Targets = map[string]TargetRecord{}
		}
		rec := st.Targets[target.Generation]
		rec.Distro = target.Distro
		rec.UID = target.UID
		rec.Home = target.Home
		rec.LegacyGeneration = target.LegacyGeneration
		rec.LastSeen = time.Now()
		if on != rec.Unified {
			rec.Unified = on
			if on {
				rec.UnifiedAt = time.Now()
			} else {
				rec.UnifiedAt = time.Time{}
			}
		}
		st.Targets[target.Generation] = rec
	})
}

// UnifiedCached 报告该 distro 当前展示代是否已启用统一管理（只读状态清单，
// 不探测；换代后新代无标记 → false，需重新启用）。
func (c *Coordinator) UnifiedCached(distro string) bool {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return false
	}
	st := c.state.load()
	_, rec, found := latestTargetRecord(&st, distro)
	return found && rec.Unified
}

// AutoConsentIfUnified 在统一管理已启用时，对尚未登记的单元补授 per-target
// 同意（同一 mutate 内校验 Unified 标记，避免检查-写入窗口内被 Disable）。
// 已 Consented 或已 Detached 的同代单元一律跳过（I2：GrantUnitConsent 重建
// entry 不保留 Detached——对「保留本地」的单元重复授权会静默解除停跟随）；
// 未同意但已有登记（awaiting-consent 等）的单元按 GrantUnitConsent 同代保留
// 规则授权（Subs/SelectedKeys/hash 基线不丢）。未启用统一管理时为 no-op。
// 返回本次实际授予的单元清单。
func (c *Coordinator) AutoConsentIfUnified(ctx context.Context, distro string, cli CLI, units []UnitID) ([]UnitID, error) {
	policies := make(map[UnitID]Policy, len(units))
	for _, unit := range units {
		policy, ok := c.consentPolicyFor(unit)
		if !ok {
			return nil, fmt.Errorf("wsl supply unit %q does not accept consent", string(unit))
		}
		policies[unit] = policy
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("auto consent if unified: %w", err)
	}
	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		return nil, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	// 身份探测后复查：已取消的请求不提交新的授权（PR07 同源约束）。
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("auto consent if unified canceled: %w", err)
	}
	if target.Generation == GenerationUnknown {
		return nil, fmt.Errorf("无法验证 WSL 目标身份（distro %q 拿不到 machine-id）：统一管理授权需要可验证的目标身份", distro)
	}
	granted := []UnitID{}
	err = c.state.mutate(func(st *StateFile) {
		rec := st.Targets[target.Generation]
		if !rec.Unified {
			return
		}
		for _, unit := range units {
			prev := st.entryFor(unit, target.Generation)
			if prev.Generation == target.Generation && (prev.Consented || prev.Detached) {
				continue
			}
			grantEntryIn(st, target, unit, policies[unit])
			granted = append(granted, unit)
		}
	})
	if err != nil {
		return nil, err
	}
	return granted, nil
}

// ExtendUnitSelectionIfUnified 在统一管理已启用且该单元已同意（非 detached）
// 时，把 keys 并入选择集（只增不减；空串与 "*" 通配键绝不写入）。返回实际
// 新增的键数。选择集仍由既有 SetUnitSelection / S4 导入路径原样读写——本
// 原语不改选择协议，只做统一模式下的显式键物化（Leader 裁定的降级安全路径）。
// ctx 贯通身份探测，并在 mutate 前复查：已取消的请求不写选择集。
func (c *Coordinator) ExtendUnitSelectionIfUnified(ctx context.Context, distro string, cli CLI, unit UnitID, keys []string) (int, error) {
	adapter, ok := c.projections[unit]
	if !ok {
		return 0, fmt.Errorf("wsl supply unit %q does not accept key selection", string(unit))
	}
	shapeAware, ok := adapter.(interface{ SelectionAware() bool })
	if !ok || !shapeAware.SelectionAware() {
		return 0, fmt.Errorf("wsl supply unit %q does not accept key selection", string(unit))
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("extend unit selection if unified: %w", err)
	}
	target, err := c.ResolveTarget(ctx, distro, cli)
	if err != nil {
		return 0, fmt.Errorf("resolve wsl supply target: %w", err)
	}
	// 身份探测后复查：已取消的请求不写选择集。
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("extend unit selection if unified canceled: %w", err)
	}
	if target.Generation == GenerationUnknown {
		return 0, fmt.Errorf("无法验证 WSL 目标身份（distro %q），选择集物化未执行", distro)
	}
	added := 0
	err = c.state.mutate(func(st *StateFile) {
		rec, found := st.Targets[target.Generation]
		if !found || !rec.Unified {
			return
		}
		entry := st.entryFor(unit, target.Generation)
		if entry.Generation != target.Generation || entry.Detached || !entry.Consented {
			return
		}
		selected := map[string]bool{}
		for k := range entry.SelectedKeys {
			selected[k] = true
		}
		for _, k := range keys {
			k = strings.TrimSpace(k)
			if k == "" || k == SelectAllMarker {
				continue // 防御：通配键绝不持久化（旧版本降级会误删托管配置）
			}
			if !selected[k] {
				selected[k] = true
				added++
			}
		}
		if added == 0 {
			return
		}
		entry.SelectedKeys = selected
		st.upsertEntry(entry, target.Generation)
	})
	return added, err
}

// UnitSourceKeys 返回一个键级单元来源侧的当前可导入键全集（全路径键，与
// 预览面 KeyPreview.Key 同形）。只读 Windows 侧来源文件，不探测目标、不解析
// 转换事实——供统一模式在选择集物化时计算「应选键」。非键级单元返回错误。
func (c *Coordinator) UnitSourceKeys(unit UnitID) ([]string, error) {
	src, ok := c.projections[unit].(interface {
		importableSourceKeys() ([]string, error)
	})
	if !ok {
		return nil, fmt.Errorf("wsl supply unit %q has no importable source keys", string(unit))
	}
	return src.importableSourceKeys()
}

// consentPolicyFor 返回接受 per-target 同意的单元的策略名（GrantUnitConsent
// 与 AutoConsentIfUnified 共用的可接受性判定：技能/资源单元按 TrackedProjection；
// 投影单元必须 RequiresConsent）。不可接受返回 ok=false。
func (c *Coordinator) consentPolicyFor(unit UnitID) (Policy, bool) {
	if _, isSkill := ParseSkillUnit(unit); isSkill {
		if c.skills == nil {
			return "", false
		}
		return PolicyTrackedProjection, true
	}
	if resCLI, _, isRes := ParseResourceUnit(unit); isRes {
		if c.resourceScope(resCLI) == nil {
			return "", false
		}
		return PolicyTrackedProjection, true
	}
	adapter, ok := c.projections[unit]
	if !ok || !adapter.RequiresConsent() {
		return "", false
	}
	return adapter.Policy(), true
}

// grantEntryIn 在一次 mutate 内为一个单元写入 per-target 同意登记
//（GrantUnitConsent 与 AutoConsentIfUnified 共用）。仅同代保留 hash 基线/
// 键级基准/选择集（F3：跨代不继承——继承会让新 distro 首发布被误判
// local-deletion-frozen）；同代已有登记时 Detached 由调用方先行跳过
//（I2：本函数按「显式同意」语义不携带 Detached）。
func grantEntryIn(st *StateFile, target TargetRef, unit UnitID, policy Policy) {
	prev := st.entryFor(unit, target.Generation)
	entry := StateEntry{
		Unit: unit, Policy: policy, Generation: target.Generation,
		Consented: true, LastOutcome: "consented", LastSuccess: time.Now(),
	}
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
}

// ---------------------------------------------------------------------------
// 扫描完成通知（App 层对账时机依赖：启用后推送不能早于扫描结束）
// ---------------------------------------------------------------------------

// SetScanCompletionCallback 注册「一次后台扫描结束」通知回调（skills 与全部
// CLI resources 作用域；Rescan 重扫完成同样触发）。回调在扫描 goroutine 内、
// 无锁状态下调用——必须非阻塞；App 层只做一次异步推送调度。重复注册以后者
// 为准；nil 清除。回调在触发时刻经 scanDoneNotifier 快照读取，注册后修改
// 也能被已注册作用域看到最新值。
func (c *Coordinator) SetScanCompletionCallback(fn func()) {
	c.scanDoneMu.Lock()
	c.scanDone = fn
	c.scanDoneMu.Unlock()
	notifier := c.scanDoneNotifier()
	if c.skills != nil {
		c.skills.SetScanDoneCallback(notifier)
	}
	for _, scope := range c.resources {
		scope.SetScanDoneCallback(notifier)
	}
}

// scanDoneNotifier 返回触发时快照当前回调的通知闭包。
func (c *Coordinator) scanDoneNotifier() func() {
	return func() {
		c.scanDoneMu.Lock()
		fn := c.scanDone
		c.scanDoneMu.Unlock()
		if fn != nil {
			fn()
		}
	}
}
