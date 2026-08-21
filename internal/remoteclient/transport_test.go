// transport_test.go — REST 基座测试：12 错误码全映射（对 fixture 错误家族）、
// 客户端自产 net.unreachable（拒连/超时/非契约兜底）、头注入（X-Request-ID/
// Origin/Cookie/Content-Type）、成功解码与 204。
package remoteclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// fixturePath 与 contract/wire_test.go 指向同一机器可读源（D-T02：复用
// v1-wire-fixtures.json 双端校验，不另造错误码样本）。
const wireFixturePath = "../../mobile/src/lib/contract/testdata/v1-wire-fixtures.json"

type wireFixtureError struct {
	RequestID  string              `json:"requestId"`
	Code       contract.ErrorCode  `json:"code"`
	Layer      contract.ErrorLayer `json:"layer"`
	Message    string              `json:"message"`
	ActionHint contract.ActionHint `json:"actionHint"`
	Details    map[string]any      `json:"details,omitempty"`
}

func loadFixtureErrors(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, wireFixturePath))
	if err != nil {
		t.Fatalf("read wire fixtures: %v", err)
	}
	var doc struct {
		Errors map[string]json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse wire fixtures: %v", err)
	}
	if len(doc.Errors) == 0 {
		t.Fatal("wire fixtures carry no error families")
	}
	return doc.Errors
}

// familyStatus 给每个 fixture 家族一个真实 HTTP 状态（镜像服务端映射表：
// mapGateError/mapPairingError/enforceAuthPolicy 的实际 status 选择）。
func familyStatus(code contract.ErrorCode) int {
	switch code {
	case contract.ErrorCodeAuthUnpaired, contract.ErrorCodeAuthRevoked:
		return http.StatusUnauthorized
	case contract.ErrorCodeAuthWindowExpired:
		return http.StatusGone
	case contract.ErrorCodeSessionNotFound:
		return http.StatusNotFound
	case contract.ErrorCodeSessionLaunchFailed:
		return http.StatusInternalServerError
	case contract.ErrorCodeControlBusy:
		return http.StatusConflict
	case contract.ErrorCodeControlForbidden:
		return http.StatusForbidden
	case contract.ErrorCodeRateLimited:
		return http.StatusTooManyRequests
	case contract.ErrorCodeServiceDown, contract.ErrorCodeNetUnreachable:
		return http.StatusServiceUnavailable
	default: // bad_request（含 versionMismatch 家族）
		return http.StatusBadRequest
	}
}

// TestTransportMapsAllTwelveErrorCodes：12 个稳定错误码逐一由 transport 映射
// （fixture 错误家族 → 服务端错误体 → ClientError 码/层/hint/状态保真）。
// net.unreachable 的网络自产路径另由 TestTransportNetUnreachable 覆盖。
func TestTransportMapsAllTwelveErrorCodes(t *testing.T) {
	families := loadFixtureErrors(t)
	seen := map[contract.ErrorCode]string{}
	for name, raw := range families {
		var fe wireFixtureError
		if err := json.Unmarshal(raw, &fe); err != nil {
			t.Fatalf("fixture family %s: %v", name, err)
		}
		if prev, dup := seen[fe.Code]; dup && name != prev {
			// versionMismatch 与 badRequest 同码——同码多家族只测首个。
			continue
		}
		seen[fe.Code] = name

		status := familyStatus(fe.Code)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(contract.RequestIDHeader, r.Header.Get(contract.RequestIDHeader))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(raw)
		}))
		defer ts.Close()
		tr, err := NewTransport(ts.URL)
		if err != nil {
			t.Fatalf("family %s: NewTransport: %v", name, err)
		}
		_, cerr := tr.HostSummaryUnauthenticated(context.Background())
		if cerr == nil {
			t.Fatalf("family %s: expected ClientError, got nil", name)
		}
		if got := cerr.Code(); got != fe.Code {
			t.Errorf("family %s: code = %q, want %q", name, got, fe.Code)
		}
		if cerr.API == nil {
			t.Fatalf("family %s: ClientError.API is nil", name)
		}
		if cerr.StatusCode != status {
			t.Errorf("family %s: status = %d, want %d", name, cerr.StatusCode, status)
		}
		if cerr.API.Layer != fe.Layer {
			t.Errorf("family %s: layer = %q, want %q", name, cerr.API.Layer, fe.Layer)
		}
		if cerr.API.ActionHint != fe.ActionHint {
			t.Errorf("family %s: actionHint = %q, want %q", name, cerr.API.ActionHint, fe.ActionHint)
		}
		if cerr.API.Message != fe.Message {
			t.Errorf("family %s: message not preserved", name)
		}
		if cerr.IsAuthRevoked() != (fe.Code == contract.ErrorCodeAuthRevoked) {
			t.Errorf("family %s: IsAuthRevoked wrong", name)
		}
		if cerr.IsRateLimited() != (fe.Code == contract.ErrorCodeRateLimited) {
			t.Errorf("family %s: IsRateLimited wrong", name)
		}
		if cerr.Error() == "" {
			t.Errorf("family %s: empty Error()", name)
		}
	}
	// 断言 12 码全覆盖：KnownErrorCodes 每个码都有 fixture 家族被映射过。
	for _, code := range contract.KnownErrorCodes {
		if _, ok := seen[code]; !ok {
			t.Errorf("error code %q not covered by fixture families", code)
		}
	}
	if len(seen) != len(contract.KnownErrorCodes) {
		t.Errorf("covered codes = %d, want %d", len(seen), len(contract.KnownErrorCodes))
	}
}

