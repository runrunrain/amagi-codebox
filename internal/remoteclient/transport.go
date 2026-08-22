// Package remoteclient 实现桌面端「远程客户端」能力：作为 v1 契约的消费端，
// 连接另一台运行 amagi-codebox 的宿主桌面端（internal/remote 服务端），提供
// 配对、主机登记簿、会话域/控制域 REST、/ws/v1 终端流与连接生命周期管理。
// 前端经 App 转发层新增绑定消费本包（蓝图《桌面端互联-技术实现方案》§3）。
//
// 依赖方向（蓝图 §3，硬约束）：本包只依赖 internal/remote/contract（纯契约
// 类型）、internal/secrets（Keychain 凭据）与 internal/logging；禁止反向
// import internal/remote（服务端实现）任何符号。
//
// 写权威（蓝图 §5）：一切远端事实以服务端事件/响应为准，客户端仅持投影与
// 本机登记簿（登记簿权威 = 本机）。
//
// 错误契约（蓝图 §7）：服务端 12 个稳定错误码（contract.KnownErrorCodes）
// 透传给上层；REST/网络到客户端错误的映射集中在本文件一处。
package remoteclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ClientError 是本包所有 REST/WS 调用的统一错误包装：HTTP 状态码（网络层
// 失败时为 0）+ 解码后的服务端 v1 错误体（无契约体时为 nil）+ 底层错误。
// 上层（App 转发层/前端）依据 Code 而非 Message 做恢复决策。
type ClientError struct {
	StatusCode int                // HTTP 状态码；WS/纯网络失败为 0
	API        *contract.APIError // 解码成功的服务端 v1 错误体；可为 nil（自产时为契约形态镜像）
	Err        error              // 底层错误（网络/解码失败）
	local      bool               // 自产标记（M-3 修复）：true=客户端本地构造，非服务端真契约体
}

// Error 实现 error；不含凭据与原始终端内容（蓝图 §9）。
func (e *ClientError) Error() string {
	if e.API != nil {
		return fmt.Sprintf("remoteclient: %s (layer=%s, http=%d)", e.API.Code, e.API.Layer, e.StatusCode)
	}
	return fmt.Sprintf("remoteclient: %v (http=%d)", e.Err, e.StatusCode)
}

// Unwrap 暴露底层错误供 errors.Is/As 链路判断。
func (e *ClientError) Unwrap() error { return e.Err }

// Code 返回稳定错误码。网络失败与非契约响应会被综合为契约错误形态
// （net.unreachable / service.down / auth.unpaired，见 classifyFailure），
// 因此 API 恒非 nil 且 Code 恒非空——这是 12 错误码里 net.unreachable 由
// 客户端自产的落点（T0-1 矩阵 §3.1：该码在服务端零引用）。
func (e *ClientError) Code() contract.ErrorCode {
	if e.API != nil {
		return e.API.Code
	}
	return ""
}

// IsAuthRevoked 报告是否为 auth.revoked：连接层见此码（或 WS Close 1008）
// 必须立即进入 revoked 态并停止重连——fail-closed（蓝图 §6 流程 2）。
func (e *ClientError) IsAuthRevoked() bool {
	return e.Code() == contract.ErrorCodeAuthRevoked
}

// IsRateLimited 报告是否为 rate.limited：重连退避加倍（蓝图 §6 流程 2）。
func (e *ClientError) IsRateLimited() bool {
	return e.Code() == contract.ErrorCodeRateLimited
}

// 保证 *ClientError 满足 error 接口。
var _ error = (*ClientError)(nil)

// ---------------------------------------------------------------------------
// 错误映射（蓝图 §7：映射表集中在 transport.go 一处）
// ---------------------------------------------------------------------------

// closedMessage 族是客户端自产错误的固定文案：不携带宿主地址、原始网络错误
// 文本或任何凭据材料（蓝图 §9）。底层错误仅经 Unwrap 暴露给诊断方。
const (
	msgNetUnreachable = "network unreachable"
	msgServiceDown    = "remote service is down"
	msgAuthUnpaired   = "device not paired"
	msgStatusFallback = "request failed with unexpected status"
	msgNoCredential   = "device not paired"
)

// serverAPIError 直接透传服务端契约错误体。
func serverAPIError(status int, api *contract.APIError) *ClientError {
	return &ClientError{StatusCode: status, API: api}
}

