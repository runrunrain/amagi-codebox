package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
)

// newTestService 构造测试服务：注册表目录指向临时目录，探测窗口缩短。
func newTestService(t *testing.T, probingWindow time.Duration) *Service {
	t.Helper()
	s := NewService(logging.NewService(t.TempDir()), t.TempDir())
	s.probingWindow = probingWindow
	return s
}

// startStubInfo 起一个 /api/info stub server，返回其 127.0.0.1 端口。
// handler 由测试自定义（200 匹配/不匹配/503 等）。
func startStubInfo(t *testing.T, handler http.HandlerFunc) int {
	t.Helper()
	// 契约 §6.1：server 仅 bind 127.0.0.1。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func infoJSON(sessionID string, pid, port int) string {
	return fmt.Sprintf(`{"v":1,"ready":true,"sessionId":%q,"sessionFile":null,"model":null,"pid":%d,"port":%d,"startedAt":"2026-08-15T10:50:32.487Z","seq":5,"buffered":5,"clients":0,"pendingCount":0}`,
		sessionID, pid, port)
}

// --- 端口分配 -------------------------------------------------------------

func TestAllocateFreePort(t *testing.T) {
	port, err := AllocateFreePort()
	if err != nil {
		t.Fatalf("AllocateFreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("非法端口: %d", port)
	}
	// 释放后应可重新 bind（证明确实已关闭监听）。
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("分配端口未能重新 bind: %v", err)
	}
	_ = ln.Close()
}

// --- 探测状态机（httptest stub /api/info） ----------------------------------

func TestProbe_ReadyMatching(t *testing.T) {
	s := newTestService(t, time.Minute)
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-1", 4321, port))
	})
	s.RegisterSession("s1", 4321, port, "tok-1")

	st := s.ProbeWebUI("s1")
	if st.State != StateAvailable {
		t.Fatalf("state=%s, want available", st.State)
	}
	wantURL := fmt.Sprintf("http://127.0.0.1:%d/#/t=tok-1", port)
	if st.URL != wantURL {
		t.Fatalf("url=%q, want %q", st.URL, wantURL)
	}
	// 幂等：再次探测仍 available，且学到 piSessionID。
	st = s.ProbeWebUI("s1")
	if st.State != StateAvailable {
		t.Fatalf("re-probe state=%s", st.State)
	}
	url, err := s.OpenWebPlane("s1")
	if err != nil || url != wantURL {
		t.Fatalf("OpenWebPlane=%q err=%v", url, err)
	}
}

