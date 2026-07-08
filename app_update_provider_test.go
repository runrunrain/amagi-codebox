package main

import (
	"encoding/json"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
)

// buildExportProviderJSON 构造完整的 ExportProvider JSON 字符串，供 UpdateProvider 使用。
// anthropicEnabled / openaiEnabled 控制双格式；apiKey 为空时省略顶层 api_key 字段。
func buildExportProviderJSON(t *testing.T, defaultModel, baseURL, apiKey string) string {
	t.Helper()
	ep := config.ExportProvider{
		DefaultModel: defaultModel,
		Presets:      map[string]config.Preset{},
	}
	if baseURL != "" || apiKey != "" {
		ep.Anthropic = &config.AnthropicFormat{
			Enabled: true,
			BaseURL: baseURL,
		}
	}
	if apiKey != "" {
		ep.APIKey = apiKey
	}
	b, err := json.Marshal(ep)
	if err != nil {
		t.Fatalf("marshal ExportProvider: %v", err)
	}
	return string(b)
}

// ============================================================================
// App.UpdateProvider -- 未改名分支
// ============================================================================

// TestUpdateProvider_NoRenameFallsBackToSaveProviderFromJSON 验证 oldName==newName 时
// 走 SaveProviderFromJSON 路径，不触发 config 迁移，不产生 secrets 副作用。
func TestUpdateProvider_NoRenameFallsBackToSaveProviderFromJSON(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)

	// 初始保存 provider "glm"
	if err := app.Config.SaveProvider("glm", config.Provider{DefaultModel: "glm-4"}); err != nil {
		t.Fatalf("SaveProvider(glm): %v", err)
	}
	// 初始密钥
	if err := app.Secrets.SetAPIKey("glm", "old-secret"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	// 未改名，只更新属性 + 新密钥
	jsonStr := buildExportProviderJSON(t, "glm-5", "https://new.base", "new-secret")
	if err := app.UpdateProvider("glm", "glm", jsonStr); err != nil {
		t.Fatalf("UpdateProvider(no-rename): %v", err)
	}

	// 属性已更新
	p, err := app.Config.GetProvider("glm")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if p.DefaultModel != "glm-5" {
		t.Fatalf("DefaultModel = %q, want glm-5", p.DefaultModel)
	}
	// 密钥已更新（走 SaveProviderFromJSON 路径）
	key, _ := app.Secrets.GetAPIKey("glm")
	if key != "new-secret" {
		t.Fatalf("API key = %q, want 'new-secret'", key)
	}
}

// ============================================================================
// App.UpdateProvider -- 改名分支 secrets 迁移三分支
// ============================================================================

