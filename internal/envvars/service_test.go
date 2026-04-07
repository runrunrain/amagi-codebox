package envvars

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCRUD(t *testing.T) {
	dir := t.TempDir()
	svc := NewEnvVarsService(dir)

	// 初始为空
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := svc.GetAll(); len(got) != 0 {
		t.Fatalf("expected 0 vars, got %d", len(got))
	}

	// Set - 新增
	if err := svc.Set("FOO", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, ok := svc.Get("FOO"); !ok || v != "bar" {
		t.Fatalf("Get FOO: got %q %v", v, ok)
	}

	// Set - 更新
	if err := svc.Set("FOO", "baz"); err != nil {
		t.Fatalf("Set update: %v", err)
	}
	if v, ok := svc.Get("FOO"); !ok || v != "baz" {
		t.Fatalf("Get FOO after update: got %q", v)
	}

	// Set 第二个变量
	if err := svc.Set("BAR", "qux"); err != nil {
		t.Fatalf("Set BAR: %v", err)
	}
	if len(svc.GetAll()) != 2 {
		t.Fatalf("expected 2 vars")
	}

	// Delete
	if err := svc.Delete("FOO"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := svc.Get("FOO"); ok {
		t.Fatal("FOO should be deleted")
	}
	if len(svc.GetAll()) != 1 {
		t.Fatalf("expected 1 var after delete")
	}

	// Delete 不存在的 key
	if err := svc.Delete("NONEXISTENT"); err == nil {
		t.Fatal("expected error for nonexistent key")
	}

	// Set empty key
	if err := svc.Set("", "value"); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	svc := NewEnvVarsService(dir)

	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.Set("PERSIST_KEY", "persist_val"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// 验证文件已写入
	configPath := filepath.Join(dir, "envvars.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read envvars.json: %v", err)
	}

	var f envVarsFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(f.EnvVars) != 1 || f.EnvVars[0].Key != "PERSIST_KEY" || f.EnvVars[0].Value != "persist_val" {
		t.Fatalf("unexpected persisted content: %+v", f)
	}

	// 新实例重新加载，验证持久化正确
	svc2 := NewEnvVarsService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load2: %v", err)
	}
	if v, ok := svc2.Get("PERSIST_KEY"); !ok || v != "persist_val" {
		t.Fatalf("reload Get: %q %v", v, ok)
	}
}

func TestMergeWithSystem(t *testing.T) {
	dir := t.TempDir()
	svc := NewEnvVarsService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 覆盖一个已存在的系统变量（PATH 肯定存在）
	// 用一个不太可能存在的 key 测试新增
	testKey := "CC_SWITCH_TEST_CUSTOM_VAR_XYZ"
	testVal := "custom_value_123"
	if err := svc.Set(testKey, testVal); err != nil {
		t.Fatalf("Set: %v", err)
	}

	merged := svc.MergeWithSystem()

	// 确认自定义变量出现在结果中
	found := false
	for _, kv := range merged {
		if kv == testKey+"="+testVal {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("custom var not found in merged env")
	}

	// 确认系统变量（PATH）也在结果中
	pathFound := false
	for _, kv := range merged {
		if len(kv) >= 5 && (kv[:5] == "PATH=" || kv[:5] == "path=" || kv[:5] == "Path=") {
			pathFound = true
			break
		}
	}
	if !pathFound {
		t.Fatal("PATH not found in merged env")
	}
}

func TestMergeWithSystemOverride(t *testing.T) {
	dir := t.TempDir()
	svc := NewEnvVarsService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 设置一个与系统变量同名的自定义变量进行覆盖测试
	// 使用 PATH 来验证覆盖（PATH 在所有系统中都存在）
	customPathVal := "C:\\custom\\path"
	if err := svc.Set("PATH", customPathVal); err != nil {
		t.Fatalf("Set PATH: %v", err)
	}

	merged := svc.MergeWithSystem()

	// 在结果中查找 PATH，验证值是自定义值而非原始系统值
	for _, kv := range merged {
		k := ""
		for i, c := range kv {
			if c == '=' {
				k = kv[:i]
				v := kv[i+1:]
				if k == "PATH" || k == "path" || k == "Path" {
					if v != customPathVal {
						t.Fatalf("PATH should be overridden to %q, got %q", customPathVal, v)
					}
					return
				}
				break
			}
		}
	}
	t.Fatal("PATH not found in merged env")
}

func TestMergeEnvWindowsCaseInsensitiveOverride(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only path case-insensitive behavior")
	}

	merged := mergeEnv(
		[]string{
			"Path=C:\\Windows\\System32",
			"ComSpec=C:\\Windows\\System32\\cmd.exe",
		},
		[]EnvVar{{Key: "PATH", Value: `C:\custom\bin`}},
	)

	pathCount := 0
	for _, kv := range merged {
		k, v := splitEnvKV(kv)
		if k == "Path" || k == "PATH" || k == "path" {
			pathCount++
			if v != `C:\custom\bin` {
				t.Fatalf("PATH should be overridden to %q, got %q", `C:\custom\bin`, v)
			}
		}
	}

	if pathCount != 1 {
		t.Fatalf("expected exactly 1 PATH entry, got %d", pathCount)
	}
}

func TestBatchSet(t *testing.T) {
	dir := t.TempDir()
	svc := NewEnvVarsService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	vars := []EnvVar{
		{Key: "A", Value: "1"},
		{Key: "B", Value: "2"},
		{Key: "C", Value: "3"},
	}
	if err := svc.BatchSet(vars); err != nil {
		t.Fatalf("BatchSet: %v", err)
	}
	if all := svc.GetAll(); len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}

	// 全量替换
	if err := svc.BatchSet([]EnvVar{{Key: "X", Value: "99"}}); err != nil {
		t.Fatalf("BatchSet replace: %v", err)
	}
	if all := svc.GetAll(); len(all) != 1 || all[0].Key != "X" {
		t.Fatalf("unexpected vars: %+v", all)
	}
}

func TestImportExport(t *testing.T) {
	dir := t.TempDir()
	svc := NewEnvVarsService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	_ = svc.Set("K1", "V1")
	_ = svc.Set("K2", "V2")

	exported, err := svc.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// 新实例导入
	svc2 := NewEnvVarsService(t.TempDir())
	if err := svc2.Import(exported); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if v, ok := svc2.Get("K1"); !ok || v != "V1" {
		t.Fatalf("K1 after import: %q %v", v, ok)
	}
	if v, ok := svc2.Get("K2"); !ok || v != "V2" {
		t.Fatalf("K2 after import: %q %v", v, ok)
	}
}
