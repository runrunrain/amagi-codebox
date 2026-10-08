//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/piconfig"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/webui"
)

// Optional browser bridge: all feature RPCs use real bound services and the
// desktop LaunchPiSession -> real PTY path. Only the Pi executable/credentials
// are synthetic isolation fixtures. No app, auth or user config is read.
// Run PI_LOGIN_BROWSER_BRIDGE=1 go test . -run TestPiLoginBrowserBridge -v,
// then open e2e/pi-login.html through Vite. POST /finish ends this test.
func TestPiLoginBrowserBridge(t *testing.T) {
	if os.Getenv("PI_LOGIN_BROWSER_BRIDGE") != "1" {
		t.Skip("opt-in browser fixture bridge")
	}
	isolatedHome(t)
	app, dir := newTestAppWithConfigDir(t)
	app.ctx = t.Context()
	app.Capabilities = platform.CurrentCapabilities()
	app.Settings = settings.NewService(dir)
	if err := app.Settings.Load(); err != nil {
		t.Fatal(err)
	}
	app.processRegistry = processcap.NewRegistry()
	wireTestControl(t, app)
	app.WebUI = webui.NewService(app.Log, t.TempDir())
	app.PiConfig = piconfig.NewService()
	_, key, auth := piLoginFixture(t, app.Config)
	if err := app.Config.DeletePiLoginPreset(key); err != nil {
		t.Fatal(err)
	}
	if err := app.Config.SaveProvider("openai-codex", config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true}, DefaultModel: "api-namesake"}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	trace := filepath.Join(bin, "trace")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PI_LOGIN_FIXTURE_TRACE\"\nexec /bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PI_LOGIN_FIXTURE_TRACE", trace)
	app.CLIResolver = platform.NewCLIResolver(app.Capabilities)
	finish := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:5187")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		var result any
		var err error
		switch r.URL.Path {
		case "/finish":
			finish <- struct{}{}
			result = "finished"
		case "/logout":
			err = os.WriteFile(auth, []byte(`{}`), 0600)
			result = "logged out"
		case "/trace":
			data, e := os.ReadFile(trace)
			err = e
			result = string(data)
		case "/rpc":
			var req struct {
				Method string            `json:"method"`
				Args   []json.RawMessage `json:"args"`
			}
			if json.NewDecoder(r.Body).Decode(&req) != nil {
				http.Error(w, "invalid test RPC", 400)
				return
			}
			str := func(i int) string {
				var s string
				if i < len(req.Args) {
					_ = json.Unmarshal(req.Args[i], &s)
				}
				return s
			}
			switch req.Method {
			case "GetPiLoginProviders":
				result, err = app.PiConfig.GetPiLoginProviders()
			case "GetPiLoginPresets":
				result, err = app.Config.GetPiLoginPresets()
			case "SavePiLoginPreset":
				var p config.PiLoginPreset
				err = json.Unmarshal(req.Args[0], &p)
				if err == nil {
					err = app.Config.SavePiLoginPreset(p)
				}
			case "DeletePiLoginPreset":
				err = app.Config.DeletePiLoginPreset(str(0))
			case "GetProviders":
				result = app.Config.GetProviders()
			case "GetProvidersByType":
				result = app.GetProvidersByType(str(0))
			case "HasAPIKey":
				result = app.Secrets.HasAPIKey(str(0))
			case "GetMergedTerminalPresets":
				result, err = app.Config.GetMergedTerminalPresets(str(0))
			case "GetOpenCodePresets":
				result = app.Config.GetOpenCodePresets()
			case "GetPlatformCapabilities":
				result = app.GetPlatformCapabilities()
			case "GetSavedWorkDirs":
				result, err = app.GetSavedWorkDirs()
			case "GetDashboardDefaults":
				result = app.Settings.GetDashboardDefaults()
			case "SetDashboardDefaults":
				var d settings.DashboardDefaults
				err = json.Unmarshal(req.Args[0], &d)
				if err == nil {
					err = app.Settings.SetDashboardDefaults(d)
				}
			case "LaunchPiSession":
				result, err = app.LaunchPiSession(str(0), str(1), str(2), bin, str(4))
			default:
				http.Error(w, "RPC not part of fixture: "+req.Method, 501)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	fmt.Printf("PI_LOGIN_BROWSER_URL=%s\n", server.URL)
	if err := os.WriteFile("/tmp/pi-login-browser-url", []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, s := range app.Sessions.List() {
			_ = app.Pty.Close(s.ID)
		}
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline := time.After(170 * time.Second)
	for {
		select {
		case <-finish:
			return
		case <-ticker.C:
			fmt.Println("Pi login browser fixture ready")
		case <-deadline:
			t.Fatal("browser fixture timed out")
		}
	}
}
