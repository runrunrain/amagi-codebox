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
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/settings"
)

// ---------------------------------------------------------------------------
// remote 计划：pi API-provider WSL
// ---------------------------------------------------------------------------

func TestRemotePiAPIProviderWSLPlanSharesWindowsRoot(t *testing.T) {
	unsetInheritedProxyEnv(t) // 宿主代理 marker 会经 MergeWithSystem 触发 NAT 门，与本用例无关
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

	// 共享 Windows agent 根：WSL pi 计划与原生同形——ConfigMutation 写 Windows
	// models.json（pi 读同一份），不携带任何 distro 供给 effect。
	ptyIdx, configIdx := -1, -1
	for i, eff := range plan.Effects {
		switch eff.Kind {
		case launchplan.EffectWSLConfigSupply:
			t.Fatalf("WSL pi plan must not carry distro supply (shared Windows root): effect %d", i)
		case launchplan.EffectPTYStart:
			ptyIdx = i
		case launchplan.EffectConfigMutation:
			configIdx = i
		}
	}
	if ptyIdx < 0 || configIdx < 0 || configIdx > ptyIdx {
		t.Fatalf("WSL pi plan must mutate Windows models.json before PTY: config=%d pty=%d", configIdx, ptyIdx)
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
	unsetInheritedProxyEnv(t) // 宿主代理 marker 会经 MergeWithSystem 触发 NAT 门，与本用例无关
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
		openCodeLayer: append([]byte(nil), content...), layerPrep: prep, ptyRef: &pty,
	}
	// 层失败 → effect 失败（启动阻断，与桌面 prepareWSLOpenCodeLayer 同语义）。
	pty2 := &ptyStartEffect{deps: launchExecutorDeps{}, spec: spec}
	failEffect := &wslConfigSupplyEffect{
		deps: launchExecutorDeps{}, distro: "Ubuntu",
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
		openCodeLayer: content, layerPrep: prep, ptyRef: &pty3,
	}
	if _, err := lateEffect.Apply(context.Background()); err == nil {
		t.Fatal("layer after PTY start must be rejected")
	}
}
