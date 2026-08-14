package remote

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"amagi-codebox/internal/structured"

	"github.com/gorilla/websocket"
)

// clientMsg 客户端→服务端帧格式
type clientMsg struct {
	Type string `json:"type"` // "input" | "resize"
	Data string `json:"data"` // base64（input 类型）
	Cols int    `json:"cols"` // resize 类型
	Rows int    `json:"rows"` // resize 类型
}

// serverMsg 服务端→客户端帧格式（legacy 兼容；M-003 后 /ws/terminal 不再产生
// output/structured-part/exit/dimensions 帧，但类型保留供 v1/测试与序列化兼容）。
type serverMsg struct {
	Type               string           `json:"type"`                         // "output" | "structured-part" | "exit" | "dimensions"
	Data               string           `json:"data,omitempty"`               // base64（output 类型）
	Seq                uint64           `json:"seq,omitempty"`                // output / structured-part 关联序号
	History            bool             `json:"history,omitempty"`            // output 是否为连接建立时的历史快照
	StructuredExpected bool             `json:"structuredExpected,omitempty"` // 新客户端用于延迟 raw fallback
	Part               *structured.Part `json:"part,omitempty"`               // structured-part 类型
	ExitCode           int              `json:"exitCode,omitempty"`           // exit 类型
	Cols               int              `json:"cols,omitempty"`               // dimensions 类型
	Rows               int              `json:"rows,omitempty"`               // dimensions 类型
}

// PtyBridge is the narrow INPUT-ONLY surface the legacy /ws/terminal handler
// needs. M-003: the legacy naked-SessionID PTY output/exit/resize callback bypass
// is REMOVED — all PTY output/exit now flows exclusively through the run-scoped
// RunEventProjector (desktop Wails events + M2 /ws/v1 causal stream). The legacy
// loopback terminal path is input-only; remote output delivery is via /ws/v1
// (design §8.6.3, §6.1 single /ws/v1 consumer). No non-projector feed producer
// remains.
type PtyBridge interface {
	PtyWrite(sessionID string, data string) error
	PtyResize(sessionID string, cols, rows int) error
}

// serveWebSocket 处理 /ws/terminal/{sessionID} 的 WebSocket 连接。
//
// M-003: this legacy loopback path is INPUT-ONLY. It no longer registers naked
// SessionID PTY output/exit/resize callbacks that wrote directly to the socket
// (a second feed producer bypassing the run-scoped RunEventProjector, which
// defeated the H1 production producer uniqueness claim). All PTY output/exit is
// now unified through the projector → H1 feed → v1 stream; remote clients
// receive output via /ws/v1. This handler retains only input dispatch (PtyWrite)
// for backward compatibility with legacy loopback callers.
func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request, sessionID string) {
	ptyBridge, ok := s.ptyBridge.(PtyBridge)
	if !ok {
		writeError(w, http.StatusInternalServerError, "pty bridge not available")
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			return isAllowedWebSocketOrigin(r)
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close() //nolint:errcheck // 尽力关闭 WS 连接；客户端可能已断开，错误无可处理

	connID := fmt.Sprintf("ws-%d", time.Now().UnixNano())
	s.log.Info("remote", "WebSocket（legacy input-only）连接已建立", fmt.Sprintf("session=%s conn=%s", sessionID, connID))

	// 读取客户端消息循环（仅输入）。M-003：不再注册 output/exit/resize 回调，
	// 消除非 projector 的 feed producer。
	for {
		_, rawMsg, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Debug("remote", "WebSocket read 失败", fmt.Sprintf("conn=%s err=%v", connID, err))
			}
			return
		}

		var msg clientMsg
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			s.log.Debug("remote", "WebSocket 消息解析失败", fmt.Sprintf("conn=%s err=%v", connID, err))
			continue
		}

		switch msg.Type {
		case "input":
			if err := ptyBridge.PtyWrite(sessionID, msg.Data); err != nil {
				s.log.Debug("remote", "PTY 写入失败", fmt.Sprintf("session=%s err=%v", sessionID, err))
			}
		case "resize":
			// Web/remote terminal is an input/output surface, not a PTY geometry
			// owner. Desktop/Wails remains the authority for shared PTY dimensions.
			continue
		default:
			s.log.Debug("remote", "未知 WebSocket 消息类型", fmt.Sprintf("type=%s", msg.Type))
		}
	}
}
