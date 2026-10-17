package wslsupply

// reconcile.go — ReconcilePending（攻坚契约 PR05）：按明确 target、固定已登记
// 路径运行；重建 Coordinator（无旧 Undo）时能识别 intent/settled/目标并处置。
// 只处理请求目标的登记路径，不唤醒其它 distro；GetStatus 仅展示。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReconcileDisposition 是单个 intent 的处置结论。
type ReconcileDisposition string

const (
	ReconcileNotApplied    ReconcileDisposition = "not-applied"    // T==before：核实后终结
	ReconcileCommitted     ReconcileDisposition = "committed"      // T==after：核实后补登记（基准字段）
	ReconcileUnconfirmed   ReconcileDisposition = "commit-unconfirmed" // T 不可判：留 pending
	ReconcileCleanupResent ReconcileDisposition = "cleanup-residual"   // settled 在 intent 仍在：清材料
	ReconcileRemoveDone    ReconcileDisposition = "removed"        // remove intent 且 T 已 absent
	ReconcileRemovePending ReconcileDisposition = "withdrawal-unconfirmed"
	ReconcileConflicted    ReconcileDisposition = "freeze-conflict" // T 后改：不赋覆盖权
	ReconcileLeased        ReconcileDisposition = "leased"         // 本进程活跃租用：跳过清理
	ReconcileSaveFailed    ReconcileDisposition = "settle-save-failed"
	ReconcileSkipped       ReconcileDisposition = "skipped"        // 无法处置（保留全部材料）
)

// ReconcileReport 是一次 reconcile 的结论摘要。
type ReconcileReport struct {
	TargetKey   string
	Dispositions []struct {
		OpID       string
		Kind       OpKind
		Unit       UnitID
		Disposition ReconcileDisposition
		Detail     string
	}
	// Blocked 表示存在未决操作：该目标的新写/敏感消费需要先解决。
	Blocked bool
	Reason  string
	OpID    string
}

// pathReconcileResult 是同路径预检的简化结果。
type pathReconcileResult struct {
	blocked bool
	opID    string
	reason  string
}

// ReconcilePending 处理请求目标的全部登记路径（调用方持有 applyMu；RunUnits
// 在 Plan 前调用）。返回处置摘要；存储不可读时如实报告 blocked（停止新写）。
func (c *Coordinator) ReconcilePending(ctx context.Context, target TargetRef) ReconcileReport {
	targetKey := targetKeyOf(target)
	report := ReconcileReport{TargetKey: targetKey}
	intents, err := c.state.loadIntents()
	if err != nil {
		report.Blocked = true
		report.Reason = "pending-store-unreadable"
		return report
	}
	ids := make([]string, 0, len(intents))
	for id := range intents {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		op := intents[id]
		if op.TargetKey != targetKey {
			continue
		}
		if scope := intentScope(op); scope != "" && scope != target.CLI {
			// 键位碰撞/旧格式 intent：不得拿当前调用者的目标根解释其它作用域的
			// RelPath（保留待其所属作用域处理；不清未决、不臆断 absent）。
			continue
		}
		disp, detail := c.reconcileIntent(ctx, target, op)
		report.Dispositions = append(report.Dispositions, struct {
			OpID        string
			Kind        OpKind
			Unit        UnitID
			Disposition ReconcileDisposition
			Detail      string
		}{OpID: op.OpID, Kind: op.Kind, Unit: op.Unit, Disposition: disp, Detail: detail})
		switch disp {
		case ReconcileUnconfirmed, ReconcileRemovePending, ReconcileConflicted, ReconcileSaveFailed, ReconcileSkipped:
			if !report.Blocked {
				report.Blocked = true
				report.Reason = string(disp)
				report.OpID = op.OpID
			}
		}
	}
	return report
}

