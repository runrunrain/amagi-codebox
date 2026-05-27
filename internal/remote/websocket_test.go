package remote

import (
	"embed"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/structured"

	"github.com/gorilla/websocket"
)

type websocketTestApp struct {
	mu              sync.Mutex
	resizeCallbacks map[string]func(int, int)
	ptyResizeCalls  int
	inputWrites     chan string
}

func newWebsocketTestApp() *websocketTestApp {
	return &websocketTestApp{
		resizeCallbacks: make(map[string]func(int, int)),
		inputWrites:     make(chan string, 4),
	}
}

func (a *websocketTestApp) AttachSessionObserver(sessionID string, id string, outputCB func(data []byte), resizeCB func(cols, rows int)) ([]byte, int, int, error) {
	a.mu.Lock()
	a.resizeCallbacks[id] = resizeCB
	a.mu.Unlock()
	return []byte("history"), 132, 43, nil
}

func (a *websocketTestApp) DetachSessionObserver(sessionID string, id string) {
	a.mu.Lock()
	delete(a.resizeCallbacks, id)
	a.mu.Unlock()
}

func (a *websocketTestApp) emitResize(cols, rows int) {
	a.mu.Lock()
	callbacks := make([]func(int, int), 0, len(a.resizeCallbacks))
	for _, cb := range a.resizeCallbacks {
		callbacks = append(callbacks, cb)
	}
	a.mu.Unlock()

	for _, cb := range callbacks {
		cb(cols, rows)
	}
}

func (a *websocketTestApp) resizeCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ptyResizeCalls
}

func (a *websocketTestApp) PtyWrite(sessionID string, data string) error {
	a.inputWrites <- data
	return nil
}

func (a *websocketTestApp) PtyResize(sessionID string, cols, rows int) error {
	a.mu.Lock()
	a.ptyResizeCalls++
	a.mu.Unlock()
	return nil
}

func (a *websocketTestApp) RegisterOutputCallback(sessionID string, id string, cb func(data []byte)) {
}
func (a *websocketTestApp) UnregisterOutputCallback(sessionID string, id string) {}
func (a *websocketTestApp) RegisterExitCallback(sessionID string, id string, cb func(exitCode uint32)) {
}
func (a *websocketTestApp) UnregisterExitCallback(sessionID string, id string) {}
func (a *websocketTestApp) RegisterResizeCallback(sessionID string, id string, cb func(cols, rows int)) {
	a.mu.Lock()
	a.resizeCallbacks[id] = cb
	a.mu.Unlock()
}
func (a *websocketTestApp) UnregisterResizeCallback(sessionID string, id string) {
	a.mu.Lock()
	delete(a.resizeCallbacks, id)
	a.mu.Unlock()
}

func (a *websocketTestApp) GetAppInfo() map[string]any         { return map[string]any{} }
func (a *websocketTestApp) GetSessions() []session.SessionInfo { return nil }
func (a *websocketTestApp) GetSession(sessionID string) (session.SessionInfo, error) {
	return session.SessionInfo{}, errors.New("not implemented")
}
func (a *websocketTestApp) LaunchSession(providerName, presetName string, mode string, workDir string, useProxy bool, shellPath string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) LaunchCodexSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) LaunchOpenCode(providerName string, presetName string, mode string, workDir string, shellPath string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) StopSession(sessionID string) error   { return nil }
func (a *websocketTestApp) RemoveSession(sessionID string) error { return nil }
func (a *websocketTestApp) ClearStoppedSessions() int            { return 0 }
func (a *websocketTestApp) GetProvidersByType(providerType string) map[string]config.Provider {
	return nil
}
func (a *websocketTestApp) GetProviderExportJSON(providerName string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) SaveProviderFromJSON(providerName string, jsonStr string) error {
	return nil
}
func (a *websocketTestApp) SaveAllConfig() error                            { return nil }
func (a *websocketTestApp) GetKeyDiagnostics() map[string]map[string]string { return nil }
func (a *websocketTestApp) GetLogs(level string, source string, keyword string, limit int) []logging.Entry {
	return nil
}
func (a *websocketTestApp) GetSettingsService() *settings.Service   { return nil }
func (a *websocketTestApp) GetPathsService() *paths.PathsService    { return nil }
func (a *websocketTestApp) GetConfigService() *config.ConfigService { return nil }
func (a *websocketTestApp) SetRemotePort(port int) error            { return nil }

