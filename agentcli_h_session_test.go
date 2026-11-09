package main

// agentcli_h_session_test.go — 会话域 Handler 测试：
//   - 纯函数：stripTerminalControls（CSI/OSC/回车覆盖行/退格）、tailUTF8Safe、
//     renderSessionOutput（快照解析/since-seq/tail/raw）；
//   - launch 参数校验与五 AppType 分发映射（不启动真实 CLI 进程）；
//   - Handler 错误映射：不存在会话→not_found、已停止会话→conflict、
//     控制运行时未接线→not_ready；send happy path 经真实 ControlRuntime 门。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/session"
)

// ---- spec 错误码断言小件 ----

func specErrorCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("期望非 nil 错误")
	}
	var se *spec.Error
	if !errors.As(err, &se) {
		t.Fatalf("期望 *spec.Error，得到 %T: %v", err, err)
	}
	return se.Code
}

// ---- stripTerminalControls ----

func TestStripTerminalControls(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"纯文本", "hello world", "hello world"},
		{"CSI 颜色", "\x1b[31mRED\x1b[0m plain", "RED plain"},
		{"CSI 光标与清屏序列丢弃", "abc\x1b[2K\x1b[1GX", "abcX"},
		{"OSC BEL 结尾超链接", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x07tail", "linktail"},
		{"OSC ESC 反斜杠结尾", "\x1b]0;title\x07text", "text"},
		{"未终结 OSC 整段丢弃", "keep\x1b]8;;dangling", "keep"},
		{"回车覆盖行", "download 10%\rdownload 100%\n", "download 100%"},
		{"回车短覆盖保留尾部", "abcdef\rX", "Xbcdef"},
		{
			name: "退格后覆盖写",
			in:   "ab\b\bc",
			want: "cb", // 两次退格后 c 覆盖首位，与真实终端一致
		},
		{"退格不出界", "\b\bstart", "start"},
		{"C0 与 DEL 丢弃", "\x00\x07text\x7f", "text"},
		{"两字符转义丢弃", "a\x1bMb", "ab"},
		{"悬空 ESC 丢弃", "end\x1b", "end"},
		{"回车换行序列", "line1\r\nline2\r\n", "line1\nline2"},
		{"多行与末尾无换行", "a\nb\rc", "a\nc"},
		{"中英文混合", "\x1b[1m你好\x1b[0m world", "你好 world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripTerminalControls(tc.in); got != tc.want {
				t.Errorf("stripTerminalControls(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ---- tailUTF8Safe ----

func TestTailUTF8Safe(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
		cut   bool
	}{
		{"不超限原样返回", "hello", 10, "hello", false},
		{"零上限不截断", "hello", 0, "hello", false},
		{"负上限不截断", "hello", -1, "hello", false},
		{"恰好等长不截断", "hello", 5, "hello", false},
		{"ASCII 截断", "hello world", 5, "world", true},
		{"多字节边界右移到字符边界", "a你", 3, "你", true},  // "你" 3 字节，切点从中间右移
		{"切点落在多字节序列中间右移", "ab你", 1, "", true}, // 严格不超 limit，末字符放不下则为空
		{"只留头字符", "你世界", 3, "界", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, cut := tailUTF8Safe(tc.in, tc.limit)
			if got != tc.want || cut != tc.cut {
				t.Errorf("tailUTF8Safe(%q, %d) = (%q, %v), want (%q, %v)", tc.in, tc.limit, got, cut, tc.want, tc.cut)
			}
		})
	}
}

// ---- renderSessionOutput ----

func testOutputSnapshot(t *testing.T, data string, seq uint64) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"data":       base64.StdEncoding.EncodeToString([]byte(data)),
		"seq":        seq,
		"runToken":   "opaque-token",
		"runVersion": "3",
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return string(b)
}

