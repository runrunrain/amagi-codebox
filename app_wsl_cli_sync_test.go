package main

// app_wsl_cli_sync_test.go — P2-C 安装/升级双侧联动的 App 层门控/分派回归：
// shell=wsl 或 WSL 已装才同步（否则 skipped）、install 未装走 InstallTool /
// install 已装或 update 走 UpdateTool、claude_code→claude 与 omp 键映射、
// 无发行版返回 nil、WSL 侧失败原文保留，以及 UpdateCLIInWSL 绑定冒烟。

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/envcheck"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/wslsetup"
)

// fakeWSLCLISetup 是 wslCLISetupService 接缝的假实现：预设状态与两个操作
// 的返回值，记录调用。
type fakeWSLCLISetup struct {
	mu          sync.Mutex
	status      wslsetup.Status
	installRes  *wslsetup.InstallResult
	installErr  error
	updateRes   *wslsetup.InstallResult
	updateErr   error
	installKeys []string
	updateKeys  []string
}

func (f *fakeWSLCLISetup) GetStatus() wslsetup.Status { return f.status }

func (f *fakeWSLCLISetup) InstallTool(tool string) (*wslsetup.InstallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installKeys = append(f.installKeys, tool)
	return f.installRes, f.installErr
}

func (f *fakeWSLCLISetup) UpdateTool(tool string) (*wslsetup.InstallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateKeys = append(f.updateKeys, tool)
	return f.updateRes, f.updateErr
}

func (f *fakeWSLCLISetup) calls() (installs []string, updates []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.installKeys...), append([]string(nil), f.updateKeys...)
}

// newFakeSetup 构造带五 CLI 工具快照的假实现（installed 为已装键集）。
func newFakeSetup(installed ...string) *fakeWSLCLISetup {
	f := &fakeWSLCLISetup{status: wslsetup.Status{Available: true, Distro: "Ubuntu"}}
	for _, key := range []string{"claude", "opencode", "codex", "pi", "omp"} {
		ts := wslsetup.ToolStatus{Tool: key}
		for _, inst := range installed {
			if inst == key {
				ts.Installed = true
			}
		}
		f.status.Tools = append(f.status.Tools, ts)
	}
	return f
}

// newWSLCLISyncApp 构造注入接缝的 App：指定默认 distro（空串=不可用）与
// 所有 CLI 的 shell 设置（真实 settings 服务在 macOS 会把 "wsl" 归一化掉，
// 故经接缝注入）。
func newWSLCLISyncApp(t *testing.T, setup wslCLISetupService, distro, shell string) *App {
	t.Helper()
	app := newTestApp(t)
	app.Settings = settings.NewService(t.TempDir())
	if err := app.Settings.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	prevSetup, prevDistro, prevShell := wslCLISetupFor, wslDefaultDistroFn, wslShellSettingFor
	wslCLISetupFor = func(*App) wslCLISetupService { return setup }
	wslDefaultDistroFn = func([]string) string { return distro }
	wslShellSettingFor = func(*App, envcheck.CLITool) string { return shell }
	t.Cleanup(func() {
		wslCLISetupFor, wslDefaultDistroFn, wslShellSettingFor = prevSetup, prevDistro, prevShell
	})
	return app
}

// --- 门控：未使用 WSL 且未安装 → skipped，不触碰安装器 ---

func TestSyncCLIToWSL_GateSkippedWhenNotUsingWSLAndNotInstalled(t *testing.T) {
	setup := newFakeSetup() // 无已装
	app := newWSLCLISyncApp(t, setup, "Ubuntu", "pwsh")

	res := app.syncCLIToWSL(envcheck.ToolClaudeCode, envcheck.OperationKindInstall)
	if res == nil {
		t.Fatal("gated sync should return an explicit skipped result, not nil")
	}
	if res.Status != envcheck.WSLSyncSkipped {
		t.Errorf("Status = %q, want skipped", res.Status)
	}
	if res.Distro != "Ubuntu" {
		t.Errorf("Distro = %q, want Ubuntu（skipped 也带目标便于展示）", res.Distro)
	}
	if installs, updates := setup.calls(); len(installs) != 0 || len(updates) != 0 {
		t.Errorf("installer must not be touched on skip, got install=%v update=%v", installs, updates)
	}
}

