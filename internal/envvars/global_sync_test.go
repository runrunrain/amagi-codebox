package envvars

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// fakePlatform 用于测试的全局环境变量操作 mock
type fakePlatform struct {
	vars         map[string]string // 模拟的 OS 用户级环境变量
	writes       []string          // 记录写入操作的 key
	deletes      []string          // 记录删除操作的 key
	broadcasts   int32             // 广播次数
	writeErr     error             // 模拟写入错误
	deleteErr    error             // 模拟删除错误
	readErr      error             // 模拟读取错误
	broadcastErr error             // 模拟广播错误
}

func newFakePlatform() *fakePlatform {
	return &fakePlatform{
		vars: make(map[string]string),
	}
}

func (f *fakePlatform) supportsGlobalSync() bool {
	return true
}

func (f *fakePlatform) readUserEnvVar(key string) (string, bool, error) {
	if f.readErr != nil {
		return "", false, f.readErr
	}
	// Windows 下 key 大小写不敏感
	if runtime.GOOS == "windows" {
		for k, v := range f.vars {
			if normalizeKey(k) == normalizeKey(key) {
				return v, true, nil
			}
		}
	} else {
		if v, ok := f.vars[key]; ok {
			return v, true, nil
		}
	}
	return "", false, nil
}

func (f *fakePlatform) writeUserEnvVar(key, value string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.vars[key] = value
	f.writes = append(f.writes, key)
	return nil
}

func (f *fakePlatform) deleteUserEnvVar(key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.vars, key)
	f.deletes = append(f.deletes, key)
	return nil
}

func (f *fakePlatform) broadcastEnvChange() error {
	atomic.AddInt32(&f.broadcasts, 1)
	if f.broadcastErr != nil {
		return f.broadcastErr
	}
	return nil
}

// --- 测试用例 ---

func TestGlobalSyncStatus_DefaultDisabled(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	status := svc.GetGlobalSyncStatus()
	if status.Enabled {
		t.Fatal("default should be disabled")
	}
	if status.ManagedCount != 0 {
		t.Fatalf("expected 0 managed keys, got %d", status.ManagedCount)
	}
	if len(status.ManagedKeys) != 0 {
		t.Fatalf("expected empty managed keys, got %v", status.ManagedKeys)
	}
}

func TestGlobalSync_EnableWritesAllVars(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 先设置几个变量
	if err := svc.Set("FOO", "bar"); err != nil {
		t.Fatalf("Set FOO: %v", err)
	}
	if err := svc.Set("BAZ", "qux"); err != nil {
		t.Fatalf("Set BAZ: %v", err)
	}

	// 开启全局同步
	status, err := svc.SetGlobalSyncEnabled(true)
	if err != nil {
		t.Fatalf("SetGlobalSyncEnabled(true): %v", err)
	}
	if !status.Enabled {
		t.Fatal("should be enabled")
	}
	if status.ManagedCount != 2 {
		t.Fatalf("expected 2 managed keys, got %d", status.ManagedCount)
	}

	// 验证 fakePlatform 中写入了变量
	if v, ok := fp.vars["FOO"]; !ok || v != "bar" {
		t.Fatalf("FOO not written to global env, got %q ok=%v", v, ok)
	}
	if v, ok := fp.vars["BAZ"]; !ok || v != "qux" {
		t.Fatalf("BAZ not written to global env, got %q ok=%v", v, ok)
	}

	// 验证备份已记录（key 不存在于 OS 前，existed=false）
	if svc.globalSyncBackups["FOO"].Existed {
		t.Fatal("FOO backup should show Existed=false")
	}
	if svc.globalSyncBackups["BAZ"].Existed {
		t.Fatal("BAZ backup should show Existed=false")
	}
}

func TestGlobalSync_EnableBacksUpExistingValues(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	// 模拟 OS 中已有 FOO 变量
	fp.vars["FOO"] = "original_value"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("FOO", "new_value"); err != nil {
		t.Fatalf("Set FOO: %v", err)
	}

	status, err := svc.SetGlobalSyncEnabled(true)
	if err != nil {
		t.Fatalf("SetGlobalSyncEnabled(true): %v", err)
	}
	if !status.Enabled {
		t.Fatal("should be enabled")
	}

	// 备份应记录原始值
	backup := svc.globalSyncBackups["FOO"]
	if !backup.Existed {
		t.Fatal("FOO backup should show Existed=true")
	}
	if backup.Value != "original_value" {
		t.Fatalf("FOO backup value = %q, want %q", backup.Value, "original_value")
	}

	// 全局环境变量应更新为新值
	if v := fp.vars["FOO"]; v != "new_value" {
		t.Fatalf("global FOO = %q, want %q", v, "new_value")
	}
}

