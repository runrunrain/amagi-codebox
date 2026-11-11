package main

// agentcli_e2e_test.go — agent-CLI 全链路集成测试（Wave 2 C2）：真实
// agentcli.Server + 真实 cmds.Table() + 真实 agentCLIHost（App 夹具全部指向
// 临时目录，绝不触碰真实 ~/.amagi-codebox 与 ~/.agents/~/.claude/~/.codex）
// + 真实 client.Run，在进程内跑完整链路（argv → 本地预检 → 端点发现 → 探活
// → 执行 → Envelope 输出）。

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/agentcli"
	"amagi-codebox/internal/agentcli/client"
	"amagi-codebox/internal/agentcli/cmds"
	"amagi-codebox/internal/agentcli/install"
	"amagi-codebox/internal/agentcli/spec"
	"amagi-codebox/internal/config"
	"amagi-codebox/internal/envvars"
	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/paths"
	"amagi-codebox/internal/pty"
	"amagi-codebox/internal/session"
	"amagi-codebox/internal/settings"
)

// agentCLIE2EFixture 聚合夹具句柄：App（真实服务）+ handler 调用计数器 +
// 汇总的全部 CLI 输出（用于终局脱敏断言）。
type agentCLIE2EFixture struct {
	app      *App
	recorder *agentCLIHandlerRecorder
	logs     bytes.Buffer // 所有 stdout+stderr 的并集
}

// agentCLIHandlerRecorder 包装真实 Handler 统计调用次数（验证"缺 --yes 不
// 触达 handler"这类先于网络的本地预检）。
type agentCLIHandlerRecorder struct {
	mu    sync.Mutex
	calls map[string]int
}

func (r *agentCLIHandlerRecorder) wrap(name string, h spec.Handler) spec.Handler {
	return func(ctx context.Context, args spec.Args) (any, error) {
		r.mu.Lock()
		r.calls[name]++
		r.mu.Unlock()
		return h(ctx, args)
	}
}

func (r *agentCLIHandlerRecorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[name]
}

// newAgentCLIE2E 构造全链路夹具：临时 configDir（配置/端点/审计全在其中）、
// 临时 userHome（shim 与技能目标全在其中）、内存密钥库、真实
// Settings/EnvVars/Config/Secrets 服务 + 已启动的 agentcli.Server。
func newAgentCLIE2E(t *testing.T) *agentCLIE2EFixture {
	t.Helper()
	configDir := t.TempDir()
	userHome := t.TempDir()
	logSvc := logging.NewService(configDir)
	t.Cleanup(logSvc.Close)

	cfgSvc := config.NewConfigService(configDir)
	if err := cfgSvc.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	envVarsSvc := envvars.NewEnvVarsService(configDir)
	if err := envVarsSvc.Load(); err != nil {
		t.Fatalf("load envvars: %v", err)
	}
	settingsSvc := settings.NewService(configDir)
	if err := settingsSvc.Load(); err != nil {
		t.Fatalf("load settings: %v", err)
	}

	app := &App{
		Log:       logSvc,
		Config:    cfgSvc,
		Secrets:   newTestSecretsService(t, configDir),
		Sessions:  session.NewManager(),
		Launcher:  launcher.NewLauncherService(logSvc, envVarsSvc),
		Pty:       pty.NewService(logSvc),
		EnvVars:   envVarsSvc,
		Paths:     paths.NewPathsService(configDir),
		Settings:  settingsSvc,
		configDir: configDir,
		// 关闭 harness 同步：夹具未装配 OpenCodeConfig/PiConfig/OmpConfig，
		// 且绝不允许 provider 保存路径写真实 ~/.pi / ~/.omp / OpenCode 配置。
		providerSyncEnabled: false,
	}
	// 安装管理器：home 与技能目标全部指向临时目录；exePath 用假路径（本测试
	// 不执行 shim）。
	app.agentCLIInstall = install.New(configDir, userHome, filepath.Join(configDir, "testbin", "codebox-app"), cmds.Table())

	// 真实 Server + 真实 host Handler（包一层计数器）。
	rec := &agentCLIHandlerRecorder{calls: map[string]int{}}
	handlers := newAgentCLIHost(app).handlers()
	for name, h := range handlers {
		handlers[name] = rec.wrap(name, h)
	}
	app.agentCLI = agentcli.NewServer(configDir, resolveAppVersion(), cmds.Table(), handlers, logSvc)
	if err := app.agentCLI.Start(); err != nil {
		t.Fatalf("start agent-cli server: %v", err)
	}
	t.Cleanup(app.agentCLI.Stop)

	return &agentCLIE2EFixture{app: app, recorder: rec}
}

