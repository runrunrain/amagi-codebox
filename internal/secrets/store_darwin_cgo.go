//go:build darwin && cgo

package secrets

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

OSStatus amagi_find_generic_password(const char* service, const char* account, void** data, UInt32* length) {
	return SecKeychainFindGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, length, data, NULL);
}

void amagi_free_generic_password(void* data) {
	if (data != NULL) {
		SecKeychainItemFreeContent(NULL, data);
	}
}

OSStatus amagi_set_generic_password(const char* service, const char* account, const void* data, UInt32 length) {
	SecKeychainItemRef item = NULL;
	UInt32 existingLength = 0;
	void* existingData = NULL;
	OSStatus status = SecKeychainFindGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, &existingLength, &existingData, &item);
	if (status == errSecSuccess) {
		if (existingData != NULL) {
			SecKeychainItemFreeContent(NULL, existingData);
		}
		OSStatus updateStatus = SecKeychainItemModifyAttributesAndData(item, NULL, length, data);
		if (item != NULL) {
			CFRelease(item);
		}
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

OSStatus amagi_delete_generic_password(const char* service, const char* account) {
	SecKeychainItemRef item = NULL;
	UInt32 existingLength = 0;
	void* existingData = NULL;
	OSStatus status = SecKeychainFindGenericPassword(NULL, (UInt32)strlen(service), service, (UInt32)strlen(account), account, &existingLength, &existingData, &item);
	if (existingData != NULL) {
		SecKeychainItemFreeContent(NULL, existingData);
	}
	if (status == errSecItemNotFound) {
		if (item != NULL) {
			CFRelease(item);
		}
		return status;
	}
	if (status != errSecSuccess) {
		if (item != NULL) {
			CFRelease(item);
		}
		return status;
	}
	OSStatus deleteStatus = SecKeychainItemDelete(item);
	if (item != NULL) {
		CFRelease(item);
	}
	return deleteStatus;
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

type darwinSecretStore struct{}

const (
	darwinKeychainService = "amagi-codebox.providers"
	darwinKeychainAccount = "desktop-app"
)

func NewSecretStore() SecretStore {
	return darwinSecretStore{}
}

func (darwinSecretStore) Load(path string) (map[string]string, error) {
	_ = path
	service := C.CString(darwinKeychainService)
	account := C.CString(darwinKeychainAccount)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))

	var data unsafe.Pointer
	var length C.UInt32
	status := C.amagi_find_generic_password(service, account, (*unsafe.Pointer)(&data), &length)
	if status == C.errSecItemNotFound {
		return map[string]string{}, nil
	}
	if status != C.errSecSuccess {
		return nil, fmt.Errorf("read keychain secret store: osstatus=%d", int(status))
	}
	defer C.amagi_free_generic_password(data)

	payload := C.GoBytes(data, C.int(length))
	if len(payload) == 0 {
		return map[string]string{}, nil
	}
	var values map[string]string
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, fmt.Errorf("parse keychain secrets json: %w", err)
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}

func (darwinSecretStore) Save(path string, values map[string]string) error {
	_ = path
	trimmed := map[string]string{}
	for key, value := range values {
		if value != "" {
			trimmed[key] = value
		}
	}

	service := C.CString(darwinKeychainService)
	account := C.CString(darwinKeychainAccount)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))

	if len(trimmed) == 0 {
		status := C.amagi_delete_generic_password(service, account)
		if status == C.errSecItemNotFound || status == C.errSecSuccess {
			return nil
		}
		return fmt.Errorf("delete keychain secret store: osstatus=%d", int(status))
	}

	payload, err := json.Marshal(trimmed)
	if err != nil {
		return fmt.Errorf("marshal keychain secrets: %w", err)
	}
	cPayload := C.CBytes(payload)
	defer C.free(cPayload)
	status := C.amagi_set_generic_password(service, account, cPayload, C.UInt32(len(payload)))
	if status != C.errSecSuccess {
		return fmt.Errorf("write keychain secret store: osstatus=%d", int(status))
	}
	return nil
}

func (darwinSecretStore) Kind() string { return "keychain" }

func (darwinSecretStore) LegacyImportPath(path string) string { return path }
