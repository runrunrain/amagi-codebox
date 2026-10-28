package claudeaccounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PoolSchemaVersion 是账号池 envelope 的 schema 版本（design §3.1）。
const PoolSchemaVersion = 1

// GrantKind 标识两份独立 OAuth grant（design §1.3-1：两份 token 不互抄）。
type GrantKind string

const (
	GrantClaude GrantKind = "claude"
	GrantPi     GrantKind = "pi"
)

// ProjectionKey 标识三处受管原生投影（design §4）。
type ProjectionKey string

const (
	ProjectionCCKeychain ProjectionKey = "cc-keychain"
	ProjectionClaudeJSON ProjectionKey = "claude-json"
	ProjectionPiAuth     ProjectionKey = "pi-auth"
)

// AllProjections 是受管投影全集，顺序即 swap 的写入顺序（design §4.4）。
var AllProjections = []ProjectionKey{ProjectionCCKeychain, ProjectionClaudeJSON, ProjectionPiAuth}

// FileRevision 是一处原生位置的 revision 指纹（design §4.3：整文件 hash +
// 受管字段 hash，mtime/inode 只作辅助；hash 不进日志，只存 Keychain envelope）。
type FileRevision struct {
	SHA256Hex   string `json:"sha256"`
	ManagedHash string `json:"managed,omitempty"`
	MtimeNS     int64  `json:"mtimeNs,omitempty"`
	Ino         uint64 `json:"ino,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// HashRevision 计算字节的 SHA-256 指纹。
func HashRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// KeychainLocator 精确标识 Claude Code 凭据 Keychain 项：service + 唯一
// 匹配发现的 account（design §3.1：零项报缺失、多项报歧义）。
type KeychainLocator struct {
	Service string `json:"service"`
	Account string `json:"account,omitempty"`
}

// ClaudeIdentityFields 是 `.claude.json` 中本功能唯一可写的两个字段
// （design §1.3-4：只改 oauthAccount、userID；oauthAccount 完整保存其现有
// 字段，不以当前认识的少数字段重建）。
type ClaudeIdentityFields struct {
	OauthAccount json.RawMessage `json:"oauthAccount"`
	UserID       string          `json:"userID"`
}

// OwnedProjections 是事务拥有的受管投影值。只保存本事务拥有的投影，不把
// 完整 `.claude.json` 历史和其他 Pi token 拷贝进 journal（design §3.3）。
type OwnedProjections struct {
	ClaudeOAuth     json.RawMessage       `json:"claudeOauth,omitempty"`
	ClaudeIdentity  *ClaudeIdentityFields `json:"claudeIdentity,omitempty"`
	PiAnthropic     json.RawMessage       `json:"piAnthropic,omitempty"`
}

// Projection 按 key 取受管投影；未拥有返回 nil。
func (o OwnedProjections) Projection(key ProjectionKey) json.RawMessage {
	switch key {
	case ProjectionCCKeychain:
		return o.ClaudeOAuth
	case ProjectionClaudeJSON:
		if o.ClaudeIdentity == nil {
			return nil
		}
		return o.ClaudeIdentity.OauthAccount
	case ProjectionPiAuth:
		return o.PiAnthropic
	}
	return nil
}

// IsZero 报告是否未拥有任何投影。
func (o OwnedProjections) IsZero() bool {
	return o.ClaudeOAuth == nil && o.ClaudeIdentity == nil && o.PiAnthropic == nil
}

// ExpectedRevisions 是 journal 记录的各处期望 revision，用于最终复核。
type ExpectedRevisions struct {
	ClaudeKeychain *FileRevision `json:"claudeKeychain,omitempty"`
	ClaudeJSON     *FileRevision `json:"claudeJson,omitempty"`
	PiAuth         *FileRevision `json:"piAuth,omitempty"`
}

// SwapPhase 是 SwapJournal 的进度阶段（design §4.4：每个写入点持久化进度）。
type SwapPhase string

const (
	SwapPrepared    SwapPhase = "prepared"
	SwapPatchingCC  SwapPhase = "patching-cc-keychain"
	SwapPatchingCJ  SwapPhase = "patching-claude-json"
	SwapPatchingPA  SwapPhase = "patching-pi-auth"
	SwapVerifying   SwapPhase = "verifying"
	SwapCommitting  SwapPhase = "committing"
)

// SwapJournal 是一笔切换事务的持久化 journal（design §3.3）。
type SwapJournal struct {
	TxnID            string            `json:"txnId"`
	SourceID         string            `json:"sourceId"`
	TargetID         string            `json:"targetId"`
	FromGeneration   uint64            `json:"fromGeneration"`
	Phase            SwapPhase         `json:"phase"`
	Before           OwnedProjections  `json:"beforeOwnedProjections"`
	After            OwnedProjections  `json:"afterOwnedProjections"`
	Applied          []ProjectionKey   `json:"appliedMask,omitempty"`
	ExpectedRevisions ExpectedRevisions `json:"expectedRevisions"`
	StartedAt        time.Time         `json:"startedAt"`
}

// HasApplied 报告指定投影是否已按 journal 记录写入过（提示性，实际位置以
// 回读判定，design §4.5）。
func (j *SwapJournal) HasApplied(key ProjectionKey) bool {
	for _, k := range j.Applied {
		if k == key {
			return true
		}
	}
	return false
}

// EnrollmentJournal 记录录入向导状态：BeginEnrollment 先 harvest 现有活动
// 账号再进入 enrolling（design §4.1）。
type EnrollmentJournal struct {
	TxnID     string    `json:"txnId"`
	Harvested bool      `json:"harvested"`
	StartedAt time.Time `json:"startedAt"`
	// DriftedReason 非空表示取消录入时检测到原生槽位已偏离活动源账号
	//（F04/design §4.1）：不清除 journal、进入 needs-reconcile，由主上选择
	// 完成录入保存当前配对，或在原生客户端重新登录原账号后再次取消。
	DriftedReason string `json:"driftedReason,omitempty"`
}

// TransactionKind 区分池上未结事务类型。
type TransactionKind string

const (
	TransactionSwap       TransactionKind = "swap"
	TransactionEnrollment TransactionKind = "enrollment"
)

// PoolTransaction 是池 envelope 中未结事务的包装（Swap 或 Enrollment 二选一）。
type PoolTransaction struct {
	Kind       TransactionKind    `json:"kind"`
	Swap       *SwapJournal       `json:"swap,omitempty"`
	Enrollment *EnrollmentJournal `json:"enrollment,omitempty"`
}

// RefreshIntent 持久化“刷新请求已发出”意图（design §5.1：先写 intent →
// POST → 校验 → 新 grant 与清 intent 一次存池；未结 intent 一律
// refresh-uncertain）。
type RefreshIntent struct {
	AccountID        string    `json:"accountId"`
	GrantKind        GrantKind `json:"grantKind"`
	ExpectedRevision uint64    `json:"expectedRevision"`
	RequestID        string    `json:"requestId"`
	StartedAt        time.Time `json:"startedAt"`
}

// LastSwitch 记录最近一次成功切换。
type LastSwitch struct {
	At              time.Time `json:"at"`
	SourceAccountID string    `json:"sourceAccountId,omitempty"`
	TargetAccountID string    `json:"targetAccountId"`
	Reason          string    `json:"reason"`
}

// 切换原因（design §7.2：前端不可伪造 automatic）。
const (
	SwapReasonManual = "manual"
)

// AccountIdentity 是池内账号身份（邮箱仅展示，不作主键，design §4.2）。
type AccountIdentity struct {
	AccountUUID      string `json:"accountUuid,omitempty"`
	OrganizationUUID string `json:"organizationUuid,omitempty"`
	Email            string `json:"email,omitempty"`
	SeatTier         string `json:"seatTier,omitempty"`
}

// GrantHealth 按 grant 独立记录健康度（design §3.1）。
type GrantHealth string

const (
	HealthReady            GrantHealth = "ready"
	HealthExpired          GrantHealth = "expired"
	HealthNeedsLogin       GrantHealth = "needs-login"
	HealthRefreshUncertain GrantHealth = "refresh-uncertain"
	HealthQuarantined      GrantHealth = "quarantined"
)

// AccountHealth 汇总账号健康度；完整账号只有两个 grant 及身份都通过才能
// 进入自动候选（自动引擎属后续任务）。
type AccountHealth struct {
	ClaudeGrant    GrantHealth `json:"claudeGrant"`
	PiGrant        GrantHealth `json:"piGrant"`
	IdentityStatus string      `json:"identityStatus,omitempty"`
	ReasonCode     string      `json:"reasonCode,omitempty"`
}

// ClaudeGrantState 是 Claude Code 侧 grant 的池内状态。rawCredentialItem
// 只用于审计/恢复所需的安全镜像；实际应用通过 oauthProjection 修改
// （design §3.1）。
type ClaudeGrantState struct {
	RawCredentialItem   json.RawMessage `json:"rawCredentialItem,omitempty"`
	OauthProjection     json.RawMessage `json:"oauthProjection,omitempty"`
	StoreLocator        *KeychainLocator `json:"storeLocator,omitempty"`
	ExpiresAt           *time.Time      `json:"expiresAt,omitempty"`
	AdapterVersion      string          `json:"adapterVersion,omitempty"`
	ObservedAt          time.Time       `json:"observedAt"`
	RevisionFingerprint *FileRevision   `json:"revisionFingerprint,omitempty"`
}

// PiGrantState 是 Pi 侧 anthropic grant 的池内状态（仅 anthropic 键，
// 不复制其他 provider 条目，design §3.1）。
type PiGrantState struct {
	Anthropic           json.RawMessage `json:"anthropic,omitempty"`
	AdapterVersion      string          `json:"adapterVersion,omitempty"`
	ObservedAt          time.Time       `json:"observedAt"`
	SourceMtimeNS       int64           `json:"sourceMtimeNs,omitempty"`
	RevisionFingerprint *FileRevision   `json:"revisionFingerprint,omitempty"`
}

// IdentityEvidence 是双 grant 身份配对校验的证据（存 Keychain，不出 DTO）。
type IdentityEvidence struct {
	CCAccountUUID  string    `json:"ccAccountUuid,omitempty"`
	PiAccountUUID  string    `json:"piAccountUuid,omitempty"`
	CheckedAt      time.Time `json:"checkedAt"`
	AdapterVersion string    `json:"adapterVersion,omitempty"`
}

// AccountSnapshot 是一个本机账号的完整快照（design §3.1）。
type AccountSnapshot struct {
	ID              string               `json:"id"`
	Alias           string               `json:"alias"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
	Identity        AccountIdentity      `json:"identity"`
	Claude          ClaudeGrantState     `json:"claude"`
	ClaudeIdentity  ClaudeIdentityFields `json:"claudeIdentity"`
	Pi              PiGrantState         `json:"pi"`
	IdentityEvidence *IdentityEvidence   `json:"identityEvidence,omitempty"`
	Health          AccountHealth        `json:"health"`
}