func TestRenderSessionOutput(t *testing.T) {
	const raw = "\x1b[32mOK\x1b[0m done\nsecond line"
	cases := []struct {
		name     string
		data     string
		seq      uint64
		tail     int
		sinceSeq int64
		rawFlag  bool
		wantText string
		wantTrun bool
	}{
		{"默认剥离并全量返回", raw, 7, 0, -1, false, "OK done\nsecond line", false},
		{"raw 保留原始文本", raw, 7, 0, -1, true, raw, false},
		{"sinceSeq 等于当前 seq 返回空", raw, 7, 0, 7, false, "", false},
		{"sinceSeq 大于当前 seq 返回空", raw, 5, 0, 9, false, "", false},
		{"sinceSeq 小于当前 seq 全量重算", raw, 7, 0, 6, false, "OK done\nsecond line", false},
		{"tailBytes 截断到尾部", raw, 7, 11, -1, false, "second line", true},
		{"tailBytes 大于文本不截断", raw, 7, 9999, -1, false, "OK done\nsecond line", false},
		{"tailBytes 按字符边界", "a你", 3, 3, -1, false, "你", true},
		{"sinceSeq 未前进时忽略 tail", raw, 7, 4, 7, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderSessionOutput("s1", "running", testOutputSnapshot(t, tc.data, tc.seq), tc.tail, tc.sinceSeq, tc.rawFlag)
			if err != nil {
				t.Fatalf("renderSessionOutput: %v", err)
			}
			res, ok := got.(sessionOutputResult)
			if !ok {
				t.Fatalf("返回类型 %T", got)
			}
			if res.Text != tc.wantText {
				t.Errorf("text = %q, want %q", res.Text, tc.wantText)
			}
			if res.Truncated != tc.wantTrun {
				t.Errorf("truncated = %v, want %v", res.Truncated, tc.wantTrun)
			}
			if res.Seq != tc.seq {
				t.Errorf("seq = %d, want %d", res.Seq, tc.seq)
			}
			if res.SessionID != "s1" || res.Status != "running" {
				t.Errorf("sessionId/status = %q/%q", res.SessionID, res.Status)
			}
		})
	}
}

func TestRenderSessionOutput_BadSnapshot(t *testing.T) {
	if _, err := renderSessionOutput("s1", "running", "not-json", 0, -1, false); specErrorCode(t, err) != spec.ErrInternal {
		t.Errorf("非 JSON 快照应映射 internal，得到 %s", specErrorCode(t, err))
	}
	if _, err := renderSessionOutput("s1", "running", `{"data":"!!!not-base64!!!","seq":1}`, 0, -1, false); specErrorCode(t, err) != spec.ErrInternal {
		t.Errorf("坏 base64 应映射 internal，得到 %s", specErrorCode(t, err))
	}
}

// ---- launch：参数校验与分发映射 ----

