// legacyconfig.go legacy 配置面客户端（WD-5 过渡方案，任务 RC4-1）：
//
// v1 契约（manifest）不含 providers/settings 端点；宿主现存的配置管理面只有
// legacy REST（README 远程控制节）：GET/PUT /api/providers、/api/providers/{name}、
// GET/PUT /api/settings，Bearer Token 鉴权（与 v1 同端口 8680）。本文件实现
// 该面的消费端：按 WD-5，客户端 per-host 可选保存 legacy token（Keychain 条目
// codebox-remoteclient/<DeviceID>/legacy，登记簿记 hasLegacyToken 投影标记），
// 配置管理仅在该 token 存在时可用。过渡语义：legacy 面最终会被 v1 契约的
// 配置端点取代，届时本文件整体退役。
//
// 安全不变量（蓝图 §9 同源要求）：
//   - token 只进 Keychain 与内存，不落盘、不进日志、不进前端事件；
//   - 响应净化层（验收 5）：providers/settings 响应中任何疑似密钥字段（按
//     internal/config 实际字段名 + 通用模式识别）在离开本包前替换为掩码
//     占位 RemoteManagedMask，明文密钥永不抵达前端；
//   - 上行拦截：PUT 请求体携带明文密钥字段时本地拒绝（bad_request），绝
//     不出网；掩码占位按字段语义转换（api_key → 空串=保留宿主现值）或拒绝。
//
// 错误契约：legacy 服务端不回 v1 错误体（401/403 为 {"error":...}），本文件
// 把它们归入既有 12 码体系：401/403 → auth.unpaired 形态（提示需重填 token）；
// 若宿主未来升级为回契约错误体则原样透传（serverAPIError）。
package remoteclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// 凭据字段识别与净化层（验收 5）
// ---------------------------------------------------------------------------

// RemoteManagedMask 是净化层对疑似密钥字段的统一掩码占位。前端以该值识别
// 「宿主管 fields」并按过渡语义禁编辑（UI 明示）。
const RemoteManagedMask = "«remote-managed»"

