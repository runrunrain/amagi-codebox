package main

// app_wsl_unified.go — P2-B「统一配置管理」的 App 编排面（Windows 统一配置
// 入口，direction.md §1/§4 S-B + Leader 裁定）。
//
// 一次明确启用（EnableWSLUnifiedSupply）后：
//   - Claude/Codex 键级单元在会话启动 lazy 补漏（失败只告警不阻断）；
//   - 保存推送（doPushWSLSupply）覆盖 claude/codex 已授权单元 + 资源对账；
//   - CodeBox 启动对账（Startup 一行 → scheduleWSLUnifiedStartupReconcile）；
//   - skills / CLI resources 后台扫描完成回调真正触发一次推送（不是启用时
//     调度一次早于扫描结束的空转——SetScanCompletionCallback 保证）。
//
// 选择集语义（Leader 裁定）：每次对账把来源侧显式可导入键物化进选择集
//（ExtendUnitSelectionIfUnified，只增不减、永不写 "*"）；新键在下一次对账
// 自动纳入。旧版本二进制消费的选择集与用户手工勾选完全同形——降级不删
// 托管配置，无需通配键。
//
// 保护边界（不变）：per-generation 授权、unknown 身份拒绝、detached 不再
// 授权、手改冻结、已有同名项保护、敏感字段白名单、shared fail-closed 语义
//（视觉清单闸门仍在 runWSLSharedFilesAtLaunch，本文件不重复实现）。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/wslsupply"
)

// wslUnifiedReconcileLoopState 是统一管理后台对账循环与延期推送标记的进程内
// 状态（P4 修复 F1/F2；App 字段，随 App 实例生命周期）。
type wslUnifiedReconcileLoopState struct {
	mu     sync.Mutex
	cancel context.CancelFunc // nil = 循环未运行
	done   chan struct{}      // 循环退出信号（stop 等待它，保证不泄漏）
	// pushPending：推送被活动会话挡下后保留的待补推标记（F2——活动会话
	// 归零后由循环心跳异步补推，无需用户再保存一次）。
	pushPending bool
}

// 统一管理后台循环的两个节拍（有节制调度；测试注入缩短）。
var (
	// wslUnifiedPendingTickInterval：延期补推检查周期（只查会话状态与标记，
	// 不唤醒停机 distro、不写目标）。
	wslUnifiedPendingTickInterval = 5 * time.Second
	// wslUnifiedRescanInterval：来源重扫周期（有界、只读 Windows 来源目录；
	// 完成回调才触发一次推送）。
	wslUnifiedRescanInterval = 5 * time.Minute
)

// wslUnifiedCoordinator 是统一管理消费的协调器能力切片（nil 语义：运行器
// 不支持 → 全部自动路径退化为现状 pi/shared 行为）。
type wslUnifiedCoordinator interface {
	SetTargetUnified(ctx context.Context, distro string, on bool) error
	UnifiedCached(distro string) bool
	AutoConsentIfUnified(ctx context.Context, distro string, cli wslsupply.CLI, units []wslsupply.UnitID) ([]wslsupply.UnitID, error)
	ExtendUnitSelectionIfUnified(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID, keys []string) (int, error)
	UnitSourceKeys(unit wslsupply.UnitID) ([]string, error)
}

func (a *App) wslUnifiedCoordinator() wslUnifiedCoordinator {
	if a == nil || a.WSLSupply == nil {
		return nil
	}
	coord, _ := a.WSLSupply.(wslUnifiedCoordinator)
	return coord
}

// wslUnifiedCached 报告当前 distro 是否已启用统一管理（能力缺失/未启用均为
// false——自动路径全部退化为现状行为）。
func (a *App) wslUnifiedCached(distro string) bool {
	if coord := a.wslUnifiedCoordinator(); coord != nil {
		return coord.UnifiedCached(distro)
	}
	return false
}

