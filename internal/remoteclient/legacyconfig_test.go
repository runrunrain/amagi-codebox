// legacyconfig_test.go — legacy 配置面（WD-5 过渡方案，RC4-1）测试：
// httptest 假 legacy 服务端（Bearer 鉴权、providers/settings 读写）覆盖：
//   · providers/settings 往返（列表/详情/保存，请求头注入）；
//   · 401/403 族 → auth.unpaired 形态（提示重填 token，不泄露 token）；
//   · token 未配置 → 明确错误 + 零出网；
//   · 净化层负向断言：伪造含密钥响应 → 下行掩码；伪造含密钥请求 → 上行拦截；
//   · token 不落盘（Keychain 条目 + 登记簿仅布尔投影）；
//   · hasLegacyToken 标记生命周期（设置/清除/地址变更重置/ForgetHost 清理）。
package remoteclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
)

// ---------------------------------------------------------------------------
// 假 legacy 服务端（镜像 internal/remote handlers.go 的可观察契约：Bearer
// 鉴权、{"error":...} 错误体、providers 列表/详情/保存、settings 读写）。
// ---------------------------------------------------------------------------

// legacyCapturedRequest 记录假服务端观察到的请求（供头部/体断言）。
type legacyCapturedRequest struct {
	Method      string
	Path        string
	Auth        string
	RequestID   string
	ContentType string
	Body        string
}

// fakeLegacyHost 是 legacy REST 假宿主：providers 持久 map（PUT 即写回）、
// settings 字符串、可选 401/403 拒绝模式。
type fakeLegacyHost struct {
	t         *testing.T
	srv       *httptest.Server
	token     string
	mu        sync.Mutex
	providers map[string]string // name → 导出 JSON
	settings  string
	reject    int // 0=正常；401/403=全部按该状态拒绝
	captured  []legacyCapturedRequest
}

func newFakeLegacyHost(t *testing.T) *fakeLegacyHost {
	t.Helper()
	f := &fakeLegacyHost{
		t:         t,
		token:     "legacy-tok-" + fmt.Sprint(time.Now().UnixNano()),
		providers: map[string]string{},
		settings:  `{"remotePort":8680,"remoteToken":"host-secret-token","autoStart":false,"logLevel":"info"}`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/providers", f.guard(f.handleList))
	mux.HandleFunc("GET /api/providers/{name}", f.guard(f.handleGet))
	mux.HandleFunc("PUT /api/providers/{name}", f.guard(f.handlePut))
	mux.HandleFunc("GET /api/settings", f.guard(f.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", f.guard(f.handlePutSettings))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// guard 记录请求 + Bearer 鉴权（无效 → 401 {"error":"unauthorized"}）+ 拒绝模式。
// body 全量读取入捕获记录后重新注入（handler 可复读）。
func (f *fakeLegacyHost) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body string
		if r.Body != nil {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			body = string(raw)
			r.Body = io.NopCloser(strings.NewReader(body))
		}
		f.mu.Lock()
		f.captured = append(f.captured, legacyCapturedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			Auth:        r.Header.Get("Authorization"),
			RequestID:   r.Header.Get(contract.RequestIDHeader),
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		})
		reject := f.reject
		f.mu.Unlock()
		if reject != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(reject)
			fmt.Fprintf(w, `{"error":"%s"}`, map[int]string{401: "unauthorized", 403: "forbidden"}[reject])
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next(w, r)
	}
}

func (f *fakeLegacyHost) handleList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.providers))
	for name, raw := range f.providers {
		var p map[string]any
		_ = json.Unmarshal([]byte(raw), &p)
		item := map[string]any{"id": name, "name": name}
		if v, ok := p["type"].(string); ok {
			item["type"] = v
		}
		if v, ok := p["base_url"].(string); ok {
			item["baseURL"] = v
		}
		if v, ok := p["default_model"].(string); ok {
			item["model"] = v
		}
		out = append(out, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeLegacyHost) handleGet(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.providers[r.PathValue("name")]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"provider not found"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(raw))
}

func (f *fakeLegacyHost) handlePut(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.providers[r.PathValue("name")] = string(raw)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (f *fakeLegacyHost) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(f.settings))
}

