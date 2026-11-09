package main

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/usage"
)

// 本文件实现 usage 域 Handler（usage summary/daily/models/providers/requests/sync + logs）。
// 过滤参数与 internal/usage/api_types.go 的 Filter 结构一一对应，返回值直接复用
// 服务层已带 camelCase json tag 的结构；logs 输出经 agentCLIScrubCredentials
// 对常见凭据形态做清洗（D-01c）。

func (h *agentCLIHost) usageService() (*usage.Service, *spec.Error) {
	if h.app == nil || h.app.Usage == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "usage 服务未初始化")
	}
	return h.app.Usage, nil
}

// agentCLIDateFilter 校验并提取闭区间日期参数。
func agentCLIDateFilter(args spec.Args) (string, string, *spec.Error) {
	start := strings.TrimSpace(args.String("startDate"))
	end := strings.TrimSpace(args.String("endDate"))
	for name, v := range map[string]string{"startDate": start, "endDate": end} {
		if v != "" && !agentCLIIsDate(v) {
			return "", "", agentCLIArgErrf("%s 需为 YYYY-MM-DD 格式，收到 %q", name, v)
		}
	}
	if (start == "") != (end == "") {
		return "", "", agentCLIArgErrf("startDate 与 endDate 必须成对给出")
	}
	return start, end, nil
}

func agentCLIIsDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	if _, err := strconv.Atoi(s[0:4]); err != nil {
		return false
	}
	m, err1 := strconv.Atoi(s[5:7])
	if err1 != nil || m < 1 || m > 12 {
		return false
	}
	d, err2 := strconv.Atoi(s[8:10])
	return err2 == nil && d >= 1 && d <= 31
}

func agentCLISummaryFilter(args spec.Args) (usage.SummaryFilter, *spec.Error) {
	start, end, serr := agentCLIDateFilter(args)
	if serr != nil {
		return usage.SummaryFilter{}, serr
	}
	return usage.SummaryFilter{
		StartDate: start,
		EndDate:   end,
		AppType:   strings.TrimSpace(args.String("appType")),
		Source:    strings.TrimSpace(args.String("source")),
		Provider:  strings.TrimSpace(args.String("provider")),
	}, nil
}

func (h *agentCLIHost) handleUsageSummary(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.usageService()
	if serr != nil {
		return nil, serr
	}
	filter, aerr := agentCLISummaryFilter(args)
	if aerr != nil {
		return nil, aerr
	}
	summary, err := svc.GetUsageSummary(filter)
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return summary, nil
}

func (h *agentCLIHost) handleUsageDaily(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.usageService()
	if serr != nil {
		return nil, serr
	}
	base, aerr := agentCLISummaryFilter(args)
	if aerr != nil {
		return nil, aerr
	}
	filter := usage.TrendFilter{SummaryFilter: base}
	filter.Granularity = strings.TrimSpace(args.String("granularity"))
	if filter.Granularity == "" {
		filter.Granularity = "day"
	}
	if filter.Granularity != "day" && filter.Granularity != "week" {
		return nil, agentCLIArgErrf("granularity 只支持 day/week，收到 %q", filter.Granularity)
	}
	// 有显式日期区间时忽略 days，否则取 --days（缺省 30）。
	if base.StartDate == "" && base.EndDate == "" {
		days := args.Int("days", 30)
		if days <= 0 {
			return nil, agentCLIArgErrf("days 需为正整数，收到 %d", days)
		}
		filter.Days = days
	}
	points, err := svc.GetDailyTrends(filter)
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"points": points, "count": len(points)}, nil
}

func (h *agentCLIHost) handleUsageModels(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.usageService()
	if serr != nil {
		return nil, serr
	}
	base, aerr := agentCLISummaryFilter(args)
	if aerr != nil {
		return nil, aerr
	}
	stats, err := svc.GetModelStats(usage.StatFilter{SummaryFilter: base})
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"models": stats, "count": len(stats)}, nil
}

func (h *agentCLIHost) handleUsageProviders(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.usageService()
	if serr != nil {
		return nil, serr
	}
	base, aerr := agentCLISummaryFilter(args)
	if aerr != nil {
		return nil, aerr
	}
	stats, err := svc.GetProviderStats(usage.StatFilter{SummaryFilter: base})
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"providers": stats, "count": len(stats)}, nil
}

func (h *agentCLIHost) handleUsageRequests(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.usageService()
	if serr != nil {
		return nil, serr
	}
	base, aerr := agentCLISummaryFilter(args)
	if aerr != nil {
		return nil, aerr
	}
	page := args.Int("page", 1)
	if page <= 0 {
		return nil, agentCLIArgErrf("page 需为正整数，收到 %d", page)
	}
	pageSize := args.Int("pageSize", 50)
	if pageSize <= 0 || pageSize > 500 {
		return nil, agentCLIArgErrf("pageSize 需在 1..500，收到 %d", pageSize)
	}
	logs, err := svc.GetRequestLogs(usage.LogFilter{
		SummaryFilter: base,
		Model:         strings.TrimSpace(args.String("model")),
		Page:          page,
		PageSize:      pageSize,
	})
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"requests": logs, "count": len(logs), "page": page, "pageSize": pageSize}, nil
}

