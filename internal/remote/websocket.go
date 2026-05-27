package remote

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
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

// serverMsg 服务端→客户端帧格式
type serverMsg struct {
	Type               string           `json:"type"`                         // "output" | "structured-part" | "exit" | "dimensions"
	Data               string           `json:"data,omitempty"`               // base64（output 类型）
	Seq                uint64           `json:"seq,omitempty"`                // output / structured-part 关联序号
	StructuredExpected bool             `json:"structuredExpected,omitempty"` // 新客户端用于延迟 raw fallback
	Part               *structured.Part `json:"part,omitempty"`               // structured-part 类型
	ExitCode           int              `json:"exitCode,omitempty"`           // exit 类型
	Cols               int              `json:"cols,omitempty"`               // dimensions 类型
	Rows               int              `json:"rows,omitempty"`               // dimensions 类型
}

type structuredWorkItem struct {
	seq  uint64
	data []byte
}

const structuredQueueSize = 32

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

// DimensionsProvider 可选接口：支持返回 PTY 当前尺寸，供所有远程客户端同步终端大小。
type DimensionsProvider interface {
	GetPtyDimensions(sessionID string) (cols, rows int, err error)
}

// ObserverAttachProvider 提供原子 attach：先冻结快照，再注册 live output / dimensions 回调，避免 observer 丢帧窗口。
type ObserverAttachProvider interface {
	AttachSessionObserver(sessionID string, id string, outputCB func(data []byte), resizeCB func(cols, rows int)) (history []byte, cols, rows int, err error)
	DetachSessionObserver(sessionID string, id string)
}

// serveWebSocket 处理 /ws/terminal/{sessionID} 的 WebSocket 连接
func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request, sessionID string) {
	ptyBridge, ok := s.app.(PtyBridge)
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
		s.log.Error("remote", "WebSocket upgrade 失败", fmt.Sprintf("session=%s err=%v", sessionID, err))
		return
	}
	defer conn.Close()

	// 远程 resize 始终不作为 PTY 尺寸权威来源，避免 Web 端与桌面端争用
	// 同一个共享 PTY。输入权限仍沿用既有前端 mode 语义，不在此处改变。

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

	structuredQueue := make(chan structuredWorkItem, structuredQueueSize)
	structuredDone := make(chan struct{})
	defer close(structuredDone)

	go func() {
		for {
			select {
			case <-structuredDone:
				return
			case item := <-structuredQueue:
				func() {
					defer func() {
						if recovered := recover(); recovered != nil {
							s.log.Debug("remote", "structured 分类失败，raw output 已保留", fmt.Sprintf("conn=%s err=%v", connID, recovered))
						}
					}()
					part := structured.Classify(item.data, item.seq)
					if err := writeJSON(serverMsg{Type: "structured-part", Seq: item.seq, Part: &part}, 5*time.Second); err != nil {
						s.log.Debug("remote", "WebSocket structured-part write 失败（连接可能已断开）", fmt.Sprintf("conn=%s err=%v", connID, err))
					}
				}()
			}
		}
	}()

	var outputSeq uint64

	outputCB := func(data []byte) {
		seq := atomic.AddUint64(&outputSeq, 1)
		encoded := base64.StdEncoding.EncodeToString(data)
		msg := serverMsg{Type: "output", Data: encoded, Seq: seq, StructuredExpected: len(data) > 0}
		if err := writeJSON(msg, 10*time.Second); err != nil {
			s.log.Debug("remote", "WebSocket write 失败（连接可能已断开）", fmt.Sprintf("conn=%s err=%v", connID, err))
			return
		}
		if len(data) == 0 {
			return
		}
		structuredData := make([]byte, len(data))
		copy(structuredData, data)
		select {
		case structuredQueue <- structuredWorkItem{seq: seq, data: structuredData}:
		default:
			s.log.Debug("remote", "structured 队列已满，客户端将按 raw fallback", fmt.Sprintf("conn=%s seq=%d", connID, seq))
		}
	}

	resizeCB := func(cols, rows int) {
		if cols <= 0 || rows <= 0 {
			return
		}
		msg := serverMsg{Type: "dimensions", Cols: cols, Rows: rows}
		if err := writeJSON(msg, 5*time.Second); err != nil {
			s.log.Debug("remote", "WebSocket 推送尺寸失败", fmt.Sprintf("conn=%s err=%v", connID, err))
		}
	}

	if attachProvider, ok := s.app.(ObserverAttachProvider); ok {
		history, cols, rows, err := attachProvider.AttachSessionObserver(sessionID, connID, outputCB, resizeCB)
		if err != nil {
			s.log.Error("remote", "WebSocket attach observer 失败", fmt.Sprintf("session=%s conn=%s err=%v", sessionID, connID, err))
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()), time.Now().Add(2*time.Second))
			return
		}
		defer attachProvider.DetachSessionObserver(sessionID, connID)

		if len(history) > 0 {
			encoded := base64.StdEncoding.EncodeToString(history)
			if err := writeJSON(serverMsg{Type: "output", Data: encoded}, 10*time.Second); err != nil {
				s.log.Debug("remote", "WebSocket 发送历史输出失败", fmt.Sprintf("conn=%s err=%v", connID, err))
			} else {
				s.log.Info("remote", "WebSocket 历史输出已发送", fmt.Sprintf("conn=%s bytes=%d", connID, len(history)))
			}
		}

		if cols > 0 && rows > 0 {
			if err := writeJSON(serverMsg{Type: "dimensions", Cols: cols, Rows: rows}, 5*time.Second); err != nil {
				s.log.Debug("remote", "WebSocket 发送尺寸失败", fmt.Sprintf("conn=%s err=%v", connID, err))
			} else {
				s.log.Info("remote", "WebSocket PTY 尺寸已发送", fmt.Sprintf("conn=%s cols=%d rows=%d", connID, cols, rows))
			}
		}
	} else {
		// 兼容旧实现：先发送 history / dimensions，再注册 live 回调。
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
		ptyBridge.RegisterOutputCallback(sessionID, connID, outputCB)
		ptyBridge.RegisterResizeCallback(sessionID, connID, resizeCB)
		defer func() {
			ptyBridge.UnregisterOutputCallback(sessionID, connID)
			ptyBridge.UnregisterResizeCallback(sessionID, connID)
		}()
	}

	// 注册 PTY 退出回调
	ptyBridge.RegisterExitCallback(sessionID, connID, func(exitCode uint32) {
		msg := serverMsg{Type: "exit", ExitCode: int(exitCode)}
		_ = writeJSON(msg, 5*time.Second)
		conn.Close()
	})

	defer func() {
		ptyBridge.UnregisterExitCallback(sessionID, connID)
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
			// Web/remote terminal is an input/output surface, not a PTY geometry
			// owner. Desktop/Wails remains the authority for shared PTY dimensions;
			// every remote client receives dimensions frames and syncs locally.
			continue
		default:
			s.log.Debug("remote", "未知 WebSocket 消息类型", fmt.Sprintf("type=%s", msg.Type))
		}
	}
}
