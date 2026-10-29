package main

// R04：真实桌面外部启动/运行 lease 与 remote create/restart/Prepare 混合。
// Launcher、PTY、PoolStore 与 identity 均为隔离 fixture，不启动真实 CLI。
import (
	"context"
	"os"
	"testing"

	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/remote"
	"amagi-codebox/internal/remote/contract"
)

func newAbsentMixedLaunchFixture(t *testing.T) (*App, *transactionHarness, string) {
	t.Helper()
	h := newTransactionHarness(t, newTransactionPTY(t))
	// harness 隔离 HOME/USERPROFILE 时各用一个目录；本反例消费 canonical
	// 宿主槽位，两个变量须指向同一个隔离 home，不能误走独立根释放分支。
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	app, fake, _ := newR6ExternalLeaseApp(t)
	app.sharedCoord = h.coord
	app.claudeAccountsQuiescenceLister = func() ([]claudeProcessSample, error) { return nil, nil }
	identity := &fakeIdentityProvider{responses: map[string]claudeaccounts.ProfileIdentity{
		"SECRET-CC-A": {AccountUUID: "uuid-a", OrganizationUUID: "org-a"},
		"SECRET-PI-A": {AccountUUID: "uuid-a", OrganizationUUID: "org-a"},
	}}
	svc, store, _ := newRotationTestService(t, app, nil, nativeMatchingA(), identity)
	app.claudeAccounts = svc
	app.claudeAccountsPoolStore = store
	app.claudeAccountsPoolMarkerPath = t.TempDir() + "/marker"
	setClaudeAccountsCache(app, nil, nil, true)
	h.adapter.SetClaudeCredentialsAbsenceCheck(app.claudeCredentialsAbsenceCheck)
	h.adapter.SetClaudeCredentialsFingerprint(app.claudeCredentialsFingerprint)
	provider := configureR6ClaudeProvider(t, app)
	t.Cleanup(func() {
		for _, s := range app.Sessions.List() {
			if fake.IsRunning(s.ID) {
				if err := app.StopSession(s.ID); err != nil {
					t.Errorf("desktop fixture cleanup: %v", err)
				}
			}
		}
		waitR6(t, func() bool { return h.coord.LeaseCount(remote.SharedServiceClaudeCredentials) == 0 }, "desktop absent lease cleanup")
	})
	return app, h, provider
}

func assertMixedFirstCaptureBusy(t *testing.T, app *App) {
	t.Helper()
	// 同步领域入口验证真实 Gate，不向未启动的 Wails runtime 发事件；既有
	// TestAbsent*ReservationBlocksFirstCapture 另保留完整异步 facade 屏障。
	if _, err := app.claudeAccounts.CaptureFirstAccount(context.Background(), "first", 0); claudeaccounts.ReasonOf(err) != claudeaccounts.ReasonSessionsActive {
		t.Fatalf("混合启动不得放开 mutation，首次 capture 必须 busy: %v", err)
	}
	pool, err := app.claudeAccountsPoolStore.Load(context.Background())
	if err != nil || pool != nil {
		t.Fatalf("busy capture 不得建池: %v/%v", pool, err)
	}
}

func TestAbsentDesktopLeaseAllowsRemoteCreateAndRestart(t *testing.T) {
	for _, cli := range []contract.CLIType{contract.CLITypeClaudeCode, contract.CLITypePi} {
		t.Run(string(cli), func(t *testing.T) {
			app, h, provider := newAbsentMixedLaunchFixture(t)
			desktopID, err := app.LaunchSession(provider, "", "terminal", t.TempDir(), false, "")
			if err != nil {
				t.Fatalf("真实桌面启动: %v", err)
			}
			if desktopID == "" || h.coord.LeaseCount(remote.SharedServiceClaudeCredentials) != 1 {
				t.Fatal("桌面 absent 启动必须已成功提升运行 lease")
			}
			principal := remote.DevicePrincipal{DeviceID: "mixed-device"}
			created, aerr := h.adapter.CreateSession(context.Background(), "mixed-create", principal, contract.CreateSessionRequest{CLIType: cli})
			if aerr != nil {
				t.Fatalf("桌面 lease 存活时 remote create 必须成功: %v", aerr)
			}
			acquireTransactionControl(t, h, principal, created.Detail.ID, "mixed-control")
			if _, aerr := h.adapter.RestartSession(context.Background(), "mixed-restart", principal, created.Detail.ID); aerr != nil {
				t.Fatalf("桌面 lease 存活时 remote restart 必须成功: %v", aerr)
			}
			h.pty.mu.Lock()
			starts := len(h.pty.startSpecs)
			h.pty.mu.Unlock()
			if starts != 2 {
				t.Fatalf("create/restart 必须真实执行 PTY effect，实际=%d", starts)
			}
			assertMixedFirstCaptureBusy(t, app)
		})
	}
}

func TestAbsentRemotePrepareAllowsDesktopLaunch(t *testing.T) {
	for _, action := range []string{"create", "restart"} {
		t.Run(action, func(t *testing.T) {
			app, h, provider := newAbsentMixedLaunchFixture(t)
			stopAt := 1
			if action == "restart" {
				stopAt = 2
			}
			e := &prepareBarrierExecutor{Executor: newAppLaunchExecutor(launchExecutorDeps{pty: h.pty, sharedCoord: h.coord, debts: launchplan.NewCompensationDebtRegistry()}), entered: make(chan struct{}), resume: make(chan struct{}), stopAt: stopAt}
			h.adapter.SetLaunchExecutor(e, h.coord)
			principal := remote.DevicePrincipal{DeviceID: "mixed-prepare-device"}
			var sid contract.SessionID
			if action == "restart" {
				created, err := h.adapter.CreateSession(context.Background(), "prepare-first", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
				if err != nil {
					t.Fatal(err)
				}
				sid = created.Detail.ID
				acquireTransactionControl(t, h, principal, sid, "prepare-control")
			}
			finished := make(chan struct{})
			var remoteErr *remote.AdapterError
			go func() {
				defer close(finished)
				if action == "create" {
					_, remoteErr = h.adapter.CreateSession(context.Background(), "prepare-second", principal, contract.CreateSessionRequest{CLIType: contract.CLITypeClaudeCode})
				} else {
					_, remoteErr = h.adapter.RestartSession(context.Background(), "prepare-second", principal, sid)
				}
			}()
			// 无论断言是否失败，都退出 fake Prepare，避免遗留 goroutine。
			defer func() {
				close(e.resume)
				awaitBarrier(t, finished)
				if remoteErr == nil {
					t.Error("Prepare 注入失败必须向真实 remote 入口传播")
				}
			}()
			awaitBarrier(t, e.entered)
			assertMixedFirstCaptureBusy(t, app) // 尚无桌面 lease，只能由 remote reservation 阻止。
			desktopID, err := app.LaunchSession(provider, "", "terminal", t.TempDir(), false, "")
			if err != nil {
				t.Fatalf("remote Prepare 在途时真实桌面启动必须成功: %v", err)
			}
			if desktopID == "" || h.coord.LeaseCount(remote.SharedServiceClaudeCredentials) != 1 {
				t.Fatal("桌面必须成功提升 lease，不仅取得 claim")
			}
			assertMixedFirstCaptureBusy(t, app)
		})
	}
}