func (h *agentCLIHost) handleUsageSync(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	svc, serr := h.usageService()
	if serr != nil {
		return nil, serr
	}
	// 入库扫描不可中途取消：提交前再检查一次，避免已取消请求继续启动长扫描。
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if serr != nil {
		return nil, serr
	}
	result, err := svc.SyncSessionUsage()
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return result, nil
}

// ---- logs ----

// 常见凭据形态（正则命中段替换为掩码占位）：
//   - Authorization / Proxy-Authorization / X-Api-Key / Api-Key 头部值（保留前缀词）；
//   - 独立 Bearer 令牌；
//   - 常见 token 前缀族（sk- / ghp_ / gho_ / ghu_ / ghs_ / ghr_ / github_pat_ /
//     xox[bapr]- / glpat- / AKIA）。
var (
	agentCLIScrubAuthHeaderRe = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|x-api-key|api-key)(\s*[:=]\s*)(?:bearer\s+)?[^\s"';,]{4,}`)
	agentCLIScrubBearerRe     = regexp.MustCompile(`(?i)\b(bearer\s+)[a-z0-9._~+/=-]{6,}`)
	agentCLIScrubTokenRe      = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{7,}|gh[posuhr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|glpat-[A-Za-z0-9_-]{10,}|AKIA[0-9A-Z]{16})\b`)
)

// agentCLIScrubCredentials 清洗文本中的常见凭据（纯函数，供 logs 输出
// 脱敏与测试）：先精确替换已知密钥值（来自 Secrets 的当前存储值；降序替换
// 避免前缀遮蔽，短于 8 字符的值不参与精确替换以免误伤普通文本），再按形态
// 掩码 Authorization/Bearer 值与 token 前缀族。
func agentCLIScrubCredentials(text string, knownSecrets []string) string {
	out := text
	secrets := make([]string, 0, len(knownSecrets))
	for _, s := range knownSecrets {
		if len(strings.TrimSpace(s)) >= 8 {
			secrets = append(secrets, s)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, s := range secrets {
		out = strings.ReplaceAll(out, s, agentCLIHeaderMask)
	}
	out = agentCLIScrubAuthHeaderRe.ReplaceAllString(out, "$1$2"+agentCLIHeaderMask)
	out = agentCLIScrubBearerRe.ReplaceAllString(out, "$1"+agentCLIHeaderMask)
	out = agentCLIScrubTokenRe.ReplaceAllString(out, agentCLIHeaderMask)
	return out
}

// knownStoredSecrets 收集当前已存储的全部 provider key 明文（仅用于输出侧
// 精确替换为掩码，绝不进入返回值）。
func (h *agentCLIHost) knownStoredSecrets() []string {
	a := h.app
	if a == nil || a.Secrets == nil {
		return nil
	}
	names := a.Secrets.GetAllProviders()
	if a.Config != nil {
		names = append(names, a.Config.GetProviderNames()...)
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if key, err := a.Secrets.GetAPIKey(n); err == nil && strings.TrimSpace(key) != "" {
			out = append(out, key)
		}
	}
	return out
}

func (h *agentCLIHost) handleLogs(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.Log == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "日志服务未初始化")
	}
	level := strings.TrimSpace(args.String("level"))
	switch level {
	case "", "DEBUG", "INFO", "WARN", "ERROR":
	default:
		return nil, agentCLIArgErrf("level 只支持 DEBUG/INFO/WARN/ERROR，收到 %q", level)
	}
	limit := args.Int("limit", 100)
	if limit <= 0 {
		return nil, agentCLIArgErrf("limit 需为正整数，收到 %d", limit)
	}
	entries := h.app.GetLogs(level, strings.TrimSpace(args.String("source")), strings.TrimSpace(args.String("keyword")), limit)
	if entries == nil {
		entries = []logging.Entry{}
	}
	// 输出侧凭据清洗（D-01c）：日志正文/详情可能回显终端粘贴的密钥或请求头。
	known := h.knownStoredSecrets()
	for i := range entries {
		entries[i].Message = agentCLIScrubCredentials(entries[i].Message, known)
		entries[i].Detail = agentCLIScrubCredentials(entries[i].Detail, known)
	}
	return map[string]any{"entries": entries, "count": len(entries)}, nil
}

func (h *agentCLIHost) usageHandlers() map[string]spec.Handler {
	return map[string]spec.Handler{
		"usage.summary":   h.handleUsageSummary,
		"usage.daily":     h.handleUsageDaily,
		"usage.models":    h.handleUsageModels,
		"usage.providers": h.handleUsageProviders,
		"usage.requests":  h.handleUsageRequests,
		"usage.sync":      h.handleUsageSync,
		"logs":            h.handleLogs,
	}
}
