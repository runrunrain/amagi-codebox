package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 额度实弹探测（契约《额度查询与常显设计方案》§2/§4，端点形态已逆向实证）。
//
// 三家族数据源：
//   - GLM Coding Plan（bigmodel.cn / api.z.ai）：
//     GET {origin}/api/monitor/usage/quota/limit，头 authorization: <key>（无
//     Bearer 前缀，zcode 逆向结论）。窗口单位为 prompts，非美元。
//   - DeepSeek：GET {origin}/user/balance，Bearer <key>，余额制（无进度分母）。
//   - Codex 订阅：纯本地读 ~/.codex/sessions 下最新 rollout-*.jsonl 的
//     token_count 事件 rate_limits（主动外发被 Cloudflare 拦截，不采用）。
//
// 与 modalities_probe.go 同一形态纪律：本包不依赖 secrets——API key 由调用方
// （app 组装层）解析后传入；client/根目录注入，便于 httptest/t.TempDir 覆盖。
// 错误分类沿用「未决不落缓存」语义的额度版：网络/超时/解析失败 → error，不
// 覆盖已有 ok 缓存（RecordProviderQuota 兜底），下次触发自然重试。
//
// 安全边界（§8）：仅 GET；仅发往 provider 自身 baseURL 同源端点；codex 路径
// 不触碰 ~/.codex/auth.json、不读 archived_sessions；Entry 值域天然无凭据。

// 额度数据源常量（ProviderQuotaEntry.Source 值域）。
const (
	QuotaSourceGLMAPI           = "glm-api"
	QuotaSourceDeepSeekAPI      = "deepseek-api"
	QuotaSourceCodexSessionFile = "codex-session-file"
	QuotaSourceOpenCodeZenAPI   = "opencode-zen-api"
	QuotaSourceOpenRouterAPI    = "openrouter-api"
)

// 窗口 Kind 常量（QuotaWindow.Kind 值域）。
const (
	QuotaWindowPrimary   = "primary"   // 主显示窗口（GLM TIME_LIMIT / Codex 5h / Zen 滚动）
	QuotaWindowSecondary = "secondary" // 次窗口（GLM 其他限额 / Codex 周 / Zen 周）
	QuotaWindowTertiary  = "tertiary"  // 第三窗口（Zen 月）
)

const (
	// quotaProbeMaxBodyBytes 限制响应读取体积（错误体仅需头部片段做分类）。
	quotaProbeMaxBodyBytes = 64 * 1024
	// codexQuotaTailBytes 单个 rollout 文件只读尾部（token_count 事件随对话
	// 追加，最新额度必在尾部；头部元数据对额度无用）。
	codexQuotaTailBytes = 256 * 1024
	// codexQuotaMaxFiles 按 mtime 参与扫描的 rollout 文件数上限。
	codexQuotaMaxFiles = 20
	// codexQuotaProviderName Codex 订阅条目的固定 provider 名（独立于本地
	// provider 表：订阅额度挂在 codex CLI 会话而非某个 API key 上）。
	codexQuotaProviderName = "codex"
	// quotaMsgBriefMax Message 概要截断长度（错误体可能很长）。
	quotaMsgBriefMax = 200
)

// glmNoPlanKeywords 判定「key 有效但未开通 Coding Plan 套餐」的 msg 关键词。
var glmNoPlanKeywords = []string{"不存在coding plan", "没有资格"}

// flexNumber 宽松数值：JSON 数字或字符串数字均可（GLM limits 条目的数值字段
// 可能以字符串下发）；缺省/null/不可解析按 unset 处理（契约：缺省跳过）。
type flexNumber struct {
	set bool
	v   float64
}

// UnmarshalJSON 宽松解析：数字、字符串数字、null；解析失败不报错（按缺省）。
func (n *flexNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // 宽松契约：不可解析视同缺省
	}
	n.set, n.v = true, v
	return nil
}

// glmQuotaEnvelope GLM 额度接口响应信封（契约 §2）。
type glmQuotaEnvelope struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Success *bool  `json:"success"` // 指针：缺省视为非 false
	Data    struct {
		Limits []glmLimitEntry `json:"limits"`
		Level  string          `json:"level"`
	} `json:"data"`
}

// glmLimitEntry GLM data.limits 单条限额（数值字段宽松解析；unit 为 flexString，
// 真实端点返回数字形态——v1.3.77 线上实证 string 声明整体反序列化失败，
// zcode 侧本就按 typeof unit=="number" 消费，故容忍 string/number/null）。
// v1.3.81 实证补齐（max 套餐真实响应）：
//   - TIME_LIMIT  + unit=5(月) + usageDetails[...] → MCP 月额度（非 5h！）
//   - TOKENS_LIMIT + unit=3(小时) + number=5 → 5 小时 prompt 窗口（主额度）
//   - nextResetTime 毫秒时间戳；usageDetails 携带 MCP 工具级明细
type glmLimitEntry struct {
	Type          string           `json:"type"`
	Unit          flexString       `json:"unit"`
	Number        flexNumber       `json:"number"`
	Usage         flexNumber       `json:"usage"`
	CurrentValue  flexNumber       `json:"currentValue"`
	Remaining     flexNumber       `json:"remaining"`
	Percentage    flexNumber       `json:"percentage"`
	NextResetTime flexNumber       `json:"nextResetTime"` // 毫秒 unix
	UsageDetails  []glmUsageDetail `json:"usageDetails"`
}

