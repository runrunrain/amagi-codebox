package client

// run_test.go — 测试基建（假命令表 + httptest 假服务端，不依赖 A1 Server 包）
// 与预检顺序契约（解析→校验→confirm 先于端点发现，DESIGN §2）。

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/agentcli/cmds"
	"amagi-codebox/internal/agentcli/spec"
)

// testTable 是自建的假命令表（覆盖 read/mutate/exec/destructive 与全部参数类型）。
func testTable() []spec.Command {
	return []spec.Command{
		{Path: []string{"status"}, Summary: "应用总览", Risk: spec.RiskRead, Examples: []string{"codebox-agent status"}},
		{Path: []string{"session", "list"}, Summary: "列出会话", Risk: spec.RiskRead, Params: []spec.Param{
			{Name: "status", Type: spec.TypeString, Enum: []string{"running", "stopped"}, Desc: "按状态过滤"},
		}},
		{Path: []string{"session", "launch"}, Summary: "启动会话", Risk: spec.RiskExec, Confirm: "LAUNCH", Params: []spec.Param{
			{Name: "appType", Type: spec.TypeString, Required: true, Positional: true, Enum: []string{"claudecode", "pi"}},
			{Name: "mode", Type: spec.TypeString, Default: "embedded"},
			{Name: "headroom", Type: spec.TypeBool, Desc: "启用 headroom"},
		}, Examples: []string{"codebox-agent session launch claudecode"}, Notes: "返回 sessionId"},
		{Path: []string{"session", "send"}, Summary: "发送输入", Risk: spec.RiskExec, Confirm: "SEND", Params: []spec.Param{
			{Name: "id", Type: spec.TypeString, Required: true, Positional: true},
			{Name: "text", Type: spec.TypeString, Required: true, Positional: true, Stdin: true},
			{Name: "enter", Type: spec.TypeBool},
		}},
		{Path: []string{"session", "remove"}, Summary: "删除会话", Risk: spec.RiskDestructive, Confirm: "REMOVE", Params: []spec.Param{
			{Name: "id", Type: spec.TypeString, Required: true, Positional: true},
		}},
		{Path: []string{"provider", "save"}, Summary: "保存 provider", Risk: spec.RiskMutate, Params: []spec.Param{
			{Name: "data", Type: spec.TypeJSON, Required: true, Desc: "provider JSON"},
			{Name: "json", Type: spec.TypeJSON, Desc: "与全局 --json 同名的参数（真实表 provider.save 用此名）"},
		}},
		{Path: []string{"key", "set"}, Summary: "写入密钥", Risk: spec.RiskMutate, Params: []spec.Param{
			{Name: "name", Type: spec.TypeString, Required: true, Positional: true},
			{Name: "value", Type: spec.TypeString, Required: true, Positional: true, Stdin: true, Secret: true},
		}},
		{Path: []string{"tags"}, Summary: "标签查询", Risk: spec.RiskRead, Params: []spec.Param{
			{Name: "items", Type: spec.TypeStrings, Positional: true},
		}},
		{Path: []string{"usage", "daily"}, Summary: "按日用量", Risk: spec.RiskRead, Timeout: 30, Params: []spec.Param{
			{Name: "days", Type: spec.TypeInt, Desc: "天数"},
		}},
		{Path: []string{"config"}, Summary: "配置概况", Risk: spec.RiskRead},
		{Path: []string{"config", "get"}, Summary: "读取配置", Risk: spec.RiskRead, Params: []spec.Param{
			{Name: "key", Type: spec.TypeString, Required: true, Positional: true},
		}},
	}
}

func runTest(t *testing.T, argv []string, stdin string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), argv, strings.NewReader(stdin), &out, &errb, testTable(), "vtest")
	return code, out.String(), errb.String()
}

// statusForTest 复刻服务端 statusForCode 映射（假服务端用）。
func statusForTest(code string) int {
	switch code {
	case spec.ErrBadArgs:
		return 400
	case spec.ErrUnauthorized:
		return 401
	case spec.ErrForbidden, spec.ErrForbiddenCommand:
		return 403
	case spec.ErrUnknownCommand, spec.ErrNotFound:
		return 404
	case spec.ErrConfirmationRequired, spec.ErrConflict:
		return 409
	case spec.ErrDisabled, spec.ErrNotReady:
		return 503
	default:
		return 500
	}
}