func (f *fakeLegacyHost) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.settings = string(raw)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true,"message":"settings updated and applied"}`))
}

// setProvider 预置服务端存储的提供商导出 JSON。
func (f *fakeLegacyHost) setProvider(name, raw string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers[name] = raw
}

// setSettings 覆写服务端设置的 JSON。
func (f *fakeLegacyHost) setSettings(raw string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings = raw
}

// hostPort 返回 127.0.0.1:port 形态地址。
func (f *fakeLegacyHost) hostPort() string {
	return strings.TrimPrefix(f.srv.URL, "http://")
}

// setReject 注入 401/403 拒绝模式（0 恢复）。
func (f *fakeLegacyHost) setReject(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reject = status
}

// capturedCount 返回已捕获请求数。
func (f *fakeLegacyHost) capturedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.captured)
}

// lastPut 返回最近一次 PUT 请求（方法/路径/头/体）。
func (f *fakeLegacyHost) lastPut() legacyCapturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.captured) - 1; i >= 0; i-- {
		if f.captured[i].Method == http.MethodPut {
			return f.captured[i]
		}
	}
	f.t.Fatal("no PUT request captured")
	return legacyCapturedRequest{}
}

// storedProvider 返回服务端当前存储的提供商导出 JSON。
func (f *fakeLegacyHost) storedProvider(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.providers[name]
}

// ---------------------------------------------------------------------------
// fixture：登记簿（temp 落盘）+ 内存凭据存储 + 已配对条目 + 服务实例。
// ---------------------------------------------------------------------------

type legacyFixture struct {
	host     *fakeLegacyHost
	registry *HostRegistry
	creds    *memCredentialStore
	svc      *LegacyConfigService
	hostID   string
	deviceID string
}

func newLegacyFixture(t *testing.T) *legacyFixture {
	t.Helper()
	fh := newFakeLegacyHost(t)
	registry := NewHostRegistry(filepath.Join(t.TempDir(), "remote-hosts.json"))
	creds := newMemCredentialStore()
	svc, err := NewLegacyConfigService(registry, creds)
	if err != nil {
		t.Fatalf("NewLegacyConfigService: %v", err)
	}
	devID := deviceWireID(7)
	hostID, err := registry.UpsertPaired(fh.hostPort(), devID, "host-a", HealthReachable, time.Now())
	if err != nil {
		t.Fatalf("UpsertPaired: %v", err)
	}
	return &legacyFixture{host: fh, registry: registry, creds: creds, svc: svc, hostID: hostID, deviceID: devID}
}

func (f *legacyFixture) setToken(t *testing.T) {
	t.Helper()
	if err := f.svc.SetLegacyToken(f.hostID, f.host.token); err != nil {
		t.Fatalf("SetLegacyToken: %v", err)
	}
}

// seedProvider 预置一个「无 auth_key」的提供商（api_key 掩码占位在统一
// 密钥字段上具备回传语义，可走完整往返）。
func (f *legacyFixture) seedProvider(t *testing.T, name string) {
	t.Helper()
	f.host.setProvider(name, `{"default_model":"m1","type":"openai","base_url":"https://api.example.com","api_key":"sk-live-plaintext-123","openai":{"enabled":true,"api_key":"sk-nested-456","base_url":"https://y.example.com"},"presets":{"p1":{"name":"p1","model":"m1","parameters":{"max_tokens":8192,"temperature":0.5}}}}`)
}

// ---------------------------------------------------------------------------
// 往返（正路径）
// ---------------------------------------------------------------------------

// TestLegacyProviderRoundtrip：设置 token → 列表/详情（下行掩码）→ 保存
// （api_key 掩码占位转空串=保留宿主现值）→ 服务端收到净化后的体。
func TestLegacyProviderRoundtrip(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)
	f.seedProvider(t, "prov-a")
	ctx := context.Background()

	// 列表：摘要固定字段。
	list, cerr := f.svc.ListProviders(ctx, f.hostID)
	if cerr != nil {
		t.Fatalf("ListProviders: %v", cerr)
	}
	if len(list) != 1 || list[0].ID != "prov-a" || list[0].Model != "m1" {
		t.Fatalf("list = %+v, want single prov-a summary", list)
	}

	// 详情：密钥字段下行掩码，非密钥字段（含 max_tokens）保真。
	raw, cerr := f.svc.GetProvider(ctx, f.hostID, "prov-a")
	if cerr != nil {
		t.Fatalf("GetProvider: %v", cerr)
	}
	for _, secret := range []string{"sk-live-plaintext-123", "sk-nested-456"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("GetProvider output leaks secret %q:\n%s", secret, raw)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("sanitized output not JSON: %v\n%s", err, raw)
	}
	if doc["api_key"] != RemoteManagedMask {
		t.Errorf("api_key = %v, want mask", doc["api_key"])
	}
	nested := doc["openai"].(map[string]any)
	if nested["api_key"] != RemoteManagedMask {
		t.Errorf("openai.api_key = %v, want mask", nested["api_key"])
	}
	if nested["base_url"] != "https://y.example.com" {
		t.Errorf("openai.base_url = %v, want intact", nested["base_url"])
	}
	preset := doc["presets"].(map[string]any)["p1"].(map[string]any)
	params := preset["parameters"].(map[string]any)
	if params["max_tokens"].(float64) != 8192 {
		t.Errorf("max_tokens = %v, want preserved (not masked)", params["max_tokens"])
	}

	// 保存：基于净化输出改非密钥字段，api_key 保持掩码占位回传。
	doc["default_model"] = "m2"
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal edited: %v", err)
	}
	if cerr := f.svc.PutProvider(ctx, f.hostID, "prov-a", string(edited)); cerr != nil {
		t.Fatalf("PutProvider: %v", cerr)
	}
	stored := f.host.storedProvider("prov-a")
	if strings.Contains(stored, "sk-") || strings.Contains(stored, RemoteManagedMask) {
		t.Fatalf("stored body must carry neither plaintext nor mask:\n%s", stored)
	}
	var storedDoc map[string]any
	_ = json.Unmarshal([]byte(stored), &storedDoc)
	if storedDoc["default_model"] != "m2" {
		t.Errorf("stored default_model = %v, want m2", storedDoc["default_model"])
	}
	if storedDoc["api_key"] != "" {
		t.Errorf("stored api_key = %v, want empty (keep-current semantics)", storedDoc["api_key"])
	}

	// 请求形状：Bearer 头 + X-Request-ID + JSON Content-Type。
	put := f.host.lastPut()
	if put.Path != "/api/providers/prov-a" {
		t.Errorf("PUT path = %q, want /api/providers/prov-a", put.Path)
	}
	if put.Auth != "Bearer "+f.host.token {
		t.Errorf("PUT Authorization = %q, want Bearer token", put.Auth)
	}
	if put.RequestID == "" {
		t.Error("PUT missing X-Request-ID")
	}
	if put.ContentType != "application/json" {
		t.Errorf("PUT Content-Type = %q, want application/json", put.ContentType)
	}
}

// TestLegacySettingsRoundtrip：设置下行掩码 remoteToken、保真 remotePort；
// 上行保存非密钥字段。
func TestLegacySettingsRoundtrip(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)
	ctx := context.Background()

	raw, cerr := f.svc.GetSettings(ctx, f.hostID)
	if cerr != nil {
		t.Fatalf("GetSettings: %v", cerr)
	}
	if strings.Contains(raw, "host-secret-token") {
		t.Fatalf("GetSettings output leaks remoteToken:\n%s", raw)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("sanitized settings not JSON: %v\n%s", err, raw)
	}
	if doc["remoteToken"] != RemoteManagedMask {
		t.Errorf("remoteToken = %v, want mask", doc["remoteToken"])
	}
	if doc["remotePort"].(float64) != 8680 {
		t.Errorf("remotePort = %v, want 8680 intact", doc["remotePort"])
	}
	if doc["logLevel"] != "info" {
		t.Errorf("logLevel = %v, want intact", doc["logLevel"])
	}

	if cerr := f.svc.PutSettings(ctx, f.hostID, `{"remotePort":8690}`); cerr != nil {
		t.Fatalf("PutSettings: %v", cerr)
	}
	f.host.mu.Lock()
	stored := f.host.settings
	f.host.mu.Unlock()
	if !strings.Contains(stored, `"remotePort":8690`) {
		t.Fatalf("stored settings = %s, want remotePort 8690", stored)
	}
}

// ---------------------------------------------------------------------------
// 401 族 / 未配置 token
// ---------------------------------------------------------------------------

// TestLegacyAuth401：token 错误 → 服务端 401 → auth.unpaired 形态 + 重填
// 提示；错误文本不含 token 本体。
func TestLegacyAuth401(t *testing.T) {
	f := newLegacyFixture(t)
	if err := f.svc.SetLegacyToken(f.hostID, "wrong-token-value"); err != nil {
		t.Fatalf("SetLegacyToken: %v", err)
	}
	_, cerr := f.svc.GetSettings(context.Background(), f.hostID)
	if cerr == nil {
		t.Fatal("GetSettings with wrong token: want error")
	}
	if cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("code = %s, want auth.unpaired (err=%v)", cerr.Code(), cerr)
	}
	if cerr.API == nil || !strings.Contains(cerr.API.Message, "re-enter") {
		t.Errorf("error message should hint re-entering token: %+v", cerr.API)
	}
	if strings.Contains(cerr.Error(), "wrong-token-value") {
		t.Errorf("error text leaks token: %v", cerr)
	}
}

// TestLegacyAuth403：服务端 403（宿主 loopback 策略等）→ auth.unpaired 形态。
func TestLegacyAuth403(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)
	f.host.setReject(http.StatusForbidden)
	defer f.host.setReject(0)
	_, cerr := f.svc.GetSettings(context.Background(), f.hostID)
	if cerr == nil || cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("403 → code = %v want auth.unpaired (err=%v)", cerr.Code(), cerr)
	}
}

// TestLegacyTokenNotConfigured：未配置 token → 明确 auth.unpaired 文案 +
// 零出网（假宿主未捕获任何请求）。
func TestLegacyTokenNotConfigured(t *testing.T) {
	f := newLegacyFixture(t)
	before := f.host.capturedCount()
	list, cerr := f.svc.ListProviders(context.Background(), f.hostID)
	if cerr == nil {
		t.Fatal("ListProviders without token: want error")
	}
	if cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("code = %s, want auth.unpaired", cerr.Code())
	}
	if cerr.API == nil || !strings.Contains(cerr.API.Message, "not configured") {
		t.Errorf("error should state token not configured: %+v", cerr.API)
	}
	if list != nil {
		t.Errorf("list = %+v, want nil", list)
	}
	if f.host.capturedCount() != before {
		t.Fatalf("requests leaked to host without token: %d → %d", before, f.host.capturedCount())
	}
}

// ---------------------------------------------------------------------------
// 净化层负向断言（验收 5）
// ---------------------------------------------------------------------------

// TestLegacySanitizeDownloadForgedSecrets：伪造含密钥响应（嵌套 api_key/
// auth_key、headers 凭据头、settings token 族字段）→ 到达调用方前全部掩码。
func TestLegacySanitizeDownloadForgedSecrets(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)
	ctx := context.Background()

	f.host.setProvider("evil", `{"api_key":"topsecret-1","auth_key":"topsecret-2","anthropic":{"api_key":"topsecret-3","auth_key":"topsecret-4","headers":{"Authorization":"Bearer topsecret-5","X-Api-Key":"topsecret-6"}},"openai":{"auth_key":"topsecret-7"},"presets":{"p":{"parameters":{"max_tokens":4096}}}}`)
	f.host.setSettings(`{"remotePort":8680,"remoteToken":"topsecret-8","accessToken":"topsecret-9","clientSecret":"topsecret-10","logLevel":"info"}`)

	prov, cerr := f.svc.GetProvider(ctx, f.hostID, "evil")
	if cerr != nil {
		t.Fatalf("GetProvider: %v", cerr)
	}
	for _, s := range []string{"topsecret-1", "topsecret-2", "topsecret-3", "topsecret-4", "topsecret-5", "topsecret-6", "topsecret-7"} {
		if strings.Contains(prov, s) {
			t.Fatalf("provider response leaks %q:\n%s", s, prov)
		}
	}
	if !strings.Contains(prov, `"max_tokens":4096`) {
		t.Errorf("max_tokens must survive sanitization:\n%s", prov)
	}

	settings, cerr := f.svc.GetSettings(ctx, f.hostID)
	if cerr != nil {
		t.Fatalf("GetSettings: %v", cerr)
	}
	for _, s := range []string{"topsecret-8", "topsecret-9", "topsecret-10"} {
		if strings.Contains(settings, s) {
			t.Fatalf("settings response leaks %q:\n%s", s, settings)
		}
	}
}

// TestLegacySanitizeUploadRejectsPlaintext：伪造含明文密钥的 PUT 体 →
// 本地 bad_request 拒绝、零出网；auth_key 等全量替换字段连掩码占位也拒绝
// （防静默清空宿主配置）；空串 api_key（保留语义）放行。
func TestLegacySanitizeUploadRejectsPlaintext(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)
	ctx := context.Background()

	cases := []struct {
		name string
		body string
		want string // 期望错误文本包含的字段路径
	}{
		{"plaintext api_key", `{"default_model":"m1","api_key":"sk-plaintext"}`, "api_key"},
		{"nested plaintext", `{"openai":{"api_key":"sk-nested"}}`, "openai.api_key"},
		{"plaintext auth_key", `{"auth_key":"tok"}`, "auth_key"},
		{"mask on replace-semantics field", `{"auth_key":"` + RemoteManagedMask + `"}`, "auth_key"},
	}
	for _, tc := range cases {
		before := f.host.capturedCount()
		cerr := f.svc.PutProvider(ctx, f.hostID, "prov-a", tc.body)
		if cerr == nil {
			t.Fatalf("%s: PutProvider accepted forbidden body", tc.name)
		}
		if cerr.Code() != contract.ErrorCodeBadRequest {
			t.Fatalf("%s: code = %s, want bad_request (err=%v)", tc.name, cerr.Code(), cerr)
		}
		if cerr.API == nil || !strings.Contains(cerr.API.Message, tc.want) {
			t.Errorf("%s: error message should name field %q: %+v", tc.name, tc.want, cerr.API)
		}
		if f.host.capturedCount() != before {
			t.Fatalf("%s: forbidden body leaked to host", tc.name)
		}
	}

	// settings：remoteToken 明文/掩码均拒绝、零出网。
	for _, body := range []string{
		`{"remoteToken":"tok-plain"}`,
		`{"remoteToken":"` + RemoteManagedMask + `"}`,
	} {
		before := f.host.capturedCount()
		cerr := f.svc.PutSettings(ctx, f.hostID, body)
		if cerr == nil || cerr.Code() != contract.ErrorCodeBadRequest {
			t.Fatalf("PutSettings(%s): code = %v, want bad_request", body, cerr.Code())
		}
		if f.host.capturedCount() != before {
			t.Fatalf("PutSettings(%s): leaked to host", body)
		}
	}

	// 空串 api_key（显式保留语义）放行且正常出网。
	if cerr := f.svc.PutProvider(ctx, f.hostID, "prov-b", `{"default_model":"m0","api_key":""}`); cerr != nil {
		t.Fatalf("PutProvider empty api_key: %v", cerr)
	}
}

// ---------------------------------------------------------------------------
// token 存储（不落盘）与标记生命周期
// ---------------------------------------------------------------------------

// TestLegacyTokenNeverOnDisk：token 只进凭据存储（生产=Keychain/DPAPI），登记簿
// 文件仅落 hasLegacyToken 布尔投影；configDir 其余文件零 token 字节。
func TestLegacyTokenNeverOnDisk(t *testing.T) {
	fh := newFakeLegacyHost(t)
	dir := t.TempDir()
	registry := NewHostRegistry(filepath.Join(dir, "remote-hosts.json"))
	svcSecrets := secrets.NewSecretsServiceWithStore(dir, stubSecretStore{})
	if err := svcSecrets.Load(); err != nil {
		t.Fatalf("load secrets service: %v", err)
	}
	creds, err := NewSecretsCredentialStoreWithService(svcSecrets)
	if err != nil {
		t.Fatalf("credential store: %v", err)
	}
	svc, err := NewLegacyConfigService(registry, creds)
	if err != nil {
		t.Fatalf("NewLegacyConfigService: %v", err)
	}
	devID := deviceWireID(3)
	hostID, err := registry.UpsertPaired(fh.hostPort(), devID, "host-x", HealthReachable, time.Now())
	if err != nil {
		t.Fatalf("UpsertPaired: %v", err)
	}
	const token = "topsecret-legacy-token-42"
	if err := svc.SetLegacyToken(hostID, token); err != nil {
		t.Fatalf("SetLegacyToken: %v", err)
	}

	registryPath := filepath.Join(dir, "remote-hosts.json")
	raw, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var regEntries []HostEntry
	if err := json.Unmarshal(raw, &regEntries); err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	if len(regEntries) != 1 || !regEntries[0].HasLegacyToken {
		t.Fatalf("registry should carry hasLegacyToken=true:\n%s", raw)
	}
	// 登记簿与 configDir 内除凭据存储文件外的任何文件都不得含 token。
	credsPath := filepath.Join(dir, "secrets.enc")
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() || path == credsPath {
			return werr
		}
		content, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(content), token) {
			t.Errorf("token bytes found in non-keychain file %s", path)
		}
		return nil
	})
	// 凭据存储条目名冻结格式 codebox-remoteclient/<DeviceID>/legacy。
	got, err := creds.Get(LegacyTokenEntryName(devID))
	if err != nil || got != token {
		t.Fatalf("keychain entry = %q err=%v, want stored token", got, err)
	}
}

// TestLegacyTokenFlagLifecycle：Set/Clear 维护标记；地址变更（配对态重置）
// 清标记；ForgetHost 连带清理 legacy token 条目。
func TestLegacyTokenFlagLifecycle(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)

	entry, ok := f.registry.Get(f.hostID)
	if !ok || !entry.HasLegacyToken {
		t.Fatalf("after SetLegacyToken: entry = %+v, want hasLegacyToken", entry)
	}

	if err := f.svc.ClearLegacyToken(f.hostID); err != nil {
		t.Fatalf("ClearLegacyToken: %v", err)
	}
	entry, _ = f.registry.Get(f.hostID)
	if entry.HasLegacyToken {
		t.Fatal("after ClearLegacyToken: flag should be false")
	}
	if got, err := f.creds.Get(legacyTokenEntryName(f.deviceID)); err != nil || got != "" {
		t.Fatalf("legacy keychain entry = %q err=%v, want deleted", got, err)
	}

	// 重新设置后走地址变更路径：配对态重置连带标记重置。
	f.setToken(t)
	newPort := "127.0.0.1:9999"
	if err := f.registry.UpdateHostPort(f.hostID, newPort); err != nil {
		t.Fatalf("UpdateHostPort: %v", err)
	}
	entry, _ = f.registry.Get(f.hostID)
	if entry.HasLegacyToken || entry.DeviceID != "" {
		t.Fatalf("after UpdateHostPort: entry = %+v, want pairing reset incl. legacy flag", entry)
	}
}

// TestLegacyForgetHostCleansLegacyToken：ForgetHost 同时清理设备凭据与
// legacy token 条目（凭据删除失败则整体失败，不留孤儿）。
func TestLegacyForgetHostCleansLegacyToken(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)
	pairing, err := NewPairingService(f.registry, f.creds)
	if err != nil {
		t.Fatalf("NewPairingService: %v", err)
	}
	if err := pairing.ForgetHost(f.hostID); err != nil {
		t.Fatalf("ForgetHost: %v", err)
	}
	if got, err := f.creds.Get(legacyTokenEntryName(f.deviceID)); err != nil || got != "" {
		t.Fatalf("legacy token entry = %q err=%v, want deleted by ForgetHost", got, err)
	}
	if _, ok := f.registry.Get(f.hostID); ok {
		t.Error("host entry should be removed")
	}
}

// TestLegacyFlagSelfHealsOnMissingKeychain：标记为真但 Keychain 条目缺失 →
// 报 auth.unpaired（token 未配置）并自愈清除标记。
func TestLegacyFlagSelfHealsOnMissingKeychain(t *testing.T) {
	f := newLegacyFixture(t)
	f.setToken(t)
	if err := f.creds.Delete(legacyTokenEntryName(f.deviceID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, cerr := f.svc.GetSettings(context.Background(), f.hostID)
	if cerr == nil || cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("code = %v, want auth.unpaired", cerr.Code())
	}
	entry, _ := f.registry.Get(f.hostID)
	if entry.HasLegacyToken {
		t.Error("flag should self-heal to false when keychain entry is missing")
	}
}

// TestLegacyPreconditions：未配对主机 SetLegacyToken/配置方法 → 明确错误；
// 未知主机 → bad_request。
func TestLegacyPreconditions(t *testing.T) {
	f := newLegacyFixture(t)

	// 未配对条目。
	entry, err := f.registry.Add("unpaired", "127.0.0.1:1")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := f.svc.SetLegacyToken(entry.ID, "tok"); err == nil || !strings.Contains(err.Error(), "not paired") {
		t.Fatalf("SetLegacyToken on unpaired: err = %v, want not-paired error", err)
	}
	_, cerr := f.svc.GetSettings(context.Background(), entry.ID)
	if cerr == nil || cerr.Code() != contract.ErrorCodeAuthUnpaired {
		t.Fatalf("GetSettings unpaired: code = %v, want auth.unpaired", cerr.Code())
	}

	// 未知主机。
	_, cerr = f.svc.GetSettings(context.Background(), "host-nope")
	if cerr == nil || cerr.Code() != contract.ErrorCodeBadRequest {
		t.Fatalf("GetSettings unknown host: code = %v, want bad_request", cerr.Code())
	}
	if err := f.svc.SetLegacyToken("host-nope", "tok"); err == nil {
		t.Fatal("SetLegacyToken unknown host: want error")
	}
}