// reconcileTargetPath 是新写前的同路径未决 intent 预检（commitFile 第 0 步）。
func (c *Coordinator) reconcileTargetPath(ctx context.Context, target TargetRef, unit UnitID, relPath string) pathReconcileResult {
	targetKey := targetKeyOf(target)
	intents, err := c.state.loadIntents()
	if err != nil {
		return pathReconcileResult{blocked: true, reason: "pending-store-unreadable"}
	}
	// 健康门先完成本次扫描再决策：损坏材料保留、禁止新写（不靠上一轮缓存
	// 健康值；loadIntents 刚刷新）。
	if h := c.state.Health(); h.Pending == StoreUnreadable || h.Pending == StoreCorrupt {
		return pathReconcileResult{blocked: true, reason: "pending-store-" + string(h.Pending)}
	}
	ids := make([]string, 0, len(intents))
	for id := range intents {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		op := intents[id]
		if op.TargetKey != targetKey || op.RelPath == "" || op.RelPath != relPath {
			continue
		}
		if scope := intentScope(op); scope != "" && scope != target.CLI {
			continue // 其它作用域的未决：不属于本路径，保留
		}
		if _, settled := c.settledOf(op.OpID); settled {
			// 已终结但清理未完：本进程无租用时收尾。
			c.reconcileIntent(ctx, target, op)
			continue
		}
		disp, detail := c.reconcileIntent(ctx, target, op)
		switch disp {
		case ReconcileNotApplied, ReconcileCommitted, ReconcileCleanupResent, ReconcileRemoveDone:
			continue // 已收敛，可以继续新写
		default:
			return pathReconcileResult{blocked: true, opID: op.OpID, reason: string(disp) + ": " + detail}
		}
	}
	return pathReconcileResult{}
}

// settledOf 读取某操作的 settled 记录。
func (c *Coordinator) settledOf(opID string) (SettledOperation, bool) {
	st := c.state.load()
	op, ok := st.SettledOperations[opID]
	return op, ok
}

// leaseActive 报告本进程是否有活跃撤销租用。
func (c *Coordinator) leaseActive(opID string) bool {
	c.leaseMu.Lock()
	defer c.leaseMu.Unlock()
	return c.activeLeases[opID]
}

// reconcileIntent 处理单个 intent（D/T 矩阵）。目录交换按树形态验证（typed
// fingerprint：dir-swap 的 AfterHash/PrevTreeHash 是树 hash；通用文件 ReadFile
// 读目录恒 EISDIR，不得据此判 unreadable 卡死或误清材料）。
func (c *Coordinator) reconcileIntent(ctx context.Context, target TargetRef, op OperationIntent) (ReconcileDisposition, string) {
	if c.leaseActive(op.OpID) {
		return ReconcileLeased, "active undo lease in this process"
	}
	uncPath := absOf(target, op.RelPath)
	if op.Kind == OpKindRemove || op.Kind == OpKindA2Sync || op.Kind == OpKindPluginOp {
		return c.reconcileNonWriteIntent(ctx, target, op, uncPath)
	}
	if op.Kind == OpKindDirSwap {
		return c.reconcileDirIntent(ctx, target, op, uncPath)
	}
	// write / compensate。
	if op.Kind != OpKindWrite && op.Kind != OpKindCompensate {
		return ReconcileSkipped, "unknown kind " + string(op.Kind)
	}
	cur, curErr := os.ReadFile(uncPath)
	if curErr != nil && !errors.Is(curErr, os.ErrNotExist) {
		return ReconcileUnconfirmed, "target unreadable"
	}
	present := curErr == nil
	curHash := ""
	if present {
		curHash = hexHash(cur)
	}
	if settled, ok := c.settledOf(op.OpID); ok {
		// 已完成但清理未完：不重复提交/补偿，保留目标只清材料。
		_ = settled
		c.finishMaterials(target, op)
		return ReconcileCleanupResent, "settled already recorded; materials cleaned"
	}
	switch {
	case present && op.AfterHash != "" && curHash == op.AfterHash:
		// committed-unregistered：核实 after → 补登记基准字段（不伪造授权）。
		if err := c.settleReconciled(op, "committed", func(st *StateFile) {
			c.applyIntentBaseline(st, op, target)
		}); err != nil {
			return ReconcileSaveFailed, err.Error()
		}
		c.recordObservation(op.TargetKey, op.OpID, op.Unit, OpEffectCommitted, OpRegistrationConfirmed, OpRecoveryNone, "committed-unregistered-registered", "reconcile")
		c.finishMaterials(target, op)
		return ReconcileCommitted, "verified target equals intent after-hash; baseline registered"
	case !present && !op.BeforeExists:
		// 首建 intent 但目标缺失：未应用（或已被清理）→ not-applied。
		if err := c.settleReconciled(op, "not-applied", nil); err != nil {
			return ReconcileSaveFailed, err.Error()
		}
		c.finishMaterials(target, op)
		return ReconcileNotApplied, "target absent and intent created nothing"
	case present && op.BeforeHash != "" && curHash == op.BeforeHash:
		// T==before：未应用或已恢复 → not-applied/restored 终结。
		if err := c.settleReconciled(op, "restored", nil); err != nil {
			return ReconcileSaveFailed, err.Error()
		}
		c.recordObservation(op.TargetKey, op.OpID, op.Unit, OpEffectRestored, OpRegistrationConfirmed, OpRecoveryNone, "restored-confirmed", "reconcile")
		c.finishMaterials(target, op)
		return ReconcileNotApplied, "target equals preimage; op not applied"
	case present && (op.BeforeExists && op.DisplacedRel != "") && curHash != op.BeforeHash && curHash != op.AfterHash:
		return ReconcileConflicted, "target changed to unknown content; explicit new preview required"
	default:
		// 目标未知/后改/缺失但曾移走旧实体：不自动重写/删除；保留前像。
		return ReconcileUnconfirmed, "commit state unconfirmed; materials retained"
	}
}