// wslUnifiedFixedUnits 是启用统一管理时一次性授权的固定单元集（direction
// §1.3）：共享视觉清单（密钥跨界，仍走 per-target 同意原语）、Claude MCP/
// 设置白名单、Codex MCP/profile。多模态学习层不需同意（既有启动/推送路径已
// 自动运行）。Pi 的三键级投影已随「WSL 内 Pi 共享 Windows agent 根」退役。
var wslUnifiedFixedUnits = []struct {
	CLI   wslsupply.CLI
	Units []wslsupply.UnitID
}{
	{wslsupply.CLICommon, []wslsupply.UnitID{wslsupply.UnitSharedVisionExport}},
	{wslsupply.CLIClaude, []wslsupply.UnitID{wslsupply.UnitClaudeMCP, wslsupply.UnitClaudeSettings}},
	{wslsupply.CLICodex, []wslsupply.UnitID{wslsupply.UnitCodexConfig}},
}

// wslUnifiedSelectionUnits 是参与「显式键物化」的键级单元（selectLeaf）。
func wslUnifiedSelectionUnits(cli wslsupply.CLI) []wslsupply.UnitID {
	return cliConfigUnits[cli]
}

// EnableWSLUnifiedSupply 执行一次明确启用：登记目标代 Unified 标记 → 对固定
// 单元集补授 per-target 同意（跳过已授权/保留本地）→ 物化当前显式可导入键 →
// 启动 skills/resources 后台扫描（完成回调再触发推送）→ 立即调度一次推送
// （覆盖已完成的扫描与刚授权的单元）。逐单元返回结果，不汇总成单一成功。
func (a *App) EnableWSLUnifiedSupply() ([]WSLConfigUnitOutcome, error) {
	out := []WSLConfigUnitOutcome{}
	if a == nil || a.WSLSupply == nil {
		return out, fmt.Errorf("WSL 配置供给未启用")
	}
	coord := a.wslUnifiedCoordinator()
	if coord == nil {
		return out, fmt.Errorf("当前供给运行器不支持统一管理")
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return out, fmt.Errorf("未找到可用的 WSL 发行版")
	}
	ctx := context.Background()
	if err := coord.SetTargetUnified(ctx, distro, true); err != nil {
		return out, fmt.Errorf("启用统一管理失败：%v", err)
	}
	// 先启动后台对账循环（P4-F1/F2）：即使下方分组授权部分失败，周期重扫
	// 与延期补推也已在运行，未完成分组随自动对账续接。
	a.startWSLUnifiedReconcileLoop()
	var failedGroups []string
	for _, group := range wslUnifiedFixedUnits {
		units := wslSupplyUnitsFiltered(a.WSLSupply, group.Units...)
		if len(units) == 0 {
			continue
		}
		granted, err := coord.AutoConsentIfUnified(ctx, distro, group.CLI, units)
		if err != nil {
			for _, u := range units {
				out = append(out, WSLConfigUnitOutcome{Unit: string(u), Outcome: "failed", Detail: "自动授权失败：" + err.Error()})
			}
			// P4-F3：不回滚已提交的 Unified 标记，也不中断后续分组——成功分组
			// 保持不变，失败分组随下次统一对账幂等续接（reconcileWSLUnified-
			// FixedUnits：AutoConsentIfUnified 跳过已授权/detached，无需用户
			// 再次敏感授权）。
			failedGroups = append(failedGroups, fmt.Sprintf("%s：%v", group.CLI, err))
			continue
		}
		grantedSet := map[wslsupply.UnitID]bool{}
		for _, u := range granted {
			grantedSet[u] = true
		}
		status := a.wslSupplyStatus(distro, group.CLI)
		for _, u := range units {
			switch {
			case grantedSet[u]:
				out = append(out, WSLConfigUnitOutcome{Unit: string(u), Outcome: "applied", Detail: "已授权统一管理"})
			default:
				if entry := wslFindUnitStatus(status, u); entry != nil && entry.Detached {
					out = append(out, WSLConfigUnitOutcome{Unit: string(u), Outcome: "blocked", Detail: "已保留本地（停跟随），跳过自动授权"})
				} else {
					out = append(out, WSLConfigUnitOutcome{Unit: string(u), Outcome: "fresh", Detail: "此前已授权，保持不变"})
				}
			}
		}
	}
	// 显式键物化（当前来源全量可导入键；只增不减，永不写 "*"）。
	for _, group := range wslUnifiedFixedUnits {
		if len(wslUnifiedSelectionUnits(group.CLI)) == 0 {
			continue
		}
		a.materializeWSLUnifiedSelections(ctx, distro, group.CLI)
	}
	// 后台扫描（完成回调 → scheduleWSLSupplyPush 真正等到扫描结束再对账）。
	a.startWSLUnifiedScans()
	// 立即推一轮：覆盖固定单元与已完成的扫描缓存（未完成的由回调补）。
	a.scheduleWSLSupplyPush()
	if len(failedGroups) > 0 {
		return out, fmt.Errorf("统一管理部分授权失败（%s）；成功分组保持不变，失败分组将随自动对账续接", strings.Join(failedGroups, "；"))
	}
	a.Log.Info("wslsupply", "已启用 WSL 统一配置管理（一次授权；此后启动/保存/对账自动同步，新条目随下次对账纳入）", "distro="+distro)
	return out, nil
}