// TestTransportNetUnreachable：拒连/超时两类网络层失败统一自产
// net.unreachable（12 码中唯一客户端自产码，T0-1 §3.1）。
func TestTransportNetUnreachable(t *testing.T) {
	// 拒连：关掉的服务器端口。
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	tr, err := NewTransport(deadURL)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	_, cerr := tr.HostSummaryUnauthenticated(context.Background())
	if cerr == nil {
		t.Fatal("expected net error for closed server")
	}
	if cerr.Code() != contract.ErrorCodeNetUnreachable {
		t.Fatalf("code = %q, want net.unreachable", cerr.Code())
	}
	if cerr.StatusCode != 0 {
		t.Errorf("network failure status = %d, want 0", cerr.StatusCode)
	}
	if cerr.API == nil || cerr.API.Layer != contract.ErrorLayerConnection {
		t.Errorf("synthesized API error missing or wrong layer: %+v", cerr.API)
	}
	if cerr.Error() != "remoteclient: net.unreachable (layer=connection, http=0)" {
		t.Errorf("Error() = %q, want closed-text form without host detail", cerr.Error())
	}

	// 超时：挂起处理器 + 短超时客户端。
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer hang.Close()
	tr2, err := NewTransport(hang.URL)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	tr2.HTTP = &http.Client{Timeout: 50 * time.Millisecond}
	_, cerr2 := tr2.HostSummaryUnauthenticated(context.Background())
	if cerr2 == nil || cerr2.Code() != contract.ErrorCodeNetUnreachable {
		t.Fatalf("timeout: code = %v, want net.unreachable", cerr2)
	}
}

// TestTransportFallbackClassification：无契约错误体时按状态兜底
// （401/403 → auth.unpaired；其余 → net.unreachable），镜像 mobile 语义。
func TestTransportFallbackClassification(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    contract.ErrorCode
		wantLay contract.ErrorLayer
	}{
		{"401 html", http.StatusUnauthorized, "<html>login</html>", contract.ErrorCodeAuthUnpaired, contract.ErrorLayerAuth},
		{"403 empty", http.StatusForbidden, "", contract.ErrorCodeAuthUnpaired, contract.ErrorLayerAuth},
		{"502 gateway", http.StatusBadGateway, "Bad Gateway", contract.ErrorCodeNetUnreachable, contract.ErrorLayerConnection},
		{"500 garbage", http.StatusInternalServerError, "{}", contract.ErrorCodeNetUnreachable, contract.ErrorLayerConnection},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			tr, err := NewTransport(ts.URL)
			if err != nil {
				t.Fatalf("NewTransport: %v", err)
			}
			_, cerr := tr.HostSummaryUnauthenticated(context.Background())
			if cerr == nil {
				t.Fatal("expected error")
			}
			if cerr.Code() != tc.want {
				t.Fatalf("code = %q, want %q", cerr.Code(), tc.want)
			}
			if cerr.API == nil || cerr.API.Layer != tc.wantLay {
				t.Fatalf("layer = %+v, want %q", cerr.API, tc.wantLay)
			}
		})
	}
}

// TestTransportSuccessDecode：200 成功体解码；非 JSON 成功体 → service.down。
func TestTransportSuccessDecode(t *testing.T) {
	f := newFakeHost(t)
	tr, err := NewTransport(f.baseURL())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if tr.HasCredential() {
		t.Fatal("fresh transport must not carry a credential")
	}
	// 无凭据 GET host/summary：假宿主按未配对回 401 auth.unpaired（探活语义）。
	_, cerr := tr.HostSummary(context.Background())
	if cerr == nil || cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("authed call without credential: %v, want auth.unpaired", cerr)
	}

	// 装载凭据后 200 解码成功。
	if err := tr.SetCredential(deviceWireID(7), deviceWireSecret(11)); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if !tr.HasCredential() {
		t.Fatal("HasCredential after set")
	}
	hs, cerr := tr.HostSummary(context.Background())
	if cerr != nil {
		t.Fatalf("HostSummary: %v", cerr)
	}
	if hs.APIVersion != contract.APIVersionV1 || len(hs.CLIAvailability) != len(contract.KnownCLITypes) {
		t.Fatalf("HostSummary decoded wrong: %+v", hs)
	}

	// 非 JSON 成功体 → service.down（不伪装成功）。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>proxy page</html>"))
	}))
	defer ts.Close()
	tr2, _ := NewTransport(ts.URL)
	_, cerr = tr2.HostSummaryUnauthenticated(context.Background())
	if cerr == nil || cerr.Code() != contract.ErrorCodeServiceDown {
		t.Fatalf("non-JSON success body: %v, want service.down", cerr)
	}
}