// reconcileDirIntent 按 Kind=dir-swap 的树形态验证处置：目标树 hash ==
// AfterHash → committed-unregistered 补基准；== PrevTreeHash → restored/
// not-applied；目标缺失且 prev 材料在 → no-replace 放回复验；其它 → 冲突/
// 不可判（保留材料；不得猜 absent）。
func (c *Coordinator) reconcileDirIntent(ctx context.Context, target TargetRef, op OperationIntent, uncPath string) (ReconcileDisposition, string) {
	if settled, ok := c.settledOf(op.OpID); ok {
		_ = settled
		c.finishMaterials(target, op)
		return ReconcileCleanupResent, "settled already recorded; materials cleaned"
	}
	tgt := scanTargetSkillDir(uncPath, c.skillLimits())
	if tgt.Unverifiable {
		return ReconcileUnconfirmed, "target tree unreadable"
	}
	switch {
	case tgt.Exists && op.AfterHash != "" && tgt.TreeHash == op.AfterHash:
		// committed-unregistered：核实 after → 补登记基准字段（不伪造授权）。
		if err := c.settleReconciled(op, "committed", func(st *StateFile) {
			c.applyIntentBaseline(st, op, target)
		}); err != nil {
			return ReconcileSaveFailed, err.Error()
		}
		c.recordObservation(op.TargetKey, op.OpID, op.Unit, OpEffectCommitted, OpRegistrationConfirmed, OpRecoveryNone, "committed-unregistered-registered", "reconcile")
		c.finishMaterials(target, op)
		return ReconcileCommitted, "verified target tree equals intent after-hash; baseline registered"
	case tgt.Exists && op.PrevTreeHash != "" && tgt.TreeHash == op.PrevTreeHash:
		// T==before：未应用或已恢复 → restored 终结。
		if err := c.settleReconciled(op, "restored", nil); err != nil {
			return ReconcileSaveFailed, err.Error()
		}
		c.recordObservation(op.TargetKey, op.OpID, op.Unit, OpEffectRestored, OpRegistrationConfirmed, OpRecoveryNone, "restored-confirmed", "reconcile")
		c.finishMaterials(target, op)
		return ReconcileNotApplied, "target tree equals preimage; op not applied"
	case !tgt.Exists && !op.BeforeExists:
		// 首建 intent 且目标树缺失：未应用（或已被清理）。
		if err := c.settleReconciled(op, "not-applied", nil); err != nil {
			return ReconcileSaveFailed, err.Error()
		}
		c.finishMaterials(target, op)
		return ReconcileNotApplied, "target tree absent and intent created nothing"
	case !tgt.Exists && op.DisplacedRel != "":
		// 目标缺失且本次移走了旧树：no-replace 放回 prev（外部新目标拒绝保留
		// 两处）；放回后按树 hash 复验。
		prevPath := absOf(target, op.DisplacedRel)
		if _, perr := os.Lstat(prevPath); perr != nil {
			return ReconcileUnconfirmed, "target tree absent and prev material missing; not guessing"
		}
		if rbErr := publishNoReplaceFn(prevPath, uncPath); rbErr != nil {
			return ReconcileUnconfirmed, "restore prev (no-replace) failed: " + rbErr.Error()
		}
		pv := scanTargetSkillDir(uncPath, c.skillLimits())
		if pv.Unverifiable || (op.PrevTreeHash != "" && pv.TreeHash != op.PrevTreeHash) {
			return ReconcileUnconfirmed, "prev put back but tree hash mismatch/unverifiable"
		}
		if err := c.settleReconciled(op, "restored", nil); err != nil {
			return ReconcileSaveFailed, err.Error()
		}
		c.recordObservation(op.TargetKey, op.OpID, op.Unit, OpEffectRestored, OpRegistrationConfirmed, OpRecoveryNone, "restored-confirmed", "reconcile")
		c.finishMaterials(target, op)
		return ReconcileNotApplied, "prev tree restored to target"
	default:
		// 树存在但 hash 不匹配 after/before：不自动重写/删除；保留 prev/材料。
		if !tgt.Exists {
			return ReconcileUnconfirmed, "commit state unconfirmed; materials retained"
		}
		return ReconcileConflicted, "target tree changed to unknown content; explicit new preview required"
	}
}

