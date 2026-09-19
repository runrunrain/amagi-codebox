package config

// quota_probe_test.go — 额度探测（额度查询与常显设计方案 §2/§4/§9）单测：
// GLM/DeepSeek httptest 三形态、Codex 本地 rollout fixture（多事件取最新、
// 大文件尾部截断、无文件 error 态）、家族判别表驱动、models.json 往返兼容。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// quotaTestServer 可编程的额度假端点：path/status/body 三元组按序应答，
// authHeader 记录最近一次 Authorization 头（供鉴权形态断言）。
type quotaTestServer struct {
	mu         sync.Mutex
	path       string
	status     int
	body       string
	authHeader string
	calls      int
}

func (s *quotaTestServer) set(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body = status, body
}

func (s *quotaTestServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls++
		s.authHeader = r.Header.Get("Authorization")
		status, body := s.status, s.body
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func newGLMTestServer(t *testing.T, status int, body string) (*quotaTestServer, *httptest.Server) {
	t.Helper()
	s := &quotaTestServer{path: "/api/monitor/usage/quota/limit", status: status, body: body}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return s, srv
}

// GLM 正向：code==200 信封 → ok；TIME_LIMIT 为主窗口（排在首位），其余
// 带 percentage/remaining 的条目为 secondary；数值字段字符串/数字混排宽松
// 解析；level trim；无数据条目（EMPTY）跳过；鉴权头无 Bearer 前缀。
func TestProbeGLMQuota_OK(t *testing.T) {
	body := `{"code":200,"msg":"success","data":{"level":" pro ",` +
		`"limits":[{"type":"OTHER","unit":"prompts","number":"120","usage":30,"percentage":31.5,"remaining":80},` +
		`{"type":"TIME_LIMIT","unit":"prompts","number":300,"usage":180,"currentValue":6,"percentage":62.0,"remaining":114},` +
		`{"type":"EMPTY","unit":"prompts"}]}}`
	s, srv := newGLMTestServer(t, http.StatusOK, body)

	entry := ProbeGLMQuota(context.Background(), srv.Client(), srv.URL, "coding-plan-key", "glm")
	if entry.Status != QuotaStatusOK {
		t.Fatalf("status = %q, want ok (message=%q)", entry.Status, entry.Message)
	}
	if entry.Family != QuotaFamilyGLMBigmodel || entry.Source != QuotaSourceGLMAPI || entry.Provider != "glm" {
		t.Errorf("entry meta = family:%s source:%s provider:%s", entry.Family, entry.Source, entry.Provider)
	}
	if entry.Level != "pro" {
		t.Errorf("level = %q, want trimmed %q", entry.Level, "pro")
	}
	if len(entry.Windows) != 2 {
		t.Fatalf("windows = %+v, want 2 (primary + secondary)", entry.Windows)
	}
	if w := entry.Windows[0]; w.Kind != QuotaWindowPrimary || w.UsedPercent != 62.0 || w.Remaining != 114 {
		t.Errorf("primary window = %+v, want TIME_LIMIT percentage=62 remaining=114", w)
	}
	if w := entry.Windows[1]; w.Kind != QuotaWindowSecondary || w.UsedPercent != 31.5 || w.Remaining != 80 {
		t.Errorf("secondary window = %+v, want percentage=31.5 remaining=80", w)
	}
	if s.authHeader != "coding-plan-key" {
		t.Errorf("Authorization header = %q, want raw key without Bearer prefix", s.authHeader)
	}
	if entry.ProbedAt == "" {
		t.Error("ProbedAt must be set (RFC3339)")
	}
}

// GLM 无套餐：code!=200 或 msg 命中关键词 → no_plan。
func TestProbeGLMQuota_NoPlan(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"code!=200", `{"code":404,"msg":"订阅不存在","success":false}`},
		{"msg 关键词-不存在coding plan", `{"code":200,"msg":"该 key 不存在coding plan 套餐","success":false}`},
		{"msg 关键词-没有资格", `{"code":200,"msg":"当前账号没有资格使用该接口"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := newGLMTestServer(t, http.StatusOK, tc.body)
			entry := ProbeGLMQuota(context.Background(), srv.Client(), srv.URL, "k", "glm")
			if entry.Status != QuotaStatusNoPlan {
				t.Fatalf("status = %q, want no_plan (message=%q)", entry.Status, entry.Message)
			}
			if entry.Message == "" {
				t.Error("no_plan must carry msg 概要 in Message")
			}
		})
	}
}

// GLM 鉴权失败：HTTP 401/403 或信封 code==1001 → no_key。
func TestProbeGLMQuota_NoKey(t *testing.T) {
	t.Run("HTTP 401", func(t *testing.T) {
		_, srv := newGLMTestServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
		entry := ProbeGLMQuota(context.Background(), srv.Client(), srv.URL, "bad", "glm")
		if entry.Status != QuotaStatusNoKey {
			t.Fatalf("status = %q, want no_key", entry.Status)
		}
	})
	t.Run("code=1001", func(t *testing.T) {
		_, srv := newGLMTestServer(t, http.StatusOK, `{"code":1001,"msg":"令牌无效","success":false}`)
		entry := ProbeGLMQuota(context.Background(), srv.Client(), srv.URL, "bad", "glm")
		if entry.Status != QuotaStatusNoKey {
			t.Fatalf("status = %q, want no_key (message=%q)", entry.Status, entry.Message)
		}
		if !strings.Contains(entry.Message, "令牌无效") {
			t.Errorf("message = %q, want msg 概要", entry.Message)
		}
	})
}

// GLM 未决：网络/取消/解析失败 → error（不落 ok 缓存由 RecordProviderQuota 兜底）。
func TestProbeGLMQuota_Error(t *testing.T) {
	t.Run("ctx 取消", func(t *testing.T) {
		_, srv := newGLMTestServer(t, http.StatusOK, `{}`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		entry := ProbeGLMQuota(ctx, srv.Client(), srv.URL, "k", "glm")
		if entry.Status != QuotaStatusError {
			t.Fatalf("status = %q, want error", entry.Status)
		}
	})
	t.Run("响应体非 JSON", func(t *testing.T) {
		_, srv := newGLMTestServer(t, http.StatusOK, `<html>gateway error</html>`)
		entry := ProbeGLMQuota(context.Background(), srv.Client(), srv.URL, "k", "glm")
		if entry.Status != QuotaStatusError {
			t.Fatalf("status = %q, want error", entry.Status)
		}
	})
	t.Run("信封 ok 但 limits 全空", func(t *testing.T) {
		_, srv := newGLMTestServer(t, http.StatusOK, `{"code":200,"msg":"ok","data":{"limits":[]}}`)
		entry := ProbeGLMQuota(context.Background(), srv.Client(), srv.URL, "k", "glm")
		if entry.Status != QuotaStatusError {
			t.Fatalf("status = %q, want error (无可用限额数据不得记空 ok)", entry.Status)
		}
	})
}

// DeepSeek 正向：字符串数字 → float 余额；Bearer 头；is_available=false 标注。
func TestProbeDeepSeekQuota_OK(t *testing.T) {
	s := &quotaTestServer{path: "/user/balance", status: http.StatusOK,
		body: `{"is_available":true,"balance_infos":[{"total_balance":"128.50","granted_balance":"10.00","topped_up_balance":"118.50","currency":"CNY"}]}`}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	entry := ProbeDeepSeekQuota(context.Background(), srv.Client(), srv.URL, "sk-ds", "deepseek")
	if entry.Status != QuotaStatusOK {
		t.Fatalf("status = %q, want ok", entry.Status)
	}
	if entry.Family != QuotaFamilyDeepSeek || entry.Source != QuotaSourceDeepSeekAPI {
		t.Errorf("entry meta = family:%s source:%s", entry.Family, entry.Source)
	}
	b := entry.Balance
	if b == nil {
		t.Fatal("balance must be set")
	}
	if b.Currency != "CNY" || b.Total != 128.5 || b.Granted != 10 || b.ToppedUp != 118.5 {
		t.Errorf("balance = %+v, want CNY 128.5/10/118.5", b)
	}
	if entry.Message != "" {
		t.Errorf("available account must not carry message, got %q", entry.Message)
	}
	if s.authHeader != "Bearer sk-ds" {
		t.Errorf("Authorization header = %q, want Bearer form", s.authHeader)
	}
}

// DeepSeek 401 → no_key；无 balance_infos → error；is_available=false → 标注。
func TestProbeDeepSeekQuota_SpecialStates(t *testing.T) {
	t.Run("HTTP 401", func(t *testing.T) {
		s := &quotaTestServer{path: "/user/balance", status: http.StatusUnauthorized, body: `{"error":"auth"}`}
		srv := httptest.NewServer(s.handler())
		defer srv.Close()
		entry := ProbeDeepSeekQuota(context.Background(), srv.Client(), srv.URL, "bad", "deepseek")
		if entry.Status != QuotaStatusNoKey {
			t.Fatalf("status = %q, want no_key", entry.Status)
		}
	})
	t.Run("空 balance_infos", func(t *testing.T) {
		s := &quotaTestServer{path: "/user/balance", status: http.StatusOK, body: `{"is_available":true,"balance_infos":[]}`}
		srv := httptest.NewServer(s.handler())
		defer srv.Close()
		entry := ProbeDeepSeekQuota(context.Background(), srv.Client(), srv.URL, "k", "deepseek")
		if entry.Status != QuotaStatusError {
			t.Fatalf("status = %q, want error", entry.Status)
		}
	})
	t.Run("is_available=false 标注", func(t *testing.T) {
		s := &quotaTestServer{path: "/user/balance", status: http.StatusOK,
			body: `{"is_available":false,"balance_infos":[{"total_balance":"1.00","granted_balance":"1.00","topped_up_balance":"0","currency":"CNY"}]}`}
		srv := httptest.NewServer(s.handler())
		defer srv.Close()
		entry := ProbeDeepSeekQuota(context.Background(), srv.Client(), srv.URL, "k", "deepseek")
		if entry.Status != QuotaStatusOK || entry.Balance == nil {
			t.Fatalf("status=%q balance=%v, want ok with balance", entry.Status, entry.Balance)
		}
		if entry.Message == "" {
			t.Error("is_available=false must be annotated in Message")
		}
	})
}

// codexRolloutLine 构造一条 token_count 事件行（rate_limits 字段宽松可缺）。
func codexRolloutLine(ts string, primaryPercent, secondaryPercent float64, planType string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count",`+
		`"rate_limits":{"primary":{"used_percent":%v,"window_minutes":300,"resets_at":1758286980},`+
		`"secondary":{"used_percent":%v,"window_minutes":10080,"resets_at":1758891180},`+
		`"credits":{"has_credits":%t,"unlimited":false},"plan_type":%q}}}`,
		ts, primaryPercent, secondaryPercent, planType != "" && planType != "none", planType)
}

