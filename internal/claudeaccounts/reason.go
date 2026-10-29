// Package claudeaccounts 实现《Claude 订阅账号轮换》设计（agent-outputs/
// 2026-10-28-claude-account-rotation/design.md）的领域层：账号池 Keychain
// envelope 存储、原生凭据三处投影适配器、capture/enrollment/harvest/swap/
// reconcile 事务，以及 standby grant 的受控 OAuth 刷新与身份配对校验。
//
// 本包只做领域逻辑，不做 Wails facade、不做启动接线；共享 lease 与进程
// 观察器通过 MutationGate / QuiescenceChecker 接口注入（见 design §2、§6.3）。
//
// 敏感边界（design §3.3 / §1.3-5）：token、完整身份快照、凭据 hash、HTTP
// 原始响应一律不进入日志、错误消息或 DTO；日志只允许本机账号 ID 与 reason。
package claudeaccounts

import (
	"errors"
	"fmt"
)

// Reason 是对外稳定的错误码（design §7.2）。新增取值必须同步设计文档。
type Reason string

const (
	// ReasonUnsupportedPlatform 平台不支持本能力（非 macOS 的原生凭据访问等）。
	ReasonUnsupportedPlatform Reason = "UNSUPPORTED_PLATFORM"
	// ReasonPoolLocked 账号池跨进程锁被他人持有，或 Keychain 访问被锁定。
	ReasonPoolLocked Reason = "POOL_LOCKED"
	// ReasonPoolMissing 账号池从未创建（正常未启用）或已被删除。
	ReasonPoolMissing Reason = "POOL_MISSING"
	// ReasonPoolCorrupt 池 envelope 存在但不可解析（schema/JSON 损坏）。
	ReasonPoolCorrupt Reason = "POOL_CORRUPT"
	// ReasonRevisionConflict 调用方携带的期望 revision/generation 与池不符。
	ReasonRevisionConflict Reason = "REVISION_CONFLICT"
	// ReasonIdentityMismatch 双 grant 身份不属于同一账号，或与期望账号不符。
	ReasonIdentityMismatch Reason = "IDENTITY_MISMATCH"
	// ReasonIdentityUnverified 身份无法验证（端点失败、响应不可解析、未验证契约）。
	ReasonIdentityUnverified Reason = "IDENTITY_UNVERIFIED"
	// ReasonSessionsActive 受影响启动入口存在 pending/run lease（由注入的
	// MutationGate/QuiescenceChecker 判定，本包不自行实现 lease）。
	ReasonSessionsActive Reason = "SESSIONS_ACTIVE"
	// ReasonExternalSessionUncertain 存在不可分类的外部消费进程，写入者静止条件不成立。
	ReasonExternalSessionUncertain Reason = "EXTERNAL_SESSION_UNCERTAIN"
	// ReasonNativeLockBusy 原生短写锁（Pi proper-lockfile 兼容锁）被占用。
	ReasonNativeLockBusy Reason = "NATIVE_LOCK_BUSY"
	// ReasonExternalAccountChange 原生槽位读到了与活动账号不同的账号。
	ReasonExternalAccountChange Reason = "EXTERNAL_ACCOUNT_CHANGE"
	// ReasonNeedsReconcile 事务恢复无法按 before/after 分类，需人工 reconcile。
	ReasonNeedsReconcile Reason = "NEEDS_RECONCILE"
	// ReasonRefreshUncertain 刷新请求已发出但结果不可证明（响应丢失/取消/存池失败）。
	ReasonRefreshUncertain Reason = "REFRESH_UNCERTAIN"
	// ReasonNeedsLogin grant 已失效（invalid_grant），需重新授权。
	ReasonNeedsLogin Reason = "NEEDS_LOGIN"
	// ReasonUnsupportedCredentialSchema 原生凭据 schema 未知或含无法判明归属的字段。
	ReasonUnsupportedCredentialSchema Reason = "UNSUPPORTED_CREDENTIAL_SCHEMA"
)

// Error 是本包唯一的对外错误形态：稳定 reason code + 无敏感信息的 Message。
// Message 不得包含 token、完整身份快照、凭据 hash 或 HTTP 原文。
type Error struct {
	Reason  Reason
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		return string(e.Reason)
	}
	return string(e.Reason) + ": " + e.Message
}

// NewErr 构造领域错误。format 只允许步骤名、reason、本机账号 ID 等无敏感信息。
func NewErr(reason Reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// AsError 提取 *Error；非领域错误按原因归一为最接近的稳定码，
// 避免把底层 IO/HTTP 细节直接透出（敏感边界）。
func AsError(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	var domain *Error
	if errors.As(err, &domain) {
		return domain, true
	}
	return nil, false
}

// ReasonOf 返回错误的稳定 reason；未知错误归为 NeedsReconcile（保守停写）。
func ReasonOf(err error) Reason {
	if domain, ok := AsError(err); ok {
		return domain.Reason
	}
	return ReasonNeedsReconcile
}
