//go:build windows

package wslsetup

import (
	"errors"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// stubSearchToolsEnv 替换 InstallSearchTools 依赖的三个包级 var：
// searchToolStatus（探测序列，末值保持）、resetSearchToolCache（记录调用）、
// wslExecRoot（记录 apt 脚本并由 rootFn 决定输出/错误）。
func stubSearchToolsEnv(t *testing.T, statuses []platform.WSLSearchTools, rootFn func(distro, script string) (string, error)) (probes, resets *int, scripts *[]string) {
	t.Helper()
	prevStatus, prevReset, prevRoot := searchToolStatus, resetSearchToolCache, wslExecRoot
	probeCount, resetCount := 0, 0
	var rootScripts []string
	searchToolStatus = func(distro string) platform.WSLSearchTools {
		probeCount++
		if len(statuses) == 0 {
			return platform.WSLSearchTools{}
		}
		if probeCount <= len(statuses) {
			return statuses[probeCount-1]
		}
		return statuses[len(statuses)-1]
	}
	resetSearchToolCache = func(string) { resetCount++ }
	wslExecRoot = func(distro, script string) (string, error) {
		rootScripts = append(rootScripts, script)
		if rootFn != nil {
			return rootFn(distro, script)
		}
		return "ok\n", nil
	}
	t.Cleanup(func() {
		searchToolStatus, resetSearchToolCache, wslExecRoot = prevStatus, prevReset, prevRoot
	})
	return &probeCount, &resetCount, &rootScripts
}

// TestInstallSearchToolsAlreadyOK 已装短路：探测全命中时不跑 apt，只做一次
// 安装前缓存失效（刷新 stale 快照）。
func TestInstallSearchToolsAlreadyOK(t *testing.T) {
	skipWithoutWSLDistro(t)
	probes, resets, scripts := stubSearchToolsEnv(t, []platform.WSLSearchTools{{FD: true, Ripgrep: true}}, nil)

	res, err := NewService(nil).InstallSearchTools()
	if err != nil {
		t.Fatalf("InstallSearchTools: %v", err)
	}
	if !res.Success || !res.AlreadyOK {
		t.Fatalf("result = %+v, want already-ok success", res)
	}
	if *probes != 1 || *resets != 1 {
		t.Errorf("probes=%d resets=%d, want 1/1 (pre-check only)", *probes, *resets)
	}
	if len(*scripts) != 0 {
		t.Errorf("no apt must run when already installed, got %v", *scripts)
	}
	if res.Tool != SearchToolsKey || res.Package != "fd-find ripgrep" {
		t.Errorf("tool/package = %q/%q", res.Tool, res.Package)
	}
}

// TestInstallSearchToolsInstallFlow 安装主流程：apt 脚本含 update 与
// fd-find/ripgrep；安装前后各失效一次缓存；验证探测命中后 Success。
func TestInstallSearchToolsInstallFlow(t *testing.T) {
	skipWithoutWSLDistro(t)
	probes, resets, scripts := stubSearchToolsEnv(t,
		[]platform.WSLSearchTools{{}, {FD: true, Ripgrep: true}}, nil)

	res, err := NewService(nil).InstallSearchTools()
	if err != nil {
		t.Fatalf("InstallSearchTools: %v", err)
	}
	if !res.Success || res.AlreadyOK {
		t.Fatalf("result = %+v, want fresh-install success", res)
	}
	if !strings.Contains(res.Message, "fd-find") || !strings.Contains(res.Message, "PI_OFFLINE") {
		t.Errorf("message should explain what/why: %q", res.Message)
	}
	if *probes != 2 || *resets != 2 {
		t.Errorf("probes=%d resets=%d, want 2/2 (pre + post)", *probes, *resets)
	}
	if len(*scripts) != 1 {
		t.Fatalf("apt scripts = %v, want exactly one", *scripts)
	}
	script := (*scripts)[0]
	for _, want := range []string{"DEBIAN_FRONTEND=noninteractive", "apt-get update -qq", "apt-get install -y fd-find ripgrep"} {
		if !strings.Contains(script, want) {
			t.Errorf("apt script missing %q:\n%s", want, script)
		}
	}
}

// TestInstallSearchToolsAptFailure apt 失败：Success=false，Error 带原因；
// 非 apt 系（command not found）额外给手动包管理器指引。
func TestInstallSearchToolsAptFailure(t *testing.T) {
	skipWithoutWSLDistro(t)

	t.Run("generic apt failure", func(t *testing.T) {
		_, _, _ = stubSearchToolsEnv(t, []platform.WSLSearchTools{{}}, func(_, _ string) (string, error) {
			return "E: Unable to locate package fd-find\n", errors.New("exit status 100")
		})
		res, err := NewService(nil).InstallSearchTools()
		if err != nil {
			t.Fatalf("InstallSearchTools: %v", err)
		}
		if res.Success || res.Error == "" || !strings.Contains(res.Error, "apt 安装") {
			t.Fatalf("result = %+v, want structured apt failure", res)
		}
	})

	t.Run("non-apt distro guidance", func(t *testing.T) {
		_, _, _ = stubSearchToolsEnv(t, []platform.WSLSearchTools{{}}, func(_, _ string) (string, error) {
			return "bash: apt-get: command not found\n", errors.New("exit status 127")
		})
		res, err := NewService(nil).InstallSearchTools()
		if err != nil {
			t.Fatalf("InstallSearchTools: %v", err)
		}
		if res.Success || !strings.Contains(res.Error, "apk add fd ripgrep") {
			t.Fatalf("non-apt failure should carry manual guidance, got: %q", res.Error)
		}
	})
}

// TestInstallSearchToolsVerifyFails 安装命令成功但验证探测仍缺失：
// Success=false 且 Error 给手动兜底命令（不能谎报成功）。
func TestInstallSearchToolsVerifyFails(t *testing.T) {
	skipWithoutWSLDistro(t)
	_, _, _ = stubSearchToolsEnv(t, []platform.WSLSearchTools{{}, {Ripgrep: true}}, nil)

	res, err := NewService(nil).InstallSearchTools()
	if err != nil {
		t.Fatalf("InstallSearchTools: %v", err)
	}
	if res.Success {
		t.Fatal("Success=true despite fd missing after install")
	}
	if !strings.Contains(res.Error, "sudo apt-get install -y fd-find ripgrep") {
		t.Errorf("error should carry the manual fallback command: %q", res.Error)
	}
}