// DisableWSLUnifiedSupply 停止自动纳入新项：目标代 Unified 标记关闭。已授权
// 单元、选择集与既有副本保持不动（撤回单个单元走既有「保留本地」）。
func (a *App) DisableWSLUnifiedSupply() error {
	if a == nil || a.WSLSupply == nil {
		return fmt.Errorf("WSL 配置供给未启用")
	}
	coord := a.wslUnifiedCoordinator()
	if coord == nil {
		return fmt.Errorf("当前供给运行器不支持统一管理")
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return fmt.Errorf("未找到可用的 WSL 发行版")
	}
	if err := coord.SetTargetUnified(context.Background(), distro, false); err != nil {
		return fmt.Errorf("停止统一管理失败：%v", err)
	}
	// P4-F2：停用即收敛后台循环与延期补推标记（防 goroutine 泄漏；未完成
	// 的固定单元续接随之停止——重新启用时恢复）。
	a.stopWSLUnifiedReconcileLoop()
	a.Log.Info("wslsupply", "已停止 WSL 统一配置管理的自动纳入（已授权单元与副本保持不变；撤回单个单元用「保留本地」）", "distro="+distro)
	return nil
}

// startWSLUnifiedScans 单飞启动 skills 与 claude/codex 资源后台扫描（已启动
// 过幂等；完成回调触发统一对账）。扫描不进启动链等待——这里只点火。
func (a *App) startWSLUnifiedScans() {
	if c := a.wslSkillsCoordinator(); c != nil {
		c.StartSkillsScan()
	}
	if c := a.wslCliSupplyCoordinator(); c != nil {
		c.StartCliResourcesScan(wslsupply.CLIClaude)
		c.StartCliResourcesScan(wslsupply.CLICodex)
	}
}

// scheduleWSLUnifiedStartupReconcile 是 CodeBox 启动对账入口（app.go Startup
// 一行接线）：统一管理开启时先点火后台扫描（完成回调会再推一轮），然后推
// 一轮保存后补漏（未启用统一管理时与现状行为一致——只推 pi/shared）。
func (a *App) scheduleWSLUnifiedStartupReconcile() {
	if a == nil || a.WSLSupply == nil {
		return
	}
	distro := a.wslDefaultDistro()
	if distro == "" {
		return
	}
	if a.wslUnifiedCached(distro) {
		a.startWSLUnifiedScans()
		// 已启用统一管理：恢复后台对账循环（P4-F1/F2——来源重扫与延期
		// 补推跨重启续跑）。
		a.startWSLUnifiedReconcileLoop()
	}
	a.scheduleWSLSupplyPush()
}

// ---------------------------------------------------------------------------
// 后台对账循环（P4-F1/F2：来源周期重扫 + 活动会话阻挡后的延期补推）
// ---------------------------------------------------------------------------

// startWSLUnifiedReconcileLoop 单飞启动后台对账循环（已运行则幂等返回）。
// ctx 父级优先取 Wails Startup ctx（应用关闭即取消）；测试环境（未 Startup）
// 用 Background + 显式 stop 收敛。循环 goroutine 经 done 通道 join，stop 返回
// 后不再有任何循环侧调度。
func (a *App) startWSLUnifiedReconcileLoop() {
	if a == nil || a.WSLSupply == nil {
		return
	}
	a.wslUnifiedLoop.mu.Lock()
	if a.wslUnifiedLoop.cancel != nil {
		a.wslUnifiedLoop.mu.Unlock()
		return
	}
	parent := context.Background()
	if a.ctx != nil {
		parent = a.ctx
	}
	loopCtx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	a.wslUnifiedLoop.cancel = cancel
	a.wslUnifiedLoop.done = done
	a.wslUnifiedLoop.mu.Unlock()
	go func() {
		// 自然退出（ctx 取消/换代自退）时按 done 令牌原子清理自身槽：不清
		// 后起新循环的槽，也绝不让旧循环占据启动位（后续同 App 重新 enable
		// 能再起新循环）。stop() 显式停止时槽已被清，令牌不匹配即 no-op。
		defer close(done)
		defer a.wslUnifiedLoopRetire(done)
		a.wslUnifiedReconcileLoop(loopCtx)
	}()
}

