package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/config"
)

// seedAgentCLIModelsJSON 用原始 models.json 落盘并重载配置。用于构造正常保存
// 链路无法表达的存量字段（wire_api / url_history / preset opencode_config ——
// internal/config scrubProviderAPIKeys 在任何保存时都会清 wire_api）。
func seedAgentCLIModelsJSON(t *testing.T, app *App, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(app.configDir, "models.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
	if err := app.Config.Load(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
}

// providerSaveJSON 是一个不含密钥的完整 provider 视图（provider get 的输出形态）。
const providerSaveJSON = `{"defaultModel":"m-main","openai":{"enabled":true,"baseUrl":"https://api.acme.test/v1","wireApi":"chat","organization":"acme"},"presets":[{"name":"Daily","model":"m-daily"}]}`

func TestAgentCLIProviderSaveGetListRoundTrip(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()

	saved, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": providerSaveJSON})
	if err != nil {
		t.Fatalf("provider save: %v", err)
	}
	view := saved.(map[string]any)["provider"].(*agentCLIProviderView)
	if view.Name != "acme" || view.DefaultModel != "m-main" {
		t.Errorf("保存后视图不符: %+v", view)
	}
	if view.OpenAI == nil || view.OpenAI.BaseURL != "https://api.acme.test/v1" || view.OpenAI.Organization != "acme" {
		t.Errorf("openai 子块不符: %+v", view.OpenAI)
	}
	// 注：wireApi 在保存链路被 internal/config scrubProviderAPIKeys 丢弃
	//（存量上游问题，已上报 Leader；此处不锚定 round-trip）。
	if len(view.Presets) != 1 || view.Presets[0].Model != "m-daily" {
		t.Errorf("presets 不符: %+v", view.Presets)
	}

	got, err := h.handleProviderGet(ctx, spec.Args{"name": "acme"})
	if err != nil {
		t.Fatalf("provider get: %v", err)
	}
	if got.(*agentCLIProviderView).OpenAI.Organization != "acme" {
		t.Errorf("get 视图不符: %+v", got)
	}

	list, err := h.handleProviderList(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("provider list: %v", err)
	}
	if list.(map[string]any)["count"] != 1 {
		t.Errorf("list count = %v", list.(map[string]any)["count"])
	}
	// type 过滤走真实 GetProvidersByType 语义。
	filtered, err := h.handleProviderList(ctx, spec.Args{"type": "anthropic"})
	if err != nil {
		t.Fatalf("provider list filter: %v", err)
	}
	if filtered.(map[string]any)["count"] != 0 {
		t.Errorf("anthropic 过滤应为 0，实际 %v", filtered.(map[string]any)["count"])
	}
}

func TestAgentCLIProviderGetMissing(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	_, err := h.handleProviderGet(context.Background(), spec.Args{"name": "ghost"})
	if code := specErrCode(t, err); code != spec.ErrNotFound {
		t.Errorf("get 缺失 provider 错误码 = %s, want not_found", code)
	}
}

func TestAgentCLIProviderSaveRejectsKeyFields(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	cases := []struct {
		name string
		json string
	}{
		{"顶层 apiKey", `{"defaultModel":"m","apiKey":"sk-live-PLAINTEXT"}`},
		{"顶层 snake api_key", `{"default_model":"m","api_key":"sk-live-PLAINTEXT"}`},
		{"嵌套 anthropic api_key", `{"anthropic":{"enabled":true,"api_key":"sk-live-PLAINTEXT"}}`},
		{"嵌套 openai auth_key", `{"openai":{"enabled":true,"auth_key":"sk-live-PLAINTEXT"}}`},
		{"presets 内 apiKey", `{"presets":[{"name":"p","apiKey":"sk-live-PLAINTEXT"}]}`},
	}
	for _, c := range cases {
		_, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": c.json})
		if code := specErrCode(t, err); code != spec.ErrBadArgs {
			t.Errorf("%s: 错误码 = %s (%v), want bad_args", c.name, code, err)
		}
	}
	// 空值密钥字段视同未提供，允许保存。
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": `{"apiKey":""}`}); err != nil {
		t.Errorf("空 apiKey 应允许: %v", err)
	}
}

func TestAgentCLIProviderKeySetMasksKey(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	const testKey = "sk-agentcli-PLAINTEXT-4f5e6d7c8b"
	data, err := h.handleProviderKeySet(ctx, spec.Args{"name": "acme", "key": testKey})
	if err != nil {
		t.Fatalf("key-set: %v", err)
	}
	// 返回的是掩码视图，绝不回显明文。
	out := mustJSON(t, data)
	if strings.Contains(out, testKey) {
		t.Fatalf("key-set 回显明文密钥: %s", out)
	}
	keyView := data.(map[string]any)["key"].(*agentCLIKeyStatusView)
	if !keyView.HasStored || keyView.MaskedKey == "" || keyView.KeyLength != len(testKey) {
		t.Errorf("key 视图不符: %+v", keyView)
	}
	if stored, _ := app.Secrets.GetAPIKey("acme"); stored != testKey {
		t.Errorf("secrets 存储与写入值不符")
	}
}

func TestAgentCLIProviderDelete(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": providerSaveJSON}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.handleProviderKeySet(ctx, spec.Args{"name": "acme", "key": "sk-agentcli-PLAINTEXT-4f5e6d7c8b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.handleProviderDelete(ctx, spec.Args{"name": "acme"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := h.handleProviderGet(ctx, spec.Args{"name": "acme"}); specErrCode(t, err) != spec.ErrNotFound {
		t.Fatalf("删除后 get 应 not_found")
	}
	if app.Secrets.HasAPIKey("acme") {
		t.Errorf("删除 provider 后密钥未清理")
	}
	if _, err := h.handleProviderDelete(ctx, spec.Args{"name": "acme"}); err != nil {
		t.Errorf("重复删除应幂等成功，实际: %v", err)
	}
}

func TestAgentCLIKeyStatus(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": providerSaveJSON}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.handleProviderKeySet(ctx, spec.Args{"name": "acme", "key": "sk-agentcli-PLAINTEXT-4f5e6d7c8b"}); err != nil {
		t.Fatal(err)
	}
	data, err := h.handleKeyStatus(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("key status: %v", err)
	}
	if got := data.(map[string]any)["count"]; got != 1 {
		t.Errorf("count = %v", got)
	}
	if _, err := h.handleKeyStatus(ctx, spec.Args{"provider": "ghost"}); specErrCode(t, err) != spec.ErrNotFound {
		t.Errorf("未知 provider 过滤应 not_found")
	}
}

func TestAgentCLIPresetSaveListDelete(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()

	// 旗标路径保存。
	if _, err := h.handlePresetSave(ctx, spec.Args{"type": "claude_code", "name": "daily", "provider": "acme", "model": "m-daily"}); err != nil {
		t.Fatalf("preset save: %v", err)
	}
	list, err := h.handlePresetList(ctx, spec.Args{"type": "claude_code"})
	if err != nil {
		t.Fatalf("preset list: %v", err)
	}
	items := list.(map[string]any)["presets"].([]agentCLIPresetListItem)
	found := false
	for _, it := range items {
		if it.Provider == "acme" && it.Model == "m-daily" {
			found = true
		}
	}
	if !found {
		t.Errorf("list 中未找到保存的预设: %+v", items)
	}
	if list.(map[string]any)["type"] != string(config.TerminalPresetAnthropic) {
		t.Errorf("type 归一化不符: %v", list.(map[string]any)["type"])
	}

	// json 路径增量更新：只改 model，provider 保留。
	if _, err := h.handlePresetSave(ctx, spec.Args{
		"type": "claude_code", "name": "daily",
		"json": `{"model":"m-daily2"}`,
	}); err != nil {
		t.Fatalf("preset save json: %v", err)
	}
	list2, _ := h.handlePresetList(ctx, spec.Args{"type": "claude_code"})
	for _, it := range list2.(map[string]any)["presets"].([]agentCLIPresetListItem) {
		if it.Provider == "acme" {
			if it.Model != "m-daily2" {
				t.Errorf("json 更新未生效: %+v", it)
			}
		}
	}

	// 缺 provider 的新预设 → bad_args。
	if _, err := h.handlePresetSave(ctx, spec.Args{"type": "claude_code", "name": "np", "model": "m"}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("缺 provider 应 bad_args: %v", err)
	}
	// 非法类型桶 → bad_args。
	if _, err := h.handlePresetList(ctx, spec.Args{"type": "nope"}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("非法类型应 bad_args: %v", err)
	}

	// 删除。
	if _, err := h.handlePresetDelete(ctx, spec.Args{"type": "claude_code", "name": "daily"}); err != nil {
		t.Fatalf("preset delete: %v", err)
	}
	list3, _ := h.handlePresetList(ctx, spec.Args{"type": "claude_code"})
	for _, it := range list3.(map[string]any)["presets"].([]agentCLIPresetListItem) {
		if it.Provider == "acme" {
			t.Errorf("删除后仍存在: %+v", it)
		}
	}
}

func TestAgentCLISettingsSetWhitelistAndForbidden(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()

	// 禁改面：remote*/cli*/含 token → forbidden_command。
	for _, key := range []string{"remoteport", "remotehost", "remotetoken", "githubtoken", "agentclienabled", "dashboard.preset-token"} {
		_, err := h.handleSettingsSet(ctx, spec.Args{"key": key, "value": "1"})
		if code := specErrCode(t, err); code != spec.ErrForbiddenCommand {
			t.Errorf("键 %q 应 forbidden_command，实际 %v", key, err)
		}
	}
	// 白名单键 + 归一化（大小写/下划线）。
	if _, err := h.handleSettingsSet(ctx, spec.Args{"key": "Dashboard.Provider", "value": "acme"}); err != nil {
		t.Fatalf("白名单键写入失败: %v", err)
	}
	if got := app.Settings.GetDashboardDefaults().Provider; got != "acme" {
		t.Errorf("dashboard.provider 未生效: %q", got)
	}
	if _, err := h.handleSettingsSet(ctx, spec.Args{"key": "terminal.scrollback", "value": "50000"}); err != nil {
		t.Fatalf("scrollback 写入失败: %v", err)
	}
	if got := app.Settings.GetTerminalSettings().Scrollback; got != 50000 {
		t.Errorf("scrollback 未生效: %d", got)
	}
	// 值类型错误 / 白名单外键。
	if _, err := h.handleSettingsSet(ctx, spec.Args{"key": "terminal.scrollback", "value": "abc"}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("scrollback 非整数应 bad_args: %v", err)
	}
	if _, err := h.handleSettingsSet(ctx, spec.Args{"key": "skin.background", "value": "#000"}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("白名单外键应 bad_args: %v", err)
	}
}

func TestAgentCLISettingsGetMasksGitHubToken(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	const ghToken = "ghp-agentcli-PLAINTEXT-1a2b3c4d5e"
	if err := app.Settings.SetGitHubToken(ghToken); err != nil {
		t.Fatal(err)
	}
	data, err := h.handleSettingsGet(context.Background(), nil)
	if err != nil {
		t.Fatalf("settings get: %v", err)
	}
	out := mustJSON(t, data)
	if strings.Contains(out, ghToken) {
		t.Fatalf("settings get 泄露 githubToken: %s", out)
	}
	m := data.(map[string]any)
	if m["githubToken"] != "" || m["githubTokenSet"] != true {
		t.Errorf("githubToken 脱敏不符: %v %v", m["githubToken"], m["githubTokenSet"])
	}
}

func TestAgentCLIEnvVarsHandlers(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	if _, err := h.handleEnvVarsSet(ctx, spec.Args{"key": "AGENTCLI_TEST_VAR", "value": "1"}); err != nil {
		t.Fatalf("envvars set: %v", err)
	}
	data, err := h.handleEnvVarsList(ctx, nil)
	if err != nil {
		t.Fatalf("envvars list: %v", err)
	}
	if got := data.(map[string]any)["count"]; got != 1 {
		t.Errorf("count = %v", got)
	}
	if _, err := h.handleEnvVarsDelete(ctx, spec.Args{"key": "AGENTCLI_TEST_VAR"}); err != nil {
		t.Fatalf("envvars delete: %v", err)
	}
	if _, err := h.handleEnvVarsDelete(ctx, spec.Args{"key": "AGENTCLI_TEST_VAR"}); specErrCode(t, err) != spec.ErrNotFound {
		t.Errorf("重复删除应 not_found: %v", err)
	}
	if _, err := h.handleEnvVarsSet(ctx, spec.Args{"key": "BAD=KEY", "value": "x"}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("非法键应 bad_args: %v", err)
	}
}

func TestAgentCLIWorkDirHandlers(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	added, err := h.handleWorkdirAdd(ctx, spec.Args{"path": dir, "label": "测试目录"})
	if err != nil {
		t.Fatalf("workdir add: %v", err)
	}
	if !strings.Contains(mustJSON(t, added), "测试目录") {
		t.Errorf("add 返回未包含新目录: %s", mustJSON(t, added))
	}
	listed, err := h.handleWorkdirList(ctx, nil)
	if err != nil {
		t.Fatalf("workdir list: %v", err)
	}
	if !strings.Contains(mustJSON(t, listed), "测试目录") {
		t.Errorf("list 未包含新增目录: %s", mustJSON(t, listed))
	}
	if _, err := h.handleWorkdirRemove(ctx, spec.Args{"path": dir}); err != nil {
		t.Fatalf("workdir remove: %v", err)
	}
	listed2, _ := h.handleWorkdirList(ctx, nil)
	if strings.Contains(mustJSON(t, listed2), "测试目录") {
		t.Errorf("remove 后目录仍存在")
	}
}

// TestAgentCLIHandlersNeverLeakPlaintextSecrets 脱敏总检：写入已知明文密钥后，
// 遍历 D1 全部只读 Handler，断言序列化输出不含明文，也不含 api_key 字段名。
func TestAgentCLIHandlersNeverLeakPlaintextSecrets(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()

	const testKey = "sk-agentcli-PLAINTEXT-4f5e6d7c8b"
	// 真实 ghp_ 令牌形态（下划线、无连字符），供 logs 的形态清洗命中。
	const ghToken = "ghp_agentcliPLAINTEXT1a2b3c4d5e"

	// 铺底：provider（含明文遗留字段与认证 header，模拟历史脏数据）+ 密钥 +
	// settings 凭据 + 敏感环境变量 + 含密钥的日志。
	dirty := config.Provider{DefaultModel: "m"}
	dirty.OpenAI = &config.OpenAIFormat{
		Enabled: true, APIKey: testKey, BaseURL: "https://api.acme.test",
		Headers: map[string]string{
			"Authorization": "Bearer " + testKey,
			"X-Safe":        "plain-safe-value",
		},
	}
	if err := app.Config.SaveProvider("acme", dirty); err != nil {
		t.Fatal(err)
	}
	if err := app.saveImportedProviderAPIKey("acme", testKey); err != nil {
		t.Fatal(err)
	}
	if err := app.Settings.SetGitHubToken(ghToken); err != nil {
		t.Fatal(err)
	}
	if err := app.SetEnvVar("AGENTCLI_SECRETISH", "value-not-a-key"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetEnvVar("AGENTCLI_API_KEY", testKey); err != nil {
		t.Fatal(err)
	}
	app.Log.Error("app", "鉴权失败重试 "+testKey, "detail 含 "+ghToken)

	handlers := map[string]func() (any, error){
		"cli.info":      func() (any, error) { return h.handleCLIInfo(ctx, nil) },
		"env.status":    func() (any, error) { return h.handleEnvStatus(ctx, nil) },
		"remote.status": func() (any, error) { return h.handleRemoteStatus(ctx, nil) },
		"provider.list": func() (any, error) { return h.handleProviderList(ctx, spec.Args{}) },
		"provider.get":  func() (any, error) { return h.handleProviderGet(ctx, spec.Args{"name": "acme"}) },
		"key.status":    func() (any, error) { return h.handleKeyStatus(ctx, spec.Args{}) },
		"preset.list":   func() (any, error) { return h.handlePresetList(ctx, spec.Args{"type": "claude_code"}) },
		"settings.get":  func() (any, error) { return h.handleSettingsGet(ctx, nil) },
		"envvars.list":  func() (any, error) { return h.handleEnvVarsList(ctx, nil) },
		// 写回显同样不得包含明文（敏感键掩码）。
		"envvars.set": func() (any, error) {
			return h.handleEnvVarsSet(ctx, spec.Args{"key": "AGENTCLI_ECHO_TOKEN", "value": testKey})
		},
		"workdir.list":  func() (any, error) { return h.handleWorkdirList(ctx, nil) },
		"usage.summary": func() (any, error) { return h.handleUsageSummary(ctx, spec.Args{}) },
		"logs":          func() (any, error) { return h.handleLogs(ctx, spec.Args{}) },
		"git.repo-info": func() (any, error) { return h.handleGitRepoInfo(ctx, spec.Args{"dir": t.TempDir()}) },
		"git.branches": func() (any, error) {
			_, err := h.handleGitBranches(ctx, spec.Args{"dir": t.TempDir()})
			return nil, err // 非 git 目录 → not_found，错误消息也不应含明文
		},
	}
	for name, fn := range handlers {
		data, err := fn()
		payload := mustJSON(t, data) + errText(err)
		if strings.Contains(payload, testKey) {
			t.Errorf("%s 输出泄露明文 API key", name)
		}
		if strings.Contains(payload, ghToken) {
			t.Errorf("%s 输出泄露明文 github token", name)
		}
		if name == "provider.list" || name == "provider.get" {
			lower := strings.ToLower(payload)
			if strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") || strings.Contains(lower, "authkey") {
				t.Errorf("%s 输出含密钥字段名: %s", name, payload)
			}
		}
	}
	// 密钥错误回显也不得包含明文（Secret 参数脱敏属传输/审计层，这里锚定值不进结果）。
	_, err := h.handleProviderKeySet(ctx, spec.Args{"name": "acme"})
	if strings.Contains(errText(err), testKey) {
		t.Errorf("key-set 错误回显含明文")
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// jsonEqual 语义比较两段 JSON（保存链路的 MarshalIndent 会重排 RawMessage
// 的空白，字节比较会误报丢失）。
func jsonEqual(t *testing.T, a json.RawMessage, b string) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &vb); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(va) == fmt.Sprint(vb)
}

func presetKeysOf(m map[string]config.Preset) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- D-01 回归：认证类 header 脱敏 ----

// TestAgentCLIProviderGetListMaskAuthHeaders provider get/list 输出对认证类
// header（authorization / x-api-key / token / cookie 等）一律掩码并附
// headersSet 布尔；非认证 header 原样保留。修复前明文直接回显。
func TestAgentCLIProviderGetListMaskAuthHeaders(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	seedAgentCLIModelsJSON(t, app, `{
  "models": {
    "hdr": {
      "type": "openai",
      "default_model": "m",
      "openai": {
        "enabled": true,
        "base_url": "https://api.hdr.test/v1",
        "headers": {
          "Authorization": "Bearer sk-HDR-PLAIN-000111aaa",
          "x-api-key": "sk-HDR-PLAIN-222333bbb",
          "X-Auth-Token": "tok-HDR-PLAIN-444555ccc",
          "Set-Cookie": "session=HDR-PLAIN-666777ddd",
          "X-Custom": "keep-me"
        }
      }
    }
  }
}`)
	ctx := context.Background()
	for name, call := range map[string]func() (any, error){
		"provider.get":  func() (any, error) { return h.handleProviderGet(ctx, spec.Args{"name": "hdr"}) },
		"provider.list": func() (any, error) { return h.handleProviderList(ctx, spec.Args{}) },
	} {
		data, err := call()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := mustJSON(t, data)
		for _, plain := range []string{
			"sk-HDR-PLAIN-000111aaa", "sk-HDR-PLAIN-222333bbb",
			"tok-HDR-PLAIN-444555ccc", "session=HDR-PLAIN-666777ddd",
		} {
			if strings.Contains(out, plain) {
				t.Errorf("%s 输出泄露认证类 header 明文 %s", name, plain)
			}
		}
		for _, header := range []string{"Authorization", "x-api-key", "X-Auth-Token", "Set-Cookie"} {
			if !strings.Contains(out, `"`+header+`":"***"`) {
				t.Errorf("%s 认证类 header %s 未掩码: %s", name, header, out)
			}
		}
		if !strings.Contains(out, `"headersSet"`) || !strings.Contains(out, `"Authorization":true`) {
			t.Errorf("%s 缺少 headersSet 布尔: %s", name, out)
		}
		if !strings.Contains(out, `"X-Custom":"keep-me"`) {
			t.Errorf("%s 非认证 header 被误掩码: %s", name, out)
		}
	}
}

// ---- D-01(b) 回归：save 侧认证 header 写入边界 ----

// TestAgentCLIProviderSaveAuthHeaderBoundaries 明文认证 header 经 provider save
// 写入必须被拒绝（提示改用桌面应用或 key-set）；掩码占位保留现有真实值；
// $ENV: 引用按引用原样保存；空串删除 header。
func TestAgentCLIProviderSaveAuthHeaderBoundaries(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	seedAgentCLIModelsJSON(t, app, `{
  "models": {
    "acme": {
      "type": "openai",
      "default_model": "m",
      "openai": {
        "enabled": true,
        "base_url": "https://api.acme.test/v1",
        "headers": {
          "Authorization": "Bearer sk-AUTH-PLAIN-aabbccddee",
          "X-Custom": "keep-me"
        }
      }
    }
  }
}`)

	// 明文认证 header：拒绝并提示改用桌面应用或 key-set。
	for _, dto := range []string{
		`{"openai":{"headers":{"Authorization":"Bearer sk-NEW-PLAIN-112233"}}}`,
		`{"openai":{"headers":{"x-api-key":"sk-NEW-PLAIN-445566"}}}`,
	} {
		_, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": dto})
		if code := specErrCode(t, err); code != spec.ErrBadArgs {
			t.Fatalf("明文认证 header 应拒绝 bad_args，得到 %s（%v）", code, err)
		}
		if !strings.Contains(err.Error(), "key-set") && !strings.Contains(err.Error(), "桌面") {
			t.Errorf("拒绝信息应提示桌面应用/key-set：%v", err)
		}
	}
	// 拒绝后现有配置不受影响。
	p, err := app.Config.GetProvider("acme")
	if err != nil {
		t.Fatal(err)
	}
	if p.OpenAI.Headers["Authorization"] != "Bearer sk-AUTH-PLAIN-aabbccddee" {
		t.Fatalf("拒绝路径不应改写现有 header：%q", p.OpenAI.Headers["Authorization"])
	}

	// 掩码占位 = 保留现有真实值；同批其他 header 正常覆盖。
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": `{"openai":{"headers":{"Authorization":"***","X-Custom":"changed"}}}`}); err != nil {
		t.Fatalf("掩码占位保存应成功: %v", err)
	}
	p2, err := app.Config.GetProvider("acme")
	if err != nil {
		t.Fatal(err)
	}
	if got := p2.OpenAI.Headers["Authorization"]; got != "Bearer sk-AUTH-PLAIN-aabbccddee" {
		t.Errorf("掩码占位应保留真实 header，实际 %q", got)
	}
	if got := p2.OpenAI.Headers["X-Custom"]; got != "changed" {
		t.Errorf("非认证 header 未生效：%q", got)
	}

	// $ENV: 引用不是明文，按引用原样保存。
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": `{"openai":{"headers":{"Authorization":"$ENV:ACME_TOKEN"}}}`}); err != nil {
		t.Fatalf("$ENV 引用保存应成功: %v", err)
	}
	p3, _ := app.Config.GetProvider("acme")
	if got := p3.OpenAI.Headers["Authorization"]; got != "$ENV:ACME_TOKEN" {
		t.Errorf("$ENV 引用应原样保存，实际 %q", got)
	}

	// 空串删除 header。
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": `{"openai":{"headers":{"Authorization":""}}}`}); err != nil {
		t.Fatalf("空串删除 header 应成功: %v", err)
	}
	p4, _ := app.Config.GetProvider("acme")
	if _, exists := p4.OpenAI.Headers["Authorization"]; exists {
		t.Errorf("空串应删除 header，实际 %q", p4.OpenAI.Headers["Authorization"])
	}
}

