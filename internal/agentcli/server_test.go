package agentcli

// server_test.go — Server 全链路测试（真实回环 listener + 端点文件发现，
// 对齐 R1 §9 "真服务" 测试组织）：三重校验、令牌、禁用 503、confirm 门、
// 参数校验归一、错误码映射、panic 恢复、审计脱敏、令牌轮换、请求体上限。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"amagi-codebox/internal/agentcli/cmds"
	"amagi-codebox/internal/agentcli/spec"
)

// ---- 测试命令表与 Handler ----

func testTable() []spec.Command {
	return []spec.Command{
		{
			Path:    []string{"demo", "echo"},
			Summary: "回显参数（read）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "name", Type: spec.TypeString, Required: true, Positional: true},
				{Name: "count", Type: spec.TypeInt, Default: 2},
				{Name: "loud", Type: spec.TypeBool},
				{Name: "tags", Type: spec.TypeStrings},
				{Name: "mode", Type: spec.TypeString, Enum: []string{"a", "b"}},
				{Name: "payload", Type: spec.TypeJSON},
			},
		},
		{
			Path:    []string{"demo", "save"},
			Summary: "保存配置（mutate）",
			Risk:    spec.RiskMutate,
			Params: []spec.Param{
				{Name: "name", Type: spec.TypeString, Required: true},
				{Name: "apiKey", Type: spec.TypeString, Secret: true, Stdin: true},
			},
		},
		{
			Path:    []string{"demo", "launch"},
			Summary: "启动（exec）",
			Risk:    spec.RiskExec,
			Confirm: "LAUNCH",
			Params: []spec.Param{
				{Name: "appType", Type: spec.TypeString, Required: true},
			},
		},
		{
			Path:    []string{"demo", "purge"},
			Summary: "清空（destructive）",
			Risk:    spec.RiskDestructive,
			Confirm: "PURGE",
		},
		{
			Path:    []string{"demo", "boom"},
			Summary: "崩溃（exec）",
			Risk:    spec.RiskExec,
			Confirm: "BOOM",
		},
		{
			Path:    []string{"demo", "fail"},
			Summary: "按 code 返回错误（read）",
			Risk:    spec.RiskRead,
			Params: []spec.Param{
				{Name: "code", Type: spec.TypeString, Required: true},
			},
		},
	}
}

func testHandlers() map[string]spec.Handler {
	return map[string]spec.Handler{
		"demo.echo": func(_ context.Context, args spec.Args) (any, error) {
			return map[string]any{"args": map[string]any(args)}, nil
		},
		"demo.save": func(_ context.Context, args spec.Args) (any, error) {
			return map[string]any{"saved": args.String("name")}, nil
		},
		"demo.launch": func(_ context.Context, _ spec.Args) (any, error) {
			return map[string]any{"sessionId": "s-1"}, nil
		},
		"demo.purge": func(_ context.Context, _ spec.Args) (any, error) {
			return map[string]any{"removed": 0}, nil
		},
		"demo.boom": func(_ context.Context, _ spec.Args) (any, error) {
			panic("boom: intentional test panic")
		},
		"demo.fail": func(_ context.Context, args spec.Args) (any, error) {
			code := args.String("code")
			if code == "plain" {
				return nil, fmt.Errorf("plain error %s", code)
			}
			return nil, spec.Errorf(code, "测试错误 %s", code)
		},
	}
}

