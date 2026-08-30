package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withFakeWSLScript 注入假 wsl.exe runner 并清空相关缓存，测试结束后还原。
func withFakeWSLScript(t *testing.T, fn func(distro, script string) ([]byte, error)) {
	t.Helper()
	orig := wslScriptRunner
	wslScriptRunner = fn
	resetWSLHomeCachesForTest()
	t.Cleanup(func() {
		wslScriptRunner = orig
		resetWSLHomeCachesForTest()
	})
}

func withFakeWSLUNCRoot(t *testing.T, root func(distro string) string) {
	t.Helper()
	orig := wslUNCRootProbe
	wslUNCRootProbe = root
	resetWSLHomeCachesForTest()
	t.Cleanup(func() {
		wslUNCRootProbe = orig
		resetWSLHomeCachesForTest()
	})
}

func TestWSLUserHomeResolvesAndCaches(t *testing.T) {
	// 被测函数在非 Windows 早退返回零值（wsl_home.go GOOS 守卫），fake runner
	// 不会执行；CI 全量测试仅在 macOS 跑（ci.yml），必须 skip（diting M1）。
	if runtime.GOOS != "windows" {
		t.Skip("WSLUserHome 需要 Windows wsl.exe 路径")
	}
	calls := 0
	withFakeWSLScript(t, func(distro, script string) ([]byte, error) {
		calls++
		if distro != "Ubuntu" {
			t.Fatalf("distro = %q, want Ubuntu", distro)
		}
		if script != `printf %s "$HOME"` {
			t.Fatalf("script = %q", script)
		}
		return []byte("/home/wslu\r\n"), nil
	})
	if got := WSLUserHome("Ubuntu"); got != "/home/wslu" {
		t.Fatalf("WSLUserHome = %q, want /home/wslu", got)
	}
	if got := WSLUserHome("Ubuntu"); got != "/home/wslu" {
		t.Fatalf("cached WSLUserHome = %q, want /home/wslu", got)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1 (cached)", calls)
	}
}

func TestWSLUserHomeInvalidResultsCachedEmpty(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"relative", "wslu"},
		{"empty", ""},
		{"windows-path", `C:\Users\wslu`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFakeWSLScript(t, func(distro, script string) ([]byte, error) {
				return []byte(tc.out), nil
			})
			if got := WSLUserHome("Ubuntu"); got != "" {
				t.Fatalf("WSLUserHome(%q) = %q, want empty", tc.out, got)
			}
		})
	}
}

func TestWSLUserHomeEmptyDistro(t *testing.T) {
	withFakeWSLScript(t, func(distro, script string) ([]byte, error) {
		t.Fatalf("must not probe for empty distro")
		return nil, nil
	})
	if got := WSLUserHome("  "); got != "" {
		t.Fatalf("WSLUserHome(empty) = %q, want empty", got)
	}
}

