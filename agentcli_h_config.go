package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/settings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 本文件实现 config 域 Handler（provider / preset / key / settings / envvars / workdir）。
// 安全基线：所有返回结构不含任何明文密钥——provider 视图逐字段核对自建 DTO
// 并对认证类 header 掩码，settings 视图对凭据字段做置空脱敏，敏感环境变量名
// （含 KEY/TOKEN/SECRET/PASSWORD/PASSWD/CREDENTIAL/AUTH）的值一律掩码，
// 密钥写入只经 key-set 的 Secret 参数；provider/preset 保存一律在现有对象上
// 合并（DTO 出现的字段覆盖，未出现的原样保留），避免有损 DTO 整体替换。

// agentCLIHeaderMask 是 CLI 输出中凭据值的统一掩码占位；provider save 收到
// 该占位时解释为"保留现有真实值"，绝不把掩码写进配置。
const agentCLIHeaderMask = "***"

// agentCLIAuthHeaderNeedles 认证类 header 名的子串特征（名称小写并去掉
// "-" / "_" 后包含任一即认定：authorization / x-api-key / api-key / token /
// secret / cookie / password / passwd / credential 等）。
var agentCLIAuthHeaderNeedles = []string{
	"authorization", "apikey", "token", "secret", "cookie",
	"password", "passwd", "credential",
}

// agentCLIIsAuthHeaderName 判定 header 名是否属认证类（大小写与分隔符不敏感）。
func agentCLIIsAuthHeaderName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "-", "")
	n = strings.ReplaceAll(n, "_", "")
	for _, needle := range agentCLIAuthHeaderNeedles {
		if strings.Contains(n, needle) {
			return true
		}
	}
	return false
}

// agentCLISensitiveEnvVarNeedles 敏感环境变量名的子串特征（名称大写后包含任一
// 即认定，值一律掩码并附 valueSet 布尔）。
var agentCLISensitiveEnvVarNeedles = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH",
}

// agentCLIIsSensitiveEnvVarName 判定环境变量名是否属敏感类。
func agentCLIIsSensitiveEnvVarName(name string) bool {
	n := strings.ToUpper(strings.TrimSpace(name))
	for _, needle := range agentCLISensitiveEnvVarNeedles {
		if strings.Contains(n, needle) {
			return true
		}
	}
	return false
}

// agentCLIEnvHeaderRefRe 与 internal/config 的 $ENV: 引用全串匹配同义：引用
// 不是明文凭据，输出不掩码、保存按引用原样放行。
var agentCLIEnvHeaderRefRe = regexp.MustCompile(`^\$\{ENV:([A-Za-z_][A-Za-z0-9_]*)\}$|^\$ENV:([A-Za-z_][A-Za-z0-9_]*)$`)

func agentCLIIsEnvRefHeader(v string) bool {
	return agentCLIEnvHeaderRefRe.MatchString(strings.TrimSpace(v))
}

// ---- provider 脱敏视图（DTO，永不包含 api_key/auth_key 字段）----

type agentCLIKeyStatusView struct {
	Source      string            `json:"source"`
	HasStored   bool              `json:"hasStored"`
	MaskedKey   string            `json:"maskedKey"`
	KeyLength   int               `json:"keyLength"`
	Environment map[string]string `json:"environment,omitempty"`
}

type agentCLIProviderFormatView struct {
	Enabled    bool              `json:"enabled"`
	BaseURL    string            `json:"baseUrl,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`    // 认证类 header 值已掩码
	HeadersSet map[string]bool   `json:"headersSet,omitempty"` // 认证类 header 的“已设置”布尔（配合掩码值）
	AuthHeader *bool             `json:"authHeader,omitempty"`
}

type agentCLIProviderOpenAIView struct {
	Enabled      bool              `json:"enabled"`
	BaseURL      string            `json:"baseUrl,omitempty"`
	Organization string            `json:"organization,omitempty"`
	WireAPI      string            `json:"wireApi,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`    // 认证类 header 值已掩码
	HeadersSet   map[string]bool   `json:"headersSet,omitempty"` // 认证类 header 的“已设置”布尔
	AuthHeader   *bool             `json:"authHeader,omitempty"`
}

type agentCLIPresetView struct {
	Key            string          `json:"key,omitempty"` // 配置内的 preset map 键（与显示名 Name 可不同），保证 get→save 往返无损
	Name           string          `json:"name,omitempty"`
	Model          string          `json:"model,omitempty"`
	ModelHaiku     string          `json:"modelHaiku,omitempty"`
	ModelSonnet    string          `json:"modelSonnet,omitempty"`
	ModelOpus      string          `json:"modelOpus,omitempty"`
	Target         string          `json:"target,omitempty"`
	Parameters     json.RawMessage `json:"parameters,omitempty"`
	OpenCodeConfig json.RawMessage `json:"opencodeConfig,omitempty"` // 原样透出，保证 get→save 往返无损
}

type agentCLIProviderView struct {
	Name         string                      `json:"name"`
	DefaultModel string                      `json:"defaultModel,omitempty"`
	Anthropic    *agentCLIProviderFormatView `json:"anthropic,omitempty"`
	OpenAI       *agentCLIProviderOpenAIView `json:"openai,omitempty"`
	Presets      []agentCLIPresetView        `json:"presets,omitempty"`
	Key          *agentCLIKeyStatusView      `json:"key,omitempty"`
}