func TestProbe_NotReadyThenAvailable(t *testing.T) {
	s := newTestService(t, time.Minute)
	ready := false
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			// §4.1：503 是"未就绪"而非错误，体结构相同。
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"v":1,"ready":false,"sessionId":null,"pid":4321,"port":null,"startedAt":"x","seq":0,"buffered":0,"clients":0,"pendingCount":0}`)
			return
		}
		fmt.Fprint(w, infoJSON("pi-sess-2", 4321, port))
	})
	s.RegisterSession("s2", 4321, port, "")

	if st := s.ProbeWebUI("s2"); st.State != StateProbing {
		t.Fatalf("503 应维持 probing，got %s", st.State)
	}
	ready = true
	if st := s.ProbeWebUI("s2"); st.State != StateAvailable {
		t.Fatalf("ready 后应 available，got %s", st.State)
	}
}

func TestProbe_RefusedThenUnavailable(t *testing.T) {
	// 窗口极短：第二次探测即耗尽窗口 → unavailable（未装插件场景，A-4）。
	s := newTestService(t, time.Nanosecond)
	deadPort, err := AllocateFreePort() // 分配后不占用 → 拒连
	if err != nil {
		t.Fatalf("AllocateFreePort: %v", err)
	}
	s.RegisterSession("s3", 99999, deadPort, "")

	time.Sleep(time.Millisecond) // 保证超过 probingWindow
	st := s.ProbeWebUI("s3")
	if st.State != StateUnavailable {
		t.Fatalf("state=%s, want unavailable", st.State)
	}
	if st.URL != "" {
		t.Fatalf("unavailable 不应带 url: %q", st.URL)
	}
}

func TestProbe_ResumeSessionSwitchAdopted(t *testing.T) {
	// /resume、/new、fork、reload：同 pid 会话切换，sessionId 演进（A→B）
	// 且端口不变 → 采纳为 available，tracker 跟随更新 piSessionID。
	s := newTestService(t, time.Minute)
	stubSession := "pi-sess-a"
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON(stubSession, 4321, port))
	})
	s.RegisterSession("s4", 4321, port, "")
	if st := s.ProbeWebUI("s4"); st.State != StateAvailable {
		t.Fatalf("首轮应 available，got %s", st.State)
	}
	if learned := trackerPiSessionID(s, "s4"); learned != "pi-sess-a" {
		t.Fatalf("piSessionID=%q, want pi-sess-a", learned)
	}
	// TUI /resume：sessionId 演进，pid/端口不变。
	stubSession = "pi-sess-b"
	if st := s.ProbeWebUI("s4"); st.State != StateAvailable {
		t.Fatalf("会话切换后应保持 available，got %s", st.State)
	}
	if learned := trackerPiSessionID(s, "s4"); learned != "pi-sess-b" {
		t.Fatalf("会话切换后 piSessionID=%q, want pi-sess-b（跟随更新）", learned)
	}
}

func TestProbe_AvailableKeepsAliveOnTransientNotReady(t *testing.T) {
	// 保活探测（available 后低频轮询）遇瞬时 503（会话切换/服务重建窗口
	// ready=false，pid 校验通过）：不降级——unavailable 只属于探测窗口内
	// 未就绪，available 态由 failStreak（持续不可达）或 Invalidate 决定。
	s := newTestService(t, time.Millisecond) // 探测窗口投小，验证 available 后 503 不触发窗口耗尽分支
	ready := true
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"v":1,"ready":false,"sessionId":null,"pid":4321,"port":null,"startedAt":"x","seq":0,"buffered":0,"clients":0,"pendingCount":0}`)
			return
		}
		fmt.Fprint(w, infoJSON("pi-sess-k", 4321, port))
	})
	s.RegisterSession("sk", 4321, port, "")
	if st := s.ProbeWebUI("sk"); st.State != StateAvailable {
		t.Fatalf("首轮应 available，got %s", st.State)
	}
	// 模拟会话切换窗口的瞬时未就绪：available 必须保持。
	ready = false
	if st := s.ProbeWebUI("sk"); st.State != StateAvailable {
		t.Fatalf("available 遇瞬时 503 不应降级，got %s", st.State)
	}
	// 恢复后仍 available 且 URL 不变。
	ready = true
	if st := s.ProbeWebUI("sk"); st.State != StateAvailable {
		t.Fatalf("恢复后应保持 available，got %s", st.State)
	}
}

func TestProbe_SessionSwitchWithPIDChangeRejected(t *testing.T) {
	// 会话切换合法的前提是 pid 不变：sessionId 演进 + pid 也变（端口被其他
	// pi 进程复用）→ 拒绝，failStreak 照常累积（ended 语义不变）。
	s := newTestService(t, time.Minute)
	stubPID := 4321
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-b", stubPID, port))
	})
	s.RegisterSession("s4b", 4321, port, "")
	if st := s.ProbeWebUI("s4b"); st.State != StateAvailable {
		t.Fatalf("首轮应 available，got %s", st.State)
	}
	// 端口被另一个 pi 复用：sessionId 与 pid 均变。
	stubPID = 7777
	if st := s.ProbeWebUI("s4b"); st.State != StateAvailable {
		t.Fatalf("首次失败应保持 available（failStreak=1），got %s", st.State)
	}
	if st := s.ProbeWebUI("s4b"); st.State != StateEnded {
		t.Fatalf("连续失败应 ended，got %s", st.State)
	}
	if learned := trackerPiSessionID(s, "s4b"); learned != "pi-sess-b" {
		t.Fatalf("拒绝路径不得更新 piSessionID，got %q", learned)
	}
}