// normalizeFieldName 折叠字段名供模式匹配：小写、去 _/-/空格（api_key、
// apiKey、X-Api-Key 归一为同一形态）。
func normalizeFieldName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToLower(name) {
		if r == '_' || r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// credentialFieldMarkers 是子串模式：命中即视为疑似密钥字段。依据
// internal/config/types.go 的实际凭据字段（api_key / auth_key，顶层与
// anthropic/openai 嵌套、headers 内的 authorization / x-api-key）加通用
// secret/password/credential 族。注意不含裸 "token" 子串——预设参数里的
// max_tokens（types.go Parameters.MaxTokens）会误伤。
var credentialFieldMarkers = []string{
	"apikey",
	"authkey",
	"secret",
	"password",
	"passwd",
	"authorization",
	"credential",
}

// exactCredentialFields 是归一化后全等的 token 族字段（settings 的
// remoteToken 在此覆盖；max_tokens 不在其中，不受影响）。
var exactCredentialFields = map[string]bool{
	"token":        true,
	"remotetoken":  true,
	"accesstoken":  true,
	"refreshtoken": true,
	"idtoken":      true,
	"devicetoken":  true,
	"apitoken":     true,
	"authtoken":    true,
	"bearertoken":  true,
}

// knownNonCredentialFields 是已知会被模式误伤的正常配置字段（归一化形态）：
// 预设参数 max_tokens 是模型上限而非凭据。
var knownNonCredentialFields = map[string]bool{
	"maxtokens": true,
}

// looksLikeCredentialField 报告字段名是否疑似密钥承载字段。
func looksLikeCredentialField(name string) bool {
	n := normalizeFieldName(name)
	if n == "" || knownNonCredentialFields[n] {
		return false
	}
	if exactCredentialFields[n] {
		return true
	}
	for _, m := range credentialFieldMarkers {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}

// isUnifiedKeyField 报告字段是否为宿主有「空值=保留现值」语义的统一密钥
// 字段（internal/config ExportProvider：顶层 api_key 与 anthropic/openai
// 嵌套 api_key，UnifiedAPIKey 空串时服务端不动 secrets）。这些字段上的
// 掩码占位回传时转换为空串（= 不改宿主密钥）；其余密钥字段（auth_key 等
// 全量替换语义）回传占位会被拒绝，避免静默清空宿主配置。
func isUnifiedKeyField(name string) bool {
	return normalizeFieldName(name) == "apikey"
}

// maskRemoteSecrets 就地把 v（已解码的 JSON any 形态）中疑似密钥字段的
// 非空字符串值替换为 RemoteManagedMask。返回是否发生替换（供诊断）。
func maskRemoteSecrets(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		for k, val := range t {
			if s, ok := val.(string); ok && looksLikeCredentialField(k) && s != "" {
				t[k] = RemoteManagedMask
				changed = true
				continue
			}
			if maskRemoteSecrets(val) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, item := range t {
			if maskRemoteSecrets(item) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// redactRemoteUpload 净化上行 PUT 体：疑似密钥字段只允许三种取值——
// 空串（显式「不改」）、掩码占位（统一密钥字段转空串；其余字段记为
// 拒绝项）、非字符串（结构性值按原样继续走，但其内层字段仍递归检查）。
// 任何明文取值都记入 offenders（只记字段路径，绝不记值）。
func redactRemoteUpload(v any, path string, offenders *[]string) {
	join := func(path, key string) string {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && looksLikeCredentialField(k) {
				switch {
				case s == "":
					// 空串：合法（api_key 语义=保留现值）。
				case s == RemoteManagedMask:
					if isUnifiedKeyField(k) {
						t[k] = "" // 占位 → 不改宿主密钥
					} else {
						*offenders = append(*offenders, join(path, k))
					}
				default:
					*offenders = append(*offenders, join(path, k))
				}
				continue
			}
			redactRemoteUpload(val, join(path, k), offenders)
		}
	case []any:
		for _, item := range t {
			redactRemoteUpload(item, path, offenders)
		}
	}
}

// sanitizeDownloadJSON 解码成功响应体、掩码密钥字段并重新编出 JSON 字符串。
// 非 JSON 成功体如实映射 service.down（不伪装成功）。
func sanitizeDownloadJSON(raw []byte) (string, *ClientError) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", serviceDownError(http.StatusOK, fmt.Errorf("non-JSON legacy response: %w", err))
	}
	maskRemoteSecrets(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return "", serviceDownError(http.StatusOK, err)
	}
	return string(out), nil
}

// sanitizeUploadJSON 校验并净化上行 JSON：返回可出网的 body 与错误。明文/
// 非法占位密钥字段 → bad_request（本地拒绝，不出网），错误文案只含字段
// 路径。非 JSON 体 → bad_request。
func sanitizeUploadJSON(raw []byte) ([]byte, *ClientError) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "invalid JSON body for legacy upload", err)
	}
	var offenders []string
	redactRemoteUpload(doc, "", &offenders)
	if len(offenders) > 0 {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry,
			fmt.Sprintf("legacy upload rejected: credential field(s) must not carry plaintext or mask values (edit them on the host): %s",
				strings.Join(offenders, ", ")), nil)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "invalid JSON body for legacy upload", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// legacy token 存取（Keychain 条目，WD-5 冻结格式）
// ---------------------------------------------------------------------------

// legacyTokenEntryName 构造 legacy token 的 Keychain 条目名（WD-5 冻结格式
// codebox-remoteclient/<DeviceID>/legacy）。token 本体只进 OS 凭据库。
func legacyTokenEntryName(deviceID string) string {
	return credentialEntryName(deviceID) + "/legacy"
}

// LegacyTokenEntryName 是 legacyTokenEntryName 的导出形态（App 转发层清理
// 路径按同一冻结格式构造条目名；格式仍只有一处定义）。
func LegacyTokenEntryName(deviceID string) string {
	return legacyTokenEntryName(deviceID)
}

// ---------------------------------------------------------------------------
// 错误映射（legacy 形态 → 12 码体系）
// ---------------------------------------------------------------------------

const (
	msgLegacyTokenRejected = "host rejected the legacy config request (token invalid or host restricts remote config access); re-enter the remote-control token"
	msgLegacyTokenMissing  = "legacy token not configured for this host; set it before using remote config management"
)