type glmUsageDetail struct {
	ModelCode string     `json:"modelCode"`
	Usage     flexNumber `json:"usage"`
}

// flexString 宽松字符串：接受 string/number/bool/null 任一 JSON 形态，
// 去引号后取原始字面量为字符串（number 不丢精度）；null/空为空串。
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = ""
		return nil
	}
	*f = flexString(strings.Trim(s, `"`))
	return nil
}

// deepSeekBalanceEnvelope DeepSeek 余额接口响应（数值均为字符串数字）。
type deepSeekBalanceEnvelope struct {
	IsAvailable  bool `json:"is_available"`
	BalanceInfos []struct {
		TotalBalance    string `json:"total_balance"`
		GrantedBalance  string `json:"granted_balance"`
		ToppedUpBalance string `json:"topped_up_balance"`
		Currency        string `json:"currency"`
	} `json:"balance_infos"`
}

// DetectQuotaFamily 按 baseURL 域特征判别额度家族与探测 origin（契约 §2）：
//   - 含 bigmodel.cn → glm-bigmodel，origin 取该 URL 的 scheme://host 同域；
//   - 含 api.z.ai → glm-zai，origin 固定 https://api.z.ai（无论配置路径）；
//   - 含 api.deepseek.com → deepseek，origin 同域；
//   - host==opencode.ai 且路径含 /zen → opencode-zen，origin 取归一后的 /v1
//     段（…/zen/go → …/zen/go/v1，plan 路径随配置走，对未来非 go 档自适应）；
//   - host==openrouter.ai → openrouter，origin 取归一后的 /api/v1 段。
//
// 无匹配返回 ("", "")——调用方落 Status=unsupported 缓存避免重复探测。
// 家族判别只认 provider 自身配置的域，绝不外发第三方。新家族按 u.Host
// 锚定判别（diting F-1：防 openrouter.aimirror.com 等内嵌官方域子串的
// 中转/仿冒域误判；GLM/DeepSeek 既有子串匹配为历史行为，勿混改）。
func DetectQuotaFamily(baseURL string) (family, origin string) {
	trimmed := strings.TrimSpace(baseURL)
	lower := strings.ToLower(trimmed)
	var matched string
	switch {
	case strings.Contains(lower, "bigmodel.cn"):
		matched = QuotaFamilyGLMBigmodel
	case strings.Contains(lower, "api.z.ai"):
		matched = QuotaFamilyGLMZai
	case strings.Contains(lower, "api.deepseek.com"):
		matched = QuotaFamilyDeepSeek
	default:
		// 新家族 host 锚定判别（diting F-1）：仅认官方域本体，防内嵌子串误判。
		u, err := url.Parse(trimmed)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", ""
		}
		host := strings.ToLower(u.Host)
		switch {
		case host == "opencode.ai" && strings.Contains(strings.ToLower(u.Path), "/zen"):
			matched = QuotaFamilyOpenCodeZen
		case host == "openrouter.ai":
			matched = QuotaFamilyOpenRouter
		default:
			return "", ""
		}
	}
	if matched == QuotaFamilyGLMZai {
		// z.ai 逆向结论：额度端点固定挂 api.z.ai 根域，不随 baseURL 路径走。
		return QuotaFamilyGLMZai, "https://api.z.ai"
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ""
	}
	// host 统一小写（域名大小写不敏感，归一后 origin 稳定可比较）。
	origin = u.Scheme + "://" + strings.ToLower(u.Host)
	switch matched {
	case QuotaFamilyOpenCodeZen:
		// zen 额度端点挂 baseURL 的 /v1 段（plan 路径随配置走）。
		return matched, origin + zenV1Path(u.Path)
	case QuotaFamilyOpenRouter:
		// openrouter 额度端点固定挂 /api/v1 段（key/credits）。
		return matched, origin + openRouterAPIPath(u.Path)
	}
	return matched, origin
}

// quotaBasePath 归一 baseURL 路径：剥尾 /chat/completions 与 /responses、去尾 /
// （勿复用 NormalizeOpenAIBaseURL：其不补 /v1、/api/v1 尾缀，语义不同）。
func quotaBasePath(path string) string {
	p := strings.TrimRight(strings.TrimSpace(path), "/")
	for _, suffix := range []string{"/chat/completions", "/responses"} {
		if strings.HasSuffix(p, suffix) {
			p = strings.TrimRight(strings.TrimSuffix(p, suffix), "/")
		}
	}
	return p
}

// zenV1Path zen 探测基路径：保证以 /v1 结尾（…/zen/go → …/zen/go/v1）。
func zenV1Path(path string) string {
	p := quotaBasePath(path)
	if !strings.HasSuffix(p, "/v1") {
		p += "/v1"
	}
	return p
}