func TestProbe_RegistryFallbackSessionSwitchAdopted(t *testing.T) {
	// 注册表回退通道：已学到 piSessionID=A；条目 sessionId=B 但 pid 一致
	//（会话切换后扩展覆盖写注册表）→ 入围并采纳，tracker 跟随更新为 B。
	s := newTestService(t, time.Minute)
	var realPort int
	realPort = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-b", 4321, realPort))
	})
	deadPort, err := AllocateFreePort()
	if err != nil {
		t.Fatalf("AllocateFreePort: %v", err)
	}
	writeRegistryEntry(t, s.registryDir, 4321, RegistryEntry{
		V: 1, PID: 4321, Host: "127.0.0.1", Port: realPort, SessionID: "pi-sess-b", Ready: true,
	})
	s.RegisterSession("s4c", 4321, deadPort, "")
	// 预置已学到的 piSessionID（模拟采纳后端口漂移，只剩注册表通道）。
	s.mu.Lock()
	s.trackers["s4c"].piSessionID = "pi-sess-a"
	s.mu.Unlock()

	st := s.ProbeWebUI("s4c")
	if st.State != StateAvailable || st.Port != realPort {
		t.Fatalf("pid 一致但 sessionId 演进的注册表条目应被采纳，got %+v", st)
	}
	if learned := trackerPiSessionID(s, "s4c"); learned != "pi-sess-b" {
		t.Fatalf("piSessionID=%q, want pi-sess-b（跟随更新）", learned)
	}
}

// trackerPiSessionID 读取 tracker 当前学到的 pi 会话 ID（测试断言用）。
func trackerPiSessionID(s *Service, sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trackers[sessionID].piSessionID
}

func TestProbe_ProtocolVersionRejected(t *testing.T) {
	s := newTestService(t, time.Minute)
	port := startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// §2：v 缺失/非法视为协议错误。
		fmt.Fprint(w, `{"v":2,"ready":true,"sessionId":"x","pid":1}`)
	})
	s.RegisterSession("s5", 4321, port, "")
	if st := s.ProbeWebUI("s5"); st.State == StateAvailable {
		t.Fatal("v!=1 不得 available")
	}
}

func TestProbe_EndedAfterAvailableLost(t *testing.T) {
	s := newTestService(t, time.Minute)
	var srv *httptest.Server
	var port int
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-6", 4321, port))
	}))
	_, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ = strconv.Atoi(portStr)
	s.RegisterSession("s6", 4321, port, "")

	if st := s.ProbeWebUI("s6"); st.State != StateAvailable {
		t.Fatalf("state=%s, want available", st.State)
	}
	srv.Close() // 模拟 pi 进程退出 → server 消亡
	// §7.4：连续失败达阈值才 ended。
	if st := s.ProbeWebUI("s6"); st.State != StateAvailable {
		t.Fatalf("首次失败应保持 available（failStreak=1），got %s", st.State)
	}
	if st := s.ProbeWebUI("s6"); st.State != StateEnded {
		t.Fatalf("连续失败应 ended，got %s", st.State)
	}
	// ended 后不再变化。
	if st := s.ProbeWebUI("s6"); st.State != StateEnded {
		t.Fatalf("ended 应粘性，got %s", st.State)
	}
}

// --- 注册表扫描与回退 ------------------------------------------------------

