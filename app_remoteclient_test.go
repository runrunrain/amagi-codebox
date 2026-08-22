// app_remoteclient_test.go — App 转发层 RC2 测试：
//   · M-4（diting Minor）：Connect 锁范围收窄——慢宿主的 Connect 验证阶段
//     （Keychain 读取 + host/summary 网络验证）不阻塞 Disconnect / 会话域
//     绑定 / 主机管理；
//   · 终端绑定错误路径（未连接 / 未 attach / 空参）；
//   · 终端绑定全链（真实 TerminalManager + fake WS 宿主形态由
//     internal/remoteclient 包覆盖，此处验证转发层装配）。
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remoteclient"
)

// memCredentialStore 是注入 App.rcCreds 的内存凭据存储。
type memCredentialStore struct {
	mu    sync.Mutex
	items map[string]string
}

func newMemCredentialStore() *memCredentialStore {
	return &memCredentialStore{items: map[string]string{}}
}

func (s *memCredentialStore) Put(name, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[name] = secret
	return nil
}

func (s *memCredentialStore) Get(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.items[name], nil
}

func (s *memCredentialStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, name)
	return nil
}

// rcTestApp 构造装配了 remoteclient 域的 App（登记簿 + 内存凭据）。
func rcTestApp(t *testing.T) *App {
	t.Helper()
	app, configDir := newTestAppWithConfigDir(t)
	app.initRemoteClientRegistry(configDir)
	app.rcCreds = newMemCredentialStore()
	return app
}

// rcPairedHost 登记一个已配对主机（凭据入内存库），返回 entryID。
func rcPairedHost(t *testing.T, app *App, hostPort string) string {
	t.Helper()
	// 直接回填配对态（UpsertPaired 是域层配对流回填入口）。
	id, err := app.rcRegistry.UpsertPaired(hostPort, "dev-test-0000000000000001", "测试宿主", remoteclient.HealthReachable, time.Now())
	if err != nil {
		t.Fatalf("upsert paired: %v", err)
	}
	if err := app.rcCreds.Put(remoteclient.CredentialEntryName("dev-test-0000000000000001"), "s3cr3t-value-0000000000000000000000000001"); err != nil {
		t.Fatalf("put credential: %v", err)
	}
	return id
}

// TestRemoteClientConnectNarrowLock（M-4）：慢宿主（host/summary 延迟 800ms）
// 的 Connect 进行中，Disconnect / ListRemoteSessions / RenameHost 必须即刻
// 返回（不被 rcMu 阻塞）。
func TestRemoteClientConnectNarrowLock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	app := rcTestApp(t)
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	hostID := rcPairedHost(t, app, hostPort)

	connectDone := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := app.RemoteClientConnect(hostID)
		connectDone <- err
	}()
	time.Sleep(150 * time.Millisecond) // Connect 已进入慢验证阶段

	// Disconnect：无连接，应快速报错而非等 Connect。
	d0 := time.Now()
	if err := app.RemoteClientDisconnect(hostID); err == nil {
		t.Fatal("Disconnect without connection must error")
	}
	if el := time.Since(d0); el > 300*time.Millisecond {
		t.Fatalf("Disconnect blocked %v behind Connect (M-4 regression)", el)
	}

	// 会话域绑定：未连接，快速报错。
	d1 := time.Now()
	if _, err := app.RemoteClientListRemoteSessions(); err == nil {
		t.Fatal("ListRemoteSessions without connection must error")
	}
	if el := time.Since(d1); el > 300*time.Millisecond {
		t.Fatalf("ListRemoteSessions blocked %v behind Connect (M-4 regression)", el)
	}

	// 主机管理（改名）不受阻。
	d2 := time.Now()
	if err := app.RemoteClientRenameHost(hostID, "改名"); err != nil {
		t.Fatalf("RenameHost: %v", err)
	}
	if el := time.Since(d2); el > 300*time.Millisecond {
		t.Fatalf("RenameHost blocked %v behind Connect (M-4 regression)", el)
	}

	// Connect 本身按预期失败（非契约 503 响应）。
	select {
	case err := <-connectDone:
		if err == nil {
			t.Fatal("Connect against broken host must fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Connect never returned")
	}
	if el := time.Since(start); el < 700*time.Millisecond {
		t.Logf("connect elapsed %v (server latency 800ms)", el)
	}
}