// fakeOptions 在构造时固化（handler 闭包捕获，避免跨 goroutine 数据竞争）。
type fakeOptions struct {
	handler   func(name string, req spec.Request) (any, *spec.Error)
	tableHash string
	delay     time.Duration
}

type fakeServer struct {
	*httptest.Server
	token string
	mu    sync.Mutex
	// requests 记录 "METHOD name"（ping 不计），供断言“未发请求”。
	requests []string
	lastReq  spec.Request
}

func newFakeServer(t *testing.T, opts fakeOptions) *fakeServer {
	t.Helper()
	token := "test-token-0123456789abcdef"
	tableHash := opts.tableHash
	if tableHash == "" {
		tableHash = hashOf(testTable())
	}
	handler := opts.handler
	if handler == nil {
		handler = func(name string, req spec.Request) (any, *spec.Error) {
			return map[string]any{"cmd": name, "echo": req.Args, "confirm": req.Confirm}, nil
		}
	}
	fs := &fakeServer{token: token}
	writeEnv := func(w http.ResponseWriter, status int, env spec.Envelope) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(env)
	}
	gate := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get(spec.HeaderToken) != token {
			writeEnv(w, 401, spec.Envelope{OK: false, Error: spec.Errorf(spec.ErrUnauthorized, "令牌无效")})
			return false
		}
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc(spec.PathPing, func(w http.ResponseWriter, r *http.Request) {
		if !gate(w, r) {
			return
		}
		writeEnv(w, 200, spec.Envelope{OK: true, Data: spec.PingData{AppVersion: "vtest", PID: os.Getpid(), TableHash: tableHash}})
	})
	mux.HandleFunc(spec.PathCommand, func(w http.ResponseWriter, r *http.Request) {
		if !gate(w, r) {
			return
		}
		name := strings.TrimPrefix(r.URL.Path, spec.PathCommand)
		var req spec.Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		fs.mu.Lock()
		fs.requests = append(fs.requests, r.Method+" "+name)
		fs.lastReq = req
		fs.mu.Unlock()
		if opts.delay > 0 {
			time.Sleep(opts.delay)
		}
		data, herr := handler(name, req)
		if herr != nil {
			writeEnv(w, statusForTest(herr.Code), spec.Envelope{OK: false, Error: herr})
			return
		}
		writeEnv(w, 200, spec.Envelope{OK: true, Data: data})
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

func (fs *fakeServer) requestCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.requests)
}

func (fs *fakeServer) lastRequest() spec.Request {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastReq
}

// useEndpoint 把假服务端写进临时 home 并设 AMAGI_CODEBOX_HOME。
func (fs *fakeServer) useEndpoint(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeEndpointFile(t, home, fs.URL, os.Getpid(), fs.token)
	t.Setenv("AMAGI_CODEBOX_HOME", home)
	return home
}

