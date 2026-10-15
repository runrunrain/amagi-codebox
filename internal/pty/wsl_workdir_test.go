//go:build windows

package pty

// wsl_workdir_test.go — B4 回归：ConPTY 的 CreateProcess lpCurrentDirectory
// 无法消费 Linux 形态工作目录（真机定谳 err=267 ERROR_DIRECTORY：PowerShell
// P/Invoke CreateProcessW 直测 ~/…、/home/…、/mnt/x/… 三形态全 267，对照
// 盘符路径成功）。WSL 会话的 Linux 形态 workDir 只能交给 wsl.exe --cd，
// 不传 ConPtyWorkDir；同时钉定 A2 的 StartupCommand 权威载荷语义。

import (
	"os"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
)

func TestIsLinuxStylePath(t *testing.T) {
	cases := map[string]bool{
		"~/projects":        true,
		"~":                 true,  // m-6：裸 ~ 对 CreateProcess 同样非法（err=267），--cd ~ 则是合法 Linux home 形态
		"~user/projects":    false, // 别家 Home 形态：明确声明的范围外
		"/home/u":           true,
		"/mnt/c/repo":       true,
		" /home/u ":         true, // TrimSpace 后判定
		`C:\repo`:           false,
		"c:/repo":           false,
		`\\wsl.localhost\X`: false,
		"":                  false,
	}
	for in, want := range cases {
		if got := isLinuxStylePath(in); got != want {
			t.Errorf("isLinuxStylePath(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestConPtyWorkDirFor(t *testing.T) {
	wsl := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapWSL}
	direct := platform.ResolvedLaunchSpec{BootstrapMode: platform.BootstrapDirectCommand}
	cases := []struct {
		name string
		spec platform.ResolvedLaunchSpec
		in   string
		want string
	}{
		{"wsl linux home", wsl, "~/projects", ""},
		{"wsl bare tilde", wsl, "~", ""},              // m-6：裸 ~ 不传 ConPtyWorkDir
		{"wsl tilde user", wsl, "~user/x", "~user/x"}, // 别家 Home 形态范围外，维持原语义
		{"wsl linux abs", wsl, "/home/u/repo", ""},
		{"wsl drvfs mount", wsl, "/mnt/x/WorkSpace/amagi-codebox", ""},
		{"wsl drive letter", wsl, `X:\WorkSpace`, `X:\WorkSpace`},
		{"wsl empty", wsl, "", ""},
		{"non-wsl linux passthrough", direct, "/home/u/repo", "/home/u/repo"},
		{"non-wsl drive", direct, `X:\WorkSpace`, `X:\WorkSpace`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conPtyWorkDirFor(tc.spec, tc.in); got != tc.want {
				t.Fatalf("conPtyWorkDirFor(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBuildWSLCommandLinePrefersStartupCommand：StartupCommand 非空的 WSL
// spec 以其为权威 bash payload（A2：登录重锚载荷），缺省拼装被替代；
// --cd / distro / bash -lic 外壳不变。
func TestBuildWSLCommandLinePrefersStartupCommand(t *testing.T) {
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine; skipping WSL command line build")
	}
	spec := platform.ResolvedLaunchSpec{
		BootstrapMode:  platform.BootstrapWSL,
		WorkDir:        "~/projects",
		CLI:            platform.ResolvedCLI{Path: "pi", Args: []string{"--from-default-assembly"}},
		StartupCommand: `mkdir -p -- "$HOME/.pi/agent"; env -u PI_CODING_AGENT_DIR 'pi' '--provider' 'openai-codex'; exec bash -li`,
		Env:            platform.ResolvedEnv{Variables: []string{}},
	}
	cmd := buildWSLCommandLine(spec, nil)
	if !strings.HasPrefix(cmd, "wsl.exe -d "+distro+" --cd \"~/projects\" -- bash -lic ") {
		t.Fatalf("command line shell frame wrong: %s", cmd)
	}
	// 权威载荷：同步片段 + env -u 前缀在内层 Windows 双引号里（内部双引号经
	// windowsQuoteArg 转义为 \"）。
	if !strings.Contains(cmd, `mkdir -p -- \"$HOME/.pi/agent\"; env -u PI_CODING_AGENT_DIR 'pi' '--provider' 'openai-codex'; exec bash -li`) {
		t.Fatalf("startup command payload not authoritative: %s", cmd)
	}
	// 缺省拼装（CLI.Path/CLI.Args）被替代，不得出现。
	if strings.Contains(cmd, "--from-default-assembly") {
		t.Fatalf("default CLI assembly leaked alongside startup command: %s", cmd)
	}
}

// TestWSLRealMachineLinuxWorkDirStarts 是 B4 的真机 L1 冒烟：ext4 形态
// （~/）workDir 的 WSL 会话经真实 ConPTY 启动成功——修复前该形态直传
// lpCurrentDirectory 必得 err=267。会话跑 echo 后 exec bash；读完输出即关。
// 仅在设置 AMAGI_WSL_E2E=1 且本机有可用 distro 时运行。
func TestWSLRealMachineLinuxWorkDirStarts(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL end-to-end probe")
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine")
	}
	log := logging.NewService(t.TempDir())
	t.Cleanup(func() { log.Close() })
	svc := NewService(log)
	t.Cleanup(func() { svc.CloseAll() })

	spec := platform.ResolvedLaunchSpec{
		AppType:       "pi",
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       "~/",
		CLI:           platform.ResolvedCLI{Path: "echo", Args: []string{"amagi-wsl-e2e-ok"}},
		PTYCols:       80, PTYRows: 24,
	}
	if _, err := svc.StartResolved("wsl-e2e-b4", spec); err != nil {
		t.Fatalf("real WSL start with ~/ workdir failed (B4 regression?): %v", err)
	}
	history := ""
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := svc.GetOutputHistory("wsl-e2e-b4"); err == nil {
			history = string(data)
			if strings.Contains(history, "amagi-wsl-e2e-ok") {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(history, "amagi-wsl-e2e-ok") {
		t.Fatalf("session started but echo marker not observed in output: %.200s", history)
	}
	if err := svc.Close("wsl-e2e-b4"); err != nil {
		t.Fatalf("close: %v", err)
	}
}
