package main

// wsl_s2_remote_test.go — P3-S2 remote 计划接入与双链路验收：
//   - remote pi API-provider WSL 计划携带 EffectWSLConfigSupply（托管面 + 三个
//     键级配置单元），不再对 Windows models.json 做 ConfigMutation（Windows
//     文件由 providerSync 拥有）；
//   - remote OpenCode WSL 计划携带附加层供给 effect（层内容经 SecretBuffer，
//     顺序在 PTY 之前）；
//   - 双链路：桌面启动闸门与 remote effect 对同一目标产生同一结果（S2 验收 5）；
//   - 附加层 effect：层准备失败阻断、成功后 OPENCODE_CONFIG 重锚进 PTY env
//     并追加 WSLENV 转发清单（与桌面 prepareWSLOpenCodeLayer 同一函数）。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/settings"
	"amagi-codebox/internal/wslsupply"
)

// ---------------------------------------------------------------------------
// remote 计划：pi API-provider WSL
// ---------------------------------------------------------------------------

func TestRemotePiAPIProviderWSLPlanCarriesSupply(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatal(err)
	}
	secSvc := newTestSecretsService(t, dir)
	if err := cfgSvc.SaveProvider("prov", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.example/v1"},
		DefaultModel: "m1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := secSvc.SetAPIKey("prov", "sk-test-1234567890"); err != nil {
		t.Fatal(err)
	}
	sett := settings.NewService(dir)
	if err := sett.Load(); err != nil {
		t.Fatal(err)
	}
	defaults, err := launchplan.NewDefaultStore(sett)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &piLoginWSLShellResolver{}
	planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, resolver, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	refs := &launchplan.StableLaunchRefs{ProviderRef: "prov", ModelRef: "m1"}
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: "pi", Origin: launchplan.OriginDesktop, Mode: launchplan.ModeEmbedded, StableRefs: refs,
	})
	if failure != nil {
		t.Fatalf("WSL pi plan failed: %#v", failure)
	}
	defer plan.Secrets.Dispose()

	supplyIdx, ptyIdx, configIdx := -1, -1, -1
	for i, eff := range plan.Effects {
		switch eff.Kind {
		case launchplan.EffectWSLConfigSupply:
			supplyIdx = i
			spec := eff.WSLSupply
			if spec == nil || spec.CLI != string(wslsupply.CLIPi) {
				t.Fatalf("supply spec wrong: %+v", spec)
			}
			want := []string{
				string(wslsupply.UnitPiManagedModels),
				string(wslsupply.UnitPiSettings), string(wslsupply.UnitPiAmagi), string(wslsupply.UnitPiMCP),
			}
			if strings.Join(spec.Units, ",") != strings.Join(want, ",") {
				t.Fatalf("supply units = %v, want %v", spec.Units, want)
			}
			if spec.Distro != "" {
				t.Fatalf("supply distro must stay empty (Apply-time resolution): %q", spec.Distro)
			}
		case launchplan.EffectPTYStart:
			ptyIdx = i
		case launchplan.EffectConfigMutation:
			configIdx = i
		}
	}
	if supplyIdx < 0 || ptyIdx < 0 || supplyIdx > ptyIdx {
		t.Fatalf("supply effect must precede PTY: supply=%d pty=%d", supplyIdx, ptyIdx)
	}
	if configIdx >= 0 {
		t.Fatalf("WSL pi plan must not mutate Windows models.json at launch (providerSync owns it): effect %d", configIdx)
	}

	// 对照：非 WSL 计划仍走 ConfigMutation（既有语义不变）。
	plainPlanner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	plainPlan, failure := plainPlanner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: "pi", Origin: launchplan.OriginDesktop, Mode: launchplan.ModeEmbedded, StableRefs: refs,
	})
	if failure != nil {
		t.Fatalf("plain pi plan failed: %#v", failure)
	}
	defer plainPlan.Secrets.Dispose()
	hasMutation := false
	for _, eff := range plainPlan.Effects {
		if eff.Kind == launchplan.EffectConfigMutation {
			hasMutation = true
		}
	}
	if !hasMutation {
		t.Fatalf("non-WSL pi plan must keep ConfigMutation")
	}
}

