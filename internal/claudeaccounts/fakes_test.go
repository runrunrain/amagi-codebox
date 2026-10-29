package claudeaccounts

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 测试 fixture 全部使用合成字符串（design §10.1：OAuth fixture 全用合成
// 字符串，不保存真实 token；存储与路径全部依赖注入）。

const (
	syntheticUUIDA = "11111111-1111-4111-8111-111111111111"
	syntheticUUIDB = "22222222-2222-4222-8222-222222222222"
	syntheticOrgA  = "org-aaaaaaaa"
	syntheticOrgB  = "org-bbbbbbbb"
)

// syntheticKeychainItem 构造合成 Claude Code Keychain 项（含未知兄弟字段）。
func syntheticKeychainItem(ccToken string) []byte {
	return []byte(fmt.Sprintf(`{
  "claudeAiOauth": {"accessToken": %q, "refreshToken": "ccr-%s"},
  "otherSibling": {"keep": true}
}`, ccToken, ccToken))
}

// syntheticClaudeJSON 构造合成 .claude.json（含非受管字段）。
func syntheticClaudeJSON(accountUUID, userID, email string) []byte {
	return []byte(fmt.Sprintf(`{
  "oauthAccount": {"accountUuid": %q, "emailAddress": %q, "emailAddressVerified": true},
  "userID": %q,
  "projects": {"/tmp/demo": {"allowedTools": ["Read"]}},
  "firstStartTime": "2026-01-01T00:00:00Z",
  "isQualifiedForDataSharing": true
}`, accountUUID, email, userID))
}

// syntheticPiAuth 构造合成 auth.json（anthropic + 其他 provider）。
func syntheticPiAuth(piToken string, expiresMS int64) []byte {
	return []byte(fmt.Sprintf(`{
  "anthropic": {"type": "oauth", "access": %q, "refresh": "pir-%s", "expires": %d},
  "openai": {"type": "api_key", "key": "sk-synthetic"},
  "google": {"type": "api_key", "key": "g-synthetic"}
}`, piToken, piToken, expiresMS))
}

func futureExpiresMS(now time.Time) int64 { return now.Add(6 * time.Hour).UnixMilli() }

// ---- fake 池存储 ----

type fakePoolStore struct {
	mu        sync.Mutex
	envelope  []byte
	loadErr   error
	saveErrAt int // 第 N 次 Save（1-based）失败；0=不失败
	saveCalls int
}

func (f *fakePoolStore) Load(ctx context.Context) (*AccountPool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	if f.envelope == nil {
		return nil, nil // 从未创建
	}
	return DecodePoolEnvelope(f.envelope)
}

func (f *fakePoolStore) Save(ctx context.Context, pool *AccountPool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.saveErrAt == f.saveCalls {
		return NewErr(ReasonPoolCorrupt, "注入的保存失败")
	}
	data, err := encodePoolEnvelope(pool)
	if err != nil {
		return err
	}
	f.envelope = data
	// 与真实 Keychain 后端一致：保存后回读验证。
	reloaded, err := DecodePoolEnvelope(f.envelope)
	if err != nil {
		return err
	}
	return verifySavedPool(reloaded, pool)
}

func (f *fakePoolStore) Kind() string { return "memory" }

func (f *fakePoolStore) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveCalls
}

// ---- fake Claude Code Keychain ----

type fakeKCItem struct {
	account string
	data    []byte
}

type fakeKeychain struct {
	mu           sync.Mutex
	items        []fakeKCItem
	locateErr    error
	readErr      error
	updateErrAt  int // 第 N 次 UpdateItem 失败；0=不失败
	updateCalls  int
	// onRead 在每次 ReadItem 读取前回调（锁外；F03 测试：在指定读取处模拟
	// 外部写入者刷新，覆盖「source 读后→提交前」窗口）。
	onRead func()
	// afterUpdate 在每次成功 UpdateItem 后回调（持 keychain 锁；只能触碰
	// 其他 fake。R02 测试：Patch 成功后令下一次池进度 Save 失败）。
	afterUpdate func()
}

