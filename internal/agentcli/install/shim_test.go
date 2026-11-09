package install

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/agentcli/spec"
)

// testManager 构造注入平台风格的测试管理器；exePath 默认含空格以覆盖引用。
func testManager(t *testing.T, platform shimPlatform, table []spec.Command) *Manager {
	t.Helper()
	return newManager(t.TempDir(), t.TempDir(), "/opt/Amagi CodeBox/amagi-codebox", table, platform)
}

func TestShimInstallIdempotent(t *testing.T) {
	for _, platform := range []shimPlatform{shimUnix, shimWindows} {
		t.Run(string(platform), func(t *testing.T) {
			m := testManager(t, platform, nil)
			if got := m.ShimStatus().State; got != StateNotInstalled {
				t.Fatalf("初始状态 = %s, want not-installed", got)
			}
			if err := m.InstallShim(); err != nil {
				t.Fatal(err)
			}
			if got := m.ShimStatus().State; got != StateUpToDate {
				t.Fatalf("安装后状态 = %s, want up-to-date", got)
			}
			// 内容一致不重写：把 mtime 拨回过去，二次安装后 mtime 应保持不变。
			path := m.ShimPath()
			past := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatal(err)
			}
			if err := m.InstallShim(); err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !fi.ModTime().Equal(past) {
				t.Fatalf("内容一致时二次安装不应重写文件：mtime %v != %v", fi.ModTime(), past)
			}
			// 安装目录无残留临时文件
			entries, err := os.ReadDir(m.BinDir())
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("bin 目录应只有 shim 一个文件，got %d 项", len(entries))
			}
		})
	}
}

func TestUnixShimContent(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	if err := m.InstallShim(); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(m.ShimPath()) != "codebox-agent" {
		t.Fatalf("Unix shim 文件名 = %q", filepath.Base(m.ShimPath()))
	}
	fi, err := os.Stat(m.ShimPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("权限 = %o, want 755", fi.Mode().Perm())
	}
	data, err := os.ReadFile(m.ShimPath())
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.HasPrefix(body, "#!/bin/sh\n") {
		t.Fatalf("应以 shebang 开头，got:\n%s", body)
	}
	wantExec := "exec " + shellQuote(m.exePath) + " cli \"$@\""
	if !strings.Contains(body, wantExec) {
		t.Fatalf("exec 行引用错误，want %q，got:\n%s", wantExec, body)
	}
	if !strings.Contains(body, "export AMAGI_CODEBOX_HOME="+shellQuote(m.home)) {
		t.Fatalf("应导出 AMAGI_CODEBOX_HOME，got:\n%s", body)
	}
	// 末行为托管标记
	lines := strings.Split(body, "\n")
	mark := lines[len(lines)-2]
	if !strings.HasPrefix(mark, "# amagi-codebox managed sha256:") || len(mark) != len("# amagi-codebox managed sha256:")+64 {
		t.Fatalf("托管标记异常：%q", mark)
	}
}

func TestWindowsShimContent(t *testing.T) {
	m := newManager(t.TempDir(), t.TempDir(), `C:\Apps\Amagi CodeBox\amagi-codebox.exe`, nil, shimWindows)
	if err := m.InstallShim(); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(m.ShimPath()) != "codebox-agent.cmd" {
		t.Fatalf("Windows shim 文件名 = %q", filepath.Base(m.ShimPath()))
	}
	data, err := os.ReadFile(m.ShimPath())
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "\r\n") {
		t.Fatal("应为 CRLF 行尾")
	}
	if !strings.Contains(body, "\"C:\\Apps\\Amagi CodeBox\\amagi-codebox.exe\" cli %*") {
		t.Fatalf("调用行错误，got:\n%s", body)
	}
	if !strings.Contains(body, "set \"AMAGI_CODEBOX_HOME=") {
		t.Fatal("应设置 AMAGI_CODEBOX_HOME")
	}
	// % 转义为 %%（批处理内双引号中 % 仍会展开）
	weird := newManager(t.TempDir(), t.TempDir(), `C:\100%\codebox.exe`, nil, shimWindows)
	spec := shimSpec(weird.home, weird.exePath, shimWindows)
	if !strings.Contains(spec.body, "\"C:\\100%%\\codebox.exe\" cli %*") {
		t.Fatalf("%% 转义缺失，got:\n%s", spec.body)
	}
	mark := spec.mark("x")
	if !strings.HasPrefix(mark, "rem amagi-codebox managed sha256:") || !strings.HasSuffix(mark, "\r\n") {
		t.Fatalf("Windows 标记格式异常：%q", mark)
	}
}

