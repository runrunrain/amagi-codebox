package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/headroom"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
	"amagi-codebox/internal/settings"
)

// --- headroomWSLNetworkAdmission 单测（D1 判定核心） ---

// NAT / unknown（无 .wslconfig）模式：WSL 目标必须拒绝，且拒绝文案携带可
// 操作的修复指引（与 aiproxy.PinProxyEnvironment 的 NAT 文案同构）。
func TestHeadroomWSLNetworkAdmissionNATRejected(t *testing.T) {
	home := t.TempDir() // no .wslconfig -> unknown -> treated as NAT
	err := headroomWSLNetworkAdmission(true, []string{"USERPROFILE=" + home})
	if err == nil {
		t.Fatal("NAT WSL headroom session must be rejected")
	}
	msg := err.Error()
	for _, want := range []string{"NAT", "networkingMode=mirrored", "wsl --shutdown", ".wslconfig", "8787", "未启动"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rejection text missing %q: %s", want, msg)
		}
	}
}

// mirrored：遵循现有准入策略放行——放行仅是准入，文案与断言都不宣称连通。
func TestHeadroomWSLNetworkAdmissionMirroredPasses(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".wslconfig"), []byte("[wsl2]\nnetworkingMode=mirrored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := headroomWSLNetworkAdmission(true, []string{"USERPROFILE=" + home}); err != nil {
		t.Fatalf("mirrored WSL headroom session must be admitted (not a connectivity claim): %v", err)
	}
}

// 非 WSL 目标（桌面/外部终端会话）：不受网络模式影响，永不拦截。
func TestHeadroomWSLNetworkAdmissionNonWSLPasses(t *testing.T) {
	home := t.TempDir() // NAT-shaped home on purpose
	if err := headroomWSLNetworkAdmission(false, []string{"USERPROFILE=" + home}); err != nil {
		t.Fatalf("non-WSL session must never hit the gate: %v", err)
	}
}

func TestHeadroomSpecTargetsWSL(t *testing.T) {
	if !headroomSpecTargetsWSL(platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL}) {
		t.Fatal("BootstrapWSL spec must target WSL")
	}
	if headroomSpecTargetsWSL(platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapShellAttach}) {
		t.Fatal("non-WSL spec must not target WSL")
	}
}

// D6：codex 全局压缩对 WSL 不生效的 advisory/拒绝文案钉（路由载体是宿主侧
// config.toml，WSL 会话读取发行版侧独立配置）。
func TestCodexHeadroomWSLIneffectiveMsg(t *testing.T) {
	msg := codexHeadroomWSLIneffectiveMsg()
	for _, want := range []string{"8788", "config.toml", "WSL", "不会生效"} {
		if !strings.Contains(msg, want) {
			t.Errorf("advisory text missing %q: %s", want, msg)
		}
	}
	if err := errCodexHeadroomWSLUnsupported(); err == nil || !strings.Contains(err.Error(), "不生效") {
		t.Fatalf("unsupported error must carry the ineffective semantics: %v", err)
	}
}

// --- planner 链集成（D1/D6 的 remote/restart 面） ---

// wslSpecCLIResolver 把一切请求解析为落在 WSL distro 内的 spec（env 透传，
// 使 WSLNetworkingMode 能从 spec.Env.Variables 的 USERPROFILE 定位 .wslconfig）。
type wslSpecCLIResolver struct{}

func (wslSpecCLIResolver) Resolve(req platform.ResolveRequest) (platform.ResolvedLaunchSpec, error) {
	return platform.ResolvedLaunchSpec{
		AppType: req.AppType, LaunchMode: req.LaunchMode, WorkDir: req.WorkDir,
		CLI:           platform.ResolvedCLI{Name: req.AppType, Path: "/wsl/echo"},
		Env:           platform.ResolvedEnv{Variables: append([]string(nil), req.Env...)},
		BootstrapMode: platform.BootstrapWSL,
	}, nil
}

func (wslSpecCLIResolver) ResolveExecutable(command string, args []string, env []string) (platform.ResolvedCLI, platform.LaunchDiagnostics, error) {
	return platform.ResolvedCLI{Name: "test", Path: "/wsl/echo"}, platform.LaunchDiagnostics{}, nil
}

