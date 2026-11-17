package platform

// wsl_launch_probe_test.go — WSL 启动预检融合探测的钉子：脚本融合形态（身份
// 三行 + 目录校验 + 定谳标记）、正常解析、目录标记缺失、身份三行异常、标记后
// 路径非法、runner 失败与空 distro 的 fail closed。全部经注入
// wslScriptRunnerContext 假 runner 运行，不触达真实 wsl.exe。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// probeOutput 拼一段融合脚本的典型成功输出：身份三行 + 目录 OK 标记行 + 定谳段。
func probeOutput(uid, home, machineID, resolved string) []byte {
	head := uid + "\n" + home + "\n"
	if machineID != "" {
		head += machineID + "\n"
	}
	return []byte(head + "\n__AMAGI_WORKDIR_OK__\n" + wslResolvedDirectoryMarker + resolved + "\x00")
}

func TestWSLLaunchPreflightProbeParsesFusedOutput(t *testing.T) {
	orig := wslScriptRunnerContext
	t.Cleanup(func() { wslScriptRunnerContext = orig })
	var gotScript string
	wslScriptRunnerContext = func(_ context.Context, distro, script string) ([]byte, error) {
		if distro != "Ubuntu" {
			t.Fatalf("wrong distro: %q", distro)
		}
		gotScript = script
		return probeOutput("1000", "/home/u", "6b1c1f0e8a2d4f3b", "/home/u/proj"), nil
	}
	pf, err := WSLLaunchPreflightProbe(context.Background(), " Ubuntu ", "~/proj")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !pf.Identity.OK || pf.Identity.UID != "1000" || pf.Identity.Home != "/home/u" || pf.Identity.MachineID != "6b1c1f0e8a2d4f3b" {
		t.Fatalf("identity = %+v", pf.Identity)
	}
	if pf.ResolvedWorkDir != "/home/u/proj" {
		t.Fatalf("resolved dir = %q", pf.ResolvedWorkDir)
	}
	// 脚本融合形态：身份三行在前，目录校验与定谳标记在后（一次调用两份事实）。
	for _, want := range []string{"id -u", "cat /etc/machine-id", "__AMAGI_WORKDIR_OK__", "__AMAGI_RESOLVED_DIRECTORY__"} {
		if !strings.Contains(gotScript, want) {
			t.Fatalf("fused script missing %q: %q", want, gotScript)
		}
	}
	if !strings.HasPrefix(gotScript, wslIdentityScript+"\n") {
		t.Fatalf("identity script must lead the fused script: %q", gotScript)
	}
}

func TestWSLLaunchPreflightProbeMachineIDOptional(t *testing.T) {
	orig := wslScriptRunnerContext
	t.Cleanup(func() { wslScriptRunnerContext = orig })
	wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
		return probeOutput("1000", "/home/u", "", "/home/u"), nil
	}
	pf, err := WSLLaunchPreflightProbe(context.Background(), "Ubuntu", "")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !pf.Identity.OK || pf.Identity.MachineID != "" || pf.ResolvedWorkDir != "/home/u" {
		t.Fatalf("preflight = %+v", pf)
	}
}

// TestWSLLaunchPreflightProbeFailClosed 覆盖失败形态：runner 错误、目录标记缺失、
// 身份三行异常、标记后路径非法（非 / 前缀 / 缺 NUL 结尾）——全部返回携带
// distro 与 workDir 的中文错误，不把失败当空值。
func TestWSLLaunchPreflightProbeFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		out     []byte
		runErr  error
		wantSub []string
	}{
		{"runner error", nil, errors.New("exit 43"), []string{"Ubuntu", `"/mnt/c/p"`, "不可用"}},
		{"directory marker missing", []byte("1000\n/home/u\nmid\n\n__AMAGI_WORKDIR_OK__\n"), nil, []string{"未取得目录定谳标记"}},
		{"identity uid not numeric", probeOutput("ubuntu", "/home/u", "", "/home/u"), nil, []string{"身份解析失败"}},
		{"identity home not absolute", probeOutput("1000", "home/u", "", "/home/u"), nil, []string{"身份解析失败"}},
		{"identity missing home line", []byte("1000\n" + wslResolvedDirectoryMarker + "/home/u\x00"), nil, []string{"身份解析失败"}},
		{"resolved path relative", []byte("1000\n/home/u\n" + wslResolvedDirectoryMarker + "home/u\x00"), nil, []string{"无效路径"}},
		{"resolved path unterminated", []byte("1000\n/home/u\n" + wslResolvedDirectoryMarker + "/home/u"), nil, []string{"不完整"}},
		{"resolved path embeds NUL", []byte("1000\n/home/u\n" + wslResolvedDirectoryMarker + "/home/u\x00extra\x00"), nil, []string{"无效路径"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := wslScriptRunnerContext
			t.Cleanup(func() { wslScriptRunnerContext = orig })
			wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
				return tc.out, tc.runErr
			}
			pf, err := WSLLaunchPreflightProbe(context.Background(), "Ubuntu", "/mnt/c/p")
			if err == nil {
				t.Fatalf("expected fail-closed error, got %+v", pf)
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q missing %q", err.Error(), sub)
				}
			}
			if !strings.Contains(err.Error(), "Ubuntu") {
				t.Fatalf("error must name the distro: %q", err.Error())
			}
		})
	}
}

func TestWSLLaunchPreflightProbeEmptyDistro(t *testing.T) {
	orig := wslScriptRunnerContext
	wslScriptRunnerContext = func(context.Context, string, string) ([]byte, error) {
		t.Fatal("probe must not run for empty distro")
		return nil, nil
	}
	t.Cleanup(func() { wslScriptRunnerContext = orig })
	if _, err := WSLLaunchPreflightProbe(context.Background(), "   ", "/proj"); err == nil || !strings.Contains(err.Error(), "没有可用的发行版") {
		t.Fatalf("empty distro must fail closed, got %v", err)
	}
}