// openRouterAPIPath openrouter 探测基路径：保证以 /api/v1 结尾。
func openRouterAPIPath(path string) string {
	p := quotaBasePath(path)
	if !strings.HasSuffix(p, "/api/v1") {
		p += "/api/v1"
	}
	return p
}

// quotaErrorEntry 构造未决 error 条目（首探落盘允许，Message 说明原因）。
func quotaErrorEntry(provider, family, source, message string) ProviderQuotaEntry {
	return ProviderQuotaEntry{
		Provider: provider,
		Family:   family,
		Status:   QuotaStatusError,
		Message:  briefQuotaMsg(message),
		Source:   source,
		ProbedAt: time.Now().Format(time.RFC3339),
	}
}

// briefQuotaMsg 错误概要截断（Message 面向 UI，不透传完整错误体）。
func briefQuotaMsg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > quotaMsgBriefMax {
		return s[:quotaMsgBriefMax] + "…"
	}
	return s
}

// ProbeGLMQuota 探测 GLM Coding Plan 额度：GET {origin}/api/monitor/usage/
// quota/limit，头 authorization: <key>（无 Bearer 前缀，zcode 逆向结论）。
// 状态分类（契约 §4）：code==200 且 success!=false → ok（TIME_LIMIT 主窗口
// 优先）；code!=200 或 msg 含无套餐关键词 → no_plan；HTTP 401/403 或
// code==1001 → no_key；网络/超时/解析失败 → error。
func ProbeGLMQuota(ctx context.Context, client *http.Client, origin, apiKey, providerName string) ProviderQuotaEntry {
	// diting Major-1 修复：family 按 origin 推导（z.ai 家族 origin 固定 api.z.ai，
	// bigmodel 为同域 origin），不再硬编码 QuotaFamilyGLMBigmodel——否则 glm-zai
	// provider 落盘条目标签错误，前端 z.ai 卡永不渲染。
	family := glmFamilyForOrigin(origin)
	now := time.Now().Format(time.RFC3339)
	if client == nil || strings.TrimSpace(origin) == "" {
		return quotaErrorEntry(providerName, family, QuotaSourceGLMAPI, "探测客户端或端点不可用")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+"/api/monitor/usage/quota/limit", nil)
	if err != nil {
		return quotaErrorEntry(providerName, family, QuotaSourceGLMAPI, "构造请求失败: "+err.Error())
	}
	req.Header.Set("Authorization", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return quotaErrorEntry(providerName, family, QuotaSourceGLMAPI, "请求失败: "+err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, quotaProbeMaxBodyBytes))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusNoKey,
			Message: briefQuotaMsg(fmt.Sprintf("鉴权失败（HTTP %d）", resp.StatusCode)), Source: QuotaSourceGLMAPI, ProbedAt: now}
	}
	var env glmQuotaEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return quotaErrorEntry(providerName, family, QuotaSourceGLMAPI, "响应解析失败: "+err.Error())
	}
	switch {
	case env.Code == 1001:
		return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusNoKey,
			Message: briefQuotaMsg(env.Msg), Source: QuotaSourceGLMAPI, ProbedAt: now}
	case env.Code != 200:
		return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusNoPlan,
			Message: briefQuotaMsg(env.Msg), Source: QuotaSourceGLMAPI, ProbedAt: now}
	}
	for _, kw := range glmNoPlanKeywords {
		if strings.Contains(env.Msg, kw) {
			return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusNoPlan,
				Message: briefQuotaMsg(env.Msg), Source: QuotaSourceGLMAPI, ProbedAt: now}
		}
	}
	if env.Success != nil && !*env.Success {
		return quotaErrorEntry(providerName, family, QuotaSourceGLMAPI, briefQuotaMsg("success=false: "+env.Msg))
	}
	entry := ProviderQuotaEntry{
		Provider: providerName,
		Family:   family,
		Status:   QuotaStatusOK,
		Level:    strings.TrimSpace(env.Data.Level),
		Windows:  glmQuotaWindows(env.Data.Limits),
		Source:   QuotaSourceGLMAPI,
		ProbedAt: now,
	}
	if len(entry.Windows) == 0 && entry.Level == "" {
		// 信封 ok 但无任何可用限额数据：按未决处理，防缓存「空 ok」误导。
		return quotaErrorEntry(providerName, family, QuotaSourceGLMAPI, "响应缺少可用的 limits 数据")
	}
	return entry
}

// glmFamilyForOrigin 从探测 origin 反推 GLM 家族（Major-1 修复配套纯函数，
// 便于离线测试）：api.z.ai → glm-zai；含 bigmodel.cn → glm-bigmodel；
// 其余（如本地 httptest 地址）回退 glm-bigmodel（与 DetectQuotaFamily 的
// 家族判别一致——能走进 ProbeGLMQuota 的 origin 本就属 GLM 双域之一）。
func glmFamilyForOrigin(origin string) string {
	if f, _ := DetectQuotaFamily(origin); f != "" && (f == QuotaFamilyGLMZai || f == QuotaFamilyGLMBigmodel) {
		return f
	}
	return QuotaFamilyGLMBigmodel
}

