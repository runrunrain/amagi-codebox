package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/aiproxy"
)

// Explicit opt-in bridge for the real Vue page and real desktop facade.
// All application state and v2rayN records are synthetic and isolated.
func TestAIProxyBrowserBridge(t *testing.T) {
	if os.Getenv("AI_PROXY_BROWSER_BRIDGE") != "1" {
		t.Skip("opt-in real Go / Vue browser bridge")
	}
	dir := t.TempDir()
	s := aiproxy.New(filepath.Join(dir, "ai-proxy"))
	defer s.Close()
	dbPath := filepath.Join(dir, "guiNDB.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`CREATE TABLE SubItem (Id TEXT, Remarks TEXT, Url TEXT)`, `INSERT INTO SubItem VALUES ('fixture','v2rayN 测试订阅','https://example.invalid/private-token')`, `CREATE TABLE ProfileItem (ConfigType INTEGER, Address TEXT, Port INTEGER, Password TEXT, Username TEXT, Remarks TEXT, Subid TEXT)`, `INSERT INTO ProfileItem VALUES (4,'127.0.0.1',18001,'private-pass','user','Claude 测试出口','fixture')`, `INSERT INTO ProfileItem VALUES (4,'127.0.0.1',18002,'private-pass','user','OpenAI 测试出口','fixture')`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	settings := s.State().Settings
	settings.V2rayNDatabase = dbPath
	settings.CorePath = os.Getenv("SING_BOX_TEST_BINARY")
	var listeners []net.Listener
	ports := []int{}
	for range 3 {
		ln, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		listeners = append(listeners, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range listeners {
		ln.Close()
	}
	settings.Routing.ProxyPort, settings.Routing.Rules[0].Port, settings.Routing.Rules[1].Port = ports[0], ports[1], ports[2]
	if err = s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := &App{aiProxy: s}
	finish := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:5188")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/finish" {
			select {
			case finish <- struct{}{}:
			default:
			}
			json.NewEncoder(w).Encode("finished")
			return
		}
		var input struct {
			Method string            `json:"method"`
			Args   []json.RawMessage `json:"args"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid input", 400)
			return
		}
		var result aiproxy.State
		var err error
		switch input.Method {
		case "GetAIProxyState":
			result, err = app.GetAIProxyState()
		case "SaveAIProxySettings":
			var v aiproxy.Settings
			if len(input.Args) != 1 || json.Unmarshal(input.Args[0], &v) != nil {
				http.Error(w, "invalid settings", 400)
				return
			}
			result, err = app.SaveAIProxySettings(v)
		case "StartAIProxy":
			result, err = app.StartAIProxy()
		case "StopAIProxy":
			result, err = app.StopAIProxy()
		case "SyncAIProxyV2rayN":
			result, err = app.SyncAIProxyV2rayN()
		case "ImportAIProxySubscription":
			var values [3]string
			if len(input.Args) != 3 {
				http.Error(w, "invalid import", 400)
				return
			}
			for i := range values {
				if json.Unmarshal(input.Args[i], &values[i]) != nil {
					http.Error(w, "invalid import", 400)
					return
				}
			}
			result, err = app.ImportAIProxySubscription(values[0], values[1], values[2])
		case "DeleteAIProxySubscription", "RefreshAIProxySubscription":
			var id string
			if len(input.Args) != 1 || json.Unmarshal(input.Args[0], &id) != nil {
				http.Error(w, "invalid group", 400)
				return
			}
			if input.Method == "DeleteAIProxySubscription" {
				result, err = app.DeleteAIProxySubscription(id)
			} else {
				result, err = app.RefreshAIProxySubscription(id)
			}
		default:
			http.Error(w, "unknown method", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	fmt.Println("AI_PROXY_BROWSER_BRIDGE=" + server.URL)
	select {
	case <-finish:
	case <-time.After(15 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
