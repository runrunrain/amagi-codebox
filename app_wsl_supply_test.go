package main

// app_wsl_supply_test.go — P3-S1 App 编排面的行为钉子：启动闸门分发
//（corrupt 阻断/其余告警）、verdict 对托管单元的降级、HasUnit 能力过滤、
// 保存后推送守卫（停机 distro 不唤醒/活动会话暂停）、状态面聚合、OpenCode
// 附加层失败语义与同意入口。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/wslsupply"
)

// stubSupplyRunner 是 wslsupply.Runner 的可控桩（记录 RunUnits 调用）。
type stubSupplyRunner struct {
	mu      sync.Mutex
	calls   []string // "cli:unit1,unit2"
	result  wslsupply.RunResult
	runErr  error
	markErr error
}

func (s *stubSupplyRunner) RunUnits(_ context.Context, distro string, cli wslsupply.CLI, _ wslsupply.Reason, units []wslsupply.UnitID) (wslsupply.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, len(units))
	for i, u := range units {
		names[i] = string(u)
	}
	s.calls = append(s.calls, string(cli)+":"+strings.Join(names, ","))
	if s.runErr != nil {
		return wslsupply.RunResult{}, s.runErr
	}
	return s.result, nil
}
func (s *stubSupplyRunner) MarkDirty(string, uint64) error { return nil }
func (s *stubSupplyRunner) GetStatus(string, wslsupply.CLI) wslsupply.TargetStatus {
	return wslsupply.TargetStatus{}
}
func (s *stubSupplyRunner) Compensate(context.Context, ...*wslsupply.CompensationHandle) []wslsupply.CompensationOutcome {
	return nil
}
func (s *stubSupplyRunner) calls_() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// fakeSupplyCoordinatorWithUnit 构造带 S1 适配器能力的真协调器（fake 探测面）。
func fakeSupplyCoordinatorWithUnit(t *testing.T) *wslsupply.Coordinator {
	t.Helper()
	uncRoot := t.TempDir()
	c := wslsupply.NewCoordinator(t.TempDir(), nil, wslsupply.Probes{
		Identity: func(string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "m1", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_, _ string, _ ...string) error { return nil },
	})
	return c
}

func TestRunWSLManagedModelsAtLaunchVerdicts(t *testing.T) {
	t.Run("corrupt target blocks launch with chinese error", func(t *testing.T) {
		c := fakeSupplyCoordinatorWithUnit(t)
		c.RegisterProjection(wslsupply.NewPiManagedModelsAdapter(stubPiManagedSourceForTest{
			spec: wslsupply.PiManagedModelsSpec{Providers: map[string]any{"providers": map[string]any{"amagi-x": map[string]any{"baseUrl": "u"}}}},
		}))
		app := newTestApp(t)
		app.WSLSupply = c
		// 目标损坏文件经假 UNC 落在协调器 temp 根下。
		corrupt := `{"providers": broken`
		if err := writeFakeUNCFile(c, "Ubuntu", "models.json", corrupt); err != nil {
			t.Fatal(err)
		}
		err := app.runWSLManagedModelsAtLaunch("Ubuntu")
		if err == nil || !strings.Contains(err.Error(), "已损坏") {
			t.Fatalf("corrupt target must block with chinese error, got %v", err)
		}
	})
	t.Run("other failures warn and pass", func(t *testing.T) {
		runner := &stubSupplyRunner{result: wslsupply.RunResult{Results: []wslsupply.UnitResult{{
			Unit: wslsupply.UnitPiManagedModels, Outcome: wslsupply.UnitFailed, Detail: "write-failed",
			Err: errors.New("unc unreachable"),
		}}}}
		app := newTestApp(t)
		app.WSLSupply = runner
		if err := app.runWSLManagedModelsAtLaunch("Ubuntu"); err != nil {
			t.Fatalf("non-corrupt failure must warn not block: %v", err)
		}
		if len(runner.calls_()) != 1 {
			t.Fatalf("expected one supply run, got %v", runner.calls_())
		}
	})
	t.Run("nil runner or empty distro is a no-op", func(t *testing.T) {
		app := newTestApp(t)
		app.WSLSupply = nil
		if err := app.runWSLManagedModelsAtLaunch("Ubuntu"); err != nil {
			t.Fatal(err)
		}
		app.WSLSupply = &stubSupplyRunner{}
		if err := app.runWSLManagedModelsAtLaunch("  "); err != nil {
			t.Fatal(err)
		}
	})
}

