package main

// launch_webui_plane_test.go — remote 链 Web 平面回归：
//   - pi（remote-webui-plane）：remote v1 创建/重启的 embedded pi 会话必须在
//     启动链注入 AMAGI_WEBUI_PORT/AMAGI_WEBUI_TOKEN env，并在 commit 后注册
//     webui tracker（对齐桌面 LaunchPiSession T-1.5）；
//   - claudecode（claude-webui design §5.4/§11）：planner 注入 --session-id 并
//     携带 WebUIPlane 材料，executor 不注 AMAGI_WEBUI_* env，commit 后依次
//     RecordClaudeSessionID → Start（PlaneConfig 含 sid/workDir/transcriptRoot）
//     → RegisterTracker；Start 失败仅记日志不注册；
//   - 非 pi/claudecode 会话（codex/omp）不注入不注册；WSL 目标不置平面字段。
//   Abort 路径不得残留任何注册或平面调用。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"amagi-codebox/internal/claudewebui"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/processcap"
	"amagi-codebox/internal/remote/contract"
)

// --- 测试替身 ---

type webuiRegistration struct {
	sessionID string
	pid       int
	port      int
	token     string
}

// webuiPlaneSpy 记录 PrepareEnv 分配与 RegisterSession 调用。
type webuiPlaneSpy struct {
	mu            sync.Mutex
	prepareCalls  int
	preparePort   int
	prepareToken  string
	registrations []webuiRegistration
}

func (s *webuiPlaneSpy) PrepareEnv() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepareCalls++
	s.preparePort = 47000 + s.prepareCalls
	s.prepareToken = fmt.Sprintf("tok-%d", s.prepareCalls)
	return s.preparePort, s.prepareToken
}

func (s *webuiPlaneSpy) RegisterSession(sessionID string, pid, port int, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrations = append(s.registrations, webuiRegistration{
		sessionID: sessionID, pid: pid, port: port, token: token,
	})
}

func (s *webuiPlaneSpy) snapshot() (int, int, string, []webuiRegistration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	regs := append([]webuiRegistration(nil), s.registrations...)
	return s.prepareCalls, s.preparePort, s.prepareToken, regs
}

// claudeWebUIPlaneSpy 记录 claudeWebUILaunchPlane 三方法的调用与顺序
// （order 逐次追加 "record"/"start"/"register"，供「依次各一次」断言）。
type claudeWebUIPlaneSpy struct {
	mu       sync.Mutex
	order    []string
	recorded []string
	starts   []claudewebui.PlaneConfig
	trackers []webuiRegistration
	// startErr 非 nil 时 Start 返回该错误（Start 失败降级路径）。
	startErr error
}

func (s *claudeWebUIPlaneSpy) RecordClaudeSessionID(_ string, claudeSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, "record")
	s.recorded = append(s.recorded, claudeSessionID)
}

func (s *claudeWebUIPlaneSpy) Start(cfg claudewebui.PlaneConfig) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, "start")
	s.starts = append(s.starts, cfg)
	if s.startErr != nil {
		return 0, s.startErr
	}
	return 47200 + len(s.starts), nil
}

func (s *claudeWebUIPlaneSpy) RegisterTracker(sessionID string, pid, port int, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, "register")
	s.trackers = append(s.trackers, webuiRegistration{
		sessionID: sessionID, pid: pid, port: port, token: token,
	})
}

func (s *claudeWebUIPlaneSpy) snapshot() (order []string, recorded []string, starts []claudewebui.PlaneConfig, trackers []webuiRegistration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...), append([]string(nil), s.recorded...),
		append([]claudewebui.PlaneConfig(nil), s.starts...), append([]webuiRegistration(nil), s.trackers...)
}

// findSessionIDArg 从 argv 中取 "--session-id" 的参数值（不存在返回 ""）。
func findSessionIDArg(args []string) string {
	for i, arg := range args {
		if arg == "--session-id" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

type fixedTestBinding struct{ id processcap.BindingID }

func (b fixedTestBinding) BindingID() processcap.BindingID { return b.id }
func (b fixedTestBinding) CloseExact(context.Context) processcap.ExactCloseEvidence {
	return processcap.ExactCloseEvidence{}
}

// webuiPlanePTY 捕获送入 PTY 启动的 ResolvedLaunchSpec。
type webuiPlanePTY struct {
	mu      sync.Mutex
	specs   []platform.ResolvedLaunchSpec
	nextPID int
}

func (p *webuiPlanePTY) StartResolvedWithRunEvidence(_ string, spec platform.ResolvedLaunchSpec, _ any) (processcap.StartEvidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.specs = append(p.specs, spec)
	p.nextPID++
	binding := fixedTestBinding{id: processcap.BindingID{
		Kind:       processcap.BackendPTY,
		Owner:      uint64(p.nextPID + 100),
		Generation: uint64(p.nextPID),
	}}
	return processcap.StartEvidence{PID: 2000 + p.nextPID, Binding: binding}, nil
}

func (p *webuiPlanePTY) WaitReadyForBinding(context.Context, string, processcap.BindingID) error {
	return nil
}

func (p *webuiPlanePTY) WriteRawForBinding(context.Context, string, processcap.BindingID, []byte) error {
	return nil
}

func (p *webuiPlanePTY) capturedSpecs() []platform.ResolvedLaunchSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]platform.ResolvedLaunchSpec(nil), p.specs...)
}