// reconcileNonWriteIntent 处理 remove / a2 / plugin 摘要 intent。
func (c *Coordinator) reconcileNonWriteIntent(ctx context.Context, target TargetRef, op OperationIntent, uncPath string) (ReconcileDisposition, string) {
	if _, ok := c.settledOf(op.OpID); ok {
		c.finishMaterials(target, op)
		return ReconcileCleanupResent, "settled; materials cleaned"
	}
	if op.Kind == OpKindA2Sync || op.Kind == OpKindPluginOp {
		// 摘要型：重启重新真实判定，不按摘要重放/授权；仅清理已终结者。
		return ReconcileSkipped, "summary intent requires a fresh real run; not replayed"
	}
	// remove：确认消费路径状态。
	_, err := os.Lstat(uncPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if serr := c.settleReconciled(op, "removed", nil); serr != nil {
			return ReconcileSaveFailed, serr.Error()
		}
		c.recordObservation(op.TargetKey, op.OpID, op.Unit, OpEffectRemoved, OpRegistrationConfirmed, OpRecoveryNone, "removed-confirmed", "reconcile")
		c.finishMaterials(target, op)
		return ReconcileRemoveDone, "consumption path absent"
	case err != nil:
		return ReconcileUnconfirmed, "consumption path unreadable"
	default:
		return ReconcileRemovePending, "copy still present; withdrawal unconfirmed"
	}
}

// settleReconciled 保存 settled 事实（清理前）；失败保留 pending。
func (c *Coordinator) settleReconciled(op OperationIntent, disposition string, extra func(*StateFile)) error {
	return c.state.mutate(func(st *StateFile) {
		if st.SettledOperations == nil {
			st.SettledOperations = map[string]SettledOperation{}
		}
		related := []string(nil)
		if op.ParentOpID != "" {
			related = append(related, op.ParentOpID)
		}
		st.SettledOperations[op.OpID] = SettledOperation{
			TargetKey: op.TargetKey, Unit: op.Unit, Disposition: disposition,
			RelatedOpIDs: related, SettledAt: time.Now(),
		}
		if extra != nil {
			extra(st)
		}
	})
}

// applyIntentBaseline 从 intent 恢复基准字段（仅基准：hash/transform；不恢复
// consent/selection/detached——契约「PriorEntry/ProposedEntry 不提供授权恢复
// 能力」；主清单损坏时禁止从 intent 臆造全部授权）。
func (c *Coordinator) applyIntentBaseline(st *StateFile, op OperationIntent, target TargetRef) {
	if op.Unit == "" || op.AfterHash == "" {
		return
	}
	prev := st.entryFor(op.Unit, target.Generation)
	// 基准字段 upsert：同代已有登记则仅刷新写入基准；无登记则创建仅含基准
	// 字段的新条目（Consented/Detached/SelectedKeys 一律不臆造——intent 不提
	// 供授权恢复能力）。
	entry := StateEntry{
		Unit: op.Unit, Policy: prev.Policy, Generation: target.Generation,
		LastWrittenHash: op.AfterHash, LastOperationID: op.OpID,
		LastOutcome: "written", LastSuccess: time.Now(),
		SourceHash: prev.SourceHash, TransformVersion: prev.TransformVersion,
		Subs: prev.Subs, SelectedKeys: prev.SelectedKeys,
		Consented: prev.Consented, Detached: prev.Detached,
	}
	st.upsertEntry(entry, target.Generation)
}

