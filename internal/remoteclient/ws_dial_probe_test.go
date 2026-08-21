// ws_dial_probe_test.go —— T0-3（核验任务 C-T02）WS 库选型核验的回环证明。
//
// 结论前置：vendor 内 github.com/gorilla/websocket v1.5.3 已是 go.mod 直接
// 依赖（服务端 internal/remote/websocket.go 用其 Upgrader），同库 client.go
// 提供完整客户端能力（Dialer.Dial / Dialer.DialContext），可复用为远程客户端
// 传输，无需新增任何依赖。本测试做最小回环验证：httptest + Upgrader 起假
// 宿主 → 客户端 Dialer 拨号 → 文本帧收发回显。测试保留作为 vendor 升级时
// 「客户端能力仍在」的回归锚点。
package remoteclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWSClientCapabilityProbe(t *testing.T) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close() //nolint:errcheck // 测试回环，尽力关闭
		mt, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		_ = c.WriteMessage(mt, msg) // 回显
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	dialer := websocket.Dialer{} // 客户端能力锚点：同库 Dialer
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dialer.Dial 失败（客户端能力不成立）: %v (resp=%v)", err, resp)
	}
	defer conn.Close() //nolint:errcheck // 测试回环，尽力关闭

	const probe = "t0-3-dialer-probe"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(probe)); err != nil {
		t.Fatalf("客户端写帧失败: %v", err)
	}
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("客户端读帧失败: %v", err)
	}
	if string(got) != probe {
		t.Fatalf("回显不匹配: got %q, want %q", got, probe)
	}
}