// run 运行一次真实客户端：全局 --home 钉到夹具 configDir（不依赖
// AMAGI_CODEBOX_HOME / 真实 home），返回退出码与 stdout/stderr。
func (f *agentCLIE2EFixture) run(t *testing.T, stdin string, argv ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := client.Run(t.Context(), append([]string{"--home", f.app.configDir}, argv...),
		strings.NewReader(stdin), &out, &errOut, cmds.Table(), resolveAppVersion())
	f.logs.Write(out.Bytes())
	f.logs.Write(errOut.Bytes())
	return code, out.String(), errOut.String()
}

// runJSON 运行一次 --json 客户端并解析 stdout 包络（--json 下 stdout 只应
// 有一个 Envelope JSON）。全局 --json 放在命令词之前：与参数旗标同名的
// 全局旗标在命令词之后会优先按参数解析（见 client.extractGlobals）。
func (f *agentCLIE2EFixture) runJSON(t *testing.T, stdin string, argv ...string) (int, spec.Envelope, string) {
	t.Helper()
	code, out, errOut := f.run(t, stdin, append([]string{"--json"}, argv...)...)
	var env spec.Envelope
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("stdout 不是可解析的 Envelope JSON：%v\nstdout=%q\nstderr=%q", jerr, out, errOut)
	}
	return code, env, errOut
}

// agentCLITokenForTest 直读端点文件取真实令牌（仅测试断言用；CLI 输出绝不
// 应包含它）。
func agentCLITokenForTest(t *testing.T, configDir string) (string, []byte) {
	t.Helper()
	path := filepath.Join(configDir, "run", "endpoint.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读端点文件：%v", err)
	}
	var ep spec.Endpoint
	if err := json.Unmarshal(raw, &ep); err != nil {
		t.Fatalf("解析端点文件：%v", err)
	}
	return ep.CLIToken, raw
}

