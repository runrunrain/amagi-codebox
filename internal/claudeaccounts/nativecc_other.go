//go:build !darwin || !cgo

package claudeaccounts

import "context"

// UnsupportedClaudeKeychain 覆盖非 darwin（及 darwin 无 cgo）平台：
// v1 macOS only（design §8），其余平台一律 UNSUPPORTED_PLATFORM。
type UnsupportedClaudeKeychain struct{}

// NewClaudeKeychainAccess 返回当前平台的访问器（本平台为 unsupported）。
func NewClaudeKeychainAccess() ClaudeKeychainAccess {
	return UnsupportedClaudeKeychain{}
}

// LocateItem 一律返回 UNSUPPORTED_PLATFORM。
func (UnsupportedClaudeKeychain) LocateItem(ctx context.Context) (*KeychainLocator, error) {
	_ = ctx
	return nil, NewErr(ReasonUnsupportedPlatform, "Claude Code Keychain 访问仅支持 macOS（cgo）")
}

// ReadItem 一律返回 UNSUPPORTED_PLATFORM。
func (UnsupportedClaudeKeychain) ReadItem(ctx context.Context, loc *KeychainLocator) ([]byte, error) {
	_, _ = ctx, loc
	return nil, NewErr(ReasonUnsupportedPlatform, "Claude Code Keychain 访问仅支持 macOS（cgo）")
}

// UpdateItem 一律返回 UNSUPPORTED_PLATFORM。
func (UnsupportedClaudeKeychain) UpdateItem(ctx context.Context, loc *KeychainLocator, item []byte) error {
	_, _, _ = ctx, loc, item
	return NewErr(ReasonUnsupportedPlatform, "Claude Code Keychain 访问仅支持 macOS（cgo）")
}
