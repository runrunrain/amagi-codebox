package headroom

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// envMap converts a "KEY=VALUE" slice into a map for easier assertions.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		m[key] = value
	}
	return m
}

// pathEntries splits a PATH string into a normalized set for comparison.
func pathEntries(pathValue string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, entry := range filepath.SplitList(pathValue) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		out[filepath.Clean(entry)] = struct{}{}
	}
	return out
}

// TestBuildChildEnvWithBase_StripsAnthropicBaseURL verifies the death-loop
// prevention: any inherited ANTHROPIC_BASE_URL must be removed regardless of
// case so headroom forwards to the real upstream, not back into itself.
func TestBuildChildEnvWithBase_StripsAnthropicBaseURL(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:8787",
		"anthropic_base_url=http://127.0.0.1:5280",
		"HOME=/tmp/test-home",
	}
	env := buildChildEnvWithBase("https://api.example.com", 8787, base)
	m := envMap(env)
	if _, ok := m["ANTHROPIC_BASE_URL"]; ok {
		t.Fatalf("ANTHROPIC_BASE_URL should be stripped, got %q", m["ANTHROPIC_BASE_URL"])
	}
	if _, ok := m["anthropic_base_url"]; ok {
		t.Fatalf("lowercase anthropic_base_url should be stripped, got %q", m["anthropic_base_url"])
	}
}

// TestBuildChildEnvWithBase_InjectsHeadroomVars verifies the three injected
// variables headroom needs to locate its listen port and real upstream.
func TestBuildChildEnvWithBase_InjectsHeadroomVars(t *testing.T) {
	base := []string{"PATH=/usr/bin:/bin"}
	env := buildChildEnvWithBase("https://api.example.com/", 9999, base)
	m := envMap(env)

	if m["ANTHROPIC_TARGET_API_URL"] != "https://api.example.com" {
		t.Errorf("ANTHROPIC_TARGET_API_URL = %q, want %q", m["ANTHROPIC_TARGET_API_URL"], "https://api.example.com")
	}
	if m["HEADROOM_PORT"] != "9999" {
		t.Errorf("HEADROOM_PORT = %q, want %q", m["HEADROOM_PORT"], "9999")
	}
	if m["HEADROOM_HOST"] != "127.0.0.1" {
		t.Errorf("HEADROOM_HOST = %q, want %q", m["HEADROOM_HOST"], "127.0.0.1")
	}
}

// TestBuildChildEnvWithBase_PreservesEnhancedPATH is the regression test for the
// macOS "executable file not found in $PATH" failure.
//
// resolveHeadroomBinWithEnv augments PATH with common install locations so it
// can find headroom under a minimal GUI PATH. buildChildEnvWithBase must carry
// that augmented PATH into the child process environment -- if it silently
// resets PATH to the (minimal) process environment, the child cannot locate
// headroom and fails with "executable file not found in $PATH".
func TestBuildChildEnvWithBase_PreservesEnhancedPATH(t *testing.T) {
	// Simulate an enhanced PATH that includes a directory the minimal GUI PATH
	// lacks (e.g. a pip install location under the user home).
	enhancedDir := filepath.Join(os.TempDir(), "headroom-augmented-bin")
	enhancedEnv := []string{
		"PATH=/usr/bin:/bin:" + enhancedDir,
		"HOME=/tmp/test-home",
	}
	env := buildChildEnvWithBase("https://api.example.com", 8787, enhancedEnv)
	m := envMap(env)

	entries := pathEntries(m["PATH"])
	if _, ok := entries[filepath.Clean(enhancedDir)]; !ok {
		t.Fatalf("enhanced PATH entry %q missing from child env PATH=%q; child would not find headroom", enhancedDir, m["PATH"])
	}
}

// TestBuildChildEnvWithBase_DoesNotMutateBase verifies the base env slice is
// not mutated (callers may reuse it).
func TestBuildChildEnvWithBase_DoesNotMutateBase(t *testing.T) {
	base := []string{"PATH=/usr/bin:/bin", "HOME=/tmp/test-home"}
	baseCopy := append([]string(nil), base...)
	_ = buildChildEnvWithBase("https://api.example.com", 8787, base)
	if len(base) != len(baseCopy) {
		t.Fatalf("base env was mutated: len %d, want %d", len(base), len(baseCopy))
	}
	for i := range base {
		if base[i] != baseCopy[i] {
			t.Fatalf("base env entry %d mutated: %q, want %q", i, base[i], baseCopy[i])
		}
	}
}