func TestWSLToUNCMapping(t *testing.T) {
	withFakeWSLUNCRoot(t, func(distro string) string {
		return `\\wsl.localhost\` + distro
	})
	got := WSLToUNC("Ubuntu", "/home/wslu/.pi/agent")
	if got != `\\wsl.localhost\Ubuntu\home\wslu\.pi\agent` {
		t.Fatalf("WSLToUNC = %q", got)
	}
	// 旧别名前缀同样可被使用（探测返回什么就用什么）。
	withFakeWSLUNCRoot(t, func(distro string) string {
		return `\\wsl$\` + distro
	})
	got = WSLToUNC("Ubuntu", "/home/wslu")
	if got != `\\wsl$\Ubuntu\home\wslu` {
		t.Fatalf("WSLToUNC legacy alias = %q", got)
	}
}

func TestWSLToUNCUnreachableRoot(t *testing.T) {
	withFakeWSLUNCRoot(t, func(distro string) string { return "" })
	if got := WSLToUNC("Ubuntu", "/home/wslu"); got != "" {
		t.Fatalf("WSLToUNC(unreachable) = %q, want empty", got)
	}
	if got := WSLToUNC("Ubuntu", "relative/path"); got != "" {
		t.Fatalf("WSLToUNC(relative) = %q, want empty", got)
	}
}

func TestWSLSearchToolStatusParsing(t *testing.T) {
	// 同 TestWSLUserHomeResolvesAndCaches：非 Windows 早退导致期望 true 的
	// 子用例必败（diting M1）。
	if runtime.GOOS != "windows" {
		t.Skip("WSLSearchToolStatus 需要 Windows wsl.exe 路径")
	}
	cases := []struct {
		name   string
		out    string
		fdWant bool
		rgWant bool
	}{
		{"both missing", "fd:missing\nrg:missing\r\n", false, false},
		{"fdfind fallback", "fd:/usr/bin/fdfind\nrg:missing\n", true, false},
		{"both present", "fd:/usr/bin/fd\nrg:/usr/bin/rg\n", true, true},
		{"rg only", "fd:missing\nrg:/usr/bin/rg\n", false, true},
		{"probe error", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFakeWSLScript(t, func(distro, script string) ([]byte, error) {
				if tc.out == "" {
					return nil, errors.New("probe failed")
				}
				if !strings.Contains(script, "command -v fd") || !strings.Contains(script, "command -v rg") {
					t.Fatalf("unexpected probe script: %q", script)
				}
				return []byte(tc.out), nil
			})
			got := WSLSearchToolStatus("Ubuntu")
			if got.FD != tc.fdWant || got.Ripgrep != tc.rgWant {
				t.Fatalf("tools = %+v, want fd=%v rg=%v", got, tc.fdWant, tc.rgWant)
			}
		})
	}
}

func TestWSLChmodScriptConstruction(t *testing.T) {
	var scripts []string
	withFakeWSLScript(t, func(distro, script string) ([]byte, error) {
		if distro != "Ubuntu" {
			t.Fatalf("distro = %q", distro)
		}
		scripts = append(scripts, script)
		return nil, nil
	})
	if err := WSLChmod("Ubuntu", "600", "/home/wslu/.pi/agent/models.json"); err != nil {
		t.Fatalf("WSLChmod: %v", err)
	}
	if len(scripts) != 1 || scripts[0] != `chmod 600 '/home/wslu/.pi/agent/models.json'` {
		t.Fatalf("scripts = %v", scripts)
	}
	// 多路径 + 单引号转义。
	scripts = nil
	if err := WSLChmod("Ubuntu", "700", `/home/wsl u/.pi`, "/x'y"); err != nil {
		t.Fatalf("WSLChmod multi: %v", err)
	}
	if len(scripts) != 1 || scripts[0] != `chmod 700 '/home/wsl u/.pi' '/x'\''y'` {
		t.Fatalf("scripts = %v", scripts)
	}
	// 非法 mode（防御性校验）与空输入。
	if err := WSLChmod("Ubuntu", "600; rm -rf /", "/x"); err == nil {
		t.Fatal("expected error for metachar mode")
	}
	if err := WSLChmod("Ubuntu", "600"); err != nil {
		t.Fatalf("no-path call should be a no-op, got %v", err)
	}
	if err := WSLChmod("", "600", "/x"); err != nil {
		t.Fatalf("empty distro should be a no-op, got %v", err)
	}
}

// TestEmbeddedLaunchTargetsWSLGates 覆盖模式门与显式 shell 判定的跨平台可测部分。
// 正向用例（默认 shell=WSL）依赖 Windows 二进制解析，由 Windows 手动 E2E 覆盖。
func TestEmbeddedLaunchTargetsWSLGates(t *testing.T) {
	// 非 Windows 宿主恒为 false。
	if embeddedLaunchTargetsWSLForOS("linux", "embedded", "", nil) {
		t.Fatal("linux host must never target WSL")
	}
	if embeddedLaunchTargetsWSLForOS("darwin", "", "", nil) {
		t.Fatal("darwin host must never target WSL")
	}
	// 非 embedded 模式（terminal/vscode/zed）不进 WSL 分支（resolver 同语义）。
	for _, mode := range []string{"terminal", "vscode", "zed", "EMBEDDED-x"} {
		if embeddedLaunchTargetsWSLForOS("windows", mode, "", nil) {
			t.Fatalf("mode %q must not target WSL", mode)
		}
	}
	// embedded / 空（默认 embedded）允许进入判定。
	if !embeddedLaunchTargetsWSLForOS("windows", "embedded", explicitFakeShell(t, "wsl.exe"), nil) {
		t.Fatal("embedded + wsl shell should target WSL")
	}
	if !embeddedLaunchTargetsWSLForOS("windows", "", explicitFakeShell(t, "wsl.exe"), nil) {
		t.Fatal("default mode + wsl shell should target WSL")
	}
	// 显式 pwsh shell 不进 WSL。
	if embeddedLaunchTargetsWSLForOS("windows", "embedded", explicitFakeShell(t, "pwsh.exe"), nil) {
		t.Fatal("pwsh shell must not target WSL")
	}
	// 不存在的显式 shell 走非 WSL 回退。
	if embeddedLaunchTargetsWSLForOS("windows", "embedded", filepath.Join(t.TempDir(), "nope", "zsh"), nil) {
		t.Fatal("unresolvable shell must not target WSL")
	}
}

// explicitFakeShell 返回一个存在的绝对路径 shell 可执行文件（basename 决定
// resolveRequestedShell 归一化出的 shell key）。
func explicitFakeShell(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestResetWSLSearchToolCache 验证定向/全量失效：安装器装完 fd/ripgrep 后必须
// 失效对应 distro 的探测缓存，否则环境检测与启动探测继续拿到 stale 结果。
func TestResetWSLSearchToolCache(t *testing.T) {
	// 缓存只在 Windows GOOS 守卫内填充（wsl_home.go），非 Windows 全链路早退。
	if runtime.GOOS != "windows" {
		t.Skip("ResetWSLSearchToolCache 的缓存路径需要 Windows wsl.exe 守卫")
	}
	probes := 0
	withFakeWSLScript(t, func(distro, script string) ([]byte, error) {
		probes++
		return []byte("fd:missing\nrg:missing\n"), nil
	})
	if got := WSLSearchToolStatus("Ubuntu"); got.FD || got.Ripgrep {
		t.Fatalf("initial probe = %+v, want all missing", got)
	}
	if probes != 1 {
		t.Fatalf("probes = %d, want 1", probes)
	}
	// 缓存命中：同 distro 不再 fork wsl.exe。
	if got := WSLSearchToolStatus("Ubuntu"); got.FD || got.Ripgrep {
		t.Fatalf("cached probe = %+v, want all missing", got)
	}
	if probes != 1 {
		t.Fatalf("cached probes = %d, want 1", probes)
	}
	// 定向失效：仅该 distro 重新探测。
	ResetWSLSearchToolCache("Ubuntu")
	if got := WSLSearchToolStatus("Ubuntu"); got.FD || got.Ripgrep {
		t.Fatalf("post-reset probe = %+v, want all missing", got)
	}
	if probes != 2 {
		t.Fatalf("after targeted reset probes = %d, want 2", probes)
	}
	// 空 distro（含纯空白）＝清空全部。
	ResetWSLSearchToolCache("   ")
	if got := WSLSearchToolStatus("Ubuntu"); got.FD || got.Ripgrep {
		t.Fatalf("post-empty-reset probe = %+v, want all missing", got)
	}
	if probes != 3 {
		t.Fatalf("after empty reset probes = %d, want 3", probes)
	}
	ResetWSLSearchToolCache("")
	if got := WSLSearchToolStatus("Ubuntu"); got.FD || got.Ripgrep {
		t.Fatalf("post-clear-all probe = %+v, want all missing", got)
	}
	if probes != 4 {
		t.Fatalf("after clear-all probes = %d, want 4", probes)
	}
	// 失效不存在的 distro 是无害 no-op（不 panic、不清别的）。
	ResetWSLSearchToolCache("NoSuchDistro")
	if got := WSLSearchToolStatus("Ubuntu"); got.FD || got.Ripgrep {
		t.Fatalf("post-noop-reset probe = %+v, want all missing", got)
	}
	if probes != 4 {
		t.Fatalf("noop reset must not re-probe, probes = %d, want 4", probes)
	}
}
