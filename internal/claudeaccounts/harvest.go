package claudeaccounts

import (
	"context"
	"time"

	"github.com/tidwall/gjson"
)

// standbyRefreshMargin 是 standby grant 换入前的提前刷新边距（与 Pi 自身
// 的 5 分钟安全窗一致，design §5.1）。
const standbyRefreshMargin = 5 * time.Minute

// SwapReasonRecovered 是恢复路径补记的切换原因（journal 本身不存 reason，
// design §3.3；恢复完成不是一次新的手动切换）。
const SwapReasonRecovered = "recovered"

// HarvestActive 是“更新当前登录”入口：回收活动账号原生槽位的最新值
// （design §7.1）。expectedRevision 用于并发防护。
func (s *Service) HarvestActive(ctx context.Context, expectedRevision uint64) (AccountDTO, error) {
	release, err := s.withLocks(ctx, "harvest")
	if err != nil {
		return AccountDTO{}, err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return AccountDTO{}, err
	}
	if pool == nil || pool.ActiveAccountID == "" {
		return AccountDTO{}, NewErr(ReasonPoolMissing, "账号池未启用")
	}
	if pool.Transaction != nil {
		return AccountDTO{}, NewErr(ReasonNeedsReconcile, "池存在未结事务，先完成恢复")
	}
	if pool.Revision != expectedRevision {
		return AccountDTO{}, NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	gateRelease, err := s.acquireGate(ctx, "harvest")
	if err != nil {
		return AccountDTO{}, err
	}
	defer gateRelease()
	if err := s.checkQuiescence(ctx); err != nil {
		return AccountDTO{}, err
	}
	if _, _, err := s.harvestLocked(ctx, pool, pool.ActiveAccountID); err != nil {
		return AccountDTO{}, err
	}
	snap := pool.Account(pool.ActiveAccountID)
	return snap.ToDTO(pool.ActiveAccountID), nil
}

// harvestLocked 是 harvest 伪码（design §4.4）。前置：持有串行锁、mutation
// admission、写入者静止条件（由调用方保证）。返回当前受管投影与 revision。
func (s *Service) harvestLocked(ctx context.Context, pool *AccountPool, activeID string) (OwnedProjections, ExpectedRevisions, error) {
	active := pool.Account(activeID)
	if active == nil {
		return OwnedProjections{}, ExpectedRevisions{}, NewErr(ReasonPoolCorrupt, "活动账号不存在于池")
	}
	current, err := s.readStableAll(ctx)
	if err != nil {
		return OwnedProjections{}, ExpectedRevisions{}, err
	}
	owned := ownedFromNative(current)

	// 校验 current 的账号属于 activeId；不是则返回 EXTERNAL_ACCOUNT_CHANGE。
	if projectionsEqual(owned, active.OwnedProjectionsOf()) {
		// 无变化：不写池（mtime 变化可能来自 Codex 等其他条目，design §4.3）。
		return owned, current.Revisions, nil
	}
	ccID, piID, err := s.fetchPairedIdentity(ctx, current)
	if err != nil {
		return OwnedProjections{}, ExpectedRevisions{}, err
	}
	if ccID.AccountUUID != active.Identity.AccountUUID || piID.AccountUUID != active.Identity.AccountUUID {
		return OwnedProjections{}, ExpectedRevisions{}, NewErr(ReasonExternalAccountChange, "原生槽位已不属于活动账号")
	}

	// 对每个 grant 独立比较并保存新值（不拿 Pi 的 expires 与 CC 的互比）。
	nowT := s.now()
	if !equalJSON(current.ClaudeOAuth, active.Claude.OauthProjection) {
		active.Claude.OauthProjection = append([]byte(nil), current.ClaudeOAuth...)
		active.Claude.RawCredentialItem = append([]byte(nil), current.ClaudeOAuthRaw...)
		active.Claude.StoreLocator = current.Locator
		active.Claude.AdapterVersion = ClaudeKeychainAdapterVersion
		active.Claude.ObservedAt = nowT
		active.Claude.RevisionFingerprint = current.Revisions.ClaudeKeychain
	}
	if current.ClaudeIdentity != nil &&
		(!equalJSON(current.ClaudeIdentity.OauthAccount, active.ClaudeIdentity.OauthAccount) ||
			current.ClaudeIdentity.UserID != active.ClaudeIdentity.UserID) {
		active.ClaudeIdentity = *current.ClaudeIdentity
	}
	if !equalJSON(current.PiAnthropic, active.Pi.Anthropic) {
		active.Pi.Anthropic = append([]byte(nil), current.PiAnthropic...)
		active.Pi.AdapterVersion = PiRefreshAdapterVersion
		active.Pi.ObservedAt = nowT
		active.Pi.SourceMtimeNS = current.Revisions.PiAuth.MtimeNS
		active.Pi.RevisionFingerprint = current.Revisions.PiAuth
	}
	active.UpdatedAt = nowT
	active.Health.IdentityStatus = "verified"
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		// 池保存失败：harvest 不写原生槽位，直接失败返回。
		return OwnedProjections{}, ExpectedRevisions{}, err
	}
	s.logStep("harvest-saved", "", activeID)
	return owned, current.Revisions, nil
}

