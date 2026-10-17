package main

import (
	"context"
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
	if err := runWSLSharedFilesForSpec(context.Background(), runner, platform.ResolvedLaunchSpec{}); err != nil {
		t.Fatal(err)
	}
	if len(runner.callsSnapshot()) != 0 {
		t.Fatal("native launch performed WSL supply")
	}
}
