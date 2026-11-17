package claudeaccounts

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// 网络硬约束（design §5.2）：固定 HTTPS 官方 origin、10 秒超时、最多
// 64 KiB 响应、拒绝所有 redirect；禁用请求头/响应体日志。
const (
	httpRequestTimeout = 10 * time.Second
	maxResponseBytes   = 64 << 10
)

// errRedirectRefused 是拒绝 redirect 的固定错误（不含目标 URL，避免把
// 可能携带 token 的查询参数泄漏到日志）。
var errRedirectRefused = fmt.Errorf("redirect refused")

// NewConstrainedHTTPClient 返回受约束的固定 http.Client。transport 可注入
// （测试注入 RoundTripper / httptest），nil 时使用默认 transport。
// 生产端点固定 HTTPS 官方 origin；不接受 provider baseURL、用户输入 URL
// 或镜像域（design §5.2）。
func NewConstrainedHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout: httpRequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errRedirectRefused
		},
		Transport: transport,
	}
}

// readBoundedBody 读取不超过 maxResponseBytes 的响应体；超限报错。
// 错误消息不含 body 内容（敏感边界：HTTP 原文不进日志/错误消息）。
func readBoundedBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, NewErr(ReasonIdentityUnverified, "网络响应读取失败")
	}
	if len(data) > maxResponseBytes {
		return nil, NewErr(ReasonIdentityUnverified, "网络响应超过 %d 字节上限", maxResponseBytes)
	}
	return data, nil
}