// TestBuildChildEnv_BackwardCompatible verifies the legacy buildChildEnv wrapper
// still produces a valid environment (strips ANTHROPIC_BASE_URL, injects vars).
func TestBuildChildEnv_BackwardCompatible(t *testing.T) {
	// buildChildEnv reads os.Environ(); set a sentinel via t.Setenv so we can
	// confirm it flows through.
	t.Setenv("HEADROOM_TEST_SENTINEL", "present")
	env := buildChildEnv("https://api.example.com", 8787)
	m := envMap(env)
	if m["HEADROOM_TEST_SENTINEL"] != "present" {
		t.Errorf("buildChildEnv did not carry process env: missing HEADROOM_TEST_SENTINEL")
	}
	if m["HEADROOM_HOST"] != "127.0.0.1" {
		t.Errorf("buildChildEnv did not inject HEADROOM_HOST: %q", m["HEADROOM_HOST"])
	}
}

// TestResolveHeadroomBinWithEnv_AugmentsPATH verifies the returned environment
// has a PATH that is a superset of the process PATH -- i.e. the resolver's
// augmentation directories are present. This is the core guarantee that lets
// the child process find headroom under a minimal macOS GUI PATH.
func TestResolveHeadroomBinWithEnv_AugmentsPATH(t *testing.T) {
	// Snapshot the process PATH entries before resolution.
	processPathValue := os.Getenv("PATH")
	processEntries := pathEntries(processPathValue)

	svc := NewHeadroomService(nil, nil)
	_, enhancedEnv := svc.resolveHeadroomBinWithEnv()
	m := envMap(enhancedEnv)
	enhancedEntries := pathEntries(m["PATH"])

	// Every entry present in the original PATH must still be present in the
	// enhanced PATH (augmentation only adds, never removes).
	var missing []string
	for entry := range processEntries {
		if _, ok := enhancedEntries[entry]; !ok {
			missing = append(missing, entry)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("enhanced PATH lost entries from process PATH: %v", missing)
	}

	// On darwin the resolver is expected to add at least one controlled
	// directory (e.g. /opt/homebrew/bin, ~/.local/bin). This is the whole point
	// of the fix: the child needs these entries to locate headroom.
	if runtime.GOOS == "darwin" {
		if len(enhancedEntries) <= len(processEntries) {
			t.Fatalf("enhanced PATH (%d entries) did not add any directories over process PATH (%d entries); augmentation expected on darwin", len(enhancedEntries), len(processEntries))
		}
	}
}

// TestResolveHeadroomBinWithEnv_ReturnsBinPath verifies the function always
// returns a non-empty bin path (absolute when resolved, bare "headroom" as
// fallback), so callers can always construct a CommandSpec.
func TestResolveHeadroomBinWithEnv_ReturnsBinPath(t *testing.T) {
	svc := NewHeadroomService(nil, nil)
	binPath, enhancedEnv := svc.resolveHeadroomBinWithEnv()
	if strings.TrimSpace(binPath) == "" {
		t.Fatal("binPath should never be empty")
	}
	if len(enhancedEnv) == 0 {
		t.Fatal("enhancedEnv should never be empty")
	}
}

// TestResolveHeadroomBinWithEnv_PrependsVenvBin verifies the venv bin
// directory is prepended to the enhanced PATH so the venv-installed headroom
// wins over any system headroom. This injection is independent of envcheck's
// buildEnhancedEnv and must stay in sync with it.
func TestResolveHeadroomBinWithEnv_PrependsVenvBin(t *testing.T) {
	venvBin := filepath.Join(t.TempDir(), "venv-bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	svc := NewHeadroomService(nil, nil)
	svc.SetVenvBinDir(venvBin)
	_, enhancedEnv := svc.resolveHeadroomBinWithEnv()
	m := envMap(enhancedEnv)
	entries := pathEntries(m["PATH"])
	if _, ok := entries[filepath.Clean(venvBin)]; !ok {
		t.Fatalf("venv bin %q missing from enhanced PATH=%q", venvBin, m["PATH"])
	}
	// venv bin must be the FIRST entry so it takes priority.
	first := strings.Split(m["PATH"], string(os.PathListSeparator))[0]
	if filepath.Clean(first) != filepath.Clean(venvBin) {
		t.Fatalf("venv bin %q should be the first PATH entry, got %q", venvBin, first)
	}
}

// TestBuildChildEnvForKind_OpenAITarget verifies the OpenAI-target variant
// injects OPENAI_TARGET_API_URL (not the Anthropic one) and strips
// OPENAI_BASE_URL for death-loop prevention.
func TestBuildChildEnvForKind_OpenAITarget(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"OPENAI_BASE_URL=http://127.0.0.1:8788/v1",
		"openai_base_url=http://127.0.0.1:8788/v1",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:8787",
		"HOME=/tmp/test-home",
	}
	env := buildChildEnvForKind("https://api.openai.com/v1", 8788, base, backendOpenAI)
	m := envMap(env)

	if m["OPENAI_TARGET_API_URL"] != "https://api.openai.com/v1" {
		t.Errorf("OPENAI_TARGET_API_URL = %q, want %q", m["OPENAI_TARGET_API_URL"], "https://api.openai.com/v1")
	}
	if m["HEADROOM_PORT"] != "8788" {
		t.Errorf("HEADROOM_PORT = %q, want %q", m["HEADROOM_PORT"], "8788")
	}
	if m["HEADROOM_HOST"] != "127.0.0.1" {
		t.Errorf("HEADROOM_HOST = %q, want %q", m["HEADROOM_HOST"], "127.0.0.1")
	}
	// OPENAI_BASE_URL must be stripped (case-insensitive) so headroom does not
	// forward back into itself.
	if _, ok := m["OPENAI_BASE_URL"]; ok {
		t.Errorf("OPENAI_BASE_URL should be stripped for OpenAI kind, got %q", m["OPENAI_BASE_URL"])
	}
	if _, ok := m["openai_base_url"]; ok {
		t.Errorf("lowercase openai_base_url should be stripped for OpenAI kind, got %q", m["openai_base_url"])
	}
	// ANTHROPIC_BASE_URL must survive in the OpenAI-kind env: only the matching
	// kind's loop var is stripped, so an Anthropic base URL (irrelevant to an
	// OpenAI-target headroom) is left untouched.
	if _, ok := m["ANTHROPIC_BASE_URL"]; !ok {
		t.Errorf("ANTHROPIC_BASE_URL should be preserved for OpenAI kind, got env without it")
	}
	// The Anthropic target var must NOT be injected for the OpenAI kind.
	if _, ok := m["ANTHROPIC_TARGET_API_URL"]; ok {
		t.Errorf("ANTHROPIC_TARGET_API_URL must not be injected for OpenAI kind, got %q", m["ANTHROPIC_TARGET_API_URL"])
	}
}

// TestBuildChildEnvForKind_AnthropicStillDefault verifies the Anthropic kind
// still injects ANTHROPIC_TARGET_API_URL and strips ANTHROPIC_BASE_URL, so the
// OpenAI refactor did not regress the claude-session path.
func TestBuildChildEnvForKind_AnthropicStillDefault(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:8787",
		"HOME=/tmp/test-home",
	}
	env := buildChildEnvForKind("https://api.anthropic.example", 8787, base, backendAnthropic)
	m := envMap(env)

	if m["ANTHROPIC_TARGET_API_URL"] != "https://api.anthropic.example" {
		t.Errorf("ANTHROPIC_TARGET_API_URL = %q, want %q", m["ANTHROPIC_TARGET_API_URL"], "https://api.anthropic.example")
	}
	if _, ok := m["ANTHROPIC_BASE_URL"]; ok {
		t.Errorf("ANTHROPIC_BASE_URL should be stripped for Anthropic kind, got %q", m["ANTHROPIC_BASE_URL"])
	}
	if _, ok := m["OPENAI_TARGET_API_URL"]; ok {
		t.Errorf("OPENAI_TARGET_API_URL must not be injected for Anthropic kind, got %q", m["OPENAI_TARGET_API_URL"])
	}
}