func TestGlobalSync_DisableRestoresBackups(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	// 模拟 OS 中已有 FOO 变量
	fp.vars["FOO"] = "original_foo"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("FOO", "managed_foo"); err != nil {
		t.Fatalf("Set FOO: %v", err)
	}
	if err := svc.Set("NEW_VAR", "new_val"); err != nil {
		t.Fatalf("Set NEW_VAR: %v", err)
	}

	// 开启
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 关闭
	status, err := svc.SetGlobalSyncEnabled(false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if status.Enabled {
		t.Fatal("should be disabled after disable")
	}
	if status.ManagedCount != 0 {
		t.Fatalf("expected 0 managed keys after disable, got %d", status.ManagedCount)
	}

	// FOO 应恢复为原始值
	if v := fp.vars["FOO"]; v != "original_foo" {
		t.Fatalf("FOO after disable = %q, want %q", v, "original_foo")
	}
	// NEW_VAR 应被删除（它原来不存在）
	if _, ok := fp.vars["NEW_VAR"]; ok {
		t.Fatal("NEW_VAR should be deleted from global env")
	}
}

func TestGlobalSync_SetReconcilesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 先开启同步
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Set 新变量
	if err := svc.Set("ADDED", "added_val"); err != nil {
		t.Fatalf("Set ADDED: %v", err)
	}

	if v := fp.vars["ADDED"]; v != "added_val" {
		t.Fatalf("ADDED not synced to global env, got %q", v)
	}

	// Set 更新变量
	if err := svc.Set("ADDED", "updated_val"); err != nil {
		t.Fatalf("Set ADDED update: %v", err)
	}

	if v := fp.vars["ADDED"]; v != "updated_val" {
		t.Fatalf("ADDED not updated in global env, got %q", v)
	}

	// 更新不应覆盖原始 backup
	backup := svc.globalSyncBackups["ADDED"]
	if backup.Existed {
		t.Fatal("ADDED backup should still show Existed=false after update")
	}
}

func TestGlobalSync_DeleteReconcilesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	fp.vars["EXISTING"] = "os_original"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("EXISTING", "managed"); err != nil {
		t.Fatalf("Set EXISTING: %v", err)
	}
	if err := svc.Set("NEWONE", "new"); err != nil {
		t.Fatalf("Set NEWONE: %v", err)
	}

	// 开启同步
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 删除一个原来存在于 OS 的变量
	if err := svc.Delete("EXISTING"); err != nil {
		t.Fatalf("Delete EXISTING: %v", err)
	}

	// 应恢复为 OS 原值
	if v := fp.vars["EXISTING"]; v != "os_original" {
		t.Fatalf("EXISTING should be restored to %q, got %q", "os_original", v)
	}

	// 删除一个原来不存在于 OS 的变量
	if err := svc.Delete("NEWONE"); err != nil {
		t.Fatalf("Delete NEWONE: %v", err)
	}

	// 应从全局环境中删除
	if _, ok := fp.vars["NEWONE"]; ok {
		t.Fatal("NEWONE should be deleted from global env")
	}
}

func TestGlobalSync_ImportReconcilesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	fp.vars["EXISTING"] = "os_original"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("EXISTING", "managed"); err != nil {
		t.Fatalf("Set EXISTING: %v", err)
	}
	if err := svc.Set("TO_REMOVE", "will_be_removed"); err != nil {
		t.Fatalf("Set TO_REMOVE: %v", err)
	}

	// 开启同步
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Import 全量替换：只保留 EXISTING（新值），新增 IMPORTED
	importJSON := `{"envvars": [{"key": "EXISTING", "value": "imported_val"}, {"key": "IMPORTED", "value": "imported_new"}]}`
	if err := svc.Import(importJSON); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// EXISTING 应更新为导入值
	if v := fp.vars["EXISTING"]; v != "imported_val" {
		t.Fatalf("EXISTING = %q, want %q", v, "imported_val")
	}
	// EXISTING 的 backup 不应改变（仍然是 os_original）
	backup := svc.globalSyncBackups["EXISTING"]
	if !backup.Existed || backup.Value != "os_original" {
		t.Fatalf("EXISTING backup should be unchanged: %+v", backup)
	}

	// IMPORTED 应写入
	if v := fp.vars["IMPORTED"]; v != "imported_new" {
		t.Fatalf("IMPORTED = %q, want %q", v, "imported_new")
	}

	// TO_REMOVE 应从全局环境中恢复/删除
	if _, ok := fp.vars["TO_REMOVE"]; ok {
		t.Fatal("TO_REMOVE should be removed from global env after import")
	}
}