// --- 门控：无有效发行版 → nil（前端表现与联动存在前一致） ---

func TestSyncCLIToWSL_NoDistroReturnsNil(t *testing.T) {
	setup := newFakeSetup()
	app := newWSLCLISyncApp(t, setup, "", "wsl")

	if res := app.syncCLIToWSL(envcheck.ToolOmp, envcheck.OperationKindUpdate); res != nil {
		t.Errorf("no-distro sync must return nil, got %+v", res)
	}
	if installs, updates := setup.calls(); len(installs) != 0 || len(updates) != 0 {
		t.Errorf("installer must not be touched without a distro, got install=%v update=%v", installs, updates)
	}
}

// --- 门控：headroom 与非 install/update kind → nil ---

func TestSyncCLIToWSL_HeadroomAndOtherKindsReturnNil(t *testing.T) {
	setup := newFakeSetup("claude")
	app := newWSLCLISyncApp(t, setup, "Ubuntu", "wsl")

	if res := app.syncCLIToWSL(envcheck.ToolHeadroom, envcheck.OperationKindInstall); res != nil {
		t.Errorf("headroom must not sync to WSL, got %+v", res)
	}
	if res := app.syncCLIToWSL(envcheck.ToolPi, envcheck.OperationKindUninstall); res != nil {
		t.Errorf("uninstall kind must not sync to WSL, got %+v", res)
	}
	if installs, updates := setup.calls(); len(installs) != 0 || len(updates) != 0 {
		t.Errorf("no installer calls expected, got install=%v update=%v", installs, updates)
	}
}

// --- 分派：install + shell=wsl + 未装 → InstallTool（claude_code → claude） ---

func TestSyncCLIToWSL_InstallDispatchesInstallToolWithClaudeKey(t *testing.T) {
	setup := newFakeSetup()
	setup.installRes = &wslsetup.InstallResult{Tool: "claude", Package: "@anthropic-ai/claude-code",
		Distro: "Ubuntu", Success: true, Version: "1.0.30", Message: "安装成功"}
	app := newWSLCLISyncApp(t, setup, "Ubuntu", "wsl")

	res := app.syncCLIToWSL(envcheck.ToolClaudeCode, envcheck.OperationKindInstall)
	if res == nil || res.Status != envcheck.WSLSyncSucceeded {
		t.Fatalf("res = %+v, want succeeded", res)
	}
	installs, updates := setup.calls()
	if len(installs) != 1 || installs[0] != "claude" {
		t.Errorf("InstallTool keys = %v, want [claude]（claude_code 键映射）", installs)
	}
	if len(updates) != 0 {
		t.Errorf("unexpected UpdateTool calls: %v", updates)
	}
	if res.Version != "1.0.30" || res.Distro != "Ubuntu" {
		t.Errorf("mapping lost fields: %+v", res)
	}
}

// --- 分派：install + 已装（即使 shell 非 wsl）→ UpdateTool ---

func TestSyncCLIToWSL_InstallWhenAlreadyInstalledDispatchesUpdateTool(t *testing.T) {
	setup := newFakeSetup("opencode")
	setup.updateRes = &wslsetup.InstallResult{Tool: "opencode", Success: true, Version: "0.5.0"}
	app := newWSLCLISyncApp(t, setup, "Ubuntu", "pwsh") // shell 非 wsl，但已装

	res := app.syncCLIToWSL(envcheck.ToolOpenCode, envcheck.OperationKindInstall)
	if res == nil || res.Status != envcheck.WSLSyncSucceeded {
		t.Fatalf("res = %+v, want succeeded", res)
	}
	installs, updates := setup.calls()
	if len(installs) != 0 {
		t.Errorf("unexpected InstallTool calls: %v", installs)
	}
	if len(updates) != 1 || updates[0] != "opencode" {
		t.Errorf("UpdateTool keys = %v, want [opencode]", updates)
	}
}

