//go:build !darwin || !cgo

package claudeaccounts

import "context"

// UnsupportedPoolStore 覆盖非 darwin（及 darwin 无 cgo）平台：设计 §8 风险
// 表规定 v1 macOS only，Windows/WSL mutation 返回 unsupported，不回退明文。
type UnsupportedPoolStore struct{}

// NewKeychainPoolStore 在本平台返回 unsupported 后端（与 darwin 构造器同名，
// 保证调用方跨平台一致）。测试不得用它替代 fake。
func NewKeychainPoolStore() PoolStore {
	return UnsupportedPoolStore{}
}

// Kind 标识后端。
func (UnsupportedPoolStore) Kind() string { return "unsupported" }

// Load 一律返回 UNSUPPORTED_PLATFORM。
func (UnsupportedPoolStore) Load(ctx context.Context) (*AccountPool, error) {
	_ = ctx
	return nil, NewErr(ReasonUnsupportedPlatform, "账号池 Keychain 后端仅支持 macOS（cgo）")
}

// Save 一律返回 UNSUPPORTED_PLATFORM。
func (UnsupportedPoolStore) Save(ctx context.Context, pool *AccountPool) error {
	_, _ = ctx, pool
	return NewErr(ReasonUnsupportedPlatform, "账号池 Keychain 后端仅支持 macOS（cgo）")
}