func TestGlobalSync_SaveJSONReconcilesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("A", "1"); err != nil {
		t.Fatalf("Set A: %v", err)
	}

	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// SaveJSON（等同于 Import）
	saveJSON := `{"envvars": [{"key": "B", "value": "2"}]}`
	if err := svc.SaveJSON(saveJSON); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	if v := fp.vars["B"]; v != "2" {
		t.Fatalf("B = %q, want %q", v, "2")
	}
	if _, ok := fp.vars["A"]; ok {
		t.Fatal("A should be removed from global env after SaveJSON")
	}
}

func TestGlobalSync_BatchSetReconcilesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("OLD", "old_val"); err != nil {
		t.Fatalf("Set OLD: %v", err)
	}

	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// BatchSet 全量替换
	if err := svc.BatchSet([]EnvVar{{Key: "NEW", Value: "new_val"}}); err != nil {
		t.Fatalf("BatchSet: %v", err)
	}

	if v := fp.vars["NEW"]; v != "new_val" {
		t.Fatalf("NEW = %q, want %q", v, "new_val")
	}
	if _, ok := fp.vars["OLD"]; ok {
		t.Fatal("OLD should be removed from global env after BatchSet")
	}
}

func TestGlobalSync_EnableFailureRollback(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("FOO", "bar"); err != nil {
		t.Fatalf("Set FOO: %v", err)
	}

	// 模拟写入失败
	fp.writeErr = fmt.Errorf("simulated write failure")

	// 开启同步应失败
	status, err := svc.SetGlobalSyncEnabled(true)
	if err == nil {
		t.Fatal("expected error when enable fails")
	}

	// 关键断言：失败后状态必须是 disabled
	if status.Enabled {
		t.Fatal("enabled should remain false after enable failure")
	}
	if status.ManagedCount != 0 {
		t.Fatalf("expected 0 managed keys after enable failure, got %d", status.ManagedCount)
	}

	// 内存中 globalSyncEnabled 应为 false
	if svc.globalSyncEnabled {
		t.Fatal("globalSyncEnabled should remain false in memory")
	}
	if len(svc.globalSyncManagedKeys) != 0 {
		t.Fatalf("expected 0 managed keys in memory, got %d", len(svc.globalSyncManagedKeys))
	}
	if len(svc.globalSyncBackups) != 0 {
		t.Fatalf("expected 0 backups in memory, got %d", len(svc.globalSyncBackups))
	}

	// envVars 数据应不受影响
	if v, ok := svc.Get("FOO"); !ok || v != "bar" {
		t.Fatalf("FOO should still be in envVars, got %q ok=%v", v, ok)
	}
}

func TestGlobalSync_EnableReadFailureRollback(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("FOO", "bar"); err != nil {
		t.Fatalf("Set FOO: %v", err)
	}

	// 模拟读取失败（backup 阶段）
	fp.readErr = fmt.Errorf("simulated read failure")

	status, err := svc.SetGlobalSyncEnabled(true)
	if err == nil {
		t.Fatal("expected error when read fails during enable")
	}
	if status.Enabled {
		t.Fatal("enabled should remain false after read failure")
	}
	if svc.globalSyncEnabled {
		t.Fatal("globalSyncEnabled should remain false")
	}
}

func TestGlobalSync_EnableBroadcastFailureStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("FOO", "bar"); err != nil {
		t.Fatalf("Set FOO: %v", err)
	}

	// 模拟广播失败
	fp.broadcastErr = fmt.Errorf("simulated broadcast failure")

	// 开启同步应成功（broadcast failure 不作为事务失败，OS 写入已成功）
	status, err := svc.SetGlobalSyncEnabled(true)
	if err != nil {
		t.Fatalf("SetGlobalSyncEnabled(true) should succeed when only broadcast fails: %v", err)
	}

	// 状态应为 enabled
	if !status.Enabled {
		t.Fatal("enabled should be true after enable with broadcast failure")
	}
	if !svc.globalSyncEnabled {
		t.Fatal("globalSyncEnabled should be true in memory")
	}

	// managedKeys 应包含 FOO
	if status.ManagedCount != 1 {
		t.Fatalf("expected 1 managed key, got %d", status.ManagedCount)
	}

	// backups 应包含 FOO
	if _, ok := svc.globalSyncBackups["FOO"]; !ok {
		t.Fatal("backups should contain FOO")
	}

	// fake platform 应已写入 FOO（OS 写入成功）
	if v, ok := fp.vars["FOO"]; !ok || v != "bar" {
		t.Fatalf("fake platform should have FOO=bar, got %q ok=%v", v, ok)
	}
}