// TestAgentCLIEndToEnd 全链路：read/mutate/exec/destructive 命令经真实客户端
// → 真实 HTTP 服务端 → 真实 App 服务执行，校验退出码、包络与全程脱敏。
func TestAgentCLIEndToEnd(t *testing.T) {
	f := newAgentCLIE2E(t)
	app := f.app
	token, endpointRaw := agentCLITokenForTest(t, app.configDir)
	const testAPIKey = "sk-agentcli-e2e-PLAINTEXT-9f1e"

	// ---- status（read，--json 包络可解析）----
	code, env, _ := f.runJSON(t, "", "status")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("status --json：code=%d ok=%v err=%+v", code, env.OK, env.Error)
	}
	st, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("status data 应为对象：%#v", env.Data)
	}
	agentCli, ok := st["agentCli"].(map[string]any)
	if !ok {
		t.Fatalf("status 应含 agentCli 段：%#v", st)
	}
	if agentCli["running"] != true || agentCli["url"] == "" || agentCli["enabled"] != true {
		t.Fatalf("agentCli 段运行态异常：%#v", agentCli)
	}

	// ---- cli info（install 状态 + 服务运行态，无令牌）----
	code, env, _ = f.runJSON(t, "", "cli", "info")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("cli info：code=%d err=%+v", code, env.Error)
	}
	info, _ := env.Data.(map[string]any)
	shim, _ := info["shim"].(map[string]any)
	if shim == nil || !strings.HasSuffix(shim["path"].(string), "codebox-agent") || shim["status"] == "" {
		t.Fatalf("cli info 应含 shim 安装状态：%#v", info)
	}
	server, _ := info["server"].(map[string]any)
	if server == nil || server["running"] != true {
		t.Fatalf("cli info 应含服务运行态：%#v", info)
	}
	skills, _ := info["skills"].(map[string]any)
	if len(skills) != 3 {
		t.Fatalf("cli info 应含三个技能目标：%#v", skills)
	}

	// ---- session list（空）----
	code, env, _ = f.runJSON(t, "", "session", "list")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("session list：code=%d err=%+v", code, env.Error)
	}
	if list, ok := env.Data.([]any); !ok || len(list) != 0 {
		t.Fatalf("session list 应为空数组：%#v", env.Data)
	}

	// ---- provider：list（空）→ save → key-set（stdin）→ get → list → delete（--yes）----
	code, env, _ = f.runJSON(t, "", "provider", "list")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("provider list：code=%d err=%+v", code, env.Error)
	}
	if pl, _ := env.Data.(map[string]any); pl["count"] != float64(0) {
		t.Fatalf("provider list 初始应为空：%#v", env.Data)
	}
	code, env, _ = f.runJSON(t, "", "provider", "save", "acme",
		"--json", `{"defaultModel":"m-1","openai":{"enabled":true,"baseUrl":"https://unit-test.example/v1"}}`)
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("provider save：code=%d err=%+v", code, env.Error)
	}
	// 密钥经 stdin（Secret 参数），明文绝不出现在 argv。
	code, env, _ = f.runJSON(t, testAPIKey, "provider", "key-set", "acme", "--key", "-")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("provider key-set：code=%d err=%+v", code, env.Error)
	}
	code, env, _ = f.runJSON(t, "", "provider", "get", "acme")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("provider get：code=%d err=%+v", code, env.Error)
	}
	pv, _ := env.Data.(map[string]any)
	if pv["name"] != "acme" {
		t.Fatalf("provider get 应返回 acme：%#v", env.Data)
	}
	keyView, _ := pv["key"].(map[string]any)
	if keyView == nil || keyView["hasStored"] != true || strings.Contains(keyView["maskedKey"].(string), testAPIKey) {
		t.Fatalf("provider get 密钥状态应为脱敏视图：%#v", keyView)
	}
	// 含密钥字段的 JSON 必须被拒绝。
	code, env, _ = f.runJSON(t, "", "provider", "save", "bad", "--json", `{"apiKey":"`+testAPIKey+`"}`)
	if code != spec.ExitUsage || env.OK || env.Error.Code != spec.ErrBadArgs {
		t.Fatalf("provider save 含密钥字段应拒绝（exit 2 / bad_args）：code=%d env=%+v", code, env)
	}
	code, env, _ = f.runJSON(t, "", "provider", "list")
	if code != spec.ExitOK || env.Data.(map[string]any)["count"] != float64(1) {
		t.Fatalf("provider list 应有 1 个：%+v", env)
	}
	code, env, _ = f.runJSON(t, "", "provider", "delete", "acme", "--yes")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("provider delete --yes：code=%d err=%+v", code, env.Error)
	}
	code, env, _ = f.runJSON(t, "", "provider", "get", "acme")
	if code != spec.ExitBusiness || env.OK || env.Error.Code != spec.ErrNotFound {
		t.Fatalf("删除后 provider get 应 not_found（exit 1）：code=%d env=%+v", code, env)
	}

	// ---- settings：get → set（白名单键）→ set（禁改键被拒，exit 2）----
	code, env, _ = f.runJSON(t, "", "settings", "get")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("settings get：code=%d err=%+v", code, env.Error)
	}
	code, env, _ = f.runJSON(t, "", "settings", "set", "dashboard.provider", "acme")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("settings set：code=%d err=%+v", code, env.Error)
	}
	for _, forbidden := range []string{"remoteport", "agentclienabled", "githubtoken"} {
		code, env, _ = f.runJSON(t, "", "settings", "set", forbidden, "1")
		if code != spec.ExitUsage || env.OK || env.Error.Code != spec.ErrForbiddenCommand {
			t.Fatalf("settings set %s 应 forbidden_command（exit 2）：code=%d env=%+v", forbidden, code, env)
		}
	}

	// ---- envvars：set → list → delete（--yes）----
	code, env, _ = f.runJSON(t, "", "envvars", "set", "AGENTCLI_E2E", "hello")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("envvars set：code=%d err=%+v", code, env.Error)
	}
	code, env, _ = f.runJSON(t, "", "envvars", "list")
	if code != spec.ExitOK || env.Data.(map[string]any)["count"] != float64(1) {
		t.Fatalf("envvars list 应有 1 个：%+v", env)
	}
	code, env, _ = f.runJSON(t, "", "envvars", "delete", "AGENTCLI_E2E", "--yes")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("envvars delete --yes：code=%d err=%+v", code, env.Error)
	}

	// ---- workdir：list → add → remove ----
	code, env, _ = f.runJSON(t, "", "workdir", "list")
	if code != spec.ExitOK || !env.OK {
		t.Fatalf("workdir list：code=%d err=%+v", code, env.Error)
	}
	workdir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	code, env, _ = f.runJSON(t, "", "workdir", "add", workdir)
	if code != spec.ExitOK || env.Data.(map[string]any)["count"] != float64(1) {
		t.Fatalf("workdir add：%+v", env)
	}
	code, env, _ = f.runJSON(t, "", "workdir", "remove", workdir)
	if code != spec.ExitOK || env.Data.(map[string]any)["count"] != float64(0) {
		t.Fatalf("workdir remove：%+v", env)
	}

	// ---- 人类可读模式冒烟（status 渲染不 panic、不含令牌）----
	code, human, humanErr := f.run(t, "", "status")
	if code != spec.ExitOK || strings.TrimSpace(human) == "" {
		t.Fatalf("status 人类模式：code=%d stdout=%q stderr=%q", code, human, humanErr)
	}

	// ---- 缺 --yes：本地确认预检先于任何网络，退出 4 且不触达 handler ----
	before := f.recorder.count("session.stop")
	code, env, _ = f.runJSON(t, "", "session", "stop", "sess-x")
	if code != spec.ExitConfirm || env.OK || env.Error.Code != spec.ErrConfirmationRequired {
		t.Fatalf("缺 --yes 应 exit 4：code=%d env=%+v", code, env)
	}
	if got := f.recorder.count("session.stop"); got != before {
		t.Fatalf("缺 --yes 不应触达 handler（session.stop 调用 %d 次）", got)
	}

	// ---- 令牌错误 → 3 ----
	corrupt := spec.Endpoint{}
	if err := json.Unmarshal(endpointRaw, &corrupt); err != nil {
		t.Fatal(err)
	}
	corrupt.CLIToken = "definitely-wrong-token"
	bad, err := json.Marshal(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(filepath.Join(app.configDir, "run", "endpoint.json"), bad, 0o600); werr != nil {
		t.Fatal(werr)
	}
	code, env, _ = f.runJSON(t, "", "status")
	if code != spec.ExitUnreachable || env.OK || env.Error.Code != "unreachable" {
		t.Fatalf("令牌错误应 exit 3：code=%d env=%+v", code, env)
	}
	// 还原真实端点文件。
	if werr := os.WriteFile(filepath.Join(app.configDir, "run", "endpoint.json"), endpointRaw, 0o600); werr != nil {
		t.Fatal(werr)
	}

	// ---- 禁用后 → 3 ----
	if err := app.SetAgentCLIEnabled(false); err != nil {
		t.Fatalf("SetAgentCLIEnabled(false)：%v", err)
	}
	code, env, _ = f.runJSON(t, "", "status")
	if code != spec.ExitUnreachable || env.OK {
		t.Fatalf("禁用后应 exit 3：code=%d env=%+v", code, env)
	}

	// ---- 终局脱敏断言：全程输出不含令牌与测试密钥明文 ----
	all := f.logs.String()
	if strings.Contains(all, token) {
		t.Fatal("CLI 全程输出泄漏了服务端令牌")
	}
	if strings.Contains(all, testAPIKey) {
		t.Fatal("CLI 全程输出泄漏了测试 API Key 明文")
	}

	// ---- 审计日志：mutate 命令留痕且不含参数值 ----
	auditRaw, err := os.ReadFile(filepath.Join(app.configDir, "logs", "agent-cli-audit.jsonl"))
	if err != nil {
		t.Fatalf("读审计日志：%v", err)
	}
	audit := string(auditRaw)
	if !strings.Contains(audit, "provider.key-set") || !strings.Contains(audit, "provider.delete") {
		t.Fatal("审计日志应记录非 read 命令")
	}
	if strings.Contains(audit, testAPIKey) || strings.Contains(audit, token) {
		t.Fatal("审计日志不得包含参数值或令牌")
	}
}