// legacyAuthError 是 legacy 401/403 的自产映射：auth.unpaired 形态 + 提示
// 需重填 token（WD-5）。不含宿主地址与凭据材料。
func legacyAuthError(status int, cause error) *ClientError {
	return localAPIError(status, contract.ErrorCodeAuthUnpaired, contract.ErrorLayerAuth,
		contract.ActionHintRePair, msgLegacyTokenRejected, cause)
}

// classifyLegacyFailure 归类「有 HTTP 响应但非成功」：契约错误体优先透传
// （宿主未来升级到 v1 错误体时无缝）；401/403 → auth.unpaired（legacy token
// 语义）；其余 → net.unreachable 兜底。镜像 classifyFailure 的优先级结构。
func classifyLegacyFailure(status int, body []byte) *ClientError {
	var api contract.APIError
	if err := json.Unmarshal(body, &api); err == nil && api.Code != "" && api.Message != "" {
		return serverAPIError(status, &api)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return legacyAuthError(status, nil)
	}
	return localAPIError(status, contract.ErrorCodeNetUnreachable, contract.ErrorLayerConnection,
		contract.ActionHintRetry, msgStatusFallback, nil)
}

// ---------------------------------------------------------------------------
// LegacyConfigClient — Bearer Token REST 客户端
// ---------------------------------------------------------------------------

// LegacyConfigClient 是宿主 legacy 配置面的最小 REST 客户端：一次性持有
// hostPort + token（token 仅内存，调用完即弃），不复用 v1 Transport（legacy
// 路径前缀 /api 与 v1 的 /api/v1 不同，且鉴权载体是 Bearer 头而非设备
// Cookie）。HTTP 加固策略与 Transport 一致（禁跟随重定向、短超时）。
type LegacyConfigClient struct {
	baseURL string // http://host:port
	token   string // 仅内存（调用期间持有）
	http    *http.Client
}

// newLegacyConfigClient 按 hostPort + token 构建 legacy 客户端。hostPort 走
// 与登记簿一致的白名单校验；token 必须非空。
func newLegacyConfigClient(hostPort, token string) (*LegacyConfigClient, error) {
	hp, err := ValidateHostPort(hostPort)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("legacy token must be non-empty")
	}
	return &LegacyConfigClient{
		baseURL: "http://" + hp,
		token:   token,
		http:    newHardenedHTTPClient(),
	}, nil
}

// do 执行一次 legacy REST 请求：注入 Authorization: Bearer <token>、
// X-Request-ID 与 Content-Type；成功（2xx）返回原始响应体；失败统一走
// classifyLegacyFailure。token 绝不出现在错误文本。
func (c *LegacyConfigClient) do(ctx context.Context, method, path string, body []byte) ([]byte, *ClientError) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, netUnreachableError(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set(contract.RequestIDHeader, string(newRequestID()))
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, netUnreachableError(err)
	}
	defer drainAndClose(resp.Body) //nolint:errcheck // 尽力排空复连
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, successBodyCap))
		if rerr != nil {
			return nil, serviceDownError(resp.StatusCode, rerr)
		}
		return raw, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyCap))
	return nil, classifyLegacyFailure(resp.StatusCode, raw)
}

// LegacyProviderSummary 镜像宿主 GET /api/providers 的摘要项
// （internal/remote handlers.go providerSummary：id/name/type/baseURL/model，
// 固定字段不含凭据；多余字段在类型解码时即被丢弃）。
type LegacyProviderSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
}

// legacyProviderPath 构造 /api/providers/{name}（单段转义）。
func legacyProviderPath(name string) string {
	return "/api/providers/" + url.PathEscape(name)
}

// ListProviders 获取宿主提供商摘要列表（空列表为 [] 非 null）。
func (c *LegacyConfigClient) ListProviders(ctx context.Context) ([]LegacyProviderSummary, *ClientError) {
	raw, cerr := c.do(ctx, http.MethodGet, "/api/providers", nil)
	if cerr != nil {
		return nil, cerr
	}
	out := []LegacyProviderSummary{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, serviceDownError(http.StatusOK, fmt.Errorf("non-JSON legacy providers response: %w", err))
	}
	return out, nil
}

