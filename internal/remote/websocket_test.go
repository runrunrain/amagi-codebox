package remote

import (
	"embed"
	"encoding/json"
	"errors"
	"net/http"
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
	mu             sync.Mutex
	ptyResizeCalls int
	inputWrites    chan string
}

func newWebsocketTestApp() *websocketTestApp {
	return &websocketTestApp{
		inputWrites: make(chan string, 4),
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

func (a *websocketTestApp) GetAppInfo() map[string]any         { return map[string]any{} }
func (a *websocketTestApp) GetSessions() []session.SessionInfo { return nil }
func (a *websocketTestApp) GetSession(sessionID string) (session.SessionInfo, error) {
	return session.SessionInfo{}, errors.New("not implemented")
}
func (a *websocketTestApp) LaunchSession(providerName, presetName string, mode string, workDir string, useProxy bool, useHeadroom bool, shellPath string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) LaunchCodexSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) LaunchOpenCode(providerName string, presetName string, mode string, workDir string, shellPath string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) LaunchPiSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
	return "", errors.New("not implemented")
}
func (a *websocketTestApp) LaunchOmpSession(modelName string, providerID string, mode string, workDir string, shellPath string) (string, error) {
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

// TestWebSocketLegacyInputOnlyNoOutputBypass (M-003): the legacy /ws/terminal
// path is INPUT-ONLY. It must NOT deliver output/history/dimensions/exit frames
// (the naked-SessionID callback bypass is removed; all PTY output/exit flows
// through the run-scoped projector). Input (PtyWrite) is still dispatched, and a
// remote resize frame does not call PtyResize (desktop remains geometry owner).
func TestWebSocketLegacyInputOnlyNoOutputBypass(t *testing.T) {
	app := newWebsocketTestApp()
	srv := NewServer(0, app, logging.NewService(t.TempDir()), embed.FS{})
	srv.SetPtyBridge(app) // M3-A2: unbound PTY bridge (design §8.6.3)
	t.Cleanup(srv.log.Close)

	httpServer := httptest.NewServer(srv.buildHandler())
	t.Cleanup(httpServer.Close)

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/terminal/session-1?token=" + url.QueryEscape(srv.GetToken()) + "&mode=controller"
	hdr := http.Header{}
	hdr.Set("Origin", httpServer.URL)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// M-003: no output/dimensions/history frame should arrive — the bypass is gone.
	// A short read deadline must time out (no server→client data frames).
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var msg serverMsg
	if err := conn.ReadJSON(&msg); err == nil {
		t.Fatalf("M-003 regression: legacy path delivered a %q frame (output bypass not removed)", msg.Type)
	}

	// Input is still dispatched (legacy loopback compat).
	if err := conn.WriteJSON(clientMsg{Type: "input", Data: "abc"}); err != nil {
		t.Fatalf("write input frame: %v", err)
	}
	select {
	case got := <-app.inputWrites:
		if got != "abc" {
			t.Fatalf("input data = %q, want abc", got)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not process input")
	}

	// A resize frame must not call PtyResize (desktop owns geometry).
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatalf("reset read deadline: %v", err)
	}
	_ = conn.WriteJSON(clientMsg{Type: "resize", Cols: 88, Rows: 24})
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

func readNextFrame(t *testing.T, conn *websocket.Conn) serverMsg {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var msg serverMsg
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket frame: %v", err)
	}
	return msg
}
