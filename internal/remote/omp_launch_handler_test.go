package remote

// TestHandleLaunchOmpSmoke 冒烟：POST /api/sessions/launch-omp 的 body 校验与
// app.LaunchOmpSession -> GetSession 回写链路（复刻 handleLaunchPi 的行为模式；
// launch-pi 本身无 handler 级测试，此处按 diting MINOR-3 建议补 omp 的最小冒烟）。

import (
	"bytes"
	"crypto/rand"
	"embed"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/session"
)

// ompLaunchSpyApp 记录 LaunchOmpSession 调用并提供可回写的 GetSession。
// 内嵌 b2aSpyApp 以满足 AppInterface 其余方法（本测试只关心 launch 链路）。
type ompLaunchSpyApp struct {
	*b2aSpyApp
	launchCalls  int
	lastModel    string
	lastProvider string
	lastMode     string
	lastWorkDir  string
	sessionInfo  session.SessionInfo
}

func (a *ompLaunchSpyApp) LaunchOmpSession(modelName, providerID, mode, workDir, shellPath string) (string, error) {
	a.launchCalls++
	a.lastModel = modelName
	a.lastProvider = providerID
	a.lastMode = mode
	a.lastWorkDir = workDir
	return "sess-omp-1", nil
}

func (a *ompLaunchSpyApp) GetSession(id string) (session.SessionInfo, error) {
	return a.sessionInfo, nil
}

// newOmpLaunchServer 构造带 omp spy app 的安全 Server（对标 newSecServer）。
func newOmpLaunchServer(t *testing.T, app *ompLaunchSpyApp) *Server {
	t.Helper()
	clk := newSecFakeClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	opts := newSecurityOptions(t.TempDir(), validHostSummary, clk, rand.Reader, NewVolatileSecurityEventSink())
	srv := NewServerWithSecurity(0, app, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	if err := srv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	return srv
}

func TestHandleLaunchOmpSmoke(t *testing.T) {
	spy := &ompLaunchSpyApp{b2aSpyApp: &b2aSpyApp{}, sessionInfo: session.SessionInfo{ID: "sess-omp-1", AppType: "omp"}}
	srv := newOmpLaunchServer(t, spy)

	// 正常路径：合法 body -> LaunchOmpSession 调用 -> GetSession 回写。
	body := `{"modelName":"glm-5","providerID":"glm","mode":"embedded","workDir":"/tmp","shellPath":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/launch-omp", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.handleLaunchOmp(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if spy.launchCalls != 1 {
		t.Fatalf("LaunchOmpSession calls = %d, want 1", spy.launchCalls)
	}
	if spy.lastModel != "glm-5" || spy.lastProvider != "glm" || spy.lastMode != "embedded" || spy.lastWorkDir != "/tmp" {
		t.Errorf("launch args = (%q,%q,%q,%q), want (glm-5,glm,embedded,/tmp)",
			spy.lastModel, spy.lastProvider, spy.lastMode, spy.lastWorkDir)
	}

	// 非法 body -> 400，不触达 app。
	bad := httptest.NewRequest(http.MethodPost, "/api/sessions/launch-omp", bytes.NewBufferString(`{not-json`))
	rr2 := httptest.NewRecorder()
	srv.handleLaunchOmp(rr2, bad)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("bad-body status = %d, want 400", rr2.Code)
	}
	if spy.launchCalls != 1 {
		t.Fatalf("LaunchOmpSession calls after bad body = %d, want still 1", spy.launchCalls)
	}
}
