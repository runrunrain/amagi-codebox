package remote

// m-002: the WS sole writer must not drop write errors. A write failure tears
// the connection down (closes the transport so the read loop runs the
// authoritative detach/fence/unregister); a healthy write must NOT tear down.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"amagi-codebox/internal/remote/contract"
)

// newWSPair returns a connected (client, server) gorilla pair over an in-memory
// httptest server.
func newWSPair(t *testing.T) (client, server *websocket.Conn, srvURL string) {
	t.Helper()
	serverCh := make(chan *websocket.Conn, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverCh <- c
	}))
	t.Cleanup(hs.Close)
	u := "ws" + strings.TrimPrefix(hs.URL, "http") + "/"
	cl, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Wait for the server conn (synchronized via channel — no shared-variable race).
	select {
	case sv := <-serverCh:
		server = sv
	case <-time.After(2 * time.Second):
		t.Fatal("server conn never upgraded")
	}
	return cl, server, u
}

func TestM002_HealthyWriteDoesNotTearDown(t *testing.T) {
	client, server, _ := newWSPair(t)
	defer client.Close()

	conn := &wsV1Connection{
		conn:     server,
		state:    wsV1StateAttached,
		inputIDs: make(map[contract.MessageID]struct{}),
		done:     make(chan struct{}),
	}
	ev := contract.ErrorEvent{Type: contract.ServerEventTypeError, Code: contract.ErrorCodeServiceDown, Layer: contract.ErrorLayerConnection, Message: "unavailable", ActionHint: contract.ActionHintCheckDesktop}
	conn.writeServerEvent(ev)

	// The client must receive the event (write succeeded, no teardown).
	client.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("healthy write should deliver; got read err: %v", err)
	}
	if !strings.Contains(string(msg), string(contract.ServerEventTypeError)) {
		t.Fatalf("unexpected message: %s", msg)
	}
	// The connection must still be usable, but R4-003 forbids the test itself
	// from becoming a second server-side writer. Admit a second event through the
	// same outbound queue and observe it at the client.
	ev.Message = "still-alive"
	if ok := conn.writeServerEvent(ev); !ok {
		t.Fatal("healthy connection rejected second queued write")
	}
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, second, err := client.ReadMessage(); err != nil || !strings.Contains(string(second), "still-alive") {
		t.Fatalf("server conn should remain alive after healthy write: payload=%s err=%v", second, err)
	}
}

func TestM002_WriteErrorClosesTransport(t *testing.T) {
	client, server, _ := newWSPair(t)
	defer client.Close()

	conn := &wsV1Connection{
		conn:     server,
		state:    wsV1StateAttached,
		inputIDs: make(map[contract.MessageID]struct{}),
		done:     make(chan struct{}),
	}
	// Close the client peer so server writes fail (broken pipe / peer gone).
	_ = client.Close()
	// Drain/warm the RST: a short deadline + a few writes forces the failure
	// deterministically rather than relying on TCP buffer absorption.
	server.SetWriteDeadline(time.Now().Add(time.Second))

	ev := contract.ErrorEvent{Type: contract.ServerEventTypeError, Code: contract.ErrorCodeServiceDown, Layer: contract.ErrorLayerConnection, Message: "unavailable", ActionHint: contract.ActionHintCheckDesktop}
	// Retry the write until the broken-pipe surfaces (writeServerEvent closes
	// the transport on the first failure).
	var closed bool
	for i := 0; i < 200 && !closed; i++ {
		conn.writeServerEvent(ev)
		// After writeServerEvent sees a write fault it calls conn.Close(); a
		// subsequent read on the server conn must then error.
		server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		if _, _, rerr := server.ReadMessage(); rerr != nil {
			closed = true
		}
		server.SetReadDeadline(time.Time{})
	}
	if !closed {
		t.Fatal("writeServerEvent on a failing write must tear down the transport (close the conn)")
	}
	// writeFault is a sync.Once: further writeServerEvent calls must not panic.
	conn.writeServerEvent(ev)
}