// glmQuotaWindows 筛选 GLM limits 条目并赋予窗口语义（v1.3.81 重写）。
//
// max 套餐实证响应（错误旧版假设的对照）：
//
//	limits[0] = {type: TIME_LIMIT,  unit: 5(月),  usageDetails: [search-prime…]} → MCP 月额度
//	limits[1] = {type: TOKENS_LIMIT, unit: 3(小时), number: 5}                → 5h prompt 窗口（主额度）
//
// 旧版把 TIME_LIMIT 首条当 primary 标「5h」、其余标「周」——在无周额度的
// max 套餐上 MCP 月额被当 5h、5h 被当周（主上报错错位）。
//
// 新规则（数据驱动，不硬编码套餐形态）：
//   - primary：TOKENS_LIMIT（coding plan 主 prompt 窗口）优先；
//     无 TOKENS_LIMIT 时首条可用条兜底（其他套餐防御）。
//   - Label：由 type/unit/number 实证枚举推导（glmWindowLabel）；
//     usageDetails 非空的 TIME_LIMIT 追加「MCP·」前缀。
//   - ResetsAt：nextResetTime 毫秒 → unix 秒（旧版未解析，重置时间一直缺失）。
func glmQuotaWindows(limits []glmLimitEntry) []QuotaWindow {
	var windows []QuotaWindow
	for _, l := range limits {
		if !l.Percentage.set && !l.Remaining.set {
			continue // 数值缺省跳过
		}
		w := QuotaWindow{UsedPercent: l.Percentage.v, Remaining: l.Remaining.v}
		if l.NextResetTime.set && l.NextResetTime.v > 0 {
			w.ResetsAt = int64(l.NextResetTime.v / 1000) // 毫秒 → 秒
		}
		label := glmWindowLabel(l)
		if strings.EqualFold(strings.TrimSpace(l.Type), "TIME_LIMIT") && len(l.UsageDetails) > 0 {
			label = "MCP·" + label
		}
		w.Label = label
		windows = append(windows, w)
	}
	if len(windows) == 0 {
		return nil
	}
	// primary 逃选：TOKENS_LIMIT 优先；都没有时首条兜底。primary 排首位
	//（前端 primaryWindowOf 依赖）。
	primaryIdx := 0
	for i := range windows {
		if strings.EqualFold(strings.TrimSpace(limits[i].Type), "TOKENS_LIMIT") {
			primaryIdx = i
			break
		}
	}
	result := make([]QuotaWindow, 0, len(windows))
	for i, w := range windows {
		if i == primaryIdx {
			w.Kind = QuotaWindowPrimary
			result = append(result, w)
		}
	}
	for i, w := range windows {
		if i != primaryIdx {
			w.Kind = QuotaWindowSecondary
			result = append(result, w)
		}
	}
	return result
}

// glmWindowLabel 由实证枚举推导窗口时长标签。
// unit 枚举（v1.3.81 max 套餐实证）：3=小时、5=月；未验证枚举不硬造标签
// （返回空串，前端回退 kind 映射）。unit 为 flexString（数字形态）。
func glmWindowLabel(l glmLimitEntry) string {
	if !l.Number.set {
		return ""
	}
	u, err := strconv.ParseFloat(strings.TrimSpace(string(l.Unit)), 64)
	if err != nil {
		return ""
	}
	switch u {
	case 3: // 小时
		return strconv.FormatFloat(l.Number.v, 'f', -1, 64) + "h"
	case 5: // 月（number 恒 1）
		return "月"
	default:
		return ""
	}
}

// ProbeDeepSeekQuota 探测 DeepSeek 余额：GET {origin}/user/balance，Bearer key。
// 取 balance_infos 首条构造 Balance（字符串数字→float）；is_available=false 时
// Message 标注（余额仍展示）；401/403 → no_key。
func ProbeDeepSeekQuota(ctx context.Context, client *http.Client, origin, apiKey, providerName string) ProviderQuotaEntry {
	now := time.Now().Format(time.RFC3339)
	if client == nil || strings.TrimSpace(origin) == "" {
		return quotaErrorEntry(providerName, QuotaFamilyDeepSeek, QuotaSourceDeepSeekAPI, "探测客户端或端点不可用")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+"/user/balance", nil)
	if err != nil {
		return quotaErrorEntry(providerName, QuotaFamilyDeepSeek, QuotaSourceDeepSeekAPI, "构造请求失败: "+err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return quotaErrorEntry(providerName, QuotaFamilyDeepSeek, QuotaSourceDeepSeekAPI, "请求失败: "+err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, quotaProbeMaxBodyBytes))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ProviderQuotaEntry{Provider: providerName, Family: QuotaFamilyDeepSeek, Status: QuotaStatusNoKey,
			Message: briefQuotaMsg(fmt.Sprintf("鉴权失败（HTTP %d）", resp.StatusCode)), Source: QuotaSourceDeepSeekAPI, ProbedAt: now}
	}
	var env deepSeekBalanceEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return quotaErrorEntry(providerName, QuotaFamilyDeepSeek, QuotaSourceDeepSeekAPI, "响应解析失败: "+err.Error())
	}
	if len(env.BalanceInfos) == 0 {
		return quotaErrorEntry(providerName, QuotaFamilyDeepSeek, QuotaSourceDeepSeekAPI, "响应缺少 balance_infos 余额数据")
	}
	info := env.BalanceInfos[0]
	entry := ProviderQuotaEntry{
		Provider: providerName,
		Family:   QuotaFamilyDeepSeek,
		Status:   QuotaStatusOK,
		Balance: &QuotaBalance{
			Currency: strings.TrimSpace(info.Currency),
			Total:    parseLenientFloat(info.TotalBalance),
			Granted:  parseLenientFloat(info.GrantedBalance),
			ToppedUp: parseLenientFloat(info.ToppedUpBalance),
		},
		Source:   QuotaSourceDeepSeekAPI,
		ProbedAt: now,
	}
	if !env.IsAvailable {
		entry.Message = "账户当前不可用（is_available=false），余额仅供参考"
	}
	return entry
}