// localAPIError 综合客户端自产错误为契约错误形态（镜像 mobile lib/api.ts 的
// toApiRequestError 语义）：网络失败→net.unreachable、非契约响应→service.down、
// 无凭据/401/403 兜底→auth.unpaired。requestId 填本次生成的关联 ID。
func localAPIError(status int, code contract.ErrorCode, layer contract.ErrorLayer, hint contract.ActionHint, msg string, cause error) *ClientError {
	return &ClientError{
		StatusCode: status,
		API: &contract.APIError{
			Code:       code,
			Layer:      layer,
			Message:    msg,
			ActionHint: hint,
		},
		Err:   cause,
		local: true, // 自产：非服务端真契约体（探活等消费方不得据此判定宿主存活）
	}
}

// netUnreachableError 是网络层失败（DNS/TCP/超时/离线）的统一自产映射。
func netUnreachableError(cause error) *ClientError {
	return localAPIError(0, contract.ErrorCodeNetUnreachable, contract.ErrorLayerConnection, contract.ActionHintRetry, msgNetUnreachable, cause)
}

// serviceDownError 是服务端行为异常（非 JSON 成功体/配对响应缺 Cookie 等）的自产映射。
func serviceDownError(status int, cause error) *ClientError {
	return localAPIError(status, contract.ErrorCodeServiceDown, contract.ErrorLayerConnection, contract.ActionHintCheckDesktop, msgServiceDown, cause)
}

// authUnpairedError 是本机未配对凭据缺失、或无契约错误体时 401/403 的兜底映射
// （镜像 mobile 的 fallback 分类）。
func authUnpairedError(status int, cause error) *ClientError {
	return localAPIError(status, contract.ErrorCodeAuthUnpaired, contract.ErrorLayerAuth, contract.ActionHintRePair, msgAuthUnpaired, cause)
}

// classifyFailure 将「有 HTTP 响应但非成功状态」按优先级归类：契约错误体优先
// 透传；无契约体时 401/403 → auth.unpaired，其余 → net.unreachable。
func classifyFailure(status int, body []byte, decodeErr error) *ClientError {
	if decodeErr == nil {
		var api contract.APIError
		if err := json.Unmarshal(body, &api); err == nil && api.Code != "" && api.Message != "" {
			return serverAPIError(status, &api)
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return authUnpairedError(status, decodeErr)
	}
	return localAPIError(status, contract.ErrorCodeNetUnreachable, contract.ErrorLayerConnection, contract.ActionHintRetry, msgStatusFallback, decodeErr)
}

// ---------------------------------------------------------------------------
// Device Cookie（Go 侧保管，不进前端事件——蓝图 §9 / D-T05）
// ---------------------------------------------------------------------------

// deviceCookieName 镜像服务端 O-3 冻结值（internal/remote device_auth.go:
// "amagi_codebox_device"）。契约包不导出该常量且本包禁止 import 服务端实现，
// 故在此冻结镜像；任何一侧改动必须同步（fixture 与集成测试会捕获漂移）。
const deviceCookieName = "amagi_codebox_device"

// Device cookie 值的冻结格式（服务端 deviceCookieValue）：
// "v1." + <22 字符 DeviceID> + "." + <43 字符 secret>，恰 69 字节；
// 两段均为 base64 RawURL 编码（16/32 原始字节）。
const (
	cookiePrefix        = "v1."
	cookieTotalLen      = 69
	deviceIDWireLen     = 22
	deviceSecretWireLen = 43
)

// buildDeviceCookieValue 组装完整 Cookie 值（与请求注入格式一致）。
func buildDeviceCookieValue(deviceID, secret string) string {
	return cookiePrefix + deviceID + "." + secret
}

// parseDeviceCookieValue 严格解析 Set-Cookie 下发的凭据值：前缀 v1.、总长 69、
// 两段 base64 RawURL 恰好 22/43 字符且可解码为 16/32 字节（非规范编码拒绝）。
// 镜像服务端 parseDeviceCookie 的严格度，防脏数据入库。
func parseDeviceCookieValue(v string) (deviceID, secret string, err error) {
	if len(v) != cookieTotalLen || !strings.HasPrefix(v, cookiePrefix) {
		return "", "", fmt.Errorf("device cookie value malformed (len=%d)", len(v))
	}
	rest := v[len(cookiePrefix):]
	dot := strings.LastIndexByte(rest, '.')
	if dot < 0 {
		return "", "", errors.New("device cookie value malformed (no secret separator)")
	}
	id, sec := rest[:dot], rest[dot+1:]
	if len(id) != deviceIDWireLen || len(sec) != deviceSecretWireLen {
		return "", "", fmt.Errorf("device cookie value malformed (id=%d, secret=%d)", len(id), len(sec))
	}
	if rawID, derr := base64.RawURLEncoding.DecodeString(id); derr != nil || len(rawID) != 16 {
		return "", "", errors.New("device cookie value malformed (deviceID not 16 bytes)")
	}
	if rawSec, serr := base64.RawURLEncoding.DecodeString(sec); serr != nil || len(rawSec) != 32 {
		return "", "", errors.New("device cookie value malformed (secret not 32 bytes)")
	}
	return id, sec, nil
}

// ---------------------------------------------------------------------------
// X-Request-ID（与服务端同构：16 随机字节 RawURL 编码）
// ---------------------------------------------------------------------------

func newRequestID() contract.RequestID {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败极罕见；fail-closed 用固定安全 ID（镜像服务端策略）。
		return "0000000000000000"
	}
	return contract.RequestID(base64.RawURLEncoding.EncodeToString(buf))
}