// unsetInheritedWebUIEnv 隔离宿主自身的 AMAGI_WEBUI_*（CodeBox 从 pi 会话
// 内启动 go test 时会继承），保证断言确定性。
func unsetInheritedWebUIEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"AMAGI_WEBUI_PORT", "AMAGI_WEBUI_TOKEN"} {
		if old, ok := os.LookupEnv(key); ok {
			os.Unsetenv(key)
			t.Cleanup(func() { os.Setenv(key, old) })
		}
	}
}

// prepareAndApplyPTY 只 Prepare + Apply PTY 启动效果（跳过 config mutation /
// bootstrap 的落盘副作用），返回 prepared execution 供 commit/abort 断言。
// webui/claudePlane 分别是 pi 与 claudecode 的平面接线（nil = 未接线）。
func prepareAndApplyPTY(t *testing.T, cliType contract.CLIType, pty ptyStartPort, webui webuiLaunchPlane, claudePlane claudeWebUILaunchPlane, sessionID string) launchplan.PreparedExecution {
	t.Helper()
	unsetInheritedWebUIEnv(t)
	planner, _, _, _ := testPlannerSetup(t, cliType, "webui-provider", "webui-key-123456789")
	plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
		CLIType: cliType, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
	})
	if failure != nil {
		t.Fatalf("BuildPlan: %v", failure)
	}
	t.Cleanup(plan.Secrets.Dispose)
	exec := newAppLaunchExecutor(launchExecutorDeps{pty: pty, webui: webui, claudeWebUI: claudePlane})
	prepared, err := exec.Prepare(context.Background(), plan, launchplan.ExecutionBinding{
		SessionID: sessionID, RunEpoch: 1, RunHandle: "run",
	})
	if err != nil {
		plan.Secrets.Dispose()
		t.Fatalf("Prepare: %v", err)
	}
	for i := 0; i < prepared.Count(); i++ {
		eff := prepared.Effect(i)
		if eff.Kind() != launchplan.EffectPTYStart {
			continue
		}
		eff.ArmOwnership()
		if _, applyErr := eff.Apply(context.Background()); applyErr != nil {
			plan.Secrets.Dispose()
			t.Fatalf("PTY effect Apply: %v", applyErr)
		}
	}
	return prepared
}

// --- 回归测试 ---

// TestExecutorPiWebUIPlaneInjectedAndRegistered：embedded pi 会话——env 注入
// （AMAGI_WEBUI_PORT/AMAGI_WEBUI_TOKEN）+ MarkCommitted 后 RegisterSession
// （pid 来自 StartEvidence）。
func TestExecutorPiWebUIPlaneInjectedAndRegistered(t *testing.T) {
	pty := &webuiPlanePTY{}
	spy := &webuiPlaneSpy{}
	prepared := prepareAndApplyPTY(t, contract.CLITypePi, pty, spy, nil, "webui-pi-s1")

	specs := pty.capturedSpecs()
	if len(specs) != 1 {
		t.Fatalf("PTY start specs = %d, want 1", len(specs))
	}
	calls, port, token, regs := spy.snapshot()
	if calls != 1 {
		t.Fatalf("PrepareEnv calls = %d, want 1", calls)
	}
	if !envContainsExact(specs[0].Env.Variables, "AMAGI_WEBUI_PORT="+fmt.Sprint(port)) {
		t.Fatalf("resolved env missing AMAGI_WEBUI_PORT=%d: %v", port, specs[0].Env.Variables)
	}
	if !envContainsExact(specs[0].Env.Variables, "AMAGI_WEBUI_TOKEN="+token) {
		t.Fatalf("resolved env missing AMAGI_WEBUI_TOKEN: %v", specs[0].Env.Variables)
	}
	if len(regs) != 0 {
		t.Fatalf("commit 前不应注册 tracker，got %+v", regs)
	}

	prepared.MarkCommitted()
	_, _, _, regs = spy.snapshot()
	if len(regs) != 1 {
		t.Fatalf("MarkCommitted 后应注册 1 次，got %+v", regs)
	}
	wantPID := 2001 // webuiPlanePTY 首个 PID
	if regs[0].sessionID != "webui-pi-s1" || regs[0].pid != wantPID ||
		regs[0].port != port || regs[0].token != token {
		t.Fatalf("注册参数 = %+v, want {webui-pi-s1 %d %d %s}", regs[0], wantPID, port, token)
	}
}