// OwnedProjectionsOf 从快照导出三处受管投影值（用于 harvest 前值与 swap
// after 值）。快照缺失的投影保持未拥有。
func (s *AccountSnapshot) OwnedProjectionsOf() OwnedProjections {
	owned := OwnedProjections{PiAnthropic: s.Pi.Anthropic}
	if len(s.Claude.OauthProjection) > 0 {
		owned.ClaudeOAuth = s.Claude.OauthProjection
	}
	if len(s.ClaudeIdentity.OauthAccount) > 0 || s.ClaudeIdentity.UserID != "" {
		fields := s.ClaudeIdentity
		owned.ClaudeIdentity = &fields
	}
	return owned
}

// AccountPool 是 Keychain envelope 的顶层结构（design §3.1）。
// 时间统一 RFC3339Nano（time.Time 默认编码）；ActiveAccountID 空串表示 null。
type AccountPool struct {
	SchemaVersion      int               `json:"schemaVersion"`
	Revision           uint64            `json:"revision"`
	ActiveAccountID    string            `json:"activeAccountId,omitempty"`
	ActiveGeneration   uint64            `json:"activeGeneration"`
	Accounts           map[string]*AccountSnapshot `json:"accounts"`
	Transaction        *PoolTransaction  `json:"transaction,omitempty"`
	RefreshIntent      *RefreshIntent    `json:"refreshIntent,omitempty"`
	LastSwitch         *LastSwitch       `json:"lastSwitch,omitempty"`
	ManualRespectUntil *time.Time        `json:"manualRespectUntil,omitempty"`
}

