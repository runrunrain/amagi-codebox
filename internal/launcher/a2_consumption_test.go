package launcher

// a2_consumption_test.go — A2 统一消费门与真实 ctx 接缝的正式回归
//（wsl-goal-closure 攻坚 G0，attack-contract PR02/PR07 矩阵）：
//   - 5 个可消费出口 × 权限形态（600 健康 / 644 可收紧 / 收紧失败 /
//     unknown / 复验失败）——全部经 finishA2Consumption，成功回写 Windows
//     不再绕门；
//   - 成功反向/并列回写后门失败：新 token 保留（不回滚），NoAuth；
//   - 各真实 ctx 接缝取消（身份探测中、mtime 探测中、distro 侧候选后
//     rename 前、Windows 侧候选后 rename 前、提交后门内）：未提交侧原样、
//     提交后取消如实 NoAuth；
//   - 持续候选污染（staging 字节损坏）：原 auth 不变；
//   - 目标身份失配（UID/Home/MachineID）拒绝。
// 全部断言落到文件字节与返回 outcome，不以「helper 被调用」代替。auth 均
// 为临时目录合成 fixture，不触真实 WSL/用户凭据。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// a2AuthModeSeq 塑造 gate 对 distro auth.json 的探测面：每次探测弹出下一
// 个元素（最后一个重复）；oks 控制探测是否可用（unknown 形态）。
type a2AuthModeSeq struct {
	mu    sync.Mutex
	modes []string
	oks   []bool
	calls int
}

func (s *a2AuthModeSeq) probe() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	if idx >= len(s.modes) {
		idx = len(s.modes) - 1
	}
	if idx < 0 {
		return "", false
	}
	ok := true
	if idx < len(s.oks) {
		ok = s.oks[idx]
	}
	return s.modes[idx], ok
}

func (s *a2AuthModeSeq) probeCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// a2Fake 组装矩阵所需的可注入行为。
type a2Fake struct {
	root       string // distro 侧 UNC 映射根
	winHome    string // Windows 侧 HOME
	chmodCalls *[]string

	authModes []string
	authOKs   []bool
	chmodErr  error
	mtime     time.Time
	mtimeOK   bool

	identity      platform.WSLUserIdentity
	onIdentity    func()                 // 身份探测钩子（取消注入）
	onMTime       func()                 // mtime 探测钩子
	onStagingMode func(linuxPath string) // staging 探测钩子（污染/取消注入）
	onGateMode    func()                 // gate 首次 auth 探测钩子（提交后取消注入）
}

func installA2Fake(t *testing.T, f *a2Fake) {
	t.Helper()
	f.root = t.TempDir()
	f.winHome = t.TempDir()
	f.chmodCalls = &[]string{}
	if f.identity.Home == "" {
		f.identity = platform.WSLUserIdentity{UID: "1000", Home: "/home/wslu", MachineID: "test-machine", OK: true}
	}
	t.Setenv("HOME", f.winHome)
	t.Setenv("USERPROFILE", f.winHome)

	origHome, origUNC, origChmod, origStatMode, origStatMTime, origIdentity :=
		wslUserHomeFn, wslToUNCFn, wslChmodFn, wslStatModeFn, wslStatMTimeFn, wslTargetIdentityContextFn
	wslUserHomeFn = func(string) string { return "/home/wslu" }
	wslToUNCFn = func(_, linuxPath string) string {
		return filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
	}
	wslChmodFn = func(_ context.Context, _, mode string, paths ...string) error {
		for _, p := range paths {
			*f.chmodCalls = append(*f.chmodCalls, mode+" "+p)
		}
		// chmodErr 仅作用于 auth.json 的 600 收紧（gate 面）；staging/目录
		// chmod 恒成功——矩阵聚焦门内收紧失败形态。
		if f.chmodErr != nil && mode == "600" && len(paths) > 0 && strings.HasSuffix(paths[0], "/auth.json") {
			return f.chmodErr
		}
		return nil
	}
	seq := &a2AuthModeSeq{modes: f.authModes, oks: f.authOKs}
	wslStatModeFn = func(_ context.Context, _, linuxPath string) (string, bool) {
		if strings.HasSuffix(linuxPath, "/auth.json") {
			if seq.probeCalls() == 0 && f.onGateMode != nil {
				f.onGateMode()
			}
			return seq.probe()
		}
		if strings.HasSuffix(linuxPath, "/.amagi-staging") {
			return "700", true
		}
		if strings.Contains(linuxPath, "/.amagi-staging/") {
			if f.onStagingMode != nil {
				f.onStagingMode(linuxPath)
			}
			return "600", true
		}
		return "700", true
	}
	wslStatMTimeFn = func(_ context.Context, _, _ string) (int64, bool) {
		if f.onMTime != nil {
			f.onMTime()
		}
		if !f.mtimeOK {
			return 0, false
		}
		return f.mtime.Unix(), true
	}
	wslTargetIdentityContextFn = func(context.Context, string) platform.WSLUserIdentity {
		if f.onIdentity != nil {
			f.onIdentity()
		}
		return f.identity
	}
	t.Cleanup(func() {
		wslUserHomeFn, wslToUNCFn, wslChmodFn, wslStatModeFn, wslStatMTimeFn, wslTargetIdentityContextFn =
			origHome, origUNC, origChmod, origStatMode, origStatMTime, origIdentity
	})
}