func TestGlobalSync_SetSyncFailureRollback(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 先开启同步（不设变量，空 reconcile）
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 模拟写入失败
	fp.writeErr = fmt.Errorf("simulated write failure")

	// Set 应失败
	if err := svc.Set("NEW_VAR", "new_val"); err == nil {
		t.Fatal("expected error when sync fails during Set")
	}

	// 内存中 envVars 应回滚，不包含 NEW_VAR
	if _, ok := svc.Get("NEW_VAR"); ok {
		t.Fatal("NEW_VAR should not be in envVars after sync failure rollback")
	}
}

func TestGlobalSync_DeleteSyncFailureRollback(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	fp.vars["EXISTING"] = "os_original"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("EXISTING", "managed"); err != nil {
		t.Fatalf("Set EXISTING: %v", err)
	}

	// 开启同步
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 模拟写入失败（恢复原值时失败）
	fp.writeErr = fmt.Errorf("simulated write failure")

	// Delete 应失败
	if err := svc.Delete("EXISTING"); err == nil {
		t.Fatal("expected error when sync fails during Delete")
	}

	// 内存中 envVars 应回滚，仍包含 EXISTING
	if v, ok := svc.Get("EXISTING"); !ok || v != "managed" {
		t.Fatalf("EXISTING should still be in envVars after sync failure rollback, got %q ok=%v", v, ok)
	}
}

func TestGlobalSync_ImportSyncFailureRollback(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("A", "1"); err != nil {
		t.Fatalf("Set A: %v", err)
	}

	// 开启同步
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 模拟写入失败
	fp.writeErr = fmt.Errorf("simulated write failure")

	// Import 应失败
	importJSON := `{"envVars": [{"key": "B", "value": "2"}]}`
	if err := svc.Import(importJSON); err == nil {
		t.Fatal("expected error when sync fails during Import")
	}

	// 内存中 envVars 应回滚，仍包含 A
	if v, ok := svc.Get("A"); !ok || v != "1" {
		t.Fatalf("A should still be in envVars after import failure rollback, got %q ok=%v", v, ok)
	}
	if _, ok := svc.Get("B"); ok {
		t.Fatal("B should not be in envVars after import failure rollback")
	}
}

func TestGlobalSync_BatchSetSyncFailureRollback(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("OLD", "old_val"); err != nil {
		t.Fatalf("Set OLD: %v", err)
	}

	// 开启同步
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 模拟写入失败
	fp.writeErr = fmt.Errorf("simulated write failure")

	// BatchSet 应失败
	if err := svc.BatchSet([]EnvVar{{Key: "NEW", Value: "new_val"}}); err == nil {
		t.Fatal("expected error when sync fails during BatchSet")
	}

	// 内存中 envVars 应回滚，仍包含 OLD
	if v, ok := svc.Get("OLD"); !ok || v != "old_val" {
		t.Fatalf("OLD should still be in envVars after batch set failure rollback, got %q ok=%v", v, ok)
	}
	if _, ok := svc.Get("NEW"); ok {
		t.Fatal("NEW should not be in envVars after batch set failure rollback")
	}
}

func TestGlobalSync_BatchSetValidationRejectsInvalidKeys(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 空 key
	if err := svc.BatchSet([]EnvVar{{Key: "", Value: "v"}}); err == nil {
		t.Fatal("BatchSet should reject empty key")
	}

	// 含 = 的 key
	if err := svc.BatchSet([]EnvVar{{Key: "K=V", Value: "v"}}); err == nil {
		t.Fatal("BatchSet should reject key with =")
	}

	// 含 NUL 的 key
	if err := svc.BatchSet([]EnvVar{{Key: "K\x00V", Value: "v"}}); err == nil {
		t.Fatal("BatchSet should reject key with NUL")
	}

	// 含换行的 key
	if err := svc.BatchSet([]EnvVar{{Key: "K\nV", Value: "v"}}); err == nil {
		t.Fatal("BatchSet should reject key with newline")
	}

	// 含回车的 key
	if err := svc.BatchSet([]EnvVar{{Key: "K\rV", Value: "v"}}); err == nil {
		t.Fatal("BatchSet should reject key with carriage return")
	}
}

func TestGlobalSync_BatchSetRejectsDuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 完全相同的 key
	if err := svc.BatchSet([]EnvVar{
		{Key: "FOO", Value: "1"},
		{Key: "FOO", Value: "2"},
	}); err == nil {
		t.Fatal("BatchSet should reject duplicate keys")
	}

	// Windows 大小写不同但 normalize 后重复
	if runtime.GOOS == "windows" {
		if err := svc.BatchSet([]EnvVar{
			{Key: "foo", Value: "1"},
			{Key: "FOO", Value: "2"},
		}); err == nil {
			t.Fatal("BatchSet should reject case-insensitive duplicate keys on Windows")
		}
	}
}