// ownedFromNative 把原生读取结果整理为受管投影组。
func ownedFromNative(current *NativeProjections) OwnedProjections {
	owned := OwnedProjections{PiAnthropic: current.PiAnthropic}
	if len(current.ClaudeOAuth) > 0 {
		owned.ClaudeOAuth = current.ClaudeOAuth
	}
	if current.ClaudeIdentity != nil {
		fields := *current.ClaudeIdentity
		owned.ClaudeIdentity = &fields
	}
	return owned
}

// ensureTargetReadyLocked 准备目标：独占刷新过期的 standby grants；验证
// 目标双端身份（design §4.4「验证目标双端身份」；F05：不能只信 capture 时的
// 身份证据与存储健康度，写入原生前必须用当前 token 现场验证）。调用方持锁
// 与 admission；网络身份探测在拿原生写锁之前完成（design §4.3）。
func (s *Service) ensureTargetReadyLocked(ctx context.Context, pool *AccountPool, targetID string) error {
	if pool.ActiveAccountID == targetID {
		return nil // 切到自身无需准备
	}
	snap := pool.Account(targetID)
	if snap == nil {
		return NewErr(ReasonRevisionConflict, "目标账号不存在")
	}
	// 身份证据：capture 时建立的双端配对证据必须存在且自洽。
	ev := snap.IdentityEvidence
	if ev == nil || ev.CCAccountUUID == "" || ev.CCAccountUUID != ev.PiAccountUUID ||
		snap.Identity.AccountUUID != ev.CCAccountUUID {
		return NewErr(ReasonIdentityUnverified, "目标账号缺少有效的双端身份证据")
	}
	// 健康度门禁：needs-login / refresh-uncertain / quarantined 不做候选。
	if err := grantHealthGate(snap.Health.ClaudeGrant, "claude"); err != nil {
		return err
	}
	if err := grantHealthGate(snap.Health.PiGrant, "pi"); err != nil {
		return err
	}
	// 过期 standby grant 换入前按需刷新（design §5.1）。
	refreshed := false
	if piGrantExpired(snap, s.now()) {
		if err := s.refreshStandbyGrantLocked(ctx, pool, targetID, GrantPi); err != nil {
			return err
		}
		refreshed = true
	}
	if claudeGrantExpired(snap, s.now()) {
		if err := s.refreshStandbyGrantLocked(ctx, pool, targetID, GrantClaude); err != nil {
			return err
		}
		refreshed = true
	}
	if refreshed {
		snap = pool.Account(targetID)
		if snap == nil {
			return NewErr(ReasonPoolCorrupt, "刷新后目标账号丢失")
		}
		if err := grantHealthGate(snap.Health.PiGrant, "pi"); err != nil {
			return err
		}
		if err := grantHealthGate(snap.Health.ClaudeGrant, "claude"); err != nil {
			return err
		}
	}
	// F05：写入原生前用目标当前两份 grant 现场验证身份——刷新后的新 token
	// 必须仍可证明属于目标账号（与目标身份/组织及双端配对一致）；未过期但
	// 已撤销的 token 同样在此暴露（identity 查询失败/不一致 → 不写原生）。
	return s.verifyTargetGrantsLocked(ctx, pool, targetID)
}