func (f *a2Fake) distroAuthPath() string {
	return filepath.Join(f.root, "home", "wslu", ".pi", "agent", "auth.json")
}

func (f *a2Fake) windowsAuthPath() string {
	return filepath.Join(f.winHome, ".pi", "agent", "auth.json")
}

func (f *a2Fake) writeWindowsAuth(t *testing.T, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.windowsAuthPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.windowsAuthPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f.windowsAuthPath(), mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func (f *a2Fake) writeDistroAuth(t *testing.T, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.distroAuthPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.distroAuthPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f.distroAuthPath(), mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func (f *a2Fake) resolvedTarget() WSLAgentTarget {
	return WSLAgentTarget{
		Distro:    "Ubuntu",
		Home:      "/home/wslu",
		LinuxDir:  "/home/wslu/.pi/agent",
		UNCDir:    filepath.Join(f.root, "home", "wslu", ".pi", "agent"),
		UID:       f.identity.UID,
		MachineID: f.identity.MachineID,
	}
}

func mustReadAuth(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func authChmodCount(calls []string) int {
	n := 0
	for _, c := range calls {
		if c == "600 /home/wslu/.pi/agent/auth.json" {
			n++
		}
	}
	return n
}

// runA2Sync 执行一次 Context 入口同步（矩阵统一入口）。
func runA2Sync(f *a2Fake) (SyncWSLPiLoginAuthOutcome, error) {
	return SyncWSLPiLoginAuthContext(context.Background(), f.resolvedTarget())
}

// --- 矩阵一：5 出口 × 5 权限形态 ---

type a2Direction int

const (
	dirCopied a2Direction = iota
	dirFresh
	dirDistroNewer
	dirTieKept
	dirHasCopy
)

func (d a2Direction) name() string {
	switch d {
	case dirCopied:
		return "copied"
	case dirFresh:
		return "fresh"
	case dirDistroNewer:
		return "distro-newer"
	case dirTieKept:
		return "tie-kept"
	default:
		return "has-copy"
	}
}

// setupDirection 按方向布置两侧 auth 状态与 mtime 形态，返回期望的
// （门通过时）outcome、distro 侧期望字节与 windows 侧期望字节。
func setupDirection(t *testing.T, d a2Direction, f *a2Fake) (wantPass SyncWSLPiLoginAuthOutcome, wantDistro, wantWindows string) {
	t.Helper()
	const winAuth = `{"win":{"type":"oauth"}}`
	const distroAuth = `{"distro":{"type":"oauth","rotated":true}}`
	now := time.Now()
	switch d {
	case dirCopied:
		f.writeWindowsAuth(t, winAuth, now)
		// distro 侧缺失；mtime 不参与。
		return SyncWSLPiLoginAuthCopied, winAuth, winAuth
	case dirFresh:
		f.writeWindowsAuth(t, winAuth, now)
		f.writeDistroAuth(t, winAuth, now)
		return SyncWSLPiLoginAuthFresh, winAuth, winAuth
	case dirDistroNewer:
		f.writeWindowsAuth(t, winAuth, now)
		f.writeDistroAuth(t, distroAuth, now)
		f.mtime, f.mtimeOK = now.Add(time.Hour), true
		return SyncWSLPiLoginAuthDistroNewer, distroAuth, distroAuth
	case dirTieKept:
		tie := now.Add(-time.Minute)
		f.writeWindowsAuth(t, winAuth, tie)
		f.writeDistroAuth(t, distroAuth, tie)
		f.mtime, f.mtimeOK = tie, true
		return SyncWSLPiLoginAuthDistroTieKept, distroAuth, distroAuth
	default: // dirHasCopy：内容不同 + mtime 不可判
		f.writeWindowsAuth(t, winAuth, now)
		f.writeDistroAuth(t, distroAuth, now)
		return SyncWSLPiLoginAuthDistroHasCopy, distroAuth, winAuth
	}
}

// TestA2ConsumptionGateMatrix：全部出口 × 权限形态统一过门。
func TestA2ConsumptionGateMatrix(t *testing.T) {
	type permCase struct {
		name        string
		authModes   []string
		authOKs     []bool
		chmodErr    error
		wantGateOK  bool
		wantTighten bool
	}
	permCases := []permCase{
		{name: "mode600-healthy", authModes: []string{"600"}, wantGateOK: true},
		{name: "mode644-tightened", authModes: []string{"644", "600"}, wantGateOK: true, wantTighten: true},
		{name: "mode644-chmod-fails", authModes: []string{"644"}, chmodErr: errors.New("chmod denied"), wantTighten: true},
		{name: "mode-unknown-reverify-fails", authModes: []string{""}, authOKs: []bool{false, false}, wantTighten: true},
		{name: "mode644-reverify-still-644", authModes: []string{"644", "644"}, wantTighten: true},
	}
	directions := []a2Direction{dirCopied, dirFresh, dirDistroNewer, dirTieKept, dirHasCopy}

	for _, d := range directions {
		for _, pc := range permCases {
			t.Run(d.name()+"/"+pc.name, func(t *testing.T) {
				f := &a2Fake{authModes: pc.authModes, authOKs: pc.authOKs, chmodErr: pc.chmodErr}
				installA2Fake(t, f)
				wantOutcome, wantDistroBytes, wantWindowsBytes := setupDirection(t, d, f)

				outcome, err := runA2Sync(f)

				if pc.wantGateOK {
					if outcome != wantOutcome {
						t.Fatalf("outcome = %v, want %v", outcome, wantOutcome)
					}
					// HasCopy 方向的 err 是 mtime 不可判降级原因（保留）；其余门通过时不得有 err。
					if err != nil && d != dirHasCopy {
						t.Fatalf("gate-passed %v must not carry err, got %v", wantOutcome, err)
					}
					if err == nil && d == dirHasCopy {
						t.Fatal("has-copy degradation must keep its mtime-unavailable cause")
					}
					if got := mustReadAuth(t, f.distroAuthPath()); got != wantDistroBytes {
						t.Fatalf("distro auth bytes = %q, want %q", got, wantDistroBytes)
					}
					if got := mustReadAuth(t, f.windowsAuthPath()); got != wantWindowsBytes {
						t.Fatalf("windows auth bytes = %q, want %q", got, wantWindowsBytes)
					}
					if n := authChmodCount(*f.chmodCalls); (n == 1) != pc.wantTighten {
						t.Fatalf("auth tighten chmod calls = %d (wantTighten=%v), calls=%v", n, pc.wantTighten, *f.chmodCalls)
					}
					return
				}

				// 门失败：一律 NoAuth，双 cause 保留（权限 cause + 同步 cause），
				// 且方向性已提交写不回滚。
				if outcome != SyncWSLPiLoginAuthDistroNoAuth {
					t.Fatalf("outcome = %v, want DistroNoAuth when gate cannot prove 600", outcome)
				}
				if err == nil {
					t.Fatal("gate failure must surface an error")
				}
				if !errors.Is(err, errWSLAuthLooseLeftover) {
					t.Fatalf("err = %v, want errWSLAuthLooseLeftover in chain", err)
				}
				if !strings.Contains(err.Error(), "auth.json") {
					t.Fatalf("error must identify the affected secret file: %v", err)
				}
				if got := mustReadAuth(t, f.distroAuthPath()); got != wantDistroBytes {
					t.Fatalf("distro auth bytes after gate failure = %q, want %q", got, wantDistroBytes)
				}
				if got := mustReadAuth(t, f.windowsAuthPath()); got != wantWindowsBytes {
					t.Fatalf("windows auth bytes after gate failure = %q, want %q (committed writeback must not be rolled back)", got, wantWindowsBytes)
				}
				if n := authChmodCount(*f.chmodCalls); n != 1 {
					t.Fatalf("tighten attempt count = %d, want 1, calls=%v", n, *f.chmodCalls)
				}
				// 成功回写后的门失败必须在错误链中报告方向性实际写入。
				if d == dirDistroNewer || d == dirTieKept {
					if !strings.Contains(err.Error(), "windows-side writeback committed") {
						t.Fatalf("gate failure after writeback must report the committed direction: %v", err)
					}
				}
			})
		}
	}
}

// --- 矩阵二：ctx 真实接缝取消 ---

// TestA2CancelDuringIdentityProbe：身份探测中取消——目标未选定、零写入、
// NoAuth。
func TestA2CancelDuringIdentityProbe(t *testing.T) {
	f := &a2Fake{authModes: []string{"600"}}
	installA2Fake(t, f)
	f.writeWindowsAuth(t, `{"win":1}`, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	f.onIdentity = cancel
	outcome, err := SyncWSLPiLoginAuthContext(ctx, f.resolvedTarget())
	if outcome != SyncWSLPiLoginAuthDistroNoAuth {
		t.Fatalf("outcome = %v, want DistroNoAuth", outcome)
	}
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(f.distroAuthPath()); !os.IsNotExist(statErr) {
		t.Fatal("canceled during identity probe must not write the distro side")
	}
	if got := mustReadAuth(t, f.windowsAuthPath()); got != `{"win":1}` {
		t.Fatalf("windows side altered: %q", got)
	}
}

// TestA2CancelDuringMTimeProbe：mtime 探测中取消——两侧原样、NoAuth（不按
// 存在降为 HasCopy）。
func TestA2CancelDuringMTimeProbe(t *testing.T) {
	f := &a2Fake{authModes: []string{"600"}}
	installA2Fake(t, f)
	f.writeWindowsAuth(t, `{"win":1}`, time.Now())
	f.writeDistroAuth(t, `{"distro":1}`, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	f.onMTime = cancel
	outcome, err := SyncWSLPiLoginAuthContext(ctx, f.resolvedTarget())
	if outcome != SyncWSLPiLoginAuthDistroNoAuth {
		t.Fatalf("outcome = %v, want DistroNoAuth (cancellation must not degrade to HasCopy)", outcome)
	}
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := mustReadAuth(t, f.distroAuthPath()); got != `{"distro":1}` {
		t.Fatalf("distro side altered: %q", got)
	}
	if got := mustReadAuth(t, f.windowsAuthPath()); got != `{"win":1}` {
		t.Fatalf("windows side altered: %q", got)
	}
}

// TestA2CancelAfterDistroCandidateBeforeRename：distro 侧候选已写入 staging、
// chmod 已收紧，rename 前取消——原 auth 不变（未提交侧原样），staging 临时
// 文件清理。
func TestA2CancelAfterDistroCandidateBeforeRename(t *testing.T) {
	f := &a2Fake{authModes: []string{"600"}}
	installA2Fake(t, f)
	f.writeWindowsAuth(t, `{"win":1}`, time.Now())
	f.writeDistroAuth(t, `{"old":1}`, time.Now().Add(-time.Hour)) // Windows 新 → 前向写
	f.mtime, f.mtimeOK = time.Now().Add(-time.Hour), true

	ctx, cancel := context.WithCancel(context.Background())
	stagingDir := filepath.Join(f.root, "home", "wslu", ".pi", "agent", ".amagi-staging")
	f.onStagingMode = func(string) { cancel() }
	outcome, err := SyncWSLPiLoginAuthContext(ctx, f.resolvedTarget())
	if outcome != SyncWSLPiLoginAuthDistroNoAuth {
		t.Fatalf("outcome = %v, want DistroNoAuth (cancel must not degrade to HasCopy)", outcome)
	}
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := mustReadAuth(t, f.distroAuthPath()); got != `{"old":1}` {
		t.Fatalf("pre-rename cancel must leave original auth untouched, got %q", got)
	}
	if got := mustReadAuth(t, f.windowsAuthPath()); got != `{"win":1}` {
		t.Fatalf("windows side altered: %q", got)
	}
	entries, rerr := os.ReadDir(stagingDir)
	if rerr == nil && len(entries) != 0 {
		t.Fatalf("staging must be cleaned after pre-rename cancel: %v", entries)
	}
}

// countingCtx 在第 n 次 Err() 调用时取消自身（确定性钉住「候选写入后、
// rename 前」的取消窗口）。
type countingCtx struct {
	context.Context
	mu       sync.Mutex
	cancelAt int
	calls    int
	canceled bool
}

func (c *countingCtx) Err() error {
	c.mu.Lock()
	c.calls++
	trigger := c.calls >= c.cancelAt
	c.mu.Unlock()
	if trigger && !c.canceled {
		c.canceled = true
	}
	if c.canceled {
		return context.Canceled
	}
	return nil
}

// TestA2WindowsWriterCancelBeforeRename：Windows 侧 writer 候选写入后、实际
// rename 前取消（第 2 次 ctx 检查点 = 候选写完成之后）——旧 Windows auth
// 原样、tmp 清理、distro 不动。
func TestA2WindowsWriterCancelBeforeRename(t *testing.T) {
	f := &a2Fake{authModes: []string{"600"}}
	installA2Fake(t, f)
	agentDir := filepath.Join(f.winHome, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{"old-win":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := &countingCtx{Context: context.Background(), cancelAt: 2}
	err := writeWindowsAuthAtomicContext(ctx, agentDir, []byte(`{"new":1}`))
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := mustReadAuth(t, filepath.Join(agentDir, "auth.json")); got != `{"old-win":1}` {
		t.Fatalf("pre-rename cancel must keep old windows auth, got %q", got)
	}
	entries, rerr := os.ReadDir(agentDir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".amagi-auth-win-") {
			t.Fatalf("windows staging leftover: %s", e.Name())
		}
	}
}

// TestA2CancelAfterCommitGateFails：提交后（前向复制完成）取消——门如实
// NoAuth，新 token 保留在 distro 侧（不回滚）、错误链报告方向性写入。
func TestA2CancelAfterCommitGateFails(t *testing.T) {
	f := &a2Fake{authModes: []string{"600"}}
	installA2Fake(t, f)
	f.writeWindowsAuth(t, `{"win":1}`, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	f.onGateMode = cancel
	outcome, err := SyncWSLPiLoginAuthContext(ctx, f.resolvedTarget())
	if outcome != SyncWSLPiLoginAuthDistroNoAuth {
		t.Fatalf("outcome = %v, want DistroNoAuth (post-commit cancel)", outcome)
	}
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "forward copy to distro committed") {
		t.Fatalf("post-commit cancel must report the committed direction: %v", err)
	}
	if got := mustReadAuth(t, f.distroAuthPath()); got != `{"win":1}` {
		t.Fatalf("committed new token must be preserved (not rolled back), got %q", got)
	}
	if got := mustReadAuth(t, f.windowsAuthPath()); got != `{"win":1}` {
		t.Fatalf("windows side altered: %q", got)
	}
}

// TestA2CancelAfterWritebackGateFails：反向回写成功后取消——Windows 侧保留
// 新 token（不回滚），distro 侧原样，NoAuth + 方向性报告。
func TestA2CancelAfterWritebackGateFails(t *testing.T) {
	f := &a2Fake{authModes: []string{"600"}}
	installA2Fake(t, f)
	now := time.Now()
	f.writeWindowsAuth(t, `{"win":1}`, now)
	f.writeDistroAuth(t, `{"distro":1}`, now)
	f.mtime, f.mtimeOK = now.Add(time.Hour), true
	ctx, cancel := context.WithCancel(context.Background())
	f.onGateMode = cancel
	outcome, err := SyncWSLPiLoginAuthContext(ctx, f.resolvedTarget())
	if outcome != SyncWSLPiLoginAuthDistroNoAuth {
		t.Fatalf("outcome = %v, want DistroNoAuth (post-writeback cancel)", outcome)
	}
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "windows-side writeback committed") {
		t.Fatalf("post-writeback cancel must report the committed direction: %v", err)
	}
	if got := mustReadAuth(t, f.windowsAuthPath()); got != `{"distro":1}` {
		t.Fatalf("committed writeback must be preserved (not rolled back), got %q", got)
	}
	if got := mustReadAuth(t, f.distroAuthPath()); got != `{"distro":1}` {
		t.Fatalf("distro side altered: %q", got)
	}
}

// --- 矩阵三：持续候选污染与身份失配 ---

// TestA2ContinuousStagingPollution：staging 字节持续被污染——候选读回预验
// 拦截，原 auth 原样；按前向写失败分类（旧副本在场 → HasCopy 过门）。
func TestA2ContinuousStagingPollution(t *testing.T) {
	f := &a2Fake{authModes: []string{"600"}}
	installA2Fake(t, f)
	now := time.Now()
	f.writeWindowsAuth(t, `{"win":1}`, now)
	f.writeDistroAuth(t, `{"old":1}`, now.Add(-time.Hour))
	f.mtime, f.mtimeOK = now.Add(-time.Hour), true

	stagingDir := filepath.Join(f.root, "home", "wslu", ".pi", "agent", ".amagi-staging")
	f.onStagingMode = func(string) {
		// 每次 staging 文件探测时把所有 staging 候选污染掉（模拟持续写坏）。
		entries, rerr := os.ReadDir(stagingDir)
		if rerr != nil {
			return
		}
		for _, e := range entries {
			p := filepath.Join(stagingDir, e.Name())
			_ = os.WriteFile(p, []byte(`{"polluted":true}`), 0o600)
		}
	}
	outcome, err := runA2Sync(f)
	if outcome != SyncWSLPiLoginAuthDistroHasCopy {
		t.Fatalf("outcome = %v, want DistroHasCopy (old copy intact, gate-passed 600)", outcome)
	}
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err = %v, want candidate mismatch error", err)
	}
	if got := mustReadAuth(t, f.distroAuthPath()); got != `{"old":1}` {
		t.Fatalf("polluted candidate must never replace original auth, got %q", got)
	}
}

// TestA2TargetIdentityMismatch：传入 target 与当次身份探测失配（UID/Home/
// MachineID）或结构不完整——NoAuth 且零写入。
func TestA2TargetIdentityMismatch(t *testing.T) {
	base := func(t *testing.T) *a2Fake {
		f := &a2Fake{authModes: []string{"600"}}
		installA2Fake(t, f)
		f.writeWindowsAuth(t, `{"win":1}`, time.Now())
		f.writeDistroAuth(t, `{"distro":1}`, time.Now())
		return f
	}
	cases := []struct {
		name   string
		mutate func(f *a2Fake, target *WSLAgentTarget)
	}{
		{"uid-changed", func(f *a2Fake, target *WSLAgentTarget) { target.UID = "4242" }},
		{"home-changed", func(f *a2Fake, target *WSLAgentTarget) {
			target.Home = "/home/other"
			target.LinuxDir = "/home/other/.pi/agent"
			target.UNCDir = filepath.Join(f.root, "home", "other", ".pi", "agent")
		}},
		{"machine-id-changed", func(f *a2Fake, target *WSLAgentTarget) { target.MachineID = "different-machine" }},
		{"uid-missing", func(f *a2Fake, target *WSLAgentTarget) { target.UID = "" }},
		{"linux-dir-mismatch", func(f *a2Fake, target *WSLAgentTarget) { target.LinuxDir = "/home/wslu/.pi/elsewhere" }},
		{"unc-dir-mismatch", func(f *a2Fake, target *WSLAgentTarget) { target.UNCDir = filepath.Join(f.root, "elsewhere") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base(t)
			target := f.resolvedTarget()
			tc.mutate(f, &target)
			outcome, err := SyncWSLPiLoginAuthContext(context.Background(), target)
			if outcome != SyncWSLPiLoginAuthDistroNoAuth {
				t.Fatalf("outcome = %v, want DistroNoAuth", outcome)
			}
			if err == nil {
				t.Fatal("identity/structure mismatch must error")
			}
			if got := mustReadAuth(t, f.distroAuthPath()); got != `{"distro":1}` {
				t.Fatalf("distro side altered on mismatch: %q", got)
			}
			if got := mustReadAuth(t, f.windowsAuthPath()); got != `{"win":1}` {
				t.Fatalf("windows side altered on mismatch: %q", got)
			}
		})
	}
}

// TestA2CompatWrapperPassesGate：旧入口 SyncWSLPiLoginAuth 同样过门——
// 不可证 600 的副本不得经兼容包装降级放行（无旧权限旁路）。
func TestA2CompatWrapperPassesGate(t *testing.T) {
	t.Run("healthy 600 degrades via gate", func(t *testing.T) {
		f := &a2Fake{authModes: []string{"600"}}
		installA2Fake(t, f)
		now := time.Now()
		f.writeWindowsAuth(t, `{"win":1}`, now)
		f.writeDistroAuth(t, `{"distro":1}`, now)
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if outcome != SyncWSLPiLoginAuthDistroHasCopy {
			t.Fatalf("outcome = %v, want DistroHasCopy (mtime unavailable, gate-passed)", outcome)
		}
		if err == nil {
			t.Fatal("mtime-unavailable degradation must keep its cause")
		}
	})
	t.Run("unprovable mode fails closed via wrapper", func(t *testing.T) {
		f := &a2Fake{authModes: []string{"644"}, chmodErr: errors.New("chmod denied")}
		installA2Fake(t, f)
		now := time.Now()
		f.writeWindowsAuth(t, `{"win":1}`, now)
		f.writeDistroAuth(t, `{"distro":1}`, now)
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if outcome != SyncWSLPiLoginAuthDistroNoAuth {
			t.Fatalf("outcome = %v, want DistroNoAuth (compat wrapper must not bypass the gate)", outcome)
		}
		if err == nil || !errors.Is(err, errWSLAuthLooseLeftover) {
			t.Fatalf("err = %v, want errWSLAuthLooseLeftover", err)
		}
	})
	t.Run("wrapper success path", func(t *testing.T) {
		f := &a2Fake{authModes: []string{"600"}}
		installA2Fake(t, f)
		now := time.Now()
		f.writeWindowsAuth(t, `{"win":1}`, now.Add(time.Hour))
		f.writeDistroAuth(t, `{"old":1}`, now)
		f.mtime, f.mtimeOK = now, true // distro 旧 → 前向复制
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err != nil || outcome != SyncWSLPiLoginAuthCopied {
			t.Fatalf("outcome=%v err=%v, want Copied", outcome, err)
		}
		if got := mustReadAuth(t, f.distroAuthPath()); got != `{"win":1}` {
			t.Fatalf("distro bytes = %q, want forward-copied windows bytes", got)
		}
	})
}