// fakeRunner is a minimal platform.ProcessRunner that records the last started
// CommandSpec without spawning a real process. It supports a single in-flight
// command whose Wait blocks until stopChan is closed (so Start reaper goroutine
// does not race the test).
//
// fakeRunner 记录最后一次启动的 CommandSpec，并 spawn 一个真实的 sleep 子进程供
// HeadroomService.start() 的 reaper goroutine 调用 Wait。所有 spawn 出的子进程都会
// 被记入 cmds，便于并发测试在收尾时统一 kill，避免 racy 路径下逃逸出 svc.Stop() 的
// 孤儿 sleep 进程残留。
type fakeRunner struct {
	mu       sync.Mutex
	lastSpec platform.CommandSpec
	startErr error
	stopChan chan struct{}
	started  int

	cmdsMu sync.Mutex
	cmds   []*exec.Cmd
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{stopChan: make(chan struct{})}
}

func (f *fakeRunner) Start(spec platform.CommandSpec) (*exec.Cmd, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSpec = spec
	f.started++
	if f.startErr != nil {
		return nil, f.startErr
	}
	// Build a real *exec.Cmd for a process that blocks on the stop channel so
	// the reaper goroutine in start() has something to Wait on. We do not
	// actually exec anything heavy: use sleep with a long duration; the test
	// closes stopChan and kills the cmd via Stop().
	// 构造一个真实 *exec.Cmd（sleep 长时间），让 start() 的 reaper 有 Wait 目标；
	// 测试通过 Stop() 或 killAll() 终止。
	cmd := exec.Command("sleep", "3600")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	f.cmdsMu.Lock()
	f.cmds = append(f.cmds, cmd)
	f.cmdsMu.Unlock()
	return cmd, nil
}

