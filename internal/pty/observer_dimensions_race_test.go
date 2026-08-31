//go:build darwin

package pty

import (
	"context"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// TestAttachObserverDimensionsNoRaceWithResize 回归：AttachSessionObserver 与
// Resize 并发时的 dimensions 读/写 data race。
//
// 根因（修复前）：darwin 后端的 Resize 在 ps.mu 下写 currentCols/currentRows，
// 而 AttachSessionObserver 只持 s.mu+historyMu 读同一字段——两把锁都不与
// Resize 的写构成 happens-before，移动端 attach（读 dimensions 快照）与桌面
// resize（写 dimensions）并发时构成真实 data race（-race 实证：写
// service_darwin.go Resize / 读 AttachSessionObserver）。修复：observer 改在
// ps.mu.RLock 下读取，与 GetPtyDimensions/RunningCount 的既有锁序一致
// （s.mu → ps.mu）。Windows 侧同名字段由 s.mu 统一保护，无此问题。
func TestAttachObserverDimensionsNoRaceWithResize(t *testing.T) {
	s := NewService(nil)
	spec := platform.ResolvedLaunchSpec{
		CLI:           platform.ResolvedCLI{Path: "/bin/sh", Args: []string{"-c", "sleep 30"}},
		BootstrapMode: platform.BootstrapDirectCommand,
	}
	if _, err := s.StartResolvedWithRun("observer-dimensions-race", spec, nil); err != nil {
		t.Fatalf("start resolved: %v", err)
	}
	defer func() { _ = s.Close("observer-dimensions-race") }()

	ctx := context.Background()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			if err := s.Resize(ctx, "observer-dimensions-race", 80+(i%40), 24+(i%20)); err != nil {
				t.Errorf("resize: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, _, _, err := s.AttachSessionObserver("observer-dimensions-race", "obs", func([]byte) {}, func(int, int) {}); err != nil {
				t.Errorf("attach: %v", err)
				return
			}
			s.DetachSessionObserver("observer-dimensions-race", "obs")
		}
	}()
	time.Sleep(1200 * time.Millisecond)
	close(done)
	wg.Wait()
}
