package agentcli

// server_lifecycle_test.go — 生命周期并发与跨进程所有权回归（评审 D-03/D-04）：
//   - D-03：RotateToken/SetEnabled/Start/Stop 的状态变更+发布+回滚必须在统一
//     互斥下串行；端点临时文件独占随机名；失败只回滚自己的版本。并发轮换后
//     端点文件中的令牌必须立即可用（ping 200）。
//   - D-04：Start 发布前检测现有端点文件的 pid 存活且非本进程 → 拒绝发布、
//     不覆盖、状态如实（LastError）；占用进程退出后可正常启动。

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

// TestConcurrentRotateTokenKeepsEndpointUsable — 30 goroutine 并发轮换后，
// 端点文件里的令牌必须能 ping 200；轮换不允许出现发布失败（发布失败意味着
// 内存令牌与磁盘端点不一致，CLI 重新发现后会拿到永远 401 的令牌）。
func TestConcurrentRotateTokenKeepsEndpointUsable(t *testing.T) {
	s, _ := startTestServer(t)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RotateToken(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("并发轮换出现失败: %v", err)
	}

	ep, err := readEndpointFile(endpointPath(s.configDir))
	if err != nil {
		t.Fatalf("读端点文件: %v", err)
	}
	if ep.PID != os.Getpid() {
		t.Fatalf("端点 pid=%d, want %d", ep.PID, os.Getpid())
	}
	status, env := call(t, http.MethodGet, ep.URL, ep.CLIToken, spec.PathPing, nil)
	if status != http.StatusOK || !env.OK {
		t.Fatalf("并发轮换后端点令牌 ping: status=%d env=%+v, want 200 ok", status, env)
	}
}

// TestLifecycleInterleavedWithDisableAndStop — 轮换与 Disable/Stop/Enable 交错
// 时不允许出现端点发布失败（生命周期提交边界必须串行）；全部结束后恢复启用
// 运行态，端点令牌必须可用。
func TestLifecycleInterleavedWithDisableAndStop(t *testing.T) {
	s, _ := startTestServer(t)

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); _ = s.RotateToken() }()
		go func() { defer wg.Done(); _ = s.SetEnabled(false) }()
		go func() { defer wg.Done(); _ = s.SetEnabled(true) }()
		go func() { defer wg.Done(); s.Stop() }()
	}
	wg.Wait()

	// 归一到启用运行态：端点文件必须重新可用。
	if err := s.Start(); err != nil {
		t.Fatalf("交错后恢复启动: %v", err)
	}
	ep, err := readEndpointFile(endpointPath(s.configDir))
	if err != nil {
		t.Fatalf("读端点文件: %v", err)
	}
	status, env := call(t, http.MethodGet, ep.URL, ep.CLIToken, spec.PathPing, nil)
	if status != http.StatusOK || !env.OK {
		t.Fatalf("交错生命周期后端点令牌 ping: status=%d env=%+v", status, env)
	}
}

// TestStartRefusesWhenLiveForeignProcessOwnsEndpoint — 同一 home 的端点文件
// 属于另一个存活进程时，第二实例的 Start 必须拒绝发布（不覆盖仍存活第一实例
// 的发现文件）；占用进程退出后可正常启动。
func TestStartRefusesWhenLiveForeignProcessOwnsEndpoint(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("无法启动子进程: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	writeEndpointForTest(t, dir, cmd.Process.Pid)

	s := NewServer(dir, "v", testTable(), testHandlers(), nil)
	err := s.Start()
	if err == nil {
		s.Stop()
		t.Fatalf("端点被存活进程（pid=%d）占用时 Start 应拒绝", cmd.Process.Pid)
	}
	if !strings.Contains(err.Error(), "实例") {
		t.Fatalf("拒绝错误应说明是另一实例占用: %v", err)
	}
	// 原端点文件不得被覆盖（第一实例仍可被发现）。
	ep, rerr := readEndpointFile(endpointPath(dir))
	if rerr != nil {
		t.Fatalf("原端点文件应保留: %v", rerr)
	}
	if ep.PID != cmd.Process.Pid {
		t.Fatalf("端点文件被覆盖: pid=%d want=%d", ep.PID, cmd.Process.Pid)
	}
	if st := s.Status(); st.Running {
		t.Fatal("拒绝发布后不应处于运行态")
	}

	// 占用进程退出后（端点变陈旧）：Start 应清理并正常发布。
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if err := s.Start(); err != nil {
		t.Fatalf("占用进程退出后 Start 应成功: %v", err)
	}
	t.Cleanup(s.Stop)
	ep2, err := readEndpointFile(endpointPath(dir))
	if err != nil || ep2.PID != os.Getpid() {
		t.Fatalf("重启后端点应属于本进程: err=%v ep=%+v", err, ep2)
	}
}

// TestStartRefusalReportsLastErrorViaStatusAndWarns — 注入 pid 存活函数模拟
// 两实例（评审 D-04）：拒绝发布时 Status().LastError 携带原因、应用日志告警；
// 拒绝不影响后续正常启动（成功后 LastError 清空）。
func TestStartRefusalReportsLastErrorViaStatusAndWarns(t *testing.T) {
	dir := t.TempDir()
	writeEndpointForTest(t, dir, 424242)

	logs := &captureLogger{}
	s := NewServer(dir, "v", testTable(), testHandlers(), logs)
	s.processAlive = func(pid int) bool { return pid == 424242 } // 模拟另一存活实例

	if err := s.Start(); err == nil {
		s.Stop()
		t.Fatal("注入存活实例占用时 Start 应拒绝")
	}
	st := s.Status()
	if st.Running {
		t.Fatal("拒绝后不应运行")
	}
	if st.LastError == "" {
		t.Fatal("Status().LastError 应携带拒绝原因")
	}
	if !strings.Contains(strings.Join(logs.warns, "\n"), "占用") {
		t.Fatalf("应用日志应告警占用冲突: %v", logs.warns)
	}

	// 占用实例退出（存活函数返回 false）→ 正常启动，LastError 清空。
	s.processAlive = func(int) bool { return false }
	if err := s.Start(); err != nil {
		t.Fatalf("占用消失后 Start 应成功: %v", err)
	}
	t.Cleanup(s.Stop)
	if st := s.Status(); st.LastError != "" || !st.Running {
		t.Fatalf("成功启动后 LastError 应清空且运行中: %+v", st)
	}
}

// captureLogger 收集 agentcli.Logger 的告警/错误供断言。
type captureLogger struct {
	mu    sync.Mutex
	warns []string
}

func (c *captureLogger) Info(source, message string, detail ...string) {}

func (c *captureLogger) Warn(source, message string, detail ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warns = append(c.warns, message+" "+strings.Join(detail, " "))
}

func (c *captureLogger) Error(source, message string, detail ...string) {}