// startTestServer 启动真实 Server 并从端点文件取 url/token（真实客户端路径）。
func startTestServer(t *testing.T) (*Server, spec.Endpoint) {
	t.Helper()
	dir := t.TempDir()
	s := NewServer(dir, "test-version", testTable(), testHandlers(), nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(s.Stop)
	ep, err := readEndpointFile(endpointPath(dir))
	if err != nil {
		t.Fatalf("read endpoint file: %v", err)
	}
	return s, ep
}

// call 以 CLI 客户端语义发起请求（默认合法回环请求形态）。
func call(t *testing.T, method, url, token, path string, body any) (int, spec.Envelope) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(spec.HeaderToken, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var env spec.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return resp.StatusCode, env
}

// ---- 三重校验 ----

func TestServerPingTokenEnforced(t *testing.T) {
	_, ep := startTestServer(t)

	// 无令牌 → 401。
	status, env := call(t, http.MethodGet, ep.URL, "", spec.PathPing, nil)
	if status != http.StatusUnauthorized || env.OK || env.Error == nil || env.Error.Code != spec.ErrUnauthorized {
		t.Fatalf("无令牌 ping: status=%d env=%+v, want 401 unauthorized", status, env)
	}
	// 错误令牌 → 401。
	status, _ = call(t, http.MethodGet, ep.URL, "bogus-token", spec.PathPing, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("错误令牌 ping: status=%d, want 401", status)
	}
	// 正确令牌 → 200 + PingData（tableHash 与 cmds.Hash 一致）。
	status, env = call(t, http.MethodGet, ep.URL, ep.CLIToken, spec.PathPing, nil)
	if status != http.StatusOK || !env.OK {
		t.Fatalf("正确令牌 ping: status=%d env=%+v", status, env)
	}
	data, _ := env.Data.(map[string]any)
	if data["appVersion"] != "test-version" {
		t.Fatalf("ping appVersion=%v, want test-version", data["appVersion"])
	}
	if data["tableHash"] != cmds.Hash(testTable()) {
		t.Fatalf("ping tableHash=%v 与 cmds.Hash 不一致（客户端版本偏差提示会误报）", data["tableHash"])
	}
}

func TestServerRejectsNonLoopbackPeer(t *testing.T) {
	s, ep := startTestServer(t)
	// 直接构造带外部 RemoteAddr 的请求打内部 handler（httptest 无法伪造源地址）。
	req := httptest.NewRequest(http.MethodGet, ep.URL+spec.PathPing, nil)
	req.Header.Set(spec.HeaderToken, ep.CLIToken)
	req.RemoteAddr = "192.168.1.5:44444"
	rec := httptest.NewRecorder()
	s.buildHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非回环对端: status=%d, want 403", rec.Code)
	}
	var env spec.Envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error == nil || env.Error.Code != spec.ErrForbidden {
		t.Fatalf("非回环对端错误码=%+v, want forbidden", env.Error)
	}
}

func TestServerRejectsOriginHeader(t *testing.T) {
	_, ep := startTestServer(t)
	req, err := http.NewRequest(http.MethodGet, ep.URL+spec.PathPing, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(spec.HeaderToken, ep.CLIToken)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("带 Origin: status=%d, want 403", resp.StatusCode)
	}
}

func TestServerRejectsForeignHost(t *testing.T) {
	s, ep := startTestServer(t)
	// 合法 Host 必须带 listener 实际端口（评审 D-09：端口精确匹配）。
	port := hostPortOf(t, ep)
	for _, host := range []string{"evil.example.com", "10.0.0.9:1234", "localhost.evil.com", "127.0.0.2:" + port} {
		req := httptest.NewRequest(http.MethodGet, ep.URL+spec.PathPing, nil)
		req.Header.Set(spec.HeaderToken, ep.CLIToken)
		req.Host = host
		req.RemoteAddr = "127.0.0.1:55555"
		rec := httptest.NewRecorder()
		s.buildHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Host=%q: status=%d, want 403", host, rec.Code)
		}
	}
	// 合法 Host（含端口、IPv6、localhost）放行。
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port} {
		req := httptest.NewRequest(http.MethodGet, ep.URL+spec.PathPing, nil)
		req.Header.Set(spec.HeaderToken, ep.CLIToken)
		req.Host = host
		req.RemoteAddr = "127.0.0.1:55555"
		rec := httptest.NewRecorder()
		s.buildHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("合法 Host=%q: status=%d, want 200", host, rec.Code)
		}
	}
}

// hostPortOf 取端点 URL 的端口（Host 精确校验测试用）。
func hostPortOf(t *testing.T, ep spec.Endpoint) string {
	t.Helper()
	u, err := url.Parse(ep.URL)
	if err != nil || u.Port() == "" {
		t.Fatalf("endpoint URL 应带端口: %q err=%v", ep.URL, err)
	}
	return u.Port()
}

// ---- confirm 门 ----

