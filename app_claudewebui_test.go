package main

// app_claudewebui_test.go — Claude Code Web 平面的 App 接线验证（claude-webui
// design §5.3/§11「Go·App」行，2026-11）。
//
// fake 注入按仓库既有模式落地（控制面照 bind_failclosed_test.go /
// agentcli_h_session_test.go：真实 ControlRuntime + 记录型 PTY raw port；
// Settings 用真实 Service 注入）：
//
//   - startClaudeWebPlane 收到的 PlaneConfig（sid/workDir/transcriptRoot/pid/
//     port/token）经真实 Manager 的数据面可观测断言——/api/info 钉死
//     sessionId/pid/port/token 四元组，/api/history 读到按 root+workDir+sid
//     编码落位的 fixture 记录钉死转录根解析（含 CLAUDE_CONFIG_DIR 覆盖）；
//   - RegisterSession 参数一致经真实 webui.Service 断言：注册即 probing，
//     探测（注入 token 200 → tokenProven 采纳）后 available 且
//     SessionEndpoint==(boundPort, token)；
//   - WSL/开关关/材料缺失/Start 失败一律不注册（tracker unknown）；
//   - 退出/删除路径 Stop+Invalidate 经可观测状态断言（监听器关闭、tracker
//     ended/unknown）；
//   - appClaudeInputPort 四方法向真实控制门转发（桌面车道 take-first、设备
//     平面车道 no-take 各自锚定；持控失败的双哨兵错误链见映射测试）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/appmeta/claude"
	"amagi-codebox/internal/claudewebui"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/webui"
)

// --- 测试助手 -------------------------------------------------------------

// wireClaudeWebPlane 给测试 App 接上真实的 claudewebui.Manager 与 webui.Service，
// 并注入已加载的 Settings（默认 claudeWebUIEnabled=true）。
func wireClaudeWebPlane(t *testing.T, app *App) (*webui.Service, *settings.Service) {
	t.Helper()
	svc := wireWebUI(t, app)
	app.ClaudeWebUI = claudewebui.NewManager(app.Log)
	st := settings.NewService(t.TempDir())
	if err := st.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	app.Settings = st
	return svc, st
}