func writeRegistryEntry(t *testing.T, dir string, pid int, entry RegistryEntry) {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func TestScanRegistry_ParseAndSkipBad(t *testing.T) {
	dir := t.TempDir()
	writeRegistryEntry(t, dir, 111, RegistryEntry{V: 1, PID: 111, Host: "127.0.0.1", Port: 5001, SessionID: "a"})
	writeRegistryEntry(t, dir, 222, RegistryEntry{V: 1, PID: 222, Host: "127.0.0.1", Port: 5002, SessionID: "b"})
	// 损坏 JSON、v 非法、port 非法、非 .json 文件均应跳过。
	if err := os.WriteFile(filepath.Join(dir, "333.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRegistryEntry(t, dir, 444, RegistryEntry{V: 2, PID: 444, Port: 5004})
	writeRegistryEntry(t, dir, 555, RegistryEntry{V: 1, PID: 555, Port: 0})
	if err := os.WriteFile(filepath.Join(dir, "666.tmp"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries := scanRegistry(dir)
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2: %+v", len(entries), entries)
	}
	got := map[int]int{}
	for _, e := range entries {
		got[e.PID] = e.Port
	}
	if got[111] != 5001 || got[222] != 5002 {
		t.Fatalf("entries mismatch: %+v", got)
	}
	// 目录不存在返回空而非报错。
	if got := scanRegistry(filepath.Join(dir, "nonexistent")); len(got) != 0 {
		t.Fatalf("missing dir should yield no entries, got %+v", got)
	}
}

func TestProbe_RegistryFallbackByPID(t *testing.T) {
	// 注入端口拒连（bind 失败回退场景，§7.2）；注册表里有该 pid 的真实端口。
	s := newTestService(t, time.Minute)
	var realPort int
	realPort = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-7", 7777, realPort))
	})
	deadPort, err := AllocateFreePort()
	if err != nil {
		t.Fatalf("AllocateFreePort: %v", err)
	}
	writeRegistryEntry(t, s.registryDir, 7777, RegistryEntry{
		V: 1, PID: 7777, Host: "127.0.0.1", Port: realPort, SessionID: "pi-sess-7", Ready: true,
	})
	s.RegisterSession("s7", 7777, deadPort, "")

	st := s.ProbeWebUI("s7")
	if st.State != StateAvailable {
		t.Fatalf("注册表回退应 available，got %s", st.State)
	}
	if st.Port != realPort {
		t.Fatalf("port=%d, want %d（注册表实际端口）", st.Port, realPort)
	}
}

func TestProbe_RegistryEvictsFailedCandidates(t *testing.T) {
	// 注册表存在两个同 pid 不可能，改为：陈旧条目（探测失败）+ 有效条目
	// 分属不同 pid；tracker 只认自己 pid——陈旧/异 pid 条目一律不采纳。
	s := newTestService(t, time.Minute)
	var realPort int
	realPort = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-8", 8888, realPort))
	})
	stalePort, err := AllocateFreePort()
	if err != nil {
		t.Fatalf("AllocateFreePort: %v", err)
	}
	// 陈旧条目：pid 匹配但端口拒连 → 探测淘汰。
	writeRegistryEntry(t, s.registryDir, 8888, RegistryEntry{V: 1, PID: 8888, Port: stalePort, SessionID: "old"})
	// 异 pid 条目：不匹配 → 跳过。
	writeRegistryEntry(t, s.registryDir, 9999, RegistryEntry{V: 1, PID: 9999, Port: realPort, SessionID: "pi-sess-8"})
	s.RegisterSession("s8", 8888, 0, "") // 无注入端口，纯注册表发现

	st := s.ProbeWebUI("s8")
	if st.State == StateAvailable {
		t.Fatalf("陈旧条目应被淘汰（探测失败），不得 available: %+v", st)
	}
	// 修正注册表（listen 回调后覆盖写真实端口，§7.1）→ 下一轮 available。
	writeRegistryEntry(t, s.registryDir, 8888, RegistryEntry{V: 1, PID: 8888, Port: realPort, SessionID: "pi-sess-8", Ready: true})
	if st := s.ProbeWebUI("s8"); st.State != StateAvailable {
		t.Fatalf("有效条目应 available，got %s", st.State)
	}
}

// --- 服务面边界 ------------------------------------------------------------