func agentCLIKeyStatusViewFrom(entry map[string]string) *agentCLIKeyStatusView {
	if entry == nil {
		return nil
	}
	view := &agentCLIKeyStatusView{
		Source:    entry["source"],
		HasStored: entry["has_stored"] == "true",
		MaskedKey: entry["masked_key"],
	}
	if n, err := strconv.Atoi(entry["key_length"]); err == nil {
		view.KeyLength = n
	}
	for k, v := range entry {
		if strings.HasPrefix(k, "env_") {
			if view.Environment == nil {
				view.Environment = map[string]string{}
			}
			view.Environment[strings.TrimPrefix(k, "env_")] = v
		}
	}
	return view
}

// buildAgentCLIProviderView 从 config.Provider 构造脱敏视图。
// 逐字段白名单拷贝：Provider 上的 Anthropic.APIKey/OpenAI.APIKey/AuthKey、
// 废弃 AuthKey 等密钥位一律不进入视图。
func buildAgentCLIProviderView(name string, p config.Provider, diag map[string]string) agentCLIProviderView {
	view := agentCLIProviderView{
		Name:         name,
		DefaultModel: p.DefaultModel,
	}
	if p.Anthropic != nil {
		headers, headersSet := agentCLIMaskHeaders(p.Anthropic.Headers)
		view.Anthropic = &agentCLIProviderFormatView{
			Enabled:    p.Anthropic.Enabled,
			BaseURL:    p.Anthropic.BaseURL,
			Headers:    headers,
			HeadersSet: headersSet,
			AuthHeader: p.Anthropic.AuthHeader,
		}
	}
	if p.OpenAI != nil {
		headers, headersSet := agentCLIMaskHeaders(p.OpenAI.Headers)
		view.OpenAI = &agentCLIProviderOpenAIView{
			Enabled:      p.OpenAI.Enabled,
			BaseURL:      p.OpenAI.BaseURL,
			Organization: p.OpenAI.Organization,
			WireAPI:      p.OpenAI.WireAPI,
			Headers:      headers,
			HeadersSet:   headersSet,
			AuthHeader:   p.OpenAI.AuthHeader,
		}
	}
	if len(p.Presets) > 0 {
		names := make([]string, 0, len(p.Presets))
		for n := range p.Presets {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			pv := agentCLIPresetViewFromPreset(p.Presets[n])
			pv.Key = n
			view.Presets = append(view.Presets, pv)
		}
	}
	view.Key = agentCLIKeyStatusViewFrom(diag)
	return view
}

// agentCLIMaskHeaders 返回脱敏 header 副本：认证类 header 的明文值替换为
// 掩码占位，同时返回各认证类 header 的“值已设置”布尔；$ENV: 引用不是明文，
// 原样保留。原 map 不被修改。
func agentCLIMaskHeaders(headers map[string]string) (map[string]string, map[string]bool) {
	if headers == nil {
		return nil, nil
	}
	out := make(map[string]string, len(headers))
	var set map[string]bool
	for k, v := range headers {
		if agentCLIIsAuthHeaderName(k) && !agentCLIIsEnvRefHeader(v) {
			out[k] = agentCLIHeaderMask
			if set == nil {
				set = map[string]bool{}
			}
			set[k] = strings.TrimSpace(v) != ""
			continue
		}
		out[k] = v
	}
	return out, set
}

func agentCLIPresetViewFromPreset(p config.Preset) agentCLIPresetView {
	view := agentCLIPresetView{
		Name:        p.Name,
		Model:       p.Model,
		ModelHaiku:  p.ModelHaiku,
		ModelSonnet: p.ModelSonnet,
		ModelOpus:   p.ModelOpus,
		Target:      string(p.Target),
	}
	if raw, err := json.Marshal(p.Parameters); err == nil && string(raw) != "{}" {
		view.Parameters = raw
	}
	if len(p.OpenCodeConfig) > 0 {
		view.OpenCodeConfig = p.OpenCodeConfig
	}
	return view
}

// agentCLIValidateSaveHeaders 校验 save DTO 中 header 的写入边界：header 值
// 必须是字符串；认证类 header 只接受掩码占位（= 保留现有值）、$ENV: 引用与
// 空串（= 删除），明文一律拒绝并提示改用桌面应用或 provider key-set（D-01b）。
func agentCLIValidateSaveHeaders(dto map[string]any) *spec.Error {
	for _, format := range []string{"anthropic", "openai"} {
		sub, ok := dto[format].(map[string]any)
		if !ok {
			continue
		}
		headers, ok := sub["headers"].(map[string]any)
		if !ok {
			continue
		}
		for k, raw := range headers {
			v, ok := raw.(string)
			if !ok {
				return agentCLIArgErrf("%s.headers.%s 的值必须是字符串", format, k)
			}
			if !agentCLIIsAuthHeaderName(k) || v == agentCLIHeaderMask || agentCLIIsEnvRefHeader(v) || strings.TrimSpace(v) == "" {
				continue
			}
			return agentCLIArgErrf("认证类 header %s 不能以明文经 provider save 写入：请在桌面应用配置，或改用 $ENV: 环境变量引用；API 密钥请用 provider key-set", k)
		}
	}
	return nil
}

