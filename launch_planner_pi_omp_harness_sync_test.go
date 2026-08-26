package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/settings"
)

// Pi/Omp 启动链解析 HarnessSync 标记的 anthropic 桶预设（第二轮扩面）：
// marked 预设作为 stable key 命中（openai 桶 miss 后 opt-in 回退），
// unmarked anthropic 预设不命中（走 legacy 回退，裸 model id 不解析预设）。
// Codex 调用点不受影响（仍硬走 openai 桶）。

func TestBuildPlanPiOmpResolvesMarkedAnthropicPreset(t *testing.T) {
	for _, cli := range []contract.CLIType{contract.CLITypePi, contract.CLITypeOmp} {
		t.Run(string(cli), func(t *testing.T) {
			home := isolatedHome(t)
			dir := t.TempDir()
			cfgSvc := config.NewConfigService(dir)
			if err := cfgSvc.Load(); err != nil {
				t.Fatal(err)
			}
			secSvc := secrets.NewSecretsService(dir)
			settingsSvc := settings.NewService(dir)
			if err := settingsSvc.Load(); err != nil {
				t.Fatal(err)
			}
			defaults, err := launchplan.NewDefaultStore(settingsSvc)
			if err != nil {
				t.Fatal(err)
			}
			// anthropic 兼容 provider（非 OpenAI）：其 anthropic 桶预设带
			// HarnessSync 标记，应可被 pi/omp 启动链解析。
			if err := cfgSvc.SaveProvider("marked-provider", config.Provider{
				Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://marked.example/v1"},
				DefaultModel: "marked-default",
			}); err != nil {
				t.Fatal(err)
			}
			if err := cfgSvc.SaveTerminalPreset("claude_code", "marked-provider/marked", config.TerminalPreset{
				Name: "Marked", Provider: "marked-provider", Model: "marked-model",
				Parameters:   config.Parameters{ReasoningEffort: "max"},
				HarnessSync: true,
			}); err != nil {
				t.Fatal(err)
			}
			if err := secSvc.SetAPIKey("marked-provider", "marked-secret-123456789"); err != nil {
				t.Fatal(err)
			}
			planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(),
				paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
			plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
				CLIType: cli, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
				StableRefs: &launchplan.StableLaunchRefs{ModelRef: "marked-provider/marked"},
			})
			if failure != nil {
				t.Fatalf("BuildPlan failure: %#v", failure)
			}
			defer plan.Secrets.Dispose()
			// Recipe 保留原始 stable refs（解析发生在 effects，不在 recipe）；
		// 解析结果验证 argv 与托管配置。
			// argv 走 amagi-<provider> 托管条目 + preset 模型。
			var sawProcess bool
			for _, eff := range plan.Effects {
				if eff.Process == nil {
					continue
				}
				sawProcess = true
				joined := strings.Join(eff.Process.Resolved.CLI.Args, " ")
				if !strings.Contains(joined, "--provider amagi-marked-provider") ||
					!strings.Contains(joined, "--model marked-model") {
					t.Fatalf("process args missing marked preset resolution: %v", eff.Process.Resolved.CLI.Args)
				}
			}
			if !sawProcess {
				t.Fatal("plan has no process effect")
			}
			// 托管配置写入 marked 模型（config mutation buffer 内含 marked-model）。
			var configContent []byte
			for _, eff := range plan.Effects {
				if eff.Config != nil {
					content, ok := plan.Secrets.Buffer(eff.Config.Candidate)
					if !ok {
						t.Fatal("config buffer not found")
					}
					configContent = content
				}
			}
			if configContent == nil {
				t.Fatal("plan should carry config mutation for marked anthropic preset")
			}
			var cfg map[string]any
			if err := json.Unmarshal(configContent, &cfg); err != nil {
				t.Fatalf("unmarshal config: %v", err)
			}
			serialized, _ := json.Marshal(cfg)
			if !strings.Contains(string(serialized), "marked-model") {
				t.Fatalf("managed config missing marked model: %s", serialized)
			}
		})
	}
}

func TestBuildPlanPiUnmarkedAnthropicPresetNotResolved(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatal(err)
	}
	secSvc := secrets.NewSecretsService(dir)
	settingsSvc := settings.NewService(dir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatal(err)
	}
	defaults, err := launchplan.NewDefaultStore(settingsSvc)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfgSvc.SaveProvider("plain-provider", config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://plain.example/v1"},
		DefaultModel: "plain-default",
	}); err != nil {
		t.Fatal(err)
	}
	if err := cfgSvc.SaveTerminalPreset("anthropic", "plain-provider/unmarked", config.TerminalPreset{
		Name: "Unmarked", Provider: "plain-provider", Model: "unmarked-model",
	}); err != nil {
		t.Fatal(err)
	}
	if err := secSvc.SetAPIKey("plain-provider", "plain-secret-123456789"); err != nil {
		t.Fatal(err)
	}
	planner := newAppLaunchPlanner(cfgSvc, secSvc, defaults, fakeCLIResolver{}, fakePlatformCaps(),
		paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		StableRefs: &launchplan.StableLaunchRefs{ModelRef: "plain-provider/unmarked"},
	})
	// 差分验证：若 unmarked 预设被错误解析，provider 会取自预设（plain-provider）
	// 且计划成功；实际不解析 → providerID 空 → FailureLaunchContext（密钥已备，
	// 排除其他原因）。
	if failure == nil {
		plan.Secrets.Dispose()
		t.Fatal("unmarked anthropic preset must not be resolved by pi launch chain")
	}
	if failure.Kind != launchplan.FailureLaunchContext {
		t.Fatalf("failure kind = %v, want FailureLaunchContext", failure.Kind)
	}
}
