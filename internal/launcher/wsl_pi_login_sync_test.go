package launcher

// wsl_pi_login_sync_test.go — A2 凭据新鲜度同步函数的单测（M-1 后为双向
// 新陈判定）：三态方向（Windows 新→前向 / distro 新→回同步 / 并列→保守取
// distro）、差异检测（SHA-256）、单文件复制（原子替换 + chmod 600 补偿）、
// tmp 残留清理（m-2）、chmod+清理双失败 fail closed（m-3）、Windows 侧源缺失
// 的登录失效语义。WSL 探测接缝（wslUserHomeFn/wslToUNCFn/wslChmodFn/
// wslStatMTimeFn/wslAuthRemoveFn）替换为本地 temp dir 与内存值，不触达真实
// distro，跨平台可跑。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// fakeWSLForSync 把 WSL 探测接缝指向本地目录：distro 的 $HOME=/home/wslu，
// UNC 映射落到 root 下同名相对路径；distro 侧 mtime 接缝默认 ok=false
// （子测试按需覆盖）；StatMode 从 Chmod 记录读回（staging 验证探测）；身份
// 接缝固定返回与 /home/wslu 一致的 uid/machine-id（G0：Context 入口当次
// 身份核对）。
// 返回 root 与 chmod 记录器。
func fakeWSLForSync(t *testing.T, chmodErr error) (root string, chmodCalls *[]string) {
	t.Helper()
	root = t.TempDir()
	chmodCalls = &[]string{}
	modes := map[string]string{}
	origHome, origUNC, origChmod, origStat, origStatMode, origIdentity := wslUserHomeFn, wslToUNCFn, wslChmodFn, wslStatMTimeFn, wslStatModeFn, wslTargetIdentityContextFn
	wslUserHomeFn = func(string) string { return "/home/wslu" }
	wslToUNCFn = func(_, linuxPath string) string {
		return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
	}
	wslChmodFn = func(_ context.Context, _, mode string, paths ...string) error {
		for _, p := range paths {
			*chmodCalls = append(*chmodCalls, mode+" "+p)
			modes[p] = mode
		}
		return chmodErr
	}
	wslStatModeFn = func(_ context.Context, _, linuxPath string) (string, bool) {
		if m, ok := modes[linuxPath]; ok {
			return m, true
		}
		// PR02：未显式记录 chmod 的路径回退读真实文件权限（模拟健康 0600
		// 副本——降级消费前的权限复核需要可信探测面）。
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/"))))
		if err != nil || !info.Mode().IsRegular() {
			return "", false
		}
		return fmt.Sprintf("%o", info.Mode().Perm()), true
	}
	wslStatMTimeFn = func(context.Context, string, string) (int64, bool) { return 0, false }
	wslTargetIdentityContextFn = func(context.Context, string) platform.WSLUserIdentity {
		return platform.WSLUserIdentity{UID: "1000", Home: "/home/wslu", MachineID: "test-machine", OK: true}
	}
	t.Cleanup(func() {
		wslUserHomeFn, wslToUNCFn, wslChmodFn, wslStatMTimeFn, wslStatModeFn, wslTargetIdentityContextFn = origHome, origUNC, origChmod, origStat, origStatMode, origIdentity
	})
	return root, chmodCalls
}

// stubDistroMTime 把 distro 侧 auth.json 的 mtime 探测固定为给定时间。
func stubDistroMTime(t *testing.T, mtime time.Time) {
	t.Helper()
	orig := wslStatMTimeFn
	wslStatMTimeFn = func(_ context.Context, _, path string) (int64, bool) {
		if strings.HasSuffix(path, "/auth.json") {
			return mtime.Unix(), true
		}
		return 0, false
	}
	t.Cleanup(func() { wslStatMTimeFn = orig })
}

// fakeWindowsAuth 把 HOME 指向临时目录并写入 Windows 侧 auth.json，返回其内容。
func fakeWindowsAuth(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "auth.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return content
}

func windowsAuthPath(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent", "auth.json")
}

func distroAuthPath(root string) string {
	return filepath.Join(root, "home", "wslu", ".pi", "agent", "auth.json")
}

