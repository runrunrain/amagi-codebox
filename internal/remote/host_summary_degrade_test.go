package remote

// host_summary_degrade_test.go — P2-B HostSummary 探测失败降级（读面解耦）。
//
// 锚定的行为契约（执行计划 Phase 2 / 素材B §4 P7）：
//   - GET /host/summary 在 provider/探测失败时不再 503，而是返回保守降级
//     HostSummary（全部已知 CLI Available=false、serverVersion 诚实非空、
//     LaunchSettings 省略）——降级方向保持 fail-closed：探测不到的 CLI
//     绝不报告为可启动；
//   - 会话读面（GET /sessions、GET /sessions/{id}）在探测失败时仍完全可用
//     （这两个端点从不消费 hostCache，本文件在 HTTP 级锁死该不变量，防止
//     未来把 HostSummary 成功重新引入会话读路径）；
//   - POST /pairing/complete 在 provider 失败时保持 fail-closed 503（配对是
//     信任根建立且 201 响应内嵌 HostSummary；未配对设备没有需要保护的会话
//     面，保守拒绝是有意选择）。
//
// 复用 b2b/security_v1/m2a 测试基建（同包 helper），不复制端点字面量。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// sessionDetailPath builds the {id} route from contract manifest symbols only.
func sessionDetailPath(id contract.SessionID) string {
	return contract.RESTBasePath + strings.ReplaceAll(contract.V1RestEndpoints[3].Path, "{id}", string(id))
}

// assertDegradedSummary verifies the conservative degrade shape: contract-valid,
// every known CLI NOT launchable, LaunchSettings omitted, no secret leakage.
func assertDegradedSummary(t *testing.T, body []byte) contract.HostSummary {
	t.Helper()
	for _, bad := range []string{"provider", "token", "apiKey", "secret", "basePath", "path", "env"} {
		if strings.Contains(strings.ToLower(string(body)), bad) {
			t.Fatalf("degraded body leaked forbidden key %q: %s", bad, body)
		}
	}
	var hs contract.HostSummary
	if err := json.Unmarshal(body, &hs); err != nil {
		t.Fatalf("degraded body not a HostSummary: %v (%s)", err, body)
	}
	if hs.APIVersion != contract.APIVersionV1 {
		t.Fatalf("degraded apiVersion = %q", hs.APIVersion)
	}
	if hs.ServerVersion == "" {
		t.Fatal("degraded serverVersion must be non-empty")
	}
	if len(hs.CLIAvailability) != len(contract.KnownCLITypes) {
		t.Fatalf("degraded availability entries = %d want %d", len(hs.CLIAvailability), len(contract.KnownCLITypes))
	}
	seen := map[contract.CLIType]bool{}
	for _, c := range hs.CLIAvailability {
		if c.Available {
			t.Fatalf("degraded summary advertised %q as launchable", c.CLIType)
		}
		seen[c.CLIType] = true
	}
	for _, k := range contract.KnownCLITypes {
		if !seen[k] {
			t.Fatalf("degraded summary missing CLIType %q", k)
		}
	}
	if strings.Contains(strings.ToLower(string(body)), "launchsettings") {
		t.Fatalf("degraded summary must omit launchSettings: %s", body)
	}
	return hs
}

// TestHostSummaryProviderFailure_DegradesToConservativeSummary: provider 失败 →
// 200 + 保守降级 DTO（不 503）；provider 恢复后回到真实摘要（缓存不粘死）。
func TestHostSummaryProviderFailure_DegradesToConservativeSummary(t *testing.T) {
	cp := &b2bCountingProvider{}
	ts, srv, _, cookie, _ := pairWithProvider(t, cp)
	h := srv.buildV1Handler()

	// Provider failure → degraded 200.
	bustHostCache(srv)
	cp.setFail(true)
	rr := rec(h, hsReq(ts, cookie))
	if rr.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("provider failure must degrade to %d, got %d body=%s",
			contract.V1RestEndpoints[1].SuccessStatus, rr.Code, rr.Body.String())
	}
	assertDegradedSummary(t, rr.Body.Bytes())

	// Recovery: provider healthy again → real summary returns (cache un-sticks).
	cp.setFail(false)
	bustHostCache(srv)
	rr2 := rec(h, hsReq(ts, cookie))
	if rr2.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("recovered summary: %d body=%s", rr2.Code, rr2.Body.String())
	}
	var hs contract.HostSummary
	if err := json.Unmarshal(rr2.Body.Bytes(), &hs); err != nil {
		t.Fatal(err)
	}
	if hs.ServerVersion == "unknown" || len(hs.CLIAvailability) == 0 {
		t.Fatalf("recovered summary still degraded: %+v", hs)
	}
	foundAvailable := false
	for _, c := range hs.CLIAvailability {
		if c.Available {
			foundAvailable = true
		}
	}
	if !foundAvailable {
		t.Fatalf("recovered summary reports nothing launchable: %+v", hs.CLIAvailability)
	}
}