// ---------------------------------------------------------------------------
// v1 端点具名 handle（镜像 contract.V1RestEndpoints 索引，Minor-01 纪律：
// 消费方引用清单而非复制 method/path 字符串）
// ---------------------------------------------------------------------------

var (
	epPairingComplete = contract.V1RestEndpoints[0]
	epHostSummary     = contract.V1RestEndpoints[1]
	epSessionsList    = contract.V1RestEndpoints[2]
	epSessionDetail   = contract.V1RestEndpoints[3]
	epSessionCreate   = contract.V1RestEndpoints[4]
	epSessionStop     = contract.V1RestEndpoints[5]
	epSessionRestart  = contract.V1RestEndpoints[6]
	epSessionRemove   = contract.V1RestEndpoints[7]
	epControlAcquire = contract.V1RestEndpoints[8]
	epControlRelease  = contract.V1RestEndpoints[9]
)

// Transport 承载 REST 基座状态（蓝图 §3）：base URL、device Cookie（Go 侧
// 保管并随请求注入，不进前端事件，§9）、X-Request-ID 生成、统一错误映射。
// 所有 v1 REST 调用（pairing/hosts/sessions）都经 doResp 单一入口，保证
// 头注入与 12 错误码映射只有一份实现。
type Transport struct {
	BaseURL string // 例：http://host:port（Tailscale 明文 HTTP，TLS 后置）
	HTTP    *http.Client

	mu       sync.RWMutex
	deviceID string // 配对后填入；空表示未持凭据
	secret   string // 永不落盘/落日志（仅 Keychain + 内存）
}

// defaultRequestTimeout 是 REST 短连接的默认超时（蓝图 §6：REST 短连接）。
const defaultRequestTimeout = 10 * time.Second

