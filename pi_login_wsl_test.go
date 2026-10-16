package main

// pi_login_wsl_test.go — A2 闸门解除（主上裁定 2026-10-16）+ P3-S0 统一执行
// 边界的回归：WSL spec 下的登录计划放行（供给 effect 在 PTY 之前 + 载荷只剩
// env -u 重锚，cmp/cp 同步片段退役）、桌面闸门与 remote effect 共用同一
// A2 故障矩阵（wslsupply 协调器真身 + A2 适配器接缝注入）、PI_CODING_AGENT_DIR
// 剥离语义不变。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/wslsupply"
)

// piLoginWSLShellResolver 固定产出 BootstrapWSL spec（resolver 的 WSL 分支
// 形态：CLI 裸名 + Args、Shell key=wsl、StartupCommand 空），其余复用
// fakeCLIResolver。跨平台可用——不依赖真实 WSL。
type piLoginWSLShellResolver struct{ request platform.ResolveRequest }

func (r *piLoginWSLShellResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	r.request = req
	spec, err := fakeCLIResolver{}.Resolve(req)
	if err != nil {
		return spec, err
	}
	spec.Shell = &platform.ResolvedShell{Key: "wsl", Path: "wsl.exe"}
	spec.BootstrapMode = platform.BootstrapWSL
	spec.CLI.Path = "pi"
	spec.StartupCommand = ""
	return spec, nil
}

func (*piLoginWSLShellResolver) ResolveExecutable(cmd string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return fakeCLIResolver{}.ResolveExecutable(cmd, args, env)
}

// TestPiLoginPlannerWSLGateLifted：WSL spec 的登录计划不再 fail-closed——
// env 剥离 PI_CODING_AGENT_DIR（Windows 路径不跨界），StartupCommand 只剩
// env -u 前缀 + 单引号 CLI + exec bash -li（P3-S0：cmp/cp 同步片段退役，凭据
// 字节永不进入命令行），并在 PTY 之前携带 EffectWSLConfigSupply（计划只带
// 待供给描述：distro 留空、Apply 时解析，BuildPlan 保持纯读零 spawn）。
func TestPiLoginPlannerWSLGateLifted(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()
	cfg := config.NewConfigService(dir)
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	preset, key, _ := piLoginFixture(t, cfg)
	sett := settings.NewService(dir)
	if err := sett.Load(); err != nil {
		t.Fatal(err)
	}
	defaults, err := launchplan.NewDefaultStore(sett)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &piLoginWSLShellResolver{}
	planner := newAppLaunchPlanner(cfg, newTestSecretsService(t, dir), defaults, resolver, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		StableRefs: &launchplan.StableLaunchRefs{ProviderRef: config.PiLoginProviderID(preset.Provider), ModelRef: key},
	})
	if failure != nil {
		t.Fatalf("WSL login plan failed (gate not lifted?): %#v", failure)
	}
	defer plan.Secrets.Dispose()
	if plan.Recipe.ProviderRef != config.PiLoginProviderID(preset.Provider) || plan.Recipe.ModelRef != key {
		t.Fatalf("WSL login recipe lost: %+v", plan.Recipe)
	}

	// 供给 effect：在 PTY 之前、只带描述（无候选正文、无 distro 探测结果）。
	supplyIdx, ptyIdx := -1, -1
	for i, eff := range plan.Effects {
		if eff.Kind == launchplan.EffectWSLConfigSupply {
			supplyIdx = i
			if eff.WSLSupply == nil {
				t.Fatal("supply effect missing spec")
			}
			spec := eff.WSLSupply
			if spec.CLI != string(wslsupply.CLIPi) {
				t.Fatalf("supply cli = %q, want pi", spec.CLI)
			}
			if spec.Distro != "" {
				t.Fatalf("supply distro must stay empty for Apply-time resolution (BuildPlan purity): %q", spec.Distro)
			}
			if len(spec.Units) != 1 || spec.Units[0] != string(wslsupply.UnitPiA2Auth) {
				t.Fatalf("supply units = %v, want [pi/a2-auth]", spec.Units)
			}
		}
		if eff.Kind == launchplan.EffectPTYStart {
			ptyIdx = i
		}
	}
	if supplyIdx < 0 {
		t.Fatal("WSL login plan missing EffectWSLConfigSupply")
	}
	if ptyIdx < 0 || supplyIdx > ptyIdx {
		t.Fatalf("supply effect must precede PTY: supply=%d pty=%d", supplyIdx, ptyIdx)
	}

	spec := plan.Effects[ptyIdx].Process.Resolved
	if spec.BootstrapMode != platform.BootstrapWSL {
		t.Fatalf("bootstrap mode mutated: %q", spec.BootstrapMode)
	}
	// R2 分叉（WSL 侧）：Windows 路径钉定被剥离，不进 WSLENV 转发面。
	envMap := map[string]string{}
	for _, kv := range spec.Env.Variables {
		k, v, _ := strings.Cut(kv, "=")
		envMap[k] = v
	}
	if _, ok := envMap["PI_CODING_AGENT_DIR"]; ok {
		t.Fatalf("PI_CODING_AGENT_DIR leaked into WSL spec env: %q", envMap["PI_CODING_AGENT_DIR"])
	}
	if wslenv := envMap["WSLENV"]; wslenv != "" && strings.Contains(strings.ToUpper(wslenv), "PI_CODING_AGENT_DIR") {
		t.Fatalf("PI_CODING_AGENT_DIR still in WSLENV list: %q", wslenv)
	}
	// 载荷（P3-S0 退役同步片段后）：只剩 env -u 前缀（凭据键 + agent dir）→
	// 单引号 CLI 与参数 → exec bash -li；不得再含 cmp/cp/stat/chmod 同步逻辑
	// 与任何 auth.json 路径字节。
	cmd := spec.StartupCommand
	for _, banned := range []string{"cmp -s", "cp --", "stat -c %Y", "chmod 600", "auth.json", "mkdir -p"} {
		if strings.Contains(cmd, banned) {
			t.Fatalf("retired auth-sync machinery leaked into payload (%q): %s", banned, cmd)
		}
	}
	envPrefix := "env"
	for _, k := range piLoginCredentialEnvKeys {
		envPrefix += " -u " + k
	}
	envPrefix += " -u PI_CODING_AGENT_DIR"
	if !strings.Contains(cmd, envPrefix+" 'pi' '--provider' 'openai-codex' '--model' 'native-model' '--thinking' 'max'") {
		t.Fatalf("payload missing re-anchor prefix + quoted cli: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "; exec bash -li") {
		t.Fatalf("payload missing exec bash tail: %s", cmd)
	}
}

