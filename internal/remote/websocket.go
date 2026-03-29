package remote

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// 允许所有来源（移动端 WebView 不发送 Origin 或来源不受信任域名）
		return true
	},
}

// clientMsg 客户端→服务端帧格式
type clientMsg struct {
	Type string `json:"type"` // "input" | "resize"
	Data string `json:"data"` // base64（input 类型）
	Cols int    `json:"cols"` // resize 类型
	Rows int    `json:"rows"` // resize 类型
}

// serverMsg 服务端→客户端帧格式
type serverMsg struct {
	Type     string `json:"type"`               // "output" | "exit" | "dimensions"
	Data     string `json:"data,omitempty"`     // base64（output 类型）
	ExitCode int    `json:"exitCode,omitempty"` // exit 类型
	Cols     int    `json:"cols,omitempty"`     // dimensions 类型
	Rows     int    `json:"rows,omitempty"`     // dimensions 类型
}

// PtyBridge PTY 输入/输出桥接接口，由 App 实现（委托给 pty.Service）
type PtyBridge interface {
	PtyWrite(sessionID string, data string) error
	PtyResize(sessionID string, cols, rows int) error
	RegisterOutputCallback(sessionID string, id string, cb func(data []byte))
	UnregisterOutputCallback(sessionID string, id string)
	RegisterExitCallback(sessionID string, id string, cb func(exitCode uint32))
	UnregisterExitCallback(sessionID string, id string)
	RegisterResizeCallback(sessionID string, id string, cb func(cols, rows int))
	UnregisterResizeCallback(sessionID string, id string)
}

// HistoryProvider 可选接口：支持返回 PTY 输出历史，供新连接的 WebSocket 客户端重放。
type HistoryProvider interface {
	GetOutputHistory(sessionID string) ([]byte, error)
}

// DimensionsProvider 可选接口：支持返回 PTY 当前尺寸，供 observer 客户端同步终端大小。
type DimensionsProvider interface {
	GetPtyDimensions(sessionID string) (cols, rows int, err error)
}

// serveWebSocket 处理 /ws/terminal/{sessionID} 的 WebSocket 连接
func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request, sessionID string) {
	ptyBridge, ok := s.app.(PtyBridge)
	if !ok {
		writeError(w, http.StatusInternalServerError, "pty bridge not available")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("remote", "WebSocket upgrade 失败", fmt.Sprintf("session=%s err=%v", sessionID, err))
		return
	}
	defer conn.Close()

	// 读取 mode 参数，observer 模式下忽略 resize 请求
	mode := r.URL.Query().Get("mode")
	isObserver := mode == "observer"

	// 为此连接生成唯一 ID
	connID := fmt.Sprintf("ws-%d", time.Now().UnixNano())
	s.log.Info("remote", "WebSocket 连接已建立", fmt.Sprintf("session=%s conn=%s", sessionID, connID))

	var writeMu sync.Mutex
	writeJSON := func(msg serverMsg, timeout time.Duration) error {
		b, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if timeout > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(timeout))
		}
		return conn.WriteMessage(websocket.TextMessage, b)
	}

	// observer 模式：先发送当前 PTY 尺寸，让移动端在回放历史输出前就进入正确的 cols/rows
	if isObserver {
		if dp, ok := s.app.(DimensionsProvider); ok {
			if cols, rows, err := dp.GetPtyDimensions(sessionID); err == nil && cols > 0 && rows > 0 {
				dimMsg := serverMsg{Type: "dimensions", Cols: cols, Rows: rows}
				if err := writeJSON(dimMsg, 5*time.Second); err != nil {
					s.log.Debug("remote", "WebSocket 发送尺寸失败", fmt.Sprintf("conn=%s err=%v", connID, err))
				} else {
					s.log.Info("remote", "WebSocket PTY 尺寸已发送", fmt.Sprintf("conn=%s cols=%d rows=%d", connID, cols, rows))
				}
			}
		}
	}

	// 发送历史输出给新连接的客户端（重放缓冲区），让"后加入者"看到历史内容
	if hp, ok := s.app.(HistoryProvider); ok {
		if history, err := hp.GetOutputHistory(sessionID); err == nil && len(history) > 0 {
			encoded := base64.StdEncoding.EncodeToString(history)
			histMsg := serverMsg{Type: "output", Data: encoded}
			if err := writeJSON(histMsg, 10*time.Second); err != nil {
				s.log.Debug("remote", "WebSocket 发送历史输出失败", fmt.Sprintf("conn=%s err=%v", connID, err))
			} else {
				s.log.Info("remote", "WebSocket 历史输出已发送", fmt.Sprintf("conn=%s bytes=%d", connID, len(history)))
			}
		}
	}

	// 注册 PTY 输出回调：将 PTY 输出转发给 WebSocket 客户端
	ptyBridge.RegisterOutputCallback(sessionID, connID, func(data []byte) {
		encoded := base64.StdEncoding.EncodeToString(data)
		msg := serverMsg{Type: "output", Data: encoded}
		if err := writeJSON(msg, 10*time.Second); err != nil {
			s.log.Debug("remote", "WebSocket write 失败（连接可能已断开）", fmt.Sprintf("conn=%s err=%v", connID, err))
		}
	})

	// 注册 PTY 退出回调
	ptyBridge.RegisterExitCallback(sessionID, connID, func(exitCode uint32) {
		msg := serverMsg{Type: "exit", ExitCode: int(exitCode)}
		_ = writeJSON(msg, 5*time.Second)
		conn.Close()
	})

	if isObserver {
		ptyBridge.RegisterResizeCallback(sessionID, connID, func(cols, rows int) {
			if cols <= 0 || rows <= 0 {
				return
			}
			msg := serverMsg{Type: "dimensions", Cols: cols, Rows: rows}
			if err := writeJSON(msg, 5*time.Second); err != nil {
				s.log.Debug("remote", "WebSocket 推送尺寸失败", fmt.Sprintf("conn=%s err=%v", connID, err))
			}
		})
	}

	defer func() {
		ptyBridge.UnregisterOutputCallback(sessionID, connID)
		ptyBridge.UnregisterExitCallback(sessionID, connID)
		ptyBridge.UnregisterResizeCallback(sessionID, connID)
		s.log.Info("remote", "WebSocket 连接已断开", fmt.Sprintf("session=%s conn=%s", sessionID, connID))
	}()

	// 读取客户端消息循环
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
			if isObserver {
				// Observer 模式：忽略 resize，避免影响桌面端 PTY
				continue
			}
			if msg.Cols > 0 && msg.Rows > 0 {
				if err := ptyBridge.PtyResize(sessionID, msg.Cols, msg.Rows); err != nil {
					s.log.Debug("remote", "PTY resize 失败", fmt.Sprintf("session=%s err=%v", sessionID, err))
				}
			}
		default:
			s.log.Debug("remote", "未知 WebSocket 消息类型", fmt.Sprintf("type=%s", msg.Type))
		}
	}
}