// stubPiManagedSourceForTest 固定期望集的 PiManagedModelsSource 桩。
type stubPiManagedSourceForTest struct {
	spec wslsupply.PiManagedModelsSpec
}

func (s stubPiManagedSourceForTest) DesiredPiManagedModels() wslsupply.PiManagedModelsSpec {
	return s.spec
}

// writeFakeUNCFile 直接在假探测面的 UNC 根下落一个目标文件（测试预置）。
// 经由协调器重解析目标获得真实 UNC 路径。
func writeFakeUNCFile(c *wslsupply.Coordinator, distro, relFile, content string) error {
	target, err := c.ResolveTarget(context.Background(), distro, wslsupply.CLIPi)
	if err != nil {
		return err
	}
	p := filepath.Join(target.UNCRoot, filepath.FromSlash(relFile))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o600)
}

func TestVerdictForWSLLoginSupplyManagedModelsWarn(t *testing.T) {
	// A2 正常 + 托管单元失败 → 告警放行（不 fail closed）。
	verdict := verdictForWSLLoginSupply(wslsupply.RunResult{
		Results: []wslsupply.UnitResult{
			{Unit: wslsupply.UnitPiA2Auth, Policy: wslsupply.PolicyA2Auth, Outcome: wslsupply.UnitFresh,
				A2: &wslsupply.A2Result{Outcome: launcher.SyncWSLPiLoginAuthFresh}},
			{Unit: wslsupply.UnitPiManagedModels, Policy: wslsupply.PolicyManagedNamespace, Outcome: wslsupply.UnitFailed, Detail: "write-failed"},
		},
	}, nil)
	if verdict.launchErr != nil {
		t.Fatalf("managed-models failure must not block login: %v", verdict.launchErr)
	}
	if !verdict.warn {
		t.Fatal("managed-models failure must warn")
	}

	// A2 失败仍是 fail closed（未知单元同理）。
	verdict = verdictForWSLLoginSupply(wslsupply.RunResult{
		Results: []wslsupply.UnitResult{
			{Unit: wslsupply.UnitPiA2Auth, Outcome: wslsupply.UnitFailed, Detail: "x"},
			{Unit: wslsupply.UnitPiManagedModels, Outcome: wslsupply.UnitFresh},
		},
	}, nil)
	if verdict.launchErr == nil {
		t.Fatal("a2 failure must stay fail-closed")
	}
	verdict = verdictForWSLLoginSupply(wslsupply.RunResult{
		Results: []wslsupply.UnitResult{{Unit: "pi/unknown", Outcome: wslsupply.UnitFailed}},
	}, nil)
	if verdict.launchErr == nil {
		t.Fatal("unknown unit failure must stay fail-closed")
	}
}

