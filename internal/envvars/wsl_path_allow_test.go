package envvars

// P4-A2 per-key 路径值放行门（allowPathValue）：envvars.json 键级标记驱动
// WSL 启动链的路径值放行（StripWSLHostPathEnvWithAllow 豁免）与 WSLENV /p
// 路径翻译（appendWSLENVForwardingWithPaths）。本文件钉定持久化面：
// 标记随 EnvVar 结构读写往返、保留键拒绝、PathAllowKeys 顺序、
// Set(key,value) 不丢标记、可迁移快照携带标记。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newAllowGateTestService(t *testing.T) *EnvVarsService {
	t.Helper()
	s := NewEnvVarsService(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}

func readEnvVarsJSON(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "envvars.json"))
	if err != nil {
		t.Fatalf("read envvars.json: %v", err)
	}
	return string(b)
}

// 标记随结构持久化：BatchSet 写入 → 磁盘 JSON 含 allowPathValue:true →
// 重新 Load → GetAll 保标记；未标记键 omitempty 不落字段（默认基线可观测）。
func TestAllowPathValueRoundTrip(t *testing.T) {
	s := newAllowGateTestService(t)
	if err := s.BatchSet([]EnvVar{
		{Key: "UE_ROOT", Value: `C:\Program Files\Epic Games\UE_5.4`, AllowPathValue: true},
		{Key: "TAVILY_API_KEY", Value: "tk"},
	}); err != nil {
		t.Fatalf("batch set: %v", err)
	}
	disk := readEnvVarsJSON(t, filepath.Dir(s.configPath))
	if !strings.Contains(disk, `"allowPathValue": true`) {
		t.Fatalf("saved JSON missing allowPathValue marker: %s", disk)
	}
	if strings.Count(disk, "allowPathValue") != 1 {
		t.Fatalf("unmarked key must omit allowPathValue (omitempty): %s", disk)
	}

	s2 := NewEnvVarsService(filepath.Dir(s.configPath))
	if err := s2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	vars := s2.GetAll()
	if len(vars) != 2 || !vars[0].AllowPathValue || vars[1].AllowPathValue {
		t.Fatalf("AllowPathValue lost on reload: %+v", vars)
	}
	if keys := s2.PathAllowKeys(); strings.Join(keys, ",") != "UE_ROOT" {
		t.Fatalf("PathAllowKeys = %v, want [UE_ROOT]", keys)
	}
}

// 保留键不适用放行门：批量保存与 JSON 导入路径都在 validateEnvVars 拒绝。
func TestAllowPathValueReservedKeyRejected(t *testing.T) {
	s := newAllowGateTestService(t)
	for _, reserved := range []string{"WSLENV", "PATH", "PATHEXT", "path"} {
		if err := s.BatchSet([]EnvVar{{Key: reserved, Value: "x", AllowPathValue: true}}); err == nil {
			t.Fatalf("BatchSet must reject allowPathValue on reserved key %q", reserved)
		}
	}
	if err := s.Import(`{"envVars":[{"key":"PATH","value":"x","allowPathValue":true}]}`); err == nil {
		t.Fatal("Import(SaveJSON) must reject allowPathValue on reserved key")
	}
	// 保留键本身仍可存在（只是不能标记放行）。
	if err := s.BatchSet([]EnvVar{{Key: "PATH", Value: "x"}}); err != nil {
		t.Fatalf("reserved key without marker must stay accepted: %v", err)
	}
}