// TestPiLoginWSLPayloadSkipsAgentDirPin：非 WSL spec 的载荷语义不变
// （不携带 env -u PI_CODING_AGENT_DIR 重锚）；纯终端 WSL spec 无载荷。
func TestPiLoginWSLPayloadSkipsAgentDirPin(t *testing.T) {
	home := isolatedHome(t)
	canonical := filepath.Join(home, ".pi", "agent")
	spec := piLoginWSLReAnchorSpec(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapShellAttach,
		Shell:         &platform.ResolvedShell{Key: "bash", Path: "/bin/bash"},
		CLI:           platform.ResolvedCLI{Path: "pi", Args: []string{"--provider", "x"}},
	}, canonical)
	if strings.Contains(spec.StartupCommand, "env -u PI_CODING_AGENT_DIR") {
		t.Fatalf("non-WSL spec got WSL payload: %s", spec.StartupCommand)
	}
	// WSL spec 但 CLI 为空（纯终端）：无锚定面，载荷不变。
	terminal := piLoginWSLReAnchorSpec(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		Env:           platform.ResolvedEnv{Variables: []string{"PI_CODING_AGENT_DIR=" + canonical, "WSLENV=PI_CODING_AGENT_DIR/u:PATH"}},
	}, canonical)
	if terminal.StartupCommand != "" {
		t.Fatalf("pure terminal WSL spec must not gain a payload: %q", terminal.StartupCommand)
	}
	if hasEnvEntryForTest(terminal.Env.Variables, "PI_CODING_AGENT_DIR") {
		t.Fatalf("agent dir not stripped for pure terminal WSL spec: %v", terminal.Env.Variables)
	}
	if wslenv := envValue(terminal.Env.Variables, "WSLENV"); wslenv != "PATH" {
		t.Fatalf("WSLENV not pruned: %q", wslenv)
	}
}

