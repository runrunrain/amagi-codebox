package contract

// webui.go — pi Web 平面（embedded pi webui）远程暴露面的 wire 符号
// （remote-webui-plane 切片，2026-09）。
//
// 该面独立于 §3 冻结的 10 端点 REST 清单（V1RestEndpoints 与共享 fixture
// 的 manifest.restEndpoints 保持 10 条不变），由两部分组成：
//   1. GET {RESTBasePath}/session/{id}/webui —— 会话 webui 平面状态端点
//      （WebUIStatusEndpoint + WebUIStatus，本文件）；
//   2. /webui/{sessionID}/... —— per-session 反向代理面
//      （WebUIProxyPathPrefix 前缀，非 REST 端点；由 remote server 顶层
//      分发挂载，详见 docs/developer/remote-api-v1-contract.md）。
//
// 安全红线：webui capability token 视同会话权限 —— 只经 URL fragment
// （#/t=<token>）下发，绝不进 query/path/请求行/日志/错误体。

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// WebUIProxyPathPrefix is the top-level URL prefix of the per-session pi
// webui reverse proxy: /webui/{sessionID}/<backend-path>. It is served by the
// remote server (NOT the loopback-only pi webui server) and is the pi webui
// plane's ONLY externally visible face.
const WebUIProxyPathPrefix = "/webui/"

// WebUIPlaneActorDeviceHeader 是设备平面车道身份透传头的 wire 值（冻结
// "X-Amagi-Plane-Actor-Device"，design §5.5-3）。仅由 remote 反代在写面
// 转发时覆盖式设置：先剥离客户端自带值，再注入 plane token 解析出的已
// 鉴权设备 ID（与 Authorization 覆盖注入同一纪律）。后端数据面
// （internal/claudewebui）把它当作「车道选择信号」——请求携带非空值 →
// 写入分流到设备平面车道（无夺权、写时校验该设备持控），否则 → 桌面
// 车道；该头不是鉴权（鉴权永远是 Bearer/子协议 token），伪造头不构成
// 提权。
const WebUIPlaneActorDeviceHeader = "X-Amagi-Plane-Actor-Device"

// WebUIPlaneState enumerates the five session webui-plane states (mirrors the
// host-side internal/webui.State machine).
type WebUIPlaneState string

const (
	WebUIPlaneStateProbing     WebUIPlaneState = "probing"
	WebUIPlaneStateAvailable   WebUIPlaneState = "available"
	WebUIPlaneStateUnavailable WebUIPlaneState = "unavailable"
	WebUIPlaneStateEnded       WebUIPlaneState = "ended"
	WebUIPlaneStateUnknown     WebUIPlaneState = "unknown"
)

// KnownWebUIPlaneStates is the complete set of five webui-plane states.
var KnownWebUIPlaneStates = []WebUIPlaneState{
	WebUIPlaneStateProbing,
	WebUIPlaneStateAvailable,
	WebUIPlaneStateUnavailable,
	WebUIPlaneStateEnded,
	WebUIPlaneStateUnknown,
}

// IsKnownWebUIPlaneState reports whether s is one of the five closed states.
func IsKnownWebUIPlaneState(s WebUIPlaneState) bool {
	for _, k := range KnownWebUIPlaneStates {
		if k == s {
			return true
		}
	}
	return false
}

// WebUIStatusEndpoint is the additive v1 extension endpoint (path relative to
// RESTBasePath): GET /session/{id}/webui.
//
// NOTE: deliberately declared OUTSIDE contract.V1RestEndpoints — that manifest
// is the frozen 10-endpoint enumeration mirrored by the shared wire fixture
// (mobile/src/lib/contract/testdata/v1-wire-fixtures.json) and must stay at 10.
var WebUIStatusEndpoint = RestEndpoint{
	Method:        http.MethodGet,
	Path:          "/session/{id}/webui",
	SuccessStatus: http.StatusOK,
}

// WebUIStatus is the success body of GET /session/{id}/webui. URL is a
// conditional field: REQUIRED only when State=="available" (omitted
// otherwise). It is a same-origin relative proxy URL of the form
// {WebUIProxyPathPrefix}{sessionId}/[/#/t=<capability-token>]; the token (if
// any) travels ONLY in the URL fragment and never enters an HTTP request
// line, query, or log.
type WebUIStatus struct {
	State WebUIPlaneState `json:"state"`
	URL   string          `json:"url,omitempty"`
}

func (WebUIStatus) isRESTResponse() {}

// ValidateWebUIStatus enforces the conditional union: known state; URL
// required iff state=="available"; URL must be a relative path under the
// proxy prefix with no query string and no embedded credentials.
func ValidateWebUIStatus(v WebUIStatus) error {
	if !IsKnownWebUIPlaneState(v.State) {
		return fmt.Errorf("contract: WebUIStatus.State %q is not a known webui plane state", v.State)
	}
	if v.State == WebUIPlaneStateAvailable {
		if v.URL == "" {
			return errors.New("contract: WebUIStatus.URL is required when state is available")
		}
	} else if v.URL != "" {
		return fmt.Errorf("contract: WebUIStatus.URL must be omitted for state %q", v.State)
	}
	if v.URL != "" {
		if !strings.HasPrefix(v.URL, WebUIProxyPathPrefix) {
			return fmt.Errorf("contract: WebUIStatus.URL must start with %q", WebUIProxyPathPrefix)
		}
		// 相对路径 only：无 query（token 绝不走 query）、无 userinfo、无 scheme。
		if strings.ContainsAny(v.URL, "?@") || strings.Contains(v.URL, "://") {
			return errors.New("contract: WebUIStatus.URL must be a relative proxy path without query")
		}
	}
	return nil
}
