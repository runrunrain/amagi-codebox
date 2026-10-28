package claudeaccounts

import (
	"context"
)

// ReconcileAction 是恢复决策表（design §4.5）的动作结论。
type ReconcileAction string

const (
	// ReconcileNone 无未结 swap 事务。
	ReconcileNone ReconcileAction = "none"
	// ReconcileClearedJournal 全部等于 before：判定尚未应用，清 journal，
	// 源仍活动；目标曾刷新的新快照保留。
	ReconcileClearedJournal ReconcileAction = "cleared-journal"
	// ReconcileCommittedAfter 全部等于 after：完成 active/generation 提交，
	// 不重复写文件。
	ReconcileCommittedAfter ReconcileAction = "committed-after"
	// ReconcileRolledForward 混合但每一处都可分类：静止与锁条件重新成立后
	// roll-forward 到 after，只补未完成投影。
	ReconcileRolledForward ReconcileAction = "rolled-forward"
	// ReconcileRollForwardPending 存在混合态但调用方未持有 admission：
	// 需要带 gate 重新执行（facade action=complete-target 语义）。
	ReconcileRollForwardPending ReconcileAction = "roll-forward-pending"
	// ReconcileNeedsReconcile 任一投影既非 before 也非 after：停止写入，
	// 保全当前值和已有 journal。
	ReconcileNeedsReconcile ReconcileAction = "needs-reconcile"
	// ReconcileBlocked Keychain 缺失/拒绝访问、JSON 坏、文件缺失：
	// 要求修复访问或重新登录，不创建空 JSON 覆盖。
	ReconcileBlocked ReconcileAction = "blocked"
)

// ReconcileOutcome 是一次恢复检查的结论（credential-free）。
type ReconcileOutcome struct {
	Action     ReconcileAction `json:"action"`
	TxnID      string          `json:"txnId,omitempty"`
	ActiveID   string          `json:"activeId,omitempty"`
	Generation uint64          `json:"generation,omitempty"`
	ReasonCode string          `json:"reasonCode,omitempty"`
}

// projectionClass 是单个投影相对 journal 的分类。
type projectionClass string

const (
	classBefore  projectionClass = "before"
	classAfter   projectionClass = "after"
	classUnknown projectionClass = "unknown"
)

// classifyProjection 比较原生当前值与 journal 前后值（design §4.5；语义
// 比较，池往返 compact 化与原生格式差异不算变化）。
func classifyProjection(current *NativeProjections, journal *SwapJournal, key ProjectionKey) projectionClass {
	before := journal.Before.Projection(key)
	after := journal.After.Projection(key)
	switch key {
	case ProjectionCCKeychain:
		switch {
		case equalJSON(current.ClaudeOAuth, before):
			return classBefore
		case equalJSON(current.ClaudeOAuth, after):
			return classAfter
		}
	case ProjectionClaudeJSON:
		cb := current.ClaudeIdentity
		if cb != nil && journal.Before.ClaudeIdentity != nil &&
			cb.UserID == journal.Before.ClaudeIdentity.UserID &&
			equalJSON(cb.OauthAccount, journal.Before.ClaudeIdentity.OauthAccount) {
			return classBefore
		}
		if cb != nil && journal.After.ClaudeIdentity != nil &&
			cb.UserID == journal.After.ClaudeIdentity.UserID &&
			equalJSON(cb.OauthAccount, journal.After.ClaudeIdentity.OauthAccount) {
			return classAfter
		}
	case ProjectionPiAuth:
		switch {
		case equalJSON(current.PiAnthropic, before):
			return classBefore
		case equalJSON(current.PiAnthropic, after):
			return classAfter
		}
	}
	return classUnknown
}