// killAll terminates every subprocess this runner has spawned. It is the
// reliable cleanup path for concurrency tests: when start() is unlocked,
// multiple concurrent Starts each succeed and spawn their own sleep process,
// but only one is tracked in HeadroomService.cmd at a time -- the rest escape
// svc.Stop(). killAll guarantees no orphaned sleep processes survive the test.
//
// killAll 终止 runner spawn 出的所有子进程。并发测试收尾用：start() 无锁时多个并发
// Start 各自成功并 spawn 子进程，而 HeadroomService.cmd 同一时刻只跟踪一个，其余会
// 逃逸出 svc.Stop()，由 killAll 兜底回收。
func (f *fakeRunner) killAll() {
	f.cmdsMu.Lock()
	defer f.cmdsMu.Unlock()
	for _, c := range f.cmds {
		if c == nil || c.Process == nil {
			continue
		}
		_ = c.Process.Kill()
	}
	f.cmds = nil
}

func (f *fakeRunner) Run(ctx context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	return nil, errors.New("Run not implemented for fake runner")
}

// TestStartForOpenAI_InjectsOpenAITargetEnv verifies StartForOpenAI launches
// the child with OPENAI_TARGET_API_URL set and ANTHROPIC_TARGET_API_URL absent,
// proving the second-instance OpenAI path is wired correctly end-to-end.
func TestStartForOpenAI_InjectsOpenAITargetEnv(t *testing.T) {
	runner := newFakeRunner()
	svc := NewHeadroomService(runner, nil)
	t.Cleanup(func() {
		_ = svc.Stop()
	})

	if err := svc.StartForOpenAI("https://api.openai.com/v1"); err != nil {
		t.Fatalf("StartForOpenAI: %v", err)
	}

	runner.mu.Lock()
	spec := runner.lastSpec
	runner.mu.Unlock()

	m := envMap(spec.Env)
	if m["OPENAI_TARGET_API_URL"] != "https://api.openai.com/v1" {
		t.Fatalf("OPENAI_TARGET_API_URL = %q, want %q", m["OPENAI_TARGET_API_URL"], "https://api.openai.com/v1")
	}
	if _, ok := m["ANTHROPIC_TARGET_API_URL"]; ok {
		t.Fatalf("ANTHROPIC_TARGET_API_URL must not be set for OpenAI target, got %q", m["ANTHROPIC_TARGET_API_URL"])
	}
	if !svc.IsRunning() {
		t.Fatal("service should report running after StartForOpenAI")
	}
}