// wslUnifiedLoopRetire 按 done 令牌清理循环槽（仅当槽仍属于该循环实例；
// stop 已接管或后起新循环时不动）。
func (a *App) wslUnifiedLoopRetire(done chan struct{}) {
	a.wslUnifiedLoop.mu.Lock()
	if a.wslUnifiedLoop.done == done {
		a.wslUnifiedLoop.cancel = nil
		a.wslUnifiedLoop.done = nil
	}
	a.wslUnifiedLoop.mu.Unlock()
}

// stopWSLUnifiedReconcileLoop 停止后台对账循环并清空延期补推标记（幂等；
// 等待循环真正退出后返回——之后不再产生循环侧推送调度，防泄漏）。
func (a *App) stopWSLUnifiedReconcileLoop() {
	if a == nil {
		return
	}
	a.wslUnifiedLoop.mu.Lock()
	cancel, done := a.wslUnifiedLoop.cancel, a.wslUnifiedLoop.done
	a.wslUnifiedLoop.cancel, a.wslUnifiedLoop.done = nil, nil
	a.wslUnifiedLoop.pushPending = false
	a.wslUnifiedLoop.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// wslUnifiedReconcileLoop 是统一管理的后台对账循环（单实例；有节制调度）：
//   - pending 心跳：被活动会话挡下的推送在会话归零后自动补推（F2——只查
//     会话状态与标记，不唤醒停机 distro、不写目标）；
//   - rescan 心跳：有界重扫技能与 Claude/Codex 资源来源（F1——首次扫描后
//     新增/修改条目随真实对账周期自动跟随，无需高级面板显式检查；来源
//     删除仍按 source-deleted 既有保护，不自动删目标）。
//
// 扫描完成回调（既有机制）→ scheduleWSLSupplyPush → 对账消费快照；推送
// 永不触发重扫，不存在「扫描完成→推送→再扫描」循环。ctx 取消（应用关闭）
// 即退出；统一管理停用（Disable 显式停）或换代（UnifiedCached=false）时
// 心跳返回 false 自行退出。
func (a *App) wslUnifiedReconcileLoop(ctx context.Context) {
	// 节拍在启动时快照（测试可在 Enable 前注入短周期；循环运行期不再读
	// 全局变量，避免与清理还原竞态）。
	pendingEvery, rescanEvery := wslUnifiedPendingTickInterval, wslUnifiedRescanInterval
	pending := time.NewTicker(pendingEvery)
	defer pending.Stop()
	rescan := time.NewTimer(rescanEvery)
	defer rescan.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pending.C:
			if !a.wslUnifiedPendingTick() {
				return
			}
		case <-rescan.C:
			if !a.wslUnifiedRescanTick() {
				return
			}
			rescan.Reset(rescanEvery)
		}
	}
}

// wslUnifiedPendingTick 处理一次延期补推心跳；返回 false 表示循环应退出
// （统一管理已停用/换代失效）。distro 暂不可解析时保持循环但不动作。
func (a *App) wslUnifiedPendingTick() bool {
	distro := a.wslDefaultDistro()
	if distro == "" {
		return true // 目标暂不可解析：保持循环，下个心跳再试
	}
	if !a.wslUnifiedCached(distro) {
		return false
	}
	if !a.wslUnifiedTakePendingPush() {
		return true // 无延期任务，或活动会话仍未归零（标记保留）
	}
	a.scheduleWSLSupplyPush()
	return true
}

