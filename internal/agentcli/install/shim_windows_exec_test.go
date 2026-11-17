//go:build windows

package install

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type shimProbeRecord struct {
	Home string   `json:"home"`
	Args []string `json:"args"`
}

// TestWindowsShimExecSmoke 端到端冒烟：在含中文与空格路径、CP936 活动代码页下
// 执行 codebox-agent.cmd，验证 AMAGI_CODEBOX_HOME、参数转发、退出码传递与代码页恢复
// 均无乱码（对应 Windows 中文用户名下读不到 endpoint.json 的线上回归）。
func TestWindowsShimExecSmoke(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("跳过：未找到 go 工具链（%v）", err)
	}

	// 编译最小 UTF-8 探针二进制：把收到的 AMAGI_CODEBOX_HOME 与 argv 写入 TEST_OUT_FILE，
	// 传入 --exit7 时以退出码 7 退出以验证 ERRORLEVEL 透传。
	probeSrcDir := t.TempDir()
	probeSrc := `package main

import (
	"encoding/json"
	"os"
)

func main() {
	out := struct {
		Home string   ` + "`json:\"home\"`" + `
		Args []string ` + "`json:\"args\"`" + `
	}{
		Home: os.Getenv("AMAGI_CODEBOX_HOME"),
		Args: os.Args[1:],
	}
	if p := os.Getenv("TEST_OUT_FILE"); p != "" {
		data, err := json.Marshal(out)
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			os.Exit(2)
		}
	}
	for _, a := range os.Args[1:] {
		if a == "--exit7" {
			os.Exit(7)
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(probeSrcDir, "main.go"), []byte(probeSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	probeBin := filepath.Join(probeSrcDir, "probe.exe")
	build := exec.Command(goTool, "build", "-o", probeBin, filepath.Join(probeSrcDir, "main.go"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("编译探针失败：%v\n%s", err, out)
	}
	probeBytes, err := os.ReadFile(probeBin)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "毛润 用户")
	home := filepath.Join(root, ".amagi-codebox")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}

	// Subcase 1：每用户安装路径（home 含中文，exe 相对 home 纯 ASCII → 整份 .cmd 100% ASCII）。
	t.Run("PerUserASCIIRelative_CP936", func(t *testing.T) {
		exe := filepath.Join(root, "AppData", "Local", "Amagi CodeBox", "amagi-codebox.exe")
		if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, probeBytes, 0o755); err != nil {
			t.Fatal(err)
		}
		m := newManager(home, t.TempDir(), exe, nil, shimWindows)
		if err := m.InstallShim(); err != nil {
			t.Fatal(err)
		}
		shimBytes, err := os.ReadFile(m.ShimPath())
		if err != nil {
			t.Fatal(err)
		}
		if !isASCII(string(shimBytes)) {
			t.Fatalf("同用户目录安装生成的 shim 文件应为纯 7-bit ASCII，got:\n%s", string(shimBytes))
		}

		outFile := filepath.Join(t.TempDir(), "probe1.json")
		cmd := exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CmdLine: `cmd.exe /d /s /c "chcp 936 >nul & "` + m.ShimPath() + `" status --json --exit7"`,
		}
		cmd.Env = append(os.Environ(), "TEST_OUT_FILE="+outFile, "AMAGI_CODEBOX_HOME=")
		var ee *exec.ExitError
		out, err := cmd.CombinedOutput()
		if !errors.As(err, &ee) || ee.ExitCode() != 7 {
			t.Fatalf("期望退出码 7，got err=%v out=%s", err, out)
		}
		rec := readShimProbeRecord(t, outFile)
		if rec.Home != home {
			t.Fatalf("AMAGI_CODEBOX_HOME = %q, want %q", rec.Home, home)
		}
		wantArgs := []string{"cli", "status", "--json", "--exit7"}
		if strings.Join(rec.Args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Fatalf("转发参数 = %q, want %q", rec.Args, wantArgs)
		}

		// 同时验证 cmd /d /c 直接传入 shim 路径调用。
		outFileDirect := filepath.Join(t.TempDir(), "probe1-direct.json")
		directCmd := exec.Command("cmd.exe", "/d", "/c", m.ShimPath(), "status", "--json", "--exit7")
		directCmd.Env = append(os.Environ(), "TEST_OUT_FILE="+outFileDirect, "AMAGI_CODEBOX_HOME=")
		out, err = directCmd.CombinedOutput()
		if !errors.As(err, &ee) || ee.ExitCode() != 7 {
			t.Fatalf("直接调用期望退出码 7，got err=%v out=%s", err, out)
		}
		recDirect := readShimProbeRecord(t, outFileDirect)
		if recDirect.Home != home {
			t.Fatalf("直接调用 AMAGI_CODEBOX_HOME = %q, want %q", recDirect.Home, home)
		}
	})

	// Subcase 2：非 ASCII 子目录（触发 chcp 65001 切换），验证转发无乱码、退出码传播与原代码页恢复。
	t.Run("NonASCIISubdir_Chcp65001FallbackAndRestore", func(t *testing.T) {
		exe := filepath.Join(root, "桌面 工具", "amagi-codebox.exe")
		if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, probeBytes, 0o755); err != nil {
			t.Fatal(err)
		}
		home2 := filepath.Join(root, ".amagi-codebox-fallback")
		if err := os.MkdirAll(home2, 0o755); err != nil {
			t.Fatal(err)
		}
		m := newManager(home2, t.TempDir(), exe, nil, shimWindows)
		if err := m.InstallShim(); err != nil {
			t.Fatal(err)
		}

		outFile := filepath.Join(t.TempDir(), "probe2.json")
		cmd := exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CmdLine: `cmd.exe /v:on /d /s /c "chcp 936 >nul & call "` + m.ShimPath() + `" status --json --exit7 & set "RC=!ERRORLEVEL!" & chcp & exit /b !RC!"`,
		}
		cmd.Env = append(os.Environ(), "TEST_OUT_FILE="+outFile, "AMAGI_CODEBOX_HOME=")
		var ee *exec.ExitError
		out, err := cmd.CombinedOutput()
		if !errors.As(err, &ee) || ee.ExitCode() != 7 {
			t.Fatalf("期望退出码 7，got err=%v out=%s", err, out)
		}
		if !strings.Contains(string(out), "936") {
			t.Fatalf("shim 执行后未恢复原代码页 936，chcp 输出：%q", string(out))
		}
		rec := readShimProbeRecord(t, outFile)
		if rec.Home != home2 {
			t.Fatalf("AMAGI_CODEBOX_HOME = %q, want %q", rec.Home, home2)
		}
		wantArgs := []string{"cli", "status", "--json", "--exit7"}
		if strings.Join(rec.Args, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Fatalf("转发参数 = %q, want %q", rec.Args, wantArgs)
		}
	})
}

func readShimProbeRecord(t *testing.T, path string) shimProbeRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取探针输出 %s 失败（shim 可能未能启动目标进程）：%v", path, err)
	}
	var rec shimProbeRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("解析探针输出失败：%v（raw=%q）", err, string(data))
	}
	return rec
}
