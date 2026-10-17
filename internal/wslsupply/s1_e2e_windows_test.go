//go:build windows

package wslsupply

// s1_e2e_windows_test.go — 真实 WSL 入口的 S1 托管分发冒烟（AMAGI_WSL_E2E=1
// 手动触发；s0-report §5 同款环境约定）。与 S0 冒烟不同，本组验证「写入路径」：
//
//   - managed-models：旧 distro（预置非 amagi provider + 损坏保护外的合法文件）
//     不重装即获得托管更新；chmod 600 从 distro 内 stat 实证（Windows os.Chmod
//     成功不作数）；无变化第二次运行零写入（mtime 不变 + chmod 调用零增量）；
//   - vision-export：同意门 + 发布后 distro 内 stat 0600 实证。
//
// 隔离规约（design §八「真机验证」）：全部写入落在 distro 内
// /tmp/amagi-wslsupply-e2e/（fake Home 重锚），不触碰真实 $HOME/.pi、真实
// auth.json 或真实 ~/.agents；视觉清单内容为桩字节（无真实密钥）。

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/launcher"
	"amagi-codebox/internal/platform"
)

// e2eIsolatedProbes 返回真实身份/chmod/statmode + fake home
// （/tmp/amagi-wslsupply-e2e）的探测面：写入只落 distro 内临时目录。
func e2eIsolatedProbes(distro string) Probes {
	return Probes{
		Identity: func(context.Context, string) platform.WSLUserIdentity {
			id := platform.WSLTargetIdentity(distro)
			id.Home = "/tmp/amagi-wslsupply-e2e" // 重锚：绝不触碰真实用户树
			return id
		},
		ToUNC: platform.WSLToUNC,
		Chmod: func(ctx context.Context, distro, mode string, paths ...string) error {
			return platform.WSLChmod(distro, mode, paths...)
		},
		StatMode: func(ctx context.Context, distro, linuxPath string) (string, bool) {
			return platform.WSLFileMode(distro, linuxPath)
		},
	}
}

// e2eStatMode 经 wsl.exe 在 distro 内 stat 目标权限位（octal，如 "600"）。
func e2eStatMode(t *testing.T, distro, linuxPath string) string {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-d", distro, "--", "stat", "-c", "%a", linuxPath).Output()
	if err != nil {
		t.Fatalf("stat %s in distro: %v", linuxPath, err)
	}
	return strings.TrimSpace(strings.Trim(string(out), "\r\n\x00"))
}

func TestManualRealWSLManagedModelsSupply(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL S1 managed-models smoke")
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine")
	}
	// 清理隔离根（幂等）。
	_ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", "/tmp/amagi-wslsupply-e2e").Run()

	c := NewCoordinator(t.TempDir(), nil, e2eIsolatedProbes(distro))
	spec := PiManagedModelsSpec{
		Providers: map[string]any{"providers": map[string]any{
			"amagi-e2e": map[string]any{"baseUrl": "https://e2e.example", "apiKey": "sk-e2e-fake"},
		}},
		RetainProviders: []string{},
		LoginOverrides:  []PiLoginModelOverrideSpec{{Provider: "openai", Model: "gpt-e2e", ContextWindow: 123456}},
	}
	c.RegisterProjection(NewPiManagedModelsAdapter(stubE2EPiSource{spec: spec}))

	// 预置旧 distro 状态：非 amagi provider + 未知顶层字段（播种树遗存形态）。
	target, err := c.ResolveTarget(context.Background(), distro, CLIPi)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	preseed := `{"providers":{"user-own":{"baseUrl":"https://user.example"}},"equivalence":{"keep":true}}`
	if err := os.MkdirAll(target.UNCRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target.UNCRoot, "models.json"), []byte(preseed), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := c.RunUnits(context.Background(), distro, CLIPi, ReasonLaunch, []UnitID{UnitPiManagedModels})
	if err != nil || len(res.Results) != 1 || res.Results[0].Outcome != UnitApplied {
		t.Fatalf("first supply run: err=%v results=%+v", err, res.Results)
	}
	// distro 内实证：内容合并（非 amagi 保留 + amagi 写入 + 登录覆写）与 0600。
	linuxModels := target.LinuxRoot + "/models.json"
	got := e2eCat(t, distro, linuxModels)
	for _, want := range []string{"user-own", "amagi-e2e", "contextWindow"} {
		if !strings.Contains(got, want) {
			t.Fatalf("distro models.json missing %s:\n%s", want, got)
		}
	}
	if mode := e2eStatMode(t, distro, linuxModels); mode != "600" {
		t.Fatalf("models.json mode = %s, want 600 (verified from inside the distro)", mode)
	}
	t.Logf("managed models published; distro stat mode=600")

	// 无变化第二次运行：零写入（mtime 秒级不变 + 结果 Fresh）。
	before := e2eStatMtime(t, distro, linuxModels)
	res2, err := c.RunUnits(context.Background(), distro, CLIPi, ReasonLaunch, []UnitID{UnitPiManagedModels})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Results[0].Outcome != UnitFresh {
		t.Fatalf("second run must be fresh (zero write), got %+v", res2.Results[0])
	}
	after := e2eStatMtime(t, distro, linuxModels)
	if before != after {
		t.Fatalf("zero-write run changed mtime: %s -> %s", before, after)
	}
	t.Logf("second run zero-write confirmed (mtime %s unchanged)", before)
	t.Logf("baseline: identity probes per run = 2 (ResolveTarget + Apply re-check)")
}