func TestStatus_UnknownSession(t *testing.T) {
	s := newTestService(t, time.Minute)
	if st := s.GetWebUIStatus("nope"); st.State != StateUnknown {
		t.Fatalf("state=%s, want unknown", st.State)
	}
	if _, err := s.OpenWebPlane("nope"); err == nil {
		t.Fatal("未注册会话 OpenWebPlane 应报错")
	}
}

func TestInvalidate_AndRemove(t *testing.T) {
	s := newTestService(t, time.Minute)
	s.RegisterSession("s9", 1234, 0, "")
	s.Invalidate("s9")
	if st := s.GetWebUIStatus("s9"); st.State != StateEnded {
		t.Fatalf("state=%s, want ended", st.State)
	}
	s.RemoveSession("s9")
	if st := s.GetWebUIStatus("s9"); st.State != StateUnknown {
		t.Fatalf("state=%s, want unknown", st.State)
	}
}

func TestProbeInfo_ContextCancel(t *testing.T) {
	port := startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, infoJSON("x", 1, 0))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, outcome := probeInfo(ctx, &http.Client{Timeout: time.Second}, port, "", 1)
	if outcome != probeUnreachable {
		t.Fatalf("cancelled ctx outcome=%v, want unreachable", outcome)
	}
}

// --- Major2：采纳强校验（端口复用/他服务占用） ------------------------------

func TestProbe_FirstAdoptionRejectsPIDMismatch(t *testing.T) {
	// 端口复用场景：注入端口被另一个 pi 进程占用（pid 不同）。
	s := newTestService(t, time.Minute)
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-other", 9999, port)) // pid 9999 ≠ 注册 pid 4321
	})
	s.RegisterSession("m2a", 4321, port, "")

	if st := s.ProbeWebUI("m2a"); st.State == StateAvailable {
		t.Fatalf("pid 不匹配不得采纳，got %+v", st)
	}
}

func TestProbe_FirstAdoptionRejectsPortMismatch(t *testing.T) {
	// 他服务占用场景：info 自报端口与候选端口不一致（代理/转发畸形服务）。
	s := newTestService(t, time.Minute)
	port := startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-x", 4321, 1)) // port=1 ≠ 候选端口
	})
	s.RegisterSession("m2b", 4321, port, "")

	if st := s.ProbeWebUI("m2b"); st.State == StateAvailable {
		t.Fatalf("info.port!=候选端口不得采纳，got %+v", st)
	}
}

func TestProbe_FirstAdoptionRejectsEmptySessionID(t *testing.T) {
	// ready=true 但 sessionId 为空（违反 §4.1 的畸形响应）→ 不采纳。
	s := newTestService(t, time.Minute)
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("", 4321, port))
	})
	s.RegisterSession("m2c", 4321, port, "")

	if st := s.ProbeWebUI("m2c"); st.State == StateAvailable {
		t.Fatalf("sessionId 空不得采纳，got %+v", st)
	}
}

func TestProbe_StickyRechecksPID(t *testing.T) {
	// 粘性复核（resume 修订后粘性键 = pid）：采纳后端口被新 pi 复用
	//（pid 变化）→ 不得保持 available 于错误身份；转注册表/失败路径。
	s := newTestService(t, time.Minute)
	stubPID := 4321
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-sticky", stubPID, port))
	})
	s.RegisterSession("m2d", 4321, port, "")
	if st := s.ProbeWebUI("m2d"); st.State != StateAvailable {
		t.Fatalf("首轮应 available，got %s", st.State)
	}
	// 端口被另一个 pi 复用：sessionId 相同但 pid 变化。
	stubPID = 7777
	for i := 0; i < endedFailThreshold; i++ {
		s.ProbeWebUI("m2d")
	}
	if st := s.GetWebUIStatus("m2d"); st.State != StateEnded {
		t.Fatalf("粘性 pid 校验失败应计为探测失败并累积 ended，got %s", st.State)
	}
}

// --- Major3：503 强校验与并发健壮性 ------------------------------------------

