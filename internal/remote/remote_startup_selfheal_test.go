package remote

// remote_startup_selfheal_test.go — P3-B③ Startup 恢复路径自愈测试
//（Server.StartWithRetry 重试引擎；App 壳编排见 app.go healRemoteStartupRestore）。
//
// 锚定的行为契约（素材B ⑥ / 执行计划 P3-B）：
//   - 瞬时端口占用（旧实例关闭中残留）：退避重试后自愈 → running=true；
//   - 持续端口占用（负例）：重试耗尽 → 返回最后一次 bind 错误、running=false，
//     由 App 层将漂移对齐为可观察（启动警告 + GetRemoteStatus.lastStartError）；
//   - 空闲/已运行：立即成功且零重试回调（Start 幂等）；
//   - 关闭中的父 context：停止重试并透出待决错误，绝不返回裸 nil。

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// selfHealSecServer builds a security-enabled server ready to really Start
// (store loaded + pairing resumed), mirroring the App construction path.
func selfHealSecServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newSecServer(t, validHostSummary)
	srv.SetHost("127.0.0.1")
	return srv
}

// TestStartWithRetry_TransientPortConflictRecovers: 首次尝试失败（端口被占）→
// 首个失败回调释放端口 → 下一次重试成功自愈。
func TestStartWithRetry_TransientPortConflictRecovers(t *testing.T) {
	srv := selfHealSecServer(t)
	ln, port := occupyPortForSelfHeal(t)
	srv.SetPort(port)
	t.Cleanup(srv.Stop)

	var failures int32
	freeOnFirstFailure := func(attempt int, err error) {
		if atomic.AddInt32(&failures, 1) == 1 {
			ln.Close() // transient occupier exits after the first failed attempt
		}
	}

	delays := []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	if err := srv.StartWithRetry(context.Background(), delays, freeOnFirstFailure); err != nil {
		t.Fatalf("transient conflict must self-heal, got: %v", err)
	}
	if !srv.IsRunning() {
		t.Fatal("server must be running after transient-conflict self-heal")
	}
	if n := atomic.LoadInt32(&failures); n < 1 {
		t.Fatalf("expected at least one failed attempt before recovery, got %d", n)
	}
}

// TestStartWithRetry_PersistentConflictExhaustsAndErrors（负例）: 端口被持续
// 占用 → 全部重试耗尽 → 返回最后一次 bind 错误（含 address already in use），
// running=false；失败回调次数 = 总尝试次数（1 + len(delays)）。
func TestStartWithRetry_PersistentConflictExhaustsAndErrors(t *testing.T) {
	srv := selfHealSecServer(t)
	ln, port := occupyPortForSelfHeal(t)
	defer ln.Close()
	srv.SetPort(port)

	var attempts int32
	delays := []time.Duration{time.Millisecond, time.Millisecond}
	err := srv.StartWithRetry(context.Background(), delays, func(int, error) {
		atomic.AddInt32(&attempts, 1)
	})
	if err == nil {
		t.Fatal("persistent conflict must exhaust into an error")
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("exhausted error must carry the bind failure, got: %v", err)
	}
	if srv.IsRunning() {
		t.Fatal("server must stay stopped under a persistent port conflict")
	}
	if got := atomic.LoadInt32(&attempts); got != int32(1+len(delays)) {
		t.Fatalf("failed attempts = %d want %d (every failed attempt notifies)", got, 1+len(delays))
	}
}

// TestStartWithRetry_FreePortSucceedsWithoutRetry: 端口空闲（临时端口）→ 立即
// 成功，零失败回调、零延迟消耗。
func TestStartWithRetry_FreePortSucceedsWithoutRetry(t *testing.T) {
	srv := selfHealSecServer(t)
	srv.SetPort(0) // ephemeral bind — Start must succeed on the first attempt
	t.Cleanup(srv.Stop)

	var attempts int32
	start := time.Now()
	if err := srv.StartWithRetry(context.Background(), []time.Duration{time.Hour}, func(int, error) {
		atomic.AddInt32(&attempts, 1)
	}); err != nil {
		t.Fatalf("free port must start immediately: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 0 {
		t.Fatal("immediate success must not report failed attempts")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("immediate success must not consume backoff delays (elapsed %v)", elapsed)
	}
	if !srv.IsRunning() {
		t.Fatal("server must be running")
	}

	// Idempotency: StartWithRetry on an already-running server returns nil
	// immediately without touching the schedule.
	if err := srv.StartWithRetry(context.Background(), []time.Duration{time.Hour}, nil); err != nil {
		t.Fatalf("already-running server must return nil: %v", err)
	}
}

// TestStartWithRetry_CancelledContextStopsRetrying: 父 context 已取消 → 不再
// 追加尝试，返回待决错误（首试失败后取消时为最后一次 Start 错误；无任何
// 失败记录时为 context 错误，绝不裸 nil）。
func TestStartWithRetry_CancelledContextStopsRetrying(t *testing.T) {
	srv := selfHealSecServer(t)
	ln, port := occupyPortForSelfHeal(t)
	defer ln.Close()
	srv.SetPort(port)

	ctx, cancel := context.WithCancel(context.Background())
	var attempts int32
	// Cancel during the first backoff sleep, after attempt 0 already failed.
	hook := func(attempt int, err error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			cancel()
		}
	}
	start := time.Now()
	err := srv.StartWithRetry(ctx, []time.Duration{50 * time.Millisecond, 5 * time.Second}, hook)
	if err == nil {
		t.Fatal("cancelled retry loop must surface the pending error")
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("pending error must be the last Start failure, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("cancelled loop must stop before the remaining delays (elapsed %v)", elapsed)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts after cancel = %d want 1", got)
	}
}

// occupyPortForSelfHeal binds 127.0.0.1:0 and returns the listener and port.
func occupyPortForSelfHeal(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port
}
