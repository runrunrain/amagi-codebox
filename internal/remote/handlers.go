package remote

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// registerRoutes 注册所有 REST API 路由。
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// App info
	mux.HandleFunc("GET /api/info", s.handleGetInfo)

	// Sessions
	mux.HandleFunc("GET /api/sessions", s.handleGetSessions)
	mux.HandleFunc("POST /api/sessions/launch", s.handleLaunchSession)
	mux.HandleFunc("POST /api/sessions/launch-codex", s.handleLaunchCodex)
	mux.HandleFunc("POST /api/sessions/launch-opencode", s.handleLaunchOpenCode)
	mux.HandleFunc("POST /api/sessions/clear-stopped", s.handleClearStopped)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleStopSession)
	mux.HandleFunc("POST /api/sessions/{id}/resize", s.handleResizeSession)
	mux.HandleFunc("DELETE /api/sessions/{id}/remove", s.handleRemoveSession)

	// Providers
	mux.HandleFunc("GET /api/providers", s.handleGetProviders)
	mux.HandleFunc("GET /api/providers/{name}", s.handleGetProvider)
	mux.HandleFunc("PUT /api/providers/{name}", s.handleSaveProvider)
	mux.HandleFunc("GET /api/providers-by-type/{type}", s.handleGetProvidersByType)

	// Config
	mux.HandleFunc("POST /api/config/save", s.handleSaveConfig)

	// Secrets
	mux.HandleFunc("GET /api/secrets/diagnostics", s.handleGetDiagnostics)

	// Settings
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)

	// Logs
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)

	// Paths
	mux.HandleFunc("GET /api/paths", s.handleGetPaths)

	// WebSocket terminal (no auth middleware here - handled inside handler via token param)
	mux.HandleFunc("/ws/terminal/{sessionID}", s.handleWebSocketTerminal)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// --- handlers ---

func (s *Server) handleGetInfo(w http.ResponseWriter, r *http.Request) {
	info := s.app.GetAppInfo()
	info["remotePort"] = s.port
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleGetSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.GetSessions())
}

func (s *Server) handleLaunchSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderName string `json:"providerName"`
		PresetName   string `json:"presetName"`
		Mode         string `json:"mode"`
		WorkDir      string `json:"workDir"`
		UseProxy     bool   `json:"useProxy"`
		ShellPath    string `json:"shellPath"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	id, err := s.app.LaunchSession(body.ProviderName, body.PresetName, body.Mode, body.WorkDir, body.UseProxy, body.ShellPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info, err := s.app.GetSession(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleLaunchCodex(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ModelName  string `json:"modelName"`
		ProviderID string `json:"providerID"`
		Mode       string `json:"mode"`
		WorkDir    string `json:"workDir"`
		ShellPath  string `json:"shellPath"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	id, err := s.app.LaunchCodexSession(body.ModelName, body.ProviderID, body.Mode, body.WorkDir, body.ShellPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info, err := s.app.GetSession(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleLaunchOpenCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderName string `json:"providerName"`
		Mode         string `json:"mode"`
		WorkDir      string `json:"workDir"`
		ShellPath    string `json:"shellPath"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	id, err := s.app.LaunchOpenCode(body.ProviderName, body.Mode, body.WorkDir, body.ShellPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info, err := s.app.GetSession(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.app.StopSession(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResizeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := s.app.PtyResize(id, body.Cols, body.Rows); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRemoveSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.app.RemoveSession(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClearStopped(w http.ResponseWriter, r *http.Request) {
	count := s.app.ClearStoppedSessions()
	writeJSON(w, http.StatusOK, map[string]int{"cleared": count})
}

// providerSummary is the JSON shape expected by the mobile frontend.
type providerSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
}

func (s *Server) handleGetProviders(w http.ResponseWriter, r *http.Request) {
	raw := s.app.GetConfigService().GetProviders()
	out := make([]providerSummary, 0, len(raw))
	for name, p := range raw {
		out = append(out, providerSummary{
			ID:      name,
			Name:    name,
			Type:    p.Type,
			BaseURL: p.BaseURL,
			Model:   p.DefaultModel,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	jsonStr, err := s.app.GetProviderExportJSON(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(jsonStr))
}

func (s *Server) handleSaveProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.app.SaveProviderFromJSON(name, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetProvidersByType(w http.ResponseWriter, r *http.Request) {
	provType := r.PathValue("type")
	raw := s.app.GetProvidersByType(provType)
	out := make([]providerSummary, 0, len(raw))
	for name, p := range raw {
		out = append(out, providerSummary{
			ID:      name,
			Name:    name,
			Type:    p.Type,
			BaseURL: p.BaseURL,
			Model:   p.DefaultModel,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if err := s.app.SaveAllConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetDiagnostics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.GetKeyDiagnostics())
}

// remoteSettingsData is the settings shape expected by the mobile frontend.
type remoteSettingsData struct {
	RemotePort  int    `json:"remotePort"`
	RemoteToken string `json:"remoteToken"`
	AutoStart   bool   `json:"autoStart"`
	LogLevel    string `json:"logLevel"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, remoteSettingsData{
		RemotePort:  s.port,
		RemoteToken: s.auth.GetToken(),
		AutoStart:   false,
		LogLevel:    "info",
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RemotePort *int `json:"remotePort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RemotePort != nil {
		port := *req.RemotePort
		// 持久化到 settings.json
		settingsSvc := s.app.GetSettingsService()
		if err := settingsSvc.SetRemotePort(port); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 立即应用：停止当前服务器、切换端口、重启
		if err := s.app.SetRemotePort(port); err != nil {
			writeError(w, http.StatusInternalServerError, "port saved but failed to apply: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "settings updated and applied"})
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	level := q.Get("level")
	source := q.Get("source")
	keyword := q.Get("keyword")
	limitStr := q.Get("limit")
	limit := 100
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, s.app.GetLogs(level, source, keyword, limit))
}

func (s *Server) handleGetPaths(w http.ResponseWriter, r *http.Request) {
	pathSvc := s.app.GetPathsService()
	entries := pathSvc.GetPaths()
	paths := make([]string, 0, len(entries)+1)
	if dp := pathSvc.GetDefaultPath(); dp != "" {
		paths = append(paths, dp)
	}
	for _, e := range entries {
		if e.Path != "" && e.Path != pathSvc.GetDefaultPath() {
			paths = append(paths, e.Path)
		}
	}
	writeJSON(w, http.StatusOK, paths)
}

// handleWebSocketTerminal WebSocket 端点，认证通过 URL 参数 ?token=xxx。
// 注意：此 handler 不经过全局 Auth Middleware，需自行验证。
func (s *Server) handleWebSocketTerminal(w http.ResponseWriter, r *http.Request) {
	// 单独验证 token（支持 URL 参数）
	if !s.auth.validate(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}

	sessionID := r.PathValue("sessionID")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "missing sessionID")
		return
	}

	// 去掉可能的前缀斜杠
	sessionID = strings.TrimPrefix(sessionID, "/")

	s.serveWebSocket(w, r, sessionID)
}
