package claudeaccounts

import (
	"context"
	"time"
)

// SwapRequest 是切换请求（design §7.2：reason 固定 manual，前端不可伪造
// automatic；expectedGeneration 用于幂等与并发防护）。
type SwapRequest struct {
	TargetID             string
	Reason               string
	ExpectedGeneration   uint64
	ManualRespectSeconds int
}

// SwapResult 是切换结果。
type SwapResult struct {
	TxnID      string
	Generation uint64
	ActiveID   string
	NoOp       bool
}

// defaultManualRespectSeconds 是手动切换尊重期默认值（design §6.2）。
const defaultManualRespectSeconds = 3600

// Swap 执行安全切换（design §4.4 伪码）。任一步失败进入 reconcile，不自动
// 用 before 整体覆盖。
func (s *Service) Swap(ctx context.Context, req SwapRequest) (res SwapResult, err error) {
	if req.Reason != SwapReasonManual {
		return SwapResult{}, NewErr(ReasonRevisionConflict, "切换 reason 仅允许 %s", SwapReasonManual)
	}
	respect := req.ManualRespectSeconds
	if respect <= 0 {
		respect = defaultManualRespectSeconds
	}
	release, err := s.withLocks(ctx, "swap")
	if err != nil {
		return SwapResult{}, err
	}
	defer release()

	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return SwapResult{}, err
	}
	if pool == nil || pool.ActiveAccountID == "" {
		return SwapResult{}, NewErr(ReasonPoolMissing, "账号池未启用（无活动账号）")
	}
	target := pool.Account(req.TargetID)
	if target == nil {
		return SwapResult{}, NewErr(ReasonRevisionConflict, "目标账号不存在")
	}
	if pool.ActiveGeneration != req.ExpectedGeneration {
		return SwapResult{}, NewErr(ReasonRevisionConflict, "活动代际期望 %d，实际 %d", req.ExpectedGeneration, pool.ActiveGeneration)
	}

	// 同目标且三处确实匹配 → no-op（不能绕过漂移检查，design §4.4）。
	if req.TargetID == pool.ActiveAccountID {
		current, rerr := s.readStableAll(ctx)
		if rerr != nil {
			return SwapResult{}, rerr
		}
		if s.nativeMatchesOwned(current, pool.Active().OwnedProjectionsOf()) {
			return SwapResult{TxnID: "", Generation: pool.ActiveGeneration, ActiveID: pool.ActiveAccountID, NoOp: true}, nil
		}
		// 不匹配：存在漂移，继续走完整流程（内部 harvest 完成身份校验）。
	}

	// 取得共享凭据 mutation admission（design §6.3：凭据 admission）。
	gateRelease, err := s.acquireGate(ctx, "swap")
	if err != nil {
		return SwapResult{}, err
	}
	defer gateRelease()
	if err := s.checkQuiescence(ctx); err != nil {
		return SwapResult{}, err
	}

	// reconcileExistingTransaction 必须先完成（design §4.4）。
	if _, rerr := s.reconcileLocked(ctx, pool, gateRelease); rerr != nil {
		return SwapResult{}, rerr
	}
	if pool.ActiveAccountID == "" {
		return SwapResult{}, NewErr(ReasonNeedsReconcile, "事务恢复后池处于异常状态")
	}

	journalPersisted := false
	defer func() {
		if err == nil || !journalPersisted {
			return
		}
		// 任一步失败：进入 reconcile（gate 仍持有，混合态默认 roll-forward；
		// 不自动用 before 整体覆盖，design §4.4/§4.5）。
		fresh, lerr := s.loadPoolLocked(ctx)
		if lerr != nil {
			s.logf("[claudeaccounts] swap 失败后加载池失败：原因码=%s", ReasonOf(lerr))
			return
		}
		if outcome, rerr := s.reconcileLocked(ctx, fresh, gateRelease); rerr != nil {
			s.logf("[claudeaccounts] swap 失败后恢复未完成：%s（%s）", outcome.Action, ReasonOf(rerr))
		}
	}()

	// source = harvest(activeId)：换出前读取活动槽位最新值（design §1.3-3）。
	sourceOwned, sourceRevs, herr := s.harvestLocked(ctx, pool, pool.ActiveAccountID)
	if herr != nil {
		return SwapResult{}, herr
	}

	// 准备目标：独占刷新过期的 standby grants；验证目标双端身份。
	if terr := s.ensureTargetReadyLocked(ctx, pool, req.TargetID); terr != nil {
		return SwapResult{}, terr
	}

	// 重新读取 source，若有刷新则重新 harvest；若变成别的账号则拒绝
	// （design §4.4）。
	current2, rerr := s.readStableAll(ctx)
	if rerr != nil {
		return SwapResult{}, rerr
	}
	if !sameRevisions(current2.Revisions, sourceRevs) {
		_, sourceRevs, herr = s.harvestLocked(ctx, pool, pool.ActiveAccountID)
		if herr != nil {
			return SwapResult{}, herr
		}
		current2, rerr = s.readStableAll(ctx)
		if rerr != nil {
			return SwapResult{}, rerr
		}
	}

	after := pool.Account(req.TargetID).OwnedProjectionsOf()
	if len(after.ClaudeOAuth) == 0 || after.ClaudeIdentity == nil || len(after.PiAnthropic) == 0 {
		return SwapResult{}, NewErr(ReasonUnsupportedCredentialSchema, "目标账号快照缺少完整双端投影")
	}

	txnID, err := newRandomID()
	if err != nil {
		return SwapResult{}, err
	}
	journal := &SwapJournal{
		TxnID:            txnID,
		SourceID:         pool.ActiveAccountID,
		TargetID:         req.TargetID,
		FromGeneration:   pool.ActiveGeneration,
		Phase:            SwapPrepared,
		Before:           sourceOwned,
		After:            after,
		ExpectedRevisions: current2.Revisions,
		StartedAt:        s.now(),
	}
	pool.Transaction = &PoolTransaction{Kind: TransactionSwap, Swap: journal}
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return SwapResult{}, err
	}
	journalPersisted = true
	s.logStep("swap-prepared", txnID, req.TargetID)

	if cerr := s.applyAndCommitLocked(ctx, pool, journal, req.Reason, respect, AllProjections); cerr != nil {
		return SwapResult{}, cerr
	}
	return SwapResult{TxnID: txnID, Generation: pool.ActiveGeneration, ActiveID: pool.ActiveAccountID}, nil
}