// wslUnifiedRescanTick 处理一次来源重扫心跳；返回 false 表示循环应退出。
// 重扫有界且只读 Windows 来源目录；完成回调触发推送时由 doPushWSLSupply
// 的既有守卫（停机不唤醒/活动会话不写）把守。
func (a *App) wslUnifiedRescanTick() bool {
	distro := a.wslDefaultDistro()
	if distro == "" {
		return true
	}
	if !a.wslUnifiedCached(distro) {
		return false
	}
	if c := a.wslSkillsCoordinator(); c != nil {
		c.RescanSkills()
	}
	if c := a.wslCliSupplyCoordinator(); c != nil {
		c.RescanCliResources(wslsupply.CLIClaude)
		c.RescanCliResources(wslsupply.CLICodex)
	}
	return true
}

// wslUnifiedMarkPushPending 记录一次被挡下的推送（doPushWSLSupply 活动会话
// 早退路径调用；F2）。
func (a *App) wslUnifiedMarkPushPending() {
	a.wslUnifiedLoop.mu.Lock()
	a.wslUnifiedLoop.pushPending = true
	a.wslUnifiedLoop.mu.Unlock()
}

// wslUnifiedTakePendingPush 原子消费延期补推标记：无标记或活动会话仍未
// 归零返回 false；否则清标记返回 true（调用方随后调度推送——推送自身的
// 守卫若再次被挡会重新置标记，竞态安全）。
func (a *App) wslUnifiedTakePendingPush() bool {
	a.wslUnifiedLoop.mu.Lock()
	pending := a.wslUnifiedLoop.pushPending
	a.wslUnifiedLoop.mu.Unlock()
	if !pending {
		return false
	}
	if a.hasActiveSessionsForWSLPush() {
		return false
	}
	a.wslUnifiedLoop.mu.Lock()
	a.wslUnifiedLoop.pushPending = false
	a.wslUnifiedLoop.mu.Unlock()
	return true
}

// wslUnifiedOnScanDone 是扫描完成回调（协调器扫描 goroutine 内触发，必须
// 非阻塞）：统一管理开启时调度一次推送，把扫描到的技能/资源纳入对账。
func (a *App) wslUnifiedOnScanDone() {
	if a == nil || a.WSLSupply == nil {
		return
	}
	distro := a.wslDefaultDistro()
	if distro == "" || !a.wslUnifiedCached(distro) {
		return
	}
	a.scheduleWSLSupplyPush()
}

// materializeWSLUnifiedSelections 把该 CLI 键级单元的来源侧显式可导入键并入
// 选择集（统一模式的核心物化；来源读失败只告警——下次对账重试）。
func (a *App) materializeWSLUnifiedSelections(ctx context.Context, distro string, cli wslsupply.CLI) {
	coord := a.wslUnifiedCoordinator()
	if coord == nil {
		return
	}
	for _, unit := range wslUnifiedSelectionUnits(cli) {
		if !wslSupplyHasUnit(a.WSLSupply, unit) {
			continue
		}
		keys, err := coord.UnitSourceKeys(unit)
		if err != nil {
			a.Log.Warn("wslsupply", "统一管理选择集物化跳过（来源不可读，下次对账重试）", fmt.Sprintf("unit=%s: %v", unit, err))
			continue
		}
		if _, err := coord.ExtendUnitSelectionIfUnified(ctx, distro, cli, unit, keys); err != nil {
			a.Log.Warn("wslsupply", "统一管理选择集物化失败（下次对账重试）", fmt.Sprintf("unit=%s: %v", unit, err))
		}
	}
}

// wslSupplyHasUnit 报告运行器是否注册了该单元（能力过滤，与
// wslSupplyUnitsFiltered 同语义）。
func wslSupplyHasUnit(runner wslsupply.Runner, unit wslsupply.UnitID) bool {
	known, ok := runner.(interface{ HasUnit(wslsupply.UnitID) bool })
	if !ok {
		return true
	}
	return known.HasUnit(unit)
}

