package main

// app_quota_probe_test.go — App 绑定层（app_quota_probe.go）单测：缓存快照、
// unsupported/no-provider 分支、codex 会话根注入、自动冷却判定、单飞复用。
// 网络探测本体（GLM/DeepSeek/家族判别）在 internal/config/quota_probe_test.go
// 用 httptest 覆盖；此处只验 App 组装语义，不外发真实请求。

import (
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/config"
)

// GetProviderQuotas 空缓存返回非 nil 空 map（绑定层契约：前端免 null 处理）。
func TestGetProviderQuotas_EmptyCache(t *testing.T) {
	app := newTestApp(t)
	got := app.GetProviderQuotas()
	if got == nil {
		t.Fatal("GetProviderQuotas must return non-nil map for empty cache")
	}
	if len(got) != 0 {
		t.Fatalf("empty cache must yield empty map, got %v", got)
	}
}

// ProbeProviderQuota：空名直接错误态；不存在的 provider 错误态且不落缓存。
func TestProbeProviderQuota_MissingProvider(t *testing.T) {
	app := newTestApp(t)
	if e := app.ProbeProviderQuota("  "); e.Status != config.QuotaStatusError {
		t.Fatalf("blank name status = %q, want error", e.Status)
	}
	if e := app.ProbeProviderQuota("ghost"); e.Status != config.QuotaStatusError {
		t.Fatalf("ghost status = %q, want error", e.Status)
	}
	if _, cached := app.GetProviderQuotas()["ghost"]; cached {
		t.Error("nonexistent provider must not pollute the quota cache")
	}
}

// 家族不匹配 → unsupported 落缓存（避免重复探测），并随 models.json 持久化。
func TestProbeProviderQuota_UnsupportedFamily(t *testing.T) {
	app, configDir := newTestAppWithConfigDir(t)
	if err := app.Config.SaveProvider("acme", config.Provider{
		OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://quota-test.internal/v1"},
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	entry := app.ProbeProviderQuota("acme")
	if entry.Status != config.QuotaStatusUnsupported {
		t.Fatalf("status = %q, want unsupported", entry.Status)
	}
	cached := app.GetProviderQuotas()["acme"]
	if cached.Status != config.QuotaStatusUnsupported || cached.Provider != "acme" {
		t.Fatalf("unsupported entry must be cached, got %+v", cached)
	}
	// 持久化：新 service 重载可见。
	svc2 := config.NewConfigService(configDir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if e := svc2.GetProviderQuotaCache()["acme"]; e.Status != config.QuotaStatusUnsupported {
		t.Errorf("unsupported entry must persist, got %+v", e)
	}
}

// codex 路径：注入空会话根 → error 态落缓存；ProbeAll 含 codex 条目。
func TestProbeProviderQuota_CodexInjectedRoot(t *testing.T) {
	app := newTestApp(t)
	app.quotaCodexSessionsRoot = t.TempDir() // 空目录 → 未找到会话数据

	entry := app.ProbeProviderQuota("codex")
	if entry.Status != config.QuotaStatusError {
		t.Fatalf("status = %q, want error", entry.Status)
	}
	if entry.Family != config.QuotaFamilyCodexSub || entry.Source != config.QuotaSourceCodexSessionFile {
		t.Errorf("entry meta = %+v", entry)
	}
	if e := app.GetProviderQuotas()["codex"]; e.Status != config.QuotaStatusError {
		t.Error("codex error entry must be cached")
	}

	all := app.ProbeAllProviderQuotas()
	if _, ok := all["codex"]; !ok {
		t.Error("ProbeAllProviderQuotas must include the codex entry")
	}
}

// 自动路径冷却判定：ok 且新近 → fresh；error/过期/坏时间戳 → 不 fresh。
func TestQuotaEntryFresh(t *testing.T) {
	now := time.Now().Format(time.RFC3339)
	old := time.Now().Add(-quotaProbeCooldown - time.Minute).Format(time.RFC3339)
	if !quotaEntryFresh(config.ProviderQuotaEntry{Status: config.QuotaStatusOK, ProbedAt: now}) {
		t.Error("recent ok entry must be fresh")
	}
	if quotaEntryFresh(config.ProviderQuotaEntry{Status: config.QuotaStatusOK, ProbedAt: old}) {
		t.Error("expired ok entry must not be fresh")
	}
	if quotaEntryFresh(config.ProviderQuotaEntry{Status: config.QuotaStatusError, ProbedAt: now}) {
		t.Error("error entry must not be fresh")
	}
	if quotaEntryFresh(config.ProviderQuotaEntry{Status: config.QuotaStatusOK, ProbedAt: "not-a-time"}) {
		t.Error("malformed ProbedAt must not be fresh")
	}
}

// 单飞语义：同 provider 重复登记复用同一槽位；结算后等待者拿到同一结果，
// 槽位释放可再次登记（复刻 modality probe 单飞语义的「等待复用」变体）。
func TestQuotaProbeSingleFlight(t *testing.T) {
	app := newTestApp(t)
	f1, first1 := app.beginQuotaFlight("x")
	if !first1 {
		t.Fatal("first beginQuotaFlight must be the executor")
	}
	f2, first2 := app.beginQuotaFlight("x")
	if first2 || f2 != f1 {
		t.Fatal("in-flight duplicate must reuse the same flight slot")
	}
	want := config.ProviderQuotaEntry{Provider: "x", Status: config.QuotaStatusOK}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-f2.done
		if f2.entry.Status != config.QuotaStatusOK {
			t.Error("waiter must receive the finished entry")
		}
	}()
	app.finishQuotaFlight("x", f1, want)
	wg.Wait()
	if f1.entry.Provider != "x" {
		t.Error("executor entry must be recorded")
	}
	// 槽位已释放：可再次登记成为执行者。
	f3, first3 := app.beginQuotaFlight("x")
	if !first3 || f3 == f1 {
		t.Fatal("finished flight slot must be reusable")
	}
	app.finishQuotaFlight("x", f3, want)
}
