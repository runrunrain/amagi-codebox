package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/settings"
)

func piLoginFixture(t *testing.T, cfg *config.ConfigService) (config.PiLoginPreset, string, string) {
	t.Helper()
	dir := defaultPiAgentDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(auth, []byte(`{"openai-codex":{"type":"oauth","access":"fixture-access","refresh":"fixture-refresh","expires":1}}`), 0600); err != nil {
		t.Fatal(err)
	}
	preset := config.PiLoginPreset{Name: "Daily", Provider: "openai-codex", Model: "native-model", Thinking: "max"}
	if err := cfg.SavePiLoginPreset(preset); err != nil {
		t.Fatal(err)
	}
	return preset, config.PiLoginPresetKey(preset.Provider, preset.Name), auth
}

func TestPiLoginBuildPlanAndStableRestart(t *testing.T) {
	home := isolatedHome(t)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "fixture-inherited-key")
	t.Setenv("ANTHROPIC_API_KEY", "fixture-inherited-key")
	dir := t.TempDir()
	cfg := config.NewConfigService(dir)
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	preset, key, auth := piLoginFixture(t, cfg)
	// Ordinary same-name API provider must not hijack native login resolution.
	if err := cfg.SaveProvider(preset.Provider, config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true}, DefaultModel: "api-model"}); err != nil {
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
	planner := newAppLaunchPlanner(cfg, newTestSecretsService(t, dir), defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	refs := &launchplan.StableLaunchRefs{ProviderRef: config.PiLoginProviderID(preset.Provider), ModelRef: key}
	for _, origin := range []launchplan.Origin{launchplan.OriginRemote, launchplan.OriginRestart} {
		req := launchplan.BuildRequest{CLIType: contract.CLITypePi, Origin: origin, Mode: launchplan.ModeEmbedded, StableRefs: refs}
		plan, failure := planner.BuildPlan(context.Background(), req)
		if failure != nil {
			t.Fatalf("login plan: %#v", failure)
		}
		if err := plan.Validate(); err != nil {
			t.Fatal(err)
		}
		if plan.Recipe.ProviderRef != config.PiLoginProviderID(preset.Provider) || plan.Recipe.ModelRef != key || plan.Recipe.PresetRef != key {
			t.Fatalf("lost login recipe: %+v", plan.Recipe)
		}
		if err := defaults.RecordDesktopActivation(plan.Recipe); err != nil {
			t.Fatal(err)
		}
		refs = nil // Restart through persisted desktop defaults, not explicit refs.
		found := false
		for _, eff := range plan.Effects {
			if eff.Config != nil {
				t.Fatal("login plan generated managed configuration")
			}
			if eff.Process != nil {
				found = true
				spec := eff.Process.Resolved
				if !reflect.DeepEqual(spec.CLI.Args, []string{"--provider", "openai-codex", "--model", "native-model", "--thinking", "max"}) {
					t.Fatalf("argv: %v", spec.CLI.Args)
				}
				// R2：canonical agent root 被显式钉住（而非删除）；继承凭据被剥离。
				envMap := map[string]string{}
				for _, kv := range spec.Env.Variables {
					k, v, _ := strings.Cut(kv, "=")
					envMap[k] = v
				}
				if envMap["PI_CODING_AGENT_DIR"] != filepath.Join(home, ".pi", "agent") {
					t.Fatalf("agent root not pinned to canonical: %q", envMap["PI_CODING_AGENT_DIR"])
				}
				for _, entry := range spec.Env.Variables {
					if strings.Contains(entry, "fixture-inherited-key") {
						t.Fatal("inherited credential survives")
					}
				}
			}
		}
		if !found {
			t.Fatal("no process effect")
		}
		plan.Secrets.Dispose()
	}
	body, _ := os.ReadFile(auth)
	if !strings.Contains(string(body), "fixture-access") {
		t.Fatal("auth mutated")
	}
	if _, err := os.Stat(filepath.Join(defaultPiAgentDir(), "models.json")); !os.IsNotExist(err) {
		t.Fatal("login wrote managed provider")
	}
	// Removing login rejects restart, despite persisted stable refs and API namesake.
	if err := os.WriteFile(auth, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{CLIType: contract.CLITypePi, Origin: launchplan.OriginRestart}); failure == nil {
		plan.Secrets.Dispose()
		t.Fatal("logout silently fell back")
	}
	for _, cli := range []contract.CLIType{contract.CLITypeClaudeCode, contract.CLITypeCodex, contract.CLITypeOpenCode, contract.CLITypeOmp} {
		plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{CLIType: cli, Origin: launchplan.OriginRemote, StableRefs: &launchplan.StableLaunchRefs{ProviderRef: config.PiLoginProviderID(preset.Provider), PresetRef: key, ModelRef: key}})
		if failure == nil {
			plan.Secrets.Dispose()
			t.Fatalf("login allowed for %s", cli)
		}
	}
}

