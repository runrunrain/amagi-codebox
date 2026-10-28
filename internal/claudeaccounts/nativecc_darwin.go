//go:build darwin && cgo

package claudeaccounts

/*
Claude Code 原生凭据 Keychain 访问（design §3.1/§4）：
locator 先按 service 查唯一匹配项并记录 account；零项报缺失（未登录）、
多项报歧义，禁止更新第一个模糊结果、枚举整库或删除后重建以绕过 ACL
（design §3.1）。
*/

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// amagi_cc_locate 按 service 枚举 generic password 项的属性字典数组。
// 调用方负责 CFRelease(outAttrs)。*outCount 为匹配数（无匹配时为 0 且
// outAttrs 为 NULL）。
OSStatus amagi_cc_locate(const char* service, CFArrayRef* outAttrs, CFIndex* outCount) {
	CFStringRef svc = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	if (svc == NULL) { return errSecAllocate; }
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (query == NULL) { CFRelease(svc); return errSecAllocate; }
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, svc);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitAll);
	CFDictionarySetValue(query, kSecReturnAttributes, kCFBooleanTrue);
	*outAttrs = NULL;
	*outCount = 0;
	OSStatus status = SecItemCopyMatching((CFDictionaryRef)query, (CFTypeRef*)outAttrs);
	CFRelease(query);
	CFRelease(svc);
	if (status == errSecSuccess && *outAttrs != NULL) {
		*outCount = CFArrayGetCount(*outAttrs);
	}
	return status;
}

void amagi_cc_release(CFTypeRef ref) {
	if (ref != NULL) { CFRelease(ref); }
}

// amagi_cc_account_at 把第 idx 个属性字典的 kSecAttrAccount 拷贝到 out
// （容量 cap 字节）。返回是否取得非空 account。
Boolean amagi_cc_account_at(CFArrayRef attrs, CFIndex idx, char* out, CFIndex cap) {
	out[0] = '\0';
	CFDictionaryRef dict = (CFDictionaryRef)CFArrayGetValueAtIndex(attrs, idx);
	if (dict == NULL) { return false; }
	CFStringRef account = (CFStringRef)CFDictionaryGetValue(dict, kSecAttrAccount);
	if (account == NULL || CFGetTypeID(account) != CFStringGetTypeID()) { return false; }
	if (!CFStringGetCString(account, out, cap, kCFStringEncodingUTF8)) { return false; }
	return out[0] != '\0';
}

// amagi_cc_read 按 service+account 读取项内容（与 internal/secrets 同原语）。
OSStatus amagi_cc_read(const char* service, const char* account, void** data, UInt32* length) {
	return SecKeychainFindGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, length, data, NULL);
}

void amagi_cc_free(void* data) {
	if (data != NULL) { SecKeychainItemFreeContent(NULL, data); }
}

// amagi_cc_update 按 service+account 原位更新项内容（保留既有 ACL；
// design §4.4：使用更新现有 item 的 API，不 delete+add）。
OSStatus amagi_cc_update(const char* service, const char* account, const void* data, UInt32 length) {
	SecKeychainItemRef item = NULL;
	UInt32 existingLength = 0;
	void* existingData = NULL;
	OSStatus status = SecKeychainFindGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, &existingLength, &existingData, &item);
	if (status == errSecSuccess) {
		if (existingData != NULL) { SecKeychainItemFreeContent(NULL, existingData); }
		OSStatus updateStatus = SecKeychainItemModifyAttributesAndData(item, NULL, length, data);
		CFRelease(item);
		return updateStatus;
	}
	if (item != NULL) { CFRelease(item); }
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

// DarwinClaudeKeychain 是 Claude Code 凭据项的 macOS Keychain 访问实现。
type DarwinClaudeKeychain struct{}

// NewClaudeKeychainAccess 返回当前平台的 Claude Code Keychain 访问器。
func NewClaudeKeychainAccess() ClaudeKeychainAccess {
	return DarwinClaudeKeychain{}
}

// LocateItem 查 service 的唯一匹配项：零项报缺失（未登录）、多项报歧义
// （design §3.1）。
func (DarwinClaudeKeychain) LocateItem(ctx context.Context) (*KeychainLocator, error) {
	_ = ctx
	service := C.CString(ClaudeKeychainServiceName)
	defer C.free(unsafe.Pointer(service))

	var attrs C.CFArrayRef
	var count C.CFIndex
	status := C.amagi_cc_locate(service, &attrs, &count)
	if status != C.errSecSuccess {
		return nil, NewErr(ReasonNeedsReconcile, "Claude Code Keychain 查询被拒（osstatus=%d）", int(status))
	}
	defer C.amagi_cc_release(C.CFTypeRef(attrs))
	switch {
	case count == 0:
		return nil, NewErr(ReasonNeedsLogin, "Claude Code Keychain 凭据项不存在（未登录或已登出）")
	case count > 1:
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Code Keychain service 匹配 %d 项，无法唯一定位", int(count))
	}
	var buf [256]C.char
	if C.amagi_cc_account_at(attrs, 0, &buf[0], C.CFIndex(len(buf))) == 0 {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Code Keychain 匹配项缺少 account 属性")
	}
	account := C.GoString(&buf[0])
	return &KeychainLocator{Service: ClaudeKeychainServiceName, Account: account}, nil
}

// ReadItem 读取项完整字节。
func (DarwinClaudeKeychain) ReadItem(ctx context.Context, loc *KeychainLocator) ([]byte, error) {
	_ = ctx
	if loc == nil || loc.Account == "" {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Code Keychain locator 缺少 account")
	}
	service := C.CString(loc.Service)
	account := C.CString(loc.Account)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))

	var data unsafe.Pointer
	var length C.UInt32
	status := C.amagi_cc_read(service, account, (*unsafe.Pointer)(&data), &length)
	if status == C.errSecItemNotFound {
		return nil, NewErr(ReasonNeedsLogin, "Claude Code Keychain 凭据项不存在")
	}
	if status != C.errSecSuccess {
		return nil, NewErr(ReasonNeedsReconcile, "Claude Code Keychain 读取被拒（osstatus=%d）", int(status))
	}
	defer C.amagi_cc_free(data)
	if length == 0 {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Code Keychain 项内容为空")
	}
	return C.GoBytes(data, C.int(length)), nil
}

// UpdateItem 原位更新项内容（保留 ACL）。
func (DarwinClaudeKeychain) UpdateItem(ctx context.Context, loc *KeychainLocator, item []byte) error {
	_ = ctx
	if loc == nil || loc.Account == "" {
		return NewErr(ReasonUnsupportedCredentialSchema, "Claude Code Keychain locator 缺少 account")
	}
	service := C.CString(loc.Service)
	account := C.CString(loc.Account)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))

	cPayload := C.CBytes(item)
	defer C.free(cPayload)
	status := C.amagi_cc_update(service, account, cPayload, C.UInt32(len(item)))
	if status != C.errSecSuccess {
		return NewErr(ReasonNeedsReconcile, "Claude Code Keychain 更新被拒（osstatus=%d）", int(status))
	}
	return nil
}
