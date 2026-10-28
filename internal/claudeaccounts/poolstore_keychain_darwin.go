//go:build darwin && cgo

package claudeaccounts

/*
复用 internal/secrets 的 Security.framework 原语模式（store_darwin_cgo.go），
但使用独立的 Keychain service：amagi-codebox.claude-accounts / desktop-app，
与 provider 密钥表完全隔离（design §2「池存储」决策）。

Claude Code 原生凭据项的定位（locator 唯一匹配查询）在 nativecc_darwin.go。
*/

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// amagi_ca_find_pool 按 service+account 精确查找账号池 envelope 项。
OSStatus amagi_ca_find_pool(const char* service, const char* account, void** data, UInt32* length) {
	return SecKeychainFindGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, length, data, NULL);
}

void amagi_ca_free(void* data) {
	if (data != NULL) {
		SecKeychainItemFreeContent(NULL, data);
	}
}

// amagi_ca_set_pool 写入（存在则原位更新，保留既有 ACL；不存在则新增）。
// design §4.4：Keychain 使用更新现有 item 的 API，不 delete+add 绕过 ACL。
OSStatus amagi_ca_set_pool(const char* service, const char* account, const void* data, UInt32 length) {
	SecKeychainItemRef item = NULL;
	UInt32 existingLength = 0;
	void* existingData = NULL;
	OSStatus status = SecKeychainFindGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, &existingLength, &existingData, &item);
	if (status == errSecSuccess) {
		if (existingData != NULL) {
			SecKeychainItemFreeContent(NULL, existingData);
		}
		OSStatus updateStatus = SecKeychainItemModifyAttributesAndData(item, NULL, length, data);
		CFRelease(item);
		return updateStatus;
	}
	if (item != NULL) {
		CFRelease(item);
	}
	if (status == errSecItemNotFound) {
		return SecKeychainAddGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, length, data, NULL);
	}
	return status;
}
*/
import "C"

import (
	"context"
	"unsafe"
)

// 账号池专用 Keychain service（design §2：新 service amagi-codebox.claude-accounts）。
const (
	poolKeychainService = "amagi-codebox.claude-accounts"
	poolKeychainAccount = "desktop-app"
)

// KeychainPoolStore 是 macOS Keychain 上的池 envelope 后端。
type KeychainPoolStore struct{}

// NewKeychainPoolStore 返回真实 Keychain 后端。仅供生产接线使用；
// 测试一律注入 fake store，不得调用本构造器。
func NewKeychainPoolStore() PoolStore {
	return KeychainPoolStore{}
}

// Kind 标识后端。
func (KeychainPoolStore) Kind() string { return "keychain" }

// Load 读取池 envelope；从未创建返回 (nil, nil)。
func (KeychainPoolStore) Load(ctx context.Context) (*AccountPool, error) {
	_ = ctx
	service := C.CString(poolKeychainService)
	account := C.CString(poolKeychainAccount)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))

	var data unsafe.Pointer
	var length C.UInt32
	status := C.amagi_ca_find_pool(service, account, (*unsafe.Pointer)(&data), &length)
	if status == C.errSecItemNotFound {
		return nil, nil // 从未创建过池：正常未启用（design §3.3）
	}
	if status != C.errSecSuccess {
		// errSecAuthFailed / errSecInteractionNotAllowed 等访问被拒：
		// 不自动生成空池覆盖原生认证，交由上层停用并告警。
		return nil, NewErr(ReasonPoolLocked, "账号池 Keychain 读取被拒（osstatus=%d）", int(status))
	}
	defer C.amagi_ca_free(data)

	payload := C.GoBytes(data, C.int(length))
	if len(payload) == 0 {
		return nil, NewErr(ReasonPoolCorrupt, "账号池 Keychain 项为空")
	}
	pool, err := DecodePoolEnvelope(payload)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// Save 写入池 envelope 并立即回读验证 revision（design §3.3）。
func (KeychainPoolStore) Save(ctx context.Context, pool *AccountPool) error {
	_ = ctx
	if pool == nil {
		return NewErr(ReasonPoolCorrupt, "拒绝写入 nil 池")
	}
	payload, err := encodePoolEnvelope(pool)
	if err != nil {
		return NewErr(ReasonPoolCorrupt, "池 envelope 序列化失败")
	}
	service := C.CString(poolKeychainService)
	account := C.CString(poolKeychainAccount)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))

	cPayload := C.CBytes(payload)
	defer C.free(cPayload)
	status := C.amagi_ca_set_pool(service, account, cPayload, C.UInt32(len(payload)))
	if status != C.errSecSuccess {
		return NewErr(ReasonPoolLocked, "账号池 Keychain 写入被拒（osstatus=%d）", int(status))
	}

	// 回读验证 revision：保存成功必须可被再次读到同一版本。
	reloaded, err := KeychainPoolStore{}.Load(ctx)
	if err != nil {
		return NewErr(ReasonPoolCorrupt, "池保存后回读失败")
	}
	return verifySavedPool(reloaded, pool)
}
