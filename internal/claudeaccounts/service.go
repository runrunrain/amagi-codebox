package claudeaccounts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// MutationGate 是共享凭据 mutation admission 的注入边界（design §2/§6.3）。
// Phase 1a 不接线真实 SharedServiceClaudeCredentials kind；接线任务负责提供
// 实现。Acquire 返回的 release 必须幂等。
type MutationGate interface {
	Acquire(ctx context.Context, why string) (func(), error)
}

// QuiescenceChecker 是写入者静止条件的注入边界（design §4.3）：消费进程
// 结束、无未清外部进程树。返回 *Error（SESSIONS_ACTIVE /
// EXTERNAL_SESSION_UNCERTAIN 等）。
type QuiescenceChecker interface {
	Check(ctx context.Context) error
}

// Event 是 credential-free 的完成事件（design §4.4：发一个完成事件）。
type Event struct {
	Kind       string `json:"kind"`
	TxnID      string `json:"txnId,omitempty"`
	AccountID  string `json:"accountId,omitempty"`
	Generation uint64 `json:"generation,omitempty"`
}

// 事件种类。
const (
	EventCaptureCompleted   = "capture-completed"
	EventEnrollmentBegun    = "enrollment-begun"
	EventEnrollmentCleared  = "enrollment-cleared"
	EventSwapCompleted      = "swap-completed"
	EventReconcileCompleted = "reconcile-completed"
	EventRefreshCompleted   = "refresh-completed"
)

// Deps 是 Service 的依赖注入集合（design §2：App 向服务注入共享 lease、
// 标准路径、固定端点 HTTP client、时钟、进程观察器）。所有路径/后端必须
// 注入；测试使用 tempdir 与 fake，绝不触碰真实 Keychain 或真实
// ~/.claude、~/.pi/agent（design §3.3）。
type Deps struct {
	Pool       PoolStore
	Native     NativeCredentialStore
	LockPath   string // 跨进程锁文件路径
	PiAuthPath string // Pi auth.json 路径（proper-lockfile 兼容锁目标）
	Gate       MutationGate
	Quiescence QuiescenceChecker
	Identity   IdentityProvider

	HTTPClient *http.Client
	PiRefresh  *PiRefreshAdapter
	CCRefresh  *CCRefreshAdapter

	Clock       func() time.Time
	Logf        func(format string, args ...any)
	EmitEvent   func(Event)
}

// Service 是 Claude 订阅账号轮换的事务服务（design §4）。进程内串行 + 跨
// 进程锁保护 read-modify-write；所有公共方法遵循同一事务原语与门禁
// （design §1.3-7）。
type Service struct {
	pool      PoolStore
	native    NativeCredentialStore
	lockPath  string
	piAuthPath string
	gate      MutationGate
	quiescence QuiescenceChecker
	identity  IdentityProvider
	piRefresh *PiRefreshAdapter
	ccRefresh *CCRefreshAdapter
	clock     func() time.Time
	logf      func(format string, args ...any)
	emit      func(Event)

	mu sync.Mutex
}

// NewService 构造事务服务并校验必需依赖。
func NewService(deps Deps) (*Service, error) {
	if deps.Pool == nil {
		return nil, fmt.Errorf("claudeaccounts: Pool 依赖必需")
	}
	if deps.Native == nil {
		return nil, fmt.Errorf("claudeaccounts: Native 依赖必需")
	}
	if deps.LockPath == "" {
		return nil, fmt.Errorf("claudeaccounts: LockPath 依赖必需")
	}
	if deps.PiAuthPath == "" {
		return nil, fmt.Errorf("claudeaccounts: PiAuthPath 依赖必需")
	}
	if deps.Gate == nil {
		return nil, fmt.Errorf("claudeaccounts: Gate 依赖必需（共享凭据 admission 由接线层注入）")
	}
	if deps.Quiescence == nil {
		return nil, fmt.Errorf("claudeaccounts: Quiescence 依赖必需（写入者静止条件由接线层注入）")
	}
	if deps.Identity == nil {
		return nil, fmt.Errorf("claudeaccounts: Identity 依赖必需")
	}
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	logf := deps.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	emit := deps.EmitEvent
	if emit == nil {
		emit = func(Event) {}
	}
	client := deps.HTTPClient
	if client == nil {
		client = NewConstrainedHTTPClient(nil)
	}
	piRefresh := deps.PiRefresh
	if piRefresh == nil {
		piRefresh = NewPiRefreshAdapter(client)
	}
	ccRefresh := deps.CCRefresh
	if ccRefresh == nil {
		ccRefresh = NewCCRefreshAdapter(client, CCRefreshProtocol{})
	}
	return &Service{
		pool:       deps.Pool,
		native:     deps.Native,
		lockPath:   deps.LockPath,
		piAuthPath: deps.PiAuthPath,
		gate:       deps.Gate,
		quiescence: deps.Quiescence,
		identity:   deps.Identity,
		piRefresh:  piRefresh,
		ccRefresh:  ccRefresh,
		clock:      clock,
		logf:       logf,
		emit:       emit,
	}, nil
}