func TestProbe_503WithWrongPIDFallsBackToRegistry(t *testing.T) {
	// 注入端口被他服务占用：503 但 pid 不匹配 → 不可达，转注册表回退并采纳
	// 真实端口。
	s := newTestService(t, time.Minute)
	var realPort int
	realPort = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-m3", 5555, realPort))
	})
	occupied := startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 他服务的 503：pid 不属目标会话。
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"v":1,"ready":false,"sessionId":null,"pid":9999,"port":null,"startedAt":"x","seq":0,"buffered":0,"clients":0,"pendingCount":0}`)
	})
	writeRegistryEntry(t, s.registryDir, 5555, RegistryEntry{
		V: 1, PID: 5555, Host: "127.0.0.1", Port: realPort, SessionID: "pi-sess-m3", Ready: true,
	})
	s.RegisterSession("m3a", 5555, occupied, "")

	st := s.ProbeWebUI("m3a")
	if st.State != StateAvailable || st.Port != realPort {
		t.Fatalf("pid 不匹配的 503 应转注册表回退并采纳真实端口，got %+v", st)
	}
}

func TestProbe_503WithBadVersionIsUnreachable(t *testing.T) {
	// 503 体 v!=1：不是本协议服务 → 不可达（而非积压等待）。
	s := newTestService(t, time.Minute)
	port := startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"v":2,"ready":false}`)
	})
	s.RegisterSession("m3b", 4321, port, "")
	// 注入端口 503 非法 → 注册表也空 → 全失败 → 窗口内仍 probing（不是"已确认
	// 未就绪"语义，但也绝不 available）。
	if st := s.ProbeWebUI("m3b"); st.State != StateProbing {
		t.Fatalf("v 非法 503 不得采纳，state=%s", st.State)
	}
}

func TestProbe_503WithGarbageBodyIsUnreachable(t *testing.T) {
	s := newTestService(t, time.Minute)
	port := startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `<html>bad gateway</html>`)
	})
	s.RegisterSession("m3c", 4321, port, "")
	if st := s.ProbeWebUI("m3c"); st.State != StateProbing {
		t.Fatalf("体损坏 503 不得视为已确认未就绪以外状态，state=%s", st.State)
	}
}

func TestProbe_ConcurrentSessionsDoNotBlockOnGlobalLock(t *testing.T) {
	// Major3：网络 I/O 移出全局锁——会话 A 的慢探测不得阻塞会话 B 的状态读。
	s := newTestService(t, time.Minute)
	release := make(chan struct{})
	slowPort := startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		<-release // 挂起直到测试放行
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-slow", 1111, 0))
	})
	s.RegisterSession("slow", 1111, slowPort, "")
	s.RegisterSession("fast", 2222, 0, "")

	done := make(chan struct{})
	go func() {
		s.ProbeWebUI("slow") // 会卡在慢 stub 上
		close(done)
	}()
	// 等慢探测进入 I/O。
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	if st := s.GetWebUIStatus("fast"); st.State != StateProbing {
		t.Fatalf("fast state=%s, want probing", st.State)
	}
	if st := s.ProbeWebUI("fast"); st.State != StateProbing {
		t.Fatalf("fast probe state=%s, want probing", st.State)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("全局锁被慢探测阻塞：fast 路径耗时 %v", elapsed)
	}
	close(release) // 放行慢 stub，等探测 goroutine 收尾
	<-done
}

func TestProbe_RemoveDuringProbeDiscardsLateResult(t *testing.T) {
	// Minor6 对齐：探测 I/O 进行中 Remove 会话 → 迟到结果必须丢弃，不得复活
	// tracker。
	s := newTestService(t, time.Minute)
	release := make(chan struct{})
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-late", 3333, port))
	})
	s.RegisterSession("late", 3333, port, "")

	done := make(chan Status, 1)
	go func() { done <- s.ProbeWebUI("late") }()
	time.Sleep(50 * time.Millisecond) // 确保探测已进入锁外 I/O
	s.RemoveSession("late")
	close(release)

	st := <-done
	if st.State != StateUnknown {
		t.Fatalf("Remove 后迟到结果应上报 unknown，got %s", st.State)
	}
	if st := s.GetWebUIStatus("late"); st.State != StateUnknown {
		t.Fatalf("迟到采纳不得复活 tracker，got %s", st.State)
	}
}

