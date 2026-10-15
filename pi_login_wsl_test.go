package main

// pi_login_wsl_test.go — A2 闸门解除（主上裁定 2026-10-16）的回归：
// WSL spec 下的登录计划放行、WSL 锚定分叉（载荷惰性同步 + env -u 重锚 +
// PI_CODING_AGENT_DIR 剥离）、桌面闸门的同步校验调用与失败降级语义。

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
// env 剥离 PI_CODING_AGENT_DIR（Windows 路径不跨界），StartupCommand 换成
// WSL 权威载荷（惰性同步片段 + env -u 前缀 + 单引号 CLI + exec bash -li），
// recipe 保真。
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
	found := false
	for _, eff := range plan.Effects {
		if eff.Process == nil {
			continue
		}
		found = true
		spec := eff.Process.Resolved
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
		// 载荷：同步片段（源为映射后的 /mnt 路径；非盘符 HOME（非 Windows 宿主）
		// 无法映射，无片段属预期）→ env -u 前缀（凭据键 + PI_CODING_AGENT_DIR）
		// → 单引号 CLI 与参数 → exec bash -li。
		cmd := spec.StartupCommand
		wantSrc := launcher.WSLDrvFSMountPath(filepath.Join(home, ".pi", "agent", "auth.json"))
		if wantSrc != "" {
			if !strings.Contains(cmd, "cmp -s -- '"+wantSrc+"'") {
				t.Fatalf("payload missing auth sync snippet (src=%s): %s", wantSrc, cmd)
			}
			// M-1：双向——stat 秒差 >2 才前向（q2-1：与 Go 侧 DistroTieKept 同带 2s 容差，
			// 歧义带内不前向）；否则反向回写 Windows 侧。
			if !strings.Contains(cmd, "- $(stat -c %Y -- \"$HOME/.pi/agent/auth.json\") )) -gt 2 ]") {
				t.Fatalf("payload missing bidirectional stat-based 2s-tolerance guard: %s", cmd)
			}
			if !strings.Contains(cmd, "cp -- \"$HOME/.pi/agent/auth.json\" '"+wantSrc+"'") {
				t.Fatalf("payload missing reverse copy branch: %s", cmd)
			}
		} else if strings.Contains(cmd, "cmp -s --") {
			t.Fatalf("unexpected sync snippet for unmappable home: %s", cmd)
		}
		if !strings.Contains(cmd, "chmod 600 -- \"$HOME/.pi/agent/auth.json\"") {
			t.Fatalf("payload missing chmod 600: %s", cmd)
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
	if !found {
		t.Fatal("no process effect")
	}
}

// TestPiLoginWSLPayloadSkipsAgentDirPin：非 WSL spec 的载荷语义不变
// （不携带同步片段 / env -u PI_CODING_AGENT_DIR）。
func TestPiLoginWSLPayloadSkipsAgentDirPin(t *testing.T) {
	home := isolatedHome(t)
	canonical := filepath.Join(home, ".pi", "agent")
	spec := piLoginWSLReAnchorSpec(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapShellAttach,
		Shell:         &platform.ResolvedShell{Key: "bash", Path: "/bin/bash"},
		CLI:           platform.ResolvedCLI{Path: "pi", Args: []string{"--provider", "x"}},
	}, canonical)
	if strings.Contains(spec.StartupCommand, "cmp -s") {
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

// TestPiLoginDesktopWSLGateSync：桌面闸门在 WSL 登录会话放行前调用惰性同步，
// 并按结果分叉：失败且 distro 无副本 → 中文报错拒绝；失败但 distro 有副本 →
// WARN 降级放行（后续 control 未接线自然失败，不触达 PTY spawn）。
func TestPiLoginDesktopWSLGateSync(t *testing.T) {
	stubSync := func(outcome launcher.SyncWSLPiLoginAuthOutcome, err error) *[]string {
		calls := &[]string{}
		syncWSLPiLoginAuthFn = func(distro string) (launcher.SyncWSLPiLoginAuthOutcome, error) {
			*calls = append(*calls, distro)
			return outcome, err
		}
		t.Cleanup(func() { syncWSLPiLoginAuthFn = launcher.SyncWSLPiLoginAuth })
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
		calls := stubSync(launcher.SyncWSLPiLoginAuthDistroNoAuth, errors.New("chmod failed"))
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "WSL 内没有可用的 Pi 登录凭据") {
			t.Fatalf("expected chinese fail-closed error, got %v", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("sync not consulted once: %v", *calls)
		}
	})

	t.Run("source missing keeps login-invalid semantics", func(t *testing.T) {
		stubSync(launcher.SyncWSLPiLoginAuthDistroNoAuth, launcher.ErrWSLPiLoginSourceMissing)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "Pi 登录已失效") {
			t.Fatalf("expected login-invalid error, got %v", err)
		}
	})

	t.Run("stale distro copy degrades with warn", func(t *testing.T) {
		stubSync(launcher.SyncWSLPiLoginAuthDistroHasCopy, errors.New("UNC unreachable"))
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
	})

	t.Run("fresh copy passes gate", func(t *testing.T) {
		calls := stubSync(launcher.SyncWSLPiLoginAuthFresh, nil)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("sync not consulted once: %v", *calls)
		}
	})

	t.Run("distro newer reverse-sync passes gate", func(t *testing.T) {
		stubSync(launcher.SyncWSLPiLoginAuthDistroNewer, nil)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
	})

	t.Run("mtime tie kept passes gate", func(t *testing.T) {
		stubSync(launcher.SyncWSLPiLoginAuthDistroTieKept, nil)
		err := launchWithFixture(t)
		if err == nil || !strings.Contains(err.Error(), "start pi pty") {
			t.Fatalf("expected gate to pass through to PTY layer, got %v", err)
		}
	})
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

// TestPiLoginWSLAuthSnippetQuoting：同步片段的源路径经 POSIX 单引号包裹，
// 含单引号的路径不逃逸。
func TestPiLoginWSLAuthSnippetQuoting(t *testing.T) {
	snippet := piLoginWSLAuthSyncSnippet(`/mnt/c/Users/o'brien/.pi/agent/auth.json`)
	if !strings.Contains(snippet, `'/mnt/c/Users/o'\''brien/.pi/agent/auth.json'`) {
		t.Fatalf("source path not single-quote escaped: %s", snippet)
	}
	if !strings.HasPrefix(snippet, `mkdir -p -- "$HOME/.pi/agent"`) {
		t.Fatalf("snippet missing mkdir prefix: %s", snippet)
	}
}