func TestServerConfirmGate(t *testing.T) {
	_, ep := startTestServer(t)
	const path = spec.PathCommand + "demo.launch"
	body := func(confirm string, args map[string]any) any {
		return spec.Request{Args: args, Confirm: confirm}
	}
	validArgs := map[string]any{"appType": "claudecode"}

	// 缺 confirm → 409。
	status, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, path, body("", validArgs))
	if status != http.StatusConflict || env.Error == nil || env.Error.Code != spec.ErrConfirmationRequired {
		t.Fatalf("缺 confirm: status=%d env=%+v, want 409 confirmation_required", status, env)
	}
	// 错误确认值 → 409。
	status, _ = call(t, http.MethodPost, ep.URL, ep.CLIToken, path, body("PURGE", validArgs))
	if status != http.StatusConflict {
		t.Fatalf("错误确认值: status=%d, want 409", status)
	}
	// 正确确认值 → 200。
	status, env = call(t, http.MethodPost, ep.URL, ep.CLIToken, path, body("LAUNCH", validArgs))
	if status != http.StatusOK || !env.OK {
		t.Fatalf("正确确认值: status=%d env=%+v, want 200", status, env)
	}
	// destructive 命令同样强制。
	status, _ = call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.purge", body("LAUNCH", nil))
	if status != http.StatusConflict {
		t.Fatalf("destructive 错误确认值: status=%d, want 409", status)
	}
	status, _ = call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.purge", body("PURGE", nil))
	if status != http.StatusOK {
		t.Fatalf("destructive 正确确认值: status=%d, want 200", status)
	}
}

// ---- 参数校验与归一 ----

func TestServerArgValidation(t *testing.T) {
	_, ep := startTestServer(t)
	const path = spec.PathCommand + "demo.echo"

	cases := []struct {
		name    string
		args    map[string]any
		wantErr bool
	}{
		{"缺少必填", map[string]any{}, true},
		{"未知参数", map[string]any{"name": "x", "bogus": 1}, true},
		{"int 传字符串", map[string]any{"name": "x", "count": "3"}, true},
		{"int 传小数", map[string]any{"name": "x", "count": 1.5}, true},
		{"int 传布尔", map[string]any{"name": "x", "count": true}, true},
		{"枚举外取值", map[string]any{"name": "x", "mode": "c"}, true},
		{"bool 传字符串", map[string]any{"name": "x", "loud": "yes"}, true},
		{"数组元素非字符串", map[string]any{"name": "x", "tags": []any{1, 2}}, true},
		{"JSON 字面量非法", map[string]any{"name": "x", "payload": "{not json"}, true},
	}
	for _, tc := range cases {
		status, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, path, spec.Request{Args: tc.args})
		if !tc.wantErr {
			continue
		}
		if status != http.StatusBadRequest || env.Error == nil || env.Error.Code != spec.ErrBadArgs {
			t.Fatalf("%s: status=%d env=%+v, want 400 bad_args", tc.name, status, env)
		}
	}

	// 归一与默认值兜底：count 缺省=2（Default）、tags → []string、payload
	// JSON 字面量 → 解析后的对象、整数 → 数字。
	status, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, path, spec.Request{Args: map[string]any{
		"name":    "x",
		"tags":    []any{"t1", "t2"},
		"mode":    "b",
		"payload": `{"k":[1,2]}`,
	}})
	if status != http.StatusOK || !env.OK {
		t.Fatalf("合法请求: status=%d env=%+v", status, env)
	}
	data, _ := env.Data.(map[string]any)
	args, _ := data["args"].(map[string]any)
	if got := args["count"]; got != float64(2) { // 回显经 JSON，int64(2) 序列化回 2
		t.Fatalf("count 默认值兜底: got %v (%T), want 2", got, got)
	}
	tags, ok := args["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "t1" {
		t.Fatalf("tags 归一: got %v", args["tags"])
	}
	payload, _ := args["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("payload JSON 字面量应解析为对象, got %T", args["payload"])
	}
	if _, ok := args["loud"]; ok {
		t.Fatal("loud 未提供且无默认值，不应出现在 args")
	}
}

// ---- 错误码映射与 panic 恢复 ----