// --- v1.0.2：capability token 消费 -------------------------------------------

func TestGenerateToken(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if len(tok) != 32 {
		t.Fatalf("token 长度=%d, want 32 hex chars（128bit）", len(tok))
	}
	tok2, _ := GenerateToken()
	if tok == tok2 {
		t.Fatal("两次生成不得相同")
	}
}

func TestProbeInfo_SendsBearerToken(t *testing.T) {
	var gotAuth string
	port := startStubInfo(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-tok", 4321, 0))
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, outcome := probeInfo(ctx, &http.Client{Timeout: time.Second}, port, "deadbeef", 0)
	if outcome != probeReady {
		t.Fatalf("outcome=%v, want ready", outcome)
	}
	if gotAuth != "Bearer deadbeef" {
		t.Fatalf("Authorization=%q, want %q", gotAuth, "Bearer deadbeef")
	}
}

func TestProbe_AuthorizesWithInjectedToken(t *testing.T) {
	// 服务端强制校验 token：无 token/错 token → 403（不可达）；注入 token 通过。
	s := newTestService(t, time.Minute)
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sess-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"forbidden"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-tok", 4321, port))
	})
	s.RegisterSession("tok1", 4321, port, "sess-token")

	st := s.ProbeWebUI("tok1")
	if st.State != StateAvailable {
		t.Fatalf("携带正确 token 应 available，got %s", st.State)
	}
	wantURL := fmt.Sprintf("http://127.0.0.1:%d/#/t=sess-token", port)
	if st.URL != wantURL {
		t.Fatalf("url=%q, want %q（v1.0.2 fragment 携带 token）", st.URL, wantURL)
	}
	url, err := s.OpenWebPlane("tok1")
	if err != nil || url != wantURL {
		t.Fatalf("OpenWebPlane=%q err=%v, want fragment URL", url, err)
	}
}

func TestProbe_RegistryTokenUsedWhenNotInjected(t *testing.T) {
	// 独立终端场景（A-6）：AMAGI_WEBUI_TOKEN 未注入，扩展自生成 token 写入
	// 注册表条目；codebox 探测时从条目补读携带。
	s := newTestService(t, time.Minute)
	var realPort int
	realPort = startStubInfo(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer registry-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-reg", 6666, realPort))
	})
	writeRegistryEntry(t, s.registryDir, 6666, RegistryEntry{
		V: 1, PID: 6666, Host: "127.0.0.1", Port: realPort, SessionID: "pi-sess-reg",
		Ready: true, Token: "registry-token",
	})
	s.RegisterSession("tok2", 6666, 0, "") // 无注入端口、无注入 token

	st := s.ProbeWebUI("tok2")
	if st.State != StateAvailable {
		t.Fatalf("注册表 token 应使探测通过鉴权，got %s", st.State)
	}
	wantURL := fmt.Sprintf("http://127.0.0.1:%d/#/t=registry-token", realPort)
	if st.URL != wantURL {
		t.Fatalf("url=%q, want %q（学到的 token 应反映到 fragment）", st.URL, wantURL)
	}
}

func TestProbe_NoTokenPlainURL(t *testing.T) {
	// 旧扩展（无 token 校验）：token 为空时 URL 保持裸形式（前向兼容）。
	s := newTestService(t, time.Minute)
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-plain", 4321, port))
	})
	s.RegisterSession("tok3", 4321, port, "")
	st := s.ProbeWebUI("tok3")
	if st.State != StateAvailable {
		t.Fatalf("state=%s, want available", st.State)
	}
	wantURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if st.URL != wantURL {
		t.Fatalf("url=%q, want %q", st.URL, wantURL)
	}
}

