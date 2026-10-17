//go:build windows

package wslsupply

// core_staging_e2e_windows_test.go — SA-06 私有 staging 提交原语的真机
//（Windows + WSL）验证面：AMAGI_WSL_E2E=1 手动触发，隔离规约与
// s1_e2e_windows_test.go 相同（写入只落 distro 内 /tmp/amagi-wslsupply-e2e，
// 秘密字节为合成桩，无真实密钥）。
//
// 验证点（对应 supply-audit SA-06 最小修复方向）：
//   - 敏感提交后 distro 内 stat 实证：staging 目录 0700、最终文件 0600；
//   - chmod 失败注入（Chmod 探测面报错）时既有混合配置原样保留（SA-07；
//     配 fake Chmod 但真实 StatMode，验证「未验证即不写」的 fail-closed 面）；
//   - 第二 Linux 用户观测整个写入窗口的完整取证脚本见
//     agent-outputs/workflow/wsl-goal-closure/verify-staging-window.ps1
//     （需要第二个 Linux 用户，超出本测试自动化面——明确边界，不冒充）。

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

func TestManualRealWSLStagingIsolation(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL SA-06 staging smoke")
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine")
	}
	_ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", "/tmp/amagi-wslsupply-e2e").Run()
	t.Cleanup(func() {
		_ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", "/tmp/amagi-wslsupply-e2e").Run()
	})

	probes := e2eIsolatedProbes(distro)
	c := NewCoordinator(t.TempDir(), nil, probes)
	spec := PiManagedModelsSpec{
		Providers: map[string]any{"providers": map[string]any{
			"amagi-e2e": map[string]any{"baseUrl": "https://e2e.example", "apiKey": "sk-e2e-fake"},
		}},
	}
	c.RegisterProjection(NewPiManagedModelsAdapter(stubE2EPiSource{spec: spec}))

	target, err := c.ResolveTarget(context.Background(), distro, CLIPi)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	// 预置混合配置（用户自有 provider + 用户字段）——SA-07 的前像。
	mixed := `{"providers":{"user-own":{"baseUrl":"https://user.example"}},"userSetting":true}`
	if err := os.MkdirAll(target.UNCRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target.UNCRoot, "models.json"), []byte(mixed), 0o600); err != nil {
		t.Fatal(err)
	}

	if res, err := c.RunUnits(context.Background(), distro, CLIPi, ReasonLaunch, []UnitID{UnitPiManagedModels}); err != nil ||
		len(res.Results) != 1 || res.Results[0].Outcome != UnitApplied {
		t.Fatalf("supply run: err=%v results=%+v", err, res.Results)
	}
	// distro 内实证（Windows os.Chmod/UNC stat 不作数）：staging 700 + 文件 600。
	if mode := e2eStatMode(t, distro, target.LinuxRoot+"/.amagi-staging"); mode != "700" {
		t.Fatalf("staging dir mode = %q, want 700 (distro-side stat)", mode)
	}
	if mode := e2eStatMode(t, distro, target.LinuxRoot+"/models.json"); mode != "600" {
		t.Fatalf("final file mode = %q, want 600 (distro-side stat)", mode)
	}

	// SA-07：chmod 故障注入（真实 StatMode）→ 既有混合配置必须原样保留。
	c2 := NewCoordinator(t.TempDir(), nil, Probes{
		Identity: probes.Identity,
		ToUNC:    probes.ToUNC,
		Chmod:    func(context.Context, string, string, ...string) error { return errors.New("injected chmod failure") },
		StatMode: func(ctx context.Context, distro, linuxPath string) (string, bool) {
			return platform.WSLFileMode(distro, linuxPath)
		},
	})
	c2.RegisterProjection(NewPiManagedModelsAdapter(stubE2EPiSource{spec: PiManagedModelsSpec{
		Providers: map[string]any{"providers": map[string]any{
			"amagi-e2e2": map[string]any{"baseUrl": "https://e2e2.example", "apiKey": "sk-e2e-fake"},
		}},
	}}))
	res, err := c2.RunUnits(context.Background(), distro, CLIPi, ReasonLaunch, []UnitID{UnitPiManagedModels})
	if err != nil || len(res.Results) != 1 || res.Results[0].Outcome != UnitFailed {
		t.Fatalf("chmod failure must fail the unit: err=%v results=%+v", err, res.Results)
	}
	got := e2eCat(t, distro, target.LinuxRoot+"/models.json")
	if !strings.Contains(got, "user-own") || !strings.Contains(got, "userSetting") {
		t.Fatalf("pre-existing user content must be preserved on chmod failure:\n%s", got)
	}
	if strings.Contains(got, "amagi-e2e2") {
		t.Fatalf("new secret content must NOT be committed when chmod fails:\n%s", got)
	}
}