// newHeadroomGatePlanner 构造带可注入 resolver 的 planner（isolated home，
// .wslconfig 内容由 networkingMode 控制，"" = 无文件 → NAT fail-safe）。
// 返回 planner 与其背后的 config/secrets 服务（供用例直接注入 provider/key）。
func newHeadroomGatePlanner(t *testing.T, resolver platform.CLIResolver, networkingMode string) (*appLaunchPlanner, *config.ConfigService, *secrets.SecretsService) {
	t.Helper()
	homeDir := isolatedHome(t)
	if networkingMode != "" {
		if err := os.WriteFile(filepath.Join(homeDir, ".wslconfig"), []byte(networkingMode), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	cfgSvc := config.NewConfigService(dir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatalf("config Load: %v", err)
	}
	settingsSvc := settings.NewService(dir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatalf("settings Load: %v", err)
	}
	defaults, err := launchplan.NewDefaultStore(settingsSvc)
	if err != nil {
		t.Fatalf("NewDefaultStore: %v", err)
	}
	secSvc := secrets.NewSecretsService(dir)
	return newAppLaunchPlanner(cfgSvc, secSvc, defaults, resolver,
		fakePlatformCaps(), paths.NewPathsService(dir), envvars.NewEnvVarsService(dir), homeDir), cfgSvc, secSvc
}

func saveHeadroomGateProvider(t *testing.T, cfgSvc *config.ConfigService, secSvc *secrets.SecretsService, cli contract.CLIType) {
	t.Helper()
	provider := config.Provider{
		Anthropic:    &config.AnthropicFormat{Enabled: true, BaseURL: "https://api.test.com"},
		DefaultModel: "test-model",
	}
	if cli == contract.CLITypeCodex {
		provider.Anthropic = nil
		provider.OpenAI = &config.OpenAIFormat{Enabled: true, BaseURL: "https://api.openai.com/v1", AuthKey: "OPENAI_API_KEY"}
	}
	if err := cfgSvc.SaveProvider("gate-provider", provider); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if err := secSvc.SetAPIKey("gate-provider", "sk-gate"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
}

func headroomBuildRequest(cli contract.CLIType, useHeadroom bool) launchplan.BuildRequest {
	on := useHeadroom
	return launchplan.BuildRequest{
		CLIType: cli, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
		Workdir: tWorkdirForGate(),
		StableRefs: &launchplan.StableLaunchRefs{
			ProviderRef: "gate-provider", ModelRef: "test-model", UseHeadroom: &on,
		},
	}
}

func tWorkdirForGate() string { return os.TempDir() }

func hasEffectKind(plan *launchplan.Plan, kind launchplan.EffectKind) bool {
	for _, e := range plan.Effects {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// D1 remote 链：UseHeadroom + WSL spec + NAT（无 .wslconfig）→ 计划构建即拒绝
// （FailureCapability），不产生 HeadroomStart effect、不启动 PTY——宿主侧 8787
// 不会被启动，distro 内也不会拿到不可达的 ANTHROPIC_BASE_URL 会话。
func TestBuildClaudePlanHeadroomWSLNATRejected(t *testing.T) {
	p, cfgSvc, secSvc := newHeadroomGatePlanner(t, wslSpecCLIResolver{}, "")
	saveHeadroomGateProvider(t, cfgSvc, secSvc, contract.CLITypeClaudeCode)
	plan, failure := p.BuildPlan(context.Background(), headroomBuildRequest(contract.CLITypeClaudeCode, true))
	if failure == nil || failure.Kind != launchplan.FailureCapability {
		t.Fatalf("headroom + WSL + NAT must fail plan build with FailureCapability, got plan=%v failure=%+v", plan, failure)
	}
	if plan != nil {
		t.Fatalf("rejected plan must not be returned: %+v", plan)
	}
}

// D1 remote 链 mirrored：放行且计划携带 HeadroomStart effect（准入语义，
// 不构成连通性声明——与 aiproxy 闸门同款行为）。
func TestBuildClaudePlanHeadroomWSLMirroredAdmitted(t *testing.T) {
	p, cfgSvc, secSvc := newHeadroomGatePlanner(t, wslSpecCLIResolver{}, "[wsl2]\nnetworkingMode=mirrored\n")
	saveHeadroomGateProvider(t, cfgSvc, secSvc, contract.CLITypeClaudeCode)
	plan, failure := p.BuildPlan(context.Background(), headroomBuildRequest(contract.CLITypeClaudeCode, true))
	if failure != nil {
		t.Fatalf("mirrored headroom WSL session must be admitted: %+v", failure)
	}
	if !hasEffectKind(plan, launchplan.EffectHeadroomStart) {
		t.Fatalf("admitted plan must carry the headroom effect: %+v", plan.Effects)
	}
}

// 非 WSL spec：UseHeadroom 会话不受闸门影响（无 .wslconfig 的 NAT 形态宿主
// 也不拦——闸门只对 WSL 目标生效，防止误伤普通桌面/远端会话）。
func TestBuildClaudePlanHeadroomNonWSLUnaffected(t *testing.T) {
	p, cfgSvc, secSvc := newHeadroomGatePlanner(t, fakeCLIResolver{}, "")
	saveHeadroomGateProvider(t, cfgSvc, secSvc, contract.CLITypeClaudeCode)
	plan, failure := p.BuildPlan(context.Background(), headroomBuildRequest(contract.CLITypeClaudeCode, true))
	if failure != nil {
		t.Fatalf("non-WSL headroom session must not hit the WSL gate: %+v", failure)
	}
	if !hasEffectKind(plan, launchplan.EffectHeadroomStart) {
		t.Fatalf("plan must keep the headroom effect: %+v", plan.Effects)
	}
}

// D6 remote 链：显式选择压缩的 WSL codex 会话按「无法兑现即拒绝」处理——
// mirrored 也不放行（路由载体是宿主侧 config.toml，网络模式无关）。
func TestBuildCodexPlanHeadroomWSLRejected(t *testing.T) {
	for _, mode := range []string{"", "[wsl2]\nnetworkingMode=mirrored\n", "[wsl2]\nnetworkingMode=nat\n"} {
		p, cfgSvc, secSvc := newHeadroomGatePlanner(t, wslSpecCLIResolver{}, mode)
		saveHeadroomGateProvider(t, cfgSvc, secSvc, contract.CLITypeCodex)
		plan, failure := p.BuildPlan(context.Background(), headroomBuildRequest(contract.CLITypeCodex, true))
		if failure == nil || failure.Kind != launchplan.FailureCapability {
			t.Fatalf("codex headroom + WSL must be rejected regardless of networking mode (mode=%q), got plan=%v failure=%+v", mode, plan, failure)
		}
	}
}

// D6 remote 链：非 WSL codex 会话维持既有行为（UseHeadroom 照旧加
// HeadroomStart effect，不因本闸门改变）。
func TestBuildCodexPlanHeadroomNonWSLUnaffected(t *testing.T) {
	p, cfgSvc, secSvc := newHeadroomGatePlanner(t, fakeCLIResolver{}, "")
	saveHeadroomGateProvider(t, cfgSvc, secSvc, contract.CLITypeCodex)
	plan, failure := p.BuildPlan(context.Background(), headroomBuildRequest(contract.CLITypeCodex, true))
	if failure != nil {
		t.Fatalf("non-WSL codex headroom session must keep existing behavior: %+v", failure)
	}
	if !hasEffectKind(plan, launchplan.EffectHeadroomStart) {
		t.Fatalf("plan must keep the codex headroom effect: %+v", plan.Effects)
	}
}

// 引用保持：headroom 包端口常量在闸门文案中被钉定（8787），防止文案与实际
// 监听端口漂移。
func TestHeadroomGatePortMatchesClaudeDefault(t *testing.T) {
	if !strings.Contains(headroomWSLNetworkAdmission(true, []string{"USERPROFILE=" + t.TempDir()}).Error(),
		"127.0.0.1:"+strconv.Itoa(headroom.DefaultPort)) {
		t.Fatalf("gate text must pin the claude headroom port %d", headroom.DefaultPort)
	}
}