// parseLenientFloat 字符串数字宽松解析：空/不可解析按 0（契约：宽松解析）。
func parseLenientFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// ---- OpenCode Zen（opencode.ai/zen，Go 等订阅档）----
//
// ProbeOpenCodeZenQuota 探测 Zen 订阅额度：GET {base}/usage，Bearer <key>。
// 响应 usage.{rolling,weekly,monthly}，各 {status,percent,resetsAt}（上游
// anomalyco/opencode 源码实证：percent=已用%（floor(min(100,usage/limit*100))）、
// status∈{ok,rate-limited}、resetsAt=窗口重置时刻）。映射三窗口
// primary/secondary/tertiary，Label「滚动/周/月」；服务端不下发滚动时长与
// 绝对剩余数，WindowMin/Remaining 不硬造（零假数据）。状态分类：200→ok；
// 401→no_key；403→no_plan（EntitlementError，key 有效未订对应套餐）；
// 其它 HTTP/网络/解析失败→error。

// zenUsageEnvelope Zen /usage 响应（字段宽松缺省）。
type zenUsageEnvelope struct {
	Usage zenUsageData `json:"usage"`
}

type zenUsageData struct {
	Rolling zenUsageWindow `json:"rolling"`
	Weekly  zenUsageWindow `json:"weekly"`
	Monthly zenUsageWindow `json:"monthly"`
}

type zenUsageWindow struct {
	Status   string     `json:"status"`   // ok | rate-limited（percent=100 已标红，仅展示参考）
	Percent  flexNumber `json:"percent"`  // 已用 %
	ResetsAt string     `json:"resetsAt"` // ISO8601 重置时刻
}

// zenErrorEnvelope Zen 错误体（{"type":"error","error":{type,message}}）。
type zenErrorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func ProbeOpenCodeZenQuota(ctx context.Context, client *http.Client, base, apiKey, providerName string) ProviderQuotaEntry {
	family := QuotaFamilyOpenCodeZen
	now := time.Now().Format(time.RFC3339)
	if client == nil || strings.TrimSpace(base) == "" {
		return quotaErrorEntry(providerName, family, QuotaSourceOpenCodeZenAPI, "探测客户端或端点不可用")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/usage", nil)
	if err != nil {
		return quotaErrorEntry(providerName, family, QuotaSourceOpenCodeZenAPI, "构造请求失败: "+err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return quotaErrorEntry(providerName, family, QuotaSourceOpenCodeZenAPI, "请求失败: "+err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, quotaProbeMaxBodyBytes))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusNoKey,
			Message: briefQuotaMsg(zenStatusMessage("鉴权失败（HTTP 401）", body)), Source: QuotaSourceOpenCodeZenAPI, ProbedAt: now}
	case resp.StatusCode == http.StatusForbidden:
		return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusNoPlan,
			Message: briefQuotaMsg(zenStatusMessage("未开通对应订阅套餐（HTTP 403）", body)), Source: QuotaSourceOpenCodeZenAPI, ProbedAt: now}
	case resp.StatusCode != http.StatusOK:
		return quotaErrorEntry(providerName, family, QuotaSourceOpenCodeZenAPI, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	var env zenUsageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return quotaErrorEntry(providerName, family, QuotaSourceOpenCodeZenAPI, "响应解析失败: "+err.Error())
	}
	windows := zenQuotaWindows(env.Usage)
	if len(windows) == 0 {
		return quotaErrorEntry(providerName, family, QuotaSourceOpenCodeZenAPI, "响应缺少 usage 用量数据")
	}
	return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusOK,
		Windows: windows, Source: QuotaSourceOpenCodeZenAPI, ProbedAt: now}
}

// zenStatusMessage 拼接状态前缀与错误体 message（无 message 时只留前缀）。
func zenStatusMessage(prefix string, body []byte) string {
	var env zenErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil || strings.TrimSpace(env.Error.Message) == "" {
		return prefix
	}
	return prefix + "：" + strings.TrimSpace(env.Error.Message)
}