// SetPathAllow：开启/取消均持久化；未知键与保留键开启拒绝；幂等不写盘。
func TestSetPathAllow(t *testing.T) {
	s := newAllowGateTestService(t)
	if err := s.BatchSet([]EnvVar{
		{Key: "UE_ROOT", Value: `D:\eng`},
		{Key: "VSINSTALLDIR", Value: `C:\Program Files\Microsoft Visual Studio\2022\Community`},
	}); err != nil {
		t.Fatalf("batch set: %v", err)
	}
	if err := s.SetPathAllow("UE_ROOT", true); err != nil {
		t.Fatalf("SetPathAllow(UE_ROOT, true): %v", err)
	}
	// 键匹配跟随 keysEqual 的平台规则（Windows 大小写不敏感，其余平台大小写
	// 敏感——不扩原生行为，只对齐断言）：Windows 上异体大小写命中同一键；
	// 非 Windows 上异体大小写是未知键（报错），精确大小写才命中。
	if runtime.GOOS == "windows" {
		if err := s.SetPathAllow("vsinstalldir", true); err != nil {
			t.Fatalf("SetPathAllow(vsinstalldir, true): %v", err)
		}
	} else {
		if err := s.SetPathAllow("vsinstalldir", true); err == nil {
			t.Fatal("non-Windows keys are case-sensitive; mismatched case must error")
		}
		if err := s.SetPathAllow("VSINSTALLDIR", true); err != nil {
			t.Fatalf("SetPathAllow(VSINSTALLDIR, true) with exact case: %v", err)
		}
	}
	if keys := s.PathAllowKeys(); strings.Join(keys, ",") != "UE_ROOT,VSINSTALLDIR" {
		t.Fatalf("PathAllowKeys = %v, want config order [UE_ROOT VSINSTALLDIR]", keys)
	}
	if err := s.SetPathAllow("UE_ROOT", false); err != nil {
		t.Fatalf("SetPathAllow(UE_ROOT, false): %v", err)
	}
	if keys := s.PathAllowKeys(); strings.Join(keys, ",") != "VSINSTALLDIR" {
		t.Fatalf("PathAllowKeys after disable = %v, want [VSINSTALLDIR]", keys)
	}
	if err := s.SetPathAllow("MISSING_KEY", true); err == nil {
		t.Fatal("SetPathAllow on unknown key must error")
	}
	if err := s.SetPathAllow("PATH", true); err == nil {
		t.Fatal("SetPathAllow on reserved key must error")
	}
	// 磁盘与内存一致。
	if disk := readEnvVarsJSON(t, filepath.Dir(s.configPath)); !strings.Contains(disk, `"allowPathValue": true`) {
		t.Fatalf("marker not persisted: %s", disk)
	}
}

// Set(key,value) 只改值不改标记：编辑路径不静默丢失用户的放行同意。
func TestSetPreservesAllowPathValue(t *testing.T) {
	s := newAllowGateTestService(t)
	if err := s.BatchSet([]EnvVar{{Key: "UE_ROOT", Value: `D:\eng`, AllowPathValue: true}}); err != nil {
		t.Fatalf("batch set: %v", err)
	}
	if err := s.Set("UE_ROOT", `E:\eng\5.5`); err != nil {
		t.Fatalf("set value: %v", err)
	}
	vars := s.GetAll()
	if vars[0].Value != `E:\eng\5.5` || !vars[0].AllowPathValue {
		t.Fatalf("Set must update value but keep marker: %+v", vars[0])
	}
}

// 可迁移快照携带标记（PortableConfig/ReplacePortableConfig 全走 EnvVar）。
func TestPortableConfigCarriesAllowPathValue(t *testing.T) {
	s := newAllowGateTestService(t)
	if err := s.BatchSet([]EnvVar{{Key: "UE_ROOT", Value: `D:\eng`, AllowPathValue: true}}); err != nil {
		t.Fatalf("batch set: %v", err)
	}
	pc := s.GetPortableConfig()
	if len(pc.EnvVars) != 1 || !pc.EnvVars[0].AllowPathValue {
		t.Fatalf("portable config lost marker: %+v", pc.EnvVars)
	}
	dst := newAllowGateTestService(t)
	if err := dst.ReplacePortableConfig(pc); err != nil {
		t.Fatalf("replace portable config: %v", err)
	}
	if keys := dst.PathAllowKeys(); strings.Join(keys, ",") != "UE_ROOT" {
		t.Fatalf("PathAllowKeys after import = %v, want [UE_ROOT]", keys)
	}
}
