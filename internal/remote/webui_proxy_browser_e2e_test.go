//go:build browser_e2e

package remote

// webui_proxy_browser_e2e_test.go — 手动浏览器端到端验证工具（G2 修复批，
// Critical-01 复审用；build tag browser_e2e，CI 默认不编译不运行）。
//
// 用途：起一个带完整 v1 安全面（device cookie 配对）的真实监听 Server，
// 供 Leader 用外部浏览器/curl 验证「带真实 T1 鉴权面」的 Web 平面链路
//（预检豁免 → cookie 鉴权代理 → 后端），弥补 G1 用 Director 等价物绕过
// 鉴权层的方法学缺口。
//
// 用法（仓库根目录）：
//
//	# A. 后端指向真实 pi webui server（推荐，验证完整链路）：
//	WEBUI_E2E_BACKEND_PORT=5177 WEBUI_E2E_BACKEND_TOKEN=<pi webui capability token> \
//	  go test ./internal/remote/ -run TestWebUIProxyBrowserE2E -tags browser_e2e -v -timeout 10m
//
//	# B. 不注入后端：自动起内置 fake backend（127.0.0.1 随机端口）：
//	go test ./internal/remote/ -run TestWebUIProxyBrowserE2E -tags browser_e2e -v -timeout 10m
//
// 可选环境变量：
//	WEBUI_E2E_PORT   监听端口（默认 8644，监听 0.0.0.0 供局域网真机访问）
//	WEBUI_E2E_HOLD   保持运行秒数（默认 90；期间用打印的 cookie/URL 手动验证）
//	WEBUI_E2E_SID    平面会话 ID（默认 "e2e"）
//
// 验证清单（打印在测试输出中）：
//	1) CORS 预检豁免（无 cookie）：
//	   curl -i -X OPTIONS http://127.0.0.1:8644/webui/e2e/api/info \
//	     -H 'Origin: null' -H 'Access-Control-Request-Method: POST'
//	   预期 204 + ACAO:null + Allow-Credentials:true。
//	2) 真实浏览器：注入 device cookie 后打开入口 URL（打印值），
//	   sandbox iframe 内平面页面的 fetch/WS 全部走 /webui/e2e/*。
//	3) 无控制权写面（观察者）：POST /api/agent-interact → 403。
//
// hold 结束后本测试做最小自检（代理入口可达 + 预检豁免头）后退出；
// 自检失败即测试失败。

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