// TestStartForOpenAI_RejectsEmptyURL verifies the guard matches Start().
func TestStartForOpenAI_RejectsEmptyURL(t *testing.T) {
	svc := NewHeadroomService(newFakeRunner(), nil)
	if err := svc.StartForOpenAI("   "); err == nil {
		t.Fatal("StartForOpenAI with empty URL should error")
	}
}

// TestSetPort_BeforeStart verifies the port can be configured pre-Start and is
// reflected in the launched child args / env.
func TestSetPort_BeforeStart(t *testing.T) {
	runner := newFakeRunner()
	svc := NewHeadroomService(runner, nil)
	t.Cleanup(func() {
		_ = svc.Stop()
	})
	if err := svc.SetPort(8788); err != nil {
		t.Fatalf("SetPort: %v", err)
	}
	if got := svc.GetPort(); got != 8788 {
		t.Fatalf("GetPort = %d, want 8788", got)
	}
	if err := svc.StartForOpenAI("https://api.openai.com/v1"); err != nil {
		t.Fatalf("StartForOpenAI: %v", err)
	}
	runner.mu.Lock()
	spec := runner.lastSpec
	runner.mu.Unlock()

	// The proxy listen port must be passed via CLI args.
	foundPortArg := false
	for _, a := range spec.Args {
		if a == "8788" {
			foundPortArg = true
		}
	}
	if !foundPortArg {
		t.Fatalf("expected 8788 in proxy args %v", spec.Args)
	}
	if envMap(spec.Env)["HEADROOM_PORT"] != "8788" {
		t.Fatalf("HEADROOM_PORT env = %q, want 8788", envMap(spec.Env)["HEADROOM_PORT"])
	}
}

// TestSetPort_RejectsWhileRunning verifies the port cannot be changed while the
// proxy is running (callers must Stop first).
func TestSetPort_RejectsWhileRunning(t *testing.T) {
	svc := NewHeadroomService(newFakeRunner(), nil)
	t.Cleanup(func() {
		_ = svc.Stop()
	})
	if err := svc.StartForOpenAI("https://api.openai.com/v1"); err != nil {
		t.Fatalf("StartForOpenAI: %v", err)
	}
	if err := svc.SetPort(9000); err == nil {
		t.Fatal("SetPort while running should error")
	}
	if got := svc.GetPort(); got != 8787 {
		t.Fatalf("GetPort = %d, want unchanged 8787", got)
	}
}

// TestSecondInstance_PortIsolation verifies two independent HeadroomService
// instances keep separate ports: the codex-global instance (8788) is unaffected
// by the claude-session instance (8787) and vice versa.
func TestSecondInstance_PortIsolation(t *testing.T) {
	runnerA := newFakeRunner()
	runnerB := newFakeRunner()
	claude := NewHeadroomService(runnerA, nil)
	codex := NewHeadroomService(runnerB, nil)
	t.Cleanup(func() {
		_ = claude.Stop()
		_ = codex.Stop()
	})
	if err := codex.SetPort(8788); err != nil {
		t.Fatalf("codex SetPort: %v", err)
	}

	if err := claude.Start("https://api.anthropic.example"); err != nil {
		t.Fatalf("claude Start: %v", err)
	}
	if err := codex.StartForOpenAI("https://api.openai.com/v1"); err != nil {
		t.Fatalf("codex StartForOpenAI: %v", err)
	}

	if claude.GetPort() != 8787 {
		t.Errorf("claude port = %d, want 8787", claude.GetPort())
	}
	if codex.GetPort() != 8788 {
		t.Errorf("codex port = %d, want 8788", codex.GetPort())
	}

	runnerA.mu.Lock()
	envA := envMap(runnerA.lastSpec.Env)
	runnerA.mu.Unlock()
	runnerB.mu.Lock()
	envB := envMap(runnerB.lastSpec.Env)
	runnerB.mu.Unlock()

	if envA["ANTHROPIC_TARGET_API_URL"] == "" {
		t.Error("claude instance should inject ANTHROPIC_TARGET_API_URL")
	}
	if envB["OPENAI_TARGET_API_URL"] == "" {
		t.Error("codex instance should inject OPENAI_TARGET_API_URL")
	}
	if _, ok := envA["OPENAI_TARGET_API_URL"]; ok {
		t.Error("claude instance must NOT inject OPENAI_TARGET_API_URL")
	}
	if _, ok := envB["ANTHROPIC_TARGET_API_URL"]; ok {
		t.Error("codex instance must NOT inject ANTHROPIC_TARGET_API_URL")
	}

	// Stopping the claude instance must not stop the codex instance.
	_ = claude.Stop()
	if !codex.IsRunning() {
		t.Fatal("stopping claude instance must not affect the independent codex instance")
	}
}