func writeEndpointFile(t *testing.T, home, url string, pid int, token string) {
	t.Helper()
	dir := filepath.Join(home, "run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ep := spec.Endpoint{
		SchemaVersion: spec.EndpointSchemaVersion,
		URL:           url,
		PID:           pid,
		StartedAt:     "2025-01-01T00:00:00Z",
		AppVersion:    "vtest",
		CLIToken:      token,
	}
	b, err := json.Marshal(ep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "endpoint.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---- 预检顺序契约：无 endpoint 时用法错误仍 2、缺 --yes 仍 4 ----

func TestPrecheckOrderBeforeEndpoint(t *testing.T) {
	t.Setenv("AMAGI_CODEBOX_HOME", t.TempDir()) // 无 endpoint 文件

	// 用法错误（缺必填参数）→ 2，先于端点发现。
	code, _, errStr := runTest(t, []string{"session", "launch"}, "")
	if code != 2 {
		t.Fatalf("缺必填参数: code=%d err=%q", code, errStr)
	}
	// 枚举违规 → 2。
	code, _, _ = runTest(t, []string{"session", "list", "--status", "bogus"}, "")
	if code != 2 {
		t.Fatalf("枚举违规: code=%d", code)
	}
	// 参数合法但缺 --yes → 4（不发请求）。
	code, _, errStr = runTest(t, []string{"session", "launch", "claudecode"}, "")
	if code != 4 {
		t.Fatalf("缺 --yes: code=%d err=%q", code, errStr)
	}
	// --json 模式下本地失败也输出包络（stdout 单个 Envelope）。
	code, out, _ := runTest(t, []string{"--json", "session", "launch", "claudecode"}, "")
	if code != 4 {
		t.Fatalf("--json 缺 --yes: code=%d", code)
	}
	var env spec.Envelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("stdout 不是包络 JSON: %v\n%q", err, out)
	}
	if env.OK || env.Error == nil || env.Error.Code != spec.ErrConfirmationRequired {
		t.Fatalf("包络错误: %+v", env)
	}
	// 同理：用法错误在 --json 模式也走包络。
	code, out, _ = runTest(t, []string{"--json", "session", "launch"}, "")
	if code != 2 {
		t.Fatalf("--json 用法错误: code=%d", code)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil || env.OK || env.Error.Code != spec.ErrBadArgs {
		t.Fatalf("--json 用法错误包络: %v %+v", err, env)
	}
}

// ---- confirm 门：缺 --yes 不发请求；--yes 注入确认值 ----

func TestConfirmGate(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	fs.useEndpoint(t)

	code, _, _ := runTest(t, []string{"session", "remove", "s1"}, "")
	if code != 4 {
		t.Fatalf("缺 --yes 应返回 4，得到 %d", code)
	}
	if n := fs.requestCount(); n != 0 {
		t.Fatalf("缺 --yes 不应发请求，实际发了 %d 个", n)
	}
	code, _, _ = runTest(t, []string{"--yes", "session", "remove", "s1"}, "")
	if code != 0 {
		t.Fatalf("--yes 应成功，得到 %d", code)
	}
	if got := fs.lastRequest().Confirm; got != "REMOVE" {
		t.Fatalf("confirm 注入: got %q want REMOVE", got)
	}
	// read 命令 --yes 无副作用。
	code, _, _ = runTest(t, []string{"--yes", "status"}, "")
	if code != 0 {
		t.Fatalf("read 命令 +--yes: code=%d", code)
	}
}

// ---- 令牌不泄漏 ----

func TestTokenNeverLeaked(t *testing.T) {
	fs := newFakeServer(t, fakeOptions{})
	home := fs.useEndpoint(t)
	scenarios := [][]string{
		{"status"},                           // 成功
		{"session", "launch"},                // 用法错误
		{"session", "launch", "claudecode"},  // 缺确认
		{"provider", "save", "--data", "{x"}, // json 非法
		{"--json", "status"},                 // json 成功
	}
	for _, argv := range scenarios {
		code, out, errStr := runTest(t, argv, "")
		_ = code
		if strings.Contains(out+errStr, fs.token) {
			t.Fatalf("令牌泄漏到输出: argv=%v out=%q err=%q", argv, out, errStr)
		}
	}
	// 端点文件损坏场景：错误信息不含文件内容。
	if err := os.WriteFile(filepath.Join(home, "run", "endpoint.json"), []byte(`{"cliToken":"LEAK-CHECK"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, errStr := runTest(t, []string{"status"}, "")
	if strings.Contains(out+errStr, "LEAK-CHECK") {
		t.Fatalf("损坏端点文件内容泄漏: %q %q", out, errStr)
	}
}

// ---- 版本 ----

func TestVersion(t *testing.T) {
	code, out, _ := runTest(t, []string{"--version"}, "")
	if code != 0 || !strings.Contains(out, "vtest") {
		t.Fatalf("--version: code=%d out=%q", code, out)
	}
}

// ---- hash 与 cmds.Hash 锚定（客户端/命令表/服务端三处同算法）----

func TestHashAnchoredToCmds(t *testing.T) {
	if got, want := hashOf(cmds.Table()), cmds.Hash(cmds.Table()); got != want {
		t.Fatalf("客户端 hashOf 与 cmds.Hash 漂移: %s != %s", got, want)
	}
}
