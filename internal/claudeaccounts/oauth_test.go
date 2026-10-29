package claudeaccounts

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// OAuth/identity 组（design §10.1 矩阵第 5 行）：全部通过注入
// RoundTripper 访问隔离 fake，不触网。

func newRefreshAdapter(t *testing.T, handler func(*http.Request) (*http.Response, error)) (*PiRefreshAdapter, *fakeTransport) {
	t.Helper()
	transport := &fakeTransport{handler: handler}
	return NewPiRefreshAdapter(NewConstrainedHTTPClient(transport)), transport
}

// TestPiRefreshProtocolConstants：已核实协议常量与请求/响应契约
// （design §5.1：token URL、grant_type/client_id/refresh_token、
// expires=now+expires_in×1000−5min）。
func TestPiRefreshProtocolConstants(t *testing.T) {
	adapter, transport := newRefreshAdapter(t, func(req *http.Request) (*http.Response, error) {
		return httpResponse(200, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`), nil
	})
	result, err := adapter.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if result.AccessToken != "new-access" || result.RefreshToken != "new-refresh" {
		t.Fatalf("token 对不符: %+v", result)
	}
	// expires = now + expires_in*1000 - 5min（毫秒语义换算为时间）。
	want := time.Now().Add(time.Duration(3600*1000)*time.Millisecond - 5*time.Minute)
	if result.ExpiresAt.Sub(want) > 2*time.Second || want.Sub(result.ExpiresAt) > 2*time.Second {
		t.Fatalf("ExpiresAt 偏差过大: %v vs %v", result.ExpiresAt, want)
	}
	// 请求契约。
	if len(transport.calls) != 1 {
		t.Fatalf("期望恰好一次请求，实际 %d", len(transport.calls))
	}
	call := transport.calls[0]
	if call.URL.String() != PiTokenURL {
		t.Fatalf("token URL 应为固定常量，实际 %s", call.URL.String())
	}
	if call.Method != http.MethodPost {
		t.Fatal("必须 POST")
	}
	var body map[string]string
	_ = json.NewDecoder(call.Body).Decode(&body)
	if body["grant_type"] != "refresh_token" || body["client_id"] != PiOAuthClientID || body["refresh_token"] != "old-refresh" {
		t.Fatalf("刷新请求字段不符: %+v", body)
	}
	// 构造 anthropic 条目并验证 schema。
	entry, err := BuildPiAnthropicEntry(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePiAnthropicEntry(entry); err != nil {
		t.Fatalf("构造的条目应满足 schema: %v", err)
	}
}

// TestPiRefreshInvalidGrant：invalid_grant → NEEDS_LOGIN。
func TestPiRefreshInvalidGrant(t *testing.T) {
	adapter, _ := newRefreshAdapter(t, func(req *http.Request) (*http.Response, error) {
		return httpResponse(400, `{"error":"invalid_grant"}`), nil
	})
	_, err := adapter.Refresh(context.Background(), "dead-refresh")
	assertReason(t, err, ReasonNeedsLogin)
}

// TestPiRefreshUncertainClasses：响应丢失/非 200/坏响应 → REFRESH_UNCERTAIN
// （design §5.1：请求可能已被处理并轮换 refresh token）。
func TestPiRefreshUncertainClasses(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*http.Request) (*http.Response, error)
	}{
		{"响应丢失", func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }},
		{"500", func(*http.Request) (*http.Response, error) { return httpResponse(500, `{}`), nil }},
		{"400 其他错误", func(*http.Request) (*http.Response, error) { return httpResponse(400, `{"error":"invalid_request"}`), nil }},
		{"200 缺字段", func(*http.Request) (*http.Response, error) { return httpResponse(200, `{"access_token":"x"}`), nil }},
		{"200 坏 JSON", func(*http.Request) (*http.Response, error) { return httpResponse(200, `not-json`), nil }},
		{"expires_in 非有限", func(*http.Request) (*http.Response, error) { return httpResponse(200, `{"access_token":"a","refresh_token":"r","expires_in":1e18}`), nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, _ := newRefreshAdapter(t, tc.handler)
			_, err := adapter.Refresh(context.Background(), "some-refresh")
			assertReason(t, err, ReasonRefreshUncertain)
		})
	}
}

// TestCCRefreshUnverifiedGate：协议未验证 → 拒绝且不发请求。
func TestCCRefreshUnverifiedGate(t *testing.T) {
	var called bool
	adapter := NewCCRefreshAdapter(NewConstrainedHTTPClient(&fakeTransport{handler: func(*http.Request) (*http.Response, error) {
		called = true
		return httpResponse(200, `{}`), nil
	}}), CCRefreshProtocol{})
	_, err := adapter.Refresh(context.Background(), "cc-refresh")
	assertReason(t, err, ReasonUnsupportedCredentialSchema)
	if called {
		t.Fatal("未验证协议不得发出网络请求")
	}
	// 验证态但缺 expires 单位 → 仍拒绝（不猜常量）。
	adapter.Protocol = CCRefreshProtocol{Verified: true, TokenURL: "https://example.invalid/token", ClientID: "cid"}
	_, err = adapter.Refresh(context.Background(), "cc-refresh")
	assertReason(t, err, ReasonUnsupportedCredentialSchema)
}

// TestRefreshStandbyTransaction：RefreshIntent 持久化 → POST → 存池 → 清
// intent 单事务；intent 语义按结果分类。
func TestRefreshStandbyTransaction(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	// 把 A 的 pi grant 置为过期（基于注入时钟）。
	pool, _ := env.svc.LoadPool(context.Background())
	env.pool.mu.Lock()
	pool.Accounts[idA].Pi.Anthropic = []byte(`{"type":"oauth","access":"a","refresh":"pir-old","expires":1}`)
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	env.transport.handler = func(req *http.Request) (*http.Response, error) {
		return httpResponse(200, `{"access_token":"acc-new","refresh_token":"ref-new","expires_in":7200}`), nil
	}
	if err := env.svc.RefreshStandbyGrant(context.Background(), idA, GrantPi); err != nil {
		t.Fatalf("refresh standby: %v", err)
	}
	pool2, _ := env.svc.LoadPool(context.Background())
	if got := string(pool2.Accounts[idA].Pi.Anthropic); !strings.Contains(got, "acc-new") {
		t.Fatalf("新 grant 未存池: %s", got)
	}
	if pool2.RefreshIntent != nil {
		t.Fatal("成功后 intent 应清除")
	}
	if pool2.Accounts[idA].Health.PiGrant != HealthReady {
		t.Fatal("成功后健康度应为 ready")
	}
	_ = idB
}

// TestRefreshStandbyResponseLostKeepsIntent：响应丢失 → 未结 intent +
// refresh-uncertain（design §5.1）。
func TestRefreshStandbyResponseLostKeepsIntent(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	env.pool.mu.Lock()
	pool.Accounts[idA].Pi.Anthropic = []byte(`{"type":"oauth","access":"a","refresh":"pir-old","expires":1}`)
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	env.transport.handler = func(req *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded // 响应丢失
	}
	err := env.svc.RefreshStandbyGrant(context.Background(), idA, GrantPi)
	assertReason(t, err, ReasonRefreshUncertain)
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.RefreshIntent == nil {
		t.Fatal("响应丢失必须保留未结 intent")
	}
	if pool2.Accounts[idA].Health.PiGrant != HealthRefreshUncertain {
		t.Fatal("响应丢失必须标记 refresh-uncertain")
	}
}

// TestRefreshSaveFailureAfterRotation：旋转后存池失败 → REFRESH_UNCERTAIN
// （design §10.1：旋转后池保存失败）。
func TestRefreshSaveFailureAfterRotation(t *testing.T) {
	env := newTestEnv(t)
	idA, _ := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	env.pool.mu.Lock()
	pool.Accounts[idA].Pi.Anthropic = []byte(`{"type":"oauth","access":"a","refresh":"pir-old","expires":1}`)
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	// 第一次 Save（intent）成功，第二次 Save（结果）失败。
	env.pool.saveErrAt = env.pool.saveCalls + 2
	env.pool.mu.Unlock()
	env.transport.handler = func(req *http.Request) (*http.Response, error) {
		return httpResponse(200, `{"access_token":"acc-x","refresh_token":"ref-x","expires_in":600}`), nil
	}
	err := env.svc.RefreshStandbyGrant(context.Background(), idA, GrantPi)
	assertReason(t, err, ReasonRefreshUncertain)
}

// TestRefreshStandbyRejectsActiveAccount：活动账号不主动 refresh。
func TestRefreshStandbyRejectsActiveAccount(t *testing.T) {
	env := newTestEnv(t)
	_, idB := env.enrollAccountB()
	err := env.svc.RefreshStandbyGrant(context.Background(), idB, GrantPi)
	assertReason(t, err, ReasonSessionsActive)
}

// TestRecoverAtStartupMarksUnclosedIntent：重启发现未结 intent →
// refresh-uncertain（design §5.1）。
func TestRecoverAtStartupMarksUnclosedIntent(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	env.pool.mu.Lock()
	pool.RefreshIntent = &RefreshIntent{AccountID: idA, GrantKind: GrantPi, RequestID: "req-1", StartedAt: env.clock.Now()}
	data, _ := encodePoolEnvelope(pool)
	env.pool.envelope = data
	env.pool.mu.Unlock()
	if _, err := env.svc.RecoverAtStartup(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	pool2, _ := env.svc.LoadPool(context.Background())
	if pool2.Accounts[idA].Health.PiGrant != HealthRefreshUncertain {
		t.Fatal("未结 intent 必须标记 refresh-uncertain")
	}
	if pool2.RefreshIntent != nil {
		t.Fatal("intent 应已转化为健康度标记并清除")
	}
	_ = idB
}

// ---- identity（design §4.2/§5.2 约束）----

func newIdentity(t *testing.T, handler func(*http.Request) (*http.Response, error)) *HTTPIdentityProvider {
	return NewHTTPIdentityProvider(NewConstrainedHTTPClient(&fakeTransport{handler: handler}))
}

// TestIdentityProfileSuccess：200 且含账号 UUID → 解析；带 beta 头。
func TestIdentityProfileSuccess(t *testing.T) {
	var gotBeta, gotAuth string
	provider := newIdentity(t, func(req *http.Request) (*http.Response, error) {
		gotBeta = req.Header.Get("anthropic-beta")
		gotAuth = req.Header.Get("Authorization")
		return httpResponse(200, `{"accountUuid":"uuid-1","organizationUuid":"org-1","seatTier":"max"}`), nil
	})
	id, err := provider.FetchProfile(context.Background(), "tok-1")
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if id.AccountUUID != "uuid-1" || id.OrganizationUUID != "org-1" || id.SeatTier != "max" {
		t.Fatalf("解析不符: %+v", id)
	}
	if gotBeta != "oauth-2025-04-20" {
		t.Fatalf("beta 头不符: %s", gotBeta)
	}
	if gotAuth != "Bearer tok-1" {
		t.Fatalf("Authorization 头不符: %s", gotAuth)
	}
}

// TestIdentityFailures：403 scope / 401 / 缺 UUID / 坏 JSON → IDENTITY_UNVERIFIED。
func TestIdentityFailures(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*http.Request) (*http.Response, error)
	}{
		{"403 scope", func(*http.Request) (*http.Response, error) { return httpResponse(403, `{}`), nil }},
		{"401", func(*http.Request) (*http.Response, error) { return httpResponse(401, `{}`), nil }},
		{"缺账号 UUID", func(*http.Request) (*http.Response, error) { return httpResponse(200, `{"organizationUuid":"o"}`), nil }},
		{"坏 JSON", func(*http.Request) (*http.Response, error) { return httpResponse(200, `nope`), nil }},
		{"网络失败", func(*http.Request) (*http.Response, error) { return nil, context.Canceled }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := newIdentity(t, tc.handler)
			_, err := provider.FetchProfile(context.Background(), "tok")
			assertReason(t, err, ReasonIdentityUnverified)
		})
	}
}

// TestIdentityRejectsRedirect：拒绝 redirect 且不泄漏目标 URL。
func TestIdentityRejectsRedirect(t *testing.T) {
	provider := newIdentity(t, func(req *http.Request) (*http.Response, error) {
		resp := httpResponse(302, ``)
		resp.Header.Set("Location", "https://evil.example/leak?token=tok-1")
		return resp, nil
	})
	// http.Client 在 CheckRedirect 报错时返回该错误与最终响应；
	// 受限 client 的行为：返回 errRedirectRefused。
	_, err := provider.FetchProfile(context.Background(), "tok-1")
	if err == nil {
		t.Fatal("redirect 应被拒绝")
	}
	if strings.Contains(err.Error(), "evil.example") || strings.Contains(err.Error(), "tok-1") {
		t.Fatalf("redirect 错误泄漏了目标 URL: %v", err)
	}
}

// TestResponseSizeLimit：超过 64KiB → 拒绝。
func TestResponseSizeLimit(t *testing.T) {
	big := strings.Repeat("a", 65*1024)
	provider := newIdentity(t, func(*http.Request) (*http.Response, error) {
		return httpResponse(200, big), nil
	})
	_, err := provider.FetchProfile(context.Background(), "tok")
	assertReason(t, err, ReasonIdentityUnverified)
}

// TestVerifyPairedIdentities：双 grant 配对与本地投影比较。
func TestVerifyPairedIdentities(t *testing.T) {
	a := ProfileIdentity{AccountUUID: "uuid-1"}
	b := ProfileIdentity{AccountUUID: "uuid-2"}
	if err := VerifyPairedIdentities(a, a, "uuid-1"); err != nil {
		t.Fatalf("同账号应通过: %v", err)
	}
	assertReason(t, VerifyPairedIdentities(a, b, ""), ReasonIdentityMismatch)
	assertReason(t, VerifyPairedIdentities(a, a, "uuid-9"), ReasonIdentityMismatch)
	assertReason(t, VerifyPairedIdentities(ProfileIdentity{}, a, ""), ReasonIdentityUnverified)
}