// NewAccountPool 创建一个从未启用过的空池（revision 0、无账号）。
func NewAccountPool() *AccountPool {
	return &AccountPool{
		SchemaVersion: PoolSchemaVersion,
		Accounts:      map[string]*AccountSnapshot{},
	}
}

// Clone 深拷贝池，避免把内部可变状态透出（design §3.2 的显式复制原则）。
func (p *AccountPool) Clone() (*AccountPool, error) {
	if p == nil {
		return nil, nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("clone pool: %w", err)
	}
	var out AccountPool
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("clone pool: %w", err)
	}
	if out.Accounts == nil {
		out.Accounts = map[string]*AccountSnapshot{}
	}
	return &out, nil
}

// Active 返回活动账号快照；无活动账号返回 nil。
func (p *AccountPool) Active() *AccountSnapshot {
	if p == nil || p.ActiveAccountID == "" {
		return nil
	}
	return p.Accounts[p.ActiveAccountID]
}

// Account 返回指定账号快照。
func (p *AccountPool) Account(id string) *AccountSnapshot {
	if p == nil {
		return nil
	}
	return p.Accounts[id]
}

// NewLocalAccountID 生成独立于邮箱与服务端 UUID 的本机账号 ID
// （design §3.1：crypto/rand 生成 UUID 形态）。
func NewLocalAccountID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate local account id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	const hexDigits = "0123456789abcdef"
	var sb strings.Builder
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			sb.WriteByte('-')
		}
		sb.WriteByte(hexDigits[v>>4])
		sb.WriteByte(hexDigits[v&0x0f])
	}
	return sb.String(), nil
}

