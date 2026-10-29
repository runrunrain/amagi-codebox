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
	// Guard（F06）：恢复类调用（restore-source）携带的锁内校验条件——在同一
	// withLocks 区间内、任何写入前比对池 revision 与所选 journal，陈旧命令
	// 不得作用于已变化的事务。nil = 普通切换（仅比代际）。
	Guard *RecoveryGuard
}

// RecoveryGuard 是恢复命令的锁内前置条件（F06）：ExpectedRevision 必须等于
// 当前池 revision；ExpectedTxnID 非空时当前未结 swap journal 的 TxnID 必须
// 一致。检查与执行处于同一 withLocks 区间，不存在预读→执行的 TOCTOU。
type RecoveryGuard struct {
	ExpectedRevision uint64
	ExpectedTxnID    string
}

// checkLocked 在持锁状态下校验 guard（调用方已 loadPoolLocked）。
func (g *RecoveryGuard) checkLocked(pool *AccountPool) error {
	if g == nil {
		return nil
	}
	var revision uint64
	if pool != nil {
		revision = pool.Revision
	}
	if revision != g.ExpectedRevision {
		return NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", g.ExpectedRevision, revision)
	}
	if g.ExpectedTxnID != "" {
		if pool == nil || pool.Transaction == nil || pool.Transaction.Kind != TransactionSwap ||
			pool.Transaction.Swap == nil || pool.Transaction.Swap.TxnID != g.ExpectedTxnID {
			return NewErr(ReasonRevisionConflict, "所选恢复事务已不是当前未结事务")
		}
	}
	return nil
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
	if err := req.Guard.checkLocked(pool); err != nil {
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

	// 同目标且三处确实匹配 → no-op（不能绕过漂移检查，design §4.4）。
	// F06：no-op 判定必须在未结事务处理之后——restore-source 场景 target==source
	// 且原生全 before 时，先由 reconcile 清 journal，再进入 no-op，不能带着
	// journal 提前返回 done。
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
	// （design §4.4）。F03-B：第二次 harvest 的返回值必须同步替换
	// sourceOwned/sourceRevs——journal 的 before 与 expected 必须来自同一次
	// 快照，否则刷新后的 A′ 对 before/after 均不可分类，会把可恢复事务卡死。
	current2, rerr := s.readStableAll(ctx)
	if rerr != nil {
		return SwapResult{}, rerr
	}
	if !sameRevisions(current2.Revisions, sourceRevs) {
		sourceOwned, sourceRevs, herr = s.harvestLocked(ctx, pool, pool.ActiveAccountID)
		if herr != nil {
			return SwapResult{}, herr
		}
		current2, rerr = s.readStableAll(ctx)
		if rerr != nil {
			return SwapResult{}, rerr
		}
		// F03-B：before 与 expected 必须来自同一次快照——重新 harvest 后若仍在
		// 变化（存在活跃外部写入者），静止条件已失效，fail closed 而非带着
		// 分裂快照进事务。
		if !sameRevisions(current2.Revisions, sourceRevs) {
			return SwapResult{}, NewErr(ReasonExternalSessionUncertain, "源账号回收期间原生槽位持续变化，已中止切换")
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
		TxnID:             txnID,
		SourceID:          pool.ActiveAccountID,
		TargetID:          req.TargetID,
		FromGeneration:    pool.ActiveGeneration,
		Phase:             SwapPrepared,
		Before:            sourceOwned,
		After:             after,
		ExpectedRevisions: current2.Revisions,
		StartedAt:         s.now(),
	}
	pool.Transaction = &PoolTransaction{Kind: TransactionSwap, Swap: journal}
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return SwapResult{}, err
	}
	journalPersisted = true
	s.logStep("swap-prepared", txnID, req.TargetID)

	if cerr := s.applyAndCommitLocked(ctx, pool, journal, req.Reason, respect, AllProjections, nil); cerr != nil {
		return SwapResult{}, cerr
	}
	return SwapResult{TxnID: txnID, Generation: pool.ActiveGeneration, ActiveID: pool.ActiveAccountID}, nil
}

// applyAndCommitLocked 执行受管投影写入序列、最终验证与池提交（design
// §4.4 后半段）。keys 指定要写的投影（roll-forward 只补未完成的）。
// recovery 非 nil 表示恢复 roll-forward，携带本轮分类快照：短锁内按当前值重新分类并冻结新的
// 恢复基线（R02），而非要求崩溃前的写入进度必然已落盘。
func (s *Service) applyAndCommitLocked(ctx context.Context, pool *AccountPool, journal *SwapJournal, reason string, respectSeconds int, keys []ProjectionKey, recovery *NativeProjections) error {
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

	// F03-A：短提交锁内最终复核整文件/受管 revision——最终一次 source 读取
	// 与取得原生锁之间若发生外部刷新（比较、rename、Keychain update 都不是
	// 跨进程 CAS，design §4.3），当前值已不是 journal 记录的期望基线，直接
	// 写会覆盖未回收的新 token；漂移则拒绝写入并交由 reconcile 分类（任一
	// 投影既非 before 也非 after → needs-reconcile，保全当前值），不盲覆盖。
	verify, verr := s.readStableAll(ctx)
	if verr != nil {
		return verr
	}
	if recovery != nil {
		if !sameRevisions(verify.Revisions, recovery.Revisions) {
			return NewErr(ReasonNeedsReconcile, "恢复分类后原生槽位再次漂移，停止写入")
		}
		// R02：原生 Patch 与池进度 Save 是两个独立提交——「Patch 成功→进度
		// Save 前终止」是合法崩溃窗口，持久化 journal 的 ExpectedRevisions
		// 仍是旧值。恢复在短锁内对当前三处重新分类：任一 unknown（或在
		// reconcile 分类后再次漂移成不可分类值）→ 拒绝；全部可分类 → 以当前
		// revision 冻结新恢复基线并持久化，只补仍为 before 的投影。
		pending, ferr := s.freezeRecoveryBaselineLocked(ctx, pool, journal, verify)
		if ferr != nil {
			return ferr
		}
		keys = pending
	} else if !sameRevisions(verify.Revisions, journal.ExpectedRevisions) {
		return NewErr(ReasonNeedsReconcile, "原生槽位在提交前发生意外变化（revision 漂移），已停止写入待恢复")
	}

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

// freezeRecoveryBaselineLocked 在恢复短锁内重新分类当前投影并冻结恢复
// 基线（R02）：拒绝 unknown；ExpectedRevisions 改为本次短锁读取的当前值并
// 先持久化（再次崩溃仍可按同一规则恢复）；返回仍为 before 的待补投影。
func (s *Service) freezeRecoveryBaselineLocked(ctx context.Context, pool *AccountPool, journal *SwapJournal, current *NativeProjections) ([]ProjectionKey, error) {
	pending := []ProjectionKey{}
	for _, key := range AllProjections {
		switch classifyProjection(current, journal, key) {
		case classBefore:
			pending = append(pending, key)
		case classAfter:
		default:
			return nil, NewErr(ReasonNeedsReconcile, "恢复提交前投影 %s 既非 before 也非 after，已停止写入待人工恢复", string(key))
		}
	}
	if !sameRevisions(current.Revisions, journal.ExpectedRevisions) {
		journal.ExpectedRevisions = current.Revisions
		pool.Revision++
		if err := s.savePoolLocked(ctx, pool); err != nil {
			return nil, err
		}
		s.logStep("reconcile-baseline-frozen", journal.TxnID, journal.TargetID)
	}
	return pending, nil
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
