package remote

// host_summary_degraded_export_test.go — P3-B R2：hostSummary 缓存最近错误态的
// 只读透出（hostSummaryCache.degradedSnapshot / Server.HostSummaryDegraded）。
//
// 锚定的行为契约：
//   - 只读：degradedSnapshot / HostSummaryDegraded 绝不触发 provider 调用、
//     绝不改变缓存状态（fresh cache 报告 false 而不是探测一次）；
//   - 最近错误态：provider 失败后为 true，恢复成功后回到 false（不粘死）；
//   - nil/legacy 安全：零值 Server 与无安全面（NewServer legacy）恒 false。

import (
	"embed"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/remote/contract"
)

func TestHostSummaryCache_DegradedSnapshotReadOnly(t *testing.T) {
	var calls int32
	cache := newHostSummaryCache(func() (contract.HostSummary, error) {
		atomic.AddInt32(&calls, 1)
		return validHostSummary()
	})

	// Fresh cache: no outcome yet → not degraded, and reading must NOT probe.
	for i := 0; i < 3; i++ {
		if cache.degradedSnapshot() {
			t.Fatal("fresh cache must not report degraded")
		}
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("degradedSnapshot triggered %d provider calls; must be read-only", n)
	}

	// Prime a success outcome → still not degraded.
	if _, err := cache.get(); err != nil {
		t.Fatalf("prime success: %v", err)
	}
	if cache.degradedSnapshot() {
		t.Fatal("successful outcome must not report degraded")
	}
}

func TestHostSummaryCache_DegradedSnapshotTracksLastOutcome(t *testing.T) {
	var fail atomic.Bool
	var calls int32
	cache := newHostSummaryCache(func() (contract.HostSummary, error) {
		if atomic.AddInt32(&calls, 1) < 0 {
			return contract.HostSummary{}, errors.New("unreachable")
		}
		if fail.Load() {
			return contract.HostSummary{}, errors.New("probe down")
		}
		return validHostSummary()
	})

	// Failure outcome → degraded latches (sticky beyond the 1s failure TTL:
	// this is the "most recent error state", the desktop self-check contract).
	fail.Store(true)
	if _, err := cache.get(); err == nil {
		t.Fatal("expected provider failure")
	}
	if !cache.degradedSnapshot() {
		t.Fatal("failed outcome must report degraded")
	}
	if !cache.degradedSnapshot() {
		t.Fatal("degraded must be sticky between reads (read-only accessor)")
	}

	// Recovery → degraded clears (cache un-sticks on success). Bust the cached
	// failure the same way the b2b tests do (skip the 1s failure TTL without
	// wall-clock sleeping) so the next get() hits the recovered provider.
	fail.Store(false)
	cache.mu.Lock()
	cache.cachedAt = time.Time{}
	cache.failed = false
	cache.mu.Unlock()
	if _, err := cache.get(); err != nil {
		t.Fatalf("recovered provider: %v", err)
	}
	if cache.degradedSnapshot() {
		t.Fatal("recovered outcome must clear degraded")
	}
}

func TestServer_HostSummaryDegraded_NilAndLegacySafety(t *testing.T) {
	var nilSrv *Server
	if nilSrv.HostSummaryDegraded() {
		t.Fatal("nil receiver must report false")
	}
	// Legacy NewServer has no security surface (v1sec nil) → false.
	legacy := NewServer(0, nil, logging.NewService(t.TempDir()), embed.FS{})
	if legacy.HostSummaryDegraded() {
		t.Fatal("legacy server (no v1 security surface) must report false")
	}
}

func TestServer_HostSummaryDegraded_ReflectsCacheAfterRequest(t *testing.T) {
	var fail atomic.Bool
	srv, _ := newSecServer(t, func() (contract.HostSummary, error) {
		if fail.Load() {
			return contract.HostSummary{}, errors.New("probe down")
		}
		return validHostSummary()
	})

	if srv.HostSummaryDegraded() {
		t.Fatal("no outcome yet → not degraded")
	}

	// Drive the cache exactly like a paired device request would (whitebox).
	fail.Store(true)
	if _, err := srv.v1sec.hostCache.get(); err == nil {
		t.Fatal("expected provider failure")
	}
	if !srv.HostSummaryDegraded() {
		t.Fatal("server must expose degraded after a failed provider outcome")
	}

	// Recovery clears it again (bustHostCache skips the 1s failure TTL).
	fail.Store(false)
	bustHostCache(srv)
	if _, err := srv.v1sec.hostCache.get(); err != nil {
		t.Fatalf("recovered provider: %v", err)
	}
	if srv.HostSummaryDegraded() {
		t.Fatal("server must clear degraded after provider recovery")
	}
}