// TestExecutorNonPiSkipsWebUIPlane：codex 与 omp 会话不注入、不注册、
// 不触碰 claude 平面接线。
func TestExecutorNonPiSkipsWebUIPlane(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cliType contract.CLIType
	}{
		{"codex", contract.CLITypeCodex},
		{"omp", contract.CLITypeOmp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pty := &webuiPlanePTY{}
			spy := &webuiPlaneSpy{}
			claudeSpy := &claudeWebUIPlaneSpy{}
			prepared := prepareAndApplyPTY(t, tc.cliType, pty, spy, claudeSpy, "webui-neg-"+tc.name)

			for _, spec := range pty.capturedSpecs() {
				for _, entry := range spec.Env.Variables {
					if strings.HasPrefix(entry, "AMAGI_WEBUI_") {
						t.Fatalf("%s 会话不应注入 webui env: %v", tc.name, spec.Env.Variables)
					}
				}
			}
			if calls, _, _, regs := spy.snapshot(); calls != 0 || len(regs) != 0 {
				t.Fatalf("%s 会话不应触碰 webui plane（prepare=%d regs=%+v）", tc.name, calls, regs)
			}
			assertClaudePlaneCalls(t, claudeSpy, 0, 0, 0)
			prepared.MarkCommitted()
			if _, _, _, regs := spy.snapshot(); len(regs) != 0 {
				t.Fatalf("%s 会话 commit 后不应注册 tracker，got %+v", tc.name, regs)
			}
			assertClaudePlaneCalls(t, claudeSpy, 0, 0, 0)
		})
	}
}

// assertClaudePlaneCalls 断言 claude 平面三方法的调用次数。
func assertClaudePlaneCalls(t *testing.T, spy *claudeWebUIPlaneSpy, wantRecord, wantStart, wantTracker int) {
	t.Helper()
	order, recorded, starts, trackers := spy.snapshot()
	if len(recorded) != wantRecord || len(starts) != wantStart || len(trackers) != wantTracker {
		t.Fatalf("claude 平面调用数 = record:%d start:%d tracker:%d, want %d/%d/%d（order=%v）",
			len(recorded), len(starts), len(trackers), wantRecord, wantStart, wantTracker, order)
	}
}

