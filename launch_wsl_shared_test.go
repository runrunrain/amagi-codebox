package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/wslsupply"
)

func TestWSLSharedGateBeforeDesktopAndRemotePTYForEveryCLI(t *testing.T) {
	orig := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = orig })
	// 新语义（启动预检融合）：桌面链路在供给闸门前先跑一次融合预检——注入
	// 假预检（身份 + 定谳目录）聚焦闸门断言，不依赖真机 wsl.exe；
	// gateStubRunner 不实现 RunUnitsWithTarget 面，供给自动回退既有 RunUnits
	// 路径（本测试断言的闸门语义不变）。
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
	for _, cli := range []session.AppType{session.AppTypeClaudeCode, session.AppTypeCodex, session.AppTypeOpenCode, session.AppTypePi, session.AppTypeOhMyPi} {
		t.Run(string(cli), func(t *testing.T) {
			runner := &gateStubRunner{result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
				Unit: wslsupply.UnitSharedVisionExport, Outcome: wslsupply.UnitBlocked, Detail: "withdrawal-unconfirmed-frozen",
			}}}}
			spec := platform.ResolvedLaunchSpec{AppType: string(cli), BootstrapMode: platform.BootstrapWSL, WorkDir: "/mnt/c/project"}
			app := newTestApp(t)
			app.Sessions = session.NewManager()
			app.WSLSupply = runner
			app.control = remote.NewControlRuntime(remote.NewSystemClock(), app.Log)
			app.processRegistry = processcap.NewRegistry()
			_, reservation, err := app.reserveLaunchSession(cli, "", "", "", session.ModeEmbedded, spec.WorkDir)
			if err != nil {
				t.Fatal(err)
			}
			defer app.Sessions.AbortCreate(reservation)
			// The nil PTY must never be reached, nor may activation become public.
			if _, err := app.launchEmbeddedPTY(reservation, launchplan.StableRecipe{}, spec); err == nil || !strings.Contains(err.Error(), "未确认撤回") {
				t.Fatalf("desktop did not honor shared withdrawal gate: %v", err)
			}
			if len(app.Sessions.List()) != 0 {
				t.Fatal("blocked desktop launch became visible")
			}
			pty := newTransactionPTY(t)
			effect := &ptyStartEffect{deps: launchExecutorDeps{pty: pty, wslSupply: runner}, sessionID: "test", spec: spec}
			if _, err := effect.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "未确认撤回") {
				t.Fatalf("remote/restart did not honor shared withdrawal gate: %v", err)
			}
			if len(pty.startSpecs) != 0 {
				t.Fatal("remote PTY started despite failed withdrawal")
			}
			if len(runner.callsSnapshot()) != 2 {
				t.Fatalf("expected both entry chains to reconcile: %v", runner.callsSnapshot())
			}
			// Once withdrawal succeeds, the same remote/restart effect may start.
			runner.result.Results[0].Outcome = wslsupply.UnitApplied
			runner.result.Results[0].Detail = "withdrawn"
			if _, err := effect.Apply(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(pty.startSpecs) != 1 {
				t.Fatal("safe launch was not started")
			}
			effect.compensate(context.Background())
		})
	}
}

func TestWSLSharedGateDoesNotProbeForNativeLaunch(t *testing.T) {
	orig := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { t.Fatal("native launch probed WSL"); return "" }
	t.Cleanup(func() { wslDefaultDistroFn = orig })
	runner := &gateStubRunner{}
	if err := runWSLSharedFilesForSpec(context.Background(), runner, platform.ResolvedLaunchSpec{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(runner.callsSnapshot()) != 0 {
		t.Fatal("native launch performed WSL supply")
	}
}

// preflightTargetRunnerStub 在 gateStubRunner 之上实现免重探供给面（生产
// *wslsupply.Coordinator 的行为面）：RunUnits 记 "legacy:" 前缀，
// TargetFromIdentity/RunUnitsWithTarget 记 "with:" 前缀，供两族路径断言。
type preflightTargetRunnerStub struct {
	gateStubRunner
	targetErr error
	// targets 记录每次 RunUnitsWithTarget 收到的目标（按 CLI 作用域分别构造）。
	targets []string
}

// RunUnits 覆写 gateStubRunner 的记录（加 legacy: 前缀），与 with: 前缀区分两族路径。
func (s *preflightTargetRunnerStub) RunUnits(_ context.Context, _ string, cli wslsupply.CLI, reason wslsupply.Reason, units []wslsupply.UnitID) (wslsupply.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "legacy:"+string(cli)+"|"+string(reason)+"|"+unitNames(units))
	if s.runErr != nil {
		return wslsupply.RunResult{}, s.runErr
	}
	return s.result, nil
}

func (s *preflightTargetRunnerStub) TargetFromIdentity(distro string, cli wslsupply.CLI, ident platform.WSLUserIdentity) (wslsupply.TargetRef, error) {
	if s.targetErr != nil {
		return wslsupply.TargetRef{}, s.targetErr
	}
	return wslsupply.TargetRef{Distro: distro, CLI: cli, UID: ident.UID, Home: ident.Home, LinuxRoot: "root/" + string(cli)}, nil
}

func (s *preflightTargetRunnerStub) RunUnitsWithTarget(_ context.Context, target wslsupply.TargetRef, reason wslsupply.Reason, units []wslsupply.UnitID) (wslsupply.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "with:"+string(target.CLI)+"|"+string(reason)+"|"+unitNames(units))
	s.targets = append(s.targets, string(target.CLI)+":"+target.UID+"@"+target.Home)
	return s.result, nil
}