// writeCodexRollout 在 dir 下写一个 rollout jsonl 并设定 mtime。
func writeCodexRollout(t *testing.T, dir, name string, content string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

// Codex 正向：windows=[primary,secondary]、Level=plan_type、ProbedAt=事件
// 时间戳、credits 概要进 Message。
func TestProbeCodexSubscriptionQuota_OK(t *testing.T) {
	root := t.TempDir()
	content := `{"timestamp":"2026-09-19T10:00:00Z","type":"session_meta","payload":{"cwd":"/w"}}` + "\n" +
		codexRolloutLine("2026-09-19T12:03:00Z", 47.2, 12.5, "goplus") + "\n"
	writeCodexRollout(t, root, "rollout-2026-09-19T12-00-00-abc.jsonl", content, time.Now())

	entry := ProbeCodexSubscriptionQuota(root)
	if entry.Status != QuotaStatusOK {
		t.Fatalf("status = %q, want ok (message=%q)", entry.Status, entry.Message)
	}
	if entry.Provider != "codex" || entry.Family != QuotaFamilyCodexSub || entry.Source != QuotaSourceCodexSessionFile {
		t.Errorf("entry meta = provider:%s family:%s source:%s", entry.Provider, entry.Family, entry.Source)
	}
	if entry.ProbedAt != "2026-09-19T12:03:00Z" {
		t.Errorf("ProbedAt = %q, want event timestamp", entry.ProbedAt)
	}
	if entry.Level != "goplus" {
		t.Errorf("level = %q, want plan_type goplus", entry.Level)
	}
	if len(entry.Windows) != 2 {
		t.Fatalf("windows = %+v, want primary+secondary", entry.Windows)
	}
	if w := entry.Windows[0]; w.Kind != QuotaWindowPrimary || w.UsedPercent != 47.2 || w.WindowMin != 300 || w.ResetsAt != 1758286980 {
		t.Errorf("primary window = %+v", w)
	}
	if w := entry.Windows[1]; w.Kind != QuotaWindowSecondary || w.UsedPercent != 12.5 || w.WindowMin != 10080 {
		t.Errorf("secondary window = %+v", w)
	}
	if entry.Message == "" {
		t.Error("credits 概要应进 Message")
	}
}

// Codex 多 token_count 取最新：单文件内倒序扫描命中最后一条；跨文件按
// mtime 先扫最新文件。
func TestProbeCodexSubscriptionQuota_NewestEventWins(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	// 旧文件（mtime 较旧）：更早的额度事件。
	writeCodexRollout(t, root, "rollout-old.jsonl",
		codexRolloutLine("2026-09-19T08:00:00Z", 90, 80, "lite")+"\n", now.Add(-2*time.Hour))
	// 新文件：两条 token_count，后者（12:03）应胜出。
	writeCodexRollout(t, root, "rollout-new.jsonl",
		codexRolloutLine("2026-09-19T11:00:00Z", 10, 20, "lite")+"\n"+
			codexRolloutLine("2026-09-19T12:03:00Z", 33.3, 44.4, "max")+"\n", now)

	entry := ProbeCodexSubscriptionQuota(root)
	if entry.Status != QuotaStatusOK {
		t.Fatalf("status = %q, want ok", entry.Status)
	}
	if entry.ProbedAt != "2026-09-19T12:03:00Z" {
		t.Errorf("ProbedAt = %q, want newest event 12:03", entry.ProbedAt)
	}
	if entry.Level != "max" || entry.Windows[0].UsedPercent != 33.3 || entry.Windows[1].UsedPercent != 44.4 {
		t.Errorf("entry = level:%s windows:%+v, want newest values", entry.Level, entry.Windows)
	}
}

// Codex 尾部截断：>256KB 的文件只读尾部（头部事件不可见）；最新文件无命中
// 时回退扫描旧文件。
func TestProbeCodexSubscriptionQuota_TailTruncationAndFallback(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	// 旧文件：尾部有一条可命中事件。
	oldContent := codexRolloutLine("2026-09-19T08:00:00Z", 90, 80, "lite") + "\n"
	writeCodexRollout(t, root, "rollout-old.jsonl", oldContent, now.Add(-2*time.Hour))
	// 新文件：头部（>256KB 之外）有 token_count，尾部全部是无关事件 → 该文件
	// 不得命中，探测回退到旧文件。
	var head strings.Builder
	head.WriteString(codexRolloutLine("2026-09-18T00:00:00Z", 1, 1, "ancient") + "\n")
	for head.Len() <= 300*1024 {
		head.WriteString(`{"timestamp":"2026-09-19T09:00:00Z","type":"event_msg","payload":{"type":"agent_message","message":"noise"}}` + "\n")
	}
	writeCodexRollout(t, root, "rollout-huge.jsonl", head.String(), now)

	entry := ProbeCodexSubscriptionQuota(root)
	if entry.Status != QuotaStatusOK {
		t.Fatalf("status = %q, want ok via fallback file (message=%q)", entry.Status, entry.Message)
	}
	if entry.ProbedAt != "2026-09-19T08:00:00Z" {
		t.Errorf("ProbedAt = %q, want fallback file event", entry.ProbedAt)
	}
}

// Codex 无数据：目录为空/根目录不存在 → error（未找到本地 codex 会话数据）。
func TestProbeCodexSubscriptionQuota_NoData(t *testing.T) {
	for name, root := range map[string]string{
		"空目录":    t.TempDir(),
		"根目录不存在": filepath.Join(t.TempDir(), "missing"),
		"根目录为空串": "",
	} {
		t.Run(name, func(t *testing.T) {
			entry := ProbeCodexSubscriptionQuota(root)
			if entry.Status != QuotaStatusError {
				t.Fatalf("status = %q, want error", entry.Status)
			}
			if !strings.Contains(entry.Message, "未找到本地 codex 会话数据") {
				t.Errorf("message = %q, want 未找到本地 codex 会话数据", entry.Message)
			}
		})
	}
}

// 家族判别表驱动：baseURL × family × origin。
func TestDetectQuotaFamily(t *testing.T) {
	tests := []struct {
		baseURL string
		family  string
		origin  string
	}{
		{"https://open.bigmodel.cn/api/paas/v4", QuotaFamilyGLMBigmodel, "https://open.bigmodel.cn"},
		{"https://bigmodel.cn", QuotaFamilyGLMBigmodel, "https://bigmodel.cn"},
		{"https://open.BIGMODEL.cn/api/paas/v4/", QuotaFamilyGLMBigmodel, "https://open.bigmodel.cn"},
		{"https://api.z.ai/api/anthropic", QuotaFamilyGLMZai, "https://api.z.ai"},
		{"https://api.z.ai/v1", QuotaFamilyGLMZai, "https://api.z.ai"},
		{"https://api.deepseek.com/v1", QuotaFamilyDeepSeek, "https://api.deepseek.com"},
		{"https://api.deepseek.com", QuotaFamilyDeepSeek, "https://api.deepseek.com"},
		{"https://api.openai.com/v1", "", ""},
		{"https://sub.example.com", "", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		family, origin := DetectQuotaFamily(tt.baseURL)
		if family != tt.family || origin != tt.origin {
			t.Errorf("DetectQuotaFamily(%q) = (%q, %q), want (%q, %q)", tt.baseURL, family, origin, tt.family, tt.origin)
		}
	}
}

// models.json 往返：旧文件无 provider_quota 字段加载不报错；记录后落盘含该
// 字段；重载可见；error 不覆盖已有 ok 缓存。
func TestProviderQuotaModelsJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// 旧版 JSON：无 provider_quota（也无常驻 modality_probe），加载必须安全。
	legacy := `{
  "models": {
    "glm": {"type": "openai", "base_url": "https://open.bigmodel.cn/api/paas/v4", "auth_key": "OPENAI_API_KEY"}
  },
  "agent_teams": {"enabled": false, "teammate_mode": ""},
  "version": "1.0"
}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewConfigService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("load legacy models.json: %v", err)
	}
	if got := len(svc.GetProviderQuotaCache()); got != 0 {
		t.Fatalf("legacy file must load with empty quota cache, got %d entries", got)
	}

	okEntry := ProviderQuotaEntry{
		Provider: "glm", Family: QuotaFamilyGLMBigmodel, Status: QuotaStatusOK,
		Level: "pro", Source: QuotaSourceGLMAPI, ProbedAt: "2026-09-19T12:03:00+08:00",
		Windows: []QuotaWindow{{Kind: QuotaWindowPrimary, UsedPercent: 62, Remaining: 114}},
	}
	if err := svc.RecordProviderQuota(okEntry); err != nil {
		t.Fatalf("RecordProviderQuota: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"provider_quota"`) {
		t.Error("saved models.json must contain provider_quota field")
	}

	// 新实例重载可见（深拷贝快照值等价）。
	svc2 := NewConfigService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	cache := svc2.GetProviderQuotaCache()
	got, ok := cache["glm"]
	if !ok || got.Status != QuotaStatusOK || got.Level != "pro" || len(got.Windows) != 1 {
		t.Fatalf("reloaded entry = %+v ok=%v, want persisted ok entry", got, ok)
	}
	// 深拷贝隔离：改快照不影响内部缓存。
	got.Windows[0].UsedPercent = 999
	if again := svc2.GetProviderQuotaCache()["glm"]; again.Windows[0].UsedPercent != 62 {
		t.Error("GetProviderQuotaCache must deep-copy windows slice")
	}

	// 未决语义：error 不覆盖已有 ok。
	errEntry := ProviderQuotaEntry{Provider: "glm", Family: QuotaFamilyGLMBigmodel,
		Status: QuotaStatusError, Message: "请求失败", Source: QuotaSourceGLMAPI, ProbedAt: "2026-09-19T13:00:00+08:00"}
	if err := svc2.RecordProviderQuota(errEntry); err != nil {
		t.Fatalf("RecordProviderQuota error entry: %v", err)
	}
	if kept := svc2.GetProviderQuotaCache()["glm"]; kept.Status != QuotaStatusOK {
		t.Errorf("error must not overwrite cached ok, got status=%q", kept.Status)
	}
	// 首探直接落 error 允许（无既有 ok 时）。
	first := ProviderQuotaEntry{Provider: "deepseek", Family: QuotaFamilyDeepSeek,
		Status: QuotaStatusError, Message: "请求失败", Source: QuotaSourceDeepSeekAPI, ProbedAt: "2026-09-19T13:00:00+08:00"}
	if err := svc2.RecordProviderQuota(first); err != nil {
		t.Fatalf("RecordProviderQuota first error: %v", err)
	}
	if e := svc2.GetProviderQuotaCache()["deepseek"]; e.Status != QuotaStatusError {
		t.Errorf("first probe error must be cached, got %q", e.Status)
	}
	// 空 provider 名拒绝。
	if err := svc2.RecordProviderQuota(ProviderQuotaEntry{Provider: "  "}); err == nil {
		t.Error("empty provider name must be rejected")
	}
}