func TestPiLoginDesktopRejectsForeignCLIsAndStaleSelection(t *testing.T) {
	isolatedHome(t)
	app := newTestApp(t)
	preset, key, auth := piLoginFixture(t, app.Config)
	source := config.PiLoginProviderID(preset.Provider)
	calls := []func() (string, error){
		func() (string, error) { return app.LaunchSession(source, key, "embedded", "", false, "") },
		func() (string, error) { return app.LaunchCodexSession(key, source, "embedded", "", "") },
		func() (string, error) { return app.LaunchOpenCode(source, key, "embedded", "", "") },
		func() (string, error) { return app.LaunchOmpSession(key, source, "embedded", "", "") },
	}
	for _, call := range calls {
		if _, err := call(); err == nil {
			t.Fatal("foreign CLI accepted login")
		}
	}
	if login, err := resolvePiLoginLaunch(app.Config, key, source, true); err == nil || login != nil {
		t.Fatal("WSL host credential downgrade")
	}
	if login, err := resolvePiLoginLaunch(app.Config, "native-model", source, false); err == nil || login != nil {
		t.Fatal("login without dedicated preset allowed")
	}
	if login, err := resolvePiLoginLaunch(app.Config, key, "ordinary-provider", false); err == nil || login != nil {
		t.Fatal("mismatched source allowed")
	}
	if err := os.Remove(auth); err != nil {
		t.Fatal(err)
	}
	if _, err := app.LaunchPiSession(key, source, "embedded", "", ""); err == nil {
		t.Fatal("stale desktop login accepted")
	}
}

// A resolver fixture stops at the OS boundary; verifies the real desktop entry's
// argv/env and that configuration/auth remain unchanged before a spawn failure.
type piLoginCaptureResolver struct{ request platform.ResolveRequest }

func (r *piLoginCaptureResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	r.request = req
	return platform.ResolvedLaunchSpec{}, os.ErrNotExist
}
func (*piLoginCaptureResolver) ResolveExecutable(string, []string, []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{}, platform.LaunchDiagnostics{}, os.ErrNotExist
}

func TestPiLoginDesktopNativeArgsAndFailureNoMutation(t *testing.T) {
	home := isolatedHome(t)
	app := newTestApp(t)
	app.ctx = t.Context()
	app.Capabilities = fakePlatformCaps()
	preset, key, auth := piLoginFixture(t, app.Config)
	before, _ := os.ReadFile(auth)
	resolver := &piLoginCaptureResolver{}
	app.CLIResolver = resolver
	t.Setenv("OPENAI_API_KEY", "fixture-inherited-key")
	if _, err := app.LaunchPiSession(key, config.PiLoginProviderID(preset.Provider), "embedded", t.TempDir(), ""); err == nil {
		t.Fatal("expected OS-boundary fixture failure")
	}
	if !reflect.DeepEqual(resolver.request.CLIArgs, []string{"--provider", "openai-codex", "--model", "native-model", "--thinking", "max"}) {
		t.Fatalf("desktop argv: %v", resolver.request.CLIArgs)
	}
	for _, entry := range resolver.request.Env {
		if strings.Contains(entry, "fixture-inherited-key") {
			t.Fatal("desktop injected inherited key")
		}
		if strings.HasPrefix(entry, "PI_CODING_AGENT_DIR=") && entry != "PI_CODING_AGENT_DIR="+filepath.Join(home, ".pi", "agent") {
			t.Fatalf("desktop agent root not pinned: %q", entry)
		}
	}
	after, _ := os.ReadFile(auth)
	if string(before) != string(after) {
		t.Fatal("auth mutated")
	}
	if _, err := os.Stat(filepath.Join(defaultPiAgentDir(), "models.json")); !os.IsNotExist(err) {
		t.Fatal("managed provider written")
	}
}

