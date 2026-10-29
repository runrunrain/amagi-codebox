package claudeaccounts

import (
	"context"
	"encoding/json"
	"net/http"
)

// 身份校验协议（design §4.2）。
//
// 【待实测】profile 端点、beta 头、响应字段、两个 grant 的 scope/组织可见
// 性尚未在本任务中验证（design §4.2 明确标注）；解析失败一律返回
// IDENTITY_UNVERIFIED，不得用用户勾选“同一账号”代替证明。真机验证通过
// 前不得开放生产 capture。
const (
	anthropicProfileURL    = "https://api.anthropic.com/api/oauth/profile"
	anthropicOAuthBeta     = "oauth-2025-04-20"
	IdentityAdapterVersion = "anthropic-oauth-profile-v1"
)

// ProfileIdentity 是 profile 查询的最小身份视图：账号 UUID、组织 UUID 与
// 套餐提示（design §4.2：只解析这三项）。
type ProfileIdentity struct {
	AccountUUID      string
	OrganizationUUID string
	SeatTier         string
}

// IdentityProvider 用一份 grant 的 access token 查询其账号身份。
// 测试注入 fake；生产为 HTTP 实现。
type IdentityProvider interface {
	FetchProfile(ctx context.Context, accessToken string) (ProfileIdentity, error)
}

// HTTPIdentityProvider 是 GET /api/oauth/profile 的 HTTP 实现。
type HTTPIdentityProvider struct {
	client *http.Client
}

// NewHTTPIdentityProvider 构造身份适配器；client 必须来自
// NewConstrainedHTTPClient（10s 超时、64KiB 上限、拒绝 redirect）。
func NewHTTPIdentityProvider(client *http.Client) *HTTPIdentityProvider {
	return &HTTPIdentityProvider{client: client}
}

// profileResponse 是响应解析契约。【待实测】字段名以真机验证为准；解析
// 失败/缺失返回 IDENTITY_UNVERIFIED，不猜测字段。
type profileResponse struct {
	AccountUUID      string `json:"accountUuid"`
	OrganizationUUID string `json:"organizationUuid"`
	SeatTier         string `json:"seatTier"`
}

// FetchProfile 执行身份查询。任何失败（网络、状态码、解析、空 UUID）都
// 归为 IDENTITY_UNVERIFIED；错误消息不含 token 与响应原文。
func (p *HTTPIdentityProvider) FetchProfile(ctx context.Context, accessToken string) (ProfileIdentity, error) {
	if accessToken == "" {
		return ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "access token 为空")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicProfileURL, nil)
	if err != nil {
		return ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "身份请求构造失败")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", anthropicOAuthBeta)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "身份查询失败（网络）")
	}
	body, err := readBoundedBody(resp)
	if err != nil {
		return ProfileIdentity{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "身份查询失败（HTTP %d）", resp.StatusCode)
	}
	var parsed profileResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "身份响应不可解析")
	}
	if parsed.AccountUUID == "" {
		return ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "身份响应缺少账号 UUID")
	}
	return ProfileIdentity{
		AccountUUID:      parsed.AccountUUID,
		OrganizationUUID: parsed.OrganizationUUID,
		SeatTier:         parsed.SeatTier,
	}, nil
}

// VerifyPairedIdentities 做双 grant 身份配对校验（design §4.2）：两个独立
// grant 分别查询身份后互比，并与 `.claude.json` 的 accountUuid 比较；
// 邮箱只展示，不作主键。expectedAccountUUID 为空时跳过与本地投影的比较
// （capture 场景本地投影本身就是被采集对象，见 service.capture）。
func VerifyPairedIdentities(cc, pi ProfileIdentity, expectedAccountUUID string) error {
	if cc.AccountUUID == "" || pi.AccountUUID == "" {
		return NewErr(ReasonIdentityUnverified, "双端身份存在未验证项")
	}
	if cc.AccountUUID != pi.AccountUUID {
		return NewErr(ReasonIdentityMismatch, "Claude Code 与 Pi 不是同一账号")
	}
	if expectedAccountUUID != "" && cc.AccountUUID != expectedAccountUUID {
		return NewErr(ReasonIdentityMismatch, "grant 身份与 .claude.json accountUuid 不符")
	}
	return nil
}