// applyAndCommitLocked 执行受管投影写入序列、最终验证与池提交（design
// §4.4 后半段）。keys 指定要写的投影（roll-forward 只补未完成的）。
func (s *Service) applyAndCommitLocked(ctx context.Context, pool *AccountPool, journal *SwapJournal, reason string, respectSeconds int, keys []ProjectionKey) error {
	// 获取可用的原生短写锁（Pi proper-lockfile 兼容锁，design §4.3）。
	piLock, err := AcquireProperLock(s.piAuthPath, s.now())
	if err != nil {
		return err
	}
	defer func() {
		if rerr := piLock.Release(); rerr != nil {
			s.logf("[claudeaccounts] 原生写锁释放异常（原因码=%s）", ReasonOf(rerr))
		}
	}()

	for _, key := range keys {
		if err := ctxShort(ctx); err != nil {
			return err
		}
		if err := s.applyProjectionLocked(ctx, pool, journal, key); err != nil {
			return err
		}
	}

	// 再读三处，验证 after 投影一致、非拥有字段未损坏（design §4.4）。
	final, err := s.readStableAll(ctx)
	if err != nil {
		return err
	}
	if !s.nativeMatchesOwned(final, journal.After) {
		return NewErr(ReasonNeedsReconcile, "最终回读与目标投影不符")
	}

	// PoolStore.Save：active=target, generation++, lastSwitch,
	// manualRespectUntil(仅手动成功)，清除 journal。
	pool.Transaction = nil
	pool.ActiveAccountID = journal.TargetID
	pool.ActiveGeneration = journal.FromGeneration + 1
	pool.LastSwitch = &LastSwitch{
		At:              s.now(),
		SourceAccountID: journal.SourceID,
		TargetAccountID: journal.TargetID,
		Reason:          reason,
	}
	if reason == SwapReasonManual {
		until := s.now().Add(time.Duration(respectSeconds) * time.Second)
		pool.ManualRespectUntil = &until
	}
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return err
	}
	s.logStep("swap-committed", journal.TxnID, journal.TargetID)
	s.emitEvent(Event{Kind: EventSwapCompleted, TxnID: journal.TxnID, AccountID: journal.TargetID, Generation: pool.ActiveGeneration})
	return nil
}

// applyProjectionLocked 写入单个投影并持久化进度（design §4.4：每个写入点
// 持久化进度）。
func (s *Service) applyProjectionLocked(ctx context.Context, pool *AccountPool, journal *SwapJournal, key ProjectionKey) error {
	switch key {
	case ProjectionCCKeychain:
		rev, err := s.native.PatchClaudeKeychainOAuth(ctx, journal.After.ClaudeOAuth)
		if err != nil {
			return err
		}
		journal.ExpectedRevisions.ClaudeKeychain = rev
		journal.Phase = SwapPatchingCC
	case ProjectionClaudeJSON:
		rev, err := s.native.PatchClaudeIdentity(ctx, *journal.After.ClaudeIdentity)
		if err != nil {
			return err
		}
		journal.ExpectedRevisions.ClaudeJSON = rev
		journal.Phase = SwapPatchingCJ
	case ProjectionPiAuth:
		rev, err := s.native.PatchPiAnthropic(ctx, journal.After.PiAnthropic)
		if err != nil {
			return err
		}
		journal.ExpectedRevisions.PiAuth = rev
		journal.Phase = SwapPatchingPA
	default:
		return NewErr(ReasonNeedsReconcile, "未知投影 %s", string(key))
	}
	journal.Applied = append(journal.Applied, key)
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return err
	}
	return nil
}

// nativeMatchesOwned 报告三处受管投影是否与 owned 值一致（语义比较，
// 池 envelope 往返 compact 化与原生格式差异不算变化）。
func (s *Service) nativeMatchesOwned(native *NativeProjections, owned OwnedProjections) bool {
	if native == nil {
		return false
	}
	ccOK := equalJSON(native.ClaudeOAuth, owned.ClaudeOAuth)
	if owned.ClaudeIdentity == nil {
		if native.ClaudeIdentity != nil {
			return false
		}
	} else if native.ClaudeIdentity == nil ||
		native.ClaudeIdentity.UserID != owned.ClaudeIdentity.UserID ||
		!equalJSON(native.ClaudeIdentity.OauthAccount, owned.ClaudeIdentity.OauthAccount) {
		return false
	}
	return ccOK && equalJSON(native.PiAnthropic, owned.PiAnthropic)
}