//go:build windows

package dockerwsl

import (
	"os"
	"testing"
)

// TestDockerWSLHealthSmoke 真机冒烟（AMAGI_DOCKERWSL_SMOKE=1 启用）：直接跑
// GetHealth 验证 wsl.exe / tasklist.exe / docker.exe 接缝在真实 Windows +
// Docker Desktop 环境下的解析（UTF-16 解码、CSV 计数、stat 字节数）。
// 默认跳过——CI（macOS）与本机常规测试不依赖 Docker 环境。
func TestDockerWSLHealthSmoke(t *testing.T) {
	if os.Getenv("AMAGI_DOCKERWSL_SMOKE") != "1" {
		t.Skip("set AMAGI_DOCKERWSL_SMOKE=1 to run the real-machine smoke")
	}
	s := NewService(nil)
	rep := s.GetHealth()
	t.Logf("health: available=%v distro=%q mount=%s(%d) engine=%v(%s) tool=%v/%v procs=%d issues=%v selfHeal=%v",
		rep.Available, rep.DefaultDistro, rep.IntegrationMountState, rep.IntegrationMountBytes,
		rep.EngineReady, rep.EngineVersion, rep.ToolDistroPresent, rep.ToolDistroRunning,
		rep.DesktopProcessesRunning, rep.Issues, rep.RecommendSelfHeal)
	if !rep.Available {
		t.Skip("Docker Desktop not installed on this machine")
	}
	if rep.DefaultDistro == "" {
		t.Error("default distro should resolve via platform.DefaultWSLDistro")
	}
	if rep.IntegrationMountState == MountUnknown {
		t.Error("integration mount state should resolve (ok/broken/absent), got unknown")
	}
	if rep.DesktopProcessesRunning < 0 {
		t.Error("process count must be >= 0")
	}
}