// ---------------------------------------------------------------------------
// remote 计划：OpenCode WSL 附加层
// ---------------------------------------------------------------------------

func TestRemoteOpenCodeWSLPlanCarriesLayer(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatal(err)
	}
	secSvc := newTestSecretsService(t, dir)
	if err := cfgSvc.SaveProvider("ocp", config.Provider{
		OpenAI:       &config.OpenAIFormat{Enabled: true, BaseURL: "https://oc.example/v1"},
		DefaultModel: "om",
	}); err != nil {
		t.Fatal(err)
	}
	if err := secSvc.SetAPIKey("ocp", "sk-oc-1234567890"); err != nil {
		t.Fatal(err)
	}
	if err := cfgSvc.SaveOpenCodePreset("daily", config.OpenCodePreset{
		Name:        "daily",
		Description: "test preset",
		Config:      json.RawMessage(`{"model": "ocp/om"}`),
		Bindings: map[string]config.OpenCodeBinding{
			"ocp": {LocalProvider: "ocp", Format: "openai", Inject: []string{"apiKey", "baseURL"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	sett := settings.NewService(dir)
	if err := sett.Load(); err != nil {
		t.Fatal(err)
	}
	defaults, err := launchplan.NewDefaultStore(sett)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &piLoginWSLShellResolver{}
	planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, resolver, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: "opencode", Origin: launchplan.OriginDesktop, Mode: launchplan.ModeEmbedded,
		StableRefs: &launchplan.StableLaunchRefs{PresetRef: "daily"},
	})
	if failure != nil {
		t.Fatalf("WSL opencode plan failed: %#v", failure)
	}
	defer plan.Secrets.Dispose()

	supplyIdx, ptyIdx := -1, -1
	for i, eff := range plan.Effects {
		switch eff.Kind {
		case launchplan.EffectWSLConfigSupply:
			supplyIdx = i
			spec := eff.WSLSupply
			if spec == nil || spec.OpenCodeLayer == nil {
				t.Fatalf("supply spec must carry opencode layer: %+v", spec)
			}
			if len(spec.Units) != 0 {
				t.Fatalf("layer-only supply must carry no units: %v", spec.Units)
			}
			content, ok := plan.Secrets.Buffer(spec.OpenCodeLayer.Content)
			if !ok || !strings.Contains(string(content), "https://oc.example/v1") {
				t.Fatalf("layer content must come from the secret buffer with provider config")
			}
		case launchplan.EffectPTYStart:
			ptyIdx = i
		}
	}
	if supplyIdx < 0 || ptyIdx < 0 || supplyIdx > ptyIdx {
		t.Fatalf("layer effect must precede PTY: layer=%d pty=%d", supplyIdx, ptyIdx)
	}
}

// ---------------------------------------------------------------------------
// 双链路：桌面闸门 vs remote effect（同一协调器、同一目标）
// ---------------------------------------------------------------------------

// fakePiConfigSourceForDualLink 是双链路测试的 PiConfigSource 桩。
type fakePiConfigSourceForDualLink struct {
	amagi []byte
}

func (s fakePiConfigSourceForDualLink) SettingsJSON() ([]byte, error) { return nil, nil }
func (s fakePiConfigSourceForDualLink) AmagiJSON() ([]byte, error)    { return s.amagi, nil }
func (s fakePiConfigSourceForDualLink) MCPJSON() ([]byte, error)      { return nil, nil }
func (s fakePiConfigSourceForDualLink) KnownProviderIDs() ([]string, error) {
	return []string{"amagi-glm"}, nil
}

// stubFactsForDualLink 固定事实：npx 已验证。
type stubFactsForDualLink struct{}

func (stubFactsForDualLink) TransformFacts(_ context.Context, _ wslsupply.TargetRef) (wslsupply.TransformFacts, error) {
	return wslsupply.TransformFacts{ResolvedCommands: map[string]string{"npx": "/usr/bin/npx"}}, nil
}

func TestWSLPiConfigSupplyDualLinkDesktopVsRemote(t *testing.T) {
	// 同一目标（假探测面）：先经桌面闸门运行一轮建立登记，再清空目标重演——
	// 桌面路径与 remote effect 路径必须产出逐字节相同的结果。
	buildCoord := func(t *testing.T) *wslsupply.Coordinator {
		c := fakeSupplyCoordinatorWithUnit(t)
		c.RegisterProjection(wslsupply.NewPiManagedModelsAdapter(stubPiManagedSourceForTest{
			spec: wslsupply.PiManagedModelsSpec{Providers: map[string]any{"providers": map[string]any{
				"amagi-x": map[string]any{"baseUrl": "https://x.example"},
			}}},
		}))
		src := fakePiConfigSourceForDualLink{amagi: []byte(`{"profile": "tiered"}`)}
		c.RegisterProjection(wslsupply.NewPiAmagiAdapter(src, stubFactsForDualLink{}))
		// F-3：键级单元需 per-target 同意（双链路在同意后的投影结果上比对）。
		if err := c.GrantUnitConsent(context.Background(), "Ubuntu", wslsupply.CLIPi, wslsupply.UnitPiAmagi); err != nil {
			t.Fatalf("grant consent: %v", err)
		}
		return c
	}

	runDesktop := func(t *testing.T) string {
		app := newTestApp(t)
		app.WSLSupply = buildCoord(t)
		if err := app.runWSLManagedModelsAtLaunch("Ubuntu"); err != nil {
			t.Fatalf("desktop gate: %v", err)
		}
		return dualLinkTargetBytes(t, app.WSLSupply)
	}
	runRemote := func(t *testing.T) string {
		coord := buildCoord(t)
		effect := &wslConfigSupplyEffect{
			deps: launchExecutorDeps{wslSupply: coord}, distro: "Ubuntu",
			cli: wslsupply.CLIPi, reason: wslsupply.ReasonLaunch,
			units: []wslsupply.UnitID{wslsupply.UnitPiManagedModels, wslsupply.UnitPiAmagi},
		}
		if _, err := effect.Apply(context.Background()); err != nil {
			t.Fatalf("remote effect: %v", err)
		}
		return dualLinkTargetBytes(t, coord)
	}

	desktop := runDesktop(t)
	remote := runRemote(t)
	if len(desktop) == 0 {
		t.Fatal("desktop run produced no target files")
	}
	if desktop != remote {
		t.Fatalf("dual-link divergence:\n--- desktop ---\n%s\n--- remote ---\n%s", desktop, remote)
	}
}

// dualLinkTargetBytes 汇总目标根下全部投影文件的字节（确定性排序）。
func dualLinkTargetBytes(t *testing.T, runner wslsupply.Runner) string {
	t.Helper()
	coord, ok := runner.(*wslsupply.Coordinator)
	if !ok {
		t.Fatal("runner is not a coordinator")
	}
	target, err := coord.ResolveTarget(context.Background(), "Ubuntu", wslsupply.CLIPi)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.Walk(target.UNCRoot, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, f := range files {
		data, _ := os.ReadFile(f)
		rel := strings.TrimPrefix(f, target.UNCRoot)
		sb.WriteString(rel + "\n" + string(data) + "\x00")
	}
	return sb.String()
}

// 双链路 · 闸门一致性：corrupt 阻断 / 其余告警，两条链路判定相同。
func TestWSLPiConfigSupplyGateConsistencyBothLinks(t *testing.T) {
	corruptResult := wslsupply.RunResult{Target: platformTargetRef("Ubuntu"), Results: []wslsupply.UnitResult{{
		Unit: wslsupply.UnitPiManagedModels, Outcome: wslsupply.UnitFailed, Detail: "target-corrupt",
		Err: errors.New("corrupt"),
	}}}
	// 桌面闸门。
	app := newTestApp(t)
	desktopErr := app.gateWSLPiConfigSupplyResult(corruptResult, nil, "Ubuntu")
	if desktopErr == nil || !strings.Contains(desktopErr.Error(), "已损坏") {
		t.Fatalf("desktop corrupt gate must block, got %v", desktopErr)
	}
	// remote effect 判定（同一纯 verdict）。
	launchErr, warnMsg, _ := verdictForWSLPiConfigSupply(corruptResult, nil)
	if launchErr == nil || !strings.Contains(launchErr.Error(), "已损坏") {
		t.Fatalf("remote corrupt verdict must block, got %v / warn %q", launchErr, warnMsg)
	}
	if warnMsg != "" {
		t.Fatalf("corrupt must not also warn: %q", warnMsg)
	}
	// 键级单元冻结 → 两条链路都只告警放行。
	frozenResult := wslsupply.RunResult{Target: platformTargetRef("Ubuntu"), Results: []wslsupply.UnitResult{{
		Unit: wslsupply.UnitPiAmagi, Outcome: wslsupply.UnitBlocked,
		Detail: "both-changed-frozen; 键 profile：双侧均有修改",
	}}}
	if err := app.gateWSLPiConfigSupplyResult(frozenResult, nil, "Ubuntu"); err != nil {
		t.Fatalf("frozen keyed unit must not block desktop: %v", err)
	}
	// 冻结属预期态：两条链路一致地静默放行（状态页可见，不刷启动告警）。
	launchErr, warnMsg, _ = verdictForWSLPiConfigSupply(frozenResult, nil)
	if launchErr != nil || warnMsg != "" {
		t.Fatalf("frozen keyed unit must pass silently on remote (same as desktop): %v %q", launchErr, warnMsg)
	}
}

func platformTargetRef(distro string) wslsupply.TargetRef {
	return wslsupply.TargetRef{Distro: distro, CLI: wslsupply.CLIPi}
}

// ---------------------------------------------------------------------------
// OpenCode 层 effect：失败阻断 / 成功重锚 PTY env
// ---------------------------------------------------------------------------

func TestOpenCodeLayerEffectAnchorsPTYEnv(t *testing.T) {
	layerCfg := map[string]any{"provider": map[string]any{"x": map[string]any{"apiKey": "sk"}}}
	raw, _ := json.Marshal(layerCfg)
	bundle := launchplan.NewEphemeralSecretBundle(raw)
	defer bundle.Dispose()
	content, ok := bundle.Buffer(launchplan.SecretBufferRef{Index: 0})
	if !ok {
		t.Fatal("secret buffer missing")
	}
	spec := platform.ResolvedLaunchSpec{
		AppType: "opencode", BootstrapMode: platform.BootstrapWSL,
		Shell: &platform.ResolvedShell{Key: "wsl", Path: "wsl.exe"},
		Env:   platform.ResolvedEnv{Variables: []string{"OPENCODE_CONFIG_CONTENT=" + string(raw)}},
	}
	pty := &ptyStartEffect{deps: launchExecutorDeps{}, spec: spec}
	prep := func(_ context.Context, _ string, cfg map[string]any) (string, error) {
		if _, ok := cfg["provider"]; !ok {
			return "", errors.New("cfg lost provider section")
		}
		return "/home/u/.config/opencode/amagi-codebox.json", nil
	}
	effect := &wslConfigSupplyEffect{
		deps: launchExecutorDeps{}, distro: "Ubuntu",
		cli: "opencode", reason: wslsupply.ReasonLaunch,
		openCodeLayer: append([]byte(nil), content...), layerPrep: prep, ptyRef: &pty,
	}
	// 层失败 → effect 失败（启动阻断，与桌面 prepareWSLOpenCodeLayer 同语义）。
	pty2 := &ptyStartEffect{deps: launchExecutorDeps{}, spec: spec}
	failEffect := &wslConfigSupplyEffect{
		deps: launchExecutorDeps{}, distro: "Ubuntu",
		cli: "opencode", reason: wslsupply.ReasonLaunch,
		openCodeLayer: []byte("{not-json"), layerPrep: prep, ptyRef: &pty2,
	}
	if _, err := failEffect.Apply(context.Background()); err == nil {
		t.Fatal("invalid layer content must fail the effect")
	}
	// 层成功 → OPENCODE_CONFIG 注入 PTY env + WSLENV 转发清单追加。
	if _, err := effect.Apply(context.Background()); err != nil {
		t.Fatalf("layer apply: %v", err)
	}
	vars := pty.spec.Env.Variables
	found := ""
	for _, kv := range vars {
		if strings.HasPrefix(kv, "OPENCODE_CONFIG=") {
			found = strings.TrimPrefix(kv, "OPENCODE_CONFIG=")
		}
	}
	if found != "/home/u/.config/opencode/amagi-codebox.json" {
		t.Fatalf("OPENCODE_CONFIG not anchored into PTY env: %v", vars)
	}
	wslenv := ""
	for _, kv := range vars {
		if strings.HasPrefix(strings.ToUpper(kv), "WSLENV=") {
			wslenv = strings.TrimPrefix(kv[len("WSLENV="):], "")
		}
	}
	if !strings.Contains(wslenv, "OPENCODE_CONFIG") {
		t.Fatalf("OPENCODE_CONFIG missing from WSLENV forwarding list: %q", wslenv)
	}
	// 层必须在 PTY 之前：已启动的 PTY 上层注入被拒绝。
	pty3 := &ptyStartEffect{deps: launchExecutorDeps{}, spec: spec, applied: true}
	lateEffect := &wslConfigSupplyEffect{
		deps: launchExecutorDeps{}, distro: "Ubuntu",
		cli: "opencode", reason: wslsupply.ReasonLaunch,
		openCodeLayer: content, layerPrep: prep, ptyRef: &pty3,
	}
	if _, err := lateEffect.Apply(context.Background()); err == nil {
		t.Fatal("layer after PTY start must be rejected")
	}
}

// ---------------------------------------------------------------------------
// 面板保存 → pi-config 标脏（异步推送/启动补漏的触发源）
// ---------------------------------------------------------------------------

func TestSaveAmagiConfigMarksPiConfigDirty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	app := newTestApp(t)
	runner := &stubSupplyRunner{}
	app.WSLSupply = runner
	if err := app.SaveAmagiConfig(`{"profile": "tiered"}`); err != nil {
		t.Fatalf("SaveAmagiConfig: %v", err)
	}
	dirty := runner.dirty_()
	if len(dirty) != 1 || dirty[0] != wslsupply.DirtySourcePiConfig {
		t.Fatalf("pi-config dirty source must be marked once, got %v", dirty)
	}
	// 保存失败不标脏（宿主保存与 WSL 同步是两个结果）。
	if err := app.SaveAmagiConfig(`{invalid-json`); err == nil {
		t.Fatal("expected save failure on invalid content")
	}
	if got := runner.dirty_(); len(got) != 1 {
		t.Fatalf("failed save must not mark dirty, got %v", got)
	}
}

// EnableWSLPiConfigSupply（F-3 同意入口）：未接线拒绝；已接线对三单元授权并
// 触发一次异步推送。
func TestEnableWSLPiConfigSupply(t *testing.T) {
	app := newTestApp(t)
	app.WSLSupply = &stubSupplyRunner{} // 无 GrantUnitConsent 能力
	if err := app.EnableWSLPiConfigSupply(); err == nil {
		t.Fatal("incapable runner must reject enable")
	}

	c := fakeSupplyCoordinatorWithUnit(t)
	src := fakePiConfigSourceForDualLink{amagi: []byte(`{"profile": "tiered"}`)}
	c.RegisterProjection(wslsupply.NewPiSettingsAdapter(src, stubFactsForDualLink{}))
	c.RegisterProjection(wslsupply.NewPiAmagiAdapter(src, stubFactsForDualLink{}))
	c.RegisterProjection(wslsupply.NewPiMCPAdapter(src, stubFactsForDualLink{}))
	app.WSLSupply = c
	origDefault := wslDefaultDistroFn
	t.Cleanup(func() { wslDefaultDistroFn = origDefault })
	wslDefaultDistroFn = func([]string) string { return "Ubuntu" }
	if err := app.EnableWSLPiConfigSupply(); err != nil {
		t.Fatalf("enable with capable coordinator: %v", err)
	}
	st := c.GetStatus("Ubuntu", wslsupply.CLIPi)
	for _, u := range st.Units {
		if u.Unit == wslsupply.UnitPiAmagi && !u.Consented {
			t.Fatalf("pi/amagi must be consented after enable")
		}
	}
}