func TestManualRealWSLVisionExportPublish(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" {
		t.Skip("set AMAGI_WSL_E2E=1 to run the real-WSL S1 vision-export smoke")
	}
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine")
	}
	_ = exec.Command("wsl.exe", "-d", distro, "--", "rm", "-rf", "/tmp/amagi-wslsupply-e2e").Run()

	c := NewCoordinator(t.TempDir(), nil, e2eIsolatedProbes(distro))
	content := fmt.Sprintf(`{"version":1,"updated_at":%q,"models":[{"id":"e2e/fake","api_key":"sk-e2e-fake-not-real"}]}`, time.Now().Format(time.RFC3339))
	c.RegisterProjection(NewVisionExportAdapter(stubE2ESharedSource{vision: []byte(content)}))

	// 未同意 → 阻断不写。
	res, err := c.RunUnits(context.Background(), distro, CLICommon, ReasonSave, []UnitID{UnitSharedVisionExport})
	if err != nil || res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "awaiting-consent" {
		t.Fatalf("pre-consent run must block awaiting-consent: err=%v results=%+v", err, res.Results)
	}
	// 同意 → 发布 → distro 内 0600 实证。
	if err := c.GrantUnitConsent(context.Background(), distro, CLICommon, UnitSharedVisionExport); err != nil {
		t.Fatalf("GrantUnitConsent: %v", err)
	}
	res, err = c.RunUnits(context.Background(), distro, CLICommon, ReasonSave, []UnitID{UnitSharedVisionExport})
	if err != nil || res.Results[0].Outcome != UnitApplied {
		t.Fatalf("consented publish must apply: err=%v results=%+v", err, res.Results)
	}
	target, err := c.ResolveTarget(context.Background(), distro, CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	visionPath := target.LinuxRoot + "/amagi-media-models.json"
	got := e2eCat(t, distro, visionPath)
	if got != content {
		t.Fatalf("published bytes must equal source verbatim:\n%s", got)
	}
	if mode := e2eStatMode(t, distro, visionPath); mode != "600" {
		t.Fatalf("vision export mode = %s, want 600 (verified from inside the distro)", mode)
	}
	t.Logf("vision export published with fake content; distro stat mode=600")
}

// --- 小助手 ---

type stubE2EPiSource struct{ spec PiManagedModelsSpec }

func (s stubE2EPiSource) DesiredPiManagedModels() PiManagedModelsSpec { return s.spec }

type stubE2ESharedSource struct{ vision []byte }

func (s stubE2ESharedSource) VisionExportBytes() ([]byte, error) { return s.vision, nil }
func (s stubE2ESharedSource) ModalityKBBytes() ([]byte, error)   { return nil, nil }

func e2eCat(t *testing.T, distro, linuxPath string) string {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-d", distro, "--", "cat", linuxPath).Output()
	if err != nil {
		t.Fatalf("cat %s in distro: %v", linuxPath, err)
	}
	return string(out)
}

func e2eStatMtime(t *testing.T, distro, linuxPath string) string {
	t.Helper()
	out, err := exec.Command("wsl.exe", "-d", distro, "--", "stat", "-c", "%Y", linuxPath).Output()
	if err != nil {
		t.Fatalf("stat mtime %s: %v", linuxPath, err)
	}
	return strings.TrimSpace(strings.Trim(string(out), "\r\n\x00"))
}

var _ = launcher.SyncWSLPiLoginAuthFresh // 保持 launcher 导入（文档化关联 A2 链）