func TestGlobalSync_ImportValidationRejectsInvalidKeys(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 空 key
	if err := svc.Import(`{"envVars": [{"key": "", "value": "v"}]}`); err == nil {
		t.Fatal("Import should reject empty key")
	}

	// 含 = 的 key
	if err := svc.Import(`{"envVars": [{"key": "K=V", "value": "v"}]}`); err == nil {
		t.Fatal("Import should reject key with =")
	}

	// 重复 key
	if err := svc.Import(`{"envVars": [{"key": "A", "value": "1"}, {"key": "A", "value": "2"}]}`); err == nil {
		t.Fatal("Import should reject duplicate keys")
	}
}

func TestGlobalSync_SaveJSONValidationRejectsInvalidKeys(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// SaveJSON 等同于 Import，应同样校验
	if err := svc.SaveJSON(`{"envVars": [{"key": "", "value": "v"}]}`); err == nil {
		t.Fatal("SaveJSON should reject empty key")
	}
}

func TestGlobalSync_OldJSONCompatibility(t *testing.T) {
	dir := t.TempDir()

	// 测试旧格式 JSON 使用 "envVars"（当前 schema）
	configPath := filepath.Join(dir, "envvars.json")
	oldJSON := `{"envVars": [{"key": "FOO", "value": "bar"}]}`
	if err := os.WriteFile(configPath, []byte(oldJSON), 0o644); err != nil {
		t.Fatalf("write old json: %v", err)
	}

	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 应正常加载旧数据
	if v, ok := svc.Get("FOO"); !ok || v != "bar" {
		t.Fatalf("FOO after load old format: %q %v", v, ok)
	}

	// 全局同步应为默认关闭
	status := svc.GetGlobalSyncStatus()
	if status.Enabled {
		t.Fatal("global sync should be disabled by default for old format")
	}
	if status.ManagedCount != 0 {
		t.Fatalf("expected 0 managed keys, got %d", status.ManagedCount)
	}

	// 开启同步后保存，再加载验证新格式
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 重新加载
	svc2 := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load2: %v", err)
	}
	status2 := svc2.GetGlobalSyncStatus()
	if !status2.Enabled {
		t.Fatal("global sync should persist as enabled")
	}
}

func TestGlobalSync_OldJSONLowercaseCompatibility(t *testing.T) {
	// Go json.Unmarshal 支持大小写不敏感匹配
	// 旧代码使用 json:"envvars"，验证 "envvars" 能正确加载到 "envVars" 字段
	dir := t.TempDir()
	configPath := filepath.Join(dir, "envvars.json")
	lowercaseJSON := `{"envvars": [{"key": "LEGACY", "value": "old_format"}]}`
	if err := os.WriteFile(configPath, []byte(lowercaseJSON), 0o644); err != nil {
		t.Fatalf("write lowercase json: %v", err)
	}

	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if v, ok := svc.Get("LEGACY"); !ok || v != "old_format" {
		t.Fatalf("LEGACY from lowercase json tag: %q %v", v, ok)
	}
}

func TestGlobalSync_PersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("A", "1"); err != nil {
		t.Fatalf("Set A: %v", err)
	}
	if err := svc.Set("B", "2"); err != nil {
		t.Fatalf("Set B: %v", err)
	}

	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 验证持久化文件包含全局同步字段
	configPath := filepath.Join(dir, "envvars.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var f envVarsFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !f.GlobalSyncEnabled {
		t.Fatal("persisted GlobalSyncEnabled should be true")
	}
	if len(f.GlobalSyncManagedKeys) != 2 {
		t.Fatalf("expected 2 managed keys in file, got %d", len(f.GlobalSyncManagedKeys))
	}
	if len(f.GlobalSyncBackups) != 2 {
		t.Fatalf("expected 2 backups in file, got %d", len(f.GlobalSyncBackups))
	}
}