func hasEnvEntryForTest(env []string, key string) bool {
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// newFakeSupplyCoordinator 构造带假探测面的真协调器（身份/UNC/Chmod 全注入，
// UNC 落本地 temp dir；A2 适配器由用例注入）。跨平台可跑，不触达真实 WSL。
func newFakeSupplyCoordinator(t *testing.T, a2 wslsupply.A2Adapter) *wslsupply.Coordinator {
	t.Helper()
	uncRoot := t.TempDir()
	c := wslsupply.NewCoordinator(t.TempDir(), nil, wslsupply.Probes{
		Identity: func(string) platform.WSLUserIdentity {
			return platform.WSLUserIdentity{UID: "1000", Home: "/home/u", MachineID: "test-machine-id", OK: true}
		},
		ToUNC: func(_, linuxPath string) string {
			return filepath.Join(uncRoot, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
		},
		Chmod: func(_, _ string, _ ...string) error { return nil },
	})
	if a2 != nil {
		c.SetA2Adapter(a2)
	}
	return c
}

// stubA2Adapter 记录 Sync 调用并返回固定结果（A2 故障矩阵注入点）。
type stubA2Adapter struct {
	calls   *[]string
	outcome launcher.SyncWSLPiLoginAuthOutcome
	err     error
}

func (s stubA2Adapter) Files() []string { return []string{"auth.json"} }

func (s stubA2Adapter) Sync(distro string) (launcher.SyncWSLPiLoginAuthOutcome, error) {
	*s.calls = append(*s.calls, distro)
	return s.outcome, s.err
}

// TestWSLLoginSupplyFailureMatrixBothLinks：A2 故障矩阵（设计 §八 S0 验收 3）
// ——同一组用例经两条链路各跑一遍，结果必须一致：
//   - 桌面链路：App.syncWSLPiLoginAuthAtLaunch（runWSLLoginSupplyFn 生产默认
//     实现 → 协调器 RunUnits）；
//   - remote 链路：launchplan EffectWSLConfigSupply → 执行器 Apply → 同一
//     协调器 RunUnits。
//
// A2 判定语义零变化：矩阵注入在 A2 适配器接缝（生产实现就是
// launcher.SyncWSLPiLoginAuth，其算法矩阵由 launcher 包单测钉住）。
func TestWSLLoginSupplyFailureMatrixBothLinks(t *testing.T) {
	cases := []struct {
		name      string
		outcome   launcher.SyncWSLPiLoginAuthOutcome
		err       error
		wantBlock bool // true → 两链路都必须拒绝启动
		wantText  string
	}{
		{"both sides identical fresh", launcher.SyncWSLPiLoginAuthFresh, nil, false, ""},
		{"host newer copied", launcher.SyncWSLPiLoginAuthCopied, nil, false, ""},
		{"distro newer reverse synced", launcher.SyncWSLPiLoginAuthDistroNewer, nil, false, ""},
		{"mtime tie keeps distro", launcher.SyncWSLPiLoginAuthDistroTieKept, nil, false, ""},
		{"probe unavailable degrades with existing copy", launcher.SyncWSLPiLoginAuthDistroHasCopy, errors.New("probe distro pi auth.json mtime: unavailable"), false, ""},
		{"write failure with old copy degrades", launcher.SyncWSLPiLoginAuthDistroHasCopy, errors.New("UNC unreachable"), false, ""},
		{"logout source missing fails closed", launcher.SyncWSLPiLoginAuthDistroNoAuth, launcher.ErrWSLPiLoginSourceMissing, true, "Pi 登录已失效"},
		{"no auth either side fails closed", launcher.SyncWSLPiLoginAuthDistroNoAuth, errors.New("chmod failed"), true, "WSL 内没有可用的 Pi 登录凭据"},
		{"chmod plus cleanup double failure fails closed", launcher.SyncWSLPiLoginAuthDistroNoAuth, errors.New("chmod 600 wsl pi auth.json: boom (cleanup failed: boom2)"), true, "WSL 内没有可用的 Pi 登录凭据"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desktopErr := func() error {
				calls := &[]string{}
				coord := newFakeSupplyCoordinator(t, stubA2Adapter{calls: calls, outcome: tc.outcome, err: tc.err})
				app := newTestApp(t)
				app.WSLSupply = coord
				err := app.syncWSLPiLoginAuthAtLaunch("Ubuntu")
				if len(*calls) != 1 {
					t.Fatalf("a2 adapter consulted %d times, want 1", len(*calls))
				}
				return err
			}()
			remoteErr := func() error {
				calls := &[]string{}
				coord := newFakeSupplyCoordinator(t, stubA2Adapter{calls: calls, outcome: tc.outcome, err: tc.err})
				exec := newAppLaunchExecutor(launchExecutorDeps{wslSupply: coord})
				plan := &launchplan.Plan{Recipe: launchplan.StableRecipe{CLIType: contract.CLITypePi, Workdir: "C:\\w"},
					Effects: []launchplan.EffectSpec{{Kind: launchplan.EffectWSLConfigSupply,
						WSLSupply: &launchplan.WSLConfigSupplySpec{Distro: "Ubuntu", CLI: string(wslsupply.CLIPi), Reason: string(wslsupply.ReasonLaunch), Units: []string{string(wslsupply.UnitPiA2Auth)}}}}}
				prepared, err := exec.Prepare(context.Background(), plan, launchplan.ExecutionBinding{SessionID: "wsl-supply-matrix", RunEpoch: 1})
				if err != nil {
					t.Fatalf("prepare: %v", err)
				}
				effect := prepared.Effect(0)
				effect.ArmOwnership()
				_, applyErr := effect.Apply(context.Background())
				if len(*calls) != 1 {
					t.Fatalf("a2 adapter consulted %d times, want 1", len(*calls))
				}
				return applyErr
			}()
			if (desktopErr != nil) != tc.wantBlock {
				t.Fatalf("desktop gate err=%v, wantBlock=%v", desktopErr, tc.wantBlock)
			}
			if (remoteErr != nil) != tc.wantBlock {
				t.Fatalf("remote effect err=%v, wantBlock=%v", remoteErr, tc.wantBlock)
			}
			if tc.wantBlock {
				if desktopErr == nil || remoteErr == nil {
					t.Fatalf("both links must fail closed: desktop=%v remote=%v", desktopErr, remoteErr)
				}
				if !strings.Contains(desktopErr.Error(), tc.wantText) || !strings.Contains(remoteErr.Error(), tc.wantText) {
					t.Fatalf("fail-closed text mismatch (want %q): desktop=%v remote=%v", tc.wantText, desktopErr, remoteErr)
				}
			}
		})
	}
}

// TestWSLLoginSupplyDistroFallbackAndUnresolved：桌面闸门 distro 兜底重探 +
// 目标不可解析（无 WSL 机器）时 fail closed（中文文案），remote effect 同判。
func TestWSLLoginSupplyDistroFallbackAndUnresolved(t *testing.T) {
	app := newTestApp(t)
	// 不接 WSLSupply：runner 缺位 → fail closed（不静默放行无凭据会话）。
	err := app.syncWSLPiLoginAuthAtLaunch("")
	if err == nil || !strings.Contains(err.Error(), "WSL 内没有可用的 Pi 登录凭据") {
		t.Fatalf("expected fail-closed chinese error, got %v", err)
	}
	// 身份探测失败（协调器真身 + Identity 恒失败）→ 目标不可解析 → 同判。
	coord := wslsupply.NewCoordinator(t.TempDir(), nil, wslsupply.Probes{
		Identity: func(string) platform.WSLUserIdentity { return platform.WSLUserIdentity{} },
		ToUNC:    func(_, _ string) string { return "" },
		Chmod:    func(_, _ string, _ ...string) error { return nil },
	})
	app.WSLSupply = coord
	err = app.syncWSLPiLoginAuthAtLaunch("Ubuntu")
	if err == nil || !strings.Contains(err.Error(), "WSL 内没有可用的 Pi 登录凭据") {
		t.Fatalf("expected unresolved-target fail-closed, got %v", err)
	}
}

// TestPiLoginDesktopWSLGateSync：桌面闸门在 WSL 登录会话放行前调用统一供给，
// 并按 verdict 分叉：失败且 distro 无副本 → 中文报错拒绝；降级/成功 → 放行
// 到 PTY 层（后续 control 未接线自然失败，不触达 PTY spawn）。
func TestPiLoginDesktopWSLGateSync(t *testing.T) {
	stubRun := func(outcome launcher.SyncWSLPiLoginAuthOutcome, err error) *[]string {
		calls := &[]string{}
		runWSLLoginSupplyFn = func(_ context.Context, _ wslsupply.Runner, distro string) (wslsupply.RunResult, error) {
			*calls = append(*calls, distro)
			return wslsupply.RunResult{Target: wslsupply.TargetRef{Distro: "Ubuntu"}, Results: []wslsupply.UnitResult{{
				Unit: wslsupply.UnitPiA2Auth, Policy: wslsupply.PolicyA2Auth, Outcome: unitOutcomeForA2(outcome),
				A2: &wslsupply.A2Result{Outcome: outcome, Err: err},
			}}}, nil
		}
		t.Cleanup(func() { runWSLLoginSupplyFn = defaultRunWSLLoginSupply })
		return calls
	}
	launchWithFixture := func(t *testing.T) error {
		t.Helper()
		home := isolatedHome(t)
		app := newTestApp(t)
		app.ctx = t.Context()
		app.Capabilities = fakePlatformCaps()
		_, key, _ := piLoginFixture(t, app.Config)
		resolver := &piLoginWSLShellResolver{}
		app.CLIResolver = resolver
		_, err := app.LaunchPiSession(key, config.PiLoginProviderID("openai-codex"), "embedded", home, "")
		return err
	}

	t.Run("no auth both sides fails closed in chinese", func(t *testing.T) {
		calls := stubRun(launcher.SyncWSLPiLoginAuthDistroNoAuth, errors.New("chmod failed"))
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "WSL 内没有可用的 Pi 登录凭据") {
			t.Fatalf("expected chinese fail-closed error, got %v", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("supply not consulted once: %v", *calls)
		}
	})

	t.Run("source missing keeps login-invalid semantics", func(t *testing.T) {
		stubRun(launcher.SyncWSLPiLoginAuthDistroNoAuth, launcher.ErrWSLPiLoginSourceMissing)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "Pi 登录已失效") {
			t.Fatalf("expected login-invalid error, got %v", err)
		}
	})

	t.Run("stale distro copy degrades with warn", func(t *testing.T) {
		stubRun(launcher.SyncWSLPiLoginAuthDistroHasCopy, errors.New("UNC unreachable"))
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
	})

	t.Run("fresh copy passes gate", func(t *testing.T) {
		calls := stubRun(launcher.SyncWSLPiLoginAuthFresh, nil)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("supply not consulted once: %v", *calls)
		}
	})

	t.Run("distro newer reverse-sync passes gate", func(t *testing.T) {
		stubRun(launcher.SyncWSLPiLoginAuthDistroNewer, nil)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
	})

	t.Run("mtime tie kept passes gate", func(t *testing.T) {
		stubRun(launcher.SyncWSLPiLoginAuthDistroTieKept, nil)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
	})
}

