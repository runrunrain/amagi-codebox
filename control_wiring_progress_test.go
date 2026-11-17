package main

// control_wiring_progress_test.go — launch:progress 阶段进度事件的发射点与
// best-effort 守卫测试。发射点断言复用 launch_wsl_shared_test 的闸门阻断脚手架
// （gateStubRunner 不实现免重探面 → 供给回退既有 RunUnits 路径，阻断语义不变），
// 不依赖真机 wsl.exe，也不触达真实 PTY 启动。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/wslsupply"
)

// progressRecorder 替换 launchProgressEmitFn 接缝，记录 launch:progress 负载。
type progressRecorder struct {
	mu       sync.Mutex
	payloads []map[string]any
}

func (r *progressRecorder) record(_ context.Context, event string, data ...any) {
	if event != launchProgressEvent || len(data) != 1 {
		return
	}
	payload, ok := data[0].(map[string]any)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payloads = append(r.payloads, payload)
}

func (r *progressRecorder) stages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	stages := make([]string, 0, len(r.payloads))
	for _, p := range r.payloads {
		stages = append(stages, p["stage"].(string))
	}
	return stages
}

func installLaunchProgressRecorder(t *testing.T) *progressRecorder {
	t.Helper()
	rec := &progressRecorder{}
	orig := launchProgressEmitFn
	launchProgressEmitFn = rec.record
	t.Cleanup(func() { launchProgressEmitFn = orig })
	return rec
}

// TestLaunchProgressEmittedForWSLLaunchUpToGate：WSL 启动在预检前后发射两个
// preflight 事件（开始/完成）；供给闸门被阻断时不发射 supply（供给未完成）。
func TestLaunchProgressEmittedForWSLLaunchUpToGate(t *testing.T) {
	orig := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = orig })
	origProbe := wslLaunchPreflightProbeFn
	wslLaunchPreflightProbeFn = func(_ context.Context, distro, _ string) (platform.WSLLaunchPreflight, error) {
		if distro != "Ubuntu" {
			t.Fatalf("preflight distro = %q", distro)
		}
		return platform.WSLLaunchPreflight{
			Identity:        platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "machine-abc", OK: true},
			ResolvedWorkDir: "/home/u/project",
		}, nil
	}
	t.Cleanup(func() { wslLaunchPreflightProbeFn = origProbe })

	rec := installLaunchProgressRecorder(t)

	runner := &gateStubRunner{result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
		Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitBlocked, Detail: "withdrawal-unconfirmed-frozen",
	}}}}
	spec := platform.ResolvedLaunchSpec{AppType: string(session.AppTypeClaudeCode), BootstrapMode: platform.BootstrapWSL, WorkDir: "/mnt/c/project"}
	app := newTestApp(t)
	app.Sessions = session.NewManager()
	app.WSLSupply = runner
	app.control = remote.NewControlRuntime(remote.NewSystemClock(), app.Log)
	app.processRegistry = processcap.NewRegistry()
	_, reservation, err := app.reserveLaunchSession(session.AppTypeClaudeCode, "", "", "", session.ModeEmbedded, spec.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Sessions.AbortCreate(reservation)
	if _, err := app.launchEmbeddedPTY(reservation, launchplan.StableRecipe{}, spec); err == nil || !strings.Contains(err.Error(), "未确认撤回") {
		t.Fatalf("expected supply gate block, got: %v", err)
	}

	stages := rec.stages()
	if got := strings.Join(stages, ","); got != "preflight,preflight" {
		t.Fatalf("stages = %q, want \"preflight,preflight\" (gate blocked must not emit supply)", got)
	}
	for _, p := range rec.payloads {
		if p["appType"] != string(session.AppTypeClaudeCode) {
			t.Fatalf("appType = %v, want %q", p["appType"], session.AppTypeClaudeCode)
		}
		if _, ok := p["message"].(string); !ok {
			t.Fatalf("payload missing string message: %v", p)
		}
	}
}

// TestLaunchProgressNotEmittedForNativeLaunch：非 WSL 启动不发任何进度事件
// （前端无事件 → 维持现状文案）。control 置 nil 走顶部 ErrControlNotReady 早退，
// 断言零发射覆盖「非 WSL 路径不触碰发射面」。
func TestLaunchProgressNotEmittedForNativeLaunch(t *testing.T) {
	rec := installLaunchProgressRecorder(t)
	app := newTestApp(t)
	app.Sessions = session.NewManager()
	app.control = nil
	spec := platform.ResolvedLaunchSpec{AppType: string(session.AppTypePi)}
	_, reservation, err := app.reserveLaunchSession(session.AppTypePi, "", "", "", session.ModeEmbedded, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Sessions.AbortCreate(reservation)
	if _, err := app.launchEmbeddedPTY(reservation, launchplan.StableRecipe{}, spec); err == nil {
		t.Fatal("expected ErrControlNotReady for control-less app")
	}
	if stages := rec.stages(); len(stages) != 0 {
		t.Fatalf("native launch must not emit progress events, got %v", stages)
	}
}

// TestLaunchProgressEmitGuardedWithoutWailsContext：默认发射面对 nil/裸 ctx
// 必须静默跳过（Wails EventsEmit 对未携带 events 值的 ctx 会 log.Fatalf 杀死
// 进程）——测试进程存活通过即证明守卫生效。同时固定 stage 枚举字符串（与前端
// LAUNCH_STAGE_TEXT 键的跨语言契约）。
func TestLaunchProgressEmitGuardedWithoutWailsContext(t *testing.T) {
	emitLaunchProgressEvent(nil, launchProgressEvent, map[string]any{"stage": launchProgressStagePTY})
	emitLaunchProgressEvent(context.Background(), launchProgressEvent, map[string]any{"stage": launchProgressStagePTY})

	if launchProgressStagePreflight != "preflight" || launchProgressStageSupply != "supply" || launchProgressStagePTY != "pty" {
		t.Fatalf("stage enum drifted from frontend contract: %q/%q/%q",
			launchProgressStagePreflight, launchProgressStageSupply, launchProgressStagePTY)
	}
}