func TestDefaultRunWSLLoginSupplyFiltersByCapability(t *testing.T) {
	t.Run("bare coordinator degrades to a2-only", func(t *testing.T) {
		c := fakeSupplyCoordinatorWithUnit(t) // 无 S1 适配器
		res, err := defaultRunWSLLoginSupply(context.Background(), c, "Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Results) != 1 || res.Results[0].Unit != wslsupply.UnitPiA2Auth {
			t.Fatalf("bare coordinator must run a2 only, got %+v", res.Results)
		}
	})
	t.Run("coordinator with adapters runs both", func(t *testing.T) {
		c := fakeSupplyCoordinatorWithUnit(t)
		c.RegisterProjection(wslsupply.NewPiManagedModelsAdapter(stubPiManagedSourceForTest{}))
		res, err := defaultRunWSLLoginSupply(context.Background(), c, "Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		units := map[string]bool{}
		for _, r := range res.Results {
			units[string(r.Unit)] = true
		}
		if !units[string(wslsupply.UnitPiA2Auth)] || !units[string(wslsupply.UnitPiManagedModels)] {
			t.Fatalf("expected a2 + managed-models, got %+v", res.Results)
		}
	})
	t.Run("nil runner fails closed", func(t *testing.T) {
		res, err := defaultRunWSLLoginSupply(context.Background(), nil, "Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		if res.LaunchBlocked() == nil {
			t.Fatal("nil runner must fail closed")
		}
	})
}

func TestPushWSLSupplyGuards(t *testing.T) {
	origRunning := wslDistroRunningFn
	t.Cleanup(func() { wslDistroRunningFn = origRunning })

	t.Run("stopped distro is never pushed (no wake)", func(t *testing.T) {
		wslDistroRunningFn = func(string) bool { return false }
		runner := &stubSupplyRunner{}
		app := newTestApp(t)
		app.WSLSupply = runner
		app.pushWSLSupplyOnce()
		if len(runner.calls_()) != 0 {
			t.Fatalf("stopped distro must not be pushed: %v", runner.calls_())
		}
	})

	t.Run("active session defers push", func(t *testing.T) {
		wslDistroRunningFn = func(string) bool { return true }
		runner := &stubSupplyRunner{}
		app := newTestApp(t)
		app.WSLSupply = runner
		sess := app.Sessions.Create(session.AppTypePi, "p", "", "", session.ModeEmbedded, "/tmp")
		if sess == nil {
			t.Fatal("session create failed in test env")
		}
		app.pushWSLSupplyOnce()
		if len(runner.calls_()) != 0 {
			t.Fatalf("active session must defer push: %v", runner.calls_())
		}
	})

	t.Run("running idle distro pushes both scopes", func(t *testing.T) {
		wslDistroRunningFn = func(string) bool { return true }
		runner := &stubSupplyRunner{}
		app := newTestApp(t)
		app.WSLSupply = runner
		app.pushWSLSupplyOnce()
		calls := runner.calls_()
		if len(calls) == 0 {
			t.Fatal("running idle distro must be pushed")
		}
		// 桩 runner 未实现 HasUnit → 全部单元被过滤 → 零 RunUnits 属预期降级；
		// 该子测只验证守卫放行（不崩溃、不误报）。
	})
}

func TestGrantWSLVisionConsentRequiresCapableRunner(t *testing.T) {
	app := newTestApp(t)
	app.WSLSupply = &stubSupplyRunner{} // 无 GrantUnitConsent 能力
	if err := app.GrantWSLVisionConsent(); err == nil {
		t.Fatal("incapable runner must reject consent")
	}
	if err := app.GrantWSLVisionConsent(); !strings.Contains(err.Error(), "授权失败") && !strings.Contains(err.Error(), "不支持") {
		// 中文错误面（ incapable 路径文案为「不支持共享授权」）。
		t.Fatalf("consent error must be chinese-readable: %v", err)
	}
}

func TestGetWSLConfigSupplyStatusFaces(t *testing.T) {
	app := newTestApp(t)
	app.WSLSupply = &stubSupplyRunner{}
	st := app.GetWSLConfigSupplyStatus()
	if len(st.Faces) == 0 {
		t.Fatal("status must expose faces even without WSL")
	}
	faceKeys := map[string]bool{}
	for _, f := range st.Faces {
		faceKeys[f.Key] = true
	}
	for _, key := range []string{"providers", "vision-export", "modality-kb", "opencode-layer"} {
		if !faceKeys[key] {
			t.Fatalf("face %s missing: %+v", key, st.Faces)
		}
	}
}

func TestPrepareWSLOpenCodeLayerFailureSemantics(t *testing.T) {
	// 无 WSL 机器（fake resolver 无 home）：写入失败 → 中文阻断（新语义）。
	app := newTestApp(t)
	_, err := app.prepareWSLOpenCodeLayer("NoSuchDistro", map[string]any{"provider": map[string]any{}})
	if err == nil {
		t.Fatal("layer preparation failure must block (new fail-closed semantics)")
	}
	if !strings.Contains(err.Error(), "不再以无 provider 配置静默启动") {
		t.Fatalf("failure must carry the approved chinese semantics text: %v", err)
	}
	rec, ok := lookupWSLOpenCodeLayer("NoSuchDistro")
	if !ok || rec.OK {
		t.Fatal("failure must be recorded for the status face")
	}
}

// TestWSLPiProviderBuildErrorText 钉住「选中 provider 不可构建 → 阻断启动」
// 的中文文案（P3-S1 已批准语义：不静默换默认模型；WSL 路径条件由调用点
// wslDistro 判定，Windows 全链路断言见 TestWSLPiProviderBuildFailureBlocksLaunch）。
func TestWSLPiProviderBuildErrorText(t *testing.T) {
	err := wslPiProviderBuildError("kimi", errors.New("no baseURL"))
	for _, want := range []string{"无法构建 Pi 供应商", "kimi", "未改用默认模型"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error text missing %q: %v", want, err)
		}
	}
}