func TestNormalizeAgentLaunchParams(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "a-file")
	if writeErr := os.WriteFile(notDir, []byte("x"), 0o600); writeErr != nil {
		t.Fatalf("setup: %v", writeErr)
	}

	cases := []struct {
		name    string
		in      agentLaunchParams
		wantErr string
		check   func(t *testing.T, out agentLaunchParams)
	}{
		{
			name: "claudecode 归一化 workDir 与默认 mode",
			in:   agentLaunchParams{AppType: "claudecode", Provider: "p", WorkDir: dir + "/"},
			check: func(t *testing.T, out agentLaunchParams) {
				if out.Mode != "embedded" {
					t.Errorf("默认 mode = %q, want embedded", out.Mode)
				}
				if out.WorkDir != dir {
					t.Errorf("workDir 未归一化: %q want %q", out.WorkDir, dir)
				}
			},
		},
		{
			name:    "未知 appType",
			in:      agentLaunchParams{AppType: "vscode", Provider: "p"},
			wantErr: spec.ErrBadArgs,
		},
		{
			name:    "非法 mode",
			in:      agentLaunchParams{AppType: "claudecode", Provider: "p", Mode: "detached"},
			wantErr: spec.ErrBadArgs,
		},
		{
			name:    "workDir 不存在",
			in:      agentLaunchParams{AppType: "claudecode", Provider: "p", WorkDir: dir + "/missing"},
			wantErr: spec.ErrBadArgs,
		},
		{
			name:    "workDir 是文件",
			in:      agentLaunchParams{AppType: "claudecode", Provider: "p", WorkDir: notDir},
			wantErr: spec.ErrBadArgs,
		},
		{
			name:    "claudecode 缺 provider",
			in:      agentLaunchParams{AppType: "claudecode"},
			wantErr: spec.ErrBadArgs,
		},
		{
			name:    "opencode 缺 preset 与 provider",
			in:      agentLaunchParams{AppType: "opencode"},
			wantErr: spec.ErrBadArgs,
		},
		{
			name:    "codex 缺 model 与 preset",
			in:      agentLaunchParams{AppType: "codex", Provider: "p"},
			wantErr: spec.ErrBadArgs,
		},
		{
			name:    "codex headroom 不支持",
			in:      agentLaunchParams{AppType: "codex", Model: "m", Headroom: true},
			wantErr: spec.ErrBadArgs,
		},
		{
			name: "pi 仅 preset 键可用",
			in:   agentLaunchParams{AppType: "pi", Preset: "prov/key"},
			check: func(t *testing.T, out agentLaunchParams) {
				if out.Mode != "embedded" {
					t.Errorf("pi 默认 mode = %q", out.Mode)
				}
			},
		},
	}
	// workDir 相对路径场景：尾随分隔符被归一化。
	{
		in := agentLaunchParams{AppType: "claudecode", Provider: "p", WorkDir: dir + string(os.PathSeparator)}
		out, nErr := normalizeAgentLaunchParams(in)
		if nErr != nil {
			t.Fatalf("尾随分隔符应通过: %v", nErr)
		}
		if out.WorkDir != dir {
			t.Errorf("workDir 归一化 = %q, want %q", out.WorkDir, dir)
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := normalizeAgentLaunchParams(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("期望错误 %s，得到 nil（out=%+v）", tc.wantErr, out)
				}
				if code := specErrorCode(t, err); code != tc.wantErr {
					t.Errorf("错误码 = %s, want %s（%v）", code, tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("未期望错误: %v", err)
			}
			if tc.check != nil {
				tc.check(t, out)
			}
		})
	}
}

// recordingLauncher 记录五入口的调用参数（launch 分发映射测试）。
type recordingLauncher struct {
	calls []string
}

func (r *recordingLauncher) LaunchSession(providerName, presetName, mode, workDir string, useHeadroom bool, shellPath string) (string, error) {
	r.calls = append(r.calls, fmt.Sprintf("LaunchSession(%q,%q,%q,%q,%v,%q)", providerName, presetName, mode, workDir, useHeadroom, shellPath))
	return "id-claude", nil
}

func (r *recordingLauncher) LaunchOpenCode(providerName, presetName, mode, workDir, shellPath string) (string, error) {
	r.calls = append(r.calls, fmt.Sprintf("LaunchOpenCode(%q,%q,%q,%q,%q)", providerName, presetName, mode, workDir, shellPath))
	return "id-oc", nil
}

func (r *recordingLauncher) LaunchCodexSession(modelName, providerID, mode, workDir, shellPath string) (string, error) {
	r.calls = append(r.calls, fmt.Sprintf("LaunchCodexSession(%q,%q,%q,%q,%q)", modelName, providerID, mode, workDir, shellPath))
	return "id-codex", nil
}

func (r *recordingLauncher) LaunchPiSession(modelName, providerID, mode, workDir, shellPath string) (string, error) {
	r.calls = append(r.calls, fmt.Sprintf("LaunchPiSession(%q,%q,%q,%q,%q)", modelName, providerID, mode, workDir, shellPath))
	return "id-pi", nil
}

func (r *recordingLauncher) LaunchOmpSession(modelName, providerID, mode, workDir, shellPath string) (string, error) {
	r.calls = append(r.calls, fmt.Sprintf("LaunchOmpSession(%q,%q,%q,%q,%q)", modelName, providerID, mode, workDir, shellPath))
	return "id-omp", nil
}