// TestAgentCLIFacadeInstallLifecycle 门面安装/清理闭环（真实 Manager + 临时
// home/userHome）：Install → 状态 up-to-date → 文件落盘 → Remove → 状态
// not-installed → 文件消失；非法目标报错。
func TestAgentCLIFacadeInstallLifecycle(t *testing.T) {
	f := newAgentCLIE2E(t)
	app := f.app

	// 初始：全部未安装。
	status := app.GetAgentCLIInstallStatus()
	if status.ShimStatus != string(install.StateNotInstalled) || status.CommandCount != len(cmds.Table()) {
		t.Fatalf("初始状态异常：%+v", status)
	}
	if status.Command == "" || !strings.Contains(status.Command, "codebox-agent") {
		t.Fatalf("shim 调用名异常：%q", status.Command)
	}
	agentSkill := agentCLISkillStatus(t, status, "agents")
	if agentSkill.Status != string(install.StateNotInstalled) {
		t.Fatalf("agents 技能初始应未安装：%+v", status.Skills)
	}
	if agentSkill.Path == "" {
		t.Fatal("agents 技能路径不应为空")
	}

	// 安装 shim 与 agents 技能。
	status, err := app.InstallAgentCLIShim()
	if err != nil || status.ShimStatus != string(install.StateUpToDate) {
		t.Fatalf("InstallAgentCLIShim：%v %+v", err, status)
	}
	if _, serr := os.Stat(status.ShimPath); serr != nil {
		t.Fatalf("shim 文件应存在：%v", serr)
	}
	status, err = app.InstallAgentCLISkill("agents")
	if err != nil {
		t.Fatalf("InstallAgentCLISkill(agents)：%v", err)
	}
	agentSkill = agentCLISkillStatus(t, status, "agents")
	if agentSkill.Status != string(install.StateUpToDate) {
		t.Fatalf("agents 技能应 up-to-date：%+v", status.Skills)
	}
	skillDir := agentSkill.Path // SkillStatus 的 Path 即技能目录
	if _, serr := os.Stat(filepath.Join(skillDir, "SKILL.md")); serr != nil {
		t.Fatalf("SKILL.md 应存在：%v", serr)
	}
	if _, serr := os.Stat(filepath.Join(skillDir, "references", "commands.md")); serr != nil {
		t.Fatalf("references/commands.md 应存在：%v", serr)
	}

	// 非法目标。
	if _, err = app.InstallAgentCLISkill("unknown"); err == nil {
		t.Fatal("非法技能目标应报错")
	}

	// 清理：文件消失、状态回到 not-installed。
	status, err = app.RemoveAgentCLISkill("agents")
	if err != nil || agentCLISkillStatus(t, status, "agents").Status != string(install.StateNotInstalled) {
		t.Fatalf("RemoveAgentCLISkill：%v %+v", err, status.Skills)
	}
	status, err = app.RemoveAgentCLIShim()
	if err != nil || status.ShimStatus != string(install.StateNotInstalled) {
		t.Fatalf("RemoveAgentCLIShim：%v %+v", err, status)
	}
}