// now 返回注入时钟的当前时间。
func (s *Service) now() time.Time { return s.clock() }

// logStep 记录无敏感信息的步骤日志（只含事务/账号 ID 与 reason）。
func (s *Service) logStep(step, txnID, accountID string) {
	s.logf("[claudeaccounts] %s txn=%s account=%s", step, txnID, accountID)
}

func (s *Service) emitEvent(ev Event) { s.emit(ev) }

// withLocks 取进程内串行锁与跨进程锁。调用方在返回的 release 内先释放跨
// 进程锁再返回（进程内锁由 defer 释放）。
func (s *Service) withLocks(ctx context.Context, step string) (func(), error) {
	s.mu.Lock()
	flock, err := acquireFileLock(s.lockPath)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		flock.release()
		s.mu.Unlock()
	}, nil
}

// loadPoolLocked 读取并校验池；从未创建返回 (nil, nil)。
func (s *Service) loadPoolLocked(ctx context.Context) (*AccountPool, error) {
	pool, err := s.pool.Load(ctx)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// savePoolLocked 写池（Store 内部回读验证 revision）。
func (s *Service) savePoolLocked(ctx context.Context, pool *AccountPool) error {
	return s.pool.Save(ctx, pool)
}

// readStableAll 读取三处受管投影的稳定快照。
func (s *Service) readStableAll(ctx context.Context) (*NativeProjections, error) {
	return s.native.ReadStable(ctx)
}

// acquireGate 取得共享凭据 mutation admission；gate 层错误映射为
// SESSIONS_ACTIVE（design §4.4：取得 admission 后禁止新启动）。
func (s *Service) acquireGate(ctx context.Context, why string) (func(), error) {
	release, err := s.gate.Acquire(ctx, why)
	if err != nil {
		if _, ok := AsError(err); ok {
			return nil, err
		}
		return nil, NewErr(ReasonSessionsActive, "共享凭据 admission 被拒")
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		release()
	}, nil
}

// checkQuiescence 校验写入者静止条件（design §4.3）。
func (s *Service) checkQuiescence(ctx context.Context) error {
	if err := s.quiescence.Check(ctx); err != nil {
		if _, ok := AsError(err); ok {
			return err
		}
		return NewErr(ReasonExternalSessionUncertain, "写入者静止条件未确认")
	}
	return nil
}

// LoadPool 只读返回池快照（深拷贝，调用方不得篡改内部状态）。
func (s *Service) LoadPool(ctx context.Context) (*AccountPool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadPoolLocked(ctx)
}

// ListAccounts 返回 credential-free 的账号 DTO 列表。
func (s *Service) ListAccounts(ctx context.Context) ([]AccountDTO, error) {
	pool, err := s.LoadPool(ctx)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, nil
	}
	out := make([]AccountDTO, 0, len(pool.Accounts))
	for _, snap := range pool.Accounts {
		if snap == nil {
			continue
		}
		out = append(out, snap.ToDTO(pool.ActiveAccountID))
	}
	sortAccountDTOs(out)
	return out, nil
}

func sortAccountDTOs(dtos []AccountDTO) {
	for i := 1; i < len(dtos); i++ {
		for j := i; j > 0 && dtos[j].ID < dtos[j-1].ID; j-- {
			dtos[j], dtos[j-1] = dtos[j-1], dtos[j]
		}
	}
}

// ---- 身份提取辅助（字段名契约标注）----

// ccOAuthAccessTokenField 是 claudeAiOauth 投影内 access token 的字段名
// （【待实测】契约，design §3.1；缺失时身份查询返回 IDENTITY_UNVERIFIED）。
const ccOAuthAccessTokenField = "accessToken"

// claudeJSONAccountUUID 从 `.claude.json` oauthAccount 对象取 accountUuid
// （pi-black claude-code-protocol.ts 已核实的字段）。
func claudeJSONAccountUUID(oauthAccount json.RawMessage) string {
	return gjson.GetBytes(oauthAccount, "accountUuid").String()
}