// finishMaterials 终结清理：材料 → intent → settled 墓碑。
func (c *Coordinator) finishMaterials(target TargetRef, op OperationIntent) {
	rc := &CommitReceipt{OpID: op.OpID, BackupRel: op.BackupRel, StagingRel: op.StagingRel, DisplacedRel: op.DisplacedRel}
	c.cleanupMaterial(target, rc, stageCleanup)
	if err := c.state.deleteIntent(op.OpID); err != nil {
		logWarn(c.log, "wslsupply", "intent 清理失败（保留可清理历史）", op.OpID)
		return
	}
	// 最后删除 settled 墓碑（墓碑删除失败只留可清理历史，不重新写目标）。
	if err := c.state.mutate(func(st *StateFile) {
		if st.SettledOperations != nil {
			delete(st.SettledOperations, op.OpID)
		}
	}); err != nil {
		logWarn(c.log, "wslsupply", "settled 墓碑清理失败（下次重启收敛）", op.OpID)
	}
}

// ---------------------------------------------------------------------------
// 撤回（remove）原语：保留 displaced、再核验、不覆盖
// ---------------------------------------------------------------------------

// commitRemove 执行 SA-09 撤回（来源可靠 ENOENT 后的消费路径移除）：
// remove intent（含 before/after 指纹与 displaced 名）→ ctx → 目标仍等于
// durable/pending after 才移入 displaced → 复验 ENOENT → settled removed。
// EACCES/I/O 不是 absent；外部后改保留（withdrawal-unconfirmed）。
func (c *Coordinator) commitRemove(ctx context.Context, target TargetRef, unit UnitID, relPath, expectedHash string) *CommitReceipt {
	targetKey := targetKeyOf(target)
	opID := newOpID()
	uncPath := filepath.Join(target.UNCRoot, filepath.FromSlash(relPath))
	rc := &CommitReceipt{
		OpID: opID, TargetKey: targetKey, Unit: unit, RelPath: relPath,
		Kind: OpKindRemove, Effect: OpEffectNone, Registration: OpRegistrationPending, Recovery: OpRecoveryNone,
	}
	if report := c.reconcileTargetPath(ctx, target, unit, relPath); report.blocked {
		rc.Err = fmt.Errorf("pending operation %s unresolved: %s", report.opID, report.reason)
		rc.ErrDetail = "pending-intent-conflict"
		return rc
	}
	before, class := readPreimageClass(uncPath)
	switch class {
	case preimageAbsent:
		rc.Effect = OpEffectRemoved
		rc.Registration = OpRegistrationPending
		if err := c.settleRemove(rc, target, unit, "removed", nil); err != nil {
			rc.Err = fmt.Errorf("absent confirmed but state persist failed (remove intent kept; rerun re-confirms): %w", err)
			rc.ErrDetail = "removed-state-unpersisted"
			_ = c.state.saveIntent(OperationIntent{OpID: opID, Kind: OpKindRemove, TargetKey: targetKey,
				TargetDistro: target.Distro, TargetHome: target.Home, TargetCLI: string(target.CLI), Unit: unit, RelPath: relPath, CreatedAt: time.Now()})
			c.recordObservation(targetKey, opID, unit, OpEffectRemoved, OpRegistrationPending, OpRecoveryNone, rc.ErrDetail, "withdraw")
		}
		return rc
	case preimageUnreadable, preimageUnsupported:
		rc.Err = fmt.Errorf("read withdrawn target: %s", class)
		rc.ErrDetail = "target-unreadable"
		return rc
	}
	rc.BeforeExists = true
	rc.BeforeHash = hexHash(before)
	if expectedHash != "" && rc.BeforeHash != expectedHash {
		// T 后改：不删混合文件。
		rc.Err = errors.New("withdrawn key copy was hand-modified; local edit retained")
		rc.ErrDetail = "withdrawal-unconfirmed-frozen"
		return rc
	}
	if err := ctx.Err(); err != nil {
		rc.Err = err
		rc.ErrDetail = "context-canceled"
		return rc
	}
	displacedRel := relPathOf(target, filepath.Join(filepath.Dir(uncPath), ".amagi-supply-displaced-"+opID))
	if err := c.state.saveIntent(OperationIntent{
		OpID: opID, Kind: OpKindRemove, TargetKey: targetKey, TargetDistro: target.Distro,
		TargetHome: target.Home, TargetCLI: string(target.CLI), Unit: unit, RelPath: relPath, Sensitive: true,
		BeforeExists: true, BeforeHash: rc.BeforeHash, DisplacedRel: displacedRel, CreatedAt: time.Now(),
	}); err != nil {
		rc.Err = fmt.Errorf("save remove intent: %w", err)
		rc.ErrDetail = "intent-unavailable"
		return rc
	}
	if err := c.faults.fail(stageMove); err != nil {
		rc.Effect = OpEffectUnknown
		rc.Err = err
		rc.ErrDetail = "remove-move-unknown"
		c.recordObservation(targetKey, opID, unit, OpEffectUnknown, OpRegistrationPending, OpRecoveryNone, rc.ErrDetail, stageMove)
		return rc
	}
	// 保留 displaced 的移除（不直接删除——前像/隔离物可能含旧秘密，清理面
	// 独立展示与处置）。
	if err := os.Rename(uncPath, absOf(target, displacedRel)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			rc.Effect = OpEffectRemoved
		} else {
			rc.Effect = OpEffectUnknown
			rc.Err = fmt.Errorf("quarantine withdrawn copy: %w", err)
			rc.ErrDetail = "withdrawal-remove-failed"
			c.recordObservation(targetKey, opID, unit, rc.Effect, OpRegistrationPending, OpRecoveryNone, rc.ErrDetail, stageMove)
			return rc
		}
	} else {
		rc.Effect = OpEffectRemoved
	}
	// 复验 absent（EACCES/I/O 不是 absent）。
	if _, serr := os.Lstat(uncPath); serr == nil {
		rc.Effect = OpEffectUnknown
		rc.Err = errors.New("withdrawn copy still present after quarantine")
		rc.ErrDetail = "withdrawal-remove-failed"
		c.recordObservation(targetKey, opID, unit, rc.Effect, OpRegistrationPending, OpRecoveryNone, rc.ErrDetail, stageVerify)
		return rc
	} else if !errors.Is(serr, os.ErrNotExist) {
		rc.Effect = OpEffectUnknown
		rc.Err = fmt.Errorf("verify absent after quarantine: %v", serr)
		rc.ErrDetail = "withdrawal-remove-failed"
		c.recordObservation(targetKey, opID, unit, rc.Effect, OpRegistrationPending, OpRecoveryNone, rc.ErrDetail, stageVerify)
		return rc
	}
	if err := c.settleRemove(rc, target, unit, "removed", func(st *StateFile) {
		clearLegacySensitiveRisk(st, target.Distro, unit)
	}); err != nil {
		rc.Err = fmt.Errorf("copy withdrawn but state persist failed (remove intent kept; rerun re-confirms): %w", err)
		rc.ErrDetail = "removed-state-unpersisted"
		c.recordObservation(targetKey, opID, unit, OpEffectRemoved, OpRegistrationPending, OpRecoveryNone, rc.ErrDetail, stageFinalize)
		return rc
	}
	rc.Registration = OpRegistrationConfirmed
	c.recordObservation(targetKey, opID, unit, OpEffectRemoved, OpRegistrationConfirmed, OpRecoveryNone, "removed", stageFinalize)
	return rc
}