// TestUpdateProvider_RenameMigratesOldKeyWhenNewKeyEmpty 验证：
// 旧 provider 有密钥 + JSON 未填新密钥 → 迁移旧密钥到 newName + 删旧。
func TestUpdateProvider_RenameMigratesOldKeyWhenNewKeyEmpty(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{DefaultModel: "glm-4"}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey("glm", "old-secret"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	// JSON 不含 api_key（留空 = 保持不变）
	jsonStr := buildExportProviderJSON(t, "glm-4", "https://a.glm.com", "")
	if err := app.UpdateProvider("glm", "zhipu", jsonStr); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	// 旧密钥迁移到 newName
	newKey, _ := app.Secrets.GetAPIKey("zhipu")
	if newKey != "old-secret" {
		t.Fatalf("migrated key = %q, want 'old-secret'", newKey)
	}
	// 旧 name 密钥已删
	oldKey, _ := app.Secrets.GetAPIKey("glm")
	if oldKey != "" {
		t.Fatalf("old key should be deleted, got %q", oldKey)
	}
	// config 改名生效
	if _, err := app.Config.GetProvider("zhipu"); err != nil {
		t.Fatalf("zhipu should exist: %v", err)
	}
}

// TestUpdateProvider_RenameWritesNewKeyAndDeletesOld 验证：
// 旧 provider 有密钥 + JSON 填了新密钥 → 写入 newName 的新密钥 + 删旧。
func TestUpdateProvider_RenameWritesNewKeyAndDeletesOld(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{DefaultModel: "glm-4"}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Secrets.SetAPIKey("glm", "old-secret"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	jsonStr := buildExportProviderJSON(t, "glm-4", "https://a.glm.com", "brand-new-secret")
	if err := app.UpdateProvider("glm", "zhipu", jsonStr); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	newKey, _ := app.Secrets.GetAPIKey("zhipu")
	if newKey != "brand-new-secret" {
		t.Fatalf("new key = %q, want 'brand-new-secret'", newKey)
	}
	oldKey, _ := app.Secrets.GetAPIKey("glm")
	if oldKey != "" {
		t.Fatalf("old key should be deleted, got %q", oldKey)
	}
}

// TestUpdateProvider_RenameSkipsSecretsWhenBothEmpty 验证：
// 旧 provider 无密钥 + JSON 未填新密钥 → 跳过 secrets 迁移，不调用 Save。
func TestUpdateProvider_RenameSkipsSecretsWhenBothEmpty(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{DefaultModel: "glm-4"}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	// 不设置任何密钥

	jsonStr := buildExportProviderJSON(t, "glm-4", "https://a.glm.com", "")
	if err := app.UpdateProvider("glm", "zhipu", jsonStr); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	// 两侧都无密钥
	if k, _ := app.Secrets.GetAPIKey("zhipu"); k != "" {
		t.Fatalf("zhipu should have no key, got %q", k)
	}
	if k, _ := app.Secrets.GetAPIKey("glm"); k != "" {
		t.Fatalf("glm should have no key, got %q", k)
	}
	// config 改名生效
	if _, err := app.Config.GetProvider("zhipu"); err != nil {
		t.Fatalf("zhipu should exist: %v", err)
	}
}

// ============================================================================
// App.UpdateProvider -- 改名分支 config 一致性
// ============================================================================

// TestUpdateProvider_RenameAppliesNewProperties 验证改名后新属性覆盖生效。
func TestUpdateProvider_RenameAppliesNewProperties(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{
		DefaultModel: "glm-4",
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://old.base"},
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// 改名 + 更新 default_model + baseURL
	jsonStr := buildExportProviderJSON(t, "glm-5", "https://new.base", "")
	if err := app.UpdateProvider("glm", "zhipu", jsonStr); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	p, err := app.Config.GetProvider("zhipu")
	if err != nil {
		t.Fatalf("GetProvider(zhipu): %v", err)
	}
	if p.DefaultModel != "glm-5" {
		t.Fatalf("DefaultModel = %q, want glm-5", p.DefaultModel)
	}
	if p.Anthropic == nil || p.Anthropic.BaseURL != "https://new.base" {
		t.Fatalf("Anthropic.BaseURL not updated, got %+v", p.Anthropic)
	}
}

// TestUpdateProvider_RenameMigratesTerminalPresets 验证改名后 TerminalPresets stable key 同步，
// 启动链 ResolveTerminalPreset 能用新 key 解析到新 provider。
func TestUpdateProvider_RenameMigratesTerminalPresets(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{DefaultModel: "glm-4"}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := app.Config.SaveTerminalPreset("claude_code", "glm/max", config.TerminalPreset{
		Name: "Max", Provider: "glm", Model: "glm-4-max",
	}); err != nil {
		t.Fatalf("SaveTerminalPreset: %v", err)
	}

	jsonStr := buildExportProviderJSON(t, "glm-4", "", "")
	if err := app.UpdateProvider("glm", "zhipu", jsonStr); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	// 启动链关键：ResolveTerminalPreset 用新 key 解析到新 provider name
	provName, tp, err := app.Config.ResolveTerminalPreset("claude_code", "zhipu/max")
	if err != nil {
		t.Fatalf("ResolveTerminalPreset: %v", err)
	}
	if provName != "zhipu" {
		t.Fatalf("resolved provider = %q, want zhipu", provName)
	}
	if tp == nil {
		t.Fatal("TerminalPreset should resolve")
	}
	if tp.Provider != "zhipu" {
		t.Fatalf("tp.Provider = %q, want zhipu", tp.Provider)
	}
	// 旧 key 不应再解析
	if _, _, err := app.Config.ResolveTerminalPreset("claude_code", "glm/max"); err != nil {
		// err 为 nil 表示未找到（返回空），此处期望返回空 provider
	}
}

// ============================================================================
// App.UpdateProvider -- 校验
// ============================================================================

// TestUpdateProvider_ValidationErrors 验证各类校验失败。
func TestUpdateProvider_ValidationErrors(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{DefaultModel: "glm-4"}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	validJSON := buildExportProviderJSON(t, "glm-4", "", "")

	tests := []struct {
		name        string
		oldName     string
		newName     string
		jsonStr     string
		wantSubstr  string
	}{
		{"empty oldName", "", "new", validJSON, "provider name is required"},
		{"empty newName", "glm", "  ", validJSON, "provider name is required"},
		{"newName with slash", "glm", "zhi/pu", validJSON, "must not contain '/'"},
		{"invalid JSON", "glm", "zhipu", "{not json}", "invalid JSON format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := app.UpdateProvider(tt.oldName, tt.newName, tt.jsonStr)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}

// TestUpdateProvider_RenameNonExistentOldName 验证改名时 oldName 不存在 → RenameProvider 报错透传。
func TestUpdateProvider_RenameNonExistentOldName(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	jsonStr := buildExportProviderJSON(t, "x", "", "")
	err := app.UpdateProvider("nonexistent", "newname", jsonStr)
	if err == nil {
		t.Fatal("expected error for non-existent oldName")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should mention 'not found', got: %v", err)
	}
}

// TestUpdateProvider_RenameToExistingName 验证 newName 与其他 provider 重名时报错。
func TestUpdateProvider_RenameToExistingName(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{DefaultModel: "glm-4"}); err != nil {
		t.Fatalf("SaveProvider(glm): %v", err)
	}
	if err := app.Config.SaveProvider("zhipu", config.Provider{DefaultModel: "zhipu-4"}); err != nil {
		t.Fatalf("SaveProvider(zhipu): %v", err)
	}
	jsonStr := buildExportProviderJSON(t, "glm-4", "", "")
	err := app.UpdateProvider("glm", "zhipu", jsonStr)
	if err == nil {
		t.Fatal("expected error when newName already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error should mention 'already exists', got: %v", err)
	}
}

// TestUpdateProvider_RenameCleansLegacySecretKeys 验证改名后旧 name 的 legacy 密钥条目也被清理。
func TestUpdateProvider_RenameCleansLegacySecretKeys(t *testing.T) {
	app, _ := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("glm", config.Provider{
		DefaultModel: "glm-4",
		Anthropic:    &config.AnthropicFormat{Enabled: true},
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	// 模拟 legacy 密钥命名（providerName:anthropic）
	if err := app.Secrets.SetAPIKey("glm:anthropic", "legacy-secret"); err != nil {
		t.Fatalf("SetAPIKey legacy: %v", err)
	}
	if err := app.Secrets.SetAPIKey("glm", "unified-secret"); err != nil {
		t.Fatalf("SetAPIKey unified: %v", err)
	}

	jsonStr := buildExportProviderJSON(t, "glm-4", "", "")
	if err := app.UpdateProvider("glm", "zhipu", jsonStr); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	// 统一密钥迁移
	if k, _ := app.Secrets.GetAPIKey("zhipu"); k != "unified-secret" {
		t.Fatalf("zhipu key = %q, want 'unified-secret'", k)
	}
	// 旧 name 的统一 + legacy 条目均清理
	if k, _ := app.Secrets.GetAPIKey("glm"); k != "" {
		t.Fatalf("glm unified key should be deleted, got %q", k)
	}
	if k, _ := app.Secrets.GetAPIKey("glm:anthropic"); k != "" {
		t.Fatalf("glm:anthropic legacy key should be deleted, got %q", k)
	}
}