// TestAgentCLIInstallDefaultsRespectsDeclined Startup 默认安装：清理过的面
// 不再自动装回，未清理的面正常安装。
func TestAgentCLIInstallDefaultsRespectsDeclined(t *testing.T) {
	f := newAgentCLIE2E(t)
	app := f.app

	if _, err := app.RemoveAgentCLIShim(); err != nil {
		t.Fatalf("RemoveAgentCLIShim：%v", err)
	}
	app.installAgentCLIDefaults()

	status := app.GetAgentCLIInstallStatus()
	if status.ShimStatus != string(install.StateNotInstalled) {
		t.Fatalf("用户清理过的 shim 不应被默认装回：%+v", status)
	}
	agentSkill := agentCLISkillStatus(t, status, "agents")
	if agentSkill.Status != string(install.StateUpToDate) {
		t.Fatalf("未清理的 agents 技能应默认安装：%+v", status.Skills)
	}

	// 命令行访问关闭时不做默认安装。
	if _, err := app.RemoveAgentCLISkill("agents"); err != nil {
		t.Fatalf("RemoveAgentCLISkill：%v", err)
	}
	if err := app.SetAgentCLIEnabled(false); err != nil {
		t.Fatal(err)
	}
	app.installAgentCLIDefaults()
	status = app.GetAgentCLIInstallStatus()
	if agentCLISkillStatus(t, status, "agents").Status != string(install.StateNotInstalled) {
		t.Fatalf("AgentCLIEnabled=false 时不应默认安装：%+v", status.Skills)
	}
}