// R1 回归：字面名为 "pi-login" 的普通 provider 与其公共预设（含三段 key）
// 不被登录命名空间劫持——保存、解析、Pi/其它 CLI 启动分类全部走普通路径。
func TestPiLoginNamespaceOrdinaryProviderCoexists(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()
	cfg := config.NewConfigService(dir)
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveProvider("pi-login", config.Provider{OpenAI: &config.OpenAIFormat{Enabled: true, BaseURL: "https://pi-login.example/v1"}, DefaultModel: "api-model"}); err != nil {
		t.Fatalf("SaveProvider(pi-login): %v", err)
	}
	for _, key := range []string{"pi-login/Daily", "pi-login/Foo/Bar"} {
		if err := cfg.SaveTerminalPreset("openai", key, config.TerminalPreset{Name: strings.TrimPrefix(key, "pi-login/"), Provider: "pi-login", Model: "api-model"}); err != nil {
			t.Fatalf("SaveTerminalPreset(%s): %v", key, err)
		}
		if _, isLogin, err := cfg.ResolvePiLoginLaunch(key, "pi-login"); isLogin || err != nil {
			t.Fatalf("public key %s hijacked as login: isLogin=%v err=%v", key, isLogin, err)
		}
		if err := cfg.RejectPiLoginLaunchRefs(key, "pi-login"); err != nil {
			t.Fatalf("public key %s rejected on foreign CLI: %v", key, err)
		}
	}
	// 同一配置内并存真实登录预设：登录 key 精确命中仍走登录；普通 key 不受影响。
	preset, key, _ := piLoginFixture(t, cfg)
	if _, isLogin, err := cfg.ResolvePiLoginLaunch(key, config.PiLoginProviderID(preset.Provider)); !isLogin || err != nil {
		t.Fatalf("stored login key not classified as login: %v %v", isLogin, err)
	}
	if err := cfg.RejectPiLoginLaunchRefs(key, config.PiLoginProviderID(preset.Provider)); err == nil {
		t.Fatal("stored login key accepted on foreign CLI")
	}
	// 登录 key 与公共 key 同字符串的互斥守卫（两个方向）。
	if err := cfg.SaveTerminalPreset("openai", key, config.TerminalPreset{Name: "collide", Provider: "pi-login", Model: "api-model"}); err == nil {
		t.Fatal("public preset overwrote login key")
	}
	collideName := "Bar"
	collideKey := config.PiLoginPresetKey("pi-login", collideName)
	if err := cfg.SaveTerminalPreset("openai", collideKey, config.TerminalPreset{Name: "collide", Provider: "pi-login", Model: "api-model"}); err != nil {
		t.Fatalf("prepare public 3-segment key: %v", err)
	}
	if err := cfg.SavePiLoginPreset(config.PiLoginPreset{Name: collideName, Provider: "pi-login", Model: "m"}); err == nil {
		t.Fatal("login preset overwrote public key")
	}
	// 普通 Pi 计划可构建（带密钥），参数走 amagi- 托管 provider 而非登录分支。
	sec := newTestSecretsService(t, dir)
	if err := sec.SetAPIKey("pi-login", "fixture-api-key-123456789"); err != nil {
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
	planner := newAppLaunchPlanner(cfg, sec, defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		StableRefs: &launchplan.StableLaunchRefs{ProviderRef: "pi-login", ModelRef: "pi-login/Daily"},
	})
	if failure != nil {
		t.Fatalf("ordinary pi-login provider plan failed: %#v", failure)
	}
	defer plan.Secrets.Dispose()
	sawManaged := false
	for _, eff := range plan.Effects {
		if eff.Process != nil {
			joined := strings.Join(eff.Process.Resolved.CLI.Args, " ")
			if !strings.Contains(joined, "--provider amagi-pi-login") || !strings.Contains(joined, "--model api-model") {
				t.Fatalf("ordinary plan argv: %v", eff.Process.Resolved.CLI.Args)
			}
		}
		if eff.Config != nil {
			sawManaged = true
		}
	}
	if !sawManaged {
		t.Fatal("ordinary pi-login provider lost managed config effect")
	}
}