// unitOutcomeForA2 把 launcher outcome 映射为协调器单元分类（与生产 classifyA2
// 同向；这里只构造闸门消费所需的最小结果面）。
func unitOutcomeForA2(outcome launcher.SyncWSLPiLoginAuthOutcome) wslsupply.UnitOutcome {
	switch outcome {
	case launcher.SyncWSLPiLoginAuthFresh:
		return wslsupply.UnitFresh
	case launcher.SyncWSLPiLoginAuthDistroHasCopy:
		return wslsupply.UnitWarnDegraded
	case launcher.SyncWSLPiLoginAuthDistroNoAuth:
		return wslsupply.UnitFailed
	default:
		return wslsupply.UnitApplied
	}
}

// TestPiLoginStripAgentDirForWSL：WSL spec 环境清理——键大小写不敏感、
// WSLENV 清单（含 /flags 形态）同步剪除、清单空则整体移除、无关变量不动。
func TestPiLoginStripAgentDirForWSL(t *testing.T) {
	env := []string{
		"PI_CODING_AGENT_DIR=" + `C:\Users\u\.pi\agent`,
		"pi_coding_agent_dir=dup",
		"WSLENV=PI_CODING_AGENT_DIR/u:PATH:pi_coding_agent_dir",
		"LANG=en_US.UTF-8",
	}
	got := piLoginStripAgentDirForWSL(env)
	want := []string{"WSLENV=PATH", "LANG=en_US.UTF-8"}
	if len(got) != len(want) {
		t.Fatalf("strip result = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("strip result = %v, want %v", got, want)
		}
	}
	if all := piLoginStripAgentDirForWSL([]string{"WSLENV=PI_CODING_AGENT_DIR/u"}); len(all) != 0 {
		t.Fatalf("empty WSLENV list must drop the variable entirely: %v", all)
	}
}