// GetProvider 获取宿主单个提供商的导出 JSON（含密钥字段），出网前经净化层
// 掩码，返回净化后的 JSON 字符串。宿主 404（未知提供商）会以
// {"error":...} 非 2xx 返回，经 classifyLegacyFailure 归类（非 401/403 →
// net.unreachable 兜底形态）。
func (c *LegacyConfigClient) GetProvider(ctx context.Context, name string) (string, *ClientError) {
	raw, cerr := c.do(ctx, http.MethodGet, legacyProviderPath(name), nil)
	if cerr != nil {
		return "", cerr
	}
	return sanitizeDownloadJSON(raw)
}

// PutProvider 上行保存提供商导出 JSON：先经 sanitizeUploadJSON（明文密钥
// 字段本地拒绝不出网；api_key 掩码占位转空串=保留宿主现值），再 PUT。
func (c *LegacyConfigClient) PutProvider(ctx context.Context, name, providerJSON string) *ClientError {
	body, cerr := sanitizeUploadJSON([]byte(providerJSON))
	if cerr != nil {
		return cerr
	}
	_, cerr = c.do(ctx, http.MethodPut, legacyProviderPath(name), body)
	return cerr
}

// GetSettings 获取宿主设置 JSON（legacy 面会回 remoteToken），出网前经
// 净化层掩码，返回净化后的 JSON 字符串。
func (c *LegacyConfigClient) GetSettings(ctx context.Context) (string, *ClientError) {
	raw, cerr := c.do(ctx, http.MethodGet, "/api/settings", nil)
	if cerr != nil {
		return "", cerr
	}
	return sanitizeDownloadJSON(raw)
}

// PutSettings 上行保存设置 JSON：先经 sanitizeUploadJSON（remoteToken 等
// 密钥字段明文本地拒绝），再 PUT。
func (c *LegacyConfigClient) PutSettings(ctx context.Context, settingsJSON string) *ClientError {
	body, cerr := sanitizeUploadJSON([]byte(settingsJSON))
	if cerr != nil {
		return cerr
	}
	_, cerr = c.do(ctx, http.MethodPut, "/api/settings", body)
	return cerr
}

// ---------------------------------------------------------------------------
// LegacyConfigService — 登记簿 + Keychain 编排（App 转发层消费）
// ---------------------------------------------------------------------------

// LegacyConfigService 把 legacy 配置面按 WD-5 编排到既有登记簿/凭据存储：
// token 经 CredentialStore（生产为 DPAPI/Keychain 保护的 secrets 存储）存取，
// hasLegacyToken 投影标记随 Keychain 写入/删除/失效同步到登记簿。
type LegacyConfigService struct {
	registry *HostRegistry
	creds    CredentialStore
}

// NewLegacyConfigService 构建服务（registry/creds 均为装配期注入的可用实例）。
func NewLegacyConfigService(registry *HostRegistry, creds CredentialStore) (*LegacyConfigService, error) {
	if registry == nil || creds == nil {
		return nil, errors.New("legacy config service requires non-nil registry and credential store")
	}
	return &LegacyConfigService{registry: registry, creds: creds}, nil
}

// SetLegacyToken 保存（覆盖）指定主机的 legacy token：要求主机已配对
// （条目按 DeviceID 键控）。token 只进 Keychain；登记簿只落 hasLegacyToken
// 布尔投影。
func (s *LegacyConfigService) SetLegacyToken(hostID, token string) error {
	tok := strings.TrimSpace(token)
	if tok == "" {
		return errors.New("legacy token must be non-empty")
	}
	entry, ok := s.registry.Get(hostID)
	if !ok {
		return fmt.Errorf("host %q not found", hostID)
	}
	if entry.DeviceID == "" {
		return fmt.Errorf("host %q is not paired; complete pairing before setting a legacy token", hostID)
	}
	if err := s.creds.Put(legacyTokenEntryName(entry.DeviceID), tok); err != nil {
		return fmt.Errorf("store legacy token: %w", err)
	}
	if err := s.registry.SetLegacyTokenFlag(hostID, true); err != nil {
		return fmt.Errorf("mark hasLegacyToken: %w", err)
	}
	return nil
}

