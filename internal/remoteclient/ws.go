// ws.go /ws/v1 客户端线缆层（蓝图 §3/§6 流程 3，决策 D-T05）：
//
// 长驻 goroutine 持有 WebSocket 连接，前端不直连 WS（Origin/凭据不暴露）。
// 传输库：复用 vendor 内 github.com/gorilla/websocket v1.5.3——服务端
// （internal/remote）用其 Upgrader，客户端用同库 Dialer（ws_dial_probe_test.go
// 是客户端能力回归锚点）。
//
// 拨号纪律（契约 §4）：URL 唯一 ws(s)://<host>/ws/v1 且不带任何凭据/查询参数；
// 设备凭据只经 device Cookie 头注入（Go 侧保管，不进前端事件，§9）；Origin
// 声明同源形态 http(s)://host:port（服务端 unsafeOriginRequired/safeBrowserProof
// 与 /ws/v1 单 Origin 白名单策略）。
//
// 帧纪律（D-T02）：出站客户端帧一律先构造 contract 纯类型，编码后必须过
// contract.DecodeClientFrame 严格回验（契约没有客户端帧 Marshal 导出，本文件
// 以「marshal→decode 回验→发送」保证与冻结解码器一致，禁止裸 json.Marshal
// 直发）；入站服务端事件解码分流见 decodeServerEvent——未知类型不得终止连接
// （契约 §2.3 前向兼容：记净化诊断、忽略业务更新）。
//
// 输入不直接写连接，一律经 outbox.go；输出经 conn.go 事件总线回流前端。

package remoteclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"amagi-codebox/internal/remote/contract"
)

// wsHandshakeTimeout 是 WS 拨号握手的默认超时（与服务端 REST 短连接节奏一致）。
const wsHandshakeTimeout = 10 * time.Second

// wsURL 从 REST BaseURL 推导唯一 WS URL：http→ws / https→wss + 契约冻结路径，
// 无查询参数（契约 §4：URL MUST NOT 携带 token/session/mode/凭据）。
func wsURL(baseURL string) string {
	u := baseURL
	if strings.HasPrefix(u, "https://") {
		u = "wss://" + strings.TrimPrefix(u, "https://")
	} else {
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + contract.WebSocketV1Path
}

// dialWebSocket 拨号 /ws/v1：Origin 头 = REST BaseURL（同源形态），device
// Cookie 头注入设备凭据。transport 未装载凭据时返回错误（fail-closed，不裸连）。
func dialWebSocket(ctx context.Context, t *Transport, handshakeTimeout time.Duration) (*websocket.Conn, error) {
	deviceID, secret, ok := t.Credential()
	if !ok {
		return nil, fmt.Errorf("remoteclient: ws dial requires a loaded device credential")
	}
	if handshakeTimeout <= 0 {
		handshakeTimeout = wsHandshakeTimeout
	}
	header := http.Header{}
	header.Set("Origin", t.origin())
	header.Set("Cookie", deviceCookieName+"="+buildDeviceCookieValue(deviceID, secret))
	d := websocket.Dialer{HandshakeTimeout: handshakeTimeout}
	conn, _, err := d.DialContext(ctx, wsURL(t.BaseURL), header)
	return conn, err
}

// encodeClientFrame 编码一个客户端帧：json.Marshal 后立即用契约严格解码器
// 回验（未知字段/条件失败/越界值都会在此拦下，绝不把非法帧写上连接）。
func encodeClientFrame(f contract.ClientFrame) ([]byte, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("remoteclient: encode client frame: %w", err)
	}
	if _, derr := contract.DecodeClientFrame(raw); derr != nil {
		return nil, fmt.Errorf("remoteclient: client frame failed contract validation: %w", derr)
	}
	return raw, nil
}

// UnknownServerEvent 是未知/畸形服务端事件的净化诊断（契约 §2.3：未知类型
// 保持连接、忽略业务更新、记录 sanitized 诊断；不保留任何原始字段值——
// WireType 仅来自顶层 type 字段的字符串值，其余字段一律丢弃）。
type UnknownServerEvent struct {
	WireType string
	Reason   string // unknown-type | malformed
}

// DecodedServerEvent 是入站事件的分流结果：Known 与 Unknown 恰一非空。
type DecodedServerEvent struct {
	Known   contract.KnownServerEvent
	Unknown *UnknownServerEvent
}

// unknown/malformed 归类的固定 reason 值。
const (
	unknownReasonType      = "unknown-type"
	unknownReasonMalformed = "malformed"
)

// decodeServerEvent 严格解码一个服务端事件：已知类型走契约 DecodeKnownServerEvent
// （校验必填/闭集枚举/安全 seq/Base64）；未知类型归 Unknown（不终止连接）；
// 已知类型但校验失败同样归 Unknown（malformed——畸形 ACK 按契约净化为 Unknown
// 并由 conn 层强制只读，不结算、不保留原始 ID）。
func decodeServerEvent(raw []byte) DecodedServerEvent {
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &probe)
	if !isKnownServerEventType(probe.Type) {
		return DecodedServerEvent{Unknown: &UnknownServerEvent{WireType: probe.Type, Reason: unknownReasonType}}
	}
	ev, err := contract.DecodeKnownServerEvent(raw)
	if err != nil {
		return DecodedServerEvent{Unknown: &UnknownServerEvent{WireType: probe.Type, Reason: unknownReasonMalformed}}
	}
	return DecodedServerEvent{Known: ev}
}

// isKnownServerEventType 报告 t 是否属于 8 个冻结服务端事件类型。
func isKnownServerEventType(t string) bool {
	for _, k := range contract.KnownServerEventTypes {
		if k == t {
			return true
		}
	}
	return false
}