// agentCLIOverlayProvider 把 CLI DTO（已解析的 map，键出现即覆盖）合并到现有
// Provider 上：DTO 未提及的字段原样保留（含 UrlHistory、preset 的
// OpenCodeConfig、未列出的 header/preset）。认证类 header 传掩码占位时保留
// 现有真实值（无现有值则不设置）；空串表示删除该 header（D-02）。
func agentCLIOverlayProvider(existing *config.Provider, dto map[string]any) (config.Provider, error) {
	merged := config.Provider{}
	if existing != nil {
		merged = *existing
	}
	merged.Presets = nil
	if existing != nil && len(existing.Presets) > 0 {
		merged.Presets = make(map[string]config.Preset, len(existing.Presets))
		for k, v := range existing.Presets {
			merged.Presets[k] = v
		}
	}
	if v, ok := dto["defaultModel"].(string); ok {
		merged.DefaultModel = v
	}
	if sub, ok := dto["anthropic"].(map[string]any); ok {
		merged.Anthropic = overlayAgentCLIAnthropic(merged.Anthropic, sub)
	}
	if sub, ok := dto["openai"].(map[string]any); ok {
		f, err := overlayAgentCLIOpenAI(merged.OpenAI, sub)
		if err != nil {
			return merged, err
		}
		merged.OpenAI = f
	}
	if list, ok := dto["presets"].([]any); ok {
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				return merged, agentCLIArgErrf("presets 条目必须是对象")
			}
			// 键优先用 view 的 key（配置 map 键，与显示名 Name 可不同），
			// 未提供时回退 name——避免往返时把 Name 当键写出重复 preset。
			key, _ := m["key"].(string)
			key = strings.TrimSpace(key)
			if key == "" {
				key, _ = m["name"].(string)
				key = strings.TrimSpace(key)
			}
			if key == "" {
				return merged, agentCLIArgErrf("presets 条目缺少 name/key")
			}
			np, err := overlayAgentCLIPreset(merged.Presets[key], m)
			if err != nil {
				return merged, err
			}
			if merged.Presets == nil {
				merged.Presets = map[string]config.Preset{}
			}
			merged.Presets[key] = np
		}
	}
	return merged, nil
}

// overlayAgentCLIPresetHeaders 按 header 键合并：DTO 出现的键覆盖（认证类的
// 掩码占位与空串有专用语义），未出现的保留现有值。值类型已由
// agentCLIValidateSaveHeaders 校验为字符串。
func overlayAgentCLIPresetHeaders(existing map[string]string, dto map[string]any) map[string]string {
	out := make(map[string]string, len(existing)+len(dto))
	for k, v := range existing {
		out[k] = v
	}
	for k, raw := range dto {
		v, _ := raw.(string)
		if agentCLIIsAuthHeaderName(k) && v == agentCLIHeaderMask {
			continue // 掩码占位 = 保留现有真实值（无现有值则不设置）
		}
		if strings.TrimSpace(v) == "" {
			delete(out, k) // 空串 = 删除该 header
			continue
		}
		out[k] = v
	}
	return out
}

// overlayAgentCLIAnthropic 在现有 Anthropic 格式上覆盖 DTO 出现的键。
// 值拷贝基底，给终不修改 ConfigService 内部指针所指向的对象。
func overlayAgentCLIAnthropic(base *config.AnthropicFormat, sub map[string]any) *config.AnthropicFormat {
	f := config.AnthropicFormat{}
	if base != nil {
		f = *base
	}
	if v, ok := sub["enabled"].(bool); ok {
		f.Enabled = v
	}
	if v, ok := sub["baseUrl"].(string); ok {
		f.BaseURL = v
	}
	if headers, ok := sub["headers"].(map[string]any); ok {
		f.Headers = overlayAgentCLIPresetHeaders(f.Headers, headers)
	}
	if v, ok := sub["authHeader"].(bool); ok {
		t := v
		f.AuthHeader = &t
	}
	return &f
}

// overlayAgentCLIOpenAI 在现有 OpenAI 格式上覆盖 DTO 出现的键（wireApi 校验
// 与 EffectiveWireAPI 同规：仅 chat/responses/空）。
func overlayAgentCLIOpenAI(base *config.OpenAIFormat, sub map[string]any) (*config.OpenAIFormat, error) {
	f := config.OpenAIFormat{}
	if base != nil {
		f = *base
	}
	if v, ok := sub["enabled"].(bool); ok {
		f.Enabled = v
	}
	if v, ok := sub["baseUrl"].(string); ok {
		f.BaseURL = v
	}
	if v, ok := sub["organization"].(string); ok {
		f.Organization = v
	}
	if v, ok := sub["wireApi"].(string); ok {
		norm := strings.ToLower(strings.TrimSpace(v))
		switch norm {
		case "", config.WireAPIChat, config.WireAPIResponses:
			f.WireAPI = norm
		default:
			return nil, agentCLIArgErrf("wireApi 只支持 chat/responses，收到 %q", v)
		}
	}
	if headers, ok := sub["headers"].(map[string]any); ok {
		f.Headers = overlayAgentCLIPresetHeaders(f.Headers, headers)
	}
	if v, ok := sub["authHeader"].(bool); ok {
		t := v
		f.AuthHeader = &t
	}
	return &f, nil
}

// overlayAgentCLIPreset 在现有 provider preset 上覆盖 DTO 出现的键；未提及的
// 字段（含 opencode_config/parameters）原样保留。
func overlayAgentCLIPreset(base config.Preset, m map[string]any) (config.Preset, error) {
	p := base
	if v, ok := m["name"].(string); ok && strings.TrimSpace(v) != "" {
		p.Name = v
	}
	if v, ok := m["model"].(string); ok {
		p.Model = v
	}
	if v, ok := m["modelHaiku"].(string); ok {
		p.ModelHaiku = v
	}
	if v, ok := m["modelSonnet"].(string); ok {
		p.ModelSonnet = v
	}
	if v, ok := m["modelOpus"].(string); ok {
		p.ModelOpus = v
	}
	if v, ok := m["target"].(string); ok {
		switch config.PresetTargetType(v) {
		case "", config.PresetTargetCodex, config.PresetTargetOpenCode, config.PresetTargetPi:
			p.Target = config.PresetTargetType(v)
		default:
			return p, agentCLIArgErrf("preset %s 的 target 非法：%q（可用 codex/opencode/pi）", p.Name, v)
		}
	}
	if raw, ok := m["parameters"]; ok {
		if raw == nil {
			p.Parameters = config.Parameters{}
		} else {
			b, merr := json.Marshal(raw)
			if merr != nil {
				return p, agentCLIServiceErr(merr)
			}
			if uerr := json.Unmarshal(b, &p.Parameters); uerr != nil {
				return p, agentCLIArgErrf("preset %s 的 parameters 结构不符：%v", p.Name, uerr)
			}
		}
	}
	if raw, ok := m["opencodeConfig"]; ok {
		if raw == nil {
			p.OpenCodeConfig = nil
		} else {
			b, merr := json.Marshal(raw)
			if merr != nil {
				return p, agentCLIServiceErr(merr)
			}
			p.OpenCodeConfig = json.RawMessage(b)
		}
	}
	return p, nil
}