// TestStart_ConcurrentAccessNoDataRace is the concurrency regression test for
// the data race introduced when Start() was refactored into start() and the
// s.mu.Lock()+defer Unlock() was dropped.
//
// Why this exists (and why the prior tests gave a false negative):
// start() reads/writes s.running / s.cmd / s.port / s.backendURL, while
// Stop() / SetPort() / IsRunning() / GetStatus() / GetPort() and the reaper
// goroutine all access the same fields under s.mu. Without the lock in start(),
// concurrent callers (LaunchSession vs. the codex-global toggle, or the startup
// restore goroutine vs. a UI toggle) race on those fields. The earlier tests
// called Start -> IsRunning -> Stop strictly in sequence, so the race detector
// never saw overlapping accesses and reported a clean run.
//
// This test fixes that gap with REAL concurrency: N goroutines released by a
// shared barrier each hammer a mixed sequence of Start / StartForOpenAI /
// IsRunning / GetStatus / GetPort / Stop for several iterations. Under -race
// with start() unlocked this reports a data race; with the lock restored it is
// clean.
//
// 该测试用于回归 start() 重构丢失 s.mu 锁导致的 data race。此前测试顺序调用
// Start->IsRunning->Stop，race detector 看不到并发访问故报假阴性。本测试用共享
// barrier 同时释放 N 个 goroutine，各自反复混合调用 Start/StartForOpenAI/
// IsRunning/GetStatus/GetPort/Stop，在 -race 下：start() 无锁时报 race，加锁后干净。
func TestStart_ConcurrentAccessNoDataRace(t *testing.T) {
	runner := newFakeRunner()
	svc := NewHeadroomService(runner, nil)
	t.Cleanup(func() {
		_ = svc.Stop()
		// Guarantees no orphaned sleep subprocess survives: when start() is
		// unlocked, multiple concurrent Starts each spawn their own child but
		// only one is tracked in svc.cmd, so svc.Stop() alone cannot reach the
		// rest. killAll is a no-op when every child was already reaped.
		runner.killAll()
	})

	const (
		goroutines = 8
		iterations = 25
	)

	// Counters are informational only: they let the test assert that real
	// concurrency actually happened (both Start and Stop were exercised many
	// times), so a silent regression that made the test sequential would fail
	// loud rather than degrade into another false negative.
	// 计数器仅用于断言真实并发确实发生（Start 与 Stop 各被调用多次），避免测试退化
	// 成顺序调用后再次出现假阴性。
	var startCalls, stopCalls int64

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			<-startBarrier // release all goroutines simultaneously / 同时释放
			for i := 0; i < iterations; i++ {
				// Mixed access to the s.mu-guarded fields. Every branch must be
				// safe to run concurrently with every other branch.
				// 对 s.mu 保护字段的混合访问；任意分支间必须并发安全。
				_ = svc.IsRunning()
				_ = svc.GetStatus()
				_ = svc.GetPort()

				atomic.AddInt64(&startCalls, 1)
				// Start / StartForOpenAI mostly return "already running" once one
				// goroutine wins the race; the point is the concurrent field
				// access, not the return value.
				if (g+i)%2 == 0 {
					_ = svc.Start("https://api.anthropic.example")
				} else {
					_ = svc.StartForOpenAI("https://api.openai.com/v1")
				}

				atomic.AddInt64(&stopCalls, 1)
				_ = svc.Stop()
			}
		}(g)
	}
	close(startBarrier)
	wg.Wait()

	// Drain in-flight reaper goroutines: killAll unblocks every c.Wait(), after
	// which each reaper briefly takes s.mu to clear s.running. A short wait lets
	// them finish so they do not outlive the test and trip the race detector at
	// an irrelevant point. With start() locked this is purely defensive.
	// killAll 让所有 c.Wait() 返回，reaper 随后短暂持 s.mu 清零 s.running；短暂等待
	// 让其收尾，避免 reaper 跨测试存活。加锁情况下这仅是防御性等待。
	runner.killAll()
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt64(&startCalls) != goroutines*iterations {
		t.Fatalf("startCalls = %d, want %d (test wiring broken)", startCalls, goroutines*iterations)
	}
	if atomic.LoadInt64(&stopCalls) != goroutines*iterations {
		t.Fatalf("stopCalls = %d, want %d (test wiring broken)", stopCalls, goroutines*iterations)
	}
}