// settleRemove 保存撤回终态登记。
func (c *Coordinator) settleRemove(rc *CommitReceipt, target TargetRef, unit UnitID, disposition string, extra func(*StateFile)) error {
	return c.state.mutate(func(st *StateFile) {
		if st.SettledOperations == nil {
			st.SettledOperations = map[string]SettledOperation{}
		}
		st.SettledOperations[rc.OpID] = SettledOperation{
			TargetKey: rc.TargetKey, Unit: unit, Disposition: disposition, SettledAt: time.Now(),
		}
		if extra != nil {
			extra(st)
		}
	})
}

// PendingSensitiveOperation 报告目标+单元是否存在未决（未 settled）的写/
// remove intent——启动资格与状态面用（真实持久事实，不是 LastOutcome 字符串）。
func (c *Coordinator) PendingSensitiveOperation(distro string, unit UnitID) (string, bool) {
	intents, err := c.state.loadIntents()
	if err != nil {
		return "pending-store-unreadable", true
	}
	st := c.state.load()
	ids := make([]string, 0, len(intents))
	for id := range intents {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		op := intents[id]
		if op.Unit != unit {
			continue
		}
		// 归属：完整身份键或 distro 匹配（unknown 键含 distro）。
		if op.TargetKey != distro && !strings.Contains(op.TargetKey, distro) && op.TargetDistro != distro {
			continue
		}
		if _, settled := st.SettledOperations[op.OpID]; settled {
			continue
		}
		if op.Kind == OpKindWrite || op.Kind == OpKindRemove || op.Kind == OpKindDirSwap {
			return op.OpID, true
		}
	}
	return "", false
}

// StoreHealthSnapshot 返回存储面健康（GetStatus/App 展示）。
func (c *Coordinator) StoreHealthSnapshot() StoreHealth { return c.state.Health() }