// agentCLISecretFieldPath 递归扫描已解析的 JSON 值，返回第一个非空密钥字段的
// 路径（apiKey/authKey 及 snake_case 变体，任意深度）。用于 provider/preset
// 保存入口拒绝内嵌密钥。
func agentCLISecretFieldPath(v any, path string) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if agentCLIIsSecretFieldName(k) {
				if s, ok := t[k].(string); ok && strings.TrimSpace(s) != "" {
					return p, true
				}
			}
			if hit, found := agentCLISecretFieldPath(t[k], p); found {
				return hit, true
			}
		}
	case []any:
		for i, item := range t {
			p := fmt.Sprintf("%s[%d]", path, i)
			if hit, found := agentCLISecretFieldPath(item, p); found {
				return hit, true
			}
		}
	}
	return "", false
}

func agentCLIIsSecretFieldName(name string) bool {
	switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "_", ""), "-", "")) {
	case "apikey", "authkey":
		return true
	}
	return false
}

// ---- provider handlers ----

func (h *agentCLIHost) handleProviderList(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Config == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "config 服务未初始化")
	}
	typeFilter := strings.TrimSpace(args.String("type"))
	if typeFilter != "" && typeFilter != "anthropic" && typeFilter != "openai" {
		return nil, agentCLIArgErrf("type 只支持 anthropic/openai，收到 %q", typeFilter)
	}
	providers := a.Config.GetProviders()
	if typeFilter != "" {
		providers = a.GetProvidersByType(typeFilter)
	}
	var diags map[string]map[string]string
	if a.Secrets != nil && a.Config != nil {
		diags = a.Secrets.GetKeyDiagnostics(a.Config.GetProviderNames())
	}
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	views := make([]agentCLIProviderView, 0, len(names))
	for _, n := range names {
		views = append(views, buildAgentCLIProviderView(n, providers[n], diags[n]))
	}
	return map[string]any{"providers": views, "count": len(views)}, nil
}

func (h *agentCLIHost) providerGetView(name string) (*agentCLIProviderView, error) {
	a := h.app
	p, err := a.Config.GetProvider(name)
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	var diag map[string]string
	if a.Secrets != nil {
		diag = a.Secrets.GetKeyDiagnostics([]string{name})[name]
	}
	view := buildAgentCLIProviderView(name, *p, diag)
	return &view, nil
}

func (h *agentCLIHost) handleProviderGet(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Config == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "config 服务未初始化")
	}
	name := strings.TrimSpace(args.String("name"))
	if name == "" {
		return nil, agentCLIArgErrf("缺少必填参数 name")
	}
	return h.providerGetView(name)
}

func (h *agentCLIHost) handleProviderSave(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Config == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "config 服务未初始化")
	}
	name := strings.TrimSpace(args.String("name"))
	if name == "" {
		return nil, agentCLIArgErrf("缺少必填参数 name")
	}
	if strings.Contains(name, "/") {
		return nil, agentCLIArgErrf("provider name 不可包含 /")
	}
	raw, err := agentCLIParseJSONArg(args["json"])
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, agentCLIArgErrf("缺少必填参数 json")
	}
	dto, ok := raw.(map[string]any)
	if !ok {
		return nil, agentCLIArgErrf("json 必须是对象（与 provider get 输出同构）")
	}
	if hit, found := agentCLISecretFieldPath(dto, ""); found {
		return nil, agentCLIArgErrf("json 含密钥字段 %s：密钥请改用 provider key-set（stdin）写入", hit)
	}
	if verr := agentCLIValidateSaveHeaders(dto); verr != nil {
		return nil, verr
	}
	// D-02：在现有对象上合并——DTO 出现的字段覆盖，未出现的（含 DTO 不暴露的
	// url_history、preset.opencode_config、未列出的 header/preset）原样保留。
	var existing *config.Provider
	if p, gerr := a.Config.GetProvider(name); gerr == nil {
		existing = p
	}
	merged, merr := agentCLIOverlayProvider(existing, dto)
	if merr != nil {
		return nil, merr
	}
	// 保存链路上游缺陷保全值：internal/config scrubProviderAPIKeys 在任何
	// SaveProvider 时都会清空 wire_api（已上报 Leader，上游修复后本调用点与
	// restoreProviderWireAPI 可整体移除）。
	wireWant := merged.OpenAI.EffectiveWireAPI()
	if err := a.Config.SaveProvider(name, merged); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	if werr := h.restoreProviderWireAPI(name, wireWant); werr != nil {
		return nil, werr
	}
	if err := a.syncProvidersToHarnesses(); err != nil {
		return nil, spec.Errorf(spec.ErrInternal, "提供商配置已保存，但同步 OpenCode/Pi/OMP 配置失败: %s", err.Error())
	}
	a.Log.Info("app", "agent-CLI 保存提供商配置", name)
	saved, err := h.providerGetView(name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"saved": true, "provider": saved}, nil
}