// blockingTransport 用于 ended 栅栏测试：首个请求进入后阻塞，直到 release。
type blockingTransport struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil, errors.New("transport released")
}

// TestProbe_LateResultDoesNotResurrectEnded：锁外探测期间 Invalidate 置 ended，
// 迟到结果不得将 tracker 复活为 available/probing（diting R1 增量复审 Major）。
func TestProbe_LateResultDoesNotResurrectEnded(t *testing.T) {
	s := newTestService(t, time.Minute)
	bt := &blockingTransport{entered: make(chan struct{}), release: make(chan struct{})}
	s.client = &http.Client{Transport: bt}
	s.RegisterSession("s9", 4242, 1, "") // 注入端口 1：探测请求将被 transport 拦截

	done := make(chan struct{})
	go func() { defer close(done); s.ProbeWebUI("s9") }()
	<-bt.entered // 探测 I/O 进行中
	s.Invalidate("s9")
	if st := s.GetWebUIStatus("s9"); st.State != StateEnded {
		t.Fatalf("Invalidate 后应立即 ended，got %s", st.State)
	}
	close(bt.release)
	<-done
	if st := s.GetWebUIStatus("s9"); st.State != StateEnded {
		t.Fatalf("迟到结果不得复活 ended，got %s", st.State)
	}
}

// TestProbe_EvictsStaleRegistryFile：回退扫描中探测失败的候选，
// 其注册文件应被 best-effort 删除（diting Minor7：淘汰落实到消费方）。
func TestProbe_EvictsStaleRegistryFile(t *testing.T) {
	s := newTestService(t, time.Minute)
	deadPort, err := AllocateFreePort()
	if err != nil {
		t.Fatalf("AllocateFreePort: %v", err)
	}
	writeRegistryEntry(t, s.registryDir, 5151, RegistryEntry{V: 1, PID: 5151, Port: deadPort, SessionID: "stale"})
	s.RegisterSession("s10", 5151, 0, "")

	st := s.ProbeWebUI("s10")
	if st.State == StateAvailable {
		t.Fatalf("拒连端口不得 available")
	}
	if _, err := os.Stat(filepath.Join(s.registryDir, "5151.json")); !os.IsNotExist(err) {
		t.Fatalf("陈旧注册文件应被删除，err=%v", err)
	}
}

func TestProbe_WindowsShellAttachTokenProvenAdopted(t *testing.T) {
	// Windows 内嵌 pi 走 BootstrapShellAttach：ConPTY 只起 shell（tracker 注册
	// 的 pid 是 shell pid），pi(node) 经输入流启动，webui server 的 info.PID 是
	// node pid——必然失配。注入 token 非空且探测 200 即证明归属，应采纳。
	s := newTestService(t, time.Minute)
	const shellPID = 4321
	const nodePID = 9999
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-win", nodePID, port))
	})
	s.RegisterSession("sw", shellPID, port, "tok-win")
	if st := s.ProbeWebUI("sw"); st.State != StateAvailable {
		t.Fatalf("token 已证归属时 pid 失配（shell=%d node=%d）应采纳，got %s", shellPID, nodePID, st.State)
	}
}

func TestProbe_WindowsShellAttachEmptyTokenStillRejected(t *testing.T) {
	// token 为空（legacy/独立终端未注入）：pid 防线维持——pid 失配仍拒绝，
	// 防端口被其他进程复用误采纳。
	s := newTestService(t, time.Millisecond)
	const shellPID = 4321
	const otherPID = 9999
	var port int
	port = startStubInfo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, infoJSON("pi-sess-x", otherPID, port))
	})
	s.RegisterSession("sx", shellPID, port, "")
	if st := s.ProbeWebUI("sx"); st.State == StateAvailable {
		t.Fatalf("空 token + pid 失配不应采纳，got %s", st.State)
	}
}