func TestWebSocketControllerReceivesDimensionsWithoutOwningResize(t *testing.T) {
	app := newWebsocketTestApp()
	srv := NewServer(0, app, logging.NewService(t.TempDir()), embed.FS{})
	t.Cleanup(srv.log.Close)

	httpServer := httptest.NewServer(srv.buildHandler())
	t.Cleanup(httpServer.Close)

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/terminal/session-1?token=" + url.QueryEscape(srv.GetToken()) + "&mode=controller"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	initial := readDimensionsFrame(t, conn)
	if initial.Cols != 132 || initial.Rows != 43 {
		t.Fatalf("initial dimensions = %dx%d, want 132x43", initial.Cols, initial.Rows)
	}

	app.emitResize(120, 35)
	live := readDimensionsFrame(t, conn)
	if live.Cols != 120 || live.Rows != 35 {
		t.Fatalf("live dimensions = %dx%d, want 120x35", live.Cols, live.Rows)
	}

	if err := conn.WriteJSON(clientMsg{Type: "resize", Cols: 88, Rows: 24}); err != nil {
		t.Fatalf("write resize frame: %v", err)
	}
	if err := conn.WriteJSON(clientMsg{Type: "input", Data: "abc"}); err != nil {
		t.Fatalf("write input frame: %v", err)
	}

	select {
	case <-app.inputWrites:
	case <-time.After(time.Second):
		t.Fatal("server did not process input after resize frame")
	}

	if got := app.resizeCallCount(); got != 0 {
		t.Fatalf("remote resize should not call PtyResize, got %d calls", got)
	}
}

func TestServerMsgSerializesStructuredPartFrame(t *testing.T) {
	part := structured.Classify([]byte("# Plan\n\n- inspect"), 7)
	raw, err := json.Marshal(serverMsg{Type: "structured-part", Seq: 7, Part: &part})
	if err != nil {
		t.Fatalf("marshal structured-part frame: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal structured-part frame: %v", err)
	}
	if decoded["type"] != "structured-part" {
		t.Fatalf("type = %v, want structured-part", decoded["type"])
	}
	if decoded["seq"] != float64(7) {
		t.Fatalf("seq = %v, want 7", decoded["seq"])
	}
	partValue, ok := decoded["part"].(map[string]any)
	if !ok {
		t.Fatalf("part missing or wrong type: %#v", decoded["part"])
	}
	if partValue["type"] != "markdown" {
		t.Fatalf("part.type = %v, want markdown", partValue["type"])
	}
}

func TestServerMsgSerializesOutputCompatibilityFields(t *testing.T) {
	raw, err := json.Marshal(serverMsg{Type: "output", Data: "YWJj", Seq: 11, StructuredExpected: true})
	if err != nil {
		t.Fatalf("marshal output frame: %v", err)
	}
	jsonText := string(raw)
	for _, fragment := range []string{`"type":"output"`, `"data":"YWJj"`, `"seq":11`, `"structuredExpected":true`} {
		if !strings.Contains(jsonText, fragment) {
			t.Fatalf("output frame %s missing fragment %s", jsonText, fragment)
		}
	}
}

func readDimensionsFrame(t *testing.T, conn *websocket.Conn) serverMsg {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}

		var msg serverMsg
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read websocket frame: %v", err)
		}
		if msg.Type == "dimensions" {
			return msg
		}
	}
	t.Fatal("timed out waiting for dimensions frame")
	return serverMsg{}
}