// wslFindUnitStatus 在状态快照中定位单元登记（未登记返回 nil）。
func wslFindUnitStatus(st wslsupply.TargetStatus, unit wslsupply.UnitID) *wslsupply.UnitStatus {
	for i := range st.Units {
		if st.Units[i].Unit == unit {
			return &st.Units[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// P4-F3：固定单元初始化的幂等续接（统一管理对账内）
// ---------------------------------------------------------------------------

// reconcileWSLUnifiedFixedUnits 在每次统一对账前幂等续接固定单元初始化：
// EnableWSLUnifiedSupply 可能在 SetTargetUnified 已持久化后因某分组
// ResolveTarget/state.mutate 短暂失败而部分返回（Unified=true 但剩余固定
// 单元从未授权，UI 隐藏启用入口）。这里对未就绪分组补 AutoConsentIfUnified
// 与选择集物化——同代已授权与 detached 一律跳过（不复活保留本地、不动
// hash 基线、无需用户再次敏感授权）；写入由本推送后续分支（pi/shared/键级
// 单元）执行。全部就绪的分组零探测成本跳过（读状态缓存，不 spawn）。
func (a *App) reconcileWSLUnifiedFixedUnits(distro string) {
	coord := a.wslUnifiedCoordinator()
	if coord == nil || !a.wslUnifiedCached(distro) {
		return
	}
	for _, group := range wslUnifiedFixedUnits {
		units := wslSupplyUnitsFiltered(a.WSLSupply, group.Units...)
		if len(units) == 0 || a.wslUnifiedFixedUnitsSettled(distro, group.CLI, units) {
			continue
		}
		if _, err := coord.AutoConsentIfUnified(context.Background(), distro, group.CLI, units); err != nil {
			a.Log.Warn("wslsupply", "统一管理固定单元授权续接失败（下次对账重试）", fmt.Sprintf("cli=%s: %v", group.CLI, err))
			continue
		}
		if len(wslUnifiedSelectionUnits(group.CLI)) > 0 {
			a.materializeWSLUnifiedSelections(context.Background(), distro, group.CLI)
		}
	}
}

// wslUnifiedFixedUnitsSettled 报告该固定分组在当前展示代是否已全部处置
// （同代已同意，或已保留本地 detached）；登记缺失或未同意均视为未就绪。
// 只读状态快照，不探测目标。
func (a *App) wslUnifiedFixedUnitsSettled(distro string, cli wslsupply.CLI, units []wslsupply.UnitID) bool {
	status := a.wslSupplyStatus(distro, cli)
	for _, unit := range units {
		entry := wslFindUnitStatus(status, unit)
		if entry == nil || (!entry.Consented && !entry.Detached) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 保存推送增量 + 资源对账（doPushWSLSupply 在 pi/shared 之后调用）
// ---------------------------------------------------------------------------

// pushWSLUnifiedCliConfig 是保存推送的 claude/codex 增量：物化选择集 → 对
// 「同代已同意且非 detached」的键级单元跑一轮 ReasonSave（冻结/未托管/
// 转换受阻键天然保持现状）。未启用统一管理时为 no-op。
func (a *App) pushWSLUnifiedCliConfig(ctx context.Context, distro string) {
	if a == nil || !a.wslUnifiedCached(distro) {
		return
	}
	for _, cli := range []wslsupply.CLI{wslsupply.CLIClaude, wslsupply.CLICodex} {
		units := wslSupplyUnitsFiltered(a.WSLSupply, cliConfigUnits[cli]...)
		if len(units) == 0 {
			continue
		}
		a.materializeWSLUnifiedSelections(ctx, distro, cli)
		runnable := a.wslUnifiedRunnableUnits(distro, cli, units)
		if len(runnable) == 0 {
			continue
		}
		res, err := a.WSLSupply.RunUnits(ctx, distro, cli, wslsupply.ReasonSave, runnable)
		a.logWSLUnifiedRunWarn(res, err, string(cli)+" 保存推送")
	}
}

// reconcileWSLUnifiedResources 是统一管理的资源对账：扫描快照已完成时，对
// 可移植的技能/资源单元自动授权（AutoConsentIfUnified 跳过 detached），再对
// 「已同意且非 detached」的单元跑一轮 ReasonSave。扫描未启动/仍在跑 → 跳过
// （完成回调会再触发推送，不在这里轮询等待）。资源新项在下一次对账纳入，
// 不进会话启动扫描（SA-17）。
func (a *App) reconcileWSLUnifiedResources(distro string) {
	coord := a.wslUnifiedCoordinator()
	if coord == nil || !a.wslUnifiedCached(distro) {
		return
	}
	if c := a.wslSkillsCoordinator(); c != nil {
		snap := c.SkillsSnapshot()
		if snap.Started && !snap.Running && !snap.RootMissing {
			var candidates []wslsupply.UnitID
			for _, m := range snap.Skills {
				if m.NotPortableReason() == "" {
					candidates = append(candidates, wslsupply.SkillUnitID(m.Name))
				}
			}
			a.runWSLUnifiedTreeUnits(distro, wslsupply.CLICommon, candidates)
		}
	}
	if c := a.wslCliSupplyCoordinator(); c != nil {
		for _, cli := range []wslsupply.CLI{wslsupply.CLIClaude, wslsupply.CLICodex} {
			snap := c.CliResourcesSnapshot(cli)
			if !snap.Started || snap.Running || snap.RootMissing {
				continue
			}
			var candidates []wslsupply.UnitID
			for _, m := range snap.Skills {
				if m.NotPortableReason() == "" {
					candidates = append(candidates, wslsupply.ResourceUnitID(cli, m.Name))
				}
			}
			a.runWSLUnifiedTreeUnits(distro, cli, candidates)
		}
	}
}

// runWSLUnifiedTreeUnits 对树单元（技能/资源）候选集执行「自动授权 + 运行」。
func (a *App) runWSLUnifiedTreeUnits(distro string, cli wslsupply.CLI, candidates []wslsupply.UnitID) {
	units := wslSupplyUnitsFiltered(a.WSLSupply, candidates...)
	if len(units) == 0 {
		return
	}
	coord := a.wslUnifiedCoordinator()
	if coord == nil {
		return
	}
	if _, err := coord.AutoConsentIfUnified(context.Background(), distro, cli, units); err != nil {
		a.Log.Warn("wslsupply", "统一管理资源自动授权失败（下次对账重试）", err.Error())
	}
	runnable := a.wslUnifiedRunnableUnits(distro, cli, units)
	if len(runnable) == 0 {
		return
	}
	res, err := a.WSLSupply.RunUnits(context.Background(), distro, cli, wslsupply.ReasonSave, runnable)
	a.logWSLUnifiedRunWarn(res, err, string(cli)+" 资源对账")
}

// wslUnifiedRunnableUnits 从状态快照筛「同代已同意且非 detached」的单元；
// 无授权的单元不进 RunUnits（避免每次推送都写一条 awaiting-consent 状态）。
func (a *App) wslUnifiedRunnableUnits(distro string, cli wslsupply.CLI, units []wslsupply.UnitID) []wslsupply.UnitID {
	status := a.wslSupplyStatus(distro, cli)
	runnable := make([]wslsupply.UnitID, 0, len(units))
	for _, unit := range units {
		if entry := wslFindUnitStatus(status, unit); entry != nil && entry.Consented && !entry.Detached {
			runnable = append(runnable, unit)
		}
	}
	return runnable
}

// logWSLUnifiedRunWarn 统一记录后台运行的非预期结果（预期态——冻结/未托管/
// 待同意/detached/扫描未就绪——状态页可见，不刷错误日志；与
// doPushWSLSupply 的 shared 分支同一过滤口径）。
func (a *App) logWSLUnifiedRunWarn(res wslsupply.RunResult, err error, scope string) {
	if err != nil {
		a.Log.Warn("wslsupply", "统一管理"+scope+"失败（下次对账重试）", err.Error())
		return
	}
	for _, r := range res.Results {
		if r.Outcome != wslsupply.UnitFailed && r.Outcome != wslsupply.UnitBlocked {
			continue
		}
		if r.Detail == "awaiting-consent" || strings.Contains(r.Detail, "frozen") ||
			strings.Contains(r.Detail, "unmanaged-existing") || strings.Contains(r.Detail, "detached") ||
			strings.Contains(r.Detail, "conversion-blocked") || strings.Contains(r.Detail, "scan-not-ready") ||
			strings.Contains(r.Detail, "source-deleted") {
			continue
		}
		a.Log.Warn("wslsupply", "统一管理"+scope+"未完成", fmt.Sprintf("unit=%s detail=%s", r.Unit, r.Detail))
	}
}

// ---------------------------------------------------------------------------
// 会话启动 lazy（Claude/Codex 键级单元；失败只告警不阻断）
// ---------------------------------------------------------------------------

// runWSLCliConfigAtLaunch 在 WSL 会话启动链尾对 claudecode/codex 的键级单元
// 做一次 lazy 补漏：统一管理开启时先物化选择集（Windows 新增条目随下次启动
// 纳入），再对「同代已同意且非 detached」的单元跑一轮 ReasonLaunch。没有
// 授权就不调 RunUnits（不写 awaiting-consent 状态）；结果只告警不阻断——
// 预期态（冻结/未托管/detached）不是启动告警面（可选偏好单元口径一致）。
func runWSLCliConfigAtLaunch(ctx context.Context, runner wslsupply.Runner, distro string, appType string) {
	cli, ok := wslCliForLaunch(appType)
	if !ok || runner == nil || strings.TrimSpace(distro) == "" {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}
	units := wslSupplyUnitsFiltered(runner, cliConfigUnits[cli]...)
	if len(units) == 0 {
		return
	}
	type unifiedRunner = interface {
		UnifiedCached(distro string) bool
		UnitSourceKeys(unit wslsupply.UnitID) ([]string, error)
		ExtendUnitSelectionIfUnified(ctx context.Context, distro string, cli wslsupply.CLI, unit wslsupply.UnitID, keys []string) (int, error)
	}
	if coord, ok := runner.(unifiedRunner); ok && coord.UnifiedCached(distro) {
		for _, unit := range units {
			keys, err := coord.UnitSourceKeys(unit)
			if err != nil {
				continue // 来源不可读：不阻断启动，下次对账重试
			}
			if _, err := coord.ExtendUnitSelectionIfUnified(ctx, distro, cli, unit, keys); err != nil && appLogHook != nil {
				appLogHook("wslsupply", "统一管理启动物化失败（会话继续）", fmt.Sprintf("unit=%s: %v", unit, err))
			}
		}
	}
	runnable := launchRunnableUnits(runner, distro, cli, units)
	if len(runnable) == 0 {
		return
	}
	res, err := runner.RunUnits(ctx, distro, cli, wslsupply.ReasonLaunch, runnable)
	if err != nil {
		if appLogHook != nil {
			appLogHook("wslsupply", "WSL CLI 用户配置启动补漏失败（会话继续，按既有回退语义）", err.Error())
		}
		return
	}
	for _, r := range res.Results {
		if r.Outcome != wslsupply.UnitFailed && r.Outcome != wslsupply.UnitBlocked {
			continue
		}
		if r.Detail == "awaiting-consent" || strings.Contains(r.Detail, "frozen") ||
			strings.Contains(r.Detail, "unmanaged-existing") || strings.Contains(r.Detail, "detached") ||
			strings.Contains(r.Detail, "conversion-blocked") {
			continue // 预期态：状态页可见，不作为启动告警面
		}
		if appLogHook != nil {
			appLogHook("wslsupply", "WSL CLI 用户配置启动补漏未完成（会话继续）", fmt.Sprintf("unit=%s detail=%s", r.Unit, r.Detail))
		}
	}
}

// wslCliForLaunch 把启动 spec 的 appType 映射到统一启动补漏的 CLI 作用域
// （opencode/omp 走各自附加层；pi 共享 Windows agent 根，均不在本面）。
func wslCliForLaunch(appType string) (wslsupply.CLI, bool) {
	switch appType {
	case "claudecode":
		return wslsupply.CLIClaude, true
	case "codex":
		return wslsupply.CLICodex, true
	default:
		return "", false
	}
}

// launchRunnableUnits 是启动路径的「已同意且非 detached」筛选（与
// wslUnifiedRunnableUnits 同语义的包级版本，runner 可能不是 App）。
func launchRunnableUnits(runner wslsupply.Runner, distro string, cli wslsupply.CLI, units []wslsupply.UnitID) []wslsupply.UnitID {
	status := runner.GetStatus(distro, cli)
	runnable := make([]wslsupply.UnitID, 0, len(units))
	for _, unit := range units {
		for i := range status.Units {
			if status.Units[i].Unit == unit && status.Units[i].Consented && !status.Units[i].Detached {
				runnable = append(runnable, unit)
				break
			}
		}
	}
	return runnable
}