// TestRemoteClientTerminalBindingsGuard：终端绑定错误路径——未连接、空
// sessionID、未 attach。
func TestRemoteClientTerminalBindingsGuard(t *testing.T) {
	app := rcTestApp(t)

	if _, err := app.RemoteClientTerminalAttach("sess-1"); err == nil || !strings.Contains(err.Error(), "no host is connected") {
		t.Errorf("Attach without connection err = %v, want no-host error", err)
	}
	if err := app.RemoteClientTerminalDetach("sess-1"); err == nil {
		t.Error("Detach without connection must error")
	}
	if err := app.RemoteClientTerminalSendInput("sess-1", "x"); err == nil {
		t.Error("SendInput without connection must error")
	}
	if err := app.RemoteClientTerminalResize("sess-1", 80, 24); err == nil {
		t.Error("Resize without connection must error")
	}

	// 连接一个最小 v1 宿主（host/summary 200）后：attach 空参报错。
	app2, configDir := newTestAppWithConfigDir(t)
	app2.initRemoteClientRegistry(configDir)
	app2.rcCreds = newMemCredentialStore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","serverVersion":"test","cliAvailability":[]}`))
	}))
	defer srv.Close()
	hostID := rcPairedHost(t, app2, strings.TrimPrefix(srv.URL, "http://"))
	if _, err := app2.RemoteClientConnect(hostID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = app2.RemoteClientDisconnect(hostID) }()
	if _, err := app2.RemoteClientTerminalAttach("  "); err == nil {
		t.Error("Attach with blank sessionID must error")
	}
	if err := app2.RemoteClientTerminalSendInput("sess-x", "hi"); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Errorf("SendInput for unattached session err = %v, want not-attached", err)
	}
	if err := app2.RemoteClientTerminalResize("sess-x", 0, 24); err == nil {
		t.Error("Resize unattached must error")
	}
	if err := app2.RemoteClientTerminalDetach("sess-x"); err == nil {
		t.Error("Detach unattached must error")
	}
}

// TestRemoteClientShutdownDetachesTerminals：shutdownRemoteClientTerminals 在
// 已连接（含 attach 中会话）时安全收尾，不 panic、可重入。
func TestRemoteClientShutdownDetachesTerminals(t *testing.T) {
	app, configDir := newTestAppWithConfigDir(t)
	app.initRemoteClientRegistry(configDir)
	app.rcCreds = newMemCredentialStore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// host/summary 契约体；/ws/v1 由 remoteclient 包测试覆盖，这里保持
		// REST 可连接即可（终端 attach 的 WS 拨号会失败并按退避重试，收尾
		// 时被取消）。
		if strings.HasSuffix(r.URL.Path, "/host/summary") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"apiVersion":"v1","serverVersion":"test","cliAvailability":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	hostID := rcPairedHost(t, app, strings.TrimPrefix(srv.URL, "http://"))
	if _, err := app.RemoteClientConnect(hostID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := app.RemoteClientTerminalAttach("sess-shutdown"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	done := make(chan struct{})
	go func() {
		app.shutdownRemoteClientTerminals()
		app.shutdownRemoteClientTerminals() // 幂等重入
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdownRemoteClientTerminals hung")
	}
	if _, err := app.RemoteClientTerminalAttach("sess-after"); err == nil || !strings.Contains(fmt.Sprint(err), "no host is connected") {
		t.Errorf("Attach after shutdown err = %v, want no-host error", err)
	}
}