func newPreflightGateSpec(appType string) platform.ResolvedLaunchSpec {
	return platform.ResolvedLaunchSpec{AppType: appType, BootstrapMode: platform.BootstrapWSL, WorkDir: "/mnt/c/project"}
}

func newTestPreflight() *platform.WSLLaunchPreflight {
	return &platform.WSLLaunchPreflight{
		Identity:        platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "machine-abc", OK: true},
		ResolvedWorkDir: "/home/u/project",
	}
}

// TestWSLSharedGatePreflightUsesRunUnitsWithTarget 预检身份传入时：shared 与
// cli-config 两轮都走 RunUnitsWithTarget（目标按 CLICommon/CLIClaude 两个作用域
// 分别用预检身份构造），零次既有 RunUnits（不重探身份）。
func TestWSLSharedGatePreflightUsesRunUnitsWithTarget(t *testing.T) {
	orig := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = orig })
	// claude/codex 键级单元已同意 → cli-config 轮会真正执行。
	runner := &preflightTargetRunnerStub{}
	runner.status = wslsupply.TargetStatus{Units: []wslsupply.UnitStatus{
		{Unit: wslsupply.UnitClaudeMCP, Consented: true},
		{Unit: wslsupply.UnitClaudeSettings, Consented: true},
	}}
	if err := runWSLSharedFilesForSpec(context.Background(), runner, newPreflightGateSpec("claudecode"), newTestPreflight()); err != nil {
		t.Fatal(err)
	}
	calls := runner.callsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("expected shared + cli-config runs, got %v", calls)
	}
	if !strings.HasPrefix(calls[0], "with:shared|launch|") || !strings.Contains(calls[0], "shared/vision-export") {
		t.Fatalf("shared round must run with-target CLICommon, got %q", calls[0])
	}
	if !strings.HasPrefix(calls[1], "with:claude|launch|") || !strings.Contains(calls[1], "claude/mcp") {
		t.Fatalf("cli-config round must run with-target CLIClaude, got %q", calls[1])
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "legacy:") {
			t.Fatalf("preflight path must not fall back to RunUnits: %v", calls)
		}
	}
	// 两轮目标都由预检身份构造（uid/home 透传，作用域各异）。
	for _, tgt := range runner.targets {
		if tgt != "shared:1000@/home/u" && tgt != "claude:1000@/home/u" {
			t.Fatalf("targets must derive from preflight identity, got %v", runner.targets)
		}
	}
}

// TestWSLSharedGateNilPreflightFallsBackToRunUnits nil 预检（remote/restart/
// 旧调用）：两轮走既有 RunUnits 路径，零次 RunUnitsWithTarget——回退语义完全
// 不变。
func TestWSLSharedGateNilPreflightFallsBackToRunUnits(t *testing.T) {
	orig := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = orig })
	runner := &preflightTargetRunnerStub{}
	runner.status = wslsupply.TargetStatus{Units: []wslsupply.UnitStatus{
		{Unit: wslsupply.UnitClaudeMCP, Consented: true},
		{Unit: wslsupply.UnitClaudeSettings, Consented: true},
	}}
	if err := runWSLSharedFilesForSpec(context.Background(), runner, newPreflightGateSpec("claudecode"), nil); err != nil {
		t.Fatal(err)
	}
	calls := runner.callsSnapshot()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "legacy:") || !strings.HasPrefix(calls[1], "legacy:") {
		t.Fatalf("nil preflight must use legacy RunUnits for both rounds, got %v", calls)
	}
}

// TestWSLSharedGatePreflightTargetUnresolvedFailsClosed 预检身份传入但目标不可
// 构造（TargetFromIdentity 报错，如 UNC 不可达）：按 RunUnits 的
// target-unresolved 阻断语义产出逐单元结果，不回退重探（零次任何 Run）。
func TestWSLSharedGatePreflightTargetUnresolvedFailsClosed(t *testing.T) {
	orig := wslDefaultDistroFn
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	t.Cleanup(func() { wslDefaultDistroFn = orig })
	runner := &preflightTargetRunnerStub{targetErr: errors.New("WSL UNC share unreachable for distro \"Ubuntu\"")}
	if err := runWSLSharedFilesForSpec(context.Background(), runner, newPreflightGateSpec("pi"), newTestPreflight()); err != nil {
		t.Fatalf("blocked result must flow through the gate decision, not error: %v", err)
	}
	if calls := runner.callsSnapshot(); len(calls) != 0 {
		t.Fatalf("unresolvable target must not run any units (fail-closed, no re-probe), got %v", calls)
	}
}
