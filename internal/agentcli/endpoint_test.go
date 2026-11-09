package agentcli

// endpoint_test.go — 端点文件生命周期：权限（0700/0600）、原子写（无 tmp
// 残留、内容完整）、撤销的 pid 所有权校验、陈旧清理的存活性判定。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"amagi-codebox/internal/agentcli/spec"
)

// noEndpointTmpLeft 斷新端点目录内无残留临时文件（独占随机名，评审 D-03）。
func noEndpointTmpLeft(t *testing.T, path string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".endpoint-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("不应残留 tmp 文件: %v", left)
	}
}

func TestEndpointFilePermissionsAndAtomicity(t *testing.T) {
	s, ep := startTestServer(t)
	path := endpointPath(s.configDir)

	// run 目录 0700、endpoint.json 0600。
	runDirSt, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if perm := runDirSt.Mode().Perm(); perm != 0o700 {
		t.Fatalf("run 目录权限=%o, want 700", perm)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat endpoint: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("endpoint.json 权限=%o, want 600", perm)
	}
	// 原子写：无 tmp 残留，内容完整可解析且字段齐备。
	noEndpointTmpLeft(t, path)
	if ep.SchemaVersion != spec.EndpointSchemaVersion || ep.URL == "" || ep.PID != os.Getpid() {
		t.Fatalf("端点内容不完整: %+v", ep)
	}
	if ep.CLIToken == "" || len(ep.CLIToken) != 43 { // 32 字节 base64url = 43 字符
		t.Fatalf("令牌应为 32 字节 base64url, got len=%d", len(ep.CLIToken))
	}
	if ep.AppVersion != "test-version" || ep.StartedAt == "" {
		t.Fatalf("端点缺 appVersion/startedAt: %+v", ep)
	}

	// 再次发布（轮换）走同一原子路径：旧文件被完整替换、无 tmp 残留。
	if err := s.RotateToken(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	ep2, err := readEndpointFile(path)
	if err != nil {
		t.Fatalf("轮换后读端点: %v", err)
	}
	if ep2.CLIToken == ep.CLIToken {
		t.Fatal("轮换后令牌应更新")
	}
	noEndpointTmpLeft(t, path)
}

func TestEndpointPublishFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	// 把 run 路径占位成普通文件 → MkdirAll 失败：发布报错且不留 tmp。
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := publishEndpoint(dir, spec.Endpoint{SchemaVersion: 1, URL: "http://127.0.0.1:1", PID: 1, CLIToken: "x"})
	if err == nil {
		t.Fatal("run 被文件占位时发布应失败")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".endpoint-*.tmp"))
	if len(left) != 0 {
		t.Fatalf("发布失败不应残留 tmp 文件: %v", left)
	}
}

func TestStopRevokesEndpointFile(t *testing.T) {
	dir := t.TempDir()
	s := NewServer(dir, "v", testTable(), testHandlers(), nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := os.Stat(endpointPath(dir)); err != nil {
		t.Fatalf("运行中端点应存在: %v", err)
	}
	s.Stop()
	if _, err := os.Stat(endpointPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("Stop 后端点文件应删除, stat err=%v", err)
	}
	st := s.Status()
	if st.Running || st.URL != "" || st.StartedAt != "" {
		t.Fatalf("Stop 后状态: %+v", st)
	}
	// 幂等。
	s.Stop()
}

func TestRemoveOwnEndpointRespectsOwnership(t *testing.T) {
	t.Run("own pid removed", func(t *testing.T) {
		dir := t.TempDir()
		writeEndpointForTest(t, dir, os.Getpid())
		removeOwnEndpoint(dir)
		if _, err := os.Stat(endpointPath(dir)); !os.IsNotExist(err) {
			t.Fatalf("本进程的端点文件应被删除, stat err=%v", err)
		}
	})
	t.Run("foreign live-ish pid kept", func(t *testing.T) {
		dir := t.TempDir()
		// pid=1 恒非当前测试进程：removeOwnEndpoint 只做归属比对（不查存活），
		// 属于他人的文件不动。
		writeEndpointForTest(t, dir, 1)
		removeOwnEndpoint(dir)
		if _, err := os.Stat(endpointPath(dir)); err != nil {
			t.Fatalf("他人 pid 的端点文件应保留: %v", err)
		}
	})
	t.Run("corrupt file cleaned", func(t *testing.T) {
		dir := t.TempDir()
		path := endpointPath(dir)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		removeOwnEndpoint(dir)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("损坏的端点文件应作为垃圾清理, stat err=%v", err)
		}
	})
}

func TestCleanStaleEndpoint(t *testing.T) {
	t.Run("missing file noop", func(t *testing.T) {
		dir := t.TempDir()
		CleanStaleEndpoint(dir) // 不应报错
	})
	t.Run("dead pid removed", func(t *testing.T) {
		dir := t.TempDir()
		// 选一个几乎不可能存活且不属于本进程的 pid（大值区间）。
		writeEndpointForTest(t, dir, 1<<22)
		CleanStaleEndpoint(dir)
		if _, err := os.Stat(endpointPath(dir)); !os.IsNotExist(err) {
			t.Fatalf("死 pid 端点应清理, stat err=%v", err)
		}
	})
	t.Run("live foreign pid kept", func(t *testing.T) {
		dir := t.TempDir()
		writeEndpointForTest(t, dir, os.Getppid()) // 父进程（go test）恒存活且非本进程
		CleanStaleEndpoint(dir)
		if _, err := os.Stat(endpointPath(dir)); err != nil {
			t.Fatalf("存活实例的端点应保留: %v", err)
		}
	})
	t.Run("corrupt file removed", func(t *testing.T) {
		dir := t.TempDir()
		path := endpointPath(dir)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{bogus"), 0o600); err != nil {
			t.Fatal(err)
		}
		CleanStaleEndpoint(dir)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("损坏端点应清理, stat err=%v", err)
		}
	})
}

// TestStartCleansStaleEndpointBeforePublish 验证 Start 先清理再发布：启动
// 前残留的陈旧文件不会以“读到旧令牌 → 探活失败”的形式干扰发现。
func TestStartCleansStaleEndpointBeforePublish(t *testing.T) {
	dir := t.TempDir()
	writeEndpointForTest(t, dir, 1<<22) // 死 pid 残留
	s := NewServer(dir, "v", testTable(), testHandlers(), nil)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Stop)
	ep, err := readEndpointFile(endpointPath(dir))
	if err != nil {
		t.Fatalf("read endpoint: %v", err)
	}
	if ep.PID != os.Getpid() {
		t.Fatalf("残留文件应被新端点覆盖, pid=%d want=%d", ep.PID, os.Getpid())
	}
}

func writeEndpointForTest(t *testing.T, dir string, pid int) {
	t.Helper()
	ep := spec.Endpoint{SchemaVersion: spec.EndpointSchemaVersion, URL: "http://127.0.0.1:1", PID: pid, CLIToken: "stale-token"}
	b, err := json.Marshal(ep)
	if err != nil {
		t.Fatal(err)
	}
	path := endpointPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