// restoreProviderWireAPI 规避 internal/config scrubProviderAPIKeys 丢 WireAPI
// 的上游缺陷（该缺陷比 CLI 路径更宽：任何 saveLocked 都会把内存与磁盘上全部
// provider 的 wire_api 清空；已上报，上游修复后本函数与调用点可整体移除）：
// 保存后若 wire_api 被清而期望值非空，用 sjson 把 models.json 内该 provider
// 的 wire_api 精确写回（tmp+rename 原子写、0600）并重载配置。
func (h *agentCLIHost) restoreProviderWireAPI(name, want string) error {
	if want == "" {
		return nil
	}
	a := h.app
	p, err := a.Config.GetProvider(name)
	if err != nil {
		return agentCLIServiceErr(err)
	}
	if p.OpenAI != nil && p.OpenAI.WireAPI == want {
		return nil // 未被丢弃（上游已修复时短路）
	}
	path := filepath.Join(agentCLIHomeDir(a), "models.json")
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		return spec.Errorf(spec.ErrInternal, "wire_api 保全：读取 models.json 失败: %v", rerr)
	}
	field := "models." + agentCLIGJSONPathKey(name) + ".openai.wire_api"
	if gjson.GetBytes(raw, field).String() == want {
		// 内存被清但磁盘未丢（别的写入路径已回写）：只补内存状态。
		if lerr := a.Config.Load(); lerr != nil {
			return spec.Errorf(spec.ErrInternal, "wire_api 已在 models.json，但重载配置失败: %v", lerr)
		}
		return nil
	}
	out, serr := sjson.SetBytes(raw, field, want)
	if serr != nil {
		return spec.Errorf(spec.ErrInternal, "wire_api 保全：改写 models.json 失败: %v", serr)
	}
	tmp := path + ".agentcli-tmp"
	if werr := os.WriteFile(tmp, out, 0o600); werr != nil {
		return spec.Errorf(spec.ErrInternal, "wire_api 保全：写临时文件失败: %v", werr)
	}
	_ = os.Chmod(tmp, 0o600)
	if rerr := os.Rename(tmp, path); rerr != nil {
		_ = os.Remove(tmp)
		return spec.Errorf(spec.ErrInternal, "wire_api 保全：替换 models.json 失败: %v", rerr)
	}
	_ = os.Chmod(path, 0o600)
	if lerr := a.Config.Load(); lerr != nil {
		return spec.Errorf(spec.ErrInternal, "wire_api 已写回 models.json，但重载配置失败: %v", lerr)
	}
	return nil
}