// TestExecutorClaudeWebUIPlaneInjectedAndRegistered（claude-webui design
// §5.4/§11）：embedded claude 会话——argv 含 --session-id、无 AMAGI_WEBUI_*
// env（数据面 server 在进程内，env 通道无消费方）、pi 分支不被触碰；
// MarkCommitted 后三方法依序各一次，Start 收到的 PlaneConfig 含
// sid/workDir/transcriptRoot（ClaudeConfigRoot 从进程 env 解析），RegisterTracker
// 用 Start 返回的 boundPort。
func TestExecutorClaudeWebUIPlaneInjectedAndRegistered(t *testing.T) {
	pty := &webuiPlanePTY{}
	spy := &claudeWebUIPlaneSpy{}
	piSpy := &webuiPlaneSpy{}
	prepared := prepareAndApplyPTY(t, contract.CLITypeClaudeCode, pty, piSpy, spy, "webui-claude-s1")

	specs := pty.capturedSpecs()
	if len(specs) != 1 {
		t.Fatalf("PTY start specs = %d, want 1", len(specs))
	}
	spec := specs[0]
	sid := findSessionIDArg(spec.CLI.Args)
	if sid == "" {
		t.Fatalf("claude argv 缺少 --session-id 注入: %v", spec.CLI.Args)
	}
	for _, entry := range spec.Env.Variables {
		if strings.HasPrefix(entry, "AMAGI_WEBUI_") {
			t.Fatalf("claude 会话不应注入 AMAGI_WEBUI_* env（进程内数据面无消费方）: %v", spec.Env.Variables)
		}
	}
	if calls, _, _, regs := piSpy.snapshot(); calls != 0 || len(regs) != 0 {
		t.Fatalf("claude 会话不应触碰 pi webui plane（prepare=%d regs=%+v）", calls, regs)
	}
	assertClaudePlaneCalls(t, spy, 0, 0, 0) // commit 前零调用

	prepared.MarkCommitted()

	order, recorded, starts, trackers := spy.snapshot()
	if strings.Join(order, ",") != "record,start,register" {
		t.Fatalf("commit 后三方法应依序各一次，got order=%v", order)
	}
	if len(recorded) != 1 || recorded[0] != sid {
		t.Fatalf("RecordClaudeSessionID = %v, want [%s]", recorded, sid)
	}
	if len(starts) != 1 {
		t.Fatalf("Start 调用 %d 次, want 1", len(starts))
	}
	cfg := starts[0]
	homeDir, homeErr := os.UserHomeDir() // isolatedHome 已把 HOME/USERPROFILE 指到临时目录
	if homeErr != nil {
		t.Fatalf("UserHomeDir: %v", homeErr)
	}
	wantRoot := filepath.Join(homeDir, ".claude")
	if cfg.SessionID != "webui-claude-s1" || cfg.ClaudeSessionID != sid ||
		cfg.WorkDir != spec.WorkDir || cfg.TranscriptRoot != wantRoot {
		t.Fatalf("PlaneConfig = {sid:%s workDir:%q root:%q}, want {sid:%s workDir:%q root:%q}",
			cfg.ClaudeSessionID, cfg.WorkDir, cfg.TranscriptRoot, sid, spec.WorkDir, wantRoot)
	}
	wantPID := 2001 // webuiPlanePTY 首个 PID
	if cfg.PID != wantPID || cfg.Port <= 0 || cfg.Token == "" {
		t.Fatalf("PlaneConfig PID/port/token = %d/%d/%q, want PID=%d 且 port>0、token 非空", cfg.PID, cfg.Port, cfg.Token, wantPID)
	}
	if len(trackers) != 1 {
		t.Fatalf("RegisterTracker 调用 %d 次, want 1", len(trackers))
	}
	boundPort := 47200 + 1 // spy 首次 Start 的返回值
	if trackers[0].sessionID != "webui-claude-s1" || trackers[0].pid != wantPID ||
		trackers[0].port != boundPort || trackers[0].token != cfg.Token {
		t.Fatalf("RegisterTracker 参数 = %+v, want {webui-claude-s1 %d %d <Start 返回的 token>}", trackers[0], wantPID, boundPort)
	}
}

// TestExecutorClaudeWebUIPlaneAbortLeavesNoResidue：Apply 后 Abort（补偿）
// 不得触碰 claude 平面三方法——否则 remote 前端会轮询到一个不存在的平面
// 或残留 server。
func TestExecutorClaudeWebUIPlaneAbortLeavesNoResidue(t *testing.T) {
	pty := &webuiPlanePTY{}
	spy := &claudeWebUIPlaneSpy{}
	prepared := prepareAndApplyPTY(t, contract.CLITypeClaudeCode, pty, nil, spy, "webui-claude-abort-s1")

	report := prepared.Abort(context.Background())
	_ = report // 补偿细节由既有 abort 测试覆盖；此处只关心平面零调用
	assertClaudePlaneCalls(t, spy, 0, 0, 0)
}

// TestExecutorClaudeWebUIPlaneStartFailureSkipsTracker：Start 失败仅记日志、
// 不注册 tracker（平面 unknown 降级，design §5.4/§9）；RecordClaudeSessionID
// 先于 Start，不因平面失败回退。
func TestExecutorClaudeWebUIPlaneStartFailureSkipsTracker(t *testing.T) {
	pty := &webuiPlanePTY{}
	spy := &claudeWebUIPlaneSpy{startErr: fmt.Errorf("bind: address already in use")}
	prepared := prepareAndApplyPTY(t, contract.CLITypeClaudeCode, pty, nil, spy, "webui-claude-fail-s1")

	prepared.MarkCommitted()

	order, _, _, trackers := spy.snapshot()
	if strings.Join(order, ",") != "record,start" {
		t.Fatalf("Start 失败路径应只 record→start，got order=%v", order)
	}
	if len(trackers) != 0 {
		t.Fatalf("Start 失败后不应注册 tracker，got %+v", trackers)
	}
}