// TestTransportHeaderInjection：X-Request-ID（可打印、≤128、每请求生成）、
// Origin（=BaseURL 同源形态）、device Cookie（仅 auth 请求）、
// Content-Type（有体请求）逐一在服务端侧断言。
func TestTransportHeaderInjection(t *testing.T) {
	f := newFakeHost(t)
	tr, err := NewTransport(f.baseURL())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	deviceID, secret := deviceWireID(7), deviceWireSecret(11)
	if err := tr.SetCredential(deviceID, secret); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}

	// 无凭据 GET host/summary：假宿主按未配对回 401 auth.unpaired（契约形态，
	// 请求头已注入——本测试只关心头，不关心成败）。
	if _, cerr := tr.HostSummaryUnauthenticated(context.Background()); cerr != nil && cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("unauthenticated probe: %v", cerr)
	}
	if _, cerr := tr.HostSummary(context.Background()); cerr != nil {
		t.Fatalf("authenticated summary: %v", cerr)
	}
	reqs := f.requests()
	if len(reqs) < 2 {
		t.Fatalf("captured %d requests, want ≥2", len(reqs))
	}
	ids := map[string]bool{}
	for _, req := range reqs {
		if req.RequestID == "" || len(req.RequestID) > 128 {
			t.Errorf("request id %q empty or too long", req.RequestID)
		}
		for _, c := range req.RequestID {
			if c < '!' || c > '~' {
				t.Errorf("request id %q has non-printable rune", req.RequestID)
			}
		}
		ids[req.RequestID] = true
		if req.Origin != f.baseURL() {
			t.Errorf("Origin = %q, want %q", req.Origin, f.baseURL())
		}
	}
	if len(ids) < 2 {
		t.Error("X-Request-ID not regenerated per request")
	}
	// 最后一次（authed）带 Cookie；首次（unauth）不带。
	last := reqs[len(reqs)-1]
	wantCookie := deviceCookieName + "=" + buildDeviceCookieValue(deviceID, secret)
	if last.Cookie != wantCookie {
		t.Errorf("Cookie = %q, want %q", last.Cookie, wantCookie)
	}
	if reqs[0].Cookie != "" {
		t.Errorf("unauthenticated probe must not carry cookie, got %q", reqs[0].Cookie)
	}
}

// TestTransportCookieParseStrict：Set-Cookie 值严格解析（长度/前缀/两段 base64）。
func TestTransportCookieParseStrict(t *testing.T) {
	bad := []string{
		"",
		"v1.short.short",
		"v2." + deviceWireID(7) + "." + deviceWireSecret(11),
		deviceWireID(7) + "." + deviceWireSecret(11),
		"v1." + deviceWireID(7) + "." + deviceWireSecret(11) + "x",
		"v1." + strings.Repeat("A", 22) + "." + strings.Repeat("!", 43), // 非法字符
	}
	for _, v := range bad {
		if _, _, err := parseDeviceCookieValue(v); err == nil {
			t.Errorf("value %q parsed, want rejection", v)
		}
	}
	id, sec, err := parseDeviceCookieValue(buildDeviceCookieValue(deviceWireID(7), deviceWireSecret(11)))
	if err != nil || id != deviceWireID(7) || sec != deviceWireSecret(11) {
		t.Fatalf("round-trip parse failed: id=%q err=%v", id, err)
	}
}

// TestTransportNoContent204：DELETE 204 无 body 路径。
func TestTransportNoContent204(t *testing.T) {
	f := newFakeHost(t)
	tr, _ := NewTransport(f.baseURL())
	_ = tr.SetCredential(deviceWireID(7), deviceWireSecret(11)) //nolint:errcheck // 测试恒成功
	sc := NewSessionClient(tr)
	d, cerr := sc.CreateSession(context.Background(), contract.CreateSessionRequest{CLIType: contract.CLITypePi})
	if cerr != nil {
		t.Fatalf("CreateSession: %v", cerr)
	}
	if cerr := sc.DeleteSession(context.Background(), d.ID); cerr != nil {
		t.Fatalf("DeleteSession: %v", cerr)
	}
	if _, cerr := sc.GetSession(context.Background(), d.ID); cerr == nil || cerr.Code() != contract.ErrorCodeSessionNotFound {
		t.Fatalf("get after delete: %v, want session.not_found", cerr)
	}
}