// NewTransport 按 baseURL（http/https + host[:port]）构建 REST 基座。
func NewTransport(baseURL string) (*Transport, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q", baseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported base URL scheme %q", u.Scheme)
	}
	return &Transport{
		BaseURL: strings.TrimSuffix(u.Scheme+"://"+u.Host, "/"),
		// 自查加固（非 diting 编号项）：v1 契约无重定向语义；跟随 3xx 会让设备
		// Cookie 被 net/http 重定向复制器带到同域其它端口/子域（Cookie 头
		// 对子域开放，isDomainOrSubdomain），或把代理拦截页伪成成功链路。
		// 处置：禁用跟随，重定向一律作为非成功状态交给 classifyFailure。
		HTTP: &http.Client{
			Timeout: defaultRequestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// SetCredential 装载配对获得的设备凭据（内存持有；持久化在 Keychain，由
// PairingService 负责）。空值拒绝。
func (t *Transport) SetCredential(deviceID, secret string) error {
	if deviceID == "" || secret == "" {
		return errors.New("device credential requires non-empty deviceID and secret")
	}
	t.mu.Lock()
	t.deviceID = deviceID
	t.secret = secret
	t.mu.Unlock()
	return nil
}

// ClearCredential 清除内存凭据（撤销/登出时）。
func (t *Transport) ClearCredential() {
	t.mu.Lock()
	t.deviceID, t.secret = "", ""
	t.mu.Unlock()
}

// HasCredential 报告是否已持有设备凭据。
func (t *Transport) HasCredential() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.deviceID != ""
}

// Credential 返回内存态设备凭据快照（供 ws.go 拨号头注入；secret 不得入
// 日志/事件）。未装载时 ok=false。
func (t *Transport) Credential() (deviceID, secret string, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.deviceID == "" || t.secret == "" {
		return "", "", false
	}
	return t.deviceID, t.secret, true
}

// origin 返回随每个请求声明的 Origin（同源浏览器形态 http(s)://host:port），
// 满足服务端 unsafeOriginRequired（POST/DELETE 系）与 safeBrowserProof
// （GET 系）策略——与 e2e harness 的 Go 客户端做法一致。
func (t *Transport) origin() string { return t.BaseURL }

// requestOption 描述一次 v1 REST 调用：{id} 段替换、请求体、是否携带设备
// Cookie（pairing/complete 与未配对探活为 false）。
type requestOption struct {
	sessionID string
	body      any
	auth      bool
}

// errorBodyCap 限制错误体读取量（防御异常服务端拖垮内存）。
const errorBodyCap = 64 << 10

// successBodyCap 是成功体读取上限（M-2，diting Minor 修复）：会话列表/详情
// 是合法可超 64KiB 的响应，此前与错误体共用 errorBodyCap 会在静默截断后把
// 合法成功体伪装成 service.down。4MiB 与服务端回放环上限同量级，超出则
// 如实报 service.down（防御异常服务端）。
const successBodyCap = 4 << 20

// doResp 执行一次 v1 REST 请求：路径一律取自 contract.V1RestEndpoints 具名
// handle；注入 X-Request-ID / Origin /（按需）device Cookie / Content-Type；
// 成功体 JSON 解码到 out（nil 或 204 跳过）；失败统一走 classifyFailure。
// 返回已排空关闭的 *http.Response（仅供读取 Set-Cookie 等响应头）。
func (t *Transport) doResp(ctx context.Context, ep contract.RestEndpoint, opt requestOption, out any) (*http.Response, *ClientError) {
	// 路径：{id} 单段恰好替换一次（与服务端恰好一次 PathUnescape 对齐）。
	path := ep.Path
	if opt.sessionID != "" {
		path = strings.Replace(path, "{id}", url.PathEscape(opt.sessionID), 1)
	}
	full := t.BaseURL + contract.RESTBasePath + path

	var bodyReader io.Reader
	if opt.body != nil {
		raw, err := json.Marshal(opt.body)
		if err != nil {
			return nil, localAPIError(0, contract.ErrorCodeBadRequest, contract.ErrorLayerConnection,
				contract.ActionHintRetry, "invalid request body", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, ep.Method, full, bodyReader)
	if err != nil {
		return nil, netUnreachableError(err)
	}
	reqID := newRequestID()
	req.Header.Set(contract.RequestIDHeader, string(reqID))
	req.Header.Set("Origin", t.origin())
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if opt.auth {
		t.mu.RLock()
		deviceID, secret := t.deviceID, t.secret
		t.mu.RUnlock()
		if deviceID == "" {
			return nil, localAPIError(0, contract.ErrorCodeAuthUnpaired, contract.ErrorLayerAuth,
				contract.ActionHintRePair, msgNoCredential, nil)
		}
		req.AddCookie(&http.Cookie{Name: deviceCookieName, Value: buildDeviceCookieValue(deviceID, secret)})
	}
	if t.HTTP == nil {
		t.HTTP = &http.Client{
			Timeout: defaultRequestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	resp, err := t.HTTP.Do(req)
	if err != nil {
		// 网络层失败（DNS/TCP/超时/取消）：统一自产 net.unreachable，不透出宿主细节。
		return nil, netUnreachableError(err)
	}
	defer drainAndClose(resp.Body) //nolint:errcheck // 尽力排空复连

	if resp.StatusCode == ep.SuccessStatus {
		if out == nil || resp.StatusCode == http.StatusNoContent {
			return resp, nil
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, successBodyCap))
		if rerr != nil {
			return resp, serviceDownError(resp.StatusCode, rerr)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			// 非 JSON 成功体（如代理拦截页）或超限截断：如实映射 service.down，
			// 不伪装成功。
			return resp, serviceDownError(resp.StatusCode, err)
		}
		return resp, nil
	}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyCap))
	return resp, classifyFailure(resp.StatusCode, raw, nil)
}

// do 是 doResp 的简化入口（不关心响应头）。
func (t *Transport) do(ctx context.Context, ep contract.RestEndpoint, opt requestOption, out any) *ClientError {
	_, cerr := t.doResp(ctx, ep, opt, out)
	return cerr
}

// HostSummaryUnauthenticated 探活语义（蓝图 §6 流程 1：无鉴权 GET host/summary）。
func (t *Transport) HostSummaryUnauthenticated(ctx context.Context) (contract.HostSummary, *ClientError) {
	var hs contract.HostSummary
	cerr := t.do(ctx, epHostSummary, requestOption{}, &hs)
	return hs, cerr
}

// HostSummary 携带设备凭据获取宿主摘要（配对后的凭据验证 / 健康探活用）。
func (t *Transport) HostSummary(ctx context.Context) (contract.HostSummary, *ClientError) {
	var hs contract.HostSummary
	cerr := t.do(ctx, epHostSummary, requestOption{auth: true}, &hs)
	return hs, cerr
}

// drainAndClose 排空并关闭响应体，保证连接可复用。
func drainAndClose(body io.ReadCloser) error {
	if body == nil {
		return nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, errorBodyCap))
	return body.Close()
}