func (f *fakeKeychain) LocateItem(ctx context.Context) (*KeychainLocator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locateErr != nil {
		return nil, f.locateErr
	}
	switch len(f.items) {
	case 0:
		return nil, NewErr(ReasonNeedsLogin, "Claude Code Keychain 凭据项不存在（未登录或已登出）")
	case 1:
		return &KeychainLocator{Service: ClaudeKeychainServiceName, Account: f.items[0].account}, nil
	default:
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "Claude Code Keychain service 匹配 %d 项，无法唯一定位", len(f.items))
	}
}

func (f *fakeKeychain) find(account string) *fakeKCItem {
	for i := range f.items {
		if f.items[i].account == account {
			return &f.items[i]
		}
	}
	return nil
}

func (f *fakeKeychain) ReadItem(ctx context.Context, loc *KeychainLocator) ([]byte, error) {
	if hook := f.onRead; hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	item := f.find(loc.Account)
	if item == nil {
		return nil, NewErr(ReasonNeedsLogin, "Claude Code Keychain 凭据项不存在")
	}
	return append([]byte(nil), item.data...), nil
}

func (f *fakeKeychain) UpdateItem(ctx context.Context, loc *KeychainLocator, item []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	if f.updateErrAt == f.updateCalls {
		return NewErr(ReasonNeedsReconcile, "注入的 Keychain 更新失败")
	}
	target := f.find(loc.Account)
	if target == nil {
		return NewErr(ReasonNeedsLogin, "Claude Code Keychain 凭据项不存在")
	}
	target.data = append([]byte(nil), item...)
	if hook := f.afterUpdate; hook != nil {
		hook()
	}
	return nil
}

func (f *fakeKeychain) updateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updateCalls
}

// ---- fake admission / 静止条件 / 身份 / 时钟 ----

type fakeGate struct {
	mu   sync.Mutex
	held int
	deny bool
}

func (f *fakeGate) Acquire(ctx context.Context, why string) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deny {
		return nil, NewErr(ReasonSessionsActive, "注入：存在运行中会话")
	}
	f.held++
	released := false
	return func() {
		if released {
			return
		}
		released = true
		f.mu.Lock()
		f.held--
		f.mu.Unlock()
	}, nil
}

type fakeQuiescence struct {
	mu  sync.Mutex
	err error
}

func (f *fakeQuiescence) Check(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

type fakeIdentity struct {
	mu        sync.Mutex
	responses map[string]ProfileIdentity // token → 身份
	calls     int
}

func (f *fakeIdentity) FetchProfile(ctx context.Context, accessToken string) (ProfileIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if id, ok := f.responses[accessToken]; ok {
		return id, nil
	}
	return ProfileIdentity{}, NewErr(ReasonIdentityUnverified, "注入：token 无已知身份")
}

func (f *fakeIdentity) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// ---- 故障注入文件操作 ----

type failOps struct {
	inner    writeFileOps
	failStep string // create-temp/chmod-temp/write-temp/sync-temp/close-temp/rename/sync-directory
	failAt   int    // 第 N 次该步骤调用失败（跨所有步骤累计计数）；0=不失败
	calls    map[string]int
}

func newFailOps(failStep string, failAt int) *failOps {
	return &failOps{inner: osWriteFileOps{}, failStep: failStep, failAt: failAt, calls: map[string]int{}}
}

func (o *failOps) tick(step string) bool {
	o.calls[step]++
	return o.failStep == step && o.calls[step] == o.failAt
}

func (o *failOps) CreateTemp(dir, pattern string) (*os.File, error) {
	if o.tick("create-temp") {
		return nil, fmt.Errorf("injected create-temp failure")
	}
	return o.inner.CreateTemp(dir, pattern)
}
func (o *failOps) Sync(f *os.File) error {
	if o.tick("sync-temp") {
		return fmt.Errorf("injected sync failure")
	}
	return o.inner.Sync(f)
}
func (o *failOps) Close(f *os.File) error {
	if o.tick("close-temp") {
		return fmt.Errorf("injected close failure")
	}
	return o.inner.Close(f)
}
func (o *failOps) Rename(oldPath, newPath string) error {
	if o.tick("rename") {
		return fmt.Errorf("injected rename failure")
	}
	return o.inner.Rename(oldPath, newPath)
}
func (o *failOps) Remove(path string) error { return o.inner.Remove(path) }
func (o *failOps) Chmod(path string, mode os.FileMode) error {
	if o.tick("chmod-target") {
		return fmt.Errorf("injected chmod failure")
	}
	return o.inner.Chmod(path, mode)
}
func (o *failOps) SyncDir(dir string) error {
	if o.tick("sync-directory") {
		return fmt.Errorf("injected directory sync failure")
	}
	return o.inner.SyncDir(dir)
}

// ---- fake HTTP transport ----

type fakeTransport struct {
	mu      sync.Mutex
	handler func(req *http.Request) (*http.Response, error)
	calls   []*http.Request
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return nil, fmt.Errorf("injected transport failure")
	}
	return handler(req)
}

