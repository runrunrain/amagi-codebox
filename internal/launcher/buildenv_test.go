package launcher

import (
	"strings"
	"testing"

	"amagi-codebox/internal/envvars"
)

func TestBuildEnvPriorityChain(t *testing.T) {
	// 模拟：baseEnv = MergeWithSystem() 的结果（含自定义变量覆盖了系统变量）
	// overrides = 认证变量（如 ANTHROPIC_API_KEY）
	// 验证优先级：overrides > 自定义变量 > 系统变量

	base := []string{
		"PATH=/usr/bin",
		"MY_CUSTOM_VAR=custom_value", // 自定义变量（来自 envvars service）
		"ANTHROPIC_API_KEY=old_key",  // 系统中存在的 key（应被 override 覆盖）
	}

	overrides := map[string]string{
		"ANTHROPIC_API_KEY": "new_injected_key", // override 覆盖
	}

	result := BuildEnv(base, overrides)

	// ANTHROPIC_API_KEY 应为 override 的值
	for _, kv := range result {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			val := kv[len("ANTHROPIC_API_KEY="):]
			if val != "new_injected_key" {
				t.Fatalf("ANTHROPIC_API_KEY should be 'new_injected_key', got %q", val)
			}
			break
		}
	}

	// MY_CUSTOM_VAR 应保留
	found := false
	for _, kv := range result {
		if kv == "MY_CUSTOM_VAR=custom_value" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("MY_CUSTOM_VAR should be present in result")
	}

	// PATH 应保留
	pathFound := false
	for _, kv := range result {
		if strings.HasPrefix(kv, "PATH=") {
			pathFound = true
			break
		}
	}
	if !pathFound {
		t.Fatal("PATH should be present in result")
	}
}

func TestBuildEnvDeleteOnEmpty(t *testing.T) {
	// 验证 override 值为 "" 时，该 key 从结果中删除
	base := []string{
		"ANTHROPIC_AUTH_TOKEN=some_token",
		"ANTHROPIC_API_KEY=some_key",
	}
	overrides := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "", // 删除
		"ANTHROPIC_API_KEY":    "real_key",
	}

	result := BuildEnv(base, overrides)

	for _, kv := range result {
		if strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			t.Fatal("ANTHROPIC_AUTH_TOKEN should be deleted from env")
		}
	}

	keyFound := false
	for _, kv := range result {
		if kv == "ANTHROPIC_API_KEY=real_key" {
			keyFound = true
			break
		}
	}
	if !keyFound {
		t.Fatal("ANTHROPIC_API_KEY=real_key should be present")
	}
}

func TestBuildEnvNewKeyAppended(t *testing.T) {
	base := []string{"EXISTING=val"}
	overrides := map[string]string{
		"NEW_KEY": "new_val",
	}

	result := BuildEnv(base, overrides)

	found := false
	for _, kv := range result {
		if kv == "NEW_KEY=new_val" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("NEW_KEY should be appended to result")
	}
}

func TestLauncherBaseEnvUsesCustomEnvVars(t *testing.T) {
	t.Setenv("AMAGI_CODEBOX_LAUNCHER_ENV_TEST", "system")

	envSvc := envvars.NewEnvVarsService(t.TempDir())
	if err := envSvc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := envSvc.Set("AMAGI_CODEBOX_LAUNCHER_ENV_TEST", "custom"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	svc := NewLauncherService(nil, envSvc)
	base := svc.baseEnv()

	for _, kv := range base {
		if kv == "AMAGI_CODEBOX_LAUNCHER_ENV_TEST=custom" {
			return
		}
	}

	t.Fatal("expected launcher base env to contain custom env var override")
}