// zenQuotaWindows usage 三窗口映射（percent 缺省的窗口跳过，不硬造）。
func zenQuotaWindows(u zenUsageData) []QuotaWindow {
	type slot struct {
		kind, label string
		w           zenUsageWindow
	}
	slots := []slot{
		{QuotaWindowPrimary, "滚动", u.Rolling},
		{QuotaWindowSecondary, "周", u.Weekly},
		{QuotaWindowTertiary, "月", u.Monthly},
	}
	var windows []QuotaWindow
	for _, s := range slots {
		if !s.w.Percent.set {
			continue
		}
		w := QuotaWindow{Kind: s.kind, Label: s.label, UsedPercent: s.w.Percent.v}
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s.w.ResetsAt)); err == nil {
			w.ResetsAt = t.Unix()
		}
		windows = append(windows, w)
	}
	return windows
}

// ---- OpenRouter（openrouter.ai/api/v1）----
//
// ProbeOpenRouterQuota 探测 OpenRouter 额度：主跳 GET {base}/credits（账户级
// total_credits=累计购入 / total_usage=累计已用，实证普通 key 可用）；主跳非 200
// 时降级 GET {base}/key（per-key 口径：usage + limit/limit_remaining，Message
// 标注口径差异，不冒充余额）。状态分类：任一跳 200→ok；两跳均 401/403→
// no_key；其余→error（未决不覆盖 ok 缓存）。

// openRouterCreditsEnvelope /credits 响应（兼容 data 包裹与平铺两种形态）。
type openRouterCreditsEnvelope struct {
	Data *struct {
		TotalCredits flexNumber `json:"total_credits"`
		TotalUsage   flexNumber `json:"total_usage"`
	} `json:"data"`
	TotalCredits flexNumber `json:"total_credits"`
	TotalUsage   flexNumber `json:"total_usage"`
}

// openRouterKeyEnvelope /key 响应（只取额度相关字段；rate_limit 已废弃忽略）。
type openRouterKeyEnvelope struct {
	Data *struct {
		Limit          *flexNumber `json:"limit"`
		LimitRemaining *flexNumber `json:"limit_remaining"`
		Usage          flexNumber  `json:"usage"`
	} `json:"data"`
}

func ProbeOpenRouterQuota(ctx context.Context, client *http.Client, base, apiKey, providerName string) ProviderQuotaEntry {
	family := QuotaFamilyOpenRouter
	now := time.Now().Format(time.RFC3339)
	if client == nil || strings.TrimSpace(base) == "" {
		return quotaErrorEntry(providerName, family, QuotaSourceOpenRouterAPI, "探测客户端或端点不可用")
	}
	trimmed := strings.TrimRight(base, "/")
	// 主跳 /credits（账户余额口径）。
	pStatus, pBody, pErr := openRouterGet(ctx, client, trimmed+"/credits", apiKey)
	if pErr == nil && pStatus == http.StatusOK {
		if entry, ok := openRouterCreditsEntry(providerName, pBody, now); ok {
			return entry
		}
		pErr = errOpenRouterParse
	}
	// 降级跳 /key（per-key 口径，Message 标注）。
	sStatus, sBody, sErr := openRouterGet(ctx, client, trimmed+"/key", apiKey)
	if sErr == nil && sStatus == http.StatusOK {
		if entry, ok := openRouterKeyEntry(providerName, sBody, now); ok {
			entry.Message = "余额端点不可用，按 Key 额度上限口径"
			return entry
		}
		sErr = errOpenRouterParse
	}
	if isHTTPAuthStatus(pStatus) && isHTTPAuthStatus(sStatus) {
		return ProviderQuotaEntry{Provider: providerName, Family: family, Status: QuotaStatusNoKey,
			Message: briefQuotaMsg(fmt.Sprintf("鉴权失败（HTTP %d）", sStatus)), Source: QuotaSourceOpenRouterAPI, ProbedAt: now}
	}
	msg := "主跳 /credits 与降级 /key 均失败：credits " + openRouterLegSummary(pErr, pStatus) + "；key " + openRouterLegSummary(sErr, sStatus)
	return quotaErrorEntry(providerName, family, QuotaSourceOpenRouterAPI, msg)
}

// errOpenRouterParse 单跳 200 但响应不可用（解析失败/缺额字段）的哨兵错误。
var errOpenRouterParse = fmt.Errorf("响应解析失败或缺少额度字段")

// openRouterGet 单跳 GET（状态码/响应体/网络错误三元组）。
func openRouterGet(ctx context.Context, client *http.Client, endpoint, apiKey string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, quotaProbeMaxBodyBytes))
	return resp.StatusCode, body, nil
}

func isHTTPAuthStatus(code int) bool {
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}

// openRouterLegSummary 单跳失败概要（错误或 HTTP 码，面向 Message 拼接）。
func openRouterLegSummary(err error, status int) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("HTTP %d", status)
}