func (f *fakeTransport) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// ---- 测试环境 ----

type testEnv struct {
	t          *testing.T
	dir        string
	home       string
	pool       *fakePoolStore
	keychain   *fakeKeychain
	files      *FileCredentialStore
	filesOps   writeFileOps
	gate       *fakeGate
	quiescence *fakeQuiescence
	identity   IdentityProvider
	identityFake *fakeIdentity
	clock      *fakeClock
	transport  *fakeTransport
	svc        *Service
	native     NativeCredentialStore
	nativeOverride NativeCredentialStore
	logs       []string
	events     []Event
	logMu      sync.Mutex
}

// newTestEnv 建立账号 A 的合成双端登录环境并构造 Service。
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	env := &testEnv{
		t:          t,
		dir:        dir,
		home:       filepath.Join(dir, "home"),
		pool:       &fakePoolStore{},
		keychain:   &fakeKeychain{},
		gate:       &fakeGate{},
		quiescence: &fakeQuiescence{},
		identityFake: &fakeIdentity{responses: map[string]ProfileIdentity{}},
		clock:      &fakeClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)},
		transport:  &fakeTransport{},
	}
	env.identity = env.identityFake
	env.filesOps = osWriteFileOps{}
	env.seedAccountA()
	env.rebuildService()
	return env
}