func TestGlobalSync_MergeWithSystemUnaffected(t *testing.T) {
	// 验证 MergeWithSystem 在全局同步开启后仍然正常工作
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	testKey := "GLOBAL_SYNC_MERGE_TEST"
	if err := svc.Set(testKey, "test_value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// 开启全局同步
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// MergeWithSystem 应仍然工作
	merged := svc.MergeWithSystem()
	found := false
	for _, kv := range merged {
		if kv == testKey+"=test_value" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("custom var not found in MergeWithSystem after enabling global sync")
	}
}

func TestGlobalSync_EnableIdempotent(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("FOO", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// 开启两次
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable 1: %v", err)
	}
	broadcastCount1 := atomic.LoadInt32(&fp.broadcasts)

	status, err := svc.SetGlobalSyncEnabled(true)
	if err != nil {
		t.Fatalf("enable 2: %v", err)
	}
	if !status.Enabled {
		t.Fatal("should still be enabled")
	}
	broadcastCount2 := atomic.LoadInt32(&fp.broadcasts)

	// 第二次调用不应触发额外的写入操作（状态未变化）
	if broadcastCount2 != broadcastCount1 {
		t.Fatalf("idempotent enable should not trigger extra operations: broadcasts before=%d after=%d", broadcastCount1, broadcastCount2)
	}
}

func TestGlobalSync_DisableIdempotent(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 关闭（未开启时）应直接返回
	status, err := svc.SetGlobalSyncEnabled(false)
	if err != nil {
		t.Fatalf("disable when not enabled: %v", err)
	}
	if status.Enabled {
		t.Fatal("should remain disabled")
	}
}

func TestGlobalSync_ExportDoesNotLeakBackupValues(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	fp.vars["SENSITIVE"] = "secret_original"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("SENSITIVE", "managed_value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Export 只应包含 envvars，不应暴露 backup 值
	exported, err := svc.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if contains := containsStr(exported, "secret_original"); contains {
		t.Fatal("Export should not contain backup value 'secret_original'")
	}
}

func TestGlobalSync_NonWindowsUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this test verifies non-Windows behavior")
	}

	dir := t.TempDir()
	svc := NewEnvVarsService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	status := svc.GetGlobalSyncStatus()
	if status.Supported {
		t.Fatal("non-Windows should report unsupported")
	}

	_, err := svc.SetGlobalSyncEnabled(true)
	if err == nil {
		t.Fatal("expected error when enabling on non-Windows")
	}
}

func TestGlobalSync_KeyValidation(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 空 key
	if err := svc.Set("", "value"); err == nil {
		t.Fatal("expected error for empty key")
	}

	// 含 = 的 key
	if err := svc.Set("KEY=VAL", "value"); err == nil {
		t.Fatal("expected error for key with =")
	}

	// 含 NUL 的 key
	if err := svc.Set("KEY\x00VAL", "value"); err == nil {
		t.Fatal("expected error for key with NUL")
	}

	// 含换行的 key
	if err := svc.Set("KEY\nVAL", "value"); err == nil {
		t.Fatal("expected error for key with newline")
	}
}

func TestGlobalSync_PartialDisableFailure(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	// A 原来存在于 OS，B 不存在
	// 关闭时 A 需要恢复原值（writeUserEnvVar），B 需要删除（deleteUserEnvVar）
	fp.vars["A"] = "os_original_a"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("A", "1"); err != nil {
		t.Fatalf("Set A: %v", err)
	}
	if err := svc.Set("B", "2"); err != nil {
		t.Fatalf("Set B: %v", err)
	}

	// 开启
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 模拟删除失败（B 关闭时需要 deleteUserEnvVar，A 需要恢复用 writeUserEnvVar）
	fp.deleteErr = fmt.Errorf("simulated delete failure")

	// 关闭应返回错误
	status, err := svc.SetGlobalSyncEnabled(false)
	if err == nil {
		t.Fatal("expected error on partial disable failure")
	}

	// A 应该已恢复成功（从 managed 中移除），B 仍保留
	// 由于部分失败，enabled 仍为 true
	if !status.Enabled {
		t.Fatal("enabled should remain true on partial disable failure")
	}
	if status.ManagedCount != 1 {
		t.Fatalf("expected 1 managed key (B) after partial disable, got %d", status.ManagedCount)
	}
	// B 应仍在 managedKeys 中
	if len(svc.globalSyncManagedKeys) != 1 || svc.globalSyncManagedKeys[0] != "B" {
		t.Fatalf("expected managedKeys to contain only B, got %v", svc.globalSyncManagedKeys)
	}
	// B 的 backup 应保留
	if _, ok := svc.globalSyncBackups["B"]; !ok {
		t.Fatal("B backup should be preserved for retry")
	}
	// A 应已恢复为 OS 原值
	if v := fp.vars["A"]; v != "os_original_a" {
		t.Fatalf("A should be restored to original, got %q", v)
	}
}

func TestGlobalSync_BroadcastCount(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("A", "1"); err != nil {
		t.Fatalf("Set A: %v", err)
	}
	if err := svc.Set("B", "2"); err != nil {
		t.Fatalf("Set B: %v", err)
	}
	if err := svc.Set("C", "3"); err != nil {
		t.Fatalf("Set C: %v", err)
	}

	// 开启同步（reconcile 批量操作应只广播一次）
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	bc := atomic.LoadInt32(&fp.broadcasts)
	if bc != 1 {
		t.Fatalf("expected 1 broadcast after enable, got %d", bc)
	}
}

