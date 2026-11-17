package wslsetup

import (
	"errors"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// stubSearchEnv wires a fake runner for InstallSearchTools: search probes
// return the FIFO statuses (last value holds), cache resets are counted, and
// rootFn answers the apt scripts. The returned funcs are race-safe getters.
func stubSearchEnv(t *testing.T, statuses []platform.WSLSearchTools, rootFn func(script string, timeout time.Duration) (string, error)) (f *fakeWSL, probes func() int, resets func() int) {
	t.Helper()
	f = &fakeWSL{distro: "Ubuntu-Test"}
	probeCount, resetCount := 0, 0
	f.search = func(string) platform.WSLSearchTools {
		probeCount++
		if probeCount <= len(statuses) {
			return statuses[probeCount-1]
		}
		if len(statuses) > 0 {
			return statuses[len(statuses)-1]
		}
		return platform.WSLSearchTools{}
	}
	f.reset = func(string) { resetCount++ }
	f.root = func(_ *fakeWSL, script string, timeout time.Duration) (string, error) {
		if rootFn != nil {
			return rootFn(script, timeout)
		}
		return "ok\n", nil
	}
	injectFake(t, f)
	return f, func() int { return probeCount }, func() int { return resetCount }
}

func rootScriptsOf(calls []fakeCall) []fakeCall {
	var out []fakeCall
	for _, c := range calls {
		if c.kind == "root" {
			out = append(out, c)
		}
	}
	return out
}

// TestInstallSearchToolsAlreadyOK 已装短路：探测全命中时不跑 apt，只做一次
// 安装前缓存失效（刷新 stale 快照）。
func TestInstallSearchToolsAlreadyOK(t *testing.T) {
	f, probes, resets := stubSearchEnv(t, []platform.WSLSearchTools{{FD: true, Ripgrep: true}}, nil)

	res, err := NewService(nil).InstallSearchTools()
	if err != nil {
		t.Fatalf("InstallSearchTools: %v", err)
	}
	if !res.Success || !res.AlreadyOK {
		t.Fatalf("result = %+v, want already-ok success", res)
	}
	if probes() != 1 || resets() != 1 {
		t.Errorf("probes=%d resets=%d, want 1/1 (pre-check only)", probes(), resets())
	}
	if roots := rootScriptsOf(f.recorded()); len(roots) != 0 {
		t.Errorf("no apt must run when already installed, got %v", roots)
	}
	if res.Tool != SearchToolsKey || res.Package != "fd-find ripgrep" {
		t.Errorf("tool/package = %q/%q", res.Tool, res.Package)
	}
}

// TestInstallSearchToolsInstallFlow 安装主流程：apt 脚本含 update 与
// fd-find/ripgrep 且受 aptOpTimeout 约束；安装前后各失效一次缓存；验证探测
// 命中后 Success。
func TestInstallSearchToolsInstallFlow(t *testing.T) {
	f, probes, resets := stubSearchEnv(t,
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
	if probes() != 2 || resets() != 2 {
		t.Errorf("probes=%d resets=%d, want 2/2 (pre + post)", probes(), resets())
	}
	roots := rootScriptsOf(f.recorded())
	if len(roots) != 1 {
		t.Fatalf("apt scripts = %v, want exactly one", roots)
	}
	script := roots[0].script
	for _, want := range []string{"DEBIAN_FRONTEND=noninteractive", "apt-get update -qq", "apt-get install -y fd-find ripgrep"} {
		if !strings.Contains(script, want) {
			t.Errorf("apt script missing %q:\n%s", want, script)
		}
	}
	if roots[0].timeout != aptOpTimeout {
		t.Errorf("apt script timeout = %v, want %v", roots[0].timeout, aptOpTimeout)
	}
}

// TestInstallSearchToolsAptFailure apt 失败：Success=false，Error 带原因；
// 非 apt 系（command not found）额外给手动包管理器指引。
func TestInstallSearchToolsAptFailure(t *testing.T) {
	t.Run("generic apt failure", func(t *testing.T) {
		_, _, _ = stubSearchEnv(t, []platform.WSLSearchTools{{}}, func(_ string, _ time.Duration) (string, error) {
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
		_, _, _ = stubSearchEnv(t, []platform.WSLSearchTools{{}}, func(_ string, _ time.Duration) (string, error) {
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
	_, _, _ = stubSearchEnv(t, []platform.WSLSearchTools{{}, {Ripgrep: true}}, nil)

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

// TestInstallSearchToolsNoDistro covers the no-usable-distro contract for the
// search-tools entry (structured failure, zero distro side effects).
func TestInstallSearchToolsNoDistro(t *testing.T) {
	f := injectFake(t, &fakeWSL{distro: ""})
	res, err := NewService(nil).InstallSearchTools()
	if err != nil {
		t.Fatalf("InstallSearchTools: %v", err)
	}
	if res.Success || res.Error != "no usable WSL distro installed" || res.Tool != SearchToolsKey {
		t.Fatalf("result = %+v, want structured no-distro failure", res)
	}
	if calls := f.recorded(); len(calls) != 0 {
		t.Errorf("no distro means no side effects, got %d runner calls", len(calls))
	}
}