// TestGetSavings_ConcurrentWithSetVenvBinDir is the concurrency regression
// test for the data race between GetSavings() and SetVenvBinDir().
//
// Why this exists: GetSavings() resolves the headroom binary via
// resolveHeadroomBinWithEnv(), which reads s.venvBinDir. SetVenvBinDir() writes
// s.venvBinDir under s.mu. Before resolveHeadroomBinWithEnv() was refactored to
// snapshot s.venvBinDir under s.mu (delegating to the lock-free core
// resolveHeadroomBinCore), a concurrent GetSavings()+SetVenvBinDir() raced on
// s.venvBinDir under -race.
//
// fakeRunner.Run returns an error so GetSavings returns early, but the
// s.venvBinDir read (the race point) executes before runner.Run and is
// therefore exercised. With the lock-protected snapshot this is clean.
//
// 该测试回归 GetSavings 与 SetVenvBinDir 的 data race：GetSavings 经
// resolveHeadroomBinWithEnv 读取 s.venvBinDir，SetVenvBinDir 在 s.mu 下写入同一字段。
// 修复前 resolveHeadroomBinWithEnv 未持锁读取，-race 下并发会报 race。fakeRunner.Run
// 返回错误使 GetSavings 提前返回，但 race 点（venvBinDir 读取）在 Run 之前已执行。
func TestGetSavings_ConcurrentWithSetVenvBinDir(t *testing.T) {
	runner := newFakeRunner()
	svc := NewHeadroomService(runner, nil)
	t.Cleanup(func() {
		_ = svc.Stop()
		runner.killAll()
	})

	const (
		goroutines = 8
		iterations = 25
	)
	dirs := []string{"/tmp/headroom-venv-a", "/tmp/headroom-venv-b", "/tmp/headroom-venv-c"}

	var savingsCalls, setVenvCalls int64
	var wg sync.WaitGroup
	startBarrier := make(chan struct{})
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			<-startBarrier
			for i := 0; i < iterations; i++ {
				if g%2 == 0 {
					atomic.AddInt64(&savingsCalls, 1)
					// GetSavings returns an error (fakeRunner.Run is a stub) but
					// still reads s.venvBinDir via resolveHeadroomBinWithEnv().
					_, _ = svc.GetSavings(context.Background())
				} else {
					atomic.AddInt64(&setVenvCalls, 1)
					svc.SetVenvBinDir(dirs[i%len(dirs)])
				}
			}
		}(g)
	}
	close(startBarrier)
	wg.Wait()

	want := int64((goroutines / 2) * iterations)
	if atomic.LoadInt64(&savingsCalls) != want {
		t.Fatalf("savingsCalls = %d, want %d (test wiring broken)", savingsCalls, want)
	}
	if atomic.LoadInt64(&setVenvCalls) != want {
		t.Fatalf("setVenvCalls = %d, want %d (test wiring broken)", setVenvCalls, want)
	}
}