// writeClaudePlaneFixture 在转录根下按 root/projects/<encoded-cwd>/<sid>.jsonl
// 落一条最小 human user 记录（行形状见 transcript.go 的 fixture 契约）。
func writeClaudePlaneFixture(t *testing.T, root, workDir, claudeSID, text string) {
	t.Helper()
	path := claude.SessionJSONLPathInRoot(root, workDir, claudeSID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	line := fmt.Sprintf(`{"type":"user","uuid":"u-fix","timestamp":"2026-11-14T10:00:00.000Z","origin":{"kind":"human"},"message":{"role":"user","content":%q}}`, text)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// claudePlanePortOpen 探测回环端口是否仍有 listener。
func claudePlanePortOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// claudePlaneGet 带 Bearer 调平面 HTTP API 并解码 JSON。
func claudePlaneGet(t *testing.T, port int, token, path string, out any) error {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// claudePlaneWaitFor 轮询等待条件成立（默认转录尾随/探测收尾窗口）。
func claudePlaneWaitFor(t *testing.T, desc string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("超时等待：%s", desc)
}

type claudePlaneInfo struct {
	V         int    `json:"v"`
	Ready     bool   `json:"ready"`
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	AppType   string `json:"appType"`
}

type claudePlaneHistory struct {
	V     int `json:"v"`
	Items []struct {
		Kind string `json:"kind"`
		UUID string `json:"uuid"`
		Text string `json:"text"`
	} `json:"items"`
	Title string `json:"title"`
}

// laneRecordingRaw 记录每次 WriteRaw（逐次独立条目，供分块计数断言）。
type laneRecordingRaw struct {
	mu     sync.Mutex
	writes []string
}

func (f *laneRecordingRaw) WriteRaw(_ context.Context, _ string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, string(data))
	return nil
}

func (f *laneRecordingRaw) ResizeRaw(_ context.Context, _ string, _, _ int) error { return nil }

func (f *laneRecordingRaw) DetachSession(_ string) (remote.BackendDetachReceipt, error) {
	return nil, nil
}

func (f *laneRecordingRaw) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

// --- 启动路径：PlaneConfig 与 RegisterSession 参数 -------------------------

// TestStartClaudeWebPlane_PlaneConfigAndTrackerRegistration：embedded+开关开+
// 非 WSL 时 Start 收到正确 PlaneConfig（sid/workDir/transcriptRoot/pid/port/
// token 全部经数据面可观测钉死）且 RegisterSession 参数一致（tracker probing →
// 探测 available → SessionEndpoint==(port, token)）；退出路径 Stop+Invalidate。
func TestStartClaudeWebPlane_PlaneConfigAndTrackerRegistration(t *testing.T) {
	app := newTestApp(t)
	svc, _ := wireClaudeWebPlane(t, app)

	workDir := t.TempDir()
	// 用户自定义 HOME 重定向会话（design R12：HOME 重定向的实际来源是用户自定义
	// env）：ClaudeConfigRoot 应解析为 <HOME>/.claude 而非本机真实 home。
	fakeHome := t.TempDir()
	transcriptRoot := filepath.Join(fakeHome, ".claude")
	const claudeSID = "claude-sid-0001"
	const sessID = "sess-plane-main"
	const fixtureText = "hello claude plane"
	writeClaudePlaneFixture(t, transcriptRoot, workDir, claudeSID, fixtureText)

	port, err := webui.AllocateFreePort()
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	const token = "test-plane-token-0001"
	const pid = 424242

	spec := platform.ResolvedLaunchSpec{
		WorkDir: workDir,
		Env:     platform.ResolvedEnv{Variables: []string{"HOME=" + fakeHome}},
	}
	app.startClaudeWebPlane(sessID, claudeSID, spec, pid, port, token)

	// PlaneConfig.sessionId/pid/port/token：/api/info（Bearer 即 token 凭证校验，
	// 200 同时证明 token 逐字节落位）。
	var info claudePlaneInfo
	if err := claudePlaneGet(t, port, token, "/api/info", &info); err != nil {
		t.Fatalf("GET /api/info: %v", err)
	}
	if info.V != 1 || !info.Ready || info.SessionID != sessID || info.PID != pid ||
		info.Port != port || info.AppType != "claudecode" {
		t.Fatalf("info 契约字段异常: %+v（want sess=%s pid=%d port=%d）", info, sessID, pid, port)
	}

	// PlaneConfig.transcriptRoot/workDir/claudeSID：/api/history 读到按
	// root+encoded(workDir)+sid 落位的 fixture（任一字段错位 → 空历史）。
	var hist claudePlaneHistory
	claudePlaneWaitFor(t, "history 读到 fixture 记录", func() bool {
		var h claudePlaneHistory
		if err := claudePlaneGet(t, port, token, "/api/history", &h); err != nil {
			return false
		}
		hist = h
		return len(h.Items) == 1 && h.Items[0].Text == fixtureText
	})
	if got := hist.Items[0]; got.Kind != "user" || got.UUID != "u-fix" {
		t.Fatalf("fixture 记录形状异常: %+v", got)
	}
	if hist.Title != fixtureText {
		t.Fatalf("title 应取首条 human 记录文本，got %q", hist.Title)
	}

	// RegisterSession 参数一致：注册即 probing；探测（注入端口+token 200 →
	// tokenProven 采纳）后 available 且 SessionEndpoint 回出 (port, token)。
	if st := svc.GetWebUIStatus(sessID); st.State != webui.StateProbing {
		t.Fatalf("注册后 tracker state=%s, want probing", st.State)
	}
	if st := svc.ProbeWebUI(sessID); st.State != webui.StateAvailable {
		t.Fatalf("探测后 tracker state=%s (url=%q port=%d), want available", st.State, st.URL, st.Port)
	}
	epPort, epToken := svc.SessionEndpoint(sessID)
	if epPort != port || epToken != token {
		t.Fatalf("SessionEndpoint=(%d,%q), want (%d,%q)", epPort, epToken, port, token)
	}

	// 退出路径（design §5.3-3）：Stop（listener 释放）+ Invalidate（tracker ended）。
	app.stopClaudeWebPlane(sessID)
	claudePlaneWaitFor(t, "平面 listener 关闭", func() bool { return !claudePlanePortOpen(port) })
	if st := svc.GetWebUIStatus(sessID); st.State != webui.StateEnded {
		t.Fatalf("退出收尾后 tracker state=%s, want ended", st.State)
	}
	// 幂等：重复收尾不 panic、不复活。
	app.stopClaudeWebPlane(sessID)
}

// TestStartClaudeWebPlane_ClaudeConfigDirRootOverride：spec env 携带
// CLAUDE_CONFIG_DIR 的会话，转录根按该键解析（design §5.1/R12：CLAUDE_CONFIG_DIR
// 优先 → HOME/USERPROFILE → 缺省 homeDir）。
func TestStartClaudeWebPlane_ClaudeConfigDirRootOverride(t *testing.T) {
	app := newTestApp(t)
	_, _ = wireClaudeWebPlane(t, app)

	workDir := t.TempDir()
	configDirRoot := t.TempDir()
	const claudeSID = "claude-sid-0002"
	const sessID = "sess-plane-ccdir"
	const fixtureText = "config-dir root record"
	writeClaudePlaneFixture(t, configDirRoot, workDir, claudeSID, fixtureText)

	port, err := webui.AllocateFreePort()
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	spec := platform.ResolvedLaunchSpec{
		WorkDir: workDir,
		Env:     platform.ResolvedEnv{Variables: []string{"CLAUDE_CONFIG_DIR=" + configDirRoot}},
	}
	app.startClaudeWebPlane(sessID, claudeSID, spec, 111111, port, "test-plane-token-0002")

	claudePlaneWaitFor(t, "history 读到 CLAUDE_CONFIG_DIR 根下 fixture", func() bool {
		var h claudePlaneHistory
		if err := claudePlaneGet(t, port, "test-plane-token-0002", "/api/history", &h); err != nil {
			return false
		}
		return len(h.Items) == 1 && h.Items[0].Text == fixtureText
	})
	app.stopClaudeWebPlane(sessID)
}

// --- 门控与不注册情形 ------------------------------------------------------

// TestClaudeWebPlaneEnabled_Gating：WSL 目标 / 开关关 / Manager 未接线 → 不预
// 分配平面材料（LaunchSession 的门控谓词，design §5.3-1）。
func TestClaudeWebPlaneEnabled_Gating(t *testing.T) {
	app, configDir := newTestAppWithConfigDir(t)
	app.ClaudeWebUI = claudewebui.NewManager(app.Log)
	st := settings.NewService(configDir)
	if err := st.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	app.Settings = st

	native := platform.ResolvedLaunchSpec{} // 非 WSL（零值 BootstrapMode != BootstrapWSL）
	if !app.claudeWebPlaneEnabled(native) {
		t.Fatal("开关默认开 + 非 WSL + Manager 已接线：应放行")
	}

	wsl := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL}
	if app.claudeWebPlaneEnabled(wsl) {
		t.Fatal("WSL 目标应降级为无平面")
	}

	if err := st.SetClaudeWebUIEnabled(false); err != nil {
		t.Fatalf("set claudeWebUIEnabled=false: %v", err)
	}
	if app.claudeWebPlaneEnabled(native) {
		t.Fatal("开关显式关闭后不应放行")
	}

	app.ClaudeWebUI = nil
	if app.claudeWebPlaneEnabled(native) {
		t.Fatal("Manager 未接线（测试构造/早退路径）不应放行")
	}
}

// TestStartClaudeWebPlane_NoRegisterOnFailureOrMissingMaterials：Start 失败
// （端口被占）/ 材料缺失（port=0、token 空）一律不注册 tracker（unknown → 前端
// 无切换控件，design §5.3-2 的有意降级）。
func TestStartClaudeWebPlane_NoRegisterOnFailureOrMissingMaterials(t *testing.T) {
	app := newTestApp(t)
	svc, _ := wireClaudeWebPlane(t, app)

	// 占住端口：Manager.Start bind 失败。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer ln.Close()
	occupied := ln.Addr().(*net.TCPAddr).Port

	spec := platform.ResolvedLaunchSpec{WorkDir: t.TempDir()}
	app.startClaudeWebPlane("sess-start-fail", "sid-fail", spec, 1, occupied, "tok")
	app.startClaudeWebPlane("sess-no-port", "sid-noport", spec, 1, 0, "tok")
	app.startClaudeWebPlane("sess-no-token", "sid-notok", spec, 1, occupied, "")

	for _, id := range []string{"sess-start-fail", "sess-no-port", "sess-no-token"} {
		if st := svc.GetWebUIStatus(id); st.State != webui.StateUnknown {
			t.Fatalf("%s: 不应注册 tracker，state=%s", id, st.State)
		}
		if ep, tok := svc.SessionEndpoint(id); ep != 0 || tok != "" {
			t.Fatalf("%s: 不应有 endpoint，got (%d,%q)", id, ep, tok)
		}
	}
}

// TestClaudeWebPlane_NilManagerAllPathsSafe：ClaudeWebUI nil（测试构造/早退
// 路径）时启动/收尾/门控全部零操作不 panic。
func TestClaudeWebPlane_NilManagerAllPathsSafe(t *testing.T) {
	app := newTestApp(t)
	svc := wireWebUI(t, app) // ClaudeWebUI 保持 nil

	spec := platform.ResolvedLaunchSpec{WorkDir: t.TempDir()}
	app.startClaudeWebPlane("sess-nil", "sid-nil", spec, 1, 45678, "tok-nil")
	if st := svc.GetWebUIStatus("sess-nil"); st.State != webui.StateUnknown {
		t.Fatalf("nil Manager 不应注册 tracker，state=%s", st.State)
	}
	app.stopClaudeWebPlane("sess-nil") // 不 panic
	if !app.claudeWebPlaneEnabled(spec) {
		// Settings 未接线 + Manager nil → 门控关（本用例的双 nil 形态）。
		return
	}
	t.Fatal("nil Manager + nil Settings 时门控应关")
}

// --- 输入端口适配器：双车道转发 -------------------------------------------

// TestAppClaudeInputPort_DesktopLane：桌面车道四方法中的两个写口经真实控制门
// 转发——WriteInput 单发、PasteChunk 1KB 分块（2500B → 3 次原始写），且写后
// holder=desktop（take-first，与 xterm 本地打字同语义）。
func TestAppClaudeInputPort_DesktopLane(t *testing.T) {
	app := newTestApp(t)
	wireTestControl(t, app)
	app.ctx = t.Context()
	raw := &laneRecordingRaw{}
	app.control.SetPTYRawPort(raw)
	sid := registerActiveGateRun(t, app)

	p := appClaudeInputPort{a: app}
	if err := p.WriteInput(context.Background(), sid, []byte("hi\r")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	data := make([]byte, 2500)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	if err := p.PasteChunk(context.Background(), sid, data); err != nil {
		t.Fatalf("PasteChunk: %v", err)
	}

	writes := raw.snapshot()
	if len(writes) != 4 { // 1 单发 + 3 分块
		t.Fatalf("原始写次数=%d, want 4（1 单发 + 3 分块）", len(writes))
	}
	if writes[0] != "hi\r" {
		t.Fatalf("单发内容=%q, want %q", writes[0], "hi\r")
	}
	total := 0
	for _, w := range writes[1:] {
		total += len(w)
	}
	if total != 2500 {
		t.Fatalf("分块总字节=%d, want 2500", total)
	}

	// take-first 锚：桌面写后 holder=desktop。
	snap, gErr := app.control.Arbiter().SnapshotForDevice(contract.SessionID(sid), "")
	if gErr != nil {
		t.Fatalf("SnapshotForDevice: %v", gErr)
	}
	if snap.State != contract.ControlStateDesktop {
		t.Fatalf("桌面写后 holder=%s, want desktop（take-first）", snap.State)
	}
}

// TestAppClaudeInputPort_DevicePlaneLane：设备平面车道转发——持控设备写入
// 放行且 holder 不变（no-take，连发第二条仍放行）；桌面夺权后写被拒且错误
// 同时携带 claudewebui.ErrControlForbidden 哨兵（server 403 信封依据）与原
// *remote.ControlGateError{DenyNotController} 链（映射只追加不吞不换）。
func TestAppClaudeInputPort_DevicePlaneLane(t *testing.T) {
	app := newTestApp(t)
	rt := wireTestControl(t, app)
	app.ctx = t.Context()
	raw := &laneRecordingRaw{}
	rt.SetPTYRawPort(raw)
	const sid = "sess-dev-lane"
	startGateSession(t, rt, sid)
	acquireDeviceHold(t, rt, sid, "devA", "Device A", "connA")

	p := appClaudeInputPort{a: app}
	// 连发两条单发（阻断项 1 形态：第一条不得夺走设备自身控制权）。
	for i := 0; i < 2; i++ {
		if err := p.DeviceWrite(context.Background(), sid, "devA", []byte("k")); err != nil {
			t.Fatalf("DeviceWrite #%d: %v", i+1, err)
		}
	}
	snap, gErr := rt.Arbiter().SnapshotForDevice(contract.SessionID(sid), contract.DeviceID("devA"))
	if gErr != nil || snap.State != contract.ControlStateYou {
		t.Fatalf("设备连写后 holder=%s (err=%v), want you（no-take）", snap.State, gErr)
	}

	// 分块：2500B → 3 次原始写。
	data := make([]byte, 2500)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	if err := p.DevicePasteChunk(context.Background(), sid, "devA", data); err != nil {
		t.Fatalf("DevicePasteChunk: %v", err)
	}
	if got := len(raw.snapshot()); got != 5 { // 2 单发 + 3 分块
		t.Fatalf("原始写次数=%d, want 5（2 单发 + 3 分块）", got)
	}

	// 桌面夺权后：设备平面写 fail-closed，双哨兵错误链，零新写入。
	if err := rt.DesktopInput(context.Background(), contract.SessionID(sid), []byte("x")); err != nil {
		t.Fatalf("DesktopInput takeover: %v", err)
	}
	before := len(raw.snapshot())
	err := p.DeviceWrite(context.Background(), sid, "devA", []byte("k2"))
	if err == nil {
		t.Fatal("夺权后 DeviceWrite 应被拒")
	}
	if !errors.Is(err, claudewebui.ErrControlForbidden) {
		t.Fatalf("应携带 claudewebui.ErrControlForbidden 哨兵（server 403 信封依据），got %v", err)
	}
	var gateErr *remote.ControlGateError
	if !errors.As(err, &gateErr) || gateErr.Kind != remote.DenyNotController {
		t.Fatalf("原控制门错误链应保留（As *ControlGateError{DenyNotController}），got %v", err)
	}
	if got := len(raw.snapshot()); got != before {
		t.Fatalf("拒绝路径不得有原始写（before=%d after=%d）", before, got)
	}
}

// TestAppClaudeInputPort_FailClosed：control nil / 未 MarkReady → 四方法
// fail-closed，错误同时满足任务口径（errors.Is remote.ErrControlNotReady）与
// server 信封口径（errors.Is claudewebui.ErrControlDown → 503 service.down）。
func TestAppClaudeInputPort_FailClosed(t *testing.T) {
	check := func(t *testing.T, name string, p appClaudeInputPort) {
		t.Helper()
		ctx := context.Background()
		calls := map[string]error{
			"WriteInput":       p.WriteInput(ctx, "s", []byte("x")),
			"PasteChunk":       p.PasteChunk(ctx, "s", []byte("x")),
			"DeviceWrite":      p.DeviceWrite(ctx, "s", "d", []byte("x")),
			"DevicePasteChunk": p.DevicePasteChunk(ctx, "s", "d", []byte("x")),
		}
		for method, err := range calls {
			if err == nil {
				t.Fatalf("%s（%s）：应 fail-closed 报错", method, name)
			}
			if !errors.Is(err, remote.ErrControlNotReady) {
				t.Fatalf("%s（%s）：应保留 remote.ErrControlNotReady，got %v", method, name, err)
			}
			if !errors.Is(err, claudewebui.ErrControlDown) {
				t.Fatalf("%s（%s）：应携带 claudewebui.ErrControlDown 哨兵（server 503 信封依据），got %v", method, name, err)
			}
		}
	}

	t.Run("control nil", func(t *testing.T) {
		app := newTestApp(t) // 不接线 control
		check(t, "nil control", appClaudeInputPort{a: app})
	})
	t.Run("control not ready", func(t *testing.T) {
		app := newTestApp(t)
		app.control = remote.NewControlRuntime(remote.NewSystemClock(), app.Log) // 未 MarkReady
		check(t, "not ready", appClaudeInputPort{a: app})
	})
	t.Run("nil app", func(t *testing.T) {
		check(t, "nil app", appClaudeInputPort{})
	})
}