// TestHostSummaryProviderFailure_SessionReadSurfaceAvailable is the P2-B
// acceptance negative: with the provider failing, an already-paired device
// still gets the full session read surface — GET /sessions and
// GET /sessions/{id} both 200 — and the degraded host/summary 200 lets the
// mobile lobby bootstrap (host/summary → sessions) unblocked.
func TestHostSummaryProviderFailure_SessionReadSurfaceAvailable(t *testing.T) {
	cp := &b2bCountingProvider{}
	ts, srv, _, cookie, _ := pairWithProvider(t, cp)

	// Wire the M2-A session adapter + one activated session (routes 2-9 active).
	adapter, _, _, _ := setupAdapterTest(t)
	srv.SetSessionAdapter(adapter)
	const sid = contract.SessionID("p2b-sess-1")
	activateTestSession(t, adapter, sid)
	h := srv.buildV1Handler()

	// Host probing now fails.
	bustHostCache(srv)
	cp.setFail(true)

	// 1) host/summary degrades to a conservative 200 (lobby bootstrap survives).
	rrHost := rec(h, hsReq(ts, cookie))
	if rrHost.Code != contract.V1RestEndpoints[1].SuccessStatus {
		t.Fatalf("host/summary under provider failure: %d body=%s", rrHost.Code, rrHost.Body.String())
	}
	assertDegradedSummary(t, rrHost.Body.Bytes())

	// 2) Session list is fully usable.
	rList := httptest.NewRequest(http.MethodGet, contract.RESTBasePath+contract.V1RestEndpoints[2].Path, nil)
	rList.Host = strings.TrimPrefix(ts.URL, "http://")
	rList.Header.Set("Origin", ts.URL)
	rList.AddCookie(cookie)
	rrList := rec(h, rList)
	if rrList.Code != contract.V1RestEndpoints[2].SuccessStatus {
		t.Fatalf("sessions list under provider failure: %d body=%s", rrList.Code, rrList.Body.String())
	}
	var list contract.SessionList
	if err := json.Unmarshal(rrList.Body.Bytes(), &list); err != nil {
		t.Fatalf("sessions list body: %v (%s)", err, rrList.Body.String())
	}
	found := false
	for _, s := range list {
		if s.ID == sid {
			found = true
		}
	}
	if !found {
		t.Fatalf("activated session %s missing from list under provider failure: %s", sid, rrList.Body.String())
	}

	// 3) Session detail is fully usable (earliestSeq/latestSeq present).
	rDetail := httptest.NewRequest(http.MethodGet, sessionDetailPath(sid), nil)
	rDetail.Host = strings.TrimPrefix(ts.URL, "http://")
	rDetail.Header.Set("Origin", ts.URL)
	rDetail.AddCookie(cookie)
	rrDetail := rec(h, rDetail)
	if rrDetail.Code != contract.V1RestEndpoints[3].SuccessStatus {
		t.Fatalf("session detail under provider failure: %d body=%s", rrDetail.Code, rrDetail.Body.String())
	}
	var detail contract.SessionDetail
	if err := json.Unmarshal(rrDetail.Body.Bytes(), &detail); err != nil {
		t.Fatalf("session detail body: %v (%s)", err, rrDetail.Body.String())
	}
	if detail.ID != sid || detail.LatestSeq < detail.EarliestSeq {
		t.Fatalf("session detail projection invalid: %+v", detail)
	}
}

// TestPairingProviderFailure_RemainsFailClosed locks the deliberate choice:
// pairing/complete stays 503 service.down when the provider fails (trust-root
// establishment + HostSummary embedded in the 201 body; fail-closed here does
// not block any existing session surface — an unpaired device has none).
func TestPairingProviderFailure_RemainsFailClosed(t *testing.T) {
	cp := &b2bCountingProvider{}
	cp.setFail(true)
	srv, _ := newSecServer(t, cp.provider)
	code := openWindow(t, srv)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	wireTestPort(srv, ts)

	rr := httptest.NewRecorder()
	srv.buildV1Handler().ServeHTTP(rr, pairingRequest(ts, code, "phone", ts.URL))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("pairing under provider failure must stay fail-closed 503, got %d body=%s", rr.Code, rr.Body.String())
	}
	var apiErr contract.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("pairing error body: %v", err)
	}
	if apiErr.Code != contract.ErrorCodeServiceDown {
		t.Fatalf("pairing error code = %q want service.down", apiErr.Code)
	}
}
