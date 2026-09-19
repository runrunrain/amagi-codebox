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
)

// 窗口 Kind 常量（QuotaWindow.Kind 值域）。
const (
	QuotaWindowPrimary   = "primary"   // 主显示窗口（GLM TIME_LIMIT / Codex 5h）
	QuotaWindowSecondary = "secondary" // 次窗口（GLM 其他限额 / Codex 周）
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
type glmLimitEntry struct {
	Type         string     `json:"type"`
	Unit         flexString `json:"unit"`
	Number       flexNumber `json:"number"`
	Usage        flexNumber `json:"usage"`
	CurrentValue flexNumber `json:"currentValue"`
	Remaining    flexNumber `json:"remaining"`
	Percentage   flexNumber `json:"percentage"`
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
//   - 含 api.deepseek.com → deepseek，origin 同域。
//
// 无匹配返回 ("", "")——调用方落 Status=unsupported 缓存避免重复探测。
// 家族判别只认 provider 自身配置的域，绝不外发第三方。
func DetectQuotaFamily(baseURL string) (family, origin string) {
	lower := strings.ToLower(strings.TrimSpace(baseURL))
	var matched string
	switch {
	case strings.Contains(lower, "bigmodel.cn"):
		matched = QuotaFamilyGLMBigmodel
	case strings.Contains(lower, "api.z.ai"):
		matched = QuotaFamilyGLMZai
	case strings.Contains(lower, "api.deepseek.com"):
		matched = QuotaFamilyDeepSeek
	default:
		return "", ""
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
	return matched, u.Scheme + "://" + strings.ToLower(u.Host)
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

// glmQuotaWindows 筛选 GLM limits 条目：type==TIME_LIMIT 为主窗口
// （kind=primary），其余携带 percentage/remaining 的条目次之（kind=secondary）。
func glmQuotaWindows(limits []glmLimitEntry) []QuotaWindow {
	var primary *QuotaWindow
	var secondary []QuotaWindow
	for _, l := range limits {
		if !l.Percentage.set && !l.Remaining.set {
			continue // 数值缺省跳过
		}
		w := QuotaWindow{UsedPercent: l.Percentage.v, Remaining: l.Remaining.v}
		if strings.EqualFold(strings.TrimSpace(l.Type), "TIME_LIMIT") {
			if primary == nil {
				w.Kind = QuotaWindowPrimary
				primary = &w
			}
			continue
		}
		w.Kind = QuotaWindowSecondary
		secondary = append(secondary, w)
	}
	if primary == nil && len(secondary) == 0 {
		return nil
	}
	out := make([]QuotaWindow, 0, 1+len(secondary))
	if primary != nil {
		out = append(out, *primary)
	}
	return append(out, secondary...)
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
