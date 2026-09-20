package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/config"
)

// 额度探测的 app 组装层（《额度查询与常显设计方案》§3/§4 P1 后端）。
//
// 三个入口共享同一执行路径 executeQuotaProbe：
//   - 手动：前端额度页/单卡刷新按钮调 ProbeProviderQuota /
//     ProbeAllProviderQuotas（Wails 绑定），不受 10min 成功冷却节流；
//   - 自动：Startup 末尾 wireQuotaProber 启动 30s 延迟一轮（克制刷新：
//     距上次成功 <10min 的 provider 跳过），无后台轮询。
//
// 探测结论统一走 ConfigService.RecordProviderQuota 落 models.json 的
// provider_quota 缓存（error 不覆盖已有 ok）。key 解析复用 getProviderAPIKey，
// 仅探测瞬间内存使用；日志只记 provider/状态，绝不打印请求头。

// 额度探测节奏常量（设计 §3「刷新策略（克制）」）。
const (
	// quotaProbeTimeout 单家族探测超时（每 provider 独立时窗）。
	quotaProbeTimeout = 10 * time.Second
	// quotaAutoProbeDelay 启动后延迟自动探测的等待时间（避开启动高峰）。
	quotaAutoProbeDelay = 30 * time.Second
	// quotaProbeCooldown 自动路径的成功冷却窗口。
	quotaProbeCooldown = 10 * time.Minute
	// codexQuotaProviderName Codex 订阅条目的固定 provider 名。
	codexQuotaProviderName = "codex"
)

// quotaProbeFlight 单飞槽位：首个调用者执行探测，后续重复调用阻塞等待
// done 后直接复用 entry（与 modality probe 的「在飞即丢弃」不同：额度探测
// 的所有调用方都依赖返回值，等待复用比返回旧缓存更符合手动刷新语义）。
type quotaProbeFlight struct {
	done  chan struct{}
	entry config.ProviderQuotaEntry
}

// beginQuotaFlight 登记单飞槽位；first=true 表示调用者是执行者。
func (a *App) beginQuotaFlight(name string) (flight *quotaProbeFlight, first bool) {
	a.quotaProbeMu.Lock()
	defer a.quotaProbeMu.Unlock()
	if a.quotaProbeFlights == nil {
		a.quotaProbeFlights = make(map[string]*quotaProbeFlight)
	}
	if existing, ok := a.quotaProbeFlights[name]; ok {
		return existing, false
	}
	f := &quotaProbeFlight{done: make(chan struct{})}
	a.quotaProbeFlights[name] = f
	return f, true
}

// finishQuotaFlight 结算单飞：写回结果并唤醒全部等待者。
func (a *App) finishQuotaFlight(name string, f *quotaProbeFlight, entry config.ProviderQuotaEntry) {
	f.entry = entry
	a.quotaProbeMu.Lock()
	delete(a.quotaProbeFlights, name)
	a.quotaProbeMu.Unlock()
	close(f.done)
}

// wireQuotaProber 启动自动探测调度（Startup 末尾调用一次，须在 Config.Load
// 之后）。绑定生成进程（-tags bindings）不经过 Startup，不受影响。
func (a *App) wireQuotaProber() {
	go func() {
		time.Sleep(quotaAutoProbeDelay)
		a.probeAllQuotas(true)
	}()
}

// GetProviderQuotas 全量额度缓存快照（Wails 绑定：页面/按钮渲染数据源）。
// 返回 ConfigService 读锁保护下的深拷贝副本。
func (a *App) GetProviderQuotas() map[string]config.ProviderQuotaEntry {
	if a.Config == nil {
		return map[string]config.ProviderQuotaEntry{}
	}
	return a.Config.GetProviderQuotaCache()
}

// ProbeProviderQuota 手动路径（Wails 绑定）：单个 provider 立即探测并落盘。
// name=="codex" 走本地会话文件路径（订阅额度与 API key 无关）。探测中的
// 重复调用等待首个在飞探测的结果复用（单飞）。
func (a *App) ProbeProviderQuota(name string) config.ProviderQuotaEntry {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.ProviderQuotaEntry{Status: config.QuotaStatusError, Message: "未指定 provider"}
	}
	flight, first := a.beginQuotaFlight(name)
	if !first {
		<-flight.done
		return flight.entry
	}
	var entry config.ProviderQuotaEntry
	var cacheable bool
	if name == codexQuotaProviderName {
		entry = a.probeCodexSubscription()
		cacheable = true
	} else {
		entry, cacheable = a.executeQuotaProbe(name)
	}
	// provider 不存在的手滑名不落缓存（防污染缓存表）；其余结论（含
	// unsupported、no_key、首探 error）按契约落盘。
	if cacheable {
		a.commitQuotaProbe(entry)
	}
	a.finishQuotaFlight(name, flight, entry)
	return entry
}

