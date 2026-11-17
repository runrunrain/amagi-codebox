package wslsupply

// wsl_launch_target_test.go — 启动预检免重探路径的钉子：TargetFromIdentity 的
// 构造等价与错误路径（身份 OK=false / 空 distro / 未知 CLI / UNC 不可达）；
// RunUnitsWithTarget 注入假探测面断言零身份探测且流程与 RunUnits 等价（写入
// → 幂等 Fresh），取消/未知单元校验与 RunUnits 同语义。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/platform"
)

// newLaunchTargetFixture 构造带一个 stub 投影单元的协调器（与
// newStubProjectionCoordinator 同型；独立函数便于双实例对比 RunUnits 与
// RunUnitsWithTarget 的流程等价）。
func newLaunchTargetFixture(t *testing.T) (*Coordinator, *fakeProbes) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp)
	c.RegisterProjection(&stubProjection{unit: "pi/stub-prefs", rel: "stub-prefs.json", source: func() []byte {
		return []byte(`{"v":1}`)
	}})
	return c, fp
}

func TestTargetFromIdentityMatchesResolveTarget(t *testing.T) {
	c, fp := newLaunchTargetFixture(t)
	probed, err := c.ResolveTarget(context.Background(), "Ubuntu", CLIPi)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	before := atomic.LoadInt32(&fp.identityCalls)
	fromIdent, err := c.TargetFromIdentity("Ubuntu", CLIPi, fp.identity)
	if err != nil {
		t.Fatalf("TargetFromIdentity: %v", err)
	}
	if got := atomic.LoadInt32(&fp.identityCalls); got != before {
		t.Fatalf("TargetFromIdentity must not probe identity: %d -> %d", before, got)
	}
	if probed != fromIdent {
		t.Fatalf("TargetFromIdentity target diverged:\nResolveTarget:    %+v\nTargetFromIdentity:%+v", probed, fromIdent)
	}
}

// TestTargetFromIdentityFailClosed 覆盖错误路径：与 ResolveTarget 同语义的
// fail-closed（不把失败当空目标）。
func TestTargetFromIdentityFailClosed(t *testing.T) {
	c, fp := newLaunchTargetFixture(t)
	ident := fp.identity
	ident.OK = false
	if _, err := c.TargetFromIdentity("Ubuntu", CLIPi, ident); err == nil || !strings.Contains(err.Error(), "cannot resolve default user identity") {
		t.Fatalf("identity OK=false must fail closed, got %v", err)
	}
	if _, err := c.TargetFromIdentity("  ", CLIPi, fp.identity); err == nil || !strings.Contains(err.Error(), "wsl distro is required") {
		t.Fatalf("empty distro must fail closed, got %v", err)
	}
	if _, err := c.TargetFromIdentity("Ubuntu", CLI("nope"), fp.identity); err == nil || !strings.Contains(err.Error(), "unsupported wsl supply CLI") {
		t.Fatalf("unknown CLI must fail closed, got %v", err)
	}
	// UNC 不可达：ToUNC 恒空串的注入探测面（NewCoordinator 不覆盖非 nil 字段）。
	uncBroken := NewCoordinator(t.TempDir(), nil, Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity { return fp.identity },
		ToUNC:    func(string, string) string { return "" },
		Chmod:    func(context.Context, string, string, ...string) error { return nil },
	})
	if _, err := uncBroken.TargetFromIdentity("Ubuntu", CLIPi, fp.identity); err == nil || !strings.Contains(err.Error(), "UNC share unreachable") {
		t.Fatalf("unreachable UNC must fail closed, got %v", err)
	}
}