func agentCLISkillStatus(t *testing.T, status AgentCLIInstallStatus, target string) AgentCLISkillInstallStatus {
	t.Helper()
	for _, s := range status.Skills {
		if s.Target == target {
			return s
		}
	}
	t.Fatalf("状态中缺少技能目标 %s：%+v", target, status.Skills)
	return AgentCLISkillInstallStatus{}
}

// TestAgentCLIBinaryHelpSmoke 构建本仓库二进制后执行 `cli` 子命令冒烟：
// `--help` 退出 0；`session stop x` 在应用未运行时仍先命中本地确认预检
// （退出 4，不触达网络）。构建过慢或 cgo 不可用时跳过并说明。
func TestAgentCLIBinaryHelpSmoke(t *testing.T) {
	// Keep the unreachable-endpoint check isolated from any running local app.
	t.Setenv("AMAGI_CODEBOX_HOME", t.TempDir())
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("跳过：未找到 go 工具链（%v）", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Skipf("跳过：无法定位仓库根（%v）", err)
	}
	bin := filepath.Join(t.TempDir(), "codebox-smoke")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, goTool, "build", "-o", bin, ".")
	build.Dir = cwd
	out, err := build.CombinedOutput()
	if err != nil {
		t.Skipf("跳过：构建本仓库二进制失败（可能构建过慢或 cgo 不可用）：%v\n%s", err, out)
	}

	runBin := func(args ...string) (int, string) {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		cmd := exec.CommandContext(cctx, bin, args...)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if rerr := cmd.Run(); rerr != nil {
			if exitErr, ok := rerr.(*exec.ExitError); ok {
				return exitErr.ExitCode(), buf.String()
			}
			t.Fatalf("执行 %v：%v", args, rerr)
		}
		return 0, buf.String()
	}

	// cli --help：本地命令，不连服务 → 0。
	code, helpOut := runBin("cli", "--help")
	if code != 0 || !strings.Contains(helpOut, "codebox-agent") {
		t.Fatalf("cli --help：code=%d out=%q", code, helpOut)
	}
	// cli session stop x：缺 --yes 且应用未运行 → 本地确认预检先于端点发现 → 4。
	code, stopOut := runBin("cli", "session", "stop", "x")
	if code != spec.ExitConfirm {
		t.Fatalf("cli session stop x（无 --yes，应用未运行）应退出 %d，实际 %d：%s", spec.ExitConfirm, code, stopOut)
	}
	// cli status：应用未运行 → 不可达 → 3。
	code, statusOut := runBin("cli", "status")
	if code != spec.ExitUnreachable {
		t.Fatalf("cli status（应用未运行）应退出 %d，实际 %d：%s", spec.ExitUnreachable, code, statusOut)
	}
}