func TestGlobalSync_ReconcileOnLoad(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()

	// 创建服务并开启同步，设置变量
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.Set("A", "1"); err != nil {
		t.Fatalf("Set A: %v", err)
	}
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 修改 OS 环境为不同的值（模拟外部修改）
	fp.vars["A"] = "external_modified"

	// 重新加载，应 reconcile 回配置值
	svc2 := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load2: %v", err)
	}

	// reconcile 应将 A 写回为 "1"
	if v := fp.vars["A"]; v != "1" {
		t.Fatalf("after reconcile on load, A = %q, want %q", v, "1")
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key string
		ok  bool
	}{
		{"FOO", true},
		{"", true}, // 空 key 由调用方校验
		{"A=B", false},
		{"A\x00B", false},
		{"A\nB", false},
		{"A\rB", false}, // 回车也应拒绝
		{"NORMAL_KEY_123", true},
	}
	for _, tt := range tests {
		err := validateKey(tt.key)
		if (err == nil) != tt.ok {
			t.Errorf("validateKey(%q) = %v, want ok=%v", tt.key, err, tt.ok)
		}
	}
}

func TestValidateEnvVars(t *testing.T) {
	tests := []struct {
		name string
		vars []EnvVar
		ok   bool
	}{
		{
			name: "valid single",
			vars: []EnvVar{{Key: "FOO", Value: "bar"}},
			ok:   true,
		},
		{
			name: "valid multiple",
			vars: []EnvVar{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}},
			ok:   true,
		},
		{
			name: "empty key",
			vars: []EnvVar{{Key: "", Value: "v"}},
			ok:   false,
		},
		{
			name: "key with equals",
			vars: []EnvVar{{Key: "K=V", Value: "v"}},
			ok:   false,
		},
		{
			name: "duplicate keys",
			vars: []EnvVar{{Key: "FOO", Value: "1"}, {Key: "FOO", Value: "2"}},
			ok:   false,
		},
		{
			name: "nil slice",
			vars: nil,
			ok:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvVars(tt.vars)
			if (err == nil) != tt.ok {
				t.Errorf("validateEnvVars(%v) = %v, want ok=%v", tt.vars, err, tt.ok)
			}
		})
	}
}

func TestValidateEnvVars_CaseInsensitiveDuplicate(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive key check only applies on Windows")
	}
	vars := []EnvVar{
		{Key: "foo", Value: "1"},
		{Key: "FOO", Value: "2"},
	}
	if err := validateEnvVars(vars); err == nil {
		t.Fatal("should reject case-insensitive duplicate keys on Windows")
	}
}

func TestDeleteUserEnvVar_KeyNotFoundIsSuccess(t *testing.T) {
	// 使用 fakePlatform 验证：当 key 不存在时，delete 应按成功处理
	// 此测试验证 service 层面的行为而非 Windows reg 命令
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("TO_DELETE", "val"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// 从 OS 环境中手动删除 key（模拟外部已删除）
	delete(fp.vars, "TO_DELETE")

	// 现在从 envVars 中删除，removeSyncKeyLocked 应调用 deleteUserEnvVar
	// fakePlatform 的 delete 不存在的 key 不会报错
	if err := svc.Delete("TO_DELETE"); err != nil {
		t.Fatalf("Delete key that was externally removed should succeed: %v", err)
	}

	// 验证 managed 集合已清理
	status := svc.GetGlobalSyncStatus()
	if status.ManagedCount != 0 {
		t.Fatalf("expected 0 managed keys after deleting externally-removed key, got %d", status.ManagedCount)
	}
}

func TestNormalizeKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		if normalizeKey("foo") != "FOO" {
			t.Fatal("Windows should uppercase keys")
		}
		if normalizeKey("Path") != "PATH" {
			t.Fatal("Windows should uppercase Path")
		}
	} else {
		if normalizeKey("foo") != "foo" {
			t.Fatal("Non-Windows should not change case")
		}
	}
}