// MaskEmail 把邮箱遮罩为仅供本机显示的形态（design §3.1：UI 默认遮罩）。
func MaskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		if email == "" {
			return ""
		}
		return "***"
	}
	local, domain := email[:at], email[at:]
	switch {
	case len(local) == 1:
		return local + "***" + domain
	case len(local) <= 3:
		return string(local[0]) + "***" + domain
	default:
		return local[:2] + "***" + domain
	}
}

// AccountDTO 是 credential-free 的账号投影（design §1.3-5：token、完整身份
// 快照、hash 不进 DTO；accountUuid/organizationUuid 留在 Keychain）。
type AccountDTO struct {
	ID              string        `json:"id"`
	Alias           string        `json:"alias"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
	EmailMasked     string        `json:"emailMasked,omitempty"`
	SeatTier        string        `json:"seatTier,omitempty"`
	Active          bool          `json:"active"`
	Health          AccountHealth `json:"health"`
	ClaudeExpiresAt *time.Time    `json:"claudeExpiresAt,omitempty"`
}

// ToDTO 把快照投影为无凭据 DTO。
func (s *AccountSnapshot) ToDTO(activeID string) AccountDTO {
	return AccountDTO{
		ID:              s.ID,
		Alias:           s.Alias,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		EmailMasked:     MaskEmail(s.Identity.Email),
		SeatTier:        s.Identity.SeatTier,
		Active:          s.ID == activeID,
		Health:          s.Health,
		ClaudeExpiresAt: s.Claude.ExpiresAt,
	}
}