// ClearLegacyToken 删除指定主机的 legacy token（幂等：条目不存在视为成功）
// 并清除登记簿投影标记。
func (s *LegacyConfigService) ClearLegacyToken(hostID string) error {
	entry, ok := s.registry.Get(hostID)
	if !ok {
		return fmt.Errorf("host %q not found", hostID)
	}
	if entry.DeviceID != "" {
		if err := s.creds.Delete(legacyTokenEntryName(entry.DeviceID)); err != nil {
			return fmt.Errorf("delete legacy token: %w", err)
		}
	}
	return s.registry.SetLegacyTokenFlag(hostID, false)
}

// clientForHost 解析主机并从 Keychain 取 token 构建客户端。token 缺失但
// 投影标记为真时自愈（清标记），返回 auth.unpaired 形态的明确错误
// （WD-5：配置管理仅在该 token 存在时可用）。
func (s *LegacyConfigService) clientForHost(hostID string) (*LegacyConfigClient, *ClientError) {
	entry, ok := s.registry.Get(hostID)
	if !ok {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, fmt.Sprintf("host %q not found", hostID), nil)
	}
	if entry.DeviceID == "" {
		return nil, localAPIError(0, contract.ErrorCodeAuthUnpaired, contract.ErrorLayerAuth,
			contract.ActionHintRePair, "host is not paired; complete pairing before using remote config management", nil)
	}
	token, err := s.creds.Get(legacyTokenEntryName(entry.DeviceID))
	if err != nil {
		return nil, localAPIError(0, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection,
			contract.ActionHintCheckDesktop, "credential store unavailable", err)
	}
	if token == "" {
		if entry.HasLegacyToken {
			// 标记与 Keychain 脱钩（条目被外部清空等）：自愈并如实报缺。
			_ = s.registry.SetLegacyTokenFlag(hostID, false)
		}
		return nil, localAPIError(0, contract.ErrorCodeAuthUnpaired, contract.ErrorLayerAuth,
			contract.ActionHintRePair, msgLegacyTokenMissing, nil)
	}
	client, nerr := newLegacyConfigClient(entry.HostPort, token)
	if nerr != nil {
		return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "invalid host entry for legacy config", nerr)
	}
	return client, nil
}

// ListProviders 列出指定宿主的提供商摘要（WD-5：须已配置 legacy token）。
func (s *LegacyConfigService) ListProviders(ctx context.Context, hostID string) ([]LegacyProviderSummary, *ClientError) {
	c, cerr := s.clientForHost(hostID)
	if cerr != nil {
		return nil, cerr
	}
	return c.ListProviders(ctx)
}

// GetProvider 获取指定宿主单个提供商导出 JSON（净化后；含掩码占位）。
func (s *LegacyConfigService) GetProvider(ctx context.Context, hostID, name string) (string, *ClientError) {
	c, cerr := s.clientForHost(hostID)
	if cerr != nil {
		return "", cerr
	}
	n := strings.TrimSpace(name)
	if n == "" {
		return "", localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "provider name is required", nil)
	}
	return c.GetProvider(ctx, n)
}

// PutProvider 保存指定宿主的提供商导出 JSON（上行净化：明文密钥拒绝出网）。
func (s *LegacyConfigService) PutProvider(ctx context.Context, hostID, name, providerJSON string) *ClientError {
	c, cerr := s.clientForHost(hostID)
	if cerr != nil {
		return cerr
	}
	n := strings.TrimSpace(name)
	if n == "" {
		return localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
			contract.ActionHintRetry, "provider name is required", nil)
	}
	return c.PutProvider(ctx, n, providerJSON)
}

// GetSettings 获取指定宿主设置 JSON（净化后；remoteToken 为掩码占位）。
func (s *LegacyConfigService) GetSettings(ctx context.Context, hostID string) (string, *ClientError) {
	c, cerr := s.clientForHost(hostID)
	if cerr != nil {
		return "", cerr
	}
	return c.GetSettings(ctx)
}

// PutSettings 保存指定宿主设置 JSON（上行净化：remoteToken 等密钥字段拒绝出网）。
func (s *LegacyConfigService) PutSettings(ctx context.Context, hostID, settingsJSON string) *ClientError {
	c, cerr := s.clientForHost(hostID)
	if cerr != nil {
		return cerr
	}
	return c.PutSettings(ctx, settingsJSON)
}