// TestExecutorWebUIPlaneAbortLeavesNoTracker：Apply 后 Abort（补偿）不得注册
// tracker——否则 remote 前端会轮询到一个不存在的 webui 平面。
func TestExecutorWebUIPlaneAbortLeavesNoTracker(t *testing.T) {
	pty := &webuiPlanePTY{}
	spy := &webuiPlaneSpy{}
	prepared := prepareAndApplyPTY(t, contract.CLITypePi, pty, spy, nil, "webui-abort-s1")

	report := prepared.Abort(context.Background())
	_ = report // 补偿细节由既有 abort 测试覆盖；此处只关心无注册
	if _, _, _, regs := spy.snapshot(); len(regs) != 0 {
		t.Fatalf("Abort 后不应注册 tracker，got %+v", regs)
	}
}

// TestOverrideWebUIEnvReplacesInheritedEntries：系统 env 残留 AMAGI_WEBUI_*
// 时覆盖而非 append（重复键会使探测注册表回退发现指向错误端口）。
func TestOverrideWebUIEnvReplacesInheritedEntries(t *testing.T) {
	got := overrideWebUIEnv(
		[]string{"PATH=/bin", "AMAGI_WEBUI_PORT=58246", "AMAGI_WEBUI_TOKEN=stale"},
		[]string{"AMAGI_WEBUI_PORT=47001", "AMAGI_WEBUI_TOKEN=tok-1"},
	)
	want := []string{"PATH=/bin", "AMAGI_WEBUI_PORT=47001", "AMAGI_WEBUI_TOKEN=tok-1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("overrideWebUIEnv = %v, want %v", got, want)
	}
	if strings.Count(strings.Join(got, "|"), "AMAGI_WEBUI_") != 2 {
		t.Fatalf("应无重复 AMAGI_WEBUI_* 键: %v", got)
	}
}

// TestWebuiLaunchPlaneNilKeepsPiLaunchWorking：webui 未接线（nil）时 pi 会话
// 照常 Prepare/Apply——向后兼容测试构造。
func TestWebuiLaunchPlaneNilKeepsPiLaunchWorking(t *testing.T) {
	pty := &webuiPlanePTY{}
	prepared := prepareAndApplyPTY(t, contract.CLITypePi, pty, nil, nil, "webui-nil-s1")
	if len(pty.capturedSpecs()) != 1 {
		t.Fatalf("PTY start specs = %d, want 1", len(pty.capturedSpecs()))
	}
	prepared.MarkCommitted() // 不得 panic
}

// TestPlannerClaudeWebUIPlaneField：非 WSL 目标的 claude 计划携带
// WebUIPlane（sid 与 argv 注入值一致）；WSL 目标（launchTargetsWSLFn 预判
// 接缝打桩为 true）不注入 --session-id、不置字段——R8：字段缺位即 executor
// 不起平面。
func TestPlannerClaudeWebUIPlaneField(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wslPreGate bool
	}{
		{"native", false},
		{"wsl", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orig := launchTargetsWSLFn
			launchTargetsWSLFn = func(string, string) bool { return tc.wslPreGate }
			t.Cleanup(func() { launchTargetsWSLFn = orig })

			planner, _, _, _ := testPlannerSetup(t, contract.CLITypeClaudeCode, "webui-field-provider", "webui-key-123456789")
			plan, failure := planner.BuildPlan(context.Background(), launchplan.BuildRequest{
				CLIType: contract.CLITypeClaudeCode, Origin: launchplan.OriginRemote, Mode: launchplan.ModeEmbedded,
			})
			if failure != nil {
				t.Fatalf("BuildPlan: %v", failure)
			}
			t.Cleanup(plan.Secrets.Dispose)
			var proc *launchplan.ProcessStartSpec
			for _, eff := range plan.Effects {
				if eff.Process != nil {
					proc = eff.Process
				}
			}
			if proc == nil {
				t.Fatal("plan missing process effect")
			}
			if tc.wslPreGate {
				if proc.WebUIPlane != nil {
					t.Fatalf("WSL 目标不应置 WebUIPlane 字段: %+v", proc.WebUIPlane)
				}
				if sid := findSessionIDArg(proc.Resolved.CLI.Args); sid != "" {
					t.Fatalf("WSL 目标不应注入 --session-id: %v", proc.Resolved.CLI.Args)
				}
				return
			}
			if proc.WebUIPlane == nil || proc.WebUIPlane.ClaudeSessionID == "" {
				t.Fatalf("非 WSL 目标应携带 WebUIPlane（含 sid），got %+v", proc.WebUIPlane)
			}
			sid := findSessionIDArg(proc.Resolved.CLI.Args)
			if sid == "" || sid != proc.WebUIPlane.ClaudeSessionID {
				t.Fatalf("argv --session-id=%q 与 WebUIPlane.ClaudeSessionID=%q 应一致", sid, proc.WebUIPlane.ClaudeSessionID)
			}
		})
	}
}