func TestDispatchAgentLaunch(t *testing.T) {
	cases := []struct {
		name string
		in   agentLaunchParams
		want string
	}{
		{"claudecode 走 LaunchSession（含 headroom）",
			agentLaunchParams{AppType: "claudecode", Provider: "pv", Preset: "ps", Mode: "embedded", WorkDir: "/w", Headroom: true},
			`LaunchSession("pv","ps","embedded","/w",true,"")`},
		{"opencode 走 LaunchOpenCode",
			agentLaunchParams{AppType: "opencode", Provider: "pv", Preset: "ps", Mode: "embedded", WorkDir: "/w"},
			`LaunchOpenCode("pv","ps","embedded","/w","")`},
		{"codex 显式 model 优先",
			agentLaunchParams{AppType: "codex", Provider: "pv", Model: "gpt-x", Mode: "embedded", WorkDir: "/w"},
			`LaunchCodexSession("gpt-x","pv","embedded","/w","")`},
		{"codex 无 model 时 preset 键占模型位",
			agentLaunchParams{AppType: "codex", Provider: "pv", Preset: "pv/key", Mode: "embedded", WorkDir: "/w"},
			`LaunchCodexSession("pv/key","pv","embedded","/w","")`},
		{"pi 带 shell",
			agentLaunchParams{AppType: "pi", Model: "m", Provider: "pv", Mode: "embedded", WorkDir: "/w", Shell: "/bin/zsh"},
			`LaunchPiSession("m","pv","embedded","/w","/bin/zsh")`},
		{"omp",
			agentLaunchParams{AppType: "omp", Model: "m2", Mode: "embedded", WorkDir: "/w"},
			`LaunchOmpSession("m2","","embedded","/w","")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingLauncher{}
			id, err := dispatchAgentLaunch(rec, tc.in)
			if err != nil {
				t.Fatalf("dispatchAgentLaunch: %v", err)
			}
			if len(rec.calls) != 1 || rec.calls[0] != tc.want {
				t.Errorf("调用映射 = %v, want [%s]", rec.calls, tc.want)
			}
			if id == "" {
				t.Error("返回 sessionId 为空")
			}
		})
	}
}

// TestAgentCLISessionLaunch_HandlerValidation 经 Handler 全链路验证 bad_args
// （App 夹具无法避免真实启动，故仅测校验层：非法 workDir 在分发前被拒绝）。
func TestAgentCLISessionLaunch_HandlerValidation(t *testing.T) {
	app := newTestApp(t)
	h := newAgentCLIHost(app)
	_, err := h.sessionLaunch(context.Background(), spec.Args{
		"appType": "claudecode", "provider": "p", "workDir": "/definitely/not/exist/dir",
	})
	if code := specErrorCode(t, err); code != spec.ErrBadArgs {
		t.Errorf("不存在的 workDir 应在分发前拒绝为 bad_args，得到 %s（%v）", code, err)
	}
}

// ---- Handler 错误映射 ----
func TestAgentCLISessionHandlers_NotFound(t *testing.T) {
	app := newTestApp(t)
	h := newAgentCLIHost(app)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() (any, error)
	}{
		{"get", func() (any, error) { return h.sessionGet(ctx, spec.Args{"sessionId": "nope"}) }},
		{"output", func() (any, error) { return h.sessionOutput(ctx, spec.Args{"sessionId": "nope"}) }},
		{"send", func() (any, error) {
			return h.sessionSend(ctx, spec.Args{"sessionId": "nope", "text": "hi"})
		}},
		{"stop", func() (any, error) { return h.sessionStop(ctx, spec.Args{"sessionId": "nope"}) }},
		{"remove", func() (any, error) { return h.sessionRemove(ctx, spec.Args{"sessionId": "nope"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.call()
			if code := specErrorCode(t, err); code != spec.ErrNotFound {
				t.Errorf("不存在会话应映射 not_found，得到 %s", code)
			}
		})
	}
}

func TestAgentCLISessionSend_StatusErrors(t *testing.T) {
	app := newTestApp(t)
	h := newAgentCLIHost(app)
	ctx := context.Background()

	// 已停止会话 → conflict。
	stopped := app.Sessions.Create(session.AppTypeClaudeCode, "p", "ps", "m", session.ModeEmbedded, "")
	app.Sessions.MarkStopped(stopped.ID)
	_, err := h.sessionSend(ctx, spec.Args{"sessionId": stopped.ID, "text": "hi"})
	if code := specErrorCode(t, err); code != spec.ErrConflict {
		t.Errorf("已停止会话 send 应映射 conflict，得到 %s（%v）", code, err)
	}

	// running 会话但控制运行时未接线（PtyWrite fail-closed）→ not_ready。
	running := app.Sessions.Create(session.AppTypeClaudeCode, "p", "ps", "m", session.ModeEmbedded, "")
	_, err = h.sessionSend(ctx, spec.Args{"sessionId": running.ID, "text": "hi"})
	if code := specErrorCode(t, err); code != spec.ErrNotReady {
		t.Errorf("未接线 control 的 send 应映射 not_ready，得到 %s（%v）", code, err)
	}
}

// fakeAgentPTYRaw 记录 WriteRaw 调用，满足 remote.PTYRawPort。
type fakeAgentPTYRaw struct {
	mu     sync.Mutex
	writes map[string][]byte
}

func (f *fakeAgentPTYRaw) WriteRaw(_ context.Context, sessionID string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes == nil {
		f.writes = map[string][]byte{}
	}
	f.writes[sessionID] = append(f.writes[sessionID], data...)
	return nil
}

func (f *fakeAgentPTYRaw) ResizeRaw(_ context.Context, _ string, _, _ int) error { return nil }

func (f *fakeAgentPTYRaw) DetachSession(_ string) (remote.BackendDetachReceipt, error) {
	return nil, nil
}

func (f *fakeAgentPTYRaw) written(sessionID string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[sessionID]
}

// registerActiveGateRun 建立一个 running 会话记录 + 活跃 gate run（未终结），
// 供 DesktopInput 走完整控制门路径。
func registerActiveGateRun(t *testing.T, app *App) string {
	t.Helper()
	sess := app.Sessions.Create(session.AppTypeClaudeCode, "p", "ps", "m", session.ModeEmbedded, "")
	if sess == nil {
		t.Fatal("Sessions.Create 失败")
	}
	sid := sess.ID
	rt := app.control
	launchPermit, runPermit, obsPermit, err := rt.BeginDesktopRun(context.Background(), contract.SessionID(sid))
	if err != nil {
		t.Fatalf("BeginDesktopRun: %v", err)
	}
	if err := rt.ActivateDesktopRun(context.Background(), runPermit); err != nil {
		rt.AbortDesktopRun(context.Background(), launchPermit, err)
		t.Fatalf("ActivateDesktopRun: %v", err)
	}
	rt.Projector().TrackRun(contract.SessionID(sid), obsPermit)
	return sid
}

// TestAgentCLISessionSend_GateHappyPath 证明 send 经真实 ControlRuntime 门写入：
// 小文本走 DesktopInput 通道，字节内容含 --enter 追加的回车。
func TestAgentCLISessionSend_GateHappyPath(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	app.ctx = t.Context() // PtyWrite 使用 a.ctx（生产中由 Startup 注入）
	fake := &fakeAgentPTYRaw{}
	app.control.SetPTYRawPort(fake)
	h := newAgentCLIHost(app)
	sid := registerActiveGateRun(t, app)

	res, err := h.sessionSend(context.Background(), spec.Args{
		"sessionId": sid, "text": "claude --help", "enter": true,
	})
	if err != nil {
		t.Fatalf("sessionSend: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("返回类型 %T", res)
	}
	if m["sessionId"] != sid || m["bytesSent"] != len("claude --help\r") {
		t.Errorf("返回内容异常: %+v", m)
	}
	if got := string(fake.written(sid)); got != "claude --help\r" {
		t.Errorf("门内写入 = %q, want %q", got, "claude --help\r")
	}
}

// TestAgentCLISessionList_StatusFilter 校验状态过滤与空列表形状。
func TestAgentCLISessionList_StatusFilter(t *testing.T) {
	app := newTestApp(t)
	h := newAgentCLIHost(app)
	a := app.Sessions.Create(session.AppTypeClaudeCode, "p", "ps", "m", session.ModeEmbedded, "")
	app.Sessions.MarkStopped(a.ID)
	b := app.Sessions.Create(session.AppTypePi, "p", "ps", "m", session.ModeEmbedded, "")

	all, err := h.sessionList(context.Background(), spec.Args{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all.([]session.SessionInfo)) != 2 {
		t.Errorf("全量列表长度 = %d, want 2", len(all.([]session.SessionInfo)))
	}
	running, err := h.sessionList(context.Background(), spec.Args{"status": "running"})
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	infos := running.([]session.SessionInfo)
	if len(infos) != 1 || infos[0].ID != b.ID {
		t.Errorf("running 过滤结果异常: %+v", infos)
	}
	// 空结果必须是空数组而非 null（JSON 稳定形状）。
	none, err := h.sessionList(context.Background(), spec.Args{"status": "failed"})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(none.([]session.SessionInfo)) != 0 {
		t.Errorf("failed 过滤应空，得到 %+v", none)
	}
}

func TestAgentCLISessionRemoveAndClearStopped(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	h := newAgentCLIHost(app)
	ctx := context.Background()

	// running 会话 remove → conflict（先 stop 再 remove）。
	running := app.Sessions.Create(session.AppTypeClaudeCode, "p", "ps", "m", session.ModeEmbedded, "")
	if _, err := h.sessionRemove(ctx, spec.Args{"sessionId": running.ID}); specErrorCode(t, err) != spec.ErrConflict {
		t.Errorf("running 会话 remove 应 conflict，得到 %v", err)
	}

	// stopped + gate 终结条目 → remove 成功。
	stopped := registerGateSession(t, app)
	if _, err := h.sessionRemove(ctx, spec.Args{"sessionId": stopped}); err != nil {
		t.Fatalf("remove stopped: %v", err)
	}

	// clear-stopped 清掉剩余 stopped 会话。
	other := registerGateSession(t, app)
	res, err := h.sessionClearStopped(ctx, spec.Args{})
	if err != nil {
		t.Fatalf("clear-stopped: %v", err)
	}
	cleared, ok := res.(ClearStoppedSessionsResult)
	if !ok {
		t.Fatalf("返回类型 %T", res)
	}
	if cleared.Cleared < 1 || !containsString(cleared.ClearedIDs, other) {
		t.Errorf("clear-stopped 结果异常: %+v", cleared)
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ---- D-06 回归：ctx 取消在副作用前拦截 ----

// TestAgentCLISessionLaunchCanceledBeforeSideEffect 已取消 ctx 的 launch 必须
// 在启动副作用前返回 not_ready。修复前会继续走启动链路（返回 internal 启动
// 失败或误启动会话）。
func TestAgentCLISessionLaunchCanceledBeforeSideEffect(t *testing.T) {
	app := newTestApp(t)
	h := newAgentCLIHost(app)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.sessionLaunch(ctx, spec.Args{"appType": "claudecode", "provider": "ghost-prov"})
	if code := specErrorCode(t, err); code != spec.ErrNotReady {
		t.Errorf("已取消 ctx 应在启动副作用前返回 not_ready，得到 %s（%v）", code, err)
	}
	if got := len(app.GetSessions()); got != 0 {
		t.Errorf("已取消 ctx 不应创建会话，实际 %d 个", got)
	}
}

// TestAgentCLISessionSendCanceledBeforeSideEffect 已取消 ctx 的 send 必须在
// PtyWrite 副作用前返回 not_ready，且一个字节都不得写入会话。
func TestAgentCLISessionSendCanceledBeforeSideEffect(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	app.ctx = t.Context()
	fake := &fakeAgentPTYRaw{}
	app.control.SetPTYRawPort(fake)
	h := newAgentCLIHost(app)
	sid := registerActiveGateRun(t, app)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.sessionSend(ctx, spec.Args{"sessionId": sid, "text": "hello"})
	if code := specErrorCode(t, err); code != spec.ErrNotReady {
		t.Errorf("已取消 ctx 应在写入副作用前返回 not_ready，得到 %s（%v）", code, err)
	}
	if got := string(fake.written(sid)); got != "" {
		t.Errorf("已取消 ctx 下仍执行了写入：%q", got)
	}
}