// ReconcileExistingTransaction 是外部恢复入口（design §7.2 action=inspect/
// complete-target 的领域支撑）。不带 gate 时混合态返回 roll-forward-pending。
func (s *Service) ReconcileExistingTransaction(ctx context.Context) (ReconcileOutcome, error) {
	release, err := s.withLocks(ctx, "reconcile")
	if err != nil {
		return ReconcileOutcome{}, err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	return s.reconcileLocked(ctx, pool, nil)
}

// reconcileLocked 按恢复决策表处理未结 swap 事务。gateRelease 非 nil 表示
// 调用方已持有共享凭据 admission（roll-forward 允许写原生槽位）。
func (s *Service) reconcileLocked(ctx context.Context, pool *AccountPool, gateRelease func()) (ReconcileOutcome, error) {
	if pool == nil || pool.Transaction == nil {
		return ReconcileOutcome{Action: ReconcileNone, ActiveID: activeIDOf(pool), Generation: generationOf(pool)}, nil
	}
	if pool.Transaction.Kind != TransactionSwap || pool.Transaction.Swap == nil {
		// enrollment 事务不自动处理：由 CaptureEnrolledAccount / CancelEnrollment 走完。
		return ReconcileOutcome{Action: ReconcileNone, ActiveID: activeIDOf(pool), Generation: generationOf(pool)}, nil
	}
	journal := pool.Transaction.Swap
	current, err := s.readStableAll(ctx)
	if err != nil {
		// blocked：不创建空 JSON 覆盖；要求修复访问或重新登录（design §4.5）。
		return ReconcileOutcome{Action: ReconcileBlocked, TxnID: journal.TxnID, ReasonCode: string(ReasonOf(err))}, err
	}

	classes := map[ProjectionKey]projectionClass{}
	hasUnknown, hasBefore, hasAfter := false, false, false
	for _, key := range AllProjections {
		c := classifyProjection(current, journal, key)
		classes[key] = c
		switch c {
		case classUnknown:
			hasUnknown = true
		case classBefore:
			hasBefore = true
		case classAfter:
			hasAfter = true
		}
	}

	switch {
	case hasUnknown:
		// 停止写入，保全当前值和已有 journal（design §4.5）。
		return ReconcileOutcome{Action: ReconcileNeedsReconcile, TxnID: journal.TxnID, ReasonCode: string(ReasonNeedsReconcile)},
			NewErr(ReasonNeedsReconcile, "存在无法按 before/after 分类的投影，需人工恢复")
	case hasBefore && !hasAfter:
		// 全部等于 before：尚未应用，清 journal；目标刷新快照保留在池中。
		pool.Transaction = nil
		pool.Revision++
		if err := s.savePoolLocked(ctx, pool); err != nil {
			return ReconcileOutcome{Action: ReconcileBlocked, TxnID: journal.TxnID, ReasonCode: string(ReasonOf(err))}, err
		}
		s.logStep("reconcile-cleared", journal.TxnID, journal.SourceID)
		s.emitEvent(Event{Kind: EventReconcileCompleted, TxnID: journal.TxnID, AccountID: journal.SourceID})
		return ReconcileOutcome{Action: ReconcileClearedJournal, TxnID: journal.TxnID, ActiveID: journal.SourceID, Generation: journal.FromGeneration}, nil
	case hasAfter && !hasBefore:
		// 全部等于 after：完成提交，不重复写文件。
		if err := s.commitAfterOnlyLocked(ctx, pool, journal); err != nil {
			return ReconcileOutcome{Action: ReconcileBlocked, TxnID: journal.TxnID, ReasonCode: string(ReasonOf(err))}, err
		}
		return ReconcileOutcome{Action: ReconcileCommittedAfter, TxnID: journal.TxnID, ActiveID: journal.TargetID, Generation: journal.FromGeneration + 1}, nil
	default:
		// 混合：每一处都能确认为 before 或 after。
		if gateRelease == nil {
			return ReconcileOutcome{Action: ReconcileRollForwardPending, TxnID: journal.TxnID, ActiveID: journal.SourceID, Generation: journal.FromGeneration}, nil
		}
		if err := s.checkQuiescence(ctx); err != nil {
			return ReconcileOutcome{Action: ReconcileRollForwardPending, TxnID: journal.TxnID, ReasonCode: string(ReasonOf(err))}, err
		}
		pending := []ProjectionKey{}
		for _, key := range AllProjections {
			if classes[key] == classBefore {
				pending = append(pending, key)
			}
		}
		if err := s.applyAndCommitLocked(ctx, pool, journal, SwapReasonRecovered, 0, pending); err != nil {
			return ReconcileOutcome{Action: ReconcileNeedsReconcile, TxnID: journal.TxnID, ReasonCode: string(ReasonOf(err))}, err
		}
		return ReconcileOutcome{Action: ReconcileRolledForward, TxnID: journal.TxnID, ActiveID: journal.TargetID, Generation: journal.FromGeneration + 1}, nil
	}
}

// commitAfterOnlyLocked 完成全部 after 已就位的提交（不写文件）。
func (s *Service) commitAfterOnlyLocked(ctx context.Context, pool *AccountPool, journal *SwapJournal) error {
	pool.Transaction = nil
	pool.ActiveAccountID = journal.TargetID
	pool.ActiveGeneration = journal.FromGeneration + 1
	pool.LastSwitch = &LastSwitch{
		At:              s.now(),
		SourceAccountID: journal.SourceID,
		TargetAccountID: journal.TargetID,
		Reason:          SwapReasonRecovered,
	}
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return err
	}
	s.logStep("reconcile-committed", journal.TxnID, journal.TargetID)
	s.emitEvent(Event{Kind: EventReconcileCompleted, TxnID: journal.TxnID, AccountID: journal.TargetID, Generation: pool.ActiveGeneration})
	return nil
}