// R3 回归（planner 边界）：POSIX shell 的启动命令在解析后被 env 前缀重新锚定；
// direct 模式不加前缀（spec.Env 已钉定）。Windows 家族 shell 不包 POSIX 前缀。
func TestPiLoginPlannerReAnchorsPosixShellCommand(t *testing.T) {
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
	canonical := filepath.Join(home, ".pi", "agent")
	wantPrefix := "env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_BASE_URL -u OPENAI_API_KEY -u OPENAI_BASE_URL -u CODEX_API_KEY -u GEMINI_API_KEY -u GOOGLE_API_KEY -u GITHUB_TOKEN -u GH_TOKEN -u COPILOT_GITHUB_TOKEN PI_CODING_AGENT_DIR='" + canonical + "' "

	for _, tc := range []struct {
		name    string
		shell   *platform.ResolvedShell
		boot    platform.LaunchBootstrapMode
		wrapped bool
	}{
		{"zsh inline", &platform.ResolvedShell{Key: "zsh", Path: "/bin/zsh"}, platform.BootstrapShellInline, true},
		{"bash attach", &platform.ResolvedShell{Key: "bash", Path: "/bin/bash"}, platform.BootstrapShellAttach, true},
		{"direct", nil, platform.BootstrapDirectCommand, false},
		{"pwsh", &platform.ResolvedShell{Key: "pwsh", Path: "/usr/bin/pwsh"}, platform.BootstrapShellInline, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &piLoginShellResolver{shell: tc.shell, boot: tc.boot}
			planner := newAppLaunchPlanner(cfg, newTestSecretsService(t, dir), defaults, resolver, fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), home)
			plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
				CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
				StableRefs: &launchplan.StableLaunchRefs{ProviderRef: config.PiLoginProviderID(preset.Provider), ModelRef: key},
			})
			if failure != nil {
				t.Fatalf("login plan: %#v", failure)
			}
			defer plan.Secrets.Dispose()
			for _, eff := range plan.Effects {
				if eff.Process == nil {
					continue
				}
				cmd := eff.Process.Resolved.StartupCommand
				if tc.wrapped && !strings.HasPrefix(cmd, wantPrefix) {
					t.Fatalf("startup command not re-anchored: %q", cmd)
				}
				if !tc.wrapped && strings.Contains(cmd, "env -u") {
					t.Fatalf("unexpected re-anchor for %s: %q", tc.name, cmd)
				}
				envMap := map[string]string{}
				for _, kv := range eff.Process.Resolved.Env.Variables {
					k, v, _ := strings.Cut(kv, "=")
					envMap[k] = v
				}
				if envMap["PI_CODING_AGENT_DIR"] != canonical {
					t.Fatalf("env root not pinned for %s: %q", tc.name, envMap["PI_CODING_AGENT_DIR"])
				}
			}
		})
	}
}

// piLoginShellResolver 按 fixture 指定 shell/bootstrap 形态，其余复用 fakeCLIResolver。
type piLoginShellResolver struct {
	shell *platform.ResolvedShell
	boot  platform.LaunchBootstrapMode
}

func (r *piLoginShellResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	spec, err := fakeCLIResolver{}.Resolve(req)
	if err != nil {
		return spec, err
	}
	spec.Shell = r.shell
	spec.BootstrapMode = r.boot
	if r.boot == platform.BootstrapShellAttach {
		spec.StartupCommand = "pi --provider x"
	}
	return spec, nil
}

func (*piLoginShellResolver) ResolveExecutable(cmd string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return fakeCLIResolver{}.ResolveExecutable(cmd, args, env)
}

// R2 回归（planner 边界）：EnvVars 面板把子进程 HOME 改到 B，登录 env 仍钉住
// 检查时使用的 canonical 标准根（A），B 不改变实际认证目录。
func TestPiLoginPlannerPinsCanonicalRootOverCustomHome(t *testing.T) {
	home := isolatedHome(t)
	homeB := t.TempDir()
	dir := t.TempDir()
	cfg := config.NewConfigService(dir)
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	preset, key, _ := piLoginFixture(t, cfg)
	envSvc := envvars.NewEnvVarsService(dir)
	if err := envSvc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := envSvc.Set("HOME", homeB); err != nil {
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
	planner := newAppLaunchPlanner(cfg, newTestSecretsService(t, dir), defaults, fakeCLIResolver{}, fakePlatformCaps(), paths.NewPathsService(dir), envSvc, home)
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: contract.CLITypePi, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		StableRefs: &launchplan.StableLaunchRefs{ProviderRef: config.PiLoginProviderID(preset.Provider), ModelRef: key},
	})
	if failure != nil {
		t.Fatalf("login plan: %#v", failure)
	}
	defer plan.Secrets.Dispose()
	for _, eff := range plan.Effects {
		if eff.Process == nil {
			continue
		}
		envMap := map[string]string{}
		for _, kv := range eff.Process.Resolved.Env.Variables {
			k, v, _ := strings.Cut(kv, "=")
			envMap[k] = v
		}
		if envMap["PI_CODING_AGENT_DIR"] != filepath.Join(home, ".pi", "agent") {
			t.Fatalf("agent root not pinned to canonical A: %q", envMap["PI_CODING_AGENT_DIR"])
		}
		if envMap["HOME"] != homeB {
			t.Fatalf("custom HOME semantics changed for non-login vars: %q", envMap["HOME"])
		}
	}
}