// --- 分派：update + shell=wsl + 未装 → UpdateTool（@latest 未装会补装） ---

func TestSyncCLIToWSL_UpdateDispatchesUpdateToolEvenWhenMissing(t *testing.T) {
	setup := newFakeSetup()
	setup.updateRes = &wslsetup.InstallResult{Tool: "codex", Success: true, Version: "0.9.0"}
	app := newWSLCLISyncApp(t, setup, "Ubuntu", "wsl")

	res := app.syncCLIToWSL(envcheck.ToolCodex, envcheck.OperationKindUpdate)
	if res == nil || res.Status != envcheck.WSLSyncSucceeded {
		t.Fatalf("res = %+v, want succeeded", res)
	}
	installs, updates := setup.calls()
	if len(installs) != 0 {
		t.Errorf("unexpected InstallTool calls: %v", installs)
	}
	if len(updates) != 1 || updates[0] != "codex" {
		t.Errorf("UpdateTool keys = %v, want [codex]", updates)
	}
}

// --- 键映射：omp → "omp"（P2-A 五 CLI 之一） ---

func TestSyncCLIToWSL_OmpKeyMapping(t *testing.T) {
	setup := newFakeSetup("omp")
	setup.updateRes = &wslsetup.InstallResult{Tool: "omp", Package: "@oh-my-pi/pi-coding-agent",
		Success: true, AlreadyOK: false, Version: "1.1.0"}
	app := newWSLCLISyncApp(t, setup, "Ubuntu", "zsh") // 已装即可同步

	res := app.syncCLIToWSL(envcheck.ToolOmp, envcheck.OperationKindUpdate)
	if res == nil || res.Status != envcheck.WSLSyncSucceeded {
		t.Fatalf("res = %+v, want succeeded", res)
	}
	_, updates := setup.calls()
	if len(updates) != 1 || updates[0] != "omp" {
		t.Errorf("UpdateTool keys = %v, want [omp]", updates)
	}
}

// --- WSL 侧失败：错误原文保留、状态 failed、不吞 ---

func TestSyncCLIToWSL_FailureMappingKeepsOriginalError(t *testing.T) {
	t.Run("result_failure", func(t *testing.T) {
		setup := newFakeSetup("pi")
		setup.updateRes = &wslsetup.InstallResult{Tool: "pi", Success: false,
			Error: "npm i -g @earendil-works/pi-coding-agent@latest 失败: EAI_AGAIN"}
		app := newWSLCLISyncApp(t, setup, "Ubuntu", "wsl")

		res := app.syncCLIToWSL(envcheck.ToolPi, envcheck.OperationKindUpdate)
		if res == nil || res.Status != envcheck.WSLSyncFailed {
			t.Fatalf("res = %+v, want failed", res)
		}
		if !strings.Contains(res.Error, "EAI_AGAIN") {
			t.Errorf("WSL 失败原文必须保留, got %q", res.Error)
		}
	})
	t.Run("nil_result_with_error", func(t *testing.T) {
		setup := newFakeSetup("pi")
		setup.updateErr = errors.New("wsl.exe 超时")
		app := newWSLCLISyncApp(t, setup, "Ubuntu", "wsl")

		res := app.syncCLIToWSL(envcheck.ToolPi, envcheck.OperationKindUpdate)
		if res == nil || res.Status != envcheck.WSLSyncFailed {
			t.Fatalf("res = %+v, want failed", res)
		}
		if !strings.Contains(res.Error, "wsl.exe 超时") {
			t.Errorf("err 原文必须保留, got %q", res.Error)
		}
	})

	// steer 抽查 #2 回归：res 与 err 同时非nil时，err 强制 failed，不得因
	// res.Success=true 报成功；err 与 res.Error 都保留。
	t.Run("result_and_error_both_set_err_wins", func(t *testing.T) {
		setup := newFakeSetup("pi")
		setup.updateRes = &wslsetup.InstallResult{Tool: "pi", Success: true, Version: "9.9.9", Error: "probe dirty state"}
		setup.updateErr = errors.New("runner 被看门狗中止")
		app := newWSLCLISyncApp(t, setup, "Ubuntu", "wsl")

		res := app.syncCLIToWSL(envcheck.ToolPi, envcheck.OperationKindUpdate)
		if res == nil || res.Status != envcheck.WSLSyncFailed {
			t.Fatalf("res = %+v, want forced failed", res)
		}
		if !strings.Contains(res.Error, "runner 被看门狗中止") || !strings.Contains(res.Error, "probe dirty state") {
			t.Errorf("err 与 res.Error 原文都应保留, got %q", res.Error)
		}
	})
}