// agentCLIGJSONPathKey 转义 key 中的路径元字符（. 与 \），保证含点号/反斜
// 线的 provider 名也能在 gjson/sjson 路径中精确寻址。
func agentCLIGJSONPathKey(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, `\`, `\\`), `.`, `\.`)
}

func (h *agentCLIHost) handleProviderKeySet(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Secrets == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "secrets 服务未初始化")
	}
	name := strings.TrimSpace(args.String("name"))
	if name == "" {
		return nil, agentCLIArgErrf("缺少必填参数 name")
	}
	key := args.String("key")
	if strings.TrimSpace(key) == "" {
		return nil, agentCLIArgErrf("缺少必填参数 key（建议经 stdin 传入：--key -）")
	}
	if err := a.saveImportedProviderAPIKey(name, key); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	if err := a.Secrets.Save(); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	// 与 SaveProviderFromJSON 同步语义：密钥落库后同步 harness 配置。
	if err := a.syncProvidersToHarnesses(); err != nil {
		return nil, spec.Errorf(spec.ErrInternal, "密钥已保存，但同步 OpenCode/Pi/OMP 提供商配置失败: %s", err.Error())
	}
	return map[string]any{
		"name": name,
		"key":  agentCLIKeyStatusViewFrom(a.Secrets.GetKeyDiagnostics([]string{name})[name]),
	}, nil
}

func (h *agentCLIHost) handleProviderDelete(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Config == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "config 服务未初始化")
	}
	name := strings.TrimSpace(args.String("name"))
	if name == "" {
		return nil, agentCLIArgErrf("缺少必填参数 name")
	}
	if err := a.DeleteProvider(name); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"name": name, "deleted": true}, nil
}

// ---- key status ----

func (h *agentCLIHost) handleKeyStatus(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Secrets == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "secrets 服务未初始化")
	}
	provider := strings.TrimSpace(args.String("provider"))
	seen := map[string]struct{}{}
	var names []string
	if a.Config != nil {
		for _, n := range a.Config.GetProviderNames() {
			if _, dup := seen[n]; !dup {
				seen[n] = struct{}{}
				names = append(names, n)
			}
		}
	}
	for _, n := range a.Secrets.GetAllProviders() {
		if _, dup := seen[n]; !dup {
			seen[n] = struct{}{}
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if provider != "" {
		if _, ok := seen[provider]; !ok {
			return nil, agentCLINotFoundErrf("provider not found: %s", provider)
		}
		names = []string{provider}
	}
	diags := a.Secrets.GetKeyDiagnostics(names)
	views := make([]agentCLIKeyStatusView, 0, len(names))
	for _, n := range names {
		views = append(views, *agentCLIKeyStatusViewFrom(diags[n]))
	}
	return map[string]any{"providers": views, "count": len(views)}, nil
}

// ---- preset handlers ----

type agentCLITerminalPresetView struct {
	Name           string          `json:"name,omitempty"`
	Provider       string          `json:"provider,omitempty"`
	Model          string          `json:"model,omitempty"`
	ModelHaiku     string          `json:"modelHaiku,omitempty"`
	ModelSonnet    string          `json:"modelSonnet,omitempty"`
	ModelOpus      string          `json:"modelOpus,omitempty"`
	Vision         bool            `json:"vision,omitempty"`
	Video          bool            `json:"video,omitempty"`
	VisionPriority int             `json:"visionPriority,omitempty"`
	VisionExport   *bool           `json:"visionExport,omitempty"`
	HarnessSync    bool            `json:"harnessSync,omitempty"`
	OpenCodeCfg    json.RawMessage `json:"opencodeCfg,omitempty"`
	Parameters     json.RawMessage `json:"parameters,omitempty"`
}

func agentCLITerminalPresetViewFrom(tp config.TerminalPreset) agentCLITerminalPresetView {
	view := agentCLITerminalPresetView{
		Name:           tp.Name,
		Provider:       tp.Provider,
		Model:          tp.Model,
		ModelHaiku:     tp.ModelHaiku,
		ModelSonnet:    tp.ModelSonnet,
		ModelOpus:      tp.ModelOpus,
		Vision:         tp.Vision,
		Video:          tp.Video,
		VisionPriority: tp.VisionPriority,
		VisionExport:   tp.VisionExport,
		HarnessSync:    tp.HarnessSync,
		OpenCodeCfg:    tp.OpenCodeCfg,
	}
	if raw, err := json.Marshal(tp.Parameters); err == nil && string(raw) != "{}" {
		view.Parameters = raw
	}
	return view
}

func agentCLITerminalPresetFromView(view agentCLITerminalPresetView) config.TerminalPreset {
	tp := config.TerminalPreset{
		Name:           view.Name,
		Provider:       view.Provider,
		Model:          view.Model,
		ModelHaiku:     view.ModelHaiku,
		ModelSonnet:    view.ModelSonnet,
		ModelOpus:      view.ModelOpus,
		Vision:         view.Vision,
		Video:          view.Video,
		VisionPriority: view.VisionPriority,
		VisionExport:   view.VisionExport,
		HarnessSync:    view.HarnessSync,
		OpenCodeCfg:    view.OpenCodeCfg,
	}
	if len(view.Parameters) > 0 {
		_ = json.Unmarshal(view.Parameters, &tp.Parameters)
	}
	return tp
}

type agentCLIPresetListItem struct {
	Key            string          `json:"key"`
	Label          string          `json:"label"`
	Provider       string          `json:"provider"`
	Model          string          `json:"model,omitempty"`
	ModelHaiku     string          `json:"modelHaiku,omitempty"`
	ModelSonnet    string          `json:"modelSonnet,omitempty"`
	ModelOpus      string          `json:"modelOpus,omitempty"`
	Source         string          `json:"source"`
	Vision         bool            `json:"vision,omitempty"`
	Video          bool            `json:"video,omitempty"`
	VisionPriority int             `json:"visionPriority,omitempty"`
	HarnessSync    bool            `json:"harnessSync,omitempty"`
	Parameters     json.RawMessage `json:"parameters,omitempty"`
}

func (h *agentCLIHost) handlePresetList(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Config == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "config 服务未初始化")
	}
	t := strings.TrimSpace(args.String("type"))
	canonical, ok := config.CanonicalTerminalPresetType(t)
	if t == "" || !ok {
		return nil, agentCLIArgErrf("type 必须是 claude_code/opencode/codex/pi/omp/anthropic/openai 之一")
	}
	merged, err := a.GetMergedTerminalPresets(t)
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	items := make([]agentCLIPresetListItem, 0, len(merged))
	for _, mp := range merged {
		item := agentCLIPresetListItem{
			Key:            mp.Key,
			Label:          mp.Label,
			Provider:       mp.Provider,
			Model:          mp.Model,
			ModelHaiku:     mp.ModelHaiku,
			ModelSonnet:    mp.ModelSonnet,
			ModelOpus:      mp.ModelOpus,
			Source:         mp.Source,
			Vision:         mp.Vision,
			Video:          mp.Video,
			VisionPriority: mp.VisionPriority,
			HarnessSync:    mp.HarnessSync,
		}
		if raw, jerr := json.Marshal(mp.Parameters); jerr == nil && string(raw) != "{}" {
			item.Parameters = raw
		}
		items = append(items, item)
	}
	return map[string]any{"type": string(canonical), "presets": items, "count": len(items)}, nil
}

func (h *agentCLIHost) handlePresetSave(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Config == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "config 服务未初始化")
	}
	t := strings.TrimSpace(args.String("type"))
	canonical, ok := config.CanonicalTerminalPresetType(t)
	if t == "" || !ok {
		return nil, agentCLIArgErrf("type 必须是合法预设桶（见 --help 枚举）")
	}
	name := strings.TrimSpace(args.String("name"))
	if name == "" {
		return nil, agentCLIArgErrf("缺少必填参数 name")
	}
	// 基底：已存在的同名预设（未给出的字段保留原值）。
	view := agentCLITerminalPresetView{Name: name}
	if presets, err := a.GetTerminalPresets(t); err == nil {
		if base, exists := presets[name]; exists {
			view = agentCLITerminalPresetViewFrom(base)
		}
	}
	if raw, err := agentCLIParseJSONArg(args["json"]); err != nil {
		return nil, err
	} else if raw != nil {
		if hit, found := agentCLISecretFieldPath(raw, ""); found {
			return nil, agentCLIArgErrf("json 含密钥字段 %s：预设不支持密钥字段", hit)
		}
		rawJSON, merr := json.Marshal(raw)
		if merr != nil {
			return nil, agentCLIServiceErr(merr)
		}
		if uerr := json.Unmarshal(rawJSON, &view); uerr != nil {
			return nil, agentCLIArgErrf("json 结构不符：%v", uerr)
		}
	}
	// 显式旗标优先于 json。
	if _, exists := args["provider"]; exists {
		view.Provider = args.String("provider")
	}
	if _, exists := args["model"]; exists {
		view.Model = args.String("model")
	}
	if _, exists := args["vision"]; exists {
		view.Vision = args.Bool("vision")
	}
	if _, exists := args["video"]; exists {
		view.Video = args.Bool("video")
	}
	if strings.TrimSpace(view.Provider) == "" {
		return nil, agentCLIArgErrf("preset 需要 provider（经 --provider 或 json.provider 提供）")
	}
	if view.Name == "" {
		view.Name = name
	}
	tp := agentCLITerminalPresetFromView(view)
	if err := a.SaveTerminalPreset(t, name, tp); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{
		"type":   string(canonical),
		"name":   name,
		"preset": agentCLITerminalPresetViewFrom(tp),
	}, nil
}

func (h *agentCLIHost) handlePresetDelete(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Config == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "config 服务未初始化")
	}
	t := strings.TrimSpace(args.String("type"))
	if _, ok := config.CanonicalTerminalPresetType(t); !ok || t == "" {
		return nil, agentCLIArgErrf("type 必须是合法预设桶（见 --help 枚举）")
	}
	name := strings.TrimSpace(args.String("name"))
	if name == "" {
		return nil, agentCLIArgErrf("缺少必填参数 name")
	}
	if err := a.DeleteTerminalPreset(t, name); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"type": t, "name": name, "deleted": true}, nil
}

// ---- settings ----

// agentCLISanitizeSettings 把 AppSettings 序列化视图中的凭据字段置空脱敏。
func agentCLISanitizeSettings(s *settings.AppSettings) (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if token, ok := m["githubToken"].(string); ok {
		m["githubTokenSet"] = strings.TrimSpace(token) != ""
		m["githubToken"] = ""
	}
	return m, nil
}

func (h *agentCLIHost) settingsView() (map[string]any, error) {
	st := h.app.Settings.GetSettings()
	return agentCLISanitizeSettings(st)
}

func (h *agentCLIHost) handleSettingsGet(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.Settings == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "settings 服务未初始化")
	}
	return h.settingsView()
}

// normalizeAgentCLISettingKey 与 cmd/codebox 的 normalizeSettingKey 同规则：
// 小写、按 "." 分段、去掉段内 "-" 与 "_"。
func normalizeAgentCLISettingKey(key string) string {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(key)), ".")
	for i, part := range parts {
		part = strings.ReplaceAll(part, "-", "")
		part = strings.ReplaceAll(part, "_", "")
		parts[i] = part
	}
	return strings.Join(parts, ".")
}

// agentCLISettingKeyForbidden 判定禁改面：remote*（远程控制）、含 cli（agent-cli 自身
// 及 AgentCLI 相关键）、含 token 的键。命中返回 forbidden_command（安全策略拒绝）。
func agentCLISettingKeyForbidden(normalized string) bool {
	return strings.HasPrefix(normalized, "remote") ||
		strings.Contains(normalized, "cli") ||
		strings.Contains(normalized, "token")
}

func (h *agentCLIHost) handleSettingsSet(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Settings == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "settings 服务未初始化")
	}
	key := strings.TrimSpace(args.String("key"))
	value := args.String("value")
	if key == "" {
		return nil, agentCLIArgErrf("缺少必填参数 key")
	}
	if _, hasValue := args["value"]; !hasValue {
		return nil, agentCLIArgErrf("缺少必填参数 value")
	}
	normalized := normalizeAgentCLISettingKey(key)
	if agentCLISettingKeyForbidden(normalized) {
		return nil, spec.Errorf(spec.ErrForbiddenCommand, "设置键 %q 属于禁改面（remote*/cli*/token），CLI 不可修改", key)
	}
	if err := setAgentCLISettingValue(a, normalized, value); err != nil {
		if se, ok := err.(*spec.Error); ok {
			return nil, se
		}
		return nil, agentCLIServiceErr(err)
	}
	view, err := h.settingsView()
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"key": normalized, "value": value, "settings": view}, nil
}

// setAgentCLISettingValue 白名单与 cmd/codebox setSettingValue 对齐（另补 pi/omp
// 面板键），并剔除其 remoteport/githubtoken 项（本 CLI 禁改面）。
func setAgentCLISettingValue(a *App, normalizedKey, value string) error {
	svc := a.Settings
	dashboard := svc.GetDashboardDefaults()
	terminal := svc.GetTerminalSettings()
	switch normalizedKey {
	case "dashboard.provider":
		dashboard.Provider = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.preset":
		dashboard.Preset = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.opencodeprovider":
		dashboard.OpenCodeProvider = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.mode":
		dashboard.Mode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.shell":
		dashboard.Shell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.claudemode":
		dashboard.ClaudeMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.claudeshell":
		dashboard.ClaudeShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.opencodemode":
		dashboard.OpenCodeMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.opencodeshell":
		dashboard.OpenCodeShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.codexmode":
		dashboard.CodexMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.codexshell":
		dashboard.CodexShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.pimode":
		dashboard.PiMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.pishell":
		dashboard.PiShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.ompmode":
		dashboard.OmpMode = value
		return svc.SetDashboardDefaults(dashboard)
	case "dashboard.ompshell":
		dashboard.OmpShell = value
		return svc.SetDashboardDefaults(dashboard)
	case "terminal.scrollback":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("terminal.scrollback 需要整数: %w", err)
		}
		terminal.Scrollback = parsed
		return svc.SetTerminalSettings(terminal)
	case "mobilewebroot":
		return svc.SetMobileWebRoot(value)
	default:
		return agentCLIArgErrf("不支持的设置键 %q（白名单见 --help）", normalizedKey)
	}
}

// ---- envvars ----

// agentCLIEnvVarView 是 envvars 的脱敏视图：敏感名（含 KEY/TOKEN/SECRET/
// PASSWORD/PASSWD/CREDENTIAL/AUTH）的值一律掩码并附 valueSet 布尔；
// 非敏感名原样回显。
type agentCLIEnvVarView struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	ValueSet       bool   `json:"valueSet,omitempty"` // 敏感键：值非空时 true
	AllowPathValue bool   `json:"allowPathValue,omitempty"`
}

func agentCLIEnvVarViewFrom(v envvars.EnvVar) agentCLIEnvVarView {
	view := agentCLIEnvVarView{Key: v.Key, AllowPathValue: v.AllowPathValue}
	if agentCLIIsSensitiveEnvVarName(v.Key) {
		if strings.TrimSpace(v.Value) == "" {
			return view // 未设置：不显示掩码，valueSet 省略
		}
		view.Value = agentCLIHeaderMask
		view.ValueSet = true
		return view
	}
	view.Value = v.Value
	return view
}

func (h *agentCLIHost) handleEnvVarsList(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.EnvVars == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "envvars 服务未初始化")
	}
	vars, err := h.app.GetEnvVars()
	if err != nil {
		return nil, agentCLIServiceErr(err)
	}
	views := make([]agentCLIEnvVarView, 0, len(vars))
	for _, v := range vars {
		views = append(views, agentCLIEnvVarViewFrom(v))
	}
	return map[string]any{"vars": views, "count": len(views)}, nil
}

func (h *agentCLIHost) handleEnvVarsSet(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.EnvVars == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "envvars 服务未初始化")
	}
	key := strings.TrimSpace(args.String("key"))
	if key == "" {
		return nil, agentCLIArgErrf("缺少必填参数 key")
	}
	value := args.String("value")
	if err := h.app.SetEnvVar(key, value); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	// 回显脱敏：敏感键绝不回显明文，仅掩码 + 是否已设置；底层存储保留真实值。
	if agentCLIIsSensitiveEnvVarName(key) {
		return map[string]any{"key": key, "value": agentCLIHeaderMask, "valueSet": strings.TrimSpace(value) != ""}, nil
	}
	return map[string]any{"key": key, "value": value}, nil
}

func (h *agentCLIHost) handleEnvVarsDelete(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.EnvVars == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "envvars 服务未初始化")
	}
	key := strings.TrimSpace(args.String("key"))
	if key == "" {
		return nil, agentCLIArgErrf("缺少必填参数 key")
	}
	if err := h.app.DeleteEnvVar(key); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return map[string]any{"key": key, "deleted": true}, nil
}

// ---- workdir ----

func (h *agentCLIHost) workdirView() any {
	dirs, err := h.app.GetSavedWorkDirs()
	if err != nil {
		return map[string]any{"workdirs": []settings.WorkDirEntry{}, "count": 0}
	}
	if dirs == nil {
		dirs = []settings.WorkDirEntry{}
	}
	return map[string]any{"workdirs": dirs, "count": len(dirs)}
}

func (h *agentCLIHost) handleWorkdirList(ctx context.Context, _ spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	if h.app == nil || h.app.Settings == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "settings 服务未初始化")
	}
	return h.workdirView(), nil
}

func (h *agentCLIHost) handleWorkdirAdd(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Settings == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "settings 服务未初始化")
	}
	path := strings.TrimSpace(args.String("path"))
	if path == "" {
		return nil, agentCLIArgErrf("缺少必填参数 path")
	}
	if _, err := a.AddSavedWorkDir(path, args.String("label")); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return h.workdirView(), nil
}

func (h *agentCLIHost) handleWorkdirRemove(ctx context.Context, args spec.Args) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, agentCLICtxErr(err)
	}
	a := h.app
	if a == nil || a.Settings == nil {
		return nil, spec.Errorf(spec.ErrNotReady, "settings 服务未初始化")
	}
	path := strings.TrimSpace(args.String("path"))
	if path == "" {
		return nil, agentCLIArgErrf("缺少必填参数 path")
	}
	if _, err := a.RemoveSavedWorkDir(path); err != nil {
		return nil, agentCLIServiceErr(err)
	}
	return h.workdirView(), nil
}

func (h *agentCLIHost) configHandlers() map[string]spec.Handler {
	return map[string]spec.Handler{
		"provider.list":    h.handleProviderList,
		"provider.get":     h.handleProviderGet,
		"provider.save":    h.handleProviderSave,
		"provider.key-set": h.handleProviderKeySet,
		"provider.delete":  h.handleProviderDelete,
		"key.status":       h.handleKeyStatus,
		"preset.list":      h.handlePresetList,
		"preset.save":      h.handlePresetSave,
		"preset.delete":    h.handlePresetDelete,
		"settings.get":     h.handleSettingsGet,
		"settings.set":     h.handleSettingsSet,
		"envvars.list":     h.handleEnvVarsList,
		"envvars.set":      h.handleEnvVarsSet,
		"envvars.delete":   h.handleEnvVarsDelete,
		"workdir.list":     h.handleWorkdirList,
		"workdir.add":      h.handleWorkdirAdd,
		"workdir.remove":   h.handleWorkdirRemove,
	}
}