// ---- D-02 回归：provider get→save 无损往返 ----

// TestAgentCLIProviderSaveMergeRoundTrip get→save 无改动往返必须无损：
// wire_api、url_history、preset 的 opencode_config、掩码 header 真实值
// 均原样保留；部分更新只覆盖 DTO 出现的字段。
func TestAgentCLIProviderSaveMergeRoundTrip(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	const authPlain = "Bearer sk-ROUNDTRIP-PLAIN-a1b2c3d4"
	seedAgentCLIModelsJSON(t, app, `{
  "models": {
    "acme": {
      "type": "openai",
      "default_model": "m-main",
      "url_history": ["https://old.acme.test/v1", "https://api.acme.test/v1"],
      "openai": {
        "enabled": true,
        "base_url": "https://api.acme.test/v1",
        "wire_api": "responses",
        "headers": {
          "Authorization": "`+authPlain+`",
          "X-Custom": "keep-me"
        }
      },
      "presets": {
        "daily": {
          "name": "Daily",
          "model": "m-daily",
          "opencode_config": {"custom":"preserve-me"}
        }
      }
    }
  }
}`)

	got, err := h.handleProviderGet(ctx, spec.Args{"name": "acme"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	viewJSON := mustJSON(t, got)
	if !strings.Contains(viewJSON, `"Authorization":"***"`) || !strings.Contains(viewJSON, `"headersSet"`) {
		t.Fatalf("get 视图应掩码认证 header：%s", viewJSON)
	}
	if !strings.Contains(viewJSON, `"opencodeConfig":{"custom":"preserve-me"}`) {
		t.Fatalf("get 视图应透出 preset 的 opencodeConfig：%s", viewJSON)
	}

	// 无改动往返：把 get 输出原样存回。
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": viewJSON}); err != nil {
		t.Fatalf("round-trip save: %v", err)
	}
	after, err := app.Config.GetProvider("acme")
	if err != nil {
		t.Fatal(err)
	}
	if after.DefaultModel != "m-main" {
		t.Errorf("defaultModel 丢失：%q", after.DefaultModel)
	}
	if after.OpenAI == nil || after.OpenAI.WireAPI != "responses" {
		t.Errorf("wire_api 在往返后丢失（scrubProviderAPIKeys 上游缺陷未被规避）：%+v", after.OpenAI)
	}
	if after.OpenAI == nil || after.OpenAI.Headers["Authorization"] != authPlain {
		t.Errorf("掩码占位应保留真实 header，实际 %q", after.OpenAI.Headers["Authorization"])
	}
	if after.OpenAI == nil || after.OpenAI.Headers["X-Custom"] != "keep-me" {
		t.Errorf("非认证 header 在往返后丢失：%+v", after.OpenAI)
	}
	if len(after.UrlHistory) != 2 {
		t.Errorf("url_history 在往返后丢失：%+v", after.UrlHistory)
	}
	preset, ok := after.Presets["daily"]
	if !ok {
		t.Fatalf("preset daily 在往返后丢失：%+v", after.Presets)
	}
	if len(after.Presets) != 1 {
		t.Fatalf("往返不应产生重复 preset（key 与 Name 可不同），实际 %d 个：%v", len(after.Presets), presetKeysOf(after.Presets))
	}
	if !jsonEqual(t, preset.OpenCodeConfig, `{"custom":"preserve-me"}`) {
		t.Errorf("preset 的 opencode_config 在往返后丢失/变形：%s", string(preset.OpenCodeConfig))
	}
	if preset.Model != "m-daily" {
		t.Errorf("preset model 丢失：%q", preset.Model)
	}

	// 部分更新：只改 defaultModel，其余（wire_api/header/url_history/preset）保留。
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": `{"defaultModel":"m-main-2"}`}); err != nil {
		t.Fatalf("partial save: %v", err)
	}
	part, err := app.Config.GetProvider("acme")
	if err != nil {
		t.Fatal(err)
	}
	if part.DefaultModel != "m-main-2" || part.OpenAI.WireAPI != "responses" ||
		part.OpenAI.Headers["Authorization"] != authPlain || len(part.UrlHistory) != 2 ||
		len(part.Presets) != 1 ||
		!jsonEqual(t, part.Presets["daily"].OpenCodeConfig, `{"custom":"preserve-me"}`) {
		t.Errorf("部分更新丢失了 DTO 未提及的字段：%+v", part)
	}

	// 保存后的视图依旧掩码，绝不回显明文。
	got2, err := h.handleProviderGet(ctx, spec.Args{"name": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	out2 := mustJSON(t, got2)
	if strings.Contains(out2, "sk-ROUNDTRIP-PLAIN") {
		t.Errorf("保存后 get 视图泄露明文 header：%s", out2)
	}
	if !strings.Contains(out2, `"Authorization":"***"`) {
		t.Errorf("保存后 get 视图应掩码：%s", out2)
	}
}

// ---- D-01(a) 回归：敏感环境变量脱敏 ----

// TestAgentCLIEnvVarsSensitiveValueMasking 名称含 KEY/TOKEN/SECRET/PASSWORD/
// PASSWD/CREDENTIAL/AUTH 的环境变量：set 回显与 list 输出一律掩码并带
// valueSet 布尔；非敏感变量原样回显；底层存储保留真实值。
func TestAgentCLIEnvVarsSensitiveValueMasking(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	const secret = "sk-ENVVAR-PLAIN-aabbccdd9988"

	set, err := h.handleEnvVarsSet(ctx, spec.Args{"key": "OPENAI_API_KEY", "value": secret})
	if err != nil {
		t.Fatalf("envvars set: %v", err)
	}
	if out := mustJSON(t, set); strings.Contains(out, secret) {
		t.Errorf("envvars set 回显明文：%s", out)
	} else if !strings.Contains(out, "***") && !strings.Contains(out, `"valueSet":true`) {
		t.Errorf("envvars set 回显未掩码：%s", out)
	}

	if _, err := h.handleEnvVarsSet(ctx, spec.Args{"key": "AGENTCLI_PLAIN", "value": "hello-world"}); err != nil {
		t.Fatal(err)
	}

	list, err := h.handleEnvVarsList(ctx, nil)
	if err != nil {
		t.Fatalf("envvars list: %v", err)
	}
	out := mustJSON(t, list)
	if strings.Contains(out, secret) {
		t.Errorf("envvars list 泄露敏感值明文：%s", out)
	}
	if !strings.Contains(out, `"key":"OPENAI_API_KEY","value":"***"`) || !strings.Contains(out, `"valueSet":true`) {
		t.Errorf("敏感变量应掩码并带 valueSet：%s", out)
	}
	if !strings.Contains(out, `"AGENTCLI_PLAIN","value":"hello-world"`) {
		t.Errorf("非敏感变量应原样回显：%s", out)
	}

	// 底层存储保留真实值（脱敏只在输出侧）。
	vars, err := app.GetEnvVars()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vars {
		if v.Key == "OPENAI_API_KEY" {
			found = true
			if v.Value != secret {
				t.Errorf("底层存储被掩码污染：%q", v.Value)
			}
		}
	}
	if !found {
		t.Fatal("未找到已写入的变量")
	}
}

// TestAgentCLIPresetSavePreservesOpenCodeCfg D-02（preset 侧）：preset save 的
// json 部分更新不携带 opencodeCfg 时原样保留；显式给出时覆盖。
func TestAgentCLIPresetSavePreservesOpenCodeCfg(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()

	if _, err := h.handlePresetSave(ctx, spec.Args{
		"type": "claude_code", "name": "daily",
		"json": `{"provider":"acme","model":"m-daily","opencodeCfg":{"custom":"preserve-me"}}`,
	}); err != nil {
		t.Fatalf("preset save 初始: %v", err)
	}
	// 部分更新只改 model：opencodeCfg 未提及必须保留。
	if _, err := h.handlePresetSave(ctx, spec.Args{
		"type": "claude_code", "name": "daily", "json": `{"model":"m-daily2"}`,
	}); err != nil {
		t.Fatalf("preset save 部分更新: %v", err)
	}
	presets, err := app.GetTerminalPresets("claude_code")
	if err != nil {
		t.Fatal(err)
	}
	tp, ok := presets["daily"]
	if !ok {
		t.Fatalf("preset daily 丢失：%v", presets)
	}
	if tp.Model != "m-daily2" {
		t.Errorf("model 未更新：%q", tp.Model)
	}
	if !jsonEqual(t, tp.OpenCodeCfg, `{"custom":"preserve-me"}`) {
		t.Errorf("opencodeCfg 在部分更新后丢失/变形：%s", string(tp.OpenCodeCfg))
	}
	// 显式给出时覆盖。
	if _, err := h.handlePresetSave(ctx, spec.Args{
		"type": "claude_code", "name": "daily", "json": `{"opencodeCfg":{"custom":"replaced"}}`,
	}); err != nil {
		t.Fatalf("preset save 覆盖: %v", err)
	}
	presets2, _ := app.GetTerminalPresets("claude_code")
	if !jsonEqual(t, presets2["daily"].OpenCodeCfg, `{"custom":"replaced"}`) {
		t.Errorf("显式 opencodeCfg 未覆盖：%s", string(presets2["daily"].OpenCodeCfg))
	}
}
