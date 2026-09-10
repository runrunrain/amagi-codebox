package main

// remote_startup_selfheal_test.go — P3-B③ App 壳编排测试（healRemoteStartupRestore
// 的警告/状态透出/失败关闭门；重试引擎已在 internal/remote 全测）。
// 落盘：Leader 直修（P3-B 报告 §4 BLOCKED-1 patch 收口）。

import (
	"context"
	"embed"
	"net"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
)

func newSelfHealApp(t *testing.T) (*App, string) {
	t.Helper()
	configDir := t.TempDir()
	logSvc := logging.NewService(configDir)
	t.Cleanup(logSvc.Close)
	// ToggleRemoteServer → Remote.Start(a.ctx)：裸构造的 App 缺 ctx 会 panic
	// （生产由 Wails Startup 注入；测试用可取消的后台 context 等价注入）。
	app := &App{ctx: context.Background(), configDir: configDir, Log: logSvc, Settings: settings.NewService(configDir)}
	opts := remote.NewProductionSecurityOptions(configDir, func() (contract.HostSummary, error) {
		return validHostSummaryMain(), nil
	})
	srv := remote.NewServerWithSecurity(0, app, logSvc, embed.FS{}, opts)
	srv.SetHost("127.0.0.1")
	app.Remote = srv
	return app, configDir
}

func occupyTCPPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port
}

// 持续占用负例：重试耗尽 → 警告 + 漂移三元组 + enabled 不翻动。
func TestHealRemoteStartupRestore_PersistentConflictObservable(t *testing.T) {
	saved := remoteStartRetryDelays
	remoteStartRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { remoteStartRetryDelays = saved })

	app, configDir := newSelfHealApp(t)
	ln, port := occupyTCPPort(t)
	defer ln.Close()
	writeSettings(t, configDir, []byte(`{"remoteEnabled":true,"remoteHost":"127.0.0.1","remotePort":`+itoa(port)+`}`))
	secLoaded, startAllowed := app.runRemoteSecurityMigrationGate()
	if err := app.Settings.Load(); err != nil {
		t.Fatal(err)
	}
	app.Remote.SetPort(port)

	app.applyRemoteGateResult(context.Background(), secLoaded, startAllowed, true)
	app.healRemoteStartupRestore(context.Background(), true, true)

	if app.Remote.IsRunning() {
		t.Fatal("remote must stay stopped under persistent conflict")
	}
	if !app.hasWarning("远程服务自启动失败") {
		t.Fatal("expected fixed startup warning")
	}
	if !strings.Contains(app.getLastRemoteStartError(), "address already in use") {
		t.Fatalf("lastStartError = %q", app.getLastRemoteStartError())
	}
	if !app.Settings.GetRemoteEnabled() {
		t.Fatal("persisted enabled must stay true")
	}
	s := app.GetRemoteStatus()
	if s["enabled"] != true || s["running"] != false {
		t.Fatalf("drift tuple: enabled=%v running=%v", s["enabled"], s["running"])
	}
	if _, ok := s["hostSummaryDegraded"]; !ok {
		t.Fatal("GetRemoteStatus must expose hostSummaryDegraded")
	}
}

// 失败关闭门：存储未就绪（LoadSecurityState 失败语义）绝不重试 Start。
func TestHealRemoteStartupRestore_RespectsFailClosedGate(t *testing.T) {
	app, _ := newSelfHealApp(t)
	app.healRemoteStartupRestore(context.Background(), true, true) // store not loaded
	if app.Remote.IsRunning() {
		t.Fatal("must never Start across a not-ready security state")
	}
	if app.hasWarning("远程服务自启动失败") {
		t.Fatal("no Start attempted — no warning may be recorded")
	}
}

// 手动 Toggle(true) 成功清空遗留错误。
func TestToggleRemoteServer_ClearsStaleStartError(t *testing.T) {
	app, configDir := newSelfHealApp(t)
	app.setLastRemoteStartError(errStr("listen tcp 0.0.0.0:8680: bind: address already in use"))
	writeSettings(t, configDir, []byte(`{"remoteEnabled":true,"remoteHost":"127.0.0.1","remotePort":0}`))
	_, _ = app.runRemoteSecurityMigrationGate()
	if err := app.Settings.Load(); err != nil {
		t.Fatal(err)
	}
	if err := app.Remote.LoadSecurityState(); err != nil {
		t.Fatal(err)
	}
	if err := app.ToggleRemoteServer(true); err != nil {
		t.Fatal(err)
	}
	if app.getLastRemoteStartError() != "" {
		t.Fatal("successful toggle must clear lastStartError")
	}
	app.Remote.Stop()
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

type errStr string

func (e errStr) Error() string { return string(e) }