func TestWebUIProxyBrowserE2E(t *testing.T) {
	// ------------------------------------------------------------------
	// 1. Resolve configuration (env-injectable backend target).
	// ------------------------------------------------------------------
	port := envInt(t, "WEBUI_E2E_PORT", 8644)
	hold := envInt(t, "WEBUI_E2E_HOLD", 90)
	sid := envString("WEBUI_E2E_SID", "e2e")

	backendPort := envInt(t, "WEBUI_E2E_BACKEND_PORT", 0)
	backendToken := envString("WEBUI_E2E_BACKEND_TOKEN", "")
	var backendHostPort string
	if backendPort > 0 {
		backendHostPort = net.JoinHostPort("127.0.0.1", strconv.Itoa(backendPort))
	} else {
		// Internal fake backend (from webui_proxy_test.go; same package).
		fb := newWebuiFakeBackend(t)
		backendPort = fb.backendPort()
		backendToken = webuiTestToken
		backendHostPort = net.JoinHostPort("127.0.0.1", strconv.Itoa(backendPort))
	}

	// ------------------------------------------------------------------
	// 2. Full security server (fixture mode) + one paired device.
	// ------------------------------------------------------------------
	app := &webuiProxyTestApp{b2aSpyApp: &b2aSpyApp{}}
	app.setInfo(sid, SessionWebUIInfo{State: "available", Port: backendPort, Token: backendToken})
	clk := newSecFakeClock(time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC))
	opts := newSecurityOptions(t.TempDir(), validHostSummary, clk, rand.Reader, NewVolatileSecurityEventSink())
	srv := NewServerWithSecurity(port, app, logging.NewService(t.TempDir()), embed.FS{}, opts)
	t.Cleanup(srv.log.Close)
	if err := srv.LoadSecurityState(); err != nil {
		t.Fatalf("LoadSecurityState: %v", err)
	}
	srv.pairing.Resume()
	code := openWindow(t, srv)

	// Real listener on 0.0.0.0 (LAN-reachable for real-device checks).
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatalf("listen 0.0.0.0:%d: %v (可用 WEBUI_E2E_PORT 换端口)", port, err)
	}
	httpSrv := &http.Server{Handler: srv.buildHandler()}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	// Pair over the real listener (Origin/Host match the live address).
	pairURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	preq, _ := http.NewRequest(http.MethodPost, pairURL+contract.RESTBasePath+"/pairing/complete",
		strings.NewReader(fmt.Sprintf(`{"code":%q,"deviceName":"browser-e2e"}`, code)))
	preq.Header.Set("Content-Type", "application/json")
	preq.Header.Set("Origin", pairURL)
	presp, err := http.DefaultClient.Do(preq)
	if err != nil || presp.StatusCode != http.StatusCreated {
		t.Fatalf("pair over real listener: err=%v status=%v", err, presp)
	}
	var cookie *http.Cookie
	for _, c := range presp.Cookies() {
		if c.Name == "amagi_codebox_device" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("pairing did not set the device cookie")
	}

	// ------------------------------------------------------------------
	// 3. Print the manual-verification recipe and hold.
	// ------------------------------------------------------------------
	// G3：入口 URL（含 per-device plane token）从状态端点获取（device cookie
	// 鉴权，与移动端宿主页同一流程）；raw 后端 token 不再直接下发。
	stReq, _ := http.NewRequest(http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d%s", port, webuiStatusPath(sid)), nil)
	stReq.Host = fmt.Sprintf("127.0.0.1:%d", port)
	stReq.Header.Set("Origin", fmt.Sprintf("http://127.0.0.1:%d", port))
	stReq.AddCookie(cookie)
	stResp, err := http.DefaultClient.Do(stReq)
	if err != nil || stResp.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint: err=%v status=%v", err, stResp)
	}
	var st contract.WebUIStatus
	if err := json.NewDecoder(stResp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	stResp.Body.Close()
	entryURL := fmt.Sprintf("http://127.0.0.1:%d%s", port, st.URL)
	t.Logf("========== webui 平面手动端到端（G2 Critical-01 复审） ==========")
	t.Logf("后端目标      : http://%s (token=%q)", backendHostPort, backendToken)
	t.Logf("监听          : 0.0.0.0:%d (局域网设备用本机 LAN IP 替换 127.0.0.1)", port)
	t.Logf("device cookie : %s=%s", cookie.Name, cookie.Value)
	t.Logf("平面入口 URL  : %s", entryURL)
	t.Logf("")
	t.Logf("[1] 预检豁免（无 cookie，预期 204 + ACAO:null + Allow-Credentials:true）:")
	t.Logf("    curl -i -X OPTIONS http://127.0.0.1:%d/webui/%s/api/info \\", port, sid)
	t.Logf("      -H 'Origin: null' -H 'Access-Control-Request-Method: POST'")
	t.Logf("[2] 真实浏览器：开发者工具注入 cookie 后打开入口 URL：")
	t.Logf("    document.cookie = \"%s=%s; path=/\"", cookie.Name, cookie.Value)
	t.Logf("    然后访问 %s", entryURL)
	t.Logf("    （或 curl -i --cookie '%s=%s' %s）", cookie.Name, cookie.Value, entryURL)
	t.Logf("[3] 观察者写面（预期 403 control.forbidden，未持控制权；本地错误带 ACAO:null，真实错误码可读）:")
	t.Logf("    curl -i -X POST http://127.0.0.1:%d/webui/%s/api/agent-interact \\", port, sid)
	t.Logf("      -H 'Origin: null' --cookie '%s=%s' -d '{}'", cookie.Name, cookie.Value)
	t.Logf("==============================================================")
	t.Logf("保持运行 %d 秒（WEBUI_E2E_HOLD 调整）……", hold)
	time.Sleep(time.Duration(hold) * time.Second)

	// ------------------------------------------------------------------
	// 4. Minimal self-check after the hold: proxy entry reachable (with
	//    cookie) + preflight exemption headers (without cookie).
	// ------------------------------------------------------------------
	entryReq, _ := http.NewRequest(http.MethodGet, entryURL, nil)
	entryReq.AddCookie(cookie)
	entryResp, err := http.DefaultClient.Do(entryReq)
	if err != nil {
		t.Fatalf("self-check: proxy entry unreachable: %v", err)
	}
	entryResp.Body.Close()
	if entryResp.StatusCode != http.StatusOK {
		t.Fatalf("self-check: proxy entry status=%d want 200", entryResp.StatusCode)
	}

	pfReq, _ := http.NewRequest(http.MethodOptions, fmt.Sprintf("http://127.0.0.1:%d/webui/%s/api/info", port, sid), nil)
	pfReq.Header.Set("Origin", "null")
	pfReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	pfResp, err := http.DefaultClient.Do(pfReq)
	if err != nil {
		t.Fatalf("self-check: preflight: %v", err)
	}
	pfResp.Body.Close()
	if pfResp.StatusCode != http.StatusNoContent ||
		pfResp.Header.Get("Access-Control-Allow-Origin") != "null" ||
		pfResp.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("self-check: preflight status=%d ACAO=%q ACAC=%q",
			pfResp.StatusCode, pfResp.Header.Get("Access-Control-Allow-Origin"),
			pfResp.Header.Get("Access-Control-Allow-Credentials"))
	}
	t.Logf("自检通过：代理入口 200、预检豁免 204 + CORS 头齐备。")
}

func envString(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(t *testing.T, key string, def int) int {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", key, v)
	}
	return n
}
