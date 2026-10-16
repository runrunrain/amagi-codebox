//go:build windows

package main

// pi_wsl_provider_build_windows_test.go — Windows 全链路断言（P3-S1 验收 3）：
// WSL 内嵌启动 API-provider 会话且选中 provider 不可构建（无 baseURL）→
// 阻断启动并返回中文报错，不静默换默认模型。wslDistro 判定是 Windows 专属
// 路径（EmbeddedLaunchTargetsWSL 非 Windows 恒 false），故本测 Windows-only；
// macOS CI 不执行（与 wslsetup 播种测试同布局限制，报告已披露）。

import (
	"os"
	"strings"
	"testing"

	"amagi-codebox/internal/config"
	"amagi-codebox/internal/platform"
)

func TestWSLPiProviderBuildFailureBlocksLaunch(t *testing.T) {
	if os.Getenv("AMAGI_WSL_E2E") == "" && !windowsTestWSLAvailable() {
		t.Skip("no usable WSL distro; set AMAGI_WSL_E2E=1 to force")
	}
	home := isolatedHome(t)
	app := newTestApp(t)
	app.ctx = t.Context()
	app.Capabilities = fakePlatformCaps()
	// 无 baseURL 的 provider：BuildPiModelsConfig 必然失败（选中 provider 不可构建）。
	if err := app.Config.SaveProvider("no-base-url", config.Provider{}); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	app.CLIResolver = &piLoginWSLShellResolver{} // WSL 形态 spec（镜像判定）
	_, err := app.LaunchPiSession("some-model", "no-base-url", "embedded", home, "")
	if err == nil {
		t.Fatal("unbuildable selected provider must block WSL launch")
	}
	if !strings.Contains(err.Error(), "无法构建 Pi 供应商") || !strings.Contains(err.Error(), "未改用默认模型") {
		t.Fatalf("block error must be the approved chinese text, got %v", err)
	}
	// F1 锁可用性（真实全链路）：阻断 return 后 providerSyncMu 必须立即可用
	//（泄漏则此后 provider 保存/Pi 启动/SaveOpenCodeConfig 全部挂死）。
	if !app.providerSyncMu.TryLock() {
		t.Fatal("providerSyncMu leaked by the WSL build-failure block (F1)")
	}
	app.providerSyncMu.Unlock()
	// 二次启动同样错误返回（而非挂死）。
	if _, err := app.LaunchPiSession("some-model", "no-base-url", "embedded", home, ""); err == nil || !strings.Contains(err.Error(), "无法构建 Pi 供应商") {
		t.Fatalf("second launch must fail fast with the same chinese error, got %v", err)
	}
	if !app.providerSyncMu.TryLock() {
		t.Fatal("providerSyncMu leaked on the second WSL build-failure block")
	}
	app.providerSyncMu.Unlock()
}

func windowsTestWSLAvailable() bool { return platform.DefaultWSLDistro(nil) != "" }