// --- 门控快照可信度（steer 抽查 #2）：不可用/其它发行版快照的 installed 不判已装 ---

func TestSyncCLIToWSL_IgnoresUntrustedStatusSnapshots(t *testing.T) {
	t.Run("unavailable_snapshot_not_treated_as_installed", func(t *testing.T) {
		setup := newFakeSetup("claude")
		setup.status.Available = false // 探测失败：installed 位不可信
		setup.status.Distro = ""
		app := newWSLCLISyncApp(t, setup, "Ubuntu", "pwsh") // shell 非 wsl

		res := app.syncCLIToWSL(envcheck.ToolClaudeCode, envcheck.OperationKindUpdate)
		if res == nil || res.Status != envcheck.WSLSyncSkipped {
			t.Fatalf("不可用快照的 installed 不得判已装, res = %+v, want skipped", res)
		}
	})
	t.Run("foreign_distro_snapshot_not_treated_as_installed", func(t *testing.T) {
		setup := newFakeSetup("codex")
		setup.status.Distro = "Debian"                     // 与当前目标 Ubuntu 不一致
		app := newWSLCLISyncApp(t, setup, "Ubuntu", "zsh") // shell 非 wsl

		res := app.syncCLIToWSL(envcheck.ToolCodex, envcheck.OperationKindUpdate)
		if res == nil || res.Status != envcheck.WSLSyncSkipped {
			t.Fatalf("其它目标快照的 installed 不得判已装, res = %+v, want skipped", res)
		}
	})
	t.Run("matching_distro_snapshot_still_gates_install", func(t *testing.T) {
		// 对照：可用且同发行版的快照仍能判已装（install 已装 → UpdateTool）。
		setup := newFakeSetup("codex")
		setup.updateRes = &wslsetup.InstallResult{Tool: "codex", Success: true}
		app := newWSLCLISyncApp(t, setup, "Ubuntu", "zsh")

		res := app.syncCLIToWSL(envcheck.ToolCodex, envcheck.OperationKindInstall)
		if res == nil || res.Status != envcheck.WSLSyncSucceeded {
			t.Fatalf("res = %+v, want succeeded", res)
		}
		_, updates := setup.calls()
		if len(updates) != 1 {
			t.Errorf("已装（可信快照）install 应走 UpdateTool, got %v", updates)
		}
	})
}

// --- UpdateCLIInWSL 绑定冒烟：非 Windows 真实服务返回平台限制结果，不 panic ---

func TestUpdateCLIInWSL_NonWindowsReturnsPlatformLimitation(t *testing.T) {
	app := newTestApp(t)
	app.WSLSetup = wslsetup.NewService(app.Log)
	res, err := app.UpdateCLIInWSL("claude")
	if err != nil {
		t.Fatalf("UpdateCLIInWSL error = %v（平台限制应为结果而非错误）", err)
	}
	if res == nil || res.Success {
		t.Fatalf("non-Windows result should be a failed InstallResult, got %+v", res)
	}
	if res.Error == "" {
		t.Error("platform-limitation result must carry an error message")
	}
}