// openRouterCreditsEntry /credits → 余额条目（Remaining=Total-Used；指针保留 0 值）。
func openRouterCreditsEntry(providerName string, body []byte, now string) (ProviderQuotaEntry, bool) {
	var env openRouterCreditsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ProviderQuotaEntry{}, false
	}
	total, usage := env.TotalCredits, env.TotalUsage
	if env.Data != nil {
		total, usage = env.Data.TotalCredits, env.Data.TotalUsage
	}
	if !total.set && !usage.set {
		return ProviderQuotaEntry{}, false
	}
	totalV, usedV := total.v, usage.v
	remaining := totalV - usedV
	return ProviderQuotaEntry{
		Provider: providerName,
		Family:   QuotaFamilyOpenRouter,
		Status:   QuotaStatusOK,
		Balance: &QuotaBalance{
			Currency:  "USD",
			Total:     totalV,
			Used:      &usedV,
			Remaining: &remaining,
		},
		Source:   QuotaSourceOpenRouterAPI,
		ProbedAt: now,
	}, true
}

// openRouterKeyEntry /key → 降级条目（per-key 口径；limit_remaining 缺省不填）。
func openRouterKeyEntry(providerName string, body []byte, now string) (ProviderQuotaEntry, bool) {
	var env openRouterKeyEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
		return ProviderQuotaEntry{}, false
	}
	d := env.Data
	if !d.Usage.set && d.LimitRemaining == nil {
		return ProviderQuotaEntry{}, false
	}
	usedV := d.Usage.v
	balance := &QuotaBalance{Currency: "USD", Used: &usedV}
	if d.LimitRemaining != nil && d.LimitRemaining.set {
		remainV := d.LimitRemaining.v
		balance.Remaining = &remainV
		if d.Limit != nil && d.Limit.set {
			balance.Total = d.Limit.v
		}
	}
	return ProviderQuotaEntry{
		Provider: providerName,
		Family:   QuotaFamilyOpenRouter,
		Status:   QuotaStatusOK,
		Balance:  balance,
		Source:   QuotaSourceOpenRouterAPI,
		ProbedAt: now,
	}, true
}

// codexQuotaLine rollout jsonl 单行中和额度相关的字段子集（宽松：字段可缺）。
type codexQuotaLine struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type       string              `json:"type"`
		RateLimits *codexRateLimitData `json:"rate_limits"`
		PlanType   string              `json:"plan_type"`
	} `json:"payload"`
}

// codexRateLimitData token_count 事件携带的 rate_limits（5h/周窗口 + credits）。
type codexRateLimitData struct {
	Primary   *codexRateWindow `json:"primary"`
	Secondary *codexRateWindow `json:"secondary"`
	Credits   *codexCredits    `json:"credits"`
	PlanType  string           `json:"plan_type"`
}

type codexRateWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

type codexCredits struct {
	HasCredits bool `json:"has_credits"`
	Unlimited  bool `json:"unlimited"`
}

// ProbeCodexSubscriptionQuota 从本地 codex 会话文件提取订阅额度（契约 §2/§4）：
// 扫描 sessionsRoot（~/.codex/sessions）下最新 ≤20 个 rollout-*.jsonl（按
// mtime 降序，不读 archived_sessions），单文件只读尾部 256KB，倒序逐行找
// payload.type=="token_count" 的 rate_limits。命中 → Windows=[primary,
// secondary]、Level=plan_type、ProbedAt=事件时间戳；无文件/无命中 → error。
// 纯本地读：不触碰 ~/.codex/auth.json，不外发任何请求。
func ProbeCodexSubscriptionQuota(sessionsRoot string) ProviderQuotaEntry {
	files := latestCodexRollouts(sessionsRoot, codexQuotaMaxFiles)
	for _, path := range files {
		if line, ok := newestCodexRateLimitLine(path); ok {
			return codexEntryFromLine(line)
		}
	}
	return quotaErrorEntry(codexQuotaProviderName, QuotaFamilyCodexSub, QuotaSourceCodexSessionFile, "未找到本地 codex 会话数据")
}

// latestCodexRollouts 递归收集 sessionsRoot 下全部 rollout-*.jsonl，按 mtime
// 降序取前 max 个（archived_sessions 目录整棵跳过）。
func latestCodexRollouts(sessionsRoot string, max int) []string {
	if strings.TrimSpace(sessionsRoot) == "" {
		return nil
	}
	var files []string
	var mtimes []time.Time
	_ = filepath.WalkDir(sessionsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil // 宽松：不可读条目直接跳过
		}
		if d.IsDir() {
			if d.Name() == "archived_sessions" {
				return filepath.SkipDir // 契约：不读归档会话
			}
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		files = append(files, path)
		mtimes = append(mtimes, info.ModTime())
		return nil
	})
	type indexed struct {
		path string
		mt   time.Time
	}
	idx := make([]indexed, len(files))
	for i := range files {
		idx[i] = indexed{files[i], mtimes[i]}
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].mt.After(idx[j].mt) })
	out := make([]string, 0, max)
	for _, it := range idx {
		if len(out) >= max {
			break
		}
		out = append(out, it.path)
	}
	return out
}

