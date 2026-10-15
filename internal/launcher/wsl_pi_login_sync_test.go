package launcher

// wsl_pi_login_sync_test.go — A2 凭据新鲜度同步函数的单测（M-1 后为双向
// 新陈判定）：三态方向（Windows 新→前向 / distro 新→回同步 / 并列→保守取
// distro）、差异检测（SHA-256）、单文件复制（原子替换 + chmod 600 补偿）、
// tmp 残留清理（m-2）、chmod+清理双失败 fail closed（m-3）、Windows 侧源缺失
// 的登录失效语义。WSL 探测接缝（wslUserHomeFn/wslToUNCFn/wslChmodFn/
// wslStatMTimeFn/wslAuthRemoveFn）替换为本地 temp dir 与内存值，不触达真实
// distro，跨平台可跑。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeWSLForSync 把 WSL 探测接缝指向本地目录：distro 的 $HOME=/home/wslu，
// UNC 映射落到 root 下同名相对路径；distro 侧 mtime 接缝默认 ok=false
// （子测试按需覆盖）。返回 root 与 chmod 记录器。
func fakeWSLForSync(t *testing.T, chmodErr error) (root string, chmodCalls *[]string) {
	t.Helper()
	root = t.TempDir()
	chmodCalls = &[]string{}
	origHome, origUNC, origChmod, origStat, origRemove := wslUserHomeFn, wslToUNCFn, wslChmodFn, wslStatMTimeFn, wslAuthRemoveFn
	wslUserHomeFn = func(string) string { return "/home/wslu" }
	wslToUNCFn = func(_, linuxPath string) string {
		return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(linuxPath, "/")))
	}
	wslChmodFn = func(_, mode string, paths ...string) error {
		for _, p := range paths {
			*chmodCalls = append(*chmodCalls, mode+" "+p)
		}
		return chmodErr
	}
	wslStatMTimeFn = func(_, _ string) (int64, bool) { return 0, false }
	wslAuthRemoveFn = os.Remove
	t.Cleanup(func() {
		wslUserHomeFn, wslToUNCFn, wslChmodFn, wslStatMTimeFn, wslAuthRemoveFn = origHome, origUNC, origChmod, origStat, origRemove
	})
	return root, chmodCalls
}

// stubDistroMTime 把 distro 侧 auth.json 的 mtime 探测固定为给定时间。
func stubDistroMTime(t *testing.T, mtime time.Time) {
	t.Helper()
	orig := wslStatMTimeFn
	wslStatMTimeFn = func(_, path string) (int64, bool) {
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
		if len(*chmodCalls) == 0 || !strings.HasPrefix((*chmodCalls)[0], "600 /home/wslu/.pi/agent/auth.json") {
			t.Fatalf("chmod 600 compensation missing: %v", *chmodCalls)
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
		distroDir := filepath.Dir(distroAuthPath(root))
		writeDistroAuth(t, root, `{"old":{"type":"oauth"}}`, time.Now().Add(-time.Hour))
		stubDistroMTime(t, time.Now().Add(-time.Hour)) // Windows 新 → 前向写
		// 在 tmp 路径上放一个目录，让原子写第一阶段失败（rename 未发生，
		// 旧副本必须仍在）。错误类型不重要，只要求写入失败。
		if err := os.Mkdir(filepath.Join(distroDir, "auth.json.amagi-tmp"), 0o700); err != nil {
			t.Fatal(err)
		}
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
		// m-2：WriteFile 失败分支清理 tmp 残留（空目录被 Remove 收走）。
		if _, statErr := os.Stat(filepath.Join(distroDir, "auth.json.amagi-tmp")); statErr == nil {
			t.Fatal("tmp leftover after WriteFile failure (m-2)")
		}
	})

	t.Run("chmod failure cleans and fails closed", func(t *testing.T) {
		fakeWindowsAuth(t, `{"openai-codex":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, errors.New("chmod boom"))
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil || !strings.Contains(err.Error(), "chmod") {
			t.Fatalf("err = %v, want chmod failure", err)
		}
		if outcome != SyncWSLPiLoginAuthDistroNoAuth {
			t.Fatalf("outcome = %v, want DistroNoAuth", outcome)
		}
		// fail closed：chmod 补偿失败时清理已落盘内容，不留半写/宽松权限状态。
		if _, statErr := os.Stat(distroAuthPath(root)); !os.IsNotExist(statErr) {
			t.Fatal("half-written auth must be cleaned on chmod failure")
		}
	})

	t.Run("chmod plus cleanup double failure fails closed even with file present", func(t *testing.T) {
		fakeWindowsAuth(t, `{"openai-codex":{"type":"oauth"}}`)
		root, _ := fakeWSLForSync(t, errors.New("chmod boom"))
		// 清理失败注入：Remove 恒错 → rename 后的新文件留在盘上（宽松权限）。
		origRemove := wslAuthRemoveFn
		wslAuthRemoveFn = func(string) error { return errors.New("9p stuck") }
		t.Cleanup(func() { wslAuthRemoveFn = origRemove })
		outcome, err := SyncWSLPiLoginAuth("Ubuntu")
		if err == nil || !errors.Is(err, errWSLAuthLooseLeftover) {
			t.Fatalf("err = %v, want errWSLAuthLooseLeftover", err)
		}
		if outcome != SyncWSLPiLoginAuthDistroNoAuth {
			t.Fatalf("outcome = %v, want DistroNoAuth (m-3: loose-perm copy must not degrade-pass)", outcome)
		}
		if _, statErr := os.Stat(distroAuthPath(root)); statErr != nil {
			t.Fatal("precondition: the loose leftover file should still be present in this fault mode")
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

func TestWSLDrvFSMountPath(t *testing.T) {
	cases := map[string]string{
		`C:\Users\u\.pi\agent\auth.json`: "/mnt/c/Users/u/.pi/agent/auth.json",
		`x:/WorkSpace/repo`:              "/mnt/x/WorkSpace/repo",
		`D:\a b\c`:                       "/mnt/d/a b/c",
		`\\wsl.localhost\Ubuntu\home`:    "",
		"/home/u":                        "",
		"":                               "",
		"C:":                             "",
	}
	for in, want := range cases {
		if got := WSLDrvFSMountPath(in); got != want {
			t.Errorf("WSLDrvFSMountPath(%q) = %q, want %q", in, got, want)
		}
	}
}