// Entry JSON 序列化冒烟：字段名是 models.json 缓存文件 API。
func TestProviderQuotaEntryJSON(t *testing.T) {
	entry := ProviderQuotaEntry{
		Provider: "glm", Family: QuotaFamilyGLMBigmodel, Status: QuotaStatusOK, Level: "pro",
		Windows: []QuotaWindow{{Kind: QuotaWindowPrimary, UsedPercent: 62, Remaining: 114, WindowMin: 300, ResetsAt: 1758286980}},
		Balance: nil,
		Source:  QuotaSourceGLMAPI, ProbedAt: "2026-09-19T12:03:00Z",
	}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, key := range []string{`"provider":"glm"`, `"family":"glm-bigmodel"`, `"status":"ok"`, `"level":"pro"`, `"used_percent":62`, `"window_minutes":300`, `"resets_at":1758286980`, `"source":"glm-api"`, `"probed_at"`} {
		if !strings.Contains(s, key) {
			t.Errorf("entry json %s missing %s", s, key)
		}
	}
	// Balance 指针形态。
	bEntry := ProviderQuotaEntry{Provider: "deepseek", Family: QuotaFamilyDeepSeek, Status: QuotaStatusOK,
		Balance: &QuotaBalance{Currency: "CNY", Total: 128.5, Granted: 10, ToppedUp: 118.5},
		Source:  QuotaSourceDeepSeekAPI, ProbedAt: "2026-09-19T12:03:00Z"}
	b2, _ := json.Marshal(bEntry)
	for _, key := range []string{`"currency":"CNY"`, `"total":128.5`, `"granted":10`, `"topped_up":118.5`} {
		if !strings.Contains(string(b2), key) {
			t.Errorf("balance json %s missing %s", string(b2), key)
		}
	}
}