// verifyTargetGrantsLocked 用目标快照内当前两份 access token 分别查询身份，
// 并与目标身份/组织及双端配对比较（design §4.4；F05）。
func (s *Service) verifyTargetGrantsLocked(ctx context.Context, pool *AccountPool, targetID string) error {
	snap := pool.Account(targetID)
	if snap == nil {
		return NewErr(ReasonRevisionConflict, "目标账号不存在")
	}
	ccToken := ccProjectionAccessToken(snap.Claude.OauthProjection)
	piToken := piAnthropicAccessToken(snap.Pi.Anthropic)
	if ccToken == "" || piToken == "" {
		return NewErr(ReasonIdentityUnverified, "目标 grant 缺少可用的 access token")
	}
	ccID, err := s.identity.FetchProfile(ctx, ccToken)
	if err != nil {
		return NewErr(ReasonIdentityUnverified, "目标 Claude grant 身份验证失败：%s", ReasonOf(err))
	}
	piID, err := s.identity.FetchProfile(ctx, piToken)
	if err != nil {
		return NewErr(ReasonIdentityUnverified, "目标 Pi grant 身份验证失败：%s", ReasonOf(err))
	}
	if ccID.AccountUUID != snap.Identity.AccountUUID || piID.AccountUUID != snap.Identity.AccountUUID {
		return NewErr(ReasonIdentityMismatch, "目标 grant 身份与账号身份不符，拒绝写入原生槽位")
	}
	if ccID.AccountUUID != piID.AccountUUID {
		return NewErr(ReasonIdentityMismatch, "目标两份 grant 不属于同一账号，拒绝写入原生槽位")
	}
	if ccID.OrganizationUUID != "" && snap.Identity.OrganizationUUID != "" &&
		ccID.OrganizationUUID != snap.Identity.OrganizationUUID {
		return NewErr(ReasonIdentityMismatch, "目标 Claude grant 组织与账号记录不符，拒绝写入原生槽位")
	}
	return nil
}

func grantHealthGate(health GrantHealth, kind string) error {
	switch health {
	case HealthReady, HealthExpired:
		return nil // expired 由刷新流程处理；刷新后仍 expired 会在刷新错误中暴露
	case HealthNeedsLogin:
		return NewErr(ReasonNeedsLogin, "%s grant 需要重新登录", kind)
	case HealthRefreshUncertain, HealthQuarantined:
		return NewErr(ReasonRefreshUncertain, "%s grant 刷新结果不确定，已退出自动候选", kind)
	default:
		return NewErr(ReasonIdentityUnverified, "%s grant 健康状态未知", kind)
	}
}

// piGrantExpired 按 anthropic.expires（毫秒 epoch，已核实契约）判断。
func piGrantExpired(snap *AccountSnapshot, now time.Time) bool {
	ms := gjson.GetBytes(snap.Pi.Anthropic, "expires").Float()
	if ms <= 0 {
		return false
	}
	return time.UnixMilli(int64(ms)).Before(now.Add(standbyRefreshMargin))
}

// claudeGrantExpired 按 OAuth 投影内 expiresAt 判断。【待实测】字段名与
// 单位未验证（design §5.1）；字段缺失时视为未过期，由健康度与真机验证
// 兜底，不猜测契约。
func claudeGrantExpired(snap *AccountSnapshot, now time.Time) bool {
	ms := gjson.GetBytes(snap.Claude.OauthProjection, "expiresAt").Float()
	if ms <= 0 {
		return false
	}
	// 若为秒级会远小于当前毫秒，同样判定过期，由刷新流程给出明确错误。
	return time.UnixMilli(int64(ms)).Before(now.Add(standbyRefreshMargin))
}