func TestServerErrorCodeMapping(t *testing.T) {
	_, ep := startTestServer(t)
	const path = spec.PathCommand + "demo.fail"
	cases := []struct {
		code       string
		wantStatus int
	}{
		{spec.ErrBadArgs, 400},
		{spec.ErrNotFound, 404},
		{spec.ErrConflict, 409},
		{spec.ErrForbiddenCommand, 403},
		{spec.ErrNotReady, 503},
		{spec.ErrInternal, 500},
		{"unknown_code_xyz", 500},
		{"plain", 500},
	}
	for _, tc := range cases {
		status, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, path, spec.Request{Args: map[string]any{"code": tc.code}})
		if status != tc.wantStatus {
			t.Fatalf("code=%s: status=%d, want %d", tc.code, status, tc.wantStatus)
		}
		if env.OK {
			t.Fatalf("code=%s: 不应 ok", tc.code)
		}
		if tc.code == "plain" {
			if env.Error == nil || env.Error.Code != spec.ErrInternal {
				t.Fatalf("普通 error 应映射 internal, got %+v", env.Error)
			}
		}
	}
	// 未知命令 → 404 unknown_command。
	status, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.nope", spec.Request{})
	if status != http.StatusNotFound || env.Error == nil || env.Error.Code != spec.ErrUnknownCommand {
		t.Fatalf("未知命令: status=%d env=%+v, want 404 unknown_command", status, env)
	}
}

func TestServerPanicRecovered(t *testing.T) {
	_, ep := startTestServer(t)
	status, env := call(t, http.MethodPost, ep.URL, ep.CLIToken, spec.PathCommand+"demo.boom", spec.Request{Confirm: "BOOM"})
	if status != http.StatusInternalServerError || env.OK || env.Error == nil || env.Error.Code != spec.ErrInternal {
		t.Fatalf("panic 恢复: status=%d env=%+v, want 500 internal", status, env)
	}
	// 服务仍存活（panic 不拖垮进程/listener）。
	status, _ = call(t, http.MethodGet, ep.URL, ep.CLIToken, spec.PathPing, nil)
	if status != http.StatusOK {
		t.Fatalf("panic 后服务应存活, ping status=%d", status)
	}
}

// ---- 请求体上限 ----

// postRaw 以原始字符串体发 POST（保留非规范 JSON 形态供解析边界测试）。
func postRaw(t *testing.T, url, token, path, body string) (int, spec.Envelope) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(spec.HeaderToken, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var env spec.Envelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env
}

func TestServerBodyLimit(t *testing.T) {
	_, ep := startTestServer(t)
	big := strings.Repeat("x", maxBodyBytes+1024)
	status, env := postRaw(t, ep.URL, ep.CLIToken, spec.PathCommand+"demo.echo", `{"args":{"name":"`+big+`"}}`)
	// 超限 → 413（包络错误码 bad_args，客户端退出码仍按 bad_args 映射）。
	if status != http.StatusRequestEntityTooLarge || env.Error == nil || env.Error.Code != spec.ErrBadArgs {
		t.Fatalf("超限请求体: status=%d env=%+v, want 413 bad_args", status, env)
	}
}

// TestServerBodyMustBeExactlyOneJSONValue — 请求体必须恰好一个 JSON 值
//（评审 D-11）：尾随第二 JSON 值/垃圾内容 → 400 bad_args；尾随纯空白正常。
func TestServerBodyMustBeExactlyOneJSONValue(t *testing.T) {
	_, ep := startTestServer(t)
	const path = spec.PathCommand + "demo.echo"

	for name, body := range map[string]string{
		"尾随第二 JSON 值":  `{"args":{"name":"x"}} {"args":{"name":"y"}}`,
		"尾随垃圾文本":     `{"args":{"name":"x"}} garbage`,
		"尾随数字":       `{"args":{"name":"x"}} 42`,
		"首值之前有内容":  `junk {"args":{"name":"x"}}`,
	} {
		status, env := postRaw(t, ep.URL, ep.CLIToken, path, body)
		if status != http.StatusBadRequest || env.Error == nil || env.Error.Code != spec.ErrBadArgs {
			t.Fatalf("%s: status=%d env=%+v, want 400 bad_args", name, status, env)
		}
	}
	// 尾随纯空白 → 正常执行。
	status, env := postRaw(t, ep.URL, ep.CLIToken, path, "{\"args\":{\"name\":\"x\"}}\n\n  \t")
	if status != http.StatusOK || !env.OK {
		t.Fatalf("尾随纯空白应正常: status=%d env=%+v", status, env)
	}
}

