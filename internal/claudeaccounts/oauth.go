package claudeaccounts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pi OAuth 刷新协议常量（design §5.1 已核实；与 pi-ai 安装源码
// dist/auth/oauth/anthropic.js 的 CLIENT_ID/TOKEN_URL/refresh 常量一致）。
const (
	PiTokenURL            = "https://platform.claude.com/v1/oauth/token"
	PiOAuthClientID        = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	PiRefreshAdapterVersion = "pi-oauth-refresh-v1"
	// piRefreshExpirySafety 与 Pi 一致：expires = now + expires_in×1000 − 5min。
	piRefreshExpirySafety = 5 * time.Minute
)

// GrantRefreshResult 是一次成功刷新得到的 token 对。
type GrantRefreshResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// tokenResponse 是刷新响应的最小校验视图（严格有限数值校验）。
type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    float64 `json:"expires_in"`
	Error        string  `json:"error"`
}

// postTokenRequest 发送刷新 POST 并返回解析结果。错误分类（design §5.1）：
// invalid_grant → NEEDS_LOGIN；其余任何失败（网络/超时/取消/其他状态码/
// 响应不可解析）→ REFRESH_UNCERTAIN——请求可能已被服务器处理并轮换了
// refresh token，不可当作“凭据未动”。错误消息不含 token 与响应原文。
func postTokenRequest(ctx context.Context, client *http.Client, tokenURL string, body map[string]string) (tokenResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新请求构造失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(payload))
	if err != nil {
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新请求构造失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新请求未获响应（结果不确定）")
	}
	respBody, err := readBoundedBody(resp)
	if err != nil {
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新响应不可读（结果不确定）")
	}
	var parsed tokenResponse
	parseErr := json.Unmarshal(respBody, &parsed)
	if resp.StatusCode != http.StatusOK {
		if parseErr == nil && parsed.Error == "invalid_grant" {
			return tokenResponse{}, NewErr(ReasonNeedsLogin, "refresh token 已失效（invalid_grant）")
		}
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新请求被拒（HTTP %d，结果不确定）", resp.StatusCode)
	}
	if parseErr != nil {
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新响应不可解析（结果不确定）")
	}
	if parsed.AccessToken == "" || parsed.RefreshToken == "" {
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新响应缺少 token 字段（结果不确定）")
	}
	if parsed.ExpiresIn <= 0 || parsed.ExpiresIn > 1e7 {
		return tokenResponse{}, NewErr(ReasonRefreshUncertain, "刷新响应 expires_in 非有限正数（结果不确定）")
	}
	return parsed, nil
}

// PiRefreshAdapter 是 Pi anthropic grant 的刷新适配器（协议常量已核实）。
type PiRefreshAdapter struct {
	client *http.Client
}

// NewPiRefreshAdapter 构造 Pi 刷新适配器。
func NewPiRefreshAdapter(client *http.Client) *PiRefreshAdapter {
	return &PiRefreshAdapter{client: client}
}

// AdapterVersion 返回适配器版本标识。
func (a *PiRefreshAdapter) AdapterVersion() string { return PiRefreshAdapterVersion }

// Refresh 执行 grant_type=refresh_token 刷新并返回新 anthropic 条目 JSON
// （type=oauth、access、refresh、expires 毫秒 epoch，与 Pi 存储一致）。
func (a *PiRefreshAdapter) Refresh(ctx context.Context, refreshToken string) (GrantRefreshResult, error) {
	result, err := postTokenRequest(ctx, a.client, PiTokenURL, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     PiOAuthClientID,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return GrantRefreshResult{}, err
	}
	expires := time.Now().Add(time.Duration(result.ExpiresIn*1000)*time.Millisecond - piRefreshExpirySafety)
	return GrantRefreshResult{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, ExpiresAt: expires}, nil
}

// BuildPiAnthropicEntry 用刷新结果构造 anthropic 条目 JSON（expires 为
// 毫秒 epoch，设计 §5.1）。
func BuildPiAnthropicEntry(result GrantRefreshResult) (json.RawMessage, error) {
	entry := PiAnthropicEntry{
		Type:    "oauth",
		Access:  result.AccessToken,
		Refresh: result.RefreshToken,
		Expires: float64(result.ExpiresAt.UnixMilli()),
	}
	if err := ValidatePiAnthropicEntry(mustMarshalEntry(entry)); err != nil {
		return nil, err
	}
	return mustMarshalEntry(entry), nil
}

func mustMarshalEntry(entry PiAnthropicEntry) json.RawMessage {
	data, _ := json.Marshal(entry)
	return data
}

// CCRefreshProtocol 描述 Claude Code 刷新协议契约。设计 §5.1：CC 对应
// client_id、scope 和 expiresAt 单位【待实测】，与 Pi 相同也要有独立证据。
// Verified=false 时适配器拒绝刷新——不猜常量（design §5.1）；ExpiresUnit
// 为 "seconds"/"milliseconds"，真机验证后由接线层注入。
type CCRefreshProtocol struct {
	Verified    bool
	TokenURL    string
	ClientID    string
	ExpiresUnit string
}

// CCRefreshAdapter 是 Claude Code grant 的刷新适配器：结构完整、协议常量
// 留 adapterVersion 门。真机验证（design §10.2 步骤 4）通过后由接线层
// 注入 Verified 协议常量。
type CCRefreshAdapter struct {
	Protocol CCRefreshProtocol
	client   *http.Client
}

// CCRefreshAdapterVersion 返回适配器版本；未验证协议以 “unverified” 标记。
func (a *CCRefreshAdapter) AdapterVersion() string {
	if a.Protocol.Verified {
		return "cc-oauth-refresh-v1"
	}
	return "cc-oauth-refresh-unverified"
}

// NewCCRefreshAdapter 构造 CC 刷新适配器。
func NewCCRefreshAdapter(client *http.Client, protocol CCRefreshProtocol) *CCRefreshAdapter {
	return &CCRefreshAdapter{client: client, Protocol: protocol}
}

// Refresh 在协议未验证时返回需验证状态（UNSUPPORTED_CREDENTIAL_SCHEMA），
// 不执行网络请求；协议验证后行为与 Pi 适配器同构。
func (a *CCRefreshAdapter) Refresh(ctx context.Context, refreshToken string) (GrantRefreshResult, error) {
	if !a.Protocol.Verified || a.Protocol.TokenURL == "" || a.Protocol.ClientID == "" {
		return GrantRefreshResult{}, NewErr(ReasonUnsupportedCredentialSchema, "Claude Code 刷新协议未验证（adapterVersion 门），拒绝猜测常量")
	}
	switch a.Protocol.ExpiresUnit {
	case "seconds", "milliseconds":
	default:
		// expiresAt 单位【待实测】：未注入明确单位前不推导过期时间。
		return GrantRefreshResult{}, NewErr(ReasonUnsupportedCredentialSchema, "Claude Code 刷新协议 expiresAt 单位未验证，拒绝猜测")
	}
	result, err := postTokenRequest(ctx, a.client, a.Protocol.TokenURL, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     a.Protocol.ClientID,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return GrantRefreshResult{}, err
	}
	// CC 侧 expiresAt 单位待真机验证；单位注入前不换算为时间。
	return GrantRefreshResult{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken}, nil
}