// executeQuotaProbe 执行单个 provider 的额度探测：解析 baseURL 家族 → 三选
// 一分发。返回的 Entry 已含全部元数据；cacheable=false 表示结论不应落缓存
// （provider 不存在）；本函数不落盘（由调用方收尾）。
func (a *App) executeQuotaProbe(name string) (config.ProviderQuotaEntry, bool) {
	provider, err := a.Config.GetProvider(name)
	if err != nil || provider == nil {
		// provider 不存在：不落缓存，直接返回错误态。
		return config.ProviderQuotaEntry{Provider: name, Status: config.QuotaStatusError,
			Message: "provider 不存在", ProbedAt: time.Now().Format(time.RFC3339)}, false
	}
	// 家族判别：anthropic/openai 两种格式都考虑，取首个可用匹配者。
	family, origin := config.DetectQuotaFamily(provider.EffectiveBaseURL("openai"))
	if family == "" {
		family, origin = config.DetectQuotaFamily(provider.EffectiveBaseURL("anthropic"))
	}
	if family == "" {
		return config.ProviderQuotaEntry{
			Provider: name, Family: "", Status: config.QuotaStatusUnsupported,
			Message: "该服务的 baseURL 不属于支持额度查询的家族",
			Source:  "", ProbedAt: time.Now().Format(time.RFC3339),
		}, true
	}
	apiKey, _ := a.getProviderAPIKey(name, *provider)
	if apiKey == "" {
		return config.ProviderQuotaEntry{
			Provider: name, Family: family, Status: config.QuotaStatusNoKey,
			Message: "未配置 API Key", Source: quotaSourceForFamily(family),
			ProbedAt: time.Now().Format(time.RFC3339),
		}, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), quotaProbeTimeout)
	defer cancel()
	// client 不设内置超时：整体时限由 ctx 控制（10s），避免双超时语义打架。
	client := &http.Client{}
	switch family {
	case config.QuotaFamilyGLMBigmodel, config.QuotaFamilyGLMZai:
		return config.ProbeGLMQuota(ctx, client, origin, apiKey, name), true
	case config.QuotaFamilyDeepSeek:
		return config.ProbeDeepSeekQuota(ctx, client, origin, apiKey, name), true
	default:
		return config.ProviderQuotaEntry{
			Provider: name, Family: family, Status: config.QuotaStatusUnsupported,
			Message:  "该服务的 baseURL 不属于支持额度查询的家族",
			ProbedAt: time.Now().Format(time.RFC3339),
		}, true
	}
}

// quotaSourceForFamily 家族 → 数据源常量（no_key 等未发请求也归入对应源）。
func quotaSourceForFamily(family string) string {
	switch family {
	case config.QuotaFamilyGLMBigmodel, config.QuotaFamilyGLMZai:
		return config.QuotaSourceGLMAPI
	case config.QuotaFamilyDeepSeek:
		return config.QuotaSourceDeepSeekAPI
	default:
		return ""
	}
}

// probeCodexSubscription Codex 订阅路径：读用户家目录下的 ~/.codex/sessions
// （quotaCodexSessionsRoot 非空时优先，单测注入用）。
func (a *App) probeCodexSubscription() config.ProviderQuotaEntry {
	root := a.quotaCodexSessionsRoot
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return config.ProviderQuotaEntry{
				Provider: codexQuotaProviderName, Family: config.QuotaFamilyCodexSub,
				Status: config.QuotaStatusError, Message: "无法定位用户主目录: " + err.Error(),
				Source: config.QuotaSourceCodexSessionFile, ProbedAt: time.Now().Format(time.RFC3339),
			}
		}
		root = filepath.Join(home, ".codex", "sessions")
	}
	return config.ProbeCodexSubscriptionQuota(root)
}

// commitQuotaProbe 探测收尾：落 provider_quota 缓存（error 不覆盖已有 ok，
// 由 RecordProviderQuota 兜底）。日志只记 provider/家族/状态，不含请求头。
func (a *App) commitQuotaProbe(entry config.ProviderQuotaEntry) {
	if err := a.Config.RecordProviderQuota(entry); err != nil {
		if a.Log != nil {
			a.Log.Warn("quota-probe", "额度探测结论落盘失败", err.Error())
		}
		return
	}
	if a.Log != nil {
		a.Log.Info("quota-probe", "额度探测完成",
			"provider="+entry.Provider+" family="+entry.Family+" status="+entry.Status)
	}
}

// quotaEntryFresh 自动路径冷却判定：ok 且 ProbedAt（RFC3339）距今 <10min。
func quotaEntryFresh(entry config.ProviderQuotaEntry) bool {
	if entry.Status != config.QuotaStatusOK || entry.ProbedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, entry.ProbedAt)
	if err != nil {
		return false
	}
	return time.Since(t) < quotaProbeCooldown
}

// probeAllQuotas 遍历全部 providers 并发探测 + codex 订阅条目。
// skipFresh=true（自动路径）时，冷却期内的 ok 条目直接沿用缓存；false
// （手动「全部刷新」）时全部重探。每 provider 独享 10s 超时与单飞槽位。
func (a *App) probeAllQuotas(skipFresh bool) map[string]config.ProviderQuotaEntry {
	cache := a.GetProviderQuotas()
	results := make(map[string]config.ProviderQuotaEntry, len(cache)+1)
	var mu sync.Mutex
	var wg sync.WaitGroup
	probe := func(name string, cached config.ProviderQuotaEntry) {
		if skipFresh && quotaEntryFresh(cached) {
			mu.Lock()
			results[name] = cached
			mu.Unlock()
			return
		}
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			entry := a.ProbeProviderQuota(n)
			mu.Lock()
			results[n] = entry
			mu.Unlock()
		}(name)
	}
	if names := a.Config.GetProviderNames(); len(names) > 0 {
		for _, name := range names {
			probe(name, cache[name])
		}
	}
	probe(codexQuotaProviderName, cache[codexQuotaProviderName])
	wg.Wait()
	return results
}

// ProbeAllProviderQuotas 手动全量刷新（Wails 绑定：额度页「全部刷新」按钮）。
// 不受 10min 成功冷却节流；返回探测后的全量快照。
func (a *App) ProbeAllProviderQuotas() map[string]config.ProviderQuotaEntry {
	return a.probeAllQuotas(false)
}