// ---- D-08 回归：命令表全部示例经真实客户端解析器本地预检 ----

// TestAgentCLIExamplesParseLocally 对 cmds.Table() 全部示例做本地预检：示例
// 经真实 client 解析器跑通命令词/参数/枚举/确认检查（--home 指向不存在的
// 目录，端点发现必然失败，不连接任何服务）。用法可解析的退出码只能是
// 3（不可达）或 4（需确认）；出现 2（用法错误）即示例不可执行。
// 修复前 preset 两例的 claudecode 拼写与 usage 四例的 camelCase 旗标均为 2。
func TestAgentCLIExamplesParseLocally(t *testing.T) {
	home := filepath.Join(t.TempDir(), "definitely-not-created")
	for _, c := range cmds.Table() {
		for i, ex := range c.Examples {
			argv := agentCLIExampleArgv(t, ex)
			var out, errOut bytes.Buffer
			code := client.Run(context.Background(), append([]string{"--home", home}, argv...),
				strings.NewReader("synthetic-stdin"), &out, &errOut, cmds.Table(), "test")
			if code != spec.ExitUnreachable && code != spec.ExitConfirm {
				t.Errorf("%s 示例[%d] 不可本地解析（exit %d）：%s\nstdout=%s\nstderr=%s",
					c.Name(), i, code, ex, out.String(), errOut.String())
			}
		}
	}
}

// agentCLIExampleArgv 把示例行转为 argv：取最末段管道（`echo x | codebox-agent
// …` 只保留 CLI 命令）、剥 codebox-agent 前缀、丢弃 `<<< "$VAR"` here-string
// （stdin 由合成输入替代），按 shell 引号规则分词。
func agentCLIExampleArgv(t *testing.T, example string) []string {
	t.Helper()
	seg := example
	if idx := strings.LastIndex(seg, "|"); idx >= 0 {
		seg = seg[idx+1:]
	}
	tokens := agentCLIShlex(t, seg)
	argv := make([]string, 0, len(tokens))
	skipNext := false
	droppedProgram := false
	for _, tok := range tokens {
		if skipNext {
			skipNext = false
			continue
		}
		if tok == "<<<" {
			skipNext = true
			continue
		}
		if !droppedProgram {
			if tok != "codebox-agent" {
				t.Fatalf("示例不以 codebox-agent 开头：%q", example)
			}
			droppedProgram = true
			continue
		}
		argv = append(argv, tok)
	}
	if len(argv) == 0 {
		t.Fatalf("示例没有可解析的参数：%q", example)
	}
	return argv
}

// agentCLIShlex 最小 shell 分词器：支持单引号/双引号（含 \" 与 \\ 转义）与
// 反斜杠转义；示例只用到这些形态。
func agentCLIShlex(t *testing.T, s string) []string {
	t.Helper()
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t':
			flush()
			i++
		case r == '\'':
			i++
			for i < len(runes) && runes[i] != '\'' {
				cur.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				t.Fatalf("示例引号未闭合：%q", s)
			}
			i++
		case r == '"':
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) {
					cur.WriteRune(runes[i+1])
					i += 2
					continue
				}
				cur.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				t.Fatalf("示例引号未闭合：%q", s)
			}
			i++
		case r == '\\' && i+1 < len(runes):
			cur.WriteRune(runes[i+1])
			i += 2
		default:
			cur.WriteRune(r)
			i++
		}
	}
	flush()
	return tokens
}