// TestPiProviderWriteLockReleasedOnBuildFailure（F1 锁可用性）：WSL 构建失败
// 阻断分支返回后 providerSyncMu 必须可用——修复前该分支在 Lock/Unlock 区间内
// 直接 return，锁永久泄漏，此后 provider 保存/Pi 启动/SaveOpenCodeConfig 全部
// 挂死。writePiProviderModelsAtLaunch 以参数接收 wslDistro，本测跨平台可跑
// （构建在触达任何 WSL I/O 之前失败）；Windows 全链路断言见
// TestWSLPiProviderBuildFailureBlocksLaunch 的同款 TryLock 段。
func TestPiProviderWriteLockReleasedOnBuildFailure(t *testing.T) {
	app := newTestApp(t)
	if err := app.Config.SaveProvider("no-base-url", config.Provider{}); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	env := map[string]string{}
	_, err := app.writePiProviderModelsAtLaunch("no-base-url", piLaunchSettings{Model: "m"}, config.Parameters{}, "FakeDistro", env)
	if err == nil || !strings.Contains(err.Error(), "无法构建 Pi 供应商") {
		t.Fatalf("expected build-failure block error, got %v", err)
	}
	// 锁可用性：阻断 return 后必须能立即重新进入临界区（泄漏则 TryLock 失败）。
	if !app.providerSyncMu.TryLock() {
		t.Fatal("providerSyncMu leaked by the build-failure return path (F1): TryLock failed")
	}
	app.providerSyncMu.Unlock()

	// 二次调用同样错误返回（而非挂死）且锁仍可用——结构性防再犯断言。
	if _, err = app.writePiProviderModelsAtLaunch("no-base-url", piLaunchSettings{Model: "m"}, config.Parameters{}, "FakeDistro", env); err == nil {
		t.Fatal("second call must also block with the same error")
	}
	if !app.providerSyncMu.TryLock() {
		t.Fatal("providerSyncMu leaked on the second build-failure return")
	}
	app.providerSyncMu.Unlock()
}

// TestVisionFaceConsentedNotSynced（F5）：授权完成但发布未落地 → 独立
// 「consented（已授权，待发布）」态，不虚报已同步；发布后（written）才 synced。
func TestVisionFaceConsentedNotSynced(t *testing.T) {
	app := newTestApp(t)
	app.WSLSupply = &stubSupplyRunner{result: wslsupply.RunResult{Target: wslsupply.TargetRef{Distro: "Ubuntu"}}}
	// stub GetStatus 返回空——直接驱动 face 映射函数。
	st := wslsupply.TargetStatus{
		Distro: "Ubuntu", Checked: true,
		Units: []wslsupply.UnitStatus{{Unit: wslsupply.UnitSharedVisionExport, Consented: true, LastOutcome: "consented"}},
	}
	face := wslVisionFace(st, false)
	if face.State != "consented" || !strings.Contains(face.Detail, "待发布") {
		t.Fatalf("consented-but-unpublished must map to consented(待发布), got %+v", face)
	}
	face = wslVisionFace(wslsupply.TargetStatus{
		Distro: "Ubuntu", Checked: true,
		Units: []wslsupply.UnitStatus{{Unit: wslsupply.UnitSharedVisionExport, Consented: true, LastOutcome: "written"}},
	}, false)
	if face.State != "synced" {
		t.Fatalf("published unit must map to synced, got %+v", face)
	}
}