// claudeJSONEmail 从 oauthAccount 取展示邮箱（仅展示用，design §4.2）。
func claudeJSONEmail(oauthAccount json.RawMessage) string {
	return gjson.GetBytes(oauthAccount, "emailAddress").String()
}

// ccProjectionAccessToken 从 Claude OAuth 投影取 access token。
func ccProjectionAccessToken(oauth json.RawMessage) string {
	return gjson.GetBytes(oauth, ccOAuthAccessTokenField).String()
}

// piAnthropicAccessToken 从 anthropic 条目取 access token（已核实契约）。
func piAnthropicAccessToken(anthropic json.RawMessage) string {
	return gjson.GetBytes(anthropic, "access").String()
}

// fetchPairedIdentity 网络校验双 grant 身份并配对（design §4.2）。
// expectedAccountUUID 为本地 `.claude.json` 投影的 accountUuid。
func (s *Service) fetchPairedIdentity(ctx context.Context, native *NativeProjections) (ccID, piID ProfileIdentity, err error) {
	ccToken := ccProjectionAccessToken(native.ClaudeOAuth)
	piToken := piAnthropicAccessToken(native.PiAnthropic)
	if ccToken == "" || piToken == "" {
		return ProfileIdentity{}, ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "grant 缺少可用的 access token")
	}
	ccID, err = s.identity.FetchProfile(ctx, ccToken)
	if err != nil {
		return
	}
	piID, err = s.identity.FetchProfile(ctx, piToken)
	if err != nil {
		return
	}
	expected := ""
	if native.ClaudeIdentity != nil && len(native.ClaudeIdentity.OauthAccount) > 0 {
		expected = claudeJSONAccountUUID(native.ClaudeIdentity.OauthAccount)
	}
	if err = VerifyPairedIdentities(ccID, piID, expected); err != nil {
		return
	}
	return ccID, piID, nil
}

// ---- capture / enrollment（design §4.1/§4.4）----