func activeIDOf(pool *AccountPool) string {
	if pool == nil {
		return ""
	}
	return pool.ActiveAccountID
}

func generationOf(pool *AccountPool) uint64 {
	if pool == nil {
		return 0
	}
	return pool.ActiveGeneration
}

// NativeStateReport 是 InspectNativeState 的只读分类报告（零写入，
// design §7.2 action=inspect 语义）。
type NativeStateReport struct {
	HasJournal          bool                  `json:"hasJournal"`
	JournalPhase        SwapPhase             `json:"journalPhase,omitempty"`
	Classifications     map[ProjectionKey]string `json:"classifications,omitempty"`
	MatchesActiveSnapshot bool                `json:"matchesActiveSnapshot"`
	ExternalChangeSuspected bool              `json:"externalChangeSuspected"`
	ActiveID            string                `json:"activeId,omitempty"`
	Generation          uint64                `json:"generation"`
}

// InspectNativeState 零写入地分类当前原生槽位与池/事务的关系。
func (s *Service) InspectNativeState(ctx context.Context) (*NativeStateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return nil, err
	}
	report := &NativeStateReport{ActiveID: activeIDOf(pool), Generation: generationOf(pool)}
	current, err := s.readStableAll(ctx)
	if err != nil {
		return nil, err
	}
	if pool != nil && pool.Transaction != nil && pool.Transaction.Kind == TransactionSwap && pool.Transaction.Swap != nil {
		journal := pool.Transaction.Swap
		report.HasJournal = true
		report.JournalPhase = journal.Phase
		report.Classifications = map[ProjectionKey]string{}
		for _, key := range AllProjections {
			report.Classifications[key] = string(classifyProjection(current, journal, key))
		}
		return report, nil
	}
	if pool != nil && pool.Active() != nil {
		report.MatchesActiveSnapshot = s.nativeMatchesOwned(current, pool.Active().OwnedProjectionsOf())
		// 原生与活动快照不符且无 journal → 外部换号嫌疑（design §4.5 末行），
		// 经显式采用（AdoptCurrentLogin）才更新 active。
		report.ExternalChangeSuspected = !report.MatchesActiveSnapshot
	}
	return report, nil
}

