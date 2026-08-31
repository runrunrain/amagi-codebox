package envcheck

// probe_cache_concurrency_test.go — resetNPMCache 与探测路径的并发回归测试。
//
// 背景：resetNPMCache 在运行期整体替换 npmOnce/pythonOnce（sync.Once）并清空
// 结果字段，而检测路径（populateCanInstall → ensureNPMAvailabilityCached →
// Once.Do → probeNPMAvailability）在并发 goroutine 上执行。历史上二者无锁并发，
// 构成对 sync.Once 内部状态与缓存字段的数据竞争（go test -race 可复现）。
// 修复引入 Service.probeMu 串行化后，本测试必须在 -race 下保持干净。

import (
	"context"
	"os/exec"
	"testing"

	"amagi-codebox/internal/platform"
)

// probeBlockingRunner 在每次 Run 时先经 started 通知测试「探测已进入运行中」，
// 再阻塞到 release 关闭，用于确定性制造「探测 in-flight 时 resetNPMCache
// 被并发调用」的窗口。
type probeBlockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *probeBlockingRunner) Run(ctx context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	select {
	case r.started <- struct{}{}:
	case <-ctx.Done():
		return &platform.ProcessResult{}, ctx.Err()
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return &platform.ProcessResult{}, ctx.Err()
	}
	return &platform.ProcessResult{Stdout: "10.9.8\n"}, nil
}

func (r *probeBlockingRunner) Start(spec platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// TestResetNPMCacheConcurrentWithProbe 必须以 -race 运行才能发挥回归作用：
// 旧实现中 resetNPMCache 直接覆写 Once/结果字段，与 in-flight 探测的写入
// 构成数据竞争；修复后二者经 probeMu 串行化。
func TestResetNPMCacheConcurrentWithProbe(t *testing.T) {
	runner := &probeBlockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc := NewServiceWithRunner(runner)

	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		// 走与生产 CheckOne 相同的探测入口（Once.Do + 结果字段写入）。
		svc.populateCanInstall(&CheckStatus{Tool: ToolCodex})
	}()

	select {
	case <-runner.started:
	case <-probeDone:
		t.Fatal("populateCanInstall finished before the npm probe started")
	}

	// 探测 in-flight 期间触发 reset。修复后 reset 会等待 probeMu 释放，
	// 与探测串行化，不再产生并发写。
	resetDone := make(chan struct{})
	go func() {
		defer close(resetDone)
		svc.resetNPMCache()
	}()

	close(runner.release)

	<-probeDone
	<-resetDone
}