// TestGlobalSync_SetBroadcastFailureStillSucceeds verifies: Set new key + broadcastErr
// => Set returns nil; envVars has new key; managed/backups have new key; fake platform has new key.
func TestGlobalSync_SetBroadcastFailureStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Enable sync (empty envVars, reconcile is no-op)
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Simulate broadcast failure
	fp.broadcastErr = fmt.Errorf("simulated broadcast failure")

	// Set new key should succeed (broadcast failure is non-blocking)
	if err := svc.Set("NEWKEY", "newval"); err != nil {
		t.Fatalf("Set should succeed when only broadcast fails: %v", err)
	}

	// envVars should contain NEWKEY
	if v, ok := svc.Get("NEWKEY"); !ok || v != "newval" {
		t.Fatalf("NEWKEY should be in envVars, got %q ok=%v", v, ok)
	}

	// managedKeys should contain NEWKEY
	foundManaged := false
	for _, mk := range svc.globalSyncManagedKeys {
		if mk == normalizeKey("NEWKEY") {
			foundManaged = true
			break
		}
	}
	if !foundManaged {
		t.Fatal("managedKeys should contain NEWKEY after Set with broadcast failure")
	}

	// backups should contain NEWKEY
	if _, ok := svc.globalSyncBackups[normalizeKey("NEWKEY")]; !ok {
		t.Fatal("backups should contain NEWKEY after Set with broadcast failure")
	}

	// fake platform should have NEWKEY=newval (OS write succeeded)
	if v, ok := fp.vars["NEWKEY"]; !ok || v != "newval" {
		t.Fatalf("fake platform should have NEWKEY=newval, got %q ok=%v", v, ok)
	}
}

// TestGlobalSync_SetWriteFailureNoBackupResidue verifies: Set new key + writeErr
// => failure, backup must not be left behind.
func TestGlobalSync_SetWriteFailureNoBackupResidue(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Enable sync (empty envVars, reconcile is no-op)
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Simulate write failure (happens after backup read but during write)
	fp.writeErr = fmt.Errorf("simulated write failure")

	// Set new key should fail
	if err := svc.Set("ANOTHER", "val"); err == nil {
		t.Fatal("expected error when write fails during Set")
	}

	// envVars should be rolled back
	if _, ok := svc.Get("ANOTHER"); ok {
		t.Fatal("ANOTHER should not be in envVars after write failure rollback")
	}

	// backups should be rolled back: no residue
	if _, ok := svc.globalSyncBackups[normalizeKey("ANOTHER")]; ok {
		t.Fatal("backups should not contain ANOTHER after write failure rollback (no residue)")
	}

	// managedKeys should be rolled back
	for _, mk := range svc.globalSyncManagedKeys {
		if mk == normalizeKey("ANOTHER") {
			t.Fatal("managedKeys should not contain ANOTHER after write failure rollback")
		}
	}
}

// TestGlobalSync_DeleteBroadcastFailureStillSucceeds verifies: Delete managed key + broadcastErr
// => Delete returns nil; envVars deleted; managed/backups deleted; fake platform restored to os_original.
func TestGlobalSync_DeleteBroadcastFailureStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	fp := newFakePlatform()
	fp.vars["MYKEY"] = "os_original"

	svc := NewEnvVarsServiceWithPlatform(dir, fp)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := svc.Set("MYKEY", "managed"); err != nil {
		t.Fatalf("Set MYKEY: %v", err)
	}

	// Enable sync
	if _, err := svc.SetGlobalSyncEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Verify MYKEY is managed
	foundManaged := false
	for _, mk := range svc.globalSyncManagedKeys {
		if mk == normalizeKey("MYKEY") {
			foundManaged = true
			break
		}
	}
	if !foundManaged {
		t.Fatal("MYKEY should be in managedKeys before delete attempt")
	}

	// Simulate broadcast failure
	fp.broadcastErr = fmt.Errorf("simulated broadcast failure")

	// Delete should succeed (broadcast failure is non-blocking)
	if err := svc.Delete("MYKEY"); err != nil {
		t.Fatalf("Delete should succeed when only broadcast fails: %v", err)
	}

	// envVars should no longer contain MYKEY
	if _, ok := svc.Get("MYKEY"); ok {
		t.Fatal("MYKEY should not be in envVars after successful delete")
	}

	// managedKeys should no longer contain MYKEY
	for _, mk := range svc.globalSyncManagedKeys {
		if mk == normalizeKey("MYKEY") {
			t.Fatal("managedKeys should not contain MYKEY after successful delete")
		}
	}

	// backups should no longer contain MYKEY
	if _, ok := svc.globalSyncBackups[normalizeKey("MYKEY")]; ok {
		t.Fatal("backups should not contain MYKEY after successful delete")
	}

	// fake platform should have MYKEY restored to os_original (OS restore succeeded)
	if v := fp.vars["MYKEY"]; v != "os_original" {
		t.Fatalf("fake platform MYKEY should be restored to os_original, got %q", v)
	}
}

// containsStr 检查字符串是否包含子串
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && findSubstr(s, sub)))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