// RefreshStandbyGrant 独占刷新一个 standby 账号的指定 grant（design §5.1）。
func (s *Service) RefreshStandbyGrant(ctx context.Context, accountID string, kind GrantKind) error {
	release, err := s.withLocks(ctx, "refresh-grant")
	if err != nil {
		return err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return err
	}
	return s.refreshStandbyGrantLocked(ctx, pool, accountID, kind)
}

// refreshStandbyGrantLocked 执行 RefreshIntent → POST → 校验 → 新 grant 与
// 清 intent 一次存池的单事务（design §5.1）。调用方持锁。
func (s *Service) refreshStandbyGrantLocked(ctx context.Context, pool *AccountPool, accountID string, kind GrantKind) error {
	snap := pool.Account(accountID)
	if snap == nil {
		return NewErr(ReasonRevisionConflict, "目标账号不存在")
	}
	if pool.ActiveAccountID == accountID {
		return NewErr(ReasonSessionsActive, "活动账号 grant 由原生客户端刷新，CodeBox 不主动 refresh（design §5.1）")
	}

	var result GrantRefreshResult
	var err error
	requestID, idErr := newRandomID()
	if idErr != nil {
		return idErr
	}
	intent := &RefreshIntent{
		AccountID:        accountID,
		GrantKind:        kind,
		ExpectedRevision: pool.Revision,
		RequestID:        requestID,
		StartedAt:        s.now(),
	}
	pool.RefreshIntent = intent
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return err // intent 尚未发出请求，保存失败即中止，无 uncertain 风险
	}

	switch kind {
	case GrantPi:
		refreshToken := gjson.GetBytes(snap.Pi.Anthropic, "refresh").String()
		if refreshToken == "" {
			return NewErr(ReasonNeedsLogin, "Pi grant 缺少 refresh token")
		}
		result, err = s.piRefresh.Refresh(ctx, refreshToken)
	case GrantClaude:
		// 【待实测】CC 投影内 refresh token 字段名未验证；适配器本身有
		// 协议门，不猜常量（design §5.1）。
		refreshToken := gjson.GetBytes(snap.Claude.OauthProjection, "refreshToken").String()
		if refreshToken == "" {
			return NewErr(ReasonUnsupportedCredentialSchema, "Claude grant 缺少可识别的 refresh token 字段（契约待验证）")
		}
		result, err = s.ccRefresh.Refresh(ctx, refreshToken)
	default:
		return NewErr(ReasonRevisionConflict, "未知 grant 类型 %q", string(kind))
	}

	if err != nil {
		switch ReasonOf(err) {
		case ReasonNeedsLogin:
			// invalid_grant：确定失败，标 needs-login 并清 intent。
			markGrantHealth(snap, kind, HealthNeedsLogin)
			pool.RefreshIntent = nil
			pool.Revision++
			if saveErr := s.savePoolLocked(ctx, pool); saveErr != nil {
				return saveErr
			}
			return err
		default:
			// 响应丢失/取消/未验证门：未结 intent 一律 refresh-uncertain
			// 并退出自动候选（design §5.1）。
			markGrantHealth(snap, kind, HealthRefreshUncertain)
			pool.Revision++
			if saveErr := s.savePoolLocked(ctx, pool); saveErr != nil {
				return saveErr
			}
			return err
		}
	}

	// 成功：新 grant 与清 intent 一次存池。
	if aerr := s.applyRefreshResultLocked(pool, snap, kind, result); aerr != nil {
		// 请求已发出而结果无法安全入池 → refresh-uncertain（design §5.1）。
		markGrantHealth(snap, kind, HealthRefreshUncertain)
		pool.Revision++
		if saveErr := s.savePoolLocked(ctx, pool); saveErr != nil {
			return saveErr
		}
		return NewErr(ReasonRefreshUncertain, "刷新结果写入池失败，结果不确定")
	}
	pool.RefreshIntent = nil
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		// 保存失败：token 可能已被服务器轮换 → refresh-uncertain
		// （design §5.1）。保留 intent 标记未结状态。
		markGrantHealth(snap, kind, HealthRefreshUncertain)
		s.logf("[claudeaccounts] refresh 存池失败，标记 refresh-uncertain（account=%s kind=%s）", accountID, string(kind))
		return NewErr(ReasonRefreshUncertain, "刷新成功但存池失败，结果不确定")
	}
	s.logStep("refresh-completed", requestID, accountID)
	s.emitEvent(Event{Kind: EventRefreshCompleted, TxnID: requestID, AccountID: accountID})
	return nil
}

