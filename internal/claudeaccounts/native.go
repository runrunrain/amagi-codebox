package claudeaccounts

import (
	"context"
	"encoding/json"

	"github.com/tidwall/gjson"
)

// NativeProjections 是一次 readStable 读到的三处受管投影与其 revision。
// ClaudeOAuthRaw 是 Claude Code Keychain 项的完整字节镜像（仅审计/恢复用，
// 不出包边界，design §3.1）。
type NativeProjections struct {
	ClaudeOAuth     json.RawMessage
	ClaudeOAuthRaw  json.RawMessage
	Locator         *KeychainLocator
	ClaudeIdentity  *ClaudeIdentityFields
	PiAnthropic     json.RawMessage
	Revisions       ExpectedRevisions
	// PiOtherProviders 报告 auth.json 中除 anthropic 外的其他顶层键名
	// （仅键名，不含值）：patch 前后必须保持一致（design §1.3-4）。
	PiOtherProviders []string
	// ClaudeJSONOtherTopKeys 报告 .claude.json 除受管两个字段外的顶层键名。
	ClaudeJSONOtherTopKeys []string
}

// NativeCredentialStore 是三处原生凭据位置的读写边界（design §4）：
// macOS Keychain `Claude Code-credentials`、`~/.claude.json` 的
// oauthAccount+userID、`~/.pi/agent/auth.json` 的 anthropic 键。
// 所有 Patch 内部必须完成写入 + 回读验证后才返回成功。
type NativeCredentialStore interface {
	// ReadStable 按 lstat→读→stat→hash→重读读取稳定快照（design §4.3）。
	ReadStable(ctx context.Context) (*NativeProjections, error)
	// PatchClaudeKeychainOAuth 更新 Keychain 项中的 OAuth 投影字段，保留
	// 未知顶层兄弟字段，回读验证后返回新的项 revision。
	PatchClaudeKeychainOAuth(ctx context.Context, oauth json.RawMessage) (*FileRevision, error)
	// PatchClaudeIdentity 字段级 patch `.claude.json` 的 oauthAccount 与
	// userID，其他字段字节片段保留，回读验证后返回新 revision。
	PatchClaudeIdentity(ctx context.Context, fields ClaudeIdentityFields) (*FileRevision, error)
	// PatchPiAnthropic 仅替换 auth.json 的 anthropic 键，其他 provider 条目
	// 原样保留，回读验证后返回新 revision。
	PatchPiAnthropic(ctx context.Context, anthropic json.RawMessage) (*FileRevision, error)
	// Kind 返回后端标识。
	Kind() string
}

// ClaudeKeychainServiceName 是 Claude Code 凭据项的 Keychain service
// （任务书给定）。locator 必须先查唯一匹配项并记录 account（design §3.1）。
const ClaudeKeychainServiceName = "Claude Code-credentials"

// ClaudeKeychainAdapterVersion 标记 Keychain OAuth 投影字段契约的适配器
// 版本。claudeAiOauth 结构为待验证的适配器契约（design §3.1），真机验证
// 通过前不得假定字段内部结构，只做整体投影搬运。
const ClaudeKeychainAdapterVersion = "cc-keychain-claudeAiOauth-v1"

// claudeOAuthProjectionField 是 Claude Code Keychain 项内 OAuth 投影的顶层
// 字段名（待真机契约验证，design §3.1）。
const claudeOAuthProjectionField = "claudeAiOauth"

// ExtractClaudeOAuthProjection 从 Keychain 项字节中定位 OAuth 投影。
// 项不是对象、缺投影字段、或投影不是 JSON 对象 → UNSUPPORTED_CREDENTIAL_SCHEMA
// （不做跨账号复制，design §3.1）。
func ExtractClaudeOAuthProjection(item []byte) (json.RawMessage, error) {
	if !gjson.ValidBytes(item) {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Keychain 项不是合法 JSON")
	}
	root := gjson.ParseBytes(item)
	if !root.IsObject() {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Keychain 项不是 JSON 对象")
	}
	field := root.Get(claudeOAuthProjectionField)
	if !field.Exists() || !field.IsObject() {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Keychain 项缺少可识别的 OAuth 投影字段")
	}
	// 入库采用规范形态：池 envelope 往返会 compact 化 RawMessage，统一
	// 形态避免格式差异被误判为内容变化。
	return canonicalJSON([]byte(field.Raw)), nil
}

// PatchClaudeKeychainItemBytes 在保留未知顶层兄弟字段的前提下替换 OAuth
// 投影字段，返回新的项字节。输入项缺投影字段时拒绝（schema 不可识别）。
func PatchClaudeKeychainItemBytes(item []byte, oauth json.RawMessage) (json.RawMessage, error) {
	if _, err := ExtractClaudeOAuthProjection(item); err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(oauth) || !gjson.ParseBytes(oauth).IsObject() {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "OAuth 投影必须是 JSON 对象")
	}
	next, err := sjsonSetRawBytes(item, claudeOAuthProjectionField, oauth)
	if err != nil {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Keychain 项投影替换失败")
	}
	return next, nil
}

// PiAnthropicEntry 是 Pi auth.json anthropic 条目的校验视图（design §3.1：
// 必须满足 type=oauth、非空 access/refresh、有限数值 expires；单位毫秒）。
type PiAnthropicEntry struct {
	Type    string  `json:"type"`
	Access  string  `json:"access"`
	Refresh string  `json:"refresh"`
	Expires float64 `json:"expires"`
}

// ValidatePiAnthropicEntry 校验 anthropic 条目结构；未知字段保留由 patch
// 语义保证，这里只验证设计 §3.1 的必要条件。
func ValidatePiAnthropicEntry(raw []byte) error {
	var entry PiAnthropicEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return NewErr(ReasonUnsupportedCredentialSchema, "Pi anthropic 条目不是合法 JSON")
	}
	if entry.Type != "oauth" {
		return NewErr(ReasonUnsupportedCredentialSchema, "Pi anthropic 条目 type 不是 oauth")
	}
	if entry.Access == "" || entry.Refresh == "" {
		return NewErr(ReasonUnsupportedCredentialSchema, "Pi anthropic 条目缺少 access/refresh")
	}
	if entry.Expires == 0 || entry.Expires != entry.Expires || entry.Expires > 1e15 {
		return NewErr(ReasonUnsupportedCredentialSchema, "Pi anthropic 条目 expires 非有限毫秒数值")
	}
	return nil
}