func TestShimQuoting(t *testing.T) {
	// 路径含空格、单引号、双引号与 $：shellQuote 必须整体安全引用。
	exe := `/opt/My App's "bin"/amagi $box`
	quoted := shellQuote(exe)
	if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
		t.Fatalf("应为单引号引用：%q", quoted)
	}
	// 单引号内的 '\'' 转义数应与源串单引号数一致
	if got := strings.Count(quoted, `'\''`); got != strings.Count(exe, "'") {
		t.Fatalf("单引号转义数 = %d, want %d（%q）", got, strings.Count(exe, "'"), quoted)
	}
	body := unixShimBody("/home/u/.amagi-codebox", exe)
	if !strings.Contains(body, "exec "+quoted+" cli \"$@\"") {
		t.Fatalf("exec 行引用错误，got:\n%s", body)
	}
}

func TestShimConflictPreservesUserFile(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	if err := m.InstallShim(); err != nil {
		t.Fatal(err)
	}
	path := m.ShimPath()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# 用户自定义内容\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if got := m.ShimStatus().State; got != StateModifiedByUser {
		t.Fatalf("外部改动后状态 = %s, want modified-by-user", got)
	}
	var ce *ConflictedError
	if err := m.InstallShim(); !errors.As(err, &ce) {
		t.Fatalf("安装应报冲突，got %v", err)
	}
	if err := m.RemoveShim(); !errors.As(err, &ce) {
		t.Fatalf("清理应拒绝外部改动文件，got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "用户自定义内容") {
		t.Fatal("用户内容必须保留")
	}

	// 完全无托管标记的用户自有文件同样保留
	if err := os.WriteFile(path, []byte("echo hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := m.ShimStatus().State; got != StateModifiedByUser {
		t.Fatalf("无标记文件状态 = %s, want modified-by-user", got)
	}
	if err := m.InstallShim(); !errors.As(err, &ce) {
		t.Fatalf("无标记文件安装应报冲突，got %v", err)
	}
}

func TestShimOversizeAndNonRegularAreConflict(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	if err := os.MkdirAll(m.BinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.ShimPath(), bytes.Repeat([]byte("a"), maxManagedFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.ShimStatus().State; got != StateModifiedByUser {
		t.Fatalf("超大文件状态 = %s, want modified-by-user", got)
	}

	m2 := testManager(t, shimUnix, nil)
	if err := os.MkdirAll(m2.ShimPath(), 0o755); err != nil { // shim 路径被目录占用
		t.Fatal(err)
	}
	if got := m2.ShimStatus().State; got != StateModifiedByUser {
		t.Fatalf("非常规文件状态 = %s, want modified-by-user", got)
	}
}

func TestShimOutdatedUpgrades(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	// 模拟旧版 shim：结构相同但 exePath 是旧路径，带有效托管标记。
	old := shimSpec(m.home, "/old/path/amagi-codebox", shimUnix)
	if err := os.MkdirAll(m.BinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old.path, []byte(old.body+old.mark(sha256Hex(old.body))), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := m.ShimStatus().State; got != StateOutdated {
		t.Fatalf("旧版标记有效内容状态 = %s, want outdated", got)
	}
	if err := m.InstallShim(); err != nil {
		t.Fatal(err)
	}
	if got := m.ShimStatus().State; got != StateUpToDate {
		t.Fatalf("升级后状态 = %s, want up-to-date", got)
	}
	data, err := os.ReadFile(m.ShimPath())
	if err != nil || !strings.Contains(string(data), shellQuote(m.exePath)) {
		t.Fatal("应升级到当前 exePath")
	}
}

func TestShimRemove(t *testing.T) {
	m := testManager(t, shimUnix, nil)
	if err := m.InstallShim(); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveShim(); err != nil {
		t.Fatal(err)
	}
	if got := m.ShimStatus().State; got != StateNotInstalled {
		t.Fatalf("清理后状态 = %s, want not-installed", got)
	}
	// 未安装时清理应为幂等成功
	if err := m.RemoveShim(); err != nil {
		t.Fatalf("未安装时清理应幂等：%v", err)
	}
}