// applyRefreshResultLocked 把刷新结果写入账号快照（Phase 1a：Pi 路径完整；
// CC 路径的投影内字段写回契约未验证，显式拒绝而不猜测字段）。
func (s *Service) applyRefreshResultLocked(pool *AccountPool, snap *AccountSnapshot, kind GrantKind, result GrantRefreshResult) error {
	nowT := s.now()
	switch kind {
	case GrantPi:
		// F13：在既有 anthropic JSON 上字段级更新 token/expiry，保留未知
		// 扩展字段（design §3.1 未知字段保留契约），不从零重建条目。
		entry, err := MergePiAnthropicRefresh(snap.Pi.Anthropic, result)
		if err != nil {
			return err
		}
		snap.Pi.Anthropic = entry
		snap.Pi.AdapterVersion = s.piRefresh.AdapterVersion()
		snap.Pi.ObservedAt = nowT
		markGrantHealth(snap, kind, HealthReady)
		return nil
	case GrantClaude:
		// 写回 claudeAiOauth 内 token 字段的契约【待实测】（design §3.1/§5.1），
		// 在真机验证前拒绝猜测字段结构。
		return NewErr(ReasonUnsupportedCredentialSchema, "Claude grant 刷新结果写回契约待真机验证")
	default:
		return NewErr(ReasonRevisionConflict, "未知 grant 类型")
	}
}

func markGrantHealth(snap *AccountSnapshot, kind GrantKind, health GrantHealth) {
	switch kind {
	case GrantPi:
		snap.Health.PiGrant = health
	case GrantClaude:
		snap.Health.ClaudeGrant = health
	}
}

// RemoveAccount 移除一个非活动账号（design §7.1：移除活动账号必须先切换
// 或停止管理；未清 journal 时禁止移除）。
func (s *Service) RemoveAccount(ctx context.Context, id string, expectedRevision uint64) error {
	release, err := s.withLocks(ctx, "remove-account")
	if err != nil {
		return err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return err
	}
	if pool == nil || pool.Accounts[id] == nil {
		return NewErr(ReasonRevisionConflict, "账号不存在")
	}
	if pool.Transaction != nil {
		return NewErr(ReasonNeedsReconcile, "存在未结事务，禁止移除账号")
	}
	if pool.ActiveAccountID == id {
		return NewErr(ReasonRevisionConflict, "活动账号须先切换到其他账号或停止管理")
	}
	if pool.RefreshIntent != nil && pool.RefreshIntent.AccountID == id {
		return NewErr(ReasonRefreshUncertain, "该账号存在未结刷新意图，禁止移除")
	}
	// F14：与 Rename/Capture 同源的乐观锁——stale revision 携带的删除请求
	// 必须拒绝，不基于旧列表快照改写池（design §7.2 接口一致性）。
	if pool.Revision != expectedRevision {
		return NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	delete(pool.Accounts, id)
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return err
	}
	s.logStep("account-removed", "", id)
	return nil
}

// RenameAccount 修改账号别名（design §7.2：别名长度限制 1..64）。
func (s *Service) RenameAccount(ctx context.Context, id, alias string, expectedRevision uint64) error {
	if len(alias) < 1 || len(alias) > 64 {
		return NewErr(ReasonRevisionConflict, "别名长度必须在 1..64")
	}
	release, err := s.withLocks(ctx, "rename-account")
	if err != nil {
		return err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return err
	}
	if pool == nil {
		return NewErr(ReasonPoolMissing, "账号池未启用")
	}
	snap := pool.Account(id)
	if snap == nil {
		return NewErr(ReasonRevisionConflict, "账号不存在")
	}
	if pool.Revision != expectedRevision {
		return NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	snap.Alias = alias
	snap.UpdatedAt = s.now()
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return err
	}
	return nil
}