// AdoptCurrentLogin 显式采用当前原生登录为活动账号（design §4.5 末行：
// 在两个 grant 身份验证后，经显式采用才更新 active）。
func (s *Service) AdoptCurrentLogin(ctx context.Context, expectedRevision uint64) (AccountDTO, error) {
	release, err := s.withLocks(ctx, "adopt-current")
	if err != nil {
		return AccountDTO{}, err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return AccountDTO{}, err
	}
	if pool == nil {
		return AccountDTO{}, NewErr(ReasonPoolMissing, "账号池未启用")
	}
	if pool.Transaction != nil {
		return AccountDTO{}, NewErr(ReasonNeedsReconcile, "池存在未结事务，先完成恢复")
	}
	if pool.Revision != expectedRevision {
		return AccountDTO{}, NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	gateRelease, err := s.acquireGate(ctx, "adopt-current")
	if err != nil {
		return AccountDTO{}, err
	}
	defer gateRelease()
	if err := s.checkQuiescence(ctx); err != nil {
		return AccountDTO{}, err
	}
	current, err := s.readStableAll(ctx)
	if err != nil {
		return AccountDTO{}, err
	}
	ccID, _, err := s.fetchPairedIdentity(ctx, current)
	if err != nil {
		return AccountDTO{}, err
	}
	var match *AccountSnapshot
	for _, snap := range pool.Accounts {
		if snap != nil && snap.Identity.AccountUUID == ccID.AccountUUID {
			match = snap
			break
		}
	}
	if match == nil {
		return AccountDTO{}, NewErr(ReasonExternalAccountChange, "原生登录不属于池内任何账号，请走 enrollment 录入")
	}
	if pool.ActiveAccountID != match.ID {
		pool.ActiveAccountID = match.ID
		pool.ActiveGeneration++
		pool.Revision++
		if err := s.savePoolLocked(ctx, pool); err != nil {
			return AccountDTO{}, err
		}
	}
	// 采用后回收当前原生值，使快照与槽位一致。
	if _, _, err := s.harvestLocked(ctx, pool, pool.ActiveAccountID); err != nil {
		return AccountDTO{}, err
	}
	s.logStep("adopt-current", "", match.ID)
	return match.ToDTO(pool.ActiveAccountID), nil
}

// RecoverAtStartup 启动恢复（design §4.5：启动先加载池并完成恢复检查）：
// 1) 未结 RefreshIntent → 对应 grant 标 refresh-uncertain（退出自动候选）；
// 2) 未结 swap 事务 → 在取得 admission 后按恢复决策表处理。
func (s *Service) RecoverAtStartup(ctx context.Context) (ReconcileOutcome, error) {
	release, err := s.withLocks(ctx, "recover-startup")
	if err != nil {
		return ReconcileOutcome{}, err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	if pool == nil {
		return ReconcileOutcome{Action: ReconcileNone}, nil
	}
	if pool.RefreshIntent != nil {
		snap := pool.Account(pool.RefreshIntent.AccountID)
		if snap != nil {
			// 重启发现未结 intent：一律 refresh-uncertain（design §5.1）。
			markGrantHealth(snap, pool.RefreshIntent.GrantKind, HealthRefreshUncertain)
			s.logf("[claudeaccounts] 启动发现未结刷新意图，标记 refresh-uncertain（account=%s kind=%s）",
				pool.RefreshIntent.AccountID, string(pool.RefreshIntent.GrantKind))
		}
		pool.RefreshIntent = nil
		pool.Revision++
		if err := s.savePoolLocked(ctx, pool); err != nil {
			return ReconcileOutcome{}, err
		}
	}
	if pool.Transaction == nil || pool.Transaction.Kind != TransactionSwap {
		return ReconcileOutcome{Action: ReconcileNone, ActiveID: pool.ActiveAccountID, Generation: pool.ActiveGeneration}, nil
	}
	// swap 事务：取得 admission 后恢复；admission 被拒时保留事务并返回
	// pending（等待会话退出后重试）。
	gateRelease, gerr := s.acquireGate(ctx, "recover-startup")
	if gerr != nil {
		return ReconcileOutcome{Action: ReconcileRollForwardPending, TxnID: pool.Transaction.Swap.TxnID, ActiveID: pool.ActiveAccountID, Generation: pool.ActiveGeneration, ReasonCode: string(ReasonOf(gerr))}, nil
	}
	defer gateRelease()
	if qerr := s.checkQuiescence(ctx); qerr != nil {
		return ReconcileOutcome{Action: ReconcileRollForwardPending, TxnID: pool.Transaction.Swap.TxnID, ActiveID: pool.ActiveAccountID, Generation: pool.ActiveGeneration, ReasonCode: string(ReasonOf(qerr))}, nil
	}
	return s.reconcileLocked(ctx, pool, gateRelease)
}