// TestServerIntArgRejectsNonIntegerAndOutOfRange — 服务端 int 归一拒绝非整数
// 与越界（评审 D-10）：1e100/1.5/bool/int64 溢出字面量一律 bad_args，不得经
// float64 舍入静默接受。
func TestServerIntArgRejectsNonIntegerAndOutOfRange(t *testing.T) {
	_, ep := startTestServer(t)
	const path = spec.PathCommand + "demo.echo"
	for name, body := range map[string]string{
		"指数形式越界":  `{"args":{"name":"x","count":1e100}}`,
		"小数":       `{"args":{"name":"x","count":1.5}}`,
		"布尔":       `{"args":{"name":"x","count":true}}`,
		"字符串数字":    `{"args":{"name":"x","count":"3"}}`,
		"int64 溢出": `{"args":{"name":"x","count":9223372036854775808}}`,
		"int64 下溢": `{"args":{"name":"x","count":-9223372036854775809}}`,
	} {
		status, env := postRaw(t, ep.URL, ep.CLIToken, path, body)
		if status != http.StatusBadRequest || env.Error == nil || env.Error.Code != spec.ErrBadArgs {
			t.Fatalf("%s: status=%d env=%+v, want 400 bad_args", name, status, env)
		}
	}
	// 合法整数（含 int64 边界）仍接受，且不经舍入。
	status, env := postRaw(t, ep.URL, ep.CLIToken, path, `{"args":{"name":"x","count":9223372036854775807}}`)
	if status != http.StatusOK || !env.OK {
		t.Fatalf("int64 上界应接受: status=%d env=%+v", status, env)
	}
	data, _ := env.Data.(map[string]any)
	args, _ := data["args"].(map[string]any)
	if got, _ := args["count"].(float64); got != 9223372036854775807 {
		t.Fatalf("int64 上界回显: got %v, want 9223372036854775807", args["count"])
	}
}

// ---- ctx 预检与审计标注（评审 D-06 服务端侧）----

// TestServerCtxCancelledBeforeInvokeSkipsHandler — 请求 ctx 已取消时不再
// 调用 Handler（不触发副作用），审计如实记录。
func TestServerCtxCancelledBeforeInvokeSkipsHandler(t *testing.T) {
	dir := t.TempDir()
	invoked := false
	table := []spec.Command{{
		Path: []string{"demo", "mut"}, Risk: spec.RiskMutate, Summary: "可变命令",
	}}
	handlers := map[string]spec.Handler{
		"demo.mut": func(context.Context, spec.Args) (any, error) {
			invoked = true
			return map[string]any{"mutated": true}, nil
		},
	}
	s := NewServer(dir, "v", table, handlers, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Stop)
	ep, err := readEndpointFile(endpointPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 先取消
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL+spec.PathCommand+"demo.mut", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(spec.HeaderToken, ep.CLIToken)
	req.RemoteAddr = "127.0.0.1:55555"
	req.Host = hostWithListenerPort(t, ep)
	rec := httptest.NewRecorder()
	s.buildHandler().ServeHTTP(rec, req)

	if invoked {
		t.Fatal("ctx 已取消时不应调用 Handler（副作用风险）")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("预检拒绝: status=%d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	lines := auditLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("审计行数=%d, want 1", len(lines))
	}
	if lines[0]["code"] != spec.ErrInternal || lines[0]["ctxCancelled"] != true {
		t.Fatalf("审计应标注取消且未执行: %v", lines[0])
	}
}

// TestServerCtxCancelledAfterHandlerStillReturnsResultWithAuditNote —
// Handler 完成后 ctx 已取消：结果仍如实返回（不篡改成功语义），审计标注
// ctxCancelled（不可取消的 Handler 在客户端超时后仍会完成）。
func TestServerCtxCancelledAfterHandlerStillReturnsResultWithAuditNote(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	table := []spec.Command{{
		Path: []string{"demo", "slow"}, Risk: spec.RiskMutate, Summary: "慢命令",
	}}
	handlers := map[string]spec.Handler{
		"demo.slow": func(context.Context, spec.Args) (any, error) {
			close(started)
			<-release // 模拟不可取消的工作：不消费 ctx
			return map[string]any{"done": true}, nil
		},
	}
	s := NewServer(dir, "v", table, handlers, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Stop)
	ep, err := readEndpointFile(endpointPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL+spec.PathCommand+"demo.slow", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(spec.HeaderToken, ep.CLIToken)
	req.RemoteAddr = "127.0.0.1:55555"
	req.Host = hostWithListenerPort(t, ep)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.buildHandler().ServeHTTP(rec, req)
	}()
	<-started
	cancel() // 客户端放弃等待
	close(release)
	<-done

	var env spec.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是包络: %v; body=%s", err, rec.Body.String())
	}
	if !env.OK {
		t.Fatalf("Handler 已完成，结果应如实返回: %+v", env)
	}
	data, _ := env.Data.(map[string]any)
	if data["done"] != true {
		t.Fatalf("结果数据丢失: %+v", env.Data)
	}
	lines := auditLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("审计行数=%d, want 1", len(lines))
	}
	if lines[0]["code"] != resultCodeOK || lines[0]["ctxCancelled"] != true {
		t.Fatalf("审计应记 ok 且标注 ctxCancelled: %v", lines[0])
	}
}