// seedAccountA 写入账号 A 的三处原生投影与身份映射。
func (e *testEnv) seedAccountA() {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Join(e.home, ".pi", "agent"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.claudeJSONPath(), syntheticClaudeJSON(syntheticUUIDA, "user-amagi-a", "a@example.com"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.piAuthPath(), syntheticPiAuth("pi-a-1", futureExpiresMS(e.clock.Now())), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.keychain.items = []fakeKCItem{{account: "claude", data: syntheticKeychainItem("cc-a-1")}}
	e.identityFake.responses["cc-a-1"] = ProfileIdentity{AccountUUID: syntheticUUIDA, OrganizationUUID: syntheticOrgA, SeatTier: "max"}
	e.identityFake.responses["pi-a-1"] = ProfileIdentity{AccountUUID: syntheticUUIDA, OrganizationUUID: syntheticOrgA, SeatTier: "max"}
}

// seedAccountB 把原生槽位整体换为账号 B（模拟外部登录 B）。
func (e *testEnv) seedAccountB() {
	e.t.Helper()
	if err := os.WriteFile(e.claudeJSONPath(), syntheticClaudeJSON(syntheticUUIDB, "user-amagi-b", "b@example.com"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.piAuthPath(), syntheticPiAuth("pi-b-1", futureExpiresMS(e.clock.Now())), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.keychain.items = []fakeKCItem{{account: "claude", data: syntheticKeychainItem("cc-b-1")}}
	e.identityFake.responses["cc-b-1"] = ProfileIdentity{AccountUUID: syntheticUUIDB, OrganizationUUID: syntheticOrgB, SeatTier: "pro"}
	e.identityFake.responses["pi-b-1"] = ProfileIdentity{AccountUUID: syntheticUUIDB, OrganizationUUID: syntheticOrgB, SeatTier: "pro"}
}

// enrollAccountB 完整走一遍录入 B（capture A → begin → 换原生 → capture B）。
func (e *testEnv) enrollAccountB() (idA, idB string) {
	e.t.Helper()
	ctx := context.Background()
	dtoA, err := e.svc.CaptureFirstAccount(ctx, "account-a", 0)
	if err != nil {
		e.t.Fatalf("capture A: %v", err)
	}
	idA = dtoA.ID
	pool, err := e.svc.LoadPool(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	enrollID, err := e.svc.BeginEnrollment(ctx, pool.Revision)
	if err != nil {
		e.t.Fatalf("begin enrollment: %v", err)
	}
	pool, err = e.svc.LoadPool(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	e.seedAccountB()
	dtoB, err := e.svc.CaptureEnrolledAccount(ctx, enrollID, "account-b", pool.Revision)
	if err != nil {
		e.t.Fatalf("capture B: %v", err)
	}
	return idA, dtoB.ID
}

func (e *testEnv) claudeJSONPath() string { return filepath.Join(e.home, ".claude.json") }
func (e *testEnv) piAuthPath() string    { return filepath.Join(e.home, ".pi", "agent", "auth.json") }

// rebuildService 按当前注入重建 Service（文件 ops/native 变更后调用）。
func (e *testEnv) rebuildService() {
	e.t.Helper()
	e.files = NewFileCredentialStore(FileCredentialPaths{ClaudeJSONPath: e.claudeJSONPath(), PiAuthPath: e.piAuthPath()}, e.filesOps)
	native := NativeCredentialStore(NewCombinedNativeStore(e.files, e.keychain))
	e.native = native
	if e.nativeOverride != nil {
		native = e.nativeOverride
	}
	e.logMu.Lock()
	logs, events := e.logs, e.events
	e.logMu.Unlock()
	svc, err := NewService(Deps{
		Pool:       e.pool,
		Native:     native,
		LockPath:   filepath.Join(e.dir, "lock", "claude-accounts.lock"),
		PiAuthPath: e.piAuthPath(),
		Gate:       e.gate,
		Quiescence: e.quiescence,
		Identity:   e.identity,
		HTTPClient: NewConstrainedHTTPClient(e.transport),
		Clock:      e.clock.Now,
		Logf: func(format string, args ...any) {
			e.logMu.Lock()
			defer e.logMu.Unlock()
			logs = append(logs, fmt.Sprintf(format, args...))
			e.logs = logs
		},
		EmitEvent: func(ev Event) {
			e.logMu.Lock()
			defer e.logMu.Unlock()
			events = append(events, ev)
			e.events = events
		},
	})
	if err != nil {
		e.t.Fatalf("NewService: %v", err)
	}
	e.svc = svc
}

func (e *testEnv) allLogs() []string {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	return append([]string(nil), e.logs...)
}

// svcNative 返回当前底层 native 适配器。
func (e *testEnv) svcNative() NativeCredentialStore { return e.native }

// setNative 注入 native 包装器并重建 Service。
func (e *testEnv) setNative(n NativeCredentialStore) {
	e.nativeOverride = n
	e.rebuildService()
}

// readJSON 读文件并校验 JSON。
func readJSON(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// assertReason 断言错误 reason。
func assertReason(t *testing.T, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际 nil", want)
	}
	got := ReasonOf(err)
	if got != want {
		t.Fatalf("期望错误 reason %s，实际 %s（%v）", want, got, err)
	}
}