// CaptureFirstAccount 保存当前双端登录为首个账号并设为活动账号
// （design §4.1 步骤 1）。expectedRevision 必须匹配当前池 revision（空池
// 为 0）。任何错误均不写原生槽位。
func (s *Service) CaptureFirstAccount(ctx context.Context, alias string, expectedRevision uint64) (AccountDTO, error) {
	release, err := s.withLocks(ctx, "capture-first")
	if err != nil {
		return AccountDTO{}, err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return AccountDTO{}, err
	}
	if pool != nil && pool.ActiveAccountID != "" {
		return AccountDTO{}, NewErr(ReasonRevisionConflict, "已有活动账号，添加另一个账号走 BeginEnrollment")
	}
	if pool == nil {
		pool = NewAccountPool()
	}
	if pool.Transaction != nil {
		return AccountDTO{}, NewErr(ReasonNeedsReconcile, "池存在未结事务，先完成恢复")
	}
	if pool.Revision != expectedRevision {
		return AccountDTO{}, NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	snap, err := s.captureLocked(ctx, pool, alias, "")
	if err != nil {
		return AccountDTO{}, err
	}
	return snap.ToDTO(pool.ActiveAccountID), nil
}

// BeginEnrollment 先 harvest 现有活动账号并持久化，再进入 enrolling 状态
// （design §4.1 步骤 2）。暂停自动与受影响启动由接线层的 admission 语义
// 承接。返回 enrollmentId（= journal TxnID）。
func (s *Service) BeginEnrollment(ctx context.Context, expectedRevision uint64) (string, error) {
	release, err := s.withLocks(ctx, "begin-enrollment")
	if err != nil {
		return "", err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return "", err
	}
	if pool == nil || pool.ActiveAccountID == "" {
		return "", NewErr(ReasonPoolMissing, "尚无活动账号，首次录入走 CaptureFirstAccount")
	}
	if pool.Transaction != nil {
		return "", NewErr(ReasonNeedsReconcile, "池存在未结事务，先完成恢复")
	}
	if pool.Revision != expectedRevision {
		return "", NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	gateRelease, err := s.acquireGate(ctx, "begin-enrollment")
	if err != nil {
		return "", err
	}
	defer gateRelease()
	if err := s.checkQuiescence(ctx); err != nil {
		return "", err
	}
	// 先 harvest 现有账号（内部完成身份校验与池保存）。
	if _, _, err := s.harvestLocked(ctx, pool, pool.ActiveAccountID); err != nil {
		return "", err
	}
	txnID, err := newRandomID()
	if err != nil {
		return "", err
	}
	pool.Transaction = &PoolTransaction{Kind: TransactionEnrollment, Enrollment: &EnrollmentJournal{
		TxnID: txnID, Harvested: true, StartedAt: s.now(),
	}}
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return "", err
	}
	s.logStep("enrollment-begun", txnID, pool.ActiveAccountID)
	s.emitEvent(Event{Kind: EventEnrollmentBegun, TxnID: txnID, AccountID: pool.ActiveAccountID})
	return txnID, nil
}

// CaptureEnrolledAccount 完成第二个账号的采集（design §4.1 步骤 4-5）：
// capture 双 grant 身份；不一致不入池、不宣布活动。通过则一次保存 B
// 快照、active=B、generation+1、清除 enrollment。
func (s *Service) CaptureEnrolledAccount(ctx context.Context, enrollmentID, alias string, expectedRevision uint64) (AccountDTO, error) {
	release, err := s.withLocks(ctx, "capture-enrolled")
	if err != nil {
		return AccountDTO{}, err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return AccountDTO{}, err
	}
	if pool == nil || pool.Transaction == nil || pool.Transaction.Kind != TransactionEnrollment || pool.Transaction.Enrollment == nil {
		return AccountDTO{}, NewErr(ReasonNeedsReconcile, "不存在进行中的 enrollment")
	}
	if pool.Transaction.Enrollment.TxnID != enrollmentID {
		return AccountDTO{}, NewErr(ReasonRevisionConflict, "enrollmentId 与池记录不符")
	}
	if pool.Revision != expectedRevision {
		return AccountDTO{}, NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	snap, err := s.captureLocked(ctx, pool, alias, enrollmentID)
	if err != nil {
		return AccountDTO{}, err
	}
	return snap.ToDTO(pool.ActiveAccountID), nil
}

// CancelEnrollment 取消录入（design §4.1 末段）：只清 enrollment journal；
// 若原生槽位在录入期间被登录进程改变，不恢复旧 token 覆盖新值，保持
// needs-reconcile 由 reconcile 分类。
func (s *Service) CancelEnrollment(ctx context.Context, expectedRevision uint64) error {
	release, err := s.withLocks(ctx, "cancel-enrollment")
	if err != nil {
		return err
	}
	defer release()
	pool, err := s.loadPoolLocked(ctx)
	if err != nil {
		return err
	}
	if pool == nil || pool.Transaction == nil || pool.Transaction.Kind != TransactionEnrollment {
		return NewErr(ReasonNeedsReconcile, "不存在进行中的 enrollment")
	}
	if pool.Revision != expectedRevision {
		return NewErr(ReasonRevisionConflict, "池 revision 期望 %d，实际 %d", expectedRevision, pool.Revision)
	}
	txnID := pool.Transaction.Enrollment.TxnID
	pool.Transaction = nil
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return err
	}
	s.logStep("enrollment-cleared", txnID, pool.ActiveAccountID)
	s.emitEvent(Event{Kind: EventEnrollmentCleared, TxnID: txnID})
	return nil
}

// captureLocked 是 capture 伪码（design §4.4）：调用方持锁；任何错误均不
// 写原生槽位。
func (s *Service) captureLocked(ctx context.Context, pool *AccountPool, alias, enrollmentID string) (*AccountSnapshot, error) {
	gateRelease, err := s.acquireGate(ctx, "capture")
	if err != nil {
		return nil, err
	}
	defer gateRelease()
	if err := s.checkQuiescence(ctx); err != nil {
		return nil, err
	}

	var native *NativeProjections
	var ccID, piID ProfileIdentity
	// 网络身份校验结束后再 readStable；有变化则有限重试整个采集。
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctxShort(ctx); err != nil {
			return nil, err
		}
		first, err := s.readStableAll(ctx)
		if err != nil {
			return nil, err
		}
		ccID, piID, err = s.fetchPairedIdentity(ctx, first)
		if err != nil {
			return nil, err
		}
		second, err := s.readStableAll(ctx)
		if err != nil {
			return nil, err
		}
		if sameRevisions(first.Revisions, second.Revisions) {
			native = second
			break
		}
	}
	if native == nil {
		return nil, NewErr(ReasonNeedsReconcile, "采集期间原生槽位持续变化")
	}

	// 构建 AccountSnapshot（各 grant 保留各自原始字段与时间单位）。
	accountID := ""
	for id, existing := range pool.Accounts {
		if existing != nil && existing.Identity.AccountUUID == ccID.AccountUUID &&
			existing.Identity.OrganizationUUID == ccID.OrganizationUUID {
			accountID = id // 已有同账号：更新登录，沿用 local ID（design §4.1）
			break
		}
	}
	if accountID == "" {
		accountID, err = NewLocalAccountID()
		if err != nil {
			return nil, err
		}
	}
	nowT := s.now()
	old := pool.Accounts[accountID]
	snap := &AccountSnapshot{
		ID:        accountID,
		Alias:     alias,
		CreatedAt: nowT,
		UpdatedAt: nowT,
		Identity: AccountIdentity{
			AccountUUID:      ccID.AccountUUID,
			OrganizationUUID: ccID.OrganizationUUID,
			Email:            claudeJSONEmail(native.ClaudeIdentity.OauthAccount),
			SeatTier:         ccID.SeatTier,
		},
		Claude: ClaudeGrantState{
			OauthProjection: append(json.RawMessage(nil), native.ClaudeOAuth...),
			RawCredentialItem: append(json.RawMessage(nil), native.ClaudeOAuthRaw...),
			StoreLocator:      native.Locator,
			AdapterVersion:    ClaudeKeychainAdapterVersion,
			ObservedAt:        nowT,
			RevisionFingerprint: native.Revisions.ClaudeKeychain,
		},
		ClaudeIdentity: ClaudeIdentityFields{
			OauthAccount: append(json.RawMessage(nil), native.ClaudeIdentity.OauthAccount...),
			UserID:       native.ClaudeIdentity.UserID,
		},
		Pi: PiGrantState{
			Anthropic:          append(json.RawMessage(nil), native.PiAnthropic...),
			AdapterVersion:     PiRefreshAdapterVersion,
			ObservedAt:         nowT,
			SourceMtimeNS:      native.Revisions.PiAuth.MtimeNS,
			RevisionFingerprint: native.Revisions.PiAuth,
		},
		IdentityEvidence: &IdentityEvidence{
			CCAccountUUID:  ccID.AccountUUID,
			PiAccountUUID:  piID.AccountUUID,
			CheckedAt:      nowT,
			AdapterVersion: IdentityAdapterVersion,
		},
		Health: AccountHealth{ClaudeGrant: HealthReady, PiGrant: HealthReady, IdentityStatus: "verified"},
	}
	if old != nil {
		snap.CreatedAt = old.CreatedAt // 更新登录保留原创建时间
	}
	pool.Accounts[accountID] = snap
	pool.ActiveAccountID = accountID
	pool.ActiveGeneration++
	pool.Transaction = nil // 清除 enrollment（design §4.1 步骤 5：一次保存）
	pool.Revision++
	if err := s.savePoolLocked(ctx, pool); err != nil {
		return nil, err
	}
	s.logStep("capture-completed", enrollmentID, accountID)
	s.emitEvent(Event{Kind: EventCaptureCompleted, AccountID: accountID, Generation: pool.ActiveGeneration})
	return snap, nil
}