// ---- 禁用 503 与文件撤销、令牌轮换 ----

func TestServerDisabledReturns503AndRevokesEndpoint(t *testing.T) {
	s, ep := startTestServer(t)

	if err := s.SetEnabled(false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// 端点文件撤销。
	if _, err := os.Stat(endpointPath(s.configDir)); !os.IsNotExist(err) {
		t.Fatalf("禁用后端点文件应被撤销, stat err=%v", err)
	}
	// 旧令牌请求 → 503 cli_disabled（listener 保留存活）。
	status, env := call(t, http.MethodGet, ep.URL, ep.CLIToken, spec.PathPing, nil)
	if status != http.StatusServiceUnavailable || env.Error == nil || env.Error.Code != spec.ErrDisabled {
		t.Fatalf("禁用后: status=%d env=%+v, want 503 cli_disabled", status, env)
	}
	st := s.Status()
	if st.Running != true || st.Enabled != false {
		t.Fatalf("禁用态: running=%v enabled=%v, want running=true enabled=false", st.Running, st.Enabled)
	}

	// 重新开启：重发布端点文件 + 全新令牌，旧令牌失效。
	if err := s.SetEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ep2, err := readEndpointFile(endpointPath(s.configDir))
	if err != nil {
		t.Fatalf("重新开启后读端点文件: %v", err)
	}
	if ep2.CLIToken == ep.CLIToken {
		t.Fatal("重新开启应轮换令牌（旧令牌曾写入撤销前的文件）")
	}
	status, _ = call(t, http.MethodGet, ep.URL, ep.CLIToken, spec.PathPing, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("旧令牌在重新开启后应失效, got %d", status)
	}
	status, _ = call(t, http.MethodGet, ep2.URL, ep2.CLIToken, spec.PathPing, nil)
	if status != http.StatusOK {
		t.Fatalf("新令牌应可用, got %d", status)
	}
}

func TestServerRotateTokenInvalidatesOld(t *testing.T) {
	s, ep := startTestServer(t)
	if err := s.RotateToken(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// 旧令牌立即失效。
	status, _ := call(t, http.MethodGet, ep.URL, ep.CLIToken, spec.PathPing, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("旧令牌轮换后应 401, got %d", status)
	}
	// 端点文件里是新令牌（CLI 客户端重新发现即可用）。
	ep2, err := readEndpointFile(endpointPath(s.configDir))
	if err != nil {
		t.Fatalf("读端点文件: %v", err)
	}
	if ep2.CLIToken == ep.CLIToken {
		t.Fatal("端点文件应已重写为新令牌")
	}
	status, _ = call(t, http.MethodGet, ep2.URL, ep2.CLIToken, spec.PathPing, nil)
	if status != http.StatusOK {
		t.Fatalf("新令牌应可用, got %d", status)
	}
	_ = s
}

// ---- hashTable 与 cmds.Hash 防漂移 ----

func TestHashTableMatchesCmdsHash(t *testing.T) {
	table := cmds.Table()
	if hashTable(table) != cmds.Hash(table) {
		t.Fatal("服务端 hashTable 与 cmds.Hash 算法漂移（客户端版本偏差提示会误报）")
	}
}
