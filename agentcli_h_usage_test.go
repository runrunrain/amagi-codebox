package main

import (
	"context"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/usage"
)

func TestAgentCLIUsageSummaryEmpty(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	data, err := h.handleUsageSummary(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	summary := data.(usage.Summary)
	if summary.TotalRequests != 0 || summary.TotalCostByCurrency == nil {
		t.Errorf("空库 summary 不符: %+v", summary)
	}
}

func TestAgentCLIUsageFilterValidation(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	cases := []struct {
		name string
		args spec.Args
	}{
		{"非法日期", spec.Args{"startDate": "2026/07/01", "endDate": "2026-07-31"}},
		{"非法月份", spec.Args{"startDate": "2026-13-01", "endDate": "2026-13-31"}},
		{"日期不成对", spec.Args{"startDate": "2026-07-01"}},
		{"非法 granularity", spec.Args{"granularity": "month"}},
		{"days 非正", spec.Args{"days": -1}},
		{"page 非正", spec.Args{"page": 0}},
		{"pageSize 超限", spec.Args{"pageSize": 501}},
	}
	for _, cmd := range []struct {
		name string
		fn   func(spec.Args) (any, error)
	}{
		{"summary", func(a spec.Args) (any, error) { return h.handleUsageSummary(ctx, a) }},
		{"daily", func(a spec.Args) (any, error) { return h.handleUsageDaily(ctx, a) }},
		{"requests", func(a spec.Args) (any, error) { return h.handleUsageRequests(ctx, a) }},
	} {
		for _, c := range cases {
			// 每条非法参数只对声明了该参数的命令生效；未声明参数被忽略不报错，
			// 因此这里只断言“若返回错误必须是 bad_args”。
			_, err := cmd.fn(c.args)
			if err != nil {
				if code := specErrCode(t, err); code != spec.ErrBadArgs {
					t.Errorf("%s %s: 错误码 = %s (%v), want bad_args", cmd.name, c.name, code, err)
				}
			}
		}
	}
}

func TestAgentCLIUsageDailyRequestsEmpty(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	daily, err := h.handleUsageDaily(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	// 空库也返回按日零填充的曲线（默认 30 天）。
	if daily.(map[string]any)["count"] != 30 {
		t.Errorf("daily count = %v, want 30", daily.(map[string]any)["count"])
	}
	reqs, err := h.handleUsageRequests(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("requests: %v", err)
	}
	if reqs.(map[string]any)["pageSize"] != 50 || reqs.(map[string]any)["page"] != 1 {
		t.Errorf("requests 分页默认值不符: %v", reqs)
	}
}

func TestAgentCLIUsageModelsProvidersEmpty(t *testing.T) {
	h, _, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	if _, err := h.handleUsageModels(ctx, spec.Args{}); err != nil {
		t.Fatalf("models: %v", err)
	}
	if _, err := h.handleUsageProviders(ctx, spec.Args{"appType": "codex"}); err != nil {
		t.Fatalf("providers: %v", err)
	}
}

func TestAgentCLIUsageNotLoadedMapsNotReady(t *testing.T) {
	// 未 Load 的 usage 服务 → not_ready（不触发真实同步/查询）。
	app, configDir := newTestAppWithConfigDir(t)
	app.Usage = usage.NewService(configDir, app.Log)
	h := newAgentCLIHost(app)
	ctx := context.Background()
	for name, fn := range map[string]func() (any, error){
		"summary": func() (any, error) { return h.handleUsageSummary(ctx, spec.Args{}) },
		"sync":    func() (any, error) { return h.handleUsageSync(ctx, nil) },
	} {
		_, err := fn()
		if code := specErrCode(t, err); code != spec.ErrNotReady {
			t.Errorf("%s 未加载服务应 not_ready，实际 %v", name, err)
		}
	}
}

func TestAgentCLILogsHandler(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	all, err := h.handleLogs(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	baseline := all.(map[string]any)["count"] // 服务启动日志等既有条数
	app.Log.Info("app", "普通消息 agentcli-ok")
	app.Log.Warn("session", "警告消息 agentcli-ok", "detail-agentcli")
	app.Log.Error("usage", "错误消息 agentcli-nope")

	all2, err := h.handleLogs(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if got := all2.(map[string]any)["count"]; got != baseline.(int)+3 {
		t.Errorf("count = %v, want %v", got, baseline.(int)+3)
	}
	warnOnly, err := h.handleLogs(ctx, spec.Args{"level": "WARN"})
	if err != nil {
		t.Fatalf("logs level: %v", err)
	}
	if got := warnOnly.(map[string]any)["count"]; got != 2 { // WARN 及以上
		t.Errorf("WARN+ count = %v, want 2", got)
	}
	kw, err := h.handleLogs(ctx, spec.Args{"keyword": "agentcli-nope"})
	if err != nil {
		t.Fatalf("logs keyword: %v", err)
	}
	if got := kw.(map[string]any)["count"]; got != 1 {
		t.Errorf("keyword count = %v, want 1", got)
	}
	lim, err := h.handleLogs(ctx, spec.Args{"limit": 2})
	if err != nil {
		t.Fatalf("logs limit: %v", err)
	}
	if got := lim.(map[string]any)["count"]; got != 2 {
		t.Errorf("limit count = %v, want 2", got)
	}
	if _, err := h.handleLogs(ctx, spec.Args{"level": "TRACE"}); specErrCode(t, err) != spec.ErrBadArgs {
		t.Errorf("非法 level 应 bad_args: %v", err)
	}
}

// TestAgentCLILogsScrubCredentials D-01(c) 回归：logs 输出对常见凭据形态做
// 清洗——已存储 provider key 的精确值、Authorization/Bearer 值、sk-/ghp_/xox
// 等常见 token 前缀——一律替换为掩码。修复前原样回显。
func TestAgentCLILogsScrubCredentials(t *testing.T) {
	h, app, _ := newAgentCLIHostFixture(t)
	ctx := context.Background()
	const storedKey = "sk-LOGS-PLAIN-a1b2c3d4e5f6"

	// 已存储的 provider key（精确值替换的数据源）。
	if _, err := h.handleProviderSave(ctx, spec.Args{"name": "acme", "json": `{"openai":{"enabled":true,"baseUrl":"https://api.acme.test/v1"}}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.handleProviderKeySet(ctx, spec.Args{"name": "acme", "key": storedKey}); err != nil {
		t.Fatal(err)
	}
	// 日志正文/详情含多种凭据形态。
	app.Log.Error("session", "调用失败 Authorization: Bearer "+storedKey,
		"resp: ghp_abcdefghij0123456789 token=sk-OTHER-PLAIN-11223344 xoxb-1122334455667788990011 api-key:sk-yet-PLAIN-77889900")

	data, err := h.handleLogs(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	out := mustJSON(t, data)
	for _, secret := range []string{
		storedKey,
		"ghp_abcdefghij0123456789",
		"sk-OTHER-PLAIN-11223344",
		"xoxb-1122334455667788990011",
		"sk-yet-PLAIN-77889900",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("logs 输出泄露凭据 %s：%s", secret, out)
		}
	}
	if !strings.Contains(out, "***") {
		t.Errorf("logs 输出应含掩码占位：%s", out)
	}
}

// TestAgentCLIScrubCredentials 凭据清洗纯函数的行为锚点（D-01c）：
// 已知密钥精确替换、Authorization/Bearer 值、常见 token 前缀族；
// 普通文本（含 skill- 这类含 sk 的普通词）不被误伤。
func TestAgentCLIScrubCredentials(t *testing.T) {
	const known = "sk-KNOWN-PLAIN-112233445566"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"已知密钥精确替换", "failed with " + known + " in body", "failed with *** in body"},
		{"Authorization 头部值", `Authorization: Bearer abc-cred-12345678`, `Authorization: ***`},
		{"authorization= 形态", `authorization=abcdef1234567890`, `authorization=***`},
		{"x-api-key 头部值", `X-Api-Key: verysecretkey000111`, `X-Api-Key: ***`},
		{"独立 Bearer", `resp bearer YaHo.token-value99`, `resp bearer ***`},
		{"sk- 前缀", `token=sk-proj-abcdef1234567890 end`, `token=*** end`},
		{"ghp_ 前缀", `see ghp_abcdefghij0123456789 now`, `see *** now`},
		{"xoxb- 前缀", `slack xoxb-11223344556677889900 ok`, `slack *** ok`},
		{"glpat- 前缀", `gitlab glpat-abcdefghijklmnopqrst done`, `gitlab *** done`},
		{"AKIA 前缀", `aws AKIAIOSFODNN7EXAMPLE key`, `aws *** key`},
		{"普通词不误伤", "task-management skill-development awesome", "task-management skill-development awesome"},
		{"普通文本不变", "hello world 123", "hello world 123"},
	}
	for _, c := range cases {
		if got := agentCLIScrubCredentials(c.in, []string{known}); got != c.want {
			t.Errorf("%s：\n got=%q\nwant=%q", c.name, got, c.want)
		}
	}
	// 已知密钥优先于形态掩码；短值（<8 字符）不参与精确替换。
	if got := agentCLIScrubCredentials("v=short", []string{"short"}); got != "v=short" {
		t.Errorf("短值不应参与精确替换：%q", got)
	}
	if got := agentCLIScrubCredentials("a sk-KNOWN-PLAIN-112233445566 b "+known, []string{known}); got != "a *** b ***" {
		t.Errorf("多次出现都应替换：%q", got)
	}
}
