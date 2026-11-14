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
	// Windows 文件系统不保留 POSIX 0755 位（恒为 0666）；借已有 build-tag 常量
	// currentShimPlatform（无 runtime.GOOS 分支）仅在 Unix 宿主校验权限位。
	if currentShimPlatform == shimUnix && fi.Mode().Perm() != 0o755 {
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
	if !strings.Contains(body, "for %%I in (\"%~dp0..\") do set \"AMAGI_CODEBOX_HOME=%%~fI\"") {
		t.Fatalf("应通过 %%~dp0.. 设置 AMAGI_CODEBOX_HOME，got:\n%s", body)
	}
	if !isASCII(body) {
		t.Fatalf("纯 ASCII exe 路径下整份 shim 应为 7-bit ASCII，got:\n%s", body)
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

func TestWindowsShimNonASCIIHomeAndExe(t *testing.T) {
	home := `C:\Users\毛润\.amagi-codebox`

	// Case A：中文 home + 纯 ASCII exePath → 通过 %~dp0.. 保持整份 .cmd 纯 ASCII。
	specA := shimSpec(home, `X:\MyTool\amagi-codebox.exe`, shimWindows)
	if !isASCII(specA.body) {
		t.Fatalf("Case A 应保持纯 7-bit ASCII，got:\n%s", specA.body)
	}
	if !strings.Contains(specA.body, `"%~dp0.."`) || !strings.Contains(specA.body, `"X:\MyTool\amagi-codebox.exe" cli %*`) {
		t.Fatalf("Case A 内容异常：\n%s", specA.body)
	}
	if strings.Contains(specA.body, "chcp") {
		t.Fatalf("Case A 不应包含 chcp：\n%s", specA.body)
	}

	// Case B：中文 home + 同用户目录下的 exePath → 折叠为 %AMAGI_CODEBOX_HOME%\...，零中文。
	for _, tc := range []struct {
		name string
		exe  string
		want string
	}{
		{
			name: "LocalPrograms",
			exe:  `C:\Users\毛润\AppData\Local\Programs\Amagi CodeBox\amagi-codebox.exe`,
			want: `"%AMAGI_CODEBOX_HOME%\..\AppData\Local\Programs\Amagi CodeBox\amagi-codebox.exe" cli %*`,
		},
		{
			name: "HomeRoot",
			exe:  `C:\Users\毛润\.amagi-codebox\amagi-codebox.exe`,
			want: `"%AMAGI_CODEBOX_HOME%\amagi-codebox.exe" cli %*`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specB := shimSpec(home, tc.exe, shimWindows)
			if !isASCII(specB.body) {
				t.Fatalf("Case B(%s) 应保持纯 7-bit ASCII，got:\n%s", tc.name, specB.body)
			}
			if strings.Contains(specB.body, "毛润") || strings.Contains(specB.body, "chcp") {
				t.Fatalf("Case B(%s) 不应出现中文或 chcp：\n%s", tc.name, specB.body)
			}
			if !strings.Contains(specB.body, tc.want) {
				t.Fatalf("Case B(%s) 缺少 %q，got:\n%s", tc.name, tc.want, specB.body)
			}
		})
	}

	// Case C：中文 home + 跨盘/非 ASCII 子目录 exePath → 前导 chcp 65001 切换并在退出前恢复原代码页。
	specC := shimSpec(home, `D:\工具\100%\amagi-codebox.exe`, shimWindows)
	if strings.Contains(specC.body, "毛润") {
		t.Fatalf("Case C 不应硬编码 home 中文目录：\n%s", specC.body)
	}
	for _, want := range []string{
		`chcp 65001 >nul 2>&1`,
		`"D:\工具\100%%\amagi-codebox.exe" cli %*`,
		`chcp %_CB_CP% >nul 2>&1`,
		`exit /b %_CB_RC%`,
	} {
		if !strings.Contains(specC.body, want) {
			t.Fatalf("Case C 缺少 %q，got:\n%s", want, specC.body)
		}
	}
	lines := strings.Split(specC.body, "\r\n")
	for _, line := range lines {
		if strings.Contains(line, `D:\工具\`) {
			break
		}
		if !isASCII(line) {
			t.Fatalf("Case C 执行行之前的前导行必须为纯 ASCII：%q", line)
		}
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