// newestCodexRateLimitLine 读单个 rollout 尾部并倒序找第一条携带 rate_limits
// 的 token_count 事件（倒序保证取文件内最新一次额度快照）。
func newestCodexRateLimitLine(path string) (codexQuotaLine, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexQuotaLine{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return codexQuotaLine{}, false
	}
	offset := st.Size() - codexQuotaTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return codexQuotaLine{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, codexQuotaTailBytes))
	if err != nil {
		return codexQuotaLine{}, false
	}
	lines := strings.Split(string(data), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // 起点落在行中间时首行是残行，丢弃
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var rec codexQuotaLine
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // 宽松：单行损坏不阻断后续行
		}
		if rec.Payload.Type != "token_count" || rec.Payload.RateLimits == nil {
			continue
		}
		if rec.Payload.RateLimits.Primary == nil && rec.Payload.RateLimits.Secondary == nil {
			continue // 空壳事件继续向前找
		}
		return rec, true
	}
	return codexQuotaLine{}, false
}

// codexEntryFromLine 把命中的 token_count 事件转为统一 Entry。
func codexEntryFromLine(rec codexQuotaLine) ProviderQuotaEntry {
	entry := ProviderQuotaEntry{
		Provider: codexQuotaProviderName,
		Family:   QuotaFamilyCodexSub,
		Status:   QuotaStatusOK,
		Source:   QuotaSourceCodexSessionFile,
		ProbedAt: strings.TrimSpace(rec.Timestamp), // 事件时间戳（非探测时刻）
	}
	rl := rec.Payload.RateLimits
	if rl.Primary != nil {
		entry.Windows = append(entry.Windows, QuotaWindow{
			Kind: QuotaWindowPrimary, UsedPercent: rl.Primary.UsedPercent,
			WindowMin: rl.Primary.WindowMinutes, ResetsAt: rl.Primary.ResetsAt,
		})
	}
	if rl.Secondary != nil {
		entry.Windows = append(entry.Windows, QuotaWindow{
			Kind: QuotaWindowSecondary, UsedPercent: rl.Secondary.UsedPercent,
			WindowMin: rl.Secondary.WindowMinutes, ResetsAt: rl.Secondary.ResetsAt,
		})
	}
	// plan_type 优先取 rate_limits 内，其次 payload 平铺（宽松：两处都可缺）。
	entry.Level = strings.TrimSpace(rl.PlanType)
	if entry.Level == "" {
		entry.Level = strings.TrimSpace(rec.Payload.PlanType)
	}
	if c := rl.Credits; c != nil {
		switch {
		case c.Unlimited:
			entry.Message = "credits: 无限"
		case c.HasCredits:
			entry.Message = "credits: 有"
		default:
			entry.Message = "credits: 无"
		}
	}
	return entry
}

// RecordProviderQuota 记录一次额度探测结论并持久化（models.json 的
// provider_quota 缓存，模式同 RecordModalityProbe）。未决语义的额度版：
// Status==error 不覆盖已有 ok 缓存（环境故障不抹掉最后一次成功快照），
// 其余状态（含 unsupported——落缓存正是为了避免重复探测）正常覆写。
func (s *ConfigService) RecordProviderQuota(entry ProviderQuotaEntry) error {
	name := strings.TrimSpace(entry.Provider)
	if name == "" {
		return fmt.Errorf("provider name is required")
	}
	entry.Provider = name
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return fmt.Errorf("config not loaded")
	}
	if entry.Status == QuotaStatusError {
		if prev, ok := s.config.ProviderQuota[name]; ok && prev.Status == QuotaStatusOK {
			return nil
		}
	}
	if s.config.ProviderQuota == nil {
		s.config.ProviderQuota = map[string]ProviderQuotaEntry{}
	}
	s.config.ProviderQuota[name] = entry
	return s.saveLocked()
}

// GetProviderQuotaCache 返回额度缓存的整体快照（深拷贝，读锁保护；供 App
// 绑定 GetProviderQuotas 转发给前端，防止并发探测写穿共享引用）。
func (s *ConfigService) GetProviderQuotaCache() map[string]ProviderQuotaEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return map[string]ProviderQuotaEntry{}
	}
	out := make(map[string]ProviderQuotaEntry, len(s.config.ProviderQuota))
	for k, v := range s.config.ProviderQuota {
		out[k] = cloneProviderQuotaEntry(v)
	}
	return out
}

// cloneProviderQuotaEntry 深拷贝单条 Entry（Windows 切片与 Balance 指针）。
func cloneProviderQuotaEntry(e ProviderQuotaEntry) ProviderQuotaEntry {
	if e.Windows != nil {
		e.Windows = append([]QuotaWindow(nil), e.Windows...)
	}
	if e.Balance != nil {
		b := *e.Balance
		// 指针字段解引用拷贝（v1.3.86 Used/Remaining）：防并发写穿共享引用。
		if b.Used != nil {
			u := *b.Used
			b.Used = &u
		}
		if b.Remaining != nil {
			r := *b.Remaining
			b.Remaining = &r
		}
		e.Balance = &b
	}
	return e
}

// LookupProviderQuota 从缓存取单条额度快照；缓存缺失返回零值 + false。
func (cfg *AppConfig) LookupProviderQuota(provider string) (ProviderQuotaEntry, bool) {
	if cfg == nil || cfg.ProviderQuota == nil {
		return ProviderQuotaEntry{}, false
	}
	entry, ok := cfg.ProviderQuota[strings.TrimSpace(provider)]
	return entry, ok
}
