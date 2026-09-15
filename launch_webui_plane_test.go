package main

// launch_webui_plane_test.go — remote-webui-plane 回归：remote v1 创建/重启的
// embedded pi 会话必须在启动链注入 AMAGI_WEBUI_PORT/AMAGI_WEBUI_TOKEN env，
// 并在 commit 后注册 webui tracker（对齐桌面 LaunchPiSession T-1.5）；
// 非 pi 会话（codex/omp）不注入不注册。Abort 路径不得残留注册。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

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
func prepareAndApplyPTY(t *testing.T, cliType contract.CLIType, pty ptyStartPort, webui webuiLaunchPlane, sessionID string) launchplan.PreparedExecution {
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
	exec := newAppLaunchExecutor(launchExecutorDeps{pty: pty, webui: webui})
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
//（pid 来自 StartEvidence）。
func TestExecutorPiWebUIPlaneInjectedAndRegistered(t *testing.T) {
	pty := &webuiPlanePTY{}
	spy := &webuiPlaneSpy{}
	prepared := prepareAndApplyPTY(t, contract.CLITypePi, pty, spy, "webui-pi-s1")

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

// TestExecutorNonPiSkipsWebUIPlane：codex 与 omp 会话不注入、不注册。
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
			prepared := prepareAndApplyPTY(t, tc.cliType, pty, spy, "webui-neg-"+tc.name)

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
			prepared.MarkCommitted()
			if _, _, _, regs := spy.snapshot(); len(regs) != 0 {
				t.Fatalf("%s 会话 commit 后不应注册 tracker，got %+v", tc.name, regs)
			}
		})
	}
}

// TestExecutorWebUIPlaneAbortLeavesNoTracker：Apply 后 Abort（补偿）不得注册
// tracker——否则 remote 前端会轮询到一个不存在的 webui 平面。
func TestExecutorWebUIPlaneAbortLeavesNoTracker(t *testing.T) {
	pty := &webuiPlanePTY{}
	spy := &webuiPlaneSpy{}
	prepared := prepareAndApplyPTY(t, contract.CLITypePi, pty, spy, "webui-abort-s1")

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
	prepared := prepareAndApplyPTY(t, contract.CLITypePi, pty, nil, "webui-nil-s1")
	if len(pty.capturedSpecs()) != 1 {
		t.Fatalf("PTY start specs = %d, want 1", len(pty.capturedSpecs()))
	}
	prepared.MarkCommitted() // 不得 panic
}