// TestRunUnitsWithTargetZeroProbesAndEquivalentFlow 断言两件事：
//  1. RunUnitsWithTarget 全程零身份探测（入口不 ResolveTarget、Apply 不重探复核）；
//  2. 流程与 RunUnits 等价——同型协调器（同身份、同单元）首跑 UnitApplied 且
//     目标文件内容一致，重跑幂等 Fresh（no-change）。
func TestRunUnitsWithTargetZeroProbesAndEquivalentFlow(t *testing.T) {
	units := []UnitID{"pi/stub-prefs"}

	legacyC, legacyFP := newLaunchTargetFixture(t)
	legacyRes, err := legacyC.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, units)
	if err != nil {
		t.Fatalf("RunUnits: %v", err)
	}

	c, fp := newLaunchTargetFixture(t)
	target, err := c.TargetFromIdentity("Ubuntu", CLIPi, fp.identity)
	if err != nil {
		t.Fatalf("TargetFromIdentity: %v", err)
	}
	atomic.StoreInt32(&fp.identityCalls, 0)
	res, err := c.RunUnitsWithTarget(context.Background(), target, ReasonLaunch, units)
	if err != nil {
		t.Fatalf("RunUnitsWithTarget: %v", err)
	}
	if got := atomic.LoadInt32(&fp.identityCalls); got != 0 {
		t.Fatalf("RunUnitsWithTarget must not probe identity, got %d calls", got)
	}
	// 等价断言：首跑写入成功、结果面与 RunUnits 一致。
	if len(res.Results) != 1 || res.Results[0].Outcome != UnitApplied {
		t.Fatalf("RunUnitsWithTarget first run = %+v", res.Results)
	}
	if len(legacyRes.Results) != 1 || legacyRes.Results[0].Outcome != res.Results[0].Outcome {
		t.Fatalf("flow diverged: RunUnits=%+v RunUnitsWithTarget=%+v", legacyRes.Results, res.Results)
	}
	want, rerr := os.ReadFile(filepath.Join(legacyFP.uncRoot, "home/u/.pi/agent/stub-prefs.json"))
	got, rerr2 := os.ReadFile(filepath.Join(fp.uncRoot, "home/u/.pi/agent/stub-prefs.json"))
	if rerr != nil || rerr2 != nil || string(want) != string(got) {
		t.Fatalf("written bytes diverged (%v / %v)", rerr, rerr2)
	}
	// 重跑幂等：零变更 Fresh（同 RunUnits 语义），仍零探测。
	res2, err := c.RunUnitsWithTarget(context.Background(), target, ReasonLaunch, units)
	if err != nil {
		t.Fatalf("RunUnitsWithTarget re-run: %v", err)
	}
	if len(res2.Results) != 1 || res2.Results[0].Outcome != UnitFresh {
		t.Fatalf("RunUnitsWithTarget re-run must be Fresh, got %+v", res2.Results)
	}
	if got := atomic.LoadInt32(&fp.identityCalls); got != 0 {
		t.Fatalf("re-run must stay probe-free, got %d identity calls", got)
	}
}

// TestRunUnitsWithTargetKeepsEntryGuards 入口防线与 RunUnits 同语义：未知单元
// 报错；已取消请求以逐单元 context-canned 阻断结果返回且不触碰探测面。
func TestRunUnitsWithTargetKeepsEntryGuards(t *testing.T) {
	c, fp := newLaunchTargetFixture(t)
	target, err := c.TargetFromIdentity("Ubuntu", CLIPi, fp.identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunUnitsWithTarget(context.Background(), target, ReasonLaunch, []UnitID{"pi/nope"}); err == nil || !strings.Contains(err.Error(), "unknown wsl supply unit") {
		t.Fatalf("unknown unit must be rejected, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	atomic.StoreInt32(&fp.identityCalls, 0)
	res, err := c.RunUnitsWithTarget(ctx, target, ReasonLaunch, []UnitID{"pi/stub-prefs"})
	if err != nil {
		t.Fatalf("canceled run returns blocked results, not error: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "context-canceled" {
		t.Fatalf("canceled run = %+v", res.Results)
	}
	if got := atomic.LoadInt32(&fp.identityCalls); got != 0 {
		t.Fatalf("canceled run must not probe, got %d calls", got)
	}
}
