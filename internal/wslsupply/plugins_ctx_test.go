package wslsupply

// plugins_ctx_test.go — 攻坚契约 PR07 插件后继提交回归：真实 ctx 到 filters
// 边界；原生 install/remove 副作用摘要与 filters 分账；首次失败也有待确认
// 摘要（重启不自动重放）；runScript 返回 nil 但 ctx 已结束 → canceled 不执行
// 后继写。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// cancelingRunner 在首次脚本执行后取消请求 ctx（模拟 install 完成后、filters
// 前的取消边界）。
type cancelingRunner struct {
	inner  PluginCommandRunner
	cancel context.CancelFunc
}

func (r *cancelingRunner) run(ctx context.Context, distro, script string) (string, error) {
	out, err := r.inner(ctx, distro, script)
	if strings.HasPrefix(script, "pi install") {
		// install 原生命令已执行（副作用可能已落地）后、filters 前取消。
		r.cancel()
	}
	return out, err
}

// ScriptCount 返回已执行的 distro 命令数。
func (d *fakePluginDistro) ScriptCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.scripts)
}

// TestAttackPluginInstallThenCancelKeepsPendingNoFilters：install 完成后取消
// → native 副作用可能已应用、filters 不后继写：canceled 非 OK/Installed，摘要
// 跨重启保留（PendingOps 优先于旧 installed 展示）。
func TestAttackPluginInstallThenCancelKeepsPendingNoFilters(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &cancelingRunner{inner: d.run, cancel: cancel}
	c.SetPluginCommandRunner(func(ctx context.Context, distro, script string) (string, error) {
		return runner.run(ctx, distro, script)
	})
	res := c.RunPluginOperation(base, "Ubuntu", PluginOperation{
		Source: "npm:demo@1.0.0", Kind: PluginOpInstall, AnchorRef: "1.0.0",
	})
	if res.Outcome == PluginOutcomeInstalled || res.OK {
		t.Fatalf("canceled-after-install must not report OK/Installed, got %+v", res)
	}
	if res.ErrCode != PluginErrCanceled && !strings.Contains(res.Detail, "取消") {
		t.Fatalf("result must be attributed to cancellation, got %+v", res)
	}
	before := d.ScriptCount()
	// 摘要保留为未决（首次失败也有待确认摘要；重启不自动重放）。
	if got := c.PluginRegistrationSummary().PendingOps; got < 1 {
		t.Fatalf("pending plugin summary must survive the failure, got %d", got)
	}
	// filters 不后继写：settings 无过滤对象（install 的 native 副作用已可能
	// 落地——由复检面如实呈现，不宣称回滚）。
	settings := mustReadSettings(t, d)
	if strings.Contains(settings, "skills") {
		t.Fatalf("filters must not be applied after cancellation, got %s", settings)
	}
	// 重启（新协调器、同一 configDir）后摘要仍在，且不自动重放（无命令执行）。
	c2 := NewCoordinator(filepath.Dir(c.state.path), nil, Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m-s5", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(d.uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(context.Context, string, string, ...string) error { return nil },
	})
	c2.SetPluginSourceParser(testPluginParser, testRefCheck)
	if got := c2.PluginRegistrationSummary().PendingOps; got < 1 {
		t.Fatalf("pending summary must survive restart, got %d", got)
	}
	if d.ScriptCount() != before {
		t.Fatalf("restart must not auto-replay plugin operations, scripts %d -> %d", before, d.ScriptCount())
	}
}

// TestAttackPluginFiltersNotAppliedWhenCanceledAtEntry：请求开始前已取消 →
// 不执行任何 distro 命令、不写摘要。
func TestAttackPluginFiltersNotAppliedWhenCanceledAtEntry(t *testing.T) {
	d := newFakePluginDistro(t)
	c, _ := newPluginTestCoordinator(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := c.RunPluginOperation(ctx, "Ubuntu", PluginOperation{
		Source: "npm:demo@1.0.0", Kind: PluginOpInstall, AnchorRef: "1.0.0",
	})
	if res.Outcome != PluginOutcomeFailed || res.ErrCode != PluginErrCanceled {
		t.Fatalf("pre-canceled request must be attributed canceled, got %+v", res)
	}
	if c.PluginRegistrationSummary().PendingOps != 0 {
		t.Fatal("pre-canceled request must not persist a summary")
	}
	if d.ScriptCount() != 0 {
		t.Fatalf("pre-canceled request must not run distro commands, got %d", d.ScriptCount())
	}
}