func writeDistroAuth(t *testing.T, root, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(distroAuthPath(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(distroAuthPath(root), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(distroAuthPath(root), mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestSyncWSLPiLoginAuthMissingSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows os.UserHomeDir
	root, _ := fakeWSLForSync(t, nil)
	outcome, err := SyncWSLPiLoginAuth("Ubuntu")
	if !errors.Is(err, ErrWSLPiLoginSourceMissing) {
		t.Fatalf("err = %v, want ErrWSLPiLoginSourceMissing", err)
	}
	if outcome != SyncWSLPiLoginAuthDistroNoAuth {
		t.Fatalf("outcome = %v, want DistroNoAuth", outcome)
	}
	if _, statErr := os.Stat(distroAuthPath(root)); statErr == nil {
		t.Fatal("source-missing path must not touch the distro side")
	}
}

func TestSyncWSLPiLoginAuthSeedsAndRefreshes(t *testing.T) {
	auth := fakeWindowsAuth(t, `{"openai-codex":{"type":"oauth"}}`)

	t.Run("seeds missing distro copy", func(t *testing.T) {
		root, chmodCalls := fakeWSLForSync(t, nil)
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		if outcome != SyncWSLPiLoginAuthCopied {
			t.Fatalf("outcome = %v, want Copied", outcome)
		}
		got, rerr := os.ReadFile(distroAuthPath(root))
		if rerr != nil || string(got) != auth {
			t.Fatalf("distro copy = %q err=%v, want source bytes", got, rerr)
		}
		if len(*chmodCalls) == 0 || !strings.HasPrefix((*chmodCalls)[0], "700 /home/wslu/.pi/agent") {
			t.Fatalf("staged hardening chmod missing: %v", *chmodCalls)
		}
		// SA-06：秘密字节在 staging 内 rename 前收紧 0600（而非 rename 后补偿）。
		var staged600 bool
		for _, c := range *chmodCalls {
			if strings.HasPrefix(c, "600 /home/wslu/.pi/agent/.amagi-staging/") {
				staged600 = true
			}
		}
		if !staged600 {
			t.Fatalf("chmod 600 must tighten the staged secret before rename: %v", *chmodCalls)
		}
	})

	t.Run("identical copy is fresh", func(t *testing.T) {
		root, _ := fakeWSLForSync(t, nil)
		writeDistroAuth(t, root, auth, time.Now().Add(-time.Hour))
		before, _ := os.Stat(distroAuthPath(root))
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		if outcome != SyncWSLPiLoginAuthFresh {
			t.Fatalf("outcome = %v, want Fresh", outcome)
		}
		after, _ := os.Stat(distroAuthPath(root))
		if !after.ModTime().Equal(before.ModTime()) {
			t.Fatal("fresh path must not rewrite the file")
		}
	})

	t.Run("windows newer replaces stale distro copy", func(t *testing.T) {
		root, _ := fakeWSLForSync(t, nil)
		writeDistroAuth(t, root, `{"old":{"type":"oauth"}}`, time.Now().Add(-time.Hour))
		stubDistroMTime(t, time.Now().Add(-time.Hour))
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		if outcome != SyncWSLPiLoginAuthCopied {
			t.Fatalf("outcome = %v, want Copied", outcome)
		}
		got, _ := os.ReadFile(distroAuthPath(root))
		if string(got) != auth {
			t.Fatalf("stale copy not replaced: %q", got)
		}
		// 原子替换不留 tmp 残留。
		entries, _ := os.ReadDir(filepath.Dir(distroAuthPath(root)))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".amagi-tmp") {
				t.Fatalf("tmp leftover: %s", e.Name())
			}
		}
	})
}

// TestSyncWSLPiLoginAuthBidirectional：M-1 三态——distro 新 → 回同步
// （Windows 侧被 distro 字节覆写，distro 不动）；mtime 并列 → 保守取 distro
// （同样回同步）；mtime 探测失败 → 不动任何一侧，降级 DistroHasCopy。
func TestSyncWSLPiLoginAuthBidirectional(t *testing.T) {
	distroAuth := `{"openai-codex":{"type":"oauth","rotated":"in-wsl"}}`
	winAuth := `{"openai-codex":{"type":"oauth"}}`

	t.Run("distro newer reverse-syncs to windows", func(t *testing.T) {
		fakeWindowsAuth(t, winAuth)
		root, _ := fakeWSLForSync(t, nil)
		writeDistroAuth(t, root, distroAuth, time.Now().Add(-time.Hour))
		stubDistroMTime(t, time.Now().Add(time.Hour)) // distro 明确更新
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		if outcome != SyncWSLPiLoginAuthDistroNewer {
			t.Fatalf("outcome = %v, want DistroNewer", outcome)
		}
		winGot, rerr := os.ReadFile(windowsAuthPath(t))
		if rerr != nil || string(winGot) != distroAuth {
			t.Fatalf("windows copy not reverse-synced: %q err=%v", winGot, rerr)
		}
		// distro 侧（唯一新凭据）必须原样保全——单向覆盖式回写会摧毁它。
		distGot, _ := os.ReadFile(distroAuthPath(root))
		if string(distGot) != distroAuth {
			t.Fatalf("distro copy must remain untouched: %q", distGot)
		}
		// 回写不留 tmp。
		if _, statErr := os.Stat(windowsAuthPath(t) + ".amagi-tmp"); statErr == nil {
			t.Fatal("windows tmp leftover after reverse sync")
		}
	})

	t.Run("mtime tie conservatively takes distro", func(t *testing.T) {
		fakeWindowsAuth(t, winAuth)
		root, _ := fakeWSLForSync(t, nil)
		tie := time.Now().Add(-time.Minute)
		writeDistroAuth(t, root, distroAuth, tie)
		// Windows 侧 mtime 也钉到同一时刻：并列容差（≤2s）内不可判。
		if err := os.Chtimes(windowsAuthPath(t), tie, tie); err != nil {
			t.Fatal(err)
		}
		stubDistroMTime(t, tie) // 并列（容差内）
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err != nil {
			t.Fatal(err)
		}
		if outcome != SyncWSLPiLoginAuthDistroTieKept {
			t.Fatalf("outcome = %v, want DistroTieKept", outcome)
		}
		winGot, _ := os.ReadFile(windowsAuthPath(t))
		if string(winGot) != distroAuth {
			t.Fatalf("tie must converge windows to distro bytes: %q", winGot)
		}
	})

	t.Run("mtime probe unavailable keeps both sides", func(t *testing.T) {
		fakeWindowsAuth(t, winAuth)
		root, _ := fakeWSLForSync(t, nil)
		writeDistroAuth(t, root, distroAuth, time.Now().Add(-time.Hour))
		// fakeWSLForSync 默认 wslStatMTimeFn=ok=false，即为「探测失败」。
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil {
			t.Fatal("expected degradation error")
		}
		if outcome != SyncWSLPiLoginAuthDistroHasCopy {
			t.Fatalf("outcome = %v, want DistroHasCopy", outcome)
		}
		winGot, _ := os.ReadFile(windowsAuthPath(t))
		distGot, _ := os.ReadFile(distroAuthPath(root))
		if string(winGot) != winAuth || string(distGot) != distroAuth {
			t.Fatal("indeterminable mtime must not touch either side")
		}
	})
}

func TestSyncWSLPiLoginAuthFailureClassification(t *testing.T) {
	t.Run("write failure with old copy intact degrades", func(t *testing.T) {
		fakeWindowsAuth(t, `{"openai-codex":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, nil)
		writeDistroAuth(t, root, `{"old":{"type":"oauth"}}`, time.Now().Add(-time.Hour))
		stubDistroMTime(t, time.Now().Add(-time.Hour)) // Windows 新 → 前向写
		// 注入 staging 验证失败（rename 前）：旧副本必须仍在（DistroHasCopy
		// 降级）；auth.json 本体保持可探测 0600——降级消费前的权限复核
		//（PR02）必须能证明副本安全，否则降级会被正确拒绌。
		origStatMode := wslStatModeFn
		prevStatMode := wslStatModeFn
		wslStatModeFn = func(ctx context.Context, d, linuxPath string) (string, bool) {
			if strings.Contains(linuxPath, ".amagi-staging") {
				return "", false // 仅 staging 验证失败（rename 前，未提交字节）
			}
			return prevStatMode(ctx, d, linuxPath)
		}
		t.Cleanup(func() { wslStatModeFn = origStatMode })
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil {
			t.Fatal("expected write failure")
		}
		if outcome != SyncWSLPiLoginAuthDistroHasCopy {
			t.Fatalf("outcome = %v, want DistroHasCopy", outcome)
		}
		got, rerr := os.ReadFile(distroAuthPath(root))
		if rerr != nil || string(got) != `{"old":{"type":"oauth"}}` {
			t.Fatalf("old copy must survive a pre-rename failure: %q err=%v", got, rerr)
		}
	})

	t.Run("chmod failure never commits half-written auth", func(t *testing.T) {
		fakeWindowsAuth(t, `{"openai-codex":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, errors.New("chmod boom"))
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil || !strings.Contains(err.Error(), "chmod") {
			t.Fatalf("err = %v, want chmod failure", err)
		}
		if outcome != SyncWSLPiLoginAuthDistroNoAuth {
			t.Fatalf("outcome = %v, want DistroNoAuth", outcome)
		}
		// SA-06/07：chmod 失败发生在 rename 之前——无半写状态可清理，目标
		// 不存在即未提交任何字节。
		if _, statErr := os.Stat(distroAuthPath(root)); !os.IsNotExist(statErr) {
			t.Fatal("no auth bytes may be committed when chmod hardening fails")
		}
	})

	t.Run("fresh copy with provably loose mode fails closed on retighten failure", func(t *testing.T) {
		// m-3 语义在 SA-06 后的新性：宽松权限副本只能出现在「已存在但被
		// 改宽」的零写入路径——重收紧失败时必须 fail closed，不得降级放行
		//（errWSLAuthLooseLeftover → NoAuth）。
		fakeWindowsAuth(t, `{"openai-codex":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, errors.New("chmod boom"))
		writeDistroAuth(t, root, `{"openai-codex":{"type":"oauth"}}`, time.Now().Add(-time.Hour))
		modes := map[string]string{"/home/wslu/.pi/agent/auth.json": "644"}
		origStatMode := wslStatModeFn
		wslStatModeFn = func(_ context.Context, _, p string) (string, bool) {
			m, ok := modes[p]
			return m, ok
		}
		t.Cleanup(func() { wslStatModeFn = origStatMode })
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil || !errors.Is(err, errWSLAuthLooseLeftover) {
			t.Fatalf("err = %v, want errWSLAuthLooseLeftover", err)
		}
		if outcome != SyncWSLPiLoginAuthDistroNoAuth {
			t.Fatalf("outcome = %v, want DistroNoAuth (loose-perm copy must not degrade-pass)", outcome)
		}
	})

	t.Run("unresolvable distro fails closed", func(t *testing.T) {
		fakeWindowsAuth(t, `{"openai-codex":{"type":"oauth"}}`)
		origHome, origUNC, origChmod := wslUserHomeFn, wslToUNCFn, wslChmodFn
		wslUserHomeFn = func(string) string { return "" }
		wslToUNCFn = func(_, _ string) string { return "" }
		wslChmodFn = origChmod
		t.Cleanup(func() { wslUserHomeFn, wslToUNCFn, wslChmodFn = origHome, origUNC, origChmod })
		outcome, err := SyncWSLPiLoginAuth("")
		if err == nil || outcome != SyncWSLPiLoginAuthDistroNoAuth {
			t.Fatalf("outcome=%v err=%v, want DistroNoAuth + error", outcome, err)
		}
	})
}

// TestWriteWSLAuthAtomicUniqueStagingNames：P3-S0 提交原语——staging 文件
// 使用唯一临时名（os.CreateTemp）。生产上同目标写入由 wslsupply 协调器
// applyMu 串行化（Windows rename 同目标并发会 Access Denied），本测按同一
// 串行化前提验证：连续写入不残留 staging、目标字节始终是某个完整写入。
func TestWriteWSLAuthAtomicUniqueStagingNames(t *testing.T) {
	uncDir := t.TempDir()
	target := WSLAgentTarget{Distro: "Ubuntu", Home: "/home/u", LinuxDir: "/home/u/.pi/agent", UNCDir: uncDir}
	modes := map[string]string{}
	origChmod, origStatMode := wslChmodFn, wslStatModeFn
	wslChmodFn = func(_ context.Context, _, mode string, paths ...string) error {
		for _, p := range paths {
			modes[p] = mode
		}
		return nil
	}
	wslStatModeFn = func(_ context.Context, _, p string) (string, bool) {
		m, ok := modes[p]
		return m, ok
	}
	t.Cleanup(func() { wslChmodFn, wslStatModeFn = origChmod, origStatMode })
	const writers = 8
	var mu sync.Mutex
	for i := 0; i < writers; i++ {
		payload := []byte(fmt.Sprintf("{\"writer\":%d}", i))
		mu.Lock()
		if err := writeWSLAuthAtomicContext(context.Background(), target, payload); err != nil {
			mu.Unlock()
			t.Fatalf("serialized writeWSLAuthAtomic #%d: %v", i, err)
		}
		mu.Unlock()
		got, err := os.ReadFile(filepath.Join(uncDir, "auth.json"))
		if err != nil || string(got) != string(payload) {
			t.Fatalf("after write #%d auth.json = %q err=%v, want the writer's complete payload", i, got, err)
		}
	}
	entries, err := os.ReadDir(uncDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".amagi-auth") {
			t.Fatalf("staging leftover: %s", e.Name())
		}
	}
}