// Major-1 回归：GLM 家族标签按 origin 推导，不再硬编码 bigmodel——
// 否则 glm-zai provider 落盘条目标签错误，前端 z.ai 卡永不渲染。
func TestGlmFamilyForOrigin(t *testing.T) {
	cases := []struct {
		origin string
		want   string
	}{
		{"https://api.z.ai", QuotaFamilyGLMZai},
		{"https://api.z.ai/", QuotaFamilyGLMZai},
		{"https://bigmodel.cn", QuotaFamilyGLMBigmodel},
		{"https://open.bigmodel.cn", QuotaFamilyGLMBigmodel},
		// 非双域 origin（如 httptest 本地地址）：回退 bigmodel，
		// 与 DetectQuotaFamily 家族判别入口一致。
		{"http://127.0.0.1:58081", QuotaFamilyGLMBigmodel},
		{"", QuotaFamilyGLMBigmodel},
	}
	for _, c := range cases {
		if got := glmFamilyForOrigin(c.origin); got != c.want {
			t.Errorf("glmFamilyForOrigin(%q) = %q, want %q", c.origin, got, c.want)
		}
	}
}

// v1.3.77 线上回归：GLM 真实端点 limits[].unit 为数字形态（zcode 侧
// typeof unit=="number" 消费），旧 string 声明导致整包反序列化失败
// （json: cannot unmarshal number into ... limits.unit of type string）。
func TestProbeGLMQuota_UnitNumberForm(t *testing.T) {
	body := `{"code":200,"msg":"success","data":{"level":"pro",` +
		`"limits":[{"type":"TIME_LIMIT","unit":1,"number":300,"usage":180,"percentage":62.0,"remaining":114},` +
		`{"type":"WEEK","unit":null,"number":"120","usage":30,"percentage":31.5,"remaining":80},` +
		`{"type":"MIXED"}]}}`
	_, srv := newGLMTestServer(t, http.StatusOK, body)
	entry := ProbeGLMQuota(context.Background(), srv.Client(), srv.URL, "k", "glm")
	if entry.Status != QuotaStatusOK {
		t.Fatalf("status = %q, want ok (message=%q)", entry.Status, entry.Message)
	}
	if len(entry.Windows) != 2 {
		t.Fatalf("windows = %+v, want 2（数字/null 形态 unit 不阻断解析）", entry.Windows)
	}
	if w := entry.Windows[0]; w.Kind != QuotaWindowPrimary || w.UsedPercent != 62.0 {
		t.Errorf("primary = %+v, want percentage=62", w)
	}
}