// sameRevisions 比较两处 revision 指纹是否一致。
func sameRevisions(a, b ExpectedRevisions) bool {
	return fileRevisionEqual(a.ClaudeKeychain, b.ClaudeKeychain) &&
		fileRevisionEqual(a.ClaudeJSON, b.ClaudeJSON) &&
		fileRevisionEqual(a.PiAuth, b.PiAuth)
}

func fileRevisionEqual(a, b *FileRevision) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.SHA256Hex == b.SHA256Hex && a.ManagedHash == b.ManagedHash
}

// projectionsEqual 逐投影比较两组受管投影值（语义比较，容忍 JSON 格式
// 差异；池 envelope 往返会 compact 化，design §4.3 的“hash 相等”以规范形
// 态为准）。
func projectionsEqual(a, b OwnedProjections) bool {
	ccOK := equalJSON(a.ClaudeOAuth, b.ClaudeOAuth)
	idOK := false
	if a.ClaudeIdentity == nil && b.ClaudeIdentity == nil {
		idOK = true
	} else if a.ClaudeIdentity != nil && b.ClaudeIdentity != nil {
		idOK = a.ClaudeIdentity.UserID == b.ClaudeIdentity.UserID &&
			equalJSON(a.ClaudeIdentity.OauthAccount, b.ClaudeIdentity.OauthAccount)
	}
	return ccOK && idOK && equalJSON(a.PiAnthropic, b.PiAnthropic)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newRandomID 生成随机 UUID（事务 ID 复用同一生成器）。
func newRandomID() (string, error) { return NewLocalAccountID() }
